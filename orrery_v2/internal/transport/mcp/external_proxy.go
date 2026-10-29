package mcp

import (
	"context"
	"fmt"

	toolres "orrery_v2/internal/resource/tool"
)

type ExternalMCPClient interface {
	CallTool(ctx context.Context, req ExternalMCPCallRequest) (CallToolResult, error)
}

type ExternalMCPCallRequest struct {
	Server             toolres.ToolDefinition
	DownstreamToolName string
	PrincipalID        string
	Permissions        []string
	Arguments          map[string]any
	RequestMeta        map[string]any
}

type NotConfiguredExternalMCPClient struct{}

func (NotConfiguredExternalMCPClient) CallTool(ctx context.Context, req ExternalMCPCallRequest) (CallToolResult, error) {
	if err := ctx.Err(); err != nil {
		return CallToolResult{}, err
	}
	serverID := string(req.Server.ID)
	if serverID == "" {
		serverID = "unknown"
	}
	return CallToolResult{}, fmt.Errorf("%w: external MCP runtime is not configured for %s.%s", ErrServiceDependency, serverID, req.DownstreamToolName)
}
