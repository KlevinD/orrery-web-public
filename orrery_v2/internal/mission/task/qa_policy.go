package task

import (
	"strings"

	agentcore "orrery_v2/internal/agent/core"
)

type QAContractID string
type CheckMode string
type EvidenceStrictness string

const (
	CheckModeAutomated CheckMode = "automated"
	CheckModeHuman     CheckMode = "human"
	CheckModeHybrid    CheckMode = "hybrid"

	EvidenceStrictnessBestEffort EvidenceStrictness = "best_effort"
	EvidenceStrictnessRequired   EvidenceStrictness = "required"
	EvidenceStrictnessStrict     EvidenceStrictness = "strict"
)

type QAContract struct {
	ID                         QAContractID
	AcceptanceCriteria         []AcceptanceCriterion
	EvidenceRequirements       []EvidenceRequirement
	PassPolicy                 string
	RejectionPolicy            string
	MaxReworkAttempts          int
	RequiredReviewerProfileRef agentcore.AgentProfileRef
}

type QAPolicy struct {
	CheckMode               CheckMode
	EvidenceStrictness      EvidenceStrictness
	AllowHumanOverride      bool
	RequireReasonOnReject   bool
	RequireAcceptanceReport bool
}

type QACheckInput struct {
	TaskID       TaskID
	SubmissionID string
	CheckedBy    agentcore.AgentID
	Evidence     []agentcore.Evidence
}

func CheckQA(contract QAContract, policy QAPolicy, input QACheckInput, proposed QAResultRecord) QAResultRecord {
	result := proposed
	if result.TaskID == "" {
		result.TaskID = input.TaskID
	}
	if result.SubmissionID == "" {
		result.SubmissionID = input.SubmissionID
	}
	if result.CheckedBy == "" {
		result.CheckedBy = input.CheckedBy
	}
	result.CheckedCriteria = mergeCriteriaChecks(result.CheckedCriteria, contract.AcceptanceCriteria, input.Evidence)
	result.EvidenceResults, result.MissingEvidence = mergeEvidenceChecks(result.EvidenceResults, contract.EvidenceRequirements, input.Evidence)
	if hasFailedCriterion(result.CheckedCriteria) || hasFailedEvidence(result.EvidenceResults) || len(result.MissingEvidence) > 0 {
		result.Status = QAStatusRejected
	}
	if result.Status == "" {
		result.Status = QAStatusApproved
	}
	if policy.EvidenceStrictness == EvidenceStrictnessRequired && len(contract.EvidenceRequirements) > 0 && len(result.EvidenceResults) == 0 {
		result.Status = QAStatusRejected
		result.RejectionReasons = append(result.RejectionReasons, "required evidence checks were not performed")
	}
	for _, item := range result.CheckedCriteria {
		if item.Status == CheckStatusFailed || item.Status == CheckStatusMissing {
			result.RejectionReasons = appendNonBlank(result.RejectionReasons, item.Reason)
		}
	}
	for _, item := range result.EvidenceResults {
		if item.Status == CheckStatusFailed || item.Status == CheckStatusMissing {
			result.RejectionReasons = appendNonBlank(result.RejectionReasons, item.Reason)
		}
	}
	if result.Status == QAStatusRejected && policy.RequireReasonOnReject && allBlankStrings(result.RejectionReasons) {
		result.Status = QAStatusBlocked
		result.RejectionReasons = []string{"rejection reason is required by QA policy"}
	}
	return result
}

func mergeCriteriaChecks(existing []CheckedCriterion, criteria []AcceptanceCriterion, evidence []agentcore.Evidence) []CheckedCriterion {
	byID := map[AcceptanceCriterionID]CheckedCriterion{}
	for _, item := range existing {
		byID[item.CriterionID] = item
	}
	out := make([]CheckedCriterion, 0, len(criteria))
	for _, criterion := range criteria {
		if item, ok := byID[criterion.ID]; ok && item.Status != "" {
			out = append(out, item)
			continue
		}
		check := CheckedCriterion{CriterionID: criterion.ID, Status: CheckStatusMissing, Reason: "acceptance criterion has no supporting evidence"}
		for _, item := range evidence {
			if item.AcceptanceCriterionID == string(criterion.ID) && item.HasTraceableSource() {
				check.Status = CheckStatusPassed
				check.Reason = ""
				break
			}
		}
		out = append(out, check)
	}
	return out
}

func mergeEvidenceChecks(existing []EvidenceCheckResult, requirements []EvidenceRequirement, evidence []agentcore.Evidence) ([]EvidenceCheckResult, []EvidenceRequirementID) {
	byID := map[EvidenceRequirementID]EvidenceCheckResult{}
	for _, item := range existing {
		byID[item.RequirementID] = item
	}
	out := make([]EvidenceCheckResult, 0, len(requirements))
	var missing []EvidenceRequirementID
	for _, requirement := range requirements {
		if item, ok := byID[requirement.ID]; ok && item.Status != "" {
			out = append(out, item)
			if requirement.Required && item.Status != CheckStatusPassed {
				missing = append(missing, requirement.ID)
			}
			continue
		}
		check := EvidenceCheckResult{RequirementID: requirement.ID, Status: CheckStatusMissing, Reason: "required evidence is missing"}
		for _, item := range evidence {
			if item.EvidenceRequirementID == string(requirement.ID) && item.HasTraceableSource() {
				check.Status = CheckStatusPassed
				check.EvidenceID = item.ID
				check.Reason = ""
				break
			}
		}
		out = append(out, check)
		if requirement.Required && check.Status != CheckStatusPassed {
			missing = append(missing, requirement.ID)
		}
	}
	return out, missing
}

func hasFailedCriterion(items []CheckedCriterion) bool {
	for _, item := range items {
		if item.Status == CheckStatusFailed || item.Status == CheckStatusMissing {
			return true
		}
	}
	return false
}

func hasFailedEvidence(items []EvidenceCheckResult) bool {
	for _, item := range items {
		if item.Status == CheckStatusFailed || item.Status == CheckStatusMissing {
			return true
		}
	}
	return false
}

func appendNonBlank(items []string, item string) []string {
	if strings.TrimSpace(item) == "" {
		return items
	}
	return append(items, item)
}

func allBlankStrings(items []string) bool {
	if len(items) == 0 {
		return true
	}
	for _, item := range items {
		if strings.TrimSpace(item) != "" {
			return false
		}
	}
	return true
}
