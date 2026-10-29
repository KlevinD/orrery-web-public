package discussionsession

import (
	"context"
	"testing"
	"time"
)

func TestPersistPartialAssistantMessageIsIdempotentPerRun(t *testing.T) {
	repo := newMemoryRepository(t)
	ctx := context.Background()
	if err := repo.SaveConversation(ctx, ConversationRecord{ConversationID: "conv-1", ConversationKind: "group", Title: "Launch", Status: "running", CreatedBy: "user-1", CreatedAt: testTime, UpdatedAt: testTime}); err != nil {
		t.Fatal(err)
	}
	for _, member := range []ConversationMemberRecord{
		{ConversationID: "conv-1", MemberID: "user-1", MemberKind: "user", DisplayName: "Local User", CreatedAt: testTime},
		{ConversationID: "conv-1", MemberID: "lead-agent", MemberKind: "agent", DisplayName: "Lead", CreatedAt: testTime},
	} {
		if err := repo.SaveMember(ctx, member); err != nil {
			t.Fatal(err)
		}
	}
	if err := repo.SaveMessage(ctx, MessageRecord{MessageID: "msg-user", ConversationID: "conv-1", TurnID: "turn-1", Role: "user", AuthorID: "user-1", AuthorName: "Local User", Content: "Plan it", CreatedAt: testTime}); err != nil {
		t.Fatal(err)
	}
	run := RunRecord{RunID: "run-1", ConversationID: "conv-1", TurnID: "turn-1", Mode: "group", AgentID: "lead-agent", Status: "running", StartedAt: testTime.Add(time.Second), CreatedAt: testTime.Add(time.Second)}
	if err := repo.SaveRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	realtime := &recordingRealtimePublisher{}
	service := newTestService(ServiceOptions{Repository: repo, RealtimePublisher: realtime, Clock: fixedClock{now: testTime.Add(2 * time.Second)}})
	response := DiscussionAgentResponse{AssistantMessage: "partial output"}

	first, err := service.persistPartialAssistantMessage(ctx, "conv-1", run, response, testTime.Add(2*time.Second), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.persistPartialAssistantMessage(ctx, "conv-1", run, response, testTime.Add(3*time.Second), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if first == nil || second != nil {
		t.Fatalf("partial results = first:%+v second:%+v", first, second)
	}
	view, err := repo.GetConversation(ctx, "conv-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Messages) != 2 || view.Messages[1].RunID != "run-1" || view.Messages[1].Metadata[discussionPartialMessageMetadataKey] != "true" {
		t.Fatalf("messages = %+v", view.Messages)
	}
	if got := len(realtime.eventsByType(RealtimeEventDiscussionMessageCompleted)); got != 1 {
		t.Fatalf("message completed realtime events = %d", got)
	}
}

func TestPersistPartialPersonalAssistantMessageDoesNotRequireAgentMember(t *testing.T) {
	repo := newMemoryRepository(t)
	ctx := context.Background()
	if err := repo.SaveConversation(ctx, ConversationRecord{
		ConversationID: "conv-assistant", ConversationKind: ConversationKindPersonalAssistant,
		Title: "Personal Assistant", Status: "running", CreatedBy: "user-1", CreatedAt: testTime, UpdatedAt: testTime,
	}); err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveMember(ctx, ConversationMemberRecord{
		ConversationID: "conv-assistant", MemberID: "user-1", MemberKind: "user", DisplayName: "Local User", CreatedAt: testTime,
	}); err != nil {
		t.Fatal(err)
	}
	run := RunRecord{
		RunID: "run-assistant", ConversationID: "conv-assistant", TurnID: "turn-assistant", Mode: "group",
		AgentID: "lead-agent", AgentName: "Personal Assistant", Status: "running", StartedAt: testTime.Add(time.Second), CreatedAt: testTime.Add(time.Second),
	}
	if err := repo.SaveRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	service := newTestService(ServiceOptions{Repository: repo, Clock: fixedClock{now: testTime.Add(2 * time.Second)}})

	message, err := service.persistPartialAssistantMessage(ctx, "conv-assistant", run, DiscussionAgentResponse{AssistantMessage: "Partial result"}, testTime.Add(2*time.Second), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if message == nil || message.AuthorName != "Personal Assistant" || message.Content != discussionPartialMessageFallback {
		t.Fatalf("partial personal assistant message = %+v", message)
	}
}
