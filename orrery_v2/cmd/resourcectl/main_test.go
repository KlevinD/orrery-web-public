package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/platform/db"
	skillres "orrery_v2/internal/resource/skill"
	toolres "orrery_v2/internal/resource/tool"
)

func TestCLIRegistersMCPConfig(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	dbPath := filepath.Join(root, "resources.db")
	configPath := filepath.Join(root, "mcp.json")
	if err := os.WriteFile(configPath, []byte(`{"mcpServers":{"github":{"command":"github-mcp-server","env":{"GITHUB_TOKEN":"secret"}}}}`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	var out bytes.Buffer
	err := run(ctx, []string{"register-mcp", "--db", "file://" + filepath.ToSlash(dbPath), "--config", configPath}, &out)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(out.String(), "registered 1 mcp server") {
		t.Fatalf("output = %q", out.String())
	}
	store, err := db.Open(ctx, db.Config{Driver: db.DriverFile, URL: "file://" + filepath.ToSlash(dbPath), Name: "resourcectl-test"})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer store.Close()
	definition, err := toolres.NewPlatformRegistry(store).Get(ctx, agentcore.ToolID("mcp.github"))
	if err != nil {
		t.Fatalf("get mcp: %v", err)
	}
	if definition.SecretRefs[0].Name == "secret" {
		t.Fatalf("secret value leaked: %+v", definition.SecretRefs)
	}
}

func TestCLILoadsSkillDirectory(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	dbPath := filepath.Join(root, "resources.db")
	skillDir := filepath.Join(root, "skills", "planner")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: planner\n---\n# Planner\n"), 0o600); err != nil {
		t.Fatalf("write skill: %v", err)
	}
	var out bytes.Buffer
	err := run(ctx, []string{"load-skills", "--db", "file://" + filepath.ToSlash(dbPath), "--root", filepath.Join(root, "skills")}, &out)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(out.String(), "registered 1 skill") {
		t.Fatalf("output = %q", out.String())
	}
	store, err := db.Open(ctx, db.Config{Driver: db.DriverFile, URL: "file://" + filepath.ToSlash(dbPath), Name: "resourcectl-skill-test"})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer store.Close()
	if _, err := skillres.NewPlatformRegistry(store).Get(ctx, agentcore.SkillID("skill.planner")); err != nil {
		t.Fatalf("get skill: %v", err)
	}
}
