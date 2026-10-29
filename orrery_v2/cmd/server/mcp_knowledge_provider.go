package main

import (
	"context"
	"fmt"
	"strings"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/knowledgeadmin"
	"orrery_v2/internal/app/knowledgeprovider"
	resourceadmin "orrery_v2/internal/app/resourceadmin"
	toolres "orrery_v2/internal/resource/tool"
	mcptransport "orrery_v2/internal/transport/mcp"
)

type knowledgeProviderToolRegistry interface {
	Get(context.Context, agentcore.ToolID) (toolres.ToolDefinition, error)
}

type knowledgeProviderMCPRuntime interface {
	ListTools(context.Context, toolres.ToolDefinition) (resourceadmin.MCPRuntimeToolList, error)
	CallTool(context.Context, mcptransport.ExternalMCPCallRequest) (mcptransport.CallToolResult, error)
}

type productionKnowledgeProviderMCPPort struct {
	registry knowledgeProviderToolRegistry
	runtime  knowledgeProviderMCPRuntime
}

var _ knowledgeprovider.MCPPort = productionKnowledgeProviderMCPPort{}

func (p productionKnowledgeProviderMCPPort) GetServer(ctx context.Context, id string) (toolres.ToolDefinition, error) {
	if p.registry == nil {
		return toolres.ToolDefinition{}, fmt.Errorf("MCP registry is unavailable")
	}
	definition, err := p.registry.Get(ctx, agentcore.ToolID(strings.TrimSpace(id)))
	if err != nil {
		return toolres.ToolDefinition{}, err
	}
	if definition.Kind != toolres.KindMCPServer || definition.MCP == nil || !definition.Enabled {
		return toolres.ToolDefinition{}, fmt.Errorf("tool %s is not an enabled MCP server", id)
	}
	return definition, nil
}

func (p productionKnowledgeProviderMCPPort) ListTools(ctx context.Context, server toolres.ToolDefinition) ([]toolres.MCPExposedToolDefinition, error) {
	if p.runtime == nil {
		return nil, fmt.Errorf("MCP runtime is unavailable")
	}
	result, err := p.runtime.ListTools(ctx, server)
	if err != nil {
		return nil, err
	}
	if len(result.ToolDetails) > 0 {
		return result.ToolDetails, nil
	}
	tools := make([]toolres.MCPExposedToolDefinition, 0, len(result.Tools))
	for _, name := range result.Tools {
		tools = append(tools, toolres.MCPExposedToolDefinition{Name: name})
	}
	return tools, nil
}

func (p productionKnowledgeProviderMCPPort) CallTool(ctx context.Context, server toolres.ToolDefinition, toolName, principalID string, arguments map[string]any) (map[string]any, error) {
	if p.runtime == nil {
		return nil, fmt.Errorf("MCP runtime is unavailable")
	}
	result, err := p.runtime.CallTool(ctx, mcptransport.ExternalMCPCallRequest{
		Server:             server,
		DownstreamToolName: toolName,
		PrincipalID:        principalID,
		Permissions:        []string{knowledgeadmin.PermissionKnowledgeRead, knowledgeadmin.PermissionKnowledgeManage},
		Arguments:          arguments,
	})
	if err != nil {
		return nil, err
	}
	return result.Output, nil
}
