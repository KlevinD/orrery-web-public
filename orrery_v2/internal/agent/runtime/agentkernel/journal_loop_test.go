package agentkernel

import (
	"context"
	"errors"
	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/agent/llm"
	runtimecore "orrery_v2/internal/agent/runtime/core"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func toolThenAnswerModel() *modelFunc {
	return &modelFunc{stream: func(_ context.Context, messages []*llm.Message, _ ...llm.Option) (*llm.MessageStream, error) {
		if messages[len(messages)-1].Role == llm.ToolRole {
			return llm.NewMessageStream([]*llm.Message{reply("answer", "stop")}), nil
		}
		return llm.NewMessageStream([]*llm.Message{reply("", "tool_calls", toolCall("durable-call", `{"kb":"public"}`))}), nil
	}}
}

func journalKeyForLoop() ToolExecutionKey {
	return ToolExecutionKey{TenantID: "tenant-public", RunID: "kernel-run", CallID: "durable-call"}
}

func TestLoopJournalBlocksRepeatExecutionAcrossFreshEngines(t *testing.T) {
	for _, mode := range []string{"completed", "reported-failure", "transport-error", "capacity-error", "timeout", "invalid-result", "result-budget", "emitter-error", "panic"} {
		t.Run(mode, func(t *testing.T) {
			journal := newTestJournal(t)
			var dispatches atomic.Int32
			m := toolThenAnswerModel()
			runner, input := openRunner(t, m, func(c *Config, _ *agentcore.RuntimeRequest) {
				c.ToolJournal = journal
				if mode == "timeout" {
					c.ToolTimeout = 50 * time.Millisecond
				}
				if mode == "result-budget" {
					c.MaxResponseBytes = 1024
				}
				c.Tools = runtimecore.RuntimeToolDispatcherFunc(func(ctx context.Context, request runtimecore.RuntimeToolCallRequest) (runtimecore.RuntimeToolCallResult, error) {
					dispatches.Add(1)
					// The durable claim must commit before the external operation begins.
					record, found, err := journal.Get(context.Background(), journalKeyForLoop())
					if err != nil || !found || record.State != ToolExecutionStarted || record.ToolID != "knowledge.search" {
						t.Fatalf("tool started before durable intent: %+v, %v", record, err)
					}
					switch mode {
					case "transport-error":
						return runtimecore.RuntimeToolCallResult{}, errors.New("private upstream URL and credentials")
					case "capacity-error":
						return runtimecore.RuntimeToolCallResult{}, ErrCapacity
					case "timeout":
						<-ctx.Done()
						// A late success must remain unknown after its deadline.
						return runtimecore.RuntimeToolCallResult{Output: map[string]any{"answer": "late"}}, nil
					case "invalid-result":
						return runtimecore.RuntimeToolCallResult{Output: map[string]any{"unsupported": make(chan int)}}, nil
					case "result-budget":
						return runtimecore.RuntimeToolCallResult{Output: map[string]any{"answer": strings.Repeat("x", c.MaxResponseBytes+1)}}, nil
					case "panic":
						panic("private panic contents")
					case "reported-failure":
						return runtimecore.RuntimeToolCallResult{Output: map[string]any{"status": "failed"}}, nil
					}
					return runtimecore.RuntimeToolCallResult{Output: map[string]any{"answer": "private tool answer"}}, nil
				})
			})
			var runErr error
			panicked := false
			func() {
				defer func() { panicked = recover() != nil }()
				_, runErr = runner.Run(context.Background(), input, func(event agentcore.SessionEvent) error {
					if mode == "emitter-error" && event.Kind == agentcore.EventToolResult {
						return errors.New("consumer disconnected")
					}
					return nil
				})
			}()
			if panicked {
				t.Fatalf("unexpected panic behavior: %v", panicked)
			}
			if mode == "completed" || mode == "reported-failure" {
				if runErr != nil {
					t.Fatal(runErr)
				}
			} else if runErr == nil {
				t.Fatal("uncertain execution became success")
			}
			record, found, err := journal.Get(context.Background(), journalKeyForLoop())
			if err != nil || !found || dispatches.Load() != 1 {
				t.Fatalf("missing durable outcome: %+v, %v, dispatches=%d", record, err, dispatches.Load())
			}
			expectedState, expectedReason := ToolExecutionUnknown, ""
			switch mode {
			case "completed", "emitter-error":
				expectedState = ToolExecutionCompleted
			case "reported-failure":
				expectedState, expectedReason = ToolExecutionFailed, "reported_failure"
			case "transport-error", "capacity-error":
				expectedReason = "transport_error"
			case "timeout":
				expectedReason = "timeout"
			case "invalid-result":
				expectedReason = "invalid_result"
			case "result-budget":
				expectedReason = "result_budget"
			case "panic":
				expectedReason = "interrupted"
			}
			if record.State != expectedState || record.Reason != expectedReason {
				t.Fatalf("wrong durable outcome: %+v", record)
			}
			// A fresh Engine has no in-memory seen-call history to protect it.
			restartedModel := toolThenAnswerModel()
			restarted, restartedInput := openRunner(t, restartedModel, func(c *Config, _ *agentcore.RuntimeRequest) {
				c.ToolJournal = journal
				c.Tools = runtimecore.RuntimeToolDispatcherFunc(func(context.Context, runtimecore.RuntimeToolCallRequest) (runtimecore.RuntimeToolCallResult, error) {
					dispatches.Add(1)
					return runtimecore.RuntimeToolCallResult{}, nil
				})
			})
			if _, err := restarted.Run(context.Background(), restartedInput, discard); !errors.Is(err, ErrToolRecorded) || dispatches.Load() != 1 || restartedModel.calls.Load() != 1 {
				t.Fatalf("operation replayed after fresh Engine: %v, dispatches=%d", err, dispatches.Load())
			}
		})
	}
}

func TestLoopStopsOnJournalWriteFailureBeforeAndAfterDispatch(t *testing.T) {
	for _, phase := range []string{"begin", "finish"} {
		t.Run(phase, func(t *testing.T) {
			journal, store := openTestJournal(t, filepath.Join(t.TempDir(), "runtime.db"))
			m := toolThenAnswerModel()
			var dispatches, results int
			runner, input := openRunner(t, m, func(c *Config, _ *agentcore.RuntimeRequest) {
				c.ToolJournal = journal
				c.Tools = runtimecore.RuntimeToolDispatcherFunc(func(context.Context, runtimecore.RuntimeToolCallRequest) (runtimecore.RuntimeToolCallResult, error) {
					dispatches++
					store.InjectReplaceError(errors.New("database credential must not leak"))
					return runtimecore.RuntimeToolCallResult{Output: map[string]any{"answer": "side effect applied"}}, nil
				})
			})
			if phase == "begin" {
				store.InjectReplaceError(errors.New("database path must not leak"))
			}
			_, err := runner.Run(context.Background(), input, func(event agentcore.SessionEvent) error {
				if event.Kind == agentcore.EventToolResult {
					results++
				}
				return nil
			})
			if !errors.Is(err, ErrJournal) || strings.Contains(err.Error(), "must not leak") || results != 0 || m.calls.Load() != 1 {
				t.Fatalf("journal failure was hidden: %v, results=%d, model=%d", err, results, m.calls.Load())
			}
			expectedDispatches := 0
			if phase == "finish" {
				expectedDispatches = 1
			}
			if dispatches != expectedDispatches {
				t.Fatalf("dispatches=%d, want=%d", dispatches, expectedDispatches)
			}
			store.InjectReplaceError(nil)
			record, found, err := journal.Get(context.Background(), journalKeyForLoop())
			if err != nil || found != (phase == "finish") || found && record.State != ToolExecutionStarted {
				t.Fatalf("uncertain claim was lost or finalized: %+v, found=%v, %v", record, found, err)
			}
		})
	}
}

type cancellingJournal struct {
	ToolJournal
	cancel context.CancelFunc
}

func (j cancellingJournal) Begin(ctx context.Context, record ToolExecution) (ToolExecution, error) {
	claim, err := j.ToolJournal.Begin(ctx, record)
	if err == nil {
		j.cancel()
	}
	return claim, err
}

func TestLoopPersistsCancelledClaimWithoutDispatching(t *testing.T) {
	journal := newTestJournal(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var dispatches int
	m := toolThenAnswerModel()
	runner, input := openRunner(t, m, func(c *Config, _ *agentcore.RuntimeRequest) {
		c.ToolJournal = cancellingJournal{ToolJournal: journal, cancel: cancel}
		c.Tools = runtimecore.RuntimeToolDispatcherFunc(func(context.Context, runtimecore.RuntimeToolCallRequest) (runtimecore.RuntimeToolCallResult, error) {
			dispatches++
			return runtimecore.RuntimeToolCallResult{}, nil
		})
	})
	if _, err := runner.Run(ctx, input, discard); !errors.Is(err, context.Canceled) || dispatches != 0 {
		t.Fatalf("cancelled claim dispatched: %v, tools=%d", err, dispatches)
	}
	record, found, err := journal.Get(context.Background(), journalKeyForLoop())
	if err != nil || !found || record.State != ToolExecutionNotDispatched || record.Reason != "cancelled_before_dispatch" {
		t.Fatalf("cancellation erased execution evidence: %+v, found=%v, %v", record, found, err)
	}
}

func TestLoopRecordsCancellationDuringExternalOperation(t *testing.T) {
	journal := newTestJournal(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m := toolThenAnswerModel()
	runner, input := openRunner(t, m, func(c *Config, _ *agentcore.RuntimeRequest) {
		c.ToolJournal = journal
		c.Tools = runtimecore.RuntimeToolDispatcherFunc(func(ctx context.Context, _ runtimecore.RuntimeToolCallRequest) (runtimecore.RuntimeToolCallResult, error) {
			cancel()
			<-ctx.Done()
			return runtimecore.RuntimeToolCallResult{}, ctx.Err()
		})
	})
	if _, err := runner.Run(ctx, input, discard); !errors.Is(err, context.Canceled) || m.calls.Load() != 1 {
		t.Fatalf("cancelled operation advanced model: %v", err)
	}
	record, found, err := journal.Get(context.Background(), journalKeyForLoop())
	if err != nil || !found || record.State != ToolExecutionUnknown || record.Reason != "cancelled" {
		t.Fatalf("interrupted external operation lost: %+v, found=%v, %v", record, found, err)
	}
}

func TestLoopDoesNotClaimCallsDeniedOrRejectedByConsumer(t *testing.T) {
	for _, mode := range []string{"policy-denied", "event-rejected"} {
		t.Run(mode, func(t *testing.T) {
			journal := newTestJournal(t)
			var dispatches int
			runner, input := openRunner(t, toolThenAnswerModel(), func(c *Config, _ *agentcore.RuntimeRequest) {
				c.ToolJournal = journal
				if mode == "policy-denied" {
					c.AuthorizeTool = func(context.Context, runtimecore.RuntimeToolCallRequest) error { return ErrToolDenied }
				}
				c.Tools = runtimecore.RuntimeToolDispatcherFunc(func(context.Context, runtimecore.RuntimeToolCallRequest) (runtimecore.RuntimeToolCallResult, error) {
					dispatches++
					return runtimecore.RuntimeToolCallResult{}, nil
				})
			})
			_, err := runner.Run(context.Background(), input, func(event agentcore.SessionEvent) error {
				if mode == "event-rejected" && event.Kind == agentcore.EventToolCall {
					return errors.New("consumer unavailable")
				}
				return nil
			})
			if err == nil || dispatches != 0 {
				t.Fatalf("unaccepted operation executed: %v, tools=%d", err, dispatches)
			}
			if _, found, err := journal.Get(context.Background(), journalKeyForLoop()); err != nil || found {
				t.Fatalf("unaccepted call acquired durable claim: found=%v, %v", found, err)
			}
		})
	}
}

func TestTextOnlySessionDoesNotRequireToolInfrastructure(t *testing.T) {
	m := &modelFunc{stream: func(context.Context, []*llm.Message, ...llm.Option) (*llm.MessageStream, error) {
		return llm.NewMessageStream([]*llm.Message{reply("text answer", "stop")}), nil
	}}
	runner, input := openRunner(t, m, func(c *Config, req *agentcore.RuntimeRequest) {
		req.ToolBindings = nil
		c.Tools, c.AuthorizeTool, c.ToolJournal = nil, nil, nil
	})
	result, err := runner.Run(context.Background(), input, discard)
	if err != nil || result.ResultText != "text answer" {
		t.Fatalf("text-only session required tool services: %+v, %v", result, err)
	}
}
