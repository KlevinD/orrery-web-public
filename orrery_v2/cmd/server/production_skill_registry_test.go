package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/platform/db"
	skillres "orrery_v2/internal/resource/skill"
)

func TestProductionSkillRegistryFreezesLegacyPackagesAndLoadsBoundSnapshot(t *testing.T) {
	ctx := context.Background()
	core, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: "production-skill-snapshot"})
	if err != nil {
		t.Fatal(err)
	}
	defer core.Close()
	legacy := skillres.NewPlatformRegistry(core)
	if err := legacy.Register(ctx, skillres.SkillDefinition{ID: "skill.production-snapshot", Name: "Production snapshot", Version: "1.0.0", Enabled: true, PromptAssets: []skillres.PromptAsset{{Path: "SKILL.md", Content: "FROZEN_POLICY"}}}); err != nil {
		t.Fatal(err)
	}
	_, registry, _, packages, err := newProductionResourceRegistries(ctx, core, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	definition, err := registry.Get(ctx, "skill.production-snapshot")
	if err != nil || definition.PackageSnapshot == nil {
		t.Fatalf("production startup left a mutable skill: %v", err)
	}
	loader := skillres.NewRuntimeToolService(skillres.RuntimeToolServiceOptions{Registry: registry, Store: packages})
	loaded, err := loader.LoadSkill(ctx, skillres.RuntimeSkillRequest{SkillID: string(definition.ID), SkillBindings: []agentcore.RuntimeSkillBinding{{SkillID: string(definition.ID), Version: definition.Version, PackageDigest: definition.PackageSnapshot.Digest}}})
	if err != nil || !strings.Contains(loaded.Content, "FROZEN_POLICY") {
		t.Fatalf("production runtime content = %+v, error = %v", loaded, err)
	}
}

func TestProductionSkillRegistryMigrationHonorsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	core, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: "production-skill-canceled"})
	if err != nil {
		t.Fatal(err)
	}
	defer core.Close()
	cancel()
	if _, _, _, _, err := newProductionResourceRegistries(ctx, core, t.TempDir()); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled migration error = %v", err)
	}
}
