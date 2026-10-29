package openclaw

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	runtimecore "orrery_v2/internal/agent/runtime/core"
	"orrery_v2/internal/agent/runtime/process"
	"orrery_v2/internal/agent/runtime/providers/cliinternal"
	"orrery_v2/internal/agent/runtime/providers/workspaceprompt"
)

type RuntimeConfig struct {
	Executable     string            `json:"executable,omitempty"`
	Mode           string            `json:"mode,omitempty"`
	Transport      string            `json:"transport,omitempty"`
	GatewayURL     string            `json:"gateway_url,omitempty"`
	BaseURL        string            `json:"base_url,omitempty"`
	Token          string            `json:"token,omitempty"`
	TokenEnv       string            `json:"token_env,omitempty"`
	TLSFingerprint string            `json:"tls_fingerprint,omitempty"`
	HTTPAPI        string            `json:"http_api,omitempty"`
	AgentID        string            `json:"agent_id,omitempty"`
	ModelID        string            `json:"model_id,omitempty"`
	ExtraArgs      []string          `json:"extra_args,omitempty"`
	Env            map[string]string `json:"env,omitempty"`
}

var blockedArgs = map[string]process.BlockedArgMode{
	"--local":         process.BlockedStandalone,
	"--json":          process.BlockedStandalone,
	"--session-id":    process.BlockedWithValue,
	"--message":       process.BlockedWithValue,
	"--agent":         process.BlockedWithValue,
	"--model":         process.BlockedWithValue,
	"--system-prompt": process.BlockedWithValue,
	"--url":           process.BlockedWithValue,
	"--token":         process.BlockedWithValue,
	"--password":      process.BlockedWithValue,
}

func BuildArgs(prompt, sessionID string, opts runtimecore.ExecOptions) []string {
	runtimeCfg := parseRuntimeConfig(opts.ProviderConfig)
	args := []string{"agent", "--local", "--json", "--session-id", sessionID}
	if opts.Timeout > 0 {
		args = append(args, "--timeout", fmt.Sprintf("%d", int(opts.Timeout.Seconds())))
	}
	customArgs := process.FilterArgs(opts.CustomArgs, blockedArgs)
	managedAgentID := strings.TrimSpace(opts.Environment["ORRERY_OPENCLAW_AGENT_ID"])
	if managedAgentID == "" {
		managedAgentID = strings.TrimSpace(runtimeCfg.AgentID)
	}
	if managedAgentID != "" {
		args = append(args, "--agent", managedAgentID)
	}
	model := strings.TrimSpace(runtimeCfg.ModelID)
	if model == "" {
		model = strings.TrimSpace(opts.Model)
	}
	if model != "" {
		args = append(args, "--model", model)
	}
	args = append(args, process.FilterArgs(opts.ExtraArgs, blockedArgs)...)
	args = append(args, customArgs...)
	if systemPrompt := workspaceprompt.SystemPrompt(opts.SystemPrompt, opts); systemPrompt != "" {
		prompt = systemPrompt + "\n\n" + prompt
	}
	args = append(args, "--message", prompt)
	return args
}

func parseRuntimeConfig(raw json.RawMessage) RuntimeConfig {
	if len(raw) == 0 {
		return RuntimeConfig{}
	}
	var runtimeCfg RuntimeConfig
	_ = json.Unmarshal(raw, &runtimeCfg)
	return runtimeCfg
}

func (b *Backend) ValidateProviderConfig(raw json.RawMessage) error {
	if len(raw) == 0 {
		return nil
	}
	if strings.TrimSpace(string(raw)) == "null" {
		return fmt.Errorf("openclaw provider config must be a JSON object")
	}
	var runtimeCfg RuntimeConfig
	if err := json.Unmarshal(raw, &runtimeCfg); err != nil {
		return fmt.Errorf("decode openclaw provider config: %w", err)
	}
	return nil
}

func (c RuntimeConfig) normalizedMode() string {
	mode := strings.ToLower(strings.TrimSpace(c.Mode))
	if mode == "" {
		mode = strings.ToLower(strings.TrimSpace(c.Transport))
	}
	return strings.ReplaceAll(mode, "-", "_")
}

func (c RuntimeConfig) isRemoteHTTP() bool {
	return c.normalizedMode() == "remote_http"
}

func (c RuntimeConfig) isRemoteWS() bool {
	return c.normalizedMode() == "remote_ws"
}

func (c RuntimeConfig) httpAPI() string {
	switch strings.ReplaceAll(strings.ToLower(strings.TrimSpace(c.HTTPAPI)), "-", "_") {
	case "chat_completions":
		return "chat_completions"
	default:
		return "responses"
	}
}

func (c RuntimeConfig) remoteURL() string {
	if strings.TrimSpace(c.GatewayURL) != "" {
		return strings.TrimSpace(c.GatewayURL)
	}
	return strings.TrimSpace(c.BaseURL)
}

func (c RuntimeConfig) tokenValue() string {
	if strings.TrimSpace(c.Token) != "" {
		return strings.TrimSpace(c.Token)
	}
	if strings.TrimSpace(c.TokenEnv) != "" {
		return os.Getenv(strings.TrimSpace(c.TokenEnv))
	}
	return ""
}

func applyRuntimeConfig(opts runtimecore.ExecOptions, cfg *Config) runtimecore.ExecOptions {
	if len(opts.ProviderConfig) == 0 {
		return opts
	}
	var runtimeCfg RuntimeConfig
	if json.Unmarshal(opts.ProviderConfig, &runtimeCfg) != nil {
		return opts
	}
	if runtimeCfg.Executable != "" && cfg != nil {
		cfg.Executable = runtimeCfg.Executable
	}
	if len(runtimeCfg.Env) > 0 && cfg != nil {
		cfg.Env = cliinternal.MergeEnv(cfg.Env, runtimeCfg.Env)
	}
	if len(runtimeCfg.ExtraArgs) > 0 {
		opts.ExtraArgs = append(append([]string(nil), runtimeCfg.ExtraArgs...), opts.ExtraArgs...)
	}
	return opts
}
