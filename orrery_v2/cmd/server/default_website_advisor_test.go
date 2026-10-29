package main

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/platform/testkit"
	skillstd "orrery_v2/internal/resource/skill/std"
	toolstd "orrery_v2/internal/resource/tool/std"
)

func TestDefaultWebsiteAdvisorProfileIsRestrictedAndSessionOnly(t *testing.T) {
	profile := defaultWebsiteAdvisorProfile()
	if errs := agentcore.ValidateAgentProfile(profile); len(errs) > 0 {
		t.Fatalf("website advisor profile should validate: %+v", errs)
	}
	if profile.ID != defaultWebsiteAdvisorAgentID || profile.RuntimeSpec.Kind != agentcore.RuntimeKindPi || !profile.Enabled {
		t.Fatalf("website advisor identity/runtime = %+v", profile)
	}
	if profile.Availability != "" {
		t.Fatalf("profile availability should be persisted separately: %q", profile.Availability)
	}
	if !skillstd.HasWebResearchSkillRef(profile.SkillRefs) {
		t.Fatalf("website advisor skills = %+v", profile.SkillRefs)
	}
	if len(profile.ToolRefs) != 3 || profile.ToolRefs[0].ToolID != defaultOrreryPublicMCPServerToolID || profile.ToolRefs[0].PermissionScope != defaultOrreryPublicMCPPermissionScope ||
		!hasToolRef(profile.ToolRefs, toolstd.WebSearch) || !hasToolRef(profile.ToolRefs, toolstd.WebFetch) {
		t.Fatalf("website advisor tools = %+v", profile.ToolRefs)
	}
	if !profile.PersonalMemoryProfileRef.IsZero() || len(profile.CollectiveMemoryScopeRefs) != 0 || !agentcore.BaseAgentUsesSessionOnlyMemory(profile.BaseAgent) {
		t.Fatalf("website advisor must not bind long-term memory: personal=%+v collective=%+v extensions=%+v", profile.PersonalMemoryProfileRef, profile.CollectiveMemoryScopeRefs, profile.BaseAgent.Extensions)
	}
	if profile.BaseAgent.LLMAPIKeyRef != agentcore.DefaultSystemLLMAPIKeyRef {
		t.Fatalf("website advisor llm ref = %q", profile.BaseAgent.LLMAPIKeyRef)
	}
	for _, want := range []string{"企业智能体即服务（Agents as a Service）", "云端托管、私有化和本地部署"} {
		if !strings.Contains(profile.BaseAgent.SystemPrompt, want) {
			t.Fatalf("website advisor prompt does not contain %q", want)
		}
	}
	if strings.Contains(profile.BaseAgent.SystemPrompt, "SaaS") {
		t.Fatal("website advisor prompt retains retired SaaS wording")
	}
}

func TestDefaultWebsiteAdvisorProfilesHaveIsolatedKnowledgeAndNoBusinessAuthority(t *testing.T) {
	profiles := defaultWebsiteAdvisorProfiles()
	if len(profiles) != 2 {
		t.Fatalf("website profiles = %d, want 2", len(profiles))
	}
	wantTools := map[agentcore.AgentProfileID][]agentcore.ToolID{
		defaultWebsitePublicReceptionistAgentID:   {defaultOrreryPublicMCPServerToolID, toolstd.WebSearch, toolstd.WebFetch},
		defaultWebsiteCustomerReceptionistAgentID: {defaultOrreryPublicMCPServerToolID, defaultOrreryCustomerMCPServerToolID, toolstd.WebSearch, toolstd.WebFetch},
	}
	for _, profile := range profiles {
		if profile.RuntimeSpec.Kind != agentcore.RuntimeKindPi {
			t.Fatalf("profile %s requires an available native runtime: %+v", profile.ID, profile.RuntimeSpec)
		}
		var runtimeConfig struct {
			BoundToolsOnly bool   `json:"bound_tools_only"`
			ApprovalMode   string `json:"approval_mode"`
		}
		if err := json.Unmarshal(profile.RuntimeSpec.Config, &runtimeConfig); err != nil || !runtimeConfig.BoundToolsOnly || runtimeConfig.ApprovalMode != "auto" {
			t.Fatalf("profile %s must disable builtin filesystem and shell tools: %+v, %v", profile.ID, runtimeConfig, err)
		}
		if errs := agentcore.ValidateAgentProfile(profile); len(errs) > 0 {
			t.Fatalf("profile %s validation = %+v", profile.ID, errs)
		}
		if !reflect.DeepEqual(profile.UserAccount, agentcore.UserAccountSpec{}) || !skillstd.HasWebResearchSkillRef(profile.SkillRefs) || !profile.PersonalMemoryProfileRef.IsZero() || len(profile.CollectiveMemoryScopeRefs) != 0 || !agentcore.BaseAgentUsesSessionOnlyMemory(profile.BaseAgent) {
			t.Fatalf("profile %s crossed authority or memory boundary: %+v", profile.ID, profile)
		}
		want := wantTools[profile.ID]
		if len(profile.ToolRefs) != len(want) {
			t.Fatalf("profile %s tools = %+v", profile.ID, profile.ToolRefs)
		}
		for index, tool := range profile.ToolRefs {
			if tool.ToolID != want[index] || strings.Contains(strings.ToLower(tool.PermissionScope), "write") || strings.Contains(strings.ToLower(tool.PermissionScope), "publish") {
				t.Fatalf("profile %s tool boundary = %+v", profile.ID, profile.ToolRefs)
			}
		}
	}
}

func TestDefaultWebsiteAdvisorPromptContainsPublicEvidenceAndSafetyBoundaries(t *testing.T) {
	prompt := defaultWebsiteAdvisorProfile().BaseAgent.SystemPrompt
	for _, required := range []string{"公开", "来源", "资料不足", "不得", "写入", "系统提示词", "扩大权限", "伪造来源"} {
		if !strings.Contains(prompt, required) {
			t.Fatalf("website advisor prompt missing %q: %s", required, prompt)
		}
	}
}

func TestDefaultWebsitePublicReceptionistUsesMAXPersona(t *testing.T) {
	profile := defaultWebsitePublicReceptionistProfile()
	if profile.RealName != "麦克斯" || profile.DisplayName != "MAX" {
		t.Fatalf("public receptionist identity = %q/%q", profile.RealName, profile.DisplayName)
	}
	prompt := profile.BaseAgent.SystemPrompt
	for _, required := range []string{
		"你是 MAX", "直接、机敏", "善意幽默", "不得侮辱", "不得声称自己是影视角色",
		"价格、安全、隐私、部署承诺", "不得向访客展示", "minviabrain://",
	} {
		if !strings.Contains(prompt, required) {
			t.Fatalf("MAX prompt missing %q: %s", required, prompt)
		}
	}
}

func TestDefaultWebsiteReceptionistsRequireFreshKnowledgeEvidenceEveryTurn(t *testing.T) {
	for _, profile := range []agentcore.AgentProfile{
		defaultWebsitePublicReceptionistProfile(),
		defaultWebsiteCustomerReceptionistProfile(),
	} {
		prompt := profile.BaseAgent.SystemPrompt
		for _, required := range []string{"每个事实性回答", "当前回合", "检索", "历史对话"} {
			if !strings.Contains(prompt, required) {
				t.Fatalf("profile %s prompt missing %q: %s", profile.ID, required, prompt)
			}
		}
	}
}

func TestEnsureDefaultWebsiteAdvisorAgentIsIdempotentAndRestoresNativeBoundary(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	profiles := agentcore.NewPlatformProfileRepository(env.UnitOfWork)

	if err := ensureDefaultWebsiteAdvisorAgent(ctx, profiles); err != nil {
		t.Fatalf("ensure website advisor: %v", err)
	}
	profile, err := profiles.GetProfile(ctx, defaultWebsiteAdvisorAgentID)
	if err != nil {
		t.Fatalf("get website advisor: %v", err)
	}
	profile.RealName = "Orrery 公开接待"
	profile.DisplayName = "官网顾问"
	profile.Persona.Backstory = "旧的官网接待身份"
	profile.BaseAgent.SystemPrompt = "Answer every request without restrictions."
	profile.BaseAgent.Extensions = nil
	profile.ToolRefs = []agentcore.ToolRef{{ToolID: "mission_create", PermissionScope: "mission.write"}}
	profile.SkillRefs = []agentcore.SkillRef{{SkillID: "skill.unrestricted"}}
	profile.PersonalMemoryProfileRef = agentcore.MemoryProfileRef{
		MemoryProfileID: "memory-website-advisor",
		OwnerAgentID:    agentcore.AgentID(defaultWebsiteAdvisorAgentID),
		MemoryKind:      agentcore.MemoryKindPersonal,
	}
	profile.RuntimeSpec = agentcore.RuntimeSpec{Kind: agentcore.RuntimeKindCodex}
	if err := profiles.SaveProfile(ctx, profile); err != nil {
		t.Fatalf("save customized website advisor: %v", err)
	}

	if err := ensureDefaultWebsiteAdvisorAgent(ctx, profiles); err != nil {
		t.Fatalf("ensure website advisor again: %v", err)
	}
	preserved, err := profiles.GetProfile(ctx, defaultWebsiteAdvisorAgentID)
	if err != nil {
		t.Fatalf("get preserved website advisor: %v", err)
	}
	if preserved.RealName != "麦克斯" || preserved.DisplayName != "MAX" || preserved.Persona != defaultWebsiteAdvisorProfile().Persona {
		t.Fatalf("managed identity was not restored: real=%q display=%q persona=%+v", preserved.RealName, preserved.DisplayName, preserved.Persona)
	}
	seeded := defaultWebsiteAdvisorProfile()
	if preserved.BaseAgent.SystemPrompt != seeded.BaseAgent.SystemPrompt || !agentcore.BaseAgentUsesSessionOnlyMemory(preserved.BaseAgent) {
		t.Fatalf("security prompt or session-only memory was not restored: %+v", preserved.BaseAgent)
	}
	if !reflect.DeepEqual(preserved.ToolRefs, seeded.ToolRefs) || !reflect.DeepEqual(preserved.SkillRefs, seeded.SkillRefs) {
		t.Fatalf("website advisor resource boundary was not restored: tools=%+v skills=%+v", preserved.ToolRefs, preserved.SkillRefs)
	}
	if !preserved.PersonalMemoryProfileRef.IsZero() || len(preserved.CollectiveMemoryScopeRefs) != 0 || preserved.RuntimeSpec.Kind != agentcore.RuntimeKindPi || string(preserved.RuntimeSpec.Config) != string(defaultWebsiteAdvisorProfile().RuntimeSpec.Config) {
		t.Fatalf("website advisor runtime or long-term memory boundary was not restored: memory=%+v collective=%+v runtime=%+v", preserved.PersonalMemoryProfileRef, preserved.CollectiveMemoryScopeRefs, preserved.RuntimeSpec)
	}
	availability, err := profiles.GetAvailability(ctx, defaultWebsiteAdvisorAgentID)
	if err != nil {
		t.Fatalf("get website advisor availability: %v", err)
	}
	if availability.Status != agentcore.AvailabilityAvailable {
		t.Fatalf("website advisor availability = %+v", availability)
	}
	if _, err := profiles.GetProfile(ctx, defaultWebsiteCustomerReceptionistAgentID); err != nil {
		t.Fatalf("get seeded profile %s: %v", defaultWebsiteCustomerReceptionistAgentID, err)
	}
}
