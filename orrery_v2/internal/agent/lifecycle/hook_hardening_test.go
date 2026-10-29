package lifecycle

import (
	"context"
	"errors"
	"testing"

	"orrery_v2/internal/platform/clock"
)

func TestHookRegistryRunsEnabledHooksInOrder(t *testing.T) {
	registry := NewHookRegistry()
	var order []string
	registry.RegisterPreHookE(NewPreHook("late", func(context.Context, *AgentRunState) error {
		order = append(order, "late")
		return nil
	}), HookOptions{Order: 20})
	registry.RegisterPreHookE(NewPreHook("disabled", func(context.Context, *AgentRunState) error {
		order = append(order, "disabled")
		return nil
	}), HookOptions{Order: 5, Disabled: true})
	registry.RegisterPreHookE(NewPreHook("early", func(context.Context, *AgentRunState) error {
		order = append(order, "early")
		return nil
	}), HookOptions{Order: 10})

	for _, hook := range registry.PreHooks() {
		if err := hook.BeforeAgentStep(context.Background(), &AgentRunState{}); err != nil {
			t.Fatalf("hook failed: %v", err)
		}
	}

	if len(order) != 2 || order[0] != "early" || order[1] != "late" {
		t.Fatalf("unexpected hook order: %#v", order)
	}
}

func TestHookRegistryRejectsDuplicateHookNames(t *testing.T) {
	registry := NewHookRegistry()
	if err := registry.RegisterPreHookE(NewPreHook("permission", func(context.Context, *AgentRunState) error { return nil }), HookOptions{}); err != nil {
		t.Fatalf("first register failed: %v", err)
	}
	if err := registry.RegisterPreHookE(NewPreHook("permission", func(context.Context, *AgentRunState) error { return nil }), HookOptions{}); err == nil {
		t.Fatal("expected duplicate hook registration to fail")
	}
}

func TestPipelineAppliesHookFailurePoliciesToState(t *testing.T) {
	registry := NewHookRegistry()
	registry.RegisterPreHookE(NewPreHook("cost", func(context.Context, *AgentRunState) error {
		return errors.New("cost unavailable")
	}), HookOptions{
		Order:             10,
		FailurePolicy:     FailurePolicyDegrade,
		DegradeCapability: "cost_estimation",
	})
	registry.RegisterPreHookE(NewPreHook("audit", func(context.Context, *AgentRunState) error {
		return errors.New("audit sink unavailable")
	}), HookOptions{
		Order:         20,
		FailurePolicy: FailurePolicyContinue,
	})

	executor := NewPipelineExecutor(PipelineOptions{
		Bus:   NewInMemorySignalBus(SignalBusOptions{Clock: clock.NewFake(testNow())}),
		Clock: clock.NewFake(testNow()),
		Hooks: registry,
	})
	state := validRunState()
	err := executor.RunStep(context.Background(), &state, PipelineStep{
		Name:   "accept",
		Before: BeforeTaskAccepted,
		After:  AfterTaskAccepted,
		Step: func(context.Context, *AgentRunState) error {
			return nil
		},
	})
	if err != nil {
		t.Fatalf("soft hook failures should not stop pipeline: %v", err)
	}
	if len(state.Warnings) != 2 {
		t.Fatalf("expected hook warnings, got %#v", state.Warnings)
	}
	if len(state.DegradedCapabilities) != 1 || state.DegradedCapabilities[0] != "cost_estimation" {
		t.Fatalf("expected degraded capability, got %#v", state.DegradedCapabilities)
	}
}
