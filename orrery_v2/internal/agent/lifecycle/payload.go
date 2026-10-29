package lifecycle

import (
	"strings"

	agentcore "orrery_v2/internal/agent/core"
	missioncore "orrery_v2/internal/mission/core"
)

type SignalPayload interface {
	ValidateSignalPayload() error
}

type EmptyPayload struct{}

func (EmptyPayload) ValidateSignalPayload() error { return nil }

type TaskReceivedPayload struct {
	TaskID missioncore.TaskID
}

func (TaskReceivedPayload) ValidateSignalPayload() error { return nil }

type TaskAcceptedPayload struct {
	AcceptedBy agentcore.AgentID
}

func (TaskAcceptedPayload) ValidateSignalPayload() error { return nil }

type ContextBuildPayload struct {
	ContextPackRef string
	RawMemory      string
}

func (p ContextBuildPayload) ValidateSignalPayload() error {
	if strings.TrimSpace(p.RawMemory) != "" {
		return SignalValidationError{Field: "payload.raw_memory", Cause: ErrInvalidSignal}
	}
	return nil
}

type RuntimeStartPayload struct {
	RuntimeRequestRef string
	SecretRef         string
}

func (p RuntimeStartPayload) ValidateSignalPayload() error {
	if strings.TrimSpace(p.SecretRef) != "" {
		return SignalValidationError{Field: "payload.secret_ref", Cause: ErrInvalidSignal}
	}
	return nil
}

type RuntimeProgressPayload struct {
	RuntimeRequestRef string
	ProgressRef       string
	Message           string
	FullTrace         string
}

func (p RuntimeProgressPayload) ValidateSignalPayload() error {
	if strings.TrimSpace(p.FullTrace) != "" {
		return SignalValidationError{Field: "payload.full_trace", Cause: ErrInvalidSignal}
	}
	return nil
}

type RuntimeFinishPayload struct {
	RuntimeRequestRef  string
	RuntimeResponseRef string
}

func (RuntimeFinishPayload) ValidateSignalPayload() error { return nil }

type ResultPayload struct {
	ResultRef string
}

func (ResultPayload) ValidateSignalPayload() error { return nil }

type AgentReplyPayload struct {
	MessageRef string
}

func (AgentReplyPayload) ValidateSignalPayload() error { return nil }

type FailurePayload struct {
	Code    string
	Message string
}

func (FailurePayload) ValidateSignalPayload() error { return nil }

type UnsafePayloadMarker struct {
	ContainsFullMemory bool
	ContainsFullTrace  bool
}

func (p UnsafePayloadMarker) ValidateSignalPayload() error {
	if p.ContainsFullMemory {
		return SignalValidationError{Field: "payload", Cause: ErrInvalidSignal}
	}
	if p.ContainsFullTrace {
		return SignalValidationError{Field: "payload", Cause: ErrInvalidSignal}
	}
	return nil
}
