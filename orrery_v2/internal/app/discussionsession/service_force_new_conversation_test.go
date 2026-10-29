package discussionsession

import (
	"context"
	"testing"
	"time"
)

func TestServiceSendMessageForceNewConversationStartsSeparateAgentSession(t *testing.T) {
	repo := newMemoryRepository(t)
	existing := ConversationRecord{
		ConversationID:   "conv-direct-existing",
		ConversationKind: "direct",
		DirectAgentID:    "staffing-agent",
		CreatedBy:        "local-user",
		CreatedAt:        testTime,
		UpdatedAt:        testTime,
	}
	if err := repo.SaveConversation(context.Background(), existing); err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveMember(context.Background(), ConversationMemberRecord{ConversationID: existing.ConversationID, MemberKind: "agent", MemberID: "staffing-agent", DisplayName: "Staffing Agent", CreatedAt: testTime}); err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveMember(context.Background(), ConversationMemberRecord{ConversationID: existing.ConversationID, MemberKind: "user", MemberID: "local-user", DisplayName: "Local User", CreatedAt: testTime.Add(time.Nanosecond)}); err != nil {
		t.Fatal(err)
	}
	direct := &capturingDiscussionAgentMentionResponder{response: DiscussionAgentResponse{
		AssistantMessage: "Started separately.",
		Status:           "completed",
		RuntimeKind:      "managed",
	}}
	service := newTestService(ServiceOptions{
		Repository:       repo,
		MentionResponder: direct,
		Clock:            fixedClock{now: testTime.Add(time.Minute)},
		IDGenerator:      fixedIDGen("conv-direct-new", "msg-user", "run-direct", "msg-direct"),
	})

	dto, err := service.SendMessage(context.Background(), ViewerRef{ViewerID: "local-user", ViewerKind: ViewerKindUser, DisplayName: "Local User"}, SendMessageCommand{
		Content:              "Start separately.",
		DirectAgentID:        "staffing-agent",
		ForceNewConversation: true,
		IdempotencyKey:       "direct-staffing-new-session",
	})
	if err != nil {
		t.Fatal(err)
	}

	if dto.ConversationID != "conv-direct-new" {
		t.Fatalf("conversation id = %q, want a new direct conversation", dto.ConversationID)
	}
	if !direct.called || direct.request.ConversationID != "conv-direct-new" {
		t.Fatalf("direct responder request = %+v", direct.request)
	}
	oldConversation, err := repo.GetConversation(context.Background(), existing.ConversationID)
	if err != nil {
		t.Fatal(err)
	}
	if len(oldConversation.Messages) != 0 {
		t.Fatalf("existing direct conversation messages = %+v, want unchanged", oldConversation.Messages)
	}
}
