package main

import (
	"strings"

	agentcore "orrery_v2/internal/agent/core"
)

func defaultFreelanceAgentProfiles(runtime agentcore.RuntimeSpec) ([]agentcore.AgentProfile, error) {
	_ = runtime
	return []agentcore.AgentProfile{}, nil
}

func defaultFreelanceAgentProfile(runtime agentcore.RuntimeSpec, template defaultAgentTemplate) agentcore.AgentProfile {
	id := strings.TrimSpace(template.Slug)
	name := defaultAgentChineseName(id, strings.TrimSpace(template.Name))
	roleName := strings.TrimSpace(template.WorkerConfig.Role)
	if roleName == "" {
		roleName = strings.TrimSpace(template.Name)
	}
	roleID := strings.TrimSpace(template.RoleKind)
	if roleID == "" {
		roleID = strings.TrimSpace(template.Slug)
	}
	backstory := strings.TrimSpace(template.Instructions)
	if description := strings.TrimSpace(template.Description); description != "" {
		backstory = name + " / " + strings.TrimSpace(template.Name) + ": " + description + "\n\n" + backstory
	}
	return agentcore.AgentProfile{
		ID:          agentcore.AgentProfileID(id),
		RealName:    name,
		DisplayName: name,
		RoleName:    roleName,
		AgentKind:   agentcore.AgentKindAI,
		OrgIdentity: agentcore.AgentOrgIdentity{
			OrganizationRef:   agentcore.OrganizationRef{ID: "org-agent-talent-market"},
			DepartmentRef:     agentcore.DepartmentRef{ID: "dept-freelance-agents"},
			PositionRef:       agentcore.PositionRef{ID: "position-freelance-agent"},
			RoleRefs:          []agentcore.RoleRef{{ID: agentcore.RoleID(roleID)}},
			EmploymentType:    agentcore.EmploymentTypeAIAgent,
			SupervisionPolicy: agentcore.SupervisionPolicySupervised,
			AuthorityScope:    "freelance_agent",
		},
		Persona: agentcore.AgentPersona{
			Backstory:              backstory,
			ResponsibilityBoundary: defaultAgentChineseResponsibility(id, roleName),
			BehaviorBoundary:       "Join Orrery Mission teams only through staffing, delegation, permission, and review records.",
		},
		BaseAgent: defaultBaseAgentSpec(name, roleName),
		PersonalMemoryProfileRef: agentcore.MemoryProfileRef{
			MemoryProfileID: agentcore.MemoryProfileID("memory-" + id),
			OwnerAgentID:    agentcore.AgentID(id),
			MemoryKind:      agentcore.MemoryKindPersonal,
			AccessScope:     "freelance",
		},
		CollectiveMemoryScopeRefs: []agentcore.MemoryProfileRef{},
		SkillRefs:                 defaultTemplateSkillRefs(template),
		ToolRefs:                  []agentcore.ToolRef{},
		RuntimeSpec:               runtime,
		QAEnabled:                 template.WorkerConfig.QA.Enabled,
		Enabled:                   true,
	}
}
