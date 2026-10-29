package event_test

import (
	"context"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	missionevent "orrery_v2/internal/mission/event"
	"orrery_v2/internal/platform/testkit"
)

func TestEnvelopeValidationRequiresMissionStreamVersionAndVersionedPayload(t *testing.T) {
	env := missionevent.Envelope{
		EventID:       "evt-1",
		StreamVersion: 1,
		EventType:     missionevent.EventMissionCreated,
		EventVersion:  1,
		Actor:         missionevent.ActorRef{Kind: missionevent.ActorKindSystem, ID: "system"},
		OccurredAt:    time.Date(2026, 5, 31, 12, 0, 0, 0, time.UTC),
		Payload:       missionevent.MissionCreatedPayload{Title: "Launch", FinalGoal: "Ship"},
	}

	if err := env.Validate(); err == nil {
		t.Fatalf("Validate returned nil, want missing mission_id error")
	}

	env.MissionID = "mission-1"
	if err := env.Validate(); err != nil {
		t.Fatalf("Validate returned error for complete envelope: %v", err)
	}
	if env.StreamIDOrDefault() != "mission-1" {
		t.Fatalf("StreamIDOrDefault = %q, want mission id", env.StreamIDOrDefault())
	}
}

func TestEventSinkAppendsAndReaderLoadsMissionEventsByMissionID(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	sink := missionevent.NewStoreSink(env.EventStore)
	reader := missionevent.NewStoreReader(env.EventStore)
	occurredAt := time.Date(2026, 5, 31, 12, 30, 0, 0, time.UTC)

	err := sink.Append(ctx, missionevent.Envelope{
		EventID:       "evt-1",
		MissionID:     "mission-1",
		StreamVersion: 1,
		EventType:     missionevent.EventAgentResultSubmitted,
		EventVersion:  1,
		Actor:         missionevent.ActorRef{Kind: missionevent.ActorKindAgent, ID: "agent-1"},
		CorrelationID: "corr-1",
		OccurredAt:    occurredAt,
		Payload: missionevent.AgentResultSubmittedPayload{
			TaskID:    "task-1",
			AgentID:   "agent-1",
			ResultRef: "result-1",
			Summary:   "Implemented the slice.",
		},
		Metadata: map[string]string{"source": "test"},
	})
	if err != nil {
		t.Fatalf("Append returned error: %v", err)
	}

	events, err := reader.LoadMission(ctx, "mission-1")
	if err != nil {
		t.Fatalf("LoadMission returned error: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("loaded %d events, want 1", len(events))
	}
	got := events[0]
	if got.EventID != "evt-1" || got.MissionID != "mission-1" || got.EventType != missionevent.EventAgentResultSubmitted {
		t.Fatalf("unexpected envelope: %+v", got)
	}
	payload, ok := got.Payload.(missionevent.AgentResultSubmittedPayload)
	if !ok {
		t.Fatalf("payload type = %T, want AgentResultSubmittedPayload", got.Payload)
	}
	if payload.ResultRef != "result-1" || payload.Summary != "Implemented the slice." {
		t.Fatalf("unexpected payload: %+v", payload)
	}
	if !got.OccurredAt.Equal(occurredAt) {
		t.Fatalf("OccurredAt = %s, want %s", got.OccurredAt, occurredAt)
	}
}

func TestEventSinkIsIdempotentByBusinessKey(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	sink := missionevent.NewStoreSink(env.EventStore)
	reader := missionevent.NewStoreReader(env.EventStore)
	event := missionevent.Envelope{
		EventID:        "evt-1",
		MissionID:      "mission-1",
		StreamVersion:  1,
		EventType:      missionevent.EventTaskAccepted,
		EventVersion:   1,
		Actor:          missionevent.ActorRef{Kind: missionevent.ActorKindAgent, ID: "agent-1"},
		IdempotencyKey: "task-1:accepted",
		OccurredAt:     time.Date(2026, 5, 31, 12, 0, 0, 0, time.UTC),
		Payload: missionevent.TaskAcceptedPayload{
			TaskID:     "task-1",
			AcceptedBy: agentcore.AgentID("agent-1"),
		},
	}

	if err := sink.Append(ctx, event); err != nil {
		t.Fatalf("first append: %v", err)
	}
	if err := sink.Append(ctx, event); err != nil {
		t.Fatalf("second append: %v", err)
	}
	events, err := reader.LoadMission(ctx, "mission-1")
	if err != nil {
		t.Fatalf("LoadMission returned error: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("event count = %d, want 1", len(events))
	}
}

func TestEventStoreIsolatesSameMissionIDAndIdempotencyKeyAcrossTenants(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	sink := missionevent.NewStoreSink(env.EventStore)
	reader := missionevent.NewStoreReader(env.EventStore)
	now := time.Date(2026, 7, 25, 8, 0, 0, 0, time.UTC)
	for i, tenantID := range []string{"tenant-a", "tenant-b"} {
		event := missionevent.Envelope{
			EventID: "event-" + tenantID, TenantID: tenantID, MissionID: "mission-shared", StreamVersion: 1,
			EventType: missionevent.EventMissionCreated, EventVersion: 1,
			Actor: missionevent.ActorRef{Kind: missionevent.ActorKindUser, ID: "owner"}, IdempotencyKey: "mission-created",
			OccurredAt: now.Add(time.Duration(i) * time.Minute), Payload: missionevent.MissionCreatedPayload{
				Title: tenantID, PersonalAssistantOriginOwnerID: "owner-" + tenantID, PersonalAssistantOriginSubjectID: "subject-" + tenantID,
			},
		}
		if err := sink.Append(ctx, event); err != nil {
			t.Fatalf("Append(%s): %v", tenantID, err)
		}
	}
	for _, tenantID := range []string{"tenant-a", "tenant-b"} {
		events, err := reader.LoadMissionForTenant(ctx, tenantID, "mission-shared")
		if err != nil {
			t.Fatalf("LoadMissionForTenant(%s): %v", tenantID, err)
		}
		if len(events) != 1 || events[0].Tenant() != tenantID || events[0].StreamID != tenantID+"/mission-shared" || events[0].IdempotencyKey != "mission-created" {
			t.Fatalf("events(%s) = %+v", tenantID, events)
		}
		payload, ok := events[0].Payload.(missionevent.MissionCreatedPayload)
		if !ok || payload.Title != tenantID || payload.PersonalAssistantOriginOwnerID != "owner-"+tenantID || payload.PersonalAssistantOriginSubjectID != "subject-"+tenantID {
			t.Fatalf("payload(%s) = %#v", tenantID, events[0].Payload)
		}
	}
	global, err := reader.LoadMission(ctx, "mission-shared")
	if err != nil {
		t.Fatalf("LoadMission(global): %v", err)
	}
	if len(global) != 0 {
		t.Fatalf("global events = %+v", global)
	}
}
