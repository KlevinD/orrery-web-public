package team

import "errors"

type PolicyCode string

const (
	CodeProfileNotFound       PolicyCode = "profile_not_found"
	CodeProfileDisabled       PolicyCode = "profile_disabled"
	CodeOrgIdentityIncomplete PolicyCode = "org_identity_incomplete"
	CodeCandidateNotAgent     PolicyCode = "candidate_not_agent"
	CodeAvailabilityDenied    PolicyCode = "availability_denied"
	CodeAuthorityDenied       PolicyCode = "authority_denied"
	CodeSkillMissing          PolicyCode = "skill_missing"
	CodeToolMissing           PolicyCode = "tool_missing"
	CodeRuntimeNotAvailable   PolicyCode = "runtime_not_available"
	CodeMemoryScopeDenied     PolicyCode = "memory_scope_denied"
	CodeRiskDenied            PolicyCode = "risk_denied"
	CodeBudgetDenied          PolicyCode = "budget_denied"
	CodeQualityDenied         PolicyCode = "quality_denied"
	CodeDuplicateAssignment   PolicyCode = "duplicate_assignment"
	CodeTeamMemberNotFound    PolicyCode = "team_member_not_found"
	CodeAssignmentNotActive   PolicyCode = "assignment_not_active"
	CodeEventCommitFailed     PolicyCode = "event_commit_failed"
)

var (
	ErrInvalidArgument      = errors.New("invalid staffing argument")
	ErrPolicyDenied         = errors.New("staffing policy denied")
	ErrConflict             = errors.New("staffing conflict")
	ErrNotFound             = errors.New("staffing record not found")
	ErrProvisioningFailed   = errors.New("agent instance provisioning failed")
	ErrRepositoryDependency = errors.New("staffing repository dependency failed")
)

type PolicyError struct {
	Code    PolicyCode
	Field   string
	Message string
}

func (e PolicyError) Error() string {
	if e.Message != "" {
		return string(e.Code) + ": " + e.Message
	}
	return string(e.Code)
}
