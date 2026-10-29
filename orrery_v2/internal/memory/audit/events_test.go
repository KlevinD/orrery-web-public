package audit

import "testing"

func TestAuditEventTypesCoverStageFiveMemorySemantics(t *testing.T) {
	required := []EventType{
		EventMemoryCandidateCreated,
		EventMemoryCandidateAccepted,
		EventMemoryCandidateRejected,
		EventMemoryCandidateMerged,
		EventMemoryRecordCreated,
		EventMemoryRecordUpdated,
		EventMemoryRecordDisabled,
		EventMemoryDigestGenerated,
		EventMemoryAccessDenied,
		EventMemoryDistillationStarted,
		EventMemoryDistillationCompleted,
		EventMemoryWorldConsolidationStarted,
		EventMemoryWorldConsolidationCompleted,
		EventMemoryRecordReferenced,
		EventMemoryRecordArchived,
		EventMemoryRecordAnonymized,
		EventMemoryRecordDeleted,
	}
	for _, eventType := range required {
		if eventType.String() == "" {
			t.Fatalf("empty audit event type: %q", eventType)
		}
	}
}
