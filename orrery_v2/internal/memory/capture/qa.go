package capture

import (
	"strings"

	agentcore "orrery_v2/internal/agent/core"
	memcore "orrery_v2/internal/memory/core"
	missiontask "orrery_v2/internal/mission/task"
)

func BuildQADraft(qa missiontask.QAResultRecord, missionID string) CandidateDraft {
	evidenceRefs := make([]string, 0, len(qa.EvidenceResults)+len(qa.CheckedCriteria)+len(qa.MissingEvidence))
	for _, result := range qa.EvidenceResults {
		if result.EvidenceID != "" {
			evidenceRefs = append(evidenceRefs, "evidence:"+string(result.EvidenceID))
		}
	}
	for _, criterion := range qa.CheckedCriteria {
		if criterion.CriterionID != "" {
			evidenceRefs = append(evidenceRefs, "acceptance:"+string(criterion.CriterionID))
		}
	}
	for _, missing := range qa.MissingEvidence {
		if missing != "" {
			evidenceRefs = append(evidenceRefs, "missing_evidence:"+string(missing))
		}
	}
	summary := "QA " + string(qa.Status)
	if len(qa.RejectionReasons) > 0 {
		summary = strings.TrimSpace(summary + ": " + strings.Join(qa.RejectionReasons, "; "))
	}
	return CandidateDraft{
		ID:            "candidate.qa_result." + idSegment(string(qa.ID)),
		MissionID:     missionID,
		SourceType:    memcore.SourceQAResult,
		SourceRef:     string(qa.ID),
		ProposedScope: memcore.MemoryScope{Kind: memcore.ScopeMission, ID: missionID},
		ProposedKind:  agentcore.MemoryKindMission,
		Layer:         memcore.MemoryLayerFact,
		Topic:         "qa",
		Summary:       summary,
		Tags:          []string{"qa", string(qa.Status)},
		EvidenceRefs:  evidenceRefs,
		LineageRefs:   []string{"qa_result:" + string(qa.ID)},
		CreatedBy:     qa.CheckedBy,
		CreatedAt:     qa.CheckedAt,
		UpdatedAt:     qa.CheckedAt,
	}
}
