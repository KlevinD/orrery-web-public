package discussionsession

import (
	"context"
	"strings"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	missioncontrol "orrery_v2/internal/app/missioncontrol"
)

func TestServiceSendMessageStreamEmitsCascadedMentionMessagesAsTheyComplete(t *testing.T) {
	repo := newMemoryRepository(t)
	runner := &streamingDiscussionAgentRunner{events: []DiscussionAgentStreamEvent{{
		Kind: DiscussionAgentStreamRunCompleted,
		Response: DiscussionAgentResponse{
			AssistantMessage: "[@秦若兰](mention://agent/staffing-agent) 请帮忙拉两个人进来。",
			Status:           "completed",
			RuntimeKind:      "managed",
		},
	}}}
	responder := &sequenceDiscussionAgentMentionResponder{responses: []DiscussionAgentResponse{
		{
			AssistantMessage: "[@顾明澈](mention://agent/lead-agent) 已经安排好，我拉了两位同事。",
			Status:           "completed",
			RuntimeKind:      "managed",
		},
		{
			AssistantMessage: "[@本地用户](mention://user/user-1) Staffing 已处理，我继续跟进。",
			Status:           "completed",
			RuntimeKind:      "managed",
		},
	}}
	service := newTestService(ServiceOptions{
		Repository:       repo,
		DiscussionAgent:  runner,
		MentionResponder: responder,
		Clock:            fixedClock{now: testTime},
		IDGenerator:      fixedIDGen("conv-1", "msg-user", "run-1", "msg-assistant", "run-staffing", "msg-staffing", "run-lead-followup", "msg-lead-followup"),
	})

	events, err := service.SendMessageStream(context.Background(), ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser, DisplayName: "本地用户"}, SendMessageCommand{Content: "@一下Staffing 再随便拉两个人进来", IdempotencyKey: "send-1"})
	if err != nil {
		t.Fatal(err)
	}
	var completed []string
	var sawLeadRunCompleted bool
	var sawMentionRunStarted bool
	for event := range events {
		if event.Kind == StreamEventMessageCompleted && event.Message != nil {
			completed = append(completed, event.Message.AuthorID)
			if event.Message.AuthorID != "lead-agent" && !sawLeadRunCompleted {
				t.Fatalf("mention message_completed before Lead run_completed: author=%s completed=%v", event.Message.AuthorID, completed)
			}
		}
		if event.Kind == StreamEventRunCompleted && event.Run != nil && event.Run.RunID == "run-1" {
			sawLeadRunCompleted = true
		}
		if event.Kind == StreamEventRunStarted && event.Run != nil && event.Run.RunID != "run-1" {
			sawMentionRunStarted = true
			if !sawLeadRunCompleted {
				t.Fatalf("mention run_started before Lead run_completed: %+v", event.Run)
			}
		}
	}

	if strings.Join(completed, ",") != "lead-agent,staffing-agent,lead-agent" {
		t.Fatalf("message completion author order = %v", completed)
	}
	if !sawLeadRunCompleted || !sawMentionRunStarted {
		t.Fatalf("missing Lead completion or mention run start: leadCompleted=%v mentionStarted=%v", sawLeadRunCompleted, sawMentionRunStarted)
	}
}

func TestServiceSendMessageStreamEmitsMentionRunLifecycleEvents(t *testing.T) {
	repo := newMemoryRepository(t)
	runner := &streamingDiscussionAgentRunner{events: []DiscussionAgentStreamEvent{{
		Kind: DiscussionAgentStreamRunCompleted,
		Response: DiscussionAgentResponse{
			AssistantMessage: "[@秦若兰](mention://agent/staffing-agent) 请确认一下。",
			Status:           "completed",
			RuntimeKind:      "managed",
		},
	}}}
	responder := &capturingDiscussionAgentMentionResponder{response: DiscussionAgentResponse{
		AssistantMessage: "收到。",
		Status:           "completed",
		RuntimeKind:      "managed",
	}}
	service := newTestService(ServiceOptions{
		Repository:       repo,
		DiscussionAgent:  runner,
		MentionResponder: responder,
		Clock:            fixedClock{now: testTime},
		IDGenerator:      fixedIDGen("conv-1", "msg-user", "run-1", "msg-assistant", "run-staffing", "msg-staffing"),
	})

	events, err := service.SendMessageStream(context.Background(), ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser, DisplayName: "本地用户"}, SendMessageCommand{Content: "叫一下 Staffing", IdempotencyKey: "send-1"})
	if err != nil {
		t.Fatal(err)
	}
	var kinds []string
	for event := range events {
		switch event.Kind {
		case StreamEventRunStarted:
			if event.Run != nil && event.Run.RunID == "run-staffing" {
				kinds = append(kinds, "staffing_started")
			}
		case StreamEventMessageCompleted:
			if event.Message != nil {
				kinds = append(kinds, "message:"+event.Message.AuthorID)
			}
		case StreamEventRunCompleted:
			if event.Run != nil {
				kinds = append(kinds, "run_completed:"+event.Run.RunID)
			}
		}
	}

	want := []string{"message:lead-agent", "run_completed:run-1", "staffing_started", "message:staffing-agent", "run_completed:run-staffing"}
	if strings.Join(kinds, ",") != strings.Join(want, ",") {
		t.Fatalf("mention stream lifecycle = %v, want %v", kinds, want)
	}
}

func TestServiceSendMessageStreamEmitsMentionToolProgressBeforeMentionMessage(t *testing.T) {
	repo := newMemoryRepository(t)
	runner := &streamingDiscussionAgentRunner{events: []DiscussionAgentStreamEvent{{
		Kind: DiscussionAgentStreamRunCompleted,
		Response: DiscussionAgentResponse{
			AssistantMessage: "[@秦若兰](mention://agent/staffing-agent) 请确认一下。",
			Status:           "completed",
			RuntimeKind:      "managed",
		},
	}}}
	responder := &streamingDiscussionAgentMentionResponder{events: []DiscussionAgentStreamEvent{
		{Kind: DiscussionAgentStreamToolCall, ToolCall: ToolCallDTO{ToolCallID: "talent-list-1", Name: "discussion_talent_list", Status: "running"}},
		{Kind: DiscussionAgentStreamToolResult, ToolCall: ToolCallDTO{ToolCallID: "talent-list-1", Name: "discussion_talent_list", Status: "completed", Result: map[string]string{"agents": "staffing-agent"}}},
		{Kind: DiscussionAgentStreamRunCompleted, Response: DiscussionAgentResponse{
			AssistantMessage:  "我确认过成员列表了。",
			Status:            "completed",
			RuntimeKind:       "codex",
			ToolLoopCompleted: true,
			ToolCalls: []ToolCallDTO{{
				ToolCallID: "talent-list-1",
				Name:       "discussion_talent_list",
				Status:     "completed",
				Result:     map[string]string{"agents": "staffing-agent"},
			}},
		}},
	}}
	service := newTestService(ServiceOptions{
		Repository:       repo,
		DiscussionAgent:  runner,
		MentionResponder: responder,
		Clock:            fixedClock{now: testTime},
		IDGenerator:      fixedIDGen("conv-1", "msg-user", "run-1", "msg-assistant", "run-staffing", "msg-staffing"),
	})

	events, err := service.SendMessageStream(context.Background(), ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser, DisplayName: "本地用户"}, SendMessageCommand{Content: "叫一下 Staffing", IdempotencyKey: "send-1"})
	if err != nil {
		t.Fatal(err)
	}
	var order []string
	for event := range events {
		switch event.Kind {
		case StreamEventToolCall:
			if event.ToolCall != nil && event.ToolCall.ToolCallID == "talent-list-1" {
				if event.RunID != "run-staffing" {
					t.Fatalf("tool_call run_id = %q, want run-staffing", event.RunID)
				}
				order = append(order, "tool_call")
			}
		case StreamEventToolResult:
			if event.ToolCall != nil && event.ToolCall.ToolCallID == "talent-list-1" {
				if event.RunID != "run-staffing" {
					t.Fatalf("tool_result run_id = %q, want run-staffing", event.RunID)
				}
				order = append(order, "tool_result")
			}
		case StreamEventMessageCompleted:
			if event.Message != nil && event.Message.AuthorID == "staffing-agent" {
				order = append(order, "message")
			}
		}
	}

	if !responder.streamCalled {
		t.Fatalf("mention responder stream path was not used")
	}
	if strings.Join(order, ",") != "tool_call,tool_result,message" {
		t.Fatalf("mention stream order = %v, want tool progress before message", order)
	}
}

func TestServiceSendMessageStreamEmitsMentionPlanProgressBeforeMentionMessage(t *testing.T) {
	repo := newMemoryRepository(t)
	runner := &streamingDiscussionAgentRunner{events: []DiscussionAgentStreamEvent{{
		Kind: DiscussionAgentStreamRunCompleted,
		Response: DiscussionAgentResponse{
			AssistantMessage: "[@秦若兰](mention://agent/staffing-agent) 请处理这个 plan。",
			Status:           "completed",
			RuntimeKind:      "managed",
		},
	}}}
	responder := &streamingDiscussionAgentMentionResponder{events: []DiscussionAgentStreamEvent{
		{Kind: DiscussionAgentStreamPlan, Plan: &agentcore.RuntimePlanState{
			PlanID: "plan-staffing",
			Status: "active",
			Items: []agentcore.RuntimePlanItem{
				{ID: "step-1", Text: "Check staffing context", Status: "in_progress"},
			},
		}},
		{Kind: DiscussionAgentStreamRunCompleted, Response: DiscussionAgentResponse{
			AssistantMessage: "Completed 1 plan step(s).",
			Status:           "completed",
			RuntimeKind:      "pi",
		}},
	}}
	service := newTestService(ServiceOptions{
		Repository:       repo,
		DiscussionAgent:  runner,
		MentionResponder: responder,
		Clock:            fixedClock{now: testTime},
		IDGenerator:      fixedIDGen("conv-1", "msg-user", "run-1", "msg-assistant", "run-staffing", "msg-staffing"),
	})

	events, err := service.SendMessageStream(context.Background(), ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser, DisplayName: "本地用户"}, SendMessageCommand{Content: "叫一下 Staffing", IdempotencyKey: "send-1"})
	if err != nil {
		t.Fatal(err)
	}
	var order []string
	var plan *agentcore.RuntimePlanState
	for event := range events {
		switch event.Kind {
		case StreamEventPlan:
			if event.RunID != "run-staffing" {
				t.Fatalf("plan run_id = %q, want run-staffing", event.RunID)
			}
			plan = event.Plan
			order = append(order, "plan")
		case StreamEventMessageCompleted:
			if event.Message != nil && event.Message.AuthorID == "staffing-agent" {
				order = append(order, "message")
			}
		}
	}

	if !responder.streamCalled {
		t.Fatalf("mention responder stream path was not used")
	}
	if plan == nil || plan.PlanID != "plan-staffing" || plan.Items[0].Status != "in_progress" {
		t.Fatalf("plan payload = %+v", plan)
	}
	if strings.Join(order, ",") != "plan,message" {
		t.Fatalf("mention stream order = %v, want plan progress before message", order)
	}
}

func TestServiceSendMessageStreamEmitsSnapshotAfterMentionAddsMembers(t *testing.T) {
	repo := newMemoryRepository(t)
	runner := &streamingDiscussionAgentRunner{events: []DiscussionAgentStreamEvent{{
		Kind: DiscussionAgentStreamRunCompleted,
		Response: DiscussionAgentResponse{
			AssistantMessage: "[@秦若兰](mention://agent/staffing-agent) 请拉两位同事进群。",
			Status:           "completed",
			RuntimeKind:      "managed",
		},
	}}}
	responder := &capturingDiscussionAgentMentionResponder{response: DiscussionAgentResponse{
		AssistantMessage: "已拉两位同事进来。",
		Status:           "completed",
		RuntimeKind:      "managed",
		ToolCalls: []ToolCallDTO{{
			ToolCallID: "add-members",
			Name:       "discussion_member_add",
			Status:     "completed",
			Arguments: map[string]string{
				"agent_ids": "agent-frontend,agent-tester",
			},
		}},
	}}
	service := newTestService(ServiceOptions{
		Repository:       repo,
		DiscussionAgent:  runner,
		MentionResponder: responder,
		AgentDirectory: &testAgentDirectory{entries: append(defaultTestAgentDirectory().(*testAgentDirectory).entries,
			agentcore.AgentDirectoryEntry{AgentProfileID: "agent-frontend", AgentInstanceID: "agent-frontend", DisplayName: "林前端", RealName: "林前端", RoleName: "Frontend", AgentKind: agentcore.AgentKindAI, Enabled: true},
			agentcore.AgentDirectoryEntry{AgentProfileID: "agent-tester", AgentInstanceID: "agent-tester", DisplayName: "周测试", RealName: "周测试", RoleName: "QA", AgentKind: agentcore.AgentKindAI, Enabled: true},
		)},
		Clock:       fixedClock{now: testTime},
		IDGenerator: fixedIDGen("conv-1", "msg-user", "run-1", "msg-assistant", "run-staffing", "msg-staffing"),
	})

	events, err := service.SendMessageStream(context.Background(), ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser, DisplayName: "本地用户"}, SendMessageCommand{Content: "叫 Staffing 拉人", IdempotencyKey: "send-1"})
	if err != nil {
		t.Fatal(err)
	}
	var latestSnapshot *ConversationDTO
	for event := range events {
		if event.Kind == StreamEventSnapshot && event.Conversation != nil {
			latestSnapshot = event.Conversation
		}
	}
	if latestSnapshot == nil {
		t.Fatal("stream did not emit a final snapshot")
	}
	namesByID := map[string]string{}
	for _, member := range latestSnapshot.Members {
		namesByID[member.ID] = member.DisplayName
	}
	if namesByID["agent-frontend"] != "林前端" || namesByID["agent-tester"] != "周测试" {
		t.Fatalf("latest snapshot members = %+v", latestSnapshot.Members)
	}
}

func TestServiceSendMessageStreamEmitsHumanAuthoredMentionReplies(t *testing.T) {
	runner := &streamingDiscussionAgentRunner{events: []DiscussionAgentStreamEvent{{
		Kind: DiscussionAgentStreamRunCompleted,
		Response: DiscussionAgentResponse{
			AssistantMessage: "[@本地用户](mention://user/user-1) 我看到了，先等 Staffing 处理。",
			Status:           "completed",
			RuntimeKind:      "managed",
		},
	}}}
	responder := &capturingDiscussionAgentMentionResponder{response: DiscussionAgentResponse{
		AssistantMessage: "[@本地用户](mention://user/user-1) 收到，我来拉人。",
		Status:           "completed",
		RuntimeKind:      "managed",
	}}
	service := newTestService(ServiceOptions{
		Repository:       newMemoryRepository(t),
		DiscussionAgent:  runner,
		MentionResponder: responder,
		Clock:            fixedClock{now: testTime},
		IDGenerator:      fixedIDGen("conv-1", "msg-user", "run-staffing", "msg-staffing"),
	})

	events, err := service.SendMessageStream(context.Background(), ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser, DisplayName: "本地用户"}, SendMessageCommand{
		Content: "@秦若兰 请随便拉两个人进来", Mentions: []MessageMentionDTO{{TargetKind: "agent", TargetID: "staffing-agent", DisplayName: "秦若兰"}}, IdempotencyKey: "send-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	var completed []string
	for event := range events {
		if event.Kind == StreamEventMessageCompleted && event.Message != nil {
			completed = append(completed, event.Message.AuthorID)
		}
	}

	if strings.TrimSpace(runner.request.RunID) != "" {
		t.Fatalf("direct human-authored agent mention should not start Lead stream run: %+v", runner.request)
	}
	if !responder.called || responder.request.SourceMessage.MessageID != "msg-user" || responder.request.TargetAgent.ID != "staffing-agent" {
		t.Fatalf("mention responder request = %+v", responder.request)
	}
	if strings.Join(completed, ",") != "staffing-agent" {
		t.Fatalf("message completion author order = %v", completed)
	}
}

func TestServiceSendMessageAppliesMissionToolResultsAsSelectedDiscussionAgent(t *testing.T) {
	repo := newMemoryRepository(t)
	runner := &capturingDiscussionAgentRunner{
		agentID: "custom-lead-agent",
		response: DiscussionAgentResponse{
			AssistantMessage: "I created the Mission.",
			Status:           "completed",
			RuntimeKind:      "codex",
			ToolCalls: []ToolCallDTO{{
				ToolCallID: "tool-1",
				Name:       "mission_brief_create",
				Status:     "completed",
			}},
		},
	}
	applier := &capturingMissionToolApplier{}
	service := newTestService(ServiceOptions{
		Repository:          repo,
		DiscussionAgent:     runner,
		InitialAgentMembers: defaultTestConversationAgentMembersFor("custom-lead-agent"),
		MissionTooling:      applier,
		Clock:               fixedClock{now: testTime},
		IDGenerator:         fixedIDGen("conv-1", "msg-user", "run-1", "msg-assistant"),
	})

	if _, err := service.SendMessage(context.Background(), ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser}, SendMessageCommand{Content: "Plan a launch", IdempotencyKey: "send-1"}); err != nil {
		t.Fatal(err)
	}

	if applier.viewer.ViewerID != "custom-lead-agent" || applier.viewer.ViewerKind != missioncontrol.ViewerKindAgent {
		t.Fatalf("applier viewer = %+v, want selected discussion agent", applier.viewer)
	}
}

func TestServiceGetLatestRunDebugReturnsRawPromptAndResponse(t *testing.T) {
	repo := newMemoryRepository(t)
	runner := &capturingDiscussionAgentRunner{
		response: DiscussionAgentResponse{
			AssistantMessage: "Which deadline?",
			Status:           "completed",
			RuntimeKind:      "codex",
			OutboundPrompt:   "runtime prompt token=secret-outbound",
			RuntimeResponse:  "runtime response password=secret-response",
			ProviderMetadata: map[string]string{"pi_debug_runtime_input_path": "D:/runs/run-1/pi-debug/runtime-input.json"},
		},
		debugPrompt: &DiscussionAgentPromptDebug{
			OutboundPrompt: "initial outbound token=secret-initial",
			Selected: RunDebugSelectedDTO{
				AgentID:     "lead-agent",
				RuntimeKind: "codex",
				Model:       "gpt-5-codex",
				ConfigRef:   "runtime-ref",
			},
			Redacted: false,
		},
	}
	service := newTestService(ServiceOptions{
		Repository:      repo,
		DiscussionAgent: runner,
		Clock:           fixedClock{now: testTime},
		IDGenerator:     fixedIDGen("conv-1", "msg-user", "run-1", "msg-assistant"),
	})
	viewer := ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser}

	if _, err := service.SendMessage(context.Background(), viewer, SendMessageCommand{Content: "Plan a launch", IdempotencyKey: "send-1"}); err != nil {
		t.Fatal(err)
	}
	debug, err := service.GetLatestRunDebug(context.Background(), viewer, "conv-1")
	if err != nil {
		t.Fatal(err)
	}

	if debug.RunID != "run-1" || debug.ConversationID != "conv-1" || debug.Selected.AgentID != "lead-agent" || debug.Selected.Model != "gpt-5-codex" {
		t.Fatalf("debug metadata = %+v", debug)
	}
	if debug.Redacted {
		t.Fatalf("debug should keep raw prompt and response, got redacted flag")
	}
	if !strings.Contains(debug.OutboundPrompt, "secret-outbound") ||
		!strings.Contains(debug.RuntimeResponse, "secret-response") {
		t.Fatalf("debug should keep raw values: %+v", debug)
	}
	if debug.ProviderMetadata["pi_debug_runtime_input_path"] != "D:/runs/run-1/pi-debug/runtime-input.json" {
		t.Fatalf("debug provider metadata = %+v", debug.ProviderMetadata)
	}
}

func TestServiceGetLatestRunDebugExistsWhenRunnerHasNoPromptDebugger(t *testing.T) {
	repo := newMemoryRepository(t)
	runner := &anonymousIdentityDiscussionAgentRunner{anonymousDiscussionAgentRunner: anonymousDiscussionAgentRunner{response: DiscussionAgentResponse{
		AssistantMessage: "Which deadline?",
		Status:           "completed",
		RuntimeKind:      "codex",
		RuntimeResponse:  "runtime response from anonymous runner",
	}}}
	service := newTestService(ServiceOptions{
		Repository:      repo,
		DiscussionAgent: runner,
		Clock:           fixedClock{now: testTime},
		IDGenerator:     fixedIDGen("conv-1", "msg-user", "run-1", "msg-assistant"),
	})
	viewer := ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser}

	if _, err := service.SendMessage(context.Background(), viewer, SendMessageCommand{Content: "Plan a launch", IdempotencyKey: "send-1"}); err != nil {
		t.Fatal(err)
	}
	debug, err := service.GetLatestRunDebug(context.Background(), viewer, "conv-1")
	if err != nil {
		t.Fatal(err)
	}

	if debug.RunID != "run-1" || debug.ConversationID != "conv-1" || debug.Selected.AgentID != "lead-agent" || debug.Selected.RuntimeKind != "codex" {
		t.Fatalf("debug metadata = %+v", debug)
	}
	if !strings.Contains(debug.RuntimeResponse, "runtime response from anonymous runner") {
		t.Fatalf("debug runtime response = %q", debug.RuntimeResponse)
	}
}

func TestServiceGetLatestRunDebugExistsForDirectAgentConversation(t *testing.T) {
	repo := newMemoryRepository(t)
	responder := &capturingDiscussionAgentMentionResponder{response: DiscussionAgentResponse{
		AssistantMessage: "我来单独处理。",
		Status:           "completed",
		RuntimeKind:      "managed",
		RuntimeResponse:  "direct runtime response",
	}}
	service := newTestService(ServiceOptions{
		Repository:       repo,
		DiscussionAgent:  &capturingDiscussionAgentRunner{agentID: "lead-agent", response: DiscussionAgentResponse{AssistantMessage: "lead should not run"}},
		MentionResponder: responder,
		Clock:            fixedClock{now: testTime},
		IDGenerator:      fixedIDGen("conv-1", "msg-user", "run-direct", "msg-direct"),
	})
	viewer := ViewerRef{ViewerID: "local-user", ViewerKind: ViewerKindUser, DisplayName: "本地用户"}

	if _, err := service.SendMessage(context.Background(), viewer, SendMessageCommand{
		Content:        "这个实现你单独看下",
		DirectAgentID:  "staffing-agent",
		DirectTargetID: "staffing-agent",
		IdempotencyKey: "direct-staffing",
	}); err != nil {
		t.Fatal(err)
	}
	debug, err := service.GetLatestRunDebug(context.Background(), viewer, "conv-1")
	if err != nil {
		t.Fatal(err)
	}

	if debug.RunID != "run-direct" || debug.Selected.AgentID != "staffing-agent" || debug.Selected.RuntimeKind != "managed" {
		t.Fatalf("direct debug metadata = %+v", debug)
	}
	if !strings.Contains(debug.RuntimeResponse, "direct runtime response") {
		t.Fatalf("direct debug runtime response = %q", debug.RuntimeResponse)
	}
}

func TestServiceGetRunDebugReturnsRequestedRunInsteadOfLatest(t *testing.T) {
	repo := newMemoryRepository(t)
	now := testTime
	if err := repo.SaveConversation(context.Background(), ConversationRecord{ConversationID: "conv-1", Title: "Launch", Status: "completed", CreatedBy: "user-1", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	earlierDebug := RunDebugDTO{ConversationID: "conv-1", RunID: "run-earlier", OutboundPrompt: "earlier outbound", RuntimeResponse: "earlier response", CreatedAt: now, Redacted: false}
	latestDebug := RunDebugDTO{ConversationID: "conv-1", RunID: "run-latest", OutboundPrompt: "latest outbound", RuntimeResponse: "latest response", CreatedAt: now.Add(time.Second), Redacted: false}
	if err := repo.SaveRun(context.Background(), RunRecord{RunID: "run-earlier", ConversationID: "conv-1", AgentID: "lead-agent", Status: "completed", Debug: &earlierDebug, CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveRun(context.Background(), RunRecord{RunID: "run-latest", ConversationID: "conv-1", AgentID: "lead-agent", Status: "completed", Debug: &latestDebug, CreatedAt: now.Add(time.Second)}); err != nil {
		t.Fatal(err)
	}
	service := newTestService(ServiceOptions{Repository: repo})

	debug, err := service.GetRunDebug(context.Background(), ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser}, "conv-1", "run-earlier")
	if err != nil {
		t.Fatal(err)
	}

	if debug.RunID != "run-earlier" || debug.OutboundPrompt != "earlier outbound" || debug.RuntimeResponse != "earlier response" {
		t.Fatalf("debug = %+v, want earlier run debug", debug)
	}
}
