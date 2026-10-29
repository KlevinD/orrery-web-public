package evidence

import (
	"fmt"

	agentcore "orrery_v2/internal/agent/core"
	missiontask "orrery_v2/internal/mission/task"
)

type Validator struct {
	requirements []missiontask.EvidenceRequirement
	criteria     []missiontask.AcceptanceCriterion
}

func NewValidator(requirements []missiontask.EvidenceRequirement, criteria []missiontask.AcceptanceCriterion) Validator {
	return Validator{
		requirements: append([]missiontask.EvidenceRequirement(nil), requirements...),
		criteria:     append([]missiontask.AcceptanceCriterion(nil), criteria...),
	}
}

func (v Validator) Validate(items []agentcore.Evidence) []ValidationError {
	var errs []ValidationError
	byRequirement := map[string][]agentcore.Evidence{}
	for i, item := range items {
		field := fmt.Sprintf("evidence[%d]", i)
		if item.EvidenceRequirementID == "" {
			errs = append(errs, validationError(CodeUnknownEvidence, field+".evidence_requirement_id", "evidence must map to a requirement"))
		}
		if !item.HasTraceableSource() {
			errs = append(errs, validationError(CodeUntraceableEvidence, field, "evidence must have a traceable source"))
		}
		byRequirement[item.EvidenceRequirementID] = append(byRequirement[item.EvidenceRequirementID], item)
	}

	for _, requirement := range v.requirements {
		matches := byRequirement[string(requirement.ID)]
		if requirement.Required && len(matches) == 0 {
			errs = append(errs, validationError(CodeMissingRequiredEvidence, "evidence", "required evidence is missing"))
			continue
		}
		wantKind := ExpectedAgentEvidenceKind(requirement.Kind)
		for _, match := range matches {
			if wantKind != "" && match.Kind != wantKind {
				errs = append(errs, validationError(CodeEvidenceKindMismatch, "evidence.kind", "evidence kind does not match requirement"))
			}
		}
	}

	for _, criterion := range v.criteria {
		found := false
		for _, item := range items {
			if item.AcceptanceCriterionID == string(criterion.ID) {
				found = true
				break
			}
		}
		if !found {
			errs = append(errs, validationError(CodeMissingAcceptanceMapping, "evidence.acceptance_criterion_id", "acceptance criterion requires supporting evidence"))
		}
	}
	return errs
}

func validationError(code ValidationCode, field, message string) ValidationError {
	return ValidationError{Code: code, Field: field, Message: message}
}
