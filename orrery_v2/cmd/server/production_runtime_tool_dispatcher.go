package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	runtimecore "orrery_v2/internal/agent/runtime/core"
	deviceprovider "orrery_v2/internal/agent/runtime/providers/device"
	"orrery_v2/internal/agent/runtime/providers/toolbridge"
	"orrery_v2/internal/app/discussionsession"
	memorydiaryapp "orrery_v2/internal/app/memorydiary"
	"orrery_v2/internal/app/resourceadmin"
	"orrery_v2/internal/decision"
	"orrery_v2/internal/platform/logging"
	skillres "orrery_v2/internal/resource/skill"
	toolstd "orrery_v2/internal/resource/tool/std"
	mcptransport "orrery_v2/internal/transport/mcp"
)

type productionSkillToolExecutor interface {
	ExecuteSkillTool(ctx context.Context, toolID string, req skillres.RuntimeSkillRequest) (skillres.RuntimeToolResult, error)
}

type productionMCPGatewayCaller interface {
	CallTool(context.Context, mcptransport.CallToolRequest) (mcptransport.CallToolResult, error)
}

type productionMCPGatewayRef struct {
	gateway    productionMCPGatewayCaller
	layaShadow *decision.Observer
}

func (r *productionMCPGatewayRef) CallTool(ctx context.Context, req mcptransport.CallToolRequest) (mcptransport.CallToolResult, error) {
	if r == nil || r.gateway == nil {
		return mcptransport.CallToolResult{}, fmt.Errorf("mcp gateway is not configured")
	}
	result, err := r.gateway.CallTool(ctx, req)
	if err == nil {
		observeLayaMCPResult(ctx, r.layaShadow, req, result)
	}
	return result, err
}

type productionRuntimeToolDispatcher struct {
	desktopTools  *deviceprovider.WorkspaceToolDispatcher
	repo          *discussionsession.Repository
	tools         discussionsession.MissionToolingPort
	agents        discussionsession.AgentDirectoryPort
	skills        productionSkillToolExecutor
	personalShell personalAssistantShellExecutor
	mcpGateway    productionMCPGatewayCaller
	clock         interface{ Now() time.Time }
	ids           interface{ NewIDContext() (string, error) }
	diary         *memorydiaryapp.Coordinator
	images        *productionImageGenerator
	layaShadow    *decision.Observer
	rsiSamples    *resourceadmin.PlatformRSILearningSampleStore
	rsiLogger     logging.Logger
}

func (d productionRuntimeToolDispatcher) BeginRuntimeDiaryFlush(ctx context.Context, req runtimecore.RuntimeDiaryFlushAttemptRequest) (runtimecore.RuntimeDiaryFlushAttemptDecision, error) {
	if d.diary == nil {
		return runtimecore.RuntimeDiaryFlushAttemptDecision{Reason: "coordinator_unavailable"}, nil
	}
	decision, err := d.diary.BeginPreCompaction(ctx, memorydiaryapp.PreCompactionAttemptRequest{
		TenantID: req.TenantID, AgentID: req.AgentID.String(), SessionID: req.SessionID, ContextRevision: req.ContextRevision,
		Pressure: memorydiaryapp.FlushPressure{
			ContextWindowTokens: req.ContextWindowTokens, ReserveTokens: req.ReserveTokens,
			ProjectedTokens: req.ProjectedTokens, TranscriptBytes: req.TranscriptBytes,
		},
	})
	return runtimecore.RuntimeDiaryFlushAttemptDecision{Allowed: decision.Allowed, Attempt: decision.Attempt, Reason: decision.Reason}, err
}

func (d productionRuntimeToolDispatcher) CompleteRuntimeDiaryFlush(ctx context.Context, req runtimecore.RuntimeDiaryFlushOutcomeRequest) error {
	if d.diary == nil {
		return memorydiaryapp.ErrServiceDependency
	}
	return d.diary.CompletePreCompaction(ctx, memorydiaryapp.PreCompactionOutcomeRequest{
		TenantID: req.TenantID, AgentID: req.AgentID.String(), SessionID: req.SessionID, ContextRevision: req.ContextRevision,
		Status: req.Status, Failure: req.Failure,
		Date: req.Date,
	})
}

func (d productionRuntimeToolDispatcher) CallRuntimeTool(ctx context.Context, call runtimecore.RuntimeToolCallRequest) (result runtimecore.RuntimeToolCallResult, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			result = runtimeToolFailureResult(call, fmt.Errorf("runtime tool panic: %v", recovered), "tool_execution_panic")
			result.OutcomeUncertain = true
			err = nil
		}
		if err == nil {
			d.observeLayaFailure(ctx, call, result)
		}
	}()

	result, err = d.callRuntimeTool(ctx, call)
	if err == nil {
		return result, nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return runtimecore.RuntimeToolCallResult{}, err
	}
	return runtimeToolFailureResult(call, err, runtimeToolFailureCode(err)), nil
}

func (d productionRuntimeToolDispatcher) callRuntimeTool(ctx context.Context, call runtimecore.RuntimeToolCallRequest) (runtimecore.RuntimeToolCallResult, error) {
	binding, bound := runtimeToolBindingForCall(call.Name, call.ToolBindings)
	if err := validateRuntimeExecutionEnvelope(call, binding); err != nil {
		return runtimecore.RuntimeToolCallResult{}, err
	}
	if raw := strings.TrimSpace(call.ProviderSpecific[agentcore.RuntimeAllowedToolIDsProviderKey]); raw != "" {
		var allowed []string
		if json.Unmarshal([]byte(raw), &allowed) != nil || !bound || !slices.Contains(allowed, string(binding.ToolID)) {
			return runtimecore.RuntimeToolCallResult{}, mcptransport.ErrPermissionDenied
		}
	}
	if runtimeToolRequiresExplicitMCPScope(call) {
		return runtimecore.RuntimeToolCallResult{}, mcptransport.ErrPermissionDenied
	}
	if bound && binding.Source == "internal_plan" && toolbridge.IsPlanTool(toolbridge.RuntimeToolName(binding)) {
		output, err := toolbridge.HandlePlanTool(toolbridge.RuntimeToolName(binding), call.Input)
		return runtimecore.RuntimeToolCallResult{Output: output}, err
	}
	if bound && deviceprovider.WorkspaceToolOperation(string(binding.ToolID)) != "" {
		if d.desktopTools == nil {
			return runtimecore.RuntimeToolCallResult{}, fmt.Errorf("desktop tools unavailable")
		}
		call.Name = string(binding.ToolID)
		return d.desktopTools.CallRuntimeTool(ctx, call)
	}
	if isProductionSkillTool(call.Name) {
		return d.callSkillRuntimeTool(ctx, call)
	}
	if toolbridge.NormalizeToolName(call.Name) == string(toolstd.ImageGenerate) || (bound && binding.ToolID == toolstd.ImageGenerate) {
		return d.images.call(ctx, call, binding, bound), nil
	}
	if isPersonalAssistantShellRuntimeTool(call.Name) || (bound && isPersonalAssistantShellRuntimeBinding(binding)) {
		return d.callPersonalAssistantShellTool(ctx, call, binding)
	}
	if bound && isEvaluationReviewRuntimeBinding(binding) {
		return d.callEvaluationReviewTool(ctx, call, binding)
	}
	if bound && isSessionHistoryRuntimeBinding(binding) {
		return d.callSessionHistoryTool(ctx, call, binding)
	}
	if bound && isMemorySearchRuntimeBinding(binding) {
		return d.callMemorySearchTool(ctx, call, binding)
	}
	if bound && binding.ToolID == toolstd.RSILearningPlan {
		return d.callMemorySearchTool(ctx, call, binding)
	}
	if bound && isMemoryDiaryRuntimeBinding(binding) {
		return d.callMemoryDiaryTool(ctx, call, binding)
	}
	if bound && isEnterpriseGraphRuntimeBinding(binding) {
		return d.callEnterpriseGraphTool(ctx, call, binding)
	}
	if bound && isSchedulerRuntimeBinding(binding) {
		return d.callSchedulerTool(ctx, call, binding)
	}
	if bound && isKnowledgeRuntimeBinding(binding) {
		return d.callKnowledgeTool(ctx, call, binding)
	}
	if bound && isWebRuntimeBinding(binding) {
		return d.callWebTool(ctx, call, binding)
	}
	if bound && toolstd.IsPersonalBrowserTool(string(binding.ToolID)) {
		return d.callWebTool(ctx, call, binding)
	}
	if bound && isEmailRuntimeBinding(binding) {
		return d.callEmailTool(ctx, call, binding)
	}
	if bound && isExternalMCPRuntimeBinding(binding) {
		return d.callExternalMCPTool(ctx, call, binding)
	}
	conversationID := strings.TrimSpace(call.ProviderSpecific["discussion_id"])
	if conversationID == "" {
		return runtimecore.RuntimeToolCallResult{}, fmt.Errorf("discussion runtime tool %s requires discussion_id", call.Name)
	}
	service := discussionsession.NewService(discussionsession.ServiceOptions{
		Repository:     d.repo,
		MissionTooling: d.tools,
		AgentDirectory: d.agents,
		Clock:          d.clock,
		IDGenerator:    d.ids,
	})
	at := time.Now().UTC()
	if d.clock != nil {
		at = d.clock.Now().UTC()
	}
	applied, linkedMissions, err := service.ApplyRuntimeToolCallAs(ctx,
		conversationID,
		strings.TrimSpace(call.RunID.String()),
		strings.TrimSpace(call.AgentID.String()),
		runtimeDiscussionPrincipal(call),
		strings.TrimSpace(call.ProviderSpecific["discussion_source_message_id"]),
		strings.TrimSpace(call.ProviderSpecific["discussion_mode"]),
		discussionsession.RuntimeToolCallDTO(call.Name, call.CallID, call.Input),
		runtimeDiscussionBoundTools(call.ToolBindings),
		at,
	)
	if err != nil {
		return runtimecore.RuntimeToolCallResult{}, err
	}
	output := map[string]any{
		"status":       firstNonBlank(applied.Status, "completed"),
		"tool":         strings.TrimSpace(applied.Name),
		"tool_call_id": strings.TrimSpace(applied.ToolCallID),
	}
	for key, value := range applied.Result {
		if strings.TrimSpace(key) != "" && strings.TrimSpace(value) != "" {
			output[key] = value
		}
	}
	if len(linkedMissions) > 0 {
		output["linked_mission_count"] = fmt.Sprint(len(linkedMissions))
		if raw, err := json.Marshal(linkedMissions); err == nil {
			output["linked_missions_json"] = string(raw)
		}
	}
	return runtimecore.RuntimeToolCallResult{Output: output}, nil
}

// Autonomous-plan tasks use an explicit-scope contract. Native runtime tools
// do not yet have a scope mapping equivalent to the MCP gateway, so they are
// denied until such a mapping exists. Only established MCP-bound branches may
// execute for those tasks, where the gateway enforces the task scope.
func runtimeToolRequiresExplicitMCPScope(call runtimecore.RuntimeToolCallRequest) bool {
	responsibility := call.ResponsibilityContext
	if call.ExecutionEnvelope != nil && call.ExecutionEnvelope.ResponsibilityContext != nil {
		responsibility = call.ExecutionEnvelope.ResponsibilityContext
	}
	if responsibility == nil || !responsibility.RequireExplicitToolScopes {
		return false
	}
	for _, scope := range responsibility.TaskPermissionScopes {
		if strings.TrimSpace(scope) != "" {
			binding, bound := runtimeToolBindingForCall(call.Name, call.ToolBindings)
			desktopAllowed := bound && deviceprovider.WorkspaceToolOperation(string(binding.ToolID)) != "" && call.ExecutionEnvelope != nil && call.ExecutionEnvelope.AllowsTool(string(binding.ToolID), []string{desktopToolPermission(string(binding.ToolID))}) && runtimePermissionScopesContain(responsibility.TaskPermissionScopes, desktopToolPermission(string(binding.ToolID)))
			return !bound || !(desktopAllowed || isEvaluationReviewRuntimeBinding(binding) || isMemorySearchRuntimeBinding(binding) || isSessionHistoryRuntimeBinding(binding) || isMemoryDiaryRuntimeBinding(binding) || binding.ToolID == toolstd.RSILearningPlan ||
				isEnterpriseGraphRuntimeBinding(binding) || isSchedulerRuntimeBinding(binding) || isKnowledgeRuntimeBinding(binding) || isWebRuntimeBinding(binding) ||
				isEmailRuntimeBinding(binding) || isExternalMCPRuntimeBinding(binding))
		}
	}
	return true
}

func runtimeToolFailureResult(call runtimecore.RuntimeToolCallRequest, cause error, code string) runtimecore.RuntimeToolCallResult {
	message := "runtime tool execution failed"
	if cause != nil && strings.TrimSpace(cause.Error()) != "" {
		message = strings.TrimSpace(cause.Error())
	}
	retryable := true
	recoveryHint := "Read the error, correct the tool arguments, and call the tool again. A single failed call must not end the interaction."
	if errors.Is(cause, mcptransport.ErrToolCallOutcomeUnrecorded) {
		message = "MCP tool may have completed, but its outcome could not be recorded"
		retryable = false
		recoveryHint = "Do not call the tool again automatically. Verify downstream state and reconcile the ActionRun before retrying."
	} else if errors.Is(cause, mcptransport.ErrPermissionDenied) {
		retryable = false
		recoveryHint = "This call was denied by authorization. Do not repeat the same call or expand its permissions. Explain the access failure and request an authorized correction."
	}
	return runtimecore.RuntimeToolCallResult{OutcomeUncertain: errors.Is(cause, mcptransport.ErrToolCallOutcomeUnrecorded), Output: map[string]any{
		"status":        "failed",
		"tool":          strings.TrimSpace(call.Name),
		"tool_call_id":  strings.TrimSpace(call.CallID),
		"arguments":     cloneAnyMap(call.Input),
		"error":         message,
		"error_code":    firstNonBlank(code, "tool_execution_failed"),
		"retryable":     retryable,
		"recovery_hint": recoveryHint,
	}}
}

func runtimeToolFailureCode(err error) string {
	if errors.Is(err, mcptransport.ErrToolCallOutcomeUnrecorded) {
		return "tool_outcome_unrecorded"
	}
	if errors.Is(err, mcptransport.ErrPermissionDenied) {
		return "permission_denied"
	}
	if errors.Is(err, discussionsession.ErrInvalidRequest) {
		return "invalid_arguments"
	}
	message := strings.ToLower(strings.TrimSpace(err.Error()))
	for _, marker := range []string{"required", "invalid", "must be", "must reference", "decode", "unmarshal"} {
		if strings.Contains(message, marker) {
			return "invalid_arguments"
		}
	}
	return "tool_execution_failed"
}

func (d productionRuntimeToolDispatcher) callEvaluationReviewTool(ctx context.Context, call runtimecore.RuntimeToolCallRequest, binding agentcore.RuntimeToolBinding) (runtimecore.RuntimeToolCallResult, error) {
	if d.mcpGateway == nil {
		return runtimecore.RuntimeToolCallResult{}, fmt.Errorf("evaluation review runtime tool requires mcp gateway")
	}
	result, err := d.mcpGateway.CallTool(ctx, mcptransport.CallToolRequest{
		TenantID:                 strings.TrimSpace(call.TenantID),
		RunID:                    strings.TrimSpace(call.RunID.String()),
		ToolID:                   string(toolstd.EvaluationReview),
		PrincipalID:              runtimeExecutionPrincipalID(call),
		PrincipalKind:            runtimeExecutionPrincipalKind(call),
		AgentID:                  strings.TrimSpace(call.AgentID.String()),
		Permissions:              runtimeGatewayPermissions(call, binding),
		PermissionsAuthoritative: runtimeGatewayPermissionsAuthoritative(call),
		ResponsibilityContext:    agentcore.CloneResponsibilityContext(call.ResponsibilityContext), ExecutionEnvelope: agentcore.CloneExecutionEnvelope(call.ExecutionEnvelope),
		Arguments: cloneAnyMap(call.Input),
	})
	if err != nil {
		return runtimecore.RuntimeToolCallResult{}, err
	}
	return runtimecore.RuntimeToolCallResult{Output: result.Output}, nil
}

func (d productionRuntimeToolDispatcher) callMemoryDiaryTool(ctx context.Context, call runtimecore.RuntimeToolCallRequest, binding agentcore.RuntimeToolBinding) (runtimecore.RuntimeToolCallResult, error) {
	if d.mcpGateway == nil {
		return runtimecore.RuntimeToolCallResult{}, fmt.Errorf("memory diary runtime tool requires mcp gateway")
	}
	toolID := toolbridge.NormalizeToolName(string(binding.ToolID))
	arguments := cloneAnyMap(call.Input)
	maintenance := isDiaryMaintenanceRuntimeBinding(binding) && diaryMaintenanceCall(call.ProviderSpecific)
	if maintenance {
		arguments = lockedDiaryMaintenanceArguments(arguments, call.ProviderSpecific)
	}
	responsibilityContext := agentcore.CloneResponsibilityContext(call.ResponsibilityContext)
	if maintenance && (responsibilityContext == nil || !responsibilityContext.RequireExplicitToolScopes) {
		responsibilityContext = nil
	}
	result, err := d.mcpGateway.CallTool(ctx, mcptransport.CallToolRequest{
		TenantID:                 strings.TrimSpace(call.TenantID),
		RunID:                    strings.TrimSpace(call.RunID.String()),
		ToolID:                   toolID,
		PrincipalID:              runtimeExecutionPrincipalID(call),
		PrincipalKind:            runtimeExecutionPrincipalKind(call),
		AgentID:                  strings.TrimSpace(call.AgentID.String()),
		Permissions:              runtimeGatewayPermissions(call, binding),
		PermissionsAuthoritative: runtimeGatewayPermissionsAuthoritative(call),
		ResponsibilityContext:    responsibilityContext,
		ExecutionEnvelope:        agentcore.CloneExecutionEnvelope(call.ExecutionEnvelope),
		Arguments:                arguments,
		Maintenance:              maintenance,
	})
	if err != nil {
		return runtimecore.RuntimeToolCallResult{}, err
	}
	return runtimecore.RuntimeToolCallResult{Output: result.Output}, nil
}

func (d productionRuntimeToolDispatcher) callMemorySearchTool(ctx context.Context, call runtimecore.RuntimeToolCallRequest, binding agentcore.RuntimeToolBinding) (runtimecore.RuntimeToolCallResult, error) {
	if d.mcpGateway == nil {
		return runtimecore.RuntimeToolCallResult{}, fmt.Errorf("memory search runtime tool requires mcp gateway")
	}
	result, err := d.mcpGateway.CallTool(ctx, mcptransport.CallToolRequest{
		TenantID:                 strings.TrimSpace(call.TenantID),
		RunID:                    strings.TrimSpace(call.RunID.String()),
		ToolID:                   string(binding.ToolID),
		PrincipalID:              runtimeExecutionPrincipalID(call),
		PrincipalKind:            runtimeExecutionPrincipalKind(call),
		AgentID:                  strings.TrimSpace(call.AgentID.String()),
		LogicalAgentID:           strings.TrimSpace(call.ProviderSpecific[personalAssistantLogicalAgentIDKey]),
		Permissions:              runtimeGatewayPermissions(call, binding),
		PermissionsAuthoritative: runtimeGatewayPermissionsAuthoritative(call),
		ResponsibilityContext:    agentcore.CloneResponsibilityContext(call.ResponsibilityContext), ExecutionEnvelope: agentcore.CloneExecutionEnvelope(call.ExecutionEnvelope),
		Arguments: cloneAnyMap(call.Input),
	})
	if err != nil {
		return runtimecore.RuntimeToolCallResult{}, err
	}
	return runtimecore.RuntimeToolCallResult{Output: result.Output}, nil
}

func (d productionRuntimeToolDispatcher) callEnterpriseGraphTool(ctx context.Context, call runtimecore.RuntimeToolCallRequest, binding agentcore.RuntimeToolBinding) (runtimecore.RuntimeToolCallResult, error) {
	if d.mcpGateway == nil {
		return runtimecore.RuntimeToolCallResult{}, fmt.Errorf("enterprise graph runtime tool requires mcp gateway")
	}
	arguments := cloneAnyMap(call.Input)
	if tenantID := strings.TrimSpace(call.TenantID); tenantID != "" {
		arguments["tenant_id"] = tenantID
	}
	arguments["actor_ref"] = runtimeDataActorRef(call)
	if toolbridge.NormalizeToolName(string(binding.ToolID)) == toolbridge.NormalizeToolName(string(toolstd.EnterpriseGraphBuildContextBundle)) {
		arguments["consumer"] = "agent-runtime"
		arguments["purpose"] = "runtime-context"
	}
	result, err := d.mcpGateway.CallTool(ctx, mcptransport.CallToolRequest{
		TenantID:                 strings.TrimSpace(call.TenantID),
		RunID:                    strings.TrimSpace(call.RunID.String()),
		ToolID:                   strings.TrimSpace(string(binding.ToolID)),
		PrincipalID:              runtimeExecutionPrincipalID(call),
		PrincipalKind:            runtimeExecutionPrincipalKind(call),
		AgentID:                  strings.TrimSpace(call.AgentID.String()),
		Permissions:              runtimeGatewayPermissions(call, binding),
		PermissionsAuthoritative: runtimeGatewayPermissionsAuthoritative(call),
		ResponsibilityContext:    agentcore.CloneResponsibilityContext(call.ResponsibilityContext), ExecutionEnvelope: agentcore.CloneExecutionEnvelope(call.ExecutionEnvelope),
		Arguments:   arguments,
		RequestMeta: cloneAnyMap(call.RequestMeta),
	})
	if err != nil {
		return runtimecore.RuntimeToolCallResult{}, err
	}
	return runtimecore.RuntimeToolCallResult{Output: result.Output}, nil
}

func (d productionRuntimeToolDispatcher) callKnowledgeTool(ctx context.Context, call runtimecore.RuntimeToolCallRequest, binding agentcore.RuntimeToolBinding) (runtimecore.RuntimeToolCallResult, error) {
	if d.mcpGateway == nil {
		return runtimecore.RuntimeToolCallResult{}, fmt.Errorf("knowledge runtime tool requires mcp gateway")
	}
	result, err := d.mcpGateway.CallTool(ctx, mcptransport.CallToolRequest{
		TenantID:                 strings.TrimSpace(call.TenantID),
		RunID:                    strings.TrimSpace(call.RunID.String()),
		ToolID:                   string(binding.ToolID),
		PrincipalID:              runtimeExecutionPrincipalID(call),
		PrincipalKind:            runtimeExecutionPrincipalKind(call),
		AgentID:                  strings.TrimSpace(call.AgentID.String()),
		Permissions:              runtimeGatewayPermissions(call, binding),
		PermissionsAuthoritative: runtimeGatewayPermissionsAuthoritative(call),
		ResponsibilityContext:    agentcore.CloneResponsibilityContext(call.ResponsibilityContext), ExecutionEnvelope: agentcore.CloneExecutionEnvelope(call.ExecutionEnvelope),
		Arguments: cloneAnyMap(call.Input),
	})
	if err != nil {
		return runtimecore.RuntimeToolCallResult{}, err
	}
	output := result.Output
	switch binding.ToolID {
	case defaultWebsiteDistillationCandidatesListToolID, defaultWebsiteFAQCatalogListToolID, defaultWebsiteFAQMetricsListToolID:
		if strings.TrimSpace(call.CallID) != "" && output != nil && !runtimeToolOutputFailed(output) {
			output = cloneAnyMap(output)
			output["evidence_ref"] = strings.TrimSpace(call.CallID)
		}
	}
	return runtimecore.RuntimeToolCallResult{Output: output}, nil
}

func (d productionRuntimeToolDispatcher) callWebTool(ctx context.Context, call runtimecore.RuntimeToolCallRequest, binding agentcore.RuntimeToolBinding) (runtimecore.RuntimeToolCallResult, error) {
	if d.mcpGateway == nil {
		return runtimecore.RuntimeToolCallResult{}, fmt.Errorf("web runtime tool requires mcp gateway")
	}
	result, err := d.mcpGateway.CallTool(ctx, mcptransport.CallToolRequest{
		TenantID:                 strings.TrimSpace(call.TenantID),
		RunID:                    strings.TrimSpace(call.RunID.String()),
		ToolID:                   string(binding.ToolID),
		PrincipalID:              runtimeExecutionPrincipalID(call),
		PrincipalKind:            runtimeExecutionPrincipalKind(call),
		AgentID:                  strings.TrimSpace(call.AgentID.String()),
		Permissions:              runtimeGatewayPermissions(call, binding),
		PermissionsAuthoritative: runtimeGatewayPermissionsAuthoritative(call),
		ResponsibilityContext:    agentcore.CloneResponsibilityContext(call.ResponsibilityContext), ExecutionEnvelope: agentcore.CloneExecutionEnvelope(call.ExecutionEnvelope),
		Arguments: cloneAnyMap(call.Input),
	})
	if err != nil {
		return runtimecore.RuntimeToolCallResult{}, err
	}
	return runtimecore.RuntimeToolCallResult{Output: result.Output}, nil
}

func (d productionRuntimeToolDispatcher) callSchedulerTool(ctx context.Context, call runtimecore.RuntimeToolCallRequest, binding agentcore.RuntimeToolBinding) (runtimecore.RuntimeToolCallResult, error) {
	if d.mcpGateway == nil {
		return runtimecore.RuntimeToolCallResult{}, fmt.Errorf("scheduler runtime tool requires mcp gateway")
	}
	result, err := d.mcpGateway.CallTool(ctx, mcptransport.CallToolRequest{
		TenantID:                 strings.TrimSpace(call.TenantID),
		RunID:                    strings.TrimSpace(call.RunID.String()),
		ToolID:                   string(binding.ToolID),
		PrincipalID:              runtimeExecutionPrincipalID(call),
		PrincipalKind:            runtimeExecutionPrincipalKind(call),
		AgentID:                  strings.TrimSpace(call.AgentID.String()),
		Permissions:              runtimeGatewayPermissions(call, binding),
		PermissionsAuthoritative: runtimeGatewayPermissionsAuthoritative(call),
		ResponsibilityContext:    agentcore.CloneResponsibilityContext(call.ResponsibilityContext), ExecutionEnvelope: agentcore.CloneExecutionEnvelope(call.ExecutionEnvelope),
		Arguments: cloneAnyMap(call.Input),
	})
	if err != nil {
		return runtimecore.RuntimeToolCallResult{}, err
	}
	return runtimecore.RuntimeToolCallResult{Output: result.Output}, nil
}

func (d productionRuntimeToolDispatcher) callEmailTool(ctx context.Context, call runtimecore.RuntimeToolCallRequest, binding agentcore.RuntimeToolBinding) (runtimecore.RuntimeToolCallResult, error) {
	if d.mcpGateway == nil {
		return runtimecore.RuntimeToolCallResult{}, fmt.Errorf("email runtime tool requires mcp gateway")
	}
	result, err := d.mcpGateway.CallTool(ctx, mcptransport.CallToolRequest{
		TenantID:                 strings.TrimSpace(call.TenantID),
		RunID:                    strings.TrimSpace(call.RunID.String()),
		ToolID:                   string(toolstd.EmailSend),
		PrincipalID:              runtimeExecutionPrincipalID(call),
		PrincipalKind:            runtimeExecutionPrincipalKind(call),
		AgentID:                  strings.TrimSpace(call.AgentID.String()),
		Permissions:              runtimeGatewayPermissions(call, binding),
		PermissionsAuthoritative: runtimeGatewayPermissionsAuthoritative(call),
		ResponsibilityContext:    agentcore.CloneResponsibilityContext(call.ResponsibilityContext), ExecutionEnvelope: agentcore.CloneExecutionEnvelope(call.ExecutionEnvelope),
		Arguments: cloneAnyMap(call.Input),
	})
	if err != nil {
		return runtimecore.RuntimeToolCallResult{}, err
	}
	return runtimecore.RuntimeToolCallResult{Output: result.Output}, nil
}

func (d productionRuntimeToolDispatcher) callExternalMCPTool(ctx context.Context, call runtimecore.RuntimeToolCallRequest, binding agentcore.RuntimeToolBinding) (runtimecore.RuntimeToolCallResult, error) {
	if d.mcpGateway == nil {
		return runtimecore.RuntimeToolCallResult{}, fmt.Errorf("external mcp runtime tool %s requires mcp gateway", call.Name)
	}
	arguments := cloneAnyMap(call.Input)
	if strings.TrimSpace(binding.ServerToolID) == string(defaultOrreryPublicMCPServerToolID) &&
		strings.TrimSpace(binding.DownstreamToolName) == "search_documents" && firstToolInputInt(arguments, "limit") > 6 {
		arguments["limit"] = 6
	}
	if strings.TrimSpace(binding.ServerToolID) == string(defaultServerBrowserMCPServerToolID) {
		var err error
		arguments, err = lockedServerBrowserArguments(binding.DownstreamToolName, arguments)
		if err != nil {
			return runtimecore.RuntimeToolCallResult{}, err
		}
	}
	result, err := d.mcpGateway.CallTool(ctx, mcptransport.CallToolRequest{
		TenantID:                 strings.TrimSpace(call.TenantID),
		RunID:                    strings.TrimSpace(call.RunID.String()),
		ToolID:                   strings.TrimSpace(string(binding.ToolID)),
		PrincipalID:              runtimeExecutionPrincipalID(call),
		PrincipalKind:            runtimeExecutionPrincipalKind(call),
		AgentID:                  strings.TrimSpace(call.AgentID.String()),
		Permissions:              runtimeGatewayPermissions(call, binding),
		PermissionsAuthoritative: runtimeGatewayPermissionsAuthoritative(call),
		ResponsibilityContext:    agentcore.CloneResponsibilityContext(call.ResponsibilityContext), ExecutionEnvelope: agentcore.CloneExecutionEnvelope(call.ExecutionEnvelope),
		Arguments:   arguments,
		RequestMeta: cloneAnyMap(call.RequestMeta),
	})
	if err != nil {
		return runtimecore.RuntimeToolCallResult{}, err
	}
	return runtimecore.RuntimeToolCallResult{Output: result.Output}, nil
}

func (d productionRuntimeToolDispatcher) callPersonalAssistantShellTool(ctx context.Context, call runtimecore.RuntimeToolCallRequest, binding agentcore.RuntimeToolBinding) (runtimecore.RuntimeToolCallResult, error) {
	if d.personalShell == nil {
		return runtimecore.RuntimeToolCallResult{}, fmt.Errorf("personal assistant shell tool requires shell executor")
	}
	if runtimeExecutionPrincipal(call).Kind == agentcore.ExecutionPrincipalUser &&
		!runtimePermissionScopesContain(runtimeGatewayPermissions(call, binding), toolstd.PermissionPersonalAssistantShell) {
		return runtimecore.RuntimeToolCallResult{}, mcptransport.ErrPermissionDenied
	}
	timeoutMs := firstToolInputInt(call.Input, "timeout_ms")
	maximumTimeoutMs := 0
	if timeoutSeconds, err := strconv.ParseInt(strings.TrimSpace(call.ProviderSpecific[runtimecore.ToolCallTimeoutSecondsProviderKey]), 10, 64); err == nil && timeoutSeconds > 0 {
		maxInt := int64(^uint(0) >> 1)
		if timeoutSeconds <= maxInt/1000 {
			milliseconds := timeoutSeconds * 1000
			timeoutMs = int(milliseconds)
			maximumTimeoutMs = int(milliseconds)
		}
	}
	request := mcptransport.PersonalAssistantShellRequest{
		PrincipalID:      runtimeExecutionPrincipalID(call),
		AgentID:          strings.TrimSpace(call.AgentID.String()),
		Command:          firstToolInputString(call.Input, "command", "cmd"),
		Cwd:              firstToolInputString(call.Input, "cwd", "working_directory"),
		WorkspaceRoot:    firstNonBlank(call.WorkspaceRoot, call.WorkspacePolicy.Root),
		WorkingDirectory: firstNonBlank(call.WorkingDirectory, call.WorkspacePolicy.WorkDir),
		TimeoutMs:        timeoutMs,
		MaximumTimeoutMs: maximumTimeoutMs,
		MaxOutputLen:     firstToolInputInt(call.Input, "max_output_len"),
	}
	result, err := d.personalShell.ExecutePersonalAssistantShell(ctx, call.Name, request)
	if err != nil {
		return runtimecore.RuntimeToolCallResult{}, err
	}
	return runtimecore.RuntimeToolCallResult{Output: result.Output}, nil
}

func runtimeDiscussionPrincipal(call runtimecore.RuntimeToolCallRequest) discussionsession.ViewerRef {
	principal := runtimeExecutionPrincipal(call)
	kind := discussionsession.ViewerKindAgent
	switch principal.Kind {
	case agentcore.ExecutionPrincipalUser:
		kind = discussionsession.ViewerKindUser
	case agentcore.ExecutionPrincipalSystem:
		kind = discussionsession.ViewerKindSystem
	}
	return discussionsession.ViewerRef{
		ViewerID: principal.ID, ViewerKind: kind, TenantID: principal.TenantID,
		PermissionScope: append([]string(nil), principal.PermissionScope...),
	}
}

func (d productionRuntimeToolDispatcher) callSkillRuntimeTool(ctx context.Context, call runtimecore.RuntimeToolCallRequest) (runtimecore.RuntimeToolCallResult, error) {
	if d.skills == nil {
		return runtimecore.RuntimeToolCallResult{}, fmt.Errorf("skill runtime tool %s requires skill executor", call.Name)
	}
	result, err := d.skills.ExecuteSkillTool(ctx, call.Name, skillres.RuntimeSkillRequest{
		SkillBindings: append([]agentcore.RuntimeSkillBinding(nil), call.SkillBindings...),
		SkillID:       firstToolInputString(call.Input, "skill_id", "id", "name"),
		Path:          firstToolInputString(call.Input, "path", "resource_path"),
		Query:         firstToolInputString(call.Input, "query"),
	})
	if err != nil {
		return runtimecore.RuntimeToolCallResult{}, err
	}
	return runtimecore.RuntimeToolCallResult{Output: result.Output}, nil
}

func isProductionSkillTool(name string) bool {
	switch strings.TrimSpace(name) {
	case string(toolstd.SkillList),
		string(toolstd.SkillLoad),
		string(toolstd.SkillResourceList),
		string(toolstd.SkillResourceLoad):
		return true
	default:
		return false
	}
}

func isPersonalAssistantShellRuntimeTool(name string) bool {
	switch toolbridge.NormalizeToolName(name) {
	case string(toolstd.PersonalAssistantShellExec), "run_terminal":
		return true
	default:
		return false
	}
}

func isPersonalAssistantShellRuntimeBinding(binding agentcore.RuntimeToolBinding) bool {
	return toolbridge.NormalizeToolName(string(binding.ToolID)) == string(toolstd.PersonalAssistantShellExec)
}

func runtimeToolBindingForCall(name string, bindings []agentcore.RuntimeToolBinding) (agentcore.RuntimeToolBinding, bool) {
	return toolbridge.RuntimeToolBindingForCall(name, bindings)
}

func isEvaluationReviewRuntimeBinding(binding agentcore.RuntimeToolBinding) bool {
	return toolbridge.NormalizeToolName(string(binding.ToolID)) == string(toolstd.EvaluationReview)
}

func isMemoryDiaryRuntimeBinding(binding agentcore.RuntimeToolBinding) bool {
	switch toolbridge.NormalizeToolName(string(binding.ToolID)) {
	case string(toolstd.MemoryDiaryWrite), string(toolstd.MemoryDiaryRead), string(toolstd.MemoryDiaryAppend):
		return true
	default:
		return false
	}
}

func isMemorySearchRuntimeBinding(binding agentcore.RuntimeToolBinding) bool {
	return toolbridge.NormalizeToolName(string(binding.ToolID)) == string(toolstd.MemorySearch)
}

func isSessionHistoryRuntimeBinding(binding agentcore.RuntimeToolBinding) bool {
	return binding.ToolID == toolstd.SessionSearch || binding.ToolID == toolstd.SessionRead
}

func isDiaryMaintenanceRuntimeBinding(binding agentcore.RuntimeToolBinding) bool {
	switch toolbridge.NormalizeToolName(string(binding.ToolID)) {
	case string(toolstd.MemoryDiaryRead), string(toolstd.MemoryDiaryAppend):
		return true
	default:
		return false
	}
}

func diaryMaintenanceCall(values map[string]string) bool {
	value := strings.TrimSpace(values["diary_maintenance"])
	return value == "1" || strings.EqualFold(value, "true")
}

func lockedDiaryMaintenanceArguments(model map[string]any, host map[string]string) map[string]any {
	out := map[string]any{}
	if content := firstToolInputString(model, "content", "text"); content != "" {
		out["content"] = content
	}
	for argument, metadata := range map[string]string{
		"path":               "diary_target_path",
		"date":               "diary_target_date",
		"session_id":         "diary_session_id",
		"context_revision":   "diary_context_revision",
		"trigger":            "diary_trigger",
		"idempotency_key":    "diary_idempotency_key",
		"mission_id":         "diary_mission_id",
		"task_id":            "diary_task_id",
		"run_id":             "diary_run_id",
		"sensitivity":        "diary_sensitivity",
		"source_sensitivity": "diary_source_sensitivity",
	} {
		if value := strings.TrimSpace(host[metadata]); value != "" {
			out[argument] = value
		}
	}
	for argument, metadata := range map[string]string{
		"evidence_refs": "diary_evidence_refs",
		"lineage_refs":  "diary_lineage_refs",
	} {
		var values []any
		if raw := strings.TrimSpace(host[metadata]); raw != "" && json.Unmarshal([]byte(raw), &values) == nil {
			out[argument] = values
		}
	}
	return out
}

func isEnterpriseGraphRuntimeBinding(binding agentcore.RuntimeToolBinding) bool {
	toolID := toolbridge.NormalizeToolName(string(binding.ToolID))
	return toolID == toolbridge.NormalizeToolName(string(toolstd.EnterpriseGraphBuildContextBundle)) ||
		toolID == toolbridge.NormalizeToolName(string(toolstd.EnterpriseGraphSearchObjects))
}

func isKnowledgeRuntimeBinding(binding agentcore.RuntimeToolBinding) bool {
	switch toolbridge.NormalizeToolName(string(binding.ToolID)) {
	case string(toolstd.KnowledgeSearch), string(toolstd.KnowledgeAdd),
		string(defaultWebsiteDistillationCandidatesListToolID), string(defaultWebsiteFAQCatalogListToolID), string(defaultWebsiteFAQMetricsListToolID):
		return true
	default:
		return false
	}
}

func isWebRuntimeBinding(binding agentcore.RuntimeToolBinding) bool {
	switch toolbridge.NormalizeToolName(string(binding.ToolID)) {
	case string(toolstd.WebSearch), string(toolstd.WebFetch):
		return true
	default:
		return false
	}
}

func isSchedulerRuntimeBinding(binding agentcore.RuntimeToolBinding) bool {
	switch toolbridge.NormalizeToolName(string(binding.ToolID)) {
	case string(toolstd.ScheduledTaskCreate), string(toolstd.ScheduledTaskUpdate), string(toolstd.ScheduledTaskCancel),
		string(toolstd.ScheduledTaskList), string(toolstd.ScheduledTaskRunNow):
		return true
	default:
		return false
	}
}

func isEmailRuntimeBinding(binding agentcore.RuntimeToolBinding) bool {
	return toolbridge.NormalizeToolName(string(binding.ToolID)) == string(toolstd.EmailSend)
}

func isExternalMCPRuntimeBinding(binding agentcore.RuntimeToolBinding) bool {
	return strings.TrimSpace(binding.Source) == "mcp_server" ||
		strings.TrimSpace(binding.ServerToolID) != "" ||
		strings.TrimSpace(binding.DownstreamToolName) != ""
}
