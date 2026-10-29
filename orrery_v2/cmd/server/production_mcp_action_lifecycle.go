package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"orrery_v2/internal/app/actioncontract"
	mcptransport "orrery_v2/internal/transport/mcp"
)

const productionMCPActionTypeID actioncontract.ActionTypeID = "mcp.tool.call"

type productionMCPActionLifecycle struct {
	service *actioncontract.Service
	ids     ontologyActionIDGenerator
}

type productionMCPActionAttempt struct {
	service         *actioncontract.Service
	runID           actioncontract.ActionRunID
	receiptID       actioncontract.WritebackReceiptID
	providerCallRef actioncontract.Ref
	targetSystem    string
	targetRefs      []actioncontract.ObjectRef
	idempotencyKey  string
	auditRef        actioncontract.Ref
}

func newProductionMCPActionLifecycle(service *actioncontract.Service, ids ontologyActionIDGenerator) mcptransport.ToolCallLifecycle {
	return &productionMCPActionLifecycle{service: service, ids: ids}
}

func (l *productionMCPActionLifecycle) StartToolCall(ctx context.Context, req mcptransport.ToolCallLifecycleStart) (mcptransport.ToolCallAttempt, error) {
	if l == nil || l.service == nil || l.ids == nil {
		return nil, fmt.Errorf("MCP action lifecycle dependencies are required")
	}
	toolID := strings.TrimSpace(req.ToolID)
	if toolID == "" {
		return nil, fmt.Errorf("MCP action lifecycle tool id is required")
	}
	argsHash, err := canonicalMCPValueHash(req.Arguments)
	if err != nil {
		return nil, fmt.Errorf("hash MCP tool arguments: %w", err)
	}
	generated, err := l.ids.NewIDContext()
	if err != nil {
		return nil, fmt.Errorf("generate MCP action lifecycle id: %w", err)
	}
	generated = strings.TrimSpace(generated)
	if generated == "" {
		return nil, fmt.Errorf("generate MCP action lifecycle id: empty id")
	}

	actor, effectiveUser := productionMCPActionSubjects(req)
	targetRefs := []actioncontract.ObjectRef{{Kind: "tool", ID: toolID}}
	if serverID := strings.TrimSpace(req.ServerToolID); serverID != "" {
		targetRefs = append(targetRefs, actioncontract.ObjectRef{Kind: "mcp_server", ID: serverID})
	}
	if runID := strings.TrimSpace(req.RunID); runID != "" {
		targetRefs = append(targetRefs, actioncontract.ObjectRef{Kind: "agent_run", ID: agentRunOntologyID(runID)})
	}
	if responsibility := req.ResponsibilityContext; responsibility != nil {
		targetRefs = append(targetRefs,
			actioncontract.ObjectRef{Kind: "responsibility_assignment", ID: strings.TrimSpace(responsibility.ResponsibilityID)},
			actioncontract.ObjectRef{Kind: "responsibility_context", ID: strings.TrimSpace(responsibility.ContextID)},
			actioncontract.ObjectRef{Kind: "responsibility_policy", ID: fmt.Sprintf("%s:v%d", strings.TrimSpace(responsibility.PolicyID), responsibility.PolicyVersion)},
		)
	}
	auditRef := actioncontract.Ref{}
	if correlationID := strings.TrimSpace(req.CorrelationID); correlationID != "" {
		auditRef = actioncontract.Ref{Kind: "mcp_audit_correlation", ID: correlationID}
	}
	runID := actioncontract.ActionRunID("action-run:" + generated)
	providerCallRef := actioncontract.Ref{Kind: "mcp_tool_call", ID: "mcp-call:" + generated}
	purpose := "mcp_tool_execution"
	if req.Maintenance {
		purpose = "mcp_tool_maintenance"
	} else if req.ResponsibilityContext != nil {
		purpose = "responsibility_mcp_tool_execution"
	}
	if _, err := l.service.StartActionRun(ctx, actioncontract.StartRunRequest{
		RunID:         runID,
		TenantID:      firstNonBlank(strings.TrimSpace(req.TenantID), "system"),
		ActionTypeID:  productionMCPActionTypeID,
		Actor:         actor,
		EffectiveUser: effectiveUser,
		Runtime:       actioncontract.RuntimeRef{Kind: actioncontract.RuntimeMCPGateway, ID: "mcp-gateway"},
		Purpose:       purpose,
		ObjectRefs:    targetRefs,
		ArgsHash:      argsHash,
		AuditRef:      auditRef,
	}); err != nil {
		return nil, err
	}

	return &productionMCPActionAttempt{
		service:         l.service,
		runID:           runID,
		receiptID:       actioncontract.WritebackReceiptID("writeback-receipt:" + generated),
		providerCallRef: providerCallRef,
		targetSystem:    productionMCPWritebackTargetSystem(req),
		targetRefs:      append([]actioncontract.ObjectRef(nil), targetRefs...),
		idempotencyKey:  productionMCPIdempotencyKey(req.Arguments, argsHash),
		auditRef:        auditRef,
	}, nil
}

func (a *productionMCPActionAttempt) CompleteToolCall(ctx context.Context, result mcptransport.CallToolResult) error {
	if a == nil || a.service == nil {
		return fmt.Errorf("MCP action attempt is required")
	}
	resultHash, err := canonicalMCPValueHash(result.Output)
	if err != nil {
		hashErr := fmt.Errorf("hash MCP tool result: %w", err)
		closeErr := a.FailToolCall(ctx, mcptransport.ToolCallFailure{
			Code: "invalid_tool_result", Message: "MCP tool result could not be recorded",
		})
		return errors.Join(hashErr, closeErr)
	}
	resultRef := actioncontract.Ref{Kind: "mcp_tool_result", ID: resultHash}
	_, _, err = a.service.CompleteActionRun(ctx, actioncontract.CompleteRunRequest{
		RunID:           a.runID,
		ProviderCallRef: a.providerCallRef,
		ResultRef:       resultRef,
		Writeback: &actioncontract.WritebackReceipt{
			ID:                 a.receiptID,
			TargetSystem:       a.targetSystem,
			TargetResourceRefs: append([]actioncontract.ObjectRef(nil), a.targetRefs...),
			AfterHash:          resultHash,
			IdempotencyKey:     a.idempotencyKey,
			Status:             actioncontract.WritebackStatusSucceeded,
			EvidenceRefs:       []actioncontract.Ref{resultRef},
			AuditRef:           a.auditRef,
		},
	})
	return err
}

func (a *productionMCPActionAttempt) FailToolCall(ctx context.Context, failure mcptransport.ToolCallFailure) error {
	if a == nil || a.service == nil {
		return fmt.Errorf("MCP action attempt is required")
	}
	errorCode := firstNonBlank(strings.TrimSpace(failure.Code), "tool_execution_failed")
	errorMessage := firstNonBlank(strings.TrimSpace(failure.Message), "MCP tool execution failed")
	_, err := a.service.FailActionRun(ctx, actioncontract.FailRunRequest{
		RunID:           a.runID,
		ProviderCallRef: a.providerCallRef,
		ErrorCode:       errorCode,
		ErrorMessage:    errorMessage,
	})
	return err
}

func productionMCPActionSubjects(req mcptransport.ToolCallLifecycleStart) (actioncontract.ActionSubject, actioncontract.ActionSubject) {
	principalID := strings.TrimSpace(req.PrincipalID)
	if agentID := strings.TrimSpace(req.AgentID); agentID != "" {
		actor := actioncontract.ActionSubject{Kind: actioncontract.SubjectAgent, ID: agentID}
		if principalID != "" && principalID != agentID {
			return actor, actioncontract.ActionSubject{Kind: actioncontract.SubjectHuman, ID: principalID}
		}
		return actor, actioncontract.ActionSubject{}
	}
	if profileID := strings.TrimSpace(req.ProfileID); profileID != "" {
		actorID := firstNonBlank(principalID, profileID)
		actor := actioncontract.ActionSubject{Kind: actioncontract.SubjectHuman, ID: actorID}
		return actor, actor
	}
	return actioncontract.ActionSubject{Kind: actioncontract.SubjectService, ID: firstNonBlank(principalID, "mcp-gateway")}, actioncontract.ActionSubject{}
}

func productionMCPWritebackTargetSystem(req mcptransport.ToolCallLifecycleStart) string {
	if req.Source == mcptransport.ToolSourceExternalMCP {
		return firstNonBlank(strings.TrimSpace(req.ServerToolID), "mcp_gateway")
	}
	if req.Source == mcptransport.ToolSourceInternal {
		return "orrery"
	}
	return "mcp_gateway"
}

func productionMCPIdempotencyKey(arguments map[string]any, argsHash string) string {
	if value, ok := arguments["idempotency_key"].(string); ok {
		value = strings.TrimSpace(value)
		if value != "" && len(value) <= 512 {
			return value
		}
	}
	return argsHash
}

func canonicalMCPValueHash(value any) (string, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}
