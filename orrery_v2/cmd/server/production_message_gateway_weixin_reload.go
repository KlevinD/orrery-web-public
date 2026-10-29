package main

import (
	"context"
	"errors"
	"fmt"

	"orrery_v2/internal/gateway/channel"
	"orrery_v2/internal/gateway/channel/weixin"
	gwconfig "orrery_v2/internal/gateway/config"
)

func (m *productionMessageGatewayManager) ReloadWeixinSettings(ctx context.Context, cfg gwconfig.Config) error {
	if m == nil {
		return fmt.Errorf("gateway manager is not configured")
	}
	m.reloadMu.Lock()
	defer m.reloadMu.Unlock()
	m.mu.Lock()
	if m.stopped {
		m.mu.Unlock()
		return fmt.Errorf("gateway manager is stopped")
	}
	m.mu.Unlock()

	m.mu.Lock()
	oldConfig := m.config
	oldStarted := append([]channel.StartedAdapter(nil), m.started...)
	runtimeCtx := m.runtimeContextLocked()
	m.mu.Unlock()
	weixinAccountCount, err := weixin.ValidateConfiguredAccounts(cfg.Adapters.Weixin)
	if err != nil {
		return err
	}
	if weixinRuntimeConfigEqual(oldConfig.Adapters.Weixin, cfg.Adapters.Weixin) &&
		(!cfg.Adapters.Weixin.Enabled || weixinAccountCount == 0 || hasStartedGatewayChannel(oldStarted, "weixin")) {
		if err := m.syncWeixinCommonBindings(ctx, cfg); err != nil {
			return err
		}
		m.runner.UpdateConfig(cfg)
		m.mu.Lock()
		m.config = cfg
		delete(m.lastErrors, "weixin")
		m.mu.Unlock()
		return nil
	}

	// Weixin begins polling inside Start, so queued messages must see the new routes first.
	routesStaged := cfg.Adapters.Weixin.Enabled && weixinAccountCount > 0
	routesCommitted := false
	if routesStaged {
		m.runner.UpdateConfig(cfg)
		defer func() {
			if !routesCommitted {
				m.runner.UpdateConfig(oldConfig)
			}
		}()
	}
	var next channel.StartedAdapter
	if cfg.Adapters.Weixin.Enabled && weixinAccountCount > 0 {
		next, err = m.startWeixin(runtimeCtx, cfg)
	}
	if err != nil {
		m.mu.Lock()
		if m.lastErrors == nil {
			m.lastErrors = map[string]string{}
		}
		m.lastErrors["weixin"] = "connection_failed"
		m.mu.Unlock()
		return err
	}
	replacements := []channel.StartedAdapter{}
	if next.Handle != nil {
		replacements = append(replacements, next)
	}
	if err := m.syncWeixinCommonBindings(ctx, cfg); err != nil {
		cleanupErr := stopStartedGatewayAdapters(context.Background(), replacements, "weixin binding sync failed")
		return errors.Join(err, cleanupErr)
	}
	nextStarted, retired := replaceStartedGatewayAdapters(oldStarted, replacements, map[string]bool{"weixin": true})
	if err := stopStartedGatewayAdapters(context.Background(), retired, "weixin settings reload"); err != nil {
		cleanupErr := stopStartedGatewayAdapters(context.Background(), replacements, "weixin settings reload retirement failed")
		rollbackErr := m.rollbackWeixinCommonBindings(oldConfig)
		m.mu.Lock()
		if m.lastErrors == nil {
			m.lastErrors = map[string]string{}
		}
		m.lastErrors["weixin"] = "retirement_failed"
		m.mu.Unlock()
		return errors.Join(fmt.Errorf("retire replaced Weixin gateway channel: %w", err), cleanupErr, rollbackErr)
	}
	m.runner.UpdateConfig(cfg)
	routesCommitted = true
	m.mu.Lock()
	m.config = cfg
	m.started = nextStarted
	delete(m.lastErrors, "weixin")
	m.mu.Unlock()
	if m.rt != nil && m.rt.Logger != nil {
		m.rt.Logger.Info(ctx, "weixin gateway channel reloaded")
	}
	return nil
}
