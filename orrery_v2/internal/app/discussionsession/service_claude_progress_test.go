package discussionsession

import (
	"context"
	"strings"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
)

func TestMapRunsRecoversCancelledClaudeTasksFromLegacyToolCalls(t *testing.T) {
	runs := mapRuns([]RunRecord{{
		RunID:     "run-legacy-claude",
		Status:    "cancelled",
		CreatedAt: time.Now(),
		ToolCalls: []ToolCallDTO{
			{
				ToolCallID: "create-1",
				Name:       "TaskCreate",
				Status:     "completed",
				Arguments:  map[string]string{"subject": "Build the game", "description": "Create a single-file game"},
				Result:     map[string]string{"output": "Task #1 created successfully: Build the game"},
			},
			{
				ToolCallID: "update-1",
				Name:       "TaskUpdate",
				Status:     "completed",
				Arguments:  map[string]string{"taskId": "1", "status": "in_progress"},
			},
		},
	}})
	if len(runs) != 1 || !runs[0].HasPlan || runs[0].Plan == nil {
		t.Fatalf("legacy Claude tasks were not recovered: %+v", runs)
	}
	plan := runs[0].Plan
	if plan.PlanID != "claude-tasks" || plan.Status != "cancelled" || len(plan.Items) != 1 {
		t.Fatalf("recovered plan = %+v", plan)
	}
	if plan.Items[0].ID != "1" || plan.Items[0].Text != "Build the game" || plan.Items[0].Status != "cancelled" {
		t.Fatalf("recovered task = %+v", plan.Items[0])
	}
}

func TestMapRuntimeToolCallsKeepsCompletedArgumentsAfterProgressUpdates(t *testing.T) {
	calls := mapRuntimeToolCalls([]agentcore.RuntimeMessage{
		{Type: agentcore.RuntimeMessageToolUse, Tool: "Write", CallID: "write-1"},
		{Type: agentcore.RuntimeMessageToolUse, Tool: "Write", CallID: "write-1", Input: map[string]any{
			"__orrery_streaming_input":  "true",
			"__orrery_input_characters": "4096",
			"__orrery_input_preview":    "partial html",
		}},
		{Type: agentcore.RuntimeMessageToolUse, Tool: "Write", CallID: "write-1", Input: map[string]any{
			"file_path": "game.html",
			"content":   "<html>complete</html>",
		}},
	})
	if len(calls) != 1 {
		t.Fatalf("tool calls = %+v", calls)
	}
	if calls[0].Arguments["file_path"] != "game.html" || calls[0].Arguments["content"] != "<html>complete</html>" {
		t.Fatalf("final arguments were not retained: %+v", calls[0].Arguments)
	}
	if _, ok := calls[0].Arguments["__orrery_streaming_input"]; ok {
		t.Fatalf("progress metadata leaked into completed arguments: %+v", calls[0].Arguments)
	}
}

func TestRealtimeToolUpdatesUseDistinctEventIDs(t *testing.T) {
	realtime := &recordingRealtimePublisher{}
	service := &Service{realtime: realtime}
	calls := []ToolCallDTO{
		{ToolCallID: "write-1", Name: "Write", Status: "running", CreatedAt: testTime},
		{ToolCallID: "write-1", Name: "Write", Status: "running", Arguments: map[string]string{
			"__orrery_streaming_input":  "true",
			"__orrery_input_characters": "4096",
		}, CreatedAt: testTime},
		{ToolCallID: "write-1", Name: "Write", Status: "running", Arguments: map[string]string{
			"file_path": "game.html",
			"content":   "<html></html>",
		}, CreatedAt: testTime},
	}
	for _, call := range calls {
		if err := service.appendRealtimeToolEvents(context.Background(), "conv-1", "run-1", []ToolCallDTO{call}); err != nil {
			t.Fatal(err)
		}
	}
	events := realtime.eventsByType(RealtimeEventDiscussionToolCall)
	if len(events) != len(calls) {
		t.Fatalf("tool events = %#v", events)
	}
	seen := map[string]bool{}
	for _, event := range events {
		if seen[event.EventID] || !strings.HasPrefix(event.EventID, "discussion-tool-call-run-1-write-1-") {
			t.Fatalf("tool event id = %q, all events = %#v", event.EventID, events)
		}
		seen[event.EventID] = true
	}
}

func TestMergedStreamedToolResultKeepsStartedToolDetails(t *testing.T) {
	started := ToolCallDTO{ToolCallID: "write-1", Name: "Write", Status: "running", Arguments: map[string]string{"file_path": "game.html"}, CreatedAt: testTime}
	completed := ToolCallDTO{ToolCallID: "write-1", Status: "completed", Result: map[string]string{"output": "created"}, CreatedAt: testTime.Add(time.Second)}
	calls := upsertStreamedToolCall([]ToolCallDTO{started}, completed)
	got := mergedStreamedToolCall(calls, completed)
	if got.Name != "Write" || got.Arguments["file_path"] != "game.html" || got.Status != "completed" || got.Result["output"] != "created" || !got.CreatedAt.Equal(testTime) {
		t.Fatalf("merged tool result = %#v", got)
	}
}

func claudeTaskPlanStreamEvent() DiscussionAgentStreamEvent {
	return DiscussionAgentStreamEvent{Kind: DiscussionAgentStreamPlan, Plan: &agentcore.RuntimePlanState{
		PlanID: "claude-tasks",
		Status: "active",
		Items:  []agentcore.RuntimePlanItem{{ID: "1", Text: "Build the game", Status: "in_progress"}},
	}}
}

func assertClaudePlanStreamEvent(t *testing.T, event StreamEventDTO) {
	t.Helper()
	if event.Kind != StreamEventPlan || event.Plan == nil {
		t.Fatalf("plan = %+v", event)
	}
}

func assertCancelledClaudeRunProgress(t *testing.T, run RunDTO) {
	t.Helper()
	if run.ToolCalls[0].Status != "cancelled" {
		t.Fatalf("running tool should be cancelled on abort: %+v", run.ToolCalls[0])
	}
	if run.Plan == nil || run.Plan.Status != "cancelled" || len(run.Plan.Items) != 1 || run.Plan.Items[0].Status != "cancelled" {
		t.Fatalf("aborted direct conversation should keep cancelled Claude tasks: %+v", run.Plan)
	}
}
