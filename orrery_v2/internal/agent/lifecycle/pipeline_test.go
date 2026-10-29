package lifecycle

import (
	"context"
	"errors"
	"testing"

	"orrery_v2/internal/platform/clock"
)

func TestPipelineExecutorEmitsBeforeAndAfterAroundStep(t *testing.T) {
	bus := NewInMemorySignalBus(SignalBusOptions{Clock: clock.NewFake(testNow())})
	var order []string
	mustSubscribe(t, bus, BeforeTaskAccepted, HandlerFunc("before", func(context.Context, SignalEnvelope) error {
		order = append(order, "before")
		return nil
	}), HandlerOptions{})
	mustSubscribe(t, bus, AfterTaskAccepted, HandlerFunc("after", func(context.Context, SignalEnvelope) error {
		order = append(order, "after")
		return nil
	}), HandlerOptions{})

	executor := NewPipelineExecutor(PipelineOptions{Bus: bus, Clock: clock.NewFake(testNow())})
	err := executor.RunStep(context.Background(), &AgentRunState{
		MissionID:      validSignal().MissionID,
		AgentID:        validSignal().AgentID,
		AgentProfileID: validSignal().AgentProfileID,
		TaskID:         validSignal().TaskID,
		RunID:          validSignal().RunID,
		Attempt:        1,
	}, PipelineStep{
		Name:   "accept",
		Before: BeforeTaskAccepted,
		After:  AfterTaskAccepted,
		Step: func(context.Context, *AgentRunState) error {
			order = append(order, "step")
			return nil
		},
	})
	if err != nil {
		t.Fatalf("pipeline step failed: %v", err)
	}

	want := []string{"before", "step", "after"}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("unexpected order: want %#v got %#v", want, order)
		}
	}
}

func TestPipelineExecutorStopHandlerFailureStopsAndEmitsAgentFailed(t *testing.T) {
	bus := NewInMemorySignalBus(SignalBusOptions{Clock: clock.NewFake(testNow())})
	var emitted []SignalName
	mustSubscribe(t, bus, BeforeTaskAccepted, HandlerFunc("permission", func(context.Context, SignalEnvelope) error {
		emitted = append(emitted, BeforeTaskAccepted)
		return errors.New("permission denied")
	}), HandlerOptions{FailurePolicy: FailurePolicyStop})
	mustSubscribe(t, bus, AgentFailed, HandlerFunc("failure-recorder", func(_ context.Context, signal SignalEnvelope) error {
		emitted = append(emitted, signal.Name)
		return nil
	}), HandlerOptions{FailurePolicy: FailurePolicyContinue})

	state := validRunState()
	executor := NewPipelineExecutor(PipelineOptions{Bus: bus, Clock: clock.NewFake(testNow())})
	err := executor.RunStep(context.Background(), &state, PipelineStep{
		Name:   "accept",
		Before: BeforeTaskAccepted,
		After:  AfterTaskAccepted,
		Step: func(context.Context, *AgentRunState) error {
			t.Fatal("step should not run after before signal stop failure")
			return nil
		},
	})

	if err == nil {
		t.Fatal("expected pipeline stopped error")
	}
	if !IsPipelineStopped(err) {
		t.Fatalf("expected pipeline_stopped, got %v", err)
	}
	if len(emitted) != 2 || emitted[0] != BeforeTaskAccepted || emitted[1] != AgentFailed {
		t.Fatalf("expected before then AgentFailed, got %#v", emitted)
	}
	if len(state.Errors) == 0 {
		t.Fatal("expected state to record stop error")
	}
}

func TestPipelineExecutorClassifiesContextErrors(t *testing.T) {
	for _, tc := range []struct {
		name   string
		ctx    func() (context.Context, context.CancelFunc)
		signal SignalName
	}{
		{
			name: "cancelled",
			ctx: func() (context.Context, context.CancelFunc) {
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				return ctx, func() {}
			},
			signal: AgentCancelled,
		},
		{
			name: "timeout",
			ctx: func() (context.Context, context.CancelFunc) {
				ctx, cancel := context.WithTimeout(context.Background(), 0)
				return ctx, cancel
			},
			signal: AgentTimeout,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bus := NewInMemorySignalBus(SignalBusOptions{Clock: clock.NewFake(testNow())})
			var emitted []SignalName
			mustSubscribe(t, bus, tc.signal, HandlerFunc("recorder", func(_ context.Context, signal SignalEnvelope) error {
				emitted = append(emitted, signal.Name)
				return nil
			}), HandlerOptions{FailurePolicy: FailurePolicyContinue})

			ctx, cancel := tc.ctx()
			defer cancel()
			state := validRunState()
			executor := NewPipelineExecutor(PipelineOptions{Bus: bus, Clock: clock.NewFake(testNow())})
			err := executor.RunStep(ctx, &state, PipelineStep{
				Name:   "runtime",
				Before: BeforeRuntimeStart,
				After:  AfterRuntimeFinish,
				Step: func(context.Context, *AgentRunState) error {
					return ctx.Err()
				},
			})
			if err == nil {
				t.Fatal("expected pipeline error")
			}
			if len(emitted) != 1 || emitted[0] != tc.signal {
				t.Fatalf("expected %s signal, got %#v", tc.signal, emitted)
			}
		})
	}
}

func TestPipelineExecutorRunsMiddlewareAroundStep(t *testing.T) {
	var order []string
	executor := NewPipelineExecutor(PipelineOptions{
		Bus:   NewInMemorySignalBus(SignalBusOptions{Clock: clock.NewFake(testNow())}),
		Clock: clock.NewFake(testNow()),
		Middleware: []AgentMiddleware{
			MiddlewareFunc("outer", func(next AgentStep) AgentStep {
				return AgentStepFunc(func(ctx context.Context, state *AgentRunState) error {
					order = append(order, "outer-before")
					err := next.Run(ctx, state)
					order = append(order, "outer-after")
					return err
				})
			}),
		},
	})
	state := validRunState()
	err := executor.RunStep(context.Background(), &state, PipelineStep{
		Name:   "accept",
		Before: BeforeTaskAccepted,
		After:  AfterTaskAccepted,
		Step: func(context.Context, *AgentRunState) error {
			order = append(order, "step")
			return nil
		},
	})
	if err != nil {
		t.Fatalf("pipeline failed: %v", err)
	}
	want := []string{"outer-before", "step", "outer-after"}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("unexpected middleware order: want %#v got %#v", want, order)
		}
	}
}

func validRunState() AgentRunState {
	signal := validSignal()
	return AgentRunState{
		MissionID:      signal.MissionID,
		AgentID:        signal.AgentID,
		AgentProfileID: signal.AgentProfileID,
		TaskID:         signal.TaskID,
		RunID:          signal.RunID,
		Attempt:        signal.Attempt,
		StartedAt:      signal.EmittedAt,
	}
}
