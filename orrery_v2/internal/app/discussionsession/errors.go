package discussionsession

import (
	"errors"
	"fmt"
	"strings"

	agentcore "orrery_v2/internal/agent/core"
	missioncontrol "orrery_v2/internal/app/missioncontrol"
)

var (
	ErrServiceDependency       = errors.New("discussion service dependency unavailable")
	ErrInvalidRequest          = errors.New("discussion invalid request")
	ErrNotFound                = errors.New("discussion not found")
	ErrForbidden               = errors.New("discussion forbidden")
	ErrRuntimeUnavailable      = errors.New("discussion runtime unavailable")
	ErrRuntimeExecution        = errors.New("discussion runtime execution failed")
	ErrOrchestrationIncomplete = errors.New("discussion orchestration incomplete")
	ErrConversationMember      = errors.New("discussion member unavailable")
	ErrWorkerInternalQAMember  = errors.New("worker-internal QA cannot be a direct discussion member")
	ErrRevisionConflict        = errors.New("discussion revision conflict")
	ErrCommandInProgress       = errors.New("discussion command already in progress")
	ErrRuntimeApprovalPending  = errors.New("discussion is waiting for runtime approval")
	ErrIdempotencyMismatch     = errors.New("discussion idempotency key reused with different command")
	ErrRunNotActive            = errors.New("the mentioned Agent is not currently working")
	ErrRuntimeControl          = errors.New("the active Agent runtime cannot accept an interjection")
)

const (
	StreamErrorCodePermissionDenied      = "orchestration_permission_denied"
	StreamErrorCodeInvalidRequest        = "orchestration_invalid_request"
	streamErrorCodeRetrievalPromptBudget = "retrieval_prompt_budget_exceeded"
)

type RuntimeSetupError struct {
	RuntimeKind string
	Cause       string
	Code        string
	Retryable   bool
}

func NewRuntimeSetupErrorFromCause(runtimeKind string, cause error) RuntimeSetupError {
	setup := NewRuntimeSetupError(runtimeKind, errorString(cause))
	var bridgeErr *agentcore.RuntimeBridgeError
	if errors.As(cause, &bridgeErr) {
		setup.Code = string(bridgeErr.Code)
		setup.Retryable = bridgeErr.Retryable
	}
	return setup
}

type RuntimeExecutionError struct {
	RuntimeKind string
	Cause       string
}

func NewRuntimeSetupError(runtimeKind string, cause string) RuntimeSetupError {
	return RuntimeSetupError{
		RuntimeKind: strings.TrimSpace(runtimeKind),
		Cause:       trimRuntimeUnavailableCause(cause),
	}
}

func NewRuntimeExecutionError(runtimeKind string, cause string) RuntimeExecutionError {
	return RuntimeExecutionError{
		RuntimeKind: strings.TrimSpace(runtimeKind),
		Cause:       trimRuntimeExecutionCause(cause),
	}
}

func (e RuntimeSetupError) Error() string {
	parts := []string{ErrRuntimeUnavailable.Error()}
	if e.RuntimeKind != "" {
		parts = append(parts, fmt.Sprintf("runtime kind %q", e.RuntimeKind))
	}
	if e.Cause != "" {
		parts = append(parts, e.Cause)
	}
	return strings.Join(parts, ": ")
}

func (e RuntimeSetupError) Unwrap() error {
	return ErrRuntimeUnavailable
}

func (e RuntimeExecutionError) Error() string {
	parts := []string{ErrRuntimeExecution.Error()}
	if e.RuntimeKind != "" {
		parts = append(parts, fmt.Sprintf("runtime kind %q", e.RuntimeKind))
	}
	if e.Cause != "" {
		parts = append(parts, e.Cause)
	}
	return strings.Join(parts, ": ")
}

func (e RuntimeExecutionError) Unwrap() error {
	return ErrRuntimeExecution
}

func RuntimeUnavailableError(err error) error {
	if err == nil {
		return ErrRuntimeUnavailable
	}
	if errors.Is(err, ErrRuntimeUnavailable) {
		return err
	}
	return NewRuntimeSetupError("", err.Error())
}

func streamErrorEvent(err error) StreamEventDTO {
	event := StreamEventDTO{Kind: StreamEventError, Error: errorString(err)}
	var setup RuntimeSetupError
	if errors.As(err, &setup) {
		event.ErrorCode = setup.Code
		event.Recoverable = setup.Retryable
		if event.ErrorCode != "" {
			return event
		}
	}
	message := strings.ToLower(event.Error)
	switch {
	case errors.Is(err, ErrForbidden),
		strings.Contains(message, ErrForbidden.Error()),
		strings.Contains(message, "permission denied"):
		event.ErrorCode = StreamErrorCodePermissionDenied
		event.Recoverable = false
	case errors.Is(err, ErrInvalidRequest),
		errors.Is(err, missioncontrol.ErrInvalidRequest),
		strings.Contains(message, ErrInvalidRequest.Error()),
		strings.Contains(message, missioncontrol.ErrInvalidRequest.Error()):
		event.ErrorCode = StreamErrorCodeInvalidRequest
		event.Recoverable = false
	case strings.Contains(message, "pageindex_retrieval_prompt_budget_exceeded"),
		strings.Contains(message, "retrieval prompt exceeded its safe model context budget"):
		event.ErrorCode = streamErrorCodeRetrievalPromptBudget
		event.Recoverable = false
	}
	return event
}

func errorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func trimRuntimeUnavailableCause(cause string) string {
	cause = strings.TrimSpace(cause)
	prefix := ErrRuntimeUnavailable.Error() + ":"
	for strings.HasPrefix(cause, prefix) {
		cause = strings.TrimSpace(strings.TrimPrefix(cause, prefix))
	}
	return cause
}

func trimRuntimeExecutionCause(cause string) string {
	cause = strings.TrimSpace(cause)
	prefix := ErrRuntimeExecution.Error() + ":"
	for strings.HasPrefix(cause, prefix) {
		cause = strings.TrimSpace(strings.TrimPrefix(cause, prefix))
	}
	return cause
}
