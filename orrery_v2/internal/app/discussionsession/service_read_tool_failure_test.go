package discussionsession

import (
	"context"
	"strings"
	"testing"

	toolstd "orrery_v2/internal/resource/tool/std"
)

func readToolFailureNames() []string {
	return []string{
		string(toolstd.SkillList), string(toolstd.SkillLoad),
		string(toolstd.SkillResourceList), string(toolstd.SkillResourceLoad),
		string(toolstd.RSILearningPlan), string(toolstd.EnterpriseGraphSearchObjects),
		"enterprise_graph_search_objects",
	}
}

func TestServiceSendMessageStreamPreservesAgentReplyAfterReadToolFailure(t *testing.T) {
	for _, name := range readToolFailureNames() {
		t.Run(name, func(t *testing.T) {
			const answer = "[@本地用户](mention://user/user-1) 该只读工具请求失败，未取得内容；6×7 = 42。"
			tool := ToolCallDTO{
				ToolCallID: "read-1", Name: name, Status: "failed",
				Arguments: map[string]string{"skill_id": "skill.does-not-exist"},
				Result:    map[string]string{"status": "failed", "error_code": "tool_execution_failed", "error": "bound resource not found"},
			}
			runner := &streamingDiscussionAgentRunner{events: []DiscussionAgentStreamEvent{
				{Kind: DiscussionAgentStreamToolCall, ToolCall: ToolCallDTO{ToolCallID: tool.ToolCallID, Name: name, Status: "running", Arguments: tool.Arguments}},
				{Kind: DiscussionAgentStreamToolResult, ToolCall: tool},
				{Kind: DiscussionAgentStreamTokenDelta, Delta: answer},
				{Kind: DiscussionAgentStreamRunCompleted, Response: DiscussionAgentResponse{
					AssistantMessage: answer, Status: "completed", RuntimeKind: "managed",
					ToolLoopCompleted: true, ToolCalls: []ToolCallDTO{tool},
				}},
			}}
			repo := newMemoryRepository(t)
			service := newTestService(ServiceOptions{Repository: repo, DiscussionAgent: runner,
				Clock: fixedClock{now: testTime}, IDGenerator: fixedIDGen("conv-1", "msg-user", "run-1", "msg-assistant")})
			viewer := ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser}
			events, err := service.SendMessageStream(context.Background(), viewer, SendMessageCommand{Content: "读取只读资源并计算", IdempotencyKey: "read-error-1"})
			if err != nil {
				t.Fatal(err)
			}
			messages, completedRuns, failedTools := 0, 0, 0
			for event := range events {
				switch event.Kind {
				case StreamEventError:
					t.Fatalf("handled read error must not fail the turn: %s", event.Error)
				case StreamEventToolResult:
					if event.ToolCall != nil && event.ToolCall.Status == "failed" {
						failedTools++
					}
				case StreamEventMessageCompleted:
					if event.Message == nil || event.Message.Content != answer {
						t.Fatalf("Agent reply changed: %+v", event.Message)
					}
					messages++
				case StreamEventRunCompleted:
					if event.Run == nil || event.Run.Status != "completed" {
						t.Fatalf("runtime completed but application rejected its answer: %+v", event.Run)
					}
					completedRuns++
				}
			}
			if messages != 1 || completedRuns != 1 || failedTools != 1 {
				t.Fatalf("reply/run/error events = %d/%d/%d", messages, completedRuns, failedTools)
			}
			detail, err := service.GetConversation(context.Background(), viewer, "conv-1")
			if err != nil {
				t.Fatal(err)
			}
			if len(detail.Messages) != 2 || detail.Messages[1].Content != answer || len(detail.Runs) != 1 || detail.Runs[0].Status != "completed" || len(detail.Runs[0].ToolCalls) != 1 || detail.Runs[0].ToolCalls[0].Status != "failed" || detail.Runs[0].ToolCalls[0].Result["error_code"] != "tool_execution_failed" {
				t.Fatalf("reply or tool failure was lost: %+v", detail)
			}
		})
	}
}

func TestReadToolFailureDoesNotCompleteEmptyOrUnfinishedTurns(t *testing.T) {
	for _, name := range readToolFailureNames() {
		for _, status := range []string{"failed", "running", "blocked", "todo"} {
			t.Run(name+"/"+status, func(t *testing.T) {
				calls := []ToolCallDTO{{Name: name, Status: status, Result: map[string]string{"error": "resource unavailable"}}}
				if err := orchestrationStopError(nil, calls, ""); err == nil || !strings.Contains(err.Error(), name) {
					t.Fatalf("missing Agent reply must remain incomplete: %v", err)
				}
				if status != "failed" {
					if err := orchestrationStopError(nil, calls, "正在处理。"); err == nil {
						t.Fatal("nonterminal or blocked tool was treated as a handled failure")
					}
				}
			})
		}
	}
}

func TestReadFailurePolicyDoesNotAllowUnresolvedWriteOrUnknownMCP(t *testing.T) {
	for _, name := range []string{string(toolstd.KnowledgeAdd), string(toolstd.ScheduledTaskCreate), string(toolstd.ScheduledTaskUpdate), string(toolstd.ScheduledTaskCancel), string(toolstd.ScheduledTaskRunNow), string(toolstd.MissionPrepare), string(toolstd.MissionCoordinatorPlanParallel), "mcp.unknown.execute"} {
		t.Run(name, func(t *testing.T) {
			calls := []ToolCallDTO{{Name: name, Status: "failed", Result: map[string]string{"error": "operation failed"}}}
			if err := orchestrationStopError(nil, calls, "无法完成这次操作。"); err == nil {
				t.Fatal("a failed write or unknown MCP operation was treated as a read failure")
			}
		})
	}
}
