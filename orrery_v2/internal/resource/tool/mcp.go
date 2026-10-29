package tool

import "time"

type MCPTransport string

const TagMCPToolSurfaceLocked = "mcp-tool-surface-locked"

const (
	TransportStdio      MCPTransport = "stdio"
	TransportSSE        MCPTransport = "sse"
	TransportStreamHTTP MCPTransport = "streamable_http"
)

type HealthCheckMode string

const (
	HealthCheckNone     HealthCheckMode = "none"
	HealthCheckStartup  HealthCheckMode = "startup"
	HealthCheckPeriodic HealthCheckMode = "periodic"
)

type MCPAvailabilityStatus string

const (
	MCPAvailabilityUnknown      MCPAvailabilityStatus = ""
	MCPAvailabilityAvailable    MCPAvailabilityStatus = "available"
	MCPAvailabilityUnavailable  MCPAvailabilityStatus = "unavailable"
	MCPAvailabilityRequiresAuth MCPAvailabilityStatus = "requires_auth"
)

type MCPAuthMode string

const (
	MCPAuthModeNone     MCPAuthMode = ""
	MCPAuthModeGlobal   MCPAuthMode = "global"
	MCPAuthModePerAgent MCPAuthMode = "per_agent"
)

type MCPServerDefinition struct {
	ServerName         string                     `json:"server_name"`
	Transport          MCPTransport               `json:"transport"`
	Command            string                     `json:"command,omitempty"`
	URL                string                     `json:"url,omitempty"`
	Headers            map[string]string          `json:"headers,omitempty"`
	Auth               MCPAuthPolicy              `json:"auth,omitempty"`
	Availability       MCPAvailability            `json:"availability,omitempty"`
	Args               []string                   `json:"args,omitempty"`
	EnvSecretRefs      []SecretRef                `json:"env_secret_refs,omitempty"`
	ExposedTools       []string                   `json:"exposed_tools,omitempty"`
	ExposedToolDetails []MCPExposedToolDefinition `json:"exposed_tool_details,omitempty"`
	Permissions        []PermissionDeclaration    `json:"permission_declarations,omitempty"`
	CallTimeoutSeconds int                        `json:"call_timeout_seconds,omitempty"`
	HealthCheck        HealthCheckPolicy          `json:"health_check_policy"`
}

type MCPAuthPolicy struct {
	Required                    bool        `json:"required,omitempty"`
	Mode                        MCPAuthMode `json:"mode,omitempty"`
	HeaderName                  string      `json:"header_name,omitempty"`
	DefaultCredentialConfigured bool        `json:"default_credential_configured,omitempty"`
}

type MCPAvailability struct {
	Status        MCPAvailabilityStatus `json:"status,omitempty"`
	Message       string                `json:"message,omitempty"`
	FailureReason string                `json:"failure_reason,omitempty"`
	HTTPStatus    int                   `json:"http_status,omitempty"`
	LatencyMS     int64                 `json:"latency_ms,omitempty"`
	CheckedAt     time.Time             `json:"checked_at,omitempty"`
}

type HealthCheckPolicy struct {
	Mode    HealthCheckMode `json:"mode"`
	Timeout time.Duration   `json:"timeout"`
}

type MCPExposedToolDefinition struct {
	Name         string         `json:"name"`
	Description  string         `json:"description,omitempty"`
	InputSchema  map[string]any `json:"input_schema,omitempty"`
	OutputSchema map[string]any `json:"output_schema,omitempty"`
}

func (d MCPServerDefinition) clone() MCPServerDefinition {
	d.Args = append([]string(nil), d.Args...)
	d.Headers = cloneStringMap(d.Headers)
	d.EnvSecretRefs = append([]SecretRef(nil), d.EnvSecretRefs...)
	d.ExposedTools = append([]string(nil), d.ExposedTools...)
	d.ExposedToolDetails = cloneMCPExposedToolDefinitions(d.ExposedToolDetails)
	d.Permissions = append([]PermissionDeclaration(nil), d.Permissions...)
	return d
}

func cloneStringMap(src map[string]string) map[string]string {
	if len(src) == 0 {
		return nil
	}
	dst := make(map[string]string, len(src))
	for key, value := range src {
		dst[key] = value
	}
	return dst
}

func cloneMCPExposedToolDefinitions(src []MCPExposedToolDefinition) []MCPExposedToolDefinition {
	if len(src) == 0 {
		return nil
	}
	out := make([]MCPExposedToolDefinition, 0, len(src))
	for _, item := range src {
		item.InputSchema = cloneMap(item.InputSchema)
		item.OutputSchema = cloneMap(item.OutputSchema)
		out = append(out, item)
	}
	return out
}
