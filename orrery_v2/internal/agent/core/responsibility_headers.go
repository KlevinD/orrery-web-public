package core

import (
	"strconv"
	"strings"
)

const (
	RuntimeRunIDHeader                                   = "X-Orrery-Run"
	RuntimeResponsibilityContextIDHeader                 = "X-Orrery-Responsibility-Context"
	RuntimeResponsibilityIDHeader                        = "X-Orrery-Responsibility"
	RuntimeResponsibilityVersionHeader                   = "X-Orrery-Responsibility-Version"
	RuntimeResponsibilityPolicyIDHeader                  = "X-Orrery-Responsibility-Policy"
	RuntimeResponsibilityPolicyVersionHeader             = "X-Orrery-Responsibility-Policy-Version"
	RuntimeResponsibilityAgentProfileHeader              = "X-Agent-Profile-ID"
	RuntimeResponsibilityAllowedToolsHeader              = "X-Orrery-Responsibility-Allowed-Tools"
	RuntimeResponsibilityAllowedDataHeader               = "X-Orrery-Responsibility-Allowed-Data"
	RuntimeResponsibilityPolicyToolScopesHeader          = "X-Orrery-Responsibility-Policy-Tool-Scopes"
	RuntimeResponsibilityPolicyDataScopesHeader          = "X-Orrery-Responsibility-Policy-Data-Scopes"
	RuntimeResponsibilityTaskScopesHeader                = "X-Orrery-Responsibility-Task-Scopes"
	RuntimeResponsibilityRequireExplicitToolScopesHeader = "X-Orrery-Responsibility-Require-Explicit-Tool-Scopes"
	RuntimeExecutionEnvelopeIDHeader                     = "X-Orrery-Execution-Envelope"
	RuntimeExecutionEnvelopeDigestHeader                 = "X-Orrery-Execution-Envelope-Digest"
)

// ExecutionEnvelopeHeaders carries only the envelope's immutable identity.
// The gateway reads the full envelope from the server-owned run record and
// uses these values to detect a stale or mismatched runtime binding.
func ExecutionEnvelopeHeaders(value *ExecutionEnvelope) map[string]string {
	if value == nil {
		return nil
	}
	headers := map[string]string{
		RuntimeExecutionEnvelopeIDHeader:     strings.TrimSpace(value.EnvelopeID),
		RuntimeExecutionEnvelopeDigestHeader: strings.TrimSpace(value.EnvelopeDigest),
	}
	for key, value := range headers {
		if value == "" {
			delete(headers, key)
		}
	}
	return headers
}

// ResponsibilityMCPHeaders carries the immutable execution authority to the
// Orrery MCP gateway. The runtime authentication token protects these headers
// at the transport boundary; the gateway still validates the complete tuple.
func ResponsibilityMCPHeaders(value *ResponsibilityContext) map[string]string {
	if value == nil {
		return nil
	}
	headers := map[string]string{
		RuntimeResponsibilityContextIDHeader:     strings.TrimSpace(value.ContextID),
		RuntimeResponsibilityIDHeader:            strings.TrimSpace(value.ResponsibilityID),
		RuntimeResponsibilityVersionHeader:       positiveInt64String(value.ResponsibilityVersion),
		RuntimeResponsibilityPolicyIDHeader:      strings.TrimSpace(value.PolicyID),
		RuntimeResponsibilityPolicyVersionHeader: positiveInt64String(value.PolicyVersion),
		RuntimeResponsibilityAgentProfileHeader:  strings.TrimSpace(value.AgentProfileID),
	}
	appendResponsibilityHeader(headers, RuntimeResponsibilityAllowedToolsHeader, value.AllowedTools)
	appendResponsibilityHeader(headers, RuntimeResponsibilityAllowedDataHeader, value.AllowedDataScopes)
	appendResponsibilityHeader(headers, RuntimeResponsibilityPolicyToolScopesHeader, value.PolicyToolScopes)
	appendResponsibilityHeader(headers, RuntimeResponsibilityPolicyDataScopesHeader, value.PolicyDataScopes)
	appendResponsibilityHeader(headers, RuntimeResponsibilityTaskScopesHeader, value.TaskPermissionScopes)
	if value.RequireExplicitToolScopes {
		headers[RuntimeResponsibilityRequireExplicitToolScopesHeader] = "true"
	}
	for key, value := range headers {
		if value == "" {
			delete(headers, key)
		}
	}
	return headers
}

// RuntimeMCPAuthorityHeaders returns the server-owned identity and authority
// attached to calls made through the trusted Orrery MCP gateway.
func RuntimeMCPAuthorityHeaders(request RuntimeRequest) map[string]string {
	headers := ResponsibilityMCPHeaders(request.ContextPack.ResponsibilityContext)
	if headers == nil {
		headers = map[string]string{}
	}
	if request.RunID != "" {
		headers[RuntimeRunIDHeader] = request.RunID.String()
	}
	if request.AgentID != "" {
		headers["X-Agent-ID"] = request.AgentID.String()
	}
	if tenantID := strings.TrimSpace(request.TenantID); tenantID != "" {
		headers["X-Orrery-Tenant"] = tenantID
	}
	for key, value := range RuntimeExecutionPrincipalHeaders(request.ExecutionPrincipal, request.TenantID, request.AgentID) {
		headers[key] = value
	}
	for key, value := range ExecutionEnvelopeHeaders(request.ExecutionEnvelope) {
		headers[key] = value
	}
	return headers
}

// IsRuntimeMCPAuthorityHeader identifies fields that runtimes may not choose
// for calls to the trusted Orrery MCP gateway.
func IsRuntimeMCPAuthorityHeader(name string) bool {
	switch {
	case strings.EqualFold(name, RuntimeRunIDHeader),
		strings.EqualFold(name, "X-Agent-ID"),
		strings.EqualFold(name, "X-Orrery-Tenant"),
		strings.EqualFold(name, RuntimePrincipalIDHeader),
		strings.EqualFold(name, RuntimePrincipalKindHeader),
		strings.EqualFold(name, RuntimePermissionScopeHeader),
		strings.EqualFold(name, RuntimeExecutionSourceHeader),
		strings.EqualFold(name, RuntimeResponsibilityContextIDHeader),
		strings.EqualFold(name, RuntimeResponsibilityIDHeader),
		strings.EqualFold(name, RuntimeResponsibilityVersionHeader),
		strings.EqualFold(name, RuntimeResponsibilityPolicyIDHeader),
		strings.EqualFold(name, RuntimeResponsibilityPolicyVersionHeader),
		strings.EqualFold(name, RuntimeResponsibilityAgentProfileHeader),
		strings.EqualFold(name, RuntimeResponsibilityAllowedToolsHeader),
		strings.EqualFold(name, RuntimeResponsibilityAllowedDataHeader),
		strings.EqualFold(name, RuntimeResponsibilityPolicyToolScopesHeader),
		strings.EqualFold(name, RuntimeResponsibilityPolicyDataScopesHeader),
		strings.EqualFold(name, RuntimeResponsibilityTaskScopesHeader),
		strings.EqualFold(name, RuntimeResponsibilityRequireExplicitToolScopesHeader),
		strings.EqualFold(name, RuntimeExecutionEnvelopeIDHeader),
		strings.EqualFold(name, RuntimeExecutionEnvelopeDigestHeader):
		return true
	default:
		return false
	}
}

func appendResponsibilityHeader(headers map[string]string, key string, values []string) {
	clean := make([]string, 0, len(values))
	seen := map[string]bool{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || strings.ContainsAny(value, ",\r\n") || seen[value] {
			continue
		}
		seen[value] = true
		clean = append(clean, value)
	}
	if len(clean) > 0 {
		headers[key] = strings.Join(clean, ",")
	}
}

func positiveInt64String(value int64) string {
	if value < 1 {
		return ""
	}
	return strconv.FormatInt(value, 10)
}
