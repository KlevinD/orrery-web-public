package agentkernel

import (
	"context"
	"errors"
	"orrery_v2/internal/agent/llm"
	"os"
	"path/filepath"
	"strings"
	"testing"

	agentcore "orrery_v2/internal/agent/core"
	runtimecore "orrery_v2/internal/agent/runtime/core"
)

const blockedDraftResult = "```orrery-agent-result\n" + `{"version":1,"outcome":"blocked","summary":"The draft duplicates an old topic and has not been revised.","deliverables":[],"evidence":[]}` + "\n```"

func TestBlockedTaskReviewContinuesCorrectionAndVerifiesActualArtifact(t *testing.T) {
	file := filepath.Join(t.TempDir(), "draft.txt")
	if err := os.WriteFile(file, []byte("duplicate topic"), 0600); err != nil {
		t.Fatal(err)
	}
	steps, writes, checks := 0, 0, 0
	m := &modelFunc{stream: func(_ context.Context, messages []*llm.Message, _ ...llm.Option) (*llm.MessageStream, error) {
		steps++
		var answer *llm.Message
		switch steps {
		case 1:
			answer = reply("", "tool_calls", toolCall("first-check", `{"kb":"check"}`))
		case 2:
			answer = reply(blockedDraftResult, "stop")
		case 3:
			if messages[len(messages)-1].Content != blockedTaskStopReview {
				t.Fatal("blocked task was not returned to its executing model")
			}
			answer = reply("", "tool_calls", toolCall("revision", `{"kb":"revise"}`))
		case 4:
			answer = reply("", "tool_calls", toolCall("second-check", `{"kb":"check"}`))
		default:
			answer = reply("Verified the revised draft.", "stop")
		}
		return llm.NewMessageStream([]*llm.Message{answer}), nil
	}}
	r, input := openRunner(t, m, func(c *Config, req *agentcore.RuntimeRequest) {
		checkpointConfig(t, c)
		req.TaskID = "task-revise-draft"
		c.Tools = runtimecore.RuntimeToolDispatcherFunc(func(_ context.Context, call runtimecore.RuntimeToolCallRequest) (runtimecore.RuntimeToolCallResult, error) {
			if call.Input["kb"] == "revise" {
				writes++
				if err := os.WriteFile(file, []byte("distinct topic with verified cover"), 0600); err != nil {
					return runtimecore.RuntimeToolCallResult{}, err
				}
			}
			data, err := os.ReadFile(file)
			if err != nil {
				return runtimecore.RuntimeToolCallResult{}, err
			}
			if call.Input["kb"] == "check" {
				checks++
			}
			return runtimecore.RuntimeToolCallResult{Output: map[string]any{"verified": string(data) == "distinct topic with verified cover"}}, nil
		})
	})
	result, err := r.Run(context.Background(), input, discard)
	if err != nil || result.ResultText != "Verified the revised draft." || steps != 5 || writes != 1 || checks != 2 || !r.checkpoint.BlockedTaskReviewed {
		t.Fatalf("correction failed result=%+v err=%v steps=%d writes=%d checks=%d", result, err, steps, writes, checks)
	}
	policy := r.RuntimeExecutionSnapshot().Policy
	if policy.BlockedTaskReviewProtocol != blockedTaskReviewProtocol || policy.MaxBlockedTaskReviews != 1 {
		t.Fatal("resolved stopping mechanism is missing from RSI execution evidence")
	}
}

func TestBlockedTaskReviewKeepsRealBlockerAndIsBoundedPerTurn(t *testing.T) {
	steps := 0
	m := &modelFunc{stream: func(context.Context, []*llm.Message, ...llm.Option) (*llm.MessageStream, error) {
		steps++
		return llm.NewMessageStream([]*llm.Message{reply(blockedDraftResult, "stop")}), nil
	}}
	r, input := openRunner(t, m, func(c *Config, req *agentcore.RuntimeRequest) { checkpointConfig(t, c); req.TaskID = "blocked-task" })
	for turn := 1; turn <= 2; turn++ {
		result, err := r.Run(context.Background(), input, discard)
		if err != nil || result.ResultText != blockedDraftResult || steps != turn*2 || r.modelCalls != 2 {
			t.Fatalf("turn=%d result=%+v err=%v steps=%d", turn, result, err, steps)
		}
	}
}

func TestBlockedTaskReviewDoesNotConsumeUnavailableBudgetOrOrdinaryChat(t *testing.T) {
	for _, mode := range []string{"no-task", "exhausted", "plain-text", "completed", "refused", "invalid", "multiple"} {
		t.Run(mode, func(t *testing.T) {
			text, finish := blockedDraftResult, "stop"
			switch mode {
			case "plain-text":
				text = "The word blocked appears in a quoted document."
			case "completed":
				text = strings.ReplaceAll(text, `"blocked"`, `"completed"`)
			case "refused":
				finish = "content_filter"
			case "invalid":
				text = "```orrery-agent-result\nnot JSON\n```"
			case "multiple":
				text += "\n" + text
			}
			m := &modelFunc{stream: func(context.Context, []*llm.Message, ...llm.Option) (*llm.MessageStream, error) {
				return llm.NewMessageStream([]*llm.Message{reply(text, finish)}), nil
			}}
			r, input := openRunner(t, m, func(c *Config, req *agentcore.RuntimeRequest) {
				if mode != "no-task" {
					req.TaskID = "task"
				}
				if mode == "exhausted" {
					c.MaxModelCalls = 1
				}
			})
			result, err := r.Run(context.Background(), input, discard)
			if err != nil || result.ResultText != text || m.calls.Load() != 1 || r.blockedTaskReviewed {
				t.Fatalf("unnecessary review result=%+v err=%v calls=%d", result, err, m.calls.Load())
			}
		})
	}
}

func TestBlockedTaskReviewCannotResetAfterCheckpointRecovery(t *testing.T) {
	steps := 0
	m := &modelFunc{stream: func(context.Context, []*llm.Message, ...llm.Option) (*llm.MessageStream, error) {
		steps++
		if steps == 2 {
			return nil, errors.New("temporary model outage")
		}
		return llm.NewMessageStream([]*llm.Message{reply(blockedDraftResult, "stop")}), nil
	}}
	r, input := openRunner(t, m, func(c *Config, req *agentcore.RuntimeRequest) { checkpointConfig(t, c); req.TaskID = "task" })
	if _, err := r.Run(context.Background(), input, discard); !errors.Is(err, ErrModel) || !r.checkpoint.BlockedTaskReviewed || r.checkpoint.Phase != CheckpointReady {
		t.Fatalf("review not durably consumed: %v", err)
	}
	resumed, err := resumeTestRunner(t, r, r.request, m)
	if err != nil {
		t.Fatal(err)
	}
	result, err := resumed.Run(context.Background(), input, discard)
	if err != nil || result.ResultText != blockedDraftResult || steps != 3 || resumed.modelCalls != 3 {
		t.Fatalf("review replenished by recovery result=%+v err=%v steps=%d", result, err, steps)
	}
	reset := *resumed.checkpoint
	reset.BlockedTaskReviewed = false
	if _, err := resumed.config.Checkpoints.Save(context.Background(), reset); !errors.Is(err, ErrCheckpoint) {
		t.Fatal("same-turn checkpoint cleared consumed stopping review")
	}
}

func TestBlockedTaskReviewCannotBypassToolPolicy(t *testing.T) {
	steps, dispatched := 0, 0
	m := &modelFunc{stream: func(context.Context, []*llm.Message, ...llm.Option) (*llm.MessageStream, error) {
		steps++
		answer := reply(blockedDraftResult, "stop")
		if steps == 2 {
			answer = reply("", "tool_calls", toolCall("unauthorized-revision", `{"kb":"private"}`))
		}
		return llm.NewMessageStream([]*llm.Message{answer}), nil
	}}
	r, input := openRunner(t, m, func(c *Config, req *agentcore.RuntimeRequest) {
		req.TaskID = "task"
		c.AuthorizeTool = func(context.Context, runtimecore.RuntimeToolCallRequest) error { return ErrToolDenied }
		c.Tools = runtimecore.RuntimeToolDispatcherFunc(func(context.Context, runtimecore.RuntimeToolCallRequest) (runtimecore.RuntimeToolCallResult, error) {
			dispatched++
			return runtimecore.RuntimeToolCallResult{}, nil
		})
	})
	if _, err := r.Run(context.Background(), input, discard); !errors.Is(err, ErrToolDenied) || dispatched != 0 || steps != 2 {
		t.Fatalf("review bypassed policy err=%v dispatched=%d steps=%d", err, dispatched, steps)
	}
}

func TestBlockedTaskReviewPreservesPendingToolValidation(t *testing.T) {
	messages := []*llm.Message{
		llm.UserMessage("complete authorized task"),
		reply("", "tool_calls", toolCall("bad-input", `{}`)),
		llm.ToolMessage(`{"error_code":"tool_schema_invalid","execution_status":"not_dispatched"}`, "bad-input"),
		llm.UserMessage(blockedTaskStopReview),
	}
	if !hasUncorrectedToolValidation(messages) {
		t.Fatal("internal stopping review erased the real task's tool validation state")
	}
	if hasUncorrectedToolValidation(append(messages, llm.UserMessage("a new user task"))) {
		t.Fatal("later user task inherited old validation state")
	}
}

func TestBlockedTaskReviewRemainsConsumedAfterCompaction(t *testing.T) {
	compacted := false
	m := &modelFunc{stream: func(_ context.Context, messages []*llm.Message, _ ...llm.Option) (*llm.MessageStream, error) {
		if strings.HasPrefix(messages[0].Content, compactionInstruction) {
			compacted = true
			return llm.NewMessageStream([]*llm.Message{reply("Original task remains blocked; stopping review already occurred.", "stop")}), nil
		}
		return llm.NewMessageStream([]*llm.Message{reply(blockedDraftResult, "stop")}), nil
	}}
	r, input := openRunner(t, m, func(c *Config, req *agentcore.RuntimeRequest) {
		checkpointConfig(t, c)
		req.TaskID = "task"
		c.AutoCompact = true
		c.CompactKeepMessages = 1
		c.CompactAtBytes = 4000
	})
	// Reproduce a recovered task whose review history has become compactable.
	r.blockedTaskReviewed = true
	r.continuing = true
	r.messages = append(r.messages, reply(strings.Repeat("old evidence ", 400), "stop"), llm.UserMessage(blockedTaskStopReview))
	result, err := r.Run(context.Background(), input, discard)
	if err != nil || result.ResultText != blockedDraftResult || !compacted || r.modelCalls != 2 || !r.checkpoint.BlockedTaskReviewed {
		t.Fatalf("compaction replenished review result=%+v err=%v compacted=%v calls=%d", result, err, compacted, r.modelCalls)
	}
}
