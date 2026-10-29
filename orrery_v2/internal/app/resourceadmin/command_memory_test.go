package resourceadmin

import (
	"context"
	"errors"
	"testing"

	agentcore "orrery_v2/internal/agent/core"
	memcore "orrery_v2/internal/memory/core"
	memtestkit "orrery_v2/internal/memory/testkit"
	skillres "orrery_v2/internal/resource/skill"
	toolres "orrery_v2/internal/resource/tool"
)

func TestQueryServiceUpdatesMutableMemoryProfileSettings(t *testing.T) {
	ctx := context.Background()
	store := memtestkit.NewFakeStore()
	original := memcore.MemoryProfile{
		ID:                "memory-agent-1",
		OwnerRef:          memcore.OwnerRef{AgentID: "agent-1", OrganizationID: "org-1"},
		MemoryKind:        agentcore.MemoryKindPersonal,
		DefaultScope:      memcore.MemoryScope{Kind: memcore.ScopeAgent, ID: "agent-1"},
		RetrievalPolicy:   memcore.RetrievalPolicy{MaxRecords: 10, Tags: []string{"old"}},
		CompressionPolicy: memcore.CompressionPolicy{MaxSummaryBytes: 1024},
		VisibilityPolicy:  memcore.VisibilityPolicy{Mode: memcore.VisibilityOwnerOnly},
		Enabled:           true,
	}
	if err := store.SaveProfile(ctx, original); err != nil {
		t.Fatalf("SaveProfile: %v", err)
	}
	service := NewQueryServiceWithMemoryProfiles(toolres.NewMemoryRegistry(), skillres.NewMemoryRegistry(), store)

	updated, err := service.UpdateMemoryProfile(ctx, ViewerRef{
		ViewerID: "admin", PermissionScope: []string{PermissionResourceManage},
	}, string(original.ID), UpdateMemoryProfileRequest{
		Enabled: false, MaxRecords: 25, MaxSummaryBytes: 4096, RetrievalTags: []string{" policy ", "policy", "decision"},
	})
	if err != nil {
		t.Fatalf("UpdateMemoryProfile: %v", err)
	}
	if updated.Enabled || updated.RetrievalPolicy.MaxRecords != 25 || updated.CompressionPolicy.MaxSummaryBytes != 4096 {
		t.Fatalf("updated profile = %+v", updated)
	}
	if len(updated.RetrievalPolicy.Tags) != 2 || updated.RetrievalPolicy.Tags[0] != "policy" || updated.RetrievalPolicy.Tags[1] != "decision" {
		t.Fatalf("updated tags = %#v", updated.RetrievalPolicy.Tags)
	}
	persisted, err := store.GetProfile(ctx, original.ID)
	if err != nil {
		t.Fatalf("GetProfile: %v", err)
	}
	if persisted.OwnerRef != original.OwnerRef || persisted.DefaultScope != original.DefaultScope || persisted.MemoryKind != original.MemoryKind {
		t.Fatalf("immutable identity changed: %+v", persisted)
	}
}

func TestQueryServiceRejectsMemoryProfileUpdateWithoutManagePermission(t *testing.T) {
	service := NewQueryServiceWithMemoryProfiles(toolres.NewMemoryRegistry(), skillres.NewMemoryRegistry(), memtestkit.NewFakeStore())
	_, err := service.UpdateMemoryProfile(context.Background(), ViewerRef{
		ViewerID: "reader", PermissionScope: []string{PermissionResourceRead},
	}, "memory-agent-1", UpdateMemoryProfileRequest{MaxRecords: 1, MaxSummaryBytes: 1})
	if !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("error = %v, want permission denied", err)
	}
}
