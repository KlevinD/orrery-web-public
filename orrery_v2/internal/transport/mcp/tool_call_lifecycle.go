package mcp

import (
	"context"
	"errors"

	agentcore "orrery_v2/internal/agent/core"
)

type ToolCallLifecycleStart struct {
	TenantID              string
	RunID                 string
	ToolID                string
	PrincipalID           string
	AgentID               string
	ProfileID             string
	Arguments             map[string]any
	Source                ToolSource
	ServerToolID          string
	DownstreamToolName    string
	CorrelationID         string
	ResponsibilityContext *agentcore.ResponsibilityContext
	Maintenance           bool
}

type ToolCallFailure struct {
	Code    string
	Message string
}

type ToolCallAttempt interface {
	CompleteToolCall(context.Context, CallToolResult) error
	FailToolCall(context.Context, ToolCallFailure) error
}

type ToolCallLifecycle interface {
	StartToolCall(context.Context, ToolCallLifecycleStart) (ToolCallAttempt, error)
}

func toolCallFailure(err error) ToolCallFailure {
	switch {
	case errors.Is(err, context.Canceled):
		return ToolCallFailure{Code: "context_canceled", Message: "MCP tool call was canceled"}
	case errors.Is(err, context.DeadlineExceeded):
		return ToolCallFailure{Code: "deadline_exceeded", Message: "MCP tool call exceeded its deadline"}
	case errors.Is(err, ErrToolNotFound):
		return ToolCallFailure{Code: "tool_not_found", Message: "MCP tool was not found"}
	case errors.Is(err, ErrPermissionDenied):
		return ToolCallFailure{Code: "permission_denied", Message: "MCP tool call was denied"}
	case errors.Is(err, ErrInvalidRequest):
		return ToolCallFailure{Code: "invalid_request", Message: "MCP tool call request was invalid"}
	case errors.Is(err, ErrServiceDependency):
		return ToolCallFailure{Code: "dependency_unavailable", Message: "MCP tool dependency was unavailable"}
	default:
		return ToolCallFailure{Code: "tool_execution_failed", Message: "MCP tool execution failed"}
	}
}
