package mcp

import (
	"context"
	"errors"
	agentcore "orrery_v2/internal/agent/core"
	schedulerapp "orrery_v2/internal/app/scheduler"
	toolstd "orrery_v2/internal/resource/tool/std"
	"testing"
	"time"
)

type personalEnvelopeAuthority struct{ envelope *agentcore.ExecutionEnvelope }

func (a personalEnvelopeAuthority) ResolveResponsibilityExecution(context.Context, string) (*ResponsibilityExecutionAuthority, error) {
	e := a.envelope
	return &ResponsibilityExecutionAuthority{RunID: e.RunID, TenantID: e.TenantID, AgentID: e.AgentID, AgentProfileID: e.AgentProfileID, Envelope: agentcore.CloneExecutionEnvelope(e)}, nil
}

func TestGatewayPersonalAssistantBoundEnvelopePreservesUserAuthority(t *testing.T) {
	registry := newGatewayTestRegistry(t)
	e := &agentcore.ExecutionEnvelope{TenantID: "minvia", RunID: "run-personal", AgentID: "A000017", AgentProfileID: "A000017", PrincipalID: "H000001", PrincipalKind: agentcore.ExecutionPrincipalUser, PermissionScopes: []string{toolstd.PermissionKnowledgeRead}}
	if err := e.SealAt(time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, tenant, principal, kind string
		permissions                   []string
		allowed                       bool
	}{
		{"owner", "minvia", "H000001", "user", []string{toolstd.PermissionKnowledgeRead}, true},
		{"other-user", "minvia", "H000004", "user", []string{toolstd.PermissionKnowledgeRead}, false},
		{"technical-agent", "minvia", "A000017", "agent", []string{toolstd.PermissionKnowledgeRead}, false},
		{"wrong-kind", "minvia", "H000001", "agent", []string{toolstd.PermissionKnowledgeRead}, false},
		{"other-tenant", "public", "H000001", "user", []string{toolstd.PermissionKnowledgeRead}, false},
		{"missing-permission", "minvia", "H000001", "user", nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			executor := &recordingKnowledgeToolExecutor{result: KnowledgeToolResult{Output: map[string]any{"ok": true}}}
			gateway := NewGateway(GatewayOptions{Registry: registry, Profiles: newPersonalAssistantGatewayProfileStore(), ResponsibilityAuthority: personalEnvelopeAuthority{e}, Router: NewToolRouter(ToolRouterOptions{Registry: registry, KnowledgeExecutor: executor})})
			_, err := gateway.CallTool(context.Background(), CallToolRequest{RunID: e.RunID, TenantID: tc.tenant, AgentID: e.AgentID, PrincipalID: tc.principal, PrincipalKind: tc.kind, Permissions: tc.permissions, PermissionsAuthoritative: true, Arguments: map[string]any{"query": "knowledge inventory", "retrieval_mode": "fast"}, ToolID: string(toolstd.KnowledgeSearch)})
			if tc.allowed {
				if err != nil || executor.request.PrincipalID != "H000001" || executor.request.TenantID != "minvia" {
					t.Fatalf("owner request=%+v err=%v", executor.request, err)
				}
			} else if !errors.Is(err, ErrPermissionDenied) || executor.calledToolID != "" {
				t.Fatalf("unauthorized request executed: %s %v", executor.calledToolID, err)
			}
		})
	}
}

func TestGatewayPersonalAssistantSchedulerMutationsPreserveOwnerAuthority(t *testing.T) {
	for _, toolID := range []agentcore.ToolID{toolstd.ScheduledTaskCreate, toolstd.ScheduledTaskUpdate, toolstd.ScheduledTaskCancel, toolstd.ScheduledTaskRunNow} {
		t.Run(string(toolID), func(t *testing.T) {
			permission := toolstd.PermissionSchedulerWrite
			if toolID == toolstd.ScheduledTaskRunNow {
				permission = toolstd.PermissionSchedulerRun
			}
			for _, scenario := range []string{"owner", "other-user", "other-tenant", "missing-permission", "missing-binding"} {
				t.Run(scenario, func(t *testing.T) {
					profiles := newPersonalAssistantGatewayProfileStore()
					if scenario != "missing-binding" {
						profiles.profile.ToolRefs = append(profiles.profile.ToolRefs, agentcore.ToolRef{ToolID: toolID, PermissionScope: permission})
					}
					e := &agentcore.ExecutionEnvelope{TenantID: "minvia", RunID: "run-manage-schedule", AgentID: "A000017", AgentProfileID: "A000017", PrincipalID: "H000001", PrincipalKind: agentcore.ExecutionPrincipalUser, PermissionScopes: []string{permission}, AllowedToolIDs: []string{string(toolID)}}
					if err := e.Seal(); err != nil {
						t.Fatal(err)
					}
					executor := &recordingSchedulerExecutor{}
					registry := newGatewayTestRegistry(t)
					gateway := NewGateway(GatewayOptions{Registry: registry, Profiles: profiles, ResponsibilityAuthority: personalEnvelopeAuthority{e}, Router: NewToolRouter(ToolRouterOptions{Registry: registry, SchedulerExecutor: executor})})
					req := CallToolRequest{RunID: e.RunID, TenantID: e.TenantID, AgentID: e.AgentID, PrincipalID: e.PrincipalID, PrincipalKind: "user", ToolID: string(toolID), Permissions: []string{permission}, PermissionsAuthoritative: true, Arguments: map[string]any{"scheduled_task_id": "fixture-task"}}
					switch scenario {
					case "other-user":
						req.PrincipalID = "H000004"
					case "other-tenant":
						req.TenantID = "public"
					case "missing-permission":
						req.Permissions = nil
					}
					_, err := gateway.CallTool(context.Background(), req)
					if scenario == "owner" {
						if err != nil || executor.calledToolID != string(toolID) || executor.viewer.ViewerID != "H000001" || executor.viewer.TenantID != "minvia" || executor.viewer.ViewerKind != schedulerapp.ViewerKindUser || executor.viewer.ProfileID != "H000001" {
							t.Fatalf("scheduler viewer=%+v tool=%s err=%v", executor.viewer, executor.calledToolID, err)
						}
					} else if err == nil || executor.calledToolID != "" {
						t.Fatalf("unauthorized scheduler side effect: tool=%s err=%v", executor.calledToolID, err)
					}
				})
			}
		})
	}
}

func TestGatewayPersonalAssistantBoundMCPGrantKeepsACLsAndEnvelopeFences(t *testing.T) {
	ctx := context.Background()
	registry := newGatewayTestRegistry(t)
	registerGatewayTestExternalMCP(t, ctx, registry)
	profiles := newPersonalAssistantGatewayProfileStore()
	profiles.profile.ToolRefs = []agentcore.ToolRef{{ToolID: "mcp.github.search_issues", PermissionScope: "github.read"}}
	principal := agentcore.ExecutionPrincipal{Kind: agentcore.ExecutionPrincipalUser, ID: "H000001", TenantID: "minvia", Source: "system_assistant", PermissionScope: []string{"tool.call"}}
	request := agentcore.NewBaseAgent(agentcore.BaseAgentConfig{Identity: agentcore.AgentIdentity{AgentID: "A000017", AgentProfileID: "A000017"}, Profile: profiles.profile}).BuildRuntimeRequest(agentcore.AgentInput{
		RunID: "run-bound-mcp", TenantID: "minvia", ExecutionPrincipal: principal,
		RuntimeToolBindings: []agentcore.RuntimeToolBinding{{ToolID: "mcp.github.search_issues", Source: "mcp_server", ServerToolID: "mcp.github", DownstreamToolName: "search_issues", GrantedPermissions: []string{"github.read"}}},
	})
	for _, name := range []string{"owner", "other-user", "other-tenant", "unbound-tool", "forbidden-scope", "missing-permission", "grant-tamper"} {
		t.Run(name, func(t *testing.T) {
			envelope := agentcore.CloneExecutionEnvelope(request.ExecutionEnvelope)
			call := CallToolRequest{RunID: envelope.RunID, TenantID: "minvia", AgentID: "A000017", PrincipalID: "H000001", PrincipalKind: "user", Permissions: []string{"tool.call", "github.read"}, PermissionsAuthoritative: true, ToolID: "mcp.github.search_issues", Arguments: map[string]any{"query": "test"}}
			switch name {
			case "other-user":
				call.PrincipalID = "H000004"
			case "other-tenant":
				call.TenantID = "other"
			case "unbound-tool":
				call.ToolID = "mcp.github.create_issue"
			case "forbidden-scope":
				envelope.ForbiddenActions = []string{"github.read"}
				if err := envelope.Seal(); err != nil {
					t.Fatal(err)
				}
			case "missing-permission":
				call.Permissions = []string{"tool.call"}
			case "grant-tamper":
				envelope.BoundToolPermissionScopes["mcp.github.search_issues"] = []string{"*"}
			}
			external := &recordingExternalMCPClient{result: CallToolResult{Output: map[string]any{"ok": true}}}
			gateway := NewGateway(GatewayOptions{Registry: registry, Profiles: profiles, ResponsibilityAuthority: personalEnvelopeAuthority{envelope}, Router: NewToolRouter(ToolRouterOptions{Registry: registry, ExternalMCP: external})})
			_, err := gateway.CallTool(ctx, call)
			if name == "owner" {
				if err != nil || external.toolName != "search_issues" {
					t.Fatalf("bound owner MCP failed: %v", err)
				}
			} else if err == nil || external.toolName != "" {
				t.Fatal("unauthorized MCP executed")
			}
		})
	}
}
