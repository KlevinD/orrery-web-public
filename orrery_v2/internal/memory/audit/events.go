package audit

import "time"

type EventType string

const (
	EventMemoryCandidateCreated            EventType = "memory_candidate_created"
	EventMemoryCandidateAccepted           EventType = "memory_candidate_accepted"
	EventMemoryCandidateRejected           EventType = "memory_candidate_rejected"
	EventMemoryCandidateMerged             EventType = "memory_candidate_merged"
	EventMemoryRecordCreated               EventType = "memory_record_created"
	EventMemoryRecordUpdated               EventType = "memory_record_updated"
	EventMemoryRecordDisabled              EventType = "memory_record_disabled"
	EventMemoryRecordReferenced            EventType = "memory_record_referenced"
	EventMemoryRecordArchived              EventType = "memory_record_archived"
	EventMemoryRecordAnonymized            EventType = "memory_record_anonymized"
	EventMemoryRecordDeleted               EventType = "memory_record_deleted"
	EventMemoryDigestGenerated             EventType = "memory_digest_generated"
	EventMemoryAccessDenied                EventType = "memory_access_denied"
	EventMemoryDistillationStarted         EventType = "memory_distillation_started"
	EventMemoryDistillationCompleted       EventType = "memory_distillation_completed"
	EventMemoryWorldConsolidationStarted   EventType = "memory_world_consolidation_started"
	EventMemoryWorldConsolidationCompleted EventType = "memory_world_consolidation_completed"
)

func (t EventType) String() string {
	return string(t)
}

type Event struct {
	Type           EventType
	SubjectID      string
	IdempotencyKey string `json:"-"`
	ActorID        string
	MissionID      string
	Summary        string
	SourceRefs     []string
	Reason         string
	OccurredAt     time.Time
}
