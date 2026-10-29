package agentkernel

import (
	"context"
	"orrery_v2/internal/agent/llm"
	"slices"

	agentcore "orrery_v2/internal/agent/core"
)

func (r *Runner) RuntimeExecutionSnapshot() agentcore.RuntimeExecutionSnapshot {
	r.mu.Lock()
	defer r.mu.Unlock()
	c := r.config
	sorted := func(values []string) []string {
		copy := append([]string{}, values...)
		slices.Sort(copy)
		return slices.Compact(copy)
	}
	policy := agentcore.RuntimeExecutionPolicy{
		Protocol: "orrery-agentcore-policy-v1", MaxModelCalls: r.modelCallLimit(), MaxToolCalls: r.toolCallLimit(),
		MaxParallelTools: c.MaxParallelTools, MaxConcurrentCallbacks: c.MaxConcurrentCallbacks,
		MaxContextBytes: c.MaxContextBytes, MaxResponseBytes: c.MaxResponseBytes,
		ModelTimeoutNS: int64(c.ModelTimeout), ToolTimeoutNS: int64(c.ToolTimeout), JournalTimeoutNS: int64(c.JournalTimeout),
		ApprovalTimeoutNS: int64(c.ApprovalTimeout), DefaultLongTimeoutNS: int64(c.LongTaskTimeout),
		AutoCompact: c.AutoCompact, CompactAtBytes: c.CompactAtBytes, CompactKeepMessages: c.CompactKeepMessages,
		CompactSummaryBytes: c.CompactSummaryBytes, PromptCache: c.PromptCache,
		Checkpoints: c.Checkpoints != nil, ToolJournal: c.ToolJournal != nil,
		ExplicitToolScopes: c.ExplicitToolScopeFence && c.AuthorizeTool != nil && c.Tools != nil && c.ToolJournal != nil,
		ParallelToolIDs:    sorted(c.ParallelToolIDs), ApprovalToolIDs: sorted(c.ApprovalToolIDs),
	}
	if r.request.TaskID != "" || hasFormalResultContract(r.request) {
		policy.BlockedTaskReviewProtocol = blockedTaskReviewProtocol
		policy.MaxBlockedTaskReviews = 1
	}
	result := agentcore.RuntimeExecutionSnapshot{Engine: "orrery-agentcore", TenantID: r.request.TenantID,
		AgentID: r.request.AgentID.String(), RunID: r.request.RunID.String(), Policy: policy,
		BackendSessionID: r.backendID, AutomaticRecovery: automaticTask(r.request),
		Continuing: r.continuing, ReplayCompleted: r.replayCompleted,
		ModelCalls: r.modelCalls, ToolCalls: r.toolCalls, Compactions: r.compactions, Deadline: r.deadline.UTC()}
	result.DependencyProtocol = agentcore.RuntimeDependencyProtocol
	if result.Deadline.IsZero() {
		result.Deadline = r.request.Deadline.UTC()
	}
	if identified, ok := r.model.(interface{ ModelIdentity() string }); ok {
		result.ModelIdentity = identified.ModelIdentity()
	}
	return result
}

func (r *Runner) observeDependency(ctx context.Context, kind string, input any, tools []*llm.Tool, binding *agentcore.RuntimeToolBinding, callID string) {
	if !agentcore.HasRuntimeDependencyObserver(ctx) {
		return
	}
	o := agentcore.RuntimeDependencyObservation{TenantID: r.request.TenantID, RunID: r.request.RunID.String(), AgentID: r.request.AgentID.String(), Kind: kind}
	if kind == "model_dispatch" {
		o.InputSHA256, _ = digest(input)
		o.ToolsSHA256 = dependencyToolsDigest(tools)
		if identified, ok := r.model.(interface{ ModelIdentity() string }); ok {
			o.ModelIdentity = identified.ModelIdentity()
		}
	}
	if kind == "tool_dispatch" && binding != nil {
		copy := *binding
		copy.SecretEnv = nil
		o.ToolID, o.Version = string(copy.ToolID), copy.Version
		o.BindingSHA256, _ = digest(copy)
		o.InputSHA256, _ = digest(input)
		o.CallSHA256, _ = digest(callID)
	}
	agentcore.ObserveRuntimeDependency(ctx, o)
}

func dependencyToolsDigest(tools []*llm.Tool) string {
	contracts := make([]any, 0, len(tools))
	for _, tool := range tools {
		if tool == nil {
			return ""
		}
		var parameters any
		if tool.Parameters != nil {
			parameters = tool.Parameters
		}
		contracts = append(contracts, map[string]any{"name": tool.Name, "description": tool.Description, "parameters": parameters, "extra": nil})
	}
	hash, _ := digest(contracts)
	return hash
}
