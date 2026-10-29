package main

import (
	"context"
	"testing"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/discussionsession"
	"orrery_v2/internal/app/identity"
	"orrery_v2/internal/app/userauth"
	gwconfig "orrery_v2/internal/gateway/config"
	gwprotocol "orrery_v2/internal/gateway/protocol"
)

func TestProductionMessageGatewayRunnerRoutesWeixinToAccountPersonalAssistant(t *testing.T) {
	discussions := &fakeProductionGatewayDiscussionService{events: []discussionsession.StreamEventDTO{{Kind: discussionsession.StreamEventRunCompleted}}}
	bindingKey := gwconfig.WeixinBindingKey("tenant-a", "wx-user")
	cfg := gwconfig.DefaultConfig()
	cfg.Adapters.Weixin.Extra = map[string]any{
		"weixinUserBindings":        map[string]any{bindingKey: "human-alice"},
		"personalAssistantBindings": map[string]any{bindingKey: "ding-account"},
		"weixinAccounts": map[string]any{
			bindingKey: map[string]any{"tenantId": "tenant-a", "userId": "wx-user", "botId": "bot-a", "token": "secret"},
		},
	}
	runner := newProductionMessageGatewayRunner(discussions, cfg)
	authorization := &recordingGatewayViewerAuthorization{
		account: userauth.Account{
			UserID: "ding-account", Username: "ding", DisplayName: "Alice", AgentProfileID: "human-alice", Enabled: true,
			PermissionScope: []string{"discussion.write"},
		},
		resolved: []string{"discussion.write", "knowledge.read"},
	}
	runner.SetViewerAuthorization(authorization, authorization)
	runner.SetAgentDirectory(&fakeProductionGatewayAgentDirectory{entries: []agentcore.AgentDirectoryEntry{
		gatewayTenantDirectoryEntry("human-alice", agentcore.AgentKindHuman, "tenant-a"),
	}})
	runner.setConversationID("weixin:wx-user", "conv-process-local")
	input := gwprotocol.SubmitTurnInput{SessionKey: "weixin:wx-user", ChannelKey: gwprotocol.ChannelWeixin, Message: "ping", Telemetry: verifiedWeixinTelemetry("wx-user")}
	input.Telemetry["weixin_account_id"] = "bot-a"
	events, err := runner.RunTurn(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	_ = collectGatewayEvents(events)
	if discussions.viewer.ViewerID != "human-alice" || discussions.viewer.AgentProfileID != "human-alice" || discussions.viewer.OwnerID != "ding-account" || discussions.viewer.TenantID != "tenant-a" {
		t.Fatalf("personal assistant viewer = %+v", discussions.viewer)
	}
	if len(discussions.viewer.PermissionScope) != 2 || discussions.viewer.PermissionScope[1] != "knowledge.read" {
		t.Fatalf("personal assistant permission scope = %+v", discussions.viewer.PermissionScope)
	}
	if authorization.principal.AgentProfileID != "human-alice" || authorization.principal.UserID != "ding-account" || authorization.principal.TenantID != "tenant-a" {
		t.Fatalf("authorization principal = %+v", authorization.principal)
	}
	if len(authorization.accountScopes) != 1 || authorization.accountScopes[0] != "discussion.write" {
		t.Fatalf("authorization account scopes = %+v", authorization.accountScopes)
	}
	if discussions.cmd.ConversationKind != discussionsession.ConversationKindPersonalAssistant || discussions.cmd.ConversationID != "" || discussions.cmd.ForceNewConversation || discussions.cmd.DirectTargetKind != "" || discussions.cmd.DirectTargetID != "" {
		t.Fatalf("personal assistant command = %+v", discussions.cmd)
	}
}

type recordingGatewayViewerAuthorization struct {
	account       userauth.Account
	resolved      []string
	principal     identity.Principal
	accountScopes []string
}

func (r *recordingGatewayViewerAuthorization) GetAccountByAgentProfileID(_ context.Context, agentProfileID string) (userauth.Account, error) {
	if agentProfileID != r.account.AgentProfileID {
		return userauth.Account{}, userauth.ErrAccountNotFound
	}
	return r.account, nil
}

func (r *recordingGatewayViewerAuthorization) ResolvePermissionScopes(_ context.Context, principal identity.Principal, accountScopes []string) ([]string, error) {
	r.principal = principal
	r.accountScopes = append([]string(nil), accountScopes...)
	return append([]string(nil), r.resolved...), nil
}

func TestAuthorizedGatewayViewerSkipsUnboundGatewayIdentity(t *testing.T) {
	runner := newProductionMessageGatewayRunner(&fakeProductionGatewayDiscussionService{}, gwconfig.DefaultConfig())
	authorization := &recordingGatewayViewerAuthorization{
		account: userauth.Account{AgentProfileID: "human-alice", Enabled: true},
	}
	runner.SetViewerAuthorization(authorization, authorization)

	viewer, err := runner.authorizedGatewayViewer(context.Background(), gwprotocol.SubmitTurnInput{
		SessionKey: "website:anonymous",
		ChannelKey: gwprotocol.ChannelWebsite,
	}, productionGatewayRoute{})
	if err != nil {
		t.Fatal(err)
	}
	if viewer.ViewerID == "" || authorization.principal.AgentProfileID != "" {
		t.Fatalf("anonymous gateway viewer = %+v; authorization principal = %+v", viewer, authorization.principal)
	}
}

func TestProductionMessageGatewayRunnerPreservesExplicitWeixinNewPersonalAssistantSession(t *testing.T) {
	discussions := &fakeProductionGatewayDiscussionService{events: []discussionsession.StreamEventDTO{{Kind: discussionsession.StreamEventRunCompleted}}}
	bindingKey := gwconfig.WeixinBindingKey("tenant-a", "wx-user")
	cfg := gwconfig.DefaultConfig()
	cfg.Adapters.Weixin.Extra = map[string]any{
		"weixinUserBindings":        map[string]any{bindingKey: "human-alice"},
		"personalAssistantBindings": map[string]any{bindingKey: "ding-account"},
		"weixinAccounts": map[string]any{
			bindingKey: map[string]any{"tenantId": "tenant-a", "userId": "wx-user", "botId": "bot-a", "token": "secret"},
		},
	}
	runner := newProductionMessageGatewayRunner(discussions, cfg)
	runner.SetAgentDirectory(&fakeProductionGatewayAgentDirectory{entries: []agentcore.AgentDirectoryEntry{
		gatewayTenantDirectoryEntry("human-alice", agentcore.AgentKindHuman, "tenant-a"),
	}})
	input := gwprotocol.SubmitTurnInput{
		SessionKey: "weixin:wx-user:fresh", ChannelKey: gwprotocol.ChannelWeixin, Message: "start fresh",
		Telemetry: verifiedWeixinTelemetry("wx-user"),
	}
	input.Telemetry["weixin_account_id"] = "bot-a"
	input.Telemetry["weixin_new_session"] = "true"
	events, err := runner.RunTurn(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	_ = collectGatewayEvents(events)
	if !discussions.cmd.ForceNewConversation || discussions.cmd.ConversationID != "" || discussions.cmd.ConversationKind != discussionsession.ConversationKindPersonalAssistant {
		t.Fatalf("explicit new personal assistant command = %+v", discussions.cmd)
	}
}
