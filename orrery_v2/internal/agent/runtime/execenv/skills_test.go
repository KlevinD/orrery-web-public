package execenv

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	agentcore "orrery_v2/internal/agent/core"
)

func TestPrepareWritesToolAndSkillSummaryFiles(t *testing.T) {
	env, err := Prepare(PrepareRequest{
		Root:    t.TempDir(),
		RunID:   "run-skills",
		Runtime: "claude",
		Context: richContextPack(),
		Tools: []agentcore.RuntimeToolBinding{
			{ToolID: "shell", Alias: "sh", Summary: "Run tests", GrantedPermissions: []string{"test"}, SecretEnv: map[string]string{"TOKEN": "secret/ref"}},
		},
		Skills: []agentcore.RuntimeSkillBinding{
			{SkillID: "tdd", Alias: "tdd", Version: "1.0.0", Summary: "Write tests first"},
		},
	})
	if err != nil {
		t.Fatalf("prepare execenv: %v", err)
	}

	tools := readFile(t, env.ToolsPath)
	for _, want := range []string{"shell", "Run tests", "test", "TOKEN=secret/ref"} {
		if !strings.Contains(tools, want) {
			t.Fatalf("tool summary missing %q:\n%s", want, tools)
		}
	}
	skills := readFile(t, env.SkillsPath)
	for _, want := range []string{"tdd", "1.0.0", "Write tests first"} {
		if !strings.Contains(skills, want) {
			t.Fatalf("skill summary missing %q:\n%s", want, skills)
		}
	}
}

func TestPrepareProjectsBoundSkillsForPiNativeSkillLoading(t *testing.T) {
	env, err := Prepare(PrepareRequest{
		Root:    t.TempDir(),
		RunID:   "run-pi-skills",
		Runtime: "pi",
		Skills: []agentcore.RuntimeSkillBinding{
			{SkillID: "skill.tdd", Alias: "TDD", Version: "1.0.0", Summary: "Write tests first"},
			{SkillID: "skill.frontend-design", Summary: "Design polished frontend UI"},
		},
	})
	if err != nil {
		t.Fatalf("prepare execenv: %v", err)
	}
	pathsValue := env.RuntimeEnv["ORRERY_PI_SKILL_PATHS"]
	if pathsValue == "" {
		t.Fatalf("ORRERY_PI_SKILL_PATHS missing: %#v", env.RuntimeEnv)
	}
	paths := strings.Split(pathsValue, string(os.PathListSeparator))
	if len(paths) != 2 {
		t.Fatalf("pi skill paths = %#v", paths)
	}
	firstSkill := readFile(t, filepath.Join(paths[0], "SKILL.md"))
	for _, want := range []string{"name: tdd", `description: "Write tests first"`, "skill.tdd", "load_skill"} {
		if !strings.Contains(firstSkill, want) {
			t.Fatalf("pi skill missing %q:\n%s", want, firstSkill)
		}
	}
}
