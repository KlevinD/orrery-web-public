package operations

import (
	"strings"

	"orrery_v2/internal/app/permissionscope"
)

const (
	RoleScopeSelf               = "self"
	RoleScopeDescendants        = "descendants"
	RoleScopeSelfAndDescendants = "self_and_descendants"
	RoleScopePlatform           = "platform"

	RoleSourceDirect    = "direct"
	RoleSourceInherited = "inherited"
	RoleSourceSystem    = "system"

	RoleIDPlatformAdmin   = "platform.admin"
	RoleIDProjectLead     = "pm"
	RoleIDBasicUser       = "basic.user"
	RoleIDOntologySteward = "ontology.steward"
)

type RoleDefinitionDTO struct {
	RoleID               string   `json:"role_id"`
	Name                 string   `json:"name"`
	Description          string   `json:"description,omitempty"`
	Category             string   `json:"category"`
	Status               string   `json:"status"`
	AssignableActorTypes []string `json:"assignable_actor_types"`
	BuiltIn              bool     `json:"built_in"`
	PermissionRefs       []string `json:"permission_refs,omitempty"`
	ApprovalCapability   bool     `json:"approval_capability"`
}

type RoleDefinitionPageDTO struct {
	Items []RoleDefinitionDTO `json:"items"`
	Page  PageDTO             `json:"page"`
}

type RoleAssignmentQuery struct {
	ActorType        string
	ActorID          string
	RoleID           string
	OrganizationCode string
	EffectiveOnly    bool
	Limit            int
	Offset           int
}

type RoleAssignmentPageDTO struct {
	Items []RoleAssignmentDTO `json:"items"`
	Page  PageDTO             `json:"page"`
}

type PutRoleAssignmentsCommand struct {
	Assignments []RoleAssignmentDTO `json:"assignments"`
}

type PutRoleDefinitionsCommand struct {
	Roles []RoleDefinitionDTO `json:"roles"`
}

type PermissionRolePageQuery struct {
	AfterRoleID string
	Limit       int
}

type PermissionRolePageDTO struct {
	Items    []RoleDefinitionDTO `json:"items"`
	HasMore  bool                `json:"has_more"`
	Revision int64               `json:"revision"`
}

type ApplyPermissionRoleDefinitionsCommand struct {
	ExpectedRevision int64               `json:"expected_revision"`
	Roles            []RoleDefinitionDTO `json:"roles"`
}

type ApplyPermissionRoleDefinitionsResult struct {
	Items    []RoleDefinitionDTO `json:"items"`
	Revision int64               `json:"revision"`
}

type PermissionEffectiveGrantQuery struct {
	AssignmentID string
	ScopeOffset  int
	Limit        int
}

type PermissionEffectiveGrantDTO struct {
	PrincipalKind string `json:"principal_kind"`
	PrincipalID   string `json:"principal_id"`
	Scope         string `json:"scope"`
	Source        string `json:"source"`
	SourceID      string `json:"source_id"`
}

type PermissionEffectiveGrantPageDTO struct {
	Items            []PermissionEffectiveGrantDTO `json:"items"`
	NextAssignmentID string                        `json:"-"`
	NextScopeOffset  int                           `json:"-"`
	HasMore          bool                          `json:"-"`
}

type UpdateOrganizationOwnerCommand struct {
	OrganizationNodeID string `json:"organization_node_id"`
	OwnerProfileID     string `json:"owner_profile_id,omitempty"`
	OwnerEmployeeNo    string `json:"owner_employee_no,omitempty"`
	OwnerName          string `json:"owner_name,omitempty"`
}

func BuiltInRoleDefinitions() []RoleDefinitionDTO {
	actorTypes := []string{"human", "agent"}
	return []RoleDefinitionDTO{
		{RoleID: "org.owner", Name: "组织负责人", Category: "organization", Status: "active", AssignableActorTypes: actorTypes, BuiltIn: true, PermissionRefs: []string{"org.read", "org.manage", "roles.read", "approvals.decide"}, ApprovalCapability: true},
		{RoleID: "org.admin", Name: "组织管理员", Category: "organization", Status: "active", AssignableActorTypes: actorTypes, BuiltIn: true, PermissionRefs: []string{"org.read", "org.manage", "roles.read", "roles.manage"}, ApprovalCapability: true},
		{RoleID: "org.manager", Name: "组织经理", Category: "organization", Status: "active", AssignableActorTypes: actorTypes, BuiltIn: true, PermissionRefs: []string{"org.read", "workforce.read", "missions.assign"}, ApprovalCapability: true},
		{RoleID: "member", Name: "成员", Category: "organization", Status: "active", AssignableActorTypes: actorTypes, BuiltIn: true, PermissionRefs: []string{"org.read"}, ApprovalCapability: false},
		{RoleID: "agent.operator", Name: "Agent 调度员", Category: "agent", Status: "active", AssignableActorTypes: []string{"human", "agent"}, BuiltIn: true, PermissionRefs: []string{"agent.run", "missions.assign"}, ApprovalCapability: false},
		{RoleID: "approval.reviewer", Name: "审批员", Category: "platform", Status: "active", AssignableActorTypes: actorTypes, BuiltIn: true, PermissionRefs: []string{"approvals.read", "approvals.decide"}, ApprovalCapability: true},
		{RoleID: "resource.owner", Name: "资源负责人", Category: "resource", Status: "active", AssignableActorTypes: actorTypes, BuiltIn: true, PermissionRefs: []string{"resource.read", "resource.manage"}, ApprovalCapability: true},
		{RoleID: "audit.viewer", Name: "审计查看员", Category: "platform", Status: "active", AssignableActorTypes: []string{"human", "agent"}, BuiltIn: true, PermissionRefs: []string{"audit.read"}, ApprovalCapability: false},
		{RoleID: "security.admin", Name: "安全管理员", Category: "platform", Status: "active", AssignableActorTypes: []string{"human"}, BuiltIn: true, PermissionRefs: []string{"permissions.read", "permissions.manage", "roles.manage", "audit.read", "audit.manage"}, ApprovalCapability: true},
		{
			RoleID:               RoleIDOntologySteward,
			Name:                 "本体管理员",
			Description:          "Governed ontology maintenance for the Orrery enterprise graph",
			Category:             "platform",
			Status:               "active",
			AssignableActorTypes: []string{"agent"},
			BuiltIn:              true,
			PermissionRefs: []string{
				"ontology.read", "ontology.manage", "enterprise_graph.read",
				"knowledge.read", "knowledge.write", "memory.read", "scheduler.read", "approval.request",
				"audit.read", "operations.read", "operations.manage", "org.read", "org.manage",
			},
			ApprovalCapability: false,
		},
		{
			RoleID:               RoleIDBasicUser,
			Name:                 "基础用户",
			Description:          "Default least-privilege access for federated users",
			Category:             "platform",
			Status:               "active",
			AssignableActorTypes: []string{"human"},
			BuiltIn:              true,
			PermissionRefs: []string{
				"mission.read", "discussion.read", "discussion.write",
				"approvals.read", "scheduler.read", "todo.read", "todo.write", "idea.read", "idea.write",
				"resource.read", "knowledge.read", "web.read", "memory.read", "enterprise_graph.read", "ontology.read", "skill.read",
				"org.read", "workforce.read", "roles.read",
			},
			ApprovalCapability: false,
		},
		{
			RoleID:               RoleIDPlatformAdmin,
			Name:                 "平台管理员",
			Description:          "Platform Administrator",
			Category:             "platform",
			Status:               "active",
			AssignableActorTypes: []string{"human"},
			BuiltIn:              true,
			PermissionRefs: []string{
				"mission.read", "mission.write", "discussion.read", "discussion.write",
				"approvals.read", "approvals.decide", "scheduler.read", "scheduler.write", "scheduler.run",
				"todo.read", "todo.write", "idea.read", "idea.write",
				"resource.read", "resource.manage", "tool.call",
				"knowledge.read", "knowledge.write", "knowledge.manage", "web.read", "email.send",
				"enterprise_graph.read", "ontology.read", "ontology.manage", "skill.read", "personal_assistant.shell",
				"memory.read", "memory.write", "memory.review", "memory.world.manage", "memory.distill", "memory.audit.read",
				"operations.read", "operations.manage", "org.read", "org.manage",
				"workforce.read", "workforce.manage", "roles.read", "roles.manage",
				"permissions.read", "permissions.manage", "audit.read", "audit.manage", "settings.read", "settings.manage", "gateway.delivery.send", "agent.run", "agent.trace.read", "agent.trace.manage",
			},
			ApprovalCapability: true,
		},
		{
			RoleID:               RoleIDProjectLead,
			Name:                 "项目负责人",
			Description:          "Project Lead",
			Category:             "platform",
			Status:               "active",
			AssignableActorTypes: []string{"human"},
			BuiltIn:              true,
			PermissionRefs: []string{
				"mission.read", "mission.write", "discussion.read", "discussion.write",
				"approvals.read", "scheduler.read", "todo.read", "todo.write", "idea.read", "idea.write",
				"resource.read", "knowledge.read", "web.read", "memory.read", "enterprise_graph.read", "ontology.read", "skill.read",
				"org.read", "workforce.read", "roles.read",
			},
			ApprovalCapability: false,
		},
	}
}

func UserAssignableRoleDefinitions(roles []RoleDefinitionDTO) []RoleDefinitionDTO {
	out := make([]RoleDefinitionDTO, 0, len(roles))
	for _, role := range roles {
		if !role.BuiltIn || role.RoleID == RoleIDPlatformAdmin || role.RoleID == RoleIDProjectLead {
			out = append(out, role)
		}
	}
	return out
}

func builtInRoleDefinition(roleID string) (RoleDefinitionDTO, bool) {
	roleID = strings.ToLower(strings.TrimSpace(roleID))
	for _, role := range BuiltInRoleDefinitions() {
		if role.RoleID == roleID {
			return role, true
		}
	}
	return RoleDefinitionDTO{}, false
}

func roleRequiresPlatformAssignment(role RoleDefinitionDTO) bool {
	return strings.EqualFold(strings.TrimSpace(role.Category), "platform")
}

func roleScopeIsPlatform(scopeMode string) bool {
	return strings.EqualFold(strings.TrimSpace(scopeMode), RoleScopePlatform)
}

func canonicalPermissionScope(scope string) string {
	return permissionscope.Normalize(scope)
}

func canonicalPermissionScopes(scopes []string) []string {
	out := make([]string, 0, len(scopes))
	seen := map[string]struct{}{}
	for _, scope := range scopes {
		scope = canonicalPermissionScope(scope)
		if scope == "" {
			continue
		}
		if _, ok := seen[scope]; ok {
			continue
		}
		seen[scope] = struct{}{}
		out = append(out, scope)
	}
	return out
}
