package pi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	runtimecore "orrery_v2/internal/agent/runtime/core"
	llmprofile "orrery_v2/internal/agent/runtime/llmprofile"
	"orrery_v2/internal/agent/runtime/process"
	"orrery_v2/internal/agent/runtime/providers/workspaceprompt"
)

type Config struct {
	Executable     string
	Env            map[string]string
	ApprovalBroker runtimecore.ApprovalBroker
	ToolDispatcher runtimecore.RuntimeToolDispatcher
	SecretResolver SecretResolver
	LLMProfiles    llmprofile.ProfileProvider
}

type RuntimeConfig struct {
	Executable         string            `json:"executable,omitempty"`
	Model              string            `json:"model,omitempty"`
	ExtraArgs          []string          `json:"extra_args,omitempty"`
	Env                map[string]string `json:"env,omitempty"`
	BoundToolsOnly     bool              `json:"bound_tools_only,omitempty"`
	ApprovalMode       string            `json:"approval_mode,omitempty"`
	ApprovalTimeoutMs  int64             `json:"approval_timeout_ms,omitempty"`
	PromptAckTimeoutMs int64             `json:"prompt_ack_timeout_ms,omitempty"`
	Debug              debugConfig       `json:"debug,omitempty"`
}

func (c *RuntimeConfig) UnmarshalJSON(data []byte) error {
	type alias RuntimeConfig
	var raw struct {
		alias
		ApprovalTimeoutMs  any `json:"approval_timeout_ms,omitempty"`
		PromptAckTimeoutMs any `json:"prompt_ack_timeout_ms,omitempty"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	*c = RuntimeConfig(raw.alias)
	c.ApprovalTimeoutMs = runtimeConfigInt64(raw.ApprovalTimeoutMs)
	c.PromptAckTimeoutMs = runtimeConfigInt64(raw.PromptAckTimeoutMs)
	return nil
}

var blockedArgs = map[string]process.BlockedArgMode{
	"-p":                     process.BlockedWithValue,
	"--print":                process.BlockedStandalone,
	"--mode":                 process.BlockedWithValue,
	"--no-session":           process.BlockedStandalone,
	"--session":              process.BlockedWithValue,
	"--session-id":           process.BlockedWithValue,
	"--session-dir":          process.BlockedWithValue,
	"--continue":             process.BlockedStandalone,
	"-c":                     process.BlockedStandalone,
	"--resume":               process.BlockedStandalone,
	"-r":                     process.BlockedStandalone,
	"--fork":                 process.BlockedWithValue,
	"--system-prompt":        process.BlockedWithValue,
	"--append-system-prompt": process.BlockedWithValue,
	"--extension":            process.BlockedWithValue,
	"-e":                     process.BlockedWithValue,
	"--no-extensions":        process.BlockedStandalone,
	"-ne":                    process.BlockedStandalone,
	"--prompt-template":      process.BlockedWithValue,
	"--no-prompt-templates":  process.BlockedStandalone,
	"-np":                    process.BlockedStandalone,
	"--theme":                process.BlockedWithValue,
	"--no-themes":            process.BlockedStandalone,
	"--no-context-files":     process.BlockedStandalone,
	"-nc":                    process.BlockedStandalone,
	"--skill":                process.BlockedWithValue,
	"--no-skills":            process.BlockedStandalone,
	"-ns":                    process.BlockedStandalone,
	"--tools":                process.BlockedWithValue,
	"-t":                     process.BlockedWithValue,
	"--exclude-tools":        process.BlockedWithValue,
	"-xt":                    process.BlockedWithValue,
	"--no-tools":             process.BlockedStandalone,
	"-nt":                    process.BlockedStandalone,
	"--no-builtin-tools":     process.BlockedStandalone,
	"-nbt":                   process.BlockedStandalone,
	"--approve":              process.BlockedStandalone,
	"-a":                     process.BlockedStandalone,
	"--no-approve":           process.BlockedStandalone,
	"-na":                    process.BlockedStandalone,
	"--model":                process.BlockedWithValue,
}

const (
	piSystemPromptPathEnv = "ORRERY_PI_SYSTEM_PROMPT_PATH"
	piSessionDirEnv       = "PI_CODING_AGENT_SESSION_DIR"
)

func BuildArgs(prompt string, opts runtimecore.ExecOptions) []string {
	_ = prompt // Prompts are sent as typed RPC commands over stdin, never argv.
	// Explicit resources remain enabled, while project/user auto-discovery is
	// disabled so an authorized binding snapshot is the only loaded surface.
	args := []string{"--mode", "rpc", "--no-extensions", "--no-skills", "--no-prompt-templates", "--no-themes", "--no-context-files", "--no-approve"}
	runtimeConfig := parseRuntimeConfig(opts.ProviderConfig)
	diaryMaintenance := parseBool(opts.ProviderSpecific["diary_maintenance"])
	if runtimeConfig.BoundToolsOnly || piRequiresExplicitBoundTools(opts) {
		args = append(args, "--no-builtin-tools")
	}
	if diaryMaintenance {
		if !runtimeConfig.BoundToolsOnly {
			args = append(args, "--no-builtin-tools")
		}
		args = append(args, "--no-skills")
	}
	if !piSessionPersistenceEnabled(opts) {
		args = append(args, "--no-session")
	}
	if strings.TrimSpace(opts.Model) != "" {
		args = append(args, "--model", strings.TrimSpace(opts.Model))
	}
	if strings.TrimSpace(opts.ResumeSessionID) != "" {
		args = append(args, "--session", strings.TrimSpace(opts.ResumeSessionID))
	}
	if !diaryMaintenance {
		for _, skillPath := range runtimeListEnv(opts.Environment["ORRERY_PI_SKILL_PATHS"]) {
			args = append(args, "--skill", skillPath)
		}
	}
	if extensionPath := strings.TrimSpace(opts.Environment["ORRERY_PI_EXTENSION_PATH"]); extensionPath != "" {
		args = append(args, "--extension", extensionPath)
	}
	args = append(args, process.FilterArgs(opts.ExtraArgs, blockedArgs)...)
	args = append(args, process.FilterArgs(opts.CustomArgs, blockedArgs)...)
	return args
}

func piRequiresExplicitBoundTools(opts runtimecore.ExecOptions) bool {
	if strings.TrimSpace(opts.ProviderSpecific[agentcore.RuntimeAllowedToolIDsProviderKey]) != "" {
		return true
	}
	value := strings.TrimSpace(opts.ProviderSpecific[agentcore.RuntimeRequireExplicitToolScopesProviderKey])
	return value == "1" || strings.EqualFold(value, "true")
}

func piSessionPersistenceEnabled(opts runtimecore.ExecOptions) bool {
	if strings.TrimSpace(opts.ResumeSessionID) != "" || strings.TrimSpace(opts.Environment[piSessionDirEnv]) != "" {
		return true
	}
	if strings.TrimSpace(opts.ProviderSpecific["discussion_id"]) != "" {
		return true
	}
	switch strings.ToLower(strings.TrimSpace(opts.ProviderSpecific["discussion_external_cli_structured_prompt"])) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func runtimeListEnv(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	parts := splitRuntimeListEnv(value)
	out := make([]string, 0, len(parts))
	seen := map[string]bool{}
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" || seen[part] {
			continue
		}
		seen[part] = true
		out = append(out, part)
	}
	return out
}

func splitRuntimeListEnv(value string) []string {
	parts := strings.Split(value, string(os.PathListSeparator))
	if os.PathListSeparator != ':' {
		return parts
	}
	out := make([]string, 0, len(parts))
	for i := 0; i < len(parts); i++ {
		part := strings.TrimSpace(parts[i])
		if i+1 < len(parts) && isWindowsDriveToken(part) {
			next := strings.TrimSpace(parts[i+1])
			if strings.HasPrefix(next, "/") || strings.HasPrefix(next, "\\") {
				out = append(out, part+":"+next)
				i++
				continue
			}
		}
		out = append(out, parts[i])
	}
	return out
}

func isWindowsDriveToken(value string) bool {
	if len(value) != 1 {
		return false
	}
	return ('A' <= value[0] && value[0] <= 'Z') || ('a' <= value[0] && value[0] <= 'z')
}

func isolatePiDebugWorkerOptions(opts runtimecore.ExecOptions) (runtimecore.ExecOptions, error) {
	root := firstNonEmpty(opts.WorkspacePolicy.Root, opts.Cwd)
	if root == "" {
		root = os.TempDir()
	}
	sum := sha256.Sum256([]byte(firstNonEmpty(opts.RunID.String(), time.Now().UTC().Format(time.RFC3339Nano))))
	home := filepath.Join(filepath.Clean(root), ".orrery", "pi-rpc-debug", hex.EncodeToString(sum[:8]))
	if err := os.MkdirAll(home, 0o700); err != nil {
		return opts, fmt.Errorf("create isolated pi debug home: %w", err)
	}
	opts.Environment = mergePiEnvironment(opts.Environment, map[string]string{"PI_CODING_AGENT_DIR": home})
	return opts, nil
}

func preparePiSessionPersistence(opts runtimecore.ExecOptions, cfg Config) (runtimecore.ExecOptions, error) {
	mergedEnv := mergePiEnvironment(cfg.Env, opts.Environment)
	if configured := strings.TrimSpace(mergedEnv[piSessionDirEnv]); configured != "" {
		opts.Environment = mergePiEnvironment(opts.Environment, map[string]string{piSessionDirEnv: configured})
		return opts, nil
	}
	if !piSessionPersistenceEnabled(opts) {
		return opts, nil
	}
	workspaceDir := firstNonEmpty(opts.WorkspacePolicy.WorkDir, opts.Cwd)
	if workspaceDir == "" {
		return opts, nil
	}
	sessionDir := filepath.Join(workspaceDir, ".orrery", "pi-sessions")
	if err := os.MkdirAll(sessionDir, 0o700); err != nil {
		return opts, fmt.Errorf("create pi session dir: %w", err)
	}
	opts.Environment = mergePiEnvironment(opts.Environment, map[string]string{piSessionDirEnv: sessionDir})
	return opts, nil
}

func prepareSystemPromptFile(opts runtimecore.ExecOptions) (runtimecore.ExecOptions, error) {
	opts, systemPrompt, err := prepareDynamicSystemPrompt(opts)
	if err != nil {
		return opts, err
	}
	if err := writeDynamicSystemPrompt(opts, systemPrompt); err != nil {
		return opts, err
	}
	return opts, nil
}

func prepareDynamicSystemPrompt(opts runtimecore.ExecOptions) (runtimecore.ExecOptions, string, error) {
	parts := compactPiStrings(workspaceprompt.SystemPrompt(opts.SystemPrompt, opts))
	if contextPath := strings.TrimSpace(opts.Environment["ORRERY_AGENT_CONTEXT_PATH"]); contextPath != "" && !piStructuredDiscussionPrompt(opts) {
		raw, err := os.ReadFile(contextPath)
		if err != nil {
			return opts, "", fmt.Errorf("read pi agent context: %w", err)
		}
		contextPrompt := strings.TrimSpace(string(raw))
		if strings.TrimSpace(opts.ProviderSpecific["scheduled_task_id"]) != "" {
			contextPrompt = piScheduledAgentContextWithoutToolSummary(contextPrompt)
		}
		parts = append(parts, contextPrompt)
	}
	systemPrompt := strings.Join(compactPiStrings(parts...), "\n\n")
	if systemPrompt == "" && piSessionAffinity(opts) == "" {
		return opts, "", nil
	}
	dir := piBridgeDir(opts)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return opts, "", fmt.Errorf("create pi system prompt dir: %w", err)
	}
	path := filepath.Join(dir, "system-prompt.txt")
	opts.Environment = mergePiEnvironment(opts.Environment, map[string]string{piSystemPromptPathEnv: path})
	return opts, systemPrompt, nil
}

// Bound PI tools are registered through the bridge, so the generated catalog
// in scheduled Agent context is redundant and can exceed the model window.
func piScheduledAgentContextWithoutToolSummary(value string) string {
	lines := strings.Split(value, "\n")
	toolSummaryStart := -1
	skillsStart := -1
	for index, line := range lines {
		switch strings.TrimSpace(line) {
		case "# Tool Summary":
			if toolSummaryStart < 0 {
				toolSummaryStart = index
			}
		case "# Skills":
			if toolSummaryStart >= 0 {
				skillsStart = index
			}
		}
		if skillsStart >= 0 {
			break
		}
	}
	if toolSummaryStart < 0 || skillsStart <= toolSummaryStart {
		return value
	}
	trimmed := append([]string{}, lines[:toolSummaryStart]...)
	trimmed = append(trimmed, lines[skillsStart:]...)
	return strings.TrimSpace(strings.Join(trimmed, "\n"))
}

func piStructuredDiscussionPrompt(opts runtimecore.ExecOptions) bool {
	switch strings.ToLower(strings.TrimSpace(opts.ProviderSpecific["discussion_external_cli_structured_prompt"])) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func writeDynamicSystemPrompt(opts runtimecore.ExecOptions, systemPrompt string) error {
	path := strings.TrimSpace(opts.Environment[piSystemPromptPathEnv])
	if path == "" {
		return nil
	}
	if err := os.WriteFile(path, []byte(systemPrompt), 0o600); err != nil {
		return fmt.Errorf("write pi system prompt: %w", err)
	}
	return nil
}

func compactPiStrings(values ...string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			out = append(out, value)
		}
	}
	return out
}

func defaultExecutable(cwd string) string {
	if path := bundledExecutable(cwd); path != "" {
		return path
	}
	return "pi"
}

func shouldUseBundledDefaultExecutable(path string) bool {
	path = strings.TrimSpace(path)
	if path == "" {
		return true
	}
	if filepath.Base(path) != path {
		return false
	}
	switch strings.ToLower(path) {
	case "pi", "pi.cmd", "pi.exe", "pi.ps1":
		return true
	default:
		return false
	}
}

func resolvePiExecutable(configuredExecutable, selectedExecutable, cwd string) string {
	selectedExecutable = strings.TrimSpace(selectedExecutable)
	if !shouldUseBundledDefaultExecutable(selectedExecutable) {
		return selectedExecutable
	}
	configuredExecutable = strings.TrimSpace(configuredExecutable)
	if !shouldUseBundledDefaultExecutable(configuredExecutable) {
		return configuredExecutable
	}
	return defaultExecutable(cwd)
}

func bundledExecutable(cwd string) string {
	for _, root := range candidateRoots(cwd) {
		binDir := filepath.Join(root, "runtime", "pi-coding-agent", "node_modules", ".bin")
		for _, name := range bundledExecutableNames() {
			path := filepath.Join(binDir, name)
			if info, err := os.Stat(path); err == nil && !info.IsDir() {
				return path
			}
		}
	}
	return ""
}

func candidateRoots(cwd string) []string {
	roots := make([]string, 0, 8)
	addAncestors := func(path string) {
		path = strings.TrimSpace(path)
		if path == "" {
			return
		}
		abs, err := filepath.Abs(path)
		if err != nil {
			return
		}
		for {
			roots = append(roots, abs)
			parent := filepath.Dir(abs)
			if parent == abs {
				break
			}
			abs = parent
		}
	}
	addAncestors(os.Getenv("ORRERY_BASE_DIR"))
	addAncestors(cwd)
	if wd, err := os.Getwd(); err == nil {
		addAncestors(wd)
	}
	return uniqueStrings(roots)
}

func bundledExecutableNames() []string {
	if runtime.GOOS == "windows" {
		return []string{"pi.cmd", "pi.exe", "pi"}
	}
	return []string{"pi"}
}

func uniqueStrings(values []string) []string {
	out := make([]string, 0, len(values))
	seen := map[string]bool{}
	for _, value := range values {
		if seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

func applyRuntimeConfig(opts runtimecore.ExecOptions, cfg *Config) (runtimecore.ExecOptions, RuntimeConfig) {
	runtimeCfg := parseRuntimeConfig(opts.ProviderConfig)
	if strings.TrimSpace(runtimeCfg.Executable) != "" && cfg != nil {
		cfg.Executable = strings.TrimSpace(runtimeCfg.Executable)
	}
	if strings.TrimSpace(runtimeCfg.Model) != "" {
		opts.Model = strings.TrimSpace(runtimeCfg.Model)
	}
	if len(runtimeCfg.Env) > 0 && cfg != nil {
		cfg.Env = mergePiEnvironment(cfg.Env, runtimeCfg.Env)
	}
	if len(runtimeCfg.ExtraArgs) > 0 {
		opts.ExtraArgs = append(append([]string(nil), runtimeCfg.ExtraArgs...), opts.ExtraArgs...)
	}
	return opts, runtimeCfg
}

type SecretResolver interface {
	ResolveSecret(ctx context.Context, ref string) (string, error)
}

func applyLLMProfileRuntimeEnv(ctx context.Context, opts runtimecore.ExecOptions, cfg Config) (runtimecore.ExecOptions, Config, error) {
	selectedProfile, err := selectPiLLMProfile(ctx, opts, cfg)
	if err != nil {
		return opts, cfg, err
	}
	if selectedProfile != nil {
		profile := *selectedProfile
		if model := strings.TrimSpace(opts.ProviderSpecific[runtimecore.LLMModelOverrideProviderKey]); model != "" {
			profile.Model = model
		}
		selectedProfile = &profile
		opts = applyPiLLMProfileDefaults(opts, profile, shouldOverridePiLLMIdentityWithProfile(opts))
	}
	opts, cfg, err = applyPiLLMProfileProviderConfig(opts, cfg, selectedProfile)
	if err != nil {
		return opts, cfg, err
	}
	opts = applyProviderSpecificPiOptions(opts)
	provider := firstNonEmpty(piProviderFromArgs(opts.ExtraArgs), piProviderFromArgs(opts.CustomArgs), piProviderFromModel(opts.Model), opts.ProviderSpecific["llm_provider"])
	envKey := piAPIKeyEnvName(provider)
	if envKey == "" {
		return opts, cfg, nil
	}
	env := mergePiEnvironment(cfg.Env, opts.Environment)
	if strings.TrimSpace(env[envKey]) != "" {
		return opts, cfg, nil
	}
	if value := strings.TrimSpace(os.Getenv(envKey)); value != "" {
		cfg.Env = mergePiEnvironment(cfg.Env, map[string]string{envKey: value})
		return opts, cfg, nil
	}
	if selectedProfile != nil {
		if value := strings.TrimSpace(selectedProfile.APIKey); value != "" {
			cfg.Env = mergePiEnvironment(cfg.Env, map[string]string{envKey: value})
			return opts, cfg, nil
		}
	}
	ref := firstNonEmpty(opts.ProviderSpecific["llm_api_key_ref"], opts.ProviderSpecific["api_key_ref"])
	if ref == "" || cfg.SecretResolver == nil {
		return opts, cfg, nil
	}
	value, err := cfg.SecretResolver.ResolveSecret(ctx, ref)
	if err != nil {
		return opts, cfg, fmt.Errorf("resolve pi llm api key %s: %w", ref, err)
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return opts, cfg, nil
	}
	cfg.Env = mergePiEnvironment(cfg.Env, map[string]string{envKey: value})
	return opts, cfg, nil
}

type piModelsFile struct {
	Providers map[string]piModelsProvider `json:"providers"`
}

type piModelsProvider struct {
	BaseURL string            `json:"baseUrl"`
	API     string            `json:"api"`
	APIKey  string            `json:"apiKey"`
	Headers map[string]string `json:"headers,omitempty"`
	Compat  map[string]any    `json:"compat,omitempty"`
	Models  []piModelEntry    `json:"models"`
}

type piModelEntry struct {
	ID            string         `json:"id"`
	Name          string         `json:"name,omitempty"`
	Reasoning     bool           `json:"reasoning,omitempty"`
	Input         []string       `json:"input,omitempty"`
	ContextWindow int            `json:"contextWindow,omitempty"`
	MaxTokens     int            `json:"maxTokens,omitempty"`
	Compat        map[string]any `json:"compat,omitempty"`
}

func applyPiLLMProfileProviderConfig(opts runtimecore.ExecOptions, cfg Config, selectedProfile *llmprofile.LLMProfile) (runtimecore.ExecOptions, Config, error) {
	profile := piLLMProfileForOptions(opts, selectedProfile)
	provider := piProviderForProfile(profile, opts.ProviderSpecific["llm_provider"])
	if provider == "" {
		return opts, cfg, nil
	}
	if opts.ProviderSpecific == nil {
		opts.ProviderSpecific = map[string]string{}
	}
	opts.ProviderSpecific["llm_provider"] = provider
	if provider == "azure-openai-responses" && profile.BaseURL != "" {
		cfg.Env = mergePiEnvironment(cfg.Env, map[string]string{"AZURE_OPENAI_BASE_URL": profile.BaseURL})
	}
	if !requiresPiGeneratedModelProvider(provider) {
		return opts, cfg, nil
	}
	if profile.BaseURL == "" {
		return opts, cfg, fmt.Errorf("pi llm provider %q requires llm_base_url", provider)
	}
	if profile.Model == "" {
		return opts, cfg, fmt.Errorf("pi llm provider %q requires llm_model", provider)
	}
	agentDir := piGeneratedAgentDir(opts)
	if err := writePiGeneratedModelsFile(agentDir, provider, profile); err != nil {
		return opts, cfg, err
	}
	opts.Environment = mergePiEnvironment(opts.Environment, map[string]string{"PI_CODING_AGENT_DIR": agentDir})
	opts.Model = profile.Model
	opts.ProviderSpecific["llm_model"] = profile.Model
	opts.ProviderSpecific["llm_base_url"] = profile.BaseURL
	return opts, cfg, nil
}

func piProviderForProfile(profile llmprofile.LLMProfile, fallback string) string {
	provider := piProviderAlias(firstNonEmpty(profile.Provider, fallback))
	if provider == "openai" && profile.BaseURL != "" && !isOfficialOpenAIBaseURL(profile.BaseURL) {
		return "custom-openai"
	}
	return provider
}

func isOfficialOpenAIBaseURL(raw string) bool {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	return err == nil && strings.EqualFold(parsed.Scheme, "https") && strings.EqualFold(parsed.Hostname(), "api.openai.com")
}

func piLLMProfileForOptions(opts runtimecore.ExecOptions, selectedProfile *llmprofile.LLMProfile) llmprofile.LLMProfile {
	var profile llmprofile.LLMProfile
	if selectedProfile != nil {
		profile = *selectedProfile
	}
	profile = normalizePiLLMProfile(profile)
	if opts.ProviderSpecific != nil {
		profile.Provider = firstNonEmpty(profile.Provider, opts.ProviderSpecific["llm_provider"])
		profile.BaseURL = firstNonEmpty(profile.BaseURL, opts.ProviderSpecific["llm_base_url"])
		profile.Model = firstNonEmpty(profile.Model, opts.ProviderSpecific["llm_model"])
		profile.APIKeyRef = firstNonEmpty(profile.APIKeyRef, opts.ProviderSpecific["llm_api_key_ref"], opts.ProviderSpecific["api_key_ref"])
		if profile.ContextLength <= 0 {
			profile.ContextLength = int(runtimeConfigInt64(opts.ProviderSpecific["llm_context_length"]))
		}
	}
	profile.Model = firstNonEmpty(profile.Model, opts.Model)
	return normalizePiLLMProfile(profile)
}

func requiresPiGeneratedModelProvider(provider string) bool {
	switch piProviderAlias(provider) {
	case "custom-openai", "custom-openai-responses", "qwen", "ollama":
		return true
	default:
		return false
	}
}

func piGeneratedAgentDir(opts runtimecore.ExecOptions) string {
	return filepath.Join(piBridgeDir(opts), "agent")
}

func writePiGeneratedModelsFile(agentDir, provider string, profile llmprofile.LLMProfile) error {
	if err := os.MkdirAll(agentDir, 0o700); err != nil {
		return fmt.Errorf("create pi generated model registry dir: %w", err)
	}
	contextWindow := agentcore.EffectiveRuntimeContextTokenLimit(map[string]string{
		"llm_context_length": strconv.Itoa(profile.ContextLength),
	})
	model := piModelEntry{
		ID:            profile.Model,
		Name:          profile.DisplayName,
		Input:         piGeneratedModelInput(provider),
		ContextWindow: contextWindow,
		MaxTokens:     agentcore.RuntimeContextOutputReserveTokens(contextWindow),
	}
	cfg := piModelsFile{Providers: map[string]piModelsProvider{
		provider: {
			BaseURL: profile.BaseURL,
			API:     piGeneratedProviderAPI(provider),
			APIKey:  piGeneratedProviderAPIKey(provider),
			Headers: piGeneratedProviderHeaders(provider),
			Compat:  piGeneratedProviderCompat(provider),
			Models:  []piModelEntry{model},
		},
	}}
	raw, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("encode pi generated model registry: %w", err)
	}
	if err := os.WriteFile(filepath.Join(agentDir, "models.json"), raw, 0o600); err != nil {
		return fmt.Errorf("write pi generated model registry: %w", err)
	}
	return nil
}

func piGeneratedModelInput(provider string) []string {
	if piGeneratedProviderAPI(provider) == "openai-responses" {
		return []string{"text", "image"}
	}
	return []string{"text"}
}

func piGeneratedProviderAPI(provider string) string {
	if piProviderAlias(provider) == "custom-openai-responses" {
		return "openai-responses"
	}
	return "openai-completions"
}

func piGeneratedProviderAPIKey(provider string) string {
	if piProviderAlias(provider) == "ollama" {
		return "ollama"
	}
	return "$ORRERY_PI_LLM_API_KEY"
}

func piGeneratedProviderHeaders(provider string) map[string]string {
	switch piProviderAlias(provider) {
	case "custom-openai", "custom-openai-responses", "qwen":
		return map[string]string{"User-Agent": "Orrery/1.0"}
	default:
		return nil
	}
}

func piGeneratedProviderCompat(provider string) map[string]any {
	switch piProviderAlias(provider) {
	case "ollama":
		return map[string]any{
			"supportsDeveloperRole":   false,
			"supportsReasoningEffort": false,
		}
	default:
		return nil
	}
}

func selectPiLLMProfile(ctx context.Context, opts runtimecore.ExecOptions, cfg Config) (*llmprofile.LLMProfile, error) {
	if cfg.LLMProfiles == nil || !shouldSelectPiLLMProfile(opts, cfg) {
		return nil, nil
	}
	selection, err := llmprofile.Resolve(ctx, cfg.LLMProfiles, llmprofile.SelectionRequest{
		ProfileID: opts.ProviderSpecific["llm_profile_id"],
	})
	if err != nil {
		return nil, fmt.Errorf("select pi llm profile: %w", err)
	}
	return compatiblePiLLMProfile(opts, selection.Profile), nil
}

func shouldSelectPiLLMProfile(opts runtimecore.ExecOptions, cfg Config) bool {
	if strings.TrimSpace(opts.ProviderSpecific["llm_profile_id"]) != "" {
		return true
	}
	if !hasPiLLMIdentity(opts) {
		return true
	}
	if hasPiSpecificAPIKeyRef(opts.ProviderSpecific) {
		return false
	}
	return !hasRuntimePiAPIKey(opts, cfg)
}

func hasPiLLMIdentity(opts runtimecore.ExecOptions) bool {
	if firstNonEmpty(
		piProviderFromArgs(opts.ExtraArgs),
		piProviderFromArgs(opts.CustomArgs),
		piProviderFromModel(opts.Model),
		opts.ProviderSpecific["llm_provider"],
		opts.ProviderSpecific["llm_model"],
		opts.ProviderSpecific["llm_base_url"],
	) != "" {
		return true
	}
	return false
}

func hasPiSpecificAPIKeyRef(providerSpecific map[string]string) bool {
	ref := firstNonEmpty(providerSpecific["llm_api_key_ref"], providerSpecific["api_key_ref"])
	if ref == "" {
		return false
	}
	return !isLegacyBaseAgentAPIKeyRef(ref)
}

func isLegacyBaseAgentAPIKeyRef(ref string) bool {
	return strings.EqualFold(strings.TrimSpace(ref), agentcore.DefaultSystemLLMAPIKeyRef)
}

func shouldOverridePiLLMIdentityWithProfile(opts runtimecore.ExecOptions) bool {
	if strings.TrimSpace(opts.ProviderSpecific["llm_profile_id"]) != "" {
		return true
	}
	return !hasPiSpecificAPIKeyRef(opts.ProviderSpecific)
}

func hasRuntimePiAPIKey(opts runtimecore.ExecOptions, cfg Config) bool {
	provider := firstNonEmpty(piProviderFromArgs(opts.ExtraArgs), piProviderFromArgs(opts.CustomArgs), piProviderFromModel(opts.Model), opts.ProviderSpecific["llm_provider"])
	envKey := piAPIKeyEnvName(provider)
	if envKey == "" {
		return false
	}
	env := mergePiEnvironment(cfg.Env, opts.Environment)
	if strings.TrimSpace(env[envKey]) != "" {
		return true
	}
	return strings.TrimSpace(os.Getenv(envKey)) != ""
}

func compatiblePiLLMProfile(opts runtimecore.ExecOptions, profile llmprofile.LLMProfile) *llmprofile.LLMProfile {
	if shouldOverridePiLLMIdentityWithProfile(opts) {
		return &profile
	}
	provider := firstNonEmpty(piProviderFromArgs(opts.ExtraArgs), piProviderFromArgs(opts.CustomArgs), piProviderFromModel(opts.Model), opts.ProviderSpecific["llm_provider"])
	if provider != "" && profile.Provider != "" && piProviderAlias(provider) != piProviderAlias(profile.Provider) {
		return nil
	}
	return &profile
}

func normalizePiLLMProfile(profile llmprofile.LLMProfile) llmprofile.LLMProfile {
	profile.ProfileID = strings.TrimSpace(profile.ProfileID)
	profile.DisplayName = strings.TrimSpace(profile.DisplayName)
	profile.Provider = strings.TrimSpace(profile.Provider)
	profile.BaseURL = strings.TrimSpace(profile.BaseURL)
	profile.APIKey = strings.TrimSpace(profile.APIKey)
	profile.APIKeyRef = strings.TrimSpace(profile.APIKeyRef)
	profile.Model = strings.TrimSpace(profile.Model)
	return profile
}

func applyPiLLMProfileDefaults(opts runtimecore.ExecOptions, profile llmprofile.LLMProfile, overrideIdentity bool) runtimecore.ExecOptions {
	if opts.ProviderSpecific == nil {
		opts.ProviderSpecific = map[string]string{}
	}
	overrideKeyRef := overrideIdentity || !hasPiLLMIdentity(opts) || !hasPiSpecificAPIKeyRef(opts.ProviderSpecific)
	fillStringDefault(opts.ProviderSpecific, "llm_profile_id", profile.ProfileID)
	if overrideIdentity {
		opts.ExtraArgs = stripPiProviderArgs(opts.ExtraArgs)
		opts.CustomArgs = stripPiProviderArgs(opts.CustomArgs)
		if provider := piProviderAlias(profile.Provider); provider != "" {
			opts.ProviderSpecific["llm_provider"] = provider
		}
		if profile.BaseURL != "" {
			opts.ProviderSpecific["llm_base_url"] = profile.BaseURL
		}
		if profile.Model != "" {
			opts.Model = profile.Model
			opts.ProviderSpecific["llm_model"] = profile.Model
		}
	} else {
		fillStringDefault(opts.ProviderSpecific, "llm_provider", piProviderAlias(profile.Provider))
		fillStringDefault(opts.ProviderSpecific, "llm_base_url", profile.BaseURL)
		fillStringDefault(opts.ProviderSpecific, "llm_model", profile.Model)
	}
	if profile.APIKeyRef != "" {
		if overrideKeyRef || strings.TrimSpace(opts.ProviderSpecific["api_key_ref"]) == "" {
			opts.ProviderSpecific["api_key_ref"] = profile.APIKeyRef
		}
		if overrideKeyRef || strings.TrimSpace(opts.ProviderSpecific["llm_api_key_ref"]) == "" {
			opts.ProviderSpecific["llm_api_key_ref"] = profile.APIKeyRef
		}
	}
	if profile.ContextLength > 0 {
		fillStringDefault(opts.ProviderSpecific, "llm_context_length", fmt.Sprintf("%d", profile.ContextLength))
	}
	if profile.Temperature != 0 {
		fillStringDefault(opts.ProviderSpecific, "llm_temperature", fmt.Sprintf("%g", profile.Temperature))
	}
	return opts
}

func stripPiProviderArgs(args []string) []string {
	if len(args) == 0 {
		return nil
	}
	out := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		arg := strings.TrimSpace(args[i])
		if arg == "--provider" {
			if i+1 < len(args) {
				i++
			}
			continue
		}
		if strings.HasPrefix(arg, "--provider=") {
			continue
		}
		out = append(out, args[i])
	}
	return out
}

func applyProviderSpecificPiOptions(opts runtimecore.ExecOptions) runtimecore.ExecOptions {
	if opts.ProviderSpecific == nil {
		return opts
	}
	if strings.TrimSpace(opts.Model) == "" {
		opts.Model = strings.TrimSpace(opts.ProviderSpecific["llm_model"])
	}
	provider := piProviderAlias(opts.ProviderSpecific["llm_provider"])
	if provider != "" {
		opts.ProviderSpecific["llm_provider"] = provider
	}
	if provider == "" || piProviderFromArgs(opts.ExtraArgs) != "" || piProviderFromArgs(opts.CustomArgs) != "" || piProviderFromModel(opts.Model) != "" {
		return opts
	}
	opts.ExtraArgs = append([]string{"--provider", provider}, opts.ExtraArgs...)
	return opts
}

func fillStringDefault(values map[string]string, key, value string) {
	value = strings.TrimSpace(value)
	if value == "" || strings.TrimSpace(values[key]) != "" {
		return
	}
	values[key] = value
}

func piProviderFromArgs(args []string) string {
	for i := 0; i < len(args); i++ {
		arg := strings.TrimSpace(args[i])
		if arg == "--provider" && i+1 < len(args) {
			return strings.TrimSpace(args[i+1])
		}
		if strings.HasPrefix(arg, "--provider=") {
			return strings.TrimSpace(strings.TrimPrefix(arg, "--provider="))
		}
	}
	return ""
}

func piProviderFromModel(model string) string {
	model = strings.TrimSpace(model)
	if model == "" {
		return ""
	}
	provider, _, ok := strings.Cut(model, "/")
	if !ok {
		return ""
	}
	return strings.TrimSpace(provider)
}

func piAPIKeyEnvName(provider string) string {
	switch piProviderAlias(provider) {
	case "anthropic":
		return "ANTHROPIC_API_KEY"
	case "ant-ling":
		return "ANT_LING_API_KEY"
	case "azure-openai-responses":
		return "AZURE_OPENAI_API_KEY"
	case "openai":
		return "OPENAI_API_KEY"
	case "deepseek":
		return "DEEPSEEK_API_KEY"
	case "nvidia":
		return "NVIDIA_API_KEY"
	case "google", "gemini":
		return "GEMINI_API_KEY"
	case "mistral":
		return "MISTRAL_API_KEY"
	case "groq":
		return "GROQ_API_KEY"
	case "cerebras":
		return "CEREBRAS_API_KEY"
	case "cloudflare-ai-gateway", "cloudflare-workers-ai":
		return "CLOUDFLARE_API_KEY"
	case "xai":
		return "XAI_API_KEY"
	case "openrouter":
		return "OPENROUTER_API_KEY"
	case "vercel-ai-gateway":
		return "AI_GATEWAY_API_KEY"
	case "zai":
		return "ZAI_API_KEY"
	case "zai-coding-cn":
		return "ZAI_CODING_CN_API_KEY"
	case "opencode", "opencode-go":
		return "OPENCODE_API_KEY"
	case "huggingface":
		return "HF_TOKEN"
	case "fireworks":
		return "FIREWORKS_API_KEY"
	case "together":
		return "TOGETHER_API_KEY"
	case "kimi-coding":
		return "KIMI_API_KEY"
	case "moonshotai", "moonshotai-cn":
		return "MOONSHOT_API_KEY"
	case "minimax":
		return "MINIMAX_API_KEY"
	case "minimax-cn":
		return "MINIMAX_CN_API_KEY"
	case "custom-openai", "custom-openai-responses", "qwen", "ollama":
		return "ORRERY_PI_LLM_API_KEY"
	case "xiaomi":
		return "XIAOMI_API_KEY"
	case "xiaomi-token-plan-cn":
		return "XIAOMI_TOKEN_PLAN_CN_API_KEY"
	case "xiaomi-token-plan-ams":
		return "XIAOMI_TOKEN_PLAN_AMS_API_KEY"
	case "xiaomi-token-plan-sgp":
		return "XIAOMI_TOKEN_PLAN_SGP_API_KEY"
	default:
		return ""
	}
}

func normalizePiProvider(provider string) string {
	provider = strings.ToLower(strings.TrimSpace(provider))
	provider = strings.ReplaceAll(provider, "_", "-")
	return provider
}

func piProviderAlias(provider string) string {
	switch normalizePiProvider(provider) {
	case "azure-openai", "azure-openai-responses", "azureopenai":
		return "azure-openai-responses"
	case "custom-openai", "custom-openai-compatible", "openai-compatible":
		return "custom-openai"
	case "custom-openai-responses", "openai-responses-compatible":
		return "custom-openai-responses"
	case "gemini":
		return "google"
	case "glm", "zhipu", "zhipuai", "bigmodel", "bigmodel-cn", "z-ai":
		return "zai"
	case "moonshot", "moonshot-cn", "moonshotai-cn", "kimi":
		return "moonshotai-cn"
	case "dashscope", "aliyun-dashscope":
		return "qwen"
	default:
		return normalizePiProvider(provider)
	}
}
