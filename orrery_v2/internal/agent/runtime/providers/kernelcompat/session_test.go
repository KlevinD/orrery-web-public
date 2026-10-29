package kernelcompat

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
)

type testRunner struct {
	run    func(context.Context, agentcore.SendCommand, Emit) (agentcore.TurnResultEvent, error)
	close  func(context.Context) error
	calls  atomic.Int32
	closes atomic.Int32
}

func (r *testRunner) Run(ctx context.Context, input agentcore.SendCommand, emit Emit) (agentcore.TurnResultEvent, error) {
	r.calls.Add(1)
	return r.run(ctx, input, emit)
}

func (r *testRunner) Close(ctx context.Context) error {
	r.closes.Add(1)
	if r.close != nil {
		return r.close(ctx)
	}
	return nil
}

func completed(text string) agentcore.TurnResultEvent {
	return agentcore.TurnResultEvent{ResultText: text, Outcome: agentcore.TurnOutcome{
		Kind: agentcore.TurnOutcomeCompleted, StopReason: &agentcore.StopReason{Kind: agentcore.StopReasonEndTurn},
	}}
}

func send(id, text string) agentcore.RuntimeCommand {
	return agentcore.RuntimeCommand{CommandID: id, Kind: agentcore.RuntimeCommandSend,
		Send: &agentcore.SendCommand{Content: []agentcore.ContentBlock{{Kind: agentcore.ContentBlockText, Text: text}}, Metadata: agentcore.CommandMetadata{ClientMsgID: id}}}
}

func openTestSession(t *testing.T, runner *testRunner, mutate func(*Config)) (*Connection, *Session) {
	t.Helper()
	config := Config{Engine: EngineFunc(func(context.Context, agentcore.SessionSpec, agentcore.SessionConfig) (Runner, error) {
		return runner, nil
	})}
	if mutate != nil {
		mutate(&config)
	}
	connection, err := NewConnection(config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = connection.Close() })
	backend, err := connection.OpenSession(context.Background(), agentcore.SessionSpec{Mode: agentcore.SessionOpenFresh, SessionID: "session-test"}, agentcore.SessionConfig{WorkspaceRef: "workspace-test"})
	if err != nil {
		t.Fatal(err)
	}
	return connection, backend.(*Session)
}

func collectTurn(t *testing.T, session *Session) []agentcore.SessionEnvelope {
	t.Helper()
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	var events []agentcore.SessionEnvelope
	for {
		select {
		case event, ok := <-session.Events():
			if !ok {
				t.Fatal("event stream closed before terminal result")
			}
			if err := event.Validate(); err != nil {
				t.Fatalf("invalid envelope: %v", err)
			}
			events = append(events, event)
			if event.Event.Kind == agentcore.EventTurnResult {
				return events
			}
		case <-deadline.C:
			t.Fatal("timed out waiting for terminal result")
		}
	}
}

func terminal(events []agentcore.SessionEnvelope) *agentcore.TurnResultEvent {
	return events[len(events)-1].Event.TurnResult
}

func assertLateRunnerSettledWithoutEvents(t *testing.T, session *Session) {
	t.Helper()
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		session.mu.Lock()
		idle := session.active == nil
		session.mu.Unlock()
		if idle {
			break
		}
		select {
		case <-ticker.C:
		case <-deadline.C:
			t.Fatal("late runner did not settle")
		}
	}
	select {
	case event := <-session.Events():
		t.Fatalf("late runner rewrote the terminal event: %+v", event)
	default:
	}
}

func TestSessionStreamsOrderedEventsAndPreservesMultiTurn(t *testing.T) {
	runner := &testRunner{run: func(_ context.Context, input agentcore.SendCommand, emit Emit) (agentcore.TurnResultEvent, error) {
		for _, event := range []agentcore.SessionEvent{
			{Kind: agentcore.EventMessageDelta, MessageDelta: &agentcore.MessageDeltaEvent{ItemID: "answer", Text: "checking"}},
			{Kind: agentcore.EventToolCall, ToolCall: &agentcore.ToolCallEvent{ToolUseID: "tool-1", Name: "knowledge_search", Input: []byte(`{"query":"public"}`)}},
			{Kind: agentcore.EventToolResult, ToolResult: &agentcore.ToolResultEvent{ToolUseID: "tool-1", Content: []agentcore.ToolResultContent{{Kind: agentcore.ToolResultText, Text: "approved source"}}}},
			{Kind: agentcore.EventUsageDelta, UsageDelta: &agentcore.UsageDeltaEvent{InputTokens: 10, OutputTokens: 3, TotalTokens: 13}},
		} {
			if err := emit(event); err != nil {
				return agentcore.TurnResultEvent{}, err
			}
		}
		return completed(input.Content[0].Text), nil
	}}
	_, session := openTestSession(t, runner, nil)
	var sequence uint64
	for generation := uint64(1); generation <= 2; generation++ {
		command := send(fmt.Sprintf("send-%d", generation), "answer")
		receipt, err := session.Dispatch(context.Background(), command)
		if err != nil || !receipt.Accepted || receipt.TurnGeneration != generation {
			t.Fatalf("dispatch = %+v, %v", receipt, err)
		}
		events := collectTurn(t, session)
		if len(events) != 7 || events[len(events)-2].Event.Kind != agentcore.EventMessageFinalized || terminal(events).Outcome.Kind != agentcore.TurnOutcomeCompleted || terminal(events).ResultText != "answer" {
			t.Fatalf("events = %+v", events)
		}
		for _, event := range events {
			if event.Event.Kind == agentcore.EventTurnStarted {
				t.Fatal("provider emitted orchestrator-owned start event")
			}
			sequence++
			if event.Sequence != sequence || event.TurnGeneration != generation || event.SessionID != "session-test" {
				t.Fatalf("correlation = %+v", event)
			}
		}
	}
}

func TestSessionDuplicateCommandsDoNotExecuteTwice(t *testing.T) {
	runner := &testRunner{run: func(context.Context, agentcore.SendCommand, Emit) (agentcore.TurnResultEvent, error) {
		return completed("done"), nil
	}}
	_, session := openTestSession(t, runner, nil)
	command := send("same-command", "first")
	first, err := session.Dispatch(context.Background(), command)
	if err != nil {
		t.Fatal(err)
	}
	collectTurn(t, session)
	again, err := session.Dispatch(context.Background(), command)
	if err != nil || first != again || runner.calls.Load() != 1 {
		t.Fatalf("duplicate = %+v, %v, calls=%d", again, err, runner.calls.Load())
	}
	if _, err := session.Dispatch(context.Background(), send("same-command", "changed")); !agentcore.IsRuntimeBridgeError(err, agentcore.RuntimeBridgeCommandConflict) {
		t.Fatalf("conflicting command = %v", err)
	}
}

func TestSessionRejectsUnsupportedCommandsAndMediaBeforeRunning(t *testing.T) {
	runner := &testRunner{run: func(context.Context, agentcore.SendCommand, Emit) (agentcore.TurnResultEvent, error) {
		return completed("unexpected"), nil
	}}
	_, session := openTestSession(t, runner, nil)
	commands := []agentcore.RuntimeCommand{
		{CommandID: "steer", Kind: agentcore.RuntimeCommandSteer, Steer: &agentcore.SteerCommand{Content: []agentcore.ContentBlock{{Kind: agentcore.ContentBlockText, Text: "change"}}}},
		{CommandID: "model", Kind: agentcore.RuntimeCommandSetModel, SetModel: &agentcore.SetModelCommand{Model: "different"}},
		{CommandID: "approval", Kind: agentcore.RuntimeCommandAnswerPermission, AnswerPermission: &agentcore.AnswerPermissionCommand{RequestID: "approval", Decision: agentcore.PermissionApproved}},
		{CommandID: "image", Kind: agentcore.RuntimeCommandSend, Send: &agentcore.SendCommand{Content: []agentcore.ContentBlock{{Kind: agentcore.ContentBlockImage, Data: []byte("image"), MediaType: "image/png"}}}},
	}
	for _, command := range commands {
		if _, err := session.Dispatch(context.Background(), command); !agentcore.IsRuntimeBridgeError(err, agentcore.RuntimeBridgeUnsupportedCommand) {
			t.Fatalf("%s = %v", command.CommandID, err)
		}
	}
	if runner.calls.Load() != 0 {
		t.Fatal("unsupported input reached runner")
	}
	capabilities := session.Capabilities()
	if err := capabilities.Validate(); err != nil {
		t.Fatal(err)
	}
	if capabilities.SupportsResume || capabilities.SupportedCommands.AnswerPermission || capabilities.SupportedCommands.Steer || capabilities.SupportedCommands.CancelSession {
		t.Fatalf("overstated capabilities: %+v", capabilities)
	}
}

func TestSessionCancellationSettlesBeforeUncooperativeRunnerAndRejectsOverlap(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	releaseRunner := func() { releaseOnce.Do(func() { close(release) }) }
	defer releaseRunner()
	runner := &testRunner{run: func(context.Context, agentcore.SendCommand, Emit) (agentcore.TurnResultEvent, error) {
		close(started)
		<-release
		return completed("late success"), nil
	}}
	_, session := openTestSession(t, runner, nil)
	if _, err := session.Dispatch(context.Background(), send("send", "start")); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("runner did not start")
	}
	cancel := agentcore.RuntimeCommand{CommandID: "cancel", Kind: agentcore.RuntimeCommandCancel, Cancel: &agentcore.CancelCommand{Target: agentcore.CancelTargetTurn}}
	if _, err := session.Dispatch(context.Background(), cancel); err != nil {
		t.Fatal(err)
	}
	events := collectTurn(t, session)
	if terminal(events).Outcome.Kind != agentcore.TurnOutcomeCancelled {
		t.Fatalf("cancel result = %+v", terminal(events))
	}
	if _, err := session.Dispatch(context.Background(), send("overlap", "second")); !agentcore.IsRuntimeBridgeError(err, agentcore.RuntimeBridgeQueueFull) {
		t.Fatalf("overlap = %v", err)
	}
	releaseRunner()
	assertLateRunnerSettledWithoutEvents(t, session)
}

func TestSessionTimeoutCannotBecomeLateSuccess(t *testing.T) {
	release := make(chan struct{})
	var releaseOnce sync.Once
	releaseRunner := func() { releaseOnce.Do(func() { close(release) }) }
	defer releaseRunner()
	runner := &testRunner{run: func(context.Context, agentcore.SendCommand, Emit) (agentcore.TurnResultEvent, error) {
		<-release
		return completed("late success"), nil
	}}
	_, session := openTestSession(t, runner, func(c *Config) { c.TurnTimeout = 25 * time.Millisecond })
	if _, err := session.Dispatch(context.Background(), send("send", "start")); err != nil {
		t.Fatal(err)
	}
	events := collectTurn(t, session)
	if terminal(events).Outcome.Kind != agentcore.TurnOutcomeFailed || !strings.Contains(terminal(events).ResultText, "deadline") {
		t.Fatalf("timeout = %+v", terminal(events))
	}
	if _, err := session.Dispatch(context.Background(), send("after-timeout", "retry")); !agentcore.IsRuntimeBridgeError(err, agentcore.RuntimeBridgeBackendRejected) {
		t.Fatalf("timed-out session reused: %v", err)
	}
	releaseRunner()
	assertLateRunnerSettledWithoutEvents(t, session)
}

func TestSessionDispatchContextDoesNotOwnAdmittedTurn(t *testing.T) {
	release := make(chan struct{})
	ctxSeen := make(chan context.Context, 1)
	runner := &testRunner{run: func(ctx context.Context, _ agentcore.SendCommand, _ Emit) (agentcore.TurnResultEvent, error) {
		ctxSeen <- ctx
		<-release
		return completed("done"), nil
	}}
	_, session := openTestSession(t, runner, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if _, err := session.Dispatch(ctx, send("send", "start")); err != nil {
		t.Fatal(err)
	}
	cancel()
	select {
	case executionCtx := <-ctxSeen:
		if executionCtx.Err() != nil {
			t.Fatal("acknowledgement cancellation cancelled execution")
		}
	case <-time.After(time.Second):
		t.Fatal("runner did not start")
	}
	close(release)
	if terminal(collectTurn(t, session)).Outcome.Kind != agentcore.TurnOutcomeCompleted {
		t.Fatal("admitted turn did not complete")
	}
}

func TestSessionInvalidRunnerEventsAndOutcomesFailClosed(t *testing.T) {
	tests := []struct {
		name string
		run  func(context.Context, agentcore.SendCommand, Emit) (agentcore.TurnResultEvent, error)
	}{
		{"forged-start", func(_ context.Context, _ agentcore.SendCommand, emit Emit) (agentcore.TurnResultEvent, error) {
			_ = emit(agentcore.SessionEvent{Kind: agentcore.EventTurnStarted, TurnStarted: &agentcore.TurnStartedEvent{Epoch: 1}})
			return completed("ignored error"), nil
		}},
		{"forged-terminal", func(_ context.Context, _ agentcore.SendCommand, emit Emit) (agentcore.TurnResultEvent, error) {
			result := completed("forged")
			_ = emit(agentcore.SessionEvent{Kind: agentcore.EventTurnResult, TurnResult: &result})
			return completed("ignored error"), nil
		}},
		{"unmatched-tool-result", func(_ context.Context, _ agentcore.SendCommand, emit Emit) (agentcore.TurnResultEvent, error) {
			_ = emit(agentcore.SessionEvent{Kind: agentcore.EventToolResult, ToolResult: &agentcore.ToolResultEvent{ToolUseID: "unknown"}})
			return completed("ignored error"), nil
		}},
		{"unfinished-tool", func(_ context.Context, _ agentcore.SendCommand, emit Emit) (agentcore.TurnResultEvent, error) {
			_ = emit(agentcore.SessionEvent{Kind: agentcore.EventToolCall, ToolCall: &agentcore.ToolCallEvent{ToolUseID: "pending", Name: "search"}})
			return completed("done"), nil
		}},
		{"empty-outcome", func(context.Context, agentcore.SendCommand, Emit) (agentcore.TurnResultEvent, error) {
			return agentcore.TurnResultEvent{}, nil
		}},
		{"panic", func(context.Context, agentcore.SendCommand, Emit) (agentcore.TurnResultEvent, error) {
			panic("private provider detail")
		}},
		{"provider-error", func(context.Context, agentcore.SendCommand, Emit) (agentcore.TurnResultEvent, error) {
			return completed("incorrect success"), errors.New("private provider detail")
		}},
		{"oversized-result", func(context.Context, agentcore.SendCommand, Emit) (agentcore.TurnResultEvent, error) {
			return completed(strings.Repeat("x", 1024*1024+1)), nil
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, session := openTestSession(t, &testRunner{run: test.run}, nil)
			if _, err := session.Dispatch(context.Background(), send("send", "start")); err != nil {
				t.Fatal(err)
			}
			result := terminal(collectTurn(t, session))
			if !result.IsError || result.Outcome.Kind != agentcore.TurnOutcomeFailed || strings.Contains(result.ResultText, "private provider detail") {
				t.Fatalf("invalid runner became success or leaked error: %+v", result)
			}
		})
	}
}

func TestSessionPreservesSafeFailureCodeWithoutProviderDetails(t *testing.T) {
	for _, code := range []string{"tool_schema_invalid", "tool_denied", "tool_journal_unavailable", "deadline_exceeded", "private-secret"} {
		t.Run(code, func(t *testing.T) {
			_, session := openTestSession(t, &testRunner{run: func(context.Context, agentcore.SendCommand, Emit) (agentcore.TurnResultEvent, error) {
				return completed("incorrect success"), NewExecutionError(code, errors.New("provider credential=private-secret"))
			}}, nil)
			if _, err := session.Dispatch(context.Background(), send("send", "start")); err != nil {
				t.Fatal(err)
			}
			result := terminal(collectTurn(t, session))
			want := "managed kernel execution failed"
			if code != "private-secret" {
				want += ": " + code
			}
			if !result.IsError || result.ResultText != want || strings.Contains(result.ResultText, "private-secret") {
				t.Fatalf("failure diagnostic = %+v", result)
			}
		})
	}
}

func TestSessionCancellationCannotHideAnInvalidRunnerEvent(t *testing.T) {
	invalid := make(chan struct{})
	release := make(chan struct{})
	defer close(release)
	runner := &testRunner{run: func(_ context.Context, _ agentcore.SendCommand, emit Emit) (agentcore.TurnResultEvent, error) {
		_ = emit(agentcore.SessionEvent{Kind: agentcore.EventTurnStarted, TurnStarted: &agentcore.TurnStartedEvent{Epoch: 1}})
		close(invalid)
		<-release
		return completed("ignored error"), nil
	}}
	_, session := openTestSession(t, runner, nil)
	if _, err := session.Dispatch(context.Background(), send("send", "start")); err != nil {
		t.Fatal(err)
	}
	select {
	case <-invalid:
	case <-time.After(10 * time.Second):
		t.Fatal("runner did not emit invalid event")
	}
	if _, err := session.Dispatch(context.Background(), agentcore.RuntimeCommand{CommandID: "cancel", Kind: agentcore.RuntimeCommandCancel,
		Cancel: &agentcore.CancelCommand{Target: agentcore.CancelTargetTurn}}); err != nil {
		t.Fatal(err)
	}
	if result := terminal(collectTurn(t, session)); result.Outcome.Kind != agentcore.TurnOutcomeFailed || !result.IsError {
		t.Fatalf("cancellation hid protocol failure: %+v", result)
	}
	if _, err := session.Dispatch(context.Background(), send("retry", "retry")); !agentcore.IsRuntimeBridgeError(err, agentcore.RuntimeBridgeBackendRejected) {
		t.Fatalf("invalid session became reusable: %v", err)
	}
}

func TestSessionBackpressureClosesStreamInsteadOfHanging(t *testing.T) {
	finished := make(chan struct{})
	runner := &testRunner{run: func(_ context.Context, _ agentcore.SendCommand, emit Emit) (agentcore.TurnResultEvent, error) {
		defer close(finished)
		for i := 0; i < 10; i++ {
			if err := emit(agentcore.SessionEvent{Kind: agentcore.EventMessageDelta, MessageDelta: &agentcore.MessageDeltaEvent{ItemID: "answer", Text: "x"}}); err != nil {
				return agentcore.TurnResultEvent{}, err
			}
		}
		return completed("done"), nil
	}}
	_, session := openTestSession(t, runner, func(c *Config) { c.EventCapacity = 4; c.BackpressureWait = 20 * time.Millisecond })
	if _, err := session.Dispatch(context.Background(), send("send", "start")); err != nil {
		t.Fatal(err)
	}
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("backpressure blocked runner")
	}
	for range session.Events() {
	}
	if _, err := session.Dispatch(context.Background(), send("after-close", "retry")); !agentcore.IsRuntimeBridgeError(err, agentcore.RuntimeBridgeTransportClosed) {
		t.Fatalf("closed stream reused: %v", err)
	}
}

func TestSessionCloseIsBoundedAndReleasesRunnerOnce(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	runner := &testRunner{run: func(context.Context, agentcore.SendCommand, Emit) (agentcore.TurnResultEvent, error) {
		return completed("done"), nil
	}, close: func(context.Context) error { <-release; return nil }}
	connection, session := openTestSession(t, runner, func(c *Config) { c.CloseTimeout = 20 * time.Millisecond })
	for i := 0; i < 2; i++ {
		if err := session.Close(context.Background()); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("close = %v", err)
		}
	}
	if runner.closes.Load() != 1 {
		t.Fatalf("runner closed %d times", runner.closes.Load())
	}
	_ = connection.CloseSession(context.Background(), "session-test")
}
