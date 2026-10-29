package main

import (
	"context"
	"errors"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/agent/runtime/agentkernel"
	runtimecore "orrery_v2/internal/agent/runtime/core"
	toolstd "orrery_v2/internal/resource/tool/std"
)

func TestProductionAgentCoreSchedulerMutationAuthorization(t *testing.T) {
	for _, toolID := range []agentcore.ToolID{toolstd.ScheduledTaskCreate, toolstd.ScheduledTaskUpdate, toolstd.ScheduledTaskCancel, toolstd.ScheduledTaskRunNow} {
		t.Run(string(toolID), func(t *testing.T) {
			permission := toolstd.PermissionSchedulerWrite
			if toolID == toolstd.ScheduledTaskRunNow {
				permission = toolstd.PermissionSchedulerRun
			}
			for _, scenario := range []string{"owner", "alias", "missing-envelope", "missing-binding", "missing-grant", "read-only-envelope", "empty-envelope-scopes", "excluded-tool", "forbidden-action", "wrong-principal", "wrong-kind", "wrong-tenant", "wrong-run", "wrong-agent", "expired", "tampered", "website"} {
				t.Run(scenario, func(t *testing.T) {
					binding := agentcore.RuntimeToolBinding{ToolID: toolID, GrantedPermissions: []string{permission}}
					principal := agentcore.ExecutionPrincipal{ID: "H000001", Kind: agentcore.ExecutionPrincipalUser, TenantID: "minvia", PermissionScope: []string{permission}}
					e := &agentcore.ExecutionEnvelope{TenantID: "minvia", RunID: "scheduler-management", AgentID: "A000017", AgentProfileID: "A000017", PrincipalID: principal.ID, PrincipalKind: principal.Kind, PermissionScopes: []string{permission}, AllowedToolIDs: []string{string(toolID)}}
					call := runtimecore.RuntimeToolCallRequest{TenantID: e.TenantID, RunID: agentcore.AgentRunID(e.RunID), AgentID: agentcore.AgentID(e.AgentID), Name: string(toolID), ExecutionPrincipal: principal, ExecutionEnvelope: e}
					switch scenario {
					case "alias":
						binding.Alias, call.Name = "manage_schedule", "manage_schedule"
					case "missing-envelope":
						call.ExecutionEnvelope = nil
					case "missing-grant":
						binding.GrantedPermissions, call.ExecutionPrincipal.PermissionScope = nil, nil
					case "read-only-envelope":
						e.PermissionScopes = []string{toolstd.PermissionSchedulerRead}
						binding.GrantedPermissions = nil
					case "empty-envelope-scopes":
						e.PermissionScopes = nil
					case "excluded-tool":
						e.AllowedToolIDs = []string{string(toolstd.ScheduledTaskList)}
					case "forbidden-action":
						e.ForbiddenActions = []string{permission}
					case "wrong-principal":
						call.ExecutionPrincipal.ID = "H000004"
					case "wrong-kind":
						call.ExecutionPrincipal.Kind = agentcore.ExecutionPrincipalAgent
					case "wrong-tenant":
						call.TenantID = "other-organization"
					case "wrong-run":
						call.RunID = "another-run"
					case "wrong-agent":
						call.AgentID = "another-agent"
					case "expired":
						e.ExpiresAt = time.Now().Add(-time.Minute)
					case "website":
						call.AgentID = agentcore.AgentID(defaultWebsiteAdvisorAgentID)
						e.AgentID, e.AgentProfileID = string(call.AgentID), string(call.AgentID)
					}
					if err := e.Seal(); err != nil {
						t.Fatal(err)
					}
					if scenario == "tampered" {
						e.PrincipalID = "H000004"
					}
					if scenario != "missing-binding" {
						call.ToolBindings = []agentcore.RuntimeToolBinding{binding}
					}
					err := productionAgentCoreToolPolicy(context.Background(), call)
					allowed := scenario == "owner" || scenario == "alias"
					if (allowed && err != nil) || (!allowed && !errors.Is(err, agentkernel.ErrToolDenied)) {
						t.Fatalf("allowed=%v err=%v", allowed, err)
					}
				})
			}
		})
	}
}
