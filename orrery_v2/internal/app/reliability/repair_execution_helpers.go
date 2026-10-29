package reliability

import (
	"fmt"
	"strings"
	"time"
)

func cloneRepairTarget(target RepairTarget) RepairTarget {
	return target
}

func sameRepairTarget(left, right RepairTarget) bool {
	return left.TenantID == right.TenantID && left.ObservationID == right.ObservationID && left.IncidentID == right.IncidentID &&
		left.Kind == right.Kind && left.AuthorizationTenantID == right.AuthorizationTenantID && left.MissionID == right.MissionID &&
		left.RunID == right.RunID && left.OccurredAt.Equal(right.OccurredAt)
}

func cloneRepairExecution(execution RepairExecution) RepairExecution {
	execution.Target = cloneRepairTarget(execution.Target)
	if execution.CompletedAt != nil {
		completedAt := *execution.CompletedAt
		execution.CompletedAt = &completedAt
	}
	return execution
}

func sameRepairPreflight(left, right RepairPreflight) bool {
	return left.EvidenceID == right.EvidenceID && left.TenantID == right.TenantID && left.ActionRunID == right.ActionRunID &&
		left.IncidentID == right.IncidentID && left.IncidentVersion == right.IncidentVersion && left.RuleID == right.RuleID &&
		left.RuleVersion == right.RuleVersion && left.Strategy == right.Strategy &&
		left.ArgsHash == right.ArgsHash && left.TargetObservationID == right.TargetObservationID && left.TargetKind == right.TargetKind &&
		left.TargetRunID == right.TargetRunID && left.Passed == right.Passed && left.CheckedAt.Equal(right.CheckedAt) && left.ExpiresAt.Equal(right.ExpiresAt)
}

func validateRepairPreflightCandidate(candidate RepairPreflight) error {
	targetEmpty := candidate.TargetObservationID == "" && candidate.TargetKind == "" && candidate.TargetRunID == ""
	targetComplete := strings.TrimSpace(candidate.TargetObservationID) != "" && candidate.TargetKind == RepairTargetMissionAgentRun && strings.TrimSpace(candidate.TargetRunID) != ""
	if strings.TrimSpace(candidate.EvidenceID) == "" || strings.TrimSpace(candidate.TenantID) == "" ||
		strings.TrimSpace(candidate.ActionRunID) == "" || strings.TrimSpace(candidate.IncidentID) == "" || candidate.IncidentVersion <= 0 ||
		strings.TrimSpace(candidate.RuleID) == "" || candidate.RuleVersion <= 0 || candidate.Strategy == "" || strings.TrimSpace(candidate.ArgsHash) == "" ||
		candidate.CheckedAt.IsZero() || candidate.ExpiresAt.IsZero() || !candidate.ExpiresAt.After(candidate.CheckedAt) ||
		(!targetEmpty && !targetComplete) || (candidate.Strategy == RepairStrategyRetry && !targetComplete) {
		return fmt.Errorf("%w: complete repair preflight binding is required", ErrInvalidRequest)
	}
	return nil
}

func sameRepairExecutionIdentity(left, right RepairExecution) bool {
	return left.TenantID == right.TenantID && left.ActionRunID == right.ActionRunID && left.IncidentID == right.IncidentID &&
		left.RuleID == right.RuleID && left.PreflightEvidenceID == right.PreflightEvidenceID && left.Strategy == right.Strategy &&
		sameRepairTarget(left.Target, right.Target) && left.RetryRunID == right.RetryRunID && left.StartedAt.Equal(right.StartedAt) &&
		left.DeadlineAt.Equal(right.DeadlineAt)
}

func validateRepairExecutionCandidate(candidate RepairExecution) error {
	if strings.TrimSpace(candidate.TenantID) == "" || strings.TrimSpace(candidate.ActionRunID) == "" ||
		strings.TrimSpace(candidate.IncidentID) == "" || strings.TrimSpace(candidate.RuleID) == "" ||
		strings.TrimSpace(candidate.PreflightEvidenceID) == "" || candidate.Strategy == "" || strings.TrimSpace(candidate.RetryRunID) == "" ||
		candidate.Target.TenantID != candidate.TenantID || candidate.Target.IncidentID != candidate.IncidentID ||
		strings.TrimSpace(candidate.Target.ObservationID) == "" || candidate.Target.Kind != RepairTargetMissionAgentRun ||
		strings.TrimSpace(candidate.Target.AuthorizationTenantID) == "" || strings.TrimSpace(candidate.Target.MissionID) == "" ||
		strings.TrimSpace(candidate.Target.RunID) == "" || candidate.Target.OccurredAt.IsZero() || candidate.StartedAt.IsZero() ||
		candidate.DeadlineAt.IsZero() || !candidate.DeadlineAt.After(candidate.StartedAt) || candidate.UpdatedAt.IsZero() {
		return fmt.Errorf("%w: complete repair execution binding is required", ErrInvalidRequest)
	}
	return nil
}

func repairExecutionMatchesCurrentBinding(candidate RepairExecution, preflight RepairPreflight, incident FailureIncident, rule RepairRule, latestTarget RepairTarget) bool {
	return preflight.TenantID == candidate.TenantID && preflight.EvidenceID == candidate.PreflightEvidenceID && preflight.Passed &&
		!preflight.CheckedAt.After(candidate.StartedAt) &&
		preflight.ExpiresAt.After(candidate.StartedAt) && preflight.IncidentID == candidate.IncidentID && preflight.IncidentVersion == incident.Version &&
		preflight.RuleID == candidate.RuleID && preflight.RuleVersion == rule.Version && preflight.Strategy == candidate.Strategy &&
		preflight.TargetObservationID == candidate.Target.ObservationID && preflight.TargetKind == candidate.Target.Kind &&
		preflight.TargetRunID == candidate.Target.RunID && latestTarget.OccurredAt.Equal(incident.LastSeenAt) &&
		sameRepairTarget(latestTarget, candidate.Target)
}

func repairExecutionActive(status RepairExecutionStatus) bool {
	return status == RepairExecutionDispatching || status == RepairExecutionMonitoring
}

func validRepairExecutionTransition(from, to RepairExecutionStatus) bool {
	if from == to {
		return true
	}
	switch from {
	case RepairExecutionDispatching:
		return to == RepairExecutionMonitoring || to == RepairExecutionFailed
	case RepairExecutionMonitoring:
		return to == RepairExecutionSucceeded || to == RepairExecutionFailed
	default:
		return false
	}
}

func cloneTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}
