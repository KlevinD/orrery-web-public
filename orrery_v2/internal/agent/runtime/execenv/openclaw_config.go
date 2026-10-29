package execenv

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	agentcore "orrery_v2/internal/agent/core"
)

const openclawConfigFile = "openclaw-config.json"
const openclawCLITimeout = 15 * time.Second

type OpenClawConfigPrep struct {
	OpenClawBin string
	Timeout     time.Duration
	AgentID     agentcore.AgentID
}

type OpenClawConfigResult struct {
	ConfigPath  string
	IncludeRoot string
	AgentID     string
}

func prepareOpenClawConfig(envRoot, workDir string, opts OpenClawConfigPrep) (OpenClawConfigResult, error) {
	bin := opts.OpenClawBin
	if bin == "" {
		bin = "openclaw"
	}
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = openclawCLITimeout
	}
	activePath, exists, err := openclawActiveConfigPath(bin, timeout)
	if err != nil {
		return OpenClawConfigResult{}, fmt.Errorf("locate openclaw active config: %w", err)
	}
	managedAgentID, err := managedOpenClawAgentID(opts.AgentID.String())
	if err != nil {
		return OpenClawConfigResult{}, err
	}
	cfg := buildPerTaskOpenClawConfig(activePath, exists, managedAgentID, opts.AgentID.String(), workDir, filepath.Join(envRoot, "openclaw-agent"))
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return OpenClawConfigResult{}, fmt.Errorf("marshal openclaw config: %w", err)
	}
	outPath := filepath.Join(envRoot, openclawConfigFile)
	if err := os.WriteFile(outPath, data, 0o600); err != nil {
		return OpenClawConfigResult{}, fmt.Errorf("write openclaw config: %w", err)
	}
	result := OpenClawConfigResult{ConfigPath: outPath, AgentID: managedAgentID}
	if exists {
		result.IncludeRoot = filepath.Dir(activePath)
	}
	return result, nil
}

func buildPerTaskOpenClawConfig(activePath string, exists bool, managedAgentID, sourceAgentID, workDir, agentDir string) map[string]any {
	agents := map[string]any{
		"defaults": map[string]any{"workspace": workDir},
		"list": []any{map[string]any{
			"id":        managedAgentID,
			"name":      "Orrery " + strings.TrimSpace(sourceAgentID),
			"workspace": workDir,
			"agentDir":  agentDir,
			"tools": map[string]any{
				"deny": []string{"sessions_spawn", "subagents"},
				"fs":   map[string]any{"workspaceOnly": true},
			},
		}},
	}
	cfg := map[string]any{"agents": agents}
	if exists {
		cfg["$include"] = []any{activePath}
	}
	return cfg
}

func managedOpenClawAgentID(agentID string) (string, error) {
	value := strings.ToLower(strings.TrimSpace(agentID))
	if value == "" {
		return "", fmt.Errorf("orrery agent id is required for local openclaw runtime")
	}
	var normalized strings.Builder
	for _, r := range value {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' || r == '_' {
			normalized.WriteRune(r)
			continue
		}
		normalized.WriteByte('-')
	}
	value = strings.Trim(normalized.String(), "-")
	if value == "" {
		return "", fmt.Errorf("orrery agent id %q cannot form an openclaw agent id", agentID)
	}
	const maxSuffixLength = 64 - len("orrery-")
	if len(value) > maxSuffixLength {
		value = strings.Trim(value[:maxSuffixLength], "-")
	}
	return "orrery-" + value, nil
}

func openclawActiveConfigPath(bin string, timeout time.Duration) (string, bool, error) {
	if configured := strings.TrimSpace(os.Getenv("ORRERY_OPENCLAW_BASE_CONFIG_PATH")); configured != "" {
		return validateOpenClawConfigPath(configured)
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	out, err := openclawExec(ctx, bin, "config", "file")
	if err != nil {
		return "", false, err
	}
	path := lastNonEmptyLine(out)
	if path == "" {
		return "", false, fmt.Errorf("`openclaw config file` returned empty output")
	}
	path = os.Expand(path, func(name string) string {
		if value, ok := os.LookupEnv(name); ok {
			return value
		}
		return "$" + name
	})
	return validateOpenClawConfigPath(path)
}

func validateOpenClawConfigPath(path string) (string, bool, error) {
	if path == "~" || strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", false, fmt.Errorf("expand `~` in openclaw config path: %w", err)
		}
		if path == "~" {
			path = home
		} else {
			path = filepath.Join(home, strings.TrimPrefix(path, "~/"))
		}
	}
	if !filepath.IsAbs(path) {
		return "", false, fmt.Errorf("openclaw config path is not absolute: %q", path)
	}
	path = filepath.Clean(path)
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return path, false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("stat openclaw config %s: %w", path, err)
	}
	if info.IsDir() {
		return "", false, fmt.Errorf("openclaw config path %s is a directory, not a file", path)
	}
	return path, true, nil
}

func lastNonEmptyLine(value string) string {
	lines := strings.Split(strings.ReplaceAll(value, "\r\n", "\n"), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if line := strings.TrimSpace(lines[i]); line != "" {
			return line
		}
	}
	return ""
}

var openclawExec = execOpenClawCLI

func execOpenClawCLI(ctx context.Context, bin string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Env = os.Environ()
	var stderr strings.Builder
	cmd.Stderr = &stderr
	raw, err := cmd.Output()
	if err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return "", fmt.Errorf("openclaw %s: %w (stderr: %s)", strings.Join(args, " "), err, msg)
		}
		return "", fmt.Errorf("openclaw %s: %w", strings.Join(args, " "), err)
	}
	return string(raw), nil
}
