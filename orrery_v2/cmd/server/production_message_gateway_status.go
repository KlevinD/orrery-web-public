package main

import (
	"sort"
	"strings"

	"orrery_v2/internal/gateway/channel"
	"orrery_v2/internal/gateway/channel/dingtalk"
	"orrery_v2/internal/gateway/channel/feishu"
	"orrery_v2/internal/gateway/channel/weixin"
)

func (m *productionMessageGatewayManager) GatewayAccountStatuses() []channel.AccountStatus {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	handles := map[string]channel.Handle{}
	for _, adapter := range m.started {
		if adapter.Handle != nil {
			handles[adapter.Key+"\x00"+strings.TrimSpace(adapter.AccountID)] = adapter.Handle
		}
	}
	statusFor := func(key string, accountID string, enabled bool) (string, string) {
		if !enabled {
			return "disabled", ""
		}
		handle := handles[key+"\x00"+strings.TrimSpace(accountID)]
		if handle == nil {
			handle = handles[key+"\x00"]
		}
		healthy := handle != nil
		if provider, ok := handle.(channel.AccountConnectionHealthProvider); ok && strings.TrimSpace(accountID) != "" {
			healthy = provider.GatewayAccountConnectionHealthy(accountID)
		} else if provider, ok := handle.(channel.ConnectionHealthProvider); ok {
			healthy = provider.GatewayConnectionHealthy()
		}
		if healthy {
			return "connected", ""
		}
		lastError := strings.TrimSpace(m.lastErrors[key])
		if lastError == "" && handle != nil {
			lastError = "connection_lost"
		}
		return "degraded", lastError
	}
	var statuses []channel.AccountStatus
	if cfg := m.config.Adapters.Feishu; len(cfg.Accounts) > 0 {
		accountIDs := make([]string, 0, len(cfg.Accounts))
		for accountID := range cfg.Accounts {
			accountIDs = append(accountIDs, accountID)
		}
		sort.Strings(accountIDs)
		for _, accountID := range accountIDs {
			account := cfg.Accounts[accountID]
			status, lastError := statusFor("feishu", accountID, cfg.Enabled && account.Enabled)
			statuses = append(statuses, channel.AccountStatus{ChannelKey: "feishu", AccountID: accountID, Enabled: cfg.Enabled && account.Enabled, Status: status, LastError: lastError})
		}
	} else if cfg := m.config.Adapters.Feishu; cfg.Enabled || strings.TrimSpace(cfg.AppID) != "" {
		accountID := feishu.OptionsFromConfig(cfg).AccountID()
		status, lastError := statusFor("feishu", accountID, cfg.Enabled)
		statuses = append(statuses, channel.AccountStatus{ChannelKey: "feishu", AccountID: accountID, Enabled: cfg.Enabled, Status: status, LastError: lastError})
	}
	if cfg := m.config.Adapters.DingTalk; cfg.Enabled || strings.TrimSpace(cfg.APIKey) != "" {
		accountID := dingtalk.OptionsFromConfig(cfg).AccountID()
		status, lastError := statusFor("dingtalk", accountID, cfg.Enabled)
		statuses = append(statuses, channel.AccountStatus{ChannelKey: "dingtalk", AccountID: accountID, Enabled: cfg.Enabled, Status: status, LastError: lastError})
	}
	weixinConfig := m.config.Adapters.Weixin
	weixinOptions := weixin.OptionsFromConfig(weixinConfig)
	if len(weixinOptions.Accounts) == 0 && weixinConfig.Enabled {
		status := "awaiting_binding"
		lastError := strings.TrimSpace(m.lastErrors["weixin"])
		if lastError != "" {
			status = "degraded"
		}
		statuses = append(statuses, channel.AccountStatus{ChannelKey: "weixin", Enabled: true, Status: status, LastError: lastError})
	}
	for _, account := range weixinOptions.Accounts {
		accountID := strings.TrimSpace(account.BotID)
		status, lastError := statusFor("weixin", accountID, weixinConfig.Enabled)
		statuses = append(statuses, channel.AccountStatus{ChannelKey: "weixin", AccountID: accountID, Enabled: weixinConfig.Enabled, Status: status, LastError: lastError})
	}
	return statuses
}
