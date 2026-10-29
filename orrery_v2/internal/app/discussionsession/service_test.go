package discussionsession

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"orrery_v2/internal/app/operations"
	toolstd "orrery_v2/internal/resource/tool/std"
)

func TestServiceSendMessagePersistsUserMessageAndDispatchesRealDiscussionAgent(t *testing.T) {
	repo := newMemoryRepository(t)
	runner := &capturingDiscussionAgentRunner{response: DiscussionAgentResponse{
		AssistantMessage: "What deadline should I optimize for?",
		Status:           "needs_clarification",
		RuntimeKind:      "openclaw",
		Usage: ModelUsageMap{
			"gpt-test": {InputTokens: 21, OutputTokens: 8, CacheReadTokens: 3, TotalTokens: 29},
		},
		DurationMs: 345,
	}}
	service := newTestService(ServiceOptions{
		Repository:      repo,
		DiscussionAgent: runner,
		Clock:           fixedClock{now: testTime},
		IDGenerator:     fixedIDGen("conv-1", "msg-user", "run-1", "msg-assistant"),
	})
	viewer := ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser}

	dto, err := service.SendMessage(context.Background(), viewer, SendMessageCommand{Content: "Plan a launch", IdempotencyKey: "send-1"})
	if err != nil {
		t.Fatal(err)
	}

	if !runner.called || runner.request.Content != "Plan a launch" {
		t.Fatalf("discussion agent runner not called correctly: %+v", runner.request)
	}
	if len(dto.Messages) != 2 || dto.Messages[1].Role != "assistant" {
		t.Fatalf("messages = %+v", dto.Messages)
	}
	if len(dto.Runs) != 1 || dto.Runs[0].Status != "needs_clarification" || dto.Runs[0].RuntimeKind != "openclaw" {
		t.Fatalf("runs = %+v", dto.Runs)
	}
	if usage := dto.Runs[0].Usage["gpt-test"]; usage.InputTokens != 21 || usage.OutputTokens != 8 || usage.CacheReadTokens != 3 || usage.TotalTokens != 29 {
		t.Fatalf("run usage = %#v", dto.Runs[0].Usage)
	}
	if dto.Runs[0].DurationMs != 345 {
		t.Fatalf("run duration = %d", dto.Runs[0].DurationMs)
	}
}

func TestServiceSendMessageBackfillsEvaluationReviewWithActualRunTokens(t *testing.T) {
	ctx := context.Background()
	repo := newMemoryRepository(t)
	evals := operations.NewMemoryEvaluationProjectionStore()
	if err := evals.UpsertEvaluationReview(ctx, operations.EvaluationReviewDTO{
		ReviewID:           "review-1",
		EntityType:         "agent",
		EntityID:           "profile-agent-1",
		Summary:            "结论：档案 Agent 本周表现稳定。",
		EstimatedHumanDays: 1.5,
		CreatedAt:          testTime,
	}); err != nil {
		t.Fatalf("UpsertEvaluationReview: %v", err)
	}
	runner := &capturingDiscussionAgentRunner{response: DiscussionAgentResponse{
		AssistantMessage: "已完成综合评价。",
		Status:           "completed",
		RuntimeKind:      "codex",
		ToolCalls: []ToolCallDTO{{
			ToolCallID: "tool-review",
			Name:       string(toolstd.EvaluationReview),
			Status:     "completed",
			Result: map[string]string{
				"reviews": `[{"review_id":"review-1","agent_id":"profile-agent-1"}]`,
			},
		}},
		Usage: ModelUsageMap{
			"gpt-test": {InputTokens: 900, OutputTokens: 300, TotalTokens: 1200},
		},
		ToolLoopCompleted: true,
	}}
	service := newTestService(ServiceOptions{
		Repository:        repo,
		DiscussionAgent:   runner,
		EvaluationReviews: testEvaluationReviewUsagePort{store: evals},
		Clock:             fixedClock{now: testTime},
		IDGenerator:       fixedIDGen("conv-1", "msg-user", "run-1", "msg-assistant"),
	})

	if _, err := service.SendMessage(ctx, ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser}, SendMessageCommand{Content: "对档案 Agent 开展综合评价", IdempotencyKey: "send-evaluation-review"}); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}

	review, err := evals.GetEvaluationReview(ctx, "review-1")
	if err != nil {
		t.Fatalf("GetEvaluationReview: %v", err)
	}
	if review.EvaluationTokenConsumed != 1200 {
		t.Fatalf("evaluation token = %d", review.EvaluationTokenConsumed)
	}
}

func TestEvaluationReviewIDsFromToolCallsSupportsDirectAndBatchOutputs(t *testing.T) {
	ids := evaluationReviewIDsFromToolCalls([]ToolCallDTO{
		{Name: string(toolstd.EvaluationReview), Result: map[string]string{"review_id": "review-direct"}},
		{Name: string(toolstd.EvaluationReview), Result: map[string]string{"reviews": `[{"review_id":"review-a"},{"review_id":"review-b"},{"review_id":"review-a"}]`}},
		{Name: "discussion_ack", Result: map[string]string{"review_id": "ignored"}},
	})
	if got := strings.Join(ids, ","); got != "review-direct,review-a,review-b" {
		t.Fatalf("ids = %q", got)
	}
}

func TestServiceSendMessageAppendsRealtimeOutboxEvents(t *testing.T) {
	repo := newMemoryRepository(t)
	realtime := &recordingRealtimePublisher{}
	runner := &capturingDiscussionAgentRunner{response: DiscussionAgentResponse{
		AssistantMessage: "What deadline should I optimize for?",
		Status:           "completed",
		RuntimeKind:      "codex",
		ToolCalls: []ToolCallDTO{{
			ToolCallID: "tool-1",
			Name:       "discussion_ack",
			Status:     "completed",
			Result:     map[string]string{"ok": "true"},
		}},
		ToolLoopCompleted: true,
	}}
	service := newTestService(ServiceOptions{
		Repository:        repo,
		DiscussionAgent:   runner,
		RealtimePublisher: realtime,
		Clock:             fixedClock{now: testTime},
		IDGenerator:       fixedIDGen("conv-1", "msg-user", "run-1", "msg-assistant"),
	})

	dto, err := service.SendMessage(context.Background(), ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser}, SendMessageCommand{Content: "Plan a launch", IdempotencyKey: "send-1"})
	if err != nil {
		t.Fatal(err)
	}

	wantTypes := []string{
		RealtimeEventDiscussionMessageCreated,
		RealtimeEventUserDiscussionListInvalidated,
		RealtimeEventDiscussionTurnStarted,
		RealtimeEventDiscussionRunStarted,
		RealtimeEventDiscussionToolCall,
		RealtimeEventDiscussionToolResult,
		RealtimeEventDiscussionMessageCompleted,
		RealtimeEventDiscussionRunCompleted,
		RealtimeEventDiscussionTurnCompleted,
		RealtimeEventUserDiscussionListInvalidated,
	}
	if got := strings.Join(realtime.eventTypes(), ","); got != strings.Join(wantTypes, ",") {
		t.Fatalf("realtime event types = %v, want %v", realtime.eventTypes(), wantTypes)
	}
	messageEvents := realtime.eventsByType(RealtimeEventDiscussionMessageCreated)
	if len(messageEvents) != 1 {
		t.Fatalf("message events = %+v", messageEvents)
	}
	if messageEvents[0].Channel != "conversation:"+dto.ConversationID || messageEvents[0].AggregateType != "conversation" || messageEvents[0].AggregateID != dto.ConversationID {
		t.Fatalf("message event = %+v", messageEvents[0])
	}
	if got := realtimePayloadString(t, messageEvents[0], "message_id"); got != "msg-user" {
		t.Fatalf("message payload id = %q", got)
	}
	userEvents := realtime.eventsByType(RealtimeEventUserDiscussionListInvalidated)
	if len(userEvents) != 2 || userEvents[0].Channel != "user:user-1" || userEvents[1].Channel != "user:user-1" {
		t.Fatalf("user list events = %+v", userEvents)
	}
}

func TestServiceSendMessageMentionsHumanViewerInLeadReplies(t *testing.T) {
	repo := newMemoryRepository(t)
	runner := &capturingDiscussionAgentRunner{response: DiscussionAgentResponse{
		AssistantMessage: "网页就行，马上编排。",
		Status:           "completed",
		RuntimeKind:      "managed",
	}}
	service := newTestService(ServiceOptions{
		Repository:      repo,
		DiscussionAgent: runner,
		Clock:           fixedClock{now: testTime},
		IDGenerator:     fixedIDGen("conv-1", "msg-user", "run-1", "msg-assistant"),
	})

	dto, err := service.SendMessage(context.Background(), ViewerRef{ViewerID: "local-user", ViewerKind: ViewerKindUser, DisplayName: "本地用户"}, SendMessageCommand{Content: "网页就行 最简单的实现", IdempotencyKey: "send-1"})
	if err != nil {
		t.Fatal(err)
	}

	if len(dto.Messages) != 2 {
		t.Fatalf("messages = %+v", dto.Messages)
	}
	wantMention := "[@本地用户](mention://user/local-user)"
	if !strings.HasPrefix(dto.Messages[1].Content, wantMention+" ") {
		t.Fatalf("assistant reply = %q, want @user prefix", dto.Messages[1].Content)
	}
}

func TestServiceSendMessagePreservesMultipleExplicitMentionsInLeadReplies(t *testing.T) {
	repo := newMemoryRepository(t)
	runner := &capturingDiscussionAgentRunner{response: DiscussionAgentResponse{
		AssistantMessage: "[@Staffing](mention://agent/staffing-agent) [@Coordinator](mention://agent/coordinator-agent) 请确认可用执行人和执行顺序。",
		Status:           "needs_staffing",
		RuntimeKind:      "managed",
	}}
	service := newTestService(ServiceOptions{
		Repository:      repo,
		DiscussionAgent: runner,
		Clock:           fixedClock{now: testTime},
		IDGenerator:     fixedIDGen("conv-1", "msg-user", "run-1", "msg-assistant"),
	})

	dto, err := service.SendMessage(context.Background(), ViewerRef{ViewerID: "local-user", ViewerKind: ViewerKindUser, DisplayName: "本地用户"}, SendMessageCommand{Content: "做个网页", IdempotencyKey: "send-1"})
	if err != nil {
		t.Fatal(err)
	}

	if len(dto.Messages) != 2 || dto.Messages[1].Content != "[@Staffing](mention://agent/staffing-agent) [@Coordinator](mention://agent/coordinator-agent) 请确认可用执行人和执行顺序。" {
		t.Fatalf("assistant reply = %+v, want multiple explicit mentions preserved", dto.Messages)
	}
}

func TestServiceSendMessagePreservesMultiplePlainMentionsInLeadReplies(t *testing.T) {
	repo := newMemoryRepository(t)
	runner := &capturingDiscussionAgentRunner{response: DiscussionAgentResponse{
		AssistantMessage: "@Staffing @Coordinator 请确认可用执行人和执行顺序。",
		Status:           "needs_staffing",
		RuntimeKind:      "managed",
	}}
	service := newTestService(ServiceOptions{
		Repository:      repo,
		DiscussionAgent: runner,
		Clock:           fixedClock{now: testTime},
		IDGenerator:     fixedIDGen("conv-1", "msg-user", "run-1", "msg-assistant"),
	})

	dto, err := service.SendMessage(context.Background(), ViewerRef{ViewerID: "local-user", ViewerKind: ViewerKindUser, DisplayName: "本地用户"}, SendMessageCommand{Content: "做个网页", IdempotencyKey: "send-1"})
	if err != nil {
		t.Fatal(err)
	}

	if len(dto.Messages) != 2 || dto.Messages[1].Content != "@Staffing @Coordinator 请确认可用执行人和执行顺序。" {
		t.Fatalf("assistant reply = %+v, want multiple plain mentions preserved", dto.Messages)
	}
}

func TestServiceSendMessagePersistsCurrentViewerDisplayName(t *testing.T) {
	repo := newMemoryRepository(t)
	runner := &capturingDiscussionAgentRunner{response: DiscussionAgentResponse{
		AssistantMessage: "收到，我会推进。",
		Status:           "completed",
		RuntimeKind:      "codex",
	}}
	service := newTestService(ServiceOptions{
		Repository:      repo,
		DiscussionAgent: runner,
		Clock:           fixedClock{now: testTime},
		IDGenerator:     fixedIDGen("conv-1", "msg-user", "run-1", "msg-assistant"),
	})
	viewer := ViewerRef{ViewerID: "local-user", ViewerKind: ViewerKindUser, DisplayName: "本地用户"}

	dto, err := service.SendMessage(context.Background(), viewer, SendMessageCommand{Content: "发起一个话题", IdempotencyKey: "send-display-name"})
	if err != nil {
		t.Fatal(err)
	}

	if dto.CreatedBy != "local-user" || dto.CreatedByName != "本地用户" {
		t.Fatalf("conversation creator = %q/%q", dto.CreatedBy, dto.CreatedByName)
	}
	if len(dto.Messages) == 0 || dto.Messages[0].AuthorID != "local-user" || dto.Messages[0].AuthorName != "本地用户" {
		t.Fatalf("user message author = %+v", dto.Messages)
	}
}

func TestServiceListConversationsPaginatesNewestFirst(t *testing.T) {
	repo := newMemoryRepository(t)
	service := newTestService(ServiceOptions{Repository: repo})
	ctx := context.Background()
	base := testTime
	for i, id := range []string{"conv-1", "conv-2", "conv-3"} {
		if err := repo.SaveConversation(ctx, ConversationRecord{
			ConversationID: id,
			Title:          id,
			Status:         "completed",
			CreatedBy:      "user-1",
			CreatedAt:      base.Add(time.Duration(i) * time.Minute),
			UpdatedAt:      base.Add(time.Duration(i) * time.Minute),
		}); err != nil {
			t.Fatal(err)
		}
	}

	page, err := service.ListConversations(ctx, ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser}, ListConversationsQuery{Page: 2, PageSize: 2})
	if err != nil {
		t.Fatal(err)
	}

	if page.Page.Page != 2 || page.Page.PageSize != 2 || page.Page.Total != 3 {
		t.Fatalf("page metadata = %+v", page.Page)
	}
	if len(page.Conversations) != 1 || page.Conversations[0].ConversationID != "conv-1" {
		t.Fatalf("conversations = %+v", page.Conversations)
	}
}

func TestServiceDeleteConversationRemovesConversation(t *testing.T) {
	repo := newMemoryRepository(t)
	cleaner := &capturingConversationArtifactCleaner{}
	runtimeCleaner := &capturingDiscussionRuntimeRecordCleaner{}
	service := newTestService(ServiceOptions{Repository: repo, ArtifactCleaner: cleaner, RuntimeRecordCleaner: runtimeCleaner})
	ctx := context.Background()
	if err := repo.SaveConversation(ctx, ConversationRecord{ConversationID: "conv-delete", Title: "Delete me", Status: "completed", CreatedBy: "user-1", CreatedAt: testTime, UpdatedAt: testTime}); err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveRun(ctx, RunRecord{RunID: "run-delete", ConversationID: "conv-delete", AgentID: "lead-agent", Status: "completed", CreatedAt: testTime}); err != nil {
		t.Fatal(err)
	}

	if err := service.DeleteConversation(ctx, ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser}, "conv-delete"); err != nil {
		t.Fatal(err)
	}

	if _, err := service.GetConversation(ctx, ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser}, "conv-delete"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetConversation err = %v, want ErrNotFound", err)
	}
	if len(cleaner.deletedConversations) != 1 || cleaner.deletedConversations[0] != "conv-delete" {
		t.Fatalf("deleted conversation artifacts = %+v", cleaner.deletedConversations)
	}
	if len(cleaner.deletedRuns) != 1 || cleaner.deletedRuns[0] != "conv-delete|run-delete" {
		t.Fatalf("deleted run artifacts = %+v", cleaner.deletedRuns)
	}
	if len(runtimeCleaner.deletedRuns) != 1 || runtimeCleaner.deletedRuns[0] != "run-delete" {
		t.Fatalf("deleted runtime records = %+v", runtimeCleaner.deletedRuns)
	}
}

func TestServiceDeleteConversationUsesDiscussionAgentRuntimeRecordCleaner(t *testing.T) {
	repo := newMemoryRepository(t)
	runner := &runtimeCleaningDiscussionAgentRunner{}
	service := newTestService(ServiceOptions{Repository: repo, DiscussionAgent: runner})
	ctx := context.Background()
	if err := repo.SaveConversation(ctx, ConversationRecord{ConversationID: "conv-delete", Title: "Delete me", Status: "completed", CreatedBy: "user-1", CreatedAt: testTime, UpdatedAt: testTime}); err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveRun(ctx, RunRecord{RunID: "run-delete", ConversationID: "conv-delete", AgentID: "lead-agent", Status: "completed", CreatedAt: testTime}); err != nil {
		t.Fatal(err)
	}

	if err := service.DeleteConversation(ctx, ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser}, "conv-delete"); err != nil {
		t.Fatal(err)
	}

	if len(runner.cleaner.deletedRuns) != 1 || runner.cleaner.deletedRuns[0] != "run-delete" {
		t.Fatalf("deleted runtime records = %+v", runner.cleaner.deletedRuns)
	}
}

func TestServiceDeleteConversationDeletesLinkedMissionsAndCreatedTasksBeforeConversationRecords(t *testing.T) {
	repo := newMemoryRepository(t)
	ctx := context.Background()
	if err := repo.SaveConversation(ctx, ConversationRecord{ConversationID: "conv-delete", Title: "Delete me", Status: "completed", CreatedBy: "user-1", CreatedAt: testTime, UpdatedAt: testTime.Add(4 * time.Minute)}); err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveMissionLink(ctx, LinkedMissionRecord{ConversationID: "conv-delete", MissionID: "mission-existing", Title: "Existing mission", Status: "active", CreatedAt: testTime}); err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveMessage(ctx, MessageRecord{MessageID: "msg-create", ConversationID: "conv-delete", Role: "assistant", AuthorID: "lead-agent", Content: "created mission", CreatedAt: testTime.Add(time.Minute)}); err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveRun(ctx, RunRecord{RunID: "run-create", ConversationID: "conv-delete", AgentID: "lead-agent", Status: "completed", CreatedAt: testTime.Add(time.Minute), StartedAt: testTime.Add(time.Minute), CompletedAt: testTime.Add(2 * time.Minute), ToolCalls: []ToolCallDTO{{
		ToolCallID: "tool-mission",
		Name:       "mission_brief_create",
		Status:     "completed",
		Result:     map[string]string{"mission_id": "mission-created"},
	}}}); err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveMissionLink(ctx, LinkedMissionRecord{ConversationID: "conv-delete", MissionID: "mission-created", Title: "Created mission", Status: "active", CreatedAt: testTime.Add(2 * time.Minute)}); err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveRun(ctx, RunRecord{RunID: "run-task", ConversationID: "conv-delete", AgentID: "lead-agent", Status: "completed", CreatedAt: testTime.Add(3 * time.Minute), StartedAt: testTime.Add(3 * time.Minute), CompletedAt: testTime.Add(4 * time.Minute), ToolCalls: []ToolCallDTO{{
		ToolCallID: "tool-task",
		Name:       "mission_task_create_root",
		Status:     "completed",
		Arguments:  map[string]string{"mission_id": "mission-existing"},
		Result:     map[string]string{"mission_id": "mission-existing", "task_id": "task-created"},
	}}}); err != nil {
		t.Fatal(err)
	}
	deleter := &capturingExecutionDeleter{}
	cleaner := &capturingConversationArtifactCleaner{}
	service := newTestService(ServiceOptions{Repository: repo, ExecutionDeleter: deleter, ArtifactCleaner: cleaner})

	if err := service.DeleteConversation(ctx, ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser}, "conv-delete"); err != nil {
		t.Fatal(err)
	}

	if len(deleter.deletedMissionIDs) != 1 || deleter.deletedMissionIDs[0] != "mission-created" {
		t.Fatalf("deleted missions = %+v, want only mission-created", deleter.deletedMissionIDs)
	}
	if len(deleter.deletedTasks) != 1 || deleter.deletedTasks[0] != "mission-existing|task-created" {
		t.Fatalf("deleted tasks = %+v", deleter.deletedTasks)
	}
	if len(cleaner.deletedConversations) != 1 || cleaner.deletedConversations[0] != "conv-delete" {
		t.Fatalf("deleted conversation artifacts = %+v", cleaner.deletedConversations)
	}
	if _, err := service.GetConversation(ctx, ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser}, "conv-delete"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetConversation err = %v, want ErrNotFound", err)
	}
}

func TestServiceDeleteConversationKeepsConversationWhenLinkedMissionDeleteFails(t *testing.T) {
	repo := newMemoryRepository(t)
	ctx := context.Background()
	if err := repo.SaveConversation(ctx, ConversationRecord{ConversationID: "conv-delete", Title: "Delete me", Status: "completed", CreatedBy: "user-1", CreatedAt: testTime, UpdatedAt: testTime}); err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveRun(ctx, RunRecord{RunID: "run-create", ConversationID: "conv-delete", AgentID: "lead-agent", Status: "completed", CreatedAt: testTime, StartedAt: testTime, CompletedAt: testTime.Add(time.Minute), ToolCalls: []ToolCallDTO{{
		ToolCallID: "tool-mission",
		Name:       "mission_brief_create",
		Status:     "completed",
		Result:     map[string]string{"mission_id": "mission-created"},
	}}}); err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveMissionLink(ctx, LinkedMissionRecord{ConversationID: "conv-delete", MissionID: "mission-created", Title: "Created mission", Status: "active", CreatedAt: testTime.Add(time.Minute)}); err != nil {
		t.Fatal(err)
	}
	deleter := &capturingExecutionDeleter{err: errors.New("delete mission failed")}
	cleaner := &capturingConversationArtifactCleaner{}
	service := newTestService(ServiceOptions{Repository: repo, ExecutionDeleter: deleter, ArtifactCleaner: cleaner})

	err := service.DeleteConversation(ctx, ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser}, "conv-delete")

	if err == nil || !strings.Contains(err.Error(), "delete mission failed") {
		t.Fatalf("DeleteConversation err = %v, want delete mission failure", err)
	}
	if _, getErr := service.GetConversation(ctx, ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser}, "conv-delete"); getErr != nil {
		t.Fatalf("conversation should remain after failed mission delete, get err = %v", getErr)
	}
	if len(cleaner.deletedConversations) != 0 {
		t.Fatalf("conversation artifacts should not be cleaned before external execution delete succeeds: %+v", cleaner.deletedConversations)
	}
}

func TestServiceDeleteConversationKeepsConversationWhenExecutionDeleterMissing(t *testing.T) {
	repo := newMemoryRepository(t)
	ctx := context.Background()
	if err := repo.SaveConversation(ctx, ConversationRecord{ConversationID: "conv-delete", Title: "Delete me", Status: "completed", CreatedBy: "user-1", CreatedAt: testTime, UpdatedAt: testTime}); err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveRun(ctx, RunRecord{RunID: "run-create", ConversationID: "conv-delete", AgentID: "lead-agent", Status: "completed", CreatedAt: testTime, StartedAt: testTime, CompletedAt: testTime.Add(time.Minute), ToolCalls: []ToolCallDTO{{
		ToolCallID: "tool-mission",
		Name:       "mission_brief_create",
		Status:     "completed",
		Result:     map[string]string{"mission_id": "mission-created", "operation": "created"},
	}}}); err != nil {
		t.Fatal(err)
	}
	service := newTestService(ServiceOptions{Repository: repo})

	err := service.DeleteConversation(ctx, ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser}, "conv-delete")

	if !errors.Is(err, ErrServiceDependency) {
		t.Fatalf("DeleteConversation err = %v, want ErrServiceDependency", err)
	}
	if _, getErr := service.GetConversation(ctx, ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser}, "conv-delete"); getErr != nil {
		t.Fatalf("conversation should remain without execution deleter, get err = %v", getErr)
	}
}

func TestServiceDeleteConversationKeepsExistingMissionWhenRunUpdatedIt(t *testing.T) {
	repo := newMemoryRepository(t)
	ctx := context.Background()
	if err := repo.SaveConversation(ctx, ConversationRecord{ConversationID: "conv-delete", Title: "Delete me", Status: "completed", CreatedBy: "user-1", CreatedAt: testTime, UpdatedAt: testTime.Add(2 * time.Minute)}); err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveRun(ctx, RunRecord{RunID: "run-update", ConversationID: "conv-delete", AgentID: "lead-agent", Status: "completed", CreatedAt: testTime.Add(time.Minute), StartedAt: testTime.Add(time.Minute), CompletedAt: testTime.Add(2 * time.Minute), ToolCalls: []ToolCallDTO{{
		ToolCallID: "tool-mission-update",
		Name:       "mission_brief_create",
		Status:     "completed",
		Result:     map[string]string{"mission_id": "mission-existing", "operation": "updated"},
	}}}); err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveMissionLink(ctx, LinkedMissionRecord{ConversationID: "conv-delete", MissionID: "mission-existing", Title: "Existing mission", Status: "active", CreatedAt: testTime.Add(2 * time.Minute)}); err != nil {
		t.Fatal(err)
	}
	deleter := &capturingExecutionDeleter{}
	service := newTestService(ServiceOptions{Repository: repo, ExecutionDeleter: deleter})

	if err := service.DeleteConversation(ctx, ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser}, "conv-delete"); err != nil {
		t.Fatal(err)
	}

	if len(deleter.deletedMissionIDs) != 0 {
		t.Fatalf("updated existing mission should not be deleted: %+v", deleter.deletedMissionIDs)
	}
	if _, err := service.GetConversation(ctx, ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser}, "conv-delete"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetConversation err = %v, want ErrNotFound", err)
	}
}

func TestServiceSendMessageOrdersRunAfterUserMessage(t *testing.T) {
	repo := newMemoryRepository(t)
	runner := &capturingDiscussionAgentRunner{response: DiscussionAgentResponse{
		AssistantMessage: "收到，我会推进。",
		Status:           "completed",
		RuntimeKind:      "codex",
	}}
	service := newTestService(ServiceOptions{
		Repository:      repo,
		DiscussionAgent: runner,
		Clock:           fixedClock{now: testTime},
		IDGenerator:     fixedIDGen("conv-1", "msg-user", "run-1", "msg-assistant"),
	})

	dto, err := service.SendMessage(context.Background(), ViewerRef{ViewerID: "local-user", ViewerKind: ViewerKindUser, DisplayName: "本地用户"}, SendMessageCommand{Content: "继续推进", IdempotencyKey: "send-order"})
	if err != nil {
		t.Fatal(err)
	}

	if len(dto.Messages) < 1 || len(dto.Runs) != 1 {
		t.Fatalf("conversation = %+v", dto)
	}
	if !dto.Runs[0].CreatedAt.After(dto.Messages[0].CreatedAt) || !dto.Runs[0].StartedAt.After(dto.Messages[0].CreatedAt) {
		t.Fatalf("run timestamps = %s/%s, want after user message %s", dto.Runs[0].CreatedAt, dto.Runs[0].StartedAt, dto.Messages[0].CreatedAt)
	}
}

func TestServiceSendMessagePersistsClarifyingAssistantMessageWithoutLeadAskTool(t *testing.T) {
	repo := newMemoryRepository(t)
	runner := &capturingDiscussionAgentRunner{response: DiscussionAgentResponse{
		AssistantMessage: "Which launch date should I optimize for?",
		Status:           "needs_clarification",
		RuntimeKind:      "codex",
	}}
	service := newTestService(ServiceOptions{
		Repository:      repo,
		DiscussionAgent: runner,
		Clock:           fixedClock{now: testTime},
		IDGenerator:     fixedIDGen("conv-1", "msg-user", "run-1", "msg-assistant"),
	})

	dto, err := service.SendMessage(context.Background(), ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser}, SendMessageCommand{Content: "Plan launch", IdempotencyKey: "send-1"})
	if err != nil {
		t.Fatal(err)
	}

	if len(dto.Messages) != 2 || dto.Messages[1].Role != "assistant" || dto.Messages[1].Content != "[@本地用户](mention://user/user-1) Which launch date should I optimize for?" {
		t.Fatalf("messages = %+v", dto.Messages)
	}
	if len(dto.Runs) != 1 || len(dto.Runs[0].ToolCalls) != 0 {
		t.Fatalf("run tool calls = %+v", dto.Runs)
	}
}

func TestServiceSendMessagePersistsSelectedDiscussionAgentIdentity(t *testing.T) {
	repo := newMemoryRepository(t)
	runner := &capturingDiscussionAgentRunner{
		agentID: "custom-lead-agent",
		response: DiscussionAgentResponse{
			AssistantMessage: "What deadline should I optimize for?",
			Status:           "needs_clarification",
			RuntimeKind:      "codex",
		},
	}
	service := newTestService(ServiceOptions{
		Repository:          repo,
		DiscussionAgent:     runner,
		InitialAgentMembers: defaultTestConversationAgentMembersFor("custom-lead-agent"),
		Clock:               fixedClock{now: testTime},
		IDGenerator:         fixedIDGen("conv-1", "msg-user", "run-1", "msg-assistant"),
	})
	viewer := ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser}

	dto, err := service.SendMessage(context.Background(), viewer, SendMessageCommand{Content: "Plan a launch", IdempotencyKey: "send-1"})
	if err != nil {
		t.Fatal(err)
	}

	if len(dto.Runs) != 1 || dto.Runs[0].AgentID != "custom-lead-agent" {
		t.Fatalf("runs = %+v, want selected discussion agent id", dto.Runs)
	}
	if len(dto.Messages) != 2 || dto.Messages[1].AuthorID != "custom-lead-agent" {
		t.Fatalf("messages = %+v, want assistant authored by selected discussion agent", dto.Messages)
	}
}

func TestServiceSendMessageDoesNotAutomaticallyRetrieveMemoryContext(t *testing.T) {
	repo := newMemoryRepository(t)
	runner := &capturingDiscussionAgentRunner{
		agentID:  "lead-agent",
		response: DiscussionAgentResponse{AssistantMessage: "I will answer from the live discussion.", Status: "completed", RuntimeKind: "codex"},
	}
	memory := &capturingDiscussionAgentMemoryProvider{context: DiscussionAgentMemoryContext{
		PersonalDigest: DiscussionAgentMemoryDigest{
			DigestID:     "digest.pm",
			MemoryKind:   "personal",
			Summary:      "The operator prefers short launch plans with owner/date/risk sections.",
			SourceMemory: []string{"memory-record-1"},
		},
	}}
	service := newTestService(ServiceOptions{
		Repository:      repo,
		DiscussionAgent: runner,
		MemoryProvider:  memory,
		Clock:           fixedClock{now: testTime},
		IDGenerator:     fixedIDGen("conv-1", "msg-user", "run-1", "msg-assistant"),
	})

	if _, err := service.SendMessage(context.Background(), ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser}, SendMessageCommand{Content: "Plan a launch", IdempotencyKey: "send-1"}); err != nil {
		t.Fatal(err)
	}

	if memory.called {
		t.Fatalf("memory lookup was called automatically: agent %q query %q", memory.agentID, memory.query)
	}
	if runner.request.Memory.PersonalDigest.Summary != "" || len(runner.request.Memory.CollectiveDigests) != 0 {
		t.Fatalf("discussion agent received automatic memory context = %+v", runner.request.Memory)
	}
}

func TestServiceSendMessageDoesNotAutomaticallyRetrieveKnowledgeContext(t *testing.T) {
	repo := newMemoryRepository(t)
	runner := &capturingDiscussionAgentRunner{
		agentID:  "lead-agent",
		response: DiscussionAgentResponse{AssistantMessage: "I will answer from the live discussion.", Status: "completed", RuntimeKind: "codex"},
	}
	knowledge := &capturingDiscussionAgentKnowledgeProvider{context: DiscussionAgentKnowledgeContext{
		Results: []DiscussionAgentKnowledgeResult{{
			KnowledgeBaseID: "kb-release",
			DocumentID:      "doc-release",
			ChunkID:         "chunk-release",
			DocumentName:    "release-handbook.md",
			Score:           0.91,
			Snippet:         "发布流程必须先完成灰度验收，然后通知运营。",
		}},
	}}
	service := newTestService(ServiceOptions{
		Repository:        repo,
		DiscussionAgent:   runner,
		KnowledgeProvider: knowledge,
		Clock:             fixedClock{now: testTime},
		IDGenerator:       fixedIDGen("conv-1", "msg-user", "run-1", "msg-assistant"),
	})

	if _, err := service.SendMessage(context.Background(), ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser}, SendMessageCommand{Content: "发布流程是什么？", IdempotencyKey: "send-1"}); err != nil {
		t.Fatal(err)
	}

	if knowledge.called {
		t.Fatalf("knowledge provider was called automatically: %+v", knowledge.request)
	}
	if got := runner.request.Knowledge.Results; len(got) != 0 {
		t.Fatalf("discussion agent received automatic knowledge context = %+v", runner.request.Knowledge)
	}
}

func TestServiceSendMessageIgnoresUnusedKnowledgeContextProviderFailure(t *testing.T) {
	repo := newMemoryRepository(t)
	runner := &capturingDiscussionAgentRunner{agentID: "lead-agent", response: DiscussionAgentResponse{AssistantMessage: "ok", Status: "completed", RuntimeKind: "codex"}}
	knowledge := &capturingDiscussionAgentKnowledgeProvider{err: errors.New("knowledge search failed")}
	service := newTestService(ServiceOptions{
		Repository:        repo,
		DiscussionAgent:   runner,
		KnowledgeProvider: knowledge,
		Clock:             fixedClock{now: testTime},
		IDGenerator:       fixedIDGen("conv-1", "msg-user", "run-1", "msg-assistant"),
	})

	_, err := service.SendMessage(context.Background(), ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser}, SendMessageCommand{Content: "Plan launch", IdempotencyKey: "send-1"})
	if err != nil || knowledge.called || !runner.called {
		t.Fatalf("SendMessage error = %v, knowledge called = %t, runner called = %t", err, knowledge.called, runner.called)
	}
	detail, getErr := service.GetConversation(context.Background(), ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser}, "conv-1")
	if getErr != nil {
		t.Fatal(getErr)
	}
	if detail.Status == "failed" || len(detail.Runs) != 1 || detail.Runs[0].Status != "completed" {
		t.Fatalf("conversation after knowledge provider failure = %+v", detail)
	}
}

func TestServiceSendMessageDoesNotDuplicateCurrentUserMessageInPromptHistory(t *testing.T) {
	repo := newMemoryRepository(t)
	runner := &capturingDiscussionAgentRunner{response: DiscussionAgentResponse{AssistantMessage: "Which deadline?", Status: "completed", RuntimeKind: "codex"}}
	service := newTestService(ServiceOptions{
		Repository:      repo,
		DiscussionAgent: runner,
		Clock:           fixedClock{now: testTime},
		IDGenerator:     fixedIDGen("conv-1", "msg-user", "run-1", "msg-assistant"),
	})

	if _, err := service.SendMessage(context.Background(), ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser}, SendMessageCommand{Content: "Plan launch", IdempotencyKey: "send-1"}); err != nil {
		t.Fatal(err)
	}

	if len(runner.request.History) != 0 {
		t.Fatalf("current user message should not be duplicated in prompt history: %+v", runner.request.History)
	}
	if runner.request.Content != "Plan launch" {
		t.Fatalf("request content = %q", runner.request.Content)
	}
}

func TestServiceSendMessageIgnoresUnusedMemoryContextProviderFailures(t *testing.T) {
	repo := newMemoryRepository(t)
	service := newTestService(ServiceOptions{
		Repository:      repo,
		DiscussionAgent: &capturingDiscussionAgentRunner{agentID: "lead-agent", response: DiscussionAgentResponse{AssistantMessage: "ok", Status: "completed", RuntimeKind: "codex"}},
		MemoryProvider:  &capturingDiscussionAgentMemoryProvider{err: errors.New("memory read failed")},
		Clock:           fixedClock{now: testTime},
		IDGenerator:     fixedIDGen("conv-1", "msg-user", "run-1", "msg-assistant"),
	})

	_, err := service.SendMessage(context.Background(), ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser}, SendMessageCommand{Content: "Plan launch", IdempotencyKey: "send-1"})
	if err != nil {
		t.Fatalf("SendMessage should not call memory provider: %v", err)
	}
}

func TestServiceSendMessageRequiresDiscussionAgentRunner(t *testing.T) {
	repo := newMemoryRepository(t)
	service := newTestService(ServiceOptions{
		Repository:  repo,
		Clock:       fixedClock{now: testTime},
		IDGenerator: fixedIDGen("conv-1", "msg-user", "run-1", "msg-assistant"),
	})

	_, err := service.SendMessage(context.Background(), ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser}, SendMessageCommand{Content: "Plan a launch", IdempotencyKey: "send-1"})
	if !errors.Is(err, ErrRuntimeUnavailable) {
		t.Fatalf("error = %v, want ErrRuntimeUnavailable", err)
	}
}

func TestServiceSendMessageReturnsConfiguredRuntimeSetupError(t *testing.T) {
	repo := newMemoryRepository(t)
	service := newTestService(ServiceOptions{
		Repository:        repo,
		RuntimeSetupError: NewRuntimeSetupError("pi", "pi executable not found at \"pi\""),
		Clock:             fixedClock{now: testTime},
		IDGenerator:       fixedIDGen("conv-1", "msg-user", "run-1", "msg-assistant"),
	})

	_, err := service.SendMessage(context.Background(), ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser}, SendMessageCommand{Content: "Plan a launch", IdempotencyKey: "send-1"})
	var setupErr RuntimeSetupError
	if !errors.As(err, &setupErr) || setupErr.RuntimeKind != "pi" || !strings.Contains(setupErr.Cause, "pi executable not found") {
		t.Fatalf("error = %#v, setup = %#v", err, setupErr)
	}
}

func TestServiceSendMessagePersistsFailedRunAndConversationStatus(t *testing.T) {
	repo := newMemoryRepository(t)
	service := newTestService(ServiceOptions{
		Repository:      repo,
		DiscussionAgent: &capturingDiscussionAgentRunner{err: errors.New("runtime crashed")},
		Clock:           fixedClock{now: testTime},
		IDGenerator:     fixedIDGen("conv-1", "msg-user", "run-1"),
	})
	viewer := ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser}

	_, err := service.SendMessage(context.Background(), viewer, SendMessageCommand{Content: "Plan a launch", IdempotencyKey: "send-1"})
	if err == nil {
		t.Fatal("SendMessage error = nil, want runtime error")
	}

	detail, getErr := service.GetConversation(context.Background(), viewer, "conv-1")
	if getErr != nil {
		t.Fatal(getErr)
	}
	if detail.Status != "failed" {
		t.Fatalf("conversation status = %q, want failed", detail.Status)
	}
	if len(detail.Runs) != 1 || detail.Runs[0].Status != "failed" || detail.Runs[0].CompletedAt.IsZero() {
		t.Fatalf("runs = %+v, want one completed failed run", detail.Runs)
	}
	page, listErr := service.ListConversations(context.Background(), viewer, ListConversationsQuery{})
	if listErr != nil {
		t.Fatal(listErr)
	}
	if len(page.Conversations) != 1 || page.Conversations[0].Status != "failed" {
		t.Fatalf("page = %+v, want failed conversation summary", page)
	}
}
