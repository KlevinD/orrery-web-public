package main

import (
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	runtimecore "orrery_v2/internal/agent/runtime/core"
	mcptransport "orrery_v2/internal/transport/mcp"
)

func TestValidateRuntimeExecutionEnvelopeRequiresResponsibilityContext(t *testing.T) {
	envelope := &agentcore.ExecutionEnvelope{
		TenantID:       "tenant-1",
		RunID:          "run-1",
		AgentID:        "agent-1",
		AgentProfileID: "profile-1",
		ResponsibilityContext: &agentcore.ResponsibilityContext{
			ContextID:             "context-1",
			ResponsibilityID:      "responsibility-1",
			ResponsibilityVersion: 1,
			PolicyID:              "policy-1",
			PolicyVersion:         1,
			AgentProfileID:        "profile-1",
			TaskPermissionScopes:  []string{"repository.read"},
		},
	}
	if err := envelope.SealAt(time.Date(2026, 9, 21, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("seal envelope: %v", err)
	}
	call := runtimecore.RuntimeToolCallRequest{
		TenantID:          envelope.TenantID,
		RunID:             agentcore.AgentRunID(envelope.RunID),
		AgentID:           agentcore.AgentID(envelope.AgentID),
		Name:              "repository_read",
		ExecutionEnvelope: envelope,
	}
	binding := agentcore.RuntimeToolBinding{ToolID: "repository_read", GrantedPermissions: []string{"repository.read"}}
	if err := validateRuntimeExecutionEnvelope(call, binding); err != mcptransport.ErrPermissionDenied {
		t.Fatalf("validate without responsibility context = %v, want permission denied", err)
	}
}
