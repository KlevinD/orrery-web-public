package skill

import (
	"context"
	"errors"
	"testing"

	agentcore "orrery_v2/internal/agent/core"
)

func TestMemoryBundleRegistryPersistsOrderedUniqueSkillReferences(t *testing.T) {
	ctx := context.Background()
	registry := NewMemoryBundleRegistry()
	bundle := SkillBundle{
		ID:          "skill-bundle-review",
		Name:        "Review pack",
		Description: "Shared review workflow",
		SkillIDs: []agentcore.SkillID{
			"skill.review",
			"skill.review",
			"skill.test",
		},
		CreatedBy: "admin",
	}

	if err := registry.Create(ctx, bundle); err != nil {
		t.Fatalf("Create: %v", err)
	}
	got, err := registry.Get(ctx, bundle.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(got.SkillIDs) != 2 || got.SkillIDs[0] != "skill.review" || got.SkillIDs[1] != "skill.test" {
		t.Fatalf("skill ids = %+v", got.SkillIDs)
	}

	got.SkillIDs[0] = "skill.mutated"
	again, err := registry.Get(ctx, bundle.ID)
	if err != nil {
		t.Fatalf("Get again: %v", err)
	}
	if again.SkillIDs[0] != "skill.review" {
		t.Fatalf("registry leaked mutable slice: %+v", again.SkillIDs)
	}

	again.Description = "Updated"
	again.SkillIDs = append(again.SkillIDs, "skill.deploy")
	if err := registry.Update(ctx, again); err != nil {
		t.Fatalf("Update: %v", err)
	}
	listed, err := registry.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(listed) != 1 || listed[0].Description != "Updated" || len(listed[0].SkillIDs) != 3 {
		t.Fatalf("listed = %+v", listed)
	}

	if err := registry.Delete(ctx, bundle.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := registry.Get(ctx, bundle.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get deleted error = %v, want ErrNotFound", err)
	}
}

func TestMemoryBundleRegistryValidatesIdentityAndName(t *testing.T) {
	registry := NewMemoryBundleRegistry()
	for _, bundle := range []SkillBundle{
		{Name: "Missing ID"},
		{ID: "skill-bundle-missing-name"},
	} {
		if err := registry.Create(context.Background(), bundle); !errors.Is(err, ErrInvalidDefinition) {
			t.Fatalf("Create(%+v) error = %v, want ErrInvalidDefinition", bundle, err)
		}
	}
}
