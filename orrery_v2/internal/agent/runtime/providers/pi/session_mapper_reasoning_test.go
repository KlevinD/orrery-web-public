package pi

import (
	"testing"

	agentcore "orrery_v2/internal/agent/core"
)

func TestPiMapperMarksThinkingDeltaRaw(t *testing.T) {
	mapper := &piNativeMapper{}
	mapper.reset(1)
	events, _, err := mapper.mapEvent(streamEvent{
		Type:                  "message_update",
		AssistantMessageEvent: assistantMessageEvent{Type: "thinking_delta", Delta: "private reasoning"},
	}, 1, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		if event.Kind != agentcore.EventThoughtDelta {
			continue
		}
		if event.ThoughtDelta.ReasoningKind != agentcore.ReasoningContentRaw {
			t.Fatalf("PI thought kind = %q", event.ThoughtDelta.ReasoningKind)
		}
		return
	}
	t.Fatalf("PI events did not contain a thought delta: %+v", events)
}
