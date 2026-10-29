package operations

import (
	"encoding/json"
	"time"
)

type WorkforceQuery struct {
	OrganizationID string
	DepartmentID   string
	EnabledOnly    bool
	IncludeDetails bool
	Limit          int
	Offset         int
}

type WorkforcePageDTO struct {
	Members []WorkforceMemberDTO `json:"members"`
	Page    PageDTO              `json:"page"`
}

type PageDTO struct {
	Limit    int  `json:"limit,omitempty"`
	Offset   int  `json:"offset,omitempty"`
	Total    int  `json:"total"`
	Returned int  `json:"returned"`
	HasMore  bool `json:"has_more"`
}

type WorkforceMemberDTO struct {
	AgentID                    string                         `json:"agent_id"`
	AgentProfileID             string                         `json:"agent_profile_id"`
	PersonalAssistant          bool                           `json:"personal_assistant,omitempty"`
	PersonalAssistantOwnerID   string                         `json:"personal_assistant_owner_id,omitempty"`
	AgentInstanceID            string                         `json:"agent_instance_id,omitempty"`
	RealName                   string                         `json:"real_name"`
	DisplayName                string                         `json:"display_name"`
	Email                      string                         `json:"email,omitempty"`
	PersonSource               WorkforcePersonSourceFactsDTO  `json:"person_source,omitempty"`
	AgentKind                  string                         `json:"agent_kind"`
	UserAccount                WorkforceUserAccountDTO        `json:"user_account,omitempty"`
	AccountProjection          WorkforceAccountProjectionDTO  `json:"account_projection,omitempty"`
	EmploymentType             string                         `json:"employment_type"`
	OrganizationID             string                         `json:"organization_id"`
	DepartmentID               string                         `json:"department_id"`
	PositionID                 string                         `json:"position_id"`
	DepartmentDisplayName      string                         `json:"department_display_name,omitempty"`
	PositionDisplayName        string                         `json:"position_display_name,omitempty"`
	RoleName                   string                         `json:"role_name,omitempty"`
	AvailabilityStatus         string                         `json:"availability_status,omitempty"`
	CapabilityTags             []string                       `json:"capability_tags,omitempty"`
	RoleIDs                    []string                       `json:"role_ids"`
	FunctionalRoleIDs          []string                       `json:"functional_role_ids,omitempty"`
	ManagerAgentID             string                         `json:"manager_agent_id,omitempty"`
	ReportingLineID            string                         `json:"reporting_line_id,omitempty"`
	AuthorityScope             string                         `json:"authority_scope,omitempty"`
	SupervisionPolicy          string                         `json:"supervision_policy,omitempty"`
	WorkspaceRoot              string                         `json:"workspace_root,omitempty"`
	RuntimeKind                string                         `json:"runtime_kind,omitempty"`
	RuntimeConfigRef           string                         `json:"runtime_config_ref,omitempty"`
	RuntimeConfig              json.RawMessage                `json:"runtime_config,omitempty"`
	BaseAgent                  WorkforceBaseAgentDTO          `json:"base_agent,omitempty"`
	Persona                    WorkforcePersonaDTO            `json:"persona,omitempty"`
	KnowledgeBaseID            string                         `json:"knowledge_base_id,omitempty"`
	PersonalMemoryRef          WorkforceMemoryProfileRefDTO   `json:"personal_memory_ref,omitempty"`
	CollectiveMemoryRefs       []WorkforceMemoryProfileRefDTO `json:"collective_memory_refs,omitempty"`
	SkillRefs                  []WorkforceSkillRefDTO         `json:"skill_refs,omitempty"`
	ToolRefs                   []WorkforceToolRefDTO          `json:"tool_refs,omitempty"`
	QAEnabled                  bool                           `json:"qa_enabled"`
	Enabled                    bool                           `json:"enabled"`
	PlacementStatus            string                         `json:"placement_status,omitempty"`
	ReleasedFromOrganizationID string                         `json:"released_from_organization_id,omitempty"`
	ReleasedFromDepartmentID   string                         `json:"released_from_department_id,omitempty"`
	ReleasedFromPositionID     string                         `json:"released_from_position_id,omitempty"`
	ReleasedAt                 time.Time                      `json:"released_at,omitempty"`
	ReleasedReason             string                         `json:"released_reason,omitempty"`
}

type WorkforcePersonSourceFactsDTO struct {
	EmployeeNumber string `json:"employee_number,omitempty"`
	Source         string `json:"source,omitempty"`
	SourceRecordID string `json:"source_record_id,omitempty"`
	SourceTenantID string `json:"source_tenant_id,omitempty"`
}

type WorkforceAccountProjectionDTO struct {
	Status           string `json:"status,omitempty"`
	Revision         int64  `json:"revision,omitempty"`
	KratosIdentityID string `json:"kratos_identity_id,omitempty"`
	InitialPassword  string `json:"initial_password,omitempty"`
}

type WorkforceUserAccountDTO struct {
	Username        string   `json:"username,omitempty"`
	Nickname        string   `json:"nickname,omitempty"`
	Email           string   `json:"email,omitempty"`
	Role            string   `json:"role,omitempty"`
	AccountEnabled  bool     `json:"account_enabled"`
	PermissionScope []string `json:"permission_scope,omitempty"`
	Password        string   `json:"password,omitempty"`
}

type WorkforceBaseAgentDTO struct {
	InheritsBaseAgent bool   `json:"inherits_base_agent"`
	AuditRequired     bool   `json:"audit_required"`
	SystemPrompt      string `json:"system_prompt,omitempty"`
	HasSystemPrompt   bool   `json:"has_system_prompt"`
	HasLLMAPIKeyRef   bool   `json:"has_llm_api_key_ref"`
	LLMAPIKeyRef      string `json:"llm_api_key_ref,omitempty"`
	SkillScope        string `json:"skill_scope,omitempty"`
	ToolScope         string `json:"tool_scope,omitempty"`
	MCPScope          string `json:"mcp_scope,omitempty"`
}

type WorkforceSkillRefDTO struct {
	SkillID           string   `json:"skill_id"`
	VersionConstraint string   `json:"version_constraint,omitempty"`
	Alias             string   `json:"alias,omitempty"`
	Level             string   `json:"level,omitempty"`
	EvidenceRefs      []string `json:"evidence_refs,omitempty"`
}

type WorkforceToolRefDTO struct {
	ToolID            string `json:"tool_id"`
	VersionConstraint string `json:"version_constraint,omitempty"`
	Alias             string `json:"alias,omitempty"`
	PermissionScope   string `json:"permission_scope,omitempty"`
	BindingPolicy     string `json:"binding_policy,omitempty"`
}

type WorkforceMemoryProfileRefDTO struct {
	MemoryProfileID   string `json:"memory_profile_id,omitempty"`
	OwnerAgentID      string `json:"owner_agent_id,omitempty"`
	MemoryKind        string `json:"memory_kind,omitempty"`
	AccessScope       string `json:"access_scope,omitempty"`
	RetrievalPolicy   string `json:"retrieval_policy,omitempty"`
	CompressionPolicy string `json:"compression_policy,omitempty"`
}

type OrgGraphQuery struct {
	OrganizationID string
	IncludeMembers bool
}

type OrgGraphDTO struct {
	Organizations     []OrganizationDTO     `json:"organizations,omitempty"`
	Departments       []DepartmentDTO       `json:"departments"`
	Positions         []PositionDTO         `json:"positions"`
	ReportingEdges    []ReportingEdgeDTO    `json:"reporting_edges"`
	Members           []WorkforceMemberDTO  `json:"members"`
	OrganizationNodes []OrganizationNodeDTO `json:"organization_nodes,omitempty"`
	Summary           OrgGraphSummaryDTO    `json:"summary,omitempty"`
}

type DepartmentDTO struct {
	DepartmentID       string `json:"department_id"`
	OrganizationID     string `json:"organization_id"`
	Name               string `json:"name,omitempty"`
	ParentDepartmentID string `json:"parent_department_id,omitempty"`
	DefaultPositionID  string `json:"default_position_id,omitempty"`
	MemberCount        int    `json:"member_count"`
	Code               string `json:"code,omitempty"`
	DisplayCode        string `json:"display_code,omitempty"`
	Status             string `json:"status,omitempty"`
	OwnerProfileID     string `json:"owner_profile_id,omitempty"`
	OwnerEmployeeNo    string `json:"owner_employee_no,omitempty"`
	OwnerName          string `json:"owner_name,omitempty"`
	NodeType           string `json:"node_type,omitempty"`
}

type PositionDTO struct {
	PositionID      string `json:"position_id"`
	DepartmentID    string `json:"department_id"`
	Name            string `json:"name,omitempty"`
	KnowledgeBaseID string `json:"knowledge_base_id,omitempty"`
	MemberCount     int    `json:"member_count"`
}

type ReportingEdgeDTO struct {
	FromAgentID      string `json:"from_agent_id"`
	ToManagerAgentID string `json:"to_manager_agent_id"`
	ReportingLineID  string `json:"reporting_line_id,omitempty"`
}

type PermissionSummaryDTO struct {
	EffectiveRoleCount    int      `json:"effective_role_count"`
	DirectRoleCount       int      `json:"direct_role_count"`
	InheritedRoleCount    int      `json:"inherited_role_count"`
	KeyPermissions        []string `json:"key_permissions,omitempty"`
	DenyPolicyCount       int      `json:"deny_policy_count"`
	ApprovalRequiredCount int      `json:"approval_required_count"`
}

type ActorRefDTO struct {
	ActorType string `json:"actor_type"`
	ActorID   string `json:"actor_id"`
}

type RoleAssignmentDTO struct {
	AssignmentID       string      `json:"assignment_id"`
	Actor              ActorRefDTO `json:"actor"`
	RoleID             string      `json:"role_id"`
	OrganizationNodeID string      `json:"organization_node_id"`
	OrganizationCode   string      `json:"organization_code"`
	ScopeMode          string      `json:"scope_mode"`
	Source             string      `json:"source"`
	StartsAt           time.Time   `json:"starts_at,omitempty"`
	ExpiresAt          time.Time   `json:"expires_at,omitempty"`
	AssignedBy         string      `json:"assigned_by,omitempty"`
}

type OrganizationNodeDTO struct {
	ID                 string                `json:"id"`
	NodeType           string                `json:"node_type"`
	Code               string                `json:"code"`
	DisplayCode        string                `json:"display_code"`
	Name               string                `json:"name"`
	Level              int                   `json:"level"`
	Status             string                `json:"status"`
	OwnerProfileID     string                `json:"owner_profile_id,omitempty"`
	OwnerEmployeeNo    string                `json:"owner_employee_no,omitempty"`
	OwnerName          string                `json:"owner_name,omitempty"`
	KnowledgeBaseID    string                `json:"knowledge_base_id,omitempty"`
	ParentID           string                `json:"parent_id,omitempty"`
	ParentCode         string                `json:"parent_code,omitempty"`
	ParentDisplayCode  string                `json:"parent_display_code,omitempty"`
	ParentName         string                `json:"parent_name,omitempty"`
	DirectMemberCount  int                   `json:"direct_member_count"`
	TotalMemberCount   int                   `json:"total_member_count"`
	ChildCount         int                   `json:"child_count"`
	RoleAssignments    []RoleAssignmentDTO   `json:"role_assignments,omitempty"`
	PermissionSummary  PermissionSummaryDTO  `json:"permission_summary,omitempty"`
	ApprovalPolicyRefs []string              `json:"approval_policy_refs,omitempty"`
	Children           []OrganizationNodeDTO `json:"children,omitempty"`
}

type OrgGraphSummaryDTO struct {
	RootCount            int `json:"root_count"`
	OrganizationCount    int `json:"organization_count"`
	ActiveCount          int `json:"active_count"`
	InactiveCount        int `json:"inactive_count"`
	DirectMemberCount    int `json:"direct_member_count"`
	TotalMemberCount     int `json:"total_member_count"`
	RoleAssignmentCount  int `json:"role_assignment_count"`
	PendingApprovalCount int `json:"pending_approval_count"`
}
