package main

import (
	"context"
	"testing"

	agentcore "orrery_v2/internal/agent/core"
	gwbindings "orrery_v2/internal/gateway/binding"
	gwprotocol "orrery_v2/internal/gateway/protocol"
	"orrery_v2/internal/platform/db"
)

func TestProductionMessageGatewayRunnerRejectsUnknownChatKindBeforeBinding(t *testing.T) {
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
	runner := newProductionMessageGatewayRunner(&fakeProductionGatewayDiscussionService{})
	runner.SetBindingStore(bindings)
	runner.SetAgentDirectory(&fakeProductionGatewayAgentDirectory{entries: []agentcore.AgentDirectoryEntry{
		gatewayTenantDirectoryEntry("human-a", agentcore.AgentKindHuman, "tenant-a"),
		gatewayTenantDirectoryEntry("agent-a", agentcore.AgentKindAI, "tenant-a"),
	}})
	identity := &gwprotocol.InboundIdentity{
		AccountID: "feishu:app-a", ExternalTenantID: "fs-a", ExternalUserID: "open-a", ChatKind: "unknown", Verified: true,
	}
	events, err := runner.RunTurn(ctx, gwprotocol.SubmitTurnInput{
		ChannelKey: gwprotocol.ChannelFeishu, Message: "/绑定 " + code.Value, Identity: identity,
	})
	if err != nil {
		t.Fatalf("unknown-chat RunTurn: %v", err)
	}
	got := collectGatewayEvents(events)
	if len(got) != 2 || got[1].FinishReason != "binding_rejected" {
		t.Fatalf("unknown-chat binding events = %+v", got)
	}
	identity.ChatKind = "direct"
	events, err = runner.RunTurn(ctx, gwprotocol.SubmitTurnInput{
		ChannelKey: gwprotocol.ChannelFeishu, Message: "/绑定 " + code.Value, Identity: identity,
	})
	if err != nil {
		t.Fatalf("direct-chat RunTurn: %v", err)
	}
	got = collectGatewayEvents(events)
	if len(got) != 2 || got[1].FinishReason != "binding_completed" {
		t.Fatalf("direct-chat binding events = %+v", got)
	}
}
