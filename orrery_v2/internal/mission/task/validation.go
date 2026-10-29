package task

import (
	"strconv"

	agentcore "orrery_v2/internal/agent/core"
)

func ValidateAgentResult(c TaskContract, r agentcore.AgentResult) []ValidationError {
	var errs []ValidationError
	for _, err := range agentcore.ValidateAgentResult(r) {
		errs = append(errs, ValidationError{
			Code:      mapAgentValidationCode(err.Code),
			Field:     err.Field,
			Message:   err.Message,
			Retryable: err.Retryable,
			Metadata:  err.Metadata,
		})
	}
	if r.Blocker != nil {
		if !allEvidenceHasTraceableSource(r.Evidence) {
			errs = append(errs, validationError(CodeInvalidRef, "evidence", "evidence must reference an artifact, tool log, external URL, approval record, or human submission"))
		}
		return errs
	}
	if len(c.ExpectedDeliverables) > 0 && !hasAllExpectedDeliverables(c.ExpectedDeliverables, r.Deliverables) {
		errs = append(errs, validationError(CodeMissingDeliverable, "deliverables", "result must include deliverables traceable to task expectations"))
	}
	if hasRequiredEvidence(c.EvidenceRequirements) && !hasAllRequiredEvidence(c.EvidenceRequirements, r.Evidence) {
		errs = append(errs, validationError(CodeMissingRequiredEvidence, "evidence", "result must include required evidence"))
	}
	if len(c.AcceptanceCriteria) > 0 && !hasAcceptanceEvidence(c.AcceptanceCriteria, r.Evidence) {
		errs = append(errs, validationError(CodeMissingAcceptanceMapping, "evidence", "result evidence must map to acceptance criteria"))
	}
	if !allEvidenceHasTraceableSource(r.Evidence) {
		errs = append(errs, validationError(CodeInvalidRef, "evidence", "evidence must reference an artifact, tool log, external URL, approval record, or human submission"))
	}
	return errs
}

func mapAgentValidationCode(code agentcore.ValidationCode) ValidationCode {
	switch code {
	case agentcore.CodeMissingSummary:
		return CodeMissingSummary
	default:
		return ValidationCode(code)
	}
}

func hasAllExpectedDeliverables(expected []ExpectedDeliverable, actual []agentcore.Deliverable) bool {
	for _, want := range expected {
		found := false
		for _, got := range actual {
			if got.ExpectedDeliverableID == string(want.ID) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func hasRequiredEvidence(requirements []EvidenceRequirement) bool {
	for _, requirement := range requirements {
		if requirement.Required {
			return true
		}
	}
	return false
}

func hasAllRequiredEvidence(requirements []EvidenceRequirement, actual []agentcore.Evidence) bool {
	for _, requirement := range requirements {
		if !requirement.Required {
			continue
		}
		found := false
		for _, got := range actual {
			if got.EvidenceRequirementID == string(requirement.ID) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func hasAcceptanceEvidence(criteria []AcceptanceCriterion, actual []agentcore.Evidence) bool {
	for _, criterion := range criteria {
		found := false
		for _, evidence := range actual {
			if evidence.AcceptanceCriterionID == string(criterion.ID) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func allEvidenceHasTraceableSource(evidence []agentcore.Evidence) bool {
	for _, item := range evidence {
		if !item.HasTraceableSource() {
			return false
		}
	}
	return true
}

func indexedField(collection string, index int, field string) string {
	return collection + "[" + strconv.Itoa(index) + "]." + field
}
