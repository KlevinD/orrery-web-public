package discussionsession

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	missioncontrol "orrery_v2/internal/app/missioncontrol"
)

func TestServiceSendMessageAppliesMissionToolResultsAndPersistsLinks(t *testing.T) {
	repo := newMemoryRepository(t)
	runner := &capturingDiscussionAgentRunner{response: DiscussionAgentResponse{
		AssistantMessage: "I created the Mission.",
		Status:           "completed",
		RuntimeKind:      "openclaw",
		ToolCalls: []ToolCallDTO{
			{
				ToolCallID: "tool-1",
				Name:       "mission_brief_create",
				Status:     "completed",
				Arguments:  map[string]string{"title": "Launch plan", "final_goal": "Coordinate launch"},
			},
			{
				ToolCallID: "tool-2",
				Name:       "mission_task_create_root",
				Status:     "completed",
				Arguments:  map[string]string{"goal": "Coordinate launch", "worker_personal_goal": "Own launch coordination", "assignee_id": "agent-worker", "idempotency_key": "root-task"},
			},
			{
				ToolCallID: "tool-3",
				Name:       "mission_coordinator_plan_parallel",
				Status:     "completed",
				Arguments:  map[string]string{"goal": "Continue execution", "worker_lanes_json": `[{"role":"builder","agent_id":"agent-worker","goal":"Build launch plan","idempotency_key":"lane-build"}]`, "idempotency_key": "coordinator-plan"},
			},
		},
	}}
	applier := &capturingMissionToolApplier{links: []LinkedMissionDTO{{MissionID: "mission-1", Title: "Launch plan", Status: "active", CreatedAt: testTime}}}
	service := newTestService(ServiceOptions{
		Repository:      repo,
		DiscussionAgent: runner,
		MissionTooling:  applier,
		Clock:           fixedClock{now: testTime},
		IDGenerator:     fixedIDGen("conv-1", "msg-user", "run-1", "msg-assistant"),
	})

	dto, err := service.SendMessage(context.Background(), ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser, DisplayName: "本地用户"}, SendMessageCommand{Content: "Plan a launch", IdempotencyKey: "send-1"})
	if err != nil {
		t.Fatal(err)
	}

	if !applier.called || len(applier.calls) != 3 || applier.calls[0].Name != "mission_brief_create" || applier.calls[1].Name != "mission_task_create_root" || applier.calls[2].Name != "mission_coordinator_plan_parallel" {
		t.Fatalf("applier calls = %+v", applier.calls)
	}
	if applier.viewer.ViewerID != "lead-agent" || applier.viewer.ViewerKind != missioncontrol.ViewerKindAgent {
		t.Fatalf("applier viewer = %+v, want lead-agent agent", applier.viewer)
	}
	if applier.calls[0].Arguments["initial_human_agent_id"] != "user-1" {
		t.Fatalf("initial human agent was not injected into Lead mission tool call: %+v", applier.calls[0])
	}
	if applier.calls[0].Arguments["initial_human_display_name"] != "本地用户" {
		t.Fatalf("initial human display name was not injected into Lead mission tool call: %+v", applier.calls[0])
	}
	if len(dto.LinkedMissions) != 1 || dto.LinkedMissions[0].MissionID != "mission-1" {
		t.Fatalf("linked missions = %+v", dto.LinkedMissions)
	}
}

func TestServiceSendMessageLeadAckToolRecordsVisibleAcknowledgement(t *testing.T) {
	repo := newMemoryRepository(t)
	runner := &capturingDiscussionAgentRunner{response: DiscussionAgentResponse{
		Status:      "completed",
		RuntimeKind: "managed",
		ToolCalls: []ToolCallDTO{{
			ToolCallID: "ack-1",
			Name:       "discussion_ack",
			Status:     "completed",
			Arguments:  map[string]string{"emoji": "👍"},
		}},
	}}
	service := newTestService(ServiceOptions{
		Repository:      repo,
		DiscussionAgent: runner,
		Clock:           fixedClock{now: testTime},
		IDGenerator:     fixedIDGen("conv-1", "msg-user", "run-1", "event-ack"),
	})

	dto, err := service.SendMessage(context.Background(), ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser, DisplayName: "本地用户"}, SendMessageCommand{Content: "给我这条消息点个赞 不要回复。", IdempotencyKey: "send-1"})
	if err != nil {
		t.Fatal(err)
	}

	if len(dto.Messages) != 1 {
		t.Fatalf("messages = %+v, want no assistant text for ack-only response", dto.Messages)
	}
	if len(dto.Runs) != 1 || len(dto.Runs[0].ToolCalls) != 1 || dto.Runs[0].ToolCalls[0].Result["emoji"] != "👍" {
		t.Fatalf("ack tool result = %+v", dto.Runs)
	}
	if len(dto.Events) != 1 {
		t.Fatalf("events = %+v", dto.Events)
	}
	event := dto.Events[0]
	if event.Kind != "discussion_agent_acknowledged" || event.Metadata["source_message_id"] != "msg-user" || event.Metadata["target_agent_id"] != "lead-agent" || event.Metadata["target_name"] != "顾明澈" || event.Metadata["emoji"] != "👍" {
		t.Fatalf("ack event = %+v", event)
	}
}

func TestServiceSendMessageContinuesAgentAfterMissionToolResults(t *testing.T) {
	repo := newMemoryRepository(t)
	prepare := ToolCallDTO{
		ToolCallID: "prepare-1",
		Name:       "mission_prepare",
		Status:     "completed",
		Arguments: map[string]string{
			"title":      "穿墙贪吃蛇小游戏",
			"final_goal": "交付单 HTML 文件穿墙贪吃蛇",
			"root_goal":  "团队目标：交付单 HTML 文件穿墙贪吃蛇",
		},
	}
	runner := &capturingDiscussionAgentRunner{
		boundTools: []DiscussionAgentBoundTool{{ToolID: "mission_prepare"}},
		responses: []DiscussionAgentResponse{
			{Status: "completed", RuntimeKind: "managed", ToolCalls: []ToolCallDTO{prepare}, Usage: ModelUsageMap{"gpt-test": {InputTokens: 10, OutputTokens: 4, TotalTokens: 14}}, DurationMs: 120},
			{AssistantMessage: "[@秦若兰](mention://agent/staffing-agent) Mission 已准备好，请继续安排执行人员。", Status: "completed", RuntimeKind: "managed", Usage: ModelUsageMap{"gpt-test": {InputTokens: 6, OutputTokens: 3, CacheReadTokens: 2, TotalTokens: 9}}, DurationMs: 80},
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

	dto, err := service.SendMessage(context.Background(), ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser, DisplayName: "本地用户"}, SendMessageCommand{Content: "对 就 html 就行", IdempotencyKey: "send-1"})
	if err != nil {
		t.Fatal(err)
	}

	if len(runner.requests) != 2 {
		t.Fatalf("runner requests = %d, want continuation after tool results", len(runner.requests))
	}
	continuation := runner.requests[1]
	if continuation.Mode != "tool_result_continuation" || !strings.Contains(continuation.Content, "mission_prepare") || !strings.Contains(continuation.Content, "mission-1") || !strings.Contains(continuation.Content, "root-mission-1") {
		t.Fatalf("continuation request = %+v", continuation)
	}
	if len(dto.Messages) != 2 || !strings.Contains(dto.Messages[1].Content, "mention://agent/staffing-agent") {
		t.Fatalf("messages = %+v, want continued agent handoff", dto.Messages)
	}
	if len(dto.Runs) != 1 || len(dto.Runs[0].ToolCalls) != 1 || dto.Runs[0].ToolCalls[0].Result["mission_id"] != "mission-1" || dto.Runs[0].ToolCalls[0].Result["task_id"] != "root-mission-1" {
		t.Fatalf("run tool results = %+v", dto.Runs)
	}
	if usage := dto.Runs[0].Usage["gpt-test"]; usage.InputTokens != 16 || usage.OutputTokens != 7 || usage.CacheReadTokens != 2 || usage.TotalTokens != 23 {
		t.Fatalf("merged run usage = %#v", dto.Runs[0].Usage)
	}
	if dto.Runs[0].DurationMs != 200 {
		t.Fatalf("merged run duration = %d", dto.Runs[0].DurationMs)
	}
}

func TestServiceSendMessageAllowsMissionBriefWithUnassignedRootTask(t *testing.T) {
	repo := newMemoryRepository(t)
	runner := &capturingDiscussionAgentRunner{response: DiscussionAgentResponse{
		AssistantMessage: "Mission 和团队根任务已创建，后续由 Lead 补充人员并推进任务分解。",
		Status:           "completed",
		RuntimeKind:      "managed",
		ToolCalls: []ToolCallDTO{
			{
				ToolCallID: "tool-brief",
				Name:       "mission_brief_create",
				Status:     "completed",
				Arguments:  map[string]string{"title": "穿墙贪吃蛇小游戏", "final_goal": "交付单 HTML 文件穿墙贪吃蛇"},
			},
			{
				ToolCallID: "tool-root",
				Name:       "mission_task_create_root",
				Status:     "completed",
				Arguments:  map[string]string{"goal": "团队目标：交付单 HTML 文件穿墙贪吃蛇", "idempotency_key": "root-task"},
			},
		},
	}}
	applier := &capturingMissionToolApplier{links: []LinkedMissionDTO{{MissionID: "mission-1", Title: "穿墙贪吃蛇小游戏", Status: "active", CreatedAt: testTime}}}
	service := newTestService(ServiceOptions{
		Repository:      repo,
		DiscussionAgent: runner,
		MissionTooling:  applier,
		Clock:           fixedClock{now: testTime},
		IDGenerator:     fixedIDGen("conv-1", "msg-user", "run-1", "msg-assistant"),
	})

	dto, err := service.SendMessage(context.Background(), ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser, DisplayName: "本地用户"}, SendMessageCommand{Content: "对 就 html 就行", IdempotencyKey: "send-1"})
	if err != nil {
		t.Fatalf("SendMessage returned error: %v", err)
	}

	if !applier.called || len(applier.calls) != 2 || applier.calls[1].Arguments["assignee_id"] != "" || applier.calls[1].Arguments["worker_personal_goal"] != "" {
		t.Fatalf("applier calls = %+v", applier.calls)
	}
	if dto.Status == "failed" || len(dto.Messages) != 2 || !strings.Contains(dto.Messages[1].Content, "Mission 和团队根任务已创建，后续由 Lead 补充人员并推进任务分解。") {
		t.Fatalf("conversation should complete with assistant message, dto = %+v", dto)
	}
	if len(dto.Events) != 0 {
		t.Fatalf("unexpected failure events: %+v", dto.Events)
	}
}

func TestServiceSendMessageRejectsUnboundMissionToolCallWithoutApplyingTooling(t *testing.T) {
	repo := newMemoryRepository(t)
	runner := &capturingDiscussionAgentRunner{unboundTools: true, response: DiscussionAgentResponse{
		AssistantMessage: "I created the Mission.",
		Status:           "completed",
		RuntimeKind:      "managed",
		ToolCalls: []ToolCallDTO{{
			ToolCallID: "tool-brief",
			Name:       "mission_brief_create",
			Status:     "completed",
			Arguments:  map[string]string{"title": "Launch plan", "final_goal": "Coordinate launch"},
		}},
	}}
	applier := &capturingMissionToolApplier{}
	service := newTestService(ServiceOptions{
		Repository:      repo,
		DiscussionAgent: runner,
		MissionTooling:  applier,
		Clock:           fixedClock{now: testTime},
		IDGenerator:     fixedIDGen("conv-1", "msg-user", "run-1", "event-failed"),
	})

	_, err := service.SendMessage(context.Background(), ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser}, SendMessageCommand{Content: "Plan a launch", IdempotencyKey: "send-1"})
	if !errors.Is(err, ErrOrchestrationIncomplete) {
		t.Fatalf("SendMessage error = %v, want ErrOrchestrationIncomplete", err)
	}
	if applier.called {
		t.Fatalf("unbound tool call should not reach mission tooling: %+v", applier.calls)
	}
	detail, getErr := service.GetConversation(context.Background(), ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser}, "conv-1")
	if getErr != nil {
		t.Fatal(getErr)
	}
	if detail.Status != "failed" || len(detail.Runs) != 1 || detail.Runs[0].Status != "failed" || len(detail.Runs[0].ToolCalls) != 1 {
		t.Fatalf("conversation should record failed unbound tool run: %+v", detail)
	}
	if len(detail.Events) != 1 || !strings.Contains(detail.Events[0].Summary, "unbound tool call mission_brief_create") {
		t.Fatalf("failure event = %+v, want unbound tool diagnostic", detail.Events)
	}
}

func TestServiceSendMessageRejectsUnboundMissionToolCallBeforeToolingDependency(t *testing.T) {
	repo := newMemoryRepository(t)
	runner := &capturingDiscussionAgentRunner{unboundTools: true, response: DiscussionAgentResponse{
		AssistantMessage: "I created the Mission.",
		Status:           "completed",
		RuntimeKind:      "managed",
		ToolCalls: []ToolCallDTO{{
			ToolCallID: "tool-brief",
			Name:       "mission_brief_create",
			Status:     "completed",
			Arguments:  map[string]string{"title": "Launch plan", "final_goal": "Coordinate launch"},
		}},
	}}
	service := newTestService(ServiceOptions{
		Repository:      repo,
		DiscussionAgent: runner,
		Clock:           fixedClock{now: testTime},
		IDGenerator:     fixedIDGen("conv-1", "msg-user", "run-1", "event-failed"),
	})

	_, err := service.SendMessage(context.Background(), ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser}, SendMessageCommand{Content: "Plan a launch", IdempotencyKey: "send-1"})
	if !errors.Is(err, ErrOrchestrationIncomplete) || !strings.Contains(err.Error(), "unbound tool call mission_brief_create") {
		t.Fatalf("SendMessage error = %v, want unbound tool orchestration failure", err)
	}
}

func TestServiceSendMessageAllowsMissionPreparationWithoutImmediateExecutionContinuation(t *testing.T) {
	repo := newMemoryRepository(t)
	runner := &capturingDiscussionAgentRunner{response: DiscussionAgentResponse{
		AssistantMessage: "I created and delegated the root task.",
		Status:           "completed",
		RuntimeKind:      "managed",
		ToolCalls: []ToolCallDTO{
			{
				ToolCallID: "tool-brief",
				Name:       "mission_brief_create",
				Status:     "completed",
				Arguments:  map[string]string{"title": "HTML Snake", "final_goal": "Build a snake game"},
			},
			{
				ToolCallID: "tool-root",
				Name:       "mission_task_create_root",
				Status:     "completed",
				Arguments:  map[string]string{"goal": "Build snake", "worker_personal_goal": "Deliver HTML snake", "assignee_id": "agent-worker", "idempotency_key": "root"},
			},
			{
				ToolCallID: "tool-delegate",
				Name:       "mission_task_delegate",
				Status:     "completed",
				Arguments:  map[string]string{"assignee_id": "agent-worker", "reason": "Assign root task", "idempotency_key": "delegate"},
			},
		},
	}}
	applier := &capturingMissionToolApplier{links: []LinkedMissionDTO{{MissionID: "mission-1", Title: "HTML Snake", Status: "active", CreatedAt: testTime}}}
	service := newTestService(ServiceOptions{
		Repository:      repo,
		DiscussionAgent: runner,
		MissionTooling:  applier,
		Clock:           fixedClock{now: testTime},
		IDGenerator:     fixedIDGen("conv-1", "msg-user", "run-1", "msg-assistant"),
	})

	dto, err := service.SendMessage(context.Background(), ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser}, SendMessageCommand{Content: "做个 HTML 贪吃蛇", IdempotencyKey: "send-1"})
	if err != nil {
		t.Fatalf("SendMessage returned error: %v", err)
	}
	if dto.Status == "failed" || len(dto.Runs) != 1 || dto.Runs[0].Status != "completed" {
		t.Fatalf("conversation should stay completed for Mission preparation: %+v", dto)
	}
	if len(dto.Messages) != 2 || !strings.Contains(dto.Messages[1].Content, "I created and delegated the root task.") {
		t.Fatalf("assistant message should persist after preparation tools: %+v", dto.Messages)
	}
	if len(dto.Events) != 0 {
		t.Fatalf("unexpected failure events: %+v", dto.Events)
	}
}

func TestServiceSendMessageFailsWhenLeadCreatesMissionWithoutRootTask(t *testing.T) {
	repo := newMemoryRepository(t)
	runner := &capturingDiscussionAgentRunner{response: DiscussionAgentResponse{
		AssistantMessage: "I created the Mission.",
		Status:           "completed",
		RuntimeKind:      "managed",
		ToolCalls: []ToolCallDTO{{
			ToolCallID: "tool-brief",
			Name:       "mission_brief_create",
			Status:     "completed",
			Arguments:  map[string]string{"title": "HTML Snake", "final_goal": "Build a snake game"},
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

	_, err := service.SendMessage(context.Background(), ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser}, SendMessageCommand{Content: "做个 HTML 贪吃蛇", IdempotencyKey: "send-1"})
	if !errors.Is(err, ErrOrchestrationIncomplete) {
		t.Fatalf("SendMessage error = %v, want ErrOrchestrationIncomplete", err)
	}
	detail, getErr := service.GetConversation(context.Background(), ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser}, "conv-1")
	if getErr != nil {
		t.Fatal(getErr)
	}
	if detail.Status != "failed" || len(detail.Runs) != 1 || detail.Runs[0].Status != "failed" {
		t.Fatalf("conversation should fail loudly, detail = %+v", detail)
	}
	if len(detail.LinkedMissions) != 1 || detail.LinkedMissions[0].MissionID != "mission-1" {
		t.Fatalf("created mission should remain visible for diagnosis: %+v", detail.LinkedMissions)
	}
	if len(detail.Events) != 1 || detail.Events[0].Kind != "discussion_orchestration_incomplete" || !strings.Contains(detail.Events[0].Summary, "usable root task") {
		t.Fatalf("failure event = %+v, want orchestration diagnostic", detail.Events)
	}
}

func TestServiceSendMessageFailsWhenLeadStopsWithUnresolvedTodoTool(t *testing.T) {
	repo := newMemoryRepository(t)
	runner := &capturingDiscussionAgentRunner{response: DiscussionAgentResponse{
		AssistantMessage: "I created the Mission and requested approval.",
		Status:           "completed",
		RuntimeKind:      "managed",
		ToolCalls: []ToolCallDTO{
			{
				ToolCallID: "tool-brief",
				Name:       "mission_brief_create",
				Status:     "completed",
				Arguments:  map[string]string{"title": "HTML Snake", "final_goal": "Build a snake game"},
			},
			{
				ToolCallID: "tool-root",
				Name:       "mission_task_create_root",
				Status:     "completed",
				Arguments:  map[string]string{"goal": "Build snake", "worker_personal_goal": "Deliver HTML snake", "assignee_id": "agent-worker", "idempotency_key": "root"},
			},
			{
				ToolCallID: "tool-delegate",
				Name:       "mission_coordinator_plan_parallel",
				Status:     "completed",
				Arguments:  map[string]string{"goal": "Continue execution", "worker_lanes_json": `[{"role":"builder","agent_id":"agent-worker","goal":"Build snake","idempotency_key":"lane-build"}]`, "idempotency_key": "coordinator"},
			},
			{
				ToolCallID: "tool-approve",
				Name:       "mission_worker_request_approve",
				Status:     "todo",
				Arguments:  map[string]string{"request_id": "req-1"},
			},
		},
	}}
	applier := &capturingMissionToolApplier{links: []LinkedMissionDTO{{MissionID: "mission-1", Title: "HTML Snake", Status: "active", CreatedAt: testTime}}}
	service := newTestService(ServiceOptions{
		Repository:      repo,
		DiscussionAgent: runner,
		MissionTooling:  applier,
		Clock:           fixedClock{now: testTime},
		IDGenerator:     fixedIDGen("conv-1", "msg-user", "run-1", "msg-assistant"),
	})

	_, err := service.SendMessage(context.Background(), ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser}, SendMessageCommand{Content: "做个 HTML 贪吃蛇", IdempotencyKey: "send-1"})
	if !errors.Is(err, ErrOrchestrationIncomplete) {
		t.Fatalf("SendMessage error = %v, want ErrOrchestrationIncomplete", err)
	}
	detail, getErr := service.GetConversation(context.Background(), ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser}, "conv-1")
	if getErr != nil {
		t.Fatal(getErr)
	}
	if detail.Status != "failed" || len(detail.Runs) != 1 || detail.Runs[0].Status != "failed" {
		t.Fatalf("conversation should fail loudly, detail = %+v", detail)
	}
	if len(detail.Events) != 1 || !strings.Contains(detail.Events[0].Summary, "mission_worker_request_approve") {
		t.Fatalf("failure event = %+v, want unresolved todo diagnostic", detail.Events)
	}
}

func TestServiceSendMessageFailsWhenLeadToolFinishesUnresolved(t *testing.T) {
	repo := newMemoryRepository(t)
	runner := &capturingDiscussionAgentRunner{response: DiscussionAgentResponse{
		AssistantMessage: "I tried to continue execution.",
		Status:           "completed",
		RuntimeKind:      "managed",
		ToolCalls: []ToolCallDTO{
			{
				ToolCallID: "tool-brief",
				Name:       "mission_brief_create",
				Status:     "completed",
				Arguments:  map[string]string{"title": "HTML Snake", "final_goal": "Build a snake game"},
			},
			{
				ToolCallID: "tool-root",
				Name:       "mission_task_create_root",
				Status:     "completed",
				Arguments:  map[string]string{"goal": "Build snake", "worker_personal_goal": "Deliver HTML snake", "assignee_id": "agent-worker", "idempotency_key": "root"},
			},
			{
				ToolCallID: "tool-coordinator",
				Name:       "mission_coordinator_plan_parallel",
				Status:     "failed",
				Arguments:  map[string]string{"goal": "Continue execution", "worker_lanes_json": `[{"role":"builder","agent_id":"agent-worker","goal":"Build snake","idempotency_key":"lane-build"}]`, "idempotency_key": "coordinator"},
			},
		},
	}}
	applier := &capturingMissionToolApplier{links: []LinkedMissionDTO{{MissionID: "mission-1", Title: "HTML Snake", Status: "active", CreatedAt: testTime}}}
	service := newTestService(ServiceOptions{
		Repository:      repo,
		DiscussionAgent: runner,
		MissionTooling:  applier,
		Clock:           fixedClock{now: testTime},
		IDGenerator:     fixedIDGen("conv-1", "msg-user", "run-1", "msg-assistant"),
	})

	_, err := service.SendMessage(context.Background(), ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser}, SendMessageCommand{Content: "做个 HTML 贪吃蛇", IdempotencyKey: "send-1"})
	if !errors.Is(err, ErrOrchestrationIncomplete) {
		t.Fatalf("SendMessage error = %v, want ErrOrchestrationIncomplete", err)
	}
	detail, getErr := service.GetConversation(context.Background(), ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser}, "conv-1")
	if getErr != nil {
		t.Fatal(getErr)
	}
	if detail.Status != "failed" || len(detail.Runs) != 1 || detail.Runs[0].Status != "failed" {
		t.Fatalf("conversation should fail loudly, detail = %+v", detail)
	}
	if len(detail.Events) != 1 || !strings.Contains(detail.Events[0].Summary, "mission_coordinator_plan_parallel") || !strings.Contains(detail.Events[0].Summary, "status failed") {
		t.Fatalf("failure event = %+v, want unresolved failed-tool diagnostic", detail.Events)
	}
}

func TestServiceSendMessageLetsAgentCorrectFailedMissionToolArguments(t *testing.T) {
	repo := newMemoryRepository(t)
	runner := &capturingDiscussionAgentRunner{responses: []DiscussionAgentResponse{
		{
			Status:      "completed",
			RuntimeKind: "managed",
			ToolCalls: []ToolCallDTO{{
				ToolCallID: "tool-plan-invalid",
				Name:       "mission_coordinator_plan_parallel",
				Status:     "completed",
				Arguments:  map[string]string{"worker_lanes": `[{"goal":"Build snake"}]`, "idempotency_key": "plan-1"},
			}},
		},
		{
			AssistantMessage:  "参数已修正，任务已分配。",
			Status:            "completed",
			RuntimeKind:       "managed",
			ToolLoopCompleted: true,
			ToolCalls: []ToolCallDTO{{
				ToolCallID: "tool-plan-corrected",
				Name:       "mission_coordinator_plan_parallel",
				Status:     "completed",
				Arguments:  map[string]string{"worker_lanes": `[{"goal":"Build snake","assignee_id":"agent-worker"}]`, "idempotency_key": "plan-2"},
			}},
		},
	}}
	applier := &capturingMissionToolApplier{
		links:  []LinkedMissionDTO{{MissionID: "mission-1", Title: "HTML Snake", Status: "active", CreatedAt: testTime}},
		errors: []error{errors.New("coordinator worker lane 1 assignee_id is required"), nil},
	}
	service := newTestService(ServiceOptions{
		Repository:      repo,
		DiscussionAgent: runner,
		MissionTooling:  applier,
		Clock:           fixedClock{now: testTime},
		IDGenerator:     fixedIDGen("conv-1", "msg-user", "run-1", "msg-assistant"),
	})

	dto, err := service.SendMessage(context.Background(), ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser}, SendMessageCommand{Content: "做个 HTML 贪吃蛇", IdempotencyKey: "send-1"})
	if err != nil {
		t.Fatalf("corrected tool call should complete: %v", err)
	}
	if len(runner.requests) != 2 || !strings.Contains(runner.requests[1].Content, "assignee_id is required") || !strings.Contains(runner.requests[1].Content, "修正参数后再次调用") {
		t.Fatalf("tool correction continuation = %+v", runner.requests)
	}
	if len(dto.Runs) != 1 || len(dto.Runs[0].ToolCalls) != 2 {
		t.Fatalf("run tool calls = %+v", dto.Runs)
	}
	if dto.Runs[0].ToolCalls[0].Status != "failed" || !strings.Contains(dto.Runs[0].ToolCalls[0].Result["error"], "assignee_id") || dto.Runs[0].ToolCalls[1].Status != "completed" {
		t.Fatalf("failed attempt and corrected retry should both be auditable: %+v", dto.Runs[0].ToolCalls)
	}
}

func TestServiceSendMessagePreservesLeadAnswerWithWarningWhenMissionHasNoHandoff(t *testing.T) {
	repo := newMemoryRepository(t)
	runner := &capturingDiscussionAgentRunner{response: DiscussionAgentResponse{
		AssistantMessage: "[@本地用户](mention://user/user-1) 方案确认，现在开工！",
		Status:           "completed",
		RuntimeKind:      "managed",
		ToolCalls: []ToolCallDTO{{
			ToolCallID: "tool-prepare",
			Name:       "mission_prepare",
			Status:     "completed",
			Arguments: map[string]string{
				"title":      "穿墙贪吃蛇小游戏",
				"final_goal": "交付单 HTML 文件穿墙贪吃蛇",
				"root_goal":  "团队目标：交付单 HTML 文件穿墙贪吃蛇",
			},
		}},
	}}
	runner.boundTools = []DiscussionAgentBoundTool{{ToolID: "mission_prepare"}}
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

	_, err := service.SendMessage(context.Background(), ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser, DisplayName: "本地用户"}, SendMessageCommand{Content: "对 就 html 就行", IdempotencyKey: "send-1"})
	if err != nil {
		t.Fatal(err)
	}
	detail, getErr := service.GetConversation(context.Background(), ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser}, "conv-1")
	if getErr != nil {
		t.Fatal(getErr)
	}
	if detail.Status != "completed" || len(detail.Messages) != 2 || len(detail.Runs) != 1 || detail.Runs[0].Status != "completed" {
		t.Fatalf("conversation should preserve the Lead answer: %+v", detail)
	}
	if detail.Messages[1].Metadata[discussionOrchestrationWarningMetadataKey] != missionPrepareDirectAnswerWarning {
		t.Fatalf("assistant message warning = %+v", detail.Messages[1].Metadata)
	}
	if len(detail.LinkedMissions) != 1 || detail.LinkedMissions[0].MissionID != "mission-1" {
		t.Fatalf("prepared mission should remain linked for diagnosis: %+v", detail.LinkedMissions)
	}
	if len(detail.Events) != 0 {
		t.Fatalf("warning should not create a failure event: %+v", detail.Events)
	}
}

func TestServiceSendMessageAllowsLeadToStopAfterRootTaskPreparation(t *testing.T) {
	repo := newMemoryRepository(t)
	runner := &capturingDiscussionAgentRunner{response: DiscussionAgentResponse{
		AssistantMessage: "I created the root task.",
		Status:           "completed",
		RuntimeKind:      "managed",
		ToolCalls: []ToolCallDTO{
			{
				ToolCallID: "tool-brief",
				Name:       "mission_brief_create",
				Status:     "completed",
				Arguments:  map[string]string{"title": "HTML Snake", "final_goal": "Build a snake game"},
			},
			{
				ToolCallID: "tool-root",
				Name:       "mission_task_create_root",
				Status:     "completed",
				Arguments:  map[string]string{"goal": "Build snake", "worker_personal_goal": "Deliver HTML snake", "assignee_id": "agent-worker", "idempotency_key": "root"},
			},
		},
	}}
	applier := &capturingMissionToolApplier{links: []LinkedMissionDTO{{MissionID: "mission-1", Title: "HTML Snake", Status: "active", CreatedAt: testTime}}}
	service := newTestService(ServiceOptions{
		Repository:      repo,
		DiscussionAgent: runner,
		MissionTooling:  applier,
		Clock:           fixedClock{now: testTime},
		IDGenerator:     fixedIDGen("conv-1", "msg-user", "run-1", "msg-assistant"),
	})

	dto, err := service.SendMessage(context.Background(), ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser}, SendMessageCommand{Content: "做个 HTML 贪吃蛇", IdempotencyKey: "send-1"})
	if err != nil {
		t.Fatalf("SendMessage returned error: %v", err)
	}
	if dto.Status == "failed" || len(dto.Runs) != 1 || dto.Runs[0].Status != "completed" {
		t.Fatalf("conversation should complete after root task preparation: %+v", dto)
	}
	if len(dto.Events) != 0 {
		t.Fatalf("unexpected failure events: %+v", dto.Events)
	}
}

func TestServiceSendMessageReusesExistingMissionLinkForLaterTurns(t *testing.T) {
	repo := newMemoryRepository(t)
	if err := repo.SaveConversation(context.Background(), ConversationRecord{ConversationID: "conv-1", Title: "Launch plan", Status: "completed", CreatedBy: "user-1", CreatedAt: testTime, UpdatedAt: testTime}); err != nil {
		t.Fatal(err)
	}
	seedDiscussionMembers(t, repo, "conv-1", "user-1")
	if err := repo.SaveMissionLink(context.Background(), LinkedMissionRecord{ConversationID: "conv-1", MissionID: "mission-existing", Title: "Launch plan", Status: "active", CreatedAt: testTime}); err != nil {
		t.Fatal(err)
	}
	runner := &capturingDiscussionAgentRunner{response: DiscussionAgentResponse{
		AssistantMessage: "Continuing the existing Mission.",
		Status:           "completed",
		RuntimeKind:      "codex",
		ToolCalls: []ToolCallDTO{
			{
				ToolCallID: "tool-brief-duplicate",
				Name:       "mission_brief_create",
				Status:     "completed",
				Arguments:  map[string]string{"title": "Launch plan again", "final_goal": "Do not create another mission"},
			},
			{
				ToolCallID: "tool-root",
				Name:       "mission_task_create_root",
				Status:     "completed",
				Arguments:  map[string]string{"goal": "Continue launch work", "worker_personal_goal": "Continue existing mission", "assignee_id": "agent-worker", "idempotency_key": "root-task-later-turn"},
			},
			{
				ToolCallID: "tool-delegate",
				Name:       "mission_coordinator_plan_parallel",
				Status:     "completed",
				Arguments:  map[string]string{"goal": "Continue launch work", "worker_lanes_json": `[{"role":"worker","agent_id":"agent-worker","goal":"Continue launch","idempotency_key":"lane-later"}]`, "idempotency_key": "coordinator-later-turn"},
			},
		},
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

	dto, err := service.SendMessage(context.Background(), ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser}, SendMessageCommand{ConversationID: "conv-1", Content: "继续推进", IdempotencyKey: "send-2"})
	if err != nil {
		t.Fatal(err)
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
	if len(dto.LinkedMissions) != 1 || dto.LinkedMissions[0].MissionID != "mission-existing" {
		t.Fatalf("linked missions = %+v", dto.LinkedMissions)
	}
	if len(dto.Runs) != 1 || len(dto.Runs[0].ToolCalls) != 3 || dto.Runs[0].ToolCalls[0].Name != "mission_brief_create" || dto.Runs[0].ToolCalls[0].Result["operation"] != "updated" || dto.Runs[0].ToolCalls[1].Name != "mission_task_create_root" || dto.Runs[0].ToolCalls[2].Name != "mission_coordinator_plan_parallel" {
		t.Fatalf("later run should show mission brief update status, runs = %+v", dto.Runs)
	}
	if len(runner.request.LinkedMissions) != 1 || runner.request.LinkedMissions[0].MissionID != "mission-existing" {
		t.Fatalf("discussion agent request linked missions = %+v", runner.request.LinkedMissions)
	}
}

func TestServiceSendMessagePostsMultipleLeadMentionsToLinkedMissionDiscussion(t *testing.T) {
	repo := newMemoryRepository(t)
	if err := repo.SaveConversation(context.Background(), ConversationRecord{ConversationID: "conv-1", Title: "Launch plan", Status: "completed", CreatedBy: "user-1", CreatedAt: testTime, UpdatedAt: testTime}); err != nil {
		t.Fatal(err)
	}
	seedDiscussionMembers(t, repo, "conv-1", "user-1")
	if err := repo.SaveMissionLink(context.Background(), LinkedMissionRecord{ConversationID: "conv-1", MissionID: "mission-existing", Title: "Launch plan", Status: "active", CreatedAt: testTime}); err != nil {
		t.Fatal(err)
	}
	runner := &capturingDiscussionAgentRunner{response: DiscussionAgentResponse{
		AssistantMessage: "[@Staffing](mention://agent/staffing-agent) [@Coordinator](mention://agent/coordinator-agent) 请一起确认执行人和排期。",
		Status:           "completed",
		RuntimeKind:      "managed",
	}}
	messenger := &capturingMissionMessenger{}
	service := newTestService(ServiceOptions{
		Repository:       repo,
		DiscussionAgent:  runner,
		MissionMessenger: messenger,
		Clock:            fixedClock{now: testTime.Add(time.Minute)},
		IDGenerator:      fixedIDGen("msg-user-2", "run-2", "msg-assistant-2"),
	})

	_, err := service.SendMessage(context.Background(), ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser}, SendMessageCommand{ConversationID: "conv-1", Content: "继续推进", IdempotencyKey: "send-2"})
	if err != nil {
		t.Fatal(err)
	}

	if !messenger.called || messenger.viewer.ViewerID != "lead-agent" || messenger.mission.MissionID != "mission-existing" {
		t.Fatalf("mission messenger = %+v", messenger)
	}
	if messenger.content != "[@Staffing](mention://agent/staffing-agent) [@Coordinator](mention://agent/coordinator-agent) 请一起确认执行人和排期。" {
		t.Fatalf("mission mention content = %q", messenger.content)
	}
}

func TestServiceSendMessageDoesNotPostPlainAtMentionsToMissionDiscussion(t *testing.T) {
	repo := newMemoryRepository(t)
	if err := repo.SaveConversation(context.Background(), ConversationRecord{ConversationID: "conv-1", Title: "Launch plan", Status: "completed", CreatedBy: "user-1", CreatedAt: testTime, UpdatedAt: testTime}); err != nil {
		t.Fatal(err)
	}
	seedDiscussionMembers(t, repo, "conv-1", "user-1")
	if err := repo.SaveMissionLink(context.Background(), LinkedMissionRecord{ConversationID: "conv-1", MissionID: "mission-existing", Title: "Launch plan", Status: "active", CreatedAt: testTime}); err != nil {
		t.Fatal(err)
	}
	runner := &capturingDiscussionAgentRunner{response: DiscussionAgentResponse{
		AssistantMessage: "@Staffing @Coordinator 请一起确认执行人和排期。",
		Status:           "completed",
		RuntimeKind:      "managed",
	}}
	messenger := &capturingMissionMessenger{}
	service := newTestService(ServiceOptions{
		Repository:       repo,
		DiscussionAgent:  runner,
		MissionMessenger: messenger,
		Clock:            fixedClock{now: testTime.Add(time.Minute)},
		IDGenerator:      fixedIDGen("msg-user-2", "run-2", "msg-assistant-2"),
	})

	_, err := service.SendMessage(context.Background(), ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser}, SendMessageCommand{ConversationID: "conv-1", Content: "继续推进", IdempotencyKey: "send-2"})
	if err != nil {
		t.Fatal(err)
	}

	if messenger.called {
		t.Fatalf("plain @ mention without mention:// id should not be bridged: %+v", messenger)
	}
}
