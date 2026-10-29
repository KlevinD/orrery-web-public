package main

import (
	"context"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	runtimecore "orrery_v2/internal/agent/runtime/core"
	toolres "orrery_v2/internal/resource/tool"
	toolstd "orrery_v2/internal/resource/tool/std"
	mcptransport "orrery_v2/internal/transport/mcp"
)

// Exercise the production dispatcher and gateway together: a recording gateway
// alone cannot detect a missing principal kind rejected by the owner check.
func TestProductionRuntimeWebToolsThroughOwnerCheckedGateway(t *testing.T) {
	for _, toolID := range []agentcore.ToolID{toolstd.WebSearch, toolstd.WebFetch} {
		for _, test := range []struct {
			name          string
			principalID   string
			principalKind agentcore.ExecutionPrincipalKind
			tenant        string
			sharedAgent   bool
			unbound       bool
			missingScope  bool
			wantError     error
		}{
			{name: "owner username", principalID: "owner", principalKind: agentcore.ExecutionPrincipalUser, tenant: "tenant-a"},
			{name: "owner human profile", principalID: "human-owner", principalKind: agentcore.ExecutionPrincipalUser, tenant: "tenant-a"},
			{name: "shared agent", principalID: "shared-agent", principalKind: agentcore.ExecutionPrincipalAgent, tenant: "tenant-a", sharedAgent: true},
			{name: "other owner", principalID: "other-user", principalKind: agentcore.ExecutionPrincipalUser, tenant: "tenant-a", wantError: mcptransport.ErrPermissionDenied},
			{name: "other tenant", principalID: "owner", principalKind: agentcore.ExecutionPrincipalUser, tenant: "tenant-b", wantError: mcptransport.ErrPermissionDenied},
			{name: "agent cannot impersonate owner", principalID: "owner", principalKind: agentcore.ExecutionPrincipalAgent, tenant: "tenant-a", wantError: mcptransport.ErrPermissionDenied},
			{name: "missing owner identity", tenant: "tenant-a", wantError: mcptransport.ErrPermissionDenied},
			{name: "missing profile binding", principalID: "owner", principalKind: agentcore.ExecutionPrincipalUser, tenant: "tenant-a", unbound: true, wantError: mcptransport.ErrToolNotFound},
			{name: "missing web permission", principalID: "owner", principalKind: agentcore.ExecutionPrincipalUser, tenant: "tenant-a", missingScope: true, wantError: mcptransport.ErrPermissionDenied},
		} {
			t.Run(string(toolID)+"/"+test.name, func(t *testing.T) {
				ctx := context.Background()
				registry := toolres.NewMemoryRegistry()
				for _, definition := range toolstd.Definitions(time.Unix(0, 0)) {
					if definition.ID == toolID {
						if err := registry.Register(ctx, definition); err != nil {
							t.Fatal(err)
						}
					}
				}
				profile := agentcore.AgentProfile{
					ID: "personal-assistant", AgentKind: agentcore.AgentKindAI, Enabled: true,
					PersonalAssistantOwnerID: "owner", PersonalAssistantTenantID: "tenant-a",
					ToolRefs: []agentcore.ToolRef{{ToolID: toolID, PermissionScope: toolstd.PermissionWebRead}},
				}
				if test.sharedAgent {
					profile.ID = "shared-agent"
					profile.PersonalAssistantOwnerID = ""
					profile.PersonalAssistantTenantID = ""
				}
				if test.unbound {
					profile.ToolRefs = nil
				}
				profiles := serverTestProfileStore{
					profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{
						profile.ID:    profile,
						"human-owner": {ID: "human-owner", AgentKind: agentcore.AgentKindHuman, Enabled: true},
					},
					page: agentcore.AgentDirectoryPage{Entries: []agentcore.AgentDirectoryEntry{{AgentProfileID: profile.ID}}},
				}
				human := profiles.profiles["human-owner"]
				human.UserAccount.Username = "owner"
				profiles.profiles[human.ID] = human
				executor := &recordingRuntimeWebExecutor{}
				dispatcher := productionRuntimeToolDispatcher{mcpGateway: newProductionMCPGateway(productionMCPGatewayOptions{
					Registry: registry, Profiles: profiles, WebExecutor: executor,
				})}
				permissions := []string{toolstd.PermissionWebRead}
				if test.missingScope {
					permissions = nil
				}
				result, err := dispatcher.CallRuntimeTool(ctx, runtimecore.RuntimeToolCallRequest{
					TenantID: test.tenant, RunID: "web-regression", AgentID: agentcore.AgentID(profile.ID), Name: string(toolID),
					ExecutionPrincipal: agentcore.ExecutionPrincipal{Kind: test.principalKind, ID: test.principalID, TenantID: test.tenant},
					ToolBindings:       []agentcore.RuntimeToolBinding{{ToolID: toolID, GrantedPermissions: permissions}},
					Input: map[string]any{
						"query": "public documentation", "url": "https://example.com/",
						"principal_id": "owner", "principal_kind": "user", "tenant_id": "tenant-a",
					},
				})
				if err != nil {
					t.Fatal(err)
				}
				if test.wantError != nil {
					if result.Output["status"] != "failed" || executor.calls != 0 {
						t.Fatalf("unauthorized call reached executor: output=%v calls=%d", result.Output, executor.calls)
					}
					got, _ := result.Output["error"].(string)
					if got != test.wantError.Error() && !(test.missingScope && got == test.wantError.Error()+": missing "+toolstd.PermissionWebRead) {
						t.Fatalf("error=%q, want %q", got, test.wantError)
					}
					return
				}
				if executor.calls != 1 || executor.tool != string(toolID) || result.Output["ok"] != true {
					t.Fatalf("authorized call failed: output=%v calls=%d tool=%q", result.Output, executor.calls, executor.tool)
				}
			})
		}
	}
}

type recordingRuntimeWebExecutor struct {
	calls int
	tool  string
}

func (e *recordingRuntimeWebExecutor) ExecuteWebTool(_ context.Context, toolID string, _ mcptransport.WebToolRequest) (mcptransport.WebToolResult, error) {
	e.calls++
	e.tool = toolID
	return mcptransport.WebToolResult{Output: map[string]any{"ok": true}}, nil
}
