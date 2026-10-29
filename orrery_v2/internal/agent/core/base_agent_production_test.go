package core

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestNewProductionBaseAgentRejectsMissingPipelineDependencies(t *testing.T) {
	_, err := NewProductionBaseAgent(BaseAgentConfig{
		Identity: AgentIdentity{AgentID: "agent-1", AgentProfileID: "profile-1", InstanceRef: AgentInstanceRef{ID: "instance-1"}},
		Profile:  AgentProfile{ID: "profile-1", RuntimeSpec: RuntimeSpec{Kind: RuntimeKindCodex}},
		Runtime:  productionTestSessionRuntime{},
		Delivery: &capturingDelivery{},
		Trace:    &recordingTraceRecorder{},
		Notifier: &recordingNotifier{},
	})
	if err == nil {
		t.Fatalf("expected missing normalizer/validator/summary to fail")
	}
	var dependencyErr PipelineDependencyError
	if !errors.As(err, &dependencyErr) {
		t.Fatalf("expected PipelineDependencyError, got %T: %v", err, err)
	}
	if dependencyErr.Dependency != "normalizer" {
		t.Fatalf("dependency = %q", dependencyErr.Dependency)
	}
}

func TestNewProductionBaseAgentAcceptsCompletePipelineDependencies(t *testing.T) {
	durableTrace := &capturingDurableTraceRecorder{session: &capturingDurableTraceSession{ref: "store://trace-v2/run-1"}}
	agent, err := NewProductionBaseAgent(BaseAgentConfig{
		Identity: AgentIdentity{AgentID: "agent-1", AgentProfileID: "profile-1", InstanceRef: AgentInstanceRef{ID: "instance-1"}},
		Profile:  AgentProfile{ID: "profile-1", RuntimeSpec: RuntimeSpec{Kind: RuntimeKindCodex}},
		Runtime:  productionTestSessionRuntime{},
		Delivery: &capturingDelivery{},
		Normalizer: ResultNormalizerFunc(func(input AgentInput, response RuntimeResponse, now time.Time) (AgentResult, error) {
			return AgentResult{}, nil
		}),
		Validator: ResultValidatorFunc(func(AgentInput, AgentResult) error { return nil }),
		Summary:   staticSummaryGenerator{summary: Summary{Text: "summary"}},
		Trace:     durableTrace,
		Notifier:  &recordingNotifier{},
	})
	if err != nil {
		t.Fatalf("new production base agent: %v", err)
	}
	if agent == nil {
		t.Fatalf("expected production agent")
	}
}

func TestNewProductionBaseAgentRejectsLegacyOnlyTraceRecorder(t *testing.T) {
	_, err := NewProductionBaseAgent(BaseAgentConfig{
		Identity: AgentIdentity{AgentID: "agent-1", AgentProfileID: "profile-1", InstanceRef: AgentInstanceRef{ID: "instance-1"}},
		Profile:  AgentProfile{ID: "profile-1", RuntimeSpec: RuntimeSpec{Kind: RuntimeKindCodex}},
		Runtime:  productionTestSessionRuntime{}, Delivery: &capturingDelivery{},
		Normalizer: ResultNormalizerFunc(func(AgentInput, RuntimeResponse, time.Time) (AgentResult, error) { return AgentResult{}, nil }),
		Validator:  ResultValidatorFunc(func(AgentInput, AgentResult) error { return nil }), Summary: staticSummaryGenerator{},
		Trace: &recordingTraceRecorder{}, Notifier: &recordingNotifier{},
	})
	var dependencyErr PipelineDependencyError
	if !errors.As(err, &dependencyErr) || dependencyErr.Dependency != "trace" {
		t.Fatalf("error=%T %[1]v", err)
	}
}

type productionTestSessionRuntime struct{}

func (productionTestSessionRuntime) RunSession(context.Context, RuntimeRequest, SessionEventObserver) (RuntimeResponse, error) {
	return RuntimeResponse{Status: RuntimeStatusCompleted}, nil
}
func (productionTestSessionRuntime) CancelSession(context.Context, AgentRunID, string) error {
	return nil
}
func (productionTestSessionRuntime) SteerSession(context.Context, AgentRunID, string) error {
	return nil
}
func (productionTestSessionRuntime) PrewarmSession(context.Context, RuntimeRequest) error { return nil }
func (productionTestSessionRuntime) ReleaseRuntimeWorkspace(context.Context, string) error {
	return nil
}
func (productionTestSessionRuntime) Capabilities(context.Context) Capabilities {
	return Capabilities{}
}
