// Historical source: AionCore aionui-session, Copyright 2025 AionUi
// (aionui.com), Apache-2.0. Go adaptation and modifications: Minvia, 2026.
// See docs/runtime/runtime-source-provenance.md for scope and the full license.
package core

import "time"

// Reference: aionui-session/backend/types.rs@v0.1.52.

type EventSource string

const (
	EventSourceProvider     EventSource = "provider"
	EventSourceOrchestrator EventSource = "orchestrator"
	EventSourceRecovery     EventSource = "recovery"
	EventSourceLifecycle    EventSource = "lifecycle"
)

type EventProjection string

const (
	EventProjectionCanonical  EventProjection = "canonical"
	EventProjectionDiagnostic EventProjection = "diagnostic"
)

type SessionEnvelope struct {
	SessionID      SessionID       `json:"session_id"`
	TurnGeneration uint64          `json:"turn_generation"`
	Sequence       uint64          `json:"sequence"`
	Timestamp      time.Time       `json:"timestamp"`
	Source         EventSource     `json:"source"`
	Projection     EventProjection `json:"projection"`
	Event          SessionEvent    `json:"event"`
}

func (e SessionEnvelope) Validate() error {
	if e.SessionID == "" {
		return NewRuntimeBridgeError(RuntimeBridgeInvalidPayload, "envelope session_id is required", false)
	}
	if e.Timestamp.IsZero() {
		return NewRuntimeBridgeError(RuntimeBridgeInvalidPayload, "envelope timestamp is required", false)
	}
	switch e.Source {
	case EventSourceProvider, EventSourceOrchestrator, EventSourceRecovery, EventSourceLifecycle:
	default:
		return NewRuntimeBridgeError(RuntimeBridgeInvalidPayload, "invalid envelope source", false)
	}
	switch e.Projection {
	case EventProjectionCanonical, EventProjectionDiagnostic:
	default:
		return NewRuntimeBridgeError(RuntimeBridgeInvalidPayload, "invalid envelope projection", false)
	}
	return e.Event.Validate()
}
