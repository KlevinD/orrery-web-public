package core

import (
	"context"
	"reflect"
	"testing"
)

// compileTimeSessionBackend exists only to prove the public port is implementable.
type compileTimeSessionBackend struct{}

var _ SessionBackend = (*compileTimeSessionBackend)(nil)

func (*compileTimeSessionBackend) Dispatch(context.Context, RuntimeCommand) (CommandReceipt, error) {
	return CommandReceipt{}, NewRuntimeBridgeError(RuntimeBridgeUnsupportedCommand, "compile-time backend", false)
}

func (*compileTimeSessionBackend) Events() <-chan SessionEnvelope {
	ch := make(chan SessionEnvelope)
	close(ch)
	return ch
}

func (*compileTimeSessionBackend) Capabilities() Capabilities { return Capabilities{} }

func (*compileTimeSessionBackend) PendingPermissionRequests() []PendingPermissionView { return nil }

func TestRuntimeBridgeReasonCodesAreStable(t *testing.T) {
	got := []RuntimeBridgeErrorCode{
		RuntimeBridgeInvalidCommand,
		RuntimeBridgeInvalidPayload,
		RuntimeBridgeUnsupportedCommand,
		RuntimeBridgeCommandConflict,
		RuntimeBridgeSessionMismatch,
		RuntimeBridgeGenerationMismatch,
		RuntimeBridgeQueueFull,
		RuntimeBridgeHandshakeTimeout,
		RuntimeBridgeWorkspaceUnavailable,
		RuntimeBridgeTransportClosed,
		RuntimeBridgeProtocolError,
		RuntimeBridgeBackendRejected,
		RuntimeBridgeUnknownPermissionRequest,
		RuntimeBridgeUnknownAuthRequest,
		RuntimeBridgeCancelled,
		RuntimeBridgeDetachedCleanNoResult,
		RuntimeBridgeDetachedCrash,
	}
	want := []RuntimeBridgeErrorCode{"invalid_command", "invalid_payload", "unsupported_command", "command_conflict", "session_mismatch", "generation_mismatch", "queue_full", "handshake_timeout", "workspace_unavailable", "transport_closed", "protocol_error", "backend_rejected", "unknown_permission_request", "unknown_auth_request", "cancelled", "detached_clean_no_result", "detached_crash"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("reason codes = %#v, want %#v", got, want)
	}
}
