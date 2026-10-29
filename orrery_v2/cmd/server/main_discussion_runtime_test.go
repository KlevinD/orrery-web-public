package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	agentcore "orrery_v2/internal/agent/core"
	discussionsession "orrery_v2/internal/app/discussionsession"
	memstore "orrery_v2/internal/memory/store"
	"orrery_v2/internal/platform/config"
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/testkit"
	"orrery_v2/internal/platform/uow"
	httptransport "orrery_v2/internal/transport/http"
)

func TestProductionHandlerReturnsFailedDiscussionWhenDefaultPiRuntimeHasNoOutput(t *testing.T) {
	root := t.TempDir()
	setTestConfigOverride(t, "paths.base_dir", root)
	setTestConfigOverride(t, "database.urls", productionTestDatabaseURLs(root))
	executable := writePiEmptyRuntimeFixture(t)
	setTestConfigOverride(t, "discussion.agent.runtime_kind", "pi")
	setTestConfigOverride(t, "discussion.agent.runtime_config", `{"executable":"`+jsonPath(executable)+`"}`)
	selectTestServerConfig(t)
	handler, cleanup, err := newServerHandler(context.Background(), false)
	if err != nil {
		t.Fatalf("newServerHandler production: %v", err)
	}
	defer cleanup()
	configureRuntimeFixtureLLMProfile(t, handler, "pi-test")

	send := httptest.NewRequest(http.MethodPost, "/api/discussions/messages", jsonBody(`{"content":"Plan a launch"}`))
	send.Header.Set("X-User-ID", "superadmin")
	send.Header.Set("X-Viewer-Kind", "user")
	send.Header.Set("X-User-Display-Name", "superadmin")
	send.Header.Set("Idempotency-Key", "discussion-unconfigured-send")
	rec := httptest.NewRecorder()
	serveServerTestHTTP(t, handler, rec, send)
	body := rec.Body.String()
	if rec.Code != http.StatusAccepted ||
		!strings.Contains(body, `"status":"failed"`) ||
		!strings.Contains(body, `"kind":"discussion_runtime_failed"`) ||
		!strings.Contains(body, "pi completed without assistant or tool output") ||
		strings.Contains(body, `"code":"internal_error"`) {
		t.Fatalf("discussion send with default pi failure status = %d body = %s", rec.Code, body)
	}
}

func TestProductionDiscussionSendReportsUnavailableDefaultMembers(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	cfg, err := config.LoadYAML(testConfigSource(t, map[string]string{
		"paths.base_dir": root,
		"database.urls":  productionTestDatabaseURLs(root),
	}))
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	databaseConfig, err := db.RegistryConfigFromURLs(cfg.DatabaseURLs)
	if err != nil {
		t.Fatalf("build database config: %v", err)
	}
	coreDB, err := db.Open(ctx, databaseConfig.Databases[db.RoleCore])
	if err != nil {
		t.Fatalf("open core db: %v", err)
	}
	t.Cleanup(func() {
		if err := coreDB.Close(); err != nil {
			t.Fatalf("close seeded core db: %v", err)
		}
	})
	unit := uow.New(coreDB)
	for _, seed := range []struct {
		id           string
		templateFile string
		role         string
	}{
		{id: defaultLeadAgentID, templateFile: "lead-agent.json", role: "lead"},
	} {
		template, err := readDefaultAgentTemplate(seed.templateFile)
		if err != nil {
			t.Fatalf("read template %s: %v", seed.templateFile, err)
		}
		disabledProfile := defaultCoordinatorAgentProfile(seed.id, seed.role, agentcore.RuntimeSpec{Kind: agentcore.RuntimeKindCodex}, template)
		disabledProfile.Enabled = false
		if err := saveRawAgentProfile(ctx, unit, disabledProfile); err != nil {
			t.Fatalf("save disabled profile %s: %v", seed.id, err)
		}
	}
	executable := writeCodexTextRuntimeFixture(t)
	setTestConfigOverride(t, "paths.base_dir", root)
	setTestConfigOverride(t, "database.urls", productionTestDatabaseURLs(root))
	setTestConfigOverride(t, "discussion.agent.runtime_kind", "codex")
	setTestConfigOverride(t, "discussion.agent.runtime_config", `{"executable":"`+jsonPath(executable)+`","extra_args":["--model","gpt-5-codex"]}`)
	setTestConfigOverride(t, "discussion.agent.timeout", "2s")
	selectTestServerConfig(t)
	handler, cleanup, err := newServerHandler(ctx, false)
	if err != nil {
		t.Fatalf("newServerHandler production: %v", err)
	}
	defer cleanup()

	send := httptest.NewRequest(http.MethodPost, "/api/discussions/messages", jsonBody(`{"content":"帮我写个贪吃蛇小游戏"}`))
	send.Header.Set("X-User-ID", "superadmin")
	send.Header.Set("X-Viewer-Kind", "user")
	send.Header.Set("X-User-Display-Name", "superadmin")
	send.Header.Set("Idempotency-Key", "disabled-default-members")
	rec := httptest.NewRecorder()
	serveServerTestHTTP(t, handler, rec, send)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("discussion send should report unavailable default directory members, status = %d body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "no available agent for discussion role Lead in personnel directory") {
		t.Fatalf("discussion send should identify unavailable discussion role, body = %s", rec.Body.String())
	}
}

func TestProductionDiscussionSendUsesDefaultPiRuntimeAndReplies(t *testing.T) {
	root := t.TempDir()
	executable := writePiTextRuntimeFixture(t)
	setTestConfigOverride(t, "paths.base_dir", root)
	setTestConfigOverride(t, "database.urls", productionTestDatabaseURLs(root))
	setTestConfigOverride(t, "discussion.agent.runtime_kind", "pi")
	setTestConfigOverride(t, "discussion.agent.runtime_config", `{"executable":"`+jsonPath(executable)+`"}`)
	t.Setenv("ORRERY_SECRET_LLM_UNUSED", "test-unused-key")
	selectTestServerConfig(t)
	handler, cleanup, err := newServerHandler(context.Background(), false)
	if err != nil {
		t.Fatalf("newServerHandler production: %v", err)
	}
	defer cleanup()

	putSettings := httptest.NewRequest(http.MethodPut, "/api/settings", jsonBody(`{"platform_name":"Orrery Production","organization_name":"Acme Production","language":"zh-CN","llm_profiles":[{"profile_id":"llm-unused","display_name":"Unused LLM","provider":"openai","base_url":"http://127.0.0.1:1/v1","api_key_ref":"secret://llm/unused","model":"gpt-5-mini","temperature":0.2,"context_length":128000,"enabled":true,"global_default":true}]}`))
	putSettings.Header.Set("X-User-ID", "superadmin")
	putSettings.Header.Set("X-Viewer-Kind", "user")
	putSettings.Header.Set("X-Permission-Scope", "settings.manage")
	rec := httptest.NewRecorder()
	serveServerTestHTTP(t, handler, rec, putSettings)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT /api/settings status = %d body = %s", rec.Code, rec.Body.String())
	}

	send := httptest.NewRequest(http.MethodPost, "/api/discussions/messages", jsonBody(`{"content":"你好"}`))
	send.Header.Set("X-User-ID", "superadmin")
	send.Header.Set("X-Viewer-Kind", "user")
	send.Header.Set("X-User-Display-Name", "superadmin")
	send.Header.Set("Idempotency-Key", "managed-hello-send")
	rec = httptest.NewRecorder()
	serveServerTestHTTP(t, handler, rec, send)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("discussion managed send status = %d body = %s", rec.Code, rec.Body.String())
	}
	var conversation discussionsession.ConversationDTO
	if err := json.NewDecoder(rec.Body).Decode(&conversation); err != nil {
		t.Fatalf("decode discussion: %v", err)
	}
	if conversation.Status != "completed" {
		t.Fatalf("conversation status = %q body = %+v", conversation.Status, conversation)
	}
	if len(conversation.Messages) < 2 {
		t.Fatalf("conversation missing user/assistant messages: %+v", conversation.Messages)
	}
	assistant := conversation.Messages[len(conversation.Messages)-1]
	if assistant.Role != "assistant" || !strings.HasPrefix(assistant.Content, "[@superadmin](mention://user/system-superadmin) What deadline should I optimize for?") {
		t.Fatalf("assistant message = %+v", assistant)
	}
	if len(conversation.Runs) == 0 || conversation.Runs[len(conversation.Runs)-1].RuntimeKind != string(agentcore.RuntimeKindPi) {
		t.Fatalf("conversation runs = %+v", conversation.Runs)
	}
}

func TestConfiguredDiscussionAgentRunnerUsesPiForSystemProfileWithMissingRuntimeSpec(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	executable := writePiTextRuntimeFixture(t)
	cfg, err := config.LoadYAML(writeTestConfigYAML(t, `
agent_defaults:
  runtime_config:
    executable: `+jsonPath(executable)+`
`))
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
	runner, err := newConfiguredDiscussionAgentRunner(ctx, cfg, profiles, memory, tools, skills, testProductionDiscussionAgentDependencies(productionDiscussionAgentDependencies{}))
	if err != nil {
		t.Fatalf("new configured discussion agent runner: %v", err)
	}
	if runner == nil {
		t.Fatalf("runner is nil")
	}
	if _, ok := runner.(*discussionsession.BaseAgentDiscussionAgentRunner); !ok {
		t.Fatalf("runner type = %T, want *BaseAgentDiscussionAgentRunner", runner)
	}
	response, err := runner.RunDiscussion(ctx, discussionsession.DiscussionAgentRequest{
		ConversationID: "conv-1",
		MessageID:      "msg-1",
		RunID:          "run-1",
		Content:        "你好",
	})
	if err != nil {
		t.Fatalf("RunDiscussion: %v", err)
	}
	if response.AssistantMessage != "What deadline should I optimize for?" || response.RuntimeKind != string(agentcore.RuntimeKindPi) {
		t.Fatalf("response = %+v", response)
	}
}

func TestConfiguredDiscussionAgentRunnerUsesPiAgentEndMessagesAsReply(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	executable := writePiAgentEndTextRuntimeFixture(t)
	cfg, err := config.LoadYAML(writeTestConfigYAML(t, `
agent_defaults:
  runtime_config:
    executable: `+jsonPath(executable)+`
`))
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
	runner, err := newConfiguredDiscussionAgentRunner(ctx, cfg, profiles, memory, tools, skills, testProductionDiscussionAgentDependencies(productionDiscussionAgentDependencies{}))
	if err != nil {
		t.Fatalf("new configured discussion agent runner: %v", err)
	}
	response, err := runner.RunDiscussion(ctx, discussionsession.DiscussionAgentRequest{
		ConversationID: "conv-agent-end-pi",
		MessageID:      "msg-agent-end-pi",
		RunID:          "run-agent-end-pi",
		Content:        "嘿 都冒个泡",
	})
	if err != nil {
		t.Fatalf("RunDiscussion: %v", err)
	}
	if response.Status != "completed" || response.RuntimeKind != string(agentcore.RuntimeKindPi) || response.AssistantMessage != "Agent-end reply." {
		t.Fatalf("response = %+v, want completed pi agent_end reply", response)
	}
}

func TestConfiguredDiscussionAgentRunnerFailsEmptyPiRuntimeOutput(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	executable := writePiEmptyRuntimeFixture(t)
	cfg, err := config.LoadYAML(writeTestConfigYAML(t, `
agent_defaults:
  runtime_config:
    executable: `+jsonPath(executable)+`
`))
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
	runner, err := newConfiguredDiscussionAgentRunner(ctx, cfg, profiles, memory, tools, skills, testProductionDiscussionAgentDependencies(productionDiscussionAgentDependencies{}))
	if err != nil {
		t.Fatalf("new configured discussion agent runner: %v", err)
	}
	_, err = runner.RunDiscussion(ctx, discussionsession.DiscussionAgentRequest{
		ConversationID: "conv-empty-pi",
		MessageID:      "msg-empty-pi",
		RunID:          "run-empty-pi",
		Content:        "嘿 都冒个泡",
	})
	if err == nil || !strings.Contains(err.Error(), "pi completed without assistant or tool output") {
		t.Fatalf("RunDiscussion error = %v, want empty pi output failure", err)
	}
}

func TestConfiguredDiscussionAgentRunnerRespectsProfileRuntimeWhenExplicit(t *testing.T) {
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
	settings := productionSettingsStore{router: env.DBRouter}
	if _, err := settings.SaveSettings(ctx, httptransport.SettingsDTO{
		PlatformName:     "Orrery Production",
		OrganizationName: "Acme Production",
		Language:         "zh-CN",
		LLMProfiles: []httptransport.SettingsLLMProfileDTO{{
			ProfileID:     "llm-managed",
			DisplayName:   "Managed LLM",
			Provider:      "openai",
			BaseURL:       "http://127.0.0.1:9/v1",
			APIKeyRef:     "secret://llm/managed",
			Model:         "gpt-5-mini",
			Enabled:       true,
			GlobalDefault: true,
		}},
	}); err != nil {
		t.Fatalf("save settings: %v", err)
	}

	tools, skills := seededTestResourceCatalog(t, ctx)
	runner, err := newConfiguredDiscussionAgentRunner(ctx, cfg, profiles, memory, tools, skills, testProductionDiscussionAgentDependencies(productionDiscussionAgentDependencies{LLMProfiles: settings}))
	if err != nil {
		t.Fatalf("new configured discussion agent runner: %v", err)
	}
	response, err := runner.RunDiscussion(ctx, discussionsession.DiscussionAgentRequest{
		ConversationID: "conv-profile-runtime",
		MessageID:      "msg-profile-runtime",
		RunID:          "run-profile-runtime",
		Content:        "Plan a launch",
	})
	if err != nil {
		t.Fatalf("RunDiscussion: %v", err)
	}
	if response.AssistantMessage != "What deadline should I optimize for?" || response.RuntimeKind != string(agentcore.RuntimeKindCodex) {
		t.Fatalf("response = %+v", response)
	}
}

func TestConfiguredDiscussionAgentRunnerUsesDiscussionRuntimeConfig(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	executable := writeCodexTextRuntimeFixture(t)
	cfg, err := config.LoadYAML(testConfigSource(t, map[string]string{
		"discussion.agent.runtime_kind":   "codex",
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
	settings := productionSettingsStore{router: env.DBRouter}
	if _, err := settings.SaveSettings(ctx, httptransport.SettingsDTO{
		PlatformName:     "Orrery Production",
		OrganizationName: "Acme Production",
		Language:         "zh-CN",
		LLMProfiles: []httptransport.SettingsLLMProfileDTO{{
			ProfileID:     "llm-managed",
			DisplayName:   "Managed LLM",
			Provider:      "openai",
			BaseURL:       "http://127.0.0.1:9/v1",
			APIKeyRef:     "secret://llm/managed",
			Model:         "gpt-5-mini",
			Enabled:       true,
			GlobalDefault: true,
		}},
	}); err != nil {
		t.Fatalf("save settings: %v", err)
	}

	tools, skills := seededTestResourceCatalog(t, ctx)
	runner, err := newConfiguredDiscussionAgentRunner(ctx, cfg, profiles, memory, tools, skills, testProductionDiscussionAgentDependencies(productionDiscussionAgentDependencies{LLMProfiles: settings}))
	if err != nil {
		t.Fatalf("new configured discussion agent runner: %v", err)
	}
	debugger, ok := runner.(discussionsession.DiscussionAgentPromptDebugger)
	if !ok {
		t.Fatalf("runner does not expose prompt debug")
	}
	debug := debugger.PromptDebug(discussionsession.DiscussionAgentRequest{
		ConversationID: "conv-env-ignored",
		MessageID:      "msg-discussion-runtime",
		RunID:          "run-discussion-runtime",
		Content:        "Plan a launch",
	})
	if debug == nil {
		t.Fatalf("debug = nil")
	}
	if debug.Selected.RuntimeKind != string(agentcore.RuntimeKindCodex) {
		t.Fatalf("runtime kind = %q, want codex from discussion runtime config %s", debug.Selected.RuntimeKind, executable)
	}
}

func TestConfiguredDiscussionAgentRunnerUsesAgentScopedRuntimeConfig(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	executable := writeCodexTextRuntimeFixture(t)
	cfg, err := config.LoadYAML(writeTestConfigYAML(t, `
discussion:
  agent_id: custom-primary
agents:
  custom-primary:
    runtime_kind: codex
    runtime_config:
      executable: `+jsonPath(executable)+`
    timeout: 2s
`))
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	profiles := agentcore.NewPlatformProfileRepository(env.UnitOfWork)
	leadTemplate, err := readDefaultAgentTemplate("lead-agent.json")
	if err != nil {
		t.Fatalf("read lead template: %v", err)
	}
	profile := defaultCoordinatorAgentProfile("custom-primary", "primary", agentcore.RuntimeSpec{}, leadTemplate)
	saveAvailableAgentProfile(t, ctx, profiles, profile)
	memory := memstore.NewPlatformStore(env.DB)
	if err := ensureCoordinatorMemoryProfilesForAgents(ctx, memory, []struct {
		agentID string
		role    string
	}{{agentID: "custom-primary", role: "primary"}}); err != nil {
		t.Fatalf("ensure memory profiles: %v", err)
	}

	tools, skills := seededTestResourceCatalog(t, ctx)
	runner, err := newConfiguredDiscussionAgentRunner(ctx, cfg, profiles, memory, tools, skills, testProductionDiscussionAgentDependencies(productionDiscussionAgentDependencies{}))
	if err != nil {
		t.Fatalf("new configured discussion agent runner: %v", err)
	}
	response, err := runner.RunDiscussion(ctx, discussionsession.DiscussionAgentRequest{
		ConversationID: "conv-agent-runtime",
		MessageID:      "msg-agent-runtime",
		RunID:          "run-agent-runtime",
		Content:        "Plan a launch",
	})
	if err != nil {
		t.Fatalf("RunDiscussion: %v", err)
	}
	if response.RuntimeKind != string(agentcore.RuntimeKindCodex) || response.AssistantMessage != "What deadline should I optimize for?" {
		t.Fatalf("response = %+v", response)
	}
}
