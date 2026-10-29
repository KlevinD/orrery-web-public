package main

import (
	"context"
	"testing"

	agentcore "orrery_v2/internal/agent/core"
	runtimecore "orrery_v2/internal/agent/runtime/core"
	toolstd "orrery_v2/internal/resource/tool/std"
)

func TestProductionRuntimeToolDispatcherKeepsExplicitScopeContextForDiaryMaintenance(t *testing.T) {
	gateway := &productionRuntimeTestMCPGateway{}
	dispatcher := productionRuntimeToolDispatcher{mcpGateway: gateway}
	responsibility := &agentcore.ResponsibilityContext{
		ContextID: "autoplan-context", ResponsibilityID: "responsibility-autoplan", ResponsibilityVersion: 1,
		PolicyID: "policy-autoplan", PolicyVersion: 1, AgentProfileID: "agent-1",
		RequireExplicitToolScopes: true, TaskPermissionScopes: []string{"repository.read"},
	}
	result, err := dispatcher.CallRuntimeTool(context.Background(), runtimecore.RuntimeToolCallRequest{
		TenantID: "tenant-a", RunID: "run-autoplan", Name: string(toolstd.MemoryDiaryAppend), AgentID: "agent-1",
		ResponsibilityContext: responsibility,
		Input:                 map[string]any{"content": "- durable result"},
		ProviderSpecific: map[string]string{
			"diary_maintenance": "true", "diary_target_path": "memory/2026-08-14.md", "diary_target_date": "2026-08-14",
			"diary_session_id": "session-1", "diary_context_revision": "revision-1", "diary_trigger": "run_terminal", "diary_idempotency_key": "append-autoplan",
		},
		ToolBindings: []agentcore.RuntimeToolBinding{{ToolID: toolstd.MemoryDiaryAppend, GrantedPermissions: []string{toolstd.PermissionMemoryDiaryWrite}}},
	})
	if err != nil {
		t.Fatalf("CallRuntimeTool(memory_diary_append): %v", err)
	}
	if result.Output["ok"] != true || gateway.request.ResponsibilityContext == nil || !gateway.request.ResponsibilityContext.RequireExplicitToolScopes ||
		len(gateway.request.ResponsibilityContext.TaskPermissionScopes) != 1 || gateway.request.ResponsibilityContext.TaskPermissionScopes[0] != "repository.read" {
		t.Fatalf("gateway request = %+v result=%+v", gateway.request, result.Output)
	}
}
