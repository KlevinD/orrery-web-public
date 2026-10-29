package discussionsession

import (
	"context"
	"strings"
	"testing"
)

func TestServiceSendMessageStreamPersistsAuthoritativeCompletionInsteadOfTokenCache(t *testing.T) {
	repo := newMemoryRepository(t)
	runner := &streamingDiscussionAgentRunner{events: []DiscussionAgentStreamEvent{
		{Kind: DiscussionAgentStreamTokenDelta, Delta: "draft output"},
		{Kind: DiscussionAgentStreamRunCompleted, Response: DiscussionAgentResponse{
			AssistantMessage: "authoritative final output",
			Status:           "completed",
			RuntimeKind:      "codex",
		}},
	}}
	service := newTestService(ServiceOptions{
		Repository:      repo,
		DiscussionAgent: runner,
		Clock:           fixedClock{now: testTime},
		IDGenerator:     fixedIDGen("conv-1", "msg-user", "run-1", "msg-assistant"),
	})

	events, err := service.SendMessageStream(context.Background(), ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser}, SendMessageCommand{Content: "Plan a launch", IdempotencyKey: "send-final-wins"})
	if err != nil {
		t.Fatal(err)
	}
	drainLeadStream(events)

	detail, err := service.GetConversation(context.Background(), ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser}, "conv-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(detail.Messages) != 2 || !strings.HasSuffix(detail.Messages[1].Content, "authoritative final output") {
		t.Fatalf("messages = %+v", detail.Messages)
	}
	if detail.Messages[1].Metadata[discussionPartialMessageMetadataKey] != "" {
		t.Fatalf("completed message was marked partial: %+v", detail.Messages[1].Metadata)
	}
}

func TestServiceSendMessageStreamDoesNotPersistEmptyPartialMessage(t *testing.T) {
	repo := newMemoryRepository(t)
	runner := &streamingDiscussionAgentRunner{}
	service := newTestService(ServiceOptions{
		Repository:      repo,
		DiscussionAgent: runner,
		Clock:           fixedClock{now: testTime},
		IDGenerator:     fixedIDGen("conv-1", "msg-user", "run-1"),
	})

	events, err := service.SendMessageStream(context.Background(), ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser}, SendMessageCommand{Content: "Plan a launch", IdempotencyKey: "send-empty-partial"})
	if err != nil {
		t.Fatal(err)
	}
	drainLeadStream(events)

	detail, err := service.GetConversation(context.Background(), ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser}, "conv-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(detail.Messages) != 1 || detail.Messages[0].Role != "user" {
		t.Fatalf("messages = %+v, want only the user message", detail.Messages)
	}
}

func TestServiceSendMessageStreamCoalescesFineGrainedTokenDeltas(t *testing.T) {
	const rawDeltaCount = 1983
	repo := newMemoryRepository(t)
	runtimeEvents := make([]DiscussionAgentStreamEvent, 0, rawDeltaCount+1)
	for i := 0; i < rawDeltaCount; i++ {
		runtimeEvents = append(runtimeEvents, DiscussionAgentStreamEvent{Kind: DiscussionAgentStreamTokenDelta, Delta: "x"})
	}
	runtimeEvents = append(runtimeEvents, DiscussionAgentStreamEvent{Kind: DiscussionAgentStreamRunCompleted, Response: DiscussionAgentResponse{
		AssistantMessage: strings.Repeat("x", rawDeltaCount),
		Status:           "completed",
		RuntimeKind:      "codex",
	}})
	service := newTestService(ServiceOptions{
		Repository:      repo,
		DiscussionAgent: &streamingDiscussionAgentRunner{events: runtimeEvents},
		Clock:           fixedClock{now: testTime},
		IDGenerator:     fixedIDGen("conv-1", "msg-user", "run-1", "msg-assistant"),
	})

	events, err := service.SendMessageStream(context.Background(), ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser}, SendMessageCommand{Content: "Plan a launch", IdempotencyKey: "send-coalesced"})
	if err != nil {
		t.Fatal(err)
	}
	var streamed strings.Builder
	deltaEvents := 0
	for event := range events {
		if event.Kind != StreamEventTokenDelta {
			continue
		}
		deltaEvents++
		streamed.WriteString(event.Delta)
	}
	if streamed.Len() != rawDeltaCount {
		t.Fatalf("streamed bytes = %d, want %d", streamed.Len(), rawDeltaCount)
	}
	if deltaEvents >= 100 {
		t.Fatalf("coalesced delta events = %d, want fewer than 100 for %d provider deltas", deltaEvents, rawDeltaCount)
	}
}
