package kernelcompat

import (
	"context"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/agent/runtime/session"
)

func TestSessionFinalizesAuthoritativeAnswerAfterStreamingAndTools(t *testing.T) {
	for _, streamAnswer := range []bool{false, true} {
		name := "terminal-only"
		if streamAnswer {
			name = "streamed"
		}
		t.Run(name, func(t *testing.T) {
			const answer = "Agent chose this final report."
			runner := &testRunner{run: func(_ context.Context, _ agentcore.SendCommand, emit Emit) (agentcore.TurnResultEvent, error) {
				events := []agentcore.SessionEvent{
					{Kind: agentcore.EventMessageDelta, MessageDelta: &agentcore.MessageDeltaEvent{ItemID: "progress", Text: "Inspecting sources; this is not the report."}},
					{Kind: agentcore.EventToolCall, ToolCall: &agentcore.ToolCallEvent{ToolUseID: "source", Name: "knowledge_search"}},
					{Kind: agentcore.EventToolResult, ToolResult: &agentcore.ToolResultEvent{ToolUseID: "source", Content: []agentcore.ToolResultContent{{Kind: agentcore.ToolResultText, Text: "verified"}}}},
				}
				if streamAnswer {
					events = append(events,
						agentcore.SessionEvent{Kind: agentcore.EventMessageDelta, MessageDelta: &agentcore.MessageDeltaEvent{ItemID: "answer", Text: "Agent chose "}},
						agentcore.SessionEvent{Kind: agentcore.EventMessageDelta, MessageDelta: &agentcore.MessageDeltaEvent{ItemID: "answer", Text: "this final report."}})
				}
				for _, event := range events {
					if err := emit(event); err != nil {
						return agentcore.TurnResultEvent{}, err
					}
				}
				return completed(answer), nil
			}}
			_, backend := openTestSession(t, runner, nil)
			if _, err := backend.Dispatch(context.Background(), send("report", "inspect")); err != nil {
				t.Fatal(err)
			}
			events := collectTurn(t, backend)
			projection := session.NewResponseProjection(agentcore.RuntimeRequest{RunID: "report"}, agentcore.RuntimeKindManaged, time.Now())
			finalized := 0
			for index, envelope := range events {
				if envelope.Event.Kind == agentcore.EventMessageFinalized {
					finalized++
					message := envelope.Event.MessageFinalized.Message
					if index != len(events)-2 || message.Content != answer || message.ItemID == "progress" || (streamAnswer && message.ItemID != "answer") {
						t.Fatalf("wrong final content, identity or ordering: %+v", envelope)
					}
				}
				if err := projection.Fold(envelope); err != nil {
					t.Fatal(err)
				}
			}
			response := projection.Response(time.Now())
			finals, answerCopies := 0, 0
			for _, message := range response.Messages {
				if message.Type == agentcore.RuntimeMessageText && message.Status == agentcore.RuntimeMessageStatusFinal {
					finals++
					if message.Content != answer {
						t.Fatalf("intermediate output became final: %+v", message)
					}
				}
				if message.Content == answer {
					answerCopies++
				}
				if message.CallID == "answer" && message.Status != agentcore.RuntimeMessageStatusFinal {
					t.Fatal("stream fragments survived final consolidation")
				}
			}
			if finalized != 1 || finals != 1 || answerCopies != 1 || response.Status != agentcore.RuntimeStatusCompleted {
				t.Fatalf("missing or duplicate final: %+v", response)
			}
		})
	}
}

func TestSessionNeverFinalizesInterruptedOrUnverifiedOutput(t *testing.T) {
	for _, outcome := range []agentcore.TurnOutcomeKind{agentcore.TurnOutcomeFailed, agentcore.TurnOutcomeCancelled, agentcore.TurnOutcomeCompleted} {
		t.Run(string(outcome), func(t *testing.T) {
			runner := &testRunner{run: func(_ context.Context, _ agentcore.SendCommand, emit Emit) (agentcore.TurnResultEvent, error) {
				if err := emit(agentcore.SessionEvent{Kind: agentcore.EventMessageDelta, MessageDelta: &agentcore.MessageDeltaEvent{ItemID: "partial", Text: "Unfinished report"}}); err != nil {
					return agentcore.TurnResultEvent{}, err
				}
				if outcome == agentcore.TurnOutcomeCompleted {
					// A claimed success with a pending tool is invalid.
					if err := emit(agentcore.SessionEvent{Kind: agentcore.EventToolCall, ToolCall: &agentcore.ToolCallEvent{ToolUseID: "pending", Name: "knowledge_search"}}); err != nil {
						return agentcore.TurnResultEvent{}, err
					}
				}
				result := completed("unverified terminal text")
				result.Outcome.Kind = outcome
				if outcome != agentcore.TurnOutcomeCompleted {
					result.Outcome.StopReason = nil
				}
				result.IsError = outcome == agentcore.TurnOutcomeFailed
				return result, nil
			}}
			_, backend := openTestSession(t, runner, nil)
			if _, err := backend.Dispatch(context.Background(), send("interrupted", "inspect")); err != nil {
				t.Fatal(err)
			}
			for _, envelope := range collectTurn(t, backend) {
				if envelope.Event.Kind == agentcore.EventMessageFinalized {
					t.Fatalf("unverified output became a public final: %+v", envelope)
				}
			}
		})
	}
}
