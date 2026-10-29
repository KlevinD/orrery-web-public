package main

import (
	"context"
	"testing"

	agentcore "orrery_v2/internal/agent/core"
	runtimecore "orrery_v2/internal/agent/runtime/core"
	toolstd "orrery_v2/internal/resource/tool/std"
)

func TestProductionRuntimeToolDispatcherRoutesKnowledgeSearchAliasThroughMCPGateway(t *testing.T) {
	gateway := &productionRuntimeTestMCPGateway{}
	dispatcher := productionRuntimeToolDispatcher{mcpGateway: gateway}

	result, err := dispatcher.CallRuntimeTool(context.Background(), runtimecore.RuntimeToolCallRequest{
		TenantID: "tenant-knowledge", RunID: "run-knowledge", Name: "Knowledge", AgentID: "knowledge-agent",
		Input: map[string]any{"query": "2026-08-13 completed work", "limit": 10},
		ToolBindings: []agentcore.RuntimeToolBinding{{
			ToolID: toolstd.KnowledgeSearch, Alias: "Knowledge", GrantedPermissions: []string{toolstd.PermissionKnowledgeRead},
		}},
	})
	if err != nil {
		t.Fatalf("CallRuntimeTool(Knowledge): %v", err)
	}
	if gateway.request.ToolID != string(toolstd.KnowledgeSearch) || gateway.request.RunID != "run-knowledge" ||
		gateway.request.Arguments["query"] != "2026-08-13 completed work" || gateway.request.AgentID != "knowledge-agent" {
		t.Fatalf("mcp gateway request = %+v", gateway.request)
	}
	if len(gateway.request.Permissions) != 1 || gateway.request.Permissions[0] != toolstd.PermissionKnowledgeRead || result.Output["ok"] != true {
		t.Fatalf("request=%+v result=%+v", gateway.request, result.Output)
	}
}

func TestProductionRuntimeToolDispatcherRoutesScheduledTaskListAliasThroughMCPGateway(t *testing.T) {
	gateway := &productionRuntimeTestMCPGateway{}
	dispatcher := productionRuntimeToolDispatcher{mcpGateway: gateway}

	result, err := dispatcher.CallRuntimeTool(context.Background(), runtimecore.RuntimeToolCallRequest{
		TenantID: "tenant-scheduler", RunID: "run-scheduler", Name: "Scheduler", AgentID: "scheduler-agent",
		Input: map[string]any{"include_archived": false, "limit": 50},
		ToolBindings: []agentcore.RuntimeToolBinding{{
			ToolID: toolstd.ScheduledTaskList, Alias: "Scheduler", GrantedPermissions: []string{toolstd.PermissionSchedulerRead},
		}},
	})
	if err != nil {
		t.Fatalf("CallRuntimeTool(Scheduler): %v", err)
	}
	if gateway.request.ToolID != string(toolstd.ScheduledTaskList) || gateway.request.RunID != "run-scheduler" ||
		gateway.request.Arguments["limit"] != 50 || gateway.request.AgentID != "scheduler-agent" {
		t.Fatalf("mcp gateway request = %+v", gateway.request)
	}
	if len(gateway.request.Permissions) != 1 || gateway.request.Permissions[0] != toolstd.PermissionSchedulerRead || result.Output["ok"] != true {
		t.Fatalf("request=%+v result=%+v", gateway.request, result.Output)
	}
}
