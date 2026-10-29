package main

import (
	"encoding/json"
	"strings"
	"testing"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/platform/config"
)

func TestDefaultAgentProfilesAreBaseAgentManagedWithChineseIdentity(t *testing.T) {
	runtime := agentcore.RuntimeSpec{Kind: agentcore.RuntimeKindCodex}
	coordinators, err := defaultCoordinatorAgentProfiles(runtime)
	if err != nil {
		t.Fatalf("defaultCoordinatorAgentProfiles: %v", err)
	}
	freelancers, err := defaultFreelanceAgentProfiles(runtime)
	if err != nil {
		t.Fatalf("defaultFreelanceAgentProfiles: %v", err)
	}
	profiles := append(coordinators, freelancers...)
	if len(profiles) < 4 {
		t.Fatalf("default profiles too small: %d", len(profiles))
	}
	for _, profile := range profiles {
		if !profile.BaseAgent.InheritsBaseAgent || !profile.BaseAgent.AuditRequired {
			t.Fatalf("%s is not BaseAgent audited: %+v", profile.ID, profile.BaseAgent)
		}
		if profile.BaseAgent.SystemPrompt == "" || profile.BaseAgent.LLMAPIKeyRef == "" {
			t.Fatalf("%s missing BaseAgent runtime prompt or system llm key: %+v", profile.ID, profile.BaseAgent)
		}
		if !containsChinese(profile.RealName) || !containsChinese(profile.Persona.ResponsibilityBoundary) {
			t.Fatalf("%s missing Chinese humanized identity: real_name=%q responsibility=%q", profile.ID, profile.RealName, profile.Persona.ResponsibilityBoundary)
		}
		if errs := agentcore.ValidateAgentProfile(profile); len(errs) > 0 {
			t.Fatalf("%s validation errors: %+v", profile.ID, errs)
		}
	}
}

func TestDefaultBaseAgentTemplateAppliesLeanFallbackPrompt(t *testing.T) {
	spec := defaultBaseAgentSpec("苏砚青", "Frontend Builder")

	if !spec.InheritsBaseAgent || !spec.AuditRequired {
		t.Fatalf("BaseAgent flags = %+v", spec)
	}
	if spec.SkillScope != "profile_bound" || spec.ToolScope != "profile_bound" || spec.MCPScope != "profile_bound" {
		t.Fatalf("resource scopes = skill:%q tool:%q mcp:%q", spec.SkillScope, spec.ToolScope, spec.MCPScope)
	}
	for _, want := range []string{
		"苏砚青",
		"Frontend Builder",
	} {
		if !strings.Contains(spec.SystemPrompt, want) {
			t.Fatalf("system prompt missing %q: %s", want, spec.SystemPrompt)
		}
	}
	for _, removed := range []string{"全局 BaseAgent 模板", "Memory 占位", "Knowledge 占位", "<capabilities>"} {
		if strings.Contains(spec.SystemPrompt, removed) {
			t.Fatalf("fallback prompt should not include prompt factory section %q: %s", removed, spec.SystemPrompt)
		}
	}
}

func TestDefaultCoordinatorAgentProfileBindsToolsFromTemplate(t *testing.T) {
	var template defaultAgentTemplate
	if err := json.Unmarshal([]byte(`{
		"slug": "custom-coordinator",
		"name": "配置化协调者",
		"instructions": "Use configured tools only.",
		"worker_config": {"role": "Configured coordinator"},
		"tools": [
			{"tool_id": "tool.configured", "permission_scope": "mission.write"},
			{"tool_id": "tool.notify", "permission_scope": "discussion.write"}
		]
	}`), &template); err != nil {
		t.Fatal(err)
	}

	profile := defaultCoordinatorAgentProfile("custom-coordinator", "custom", agentcore.RuntimeSpec{}, template)

	got := agentToolRefs(profile)
	if !got["tool.configured"] || !got["tool.notify"] {
		t.Fatalf("template tool refs were not bound: %+v", profile.ToolRefs)
	}
}

func TestDefaultConversationMembersComeFromTemplates(t *testing.T) {
	var templates []defaultAgentTemplate
	for _, raw := range []string{
		`{
			"slug": "primary-agent",
			"name": "主协调者",
			"instructions": "Lead discussion.",
			"worker_config": {"role": "Primary coordinator"},
			"conversation_member": {"enabled": true, "role": "Lead"}
		}`,
		`{
			"slug": "staffing-agent",
			"name": "人员协调者",
			"instructions": "Staff discussion.",
			"worker_config": {"role": "Staffing coordinator"},
			"conversation_member": {"enabled": true, "role": "Staffing"}
		}`,
	} {
		var template defaultAgentTemplate
		if err := json.Unmarshal([]byte(raw), &template); err != nil {
			t.Fatal(err)
		}
		templates = append(templates, template)
	}

	members := initialAgentMemberSeedsFromTemplates("custom-primary", templates)

	if len(members) != 2 ||
		members[0].AgentID != "custom-primary" ||
		members[0].Role != "Lead" ||
		members[1].AgentID != "staffing-agent" ||
		members[1].Role != "Staffing" {
		t.Fatalf("members = %+v", members)
	}

	cfg, err := config.LoadYAML(testConfigSource(t, map[string]string{"discussion.agent_id": "custom-primary"}))
	if err != nil {
		t.Fatal(err)
	}
	defaults, err := initialAgentMemberSeedsFromConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(defaults) != 1 ||
		defaults[0].AgentID != "custom-primary" ||
		defaults[0].Role != "Lead" {
		t.Fatalf("default members = %+v", defaults)
	}
}

func TestDefaultAgentProfilesIgnorePMRuntimeEnvAndUseAgentRuntimeConfig(t *testing.T) {
	setTestConfigOverride(t, "discussion.agent.runtime_kind", "codex")
	setTestConfigOverride(t, "discussion.agent.runtime_config", `{"executable":"codex-fixture"}`)
	cfg, err := config.LoadYAML(writeTestConfigYAML(t, `
agent_defaults:
  runtime_kind: openclaw
  runtime_config:
    executable: openclaw-default
agents:
  frontend-builder:
    runtime_kind: claude
    runtime_config_ref: runtime-frontend-claude
    runtime_config:
      executable: claude
      model: claude-sonnet-4
`))
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	pmRuntime := defaultAgentRuntimeSpec(cfg, defaultLeadAgentID)
	frontendRuntime := defaultAgentRuntimeSpec(cfg, "frontend-builder")
	workerRuntime := defaultAgentRuntimeSpec(cfg, "bug-fixer")

	if pmRuntime.Kind != agentcore.RuntimeKindOpenClaw || !strings.Contains(string(pmRuntime.Config), "openclaw-default") {
		t.Fatalf("default runtime should ignore discussion agent runtime env and use agent defaults: %+v config=%s", pmRuntime, pmRuntime.Config)
	}
	if frontendRuntime.Kind != agentcore.RuntimeKindClaude || frontendRuntime.ConfigRef != "runtime-frontend-claude" || !strings.Contains(string(frontendRuntime.Config), "claude-sonnet-4") {
		t.Fatalf("frontend runtime = %+v config=%s", frontendRuntime, frontendRuntime.Config)
	}
	if workerRuntime.Kind != agentcore.RuntimeKindOpenClaw || !strings.Contains(string(workerRuntime.Config), "openclaw-default") {
		t.Fatalf("worker runtime = %+v config=%s", workerRuntime, workerRuntime.Config)
	}
}

func TestDefaultAgentRuntimeSpecDefaultsToPiWhenUnconfigured(t *testing.T) {
	cfg, err := config.LoadYAML(testConfigSource(t))
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	runtime := defaultAgentRuntimeSpec(cfg, "bug-fixer")

	if runtime.Kind != agentcore.RuntimeKindPi || runtime.ConfigRef != "" || len(runtime.Config) != 0 {
		t.Fatalf("unconfigured default runtime should use pi runtime: %+v", runtime)
	}
}

func TestDefaultAgentRuntimeSpecUsesPiWithDefaultRuntimeConfigWhenKindUnconfigured(t *testing.T) {
	cfg, err := config.LoadYAML(writeTestConfigYAML(t, `
agent_defaults:
  runtime_config_ref: runtime-pi-local
  runtime_config:
    executable: pi
    extra_args:
      - --provider
      - openai
`))
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	runtime := defaultAgentRuntimeSpec(cfg, "bug-fixer")

	if runtime.Kind != agentcore.RuntimeKindPi || runtime.ConfigRef != "runtime-pi-local" || !strings.Contains(string(runtime.Config), "openai") {
		t.Fatalf("default pi runtime should preserve default config: %+v config=%s", runtime, runtime.Config)
	}
}
