package agentkernel

import (
	"context"
	"errors"

	agentcore "orrery_v2/internal/agent/core"
)

// Return fixed diagnostic categories, never raw provider errors or credentials.
// kernelcompat preserves the underlying cause for internal errors.Is checks.
func (e *Engine) SessionOpenFailure(err error) (agentcore.RuntimeBridgeErrorCode, string) {
	if agentcore.IsRuntimeBridgeError(err, agentcore.RuntimeBridgeWorkspaceUnavailable) {
		return agentcore.RuntimeBridgeWorkspaceUnavailable, "managed kernel workspace reference is unavailable"
	}
	reason := "initialization_failed"
	switch {
	case errors.Is(err, ErrTaskInputChanged):
		reason = "checkpoint_input_changed"
	case errors.Is(err, ErrUnsafeResume):
		reason = "checkpoint_tool_result_unresolved"
	case errors.Is(err, ErrCheckpoint):
		reason = "checkpoint_conflict"
	case errors.Is(err, context.DeadlineExceeded):
		reason = "deadline_exceeded"
	case errors.Is(err, ErrToolDenied):
		reason = "tool_scope_denied"
	case errors.Is(err, ErrBudget):
		reason = "context_budget_exceeded"
	case errors.Is(err, ErrInput):
		reason = "invalid_input"
	case errors.Is(err, ErrModel):
		reason = "model_initialization_failed"
	case errors.Is(err, ErrCapacity):
		reason = "callback_capacity_exhausted"
	case errors.Is(err, ErrConfiguration):
		reason = "configuration_unavailable"
	}
	return agentcore.RuntimeBridgeBackendRejected, "managed kernel session could not be opened (" + reason + ")"
}
