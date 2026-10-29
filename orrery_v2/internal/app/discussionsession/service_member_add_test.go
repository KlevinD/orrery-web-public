package discussionsession

import (
	"context"
	"errors"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
)

func TestServiceAddConversationMembersConvertsDirectConversationToGroup(t *testing.T) {
	repo := newMemoryRepository(t)
	service := newTestService(ServiceOptions{
		Repository: repo,
		AgentDirectory: &testAgentDirectory{entries: []agentcore.AgentDirectoryEntry{
			{AgentProfileID: "human-reviewer", DisplayName: "陆清岚", RealName: "陆清岚", RoleName: "Reviewer", AgentKind: agentcore.AgentKindHuman, Enabled: true},
			{AgentProfileID: "lead-agent", DisplayName: "顾明澈", RealName: "顾明澈", RoleName: "Lead", AgentKind: agentcore.AgentKindAI, Enabled: true},
		}},
		Clock:       fixedClock{now: testTime},
		IDGenerator: fixedIDGen("conv-human", "msg-user"),
	})
	viewer := ViewerRef{ViewerID: "local-user", ViewerKind: ViewerKindUser, DisplayName: "本地用户"}

	created, err := service.SendMessage(context.Background(), viewer, SendMessageCommand{
		Content:        "这个你也看一下",
		DirectTargetID: "human-reviewer",
		IdempotencyKey: "direct-human",
	})
	if err != nil {
		t.Fatal(err)
	}

	dto, err := service.AddConversationMembers(context.Background(), viewer, AddConversationMembersCommand{
		ConversationID: created.ConversationID,
		TargetIDs:      []string{"lead-agent"},
		IdempotencyKey: "add-lead",
	})
	if err != nil {
		t.Fatal(err)
	}

	if dto.ConversationKind != "" || dto.DirectTargetKind != "" || dto.DirectTargetID != "" || dto.DirectAgentID != "" {
		t.Fatalf("converted group metadata = kind %q targetKind %q targetID %q agent %q", dto.ConversationKind, dto.DirectTargetKind, dto.DirectTargetID, dto.DirectAgentID)
	}
	want := []ConversationMemberDTO{
		{Kind: "user", ID: "human-reviewer", DisplayName: "陆清岚", Role: "私聊对象"},
		{Kind: "user", ID: "local-user", DisplayName: "本地用户", Role: "话题发起人"},
		{Kind: "agent", ID: "lead-agent", DisplayName: "顾明澈", Role: "Lead"},
	}
	if got := dto.Members; !sameConversationMembers(got, want) {
		t.Fatalf("members = %+v, want %+v", got, want)
	}
	stored, err := repo.GetConversation(context.Background(), created.ConversationID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.ConversationKind != "" || stored.DirectTargetKind != "" || stored.DirectTargetID != "" || stored.DirectAgentID != "" {
		t.Fatalf("stored converted group metadata = %+v", stored.ConversationRecord)
	}
}

func TestServiceRejectsMemberAddWhenConversationDisablesMemberAdditions(t *testing.T) {
	repo := newMemoryRepository(t)
	allowMemberAdditions := false
	service := newTestService(ServiceOptions{
		Repository: repo,
		AgentDirectory: &testAgentDirectory{entries: []agentcore.AgentDirectoryEntry{
			{AgentProfileID: "lead-agent", DisplayName: "顾明澈", RealName: "顾明澈", RoleName: "Lead", AgentKind: agentcore.AgentKindAI, Enabled: true},
			{AgentProfileID: "coordinator-agent", DisplayName: "林知远", RealName: "林知远", RoleName: "Coordinator", AgentKind: agentcore.AgentKindAI, Enabled: true},
		}},
		Clock: fixedClock{now: testTime},
	})
	ctx := context.Background()
	if err := repo.SaveConversation(ctx, ConversationRecord{ConversationID: "conv-1", Title: "Locked", Status: "completed", CreatedBy: "user-1", MemberAdditionsAllowed: &allowMemberAdditions, CreatedAt: testTime, UpdatedAt: testTime}); err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveMember(ctx, ConversationMemberRecord{ConversationID: "conv-1", MemberKind: "user", MemberID: "user-1", DisplayName: "本地用户", Role: "话题发起人", CreatedAt: testTime}); err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveMember(ctx, ConversationMemberRecord{ConversationID: "conv-1", MemberKind: "agent", MemberID: "lead-agent", DisplayName: "顾明澈", Role: "Lead", CreatedAt: testTime.Add(time.Nanosecond)}); err != nil {
		t.Fatal(err)
	}

	_, err := service.AddConversationMembers(ctx, ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser}, AddConversationMembersCommand{
		ConversationID: "conv-1",
		TargetIDs:      []string{"coordinator-agent"},
		IdempotencyKey: "add-coordinator",
	})
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("AddConversationMembers error = %v, want ErrForbidden", err)
	}
	_, err = service.addDiscussionMembersFromToolCall(ctx, "conv-1", ToolCallDTO{
		ToolCallID: "tool-add",
		Name:       "discussion_member_add",
		Arguments:  map[string]string{"agent_ids": "coordinator-agent"},
	}, testTime)
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("tool member add error = %v, want ErrForbidden", err)
	}
	updated, getErr := repo.GetConversation(ctx, "conv-1")
	if getErr != nil {
		t.Fatal(getErr)
	}
	if conversationMemberName(updated.Members, "agent", "coordinator-agent") != "" {
		t.Fatalf("coordinator should not be added: %+v", updated.Members)
	}
}

func TestServiceExplicitMemberGrantRecordsAuthorizerAndSurvivesTodoReconciliation(t *testing.T) {
	ctx := context.Background()
	repo := newMemoryRepository(t)
	service := newTestService(ServiceOptions{
		Repository: repo,
		AgentDirectory: &testAgentDirectory{entries: []agentcore.AgentDirectoryEntry{
			{AgentProfileID: "reviewer-old", DisplayName: "Old Reviewer", AgentKind: agentcore.AgentKindHuman, Enabled: true},
			{AgentProfileID: "reviewer-new", DisplayName: "New Reviewer", AgentKind: agentcore.AgentKindHuman, Enabled: true},
		}},
		Clock: fixedClock{now: testTime},
	})
	owner := ViewerRef{ViewerID: "todo-owner", ViewerKind: ViewerKindUser, TenantID: "tenant-a"}
	conversation, err := service.EnsureTodoConversation(ctx, owner, EnsureTodoConversationCommand{
		TenantID: "tenant-a", TodoID: "todo-1", InitialMemberIDs: []string{"reviewer-old"}, ManagedSource: "todo:tenant-a/todo-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.AddConversationMembers(ctx, owner, AddConversationMembersCommand{
		ConversationID: conversation.ConversationID, TargetIDs: []string{"reviewer-old"}, IdempotencyKey: "retain-old",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ReconcileConversationMembers(ctx, ViewerRef{ViewerID: "todo-automation-agent", ViewerKind: ViewerKindSystem, TenantID: "tenant-a"}, ReconcileConversationMembersCommand{
		ConversationID: conversation.ConversationID, TargetIDs: []string{"reviewer-new"}, ManagedSource: "todo:tenant-a/todo-1",
	}); err != nil {
		t.Fatal(err)
	}
	stored, err := repo.GetConversation(ctx, conversation.ConversationID)
	if err != nil {
		t.Fatal(err)
	}
	var retained *ConversationMemberRecord
	for index := range stored.Members {
		if stored.Members[index].MemberID == "reviewer-old" {
			retained = &stored.Members[index]
			break
		}
	}
	if retained == nil {
		t.Fatalf("explicitly retained member was removed: %+v", stored.Members)
	}
	if retained.ManagedSource != "" || retained.ExplicitGrantorID != owner.ViewerID || retained.ExplicitGrantorKind != "user" || !retained.ExplicitGrantedAt.Equal(testTime) {
		t.Fatalf("explicit grant attribution = %+v", *retained)
	}
}

func TestServiceMemberGrantRequiresSameTenantConversationMembership(t *testing.T) {
	ctx := context.Background()
	repo := newMemoryRepository(t)
	service := newTestService(ServiceOptions{
		Repository:     repo,
		AgentDirectory: &testAgentDirectory{entries: []agentcore.AgentDirectoryEntry{{AgentProfileID: "target", AgentKind: agentcore.AgentKindAI, Enabled: true}}},
		Clock:          fixedClock{now: testTime},
	})
	if err := repo.SaveConversation(ctx, ConversationRecord{ConversationID: "todo-conversation", TenantID: "tenant-a", PurposeKind: "todo", PurposeID: "todo-1", CreatedAt: testTime, UpdatedAt: testTime}); err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveMember(ctx, ConversationMemberRecord{ConversationID: "todo-conversation", MemberKind: "user", MemberID: "member", CreatedAt: testTime}); err != nil {
		t.Fatal(err)
	}
	_, err := service.AddConversationMembers(ctx, ViewerRef{ViewerID: "member", ViewerKind: ViewerKindUser, TenantID: "tenant-b"}, AddConversationMembersCommand{
		ConversationID: "todo-conversation", TargetIDs: []string{"target"}, IdempotencyKey: "cross-tenant",
	})
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("cross-tenant member grant error = %v, want ErrForbidden", err)
	}
}

func TestServiceDirectAgentToolMemberAddConvertsConversationToGroup(t *testing.T) {
	repo := newMemoryRepository(t)
	direct := &capturingDiscussionAgentMentionResponder{response: DiscussionAgentResponse{
		AssistantMessage: "已把顾明澈拉进来。",
		Status:           "completed",
		RuntimeKind:      "managed",
		ToolCalls: []ToolCallDTO{{
			ToolCallID: "add-lead",
			Name:       "discussion_member_add",
			Status:     "completed",
			Arguments: map[string]string{
				"agent_ids": "lead-agent",
			},
		}},
	}}
	service := newTestService(ServiceOptions{
		Repository:       repo,
		MentionResponder: direct,
		AgentDirectory: &testAgentDirectory{entries: []agentcore.AgentDirectoryEntry{
			{AgentProfileID: "staffing-agent", DisplayName: "秦若兰", RealName: "秦若兰", RoleName: "Staffing", AgentKind: agentcore.AgentKindAI, ToolRefs: []agentcore.ToolRef{{ToolID: "discussion_member_add"}}, Enabled: true},
			{AgentProfileID: "lead-agent", DisplayName: "顾明澈", RealName: "顾明澈", RoleName: "Lead", AgentKind: agentcore.AgentKindAI, Enabled: true},
		}},
		Clock:       fixedClock{now: testTime},
		IDGenerator: fixedIDGen("conv-direct", "msg-user", "run-direct", "msg-direct"),
	})

	dto, err := service.SendMessage(context.Background(), ViewerRef{ViewerID: "local-user", ViewerKind: ViewerKindUser, DisplayName: "本地用户"}, SendMessageCommand{
		Content:        "把顾明澈拉进来",
		DirectTargetID: "staffing-agent",
		IdempotencyKey: "direct-staffing",
	})
	if err != nil {
		t.Fatal(err)
	}

	if dto.ConversationKind != "" || dto.DirectTargetKind != "" || dto.DirectTargetID != "" || dto.DirectAgentID != "" {
		t.Fatalf("converted direct agent conversation metadata = kind %q targetKind %q targetID %q agent %q", dto.ConversationKind, dto.DirectTargetKind, dto.DirectTargetID, dto.DirectAgentID)
	}
	want := []ConversationMemberDTO{
		{Kind: "agent", ID: "staffing-agent", DisplayName: "秦若兰", Role: "私聊对象"},
		{Kind: "user", ID: "local-user", DisplayName: "本地用户", Role: "话题发起人"},
		{Kind: "agent", ID: "lead-agent", DisplayName: "顾明澈", Role: "Lead"},
	}
	if got := dto.Members; !sameConversationMembers(got, want) {
		t.Fatalf("members = %+v, want %+v", got, want)
	}
	stored, err := repo.GetConversation(context.Background(), dto.ConversationID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.ConversationKind != "" || stored.DirectTargetKind != "" || stored.DirectTargetID != "" || stored.DirectAgentID != "" {
		t.Fatalf("stored converted metadata = %+v", stored.ConversationRecord)
	}
}
