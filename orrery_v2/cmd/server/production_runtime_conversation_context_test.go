package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	"orrery_v2/internal/app/discussionsession"
	"orrery_v2/internal/app/missioncontrol"
	"orrery_v2/internal/platform/testkit"
)

func TestProductionRuntimeConversationContextUsesLinkedTenantConversation(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	repository := discussionsession.NewRepository(env.DBRouter)
	conversation := discussionsession.ConversationRecord{
		ConversationID: "conversation-1", TenantID: "tenant-1", PurposeKind: "todo", PurposeID: "todo-1",
		Title: "Implement canonical discussion", CreatedAt: env.Clock.Now(), UpdatedAt: env.Clock.Now(),
	}
	if err := repository.SaveConversation(ctx, conversation); err != nil {
		t.Fatalf("SaveConversation: %v", err)
	}
	if err := repository.SaveMissionLink(ctx, discussionsession.LinkedMissionRecord{
		ConversationID: conversation.ConversationID, MissionID: "mission-1", Title: "Mission", CreatedAt: env.Clock.Now(),
	}); err != nil {
		t.Fatalf("SaveMissionLink: %v", err)
	}
	if err := repository.SaveMember(ctx, discussionsession.ConversationMemberRecord{
		ConversationID: conversation.ConversationID, MemberKind: "agent", MemberID: "agent-1", DisplayName: "Agent", CreatedAt: env.Clock.Now(),
	}); err != nil {
		t.Fatalf("SaveMember: %v", err)
	}
	view, err := repository.GetConversation(ctx, conversation.ConversationID)
	if err != nil {
		t.Fatalf("GetConversation: %v", err)
	}
	message := discussionsession.MessageRecord{
		MessageID: "message-1", ConversationID: conversation.ConversationID, Role: "user", AuthorID: "human-1", AuthorName: "Owner",
		Content: "Use the approved plan and preserve tenant isolation.", CreatedAt: env.Clock.Now(),
	}
	view.UpdatedAt = env.Clock.Now()
	if _, err := repository.ApplyTurnMutation(ctx, discussionsession.TurnAggregateMutation{
		Conversation: view.ConversationRecord, ExpectedRevision: view.Revision, Messages: []discussionsession.MessageRecord{message},
	}); err != nil {
		t.Fatalf("ApplyTurnMutation: %v", err)
	}
	provider := productionRuntimeConversationContextProvider{repository: repository}
	result, err := provider.RuntimeConversationContext(ctx, missioncontrol.RuntimeConversationContextRequest{
		TenantID: "tenant-1", MissionID: "mission-1", ConversationID: conversation.ConversationID, AgentID: "agent-1",
	})
	if err != nil {
		t.Fatalf("RuntimeConversationContext: %v", err)
	}
	if result.ConversationID != conversation.ConversationID || !strings.HasPrefix(string(result.Digest.ID), "conversation-digest-") || !strings.Contains(result.Digest.SummaryRef, "preserve tenant isolation") {
		t.Fatalf("runtime conversation context = %+v", result)
	}
	_, err = provider.RuntimeConversationContext(ctx, missioncontrol.RuntimeConversationContextRequest{
		TenantID: "tenant-other", MissionID: "mission-1", ConversationID: conversation.ConversationID, AgentID: "agent-1",
	})
	if !errors.Is(err, missioncontrol.ErrProjectionNotFound) {
		t.Fatalf("cross-tenant runtime context error = %v", err)
	}
	_, err = provider.RuntimeConversationContext(ctx, missioncontrol.RuntimeConversationContextRequest{
		TenantID: "tenant-1", MissionID: "mission-other", ConversationID: conversation.ConversationID, AgentID: "agent-1",
	})
	if !errors.Is(err, missioncontrol.ErrProjectionNotFound) {
		t.Fatalf("unlinked Mission runtime context error = %v", err)
	}
	_, err = provider.RuntimeConversationContext(ctx, missioncontrol.RuntimeConversationContextRequest{
		TenantID: "tenant-1", MissionID: "mission-1", ConversationID: conversation.ConversationID, AgentID: "agent-other",
	})
	if !errors.Is(err, missioncontrol.ErrProjectionNotFound) {
		t.Fatalf("non-member runtime context error = %v", err)
	}
}

func TestProductionRuntimeConversationContextTreatsLegacyBlankTenantAsGlobalOnly(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	repository := discussionsession.NewRepository(env.DBRouter)
	conversation := discussionsession.ConversationRecord{
		ConversationID: "conversation-legacy", Title: "Legacy Mission conversation", CreatedAt: env.Clock.Now(), UpdatedAt: env.Clock.Now(),
	}
	if err := repository.SaveConversation(ctx, conversation); err != nil {
		t.Fatalf("SaveConversation: %v", err)
	}
	if err := repository.SaveMissionLink(ctx, discussionsession.LinkedMissionRecord{
		ConversationID: conversation.ConversationID, MissionID: "mission-legacy", Title: "Legacy Mission", CreatedAt: env.Clock.Now(),
	}); err != nil {
		t.Fatalf("SaveMissionLink: %v", err)
	}
	if err := repository.SaveMember(ctx, discussionsession.ConversationMemberRecord{
		ConversationID: conversation.ConversationID, MemberKind: "agent", MemberID: "legacy-agent", DisplayName: "Legacy Agent", CreatedAt: env.Clock.Now(),
	}); err != nil {
		t.Fatalf("SaveMember: %v", err)
	}
	provider := productionRuntimeConversationContextProvider{repository: repository}
	if _, err := provider.RuntimeConversationContext(ctx, missioncontrol.RuntimeConversationContextRequest{
		TenantID: "global", MissionID: "mission-legacy", ConversationID: conversation.ConversationID, AgentID: "legacy-agent",
	}); err != nil {
		t.Fatalf("global legacy RuntimeConversationContext: %v", err)
	}
	_, err := provider.RuntimeConversationContext(ctx, missioncontrol.RuntimeConversationContextRequest{
		TenantID: "tenant-other", MissionID: "mission-legacy", ConversationID: conversation.ConversationID, AgentID: "legacy-agent",
	})
	if !errors.Is(err, missioncontrol.ErrProjectionNotFound) {
		t.Fatalf("named tenant accessed legacy global conversation: %v", err)
	}
}
