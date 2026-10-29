package main

import (
	"context"
	"testing"

	agentcore "orrery_v2/internal/agent/core"
	runtimecore "orrery_v2/internal/agent/runtime/core"
	"orrery_v2/internal/app/discussionsession"
	toolstd "orrery_v2/internal/resource/tool/std"
)

func TestProductionRuntimeToolDispatcherRoutesMemorySearchWithLogicalAssistantIdentity(t *testing.T) {
	gateway := &productionRuntimeTestMCPGateway{}
	dispatcher := productionRuntimeToolDispatcher{mcpGateway: gateway}
	logicalID := discussionsession.PersonalAssistantAgentID("tenant-a", "account-1")

	result, err := dispatcher.CallRuntimeTool(context.Background(), runtimecore.RuntimeToolCallRequest{
		TenantID: "tenant-a",
		Name:     string(toolstd.MemorySearch),
		AgentID:  "lead-agent",
		Input:    map[string]any{"query": "How does the owner prefer plans?"},
		ProviderSpecific: map[string]string{
			personalAssistantLogicalAgentIDKey: logicalID,
		},
		ToolBindings: []agentcore.RuntimeToolBinding{{
			ToolID: toolstd.MemorySearch, Alias: "memory_search", GrantedPermissions: []string{toolstd.PermissionMemoryRead},
		}},
	})
	if err != nil {
		t.Fatalf("CallRuntimeTool(memory_search): %v", err)
	}
	if gateway.request.ToolID != string(toolstd.MemorySearch) || gateway.request.AgentID != "lead-agent" ||
		gateway.request.LogicalAgentID != logicalID || gateway.request.Arguments["query"] != "How does the owner prefer plans?" {
		t.Fatalf("mcp gateway request = %+v", gateway.request)
	}
	if len(gateway.request.Permissions) != 1 || gateway.request.Permissions[0] != toolstd.PermissionMemoryRead || result.Output["ok"] != true {
		t.Fatalf("request=%+v result=%+v", gateway.request, result.Output)
	}
}
