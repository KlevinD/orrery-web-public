package discussionsession

import (
	"context"
	"errors"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
)

func TestServiceEnsureMissionConversationCreatesGroupLinkedToMission(t *testing.T) {
	repo := newMemoryRepository(t)
	service := newTestService(ServiceOptions{
		Repository: repo,
		Clock:      fixedClock{now: testTime},
	})

	conversation, err := service.EnsureMissionConversation(context.Background(), ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser, DisplayName: "本地用户"}, EnsureMissionConversationCommand{
		MissionID: "mission-1",
		Title:     "Launch plan",
		Status:    "active",
		CreatedAt: testTime,
	})
	if err != nil {
		t.Fatalf("EnsureMissionConversation returned error: %v", err)
	}
	if conversation.ConversationID == "" || conversation.ConversationKind != "" || conversation.Title != "Launch plan" || conversation.Status != "completed" {
		t.Fatalf("conversation metadata = %+v", conversation)
	}
	if conversation.TenantID != "global" {
		t.Fatalf("conversation tenant = %q, want global", conversation.TenantID)
	}
	if len(conversation.LinkedMissions) != 1 || conversation.LinkedMissions[0].MissionID != "mission-1" || conversation.LinkedMissions[0].Status != "active" {
		t.Fatalf("linked missions = %+v", conversation.LinkedMissions)
	}
	if conversation.CreatedBy != "user-1" || conversation.CreatedByName != "本地用户" {
		t.Fatalf("creator metadata = %+v", conversation)
	}
	if len(conversation.Members) != 3 {
		t.Fatalf("members = %+v, want initial agents and creator", conversation.Members)
	}
}

func TestServiceEnsureMissionConversationPersistsPersonalAssistantOriginOnGroup(t *testing.T) {
	ctx := context.Background()
	repo := newMemoryRepository(t)
	service := newTestService(ServiceOptions{
		Repository: repo,
		Clock:      fixedClock{now: testTime},
	})

	conversation, err := service.EnsureMissionConversation(ctx, ViewerRef{
		ViewerID:   "subject-1",
		OwnerID:    "account-1",
		ViewerKind: ViewerKindUser,
		TenantID:   "tenant-a",
	}, EnsureMissionConversationCommand{
		MissionID:                        "mission-pa-1",
		Title:                            "PA launch",
		Status:                           "active",
		CreatedAt:                        testTime,
		PersonalAssistantOriginOwnerID:   "account-1",
		PersonalAssistantOriginSubjectID: "subject-1",
	})
	if err != nil {
		t.Fatalf("EnsureMissionConversation returned error: %v", err)
	}
	if conversation.ConversationKind != "" || !conversation.PersonalAssistantRuntime {
		t.Fatalf("PA-origin mission conversation = %+v, want group with runtime marker", conversation)
	}
	if !conversation.MemberAdditionsAllowed {
		t.Fatalf("PA-origin mission should retain group member additions: %+v", conversation)
	}
	stored, err := repo.GetConversation(ctx, conversation.ConversationID)
	if err != nil {
		t.Fatalf("reload conversation: %v", err)
	}
	if stored.PersonalAssistantOriginOwnerID != "account-1" || stored.PersonalAssistantOriginSubjectID != "subject-1" {
		t.Fatalf("persisted PA origin = owner %q subject %q", stored.PersonalAssistantOriginOwnerID, stored.PersonalAssistantOriginSubjectID)
	}
}

func TestServiceResolvesPersonalAssistantOriginFromMarkedMissionGroup(t *testing.T) {
	ctx := context.Background()
	repo := newMemoryRepository(t)
	if err := repo.SaveConversation(ctx, ConversationRecord{
		ConversationID: "conv-pa-mission", ConversationKind: ConversationKindGroup, TenantID: "tenant-a",
		CreatedBy: "user-1", OwnerID: "account-1", PersonalAssistantOriginOwnerID: "account-1", PersonalAssistantOriginSubjectID: "user-1",
		CreatedAt: testTime, UpdatedAt: testTime,
	}); err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveMember(ctx, ConversationMemberRecord{ConversationID: "conv-pa-mission", MemberKind: "user", MemberID: "user-1", DisplayName: "User", CreatedAt: testTime}); err != nil {
		t.Fatal(err)
	}
	service := newTestService(ServiceOptions{Repository: repo, Clock: fixedClock{now: testTime}})
	origin, err := service.ResolvePersonalAssistantOrigin(ctx, ViewerRef{ViewerID: "user-1", OwnerID: "account-1", ViewerKind: ViewerKindUser, TenantID: "tenant-a"}, "conv-pa-mission")
	if err != nil || origin.OwnerID != "account-1" || origin.AuthorizationSubjectID != "user-1" {
		t.Fatalf("ResolvePersonalAssistantOrigin = %+v, err=%v", origin, err)
	}
}

func TestServiceRejectsPersonalAssistantOriginAcrossOwners(t *testing.T) {
	ctx := context.Background()
	repo := newMemoryRepository(t)
	if err := repo.SaveConversation(ctx, ConversationRecord{
		ConversationID: "conv-pa-account-b", ConversationKind: ConversationKindPersonalAssistant, TenantID: "tenant-a",
		CreatedBy: "user-1", OwnerID: "account-b", CreatedAt: testTime, UpdatedAt: testTime,
	}); err != nil {
		t.Fatal(err)
	}
	service := newTestService(ServiceOptions{Repository: repo, Clock: fixedClock{now: testTime}})
	_, err := service.ResolvePersonalAssistantOrigin(ctx, ViewerRef{
		ViewerID: "user-1", OwnerID: "account-a", ViewerKind: ViewerKindUser, TenantID: "tenant-a",
	}, "conv-pa-account-b")
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("ResolvePersonalAssistantOrigin error = %v, want ErrForbidden", err)
	}
}

func TestServiceEnsureMissionConversationBackfillsValidatedPersonalAssistantOrigin(t *testing.T) {
	ctx := context.Background()
	repo := newMemoryRepository(t)
	if err := repo.SaveConversation(ctx, ConversationRecord{
		ConversationID: "conv-existing-pa",
		TenantID:       "tenant-a",
		Title:          "Existing PA mission",
		Status:         "completed",
		CreatedBy:      "subject-1",
		OwnerID:        "account-1",
		CreatedAt:      testTime,
		UpdatedAt:      testTime,
	}); err != nil {
		t.Fatalf("save conversation: %v", err)
	}
	if err := repo.SaveMember(ctx, ConversationMemberRecord{ConversationID: "conv-existing-pa", MemberKind: "user", MemberID: "subject-1", DisplayName: "User", CreatedAt: testTime}); err != nil {
		t.Fatalf("save member: %v", err)
	}
	if err := repo.SaveMissionLink(ctx, LinkedMissionRecord{ConversationID: "conv-existing-pa", MissionID: "mission-existing-pa", Status: "active", CreatedAt: testTime}); err != nil {
		t.Fatalf("save link: %v", err)
	}
	service := newTestService(ServiceOptions{Repository: repo, Clock: fixedClock{now: testTime.Add(time.Minute)}})
	conversation, err := service.EnsureMissionConversation(ctx, ViewerRef{ViewerID: "subject-1", OwnerID: "account-1", ViewerKind: ViewerKindUser, TenantID: "tenant-a"}, EnsureMissionConversationCommand{
		MissionID:                        "mission-existing-pa",
		Title:                            "Existing PA mission",
		Status:                           "active",
		PersonalAssistantOriginOwnerID:   "account-1",
		PersonalAssistantOriginSubjectID: "subject-1",
	})
	if err != nil {
		t.Fatalf("backfill EnsureMissionConversation: %v", err)
	}
	if !conversation.PersonalAssistantRuntime {
		t.Fatalf("backfilled conversation did not expose PA runtime marker: %+v", conversation)
	}
	stored, err := repo.GetConversation(ctx, "conv-existing-pa")
	if err != nil {
		t.Fatalf("reload backfilled conversation: %v", err)
	}
	if stored.PersonalAssistantOriginOwnerID != "account-1" || stored.PersonalAssistantOriginSubjectID != "subject-1" {
		t.Fatalf("backfilled origin = owner %q subject %q", stored.PersonalAssistantOriginOwnerID, stored.PersonalAssistantOriginSubjectID)
	}
}

func TestServiceEnsureMissionConversationResolvesAgentViewerWithoutHumanProfile(t *testing.T) {
	repo := newMemoryRepository(t)
	service := newTestService(ServiceOptions{
		Repository: repo,
		Clock:      fixedClock{now: testTime},
		AgentDirectory: &testAgentDirectory{entries: []agentcore.AgentDirectoryEntry{{
			AgentProfileID: "lead-agent", AgentInstanceID: "lead-agent", DisplayName: "Lead", RealName: "Lead",
			AgentKind: agentcore.AgentKindAI, Enabled: true,
		}}},
		HumanProfiles:       &testHumanProfiles{err: errors.New("human profile lookup must not run for an agent")},
		InitialAgentMembers: []InitialAgentMemberSeed{},
	})

	conversation, err := service.EnsureMissionConversation(context.Background(), ViewerRef{ViewerID: "lead-agent", ViewerKind: ViewerKindAgent}, EnsureMissionConversationCommand{
		MissionID: "mission-agent", Title: "Agent mission", Status: "active", CreatedAt: testTime,
	})
	if err != nil {
		t.Fatalf("EnsureMissionConversation: %v", err)
	}
	if len(conversation.Members) != 1 || conversation.Members[0].Kind != "agent" || conversation.Members[0].ID != "lead-agent" {
		t.Fatalf("agent viewer members = %+v", conversation.Members)
	}
}

func TestServiceEnsureMissionConversationDoesNotReuseAnotherTenantMissionLink(t *testing.T) {
	ctx := context.Background()
	repo := newMemoryRepository(t)
	if err := repo.SaveConversation(ctx, ConversationRecord{ConversationID: "conv-tenant-a", TenantID: "tenant-a", Title: "Tenant A mission", Status: "completed", CreatedBy: "user-a", CreatedAt: testTime, UpdatedAt: testTime}); err != nil {
		t.Fatalf("save conversation: %v", err)
	}
	if err := repo.SaveMissionLink(ctx, LinkedMissionRecord{ConversationID: "conv-tenant-a", MissionID: "shared-mission-id", Title: "Tenant A mission", Status: "active", CreatedAt: testTime}); err != nil {
		t.Fatalf("save mission link: %v", err)
	}
	service := newTestService(ServiceOptions{Repository: repo, Clock: fixedClock{now: testTime.Add(time.Minute)}})

	conversation, err := service.EnsureMissionConversation(ctx, ViewerRef{ViewerID: "user-b", ViewerKind: ViewerKindUser, TenantID: "tenant-b"}, EnsureMissionConversationCommand{
		MissionID: "shared-mission-id", Title: "Tenant B mission", Status: "active", CreatedAt: testTime,
	})
	if err != nil {
		t.Fatalf("EnsureMissionConversation returned error: %v", err)
	}
	if conversation.ConversationID == "conv-tenant-a" || conversation.TenantID != "tenant-b" {
		t.Fatalf("cross-tenant conversation reused: %+v", conversation)
	}
}

func TestServiceEnsureMissionConversationReusesExistingMissionLink(t *testing.T) {
	ctx := context.Background()
	repo := newMemoryRepository(t)
	if err := repo.SaveConversation(ctx, ConversationRecord{ConversationID: "conv-existing", Title: "Existing discussion", Status: "completed", CreatedBy: "user-1", CreatedAt: testTime, UpdatedAt: testTime}); err != nil {
		t.Fatalf("save conversation: %v", err)
	}
	if err := repo.SaveMember(ctx, ConversationMemberRecord{ConversationID: "conv-existing", MemberKind: "user", MemberID: "user-1", DisplayName: "本地用户", Role: "话题发起人", CreatedAt: testTime}); err != nil {
		t.Fatalf("save member: %v", err)
	}
	if err := repo.SaveMissionLink(ctx, LinkedMissionRecord{ConversationID: "conv-existing", MissionID: "mission-1", Title: "Existing mission", Status: "active", CreatedAt: testTime}); err != nil {
		t.Fatalf("save mission link: %v", err)
	}
	service := newTestService(ServiceOptions{
		Repository: repo,
		Clock:      fixedClock{now: testTime.Add(time.Minute)},
	})

	conversation, err := service.EnsureMissionConversation(ctx, ViewerRef{ViewerID: "user-2", ViewerKind: ViewerKindUser, DisplayName: "Reviewer"}, EnsureMissionConversationCommand{
		MissionID: "mission-1",
		Title:     "Updated mission",
		Status:    "paused",
		CreatedAt: testTime,
	})
	if err != nil {
		t.Fatalf("EnsureMissionConversation returned error: %v", err)
	}
	if conversation.ConversationID != "conv-existing" {
		t.Fatalf("conversation id = %q, want existing link", conversation.ConversationID)
	}
	if len(conversation.LinkedMissions) != 1 || conversation.LinkedMissions[0].Title != "Updated mission" || conversation.LinkedMissions[0].Status != "paused" {
		t.Fatalf("mission link metadata was not refreshed: %+v", conversation.LinkedMissions)
	}
	foundViewer := false
	for _, member := range conversation.Members {
		if member.Kind == "user" && member.ID == "user-2" {
			foundViewer = true
			break
		}
	}
	if !foundViewer {
		t.Fatalf("ensure should add the requesting viewer to the existing mission discussion: %+v", conversation.Members)
	}
}

func TestServiceEnsureMissionConversationDoesNotReuseDirectChatMissionLink(t *testing.T) {
	ctx := context.Background()
	repo := newMemoryRepository(t)
	if err := repo.SaveConversation(ctx, ConversationRecord{ConversationID: "conv-direct", ConversationKind: "direct", Title: "Private mission prep", Status: "completed", CreatedBy: "user-1", CreatedAt: testTime, UpdatedAt: testTime}); err != nil {
		t.Fatalf("save direct conversation: %v", err)
	}
	if err := repo.SaveMissionLink(ctx, LinkedMissionRecord{ConversationID: "conv-direct", MissionID: "mission-private", Title: "Private mission", Status: "active", CreatedAt: testTime}); err != nil {
		t.Fatalf("save direct mission link: %v", err)
	}
	service := newTestService(ServiceOptions{
		Repository: repo,
		Clock:      fixedClock{now: testTime.Add(time.Minute)},
	})

	conversation, err := service.EnsureMissionConversation(ctx, ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser, DisplayName: "本地用户"}, EnsureMissionConversationCommand{
		MissionID: "mission-private",
		Title:     "Private mission",
		Status:    "active",
		CreatedAt: testTime,
	})
	if err != nil {
		t.Fatalf("EnsureMissionConversation returned error: %v", err)
	}
	if conversation.ConversationID == "conv-direct" || conversation.ConversationKind == "direct" {
		t.Fatalf("mission discussion should be a group conversation, got %+v", conversation)
	}
	if len(conversation.LinkedMissions) != 1 || conversation.LinkedMissions[0].MissionID != "mission-private" {
		t.Fatalf("new mission group link = %+v", conversation.LinkedMissions)
	}
}
