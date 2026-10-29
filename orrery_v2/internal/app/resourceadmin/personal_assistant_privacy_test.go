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

func TestQueryServiceHidesPersonalAssistantMemoryProfiles(t *testing.T) {
	ctx := context.Background()
	store := memtestkit.NewFakeStore()
	assistantID := "A009991"
	if err := agentcore.RegisterPersonalAssistantProfileID("tenant-resource-admin", "owner-resource-admin", assistantID); err != nil {
		t.Fatal(err)
	}
	profileID := agentcore.MemoryProfileID("memory-" + assistantID)
	if err := store.SaveProfile(ctx, memcore.MemoryProfile{
		ID: profileID, OwnerRef: memcore.OwnerRef{AgentID: agentcore.AgentID(assistantID)},
		MemoryKind: agentcore.MemoryKindPersonal, DefaultScope: memcore.MemoryScope{Kind: memcore.ScopeAgent, ID: assistantID},
		RetrievalPolicy: memcore.RetrievalPolicy{MaxRecords: 20}, CompressionPolicy: memcore.CompressionPolicy{MaxSummaryBytes: 4096}, Enabled: true,
	}); err != nil {
		t.Fatalf("save personal assistant memory profile: %v", err)
	}
	service := NewQueryServiceWithMemoryProfiles(toolres.NewMemoryRegistry(), skillres.NewMemoryRegistry(), store)
	viewer := ViewerRef{ViewerID: "admin", PermissionScope: []string{PermissionResourceRead, PermissionResourceManage}}

	view, err := service.ListResourcesFiltered(ctx, viewer, ListResourcesRequest{Kind: "memory_profile", IncludeUsage: true})
	if err != nil {
		t.Fatalf("ListResourcesFiltered: %v", err)
	}
	if view.Page.Total != 0 || len(view.MemoryProfiles) != 0 || len(view.Usage) != 0 {
		t.Fatalf("personal assistant memory leaked in resource list: %+v", view)
	}
	if _, err := service.GetMemoryProfile(ctx, viewer, string(profileID)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetMemoryProfile error = %v, want ErrNotFound", err)
	}
	if _, err := service.UpdateMemoryProfile(ctx, viewer, string(profileID), UpdateMemoryProfileRequest{
		Enabled: true, MaxRecords: 20, MaxSummaryBytes: 4096,
	}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("UpdateMemoryProfile error = %v, want ErrNotFound", err)
	}
}
