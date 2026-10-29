package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/discussionsession"
	gwbindings "orrery_v2/internal/gateway/binding"
	gwprotocol "orrery_v2/internal/gateway/protocol"
	"orrery_v2/internal/platform/db"
)

func TestProductionMessageGatewayRunnerBindsAndRoutesFeishuIdentity(t *testing.T) {
	ctx := context.Background()
	database, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: t.Name()})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer database.Close()
	bindings := gwbindings.NewStore(database)
	code, err := bindings.CreateCode(ctx, gwbindings.CreateCodeInput{
		TenantID: "tenant-a", ChannelKey: "feishu", AccountID: "feishu:app-a", HumanProfileID: "human-a", DefaultAgentID: "agent-a",
	})
	if err != nil {
		t.Fatalf("CreateCode: %v", err)
	}
	if _, err := bindings.PutAgentChannelBinding(ctx, gwbindings.PutAgentChannelBindingInput{
		TenantID: "tenant-a", ChannelKey: "feishu", AccountID: "feishu:app-a", AgentProfileID: "agent-a", InboundEnabled: true,
	}); err != nil {
		t.Fatalf("PutAgentChannelBinding: %v", err)
	}
	discussions := &fakeProductionGatewayDiscussionService{events: []discussionsession.StreamEventDTO{{Kind: discussionsession.StreamEventRunCompleted}}}
	runner := newProductionMessageGatewayRunner(discussions)
	runner.SetBindingStore(bindings)
	audit := &recordingProductionGatewayAudit{}
	runner.SetAuditRecorder(audit)
	runner.SetAgentDirectory(&fakeProductionGatewayAgentDirectory{entries: []agentcore.AgentDirectoryEntry{
		gatewayTenantDirectoryEntry("human-a", agentcore.AgentKindHuman, "tenant-a"),
		gatewayTenantDirectoryEntry("agent-a", agentcore.AgentKindAI, "tenant-a"),
	}})
	identity := &gwprotocol.InboundIdentity{
		AccountID: "feishu:app-a", ExternalTenantID: "fs-tenant-a", ExternalUserID: "open-a",
		ExternalChatID: "chat-a", ChatKind: "direct", DisplayName: "Alice", Verified: true,
	}
	bindEvents, err := runner.RunTurn(ctx, gwprotocol.SubmitTurnInput{
		SessionKey: "feishu:chat-a", ChannelKey: gwprotocol.ChannelFeishu, Message: "/绑定 " + code.Value, Identity: identity,
	})
	if err != nil {
		t.Fatalf("binding RunTurn: %v", err)
	}
	got := collectGatewayEvents(bindEvents)
	if len(got) != 2 || got[0].Text != "绑定成功，现在可以在这里使用 Orrery。" {
		t.Fatalf("binding events = %+v", got)
	}
	if len(audit.events) != 1 || audit.events[0].Result != "succeeded" || audit.events[0].Metadata["finish_reason"] != "binding_completed" {
		t.Fatalf("binding success audit = %+v", audit.events)
	}
	encoded, err := json.Marshal(audit.events[0])
	if err != nil {
		t.Fatalf("marshal audit: %v", err)
	}
	if strings.Contains(string(encoded), code.Value) {
		t.Fatalf("binding code leaked into audit: %s", encoded)
	}
	events, err := runner.RunTurn(ctx, gwprotocol.SubmitTurnInput{
		SessionKey: "feishu:chat-a", ChannelKey: gwprotocol.ChannelFeishu, Message: "ping", Identity: identity,
	})
	if err != nil {
		t.Fatalf("message RunTurn: %v", err)
	}
	_ = collectGatewayEvents(events)
	if len(discussions.cmds) != 1 || discussions.cmds[0].DirectTargetID != "agent-a" {
		t.Fatalf("commands = %+v", discussions.cmds)
	}
	if discussions.viewer.ViewerID != "human-a" || discussions.viewer.TenantID != "tenant-a" {
		t.Fatalf("viewer = %+v", discussions.viewer)
	}
}

func TestProductionMessageGatewayRunnerDoesNotAcceptArbitraryCodeForBoundIdentity(t *testing.T) {
	ctx := context.Background()
	database, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: t.Name()})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer database.Close()
	bindings := gwbindings.NewStore(database)
	identityValue := createProductionGatewayBinding(t, bindings, "feishu", "feishu:app-a", "fs-a", "open-a", "human-a", "agent-a")
	runner := newProductionMessageGatewayRunner(&fakeProductionGatewayDiscussionService{})
	runner.SetBindingStore(bindings)
	runner.SetAgentDirectory(systemGatewayTestDirectory("human-a", "agent-a"))

	events, err := runner.RunTurn(ctx, gwprotocol.SubmitTurnInput{
		ChannelKey: gwprotocol.ChannelFeishu, Message: "/绑定 INVALID-CODE", Identity: &identityValue,
	})
	if err != nil {
		t.Fatalf("RunTurn: %v", err)
	}
	got := collectGatewayEvents(events)
	if len(got) != 2 || got[0].Type != gwprotocol.EventError || got[0].Code != gwprotocol.ErrorCodeGatewayRequestInvalid || got[1].FinishReason != "binding_failed" {
		t.Fatalf("invalid binding events = %+v", got)
	}
}

func TestProductionMessageGatewayRunnerRetriesBindingStoreOutage(t *testing.T) {
	ctx := context.Background()
	database, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: t.Name()})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	bindings := gwbindings.NewStore(database)
	if err := bindings.EnsureSchema(ctx); err != nil {
		t.Fatalf("EnsureSchema: %v", err)
	}
	if err := database.Close(); err != nil {
		t.Fatalf("close database: %v", err)
	}
	discussions := &fakeProductionGatewayDiscussionService{}
	runner := newProductionMessageGatewayRunner(discussions)
	runner.SetBindingStore(bindings)
	runner.SetAgentDirectory(systemGatewayTestDirectory("human-a", "agent-a"))
	events, err := runner.RunTurn(ctx, gwprotocol.SubmitTurnInput{
		ChannelKey: gwprotocol.ChannelFeishu, Message: "hello",
		Identity: &gwprotocol.InboundIdentity{
			AccountID: "feishu:app-a", ExternalTenantID: "fs-a", ExternalUserID: "open-a", ChatKind: "direct", Verified: true,
		},
	})
	if err != nil {
		t.Fatalf("RunTurn: %v", err)
	}
	got := collectGatewayEvents(events)
	if len(got) != 1 || got[0].Type != gwprotocol.EventError || got[0].Code != gwprotocol.ErrorCodeGatewayRouteUnavailable {
		t.Fatalf("events = %+v", got)
	}
	if len(discussions.cmds) != 0 {
		t.Fatalf("discussion ran without a verified binding route: %+v", discussions.cmds)
	}
}
