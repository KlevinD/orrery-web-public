package mcp

import (
	"context"
	"errors"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	toolstd "orrery_v2/internal/resource/tool/std"
)

func TestToolRouterRejectsExplicitEnvelopeWithoutResponsibilityContext(t *testing.T) {
	contextSnapshot := &agentcore.ResponsibilityContext{
		ContextID:                 "context-1",
		ResponsibilityID:          "responsibility-1",
		ResponsibilityVersion:     1,
		PolicyID:                  "policy-1",
		PolicyVersion:             1,
		AgentProfileID:            "profile-1",
		TaskPermissionScopes:      []string{string(toolstd.PermissionSchedulerRead)},
		RequireExplicitToolScopes: true,
	}
	envelope := &agentcore.ExecutionEnvelope{
		TenantID:              "tenant-1",
		RunID:                 "run-1",
		AgentID:               "agent-1",
		AgentProfileID:        "profile-1",
		ResponsibilityContext: contextSnapshot,
	}
	if err := envelope.SealAt(time.Date(2026, 9, 21, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("seal envelope: %v", err)
	}
	router := NewToolRouter(ToolRouterOptions{Registry: newGatewayTestRegistry(t)})
	_, err := router.CallTool(context.Background(), CallToolRequest{
		TenantID:          envelope.TenantID,
		RunID:             envelope.RunID,
		ToolID:            string(toolstd.ScheduledTaskList),
		AgentID:           envelope.AgentID,
		ProfileID:         envelope.AgentProfileID,
		Permissions:       []string{string(toolstd.PermissionSchedulerRead)},
		ExecutionEnvelope: envelope,
	})
	if !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("CallTool error = %v, want permission denied", err)
	}
}
