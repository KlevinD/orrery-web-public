package email

import (
	"context"
	"testing"

	"orrery_v2/internal/gateway/protocol"
)

type failingEmailGateway struct{ calls int }

func (f *failingEmailGateway) SubmitTurn(context.Context, protocol.SubmitTurnInput) <-chan protocol.Event {
	f.calls++
	stream := make(chan protocol.Event, 1)
	stream <- protocol.Event{Type: protocol.EventError, Message: "password=secret", Recoverable: true}
	close(stream)
	return stream
}

func TestEmailFailureSendsNothingAndDoesNotConsumeRetry(t *testing.T) {
	client := &fakeClient{}
	gateway := &failingEmailGateway{}
	handle := &Handle{gateway: gateway, client: client, processed: map[string]struct{}{}, active: map[string]struct{}{}}
	message := InboundEmail{MessageID: "failed", ThreadID: "thread", From: "user@example.test"}
	for i := 0; i < 2; i++ {
		if err := handle.HandleInbound(context.Background(), message); err == nil {
			t.Fatal("failure was acknowledged as success")
		}
	}
	if gateway.calls != 2 || len(client.replies) != 0 {
		t.Fatalf("calls=%d replies=%+v", gateway.calls, client.replies)
	}
	if reply, ok := RenderReply(message, []protocol.Event{{Type: protocol.EventError, Message: "password=secret"}}); ok || reply.Body != "" {
		t.Fatalf("error became email: %+v", reply)
	}
}
