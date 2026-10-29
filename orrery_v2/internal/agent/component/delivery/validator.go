package delivery

import (
	"fmt"

	agentcore "orrery_v2/internal/agent/core"
	missiontask "orrery_v2/internal/mission/task"
)

type Validator struct {
	contract missiontask.TaskContract
}

func NewValidator(contract missiontask.TaskContract) Validator {
	return Validator{contract: contract}
}

func (v Validator) Validate(result agentcore.AgentResult) []ValidationError {
	var errs []ValidationError
	if result.Summary.IsZero() {
		errs = append(errs, validationError(CodeMissingSummary, "summary", "summary is required"))
	}
	expected := map[string]missiontask.ExpectedDeliverable{}
	for _, item := range v.contract.ExpectedDeliverables {
		expected[string(item.ID)] = item
	}
	seen := map[string]bool{}
	for i, deliverable := range result.Deliverables {
		field := fmt.Sprintf("deliverables[%d]", i)
		if deliverable.ID == "" || deliverable.ExpectedDeliverableID == "" || deliverable.Title == "" {
			errs = append(errs, validationError(CodeMissingDeliverableIdentity, field, "deliverable requires id, expected deliverable id, and title"))
		}
		if deliverable.ArtifactRef.IsZero() {
			errs = append(errs, validationError(CodeUntraceableDeliverable, field+".artifact_ref", "deliverable must reference an artifact"))
		}
		if _, ok := expected[deliverable.ExpectedDeliverableID]; !ok && deliverable.ExpectedDeliverableID != "" {
			errs = append(errs, validationError(CodeUnexpectedDeliverable, field+".expected_deliverable_id", "deliverable does not map to task expectations"))
		}
		seen[deliverable.ExpectedDeliverableID] = true
	}
	for id := range expected {
		if !seen[id] && result.Blocker == nil {
			errs = append(errs, validationError(CodeMissingExpectedDeliverable, "deliverables", "result must include every expected deliverable"))
		}
	}
	return errs
}

func validationError(code ValidationCode, field, message string) ValidationError {
	return ValidationError{Code: code, Field: field, Message: message}
}
