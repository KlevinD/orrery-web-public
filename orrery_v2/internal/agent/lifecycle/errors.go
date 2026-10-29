package lifecycle

import (
	"errors"
	"fmt"
	"strings"
)

var (
	ErrInvalidSignal        = errors.New("invalid_signal")
	ErrMissingCorrelationID = errors.New("missing_correlation_id")
	ErrHandlerFailed        = errors.New("handler_failed")
	ErrPipelineStopped      = errors.New("pipeline_stopped")
)

type HandlerFailure struct {
	HandlerName       string
	SignalName        SignalName
	Policy            FailurePolicy
	DegradeCapability string
	Cause             error
}

func (e HandlerFailure) Error() string {
	if e.Cause == nil {
		return fmt.Sprintf("%s: handler %q failed for signal %q with policy %q", ErrHandlerFailed, e.HandlerName, e.SignalName, e.Policy)
	}
	return fmt.Sprintf("%s: handler %q failed for signal %q with policy %q: %v", ErrHandlerFailed, e.HandlerName, e.SignalName, e.Policy, e.Cause)
}

func (e HandlerFailure) Unwrap() error {
	return e.Cause
}

type SignalValidationError struct {
	Field string
	Cause error
}

func (e SignalValidationError) Error() string {
	if e.Cause == nil {
		return fmt.Sprintf("%s: %s", ErrInvalidSignal, e.Field)
	}
	return fmt.Sprintf("%s: %s: %v", ErrInvalidSignal, e.Field, e.Cause)
}

func (e SignalValidationError) Unwrap() error {
	if e.Cause != nil {
		return e.Cause
	}
	return ErrInvalidSignal
}

func IsInvalidSignal(err error) bool {
	return errors.Is(err, ErrInvalidSignal)
}

func IsMissingCorrelationID(err error) bool {
	return errors.Is(err, ErrMissingCorrelationID)
}

func IsHandlerFailed(err error) bool {
	return errors.Is(err, ErrHandlerFailed)
}

func IsPipelineStopped(err error) bool {
	return errors.Is(err, ErrPipelineStopped)
}

type HandlerFailures struct {
	Failures []HandlerFailure
}

func (e HandlerFailures) Error() string {
	parts := make([]string, 0, len(e.Failures))
	for _, failure := range e.Failures {
		parts = append(parts, failure.Error())
	}
	return strings.Join(parts, "; ")
}

func (e HandlerFailures) IsSoft() bool {
	for _, failure := range e.Failures {
		if failure.Policy != FailurePolicyContinue && failure.Policy != FailurePolicyDegrade {
			return false
		}
	}
	return true
}
