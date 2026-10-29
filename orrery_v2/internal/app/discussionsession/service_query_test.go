package discussionsession

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func TestServiceListConversationsKeepsLatestPersonalAssistantOnFirstPage(t *testing.T) {
	repo := newMemoryRepository(t)
	service := newTestService(ServiceOptions{Repository: repo})
	ctx := context.Background()
	for i := 0; i < 25; i++ {
		id := fmt.Sprintf("conv-%02d", i)
		if err := repo.SaveConversation(ctx, ConversationRecord{
			ConversationID: id,
			Title:          id,
			Status:         "completed",
			CreatedBy:      "user-1",
			CreatedAt:      testTime.Add(time.Duration(i) * time.Minute),
			UpdatedAt:      testTime.Add(time.Duration(i) * time.Minute),
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := repo.SaveConversation(ctx, ConversationRecord{
		ConversationID:   "conv-assistant",
		ConversationKind: ConversationKindPersonalAssistant,
		Title:            "Personal assistant",
		Status:           "completed",
		CreatedBy:        "user-1",
		CreatedAt:        testTime.Add(-time.Minute),
		UpdatedAt:        testTime.Add(-time.Minute),
	}); err != nil {
		t.Fatal(err)
	}

	viewer := ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser}
	first, err := service.ListConversations(ctx, viewer, ListConversationsQuery{Page: 1, PageSize: 20})
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.ListConversations(ctx, viewer, ListConversationsQuery{Page: 2, PageSize: 20})
	if err != nil {
		t.Fatal(err)
	}

	if first.Page.Total != 26 || len(first.Conversations) != 20 || first.Conversations[0].ConversationID != "conv-assistant" {
		t.Fatalf("first page = %+v", first)
	}
	if len(second.Conversations) != 6 || second.Conversations[0].ConversationID != "conv-05" {
		t.Fatalf("second page = %+v", second)
	}
	for _, conversation := range second.Conversations {
		if conversation.ConversationID == "conv-assistant" {
			t.Fatalf("personal assistant was duplicated on second page: %+v", second.Conversations)
		}
	}
}

func TestServiceListConversationsPrefersWeixinPersonalAssistantOnFirstPage(t *testing.T) {
	repo := newMemoryRepository(t)
	service := newTestService(ServiceOptions{Repository: repo})
	ctx := context.Background()
	for i := 0; i < 25; i++ {
		id := fmt.Sprintf("conv-%02d", i)
		if err := repo.SaveConversation(ctx, ConversationRecord{
			ConversationID: id,
			Title:          id,
			Status:         "completed",
			CreatedBy:      "user-1",
			CreatedAt:      testTime.Add(time.Duration(i) * time.Minute),
			UpdatedAt:      testTime.Add(time.Duration(i) * time.Minute),
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := repo.SaveConversation(ctx, ConversationRecord{
		ConversationID:   "conv-assistant-browser",
		ConversationKind: ConversationKindPersonalAssistant,
		Title:            "Browser assistant",
		Status:           "completed",
		CreatedBy:        "user-1",
		CreatedAt:        testTime.Add(30 * time.Minute),
		UpdatedAt:        testTime.Add(30 * time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveConversation(ctx, ConversationRecord{
		ConversationID:   "conv-assistant-weixin",
		ConversationKind: ConversationKindPersonalAssistant,
		Title:            "Weixin assistant",
		Status:           "completed",
		CreatedBy:        "user-1",
		CreatedAt:        testTime.Add(-time.Minute),
		UpdatedAt:        testTime.Add(-time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveTurn(ctx, TurnRecord{
		TurnID:         "turn-weixin",
		ConversationID: "conv-assistant-weixin",
		IdempotencyKey: "gwin_test",
		Status:         "completed",
		CreatedAt:      testTime,
		UpdatedAt:      testTime,
	}); err != nil {
		t.Fatal(err)
	}

	viewer := ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser}
	first, err := service.ListConversations(ctx, viewer, ListConversationsQuery{Page: 1, PageSize: 20})
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.ListConversations(ctx, viewer, ListConversationsQuery{Page: 2, PageSize: 20})
	if err != nil {
		t.Fatal(err)
	}

	if first.Page.Total != 27 || len(first.Conversations) != 20 || first.Conversations[0].ConversationID != "conv-assistant-weixin" {
		t.Fatalf("first page = %+v", first)
	}
	if len(second.Conversations) != 7 || second.Conversations[0].ConversationID != "conv-06" {
		t.Fatalf("second page = %+v", second)
	}
	for _, conversation := range second.Conversations {
		if conversation.ConversationID == "conv-assistant-weixin" {
			t.Fatalf("Weixin personal assistant was duplicated on second page: %+v", second.Conversations)
		}
	}
}
