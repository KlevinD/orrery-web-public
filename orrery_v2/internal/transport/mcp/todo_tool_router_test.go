package mcp

import (
	"context"
	"testing"

	todoapp "orrery_v2/internal/app/todo"
	toolstd "orrery_v2/internal/resource/tool/std"
)

func TestToolRouterRoutesTodoToolsThroughExecutor(t *testing.T) {
	ctx := context.Background()
	registry := newGatewayTestRegistry(t)
	todoExecutor := &recordingTodoExecutor{
		result: todoapp.ToolExecutionResult{Output: map[string]any{"todo_id": "todo-1"}},
	}
	router := NewToolRouter(ToolRouterOptions{
		Registry:     registry,
		TodoExecutor: todoExecutor,
	})

	result, err := router.CallTool(ctx, CallToolRequest{
		ToolID:      string(toolstd.TodoCreate),
		PrincipalID: "agent-ops",
		AgentID:     "agent-ops",
		Permissions: []string{toolstd.PermissionTodoWrite},
		Arguments: map[string]any{
			"title":       "Ship homepage",
			"description": "Build real todo tools",
		},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if todoExecutor.calledToolID != string(toolstd.TodoCreate) ||
		todoExecutor.viewer.ViewerID != "agent-ops" ||
		todoExecutor.viewer.ViewerKind != todoapp.ViewerKindAgent ||
		todoExecutor.arguments["title"] != "Ship homepage" {
		t.Fatalf("todo request = tool:%q viewer:%+v args:%+v", todoExecutor.calledToolID, todoExecutor.viewer, todoExecutor.arguments)
	}
	if result.Output["todo_id"] != "todo-1" {
		t.Fatalf("result = %+v", result.Output)
	}
}

func TestToolRouterRoutesIdeaToolsThroughTodoExecutor(t *testing.T) {
	ctx := context.Background()
	registry := newGatewayTestRegistry(t)
	todoExecutor := &recordingTodoExecutor{
		result: todoapp.ToolExecutionResult{Output: map[string]any{"idea_id": "idea-1"}},
	}
	router := NewToolRouter(ToolRouterOptions{
		Registry:     registry,
		TodoExecutor: todoExecutor,
	})

	result, err := router.CallTool(ctx, CallToolRequest{
		ToolID:      string(toolstd.IdeaCreate),
		PrincipalID: "agent-ops",
		AgentID:     "agent-ops",
		Permissions: []string{toolstd.PermissionIdeaWrite},
		Arguments: map[string]any{
			"title":   "Capture insight",
			"content": "Convert later",
		},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if todoExecutor.calledToolID != string(toolstd.IdeaCreate) ||
		todoExecutor.viewer.ViewerID != "agent-ops" ||
		todoExecutor.viewer.ViewerKind != todoapp.ViewerKindAgent ||
		todoExecutor.arguments["title"] != "Capture insight" {
		t.Fatalf("idea request = tool:%q viewer:%+v args:%+v", todoExecutor.calledToolID, todoExecutor.viewer, todoExecutor.arguments)
	}
	if result.Output["idea_id"] != "idea-1" {
		t.Fatalf("result = %+v", result.Output)
	}
}

type recordingTodoExecutor struct {
	calledToolID string
	viewer       todoapp.ViewerRef
	arguments    map[string]any
	result       todoapp.ToolExecutionResult
	err          error
}

func (e *recordingTodoExecutor) Execute(ctx context.Context, req todoapp.ToolExecutionRequest) (todoapp.ToolExecutionResult, error) {
	e.calledToolID = req.ToolID
	e.viewer = req.Viewer
	e.arguments = req.Arguments
	return e.result, e.err
}
