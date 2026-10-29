package discussionsession

import (
	"context"
	"testing"
	"time"
)

func TestServiceDirectConversationStreamDetachesDeliveryWhenRequestContextCancels(t *testing.T) {
	repo := newMemoryRepository(t)
	events := make(chan DiscussionAgentStreamEvent)
	defer close(events)
	streamStarted := make(chan struct{}, 1)
	responder := &streamingDiscussionAgentMentionResponder{streamEvents: events, streamStarted: streamStarted}
	service := newTestService(ServiceOptions{
		Repository:       repo,
		DiscussionAgent:  &capturingDiscussionAgentRunner{agentID: "lead-agent", response: DiscussionAgentResponse{AssistantMessage: "lead should not run"}},
		MentionResponder: responder,
		Clock:            fixedClock{now: testTime},
		IDGenerator:      fixedIDGen("conv-1", "msg-user", "run-direct"),
	})
	viewer := ViewerRef{ViewerID: "local-user", ViewerKind: ViewerKindUser, DisplayName: "Local User"}
	ctx, cancel := context.WithCancel(context.Background())

	stream, err := service.SendMessageStream(ctx, viewer, SendMessageCommand{
		Content:        "Please take this directly",
		DirectAgentID:  "staffing-agent",
		IdempotencyKey: "direct-staffing-stream-cancel",
	})
	if err != nil {
		t.Fatal(err)
	}
	if event := readLeadStreamEvent(t, stream); event.Kind != StreamEventSnapshot {
		t.Fatalf("snapshot = %+v", event)
	}
	if event := readLeadStreamEvent(t, stream); event.Kind != StreamEventTurnStarted {
		t.Fatalf("turn started = %+v", event)
	}
	if event := readLeadStreamEvent(t, stream); event.Kind != StreamEventRunStarted {
		t.Fatalf("run started = %+v", event)
	}

	cancel()

	select {
	case _, ok := <-stream:
		if ok {
			t.Fatalf("direct stream should close after request context cancellation")
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatalf("direct stream did not close after request context cancellation")
	}
	select {
	case <-streamStarted:
	case <-time.After(time.Second):
		t.Fatalf("direct stream responder was not called after request context cancellation")
	}

	detail, err := service.GetConversation(context.Background(), viewer, "conv-1")
	if err != nil {
		t.Fatal(err)
	}
	if !responder.streamCalled {
		t.Fatalf("direct stream responder was not called")
	}
	if len(detail.Runs) != 1 || detail.Runs[0].Status != "running" {
		t.Fatalf("delivery disconnect must not mutate the canonical run, runs = %+v", detail.Runs)
	}
}
