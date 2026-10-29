package operations_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/operations"
)

func TestCommandServiceHidesPersonalAssistantFromWorkforceMutations(t *testing.T) {
	ctx := context.Background()
	baseAgent := agentcore.DefaultBaseAgentSpec("个人助理", "个人助理")
	baseAgent.SystemPrompt = "旧提示词"
	store := &fakeWorkforceProfileStore{profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{
		"personal-assistant-1": {
			ID:                        "personal-assistant-1",
			PersonalAssistantOwnerID:  "user-1",
			PersonalAssistantTenantID: "orrery",
			RealName:                  "个人助理",
			DisplayName:               "个人助理",
			AgentKind:                 agentcore.AgentKindAI,
			OrgIdentity: agentcore.AgentOrgIdentity{
				OrganizationRef:   agentcore.OrganizationRef{ID: "orrery"},
				DepartmentRef:     agentcore.DepartmentRef{ID: "operations"},
				PositionRef:       agentcore.PositionRef{ID: "worker"},
				EmploymentType:    agentcore.EmploymentTypeAIAgent,
				SupervisionPolicy: agentcore.SupervisionPolicySupervised,
			},
			Persona:                 agentcore.AgentPersona{ResponsibilityBoundary: "仅处理所属用户授权的任务。"},
			BaseAgent:               baseAgent,
			PersonalKnowledgeBaseID: "knowledge-personal-assistant-1",
			PersonalMemoryProfileRef: agentcore.MemoryProfileRef{
				MemoryProfileID: "memory-personal-assistant-1", OwnerAgentID: "personal-assistant-1", MemoryKind: agentcore.MemoryKindPersonal,
			},
			SkillRefs:   []agentcore.SkillRef{},
			ToolRefs:    []agentcore.ToolRef{{ToolID: "knowledge_search"}},
			RuntimeSpec: agentcore.RuntimeSpec{Kind: agentcore.RuntimeKindPi, ConfigRef: "runtime-pi"},
			Enabled:     true,
		},
	}}
	service := operations.NewCommandService(store)
	viewer := operations.ViewerRef{ViewerID: "admin", PermissionScope: []string{operations.PermissionWorkforceManage}}

	_, err := service.UpsertWorkforceMember(ctx, viewer, operations.UpsertWorkforceMemberCommand{
		AgentProfileID:   "personal-assistant-1",
		DisplayName:      "我的助理",
		AgentKind:        "ai",
		EmploymentType:   "ai_agent",
		OrganizationID:   "orrery",
		DepartmentID:     "operations",
		PositionID:       "worker",
		RuntimeKind:      "pi",
		RuntimeConfigRef: "runtime-pi",
		SystemPrompt:     "只处理我明确授权的任务。",
		Persona:          operations.WorkforcePersonaDTO{ResponsibilityBoundary: "仅处理所属用户明确授权的任务。"},
		PersonalMemoryID: "memory-personal-assistant-1",
		SkillRefs:        []operations.WorkforceSkillRefDTO{{SkillID: "skill.personal-planning"}},
		ToolRefs:         []operations.WorkforceToolRefDTO{{ToolID: "exec_command"}},
		Enabled:          true,
	})
	if !errors.Is(err, operations.ErrNotFound) {
		t.Fatalf("UpsertWorkforceMember error = %v, want ErrNotFound", err)
	}
	if err := service.SetWorkforceMemberEnabled(ctx, viewer, operations.SetWorkforceMemberEnabledCommand{AgentProfileID: "personal-assistant-1", Enabled: false}); !errors.Is(err, operations.ErrNotFound) {
		t.Fatalf("SetWorkforceMemberEnabled error = %v, want ErrNotFound", err)
	}
	if err := service.ReleaseWorkforceMember(ctx, viewer, operations.ReleaseWorkforceMemberCommand{AgentProfileID: "personal-assistant-1"}); !errors.Is(err, operations.ErrNotFound) {
		t.Fatalf("ReleaseWorkforceMember error = %v, want ErrNotFound", err)
	}
	if _, err := service.HireTalentFromMarket(ctx, viewer, operations.HireTalentFromMarketCommand{AgentProfileID: "personal-assistant-1"}); !errors.Is(err, operations.ErrNotFound) {
		t.Fatalf("HireTalentFromMarket error = %v, want ErrNotFound", err)
	}
	profile := store.profiles["personal-assistant-1"]
	if !profile.Enabled || profile.DisplayName != "个人助理" || profile.BaseAgent.SystemPrompt != "旧提示词" {
		t.Fatalf("personal assistant was mutated through workforce service: %+v", profile)
	}
}

func TestCommandServiceKeepsConfiguredBindingsForExplicitRuntimeAgent(t *testing.T) {
	ctx := context.Background()
	store := &fakeWorkforceProfileStore{profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{}}
	service := operations.NewCommandService(store)
	orgs := newFakeOrganizationCatalogStore()
	seedOrganizationCatalog(t, ctx, orgs, "orrery", []operations.DepartmentDTO{
		{DepartmentID: "operations", OrganizationID: "orrery", Name: "Operations"},
	}, []operations.PositionDTO{
		{PositionID: "worker", DepartmentID: "operations", Name: "Worker"},
	})
	service.SetOrganizationCatalogStore(orgs)
	viewer := operations.ViewerRef{ViewerID: "admin", PermissionScope: []string{operations.PermissionWorkforceManage}}

	_, err := service.UpsertWorkforceMember(ctx, viewer, operations.UpsertWorkforceMemberCommand{
		AgentProfileID:   "profile-openclaw",
		RealName:         "OpenClaw Worker",
		AgentKind:        "ai",
		EmploymentType:   "ai_agent",
		OrganizationID:   "orrery",
		DepartmentID:     "operations",
		PositionID:       "worker",
		RuntimeKind:      "openclaw",
		RuntimeConfigRef: "runtime-config-openclaw",
		RuntimeConfig:    json.RawMessage(`{"executable":"openclaw","agent_id":"planner","extra_args":["--channel","ops"]}`),
		SkillRefs:        []operations.WorkforceSkillRefDTO{{SkillID: "skill.plan"}},
		ToolRefs:         []operations.WorkforceToolRefDTO{{ToolID: "tool.git"}},
		Persona:          operations.WorkforcePersonaDTO{ResponsibilityBoundary: "Run OpenClaw tasks"},
		PersonalMemoryID: "memory-openclaw",
		Enabled:          true,
	})
	if err != nil {
		t.Fatalf("UpsertWorkforceMember: %v", err)
	}

	profile := store.profiles["profile-openclaw"]
	if profile.RuntimeSpec.Kind != agentcore.RuntimeKindOpenClaw {
		t.Fatalf("runtime kind = %q, want openclaw", profile.RuntimeSpec.Kind)
	}
	if profile.RuntimeSpec.ConfigRef != "runtime-config-openclaw" {
		t.Fatalf("runtime config ref = %q", profile.RuntimeSpec.ConfigRef)
	}
	if len(profile.RuntimeSpec.Config) != 0 {
		t.Fatalf("runtime config should stay in settings runtime profiles, got: %s", profile.RuntimeSpec.Config)
	}
	if len(profile.SkillRefs) != 2 || profile.SkillRefs[0].SkillID != "skill.plan" {
		t.Fatalf("explicit runtime agent should keep configured skill bindings: %+v", profile.SkillRefs)
	}
	assertConfiguredAgentToolAndDefaultReads(t, profile, agentcore.ToolRef{ToolID: "tool.git"})
	if !profile.BaseAgent.InheritsBaseAgent || !profile.BaseAgent.AuditRequired || profile.BaseAgent.LLMAPIKeyRef == "" {
		t.Fatalf("runtime-backed profile should still be BaseAgent managed: %+v", profile.BaseAgent)
	}
}

func TestCommandServiceKeepsHumanProfileOnBaseAgentDefaultRuntimeWithConfigBindings(t *testing.T) {
	ctx := context.Background()
	store := &fakeWorkforceProfileStore{profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{}}
	service := operations.NewCommandService(store)
	orgs := newFakeOrganizationCatalogStore()
	seedOrganizationCatalog(t, ctx, orgs, "orrery", []operations.DepartmentDTO{
		{DepartmentID: "operations", OrganizationID: "orrery", Name: "Operations"},
	}, []operations.PositionDTO{
		{PositionID: "reviewer", DepartmentID: "operations", Name: "Reviewer"},
	})
	service.SetOrganizationCatalogStore(orgs)
	viewer := operations.ViewerRef{ViewerID: "admin", PermissionScope: []string{operations.PermissionWorkforceManage}}

	_, err := service.UpsertWorkforceMember(ctx, viewer, operations.UpsertWorkforceMemberCommand{
		AgentProfileID:   "profile-human-reviewer",
		RealName:         "Operator User",
		AgentKind:        "human",
		EmploymentType:   "human",
		OrganizationID:   "orrery",
		DepartmentID:     "operations",
		PositionID:       "reviewer",
		SkillRefs:        []operations.WorkforceSkillRefDTO{{SkillID: "skill.review"}},
		ToolRefs:         []operations.WorkforceToolRefDTO{{ToolID: "tool.approval"}},
		Persona:          operations.WorkforcePersonaDTO{ResponsibilityBoundary: "Review release plans and approve risky changes."},
		PersonalMemoryID: "memory-human-reviewer",
		Enabled:          true,
	})
	if err != nil {
		t.Fatalf("UpsertWorkforceMember: %v", err)
	}

	profile := store.profiles["profile-human-reviewer"]
	if profile.RuntimeSpec.HasKind() || profile.RuntimeSpec.ConfigRef != "" || len(profile.RuntimeSpec.Config) != 0 {
		t.Fatalf("human profile without runtime config should stay system-managed and unset: %+v", profile.RuntimeSpec)
	}
	if !profile.BaseAgent.InheritsBaseAgent || !profile.BaseAgent.AuditRequired || profile.BaseAgent.LLMAPIKeyRef == "" {
		t.Fatalf("human profile should inherit BaseAgent contract: %+v", profile.BaseAgent)
	}
	if len(profile.SkillRefs) != 1 || profile.SkillRefs[0].SkillID != "skill.review" {
		t.Fatalf("human profile skill bindings should be config-bound: %+v", profile.SkillRefs)
	}
	assertConfiguredToolAndKnowledgeRead(t, profile.ToolRefs, agentcore.ToolRef{ToolID: "tool.approval"})
	if profile.RealName != "Operator User" || profile.Persona.ResponsibilityBoundary != "Review release plans and approve risky changes." {
		t.Fatalf("human identity should preserve viewer-provided text: %+v", profile)
	}
}

func TestCommandServiceKeepsHumanProfileConfiguredBindingsForExplicitRuntime(t *testing.T) {
	ctx := context.Background()
	store := &fakeWorkforceProfileStore{profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{}}
	service := operations.NewCommandService(store)
	orgs := newFakeOrganizationCatalogStore()
	seedOrganizationCatalog(t, ctx, orgs, "orrery", []operations.DepartmentDTO{
		{DepartmentID: "operations", OrganizationID: "orrery", Name: "Operations"},
	}, []operations.PositionDTO{
		{PositionID: "reviewer", DepartmentID: "operations", Name: "Reviewer"},
	})
	service.SetOrganizationCatalogStore(orgs)
	viewer := operations.ViewerRef{ViewerID: "admin", PermissionScope: []string{operations.PermissionWorkforceManage}}

	_, err := service.UpsertWorkforceMember(ctx, viewer, operations.UpsertWorkforceMemberCommand{
		AgentProfileID:   "profile-human-runtime",
		RealName:         "Operator User",
		AgentKind:        "human",
		EmploymentType:   "human",
		OrganizationID:   "orrery",
		DepartmentID:     "operations",
		PositionID:       "reviewer",
		RuntimeKind:      "human",
		RuntimeConfigRef: "runtime-human-inbox",
		SkillRefs:        []operations.WorkforceSkillRefDTO{{SkillID: "skill.review"}},
		ToolRefs:         []operations.WorkforceToolRefDTO{{ToolID: "tool.approval"}},
		Persona:          operations.WorkforcePersonaDTO{ResponsibilityBoundary: "Review release plans and approve risky changes."},
		PersonalMemoryID: "memory-human-runtime",
		Enabled:          true,
	})
	if err != nil {
		t.Fatalf("UpsertWorkforceMember: %v", err)
	}

	profile := store.profiles["profile-human-runtime"]
	if profile.RuntimeSpec.Kind != agentcore.RuntimeKindHuman || profile.RuntimeSpec.ConfigRef != "runtime-human-inbox" {
		t.Fatalf("human runtime profile = %+v", profile.RuntimeSpec)
	}
	if len(profile.SkillRefs) != 1 || profile.SkillRefs[0].SkillID != "skill.review" {
		t.Fatalf("explicit runtime human profile should keep configured skill bindings: %+v", profile.SkillRefs)
	}
	assertConfiguredToolAndKnowledgeRead(t, profile.ToolRefs, agentcore.ToolRef{ToolID: "tool.approval"})
	if !profile.BaseAgent.InheritsBaseAgent || !profile.BaseAgent.AuditRequired {
		t.Fatalf("runtime-backed human profile should still be BaseAgent managed: %+v", profile.BaseAgent)
	}
}

func TestCommandServiceLeavesManagedAgentRuntimeUnsetWhenUnspecified(t *testing.T) {
	ctx := context.Background()
	store := &fakeWorkforceProfileStore{profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{}}
	service := operations.NewCommandService(store)
	orgs := newFakeOrganizationCatalogStore()
	seedOrganizationCatalog(t, ctx, orgs, "orrery", []operations.DepartmentDTO{
		{DepartmentID: "operations", OrganizationID: "orrery", Name: "Operations"},
	}, []operations.PositionDTO{
		{PositionID: "worker", DepartmentID: "operations", Name: "Worker"},
	})
	service.SetOrganizationCatalogStore(orgs)
	viewer := operations.ViewerRef{ViewerID: "admin", PermissionScope: []string{operations.PermissionWorkforceManage}}

	_, err := service.UpsertWorkforceMember(ctx, viewer, operations.UpsertWorkforceMemberCommand{
		AgentProfileID:   "profile-default-runtime",
		RealName:         "Default Runtime Agent",
		AgentKind:        "ai",
		EmploymentType:   "ai_agent",
		OrganizationID:   "orrery",
		DepartmentID:     "operations",
		PositionID:       "worker",
		Persona:          operations.WorkforcePersonaDTO{ResponsibilityBoundary: "Run default managed runtime tasks"},
		PersonalMemoryID: "memory-default-runtime",
		Enabled:          true,
	})
	if err != nil {
		t.Fatalf("UpsertWorkforceMember: %v", err)
	}

	profile := store.profiles["profile-default-runtime"]
	if profile.RuntimeSpec.HasKind() || profile.RuntimeSpec.ConfigRef != "" || len(profile.RuntimeSpec.Config) != 0 {
		t.Fatalf("unspecified runtime should remain system-managed and unset: %+v", profile.RuntimeSpec)
	}
	if !profile.BaseAgent.InheritsBaseAgent || !profile.BaseAgent.AuditRequired {
		t.Fatalf("default runtime profile should be BaseAgent managed: %+v", profile.BaseAgent)
	}
	assertAgentWebResearchCapabilities(t, profile.SkillRefs, profile.ToolRefs)
}

func TestCommandServiceAppliesDefaultLLMProfileWithoutRuntime(t *testing.T) {
	ctx := context.Background()
	store := &fakeWorkforceProfileStore{profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{}}
	service := operations.NewCommandService(store)
	orgs := newFakeOrganizationCatalogStore()
	seedOrganizationCatalog(t, ctx, orgs, "orrery", []operations.DepartmentDTO{
		{DepartmentID: "operations", OrganizationID: "orrery", Name: "Operations"},
	}, []operations.PositionDTO{
		{PositionID: "worker", DepartmentID: "operations", Name: "Worker"},
	})
	service.SetOrganizationCatalogStore(orgs)
	viewer := operations.ViewerRef{ViewerID: "admin", PermissionScope: []string{operations.PermissionWorkforceManage}}

	_, err := service.UpsertWorkforceMember(ctx, viewer, operations.UpsertWorkforceMemberCommand{
		AgentProfileID: "profile-default-llm",
		RealName:       "Default LLM Agent",
		AgentKind:      "ai",
		EmploymentType: "ai_agent",
		OrganizationID: "orrery",
		DepartmentID:   "operations",
		PositionID:     "worker",
		Persona:        operations.WorkforcePersonaDTO{ResponsibilityBoundary: "Run default managed LLM tasks"},
		Enabled:        true,
		BaseAgentLLMProfile: agentcore.BaseAgentLLMProfileSpec{
			ProfileID:     "llm-openai",
			DisplayName:   "OpenAI Primary",
			Provider:      "openai",
			BaseURL:       "https://api.openai.com/v1",
			APIKeyRef:     "secret://llm/openai",
			Model:         "gpt-5",
			Temperature:   0.2,
			ContextLength: 128000,
		},
	})
	if err != nil {
		t.Fatalf("UpsertWorkforceMember: %v", err)
	}

	profile := store.profiles["profile-default-llm"]
	if profile.RuntimeSpec.HasKind() || profile.RuntimeSpec.ConfigRef != "" || len(profile.RuntimeSpec.Config) != 0 {
		t.Fatalf("default LLM should not invent runtime config: %+v", profile.RuntimeSpec)
	}
	if profile.BaseAgent.LLMAPIKeyRef != "secret://llm/openai" {
		t.Fatalf("base agent llm api key ref = %q", profile.BaseAgent.LLMAPIKeyRef)
	}
	var llm agentcore.BaseAgentLLMProfileSpec
	if err := json.Unmarshal(profile.BaseAgent.Extensions[agentcore.BaseAgentLLMProfileExtensionKey], &llm); err != nil {
		t.Fatalf("decode base agent llm profile extension: %v", err)
	}
	if llm.ProfileID != "llm-openai" || llm.Provider != "openai" || llm.BaseURL != "https://api.openai.com/v1" || llm.Model != "gpt-5" || llm.ContextLength != 128000 {
		t.Fatalf("base agent llm profile extension = %+v", llm)
	}
}
