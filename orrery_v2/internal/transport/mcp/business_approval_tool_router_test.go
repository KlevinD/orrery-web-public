package mcp

import (
	"context"
	"testing"

	"orrery_v2/internal/app/hil"
	toolstd "orrery_v2/internal/resource/tool/std"
)

func TestToolRouterRoutesBusinessApprovalThroughAuthenticatedExecutor(t *testing.T) {
	executor := &recordingBusinessApprovalExecutor{result: hil.BusinessApprovalToolResult{
		DecisionID: "decision-1", Status: hil.DecisionPending, RouteMode: hil.ModeApproval,
		MaterialHash: "sha256:material", Revision: 1,
	}}
	router := NewToolRouter(ToolRouterOptions{
		Registry:                 newGatewayTestRegistry(t),
		BusinessApprovalExecutor: executor,
	})
	arguments := map[string]any{"title": "公众号发布审批", "idempotency_key": "approval-1"}
	result, err := router.CallTool(context.Background(), CallToolRequest{
		TenantID: "system", ToolID: string(toolstd.RequestBusinessApproval), PrincipalID: "principal-marketing",
		AgentID: "agent-marketing", Permissions: []string{toolstd.PermissionApprovalRequest}, Arguments: arguments,
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if executor.request.TenantID != "system" || executor.request.PrincipalID != "principal-marketing" || executor.request.AgentID != "agent-marketing" || executor.request.Arguments["title"] != "公众号发布审批" {
		t.Fatalf("business approval request = %+v", executor.request)
	}
	if result.Output["decision_id"] != "decision-1" || result.Output["route_mode"] != "approval" || result.Output["material_hash"] != "sha256:material" || result.Output["revision"] != 1 {
		t.Fatalf("business approval result = %+v", result.Output)
	}
}

type recordingBusinessApprovalExecutor struct {
	request hil.BusinessApprovalToolRequest
	result  hil.BusinessApprovalToolResult
	err     error
}

func (e *recordingBusinessApprovalExecutor) Execute(_ context.Context, request hil.BusinessApprovalToolRequest) (hil.BusinessApprovalToolResult, error) {
	e.request = request
	return e.result, e.err
}
