package execenv

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

type mcpInjectionConfig struct {
	MCPServers map[string]mcpInjectionServer `json:"mcpServers"`
	Servers    map[string]mcpInjectionServer `json:"servers"`
}

type mcpInjectionServer struct {
	Name              string            `json:"name"`
	Transport         string            `json:"transport"`
	Type              string            `json:"type"`
	URL               string            `json:"url"`
	Endpoint          string            `json:"endpoint"`
	Headers           map[string]string `json:"headers"`
	HTTPHeaders       map[string]string `json:"http_headers"`
	BearerTokenEnvVar string            `json:"bearer_token_env_var"`
}

type normalizedMCPServer struct {
	Name              string
	Transport         string
	URL               string
	Headers           map[string]string
	BearerTokenEnvVar string
}

func applyMCPInjection(env Environment, runtime string, raw json.RawMessage) (Environment, error) {
	servers, err := parseMCPInjection(raw)
	if err != nil {
		return env, err
	}
	if len(servers) == 0 {
		return env, nil
	}
	switch strings.ToLower(strings.TrimSpace(runtime)) {
	case "codex":
		path, err := writeCodexMCPConfig(env.RuntimeHome, servers)
		if err != nil {
			return env, err
		}
		env.MCPConfigPath = path
	case "claude":
		path, err := writeClaudeMCPConfig(env.WorkDir, env.RuntimeHome, servers)
		if err != nil {
			return env, err
		}
		env.MCPConfigPath = path
	case "openclaw":
		path := env.RuntimeEnv["OPENCLAW_CONFIG_PATH"]
		if strings.TrimSpace(path) == "" {
			return env, fmt.Errorf("openclaw config path is required before MCP injection")
		}
		if err := injectOpenClawMCPConfig(path, servers); err != nil {
			return env, err
		}
		env.MCPConfigPath = path
	case "pi":
		path := filepath.Join(env.RuntimeHome, "mcp_config.json")
		if err := os.WriteFile(path, raw, 0o600); err != nil {
			return env, fmt.Errorf("write pi mcp config: %w", err)
		}
		env.MCPConfigPath = path
	default:
		path := filepath.Join(env.WorkDir, "mcp_config.json")
		if err := os.WriteFile(path, raw, 0o600); err != nil {
			return env, fmt.Errorf("write mcp config: %w", err)
		}
		env.MCPConfigPath = path
	}
	return env, nil
}

func parseMCPInjection(raw json.RawMessage) ([]normalizedMCPServer, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var cfg mcpInjectionConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return nil, fmt.Errorf("parse mcp injection config: %w", err)
	}
	servers := map[string]mcpInjectionServer{}
	for name, server := range cfg.MCPServers {
		servers[name] = server
	}
	for name, server := range cfg.Servers {
		if _, exists := servers[name]; !exists {
			servers[name] = server
		}
	}
	if len(servers) == 0 {
		var single mcpInjectionServer
		if err := json.Unmarshal(raw, &single); err != nil {
			return nil, fmt.Errorf("parse mcp injection config: %w", err)
		}
		if strings.TrimSpace(single.URL) != "" || strings.TrimSpace(single.Endpoint) != "" {
			servers[firstNonBlank(single.Name, "orrery")] = single
		}
	}
	names := make([]string, 0, len(servers))
	for name := range servers {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]normalizedMCPServer, 0, len(names))
	for _, name := range names {
		server, err := normalizeMCPInjectionServer(name, servers[name])
		if err != nil {
			return nil, err
		}
		out = append(out, server)
	}
	return out, nil
}

func normalizeMCPInjectionServer(name string, server mcpInjectionServer) (normalizedMCPServer, error) {
	name = strings.TrimSpace(firstNonBlank(name, server.Name, "orrery"))
	url := strings.TrimSpace(firstNonBlank(server.URL, server.Endpoint))
	transport, err := normalizeMCPTransport(firstNonBlank(server.Transport, server.Type), url)
	if err != nil {
		return normalizedMCPServer{}, fmt.Errorf("mcp server %q: %w", name, err)
	}
	if url == "" {
		return normalizedMCPServer{}, fmt.Errorf("mcp server %q requires url", name)
	}
	headers := map[string]string{}
	for key, value := range server.HTTPHeaders {
		if strings.TrimSpace(key) != "" {
			headers[strings.TrimSpace(key)] = value
		}
	}
	for key, value := range server.Headers {
		if strings.TrimSpace(key) != "" {
			headers[strings.TrimSpace(key)] = value
		}
	}
	return normalizedMCPServer{
		Name:              name,
		Transport:         transport,
		URL:               url,
		Headers:           headers,
		BearerTokenEnvVar: strings.TrimSpace(server.BearerTokenEnvVar),
	}, nil
}

func normalizeMCPTransport(raw, url string) (string, error) {
	value := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(raw), "-", "_"))
	switch value {
	case "", "http", "stream_http", "streamable_http", "streamablehttp":
		return "streamable-http", nil
	case "sse":
		return "sse", nil
	case "stdio":
		return "", fmt.Errorf("stdio transport is not supported for local runtime MCP injection")
	default:
		if strings.TrimSpace(url) != "" {
			return "", fmt.Errorf("unsupported transport %q", raw)
		}
		return "", fmt.Errorf("transport is required")
	}
}

func writeCodexMCPConfig(runtimeHome string, servers []normalizedMCPServer) (string, error) {
	base, err := os.ReadFile(filepath.Join(runtimeHome, "config.toml"))
	if err != nil {
		return "", fmt.Errorf("read Codex runtime config for MCP injection: %w", err)
	}
	var b strings.Builder
	b.Write(base)
	if b.Len() > 0 && !strings.HasSuffix(b.String(), "\n") {
		b.WriteString("\n")
	}
	b.WriteString("\n")
	for _, server := range servers {
		fmt.Fprintf(&b, "[mcp_servers.%s]\n", tomlKey(server.Name))
		fmt.Fprintf(&b, "url = %s\n", tomlString(server.URL))
		if server.BearerTokenEnvVar != "" {
			fmt.Fprintf(&b, "bearer_token_env_var = %s\n", tomlString(server.BearerTokenEnvVar))
		}
		if len(server.Headers) > 0 {
			fmt.Fprintf(&b, "http_headers = %s\n", tomlInlineStringMap(server.Headers))
		}
		b.WriteString("default_tools_approval_mode = \"auto\"\n")
		b.WriteString("required = true\n\n")
	}
	path := filepath.Join(runtimeHome, "config.toml")
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		return "", fmt.Errorf("write codex mcp config: %w", err)
	}
	return path, nil
}

func ensureCodexRuntimeConfig(runtimeHome string) error {
	config, err := codexRuntimeConfig(runtimeHome)
	if err != nil {
		return err
	}
	path := filepath.Join(runtimeHome, "config.toml")
	if err := os.WriteFile(path, config, 0o600); err != nil {
		return fmt.Errorf("write codex runtime config: %w", err)
	}
	return nil
}

func codexRuntimeConfig(runtimeHome string) ([]byte, error) {
	config := map[string]any{}
	source, required := codexConfigSource(runtimeHome)
	if source != "" {
		data, err := os.ReadFile(source)
		if err != nil {
			if !os.IsNotExist(err) || required {
				return nil, fmt.Errorf("read Codex configuration file: %w", err)
			}
		} else {
			var inherited map[string]any
			if err := toml.Unmarshal(data, &inherited); err != nil {
				return nil, fmt.Errorf("read Codex configuration file: invalid TOML: %w", err)
			}
			for _, key := range codexInheritedConfigKeys {
				if value, ok := inherited[key]; ok {
					config[key] = value
				}
			}
		}
	}
	config["features"] = map[string]any{"memories": false}
	config["memories"] = map[string]any{
		"generate_memories": false,
		"use_memories":      false,
	}
	data, err := toml.Marshal(config)
	if err != nil {
		return nil, fmt.Errorf("encode Codex runtime config: %w", err)
	}
	return data, nil
}

var codexInheritedConfigKeys = []string{
	"model_provider",
	"model_providers",
	"model",
	"review_model",
	"model_reasoning_effort",
	"model_reasoning_summary",
	"model_verbosity",
	"disable_response_storage",
	"network_access",
}

func codexConfigSource(runtimeHome string) (string, bool) {
	if explicit := strings.TrimSpace(os.Getenv("ORRERY_CODEX_CONFIG_FILE")); explicit != "" {
		return filepath.Clean(explicit), true
	}
	if inheritedHome := strings.TrimSpace(os.Getenv("CODEX_HOME")); inheritedHome != "" {
		candidate := filepath.Join(filepath.Clean(inheritedHome), "config.toml")
		if !sameCleanPath(filepath.Dir(candidate), runtimeHome) {
			return candidate, false
		}
	}
	userHome, err := os.UserHomeDir()
	if err != nil {
		return "", false
	}
	return filepath.Join(userHome, ".codex", "config.toml"), false
}

func writeClaudeMCPConfig(workDir, runtimeHome string, servers []normalizedMCPServer) (string, error) {
	mcpServers := map[string]any{}
	allowedTools := make([]string, 0, len(servers))
	for _, server := range servers {
		entry := map[string]any{
			"type": claudeMCPType(server.Transport),
			"url":  server.URL,
		}
		if len(server.Headers) > 0 {
			entry["headers"] = server.Headers
		}
		mcpServers[server.Name] = entry
		allowedTools = append(allowedTools, "mcp__"+server.Name+"__*")
	}
	sort.Strings(allowedTools)
	mcpPath := filepath.Join(workDir, ".mcp.json")
	if err := writeJSONFile(mcpPath, map[string]any{"mcpServers": mcpServers}); err != nil {
		return "", fmt.Errorf("write claude mcp config: %w", err)
	}
	settingsPath := filepath.Join(runtimeHome, "settings.json")
	settings := map[string]any{
		"settingSources": []string{"user", "project"},
		"allowedTools":   allowedTools,
	}
	if err := writeJSONFile(settingsPath, settings); err != nil {
		return "", fmt.Errorf("write claude settings: %w", err)
	}
	return mcpPath, nil
}

func claudeMCPType(transport string) string {
	if transport == "sse" {
		return "sse"
	}
	return "http"
}

func injectOpenClawMCPConfig(path string, servers []normalizedMCPServer) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read openclaw config for mcp injection: %w", err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return fmt.Errorf("parse openclaw config for mcp injection: %w", err)
	}
	cfg["mcp"] = map[string]any{"servers": openClawMCPServers(servers)}
	return writeJSONFile(path, cfg)
}

func openClawMCPServers(servers []normalizedMCPServer) map[string]any {
	out := map[string]any{}
	for _, server := range servers {
		entry := map[string]any{
			"transport": server.Transport,
			"url":       server.URL,
		}
		if len(server.Headers) > 0 {
			entry["headers"] = server.Headers
		}
		out[server.Name] = entry
	}
	return out
}

func writeJSONFile(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0o600)
}

var bareTOMLKey = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

func tomlKey(value string) string {
	if bareTOMLKey.MatchString(value) {
		return value
	}
	return tomlString(value)
}

func tomlInlineStringMap(values map[string]string) string {
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	sort.Strings(names)
	parts := make([]string, 0, len(names))
	for _, name := range names {
		parts = append(parts, tomlString(name)+" = "+tomlString(values[name]))
	}
	return "{ " + strings.Join(parts, ", ") + " }"
}

func tomlString(value string) string {
	data, _ := json.Marshal(value)
	return string(data)
}

func firstNonBlank(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
