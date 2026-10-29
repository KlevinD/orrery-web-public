package missioncontrol_test

import (
	"context"
	"testing"
	"time"

	"orrery_v2/internal/app/missioncontrol"
	"orrery_v2/internal/mission/projection"
)

func TestCommandServiceRetryAgentRunRejectsApprovedTask(t *testing.T) {
	ctx := context.Background()
	at := time.Date(2026, 6, 6, 14, 30, 0, 0, time.UTC)
	repo := missioncontrol.NewMemoryProjectionRepository()
	model := appRuntimeReadModel(at)
	model.TaskBoard.Tasks[0].Status = projection.TaskStatusApproved
	model.RuntimeSessions = []projection.RuntimeSessionView{{
		RunID: "run-stale", AgentID: "agent-1", TaskID: "task-1", RuntimeKind: "codex",
		Status: projection.RuntimeSessionFailed, ErrorSummary: "old attempt failed", CompletedAt: at.Add(-time.Minute), UpdatedAt: at.Add(-time.Minute),
	}}
	if err := repo.Save(ctx, model); err != nil {
		t.Fatalf("Save returned error: %v", err)
	}
	runStore := &capturingRunStore{}
	events := &capturingMissionEventSink{}
	service := newAuthorizedCommandService(nil)
	service.SetRuntimeCommandPorts(runStore, repo, events, &sequenceContextIDGenerator{}, fixedClock{at: at})

	_, err := service.RetryAgentRun(ctx,
		missioncontrol.ViewerRef{ViewerID: "operator", ViewerKind: missioncontrol.ViewerKindUser},
		missioncontrol.RetryAgentRunCommand{
			RequestID:      "corr-retry-terminal",
			IdempotencyKey: "retry-terminal",
			MissionID:      "mission-1",
			RunID:          "run-stale",
		})

	if err != missioncontrol.ErrInvalidRequest {
		t.Fatalf("RetryAgentRun error = %v, want ErrInvalidRequest", err)
	}
	if len(runStore.runs) != 0 || len(events.events) != 0 {
		t.Fatalf("terminal task retry changed runtime state: runs=%+v events=%+v", runStore.runs, events.events)
	}
}
