package binding

import (
	"context"
	"errors"
	"testing"
)

func TestAgentChannelBindingCreateResolveMoveAndDelete(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	created, err := store.PutAgentChannelBinding(ctx, PutAgentChannelBindingInput{
		TenantID: "tenant-a", ChannelKey: "feishu", AccountID: "jordan-belfort",
		AgentProfileID: "jordan-belfort", InboundEnabled: true,
	})
	if err != nil {
		t.Fatalf("PutAgentChannelBinding create: %v", err)
	}
	if created.Revision != 1 || !created.InboundEnabled || created.AgentProfileID != "jordan-belfort" {
		t.Fatalf("created binding = %+v", created)
	}
	resolved, err := store.ResolveAgentChannelBinding(ctx, "feishu", "jordan-belfort")
	if err != nil || resolved != created {
		t.Fatalf("ResolveAgentChannelBinding = %+v, %v", resolved, err)
	}
	listed, err := store.ListAgentChannelBindings(ctx, "tenant-a", "jordan-belfort")
	if err != nil || len(listed) != 1 || listed[0] != created {
		t.Fatalf("ListAgentChannelBindings = %+v, %v", listed, err)
	}

	moved, err := store.PutAgentChannelBinding(ctx, PutAgentChannelBindingInput{
		TenantID: "tenant-a", ChannelKey: "feishu", AccountID: "jordan-belfort",
		AgentProfileID: "sales-agent", InboundEnabled: false,
		ExpectedRevision: created.Revision, ExpectedAgentProfileID: created.AgentProfileID,
	})
	if err != nil {
		t.Fatalf("PutAgentChannelBinding move: %v", err)
	}
	if moved.Revision != 2 || moved.InboundEnabled || moved.AgentProfileID != "sales-agent" || moved.CreatedAt != created.CreatedAt {
		t.Fatalf("moved binding = %+v", moved)
	}
	if err := store.DeleteOwnedAgentChannelBinding(ctx, "tenant-a", "feishu", "jordan-belfort", "sales-agent", moved.Revision); err != nil {
		t.Fatalf("DeleteOwnedAgentChannelBinding: %v", err)
	}
	if _, err := store.ResolveAgentChannelBinding(ctx, "feishu", "jordan-belfort"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Resolve after delete error = %v", err)
	}
}

func TestAgentChannelBindingRejectsOwnershipAndRevisionConflicts(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	created, err := store.PutAgentChannelBinding(ctx, PutAgentChannelBindingInput{
		TenantID: "tenant-a", ChannelKey: "feishu", AccountID: "jordan-belfort",
		AgentProfileID: "jordan-belfort", InboundEnabled: true,
	})
	if err != nil {
		t.Fatalf("seed binding: %v", err)
	}
	for _, input := range []PutAgentChannelBindingInput{
		{
			TenantID: "tenant-b", ChannelKey: "feishu", AccountID: "jordan-belfort",
			AgentProfileID: "other-agent", InboundEnabled: true,
		},
		{
			TenantID: "tenant-a", ChannelKey: "feishu", AccountID: "jordan-belfort",
			AgentProfileID: "other-agent", InboundEnabled: true,
			ExpectedRevision: created.Revision + 1, ExpectedAgentProfileID: created.AgentProfileID,
		},
		{
			TenantID: "tenant-a", ChannelKey: "feishu", AccountID: "jordan-belfort",
			AgentProfileID: "other-agent", InboundEnabled: true,
			ExpectedRevision: created.Revision, ExpectedAgentProfileID: "wrong-owner",
		},
	} {
		if _, err := store.PutAgentChannelBinding(ctx, input); !errors.Is(err, ErrConflict) {
			t.Fatalf("PutAgentChannelBinding(%+v) error = %v", input, err)
		}
	}
	if err := store.DeleteOwnedAgentChannelBinding(ctx, "tenant-a", "feishu", "jordan-belfort", created.AgentProfileID, created.Revision+1); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale DeleteOwnedAgentChannelBinding error = %v", err)
	}
}

func TestAgentChannelBindingListsTenantAndValidatesInput(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	for _, accountID := range []string{"jordan-belfort", "main"} {
		if _, err := store.PutAgentChannelBinding(ctx, PutAgentChannelBindingInput{
			TenantID: "tenant-a", ChannelKey: "feishu", AccountID: accountID,
			AgentProfileID: accountID, InboundEnabled: true,
		}); err != nil {
			t.Fatalf("seed %s: %v", accountID, err)
		}
	}
	bindings, err := store.ListTenantAgentChannelBindings(ctx, "tenant-a")
	if err != nil || len(bindings) != 2 || bindings[0].AccountID != "jordan-belfort" || bindings[1].AccountID != "main" {
		t.Fatalf("ListTenantAgentChannelBindings = %+v, %v", bindings, err)
	}
	for _, input := range []PutAgentChannelBindingInput{
		{TenantID: "", ChannelKey: "feishu", AccountID: "main", AgentProfileID: "gangbeng"},
		{TenantID: "tenant-a", ChannelKey: "dingtalk", AccountID: "main", AgentProfileID: "gangbeng"},
		{TenantID: "tenant-a", ChannelKey: "feishu", AccountID: "", AgentProfileID: "gangbeng"},
		{TenantID: "tenant-a", ChannelKey: "feishu", AccountID: "main", AgentProfileID: ""},
		{TenantID: "tenant-a", ChannelKey: "feishu", AccountID: "main", AgentProfileID: "gangbeng", ExpectedRevision: -1},
	} {
		if _, err := store.PutAgentChannelBinding(ctx, input); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("PutAgentChannelBinding(%+v) error = %v", input, err)
		}
	}
}
