package mcp

import (
	"context"
	"strings"

	"orrery_v2/internal/app/discussionsession"
)

type SessionHistoryToolExecutor interface {
	ExecuteHistoryTool(context.Context, discussionsession.HistoryToolRequest) (map[string]any, error)
}

func sessionHistoryToolRequest(req CallToolRequest, toolID string) discussionsession.HistoryToolRequest {
	return discussionsession.HistoryToolRequest{
		TenantID: strings.TrimSpace(req.TenantID), RunID: strings.TrimSpace(req.RunID),
		AgentID: strings.TrimSpace(req.AgentID), LogicalAgentID: strings.TrimSpace(req.LogicalAgentID),
		ToolID: toolID, Arguments: cloneArguments(req.Arguments),
	}
}
