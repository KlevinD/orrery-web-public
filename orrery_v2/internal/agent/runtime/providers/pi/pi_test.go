package pi

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	runtimecore "orrery_v2/internal/agent/runtime/core"
	llmprofile "orrery_v2/internal/agent/runtime/llmprofile"
)

func TestBuildArgsUsesPiRPCModeAndFiltersOverrides(t *testing.T) {
	args := BuildArgs("do work", runtimecore.ExecOptions{
		Model:           "openai/gpt-5",
		SystemPrompt:    "system",
		ResumeSessionID: "session-1",
		Environment: map[string]string{
			"ORRERY_AGENT_CONTEXT_PATH": filepath.Join("D:", "tmp", "AGENT_CONTEXT.md"),
			"ORRERY_PI_SKILL_PATHS":     filepath.Join("D:", "tmp", "skill-one") + string(os.PathListSeparator) + filepath.Join("D:", "tmp", "skill-two"),
		},
		ExtraArgs:  []string{"--mode", "text", "--print", "--no-session=false", "--tools", "bash", "--skill", "bad", "--no-skills", "--foo", "bar"},
		CustomArgs: []string{"-p", "bad", "--approve=false", "--no-extensions"},
	})

	want := []string{
		"--mode", "rpc",
		"--no-extensions",
		"--no-skills",
		"--no-prompt-templates",
		"--no-themes",
		"--no-context-files",
		"--no-approve",
		"--model", "openai/gpt-5",
		"--session", "session-1",
		"--skill", filepath.Join("D:", "tmp", "skill-one"),
		"--skill", filepath.Join("D:", "tmp", "skill-two"),
		"--foo", "bar",
	}
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("args = %#v, want %#v", args, want)
	}
}

func TestResolvePiExecutableKeepsConfiguredAbsolutePathForBareDefault(t *testing.T) {
	configured := filepath.Join(t.TempDir(), "runtime", "pi")
	if got := resolvePiExecutable(configured, "pi", t.TempDir()); got != configured {
		t.Fatalf("resolved executable = %q, want configured path %q", got, configured)
	}
}

func TestResolvePiExecutableHonorsExplicitCustomPath(t *testing.T) {
	configured := filepath.Join(t.TempDir(), "runtime", "pi")
	selected := filepath.Join(t.TempDir(), "custom", "pi")
	if got := resolvePiExecutable(configured, selected, t.TempDir()); got != selected {
		t.Fatalf("resolved executable = %q, want selected path %q", got, selected)
	}
}

func TestResolvePiExecutableUsesStableBaseDirectoryOutsideRuntimeWorkspace(t *testing.T) {
	root := t.TempDir()
	executable := filepath.Join(root, "runtime", "pi-coding-agent", "node_modules", ".bin", bundledExecutableNames()[0])
	if err := os.MkdirAll(filepath.Dir(executable), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(executable, []byte("fixture"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ORRERY_BASE_DIR", root)
	if got := resolvePiExecutable("", "pi", t.TempDir()); got != executable {
		t.Fatalf("resolved executable = %q, want stable base directory path %q", got, executable)
	}
}

func TestPrepareDynamicSystemPromptDoesNotRepeatStructuredDiscussionContext(t *testing.T) {
	root := t.TempDir()
	workDir := filepath.Join(root, "agent")
	contextPath := filepath.Join(root, "AGENT_CONTEXT.md")
	if err := os.WriteFile(contextPath, []byte("current user input repeated in context"), 0o600); err != nil {
		t.Fatal(err)
	}

	opts, systemPrompt, err := prepareDynamicSystemPrompt(runtimecore.ExecOptions{
		Cwd: workDir,
		WorkspacePolicy: agentcore.RuntimeWorkspacePolicy{
			Root:            root,
			WorkDir:         workDir,
			EnforceBoundary: true,
		},
		Environment: map[string]string{"ORRERY_AGENT_CONTEXT_PATH": contextPath},
		ProviderSpecific: map[string]string{
			"discussion_external_cli_structured_prompt": "true",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(systemPrompt, "Workspace boundary:") {
		t.Fatalf("system prompt lost workspace boundary: %q", systemPrompt)
	}
	if strings.Contains(systemPrompt, "current user input repeated in context") {
		t.Fatalf("structured discussion context was injected into both system and user prompts: %q", systemPrompt)
	}
	if strings.TrimSpace(opts.Environment[piSystemPromptPathEnv]) == "" {
		t.Fatal("dynamic system prompt path was not prepared")
	}
}

func TestPrepareDynamicSystemPromptTrimsToolSummaryForScheduledTask(t *testing.T) {
	contextPath := filepath.Join(t.TempDir(), "AGENT_CONTEXT.md")
	contextText := strings.Join([]string{
		"# Agent Runtime Context",
		"Keep the scheduled task instructions.",
		"# Tool Summary",
		"remove-this-generated-tool-catalog",
		"# Skills",
		"Keep the bound skill instructions.",
	}, "\n")
	if err := os.WriteFile(contextPath, []byte(contextText), 0o600); err != nil {
		t.Fatal(err)
	}

	_, systemPrompt, err := prepareDynamicSystemPrompt(runtimecore.ExecOptions{
		Cwd:         t.TempDir(),
		Environment: map[string]string{"ORRERY_AGENT_CONTEXT_PATH": contextPath, "PI_CODING_AGENT_DIR": t.TempDir()},
		ProviderSpecific: map[string]string{
			"scheduled_task_id": "scheduled-task-1",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Keep the scheduled task instructions.", "# Skills", "Keep the bound skill instructions."} {
		if !strings.Contains(systemPrompt, want) {
			t.Fatalf("scheduled system prompt lost %q: %q", want, systemPrompt)
		}
	}
	if strings.Contains(systemPrompt, "remove-this-generated-tool-catalog") {
		t.Fatalf("scheduled system prompt retained generated tool summary: %q", systemPrompt)
	}
}

func TestBuildArgsKeepsNonDiscussionRunsEphemeral(t *testing.T) {
	args := BuildArgs("do work", runtimecore.ExecOptions{})
	if !containsArg(args, "--no-session") {
		t.Fatalf("non-discussion pi run should remain ephemeral: %#v", args)
	}
}

func TestBuildArgsPersistsFirstDiscussionSession(t *testing.T) {
	args := BuildArgs("do work", runtimecore.ExecOptions{
		ProviderSpecific: map[string]string{"discussion_external_cli_structured_prompt": "true"},
	})
	if containsArg(args, "--no-session") {
		t.Fatalf("discussion pi run must persist its session for the next turn: %#v", args)
	}
}

func TestBuildArgsPersistsSessionForDiscussionAffinity(t *testing.T) {
	args := BuildArgs("do work", runtimecore.ExecOptions{
		ProviderSpecific: map[string]string{"discussion_id": "discussion-1"},
	})
	if containsArg(args, "--no-session") {
		t.Fatalf("discussion affinity should retain pi session state: %#v", args)
	}
}

func TestPreparePiSessionPersistenceUsesStableWorkspaceDirectory(t *testing.T) {
	workspaceDir := t.TempDir()
	want := filepath.Join(workspaceDir, ".orrery", "pi-sessions")
	for _, runtimeHome := range []string{
		filepath.Join(t.TempDir(), "run-1", "pi-home"),
		filepath.Join(t.TempDir(), "run-2", "pi-home"),
	} {
		opts, err := preparePiSessionPersistence(runtimecore.ExecOptions{
			Cwd: workspaceDir,
			Environment: map[string]string{
				"PI_CODING_AGENT_DIR": runtimeHome,
			},
			ProviderSpecific: map[string]string{"discussion_external_cli_structured_prompt": "true"},
		}, Config{})
		if err != nil {
			t.Fatalf("prepare pi session persistence: %v", err)
		}
		if got := opts.Environment[piSessionDirEnv]; got != want {
			t.Fatalf("session dir = %q, want stable workspace dir %q", got, want)
		}
	}
	if info, err := os.Stat(want); err != nil || !info.IsDir() {
		t.Fatalf("stable pi session directory was not created: info=%v err=%v", info, err)
	}
}

func TestStabilizePiRPCSkillsKeepsCanonicalLeafDirectoryName(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "web-research")
	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "SKILL.md"), []byte("---\nname: web-research\n---\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	opts, err := stabilizePiRPCOptions(runtimecore.ExecOptions{
		Cwd: root,
		Environment: map[string]string{
			"ORRERY_PI_SKILL_PATHS": source,
		},
		ProviderSpecific: map[string]string{"discussion_id": "discussion-1"},
	})
	if err != nil {
		t.Fatalf("stabilize Pi skills: %v", err)
	}
	paths := runtimeListEnv(opts.Environment["ORRERY_PI_SKILL_PATHS"])
	if len(paths) != 1 || filepath.Base(paths[0]) != "web-research" {
		t.Fatalf("stable Pi skill path = %#v, want canonical leaf", paths)
	}
	if filepath.Dir(paths[0]) == filepath.Join(opts.Environment["PI_CODING_AGENT_DIR"], "orrery-runtime", "skills") {
		t.Fatalf("stable Pi skill path did not retain a content-scoped parent: %q", paths[0])
	}
}

func TestBuildArgsAlwaysDisablesProjectTrustAndUsesOrreryApprovalBridge(t *testing.T) {
	defaultArgs := BuildArgs("do work", runtimecore.ExecOptions{})
	if containsArg(defaultArgs, "--approve") || !containsArg(defaultArgs, "--no-approve") {
		t.Fatalf("default pi args must disable project trust and retain Orrery HIL: %#v", defaultArgs)
	}

	autoArgs := BuildArgs("do work", runtimecore.ExecOptions{ProviderConfig: json.RawMessage(`{"approval_mode":"auto"}`)})
	if containsArg(autoArgs, "--approve") || !containsArg(autoArgs, "--no-approve") {
		t.Fatalf("approval policy must not enable project-local pi files: %#v", autoArgs)
	}

	neverArgs := BuildArgs("do work", runtimecore.ExecOptions{ProviderConfig: json.RawMessage(`{"approval_mode":"never"}`)})
	if !containsArg(neverArgs, "--no-approve") {
		t.Fatalf("never approval mode should ignore project-local pi files for this run: %#v", neverArgs)
	}
}

func TestBuildArgsDiaryMaintenanceDisablesBuiltinToolsAndSkills(t *testing.T) {
	args := BuildArgs("write diary", runtimecore.ExecOptions{
		ProviderSpecific: map[string]string{"diary_maintenance": "true"},
		Environment:      map[string]string{"ORRERY_PI_SKILL_PATHS": "D:/skills/unsafe"},
	})
	if !containsArg(args, "--no-builtin-tools") || !containsArg(args, "--no-skills") {
		t.Fatalf("diary maintenance args = %#v, want built-in tools and skills disabled", args)
	}
	if containsArg(args, "--skill") {
		t.Fatalf("diary maintenance loaded inherited skills: %#v", args)
	}
}

func TestBuildArgsDisablesBuiltinToolsForBoundToolsOnlyRuntime(t *testing.T) {
	args := BuildArgs("do work", runtimecore.ExecOptions{
		ProviderConfig: json.RawMessage(`{"approval_mode":"auto","bound_tools_only":true}`),
	})
	if !containsArg(args, "--no-builtin-tools") {
		t.Fatalf("args = %v, want --no-builtin-tools", args)
	}
}

func TestBuildArgsDisablesBuiltinToolsForExplicitScopeResponsibility(t *testing.T) {
	args := BuildArgs("do work", runtimecore.ExecOptions{
		ProviderConfig:   json.RawMessage(`{"approval_mode":"auto","bound_tools_only":false}`),
		ProviderSpecific: map[string]string{agentcore.RuntimeRequireExplicitToolScopesProviderKey: "true"},
	})
	if !containsArg(args, "--no-builtin-tools") {
		t.Fatalf("args = %v, want --no-builtin-tools", args)
	}
}

func TestPrepareSystemPromptFileInjectsWorkspaceBoundary(t *testing.T) {
	opts, err := prepareSystemPromptFile(runtimecore.ExecOptions{
		Cwd:            "D:/workspace/missions/m/tasks/t/runs/r",
		Environment:    map[string]string{"PI_CODING_AGENT_DIR": t.TempDir()},
		ProviderConfig: json.RawMessage(`{"approval_mode":"auto"}`),
		WorkspacePolicy: agentcore.RuntimeWorkspacePolicy{
			Root:            "D:/workspace",
			WorkDir:         "D:/workspace/missions/m/tasks/t/runs/r",
			WritableRoots:   []string{"D:/workspace"},
			SandboxMode:     "workspace-write",
			ApprovalPolicy:  "on-request",
			EnforceBoundary: true,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	path := opts.Environment[piSystemPromptPathEnv]
	if content := readPiTestFile(t, path); !strings.Contains(content, "Workspace root: D:/workspace") {
		t.Fatalf("workspace boundary prompt missing from %s: %q", path, content)
	}
}

func TestBuildArgsLoadsBridgeExtension(t *testing.T) {
	args := BuildArgs("do work", runtimecore.ExecOptions{
		Environment: map[string]string{"ORRERY_PI_EXTENSION_PATH": "D:/tmp/orrery-runtime-extension.ts"},
	})
	if !containsArgValue(args, "--extension", "D:/tmp/orrery-runtime-extension.ts") {
		t.Fatalf("bridge extension arg missing: %#v", args)
	}
}

func TestRuntimeConfigAppliesModelProviderAndEnv(t *testing.T) {
	opts, runtimeCfg := applyRuntimeConfig(runtimecore.ExecOptions{
		ProviderConfig: json.RawMessage(`{"model":"MiniMax-M2.7","extra_args":["--provider","minimax-cn"],"env":{"MINIMAX_CN_API_KEY":"token-plan"}}`),
	}, &Config{})

	if runtimeCfg.Model != "MiniMax-M2.7" || opts.Model != "MiniMax-M2.7" {
		t.Fatalf("runtime model not applied: runtime=%q opts=%q", runtimeCfg.Model, opts.Model)
	}
	args := BuildArgs("do work", opts)
	for _, want := range []string{"--model", "MiniMax-M2.7", "--provider", "minimax-cn"} {
		if !containsArg(args, want) {
			t.Fatalf("args missing %q: %#v", want, args)
		}
	}
}

func TestRuntimeConfigParsesStringTimeoutsWithoutDroppingOtherFields(t *testing.T) {
	cfg := parseRuntimeConfig(json.RawMessage(`{
		"approval_mode":"on-request",
		"approval_timeout_ms":"600000",
		"prompt_ack_timeout_ms":"90000",
		"debug":{"capture_runtime_input":true,"output_dir":"D:/debug"},
		"executable":"pi"
	}`))

	if cfg.ApprovalMode != "on-request" || cfg.ApprovalTimeoutMs != 600000 || cfg.PromptAckTimeoutMs != 90000 || cfg.Executable != "pi" {
		t.Fatalf("runtime config = %+v", cfg)
	}
	if !cfg.Debug.CaptureRuntimeInput || cfg.Debug.OutputDir != "D:/debug" {
		t.Fatalf("debug config = %+v", cfg.Debug)
	}
}

func TestPiRPCPromptAckTimeoutUsesDedicatedRuntimeSetting(t *testing.T) {
	opts := runtimecore.ExecOptions{ProviderConfig: json.RawMessage(`{"prompt_ack_timeout_ms":"90000"}`)}
	if got := piRPCPromptAckTimeout(opts); got != 90*time.Second {
		t.Fatalf("prompt ack timeout = %s, want 90s", got)
	}
	if got := piRPCPromptAckTimeout(runtimecore.ExecOptions{}); got != 60*time.Second {
		t.Fatalf("default prompt ack timeout = %s, want 60s", got)
	}
}

func TestPiRPCPromptAckTimeoutAllowsLongerScheduledTaskTimeout(t *testing.T) {
	opts := runtimecore.ExecOptions{
		ProviderConfig: json.RawMessage(`{"prompt_ack_timeout_ms":"60000"}`),
		ProviderSpecific: map[string]string{
			runtimecore.ToolCallTimeoutSecondsProviderKey: "1800",
		},
	}
	if got := piRPCPromptAckTimeout(opts); got != 30*time.Minute {
		t.Fatalf("prompt ack timeout = %s, want 30m", got)
	}
}

func TestApplyLLMProfileRuntimeEnvResolvesMiniMaxCNSecret(t *testing.T) {
	_, cfg, err := applyLLMProfileRuntimeEnv(context.Background(), runtimecore.ExecOptions{
		Model:     "MiniMax-M2.7",
		ExtraArgs: []string{"--provider", "minimax-cn"},
		ProviderSpecific: map[string]string{
			"llm_provider":    "minimax-cn",
			"llm_api_key_ref": "secret://llm/minimax-cn",
		},
	}, Config{SecretResolver: staticPiSecretResolver{"secret://llm/minimax-cn": "  token-plan  "}})
	if err != nil {
		t.Fatalf("apply llm env: %v", err)
	}
	if cfg.Env["MINIMAX_CN_API_KEY"] != "token-plan" {
		t.Fatalf("env = %#v, want resolved MiniMax China key", cfg.Env)
	}
}

func TestApplyLLMProfileRuntimeEnvUsesProviderSpecificProfileDefaults(t *testing.T) {
	opts, cfg, err := applyLLMProfileRuntimeEnv(context.Background(), runtimecore.ExecOptions{
		ProviderSpecific: map[string]string{
			"llm_provider":    "minimax-cn",
			"llm_model":       "MiniMax-M2.7",
			"llm_api_key_ref": "secret://llm/global-minimax",
		},
	}, Config{SecretResolver: staticPiSecretResolver{"secret://llm/global-minimax": "  token-global  "}})
	if err != nil {
		t.Fatalf("apply llm env: %v", err)
	}
	if cfg.Env["MINIMAX_CN_API_KEY"] != "token-global" {
		t.Fatalf("env = %#v, want resolved global MiniMax China key", cfg.Env)
	}
	args := BuildArgs("do work", opts)
	if !containsArgValue(args, "--model", "MiniMax-M2.7") || !containsArgValue(args, "--provider", "minimax-cn") {
		t.Fatalf("args = %#v, want model and provider from provider-specific llm profile", args)
	}
}

func TestApplyLLMProfileRuntimeEnvUsesGlobalProfileKeyWhenOnlyLegacyKeyRefExists(t *testing.T) {
	opts, cfg, err := applyLLMProfileRuntimeEnv(context.Background(), runtimecore.ExecOptions{
		ProviderSpecific: map[string]string{
			"llm_provider":    "minimax-cn",
			"llm_model":       "MiniMax-M2.7",
			"llm_api_key_ref": "secret://system/llm-api-key",
		},
	}, Config{
		LLMProfiles: llmprofile.StaticProfileProvider{Profiles: []llmprofile.LLMProfile{{
			ProfileID:     "llm-global-minimax",
			Provider:      "minimax-cn",
			BaseURL:       "https://api.minimax.chat/v1",
			APIKeyRef:     "secret://llm/global-minimax",
			Model:         "MiniMax-M2.7",
			Enabled:       true,
			GlobalDefault: true,
		}}},
		SecretResolver: staticPiSecretResolver{
			"secret://system/llm-api-key": "",
			"secret://llm/global-minimax": "  token-global  ",
		},
	})
	if err != nil {
		t.Fatalf("apply llm env: %v", err)
	}
	if cfg.Env["MINIMAX_CN_API_KEY"] != "token-global" {
		t.Fatalf("env = %#v, want global profile key instead of legacy base-agent key ref", cfg.Env)
	}
	if opts.ProviderSpecific["llm_api_key_ref"] != "secret://llm/global-minimax" {
		t.Fatalf("provider specific = %#v, want global profile key ref for pi", opts.ProviderSpecific)
	}
}

func TestApplyLLMProfileRuntimeEnvOverridesRuntimeProviderWhenRuntimeHasNoKey(t *testing.T) {
	opts, cfg, err := applyLLMProfileRuntimeEnv(context.Background(), runtimecore.ExecOptions{
		Model:     "MiniMax-M2.7",
		ExtraArgs: []string{"--provider", "minimax-cn"},
		ProviderSpecific: map[string]string{
			"llm_api_key_ref": "secret://system/llm-api-key",
		},
	}, Config{
		LLMProfiles: llmprofile.StaticProfileProvider{Profiles: []llmprofile.LLMProfile{{
			ProfileID:     "llm-global-glm",
			Provider:      "glm",
			BaseURL:       "https://open.bigmodel.cn/api/paas/v4",
			APIKeyRef:     "secret://llm/glm",
			Model:         "glm-5.2",
			Enabled:       true,
			GlobalDefault: true,
		}}},
		SecretResolver: staticPiSecretResolver{
			"secret://system/llm-api-key": "",
			"secret://llm/glm":            "  token-glm  ",
		},
	})
	if err != nil {
		t.Fatalf("apply llm env: %v", err)
	}
	if cfg.Env["ZAI_API_KEY"] != "token-glm" {
		t.Fatalf("env = %#v, want global GLM key exported as PI ZAI_API_KEY", cfg.Env)
	}
	if cfg.Env["MINIMAX_CN_API_KEY"] != "" {
		t.Fatalf("env = %#v, should not keep MiniMax env when runtime has no key", cfg.Env)
	}
	if opts.Model != "glm-5.2" {
		t.Fatalf("model = %q, want global profile model", opts.Model)
	}
	args := BuildArgs("do work", opts)
	if !containsArgValue(args, "--provider", "zai") || containsArgValue(args, "--provider", "minimax-cn") {
		t.Fatalf("args = %#v, want global profile provider replacing runtime provider", args)
	}
	if opts.ProviderSpecific["llm_api_key_ref"] != "secret://llm/glm" || opts.ProviderSpecific["llm_provider"] != "zai" {
		t.Fatalf("provider specific = %#v, want global profile provider and key ref", opts.ProviderSpecific)
	}
}

func TestApplyLLMProfileRuntimeEnvMapsGLMProfileToPiZAIProvider(t *testing.T) {
	opts, cfg, err := applyLLMProfileRuntimeEnv(context.Background(), runtimecore.ExecOptions{
		ProviderSpecific: map[string]string{
			"llm_provider":    "glm",
			"llm_model":       "glm-5.2",
			"llm_api_key_ref": "secret://llm/glm",
		},
	}, Config{SecretResolver: staticPiSecretResolver{"secret://llm/glm": "  token-glm  "}})
	if err != nil {
		t.Fatalf("apply llm env: %v", err)
	}
	if cfg.Env["ZAI_API_KEY"] != "token-glm" {
		t.Fatalf("env = %#v, want GLM profile key exported as PI ZAI_API_KEY", cfg.Env)
	}
	args := BuildArgs("do work", opts)
	if !containsArgValue(args, "--model", "glm-5.2") || !containsArgValue(args, "--provider", "zai") {
		t.Fatalf("args = %#v, want GLM profile mapped to PI zai provider", args)
	}
	if opts.ProviderSpecific["llm_provider"] != "zai" {
		t.Fatalf("provider specific = %#v, want normalized pi provider", opts.ProviderSpecific)
	}
}

func TestApplyLLMProfileRuntimeEnvRegistersCustomOpenAIProfileForPi(t *testing.T) {
	cwd := t.TempDir()
	opts, cfg, err := applyLLMProfileRuntimeEnv(context.Background(), runtimecore.ExecOptions{
		RunID: "run-custom-openai",
		Cwd:   cwd,
	}, Config{
		LLMProfiles: llmprofile.StaticProfileProvider{Profiles: []llmprofile.LLMProfile{{
			ProfileID:     "llm-custom-openai",
			DisplayName:   "Custom OpenAI",
			Provider:      "custom_openai",
			BaseURL:       "https://llm.example.com/v1",
			APIKey:        "token-custom",
			Model:         "custom-model",
			ContextLength: 64000,
			Enabled:       true,
			GlobalDefault: true,
		}}},
	})
	if err != nil {
		t.Fatalf("apply llm env: %v", err)
	}
	if cfg.Env["ORRERY_PI_LLM_API_KEY"] != "token-custom" {
		t.Fatalf("env = %#v, want generated custom provider key", cfg.Env)
	}
	args := BuildArgs("do work", opts)
	if !containsArgValue(args, "--provider", "custom-openai") || !containsArgValue(args, "--model", "custom-model") {
		t.Fatalf("args = %#v, want custom OpenAI provider and model", args)
	}
	agentDir := opts.Environment["PI_CODING_AGENT_DIR"]
	if agentDir == "" {
		t.Fatalf("environment = %#v, want generated PI_CODING_AGENT_DIR", opts.Environment)
	}
	raw := readPiTestFile(t, filepath.Join(agentDir, "models.json"))
	if !strings.Contains(raw, `"custom-openai"`) ||
		!strings.Contains(raw, `"baseUrl": "https://llm.example.com/v1"`) ||
		!strings.Contains(raw, `"apiKey": "$ORRERY_PI_LLM_API_KEY"`) ||
		!strings.Contains(raw, `"User-Agent": "Orrery/1.0"`) ||
		!strings.Contains(raw, `"id": "custom-model"`) ||
		!strings.Contains(raw, `"contextWindow": 16384`) ||
		!strings.Contains(raw, `"maxTokens": 4096`) {
		t.Fatalf("generated models.json = %s", raw)
	}
	assertPiGeneratedModelInput(t, raw, "custom-openai", []string{"text"})
}

func TestApplyLLMProfileRuntimeEnvRegistersCustomOpenAIResponsesProfileForPi(t *testing.T) {
	cwd := t.TempDir()
	opts, cfg, err := applyLLMProfileRuntimeEnv(context.Background(), runtimecore.ExecOptions{
		RunID: "run-custom-openai-responses",
		Cwd:   cwd,
		ProviderSpecific: map[string]string{
			runtimecore.ToolCallTimeoutSecondsProviderKey: "1800",
		},
	}, Config{
		LLMProfiles: llmprofile.StaticProfileProvider{Profiles: []llmprofile.LLMProfile{{
			ProfileID: "llm-openai-codex-gpt-5.5", DisplayName: "OpenAI Codex GPT-5.5", Provider: "custom-openai-responses",
			BaseURL: "https://relay.example.com/v1", APIKey: "token-responses", Model: "gpt-5.5", Enabled: true,
		}}},
	})
	if err != nil {
		t.Fatalf("apply llm env: %v", err)
	}
	if cfg.Env["ORRERY_PI_LLM_API_KEY"] != "token-responses" {
		t.Fatalf("env = %#v", cfg.Env)
	}
	if !containsArgValue(BuildArgs("do work", opts), "--provider", "custom-openai-responses") {
		t.Fatalf("provider args = %#v", BuildArgs("do work", opts))
	}
	raw := readPiTestFile(t, filepath.Join(opts.Environment["PI_CODING_AGENT_DIR"], "models.json"))
	if !strings.Contains(raw, `"api": "openai-responses"`) || strings.Contains(raw, `"timeoutSeconds"`) {
		t.Fatalf("generated models.json = %s", raw)
	}
	assertPiGeneratedModelInput(t, raw, "custom-openai-responses", []string{"text", "image"})
}

func assertPiGeneratedModelInput(t *testing.T, raw, provider string, want []string) {
	t.Helper()
	var registry piModelsFile
	if err := json.Unmarshal([]byte(raw), &registry); err != nil {
		t.Fatalf("decode generated models.json: %v", err)
	}
	models := registry.Providers[provider].Models
	if len(models) != 1 {
		t.Fatalf("generated models for %q = %#v, want exactly one", provider, models)
	}
	if !reflect.DeepEqual(models[0].Input, want) {
		t.Fatalf("generated model input for %q = %#v, want %#v", provider, models[0].Input, want)
	}
}

func TestApplyLLMProfileRuntimeEnvUsesScheduledFallbackModelOverride(t *testing.T) {
	cwd := t.TempDir()
	opts, _, err := applyLLMProfileRuntimeEnv(context.Background(), runtimecore.ExecOptions{
		RunID: "run-custom-openai-responses-fallback",
		Cwd:   cwd,
		ProviderSpecific: map[string]string{
			"llm_profile_id":     "llm-openai-codex-gpt-5.5",
			"llm_model_override": "gpt-5.6-luna",
		},
	}, Config{
		LLMProfiles: llmprofile.StaticProfileProvider{Profiles: []llmprofile.LLMProfile{{
			ProfileID: "llm-openai-codex-gpt-5.5", Provider: "custom-openai-responses",
			BaseURL: "https://relay.example.com/v1", APIKey: "token-responses", Model: "gpt-5.5", Enabled: true,
		}}},
	})
	if err != nil {
		t.Fatalf("apply llm env: %v", err)
	}
	if !containsArgValue(BuildArgs("do work", opts), "--model", "gpt-5.6-luna") {
		t.Fatalf("args = %#v", BuildArgs("do work", opts))
	}
	raw := readPiTestFile(t, filepath.Join(opts.Environment["PI_CODING_AGENT_DIR"], "models.json"))
	if !strings.Contains(raw, `"id": "gpt-5.6-luna"`) || strings.Contains(raw, `"id": "gpt-5.5"`) {
		t.Fatalf("generated models.json = %s", raw)
	}
}

func TestApplyLLMProfileRuntimeEnvRoutesOpenAIProfileWithCustomBaseURLThroughGeneratedProvider(t *testing.T) {
	cwd := t.TempDir()
	opts, cfg, err := applyLLMProfileRuntimeEnv(context.Background(), runtimecore.ExecOptions{
		RunID: "run-openai-compatible",
		Cwd:   cwd,
	}, Config{
		LLMProfiles: llmprofile.StaticProfileProvider{Profiles: []llmprofile.LLMProfile{{
			ProfileID:     "llm-openai-compatible",
			DisplayName:   "OpenAI Relay",
			Provider:      "openai",
			BaseURL:       "https://relay.example.com/v1",
			APIKey:        "token-relay",
			Model:         "gpt-5.5",
			Enabled:       true,
			GlobalDefault: true,
		}}},
	})
	if err != nil {
		t.Fatalf("apply llm env: %v", err)
	}
	if cfg.Env["ORRERY_PI_LLM_API_KEY"] != "token-relay" || cfg.Env["OPENAI_API_KEY"] != "" {
		t.Fatalf("env = %#v, want relay key isolated to generated provider", cfg.Env)
	}
	args := BuildArgs("do work", opts)
	if !containsArgValue(args, "--provider", "custom-openai") || !containsArgValue(args, "--model", "gpt-5.5") {
		t.Fatalf("args = %#v, want custom-openai provider for nonstandard OpenAI base URL", args)
	}
	agentDir := opts.Environment["PI_CODING_AGENT_DIR"]
	raw := readPiTestFile(t, filepath.Join(agentDir, "models.json"))
	if !strings.Contains(raw, `"baseUrl": "https://relay.example.com/v1"`) || !strings.Contains(raw, `"id": "gpt-5.5"`) {
		t.Fatalf("generated models.json = %s", raw)
	}
}

func TestPiProviderAliasMapsSettingsProvidersToPiProviders(t *testing.T) {
	tests := map[string]string{
		"azure_openai":  "azure-openai-responses",
		"custom_openai": "custom-openai",
		"gemini":        "google",
		"glm":           "zai",
		"moonshot":      "moonshotai-cn",
		"ollama":        "ollama",
		"qwen":          "qwen",
	}
	for input, want := range tests {
		if got := piProviderAlias(input); got != want {
			t.Fatalf("piProviderAlias(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestCompatiblePiLLMProfileComparesProviderAliases(t *testing.T) {
	profile := llmprofile.LLMProfile{
		Provider: "custom_openai",
		Enabled:  true,
	}
	opts := runtimecore.ExecOptions{
		ProviderSpecific: map[string]string{"llm_provider": "custom-openai"},
	}
	if got := compatiblePiLLMProfile(opts, profile); got == nil {
		t.Fatalf("custom_openai profile should match PI custom-openai provider")
	}
}

func TestApplyLLMProfileRuntimeEnvSetsAzureBaseURLFromSettingsProfile(t *testing.T) {
	opts, cfg, err := applyLLMProfileRuntimeEnv(context.Background(), runtimecore.ExecOptions{
		ProviderSpecific: map[string]string{
			"llm_provider":    "azure_openai",
			"llm_model":       "gpt-5",
			"llm_base_url":    "https://example-resource.openai.azure.com",
			"llm_api_key_ref": "secret://llm/azure",
		},
	}, Config{SecretResolver: staticPiSecretResolver{"secret://llm/azure": "  token-azure  "}})
	if err != nil {
		t.Fatalf("apply llm env: %v", err)
	}
	if cfg.Env["AZURE_OPENAI_API_KEY"] != "token-azure" || cfg.Env["AZURE_OPENAI_BASE_URL"] != "https://example-resource.openai.azure.com" {
		t.Fatalf("env = %#v, want Azure key and base URL", cfg.Env)
	}
	args := BuildArgs("do work", opts)
	if !containsArgValue(args, "--provider", "azure-openai-responses") || !containsArgValue(args, "--model", "gpt-5") {
		t.Fatalf("args = %#v, want Azure provider and model", args)
	}
}

func TestProviderCaptureUsesRunIsolatedPiHome(t *testing.T) {
	root := t.TempDir()
	opts, err := isolatePiDebugWorkerOptions(runtimecore.ExecOptions{
		RunID:           "run-provider-capture",
		Environment:     map[string]string{"PI_CODING_AGENT_DIR": filepath.Join(root, "pooled-home")},
		WorkspacePolicy: agentcore.RuntimeWorkspacePolicy{Root: root},
	})
	if err != nil {
		t.Fatal(err)
	}
	home := opts.Environment["PI_CODING_AGENT_DIR"]
	if home == filepath.Join(root, "pooled-home") || !strings.Contains(filepath.ToSlash(home), "/.orrery/pi-rpc-debug/") {
		t.Fatalf("debug home = %q", home)
	}
	if info, err := os.Stat(home); err != nil || !info.IsDir() {
		t.Fatalf("debug home info = %v, err = %v", info, err)
	}
}

func readPiTestFile(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(raw)
}

func jsonArrayContains(values []any, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func jsonArrayValueAfter(values []any, key string) string {
	for i := 0; i+1 < len(values); i++ {
		if values[i] != key {
			continue
		}
		if value, ok := values[i+1].(string); ok {
			return value
		}
		return ""
	}
	return ""
}

func containsArg(args []string, key string) bool {
	for _, arg := range args {
		if arg == key {
			return true
		}
	}
	return false
}

func containsArgValue(args []string, key, value string) bool {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == key && args[i+1] == value {
			return true
		}
	}
	return false
}

func containsArgValueContaining(args []string, key, needle string) bool {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == key && stringsContains(args[i+1], needle) {
			return true
		}
	}
	return false
}

func stringsContains(value, needle string) bool {
	return len(needle) == 0 || (len(value) >= len(needle) && contains(value, needle))
}

type staticPiSecretResolver map[string]string

func (r staticPiSecretResolver) ResolveSecret(ctx context.Context, ref string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return r[strings.TrimSpace(ref)], nil
}

func contains(value, needle string) bool {
	for i := 0; i+len(needle) <= len(value); i++ {
		if value[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
