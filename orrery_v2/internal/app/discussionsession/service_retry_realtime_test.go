package discussionsession

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestServiceRetryMessageAppendsRealtimeOutboxEvents(t *testing.T) {
	repo := newMemoryRepository(t)
	realtime := &recordingRealtimePublisher{}
	ctx := context.Background()
	if err := repo.SaveConversation(ctx, ConversationRecord{ConversationID: "conv-1", Title: "Launch plan", Status: "completed", CreatedBy: "user-1", CreatedAt: testTime, UpdatedAt: testTime.Add(time.Minute)}); err != nil {
		t.Fatal(err)
	}
	seedDiscussionMembers(t, repo, "conv-1", "user-1")
	if err := repo.SaveMessage(ctx, MessageRecord{MessageID: "msg-user", ConversationID: "conv-1", Role: "user", AuthorID: "user-1", Content: "Plan launch", CreatedAt: testTime}); err != nil {
		t.Fatal(err)
	}
	runner := &capturingDiscussionAgentRunner{response: DiscussionAgentResponse{AssistantMessage: "Retry response", Status: "completed", RuntimeKind: "managed"}}
	service := newTestService(ServiceOptions{
		Repository:        repo,
		DiscussionAgent:   runner,
		RealtimePublisher: realtime,
		Clock:             fixedClock{now: testTime.Add(2 * time.Minute)},
		IDGenerator:       fixedIDGen("event-retry", "run-retry", "msg-assistant-retry"),
	})

	if _, err := service.RetryMessage(ctx, ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser}, RetryMessageCommand{
		ConversationID: "conv-1",
		MessageID:      "msg-user",
		IdempotencyKey: "retry-msg-user",
	}); err != nil {
		t.Fatal(err)
	}

	wantTypes := []string{
		RealtimeEventDiscussionTurnStarted,
		RealtimeEventDiscussionRunStarted,
		RealtimeEventDiscussionMessageCompleted,
		RealtimeEventDiscussionRunCompleted,
		RealtimeEventDiscussionTurnCompleted,
		RealtimeEventUserDiscussionListInvalidated,
	}
	if got := strings.Join(realtime.eventTypes(), ","); got != strings.Join(wantTypes, ",") {
		t.Fatalf("realtime event types = %v, want %v", realtime.eventTypes(), wantTypes)
	}
	messageEvents := realtime.eventsByType(RealtimeEventDiscussionMessageCompleted)
	if len(messageEvents) != 1 || realtimePayloadString(t, messageEvents[0], "message_id") != "msg-assistant-retry" {
		t.Fatalf("message completed events = %+v", messageEvents)
	}
}
