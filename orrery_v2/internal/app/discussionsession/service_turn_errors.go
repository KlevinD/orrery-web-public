package discussionsession

import (
	"context"
	"errors"
)

type supersededDiscussionRunError struct {
	cause error
}

func newSupersededDiscussionRunError(cause error) error {
	if cause == nil {
		cause = ErrNotFound
	}
	return &supersededDiscussionRunError{cause: cause}
}

func (e *supersededDiscussionRunError) Error() string {
	return e.cause.Error()
}

func (e *supersededDiscussionRunError) Unwrap() error {
	return e.cause
}

func discussionTurnErrorCode(err error) string {
	switch {
	case errors.Is(err, ErrRuntimeUnavailable):
		return "runtime_unavailable"
	case errors.Is(err, ErrRuntimeExecution):
		return "runtime_execution_failed"
	case errors.Is(err, context.Canceled):
		return "cancelled"
	case errors.Is(err, ErrForbidden):
		return "forbidden"
	case errors.Is(err, ErrInvalidRequest):
		return "invalid_request"
	default:
		return "turn_execution_failed"
	}
}
