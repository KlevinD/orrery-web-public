package resourceadmin

import (
	"context"
	"testing"

	skillres "orrery_v2/internal/resource/skill"
	toolres "orrery_v2/internal/resource/tool"
)

func TestQueryServiceDiscoversMCPServersForFrontendPicker(t *testing.T) {
	discoverer := fakeDiscoverer{servers: []toolres.DiscoveredMCPServer{{
		ID:          "io.github.linear",
		Name:        "Linear MCP",
		Description: "Linear issues",
		Version:     "1.0.0",
		Packages:    []toolres.MCPPackage{{RegistryType: "npm", Identifier: "@linear/mcp", Version: "1.0.0"}},
		Tools:       []string{"list_issues"},
	}}}
	service := NewQueryServiceWithDiscoverer(toolres.NewMemoryRegistry(), skillres.NewMemoryRegistry(), discoverer)

	results, err := service.DiscoverMCPServers(context.Background(), ViewerRef{
		ViewerID:        "admin",
		PermissionScope: []string{PermissionResourceRead},
	}, DiscoverMCPRequest{Query: "linear"})
	if err != nil {
		t.Fatalf("DiscoverMCPServers: %v", err)
	}
	if len(results.Servers) != 1 || results.Servers[0].PackageRefs[0] != "npm:@linear/mcp@1.0.0" {
		t.Fatalf("results = %+v", results)
	}
}

type fakeDiscoverer struct {
	servers []toolres.DiscoveredMCPServer
}

func (d fakeDiscoverer) Search(ctx context.Context, req toolres.DiscoverRequest) ([]toolres.DiscoveredMCPServer, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return append([]toolres.DiscoveredMCPServer(nil), d.servers...), nil
}
