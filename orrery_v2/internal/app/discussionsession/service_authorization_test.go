package discussionsession

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
)

type recordingConversationAuthorizationRelations struct {
	written  []ConversationAuthorizationSnapshot
	deleted  []ConversationAuthorizationSnapshot
	revoked  []ConversationAuthorizationSubject
	writeErr error
}

func (r *recordingConversationAuthorizationRelations) WriteConversationRelations(_ context.Context, snapshot ConversationAuthorizationSnapshot) error {
	r.written = append(r.written, snapshot)
	return r.writeErr
}

func (r *recordingConversationAuthorizationRelations) DeleteConversationRelations(_ context.Context, snapshot ConversationAuthorizationSnapshot) error {
	r.deleted = append(r.deleted, snapshot)
	return nil
}

func (r *recordingConversationAuthorizationRelations) DeleteConversationMemberRelation(_ context.Context, _ string, _ string, member ConversationAuthorizationSubject) error {
	r.revoked = append(r.revoked, member)
	return nil
}

func TestServiceProjectsNewDirectConversationAuthorization(t *testing.T) {
	relations := &recordingConversationAuthorizationRelations{}
	service := newTestService(ServiceOptions{
		Repository:             newMemoryRepository(t),
		AuthorizationRelations: relations,
		DiscussionAgent:        &capturingDiscussionAgentRunner{agentID: "lead-agent", response: DiscussionAgentResponse{AssistantMessage: "lead should not run"}},
		MentionResponder: &capturingDiscussionAgentMentionResponder{response: DiscussionAgentResponse{
			AssistantMessage: "我来处理。",
			Status:           "completed",
			RuntimeKind:      "managed",
		}},
		Clock:       fixedClock{now: testTime},
		IDGenerator: fixedIDGen("conv-direct", "msg-user", "run-direct", "msg-direct"),
	})

	if _, err := service.SendMessage(context.Background(), ViewerRef{
		ViewerID: "local-user", ViewerKind: ViewerKindUser, DisplayName: "本地用户",
	}, SendMessageCommand{
		Content:        "test",
		DirectAgentID:  "staffing-agent",
		IdempotencyKey: "direct-authorization",
	}); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}

	snapshot, ok := latestConversationAuthorizationSnapshot(relations.written, "conv-direct")
	if !ok {
		t.Fatalf("authorization writes = %+v", relations.written)
	}
	if snapshot.Owner != (ConversationAuthorizationSubject{Kind: "user", ID: "local-user"}) {
		t.Fatalf("owner = %+v", snapshot.Owner)
	}
	if snapshot.MissionID != "" {
		t.Fatalf("direct Mission ID = %q", snapshot.MissionID)
	}
	if !conversationAuthorizationSnapshotHasMember(snapshot, "user", "local-user") ||
		!conversationAuthorizationSnapshotHasMember(snapshot, "agent", "staffing-agent") {
		t.Fatalf("members = %+v", snapshot.Members)
	}
}

func TestServiceProjectsAddedConversationMemberAuthorization(t *testing.T) {
	repo := newMemoryRepository(t)
	relations := &recordingConversationAuthorizationRelations{}
	service := newTestService(ServiceOptions{
		Repository:             repo,
		AuthorizationRelations: relations,
		AgentDirectory: &testAgentDirectory{entries: []agentcore.AgentDirectoryEntry{
			{AgentProfileID: "lead-agent", DisplayName: "顾明澈", RealName: "顾明澈", RoleName: "Lead", AgentKind: agentcore.AgentKindAI, Enabled: true},
		}},
		Clock: fixedClock{now: testTime},
	})
	ctx := context.Background()
	if err := repo.SaveConversation(ctx, ConversationRecord{
		ConversationID: "conv-group", Title: "Group", Status: "completed", CreatedBy: "local-user", CreatedAt: testTime, UpdatedAt: testTime,
	}); err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveMember(ctx, ConversationMemberRecord{
		ConversationID: "conv-group", MemberKind: "user", MemberID: "local-user", DisplayName: "本地用户", Role: "话题发起人", CreatedAt: testTime,
	}); err != nil {
		t.Fatal(err)
	}

	if _, err := service.AddConversationMembers(ctx, ViewerRef{
		ViewerID: "local-user", ViewerKind: ViewerKindUser,
	}, AddConversationMembersCommand{
		ConversationID: "conv-group",
		TargetIDs:      []string{"lead-agent"},
		IdempotencyKey: "add-lead",
	}); err != nil {
		t.Fatalf("AddConversationMembers: %v", err)
	}

	snapshot, ok := latestConversationAuthorizationSnapshot(relations.written, "conv-group")
	if !ok || !conversationAuthorizationSnapshotHasMember(snapshot, "agent", "lead-agent") {
		t.Fatalf("authorization writes = %+v", relations.written)
	}
}

func TestServiceProjectsPrewarmedConversationAuthorization(t *testing.T) {
	relations := &recordingConversationAuthorizationRelations{}
	service := newTestService(ServiceOptions{
		Repository:             newMemoryRepository(t),
		AuthorizationRelations: relations,
		DiscussionAgent:        &capturingDiscussionAgentRunner{agentID: "lead-agent"},
		Clock:                  fixedClock{now: testTime},
		IDGenerator:            fixedIDGen("conv-prewarm"),
	})

	if _, err := service.PrewarmConversation(context.Background(), ViewerRef{
		ViewerID: "local-user", ViewerKind: ViewerKindUser, DisplayName: "本地用户",
	}, PrewarmConversationCommand{Title: "Draft", IdempotencyKey: "prewarm-authorization"}); err != nil {
		t.Fatalf("PrewarmConversation: %v", err)
	}
	if _, ok := latestConversationAuthorizationSnapshot(relations.written, "conv-prewarm"); !ok {
		t.Fatalf("authorization writes = %+v", relations.written)
	}
}

func TestServiceProjectsMissionConversationAuthorization(t *testing.T) {
	relations := &recordingConversationAuthorizationRelations{}
	service := newTestService(ServiceOptions{
		Repository:             newMemoryRepository(t),
		AuthorizationRelations: relations,
		Clock:                  fixedClock{now: testTime},
	})

	created, err := service.EnsureMissionConversation(context.Background(), ViewerRef{
		ViewerID: "local-user", ViewerKind: ViewerKindUser, DisplayName: "本地用户",
	}, EnsureMissionConversationCommand{MissionID: "mission-1", Title: "Mission"})
	if err != nil {
		t.Fatalf("EnsureMissionConversation: %v", err)
	}
	snapshot, ok := latestConversationAuthorizationSnapshot(relations.written, created.ConversationID)
	if !ok || snapshot.MissionID != "mission-1" {
		t.Fatalf("authorization writes = %+v", relations.written)
	}
}

func TestServiceProjectsTodoConversationAuthorization(t *testing.T) {
	relations := &recordingConversationAuthorizationRelations{}
	service := newTestService(ServiceOptions{
		Repository:             newMemoryRepository(t),
		AuthorizationRelations: relations,
		Clock:                  fixedClock{now: testTime},
	})
	viewer := ViewerRef{ViewerID: "todo-owner", ViewerKind: ViewerKindUser, TenantID: "tenant-a"}

	created, err := service.EnsureTodoConversation(context.Background(), viewer, EnsureTodoConversationCommand{
		TenantID: "tenant-a", TodoID: "todo-1", ManagedSource: "todo:tenant-a/todo-1",
	})
	if err != nil {
		t.Fatalf("EnsureTodoConversation: %v", err)
	}
	if _, ok := latestConversationAuthorizationSnapshot(relations.written, created.ConversationID); !ok {
		t.Fatalf("authorization writes = %+v", relations.written)
	}
}

func TestServiceProjectsAttachmentDraftConversationAuthorization(t *testing.T) {
	relations := &recordingConversationAuthorizationRelations{}
	service := newTestService(ServiceOptions{
		Repository:             newMemoryRepository(t),
		AuthorizationRelations: relations,
		AttachmentStore:        NewLocalAttachmentStore(t.TempDir()),
		Clock:                  fixedClock{now: testTime},
		IDGenerator:            fixedIDGen("conv-attachment", "attachment-1"),
	})

	draft, err := service.StartAttachmentDraft(context.Background(), ViewerRef{
		ViewerID: "local-user", ViewerKind: ViewerKindUser, DisplayName: "本地用户",
	}, UploadAttachmentCommand{
		FileName: "brief.txt", ContentType: "text/plain", Reader: strings.NewReader("brief"),
	})
	if err != nil {
		t.Fatalf("StartAttachmentDraft: %v", err)
	}
	if _, ok := latestConversationAuthorizationSnapshot(relations.written, draft.Conversation.ConversationID); !ok {
		t.Fatalf("authorization writes = %+v", relations.written)
	}
}

func TestServiceDeletesConversationAuthorization(t *testing.T) {
	repo := newMemoryRepository(t)
	relations := &recordingConversationAuthorizationRelations{}
	service := newTestService(ServiceOptions{Repository: repo, AuthorizationRelations: relations, Clock: fixedClock{now: testTime}})
	ctx := context.Background()
	if err := repo.SaveConversation(ctx, ConversationRecord{
		ConversationID: "conv-delete", Title: "Delete", Status: "completed", CreatedBy: "local-user", CreatedAt: testTime, UpdatedAt: testTime,
	}); err != nil {
		t.Fatal(err)
	}
	for _, member := range []ConversationMemberRecord{
		{ConversationID: "conv-delete", MemberKind: "user", MemberID: "local-user", DisplayName: "本地用户", CreatedAt: testTime},
		{ConversationID: "conv-delete", MemberKind: "agent", MemberID: "staffing-agent", DisplayName: "秦若兰", CreatedAt: testTime.Add(time.Nanosecond)},
	} {
		if err := repo.SaveMember(ctx, member); err != nil {
			t.Fatal(err)
		}
	}

	if err := service.DeleteConversation(ctx, ViewerRef{ViewerID: "local-user", ViewerKind: ViewerKindUser}, "conv-delete"); err != nil {
		t.Fatalf("DeleteConversation: %v", err)
	}
	if len(relations.deleted) != 1 {
		t.Fatalf("authorization deletes = %+v", relations.deleted)
	}
	snapshot := relations.deleted[0]
	if snapshot.Owner != (ConversationAuthorizationSubject{Kind: "user", ID: "local-user"}) ||
		!conversationAuthorizationSnapshotHasMember(snapshot, "agent", "staffing-agent") {
		t.Fatalf("deleted snapshot = %+v", snapshot)
	}
}

func TestServiceRevokesManagedConversationAuthorization(t *testing.T) {
	repo := newMemoryRepository(t)
	relations := &recordingConversationAuthorizationRelations{}
	service := newTestService(ServiceOptions{
		Repository:             repo,
		AuthorizationRelations: relations,
		AgentDirectory: &testAgentDirectory{entries: []agentcore.AgentDirectoryEntry{
			{AgentProfileID: "reviewer-old", DisplayName: "Old Reviewer", AgentKind: agentcore.AgentKindHuman, Enabled: true},
		}},
		Clock: fixedClock{now: testTime},
	})
	ctx := context.Background()
	managedSource := "todo:tenant-a/todo-1"
	if err := repo.SaveConversation(ctx, ConversationRecord{
		ConversationID: "todo-conversation", TenantID: "tenant-a", PurposeKind: "todo", PurposeID: "todo-1",
		Title: "Todo", Status: "completed", CreatedBy: "todo-owner", CreatedAt: testTime, UpdatedAt: testTime,
	}); err != nil {
		t.Fatal(err)
	}
	for _, member := range []ConversationMemberRecord{
		{ConversationID: "todo-conversation", MemberKind: "user", MemberID: "todo-owner", DisplayName: "Owner", CreatedAt: testTime},
		{ConversationID: "todo-conversation", MemberKind: "user", MemberID: "reviewer-old", DisplayName: "Old Reviewer", ManagedSource: managedSource, CreatedAt: testTime.Add(time.Nanosecond)},
	} {
		if err := repo.SaveMember(ctx, member); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := service.ReconcileConversationMembers(ctx, ViewerRef{
		ViewerID: "todo-owner", ViewerKind: ViewerKindUser, TenantID: "tenant-a",
	}, ReconcileConversationMembersCommand{
		ConversationID: "todo-conversation",
		ManagedSource:  managedSource,
	}); err != nil {
		t.Fatalf("ReconcileConversationMembers: %v", err)
	}
	if len(relations.revoked) != 1 || relations.revoked[0] != (ConversationAuthorizationSubject{Kind: "user", ID: "reviewer-old"}) {
		t.Fatalf("revoked relations = %+v", relations.revoked)
	}
}

func TestServiceConversationAuthorizationFailureDoesNotFailCommittedMessage(t *testing.T) {
	repo := newMemoryRepository(t)
	relations := &recordingConversationAuthorizationRelations{writeErr: errors.New("relation store unavailable")}
	service := newTestService(ServiceOptions{
		Repository:             repo,
		AuthorizationRelations: relations,
		DiscussionAgent: &capturingDiscussionAgentRunner{response: DiscussionAgentResponse{
			AssistantMessage: "已提交。", Status: "completed", RuntimeKind: "managed",
		}},
		Clock:       fixedClock{now: testTime},
		IDGenerator: fixedIDGen("conv-committed", "msg-user", "run-1", "msg-assistant"),
	})

	dto, err := service.SendMessage(context.Background(), ViewerRef{
		ViewerID: "local-user", ViewerKind: ViewerKindUser, DisplayName: "本地用户",
	}, SendMessageCommand{Content: "提交", IdempotencyKey: "commit-before-projection"})
	if err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	if dto.ConversationID != "conv-committed" || len(dto.Messages) != 2 {
		t.Fatalf("committed discussion = %+v", dto)
	}
	if len(relations.written) == 0 {
		t.Fatal("authorization projection was not attempted")
	}
}

func latestConversationAuthorizationSnapshot(snapshots []ConversationAuthorizationSnapshot, conversationID string) (ConversationAuthorizationSnapshot, bool) {
	for index := len(snapshots) - 1; index >= 0; index-- {
		if snapshots[index].ConversationID == conversationID {
			return snapshots[index], true
		}
	}
	return ConversationAuthorizationSnapshot{}, false
}

func conversationAuthorizationSnapshotHasMember(snapshot ConversationAuthorizationSnapshot, kind, id string) bool {
	for _, member := range snapshot.Members {
		if member.Kind == kind && member.ID == id {
			return true
		}
	}
	return false
}
