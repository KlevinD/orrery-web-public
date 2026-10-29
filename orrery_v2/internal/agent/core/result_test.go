package core

import (
	"testing"

	missioncore "orrery_v2/internal/mission/core"
)

func TestValidateAgentResultRequiresSummary(t *testing.T) {
	result := validAgentResult()
	result.Summary = Summary{}

	errs := ValidateAgentResult(result)

	assertValidationError(t, errs, CodeMissingSummary, "summary")
}

func TestValidateAgentResultAcceptsDeliverablesEvidenceAndTraceRef(t *testing.T) {
	result := validAgentResult()

	if errs := ValidateAgentResult(result); len(errs) != 0 {
		t.Fatalf("ValidateAgentResult returned errors: %#v", errs)
	}
}

func TestEvidenceRequiresSourceMatchingKind(t *testing.T) {
	tests := []struct {
		name     string
		evidence Evidence
	}{
		{
			name:     "artifact evidence",
			evidence: Evidence{Kind: EvidenceKindArtifact, ArtifactRef: ArtifactRef{ID: "artifact-001"}},
		},
		{
			name:     "tool log evidence",
			evidence: Evidence{Kind: EvidenceKindToolLog, ToolLogRef: ToolLogRef{ID: ToolLogID("tool-log-001")}},
		},
		{
			name:     "approval evidence",
			evidence: Evidence{Kind: EvidenceKindApprovalRecord, ApprovalRecordRef: ApprovalRecordRef{ID: ApprovalRecordID("approval-001")}},
		},
		{
			name:     "human submission evidence",
			evidence: Evidence{Kind: EvidenceKindHumanSubmission, HumanSubmissionRef: HumanSubmissionRef{ID: HumanSubmissionID("human-submission-001")}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !tt.evidence.HasTraceableSource() {
				t.Fatalf("expected traceable source for %#v", tt.evidence)
			}
		})
	}

	if (Evidence{Kind: EvidenceKindToolLog, ArtifactRef: ArtifactRef{ID: "wrong-source"}}).HasTraceableSource() {
		t.Fatal("tool log evidence should require ToolLogRef")
	}
}

func validAgentResult() AgentResult {
	return AgentResult{
		ID:        AgentResultID("result-001"),
		MissionID: missioncore.MissionID("mission-001"),
		TaskID:    missioncore.TaskID("task-001"),
		AgentID:   AgentID("agent-worker-001"),
		RunID:     AgentRunID("run-001"),
		Summary: Summary{
			Text: "Implemented core contracts with tests.",
		},
		Deliverables: []Deliverable{{
			ID:                    DeliverableID("deliverable-001"),
			ExpectedDeliverableID: "expected-001",
			Title:                 "Core contract package",
			ArtifactRef:           ArtifactRef{ID: "artifact-001", Kind: ArtifactKindCode},
		}},
		Evidence: []Evidence{{
			ID:                    EvidenceID("evidence-001"),
			EvidenceRequirementID: "evidence-required-001",
			AcceptanceCriterionID: "acceptance-001",
			Kind:                  EvidenceKindTestLog,
			ToolLogRef:            ToolLogRef{ID: ToolLogID("test-log-001")},
		}},
		Decisions: []Decision{{ID: DecisionID("decision-001"), Summary: "Keep contracts pure"}},
		Requests:  []AgentRequest{{ID: AgentRequestID("request-001"), Kind: AgentRequestKindReview, Summary: "Review contract names"}},
	}
}
