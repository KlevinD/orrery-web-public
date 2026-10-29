package agentkernel

import (
	"context"
	"errors"
	"fmt"
	"orrery_v2/internal/agent/llm"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	runtimecore "orrery_v2/internal/agent/runtime/core"
)

func parallelReply(calls ...llm.ToolCall) *modelFunc {
	return &modelFunc{stream: func(_ context.Context, messages []*llm.Message, _ ...llm.Option) (*llm.MessageStream, error) {
		if messages[len(messages)-1].Role != llm.ToolRole {
			return llm.NewMessageStream([]*llm.Message{reply("", "tool_calls", calls...)}), nil
		}
		return llm.NewMessageStream([]*llm.Message{reply("done", "stop")}), nil
	}}
}

func TestParallelToolsBoundConcurrencyAndPreserveResultOrder(t *testing.T) {
	calls := make([]llm.ToolCall, 5)
	release := make(map[string]chan struct{})
	for i := range calls {
		id := fmt.Sprintf("call-%d", i)
		calls[i] = toolCall(id, `{"kb":"public"}`)
		release[id] = make(chan struct{})
	}
	started, finished := make(chan string, 5), make(chan string, 5)
	var active, maximum atomic.Int32
	m := parallelReply(calls...)
	original := m.stream
	m.stream = func(ctx context.Context, messages []*llm.Message, opts ...llm.Option) (*llm.MessageStream, error) {
		if messages[len(messages)-1].Role == llm.ToolRole {
			for i, call := range calls {
				result := messages[len(messages)-len(calls)+i]
				if result.ToolCallID != call.ID {
					t.Errorf("completion reordered the tool batch: %q != %q", result.ToolCallID, call.ID)
				}
			}
		}
		return original(ctx, messages, opts...)
	}
	var journal *SQLToolJournal
	runner, input := openRunner(t, m, func(c *Config, _ *agentcore.RuntimeRequest) {
		c.MaxParallelTools, c.ParallelToolIDs = 2, []string{"knowledge.search"}
		journal = c.ToolJournal.(*SQLToolJournal)
		checkpointConfig(t, c)
		c.Tools = runtimecore.RuntimeToolDispatcherFunc(func(ctx context.Context, call runtimecore.RuntimeToolCallRequest) (runtimecore.RuntimeToolCallResult, error) {
			n := active.Add(1)
			defer active.Add(-1)
			for old := maximum.Load(); n > old; old = maximum.Load() {
				if maximum.CompareAndSwap(old, n) {
					break
				}
			}
			started <- call.CallID
			select {
			case <-release[call.CallID]:
			case <-ctx.Done():
				return runtimecore.RuntimeToolCallResult{}, ctx.Err()
			}
			return runtimecore.RuntimeToolCallResult{Output: map[string]any{"id": call.CallID}}, nil
		})
	})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := runner.Run(ctx, input, func(event agentcore.SessionEvent) error {
			if event.ToolResult != nil {
				finished <- event.ToolResult.ToolUseID
			}
			return event.Validate()
		})
		done <- err
	}()
	for offset := 0; offset < 4; offset += 2 {
		ids := map[string]bool{}
		for i := 0; i < 2; i++ {
			select {
			case id := <-started:
				ids[id] = true
			case <-ctx.Done():
				t.Fatal("read calls did not overlap")
			}
		}
		if !ids[calls[offset].ID] || !ids[calls[offset+1].ID] {
			t.Fatalf("unexpected group: %+v", ids)
		}
		select {
		case id := <-started:
			t.Fatalf("parallel limit exceeded: %s", id)
		default:
		}
		close(release[calls[offset+1].ID])
		select {
		case id := <-finished:
			if id != calls[offset+1].ID {
				t.Fatalf("out-of-order test did not complete as arranged: %s", id)
			}
		case <-ctx.Done():
			t.Fatal("read did not finish")
		}
		close(release[calls[offset].ID])
		select {
		case <-finished:
		case <-ctx.Done():
			t.Fatal("read did not finish")
		}
	}
	select {
	case id := <-started:
		if id != calls[4].ID {
			t.Fatalf("unexpected last tool: %s", id)
		}
	case <-ctx.Done():
		t.Fatal("last call did not start")
	}
	close(release[calls[4].ID])
	if err := waitCallbackError(t, done); err != nil {
		t.Fatal(err)
	}
	if maximum.Load() != 2 || runner.toolCalls != 5 || runner.checkpoint.Phase != CheckpointCompleted || runner.checkpoint.ToolCalls != 5 {
		t.Fatalf("max=%d calls=%d checkpoint=%+v", maximum.Load(), runner.toolCalls, runner.checkpoint)
	}
	for _, call := range calls {
		record, found, err := journal.Get(ctx, ToolExecutionKey{TenantID: runner.request.TenantID, RunID: runner.request.RunID, CallID: call.ID})
		if err != nil || !found || record.State != ToolExecutionCompleted {
			t.Fatalf("read outcome missing: %+v %v", record, err)
		}
	}
}

func TestParallelToolsCancellationKeepsEveryUnknownOutcomeBlocked(t *testing.T) {
	started := make(chan struct{}, 2)
	var journal *SQLToolJournal
	runner, input := openRunner(t, parallelReply(toolCall("one", `{"kb":"public"}`), toolCall("two", `{"kb":"public"}`), toolCall("three", `{"kb":"public"}`)), func(c *Config, _ *agentcore.RuntimeRequest) {
		c.MaxParallelTools, c.ParallelToolIDs = 2, []string{"knowledge.search"}
		journal = c.ToolJournal.(*SQLToolJournal)
		checkpointConfig(t, c)
		c.Tools = runtimecore.RuntimeToolDispatcherFunc(func(ctx context.Context, _ runtimecore.RuntimeToolCallRequest) (runtimecore.RuntimeToolCallResult, error) {
			started <- struct{}{}
			<-ctx.Done()
			return runtimecore.RuntimeToolCallResult{}, ctx.Err()
		})
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := runner.Run(ctx, input, discard); done <- err }()
	for i := 0; i < 2; i++ {
		waitCallbackSignal(t, started)
	}
	cancel()
	if err := waitCallbackError(t, done); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation=%v", err)
	}
	for _, id := range []string{"one", "two"} {
		record, found, err := journal.Get(context.Background(), ToolExecutionKey{TenantID: runner.request.TenantID, RunID: runner.request.RunID, CallID: id})
		if err != nil || !found || record.State != ToolExecutionUnknown {
			t.Fatalf("cancelled outcome=%+v %v", record, err)
		}
	}
	if _, found, err := journal.Get(context.Background(), ToolExecutionKey{TenantID: runner.request.TenantID, RunID: runner.request.RunID, CallID: "three"}); found || err != nil {
		t.Fatalf("later group started: %v %v", found, err)
	}
	if runner.checkpoint.Phase != CheckpointToolsPending || runner.CanResumeTask() {
		t.Fatal("unknown parallel results became resumable")
	}
}

func TestParallelToolsPreflightAndRevocationDoNotDispatchDeniedCall(t *testing.T) {
	for _, revoke := range []bool{false, true} {
		t.Run(fmt.Sprint(revoke), func(t *testing.T) {
			var mu sync.Mutex
			checks := map[string]int{}
			var dispatchedDenied atomic.Bool
			var journal *SQLToolJournal
			runner, input := openRunner(t, parallelReply(toolCall("one", `{"kb":"public"}`), toolCall("denied", `{"kb":"public"}`)), func(c *Config, _ *agentcore.RuntimeRequest) {
				c.MaxParallelTools, c.ParallelToolIDs = 2, []string{"knowledge.search"}
				journal = c.ToolJournal.(*SQLToolJournal)
				c.AuthorizeTool = func(_ context.Context, call runtimecore.RuntimeToolCallRequest) error {
					mu.Lock()
					defer mu.Unlock()
					checks[call.CallID]++
					if call.CallID == "denied" && (!revoke || checks[call.CallID] > 1) {
						return ErrToolDenied
					}
					return nil
				}
				c.Tools = runtimecore.RuntimeToolDispatcherFunc(func(_ context.Context, call runtimecore.RuntimeToolCallRequest) (runtimecore.RuntimeToolCallResult, error) {
					if call.CallID == "denied" {
						dispatchedDenied.Store(true)
					}
					return runtimecore.RuntimeToolCallResult{Output: map[string]any{"ok": true}}, nil
				})
			})
			if _, err := runner.Run(context.Background(), input, discard); !errors.Is(err, ErrToolDenied) || dispatchedDenied.Load() {
				t.Fatalf("denied call dispatched: %v", err)
			}
			record, found, err := journal.Get(context.Background(), ToolExecutionKey{TenantID: runner.request.TenantID, RunID: runner.request.RunID, CallID: "denied"})
			if err != nil || found != revoke || (revoke && record.State != ToolExecutionNotDispatched) {
				t.Fatalf("denied claim=%+v found=%v err=%v", record, found, err)
			}
		})
	}
}

func TestParallelToolsKeepStatefulBarriersAndSerializeEvents(t *testing.T) {
	calls := []llm.ToolCall{toolCall("r1", `{"kb":"public"}`), toolCall("r2", `{"kb":"public"}`), toolCall("write", `{}`), toolCall("r3", `{"kb":"public"}`), toolCall("r4", `{"kb":"public"}`)}
	calls[2].Function.Name = "write_tool"
	var active, entered atomic.Int32
	var written atomic.Bool
	gate := []chan struct{}{make(chan struct{}), make(chan struct{})}
	runner, input := openRunner(t, parallelReply(calls...), func(c *Config, req *agentcore.RuntimeRequest) {
		c.MaxParallelTools, c.ParallelToolIDs = 2, []string{"knowledge.search"}
		req.ToolBindings = append(req.ToolBindings, agentcore.RuntimeToolBinding{ToolID: "stateful", Alias: "write_tool"})
		c.Tools = runtimecore.RuntimeToolDispatcherFunc(func(ctx context.Context, call runtimecore.RuntimeToolCallRequest) (runtimecore.RuntimeToolCallResult, error) {
			if call.CallID == "write" {
				if active.Load() != 0 || entered.Load() != 2 {
					t.Error("write crossed a preceding read")
				}
				written.Store(true)
				return runtimecore.RuntimeToolCallResult{Output: map[string]any{"ok": true}}, nil
			}
			index := 0
			if call.CallID == "r3" || call.CallID == "r4" {
				index = 1
				if !written.Load() {
					t.Error("read crossed the write barrier")
				}
			}
			n := active.Add(1)
			defer active.Add(-1)
			entered.Add(1)
			if n == 2 {
				close(gate[index])
			}
			select {
			case <-gate[index]:
			case <-ctx.Done():
				return runtimecore.RuntimeToolCallResult{}, ctx.Err()
			}
			return runtimecore.RuntimeToolCallResult{Output: map[string]any{"ok": true}}, nil
		})
	})
	var emitting atomic.Int32
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := runner.Run(ctx, input, func(event agentcore.SessionEvent) error {
		if emitting.Add(1) != 1 {
			t.Error("concurrent emit reached the host")
		}
		defer emitting.Add(-1)
		return event.Validate()
	}); err != nil || !written.Load() {
		t.Fatalf("barrier run: %v", err)
	}
}

func TestParallelToolsRecoverHostEventPanic(t *testing.T) {
	runner, input := openRunner(t, parallelReply(toolCall("one", `{"kb":"public"}`), toolCall("two", `{"kb":"public"}`)), func(c *Config, _ *agentcore.RuntimeRequest) {
		c.MaxParallelTools, c.ParallelToolIDs = 2, []string{"knowledge.search"}
	})
	if _, err := runner.Run(context.Background(), input, func(agentcore.SessionEvent) error { panic("host observer failed") }); !errors.Is(err, errCallbackPanic) {
		t.Fatalf("observer panic=%v", err)
	}
}
