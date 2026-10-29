package discussionsession

import (
	"context"
	"errors"
	"testing"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/missioncontrol"
)

func TestEnsureMissionAgentMemberValidatesCanonicalMissionAndIsIdempotent(t *testing.T) {
	ctx := context.Background()
	repo := newMemoryRepository(t)
	service := newTestService(ServiceOptions{
		Repository: repo,
		AgentDirectory: &testAgentDirectory{entries: append(defaultTestAgentDirectory().(*testAgentDirectory).entries,
			agentcore.AgentDirectoryEntry{AgentProfileID: "worker-agent", AgentInstanceID: "worker-agent", DisplayName: "Worker", AgentKind: agentcore.AgentKindAI, Enabled: true},
		)},
		Clock: fixedClock{now: testTime},
	})
	owner := ViewerRef{ViewerID: "mission-owner", ViewerKind: ViewerKindUser, DisplayName: "Owner", TenantID: "tenant-a"}
	conversation, err := service.EnsureMissionConversation(ctx, owner, EnsureMissionConversationCommand{MissionID: "mission-1", Title: "Mission"})
	if err != nil {
		t.Fatal(err)
	}
	valid := missioncontrol.CanonicalConversationMembershipRequest{
		ActorKind: "system", ActorID: "mission-lifecycle", TenantID: "tenant-a", ConversationID: conversation.ConversationID,
		MissionID: "mission-1", AgentID: "worker-agent", ManagedSource: "mission-task-assignee:tenant-a/mission-1",
	}
	if err := service.EnsureMissionAgentMember(ctx, valid); err != nil {
		t.Fatal(err)
	}
	if err := service.EnsureMissionAgentMember(ctx, valid); err != nil {
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
			if member.ManagedSource != valid.ManagedSource || member.ExplicitGrantorID != "" {
				t.Fatalf("managed member = %+v", member)
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
			request.ManagedSource = "mission-task-assignee:tenant-b/mission-1"
			return request
		}(),
		func() missioncontrol.CanonicalConversationMembershipRequest {
			request := valid
			request.MissionID = "mission-other"
			request.ManagedSource = "mission-task-assignee:tenant-a/mission-other"
			return request
		}(),
		func() missioncontrol.CanonicalConversationMembershipRequest {
			request := valid
			request.ManagedSource = "mission:tenant-a/mission-1"
			return request
		}(),
		func() missioncontrol.CanonicalConversationMembershipRequest {
			request := valid
			request.TodoID = "todo-1"
			return request
		}(),
	}
	for _, request := range invalid {
		err := service.EnsureMissionAgentMember(ctx, request)
		if request.TodoID != "" {
			if !errors.Is(err, ErrInvalidRequest) {
				t.Fatalf("Todo request error = %v, want ErrInvalidRequest", err)
			}
			continue
		}
		if !errors.Is(err, ErrForbidden) {
			t.Fatalf("invalid request %+v error = %v, want ErrForbidden", request, err)
		}
	}
}

func TestEnsureMissionAgentMemberAcceptsControlledMissionTeamSource(t *testing.T) {
	ctx := context.Background()
	repo := newMemoryRepository(t)
	service := newTestService(ServiceOptions{
		Repository: repo,
		AgentDirectory: &testAgentDirectory{entries: append(defaultTestAgentDirectory().(*testAgentDirectory).entries,
			agentcore.AgentDirectoryEntry{AgentProfileID: "lead-agent", AgentInstanceID: "lead-agent", DisplayName: "Lead", AgentKind: agentcore.AgentKindAI, Enabled: true},
		)},
		Clock: fixedClock{now: testTime},
	})
	conversation, err := service.EnsureMissionConversation(ctx, ViewerRef{ViewerID: "owner", ViewerKind: ViewerKindUser, DisplayName: "Owner", TenantID: "tenant-a"}, EnsureMissionConversationCommand{MissionID: "mission-1"})
	if err != nil {
		t.Fatal(err)
	}
	request := missioncontrol.CanonicalConversationMembershipRequest{
		ActorKind: "system", ActorID: "mission-api", TenantID: "tenant-a", ConversationID: conversation.ConversationID,
		MissionID: "mission-1", AgentID: "lead-agent", ManagedSource: "mission-team:tenant-a/mission-1",
	}
	if err := service.EnsureMissionAgentMember(ctx, request); err != nil {
		t.Fatal(err)
	}
	stored, err := repo.GetConversation(ctx, conversation.ConversationID)
	if err != nil {
		t.Fatal(err)
	}
	for _, member := range stored.Members {
		if member.MemberKind == "agent" && member.MemberID == "lead-agent" {
			if member.ManagedSource != request.ManagedSource {
				t.Fatalf("managed source = %q", member.ManagedSource)
			}
			return
		}
	}
	t.Fatalf("lead agent was not added: %+v", stored.Members)
}

func TestReconcileMissionAgentMembersRevokesOnlyItsManagedGrant(t *testing.T) {
	ctx := context.Background()
	repo := newMemoryRepository(t)
	service := newTestService(ServiceOptions{
		Repository: repo,
		AgentDirectory: &testAgentDirectory{entries: append(defaultTestAgentDirectory().(*testAgentDirectory).entries,
			agentcore.AgentDirectoryEntry{AgentProfileID: "worker-agent", AgentInstanceID: "worker-agent", DisplayName: "Worker", AgentKind: agentcore.AgentKindAI, Enabled: true},
		)},
		Clock: fixedClock{now: testTime},
	})
	conversation, err := service.EnsureMissionConversation(ctx, ViewerRef{ViewerID: "owner", ViewerKind: ViewerKindUser, DisplayName: "Owner", TenantID: "tenant-a"}, EnsureMissionConversationCommand{MissionID: "mission-1"})
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range []string{"mission-task-assignee:tenant-a/mission-1", "mission-team:tenant-a/mission-1"} {
		if err := service.EnsureMissionAgentMember(ctx, missioncontrol.CanonicalConversationMembershipRequest{
			ActorKind: "system", ActorID: "mission-lifecycle", TenantID: "tenant-a", ConversationID: conversation.ConversationID,
			MissionID: "mission-1", AgentID: "worker-agent", ManagedSource: source,
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := service.ReconcileMissionAgentMembers(ctx, missioncontrol.CanonicalConversationMembershipSetRequest{
		ActorKind: "system", ActorID: "mission-lifecycle", TenantID: "tenant-a", ConversationID: conversation.ConversationID,
		MissionID: "mission-1", ManagedSource: "mission-task-assignee:tenant-a/mission-1",
	}); err != nil {
		t.Fatal(err)
	}
	stored, err := repo.GetConversation(ctx, conversation.ConversationID)
	if err != nil {
		t.Fatal(err)
	}
	worker, ok := findConversationMember(stored.Members, "agent", "worker-agent")
	if !ok || !conversationMemberHasManagedSource(worker, "mission-team:tenant-a/mission-1") || conversationMemberHasManagedSource(worker, "mission-task-assignee:tenant-a/mission-1") {
		t.Fatalf("task grant reconciliation removed the independent team grant: %+v", worker)
	}
	if err := service.ReconcileMissionAgentMembers(ctx, missioncontrol.CanonicalConversationMembershipSetRequest{
		ActorKind: "system", ActorID: "mission-api", TenantID: "tenant-a", ConversationID: conversation.ConversationID,
		MissionID: "mission-1", ManagedSource: "mission-team:tenant-a/mission-1",
	}); err != nil {
		t.Fatal(err)
	}
	stored, err = repo.GetConversation(ctx, conversation.ConversationID)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := findConversationMember(stored.Members, "agent", "worker-agent"); ok {
		t.Fatalf("worker retained access after all managed grants were revoked: %+v", stored.Members)
	}
}

func findConversationMember(members []ConversationMemberRecord, kind, id string) (ConversationMemberRecord, bool) {
	for _, member := range members {
		if member.MemberKind == kind && member.MemberID == id {
			return member, true
		}
	}
	return ConversationMemberRecord{}, false
}

func TestEnsureMissionAgentMemberRejectsTodoAndDirectConversations(t *testing.T) {
	ctx := context.Background()
	repo := newMemoryRepository(t)
	service := newTestService(ServiceOptions{
		Repository: repo,
		AgentDirectory: &testAgentDirectory{entries: append(defaultTestAgentDirectory().(*testAgentDirectory).entries,
			agentcore.AgentDirectoryEntry{AgentProfileID: "worker-agent", AgentInstanceID: "worker-agent", DisplayName: "Worker", AgentKind: agentcore.AgentKindAI, Enabled: true},
		)},
		Clock: fixedClock{now: testTime},
	})
	for _, conversation := range []ConversationRecord{
		{ConversationID: "todo-conversation", TenantID: "tenant-a", PurposeKind: "todo", PurposeID: "todo-1", CreatedAt: testTime, UpdatedAt: testTime},
		{ConversationID: "direct-conversation", TenantID: "tenant-a", ConversationKind: "direct", CreatedAt: testTime, UpdatedAt: testTime},
	} {
		if err := repo.SaveConversation(ctx, conversation); err != nil {
			t.Fatal(err)
		}
		if err := repo.SaveMissionLink(ctx, LinkedMissionRecord{ConversationID: conversation.ConversationID, MissionID: "mission-1", CreatedAt: testTime}); err != nil {
			t.Fatal(err)
		}
		request := missioncontrol.CanonicalConversationMembershipRequest{
			ActorKind: "system", ActorID: "mission-lifecycle", TenantID: "tenant-a", ConversationID: conversation.ConversationID,
			MissionID: "mission-1", AgentID: "worker-agent", ManagedSource: "mission-task-assignee:tenant-a/mission-1",
		}
		if err := service.EnsureMissionAgentMember(ctx, request); !errors.Is(err, ErrForbidden) {
			t.Fatalf("conversation %+v error = %v, want ErrForbidden", conversation, err)
		}
	}
}
