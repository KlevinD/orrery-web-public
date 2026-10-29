package skill

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
)

func TestRuntimeToolServiceListsAndLoadsOnlyBoundSkills(t *testing.T) {
	ctx := context.Background()
	registry := NewMemoryRegistry()
	store := NewPackageStore(filepath.Join(t.TempDir(), "skills"))
	writeRuntimeToolTestSkill(t, ctx, store, registry, "skill.tdd", "tdd", "Write tests first", map[string]string{
		"resources/checklist.md": "# Checklist\n- red\n- green\n",
	})
	writeRuntimeToolTestSkill(t, ctx, store, registry, "skill.unbound", "unbound", "Should not leak", map[string]string{
		"resources/secret.md": "hidden",
	})
	service := NewRuntimeToolService(RuntimeToolServiceOptions{
		Registry: registry,
		Store:    store,
	})
	bindings := []agentcore.RuntimeSkillBinding{{
		SkillID: "skill.tdd",
		Alias:   "test-first",
		Version: "1.0.0",
		Summary: "Write tests first",
	}}

	list, err := service.ListSkills(ctx, RuntimeSkillRequest{SkillBindings: bindings})
	if err != nil {
		t.Fatalf("ListSkills: %v", err)
	}
	if len(list.Skills) != 1 || list.Skills[0].SkillID != "skill.tdd" || list.Skills[0].Alias != "test-first" {
		t.Fatalf("skills = %+v", list.Skills)
	}

	loaded, err := service.LoadSkill(ctx, RuntimeSkillRequest{
		SkillBindings: bindings,
		SkillID:       "skill.tdd",
	})
	if err != nil {
		t.Fatalf("LoadSkill: %v", err)
	}
	if loaded.Skill.SkillID != "skill.tdd" || !strings.Contains(loaded.Content, "# tdd") {
		t.Fatalf("loaded skill = %+v", loaded)
	}
	stagedBindings := []agentcore.RuntimeSkillBinding{{SkillID: "skill.tdd", Alias: "tdd"}}
	staged, err := service.LoadSkill(ctx, RuntimeSkillRequest{
		SkillBindings: stagedBindings,
		SkillID:       "000-tdd-0123456789abcdef",
	})
	if err != nil || staged.SkillID != "skill.tdd" {
		t.Fatalf("staged Pi skill lookup = %+v, err=%v", staged, err)
	}

	if _, err := service.LoadSkill(ctx, RuntimeSkillRequest{SkillBindings: bindings, SkillID: "skill.unbound"}); err == nil {
		t.Fatalf("unbound skill should not load")
	}
}

func TestRuntimeToolServiceListsAndLoadsBoundSkillResources(t *testing.T) {
	ctx := context.Background()
	registry := NewMemoryRegistry()
	store := NewPackageStore(filepath.Join(t.TempDir(), "skills"))
	writeRuntimeToolTestSkill(t, ctx, store, registry, "skill.tdd", "tdd", "Write tests first", map[string]string{
		"resources/checklist.md": "# Checklist\n- red\n- green\n",
		"assets/logo.png":        "not preview text",
	})
	service := NewRuntimeToolService(RuntimeToolServiceOptions{
		Registry: registry,
		Store:    store,
	})
	bindings := []agentcore.RuntimeSkillBinding{{SkillID: "skill.tdd"}}

	resources, err := service.ListSkillResources(ctx, RuntimeSkillRequest{
		SkillBindings: bindings,
		SkillID:       "skill.tdd",
	})
	if err != nil {
		t.Fatalf("ListSkillResources: %v", err)
	}
	if resources.Total != 2 || !containsRuntimeResource(resources.Resources, "resources/checklist.md") {
		t.Fatalf("resources = %+v", resources)
	}

	loaded, err := service.LoadResource(ctx, RuntimeSkillRequest{
		SkillBindings: bindings,
		SkillID:       "skill.tdd",
		Path:          "resources/checklist.md",
	})
	if err != nil {
		t.Fatalf("LoadResource: %v", err)
	}
	if loaded.Path != "resources/checklist.md" || loaded.PreviewKind != string(PreviewKindMarkdown) || !strings.Contains(loaded.Content, "- red") {
		t.Fatalf("loaded resource = %+v", loaded)
	}

	if _, err := service.LoadResource(ctx, RuntimeSkillRequest{SkillBindings: bindings, SkillID: "skill.tdd", Path: "../outside.txt"}); err == nil {
		t.Fatalf("unsafe resource path should fail")
	}
}

func TestRuntimeToolServiceResolvesPiNamesWithoutAmbiguousOrUnboundSkills(t *testing.T) {
	ctx := context.Background()
	registry := NewMemoryRegistry()
	store := NewPackageStore(filepath.Join(t.TempDir(), "skills"))
	for _, id := range []string{"skill.web-research", "skill.other", "skill.unbound"} {
		writeRuntimeToolTestSkill(t, ctx, store, registry, id, "web-research", "Research", nil)
	}
	service := NewRuntimeToolService(RuntimeToolServiceOptions{Registry: registry, Store: store})
	bindings := []agentcore.RuntimeSkillBinding{{SkillID: "skill.web-research", Alias: "web-research"}}
	for _, name := range []string{"skill.web-research", "web-research", "skill-web-research", "000-web-research-3aec74b5bf3a30df"} {
		loaded, err := service.LoadSkill(ctx, RuntimeSkillRequest{SkillBindings: bindings, SkillID: name})
		if err != nil || loaded.SkillID != "skill.web-research" {
			t.Fatalf("LoadSkill(%q) = %+v, err=%v", name, loaded, err)
		}
	}
	for _, name := range []string{"skill.unbound", "001-unbound-3aec74b5bf3a30df", "000-web-research-not-a-digest"} {
		if _, err := service.LoadSkill(ctx, RuntimeSkillRequest{SkillBindings: bindings, SkillID: name}); err == nil {
			t.Fatalf("invalid or unbound name %q loaded", name)
		}
	}
	bindings = append(bindings, agentcore.RuntimeSkillBinding{SkillID: "skill.other", Alias: "web-research"})
	if _, err := service.LoadSkill(ctx, RuntimeSkillRequest{SkillBindings: bindings, SkillID: "000-web-research-3aec74b5bf3a30df"}); err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("ambiguous projected name error = %v", err)
	}
	if loaded, err := service.LoadSkill(ctx, RuntimeSkillRequest{SkillBindings: bindings, SkillID: "skill.web-research"}); err != nil || loaded.SkillID != "skill.web-research" {
		t.Fatalf("canonical ID must take precedence: loaded=%+v err=%v", loaded, err)
	}
	bindings = []agentcore.RuntimeSkillBinding{{SkillID: "skill.web-research", Alias: "网络检索"}}
	if loaded, err := service.LoadSkill(ctx, RuntimeSkillRequest{SkillBindings: bindings, SkillID: "网络检索"}); err != nil || loaded.SkillID != "skill.web-research" {
		t.Fatalf("exact alias must remain loadable: loaded=%+v err=%v", loaded, err)
	}
}

func writeRuntimeToolTestSkill(t *testing.T, ctx context.Context, store *PackageStore, registry Registry, id, name, description string, files map[string]string) {
	t.Helper()
	dir, err := store.SkillDirectory(agentcore.SkillID(id))
	if err != nil {
		t.Fatalf("SkillDirectory: %v", err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir skill: %v", err)
	}
	raw := "---\nname: " + name + "\ndescription: " + description + "\nversion: 1.0.0\n---\n# " + name + "\n\n" + description + "\n"
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(raw), 0o600); err != nil {
		t.Fatalf("write skill md: %v", err)
	}
	for rel, content := range files {
		path := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir file parent: %v", err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatalf("write file: %v", err)
		}
	}
	definition, err := HydrateFromDirectory(ctx, dir)
	if err != nil {
		t.Fatalf("hydrate skill: %v", err)
	}
	definition.ID = agentcore.SkillID(id)
	definition.Name = name
	definition.Description = description
	definition.CreatedAt = time.Unix(0, 0).UTC()
	definition.UpdatedAt = time.Unix(0, 0).UTC()
	if err := registry.Register(ctx, definition); err != nil {
		t.Fatalf("register skill: %v", err)
	}
}

func containsRuntimeResource(resources []RuntimeSkillResource, path string) bool {
	for _, resource := range resources {
		if resource.Path == path {
			return true
		}
	}
	return false
}
