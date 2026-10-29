package agentkernel

import (
	"context"
	"errors"

	"orrery_v2/internal/agent/runtime/providers/kernelcompat"
)

func publicExecutionError(err error) error {
	if err == nil {
		return nil
	}
	var diagnostic *kernelcompat.ExecutionError
	if errors.As(err, &diagnostic) {
		return err
	}
	for _, entry := range []struct {
		err  error
		code string
	}{
		{ErrConfiguration, "configuration_invalid"}, {ErrClosed, "session_unavailable"},
		{ErrBusy, "execution_busy"}, {ErrBudget, "budget_exceeded"},
		{ErrProtocol, "model_output_invalid"}, {ErrInput, "input_invalid"},
		{ErrModel, "model_request_failed"}, {ErrTool, "tool_execution_uncertain"},
		{ErrToolDenied, "tool_denied"}, {ErrJournal, "tool_journal_unavailable"},
		{ErrToolRecorded, "tool_already_recorded"}, {ErrToolConflict, "tool_identity_conflict"},
		{ErrRunUnresolved, "tool_execution_unresolved"}, {ErrCapacity, "callback_capacity_exhausted"},
		{context.DeadlineExceeded, "deadline_exceeded"}, {context.Canceled, "cancelled"},
	} {
		if errors.Is(err, entry.err) {
			return kernelcompat.NewExecutionError(entry.code, err)
		}
	}
	return err
}
