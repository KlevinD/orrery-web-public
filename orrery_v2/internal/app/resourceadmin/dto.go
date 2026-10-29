package resourceadmin

import (
	"net/url"
	"strings"
	"time"

	memcore "orrery_v2/internal/memory/core"
	skillres "orrery_v2/internal/resource/skill"
	toolres "orrery_v2/internal/resource/tool"
	"orrery_v2/internal/retrievalevidence"
)

type ResourceAdminViewDTO struct {
	MCPServers     []MCPServerDTO       `json:"mcp_servers"`
	Tools          []ToolDTO            `json:"tools"`
	Skills         []SkillDTO           `json:"skills"`
	MemoryProfiles []MemoryProfileDTO   `json:"memory_profiles,omitempty"`
	Usage          []ResourceUsageDTO   `json:"usage,omitempty"`
	Bindings       []ResourceBindingDTO `json:"bindings,omitempty"`
	Page           PageDTO              `json:"page"`
}

type PageDTO struct {
	Limit    int  `json:"limit,omitempty"`
	Offset   int  `json:"offset,omitempty"`
	Total    int  `json:"total"`
	Returned int  `json:"returned"`
	HasMore  bool `json:"has_more"`
}

type ListResourcesRequest struct {
	Search          string `json:"search,omitempty"`
	Kind            string `json:"kind,omitempty"`
	Category        string `json:"category,omitempty"`
	RiskLevel       string `json:"risk_level,omitempty"`
	Enabled         *bool  `json:"enabled,omitempty"`
	ResourceID      string `json:"resource_id,omitempty"`
	BoundAgentID    string `json:"bound_agent_id,omitempty"`
	RequiredToolID  string `json:"required_tool_id,omitempty"`
	IncludeUsage    bool   `json:"include_usage,omitempty"`
	IncludeBindings bool   `json:"include_bindings,omitempty"`
	Limit           int    `json:"limit,omitempty"`
	Offset          int    `json:"offset,omitempty"`
}

type DiscoverMCPRequest struct {
	Query string `json:"query"`
	Limit int    `json:"limit,omitempty"`
}

type BindResourceRequest struct {
	AgentID           string `json:"agent_id"`
	ResourceType      string `json:"resource_type"`
	ResourceID        string `json:"resource_id"`
	Alias             string `json:"alias,omitempty"`
	VersionConstraint string `json:"version_constraint,omitempty"`
	PermissionScope   string `json:"permission_scope,omitempty"`
	BindingPolicy     string `json:"binding_policy,omitempty"`
	AgentAK           string `json:"agent_ak,omitempty"`
	AuthHeaderName    string `json:"auth_header_name,omitempty"`
}

type UnbindResourceRequest struct {
	AgentID      string `json:"agent_id"`
	ResourceType string `json:"resource_type"`
	ResourceID   string `json:"resource_id"`
}

type DiscoverMCPResponseDTO struct {
	Servers []DiscoveredMCPServerDTO `json:"servers"`
}

type DiscoveredMCPServerDTO struct {
	DiscoveredID string         `json:"discovered_id"`
	Name         string         `json:"name"`
	Description  string         `json:"description"`
	Version      string         `json:"version"`
	PackageRefs  []string       `json:"package_refs"`
	Tools        []string       `json:"tools"`
	EnvSecrets   []SecretRefDTO `json:"env_secret_refs"`
	Homepage     string         `json:"homepage,omitempty"`
}

type SkillPackageDTO struct {
	FileName    string `json:"file_name"`
	ContentType string `json:"content_type"`
	Bytes       []byte `json:"-"`
}

type SkillBundleListDTO struct {
	Bundles []SkillBundleDTO `json:"bundles"`
}

type SkillBundleDTO struct {
	BundleID    string     `json:"bundle_id"`
	Name        string     `json:"name"`
	Description string     `json:"description,omitempty"`
	SkillIDs    []string   `json:"skill_ids"`
	Skills      []SkillDTO `json:"skills"`
	CreatedBy   string     `json:"created_by,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

type CreateSkillBundleRequest struct {
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	SkillIDs    []string `json:"skill_ids,omitempty"`
}

type AddSkillsToBundleRequest struct {
	SkillIDs []string `json:"skill_ids"`
}

type DeleteSkillBundleRequest struct {
	DeleteSkills bool `json:"delete_skills"`
}

type SkillResourceCountsDTO struct {
	Assets     int `json:"assets"`
	Resources  int `json:"resources"`
	References int `json:"references"`
	Scripts    int `json:"scripts"`
	Other      int `json:"other"`
	Total      int `json:"total"`
}

type SkillDetailDTO struct {
	Skill   SkillDTO            `json:"skill"`
	SkillMD SkillMarkdownDTO    `json:"skill_md"`
	Roots   []skillres.FileRoot `json:"roots"`
}

type SkillMarkdownDTO struct {
	Path      string    `json:"path"`
	Raw       string    `json:"raw"`
	UpdatedAt time.Time `json:"updated_at"`
}

type CreateSkillRequest struct {
	Name         string   `json:"name"`
	Description  string   `json:"description,omitempty"`
	Version      string   `json:"version,omitempty"`
	TaskTypeTags []string `json:"task_type_tags,omitempty"`
	Prompt       string   `json:"prompt"`
}

// SkillVersionCandidateRequest is the write envelope used by RSI producers.
// The definition is stored as an immutable candidate and does not change the
// active Skill until an explicit promotion succeeds.
type SkillVersionCandidateRequest struct {
	Definition skillres.SkillDefinition `json:"definition"`
}

type SkillVersionEvaluation struct {
	EvaluationRef           string   `json:"evaluation_ref"`
	Status                  string   `json:"status,omitempty"`
	CandidateVersion        string   `json:"candidate_version"`
	BaselineVersion         string   `json:"baseline_version"`
	CandidateDefinitionHash string   `json:"candidate_definition_hash"`
	BaselineDefinitionHash  string   `json:"baseline_definition_hash"`
	DatasetRef              string   `json:"dataset_ref"`
	PolicyVersion           string   `json:"policy_version"`
	EvaluatorRef            string   `json:"evaluator_ref"`
	BaselineScore           float64  `json:"baseline_score"`
	CandidateScore          float64  `json:"candidate_score"`
	HeldOutPassed           bool     `json:"held_out_passed"`
	SafetyPassed            bool     `json:"safety_passed"`
	RegressionCount         int      `json:"regression_count"`
	FailureReasons          []string `json:"failure_reasons,omitempty"`
	EvidenceRefs            []string `json:"evidence_refs,omitempty"`
}

type PromoteSkillVersionRequest struct {
	ExpectedActiveVersion string `json:"expected_active_version,omitempty"`
	EvaluationRef         string `json:"evaluation_ref"`
	Reason                string `json:"reason,omitempty"`
}

type RollbackSkillVersionRequest struct {
	ExpectedActiveVersion string `json:"expected_active_version,omitempty"`
	Reason                string `json:"reason,omitempty"`
}

type SkillVersionDTO struct {
	Skill         SkillDTO `json:"skill"`
	State         string   `json:"state"`
	Active        bool     `json:"active"`
	ActiveVersion string   `json:"active_version"`
}

type SkillFileRequest struct {
	SkillID string `json:"skill_id"`
	Path    string `json:"path"`
}

type SkillFileWriteRequest struct {
	SkillID string `json:"skill_id"`
	Path    string `json:"path"`
	Content string `json:"content"`
}

type SkillFileUploadRequest struct {
	SkillID string `json:"skill_id"`
	Path    string `json:"path"`
	Name    string `json:"name,omitempty"`
	Content []byte `json:"-"`
}

type SkillFileDTO struct {
	Node        skillres.FileNode `json:"node"`
	Path        string            `json:"path"`
	Name        string            `json:"name"`
	Size        int64             `json:"size"`
	MediaType   string            `json:"media_type"`
	PreviewKind string            `json:"preview_kind"`
	Content     string            `json:"content,omitempty"`
	DownloadURL string            `json:"download_url,omitempty"`
}

type SkillFileDownloadDTO struct {
	FileName    string `json:"file_name"`
	ContentType string `json:"content_type"`
	Bytes       []byte `json:"-"`
}

type ResourceUsageDTO struct {
	ResourceID         string   `json:"resource_id"`
	ResourceType       string   `json:"resource_type"`
	UsedByAgentIDs     []string `json:"used_by_agent_ids,omitempty"`
	RequiredBySkillIDs []string `json:"required_by_skill_ids,omitempty"`
	UsageCount         int      `json:"usage_count"`
}

type ResourceBindingDTO struct {
	ResourceID          string `json:"resource_id"`
	ResourceType        string `json:"resource_type"`
	BindingType         string `json:"binding_type"`
	BoundToType         string `json:"bound_to_type"`
	BoundToID           string `json:"bound_to_id"`
	BoundToDisplayName  string `json:"bound_to_display_name,omitempty"`
	Alias               string `json:"alias,omitempty"`
	VersionConstraint   string `json:"version_constraint,omitempty"`
	PermissionScope     string `json:"permission_scope,omitempty"`
	BindingPolicy       string `json:"binding_policy,omitempty"`
	MCPAuthRequired     bool   `json:"mcp_auth_required,omitempty"`
	AuthHeaderName      string `json:"auth_header_name,omitempty"`
	DefaultAKConfigured bool   `json:"default_ak_configured,omitempty"`
	AgentAKConfigured   bool   `json:"agent_ak_configured,omitempty"`
	AgentAKMasked       string `json:"agent_ak_masked,omitempty"`
}

type ToolDTO struct {
	ToolID             string          `json:"tool_id"`
	Kind               string          `json:"kind"`
	Name               string          `json:"name"`
	Description        string          `json:"description"`
	Version            string          `json:"version"`
	InputSchema        map[string]any  `json:"input_schema,omitempty"`
	OutputSchema       map[string]any  `json:"output_schema,omitempty"`
	Enabled            bool            `json:"enabled"`
	RiskLevel          string          `json:"risk_level"`
	Tags               []string        `json:"tags"`
	SecretRefs         []SecretRefDTO  `json:"secret_refs"`
	Permissions        []PermissionDTO `json:"permission_declarations"`
	Source             string          `json:"source,omitempty"`
	ServerToolID       string          `json:"server_tool_id,omitempty"`
	DownstreamToolName string          `json:"downstream_tool_name,omitempty"`
	Manageable         bool            `json:"manageable"`
	Command            string          `json:"command,omitempty"`
	URL                string          `json:"url,omitempty"`
	CreatedAt          time.Time       `json:"created_at"`
	UpdatedAt          time.Time       `json:"updated_at"`
}

type MCPServerDTO struct {
	ToolDTO
	ServerName          string              `json:"server_name"`
	Transport           string              `json:"transport"`
	Command             string              `json:"command,omitempty"`
	URL                 string              `json:"url,omitempty"`
	Headers             map[string]string   `json:"headers,omitempty"`
	AuthRequired        bool                `json:"auth_required"`
	AuthMode            string              `json:"auth_mode,omitempty"`
	AuthHeaderName      string              `json:"auth_header_name,omitempty"`
	DefaultAKConfigured bool                `json:"default_ak_configured"`
	AvailabilityStatus  string              `json:"availability_status,omitempty"`
	LastProbe           *MCPServerProbeDTO  `json:"last_probe,omitempty"`
	Args                []string            `json:"args,omitempty"`
	ExposedTools        []string            `json:"exposed_tools"`
	ExposedToolDetails  []MCPExposedToolDTO `json:"exposed_tool_details,omitempty"`
	CallTimeoutSeconds  int                 `json:"call_timeout_seconds,omitempty"`
	HealthCheck         HealthCheckDTO      `json:"health_check_policy"`
}

type MCPServerProbeDTO struct {
	Status        string    `json:"status"`
	Message       string    `json:"message,omitempty"`
	FailureReason string    `json:"failure_reason,omitempty"`
	HTTPStatus    int       `json:"http_status,omitempty"`
	LatencyMS     int64     `json:"latency_ms,omitempty"`
	CheckedAt     time.Time `json:"checked_at"`
}

type MCPExposedToolDTO struct {
	Name         string         `json:"name"`
	Description  string         `json:"description,omitempty"`
	InputSchema  map[string]any `json:"input_schema,omitempty"`
	OutputSchema map[string]any `json:"output_schema,omitempty"`
}

type SkillDTO struct {
	SkillID        string                 `json:"skill_id"`
	Name           string                 `json:"name"`
	Version        string                 `json:"version"`
	Description    string                 `json:"description"`
	DomainTags     []string               `json:"domain_tags"`
	TaskTypeTags   []string               `json:"task_type_tags"`
	RiskLevel      string                 `json:"risk_level"`
	PromptAssets   []skillres.PromptAsset `json:"prompt_assets"`
	RequiredTools  []ToolRefDTO           `json:"required_tools"`
	EvidenceRefs   []string               `json:"evidence_refs"`
	Enabled        bool                   `json:"enabled"`
	SourceRef      string                 `json:"source_ref,omitempty"`
	SkillMDPresent bool                   `json:"skill_md_present"`
	ResourceCounts SkillResourceCountsDTO `json:"resource_counts"`
	CreatedAt      time.Time              `json:"created_at"`
	UpdatedAt      time.Time              `json:"updated_at"`
}

type SecretRefDTO struct {
	Name        string `json:"name"`
	Env         string `json:"env,omitempty"`
	SecretValue string `json:"-"`
}

type PermissionDTO struct {
	Scope       string `json:"scope"`
	Description string `json:"description,omitempty"`
	Risk        string `json:"risk"`
	Required    bool   `json:"required"`
}

type ToolRefDTO struct {
	ToolID            string `json:"tool_id"`
	VersionConstraint string `json:"version_constraint,omitempty"`
	Alias             string `json:"alias,omitempty"`
}

type HealthCheckDTO struct {
	Mode    string `json:"mode"`
	Timeout string `json:"timeout,omitempty"`
}

type MemoryScopeDTO struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
}

type MemoryRetrievalPolicyDTO struct {
	MaxRecords int      `json:"max_records"`
	Tags       []string `json:"tags,omitempty"`
}

type MemoryCompressionPolicyDTO struct {
	MaxSummaryBytes int `json:"max_summary_bytes"`
}

type MemoryVisibilityPolicyDTO struct {
	Mode      string `json:"mode"`
	ScopeKind string `json:"scope_kind,omitempty"`
	ScopeID   string `json:"scope_id,omitempty"`
}

type MemoryProfileDTO struct {
	MemoryProfileID   string                     `json:"memory_profile_id"`
	OwnerAgentID      string                     `json:"owner_agent_id,omitempty"`
	OrganizationID    string                     `json:"organization_id,omitempty"`
	DepartmentID      string                     `json:"department_id,omitempty"`
	ProjectID         string                     `json:"project_id,omitempty"`
	MemoryKind        string                     `json:"memory_kind"`
	DefaultScope      MemoryScopeDTO             `json:"default_scope"`
	RetrievalPolicy   MemoryRetrievalPolicyDTO   `json:"retrieval_policy"`
	CompressionPolicy MemoryCompressionPolicyDTO `json:"compression_policy"`
	VisibilityPolicy  MemoryVisibilityPolicyDTO  `json:"visibility_policy"`
	Enabled           bool                       `json:"enabled"`
	RecordCount       int                        `json:"record_count"`
	CandidateCount    int                        `json:"candidate_count"`
}

type UpdateMemoryProfileRequest struct {
	Enabled         bool     `json:"enabled"`
	MaxRecords      int      `json:"max_records"`
	MaxSummaryBytes int      `json:"max_summary_bytes"`
	RetrievalTags   []string `json:"retrieval_tags"`
}

type MemoryProfileRefRequestDTO struct {
	MemoryProfileID   string `json:"memory_profile_id"`
	OwnerAgentID      string `json:"owner_agent_id,omitempty"`
	MemoryKind        string `json:"memory_kind,omitempty"`
	AccessScope       string `json:"access_scope,omitempty"`
	RetrievalPolicy   string `json:"retrieval_policy,omitempty"`
	CompressionPolicy string `json:"compression_policy,omitempty"`
}

type BuildMemoryDigestRequest struct {
	AgentID         string                       `json:"agent_id"`
	MissionID       string                       `json:"mission_id,omitempty"`
	TaskID          string                       `json:"task_id,omitempty"`
	OrganizationIDs []string                     `json:"organization_ids,omitempty"`
	DepartmentIDs   []string                     `json:"department_ids,omitempty"`
	ProjectIDs      []string                     `json:"project_ids,omitempty"`
	DiscussionIDs   []string                     `json:"discussion_ids,omitempty"`
	ProfileRefs     []MemoryProfileRefRequestDTO `json:"profile_refs"`
}

type MemoryDigestDTO struct {
	Evidence           []retrievalevidence.Item  `json:"evidence,omitempty"`
	DigestID           string                    `json:"digest_id"`
	AgentID            string                    `json:"agent_id"`
	MissionID          string                    `json:"mission_id,omitempty"`
	TaskID             string                    `json:"task_id,omitempty"`
	Scope              MemoryScopeDTO            `json:"scope"`
	SourceMemoryRefs   []string                  `json:"source_memory_refs"`
	SourceEvidenceRefs []string                  `json:"source_evidence_refs"`
	Summary            string                    `json:"summary"`
	Facts              []string                  `json:"facts,omitempty"`
	Preferences        []string                  `json:"preferences,omitempty"`
	Warnings           []string                  `json:"warnings,omitempty"`
	Decisions          []string                  `json:"decisions,omitempty"`
	Confidence         float64                   `json:"confidence"`
	VisibilityPolicy   MemoryVisibilityPolicyDTO `json:"visibility"`
	CreatedAt          time.Time                 `json:"created_at"`
	ExpiresAt          time.Time                 `json:"expires_at,omitempty"`
}

type RegisterMCPServerRequest struct {
	ToolID             string              `json:"tool_id"`
	Name               string              `json:"name"`
	Description        string              `json:"description"`
	Version            string              `json:"version"`
	Enabled            *bool               `json:"enabled,omitempty"`
	RiskLevel          string              `json:"risk_level,omitempty"`
	ServerName         string              `json:"server_name"`
	Transport          string              `json:"transport"`
	Command            string              `json:"command,omitempty"`
	URL                string              `json:"url,omitempty"`
	Headers            map[string]string   `json:"headers,omitempty"`
	AuthHeaderName     string              `json:"auth_header_name,omitempty"`
	Args               []string            `json:"args,omitempty"`
	EnvSecrets         []SecretRefDTO      `json:"env_secret_refs,omitempty"`
	ExposedTools       []string            `json:"exposed_tools,omitempty"`
	ExposedToolDetails []MCPExposedToolDTO `json:"exposed_tool_details,omitempty"`
	CallTimeoutSeconds int                 `json:"call_timeout_seconds,omitempty"`
	Tags               []string            `json:"tags,omitempty"`
	UpdateMask         []string            `json:"update_mask,omitempty"`
}

type ToolDefinitionRequest struct {
	ToolID      string          `json:"tool_id"`
	Kind        string          `json:"kind"`
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Version     string          `json:"version"`
	RiskLevel   string          `json:"risk_level,omitempty"`
	Enabled     *bool           `json:"enabled,omitempty"`
	Tags        []string        `json:"tags,omitempty"`
	SecretRefs  []SecretRefDTO  `json:"secret_refs,omitempty"`
	Permissions []PermissionDTO `json:"permission_declarations,omitempty"`
	Command     string          `json:"command,omitempty"`
	URL         string          `json:"url,omitempty"`
	UpdateMask  []string        `json:"update_mask,omitempty"`
}

type MCPServerHealthDTO struct {
	ToolID        string    `json:"tool_id"`
	Status        string    `json:"status"`
	Mode          string    `json:"mode"`
	Message       string    `json:"message,omitempty"`
	FailureReason string    `json:"failure_reason,omitempty"`
	LatencyMS     int64     `json:"latency_ms,omitempty"`
	CheckedAt     time.Time `json:"checked_at"`
}

type MCPServerSyncDTO struct {
	ToolID             string              `json:"tool_id"`
	Status             string              `json:"status"`
	ExposedToolCount   int                 `json:"exposed_tool_count"`
	ExposedTools       []string            `json:"exposed_tools,omitempty"`
	ExposedToolDetails []MCPExposedToolDTO `json:"exposed_tool_details,omitempty"`
	FailureReason      string              `json:"failure_reason,omitempty"`
	LatencyMS          int64               `json:"latency_ms,omitempty"`
	SyncedAt           time.Time           `json:"synced_at"`
}

func mapTool(def toolres.ToolDefinition) ToolDTO {
	dto := ToolDTO{
		ToolID:      string(def.ID),
		Kind:        string(def.Kind),
		Name:        def.Name,
		Description: def.Description,
		Version:     def.Version,
		Enabled:     def.Enabled,
		RiskLevel:   string(def.RiskLevel),
		Tags:        append([]string(nil), def.Tags...),
		Manageable:  true,
		CreatedAt:   def.CreatedAt,
		UpdatedAt:   def.UpdatedAt,
	}
	if def.CLI != nil {
		dto.Command = def.CLI.Command
	}
	if def.Kind == toolres.KindAPITool {
		dto.URL = def.RuntimeRequirements.Notes
	}
	for _, ref := range def.SecretRefs {
		dto.SecretRefs = append(dto.SecretRefs, SecretRefDTO{Name: ref.Name, Env: ref.Env})
	}
	for _, permission := range def.Permissions {
		dto.Permissions = append(dto.Permissions, PermissionDTO{
			Scope:       permission.Scope,
			Description: permission.Description,
			Risk:        string(permission.Risk),
			Required:    permission.Required,
		})
	}
	return dto
}

func mapDiscoveredMCP(server toolres.DiscoveredMCPServer) DiscoveredMCPServerDTO {
	dto := DiscoveredMCPServerDTO{
		DiscoveredID: server.ID,
		Name:         server.Name,
		Description:  server.Description,
		Version:      server.Version,
		Tools:        append([]string(nil), server.Tools...),
		Homepage:     server.Homepage,
	}
	for _, pkg := range server.Packages {
		dto.PackageRefs = append(dto.PackageRefs, pkg.RegistryType+":"+pkg.Identifier+"@"+pkg.Version)
	}
	for _, env := range server.Env {
		dto.EnvSecrets = append(dto.EnvSecrets, SecretRefDTO{Name: env.SecretRef, Env: env.Name})
	}
	return dto
}

func mapMCP(def toolres.ToolDefinition) MCPServerDTO {
	dto := MCPServerDTO{ToolDTO: mapTool(def)}
	if def.MCP != nil {
		dto.ServerName = def.MCP.ServerName
		dto.Transport = string(def.MCP.Transport)
		dto.Command = def.MCP.Command
		dto.URL = def.MCP.URL
		dto.Headers = redactMCPHeaders(def.MCP.Headers)
		dto.AuthRequired = def.MCP.Auth.Required
		dto.AuthMode = string(def.MCP.Auth.Mode)
		dto.AuthHeaderName = def.MCP.Auth.HeaderName
		dto.DefaultAKConfigured = def.MCP.Auth.DefaultCredentialConfigured
		dto.AvailabilityStatus = string(def.MCP.Availability.Status)
		if def.ID == "mcp.system" && dto.AvailabilityStatus == "" {
			dto.AvailabilityStatus = string(toolres.MCPAvailabilityAvailable)
		}
		if def.MCP.Availability.Status != "" || !def.MCP.Availability.CheckedAt.IsZero() {
			dto.LastProbe = &MCPServerProbeDTO{
				Status:        string(def.MCP.Availability.Status),
				Message:       def.MCP.Availability.Message,
				FailureReason: def.MCP.Availability.FailureReason,
				HTTPStatus:    def.MCP.Availability.HTTPStatus,
				LatencyMS:     def.MCP.Availability.LatencyMS,
				CheckedAt:     def.MCP.Availability.CheckedAt,
			}
		}
		dto.Args = append([]string(nil), def.MCP.Args...)
		dto.ExposedTools = append([]string(nil), def.MCP.ExposedTools...)
		dto.ExposedToolDetails = mapMCPExposedToolDetails(def.MCP.ExposedToolDetails)
		dto.CallTimeoutSeconds = def.MCP.CallTimeoutSeconds
		dto.HealthCheck = HealthCheckDTO{
			Mode:    string(def.MCP.HealthCheck.Mode),
			Timeout: def.MCP.HealthCheck.Timeout.String(),
		}
		for _, ref := range def.MCP.EnvSecretRefs {
			if !hasSecretRef(dto.SecretRefs, ref.Name, ref.Env) {
				dto.SecretRefs = append(dto.SecretRefs, SecretRefDTO{Name: ref.Name, Env: ref.Env})
			}
		}
	}
	return dto
}

const redactedHeaderValue = "********"
const maskedCredentialSeparator = "****"

func redactMCPHeaders(headers map[string]string) map[string]string {
	if len(headers) == 0 {
		return nil
	}
	out := make(map[string]string, len(headers))
	for name, value := range headers {
		if strings.TrimSpace(name) == "" || strings.TrimSpace(value) == "" {
			continue
		}
		if isSensitiveHeader(name) {
			out[name] = maskCredentialValue(value)
			continue
		}
		out[name] = value
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func isSensitiveHeader(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "authorization", "proxy-authorization", "x-api-key", "api-key":
		return true
	default:
		lower := strings.ToLower(strings.TrimSpace(name))
		return strings.Contains(lower, "token") || strings.Contains(lower, "secret") || strings.Contains(lower, "key")
	}
}

func maskCredentialValue(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	const visible = 4
	runes := []rune(value)
	if len(runes) <= visible*2 {
		return maskedCredentialSeparator
	}
	return string(runes[:visible]) + maskedCredentialSeparator + string(runes[len(runes)-visible:])
}

func isMaskedCredentialValue(value string) bool {
	value = strings.TrimSpace(value)
	return value == redactedHeaderValue || strings.Contains(value, maskedCredentialSeparator)
}

func mapMCPExposedToolDetails(details []toolres.MCPExposedToolDefinition) []MCPExposedToolDTO {
	if len(details) == 0 {
		return nil
	}
	dtos := make([]MCPExposedToolDTO, 0, len(details))
	for _, detail := range details {
		if detail.Name == "" {
			continue
		}
		dtos = append(dtos, MCPExposedToolDTO{
			Name:         detail.Name,
			Description:  detail.Description,
			InputSchema:  cloneSchemaMap(detail.InputSchema),
			OutputSchema: cloneSchemaMap(detail.OutputSchema),
		})
	}
	return dtos
}

func cloneSchemaMap(value map[string]any) map[string]any {
	if len(value) == 0 {
		return nil
	}
	clone := make(map[string]any, len(value))
	for key, item := range value {
		clone[key] = item
	}
	return clone
}

func mapMemoryProfile(profile memcore.MemoryProfile, recordCount, candidateCount int) MemoryProfileDTO {
	return MemoryProfileDTO{
		MemoryProfileID: string(profile.ID),
		OwnerAgentID:    string(profile.OwnerRef.AgentID),
		OrganizationID:  profile.OwnerRef.OrganizationID,
		DepartmentID:    profile.OwnerRef.DepartmentID,
		ProjectID:       profile.OwnerRef.ProjectID,
		MemoryKind:      string(profile.MemoryKind),
		DefaultScope: MemoryScopeDTO{
			Kind: string(profile.DefaultScope.Kind),
			ID:   profile.DefaultScope.ID,
		},
		RetrievalPolicy: MemoryRetrievalPolicyDTO{
			MaxRecords: profile.RetrievalPolicy.MaxRecords,
			Tags:       append([]string(nil), profile.RetrievalPolicy.Tags...),
		},
		CompressionPolicy: MemoryCompressionPolicyDTO{MaxSummaryBytes: profile.CompressionPolicy.MaxSummaryBytes},
		VisibilityPolicy: MemoryVisibilityPolicyDTO{
			Mode:      string(profile.VisibilityPolicy.Mode),
			ScopeKind: string(profile.VisibilityPolicy.ScopeKind),
			ScopeID:   profile.VisibilityPolicy.ScopeID,
		},
		Enabled:        profile.Enabled,
		RecordCount:    recordCount,
		CandidateCount: candidateCount,
	}
}

func mapMemoryDigest(digest memcore.MemoryDigest) MemoryDigestDTO {
	return MemoryDigestDTO{
		Evidence:  retrievalevidence.Clone(digest.Evidence),
		DigestID:  string(digest.ID),
		AgentID:   string(digest.AgentID),
		MissionID: digest.MissionID,
		TaskID:    digest.TaskID,
		Scope: MemoryScopeDTO{
			Kind: string(digest.Scope.Kind),
			ID:   digest.Scope.ID,
		},
		SourceMemoryRefs:   append([]string(nil), digest.SourceMemoryRefs...),
		SourceEvidenceRefs: append([]string(nil), digest.SourceEvidenceRefs...),
		Summary:            digest.Summary,
		Facts:              append([]string(nil), digest.Facts...),
		Preferences:        append([]string(nil), digest.Preferences...),
		Warnings:           append([]string(nil), digest.Warnings...),
		Decisions:          append([]string(nil), digest.Decisions...),
		Confidence:         digest.Confidence,
		VisibilityPolicy: MemoryVisibilityPolicyDTO{
			Mode:      string(digest.Visibility.Mode),
			ScopeKind: string(digest.Visibility.ScopeKind),
			ScopeID:   digest.Visibility.ScopeID,
		},
		CreatedAt: digest.CreatedAt,
		ExpiresAt: digest.ExpiresAt,
	}
}

func hasSecretRef(refs []SecretRefDTO, name, env string) bool {
	for _, ref := range refs {
		if ref.Name == name && ref.Env == env {
			return true
		}
	}
	return false
}

func mapSkill(def skillres.SkillDefinition) SkillDTO {
	dto := SkillDTO{
		SkillID:      string(def.ID),
		Name:         def.Name,
		Version:      def.Version,
		Description:  def.Description,
		DomainTags:   append([]string(nil), def.DomainTags...),
		TaskTypeTags: append([]string(nil), def.TaskTypeTags...),
		RiskLevel:    string(skillres.NormalizeRiskLevel(def.RiskLevel)),
		PromptAssets: summarizePromptAssets(def.PromptAssets),
		Enabled:      def.Enabled,
		SourceRef:    def.SourceRef,
		CreatedAt:    def.CreatedAt,
		UpdatedAt:    def.UpdatedAt,
	}
	for _, toolRef := range def.RequiredTools {
		dto.RequiredTools = append(dto.RequiredTools, ToolRefDTO{
			ToolID:            string(toolRef.ToolID),
			VersionConstraint: toolRef.VersionConstraint,
			Alias:             toolRef.Alias,
		})
	}
	for _, evidenceRef := range def.EvidenceRefs {
		dto.EvidenceRefs = append(dto.EvidenceRefs, evidenceRef.ID)
	}
	return dto
}

func summarizePromptAssets(assets []skillres.PromptAsset) []skillres.PromptAsset {
	if len(assets) == 0 {
		return nil
	}
	out := make([]skillres.PromptAsset, 0, len(assets))
	for _, asset := range assets {
		asset.Content = ""
		out = append(out, asset)
	}
	return out
}

func mapSkillWithTree(def skillres.SkillDefinition, tree skillres.FileTree) SkillDTO {
	dto := mapSkill(def)
	dto.SkillMDPresent = skillTreeHasSkillMarkdown(def)
	dto.ResourceCounts = mapSkillResourceCounts(tree.Counts)
	return dto
}

func mapSkillResourceCounts(counts skillres.ResourceCounts) SkillResourceCountsDTO {
	return SkillResourceCountsDTO{
		Assets:     counts.Assets,
		Resources:  counts.Resources,
		References: counts.References,
		Scripts:    counts.Scripts,
		Other:      counts.Other,
		Total:      counts.Total,
	}
}

func mapSkillFile(skillID string, node skillres.FileNode, raw []byte) SkillFileDTO {
	node.DownloadURL = skillFileDownloadURL(skillID, node.Path)
	dto := SkillFileDTO{
		Node:        node,
		Path:        node.Path,
		Name:        node.Name,
		Size:        node.Size,
		MediaType:   node.MediaType,
		PreviewKind: string(node.PreviewKind),
		DownloadURL: node.DownloadURL,
	}
	if raw != nil {
		dto.Content = string(raw)
	}
	return dto
}

func skillFileDownloadURL(skillID, path string) string {
	if skillID == "" || path == "" {
		return ""
	}
	return "/api/resources/skills/" + url.PathEscape(skillID) + "/files/download?path=" + url.QueryEscape(path)
}

func skillTreeHasSkillMarkdown(def skillres.SkillDefinition) bool {
	for _, asset := range def.PromptAssets {
		if asset.Path == "SKILL.md" {
			return true
		}
	}
	return false
}
