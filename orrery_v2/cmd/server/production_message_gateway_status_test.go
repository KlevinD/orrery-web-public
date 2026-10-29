package main

import (
	"context"
	"strings"
	"testing"

	"orrery_v2/internal/gateway/channel"
	gwconfig "orrery_v2/internal/gateway/config"
)

type unhealthyProductionGatewayHandle struct{}

func (unhealthyProductionGatewayHandle) Stop(context.Context, string) error { return nil }
func (unhealthyProductionGatewayHandle) GatewayConnectionHealthy() bool     { return false }

type accountHealthProductionGatewayHandle struct {
	healthy map[string]bool
	sent    []channel.Delivery
}

type routedProductionGatewayHandle struct {
	sent []channel.Delivery
}

func (h *routedProductionGatewayHandle) Stop(context.Context, string) error { return nil }
func (h *routedProductionGatewayHandle) GatewayConnectionHealthy() bool     { return true }
func (h *routedProductionGatewayHandle) Send(_ context.Context, delivery channel.Delivery) (channel.DeliveryResult, error) {
	h.sent = append(h.sent, delivery)
	return channel.DeliveryResult{ExternalMessageID: "receipt"}, nil
}

func (h *accountHealthProductionGatewayHandle) Stop(context.Context, string) error { return nil }
func (h *accountHealthProductionGatewayHandle) GatewayConnectionHealthy() bool     { return false }
func (h *accountHealthProductionGatewayHandle) GatewayAccountConnectionHealthy(accountID string) bool {
	return h.healthy[strings.TrimSpace(accountID)]
}
func (h *accountHealthProductionGatewayHandle) Send(_ context.Context, delivery channel.Delivery) (channel.DeliveryResult, error) {
	h.sent = append(h.sent, delivery)
	return channel.DeliveryResult{ExternalMessageID: "receipt"}, nil
}

func TestProductionMessageGatewayManagerReportsConnectionHealth(t *testing.T) {
	cfg := gwconfig.DefaultConfig()
	cfg.Adapters.Feishu.Enabled = true
	cfg.Adapters.Feishu.AppID = "app-feishu"
	cfg.Adapters.Feishu.AppSecret = "secret"
	cfg.Adapters.DingTalk.Enabled = true
	cfg.Adapters.DingTalk.APIKey = "app-dingtalk"
	cfg.Adapters.DingTalk.Token = "secret"
	manager := &productionMessageGatewayManager{
		config:     cfg,
		started:    []channel.StartedAdapter{{Key: "feishu", Handle: &testProductionGatewayHandle{}}},
		lastErrors: map[string]string{"dingtalk": "stream connection failed"},
	}
	statuses := manager.GatewayAccountStatuses()
	byChannel := map[string]channel.AccountStatus{}
	for _, status := range statuses {
		byChannel[status.ChannelKey] = status
	}
	if got := byChannel["feishu"]; got.Status != "connected" || got.AccountID != "feishu:app-feishu" {
		t.Fatalf("Feishu status = %+v", got)
	}
	if got := byChannel["dingtalk"]; got.Status != "degraded" || got.AccountID != "dingtalk:app-dingtalk" || got.LastError == "" {
		t.Fatalf("DingTalk status = %+v", got)
	}
}

func TestProductionMessageGatewayManagerReportsLostRuntimeConnection(t *testing.T) {
	cfg := gwconfig.DefaultConfig()
	cfg.Adapters.Feishu.Enabled = true
	cfg.Adapters.Feishu.AppID = "app-feishu"
	manager := &productionMessageGatewayManager{
		config: cfg, started: []channel.StartedAdapter{{Key: "feishu", Handle: unhealthyProductionGatewayHandle{}}}, lastErrors: map[string]string{},
	}
	statuses := manager.GatewayAccountStatuses()
	if len(statuses) != 1 || statuses[0].Status != "degraded" || statuses[0].LastError != "connection_lost" {
		t.Fatalf("statuses = %+v", statuses)
	}
}

func TestProductionMessageGatewayManagerReportsWeixinProvisioningState(t *testing.T) {
	cfg := gwconfig.DefaultConfig()
	cfg.Adapters.Weixin.Enabled = true
	manager := &productionMessageGatewayManager{config: cfg, lastErrors: map[string]string{}}
	statuses := manager.GatewayAccountStatuses()
	if len(statuses) != 1 || statuses[0].ChannelKey != "weixin" || statuses[0].Status != "awaiting_binding" {
		t.Fatalf("statuses = %+v", statuses)
	}
}

func TestProductionMessageGatewayManagerUsesPerAccountWeixinHealth(t *testing.T) {
	cfg := gwconfig.DefaultConfig()
	cfg.Adapters.Weixin.Enabled = true
	cfg.Adapters.Weixin.Extra = map[string]any{"weixinAccounts": map[string]any{
		gwconfig.WeixinBindingKey("tenant-a", "user-a"): map[string]any{"botId": "bot-a", "token": "token-a"},
		gwconfig.WeixinBindingKey("tenant-a", "user-b"): map[string]any{"botId": "bot-b", "token": "token-b"},
	}}
	handle := &accountHealthProductionGatewayHandle{healthy: map[string]bool{"bot-a": true, "bot-b": false}}
	manager := &productionMessageGatewayManager{
		config: cfg, started: []channel.StartedAdapter{{Key: "weixin", Handle: handle}}, lastErrors: map[string]string{},
	}
	statuses := manager.GatewayAccountStatuses()
	byAccount := map[string]channel.AccountStatus{}
	for _, status := range statuses {
		byAccount[status.AccountID] = status
	}
	if byAccount["bot-a"].Status != "connected" || byAccount["bot-b"].Status != "degraded" {
		t.Fatalf("statuses = %+v", statuses)
	}
	if _, err := manager.Send(context.Background(), channel.Delivery{ChannelKey: "weixin", AccountID: "bot-a", RecipientID: "user-a"}); err != nil {
		t.Fatalf("healthy account send: %v", err)
	}
	if _, err := manager.Send(context.Background(), channel.Delivery{ChannelKey: "weixin", AccountID: "bot-b", RecipientID: "user-b"}); err == nil {
		t.Fatal("unhealthy account accepted outbound delivery")
	}
	if len(handle.sent) != 1 || handle.sent[0].AccountID != "bot-a" {
		t.Fatalf("sent = %+v", handle.sent)
	}
}

func TestProductionMessageGatewayManagerRoutesFeishuByAccount(t *testing.T) {
	mainHandle := &routedProductionGatewayHandle{}
	jordanHandle := &routedProductionGatewayHandle{}
	manager := &productionMessageGatewayManager{
		started: []channel.StartedAdapter{
			{Key: "feishu", AccountID: "main", Handle: mainHandle},
			{Key: "feishu", AccountID: "jordan-belfort", Handle: jordanHandle},
		},
	}
	result, err := manager.Send(context.Background(), channel.Delivery{
		ChannelKey: "feishu", AccountID: "jordan-belfort", RecipientID: "ou_test",
	})
	if err != nil {
		t.Fatalf("Send failed: %v", err)
	}
	if result.ExternalMessageID == "" || len(mainHandle.sent) != 0 || len(jordanHandle.sent) != 1 {
		t.Fatalf("main sent=%d jordan sent=%d result=%+v", len(mainHandle.sent), len(jordanHandle.sent), result)
	}
}
