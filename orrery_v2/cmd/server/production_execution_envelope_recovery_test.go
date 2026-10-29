package main

import (
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	schedulerapp "orrery_v2/internal/app/scheduler"
)

func TestScheduledDeliveryRuntimeRunRekeysExecutionEnvelope(t *testing.T) {
	now := time.Now().UTC()
	parent := &agentcore.ExecutionEnvelope{
		TenantID:         "tenant-recovery",
		RunID:            "scheduled-run-1",
		ScheduledTaskID:  "scheduled-task-1",
		ScheduledRunID:   "scheduled-run-1",
		MissionID:        "mission-1",
		TaskID:           "task-1",
		AgentID:          "agent-1",
		AgentProfileID:   "profile-1",
		PermissionScopes: []string{"mission.read"},
	}
	if err := parent.SealAt(now); err != nil {
		t.Fatalf("seal parent envelope: %v", err)
	}
	stored := schedulerapp.ScheduledRun{
		ScheduledRunID:               "scheduled-run-1",
		ScheduledTaskID:              "scheduled-task-1",
		ScheduledTaskTitle:           "recover scheduled work",
		TenantID:                     "tenant-recovery",
		MissionID:                    "mission-1",
		TaskID:                       "task-1",
		AgentID:                      "agent-1",
		ExecutionEnvelope:            parent,
		TargetMode:                   schedulerapp.TargetModeAgentTask,
		ConfigurationSnapshotVersion: 1,
	}
	payload := productionScheduledAgentDeliveryPayload{
		TenantID:         "tenant-recovery",
		RuntimeRunID:     "runtime-run-1",
		ScheduledRunID:   "scheduled-run-1",
		ScheduledTaskID:  "scheduled-task-1",
		MissionID:        "mission-1",
		TaskID:           "task-1",
		AgentID:          "agent-1",
		ResponsibilityID: "responsibility-1",
	}
	run, err := scheduledDeliveryRuntimeRun(payload, stored)
	if err != nil {
		t.Fatalf("build delivery runtime run: %v", err)
	}
	got := run.Request.ExecutionEnvelope
	if got == nil {
		t.Fatal("delivery runtime run lost execution envelope")
	}
	if got.RunID != payload.RuntimeRunID {
		t.Fatalf("runtime envelope run id = %q, want %q", got.RunID, payload.RuntimeRunID)
	}
	if got.ScheduledRunID != stored.ScheduledRunID || got.ScheduledTaskID != stored.ScheduledTaskID {
		t.Fatalf("scheduled lineage was not preserved: task=%q run=%q", got.ScheduledTaskID, got.ScheduledRunID)
	}
	if got.EnvelopeID == parent.EnvelopeID || got.EnvelopeDigest == parent.EnvelopeDigest {
		t.Fatal("runtime envelope was reused instead of rekeyed")
	}
	if err := got.Verify(); err != nil {
		t.Fatalf("verify rekeyed envelope: %v", err)
	}
}
