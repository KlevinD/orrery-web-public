package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"orrery_v2/internal/app/identity"
	"orrery_v2/internal/app/ontologyreasoning"
	toolstd "orrery_v2/internal/resource/tool/std"
	mcptransport "orrery_v2/internal/transport/mcp"
)

type inferenceCandidateWorkflow interface {
	CandidateWorkflow(context.Context, identity.Principal, string, ontologyreasoning.WorkflowRequest) (ontologyreasoning.CandidateState, error)
}

type inferenceRulesWorkflow interface {
	InferenceRules(context.Context, identity.Principal, ontologyreasoning.RuleSetRequest) ([]ontologyreasoning.RuleSet, error)
}

type ontologyGroundedContextReader interface {
	GroundedContext(context.Context, identity.Principal, ontologyreasoning.GroundedContextRequest) (ontologyreasoning.GroundedContext, error)
}

func (e productionOntologyStewardToolExecutor) executeInferenceTool(ctx context.Context, toolID string, req mcptransport.OntologyToolRequest) (mcptransport.OntologyToolResult, error) {
	limit := 2 * 1024 * 1024
	if toolID == string(toolstd.OntologyInferenceWorkflow) {
		limit = 16 * 1024
	}
	if e.inference == nil || len(req.InferenceJSON) > limit {
		return mcptransport.OntologyToolResult{}, fmt.Errorf("ontology inference is unavailable or input exceeds budget")
	}
	agentID := strings.TrimSpace(firstNonBlank(req.AgentID, req.PrincipalID))
	if agentID == "" || strings.TrimSpace(req.TenantID) == "" {
		return mcptransport.OntologyToolResult{}, fmt.Errorf("ontology inference requires a runtime agent and tenant")
	}
	// Identity and permission bounds come from the runtime, never model arguments.
	principal := identity.NormalizePrincipal(identity.Principal{SubjectProvider: "orrery-agent-runtime", Subject: agentID, AgentProfileID: agentID, ViewerKind: identity.ViewerKindAgent, TenantID: req.TenantID, CoarseScopes: append([]string(nil), req.Permissions...), CredentialScopes: append([]string(nil), req.Permissions...), CredentialScopeRestricted: true})
	if toolID == string(toolstd.OntologyDocumentInference) {
		return e.executeDocumentInference(ctx, principal, req)
	}
	decoder := json.NewDecoder(bytes.NewReader(req.InferenceJSON))
	decoder.DisallowUnknownFields()
	if toolID == string(toolstd.OntologyGroundedContext) {
		reader, ok := e.inference.(ontologyGroundedContextReader)
		if !ok {
			return mcptransport.OntologyToolResult{}, fmt.Errorf("grounded ontology context is unavailable")
		}
		var input ontologyreasoning.GroundedContextRequest
		if len(req.InferenceJSON) > 256*1024 || decoder.Decode(&input) != nil || decoder.Decode(&struct{}{}) != io.EOF {
			return mcptransport.OntologyToolResult{}, ontologyreasoning.ErrInvalidRequest
		}
		input.TenantID = req.TenantID
		result, err := reader.GroundedContext(ctx, principal, input)
		if err != nil {
			return mcptransport.OntologyToolResult{}, err
		}
		value, err := ontologyToolJSONValue(result)
		return mcptransport.OntologyToolResult{Output: map[string]any{"context": value}}, err
	}
	if toolID == string(toolstd.OntologyInferenceRules) {
		workflow, ok := e.inference.(inferenceRulesWorkflow)
		if !ok {
			return mcptransport.OntologyToolResult{}, fmt.Errorf("continuous inference rules are unavailable")
		}
		var input ontologyreasoning.RuleSetRequest
		if err := decoder.Decode(&input); err != nil {
			return mcptransport.OntologyToolResult{}, err
		}
		if decoder.Decode(&struct{}{}) != io.EOF {
			return mcptransport.OntologyToolResult{}, ontologyreasoning.ErrInvalidRequest
		}
		input.TenantID = req.TenantID
		sets, err := workflow.InferenceRules(ctx, principal, input)
		if err != nil {
			return mcptransport.OntologyToolResult{}, err
		}
		value, err := ontologyToolJSONValue(sets)
		return mcptransport.OntologyToolResult{Output: map[string]any{"rule_sets": value}}, err
	}
	if toolID == string(toolstd.OntologyInfer) {
		var input ontologyreasoning.EvaluateRequest
		if err := decoder.Decode(&input); err != nil {
			return mcptransport.OntologyToolResult{}, fmt.Errorf("invalid ontology inference input: %w", err)
		}
		if decoder.Decode(&struct{}{}) != io.EOF {
			return mcptransport.OntologyToolResult{}, fmt.Errorf("ontology inference expects one JSON input")
		}
		input.TenantID = req.TenantID
		run, err := e.inference.Evaluate(ctx, principal, input)
		if err != nil {
			return mcptransport.OntologyToolResult{}, err
		}
		value, err := ontologyToolJSONValue(run)
		return mcptransport.OntologyToolResult{Output: map[string]any{"run": value}}, err
	}
	workflow, ok := e.inference.(inferenceCandidateWorkflow)
	if !ok {
		return mcptransport.OntologyToolResult{}, fmt.Errorf("ontology inference workflow is not configured")
	}
	var input struct {
		Operation string `json:"operation"`
		ontologyreasoning.WorkflowRequest
	}
	if err := decoder.Decode(&input); err != nil {
		return mcptransport.OntologyToolResult{}, fmt.Errorf("invalid ontology inference workflow input: %w", err)
	}
	if decoder.Decode(&struct{}{}) != io.EOF || (input.Operation != "review" && input.Operation != "plan" && input.Operation != "apply") {
		return mcptransport.OntologyToolResult{}, ontologyreasoning.ErrInvalidRequest
	}
	input.TenantID = req.TenantID
	state, err := workflow.CandidateWorkflow(ctx, principal, input.Operation, input.WorkflowRequest)
	if err != nil {
		return mcptransport.OntologyToolResult{}, err
	}
	value, err := ontologyToolJSONValue(state)
	return mcptransport.OntologyToolResult{Output: map[string]any{"candidate": value}}, err
}
