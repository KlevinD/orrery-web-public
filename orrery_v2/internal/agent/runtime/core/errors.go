package core

import (
	"fmt"

	agentcore "orrery_v2/internal/agent/core"
)

type ErrorCode string

const (
	CodeRuntimeNotAvailable ErrorCode = "runtime_not_available"
	CodeRuntimeAlreadyBound ErrorCode = "runtime_already_bound"
	CodeRuntimeInvalid      ErrorCode = "runtime_invalid"
)

type RuntimeError struct {
	Code        ErrorCode
	RuntimeKind agentcore.RuntimeKind
	Field       string
	Message     string
	Cause       error
	Retryable   bool
}

func (e RuntimeError) Error() string {
	if e.Message != "" {
		return e.Message
	}
	if e.RuntimeKind != "" {
		return fmt.Sprintf("%s: %s", e.Code, e.RuntimeKind)
	}
	return string(e.Code)
}

func (e RuntimeError) Unwrap() error {
	return e.Cause
}

func newRuntimeError(code ErrorCode, kind agentcore.RuntimeKind, field, message string, cause error) RuntimeError {
	return RuntimeError{
		Code:        code,
		RuntimeKind: kind,
		Field:       field,
		Message:     message,
		Cause:       cause,
	}
}
