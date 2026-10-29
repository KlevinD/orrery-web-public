package pi

import (
	"testing"

	agentcore "orrery_v2/internal/agent/core"
)

func TestNativePiWaitsForSettledAcrossRetriesAndFollowups(t *testing.T) {
	for _, previousStop := range []string{"stop", "error"} {
		t.Run(previousStop, func(t *testing.T) {
			var mapper piNativeMapper
			mapper.reset(1)
			for _, event := range []streamEvent{
				{Type: "agent_start"},
				{Type: "message_end", Message: piMessage{Role: "assistant", Content: "intermediate", StopReason: previousStop, ErrorMessage: "temporary failure"}},
				{Type: "agent_end", WillRetry: false},
				{Type: "agent_start"},
				{Type: "message_end", Message: piMessage{Role: "assistant", Content: "recovered final", StopReason: "stop"}},
				{Type: "agent_end", WillRetry: false},
			} {
				events, terminal, err := mapper.mapEvent(event, 1, false)
				if err != nil || terminal {
					t.Fatalf("turn completed before settlement on %s: terminal=%v, err=%v", event.Type, terminal, err)
				}
				for _, mapped := range events {
					if mapped.Kind == agentcore.EventTurnResult {
						t.Fatalf("premature turn result on %s", event.Type)
					}
				}
			}
			events, terminal, err := mapper.mapEvent(streamEvent{Type: "agent_settled"}, 1, false)
			if err != nil || !terminal {
				t.Fatalf("settlement did not complete turn: terminal=%v, err=%v", terminal, err)
			}
			var results int
			for _, mapped := range events {
				if mapped.Kind == agentcore.EventTurnResult {
					results++
					if mapped.TurnResult.IsError || mapped.TurnResult.ResultText != "recovered final" || mapped.TurnResult.Outcome.Kind != agentcore.TurnOutcomeEndTurn {
						t.Fatalf("settled result = %#v", mapped.TurnResult)
					}
				}
			}
			if results != 1 {
				t.Fatalf("turn result count = %d, want one", results)
			}
		})
	}
}

func TestNativePiSettledAbortCancelsTurn(t *testing.T) {
	var mapper piNativeMapper
	mapper.reset(1)
	events, terminal, err := mapper.mapEvent(streamEvent{Type: "agent_settled", Aborted: true}, 1, false)
	if err != nil || !terminal || len(events) != 1 || !events[0].TurnResult.IsError || events[0].TurnResult.Outcome.Kind != agentcore.TurnOutcomeCancelled {
		t.Fatalf("aborted settlement = %#v, terminal=%v, err=%v", events, terminal, err)
	}
}
