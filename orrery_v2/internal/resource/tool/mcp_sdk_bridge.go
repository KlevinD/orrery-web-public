package tool

const OfficialMCPSDKModulePath = "github.com/modelcontextprotocol/go-sdk/mcp"

type MCPSDKBridge struct {
	ModulePath string
}

type MCPImplementationMetadata struct {
	Name    string
	Version string
}

func OfficialMCPSDKBridge() MCPSDKBridge {
	return MCPSDKBridge{ModulePath: OfficialMCPSDKModulePath}
}

func (b MCPSDKBridge) ImplementationMetadata(definition ToolDefinition) MCPImplementationMetadata {
	name := definition.Name
	if definition.MCP != nil && definition.MCP.ServerName != "" {
		name = definition.MCP.ServerName
	}
	return MCPImplementationMetadata{
		Name:    name,
		Version: definition.Version,
	}
}
