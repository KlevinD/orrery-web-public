package skill

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	agentcore "orrery_v2/internal/agent/core"
)

func TestPiProjectedSkillNameKeepsHistoricalSnapshotBinding(t *testing.T) {
	ctx := context.Background()
	store := NewPackageStore(filepath.Join(t.TempDir(), "skills"))
	registry := NewMemoryRegistry()
	writeRuntimeToolTestSkill(t, ctx, store, registry, "skill.web-research", "web-research", "OLD_POLICY", nil)
	base, err := registry.Get(ctx, "skill.web-research")
	if err != nil {
		t.Fatal(err)
	}
	base, err = store.FreezeDefinition(ctx, base)
	if err != nil {
		t.Fatal(err)
	}
	if err := registry.UpdateDefinition(ctx, base); err != nil {
		t.Fatal(err)
	}
	candidate := base.Clone()
	candidate.Version = "2.0.0"
	candidate.PromptAssets[0].Content = "NEW_POLICY"
	candidate, err = store.FreezeDefinition(ctx, candidate)
	if err != nil {
		t.Fatal(err)
	}
	if err := registry.RegisterVersion(ctx, candidate); err != nil {
		t.Fatal(err)
	}
	if err := registry.PromoteVersion(ctx, base.ID, candidate.Version); err != nil {
		t.Fatal(err)
	}
	service := NewRuntimeToolService(RuntimeToolServiceOptions{Registry: registry, Store: store})
	req := RuntimeSkillRequest{SkillID: "000-web-research-3aec74b5bf3a30df", SkillBindings: []agentcore.RuntimeSkillBinding{{SkillID: string(base.ID), Alias: "web-research", Version: base.Version, PackageDigest: base.PackageSnapshot.Digest}}}
	loaded, err := service.LoadSkill(ctx, req)
	if err != nil || loaded.Skill.Version != base.Version || !strings.Contains(loaded.Content, "OLD_POLICY") || strings.Contains(loaded.Content, "NEW_POLICY") {
		t.Fatalf("projected name lost its historical binding: %+v, %v", loaded, err)
	}
	req.SkillBindings[0].PackageDigest = strings.Repeat("0", 64)
	if _, err := service.LoadSkill(ctx, req); !errors.Is(err, ErrVersionMismatch) {
		t.Fatalf("projected name accepted a changed package: %v", err)
	}
}
