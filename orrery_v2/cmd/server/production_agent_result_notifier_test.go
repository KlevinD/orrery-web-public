package main

import (
	"context"
	"errors"
	"testing"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/discussionsession"
	missioncore "orrery_v2/internal/mission/core"
	missiondiscussion "orrery_v2/internal/mission/discussion"
	"orrery_v2/internal/platform/testkit"
	"orrery_v2/internal/platform/uow"
)

func TestProductionAgentResultNotifierPersistsAndReplaysCanonicalTodoNotice(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	repo := discussionsession.NewRepository(env.DBRouter)
	now := env.Clock.Now().UTC()
	conversationID := "todo-discussion-1"
	allowMembers := true
	if err := repo.SaveConversation(ctx, discussionsession.ConversationRecord{
		ConversationID: conversationID, TenantID: "tenant-1", PurposeKind: "todo", PurposeID: "todo-1",
		MemberAdditionsAllowed: &allowMembers, Title: "Todo 1", Status: "active", CreatedBy: "owner-1", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("SaveConversation: %v", err)
	}
	for _, member := range []discussionsession.ConversationMemberRecord{
		{ConversationID: conversationID, MemberKind: "user", MemberID: "owner-1", DisplayName: "Owner", CreatedAt: now},
		{ConversationID: conversationID, MemberKind: "agent", MemberID: "agent-1", DisplayName: "Agent 1", CreatedAt: now},
	} {
		if err := repo.SaveMember(ctx, member); err != nil {
			t.Fatalf("SaveMember: %v", err)
		}
	}
	service := discussionsession.NewService(discussionsession.ServiceOptions{
		Repository: repo, Clock: env.Clock, IDGenerator: env.IDGenerator,
	})
	notifier := &productionAgentResultNotifier{}
	notifier.SetCanonical(service)
	input, result := productionNotifierInputAndResult()
	input.ContextPack.ConversationID = conversationID

	if err := notifier.Notify(ctx, input, result); err != nil {
		t.Fatalf("Notify: %v", err)
	}
	if err := notifier.Notify(ctx, input, result); err != nil {
		t.Fatalf("Notify replay: %v", err)
	}
	view, err := repo.GetConversation(ctx, conversationID)
	if err != nil {
		t.Fatalf("GetConversation: %v", err)
	}
	if len(view.Messages) != 1 {
		t.Fatalf("persisted notices = %d, want 1", len(view.Messages))
	}
	message := view.Messages[0]
	if message.Content != result.Summary.Text || message.MissionID != result.MissionID.String() || message.MissionTaskID != result.TaskID.String() || message.Metadata["run_id"] != result.RunID.String() {
		t.Fatalf("canonical notice = %+v", message)
	}
}

func TestProductionAgentResultNotifierPersistsAndReplaysMissionDiscussionNotice(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	repo := missiondiscussion.NewPlatformRepository()
	service := missiondiscussion.NewService(missiondiscussion.ServiceOptions{
		UnitOfWork: env.UnitOfWork, Repository: repo, Clock: env.Clock, IDGenerator: env.IDGenerator, Logger: env.Logger,
	})
	group, err := service.CreateGroupForTenant(ctx, "tenant-1", "mission-1", "Mission 1")
	if err != nil {
		t.Fatalf("CreateGroupForTenant: %v", err)
	}
	if err := service.AddMember(ctx, group.ID, missiondiscussion.DiscussionMember{
		AgentProfileID: "agent-1", AgentInstanceID: "agent-1", MissionAssignmentRef: "assignment-1",
		MemberKind: missiondiscussion.DiscussionMemberAgent, VisibilityScope: missiondiscussion.VisibilityMission,
		CanPost: true, CanMention: true,
	}); err != nil {
		t.Fatalf("AddMember: %v", err)
	}
	notifier := &productionAgentResultNotifier{mission: service}
	input, result := productionNotifierInputAndResult()
	input.ContextPack.DiscussionGroupID = agentcore.DiscussionGroupID(group.ID)

	if err := notifier.Notify(ctx, input, result); err != nil {
		t.Fatalf("Notify: %v", err)
	}
	if err := notifier.Notify(ctx, input, result); err != nil {
		t.Fatalf("Notify replay: %v", err)
	}
	var messages []missiondiscussion.Message
	if err := env.UnitOfWork.Do(ctx, func(ctx context.Context, tx uow.Tx) error {
		var listErr error
		messages, listErr = repo.ListMessages(ctx, tx, group.ID, 0)
		return listErr
	}); err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	if len(messages) != 1 {
		t.Fatalf("persisted Mission notices = %d, want 1", len(messages))
	}
	message := messages[0]
	if message.Content != result.Summary.Text || message.RelatedTaskID != result.TaskID || message.IdempotencyKey != "agent-result-notice:"+result.RunID.String() {
		t.Fatalf("Mission notice = %+v", message)
	}

	changed := result
	changed.Summary.Text = "Different replay content"
	if err := notifier.Notify(ctx, input, changed); !errors.Is(err, missiondiscussion.ErrConflict) {
		t.Fatalf("changed replay error = %v, want ErrConflict", err)
	}
}

func productionNotifierInputAndResult() (agentcore.AgentInput, agentcore.AgentResult) {
	input := agentcore.AgentInput{
		RunID: "run-1", TenantID: "tenant-1",
		Task:        agentcore.TaskInput{ID: "task-1", MissionID: "mission-1"},
		ContextPack: agentcore.ContextPack{AgentID: "agent-1", MissionID: missioncore.MissionID("mission-1"), TaskID: missioncore.TaskID("task-1")},
	}
	result := agentcore.AgentResult{
		ID: "result-run-1", MissionID: "mission-1", TaskID: "task-1", AgentID: "agent-1", RunID: "run-1",
		Summary: agentcore.Summary{Text: "Agent completed the approved step."}, InternalTraceRef: "artifact://trace-1",
	}
	return input, result
}
