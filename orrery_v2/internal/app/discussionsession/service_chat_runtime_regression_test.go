package discussionsession

import (
	"context"
	"errors"
	"strings"
	"testing"

	agentcore "orrery_v2/internal/agent/core"
)

func TestServiceSendMessageStreamReturnsCreatedConversationWhenRuntimeStartFails(t *testing.T) {
	repo := newMemoryRepository(t)
	runner := &streamingDiscussionAgentRunner{err: errors.New("discussion runtime unavailable: [NodeRunError] tool run_terminal not found in toolsNode")}
	service := newTestService(ServiceOptions{
		Repository:      repo,
		DiscussionAgent: runner,
		Clock:           fixedClock{now: testTime},
		IDGenerator:     fixedIDGen("conv-1", "msg-user", "run-1"),
	})

	events, err := service.SendMessageStream(context.Background(), ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser}, SendMessageCommand{Content: "Plan a launch", IdempotencyKey: "send-1"})
	if err != nil {
		t.Fatalf("SendMessageStream returned transport error: %v", err)
	}
	var gotKinds []string
	var gotConversationID string
	var gotError string
	for event := range events {
		gotKinds = append(gotKinds, event.Kind)
		if event.Kind == StreamEventSnapshot && event.Conversation != nil {
			gotConversationID = event.Conversation.ConversationID
		}
		if event.Kind == StreamEventError {
			gotError = event.Error
		}
	}
	wantKinds := []string{StreamEventSnapshot, StreamEventTurnStarted, StreamEventRunStarted, StreamEventRunFailed, StreamEventTurnFailed, StreamEventSnapshot, StreamEventError}
	if strings.Join(gotKinds, ",") != strings.Join(wantKinds, ",") {
		t.Fatalf("stream kinds = %v, want %v", gotKinds, wantKinds)
	}
	if gotConversationID != "conv-1" {
		t.Fatalf("snapshot conversation id = %q, want conv-1", gotConversationID)
	}
	if !strings.Contains(gotError, "run_terminal") {
		t.Fatalf("stream error = %q, want runtime diagnostic", gotError)
	}
	detail, getErr := service.GetConversation(context.Background(), ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser}, "conv-1")
	if getErr != nil {
		t.Fatal(getErr)
	}
	if detail.Status != "failed" || len(detail.Runs) != 1 || detail.Runs[0].Status != "failed" || len(detail.Messages) != 1 {
		t.Fatalf("failed conversation detail = %+v", detail)
	}
	if len(detail.Events) != 1 || detail.Events[0].Kind != "discussion_runtime_failed" || !strings.Contains(detail.Events[0].Summary, "run_terminal") {
		t.Fatalf("failed conversation events = %+v, want persisted runtime failure detail", detail.Events)
	}
}

func TestServiceSendMessageStreamPersistsRuntimeExecutionFailureDetail(t *testing.T) {
	repo := newMemoryRepository(t)
	runner := &streamingDiscussionAgentRunner{events: []DiscussionAgentStreamEvent{{
		Kind:  DiscussionAgentStreamRuntimeError,
		Error: NewRuntimeExecutionError("pi", "summary cannot be generated without runtime output, deliverables, or evidence"),
		Response: DiscussionAgentResponse{
			RuntimeKind: "pi",
			SessionID:   "pi-session-stream",
			ProviderMetadata: map[string]string{
				"session_id": "pi-session-stream",
			},
		},
	}}}
	service := newTestService(ServiceOptions{
		Repository:      repo,
		DiscussionAgent: runner,
		Clock:           fixedClock{now: testTime},
		IDGenerator:     fixedIDGen("conv-1", "msg-user", "run-1"),
	})

	events, err := service.SendMessageStream(context.Background(), ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser}, SendMessageCommand{Content: "Plan a launch", IdempotencyKey: "send-1"})
	if err != nil {
		t.Fatalf("SendMessageStream returned transport error: %v", err)
	}
	var gotError string
	for event := range events {
		if event.Kind == StreamEventError {
			gotError = event.Error
		}
	}
	if !strings.Contains(gotError, "summary cannot be generated") || strings.Contains(gotError, "runtime unavailable") {
		t.Fatalf("stream error = %q, want execution diagnostic", gotError)
	}
	detail, getErr := service.GetConversation(context.Background(), ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser}, "conv-1")
	if getErr != nil {
		t.Fatal(getErr)
	}
	if len(detail.Events) != 1 || detail.Events[0].Kind != "discussion_runtime_failed" || !strings.Contains(detail.Events[0].Summary, "summary cannot be generated") || strings.Contains(detail.Events[0].Summary, "runtime unavailable") {
		t.Fatalf("failed conversation events = %+v, want persisted runtime execution detail", detail.Events)
	}
	if len(detail.Runs) != 1 || detail.Runs[0].RuntimeKind != "pi" || detail.Runs[0].SessionID != "pi-session-stream" {
		t.Fatalf("failed run = %+v, want runtime kind/session from runtime response", detail.Runs)
	}
	if detail.Events[0].Metadata["runtime_kind"] != "pi" {
		t.Fatalf("failed event metadata = %+v, want runtime_kind", detail.Events[0].Metadata)
	}
}

func TestServiceSendMessageReturnsFailedConversationWithRuntimeDebugFromFailedRunnerResponse(t *testing.T) {
	repo := newMemoryRepository(t)
	runner := &capturingDiscussionAgentRunner{
		response: DiscussionAgentResponse{
			Status:          "failed",
			RuntimeKind:     "pi",
			SessionID:       "pi-session-1",
			OutboundPrompt:  "structured prompt",
			RuntimeResponse: "status: running\n\npi completed without assistant output",
			ProviderMetadata: map[string]string{
				"session_id": "pi-session-1",
			},
		},
		err: NewRuntimeExecutionError("pi", "pi completed without assistant output"),
	}
	service := newTestService(ServiceOptions{
		Repository:      repo,
		DiscussionAgent: runner,
		Clock:           fixedClock{now: testTime},
		IDGenerator:     fixedIDGen("conv-1", "msg-user", "run-1"),
	})

	dto, err := service.SendMessage(context.Background(), ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser}, SendMessageCommand{Content: "嘿 都冒个泡", IdempotencyKey: "send-1"})
	if err == nil || !errors.Is(err, ErrRuntimeExecution) {
		t.Fatalf("SendMessage error = %v, want runtime execution failure", err)
	}
	if dto.Status != "failed" || len(dto.Runs) != 1 || dto.Runs[0].Status != "failed" {
		t.Fatalf("dto = %+v, want failed conversation with failed run", dto)
	}
	detail, getErr := service.GetConversation(context.Background(), ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser}, "conv-1")
	if getErr != nil {
		t.Fatal(getErr)
	}
	if len(detail.Messages) != 1 {
		t.Fatalf("messages = %+v, want only the user message", detail.Messages)
	}
	if len(detail.Runs) != 1 || detail.Runs[0].Status != "failed" || detail.Runs[0].RuntimeKind != "pi" || detail.Runs[0].SessionID != "pi-session-1" {
		t.Fatalf("runs = %+v, want failed run with runtime metadata", detail.Runs)
	}
	if len(detail.Events) != 1 || detail.Events[0].Metadata["runtime_kind"] != "pi" {
		t.Fatalf("events = %+v, want failed event runtime kind metadata", detail.Events)
	}
	debug, debugErr := service.GetRunDebug(context.Background(), ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser}, "conv-1", "run-1")
	if debugErr != nil {
		t.Fatal(debugErr)
	}
	if debug.OutboundPrompt != "structured prompt" || !strings.Contains(debug.RuntimeResponse, "completed without assistant output") || debug.ProviderMetadata["session_id"] != "pi-session-1" {
		t.Fatalf("debug = %+v, want failed runtime response details", debug)
	}
}

func TestDiscussionResponseTreatsRuntimeToolResultsAsCompletedToolLoop(t *testing.T) {
	response := discussionResponse(agentcore.RuntimeResponse{
		RunID:  "run-1",
		Status: agentcore.RuntimeStatusCompleted,
		Messages: []agentcore.RuntimeMessage{
			{Type: agentcore.RuntimeMessageToolUse, Tool: "mission_prepare", CallID: "tool-1", Input: map[string]any{"title": "Launch plan"}},
			{Type: agentcore.RuntimeMessageToolResult, Tool: "mission_prepare", CallID: "tool-1", Output: `{"mission_id":"mission-1"}`},
			{Type: agentcore.RuntimeMessageText, Content: "Mission prepared."},
		},
	}, agentcore.RuntimeKindManaged)

	if !response.ToolLoopCompleted {
		t.Fatalf("runtime response with matched tool result should be treated as completed tool loop: %+v", response)
	}
}

func TestDiscussionResponseUsesStructuredToolFailureStatusAndReason(t *testing.T) {
	response := discussionResponse(agentcore.RuntimeResponse{
		RunID:  "run-1",
		Status: agentcore.RuntimeStatusCompleted,
		Messages: []agentcore.RuntimeMessage{
			{Type: agentcore.RuntimeMessageToolUse, Tool: "mission_coordinator_plan_parallel", CallID: "tool-1", Input: map[string]any{"worker_lanes": []any{}}},
			{Type: agentcore.RuntimeMessageToolResult, Tool: "mission_coordinator_plan_parallel", CallID: "tool-1", Status: "completed", Output: `{"status":"failed","error":"worker_lanes[0].assignee_id is required","retryable":true}`},
		},
	}, agentcore.RuntimeKindManaged)

	if len(response.ToolCalls) != 1 || response.ToolCalls[0].Status != "failed" || !strings.Contains(response.ToolCalls[0].Result["error"], "assignee_id") {
		t.Fatalf("tool failure = %+v", response.ToolCalls)
	}
	if !response.ToolLoopCompleted {
		t.Fatalf("failed tool result must still complete the transport loop so the Agent can inspect it: %+v", response)
	}
}

func TestDiscussionResponseKeepsCompletedToolStatusWhenResultDomainIsActive(t *testing.T) {
	response := discussionResponse(agentcore.RuntimeResponse{
		RunID:  "run-1",
		Status: agentcore.RuntimeStatusCompleted,
		Messages: []agentcore.RuntimeMessage{
			{Type: agentcore.RuntimeMessageToolUse, Tool: "plan_update", CallID: "tool-1"},
			{Type: agentcore.RuntimeMessageToolResult, Tool: "plan_update", CallID: "tool-1", Status: "completed", Output: `{"status":"active","plan_id":"plan-1"}`},
		},
	}, agentcore.RuntimeKindManaged)

	if len(response.ToolCalls) != 1 || response.ToolCalls[0].Status != "completed" || response.ToolCalls[0].Result["status"] != "active" {
		t.Fatalf("tool call = %+v, want completed invocation with active plan result", response.ToolCalls)
	}
}

func TestDiscussionResponsePreservesBlockedRuntimeToolStatus(t *testing.T) {
	response := discussionResponse(agentcore.RuntimeResponse{
		RunID:  "run-1",
		Status: agentcore.RuntimeStatusBlocked,
		Messages: []agentcore.RuntimeMessage{
			{Type: agentcore.RuntimeMessageToolUse, Tool: "PowerShell", CallID: "tool-1", Status: "blocked", Input: map[string]any{"command": "python -c \"print('hi')\""}},
			{Type: agentcore.RuntimeMessageStatus, Status: "blocked", Content: "This command requires approval"},
		},
		ProviderMetadata: map[string]string{
			"approval_source":     "claude",
			"approval_request_id": "tool-1",
			"approval_kind":       "command_execution",
			"approval_tool_name":  "exec_command",
		},
	}, agentcore.RuntimeKindClaude)

	if len(response.ToolCalls) != 1 {
		t.Fatalf("tool calls = %+v", response.ToolCalls)
	}
	if response.ToolCalls[0].Status != "blocked" {
		t.Fatalf("tool status = %q, want blocked: %+v", response.ToolCalls[0].Status, response.ToolCalls[0])
	}
	if response.ToolLoopCompleted {
		t.Fatalf("blocked approval tool should not be treated as completed loop: %+v", response)
	}
}

func TestServiceSendMessageDoesNotReapplyRuntimeCompletedMissionToolResults(t *testing.T) {
	repo := newMemoryRepository(t)
	runner := &capturingDiscussionAgentRunner{response: DiscussionAgentResponse{
		AssistantMessage: "[@秦若兰](mention://agent/staffing-agent) Mission prepared; please arrange execution.",
		Status:           "completed",
		RuntimeKind:      "managed",
		ToolCalls: []ToolCallDTO{{
			ToolCallID: "tool-1",
			Name:       "mission_prepare",
			Status:     "completed",
			Arguments:  map[string]string{"title": "Launch plan"},
			Result:     map[string]string{"mission_id": "mission-1", "linked_missions_json": `[{"mission_id":"mission-1","title":"Launch plan","status":"active"}]`},
		}},
		LinkedMissions:    []LinkedMissionDTO{{MissionID: "mission-1", Title: "Launch plan", Status: "active", CreatedAt: testTime}},
		ToolLoopCompleted: true,
	}}
	applier := &capturingMissionToolApplier{}
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
	if applier.called {
		t.Fatalf("runtime-completed mission tool should not be applied again: %+v", applier.calls)
	}
	if len(dto.LinkedMissions) != 1 || dto.LinkedMissions[0].MissionID != "mission-1" {
		t.Fatalf("linked missions = %+v", dto.LinkedMissions)
	}
	if len(dto.Runs) != 1 || len(dto.Runs[0].ToolCalls) != 1 || dto.Runs[0].ToolCalls[0].Result["mission_id"] != "mission-1" {
		t.Fatalf("run tool calls = %+v", dto.Runs)
	}
}
