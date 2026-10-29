package main

import (
	"context"
	"testing"

	agentcore "orrery_v2/internal/agent/core"
	runtimecore "orrery_v2/internal/agent/runtime/core"
	toolstd "orrery_v2/internal/resource/tool/std"
)

func TestProductionAgentCorePersonalBoundResourcesUseOwnerEnvelope(t *testing.T) {
	profile := newPersonalAssistantProfile("pa-resource-test", "tenant-a", "owner-a", agentcore.AgentProfile{})
	profile.ToolRefs = append(profile.ToolRefs, agentcore.ToolRef{ToolID: "mcp.browser.navigate_page", PermissionScope: "browser.server.read"})
	bindings := []agentcore.RuntimeToolBinding{
		{ToolID: "mcp.browser.navigate_page", Source: "mcp_server", ServerToolID: "mcp.browser", DownstreamToolName: "navigate_page", GrantedPermissions: []string{"browser.server.read"}},
		{ToolID: toolstd.RSILearningPlan, GrantedPermissions: []string{toolstd.PermissionRSILearningRead}},
	}
	principal := agentcore.ExecutionPrincipal{Kind: agentcore.ExecutionPrincipalUser, ID: "human-owner", TenantID: "tenant-a", Source: "system_assistant", PermissionScope: []string{"tool.call"}}
	request := agentcore.NewBaseAgent(agentcore.BaseAgentConfig{Identity: agentcore.AgentIdentity{AgentID: agentcore.AgentID(profile.ID), AgentProfileID: profile.ID}, Profile: profile}).BuildRuntimeRequest(agentcore.AgentInput{RunID: "run-resources", TenantID: "tenant-a", ExecutionPrincipal: principal, RuntimeToolBindings: bindings})
	for _, binding := range bindings {
		call := runtimecore.RuntimeToolCallRequest{TenantID: "tenant-a", AgentID: agentcore.AgentID(profile.ID), RunID: "run-resources", Name: string(binding.ToolID), ToolBindings: bindings, ExecutionPrincipal: principal, ExecutionEnvelope: request.ExecutionEnvelope}
		if err := productionAgentCoreToolPolicy(context.Background(), call); err != nil {
			t.Fatalf("bound %s: %v", binding.ToolID, err)
		}
		call.AgentID = agentcore.AgentID(defaultWebsiteAdvisorAgentID)
		call.ExecutionEnvelope = agentcore.CloneExecutionEnvelope(request.ExecutionEnvelope)
		call.ExecutionEnvelope.AgentID = call.AgentID.String()
		call.ExecutionEnvelope.AgentProfileID = call.AgentID.String()
		if err := call.ExecutionEnvelope.Seal(); err != nil {
			t.Fatal(err)
		}
		if productionAgentCoreToolPolicy(context.Background(), call) == nil {
			t.Fatalf("website accepted internal resource %s", binding.ToolID)
		}
		call.AgentID = agentcore.AgentID(profile.ID)
		call.ExecutionEnvelope = request.ExecutionEnvelope
		call.ToolBindings = nil
		if productionAgentCoreToolPolicy(context.Background(), call) == nil {
			t.Fatalf("unbound resource %s accepted", binding.ToolID)
		}
	}
}
