package execenv

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	agentcore "orrery_v2/internal/agent/core"
)

func TestPrepareWritesContextPackAndSkillSummaries(t *testing.T) {
	root := t.TempDir()
	env, err := Prepare(PrepareRequest{
		Root:       root,
		RunID:      "run-1",
		Runtime:    "codex",
		Context:    agentcore.ContextPack{MissionFinalGoal: "Ship it", DiscussionGroupGoal: "Coordinate", WorkerPersonalGoal: "Build runtime"},
		Skills:     []agentcore.RuntimeSkillBinding{{SkillID: "skill-1", Summary: "Use TDD"}},
		Tools:      []agentcore.RuntimeToolBinding{{ToolID: "tool-1", Summary: "Run shell"}},
		CleanupOld: true,
	})
	if err != nil {
		t.Fatalf("prepare execenv: %v", err)
	}
	if env.WorkDir == "" || env.RuntimeHome == "" {
		t.Fatalf("expected workdir and runtime home: %#v", env)
	}
	data, err := os.ReadFile(env.ContextPath)
	if err != nil {
		t.Fatalf("read context file: %v", err)
	}
	text := string(data)
	for _, want := range []string{"Ship it", "Coordinate", "Build runtime", "Use TDD", "Run shell"} {
		if !strings.Contains(text, want) {
			t.Fatalf("context file missing %q:\n%s", want, text)
		}
	}
	if filepath.Dir(env.ContextPath) != env.SystemDir || filepath.Dir(env.ToolsPath) != env.SystemDir || filepath.Dir(env.SkillsPath) != env.SystemDir {
		t.Fatalf("runtime context files must stay in the system directory: %#v", env)
	}
	for _, name := range []string{"AGENT_CONTEXT.md", "AGENTS.md", "TOOL_SUMMARY.md", "SKILLS.md"} {
		if _, err := os.Stat(filepath.Join(env.WorkDir, name)); !os.IsNotExist(err) {
			t.Fatalf("system file leaked into user workdir: %s (err=%v)", name, err)
		}
	}
}

func TestPrepareIsolatesPiContextFilesFromRequestedWorkDir(t *testing.T) {
	workDir := t.TempDir()
	for name, content := range map[string]string{
		"AGENT_CONTEXT.md": "# Agent Runtime Context\n\nlegacy context",
		"TOOL_SUMMARY.md":  "# Tool Summary\n\nlegacy tools",
		"SKILLS.md":        "# Skills\n\nlegacy skills",
		"AGENTS.md":        "# Project Instructions\n\nkeep this user file",
	} {
		if err := os.WriteFile(filepath.Join(workDir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	env, err := Prepare(PrepareRequest{
		Root:    filepath.Join(workDir, ".orrery", "execenv"),
		RunID:   "run-pi",
		Runtime: "pi",
		WorkDir: workDir,
	})
	if err != nil {
		t.Fatalf("prepare execenv: %v", err)
	}

	wantSystemDir := filepath.Join(workDir, ".orrery", "execenv", "run-pi", "system")
	if env.SystemDir != wantSystemDir {
		t.Fatalf("system dir = %q, want %q", env.SystemDir, wantSystemDir)
	}
	for key, want := range map[string]string{
		"ORRERY_AGENT_CONTEXT_PATH": env.ContextPath,
		"ORRERY_PI_AGENTS_PATH":     filepath.Join(env.SystemDir, "AGENTS.md"),
		"ORRERY_TOOL_SUMMARY_PATH":  env.ToolsPath,
		"ORRERY_SKILLS_PATH":        env.SkillsPath,
	} {
		if got := env.RuntimeEnv[key]; got != want {
			t.Fatalf("%s = %q, want %q", key, got, want)
		}
		if _, err := os.Stat(want); err != nil {
			t.Fatalf("expected isolated system file %s: %v", want, err)
		}
	}
	for _, name := range []string{"AGENT_CONTEXT.md", "TOOL_SUMMARY.md", "SKILLS.md"} {
		if _, err := os.Stat(filepath.Join(workDir, name)); !os.IsNotExist(err) {
			t.Fatalf("system file leaked into requested workdir: %s (err=%v)", name, err)
		}
	}
	if got := readFile(t, filepath.Join(workDir, "AGENTS.md")); got != "# Project Instructions\n\nkeep this user file" {
		t.Fatalf("user-owned AGENTS.md was changed: %q", got)
	}
}

func TestPrepareCopiesCodexAuthenticationIntoRunScopedHome(t *testing.T) {
	authPath := filepath.Join(t.TempDir(), "codex-auth.json")
	auth := `{"auth_mode":"apikey","OPENAI_API_KEY":"test-key"}`
	if err := os.WriteFile(authPath, []byte(auth), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ORRERY_CODEX_AUTH_FILE", authPath)

	env, err := Prepare(PrepareRequest{
		Root:    t.TempDir(),
		RunID:   "run-codex-auth",
		Runtime: "codex",
	})
	if err != nil {
		t.Fatalf("prepare execenv: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(env.RuntimeHome, "auth.json"))
	if err != nil {
		t.Fatalf("read run-scoped Codex auth: %v", err)
	}
	if string(got) != auth {
		t.Fatalf("run-scoped Codex auth = %q, want source contents", got)
	}
}

func TestPrepareRejectsInvalidExplicitCodexAuthentication(t *testing.T) {
	authPath := filepath.Join(t.TempDir(), "codex-auth.json")
	if err := os.WriteFile(authPath, []byte("not-json"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ORRERY_CODEX_AUTH_FILE", authPath)

	_, err := Prepare(PrepareRequest{
		Root:    t.TempDir(),
		RunID:   "run-invalid-codex-auth",
		Runtime: "codex",
	})
	if err == nil || !strings.Contains(err.Error(), "invalid JSON object") {
		t.Fatalf("prepare error = %v, want invalid Codex auth error", err)
	}
}

func TestPrepareWritesSandboxProfileToMetadata(t *testing.T) {
	env, err := Prepare(PrepareRequest{
		Root:    t.TempDir(),
		RunID:   "run-sandbox",
		Runtime: "codex",
		SandboxProfile: agentcore.RuntimeSandboxProfile{
			Mode:                 agentcore.RuntimeSandboxWorkspaceWrite,
			Approval:             agentcore.RuntimeApprovalOnRequest,
			Network:              agentcore.RuntimeNetworkDisabled,
			RuntimeHomeIsolation: agentcore.RuntimeHomeIsolatedPerRun,
			Retention:            agentcore.RuntimeRetentionKeepOnFailure,
			Root:                 "D:/workspace",
			WorkDir:              "D:/workspace/missions/m/tasks/t/runs/r",
			WritableRoots:        []string{"D:/workspace"},
			EnforceBoundary:      true,
		},
	})
	if err != nil {
		t.Fatalf("prepare execenv: %v", err)
	}
	var meta map[string]any
	if err := json.Unmarshal([]byte(readFile(t, env.MetadataPath)), &meta); err != nil {
		t.Fatalf("decode metadata: %v", err)
	}
	profile, ok := meta["sandbox_profile"].(map[string]any)
	if !ok {
		t.Fatalf("sandbox profile missing from metadata: %#v", meta)
	}
	if profile["mode"] != string(agentcore.RuntimeSandboxWorkspaceWrite) ||
		profile["approval"] != string(agentcore.RuntimeApprovalOnRequest) ||
		profile["network"] != string(agentcore.RuntimeNetworkDisabled) ||
		profile["retention"] != string(agentcore.RuntimeRetentionKeepOnFailure) {
		t.Fatalf("sandbox profile = %#v", profile)
	}
}
