package openclaw

import (
	"testing"

	agentcore "orrery_v2/internal/agent/core"
	runtimecore "orrery_v2/internal/agent/runtime/core"
)

func TestOpenClawMarksThinkingMessageRaw(t *testing.T) {
	event, ok := openClawMessageEvent(1, runtimecore.RuntimeMessage{Type: runtimecore.MessageTypeThinking, Content: "private reasoning"}, nil)
	if !ok || event.ThoughtDelta == nil {
		t.Fatalf("OpenClaw thought event = %+v, ok = %v", event, ok)
	}
	if event.ThoughtDelta.ReasoningKind != agentcore.ReasoningContentRaw {
		t.Fatalf("OpenClaw thought kind = %q", event.ThoughtDelta.ReasoningKind)
	}
}
