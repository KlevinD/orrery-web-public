package discussionsession

import (
	"context"
	"testing"
	"time"
)

func TestBeginUserMessageReusesLatestOwnedPersonalAssistantConversation(t *testing.T) {
	ctx := context.Background()
	repo := newMemoryRepository(t)
	seedPersonalAssistantConversation(t, repo, "conv-older", "tenant-a", "user-1", "account-1", testTime)
	seedPersonalAssistantConversation(t, repo, "conv-latest", "tenant-a", "user-1", "account-1", testTime.Add(time.Minute))
	seedPersonalAssistantConversation(t, repo, "conv-other-owner", "tenant-a", "user-1", "account-2", testTime.Add(2*time.Minute))
	seedPersonalAssistantConversation(t, repo, "conv-other-tenant", "tenant-b", "user-1", "account-1", testTime.Add(3*time.Minute))
	service := newTestService(ServiceOptions{
		Repository:  repo,
		Clock:       fixedClock{now: testTime.Add(4 * time.Minute)},
		IDGenerator: fixedIDGen("msg-reused"),
	})
	viewer := ViewerRef{ViewerID: "user-1", OwnerID: "account-1", ViewerKind: ViewerKindUser, TenantID: "tenant-a"}

	conversationID, message, err := service.beginUserMessage(ctx, viewer, SendMessageCommand{
		ConversationKind: ConversationKindPersonalAssistant,
		Content:          "Continue from Weixin",
		IdempotencyKey:   "message-reused",
	})
	if err != nil {
		t.Fatal(err)
	}
	if conversationID != "conv-latest" || message.ConversationID != "conv-latest" {
		t.Fatalf("reused conversation = %q message=%+v", conversationID, message)
	}
}

func TestBeginUserMessageCanForceNewPersonalAssistantConversation(t *testing.T) {
	ctx := context.Background()
	repo := newMemoryRepository(t)
	seedPersonalAssistantConversation(t, repo, "conv-existing", "tenant-a", "user-1", "account-1", testTime)
	service := newTestService(ServiceOptions{
		Repository:  repo,
		Clock:       fixedClock{now: testTime.Add(time.Minute)},
		IDGenerator: fixedIDGen("conv-new", "msg-new"),
	})
	viewer := ViewerRef{ViewerID: "user-1", OwnerID: "account-1", ViewerKind: ViewerKindUser, TenantID: "tenant-a"}

	conversationID, _, err := service.beginUserMessage(ctx, viewer, SendMessageCommand{
		ConversationKind:     ConversationKindPersonalAssistant,
		ForceNewConversation: true,
		Content:              "Start fresh",
		IdempotencyKey:       "message-new",
	})
	if err != nil {
		t.Fatal(err)
	}
	if conversationID != "conv-new" {
		t.Fatalf("new conversation id = %q", conversationID)
	}
	created, err := repo.GetConversation(ctx, conversationID)
	if err != nil {
		t.Fatal(err)
	}
	if created.HILMode != DiscussionHILModeAuto {
		t.Fatalf("personal assistant HIL mode = %q, want %q", created.HILMode, DiscussionHILModeAuto)
	}
}

func seedPersonalAssistantConversation(t *testing.T, repo *Repository, conversationID, tenantID, viewerID, ownerID string, updatedAt time.Time) {
	t.Helper()
	if err := repo.SaveConversation(context.Background(), ConversationRecord{
		ConversationID: conversationID, TenantID: tenantID, ConversationKind: ConversationKindPersonalAssistant,
		Title: "Personal Assistant", Status: "completed", CreatedBy: viewerID, OwnerID: ownerID,
		CreatedAt: updatedAt, UpdatedAt: updatedAt,
	}); err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveMember(context.Background(), ConversationMemberRecord{
		ConversationID: conversationID, MemberKind: "user", MemberID: viewerID, DisplayName: "User", Role: "Owner", CreatedAt: updatedAt,
	}); err != nil {
		t.Fatal(err)
	}
}
