package tool

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestRegisterMCPConfigLoadsServersWithoutSecretValues(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "mcp.json")
	config := `{
  "mcpServers": {
    "github": {
      "command": "github-mcp-server",
      "args": ["stdio"],
      "env": {
        "GITHUB_TOKEN": "github-token-fixture-not-stored"
      }
    }
  }
}`
	if err := os.WriteFile(path, []byte(config), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	registry := NewMemoryRegistry()

	loaded, err := RegisterMCPConfig(ctx, registry, path)
	if err != nil {
		t.Fatalf("RegisterMCPConfig: %v", err)
	}
	if loaded != 1 {
		t.Fatalf("loaded = %d, want 1", loaded)
	}
	definition, err := registry.Get(ctx, "mcp.github")
	if err != nil {
		t.Fatalf("get github mcp: %v", err)
	}
	if definition.MCP == nil || definition.MCP.Command != "github-mcp-server" {
		t.Fatalf("mcp definition = %+v", definition.MCP)
	}
	if len(definition.SecretRefs) != 1 || definition.SecretRefs[0].Env != "GITHUB_TOKEN" {
		t.Fatalf("secret refs = %+v", definition.SecretRefs)
	}
	if definition.SecretRefs[0].Name == "github-token-fixture-not-stored" {
		t.Fatalf("secret value leaked into definition: %+v", definition.SecretRefs)
	}
}

func TestParseMCPConfigDefinitionsPreservesHTTPHeaders(t *testing.T) {
	ctx := context.Background()
	raw := []byte(`{
  "mcpServers": {
    "yunji-zhaopin": {
      "url": "http://127.0.0.1:5000/api/mcp",
      "headers": {
        "Authorization": "Bearer sk_test",
        "X-Tenant": "demo"
      }
    }
  }
}`)

	definitions, err := ParseMCPConfigDefinitions(ctx, raw, MCPConfigParseOptions{IDPrefix: "mcp"})
	if err != nil {
		t.Fatalf("ParseMCPConfigDefinitions: %v", err)
	}
	if len(definitions) != 1 || definitions[0].MCP == nil {
		t.Fatalf("definitions = %+v", definitions)
	}
	headers := definitions[0].MCP.Headers
	if headers["Authorization"] != "Bearer sk_test" || headers["X-Tenant"] != "demo" {
		t.Fatalf("headers = %+v", headers)
	}
}

func TestParseMCPConfigDefinitionsAcceptsCommonClientJSONShapes(t *testing.T) {
	ctx := context.Background()
	raw := []byte(`{
  "mcpServers": {
    "github": {
      "command": "github-mcp-server",
      "args": ["stdio"],
      "env": {
        "GITHUB_TOKEN": "github-token-fixture-not-stored"
      }
    }
  },
  "servers": {
    "linear": {
      "url": "https://mcp.linear.test/sse",
      "transport": "sse",
      "env": {
        "LINEAR_API_KEY": "${input:linear-token}"
      }
    }
  }
}`)

	definitions, err := ParseMCPConfigDefinitions(ctx, raw, MCPConfigParseOptions{IDPrefix: "mcp"})
	if err != nil {
		t.Fatalf("ParseMCPConfigDefinitions: %v", err)
	}
	if len(definitions) != 2 {
		t.Fatalf("definitions = %+v", definitions)
	}
	github := definitions[0]
	if github.ID != "mcp.github" || github.MCP == nil || github.MCP.Command != "github-mcp-server" || github.MCP.Transport != TransportStdio {
		t.Fatalf("github definition = %+v", github)
	}
	if len(github.SecretRefs) != 1 || github.SecretRefs[0].Env != "GITHUB_TOKEN" || github.SecretRefs[0].Name != "mcp.github.github_token" {
		t.Fatalf("github secret refs = %+v", github.SecretRefs)
	}
	linear := definitions[1]
	if linear.ID != "mcp.linear" || linear.MCP == nil || linear.MCP.URL != "https://mcp.linear.test/sse" || linear.MCP.Transport != TransportSSE {
		t.Fatalf("linear definition = %+v", linear)
	}
	if len(linear.SecretRefs) != 1 || linear.SecretRefs[0].Env != "LINEAR_API_KEY" {
		t.Fatalf("linear secret refs = %+v", linear.SecretRefs)
	}
}

func TestParseMCPConfigDefinitionsAcceptsSingleServerJSON(t *testing.T) {
	ctx := context.Background()
	raw := []byte(`{
  "name": "playwright",
  "command": "npx",
  "args": ["@playwright/mcp@latest"],
  "env": {
    "PLAYWRIGHT_BASE_URL": "https://example.test"
  }
}`)

	definitions, err := ParseMCPConfigDefinitions(ctx, raw, MCPConfigParseOptions{DefaultServerName: "playwright"})
	if err != nil {
		t.Fatalf("ParseMCPConfigDefinitions: %v", err)
	}
	if len(definitions) != 1 {
		t.Fatalf("definitions = %+v", definitions)
	}
	definition := definitions[0]
	if definition.ID != "mcp.playwright" || definition.Name != "playwright" || definition.MCP == nil {
		t.Fatalf("definition = %+v", definition)
	}
	if definition.MCP.Command != "npx" || len(definition.MCP.Args) != 1 || definition.MCP.Args[0] != "@playwright/mcp@latest" {
		t.Fatalf("mcp command = %+v", definition.MCP)
	}
}
