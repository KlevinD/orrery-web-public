package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	gwbindings "orrery_v2/internal/gateway/binding"
	"orrery_v2/internal/gateway/channel"
	"orrery_v2/internal/gateway/channel/weixin"
	gwconfig "orrery_v2/internal/gateway/config"
	gwprotocol "orrery_v2/internal/gateway/protocol"
	"orrery_v2/internal/platform"
	"orrery_v2/internal/platform/logging"
)

const gatewayBindingRollbackTimeout = 30 * time.Second

func enabledGatewayChannelErrors(cfg gwconfig.Config, message string) map[string]string {
	errorsByChannel := map[string]string{}
	if cfg.Adapters.Feishu.Enabled {
		errorsByChannel["feishu"] = message
	}
	if cfg.Adapters.DingTalk.Enabled {
		errorsByChannel["dingtalk"] = message
	}
	if cfg.Adapters.Weixin.Enabled {
		errorsByChannel["weixin"] = message
	}
	return errorsByChannel
}

func stopStartedGatewayAdapters(ctx context.Context, started []channel.StartedAdapter, reason string) error {
	var stopErrors []error
	for _, adapter := range started {
		if adapter.Handle != nil {
			stopCtx := ctx
			cancel := func() {}
			if _, hasDeadline := ctx.Deadline(); !hasDeadline {
				stopCtx, cancel = context.WithTimeout(ctx, 15*time.Second)
			}
			if err := adapter.Handle.Stop(stopCtx, reason); err != nil {
				stopErrors = append(stopErrors, fmt.Errorf("stop gateway channel %s: %w", adapter.Key, err))
			}
			cancel()
		}
	}
	return errors.Join(stopErrors...)
}

func productionGatewayConfigStore(rt *platform.Runtime) *gwconfig.Store {
	if rt != nil && rt.Databases != nil {
		if core, err := rt.Databases.Store("core"); err == nil && core != nil {
			return gwconfig.NewDatabaseStore(core, gwconfig.DatabaseStoreOptions{
				LegacyPath:               productionGatewayConfigPath(rt),
				SecretKeyPath:            productionGatewaySecretKeyPath(rt),
				SecretKey:                os.Getenv("ORRERY_GATEWAY_SECRET_KEY"),
				RequireExistingSecretKey: strings.EqualFold(strings.TrimSpace(rt.Config.Environment), "production"),
			})
		}
	}
	return gwconfig.NewStore(productionGatewayConfigPath(rt))
}

func validatedProductionGatewayConfigStore(rt *platform.Runtime) (*gwconfig.Store, error) {
	store := productionGatewayConfigStore(rt)
	if err := store.ValidateEncryptionKey(); err != nil {
		return nil, fmt.Errorf("validate production gateway encryption key: %w", err)
	}
	return store, nil
}

func productionGatewayConfigPath(rt *platform.Runtime) string {
	if rt != nil {
		if path := strings.TrimSpace(rt.Config.Bootstrap.GatewayConfigPath); path != "" {
			return path
		}
	}
	if rt == nil || strings.TrimSpace(rt.Config.BaseDir) == "" {
		return filepath.Join("config", "gateway.yaml")
	}
	return filepath.Join(rt.Config.BaseDir, "config", "gateway.yaml")
}

func productionGatewayStateDir(rt *platform.Runtime) string {
	return filepath.Join(filepath.Dir(productionGatewayConfigPath(rt)), "state")
}

func productionGatewaySecretKeyPath(rt *platform.Runtime) string {
	if path := strings.TrimSpace(os.Getenv("ORRERY_GATEWAY_SECRET_KEY_FILE")); path != "" {
		return path
	}
	if rt != nil && strings.TrimSpace(rt.Config.Secret.Root) != "" {
		return filepath.Join(rt.Config.Secret.Root, "gateway-config-key")
	}
	return productionGatewayConfigPath(rt) + ".key"
}

type productionGatewayChannelLogger struct {
	logger logging.Logger
}

func (l productionGatewayChannelLogger) Info(message string, metadata map[string]any) {
	if l.logger != nil {
		l.logger.Info(context.Background(), message, logging.Any("metadata", metadata))
	}
}

func (l productionGatewayChannelLogger) Warn(message string, metadata map[string]any) {
	if l.logger != nil {
		l.logger.Warn(context.Background(), message, logging.Any("metadata", metadata))
	}
}

func (l productionGatewayChannelLogger) Error(message string, metadata map[string]any) {
	if l.logger != nil {
		l.logger.Error(context.Background(), message, logging.Any("metadata", metadata))
	}
}

func (m *productionMessageGatewayManager) syncWeixinCommonBindings(ctx context.Context, cfg gwconfig.Config) error {
	if m == nil || m.bindingStore == nil {
		return fmt.Errorf("gateway binding store is not configured")
	}
	if !cfg.Adapters.Weixin.Enabled {
		if _, err := m.bindingStore.ReconcileVerifiedChannelBindings(ctx, string(gwprotocol.ChannelWeixin), nil); err != nil {
			return fmt.Errorf("disable Weixin outbound bindings: %w", err)
		}
		return nil
	}
	extra := cfg.Adapters.Weixin.Extra
	if extra == nil {
		extra = map[string]any{}
	}
	humans := stringBindingsRouteExtra(extra["weixinUserBindings"])
	defaults := stringBindingsRouteExtra(extra["agentBindings"])
	active := stringBindingsRouteExtra(extra["activeAgentBindings"])
	accounts, _ := extra["weixinAccounts"].(map[string]any)
	inputs := make([]gwbindings.UpsertInput, 0, len(humans))
	for bindingKey, humanID := range humans {
		tenantID, userID, ok := gwconfig.ParseWeixinBindingKey(bindingKey)
		if !ok || strings.TrimSpace(humanID) == "" {
			return fmt.Errorf("invalid Weixin human binding %q", bindingKey)
		}
		raw, _ := accounts[bindingKey].(map[string]any)
		if raw == nil && tenantID == gwconfig.DefaultTenantID {
			raw, _ = accounts[userID].(map[string]any)
		}
		accountID := firstNonBlank(routeStringBindingValue(raw["botId"]), routeStringBindingValue(raw["bot_id"]), routeStringBindingValue(raw["accountId"]))
		if accountID == "" {
			return fmt.Errorf("Weixin human binding %q has no connected account", bindingKey)
		}
		input := gwbindings.UpsertInput{
			TenantID: tenantID, ChannelKey: string(gwprotocol.ChannelWeixin), AccountID: accountID,
			ExternalTenantID: tenantID, ExternalUserID: userID, HumanProfileID: strings.TrimSpace(humanID),
			DefaultAgentID: strings.TrimSpace(defaults[bindingKey]), ActiveAgentID: strings.TrimSpace(active[bindingKey]),
		}
		if err := m.validateWeixinBindingProfiles(ctx, input); err != nil {
			return fmt.Errorf("invalid Weixin binding %q: %w", bindingKey, err)
		}
		inputs = append(inputs, input)
	}
	if _, err := m.bindingStore.ReconcileVerifiedChannelBindings(ctx, string(gwprotocol.ChannelWeixin), inputs); err != nil {
		return fmt.Errorf("sync Weixin outbound bindings: %w", err)
	}
	return nil
}

func (m *productionMessageGatewayManager) rollbackWeixinCommonBindings(cfg gwconfig.Config) error {
	ctx, cancel := context.WithTimeout(context.Background(), gatewayBindingRollbackTimeout)
	defer cancel()
	if err := m.syncWeixinCommonBindings(ctx, cfg); err != nil {
		return fmt.Errorf("restore previous Weixin outbound bindings: %w", err)
	}
	return nil
}

func (m *productionMessageGatewayManager) validateWeixinBindingProfiles(ctx context.Context, input gwbindings.UpsertInput) error {
	if m == nil || m.profiles == nil {
		return fmt.Errorf("gateway profile directory is not configured")
	}
	human, err := m.profiles.GetProfile(ctx, agentcore.AgentProfileID(strings.TrimSpace(input.HumanProfileID)))
	if err != nil {
		return fmt.Errorf("load human profile: %w", err)
	}
	if human.AgentKind != agentcore.AgentKindHuman || !human.Enabled || strings.TrimSpace(string(human.OrgIdentity.OrganizationRef.ID)) != input.TenantID {
		return fmt.Errorf("human profile is disabled, is not human, or is outside tenant scope")
	}
	agentID := firstNonBlank(input.ActiveAgentID, input.DefaultAgentID)
	if agentID == "" {
		return nil
	}
	agent, err := m.profiles.GetProfile(ctx, agentcore.AgentProfileID(agentID))
	if err != nil {
		return fmt.Errorf("load agent profile: %w", err)
	}
	if agent.AgentKind != agentcore.AgentKindAI || !agent.Enabled || strings.TrimSpace(string(agent.OrgIdentity.OrganizationRef.ID)) != input.TenantID {
		return fmt.Errorf("agent profile is disabled, is not AI, or is outside tenant scope")
	}
	return nil
}

func (m *productionMessageGatewayManager) startWeixin(ctx context.Context, cfg gwconfig.Config) (channel.StartedAdapter, error) {
	return m.registry.StartWeixin(ctx, cfg.Adapters.Weixin, channel.StartDeps{
		Gateway:       m.gateway,
		Config:        cfg,
		Logger:        m.logger,
		StateDir:      productionGatewayStateDir(m.rt),
		InboundEvents: m.inboundStore,
	})
}

func (m *productionMessageGatewayManager) startGatewayReplacements(
	ctx context.Context,
	cfg gwconfig.Config,
	deps channel.StartDeps,
	changed map[string]bool,
) ([]channel.StartedAdapter, error) {
	var started []channel.StartedAdapter
	start := func(adapter channel.StartedAdapter, err error) error {
		if err != nil {
			return err
		}
		started = append(started, adapter)
		return nil
	}
	if changed["feishu"] && cfg.Adapters.Feishu.Enabled {
		feishuStarted, err := m.registry.StartFeishuConfigured(ctx, cfg.Adapters.Feishu, deps)
		if err != nil {
			return started, err
		}
		started = append(started, feishuStarted...)
	}
	weixinAccountCount, err := weixin.ValidateConfiguredAccounts(cfg.Adapters.Weixin)
	if err != nil {
		return started, err
	}
	if changed["weixin"] && cfg.Adapters.Weixin.Enabled && weixinAccountCount > 0 {
		if err := start(m.registry.StartWeixin(ctx, cfg.Adapters.Weixin, deps)); err != nil {
			return started, err
		}
	}
	platformConfigs := productionGatewayPlatformConfigs(cfg)
	for _, key := range productionGatewayPlatformChannelKeys() {
		platformConfig := platformConfigs[key]
		if !changed[key] || !platformConfig.Enabled {
			continue
		}
		if err := start(m.registry.StartPlatform(ctx, key, platformConfig, deps)); err != nil {
			return started, err
		}
	}
	return started, nil
}

func markMissingGatewayRuntimeChannels(cfg gwconfig.Config, started []channel.StartedAdapter, changed map[string]bool) error {
	connected := make(map[string]bool, len(started))
	for _, adapter := range started {
		if adapter.Handle != nil {
			healthy := true
			if provider, ok := adapter.Handle.(channel.ConnectionHealthProvider); ok {
				healthy = provider.GatewayConnectionHealthy()
			}
			connected[adapter.Key+"\x00"+strings.TrimSpace(adapter.AccountID)] = healthy
		}
	}
	if cfg.Adapters.Feishu.Enabled {
		for _, accountID := range configuredFeishuAccountIDs(cfg.Adapters.Feishu) {
			if !connected["feishu\x00"+accountID] && !connected["feishu\x00"] {
				changed["feishu"] = true
				break
			}
		}
	}
	weixinAccountCount, err := weixin.ValidateConfiguredAccounts(cfg.Adapters.Weixin)
	if err != nil {
		return err
	}
	if cfg.Adapters.Weixin.Enabled && weixinAccountCount > 0 && !connected["weixin"] {
		changed["weixin"] = true
	}
	for key, platformConfig := range productionGatewayPlatformConfigs(cfg) {
		if platformConfig.Enabled && !connected[key] {
			changed[key] = true
		}
	}
	return nil
}

func configuredFeishuAccountIDs(cfg gwconfig.FeishuAdapterConfig) []string {
	if len(cfg.Accounts) == 0 {
		accountID := strings.TrimSpace(cfg.AccountID)
		if accountID == "" && strings.TrimSpace(cfg.AppID) != "" {
			accountID = "feishu:" + strings.TrimSpace(cfg.AppID)
		}
		if cfg.Enabled && accountID != "" {
			return []string{accountID}
		}
		return nil
	}
	accountIDs := make([]string, 0, len(cfg.Accounts))
	for accountID, account := range cfg.Accounts {
		if account.Enabled {
			accountIDs = append(accountIDs, strings.TrimSpace(accountID))
		}
	}
	sort.Strings(accountIDs)
	return accountIDs
}

func hasStartedGatewayChannel(started []channel.StartedAdapter, key string) bool {
	for _, adapter := range started {
		if adapter.Key == key && adapter.Handle != nil {
			if provider, ok := adapter.Handle.(channel.ConnectionHealthProvider); ok {
				return provider.GatewayConnectionHealthy()
			}
			return true
		}
	}
	return false
}

func (m *productionMessageGatewayManager) runtimeContextLocked() context.Context {
	if m.serviceCtx != nil {
		return m.serviceCtx
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx
}

func changedGatewayRuntimeChannels(oldConfig, nextConfig gwconfig.Config) map[string]bool {
	changed := map[string]bool{
		"feishu": !reflect.DeepEqual(oldConfig.Adapters.Feishu, nextConfig.Adapters.Feishu),
		"weixin": !weixinRuntimeConfigEqual(oldConfig.Adapters.Weixin, nextConfig.Adapters.Weixin),
	}
	oldPlatforms := productionGatewayPlatformConfigs(oldConfig)
	nextPlatforms := productionGatewayPlatformConfigs(nextConfig)
	for _, key := range productionGatewayPlatformChannelKeys() {
		changed[key] = !reflect.DeepEqual(oldPlatforms[key], nextPlatforms[key])
	}
	return changed
}

func weixinRuntimeConfigEqual(left, right gwconfig.WeixinAdapterConfig) bool {
	return reflect.DeepEqual(weixinRuntimeConfig(left), weixinRuntimeConfig(right))
}

func weixinRuntimeConfig(cfg gwconfig.WeixinAdapterConfig) gwconfig.WeixinAdapterConfig {
	extra := make(map[string]any, len(cfg.Extra))
	for key, value := range cfg.Extra {
		extra[key] = value
	}
	delete(extra, "agentBindings")
	delete(extra, "activeAgentBindings")
	delete(extra, "weixinUserBindings")
	cfg.Extra = extra
	return cfg
}

func replaceStartedGatewayAdapters(
	oldStarted []channel.StartedAdapter,
	replacements []channel.StartedAdapter,
	changed map[string]bool,
) ([]channel.StartedAdapter, []channel.StartedAdapter) {
	next := make([]channel.StartedAdapter, 0, len(oldStarted)+len(replacements))
	var retired []channel.StartedAdapter
	for _, adapter := range oldStarted {
		if changed[adapter.Key] {
			retired = append(retired, adapter)
			continue
		}
		next = append(next, adapter)
	}
	next = append(next, replacements...)
	return next, retired
}

func productionGatewayPlatformChannelKeys() []string {
	return []string{"dingtalk"}
}

func productionGatewayPlatformConfigs(cfg gwconfig.Config) map[string]gwconfig.PlatformAdapterConfig {
	adapters := cfg.Adapters
	return map[string]gwconfig.PlatformAdapterConfig{
		"dingtalk": adapters.DingTalk,
	}
}
