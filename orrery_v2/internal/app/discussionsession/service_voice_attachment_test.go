package discussionsession

import (
	"context"
	"errors"
	"testing"
)

func TestServiceSendMessageAcceptsVoiceOnlyAttachment(t *testing.T) {
	repo := newMemoryRepository(t)
	runner := &capturingDiscussionAgentRunner{
		agentID: "lead-agent",
		response: DiscussionAgentResponse{
			AssistantMessage: "收到语音。",
			Status:           "completed",
			RuntimeKind:      "managed",
		},
	}
	service := newTestService(ServiceOptions{
		Repository:      repo,
		DiscussionAgent: runner,
		AttachmentStore: NewLocalAttachmentStore(t.TempDir()),
		Clock:           fixedClock{now: testTime},
		IDGenerator:     fixedIDGen("msg-user", "run-1", "msg-assistant"),
	})
	ctx := context.Background()
	if err := repo.SaveConversation(ctx, ConversationRecord{ConversationID: "conv-voice", Title: "Voice", Status: "completed", CreatedBy: "user-1", CreatedAt: testTime, UpdatedAt: testTime}); err != nil {
		t.Fatal(err)
	}
	for _, member := range []ConversationMemberRecord{
		{ConversationID: "conv-voice", MemberKind: "user", MemberID: "user-1", DisplayName: "本地用户", CreatedAt: testTime},
		{ConversationID: "conv-voice", MemberKind: "agent", MemberID: "lead-agent", DisplayName: "顾明澈", Role: "Lead", CreatedAt: testTime},
	} {
		if err := repo.SaveMember(ctx, member); err != nil {
			t.Fatal(err)
		}
	}
	if err := repo.SaveAttachment(ctx, AttachmentRecord{
		AttachmentID:   "att-voice",
		ConversationID: "conv-voice",
		FileName:       "voice.webm",
		ContentType:    "audio/webm",
		SizeBytes:      14,
		CreatedAt:      testTime,
	}); err != nil {
		t.Fatal(err)
	}

	dto, err := service.SendMessage(ctx, ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser}, SendMessageCommand{
		ConversationID: "conv-voice",
		AttachmentIDs:  []string{"att-voice"},
		IdempotencyKey: "send-voice",
	})
	if err != nil {
		t.Fatal(err)
	}
	if runner.request.Content != "" || len(runner.request.Attachments) != 1 || runner.request.Attachments[0].ContentType != "audio/webm" {
		t.Fatalf("runtime request = %+v", runner.request)
	}
	if len(dto.Messages) == 0 || dto.Messages[0].Content != "" || len(dto.Messages[0].Attachments) != 1 {
		t.Fatalf("voice message = %+v", dto.Messages)
	}

	for _, attachmentIDs := range [][]string{nil, {"", "  "}} {
		_, err := service.SendMessage(ctx, ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser}, SendMessageCommand{
			ConversationID: "conv-voice",
			AttachmentIDs:  attachmentIDs,
			IdempotencyKey: "send-empty",
		})
		if !errors.Is(err, ErrInvalidRequest) {
			t.Fatalf("empty message attachment ids %q error = %v, want ErrInvalidRequest", attachmentIDs, err)
		}
	}
}
