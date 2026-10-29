package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	agentassembly "orrery_v2/internal/agent/assembly"
	agentcore "orrery_v2/internal/agent/core"
	llmprofile "orrery_v2/internal/agent/runtime/llmprofile"
	discussionsession "orrery_v2/internal/app/discussionsession"
	memstore "orrery_v2/internal/memory/store"
	"orrery_v2/internal/platform/config"
	"orrery_v2/internal/platform/testkit"
	httptransport "orrery_v2/internal/transport/http"
)

func TestConfiguredDiscussionAgentRunnerUsesPiForSystemManagedProfile(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	executable := writePiTextRuntimeFixture(t)
	settings := productionSettingsStore{router: env.DBRouter}
	if _, err := settings.SaveSettings(ctx, httptransport.SettingsDTO{
		PlatformName:     "Orrery Production",
		OrganizationName: "Acme Production",
		Language:         "zh-CN",
		LLMProfiles: []httptransport.SettingsLLMProfileDTO{{
			ProfileID:     "llm-unused",
			DisplayName:   "Unused LLM",
			Provider:      "openai",
			BaseURL:       "http://127.0.0.1:1/v1",
			APIKeyRef:     "secret://llm/unused",
			Model:         "gpt-5-mini",
			Temperature:   0.2,
			Enabled:       true,
			GlobalDefault: true,
		}},
	}); err != nil {
		t.Fatalf("save settings: %v", err)
	}
	cfg, err := config.LoadYAML(testConfigSource(t, map[string]string{
		"discussion.agent.runtime_kind":   "pi",
		"discussion.agent.runtime_config": `{"executable":"` + jsonPath(executable) + `"}`,
	}))
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	profiles := agentcore.NewPlatformProfileRepository(env.UnitOfWork)
	leadTemplate, err := readDefaultAgentTemplate("lead-agent.json")
	if err != nil {
		t.Fatalf("read lead template: %v", err)
	}
	profile := defaultCoordinatorAgentProfile(defaultLeadAgentID, "lead", agentcore.RuntimeSpec{}, leadTemplate)
	saveAvailableAgentProfile(t, ctx, profiles, profile)
	memory := memstore.NewPlatformStore(env.DB)
	if err := ensureDefaultCoordinatorMemoryProfiles(ctx, memory); err != nil {
		t.Fatalf("ensure memory profiles: %v", err)
	}

	tools, skills := seededTestResourceCatalog(t, ctx)
	runner, err := newConfiguredDiscussionAgentRunner(ctx, cfg, profiles, memory, tools, skills, testProductionDiscussionAgentDependencies(productionDiscussionAgentDependencies{LLMProfiles: settings}))
	if err != nil {
		t.Fatalf("new configured discussion agent runner: %v", err)
	}
	response, err := runner.RunDiscussion(ctx, discussionsession.DiscussionAgentRequest{
		ConversationID: "conv-managed",
		MessageID:      "msg-managed",
		RunID:          "run-managed",
		Content:        "帮我规划一次 v2 上线",
	})
	if err != nil {
		t.Fatalf("RunDiscussion: %v", err)
	}

	if response.RuntimeKind != string(agentcore.RuntimeKindPi) || response.AssistantMessage != "What deadline should I optimize for?" {
		t.Fatalf("response = %+v", response)
	}
	if len(response.ToolCalls) != 0 {
		t.Fatalf("tool calls = %+v", response.ToolCalls)
	}
}

func TestConfiguredPiDiscussionAgentUsesGlobalLLMProfileWhenRuntimeHasNoKey(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	executable := writePiRequiresMiniMaxCNKeyFixture(t)
	settings := productionSettingsStore{router: env.DBRouter}
	if _, err := settings.SaveSettings(ctx, httptransport.SettingsDTO{
		PlatformName:     "Orrery Production",
		OrganizationName: "Acme Production",
		Language:         "zh-CN",
		LLMProfiles: []httptransport.SettingsLLMProfileDTO{{
			ProfileID:     "llm-global-minimax",
			DisplayName:   "Global MiniMax",
			Provider:      "minimax-cn",
			BaseURL:       "https://api.minimax.chat/v1",
			APIKeyRef:     "secret://llm/global-minimax",
			Model:         "MiniMax-M2.7",
			Enabled:       true,
			GlobalDefault: true,
		}},
	}); err != nil {
		t.Fatalf("save settings: %v", err)
	}
	cfg, err := config.LoadYAML(testConfigSource(t, map[string]string{
		"discussion.agent.runtime_kind":   "pi",
		"discussion.agent.runtime_config": `{"executable":"` + jsonPath(executable) + `"}`,
		"discussion.agent.timeout":        "2s",
	}))
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	profiles := agentcore.NewPlatformProfileRepository(env.UnitOfWork)
	leadTemplate, err := readDefaultAgentTemplate("lead-agent.json")
	if err != nil {
		t.Fatalf("read lead template: %v", err)
	}
	profile := defaultCoordinatorAgentProfile(defaultLeadAgentID, "lead", agentcore.RuntimeSpec{}, leadTemplate)
	saveAvailableAgentProfile(t, ctx, profiles, profile)
	memory := memstore.NewPlatformStore(env.DB)
	if err := ensureDefaultCoordinatorMemoryProfiles(ctx, memory); err != nil {
		t.Fatalf("ensure memory profiles: %v", err)
	}

	tools, skills := seededTestResourceCatalog(t, ctx)
	runner, err := newConfiguredDiscussionAgentRunner(ctx, cfg, profiles, memory, tools, skills, testProductionDiscussionAgentDependencies(productionDiscussionAgentDependencies{
		LLMProfiles: settings,
		Secrets: llmprofile.StaticSecretResolver{Values: map[string]string{
			"secret://llm/global-minimax": "token-plan",
		}},
	}))
	if err != nil {
		t.Fatalf("new configured discussion agent runner: %v", err)
	}
	response, err := runner.RunDiscussion(ctx, discussionsession.DiscussionAgentRequest{
		ConversationID: "conv-pi-global-llm",
		MessageID:      "msg-pi-global-llm",
		RunID:          "run-pi-global-llm",
		Content:        "嘿 都冒个泡",
	})
	if err != nil {
		t.Fatalf("RunDiscussion: %v", err)
	}
	if response.RuntimeKind != string(agentcore.RuntimeKindPi) || response.AssistantMessage != "key ok" {
		t.Fatalf("response = %+v", response)
	}
}

func TestExplicitPiDiscussionAgentUsesGlobalGLMProfileWhenProfileOnlyHasLegacyKeyRef(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	executable := writePiRequiresZAIProfileFixture(t)
	settings := productionSettingsStore{router: env.DBRouter}
	if _, err := settings.SaveSettings(ctx, httptransport.SettingsDTO{
		PlatformName:     "Orrery Production",
		OrganizationName: "Acme Production",
		Language:         "zh-CN",
		LLMProfiles: []httptransport.SettingsLLMProfileDTO{{
			ProfileID:     "llm-global-glm",
			DisplayName:   "Global GLM",
			Provider:      "glm",
			BaseURL:       "https://open.bigmodel.cn/api/paas/v4",
			APIKeyRef:     "secret://llm/global-glm",
			Model:         "glm-5.2",
			Enabled:       true,
			GlobalDefault: true,
		}},
	}); err != nil {
		t.Fatalf("save settings: %v", err)
	}
	cfg, err := config.LoadYAML(testConfigSource(t, map[string]string{
		"discussion.agent.timeout": "2s",
	}))
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	profiles := agentcore.NewPlatformProfileRepository(env.UnitOfWork)
	leadTemplate, err := readDefaultAgentTemplate("lead-agent.json")
	if err != nil {
		t.Fatalf("read lead template: %v", err)
	}
	profile := defaultCoordinatorAgentProfile(defaultLeadAgentID, "lead", agentcore.RuntimeSpec{Kind: agentcore.RuntimeKindPi, Config: json.RawMessage(`null`)}, leadTemplate)
	profile.RuntimeSpec = agentcore.RuntimeSpec{Kind: agentcore.RuntimeKindPi, Config: json.RawMessage(`{"executable":"` + jsonPath(executable) + `"}`)}
	profile.BaseAgent.LLMAPIKeyRef = agentcore.DefaultSystemLLMAPIKeyRef
	saveAvailableAgentProfile(t, ctx, profiles, profile)
	memory := memstore.NewPlatformStore(env.DB)
	if err := ensureDefaultCoordinatorMemoryProfiles(ctx, memory); err != nil {
		t.Fatalf("ensure memory profiles: %v", err)
	}

	tools, skills := seededTestResourceCatalog(t, ctx)
	runner, err := newConfiguredDiscussionAgentRunner(ctx, cfg, profiles, memory, tools, skills, testProductionDiscussionAgentDependencies(productionDiscussionAgentDependencies{
		LLMProfiles: settings,
		Secrets: llmprofile.StaticSecretResolver{Values: map[string]string{
			"secret://llm/global-glm": "token-glm",
		}},
	}))
	if err != nil {
		t.Fatalf("new configured discussion agent runner: %v", err)
	}
	response, err := runner.RunDiscussion(ctx, discussionsession.DiscussionAgentRequest{
		ConversationID: "conv-pi-global-glm",
		MessageID:      "msg-pi-global-glm",
		RunID:          "run-pi-global-glm",
		Content:        "嘿 都冒个泡",
	})
	if err != nil {
		t.Fatalf("RunDiscussion: %v", err)
	}
	if response.RuntimeKind != string(agentcore.RuntimeKindPi) || response.AssistantMessage != "glm key ok" {
		t.Fatalf("response = %+v", response)
	}
}

func TestSystemManagedCoordinatorProfilesBindToDefaultPiRuntime(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	settings := productionSettingsStore{router: env.DBRouter}
	if _, err := settings.SaveSettings(ctx, httptransport.SettingsDTO{
		PlatformName:     "Orrery Production",
		OrganizationName: "Acme Production",
		Language:         "zh-CN",
		LLMProfiles: []httptransport.SettingsLLMProfileDTO{{
			ProfileID:     "llm-managed",
			DisplayName:   "Managed LLM",
			Provider:      "openai",
			BaseURL:       "http://127.0.0.1:1/v1",
			APIKeyRef:     "secret://llm/managed",
			Model:         "gpt-5-mini",
			Enabled:       true,
			GlobalDefault: true,
		}},
	}); err != nil {
		t.Fatalf("save settings: %v", err)
	}
	profiles, err := defaultCoordinatorAgentProfiles(agentcore.RuntimeSpec{})
	if err != nil {
		t.Fatalf("defaultCoordinatorAgentProfiles: %v", err)
	}
	for i := range profiles {
		profiles[i].RuntimeSpec = agentcore.RuntimeSpec{}
	}
	memory := memstore.NewPlatformStore(env.DB)
	if err := ensureDefaultCoordinatorMemoryProfiles(ctx, memory); err != nil {
		t.Fatalf("ensure memory profiles: %v", err)
	}
	tools, skills := seededTestResourceCatalog(t, ctx)
	assembler, err := newProductionDiscussionAgentAssembler(memory, tools, skills, testProductionDiscussionAgentDependencies(productionDiscussionAgentDependencies{LLMProfiles: settings}))
	if err != nil {
		t.Fatalf("new production assembler: %v", err)
	}

	for _, profile := range profiles {
		profile.SkillRefs = []agentcore.SkillRef{}
		instance, err := assembler.Build(ctx, profile, agentassembly.BuildOptions{InstanceID: testRuntimeInstanceID(profile.ID)})
		if err != nil {
			t.Fatalf("Build(%s): %v", profile.ID, err)
		}
		provider, ok := instance.(interface {
			Runtime() agentcore.SessionRuntime
		})
		if !ok {
			t.Fatalf("%s does not expose canonical session runtime", profile.ID)
		}
		capabilities := provider.Runtime().Capabilities(ctx)
		if capabilities.Transport.Provider != string(agentcore.RuntimeKindPi) {
			t.Fatalf("%s capabilities = %+v", profile.ID, capabilities)
		}
	}
}

func TestDefaultQAAgentProfilesBindToDefaultPiRuntime(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	settings := productionSettingsStore{router: env.DBRouter}
	if _, err := settings.SaveSettings(ctx, httptransport.SettingsDTO{
		PlatformName:     "Orrery Production",
		OrganizationName: "Acme Production",
		Language:         "zh-CN",
		LLMProfiles: []httptransport.SettingsLLMProfileDTO{{
			ProfileID:     "llm-managed",
			DisplayName:   "Managed LLM",
			Provider:      "openai",
			BaseURL:       "http://127.0.0.1:1/v1",
			APIKeyRef:     "secret://llm/managed",
			Model:         "gpt-5-mini",
			Enabled:       true,
			GlobalDefault: true,
		}},
	}); err != nil {
		t.Fatalf("save settings: %v", err)
	}
	profiles, err := defaultCoordinatorAgentProfiles(agentcore.RuntimeSpec{})
	if err != nil {
		t.Fatalf("defaultCoordinatorAgentProfiles: %v", err)
	}
	for i := range profiles {
		profiles[i].RuntimeSpec = agentcore.RuntimeSpec{}
	}
	memory := memstore.NewPlatformStore(env.DB)
	tools, skills := seededTestResourceCatalog(t, ctx)
	assembler, err := newProductionDiscussionAgentAssembler(memory, tools, skills, testProductionDiscussionAgentDependencies(productionDiscussionAgentDependencies{LLMProfiles: settings}))
	if err != nil {
		t.Fatalf("new production assembler: %v", err)
	}
	targets := map[agentcore.AgentProfileID]bool{
		defaultQAPlanAgentID:  false,
		defaultQACheckAgentID: false,
	}

	for _, profile := range profiles {
		if _, ok := targets[profile.ID]; !ok {
			continue
		}
		targets[profile.ID] = true
		profile.SkillRefs = []agentcore.SkillRef{}
		if err := memory.SaveProfile(ctx, defaultCoordinatorMemoryProfile(profile.ID.String(), profile.RoleName)); err != nil {
			t.Fatalf("save memory profile for %s: %v", profile.ID, err)
		}
		instance, err := assembler.Build(ctx, profile, agentassembly.BuildOptions{InstanceID: testRuntimeInstanceID(profile.ID)})
		if err != nil {
			t.Fatalf("Build(%s): %v", profile.ID, err)
		}
		provider, ok := instance.(interface {
			Runtime() agentcore.SessionRuntime
		})
		if !ok {
			t.Fatalf("%s does not expose canonical session runtime", profile.ID)
		}
		capabilities := provider.Runtime().Capabilities(ctx)
		if capabilities.Transport.Provider != string(agentcore.RuntimeKindPi) {
			t.Fatalf("%s capabilities = %+v", profile.ID, capabilities)
		}
	}
	for id, seen := range targets {
		if !seen {
			t.Fatalf("default freelance profiles missing %s", id)
		}
	}
}

func TestConfiguredDiscussionAgentRunnerStreamsThroughAssembledBaseAgent(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	executable := writeCodexTextRuntimeFixture(t)
	cfg, err := config.LoadYAML(testConfigSource(t, map[string]string{
		"discussion.agent.timeout": "2s",
	}))
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	profiles := agentcore.NewPlatformProfileRepository(env.UnitOfWork)
	leadTemplate, err := readDefaultAgentTemplate("lead-agent.json")
	if err != nil {
		t.Fatalf("read lead template: %v", err)
	}
	profile := defaultCoordinatorAgentProfile(defaultLeadAgentID, "lead", agentcore.RuntimeSpec{
		Kind:   agentcore.RuntimeKindCodex,
		Config: json.RawMessage(`{"executable":"` + jsonPath(executable) + `"}`),
	}, leadTemplate)
	saveAvailableAgentProfile(t, ctx, profiles, profile)
	memory := memstore.NewPlatformStore(env.DB)
	if err := ensureDefaultCoordinatorMemoryProfiles(ctx, memory); err != nil {
		t.Fatalf("ensure memory profiles: %v", err)
	}
	tools, skills := seededTestResourceCatalog(t, ctx)
	runner, err := newConfiguredDiscussionAgentRunner(ctx, cfg, profiles, memory, tools, skills, testProductionDiscussionAgentDependencies(productionDiscussionAgentDependencies{}))
	if err != nil {
		t.Fatalf("new configured discussion agent runner: %v", err)
	}
	streamer, ok := runner.(discussionsession.DiscussionAgentStreamRunner)
	if !ok {
		t.Fatalf("runner type = %T, want DiscussionAgentStreamer", runner)
	}

	events, err := streamer.RunDiscussionStream(ctx, discussionsession.DiscussionAgentRequest{
		ConversationID: "conv-1",
		MessageID:      "msg-1",
		RunID:          "run-1",
		Content:        "Plan a launch",
	})
	if err != nil {
		t.Fatalf("RunDiscussionStream: %v", err)
	}
	var kinds []string
	var deltas []string
	for event := range events {
		kinds = append(kinds, string(event.Kind))
		if event.Kind == discussionsession.DiscussionAgentStreamTokenDelta {
			deltas = append(deltas, event.Delta)
		}
	}
	if !containsString(kinds, string(discussionsession.DiscussionAgentStreamTokenDelta)) || !containsString(kinds, string(discussionsession.DiscussionAgentStreamRunCompleted)) {
		t.Fatalf("stream event kinds = %#v", kinds)
	}
	if strings.TrimSpace(strings.Join(deltas, "")) == "" {
		t.Fatalf("stream deltas = %#v", deltas)
	}
}

func TestConfiguredDiscussionAgentRunnerResolvesRuntimeProfileConfigFromSettings(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	executable := writeCodexTextRuntimeFixture(t)
	settings := productionSettingsStore{router: env.DBRouter}
	if _, err := settings.SaveSettings(ctx, httptransport.SettingsDTO{
		PlatformName:     "Orrery Production",
		OrganizationName: "Acme Production",
		Language:         "zh-CN",
		RuntimeProfiles: []httptransport.SettingsRuntimeProfileDTO{{
			ProfileID:        "runtime-codex-local",
			DisplayName:      "Codex Local",
			RuntimeKind:      "codex",
			RuntimeConfigRef: "runtime-codex-local",
			RuntimeConfig:    map[string]any{"executable": jsonPath(executable)},
			Enabled:          true,
		}},
	}); err != nil {
		t.Fatalf("save settings: %v", err)
	}
	cfg, err := config.LoadYAML(testConfigSource(t))
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	profiles := agentcore.NewPlatformProfileRepository(env.UnitOfWork)
	leadTemplate, err := readDefaultAgentTemplate("lead-agent.json")
	if err != nil {
		t.Fatalf("read lead template: %v", err)
	}
	profile := defaultCoordinatorAgentProfile(defaultLeadAgentID, "lead", agentcore.RuntimeSpec{Kind: agentcore.RuntimeKindCodex, ConfigRef: "runtime-codex-local"}, leadTemplate)
	if len(profile.RuntimeSpec.Config) != 0 {
		t.Fatalf("test profile should not carry copied runtime config: %s", profile.RuntimeSpec.Config)
	}
	saveAvailableAgentProfile(t, ctx, profiles, profile)
	memory := memstore.NewPlatformStore(env.DB)
	if err := ensureDefaultCoordinatorMemoryProfiles(ctx, memory); err != nil {
		t.Fatalf("ensure memory profiles: %v", err)
	}

	tools, skills := seededTestResourceCatalog(t, ctx)
	runner, err := newConfiguredDiscussionAgentRunner(ctx, cfg, profiles, memory, tools, skills, testProductionDiscussionAgentDependencies(productionDiscussionAgentDependencies{RuntimeProfiles: settings}))
	if err != nil {
		t.Fatalf("new configured discussion agent runner: %v", err)
	}
	response, err := runner.RunDiscussion(ctx, discussionsession.DiscussionAgentRequest{
		ConversationID: "conv-runtime-profile",
		MessageID:      "msg-runtime-profile",
		RunID:          "run-runtime-profile",
		Content:        "ping",
	})
	if err != nil {
		t.Fatalf("RunDiscussion: %v", err)
	}
	if response.RuntimeKind != "codex" || strings.TrimSpace(response.AssistantMessage) == "" {
		t.Fatalf("response = %+v", response)
	}
}

func TestConfiguredDiscussionAgentRunnerUsesOnlyEnabledRuntimeProfileForKindWhenConfigRefMissing(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	executable := writePiTextRuntimeFixture(t)
	settings := productionSettingsStore{router: env.DBRouter}
	if _, err := settings.SaveSettings(ctx, httptransport.SettingsDTO{
		PlatformName:     "Orrery Production",
		OrganizationName: "Acme Production",
		Language:         "zh-CN",
		RuntimeProfiles: []httptransport.SettingsRuntimeProfileDTO{{
			ProfileID:        "runtime-pi-local",
			DisplayName:      "PI Local",
			RuntimeKind:      "pi",
			RuntimeConfigRef: "runtime-pi-local",
			RuntimeConfig:    map[string]any{"executable": jsonPath(executable)},
			Enabled:          true,
		}},
	}); err != nil {
		t.Fatalf("save settings: %v", err)
	}
	cfg, err := config.LoadYAML(testConfigSource(t))
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	profiles := agentcore.NewPlatformProfileRepository(env.UnitOfWork)
	leadTemplate, err := readDefaultAgentTemplate("lead-agent.json")
	if err != nil {
		t.Fatalf("read lead template: %v", err)
	}
	profile := defaultCoordinatorAgentProfile(defaultLeadAgentID, "lead", agentcore.RuntimeSpec{Kind: agentcore.RuntimeKindPi}, leadTemplate)
	saveAvailableAgentProfile(t, ctx, profiles, profile)
	memory := memstore.NewPlatformStore(env.DB)
	if err := ensureDefaultCoordinatorMemoryProfiles(ctx, memory); err != nil {
		t.Fatalf("ensure memory profiles: %v", err)
	}

	tools, skills := seededTestResourceCatalog(t, ctx)
	runner, err := newConfiguredDiscussionAgentRunner(ctx, cfg, profiles, memory, tools, skills, testProductionDiscussionAgentDependencies(productionDiscussionAgentDependencies{RuntimeProfiles: settings}))
	if err != nil {
		t.Fatalf("new configured discussion agent runner: %v", err)
	}
	response, err := runner.RunDiscussion(ctx, discussionsession.DiscussionAgentRequest{
		ConversationID: "conv-runtime-profile-kind",
		MessageID:      "msg-runtime-profile-kind",
		RunID:          "run-runtime-profile-kind",
		Content:        "ping",
	})
	if err != nil {
		t.Fatalf("RunDiscussion: %v", err)
	}
	if response.RuntimeKind != "pi" || response.AssistantMessage != "What deadline should I optimize for?" {
		t.Fatalf("response = %+v", response)
	}
}

func TestConfiguredDiscussionAgentRunnerPassesResolvedPiRuntimeProfileConfigToProvider(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	executable := writePiTextRuntimeFixture(t)
	debugDir := t.TempDir()
	settings := productionSettingsStore{router: env.DBRouter}
	if _, err := settings.SaveSettings(ctx, httptransport.SettingsDTO{
		PlatformName:     "Orrery Production",
		OrganizationName: "Acme Production",
		Language:         "zh-CN",
		RuntimeProfiles: []httptransport.SettingsRuntimeProfileDTO{{
			ProfileID:        "runtime-pi-local",
			DisplayName:      "PI Local",
			RuntimeKind:      "pi",
			RuntimeConfigRef: "runtime-pi-local",
			RuntimeConfig: map[string]any{
				"executable": jsonPath(executable),
				"debug": map[string]any{
					"capture_runtime_input": true,
					"output_dir":            debugDir,
				},
			},
			Enabled: true,
		}},
	}); err != nil {
		t.Fatalf("save settings: %v", err)
	}
	cfg, err := config.LoadYAML(testConfigSource(t))
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	profiles := agentcore.NewPlatformProfileRepository(env.UnitOfWork)
	leadTemplate, err := readDefaultAgentTemplate("lead-agent.json")
	if err != nil {
		t.Fatalf("read lead template: %v", err)
	}
	profile := defaultCoordinatorAgentProfile(defaultLeadAgentID, "lead", agentcore.RuntimeSpec{Kind: agentcore.RuntimeKindPi}, leadTemplate)
	saveAvailableAgentProfile(t, ctx, profiles, profile)
	memory := memstore.NewPlatformStore(env.DB)
	if err := ensureDefaultCoordinatorMemoryProfiles(ctx, memory); err != nil {
		t.Fatalf("ensure memory profiles: %v", err)
	}

	tools, skills := seededTestResourceCatalog(t, ctx)
	runner, err := newConfiguredDiscussionAgentRunner(ctx, cfg, profiles, memory, tools, skills, testProductionDiscussionAgentDependencies(productionDiscussionAgentDependencies{RuntimeProfiles: settings}))
	if err != nil {
		t.Fatalf("new configured discussion agent runner: %v", err)
	}
	response, err := runner.RunDiscussion(ctx, discussionsession.DiscussionAgentRequest{
		ConversationID: "conv-runtime-profile-pi-config",
		MessageID:      "msg-runtime-profile-pi-config",
		RunID:          "run-runtime-profile-pi-config",
		Content:        "ping",
	})
	if err != nil {
		t.Fatalf("RunDiscussion: %v", err)
	}
	if leaked := response.ProviderMetadata["pi_debug_runtime_input_path"]; leaked != "" {
		t.Fatalf("provider metadata leaked local debug path %q", leaked)
	}
	debugPath := filepath.Join(debugDir, "runtime-input.json")
	var artifact map[string]any
	raw, err := os.ReadFile(debugPath)
	if err != nil {
		t.Fatalf("read debug artifact: %v", err)
	}
	if err := json.Unmarshal(raw, &artifact); err != nil {
		t.Fatalf("decode debug artifact: %v", err)
	}
	prompt, _ := artifact["prompt"].(string)
	if !strings.Contains(prompt, "<current_user_input") || !strings.Contains(prompt, "ping") {
		t.Fatalf("artifact prompt = %+v", artifact["prompt"])
	}
}

func TestConfiguredDiscussionAgentRunnerUsesSeededLeadTemplateMetadataAsSystemPrompt(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	cfg, err := config.LoadYAML(testConfigSource(t))
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	profiles := agentcore.NewPlatformProfileRepository(env.UnitOfWork)
	leadTemplate, err := readDefaultAgentTemplate("lead-agent.json")
	if err != nil {
		t.Fatalf("read lead template: %v", err)
	}
	profile := defaultCoordinatorAgentProfile(defaultLeadAgentID, "lead", agentcore.RuntimeSpec{Kind: agentcore.RuntimeKindCodex}, leadTemplate)
	saveAvailableAgentProfile(t, ctx, profiles, profile)
	memory := memstore.NewPlatformStore(env.DB)
	if err := ensureDefaultCoordinatorMemoryProfiles(ctx, memory); err != nil {
		t.Fatalf("ensure memory profiles: %v", err)
	}

	tools, skills := seededTestResourceCatalog(t, ctx)
	_, err = newConfiguredDiscussionAgentRunner(ctx, cfg, profiles, memory, tools, skills, testProductionDiscussionAgentDependencies(productionDiscussionAgentDependencies{}))
	if err != nil {
		t.Fatalf("new configured discussion agent runner: %v", err)
	}
	prompt := agentcore.BuildAgentSystemPrompt(agentcore.AgentSystemPromptRequest{Profile: profile})
	for _, want := range []string{
		"系统原则",
		"Agent 角色",
		"ID：" + defaultLeadAgentID,
		"显示名：顾明澈",
		"职责边界",
		"Coordinate and report through Orrery Mission-native records",
		"你是顾明澈，Mission Control 主管",
		"mission_coordinator_plan_parallel",
		"discussion_close",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("system prompt missing %q: %s", want, prompt)
		}
	}
	for _, removed := range []string{"核心指令", "<capabilities>", "<tools>"} {
		if strings.Contains(prompt, removed) {
			t.Fatalf("system prompt should not expose %q: %s", removed, prompt)
		}
	}
}
