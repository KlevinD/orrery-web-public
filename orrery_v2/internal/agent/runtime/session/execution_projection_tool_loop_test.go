package session

import (
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
)

func TestCompletedCodexTurnOwnsEmptyAndFailedToolResults(t *testing.T) {
	for _, status := range []agentcore.RuntimeStatus{agentcore.RuntimeStatusCompleted, agentcore.RuntimeStatusFailed, agentcore.RuntimeStatusBlocked} {
		t.Run(string(status), func(t *testing.T) {
			p := NewResponseProjection(agentcore.RuntimeRequest{RunID: "run"}, agentcore.RuntimeKindCodex, time.Now())
			for _, event := range []agentcore.SessionEvent{
				{Kind: agentcore.EventToolCall, ToolCall: &agentcore.ToolCallEvent{ToolUseID: "patch", Name: "fileChange", Subagent: agentcore.SubagentInline}},
				{Kind: agentcore.EventToolResult, ToolResult: &agentcore.ToolResultEvent{ToolUseID: "patch", IsError: true}},
				{Kind: agentcore.EventMessageDelta, MessageDelta: &agentcore.MessageDeltaEvent{ItemID: "final", Text: `{"cases":[{"status":"needs_attention"}]}`}},
			} {
				if err := p.Fold(agentcore.SessionEnvelope{SessionID: "session", Event: event}); err != nil {
					t.Fatal(err)
				}
			}
			p.finish(time.Now(), status, "", "", false)
			response := p.Response(time.Now())
			if got := response.ProviderMetadata["tool_loop_completed"] == "true"; got != (status == agentcore.RuntimeStatusCompleted) {
				t.Fatalf("status %s loop completed=%v", status, got)
			}
			if len(response.Messages) != 3 || response.Messages[1].Status != "failed" {
				t.Fatalf("tool failure was lost: %#v", response.Messages)
			}
		})
	}
}
