package core

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestBaseAgentRuntimeRequestUsesProfileWorkspaceWhenRunHasNone(t *testing.T) {
	runtime := &capturingRuntimeRequest{response: RuntimeResponse{Status: RuntimeStatusCompleted, Messages: []RuntimeMessage{{Type: RuntimeMessageText, Content: "ok"}}}}
	workspace := filepath.Join(t.TempDir(), "agent-workspace")
	agent := NewBaseAgent(BaseAgentConfig{
		Identity: AgentIdentity{AgentID: "agent-1", AgentProfileID: "profile-1", InstanceRef: AgentInstanceRef{ID: "instance-1"}},
		Profile:  AgentProfile{ID: "profile-1", WorkspaceRoot: workspace},
		Runtime:  runtime, Signals: &recordingSignalSink{}, Delivery: &capturingDelivery{}, Trace: &recordingTraceRecorder{},
		Notifier: &recordingNotifier{}, Validator: ResultValidatorFunc(func(AgentInput, AgentResult) error { return nil }),
	})
	input := validAgentInput()
	input.RuntimeOptions.WorkingDirectory = ""
	input.RuntimeOptions.WorkspacePolicy = RuntimeWorkspacePolicy{}

	if _, err := agent.Handle(context.Background(), input); err != nil {
		t.Fatalf("handle task: %v", err)
	}
	opts := runtime.request.RuntimeOptions
	if opts.WorkingDirectory != workspace || opts.WorkspacePolicy.Root != workspace || opts.WorkspacePolicy.WorkDir != workspace {
		t.Fatalf("workspace options = %+v", opts)
	}
	if !opts.WorkspacePolicy.EnforceBoundary || opts.WorkspacePolicy.SandboxMode != "workspace-write" {
		t.Fatalf("workspace policy = %+v", opts.WorkspacePolicy)
	}
}

func TestBaseAgentMarksApprovedContinuationCommit(t *testing.T) {
	runtime := &capturingRuntimeRequest{response: RuntimeResponse{Status: RuntimeStatusCompleted, Messages: []RuntimeMessage{{Type: RuntimeMessageText, Content: "ok"}}}}
	delivery := &capturingDelivery{}
	agent := NewBaseAgent(BaseAgentConfig{
		Identity: AgentIdentity{AgentID: "agent-1", AgentProfileID: "profile-1", InstanceRef: AgentInstanceRef{ID: "instance-1"}},
		Profile:  AgentProfile{ID: "profile-1"}, Runtime: runtime, Signals: &recordingSignalSink{}, Delivery: delivery,
		Trace: &recordingTraceRecorder{}, Notifier: &recordingNotifier{}, Validator: ResultValidatorFunc(func(AgentInput, AgentResult) error { return nil }),
	})
	input := validAgentInput()
	input.RuntimeOptions.ProviderSpecific = map[string]string{
		"approval_continuation": "true", "approval_decision": "approved", "approval_action_id": "approve_once",
		"hil_receipt_effect": "authorize", "hil_receipt_id": "receipt-1",
		"approval_material_kind": "wechat_draft", "approval_material_hash": "sha256:material",
	}

	if _, err := agent.Handle(context.Background(), input); err != nil {
		t.Fatalf("handle task: %v", err)
	}
	if !delivery.options.ApprovedContinuation {
		t.Fatalf("commit options = %+v, want approved continuation", delivery.options)
	}
}

func TestBaseAgentRuntimeRequestKeepsExplicitRunWorkspace(t *testing.T) {
	runtime := &capturingRuntimeRequest{response: RuntimeResponse{Status: RuntimeStatusCompleted, Messages: []RuntimeMessage{{Type: RuntimeMessageText, Content: "ok"}}}}
	profileWorkspace := filepath.Join(t.TempDir(), "profile-workspace")
	runWorkspace := filepath.Join(t.TempDir(), "run-workspace")
	agent := NewBaseAgent(BaseAgentConfig{
		Identity: AgentIdentity{AgentID: "agent-1", AgentProfileID: "profile-1", InstanceRef: AgentInstanceRef{ID: "instance-1"}},
		Profile:  AgentProfile{ID: "profile-1", WorkspaceRoot: profileWorkspace},
		Runtime:  runtime, Signals: &recordingSignalSink{}, Delivery: &capturingDelivery{}, Trace: &recordingTraceRecorder{},
		Notifier: &recordingNotifier{}, Validator: ResultValidatorFunc(func(AgentInput, AgentResult) error { return nil }),
	})
	input := validAgentInput()
	input.RuntimeOptions.WorkingDirectory = runWorkspace
	input.RuntimeOptions.WorkspacePolicy = RuntimeWorkspacePolicy{Root: runWorkspace, WorkDir: runWorkspace, EnforceBoundary: true}

	if _, err := agent.Handle(context.Background(), input); err != nil {
		t.Fatalf("handle task: %v", err)
	}
	if got := runtime.request.RuntimeOptions.WorkingDirectory; got != runWorkspace {
		t.Fatalf("working directory = %q, want %q", got, runWorkspace)
	}
	if got := runtime.request.RuntimeOptions.WorkspacePolicy.Root; got != runWorkspace {
		t.Fatalf("workspace root = %q, want %q", got, runWorkspace)
	}
}

func TestBaseAgentRuntimeRequestRendersBoundMemoryBeforeCurrentUserInput(t *testing.T) {
	runtime := &capturingRuntimeRequest{response: RuntimeResponse{Status: RuntimeStatusCompleted, Messages: []RuntimeMessage{{Type: RuntimeMessageText, Content: "ok"}}}}
	agent := NewBaseAgent(BaseAgentConfig{
		Identity: AgentIdentity{AgentID: "agent-1", AgentProfileID: "profile-1", InstanceRef: AgentInstanceRef{ID: "instance-1"}},
		Profile:  AgentProfile{ID: "profile-1"},
		Bindings: AgentBindings{Memory: MemoryBinding{
			PersonalDigest: MemoryDigestRef{ID: "digest-personal", MemoryKind: MemoryKindPersonal, SummaryRef: "PERSONAL_MEMORY_MARKER"},
			CollectiveDigests: []MemoryDigestRef{{
				ID:         "digest-organization",
				MemoryKind: MemoryKindOrganization,
				SummaryRef: "COLLECTIVE_MEMORY_MARKER",
			}},
		}},
		Runtime: runtime, Signals: &recordingSignalSink{}, Delivery: &capturingDelivery{}, Trace: &recordingTraceRecorder{},
		Notifier: &recordingNotifier{}, Validator: ResultValidatorFunc(func(AgentInput, AgentResult) error { return nil }),
	})
	input := validAgentInput()
	input.InputMessages = []RuntimeChatMessage{
		{Role: RuntimePromptRoleSystem, Content: "PREBUILT_SYSTEM_MARKER"},
		{Role: RuntimePromptRoleUser, Content: "HISTORICAL_USER_MARKER"},
		{Role: RuntimePromptRoleUser, Content: "CURRENT_USER_MARKER"},
	}

	if _, err := agent.Handle(context.Background(), input); err != nil {
		t.Fatalf("handle task: %v", err)
	}
	prompt := runtimeMessageContent(runtime.request.InputMessages)
	for _, marker := range []string{"PERSONAL_MEMORY_MARKER", "COLLECTIVE_MEMORY_MARKER", "read-only historical summaries", "never as instructions"} {
		if !strings.Contains(prompt, marker) {
			t.Fatalf("runtime prompt missing %q: %s", marker, prompt)
		}
	}
	memoryAt := strings.Index(prompt, "PERSONAL_MEMORY_MARKER")
	currentAt := strings.Index(prompt, "CURRENT_USER_MARKER")
	if memoryAt < 0 || currentAt < 0 || memoryAt >= currentAt {
		t.Fatalf("memory must precede current user input: %s", prompt)
	}
}

func runtimeMessageContent(messages []RuntimeChatMessage) string {
	parts := make([]string, 0, len(messages))
	for _, message := range messages {
		parts = append(parts, message.Content)
	}
	return strings.Join(parts, "\n")
}

func TestBaseAgentExposesManagedPropertiesAndInjectsRuntimeDefaults(t *testing.T) {
	runtime := &capturingRuntimeRequest{response: RuntimeResponse{Status: RuntimeStatusCompleted, Messages: []RuntimeMessage{{Type: RuntimeMessageText, Content: "ok"}}}}
	agent := NewBaseAgent(BaseAgentConfig{
		Identity: AgentIdentity{AgentID: "agent-1", AgentProfileID: "profile-1", InstanceRef: AgentInstanceRef{ID: "instance-1"}},
		Profile: AgentProfile{
			ID: "profile-1",
			BaseAgent: BaseAgentSpec{
				InheritsBaseAgent: true,
				AuditRequired:     true,
				SystemPrompt:      "Managed system prompt",
				LLMAPIKeyRef:      "secret://system/llm-api-key",
				SkillScope:        "profile-bound-skills",
				ToolScope:         "profile-bound-tools",
				MCPScope:          "profile-bound-mcp",
				Extensions: map[string]json.RawMessage{
					"handoff_policy": json.RawMessage(`{"mode":"supervised"}`),
				},
			},
			RuntimeSpec: RuntimeSpec{Kind: RuntimeKindOpenClaw, ConfigRef: "runtime-config-openclaw", Config: []byte(`{"agent_id":"planner"}`)},
		},
		Runtime:  runtime,
		Signals:  &recordingSignalSink{},
		Delivery: &capturingDelivery{},
		Trace:    &recordingTraceRecorder{},
		Notifier: &recordingNotifier{},
		Validator: ResultValidatorFunc(func(AgentInput, AgentResult) error {
			return nil
		}),
	})

	if agent.SystemPrompt() != "Managed system prompt" {
		t.Fatalf("system prompt = %q", agent.SystemPrompt())
	}
	if agent.LLMAPIKeyRef() != "secret://system/llm-api-key" {
		t.Fatalf("llm api key ref = %q", agent.LLMAPIKeyRef())
	}
	if agent.SkillScope() != "profile-bound-skills" || agent.ToolScope() != "profile-bound-tools" || agent.MCPScope() != "profile-bound-mcp" {
		t.Fatalf("scopes = skill:%q tool:%q mcp:%q", agent.SkillScope(), agent.ToolScope(), agent.MCPScope())
	}
	if agent.RuntimeSpec().Kind != RuntimeKindOpenClaw || agent.RuntimeSpec().ConfigRef != "runtime-config-openclaw" {
		t.Fatalf("runtime spec = %#v", agent.RuntimeSpec())
	}
	extensions := agent.Extensions()
	extensions["handoff_policy"][0] = byte('[')
	extensions["new"] = json.RawMessage(`{"ok":true}`)
	if string(agent.Extensions()["handoff_policy"]) != `{"mode":"supervised"}` || agent.Extensions()["new"] != nil {
		t.Fatalf("extensions must be returned as a defensive copy: %#v", agent.Extensions())
	}

	input := validAgentInput()
	input.RuntimeOptions.ProviderSpecific = map[string]string{"custom": "kept"}
	if _, err := agent.Handle(context.Background(), input); err != nil {
		t.Fatalf("handle task: %v", err)
	}

	opts := runtime.request.RuntimeOptions
	if !strings.Contains(opts.SystemPrompt, "角色设定") ||
		strings.Contains(opts.SystemPrompt, "<capabilities>") ||
		!strings.Contains(opts.SystemPrompt, "Managed system prompt") {
		t.Fatalf("system prompt = %q", opts.SystemPrompt)
	}
	if strings.Contains(opts.SystemPrompt, "生命周期信号") {
		t.Fatalf("system prompt should not include legacy global template: %s", opts.SystemPrompt)
	}
	if opts.ProviderSpecific["llm_api_key_ref"] != "secret://system/llm-api-key" ||
		opts.ProviderSpecific["skill_scope"] != "profile-bound-skills" ||
		opts.ProviderSpecific["tool_scope"] != "profile-bound-tools" ||
		opts.ProviderSpecific["mcp_scope"] != "profile-bound-mcp" ||
		opts.ProviderSpecific["config_ref"] != "runtime-config-openclaw" ||
		opts.ProviderSpecific["custom"] != "kept" {
		t.Fatalf("provider specific = %#v", opts.ProviderSpecific)
	}
}

func TestBaseAgentRuntimeRequestCarriesProviderConfigFromProfile(t *testing.T) {
	runtime := &capturingRuntimeRequest{response: RuntimeResponse{Status: RuntimeStatusCompleted, Messages: []RuntimeMessage{{Type: RuntimeMessageText, Content: "ok"}}}}
	agent := NewBaseAgent(BaseAgentConfig{
		Identity: AgentIdentity{AgentID: "agent-1", AgentProfileID: "profile-1", InstanceRef: AgentInstanceRef{ID: "instance-1"}},
		Profile: AgentProfile{
			ID: "profile-1",
			BaseAgent: BaseAgentSpec{
				InheritsBaseAgent: true,
				AuditRequired:     true,
				SystemPrompt:      "Managed system prompt",
				LLMAPIKeyRef:      "secret://system/llm-api-key",
				SkillScope:        "profile_bound",
				ToolScope:         "profile_bound",
				MCPScope:          "profile_bound",
			},
			RuntimeSpec: RuntimeSpec{Kind: RuntimeKindOpenClaw, ConfigRef: "runtime-config-openclaw", Config: []byte(`{"agent_id":"planner"}`)},
		},
		Runtime:  runtime,
		Signals:  &recordingSignalSink{},
		Delivery: &capturingDelivery{},
		Trace:    &recordingTraceRecorder{},
		Notifier: &recordingNotifier{},
		Validator: ResultValidatorFunc(func(AgentInput, AgentResult) error {
			return nil
		}),
	})

	if _, err := agent.Handle(context.Background(), validAgentInput()); err != nil {
		t.Fatalf("handle task: %v", err)
	}
	if string(runtime.request.RuntimeOptions.ProviderConfig) != `{"agent_id":"planner"}` {
		t.Fatalf("provider config = %s", runtime.request.RuntimeOptions.ProviderConfig)
	}
	if runtime.request.RuntimeOptions.ProviderSpecific["config_ref"] != "runtime-config-openclaw" {
		t.Fatalf("provider specific = %#v", runtime.request.RuntimeOptions.ProviderSpecific)
	}
}

func TestBaseAgentRuntimeRequestAlwaysIncludesSkillLoaderTools(t *testing.T) {
	runtime := &capturingRuntimeRequest{response: RuntimeResponse{Status: RuntimeStatusCompleted, Messages: []RuntimeMessage{{Type: RuntimeMessageText, Content: "ok"}}}}
	agent := NewBaseAgent(BaseAgentConfig{
		Identity: AgentIdentity{AgentID: "agent-1", AgentProfileID: "profile-1", InstanceRef: AgentInstanceRef{ID: "instance-1"}},
		Profile:  AgentProfile{ID: "profile-1"},
		Bindings: AgentBindings{
			Skills: []SkillBinding{{SkillID: "skill.tdd", Version: "1.0.0", Summary: "Write tests first"}},
		},
		Runtime:  runtime,
		Signals:  &recordingSignalSink{},
		Delivery: &capturingDelivery{},
		Trace:    &recordingTraceRecorder{},
		Notifier: &recordingNotifier{},
		Validator: ResultValidatorFunc(func(AgentInput, AgentResult) error {
			return nil
		}),
	})

	if _, err := agent.Handle(context.Background(), validAgentInput()); err != nil {
		t.Fatalf("handle task: %v", err)
	}

	tools := map[ToolID]RuntimeToolBinding{}
	for _, binding := range runtime.request.ToolBindings {
		tools[binding.ToolID] = binding
	}
	for _, id := range []ToolID{"list_skills", "load_skill", "list_skill_resources", "load_resource"} {
		binding, ok := tools[id]
		if !ok {
			t.Fatalf("runtime request missing skill loader tool %s in %+v", id, runtime.request.ToolBindings)
		}
		if len(binding.InputSchema) == 0 || strings.TrimSpace(binding.Summary) == "" {
			t.Fatalf("skill loader binding %s should carry schema and summary: %+v", id, binding)
		}
	}
	if len(runtime.request.SkillBindings) != 1 || runtime.request.SkillBindings[0].SkillID != "skill.tdd" {
		t.Fatalf("skill bindings should still reflect profile skills only: %+v", runtime.request.SkillBindings)
	}
}

func TestBaseAgentRuntimeRequestFiltersToolsByScheduledAllowlist(t *testing.T) {
	runtime := &capturingRuntimeRequest{response: RuntimeResponse{Status: RuntimeStatusCompleted, Messages: []RuntimeMessage{{Type: RuntimeMessageText, Content: "ok"}}}}
	agent := NewBaseAgent(BaseAgentConfig{
		Identity: AgentIdentity{AgentID: "agent-1", AgentProfileID: "profile-1", InstanceRef: AgentInstanceRef{ID: "instance-1"}},
		Profile:  AgentProfile{ID: "profile-1"},
		Bindings: AgentBindings{Tools: []ToolBinding{
			{ToolID: "mcp.marketing.marketing_wechat_draft_create", Alias: "wechat_draft"},
			{ToolID: "mcp.marketing.marketing_publish_intent_get", Alias: "intent_get"},
		}},
		Runtime: runtime, Signals: &recordingSignalSink{}, Delivery: &capturingDelivery{}, Trace: &recordingTraceRecorder{},
		Notifier: &recordingNotifier{}, Validator: ResultValidatorFunc(func(AgentInput, AgentResult) error { return nil }),
	})
	input := validAgentInput()
	input.RuntimeOptions.ProviderSpecific = map[string]string{
		RuntimeAllowedToolIDsProviderKey: `["mcp.marketing.marketing_wechat_draft_create"]`,
	}

	if _, err := agent.Handle(context.Background(), input); err != nil {
		t.Fatalf("handle task: %v", err)
	}

	tools := map[ToolID]bool{}
	for _, binding := range runtime.request.ToolBindings {
		tools[binding.ToolID] = true
	}
	if !tools["mcp.marketing.marketing_wechat_draft_create"] || tools["mcp.marketing.marketing_publish_intent_get"] {
		t.Fatalf("filtered runtime tools = %+v", runtime.request.ToolBindings)
	}
	for _, id := range []ToolID{"list_skills", "load_skill", "list_skill_resources", "load_resource"} {
		if tools[id] {
			t.Fatalf("runtime allowlist must also restrict skill loader tool %s in %+v", id, runtime.request.ToolBindings)
		}
	}
}

func TestBaseAgentRuntimeRequestCarriesLLMProfileExtension(t *testing.T) {
	runtime := &capturingRuntimeRequest{response: RuntimeResponse{Status: RuntimeStatusCompleted, Messages: []RuntimeMessage{{Type: RuntimeMessageText, Content: "ok"}}}}
	agent := NewBaseAgent(BaseAgentConfig{
		Identity: AgentIdentity{AgentID: "agent-1", AgentProfileID: "profile-1", InstanceRef: AgentInstanceRef{ID: "instance-1"}},
		Profile: AgentProfile{
			ID: "profile-1",
			BaseAgent: BaseAgentSpec{
				InheritsBaseAgent: true,
				AuditRequired:     true,
				SystemPrompt:      "Managed system prompt",
				LLMAPIKeyRef:      "secret://system/llm-api-key",
				SkillScope:        "profile-bound-skills",
				ToolScope:         "profile-bound-tools",
				MCPScope:          "profile-bound-mcp",
				Extensions: map[string]json.RawMessage{
					BaseAgentLLMProfileExtensionKey: json.RawMessage(`{"profile_id":"llm-openai","display_name":"OpenAI Primary","provider":"openai","base_url":"https://api.openai.com/v1","api_key_ref":"secret://llm/openai","model":"gpt-5","temperature":0.2,"context_length":128000}`),
				},
			},
		},
		Runtime:  runtime,
		Signals:  &recordingSignalSink{},
		Delivery: &capturingDelivery{},
		Trace:    &recordingTraceRecorder{},
		Notifier: &recordingNotifier{},
		Validator: ResultValidatorFunc(func(AgentInput, AgentResult) error {
			return nil
		}),
	})

	if _, err := agent.Handle(context.Background(), validAgentInput()); err != nil {
		t.Fatalf("handle task: %v", err)
	}

	got := runtime.request.RuntimeOptions.ProviderSpecific
	if got["llm_profile_id"] != "llm-openai" ||
		got["llm_provider"] != "openai" ||
		got["llm_base_url"] != "https://api.openai.com/v1" ||
		got["api_key_ref"] != "secret://llm/openai" ||
		got["llm_api_key_ref"] != "secret://llm/openai" ||
		got["llm_model"] != "gpt-5" ||
		got["llm_context_length"] != "128000" {
		t.Fatalf("provider specific missing llm profile defaults: %#v", got)
	}
}

func TestBaseAgentRuntimeRequestCarriesDiaryTimezoneExtension(t *testing.T) {
	runtime := &capturingRuntimeRequest{response: RuntimeResponse{Status: RuntimeStatusCompleted, Messages: []RuntimeMessage{{Type: RuntimeMessageText, Content: "ok"}}}}
	agent := NewBaseAgent(BaseAgentConfig{
		Identity: AgentIdentity{AgentID: "agent-1", AgentProfileID: "profile-1", InstanceRef: AgentInstanceRef{ID: "instance-1"}},
		Profile: AgentProfile{ID: "profile-1", BaseAgent: BaseAgentSpec{Extensions: map[string]json.RawMessage{
			BaseAgentDiaryConfigExtensionKey: json.RawMessage(`{"timezone":"America/New_York"}`),
		}}},
		Runtime: runtime, Signals: &recordingSignalSink{}, Delivery: &capturingDelivery{}, Trace: &recordingTraceRecorder{},
		Notifier: &recordingNotifier{}, Validator: ResultValidatorFunc(func(AgentInput, AgentResult) error { return nil }),
	})
	if _, err := agent.Handle(context.Background(), validAgentInput()); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if got := runtime.request.RuntimeOptions.ProviderSpecific["diary_timezone"]; got != "America/New_York" {
		t.Fatalf("diary timezone = %q", got)
	}
}

func TestBaseAgentRuntimeRequestMergesInputRuntimeOptionsWithProfileConfig(t *testing.T) {
	runtime := &capturingRuntimeRequest{response: RuntimeResponse{Status: RuntimeStatusCompleted, Messages: []RuntimeMessage{{Type: RuntimeMessageText, Content: "ok"}}}}
	agent := NewBaseAgent(BaseAgentConfig{
		Identity: AgentIdentity{AgentID: "agent-1", AgentProfileID: "profile-1", InstanceRef: AgentInstanceRef{ID: "instance-1"}},
		Profile:  AgentProfile{ID: "profile-1", RuntimeSpec: RuntimeSpec{Kind: RuntimeKindOpenClaw, ConfigRef: "runtime-config-openclaw", Config: []byte(`{"agent_id":"planner"}`)}},
		Runtime:  runtime,
		Signals:  &recordingSignalSink{},
		Delivery: &capturingDelivery{},
		Trace:    &recordingTraceRecorder{},
		Notifier: &recordingNotifier{},
		Validator: ResultValidatorFunc(func(AgentInput, AgentResult) error {
			return nil
		}),
	})
	input := validAgentInput()
	input.RuntimeOptions = RuntimeOptions{
		WorkingDirectory: "D:/repo",
		Model:            "coordination-planner",
		SystemPrompt:     "You are the coordination Agent.",
		Timeout:          90 * time.Second,
		ProviderSpecific: map[string]string{"discussion_id": "conv-1"},
	}

	if _, err := agent.Handle(context.Background(), input); err != nil {
		t.Fatalf("handle task: %v", err)
	}

	opts := runtime.request.RuntimeOptions
	if opts.WorkingDirectory != "D:/repo" ||
		opts.Model != "coordination-planner" ||
		strings.Contains(opts.SystemPrompt, "核心指令") ||
		!strings.Contains(opts.SystemPrompt, "You are the coordination Agent.") ||
		opts.Timeout != 90*time.Second {
		t.Fatalf("runtime options = %+v", opts)
	}
	if strings.Contains(opts.SystemPrompt, "全局 BaseAgent 模板") {
		t.Fatalf("system prompt should not include legacy global template: %s", opts.SystemPrompt)
	}
	if string(opts.ProviderConfig) != `{"agent_id":"planner"}` {
		t.Fatalf("provider config = %s", opts.ProviderConfig)
	}
	if opts.ProviderSpecific["config_ref"] != "runtime-config-openclaw" || opts.ProviderSpecific["discussion_id"] != "conv-1" {
		t.Fatalf("provider specific = %+v", opts.ProviderSpecific)
	}
}

func TestBaseAgentRuntimeRequestCarriesMCPConfigFromInput(t *testing.T) {
	runtime := &capturingRuntimeRequest{response: RuntimeResponse{Status: RuntimeStatusCompleted, Messages: []RuntimeMessage{{Type: RuntimeMessageText, Content: "ok"}}}}
	agent := NewBaseAgent(BaseAgentConfig{
		Identity: AgentIdentity{AgentID: "agent-1", AgentProfileID: "profile-1", InstanceRef: AgentInstanceRef{ID: "instance-1"}},
		Profile:  AgentProfile{ID: "profile-1"},
		Runtime:  runtime,
		Signals:  &recordingSignalSink{},
		Delivery: &capturingDelivery{},
		Trace:    &recordingTraceRecorder{},
		Notifier: &recordingNotifier{},
		Validator: ResultValidatorFunc(func(AgentInput, AgentResult) error {
			return nil
		}),
	})
	input := validAgentInput()
	input.RuntimeOptions.MCPConfig = json.RawMessage(`{"servers":{"orrery":{"url":"http://127.0.0.1:8787/mcp"}}}`)

	if _, err := agent.Handle(context.Background(), input); err != nil {
		t.Fatalf("handle task: %v", err)
	}
	if string(runtime.request.RuntimeOptions.MCPConfig) != string(input.RuntimeOptions.MCPConfig) {
		t.Fatalf("mcp config = %s", runtime.request.RuntimeOptions.MCPConfig)
	}
	input.RuntimeOptions.MCPConfig[0] = '['
	if string(runtime.request.RuntimeOptions.MCPConfig) != `{"servers":{"orrery":{"url":"http://127.0.0.1:8787/mcp"}}}` {
		t.Fatalf("mcp config was not defensively copied: %s", runtime.request.RuntimeOptions.MCPConfig)
	}
}

func TestBaseAgentRuntimeRequestTreatsNullMCPConfigAsUnset(t *testing.T) {
	runtime := &capturingRuntimeRequest{response: RuntimeResponse{Status: RuntimeStatusCompleted, Messages: []RuntimeMessage{{Type: RuntimeMessageText, Content: "ok"}}}}
	agent := NewBaseAgent(BaseAgentConfig{
		Identity: AgentIdentity{AgentID: "agent-1", AgentProfileID: "profile-1", InstanceRef: AgentInstanceRef{ID: "instance-1"}},
		Profile:  AgentProfile{ID: "profile-1"},
		Runtime:  runtime,
		Signals:  &recordingSignalSink{},
		Delivery: &capturingDelivery{},
		Trace:    &recordingTraceRecorder{},
		Notifier: &recordingNotifier{},
		Validator: ResultValidatorFunc(func(AgentInput, AgentResult) error {
			return nil
		}),
	})
	input := validAgentInput()
	input.RuntimeOptions.MCPConfig = json.RawMessage(`null`)

	if _, err := agent.Handle(context.Background(), input); err != nil {
		t.Fatalf("handle task: %v", err)
	}
	if len(runtime.request.RuntimeOptions.MCPConfig) != 0 {
		t.Fatalf("null mcp config = %s, want unset", runtime.request.RuntimeOptions.MCPConfig)
	}
}

func TestBaseAgentRuntimeRequestCarriesSandboxProfileFromInput(t *testing.T) {
	runtime := &capturingRuntimeRequest{response: RuntimeResponse{Status: RuntimeStatusCompleted, Messages: []RuntimeMessage{{Type: RuntimeMessageText, Content: "ok"}}}}
	agent := NewBaseAgent(BaseAgentConfig{
		Identity: AgentIdentity{AgentID: "agent-1", AgentProfileID: "profile-1", InstanceRef: AgentInstanceRef{ID: "instance-1"}},
		Profile:  AgentProfile{ID: "profile-1"},
		Runtime:  runtime,
		Signals:  &recordingSignalSink{},
		Delivery: &capturingDelivery{},
		Trace:    &recordingTraceRecorder{},
		Notifier: &recordingNotifier{},
		Validator: ResultValidatorFunc(func(AgentInput, AgentResult) error {
			return nil
		}),
	})
	input := validAgentInput()
	input.RuntimeOptions.SandboxProfile = RuntimeSandboxProfile{
		Mode:                 RuntimeSandboxWorkspaceWrite,
		Approval:             RuntimeApprovalOnRequest,
		Network:              RuntimeNetworkDisabled,
		RuntimeHomeIsolation: RuntimeHomeIsolatedPerRun,
		Retention:            RuntimeRetentionKeepOnFailure,
		Root:                 "D:/workspace",
		WorkDir:              "D:/workspace/missions/m/tasks/t/runs/r",
		WritableRoots:        []string{"D:/workspace"},
		EnforceBoundary:      true,
	}

	if _, err := agent.Handle(context.Background(), input); err != nil {
		t.Fatalf("handle task: %v", err)
	}
	got := runtime.request.RuntimeOptions.SandboxProfile
	if got.Mode != RuntimeSandboxWorkspaceWrite ||
		got.Approval != RuntimeApprovalOnRequest ||
		got.Network != RuntimeNetworkDisabled ||
		got.RuntimeHomeIsolation != RuntimeHomeIsolatedPerRun ||
		got.Retention != RuntimeRetentionKeepOnFailure ||
		len(got.WritableRoots) != 1 ||
		got.WritableRoots[0] != "D:/workspace" {
		t.Fatalf("sandbox profile = %+v", got)
	}
	input.RuntimeOptions.SandboxProfile.WritableRoots[0] = "D:/mutated"
	if runtime.request.RuntimeOptions.SandboxProfile.WritableRoots[0] != "D:/workspace" {
		t.Fatalf("sandbox profile was not defensively copied: %+v", runtime.request.RuntimeOptions.SandboxProfile)
	}
}

func TestBaseAgentRuntimeRequestKeepsManagedPromptWhenRuntimeProvidesCustomPrompt(t *testing.T) {
	runtime := &capturingRuntimeRequest{response: RuntimeResponse{Status: RuntimeStatusCompleted, Messages: []RuntimeMessage{{Type: RuntimeMessageText, Content: "ok"}}}}
	agent := NewBaseAgent(BaseAgentConfig{
		Identity: AgentIdentity{AgentID: "agent-1", AgentProfileID: "profile-1", InstanceRef: AgentInstanceRef{ID: "instance-1"}},
		Profile: AgentProfile{
			ID: "profile-1",
			BaseAgent: BaseAgentSpec{
				InheritsBaseAgent: true,
				AuditRequired:     true,
				SystemPrompt:      "Verify audit evidence before reporting success.",
				LLMAPIKeyRef:      "secret://system/llm-api-key",
				SkillScope:        "profile_bound",
				ToolScope:         "profile_bound",
				MCPScope:          "profile_bound",
			},
		},
		Runtime:  runtime,
		Signals:  &recordingSignalSink{},
		Delivery: &capturingDelivery{},
		Trace:    &recordingTraceRecorder{},
		Notifier: &recordingNotifier{},
		Validator: ResultValidatorFunc(func(AgentInput, AgentResult) error {
			return nil
		}),
	})
	input := validAgentInput()
	input.RuntimeOptions.SystemPrompt = "Agent-specific discussion prompt."

	if _, err := agent.Handle(context.Background(), input); err != nil {
		t.Fatalf("handle task: %v", err)
	}

	for _, want := range []string{
		"角色设定", "Verify audit evidence before reporting success.", "Agent-specific discussion prompt.",
	} {
		if !strings.Contains(runtime.request.RuntimeOptions.SystemPrompt, want) {
			t.Fatalf("system prompt missing %q: %s", want, runtime.request.RuntimeOptions.SystemPrompt)
		}
	}
	for _, unexpected := range []string{"全局 BaseAgent 模板", "TODO(memory-team)", "TODO(knowledge-team)", "Memory 占位", "Knowledge 占位"} {
		if strings.Contains(runtime.request.RuntimeOptions.SystemPrompt, unexpected) {
			t.Fatalf("legacy placeholders should be removed from system prompt %q: %s", unexpected, runtime.request.RuntimeOptions.SystemPrompt)
		}
	}

}

func TestBaseAgentRuntimeRequestIncludesProfileAndRuntimeWorkingInstructions(t *testing.T) {
	runtime := &capturingRuntimeRequest{response: RuntimeResponse{Status: RuntimeStatusCompleted, Messages: []RuntimeMessage{{Type: RuntimeMessageText, Content: "ok"}}}}
	agent := NewBaseAgent(BaseAgentConfig{
		Identity: AgentIdentity{AgentID: "agent-1", AgentProfileID: "profile-1", InstanceRef: AgentInstanceRef{ID: "instance-1"}},
		Profile: AgentProfile{
			ID: "profile-1",
			BaseAgent: BaseAgentSpec{
				InheritsBaseAgent: true,
				AuditRequired:     true,
				SystemPrompt:      "Agent-specific base prompt.",
				LLMAPIKeyRef:      "secret://system/llm-api-key",
				SkillScope:        "profile_bound",
				ToolScope:         "profile_bound",
				MCPScope:          "profile_bound",
			},
		},
		Runtime:  runtime,
		Signals:  &recordingSignalSink{},
		Delivery: &capturingDelivery{},
		Trace:    &recordingTraceRecorder{},
		Notifier: &recordingNotifier{},
		Validator: ResultValidatorFunc(func(AgentInput, AgentResult) error {
			return nil
		}),
	})
	input := validAgentInput()
	input.RuntimeOptions.SystemPrompt = "Runtime conversation prompt."

	if _, err := agent.Handle(context.Background(), input); err != nil {
		t.Fatalf("handle task: %v", err)
	}

	for _, want := range []string{
		"角色设定",
	} {
		if !strings.Contains(runtime.request.RuntimeOptions.SystemPrompt, want) {
			t.Fatalf("runtime system prompt missing %q: %s", want, runtime.request.RuntimeOptions.SystemPrompt)
		}
	}
	for _, want := range []string{"Agent-specific base prompt.", "Runtime conversation prompt."} {
		if strings.Count(runtime.request.RuntimeOptions.SystemPrompt, want) != 1 {
			t.Fatalf("working instruction should occur once: %q", want)
		}
	}
	if strings.Contains(runtime.request.RuntimeOptions.SystemPrompt, "core_prompt") {
		t.Fatal("legacy core prompt block returned")
	}
}

func TestBaseAgentRuntimeRequestDeduplicatesSharedWorkingInstructions(t *testing.T) {
	runtime := &capturingRuntimeRequest{response: RuntimeResponse{Status: RuntimeStatusCompleted, Messages: []RuntimeMessage{{Type: RuntimeMessageText, Content: "ok"}}}}
	agent := NewBaseAgent(BaseAgentConfig{
		Identity: AgentIdentity{AgentID: "agent-1", AgentProfileID: "profile-1", InstanceRef: AgentInstanceRef{ID: "instance-1"}},
		Profile: AgentProfile{
			ID: "profile-1",
			BaseAgent: BaseAgentSpec{
				InheritsBaseAgent: true,
				AuditRequired:     true,
				SystemPrompt:      "Managed system prompt",
				LLMAPIKeyRef:      "secret://system/llm-api-key",
				SkillScope:        "profile_bound",
				ToolScope:         "profile_bound",
				MCPScope:          "profile_bound",
			},
		},
		Runtime:  runtime,
		Signals:  &recordingSignalSink{},
		Delivery: &capturingDelivery{},
		Trace:    &recordingTraceRecorder{},
		Notifier: &recordingNotifier{},
		Validator: ResultValidatorFunc(func(AgentInput, AgentResult) error {
			return nil
		}),
	})
	input := validAgentInput()
	input.RuntimeOptions.SystemPrompt = "Managed system prompt\n\nDiscussion tool contract."

	if _, err := agent.Handle(context.Background(), input); err != nil {
		t.Fatalf("handle task: %v", err)
	}

	for _, want := range []string{"角色设定"} {
		if !strings.Contains(runtime.request.RuntimeOptions.SystemPrompt, want) {
			t.Fatalf("system prompt missing %q: %s", want, runtime.request.RuntimeOptions.SystemPrompt)
		}
	}
	for _, want := range []string{"Managed system prompt", "Discussion tool contract."} {
		if strings.Count(runtime.request.RuntimeOptions.SystemPrompt, want) != 1 {
			t.Fatalf("working instruction should occur once: %q", want)
		}
	}
	if strings.Contains(runtime.request.RuntimeOptions.SystemPrompt, "core_prompt") {
		t.Fatal("legacy core prompt block returned")
	}
}
