package datapolicy

import (
	"context"
	"testing"
	"time"

	"orrery_v2/internal/app/contextgraph"
)

func TestMemoryStoreDecidesPurposeAndFieldPolicies(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	now := time.Unix(500, 0).UTC()
	if err := store.SavePurposePolicy(ctx, PurposePolicy{
		ID:         "purpose-hide-task",
		TenantID:   "tenant-1",
		Consumer:   "iris",
		Purpose:    "mission_assignment",
		ObjectType: "task",
		Effect:     EffectDeny,
		Reason:     "tasks are hidden for this purpose",
		Enabled:    true,
		CreatedAt:  now,
		UpdatedAt:  now,
	}); err != nil {
		t.Fatalf("SavePurposePolicy: %v", err)
	}
	if err := store.SaveFieldPolicy(ctx, FieldPolicy{
		ID:         "field-mask-secret",
		TenantID:   "tenant-1",
		Consumer:   "iris",
		Purpose:    "mission_assignment",
		ObjectType: "mission",
		FieldPath:  "secret_notes",
		Effect:     EffectMask,
		Reason:     "secret notes are not needed",
		Enabled:    true,
		CreatedAt:  now,
		UpdatedAt:  now,
	}); err != nil {
		t.Fatalf("SaveFieldPolicy: %v", err)
	}
	if revision, err := store.PolicyRevision(ctx); err != nil || revision != 2 {
		t.Fatalf("policy revision = %d, err=%v", revision, err)
	}

	decision, err := store.DecideContext(ctx, contextgraph.BuildContextBundleRequest{
		TenantID: "tenant-1",
		Consumer: "iris",
		Purpose:  "mission_assignment",
	}, []contextgraph.Object{
		{ID: "mission-1", ObjectType: "mission"},
		{ID: "task-1", ObjectType: "task"},
	})
	if err != nil {
		t.Fatalf("DecideContext: %v", err)
	}
	if !containsString(decision.HiddenObjectIDs, "task-1") {
		t.Fatalf("hidden objects = %+v, want task-1", decision.HiddenObjectIDs)
	}
	if got := decision.MaskedFields["mission-1"]; len(got) != 1 || got[0] != "secret_notes" {
		t.Fatalf("masked fields = %+v", decision.MaskedFields)
	}
	if len(decision.Constraints) != 2 {
		t.Fatalf("constraints = %+v, want purpose and field constraints", decision.Constraints)
	}
}
