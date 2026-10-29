package mcp

import (
	"context"
	"errors"
	"testing"

	"orrery_v2/internal/app/discussionsession"
	toolstd "orrery_v2/internal/resource/tool/std"
)

type recordingSessionHistoryExecutor struct {
	request discussionsession.HistoryToolRequest
	calls   int
}

func (e *recordingSessionHistoryExecutor) ExecuteHistoryTool(_ context.Context, req discussionsession.HistoryToolRequest) (map[string]any, error) {
	e.request, e.calls = req, e.calls+1
	return map[string]any{"items": []any{}}, nil
}

func TestSessionHistoryRouterRequiresReadPermissionAndHostIdentity(t *testing.T) {
	executor := &recordingSessionHistoryExecutor{}
	router := NewToolRouter(ToolRouterOptions{Registry: newGatewayTestRegistry(t), SessionHistoryExecutor: executor})
	for _, id := range []string{string(toolstd.SessionSearch), string(toolstd.SessionRead)} {
		req := CallToolRequest{ToolID: id, TenantID: "tenant", AgentID: "agent", LogicalAgentID: "logical", RunID: "run", Arguments: map[string]any{"query": "keyword", "tenant_id": "forged"}}
		if _, err := router.CallTool(context.Background(), req); !errors.Is(err, ErrPermissionDenied) {
			t.Fatalf("missing read permission accepted: %v", err)
		}
		req.Permissions = []string{toolstd.PermissionDiscussionRead}
		if _, err := router.CallTool(context.Background(), req); err != nil {
			t.Fatal(err)
		}
		if executor.request.TenantID != "tenant" || executor.request.RunID != "run" || executor.request.AgentID != "agent" || executor.request.LogicalAgentID != "logical" || executor.request.ToolID != id {
			t.Fatalf("lost host identity: %+v", executor.request)
		}
	}
	if executor.calls != 2 {
		t.Fatalf("denied calls reached executor: %d", executor.calls)
	}
	router = NewToolRouter(ToolRouterOptions{Registry: newGatewayTestRegistry(t)})
	if _, err := router.CallTool(context.Background(), CallToolRequest{ToolID: string(toolstd.SessionSearch), Permissions: []string{toolstd.PermissionDiscussionRead}}); !errors.Is(err, ErrServiceDependency) {
		t.Fatalf("missing history service must not return fake empty results: %v", err)
	}
}
