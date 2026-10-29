package scheduler

import (
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
)

func TestScheduledExecutionEnvelopeReusesPersistedOccurrenceSnapshot(t *testing.T) {
	at := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)
	parent := &agentcore.ExecutionEnvelope{
		TenantID:         "tenant-a",
		RunID:            "run-a",
		ScheduledTaskID:  "scheduled-a",
		ScheduledRunID:   "run-a",
		AgentID:          "agent-a",
		AgentProfileID:   "agent-a",
		PrincipalID:      "human-a",
		PrincipalKind:    agentcore.ExecutionPrincipalUser,
		PermissionScopes: []string{"mission.read"},
		IssuedAt:         at,
	}
	if err := parent.SealAt(at); err != nil {
		t.Fatalf("seal parent: %v", err)
	}
	task := ScheduledTask{
		TenantID:         "tenant-a",
		ScheduledTaskID:  "scheduled-a",
		CreatedBy:        "human-a",
		CreatedByKind:    ViewerKindHuman,
		CreatorProfileID: "agent-a",
	}
	run := ScheduledRun{ScheduledRunID: "run-a", StartedAt: at.Add(time.Hour), ExecutionEnvelope: parent}
	got, err := scheduledExecutionEnvelope(task, run, CreatorPermissions{PermissionScope: []string{"admin.write"}}, Target{AgentID: "agent-a"})
	if err != nil {
		t.Fatalf("reuse persisted envelope: %v", err)
	}
	if got.EnvelopeDigest != parent.EnvelopeDigest || !got.IssuedAt.Equal(parent.IssuedAt) || got.PermissionScopes[0] != "mission.read" {
		t.Fatalf("persisted snapshot was replaced or widened: parent=%+v got=%+v", parent, got)
	}
	if err := got.Verify(); err != nil {
		t.Fatalf("verify reused envelope: %v", err)
	}
}

func TestScheduledExecutionEnvelopeUsesTargetAgentProfile(t *testing.T) {
	at := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)
	task := ScheduledTask{
		TenantID:         "tenant-a",
		ScheduledTaskID:  "scheduled-a",
		CreatedBy:        "human-a",
		CreatedByKind:    ViewerKindHuman,
		CreatorProfileID: "creator-profile",
	}
	run := ScheduledRun{ScheduledRunID: "run-a", StartedAt: at}
	got, err := scheduledExecutionEnvelope(task, run, CreatorPermissions{}, Target{AgentID: "worker-profile"})
	if err != nil {
		t.Fatalf("create execution envelope: %v", err)
	}
	if got.AgentID != "worker-profile" || got.AgentProfileID != "worker-profile" {
		t.Fatalf("execution identity followed creator instead of target Agent: %+v", got)
	}
}
