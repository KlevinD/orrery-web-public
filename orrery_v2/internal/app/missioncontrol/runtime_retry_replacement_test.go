package missioncontrol_test

import (
	"context"
	"errors"
	"testing"
	"time"

	missioncontrol "orrery_v2/internal/app/missioncontrol"
	missionevent "orrery_v2/internal/mission/event"
	"orrery_v2/internal/mission/projection"
)

func TestCommandServiceRetryAgentRunCancelsEnqueuedReplacementWhenEventAppendFails(t *testing.T) {
	ctx := context.Background()
	at := time.Date(2026, 6, 6, 14, 30, 0, 0, time.UTC)
	repo := missioncontrol.NewMemoryProjectionRepository()
	model := appRuntimeReadModel(at)
	model.RuntimeSessions = []projection.RuntimeSessionView{{
		RunID: "run-failed", AgentID: "agent-1", TaskID: "task-1", RuntimeKind: "codex",
		Status: projection.RuntimeSessionFailed, CompletedAt: at.Add(-time.Minute), UpdatedAt: at.Add(-time.Minute),
	}}
	if err := repo.Save(ctx, model); err != nil {
		t.Fatal(err)
	}
	runStore := &capturingRunStore{}
	appendErr := errors.New("event append failed")
	service := newAuthorizedCommandService(nil)
	service.SetRuntimeCommandPorts(runStore, repo, failingMissionEventSink{err: appendErr}, &sequenceContextIDGenerator{values: []string{"evt-retry"}}, fixedClock{at: at})

	_, err := service.RetryAgentRun(ctx,
		missioncontrol.ViewerRef{ViewerID: "operator", ViewerKind: missioncontrol.ViewerKindUser},
		missioncontrol.RetryAgentRunCommand{
			IdempotencyKey: "retry-run-event-failure", MissionID: "mission-1", RunID: "run-failed", ReplacementRunID: "run-retry",
		})
	if !errors.Is(err, appendErr) {
		t.Fatalf("RetryAgentRun error = %v, want event append failure", err)
	}
	if len(runStore.runs) != 1 || len(runStore.cancelled) != 1 || runStore.cancelled[0].runID != "run-retry" || runStore.cancelled[0].reason != "Mission retry event append failed" {
		t.Fatalf("replacement compensation = runs %+v cancellations %+v", runStore.runs, runStore.cancelled)
	}
	updated, getErr := repo.GetMissionControl(ctx, "mission-1")
	if getErr != nil {
		t.Fatal(getErr)
	}
	if len(updated.RuntimeSessions) != 1 {
		t.Fatalf("failed retry changed mission projection: %+v", updated.RuntimeSessions)
	}
}

type failingMissionEventSink struct {
	err error
}

func (s failingMissionEventSink) Append(context.Context, missionevent.Envelope) error {
	return s.err
}
