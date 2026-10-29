package qa

import (
	"testing"

	agentcore "orrery_v2/internal/agent/core"
	missiontask "orrery_v2/internal/mission/task"
)

func TestCheckerRejectsMissingRequiredEvidence(t *testing.T) {
	checker := NewChecker(QAContract{
		ID: "qa-contract-001",
		EvidenceRequirements: []missiontask.EvidenceRequirement{{
			ID:       missiontask.EvidenceRequirementID("evidence-required-001"),
			Kind:     missiontask.EvidenceRequirementKindTestLog,
			Required: true,
		}},
	}, QAPolicy{EvidenceStrictness: EvidenceStrictnessRequired, RequireReasonOnReject: true})

	result := checker.Check(QACheckInput{
		TaskID:    missiontask.TaskID("task-001"),
		CheckedBy: agentcore.AgentID("qa-agent"),
		Evidence:  nil,
	})

	if result.Status != QAStatusRejected {
		t.Fatalf("status = %s, want rejected", result.Status)
	}
	if len(result.MissingEvidence) != 1 || len(result.RejectionReasons) == 0 {
		t.Fatalf("missing evidence not reported: %#v", result)
	}
}

func TestCheckerApprovesWhenCriteriaAndEvidencePass(t *testing.T) {
	checker := NewChecker(QAContract{
		ID: "qa-contract-001",
		AcceptanceCriteria: []missiontask.AcceptanceCriterion{{
			ID:          missiontask.AcceptanceCriterionID("acceptance-001"),
			Description: "tests pass",
		}},
		EvidenceRequirements: []missiontask.EvidenceRequirement{{
			ID:       missiontask.EvidenceRequirementID("evidence-required-001"),
			Kind:     missiontask.EvidenceRequirementKindTestLog,
			Required: true,
		}},
	}, QAPolicy{EvidenceStrictness: EvidenceStrictnessRequired, RequireAcceptanceReport: true})
	input := QACheckInput{
		TaskID:    missiontask.TaskID("task-001"),
		CheckedBy: agentcore.AgentID("qa-agent"),
		Evidence: []agentcore.Evidence{{
			ID:                    agentcore.EvidenceID("evidence-001"),
			EvidenceRequirementID: "evidence-required-001",
			AcceptanceCriterionID: "acceptance-001",
			Kind:                  agentcore.EvidenceKindTestLog,
			ToolLogRef:            agentcore.ToolLogRef{ID: agentcore.ToolLogID("test-log-001")},
		}},
	}

	result := checker.Check(input)

	if result.Status != QAStatusApproved {
		t.Fatalf("status = %s, want approved: %#v", result.Status, result)
	}
	if len(result.CheckedCriteria) != 1 || len(result.EvidenceResults) != 1 {
		t.Fatalf("result did not capture checks: %#v", result)
	}
}

func TestCheckerRequiresRejectionReasonsWhenPolicyDemandsThem(t *testing.T) {
	checker := NewChecker(QAContract{}, QAPolicy{RequireReasonOnReject: true})
	result := checker.Normalize(QAResult{
		TaskID:    missiontask.TaskID("task-001"),
		CheckedBy: agentcore.AgentID("qa-agent"),
		Status:    QAStatusRejected,
	})

	if result.Status != QAStatusBlocked {
		t.Fatalf("status = %s, want blocked", result.Status)
	}
	if len(result.RejectionReasons) == 0 {
		t.Fatalf("expected policy reason: %#v", result)
	}
}
