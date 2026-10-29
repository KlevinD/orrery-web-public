package discussionsession

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	missioncontrol "orrery_v2/internal/app/missioncontrol"
)

func TestServiceSendMessageStreamEmitsDeltasAndPersistsCompletion(t *testing.T) {
	repo := newMemoryRepository(t)
	runner := &streamingDiscussionAgentRunner{events: []DiscussionAgentStreamEvent{
		{Kind: DiscussionAgentStreamThinkingDelta, Delta: "Checking ", ReasoningKind: agentcore.ReasoningContentSummary},
		{Kind: DiscussionAgentStreamThinkingDelta, Delta: "the request.", ReasoningKind: agentcore.ReasoningContentSummary},
		{Kind: DiscussionAgentStreamTokenDelta, Delta: "What "},
		{Kind: DiscussionAgentStreamTokenDelta, Delta: "deadline?"},
		{Kind: DiscussionAgentStreamToolCall, ToolCall: ToolCallDTO{ToolCallID: "tool-1", Name: "mission_brief_create", Status: "running", Arguments: map[string]string{"title": "Launch plan"}}},
		{Kind: DiscussionAgentStreamToolResult, ToolCall: ToolCallDTO{ToolCallID: "tool-1", Name: "mission_brief_create", Status: "completed", Result: map[string]string{"mission_id": "mission-1"}}},
		{Kind: DiscussionAgentStreamRunCompleted, Response: DiscussionAgentResponse{
			AssistantMessage: "What deadline?",
			Status:           "completed",
			RuntimeKind:      "codex",
			SessionID:        "session-1",
			ToolCalls: []ToolCallDTO{{
				ToolCallID: "tool-1",
				Name:       "mission_brief_create",
				Status:     "completed",
				Arguments:  map[string]string{"title": "Launch plan"},
			}},
		}},
	}}
	service := newTestService(ServiceOptions{
		Repository:      repo,
		DiscussionAgent: runner,
		Clock:           fixedClock{now: testTime},
		IDGenerator:     fixedIDGen("conv-1", "msg-user", "run-1", "msg-assistant"),
	})

	events, err := service.SendMessageStream(context.Background(), ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser}, SendMessageCommand{Content: "Plan a launch", IdempotencyKey: "send-1"})
	if err != nil {
		t.Fatal(err)
	}
	gotKinds := []string{}
	toolEventRunIDs := []string{}
	var streamedText strings.Builder
	var streamedThinking strings.Builder
	for event := range events {
		gotKinds = append(gotKinds, event.Kind)
		if event.Kind == StreamEventTokenDelta {
			streamedText.WriteString(event.Delta)
		}
		if event.Kind == StreamEventThinkingDelta {
			streamedThinking.WriteString(event.Delta)
		}
		if event.Kind == StreamEventToolCall || event.Kind == StreamEventToolResult {
			toolEventRunIDs = append(toolEventRunIDs, event.RunID)
		}
	}
	wantKinds := []string{
		StreamEventSnapshot,
		StreamEventTurnStarted,
		StreamEventRunStarted,
		StreamEventThinkingDelta,
		StreamEventTokenDelta,
		StreamEventToolCall,
		StreamEventToolResult,
		StreamEventMessageCompleted,
		StreamEventRunCompleted,
		StreamEventTurnCompleted,
		StreamEventSnapshot,
	}
	if strings.Join(gotKinds, ",") != strings.Join(wantKinds, ",") {
		t.Fatalf("stream kinds = %v, want %v", gotKinds, wantKinds)
	}
	if strings.Join(toolEventRunIDs, ",") != "run-1,run-1" {
		t.Fatalf("tool event run IDs = %v, want both run-1", toolEventRunIDs)
	}
	if streamedText.String() != "What deadline?" {
		t.Fatalf("streamed text = %q", streamedText.String())
	}
	if streamedThinking.String() != "Checking the request." {
		t.Fatalf("streamed thinking = %q", streamedThinking.String())
	}

	detail, err := service.GetConversation(context.Background(), ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser}, "conv-1")
	if err != nil {
		t.Fatal(err)
	}
	if detail.Status != "completed" || len(detail.Messages) != 2 || detail.Messages[1].Content != "[@本地用户](mention://user/user-1) What deadline?" {
		t.Fatalf("conversation detail = %+v", detail)
	}
	if len(detail.Runs) != 1 || detail.Runs[0].RuntimeKind != "codex" || len(detail.Runs[0].ToolCalls) != 1 {
		t.Fatalf("runs = %+v", detail.Runs)
	}
	if detail.Runs[0].ThinkingSummary != "Checking the request." {
		t.Fatalf("persisted thinking summary = %q", detail.Runs[0].ThinkingSummary)
	}
}

func TestServiceStreamRuntimeAppendsRunAndMessageRealtimeEvents(t *testing.T) {
	repo := newMemoryRepository(t)
	realtime := &recordingRealtimePublisher{}
	runner := &streamingDiscussionAgentRunner{events: []DiscussionAgentStreamEvent{
		{Kind: DiscussionAgentStreamThinkingDelta, Delta: "Checking the request.", ReasoningKind: agentcore.ReasoningContentSummary},
		{Kind: DiscussionAgentStreamTokenDelta, Delta: "What deadline?"},
		{Kind: DiscussionAgentStreamToolCall, ToolCall: ToolCallDTO{ToolCallID: "tool-1", Name: "discussion_ack", Status: "running"}},
		{Kind: DiscussionAgentStreamToolResult, ToolCall: ToolCallDTO{ToolCallID: "tool-1", Name: "discussion_ack", Status: "completed", Result: map[string]string{"ok": "true"}}},
		{Kind: DiscussionAgentStreamRunCompleted, Response: DiscussionAgentResponse{
			AssistantMessage: "What deadline?",
			Status:           "completed",
			RuntimeKind:      "codex",
			ToolCalls: []ToolCallDTO{{
				ToolCallID: "tool-1",
				Name:       "discussion_ack",
				Status:     "completed",
				Result:     map[string]string{"ok": "true"},
			}},
			ToolLoopCompleted: true,
		}},
	}}
	service := newTestService(ServiceOptions{
		Repository:        repo,
		DiscussionAgent:   runner,
		RealtimePublisher: realtime,
		Clock:             fixedClock{now: testTime},
		IDGenerator:       fixedIDGen("conv-1", "msg-user", "run-1", "msg-assistant"),
	})

	events, err := service.SendMessageStream(context.Background(), ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser}, SendMessageCommand{Content: "Plan a launch", IdempotencyKey: "send-1"})
	if err != nil {
		t.Fatal(err)
	}
	drainLeadStream(events)

	wantTypes := []string{
		RealtimeEventDiscussionMessageCreated,
		RealtimeEventUserDiscussionListInvalidated,
		RealtimeEventDiscussionTurnStarted,
		RealtimeEventDiscussionRunStarted,
		RealtimeEventDiscussionThinkingDelta,
		RealtimeEventDiscussionToolCall,
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
	runEvents := realtime.eventsByType(RealtimeEventDiscussionRunCompleted)
	if len(runEvents) != 1 || runEvents[0].Channel != "conversation:conv-1" || realtimePayloadString(t, runEvents[0], "run_id") != "run-1" {
		t.Fatalf("run completed events = %+v", runEvents)
	}
	userEvents := realtime.eventsByType(RealtimeEventUserDiscussionListInvalidated)
	if len(userEvents) != 2 || userEvents[1].Channel != "user:user-1" {
		t.Fatalf("user list events = %+v", userEvents)
	}
	messageEvents := realtime.eventsByType(RealtimeEventDiscussionMessageCompleted)
	if len(messageEvents) != 1 || realtimePayloadString(t, messageEvents[0], "message_id") != "msg-assistant" {
		t.Fatalf("message completed events = %+v", messageEvents)
	}
}

func TestServiceSendMessageStreamErrorsWhenLeadCreatesMissionWithoutRootTask(t *testing.T) {
	repo := newMemoryRepository(t)
	runner := &streamingDiscussionAgentRunner{events: []DiscussionAgentStreamEvent{
		{Kind: DiscussionAgentStreamTokenDelta, Delta: "I created the Mission."},
		{Kind: DiscussionAgentStreamRunCompleted, Response: DiscussionAgentResponse{
			AssistantMessage: "I created the Mission.",
			Status:           "completed",
			RuntimeKind:      "managed",
			ToolCalls: []ToolCallDTO{{
				ToolCallID: "tool-brief",
				Name:       "mission_brief_create",
				Status:     "completed",
				Arguments:  map[string]string{"title": "HTML Snake", "final_goal": "Build a snake game"},
			}},
		}},
	}}
	applier := &capturingMissionToolApplier{links: []LinkedMissionDTO{{MissionID: "mission-1", Title: "HTML Snake", Status: "active", CreatedAt: testTime}}}
	service := newTestService(ServiceOptions{
		Repository:      repo,
		DiscussionAgent: runner,
		MissionTooling:  applier,
		Clock:           fixedClock{now: testTime},
		IDGenerator:     fixedIDGen("conv-1", "msg-user", "run-1", "msg-assistant"),
	})

	events, err := service.SendMessageStream(context.Background(), ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser}, SendMessageCommand{Content: "做个 HTML 贪吃蛇", IdempotencyKey: "send-1"})
	if err != nil {
		t.Fatal(err)
	}
	var gotError string
	var gotCompleted bool
	for event := range events {
		if event.Kind == StreamEventError {
			gotError = event.Error
		}
		if event.Kind == StreamEventRunCompleted {
			gotCompleted = true
		}
	}
	if gotCompleted {
		t.Fatal("stream should not emit run_completed for incomplete Lead orchestration")
	}
	if !strings.Contains(gotError, ErrOrchestrationIncomplete.Error()) {
		t.Fatalf("stream error = %q, want orchestration incomplete", gotError)
	}
	detail, getErr := service.GetConversation(context.Background(), ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser}, "conv-1")
	if getErr != nil {
		t.Fatal(getErr)
	}
	if detail.Status != "failed" || len(detail.Runs) != 1 || detail.Runs[0].Status != "failed" {
		t.Fatalf("conversation should fail loudly, detail = %+v", detail)
	}
}

func TestServiceSendMessageStreamPreservesLeadAnswerWithWarningWhenMissionHasNoHandoff(t *testing.T) {
	repo := newMemoryRepository(t)
	prepare := ToolCallDTO{
		ToolCallID: "tool-prepare",
		Name:       "mission_prepare",
		Status:     "completed",
		Arguments: map[string]string{
			"title":      "穿墙贪吃蛇小游戏",
			"final_goal": "交付单 HTML 文件穿墙贪吃蛇",
			"root_goal":  "团队目标：交付单 HTML 文件穿墙贪吃蛇",
		},
	}
	runner := &streamingDiscussionAgentRunner{
		boundTools: []DiscussionAgentBoundTool{{ToolID: "mission_prepare"}},
		events: []DiscussionAgentStreamEvent{
			{Kind: DiscussionAgentStreamToolCall, ToolCall: prepare},
			{Kind: DiscussionAgentStreamToolResult, ToolCall: prepare},
			{Kind: DiscussionAgentStreamRunCompleted, Response: DiscussionAgentResponse{
				AssistantMessage: "[@本地用户](mention://user/user-1) 方案确认，现在开工！",
				Status:           "completed",
				RuntimeKind:      "managed",
				ToolCalls:        []ToolCallDTO{prepare},
			}},
		},
	}
	commands := &capturingMissionCommands{
		mission: missioncontrol.MissionDTO{MissionID: "mission-1", Title: "穿墙贪吃蛇小游戏", Status: "active", CreatedAt: testTime},
		task:    missioncontrol.TaskCardDTO{TaskID: "root-mission-1", UpdatedAt: testTime.Add(time.Minute)},
	}
	service := newTestService(ServiceOptions{
		Repository:      repo,
		DiscussionAgent: runner,
		MissionTooling:  NewMissionToolApplier(commands),
		Clock:           fixedClock{now: testTime},
		IDGenerator:     fixedIDGen("conv-1", "msg-user", "run-1", "msg-assistant"),
	})

	events, err := service.SendMessageStream(context.Background(), ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser, DisplayName: "本地用户"}, SendMessageCommand{Content: "对 就 html 就行", IdempotencyKey: "send-1"})
	if err != nil {
		t.Fatal(err)
	}
	var gotError string
	var gotMessageCompleted bool
	var gotRunCompleted bool
	var completedMessage *MessageDTO
	for event := range events {
		if event.Kind == StreamEventError {
			gotError = event.Error
		}
		if event.Kind == StreamEventMessageCompleted {
			gotMessageCompleted = true
			completedMessage = event.Message
		}
		if event.Kind == StreamEventRunCompleted {
			gotRunCompleted = true
		}
	}
	if gotError != "" {
		t.Fatalf("stream error = %q, want successful completion with warning", gotError)
	}
	if !gotMessageCompleted || !gotRunCompleted || completedMessage == nil {
		t.Fatalf("stream should complete with the preserved answer, got message_completed=%v run_completed=%v", gotMessageCompleted, gotRunCompleted)
	}
	if completedMessage.Metadata[discussionOrchestrationWarningMetadataKey] != missionPrepareDirectAnswerWarning {
		t.Fatalf("completed message warning = %+v", completedMessage.Metadata)
	}
	detail, getErr := service.GetConversation(context.Background(), ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser}, "conv-1")
	if getErr != nil {
		t.Fatal(getErr)
	}
	if detail.Status != "completed" || len(detail.Messages) != 2 || len(detail.LinkedMissions) != 1 {
		t.Fatalf("conversation should complete with prepared mission and preserved answer: %+v", detail)
	}
}

func TestServiceSendMessageStreamRejectsUnboundMissionToolCallWithoutApplyingTooling(t *testing.T) {
	repo := newMemoryRepository(t)
	runner := &streamingDiscussionAgentRunner{unboundTools: true, events: []DiscussionAgentStreamEvent{{
		Kind: DiscussionAgentStreamRunCompleted,
		Response: DiscussionAgentResponse{
			AssistantMessage: "I created the Mission.",
			Status:           "completed",
			RuntimeKind:      "managed",
			ToolCalls: []ToolCallDTO{{
				ToolCallID: "tool-brief",
				Name:       "mission_brief_create",
				Status:     "completed",
				Arguments:  map[string]string{"title": "Launch plan", "final_goal": "Coordinate launch"},
			}},
		},
	}}}
	applier := &capturingMissionToolApplier{}
	service := newTestService(ServiceOptions{
		Repository:      repo,
		DiscussionAgent: runner,
		MissionTooling:  applier,
		Clock:           fixedClock{now: testTime},
		IDGenerator:     fixedIDGen("conv-1", "msg-user", "run-1", "event-failed"),
	})

	events, err := service.SendMessageStream(context.Background(), ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser}, SendMessageCommand{Content: "Plan a launch", IdempotencyKey: "send-1"})
	if err != nil {
		t.Fatal(err)
	}
	var gotError string
	for event := range events {
		if event.Kind == StreamEventError {
			gotError = event.Error
		}
	}
	if !strings.Contains(gotError, "unbound tool call mission_brief_create") {
		t.Fatalf("stream error = %q, want unbound tool diagnostic", gotError)
	}
	if applier.called {
		t.Fatalf("unbound tool call should not reach mission tooling: %+v", applier.calls)
	}
}

func TestServiceSendMessageStreamDoesNotPersistAssistantMessageWhenMissionToolApplyFails(t *testing.T) {
	repo := newMemoryRepository(t)
	rootTask := ToolCallDTO{
		ToolCallID: "tool-root",
		Name:       "mission_task_create_root",
		Status:     "completed",
		Arguments:  map[string]string{"assignee_id": "agent-worker", "goal": "Build snake", "idempotency_key": "root"},
	}
	runner := &streamingDiscussionAgentRunner{events: []DiscussionAgentStreamEvent{
		{Kind: DiscussionAgentStreamTokenDelta, Delta: "What deadline should I optimize for?"},
		{Kind: DiscussionAgentStreamToolCall, ToolCall: rootTask},
		{Kind: DiscussionAgentStreamRunCompleted, Response: DiscussionAgentResponse{
			AssistantMessage: "What deadline should I optimize for?",
			Status:           "completed",
			RuntimeKind:      "codex",
			ToolCalls:        []ToolCallDTO{rootTask},
		}},
	}}
	applier := &capturingMissionToolApplier{err: fmt.Errorf("%w: assignee_id must reference an enabled agent profile", missioncontrol.ErrInvalidRequest)}
	service := newTestService(ServiceOptions{
		Repository:      repo,
		DiscussionAgent: runner,
		MissionTooling:  applier,
		Clock:           fixedClock{now: testTime},
		IDGenerator:     fixedIDGen("conv-1", "msg-user", "run-1", "msg-assistant"),
	})

	events, err := service.SendMessageStream(context.Background(), ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser}, SendMessageCommand{Content: "先点一下群里有多少人", IdempotencyKey: "send-1"})
	if err != nil {
		t.Fatal(err)
	}
	var gotError string
	var gotMessageCompleted bool
	var gotRunCompleted bool
	for event := range events {
		if event.Kind == StreamEventError {
			gotError = event.Error
		}
		if event.Kind == StreamEventMessageCompleted {
			gotMessageCompleted = true
		}
		if event.Kind == StreamEventRunCompleted {
			gotRunCompleted = true
		}
	}
	if !strings.Contains(gotError, "assignee_id must reference an enabled agent profile") || !strings.Contains(gotError, "agent-worker") || !strings.Contains(gotError, "人员目录") {
		t.Fatalf("stream error = %q, want assignee diagnostic", gotError)
	}
	if gotMessageCompleted || gotRunCompleted {
		t.Fatalf("stream should fail without message/run completion, got message_completed=%v run_completed=%v", gotMessageCompleted, gotRunCompleted)
	}
	detail, getErr := service.GetConversation(context.Background(), ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser}, "conv-1")
	if getErr != nil {
		t.Fatal(getErr)
	}
	if len(detail.Messages) != 1 || detail.Messages[0].Role != "user" {
		t.Fatalf("assistant message should not persist after tool apply failure: %+v", detail.Messages)
	}
	if len(detail.Runs) != 1 || detail.Runs[0].Status != "failed" {
		t.Fatalf("run should be failed after tool apply failure: %+v", detail.Runs)
	}
	if len(detail.Events) != 1 || detail.Events[0].Kind != "discussion_orchestration_incomplete" || !strings.Contains(detail.Events[0].Summary, "assignee_id must reference") || !strings.Contains(detail.Events[0].Summary, "agent-worker") || !strings.Contains(detail.Events[0].Summary, "人员目录") {
		t.Fatalf("failure event = %+v, want assignee diagnostic event", detail.Events)
	}
}

func TestServiceSendMessageStreamEmitsAssistantTokenDeltas(t *testing.T) {
	repo := newMemoryRepository(t)
	runner := &streamingDiscussionAgentRunner{events: []DiscussionAgentStreamEvent{
		{Kind: DiscussionAgentStreamTokenDelta, Delta: "Which launch date should I optimize for?"},
		{Kind: DiscussionAgentStreamRunCompleted, Response: DiscussionAgentResponse{
			AssistantMessage: "Which launch date should I optimize for?",
			Status:           "needs_clarification",
			RuntimeKind:      "codex",
		}},
	}}
	service := newTestService(ServiceOptions{
		Repository:      repo,
		DiscussionAgent: runner,
		Clock:           fixedClock{now: testTime},
		IDGenerator:     fixedIDGen("conv-1", "msg-user", "run-1", "msg-assistant"),
	})

	events, err := service.SendMessageStream(context.Background(), ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser}, SendMessageCommand{Content: "Plan launch", IdempotencyKey: "send-1"})
	if err != nil {
		t.Fatal(err)
	}
	var gotDelta string
	var completedMessage string
	for event := range events {
		if event.Kind == StreamEventTokenDelta {
			gotDelta += event.Delta
		}
		if event.Kind == StreamEventMessageCompleted && event.Message != nil {
			completedMessage = event.Message.Content
		}
	}

	if gotDelta != "Which launch date should I optimize for?" {
		t.Fatalf("token deltas = %q", gotDelta)
	}
	if completedMessage != "[@本地用户](mention://user/user-1) Which launch date should I optimize for?" {
		t.Fatalf("completed message = %q", completedMessage)
	}
	detail, err := service.GetConversation(context.Background(), ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser}, "conv-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(detail.Messages) != 2 || detail.Messages[1].Content != "[@本地用户](mention://user/user-1) Which launch date should I optimize for?" {
		t.Fatalf("messages = %+v", detail.Messages)
	}
	if len(detail.Runs) != 1 || len(detail.Runs[0].ToolCalls) != 0 {
		t.Fatalf("run tool calls = %+v", detail.Runs)
	}
}

func TestServiceSendMessageStreamPersistsSelectedDiscussionAgentIdentity(t *testing.T) {
	repo := newMemoryRepository(t)
	runner := &streamingDiscussionAgentRunner{
		agentID: "custom-lead-agent",
		events: []DiscussionAgentStreamEvent{{
			Kind: DiscussionAgentStreamRunCompleted,
			Response: DiscussionAgentResponse{
				AssistantMessage: "What deadline?",
				Status:           "completed",
				RuntimeKind:      "codex",
			},
		}},
	}
	service := newTestService(ServiceOptions{
		Repository:          repo,
		DiscussionAgent:     runner,
		InitialAgentMembers: defaultTestConversationAgentMembersFor("custom-lead-agent"),
		Clock:               fixedClock{now: testTime},
		IDGenerator:         fixedIDGen("conv-1", "msg-user", "run-1", "msg-assistant"),
	})
	events, err := service.SendMessageStream(context.Background(), ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser}, SendMessageCommand{Content: "Plan a launch", IdempotencyKey: "send-1"})
	if err != nil {
		t.Fatal(err)
	}
	var runStarted *RunDTO
	for event := range events {
		if event.Kind == StreamEventRunStarted {
			runStarted = event.Run
		}
	}
	if runStarted == nil || runStarted.AgentID != "custom-lead-agent" {
		t.Fatalf("run started = %+v, want selected discussion agent", runStarted)
	}
	detail, err := service.GetConversation(context.Background(), ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser}, "conv-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(detail.Runs) != 1 || detail.Runs[0].AgentID != "custom-lead-agent" {
		t.Fatalf("runs = %+v, want selected discussion agent", detail.Runs)
	}
	if len(detail.Messages) != 2 || detail.Messages[1].AuthorID != "custom-lead-agent" {
		t.Fatalf("messages = %+v, want assistant authored by selected discussion agent", detail.Messages)
	}
}

func TestServiceSendMessageStreamRequiresRunnerIdentityInsteadOfConfiguredFirstMember(t *testing.T) {
	repo := newMemoryRepository(t)
	runner := &anonymousStreamingDiscussionAgentRunner{events: []DiscussionAgentStreamEvent{{
		Kind: DiscussionAgentStreamRunCompleted,
		Response: DiscussionAgentResponse{
			AssistantMessage: "收到",
			Status:           "completed",
			RuntimeKind:      "codex",
		},
	}}}
	service := newTestService(ServiceOptions{
		Repository:      repo,
		DiscussionAgent: runner,
		InitialAgentMembers: []InitialAgentMemberSeed{{
			AgentID: "coordinator-agent",
			Role:    "Coordinator",
		}},
		Clock:       fixedClock{now: testTime},
		IDGenerator: fixedIDGen("conv-1", "msg-user", "run-1", "msg-assistant"),
	})

	_, err := service.SendMessageStream(context.Background(), ViewerRef{ViewerID: "local-user", ViewerKind: ViewerKindUser, DisplayName: "本地用户"}, SendMessageCommand{Content: "启动项目", IdempotencyKey: "send-configured-run-agent"})
	if !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("SendMessageStream error = %v, want ErrInvalidRequest", err)
	}
	if runner.request.AgentID != "" {
		t.Fatalf("runner should not execute without stable identity, request = %+v", runner.request)
	}
	if _, getErr := repo.GetConversation(context.Background(), "conv-1"); !errors.Is(getErr, ErrNotFound) {
		t.Fatalf("conversation should not be saved without runner identity, get error = %v", getErr)
	}
}

func TestServiceSendMessageStreamReusesExistingMissionLinkForLaterTurns(t *testing.T) {
	repo := newMemoryRepository(t)
	if err := repo.SaveConversation(context.Background(), ConversationRecord{ConversationID: "conv-1", Title: "Launch plan", Status: "completed", CreatedBy: "user-1", CreatedAt: testTime, UpdatedAt: testTime}); err != nil {
		t.Fatal(err)
	}
	seedDiscussionMembers(t, repo, "conv-1", "user-1")
	if err := repo.SaveMissionLink(context.Background(), LinkedMissionRecord{ConversationID: "conv-1", MissionID: "mission-existing", Title: "Launch plan", Status: "active", CreatedAt: testTime}); err != nil {
		t.Fatal(err)
	}
	duplicateBrief := ToolCallDTO{
		ToolCallID: "tool-brief-duplicate",
		Name:       "mission_brief_create",
		Status:     "completed",
		Arguments:  map[string]string{"title": "Launch plan again", "final_goal": "Do not create another mission"},
	}
	rootTask := ToolCallDTO{
		ToolCallID: "tool-root",
		Name:       "mission_task_create_root",
		Status:     "completed",
		Arguments:  map[string]string{"goal": "Continue launch work", "worker_personal_goal": "Continue existing mission", "assignee_id": "agent-worker", "idempotency_key": "root-task-later-turn"},
	}
	coordinatorPlan := ToolCallDTO{
		ToolCallID: "tool-coordinator",
		Name:       "mission_coordinator_plan_parallel",
		Status:     "completed",
		Arguments:  map[string]string{"goal": "Continue launch work", "worker_lanes_json": `[{"role":"worker","agent_id":"agent-worker","goal":"Continue launch","idempotency_key":"lane-later"}]`, "idempotency_key": "coordinator-later-turn"},
	}
	runner := &streamingDiscussionAgentRunner{events: []DiscussionAgentStreamEvent{
		{Kind: DiscussionAgentStreamToolCall, ToolCall: duplicateBrief},
		{Kind: DiscussionAgentStreamToolResult, ToolCall: duplicateBrief},
		{Kind: DiscussionAgentStreamToolCall, ToolCall: rootTask},
		{Kind: DiscussionAgentStreamToolResult, ToolCall: rootTask},
		{Kind: DiscussionAgentStreamToolCall, ToolCall: coordinatorPlan},
		{Kind: DiscussionAgentStreamToolResult, ToolCall: coordinatorPlan},
		{Kind: DiscussionAgentStreamRunCompleted, Response: DiscussionAgentResponse{
			AssistantMessage: "Continuing the existing Mission.",
			Status:           "completed",
			RuntimeKind:      "codex",
			ToolCalls:        []ToolCallDTO{duplicateBrief, rootTask, coordinatorPlan},
		}},
	}}
	applier := &capturingMissionCommands{
		updatedMission: missioncontrol.MissionDTO{MissionID: "mission-existing", Title: "Launch plan again", Status: "active", CreatedAt: testTime},
		task:           missioncontrol.TaskCardDTO{TaskID: "task-root", UpdatedAt: testTime.Add(time.Minute)},
	}
	service := newTestService(ServiceOptions{
		Repository:      repo,
		DiscussionAgent: runner,
		MissionTooling:  NewMissionToolApplier(applier),
		Clock:           fixedClock{now: testTime.Add(time.Minute)},
		IDGenerator:     fixedIDGen("msg-user-2", "run-2", "msg-assistant-2"),
	})

	events, err := service.SendMessageStream(context.Background(), ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser}, SendMessageCommand{ConversationID: "conv-1", Content: "继续推进", IdempotencyKey: "send-2"})
	if err != nil {
		t.Fatal(err)
	}
	var streamedToolNames []string
	for event := range events {
		if (event.Kind == StreamEventToolCall || event.Kind == StreamEventToolResult) && event.ToolCall != nil {
			streamedToolNames = append(streamedToolNames, event.ToolCall.Name)
		}
	}

	if applier.createCalled {
		t.Fatalf("duplicate Lead brief create should update existing mission, not create another one: %+v", applier.createCmd)
	}
	if !applier.updateCalled || applier.updateCmd.MissionID.String() != "mission-existing" {
		t.Fatalf("duplicate Lead brief create should update existing mission, got %+v", applier.updateCmd)
	}
	if !applier.createTaskCalled || applier.createTaskCmd.MissionID.String() != "mission-existing" {
		t.Fatalf("root task should use existing mission link, create task command = %+v", applier.createTaskCmd)
	}
	wantStreamedToolNames := []string{"mission_brief_create", "mission_brief_create", "mission_task_create_root", "mission_task_create_root", "mission_coordinator_plan_parallel", "mission_coordinator_plan_parallel"}
	if strings.Join(streamedToolNames, ",") != strings.Join(wantStreamedToolNames, ",") {
		t.Fatalf("streamed tool statuses = %v, want %v", streamedToolNames, wantStreamedToolNames)
	}
	detail, err := service.GetConversation(context.Background(), ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser}, "conv-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(detail.LinkedMissions) != 1 || detail.LinkedMissions[0].MissionID != "mission-existing" {
		t.Fatalf("linked missions = %+v", detail.LinkedMissions)
	}
	if len(detail.Runs) != 1 || len(detail.Runs[0].ToolCalls) != 3 || detail.Runs[0].ToolCalls[0].Name != "mission_brief_create" || detail.Runs[0].ToolCalls[0].Result["operation"] != "updated" || detail.Runs[0].ToolCalls[1].Name != "mission_task_create_root" || detail.Runs[0].ToolCalls[2].Name != "mission_coordinator_plan_parallel" {
		t.Fatalf("later run should show mission brief update status, runs = %+v", detail.Runs)
	}
	if len(runner.request.LinkedMissions) != 1 || runner.request.LinkedMissions[0].MissionID != "mission-existing" {
		t.Fatalf("discussion agent request linked missions = %+v", runner.request.LinkedMissions)
	}
}

func TestServiceSendMessageStreamMarksFailedWhenRuntimeEndsWithoutCompletion(t *testing.T) {
	repo := newMemoryRepository(t)
	runner := &streamingDiscussionAgentRunner{events: []DiscussionAgentStreamEvent{
		{Kind: DiscussionAgentStreamTokenDelta, Delta: "partial"},
	}}
	service := newTestService(ServiceOptions{
		Repository:      repo,
		DiscussionAgent: runner,
		Clock:           fixedClock{now: testTime},
		IDGenerator:     fixedIDGen("conv-1", "msg-user", "run-1"),
	})

	events, err := service.SendMessageStream(context.Background(), ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser}, SendMessageCommand{Content: "Plan a launch", IdempotencyKey: "send-1"})
	if err != nil {
		t.Fatal(err)
	}
	gotKinds := []string{}
	var gotError string
	for event := range events {
		gotKinds = append(gotKinds, event.Kind)
		if event.Kind == StreamEventError {
			gotError = event.Error
		}
	}
	wantKinds := []string{StreamEventSnapshot, StreamEventTurnStarted, StreamEventRunStarted, StreamEventTokenDelta, StreamEventMessageCompleted, StreamEventRunFailed, StreamEventTurnFailed, StreamEventSnapshot, StreamEventError}
	if strings.Join(gotKinds, ",") != strings.Join(wantKinds, ",") {
		t.Fatalf("stream kinds = %v, want %v", gotKinds, wantKinds)
	}
	if !strings.Contains(gotError, "ended before completion") {
		t.Fatalf("error = %q", gotError)
	}
	detail, err := service.GetConversation(context.Background(), ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser}, "conv-1")
	if err != nil {
		t.Fatal(err)
	}
	if detail.Status != "failed" || len(detail.Runs) != 1 || detail.Runs[0].Status != "failed" {
		t.Fatalf("conversation detail = %+v", detail)
	}
	if len(detail.Messages) != 2 || detail.Messages[1].Content != discussionPartialMessageFallback || detail.Messages[1].RunID != "run-1" || detail.Messages[1].Metadata[discussionPartialMessageMetadataKey] != "true" {
		t.Fatalf("partial assistant message = %+v", detail.Messages)
	}
	command, err := repo.GetCommand(context.Background(), "send_message", ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser}, "send-1")
	if err != nil || command.Status != discussionCommandFailed {
		t.Fatalf("runtime-failed command = %+v, err=%v; it must remain retryable", command, err)
	}
}

func TestServiceSubscribeConversationStreamReceivesLiveLeadRunEvents(t *testing.T) {
	repo := newMemoryRepository(t)
	if err := repo.SaveConversation(context.Background(), ConversationRecord{ConversationID: "conv-1", Title: "Launch plan", Status: "running", CreatedBy: "user-1", CreatedAt: testTime, UpdatedAt: testTime}); err != nil {
		t.Fatal(err)
	}
	seedDiscussionMembers(t, repo, "conv-1", "user-1")
	runner := &streamingDiscussionAgentRunner{events: []DiscussionAgentStreamEvent{
		{Kind: DiscussionAgentStreamTokenDelta, Delta: "What deadline?"},
		{Kind: DiscussionAgentStreamRunCompleted, Response: DiscussionAgentResponse{
			AssistantMessage: "What deadline?",
			Status:           "completed",
			RuntimeKind:      "codex",
		}},
	}}
	service := newTestService(ServiceOptions{
		Repository:      repo,
		DiscussionAgent: runner,
		LegacyStreamHub: true,
		Clock:           fixedClock{now: testTime},
		IDGenerator:     fixedIDGen("msg-user", "run-1", "msg-assistant"),
	})
	viewer := ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	subscription, err := service.SubscribeConversationStream(ctx, viewer, "conv-1")
	if err != nil {
		t.Fatal(err)
	}
	snapshot := readLeadStreamEvent(t, subscription)
	if snapshot.Kind != StreamEventSnapshot || snapshot.Conversation == nil || snapshot.Conversation.ConversationID != "conv-1" {
		t.Fatalf("snapshot = %+v", snapshot)
	}

	sendEvents, err := service.SendMessageStream(ctx, viewer, SendMessageCommand{ConversationID: "conv-1", Content: "Plan launch", IdempotencyKey: "send-1"})
	if err != nil {
		t.Fatal(err)
	}
	go drainLeadStream(sendEvents)

	gotKinds := readLeadStreamKindsUntilSnapshotAfterRunCompleted(t, subscription)
	wantKinds := []string{StreamEventSnapshot, StreamEventTurnStarted, StreamEventRunStarted, StreamEventTokenDelta, StreamEventMessageCompleted, StreamEventRunCompleted, StreamEventTurnCompleted, StreamEventSnapshot}
	if strings.Join(gotKinds, ",") != strings.Join(wantKinds, ",") {
		t.Fatalf("subscription stream kinds = %v, want %v", gotKinds, wantKinds)
	}
}

func TestServiceSubscribeConversationStreamRequiresLegacyHub(t *testing.T) {
	repo := newMemoryRepository(t)
	if err := repo.SaveConversation(context.Background(), ConversationRecord{ConversationID: "conv-1", Title: "Launch plan", Status: "running", CreatedBy: "user-1", CreatedAt: testTime, UpdatedAt: testTime}); err != nil {
		t.Fatal(err)
	}
	seedDiscussionMembers(t, repo, "conv-1", "user-1")
	service := newTestService(ServiceOptions{Repository: repo})

	events, err := service.SubscribeConversationStream(context.Background(), ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser}, "conv-1")

	if !errors.Is(err, ErrServiceDependency) {
		t.Fatalf("err = %v, want ErrServiceDependency", err)
	}
	if events != nil {
		t.Fatalf("events = %v, want nil", events)
	}
}
