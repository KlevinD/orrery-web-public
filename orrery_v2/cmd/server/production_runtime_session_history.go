package main

import (
	"context"
	"encoding/json"
	"slices"
	"strings"

	agentcore "orrery_v2/internal/agent/core"
	runtimecore "orrery_v2/internal/agent/runtime/core"
	mcptransport "orrery_v2/internal/transport/mcp"
)

func (d productionRuntimeToolDispatcher) callSessionHistoryTool(ctx context.Context, call runtimecore.RuntimeToolCallRequest, binding agentcore.RuntimeToolBinding) (runtimecore.RuntimeToolCallResult, error) {
	// Keep private-history protection at this entry point, including releases
	// whose other runtime tools predate the global allowlist dispatcher guard.
	if raw := strings.TrimSpace(call.ProviderSpecific[agentcore.RuntimeAllowedToolIDsProviderKey]); raw != "" {
		var allowed []string
		if json.Unmarshal([]byte(raw), &allowed) != nil || !slices.Contains(allowed, string(binding.ToolID)) {
			return runtimecore.RuntimeToolCallResult{}, mcptransport.ErrPermissionDenied
		}
	}
	return d.callMemorySearchTool(ctx, call, binding)
}
