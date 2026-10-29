package main

import (
	"context"
	"strings"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/operations"
	"orrery_v2/internal/platform/config"
	toolstd "orrery_v2/internal/resource/tool/std"
)

const (
	defaultOntologyStewardAgentID      = "ontology-steward-agent"
	defaultOntologyStewardPositionID   = "position-ontology-steward-agent"
	defaultOntologyStewardManagerID    = "H000001"
	defaultOntologyStewardRoleID       = operations.RoleIDOntologySteward
	defaultOntologyStewardAssignmentID = "workforce:ontology-steward-agent:ontology.steward"
)

const ontologyInferenceModelWorkflowInstruction = "使用 ontology_infer 获取推理候选后，由你根据证据直接决定批准或拒绝，并用 ontology_inference_workflow 记录理由。对于关系类型已允许执行的已有对象关系，批准后自行生成并检查计划，再执行写入并保存回执，无需请求人工审核或人工确认。证据不足或冲突时自行拒绝；证据或权限发生变化时重新评估。"

const ontologyGroundedContextInstruction = "需要判断当前主张或复用旧上下文时，使用 ontology_grounded_context 读取有权限的断言、规则证明和精确支持版本。由你决定材料表达了哪项主张，并显式声明 required_claims 或 required_supports；每次复用重新读取，只有 eligible=true 才能依赖这些声明。独立支持可以维持同一主张，旧支持版本不能由其他来源替代。supported_in_snapshot 只说明本次有结构有效的支持，证据可靠性和规则含义仍由你判断；争议与未知材料不能当作确定前提。这个只读工具不创建候选、决策或写入回执。"

const ontologyContinuousInferenceInstruction = "需要持续维护的推理，使用 ontology_inference_rules 发布有版本的规则集和固定 root_id；后台会持久检测来源与权限变化并调用模型直接判断、执行，无需人工审核。使用 list 查询规则，使用 pause 和 expected_version 暂停规则。已写入的派生关系发生证据变化时，重新判断后可批准修订；最后一条有效支持消失时用 retract 判断、预览和执行撤回，仅操作自己拥有的派生关系并保留历次回执。"

const ontologyDocumentInferenceInstruction = "??????????????????????? ontology_document_inference publish ????? knowledge_document ? root_id ???????????????????????????????????????????????????? status ????????????????? pause ? expected_version ???????????????????????????????"

func ensureDefaultOntologyStewardAgent(ctx context.Context, cfg config.Config, profiles *agentcore.PlatformProfileRepository) error {
	if profiles == nil {
		return nil
	}
	seeded := defaultOntologyStewardAgentProfile(cfg)
	profile, found, err := getAgentProfile(ctx, profiles, seeded.ID)
	if err != nil {
		return err
	}
	if found {
		profile = mergeDefaultAgentProfile(profile, seeded)
	} else {
		profile = seeded
	}
	if !strings.Contains(profile.BaseAgent.SystemPrompt, "ontology_inference_workflow") {
		profile.BaseAgent.SystemPrompt = strings.TrimSpace(profile.BaseAgent.SystemPrompt) + "\n\n" + ontologyInferenceModelWorkflowInstruction
	}
	if !strings.Contains(profile.BaseAgent.SystemPrompt, "ontology_inference_rules") {
		profile.BaseAgent.SystemPrompt += "\n\n" + ontologyContinuousInferenceInstruction
	}
	if !strings.Contains(profile.BaseAgent.SystemPrompt, "ontology_grounded_context") {
		profile.BaseAgent.SystemPrompt += "\n\n" + ontologyGroundedContextInstruction
	}
	if !strings.Contains(profile.BaseAgent.SystemPrompt, "ontology_document_inference") {
		profile.BaseAgent.SystemPrompt += "\n\n" + ontologyDocumentInferenceInstruction
	}
	profile.PersonalAssistantOwnerID = ""
	profile.PersonalAssistantTenantID = ""
	profile.Placement = seeded.Placement
	profile.OrgIdentity.OrganizationRef = seeded.OrgIdentity.OrganizationRef
	profile.OrgIdentity.DepartmentRef = seeded.OrgIdentity.DepartmentRef
	profile.OrgIdentity.PositionRef = seeded.OrgIdentity.PositionRef
	profile.OrgIdentity.ManagerAgentRef = seeded.OrgIdentity.ManagerAgentRef
	profile.OrgIdentity.EmploymentType = seeded.OrgIdentity.EmploymentType
	profile.OrgIdentity.SupervisionPolicy = seeded.OrgIdentity.SupervisionPolicy
	profile.OrgIdentity.AuthorityScope = seeded.OrgIdentity.AuthorityScope
	profile.OrgIdentity.RoleRefs = ensureAgentRoleRef(profile.OrgIdentity.RoleRefs, defaultOntologyStewardRoleID)
	profile.ToolRefs = mergeDefaultAgentToolRefs(profile.ToolRefs, seeded.ToolRefs)
	profile.Enabled = true
	if err := profiles.SaveProfile(ctx, profile); err != nil {
		return err
	}
	return saveDefaultAgentAvailability(ctx, profiles, profile)
}

func defaultOntologyStewardAgentProfile(cfg config.Config) agentcore.AgentProfile {
	base := defaultBaseAgentSpec("大头", "本体管理员")
	base.SystemPrompt = strings.TrimSpace(`你是 Orrery 的本体管理员“大头”，直属 ding。你的职责是让本体持续准确表达实际业务，而不是只维护字段和技术结构。

处理任何问题时，先读取企业图谱、相关业务知识和现有映射，确认业务对象、责任人、流程、权限及来源系统的真实含义。新系统接入时，先识别其业务边界和权威数据源，再通过受治理的本体提案、审查、变更计划和应用流程增加对象类型、关系、动作或映射规则，并在知识库留下证据和决策记录。

发现缺失、漂移、悬空引用或投影异常后，应主动修复可验证且可回滚的问题。不得凭空补全业务含义，不得绕过租户、数据策略、版本、审计或审批边界。高风险、不可逆、跨组织或证据冲突的变更必须请求 ding 审批；无法验证时明确记录为待确认。`)
	base.SystemPrompt += "\n\n" + ontologyInferenceModelWorkflowInstruction
	base.SystemPrompt += "\n\n" + ontologyContinuousInferenceInstruction
	base.SystemPrompt += "\n\n" + ontologyGroundedContextInstruction
	base.SystemPrompt += "\n\n" + ontologyDocumentInferenceInstruction
	return agentcore.AgentProfile{
		ID:          defaultOntologyStewardAgentID,
		RealName:    "大头",
		DisplayName: "大头",
		RoleName:    "本体管理员",
		AgentKind:   agentcore.AgentKindAI,
		Placement:   agentcore.TalentPlacement{Status: agentcore.TalentPlacementInternal},
		OrgIdentity: agentcore.AgentOrgIdentity{
			OrganizationRef:   agentcore.OrganizationRef{ID: defaultBusinessOrganizationID},
			DepartmentRef:     agentcore.DepartmentRef{ID: defaultSystemSupportDepartmentID},
			PositionRef:       agentcore.PositionRef{ID: defaultOntologyStewardPositionID},
			RoleRefs:          []agentcore.RoleRef{{ID: defaultOntologyStewardRoleID}},
			ManagerAgentRef:   agentcore.AgentProfileRef{ID: defaultOntologyStewardManagerID},
			EmploymentType:    agentcore.EmploymentTypeSystem,
			SupervisionPolicy: agentcore.SupervisionPolicyManaged,
			AuthorityScope:    "tenant_ontology_governance",
		},
		Persona: agentcore.AgentPersona{
			Backstory:              "Orrery 的长期本体管家，负责把系统数据结构与 Minvia 的真实业务责任保持一致。",
			ResponsibilityBoundary: "理解业务后维护全部授权本体、映射和投影；主动修复有证据且可回滚的问题，重大变更提交 ding 审批。",
			BehaviorBoundary:       "Evidence first. Use governed proposals and versioned promotion plans; never bypass tenant, data-policy, approval, or audit boundaries.",
		},
		BaseAgent: base,
		PersonalMemoryProfileRef: agentcore.MemoryProfileRef{
			MemoryProfileID: "memory-" + defaultOntologyStewardAgentID,
			OwnerAgentID:    agentcore.AgentID(defaultOntologyStewardAgentID),
			MemoryKind:      agentcore.MemoryKindPersonal,
			AccessScope:     "system",
		},
		CollectiveMemoryScopeRefs: []agentcore.MemoryProfileRef{},
		SkillRefs:                 []agentcore.SkillRef{},
		ToolRefs: []agentcore.ToolRef{
			{ToolID: toolstd.EnterpriseGraphBuildContextBundle, PermissionScope: toolstd.PermissionEnterpriseGraphRead},
			{ToolID: toolstd.EnterpriseGraphSearchObjects, PermissionScope: toolstd.PermissionEnterpriseGraphRead},
			{ToolID: toolstd.KnowledgeSearch, PermissionScope: toolstd.PermissionKnowledgeRead},
			{ToolID: toolstd.KnowledgeAdd, PermissionScope: toolstd.PermissionKnowledgeWrite},
			{ToolID: toolstd.MemorySearch, PermissionScope: toolstd.PermissionMemoryRead},
			{ToolID: toolstd.RequestBusinessApproval, PermissionScope: toolstd.PermissionApprovalRequest},
			{ToolID: toolstd.ScheduledTaskList, PermissionScope: toolstd.PermissionSchedulerRead},
			{ToolID: toolstd.OntologyProposalCreate, PermissionScope: toolstd.PermissionOntologyManage},
			{ToolID: toolstd.OntologyInfer, PermissionScope: toolstd.PermissionOntologyManage},
			{ToolID: toolstd.OntologyGroundedContext, PermissionScope: toolstd.PermissionOntologyRead},
			{ToolID: toolstd.OntologyInferenceWorkflow, PermissionScope: toolstd.PermissionOntologyManage},
			{ToolID: toolstd.OntologyInferenceRules, PermissionScope: toolstd.PermissionOntologyManage},
			{ToolID: toolstd.OntologyDocumentInference, PermissionScope: toolstd.PermissionOntologyManage},
			{ToolID: toolstd.OntologyProposalDecide, PermissionScope: toolstd.PermissionOntologyManage},
			{ToolID: toolstd.OntologyPromotionPlanCreate, PermissionScope: toolstd.PermissionOntologyManage},
			{ToolID: toolstd.OntologyPromotionApply, PermissionScope: toolstd.PermissionOntologyManage},
			{ToolID: toolstd.OntologyProjectionRefreshRetry, PermissionScope: toolstd.PermissionOntologyManage},
		},
		RuntimeSpec: defaultAgentRuntimeSpec(cfg, defaultOntologyStewardAgentID),
		Enabled:     true,
	}
}

func ensureAgentRoleRef(refs []agentcore.RoleRef, roleID string) []agentcore.RoleRef {
	for _, ref := range refs {
		if strings.EqualFold(strings.TrimSpace(string(ref.ID)), strings.TrimSpace(roleID)) {
			return refs
		}
	}
	return append(refs, agentcore.RoleRef{ID: agentcore.RoleID(roleID)})
}

func ensureDefaultOntologyStewardRoleAssignment(ctx context.Context, roles *operations.RoleStore) error {
	if roles == nil {
		return nil
	}
	return roles.SaveRoleAssignment(ctx, operations.RoleAssignmentDTO{
		AssignmentID: defaultOntologyStewardAssignmentID,
		Actor:        operations.ActorRefDTO{ActorType: "agent", ActorID: defaultOntologyStewardAgentID},
		RoleID:       defaultOntologyStewardRoleID,
		ScopeMode:    operations.RoleScopePlatform,
		Source:       operations.RoleSourceSystem,
		AssignedBy:   "system:ontology-steward-bootstrap",
	})
}
