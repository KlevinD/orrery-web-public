package main

import (
	"context"

	"orrery_v2/internal/app/irisadapter"
	mcptransport "orrery_v2/internal/transport/mcp"
)

type productionIrisContextToolCaller struct {
	gateway productionMCPGatewayCaller
}

func (c productionIrisContextToolCaller) CallTool(ctx context.Context, req irisadapter.MCPToolCallRequest) (irisadapter.MCPToolCallResult, error) {
	result, err := c.gateway.CallTool(ctx, mcptransport.CallToolRequest{
		TenantID:    req.TenantID,
		ToolID:      req.ToolID,
		PrincipalID: req.PrincipalID,
		AgentID:     req.AgentID,
		ProfileID:   req.ProfileID,
		Permissions: append([]string(nil), req.Permissions...),
		Arguments:   cloneIrisContextArguments(req.Arguments),
	})
	if err != nil {
		return irisadapter.MCPToolCallResult{}, err
	}
	return irisadapter.MCPToolCallResult{Output: result.Output}, nil
}

func cloneIrisContextArguments(src map[string]any) map[string]any {
	if len(src) == 0 {
		return nil
	}
	dst := make(map[string]any, len(src))
	for key, value := range src {
		dst[key] = value
	}
	return dst
}
