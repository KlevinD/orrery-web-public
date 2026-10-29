package main

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/discussionsession"
	"orrery_v2/internal/app/operations"
	gwbindings "orrery_v2/internal/gateway/binding"
	gwprotocol "orrery_v2/internal/gateway/protocol"
	"orrery_v2/internal/platform/db"
)

type recordingProductionGatewayAudit struct {
	events []operations.AuditEventDTO
}

func (r *recordingProductionGatewayAudit) RecordAuditEvent(_ context.Context, event operations.AuditEventDTO) error {
	r.events = append(r.events, event)
	return nil
}

func TestProductionGatewayFeishuAgentBindingRoutesToCurrentOwner(t *testing.T) {
	ctx := context.Background()
	database, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: t.Name()})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	bindings := gwbindings.NewStore(database)
	if _, err := bindings.UpsertVerified(ctx, gwbindings.UpsertInput{
		TenantID: "tenant-a", ChannelKey: "feishu", AccountID: "jordan-belfort", ExternalTenantID: "fs-a",
		ExternalUserID: "open-a", HumanProfileID: "human-a", DefaultAgentID: "gangbeng", ActiveAgentID: "jordan-belfort",
	}); err != nil {
		t.Fatalf("seed personal binding: %v", err)
	}
	if _, err := bindings.PutAgentChannelBinding(ctx, gwbindings.PutAgentChannelBindingInput{
		TenantID: "tenant-a", ChannelKey: "feishu", AccountID: "jordan-belfort",
		AgentProfileID: "jordan-belfort", InboundEnabled: true,
	}); err != nil {
		t.Fatalf("seed ownership: %v", err)
	}
	discussions := &fakeProductionGatewayDiscussionService{events: []discussionsession.StreamEventDTO{{Kind: discussionsession.StreamEventRunCompleted}}}
	runner := newProductionMessageGatewayRunner(discussions)
	runner.SetBindingStore(bindings)
	audit := &recordingProductionGatewayAudit{}
	runner.SetAuditRecorder(audit)
	runner.SetAgentDirectory(&fakeProductionGatewayAgentDirectory{entries: []agentcore.AgentDirectoryEntry{
		gatewayTenantDirectoryEntry("human-a", agentcore.AgentKindHuman, "tenant-a"),
		gatewayTenantDirectoryEntry("gangbeng", agentcore.AgentKindAI, "tenant-a"),
		gatewayTenantDirectoryEntry("jordan-belfort", agentcore.AgentKindAI, "tenant-a"),
	}})

	events, err := runner.RunTurn(ctx, directFeishuInput("jordan-belfort", "fs-a", "open-a", "hello"))
	if err != nil {
		t.Fatalf("RunTurn: %v", err)
	}
	_ = collectGatewayEvents(events)
	if len(discussions.cmds) != 1 || discussions.cmds[0].DirectTargetID != "jordan-belfort" {
		t.Fatalf("discussion commands = %+v", discussions.cmds)
	}
	if len(audit.events) != 0 {
		t.Fatalf("normal messages must not emit binding audits: %+v", audit.events)
	}
}

func TestProductionGatewayFeishuAgentBindingRejectsInvalidOwnership(t *testing.T) {
	for _, test := range []struct {
		name             string
		ownershipTenant  string
		ownershipAgent   string
		inboundEnabled   bool
		personalAgent    string
		withoutOwnership bool
	}{
		{name: "unowned", personalAgent: "jordan-belfort", withoutOwnership: true},
		{name: "inbound disabled", ownershipTenant: "tenant-a", ownershipAgent: "jordan-belfort", personalAgent: "jordan-belfort"},
		{name: "cross tenant", ownershipTenant: "tenant-b", ownershipAgent: "jordan-belfort", personalAgent: "jordan-belfort", inboundEnabled: true},
		{name: "personal owner mismatch", ownershipTenant: "tenant-a", ownershipAgent: "jordan-belfort", personalAgent: "gangbeng", inboundEnabled: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			database, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: t.Name()})
			if err != nil {
				t.Fatalf("open database: %v", err)
			}
			t.Cleanup(func() { _ = database.Close() })
			bindings := gwbindings.NewStore(database)
			if _, err := bindings.UpsertVerified(ctx, gwbindings.UpsertInput{
				TenantID: "tenant-a", ChannelKey: "feishu", AccountID: "jordan-belfort", ExternalTenantID: "fs-a",
				ExternalUserID: "open-a", HumanProfileID: "human-a", DefaultAgentID: test.personalAgent,
			}); err != nil {
				t.Fatalf("seed personal binding: %v", err)
			}
			if !test.withoutOwnership {
				if _, err := bindings.PutAgentChannelBinding(ctx, gwbindings.PutAgentChannelBindingInput{
					TenantID: test.ownershipTenant, ChannelKey: "feishu", AccountID: "jordan-belfort",
					AgentProfileID: test.ownershipAgent, InboundEnabled: test.inboundEnabled,
				}); err != nil {
					t.Fatalf("seed ownership: %v", err)
				}
			}
			discussions := &fakeProductionGatewayDiscussionService{}
			runner := newProductionMessageGatewayRunner(discussions)
			runner.SetBindingStore(bindings)
			runner.SetAgentDirectory(systemGatewayTestDirectory("human-a", "gangbeng", "jordan-belfort"))
			events, err := runner.RunTurn(ctx, directFeishuInput("jordan-belfort", "fs-a", "open-a", "hello"))
			if err != nil {
				t.Fatalf("RunTurn: %v", err)
			}
			got := collectGatewayEvents(events)
			if len(discussions.cmds) != 0 || len(got) != 2 || got[1].FinishReason != "unregistered" {
				t.Fatalf("events = %+v commands = %+v", got, discussions.cmds)
			}
		})
	}
}

func TestProductionGatewayFeishuBindingCodeRequiresCurrentOwner(t *testing.T) {
	ctx := context.Background()
	database, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: t.Name()})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	bindings := gwbindings.NewStore(database)
	code, err := bindings.CreateCode(ctx, gwbindings.CreateCodeInput{
		TenantID: "tenant-a", ChannelKey: "feishu", AccountID: "jordan-belfort", HumanProfileID: "human-a", DefaultAgentID: "gangbeng",
	})
	if err != nil {
		t.Fatalf("CreateCode: %v", err)
	}
	if _, err := bindings.PutAgentChannelBinding(ctx, gwbindings.PutAgentChannelBindingInput{
		TenantID: "tenant-a", ChannelKey: "feishu", AccountID: "jordan-belfort", AgentProfileID: "jordan-belfort", InboundEnabled: true,
	}); err != nil {
		t.Fatalf("seed ownership: %v", err)
	}
	runner := newProductionMessageGatewayRunner(&fakeProductionGatewayDiscussionService{})
	runner.SetBindingStore(bindings)
	audit := &recordingProductionGatewayAudit{}
	runner.SetAuditRecorder(audit)
	runner.SetAgentDirectory(systemGatewayTestDirectory("human-a", "gangbeng", "jordan-belfort"))
	events, err := runner.RunTurn(ctx, directFeishuInput("jordan-belfort", "fs-a", "open-a", "/绑定 "+code.Value))
	if err != nil {
		t.Fatalf("RunTurn: %v", err)
	}
	got := collectGatewayEvents(events)
	if len(got) != 2 || got[1].FinishReason != "binding_owner_mismatch" {
		t.Fatalf("binding events = %+v", got)
	}
	if _, err := bindings.Resolve(ctx, gwbindings.ExternalIdentity{ChannelKey: "feishu", AccountID: "jordan-belfort", ExternalTenantID: "fs-a", ExternalUserID: "open-a"}); !errors.Is(err, gwbindings.ErrNotFound) {
		t.Fatalf("mismatched code created a personal binding: %v", err)
	}
	if _, err := bindings.ConsumeCode(ctx, code.Value, gwbindings.ExternalIdentity{ChannelKey: "feishu", AccountID: "jordan-belfort", ExternalTenantID: "fs-a", ExternalUserID: "open-a"}); err != nil {
		t.Fatalf("mismatched owner consumed code: %v", err)
	}
	if len(audit.events) != 1 || audit.events[0].Result != "failed" || audit.events[0].Metadata["finish_reason"] != "binding_owner_mismatch" {
		t.Fatalf("binding rejection audit = %+v", audit.events)
	}
	encoded, err := json.Marshal(audit.events[0])
	if err != nil {
		t.Fatalf("marshal audit: %v", err)
	}
	if strings.Contains(string(encoded), code.Value) {
		t.Fatalf("binding code leaked into audit: %s", encoded)
	}
}

func directFeishuInput(accountID, externalTenantID, externalUserID, message string) gwprotocol.SubmitTurnInput {
	return gwprotocol.SubmitTurnInput{
		SessionKey: "feishu:direct", ChannelKey: gwprotocol.ChannelFeishu, Message: message,
		Identity: &gwprotocol.InboundIdentity{
			AccountID: accountID, ExternalTenantID: externalTenantID, ExternalUserID: externalUserID,
			ExternalChatID: "chat-a", ChatKind: "direct", Verified: true,
		},
	}
}
