package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	agentcore "orrery_v2/internal/agent/core"
)

type DiscoverRequest struct {
	Query  string
	Limit  int
	Cursor string
}

type Discoverer interface {
	Search(ctx context.Context, req DiscoverRequest) ([]DiscoveredMCPServer, error)
}

type DiscoveredMCPServer struct {
	ID          string                `json:"id"`
	Name        string                `json:"name"`
	Description string                `json:"description"`
	Version     string                `json:"version"`
	Packages    []MCPPackage          `json:"packages"`
	Tools       []string              `json:"tools"`
	Env         []DiscoveredEnvSecret `json:"env"`
	Homepage    string                `json:"homepage"`
}

type MCPPackage struct {
	RegistryType string `json:"registry_type"`
	Identifier   string `json:"identifier"`
	Version      string `json:"version"`
	RuntimeHint  string `json:"runtime_hint"`
}

type DiscoveredEnvSecret struct {
	Name      string `json:"name"`
	SecretRef string `json:"secret_ref"`
}

type RegistryDiscoverer struct {
	baseURL string
	client  *http.Client
}

type registrySearchResponse struct {
	Servers    []DiscoveredMCPServer `json:"servers"`
	Items      []DiscoveredMCPServer `json:"items"`
	NextCursor string                `json:"next_cursor"`
	Cursor     string                `json:"cursor"`
}

func NewRegistryDiscoverer(baseURL string, client *http.Client) *RegistryDiscoverer {
	if client == nil {
		client = http.DefaultClient
	}
	return &RegistryDiscoverer{baseURL: strings.TrimRight(baseURL, "/"), client: client}
}

func (d *RegistryDiscoverer) Search(ctx context.Context, req DiscoverRequest) ([]DiscoveredMCPServer, error) {
	limit := req.Limit
	if limit <= 0 {
		limit = 25
	}
	var cursor = strings.TrimSpace(req.Cursor)
	var out []DiscoveredMCPServer
	for {
		page, nextCursor, err := d.searchPage(ctx, req.Query, limit-len(out), cursor)
		if err != nil {
			return nil, err
		}
		for _, candidate := range page {
			if err := validateDiscoveredServer(candidate); err != nil {
				return nil, err
			}
			out = append(out, candidate)
			if len(out) >= limit {
				return out, nil
			}
		}
		if strings.TrimSpace(nextCursor) == "" {
			return out, nil
		}
		cursor = nextCursor
	}
}

func (d *RegistryDiscoverer) searchPage(ctx context.Context, search string, limit int, cursor string) ([]DiscoveredMCPServer, string, error) {
	endpoint, err := url.Parse(d.baseURL + "/v0/servers")
	if err != nil {
		return nil, "", err
	}
	query := endpoint.Query()
	if strings.TrimSpace(search) != "" {
		query.Set("q", search)
	}
	if limit > 0 {
		query.Set("limit", fmt.Sprintf("%d", limit))
	}
	if strings.TrimSpace(cursor) != "" {
		query.Set("cursor", cursor)
	}
	endpoint.RawQuery = query.Encode()
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, "", err
	}
	resp, err := d.client.Do(httpReq)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, "", fmt.Errorf("mcp registry returned status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var decoded registrySearchResponse
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return nil, "", fmt.Errorf("decode mcp registry response: %w", err)
	}
	servers := decoded.Servers
	if len(servers) == 0 && len(decoded.Items) > 0 {
		servers = decoded.Items
	}
	nextCursor := decoded.NextCursor
	if nextCursor == "" {
		nextCursor = decoded.Cursor
	}
	return servers, nextCursor, nil
}

func (s DiscoveredMCPServer) ToToolDefinition(toolID string) (ToolDefinition, error) {
	if err := validateDiscoveredServer(s); err != nil {
		return ToolDefinition{}, err
	}
	if strings.TrimSpace(toolID) == "" {
		toolID = "mcp." + safeMCPID(s.Name)
	}
	version := strings.TrimSpace(s.Version)
	if version == "" && len(s.Packages) > 0 {
		version = s.Packages[0].Version
	}
	if version == "" {
		version = "1.0.0"
	}
	command, args := packageCommand(s.Packages)
	definition := ToolDefinition{
		ID:          agentcore.ToolID(toolID),
		Kind:        KindMCPServer,
		Name:        s.Name,
		Description: s.Description,
		Version:     version,
		Enabled:     true,
		RiskLevel:   RiskMedium,
		RuntimeRequirements: RuntimeRequirements{
			Packages: packageIDs(s.Packages),
			Notes:    s.Homepage,
		},
		MCP: &MCPServerDefinition{
			ServerName:   safeMCPID(s.Name),
			Transport:    TransportStdio,
			Command:      command,
			Args:         args,
			ExposedTools: append([]string(nil), s.Tools...),
			HealthCheck:  HealthCheckPolicy{Mode: HealthCheckStartup},
		},
		Tags: []string{"mcp", "discovered"},
	}
	for _, env := range s.Env {
		refName := strings.TrimSpace(env.SecretRef)
		if refName == "" {
			refName = "mcp." + safeMCPID(s.Name) + "." + strings.ToLower(env.Name)
		}
		ref := SecretRef{Name: refName, Env: env.Name}
		definition.SecretRefs = append(definition.SecretRefs, ref)
		definition.MCP.EnvSecretRefs = append(definition.MCP.EnvSecretRefs, ref)
	}
	return definition, nil
}

func validateDiscoveredServer(server DiscoveredMCPServer) error {
	if strings.TrimSpace(server.ID) == "" {
		return fmt.Errorf("%w: discovered mcp id is required", ErrInvalidDefinition)
	}
	if strings.TrimSpace(server.Name) == "" {
		return fmt.Errorf("%w: discovered mcp name is required", ErrInvalidDefinition)
	}
	if len(server.Packages) == 0 {
		return fmt.Errorf("%w: discovered mcp %s has no installable packages", ErrInvalidDefinition, server.ID)
	}
	for _, pkg := range server.Packages {
		if strings.TrimSpace(pkg.Identifier) != "" {
			return nil
		}
	}
	return fmt.Errorf("%w: discovered mcp %s has no package identifier", ErrInvalidDefinition, server.ID)
}

func packageCommand(packages []MCPPackage) (string, []string) {
	if len(packages) == 0 {
		return "", nil
	}
	pkg := packages[0]
	switch strings.ToLower(pkg.RegistryType) {
	case "npm":
		command := pkg.RuntimeHint
		if command == "" {
			command = "npx"
		}
		return command, []string{"-y", pkg.Identifier}
	case "docker":
		return "docker", []string{"run", "--rm", pkg.Identifier}
	default:
		if pkg.RuntimeHint != "" {
			return pkg.RuntimeHint, []string{pkg.Identifier}
		}
		return pkg.Identifier, nil
	}
}

func packageIDs(packages []MCPPackage) []string {
	out := make([]string, 0, len(packages))
	for _, pkg := range packages {
		if pkg.Identifier != "" {
			out = append(out, pkg.RegistryType+":"+pkg.Identifier+"@"+pkg.Version)
		}
	}
	return out
}
