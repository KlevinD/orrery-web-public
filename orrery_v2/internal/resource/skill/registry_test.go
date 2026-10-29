package skill

import (
	"context"
	"errors"
	"testing"

	agentcore "orrery_v2/internal/agent/core"
)

func TestRegistryRegistersSkillDefinitions(t *testing.T) {
	ctx := context.Background()
	registry := NewMemoryRegistry()
	definition := SkillDefinition{
		ID:           agentcore.SkillID("skill.code.review"),
		Name:         "code-review",
		Version:      "1.2.0",
		Description:  "Review code for behavioral regressions",
		DomainTags:   []string{"engineering"},
		TaskTypeTags: []string{"review"},
		PromptAssets: []PromptAsset{
			{Path: "SKILL.md", ContentRef: "artifact://skills/code-review/SKILL.md", Kind: PromptAssetMarkdown},
		},
		RequiredTools: []agentcore.ToolRef{{ToolID: agentcore.ToolID("tool.repo.read"), VersionConstraint: ">=1.0.0"}},
		EvidenceRefs:  []agentcore.EvidenceRef{{ID: "evidence.skill.source"}},
		Enabled:       true,
	}

	if err := registry.Register(ctx, definition); err != nil {
		t.Fatalf("register: %v", err)
	}
	got, err := registry.Get(ctx, definition.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Name != definition.Name || got.RequiredTools[0].ToolID != "tool.repo.read" {
		t.Fatalf("definition not preserved: %+v", got)
	}
	byTag, err := registry.ListByTag(ctx, "review")
	if err != nil {
		t.Fatalf("list by tag: %v", err)
	}
	if len(byTag) != 1 || byTag[0].ID != definition.ID {
		t.Fatalf("ListByTag(review) = %+v", byTag)
	}
}

func TestRegistryDisablePreventsResolution(t *testing.T) {
	ctx := context.Background()
	registry := NewMemoryRegistry()
	definition := SkillDefinition{
		ID:      agentcore.SkillID("skill.disabled"),
		Name:    "disabled",
		Version: "1.0.0",
		Enabled: true,
	}
	if err := registry.Register(ctx, definition); err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := registry.Disable(ctx, definition.ID); err != nil {
		t.Fatalf("disable: %v", err)
	}
	got, err := registry.Get(ctx, definition.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Enabled {
		t.Fatalf("expected disabled skill: %+v", got)
	}
}

func TestRegistryDeletesSkillDefinitions(t *testing.T) {
	ctx := context.Background()
	registry := NewMemoryRegistry()
	definition := SkillDefinition{
		ID:      agentcore.SkillID("skill.delete"),
		Name:    "Delete",
		Version: "1.0.0",
		Enabled: true,
	}
	if err := registry.Register(ctx, definition); err != nil {
		t.Fatalf("register: %v", err)
	}

	if err := registry.Delete(ctx, definition.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := registry.Get(ctx, definition.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("get after delete error = %v, want ErrNotFound", err)
	}
	listed, err := registry.List(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(listed) != 0 {
		t.Fatalf("listed after delete = %+v", listed)
	}
	if err := registry.Delete(ctx, definition.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second delete error = %v, want ErrNotFound", err)
	}
}

func TestVersionedRegistryKeepsCandidateSeparateUntilPromotion(t *testing.T) {
	ctx := context.Background()
	registry := NewMemoryRegistry()
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
	versions, err := registry.ListVersions(ctx, base.ID)
	if err != nil {
		t.Fatalf("list versions: %v", err)
	}
	if len(versions) != 2 || versions[0].Version != "1.0.0" || versions[1].Version != "2.0.0" {
		t.Fatalf("versions = %+v", versions)
	}
	if wasActive, err := registry.WasActiveVersion(ctx, base.ID, "2.0.0"); err != nil || wasActive {
		t.Fatalf("candidate activation history = %v, err=%v", wasActive, err)
	}
	if err := registry.RollbackVersion(ctx, base.ID, "2.0.0"); !errors.Is(err, ErrVersionMismatch) {
		t.Fatalf("candidate rollback error = %v, want ErrVersionMismatch", err)
	}
	if err := registry.PromoteVersion(ctx, base.ID, "2.0.0"); err != nil {
		t.Fatalf("promote: %v", err)
	}
	active, err = registry.Get(ctx, base.ID)
	if err != nil {
		t.Fatalf("get promoted active: %v", err)
	}
	if active.Version != "2.0.0" || active.Description != "candidate" {
		t.Fatalf("promoted active = %+v", active)
	}
}
