package tool

import "testing"

func TestOfficialMCPSDKBridgeMetadata(t *testing.T) {
	bridge := OfficialMCPSDKBridge()
	if bridge.ModulePath != "github.com/modelcontextprotocol/go-sdk/mcp" {
		t.Fatalf("module path = %q", bridge.ModulePath)
	}
	definition := ToolDefinition{
		ID:      "mcp.github",
		Kind:    KindMCPServer,
		Name:    "GitHub MCP",
		Version: "1.0.0",
		MCP:     &MCPServerDefinition{ServerName: "github"},
	}
	implementation := bridge.ImplementationMetadata(definition)
	if implementation.Name != "github" || implementation.Version != "1.0.0" {
		t.Fatalf("implementation metadata = %+v", implementation)
	}
}
