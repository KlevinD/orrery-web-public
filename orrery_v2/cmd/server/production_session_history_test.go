package main

import (
	"context"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	runtimecore "orrery_v2/internal/agent/runtime/core"
	"orrery_v2/internal/app/discussionsession"
	"orrery_v2/internal/platform/testkit"
	toolres "orrery_v2/internal/resource/tool"
	toolstd "orrery_v2/internal/resource/tool/std"
)

func TestSessionHistoryProductionDispatcherPreservesHostScope(t *testing.T) {
	for _, id := range []agentcore.ToolID{toolstd.SessionSearch, toolstd.SessionRead} {
		gateway := &productionRuntimeTestMCPGateway{}
		dispatcher := productionRuntimeToolDispatcher{mcpGateway: gateway}
		call := runtimecore.RuntimeToolCallRequest{TenantID: "tenant", RunID: "run", AgentID: "physical", Name: "history_alias",
			ProviderSpecific: map[string]string{personalAssistantLogicalAgentIDKey: "logical"},
			ToolBindings:     []agentcore.RuntimeToolBinding{{ToolID: id, Alias: "history_alias", GrantedPermissions: []string{toolstd.PermissionDiscussionRead}}},
			Input:            map[string]any{"query": "keyword"},
		}
		result, err := dispatcher.CallRuntimeTool(context.Background(), call)
		if err != nil || result.Output["ok"] != true || gateway.request.ToolID != string(id) || gateway.request.TenantID != "tenant" || gateway.request.RunID != "run" || gateway.request.AgentID != "physical" || gateway.request.LogicalAgentID != "logical" {
			t.Fatalf("history dispatch: %+v %+v %v", gateway.request, result, err)
		}
		call.ProviderSpecific[agentcore.RuntimeAllowedToolIDsProviderKey] = `["web_search"]`
		result, err = dispatcher.CallRuntimeTool(context.Background(), call)
		if err != nil || result.Output["status"] != "failed" {
			t.Fatal("restricted website/tool allowlist must block history tools")
		}
		call.ProviderSpecific = nil
		call.ToolBindings = nil
		call.Name = string(id)
		result, err = dispatcher.CallRuntimeTool(context.Background(), call)
		if err != nil || result.Output["status"] != "failed" {
			t.Fatal("unbound history tool must not run")
		}
	}
}

func TestSessionHistoryProductionGatewayWiresRealDiscussionService(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	repo := discussionsession.NewRepository(env.DBRouter)
	profiles := agentcore.NewPlatformProfileRepository(env.UnitOfWork)
	seedProductionChatProfile(t, ctx, profiles, "agent", "\u79e6\u987e\u95ee", "History reader")
	profile, err := profiles.GetProfile(ctx, "agent")
	if err != nil {
		t.Fatal(err)
	}
	profile.ToolRefs = toolstd.EnsureSessionHistoryReadToolRefs(profile.ToolRefs)
	if err := profiles.SaveProfile(ctx, profile); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"current", "past"} {
		if err := repo.SaveConversation(ctx, discussionsession.ConversationRecord{ConversationID: id, TenantID: "tenant", CreatedBy: "user", ConversationKind: discussionsession.ConversationKindDirect}); err != nil {
			t.Fatal(err)
		}
		if err := repo.SaveMember(ctx, discussionsession.ConversationMemberRecord{ConversationID: id, MemberID: "agent", MemberKind: "agent", DisplayName: "Agent"}); err != nil {
			t.Fatal(err)
		}
	}
	if err := repo.SaveMessage(ctx, discussionsession.MessageRecord{MessageID: "past-message", ConversationID: "past", Role: "user", Content: "launch code EXAMPLE", CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveRun(ctx, discussionsession.RunRecord{RunID: "run", ConversationID: "current", AgentID: "agent", Status: "running"}); err != nil {
		t.Fatal(err)
	}
	registry := toolres.NewMemoryRegistry()
	for _, definition := range toolstd.Definitions(time.Now()) {
		if err := registry.Register(ctx, definition); err != nil {
			t.Fatal(err)
		}
	}
	gateway := newProductionMCPGateway(productionMCPGatewayOptions{Registry: registry, Profiles: profiles, SessionHistoryExecutor: discussionsession.NewService(discussionsession.ServiceOptions{Repository: repo})})
	dispatcher := productionRuntimeToolDispatcher{mcpGateway: gateway}
	call := runtimecore.RuntimeToolCallRequest{TenantID: "tenant", RunID: "run", AgentID: "agent", Name: "session_search", Input: map[string]any{"query": "EXAMPLE"},
		ToolBindings: []agentcore.RuntimeToolBinding{{ToolID: toolstd.SessionSearch, Alias: "session_search", GrantedPermissions: []string{toolstd.PermissionDiscussionRead}}, {ToolID: toolstd.SessionRead, Alias: "session_read", GrantedPermissions: []string{toolstd.PermissionDiscussionRead}}},
	}
	result, err := dispatcher.CallRuntimeTool(ctx, call)
	if err != nil || result.Output["status"] == "failed" {
		t.Fatalf("real gateway search failed: %+v %v", result, err)
	}
	items := result.Output["items"].([]map[string]any)
	if len(items) != 1 || items[0]["message_id"] != "past-message" {
		t.Fatalf("real history not returned: %+v", result)
	}
	call.Name, call.Input = "session_read", map[string]any{"session_id": items[0]["session_id"], "message_id": items[0]["message_id"]}
	result, err = dispatcher.CallRuntimeTool(ctx, call)
	if err != nil || result.Output["status"] == "failed" || result.Output["items"].([]map[string]any)[0]["content"] != "launch code EXAMPLE" {
		t.Fatalf("real gateway original read failed: %+v %v", result, err)
	}
}
