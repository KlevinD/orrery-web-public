package discussionsession

import (
	"context"
	"testing"
	"time"
)

func TestServiceSendMessageDispatchesPAOriginHostMention(t *testing.T) {
	ctx := context.Background()
	repo := newMemoryRepository(t)
	if err := repo.SaveConversation(ctx, ConversationRecord{
		ConversationID:                   "conv-pa-mission",
		ConversationKind:                 ConversationKindGroup,
		TenantID:                         "tenant-a",
		OwnerID:                          "account-1",
		CreatedBy:                        "user-1",
		PersonalAssistantOriginOwnerID:   "account-1",
		PersonalAssistantOriginSubjectID: "user-1",
		Status:                           "completed",
		CreatedAt:                        testTime,
		UpdatedAt:                        testTime,
	}); err != nil {
		t.Fatal(err)
	}
	seedDiscussionMembers(t, repo, "conv-pa-mission", "user-1")

	runner := &capturingDiscussionAgentRunner{response: DiscussionAgentResponse{
		AssistantMessage: "[@Staffing](mention://agent/staffing-agent) Review the launch plan.",
		Status:           "completed",
		RuntimeKind:      "managed",
	}}
	responder := &capturingDiscussionAgentMentionResponder{response: DiscussionAgentResponse{
		AssistantMessage: "Review complete.",
		Status:           "completed",
		RuntimeKind:      "managed",
	}}
	service := newTestService(ServiceOptions{
		Repository:       repo,
		DiscussionAgent:  runner,
		MentionResponder: responder,
		Clock:            fixedClock{now: testTime.Add(time.Minute)},
		IDGenerator:      fixedIDGen("msg-user", "run-lead", "msg-lead", "run-staffing", "msg-staffing"),
	})

	dto, err := service.SendMessage(ctx, ViewerRef{
		ViewerID:    "user-1",
		OwnerID:     "account-1",
		ViewerKind:  ViewerKindUser,
		TenantID:    "tenant-a",
		DisplayName: "User",
	}, SendMessageCommand{
		ConversationID: "conv-pa-mission",
		Content:        "Start the mission.",
		IdempotencyKey: "pa-origin-host-mention",
	})
	if err != nil {
		t.Fatal(err)
	}

	logicalPAID := PersonalAssistantAgentID("tenant-a", "account-1")
	if !runner.called || runner.request.AgentID != "lead-agent" || runner.request.LogicalAgentID != logicalPAID {
		t.Fatalf("host runtime request = %+v", runner.request)
	}
	if !responder.called || responder.request.TargetAgent.ID != "staffing-agent" {
		t.Fatalf("mention responder request = %+v", responder.request)
	}
	if responder.request.SourceMessage.AuthorID != logicalPAID {
		t.Fatalf("mention source author = %q, want PA logical id %q", responder.request.SourceMessage.AuthorID, logicalPAID)
	}
	if len(dto.Messages) != 3 {
		t.Fatalf("messages = %+v", dto.Messages)
	}
	if dto.Messages[1].AuthorID != logicalPAID || dto.Messages[2].AuthorID != "staffing-agent" {
		t.Fatalf("message authors = %+v", dto.Messages)
	}
}
