package mcp

import "context"

// The complete request retains the host-bound identity. Model arguments cannot
// provide the principal, tenant, execution envelope or device owner.
type PersonalBrowserExecutor interface {
	ExecutePersonalBrowser(context.Context, CallToolRequest) (CallToolResult, error)
}
