package lifecycle

import (
	"context"
	"testing"

	"orrery_v2/internal/platform/clock"
)

func TestStandardLifecyclePlanEmitsCompleteSignalOrder(t *testing.T) {
	bus, recorder := NewTestSignalBus()
	for _, name := range StandardLifecycleSignalOrder() {
		mustSubscribe(t, bus, name, recorder, HandlerOptions{FailurePolicy: FailurePolicyContinue})
	}
	executor := NewPipelineExecutor(PipelineOptions{
		Bus:   bus,
		Clock: clock.NewFake(testNow()),
	})

	state := validRunState()
	plan := StandardLifecyclePlan(StandardLifecycleSteps{
		ReceiveTask: func(context.Context, *AgentRunState) error { return nil },
		AcceptTask:  func(context.Context, *AgentRunState) error { return nil },
		BuildContext: func(_ context.Context, state *AgentRunState) error {
			state.SetMetadata("context_pack_ref", "ctx-001")
			return nil
		},
		StartRuntime:    func(context.Context, *AgentRunState) error { return nil },
		FinishRuntime:   func(context.Context, *AgentRunState) error { return nil },
		NormalizeResult: func(context.Context, *AgentRunState) error { return nil },
		ValidateResult:  func(context.Context, *AgentRunState) error { return nil },
		CommitResult:    func(context.Context, *AgentRunState) error { return nil },
		Reply:           func(context.Context, *AgentRunState) error { return nil },
	})

	if err := executor.RunPlan(context.Background(), &state, plan); err != nil {
		t.Fatalf("standard lifecycle plan failed: %v", err)
	}

	got := recorder.Names()
	want := StandardLifecycleSignalOrder()
	if len(got) != len(want) {
		t.Fatalf("expected %d signals, got %d: %#v", len(want), len(got), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("signal order drifted at %d: want %s got %s", i, want[i], got[i])
		}
	}
}

func TestRuntimeProgressCanBeEmittedInsidePlanStep(t *testing.T) {
	bus, recorder := NewTestSignalBus()
	for _, name := range []SignalName{BeforeRuntimeStart, RuntimeStarted, RuntimeProgress, BeforeRuntimeFinish, AfterRuntimeFinish} {
		mustSubscribe(t, bus, name, recorder, HandlerOptions{FailurePolicy: FailurePolicyContinue})
	}
	executor := NewPipelineExecutor(PipelineOptions{Bus: bus, Clock: clock.NewFake(testNow())})
	state := validRunState()
	plan := []PipelineStep{
		{
			Name:   "runtime-start",
			Before: BeforeRuntimeStart,
			After:  RuntimeStarted,
			Step: func(ctx context.Context, state *AgentRunState) error {
				return executor.EmitRuntimeProgress(ctx, state, RuntimeProgressPayload{ProgressRef: "progress-001", Message: "halfway"})
			},
		},
		{
			Name:   "runtime-finish",
			Before: BeforeRuntimeFinish,
			After:  AfterRuntimeFinish,
			Step:   func(context.Context, *AgentRunState) error { return nil },
		},
	}

	if err := executor.RunPlan(context.Background(), &state, plan); err != nil {
		t.Fatalf("runtime plan failed: %v", err)
	}
	want := []SignalName{BeforeRuntimeStart, RuntimeProgress, RuntimeStarted, BeforeRuntimeFinish, AfterRuntimeFinish}
	got := recorder.Names()
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("runtime signal order drifted: want %#v got %#v", want, got)
		}
	}
}
