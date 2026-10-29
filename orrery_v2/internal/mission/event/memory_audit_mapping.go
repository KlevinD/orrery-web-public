package event

import (
	"fmt"

	memaudit "orrery_v2/internal/memory/audit"
	missioncore "orrery_v2/internal/mission/core"
)

type MemoryAuditMapper struct{}

type MemoryAuditMapOptions struct {
	EventID       string
	StreamVersion int
}

func NewMemoryAuditMapper() MemoryAuditMapper {
	return MemoryAuditMapper{}
}

func (MemoryAuditMapper) Map(auditEvent memaudit.Event, options MemoryAuditMapOptions) (Envelope, error) {
	eventType, payload, err := memoryAuditPayload(auditEvent)
	if err != nil {
		return Envelope{}, err
	}
	eventID := options.EventID
	if eventID == "" {
		eventID = fmt.Sprintf("memory:%s:%s", auditEvent.Type, auditEvent.SubjectID)
	}
	streamVersion := options.StreamVersion
	if streamVersion <= 0 {
		streamVersion = 1
	}
	return Envelope{
		EventID:        eventID,
		MissionID:      missioncore.MissionID(auditEvent.MissionID),
		StreamID:       auditEvent.MissionID,
		StreamVersion:  streamVersion,
		EventType:      eventType,
		EventVersion:   1,
		Actor:          ActorRef{Kind: ActorKindAgent, ID: auditEvent.ActorID},
		IdempotencyKey: fmt.Sprintf("%s:%s:%s", auditEvent.MissionID, auditEvent.Type, auditEvent.SubjectID),
		OccurredAt:     auditEvent.OccurredAt,
		Payload:        payload,
		Metadata: map[string]string{
			"source": "memory_audit",
		},
	}, nil
}

func memoryAuditPayload(event memaudit.Event) (EventType, Payload, error) {
	switch event.Type {
	case memaudit.EventMemoryCandidateCreated:
		return EventMemoryCandidateCreated, MemoryCandidatePayload{CandidateID: event.SubjectID, Summary: event.Summary, SourceRefs: event.SourceRefs, Reason: event.Reason}, nil
	case memaudit.EventMemoryCandidateAccepted:
		return EventMemoryCandidateAccepted, MemoryCandidatePayload{CandidateID: event.SubjectID, Summary: event.Summary, SourceRefs: event.SourceRefs, Reason: event.Reason}, nil
	case memaudit.EventMemoryRecordCreated:
		return EventMemoryRecordCreated, MemoryRecordPayload{MemoryID: event.SubjectID, Summary: event.Summary, SourceRefs: event.SourceRefs, Reason: event.Reason}, nil
	case memaudit.EventMemoryRecordUpdated:
		return EventMemoryRecordUpdated, MemoryRecordPayload{MemoryID: event.SubjectID, Summary: event.Summary, SourceRefs: event.SourceRefs, Reason: event.Reason}, nil
	case memaudit.EventMemoryRecordDisabled:
		return EventMemoryRecordDisabled, MemoryRecordPayload{MemoryID: event.SubjectID, Summary: event.Summary, SourceRefs: event.SourceRefs, Reason: event.Reason}, nil
	case memaudit.EventMemoryDigestGenerated:
		return EventMemoryDigestGenerated, MemoryDigestPayload{DigestID: event.SubjectID, Summary: event.Summary, SourceRefs: event.SourceRefs}, nil
	case memaudit.EventMemoryAccessDenied:
		return EventMemoryAccessDenied, MemoryAccessDeniedPayload{SubjectID: event.SubjectID, Reason: event.Reason}, nil
	default:
		return "", nil, fmt.Errorf("%w: unsupported memory audit event %s", ErrUnknownEventType, event.Type)
	}
}
