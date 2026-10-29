package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/discussionsession"
	memorydiaryapp "orrery_v2/internal/app/memorydiary"
	"orrery_v2/internal/app/operations"
	schedulerapp "orrery_v2/internal/app/scheduler"
	"orrery_v2/internal/platform"
	toolres "orrery_v2/internal/resource/tool"
	"orrery_v2/internal/retrievalevidence"
	mcptransport "orrery_v2/internal/transport/mcp"
	mcpruntime "orrery_v2/internal/transport/mcpruntime"
)

func newProductionExternalMCPRuntime(rt *platform.Runtime) *mcpruntime.Runtime {
	return mcpruntime.New(mcpruntime.Options{
		Timeout:        15 * time.Second,
		Now:            rt.Clock.Now,
		SecretResolver: platformSecretResolver{provider: rt.Secrets},
	})
}

type productionMCPGatewayOptions struct {
	PersonalBrowserExecutor        mcptransport.PersonalBrowserExecutor
	Registry                       toolres.Registry
	SchedulerExecutor              mcptransport.SchedulerExecutor
	TodoExecutor                   mcptransport.TodoExecutor
	BusinessApprovalExecutor       mcptransport.BusinessApprovalExecutor
	EvaluationExecutor             mcptransport.EvaluationToolExecutor
	MemorySearchExecutor           mcptransport.MemorySearchToolExecutor
	SessionHistoryExecutor         mcptransport.SessionHistoryToolExecutor
	RSILearningExecutor            mcptransport.RSILearningToolExecutor
	MemoryDiaryExecutor            mcptransport.MemoryDiaryToolExecutor
	KnowledgeExecutor              mcptransport.KnowledgeToolExecutor
	WebExecutor                    mcptransport.WebToolExecutor
	EmailExecutor                  mcptransport.EmailToolExecutor
	PersonalAssistantShellExecutor mcptransport.PersonalAssistantShellExecutor
	EnterpriseGraphExecutor        mcptransport.EnterpriseGraphExecutor
	OntologyExecutor               mcptransport.OntologyToolExecutor
	SkillExecutor                  mcptransport.SkillToolExecutor
	WebsiteKnowledgeExecutor       mcptransport.WebsiteKnowledgeToolExecutor
	AuditStore                     *operations.AuditStore
	ExternalMCP                    mcptransport.ExternalMCPClient
	Profiles                       mcptransport.AgentProfileToolSource
	Lifecycle                      mcptransport.ToolCallLifecycle
	ResponsibilityAuthority        mcptransport.ResponsibilityAuthorityResolver
	Now                            func() time.Time
}

func newProductionMCPGateway(opts productionMCPGatewayOptions) *mcptransport.Gateway {
	external := opts.ExternalMCP
	if external == nil {
		external = mcptransport.NotConfiguredExternalMCPClient{}
	}
	if opts.WebExecutor == nil {
		opts.WebExecutor = newProductionWebToolExecutor()
	}
	router := mcptransport.NewToolRouter(mcptransport.ToolRouterOptions{
		PersonalBrowserExecutor:        opts.PersonalBrowserExecutor,
		Registry:                       opts.Registry,
		SchedulerExecutor:              opts.SchedulerExecutor,
		TodoExecutor:                   opts.TodoExecutor,
		BusinessApprovalExecutor:       opts.BusinessApprovalExecutor,
		EvaluationExecutor:             opts.EvaluationExecutor,
		MemorySearchExecutor:           opts.MemorySearchExecutor,
		SessionHistoryExecutor:         opts.SessionHistoryExecutor,
		RSILearningExecutor:            opts.RSILearningExecutor,
		MemoryDiaryExecutor:            opts.MemoryDiaryExecutor,
		KnowledgeExecutor:              opts.KnowledgeExecutor,
		WebExecutor:                    opts.WebExecutor,
		EmailExecutor:                  opts.EmailExecutor,
		PersonalAssistantShellExecutor: opts.PersonalAssistantShellExecutor,
		EnterpriseGraphExecutor:        opts.EnterpriseGraphExecutor,
		OntologyExecutor:               opts.OntologyExecutor,
		SkillExecutor:                  opts.SkillExecutor,
		WebsiteKnowledgeExecutor:       opts.WebsiteKnowledgeExecutor,
		ExternalMCP:                    external,
		AuditRecorder:                  productionMCPGatewayAuditRecorder{store: opts.AuditStore},
		Now:                            opts.Now,
	})
	return mcptransport.NewGateway(mcptransport.GatewayOptions{
		Registry:                opts.Registry,
		Router:                  router,
		Profiles:                opts.Profiles,
		Lifecycle:               opts.Lifecycle,
		ResponsibilityAuthority: opts.ResponsibilityAuthority,
	})
}

func newProductionSchedulerToolExecutor(service *schedulerapp.Service, dispatcher schedulerapp.RunDispatcher) *schedulerapp.ToolExecutor {
	return schedulerapp.NewToolExecutor(schedulerapp.ToolExecutorOptions{
		Service:    service,
		Dispatcher: dispatcher,
	})
}

type productionSchedulerToolCallPort struct {
	gateway productionMCPGatewayCaller
}

func (p productionSchedulerToolCallPort) CallTool(ctx context.Context, req schedulerapp.ToolCallRequest) (schedulerapp.ToolCallResult, error) {
	if p.gateway == nil {
		return schedulerapp.ToolCallResult{}, schedulerapp.ErrServiceDependency
	}
	arguments := cloneSchedulerToolArguments(req.Arguments)
	if key := strings.TrimSpace(req.IdempotencyKey); key != "" {
		arguments["idempotency_key"] = key
	}
	result, err := p.gateway.CallTool(ctx, mcptransport.CallToolRequest{
		TenantID:          strings.TrimSpace(req.TenantID),
		RunID:             strings.TrimSpace(req.RunID),
		ToolID:            req.ToolID,
		PrincipalID:       req.PrincipalID,
		ProfileID:         req.ProfileID,
		Permissions:       append([]string(nil), req.Permissions...),
		ExecutionEnvelope: agentcore.CloneExecutionEnvelope(req.ExecutionEnvelope),
		Arguments:         arguments,
	})
	if err != nil {
		return schedulerapp.ToolCallResult{}, err
	}
	return schedulerapp.ToolCallResult{Output: result.Output}, nil
}

func cloneSchedulerToolArguments(src map[string]any) map[string]any {
	if src == nil {
		return nil
	}
	dst := make(map[string]any, len(src))
	for key, value := range src {
		dst[key] = value
	}
	return dst
}

func (p productionAPI) MCPGateway() *mcptransport.Gateway {
	return p.mcpGateway
}

type productionEvaluationToolExecutor struct {
	executor interface {
		ExecuteEvaluationTool(context.Context, string, operations.EvaluationToolRequest) (operations.EvaluationToolResult, error)
	}
}

type productionMemoryDiaryToolExecutor struct {
	executor interface {
		ExecuteMemoryDiaryTool(context.Context, string, memorydiaryapp.ToolRequest) (memorydiaryapp.ToolResult, error)
	}
}

type productionMemorySearchToolExecutor struct {
	provider discussionsession.DiscussionAgentAuthorizedQueryMemoryProvider
}

func (e productionMemorySearchToolExecutor) ExecuteMemorySearchTool(ctx context.Context, req mcptransport.MemorySearchToolRequest) (mcptransport.MemorySearchToolResult, error) {
	if e.provider == nil {
		return mcptransport.MemorySearchToolResult{}, mcptransport.ErrServiceDependency
	}
	query := strings.TrimSpace(req.Query)
	if query == "" {
		return mcptransport.MemorySearchToolResult{}, discussionsession.ErrInvalidRequest
	}
	agentID := firstNonBlank(req.LogicalAgentID, req.AgentID)
	memory, err := e.provider.DiscussionAgentMemoryContextForAuthorizedQuery(ctx, discussionsession.DiscussionAgentMemoryAccessContext{
		TenantID:      strings.TrimSpace(req.TenantID),
		PrincipalID:   strings.TrimSpace(req.PrincipalID),
		PrincipalKind: strings.TrimSpace(req.PrincipalKind),
		AgentID:       strings.TrimSpace(req.AgentID),
	}, agentID, query)
	if err != nil {
		return mcptransport.MemorySearchToolResult{}, err
	}
	collective := make([]any, 0, len(memory.CollectiveDigests))
	for _, digest := range memory.CollectiveDigests {
		collective = append(collective, productionMemorySearchDigest(digest))
	}
	return mcptransport.MemorySearchToolResult{Output: map[string]any{
		"evidence_policy":    retrievalevidence.Guidance,
		"personal_digest":    productionMemorySearchDigest(memory.PersonalDigest),
		"collective_digests": collective,
	}}, nil
}

func productionMemorySearchDigest(digest discussionsession.DiscussionAgentMemoryDigest) map[string]any {
	return map[string]any{
		"evidence":             retrievalevidence.Clone(digest.Evidence),
		"retrieval_mode":       strings.TrimSpace(digest.RetrievalMode),
		"fallback_reason":      strings.TrimSpace(digest.FallbackReason),
		"digest_id":            strings.TrimSpace(digest.DigestID),
		"memory_kind":          strings.TrimSpace(digest.MemoryKind),
		"summary":              strings.TrimSpace(digest.Summary),
		"source_memory_refs":   append([]string(nil), digest.SourceMemory...),
		"source_evidence_refs": append([]string(nil), digest.SourceEvidence...),
		"confidence":           digest.Confidence,
	}
}

type productionEvaluationReviewUsageRecorder struct {
	store interface {
		GetEvaluationReview(context.Context, string) (operations.EvaluationReviewDTO, error)
		UpsertEvaluationReview(context.Context, operations.EvaluationReviewDTO) error
	}
}

func (e productionMemoryDiaryToolExecutor) ExecuteMemoryDiaryTool(ctx context.Context, toolID string, req mcptransport.MemoryDiaryToolRequest) (mcptransport.MemoryDiaryToolResult, error) {
	if e.executor == nil {
		return mcptransport.MemoryDiaryToolResult{}, mcptransport.ErrServiceDependency
	}
	result, err := e.executor.ExecuteMemoryDiaryTool(ctx, toolID, memorydiaryapp.ToolRequest{
		TenantID:          req.TenantID,
		PrincipalID:       req.PrincipalID,
		AgentID:           req.AgentID,
		Mode:              req.Mode,
		TargetAgentID:     req.TargetAgentID,
		Date:              req.Date,
		Force:             req.Force,
		Limit:             req.Limit,
		IdempotencyKey:    req.IdempotencyKey,
		Path:              req.Path,
		Content:           req.Content,
		SessionID:         req.SessionID,
		ContextRevision:   req.ContextRevision,
		Trigger:           req.Trigger,
		MissionID:         req.MissionID,
		TaskID:            req.TaskID,
		RunID:             req.RunID,
		Sensitivity:       req.Sensitivity,
		SourceSensitivity: req.SourceSensitivity,
		EvidenceRefs:      append([]string(nil), req.EvidenceRefs...),
		LineageRefs:       append([]string(nil), req.LineageRefs...),
	})
	if err != nil {
		return mcptransport.MemoryDiaryToolResult{}, err
	}
	return mcptransport.MemoryDiaryToolResult{Output: result.Output}, nil
}

func (r productionEvaluationReviewUsageRecorder) RecordEvaluationReviewUsage(ctx context.Context, reviewID string, tokenConsumed int64) error {
	if r.store == nil {
		return nil
	}
	if tokenConsumed <= 0 {
		return nil
	}
	review, err := r.store.GetEvaluationReview(ctx, reviewID)
	if err != nil {
		return err
	}
	review.EvaluationTokenConsumed = tokenConsumed
	return r.store.UpsertEvaluationReview(ctx, review)
}

func (e productionEvaluationToolExecutor) ExecuteEvaluationTool(ctx context.Context, toolID string, req mcptransport.EvaluationToolRequest) (mcptransport.EvaluationToolResult, error) {
	if e.executor == nil {
		return mcptransport.EvaluationToolResult{}, mcptransport.ErrServiceDependency
	}
	result, err := e.executor.ExecuteEvaluationTool(ctx, toolID, operations.EvaluationToolRequest{
		PrincipalID:        req.PrincipalID,
		AgentID:            req.AgentID,
		Mode:               req.Mode,
		TargetAgentID:      req.TargetAgentID,
		EvaluatorAgentID:   req.EvaluatorAgentID,
		Summary:            req.Summary,
		EstimatedHumanDays: req.EstimatedHumanDays,
		WindowDays:         req.WindowDays,
		IdempotencyKey:     req.IdempotencyKey,
		ReviewInputs:       productionEvaluationReviewInputs(req.ReviewInputs),
	})
	if err != nil {
		return mcptransport.EvaluationToolResult{}, err
	}
	return mcptransport.EvaluationToolResult{Output: result.Output}, nil
}

func productionEvaluationReviewInputs(inputs []mcptransport.EvaluationToolReviewInput) []operations.EvaluationToolReviewInput {
	if len(inputs) == 0 {
		return nil
	}
	out := make([]operations.EvaluationToolReviewInput, 0, len(inputs))
	for _, input := range inputs {
		out = append(out, operations.EvaluationToolReviewInput{
			AgentID:            input.AgentID,
			Summary:            input.Summary,
			EstimatedHumanDays: input.EstimatedHumanDays,
		})
	}
	return out
}

type productionMCPGatewayAuditRecorder struct {
	store *operations.AuditStore
}

func (r productionMCPGatewayAuditRecorder) RecordMCPToolAudit(ctx context.Context, event mcptransport.AuditEvent) error {
	if r.store == nil {
		return nil
	}
	severity := "info"
	result := "succeeded"
	if event.Outcome == mcptransport.AuditOutcomeDenied {
		severity = "warning"
		result = "denied"
	} else if event.Outcome == mcptransport.AuditOutcomeFailed {
		severity = "error"
		result = "failed"
	}
	entityID := event.ToolID
	if event.ServerToolID != "" {
		entityID = event.ServerToolID
	}
	metadata := map[string]string{
		"tenant_id":            event.TenantID,
		"agent_run_id":         event.RunID,
		"tool_id":              event.ToolID,
		"source":               string(event.Source),
		"server_tool_id":       event.ServerToolID,
		"downstream_tool_name": event.DownstreamToolName,
		"outcome":              string(event.Outcome),
	}
	if event.ResponsibilityContextID != "" {
		metadata["responsibility_context_id"] = event.ResponsibilityContextID
		metadata["responsibility_id"] = event.ResponsibilityID
		metadata["responsibility_policy_id"] = event.ResponsibilityPolicyID
		metadata["responsibility_policy_version"] = fmt.Sprint(event.ResponsibilityPolicyVersion)
	}
	if event.ExecutionEnvelopeID != "" {
		metadata["execution_envelope_id"] = event.ExecutionEnvelopeID
		metadata["execution_envelope_digest"] = event.ExecutionEnvelopeDigest
	}
	return r.store.RecordAuditEvent(ctx, operations.AuditEventDTO{
		ActorID:       event.PrincipalID,
		ViewerID:      event.PrincipalID,
		EntityType:    "mcp_tool_call",
		EntityID:      entityID,
		Action:        "mcp.tool.call",
		Result:        result,
		Severity:      severity,
		CorrelationID: event.CorrelationID,
		Summary:       event.Reason,
		Metadata:      metadata,
		OccurredAt:    event.OccurredAt,
	})
}
