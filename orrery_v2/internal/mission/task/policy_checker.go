package task

import (
	"context"

	agentcore "orrery_v2/internal/agent/core"
)

type ResultPolicyChecker struct {
	assignees         MissionAssignmentResolver
	maxReworkAttempts int
}

type ResultPolicyCheckerOptions struct {
	AssigneeResolver  MissionAssignmentResolver
	MaxReworkAttempts int
}

type ResultPolicyInput struct {
	Contract       TaskContract
	Result         agentcore.AgentResult
	Agent          agentcore.AgentInstanceRef
	ReworkAttempts int
}

func NewResultPolicyChecker(opts ResultPolicyCheckerOptions) ResultPolicyChecker {
	return ResultPolicyChecker{
		assignees:         opts.AssigneeResolver,
		maxReworkAttempts: opts.MaxReworkAttempts,
	}
}

func (c ResultPolicyChecker) Check(input ResultPolicyInput) []ValidationError {
	errs := ValidateAgentResult(input.Contract, input.Result)
	if !input.Agent.IsZero() {
		expected, err := ResolveTaskAssignee(context.Background(), input.Contract.Assignee, c.assignees)
		if err != nil || !expected.MatchesInstance(input.Agent) {
			errs = append(errs, validationError(CodeInvalidRef, "assignee", "result agent must match task assignee"))
		}
	}
	if c.maxReworkAttempts > 0 && input.ReworkAttempts >= c.maxReworkAttempts {
		err := validationError(CodeConflict, "rework_attempts", "task exceeded max rework attempts")
		err.Retryable = false
		errs = append(errs, err)
	}
	return errs
}

func (c ResultPolicyChecker) CheckSummaryRequired(input ResultPolicyInput) []ValidationError {
	if input.Result.Summary.IsZero() {
		return []ValidationError{validationError(CodeMissingSummary, "summary", "summary is required")}
	}
	return nil
}

func (c ResultPolicyChecker) CheckExpectedDeliverables(input ResultPolicyInput) []ValidationError {
	if len(input.Contract.ExpectedDeliverables) > 0 && !hasAllExpectedDeliverables(input.Contract.ExpectedDeliverables, input.Result.Deliverables) {
		return []ValidationError{validationError(CodeMissingDeliverable, "deliverables", "result must include deliverables traceable to task expectations")}
	}
	return nil
}

func (c ResultPolicyChecker) CheckRequiredEvidence(input ResultPolicyInput) []ValidationError {
	if hasRequiredEvidence(input.Contract.EvidenceRequirements) && !hasAllRequiredEvidence(input.Contract.EvidenceRequirements, input.Result.Evidence) {
		return []ValidationError{validationError(CodeMissingRequiredEvidence, "evidence", "result must include required evidence")}
	}
	return nil
}

func (c ResultPolicyChecker) CheckEvidenceTraceability(input ResultPolicyInput) []ValidationError {
	if !allEvidenceHasTraceableSource(input.Result.Evidence) {
		return []ValidationError{validationError(CodeInvalidRef, "evidence", "evidence must reference an artifact, tool log, external URL, approval record, or human submission")}
	}
	return nil
}

func (c ResultPolicyChecker) CheckTaskAssignee(input ResultPolicyInput) []ValidationError {
	if input.Agent.IsZero() {
		return nil
	}
	expected, err := ResolveTaskAssignee(context.Background(), input.Contract.Assignee, c.assignees)
	if err != nil || !expected.MatchesInstance(input.Agent) {
		return []ValidationError{validationError(CodeInvalidRef, "assignee", "result agent must match task assignee")}
	}
	return nil
}

func (c ResultPolicyChecker) CheckReworkLimit(input ResultPolicyInput) []ValidationError {
	if c.maxReworkAttempts > 0 && input.ReworkAttempts >= c.maxReworkAttempts {
		err := validationError(CodeConflict, "rework_attempts", "task exceeded max rework attempts")
		err.Retryable = false
		return []ValidationError{err}
	}
	return nil
}
