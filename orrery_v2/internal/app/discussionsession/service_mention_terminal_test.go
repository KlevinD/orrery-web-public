package discussionsession

import (
	"context"
	"testing"
	"time"
)

func TestDirectRuntimeTimeoutPersistsTerminalEvidence(t *testing.T) {
	ctx := context.Background()
	repo := newMemoryRepository(t)
	traceRef := "/uploads/discussion-conversations/conv-1/runs/run-direct/trace-v2/runtime-trace.json"
	failure := NewRuntimeExecutionError("codex", "context deadline exceeded")
	responder := &streamingDiscussionAgentMentionResponder{events: []DiscussionAgentStreamEvent{
		{Kind: DiscussionAgentStreamRuntimeError, Error: failure, Response: DiscussionAgentResponse{
			Status: "failed", RuntimeKind: "codex", SessionID: "session-native", TraceRef: traceRef,
			DurationMs:       int64((20 * time.Minute).Milliseconds()),
			ProviderMetadata: map[string]string{"terminal_error_code": "runtime_timeout"},
		}},
	}}
	service := newTestService(ServiceOptions{
		Repository: repo, MentionResponder: responder,
		DiscussionAgent: &capturingDiscussionAgentRunner{agentID: "lead-agent"},
		Clock:           fixedClock{now: testTime},
		IDGenerator:     fixedIDGen("conv-1", "msg-user", "run-direct", "event-failure"),
	})
	viewer := ViewerRef{ViewerID: "local-user", ViewerKind: ViewerKindUser}
	stream, err := service.SendMessageStream(ctx, viewer, SendMessageCommand{
		Content: "Inspect this fault", DirectAgentID: "staffing-agent", IdempotencyKey: "terminal-timeout",
	})
	if err != nil {
		t.Fatal(err)
	}
	for range stream {
	}
	run, err := repo.GetRun(ctx, "run-direct")
	if err != nil {
		t.Fatal(err)
	}
	if run.Status != "failed" || run.CompletedAt.IsZero() || run.TraceRef != traceRef ||
		run.TraceVersion != 2 || run.TraceStatus != "hot" || run.RuntimeKind != "codex" ||
		run.SessionID != "session-native" || run.DurationMs != 1200000 ||
		run.ProviderMetadata["terminal_error_code"] != "runtime_timeout" {
		t.Fatalf("terminal evidence was lost: %+v", run)
	}
	turn, err := repo.GetTurn(ctx, run.TurnID)
	if err != nil || turn.Status != "failed" || len(turn.RunIDs) != 1 || turn.RunIDs[0] != run.RunID {
		t.Fatalf("terminal turn = %+v, err=%v", turn, err)
	}
	view, err := repo.GetConversation(ctx, run.ConversationID)
	if err != nil || view.ActiveTurnID != "" || len(view.Messages) != 1 {
		t.Fatalf("timeout must close the turn without inventing a report: %+v, err=%v", view, err)
	}
}
