package evidence

import (
	"testing"

	agentcore "orrery_v2/internal/agent/core"
	missiontask "orrery_v2/internal/mission/task"
)

func TestValidatorRequiresEvidenceForRequiredRequirements(t *testing.T) {
	requirements := []missiontask.EvidenceRequirement{
		{ID: missiontask.EvidenceRequirementID("evidence-required-001"), Kind: missiontask.EvidenceRequirementKindTestLog, Required: true},
	}

	errs := NewValidator(requirements, nil).Validate(nil)

	assertErrorCode(t, errs, CodeMissingRequiredEvidence)
}

func TestValidatorRequiresEvidenceToMapAcceptanceCriteria(t *testing.T) {
	requirements := []missiontask.EvidenceRequirement{
		{ID: missiontask.EvidenceRequirementID("evidence-required-001"), Kind: missiontask.EvidenceRequirementKindTestLog, Required: true},
	}
	criteria := []missiontask.AcceptanceCriterion{
		{ID: missiontask.AcceptanceCriterionID("acceptance-001"), Description: "tests pass"},
	}
	items := []agentcore.Evidence{{
		ID:                    agentcore.EvidenceID("evidence-001"),
		EvidenceRequirementID: "evidence-required-001",
		Kind:                  agentcore.EvidenceKindTestLog,
		ToolLogRef:            agentcore.ToolLogRef{ID: agentcore.ToolLogID("test-log-001")},
	}}

	errs := NewValidator(requirements, criteria).Validate(items)

	assertErrorCode(t, errs, CodeMissingAcceptanceMapping)
}

func TestValidatorRejectsEvidenceKindMismatchAndMissingSource(t *testing.T) {
	requirements := []missiontask.EvidenceRequirement{
		{ID: missiontask.EvidenceRequirementID("evidence-required-001"), Kind: missiontask.EvidenceRequirementKindApprovalRecord, Required: true},
	}
	items := []agentcore.Evidence{{
		ID:                    agentcore.EvidenceID("evidence-001"),
		EvidenceRequirementID: "evidence-required-001",
		Kind:                  agentcore.EvidenceKindTestLog,
	}}

	errs := NewValidator(requirements, nil).Validate(items)

	assertErrorCode(t, errs, CodeEvidenceKindMismatch)
	assertErrorCode(t, errs, CodeUntraceableEvidence)
}

func assertErrorCode(t *testing.T, errs []ValidationError, code ValidationCode) {
	t.Helper()
	for _, err := range errs {
		if err.Code == code {
			return
		}
	}
	t.Fatalf("expected validation code %s in %#v", code, errs)
}
