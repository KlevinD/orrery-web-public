package tool

type ApprovalType string

const (
	ApprovalExternalSystem    ApprovalType = "external_system"
	ApprovalHumanConfirmation ApprovalType = "human_confirmation"
)

type ApprovalToolDefinition struct {
	ApprovalType        ApprovalType `json:"approval_type"`
	RequestSchemaRef    string       `json:"request_schema_ref"`
	ResponseSchemaRef   string       `json:"response_schema_ref,omitempty"`
	RequiredPermissions []string     `json:"required_permissions,omitempty"`
}

func (d ApprovalToolDefinition) clone() ApprovalToolDefinition {
	d.RequiredPermissions = append([]string(nil), d.RequiredPermissions...)
	return d
}
