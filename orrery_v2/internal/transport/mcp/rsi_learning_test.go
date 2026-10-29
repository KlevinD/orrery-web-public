package mcp

import (
	"context"
	"errors"
	toolstd "orrery_v2/internal/resource/tool/std"
	"testing"
)

type learningExecutor struct {
	req   RSILearningToolRequest
	calls int
}

func (e *learningExecutor) ExecuteRSILearningTool(_ context.Context, req RSILearningToolRequest) (map[string]any, error) {
	e.req = req
	e.calls++
	return map[string]any{"advisory_only": true}, nil
}

func TestRSILearningToolRequiresPermissionAndPreservesTrustedScope(t *testing.T) {
	executor := &learningExecutor{}
	router := NewToolRouter(ToolRouterOptions{Registry: newGatewayTestRegistry(t), RSILearningExecutor: executor})
	req := CallToolRequest{ToolID: string(toolstd.RSILearningPlan), TenantID: "tenant", AgentID: "agent", Arguments: map[string]any{"limit": 10}}
	if _, err := router.CallTool(context.Background(), req); !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("permission bypass: %v", err)
	}
	req.Permissions = []string{toolstd.PermissionRSILearningRead}
	if _, err := router.CallTool(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if executor.calls != 1 || executor.req.TenantID != "tenant" || executor.req.AgentID != "agent" {
		t.Fatal("lost trusted identity")
	}
}
