package main

import (
	"context"
	"orrery_v2/internal/app/discussionsession"
	gwconfig "orrery_v2/internal/gateway/config"
	gwprotocol "orrery_v2/internal/gateway/protocol"
	"strings"
	"testing"
)

func TestProductionMessageGatewayRunnerPublishesCompletedMessageAfterProgressOnly(t *testing.T) {
	discussions := &fakeProductionGatewayDiscussionService{events: []discussionsession.StreamEventDTO{
		{Kind: discussionsession.StreamEventRunStarted, Run: &discussionsession.RunDTO{RunID: "progress-run"}},
		{Kind: discussionsession.StreamEventTokenDelta, Delta: "我先读取上下文并核对运行状态。", RunID: "progress-run"},
		{Kind: discussionsession.StreamEventMessageCompleted, Message: &discussionsession.MessageDTO{
			Role: "assistant", Content: "任务已完成，结果已写入通知中心。", RunID: "progress-run",
		}, RunID: "progress-run"},
		{Kind: discussionsession.StreamEventRunCompleted, Run: &discussionsession.RunDTO{RunID: "progress-run", Status: "completed"}},
	}}
	cfg := gwconfig.DefaultConfig()
	cfg.Adapters.Weixin.Extra = map[string]any{
		"agentBindings":      map[string]any{"wx-user": "staffing-agent"},
		"weixinUserBindings": map[string]any{"wx-user": "human-alice"},
	}
	runner := newProductionMessageGatewayRunner(discussions, cfg)
	runner.SetAgentDirectory(systemGatewayTestDirectory("human-alice", "staffing-agent"))

	events, err := runner.RunTurn(context.Background(), gwprotocol.SubmitTurnInput{
		SessionKey: "weixin:chat-progress",
		ChannelKey: gwprotocol.ChannelWeixin,
		Message:    "请处理任务",
		RunID:      "gateway-progress-run",
		Telemetry: map[string]string{
			"weixin_sender_id":       "wx-user",
			"weixin_sender_verified": "true",
		},
	})
	if err != nil {
		t.Fatalf("RunTurn failed: %v", err)
	}
	got := collectGatewayEvents(events)
	if len(got) != 3 {
		t.Fatalf("events = %+v", got)
	}
	if got[0].Type != gwprotocol.EventTurnStarted {
		t.Fatalf("started event = %+v", got[0])
	}
	if got[1].Type != gwprotocol.EventAssistantTextDelta || got[1].Text != "任务已完成，结果已写入通知中心。" {
		t.Fatalf("assistant event = %+v", got[1])
	}
	if strings.Contains(got[1].Text, "我先") || strings.Contains(got[1].Text, "上下文") {
		t.Fatalf("progress text leaked into assistant event = %+v", got[1])
	}
	if got[2].Type != gwprotocol.EventTurnCompleted || got[2].FinishReason != "completed" {
		t.Fatalf("completed event = %+v", got[2])
	}
}
