package execenv

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
)

type openclawCLIStub struct {
	responses map[string]openclawResponse
	calls     []string
}

type openclawResponse struct {
	stdout string
	err    error
}

func (s *openclawCLIStub) exec(_ context.Context, _ string, args ...string) (string, error) {
	key := strings.Join(args, " ")
	s.calls = append(s.calls, key)
	resp, ok := s.responses[key]
	if !ok {
		return "", os.ErrNotExist
	}
	return resp.stdout, resp.err
}

func TestPrepareOpenClawWritesManagedWrapperConfigAndRuntimeEnv(t *testing.T) {
	root := t.TempDir()
	userConfigDir := t.TempDir()
	userConfigPath := filepath.Join(userConfigDir, "openclaw.json")
	if err := os.WriteFile(userConfigPath, []byte(`{"agents":{"defaults":{"workspace":"/host"}}}`), 0o600); err != nil {
		t.Fatalf("write user config: %v", err)
	}
	stub := &openclawCLIStub{responses: map[string]openclawResponse{
		"config file": {stdout: userConfigPath + "\n"},
	}}
	prev := openclawExec
	openclawExec = stub.exec
	t.Cleanup(func() { openclawExec = prev })

	env, err := Prepare(PrepareRequest{
		Root:       root,
		RunID:      "run-openclaw",
		AgentID:    "jordan-belfort",
		Runtime:    "openclaw",
		Context:    agentcore.ContextPack{WorkerPersonalGoal: "Use OpenClaw"},
		CleanupOld: true,
		MCPConfig:  json.RawMessage(`{"servers":{"noop":{"url":"http://127.0.0.1:8787/mcp"}}}`),
	})
	if err != nil {
		t.Fatalf("Prepare openclaw: %v", err)
	}
	if env.RuntimeEnv["OPENCLAW_CONFIG_PATH"] == "" {
		t.Fatalf("OPENCLAW_CONFIG_PATH missing: %#v", env.RuntimeEnv)
	}
	if !strings.Contains(env.RuntimeEnv["OPENCLAW_INCLUDE_ROOTS"], userConfigDir) {
		t.Fatalf("OPENCLAW_INCLUDE_ROOTS = %q, want %q", env.RuntimeEnv["OPENCLAW_INCLUDE_ROOTS"], userConfigDir)
	}
	if env.RuntimeEnv["ORRERY_OPENCLAW_AGENT_ID"] != "orrery-jordan-belfort" {
		t.Fatalf("managed OpenClaw agent id = %q", env.RuntimeEnv["ORRERY_OPENCLAW_AGENT_ID"])
	}
	raw, err := os.ReadFile(env.RuntimeEnv["OPENCLAW_CONFIG_PATH"])
	if err != nil {
		t.Fatalf("read wrapper: %v", err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatalf("parse wrapper: %v", err)
	}
	if cfg["$include"].([]any)[0] != userConfigPath {
		t.Fatalf("include = %#v, want %q", cfg["$include"], userConfigPath)
	}
	agents := cfg["agents"].(map[string]any)
	defaults := agents["defaults"].(map[string]any)
	if defaults["workspace"] != env.WorkDir {
		t.Fatalf("defaults.workspace = %v, want %q", defaults["workspace"], env.WorkDir)
	}
	list := agents["list"].([]any)
	if len(list) != 1 {
		t.Fatalf("managed wrapper should contain one task agent: %#v", list)
	}
	managed := list[0].(map[string]any)
	if managed["id"] != "orrery-jordan-belfort" || managed["workspace"] != env.WorkDir {
		t.Fatalf("managed task agent = %#v", managed)
	}
	if managed["agentDir"] != filepath.Join(env.RootDir, "openclaw-agent") {
		t.Fatalf("managed agentDir = %v", managed["agentDir"])
	}
	tools := managed["tools"].(map[string]any)
	if tools["fs"].(map[string]any)["workspaceOnly"] != true {
		t.Fatalf("managed task agent must be workspace-only: %#v", tools)
	}
	deny := tools["deny"].([]any)
	if strings.Join([]string{deny[0].(string), deny[1].(string)}, ",") != "sessions_spawn,subagents" {
		t.Fatalf("managed task agent deny list = %#v", deny)
	}
	if strings.Join(stub.calls, ",") != "config file" {
		t.Fatalf("global OpenClaw employees must not be queried: %#v", stub.calls)
	}
}

func TestPrepareOpenClawWritesManagedWrapperWithoutMCPConfig(t *testing.T) {
	root := t.TempDir()
	stub := &openclawCLIStub{responses: map[string]openclawResponse{
		"config file": {stdout: filepath.Join(root, "missing-openclaw.json") + "\n"},
	}}
	prev := openclawExec
	openclawExec = stub.exec
	t.Cleanup(func() { openclawExec = prev })

	env, err := Prepare(PrepareRequest{
		Root:    root,
		RunID:   "run-openclaw-no-mcp",
		AgentID: "tangdou",
		Runtime: "openclaw",
		Context: agentcore.ContextPack{WorkerPersonalGoal: "Use local OpenClaw"},
	})
	if err != nil {
		t.Fatalf("Prepare openclaw without MCP config: %v", err)
	}
	if env.RuntimeEnv["OPENCLAW_CONFIG_PATH"] == "" || env.RuntimeEnv["ORRERY_OPENCLAW_AGENT_ID"] != "orrery-tangdou" {
		t.Fatalf("managed runtime env missing: %#v", env.RuntimeEnv)
	}
}

func TestPrepareOpenClawConfigAllowsSlowCLIStartupByDefault(t *testing.T) {
	root := t.TempDir()
	var timeout time.Duration
	prev := openclawExec
	openclawExec = func(ctx context.Context, _ string, _ ...string) (string, error) {
		deadline, ok := ctx.Deadline()
		if !ok {
			t.Fatal("OpenClaw config command missing deadline")
		}
		timeout = time.Until(deadline)
		return filepath.Join(root, "missing-openclaw.json"), nil
	}
	t.Cleanup(func() { openclawExec = prev })

	if _, err := prepareOpenClawConfig(root, root, OpenClawConfigPrep{AgentID: "slow-agent"}); err != nil {
		t.Fatalf("prepare OpenClaw config: %v", err)
	}
	if timeout < 10*time.Second {
		t.Fatalf("default OpenClaw CLI timeout = %s, want at least 10s", timeout)
	}
}

func TestOpenClawActiveConfigPathExpandsOpenClawHome(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, ".openclaw", "openclaw.json")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o700); err != nil {
		t.Fatalf("create OpenClaw config dir: %v", err)
	}
	if err := os.WriteFile(configPath, []byte(`{}`), 0o600); err != nil {
		t.Fatalf("write OpenClaw config: %v", err)
	}
	t.Setenv("OPENCLAW_HOME", root)
	prev := openclawExec
	openclawExec = func(context.Context, string, ...string) (string, error) {
		return "$OPENCLAW_HOME/.openclaw/openclaw.json\n", nil
	}
	t.Cleanup(func() { openclawExec = prev })

	got, exists, err := openclawActiveConfigPath("openclaw", time.Second)
	if err != nil {
		t.Fatalf("resolve OpenClaw config path: %v", err)
	}
	if !exists || got != configPath {
		t.Fatalf("OpenClaw config path = %q exists=%v, want %q", got, exists, configPath)
	}
}

func TestOpenClawActiveConfigPathIgnoresDoctorWarningsBeforeFinalPath(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, "openclaw.json")
	if err := os.WriteFile(configPath, []byte(`{}`), 0o600); err != nil {
		t.Fatalf("write OpenClaw config: %v", err)
	}
	prev := openclawExec
	openclawExec = func(context.Context, string, ...string) (string, error) {
		return "Doctor warnings\npermission warning\n\n" + configPath + "\n", nil
	}
	t.Cleanup(func() { openclawExec = prev })

	got, exists, err := openclawActiveConfigPath("openclaw", time.Second)
	if err != nil {
		t.Fatalf("resolve OpenClaw config path: %v", err)
	}
	if !exists || got != configPath {
		t.Fatalf("OpenClaw config path = %q exists=%v, want %q", got, exists, configPath)
	}
}

func TestPrepareOpenClawUsesConfiguredBasePathWithoutReadingHomeConfig(t *testing.T) {
	root := t.TempDir()
	basePath := filepath.Join(t.TempDir(), "openclaw-base.json")
	if err := os.WriteFile(basePath, []byte(`{"agents":{"list":[]}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ORRERY_OPENCLAW_BASE_CONFIG_PATH", basePath)
	stub := &openclawCLIStub{responses: map[string]openclawResponse{
		"config get agents.list --json": {stdout: `[]`},
	}}
	previous := openclawExec
	openclawExec = stub.exec
	t.Cleanup(func() { openclawExec = previous })

	env, err := Prepare(PrepareRequest{
		Root: root, RunID: "run-configured-base", Runtime: "openclaw", AgentID: "agent-configured-base", CleanupOld: true,
		MCPConfig: json.RawMessage(`{"servers":{"orrery":{"url":"http://127.0.0.1:18080/mcp"}}}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(stub.calls) != 0 {
		t.Fatalf("openclaw calls = %v", stub.calls)
	}
	raw, err := os.ReadFile(env.RuntimeEnv["OPENCLAW_CONFIG_PATH"])
	if err != nil {
		t.Fatal(err)
	}
	var wrapper struct {
		Includes []string `json:"$include"`
	}
	if err := json.Unmarshal(raw, &wrapper); err != nil {
		t.Fatal(err)
	}
	if len(wrapper.Includes) != 1 || wrapper.Includes[0] != basePath {
		t.Fatalf("wrapper does not include configured base path: %s", raw)
	}
}
