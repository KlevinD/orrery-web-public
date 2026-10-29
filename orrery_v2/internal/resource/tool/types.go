package tool

import (
	"time"

	agentcore "orrery_v2/internal/agent/core"
)

type Kind string

const (
	KindMCPServer             Kind = "mcp_server"
	KindAPITool               Kind = "api_tool"
	KindCLITool               Kind = "cli_tool"
	KindRPATool               Kind = "rpa_tool"
	KindBrowserTool           Kind = "browser_tool"
	KindApprovalTool          Kind = "approval_tool"
	KindHumanConfirmationTool Kind = "human_confirmation_tool"
	KindInternalServiceTool   Kind = "internal_service_tool"
)

func (k Kind) IsValid() bool {
	switch k {
	case KindMCPServer, KindAPITool, KindCLITool, KindRPATool, KindBrowserTool,
		KindApprovalTool, KindHumanConfirmationTool, KindInternalServiceTool:
		return true
	default:
		return false
	}
}

type RiskLevel string

const (
	RiskLow      RiskLevel = "low"
	RiskMedium   RiskLevel = "medium"
	RiskHigh     RiskLevel = "high"
	RiskCritical RiskLevel = "critical"
)

type ToolDefinition struct {
	ID                  agentcore.ToolID        `json:"tool_id"`
	Kind                Kind                    `json:"kind"`
	Name                string                  `json:"name"`
	Description         string                  `json:"description"`
	Version             string                  `json:"version"`
	RuntimeRequirements RuntimeRequirements     `json:"runtime_requirements"`
	InputSchema         map[string]any          `json:"input_schema,omitempty"`
	OutputSchema        map[string]any          `json:"output_schema,omitempty"`
	Permissions         []PermissionDeclaration `json:"permission_declarations"`
	SecretRefs          []SecretRef             `json:"secret_refs"`
	Enabled             bool                    `json:"enabled"`
	RiskLevel           RiskLevel               `json:"risk_level"`
	MCP                 *MCPServerDefinition    `json:"mcp,omitempty"`
	CLI                 *CLIToolDefinition      `json:"cli,omitempty"`
	Approval            *ApprovalToolDefinition `json:"approval,omitempty"`
	Tags                []string                `json:"tags,omitempty"`
	CreatedAt           time.Time               `json:"created_at"`
	UpdatedAt           time.Time               `json:"updated_at"`
}

type RuntimeRequirements struct {
	Platforms []string `json:"platforms,omitempty"`
	Packages  []string `json:"packages,omitempty"`
	Notes     string   `json:"notes,omitempty"`
}

type PermissionDeclaration struct {
	Scope       string    `json:"scope"`
	Description string    `json:"description,omitempty"`
	Risk        RiskLevel `json:"risk"`
	Required    bool      `json:"required"`
}

type SecretRef struct {
	Name string `json:"name"`
	Env  string `json:"env,omitempty"`
}

type ResolvedTool struct {
	Ref                agentcore.ToolRef
	Definition         ToolDefinition
	GrantedPermissions []string
	SecretBindingRefs  []SecretBindingRef
	BindingWarnings    []BindingWarning
}

type SecretBindingRef struct {
	SecretName  string
	Env         string
	SecretValue string
}

type BindingWarning struct {
	Code    string
	Message string
}

func (d ToolDefinition) Clone() ToolDefinition {
	d.RuntimeRequirements.Platforms = append([]string(nil), d.RuntimeRequirements.Platforms...)
	d.RuntimeRequirements.Packages = append([]string(nil), d.RuntimeRequirements.Packages...)
	d.Permissions = append([]PermissionDeclaration(nil), d.Permissions...)
	d.SecretRefs = append([]SecretRef(nil), d.SecretRefs...)
	d.Tags = append([]string(nil), d.Tags...)
	d.InputSchema = cloneMap(d.InputSchema)
	d.OutputSchema = cloneMap(d.OutputSchema)
	if d.MCP != nil {
		mcp := d.MCP.clone()
		d.MCP = &mcp
	}
	if d.CLI != nil {
		cli := d.CLI.clone()
		d.CLI = &cli
	}
	if d.Approval != nil {
		approval := d.Approval.clone()
		d.Approval = &approval
	}
	return d
}

func cloneMap(src map[string]any) map[string]any {
	if src == nil {
		return nil
	}
	dst := make(map[string]any, len(src))
	for key, value := range src {
		dst[key] = value
	}
	return dst
}
