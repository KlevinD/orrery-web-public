package ontologyscope

import (
	"context"
	"errors"
	"testing"
	"time"

	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/migration"
)

func TestPlatformBindingStorePersistsVersionsAndAudit(t *testing.T) {
	ctx := context.Background()
	store, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: "ontology-scope-bindings"})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	if err := migration.NewRunner([]migration.Migration{scopeBindingMigration()}).Up(ctx, store); err != nil {
		t.Fatalf("migration: %v", err)
	}
	repository := NewPlatformBindingStore(store)
	now := time.Date(2026, 7, 11, 8, 0, 0, 0, time.UTC)
	repository.now = func() time.Time { return now }
	binding := testBinding("mission:1", KindMission, "mission", "1", "org-1")

	created, err := repository.SaveBinding(ctx, binding, 0, "principal-1")
	if err != nil {
		t.Fatalf("SaveBinding create: %v", err)
	}
	if created.BindingVersion != 1 || !created.CreatedAt.Equal(now) || !created.UpdatedAt.Equal(now) {
		t.Fatalf("created = %#v", created)
	}
	loaded, err := repository.GetBinding(ctx, binding.TenantID)
	if err != nil || loaded.SourceObject.ID != "1" || loaded.AuthorizationTenantID != "org-1" {
		t.Fatalf("GetBinding = %#v, %v", loaded, err)
	}

	noop, err := repository.SaveBinding(ctx, binding, 1, "principal-1")
	if err != nil || noop.BindingVersion != 1 {
		t.Fatalf("no-op SaveBinding = %#v, %v", noop, err)
	}
	binding.State = BindingStateDisabled
	now = now.Add(time.Minute)
	updated, err := repository.SaveBinding(ctx, binding, 1, "principal-2")
	if err != nil {
		t.Fatalf("SaveBinding update: %v", err)
	}
	if updated.BindingVersion != 2 || updated.State != BindingStateDisabled || !updated.UpdatedAt.Equal(now) {
		t.Fatalf("updated = %#v", updated)
	}

	var auditCount int
	if err := store.QueryRow(ctx, `SELECT COUNT(*) FROM ontology_scope_binding_audits WHERE tenant_id = ?`, binding.TenantID).Scan(&auditCount); err != nil {
		t.Fatalf("count audits: %v", err)
	}
	if auditCount != 2 {
		t.Fatalf("audit count = %d, want 2", auditCount)
	}
}

func TestPlatformBindingStoreEnforcesOptimisticVersionAndStableList(t *testing.T) {
	ctx := context.Background()
	store, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: "ontology-scope-list"})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	if err := migration.NewRunner([]migration.Migration{scopeBindingMigration()}).Up(ctx, store); err != nil {
		t.Fatalf("migration: %v", err)
	}
	repository := NewPlatformBindingStore(store)
	for _, tenantID := range []string{"mission:a", "mission:b", "mission:c"} {
		if _, err := repository.SaveBinding(ctx, testBinding(tenantID, KindMission, "mission", tenantID, "org-1"), 0, "principal"); err != nil {
			t.Fatalf("SaveBinding(%s): %v", tenantID, err)
		}
	}
	if _, err := repository.SaveBinding(ctx, testBinding("mission:a", KindMission, "mission", "changed", "org-1"), 0, "principal"); !errors.Is(err, ErrBindingConflict) {
		t.Fatalf("stale SaveBinding error = %v", err)
	}
	page, err := repository.ListBindings(ctx, BindingQuery{Kind: KindMission, State: BindingStateActive, AfterTenantID: "mission:a", Limit: 2})
	if err != nil {
		t.Fatalf("ListBindings: %v", err)
	}
	if len(page) != 2 || page[0].TenantID != "mission:b" || page[1].TenantID != "mission:c" {
		t.Fatalf("page = %#v", page)
	}
	if _, err := repository.GetBinding(ctx, "missing"); !errors.Is(err, ErrBindingNotFound) {
		t.Fatalf("GetBinding missing error = %v", err)
	}
}

func TestValidateBindingRejectsUnknownAndPartialRefs(t *testing.T) {
	binding := testBinding("mission:1", Kind("unknown"), "mission", "1", "org-1")
	if err := ValidateBinding(binding); !errors.Is(err, ErrInvalidBinding) {
		t.Fatalf("unknown kind error = %v", err)
	}
	binding.Kind = KindMission
	binding.LabelRef.Type = "project"
	if err := ValidateBinding(binding); !errors.Is(err, ErrInvalidBinding) {
		t.Fatalf("partial label ref error = %v", err)
	}
	binding = testBinding("mission:1", KindMission, "project", "1", "org-1")
	if err := ValidateBinding(binding); !errors.Is(err, ErrInvalidBinding) {
		t.Fatalf("unsafe kind/action mapping error = %v", err)
	}
}
