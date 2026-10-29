package event_test

import (
	"testing"
	"time"

	memaudit "orrery_v2/internal/memory/audit"
	missionevent "orrery_v2/internal/mission/event"
)

func TestMemoryAuditMapperCreatesMissionEventPayload(t *testing.T) {
	mapper := missionevent.NewMemoryAuditMapper()
	auditEvent := memaudit.Event{
		Type:       memaudit.EventMemoryCandidateCreated,
		SubjectID:  "candidate-1",
		ActorID:    "agent-a",
		MissionID:  "mission-1",
		Summary:    "Candidate summary",
		SourceRefs: []string{"message-1"},
		OccurredAt: time.Date(2026, 5, 31, 10, 0, 0, 0, time.UTC),
	}

	envelope, err := mapper.Map(auditEvent, missionevent.MemoryAuditMapOptions{
		EventID:       "event-1",
		StreamVersion: 1,
	})
	if err != nil {
		t.Fatalf("Map: %v", err)
	}
	if envelope.EventType != missionevent.EventMemoryCandidateCreated {
		t.Fatalf("event type = %s", envelope.EventType)
	}
	payload, ok := envelope.Payload.(missionevent.MemoryCandidatePayload)
	if !ok {
		t.Fatalf("payload type = %T", envelope.Payload)
	}
	if payload.CandidateID != "candidate-1" || payload.SourceRefs[0] != "message-1" {
		t.Fatalf("payload = %+v", payload)
	}
	if err := envelope.Validate(); err != nil {
		t.Fatalf("envelope should validate: %v", err)
	}
}
