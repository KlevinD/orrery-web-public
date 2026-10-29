package discussionsession

import (
	"testing"

	agentcore "orrery_v2/internal/agent/core"
)

func TestRuntimeMessageStreamEventsOnlyPublishesReasoningSummary(t *testing.T) {
	for _, kind := range []agentcore.ReasoningContentKind{"", agentcore.ReasoningContentRaw} {
		events := runtimeMessageStreamEvents(agentcore.RuntimeMessage{Type: agentcore.RuntimeMessageThinking, Content: "private", ReasoningKind: kind})
		if len(events) != 0 {
			t.Fatalf("reasoning kind %q produced public events: %+v", kind, events)
		}
	}

	events := runtimeMessageStreamEvents(agentcore.RuntimeMessage{Type: agentcore.RuntimeMessageThinking, Content: "summary", ReasoningKind: agentcore.ReasoningContentSummary})
	if len(events) != 1 || events[0].Kind != DiscussionAgentStreamThinkingDelta || events[0].Delta != "summary" || events[0].ReasoningKind != agentcore.ReasoningContentSummary {
		t.Fatalf("summary events = %+v", events)
	}
}
