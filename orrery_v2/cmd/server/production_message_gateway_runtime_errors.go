package main

import (
	"strings"

	discussionsession "orrery_v2/internal/app/discussionsession"
	gwprotocol "orrery_v2/internal/gateway/protocol"
)

func emitGatewayRuntimeError(out chan<- gwprotocol.Event, event discussionsession.StreamEventDTO) {
	if event.ErrorCode == discussionsession.StreamErrorCodePermissionDenied {
		emitGatewayPermissionDenied(out, event.RunID)
		return
	}
	if event.ErrorCode == discussionsession.StreamErrorCodeInvalidRequest {
		emitGatewayRequestInvalid(out, event.RunID)
		return
	}
	if event.ErrorCode != "" && !event.Recoverable {
		emitGatewayRuntimeInvalid(out, event.RunID)
		return
	}
	emitGatewayRuntimeUnavailable(out, event.RunID)
}

func emitGatewayRuntimeUnavailable(out chan<- gwprotocol.Event, runID string) {
	out <- gwprotocol.Event{
		Type: gwprotocol.EventError, Code: gwprotocol.ErrorCodeGatewayRuntimeUnavailable,
		Message: "Gateway runtime is temporarily unavailable.", Recoverable: true, RunID: strings.TrimSpace(runID),
	}
}

func emitGatewayRuntimeInvalid(out chan<- gwprotocol.Event, runID string) {
	out <- gwprotocol.Event{
		Type: gwprotocol.EventError, Code: gwprotocol.ErrorCodeGatewayRuntimeInvalid,
		Message: "Gateway runtime configuration is unavailable.", Recoverable: false, RunID: strings.TrimSpace(runID),
	}
}

func emitGatewayPermissionDenied(out chan<- gwprotocol.Event, runID string) {
	out <- gwprotocol.Event{
		Type: gwprotocol.EventError, Code: gwprotocol.ErrorCodeGatewayPermissionDenied,
		Message: "Gateway request is not authorized.", Recoverable: false, RunID: strings.TrimSpace(runID),
	}
}

func emitGatewayRequestInvalid(out chan<- gwprotocol.Event, runID string) {
	out <- gwprotocol.Event{
		Type: gwprotocol.EventError, Code: gwprotocol.ErrorCodeGatewayRequestInvalid,
		Message: "Gateway request cannot be applied to the current Mission state.", Recoverable: false, RunID: strings.TrimSpace(runID),
	}
}
