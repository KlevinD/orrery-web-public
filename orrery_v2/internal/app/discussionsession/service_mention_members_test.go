package discussionsession

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	agentcore "orrery_v2/internal/agent/core"
)

func TestServiceMentionCanAddAIConversationMembersFromDirectory(t *testing.T) {
	repo := newMemoryRepository(t)
	runner := &capturingDiscussionAgentRunner{response: DiscussionAgentResponse{
		AssistantMessage: "[@秦若兰](mention://agent/staffing-agent) 请再拉两位同事进群。",
		Status:           "completed",
		RuntimeKind:      "managed",
	}}
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

	dto, err := service.SendMessage(context.Background(), ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser, DisplayName: "本地用户"}, SendMessageCommand{Content: "@一下Staffing 让她再随便拉两个人进来", IdempotencyKey: "send-1"})
	if err != nil {
		t.Fatal(err)
	}

	namesByID := map[string]string{}
	for _, member := range dto.Members {
		namesByID[member.ID] = member.DisplayName
	}
	if namesByID["agent-frontend"] != "林前端" || namesByID["agent-tester"] != "周测试" {
		t.Fatalf("members = %+v", dto.Members)
	}
	if len(dto.Runs) != 2 || len(dto.Runs[1].ToolCalls) != 1 || dto.Runs[1].ToolCalls[0].Name != "discussion_member_add" {
		t.Fatalf("mention run tool calls = %+v", dto.Runs)
	}
}

func TestServiceMentionContinuesAfterMemberAddAndDispatchesBareProviderHandoff(t *testing.T) {
	repo := newMemoryRepository(t)
	runner := &capturingDiscussionAgentRunner{response: DiscussionAgentResponse{
		AssistantMessage: "[@秦若兰](mention://agent/staffing-agent) 请再拉两位同事进群。",
		Status:           "completed",
		RuntimeKind:      "managed",
	}}
	responder := &capturingDiscussionAgentMentionResponder{
		boundTools: []DiscussionAgentBoundTool{{ToolID: "discussion_talent_list"}, {ToolID: "discussion_member_add"}},
		responses: []DiscussionAgentResponse{
			{
				Status:      "completed",
				RuntimeKind: "managed",
				ToolCalls: []ToolCallDTO{{
					ToolCallID: "add-members",
					Name:       "discussion_member_add",
					Status:     "completed",
					Arguments: map[string]string{
						"agent_ids": "agent-frontend,agent-tester",
					},
				}},
			},
			{
				AssistantMessage: "已拉两位同事进来。\n\nmention://agent/agent-frontend 请负责交付贪吃蛇小游戏。",
				Status:           "completed",
				RuntimeKind:      "managed",
			},
			{
				AssistantMessage: "贪吃蛇小游戏已交付。",
				Status:           "completed",
				RuntimeKind:      "managed",
			},
		},
	}
	service := newTestService(ServiceOptions{
		Repository:       repo,
		DiscussionAgent:  runner,
		MentionResponder: responder,
		AgentDirectory: &testAgentDirectory{entries: append(defaultTestAgentDirectory().(*testAgentDirectory).entries,
			agentcore.AgentDirectoryEntry{AgentProfileID: "agent-frontend", AgentInstanceID: "agent-frontend", DisplayName: "林前端", RealName: "林前端", RoleName: "Frontend", AgentKind: agentcore.AgentKindAI, Enabled: true},
			agentcore.AgentDirectoryEntry{AgentProfileID: "agent-tester", AgentInstanceID: "agent-tester", DisplayName: "周测试", RealName: "周测试", RoleName: "QA", AgentKind: agentcore.AgentKindAI, Enabled: true},
		)},
		Clock:       fixedClock{now: testTime},
		IDGenerator: fixedIDGen("conv-1", "msg-user", "run-1", "msg-assistant", "run-staffing", "msg-staffing", "run-frontend", "msg-frontend"),
	})

	dto, err := service.SendMessage(context.Background(), ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser, DisplayName: "本地用户"}, SendMessageCommand{Content: "@一下Staffing 让她再随便拉两个人进来", IdempotencyKey: "send-1"})
	if err != nil {
		t.Fatal(err)
	}

	if len(responder.requests) != 3 {
		t.Fatalf("mention responder requests = %d, want staffing, continuation, and frontend handoff", len(responder.requests))
	}
	continuation := responder.requests[1]
	if continuation.Mode != discussionToolResultContinuationMode || !strings.Contains(continuation.Content, "discussion_member_add") || !strings.Contains(continuation.Content, "added_count=2") {
		t.Fatalf("continuation request = %+v", continuation)
	}
	if responder.requests[2].TargetAgent.ID != "agent-frontend" {
		t.Fatalf("bare provider mention target = %+v", responder.requests[2].TargetAgent)
	}
	if len(dto.Messages) < 4 || dto.Messages[len(dto.Messages)-1].Content != "贪吃蛇小游戏已交付。" {
		t.Fatalf("messages = %+v", dto.Messages)
	}
	if len(dto.Runs) != 3 || len(dto.Runs[1].ToolCalls) != 1 || dto.Runs[1].ToolCalls[0].Result["added_count"] != "2" || dto.Runs[2].AgentID != "agent-frontend" {
		t.Fatalf("mention run tool calls = %+v", dto.Runs)
	}
}

func TestServiceMentionTalentListExcludesExistingMembersThenAddsExplicitSelection(t *testing.T) {
	repo := newMemoryRepository(t)
	runner := &capturingDiscussionAgentRunner{response: DiscussionAgentResponse{
		AssistantMessage: "[@秦若兰](mention://agent/staffing-agent) 请为 HTML 贪吃蛇找一名前端开发。",
		Status:           "completed",
		RuntimeKind:      "managed",
	}}
	responder := &capturingDiscussionAgentMentionResponder{
		boundTools: []DiscussionAgentBoundTool{{ToolID: "discussion_talent_list"}, {ToolID: "discussion_member_add"}},
		responses: []DiscussionAgentResponse{
			{
				Status:      "completed",
				RuntimeKind: "managed",
				ToolCalls: []ToolCallDTO{{
					ToolCallID: "list-talent",
					Name:       "discussion_talent_list",
					Status:     "completed",
					Arguments: map[string]string{
						"limit": "10",
					},
				}},
			},
			{
				Status:      "completed",
				RuntimeKind: "managed",
				ToolCalls: []ToolCallDTO{{
					ToolCallID: "add-frontend",
					Name:       "discussion_member_add",
					Status:     "completed",
					Arguments: map[string]string{
						"agent_ids": "agent-frontend",
					},
				}},
			},
			{
				AssistantMessage: "已加入林前端。",
				Status:           "completed",
				RuntimeKind:      "managed",
			},
		},
	}
	service := newTestService(ServiceOptions{
		Repository:       repo,
		DiscussionAgent:  runner,
		MentionResponder: responder,
		AgentDirectory: &testAgentDirectory{entries: append(defaultTestAgentDirectory().(*testAgentDirectory).entries,
			agentcore.AgentDirectoryEntry{AgentProfileID: "agent-ops", AgentInstanceID: "agent-ops", DisplayName: "中国电商运营专家", RealName: "中国电商运营专家", RoleName: "电商运营专家", AgentKind: agentcore.AgentKindAI, Enabled: true},
			agentcore.AgentDirectoryEntry{AgentProfileID: "agent-invest", AgentInstanceID: "agent-invest", DisplayName: "投资研究员", RealName: "投资研究员", RoleName: "投资研究员", AgentKind: agentcore.AgentKindAI, Enabled: true},
			agentcore.AgentDirectoryEntry{
				AgentProfileID:   "agent-frontend",
				AgentInstanceID:  "agent-frontend",
				DisplayName:      "林前端",
				RealName:         "林前端",
				RoleName:         "Web 前端开发",
				AgentKind:        agentcore.AgentKindAI,
				PersonaSummary:   "负责实现 Web 前端交互。",
				BehaviorBoundary: "只处理前端实现，不改后端服务。",
				SkillRefs:        []agentcore.SkillRef{{SkillID: "frontend-build"}},
				ToolRefs:         []agentcore.ToolRef{{ToolID: "repo-write"}, {ToolID: "shell-exec"}},
				Enabled:          true,
			},
		)},
		Clock:       fixedClock{now: testTime},
		IDGenerator: fixedIDGen("conv-1", "msg-user", "run-1", "msg-assistant", "run-staffing", "msg-staffing"),
	})

	dto, err := service.SendMessage(context.Background(), ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser, DisplayName: "本地用户"}, SendMessageCommand{Content: "找前端开发", IdempotencyKey: "send-1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(responder.requests) != 3 {
		t.Fatalf("mention responder requests = %d, want initial + list result + add result", len(responder.requests))
	}
	if !strings.Contains(responder.requests[1].Content, "discussion_talent_list") ||
		!strings.Contains(responder.requests[1].Content, "agent-frontend") ||
		strings.Contains(responder.requests[1].Content, "staffing-agent") {
		t.Fatalf("talent-list continuation should include candidates and exclude existing members: %s", responder.requests[1].Content)
	}
	if !strings.Contains(responder.requests[2].Content, "discussion_member_add") || !strings.Contains(responder.requests[2].Content, "added_agent_names=林前端") {
		t.Fatalf("member-add continuation should include added member result: %s", responder.requests[2].Content)
	}

	namesByID := map[string]string{}
	rolesByID := map[string]string{}
	for _, member := range dto.Members {
		namesByID[member.ID] = member.DisplayName
		rolesByID[member.ID] = member.Role
	}
	if namesByID["agent-frontend"] != "林前端" || namesByID["agent-ops"] != "" || namesByID["agent-invest"] != "" {
		t.Fatalf("members = %+v", dto.Members)
	}
	if rolesByID["agent-frontend"] != "Web 前端开发" {
		t.Fatalf("frontend member role should come from directory, roles = %+v", rolesByID)
	}
	if len(dto.Runs) != 2 || len(dto.Runs[1].ToolCalls) != 2 {
		t.Fatalf("mention run tool calls = %+v", dto.Runs)
	}
	listTool := dto.Runs[1].ToolCalls[0]
	if listTool.Name != "discussion_talent_list" ||
		!strings.Contains(listTool.Result["eligible_agent_ids"], "agent-frontend") ||
		strings.Contains(listTool.Result["eligible_agent_ids"], "staffing-agent") {
		t.Fatalf("talent list tool result should expose eligible non-members: %+v", listTool.Result)
	}
	var candidates []map[string]any
	if err := json.Unmarshal([]byte(listTool.Result["candidates_json"]), &candidates); err != nil {
		t.Fatalf("candidates_json should be valid JSON: %v; raw=%s", err, listTool.Result["candidates_json"])
	}
	for _, candidate := range candidates {
		if _, ok := candidate["tools"]; ok {
			t.Fatalf("talent list should not expose candidate tools: %+v", candidate)
		}
		if _, ok := candidate["skills"]; ok {
			t.Fatalf("talent list should not expose candidate skills: %+v", candidate)
		}
		if candidate["agent_id"] == "agent-frontend" && candidate["behavior_boundary"] != "只处理前端实现，不改后端服务。" {
			t.Fatalf("talent list should expose candidate behavior boundary: %+v", candidate)
		}
	}
	addTool := dto.Runs[1].ToolCalls[1]
	if addTool.Name != "discussion_member_add" || addTool.Result["added_agent_ids"] != "agent-frontend" || addTool.Result["added_agent_names"] != "林前端" {
		t.Fatalf("member add tool result should expose real added members: %+v", addTool)
	}
}

func TestServiceMentionTalentListExcludesWorkerInternalQAAgents(t *testing.T) {
	repo := newMemoryRepository(t)
	runner := &capturingDiscussionAgentRunner{response: DiscussionAgentResponse{
		AssistantMessage: "[@秦若兰](mention://agent/staffing-agent) 请为 HTML 贪吃蛇找一名前端开发。",
		Status:           "completed",
		RuntimeKind:      "managed",
	}}
	responder := &capturingDiscussionAgentMentionResponder{
		boundTools: []DiscussionAgentBoundTool{{ToolID: "discussion_talent_list"}},
		responses: []DiscussionAgentResponse{{
			Status:      "completed",
			RuntimeKind: "managed",
			ToolCalls: []ToolCallDTO{{
				ToolCallID: "list-talent",
				Name:       "discussion_talent_list",
				Status:     "completed",
				Arguments:  map[string]string{"limit": "10"},
			}},
		}},
	}
	service := newTestService(ServiceOptions{
		Repository:       repo,
		DiscussionAgent:  runner,
		MentionResponder: responder,
		AgentDirectory: &testAgentDirectory{entries: append(defaultTestAgentDirectory().(*testAgentDirectory).entries,
			agentcore.AgentDirectoryEntry{AgentProfileID: "agent-frontend", AgentInstanceID: "agent-frontend", DisplayName: "林前端", RealName: "林前端", RoleName: "Web 前端开发", AgentKind: agentcore.AgentKindAI, Enabled: true},
			agentcore.AgentDirectoryEntry{AgentProfileID: "qa-plan-agent", AgentInstanceID: "qa-plan-agent", DisplayName: "验收计划", RealName: "验收计划", RoleName: "QAPlan", AgentKind: agentcore.AgentKindAI, Enabled: true},
			agentcore.AgentDirectoryEntry{AgentProfileID: "profile-qa-check", AgentInstanceID: "profile-qa-check", DisplayName: "验收检查", RealName: "验收检查", RoleName: "质量检查", AgentKind: agentcore.AgentKindAI, OrgIdentity: agentcore.AgentOrgIdentity{RoleRefs: []agentcore.RoleRef{{ID: "QACheck"}}}, Enabled: true},
		)},
		Clock:       fixedClock{now: testTime},
		IDGenerator: fixedIDGen("conv-1", "msg-user", "run-1", "msg-assistant", "run-staffing"),
	})

	dto, err := service.SendMessage(context.Background(), ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser, DisplayName: "本地用户"}, SendMessageCommand{Content: "找前端开发", IdempotencyKey: "send-1"})
	if err != nil {
		t.Fatal(err)
	}

	if len(responder.requests) != 2 {
		t.Fatalf("mention responder requests = %d, want initial + talent-list continuation", len(responder.requests))
	}
	continuation := responder.requests[1].Content
	if !strings.Contains(continuation, "agent-frontend") || strings.Contains(continuation, "qa-plan-agent") || strings.Contains(continuation, "profile-qa-check") {
		t.Fatalf("talent-list continuation should expose workers and hide worker-internal QA agents: %s", continuation)
	}
	if len(dto.Runs) != 2 || len(dto.Runs[1].ToolCalls) != 1 {
		t.Fatalf("runs = %+v", dto.Runs)
	}
	result := dto.Runs[1].ToolCalls[0].Result
	if !strings.Contains(result["eligible_agent_ids"], "agent-frontend") || strings.Contains(result["eligible_agent_ids"], "qa-plan-agent") || strings.Contains(result["eligible_agent_ids"], "profile-qa-check") {
		t.Fatalf("talent list result should hide worker-internal QA agents: %+v", result)
	}
}

func TestServiceMentionMemberAddRejectsWorkerInternalQAAgents(t *testing.T) {
	repo := newMemoryRepository(t)
	runner := &capturingDiscussionAgentRunner{response: DiscussionAgentResponse{
		AssistantMessage: "[@秦若兰](mention://agent/staffing-agent) 请把 QA plan 和 worker 拉进群。",
		Status:           "completed",
		RuntimeKind:      "managed",
	}}
	responder := &capturingDiscussionAgentMentionResponder{response: DiscussionAgentResponse{
		Status:      "completed",
		RuntimeKind: "managed",
		ToolCalls: []ToolCallDTO{{
			ToolCallID: "add-members",
			Name:       "discussion_member_add",
			Status:     "completed",
			Arguments: map[string]string{
				"agent_ids": "qa-plan-agent,agent-frontend",
			},
		}},
	}}
	service := newTestService(ServiceOptions{
		Repository:       repo,
		DiscussionAgent:  runner,
		MentionResponder: responder,
		AgentDirectory: &testAgentDirectory{entries: append(defaultTestAgentDirectory().(*testAgentDirectory).entries,
			agentcore.AgentDirectoryEntry{AgentProfileID: "agent-frontend", AgentInstanceID: "agent-frontend", DisplayName: "林前端", RealName: "林前端", RoleName: "Web 前端开发", AgentKind: agentcore.AgentKindAI, Enabled: true},
			agentcore.AgentDirectoryEntry{AgentProfileID: "qa-plan-agent", AgentInstanceID: "qa-plan-agent", DisplayName: "验收计划", RealName: "验收计划", RoleName: "QAPlan", AgentKind: agentcore.AgentKindAI, Enabled: true},
		)},
		Clock:       fixedClock{now: testTime},
		IDGenerator: fixedIDGen("conv-1", "msg-user", "run-1", "msg-assistant", "run-staffing", "event-failure"),
	})

	dto, err := service.SendMessage(context.Background(), ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser, DisplayName: "本地用户"}, SendMessageCommand{Content: "请拉 QA plan 和 worker 进来", IdempotencyKey: "send-1"})
	if err != nil {
		t.Fatal(err)
	}

	if len(dto.Events) == 0 || dto.Events[len(dto.Events)-1].Kind != "discussion_agent_mention_failed" ||
		!strings.Contains(dto.Events[len(dto.Events)-1].Summary, "worker-internal QA") {
		t.Fatalf("events = %+v, want worker-internal QA rejection", dto.Events)
	}
	for _, member := range dto.Members {
		if member.ID == "qa-plan-agent" || member.ID == "agent-frontend" {
			t.Fatalf("member add should reject the whole call when it contains worker-internal QA: %+v", dto.Members)
		}
	}
}

func TestServiceMentionMemberAddRejectsCountWithoutExplicitAgentIDs(t *testing.T) {
	repo := newMemoryRepository(t)
	runner := &capturingDiscussionAgentRunner{response: DiscussionAgentResponse{
		AssistantMessage: "[@秦若兰](mention://agent/staffing-agent) 请随便拉一个人。",
		Status:           "completed",
		RuntimeKind:      "managed",
	}}
	responder := &capturingDiscussionAgentMentionResponder{response: DiscussionAgentResponse{
		Status:      "completed",
		RuntimeKind: "managed",
		ToolCalls: []ToolCallDTO{{
			ToolCallID: "add-by-count",
			Name:       "discussion_member_add",
			Status:     "completed",
			Arguments: map[string]string{
				"count": "1",
			},
		}},
	}, boundTools: []DiscussionAgentBoundTool{{ToolID: "discussion_member_add"}}}
	service := newTestService(ServiceOptions{
		Repository:       repo,
		DiscussionAgent:  runner,
		MentionResponder: responder,
		AgentDirectory: &testAgentDirectory{entries: append(defaultTestAgentDirectory().(*testAgentDirectory).entries,
			agentcore.AgentDirectoryEntry{AgentProfileID: "agent-frontend", AgentInstanceID: "agent-frontend", DisplayName: "林前端", RealName: "林前端", RoleName: "Web 前端开发", AgentKind: agentcore.AgentKindAI, Enabled: true},
		)},
		Clock:       fixedClock{now: testTime},
		IDGenerator: fixedIDGen("conv-1", "msg-user", "run-1", "msg-assistant", "run-staffing", "event-failure"),
	})

	dto, err := service.SendMessage(context.Background(), ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser, DisplayName: "本地用户"}, SendMessageCommand{Content: "请随便拉一个人", IdempotencyKey: "send-1"})
	if err != nil {
		t.Fatal(err)
	}

	if len(dto.Events) == 0 || dto.Events[len(dto.Events)-1].Kind != "discussion_agent_mention_failed" ||
		!strings.Contains(dto.Events[len(dto.Events)-1].Summary, "discussion_member_add no longer accepts count") {
		t.Fatalf("events = %+v, want count rejection", dto.Events)
	}
	for _, member := range dto.Members {
		if member.ID == "agent-frontend" {
			t.Fatalf("count-based member add should not add candidates: %+v", dto.Members)
		}
	}
}

func TestServiceSendMessageContinuesPrimaryAgentAfterDiscussionMemberAddToolResults(t *testing.T) {
	repo := newMemoryRepository(t)
	runner := &capturingDiscussionAgentRunner{
		boundTools: []DiscussionAgentBoundTool{{ToolID: "discussion_member_add"}},
		responses: []DiscussionAgentResponse{
			{
				AssistantMessage: "我先拉一位前端同事进来。",
				Status:           "completed",
				RuntimeKind:      "managed",
				ToolCalls: []ToolCallDTO{{
					ToolCallID: "add-frontend",
					Name:       "discussion_member_add",
					Status:     "completed",
					Arguments: map[string]string{
						"agent_ids": "agent-frontend",
					},
				}},
			},
			{
				AssistantMessage: "已拉林前端进来，成员列表已经更新。",
				Status:           "completed",
				RuntimeKind:      "managed",
			},
		},
	}
	service := newTestService(ServiceOptions{
		Repository:      repo,
		DiscussionAgent: runner,
		AgentDirectory: &testAgentDirectory{entries: append(defaultTestAgentDirectory().(*testAgentDirectory).entries,
			agentcore.AgentDirectoryEntry{AgentProfileID: "agent-frontend", AgentInstanceID: "agent-frontend", DisplayName: "林前端", RealName: "林前端", RoleName: "Frontend", AgentKind: agentcore.AgentKindAI, Enabled: true},
		)},
		Clock:       fixedClock{now: testTime},
		IDGenerator: fixedIDGen("conv-1", "msg-user", "run-1", "msg-assistant"),
	})

	dto, err := service.SendMessage(context.Background(), ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser, DisplayName: "本地用户"}, SendMessageCommand{Content: "拉一个前端进群", IdempotencyKey: "send-1"})
	if err != nil {
		t.Fatal(err)
	}

	if len(runner.requests) != 2 {
		t.Fatalf("runner requests = %d, want initial + tool-result continuation", len(runner.requests))
	}
	continuation := runner.requests[1]
	if continuation.Mode != discussionToolResultContinuationMode || !strings.Contains(continuation.Content, "discussion_member_add") || !strings.Contains(continuation.Content, "added_count=1") {
		t.Fatalf("continuation request = %+v", continuation)
	}
	if len(dto.Messages) < 2 || !strings.Contains(dto.Messages[len(dto.Messages)-1].Content, "已拉林前端进来，成员列表已经更新。") {
		t.Fatalf("messages = %+v", dto.Messages)
	}
	namesByID := map[string]string{}
	for _, member := range dto.Members {
		namesByID[member.ID] = member.DisplayName
	}
	if namesByID["agent-frontend"] != "林前端" {
		t.Fatalf("members = %+v", dto.Members)
	}
}

func TestServiceSendMessageDoesNotContinueWhenRuntimeToolLoopCompleted(t *testing.T) {
	repo := newMemoryRepository(t)
	runner := &capturingDiscussionAgentRunner{
		boundTools: []DiscussionAgentBoundTool{{ToolID: "discussion_member_add"}},
		response: DiscussionAgentResponse{
			AssistantMessage:  "已拉林前端进来，成员列表已经更新。",
			Status:            "completed",
			RuntimeKind:       "managed",
			ToolLoopCompleted: true,
			ToolCalls: []ToolCallDTO{{
				ToolCallID: "add-frontend",
				Name:       "discussion_member_add",
				Status:     "completed",
				Arguments: map[string]string{
					"agent_ids": "agent-frontend",
				},
				Result: map[string]string{
					"added_count":       "1",
					"added_agent_ids":   "agent-frontend",
					"added_agent_names": "林前端",
				},
			}},
		},
	}
	service := newTestService(ServiceOptions{
		Repository:      repo,
		DiscussionAgent: runner,
		AgentDirectory: &testAgentDirectory{entries: append(defaultTestAgentDirectory().(*testAgentDirectory).entries,
			agentcore.AgentDirectoryEntry{AgentProfileID: "agent-frontend", AgentInstanceID: "agent-frontend", DisplayName: "林前端", RealName: "林前端", RoleName: "Frontend", AgentKind: agentcore.AgentKindAI, Enabled: true},
		)},
		Clock:       fixedClock{now: testTime},
		IDGenerator: fixedIDGen("conv-1", "msg-user", "run-1", "msg-assistant"),
	})

	dto, err := service.SendMessage(context.Background(), ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser, DisplayName: "本地用户"}, SendMessageCommand{Content: "拉一个前端进群", IdempotencyKey: "send-1"})
	if err != nil {
		t.Fatal(err)
	}

	if len(runner.requests) != 1 {
		t.Fatalf("runner requests = %d, want no service-level continuation after runtime tool loop", len(runner.requests))
	}
	if len(dto.Messages) != 2 || !strings.Contains(dto.Messages[1].Content, "成员列表已经更新") {
		t.Fatalf("messages = %+v", dto.Messages)
	}
	for _, member := range dto.Members {
		if member.ID == "agent-frontend" {
			t.Fatalf("runtime-completed tool loop should not be replayed by service-level member add: %+v", dto.Members)
		}
	}
	if len(dto.Runs) != 1 || len(dto.Runs[0].ToolCalls) != 1 || dto.Runs[0].ToolCalls[0].Result["added_count"] != "1" {
		t.Fatalf("run tool calls = %+v", dto.Runs)
	}
}

func TestServiceMentionRejectsRuntimeInstanceConversationMemberID(t *testing.T) {
	repo := newMemoryRepository(t)
	runner := &capturingDiscussionAgentRunner{response: DiscussionAgentResponse{
		AssistantMessage: "[@秦若兰](mention://agent/staffing-agent) 请拉一位同事进群。",
		Status:           "completed",
		RuntimeKind:      "managed",
	}}
	responder := &capturingDiscussionAgentMentionResponder{response: DiscussionAgentResponse{
		Status:      "completed",
		RuntimeKind: "managed",
		ToolCalls: []ToolCallDTO{{
			ToolCallID: "add-members",
			Name:       "discussion_member_add",
			Status:     "completed",
			Arguments: map[string]string{
				"agent_ids": "agent-frontend:conv-a",
			},
		}},
	}}
	service := newTestService(ServiceOptions{
		Repository:       repo,
		DiscussionAgent:  runner,
		MentionResponder: responder,
		AgentDirectory: &testAgentDirectory{entries: append(defaultTestAgentDirectory().(*testAgentDirectory).entries,
			agentcore.AgentDirectoryEntry{AgentProfileID: "agent-frontend", AgentInstanceID: "agent-frontend:conv-a", DisplayName: "林前端", RealName: "林前端", RoleName: "Frontend", AgentKind: agentcore.AgentKindAI, Enabled: true},
		)},
		Clock:       fixedClock{now: testTime},
		IDGenerator: fixedIDGen("conv-1", "msg-user", "run-1", "msg-assistant", "run-mention", "event-failure"),
	})

	dto, err := service.SendMessage(context.Background(), ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser, DisplayName: "本地用户"}, SendMessageCommand{Content: "请拉一位同事进来", IdempotencyKey: "send-1"})
	if err != nil {
		t.Fatal(err)
	}

	if len(dto.Events) == 0 || dto.Events[len(dto.Events)-1].Kind != "discussion_agent_mention_failed" {
		t.Fatalf("events = %+v, want failed mention event", dto.Events)
	}
	for _, member := range dto.Members {
		if member.ID == "agent-frontend" {
			t.Fatalf("runtime instance id should not add stable discussion member: %+v", dto.Members)
		}
	}
}

func TestServiceMentionRejectsAddedAgentMemberWithoutProfileIdentity(t *testing.T) {
	repo := newMemoryRepository(t)
	runner := &capturingDiscussionAgentRunner{response: DiscussionAgentResponse{
		AssistantMessage: "[@秦若兰](mention://agent/staffing-agent) 请拉一位同事进群。",
		Status:           "completed",
		RuntimeKind:      "managed",
	}}
	responder := &capturingDiscussionAgentMentionResponder{response: DiscussionAgentResponse{
		Status:      "completed",
		RuntimeKind: "managed",
		ToolCalls: []ToolCallDTO{{
			ToolCallID: "add-members",
			Name:       "discussion_member_add",
			Status:     "completed",
			Arguments: map[string]string{
				"agent_ids": "agent-runtime-only",
			},
		}},
	}}
	service := newTestService(ServiceOptions{
		Repository:       repo,
		DiscussionAgent:  runner,
		MentionResponder: responder,
		AgentDirectory: &testAgentDirectory{entries: append(defaultTestAgentDirectory().(*testAgentDirectory).entries,
			agentcore.AgentDirectoryEntry{AgentInstanceID: "agent-runtime-only", DisplayName: "林前端", RealName: "林前端", RoleName: "Frontend", AgentKind: agentcore.AgentKindAI, Enabled: true},
		)},
		Clock:       fixedClock{now: testTime},
		IDGenerator: fixedIDGen("conv-1", "msg-user", "run-1", "msg-assistant", "run-mention", "event-failure"),
	})

	dto, err := service.SendMessage(context.Background(), ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser, DisplayName: "本地用户"}, SendMessageCommand{Content: "请拉一位同事进来", IdempotencyKey: "send-1"})
	if err != nil {
		t.Fatal(err)
	}

	if len(dto.Events) == 0 || dto.Events[len(dto.Events)-1].Kind != "discussion_agent_mention_failed" {
		t.Fatalf("events = %+v, want failed mention event", dto.Events)
	}
	for _, member := range dto.Members {
		if member.ID == "agent-runtime-only" {
			t.Fatalf("runtime-only agent should not be added as discussion member: %+v", dto.Members)
		}
	}
}

func TestServiceMentionCanAddHumanAgentMembersFromDirectory(t *testing.T) {
	repo := newMemoryRepository(t)
	runner := &capturingDiscussionAgentRunner{response: DiscussionAgentResponse{
		AssistantMessage: "[@秦若兰](mention://agent/staffing-agent) 请把真人评审加入讨论。",
		Status:           "completed",
		RuntimeKind:      "managed",
	}}
	responder := &capturingDiscussionAgentMentionResponder{response: DiscussionAgentResponse{
		AssistantMessage: "已加入真人评审。",
		Status:           "completed",
		RuntimeKind:      "managed",
		ToolCalls: []ToolCallDTO{{
			ToolCallID: "add-human-member",
			Name:       "discussion_member_add",
			Status:     "completed",
			Arguments: map[string]string{
				"agent_ids":   "human-reviewer",
				"member_role": "评审",
			},
		}},
	}}
	service := newTestService(ServiceOptions{
		Repository:       repo,
		DiscussionAgent:  runner,
		MentionResponder: responder,
		AgentDirectory: &filteringTestAgentDirectory{entries: append(defaultTestAgentDirectory().(*testAgentDirectory).entries,
			agentcore.AgentDirectoryEntry{AgentProfileID: "human-reviewer", AgentInstanceID: "human-reviewer", DisplayName: "沈评审", RealName: "沈评审", RoleName: "Reviewer", AgentKind: agentcore.AgentKindHuman, Enabled: true},
		)},
		Clock:       fixedClock{now: testTime},
		IDGenerator: fixedIDGen("conv-1", "msg-user", "run-1", "msg-assistant", "run-staffing", "msg-staffing"),
	})

	dto, err := service.SendMessage(context.Background(), ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser, DisplayName: "本地用户"}, SendMessageCommand{Content: "请把真人评审拉进来", IdempotencyKey: "send-1"})
	if err != nil {
		t.Fatal(err)
	}

	namesByID := map[string]string{}
	rolesByID := map[string]string{}
	for _, member := range dto.Members {
		namesByID[member.ID] = member.DisplayName
		rolesByID[member.ID] = member.Role
	}
	if namesByID["human-reviewer"] != "沈评审" || rolesByID["human-reviewer"] != "评审" {
		t.Fatalf("members = %+v", dto.Members)
	}
	if len(dto.Runs) != 2 || len(dto.Runs[1].ToolCalls) != 1 || dto.Runs[1].ToolCalls[0].Name != "discussion_member_add" {
		t.Fatalf("mention run tool calls = %+v", dto.Runs)
	}
}

func TestServiceMentionAckToolRecordsReceivedEventWithoutAssistantMessage(t *testing.T) {
	repo := newMemoryRepository(t)
	runner := &capturingDiscussionAgentRunner{response: DiscussionAgentResponse{
		AssistantMessage: "[@秦若兰](mention://agent/staffing-agent) 请确认一下。",
		Status:           "completed",
		RuntimeKind:      "managed",
	}}
	responder := &capturingDiscussionAgentMentionResponder{response: DiscussionAgentResponse{
		Status:      "completed",
		RuntimeKind: "managed",
		ToolCalls: []ToolCallDTO{{
			ToolCallID: "ack-1",
			Name:       "discussion_ack",
			Status:     "completed",
			Arguments: map[string]string{
				"emoji": "👍",
			},
		}},
	}}
	service := newTestService(ServiceOptions{
		Repository:       repo,
		DiscussionAgent:  runner,
		MentionResponder: responder,
		Clock:            fixedClock{now: testTime},
		IDGenerator:      fixedIDGen("conv-1", "msg-user", "run-1", "msg-assistant", "run-staffing", "event-ack"),
	})

	dto, err := service.SendMessage(context.Background(), ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser, DisplayName: "本地用户"}, SendMessageCommand{Content: "让 Staffing 确认", IdempotencyKey: "send-1"})
	if err != nil {
		t.Fatal(err)
	}

	if len(dto.Messages) != 2 {
		t.Fatalf("messages = %+v, want no assistant text for ack-only response", dto.Messages)
	}
	if len(dto.Runs) != 2 || len(dto.Runs[1].ToolCalls) != 1 || dto.Runs[1].ToolCalls[0].Name != "discussion_ack" || dto.Runs[1].ToolCalls[0].Result["emoji"] != "👍" {
		t.Fatalf("ack run tool calls = %+v", dto.Runs)
	}
	if len(responder.requests) != 1 {
		t.Fatalf("ack should not request tool-result continuation, requests = %+v", responder.requests)
	}
	if len(dto.Events) != 1 {
		t.Fatalf("events = %+v", dto.Events)
	}
	event := dto.Events[0]
	if event.Kind != "discussion_agent_acknowledged" || event.Status != "completed" || event.Metadata["emoji"] != "👍" || event.Metadata["source_message_id"] != "msg-assistant" || event.Metadata["target_agent_id"] != "staffing-agent" {
		t.Fatalf("ack event = %+v", event)
	}
}
