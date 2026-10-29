//go:build mcp_sdk

package tool

import mcp "github.com/modelcontextprotocol/go-sdk/mcp"

func (b MCPSDKBridge) SDKImplementation(definition ToolDefinition) *mcp.Implementation {
	metadata := b.ImplementationMetadata(definition)
	return &mcp.Implementation{
		Name:    metadata.Name,
		Title:   definition.Name,
		Version: metadata.Version,
	}
}
