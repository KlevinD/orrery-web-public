package discussionsession

import (
	"context"
	"errors"
	"testing"
)

func TestServiceConversationAccessFiltersAndRejectsNonMembers(t *testing.T) {
	ctx := context.Background()
	repo := newMemoryRepository(t)
	service := newTestService(ServiceOptions{
		Repository:      repo,
		DiscussionAgent: &capturingDiscussionAgentRunner{},
		Clock:           fixedClock{now: testTime},
		IDGenerator:     fixedIDGen("msg-1"),
	})
	if err := repo.SaveConversation(ctx, ConversationRecord{ConversationID: "conv-owner", Title: "Owner", Status: "completed", CreatedBy: "user-1", CreatedAt: testTime, UpdatedAt: testTime}); err != nil {
		t.Fatalf("save owner conversation: %v", err)
	}
	if err := repo.SaveMember(ctx, ConversationMemberRecord{ConversationID: "conv-owner", MemberKind: "user", MemberID: "user-1", DisplayName: "Owner", CreatedAt: testTime}); err != nil {
		t.Fatalf("save owner member: %v", err)
	}
	if err := repo.SaveConversation(ctx, ConversationRecord{ConversationID: "conv-other", Title: "Other", Status: "completed", CreatedBy: "user-2", CreatedAt: testTime, UpdatedAt: testTime}); err != nil {
		t.Fatalf("save other conversation: %v", err)
	}
	if err := repo.SaveMember(ctx, ConversationMemberRecord{ConversationID: "conv-other", MemberKind: "user", MemberID: "user-2", DisplayName: "Other", CreatedAt: testTime}); err != nil {
		t.Fatalf("save other member: %v", err)
	}

	viewer := ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser}
	page, err := service.ListConversations(ctx, viewer, ListConversationsQuery{})
	if err != nil {
		t.Fatalf("ListConversations failed: %v", err)
	}
	if page.Page.Total != 1 || len(page.Conversations) != 1 || page.Conversations[0].ConversationID != "conv-owner" {
		t.Fatalf("page = %+v", page)
	}
	if _, err := service.GetConversation(ctx, viewer, "conv-other"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("GetConversation error = %v, want ErrForbidden", err)
	}
	if _, err := service.ListMessages(ctx, viewer, "conv-other", ListMessagesQuery{}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("ListMessages error = %v, want ErrForbidden", err)
	}
	system := ViewerRef{ViewerID: "scheduler", ViewerKind: ViewerKindSystem}
	if _, err := service.GetConversation(ctx, system, "conv-owner"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("system GetConversation error = %v, want ErrForbidden", err)
	}
	page, err = service.ListConversations(ctx, system, ListConversationsQuery{})
	if err != nil {
		t.Fatalf("system ListConversations failed: %v", err)
	}
	if page.Page.Total != 0 || len(page.Conversations) != 0 {
		t.Fatalf("system viewer leaked conversations: %+v", page)
	}
	if err := service.DeleteConversation(ctx, viewer, "conv-other"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("DeleteConversation error = %v, want ErrForbidden", err)
	}
	if _, err := service.SendMessage(ctx, viewer, SendMessageCommand{ConversationID: "conv-other", Content: "hi", IdempotencyKey: "send-1"}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("SendMessage error = %v, want ErrForbidden", err)
	}
}
