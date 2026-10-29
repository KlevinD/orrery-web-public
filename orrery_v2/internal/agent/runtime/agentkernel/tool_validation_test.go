package agentkernel

import (
	"context"
	"fmt"
	"orrery_v2/internal/agent/llm"
	"strings"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	runtimecore "orrery_v2/internal/agent/runtime/core"
	toolstd "orrery_v2/internal/resource/tool/std"
)

func TestEmptyShellArgumentsAreCorrectedBeforeDispatchAndPrematureStop(t *testing.T) {
	for _, command := range []string{"", "  \\t\\n"} {
		t.Run(command, func(t *testing.T) {
			steps, executed := 0, 0
			m := &modelFunc{stream: func(_ context.Context, messages []*llm.Message, _ ...llm.Option) (*llm.MessageStream, error) {
				steps++
				var result *llm.Message
				switch steps {
				case 1:
					result = reply("", "tool_calls", toolCall("empty-command", `{"command":"`+command+`"}`))
					result.ToolCalls[0].Function.Name = "Shell"
				case 2:
					if executed != 0 || !strings.Contains(messages[len(messages)-1].Content, "not_dispatched") {
						t.Fatal("empty command was dispatched or feedback was lost")
					}
					result = reply("Blocked because no file was produced.", "stop")
				case 3:
					if !strings.Contains(messages[len(messages)-1].Content, "Argument correction review") {
						t.Fatal("premature stop was not reviewed")
					}
					result = reply("", "tool_calls", toolCall("corrected-command", `{"command":"printf recovered"}`))
					result.ToolCalls[0].Function.Name = "Shell"
				default:
					result = reply("Agent completed the corrected request.", "stop")
				}
				return llm.NewMessageStream([]*llm.Message{result}), nil
			}}
			runner, input := openRunner(t, m, func(c *Config, req *agentcore.RuntimeRequest) {
				checkpointConfig(t, c)
				for _, definition := range toolstd.Definitions(time.Unix(0, 0)) {
					if definition.ID == toolstd.PersonalAssistantShellExec {
						req.ToolBindings = []agentcore.RuntimeToolBinding{{ToolID: definition.ID, Alias: "Shell", InputSchema: definition.InputSchema}}
					}
				}
				c.Tools = runtimecore.RuntimeToolDispatcherFunc(func(_ context.Context, req runtimecore.RuntimeToolCallRequest) (runtimecore.RuntimeToolCallResult, error) {
					if req.CallID != "corrected-command" || req.Input["command"] != "printf recovered" {
						t.Fatal("invalid shell command reached dispatcher")
					}
					executed++
					return runtimecore.RuntimeToolCallResult{Output: map[string]any{"exit_code": 0}}, nil
				})
			})
			result, err := runner.Run(context.Background(), input, discard)
			if err != nil || result.ResultText != "Agent completed the corrected request." || steps != 4 || executed != 1 || runner.checkpoint.Phase != CheckpointCompleted {
				t.Fatalf("result=%+v err=%v steps=%d executed=%d", result, err, steps, executed)
			}
		})
	}
}

func TestToolValidationStopReviewIsBounded(t *testing.T) {
	steps := 0
	m := &modelFunc{stream: func(context.Context, []*llm.Message, ...llm.Option) (*llm.MessageStream, error) {
		steps++
		if steps == 1 {
			return llm.NewMessageStream([]*llm.Message{reply("", "tool_calls", toolCall("invalid", `{}`))}), nil
		}
		return llm.NewMessageStream([]*llm.Message{reply("Agent reports a remaining blocker.", "stop")}), nil
	}}
	runner, input := openRunner(t, m, nil)
	result, err := runner.Run(context.Background(), input, discard)
	if err != nil || steps != 3 || result.ResultText != "Agent reports a remaining blocker." {
		t.Fatalf("bounded review result=%+v err=%v steps=%d", result, err, steps)
	}
}

func TestToolValidationReviewTracksOnlyUncorrectedPreDispatchErrors(t *testing.T) {
	invalid := []*llm.Message{
		reply("", "tool_calls", toolCall("invalid", `{}`)),
		llm.ToolMessage(`{"error_code":"tool_schema_invalid","execution_status":"not_dispatched"}`, "invalid"),
	}
	if !hasUncorrectedToolValidation(invalid) {
		t.Fatal("local validation rejection was lost")
	}
	unrelated := toolCall("bookkeeping", `{}`)
	unrelated.Function.Name = "plan_update"
	invalid = append(invalid, reply("", "tool_calls", unrelated), llm.ToolMessage(`{"status":"completed"}`, unrelated.ID))
	if !hasUncorrectedToolValidation(invalid) {
		t.Fatal("unrelated bookkeeping cleared required argument correction")
	}
	corrected := append(append([]*llm.Message{}, invalid...), reply("", "tool_calls", toolCall("corrected", `{"kb":"public"}`)), llm.ToolMessage(`{"status":"failed","error_code":"dependency_unavailable"}`, "corrected"))
	if hasUncorrectedToolValidation(corrected) {
		t.Fatal("admitted corrected call was mistaken for uncorrected input")
	}
	nonlocal := []*llm.Message{reply("", "tool_calls", toolCall("denied", `{}`)), llm.ToolMessage(`{"status":"failed","error_code":"permission_denied","retryable":false}`, "denied")}
	if hasUncorrectedToolValidation(nonlocal) {
		t.Fatal("permission denial must not trigger argument correction review")
	}
	newTurn := append(append([]*llm.Message{}, invalid...), llm.UserMessage(toolValidationStopReview), llm.UserMessage("A different request."))
	if hasUncorrectedToolValidation(newTurn) || len(validationTurnMessages(newTurn)) != 0 {
		t.Fatal("a later user turn inherited old correction state")
	}
}

func TestInvalidToolBatchReturnsFeedbackAndExecutesOnlyCorrectedCall(t *testing.T) {
	for _, arguments := range []string{`{"unknown":"private-secret"}`, "{", "null"} {
		t.Run(arguments, func(t *testing.T) {
			steps, executed := 0, 0
			m := &modelFunc{stream: func(_ context.Context, messages []*llm.Message, _ ...llm.Option) (*llm.MessageStream, error) {
				steps++
				switch steps {
				case 1:
					return llm.NewMessageStream([]*llm.Message{reply("", "tool_calls", toolCall("valid-cancelled", `{"kb":"public"}`), toolCall("invalid", arguments))}), nil
				case 2:
					if executed != 0 || messages[len(messages)-2].ToolCallID != "valid-cancelled" || messages[len(messages)-1].ToolCallID != "invalid" || !strings.Contains(messages[len(messages)-1].Content, "not_dispatched") || strings.Contains(messages[len(messages)-1].Content, "private-secret") {
						t.Fatal("batch was executed or repair feedback lost pairing/privacy")
					}
					return llm.NewMessageStream([]*llm.Message{reply("", "tool_calls", toolCall("corrected", `{"kb":"public"}`))}), nil
				default:
					return llm.NewMessageStream([]*llm.Message{reply("Agent completed the corrected request.", "stop")}), nil
				}
			}}
			runner, input := openRunner(t, m, func(c *Config, _ *agentcore.RuntimeRequest) {
				checkpointConfig(t, c)
				c.Tools = runtimecore.RuntimeToolDispatcherFunc(func(_ context.Context, req runtimecore.RuntimeToolCallRequest) (runtimecore.RuntimeToolCallResult, error) {
					if req.CallID != "corrected" {
						t.Fatal("rejected batch reached dispatcher")
					}
					executed++
					return runtimecore.RuntimeToolCallResult{Output: map[string]any{"answer": "public"}}, nil
				})
			})
			journal := runner.config.ToolJournal.(*SQLToolJournal)
			result, err := runner.Run(context.Background(), input, func(e agentcore.SessionEvent) error {
				if e.ToolCall != nil && len(e.ToolCall.Input) > 0 && strings.Contains(string(e.ToolCall.Input), "private-secret") {
					t.Fatal("invalid arguments exposed")
				}
				return e.Validate()
			})
			if err != nil || executed != 1 || result.ResultText != "Agent completed the corrected request." || runner.modelCalls != 3 || runner.toolCalls != 1 {
				t.Fatalf("result=%+v err=%v executed=%d model=%d tools=%d", result, err, executed, runner.modelCalls, runner.toolCalls)
			}
			for _, id := range []string{"valid-cancelled", "invalid"} {
				_, found, err := journal.Get(context.Background(), ToolExecutionKey{TenantID: runner.request.TenantID, RunID: runner.request.RunID, CallID: id})
				if err != nil || found {
					t.Fatal("rejected input claimed an execution")
				}
			}
			if runner.checkpoint.Phase != CheckpointCompleted {
				t.Fatal("repair did not reach a durable completed boundary")
			}
		})
	}
}

func TestToolValidationRepairsAreBoundedAndCannotHideDeniedBinding(t *testing.T) {
	for _, mode := range []string{"bounded", "unbound", "duplicate"} {
		t.Run(mode, func(t *testing.T) {
			steps, executed := 0, 0
			m := &modelFunc{stream: func(context.Context, []*llm.Message, ...llm.Option) (*llm.MessageStream, error) {
				steps++
				calls := []llm.ToolCall{toolCall(fmt.Sprintf("bad-%d", steps), `{}`)}
				if mode != "bounded" {
					other := toolCall("other", `{"kb":"public"}`)
					if mode == "unbound" {
						other.Function.Name = "internal_admin"
					} else {
						other.ID = calls[0].ID
					}
					calls = append(calls, other)
				}
				return llm.NewMessageStream([]*llm.Message{reply("", "tool_calls", calls...)}), nil
			}}
			runner, input := openRunner(t, m, func(c *Config, _ *agentcore.RuntimeRequest) {
				c.Tools = runtimecore.RuntimeToolDispatcherFunc(func(context.Context, runtimecore.RuntimeToolCallRequest) (runtimecore.RuntimeToolCallResult, error) {
					executed++
					return runtimecore.RuntimeToolCallResult{}, nil
				})
			})
			_, err := runner.Run(context.Background(), input, discard)
			expected := 1
			if mode == "bounded" {
				expected = 4
			}
			if err == nil || steps != expected || executed != 0 {
				t.Fatalf("err=%v model=%d executed=%d", err, steps, executed)
			}
		})
	}
}
