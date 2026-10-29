package main

import (
	"context"
	"strings"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/operations"
	"orrery_v2/internal/platform/config"
)

const (
	defaultLegalOrchestratorAgentID agentcore.AgentProfileID = "legal-orchestrator-agent"
	defaultLegalKnowledgeAgentID    agentcore.AgentProfileID = "legal-knowledge-agent"
	defaultLegalContractAgentID     agentcore.AgentProfileID = "legal-contract-review-agent"
	defaultLegalComplianceAgentID   agentcore.AgentProfileID = "legal-compliance-risk-agent"
)

type defaultLegalAgentSeed struct {
	id             agentcore.AgentProfileID
	name           string
	roleName       string
	responsibility string
	prompt         string
}

func ensureDefaultLegalAgents(ctx context.Context, cfg config.Config, profiles *agentcore.PlatformProfileRepository) error {
	if profiles == nil {
		return nil
	}
	defaults := defaultLegalAgentProfilesFromConfig(cfg)
	for _, profile := range defaults {
		seeded := profile
		existing, found, err := getAgentProfile(ctx, profiles, profile.ID)
		if err != nil {
			return err
		}
		if !found {
			deleted, err := profiles.IsProfileDeleted(ctx, profile.ID)
			if err != nil {
				return err
			}
			if deleted {
				continue
			}
		}
		if found {
			profile = mergeDefaultAgentProfile(existing, profile)
			profile.OrgIdentity.RoleRefs = mergeRoleRefs(profile.OrgIdentity.RoleRefs, seeded.OrgIdentity.RoleRefs)
		}
		if errs := agentcore.ValidateAgentProfile(profile); len(errs) > 0 {
			return operations.ErrInvalidRequest
		}
		if err := profiles.SaveProfile(ctx, profile); err != nil {
			return err
		}
		if err := saveDefaultAgentAvailability(ctx, profiles, profile); err != nil {
			return err
		}
	}
	return nil
}

func defaultLegalAgentProfiles(runtime agentcore.RuntimeSpec) ([]agentcore.AgentProfile, error) {
	return defaultLegalAgentProfilesWithRuntime(func(string) agentcore.RuntimeSpec { return runtime }), nil
}

func defaultLegalAgentProfilesFromConfig(cfg config.Config) []agentcore.AgentProfile {
	return defaultLegalAgentProfilesWithRuntime(func(agentID string) agentcore.RuntimeSpec {
		return defaultAgentRuntimeSpec(cfg, agentID)
	})
}

func defaultLegalAgentProfilesWithRuntime(runtimeFor func(string) agentcore.RuntimeSpec) []agentcore.AgentProfile {
	seeds := defaultLegalAgentSeeds()
	profiles := make([]agentcore.AgentProfile, 0, len(seeds))
	for _, seed := range seeds {
		agentID := seed.id.String()
		runtime := agentcore.RuntimeSpec{}
		if runtimeFor != nil {
			runtime = runtimeFor(agentID)
		}
		profiles = append(profiles, defaultLegalAgentProfile(seed, runtime))
	}
	return profiles
}

func defaultLegalAgentSeeds() []defaultLegalAgentSeed {
	return []defaultLegalAgentSeed{
		{
			id:             defaultLegalOrchestratorAgentID,
			name:           "法务统筹",
			roleName:       "Legal Orchestrator Agent",
			responsibility: "负责识别法务任务类型、选择合适的 Legal MCP 能力、编排知识检索、合同审查、合规风险和人工复核路径。",
			prompt:         "你是 Orrery Legal Orchestrator Agent。你的职责是把用户法务任务转换为可审计的 Legal MCP 工具调用计划，并在必要时协调知识、合同审查、合规风险和人工复核 Agent。你不是法务 SaaS UI，也不直接执行外部审批或外部系统操作。所有结论必须引用 Legal MCP 返回的 evidence、resource ref、audit ref 或 human review gate。",
		},
		{
			id:             defaultLegalKnowledgeAgentID,
			name:           "法知检索",
			roleName:       "Legal Knowledge Agent",
			responsibility: "负责公司合同、政策、法规、PageIndex/RAG 证据和 Legal MCP 知识源状态的检索、入库请求和证据追踪。",
			prompt:         "你是 Orrery Legal Knowledge Agent。优先使用 Legal MCP 的 enterprise search、source manage、policy summary 和 pipeline status 能力检索公司法务知识。回答必须区分 DB/RAG 命中、PageIndex 引用、缺失资料和需要人工补充的证据。",
		},
		{
			id:             defaultLegalContractAgentID,
			name:           "合约审校",
			roleName:       "Contract Review Agent",
			responsibility: "负责合同审查、条款风险、义务提取、对比审阅、审查就绪度和人工复核门禁。",
			prompt:         "你是 Orrery Contract Review Agent。使用 Legal MCP 的 contract review、enterprise search、source manage、human review 和 audit trail 能力完成证据优先的合同审查。不得给出无证据的最终法律意见；高风险条款必须进入人工复核门禁。",
		},
		{
			id:             defaultLegalComplianceAgentID,
			name:           "合规风控",
			roleName:       "Compliance Risk Agent",
			responsibility: "负责合规扫描、政策风险、风险登记、缓解建议、政策情报状态和人工复核门禁。",
			prompt:         "你是 Orrery Compliance Risk Agent。使用 Legal MCP 的 compliance risk、policy intelligence、pipeline status、enterprise search、human review 和 audit trail 能力输出合规风险判断。所有风险结论必须附 evidence 或说明证据缺口。",
		},
	}
}

func defaultLegalAgentProfile(seed defaultLegalAgentSeed, runtime agentcore.RuntimeSpec) agentcore.AgentProfile {
	id := seed.id.String()
	return agentcore.AgentProfile{
		ID:          seed.id,
		RealName:    seed.name,
		DisplayName: seed.name,
		RoleName:    seed.roleName,
		AgentKind:   agentcore.AgentKindAI,
		OrgIdentity: agentcore.AgentOrgIdentity{
			OrganizationRef:   agentcore.OrganizationRef{ID: agentcore.OrganizationID(defaultCoordinatorOrganizationID)},
			DepartmentRef:     agentcore.DepartmentRef{ID: agentcore.DepartmentID(defaultCoordinatorDepartmentID)},
			PositionRef:       agentcore.PositionRef{ID: agentcore.PositionID(defaultCoordinatorPositionID)},
			RoleRefs:          []agentcore.RoleRef{{ID: agentcore.RoleID(strings.TrimSuffix(id, "-agent"))}, {ID: "worker"}},
			EmploymentType:    agentcore.EmploymentTypeSystem,
			SupervisionPolicy: agentcore.SupervisionPolicyRoot,
			AuthorityScope:    "legal_agents_mcp_tooling",
		},
		Persona: agentcore.AgentPersona{
			Backstory:              seed.name + "是 Orrery 系统托管的法务 Agent。",
			ResponsibilityBoundary: seed.responsibility,
			BehaviorBoundary:       "Only operate through Orrery Mission records and the bound Legal MCP server. Do not present unreviewed legal analysis as final legal advice, and do not execute external approval or connector actions.",
		},
		BaseAgent: defaultLegalBaseAgentSpec(seed),
		PersonalMemoryProfileRef: agentcore.MemoryProfileRef{
			MemoryProfileID: agentcore.MemoryProfileID("memory-" + id),
			OwnerAgentID:    agentcore.AgentID(id),
			MemoryKind:      agentcore.MemoryKindPersonal,
			AccessScope:     "system",
		},
		CollectiveMemoryScopeRefs: []agentcore.MemoryProfileRef{},
		SkillRefs:                 []agentcore.SkillRef{},
		ToolRefs:                  []agentcore.ToolRef{defaultLegalMCPServerToolRef()},
		RuntimeSpec:               runtime,
		QAEnabled:                 true,
		Enabled:                   true,
	}
}

func defaultLegalBaseAgentSpec(seed defaultLegalAgentSeed) agentcore.BaseAgentSpec {
	spec := defaultBaseAgentSpec(seed.name, seed.roleName)
	spec.SystemPrompt = strings.TrimSpace(seed.prompt)
	return spec
}

func defaultLegalMCPServerToolRef() agentcore.ToolRef {
	return agentcore.ToolRef{
		ToolID:          defaultLegalMCPServerToolID,
		Alias:           "ieado_legal",
		PermissionScope: defaultLegalMCPPermissionScope,
		BindingPolicy:   "required",
	}
}
