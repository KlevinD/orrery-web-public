package main

import (
	"context"
	"testing"

	"orrery_v2/internal/app/irisadapter"
	mcptransport "orrery_v2/internal/transport/mcp"
)

func TestProductionIrisContextToolCallerForwardsEnterpriseGraphToolCall(t *testing.T) {
	gateway := &serverTestMCPGatewayCaller{
		result: mcptransport.CallToolResult{Output: map[string]any{"bundle_id": "bundle-1"}},
	}
	caller := productionIrisContextToolCaller{gateway: gateway}

	result, err := caller.CallTool(context.Background(), irisadapter.MCPToolCallRequest{
		TenantID:    "tenant-1",
		ToolID:      irisadapter.EnterpriseGraphBuildContextBundleToolID,
		PrincipalID: "iris-runtime",
		AgentID:     "iris",
		ProfileID:   "profile-iris",
		Permissions: []string{irisadapter.EnterpriseGraphReadPermission},
		Arguments:   map[string]any{"tenant_id": "tenant-1"},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if gateway.req.TenantID != "tenant-1" ||
		gateway.req.ToolID != irisadapter.EnterpriseGraphBuildContextBundleToolID ||
		gateway.req.PrincipalID != "iris-runtime" ||
		gateway.req.AgentID != "iris" ||
		gateway.req.ProfileID != "profile-iris" {
		t.Fatalf("gateway request = %+v", gateway.req)
	}
	if len(gateway.req.Permissions) != 1 || gateway.req.Permissions[0] != irisadapter.EnterpriseGraphReadPermission {
		t.Fatalf("permissions = %+v", gateway.req.Permissions)
	}
	if gateway.req.Arguments["tenant_id"] != "tenant-1" {
		t.Fatalf("arguments = %+v", gateway.req.Arguments)
	}
	if result.Output["bundle_id"] != "bundle-1" {
		t.Fatalf("result = %+v", result.Output)
	}
}
