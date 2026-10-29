package discussionsession

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	agentcore "orrery_v2/internal/agent/core"
)

func TestServiceSendMessageStreamPersistsDisplayTimeline(t *testing.T) {
	repo := newMemoryRepository(t)
	runner := &streamingDiscussionAgentRunner{events: []DiscussionAgentStreamEvent{
		{Kind: DiscussionAgentStreamTokenDelta, Delta: "好问题，让我看看咱们当前有谁可以拉进来！先查一下人才库 👀"},
		{Kind: DiscussionAgentStreamToolCall, ToolCall: ToolCallDTO{ToolCallID: "talent-list-1", Name: "discussion_talent_list", Status: "running"}},
		{Kind: DiscussionAgentStreamToolResult, ToolCall: ToolCallDTO{ToolCallID: "talent-list-1", Name: "discussion_talent_list", Status: "completed", Result: map[string]string{"agents": "staffing-agent"}}},
		{Kind: DiscussionAgentStreamTokenDelta, Delta: "目前咱们群里只有你我二人。"},
		{Kind: DiscussionAgentStreamRunCompleted, Response: DiscussionAgentResponse{
			AssistantMessage: "好问题，让我看看咱们当前有谁可以拉进来！先查一下人才库 👀\n目前咱们群里只有你我二人。",
			Status:           "completed",
			RuntimeKind:      "managed",
			ToolCalls: []ToolCallDTO{{
				ToolCallID: "talent-list-1",
				Name:       "discussion_talent_list",
				Status:     "completed",
				Result:     map[string]string{"agents": "staffing-agent"},
			}},
			ToolLoopCompleted: true,
		}},
	}}
	service := newTestService(ServiceOptions{
		Repository:      repo,
		DiscussionAgent: runner,
		Clock:           fixedClock{now: testTime},
		IDGenerator:     fixedIDGen("conv-1", "msg-user", "run-1", "msg-assistant"),
	})

	events, err := service.SendMessageStream(context.Background(), ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser}, SendMessageCommand{Content: "看看能拉谁进来", IdempotencyKey: "send-1"})
	if err != nil {
		t.Fatal(err)
	}
	drainLeadStream(events)

	detail, err := service.GetConversation(context.Background(), ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser}, "conv-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(detail.Messages) != 2 {
		t.Fatalf("messages = %+v", detail.Messages)
	}
	raw := detail.Messages[1].Metadata["display_timeline_json"]
	var segments []struct {
		Kind       string `json:"kind"`
		Text       string `json:"text,omitempty"`
		RunID      string `json:"run_id,omitempty"`
		ToolCallID string `json:"tool_call_id,omitempty"`
	}
	if err := json.Unmarshal([]byte(raw), &segments); err != nil {
		t.Fatalf("display timeline metadata = %q: %v", raw, err)
	}
	if len(segments) != 3 {
		t.Fatalf("display timeline segments = %+v", segments)
	}
	if segments[0].Kind != "text" || !strings.Contains(segments[0].Text, "先查一下人才库") {
		t.Fatalf("first segment = %+v", segments[0])
	}
	if segments[1].Kind != "tool" || segments[1].RunID != "run-1" || segments[1].ToolCallID != "talent-list-1" {
		t.Fatalf("tool segment = %+v", segments[1])
	}
	if segments[2].Kind != "text" || !strings.Contains(segments[2].Text, "目前咱们群里") {
		t.Fatalf("final segment = %+v", segments[2])
	}
}

func TestRuntimeDisplayTimelineMapsStructuredPlanMessages(t *testing.T) {
	segments := runtimeDisplayTimeline([]agentcore.RuntimeMessage{{
		Type: agentcore.RuntimeMessagePlan,
		Plan: &agentcore.RuntimePlanState{
			PlanID: "plan-1",
			Status: "active",
			Items: []agentcore.RuntimePlanItem{{
				ID:     "step-1",
				Text:   "Inspect runtime bridge",
				Status: "in_progress",
			}},
		},
	}}, "run-1")

	if len(segments) != 1 {
		t.Fatalf("segments = %+v", segments)
	}
	if segments[0].Kind != "plan" || segments[0].RunID != "run-1" || segments[0].Plan == nil {
		t.Fatalf("plan segment = %+v", segments[0])
	}
	if segments[0].Plan.PlanID != "plan-1" || segments[0].Plan.Items[0].ID != "step-1" {
		t.Fatalf("plan state = %+v", segments[0].Plan)
	}
}

func TestServiceSendMessageStreamPersistsStructuredPlanTimeline(t *testing.T) {
	repo := newMemoryRepository(t)
	runner := &streamingDiscussionAgentRunner{events: []DiscussionAgentStreamEvent{
		{Kind: DiscussionAgentStreamPlan, Plan: &agentcore.RuntimePlanState{
			PlanID: "plan-1",
			Status: "active",
			Items: []agentcore.RuntimePlanItem{
				{ID: "step-1", Text: "Inspect runtime bridge", Status: "completed"},
				{ID: "step-2", Text: "Persist progress", Status: "in_progress"},
			},
		}},
		{Kind: DiscussionAgentStreamRunCompleted, Response: DiscussionAgentResponse{
			AssistantMessage: "Completed 2 plan step(s).",
			Status:           "completed",
			RuntimeKind:      "pi",
			DisplayTimeline: []DiscussionDisplayTimelineSegment{{
				Kind:  "plan",
				RunID: "run-1",
				Plan: &agentcore.RuntimePlanState{
					PlanID: "plan-1",
					Status: "completed",
					Items: []agentcore.RuntimePlanItem{
						{ID: "step-1", Text: "Inspect runtime bridge", Status: "completed"},
						{ID: "step-2", Text: "Persist progress", Status: "completed"},
					},
				},
			}},
		}},
	}}
	service := newTestService(ServiceOptions{
		Repository:      repo,
		DiscussionAgent: runner,
		Clock:           fixedClock{now: testTime},
		IDGenerator:     fixedIDGen("conv-1", "msg-user", "run-1", "msg-assistant"),
	})

	events, err := service.SendMessageStream(context.Background(), ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser}, SendMessageCommand{Content: "Plan work", IdempotencyKey: "send-1"})
	if err != nil {
		t.Fatal(err)
	}
	drainLeadStream(events)

	detail, err := service.GetConversation(context.Background(), ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser}, "conv-1")
	if err != nil {
		t.Fatal(err)
	}
	raw := detail.Messages[1].Metadata["display_timeline_json"]
	if !strings.Contains(raw, `"plan_id":"plan-1"`) || strings.Contains(raw, `"PlanID"`) {
		t.Fatalf("display timeline should use stable plan json fields: %s", raw)
	}
	var segments []DiscussionDisplayTimelineSegment
	if err := json.Unmarshal([]byte(raw), &segments); err != nil {
		t.Fatalf("display timeline metadata = %q: %v", raw, err)
	}
	if len(segments) != 1 || segments[0].Kind != "plan" || segments[0].Plan == nil || segments[0].Plan.Status != "completed" {
		t.Fatalf("segments = %+v", segments)
	}
}

func TestServiceSendMessageStreamPersistsStructuredPlanTimelineFromStreamEvents(t *testing.T) {
	repo := newMemoryRepository(t)
	runner := &streamingDiscussionAgentRunner{events: []DiscussionAgentStreamEvent{
		{Kind: DiscussionAgentStreamPlan, Plan: &agentcore.RuntimePlanState{
			PlanID: "plan-1",
			Status: "active",
			Items: []agentcore.RuntimePlanItem{
				{ID: "step-1", Text: "Inspect runtime bridge", Status: "in_progress"},
			},
		}},
		{Kind: DiscussionAgentStreamRunCompleted, Response: DiscussionAgentResponse{
			AssistantMessage: "Completed 1 plan step(s).",
			Status:           "completed",
			RuntimeKind:      "pi",
		}},
	}}
	service := newTestService(ServiceOptions{
		Repository:      repo,
		DiscussionAgent: runner,
		Clock:           fixedClock{now: testTime},
		IDGenerator:     fixedIDGen("conv-1", "msg-user", "run-1", "msg-assistant"),
	})

	events, err := service.SendMessageStream(context.Background(), ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser}, SendMessageCommand{Content: "Plan work", IdempotencyKey: "send-1"})
	if err != nil {
		t.Fatal(err)
	}
	drainLeadStream(events)

	detail, err := service.GetConversation(context.Background(), ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser}, "conv-1")
	if err != nil {
		t.Fatal(err)
	}
	raw := detail.Messages[1].Metadata["display_timeline_json"]
	var segments []DiscussionDisplayTimelineSegment
	if err := json.Unmarshal([]byte(raw), &segments); err != nil {
		t.Fatalf("display timeline metadata = %q: %v", raw, err)
	}
	if len(segments) != 1 || segments[0].Kind != "plan" || segments[0].Plan == nil || segments[0].Plan.Status != "active" {
		t.Fatalf("segments = %+v", segments)
	}
}

func TestServiceSendMessageStreamEmitsStructuredPlanStreamEvent(t *testing.T) {
	repo := newMemoryRepository(t)
	runner := &streamingDiscussionAgentRunner{events: []DiscussionAgentStreamEvent{
		{Kind: DiscussionAgentStreamPlan, Plan: &agentcore.RuntimePlanState{
			PlanID: "plan-1",
			Status: "active",
			Items: []agentcore.RuntimePlanItem{
				{ID: "step-1", Text: "Inspect runtime bridge", Status: "in_progress"},
			},
		}},
		{Kind: DiscussionAgentStreamRunCompleted, Response: DiscussionAgentResponse{
			AssistantMessage: "Completed 1 plan step(s).",
			Status:           "completed",
			RuntimeKind:      "pi",
		}},
	}}
	service := newTestService(ServiceOptions{
		Repository:      repo,
		DiscussionAgent: runner,
		Clock:           fixedClock{now: testTime},
		IDGenerator:     fixedIDGen("conv-1", "msg-user", "run-1", "msg-assistant"),
	})

	events, err := service.SendMessageStream(context.Background(), ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser}, SendMessageCommand{Content: "Plan work", IdempotencyKey: "send-1"})
	if err != nil {
		t.Fatal(err)
	}
	var plan *agentcore.RuntimePlanState
	var runID string
	for event := range events {
		if event.Kind != StreamEventPlan {
			continue
		}
		plan = event.Plan
		runID = event.RunID
	}

	if plan == nil || runID != "run-1" {
		t.Fatalf("plan event plan=%+v runID=%q", plan, runID)
	}
	if plan.PlanID != "plan-1" || plan.Items[0].Status != "in_progress" {
		t.Fatalf("plan event payload = %+v", plan)
	}
}

func TestServiceSendMessageStreamDoesNotDuplicateFinalTextInDisplayTimeline(t *testing.T) {
	repo := newMemoryRepository(t)
	runner := &streamingDiscussionAgentRunner{events: []DiscussionAgentStreamEvent{
		{Kind: DiscussionAgentStreamTokenDelta, Delta: "我先查一下库存。"},
		{Kind: DiscussionAgentStreamToolCall, ToolCall: ToolCallDTO{ToolCallID: "inventory-1", Name: "inventory_lookup", Status: "running"}},
		{Kind: DiscussionAgentStreamToolResult, ToolCall: ToolCallDTO{ToolCallID: "inventory-1", Name: "inventory_lookup", Status: "completed", Result: map[string]string{"stock": "12"}}},
		{Kind: DiscussionAgentStreamTokenDelta, Delta: "库存还有 12 件，可以下单。"},
		{Kind: DiscussionAgentStreamRunCompleted, Response: DiscussionAgentResponse{
			AssistantMessage: "我先查一下库存。\n库存还有 12 件，可以下单。",
			Status:           "completed",
			RuntimeKind:      "managed",
			ToolCalls: []ToolCallDTO{{
				ToolCallID: "inventory-1",
				Name:       "inventory_lookup",
				Status:     "completed",
				Result:     map[string]string{"stock": "12"},
			}},
			DisplayTimeline: []DiscussionDisplayTimelineSegment{
				{Kind: "text", Text: "我先查一下库存。"},
				{Kind: "tool", RunID: "run-1", ToolCallID: "inventory-1"},
				{Kind: "text", Text: "库存还有 12 件，可以下单。"},
				{Kind: "text", Text: "我先查一下库存。\n库存还有 12 件，可以下单。"},
			},
			ToolLoopCompleted: true,
		}},
	}}
	service := newTestService(ServiceOptions{
		Repository:      repo,
		DiscussionAgent: runner,
		Clock:           fixedClock{now: testTime},
		IDGenerator:     fixedIDGen("conv-1", "msg-user", "run-1", "msg-assistant"),
	})

	events, err := service.SendMessageStream(context.Background(), ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser}, SendMessageCommand{Content: "查库存", IdempotencyKey: "send-1"})
	if err != nil {
		t.Fatal(err)
	}
	drainLeadStream(events)

	detail, err := service.GetConversation(context.Background(), ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser}, "conv-1")
	if err != nil {
		t.Fatal(err)
	}
	raw := detail.Messages[1].Metadata["display_timeline_json"]
	var segments []struct {
		Kind       string `json:"kind"`
		Text       string `json:"text,omitempty"`
		RunID      string `json:"run_id,omitempty"`
		ToolCallID string `json:"tool_call_id,omitempty"`
	}
	if err := json.Unmarshal([]byte(raw), &segments); err != nil {
		t.Fatalf("display timeline metadata = %q: %v", raw, err)
	}
	if len(segments) != 3 {
		t.Fatalf("display timeline segments = %+v, want text/tool/text without duplicated final text", segments)
	}
	if segments[0].Kind != "text" || !strings.Contains(segments[0].Text, "我先查一下库存。") {
		t.Fatalf("first segment = %+v", segments[0])
	}
	if segments[1].Kind != "tool" || segments[1].ToolCallID != "inventory-1" {
		t.Fatalf("tool segment = %+v", segments[1])
	}
	if segments[2].Kind != "text" || segments[2].Text != "库存还有 12 件，可以下单。" {
		t.Fatalf("final text segment = %+v", segments[2])
	}
}

func TestServiceSendMessageStreamDoesNotDuplicateCompletedProviderTimelineWhenToolRunIDIsMissing(t *testing.T) {
	repo := newMemoryRepository(t)
	runner := &streamingDiscussionAgentRunner{events: []DiscussionAgentStreamEvent{
		{Kind: DiscussionAgentStreamTokenDelta, Delta: "准备"},
		{Kind: DiscussionAgentStreamTokenDelta, Delta: "查看"},
		{Kind: DiscussionAgentStreamToolCall, ToolCall: ToolCallDTO{ToolCallID: "glob-1", Name: "Glob", Status: "running"}},
		{Kind: DiscussionAgentStreamToolResult, ToolCall: ToolCallDTO{ToolCallID: "glob-1", Name: "Glob", Status: "completed", Result: map[string]string{"output": "files"}}},
		{Kind: DiscussionAgentStreamTokenDelta, Delta: "完成"},
		{Kind: DiscussionAgentStreamRunCompleted, Response: DiscussionAgentResponse{
			AssistantMessage: "准备查看完成",
			Status:           "completed",
			RuntimeKind:      "claude",
			ToolCalls: []ToolCallDTO{{
				ToolCallID: "glob-1",
				Name:       "Glob",
				Status:     "completed",
				Result:     map[string]string{"output": "files"},
			}},
			DisplayTimeline: []DiscussionDisplayTimelineSegment{
				{Kind: "text", Text: "准备查看"},
				{Kind: "tool", ToolCallID: "glob-1"},
				{Kind: "text", Text: "完成准备查看"},
				{Kind: "tool", ToolCallID: "glob-1"},
				{Kind: "text", Text: "完成"},
			},
			ToolLoopCompleted: true,
		}},
	}}
	service := newTestService(ServiceOptions{
		Repository:      repo,
		DiscussionAgent: runner,
		Clock:           fixedClock{now: testTime},
		IDGenerator:     fixedIDGen("conv-1", "msg-user", "run-1", "msg-assistant"),
	})

	events, err := service.SendMessageStream(context.Background(), ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser}, SendMessageCommand{Content: "list files", IdempotencyKey: "send-1"})
	if err != nil {
		t.Fatal(err)
	}
	drainLeadStream(events)

	detail, err := service.GetConversation(context.Background(), ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser}, "conv-1")
	if err != nil {
		t.Fatal(err)
	}
	raw := detail.Messages[1].Metadata["display_timeline_json"]
	var segments []struct {
		Kind       string `json:"kind"`
		Text       string `json:"text,omitempty"`
		RunID      string `json:"run_id,omitempty"`
		ToolCallID string `json:"tool_call_id,omitempty"`
	}
	if err := json.Unmarshal([]byte(raw), &segments); err != nil {
		t.Fatalf("display timeline metadata = %q: %v", raw, err)
	}
	if len(segments) != 3 {
		t.Fatalf("display timeline segments = %+v, want text/tool/text", segments)
	}
	if segments[0].Kind != "text" || !strings.Contains(segments[0].Text, "准备查看") {
		t.Fatalf("first text segment = %+v", segments[0])
	}
	if segments[1].Kind != "tool" || segments[1].RunID != "run-1" || segments[1].ToolCallID != "glob-1" {
		t.Fatalf("tool segment = %+v", segments[1])
	}
	if segments[2].Kind != "text" || segments[2].Text != "完成" {
		t.Fatalf("final text segment = %+v", segments[2])
	}
}

func TestServiceSendMessageStreamDoesNotDuplicateContinuationTimelineHistory(t *testing.T) {
	repo := newMemoryRepository(t)
	runner := &streamingDiscussionAgentRunner{
		events: []DiscussionAgentStreamEvent{
			{Kind: DiscussionAgentStreamTokenDelta, Delta: "准备"},
			{Kind: DiscussionAgentStreamTokenDelta, Delta: "查看"},
			{Kind: DiscussionAgentStreamToolCall, ToolCall: ToolCallDTO{ToolCallID: "glob-1", Name: "Glob", Status: "running"}},
			{Kind: DiscussionAgentStreamToolResult, ToolCall: ToolCallDTO{ToolCallID: "glob-1", Name: "Glob", Status: "completed", Result: map[string]string{"output": "files"}}},
			{Kind: DiscussionAgentStreamTokenDelta, Delta: "完成"},
			{Kind: DiscussionAgentStreamRunCompleted, Response: DiscussionAgentResponse{
				AssistantMessage: "准备查看完成",
				Status:           "completed",
				RuntimeKind:      "claude",
				ToolCalls: []ToolCallDTO{{
					ToolCallID: "glob-1",
					Name:       "Glob",
					Status:     "completed",
					Result:     map[string]string{"output": "files"},
				}},
				DisplayTimeline: []DiscussionDisplayTimelineSegment{
					{Kind: "text", Text: "准备查看"},
					{Kind: "tool", RunID: "run-1", ToolCallID: "glob-1"},
					{Kind: "text", Text: "完成"},
				},
			}},
		},
		response: DiscussionAgentResponse{
			AssistantMessage:  "准备查看完成（本轮已实际调用只读工具。）",
			Status:            "completed",
			RuntimeKind:       "claude",
			ToolLoopCompleted: true,
			ToolCalls: []ToolCallDTO{{
				ToolCallID: "glob-1",
				Name:       "Glob",
				Status:     "completed",
				Result:     map[string]string{"output": "files"},
			}},
			DisplayTimeline: []DiscussionDisplayTimelineSegment{
				{Kind: "text", Text: "准备查看"},
				{Kind: "tool", RunID: "run-1", ToolCallID: "glob-1"},
				{Kind: "text", Text: "完成（本轮已实际调用只读工具。）"},
			},
		},
	}
	service := newTestService(ServiceOptions{
		Repository:      repo,
		DiscussionAgent: runner,
		Clock:           fixedClock{now: testTime},
		IDGenerator:     fixedIDGen("conv-1", "msg-user", "run-1", "msg-assistant"),
	})

	events, err := service.SendMessageStream(context.Background(), ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser}, SendMessageCommand{Content: "list files", IdempotencyKey: "send-1"})
	if err != nil {
		t.Fatal(err)
	}
	drainLeadStream(events)

	if runner.request.Mode != discussionToolResultContinuationMode {
		t.Fatalf("last runner request mode = %q, want tool-result continuation", runner.request.Mode)
	}
	detail, err := service.GetConversation(context.Background(), ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser}, "conv-1")
	if err != nil {
		t.Fatal(err)
	}
	raw := detail.Messages[1].Metadata["display_timeline_json"]
	var segments []struct {
		Kind       string `json:"kind"`
		Text       string `json:"text,omitempty"`
		RunID      string `json:"run_id,omitempty"`
		ToolCallID string `json:"tool_call_id,omitempty"`
	}
	if err := json.Unmarshal([]byte(raw), &segments); err != nil {
		t.Fatalf("display timeline metadata = %q: %v", raw, err)
	}
	if len(segments) != 3 {
		t.Fatalf("display timeline segments = %+v, want text/tool/text", segments)
	}
	toolCount := 0
	for _, segment := range segments {
		if segment.Kind == "tool" && segment.ToolCallID == "glob-1" {
			toolCount++
		}
	}
	if toolCount != 1 {
		t.Fatalf("tool count = %d, segments = %+v", toolCount, segments)
	}
	if segments[2].Kind != "text" || !strings.Contains(segments[2].Text, "本轮已实际调用只读工具") {
		t.Fatalf("final text segment = %+v", segments[2])
	}
}

func TestServiceSendMessageStreamKeepsStructuredToolEventsInterleavedWithText(t *testing.T) {
	repo := newMemoryRepository(t)
	runner := &streamingDiscussionAgentRunner{events: []DiscussionAgentStreamEvent{
		{Kind: DiscussionAgentStreamTokenDelta, Delta: "Now let me fetch Symphony's SPEC.md."},
		{Kind: DiscussionAgentStreamToolCall, ToolCall: ToolCallDTO{
			ToolCallID: "web-tool-1",
			Name:       "webReader",
			Status:     "running",
			Arguments: map[string]string{
				"url":           "https://raw.githubusercontent.com/openai/symphony/main/SPEC.md",
				"return_format": "markdown",
			},
		}},
		{Kind: DiscussionAgentStreamToolResult, ToolCall: ToolCallDTO{
			ToolCallID: "web-tool-1",
			Name:       "webReader",
			Status:     "completed",
			Result:     map[string]string{"output": `webReader_result_summary: [{"text":{"url":"https://raw.githubusercontent.com/openai/symphony/main/SPEC.md"}}]`},
		}},
		{Kind: DiscussionAgentStreamTokenDelta, Delta: "Now I will inspect the local architecture."},
		{Kind: DiscussionAgentStreamRunCompleted, Response: DiscussionAgentResponse{
			AssistantMessage: "Now let me fetch Symphony's SPEC.md.\nNow I will inspect the local architecture.",
			Status:           "completed",
			RuntimeKind:      "claude",
			ToolCalls: []ToolCallDTO{{
				ToolCallID: "web-tool-1",
				Name:       "webReader",
				Status:     "completed",
				Arguments: map[string]string{
					"url":           "https://raw.githubusercontent.com/openai/symphony/main/SPEC.md",
					"return_format": "markdown",
				},
				Result: map[string]string{"output": `webReader_result_summary: [{"text":{"url":"https://raw.githubusercontent.com/openai/symphony/main/SPEC.md"}}]`},
			}},
			DisplayTimeline: []DiscussionDisplayTimelineSegment{
				{Kind: "text", Text: "Now let me fetch Symphony's SPEC.md."},
				{Kind: "tool", RunID: "run-1", ToolCallID: "web-tool-1"},
				{Kind: "text", Text: "Now I will inspect the local architecture."},
			},
			ToolLoopCompleted: true,
		}},
	}}
	service := newTestService(ServiceOptions{
		Repository:      repo,
		DiscussionAgent: runner,
		Clock:           fixedClock{now: testTime},
		IDGenerator:     fixedIDGen("conv-1", "msg-user", "run-1", "msg-assistant"),
	})

	events, err := service.SendMessageStream(context.Background(), ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser}, SendMessageCommand{Content: "read spec", IdempotencyKey: "send-1"})
	if err != nil {
		t.Fatal(err)
	}
	var order []string
	for event := range events {
		switch event.Kind {
		case StreamEventTokenDelta:
			order = append(order, "text:"+event.Delta)
		case StreamEventToolCall:
			order = append(order, "tool_call:"+event.ToolCall.ToolCallID)
		case StreamEventToolResult:
			order = append(order, "tool_result:"+event.ToolCall.ToolCallID)
		}
	}
	wantOrder := []string{
		"text:Now let me fetch Symphony's SPEC.md.",
		"tool_call:web-tool-1",
		"tool_result:web-tool-1",
		"text:Now I will inspect the local architecture.",
	}
	if strings.Join(order, "\n") != strings.Join(wantOrder, "\n") {
		t.Fatalf("stream order = %#v, want %#v", order, wantOrder)
	}

	detail, err := service.GetConversation(context.Background(), ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser}, "conv-1")
	if err != nil {
		t.Fatal(err)
	}
	raw := detail.Messages[1].Metadata["display_timeline_json"]
	var segments []struct {
		Kind       string `json:"kind"`
		Text       string `json:"text,omitempty"`
		RunID      string `json:"run_id,omitempty"`
		ToolCallID string `json:"tool_call_id,omitempty"`
	}
	if err := json.Unmarshal([]byte(raw), &segments); err != nil {
		t.Fatalf("display timeline metadata = %q: %v", raw, err)
	}
	if len(segments) != 3 ||
		segments[0].Kind != "text" ||
		segments[1].Kind != "tool" ||
		segments[2].Kind != "text" {
		t.Fatalf("display timeline segments = %+v, want text/tool/text", segments)
	}
}

func TestServiceMessageCompletedRealtimeIncludesMessagePayload(t *testing.T) {
	repo := newMemoryRepository(t)
	realtime := &recordingRealtimePublisher{}
	runner := &streamingDiscussionAgentRunner{events: []DiscussionAgentStreamEvent{
		{Kind: DiscussionAgentStreamTokenDelta, Delta: "先查一下人才库"},
		{Kind: DiscussionAgentStreamToolCall, ToolCall: ToolCallDTO{ToolCallID: "talent-list-1", Name: "discussion_talent_list", Status: "running"}},
		{Kind: DiscussionAgentStreamTokenDelta, Delta: "目前有这些成员。"},
		{Kind: DiscussionAgentStreamRunCompleted, Response: DiscussionAgentResponse{
			AssistantMessage:  "先查一下人才库\n目前有这些成员。",
			Status:            "completed",
			RuntimeKind:       "managed",
			ToolCalls:         []ToolCallDTO{{ToolCallID: "talent-list-1", Name: "discussion_talent_list", Status: "completed"}},
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

	events, err := service.SendMessageStream(context.Background(), ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser}, SendMessageCommand{Content: "看看能拉谁进来", IdempotencyKey: "send-1"})
	if err != nil {
		t.Fatal(err)
	}
	drainLeadStream(events)

	messageEvents := realtime.eventsByType(RealtimeEventDiscussionMessageCompleted)
	if len(messageEvents) != 1 {
		t.Fatalf("message completed events = %+v", messageEvents)
	}
	if got := realtimePayloadString(t, messageEvents[0], "content"); !strings.Contains(got, "先查一下人才库") {
		t.Fatalf("content payload = %q", got)
	}
	if got := realtimePayloadString(t, messageEvents[0], "metadata.display_timeline_json"); !strings.Contains(got, "talent-list-1") {
		t.Fatalf("display timeline payload = %q", got)
	}
}

func TestServiceSendMessageStreamPersistsDisplayTimelineForSingleToolWithText(t *testing.T) {
	// Even a single tool call with minimal text is an established fact that must be preserved.
	repo := newMemoryRepository(t)
	runner := &streamingDiscussionAgentRunner{events: []DiscussionAgentStreamEvent{
		{Kind: DiscussionAgentStreamTokenDelta, Delta: "Let me check."},
		{Kind: DiscussionAgentStreamToolCall, ToolCall: ToolCallDTO{ToolCallID: "single-tool", Name: "list_files", Status: "running"}},
		{Kind: DiscussionAgentStreamToolResult, ToolCall: ToolCallDTO{ToolCallID: "single-tool", Name: "list_files", Status: "completed", Result: map[string]string{"files": "a.go,b.go"}}},
		{Kind: DiscussionAgentStreamRunCompleted, Response: DiscussionAgentResponse{
			AssistantMessage:  "Let me check.",
			Status:            "completed",
			RuntimeKind:       "managed",
			ToolCalls:         []ToolCallDTO{{ToolCallID: "single-tool", Name: "list_files", Status: "completed", Result: map[string]string{"files": "a.go,b.go"}}},
			ToolLoopCompleted: true,
		}},
	}}
	service := newTestService(ServiceOptions{
		Repository:      repo,
		DiscussionAgent: runner,
		Clock:           fixedClock{now: testTime},
		IDGenerator:     fixedIDGen("conv-1", "msg-user", "run-1", "msg-assistant"),
	})

	events, err := service.SendMessageStream(context.Background(), ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser}, SendMessageCommand{Content: "list files", IdempotencyKey: "send-1"})
	if err != nil {
		t.Fatal(err)
	}
	drainLeadStream(events)

	detail, err := service.GetConversation(context.Background(), ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser}, "conv-1")
	if err != nil {
		t.Fatal(err)
	}
	raw := detail.Messages[1].Metadata["display_timeline_json"]
	// A single tool with text must still be persisted as an established execution fact.
	if raw == "" {
		t.Fatal("display_timeline_json must not be empty for a reply with a single tool and text")
	}
	var segments []struct {
		Kind       string `json:"kind"`
		Text       string `json:"text,omitempty"`
		RunID      string `json:"run_id,omitempty"`
		ToolCallID string `json:"tool_call_id,omitempty"`
	}
	if err := json.Unmarshal([]byte(raw), &segments); err != nil {
		t.Fatalf("display timeline metadata = %q: %v", raw, err)
	}
	if len(segments) != 2 {
		t.Fatalf("expected 2 segments (text, tool), got %+v", segments)
	}
	if segments[1].Kind != "tool" || segments[1].ToolCallID != "single-tool" {
		t.Fatalf("tool segment = %+v", segments[1])
	}
}

func TestServiceSendMessageStreamPersistsDisplayTimelineWhenContentMismatchesSegments(t *testing.T) {
	// Content may diverge from segments due to streaming dedup or preprocessing.
	// Segments are the authoritative source and must be persisted even when content doesn't match.
	repo := newMemoryRepository(t)
	runner := &streamingDiscussionAgentRunner{events: []DiscussionAgentStreamEvent{
		{Kind: DiscussionAgentStreamTokenDelta, Delta: "好问题"},
		{Kind: DiscussionAgentStreamToolCall, ToolCall: ToolCallDTO{ToolCallID: "tool-1", Name: "search", Status: "running"}},
		{Kind: DiscussionAgentStreamToolResult, ToolCall: ToolCallDTO{ToolCallID: "tool-1", Name: "search", Status: "completed", Result: map[string]string{"hits": "3"}}},
		{Kind: DiscussionAgentStreamTokenDelta, Delta: "找到了三个结果。"},
		{Kind: DiscussionAgentStreamRunCompleted, Response: DiscussionAgentResponse{
			// Content differs from stream deltas (simulates dedup / preprocessing discrepancy)
			AssistantMessage:  "已为您搜索，找到了三个结果。",
			Status:            "completed",
			RuntimeKind:       "managed",
			ToolCalls:         []ToolCallDTO{{ToolCallID: "tool-1", Name: "search", Status: "completed", Result: map[string]string{"hits": "3"}}},
			ToolLoopCompleted: true,
		}},
	}}
	service := newTestService(ServiceOptions{
		Repository:      repo,
		DiscussionAgent: runner,
		Clock:           fixedClock{now: testTime},
		IDGenerator:     fixedIDGen("conv-1", "msg-user", "run-1", "msg-assistant"),
	})

	events, err := service.SendMessageStream(context.Background(), ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser}, SendMessageCommand{Content: "search", IdempotencyKey: "send-1"})
	if err != nil {
		t.Fatal(err)
	}
	drainLeadStream(events)

	detail, err := service.GetConversation(context.Background(), ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser}, "conv-1")
	if err != nil {
		t.Fatal(err)
	}
	raw := detail.Messages[1].Metadata["display_timeline_json"]
	if raw == "" {
		t.Fatal("display_timeline_json must not be empty when segments contain tools")
	}
	var segments []struct {
		Kind       string `json:"kind"`
		Text       string `json:"text,omitempty"`
		RunID      string `json:"run_id,omitempty"`
		ToolCallID string `json:"tool_call_id,omitempty"`
	}
	if err := json.Unmarshal([]byte(raw), &segments); err != nil {
		t.Fatalf("display timeline metadata = %q: %v", raw, err)
	}
	if len(segments) != 3 {
		t.Fatalf("expected 3 segments (text, tool, text), got %+v", segments)
	}
	hasTool := false
	for _, seg := range segments {
		if seg.Kind == "tool" && seg.ToolCallID == "tool-1" {
			hasTool = true
		}
	}
	if !hasTool {
		t.Fatalf("tool segment missing, segments = %+v", segments)
	}
}
