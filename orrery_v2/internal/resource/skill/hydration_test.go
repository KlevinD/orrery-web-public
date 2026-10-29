package skill

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	agentcore "orrery_v2/internal/agent/core"
)

func TestHydrateFromSkillMarkdownDirectory(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	skillDir := filepath.Join(root, "skills", "code-review")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	body := `---
name: code-review
description: Review code changes for bugs
version: 1.3.0
domain_tags:
  - engineering
task_type_tags:
  - review
required_tools:
  - tool_id: tool.repo.read
    version_constraint: ">=1.0.0"
evidence_refs:
  - skill-source:code-review
---

# Code Review

Focus on correctness and regressions.
`
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(body), 0o600); err != nil {
		t.Fatalf("write skill: %v", err)
	}

	definition, err := HydrateFromDirectory(ctx, skillDir)
	if err != nil {
		t.Fatalf("HydrateFromDirectory: %v", err)
	}
	if definition.ID != agentcore.SkillID("skill.code-review") {
		t.Fatalf("skill id = %q", definition.ID)
	}
	if definition.Version != "1.3.0" || definition.Description != "Review code changes for bugs" {
		t.Fatalf("frontmatter not parsed: %+v", definition)
	}
	if len(definition.RequiredTools) != 1 || definition.RequiredTools[0].ToolID != "tool.repo.read" {
		t.Fatalf("required tools = %+v", definition.RequiredTools)
	}
	if len(definition.PromptAssets) != 1 || definition.PromptAssets[0].Path != "SKILL.md" {
		t.Fatalf("prompt assets = %+v", definition.PromptAssets)
	}
	if !definition.Enabled {
		t.Fatalf("hydrated skills should be enabled by default")
	}
}

func TestHydrateFromSkillMarkdownPreservesExplicitSkillID(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	skillDir := filepath.Join(root, "skills", "social-ads-special")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	body := `---
skill_id: skill.social-ads.专项技能
name: 社交广告专项技能
description: 社交广告投放策略技能
version: 2.0.0
---

# 社交广告专项技能

保留导入包中的 skill_id，不按 name 重新推导。
`
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(body), 0o600); err != nil {
		t.Fatalf("write skill: %v", err)
	}

	definition, err := HydrateFromDirectory(ctx, skillDir)
	if err != nil {
		t.Fatalf("HydrateFromDirectory: %v", err)
	}
	if definition.ID != agentcore.SkillID("skill.social-ads.专项技能") {
		t.Fatalf("skill id = %q, want explicit frontmatter id", definition.ID)
	}
}

func TestSeedLoaderRegistersAllSkillDirectories(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	writeSkill(t, filepath.Join(root, "alpha"), "alpha", "Alpha skill")
	writeSkill(t, filepath.Join(root, "nested", "beta"), "beta", "Beta skill")
	registry := NewMemoryRegistry()

	loaded, err := LoadDirectorySeeds(ctx, registry, root)
	if err != nil {
		t.Fatalf("LoadDirectorySeeds: %v", err)
	}
	if loaded != 2 {
		t.Fatalf("loaded = %d, want 2", loaded)
	}
	if _, err := registry.Get(ctx, agentcore.SkillID("skill.alpha")); err != nil {
		t.Fatalf("alpha not registered: %v", err)
	}
	if _, err := registry.Get(ctx, agentcore.SkillID("skill.beta")); err != nil {
		t.Fatalf("beta not registered: %v", err)
	}
}

func writeSkill(t *testing.T, dir, name, description string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	body := "---\nname: " + name + "\ndescription: " + description + "\n---\n# " + name + "\n"
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(body), 0o600); err != nil {
		t.Fatalf("write skill %s: %v", name, err)
	}
}
