package mcp

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	schedulerapp "orrery_v2/internal/app/scheduler"
	skillres "orrery_v2/internal/resource/skill"
	toolres "orrery_v2/internal/resource/tool"
	toolstd "orrery_v2/internal/resource/tool/std"
)

func TestGatewayListsInternalStandardAndExternalMCPTools(t *testing.T) {
	ctx := context.Background()
	registry := newGatewayTestRegistry(t)
	if err := registry.Register(ctx, toolstd.SystemMCPServerDefinition(time.Date(2026, 6, 8, 10, 0, 0, 0, time.UTC))); err != nil {
		t.Fatalf("register system mcp: %v", err)
	}
	registerGatewayTestExternalMCP(t, ctx, registry)

	gateway := NewGateway(GatewayOptions{Registry: registry})
	tools, err := gateway.ListTools(ctx, ListToolsRequest{})
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}

	byID := map[string]ToolDescriptor{}
	for _, item := range tools {
		byID[item.ToolID] = item
	}
	if _, ok := byID[string(toolstd.MissionContextList)]; ok {
		t.Fatalf("legacy context tool should not be published: %+v", byID[string(toolstd.MissionContextList)])
	}
	schedulerCreate, ok := byID[string(toolstd.ScheduledTaskCreate)]
	if !ok {
		t.Fatalf("scheduled task create tool was not published")
	}
	if schedulerCreate.Source != ToolSourceInternal || schedulerCreate.InputSchema == nil {
		t.Fatalf("scheduler tool descriptor = %+v", schedulerCreate)
	}
	if !containsString(schedulerCreate.RequiredPermissions, toolstd.PermissionSchedulerWrite) {
		t.Fatalf("scheduler permissions = %+v", schedulerCreate.RequiredPermissions)
	}

	githubSearch, ok := byID["mcp.github.search_issues"]
	if !ok {
		t.Fatalf("external mcp tool was not published; tools = %+v", byID)
	}
	if githubSearch.Source != ToolSourceExternalMCP ||
		githubSearch.ServerToolID != "mcp.github" ||
		githubSearch.DownstreamToolName != "search_issues" {
		t.Fatalf("external descriptor = %+v", githubSearch)
	}
	if !containsString(githubSearch.RequiredPermissions, "github.read") {
		t.Fatalf("external permissions = %+v", githubSearch.RequiredPermissions)
	}
	if githubSearch.InputSchema == nil {
		t.Fatalf("external tool should expose a generic argument schema")
	}
	if _, ok := byID["mcp.system."+string(toolstd.MissionPrepare)]; ok {
		t.Fatalf("system mcp server should not be expanded as external duplicate: %+v", byID["mcp.system."+string(toolstd.MissionPrepare)])
	}
}

func TestGatewayListsMCPServerCatalogTools(t *testing.T) {
	ctx := context.Background()
	registry := newGatewayTestRegistry(t)
	if err := registry.Register(ctx, toolstd.SystemMCPServerDefinition(time.Date(2026, 6, 8, 10, 0, 0, 0, time.UTC))); err != nil {
		t.Fatalf("register system mcp: %v", err)
	}
	registerGatewayTestExternalMCP(t, ctx, registry)
	gateway := NewGateway(GatewayOptions{Registry: registry})

	result, err := gateway.ListMCPServerToolSet(ctx)
	if err != nil {
		t.Fatalf("ListMCPServerToolSet: %v", err)
	}

	byID := map[string]ToolDescriptor{}
	for _, item := range result.Tools {
		byID[item.ToolID] = item
	}
	if result.TotalAvailable != len(toolstd.SystemMCPServerDefinition(time.Date(2026, 6, 8, 10, 0, 0, 0, time.UTC)).MCP.ExposedTools)+2 {
		t.Fatalf("catalog total = %+v", result)
	}
	if byID[string(toolstd.MissionPrepare)].Source != ToolSourceExternalMCP ||
		byID[string(toolstd.MissionPrepare)].ServerToolID != string(toolstd.SystemMCPServer) ||
		byID[string(toolstd.MissionPrepare)].DownstreamToolName != string(toolstd.MissionPrepare) {
		t.Fatalf("system mcp catalog descriptor = %+v", byID[string(toolstd.MissionPrepare)])
	}
	if _, ok := byID["mcp.system."+string(toolstd.MissionPrepare)]; ok {
		t.Fatalf("system mcp catalog should keep runtime tool id stable: %+v", byID["mcp.system."+string(toolstd.MissionPrepare)])
	}
	if byID["mcp.github.search_issues"].ServerToolID != "mcp.github" ||
		byID["mcp.github.search_issues"].DownstreamToolName != "search_issues" {
		t.Fatalf("external mcp catalog descriptor = %+v", byID["mcp.github.search_issues"])
	}
	if _, ok := byID[string(toolstd.ScheduledTaskCreate)]; !ok {
		t.Fatalf("system mcp catalog should include exposed system tool ids: %+v", result.Tools)
	}
	if _, ok := byID[string(toolstd.KnowledgeSearch)]; !ok {
		t.Fatalf("system mcp catalog should expose knowledge search: %+v", result.Tools)
	}
}

func TestGatewayListToolSetDefaultsToAllConfiguredTools(t *testing.T) {
	ctx := context.Background()
	registry := newGatewayTestRegistry(t)
	registerGatewayTestExternalMCP(t, ctx, registry)
	gateway := NewGateway(GatewayOptions{Registry: registry})

	result, err := gateway.ListToolSet(ctx, ListToolsRequest{
		ToolIDs: []string{string(toolstd.ScheduledTaskList), "mcp.github"},
	})
	if err != nil {
		t.Fatalf("ListToolSet: %v", err)
	}

	if result.Filtered || result.Truncated || result.TotalAvailable != 3 || len(result.Tools) != 3 {
		t.Fatalf("default all configured result = %+v", result)
	}
	byID := map[string]bool{}
	for _, item := range result.Tools {
		byID[item.ToolID] = true
	}
	for _, want := range []string{string(toolstd.ScheduledTaskList), "mcp.github.search_issues", "mcp.github.create_issue"} {
		if !byID[want] {
			t.Fatalf("configured tool %q missing from %+v", want, result.Tools)
		}
	}
}

func TestGatewayRejectsUnboundExecutionEnvelopeClaimsForToolListing(t *testing.T) {
	gateway := NewGateway(GatewayOptions{Registry: newGatewayTestRegistry(t)})
	_, err := gateway.ListToolSet(context.Background(), ListToolsRequest{
		ExecutionEnvelopeIDClaim:     "execution-envelope:run-1",
		ExecutionEnvelopeDigestClaim: "sha256:unbound",
	})
	if !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("ListToolSet error = %v, want ErrPermissionDenied", err)
	}
}

func TestGatewayListToolSetUsesAgentConfiguredToolRefs(t *testing.T) {
	ctx := context.Background()
	registry := newGatewayTestRegistry(t)
	registerGatewayTestExternalMCP(t, ctx, registry)
	profiles := fakeGatewayProfileStore{
		profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{
			"lead-agent": {
				ID: "lead-agent",
				ToolRefs: []agentcore.ToolRef{
					{ToolID: agentcore.ToolID(toolstd.ScheduledTaskList)},
					{ToolID: agentcore.ToolID("mcp.github")},
				},
			},
		},
		page: agentcore.AgentDirectoryPage{Entries: []agentcore.AgentDirectoryEntry{{
			AgentProfileID:  "lead-agent",
			AgentInstanceID: "lead-agent-runtime",
		}}},
	}
	gateway := NewGateway(GatewayOptions{Registry: registry, Profiles: profiles})

	result, err := gateway.ListToolSet(ctx, ListToolsRequest{AgentID: "lead-agent-runtime", Mode: "all"})
	if err != nil {
		t.Fatalf("ListToolSet: %v", err)
	}

	if result.TotalAvailable != 7 || len(result.Tools) != 7 {
		t.Fatalf("agent configured tools = %+v", result)
	}
	for _, item := range result.Tools {
		if item.ToolID != string(toolstd.ScheduledTaskList) &&
			item.ServerToolID != "mcp.github" &&
			!isSkillToolID(item.ToolID) {
			t.Fatalf("unbound tool leaked into agent tool list: %+v", item)
		}
	}
}

func TestGatewayUsesServerOwnedResponsibilityAuthorityForToolListing(t *testing.T) {
	ctx := context.Background()
	registry := newGatewayTestRegistry(t)
	profiles := fakeGatewayProfileStore{profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{
		"scheduler-agent": {
			ID: "scheduler-agent",
			ToolRefs: []agentcore.ToolRef{
				{ToolID: toolstd.ScheduledTaskList, PermissionScope: toolstd.PermissionSchedulerRead},
				{ToolID: toolstd.ScheduledTaskRunNow, PermissionScope: toolstd.PermissionSchedulerRun},
			},
		},
	}}
	authority := &fakeResponsibilityAuthority{context: testGatewayResponsibilityContext()}
	gateway := NewGateway(GatewayOptions{Registry: registry, Profiles: profiles, ResponsibilityAuthority: authority})

	result, err := gateway.ListToolSet(ctx, ListToolsRequest{RunID: "run-responsibility-1", AgentID: "scheduler-agent", Mode: "all"})
	if err != nil {
		t.Fatalf("ListToolSet: %v", err)
	}
	if authority.runID != "run-responsibility-1" || result.TotalAvailable != 1 || len(result.Tools) != 1 || result.Tools[0].ToolID != string(toolstd.ScheduledTaskList) {
		t.Fatalf("authority run=%q tools=%+v", authority.runID, result)
	}
}

func TestGatewayRejectsToolOutsideServerOwnedResponsibilityAuthority(t *testing.T) {
	ctx := context.Background()
	registry := newGatewayTestRegistry(t)
	executor := &recordingSchedulerExecutor{}
	audit := &recordingAuditRecorder{}
	profiles := fakeGatewayProfileStore{profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{
		"scheduler-agent": {
			ID:       "scheduler-agent",
			ToolRefs: []agentcore.ToolRef{{ToolID: toolstd.ScheduledTaskRunNow, PermissionScope: toolstd.PermissionSchedulerRun}},
		},
	}}
	authorityContext := testGatewayResponsibilityContext()
	gateway := NewGateway(GatewayOptions{
		Registry: registry, Profiles: profiles, ResponsibilityAuthority: &fakeResponsibilityAuthority{context: authorityContext},
		Router: NewToolRouter(ToolRouterOptions{Registry: registry, SchedulerExecutor: executor, AuditRecorder: audit, Now: fixedGatewayTestNow}),
	})
	claimed := agentcore.CloneResponsibilityContext(authorityContext)
	claimed.AllowedTools = []string{"*"}
	claimed.PolicyToolScopes = []string{"*"}
	claimed.TaskPermissionScopes = []string{"*"}

	_, err := gateway.CallTool(ctx, CallToolRequest{
		TenantID: "tenant-1", RunID: "run-responsibility-1", ToolID: string(toolstd.ScheduledTaskRunNow), PrincipalID: "scheduler-agent", AgentID: "scheduler-agent",
		ResponsibilityContext: claimed, Arguments: map[string]any{"scheduled_task_id": "task-1"},
	})
	if !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("CallTool error = %v, want ErrPermissionDenied", err)
	}
	if executor.calledToolID != "" {
		t.Fatalf("out-of-responsibility tool executed: %q", executor.calledToolID)
	}
	if len(audit.events) != 1 || audit.events[0].Reason != "responsibility_permission_denied" || audit.events[0].RunID != "run-responsibility-1" ||
		audit.events[0].ResponsibilityID != "responsibility-1" || audit.events[0].ResponsibilityPolicyID != "policy-1" || audit.events[0].ResponsibilityPolicyVersion != 4 {
		t.Fatalf("responsibility audit = %+v", audit.events)
	}
}

func TestGatewayRejectsResponsibilityIdentityMismatch(t *testing.T) {
	authorityContext := testGatewayResponsibilityContext()
	claimed := agentcore.CloneResponsibilityContext(authorityContext)
	claimed.PolicyVersion++
	gateway := NewGateway(GatewayOptions{
		Registry:                newGatewayTestRegistry(t),
		ResponsibilityAuthority: &fakeResponsibilityAuthority{context: authorityContext},
	})

	_, err := gateway.ListToolSet(context.Background(), ListToolsRequest{
		RunID: "run-responsibility-1", ResponsibilityContext: claimed,
	})
	if !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("ListToolSet error = %v, want ErrPermissionDenied", err)
	}
}

func TestGatewayRejectsResponsibilityRuntimeIdentityMismatch(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*CallToolRequest)
	}{
		{name: "missing tenant", mutate: func(req *CallToolRequest) { req.TenantID = "" }},
		{name: "tenant", mutate: func(req *CallToolRequest) { req.TenantID = "tenant-other" }},
		{name: "missing agent", mutate: func(req *CallToolRequest) { req.AgentID = "" }},
		{name: "agent", mutate: func(req *CallToolRequest) { req.AgentID = "agent-other" }},
		{name: "missing principal", mutate: func(req *CallToolRequest) { req.PrincipalID = "" }},
		{name: "principal", mutate: func(req *CallToolRequest) { req.PrincipalID = "principal-other" }},
		{name: "profile", mutate: func(req *CallToolRequest) { req.ProfileID = "profile-other" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			registry := newGatewayTestRegistry(t)
			gateway := NewGateway(GatewayOptions{
				Registry: registry,
				Router:   NewToolRouter(ToolRouterOptions{Registry: registry}),
				ResponsibilityAuthority: &fakeResponsibilityAuthority{
					context: testGatewayResponsibilityContext(),
				},
			})
			req := CallToolRequest{
				TenantID: "tenant-1", RunID: "run-responsibility-1", PrincipalID: "scheduler-agent", AgentID: "scheduler-agent", ProfileID: "scheduler-agent",
				ToolID: string(toolstd.ScheduledTaskList),
			}
			tc.mutate(&req)

			_, err := gateway.CallTool(context.Background(), req)
			if !errors.Is(err, ErrPermissionDenied) {
				t.Fatalf("CallTool error = %v, want ErrPermissionDenied", err)
			}
		})
	}
}

func TestGatewayListToolSetRequiresAgentProfileBindingForAgentRequests(t *testing.T) {
	ctx := context.Background()
	registry := newGatewayTestRegistry(t)
	registerGatewayTestExternalMCP(t, ctx, registry)
	gateway := NewGateway(GatewayOptions{Registry: registry})

	result, err := gateway.ListToolSet(ctx, ListToolsRequest{AgentID: "lead-agent-runtime", Mode: "all"})
	if err != nil {
		t.Fatalf("ListToolSet: %v", err)
	}
	if result.TotalAvailable != 0 || len(result.Tools) != 0 {
		t.Fatalf("agent request without profile binding should not see tools: %+v", result)
	}
}

func TestGatewayAllowsDiaryMaintenanceOnlyWithInternalMaintenanceFlag(t *testing.T) {
	ctx := context.Background()
	profiles := fakeGatewayProfileStore{
		profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{
			"agent-1": {ID: "agent-1", ToolRefs: []agentcore.ToolRef{{ToolID: toolstd.SystemMCPServer}}},
		},
		page: agentcore.AgentDirectoryPage{Entries: []agentcore.AgentDirectoryEntry{{AgentProfileID: "agent-1", AgentInstanceID: "runtime-1"}}},
	}
	diary := &recordingMemoryDiaryToolExecutor{result: MemoryDiaryToolResult{Output: map[string]any{"status": "written"}}}
	router := NewToolRouter(ToolRouterOptions{Registry: newGatewayTestRegistry(t), MemoryDiaryExecutor: diary})
	gateway := NewGateway(GatewayOptions{Registry: newGatewayTestRegistry(t), Router: router, Profiles: profiles})
	req := CallToolRequest{
		TenantID: "tenant-a", ToolID: string(toolstd.MemoryDiaryAppend), PrincipalID: "runtime-1", AgentID: "runtime-1",
		Permissions: []string{toolstd.PermissionMemoryDiaryWrite}, Arguments: map[string]any{"path": "memory/2026-07-17.md"},
	}
	if _, err := gateway.CallTool(ctx, req); !errors.Is(err, ErrToolNotFound) {
		t.Fatalf("ordinary diary maintenance error = %v, want ErrToolNotFound", err)
	}
	req.Maintenance = true
	if _, err := gateway.CallTool(ctx, req); err != nil {
		t.Fatalf("maintenance CallTool: %v", err)
	}
	if diary.request.TenantID != "tenant-a" {
		t.Fatalf("tenant was not propagated: %+v", diary.request)
	}
	req.PrincipalID = "agent-2"
	if _, err := gateway.CallTool(ctx, req); !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("cross-Agent maintenance error = %v, want ErrPermissionDenied", err)
	}
}

func TestGatewayListToolSetDoesNotUseRawAgentIDWhenDirectoryMisses(t *testing.T) {
	ctx := context.Background()
	registry := newGatewayTestRegistry(t)
	registerGatewayTestExternalMCP(t, ctx, registry)
	profiles := fakeGatewayProfileStore{
		profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{
			"superadmin": {
				ID:       "superadmin",
				ToolRefs: []agentcore.ToolRef{{ToolID: "mcp.github"}},
			},
		},
		page: agentcore.AgentDirectoryPage{},
	}
	gateway := NewGateway(GatewayOptions{Registry: registry, Profiles: profiles})

	result, err := gateway.ListToolSet(ctx, ListToolsRequest{AgentID: "superadmin", Mode: "all"})
	if err != nil {
		t.Fatalf("ListToolSet: %v", err)
	}
	if result.TotalAvailable != 0 || len(result.Tools) != 0 {
		t.Fatalf("directory miss should not fall back to raw agent id: %+v", result)
	}
}

func TestGatewayListToolSetIgnoresAgentRequestedExtraToolIDs(t *testing.T) {
	ctx := context.Background()
	registry := newGatewayTestRegistry(t)
	registerGatewayTestExternalMCP(t, ctx, registry)
	profiles := fakeGatewayProfileStore{
		profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{
			"lead-agent": {
				ID:       "lead-agent",
				ToolRefs: []agentcore.ToolRef{{ToolID: agentcore.ToolID(toolstd.ScheduledTaskList)}},
			},
		},
		page: agentcore.AgentDirectoryPage{Entries: []agentcore.AgentDirectoryEntry{{
			AgentProfileID:  "lead-agent",
			AgentInstanceID: "lead-agent-runtime",
		}}},
	}
	gateway := NewGateway(GatewayOptions{Registry: registry, Profiles: profiles})

	result, err := gateway.ListToolSet(ctx, ListToolsRequest{
		AgentID: "lead-agent-runtime",
		ToolIDs: []string{
			string(toolstd.ScheduledTaskRunNow),
			"mcp.github",
		},
		Mode: "all",
	})
	if err != nil {
		t.Fatalf("ListToolSet: %v", err)
	}

	for _, item := range result.Tools {
		if item.ToolID != string(toolstd.ScheduledTaskList) && !isSkillToolID(item.ToolID) {
			t.Fatalf("request-specified unbound tool leaked into agent tool list: %+v", item)
		}
	}
}

func TestGatewayListToolSetAlwaysIncludesSkillLoaderToolsForAgents(t *testing.T) {
	ctx := context.Background()
	registry := newGatewayTestRegistry(t)
	profiles := fakeGatewayProfileStore{
		profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{
			"writer-agent": {
				ID:        "writer-agent",
				ToolRefs:  []agentcore.ToolRef{},
				SkillRefs: []agentcore.SkillRef{{SkillID: "skill.release-notes"}},
			},
		},
		page: agentcore.AgentDirectoryPage{Entries: []agentcore.AgentDirectoryEntry{{
			AgentProfileID:  "writer-agent",
			AgentInstanceID: "writer-runtime",
		}}},
	}
	gateway := NewGateway(GatewayOptions{Registry: registry, Profiles: profiles})

	result, err := gateway.ListToolSet(ctx, ListToolsRequest{AgentID: "writer-runtime", Mode: "all"})
	if err != nil {
		t.Fatalf("ListToolSet: %v", err)
	}

	byID := map[string]ToolDescriptor{}
	for _, item := range result.Tools {
		byID[item.ToolID] = item
	}
	for _, id := range []string{string(toolstd.SkillList), string(toolstd.SkillLoad), string(toolstd.SkillResourceList), string(toolstd.SkillResourceLoad)} {
		if byID[id].Source != ToolSourceInternal {
			t.Fatalf("agent tool set missing skill loader %s: %+v", id, result.Tools)
		}
	}
	if _, ok := byID[string(toolstd.ScheduledTaskList)]; ok {
		t.Fatalf("unbound scheduler tool should not leak into skill-only agent tool set: %+v", result.Tools)
	}
}

func TestGatewayListToolSetExpandsSystemMCPBindingToInternalTools(t *testing.T) {
	ctx := context.Background()
	registry := newGatewayTestRegistry(t)
	if err := registry.Register(ctx, toolstd.SystemMCPServerDefinition(time.Date(2026, 6, 8, 10, 0, 0, 0, time.UTC))); err != nil {
		t.Fatalf("register system mcp: %v", err)
	}
	profiles := fakeGatewayProfileStore{
		profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{
			"lead-agent": {
				ID:       "lead-agent",
				ToolRefs: []agentcore.ToolRef{{ToolID: toolstd.SystemMCPServer}},
			},
		},
		page: agentcore.AgentDirectoryPage{Entries: []agentcore.AgentDirectoryEntry{{
			AgentProfileID:  "lead-agent",
			AgentInstanceID: "lead-agent-runtime",
		}}},
	}
	gateway := NewGateway(GatewayOptions{Registry: registry, Profiles: profiles})

	result, err := gateway.ListToolSet(ctx, ListToolsRequest{AgentID: "lead-agent-runtime", Mode: "all"})
	if err != nil {
		t.Fatalf("ListToolSet: %v", err)
	}

	byID := map[string]ToolDescriptor{}
	for _, item := range result.Tools {
		byID[item.ToolID] = item
		if item.ServerToolID == string(toolstd.SystemMCPServer) {
			t.Fatalf("system mcp binding should expose internal tools, not server-prefixed tools: %+v", item)
		}
	}
	if byID[string(toolstd.MissionPrepare)].Source != ToolSourceInternal ||
		byID[string(toolstd.ScheduledTaskCreate)].Source != ToolSourceInternal {
		t.Fatalf("system mcp binding did not expose internal tools: %+v", result.Tools)
	}
	if _, ok := byID[string(toolstd.PersonalAssistantShellExec)]; ok {
		t.Fatalf("personal assistant shell must require explicit tool binding, not broad mcp.system binding: %+v", byID[string(toolstd.PersonalAssistantShellExec)])
	}
	if _, ok := byID[string(toolstd.EmailSend)]; ok {
		t.Fatalf("email send must require explicit tool binding, not broad mcp.system binding: %+v", byID[string(toolstd.EmailSend)])
	}
	for _, id := range []agentcore.ToolID{toolstd.PersonalBrowserRead, toolstd.PersonalBrowserControl} {
		if _, ok := byID[string(id)]; ok {
			t.Fatalf("personal browser requires explicit owner resource binding: %s", id)
		}
	}
	if _, ok := byID["mcp.system."+string(toolstd.MissionPrepare)]; ok {
		t.Fatalf("system mcp duplicate leaked: %+v", byID)
	}
}

func TestGatewayCallToolRequiresExplicitPersonalAssistantShellBinding(t *testing.T) {
	ctx := context.Background()
	registry := newGatewayTestRegistry(t)
	if err := registry.Register(ctx, toolstd.SystemMCPServerDefinition(time.Date(2026, 6, 8, 10, 0, 0, 0, time.UTC))); err != nil {
		t.Fatalf("register system mcp: %v", err)
	}
	profiles := fakeGatewayProfileStore{
		profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{
			"broad-agent": {
				ID:       "broad-agent",
				ToolRefs: []agentcore.ToolRef{{ToolID: toolstd.SystemMCPServer}},
			},
			"personal-agent": {
				ID: "personal-agent",
				ToolRefs: []agentcore.ToolRef{{
					ToolID:          toolstd.PersonalAssistantShellExec,
					PermissionScope: toolstd.PermissionPersonalAssistantShell,
				}},
			},
		},
		page: agentcore.AgentDirectoryPage{Entries: []agentcore.AgentDirectoryEntry{
			{AgentProfileID: "broad-agent", AgentInstanceID: "broad-runtime"},
			{AgentProfileID: "personal-agent", AgentInstanceID: "personal-runtime"},
		}},
	}
	shell := &recordingPersonalAssistantShellExecutor{
		result: PersonalAssistantShellResult{Output: map[string]any{"exit_code": 0}},
	}
	gateway := NewGateway(GatewayOptions{
		Registry: registry,
		Profiles: profiles,
		Router: NewToolRouter(ToolRouterOptions{
			Registry:                       registry,
			PersonalAssistantShellExecutor: shell,
		}),
	})

	_, err := gateway.CallTool(ctx, CallToolRequest{
		ToolID:  string(toolstd.PersonalAssistantShellExec),
		AgentID: "broad-runtime",
		Arguments: map[string]any{
			"command": "pwd",
		},
	})
	if err == nil {
		t.Fatalf("broad mcp.system binding should not allow personal shell")
	}

	_, err = gateway.CallTool(ctx, CallToolRequest{
		ToolID:                   string(toolstd.PersonalAssistantShellExec),
		PrincipalID:              "user-1",
		AgentID:                  "personal-runtime",
		Permissions:              []string{"mission.read"},
		PermissionsAuthoritative: true,
		Arguments:                map[string]any{"command": "pwd"},
	})
	if err == nil {
		t.Fatal("technical agent binding elevated an authoritative user permission set")
	}

	_, err = gateway.CallTool(ctx, CallToolRequest{
		ToolID:  string(toolstd.PersonalAssistantShellExec),
		AgentID: "personal-runtime",
		Arguments: map[string]any{
			"command": "pwd",
		},
	})
	if err != nil {
		t.Fatalf("explicit personal shell binding should allow call: %v", err)
	}
	if shell.calledToolID != string(toolstd.PersonalAssistantShellExec) {
		t.Fatalf("shell executor was not called: %+v", shell)
	}

	_, err = gateway.CallTool(ctx, CallToolRequest{
		ToolID:                   string(toolstd.PersonalAssistantShellExec),
		PrincipalID:              "user-1",
		AgentID:                  "personal-runtime",
		Permissions:              []string{toolstd.PermissionPersonalAssistantShell},
		PermissionsAuthoritative: true,
		Arguments:                map[string]any{"command": "pwd"},
	})
	if err != nil {
		t.Fatalf("authorized user should retain explicitly granted shell permission: %v", err)
	}
}

func TestGatewayCallToolRequiresExplicitEmailSendBinding(t *testing.T) {
	ctx := context.Background()
	registry := newGatewayTestRegistry(t)
	if err := registry.Register(ctx, toolstd.SystemMCPServerDefinition(time.Date(2026, 6, 8, 10, 0, 0, 0, time.UTC))); err != nil {
		t.Fatalf("register system mcp: %v", err)
	}
	profiles := fakeGatewayProfileStore{
		profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{
			"broad-agent": {
				ID:       "broad-agent",
				ToolRefs: []agentcore.ToolRef{{ToolID: toolstd.SystemMCPServer}},
			},
			"email-agent": {
				ID: "email-agent",
				ToolRefs: []agentcore.ToolRef{{
					ToolID:          toolstd.EmailSend,
					PermissionScope: toolstd.PermissionEmailSend,
				}},
			},
		},
		page: agentcore.AgentDirectoryPage{Entries: []agentcore.AgentDirectoryEntry{
			{AgentProfileID: "broad-agent", AgentInstanceID: "broad-runtime"},
			{AgentProfileID: "email-agent", AgentInstanceID: "email-runtime"},
		}},
	}
	emailExecutor := &recordingEmailToolExecutor{result: EmailToolResult{Output: map[string]any{"status": "sent"}}}
	gateway := NewGateway(GatewayOptions{
		Registry: registry,
		Profiles: profiles,
		Router: NewToolRouter(ToolRouterOptions{
			Registry:      registry,
			EmailExecutor: emailExecutor,
		}),
	})
	arguments := map[string]any{"to": []any{"user@example.test"}, "subject": "Status", "text": "Body"}

	_, err := gateway.CallTool(ctx, CallToolRequest{
		TenantID: "org-1", ToolID: string(toolstd.EmailSend), AgentID: "broad-runtime", Arguments: arguments,
	})
	if err == nil {
		t.Fatal("broad mcp.system binding should not allow email send")
	}

	_, err = gateway.CallTool(ctx, CallToolRequest{
		TenantID: "org-1", ToolID: string(toolstd.EmailSend), AgentID: "email-runtime", Arguments: arguments,
	})
	if err != nil {
		t.Fatalf("explicit email binding should allow call: %v", err)
	}
	if emailExecutor.calledToolID != string(toolstd.EmailSend) {
		t.Fatalf("email executor was not called: %+v", emailExecutor)
	}
}

func TestGatewayListToolSetReturnsAllWhenConfiguredToolCountBelowThreshold(t *testing.T) {
	ctx := context.Background()
	registry := toolres.NewMemoryRegistry()
	for i := 0; i < 9; i++ {
		registerGatewaySearchTool(t, ctx, registry, "tool.small."+string(rune('a'+i)), "Small tool", "Generic helper")
	}
	gateway := NewGateway(GatewayOptions{Registry: registry})

	result, err := gateway.ListToolSet(ctx, ListToolsRequest{Mode: "filter", Query: "does not match anything", TopK: 3})
	if err != nil {
		t.Fatalf("ListToolSet: %v", err)
	}

	if result.Filtered || result.Truncated || result.TotalAvailable != 9 || len(result.Tools) != 9 {
		t.Fatalf("small configured tool set should bypass BM25: %+v", result)
	}
}

func TestGatewayListToolSetUsesBM25TopKAndWarnsWhenNotAllToolsReturned(t *testing.T) {
	ctx := context.Background()
	registry := toolres.NewMemoryRegistry()
	for _, item := range []struct {
		id          string
		name        string
		description string
	}{
		{"tool.github.search", "GitHub issue search", "Search GitHub issues and pull requests"},
		{"tool.github.create", "GitHub issue create", "Create a GitHub issue with labels"},
		{"tool.linear.search", "Linear issue search", "Search Linear issues by project"},
		{"tool.docs.search", "Docs search", "Find internal documentation"},
		{"tool.calendar.read", "Calendar read", "Read calendar events"},
		{"tool.email.send", "Email send", "Send a short email"},
		{"tool.browser.open", "Browser open", "Open a web page"},
		{"tool.file.read", "File read", "Read a workspace file"},
		{"tool.file.write", "File write", "Write a workspace file"},
		{"tool.scheduler.list", "Scheduler list", "List scheduled tasks"},
		{"tool.scheduler.run", "Scheduler run", "Run scheduled task now"},
	} {
		registerGatewaySearchTool(t, ctx, registry, item.id, item.name, item.description)
	}
	gateway := NewGateway(GatewayOptions{Registry: registry})

	result, err := gateway.ListToolSet(ctx, ListToolsRequest{Mode: "filter", Query: "github issue", TopK: 2})
	if err != nil {
		t.Fatalf("ListToolSet: %v", err)
	}

	if !result.Filtered || !result.Truncated || result.TotalAvailable != 11 || len(result.Tools) != 2 {
		t.Fatalf("BM25 result metadata = %+v", result)
	}
	if result.TopK != 2 || !strings.Contains(result.Notice, "not all") || !strings.Contains(result.Notice, "mode=all") {
		t.Fatalf("BM25 result notice/top_k = %+v", result)
	}
	for _, item := range result.Tools {
		if !strings.Contains(item.ToolID, "github") {
			t.Fatalf("BM25 should rank GitHub tools first, got %+v", result.Tools)
		}
	}
}

func TestGatewayListToolSetBM25UsesToolDescriptions(t *testing.T) {
	ctx := context.Background()
	registry := toolres.NewMemoryRegistry()
	for _, item := range []struct {
		id          string
		name        string
		description string
	}{
		{"tool.alpha", "Alpha", "Create deployment release notes and changelog entries"},
		{"tool.beta", "Beta", "Search customer feedback records"},
		{"tool.gamma", "Gamma", "Read calendar events"},
		{"tool.delta", "Delta", "Update project timeline"},
		{"tool.epsilon", "Epsilon", "Fetch repository status"},
		{"tool.zeta", "Zeta", "Open browser pages"},
		{"tool.eta", "Eta", "Write workspace files"},
		{"tool.theta", "Theta", "List scheduled tasks"},
		{"tool.iota", "Iota", "Send status email"},
		{"tool.kappa", "Kappa", "Inspect runtime health"},
	} {
		registerGatewaySearchTool(t, ctx, registry, item.id, item.name, item.description)
	}
	gateway := NewGateway(GatewayOptions{Registry: registry})

	result, err := gateway.ListToolSet(ctx, ListToolsRequest{Mode: "filter", Query: "release changelog", TopK: 1})
	if err != nil {
		t.Fatalf("ListToolSet: %v", err)
	}

	if len(result.Tools) != 1 || result.Tools[0].ToolID != "tool.alpha" {
		t.Fatalf("BM25 should match description text, got %+v", result.Tools)
	}
}

func TestGatewayListToolSetNoBM25MatchesSuggestsAllMode(t *testing.T) {
	ctx := context.Background()
	registry := toolres.NewMemoryRegistry()
	for i := 0; i < 10; i++ {
		registerGatewaySearchTool(t, ctx, registry, "tool.cache."+string(rune('a'+i)), "Cache helper", "Cache maintenance helper")
	}
	gateway := NewGateway(GatewayOptions{Registry: registry})

	result, err := gateway.ListToolSet(ctx, ListToolsRequest{Mode: "filter", Query: "quantum payroll"})
	if err != nil {
		t.Fatalf("ListToolSet: %v", err)
	}

	if !result.Filtered || !result.Truncated || result.TotalAvailable != 10 || len(result.Tools) != 0 {
		t.Fatalf("no-match BM25 result = %+v", result)
	}
	if result.TopK != 10 || !strings.Contains(result.Notice, "No tools matched") || !strings.Contains(result.Notice, "mode=all") {
		t.Fatalf("no-match notice/top_k = %+v", result)
	}
}

func TestGatewayCallsSchedulerToolThroughRouter(t *testing.T) {
	ctx := context.Background()
	registry := newGatewayTestRegistry(t)
	executor := &recordingSchedulerExecutor{
		result: schedulerapp.ToolExecutionResult{Output: map[string]any{"total": 3}},
	}
	gateway := NewGateway(GatewayOptions{
		Registry: registry,
		Router: NewToolRouter(ToolRouterOptions{
			Registry:          registry,
			SchedulerExecutor: executor,
		}),
	})

	result, err := gateway.CallTool(ctx, CallToolRequest{
		ToolID:      string(toolstd.ScheduledTaskList),
		PrincipalID: "agent-ops",
		Permissions: []string{toolstd.PermissionSchedulerRead},
		Arguments:   map[string]any{"limit": 10},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if result.Output["total"] != 3 {
		t.Fatalf("result = %+v", result.Output)
	}
	if executor.calledToolID != string(toolstd.ScheduledTaskList) {
		t.Fatalf("executor tool = %q", executor.calledToolID)
	}
}

func TestGatewayCallToolUsesAgentProfilePermissionScope(t *testing.T) {
	ctx := context.Background()
	registry := newGatewayTestRegistry(t)
	executor := &recordingSchedulerExecutor{
		result: schedulerapp.ToolExecutionResult{Output: map[string]any{"scheduled_task_id": "task-1"}},
	}
	profiles := fakeGatewayProfileStore{
		profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{
			"scheduler-agent": {
				ID: "scheduler-agent",
				ToolRefs: []agentcore.ToolRef{{
					ToolID:          toolstd.ScheduledTaskRunNow,
					PermissionScope: toolstd.PermissionSchedulerRun,
				}},
			},
		},
		page: agentcore.AgentDirectoryPage{Entries: []agentcore.AgentDirectoryEntry{{
			AgentProfileID:  "scheduler-agent",
			AgentInstanceID: "scheduler-runtime",
		}}},
	}
	gateway := NewGateway(GatewayOptions{
		Registry: registry,
		Profiles: profiles,
		Router: NewToolRouter(ToolRouterOptions{
			Registry:          registry,
			SchedulerExecutor: executor,
		}),
	})

	_, err := gateway.CallTool(ctx, CallToolRequest{
		ToolID:  string(toolstd.ScheduledTaskRunNow),
		AgentID: "scheduler-runtime",
		Arguments: map[string]any{
			"scheduled_task_id": "task-1",
		},
	})
	if err != nil {
		t.Fatalf("CallTool should use profile-bound permission: %v", err)
	}
	if executor.calledToolID != string(toolstd.ScheduledTaskRunNow) {
		t.Fatalf("executor tool = %q", executor.calledToolID)
	}
}

func TestGatewayCallToolResolvesPersonalAssistantProfileByAgentID(t *testing.T) {
	ctx := context.Background()
	registry := newGatewayTestRegistry(t)
	knowledge := &recordingKnowledgeToolExecutor{
		result: KnowledgeToolResult{Output: map[string]any{"results": []any{map[string]any{"document_id": "doc-personal"}}}},
	}
	profiles := newPersonalAssistantGatewayProfileStore()
	gateway := NewGateway(GatewayOptions{
		Registry: registry,
		Profiles: profiles,
		Router: NewToolRouter(ToolRouterOptions{
			Registry:          registry,
			KnowledgeExecutor: knowledge,
		}),
	})

	result, err := gateway.CallTool(ctx, CallToolRequest{
		TenantID:                 "minvia",
		ToolID:                   string(toolstd.KnowledgeSearch),
		PrincipalID:              "H000001",
		PrincipalKind:            "user",
		AgentID:                  "A000017",
		Permissions:              []string{toolstd.PermissionKnowledgeRead},
		PermissionsAuthoritative: true,
		Arguments: map[string]any{
			"query":          "Bain AI组织与运营模型 20260528",
			"retrieval_mode": "fast",
			"limit":          5,
		},
	})
	if err != nil {
		t.Fatalf("CallTool should resolve the personal assistant profile: %v", err)
	}
	if !profiles.query.IncludePersonalAssistants {
		t.Fatal("gateway profile lookup must include personal assistants")
	}
	if knowledge.calledToolID != string(toolstd.KnowledgeSearch) || knowledge.request.AgentID != "A000017" || knowledge.request.PrincipalID != "H000001" {
		t.Fatalf("knowledge executor request = tool:%q req:%+v", knowledge.calledToolID, knowledge.request)
	}
	results, ok := result.Output["results"].([]any)
	if !ok || len(results) != 1 {
		t.Fatalf("result = %+v", result.Output)
	}
}

func TestGatewayCallToolRejectsNonOwnerPersonalAssistantPrincipal(t *testing.T) {
	ctx := context.Background()
	registry := newGatewayTestRegistry(t)
	memory := &recordingMemorySearchToolExecutor{
		result: MemorySearchToolResult{Output: map[string]any{"personal_digest": map[string]any{"summary": "private"}}},
	}
	profiles := newPersonalAssistantGatewayProfileStore()
	profiles.profile.ToolRefs = append(profiles.profile.ToolRefs, agentcore.ToolRef{
		ToolID: toolstd.MemorySearch, PermissionScope: toolstd.PermissionMemoryRead,
	})
	gateway := NewGateway(GatewayOptions{
		Registry: registry,
		Profiles: profiles,
		Router: NewToolRouter(ToolRouterOptions{
			Registry:             registry,
			MemorySearchExecutor: memory,
		}),
	})

	tests := []struct {
		name          string
		tenantID      string
		principalID   string
		principalKind string
	}{
		{name: "different owner", tenantID: "minvia", principalID: "H000004", principalKind: "user"},
		{name: "different tenant", tenantID: "other-tenant", principalID: "H000001", principalKind: "user"},
		{name: "agent principal", tenantID: "minvia", principalID: "A000017", principalKind: "agent"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			memory.request = MemorySearchToolRequest{}
			_, err := gateway.CallTool(ctx, CallToolRequest{
				TenantID:                 test.tenantID,
				ToolID:                   string(toolstd.MemorySearch),
				PrincipalID:              test.principalID,
				PrincipalKind:            test.principalKind,
				AgentID:                  "A000017",
				Permissions:              []string{toolstd.PermissionMemoryRead},
				PermissionsAuthoritative: true,
				Arguments:                map[string]any{"query": "private decisions"},
			})
			if !errors.Is(err, ErrPermissionDenied) {
				t.Fatalf("CallTool error = %v, want ErrPermissionDenied", err)
			}
			if memory.request.Query != "" {
				t.Fatalf("unauthorized memory search was executed: %+v", memory.request)
			}
		})
	}
}

func TestGatewayCallToolRejectsUnconfiguredToolEvenWithPermission(t *testing.T) {
	ctx := context.Background()
	registry := newGatewayTestRegistry(t)
	executor := &recordingSchedulerExecutor{}
	profiles := fakeGatewayProfileStore{
		profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{
			"scheduler-agent": {
				ID: "scheduler-agent",
				ToolRefs: []agentcore.ToolRef{{
					ToolID:          toolstd.ScheduledTaskList,
					PermissionScope: toolstd.PermissionSchedulerRead,
				}},
			},
		},
		page: agentcore.AgentDirectoryPage{Entries: []agentcore.AgentDirectoryEntry{{
			AgentProfileID:  "scheduler-agent",
			AgentInstanceID: "scheduler-runtime",
		}}},
	}
	gateway := NewGateway(GatewayOptions{
		Registry: registry,
		Profiles: profiles,
		Router: NewToolRouter(ToolRouterOptions{
			Registry:          registry,
			SchedulerExecutor: executor,
		}),
	})

	_, err := gateway.CallTool(ctx, CallToolRequest{
		ToolID:      string(toolstd.ScheduledTaskRunNow),
		AgentID:     "scheduler-runtime",
		Permissions: []string{toolstd.PermissionSchedulerRun},
		Arguments: map[string]any{
			"scheduled_task_id": "task-1",
		},
	})
	if !errors.Is(err, ErrToolNotFound) {
		t.Fatalf("CallTool error = %v, want ErrToolNotFound", err)
	}
	if executor.calledToolID != "" {
		t.Fatalf("unconfigured tool should not be executed, got %q", executor.calledToolID)
	}
}

func TestGatewayCallToolRequiresAgentProfileBindingForAgentCalls(t *testing.T) {
	ctx := context.Background()
	registry := newGatewayTestRegistry(t)
	executor := &recordingSchedulerExecutor{}
	gateway := NewGateway(GatewayOptions{
		Registry: registry,
		Router: NewToolRouter(ToolRouterOptions{
			Registry:          registry,
			SchedulerExecutor: executor,
		}),
	})

	_, err := gateway.CallTool(ctx, CallToolRequest{
		ToolID:      string(toolstd.ScheduledTaskRunNow),
		AgentID:     "scheduler-runtime",
		Permissions: []string{toolstd.PermissionSchedulerRun},
		Arguments: map[string]any{
			"scheduled_task_id": "task-1",
		},
	})
	if !errors.Is(err, ErrToolNotFound) {
		t.Fatalf("CallTool error = %v, want ErrToolNotFound", err)
	}
	if executor.calledToolID != "" {
		t.Fatalf("agent call without profile-bound tool should not execute, got %q", executor.calledToolID)
	}
}

func TestGatewayCallToolInjectsPerAgentMCPAuthorization(t *testing.T) {
	ctx := context.Background()
	registry := newGatewayTestRegistry(t)
	if err := registry.Register(ctx, toolres.ToolDefinition{
		ID:          agentcore.ToolID("mcp.private"),
		Kind:        toolres.KindMCPServer,
		Name:        "Private MCP",
		Description: "Access a private MCP server.",
		Version:     "1.0.0",
		Enabled:     true,
		RiskLevel:   toolres.RiskMedium,
		Permissions: []toolres.PermissionDeclaration{{
			Scope:    "private.use",
			Risk:     toolres.RiskMedium,
			Required: true,
		}},
		MCP: &toolres.MCPServerDefinition{
			ServerName:   "private",
			Transport:    toolres.TransportStreamHTTP,
			URL:          "http://127.0.0.1:5000/api/mcp",
			ExposedTools: []string{"search"},
			Auth: toolres.MCPAuthPolicy{
				Required:   true,
				Mode:       toolres.MCPAuthModePerAgent,
				HeaderName: "Authorization",
			},
		},
	}); err != nil {
		t.Fatalf("register external mcp: %v", err)
	}
	profiles := fakeGatewayProfileStore{
		profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{
			"private-agent": {
				ID: "private-agent",
				ToolRefs: []agentcore.ToolRef{{
					ToolID:          "mcp.private",
					PermissionScope: "private.use",
				}},
				MCPAuthorizations: []agentcore.MCPAuthorization{{
					ServerToolID:    "mcp.private",
					HeaderName:      "Authorization",
					CredentialValue: "Bearer agent-token",
				}},
			},
		},
		page: agentcore.AgentDirectoryPage{Entries: []agentcore.AgentDirectoryEntry{{
			AgentProfileID:  "private-agent",
			AgentInstanceID: "private-runtime",
		}}},
	}
	external := &recordingExternalMCPClient{
		result: CallToolResult{Output: map[string]any{"ok": true}},
	}
	gateway := NewGateway(GatewayOptions{
		Registry: registry,
		Profiles: profiles,
		Router: NewToolRouter(ToolRouterOptions{
			Registry:    registry,
			ExternalMCP: external,
		}),
	})

	_, err := gateway.CallTool(ctx, CallToolRequest{
		ToolID:  "mcp.private.search",
		AgentID: "private-runtime",
	})
	if err != nil {
		t.Fatalf("CallTool should use profile-bound mcp authorization: %v", err)
	}
	if got := external.headers["Authorization"]; got != "Bearer agent-token" {
		t.Fatalf("external mcp authorization header = %q, want per-agent token; headers=%+v", got, external.headers)
	}

	external.headers = nil
	_, err = gateway.CallTool(ctx, CallToolRequest{
		ToolID:                   "mcp.private.search",
		PrincipalID:              "user-1",
		Permissions:              []string{"private.use"},
		PermissionsAuthoritative: true,
		AgentID:                  "private-runtime",
	})
	if err != nil {
		t.Fatalf("authoritative user call should retain the technical tool binding: %v", err)
	}
	if got := external.headers["Authorization"]; got != "" {
		t.Fatalf("authoritative user call inherited per-agent authorization %q", got)
	}
}

func TestGatewayCallToolAllowsInternalToolThroughSystemMCPBinding(t *testing.T) {
	ctx := context.Background()
	registry := newGatewayTestRegistry(t)
	if err := registry.Register(ctx, toolstd.SystemMCPServerDefinition(time.Date(2026, 6, 8, 10, 0, 0, 0, time.UTC))); err != nil {
		t.Fatalf("register system mcp: %v", err)
	}
	executor := &recordingSchedulerExecutor{
		result: schedulerapp.ToolExecutionResult{Output: map[string]any{"total": 2}},
	}
	profiles := fakeGatewayProfileStore{
		profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{
			"scheduler-agent": {
				ID:       "scheduler-agent",
				ToolRefs: []agentcore.ToolRef{{ToolID: toolstd.SystemMCPServer}},
			},
		},
		page: agentcore.AgentDirectoryPage{Entries: []agentcore.AgentDirectoryEntry{{
			AgentProfileID:  "scheduler-agent",
			AgentInstanceID: "scheduler-runtime",
		}}},
	}
	gateway := NewGateway(GatewayOptions{
		Registry: registry,
		Profiles: profiles,
		Router: NewToolRouter(ToolRouterOptions{
			Registry:          registry,
			SchedulerExecutor: executor,
		}),
	})

	result, err := gateway.CallTool(ctx, CallToolRequest{
		ToolID:    string(toolstd.ScheduledTaskList),
		AgentID:   "scheduler-runtime",
		Arguments: map[string]any{"limit": 5},
	})
	if err != nil {
		t.Fatalf("CallTool should allow internal tool through system MCP binding: %v", err)
	}
	if result.Output["total"] != 2 {
		t.Fatalf("result = %+v", result.Output)
	}
	if executor.calledToolID != string(toolstd.ScheduledTaskList) {
		t.Fatalf("executor tool = %q", executor.calledToolID)
	}
}

func TestGatewayCallToolInjectsProfileSkillBindingsForSkillLoader(t *testing.T) {
	ctx := context.Background()
	registry := newGatewayTestRegistry(t)
	executor := &recordingSkillToolExecutor{
		result: skillres.RuntimeToolResult{Output: map[string]any{"skill_id": "skill.release-notes"}},
	}
	profiles := fakeGatewayProfileStore{
		profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{
			"writer-agent": {
				ID:        "writer-agent",
				SkillRefs: []agentcore.SkillRef{{SkillID: "skill.release-notes", Alias: "release"}},
			},
		},
		page: agentcore.AgentDirectoryPage{Entries: []agentcore.AgentDirectoryEntry{{
			AgentProfileID:  "writer-agent",
			AgentInstanceID: "writer-runtime",
		}}},
	}
	gateway := NewGateway(GatewayOptions{
		Registry: registry,
		Profiles: profiles,
		Router: NewToolRouter(ToolRouterOptions{
			Registry:      registry,
			SkillExecutor: executor,
		}),
	})

	_, err := gateway.CallTool(ctx, CallToolRequest{
		ToolID:    string(toolstd.SkillLoad),
		AgentID:   "writer-runtime",
		Arguments: map[string]any{"skill_id": "skill.release-notes"},
	})
	if err != nil {
		t.Fatalf("CallTool should allow skill loader through implicit binding: %v", err)
	}
	if len(executor.request.SkillBindings) != 1 ||
		executor.request.SkillBindings[0].SkillID != "skill.release-notes" ||
		executor.request.SkillBindings[0].Alias != "release" {
		t.Fatalf("profile skill refs were not forwarded to loader: %+v", executor.request.SkillBindings)
	}
}

func TestToolRouterResolvesExternalMCPToolDetailsCache(t *testing.T) {
	ctx := context.Background()
	registry := newGatewayTestRegistry(t)
	if err := registry.Register(ctx, toolres.ToolDefinition{
		ID:          agentcore.ToolID("mcp.linear"),
		Kind:        toolres.KindMCPServer,
		Name:        "Linear MCP",
		Description: "Access Linear through MCP.",
		Version:     "1.0.0",
		Enabled:     true,
		RiskLevel:   toolres.RiskMedium,
		Permissions: []toolres.PermissionDeclaration{{
			Scope:    "linear.read",
			Risk:     toolres.RiskMedium,
			Required: true,
		}},
		MCP: &toolres.MCPServerDefinition{
			ServerName: "linear",
			Transport:  toolres.TransportStdio,
			Command:    "linear-mcp-server",
			ExposedToolDetails: []toolres.MCPExposedToolDefinition{{
				Name:        "search_issues",
				Description: "Search Linear issues by project or status",
			}},
			HealthCheck: toolres.HealthCheckPolicy{Mode: toolres.HealthCheckStartup},
		},
		CreatedAt: time.Date(2026, 6, 8, 10, 0, 0, 0, time.UTC),
		UpdatedAt: time.Date(2026, 6, 8, 10, 0, 0, 0, time.UTC),
	}); err != nil {
		t.Fatalf("register linear mcp: %v", err)
	}
	external := &recordingExternalMCPClient{
		result: CallToolResult{Output: map[string]any{"issues": []any{"IDE-1"}}},
	}
	router := NewToolRouter(ToolRouterOptions{Registry: registry, ExternalMCP: external})

	_, err := router.CallTool(ctx, CallToolRequest{
		ToolID:      "mcp.linear.search_issues",
		Permissions: []string{"linear.read"},
	})
	if err != nil {
		t.Fatalf("CallTool should resolve detail-only external mcp tool: %v", err)
	}
	if external.serverID != "mcp.linear" || external.toolName != "search_issues" {
		t.Fatalf("external call = server:%q tool:%q", external.serverID, external.toolName)
	}
}

func TestGatewayClosesToolCallLifecycleOnSuccess(t *testing.T) {
	ctx := context.Background()
	registry := newGatewayTestRegistry(t)
	registerGatewayTestExternalMCP(t, ctx, registry)
	external := &recordingExternalMCPClient{result: CallToolResult{Output: map[string]any{"issues": 2}}}
	attempt := &recordingToolCallAttempt{}
	lifecycle := &recordingToolCallLifecycle{attempt: attempt}
	gateway := NewGateway(GatewayOptions{
		Registry: registry,
		Router: NewToolRouter(ToolRouterOptions{
			Registry:    registry,
			ExternalMCP: external,
		}),
		Lifecycle: lifecycle,
	})

	result, err := gateway.CallTool(ctx, CallToolRequest{
		TenantID:    "tenant-a",
		ToolID:      "mcp.github.search_issues",
		PrincipalID: "agent-a",
		Permissions: []string{"github.read"},
		Arguments: map[string]any{
			"query":          "is:open",
			"correlation_id": "corr-1",
		},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if lifecycle.start.ToolID != "mcp.github.search_issues" || lifecycle.start.Source != ToolSourceExternalMCP || lifecycle.start.ServerToolID != "mcp.github" || lifecycle.start.DownstreamToolName != "search_issues" {
		t.Fatalf("lifecycle start = %+v", lifecycle.start)
	}
	if lifecycle.start.CorrelationID != "corr-1" || lifecycle.start.Arguments["query"] != "is:open" {
		t.Fatalf("lifecycle audit metadata = %+v", lifecycle.start)
	}
	if attempt.completed != 1 || attempt.failed != 0 || attempt.result.Output["issues"] != 2 || result.Output["issues"] != 2 {
		t.Fatalf("lifecycle attempt = %+v result = %+v", attempt, result)
	}
}

func TestGatewayClosesToolCallLifecycleOnFailure(t *testing.T) {
	ctx := context.Background()
	registry := newGatewayTestRegistry(t)
	executor := &recordingSchedulerExecutor{err: ErrServiceDependency}
	attempt := &recordingToolCallAttempt{}
	gateway := NewGateway(GatewayOptions{
		Registry: registry,
		Router: NewToolRouter(ToolRouterOptions{
			Registry:          registry,
			SchedulerExecutor: executor,
		}),
		Lifecycle: &recordingToolCallLifecycle{attempt: attempt},
	})

	_, err := gateway.CallTool(ctx, CallToolRequest{
		ToolID:      string(toolstd.ScheduledTaskList),
		PrincipalID: "scheduler",
		Permissions: []string{toolstd.PermissionSchedulerRead},
	})
	if !errors.Is(err, ErrServiceDependency) {
		t.Fatalf("CallTool error = %v, want ErrServiceDependency", err)
	}
	if attempt.failed != 1 || attempt.completed != 0 || attempt.failure.Code != "dependency_unavailable" {
		t.Fatalf("lifecycle attempt = %+v", attempt)
	}
}

func TestGatewayMarksSuccessfulToolWithUnrecordedOutcomeAsUnsafeToRetry(t *testing.T) {
	ctx := context.Background()
	registry := newGatewayTestRegistry(t)
	registerGatewayTestExternalMCP(t, ctx, registry)
	completionErr := errors.New("action ledger unavailable")
	attempt := &recordingToolCallAttempt{completeErr: completionErr}
	gateway := NewGateway(GatewayOptions{
		Registry: registry,
		Router: NewToolRouter(ToolRouterOptions{
			Registry:    registry,
			ExternalMCP: &recordingExternalMCPClient{result: CallToolResult{Output: map[string]any{"issue_id": "issue-1"}}},
		}),
		Lifecycle: &recordingToolCallLifecycle{attempt: attempt},
	})

	result, err := gateway.CallTool(ctx, CallToolRequest{
		ToolID:      "mcp.github.create_issue",
		PrincipalID: "agent-a",
		Permissions: []string{"github.read"},
	})
	if !errors.Is(err, ErrToolCallOutcomeUnrecorded) || !errors.Is(err, completionErr) {
		t.Fatalf("CallTool error = %v, want unrecorded outcome and completion cause", err)
	}
	if result.Output["issue_id"] != "issue-1" || attempt.completed != 1 {
		t.Fatalf("result = %+v attempt = %+v", result, attempt)
	}
}

func TestGatewayDoesNotExecuteWhenToolCallLifecycleCannotStart(t *testing.T) {
	ctx := context.Background()
	registry := newGatewayTestRegistry(t)
	executor := &recordingSchedulerExecutor{}
	startErr := errors.New("action ledger unavailable")
	gateway := NewGateway(GatewayOptions{
		Registry: registry,
		Router: NewToolRouter(ToolRouterOptions{
			Registry:          registry,
			SchedulerExecutor: executor,
		}),
		Lifecycle: &recordingToolCallLifecycle{startErr: startErr},
	})

	_, err := gateway.CallTool(ctx, CallToolRequest{
		ToolID:      string(toolstd.ScheduledTaskList),
		PrincipalID: "scheduler",
		Permissions: []string{toolstd.PermissionSchedulerRead},
	})
	if !errors.Is(err, startErr) {
		t.Fatalf("CallTool error = %v, want lifecycle start error", err)
	}
	if executor.calledToolID != "" {
		t.Fatalf("executor was called after lifecycle start failure: %q", executor.calledToolID)
	}
}

func registerGatewaySearchTool(t *testing.T, ctx context.Context, registry toolres.Registry, id, name, description string) {
	t.Helper()
	if err := registry.Register(ctx, toolres.ToolDefinition{
		ID:          agentcore.ToolID(id),
		Kind:        toolres.KindInternalServiceTool,
		Name:        name,
		Description: description,
		Version:     "1.0.0",
		Enabled:     true,
		RiskLevel:   toolres.RiskLow,
		CreatedAt:   time.Date(2026, 6, 8, 10, 0, 0, 0, time.UTC),
		UpdatedAt:   time.Date(2026, 6, 8, 10, 0, 0, 0, time.UTC),
	}); err != nil {
		t.Fatalf("register %s: %v", id, err)
	}
}

func newGatewayTestRegistry(t *testing.T) *toolres.MemoryRegistry {
	t.Helper()
	registry := toolres.NewMemoryRegistry()
	now := time.Date(2026, 6, 8, 10, 0, 0, 0, time.UTC)
	for _, definition := range toolstd.Definitions(now) {
		if err := registry.Register(context.Background(), definition); err != nil {
			t.Fatalf("register %s: %v", definition.ID, err)
		}
	}
	return registry
}

func registerGatewayTestExternalMCP(t *testing.T, ctx context.Context, registry toolres.Registry) {
	t.Helper()
	err := registry.Register(ctx, toolres.ToolDefinition{
		ID:          agentcore.ToolID("mcp.github"),
		Kind:        toolres.KindMCPServer,
		Name:        "GitHub MCP",
		Description: "Access GitHub through MCP.",
		Version:     "1.0.0",
		Enabled:     true,
		RiskLevel:   toolres.RiskMedium,
		Permissions: []toolres.PermissionDeclaration{{
			Scope:    "github.read",
			Risk:     toolres.RiskMedium,
			Required: true,
		}},
		MCP: &toolres.MCPServerDefinition{
			ServerName:   "github",
			Transport:    toolres.TransportStdio,
			Command:      "github-mcp-server",
			ExposedTools: []string{"search_issues", "create_issue"},
			HealthCheck:  toolres.HealthCheckPolicy{Mode: toolres.HealthCheckStartup},
		},
		CreatedAt: time.Date(2026, 6, 8, 10, 0, 0, 0, time.UTC),
		UpdatedAt: time.Date(2026, 6, 8, 10, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("register external mcp: %v", err)
	}
}

type fakeGatewayProfileStore struct {
	profiles map[agentcore.AgentProfileID]agentcore.AgentProfile
	page     agentcore.AgentDirectoryPage
}

type personalAssistantGatewayProfileStore struct {
	profile agentcore.AgentProfile
	people  map[agentcore.AgentProfileID]agentcore.AgentProfile
	query   agentcore.AgentDirectoryQuery
}

func newPersonalAssistantGatewayProfileStore() *personalAssistantGatewayProfileStore {
	return &personalAssistantGatewayProfileStore{
		profile: agentcore.AgentProfile{
			ID:                        "A000017",
			AgentKind:                 agentcore.AgentKindAI,
			PersonalAssistantOwnerID:  "ding",
			PersonalAssistantTenantID: "minvia",
			ToolRefs: []agentcore.ToolRef{{
				ToolID: toolstd.KnowledgeSearch, PermissionScope: toolstd.PermissionKnowledgeRead,
			}},
		},
		people: map[agentcore.AgentProfileID]agentcore.AgentProfile{
			"H000001": {ID: "H000001", AgentKind: agentcore.AgentKindHuman, Enabled: true, UserAccount: agentcore.UserAccountSpec{Username: "ding"}},
			"H000004": {ID: "H000004", AgentKind: agentcore.AgentKindHuman, Enabled: true, UserAccount: agentcore.UserAccountSpec{Username: "lipi"}},
		},
	}
}

func (s *personalAssistantGatewayProfileStore) GetProfile(_ context.Context, id agentcore.AgentProfileID) (agentcore.AgentProfile, error) {
	if id == s.profile.ID {
		return s.profile, nil
	}
	profile, ok := s.people[id]
	if !ok {
		return agentcore.AgentProfile{}, ErrToolNotFound
	}
	return profile, nil
}

func (s *personalAssistantGatewayProfileStore) QueryDirectory(_ context.Context, query agentcore.AgentDirectoryQuery) (agentcore.AgentDirectoryPage, error) {
	s.query = query
	if !query.IncludePersonalAssistants {
		return agentcore.AgentDirectoryPage{}, nil
	}
	return agentcore.AgentDirectoryPage{Entries: []agentcore.AgentDirectoryEntry{{
		AgentProfileID:  s.profile.ID,
		AgentInstanceID: agentcore.AgentInstanceID(s.profile.ID),
	}}}, nil
}

type fakeResponsibilityAuthority struct {
	runID   string
	context *agentcore.ResponsibilityContext
	err     error
}

func (a *fakeResponsibilityAuthority) ResolveResponsibilityExecution(_ context.Context, runID string) (*ResponsibilityExecutionAuthority, error) {
	a.runID = runID
	if a.err != nil || a.context == nil {
		return nil, a.err
	}
	profileID := strings.TrimSpace(a.context.AgentProfileID)
	return &ResponsibilityExecutionAuthority{
		RunID: runID, TenantID: "tenant-1", AgentID: profileID, AgentProfileID: profileID,
		Context: agentcore.CloneResponsibilityContext(a.context),
	}, nil
}

func testGatewayResponsibilityContext() *agentcore.ResponsibilityContext {
	return &agentcore.ResponsibilityContext{
		ContextID: "context-1", ResponsibilityID: "responsibility-1", ResponsibilityVersion: 2,
		PolicyID: "policy-1", PolicyVersion: 4, AgentProfileID: "scheduler-agent",
		AllowedTools: []string{string(toolstd.ScheduledTaskList)}, PolicyToolScopes: []string{toolstd.PermissionSchedulerRead}, TaskPermissionScopes: []string{toolstd.PermissionSchedulerRead},
	}
}

type recordingToolCallLifecycle struct {
	start    ToolCallLifecycleStart
	attempt  ToolCallAttempt
	startErr error
}

func (l *recordingToolCallLifecycle) StartToolCall(_ context.Context, start ToolCallLifecycleStart) (ToolCallAttempt, error) {
	l.start = start
	return l.attempt, l.startErr
}

type recordingToolCallAttempt struct {
	completed   int
	failed      int
	result      CallToolResult
	failure     ToolCallFailure
	completeErr error
	failErr     error
}

func (a *recordingToolCallAttempt) CompleteToolCall(_ context.Context, result CallToolResult) error {
	a.completed++
	a.result = result
	return a.completeErr
}

func (a *recordingToolCallAttempt) FailToolCall(_ context.Context, failure ToolCallFailure) error {
	a.failed++
	a.failure = failure
	return a.failErr
}

func (s fakeGatewayProfileStore) GetProfile(ctx context.Context, id agentcore.AgentProfileID) (agentcore.AgentProfile, error) {
	profile, ok := s.profiles[id]
	if !ok {
		return agentcore.AgentProfile{}, ErrToolNotFound
	}
	return profile, nil
}

func (s fakeGatewayProfileStore) QueryDirectory(ctx context.Context, q agentcore.AgentDirectoryQuery) (agentcore.AgentDirectoryPage, error) {
	return s.page, nil
}
