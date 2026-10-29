package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	"orrery_v2/internal/app/discussionsession"
	gwbindings "orrery_v2/internal/gateway/binding"
	"orrery_v2/internal/gateway/channel"
	gwconfig "orrery_v2/internal/gateway/config"
	gwcore "orrery_v2/internal/gateway/core"
	gwprotocol "orrery_v2/internal/gateway/protocol"
	"orrery_v2/internal/platform"
	"orrery_v2/internal/platform/db"
)

type testProductionGatewayHandle struct {
	stopped bool
	stopErr error
}

func (h *testProductionGatewayHandle) Stop(context.Context, string) error {
	h.stopped = true
	return h.stopErr
}

func TestProductionMessageGatewayReloadRejectsRetirementFailure(t *testing.T) {
	runner := newProductionMessageGatewayRunner(&fakeProductionGatewayDiscussionService{})
	registry := channel.NewRegistry()
	replacement := &testProductionGatewayHandle{}
	registry.Register("dingtalk", func(gwconfig.PlatformAdapterConfig) channel.Adapter {
		return &testProductionGatewayAdapter{start: func(context.Context, channel.StartDeps) (channel.Handle, error) {
			return replacement, nil
		}}
	})
	oldConfig := gwconfig.DefaultConfig()
	oldConfig.Adapters.DingTalk.Enabled = true
	oldConfig.Adapters.DingTalk.APIKey = "old-client"
	nextConfig := oldConfig
	nextConfig.Adapters.DingTalk.APIKey = "new-client"
	oldHandle := &testProductionGatewayHandle{stopErr: errors.New("shutdown timed out")}
	manager := &productionMessageGatewayManager{
		rt: &platform.Runtime{}, registry: registry, runner: runner, gateway: gwcore.NewGateway(runner),
		serviceCtx: context.Background(), config: oldConfig,
		started:    []channel.StartedAdapter{{Key: "dingtalk", Handle: oldHandle}},
		lastErrors: map[string]string{}, bindingStore: testProductionGatewayBindingStore(t),
	}

	err := manager.ReloadGatewaySettings(context.Background(), nextConfig)
	if err == nil || !strings.Contains(err.Error(), "shutdown timed out") {
		t.Fatalf("retirement error = %v", err)
	}
	if manager.config.Adapters.DingTalk.APIKey != "old-client" || len(manager.started) != 1 || manager.started[0].Handle != oldHandle {
		t.Fatalf("manager committed config after retirement failure: config=%+v started=%+v", manager.config.Adapters.DingTalk, manager.started)
	}
	if !replacement.stopped {
		t.Fatal("replacement was not cleaned up after retirement failure")
	}
	if manager.lastErrors["dingtalk"] != "retirement_failed" {
		t.Fatalf("last error = %q", manager.lastErrors["dingtalk"])
	}
}

func TestProductionMessageGatewayReloadRestoresBindingsAfterRetirementFailure(t *testing.T) {
	runner := newProductionMessageGatewayRunner(&fakeProductionGatewayDiscussionService{})
	registry := channel.NewRegistry()
	replacement := &testProductionGatewayHandle{}
	registry.Register("dingtalk", func(gwconfig.PlatformAdapterConfig) channel.Adapter {
		return &testProductionGatewayAdapter{start: func(context.Context, channel.StartDeps) (channel.Handle, error) {
			return replacement, nil
		}}
	})
	registry.RegisterWeixin(func(gwconfig.WeixinAdapterConfig) channel.Adapter {
		return &testProductionGatewayAdapter{start: func(context.Context, channel.StartDeps) (channel.Handle, error) {
			return &testProductionGatewayHandle{}, nil
		}}
	})
	oldConfig := gatewayReloadBindingConfig("agent-old")
	oldConfig.Adapters.DingTalk.Enabled = true
	oldConfig.Adapters.DingTalk.APIKey = "old-client"
	nextConfig := gatewayReloadBindingConfig("agent-new")
	nextConfig.Adapters.DingTalk.Enabled = true
	nextConfig.Adapters.DingTalk.APIKey = "new-client"
	bindings := testProductionGatewayBindingStore(t)
	manager := &productionMessageGatewayManager{
		rt: &platform.Runtime{}, registry: registry, runner: runner, gateway: gwcore.NewGateway(runner),
		profiles:   systemGatewayTestDirectory("human-a", "agent-old", "agent-new"),
		serviceCtx: context.Background(), config: oldConfig, bindingStore: bindings,
		started: []channel.StartedAdapter{
			{Key: "weixin", Handle: &testProductionGatewayHandle{}},
			{Key: "dingtalk", Handle: &testProductionGatewayHandle{stopErr: errors.New("shutdown timed out")}},
		},
		lastErrors: map[string]string{},
	}
	if err := manager.syncWeixinCommonBindings(context.Background(), oldConfig); err != nil {
		t.Fatalf("seed Weixin binding: %v", err)
	}

	err := manager.ReloadGatewaySettings(context.Background(), nextConfig)
	if err == nil || !strings.Contains(err.Error(), "shutdown timed out") {
		t.Fatalf("ReloadGatewaySettings error = %v", err)
	}
	resolved, err := bindings.Resolve(context.Background(), gwbindings.ExternalIdentity{
		ChannelKey: string(gwprotocol.ChannelWeixin), AccountID: "bot-a", ExternalTenantID: gwconfig.DefaultTenantID, ExternalUserID: "wx-user",
	})
	if err != nil {
		t.Fatalf("resolve restored binding: %v", err)
	}
	if resolved.CurrentAgentID() != "agent-old" {
		t.Fatalf("binding agent after failed reload = %q, want agent-old", resolved.CurrentAgentID())
	}
	if !replacement.stopped {
		t.Fatal("replacement was not cleaned up after retirement failure")
	}
}

func gatewayReloadBindingConfig(agentID string) gwconfig.Config {
	cfg := gwconfig.DefaultConfig()
	cfg.Adapters.Weixin.Enabled = true
	cfg.Adapters.Weixin.Extra = map[string]any{
		"weixinAccounts": map[string]any{
			"wx-user": map[string]any{"userId": "wx-user", "botId": "bot-a", "token": "token-a"},
		},
		"weixinUserBindings": map[string]any{"wx-user": "human-a"},
		"agentBindings":      map[string]any{"wx-user": agentID},
		"activeAgentBindings": map[string]any{
			"wx-user": agentID,
		},
	}
	return cfg
}

type testProductionGatewayAdapter struct {
	start func(context.Context, channel.StartDeps) (channel.Handle, error)
}

func testProductionGatewayBindingStore(t *testing.T) *gwbindings.Store {
	t.Helper()
	store, err := db.Open(context.Background(), db.Config{Driver: db.DriverMemory, Name: t.Name() + "-gateway-bindings"})
	if err != nil {
		t.Fatalf("open gateway binding store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return gwbindings.NewStore(store)
}

func (*testProductionGatewayAdapter) Key() string { return "weixin" }

func (a *testProductionGatewayAdapter) Start(ctx context.Context, deps channel.StartDeps) (channel.Handle, error) {
	return a.start(ctx, deps)
}

func TestProductionMessageGatewayManagerLoadsPersonalAssistantRoutesBeforeStartingWeixin(t *testing.T) {
	discussions := &fakeProductionGatewayDiscussionService{events: []discussionsession.StreamEventDTO{{Kind: discussionsession.StreamEventRunCompleted}}}
	runner := newProductionMessageGatewayRunner(discussions)
	runner.SetAgentDirectory(systemGatewayTestDirectory("human-a"))
	registry := channel.NewRegistry()
	var startupEvents []gwprotocol.Event
	registry.RegisterWeixin(func(gwconfig.WeixinAdapterConfig) channel.Adapter {
		return &testProductionGatewayAdapter{start: func(ctx context.Context, deps channel.StartDeps) (channel.Handle, error) {
			input := gwprotocol.SubmitTurnInput{
				SessionKey: "weixin:wx-user", ChannelKey: gwprotocol.ChannelWeixin, Message: "hello",
				Telemetry: verifiedWeixinTelemetry("wx-user"),
			}
			input.Telemetry["weixin_account_id"] = "bot-a"
			startupEvents = collectGatewayEvents(deps.Gateway.SubmitTurn(ctx, input))
			return &testProductionGatewayHandle{}, nil
		}}
	})
	bindingKey := gwconfig.WeixinBindingKey(gwconfig.DefaultTenantID, "wx-user")
	cfg := gwconfig.DefaultConfig()
	cfg.Adapters.Weixin.Enabled = true
	cfg.Adapters.Weixin.Extra = map[string]any{
		"weixinAccounts": map[string]any{
			bindingKey: map[string]any{"userId": "wx-user", "botId": "bot-a", "token": "token-a"},
		},
		"weixinUserBindings":        map[string]any{bindingKey: "human-a"},
		"personalAssistantBindings": map[string]any{bindingKey: "ding-account"},
	}
	manager := &productionMessageGatewayManager{
		rt: &platform.Runtime{}, registry: registry, runner: runner, gateway: gwcore.NewGateway(runner),
		profiles: systemGatewayTestDirectory("human-a"), serviceCtx: context.Background(),
		bindingStore: testProductionGatewayBindingStore(t), lastErrors: map[string]string{},
	}

	if err := manager.ReloadGatewaySettings(context.Background(), cfg); err != nil {
		t.Fatalf("ReloadGatewaySettings: %v", err)
	}
	for _, event := range startupEvents {
		if event.FinishReason == "unregistered" || event.Text == unregisteredWeixinUserMessage {
			t.Fatalf("message received during Weixin startup used empty routes: %+v", startupEvents)
		}
	}
	if len(discussions.cmds) != 1 || discussions.cmd.ConversationKind != discussionsession.ConversationKindPersonalAssistant {
		t.Fatalf("startup message did not reach the personal assistant: events=%+v command=%+v", startupEvents, discussions.cmd)
	}
	if discussions.viewer.ViewerID != "human-a" || discussions.viewer.AgentProfileID != "human-a" || discussions.viewer.OwnerID != "ding-account" {
		t.Fatalf("personal assistant viewer = %+v", discussions.viewer)
	}
}

func TestProductionMessageGatewayManagerWeixinReloadKeepsLastKnownGoodChannel(t *testing.T) {
	discussions := &fakeProductionGatewayDiscussionService{}
	runner := newProductionMessageGatewayRunner(discussions)
	registry := channel.NewRegistry()
	var startedBaseURLs []string
	registry.RegisterWeixin(func(cfg gwconfig.WeixinAdapterConfig) channel.Adapter {
		return &testProductionGatewayAdapter{start: func(context.Context, channel.StartDeps) (channel.Handle, error) {
			baseURL, _ := cfg.Extra["ilinkBaseUrl"].(string)
			startedBaseURLs = append(startedBaseURLs, baseURL)
			if baseURL == "https://invalid.example.test" {
				return nil, errors.New("invalid weixin endpoint")
			}
			return &testProductionGatewayHandle{}, nil
		}}
	})
	oldWeixin := &testProductionGatewayHandle{}
	otherChannel := &testProductionGatewayHandle{}
	oldConfig := gwconfig.DefaultConfig()
	oldConfig.Adapters.Weixin.Enabled = true
	oldConfig.Adapters.Weixin.Extra = map[string]any{
		"ilinkBaseUrl":       "https://previous.example.test",
		"agentBindings":      map[string]any{"wx-user": "agent-old"},
		"weixinUserBindings": map[string]any{"wx-user": "human-old"},
		"weixinAccounts": map[string]any{
			"wx-user": map[string]any{"userId": "wx-user", "botId": "bot-old", "token": "token-old"},
		},
	}
	runner.UpdateConfig(oldConfig)
	manager := &productionMessageGatewayManager{
		rt:         &platform.Runtime{},
		registry:   registry,
		runner:     runner,
		gateway:    gwcore.NewGateway(runner),
		serviceCtx: context.Background(),
		config:     oldConfig,
		started: []channel.StartedAdapter{
			{Key: "telegram", Handle: otherChannel},
			{Key: "weixin", Handle: oldWeixin},
		},
	}
	next := oldConfig
	next.Adapters.Weixin.Extra = map[string]any{
		"ilinkBaseUrl":       "https://invalid.example.test",
		"agentBindings":      map[string]any{"wx-user": "agent-new"},
		"weixinUserBindings": map[string]any{"wx-user": "human-old"},
		"weixinAccounts": map[string]any{
			"wx-user": map[string]any{"userId": "wx-user", "botId": "bot-old", "token": "token-old"},
		},
	}

	err := manager.ReloadWeixinSettings(context.Background(), next)
	if err == nil || !strings.Contains(err.Error(), "invalid weixin endpoint") {
		t.Fatalf("reload error = %v", err)
	}
	if oldWeixin.stopped {
		t.Fatal("old Weixin channel was stopped after replacement failed")
	}
	if otherChannel.stopped {
		t.Fatal("unrelated Gateway channel was stopped during Weixin reload")
	}
	if len(startedBaseURLs) != 1 || startedBaseURLs[0] != "https://invalid.example.test" {
		t.Fatalf("Weixin start attempts = %+v", startedBaseURLs)
	}
	identity := runner.weixinIdentity(gwprotocol.SubmitTurnInput{
		ChannelKey: gwprotocol.ChannelWeixin,
		Telemetry: map[string]string{
			"weixin_sender_id":       "wx-user",
			"weixin_sender_verified": "true",
		},
	})
	if identity.AgentID != "agent-old" || identity.ViewerID != "human-old" {
		t.Fatalf("runner routes were not rolled back: %+v", identity)
	}
}

func TestProductionMessageGatewayManagerFullReloadKeepsLastKnownGoodChannels(t *testing.T) {
	runner := newProductionMessageGatewayRunner(&fakeProductionGatewayDiscussionService{})
	registry := channel.NewRegistry()
	registry.Register("dingtalk", func(cfg gwconfig.PlatformAdapterConfig) channel.Adapter {
		return &testProductionGatewayAdapter{start: func(context.Context, channel.StartDeps) (channel.Handle, error) {
			if cfg.APIKey == "invalid-client" {
				return nil, errors.New("dingtalk credential rejected")
			}
			return &testProductionGatewayHandle{}, nil
		}}
	})
	oldHandle := &testProductionGatewayHandle{}
	oldConfig := gwconfig.DefaultConfig()
	oldConfig.Adapters.DingTalk.Enabled = true
	oldConfig.Adapters.DingTalk.APIKey = "working-client"
	nextConfig := oldConfig
	nextConfig.Adapters.DingTalk.APIKey = "invalid-client"
	manager := &productionMessageGatewayManager{
		rt: &platform.Runtime{}, registry: registry, runner: runner, gateway: gwcore.NewGateway(runner),
		serviceCtx: context.Background(), config: oldConfig,
		started: []channel.StartedAdapter{{Key: "dingtalk", Handle: oldHandle}}, lastErrors: map[string]string{},
	}

	err := manager.ReloadGatewaySettings(context.Background(), nextConfig)
	if err == nil || !strings.Contains(err.Error(), "credential rejected") {
		t.Fatalf("ReloadGatewaySettings error = %v", err)
	}
	if oldHandle.stopped {
		t.Fatal("working DingTalk channel was stopped after replacement failed")
	}
	if manager.config.Adapters.DingTalk.APIKey != "working-client" || len(manager.started) != 1 || manager.started[0].Handle != oldHandle {
		t.Fatalf("manager lost last-known-good state: config=%+v started=%+v", manager.config.Adapters.DingTalk, manager.started)
	}
}

func TestProductionMessageGatewayManagerRouteOnlyWeixinReloadDoesNotReconnect(t *testing.T) {
	runner := newProductionMessageGatewayRunner(&fakeProductionGatewayDiscussionService{})
	registry := channel.NewRegistry()
	starts := 0
	registry.RegisterWeixin(func(gwconfig.WeixinAdapterConfig) channel.Adapter {
		starts++
		return &testProductionGatewayAdapter{start: func(context.Context, channel.StartDeps) (channel.Handle, error) {
			return &testProductionGatewayHandle{}, nil
		}}
	})
	handle := &testProductionGatewayHandle{}
	oldConfig := gwconfig.DefaultConfig()
	oldConfig.Adapters.Weixin.Enabled = true
	oldConfig.Adapters.Weixin.Extra = map[string]any{
		"weixinAccounts": map[string]any{"user-a": map[string]any{"botId": "bot-a", "token": "token-a"}},
		"agentBindings":  map[string]any{"user-a": "agent-old"},
	}
	nextConfig := oldConfig
	nextConfig.Adapters.Weixin.Extra = map[string]any{
		"weixinAccounts": map[string]any{"user-a": map[string]any{"botId": "bot-a", "token": "token-a"}},
		"agentBindings":  map[string]any{"user-a": "agent-new"},
	}
	manager := &productionMessageGatewayManager{
		rt: &platform.Runtime{}, registry: registry, runner: runner, gateway: gwcore.NewGateway(runner),
		serviceCtx: context.Background(), config: oldConfig,
		started: []channel.StartedAdapter{{Key: "weixin", Handle: handle}}, lastErrors: map[string]string{},
		bindingStore: testProductionGatewayBindingStore(t),
	}

	if err := manager.ReloadWeixinSettings(context.Background(), nextConfig); err != nil {
		t.Fatalf("ReloadWeixinSettings: %v", err)
	}
	if starts != 0 || handle.stopped {
		t.Fatalf("route-only update reconnected channel: starts=%d stopped=%v", starts, handle.stopped)
	}
}

func TestProductionMessageGatewayManagerRetriesEnabledChannelWithoutHandle(t *testing.T) {
	runner := newProductionMessageGatewayRunner(&fakeProductionGatewayDiscussionService{})
	registry := channel.NewRegistry()
	starts := 0
	registry.Register("dingtalk", func(gwconfig.PlatformAdapterConfig) channel.Adapter {
		return &testProductionGatewayAdapter{start: func(context.Context, channel.StartDeps) (channel.Handle, error) {
			starts++
			return &testProductionGatewayHandle{}, nil
		}}
	})
	cfg := gwconfig.DefaultConfig()
	cfg.Adapters.DingTalk.Enabled = true
	cfg.Adapters.DingTalk.APIKey = "client-id"
	manager := &productionMessageGatewayManager{
		rt: &platform.Runtime{}, registry: registry, runner: runner, gateway: gwcore.NewGateway(runner),
		serviceCtx: context.Background(), config: cfg, lastErrors: map[string]string{"dingtalk": "connection_failed"},
		bindingStore: testProductionGatewayBindingStore(t),
	}

	if err := manager.ReloadGatewaySettings(context.Background(), cfg); err != nil {
		t.Fatalf("ReloadGatewaySettings: %v", err)
	}
	if starts != 1 || !hasStartedGatewayChannel(manager.started, "dingtalk") {
		t.Fatalf("missing channel was not restarted: starts=%d started=%+v", starts, manager.started)
	}
}

func TestProductionMessageGatewayManagerRejectsReloadsAfterStop(t *testing.T) {
	registry := channel.NewRegistry()
	starts := 0
	registry.Register("dingtalk", func(gwconfig.PlatformAdapterConfig) channel.Adapter {
		return &testProductionGatewayAdapter{start: func(context.Context, channel.StartDeps) (channel.Handle, error) {
			starts++
			return &testProductionGatewayHandle{}, nil
		}}
	})
	registry.RegisterWeixin(func(gwconfig.WeixinAdapterConfig) channel.Adapter {
		return &testProductionGatewayAdapter{start: func(context.Context, channel.StartDeps) (channel.Handle, error) {
			starts++
			return &testProductionGatewayHandle{}, nil
		}}
	})
	runtimeCtx, cancel := context.WithCancel(context.Background())
	manager := &productionMessageGatewayManager{
		registry: registry, serviceCtx: runtimeCtx, cancel: cancel, lastErrors: map[string]string{},
	}
	manager.Stop(context.Background())
	manager.Start(context.Background())
	if manager.serviceCtx != nil || manager.cancel != nil {
		t.Fatal("manager recreated its service context after stop")
	}

	cfg := gwconfig.DefaultConfig()
	cfg.Adapters.DingTalk.Enabled = true
	cfg.Adapters.DingTalk.APIKey = "client-id"
	cfg.Adapters.Weixin.Enabled = true
	cfg.Adapters.Weixin.Extra = map[string]any{
		"weixinAccounts": map[string]any{"user-a": map[string]any{"botId": "bot-a", "token": "token-a"}},
	}
	if err := manager.ReloadGatewaySettings(context.Background(), cfg); err == nil || !strings.Contains(err.Error(), "stopped") {
		t.Fatalf("full reload after stop error = %v", err)
	}
	if err := manager.ReloadWeixinSettings(context.Background(), cfg); err == nil || !strings.Contains(err.Error(), "stopped") {
		t.Fatalf("Weixin reload after stop error = %v", err)
	}
	if starts != 0 {
		t.Fatalf("started %d adapters after manager stop", starts)
	}
}
