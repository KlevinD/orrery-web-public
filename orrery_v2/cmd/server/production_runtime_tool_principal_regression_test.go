package main

import (
	"context"
	"errors"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	runtimecore "orrery_v2/internal/agent/runtime/core"
	schedulerapp "orrery_v2/internal/app/scheduler"
	toolres "orrery_v2/internal/resource/tool"
	toolstd "orrery_v2/internal/resource/tool/std"
	mcptransport "orrery_v2/internal/transport/mcp"
)

func TestRuntimeMCPBranchesPreserveExecutionPrincipalKind(t *testing.T) {
	for _, toolID := range []agentcore.ToolID{
		toolstd.ScheduledTaskList, toolstd.MemoryDiaryWrite, toolstd.EvaluationReview, toolstd.EmailSend,
	} {
		for _, kind := range []agentcore.ExecutionPrincipalKind{agentcore.ExecutionPrincipalUser, agentcore.ExecutionPrincipalAgent} {
			t.Run(string(toolID)+"/"+string(kind), func(t *testing.T) {
				gateway := &productionRuntimeTestMCPGateway{}
				dispatcher := productionRuntimeToolDispatcher{mcpGateway: gateway}
				principalID := "H000001"
				if kind == agentcore.ExecutionPrincipalAgent {
					principalID = "A000017"
				}
				result, err := dispatcher.callRuntimeTool(context.Background(), runtimecore.RuntimeToolCallRequest{
					TenantID: "minvia", RunID: "principal-test", AgentID: "A000017", Name: string(toolID),
					ExecutionPrincipal: agentcore.ExecutionPrincipal{ID: principalID, Kind: kind, TenantID: "minvia", PermissionScope: []string{"tool.call"}},
					ToolBindings:       []agentcore.RuntimeToolBinding{{ToolID: toolID, GrantedPermissions: []string{"tool.call"}}},
				})
				if err != nil || result.Output["ok"] != true {
					t.Fatalf("dispatch result=%+v err=%v", result, err)
				}
				if gateway.request.PrincipalID != principalID || gateway.request.PrincipalKind != string(kind) || gateway.request.AgentID != "A000017" {
					t.Fatalf("principal lost: id=%q kind=%q agent=%q", gateway.request.PrincipalID, gateway.request.PrincipalKind, gateway.request.AgentID)
				}
			})
		}
	}
}

func TestRuntimeSchedulerPersonalAssistantOwnerCanListTasks(t *testing.T) {
	for _, tc := range []struct {
		name, tenantID, principalID string
		kind                        agentcore.ExecutionPrincipalKind
		permissions                 []string
		wantAllowed                 bool
	}{
		{"owner", "minvia", "H000001", agentcore.ExecutionPrincipalUser, []string{toolstd.PermissionSchedulerRead}, true},
		{"other_user", "minvia", "H000004", agentcore.ExecutionPrincipalUser, []string{toolstd.PermissionSchedulerRead}, false},
		{"wrong_tenant", "other-tenant", "H000001", agentcore.ExecutionPrincipalUser, []string{toolstd.PermissionSchedulerRead}, false},
		{"agent_identity", "minvia", "A000017", agentcore.ExecutionPrincipalAgent, []string{toolstd.PermissionSchedulerRead}, false},
		{"missing_scope", "minvia", "H000001", agentcore.ExecutionPrincipalUser, []string{toolstd.PermissionDiscussionRead}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			registry := toolres.NewMemoryRegistry()
			for _, definition := range toolstd.Definitions(time.Now().UTC()) {
				if err := registry.Register(ctx, definition); err != nil {
					t.Fatalf("register %s: %v", definition.ID, err)
				}
			}
			executor := &principalRegressionSchedulerExecutor{}
			gateway := newProductionMCPGateway(productionMCPGatewayOptions{
				Registry: registry, SchedulerExecutor: executor, Profiles: principalRegressionProfileStore{},
			})
			dispatcher := productionRuntimeToolDispatcher{mcpGateway: gateway}
			result, err := dispatcher.callRuntimeTool(ctx, runtimecore.RuntimeToolCallRequest{
				TenantID: tc.tenantID, RunID: "principal-owner-test", Name: string(toolstd.ScheduledTaskList), AgentID: "A000017",
				ExecutionPrincipal: agentcore.ExecutionPrincipal{ID: tc.principalID, Kind: tc.kind, TenantID: tc.tenantID, PermissionScope: tc.permissions},
				ToolBindings:       []agentcore.RuntimeToolBinding{{ToolID: toolstd.ScheduledTaskList}},
				Input:              map[string]any{"limit": 5},
			})
			if tc.wantAllowed {
				if err != nil || result.Output["ok"] != true || executor.calls != 1 {
					t.Fatalf("owner list result=%+v err=%v executor calls=%d", result, err, executor.calls)
				}
				return
			}
			if !errors.Is(err, mcptransport.ErrPermissionDenied) || executor.calls != 0 {
				t.Fatalf("unauthorized dispatch err=%v executor calls=%d", err, executor.calls)
			}
		})
	}
}

type principalRegressionSchedulerExecutor struct{ calls int }

func (e *principalRegressionSchedulerExecutor) Execute(_ context.Context, _ schedulerapp.ToolExecutionRequest) (schedulerapp.ToolExecutionResult, error) {
	e.calls++
	return schedulerapp.ToolExecutionResult{Output: map[string]any{"ok": true}}, nil
}

type principalRegressionProfileStore struct{}

func (principalRegressionProfileStore) GetProfile(_ context.Context, id agentcore.AgentProfileID) (agentcore.AgentProfile, error) {
	switch id {
	case "A000017":
		return agentcore.AgentProfile{
			ID: id, Enabled: true, AgentKind: agentcore.AgentKindAI,
			PersonalAssistantOwnerID: "ding", PersonalAssistantTenantID: "minvia",
			ToolRefs: []agentcore.ToolRef{{ToolID: toolstd.ScheduledTaskList, PermissionScope: toolstd.PermissionSchedulerRead}},
		}, nil
	case "H000001":
		return agentcore.AgentProfile{ID: id, Enabled: true, AgentKind: agentcore.AgentKindHuman, UserAccount: agentcore.UserAccountSpec{Username: "ding"}}, nil
	case "H000004":
		return agentcore.AgentProfile{ID: id, Enabled: true, AgentKind: agentcore.AgentKindHuman, UserAccount: agentcore.UserAccountSpec{Username: "lipi"}}, nil
	}
	return agentcore.AgentProfile{}, mcptransport.ErrToolNotFound
}

func (principalRegressionProfileStore) QueryDirectory(_ context.Context, q agentcore.AgentDirectoryQuery) (agentcore.AgentDirectoryPage, error) {
	if !q.IncludePersonalAssistants {
		return agentcore.AgentDirectoryPage{}, nil
	}
	return agentcore.AgentDirectoryPage{Entries: []agentcore.AgentDirectoryEntry{{AgentProfileID: "A000017", AgentInstanceID: "A000017"}}}, nil
}
