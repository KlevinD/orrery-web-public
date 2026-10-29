package main

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/discussionsession"
	gwbindings "orrery_v2/internal/gateway/binding"
	"orrery_v2/internal/gateway/channel"
	gwconfig "orrery_v2/internal/gateway/config"
	gwprotocol "orrery_v2/internal/gateway/protocol"
	"orrery_v2/internal/platform"
)

type fakeProductionGatewayDiscussionService struct {
	viewer                  discussionsession.ViewerRef
	cmd                     discussionsession.SendMessageCommand
	cmds                    []discussionsession.SendMessageCommand
	personalApprovalContext string
	operationalFailureFacts string
	events                  []discussionsession.StreamEventDTO
	err                     error
}

func (f *fakeProductionGatewayDiscussionService) SendMessageStream(ctx context.Context, viewer discussionsession.ViewerRef, cmd discussionsession.SendMessageCommand) (<-chan discussionsession.StreamEventDTO, error) {
	f.viewer = viewer
	f.cmd = cmd
	f.cmds = append(f.cmds, cmd)
	f.personalApprovalContext = productionPersonalAssistantApprovalDialogueFromContext(ctx)
	f.operationalFailureFacts = discussionsession.OperationalFailureFacts(ctx)
	if f.err != nil {
		return nil, f.err
	}
	ch := make(chan discussionsession.StreamEventDTO, len(f.events))
	for _, event := range f.events {
		ch <- event
	}
	close(ch)
	return ch, nil
}

func TestProductionMessageGatewayRunnerClassifiesDiscussionFailuresAsRetryableInfrastructure(t *testing.T) {
	tests := []struct {
		name        string
		discussions *fakeProductionGatewayDiscussionService
	}{
		{name: "start failure", discussions: &fakeProductionGatewayDiscussionService{err: fmt.Errorf("database password=secret unavailable")}},
		{name: "stream failure", discussions: &fakeProductionGatewayDiscussionService{events: []discussionsession.StreamEventDTO{{Kind: discussionsession.StreamEventError, Error: "provider access_token=secret"}}}},
		{name: "stream ended without terminal event", discussions: &fakeProductionGatewayDiscussionService{}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			runner := newProductionMessageGatewayRunner(test.discussions)
			runner.SetBindingStore(testProductionGatewayBindingStore(t))
			runner.SetAgentDirectory(systemGatewayTestDirectory("human-a", "agent-a"))
			identity := createProductionGatewayBinding(t, runner.bindings, "feishu", "feishu:app-a", "fs-a", "open-a", "human-a", "agent-a")
			events, err := runner.RunTurn(context.Background(), gwprotocol.SubmitTurnInput{
				SessionKey: "feishu:chat-a", ChannelKey: gwprotocol.ChannelFeishu, Message: "ping", Identity: &identity,
			})
			if err != nil {
				t.Fatalf("RunTurn: %v", err)
			}
			got := collectGatewayEvents(events)
			if len(got) != 1 || got[0].Type != gwprotocol.EventError || got[0].Code != gwprotocol.ErrorCodeGatewayRuntimeUnavailable {
				t.Fatalf("events = %+v", got)
			}
			if strings.Contains(got[0].Message, "password") || strings.Contains(got[0].Message, "access_token") ||
				channel.InboundProcessingError(got) == nil {
				t.Fatalf("runtime failure was exposed or acknowledged: %+v", got)
			}
		})
	}
}

func TestProductionMessageGatewayRunnerDoesNotRetryInvalidRuntimeConfiguration(t *testing.T) {
	discussions := &fakeProductionGatewayDiscussionService{events: []discussionsession.StreamEventDTO{{
		Kind: discussionsession.StreamEventError, Error: "do not expose local runtime details",
		ErrorCode: string(agentcore.RuntimeBridgeWorkspaceUnavailable), Recoverable: false,
	}}}
	runner := newProductionMessageGatewayRunner(discussions)
	runner.SetBindingStore(testProductionGatewayBindingStore(t))
	runner.SetAgentDirectory(systemGatewayTestDirectory("human-a", "agent-a"))
	identity := createProductionGatewayBinding(t, runner.bindings, "feishu", "feishu:app-a", "fs-a", "open-a", "human-a", "agent-a")
	events, err := runner.RunTurn(context.Background(), gwprotocol.SubmitTurnInput{
		SessionKey: "feishu:chat-a", ChannelKey: gwprotocol.ChannelFeishu, Message: "ping", Identity: &identity,
	})
	if err != nil {
		t.Fatalf("RunTurn: %v", err)
	}
	got := collectGatewayEvents(events)
	if len(got) != 1 || got[0].Code != gwprotocol.ErrorCodeGatewayRuntimeInvalid || got[0].Recoverable {
		t.Fatalf("events = %+v", got)
	}
	if strings.Contains(got[0].Message, "local runtime") || !channel.IsPermanentInboundError(channel.InboundProcessingError(got)) {
		t.Fatalf("invalid runtime configuration was exposed or marked retryable: %+v", got)
	}
}

func TestProductionMessageGatewayRunnerDoesNotRetryDiscussionPermissionDenied(t *testing.T) {
	discussions := &fakeProductionGatewayDiscussionService{events: []discussionsession.StreamEventDTO{{
		Kind: discussionsession.StreamEventError, Error: "discussion forbidden: internal identity details",
		ErrorCode: discussionsession.StreamErrorCodePermissionDenied, Recoverable: true,
	}}}
	runner := newProductionMessageGatewayRunner(discussions)
	runner.SetBindingStore(testProductionGatewayBindingStore(t))
	runner.SetAgentDirectory(systemGatewayTestDirectory("human-a", "agent-a"))
	identity := createProductionGatewayBinding(t, runner.bindings, "feishu", "feishu:app-a", "fs-a", "open-a", "human-a", "agent-a")
	events, err := runner.RunTurn(context.Background(), gwprotocol.SubmitTurnInput{
		SessionKey: "feishu:chat-a", ChannelKey: gwprotocol.ChannelFeishu, Message: "ping", Identity: &identity,
	})
	if err != nil {
		t.Fatalf("RunTurn: %v", err)
	}
	got := collectGatewayEvents(events)
	if len(got) != 1 || got[0].Code != gwprotocol.ErrorCodeGatewayPermissionDenied || got[0].Recoverable {
		t.Fatalf("events = %+v", got)
	}
	if strings.Contains(got[0].Message, "identity details") || !channel.IsPermanentInboundError(channel.InboundProcessingError(got)) {
		t.Fatalf("permission failure was exposed or marked retryable: %+v", got)
	}
}

func TestProductionMessageGatewayRunnerAcceptsTurnOnlyCommandReplay(t *testing.T) {
	discussions := &fakeProductionGatewayDiscussionService{events: []discussionsession.StreamEventDTO{{
		Kind: discussionsession.StreamEventTurnCompleted, TurnID: "turn-replayed", ConversationID: "conversation-replayed",
	}}}
	runner := newProductionMessageGatewayRunner(discussions)
	runner.SetBindingStore(testProductionGatewayBindingStore(t))
	runner.SetAgentDirectory(systemGatewayTestDirectory("human-a", "agent-a"))
	identity := createProductionGatewayBinding(t, runner.bindings, "feishu", "feishu:app-a", "fs-a", "open-a", "human-a", "agent-a")

	events, err := runner.RunTurn(context.Background(), gwprotocol.SubmitTurnInput{
		SessionKey: "feishu:chat-a", ChannelKey: gwprotocol.ChannelFeishu, Message: "ping", Identity: &identity,
	})
	if err != nil {
		t.Fatal(err)
	}
	got := collectGatewayEvents(events)
	if len(got) != 1 || got[0].Type != gwprotocol.EventTurnCompleted || got[0].FinishReason != "completed" {
		t.Fatalf("replay events = %+v", got)
	}
}

func createProductionGatewayBinding(
	t *testing.T,
	store *gwbindings.Store,
	channelKey, accountID, externalTenantID, externalUserID, humanID, agentID string,
) gwprotocol.InboundIdentity {
	t.Helper()
	if channelKey == string(gwprotocol.ChannelFeishu) {
		if _, err := store.PutAgentChannelBinding(context.Background(), gwbindings.PutAgentChannelBindingInput{
			TenantID: "system", ChannelKey: channelKey, AccountID: accountID, AgentProfileID: agentID, InboundEnabled: true,
		}); err != nil {
			t.Fatalf("PutAgentChannelBinding: %v", err)
		}
	}
	code, err := store.CreateCode(context.Background(), gwbindings.CreateCodeInput{
		TenantID: "system", ChannelKey: channelKey, AccountID: accountID, HumanProfileID: humanID, DefaultAgentID: agentID,
	})
	if err != nil {
		t.Fatalf("CreateCode: %v", err)
	}
	identity := gwprotocol.InboundIdentity{
		AccountID: accountID, ExternalTenantID: externalTenantID, ExternalUserID: externalUserID,
		ExternalChatID: "chat-a", ChatKind: "direct", Verified: true,
	}
	if _, err := store.ConsumeCode(context.Background(), code.Value, gwbindings.ExternalIdentity{
		ChannelKey: channelKey, AccountID: accountID, ExternalTenantID: externalTenantID, ExternalUserID: externalUserID,
	}); err != nil {
		t.Fatalf("ConsumeCode: %v", err)
	}
	return identity
}

func TestProductionMessageGatewayRunnerRoutesDiscussionStream(t *testing.T) {
	discussions := &fakeProductionGatewayDiscussionService{events: []discussionsession.StreamEventDTO{
		{Kind: discussionsession.StreamEventSnapshot, Conversation: &discussionsession.ConversationDTO{ConversationID: "conv-1"}},
		{Kind: discussionsession.StreamEventRunStarted, Run: &discussionsession.RunDTO{RunID: "discussion-run"}},
		{Kind: discussionsession.StreamEventTokenDelta, Delta: "hello", RunID: "discussion-run"},
		{Kind: discussionsession.StreamEventApprovalRequested, RunID: "discussion-run", Approval: &discussionsession.EventDTO{
			EventID:        "approval-1",
			ConversationID: "conv-1",
			Status:         "pending",
			Metadata: map[string]string{
				"approval_request_id": "call-1",
				"approval_tool_name":  "bash",
				"command":             "do-not-expose --token secret",
			},
		}},
		{Kind: discussionsession.StreamEventRunCompleted, Run: &discussionsession.RunDTO{RunID: "discussion-run", Status: "completed"}},
	}}
	cfg := gwconfig.DefaultConfig()
	cfg.Adapters.Weixin.Extra = map[string]any{
		"agentBindings": map[string]any{
			"wx-user": "staffing-agent",
		},
		"weixinUserBindings": map[string]any{"wx-user": "human-alice"},
	}
	runner := newProductionMessageGatewayRunner(discussions, cfg)
	runner.SetAgentDirectory(systemGatewayTestDirectory("human-alice", "staffing-agent"))

	events, err := runner.RunTurn(context.Background(), gwprotocol.SubmitTurnInput{
		SessionKey: "weixin:chat-1",
		ChannelKey: gwprotocol.ChannelWeixin,
		Message:    "ping",
		RunID:      "gateway-run",
		Telemetry: map[string]string{
			"weixin_sender_id":       "wx-user",
			"weixin_sender_name":     "Alice",
			"weixin_sender_verified": "true",
		},
	})
	if err != nil {
		t.Fatalf("RunTurn failed: %v", err)
	}
	got := collectGatewayEvents(events)
	if len(got) != 3 {
		t.Fatalf("events = %d, want 3", len(got))
	}
	if got[0].Type != gwprotocol.EventTurnStarted || got[0].RunID != "discussion-run" {
		t.Fatalf("started event = %+v", got[0])
	}
	if got[1].Type != gwprotocol.EventPermissionRequest || got[1].RequestID != "call-1" || got[1].ToolName != "bash" || strings.Contains(fmt.Sprint(got[1].Payload), "secret") {
		t.Fatalf("permission event = %+v", got[1])
	}
	if got[2].Type != gwprotocol.EventTurnCompleted || got[2].FinishReason != "completed" {
		t.Fatalf("completed event = %+v", got[2])
	}
	if discussions.cmd.Content != "ping" || discussions.cmd.IdempotencyKey != "gateway-run" {
		t.Fatalf("discussion command = %+v", discussions.cmd)
	}
	if discussions.cmd.ConversationID != "" || discussions.cmd.DirectTargetKind != "agent" || discussions.cmd.DirectTargetID != "staffing-agent" {
		t.Fatalf("weixin command should enter normal direct chat path: %+v", discussions.cmd)
	}
	if discussions.viewer.DisplayName != "Alice" {
		t.Fatalf("viewer = %+v", discussions.viewer)
	}

	events, err = runner.RunTurn(context.Background(), gwprotocol.SubmitTurnInput{
		SessionKey: "weixin:chat-1",
		ChannelKey: gwprotocol.ChannelWeixin,
		Message:    "again",
		RunID:      "gateway-run-2",
		Telemetry: map[string]string{
			"weixin_sender_id":       "wx-user",
			"weixin_chat_id":         "wx-user",
			"weixin_sender_verified": "true",
		},
	})
	if err != nil {
		t.Fatalf("RunTurn failed: %v", err)
	}
	_ = collectGatewayEvents(events)
	if discussions.cmd.ConversationID != "" || discussions.cmd.DirectTargetKind != "agent" || discussions.cmd.DirectTargetID != "staffing-agent" {
		t.Fatalf("weixin direct chat should be resolved by discussion service, not gateway memory: %+v", discussions.cmd)
	}
}

func TestProductionMessageGatewayRunnerRoutesWeixinUserToBoundAgent(t *testing.T) {
	discussions := &fakeProductionGatewayDiscussionService{}
	cfg := gwconfig.DefaultConfig()
	cfg.Adapters.Weixin.Extra = map[string]any{
		"agentBindings": map[string]any{
			"wx-user": "staffing-agent",
		},
		"weixinUserBindings": map[string]any{"wx-user": "human-alice"},
	}
	runner := newProductionMessageGatewayRunner(discussions, cfg)
	runner.SetAgentDirectory(systemGatewayTestDirectory("human-alice", "staffing-agent"))

	events, err := runner.RunTurn(context.Background(), gwprotocol.SubmitTurnInput{
		SessionKey: "weixin:wx-user",
		ChannelKey: gwprotocol.ChannelWeixin,
		Message:    "ping",
		RunID:      "gateway-run",
		Telemetry: map[string]string{
			"weixin_sender_id":       "wx-user",
			"weixin_chat_id":         "wx-user",
			"weixin_sender_verified": "true",
		},
	})
	if err != nil {
		t.Fatalf("RunTurn failed: %v", err)
	}
	_ = collectGatewayEvents(events)

	if discussions.cmd.DirectTargetKind != "agent" || discussions.cmd.DirectTargetID != "staffing-agent" {
		t.Fatalf("direct target = kind %q id %q", discussions.cmd.DirectTargetKind, discussions.cmd.DirectTargetID)
	}
	if len(discussions.cmd.InitialMemberIDs) != 0 {
		t.Fatalf("initial members = %+v", discussions.cmd.InitialMemberIDs)
	}
}

func TestProductionMessageGatewayRunnerUsesBoundHumanViewerForWeixin(t *testing.T) {
	discussions := &fakeProductionGatewayDiscussionService{}
	cfg := gwconfig.DefaultConfig()
	cfg.Adapters.Weixin.Extra = map[string]any{
		"agentBindings": map[string]any{
			"wx-user": "staffing-agent",
		},
		"weixinUserBindings": map[string]any{
			"wx-user": "human-alice",
		},
	}
	runner := newProductionMessageGatewayRunner(discussions, cfg)
	runner.SetAgentDirectory(systemGatewayTestDirectory("human-alice", "staffing-agent"))

	events, err := runner.RunTurn(context.Background(), gwprotocol.SubmitTurnInput{
		SessionKey: "weixin:wx-user",
		ChannelKey: gwprotocol.ChannelWeixin,
		Message:    "ping",
		RunID:      "gateway-run",
		Telemetry: map[string]string{
			"weixin_sender_id":       "wx-user",
			"weixin_sender_name":     "Alice",
			"weixin_sender_verified": "true",
		},
	})
	if err != nil {
		t.Fatalf("RunTurn failed: %v", err)
	}
	_ = collectGatewayEvents(events)

	if discussions.viewer.ViewerID != "human-alice" || discussions.viewer.ViewerKind != discussionsession.ViewerKindUser || discussions.viewer.DisplayName != "Alice" {
		t.Fatalf("viewer = %+v", discussions.viewer)
	}
	if discussions.cmd.DirectTargetKind != "agent" || discussions.cmd.DirectTargetID != "staffing-agent" || discussions.cmd.ConversationID != "" {
		t.Fatalf("command = %+v", discussions.cmd)
	}
}

func TestProductionMessageGatewayRunnerRejectsAccountAliasIdentityFallback(t *testing.T) {
	discussions := &fakeProductionGatewayDiscussionService{}
	cfg := gwconfig.DefaultConfig()
	cfg.Adapters.Weixin.Extra = map[string]any{
		"agentBindings": map[string]any{
			"user-1@im.wechat": "staffing-agent",
		},
		"weixinUserBindings": map[string]any{"user-1@im.wechat": "human-owner"},
		"weixinAccounts": map[string]any{
			"user-1@im.wechat": map[string]any{
				"userId": "user-1@im.wechat",
				"botId":  "bot-1@im.bot",
				"token":  "bot-secret",
			},
		},
	}
	runner := newProductionMessageGatewayRunner(discussions, cfg)

	events, err := runner.RunTurn(context.Background(), gwprotocol.SubmitTurnInput{
		SessionKey: "weixin:chat-1",
		ChannelKey: gwprotocol.ChannelWeixin,
		Message:    "ping",
		Telemetry: map[string]string{
			"weixin_account_id":      "bot-1@im.bot",
			"weixin_sender_id":       "external-user",
			"weixin_chat_id":         "chat-1",
			"weixin_sender_verified": "true",
		},
	})
	if err != nil {
		t.Fatalf("RunTurn failed: %v", err)
	}
	got := collectGatewayEvents(events)
	if len(got) != 2 || got[0].Code != gwprotocol.ErrorCodeGatewayPermissionDenied {
		t.Fatalf("events = %+v", got)
	}
	if discussions.cmd.Content != "" {
		t.Fatalf("account id must not select a human identity: %+v", discussions.cmd)
	}
}

func TestProductionMessageGatewayRunnerRejectsUnboundWeixinUser(t *testing.T) {
	discussions := &fakeProductionGatewayDiscussionService{}
	cfg := gwconfig.DefaultConfig()
	cfg.Adapters.Weixin.Extra = map[string]any{
		"agentBindings": map[string]any{
			"other-user": "staffing-agent",
		},
	}
	runner := newProductionMessageGatewayRunner(discussions, cfg)

	events, err := runner.RunTurn(context.Background(), gwprotocol.SubmitTurnInput{
		SessionKey: "weixin:wx-user",
		ChannelKey: gwprotocol.ChannelWeixin,
		Message:    "ping",
		RunID:      "gateway-run",
		Telemetry: map[string]string{
			"weixin_sender_id":       "wx-user",
			"weixin_chat_id":         "wx-user",
			"weixin_sender_verified": "true",
		},
	})
	if err != nil {
		t.Fatalf("RunTurn failed: %v", err)
	}
	got := collectGatewayEvents(events)
	if len(got) != 2 || got[0].Type != gwprotocol.EventError || got[0].Code != gwprotocol.ErrorCodeGatewayPermissionDenied {
		t.Fatalf("events = %+v", got)
	}
	if discussions.cmd.Content != "" {
		t.Fatalf("discussion should not be called: %+v", discussions.cmd)
	}
}

func TestProductionMessageGatewayRunnerListsWeixinSwitchableBots(t *testing.T) {
	discussions := &fakeProductionGatewayDiscussionService{}
	cfg := gwconfig.DefaultConfig()
	cfg.Adapters.Weixin.Extra = map[string]any{
		"agentBindings":      map[string]any{"wx-user": "staffing-agent"},
		"weixinUserBindings": map[string]any{"wx-user": "human-alice"},
	}
	runner := newProductionMessageGatewayRunner(discussions, cfg)
	runner.SetAgentDirectory(&fakeProductionGatewayAgentDirectory{entries: []agentcore.AgentDirectoryEntry{
		gatewayTenantDirectoryEntry("human-alice", agentcore.AgentKindHuman, "system"),
		{AgentProfileID: "staffing-agent", DisplayName: "秦若兰", AgentKind: agentcore.AgentKindAI, Enabled: true, OrgIdentity: gatewayTenantDirectoryEntry("staffing-agent", agentcore.AgentKindAI, "system").OrgIdentity},
		{AgentProfileID: "writer-agent", DisplayName: "文案", AgentKind: agentcore.AgentKindAI, Enabled: true, OrgIdentity: gatewayTenantDirectoryEntry("writer-agent", agentcore.AgentKindAI, "system").OrgIdentity},
	}})

	events, err := runner.RunTurn(context.Background(), gwprotocol.SubmitTurnInput{
		SessionKey: "weixin:wx-user",
		ChannelKey: gwprotocol.ChannelWeixin,
		Message:    "/换人",
		Telemetry:  verifiedWeixinTelemetry("wx-user"),
	})
	if err != nil {
		t.Fatalf("RunTurn failed: %v", err)
	}
	got := collectGatewayEvents(events)
	if len(got) != 2 || !strings.Contains(got[0].Text, "可用 bot") || !strings.Contains(got[0].Text, "staffing-agent") || !strings.Contains(got[0].Text, "writer-agent") {
		t.Fatalf("events = %+v", got)
	}
	if len(discussions.cmds) != 0 {
		t.Fatalf("discussion should not be called: %+v", discussions.cmds)
	}
}

func TestProductionMessageGatewayRunnerSwitchesWeixinBotAndPersistsActiveBinding(t *testing.T) {
	discussions := &fakeProductionGatewayDiscussionService{}
	cfg := gwconfig.DefaultConfig()
	cfg.Adapters.Weixin.Extra = map[string]any{
		"agentBindings":      map[string]any{"wx-user": "staffing-agent"},
		"weixinUserBindings": map[string]any{"wx-user": "human-alice"},
	}
	store := gwconfig.NewStore(filepath.Join(t.TempDir(), "gateway.yaml"))
	if _, err := store.Save(cfg); err != nil {
		t.Fatalf("seed config: %v", err)
	}
	runner := newProductionMessageGatewayRunner(discussions, cfg)
	runner.SetConfigStore(store)
	runner.SetAgentDirectory(&fakeProductionGatewayAgentDirectory{entries: []agentcore.AgentDirectoryEntry{
		gatewayTenantDirectoryEntry("human-alice", agentcore.AgentKindHuman, "system"),
		{AgentProfileID: "staffing-agent", DisplayName: "秦若兰", AgentKind: agentcore.AgentKindAI, Enabled: true, OrgIdentity: gatewayTenantDirectoryEntry("staffing-agent", agentcore.AgentKindAI, "system").OrgIdentity},
		{AgentProfileID: "writer-agent", DisplayName: "文案", AgentKind: agentcore.AgentKindAI, Enabled: true, OrgIdentity: gatewayTenantDirectoryEntry("writer-agent", agentcore.AgentKindAI, "system").OrgIdentity},
	}})

	events, err := runner.RunTurn(context.Background(), gwprotocol.SubmitTurnInput{
		SessionKey: "weixin:wx-user",
		ChannelKey: gwprotocol.ChannelWeixin,
		Message:    "/换人 writer-agent",
		Telemetry:  verifiedWeixinTelemetry("wx-user"),
	})
	if err != nil {
		t.Fatalf("RunTurn failed: %v", err)
	}
	got := collectGatewayEvents(events)
	if len(got) != 2 || !strings.Contains(got[0].Text, "已切换到 bot") {
		t.Fatalf("events = %+v", got)
	}
	loaded, err := store.LoadOrDefault()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	active := loaded.Config.Adapters.Weixin.Extra["activeAgentBindings"].(map[string]any)
	if active["wx-user"] != "writer-agent" {
		t.Fatalf("activeAgentBindings = %+v", active)
	}

	events, err = runner.RunTurn(context.Background(), gwprotocol.SubmitTurnInput{
		SessionKey: "weixin:wx-user",
		ChannelKey: gwprotocol.ChannelWeixin,
		Message:    "ping",
		Telemetry:  verifiedWeixinTelemetry("wx-user"),
	})
	if err != nil {
		t.Fatalf("RunTurn failed: %v", err)
	}
	_ = collectGatewayEvents(events)
	if discussions.cmd.DirectTargetID != "writer-agent" {
		t.Fatalf("direct target after switch = %q", discussions.cmd.DirectTargetID)
	}
}

func TestProductionMessageGatewayRunnerRejectsMissingWeixinSwitchBot(t *testing.T) {
	discussions := &fakeProductionGatewayDiscussionService{}
	cfg := gwconfig.DefaultConfig()
	cfg.Adapters.Weixin.Extra = map[string]any{
		"agentBindings":      map[string]any{"wx-user": "staffing-agent"},
		"weixinUserBindings": map[string]any{"wx-user": "human-alice"},
	}
	runner := newProductionMessageGatewayRunner(discussions, cfg)
	runner.SetAgentDirectory(&fakeProductionGatewayAgentDirectory{entries: []agentcore.AgentDirectoryEntry{
		gatewayTenantDirectoryEntry("human-alice", agentcore.AgentKindHuman, "system"),
		{AgentProfileID: "staffing-agent", DisplayName: "秦若兰", AgentKind: agentcore.AgentKindAI, Enabled: true, OrgIdentity: gatewayTenantDirectoryEntry("staffing-agent", agentcore.AgentKindAI, "system").OrgIdentity},
	}})

	events, err := runner.RunTurn(context.Background(), gwprotocol.SubmitTurnInput{
		SessionKey: "weixin:wx-user",
		ChannelKey: gwprotocol.ChannelWeixin,
		Message:    "/换人 missing-agent",
		Telemetry:  verifiedWeixinTelemetry("wx-user"),
	})
	if err != nil {
		t.Fatalf("RunTurn failed: %v", err)
	}
	got := collectGatewayEvents(events)
	if len(got) != 2 || got[0].Code != gwprotocol.ErrorCodeGatewayRequestInvalid {
		t.Fatalf("events = %+v", got)
	}
	if len(discussions.cmds) != 0 {
		t.Fatalf("discussion should not be called: %+v", discussions.cmds)
	}
}

func TestProductionMessageGatewayRunnerReloadsWeixinBindings(t *testing.T) {
	discussions := &fakeProductionGatewayDiscussionService{}
	cfg := gwconfig.DefaultConfig()
	cfg.Adapters.Weixin.Extra = map[string]any{
		"agentBindings": map[string]any{
			"wx-user": "agent-before",
		},
		"weixinUserBindings": map[string]any{"wx-user": "human-alice"},
	}
	runner := newProductionMessageGatewayRunner(discussions, cfg)
	runner.SetAgentDirectory(systemGatewayTestDirectory("human-alice", "agent-before", "agent-after"))
	cfg.Adapters.Weixin.Extra = map[string]any{
		"agentBindings": map[string]any{
			"wx-user": "agent-after",
		},
		"weixinUserBindings": map[string]any{"wx-user": "human-alice"},
	}
	runner.UpdateConfig(cfg)

	events, err := runner.RunTurn(context.Background(), gwprotocol.SubmitTurnInput{
		SessionKey: "weixin:wx-user",
		ChannelKey: gwprotocol.ChannelWeixin,
		Message:    "ping",
		Telemetry: map[string]string{
			"weixin_sender_id":       "wx-user",
			"weixin_sender_verified": "true",
		},
	})
	if err != nil {
		t.Fatalf("RunTurn failed: %v", err)
	}
	_ = collectGatewayEvents(events)

	if discussions.cmd.DirectTargetID != "agent-after" {
		t.Fatalf("direct target after reload = %q", discussions.cmd.DirectTargetID)
	}
}

func TestProductionMessageGatewayRunnerDoesNotPinWeixinSessionAfterBindingReload(t *testing.T) {
	discussions := &fakeProductionGatewayDiscussionService{events: []discussionsession.StreamEventDTO{
		{Kind: discussionsession.StreamEventSnapshot, Conversation: &discussionsession.ConversationDTO{ConversationID: "conv-before"}},
	}}
	cfg := gwconfig.DefaultConfig()
	cfg.Adapters.Weixin.Extra = map[string]any{
		"agentBindings": map[string]any{
			"wx-user": "agent-before",
		},
		"weixinUserBindings": map[string]any{"wx-user": "human-alice"},
	}
	runner := newProductionMessageGatewayRunner(discussions, cfg)
	runner.SetAgentDirectory(systemGatewayTestDirectory("human-alice", "agent-before", "agent-after"))

	events, err := runner.RunTurn(context.Background(), gwprotocol.SubmitTurnInput{
		SessionKey: "weixin:wx-user",
		ChannelKey: gwprotocol.ChannelWeixin,
		Message:    "first",
		Telemetry: map[string]string{
			"weixin_sender_id":       "wx-user",
			"weixin_sender_verified": "true",
		},
	})
	if err != nil {
		t.Fatalf("RunTurn failed: %v", err)
	}
	_ = collectGatewayEvents(events)

	cfg.Adapters.Weixin.Extra = map[string]any{
		"agentBindings": map[string]any{
			"wx-user": "agent-after",
		},
		"weixinUserBindings": map[string]any{"wx-user": "human-alice"},
	}
	runner.UpdateConfig(cfg)
	events, err = runner.RunTurn(context.Background(), gwprotocol.SubmitTurnInput{
		SessionKey: "weixin:wx-user",
		ChannelKey: gwprotocol.ChannelWeixin,
		Message:    "second",
		Telemetry: map[string]string{
			"weixin_sender_id":       "wx-user",
			"weixin_sender_verified": "true",
		},
	})
	if err != nil {
		t.Fatalf("RunTurn failed: %v", err)
	}
	_ = collectGatewayEvents(events)

	if len(discussions.cmds) != 2 {
		t.Fatalf("commands = %d", len(discussions.cmds))
	}
	second := discussions.cmds[1]
	if second.ConversationID != "" || second.DirectTargetKind != "agent" || second.DirectTargetID != "agent-after" {
		t.Fatalf("second weixin command should route by latest binding as a normal direct chat: %+v", second)
	}
}

func TestProductionMessageGatewayRunnerRejectsUnverifiedOrGroupSender(t *testing.T) {
	discussions := &fakeProductionGatewayDiscussionService{}
	cfg := gwconfig.DefaultConfig()
	cfg.Adapters.Weixin.Extra = map[string]any{
		"agentBindings":      map[string]any{"wx-user": "staffing-agent"},
		"weixinUserBindings": map[string]any{"wx-user": "human-alice"},
	}
	runner := newProductionMessageGatewayRunner(discussions, cfg)

	for index, telemetry := range []map[string]string{
		{"weixin_sender_id": "wx-user"},
		{"weixin_sender_id": "wx-user", "weixin_sender_verified": "true", "weixin_group": "true"},
	} {
		events, err := runner.RunTurn(context.Background(), gwprotocol.SubmitTurnInput{
			SessionKey: "weixin:wx-user",
			ChannelKey: gwprotocol.ChannelWeixin,
			Message:    "ping",
			Telemetry:  telemetry,
		})
		if err != nil {
			t.Fatalf("RunTurn failed: %v", err)
		}
		got := collectGatewayEvents(events)
		if index == 0 && (len(got) != 2 || got[0].Code != gwprotocol.ErrorCodeGatewayPermissionDenied) {
			t.Fatalf("events = %+v", got)
		}
		if index == 1 && (len(got) != 1 || got[0].Type != gwprotocol.EventTurnCompleted || got[0].FinishReason != "group_not_approved") {
			t.Fatalf("events = %+v", got)
		}
	}
	if len(discussions.cmds) != 0 {
		t.Fatalf("unverified/group senders reached discussion service: %+v", discussions.cmds)
	}
}

func TestProductionGatewayConfigPathUsesPersistentBaseDir(t *testing.T) {
	root := t.TempDir()
	cfg := loadTestConfig(t, map[string]string{
		"paths.base_dir":                  root,
		"bootstrap.gateway.legacy_config": "",
	})
	rt := &platform.Runtime{Config: cfg}

	got := productionGatewayConfigPath(rt)
	want := filepath.Join(root, "config", "gateway.yaml")
	if got != want {
		t.Fatalf("config path = %q, want %q", got, want)
	}
	if stateDir := productionGatewayStateDir(rt); stateDir != filepath.Join(root, "config", "state") {
		t.Fatalf("state dir = %q", stateDir)
	}
}

func TestProductionMessageGatewayRunnerScopesSameWeixinSenderByTenantAccount(t *testing.T) {
	discussions := &fakeProductionGatewayDiscussionService{events: []discussionsession.StreamEventDTO{{Kind: discussionsession.StreamEventRunCompleted}}}
	tenantAKey := gwconfig.WeixinBindingKey("tenant-a", "wx-shared")
	tenantBKey := gwconfig.WeixinBindingKey("tenant-b", "wx-shared")
	cfg := gwconfig.DefaultConfig()
	cfg.Adapters.Weixin.Extra = map[string]any{
		"agentBindings":      map[string]any{tenantAKey: "agent-a", tenantBKey: "agent-b"},
		"weixinUserBindings": map[string]any{tenantAKey: "human-a", tenantBKey: "human-b"},
		"weixinAccounts": map[string]any{
			tenantAKey: map[string]any{"tenantId": "tenant-a", "userId": "wx-shared", "botId": "bot-a", "token": "secret-a"},
			tenantBKey: map[string]any{"tenantId": "tenant-b", "userId": "wx-shared", "botId": "bot-b", "token": "secret-b"},
		},
	}
	runner := newProductionMessageGatewayRunner(discussions, cfg)
	runner.SetAgentDirectory(&fakeProductionGatewayAgentDirectory{entries: []agentcore.AgentDirectoryEntry{
		gatewayTenantDirectoryEntry("human-a", agentcore.AgentKindHuman, "tenant-a"),
		gatewayTenantDirectoryEntry("agent-a", agentcore.AgentKindAI, "tenant-a"),
		gatewayTenantDirectoryEntry("human-b", agentcore.AgentKindHuman, "tenant-b"),
		gatewayTenantDirectoryEntry("agent-b", agentcore.AgentKindAI, "tenant-b"),
	}})

	for _, test := range []struct {
		accountID string
		tenantID  string
		humanID   string
		agentID   string
	}{
		{accountID: "bot-a", tenantID: "tenant-a", humanID: "human-a", agentID: "agent-a"},
		{accountID: "bot-b", tenantID: "tenant-b", humanID: "human-b", agentID: "agent-b"},
	} {
		input := gwprotocol.SubmitTurnInput{ChannelKey: gwprotocol.ChannelWeixin, Message: "ping", Telemetry: verifiedWeixinTelemetry("wx-shared")}
		input.Telemetry["weixin_account_id"] = test.accountID
		identity := runner.weixinIdentity(input)
		if identity.TenantID != test.tenantID || identity.ViewerID != test.humanID || identity.AgentID != test.agentID {
			t.Fatalf("account %s identity = %+v", test.accountID, identity)
		}
		events, err := runner.RunTurn(context.Background(), input)
		if err != nil {
			t.Fatalf("RunTurn(%s): %v", test.accountID, err)
		}
		_ = collectGatewayEvents(events)
		if discussions.viewer.TenantID != test.tenantID || discussions.viewer.ViewerID != test.humanID || discussions.cmd.DirectTargetID != test.agentID {
			t.Fatalf("account %s route viewer=%+v cmd=%+v", test.accountID, discussions.viewer, discussions.cmd)
		}
	}

	unknown := gwprotocol.SubmitTurnInput{ChannelKey: gwprotocol.ChannelWeixin, Telemetry: verifiedWeixinTelemetry("wx-shared")}
	unknown.Telemetry["weixin_account_id"] = "bot-unknown"
	if identity := runner.weixinIdentity(unknown); identity.UserID != "" {
		t.Fatalf("unknown account crossed tenant boundary: %+v", identity)
	}
}

func TestProductionMessageGatewayRunnerRejectsCrossTenantAgentBinding(t *testing.T) {
	discussions := &fakeProductionGatewayDiscussionService{}
	bindingKey := gwconfig.WeixinBindingKey("tenant-a", "wx-user")
	cfg := gwconfig.DefaultConfig()
	cfg.Adapters.Weixin.Extra = map[string]any{
		"agentBindings":      map[string]any{bindingKey: "agent-b"},
		"weixinUserBindings": map[string]any{bindingKey: "human-a"},
		"weixinAccounts": map[string]any{
			bindingKey: map[string]any{"tenantId": "tenant-a", "userId": "wx-user", "botId": "bot-a", "token": "secret"},
		},
	}
	runner := newProductionMessageGatewayRunner(discussions, cfg)
	runner.SetAgentDirectory(&fakeProductionGatewayAgentDirectory{entries: []agentcore.AgentDirectoryEntry{
		gatewayTenantDirectoryEntry("human-a", agentcore.AgentKindHuman, "tenant-a"),
		gatewayTenantDirectoryEntry("agent-b", agentcore.AgentKindAI, "tenant-b"),
	}})
	input := gwprotocol.SubmitTurnInput{ChannelKey: gwprotocol.ChannelWeixin, Message: "ping", Telemetry: verifiedWeixinTelemetry("wx-user")}
	input.Telemetry["weixin_account_id"] = "bot-a"
	events, err := runner.RunTurn(context.Background(), input)
	if err != nil {
		t.Fatalf("RunTurn: %v", err)
	}
	got := collectGatewayEvents(events)
	if len(got) != 2 || got[0].Code != gwprotocol.ErrorCodeGatewayPermissionDenied || len(discussions.cmds) != 0 {
		t.Fatalf("cross-tenant binding was routed: events=%+v cmds=%+v", got, discussions.cmds)
	}
}

func TestProductionMessageGatewayRunnerRejectsDisabledOrWrongKindProfiles(t *testing.T) {
	tests := []struct {
		name   string
		viewer agentcore.AgentDirectoryEntry
		agent  agentcore.AgentDirectoryEntry
	}{
		{
			name: "disabled viewer",
			viewer: func() agentcore.AgentDirectoryEntry {
				entry := gatewayTenantDirectoryEntry("human-a", agentcore.AgentKindHuman, "tenant-a")
				entry.Enabled = false
				return entry
			}(),
			agent: gatewayTenantDirectoryEntry("agent-a", agentcore.AgentKindAI, "tenant-a"),
		},
		{
			name:   "viewer is not human",
			viewer: gatewayTenantDirectoryEntry("human-a", agentcore.AgentKindAI, "tenant-a"),
			agent:  gatewayTenantDirectoryEntry("agent-a", agentcore.AgentKindAI, "tenant-a"),
		},
		{
			name:   "agent is not AI",
			viewer: gatewayTenantDirectoryEntry("human-a", agentcore.AgentKindHuman, "tenant-a"),
			agent:  gatewayTenantDirectoryEntry("agent-a", agentcore.AgentKindHuman, "tenant-a"),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			runner := newProductionMessageGatewayRunner(&fakeProductionGatewayDiscussionService{})
			runner.SetAgentDirectory(&fakeProductionGatewayAgentDirectory{entries: []agentcore.AgentDirectoryEntry{test.viewer, test.agent}})
			err := runner.validateGatewayRouteTenant(context.Background(), productionGatewayRoute{
				TenantID: "tenant-a", ViewerID: "human-a", AgentID: "agent-a",
			})
			if err == nil {
				t.Fatal("invalid profile route was accepted")
			}
		})
	}
}
