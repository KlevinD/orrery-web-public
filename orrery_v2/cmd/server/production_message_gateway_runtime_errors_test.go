package main

import (
	"context"
	"strings"
	"testing"

	"orrery_v2/internal/app/discussionsession"
	"orrery_v2/internal/gateway/channel"
	gwprotocol "orrery_v2/internal/gateway/protocol"
)

func TestProductionMessageGatewayRunnerDoesNotRetryInvalidMissionRequest(t *testing.T) {
	discussions := &fakeProductionGatewayDiscussionService{events: []discussionsession.StreamEventDTO{{
		Kind: discussionsession.StreamEventError, Error: "invalid mission control request: internal task details",
		ErrorCode: discussionsession.StreamErrorCodeInvalidRequest, Recoverable: true,
	}}}
	runner := newProductionMessageGatewayRunner(discussions)
	runner.SetBindingStore(testProductionGatewayBindingStore(t))
	runner.SetAgentDirectory(systemGatewayTestDirectory("human-a", "agent-a"))
	identity := createProductionGatewayBinding(t, runner.bindings, "feishu", "feishu:app-a", "fs-a", "open-a", "human-a", "agent-a")
	events, err := runner.RunTurn(context.Background(), gwprotocol.SubmitTurnInput{
		SessionKey: "feishu:chat-a", ChannelKey: gwprotocol.ChannelFeishu, Message: "ping", Identity: &identity,
	})
	if err != nil {
		t.Fatalf("RunTurn: %v", err)
	}
	got := collectGatewayEvents(events)
	if len(got) != 1 || got[0].Code != gwprotocol.ErrorCodeGatewayRequestInvalid || got[0].Recoverable {
		t.Fatalf("events = %+v", got)
	}
	if strings.Contains(got[0].Message, "internal task details") || !channel.IsPermanentInboundError(channel.InboundProcessingError(got)) {
		t.Fatalf("invalid Mission request was exposed or marked retryable: %+v", got)
	}
}
