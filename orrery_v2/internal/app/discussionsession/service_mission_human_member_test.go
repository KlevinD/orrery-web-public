package discussionsession

import (
	"context"
	"errors"
	"testing"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/missioncontrol"
)

func TestMissionHumanMembershipLifecycle(t *testing.T) {
	ctx := context.Background()
	repo := newMemoryRepository(t)
	service := newTestService(ServiceOptions{Repository: repo, Clock: fixedClock{now: testTime},
		AgentDirectory: &testAgentDirectory{entries: append(defaultTestAgentDirectory().(*testAgentDirectory).entries,
			agentcore.AgentDirectoryEntry{AgentProfileID: "human-reviewer", AgentInstanceID: "human-reviewer", DisplayName: "Reviewer", AgentKind: agentcore.AgentKindHuman, Enabled: true},
		)},
	})
	conversation, err := service.EnsureMissionConversation(ctx, ViewerRef{ViewerID: "owner", ViewerKind: ViewerKindUser, TenantID: "tenant-a"}, EnsureMissionConversationCommand{MissionID: "mission-1", Title: "Mission"})
	if err != nil {
		t.Fatal(err)
	}
	request := missioncontrol.CanonicalConversationMembershipSetRequest{ActorKind: "system", ActorID: "mission-lifecycle", TenantID: "tenant-a", ConversationID: conversation.ConversationID, MissionID: "mission-1", HumanIDs: []string{"human-reviewer"}, ManagedSource: "mission-task-assignee:tenant-a/mission-1"}
	reviewer := ViewerRef{ViewerID: "human-reviewer", ViewerKind: ViewerKindUser, TenantID: "tenant-a"}
	if _, err := service.GetConversation(ctx, reviewer, conversation.ConversationID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("uninvited reader error=%v", err)
	}
	for i := 0; i < 2; i++ {
		if err := service.ReconcileMissionAgentMembers(ctx, request); err != nil {
			t.Fatal(err)
		}
	}
	view, err := repo.GetConversation(ctx, conversation.ConversationID)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, member := range view.Members {
		if member.MemberID == "human-reviewer" {
			count++
			if member.MemberKind != "user" || !conversationMemberHasManagedSource(member, request.ManagedSource) {
				t.Fatalf("member=%+v", member)
			}
		}
	}
	if count != 1 {
		t.Fatalf("reviewer count=%d", count)
	}
	if _, err := service.GetConversation(ctx, reviewer, conversation.ConversationID); err != nil {
		t.Fatalf("synced reviewer cannot open discussion: %v", err)
	}
	teamRequest := request
	teamRequest.ManagedSource = "mission-team:tenant-a/mission-1"
	if err := service.ReconcileMissionAgentMembers(ctx, teamRequest); err != nil {
		t.Fatal(err)
	}
	request.HumanIDs = nil
	if err := service.ReconcileMissionAgentMembers(ctx, request); err != nil {
		t.Fatal(err)
	}
	view, err = repo.GetConversation(ctx, conversation.ConversationID)
	if err != nil {
		t.Fatal(err)
	}
	retained := false
	for _, member := range view.Members {
		if member.MemberID == "human-reviewer" {
			retained = conversationMemberHasManagedSource(member, teamRequest.ManagedSource)
		}
	}
	if !retained {
		t.Fatal("removing task source removed team grant")
	}
	teamRequest.HumanIDs = nil
	if err := service.ReconcileMissionAgentMembers(ctx, teamRequest); err != nil {
		t.Fatal(err)
	}
	view, err = repo.GetConversation(ctx, conversation.ConversationID)
	if err != nil {
		t.Fatal(err)
	}
	owner := false
	for _, member := range view.Members {
		if member.MemberID == "human-reviewer" {
			t.Fatal("removed assignee retained managed access")
		}
		if member.MemberKind == "user" {
			owner = true
		}
	}
	if !owner {
		t.Fatal("creator lost explicit membership")
	}
	if err := repo.SaveMember(ctx, ConversationMemberRecord{ConversationID: conversation.ConversationID, MemberKind: "user", MemberID: "human-reviewer", DisplayName: "Reviewer", ExplicitGrantorID: "owner", ExplicitGrantorKind: "user", CreatedAt: testTime}); err != nil {
		t.Fatal(err)
	}
	request.HumanIDs = []string{"human-reviewer"}
	if err := service.ReconcileMissionAgentMembers(ctx, request); err != nil {
		t.Fatal(err)
	}
	request.HumanIDs = nil
	if err := service.ReconcileMissionAgentMembers(ctx, request); err != nil {
		t.Fatal(err)
	}
	view, err = repo.GetConversation(ctx, conversation.ConversationID)
	if err != nil {
		t.Fatal(err)
	}
	explicit := false
	for _, member := range view.Members {
		if member.MemberID == "human-reviewer" {
			explicit = member.ExplicitGrantorID == "owner" && member.ManagedSource == ""
		}
	}
	if !explicit {
		t.Fatal("explicit invitation was adopted or revoked")
	}
	request.TenantID = "other"
	if err := service.ReconcileMissionAgentMembers(ctx, request); !errors.Is(err, ErrForbidden) {
		t.Fatalf("cross-tenant error=%v", err)
	}
}

func TestDedicatedMissionHumanAccessRequiresBootstrapProvenance(t *testing.T) {
	view := ConversationRecordView{ConversationRecord: ConversationRecord{TenantID: "tenant-a"},
		LinkedMissions: []LinkedMissionRecord{{MissionID: "mission-1"}},
		Members:        []ConversationMemberRecord{grantConversationMemberManagedSource(ConversationMemberRecord{MemberKind: "agent", MemberID: "lead"}, missionConversationBootstrapManagedSource("tenant-a", "mission-1"))},
	}
	if !isDedicatedMissionConversation(view, "mission-1") {
		t.Fatal("dedicated group rejected")
	}
	for _, kind := range []string{"direct", ConversationKindPersonalAssistant} {
		copy := view
		copy.ConversationKind = kind
		if isDedicatedMissionConversation(copy, "mission-1") {
			t.Fatalf("private kind %s accepted", kind)
		}
	}
	copy := view
	copy.Members = nil
	if isDedicatedMissionConversation(copy, "mission-1") {
		t.Fatal("linked existing chat accepted without provenance")
	}
	copy = view
	copy.PurposeKind = "todo"
	if isDedicatedMissionConversation(copy, "mission-1") {
		t.Fatal("Todo chat accepted")
	}
	copy = view
	copy.LinkedMissions = append(append([]LinkedMissionRecord(nil), view.LinkedMissions...), LinkedMissionRecord{MissionID: "another"})
	if isDedicatedMissionConversation(copy, "mission-1") {
		t.Fatal("shared multi-mission chat accepted")
	}
}
