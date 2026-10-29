package skill

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"gopkg.in/yaml.v3"
	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/platform/db"
)

func TestRuntimeLoadsExactSnapshotAcrossPromotionAndRollback(t *testing.T) {
	for _, platform := range []bool{false, true} {
		name := "memory"
		if platform {
			name = "platform"
		}
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			store := NewPackageStore(filepath.Join(t.TempDir(), "skills"))
			seed := NewMemoryRegistry()
			writeRuntimeToolTestSkill(t, ctx, store, seed, "skill.snapshot", "snapshot", "OLD_POLICY", map[string]string{"resources/checklist.md": "OLD_CHECKLIST"})
			baseline, err := seed.Get(ctx, "skill.snapshot")
			if err != nil {
				t.Fatal(err)
			}
			baseline, err = store.FreezeDefinition(ctx, baseline)
			if err != nil {
				t.Fatal(err)
			}
			var registry VersionedRegistry = NewMemoryRegistry()
			if platform {
				database, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: "snapshot"})
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { database.Close() })
				persistent := NewPlatformRegistry(database)
				persistent.SetPackageStore(store)
				registry = persistent
			}
			if err := registry.Register(ctx, baseline); err != nil {
				t.Fatal(err)
			}
			baseline, err = registry.Get(ctx, baseline.ID)
			if err != nil {
				t.Fatal(err)
			}
			candidate := baseline.Clone()
			candidate.Version = "2.0.0"
			candidate.PromptAssets[0].Content = "NEW_POLICY"
			candidate.PromptAssets = append(candidate.PromptAssets, PromptAsset{Path: "resources/checklist.md", Content: "NEW_CHECKLIST", Kind: PromptAssetMarkdown}, PromptAsset{Path: "scripts/check.py", Content: "print('verified')", Kind: PromptAssetText})
			candidate, err = store.FreezeDefinition(ctx, candidate)
			if err != nil {
				t.Fatal(err)
			}
			if candidate.PackageSnapshot.Digest == baseline.PackageSnapshot.Digest {
				t.Fatal("changed package kept baseline digest")
			}
			if err := registry.RegisterVersion(ctx, candidate); err != nil {
				t.Fatal(err)
			}
			candidate, err = registry.GetVersion(ctx, candidate.ID, candidate.Version)
			if err != nil {
				t.Fatal(err)
			}
			service := NewRuntimeToolService(RuntimeToolServiceOptions{Registry: registry, Store: store})
			request := func(definition SkillDefinition) RuntimeSkillRequest {
				return RuntimeSkillRequest{SkillID: string(definition.ID), SkillBindings: []agentcore.RuntimeSkillBinding{{SkillID: string(definition.ID), Version: definition.Version, PackageDigest: definition.PackageSnapshot.Digest}}}
			}
			assertContent := func(definition SkillDefinition, policy, checklist string) {
				t.Helper()
				req := request(definition)
				loaded, err := service.LoadSkill(ctx, req)
				if err != nil {
					t.Fatal(err)
				}
				if loaded.Skill.Version != definition.Version || loaded.Skill.PackageDigest != definition.PackageSnapshot.Digest || !strings.Contains(loaded.Content, policy) {
					t.Fatalf("wrong loaded skill: %+v", loaded)
				}
				req.Path = "resources/checklist.md"
				resource, err := service.LoadResource(ctx, req)
				if err != nil {
					t.Fatal(err)
				}
				if resource.Content != checklist {
					t.Fatalf("resource = %q", resource.Content)
				}
			}
			assertContent(candidate, "NEW_POLICY", "NEW_CHECKLIST")
			assertContent(baseline, "OLD_POLICY", "OLD_CHECKLIST")
			active, _ := registry.Get(ctx, baseline.ID)
			if active.Version != baseline.Version {
				t.Fatal("candidate registration changed active")
			}
			resources, err := service.ListSkillResources(ctx, request(candidate))
			if err != nil || resources.Total != 2 || !containsRuntimeResource(resources.Resources, "scripts/check.py") {
				t.Fatalf("candidate resource list = %+v, %v", resources, err)
			}
			if err := registry.PromoteVersion(ctx, baseline.ID, candidate.Version); err != nil {
				t.Fatal(err)
			}
			assertContent(candidate, "NEW_POLICY", "NEW_CHECKLIST")
			// An already bound run must retain its old package after active changes.
			assertContent(baseline, "OLD_POLICY", "OLD_CHECKLIST")
			if err := registry.RollbackVersion(ctx, baseline.ID, baseline.Version); err != nil {
				t.Fatal(err)
			}
			assertContent(baseline, "OLD_POLICY", "OLD_CHECKLIST")
			assertContent(candidate, "NEW_POLICY", "NEW_CHECKLIST")
			// Editable catalog files cannot change either bound version's bytes.
			dir, _ := store.SkillDirectory(baseline.ID)
			if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("UNREVIEWED_POLICY"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "resources", "checklist.md"), []byte("UNREVIEWED_CHECKLIST"), 0o600); err != nil {
				t.Fatal(err)
			}
			assertContent(baseline, "OLD_POLICY", "OLD_CHECKLIST")
			assertContent(candidate, "NEW_POLICY", "NEW_CHECKLIST")
			bad := request(candidate)
			bad.SkillBindings[0].PackageDigest = baseline.PackageSnapshot.Digest
			if _, err := service.LoadSkill(ctx, bad); !errors.Is(err, ErrVersionMismatch) {
				t.Fatalf("wrong binding digest error: %v", err)
			}
			bad.SkillBindings[0].Version = "missing"
			if _, err := service.LoadSkill(ctx, bad); !errors.Is(err, ErrNotFound) {
				t.Fatalf("missing exact version error: %v", err)
			}
			bad = request(candidate)
			bad.SkillBindings = nil
			if _, err := service.LoadSkill(ctx, bad); !errors.Is(err, ErrNotFound) {
				t.Fatalf("unbound candidate error: %v", err)
			}
		})
	}
}

func TestSnapshotPreservesMetadataAndConcurrentPublicationIsIdempotent(t *testing.T) {
	ctx := context.Background()
	store := NewPackageStore(filepath.Join(t.TempDir(), "skills"))
	definition := SkillDefinition{ID: "skill.metadata", Name: "Metadata", Version: "2.0.0", Enabled: true, PromptAssets: []PromptAsset{{Path: "SKILL.md", Content: "---\nname: old\nversion: 1.0.0\nprovider_config:\n  retries: 3\nallowed-tools: [Read, Bash]\n---\n\nDO_THE_WORK\n"}}}
	const writers = 8
	definitions := make([]SkillDefinition, writers)
	writeErrors := make([]error, writers)
	var wait sync.WaitGroup
	for i := range definitions {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			definitions[index], writeErrors[index] = store.FreezeDefinition(ctx, definition)
		}(i)
	}
	wait.Wait()
	for i, err := range writeErrors {
		if err != nil {
			t.Fatalf("writer %d: %v", i, err)
		}
		if definitions[i].PackageSnapshot.Digest != definitions[0].PackageSnapshot.Digest {
			t.Fatal("identical packages have different identities")
		}
	}
	frozen := definitions[0]
	raw, err := store.ReadSnapshotFile(ctx, frozen, "SKILL.md")
	if err != nil {
		t.Fatal(err)
	}
	header, body, present := snapshotMarkdownParts(string(raw))
	metadata := map[string]any{}
	if err := yaml.Unmarshal([]byte(header), &metadata); err != nil {
		t.Fatal(err)
	}
	if !present || metadata["version"] != "2.0.0" || metadata["name"] != "Metadata" || metadata["provider_config"] == nil || metadata["allowed-tools"] == nil || !strings.Contains(body, "DO_THE_WORK") {
		t.Fatalf("frontmatter changed executable metadata: %s", raw)
	}
	again, err := store.FreezeDefinition(ctx, frozen)
	if err != nil || DefinitionHash(frozen) != DefinitionHash(again) {
		t.Fatalf("freezing changes the evaluated identity: %v", err)
	}
	for _, content := range []string{"---\nnull\n---\nbody", "---\n---\nbody"} {
		definition.PromptAssets[0].Content = content
		if _, err := store.FreezeDefinition(ctx, definition); err != nil {
			t.Fatalf("empty frontmatter failed: %v", err)
		}
	}
	definition.PromptAssets[0].Content = "---\nname: missing terminator\nbody"
	if _, err := store.FreezeDefinition(ctx, definition); !errors.Is(err, ErrHydration) {
		t.Fatalf("malformed frontmatter accepted: %v", err)
	}
}

func TestLegacySnapshotMigrationAndCandidateSurviveDatabaseRestart(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	config := db.Config{Driver: db.DriverFile, URL: "file://" + filepath.Join(root, "registry.db")}
	database, err := db.Open(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	store := NewPackageStore(filepath.Join(root, "skills"))
	seed := NewMemoryRegistry()
	writeRuntimeToolTestSkill(t, ctx, store, seed, "skill.restart", "restart", "BASELINE", map[string]string{"resources/policy.txt": "BASELINE_RESOURCE"})
	baseline, err := seed.Get(ctx, "skill.restart")
	if err != nil {
		t.Fatal(err)
	}
	registry := NewPlatformRegistry(database)
	// Simulate the pre-version catalog with no history rows.
	if err := registry.put(ctx, baseline); err != nil {
		t.Fatal(err)
	}
	registry.SetPackageStore(store)
	if err := registry.FreezeLegacyPackages(ctx); err != nil {
		t.Fatal(err)
	}
	baseline, err = registry.GetVersion(ctx, baseline.ID, baseline.Version)
	if err != nil || baseline.PackageSnapshot == nil {
		t.Fatalf("legacy baseline was not preserved: %v", err)
	}
	baselineHash := DefinitionHash(baseline)
	if err := registry.FreezeLegacyPackages(ctx); err != nil {
		t.Fatal(err)
	}
	dir, _ := store.SkillDirectory(baseline.ID)
	if err := os.WriteFile(filepath.Join(dir, "resources", "policy.txt"), []byte("UNREVIEWED_RESOURCE"), 0o600); err != nil {
		t.Fatal(err)
	}
	// The public candidate API does not require the caller to supply a manifest.
	candidate := SkillDefinition{ID: baseline.ID, Name: baseline.Name, Version: "2.0.0", Enabled: true, PromptAssets: []PromptAsset{{Path: "SKILL.md", Content: "CANDIDATE"}}}
	if err := registry.RegisterVersion(ctx, candidate); err != nil {
		t.Fatal(err)
	}
	candidate, err = registry.GetVersion(ctx, candidate.ID, candidate.Version)
	if err != nil {
		t.Fatal(err)
	}
	resource, err := store.ReadSnapshotFile(ctx, candidate, "resources/policy.txt")
	if err != nil || string(resource) != "BASELINE_RESOURCE" {
		t.Fatalf("candidate inherited editable content: %q, %v", resource, err)
	}
	if err := registry.PromoteVersion(ctx, baseline.ID, candidate.Version); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	database, err = db.Open(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	registry = NewPlatformRegistry(database)
	store = NewPackageStore(filepath.Join(root, "skills"))
	registry.SetPackageStore(store)
	if err := registry.FreezeLegacyPackages(ctx); err != nil {
		t.Fatal(err)
	}
	active, err := registry.Get(ctx, baseline.ID)
	if err != nil || DefinitionHash(active) != DefinitionHash(candidate) {
		t.Fatalf("restart changed the active package: %v", err)
	}
	loader := NewRuntimeToolService(RuntimeToolServiceOptions{Registry: registry, Store: store})
	for _, expected := range []struct {
		definition SkillDefinition
		content    string
	}{{baseline, "BASELINE"}, {candidate, "CANDIDATE"}} {
		loaded, err := loader.LoadSkill(ctx, RuntimeSkillRequest{SkillID: string(baseline.ID), SkillBindings: []agentcore.RuntimeSkillBinding{{SkillID: string(baseline.ID), Version: expected.definition.Version, PackageDigest: expected.definition.PackageSnapshot.Digest}}})
		if err != nil || !strings.Contains(loaded.Content, expected.content) {
			t.Fatalf("restarted runtime loaded wrong package: %+v, %v", loaded, err)
		}
	}
	if err := registry.RollbackVersion(ctx, baseline.ID, baseline.Version); err != nil {
		t.Fatal(err)
	}
	active, err = registry.Get(ctx, baseline.ID)
	if err != nil || DefinitionHash(active) != baselineHash {
		t.Fatalf("rollback changed migrated baseline: %v", err)
	}
}

func TestSnapshotTamperingAndMissingFilesFailClosed(t *testing.T) {
	ctx := context.Background()
	store := NewPackageStore(filepath.Join(t.TempDir(), "skills"))
	definition, err := store.FreezeDefinition(ctx, SkillDefinition{ID: "skill.integrity", Name: "integrity", Version: "1.0.0", Enabled: true, PromptAssets: []PromptAsset{{Path: "SKILL.md", Content: "REVIEWED"}, {Path: "resources/result.txt", Content: "CORRECT"}}})
	if err != nil {
		t.Fatal(err)
	}
	dir, _ := store.snapshotDirectory(definition.PackageSnapshot)
	name := filepath.Join(dir, "files", "resources", "result.txt")
	if err := os.Chmod(name, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(name, []byte("INCORRECT"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ReadSnapshotFile(ctx, definition, "resources/result.txt"); !errors.Is(err, ErrHydration) {
		t.Fatalf("tampered content error = %v", err)
	}
	if _, err := store.ReadSnapshotFile(ctx, definition, "../outside"); err == nil {
		t.Fatal("path traversal accepted")
	}
	if err := os.Remove(filepath.Join(dir, "files", "SKILL.md")); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ReadSnapshotFile(ctx, definition, "SKILL.md"); !errors.Is(err, ErrHydration) {
		t.Fatalf("missing snapshot error = %v", err)
	}
	cloned := definition.Clone()
	cloned.PackageSnapshot.Files[0].SHA256 = strings.Repeat("0", 64)
	if definition.PackageSnapshot.Files[0].SHA256 == cloned.PackageSnapshot.Files[0].SHA256 {
		t.Fatal("Clone shares mutable manifest")
	}
	if _, err := store.ReadSnapshotFile(ctx, cloned, "SKILL.md"); !errors.Is(err, ErrHydration) {
		t.Fatalf("altered manifest error = %v", err)
	}
}

func TestSnapshotLimitsCancellationAndLegacyDrift(t *testing.T) {
	ctx := context.Background()
	store := NewPackageStoreWithLimits(filepath.Join(t.TempDir(), "small"), PackageLimits{MaxFiles: 1, MaxFileBytes: 1024, MaxTotalBytes: 1024})
	definition := SkillDefinition{ID: "skill.limits", Name: "limits", Version: "1.0.0", Enabled: true, PromptAssets: []PromptAsset{{Path: "SKILL.md", Content: "body"}, {Path: "extra.txt", Content: "extra"}}}
	if _, err := store.FreezeDefinition(ctx, definition); !errors.Is(err, ErrHydration) {
		t.Fatalf("file count limit error = %v", err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := store.FreezeDefinition(canceled, definition); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled error = %v", err)
	}
	legacyStore := NewPackageStore(filepath.Join(t.TempDir(), "legacy"))
	registry := NewMemoryRegistry()
	writeRuntimeToolTestSkill(t, ctx, legacyStore, registry, "skill.legacy-drift", "legacy-drift", "original", nil)
	dir, _ := legacyStore.SkillDirectory("skill.legacy-drift")
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("unreviewed"), 0o600); err != nil {
		t.Fatal(err)
	}
	service := NewRuntimeToolService(RuntimeToolServiceOptions{Registry: registry, Store: legacyStore})
	if _, err := service.LoadSkill(ctx, RuntimeSkillRequest{SkillID: "skill.legacy-drift", SkillBindings: []agentcore.RuntimeSkillBinding{{SkillID: "skill.legacy-drift", Version: "1.0.0"}}}); !errors.Is(err, ErrHydration) {
		t.Fatalf("legacy drift error = %v", err)
	}
}
