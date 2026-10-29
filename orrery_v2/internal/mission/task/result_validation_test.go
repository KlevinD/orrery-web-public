package task

import (
	"testing"

	agentcore "orrery_v2/internal/agent/core"
	missioncore "orrery_v2/internal/mission/core"
)

func TestValidateAgentResultAgainstTaskContractAcceptsMatchingResult(t *testing.T) {
	contract := validTaskContract()
	result := validAgentResultForContract()

	if errs := ValidateAgentResult(contract, result); len(errs) != 0 {
		t.Fatalf("ValidateAgentResult returned errors: %#v", errs)
	}
}

func TestValidateAgentResultAgainstTaskContractRejectsMissingSummary(t *testing.T) {
	contract := validTaskContract()
	result := validAgentResultForContract()
	result.Summary = agentcore.Summary{}

	errs := ValidateAgentResult(contract, result)

	assertValidationError(t, errs, CodeMissingSummary, "summary")
}

func TestValidateAgentResultAgainstTaskContractRequiresExpectedDeliverableTrace(t *testing.T) {
	contract := validTaskContract()
	result := validAgentResultForContract()
	result.Deliverables = nil

	errs := ValidateAgentResult(contract, result)

	assertValidationError(t, errs, CodeMissingDeliverable, "deliverables")
}

func TestValidateAgentResultAgainstTaskContractRequiresRequiredEvidence(t *testing.T) {
	contract := validTaskContract()
	result := validAgentResultForContract()
	result.Evidence = nil

	errs := ValidateAgentResult(contract, result)

	assertValidationError(t, errs, CodeMissingRequiredEvidence, "evidence")
}

func TestValidateAgentResultAgainstTaskContractRequiresAcceptanceEvidenceMapping(t *testing.T) {
	contract := validTaskContract()
	result := validAgentResultForContract()
	result.Evidence[0].AcceptanceCriterionID = ""

	errs := ValidateAgentResult(contract, result)

	assertValidationError(t, errs, CodeMissingAcceptanceMapping, "evidence")
}

func TestValidateAgentResultAgainstTaskContractRequiresEvidenceSource(t *testing.T) {
	contract := validTaskContract()
	result := validAgentResultForContract()
	result.Evidence[0].ArtifactRef = agentcore.ArtifactRef{}
	result.Evidence[0].Kind = agentcore.EvidenceKindExternalURL
	result.Evidence[0].ExternalURL = ""

	errs := ValidateAgentResult(contract, result)

	assertValidationError(t, errs, CodeInvalidRef, "evidence")
}

func TestValidateAgentResultAgainstTaskContractRequiresEvidenceSourceMatchingKind(t *testing.T) {
	contract := validTaskContract()
	result := validAgentResultForContract()
	result.Evidence[0].Kind = agentcore.EvidenceKindApprovalRecord
	result.Evidence[0].ToolLogRef = agentcore.ToolLogRef{ID: agentcore.ToolLogID("wrong-source")}
	result.Evidence[0].ApprovalRecordRef = agentcore.ApprovalRecordRef{}

	errs := ValidateAgentResult(contract, result)

	assertValidationError(t, errs, CodeInvalidRef, "evidence")
}

func validAgentResultForContract() agentcore.AgentResult {
	return agentcore.AgentResult{
		ID:        agentcore.AgentResultID("result-001"),
		MissionID: missioncore.MissionID("mission-001"),
		TaskID:    missioncore.TaskID("task-001"),
		AgentID:   agentcore.AgentID("agent-worker-001"),
		Summary:   agentcore.Summary{Text: "Implemented contract packages."},
		Deliverables: []agentcore.Deliverable{{
			ID:                    agentcore.DeliverableID("deliverable-001"),
			ExpectedDeliverableID: "expected-001",
			Title:                 "Contract packages",
			ArtifactRef:           agentcore.ArtifactRef{ID: "artifact-001", Kind: agentcore.ArtifactKindCode},
		}},
		Evidence: []agentcore.Evidence{{
			ID:                    agentcore.EvidenceID("evidence-001"),
			EvidenceRequirementID: "evidence-required-001",
			AcceptanceCriterionID: "acceptance-001",
			Kind:                  agentcore.EvidenceKindTestLog,
			ToolLogRef:            agentcore.ToolLogRef{ID: agentcore.ToolLogID("test-log-001")},
		}},
	}
}
