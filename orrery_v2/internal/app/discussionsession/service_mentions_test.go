package discussionsession

import (
	"context"
	"strings"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
)

func TestServiceSendMessageDispatchesDiscussionAgentMentionWithLinkedMission(t *testing.T) {
	repo := newMemoryRepository(t)
	if err := repo.SaveConversation(context.Background(), ConversationRecord{ConversationID: "conv-1", Title: "Launch plan", Status: "completed", CreatedBy: "user-1", CreatedAt: testTime, UpdatedAt: testTime}); err != nil {
		t.Fatal(err)
	}
	seedDiscussionMembers(t, repo, "conv-1", "user-1")
	if err := repo.SaveMissionLink(context.Background(), LinkedMissionRecord{ConversationID: "conv-1", MissionID: "mission-existing", Title: "Launch plan", Status: "active", CreatedAt: testTime}); err != nil {
		t.Fatal(err)
	}
	runner := &capturingDiscussionAgentRunner{response: DiscussionAgentResponse{
		AssistantMessage: "[@秦若兰](mention://agent/staffing-agent) 请继续确认人员安排。",
		Status:           "completed",
		RuntimeKind:      "managed",
	}}
	responder := &capturingDiscussionAgentMentionResponder{response: DiscussionAgentResponse{
		AssistantMessage: "[@本地用户](mention://user/user-1) 收到，我继续处理。",
		Status:           "completed",
		RuntimeKind:      "managed",
	}}
	messenger := &capturingMissionMessenger{}
	service := newTestService(ServiceOptions{
		Repository:       repo,
		DiscussionAgent:  runner,
		MentionResponder: responder,
		MissionMessenger: messenger,
		Clock:            fixedClock{now: testTime.Add(time.Minute)},
		IDGenerator:      fixedIDGen("msg-user-2", "run-2", "msg-assistant-2", "run-staffing", "msg-staffing"),
	})

	dto, err := service.SendMessage(context.Background(), ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser, DisplayName: "本地用户"}, SendMessageCommand{ConversationID: "conv-1", Content: "继续推进", IdempotencyKey: "send-2"})
	if err != nil {
		t.Fatal(err)
	}

	if !messenger.called || messenger.mission.MissionID != "mission-existing" {
		t.Fatalf("linked mission bridge = %+v", messenger)
	}
	if !responder.called || responder.request.TargetAgent.ID != "staffing-agent" || len(responder.request.LinkedMissions) != 1 || responder.request.LinkedMissions[0].MissionID != "mission-existing" {
		t.Fatalf("mention responder request = %+v", responder.request)
	}
	if len(dto.Messages) != 3 || dto.Messages[2].AuthorID != "staffing-agent" || dto.Messages[2].Content != "[@本地用户](mention://user/user-1) 收到，我继续处理。" {
		t.Fatalf("messages = %+v", dto.Messages)
	}
	if len(dto.Runs) != 2 || dto.Runs[1].AgentID != "staffing-agent" || dto.Runs[1].Status != "completed" {
		t.Fatalf("runs = %+v", dto.Runs)
	}
}

func TestServiceSendMessageDispatchesDiscussionAgentMentionWithoutMission(t *testing.T) {
	repo := newMemoryRepository(t)
	runner := &capturingDiscussionAgentRunner{response: DiscussionAgentResponse{
		AssistantMessage: "[@秦若兰](mention://agent/staffing-agent) 请帮忙确认要拉什么人。",
		Status:           "completed",
		RuntimeKind:      "managed",
	}}
	responder := &capturingDiscussionAgentMentionResponder{response: DiscussionAgentResponse{
		AssistantMessage: "[@本地用户](mention://user/user-1) 收到，我需要先知道具体目标和所需能力。",
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

	dto, err := service.SendMessage(context.Background(), ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser, DisplayName: "本地用户"}, SendMessageCommand{Content: "@一下Staffing 再随便拉两个人进来", IdempotencyKey: "send-1"})
	if err != nil {
		t.Fatal(err)
	}

	if !responder.called || responder.request.TargetAgent.ID != "staffing-agent" || responder.request.TargetAgent.DisplayName != "秦若兰" {
		t.Fatalf("mention responder request = %+v", responder.request)
	}
	if responder.request.SourceMessage.MessageID != "msg-assistant" || responder.request.SourceMessage.AuthorID != "lead-agent" {
		t.Fatalf("mention source = %+v", responder.request.SourceMessage)
	}
	if len(dto.Messages) != 3 || dto.Messages[2].AuthorID != "staffing-agent" || dto.Messages[2].AuthorName != "秦若兰" {
		t.Fatalf("messages = %+v", dto.Messages)
	}
	if dto.Messages[2].Content != "[@本地用户](mention://user/user-1) 收到，我需要先知道具体目标和所需能力。" {
		t.Fatalf("staffing reply = %q", dto.Messages[2].Content)
	}
	if len(dto.Runs) != 2 || dto.Runs[1].AgentID != "staffing-agent" || dto.Runs[1].Status != "completed" {
		t.Fatalf("runs = %+v", dto.Runs)
	}
}

func TestServiceDiscussionAgentMentionCreatesRuntimeApprovalEventWhenBlocked(t *testing.T) {
	repo := newMemoryRepository(t)
	runner := &capturingDiscussionAgentRunner{response: DiscussionAgentResponse{
		AssistantMessage: "[@秦若兰](mention://agent/staffing-agent) 请帮忙确认要拉什么人。",
		Status:           "completed",
		RuntimeKind:      "managed",
	}}
	responder := &capturingDiscussionAgentMentionResponder{response: DiscussionAgentResponse{
		Status:      "blocked",
		RuntimeKind: "codex",
		SessionID:   "thread-mention-1",
		ProviderMetadata: map[string]string{
			"approval_source":     "codex",
			"approval_request_id": "cmd-mention-1",
			"approval_kind":       "command_execution",
			"approval_type":       "command_execution",
			"approval_tool_name":  "exec_command",
			"approval_session_id": "thread-mention-1",
		},
	}}
	service := newTestService(ServiceOptions{
		Repository:       repo,
		DiscussionAgent:  runner,
		MentionResponder: responder,
		Clock:            fixedClock{now: testTime},
		IDGenerator:      fixedIDGen("conv-1", "msg-user", "run-1", "msg-assistant", "run-staffing"),
	})

	dto, err := service.SendMessage(context.Background(), ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser, DisplayName: "本地用户"}, SendMessageCommand{Content: "请 staffing 看下", IdempotencyKey: "send-mention-approval"})
	if err != nil {
		t.Fatal(err)
	}

	if len(dto.Runs) != 2 || dto.Runs[1].AgentID != "staffing-agent" || dto.Runs[1].Status != "blocked" {
		t.Fatalf("runs = %+v", dto.Runs)
	}
	var approval *EventDTO
	for i := range dto.Events {
		if dto.Events[i].Kind == "discussion_runtime_approval_requested" {
			approval = &dto.Events[i]
			break
		}
	}
	if approval == nil {
		t.Fatalf("runtime approval event not found in events: %+v", dto.Events)
	}
	if approval.Metadata["approval_request_id"] != "cmd-mention-1" || approval.Metadata["approval_run_id"] != "run-staffing" {
		t.Fatalf("approval metadata = %+v", approval.Metadata)
	}
}

func TestServiceSendMessageDispatchesHumanAuthoredDiscussionAgentMention(t *testing.T) {
	repo := newMemoryRepository(t)
	runner := &capturingDiscussionAgentRunner{response: DiscussionAgentResponse{
		AssistantMessage: "[@本地用户](mention://user/user-1) 我看到了，先等 Staffing 处理。",
		Status:           "completed",
		RuntimeKind:      "managed",
	}}
	responder := &capturingDiscussionAgentMentionResponder{response: DiscussionAgentResponse{
		AssistantMessage: "[@本地用户](mention://user/user-1) 收到，我来拉人。",
		Status:           "completed",
		RuntimeKind:      "managed",
	}}
	service := newTestService(ServiceOptions{
		Repository:       repo,
		DiscussionAgent:  runner,
		MentionResponder: responder,
		Clock:            fixedClock{now: testTime},
		IDGenerator:      fixedIDGen("conv-1", "msg-user", "run-staffing", "msg-staffing"),
	})

	dto, err := service.SendMessage(context.Background(), ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser, DisplayName: "本地用户"}, SendMessageCommand{
		Content: "@秦若兰 请随便拉两个人进来", Mentions: []MessageMentionDTO{{TargetKind: "agent", TargetID: "staffing-agent", DisplayName: "秦若兰"}}, IdempotencyKey: "send-1",
	})
	if err != nil {
		t.Fatal(err)
	}

	if runner.called {
		t.Fatalf("direct human-authored agent mention should not run Lead first: %+v", runner.request)
	}
	if !responder.called || responder.request.TargetAgent.ID != "staffing-agent" {
		t.Fatalf("mention responder request = %+v", responder.request)
	}
	if responder.request.SourceMessage.MessageID != "msg-user" || responder.request.SourceMessage.Role != "user" || responder.request.SourceMessage.AuthorID != "user-1" {
		t.Fatalf("mention source = %+v", responder.request.SourceMessage)
	}
	if len(dto.Messages) != 2 || dto.Messages[1].AuthorID != "staffing-agent" || dto.Messages[1].Content != "[@本地用户](mention://user/user-1) 收到，我来拉人。" {
		t.Fatalf("messages = %+v", dto.Messages)
	}
	if len(dto.Runs) != 1 || dto.Runs[0].AgentID != "staffing-agent" || dto.Runs[0].Status != "completed" {
		t.Fatalf("runs = %+v", dto.Runs)
	}
}

func TestServiceSendMessageDispatchesMultipleDiscussionMentionsAsOneBatch(t *testing.T) {
	repo := newMemoryRepository(t)
	if err := repo.SaveConversation(context.Background(), ConversationRecord{ConversationID: "conv-1", Title: "Batch mentions", Status: "completed", CreatedBy: "user-1", CreatedByName: "本地用户", CreatedAt: testTime, UpdatedAt: testTime}); err != nil {
		t.Fatal(err)
	}
	seedDiscussionMembers(t, repo, "conv-1", "user-1")
	if err := repo.SaveMember(context.Background(), ConversationMemberRecord{ConversationID: "conv-1", MemberKind: "agent", MemberID: "frontend-agent", DisplayName: "前端", Role: "Frontend", CreatedAt: testTime.Add(3 * time.Nanosecond)}); err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveMember(context.Background(), ConversationMemberRecord{ConversationID: "conv-1", MemberKind: "agent", MemberID: "tester-agent", DisplayName: "测试", Role: "QA", CreatedAt: testTime.Add(4 * time.Nanosecond)}); err != nil {
		t.Fatal(err)
	}
	runner := &capturingDiscussionAgentRunner{response: DiscussionAgentResponse{
		AssistantMessage: "[@前端](mention://agent/frontend-agent) [@测试](mention://agent/tester-agent) 请同时看一下。",
		Status:           "completed",
		RuntimeKind:      "managed",
	}}
	responder := &barrierDiscussionAgentMentionResponder{waitFor: 2, responses: map[string]DiscussionAgentResponse{
		"frontend-agent": {AssistantMessage: "[@本地用户](mention://user/user-1) 前端看完了。", Status: "completed", RuntimeKind: "managed"},
		"tester-agent":   {AssistantMessage: "[@本地用户](mention://user/user-1) 测试看完了。", Status: "completed", RuntimeKind: "managed"},
	}}
	service := newTestService(ServiceOptions{
		Repository:       repo,
		DiscussionAgent:  runner,
		MentionResponder: responder,
		Clock:            fixedClock{now: testTime},
		IDGenerator:      fixedIDGen("msg-user", "run-1", "msg-assistant", "run-frontend", "run-tester", "msg-frontend", "msg-tester"),
		AgentDirectory: &testAgentDirectory{entries: append(defaultTestAgentDirectory().(*testAgentDirectory).entries,
			agentcore.AgentDirectoryEntry{AgentProfileID: "frontend-agent", AgentInstanceID: "frontend-agent", DisplayName: "前端", RealName: "前端", RoleName: "Frontend", AgentKind: agentcore.AgentKindAI, Enabled: true},
			agentcore.AgentDirectoryEntry{AgentProfileID: "tester-agent", AgentInstanceID: "tester-agent", DisplayName: "测试", RealName: "测试", RoleName: "QA", AgentKind: agentcore.AgentKindAI, Enabled: true},
		)},
	})

	dto, err := service.SendMessage(context.Background(), ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser, DisplayName: "本地用户"}, SendMessageCommand{ConversationID: "conv-1", Content: "让前端和测试同时看", IdempotencyKey: "send-1"})
	if err != nil {
		t.Fatal(err)
	}

	requests := responder.Requests()
	if len(requests) != 2 {
		t.Fatalf("mention requests = %+v, want frontend and tester in one batch", requests)
	}
	gotTargets := map[string]bool{}
	for _, request := range requests {
		gotTargets[request.TargetAgent.ID] = true
	}
	if !gotTargets["frontend-agent"] || !gotTargets["tester-agent"] {
		t.Fatalf("mention targets = %v", gotTargets)
	}
	for _, request := range requests {
		for _, history := range request.History {
			if history.AuthorID == "frontend-agent" || history.AuthorID == "tester-agent" {
				t.Fatalf("batch request for %s included same-batch reply in history: %+v", request.TargetAgent.ID, request.History)
			}
		}
	}
	if len(dto.Messages) != 4 || dto.Messages[2].AuthorID != "frontend-agent" || dto.Messages[3].AuthorID != "tester-agent" {
		t.Fatalf("messages = %+v", dto.Messages)
	}
}

func TestServiceSendMessageSkipsWorkerInternalQAMentionsFromLegacyMembers(t *testing.T) {
	repo := newMemoryRepository(t)
	if err := repo.SaveConversation(context.Background(), ConversationRecord{ConversationID: "conv-1", Title: "Legacy QA members", Status: "completed", CreatedBy: "user-1", CreatedByName: "本地用户", CreatedAt: testTime, UpdatedAt: testTime}); err != nil {
		t.Fatal(err)
	}
	seedDiscussionMembers(t, repo, "conv-1", "user-1")
	if err := repo.SaveMember(context.Background(), ConversationMemberRecord{ConversationID: "conv-1", MemberKind: "agent", MemberID: "frontend-agent", DisplayName: "前端", Role: "Frontend", CreatedAt: testTime.Add(3 * time.Nanosecond)}); err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveMember(context.Background(), ConversationMemberRecord{ConversationID: "conv-1", MemberKind: "agent", MemberID: "qa-plan-agent", DisplayName: "验收计划", Role: "QAPlan", CreatedAt: testTime.Add(4 * time.Nanosecond)}); err != nil {
		t.Fatal(err)
	}
	runner := &capturingDiscussionAgentRunner{response: DiscussionAgentResponse{
		AssistantMessage: "[@前端](mention://agent/frontend-agent) [@验收计划](mention://agent/qa-plan-agent) 请同时看一下。",
		Status:           "completed",
		RuntimeKind:      "managed",
	}}
	responder := &recordingDiscussionAgentMentionResponder{responses: map[string]DiscussionAgentResponse{
		"frontend-agent": {AssistantMessage: "[@本地用户](mention://user/user-1) 前端看完了。", Status: "completed", RuntimeKind: "managed"},
		"qa-plan-agent":  {AssistantMessage: "QAPlan 不应该被直接调起。", Status: "completed", RuntimeKind: "managed"},
	}}
	service := newTestService(ServiceOptions{
		Repository:       repo,
		DiscussionAgent:  runner,
		MentionResponder: responder,
		Clock:            fixedClock{now: testTime},
		IDGenerator:      fixedIDGen("msg-user", "run-1", "msg-assistant", "run-frontend", "run-qa", "msg-frontend", "msg-qa"),
		AgentDirectory: &testAgentDirectory{entries: append(defaultTestAgentDirectory().(*testAgentDirectory).entries,
			agentcore.AgentDirectoryEntry{AgentProfileID: "frontend-agent", AgentInstanceID: "frontend-agent", DisplayName: "前端", RealName: "前端", RoleName: "Frontend", AgentKind: agentcore.AgentKindAI, Enabled: true},
			agentcore.AgentDirectoryEntry{AgentProfileID: "qa-plan-agent", AgentInstanceID: "qa-plan-agent", DisplayName: "验收计划", RealName: "验收计划", RoleName: "QAPlan", AgentKind: agentcore.AgentKindAI, Enabled: true},
		)},
	})

	dto, err := service.SendMessage(context.Background(), ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser, DisplayName: "本地用户"}, SendMessageCommand{ConversationID: "conv-1", Content: "让前端和 QAPlan 看", IdempotencyKey: "send-1"})
	if err != nil {
		t.Fatal(err)
	}

	requests := responder.Requests()
	if len(requests) != 1 || requests[0].TargetAgent.ID != "frontend-agent" {
		t.Fatalf("mention requests = %+v, want only frontend-agent", requests)
	}
	for _, run := range dto.Runs {
		if run.AgentID == "qa-plan-agent" {
			t.Fatalf("worker-internal QA should not have a direct discussion run: %+v", dto.Runs)
		}
	}
}

func TestServiceSendMessageCascadesAgentMentionsBackToLead(t *testing.T) {
	repo := newMemoryRepository(t)
	runner := &capturingDiscussionAgentRunner{response: DiscussionAgentResponse{
		AssistantMessage: "[@秦若兰](mention://agent/staffing-agent) 请帮忙拉两个人进来。",
		Status:           "completed",
		RuntimeKind:      "managed",
	}}
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

	dto, err := service.SendMessage(context.Background(), ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser, DisplayName: "本地用户"}, SendMessageCommand{Content: "@一下Staffing 再随便拉两个人进来", IdempotencyKey: "send-1"})
	if err != nil {
		t.Fatal(err)
	}

	if len(responder.requests) != 2 {
		t.Fatalf("mention requests = %+v, want Staffing then Lead", responder.requests)
	}
	if responder.requests[0].TargetAgent.ID != "staffing-agent" || responder.requests[1].TargetAgent.ID != "lead-agent" {
		t.Fatalf("mention target sequence = %+v", responder.requests)
	}
	if len(dto.Messages) != 4 || dto.Messages[2].AuthorID != "staffing-agent" || dto.Messages[3].AuthorID != "lead-agent" {
		t.Fatalf("messages = %+v", dto.Messages)
	}
	if len(dto.Runs) != 3 || dto.Runs[1].AgentID != "staffing-agent" || dto.Runs[2].AgentID != "lead-agent" {
		t.Fatalf("runs = %+v", dto.Runs)
	}
}

func TestServiceSendMessageStopsContinuousTwoAgentMentionLoopAtConfiguredLimit(t *testing.T) {
	repo := newMemoryRepository(t)
	if err := repo.SaveConversation(context.Background(), ConversationRecord{ConversationID: "conv-1", Title: "Agent loop", Status: "completed", CreatedBy: "user-1", CreatedByName: "本地用户", CreatedAt: testTime, UpdatedAt: testTime}); err != nil {
		t.Fatal(err)
	}
	seedDiscussionMembers(t, repo, "conv-1", "user-1")
	if err := repo.SaveMember(context.Background(), ConversationMemberRecord{ConversationID: "conv-1", MemberKind: "agent", MemberID: "frontend-agent", DisplayName: "前端", Role: "Frontend", CreatedAt: testTime.Add(3 * time.Nanosecond)}); err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveMember(context.Background(), ConversationMemberRecord{ConversationID: "conv-1", MemberKind: "agent", MemberID: "tester-agent", DisplayName: "测试", Role: "QA", CreatedAt: testTime.Add(4 * time.Nanosecond)}); err != nil {
		t.Fatal(err)
	}
	runner := &capturingDiscussionAgentRunner{response: DiscussionAgentResponse{
		AssistantMessage: "[@前端](mention://agent/frontend-agent) 你先看。",
		Status:           "completed",
		RuntimeKind:      "managed",
	}}
	responder := &sequenceDiscussionAgentMentionResponder{responses: []DiscussionAgentResponse{
		{AssistantMessage: "[@测试](mention://agent/tester-agent) 你接着看。", Status: "completed", RuntimeKind: "managed"},
		{AssistantMessage: "[@前端](mention://agent/frontend-agent) 我看完了，你再确认。", Status: "completed", RuntimeKind: "managed"},
		{AssistantMessage: "[@测试](mention://agent/tester-agent) 再过一遍。", Status: "completed", RuntimeKind: "managed"},
		{AssistantMessage: "[@前端](mention://agent/frontend-agent) 继续。", Status: "completed", RuntimeKind: "managed"},
	}}
	service := newTestService(ServiceOptions{
		Repository:       repo,
		DiscussionAgent:  runner,
		MentionResponder: responder,
		Clock:            fixedClock{now: testTime},
		IDGenerator: fixedIDGen(
			"msg-user", "run-1", "msg-assistant",
			"run-frontend-1", "msg-frontend-1",
			"run-tester-1", "msg-tester-1",
			"run-frontend-2", "msg-frontend-2",
			"run-tester-2", "msg-tester-2",
		),
		AgentDirectory: &testAgentDirectory{entries: append(defaultTestAgentDirectory().(*testAgentDirectory).entries,
			agentcore.AgentDirectoryEntry{AgentProfileID: "frontend-agent", AgentInstanceID: "frontend-agent", DisplayName: "前端", RealName: "前端", RoleName: "Frontend", AgentKind: agentcore.AgentKindAI, Enabled: true},
			agentcore.AgentDirectoryEntry{AgentProfileID: "tester-agent", AgentInstanceID: "tester-agent", DisplayName: "测试", RealName: "测试", RoleName: "QA", AgentKind: agentcore.AgentKindAI, Enabled: true},
		)},
		MentionConfig: fixedLeadMentionLoopConfig{pairLoopLimit: 3},
	})

	dto, err := service.SendMessage(context.Background(), ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser, DisplayName: "本地用户"}, SendMessageCommand{ConversationID: "conv-1", Content: "让前端和测试互相确认", IdempotencyKey: "send-1"})
	if err != nil {
		t.Fatal(err)
	}

	if len(responder.requests) != 4 {
		t.Fatalf("mention request count = %d, requests = %+v", len(responder.requests), responder.requests)
	}
	gotTargets := []string{}
	for _, request := range responder.requests {
		gotTargets = append(gotTargets, request.TargetAgent.ID)
	}
	if strings.Join(gotTargets, ",") != "frontend-agent,tester-agent,frontend-agent,tester-agent" {
		t.Fatalf("mention target sequence = %v", gotTargets)
	}
	if len(dto.Runs) != 5 || dto.Runs[4].AgentID != "tester-agent" {
		t.Fatalf("runs = %+v", dto.Runs)
	}
	if len(dto.Messages) != 6 {
		t.Fatalf("messages = %+v", dto.Messages)
	}
}

func TestServiceSendMessageDoesNotTreatThreeAgentChainAsPairLoop(t *testing.T) {
	repo := newMemoryRepository(t)
	if err := repo.SaveConversation(context.Background(), ConversationRecord{ConversationID: "conv-1", Title: "Agent chain", Status: "completed", CreatedBy: "user-1", CreatedByName: "本地用户", CreatedAt: testTime, UpdatedAt: testTime}); err != nil {
		t.Fatal(err)
	}
	seedDiscussionMembers(t, repo, "conv-1", "user-1")
	if err := repo.SaveMember(context.Background(), ConversationMemberRecord{ConversationID: "conv-1", MemberKind: "agent", MemberID: "frontend-agent", DisplayName: "前端", Role: "Frontend", CreatedAt: testTime.Add(3 * time.Nanosecond)}); err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveMember(context.Background(), ConversationMemberRecord{ConversationID: "conv-1", MemberKind: "agent", MemberID: "tester-agent", DisplayName: "测试", Role: "QA", CreatedAt: testTime.Add(4 * time.Nanosecond)}); err != nil {
		t.Fatal(err)
	}
	runner := &capturingDiscussionAgentRunner{response: DiscussionAgentResponse{
		AssistantMessage: "[@前端](mention://agent/frontend-agent) 你先看。",
		Status:           "completed",
		RuntimeKind:      "managed",
	}}
	responder := &sequenceDiscussionAgentMentionResponder{responses: []DiscussionAgentResponse{
		{AssistantMessage: "[@测试](mention://agent/tester-agent) 你接着看。", Status: "completed", RuntimeKind: "managed"},
		{AssistantMessage: "[@顾明澈](mention://agent/lead-agent) 我看完了，请 Lead 接一下。", Status: "completed", RuntimeKind: "managed"},
		{AssistantMessage: "[@本地用户](mention://user/user-1) 三方都过了一遍。", Status: "completed", RuntimeKind: "managed"},
	}}
	service := newTestService(ServiceOptions{
		Repository:       repo,
		DiscussionAgent:  runner,
		MentionResponder: responder,
		Clock:            fixedClock{now: testTime},
		IDGenerator: fixedIDGen(
			"msg-user", "run-1", "msg-assistant",
			"run-frontend-1", "msg-frontend-1",
			"run-tester-1", "msg-tester-1",
			"run-lead-followup", "msg-lead-followup",
		),
		AgentDirectory: &testAgentDirectory{entries: append(defaultTestAgentDirectory().(*testAgentDirectory).entries,
			agentcore.AgentDirectoryEntry{AgentProfileID: "frontend-agent", AgentInstanceID: "frontend-agent", DisplayName: "前端", RealName: "前端", RoleName: "Frontend", AgentKind: agentcore.AgentKindAI, Enabled: true},
			agentcore.AgentDirectoryEntry{AgentProfileID: "tester-agent", AgentInstanceID: "tester-agent", DisplayName: "测试", RealName: "测试", RoleName: "QA", AgentKind: agentcore.AgentKindAI, Enabled: true},
		)},
		MentionConfig: fixedLeadMentionLoopConfig{pairLoopLimit: 1},
	})

	dto, err := service.SendMessage(context.Background(), ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser, DisplayName: "本地用户"}, SendMessageCommand{ConversationID: "conv-1", Content: "让前端、测试、Lead 轮流确认", IdempotencyKey: "send-1"})
	if err != nil {
		t.Fatal(err)
	}

	if len(responder.requests) != 3 {
		t.Fatalf("mention request count = %d, requests = %+v", len(responder.requests), responder.requests)
	}
	gotTargets := []string{}
	for _, request := range responder.requests {
		gotTargets = append(gotTargets, request.TargetAgent.ID)
	}
	if strings.Join(gotTargets, ",") != "frontend-agent,tester-agent,lead-agent" {
		t.Fatalf("mention target sequence = %v", gotTargets)
	}
	if len(dto.Messages) != 5 || dto.Messages[4].AuthorID != "lead-agent" {
		t.Fatalf("messages = %+v", dto.Messages)
	}
}

func TestServiceMentionLeadToolCallsApplyMissionTooling(t *testing.T) {
	repo := newMemoryRepository(t)
	runner := &capturingDiscussionAgentRunner{response: DiscussionAgentResponse{
		AssistantMessage: "[@秦若兰](mention://agent/staffing-agent) 请先确认一下是否需要转 Mission。",
		Status:           "completed",
		RuntimeKind:      "managed",
	}}
	responder := &sequenceDiscussionAgentMentionResponder{responses: []DiscussionAgentResponse{
		{
			AssistantMessage: "[@顾明澈](mention://agent/lead-agent) 这个可以转成 Mission。",
			Status:           "completed",
			RuntimeKind:      "managed",
		},
		{
			AssistantMessage: "[@本地用户](mention://user/user-1) 我已创建 Mission brief。",
			Status:           "completed",
			RuntimeKind:      "managed",
			ToolCalls: []ToolCallDTO{{
				ToolCallID: "tool-brief",
				Name:       "mission_brief_create",
				Status:     "completed",
				Arguments: map[string]string{
					"title":      "讨论组需求",
					"final_goal": "把讨论组里的需求落成 Mission",
				},
			}},
		},
	}}
	applier := &capturingMissionToolApplier{links: []LinkedMissionDTO{{MissionID: "mission-1", Title: "讨论组需求", Status: "active", CreatedAt: testTime}}}
	service := newTestService(ServiceOptions{
		Repository:       repo,
		DiscussionAgent:  runner,
		MentionResponder: responder,
		MissionTooling:   applier,
		Clock:            fixedClock{now: testTime},
		IDGenerator:      fixedIDGen("conv-1", "msg-user", "run-1", "msg-assistant", "run-staffing", "msg-staffing", "run-lead-followup", "msg-lead-followup"),
	})

	dto, err := service.SendMessage(context.Background(), ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser, DisplayName: "本地用户"}, SendMessageCommand{Content: "让 Lead 自己接一下", IdempotencyKey: "send-1"})
	if err != nil {
		t.Fatal(err)
	}

	if !applier.called || applier.viewer.ViewerID != "lead-agent" || len(applier.calls) != 1 || applier.calls[0].Name != "mission_brief_create" {
		t.Fatalf("mission tooling = called:%v viewer:%+v calls:%+v", applier.called, applier.viewer, applier.calls)
	}
	if len(dto.LinkedMissions) != 1 || dto.LinkedMissions[0].MissionID != "mission-1" {
		t.Fatalf("linked missions = %+v", dto.LinkedMissions)
	}
	if len(dto.Runs) != 3 || len(dto.Runs[2].ToolCalls) != 1 || dto.Runs[2].ToolCalls[0].Name != "mission_brief_create" {
		t.Fatalf("mention run tool calls = %+v", dto.Runs)
	}
}

func TestServiceMentionRejectsUnboundMissionToolCallWithoutApplyingTooling(t *testing.T) {
	repo := newMemoryRepository(t)
	runner := &capturingDiscussionAgentRunner{response: DiscussionAgentResponse{
		AssistantMessage: "[@秦若兰](mention://agent/staffing-agent) 请接一下。",
		Status:           "completed",
		RuntimeKind:      "managed",
	}}
	responder := &capturingDiscussionAgentMentionResponder{unboundTools: true, response: DiscussionAgentResponse{
		AssistantMessage: "我已创建 Mission brief。",
		Status:           "completed",
		RuntimeKind:      "managed",
		ToolCalls: []ToolCallDTO{{
			ToolCallID: "tool-brief",
			Name:       "mission_brief_create",
			Status:     "completed",
			Arguments: map[string]string{
				"title":      "讨论组需求",
				"final_goal": "把讨论组里的需求落成 Mission",
			},
		}},
	}}
	applier := &capturingMissionToolApplier{}
	service := newTestService(ServiceOptions{
		Repository:       repo,
		DiscussionAgent:  runner,
		MentionResponder: responder,
		MissionTooling:   applier,
		AgentDirectory: &testAgentDirectory{entries: []agentcore.AgentDirectoryEntry{
			{AgentProfileID: "lead-agent", AgentInstanceID: "lead-agent", DisplayName: "顾明澈", RealName: "顾明澈", RoleName: "Lead", AgentKind: agentcore.AgentKindAI, ToolRefs: defaultTestAgentToolRefs(), Enabled: true},
			{AgentProfileID: "staffing-agent", AgentInstanceID: "staffing-agent", DisplayName: "秦若兰", RealName: "秦若兰", RoleName: "Staffing", AgentKind: agentcore.AgentKindAI, ToolRefs: []agentcore.ToolRef{{ToolID: "discussion_ack"}}, Enabled: true},
		}},
		Clock:       fixedClock{now: testTime},
		IDGenerator: fixedIDGen("conv-1", "msg-user", "run-1", "msg-assistant", "run-mention", "event-failed"),
	})

	dto, err := service.SendMessage(context.Background(), ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser, DisplayName: "本地用户"}, SendMessageCommand{Content: "请 Lead 处理", IdempotencyKey: "send-1"})
	if err != nil {
		t.Fatal(err)
	}
	if applier.called {
		t.Fatalf("unbound mention tool call should not reach mission tooling: %+v", applier.calls)
	}
	if len(dto.Events) != 1 || dto.Events[0].Kind != "discussion_agent_mention_failed" || !strings.Contains(dto.Events[0].Summary, "unbound tool call mission_brief_create") {
		t.Fatalf("events = %+v, want unbound tool failure event", dto.Events)
	}
	if len(dto.LinkedMissions) != 0 {
		t.Fatalf("linked missions = %+v, want none", dto.LinkedMissions)
	}
}
