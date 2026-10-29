package session

import (
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
)

func TestResponseProjectionPreservesReasoningKind(t *testing.T) {
	projection := NewResponseProjection(agentcore.RuntimeRequest{RunID: "run-1"}, agentcore.RuntimeKindCodex, time.Now())
	for sequence, kind := range []agentcore.ReasoningContentKind{agentcore.ReasoningContentRaw, agentcore.ReasoningContentSummary} {
		event := agentcore.SessionEnvelope{
			SessionID: "session-1",
			Sequence:  uint64(sequence + 1),
			Event: agentcore.SessionEvent{Kind: agentcore.EventThoughtDelta, ThoughtDelta: &agentcore.ThoughtDeltaEvent{
				ItemID:        "thought-1",
				Text:          string(kind),
				ReasoningKind: kind,
			}},
		}
		if err := projection.Fold(event); err != nil {
			t.Fatal(err)
		}
		messages := projection.LastFoldMessages()
		if len(messages) != 1 || messages[0].ReasoningKind != kind {
			t.Fatalf("projected messages = %+v, want reasoning kind %q", messages, kind)
		}
	}
}
