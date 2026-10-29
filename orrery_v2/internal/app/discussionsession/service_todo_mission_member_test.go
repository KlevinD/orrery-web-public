package discussionsession

import (
	"context"
	"errors"
	"testing"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/missioncontrol"
)

func TestEnsureTodoMissionAgentMemberValidatesCanonicalLinksAndPreservesHistory(t *testing.T) {
	ctx := context.Background()
	repo := newMemoryRepository(t)
	service := newTestService(ServiceOptions{
		Repository: repo,
		AgentDirectory: &testAgentDirectory{entries: append(defaultTestAgentDirectory().(*testAgentDirectory).entries,
			agentcore.AgentDirectoryEntry{AgentProfileID: "worker-agent", AgentInstanceID: "worker-agent", DisplayName: "Worker", AgentKind: agentcore.AgentKindAI, Enabled: true},
		)},
		Clock: fixedClock{now: testTime},
	})
	owner := ViewerRef{ViewerID: "todo-owner", ViewerKind: ViewerKindUser, DisplayName: "Owner", TenantID: "tenant-a"}
	conversation, err := service.EnsureTodoConversation(ctx, owner, EnsureTodoConversationCommand{
		TenantID: "tenant-a", TodoID: "todo-1", ManagedSource: "todo:tenant-a/todo-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.LinkMission(ctx, owner, conversation.ConversationID, LinkedMissionRecord{MissionID: "mission-1", CreatedAt: testTime}); err != nil {
		t.Fatal(err)
	}
	valid := missioncontrol.CanonicalConversationMembershipRequest{
		ActorKind: "system", ActorID: "mission-lifecycle", TenantID: "tenant-a", ConversationID: conversation.ConversationID,
		MissionID: "mission-1", TodoID: "todo-1", AgentID: "worker-agent", ManagedSource: "todo-mission:tenant-a/todo-1/mission-1",
	}
	if err := service.EnsureTodoMissionAgentMember(ctx, valid); err != nil {
		t.Fatal(err)
	}
	if err := service.EnsureTodoMissionAgentMember(ctx, valid); err != nil {
		t.Fatalf("idempotent membership: %v", err)
	}
	stored, err := repo.GetConversation(ctx, conversation.ConversationID)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, member := range stored.Members {
		if member.MemberKind == "agent" && member.MemberID == "worker-agent" {
			count++
			if member.ManagedSource != valid.ManagedSource {
				t.Fatalf("managed source = %q", member.ManagedSource)
			}
		}
	}
	if count != 1 {
		t.Fatalf("worker membership count = %d, members=%+v", count, stored.Members)
	}

	invalid := []missioncontrol.CanonicalConversationMembershipRequest{
		func() missioncontrol.CanonicalConversationMembershipRequest {
			request := valid
			request.ActorKind = "user"
			return request
		}(),
		func() missioncontrol.CanonicalConversationMembershipRequest {
			request := valid
			request.TenantID = "tenant-b"
			request.ManagedSource = "todo-mission:tenant-b/todo-1/mission-1"
			return request
		}(),
		func() missioncontrol.CanonicalConversationMembershipRequest {
			request := valid
			request.TodoID = "todo-other"
			request.ManagedSource = "todo-mission:tenant-a/todo-other/mission-1"
			return request
		}(),
		func() missioncontrol.CanonicalConversationMembershipRequest {
			request := valid
			request.MissionID = "mission-other"
			request.ManagedSource = "todo-mission:tenant-a/todo-1/mission-other"
			return request
		}(),
		func() missioncontrol.CanonicalConversationMembershipRequest {
			request := valid
			request.ManagedSource = "todo:tenant-a/todo-1"
			return request
		}(),
	}
	for _, request := range invalid {
		if err := service.EnsureTodoMissionAgentMember(ctx, request); !errors.Is(err, ErrForbidden) {
			t.Fatalf("invalid request %+v error = %v, want ErrForbidden", request, err)
		}
	}
}

func TestReconcileTodoMissionAgentMembersPreservesTodoGrantThenRevokesFormerParticipant(t *testing.T) {
	ctx := context.Background()
	repo := newMemoryRepository(t)
	service := newTestService(ServiceOptions{
		Repository: repo,
		AgentDirectory: &testAgentDirectory{entries: append(defaultTestAgentDirectory().(*testAgentDirectory).entries,
			agentcore.AgentDirectoryEntry{AgentProfileID: "worker-agent", AgentInstanceID: "worker-agent", DisplayName: "Worker", AgentKind: agentcore.AgentKindAI, Enabled: true},
		)},
		Clock: fixedClock{now: testTime},
	})
	owner := ViewerRef{ViewerID: "todo-owner", ViewerKind: ViewerKindUser, DisplayName: "Owner", TenantID: "tenant-a"}
	conversation, err := service.EnsureTodoConversation(ctx, owner, EnsureTodoConversationCommand{
		TenantID: "tenant-a", TodoID: "todo-1", InitialMemberIDs: []string{"worker-agent"}, ManagedSource: "todo:tenant-a/todo-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.LinkMission(ctx, owner, conversation.ConversationID, LinkedMissionRecord{MissionID: "mission-1", CreatedAt: testTime}); err != nil {
		t.Fatal(err)
	}
	source := "todo-mission:tenant-a/todo-1/mission-1"
	if err := service.ReconcileTodoMissionAgentMembers(ctx, missioncontrol.CanonicalConversationMembershipSetRequest{
		ActorKind: "system", ActorID: "mission-lifecycle", TenantID: "tenant-a", ConversationID: conversation.ConversationID,
		MissionID: "mission-1", TodoID: "todo-1", AgentIDs: []string{"worker-agent"}, ManagedSource: source,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ReconcileConversationMembers(ctx, ViewerRef{ViewerID: "todo-automation-agent", ViewerKind: ViewerKindSystem, TenantID: "tenant-a"}, ReconcileConversationMembersCommand{
		ConversationID: conversation.ConversationID, ManagedSource: "todo:tenant-a/todo-1",
	}); err != nil {
		t.Fatal(err)
	}
	stored, err := repo.GetConversation(ctx, conversation.ConversationID)
	if err != nil {
		t.Fatal(err)
	}
	worker, ok := findConversationMember(stored.Members, "agent", "worker-agent")
	if !ok || !conversationMemberHasManagedSource(worker, source) || conversationMemberHasManagedSource(worker, "todo:tenant-a/todo-1") {
		t.Fatalf("Todo reconciliation removed the active Mission grant: %+v", worker)
	}
	if err := service.ReconcileTodoMissionAgentMembers(ctx, missioncontrol.CanonicalConversationMembershipSetRequest{
		ActorKind: "system", ActorID: "mission-lifecycle", TenantID: "tenant-a", ConversationID: conversation.ConversationID,
		MissionID: "mission-1", TodoID: "todo-1", ManagedSource: source,
	}); err != nil {
		t.Fatal(err)
	}
	stored, err = repo.GetConversation(ctx, conversation.ConversationID)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := findConversationMember(stored.Members, "agent", "worker-agent"); ok {
		t.Fatalf("former Todo Mission participant retained access: %+v", stored.Members)
	}
}
