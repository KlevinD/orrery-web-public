package kernelcompat

import "errors"

// ExecutionError carries a fixed diagnostic code across the adapter boundary.
// Provider errors, arguments and credentials must never become public messages.
type ExecutionError struct {
	code  string
	cause error
}

func NewExecutionError(code string, cause error) error {
	return &ExecutionError{code: code, cause: cause}
}

// ExecutionErrorCode returns the fixed, safe diagnostic code carried by an
// adapter error. The code is deliberately separate from Error(): the latter
// is a bounded human-readable message, while local hosts need a stable value
// for persistence and retry decisions without exposing provider details.
func ExecutionErrorCode(err error) (string, bool) {
	if err == nil {
		return "", false
	}
	var diagnostic *ExecutionError
	if !errors.As(err, &diagnostic) || diagnostic == nil || !isPublicExecutionErrorCode(diagnostic.code) {
		return "", false
	}
	return diagnostic.code, true
}

func (e *ExecutionError) Error() string { return executionFailureMessage(e) }
func (e *ExecutionError) Unwrap() error { return e.cause }

func executionFailureMessage(err error) string {
	const fallback = "managed kernel execution failed"
	var failure *ExecutionError
	if !errors.As(err, &failure) {
		return fallback
	}
	if isPublicExecutionErrorCode(failure.code) {
		return fallback + ": " + failure.code
	}
	return fallback
}

func isPublicExecutionErrorCode(code string) bool {
	switch code {
	case "configuration_invalid", "session_unavailable", "execution_busy", "budget_exceeded",
		"model_output_invalid", "input_invalid", "model_request_failed", "tool_execution_uncertain",
		"tool_denied", "tool_journal_unavailable", "tool_already_recorded", "tool_identity_conflict",
		"tool_execution_unresolved", "callback_capacity_exhausted", "deadline_exceeded", "cancelled",
		"tool_arguments_invalid", "tool_schema_invalid", "tool_call_identity_invalid", "tool_unbound",
		"finish_reason_invalid", "empty_model_response":
		return true
	default:
		return false
	}
}
