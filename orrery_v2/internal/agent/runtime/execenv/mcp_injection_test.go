package execenv

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	agentcore "orrery_v2/internal/agent/core"
)

const testMCPConfig = `{
  "servers": {
    "orrery": {
      "transport": "streamable-http",
      "url": "http://127.0.0.1:8787/mcp",
      "headers": {
        "Authorization": "Bearer run-token",
        "X-Orrery-Run": "run-mcp"
      }
    }
  }
}`

func TestPrepareCodexInjectsMCPIntoRunScopedConfig(t *testing.T) {
	env, err := Prepare(PrepareRequest{
		Root:       t.TempDir(),
		RunID:      "run-mcp",
		Runtime:    "codex",
		Context:    agentcore.ContextPack{WorkerPersonalGoal: "Use Codex"},
		CleanupOld: true,
		MCPConfig:  json.RawMessage(testMCPConfig),
	})
	if err != nil {
		t.Fatalf("Prepare codex: %v", err)
	}
	configPath := filepath.Join(env.RuntimeHome, "config.toml")
	if env.MCPConfigPath != configPath {
		t.Fatalf("MCPConfigPath = %q, want %q", env.MCPConfigPath, configPath)
	}
	text := readFile(t, configPath)
	for _, want := range []string{
		"[features]",
		"memories = false",
		"[memories]",
		"generate_memories = false",
		"use_memories = false",
		"[mcp_servers.orrery]",
		`url = "http://127.0.0.1:8787/mcp"`,
		`http_headers = { "Authorization" = "Bearer run-token", "X-Orrery-Run" = "run-mcp" }`,
		`default_tools_approval_mode = "auto"`,
		"required = true",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("codex config missing %q:\n%s", want, text)
		}
	}
	if !strings.HasPrefix(configPath, env.RuntimeEnv["CODEX_HOME"]) {
		t.Fatalf("codex config %q not under CODEX_HOME %q", configPath, env.RuntimeEnv["CODEX_HOME"])
	}
}

func TestPreparePIInjectsMCPIntoRunScopedRuntimeHome(t *testing.T) {
	workspace := t.TempDir()
	env, err := Prepare(PrepareRequest{
		Root:       t.TempDir(),
		RunID:      "run-mcp",
		Runtime:    "pi",
		WorkDir:    workspace,
		Context:    agentcore.ContextPack{WorkerPersonalGoal: "Use PI"},
		CleanupOld: true,
		MCPConfig:  json.RawMessage(testMCPConfig),
	})
	if err != nil {
		t.Fatalf("Prepare pi: %v", err)
	}
	configPath := filepath.Join(env.RuntimeHome, "mcp_config.json")
	if env.MCPConfigPath != configPath {
		t.Fatalf("MCPConfigPath = %q, want %q", env.MCPConfigPath, configPath)
	}
	if got := readFile(t, configPath); got != testMCPConfig {
		t.Fatalf("PI MCP config = %q, want original config", got)
	}
	if info, err := os.Stat(configPath); err != nil {
		t.Fatal(err)
	} else if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("PI MCP config mode = %o, want 600", info.Mode().Perm())
	}
	if _, err := os.Stat(filepath.Join(workspace, "mcp_config.json")); !os.IsNotExist(err) {
		t.Fatalf("persistent workspace MCP config exists: %v", err)
	}
}

func TestPrepareClaudeInjectsMCPIntoProjectWithoutTemporaryConfigIsolation(t *testing.T) {
	env, err := Prepare(PrepareRequest{
		Root:       t.TempDir(),
		RunID:      "run-mcp",
		Runtime:    "claude",
		Context:    agentcore.ContextPack{WorkerPersonalGoal: "Use Claude"},
		CleanupOld: true,
		MCPConfig:  json.RawMessage(testMCPConfig),
	})
	if err != nil {
		t.Fatalf("Prepare claude: %v", err)
	}
	mcpPath := filepath.Join(env.WorkDir, ".mcp.json")
	if env.MCPConfigPath != mcpPath {
		t.Fatalf("MCPConfigPath = %q, want %q", env.MCPConfigPath, mcpPath)
	}
	var mcp map[string]any
	if err := json.Unmarshal([]byte(readFile(t, mcpPath)), &mcp); err != nil {
		t.Fatalf("parse .mcp.json: %v", err)
	}
	servers := mcp["mcpServers"].(map[string]any)
	orrery := servers["orrery"].(map[string]any)
	if orrery["type"] != "http" || orrery["url"] != "http://127.0.0.1:8787/mcp" {
		t.Fatalf("claude orrery server = %#v", orrery)
	}
	headers := orrery["headers"].(map[string]any)
	if headers["Authorization"] != "Bearer run-token" || headers["X-Orrery-Run"] != "run-mcp" {
		t.Fatalf("claude headers = %#v", headers)
	}
	settingsPath := filepath.Join(env.RuntimeHome, "settings.json")
	settings := readFile(t, settingsPath)
	for _, want := range []string{`"settingSources"`, `"project"`, `"allowedTools"`, `"mcp__orrery__*"`} {
		if !strings.Contains(settings, want) {
			t.Fatalf("claude settings missing %q:\n%s", want, settings)
		}
	}
	if _, isolated := env.RuntimeEnv["CLAUDE_CONFIG_DIR"]; isolated {
		t.Fatalf("CLAUDE_CONFIG_DIR must remain unset while Claude temporarily reuses the host login: %#v", env.RuntimeEnv)
	}
	if filepath.Dir(settingsPath) != env.RuntimeHome {
		t.Fatalf("claude run settings %q not under runtime home %q", settingsPath, env.RuntimeHome)
	}
}

func TestPrepareOpenClawInjectsMCPIntoWrapperConfig(t *testing.T) {
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
		RunID:      "run-mcp",
		AgentID:    "mcp-agent",
		Runtime:    "openclaw",
		Context:    agentcore.ContextPack{WorkerPersonalGoal: "Use OpenClaw"},
		CleanupOld: true,
		MCPConfig:  json.RawMessage(testMCPConfig),
	})
	if err != nil {
		t.Fatalf("Prepare openclaw: %v", err)
	}
	if env.MCPConfigPath != env.RuntimeEnv["OPENCLAW_CONFIG_PATH"] {
		t.Fatalf("MCPConfigPath = %q, OPENCLAW_CONFIG_PATH = %q", env.MCPConfigPath, env.RuntimeEnv["OPENCLAW_CONFIG_PATH"])
	}
	var cfg map[string]any
	if err := json.Unmarshal([]byte(readFile(t, env.MCPConfigPath)), &cfg); err != nil {
		t.Fatalf("parse openclaw wrapper: %v", err)
	}
	if cfg["$include"].([]any)[0] != userConfigPath {
		t.Fatalf("include = %#v, want %q", cfg["$include"], userConfigPath)
	}
	servers := cfg["mcp"].(map[string]any)["servers"].(map[string]any)
	orrery := servers["orrery"].(map[string]any)
	if orrery["transport"] != "streamable-http" || orrery["url"] != "http://127.0.0.1:8787/mcp" {
		t.Fatalf("openclaw orrery server = %#v", orrery)
	}
	headers := orrery["headers"].(map[string]any)
	if headers["Authorization"] != "Bearer run-token" || headers["X-Orrery-Run"] != "run-mcp" {
		t.Fatalf("openclaw headers = %#v", headers)
	}
}
