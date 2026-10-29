package agentkernel

import (
	"context"
	"errors"
	"orrery_v2/internal/agent/llm"
	"path/filepath"
	"strings"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	runtimecore "orrery_v2/internal/agent/runtime/core"
)

func automaticTaskConfig(t *testing.T, c *Config, req *agentcore.RuntimeRequest) {
	t.Helper()
	checkpointConfig(t, c)
	req.TaskID, req.MissionID = "durable-task", "durable-mission"
	req.RuntimeOptions.ProviderSpecific = map[string]string{"agentcore_task_mode": "long", "agentcore_task_recovery": "automatic"}
}

func reopenTaskRunner(t *testing.T, previous *Runner, req agentcore.RuntimeRequest, chat llm.ChatModel) (*Runner, error) {
	t.Helper()
	c := previous.config
	c.Models = kernelTestModelFactory{Model: chat}
	e, err := NewEngine(c)
	if err != nil {
		return nil, err
	}
	binding, err := c.Workspaces.Bind(context.Background(), req)
	if err != nil {
		return nil, err
	}
	defer c.Workspaces.Release(binding.Ref)
	backend, err := e.OpenSession(context.Background(), agentcore.SessionSpec{Mode: agentcore.SessionOpenFresh, SessionID: "next-delivery"}, agentcore.SessionConfig{WorkspaceRef: binding.Ref})
	if err != nil {
		return nil, err
	}
	r := backend.(*Runner)
	t.Cleanup(func() { _ = r.Close(context.Background()) })
	return r, nil
}

func TestAutomaticTaskRecoverySurvivesReopenAndReusesCompletedResult(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "automatic-task.db")
	journal, backing := openTestJournal(t, path)
	dispatches := 0
	first := &modelFunc{stream: func(_ context.Context, messages []*llm.Message, _ ...llm.Option) (*llm.MessageStream, error) {
		if messages[len(messages)-1].Role == llm.ToolRole {
			return nil, errors.New("model unavailable after tool")
		}
		return llm.NewMessageStream([]*llm.Message{reply("", "tool_calls", toolCall("once", `{"kb":"public"}`))}), nil
	}}
	r, input := openRunner(t, first, func(c *Config, req *agentcore.RuntimeRequest) {
		c.ToolJournal = journal
		automaticTaskConfig(t, c, req)
		c.Tools = runtimecore.RuntimeToolDispatcherFunc(func(context.Context, runtimecore.RuntimeToolCallRequest) (runtimecore.RuntimeToolCallResult, error) {
			dispatches++
			return runtimecore.RuntimeToolCallResult{Output: map[string]any{"evidence": "saved-public-source"}}, nil
		})
	})
	if _, err := r.Run(ctx, input, discard); !errors.Is(err, ErrModel) || !r.CanResumeTask() {
		t.Fatalf("safe failure not recoverable: %v", err)
	}
	before := *r.checkpoint
	if !strings.HasPrefix(before.ID, "agentcore-task-") || before.ModelCalls != 2 || before.ToolCalls != 1 {
		t.Fatal("task identity or budgets missing")
	}
	_ = r.Close(ctx)
	if err := backing.Close(); err != nil {
		t.Fatal(err)
	}
	newJournal, reopened := openTestJournal(t, path)
	r.config.ToolJournal = newJournal
	r.config.Checkpoints, _ = NewSQLCheckpointStore(reopened)
	answer := &modelFunc{stream: func(_ context.Context, messages []*llm.Message, _ ...llm.Option) (*llm.MessageStream, error) {
		if messages[len(messages)-1].Role != llm.ToolRole || !strings.Contains(messages[len(messages)-1].Content, "saved-public-source") {
			t.Fatal("retry restarted task")
		}
		return llm.NewMessageStream([]*llm.Message{reply("durable answer", "stop")}), nil
	}}
	second, err := reopenTaskRunner(t, r, r.request, answer)
	if err != nil {
		t.Fatal(err)
	}
	if second.BackendSessionID() != before.ID || !second.deadline.Equal(before.Deadline) || second.modelCalls != 2 {
		t.Fatal("retry replenished budgets or lost checkpoint")
	}
	if _, err := r.config.Checkpoints.Save(ctx, before); !errors.Is(err, ErrCheckpoint) {
		t.Fatal("old owner retained write access")
	}
	result, err := second.Run(ctx, input, discard)
	if err != nil || result.ResultText != "durable answer" || dispatches != 1 || second.checkpoint.ModelCalls != 3 {
		t.Fatalf("automatic resume failed: %v", err)
	}
	if result, err := second.Run(ctx, input, discard); err != nil || result.ResultText != "durable answer" || answer.calls.Load() != 1 {
		t.Fatalf("same-session redelivery repeated inference: %v", err)
	}
	never := &modelFunc{stream: func(context.Context, []*llm.Message, ...llm.Option) (*llm.MessageStream, error) {
		t.Fatal("completed task inferred again")
		return nil, nil
	}}
	third, err := reopenTaskRunner(t, second, r.request, never)
	if err != nil {
		t.Fatal(err)
	}
	usage := 0
	result, err = third.Run(ctx, input, func(e agentcore.SessionEvent) error {
		if e.Kind == agentcore.EventUsageDelta {
			usage++
		}
		return nil
	})
	if err != nil || result.ResultText != "durable answer" || never.calls.Load() != 0 || dispatches != 1 || usage != 0 || third.checkpoint.Turn != 1 || third.checkpoint.ModelCalls != 3 {
		t.Fatalf("completed task replayed execution or usage: %v", err)
	}
}

func TestAutomaticTaskRecoveryRejectsChangedAuthorityGoalAndModel(t *testing.T) {
	failed := &modelFunc{stream: func(context.Context, []*llm.Message, ...llm.Option) (*llm.MessageStream, error) {
		return nil, errors.New("offline")
	}}
	r, input := openRunner(t, identifiedMemoryModel{modelFunc: failed, identity: strings.Repeat("a", 64)}, automaticTaskConfigForTest(t))
	_, _ = r.Run(context.Background(), input, discard)
	for name, change := range map[string]func(*agentcore.RuntimeRequest){
		"principal": func(q *agentcore.RuntimeRequest) { q.ExecutionPrincipal.ID = "other" },
		"task":      func(q *agentcore.RuntimeRequest) { q.TaskID = "other" },
		"goal": func(q *agentcore.RuntimeRequest) {
			q.InputMessages = append([]agentcore.RuntimeChatMessage(nil), q.InputMessages...)
			q.InputMessages[len(q.InputMessages)-1].Content = "different goal"
		},
		"model": func(q *agentcore.RuntimeRequest) { q.RuntimeOptions.Model = "other" },
	} {
		t.Run(name, func(t *testing.T) {
			q := r.request
			change(&q)
			if _, err := reopenTaskRunner(t, r, q, identifiedMemoryModel{modelFunc: failed, identity: strings.Repeat("a", 64)}); !errors.Is(err, ErrCheckpoint) {
				t.Fatalf("changed authority reused checkpoint: %v", err)
			}
		})
	}
	if _, err := reopenTaskRunner(t, r, r.request, identifiedMemoryModel{modelFunc: failed, identity: strings.Repeat("b", 64)}); !errors.Is(err, ErrCheckpoint) {
		t.Fatalf("resolved model changed: %v", err)
	}
	if failed.calls.Load() != 1 {
		t.Fatal("rejected recovery disclosed data to model")
	}
	for _, mutate := range []func(*agentcore.RuntimeRequest){func(q *agentcore.RuntimeRequest) { q.TenantID = "other-tenant" }, func(q *agentcore.RuntimeRequest) { q.AgentID = "A000099" }, func(q *agentcore.RuntimeRequest) { q.RunID = "new-run" }} {
		q := r.request
		mutate(&q)
		next, err := reopenTaskRunner(t, r, q, identifiedMemoryModel{modelFunc: failed, identity: strings.Repeat("a", 64)})
		if err != nil || next.BackendSessionID() == r.BackendSessionID() || next.checkpoint.ModelCalls != 0 {
			t.Fatalf("independent run reused another checkpoint: %v", err)
		}
	}
}

func automaticTaskConfigForTest(t *testing.T) func(*Config, *agentcore.RuntimeRequest) {
	return func(c *Config, q *agentcore.RuntimeRequest) { automaticTaskConfig(t, c, q) }
}

func TestAutomaticTaskRecoveryDoesNotRetryUnsafeOrExhaustedRuns(t *testing.T) {
	for _, mode := range []string{"tool", "protocol", "budget", "cancel", "model-timeout"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			m := &modelFunc{stream: func(ctx context.Context, _ []*llm.Message, _ ...llm.Option) (*llm.MessageStream, error) {
				if mode == "cancel" {
					cancel()
					return nil, context.Canceled
				}
				if mode == "model-timeout" {
					<-ctx.Done()
					return nil, ctx.Err()
				}
				if mode == "tool" {
					return llm.NewMessageStream([]*llm.Message{reply("", "tool_calls", toolCall("uncertain", `{"kb":"public"}`))}), nil
				}
				if mode == "protocol" {
					return llm.NewMessageStream([]*llm.Message{reply("bad", "length")}), nil
				}
				return nil, errors.New("offline")
			}}
			r, input := openRunner(t, m, func(c *Config, q *agentcore.RuntimeRequest) {
				automaticTaskConfig(t, c, q)
				if mode == "budget" {
					q.RuntimeOptions.MaxTurns = 1
				}
				if mode == "model-timeout" {
					c.ModelTimeout = 100 * time.Millisecond
				}
				c.Tools = runtimecore.RuntimeToolDispatcherFunc(func(context.Context, runtimecore.RuntimeToolCallRequest) (runtimecore.RuntimeToolCallResult, error) {
					return runtimecore.RuntimeToolCallResult{}, errors.New("uncertain external result")
				})
			})
			if _, err := r.Run(ctx, input, discard); err == nil {
				t.Fatal("failure not exercised")
			}
			if r.CanResumeTask() != (mode == "model-timeout") {
				t.Fatalf("incorrect automatic recovery eligibility: %v", r.CanResumeTask())
			}
			if mode == "tool" {
				if _, err := reopenTaskRunner(t, r, r.request, m); !errors.Is(err, ErrUnsafeResume) {
					t.Fatalf("uncertain tool resumed: %v", err)
				}
			}
		})
	}
}
