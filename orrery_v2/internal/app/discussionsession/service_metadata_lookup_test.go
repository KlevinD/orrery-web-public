package discussionsession

import (
	"context"
	"testing"

	"orrery_v2/internal/platform/db"
)

func TestConversationClassificationDoesNotLoadUnrelatedMessagePayloads(t *testing.T) {
	ctx := context.Background()
	for _, kind := range []string{"direct", ConversationKindGroup, ConversationKindPersonalAssistant} {
		t.Run(kind, func(t *testing.T) {
			repo := newMemoryRepository(t)
			conversation := ConversationRecord{ConversationID: "conv-metadata", ConversationKind: kind,
				TenantID: "tenant-a", OwnerID: "user-1", CreatedBy: "user-1", Status: "running", CreatedAt: testTime, UpdatedAt: testTime}
			if err := repo.SaveConversation(ctx, conversation); err != nil {
				t.Fatal(err)
			}
			// Simulate a legacy child payload that cannot be decoded. Classification
			// depends solely on the main record and must not read this child table.
			if err := repo.router.PutRecord(ctx, messageTable, "legacy-message", db.Record{
				"message_id": "legacy-message", "conversation_id": conversation.ConversationID,
				"role": "user", "created_at": formatTime(testTime), conversationPayload: "{invalid",
			}); err != nil {
				t.Fatal(err)
			}
			if _, err := repo.GetConversation(ctx, conversation.ConversationID); err == nil {
				t.Fatal("fixture must fail when loading the entire conversation")
			}
			service := newTestService(ServiceOptions{Repository: repo})
			if got := service.isDirectConversation(ctx, conversation.ConversationID); got != (kind == "direct") {
				t.Fatalf("direct classification=%v for %s", got, kind)
			}
			policy, err := service.conversationToolPolicy(ctx, conversation.ConversationID)
			if err != nil || policy.MemberAdditionsAllowed != conversationMemberAdditionsAllowed(conversation) {
				t.Fatalf("metadata policy=%+v, err=%v", policy, err)
			}
		})
	}
}
