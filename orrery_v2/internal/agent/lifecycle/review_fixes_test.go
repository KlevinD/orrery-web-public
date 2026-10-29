package lifecycle

import (
	"context"
	"errors"
	"testing"

	"orrery_v2/internal/platform/clock"
)

func TestDefaultSignalBuilderDoesNotRepeatIDsAcrossBuilders(t *testing.T) {
	first := NewSignalBuilder(SignalBuilderOptions{Clock: clock.NewFake(testNow())})
	second := NewSignalBuilder(SignalBuilderOptions{Clock: clock.NewFake(testNow())})

	firstSignal, err := first.FromState(validRunState(), BeforeTaskAccepted, EmptyPayload{}, SignalBuildOptions{})
	if err != nil {
		t.Fatalf("build first signal failed: %v", err)
	}
	secondSignal, err := second.FromState(validRunState(), BeforeTaskAccepted, EmptyPayload{}, SignalBuildOptions{})
	if err != nil {
		t.Fatalf("build second signal failed: %v", err)
	}

	if firstSignal.SignalID == secondSignal.SignalID {
		t.Fatalf("default builders generated duplicate signal ids: %q", firstSignal.SignalID)
	}
}

func TestHookOptionsZeroValueRegistersEnabledHook(t *testing.T) {
	registry := NewHookRegistry()
	called := false
	if err := registry.RegisterPreHookE(NewPreHook("zero-value-enabled", func(context.Context, *AgentRunState) error {
		called = true
		return nil
	}), HookOptions{}); err != nil {
		t.Fatalf("register hook failed: %v", err)
	}

	for _, hook := range registry.PreHooks() {
		if err := hook.BeforeAgentStep(context.Background(), &AgentRunState{}); err != nil {
			t.Fatalf("hook failed: %v", err)
		}
	}
	if !called {
		t.Fatal("expected zero-value HookOptions hook to run")
	}
}

func TestHookOptionsDisabledExplicitlySkipsHook(t *testing.T) {
	registry := NewHookRegistry()
	called := false
	if err := registry.RegisterPreHookE(NewPreHook("disabled", func(context.Context, *AgentRunState) error {
		called = true
		return nil
	}), HookOptions{Disabled: true}); err != nil {
		t.Fatalf("register hook failed: %v", err)
	}

	if len(registry.PreHooks()) != 0 {
		t.Fatalf("expected disabled hook to be filtered")
	}
	if called {
		t.Fatal("disabled hook should not run")
	}
}

func TestDirectEmitReturnsNilForSoftFailuresWhilePipelineRecordsState(t *testing.T) {
	bus := NewInMemorySignalBus(SignalBusOptions{Clock: clock.NewFake(testNow())})
	mustSubscribe(t, bus, BeforeTaskAccepted, HandlerFunc("cost", func(context.Context, SignalEnvelope) error {
		return errors.New("cost unavailable")
	}), HandlerOptions{FailurePolicy: FailurePolicyDegrade, DegradeCapability: "cost_estimation"})

	if _, err := bus.Emit(context.Background(), validSignal()); err != nil {
		t.Fatalf("direct Emit should not return soft failure as error: %v", err)
	}

	executor := NewPipelineExecutor(PipelineOptions{Bus: bus, Clock: clock.NewFake(testNow())})
	state := validRunState()
	if err := executor.RunStep(context.Background(), &state, PipelineStep{
		Name:   "accept",
		Before: BeforeTaskAccepted,
		After:  AfterTaskAccepted,
		Step:   func(context.Context, *AgentRunState) error { return nil },
	}); err != nil {
		t.Fatalf("pipeline should continue after soft handler failure: %v", err)
	}
	if len(state.Warnings) == 0 || len(state.DegradedCapabilities) != 1 || state.DegradedCapabilities[0] != "cost_estimation" {
		t.Fatalf("expected pipeline to record soft failure in state, got warnings=%#v degraded=%#v", state.Warnings, state.DegradedCapabilities)
	}
}

func TestPipelineRecordsWarningWhenExceptionSignalEmissionFails(t *testing.T) {
	bus := NewInMemorySignalBus(SignalBusOptions{Clock: clock.NewFake(testNow())})
	mustSubscribe(t, bus, AgentFailed, HandlerFunc("failure-handler", func(context.Context, SignalEnvelope) error {
		return errors.New("failure signal sink down")
	}), HandlerOptions{FailurePolicy: FailurePolicyStop})

	executor := NewPipelineExecutor(PipelineOptions{Bus: bus, Clock: clock.NewFake(testNow())})
	state := validRunState()
	err := executor.RunStep(context.Background(), &state, PipelineStep{
		Name:   "accept",
		Before: BeforeTaskAccepted,
		After:  AfterTaskAccepted,
		Step:   func(context.Context, *AgentRunState) error { return errors.New("accept failed") },
	})
	if err == nil {
		t.Fatal("expected pipeline to stop")
	}
	if len(state.Warnings) == 0 {
		t.Fatal("expected warning when exception signal emission fails")
	}
}
