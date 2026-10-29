package missioncontrol_test

import (
	"context"
	"testing"
	"time"

	"orrery_v2/internal/app/missioncontrol"
	"orrery_v2/internal/mission/projection"
)

func TestCommandServiceStartAgentRunIgnoresSupersededActiveSession(t *testing.T) {
	ctx := context.Background()
	at := time.Date(2026, 6, 6, 10, 47, 0, 0, time.UTC)
	repo := missioncontrol.NewMemoryProjectionRepository()
	model := appRuntimeReadModel(at)
	model.TaskBoard.Tasks[0].Status = projection.TaskStatusBlocked
	model.RuntimeSessions = []projection.RuntimeSessionView{
		{
			RunID: "run-stale", AgentID: "agent-1", TaskID: "task-1", RuntimeKind: "codex",
			Status: projection.RuntimeSessionQueued, QueuedAt: at.Add(-2 * time.Minute), UpdatedAt: at.Add(-2 * time.Minute),
		},
		{
			RunID: "run-failed", AgentID: "agent-1", TaskID: "task-1", RuntimeKind: "codex",
			Status: projection.RuntimeSessionFailed, QueuedAt: at.Add(-time.Minute), CompletedAt: at, UpdatedAt: at,
		},
	}
	if err := repo.Save(ctx, model); err != nil {
		t.Fatal(err)
	}
	runStore := &capturingRunStore{}
	service := newAuthorizedCommandService(nil)
	service.SetRuntimeCommandPorts(runStore, repo, &capturingMissionEventSink{}, &sequenceContextIDGenerator{values: []string{"evt-retry"}}, fixedClock{at: at})

	_, err := service.StartAgentRun(ctx, missioncontrol.ViewerRef{ViewerID: "operator", ViewerKind: missioncontrol.ViewerKindUser}, missioncontrol.StartAgentRunCommand{
		IdempotencyKey: "start-retry", MissionID: "mission-1", TaskID: "task-1", AgentID: "agent-1", RuntimeKind: "codex", RunID: "run-retry",
	})
	if err != nil {
		t.Fatalf("StartAgentRun with superseded active session returned error: %v", err)
	}
	if len(runStore.runs) != 1 || runStore.runs[0].RunID != "run-retry" {
		t.Fatalf("retry run was not enqueued: %+v", runStore.runs)
	}
}

func TestCommandServiceStartAgentRunRejectsNewestActiveSession(t *testing.T) {
	ctx := context.Background()
	at := time.Date(2026, 6, 6, 10, 48, 0, 0, time.UTC)
	repo := missioncontrol.NewMemoryProjectionRepository()
	model := appRuntimeReadModel(at)
	model.RuntimeSessions = []projection.RuntimeSessionView{
		{
			RunID: "run-stale", AgentID: "agent-1", TaskID: "task-1", RuntimeKind: "codex",
			Status: projection.RuntimeSessionQueued, QueuedAt: at.Add(-2 * time.Minute), UpdatedAt: at.Add(-2 * time.Minute),
		},
		{
			RunID: "run-current", AgentID: "agent-1", TaskID: "task-1", RuntimeKind: "codex",
			Status: projection.RuntimeSessionRunning, QueuedAt: at.Add(-time.Minute), StartedAt: at, UpdatedAt: at,
		},
	}
	if err := repo.Save(ctx, model); err != nil {
		t.Fatal(err)
	}
	runStore := &capturingRunStore{}
	service := newAuthorizedCommandService(nil)
	service.SetRuntimeCommandPorts(runStore, repo, &capturingMissionEventSink{}, &sequenceContextIDGenerator{values: []string{"evt-duplicate"}}, fixedClock{at: at})

	_, err := service.StartAgentRun(ctx, missioncontrol.ViewerRef{ViewerID: "operator", ViewerKind: missioncontrol.ViewerKindUser}, missioncontrol.StartAgentRunCommand{
		IdempotencyKey: "start-duplicate", MissionID: "mission-1", TaskID: "task-1", AgentID: "agent-1", RuntimeKind: "codex", RunID: "run-duplicate",
	})
	if err != missioncontrol.ErrAgentRunAlreadyActive {
		t.Fatalf("newest active run error = %v, want ErrAgentRunAlreadyActive", err)
	}
	if len(runStore.runs) != 0 {
		t.Fatalf("duplicate active run was enqueued: %+v", runStore.runs)
	}
}
