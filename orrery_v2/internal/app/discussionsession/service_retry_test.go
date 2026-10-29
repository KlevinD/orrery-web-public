package discussionsession

import (
	"context"
	"errors"
	"testing"
	"time"

	missioncontrol "orrery_v2/internal/app/missioncontrol"
)

func TestServiceListsAndGetsPersistedConversationsForRefresh(t *testing.T) {
	repo := newMemoryRepository(t)
	if err := repo.SaveConversation(context.Background(), ConversationRecord{ConversationID: "conv-1", Title: "Launch plan", Status: "completed", CreatedBy: "user-1", CreatedAt: testTime, UpdatedAt: testTime.Add(time.Minute)}); err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveMessage(context.Background(), MessageRecord{MessageID: "msg-1", ConversationID: "conv-1", Role: "user", AuthorID: "user-1", Content: "Plan a launch", CreatedAt: testTime}); err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveMissionLink(context.Background(), LinkedMissionRecord{ConversationID: "conv-1", MissionID: "mission-1", Title: "Launch plan", Status: "active", CreatedAt: testTime}); err != nil {
		t.Fatal(err)
	}
	service := newTestService(ServiceOptions{Repository: repo})

	page, err := service.ListConversations(context.Background(), ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser}, ListConversationsQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Conversations) != 1 || page.Conversations[0].ConversationID != "conv-1" || page.Conversations[0].LastMessage != "Plan a launch" {
		t.Fatalf("page = %+v", page)
	}
	if page.Conversations[0].LinkedMissions != 1 {
		t.Fatalf("linked mission count = %d", page.Conversations[0].LinkedMissions)
	}

	detail, err := service.GetConversation(context.Background(), ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser}, "conv-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(detail.Messages) != 1 || len(detail.LinkedMissions) != 1 {
		t.Fatalf("detail = %+v", detail)
	}
}

func TestServiceMarksCompletedConversationsViewedWhenOpened(t *testing.T) {
	repo := newMemoryRepository(t)
	ctx := context.Background()
	viewer := ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser}
	if err := repo.SaveConversation(ctx, ConversationRecord{ConversationID: "conv-1", Title: "Launch plan", Status: "completed", CreatedBy: "user-1", CreatedAt: testTime, UpdatedAt: testTime.Add(time.Minute)}); err != nil {
		t.Fatal(err)
	}
	service := newTestService(ServiceOptions{Repository: repo, Clock: fixedClock{now: testTime.Add(2 * time.Minute)}})

	page, err := service.ListConversations(ctx, viewer, ListConversationsQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Conversations) != 1 || !page.Conversations[0].UnreadCompleted {
		t.Fatalf("page before viewing = %+v, want unread completed conversation", page)
	}

	detail, err := service.GetConversation(ctx, viewer, "conv-1")
	if err != nil {
		t.Fatal(err)
	}
	if detail.UnreadCompleted {
		t.Fatalf("detail after viewing = %+v, want read completed conversation", detail)
	}
	if detail.ViewedAt.IsZero() {
		t.Fatalf("detail viewed_at is zero: %+v", detail)
	}

	page, err = service.ListConversations(ctx, viewer, ListConversationsQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Conversations) != 1 || page.Conversations[0].UnreadCompleted {
		t.Fatalf("page after viewing = %+v, want no unread completed conversation", page)
	}
}

func TestServiceRetryOwnMessageAutoApprovesPrunesLaterRecordsDeletesMissionsAndReruns(t *testing.T) {
	repo := newMemoryRepository(t)
	ctx := context.Background()
	if err := repo.SaveConversation(ctx, ConversationRecord{ConversationID: "conv-1", Title: "Snake game", Status: "completed", CreatedBy: "user-1", CreatedAt: testTime, UpdatedAt: testTime.Add(4 * time.Minute)}); err != nil {
		t.Fatal(err)
	}
	seedDiscussionMembers(t, repo, "conv-1", "user-1")
	records := []MessageRecord{
		{MessageID: "msg-1", ConversationID: "conv-1", Role: "user", AuthorID: "user-1", Content: "帮我写个小游戏", CreatedAt: testTime},
		{MessageID: "msg-2", ConversationID: "conv-1", Role: "assistant", AuthorID: "lead-agent", Content: "要什么技术栈？", CreatedAt: testTime.Add(time.Minute)},
		{MessageID: "msg-3", ConversationID: "conv-1", Role: "user", AuthorID: "user-1", Content: "html，最快", CreatedAt: testTime.Add(2 * time.Minute)},
		{MessageID: "msg-4", ConversationID: "conv-1", Role: "assistant", AuthorID: "lead-agent", Content: "我创建 Mission。", CreatedAt: testTime.Add(3 * time.Minute)},
	}
	for _, record := range records {
		if err := repo.SaveMessage(ctx, record); err != nil {
			t.Fatal(err)
		}
	}
	if err := repo.SaveRun(ctx, RunRecord{RunID: "run-before", ConversationID: "conv-1", AgentID: "lead-agent", Status: "completed", CreatedAt: testTime.Add(time.Minute), StartedAt: testTime.Add(time.Minute), CompletedAt: testTime.Add(time.Minute)}); err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveRun(ctx, RunRecord{RunID: "run-after", ConversationID: "conv-1", AgentID: "lead-agent", Status: "completed", ToolCalls: []ToolCallDTO{{ToolCallID: "tool-mission", Name: "mission_brief_create", Status: "completed", Result: map[string]string{"mission_id": "mission-old", "operation": "created"}}}, CreatedAt: testTime.Add(3 * time.Minute), StartedAt: testTime.Add(3 * time.Minute), CompletedAt: testTime.Add(3 * time.Minute)}); err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveMissionLink(ctx, LinkedMissionRecord{ConversationID: "conv-1", MissionID: "mission-old", Title: "Snake game", Status: "active", CreatedAt: testTime.Add(3 * time.Minute)}); err != nil {
		t.Fatal(err)
	}
	deleter := &capturingExecutionDeleter{}
	cleaner := &capturingConversationArtifactCleaner{}
	runtimeCleaner := &capturingDiscussionRuntimeRecordCleaner{}
	runner := &capturingDiscussionAgentRunner{response: DiscussionAgentResponse{AssistantMessage: "重新开始。", Status: "completed", RuntimeKind: "managed"}}
	service := newTestService(ServiceOptions{
		Repository:           repo,
		DiscussionAgent:      runner,
		ExecutionDeleter:     deleter,
		ArtifactCleaner:      cleaner,
		RuntimeRecordCleaner: runtimeCleaner,
		Clock:                fixedClock{now: testTime.Add(5 * time.Minute)},
		IDGenerator:          fixedIDGen("event-retry", "run-retry", "msg-assistant-retry"),
	})

	dto, err := service.RetryMessage(ctx, ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser}, RetryMessageCommand{
		ConversationID: "conv-1",
		MessageID:      "msg-3",
		IdempotencyKey: "retry-msg-3",
	})
	if err != nil {
		t.Fatal(err)
	}

	if len(deleter.deletedMissionIDs) != 1 || deleter.deletedMissionIDs[0] != "mission-old" {
		t.Fatalf("deleted missions = %+v", deleter.deletedMissionIDs)
	}
	if !runner.called || runner.request.Content != "html，最快" || runner.request.MessageID != "msg-3" || runner.request.RunID != "run-retry" {
		t.Fatalf("discussion agent request = %+v", runner.request)
	}
	if !runner.request.ForceNewSession {
		t.Fatalf("retry must start a fresh runtime session: %+v", runner.request)
	}
	if len(runner.request.History) != 2 || runner.request.History[0].MessageID != "msg-1" || runner.request.History[1].MessageID != "msg-2" {
		t.Fatalf("retry history = %+v", runner.request.History)
	}
	if len(dto.Messages) != 4 || dto.Messages[0].MessageID != "msg-1" || dto.Messages[1].MessageID != "msg-2" || dto.Messages[2].MessageID != "msg-3" || dto.Messages[3].MessageID != "msg-assistant-retry" {
		t.Fatalf("messages after retry = %+v", dto.Messages)
	}
	if len(dto.Runs) != 2 || dto.Runs[0].RunID != "run-before" || dto.Runs[1].RunID != "run-retry" {
		t.Fatalf("runs after retry = %+v", dto.Runs)
	}
	if len(cleaner.deletedRuns) != 1 || cleaner.deletedRuns[0] != "conv-1|run-after" {
		t.Fatalf("deleted run artifacts = %+v", cleaner.deletedRuns)
	}
	if len(runtimeCleaner.deletedRuns) != 1 || runtimeCleaner.deletedRuns[0] != "run-after" {
		t.Fatalf("deleted runtime records = %+v", runtimeCleaner.deletedRuns)
	}
	if len(dto.LinkedMissions) != 0 {
		t.Fatalf("linked missions after retry = %+v", dto.LinkedMissions)
	}
}

func TestServiceRetryOtherUserMessageRequiresCreatorApproval(t *testing.T) {
	repo := newMemoryRepository(t)
	ctx := context.Background()
	if err := repo.SaveConversation(ctx, ConversationRecord{ConversationID: "conv-1", Title: "Launch plan", Status: "completed", CreatedBy: "owner-1", CreatedAt: testTime, UpdatedAt: testTime}); err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveMessage(ctx, MessageRecord{MessageID: "msg-user-2", ConversationID: "conv-1", Role: "user", AuthorID: "user-2", Content: "换成 html", CreatedAt: testTime.Add(time.Minute)}); err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveMember(ctx, ConversationMemberRecord{ConversationID: "conv-1", MemberID: "user-2", MemberKind: "user", DisplayName: "User 2", CreatedAt: testTime}); err != nil {
		t.Fatal(err)
	}
	runner := &capturingDiscussionAgentRunner{response: DiscussionAgentResponse{AssistantMessage: "should not run"}}
	service := newTestService(ServiceOptions{
		Repository:      repo,
		DiscussionAgent: runner,
		Clock:           fixedClock{now: testTime.Add(2 * time.Minute)},
		IDGenerator:     fixedIDGen("retry-pending", "event-pending"),
	})

	dto, err := service.RetryMessage(ctx, ViewerRef{ViewerID: "user-2", ViewerKind: ViewerKindUser}, RetryMessageCommand{
		ConversationID: "conv-1",
		MessageID:      "msg-user-2",
		IdempotencyKey: "retry-msg-user-2",
	})
	if err != nil {
		t.Fatal(err)
	}

	if runner.called {
		t.Fatalf("discussion agent should not run before creator approval")
	}
	if len(dto.Events) != 1 || dto.Events[0].Kind != "message_retry_requested" || dto.Events[0].Status != "pending" {
		t.Fatalf("events = %+v", dto.Events)
	}
}

func TestDiscussionAgentViewerDoesNotDefaultBlankAgentIDToLead(t *testing.T) {
	viewer := discussionAgentViewer("")

	if viewer.ViewerID != "" || viewer.ViewerKind != missioncontrol.ViewerKindAgent {
		t.Fatalf("viewer = %+v", viewer)
	}
}

func TestServiceRetryOwnMessageRequiresConfiguredRunAgent(t *testing.T) {
	repo := newMemoryRepository(t)
	ctx := context.Background()
	if err := repo.SaveConversation(ctx, ConversationRecord{ConversationID: "conv-1", Title: "Snake game", Status: "completed", CreatedBy: "user-1", CreatedAt: testTime, UpdatedAt: testTime.Add(time.Minute)}); err != nil {
		t.Fatal(err)
	}
	seedDiscussionMembers(t, repo, "conv-1", "user-1")
	if err := repo.SaveMessage(ctx, MessageRecord{MessageID: "msg-1", ConversationID: "conv-1", Role: "user", AuthorID: "user-1", Content: "做个小游戏", CreatedAt: testTime}); err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveMessage(ctx, MessageRecord{MessageID: "msg-after", ConversationID: "conv-1", Role: "assistant", AuthorID: "lead-agent", Content: "旧回复", CreatedAt: testTime.Add(time.Minute)}); err != nil {
		t.Fatal(err)
	}
	runner := &anonymousDiscussionAgentRunner{response: DiscussionAgentResponse{AssistantMessage: "should not run"}}
	service := newTestService(ServiceOptions{
		Repository:          repo,
		DiscussionAgent:     runner,
		InitialAgentMembers: []InitialAgentMemberSeed{},
		Clock:               fixedClock{now: testTime.Add(2 * time.Minute)},
		IDGenerator:         fixedIDGen("event-retry", "run-retry"),
	})

	_, err := service.RetryMessage(ctx, ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser}, RetryMessageCommand{
		ConversationID: "conv-1",
		MessageID:      "msg-1",
		IdempotencyKey: "retry-missing-run-agent",
	})
	if !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("RetryMessage error = %v, want ErrInvalidRequest", err)
	}
	if runner.called {
		t.Fatal("runner should not execute without a configured run agent")
	}
	view, getErr := repo.GetConversation(ctx, "conv-1")
	if getErr != nil {
		t.Fatal(getErr)
	}
	if _, ok := findMessageRecord(view.Messages, "msg-after"); !ok {
		t.Fatalf("retry should not prune messages without configured run agent: %+v", view.Messages)
	}
	for _, run := range view.Runs {
		if run.RunID == "run-retry" {
			t.Fatalf("retry run should not be saved without configured run agent: %+v", view.Runs)
		}
	}
}

func TestServiceRetryCreatorApprovalPrunesAndRerunsOriginalMessage(t *testing.T) {
	repo := newMemoryRepository(t)
	ctx := context.Background()
	if err := repo.SaveConversation(ctx, ConversationRecord{ConversationID: "conv-1", Title: "Launch plan", Status: "retry_pending", CreatedBy: "owner-1", CreatedAt: testTime, UpdatedAt: testTime.Add(4 * time.Minute)}); err != nil {
		t.Fatal(err)
	}
	seedDiscussionMembers(t, repo, "conv-1", "owner-1", "user-2")
	if err := repo.SaveMessage(ctx, MessageRecord{MessageID: "msg-owner", ConversationID: "conv-1", Role: "user", AuthorID: "owner-1", Content: "先做个网页工具", CreatedAt: testTime}); err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveMessage(ctx, MessageRecord{MessageID: "msg-user-2", ConversationID: "conv-1", Role: "user", AuthorID: "user-2", Content: "改成 HTML 最快", CreatedAt: testTime.Add(time.Minute)}); err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveMessage(ctx, MessageRecord{MessageID: "msg-after", ConversationID: "conv-1", Role: "assistant", AuthorID: "lead-agent", Content: "旧 mission 已创建", CreatedAt: testTime.Add(2 * time.Minute)}); err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveRun(ctx, RunRecord{RunID: "run-after", ConversationID: "conv-1", AgentID: "lead-agent", Status: "completed", CreatedAt: testTime.Add(2 * time.Minute), StartedAt: testTime.Add(2 * time.Minute), CompletedAt: testTime.Add(2 * time.Minute), ToolCalls: []ToolCallDTO{{
		ToolCallID: "tool-mission",
		Name:       "mission_brief_create",
		Status:     "completed",
		Result:     map[string]string{"mission_id": "mission-old", "operation": "created"},
	}}}); err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveMissionLink(ctx, LinkedMissionRecord{ConversationID: "conv-1", MissionID: "mission-old", Title: "Old mission", Status: "active", CreatedAt: testTime.Add(2 * time.Minute)}); err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveEvent(ctx, EventRecord{EventID: "retry-pending", ConversationID: "conv-1", Kind: "message_retry_requested", Status: "pending", Metadata: map[string]string{"message_id": "msg-user-2", "requested_by": "user-2"}, CreatedAt: testTime.Add(3 * time.Minute)}); err != nil {
		t.Fatal(err)
	}
	deleter := &capturingExecutionDeleter{}
	runner := &capturingDiscussionAgentRunner{response: DiscussionAgentResponse{AssistantMessage: "审批后重新开始。", Status: "completed", RuntimeKind: "managed"}}
	service := newTestService(ServiceOptions{
		Repository:       repo,
		DiscussionAgent:  runner,
		ExecutionDeleter: deleter,
		Clock:            fixedClock{now: testTime.Add(5 * time.Minute)},
		IDGenerator:      fixedIDGen("run-retry", "msg-assistant-retry"),
	})

	dto, err := service.RetryMessage(ctx, ViewerRef{ViewerID: "owner-1", ViewerKind: ViewerKindUser}, RetryMessageCommand{
		ConversationID: "conv-1",
		MessageID:      "msg-user-2",
		IdempotencyKey: "approve-retry-msg-user-2",
		Approve:        true,
	})
	if err != nil {
		t.Fatal(err)
	}

	if len(deleter.deletedMissionIDs) != 1 || deleter.deletedMissionIDs[0] != "mission-old" {
		t.Fatalf("deleted missions = %+v", deleter.deletedMissionIDs)
	}
	if !runner.called || runner.request.Content != "改成 HTML 最快" || runner.request.MessageID != "msg-user-2" {
		t.Fatalf("discussion agent request = %+v", runner.request)
	}
	if len(dto.Messages) != 3 || dto.Messages[0].MessageID != "msg-owner" || dto.Messages[1].MessageID != "msg-user-2" || dto.Messages[2].MessageID != "msg-assistant-retry" {
		t.Fatalf("messages after approved retry = %+v", dto.Messages)
	}
	if len(dto.Runs) != 1 || dto.Runs[0].RunID != "run-retry" {
		t.Fatalf("runs after approved retry = %+v", dto.Runs)
	}
	if len(dto.Events) != 1 || dto.Events[0].EventID != "retry-pending" || dto.Events[0].Status != "approved" || dto.Events[0].Metadata["approved_by"] != "owner-1" {
		t.Fatalf("events after approved retry = %+v", dto.Events)
	}
	if len(dto.LinkedMissions) != 0 {
		t.Fatalf("linked missions after approved retry = %+v", dto.LinkedMissions)
	}
}

func TestServiceRetryDeletesTasksCreatedByPrunedRunsInExistingMission(t *testing.T) {
	repo := newMemoryRepository(t)
	ctx := context.Background()
	if err := repo.SaveConversation(ctx, ConversationRecord{ConversationID: "conv-1", Title: "Existing mission", Status: "completed", CreatedBy: "user-1", CreatedAt: testTime, UpdatedAt: testTime.Add(4 * time.Minute)}); err != nil {
		t.Fatal(err)
	}
	seedDiscussionMembers(t, repo, "conv-1", "user-1")
	if err := repo.SaveMissionLink(ctx, LinkedMissionRecord{ConversationID: "conv-1", MissionID: "mission-existing", Title: "Existing mission", Status: "active", CreatedAt: testTime}); err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveMessage(ctx, MessageRecord{MessageID: "msg-1", ConversationID: "conv-1", Role: "user", AuthorID: "user-1", Content: "做个网页", CreatedAt: testTime.Add(time.Minute)}); err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveRun(ctx, RunRecord{RunID: "run-after", ConversationID: "conv-1", AgentID: "lead-agent", Status: "completed", CreatedAt: testTime.Add(time.Minute), StartedAt: testTime.Add(time.Minute), CompletedAt: testTime.Add(2 * time.Minute), ToolCalls: []ToolCallDTO{{
		ToolCallID: "tool-task",
		Name:       "mission_task_create_root",
		Status:     "completed",
		Arguments:  map[string]string{"mission_id": "mission-existing"},
		Result:     map[string]string{"mission_id": "mission-existing", "task_id": "task-created"},
	}}}); err != nil {
		t.Fatal(err)
	}
	deleter := &capturingExecutionDeleter{}
	runner := &capturingDiscussionAgentRunner{response: DiscussionAgentResponse{AssistantMessage: "重新处理", Status: "completed", RuntimeKind: "managed"}}
	service := newTestService(ServiceOptions{
		Repository:       repo,
		DiscussionAgent:  runner,
		ExecutionDeleter: deleter,
		Clock:            fixedClock{now: testTime.Add(5 * time.Minute)},
		IDGenerator:      fixedIDGen("event-retry", "run-retry", "msg-retry"),
	})

	_, err := service.RetryMessage(ctx, ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser}, RetryMessageCommand{
		ConversationID: "conv-1",
		MessageID:      "msg-1",
		IdempotencyKey: "retry-msg-1",
	})
	if err != nil {
		t.Fatal(err)
	}

	if len(deleter.deletedMissionIDs) != 0 {
		t.Fatalf("existing mission should not be deleted: %+v", deleter.deletedMissionIDs)
	}
	if len(deleter.deletedTasks) != 1 || deleter.deletedTasks[0] != "mission-existing|task-created" {
		t.Fatalf("deleted tasks = %+v", deleter.deletedTasks)
	}
}

func TestServiceRetryKeepsRecordsWhenExecutionDeleterMissingForCreatedMission(t *testing.T) {
	repo := newMemoryRepository(t)
	ctx := context.Background()
	if err := repo.SaveConversation(ctx, ConversationRecord{ConversationID: "conv-1", Title: "Mission retry", Status: "completed", CreatedBy: "user-1", CreatedAt: testTime, UpdatedAt: testTime.Add(3 * time.Minute)}); err != nil {
		t.Fatal(err)
	}
	seedDiscussionMembers(t, repo, "conv-1", "user-1")
	if err := repo.SaveMessage(ctx, MessageRecord{MessageID: "msg-1", ConversationID: "conv-1", Role: "user", AuthorID: "user-1", Content: "第一版", CreatedAt: testTime}); err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveMessage(ctx, MessageRecord{MessageID: "msg-2", ConversationID: "conv-1", Role: "assistant", AuthorID: "lead-agent", Content: "创建了 mission", CreatedAt: testTime.Add(time.Minute)}); err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveRun(ctx, RunRecord{RunID: "run-created", ConversationID: "conv-1", AgentID: "lead-agent", Status: "completed", CreatedAt: testTime.Add(time.Minute), StartedAt: testTime.Add(time.Minute), CompletedAt: testTime.Add(2 * time.Minute), ToolCalls: []ToolCallDTO{{
		ToolCallID: "tool-mission",
		Name:       "mission_brief_create",
		Status:     "completed",
		Result:     map[string]string{"mission_id": "mission-created", "operation": "created"},
	}}}); err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveMissionLink(ctx, LinkedMissionRecord{ConversationID: "conv-1", MissionID: "mission-created", Title: "Created mission", Status: "active", CreatedAt: testTime.Add(time.Minute)}); err != nil {
		t.Fatal(err)
	}
	service := newTestService(ServiceOptions{
		Repository:      repo,
		DiscussionAgent: &capturingDiscussionAgentRunner{response: DiscussionAgentResponse{AssistantMessage: "should not run"}},
		Clock:           fixedClock{now: testTime.Add(4 * time.Minute)},
		IDGenerator:     fixedIDGen("event-retry", "run-retry"),
	})

	err := service.rollbackAfterMessage(ctx, ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser}, mustConversationView(t, repo, "conv-1"), mustMessageRecord(t, repo, "conv-1", "msg-1"))

	if !errors.Is(err, ErrServiceDependency) {
		t.Fatalf("rollbackAfterMessage err = %v, want ErrServiceDependency", err)
	}
	view := mustConversationView(t, repo, "conv-1")
	if len(view.Messages) != 2 || len(view.Runs) != 1 || len(view.LinkedMissions) != 1 {
		t.Fatalf("records should remain when execution deleter is missing: messages=%+v runs=%+v links=%+v", view.Messages, view.Runs, view.LinkedMissions)
	}
}

func TestServiceRetryKeepsExistingMissionWhenPrunedRunUpdatedIt(t *testing.T) {
	repo := newMemoryRepository(t)
	ctx := context.Background()
	if err := repo.SaveConversation(ctx, ConversationRecord{ConversationID: "conv-1", Title: "Existing mission retry", Status: "completed", CreatedBy: "user-1", CreatedAt: testTime, UpdatedAt: testTime.Add(3 * time.Minute)}); err != nil {
		t.Fatal(err)
	}
	seedDiscussionMembers(t, repo, "conv-1", "user-1")
	if err := repo.SaveMessage(ctx, MessageRecord{MessageID: "msg-1", ConversationID: "conv-1", Role: "user", AuthorID: "user-1", Content: "改一下", CreatedAt: testTime}); err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveRun(ctx, RunRecord{RunID: "run-update", ConversationID: "conv-1", AgentID: "lead-agent", Status: "completed", CreatedAt: testTime.Add(time.Minute), StartedAt: testTime.Add(time.Minute), CompletedAt: testTime.Add(2 * time.Minute), ToolCalls: []ToolCallDTO{{
		ToolCallID: "tool-mission-update",
		Name:       "mission_brief_create",
		Status:     "completed",
		Result:     map[string]string{"mission_id": "mission-existing", "operation": "updated"},
	}}}); err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveMissionLink(ctx, LinkedMissionRecord{ConversationID: "conv-1", MissionID: "mission-existing", Title: "Existing mission", Status: "active", CreatedAt: testTime.Add(time.Minute)}); err != nil {
		t.Fatal(err)
	}
	deleter := &capturingExecutionDeleter{}
	service := newTestService(ServiceOptions{Repository: repo, ExecutionDeleter: deleter})

	if err := service.rollbackAfterMessage(ctx, ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser}, mustConversationView(t, repo, "conv-1"), mustMessageRecord(t, repo, "conv-1", "msg-1")); err != nil {
		t.Fatal(err)
	}

	if len(deleter.deletedMissionIDs) != 0 {
		t.Fatalf("updated existing mission should not be deleted: %+v", deleter.deletedMissionIDs)
	}
}

func TestServiceRetryInvalidatesInFlightStreamRunSoLateCompletionCannotRestorePrunedRecords(t *testing.T) {
	repo := newMemoryRepository(t)
	ctx := context.Background()
	if err := repo.SaveConversation(ctx, ConversationRecord{ConversationID: "conv-1", Title: "Snake game", Status: "running", CreatedBy: "user-1", CreatedAt: testTime, UpdatedAt: testTime}); err != nil {
		t.Fatal(err)
	}
	seedDiscussionMembers(t, repo, "conv-1", "user-1")
	runner := &delayedStreamingDiscussionAgentRunner{
		streamEvents: make(chan DiscussionAgentStreamEvent, 1),
		response:     DiscussionAgentResponse{AssistantMessage: "新的回复", Status: "completed", RuntimeKind: "managed"},
	}
	service := newTestService(ServiceOptions{
		Repository:      repo,
		DiscussionAgent: runner,
		Clock:           fixedClock{now: testTime},
		IDGenerator:     fixedIDGen("msg-user", "run-old", "event-retry", "run-retry", "msg-retry"),
	})
	events, err := service.SendMessageStream(ctx, ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser}, SendMessageCommand{
		ConversationID: "conv-1",
		Content:        "就 html 最快实现",
		IdempotencyKey: "send-old",
	})
	if err != nil {
		t.Fatal(err)
	}

	dto, err := service.RetryMessage(ctx, ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser}, RetryMessageCommand{
		ConversationID: "conv-1",
		MessageID:      "msg-user",
		IdempotencyKey: "retry-msg-user",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(dto.Runs) != 1 || dto.Runs[0].RunID != "run-retry" {
		t.Fatalf("runs after retry = %+v", dto.Runs)
	}

	runner.streamEvents <- DiscussionAgentStreamEvent{Kind: DiscussionAgentStreamRunCompleted, Response: DiscussionAgentResponse{AssistantMessage: "旧回复不应复活", Status: "completed", RuntimeKind: "managed"}}
	close(runner.streamEvents)
	drainLeadStream(events)

	after, err := service.GetConversation(ctx, ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser}, "conv-1")
	if err != nil {
		t.Fatal(err)
	}
	for _, message := range after.Messages {
		if message.Content == "旧回复不应复活" {
			t.Fatalf("old stream completion restored pruned message: %+v", after.Messages)
		}
	}
	if len(after.Runs) != 1 || after.Runs[0].RunID != "run-retry" {
		t.Fatalf("runs after late completion = %+v", after.Runs)
	}
}

func TestServiceRetryCancelsInFlightSyncRun(t *testing.T) {
	repo := newMemoryRepository(t)
	ctx := context.Background()
	runner := newFirstRunBlockingDiscussionAgentRunner(DiscussionAgentResponse{AssistantMessage: "重试回复", Status: "completed", RuntimeKind: "managed"})
	service := newTestService(ServiceOptions{
		Repository:      repo,
		DiscussionAgent: runner,
		Clock:           fixedClock{now: testTime},
		IDGenerator:     fixedIDGen("conv-1", "msg-user", "run-old", "event-retry", "run-retry", "msg-retry", "msg-old-late"),
	})
	sendDone := make(chan error, 1)
	go func() {
		_, err := service.SendMessage(ctx, ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser}, SendMessageCommand{
			Content:        "第一版",
			IdempotencyKey: "send-old",
		})
		sendDone <- err
	}()
	select {
	case <-runner.entered:
	case <-time.After(time.Second):
		t.Fatal("sync run did not start")
	}

	dto, err := service.RetryMessage(ctx, ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser}, RetryMessageCommand{
		ConversationID: "conv-1",
		MessageID:      "msg-user",
		IdempotencyKey: "retry-msg-user",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(dto.Runs) != 1 || dto.Runs[0].RunID != "run-retry" {
		t.Fatalf("runs after retry = %+v", dto.Runs)
	}
	select {
	case <-runner.cancelled:
	case <-time.After(time.Second):
		t.Fatal("old sync run was not cancelled")
	}
	if err := <-sendDone; !errors.Is(err, context.Canceled) {
		t.Fatalf("old sync run err = %v, want context.Canceled", err)
	}
}

func TestServiceRetryIgnoresLateSyncRunCompletionAfterPrune(t *testing.T) {
	repo := newMemoryRepository(t)
	ctx := context.Background()
	runner := newIgnoringCancelFirstRunDiscussionAgentRunner(DiscussionAgentResponse{AssistantMessage: "重试回复", Status: "completed", RuntimeKind: "managed"})
	service := newTestService(ServiceOptions{
		Repository:      repo,
		DiscussionAgent: runner,
		Clock:           fixedClock{now: testTime},
		IDGenerator:     fixedIDGen("conv-1", "msg-user", "run-old", "event-retry", "run-retry", "msg-retry", "msg-old-late"),
	})
	sendDone := make(chan error, 1)
	go func() {
		_, err := service.SendMessage(ctx, ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser}, SendMessageCommand{
			Content:        "第一版",
			IdempotencyKey: "send-old",
		})
		sendDone <- err
	}()
	select {
	case <-runner.entered:
	case <-time.After(time.Second):
		t.Fatal("sync run did not start")
	}

	dto, err := service.RetryMessage(ctx, ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser}, RetryMessageCommand{
		ConversationID: "conv-1",
		MessageID:      "msg-user",
		IdempotencyKey: "retry-msg-user",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(dto.Runs) != 1 || dto.Runs[0].RunID != "run-retry" {
		t.Fatalf("runs after retry = %+v", dto.Runs)
	}

	close(runner.release)
	if err := <-sendDone; !errors.Is(err, ErrNotFound) {
		t.Fatalf("old sync run err = %v, want ErrNotFound", err)
	}
	after, err := service.GetConversation(ctx, ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser}, "conv-1")
	if err != nil {
		t.Fatal(err)
	}
	for _, message := range after.Messages {
		if message.Content == "旧回复不应复活" {
			t.Fatalf("old sync completion restored pruned message: %+v", after.Messages)
		}
	}
	if len(after.Runs) != 1 || after.Runs[0].RunID != "run-retry" {
		t.Fatalf("runs after late completion = %+v", after.Runs)
	}
}

func TestServiceRetryQueuesSameDiscussionUntilRetryRunFinishes(t *testing.T) {
	repo := newMemoryRepository(t)
	ctx := context.Background()
	if err := repo.SaveConversation(ctx, ConversationRecord{ConversationID: "conv-1", Title: "Launch plan", Status: "completed", CreatedBy: "user-1", CreatedAt: testTime, UpdatedAt: testTime}); err != nil {
		t.Fatal(err)
	}
	seedDiscussionMembers(t, repo, "conv-1", "user-1")
	if err := repo.SaveMessage(ctx, MessageRecord{MessageID: "msg-1", ConversationID: "conv-1", Role: "user", AuthorID: "user-1", AuthorName: "本地用户", Content: "第一版", CreatedAt: testTime}); err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveRun(ctx, RunRecord{RunID: "run-old", ConversationID: "conv-1", AgentID: "lead-agent", Status: "completed", CreatedAt: testTime.Add(time.Nanosecond), StartedAt: testTime.Add(time.Nanosecond), CompletedAt: testTime.Add(2 * time.Nanosecond)}); err != nil {
		t.Fatal(err)
	}
	runner := newBlockingDiscussionAgentRunner(DiscussionAgentResponse{AssistantMessage: "已重试", Status: "completed", RuntimeKind: "managed"})
	service := newTestService(ServiceOptions{
		Repository:      repo,
		DiscussionAgent: runner,
		Clock:           fixedClock{now: testTime.Add(5 * time.Minute)},
		IDGenerator:     fixedIDGen("event-retry", "run-retry", "msg-retry", "msg-after", "run-after", "msg-after-reply"),
	})

	retryDone := make(chan error, 1)
	go func() {
		_, err := service.RetryMessage(ctx, ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser}, RetryMessageCommand{
			ConversationID: "conv-1",
			MessageID:      "msg-1",
			IdempotencyKey: "retry-msg-1",
		})
		retryDone <- err
	}()
	select {
	case <-runner.entered:
	case <-time.After(time.Second):
		t.Fatal("retry run did not start")
	}

	sendDone := make(chan error, 1)
	go func() {
		_, err := service.SendMessage(ctx, ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser}, SendMessageCommand{
			ConversationID: "conv-1",
			Content:        "继续",
			IdempotencyKey: "send-after-retry",
		})
		sendDone <- err
	}()
	select {
	case err := <-sendDone:
		t.Fatalf("same discussion send completed before retry released: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	if got := runner.RequestCount(); got != 1 {
		t.Fatalf("runner requests before retry release = %d, want only retry run", got)
	}

	runner.Release()
	if err := <-retryDone; err != nil {
		t.Fatal(err)
	}
	if err := <-sendDone; err != nil {
		t.Fatal(err)
	}
	if got := runner.RequestCount(); got != 2 {
		t.Fatalf("runner requests after release = %d, want retry and queued send", got)
	}
}

func TestServiceRetryMessageStreamQueuesSameDiscussionUntilStreamFinishes(t *testing.T) {
	repo := newMemoryRepository(t)
	ctx := context.Background()
	if err := repo.SaveConversation(ctx, ConversationRecord{ConversationID: "conv-1", Title: "Launch plan", Status: "completed", CreatedBy: "user-1", CreatedAt: testTime, UpdatedAt: testTime}); err != nil {
		t.Fatal(err)
	}
	seedDiscussionMembers(t, repo, "conv-1", "user-1")
	if err := repo.SaveMessage(ctx, MessageRecord{MessageID: "msg-1", ConversationID: "conv-1", Role: "user", AuthorID: "user-1", AuthorName: "本地用户", Content: "第一版", CreatedAt: testTime}); err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveRun(ctx, RunRecord{RunID: "run-old", ConversationID: "conv-1", AgentID: "lead-agent", Status: "completed", CreatedAt: testTime.Add(time.Nanosecond), StartedAt: testTime.Add(time.Nanosecond), CompletedAt: testTime.Add(2 * time.Nanosecond)}); err != nil {
		t.Fatal(err)
	}
	streamEvents := make(chan DiscussionAgentStreamEvent, 1)
	runner := newBlockingDiscussionAgentRunner(DiscussionAgentResponse{AssistantMessage: "排队后的回复", Status: "completed", RuntimeKind: "managed"})
	runner.streamEvents = streamEvents
	service := newTestService(ServiceOptions{
		Repository:      repo,
		DiscussionAgent: runner,
		Clock:           fixedClock{now: testTime.Add(5 * time.Minute)},
		IDGenerator:     fixedIDGen("event-retry", "run-retry", "msg-after", "run-after", "msg-after-reply", "msg-retry"),
	})

	events, err := service.RetryMessageStream(ctx, ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser}, RetryMessageCommand{
		ConversationID: "conv-1",
		MessageID:      "msg-1",
		IdempotencyKey: "retry-msg-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-runner.streamStarted:
	case <-time.After(time.Second):
		t.Fatal("retry stream run did not start")
	}

	sendDone := make(chan error, 1)
	go func() {
		_, err := service.SendMessage(ctx, ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser}, SendMessageCommand{
			ConversationID: "conv-1",
			Content:        "流式重试结束后继续",
			IdempotencyKey: "send-after-retry-stream",
		})
		sendDone <- err
	}()
	select {
	case err := <-sendDone:
		t.Fatalf("same discussion send completed before retry stream released: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	if got := runner.RequestCount(); got != 0 {
		t.Fatalf("sync runner requests before retry stream completion = %d, want queued send", got)
	}

	streamEvents <- DiscussionAgentStreamEvent{Kind: DiscussionAgentStreamRunCompleted, Response: DiscussionAgentResponse{AssistantMessage: "流式重试回复", Status: "completed", RuntimeKind: "managed"}}
	close(streamEvents)
	drainLeadStream(events)
	runner.Release()
	if err := <-sendDone; err != nil {
		t.Fatal(err)
	}
	if got := runner.RequestCount(); got != 1 {
		t.Fatalf("sync runner requests after retry stream completion = %d, want queued send", got)
	}
	if got := runner.StreamRequestCount(); got != 1 {
		t.Fatalf("stream runner requests = %d, want retry stream", got)
	}
}

func TestServiceRetryApprovalMustComeFromConversationCreator(t *testing.T) {
	repo := newMemoryRepository(t)
	ctx := context.Background()
	if err := repo.SaveConversation(ctx, ConversationRecord{ConversationID: "conv-1", Title: "Launch plan", Status: "completed", CreatedBy: "owner-1", CreatedAt: testTime, UpdatedAt: testTime}); err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveMessage(ctx, MessageRecord{MessageID: "msg-user-2", ConversationID: "conv-1", Role: "user", AuthorID: "user-2", Content: "换成 html", CreatedAt: testTime.Add(time.Minute)}); err != nil {
		t.Fatal(err)
	}
	service := newTestService(ServiceOptions{
		Repository:      repo,
		DiscussionAgent: &capturingDiscussionAgentRunner{},
		Clock:           fixedClock{now: testTime.Add(2 * time.Minute)},
		IDGenerator:     fixedIDGen("retry-denied", "event-denied"),
	})

	_, err := service.RetryMessage(ctx, ViewerRef{ViewerID: "reviewer-1", ViewerKind: ViewerKindUser}, RetryMessageCommand{
		ConversationID: "conv-1",
		MessageID:      "msg-user-2",
		IdempotencyKey: "retry-msg-user-2",
		Approve:        true,
	})
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("error = %v, want ErrForbidden", err)
	}
}
