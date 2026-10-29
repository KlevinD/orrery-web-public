package core

import "testing"

func TestPersonalAssistantBoundToolGrantsPreservePrincipalAndTaskFences(t *testing.T) {
	profile := AgentProfile{ID: "assistant-grants", PersonalAssistantOwnerID: "owner", PersonalAssistantTenantID: "tenant", ToolRefs: []ToolRef{
		{ToolID: "mcp.browser.navigate", PermissionScope: "browser.server.read"},
		{ToolID: "rsi_learning_plan", PermissionScope: "rsi.learning.read"},
		{ToolID: "personal_browser_read", PermissionScope: "browser.personal.read"},
		{ToolID: "personal_browser_control", PermissionScope: "browser.personal.control"},
		{ToolID: "email_send", PermissionScope: "email.send"},
	}}
	input := AgentInput{RunID: "run-grants", TenantID: "tenant", ExecutionPrincipal: ExecutionPrincipal{Kind: ExecutionPrincipalUser, ID: "human-owner", TenantID: "tenant", Source: "system_assistant", PermissionScope: []string{"tool.call"}}, RuntimeToolBindings: []RuntimeToolBinding{
		{ToolID: "mcp.browser.navigate", Source: "mcp_server", ServerToolID: "mcp.browser", DownstreamToolName: "navigate", GrantedPermissions: []string{"browser.server.read"}},
		{ToolID: "rsi_learning_plan", GrantedPermissions: []string{"rsi.learning.read"}},
		{ToolID: "personal_browser_read", GrantedPermissions: []string{"browser.personal.read"}},
		{ToolID: "personal_browser_control", GrantedPermissions: []string{"browser.personal.control"}},
		{ToolID: "email_send", GrantedPermissions: []string{"email.send"}},
	}}
	build := func(profile AgentProfile, input AgentInput) *ExecutionEnvelope {
		return NewBaseAgent(BaseAgentConfig{Identity: AgentIdentity{AgentID: "assistant-grants", AgentProfileID: profile.ID}, Profile: profile}).BuildRuntimeRequest(input).ExecutionEnvelope
	}
	envelope := build(profile, input)
	if !envelope.AllowsTool("personal_browser_read", []string{"browser.personal.read"}) || !envelope.AllowsTool("personal_browser_control", []string{"browser.personal.control"}) || envelope.AllowsTool("personal_browser_read", []string{"browser.personal.control"}) {
		t.Fatal("native browser binding scope mismatch")
	}
	if !envelope.AllowsTool("mcp.browser.navigate", []string{"browser.server.read"}) || !envelope.AllowsTool("rsi_learning_plan", []string{"rsi.learning.read"}) {
		t.Fatalf("bound resources denied: %+v", envelope)
	}
	if len(envelope.PermissionScopes) != 1 || envelope.PermissionScopes[0] != "tool.call" || envelope.PrincipalID != "human-owner" {
		t.Fatal("owner authority changed")
	}
	conversation := input
	conversation.Task = TaskInput{ID: "conversation", MissionID: "discussion-conversation"}
	conversation.ContextPack.TaskID = "conversation"
	conversation.ContextPack.MissionID = "discussion-conversation"
	conversation.ContextPack.DiscussionGroupID = "conversation"
	conversation.RuntimeOptions.ProviderSpecific = map[string]string{"discussion_id": "conversation"}
	if !build(profile, conversation).AllowsTool("mcp.browser.navigate", []string{"browser.server.read"}) {
		t.Fatal("normal conversation resource denied")
	}
	for _, tc := range []struct{ tool, permission string }{{"mcp.browser.other", "browser.server.read"}, {"email_send", "email.send"}, {"rsi_learning_plan", "knowledge.write"}} {
		if envelope.AllowsTool(tc.tool, []string{tc.permission}) {
			t.Fatalf("unrelated grant allowed: %+v", tc)
		}
	}
	for _, tc := range []string{"no-tool-call", "non-personal", "other-tenant", "non-owner-session", "task", "incoming-envelope", "unbound", "mismatched-binding"} {
		t.Run(tc, func(t *testing.T) {
			p, in := profile, input
			switch tc {
			case "no-tool-call":
				in.ExecutionPrincipal.PermissionScope = []string{"discussion.read"}
			case "non-personal":
				p.PersonalAssistantOwnerID = ""
			case "other-tenant":
				in.ExecutionPrincipal.TenantID = "other"
			case "non-owner-session":
				in.ExecutionPrincipal.Source = "task"
			case "task":
				in.Task.MissionID = "mission"
			case "incoming-envelope":
				in.ExecutionEnvelope = CloneExecutionEnvelope(envelope)
				in.ExecutionEnvelope.BoundToolPermissionScopes = nil
				_ = in.ExecutionEnvelope.Seal()
			case "unbound":
				p.ToolRefs = nil
			case "mismatched-binding":
				in.RuntimeToolBindings = []RuntimeToolBinding{{ToolID: "mcp.browser.navigate", Source: "mcp_server", ServerToolID: "other", DownstreamToolName: "navigate", GrantedPermissions: []string{"browser.server.read"}}}
			}
			if e := build(p, in); e.AllowsTool("mcp.browser.navigate", []string{"browser.server.read"}) {
				t.Fatalf("invalid resource handoff accepted: %+v", e)
			}
			if build(p, in).AllowsTool("personal_browser_control", []string{"browser.personal.control"}) {
				t.Fatal("browser authority crossed a session fence")
			}
		})
	}
	for _, tc := range []string{"forbidden-tool", "forbidden-scope", "allowed-tools", "mission", "scheduled", "agent-principal"} {
		t.Run(tc, func(t *testing.T) {
			e := CloneExecutionEnvelope(envelope)
			switch tc {
			case "forbidden-tool":
				e.ForbiddenActions = []string{"mcp.browser"}
			case "forbidden-scope":
				e.ForbiddenActions = []string{"browser.server.read"}
			case "allowed-tools":
				e.AllowedToolIDs = []string{"knowledge_search"}
			case "mission":
				e.MissionID = "mission"
			case "scheduled":
				e.ScheduledRunID = "scheduled"
			case "agent-principal":
				e.PrincipalKind = ExecutionPrincipalAgent
			}
			if err := e.Seal(); err != nil {
				t.Fatal(err)
			}
			if e.AllowsTool("mcp.browser.navigate", []string{"browser.server.read"}) {
				t.Fatal("explicit fence bypassed")
			}
		})
	}
	clone := CloneExecutionEnvelope(envelope)
	clone.BoundToolPermissionScopes["mcp.browser.navigate"][0] = "browser.write"
	if clone.Verify() == nil || envelope.Verify() != nil {
		t.Fatal("grant digest or clone isolation failed")
	}
}
