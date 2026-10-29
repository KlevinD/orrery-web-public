package operations

import (
	"encoding/json"
	"strings"

	agentcore "orrery_v2/internal/agent/core"
	skillres "orrery_v2/internal/resource/skill"
)

type UpsertWorkforceMemberCommand struct {
	AgentProfileID       string                            `json:"agent_profile_id"`
	AgentInstanceID      string                            `json:"agent_instance_id,omitempty"`
	RealName             string                            `json:"real_name"`
	DisplayName          string                            `json:"display_name"`
	Email                string                            `json:"email,omitempty"`
	PersonSource         WorkforcePersonSourceFactsDTO     `json:"person_source,omitempty"`
	AgentKind            string                            `json:"agent_kind"`
	UserAccount          WorkforceUserAccountDTO           `json:"user_account,omitempty"`
	EmploymentType       string                            `json:"employment_type"`
	OrganizationID       string                            `json:"organization_id"`
	DepartmentID         string                            `json:"department_id"`
	PositionID           string                            `json:"position_id"`
	RoleIDs              []string                          `json:"role_ids,omitempty"`
	ManagerAgentID       string                            `json:"manager_agent_id,omitempty"`
	ReportingLineID      string                            `json:"reporting_line_id,omitempty"`
	AuthorityScope       string                            `json:"authority_scope,omitempty"`
	SupervisionPolicy    string                            `json:"supervision_policy,omitempty"`
	WorkspaceRoot        string                            `json:"workspace_root,omitempty"`
	RuntimeKind          string                            `json:"runtime_kind,omitempty"`
	RuntimeConfigRef     string                            `json:"runtime_config_ref,omitempty"`
	RuntimeConfig        json.RawMessage                   `json:"runtime_config,omitempty"`
	SystemPrompt         string                            `json:"system_prompt,omitempty"`
	BaseAgentLLMProfile  agentcore.BaseAgentLLMProfileSpec `json:"-"`
	Persona              WorkforcePersonaDTO               `json:"persona,omitempty"`
	PersonalMemoryID     string                            `json:"personal_memory_id,omitempty"`
	CollectiveMemoryRefs []WorkforceMemoryProfileRefDTO    `json:"collective_memory_refs,omitempty"`
	SkillRefs            []WorkforceSkillRefDTO            `json:"skill_refs,omitempty"`
	ToolRefs             []WorkforceToolRefDTO             `json:"tool_refs,omitempty"`
	QAEnabled            bool                              `json:"qa_enabled"`
	Enabled              bool                              `json:"enabled"`
	UpdateMask           []string                          `json:"update_mask,omitempty"`
}

func (cmd UpsertWorkforceMemberCommand) partialUpdate() bool {
	return cmd.UpdateMask != nil
}

func (cmd UpsertWorkforceMemberCommand) updatesField(path string) bool {
	if cmd.UpdateMask == nil {
		return true
	}
	path = strings.TrimSpace(path)
	if path == "" {
		return false
	}
	for _, item := range cmd.UpdateMask {
		if strings.TrimSpace(item) == path {
			return true
		}
	}
	return false
}

func (cmd UpsertWorkforceMemberCommand) updatesAny(paths ...string) bool {
	for _, path := range paths {
		if cmd.updatesField(path) {
			return true
		}
	}
	return false
}

func (cmd UpsertWorkforceMemberCommand) updatesPrefix(prefix string) bool {
	if cmd.UpdateMask == nil {
		return true
	}
	prefix = strings.TrimSpace(prefix)
	if prefix == "" {
		return false
	}
	for _, item := range cmd.UpdateMask {
		item = strings.TrimSpace(item)
		if item == prefix || strings.HasPrefix(item, prefix+".") {
			return true
		}
	}
	return false
}

type WorkforcePersonaDTO struct {
	Backstory              string `json:"backstory,omitempty"`
	ResponsibilityBoundary string `json:"responsibility_boundary,omitempty"`
	BehaviorBoundary       string `json:"behavior_boundary,omitempty"`
}

type CreateOrganizationCommand struct {
	Name                  string `json:"name"`
	DefaultDepartmentName string `json:"default_department_name"`
	DefaultPositionName   string `json:"default_position_name"`
}

type CreateOrganizationUnitCommand struct {
	OrganizationID      string `json:"organization_id"`
	Name                string `json:"name"`
	ParentDepartmentID  string `json:"parent_department_id,omitempty"`
	DefaultPositionName string `json:"default_position_name,omitempty"`
}

type CreateOrganizationPositionCommand struct {
	OrganizationID string `json:"organization_id"`
	DepartmentID   string `json:"department_id,omitempty"`
	Name           string `json:"name"`
}

type OrganizationDTO struct {
	OrganizationID      string `json:"organization_id"`
	Name                string `json:"name"`
	DefaultDepartmentID string `json:"default_department_id"`
	DefaultPositionID   string `json:"default_position_id"`
	Code                string `json:"code,omitempty"`
	DisplayCode         string `json:"display_code,omitempty"`
	Status              string `json:"status,omitempty"`
	OwnerProfileID      string `json:"owner_profile_id,omitempty"`
	OwnerEmployeeNo     string `json:"owner_employee_no,omitempty"`
	OwnerName           string `json:"owner_name,omitempty"`
	KnowledgeBaseID     string `json:"knowledge_base_id,omitempty"`
	NodeType            string `json:"node_type,omitempty"`
}

type SetWorkforceMemberEnabledCommand struct {
	AgentProfileID string `json:"agent_profile_id"`
	Enabled        bool   `json:"enabled"`
}

type ReleaseWorkforceMemberCommand struct {
	AgentProfileID string `json:"agent_profile_id"`
	Reason         string `json:"reason,omitempty"`
}

type HireTalentFromMarketCommand struct {
	AgentProfileID  string   `json:"agent_profile_id"`
	OrganizationID  string   `json:"organization_id"`
	DepartmentID    string   `json:"department_id"`
	PositionID      string   `json:"position_id"`
	RoleIDs         []string `json:"role_ids,omitempty"`
	ManagerAgentID  string   `json:"manager_agent_id,omitempty"`
	ReportingLineID string   `json:"reporting_line_id,omitempty"`
}

type DeleteTalentMarketMemberCommand struct {
	AgentProfileID string `json:"agent_profile_id"`
	Reason         string `json:"reason,omitempty"`
}

type DeleteTalentMarketMembersCommand struct {
	AgentProfileIDs []string `json:"agent_profile_ids"`
	Reason          string   `json:"reason,omitempty"`
}

type DeleteTalentMarketMembersResponseDTO struct {
	Deleted      []string                       `json:"deleted"`
	Skipped      []TalentMarketDeleteSkippedDTO `json:"skipped,omitempty"`
	DeletedCount int                            `json:"deleted_count"`
}

type TalentMarketDeleteSkippedDTO struct {
	AgentProfileID string `json:"agent_profile_id"`
	Reason         string `json:"reason"`
}

type TalentMarketImportCandidateDTO struct {
	AgentProfileID       string                         `json:"agent_profile_id,omitempty"`
	RealName             string                         `json:"real_name"`
	DisplayName          string                         `json:"display_name,omitempty"`
	RoleName             string                         `json:"role_name,omitempty"`
	AgentKind            string                         `json:"agent_kind,omitempty"`
	Description          string                         `json:"description,omitempty"`
	Category             string                         `json:"category,omitempty"`
	SourceSlug           string                         `json:"source_slug,omitempty"`
	SourcePath           string                         `json:"source_path,omitempty"`
	SourceHash           string                         `json:"source_hash,omitempty"`
	SourcePrompt         string                         `json:"source_prompt,omitempty"`
	RuntimeKind          string                         `json:"runtime_kind,omitempty"`
	RuntimeConfigRef     string                         `json:"runtime_config_ref,omitempty"`
	RuntimeConfig        json.RawMessage                `json:"runtime_config,omitempty"`
	Persona              WorkforcePersonaDTO            `json:"persona,omitempty"`
	PersonalMemoryRef    WorkforceMemoryProfileRefDTO   `json:"personal_memory_ref,omitempty"`
	CollectiveMemoryRefs []WorkforceMemoryProfileRefDTO `json:"collective_memory_refs,omitempty"`
	SkillRefs            []WorkforceSkillRefDTO         `json:"skill_refs,omitempty"`
	ToolRefs             []WorkforceToolRefDTO          `json:"tool_refs,omitempty"`
	QAEnabled            bool                           `json:"qa_enabled"`
	Enabled              bool                           `json:"enabled"`
	ConflictResolution   string                         `json:"conflict_resolution,omitempty"`
	ResolvedRealName     string                         `json:"resolved_real_name,omitempty"`
}

type TalentMarketSkillDefinitionDTO struct {
	SkillID       string                 `json:"skill_id"`
	Name          string                 `json:"name"`
	Version       string                 `json:"version"`
	Description   string                 `json:"description,omitempty"`
	DomainTags    []string               `json:"domain_tags,omitempty"`
	TaskTypeTags  []string               `json:"task_type_tags,omitempty"`
	PromptAssets  []skillres.PromptAsset `json:"prompt_assets,omitempty"`
	RequiredTools []WorkforceToolRefDTO  `json:"required_tools,omitempty"`
	EvidenceRefs  []string               `json:"evidence_refs,omitempty"`
	Enabled       bool                   `json:"enabled"`
	SourceRef     string                 `json:"source_ref,omitempty"`
	CreatedAt     string                 `json:"created_at,omitempty"`
	UpdatedAt     string                 `json:"updated_at,omitempty"`
}

type TalentMarketImportPreviewRequest struct {
	Candidates []TalentMarketImportCandidateDTO `json:"candidates"`
	Skills     []TalentMarketSkillDefinitionDTO `json:"skills,omitempty"`
}

type TalentMarketImportPreviewResponseDTO struct {
	Items     []TalentMarketImportPreviewItemDTO `json:"items"`
	Conflicts []TalentMarketImportConflictDTO    `json:"conflicts"`
	Skills    []TalentMarketSkillDefinitionDTO   `json:"skills,omitempty"`
}

type TalentMarketImportPreviewItemDTO struct {
	CandidateIndex int                             `json:"candidate_index"`
	Candidate      TalentMarketImportCandidateDTO  `json:"candidate"`
	Status         string                          `json:"status"`
	Conflicts      []TalentMarketImportConflictDTO `json:"conflicts,omitempty"`
}

type TalentMarketImportConflictDTO struct {
	CandidateIndex          int    `json:"candidate_index"`
	ConflictType            string `json:"conflict_type"`
	ExistingAgentProfileID  string `json:"existing_agent_profile_id"`
	ExistingRealName        string `json:"existing_real_name"`
	ExistingDisplayName     string `json:"existing_display_name,omitempty"`
	ExistingPlacementStatus string `json:"existing_placement_status,omitempty"`
	CandidateRealName       string `json:"candidate_real_name"`
	CandidateDisplayName    string `json:"candidate_display_name,omitempty"`
	CandidateSlug           string `json:"candidate_slug,omitempty"`
}

type TalentMarketImportCommitRequest struct {
	Candidates []TalentMarketImportCandidateDTO `json:"candidates"`
	Skills     []TalentMarketSkillDefinitionDTO `json:"skills,omitempty"`
}

type TalentMarketImportCommitResponseDTO struct {
	Imported  []WorkforceMemberDTO                 `json:"imported"`
	Skipped   []TalentMarketImportSkippedDTO       `json:"skipped,omitempty"`
	Conflicts []TalentMarketImportConflictDTO      `json:"conflicts,omitempty"`
	Preview   TalentMarketImportPreviewResponseDTO `json:"preview,omitempty"`
}

type TalentMarketImportSkippedDTO struct {
	CandidateIndex int    `json:"candidate_index"`
	RealName       string `json:"real_name,omitempty"`
	Reason         string `json:"reason"`
}

type TalentMarketExportRequest struct {
	AgentProfileIDs []string `json:"agent_profile_ids"`
}

type TalentMarketExportPackageDTO struct {
	Manifest TalentMarketExportManifestDTO    `json:"manifest"`
	Agents   []TalentMarketImportCandidateDTO `json:"agents"`
	Skills   []TalentMarketSkillDefinitionDTO `json:"skills,omitempty"`
}

type TalentMarketExportManifestDTO struct {
	Format      string `json:"format"`
	Version     int    `json:"version"`
	Count       int    `json:"count"`
	ExportedAt  string `json:"exported_at"`
	SourceApp   string `json:"source_app"`
	Description string `json:"description,omitempty"`
}
