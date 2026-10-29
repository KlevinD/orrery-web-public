package core

import (
	"context"
	"reflect"
	"testing"
	"time"
)

func TestBaseAgentPipelineRunsRuntimeNormalizesCommitsAndSignals(t *testing.T) {
	recorder := &recordingSignalSink{}
	delivery := &capturingDelivery{}
	trace := &recordingTraceRecorder{ref: "store://trace/run-1"}
	notifier := &recordingNotifier{}
	normalizer := &capturingRuntimeBindingsNormalizer{}
	agent := NewBaseAgent(BaseAgentConfig{
		Identity: AgentIdentity{AgentID: "agent-1", AgentProfileID: "profile-1", InstanceRef: AgentInstanceRef{ID: "instance-1"}},
		Profile:  AgentProfile{ID: "profile-1", RuntimeSpec: RuntimeSpec{Kind: RuntimeKindCodex}},
		Bindings: AgentBindings{Tools: []ToolBinding{{ToolID: "knowledge_search", Alias: "knowledge_search", GrantedPermissions: []string{"knowledge.read"}}}},
		Runtime: fakePipelineRuntime{response: RuntimeResponse{
			Status: RuntimeStatusCompleted,
			Messages: []RuntimeMessage{{
				Type:    RuntimeMessageText,
				Content: "Implemented the feature.",
			}},
			Artifacts: []ArtifactRef{{ID: "artifact-1", Kind: ArtifactKindCode, URI: "file://diff.patch"}},
			ToolCalls: []RuntimeToolCall{{
				ID:     "evidence-1",
				Name:   "go test",
				Output: "ok",
			}},
		}},
		Signals:    recorder,
		Delivery:   delivery,
		Clock:      fixedClock{t: time.Date(2026, 6, 1, 1, 2, 3, 0, time.UTC)},
		Normalizer: normalizer,
		Trace:      trace,
		Notifier:   notifier,
	})

	input := validAgentInput()
	input.TenantID = "tenant-a"
	result, err := agent.Handle(context.Background(), input)
	if err != nil {
		t.Fatalf("handle task: %v", err)
	}
	if result.Summary.Text != "Implemented the feature." {
		t.Fatalf("summary = %q", result.Summary.Text)
	}
	if len(result.Deliverables) != 1 || result.Deliverables[0].ExpectedDeliverableID != "deliverable-1" {
		t.Fatalf("deliverables = %#v", result.Deliverables)
	}
	if len(result.Evidence) != 1 || result.Evidence[0].EvidenceRequirementID != "evidence-1" {
		t.Fatalf("evidence = %#v", result.Evidence)
	}
	var knowledgeBinding RuntimeToolBinding
	for _, binding := range normalizer.bindings {
		if binding.ToolID == "knowledge_search" {
			knowledgeBinding = binding
			break
		}
	}
	if knowledgeBinding.ToolID == "" || len(knowledgeBinding.GrantedPermissions) != 1 || knowledgeBinding.GrantedPermissions[0] != "knowledge.read" {
		t.Fatalf("normalizer runtime tool bindings = %#v", normalizer.bindings)
	}
	if delivery.committed == nil {
		t.Fatalf("expected result to be committed")
	}
	if delivery.options.IdempotencyKey != "run-1" {
		t.Fatalf("commit idempotency key = %q, want run-1", delivery.options.IdempotencyKey)
	}
	if len(delivery.options.ContextSourceRefs) != len(validAgentInput().ContextPack.SourceRefs) {
		t.Fatalf("commit context source refs = %+v", delivery.options.ContextSourceRefs)
	}
	if trace.calls != 1 {
		t.Fatalf("trace calls = %d, want 1", trace.calls)
	}
	if notifier.calls != 1 {
		t.Fatalf("notifier calls = %d, want 1", notifier.calls)
	}

	names := recorder.Names()
	wantPrefix := []SignalName{
		SignalBeforeTaskReceived,
		SignalAfterTaskReceived,
		SignalBeforeTaskAccepted,
		SignalAfterTaskAccepted,
		SignalBeforeContextBuild,
		SignalAfterContextBuild,
		SignalBeforeRuntimeStart,
		SignalRuntimeStarted,
		SignalBeforeRuntimeFinish,
		SignalAfterRuntimeFinish,
		SignalBeforeResultNormalize,
		SignalAfterResultNormalize,
		SignalBeforeResultValidate,
		SignalAfterResultValidate,
		SignalBeforeResultCommit,
		SignalAfterResultCommit,
		SignalBeforeAgentReply,
		SignalAfterAgentReply,
	}
	if len(names) < len(wantPrefix) {
		t.Fatalf("signals = %#v", names)
	}
	for i, want := range wantPrefix {
		if names[i] != want {
			t.Fatalf("signal[%d] = %s, want %s; all=%#v", i, names[i], want, names)
		}
	}
	for _, signal := range recorder.signals {
		if signal.AgentProfileID != "profile-1" {
			t.Fatalf("signal %s agent profile id = %s, want profile-1", signal.Name, signal.AgentProfileID)
		}
		if signal.TenantID != "tenant-a" {
			t.Fatalf("signal %s tenant id = %s, want tenant-a", signal.Name, signal.TenantID)
		}
	}
}

type capturingRuntimeBindingsNormalizer struct {
	bindings []RuntimeToolBinding
}

func (n *capturingRuntimeBindingsNormalizer) Normalize(input AgentInput, response RuntimeResponse, now time.Time) (AgentResult, error) {
	n.bindings = cloneRuntimeToolBindings(input.RuntimeToolBindings)
	return (DefaultResultNormalizer{}).Normalize(input, response, now)
}

func TestBaseAgentImplementsRequiredAgentBaseClassContract(t *testing.T) {
	agent := NewBaseAgent(BaseAgentConfig{
		Identity: AgentIdentity{AgentID: "agent-1", AgentProfileID: "profile-1", InstanceRef: AgentInstanceRef{ID: "agent-1"}},
		Profile:  AgentProfile{ID: "profile-1", RuntimeSpec: RuntimeSpec{Kind: RuntimeKindOpenClaw}},
	})

	var managed Agent = agent

	if managed.Base() != agent {
		t.Fatalf("Base() = %p, want %p", managed.Base(), agent)
	}
}

func TestAgentInterfaceDoesNotExposeOverridableHandle(t *testing.T) {
	agentType := reflect.TypeOf((*Agent)(nil)).Elem()

	if _, ok := agentType.MethodByName("Handle"); ok {
		t.Fatalf("Agent interface must not expose Handle; callers must use Base().Handle")
	}
	if _, ok := agentType.MethodByName("Base"); !ok {
		t.Fatalf("Agent interface must expose Base()")
	}
}
