package resourceadmin

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/platform/db"
	skillres "orrery_v2/internal/resource/skill"
	toolres "orrery_v2/internal/resource/tool"
)

func TestSkillRSICycleEvaluatesAndPromotesActuallyLoadedSnapshot(t *testing.T) {
	ctx := context.Background()
	database, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: "rsi-snapshot"})
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	packages := skillres.NewPackageStore(filepath.Join(t.TempDir(), "skills"))
	registry := skillres.NewPlatformRegistry(database)
	registry.SetPackageStore(packages)
	if err := registry.Register(ctx, skillres.SkillDefinition{ID: "skill.review", Name: "Review", Version: "1.0.0", Enabled: true, PromptAssets: []skillres.PromptAsset{{Path: "SKILL.md", Content: "OLD_POLICY"}}}); err != nil {
		t.Fatal(err)
	}
	service := NewCommandService(toolres.NewMemoryRegistry(), registry)
	service.SetSkillVersionEvaluationStore(NewMemorySkillVersionEvaluationStore())
	loader := skillres.NewRuntimeToolService(skillres.RuntimeToolServiceOptions{Registry: registry, Store: packages})
	candidate := skillres.SkillDefinition{ID: "skill.review", Name: "Review", Version: "2.0.0", Enabled: true, PromptAssets: []skillres.PromptAsset{{Path: "SKILL.md", Content: "NEW_POLICY"}}}
	viewer := ViewerRef{ViewerID: "rsi", PermissionScope: []string{PermissionResourceManage}}
	result, err := service.RunSkillRSICycle(ctx, viewer, "skill.review", testSkillVersionGenerator{candidate: candidate}, testSkillVersionEvaluator{evaluate: func(ctx context.Context, baseline, candidate skillres.SkillDefinition) SkillVersionEvaluation {
		if baseline.PackageSnapshot == nil || candidate.PackageSnapshot == nil {
			t.Fatal("evaluator received unfrozen definitions")
		}
		for _, expected := range []struct {
			definition skillres.SkillDefinition
			policy     string
		}{{baseline, "OLD_POLICY"}, {candidate, "NEW_POLICY"}} {
			loaded, err := loader.LoadSkill(ctx, skillres.RuntimeSkillRequest{SkillID: string(expected.definition.ID), SkillBindings: []agentcore.RuntimeSkillBinding{{SkillID: string(expected.definition.ID), Version: expected.definition.Version, PackageDigest: expected.definition.PackageSnapshot.Digest}}})
			if err != nil || !strings.Contains(loaded.Content, expected.policy) {
				t.Fatalf("evaluated runtime content = %+v, error = %v", loaded, err)
			}
		}
		return passingSkillVersionEvaluation("eval-runtime-snapshot", baseline, candidate)
	}}, SkillRSICycleRequest{PromoteOnPass: true})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != SkillRSICycleStatusPromoted {
		t.Fatalf("cycle status = %s", result.Status)
	}
	active, err := registry.Get(ctx, "skill.review")
	if err != nil {
		t.Fatal(err)
	}
	if active.PackageSnapshot == nil || result.Evaluation.CandidateDefinitionHash != skillres.DefinitionHash(active) {
		t.Fatal("active content differs from evaluated candidate")
	}
	loaded, err := loader.LoadSkill(ctx, skillres.RuntimeSkillRequest{SkillID: string(active.ID), SkillBindings: []agentcore.RuntimeSkillBinding{{SkillID: string(active.ID), Version: active.Version, PackageDigest: active.PackageSnapshot.Digest}}})
	if err != nil || !strings.Contains(loaded.Content, "NEW_POLICY") {
		t.Fatalf("promoted runtime content = %+v, error = %v", loaded, err)
	}
}
