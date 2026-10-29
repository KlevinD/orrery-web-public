package agentkernel

import (
	"context"
	"orrery_v2/internal/agent/llm"
	"strings"
	"testing"

	agentcore "orrery_v2/internal/agent/core"
	runtimecore "orrery_v2/internal/agent/runtime/core"
)

const blockedTestResult = "```orrery-agent-result\n{\"version\":1,\"outcome\":\"blocked\",\"summary\":\"Not yet corrected\",\"deliverables\":[],\"evidence\":[]}\n```"

func formalBlockedReviewConfig(t *testing.T, c *Config, req *agentcore.RuntimeRequest) {
	checkpointConfig(t, c)
	req.RuntimeOptions.PromptBlocks = []agentcore.RuntimePromptBlock{{BlockID: "formal_mission_task_result_contract", Role: agentcore.RuntimePromptRoleUser, Content: "Return the actual formal result after doing the task.", Enabled: true}}
}

func TestBlockedResultReviewAllowsCorrectionThroughOriginalToolPolicy(t *testing.T) {
	for _, denied := range []bool{false, true} {
		t.Run(map[bool]string{false: "corrected", true: "denied"}[denied], func(t *testing.T) {
			steps, executions := 0, 0
			chat := &modelFunc{stream: func(_ context.Context, messages []*llm.Message, _ ...llm.Option) (*llm.MessageStream, error) {
				steps++
				switch steps {
				case 1:
					return llm.NewMessageStream([]*llm.Message{reply(blockedTestResult, "stop")}), nil
				case 2:
					if messages[len(messages)-1].Content != blockedResultStopReview {
						t.Fatal("missing bounded finalization review")
					}
					return llm.NewMessageStream([]*llm.Message{reply("", "tool_calls", toolCall("correction", `{"kb":"public"}`))}), nil
				default:
					return llm.NewMessageStream([]*llm.Message{reply("Corrected and verified.", "stop")}), nil
				}
			}}
			runner, input := openRunner(t, chat, func(c *Config, req *agentcore.RuntimeRequest) {
				formalBlockedReviewConfig(t, c, req)
				c.AuthorizeTool = func(context.Context, runtimecore.RuntimeToolCallRequest) error {
					if denied {
						return ErrToolDenied
					}
					return nil
				}
				c.Tools = runtimecore.RuntimeToolDispatcherFunc(func(context.Context, runtimecore.RuntimeToolCallRequest) (runtimecore.RuntimeToolCallResult, error) {
					executions++
					return runtimecore.RuntimeToolCallResult{Output: map[string]any{"verified": true}}, nil
				})
			})
			result, err := runner.Run(context.Background(), input, discard)
			if runner.checkpoint.BlockedResultReviewRunID != runner.request.RunID {
				t.Fatal("review not durably bounded to this run")
			}
			if denied {
				if err == nil || executions != 0 || steps != 2 {
					t.Fatalf("denial bypassed: %v %d %d", err, executions, steps)
				}
			} else if err != nil || executions != 1 || steps != 3 || result.ResultText != "Corrected and verified." {
				t.Fatalf("correction failed: %+v %v %d %d", result, err, steps, executions)
			}
		})
	}
}

func TestBlockedResultReviewIsOnceAndPreservesGenuineBlockers(t *testing.T) {
	for _, mode := range []string{"once", "persisted", "chat", "completed", "budget"} {
		t.Run(mode, func(t *testing.T) {
			steps := 0
			content := blockedTestResult
			if mode == "completed" {
				content = strings.ReplaceAll(content, `"outcome":"blocked"`, `"outcome":"completed"`)
			}
			chat := &modelFunc{stream: func(context.Context, []*llm.Message, ...llm.Option) (*llm.MessageStream, error) {
				steps++
				return llm.NewMessageStream([]*llm.Message{reply(content, "stop")}), nil
			}}
			runner, input := openRunner(t, chat, func(c *Config, req *agentcore.RuntimeRequest) {
				formalBlockedReviewConfig(t, c, req)
				if mode == "chat" {
					req.RuntimeOptions.PromptBlocks = nil
				}
				if mode == "budget" {
					c.MaxModelCalls = 1
				}
			})
			if mode == "persisted" {
				runner.checkpoint.BlockedResultReviewRunID = runner.request.RunID
			}
			result, err := runner.Run(context.Background(), input, discard)
			expected := 1
			if mode == "once" {
				expected = 2
			}
			if err != nil || steps != expected || result.ResultText != content || runner.checkpoint.Phase != CheckpointCompleted {
				t.Fatalf("review bound/result changed: %+v %v %d", result, err, steps)
			}
		})
	}
}
