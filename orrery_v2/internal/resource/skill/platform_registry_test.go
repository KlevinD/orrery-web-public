package skill

import (
	"context"
	"errors"
	"testing"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/platform/db"
)

func TestPlatformRegistryPersistsSkillDefinitions(t *testing.T) {
	ctx := context.Background()
	store, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: "skill-registry-test"})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	registry := NewPlatformRegistry(store)
	definition := SkillDefinition{
		ID:           agentcore.SkillID("skill.persisted"),
		Name:         "persisted",
		Version:      "2.3.4",
		Description:  "Persisted skill",
		TaskTypeTags: []string{"implementation"},
		PromptAssets: []PromptAsset{
			{Path: "SKILL.md", ContentRef: "artifact://skill.persisted/SKILL.md", Kind: PromptAssetMarkdown},
		},
		Enabled: true,
	}
	if err := registry.Register(ctx, definition); err != nil {
		t.Fatalf("register: %v", err)
	}

	reopened := NewPlatformRegistry(store)
	got, err := reopened.Get(ctx, definition.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.ID != definition.ID || got.PromptAssets[0].ContentRef != "artifact://skill.persisted/SKILL.md" {
		t.Fatalf("definition not persisted faithfully: %+v", got)
	}
	listed, err := reopened.ListByTag(ctx, "implementation")
	if err != nil {
		t.Fatalf("list by tag: %v", err)
	}
	if len(listed) != 1 || listed[0].ID != definition.ID {
		t.Fatalf("listed = %+v", listed)
	}

	if err := reopened.Delete(ctx, definition.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := registry.Get(ctx, definition.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("get deleted definition error = %v, want ErrNotFound", err)
	}
	deleted, err := registry.IsDeleted(ctx, definition.ID)
	if err != nil {
		t.Fatalf("is deleted: %v", err)
	}
	if !deleted {
		t.Fatalf("deleted definition should keep registry tombstone")
	}
	if listed, err := registry.List(ctx); err != nil {
		t.Fatalf("list after delete: %v", err)
	} else if len(listed) != 0 {
		t.Fatalf("deleted definition should be hidden from list: %+v", listed)
	}
	if err := registry.Register(ctx, definition); err != nil {
		t.Fatalf("explicit re-register after delete: %v", err)
	}
	deleted, err = registry.IsDeleted(ctx, definition.ID)
	if err != nil {
		t.Fatalf("is deleted after re-register: %v", err)
	}
	if deleted {
		t.Fatalf("explicit re-register should clear registry tombstone")
	}
}

func TestPlatformRegistryUpdatesSkillDefinitions(t *testing.T) {
	ctx := context.Background()
	store, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: "skill-registry-update-test"})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	registry := NewPlatformRegistry(store)
	definition := SkillDefinition{
		ID:           agentcore.SkillID("skill.update"),
		Name:         "update",
		Version:      "1.0.0",
		Description:  "Initial skill",
		TaskTypeTags: []string{"old"},
		PromptAssets: []PromptAsset{
			{Path: "SKILL.md", ContentRef: "artifact://skill.update/v1/SKILL.md", Kind: PromptAssetMarkdown},
		},
		Enabled: true,
	}
	if err := registry.Register(ctx, definition); err != nil {
		t.Fatalf("register: %v", err)
	}

	updated := definition.Clone()
	updated.Version = "2.0.0"
	updated.Description = "Updated skill"
	updated.TaskTypeTags = []string{"current"}
	updated.PromptAssets[0].ContentRef = "artifact://skill.update/v2/SKILL.md"
	if err := registry.UpdateDefinition(ctx, updated); err != nil {
		t.Fatalf("update: %v", err)
	}

	reopened := NewPlatformRegistry(store)
	got, err := reopened.Get(ctx, definition.ID)
	if err != nil {
		t.Fatalf("get updated: %v", err)
	}
	if got.Version != "2.0.0" || got.Description != "Updated skill" || got.PromptAssets[0].ContentRef != "artifact://skill.update/v2/SKILL.md" {
		t.Fatalf("updated definition not persisted: %+v", got)
	}
	oldTag, err := reopened.ListByTag(ctx, "old")
	if err != nil {
		t.Fatalf("list old tag: %v", err)
	}
	if len(oldTag) != 0 {
		t.Fatalf("old tag should not retain updated skill: %+v", oldTag)
	}
	currentTag, err := reopened.ListByTag(ctx, "current")
	if err != nil {
		t.Fatalf("list current tag: %v", err)
	}
	if len(currentTag) != 1 || currentTag[0].ID != definition.ID {
		t.Fatalf("current tag should include updated skill: %+v", currentTag)
	}
}

func TestPlatformRegistryKeepsCandidateVersionsAndSupportsRollback(t *testing.T) {
	ctx := context.Background()
	store, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: "skill-registry-version-test"})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	registry := NewPlatformRegistry(store)
	base := SkillDefinition{ID: "skill.versioned", Name: "versioned", Version: "1.0.0", Description: "base", Enabled: true}
	candidate := base.Clone()
	candidate.Version = "2.0.0"
	candidate.Description = "candidate"
	if err := registry.Register(ctx, base); err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := registry.RegisterVersion(ctx, candidate); err != nil {
		t.Fatalf("register version: %v", err)
	}
	active, err := registry.Get(ctx, base.ID)
	if err != nil {
		t.Fatalf("get active: %v", err)
	}
	if active.Version != "1.0.0" {
		t.Fatalf("active version = %s, want 1.0.0", active.Version)
	}
	stored, err := registry.GetVersion(ctx, base.ID, "2.0.0")
	if err != nil {
		t.Fatalf("get candidate version: %v", err)
	}
	if stored.Description != "candidate" {
		t.Fatalf("candidate = %+v", stored)
	}
	if wasActive, err := registry.WasActiveVersion(ctx, base.ID, "2.0.0"); err != nil || wasActive {
		t.Fatalf("candidate activation history = %v, err=%v; candidate must remain rollback-ineligible", wasActive, err)
	}
	if err := registry.RollbackVersion(ctx, base.ID, "2.0.0"); !errors.Is(err, ErrVersionMismatch) {
		t.Fatalf("candidate rollback error = %v, want ErrVersionMismatch", err)
	}
	if err := registry.PromoteVersion(ctx, base.ID, "2.0.0"); err != nil {
		t.Fatalf("promote: %v", err)
	}
	if active, err = registry.Get(ctx, base.ID); err != nil || active.Version != "2.0.0" {
		t.Fatalf("promoted active = %+v, err=%v", active, err)
	}
	if wasActive, err := registry.WasActiveVersion(ctx, base.ID, "2.0.0"); err != nil || !wasActive {
		t.Fatalf("promoted activation history = %v, err=%v", wasActive, err)
	}
	if err := registry.RollbackVersion(ctx, base.ID, "1.0.0"); err != nil {
		t.Fatalf("rollback: %v", err)
	}
	if active, err = registry.Get(ctx, base.ID); err != nil || active.Version != "1.0.0" {
		t.Fatalf("rolled back active = %+v, err=%v", active, err)
	}
}
