package core

import (
	"encoding/json"
	"net/mail"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"
)

type AgentKind string

const (
	AgentKindAI        AgentKind = "ai"
	AgentKindHuman     AgentKind = "human"
	AgentKindComposite AgentKind = "composite"

	SystemSuperadminPrincipalID      = "system-superadmin"
	LegacySuperadminAgentProfileID   = "human-superadmin"
	LegacyLocalAdminAgentProfileID   = "human-local-admin"
	LegacyLocalUserAgentProfileID    = "human-local-user"
	personalAssistantProfileIDPrefix = "personal-assistant-"
)

func IsReservedNonWorkforceProfileID(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case SystemSuperadminPrincipalID, LegacySuperadminAgentProfileID, LegacyLocalAdminAgentProfileID, LegacyLocalUserAgentProfileID:
		return true
	default:
		return false
	}
}

func (k AgentKind) IsValid() bool {
	switch k {
	case AgentKindAI, AgentKindHuman, AgentKindComposite:
		return true
	default:
		return false
	}
}

type AgentProfile struct {
	ID                        AgentProfileID
	PersonalAssistantOwnerID  string
	PersonalAssistantTenantID string
	RealName                  string
	DisplayName               string
	RoleName                  string
	Email                     string
	PersonSource              PersonSourceFacts
	AgentKind                 AgentKind
	UserAccount               UserAccountSpec
	OrgIdentity               AgentOrgIdentity
	Placement                 TalentPlacement
	Persona                   AgentPersona
	BaseAgent                 BaseAgentSpec
	PersonalKnowledgeBaseID   string
	PersonalMemoryProfileRef  MemoryProfileRef
	CollectiveMemoryScopeRefs []MemoryProfileRef
	SkillRefs                 []SkillRef
	ToolRefs                  []ToolRef
	MCPAuthorizations         []MCPAuthorization
	RuntimeSpec               RuntimeSpec
	WorkspaceRoot             string
	QAEnabled                 bool
	Availability              string
	Enabled                   bool
}

func (p AgentProfile) IsPersonalAssistant() bool {
	return strings.TrimSpace(p.PersonalAssistantOwnerID) != ""
}

type PersonSourceFacts struct {
	EmployeeNumber string
	Source         string
	SourceRecordID string
	SourceTenantID string
}

type UserAccountSpec struct {
	Username         string
	Nickname         string
	Email            string
	Role             string
	AccountEnabled   bool
	PermissionScope  []string
	PasswordSalt     string
	PasswordVerifier string
}

type BaseAgentSpec struct {
	InheritsBaseAgent bool
	AuditRequired     bool
	SystemPrompt      string
	LLMAPIKeyRef      string
	SkillScope        string
	ToolScope         string
	MCPScope          string
	Extensions        map[string]json.RawMessage
}

func ValidateAgentProfile(p AgentProfile) []ValidationError {
	var errs []ValidationError
	if p.IsPersonalAssistant() && strings.TrimSpace(p.PersonalAssistantTenantID) == "" {
		errs = append(errs, validationError(CodeMissingRequiredField, "personal_assistant_tenant_id", "personal assistants require an owning tenant"))
	}
	if IsReservedNonWorkforceProfileID(p.ID.String()) {
		errs = append(errs, validationError(CodeInvalidRef, "id", "profile id is reserved for a non-workforce principal"))
	}
	if p.RealName == "" {
		errs = append(errs, validationError(CodeMissingRequiredField, "real_name", "real_name is required"))
	} else if requiresGeneratedAgentChineseIdentity(p.AgentKind) && !containsHan(p.RealName) {
		errs = append(errs, validationError(CodeMissingRequiredField, "real_name", "real_name must be a humanized Chinese name for generated agents"))
	}
	if !p.AgentKind.IsValid() {
		errs = append(errs, validationError(CodeInvalidAgentKind, "agent_kind", "agent_kind must identify an agent role"))
	}
	if email := strings.TrimSpace(p.Email); email != "" {
		address, err := mail.ParseAddress(email)
		if err != nil || address.Name != "" || !strings.EqualFold(address.Address, email) {
			errs = append(errs, validationError(CodeInvalidRef, "email", "email must be a valid mailbox address"))
		}
	}
	if !p.PersonSource.isZero() && p.AgentKind != AgentKindHuman {
		errs = append(errs, validationError(CodeInvalidAgentKind, "person_source", "person source facts can only be attached to human profiles"))
	}
	if p.PersonSource.hasProvenance() {
		if strings.TrimSpace(p.PersonSource.Source) == "" {
			errs = append(errs, validationError(CodeMissingRequiredField, "person_source.source", "person source is required when provenance is present"))
		}
		if strings.TrimSpace(p.PersonSource.SourceRecordID) == "" {
			errs = append(errs, validationError(CodeMissingRequiredField, "person_source.source_record_id", "person source record id is required when provenance is present"))
		}
		if strings.TrimSpace(p.PersonSource.SourceTenantID) == "" {
			errs = append(errs, validationError(CodeMissingRequiredField, "person_source.source_tenant_id", "person source tenant id is required when provenance is present"))
		}
	}
	if strings.TrimSpace(p.UserAccount.Username) != "" && p.AgentKind != AgentKindHuman {
		errs = append(errs, validationError(CodeInvalidAgentKind, "user_account.username", "user accounts can only be attached to human profiles"))
	}
	if p.Placement.IsInternal(p.OrgIdentity) {
		if p.OrgIdentity.OrganizationRef.ID == "" {
			errs = append(errs, validationError(CodeMissingRequiredField, "org_identity.organization_ref", "organization_ref is required"))
		}
		if !p.IsPersonalAssistant() {
			if p.OrgIdentity.DepartmentRef.ID == "" {
				errs = append(errs, validationError(CodeMissingRequiredField, "org_identity.department_ref", "department_ref is required"))
			}
			if p.OrgIdentity.PositionRef.ID == "" {
				errs = append(errs, validationError(CodeMissingRequiredField, "org_identity.position_ref", "position_ref is required"))
			}
		}
		if p.OrgIdentity.ManagerAgentRef.IsZero() && !p.OrgIdentity.SupervisionPolicy.AllowsMissingManager() {
			errs = append(errs, validationError(CodeMissingRequiredField, "org_identity.manager_agent_ref", "manager_agent_ref is required unless a later staffing policy marks this profile as root"))
		}
	}
	if p.SkillRefs == nil {
		errs = append(errs, validationError(CodeMissingRequiredField, "skill_refs", "skill_refs must be explicitly present, even when empty"))
	}
	for i, skillRef := range p.SkillRefs {
		if skillRef.IsZero() {
			errs = append(errs, validationError(CodeInvalidRef, indexedField("skill_refs", i, "skill_id"), "skill_id is required"))
		}
	}
	if p.ToolRefs == nil {
		errs = append(errs, validationError(CodeMissingRequiredField, "tool_refs", "tool_refs must be explicitly present, even when empty"))
	}
	for i, toolRef := range p.ToolRefs {
		if toolRef.IsZero() {
			errs = append(errs, validationError(CodeInvalidRef, indexedField("tool_refs", i, "tool_id"), "tool_id is required"))
		}
	}
	if !p.Persona.HasRequiredBoundary() {
		errs = append(errs, validationError(CodeMissingRequiredField, "persona", "persona requires backstory or responsibility boundary"))
	}
	if requiresGeneratedAgentChineseIdentity(p.AgentKind) && !containsHan(p.Persona.ResponsibilityBoundary) {
		errs = append(errs, validationError(CodeMissingRequiredField, "persona.responsibility_boundary", "generated agents require a Chinese responsibility and capability summary"))
	}
	errs = append(errs, validateBaseAgentSpec(p.BaseAgent)...)
	if p.PersonalMemoryProfileRef.IsZero() && !BaseAgentUsesSessionOnlyMemory(p.BaseAgent) {
		errs = append(errs, validationError(CodeMissingRequiredField, "personal_memory_profile_ref", "personal_memory_profile_ref is required"))
	} else if !p.PersonalMemoryProfileRef.MemoryKind.IsValid() || p.PersonalMemoryProfileRef.MemoryKind != MemoryKindPersonal {
		if !p.PersonalMemoryProfileRef.IsZero() {
			errs = append(errs, validationError(CodeInvalidRef, "personal_memory_profile_ref.memory_kind", "personal memory profile must use personal memory kind"))
		}
	}
	for i, memoryRef := range p.CollectiveMemoryScopeRefs {
		if memoryRef.IsZero() {
			errs = append(errs, validationError(CodeInvalidRef, indexedField("collective_memory_scope_refs", i, "memory_profile_id"), "memory_profile_id is required"))
		}
		if !memoryRef.MemoryKind.IsValid() || memoryRef.MemoryKind == MemoryKindPersonal {
			errs = append(errs, validationError(CodeInvalidRef, indexedField("collective_memory_scope_refs", i, "memory_kind"), "collective memory scopes must use non-personal memory kinds"))
		}
	}
	if p.RuntimeSpec.HasKind() && !p.RuntimeSpec.Kind.IsValid() {
		errs = append(errs, validationError(CodeInvalidRef, "runtime_spec.kind", "runtime_spec.kind must be a known runtime kind"))
	}
	if root := strings.TrimSpace(p.WorkspaceRoot); root != "" && !filepath.IsAbs(root) {
		errs = append(errs, validationError(CodeInvalidRef, "workspace_root", "workspace_root must be an absolute path"))
	}
	return errs
}

func (f PersonSourceFacts) isZero() bool {
	return strings.TrimSpace(f.EmployeeNumber) == "" && !f.hasProvenance()
}

func (f PersonSourceFacts) hasProvenance() bool {
	return strings.TrimSpace(f.Source) != "" || strings.TrimSpace(f.SourceRecordID) != "" || strings.TrimSpace(f.SourceTenantID) != ""
}

func validateBaseAgentSpec(spec BaseAgentSpec) []ValidationError {
	var errs []ValidationError
	if !spec.InheritsBaseAgent {
		errs = append(errs, validationError(CodeMissingRequiredField, "base_agent.inherits_base_agent", "agents must inherit BaseAgent execution and audit behavior"))
	}
	if !spec.AuditRequired {
		errs = append(errs, validationError(CodeMissingRequiredField, "base_agent.audit_required", "agents must require BaseAgent audit"))
	}
	if strings.TrimSpace(spec.SystemPrompt) == "" {
		errs = append(errs, validationError(CodeMissingRequiredField, "base_agent.system_prompt", "agents must declare a BaseAgent system prompt"))
	}
	if strings.TrimSpace(spec.LLMAPIKeyRef) == "" {
		errs = append(errs, validationError(CodeMissingRequiredField, "base_agent.llm_api_key_ref", "agents must reference the system-managed LLM API key"))
	}
	if strings.TrimSpace(spec.SkillScope) == "" {
		errs = append(errs, validationError(CodeMissingRequiredField, "base_agent.skill_scope", "agents must declare a BaseAgent skill scope"))
	}
	if strings.TrimSpace(spec.ToolScope) == "" {
		errs = append(errs, validationError(CodeMissingRequiredField, "base_agent.tool_scope", "agents must declare a BaseAgent tool scope"))
	}
	if strings.TrimSpace(spec.MCPScope) == "" {
		errs = append(errs, validationError(CodeMissingRequiredField, "base_agent.mcp_scope", "agents must declare a BaseAgent MCP scope"))
	}
	return errs
}

func requiresGeneratedAgentChineseIdentity(kind AgentKind) bool {
	return kind != AgentKindHuman
}

func containsHan(value string) bool {
	for _, r := range value {
		if unicode.Is(unicode.Han, r) {
			return true
		}
	}
	return false
}

func indexedField(collection string, index int, field string) string {
	return collection + "[" + strconv.Itoa(index) + "]." + field
}
