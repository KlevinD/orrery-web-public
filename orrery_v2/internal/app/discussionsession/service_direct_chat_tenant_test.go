package discussionsession

import (
	"context"
	"testing"
	"time"
)

func TestServiceDirectConversationPersistsViewerTenant(t *testing.T) {
	repo := newMemoryRepository(t)
	service := newTestService(ServiceOptions{
		Repository: repo,
		MentionResponder: &capturingDiscussionAgentMentionResponder{response: DiscussionAgentResponse{
			AssistantMessage: "Handled directly.",
			Status:           "completed",
			RuntimeKind:      "managed",
		}},
		Clock:       fixedClock{now: testTime},
		IDGenerator: fixedIDGen("conv-direct", "msg-user", "run-direct", "msg-direct"),
	})
	viewer := ViewerRef{ViewerID: "H000001", OwnerID: "ding", ViewerKind: ViewerKindUser, DisplayName: "Ding", TenantID: "minvia"}

	dto, err := service.SendMessage(context.Background(), viewer, SendMessageCommand{
		Content:        "Take a screenshot.",
		DirectAgentID:  "staffing-agent",
		IdempotencyKey: "direct-tenant-create",
	})
	if err != nil {
		t.Fatal(err)
	}
	if dto.TenantID != "minvia" || dto.HILMode != DiscussionHILModeManual {
		t.Fatalf("conversation tenant = %q HIL mode = %q, want minvia/manual", dto.TenantID, dto.HILMode)
	}
	stored, err := repo.GetConversation(context.Background(), dto.ConversationID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.TenantID != "minvia" || stored.HILMode != DiscussionHILModeManual {
		t.Fatalf("stored conversation tenant = %q HIL mode = %q, want minvia/manual", stored.TenantID, stored.HILMode)
	}
}

func TestServiceNewConversationPersistsHILModeForConversationKind(t *testing.T) {
	for _, tc := range []struct {
		name    string
		kind    string
		hilMode string
	}{
		{name: "group", kind: ConversationKindGroup, hilMode: DiscussionHILModeManual},
		{name: "personal-assistant", kind: ConversationKindPersonalAssistant, hilMode: DiscussionHILModeAuto},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := newMemoryRepository(t)
			service := newTestService(ServiceOptions{
				Repository:  repo,
				Clock:       fixedClock{now: testTime},
				IDGenerator: fixedIDGen("conv-"+tc.name, "msg-"+tc.name),
			})
			viewer := ViewerRef{ViewerID: "H000001", OwnerID: "ding", ViewerKind: ViewerKindUser, DisplayName: "Ding", TenantID: "minvia"}

			conversationID, _, err := service.beginUserMessage(context.Background(), viewer, SendMessageCommand{
				ConversationKind:     tc.kind,
				ForceNewConversation: true,
				Content:              "Start a conversation.",
				IdempotencyKey:       "new-" + tc.name,
			})
			if err != nil {
				t.Fatal(err)
			}
			stored, err := repo.GetConversation(context.Background(), conversationID)
			if err != nil {
				t.Fatal(err)
			}
			if stored.TenantID != "minvia" || stored.HILMode != tc.hilMode {
				t.Fatalf("stored conversation tenant = %q HIL mode = %q, want minvia/%s", stored.TenantID, stored.HILMode, tc.hilMode)
			}
		})
	}
}

func TestServiceDirectConversationBackfillsLegacyTenantOnCreatorMessage(t *testing.T) {
	repo := newMemoryRepository(t)
	saveDirectTenantTestConversation(t, repo, "conv-legacy", "")
	service := newTestService(ServiceOptions{
		Repository: repo,
		MentionResponder: &capturingDiscussionAgentMentionResponder{response: DiscussionAgentResponse{
			AssistantMessage: "Handled directly.",
			Status:           "completed",
			RuntimeKind:      "managed",
		}},
		Clock:       fixedClock{now: testTime.Add(time.Minute)},
		IDGenerator: fixedIDGen("msg-user", "run-direct", "msg-direct"),
	})
	viewer := ViewerRef{ViewerID: "H000001", OwnerID: "ding", ViewerKind: ViewerKindUser, DisplayName: "Ding", TenantID: "minvia"}

	dto, err := service.SendMessage(context.Background(), viewer, SendMessageCommand{
		ConversationID: "conv-legacy",
		Content:        "Take another screenshot.",
		IdempotencyKey: "direct-tenant-backfill",
	})
	if err != nil {
		t.Fatal(err)
	}
	if dto.TenantID != "minvia" || dto.HILMode != DiscussionHILModeManual {
		t.Fatalf("conversation tenant = %q HIL mode = %q, want minvia/manual", dto.TenantID, dto.HILMode)
	}
	stored, err := repo.GetConversation(context.Background(), "conv-legacy")
	if err != nil {
		t.Fatal(err)
	}
	if stored.TenantID != "minvia" || stored.HILMode != DiscussionHILModeManual {
		t.Fatalf("stored legacy conversation tenant = %q HIL mode = %q, want minvia/manual", stored.TenantID, stored.HILMode)
	}
}

func TestServiceDirectConversationDoesNotReuseOtherTenant(t *testing.T) {
	repo := newMemoryRepository(t)
	saveDirectTenantTestConversation(t, repo, "conv-tenant-a", "tenant-a")
	service := newTestService(ServiceOptions{
		Repository: repo,
		MentionResponder: &capturingDiscussionAgentMentionResponder{response: DiscussionAgentResponse{
			AssistantMessage: "Handled directly.",
			Status:           "completed",
			RuntimeKind:      "managed",
		}},
		Clock:       fixedClock{now: testTime.Add(time.Minute)},
		IDGenerator: fixedIDGen("conv-tenant-b", "msg-user", "run-direct", "msg-direct"),
	})
	viewer := ViewerRef{ViewerID: "H000001", OwnerID: "ding", ViewerKind: ViewerKindUser, DisplayName: "Ding", TenantID: "tenant-b"}

	dto, err := service.SendMessage(context.Background(), viewer, SendMessageCommand{
		Content:        "Start in tenant B.",
		DirectAgentID:  "staffing-agent",
		IdempotencyKey: "direct-tenant-isolation",
	})
	if err != nil {
		t.Fatal(err)
	}
	if dto.ConversationID != "conv-tenant-b" || dto.TenantID != "tenant-b" {
		t.Fatalf("new conversation = %q tenant %q, want conv-tenant-b in tenant-b", dto.ConversationID, dto.TenantID)
	}
	old, err := repo.GetConversation(context.Background(), "conv-tenant-a")
	if err != nil {
		t.Fatal(err)
	}
	if old.TenantID != "tenant-a" || len(old.Messages) != 0 {
		t.Fatalf("other-tenant conversation changed: %+v", old.ConversationRecord)
	}
}

func TestLegacyDirectConversationTenantRequiresCreatorMembership(t *testing.T) {
	view := ConversationRecordView{
		ConversationRecord: ConversationRecord{ConversationID: "conv-legacy", ConversationKind: "direct", CreatedBy: "H000001"},
		Members:            []ConversationMemberRecord{{ConversationID: "conv-legacy", MemberKind: "user", MemberID: "H000002", DisplayName: "Other User"}},
	}
	viewer := ViewerRef{ViewerID: "H000001", ViewerKind: ViewerKindUser, TenantID: "minvia"}
	if tenantID, ok := legacyDirectConversationTenant(view, viewer); ok {
		t.Fatalf("tenantless conversation was adopted without creator membership: %q", tenantID)
	}

	view.Members = append(view.Members, ConversationMemberRecord{ConversationID: "conv-legacy", MemberKind: "user", MemberID: "H000001", DisplayName: "Ding"})
	viewer.ViewerID = "H000002"
	if tenantID, ok := legacyDirectConversationTenant(view, viewer); ok {
		t.Fatalf("tenantless conversation was adopted by a non-creator: %q", tenantID)
	}
}

func saveDirectTenantTestConversation(t *testing.T, repo *Repository, conversationID string, tenantID string) {
	t.Helper()
	ctx := context.Background()
	if err := repo.SaveConversation(ctx, ConversationRecord{
		ConversationID:   conversationID,
		TenantID:         tenantID,
		ConversationKind: "direct",
		DirectAgentID:    "staffing-agent",
		DirectTargetKind: "agent",
		DirectTargetID:   "staffing-agent",
		Title:            "Staffing Agent",
		Status:           "completed",
		CreatedBy:        "H000001",
		OwnerID:          "ding",
		CreatedAt:        testTime,
		UpdatedAt:        testTime,
	}); err != nil {
		t.Fatal(err)
	}
	for _, member := range []ConversationMemberRecord{
		{ConversationID: conversationID, MemberKind: "agent", MemberID: "staffing-agent", DisplayName: "Staffing Agent", Role: "Direct target", CreatedAt: testTime},
		{ConversationID: conversationID, MemberKind: "user", MemberID: "H000001", DisplayName: "Ding", Role: "Creator", CreatedAt: testTime.Add(time.Nanosecond)},
	} {
		if err := repo.SaveMember(ctx, member); err != nil {
			t.Fatal(err)
		}
	}
}
