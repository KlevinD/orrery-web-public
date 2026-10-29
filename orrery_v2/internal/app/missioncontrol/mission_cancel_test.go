package missioncontrol_test

import (
	"context"
	"testing"
	"time"

	"orrery_v2/internal/app/missioncontrol"
	missioncore "orrery_v2/internal/mission/core"
	missionevent "orrery_v2/internal/mission/event"
	"orrery_v2/internal/mission/projection"
)

func TestCommandServiceCancelsMissionAndActiveRunsIdempotently(t *testing.T) {
	ctx := context.Background()
	at := time.Date(2026, 8, 24, 11, 5, 0, 0, time.UTC)
	repo := missioncontrol.NewMemoryProjectionRepository()
	model := appRuntimeReadModel(at)
	model.RuntimeSessions = []projection.RuntimeSessionView{{
		RunID: "run-active", AgentID: "agent-1", TaskID: "task-1", RuntimeKind: "pi",
		Status: projection.RuntimeSessionRunning, QueuedAt: at.Add(-time.Minute), StartedAt: at, UpdatedAt: at,
	}}
	if err := repo.Save(ctx, model); err != nil {
		t.Fatal(err)
	}
	runStore := &capturingRunStore{}
	events := &capturingMissionEventJournal{events: make([]missionevent.Envelope, model.Checkpoint.Version)}
	audit := &fakeMissionAuditRecorder{}
	service := newAuthorizedCommandService(nil)
	service.SetRuntimeCommandPorts(runStore, repo, events, &sequenceContextIDGenerator{values: []string{"evt-mission-cancelled", "evt-run-cancelled"}}, fixedClock{at: at.Add(time.Minute)})
	service.SetRuntimeEventReader(events)
	service.SetAuditRecorder(audit)
	viewer := missioncontrol.ViewerRef{ViewerID: "operator", ViewerKind: missioncontrol.ViewerKindUser}
	command := missioncontrol.CancelMissionCommand{
		RequestID: "cancel-request", IdempotencyKey: "cancel-mission-1", MissionID: "mission-1", Reason: "polluted verification Mission",
	}

	receipt, err := service.CancelMission(ctx, viewer, command)
	if err != nil {
		t.Fatal(err)
	}
	updated, err := repo.GetMissionControl(ctx, "mission-1")
	if err != nil {
		t.Fatal(err)
	}
	if receipt.CommandID != "cancel-mission-1" || updated.Mission.Status != projection.MissionStatusCancelled || updated.TaskBoard.Tasks[0].Status != projection.TaskStatusCancelled || updated.RuntimeSessions[0].Status != projection.RuntimeSessionCancelled || missioncontrol.MapViewDTO(updated, viewer).Mission.Phase != "closed" {
		t.Fatalf("receipt/projection = %+v / %+v", receipt, updated)
	}
	if len(runStore.cancelled) != 1 || len(events.events) != 9 || events.events[7].EventType != missionevent.EventMissionCancelled || events.events[8].EventType != missionevent.EventAgentRunCancelled {
		t.Fatalf("cancellations/events = %+v / %+v", runStore.cancelled, events.events)
	}
	if len(audit.events) != 2 || audit.events[1].Action != "mission.cancel" || audit.events[1].Metadata["reason"] != command.Reason {
		t.Fatalf("audit events = %+v", audit.events)
	}
	if _, err := service.CancelMission(ctx, viewer, command); err != nil {
		t.Fatalf("idempotent cancel: %v", err)
	}
	if len(runStore.cancelled) != 1 || len(events.events) != 9 || len(audit.events) != 2 {
		t.Fatalf("replay duplicated effects: runs=%d events=%d audit=%d", len(runStore.cancelled), len(events.events), len(audit.events))
	}
	changed := command
	changed.Reason = "different reason"
	if _, err := service.CancelMission(ctx, viewer, changed); err != missioncontrol.ErrConflict {
		t.Fatalf("changed idempotent replay error = %v", err)
	}
	if _, err := service.StartAgentRun(ctx, viewer, missioncontrol.StartAgentRunCommand{
		IdempotencyKey: "start-after-cancel", MissionID: "mission-1", TaskID: "task-1", AgentID: "agent-1", RuntimeKind: "pi",
	}); err != missioncontrol.ErrInvalidRequest {
		t.Fatalf("StartAgentRun after cancel error = %v", err)
	}
}

func TestCommandServiceRejectsCancellingCompletedMission(t *testing.T) {
	ctx := context.Background()
	at := time.Date(2026, 8, 24, 11, 10, 0, 0, time.UTC)
	repo := missioncontrol.NewMemoryProjectionRepository()
	model := appRuntimeReadModel(at)
	model.Mission.Status = projection.MissionStatusCompleted
	if err := repo.Save(ctx, model); err != nil {
		t.Fatal(err)
	}
	events := &capturingMissionEventSink{}
	service := newAuthorizedCommandService(nil)
	service.SetRuntimeCommandPorts(&capturingRunStore{}, repo, events, &sequenceContextIDGenerator{}, fixedClock{at: at})

	_, err := service.CancelMission(ctx, missioncontrol.ViewerRef{ViewerID: "operator", ViewerKind: missioncontrol.ViewerKindUser}, missioncontrol.CancelMissionCommand{
		IdempotencyKey: "cancel-completed", MissionID: "mission-1", Reason: "must not rewrite completed history",
	})
	if err != missioncontrol.ErrConflict || len(events.events) != 0 {
		t.Fatalf("CancelMission error/events = %v / %+v", err, events.events)
	}
}

func TestCommandServiceCancellationIgnoresSupersededQueuedSession(t *testing.T) {
	ctx := context.Background()
	at := time.Date(2026, 9, 3, 4, 0, 0, 0, time.UTC)
	repo := missioncontrol.NewMemoryProjectionRepository()
	model := appRuntimeReadModel(at)
	model.RuntimeSessions = []projection.RuntimeSessionView{
		{
			RunID: "responsibility-run-old", AgentID: "agent-1", TaskID: "task-1", RuntimeKind: "codex",
			Status: projection.RuntimeSessionQueued, QueuedAt: at.Add(-time.Hour), UpdatedAt: at.Add(-time.Hour),
		},
		{
			RunID: "run-terminal", AgentID: "agent-1", TaskID: "task-1", RuntimeKind: "codex",
			Status: projection.RuntimeSessionCompleted, StartedAt: at.Add(-30 * time.Minute), UpdatedAt: at.Add(-time.Minute),
		},
	}
	if err := repo.Save(ctx, model); err != nil {
		t.Fatal(err)
	}
	runStore := &capturingRunStore{}
	service := newAuthorizedCommandService(nil)
	service.SetRuntimeCommandPorts(runStore, repo, &capturingMissionEventSink{}, &sequenceContextIDGenerator{values: []string{"evt-mission-cancelled"}}, fixedClock{at: at})

	_, err := service.CancelMission(ctx, missioncontrol.ViewerRef{ViewerID: "operator", ViewerKind: missioncontrol.ViewerKindUser}, missioncontrol.CancelMissionCommand{
		IdempotencyKey: "cancel-superseded", MissionID: "mission-1", Reason: "stop Mission",
	})
	if err != nil {
		t.Fatalf("CancelMission returned error: %v", err)
	}
	if len(runStore.cancelled) != 0 {
		t.Fatalf("superseded run was cancelled: %+v", runStore.cancelled)
	}
}

func TestCommandServiceRejectsCancellationWithoutReason(t *testing.T) {
	service := newAuthorizedCommandService(nil)
	service.SetRuntimeCommandPorts(&capturingRunStore{}, missioncontrol.NewMemoryProjectionRepository(), &capturingMissionEventSink{}, &sequenceContextIDGenerator{}, fixedClock{})

	_, err := service.CancelMission(context.Background(), missioncontrol.ViewerRef{ViewerID: "operator", ViewerKind: missioncontrol.ViewerKindUser}, missioncontrol.CancelMissionCommand{
		IdempotencyKey: "cancel-without-reason", MissionID: "mission-1",
	})
	if err != missioncontrol.ErrInvalidRequest {
		t.Fatalf("CancelMission error = %v", err)
	}
}

func TestCommandServiceProjectsPersistedCancellationAfterCheckpointDivergence(t *testing.T) {
	ctx := context.Background()
	at := time.Date(2026, 8, 24, 12, 12, 38, 0, time.UTC)
	repo := missioncontrol.NewMemoryProjectionRepository()
	model := appRuntimeReadModel(at.Add(-time.Hour))
	model.Checkpoint.Version = 58
	if err := repo.Save(ctx, model); err != nil {
		t.Fatal(err)
	}
	events := &capturingMissionEventJournal{events: make([]missionevent.Envelope, 44)}
	events.events = append(events.events, missionevent.Envelope{
		EventID: "evt-persisted-cancel", MissionID: "mission-1", StreamID: "mission-1", StreamVersion: 45,
		EventType: missionevent.EventMissionCancelled, EventVersion: 1,
		Actor:          missionevent.ActorRef{Kind: missionevent.ActorKindUser, ID: "operator"},
		IdempotencyKey: "cancel-diverged", OccurredAt: at,
		Payload: missionevent.MissionClosedPayload{Reason: "polluted verification Mission"},
	})
	service := newAuthorizedCommandService(nil)
	service.SetRuntimeCommandPorts(&capturingRunStore{}, repo, events, &sequenceContextIDGenerator{values: []string{"evt-retry"}}, fixedClock{at: at.Add(time.Minute)})
	service.SetRuntimeEventReader(events)

	receipt, err := service.CancelMission(ctx, missioncontrol.ViewerRef{ViewerID: "operator", ViewerKind: missioncontrol.ViewerKindUser}, missioncontrol.CancelMissionCommand{
		IdempotencyKey: "cancel-diverged", MissionID: "mission-1", Reason: "polluted verification Mission",
	})
	if err != nil {
		t.Fatal(err)
	}
	updated, err := repo.GetMissionControl(ctx, "mission-1")
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Version != 59 || updated.Checkpoint.Version != 59 || updated.Mission.Status != projection.MissionStatusCancelled || updated.TaskBoard.Tasks[0].Status != projection.TaskStatusCancelled {
		t.Fatalf("receipt/projection = %+v / %+v", receipt, updated)
	}
	if len(events.events) != 45 || events.events[44].StreamVersion != 45 {
		t.Fatalf("durable events changed = %+v", events.events[44:])
	}
	latest := updated.ProgressGraph.Nodes[len(updated.ProgressGraph.Nodes)-1]
	if latest.EventID != "evt-persisted-cancel" || latest.Status != "cancelled" {
		t.Fatalf("latest progress = %+v", latest)
	}
}

func TestCommandServiceProjectsTodoPauseAfterCheckpointDivergence(t *testing.T) {
	ctx := context.Background()
	at := time.Date(2026, 8, 24, 12, 15, 0, 0, time.UTC)
	repo := missioncontrol.NewMemoryProjectionRepository()
	model := appRuntimeReadModel(at)
	model.Mission.OriginKind = "todo"
	model.Mission.OriginTodoID = "todo-1"
	model.Checkpoint.Version = 58
	if err := repo.Save(ctx, model); err != nil {
		t.Fatal(err)
	}
	events := &capturingMissionEventJournal{events: make([]missionevent.Envelope, 44)}
	service := newAuthorizedCommandService(nil)
	service.SetRuntimeCommandPorts(&capturingRunStore{}, repo, events, &sequenceContextIDGenerator{values: []string{"evt-pause"}}, fixedClock{at: at.Add(time.Minute)})
	service.SetRuntimeEventReader(events)

	err := service.PauseTodoMission(ctx, missioncontrol.ViewerRef{ViewerID: "operator", ViewerKind: missioncontrol.ViewerKindUser}, missioncontrol.PauseTodoMissionCommand{
		IdempotencyKey: "pause-diverged", MissionID: "mission-1", TodoID: "todo-1", Reason: "scope changed",
	})
	if err != nil {
		t.Fatal(err)
	}
	updated, err := repo.GetMissionControl(ctx, "mission-1")
	if err != nil {
		t.Fatal(err)
	}
	if updated.Checkpoint.Version != 59 || updated.Mission.Status != projection.MissionStatusPaused || len(events.events) != 45 || events.events[44].StreamVersion != 45 {
		t.Fatalf("projection/events = %+v / %+v", updated, events.events[44:])
	}
}

type capturingMissionEventJournal struct {
	events []missionevent.Envelope
}

func (s *capturingMissionEventJournal) Append(_ context.Context, event missionevent.Envelope) error {
	for _, existing := range s.events {
		if event.IdempotencyKey != "" && existing.IdempotencyKey == event.IdempotencyKey {
			return nil
		}
	}
	s.events = append(s.events, event)
	return nil
}

func (s *capturingMissionEventJournal) LoadMission(_ context.Context, _ missioncore.MissionID) ([]missionevent.Envelope, error) {
	return append([]missionevent.Envelope(nil), s.events...), nil
}

func (s *capturingMissionEventJournal) LoadMissionForTenant(_ context.Context, _ string, _ missioncore.MissionID) ([]missionevent.Envelope, error) {
	return append([]missionevent.Envelope(nil), s.events...), nil
}
