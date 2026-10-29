package main

import (
	"context"
	"testing"

	"orrery_v2/internal/app/discussionsession"
	gwprotocol "orrery_v2/internal/gateway/protocol"
)

func TestProductionMessageGatewayRunnerPublishesReadableFinalMessageForFeishu(t *testing.T) {
	discussions := &fakeProductionGatewayDiscussionService{events: []discussionsession.StreamEventDTO{
		{Kind: discussionsession.StreamEventRunStarted, Run: &discussionsession.RunDTO{RunID: "feishu-progress-run"}},
		{Kind: discussionsession.StreamEventTokenDelta, Delta: "internal progress", RunID: "feishu-progress-run"},
		{Kind: discussionsession.StreamEventMessageCompleted, Message: &discussionsession.MessageDTO{
			Role: "assistant", Content: "final answer\n```orrery-agent-result\n{\"job_id\":\"secret\"}\n```", RunID: "feishu-progress-run",
		}, RunID: "feishu-progress-run"},
		{Kind: discussionsession.StreamEventRunCompleted, Run: &discussionsession.RunDTO{RunID: "feishu-progress-run", Status: "completed"}},
	}}
	runner := newProductionMessageGatewayRunner(discussions)
	runner.SetBindingStore(testProductionGatewayBindingStore(t))
	runner.SetAgentDirectory(systemGatewayTestDirectory("human-a", "agent-a"))
	identity := createProductionGatewayBinding(t, runner.bindings, "feishu", "feishu:app-a", "fs-a", "open-a", "human-a", "agent-a")

	events, err := runner.RunTurn(context.Background(), gwprotocol.SubmitTurnInput{
		SessionKey: "feishu:chat-progress", ChannelKey: gwprotocol.ChannelFeishu, Message: "ping", Identity: &identity,
	})
	if err != nil {
		t.Fatalf("RunTurn failed: %v", err)
	}
	got := collectGatewayEvents(events)
	if len(got) != 3 {
		t.Fatalf("events = %+v", got)
	}
	if got[1].Type != gwprotocol.EventAssistantTextDelta || got[1].Text != "final answer" {
		t.Fatalf("final assistant event = %+v", got[1])
	}
	if got[2].Type != gwprotocol.EventTurnCompleted || got[2].FinishReason != "completed" {
		t.Fatalf("completed event = %+v", got[2])
	}
}
