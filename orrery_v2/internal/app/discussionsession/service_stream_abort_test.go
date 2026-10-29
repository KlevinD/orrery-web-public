package discussionsession

import (
	"context"
	"testing"
)

func TestServiceAbortConversationAppendsSnapshotInvalidationEvent(t *testing.T) {
	repo := newMemoryRepository(t)
	realtime := &recordingRealtimePublisher{}
	runner := &delayedStreamingDiscussionAgentRunner{streamEvents: make(chan DiscussionAgentStreamEvent)}
	service := newTestService(ServiceOptions{
		Repository:        repo,
		DiscussionAgent:   runner,
		RealtimePublisher: realtime,
		Clock:             fixedClock{now: testTime},
		IDGenerator:       fixedIDGen("conv-1", "msg-user", "run-1", "event-abort"),
	})
	viewer := ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser, DisplayName: "local user"}

	events, err := service.SendMessageStream(context.Background(), viewer, SendMessageCommand{Content: "Plan launch", IdempotencyKey: "send-1"})
	if err != nil {
		t.Fatal(err)
	}
	readLeadStreamEvent(t, events)
	readLeadStreamEvent(t, events)

	if _, err := service.AbortConversation(context.Background(), viewer, "conv-1"); err != nil {
		t.Fatal(err)
	}
	close(runner.streamEvents)
	drainLeadStream(events)

	invalidations := realtime.eventsByType(RealtimeEventDiscussionSnapshotInvalidated)
	if len(invalidations) != 1 {
		t.Fatalf("snapshot invalidation events = %+v", invalidations)
	}
	if invalidations[0].Channel != "conversation:conv-1" || invalidations[0].AggregateType != "conversation" || invalidations[0].AggregateID != "conv-1" {
		t.Fatalf("snapshot invalidation event = %+v", invalidations[0])
	}
	if got := realtimePayloadString(t, invalidations[0], "reason"); got != "abort" {
		t.Fatalf("snapshot invalidation reason = %q", got)
	}
	runEvents := realtime.eventsByType(RealtimeEventDiscussionRunFailed)
	if len(runEvents) != 1 || realtimePayloadString(t, runEvents[0], "status") != "cancelled" {
		t.Fatalf("run failed/cancelled events = %+v", runEvents)
	}
	userEvents := realtime.eventsByType(RealtimeEventUserDiscussionListInvalidated)
	if len(userEvents) != 2 || userEvents[1].Channel != "user:user-1" {
		t.Fatalf("user list events = %+v", userEvents)
	}
}

func TestServiceAbortConversationCancelsRunningRunAndRecordsEvent(t *testing.T) {
	repo := newMemoryRepository(t)
	runner := &delayedStreamingDiscussionAgentRunner{streamEvents: make(chan DiscussionAgentStreamEvent)}
	service := newTestService(ServiceOptions{
		Repository:      repo,
		DiscussionAgent: runner,
		Clock:           fixedClock{now: testTime},
		IDGenerator:     fixedIDGen("conv-1", "msg-user", "run-1", "event-abort"),
	})
	viewer := ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser, DisplayName: "本地用户"}

	events, err := service.SendMessageStream(context.Background(), viewer, SendMessageCommand{Content: "Plan launch", IdempotencyKey: "send-1"})
	if err != nil {
		t.Fatal(err)
	}
	if event := readLeadStreamEvent(t, events); event.Kind != StreamEventSnapshot {
		t.Fatalf("first event = %+v", event)
	}
	if event := readLeadStreamEvent(t, events); event.Kind != StreamEventTurnStarted {
		t.Fatalf("turn started = %+v", event)
	}
	if event := readLeadStreamEvent(t, events); event.Kind != StreamEventRunStarted || event.Run == nil || event.Run.RunID != "run-1" {
		t.Fatalf("run started = %+v", event)
	}
	runner.streamEvents <- DiscussionAgentStreamEvent{Kind: DiscussionAgentStreamTokenDelta, Delta: "partial answer"}
	if event := readLeadStreamEvent(t, events); event.Kind != StreamEventTokenDelta || event.Delta != "partial answer" {
		t.Fatalf("token delta = %+v", event)
	}

	dto, err := service.AbortConversation(context.Background(), viewer, "conv-1")
	if err != nil {
		t.Fatal(err)
	}
	close(runner.streamEvents)
	drainLeadStream(events)

	if dto.Status != "cancelled" || len(dto.Runs) != 1 || dto.Runs[0].Status != "cancelled" {
		t.Fatalf("aborted conversation = %+v", dto)
	}
	if len(dto.Messages) != 2 || dto.Messages[1].Content != discussionPartialMessageFallback || dto.Messages[1].Metadata[discussionPartialMessageMetadataKey] != "true" {
		t.Fatalf("aborted partial message = %+v", dto.Messages)
	}
	if len(dto.Events) != 1 || dto.Events[0].Kind != "discussion_session_aborted" || dto.Events[0].Summary != "用户中止了讨论流程" || dto.Events[0].Metadata["aborted_by"] != "user-1" {
		t.Fatalf("abort event = %+v", dto.Events)
	}
}
