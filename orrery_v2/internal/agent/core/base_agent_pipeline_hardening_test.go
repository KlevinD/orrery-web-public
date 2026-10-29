package core

import (
	"context"
	"errors"
	"testing"
)

func TestBaseAgentUsesSummaryGeneratorAndTraceRecorderBeforeValidation(t *testing.T) {
	delivery := &capturingDelivery{}
	trace := &recordingTraceRecorder{ref: "store://trace/run-1"}
	agent := NewBaseAgent(BaseAgentConfig{
		Identity: AgentIdentity{AgentID: "agent-1", AgentProfileID: "profile-1", InstanceRef: AgentInstanceRef{ID: "instance-1"}},
		Profile:  AgentProfile{ID: "profile-1", RuntimeSpec: RuntimeSpec{Kind: RuntimeKindCodex}},
		Runtime: fakePipelineRuntime{response: RuntimeResponse{
			Status:    RuntimeStatusCompleted,
			Artifacts: []ArtifactRef{{ID: "artifact-1", Kind: ArtifactKindCode, URI: "file://diff.patch"}},
			ToolCalls: []RuntimeToolCall{{ID: "evidence-1", Name: "go test", Output: "ok"}},
		}},
		Delivery: delivery,
		Summary:  staticSummaryGenerator{summary: Summary{Text: "Generated from artifacts."}},
		Trace:    trace,
		Notifier: &recordingNotifier{},
	})

	result, err := agent.Handle(context.Background(), validAgentInput())
	if err != nil {
		t.Fatalf("handle task: %v", err)
	}
	if result.Summary.Text != "Generated from artifacts." {
		t.Fatalf("summary = %#v", result.Summary)
	}
	if result.InternalTraceRef != "store://trace/run-1" {
		t.Fatalf("trace ref = %q", result.InternalTraceRef)
	}
	if trace.calls != 1 {
		t.Fatalf("trace calls = %d", trace.calls)
	}
}

func TestBaseAgentDoesNotInventEvidenceFromArtifactIndexes(t *testing.T) {
	delivery := &capturingDelivery{}
	agent := NewBaseAgent(BaseAgentConfig{
		Identity: AgentIdentity{AgentID: "agent-1", AgentProfileID: "profile-1", InstanceRef: AgentInstanceRef{ID: "instance-1"}},
		Profile:  AgentProfile{ID: "profile-1", RuntimeSpec: RuntimeSpec{Kind: RuntimeKindCodex}},
		Runtime: fakePipelineRuntime{response: RuntimeResponse{
			Status:   RuntimeStatusCompleted,
			Messages: []RuntimeMessage{{Type: RuntimeMessageText, Content: "Done."}},
			Artifacts: []ArtifactRef{
				{ID: "artifact-1", Kind: ArtifactKindCode, URI: "file://diff.patch"},
				{ID: "artifact-2", Kind: ArtifactKindLog, URI: "file://test.log"},
			},
		}},
		Delivery: delivery,
		Trace:    &recordingTraceRecorder{ref: "store://trace/run-1"},
		Notifier: &recordingNotifier{},
	})

	_, err := agent.Handle(context.Background(), validAgentInput())
	if err == nil {
		t.Fatalf("expected missing evidence validation")
	}
	if delivery.committed != nil {
		t.Fatalf("result should not commit when artifacts are not declared as evidence")
	}
}

func TestBaseAgentPipelineRejectsMissingRequiredEvidenceBeforeCommit(t *testing.T) {
	delivery := &capturingDelivery{}
	agent := NewBaseAgent(BaseAgentConfig{
		Identity: AgentIdentity{AgentID: "agent-1", AgentProfileID: "profile-1", InstanceRef: AgentInstanceRef{ID: "instance-1"}},
		Profile:  AgentProfile{ID: "profile-1", RuntimeSpec: RuntimeSpec{Kind: RuntimeKindCodex}},
		Runtime: fakePipelineRuntime{response: RuntimeResponse{
			Status:   RuntimeStatusCompleted,
			Messages: []RuntimeMessage{{Type: RuntimeMessageText, Content: "Done."}},
		}},
		Delivery: delivery,
		Trace:    &recordingTraceRecorder{ref: "store://trace/run-1"},
		Notifier: &recordingNotifier{},
	})

	_, err := agent.Handle(context.Background(), validAgentInput())
	if err == nil {
		t.Fatalf("expected validation error")
	}
	var validationErr ResultValidationError
	if !errors.As(err, &validationErr) {
		t.Fatalf("expected ResultValidationError, got %T: %v", err, err)
	}
	if delivery.committed != nil {
		t.Fatalf("result should not be committed when evidence is missing")
	}
}

func TestBaseAgentPipelineRequiresDeliveryBeforeCommit(t *testing.T) {
	recorder := &recordingSignalSink{}
	agent := NewBaseAgent(BaseAgentConfig{
		Identity: AgentIdentity{AgentID: "agent-1", AgentProfileID: "profile-1", InstanceRef: AgentInstanceRef{ID: "instance-1"}},
		Profile:  AgentProfile{ID: "profile-1", RuntimeSpec: RuntimeSpec{Kind: RuntimeKindCodex}},
		Runtime: fakePipelineRuntime{response: RuntimeResponse{
			Status: RuntimeStatusCompleted,
			Messages: []RuntimeMessage{{
				Type:    RuntimeMessageText,
				Content: "Implemented the feature.",
			}},
			Artifacts: []ArtifactRef{{ID: "artifact-1", Kind: ArtifactKindCode, URI: "file://diff.patch"}},
			ToolCalls: []RuntimeToolCall{{ID: "evidence-1", Name: "go test", Output: "ok"}},
		}},
		Signals:  recorder,
		Trace:    &recordingTraceRecorder{ref: "store://trace/run-1"},
		Notifier: &recordingNotifier{},
	})

	_, err := agent.Handle(context.Background(), validAgentInput())
	if err == nil {
		t.Fatalf("expected missing delivery dependency to fail")
	}
	var pipelineErr PipelineDependencyError
	if !errors.As(err, &pipelineErr) {
		t.Fatalf("expected PipelineDependencyError, got %T: %v", err, err)
	}
	for _, name := range recorder.Names() {
		if name == SignalAfterResultCommit {
			t.Fatalf("AfterResultCommit must not be emitted without a delivery commit")
		}
	}
}

func TestBaseAgentPipelineRequiresTraceBeforeRuntime(t *testing.T) {
	runtime := &observingRuntime{response: RuntimeResponse{Status: RuntimeStatusCompleted}}
	agent := NewBaseAgent(BaseAgentConfig{
		Identity: AgentIdentity{AgentID: "agent-1", AgentProfileID: "profile-1", InstanceRef: AgentInstanceRef{ID: "instance-1"}},
		Profile:  AgentProfile{ID: "profile-1", RuntimeSpec: RuntimeSpec{Kind: RuntimeKindCodex}},
		Runtime:  runtime,
		Delivery: &capturingDelivery{},
		Notifier: &recordingNotifier{},
	})

	_, err := agent.Handle(context.Background(), validAgentInput())
	if err == nil {
		t.Fatalf("expected missing trace dependency to fail")
	}
	var pipelineErr PipelineDependencyError
	if !errors.As(err, &pipelineErr) || pipelineErr.Dependency != "trace" {
		t.Fatalf("error = %T %[1]v, want trace PipelineDependencyError", err)
	}
	if runtime.finished {
		t.Fatalf("runtime must not run without trace recorder")
	}
}

func TestBaseAgentPipelineRequiresNotifierBeforeRuntime(t *testing.T) {
	runtime := &observingRuntime{response: RuntimeResponse{Status: RuntimeStatusCompleted}}
	agent := NewBaseAgent(BaseAgentConfig{
		Identity: AgentIdentity{AgentID: "agent-1", AgentProfileID: "profile-1", InstanceRef: AgentInstanceRef{ID: "instance-1"}},
		Profile:  AgentProfile{ID: "profile-1", RuntimeSpec: RuntimeSpec{Kind: RuntimeKindCodex}},
		Runtime:  runtime,
		Delivery: &capturingDelivery{},
		Trace:    &recordingTraceRecorder{ref: "store://trace/run-1"},
	})

	_, err := agent.Handle(context.Background(), validAgentInput())
	if err == nil {
		t.Fatalf("expected missing notifier dependency to fail")
	}
	var pipelineErr PipelineDependencyError
	if !errors.As(err, &pipelineErr) || pipelineErr.Dependency != "notifier" {
		t.Fatalf("error = %T %[1]v, want notifier PipelineDependencyError", err)
	}
	if runtime.finished {
		t.Fatalf("runtime must not run without notifier")
	}
}

func TestBaseAgentPipelineEmitsFailureSignalForRuntimeError(t *testing.T) {
	recorder := &recordingSignalSink{}
	agent := NewBaseAgent(BaseAgentConfig{
		Identity: AgentIdentity{AgentID: "agent-1", AgentProfileID: "profile-1", InstanceRef: AgentInstanceRef{ID: "instance-1"}},
		Profile:  AgentProfile{ID: "profile-1", RuntimeSpec: RuntimeSpec{Kind: RuntimeKindCodex}},
		Runtime:  fakePipelineRuntime{err: errors.New("runtime crashed")},
		Signals:  recorder,
		Trace:    &recordingTraceRecorder{ref: "store://trace/run-1"},
		Notifier: &recordingNotifier{},
	})

	_, err := agent.Handle(context.Background(), validAgentInput())
	if err == nil {
		t.Fatalf("expected runtime error")
	}
	if got := recorder.Names(); len(got) == 0 || got[len(got)-1] != SignalAgentFailed {
		t.Fatalf("failure signals = %#v", got)
	}
}
