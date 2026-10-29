package missioncontrol_test

import (
	"context"
	"errors"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/missioncontrol"
	missioncore "orrery_v2/internal/mission/core"
	missionevent "orrery_v2/internal/mission/event"
	"orrery_v2/internal/mission/projection"
)

func TestCancelAgentRunDoesNotPublishCancellationWhenQueueIsTerminal(t *testing.T) {
	ctx := context.Background()
	at := time.Date(2026, 9, 3, 8, 0, 0, 0, time.UTC)
	repo := missioncontrol.NewMemoryProjectionRepository()
	model := appRuntimeReadModel(at)
	model.RuntimeSessions = []projection.RuntimeSessionView{{
		RunID: "run-terminal-race", AgentID: "agent-1", TaskID: "task-1", Status: projection.RuntimeSessionRunning,
	}}
	if err := repo.Save(ctx, model); err != nil {
		t.Fatal(err)
	}
	store := &conditionalCancelRunStore{result: missioncontrol.RuntimeRunCancelResult{
		Disposition: missioncontrol.RuntimeRunCancelAlreadyTerminal, Status: "completed",
	}}
	events := &capturingMissionEventJournal{}
	service := newAuthorizedCommandService(nil)
	service.SetRuntimeCommandPorts(store, repo, events, &sequenceContextIDGenerator{values: []string{"unused"}}, fixedClock{at: at})
	service.SetRuntimeEventReader(events)

	_, err := service.CancelAgentRun(ctx, missioncontrol.ViewerRef{ViewerID: "operator", ViewerKind: missioncontrol.ViewerKindUser}, missioncontrol.CancelAgentRunCommand{
		IdempotencyKey: "cancel-terminal-race", MissionID: "mission-1", RunID: "run-terminal-race",
	})
	if !errors.Is(err, missioncontrol.ErrAgentRunTerminal) {
		t.Fatalf("CancelAgentRun error = %v, want ErrAgentRunTerminal", err)
	}
	if len(events.events) != 0 {
		t.Fatalf("published events = %+v, want none", events.events)
	}
}

func TestCancelAgentRunDetectsCompletionPublishedDuringQueueCancellation(t *testing.T) {
	ctx := context.Background()
	at := time.Date(2026, 9, 3, 8, 0, 0, 0, time.UTC)
	repo := missioncontrol.NewMemoryProjectionRepository()
	model := appRuntimeReadModel(at)
	model.RuntimeSessions = []projection.RuntimeSessionView{{
		RunID: "run-event-race", AgentID: "agent-1", TaskID: "task-1", Status: projection.RuntimeSessionRunning,
	}}
	if err := repo.Save(ctx, model); err != nil {
		t.Fatal(err)
	}
	events := &capturingMissionEventJournal{}
	store := &conditionalCancelRunStore{result: missioncontrol.RuntimeRunCancelResult{Disposition: missioncontrol.RuntimeRunCancelApplied, Status: "cancelled"}}
	store.onCancel = func() {
		events.events = append(events.events, missionevent.Envelope{
			EventID: "completed-during-cancel", TenantID: "global", MissionID: "mission-1", EventType: missionevent.EventAgentRunCompleted,
			Payload: missionevent.RuntimeLifecyclePayload{TaskID: "task-1", RunID: "run-event-race"},
		})
	}
	service := newAuthorizedCommandService(nil)
	service.SetRuntimeCommandPorts(store, repo, events, &sequenceContextIDGenerator{values: []string{"unused"}}, fixedClock{at: at})
	service.SetRuntimeEventReader(events)

	_, err := service.CancelAgentRun(ctx, missioncontrol.ViewerRef{ViewerID: "operator", ViewerKind: missioncontrol.ViewerKindUser}, missioncontrol.CancelAgentRunCommand{
		IdempotencyKey: "cancel-event-race", MissionID: "mission-1", RunID: "run-event-race",
	})
	if !errors.Is(err, missioncontrol.ErrAgentRunTerminal) {
		t.Fatalf("CancelAgentRun error = %v, want ErrAgentRunTerminal", err)
	}
	if len(events.events) != 1 || events.events[0].EventType != missionevent.EventAgentRunCompleted {
		t.Fatalf("published events = %+v, want only the completed event", events.events)
	}
}

func TestCancelAgentRunRecoversWhenCompletionWinsEventAppendRace(t *testing.T) {
	ctx := context.Background()
	at := time.Date(2026, 9, 3, 8, 0, 0, 0, time.UTC)
	repo := missioncontrol.NewMemoryProjectionRepository()
	model := appRuntimeReadModel(at)
	model.RuntimeSessions = []projection.RuntimeSessionView{{
		RunID: "run-append-race", AgentID: "agent-1", TaskID: "task-1", Status: projection.RuntimeSessionRunning,
	}}
	if err := repo.Save(ctx, model); err != nil {
		t.Fatal(err)
	}
	events := &completionWinningEventJournal{completed: missionevent.Envelope{
		EventID: "completed-won-append", TenantID: "global", MissionID: "mission-1", EventType: missionevent.EventAgentRunCompleted,
		Payload: missionevent.RuntimeLifecyclePayload{TaskID: "task-1", RunID: "run-append-race"},
	}}
	store := &conditionalCancelRunStore{result: missioncontrol.RuntimeRunCancelResult{Disposition: missioncontrol.RuntimeRunCancelApplied, Status: "cancelled"}}
	service := newAuthorizedCommandService(nil)
	service.SetRuntimeCommandPorts(store, repo, events, &sequenceContextIDGenerator{values: []string{"cancel-event"}}, fixedClock{at: at})
	service.SetRuntimeEventReader(events)

	_, err := service.CancelAgentRun(ctx, missioncontrol.ViewerRef{ViewerID: "operator", ViewerKind: missioncontrol.ViewerKindUser}, missioncontrol.CancelAgentRunCommand{
		IdempotencyKey: "cancel-append-race", MissionID: "mission-1", RunID: "run-append-race",
	})
	if !errors.Is(err, missioncontrol.ErrAgentRunTerminal) {
		t.Fatalf("CancelAgentRun error = %v, want ErrAgentRunTerminal", err)
	}
	if len(events.events) != 1 || events.events[0].EventType != missionevent.EventAgentRunCompleted {
		t.Fatalf("durable events = %+v, want only completed", events.events)
	}
}

func TestCancelAgentRunHandlesProjectionLagUsingConditionalQueueState(t *testing.T) {
	ctx := context.Background()
	at := time.Date(2026, 9, 3, 8, 0, 0, 0, time.UTC)
	repo := missioncontrol.NewMemoryProjectionRepository()
	if err := repo.Save(ctx, appRuntimeReadModel(at)); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name         string
		result       missioncontrol.RuntimeRunCancelResult
		withIdentity bool
		wantErr      error
	}{
		{name: "active queue is cancelled and projected", result: missioncontrol.RuntimeRunCancelResult{Disposition: missioncontrol.RuntimeRunCancelApplied, Status: "cancelled"}, withIdentity: true},
		{name: "active queue without event identity is not accepted", result: missioncontrol.RuntimeRunCancelResult{Disposition: missioncontrol.RuntimeRunCancelApplied, Status: "cancelled"}, wantErr: missioncontrol.ErrInvalidRequest},
		{name: "missing queue is explicit", result: missioncontrol.RuntimeRunCancelResult{Disposition: missioncontrol.RuntimeRunCancelMissing}, wantErr: missioncontrol.ErrAgentRunMissing},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repo := missioncontrol.NewMemoryProjectionRepository()
			if err := repo.Save(ctx, appRuntimeReadModel(at)); err != nil {
				t.Fatal(err)
			}
			store := &conditionalCancelRunStore{result: test.result}
			events := &capturingMissionEventJournal{}
			service := newAuthorizedCommandService(nil)
			service.SetRuntimeCommandPorts(store, repo, events, &sequenceContextIDGenerator{values: []string{"cancel-projection-lag-event"}}, fixedClock{at: at})
			service.SetRuntimeEventReader(events)
			cmd := missioncontrol.CancelAgentRunCommand{
				IdempotencyKey: "cancel-projection-lag", MissionID: "mission-1", RunID: "run-not-projected",
			}
			if test.withIdentity {
				cmd.TaskID = "task-not-projected"
				cmd.AgentID = "agent-not-projected"
			}
			receipt, err := service.CancelAgentRun(ctx, missioncontrol.ViewerRef{ViewerID: "operator", ViewerKind: missioncontrol.ViewerKindUser}, cmd)
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("CancelAgentRun error = %v, want %v", err, test.wantErr)
			}
			if test.wantErr == nil && (receipt.Status != "accepted" || receipt.CommandID != "run-not-projected" || receipt.TaskID != "task-not-projected") {
				t.Fatalf("receipt = %+v", receipt)
			}
			if test.withIdentity && (len(events.events) != 1 || events.events[0].EventType != missionevent.EventAgentRunCancelled) {
				t.Fatalf("durable cancellation events = %+v", events.events)
			}
			if store.cancelCalls != 1 {
				t.Fatalf("conditional cancel calls = %d, want 1", store.cancelCalls)
			}
		})
	}
}

type conditionalCancelRunStore struct {
	capturingRunStore
	result      missioncontrol.RuntimeRunCancelResult
	err         error
	cancelCalls int
	onCancel    func()
}

func (s *conditionalCancelRunStore) CancelWithResult(_ context.Context, _ agentcore.AgentRunID, _ string) (missioncontrol.RuntimeRunCancelResult, error) {
	s.cancelCalls++
	if s.onCancel != nil {
		s.onCancel()
	}
	return s.result, s.err
}

type completionWinningEventJournal struct {
	events    []missionevent.Envelope
	completed missionevent.Envelope
}

func (j *completionWinningEventJournal) Append(context.Context, missionevent.Envelope) error {
	if len(j.events) == 0 {
		j.events = append(j.events, j.completed)
	}
	return missioncontrol.ErrConflict
}

func (j *completionWinningEventJournal) LoadMission(context.Context, missioncore.MissionID) ([]missionevent.Envelope, error) {
	return append([]missionevent.Envelope(nil), j.events...), nil
}

func (j *completionWinningEventJournal) LoadMissionForTenant(context.Context, string, missioncore.MissionID) ([]missionevent.Envelope, error) {
	return append([]missionevent.Envelope(nil), j.events...), nil
}
