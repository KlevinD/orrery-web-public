package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"

	agentcore "orrery_v2/internal/agent/core"
)

var mcpIDUnsafe = regexp.MustCompile(`[^a-zA-Z0-9_.-]+`)

type mcpConfigFile struct {
	MCPServers map[string]mcpConfigServer `json:"mcpServers"`
	Servers    map[string]mcpConfigServer `json:"servers"`
}

type mcpConfigServer struct {
	Name        string                     `json:"name"`
	Description string                     `json:"description"`
	Version     string                     `json:"version"`
	Transport   string                     `json:"transport"`
	Type        string                     `json:"type"`
	Command     string                     `json:"command"`
	URL         string                     `json:"url"`
	Endpoint    string                     `json:"endpoint"`
	Headers     map[string]string          `json:"headers"`
	Args        []string                   `json:"args"`
	Env         map[string]json.RawMessage `json:"env"`
	Tools       []string                   `json:"tools"`
	Tags        []string                   `json:"tags"`
}

type MCPConfigParseOptions struct {
	IDPrefix          string
	DefaultServerName string
	Version           string
	Tags              []string
}

func RegisterMCPConfig(ctx context.Context, registry Registry, path string) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0, fmt.Errorf("read mcp config: %w", err)
	}
	definitions, err := ParseMCPConfigDefinitions(ctx, raw, MCPConfigParseOptions{
		IDPrefix: "mcp",
		Version:  "1.0.0",
		Tags:     []string{"mcp", "cli-seed"},
	})
	if err != nil {
		return 0, err
	}
	loaded := 0
	for _, definition := range definitions {
		if err := registry.Register(ctx, definition); err != nil {
			return loaded, err
		}
		loaded++
	}
	return loaded, nil
}

func ParseMCPConfigDefinitions(ctx context.Context, raw []byte, opts MCPConfigParseOptions) ([]ToolDefinition, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var config mcpConfigFile
	if err := json.Unmarshal(raw, &config); err != nil {
		return nil, fmt.Errorf("parse mcp config: %w", err)
	}
	servers := map[string]mcpConfigServer{}
	for name, server := range config.MCPServers {
		servers[name] = server
	}
	for name, server := range config.Servers {
		if _, exists := servers[name]; !exists {
			servers[name] = server
		}
	}
	if len(servers) == 0 {
		var server mcpConfigServer
		if err := json.Unmarshal(raw, &server); err != nil {
			return nil, fmt.Errorf("parse mcp config: %w", err)
		}
		if isSingleMCPServerConfig(server) {
			name := firstNonBlank(server.Name, opts.DefaultServerName, inferMCPServerName(server))
			servers[name] = server
		}
	}
	if len(servers) == 0 {
		return nil, fmt.Errorf("parse mcp config: no MCP servers found")
	}
	names := make([]string, 0, len(servers))
	for name := range servers {
		names = append(names, name)
	}
	sort.Strings(names)
	definitions := make([]ToolDefinition, 0, len(names))
	for _, serverName := range names {
		definition, err := mcpDefinitionFromConfigServer(serverName, servers[serverName], opts)
		if err != nil {
			return nil, err
		}
		definitions = append(definitions, definition)
	}
	return definitions, nil
}

func mcpDefinitionFromConfigServer(serverName string, server mcpConfigServer, opts MCPConfigParseOptions) (ToolDefinition, error) {
	serverName = strings.TrimSpace(firstNonBlank(serverName, server.Name, opts.DefaultServerName, inferMCPServerName(server)))
	if serverName == "" {
		return ToolDefinition{}, fmt.Errorf("parse mcp config: server name is required")
	}
	server.URL = strings.TrimSpace(firstNonBlank(server.URL, server.Endpoint))
	transport, err := inferTransport(server)
	if err != nil {
		return ToolDefinition{}, err
	}
	if err := validateMCPConfigServerTarget(serverName, transport, server); err != nil {
		return ToolDefinition{}, err
	}
	version := strings.TrimSpace(firstNonBlank(server.Version, opts.Version, "1.0.0"))
	tags := append([]string(nil), opts.Tags...)
	if len(tags) == 0 {
		tags = []string{"mcp", "json-import"}
	}
	tags = append(tags, server.Tags...)
	definition := ToolDefinition{
		ID:          agentcore.ToolID(mcpConfigToolID(opts.IDPrefix, serverName)),
		Kind:        KindMCPServer,
		Name:        firstNonBlank(server.Name, serverName),
		Description: strings.TrimSpace(server.Description),
		Version:     version,
		Enabled:     true,
		RiskLevel:   RiskMedium,
		MCP: &MCPServerDefinition{
			ServerName:   serverName,
			Transport:    transport,
			Command:      strings.TrimSpace(server.Command),
			URL:          server.URL,
			Headers:      cleanMCPHeaders(server.Headers),
			Args:         append([]string(nil), server.Args...),
			ExposedTools: append([]string(nil), server.Tools...),
			HealthCheck:  HealthCheckPolicy{Mode: HealthCheckStartup},
		},
		Tags: tags,
	}
	for _, envName := range sortedMCPEnvNames(server.Env) {
		secret := SecretRef{Name: "mcp." + safeMCPID(serverName) + "." + strings.ToLower(envName), Env: envName}
		definition.SecretRefs = append(definition.SecretRefs, secret)
		definition.MCP.EnvSecretRefs = append(definition.MCP.EnvSecretRefs, secret)
	}
	return definition, nil
}

func cleanMCPHeaders(headers map[string]string) map[string]string {
	if len(headers) == 0 {
		return nil
	}
	out := make(map[string]string, len(headers))
	for name, value := range headers {
		name = strings.TrimSpace(name)
		value = strings.TrimSpace(value)
		if name == "" || value == "" {
			continue
		}
		out[name] = value
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func inferTransport(server mcpConfigServer) (MCPTransport, error) {
	raw := strings.TrimSpace(firstNonBlank(server.Transport, server.Type))
	switch strings.ToLower(strings.ReplaceAll(raw, "-", "_")) {
	case "":
	case string(TransportStdio):
		return TransportStdio, nil
	case string(TransportSSE):
		return TransportSSE, nil
	case string(TransportStreamHTTP), "http", "stream_http", "streamablehttp":
		return TransportStreamHTTP, nil
	default:
		return "", fmt.Errorf("parse mcp config: unsupported mcp transport %q", raw)
	}
	if strings.TrimSpace(server.URL) != "" {
		return TransportStreamHTTP, nil
	}
	return TransportStdio, nil
}

func safeMCPID(value string) string {
	value = strings.Trim(mcpIDUnsafe.ReplaceAllString(value, "-"), "-")
	if value == "" {
		return "server"
	}
	return strings.ToLower(value)
}

func isSingleMCPServerConfig(server mcpConfigServer) bool {
	return strings.TrimSpace(server.Command) != "" ||
		strings.TrimSpace(server.URL) != "" ||
		strings.TrimSpace(server.Endpoint) != ""
}

func inferMCPServerName(server mcpConfigServer) string {
	command := strings.TrimSpace(server.Command)
	if command == "" {
		return "server"
	}
	parts := strings.Fields(command)
	if len(parts) == 0 {
		return "server"
	}
	return strings.TrimSuffix(parts[0], ".exe")
}

func mcpConfigToolID(prefix string, serverName string) string {
	prefix = strings.TrimSpace(prefix)
	if prefix == "" {
		prefix = "mcp"
	}
	return strings.TrimSuffix(prefix, ".") + "." + safeMCPID(serverName)
}

func validateMCPConfigServerTarget(serverName string, transport MCPTransport, server mcpConfigServer) error {
	switch transport {
	case TransportStdio:
		if strings.TrimSpace(server.Command) == "" {
			return fmt.Errorf("parse mcp config: stdio mcp server %q requires command", serverName)
		}
	case TransportStreamHTTP, TransportSSE:
		if strings.TrimSpace(server.URL) == "" {
			return fmt.Errorf("parse mcp config: %s mcp server %q requires url", transport, serverName)
		}
	default:
		return fmt.Errorf("parse mcp config: unsupported mcp transport %q", transport)
	}
	return nil
}

func sortedMCPEnvNames(env map[string]json.RawMessage) []string {
	names := make([]string, 0, len(env))
	for envName := range env {
		envName = strings.TrimSpace(envName)
		if envName != "" {
			names = append(names, envName)
		}
	}
	sort.Strings(names)
	return names
}

func firstNonBlank(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
