package skill

import (
	"context"
	"errors"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/platform/db"
)

func TestPlatformBundleRegistryPersistsBundles(t *testing.T) {
	ctx := context.Background()
	store, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: "skill-bundle-registry-test"})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	createdAt := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	bundle := SkillBundle{
		ID:          "skill-bundle-persisted",
		Name:        "Persisted pack",
		Description: "Stored in platform records",
		SkillIDs:    []agentcore.SkillID{"skill.review", "skill.test"},
		CreatedBy:   "admin",
		CreatedAt:   createdAt,
		UpdatedAt:   createdAt,
	}
	registry := NewPlatformBundleRegistry(store)
	if err := registry.Create(ctx, bundle); err != nil {
		t.Fatalf("Create: %v", err)
	}

	reopened := NewPlatformBundleRegistry(store)
	got, err := reopened.Get(ctx, bundle.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Name != bundle.Name || len(got.SkillIDs) != 2 || !got.CreatedAt.Equal(createdAt) {
		t.Fatalf("persisted bundle = %+v", got)
	}

	got.Name = "Renamed pack"
	got.SkillIDs = []agentcore.SkillID{"skill.review"}
	if err := reopened.Update(ctx, got); err != nil {
		t.Fatalf("Update: %v", err)
	}
	listed, err := registry.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(listed) != 1 || listed[0].Name != "Renamed pack" || len(listed[0].SkillIDs) != 1 {
		t.Fatalf("listed = %+v", listed)
	}

	if err := reopened.Delete(ctx, bundle.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := registry.Get(ctx, bundle.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get deleted error = %v, want ErrNotFound", err)
	}
}
