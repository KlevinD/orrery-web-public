package audit_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	memaudit "orrery_v2/internal/memory/audit"
	"orrery_v2/internal/platform"
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/eventstore"
	"orrery_v2/internal/platform/idgen"
)

func TestEventRecorderWritesMemoryAuditToPlatformRecorder(t *testing.T) {
	ctx := context.Background()
	recorder := &capturingPlatformEventRecorder{}
	audit := memaudit.NewEventRecorder(recorder)

	err := audit.RecordMemoryAudit(ctx, memaudit.Event{
		Type:      memaudit.EventMemoryCandidateCreated,
		SubjectID: "candidate-1",
		ActorID:   "agent-1",
		MissionID: "mission-1",
		Summary:   "Candidate created",
	})
	if err != nil {
		t.Fatalf("RecordMemoryAudit returned error: %v", err)
	}
	if recorder.message.StreamID != "mission-1" || recorder.message.EventType != memaudit.EventMemoryCandidateCreated.String() || recorder.message.EventVersion != memaudit.EventVersion {
		t.Fatalf("platform event message = %+v", recorder.message)
	}
	if recorder.message.IdempotencyKey != "memory_candidate_created|candidate-1" {
		t.Fatalf("idempotency key = %q", recorder.message.IdempotencyKey)
	}
	var payload memaudit.Event
	if err := json.Unmarshal(recorder.message.Payload, &payload); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if payload.SubjectID != "candidate-1" || payload.ActorID != "agent-1" {
		t.Fatalf("payload = %+v", payload)
	}
}

func TestEventRecorderUsesExplicitIdempotencyKeyWithoutPublishingIt(t *testing.T) {
	recorder := &capturingPlatformEventRecorder{}
	audit := memaudit.NewEventRecorder(recorder)

	err := audit.RecordMemoryAudit(context.Background(), memaudit.Event{
		Type:           memaudit.EventMemoryRecordUpdated,
		SubjectID:      "record-1",
		IdempotencyKey: "memory_record_updated|record-1|candidate-2",
	})
	if err != nil {
		t.Fatalf("RecordMemoryAudit returned error: %v", err)
	}
	if recorder.message.IdempotencyKey != "memory_record_updated|record-1|candidate-2" {
		t.Fatalf("idempotency key = %q", recorder.message.IdempotencyKey)
	}
	if strings.Contains(string(recorder.message.Payload), "candidate-2") {
		t.Fatalf("internal idempotency key leaked into audit payload: %s", recorder.message.Payload)
	}
}

func TestEventRecorderAppendsMultipleAuditEventsToSameMissionStream(t *testing.T) {
	ctx := context.Background()
	store, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: t.Name()})
	if err != nil {
		t.Fatalf("Open db: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	recorder := platform.NewEventRecorder(eventstore.New(store), idgen.NewDeterministic("audit-event"))
	audit := memaudit.NewEventRecorder(recorder)

	for _, event := range []memaudit.Event{
		{
			Type:      memaudit.EventMemoryCandidateCreated,
			SubjectID: "candidate-1",
			ActorID:   "agent-1",
			MissionID: "mission-1",
			Summary:   "First candidate created",
		},
		{
			Type:      memaudit.EventMemoryCandidateCreated,
			SubjectID: "candidate-2",
			ActorID:   "agent-1",
			MissionID: "mission-1",
			Summary:   "Second candidate created",
		},
	} {
		if err := audit.RecordMemoryAudit(ctx, event); err != nil {
			t.Fatalf("RecordMemoryAudit(%s): %v", event.SubjectID, err)
		}
	}

	events, err := recorder.Load(ctx, "mission-1")
	if err != nil {
		t.Fatalf("Load events: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("events = %+v, want two memory audit events", events)
	}
	if events[0].StreamVersion != 1 || events[1].StreamVersion != 2 {
		t.Fatalf("stream versions = %d/%d, want 1/2", events[0].StreamVersion, events[1].StreamVersion)
	}
}

func TestEventRecorderRetriesStreamConflictWithFreshStreamVersion(t *testing.T) {
	ctx := context.Background()
	recorder := &scriptedPlatformEventRecorder{
		loadResults: [][]platform.RecordedEvent{
			{{StreamID: "mission-1", StreamVersion: 1}},
			{{StreamID: "mission-1", StreamVersion: 1}, {StreamID: "mission-1", StreamVersion: 2}},
		},
		recordErrors: []error{eventstore.ErrConflict, nil},
	}
	audit := memaudit.NewEventRecorder(recorder)

	err := audit.RecordMemoryAudit(ctx, memaudit.Event{
		Type:      memaudit.EventMemoryCandidateCreated,
		SubjectID: "candidate-3",
		ActorID:   "agent-1",
		MissionID: "mission-1",
		Summary:   "Candidate created after concurrent write",
	})
	if err != nil {
		t.Fatalf("RecordMemoryAudit returned error: %v", err)
	}
	if len(recorder.messages) != 2 {
		t.Fatalf("recorded messages = %+v, want two attempts", recorder.messages)
	}
	if recorder.messages[0].StreamVersion != 2 || recorder.messages[1].StreamVersion != 3 {
		t.Fatalf("attempted stream versions = %d/%d, want 2/3", recorder.messages[0].StreamVersion, recorder.messages[1].StreamVersion)
	}
}

type capturingPlatformEventRecorder struct {
	message platform.EventMessage
}

func (r *capturingPlatformEventRecorder) Record(_ context.Context, message platform.EventMessage) (string, error) {
	r.message = message
	return "event-1", nil
}

type scriptedPlatformEventRecorder struct {
	loadResults  [][]platform.RecordedEvent
	recordErrors []error
	messages     []platform.EventMessage
}

func (r *scriptedPlatformEventRecorder) Record(_ context.Context, message platform.EventMessage) (string, error) {
	r.messages = append(r.messages, message)
	if len(r.recordErrors) == 0 {
		return "event-scripted", nil
	}
	err := r.recordErrors[0]
	r.recordErrors = r.recordErrors[1:]
	if err != nil {
		return "", err
	}
	return "event-scripted", nil
}

func (r *scriptedPlatformEventRecorder) Load(_ context.Context, _ string) ([]platform.RecordedEvent, error) {
	if len(r.loadResults) == 0 {
		return nil, nil
	}
	result := r.loadResults[0]
	r.loadResults = r.loadResults[1:]
	return result, nil
}
