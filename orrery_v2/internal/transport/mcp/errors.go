package mcp

import "errors"

var (
	ErrServiceDependency         = errors.New("mcp gateway service dependency")
	ErrToolNotFound              = errors.New("mcp gateway tool not found")
	ErrPermissionDenied          = errors.New("mcp gateway permission denied")
	ErrInvalidRequest            = errors.New("invalid mcp gateway request")
	ErrToolCallOutcomeUnrecorded = errors.New("mcp tool call completed but outcome recording failed")
)
