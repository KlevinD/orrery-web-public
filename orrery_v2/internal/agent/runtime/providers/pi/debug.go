package pi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	runtimecore "orrery_v2/internal/agent/runtime/core"
	"orrery_v2/internal/agent/runtime/providers/workspaceprompt"
)

type debugConfig struct {
	CaptureRuntimeInput    bool   `json:"capture_runtime_input,omitempty"`
	CaptureProviderRequest bool   `json:"capture_provider_request,omitempty"`
	OutputDir              string `json:"output_dir,omitempty"`
}

type piDebugArtifacts struct {
	RuntimeInputPath       string
	ProviderRequestsPath   string
	ProviderCaptureBaseURL string
	CaptureRuntimeInput    bool
	metadata               map[string]string
	cleanup                func()
}

func prepareDebugArtifacts(ctx context.Context, opts runtimecore.ExecOptions, runtimeCfg RuntimeConfig) (piDebugArtifacts, error) {
	cfg := effectiveDebugConfig(opts, runtimeCfg)
	if !cfg.CaptureRuntimeInput && !cfg.CaptureProviderRequest {
		return piDebugArtifacts{}, nil
	}
	dir := debugOutputDir(opts, cfg)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return piDebugArtifacts{}, fmt.Errorf("create pi debug dir: %w", err)
	}
	artifacts := piDebugArtifacts{
		CaptureRuntimeInput: cfg.CaptureRuntimeInput,
		metadata:            map[string]string{},
	}
	if cfg.CaptureRuntimeInput {
		path := filepath.Join(dir, "runtime-input.json")
		artifacts.RuntimeInputPath = path
		artifacts.metadata["pi_debug_runtime_input_path"] = path
	}
	if cfg.CaptureProviderRequest {
		path := filepath.Join(dir, "provider-requests.jsonl")
		prepared, cleanup, err := prepareProviderRequestCapture(ctx, path)
		if err != nil {
			return piDebugArtifacts{}, err
		}
		artifacts.ProviderRequestsPath = path
		artifacts.ProviderCaptureBaseURL = prepared.BaseURL
		artifacts.cleanup = cleanup
		artifacts.metadata["pi_debug_provider_requests_path"] = path
		artifacts.metadata["pi_debug_provider_capture_base_url"] = prepared.BaseURL
	}
	return artifacts, nil
}

func (a piDebugArtifacts) Environment() map[string]string {
	if strings.TrimSpace(a.ProviderCaptureBaseURL) == "" {
		return nil
	}
	return map[string]string{
		"ORRERY_PI_PROVIDER_REQUEST_CAPTURE_URL": strings.TrimRight(a.ProviderCaptureBaseURL, "/") + "/provider-request",
	}
}

func (a piDebugArtifacts) Metadata() map[string]string {
	if len(a.metadata) == 0 {
		return nil
	}
	out := make(map[string]string, len(a.metadata))
	for key, value := range a.metadata {
		out[key] = value
	}
	return out
}

func (a piDebugArtifacts) Cleanup() {
	if a.cleanup != nil {
		a.cleanup()
	}
}

func (a piDebugArtifacts) WriteRuntimeInput(prompt string, args []string, opts runtimecore.ExecOptions) error {
	if !a.CaptureRuntimeInput || strings.TrimSpace(a.RuntimeInputPath) == "" {
		return nil
	}
	return writeRuntimeInputDebug(a.RuntimeInputPath, prompt, args, opts)
}

func effectiveDebugConfig(opts runtimecore.ExecOptions, runtimeCfg RuntimeConfig) debugConfig {
	cfg := runtimeCfg.Debug
	if value := strings.TrimSpace(opts.ProviderSpecific["pi_debug_capture_runtime_input"]); value != "" {
		cfg.CaptureRuntimeInput = parseBool(value)
	}
	if value := strings.TrimSpace(opts.ProviderSpecific["pi_debug_capture_provider_request"]); value != "" {
		cfg.CaptureProviderRequest = parseBool(value)
	}
	if value := strings.TrimSpace(opts.Environment["ORRERY_PI_DEBUG_CAPTURE_RUNTIME_INPUT"]); value != "" {
		cfg.CaptureRuntimeInput = parseBool(value)
	}
	if value := strings.TrimSpace(opts.Environment["ORRERY_PI_DEBUG_CAPTURE_PROVIDER_REQUEST"]); value != "" {
		cfg.CaptureProviderRequest = parseBool(value)
	}
	if value := strings.TrimSpace(opts.ProviderSpecific["pi_debug_output_dir"]); value != "" {
		cfg.OutputDir = value
	}
	if value := strings.TrimSpace(opts.Environment["ORRERY_PI_DEBUG_DIR"]); value != "" {
		cfg.OutputDir = value
	}
	return cfg
}

func debugOutputDir(opts runtimecore.ExecOptions, cfg debugConfig) string {
	if value := strings.TrimSpace(cfg.OutputDir); value != "" {
		return filepath.Clean(value)
	}
	if value := strings.TrimSpace(opts.Environment["PI_CODING_AGENT_DIR"]); value != "" {
		return filepath.Join(value, "orrery-runtime", "debug")
	}
	if value := strings.TrimSpace(opts.Cwd); value != "" {
		return filepath.Join(value, ".orrery", "pi-debug")
	}
	return filepath.Join(os.TempDir(), "orrery-pi-debug")
}

func writeRuntimeInputDebug(path, prompt string, args []string, opts runtimecore.ExecOptions) error {
	payload := map[string]any{
		"schema_version":       1,
		"created_at":           time.Now().UTC().Format(time.RFC3339Nano),
		"run_id":               opts.RunID.String(),
		"agent_id":             opts.AgentID.String(),
		"cwd":                  opts.Cwd,
		"model":                strings.TrimSpace(opts.Model),
		"prompt":               prompt,
		"system_prompt":        workspaceprompt.SystemPrompt(opts.SystemPrompt, opts),
		"resume_session_id":    strings.TrimSpace(opts.ResumeSessionID),
		"args":                 append([]string(nil), args...),
		"environment":          redactedStringMap(opts.Environment),
		"provider_specific":    redactedStringMap(opts.ProviderSpecific),
		"tool_bindings_count":  len(opts.ToolBindings),
		"skill_bindings_count": len(opts.SkillBindings),
		"tools":                runtimeToolDebugSummaries(opts.ToolBindings),
		"skills":               runtimeSkillDebugSummaries(opts.SkillBindings),
		"workspace_policy": map[string]any{
			"root":             opts.WorkspacePolicy.Root,
			"work_dir":         opts.WorkspacePolicy.WorkDir,
			"writable_roots":   append([]string(nil), opts.WorkspacePolicy.WritableRoots...),
			"sandbox_mode":     opts.WorkspacePolicy.SandboxMode,
			"approval_policy":  opts.WorkspacePolicy.ApprovalPolicy,
			"enforce_boundary": opts.WorkspacePolicy.EnforceBoundary,
		},
	}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return fmt.Errorf("encode pi debug runtime input: %w", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("write pi debug runtime input: %w", err)
	}
	return nil
}

func runtimeToolDebugSummaries(bindings []agentcore.RuntimeToolBinding) []map[string]any {
	if len(bindings) == 0 {
		return nil
	}
	out := make([]map[string]any, 0, len(bindings))
	for _, binding := range bindings {
		out = append(out, map[string]any{
			"tool_id":             string(binding.ToolID),
			"alias":               strings.TrimSpace(binding.Alias),
			"version":             strings.TrimSpace(binding.Version),
			"summary":             strings.TrimSpace(binding.Summary),
			"granted_permissions": append([]string(nil), binding.GrantedPermissions...),
			"secret_env":          redactedStringMap(binding.SecretEnv),
			"input_schema":        binding.InputSchema,
			"output_schema":       binding.OutputSchema,
		})
	}
	return out
}

func runtimeSkillDebugSummaries(bindings []agentcore.RuntimeSkillBinding) []map[string]any {
	if len(bindings) == 0 {
		return nil
	}
	out := make([]map[string]any, 0, len(bindings))
	for _, binding := range bindings {
		out = append(out, map[string]any{
			"skill_id": strings.TrimSpace(binding.SkillID),
			"alias":    strings.TrimSpace(binding.Alias),
			"version":  strings.TrimSpace(binding.Version),
			"summary":  strings.TrimSpace(binding.Summary),
		})
	}
	return out
}

func redactedStringMap(src map[string]string) map[string]string {
	if len(src) == 0 {
		return nil
	}
	keys := make([]string, 0, len(src))
	for key := range src {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make(map[string]string, len(src))
	for _, key := range keys {
		if sensitiveName(key) {
			out[key] = "[redacted]"
			continue
		}
		out[key] = src[key]
	}
	return out
}

func redactedHeaders(headers http.Header) map[string]string {
	if len(headers) == 0 {
		return nil
	}
	keys := make([]string, 0, len(headers))
	for key := range headers {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make(map[string]string, len(headers))
	for _, key := range keys {
		canonical := http.CanonicalHeaderKey(key)
		if sensitiveName(canonical) {
			out[canonical] = "[redacted]"
			continue
		}
		out[canonical] = strings.Join(headers.Values(key), ", ")
	}
	return out
}

func sensitiveName(name string) bool {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		return false
	}
	for _, needle := range []string{"authorization", "api_key", "apikey", "token", "secret", "password", "credential", "cookie"} {
		if strings.Contains(name, needle) {
			return true
		}
	}
	return false
}

func parseBool(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "t", "true", "yes", "y", "on", "enabled":
		return true
	default:
		return false
	}
}

func mergeCleanups(cleanups ...func()) func() {
	return func() {
		for i := len(cleanups) - 1; i >= 0; i-- {
			if cleanups[i] != nil {
				cleanups[i]()
			}
		}
	}
}
