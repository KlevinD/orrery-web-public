package event

import (
	"context"
	"testing"

	agentcore "orrery_v2/internal/agent/core"
	missiontask "orrery_v2/internal/mission/task"
	"orrery_v2/internal/platform/eventstore"
	"orrery_v2/internal/platform/testkit"
	"orrery_v2/internal/platform/uow"
)

func TestTaskEventSinkMapsDeliveryEventsToStableMissionPayloads(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	sink := NewTaskEventSink()

	err := env.UnitOfWork.Do(ctx, func(ctx context.Context, tx uow.Tx) error {
		return sink.AppendTaskEvent(ctx, tx, missiontask.TaskEvent{
			Type:           missiontask.TaskEventDeliverableSubmitted,
			TaskID:         missiontask.TaskID("task-001"),
			MissionID:      "mission-001",
			ActorID:        agentcore.AgentID("agent-worker-001"),
			ActorProfileID: agentcore.AgentProfileID("agent-worker-001"),
			Payload: missiontask.DeliverableSubmission{
				TaskID:  missiontask.TaskID("task-001"),
				AgentID: agentcore.AgentID("agent-worker-001"),
				Deliverable: agentcore.Deliverable{
					ID:                    agentcore.DeliverableID("deliverable-001"),
					ExpectedDeliverableID: "expected-001",
					Title:                 "Patch",
					ArtifactRef:           agentcore.ArtifactRef{ID: "artifact-001", Kind: agentcore.ArtifactKindCode},
				},
			},
			OccurredAt: env.Clock.Now(),
		})
	})
	if err != nil {
		t.Fatalf("append task event: %v", err)
	}

	events, err := NewStoreReader(eventstore.New(env.DB)).LoadMission(ctx, "mission-001")
	if err != nil {
		t.Fatalf("load mission events: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("event count = %d, want 1", len(events))
	}
	payload, ok := events[0].Payload.(DeliverableSubmittedPayload)
	if !ok {
		t.Fatalf("payload type = %T, want DeliverableSubmittedPayload", events[0].Payload)
	}
	if payload.DeliverableID != "deliverable-001" || payload.ExpectedDeliverableID != "expected-001" {
		t.Fatalf("unexpected payload: %#v", payload)
	}
}

func TestTaskEventSinkMapsQARequestWithStableReference(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	sink := NewTaskEventSink()

	err := env.UnitOfWork.Do(ctx, func(ctx context.Context, tx uow.Tx) error {
		return sink.AppendTaskEvent(ctx, tx, missiontask.TaskEvent{
			Type:           missiontask.TaskEventQARequested,
			TaskID:         "task-qa-request",
			MissionID:      "mission-qa-request",
			ActorID:        "agent-worker",
			ActorProfileID: "agent-worker",
			Payload: map[string]any{
				"qa_ref":  missiontask.QARequestRef("task-qa-request"),
				"status":  "requested",
				"message": "Review this result",
			},
			OccurredAt: env.Clock.Now(),
		})
	})
	if err != nil {
		t.Fatalf("append QA requested event: %v", err)
	}

	events, err := NewStoreReader(eventstore.New(env.DB)).LoadMission(ctx, "mission-qa-request")
	if err != nil {
		t.Fatalf("load mission events: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("event count = %d, want 1", len(events))
	}
	payload, ok := events[0].Payload.(QAPayload)
	if !ok || payload.QARef != missiontask.QARequestRef("task-qa-request") || payload.Message != "Review this result" {
		t.Fatalf("QA requested payload = %#v", events[0].Payload)
	}
}

func TestTaskEventSinkMapsQAResultsToTheStableRequestReference(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	sink := NewTaskEventSink()
	taskID := missiontask.TaskID("task-qa-result")

	err := env.UnitOfWork.Do(ctx, func(ctx context.Context, tx uow.Tx) error {
		for _, eventType := range []missiontask.TaskEventType{missiontask.TaskEventQAApproved, missiontask.TaskEventQARejected} {
			if err := sink.AppendTaskEvent(ctx, tx, missiontask.TaskEvent{
				Type: eventType, TaskID: taskID, MissionID: "mission-qa-result", ActorID: "agent-qa", ActorProfileID: "agent-qa",
				Payload:    missiontask.QAResultRecord{ID: missiontask.QAResultID("result-" + string(eventType)), TaskID: taskID, Status: missiontask.QAStatusApproved},
				OccurredAt: env.Clock.Now(),
			}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("append QA result events: %v", err)
	}

	events, err := NewStoreReader(eventstore.New(env.DB)).LoadMission(ctx, "mission-qa-result")
	if err != nil {
		t.Fatalf("load mission events: %v", err)
	}
	for _, event := range events {
		payload, ok := event.Payload.(QAPayload)
		if !ok || payload.QARef != missiontask.QARequestRef(taskID) {
			t.Fatalf("QA result payload = %#v", event.Payload)
		}
	}
}

func TestTaskEventSinkUsesStableActorProfileIDForEnvelopeActor(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	sink := NewTaskEventSink()

	err := env.UnitOfWork.Do(ctx, func(ctx context.Context, tx uow.Tx) error {
		return sink.AppendTaskEvent(ctx, tx, missiontask.TaskEvent{
			Type:           missiontask.TaskEventTaskAccepted,
			TaskID:         missiontask.TaskID("task-001"),
			MissionID:      "mission-001",
			ActorID:        agentcore.AgentID("agent-worker-001:run-001"),
			ActorProfileID: agentcore.AgentProfileID("agent-worker-001"),
			Payload:        map[string]any{"status": "accepted"},
			OccurredAt:     env.Clock.Now(),
		})
	})
	if err != nil {
		t.Fatalf("append task event: %v", err)
	}

	events, err := NewStoreReader(eventstore.New(env.DB)).LoadMission(ctx, "mission-001")
	if err != nil {
		t.Fatalf("load mission events: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("event count = %d, want 1", len(events))
	}
	if events[0].Actor.ID != "agent-worker-001" {
		t.Fatalf("event actor id = %s, want stable actor profile", events[0].Actor.ID)
	}
}

func TestTaskEventSinkRejectsMissingActorProfileID(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	sink := NewTaskEventSink()

	err := env.UnitOfWork.Do(ctx, func(ctx context.Context, tx uow.Tx) error {
		return sink.AppendTaskEvent(ctx, tx, missiontask.TaskEvent{
			Type:       missiontask.TaskEventTaskAccepted,
			TaskID:     missiontask.TaskID("task-001"),
			MissionID:  "mission-001",
			ActorID:    agentcore.AgentID("agent-worker-001"),
			Payload:    map[string]any{"status": "accepted"},
			OccurredAt: env.Clock.Now(),
		})
	})
	if err == nil {
		t.Fatal("expected missing actor profile id to be rejected")
	}
}

func TestTaskEventSinkRejectsUnknownPayloadShape(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	sink := NewTaskEventSink()

	err := env.UnitOfWork.Do(ctx, func(ctx context.Context, tx uow.Tx) error {
		return sink.AppendTaskEvent(ctx, tx, missiontask.TaskEvent{
			Type:           missiontask.TaskEventDeliverableSubmitted,
			TaskID:         missiontask.TaskID("task-001"),
			MissionID:      "mission-001",
			ActorID:        agentcore.AgentID("agent-worker-001"),
			ActorProfileID: agentcore.AgentProfileID("agent-worker-001"),
			Payload:        map[string]string{"not": "stable"},
			OccurredAt:     env.Clock.Now(),
		})
	})
	if err == nil {
		t.Fatal("expected unstable task event payload to be rejected")
	}
}

func TestTaskEventSinkMapsReworkResolvedSeparatelyFromReworkRequested(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	sink := NewTaskEventSink()

	err := env.UnitOfWork.Do(ctx, func(ctx context.Context, tx uow.Tx) error {
		return sink.AppendTaskEvent(ctx, tx, missiontask.TaskEvent{
			Type:           missiontask.TaskEventReworkResolved,
			TaskID:         missiontask.TaskID("task-001"),
			MissionID:      "mission-001",
			ActorID:        agentcore.AgentID("agent-worker-001"),
			ActorProfileID: agentcore.AgentProfileID("agent-worker-001"),
			Payload: missiontask.ReworkRequest{
				ID:             missiontask.ReworkRequestID("rework-001"),
				OriginalTaskID: missiontask.TaskID("task-001"),
				Reason:         "fixed missing evidence",
			},
			OccurredAt: env.Clock.Now(),
		})
	})
	if err != nil {
		t.Fatalf("append rework resolved event: %v", err)
	}

	events, err := NewStoreReader(eventstore.New(env.DB)).LoadMission(ctx, "mission-001")
	if err != nil {
		t.Fatalf("load mission events: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("event count = %d, want 1", len(events))
	}
	if events[0].EventType != EventReworkResolved {
		t.Fatalf("event type = %s, want %s", events[0].EventType, EventReworkResolved)
	}
	payload, ok := events[0].Payload.(ReworkResolvedPayload)
	if !ok {
		t.Fatalf("payload type = %T, want ReworkResolvedPayload", events[0].Payload)
	}
	if payload.ReworkRef != "rework-001" || payload.TaskID != "task-001" {
		t.Fatalf("unexpected resolved payload: %#v", payload)
	}
}

func TestTaskEventSinkMapsQASkippedAsApprovedPayload(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	sink := NewTaskEventSink()

	err := env.UnitOfWork.Do(ctx, func(ctx context.Context, tx uow.Tx) error {
		return sink.AppendTaskEvent(ctx, tx, missiontask.TaskEvent{
			Type:           missiontask.TaskEventQASkipped,
			TaskID:         missiontask.TaskID("task-001"),
			MissionID:      "mission-001",
			ActorID:        agentcore.AgentID("agent-worker-001"),
			ActorProfileID: agentcore.AgentProfileID("agent-worker-001"),
			Payload: missiontask.QASkipRecord{
				ID:           missiontask.QASkipID("qa-skip-submission-001"),
				TaskID:       missiontask.TaskID("task-001"),
				SubmissionID: missiontask.TaskSubmissionID("submission-001"),
				AgentID:      agentcore.AgentID("agent-worker-001"),
				Reason:       "qa check disabled",
				SkippedAt:    env.Clock.Now(),
			},
			OccurredAt: env.Clock.Now(),
		})
	})
	if err != nil {
		t.Fatalf("append QA skipped event: %v", err)
	}

	events, err := NewStoreReader(eventstore.New(env.DB)).LoadMission(ctx, "mission-001")
	if err != nil {
		t.Fatalf("load mission events: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("event count = %d, want 1", len(events))
	}
	if events[0].EventType != EventQASkipped {
		t.Fatalf("event type = %s, want %s", events[0].EventType, EventQASkipped)
	}
	payload, ok := events[0].Payload.(QAPayload)
	if !ok {
		t.Fatalf("payload type = %T, want QAPayload", events[0].Payload)
	}
	if payload.QARef != "qa-skip-submission-001" || payload.Status != string(missiontask.QAStatusApproved) || payload.Reason != "qa check disabled" {
		t.Fatalf("unexpected skip payload: %#v", payload)
	}
}

func TestTaskEventSinkMapsHistoricalTaskStatuses(t *testing.T) {
	tests := []struct {
		name      string
		taskEvent missiontask.TaskEventType
		wantEvent EventType
	}{
		{name: "submitted", taskEvent: missiontask.TaskEventTaskSubmitted, wantEvent: EventTaskSubmitted},
		{name: "blocked", taskEvent: missiontask.TaskEventTaskBlocked, wantEvent: EventTaskBlocked},
		{name: "cancelled", taskEvent: missiontask.TaskEventTaskCancelled, wantEvent: EventTaskCancelled},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			env := testkit.New(t)
			defer env.Close()
			err := env.UnitOfWork.Do(ctx, func(ctx context.Context, tx uow.Tx) error {
				return NewTaskEventSink().AppendTaskEvent(ctx, tx, missiontask.TaskEvent{
					Type: test.taskEvent, TaskID: "task-history", MissionID: "mission-history",
					ActorID: "agent-history", ActorProfileID: "agent-history",
					Payload: map[string]any{"reason": "stored historical outcome"}, OccurredAt: env.Clock.Now(),
				})
			})
			if err != nil {
				t.Fatalf("append historical Task event: %v", err)
			}
			events, err := NewStoreReader(eventstore.New(env.DB)).LoadMission(ctx, "mission-history")
			if err != nil {
				t.Fatal(err)
			}
			if len(events) != 1 || events[0].EventType != test.wantEvent {
				t.Fatalf("historical Task events = %+v, want %s", events, test.wantEvent)
			}
			payload, ok := events[0].Payload.(TaskStatusPayload)
			if !ok || payload.TaskID != "task-history" || payload.Reason != "stored historical outcome" {
				t.Fatalf("historical Task payload = %#v", events[0].Payload)
			}
		})
	}
}
