package tool

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRegistryDiscovererFindsInternetMCPServers(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v0/servers" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
  "servers": [
    {
      "id": "io.github.github-mcp-server",
      "name": "GitHub MCP Server",
      "description": "GitHub integration",
      "version": "1.1.0",
      "packages": [
        {
          "registry_type": "npm",
          "identifier": "@modelcontextprotocol/server-github",
          "version": "1.1.0",
          "runtime_hint": "npx"
        }
      ],
      "tools": ["search_issues"],
      "env": [{"name": "GITHUB_TOKEN", "secret_ref": "github-token"}],
      "homepage": "https://github.com/modelcontextprotocol/servers"
    }
  ]
}`))
	}))
	defer server.Close()

	discoverer := NewRegistryDiscoverer(server.URL, server.Client())
	results, err := discoverer.Search(context.Background(), DiscoverRequest{Query: "github"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("results = %+v", results)
	}
	if results[0].Packages[0].Identifier != "@modelcontextprotocol/server-github" {
		t.Fatalf("package metadata = %+v", results[0].Packages)
	}
}

func TestRegistryDiscovererSupportsRegistryItemsAndPagination(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Query().Get("cursor") {
		case "":
			_, _ = w.Write([]byte(`{
  "items": [
    {"id":"io.github.one","name":"One","packages":[{"registry_type":"npm","identifier":"@one/mcp"}]}
  ],
  "next_cursor": "page-2"
}`))
		case "page-2":
			_, _ = w.Write([]byte(`{
  "items": [
    {"id":"io.github.two","name":"Two","packages":[{"registry_type":"docker","identifier":"ghcr.io/two/mcp"}]}
  ]
}`))
		default:
			t.Fatalf("unexpected cursor: %s", r.URL.Query().Get("cursor"))
		}
	}))
	defer server.Close()

	discoverer := NewRegistryDiscoverer(server.URL, server.Client())
	results, err := discoverer.Search(context.Background(), DiscoverRequest{Query: "mcp", Limit: 5})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 2 || requests != 2 {
		t.Fatalf("results=%+v requests=%d", results, requests)
	}
}

func TestRegistryDiscovererRejectsIncompleteServers(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"items":[{"id":"broken","name":"Broken"}]}`))
	}))
	defer server.Close()

	discoverer := NewRegistryDiscoverer(server.URL, server.Client())
	_, err := discoverer.Search(context.Background(), DiscoverRequest{Query: "broken"})
	if err == nil {
		t.Fatalf("expected incomplete registry server error")
	}
}

func TestDiscoveredMCPServerMapsPackageToDefinition(t *testing.T) {
	candidate := DiscoveredMCPServer{
		ID:          "io.github.github-mcp-server",
		Name:        "GitHub MCP Server",
		Description: "GitHub integration",
		Version:     "1.1.0",
		Packages: []MCPPackage{{
			RegistryType: "npm",
			Identifier:   "@modelcontextprotocol/server-github",
			Version:      "1.1.0",
			RuntimeHint:  "npx",
		}},
		Tools: []string{"search_issues"},
		Env:   []DiscoveredEnvSecret{{Name: "GITHUB_TOKEN", SecretRef: "github-token"}},
	}

	definition, err := candidate.ToToolDefinition("mcp.github")
	if err != nil {
		t.Fatalf("ToToolDefinition: %v", err)
	}
	if definition.ID != "mcp.github" || definition.MCP.Command != "npx" {
		t.Fatalf("definition = %+v", definition)
	}
	if len(definition.MCP.Args) != 2 || definition.MCP.Args[1] != "@modelcontextprotocol/server-github" {
		t.Fatalf("args = %+v", definition.MCP.Args)
	}
	if definition.SecretRefs[0].Name != "github-token" || definition.SecretRefs[0].Env != "GITHUB_TOKEN" {
		t.Fatalf("secret refs = %+v", definition.SecretRefs)
	}
}
