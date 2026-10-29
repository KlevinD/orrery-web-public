package main

import (
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	schedulerapp "orrery_v2/internal/app/scheduler"
)

func TestScheduledMissionTaskCommandKeepsOccurrenceEnvelopeFingerprintStable(t *testing.T) {
	at := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)
	parent := &agentcore.ExecutionEnvelope{
		TenantID:         "tenant-a",
		RunID:            "run-a",
		ScheduledTaskID:  "scheduled-a",
		ScheduledRunID:   "run-a",
		AgentID:          "agent-a",
		AgentProfileID:   "agent-a",
		PermissionScopes: []string{"mission.read"},
		AllowedToolIDs:   []string{"tool.read"},
		IssuedAt:         at,
	}
	if err := parent.SealAt(at); err != nil {
		t.Fatalf("seal parent: %v", err)
	}
	req := schedulerapp.AgentTaskRequest{
		TenantID:          "tenant-a",
		RunID:             "run-a",
		ScheduledTaskID:   "scheduled-a",
		AgentID:           "agent-a",
		TaskGoal:          "perform the scheduled work",
		Permissions:       []string{"mission.read"},
		Execution:         schedulerapp.ExecutionPolicy{AllowedToolIDs: []string{"tool.read"}},
		ExecutionEnvelope: parent,
	}
	first := scheduledMissionTaskCommand(req, "mission-a")
	second := scheduledMissionTaskCommand(req, "mission-a")
	if first.ExecutionEnvelope == nil || second.ExecutionEnvelope == nil {
		t.Fatal("task command dropped the execution envelope")
	}
	if first.ExecutionEnvelope.EnvelopeDigest != second.ExecutionEnvelope.EnvelopeDigest || !first.ExecutionEnvelope.IssuedAt.Equal(second.ExecutionEnvelope.IssuedAt) {
		t.Fatalf("task snapshot fingerprint changed between retries: first=%+v second=%+v", first.ExecutionEnvelope, second.ExecutionEnvelope)
	}
	if first.ExecutionEnvelope.MissionID != "mission-a" || first.ExecutionEnvelope.TaskID != string(first.TaskID) {
		t.Fatalf("task identity was not bound into snapshot: %+v", first.ExecutionEnvelope)
	}
	if len(first.ExecutionAllowedToolIDs) != 1 || first.ExecutionAllowedToolIDs[0] != "tool.read" {
		t.Fatalf("allowed tool snapshot was dropped: %+v", first.ExecutionAllowedToolIDs)
	}
	if err := first.ExecutionEnvelope.Verify(); err != nil {
		t.Fatalf("verify task snapshot: %v", err)
	}
}
