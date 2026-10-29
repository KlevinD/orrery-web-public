package session

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"unicode"

	agentcore "orrery_v2/internal/agent/core"
)

const (
	maxRuntimeMCPConfigBytes = 256 * 1024
	maxRuntimeMCPServers     = 32
)

// RuntimeMCPBinding keeps launch material behind WorkspaceBroker. Spec is the
// only portion allowed to cross the canonical SessionConfig boundary.
type RuntimeMCPBinding struct {
	Spec              agentcore.MCPServerSpec
	Command           string
	Args              []string
	Environment       map[string]string
	URL               string
	Headers           map[string]string
	BearerTokenEnvVar string
}

type runtimeMCPConfig struct {
	MCPServers map[string]runtimeMCPServer `json:"mcpServers"`
	Servers    map[string]runtimeMCPServer `json:"servers"`
}

type runtimeMCPServer struct {
	Name              string            `json:"name"`
	Transport         string            `json:"transport"`
	Type              string            `json:"type"`
	URL               string            `json:"url"`
	Endpoint          string            `json:"endpoint"`
	Headers           map[string]string `json:"headers"`
	HTTPHeaders       map[string]string `json:"http_headers"`
	BearerTokenEnvVar string            `json:"bearer_token_env_var"`
	CommandRef        string            `json:"command_ref"`
	Command           string            `json:"command"`
	Args              []string          `json:"args"`
	Environment       map[string]string `json:"env"`
}

func runtimeMCPBindings(request agentcore.RuntimeRequest) ([]RuntimeMCPBinding, error) {
	raw := request.RuntimeOptions.MCPConfig
	if len(raw) > maxRuntimeMCPConfigBytes {
		return nil, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeInvalidPayload, "runtime MCP configuration exceeds the size limit", false)
	}
	servers := make(map[string]runtimeMCPServer)
	if len(raw) != 0 {
		var config runtimeMCPConfig
		if err := json.Unmarshal(raw, &config); err != nil {
			return nil, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeInvalidPayload, "runtime MCP configuration is invalid", false)
		}
		for name, server := range config.MCPServers {
			servers[name] = server
		}
		for name, server := range config.Servers {
			if _, exists := servers[name]; !exists {
				servers[name] = server
			}
		}
		if len(servers) == 0 {
			var single runtimeMCPServer
			if err := json.Unmarshal(raw, &single); err == nil && (strings.TrimSpace(single.URL) != "" || strings.TrimSpace(single.Endpoint) != "" || strings.TrimSpace(single.Command) != "") {
				servers[firstNonBlank(single.Name, "orrery")] = single
			}
		}
	} else if endpoint := firstEnvironmentValue(request.RuntimeOptions.Environment, "ORRERY_RUNTIME_MCP_URL", "ORRERY_MCP_URL"); endpoint != "" {
		name := firstEnvironmentValue(request.RuntimeOptions.Environment, "ORRERY_RUNTIME_MCP_SERVER_NAME", "ORRERY_MCP_SERVER_NAME")
		if name == "" {
			name = "orrery"
		}
		headers := map[string]string{}
		if authorization := firstEnvironmentValue(request.RuntimeOptions.Environment, "ORRERY_RUNTIME_MCP_AUTHORIZATION", "ORRERY_MCP_AUTHORIZATION"); authorization != "" {
			headers["Authorization"] = authorization
		} else if token := firstEnvironmentValue(request.RuntimeOptions.Environment, "ORRERY_RUNTIME_MCP_TOKEN", "ORRERY_MCP_TOKEN"); token != "" {
			headers["Authorization"] = "Bearer " + token
		}
		headers[agentcore.RuntimeRunIDHeader] = request.RunID.String()
		headers["X-Agent-ID"] = request.AgentID.String()
		headers["X-Orrery-Tenant"] = strings.TrimSpace(request.TenantID)
		for key, value := range agentcore.RuntimeExecutionPrincipalHeaders(request.ExecutionPrincipal, request.TenantID, request.AgentID) {
			headers[key] = value
		}
		for key, value := range agentcore.ExecutionEnvelopeHeaders(request.ExecutionEnvelope) {
			headers[key] = value
		}
		for key, value := range agentcore.ResponsibilityMCPHeaders(request.ContextPack.ResponsibilityContext) {
			headers[key] = value
		}
		servers[name] = runtimeMCPServer{Transport: firstEnvironmentValue(request.RuntimeOptions.Environment, "ORRERY_RUNTIME_MCP_TRANSPORT", "ORRERY_MCP_TRANSPORT"), URL: endpoint, Headers: headers}
	}
	expectedURL := firstEnvironmentValue(request.RuntimeOptions.Environment, "ORRERY_RUNTIME_MCP_URL", "ORRERY_MCP_URL")
	if expectedURL != "" {
		for name, server := range servers {
			if isResponsibilityOrreryMCPServer(server, expectedURL) {
				servers[name] = bindRuntimeOwnedMCPServerHeaders(server, request)
			}
		}
	}
	if request.ContextPack.ResponsibilityContext != nil && len(servers) > 0 {
		if len(servers) != 1 {
			return nil, responsibilityMCPBindingError()
		}
		if expectedURL == "" {
			return nil, responsibilityMCPBindingError()
		}
		for name, server := range servers {
			if !isResponsibilityOrreryMCPServer(server, expectedURL) {
				return nil, responsibilityMCPBindingError()
			}
			if server.Headers == nil {
				server.Headers = map[string]string{}
			}
			for key, value := range runtimeResponsibilityMCPHeaders(request) {
				server.Headers[key] = value
			}
			servers[name] = server
		}
	}
	if len(servers) > maxRuntimeMCPServers {
		return nil, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeInvalidPayload, "runtime MCP server count exceeds the limit", false)
	}
	names := make([]string, 0, len(servers))
	for name := range servers {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]RuntimeMCPBinding, 0, len(names))
	for index, name := range names {
		binding, err := normalizeRuntimeMCPBinding(name, servers[name], index)
		if err != nil {
			return nil, err
		}
		out = append(out, binding)
	}
	return out, nil
}

func runtimeResponsibilityMCPHeaders(request agentcore.RuntimeRequest) map[string]string {
	return agentcore.RuntimeMCPAuthorityHeaders(request)
}

func bindRuntimeOwnedMCPServerHeaders(server runtimeMCPServer, request agentcore.RuntimeRequest) runtimeMCPServer {
	server.Headers = cloneStringMap(server.Headers)
	if server.Headers == nil {
		server.Headers = map[string]string{}
	}
	server.HTTPHeaders = cloneStringMap(server.HTTPHeaders)
	stripRuntimeMCPAuthorityHeaders(server.Headers)
	stripRuntimeMCPAuthorityHeaders(server.HTTPHeaders)
	for key, value := range agentcore.RuntimeMCPAuthorityHeaders(request) {
		server.Headers[key] = value
	}
	return server
}

func stripRuntimeMCPAuthorityHeaders(headers map[string]string) {
	for key := range headers {
		if agentcore.IsRuntimeMCPAuthorityHeader(strings.TrimSpace(key)) {
			delete(headers, key)
		}
	}
}

func isResponsibilityOrreryMCPServer(server runtimeMCPServer, expectedURL string) bool {
	endpoint := strings.TrimSpace(firstNonBlank(server.URL, server.Endpoint))
	if endpoint == "" || expectedURL == "" {
		return false
	}
	return strings.TrimRight(endpoint, "/") == strings.TrimRight(expectedURL, "/")
}

func responsibilityMCPBindingError() error {
	return agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeInvalidPayload, "responsibility runs may use only the server-owned Orrery MCP gateway", false)
}

func normalizeRuntimeMCPBinding(name string, server runtimeMCPServer, index int) (RuntimeMCPBinding, error) {
	name = strings.TrimSpace(firstNonBlank(name, server.Name))
	if name == "" {
		return RuntimeMCPBinding{}, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeInvalidPayload, "runtime MCP server name is required", false)
	}
	endpoint := strings.TrimSpace(firstNonBlank(server.URL, server.Endpoint))
	transport := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(firstNonBlank(server.Transport, server.Type)), "-", "_"))
	if transport == "" {
		if endpoint != "" {
			transport = "http"
		} else {
			transport = "stdio"
		}
	}
	binding := RuntimeMCPBinding{URL: endpoint, Command: strings.TrimSpace(server.Command), Args: append([]string(nil), server.Args...), Environment: cloneStringMap(server.Environment), BearerTokenEnvVar: strings.TrimSpace(server.BearerTokenEnvVar)}
	switch transport {
	case "stdio":
		commandRef := strings.TrimSpace(server.CommandRef)
		if commandRef == "" {
			commandRef = fmt.Sprintf("mcp-command-%s-%d", opaqueMCPSegment(name), index+1)
		}
		binding.Spec = agentcore.MCPServerSpec{Name: name, Transport: agentcore.MCPTransportStdio, CommandRef: commandRef}
		if binding.Command == "" {
			return RuntimeMCPBinding{}, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeInvalidPayload, "runtime stdio MCP binding requires a server-owned command", false)
		}
	case "http", "stream_http", "streamable_http", "streamablehttp", "sse":
		kind := agentcore.MCPTransportHTTP
		if transport == "sse" {
			kind = agentcore.MCPTransportSSE
		}
		headers := cloneStringMap(server.HTTPHeaders)
		if headers == nil {
			headers = make(map[string]string)
		}
		for header, value := range server.Headers {
			headers[strings.TrimSpace(header)] = value
		}
		for header := range headers {
			if strings.TrimSpace(header) == "" {
				delete(headers, header)
			}
		}
		headerNames := make([]string, 0, len(headers))
		for header := range headers {
			headerNames = append(headerNames, header)
		}
		sort.Strings(headerNames)
		headerRefs := make(map[string]string, len(headerNames))
		for headerIndex, header := range headerNames {
			headerRefs[header] = fmt.Sprintf("mcp-header-%s-%d-%d", opaqueMCPSegment(name), index+1, headerIndex+1)
		}
		binding.Headers = headers
		binding.Spec = agentcore.MCPServerSpec{Name: name, Transport: kind, Endpoint: endpoint, HeaderRefs: headerRefs}
	default:
		return RuntimeMCPBinding{}, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeInvalidPayload, "runtime MCP transport is unsupported", false)
	}
	if err := (agentcore.SessionConfig{WorkspaceRef: "workspace-ref", MCPServers: []agentcore.MCPServerSpec{binding.Spec}}).Validate(); err != nil {
		return RuntimeMCPBinding{}, err
	}
	return binding, nil
}

func cloneRuntimeMCPBindings(source []RuntimeMCPBinding) []RuntimeMCPBinding {
	out := make([]RuntimeMCPBinding, len(source))
	for index, binding := range source {
		out[index] = binding
		out[index].Spec.HeaderRefs = cloneStringMap(binding.Spec.HeaderRefs)
		out[index].Args = append([]string(nil), binding.Args...)
		out[index].Environment = cloneStringMap(binding.Environment)
		out[index].Headers = cloneStringMap(binding.Headers)
	}
	return out
}

func opaqueMCPSegment(value string) string {
	var builder strings.Builder
	for _, char := range strings.ToLower(strings.TrimSpace(value)) {
		if unicode.IsLetter(char) || unicode.IsDigit(char) || char == '-' || char == '_' {
			builder.WriteRune(char)
		} else {
			builder.WriteByte('-')
		}
	}
	if builder.Len() == 0 {
		return "server"
	}
	return builder.String()
}

func firstEnvironmentValue(environment map[string]string, names ...string) string {
	for _, name := range names {
		if value := strings.TrimSpace(environment[name]); value != "" {
			return value
		}
	}
	return ""
}
