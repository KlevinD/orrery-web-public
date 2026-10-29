package lifecycle

import (
	"context"
	"errors"
	"testing"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/platform/clock"
)

func TestAcceptanceAfterRuntimeFinishCanAppendMetadataFromRuntimeResponseRef(t *testing.T) {
	registry := NewHookRegistry()
	if err := registry.RegisterPostHookE(NewPostHook("runtime-response-metadata", func(_ context.Context, state *AgentRunState) error {
		if state.RuntimeResponseRef == "" {
			return errors.New("missing runtime response ref")
		}
		state.SetMetadata("runtime_response_seen", state.RuntimeResponseRef)
		return nil
	}), HookOptions{Order: 10, FailurePolicy: FailurePolicyStop}); err != nil {
		t.Fatalf("register hook failed: %v", err)
	}

	executor := NewPipelineExecutor(PipelineOptions{
		Bus:   NewInMemorySignalBus(SignalBusOptions{Clock: clock.NewFake(testNow())}),
		Clock: clock.NewFake(testNow()),
		Hooks: registry,
	})
	state := validRunState()
	err := executor.RunStep(context.Background(), &state, PipelineStep{
		Name:   "runtime_finish",
		Before: BeforeRuntimeFinish,
		After:  AfterRuntimeFinish,
		Step: func(_ context.Context, state *AgentRunState) error {
			state.RuntimeResponseRef = "artifact://runtime/response-001"
			return nil
		},
	})
	if err != nil {
		t.Fatalf("runtime finish failed: %v", err)
	}
	if state.Metadata["runtime_response_seen"] != "artifact://runtime/response-001" {
		t.Fatalf("expected runtime response metadata, got %#v", state.Metadata)
	}
}

func TestAcceptanceBeforeResultCommitRejectsMissingEvidence(t *testing.T) {
	bus := NewInMemorySignalBus(SignalBusOptions{Clock: clock.NewFake(testNow())})
	mustSubscribe(t, bus, BeforeResultCommit, HandlerFunc("evidence-required", func(context.Context, SignalEnvelope) error {
		return errors.New("missing evidence")
	}), HandlerOptions{FailurePolicy: FailurePolicyStop})
	mustSubscribe(t, bus, AgentFailed, HandlerFunc("failure-recorder", func(context.Context, SignalEnvelope) error {
		return nil
	}), HandlerOptions{FailurePolicy: FailurePolicyContinue})

	executor := NewPipelineExecutor(PipelineOptions{Bus: bus, Clock: clock.NewFake(testNow())})
	state := validRunState()
	state.AgentResult = agentcore.AgentResult{Evidence: nil}

	err := executor.RunStep(context.Background(), &state, PipelineStep{
		Name:   "commit_result",
		Before: BeforeResultCommit,
		After:  AfterResultCommit,
		Step:   func(context.Context, *AgentRunState) error { return nil },
	})
	if err == nil {
		t.Fatal("expected missing evidence to stop result commit")
	}
	if !IsPipelineStopped(err) {
		t.Fatalf("expected pipeline stopped, got %v", err)
	}
	if len(state.Errors) == 0 {
		t.Fatal("expected state to record commit rejection")
	}
}
