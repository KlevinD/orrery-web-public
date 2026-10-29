package agentkernel

import (
	"context"
	"errors"
	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/agent/llm"
	runtimecore "orrery_v2/internal/agent/runtime/core"
	"orrery_v2/internal/agent/runtime/providers/kernelcompat"
	"orrery_v2/internal/agent/runtime/providers/profilemodel"
	"orrery_v2/internal/agent/runtime/session"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func waitCallbackSignal(t *testing.T, signal <-chan struct{}) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(3 * time.Second):
		t.Fatal("callback synchronization timed out")
	}
}

func waitCallbackError(t *testing.T, result <-chan error) error {
	t.Helper()
	select {
	case err := <-result:
		return err
	case <-time.After(3 * time.Second):
		t.Fatal("kernel remained blocked on a cancelled callback")
		return nil
	}
}

func TestLoopBoundsUncooperativeToolAndDiscardsItsLateResult(t *testing.T) {
	for _, mode := range []string{"timeout", "close", "late-panic"} {
		t.Run(mode, func(t *testing.T) {
			started, release, exited := make(chan struct{}), make(chan struct{}), make(chan struct{})
			var releaseOnce sync.Once
			releaseTool := func() { releaseOnce.Do(func() { close(release) }) }
			t.Cleanup(releaseTool)
			journal := newTestJournal(t)
			m := toolThenAnswerModel()
			var calls, results atomic.Int32
			runner, input := openRunner(t, m, func(c *Config, _ *agentcore.RuntimeRequest) {
				c.ToolJournal, c.ToolTimeout = journal, 50*time.Millisecond
				if mode == "close" {
					c.ToolTimeout = 10 * time.Second
				}
				c.Tools = runtimecore.RuntimeToolDispatcherFunc(func(context.Context, runtimecore.RuntimeToolCallRequest) (runtimecore.RuntimeToolCallResult, error) {
					calls.Add(1)
					defer close(exited)
					close(started)
					<-release
					if mode == "late-panic" {
						panic("private late callback panic")
					}
					return runtimecore.RuntimeToolCallResult{Output: map[string]any{"answer": "late success"}}, nil
				})
			})
			done := make(chan error, 1)
			go func() {
				_, err := runner.Run(context.Background(), input, func(event agentcore.SessionEvent) error {
					if event.Kind == agentcore.EventToolResult {
						results.Add(1)
					}
					return nil
				})
				done <- err
			}()
			waitCallbackSignal(t, started)
			if mode == "close" {
				_ = runner.Close(context.Background())
			}
			wantErr, wantReason := context.DeadlineExceeded, "timeout"
			if mode == "close" {
				wantErr, wantReason = context.Canceled, "cancelled"
			}
			if err := waitCallbackError(t, done); !errors.Is(err, wantErr) || calls.Load() != 1 || m.calls.Load() != 1 {
				t.Fatalf("uncooperative tool blocked or advanced run: %v, tools=%d, model=%d", err, calls.Load(), m.calls.Load())
			}
			before, found, err := journal.Get(context.Background(), journalKeyForLoop())
			if err != nil || !found || before.State != ToolExecutionUnknown || before.Reason != wantReason {
				t.Fatalf("uncertainty not persisted before callback exit: %+v, %v", before, err)
			}
			if _, err := runner.Run(context.Background(), input, discard); !errors.Is(err, ErrClosed) {
				t.Fatalf("failed runner accepted another turn: %v", err)
			}
			restartedModel := &modelFunc{stream: func(context.Context, []*llm.Message, ...llm.Option) (*llm.MessageStream, error) {
				return llm.NewMessageStream([]*llm.Message{reply("", "tool_calls", toolCall("new-call-id", `{"kb":"public"}`))}), nil
			}}
			restarted, restartedInput := openRunner(t, restartedModel, func(c *Config, _ *agentcore.RuntimeRequest) { c.ToolJournal = journal })
			if _, err := restarted.Run(context.Background(), restartedInput, discard); !errors.Is(err, ErrRunUnresolved) {
				t.Fatalf("changed call ID bypassed unresolved run: %v", err)
			}
			releaseTool()
			waitCallbackSignal(t, exited)
			after, found, err := journal.Get(context.Background(), journalKeyForLoop())
			if err != nil || !found || before != after || results.Load() != 1 {
				t.Fatalf("late callback changed durable state or emitted another result: %+v, %v, events=%d", after, err, results.Load())
			}
		})
	}
}

func TestLoopBoundsUncooperativePolicyWithoutClaimingOrDispatching(t *testing.T) {
	started, release, exited := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	releasePolicy := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(releasePolicy)
	journal := newTestJournal(t)
	var calls atomic.Int32
	runner, input := openRunner(t, toolThenAnswerModel(), func(c *Config, _ *agentcore.RuntimeRequest) {
		c.ToolJournal, c.ToolTimeout = journal, 50*time.Millisecond
		c.AuthorizeTool = func(context.Context, runtimecore.RuntimeToolCallRequest) error {
			defer close(exited)
			close(started)
			<-release
			return nil
		}
		c.Tools = runtimecore.RuntimeToolDispatcherFunc(func(context.Context, runtimecore.RuntimeToolCallRequest) (runtimecore.RuntimeToolCallResult, error) {
			calls.Add(1)
			return runtimecore.RuntimeToolCallResult{}, nil
		})
	})
	done := make(chan error, 1)
	go func() { _, err := runner.Run(context.Background(), input, discard); done <- err }()
	waitCallbackSignal(t, started)
	if err := waitCallbackError(t, done); !errors.Is(err, context.DeadlineExceeded) || calls.Load() != 0 {
		t.Fatalf("late policy approval authorized execution: %v", err)
	}
	if _, found, err := journal.Get(context.Background(), journalKeyForLoop()); err != nil || found {
		t.Fatalf("timed-out policy acquired claim: found=%v, %v", found, err)
	}
	releasePolicy()
	waitCallbackSignal(t, exited)
	if _, found, err := journal.Get(context.Background(), journalKeyForLoop()); err != nil || found || calls.Load() != 0 {
		t.Fatalf("late policy approval started execution: found=%v, calls=%d, %v", found, calls.Load(), err)
	}
}

func TestLoopClosesStreamReturnedAfterModelDeadline(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	releaseModel := func() { releaseOnce.Do(func() { close(release) }) }
	reader, writer := llm.MessagePipe(0)

	sent, sendDone := make(chan bool, 1), make(chan struct{})
	go func() { defer close(sendDone); sent <- writer.Send(reply("late answer", "stop"), nil) }()
	t.Cleanup(func() {
		releaseModel()
		reader.Close()
		waitCallbackSignal(t, sendDone)
		writer.Close()
	})
	m := &modelFunc{stream: func(context.Context, []*llm.Message, ...llm.Option) (*llm.MessageStream, error) {
		close(started)
		<-release
		return reader, nil
	}}
	runner, input := openRunner(t, m, func(c *Config, _ *agentcore.RuntimeRequest) { c.ModelTimeout = 50 * time.Millisecond })
	done := make(chan error, 1)
	go func() { _, err := runner.Run(context.Background(), input, discard); done <- err }()
	waitCallbackSignal(t, started)
	if err := waitCallbackError(t, done); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("late stream became success: %v", err)
	}
	releaseModel()
	waitCallbackSignal(t, sendDone)
	if !<-sent {
		t.Fatal("late stream was consumed instead of closed")
	}
}

func TestEngineBoundsModelFactoryAndRetainsCapacityUntilCallbackExits(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	defer close(release)
	var calls atomic.Int32
	workspaces := session.NewWorkspaceBroker()
	engine, err := NewEngine(Config{Workspaces: workspaces, ModelTimeout: 50 * time.Millisecond, MaxConcurrentCallbacks: 1,
		Models: factoryFunc(func(context.Context, profilemodel.ModelRequest) (llm.ChatModel, error) {
			calls.Add(1)
			close(started)
			<-release
			return nil, nil
		})})
	if err != nil {
		t.Fatal(err)
	}
	req := requestForTest(t)
	req.ToolBindings = nil
	binding, err := workspaces.Bind(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	defer workspaces.Release(binding.Ref)
	config := agentcore.SessionConfig{WorkspaceRef: binding.Ref}
	done := make(chan error, 1)
	go func() {
		_, err := engine.OpenSession(context.Background(), agentcore.SessionSpec{Mode: agentcore.SessionOpenFresh, SessionID: "first"}, config)
		done <- err
	}()
	waitCallbackSignal(t, started)
	if err := waitCallbackError(t, done); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("factory deadline ignored: %v", err)
	}
	if _, err := engine.OpenSession(context.Background(), agentcore.SessionSpec{Mode: agentcore.SessionOpenFresh, SessionID: "second"}, config); !errors.Is(err, ErrCapacity) || calls.Load() != 1 {
		t.Fatalf("cancelled waiter freed occupied callback capacity: %v, calls=%d", err, calls.Load())
	}
}

func TestLoopRunsWithOneCallbackSlotAndRedactsCallbackPanics(t *testing.T) {
	for _, phase := range []string{"success", "model", "policy"} {
		t.Run(phase, func(t *testing.T) {
			m := toolThenAnswerModel()
			if phase == "model" {
				m.stream = func(context.Context, []*llm.Message, ...llm.Option) (*llm.MessageStream, error) {
					panic("private model credential")
				}
			}
			runner, input := openRunner(t, m, func(c *Config, _ *agentcore.RuntimeRequest) {
				c.MaxConcurrentCallbacks = 1
				if phase == "policy" {
					c.AuthorizeTool = func(context.Context, runtimecore.RuntimeToolCallRequest) error { panic("private policy credential") }
				}
			})
			result, err := runner.Run(context.Background(), input, kernelcompat.Emit(discard))
			if phase == "success" {
				if err != nil || result.ResultText != "answer" {
					t.Fatalf("one callback slot cannot run sequential loop: %+v, %v", result, err)
				}
			} else if err == nil || strings.Contains(err.Error(), "private") {
				t.Fatalf("callback panic leaked or became success: %v", err)
			}
		})
	}
}
