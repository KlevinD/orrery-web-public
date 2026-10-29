package channel

import (
	"strings"
	"testing"

	"orrery_v2/internal/gateway/protocol"
)

func TestInboundProcessingErrorKeepsFailuresOutOfChatAndPreservesRetryability(t *testing.T) {
	if err := InboundProcessingError([]protocol.Event{{
		Type: protocol.EventError, Code: protocol.ErrorCodeGatewayRouteUnavailable, Recoverable: true,
	}}); err == nil || !strings.Contains(err.Error(), protocol.ErrorCodeGatewayRouteUnavailable) {
		t.Fatalf("route infrastructure failure = %v", err)
	}
	if err := InboundProcessingError([]protocol.Event{{
		Type: protocol.EventError, Code: protocol.ErrorCodeGatewayRuntimeUnavailable, Recoverable: true,
	}}); err == nil || !strings.Contains(err.Error(), protocol.ErrorCodeGatewayRuntimeUnavailable) {
		t.Fatalf("runtime infrastructure failure = %v", err)
	}
	if err := InboundProcessingError([]protocol.Event{{
		Type: protocol.EventError, Message: "agent run failed", Recoverable: true,
	}}); err == nil || IsPermanentInboundError(err) {
		t.Fatalf("ordinary recoverable failure must remain retryable: %v", err)
	}
	if err := InboundProcessingError([]protocol.Event{{
		Type: protocol.EventError, Code: protocol.ErrorCodeGatewayPermissionDenied, Recoverable: false,
	}}); err == nil || !IsPermanentInboundError(err) {
		t.Fatalf("permission denial must terminate without a chat reply: %v", err)
	}
	if err := InboundProcessingError([]protocol.Event{{
		Type: protocol.EventError, Code: protocol.ErrorCodeGatewayRequestInvalid, Recoverable: false,
	}}); err == nil || !IsPermanentInboundError(err) {
		t.Fatalf("invalid request must terminate without a chat reply: %v", err)
	}
}

func TestInboundRunIDIsStableAndScoped(t *testing.T) {
	first := InboundRunID(" Feishu ", "app-a", "event-a")
	if first == "" || first != InboundRunID("feishu", "app-a", "event-a") {
		t.Fatalf("InboundRunID is not stable: %q", first)
	}
	if first == InboundRunID("feishu", "app-b", "event-a") || first == InboundRunID("dingtalk", "app-a", "event-a") {
		t.Fatal("InboundRunID is not scoped by channel account")
	}
	if got := InboundRunID("feishu", "app-a", ""); got != "" {
		t.Fatalf("InboundRunID with empty event = %q", got)
	}
}
