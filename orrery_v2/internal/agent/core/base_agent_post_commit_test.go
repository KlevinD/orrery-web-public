package core

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestBaseAgentReturnsCommittedResultWithoutReplayingNotifierFailure(t *testing.T) {
	notifyErr := errors.New("conversation store unavailable")
	delivery := &capturingDelivery{}
	agent := NewBaseAgent(BaseAgentConfig{
		Identity: AgentIdentity{AgentID: "agent-1", AgentProfileID: "profile-1", InstanceRef: AgentInstanceRef{ID: "instance-1"}},
		Profile:  AgentProfile{ID: "profile-1", RuntimeSpec: RuntimeSpec{Kind: RuntimeKindCodex}},
		Runtime: fakePipelineRuntime{response: RuntimeResponse{
			Status:    RuntimeStatusCompleted,
			Messages:  []RuntimeMessage{{Type: RuntimeMessageText, Content: "completed"}},
			Artifacts: []ArtifactRef{{ID: "artifact-1", Kind: ArtifactKindCode, URI: "artifact://artifact-1"}},
			ToolCalls: []RuntimeToolCall{{ID: "evidence-1", Name: "test", Output: "ok"}},
		}},
		Delivery: delivery,
		Clock:    fixedClock{t: time.Date(2026, 7, 26, 0, 0, 0, 0, time.UTC)},
		Trace:    &recordingTraceRecorder{ref: "artifact://trace-1"},
		Notifier: failingPipelineNotifier{err: notifyErr},
	})

	result, err := agent.Handle(context.Background(), validAgentInput())
	if result.ID == "" || delivery.committed == nil || delivery.committed.ID != result.ID {
		t.Fatalf("committed result was not returned: result=%+v committed=%+v", result, delivery.committed)
	}
	var postCommitErr PostCommitPipelineError
	if !errors.As(err, &postCommitErr) || postCommitErr.Stage != "notify" || !errors.Is(err, notifyErr) {
		t.Fatalf("error = %T %[1]v, want notify PostCommitPipelineError", err)
	}
}

type failingPipelineNotifier struct{ err error }

func (n failingPipelineNotifier) Notify(context.Context, AgentInput, AgentResult) error { return n.err }
