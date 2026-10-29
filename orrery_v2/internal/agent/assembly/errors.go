package assembly

import (
	"fmt"

	agentcore "orrery_v2/internal/agent/core"
)

type ErrorCode string

const (
	CodeToolBindingDenied   ErrorCode = "tool_binding_denied"
	CodeSkillVersionMissing ErrorCode = "skill_version_not_found"
	CodeMemoryBindingDenied ErrorCode = "memory_binding_denied"
	CodeRuntimeNotAvailable ErrorCode = "runtime_not_available"
	CodeComponentMissing    ErrorCode = "component_missing"
	CodeProfileInvalid      ErrorCode = "profile_invalid"
	CodeInstanceMissing     ErrorCode = "instance_missing"
)

type Error struct {
	Code      ErrorCode
	Field     string
	Message   string
	ProfileID agentcore.AgentProfileID
	Cause     error
}

func (e Error) Error() string {
	base := string(e.Code)
	if e.Message != "" {
		base = e.Message
	}
	if e.ProfileID != "" || e.Field != "" {
		base = fmt.Sprintf("%s: code=%s profile_id=%s field=%s", base, e.Code, e.ProfileID, e.Field)
	}
	if e.Cause != nil {
		base = fmt.Sprintf("%s cause=%v", base, e.Cause)
	}
	return base
}

func (e Error) Unwrap() error {
	return e.Cause
}

func newError(code ErrorCode, profileID agentcore.AgentProfileID, field, message string, cause error) Error {
	return Error{Code: code, ProfileID: profileID, Field: field, Message: message, Cause: cause}
}
