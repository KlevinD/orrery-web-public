package mcp

import "context"

type RSILearningToolRequest struct {
	TenantID  string
	AgentID   string
	Arguments map[string]any
}

type RSILearningToolExecutor interface {
	ExecuteRSILearningTool(context.Context, RSILearningToolRequest) (map[string]any, error)
}
