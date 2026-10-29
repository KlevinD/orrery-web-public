package channel

import (
	"context"
	"fmt"
	"sort"
	"strings"

	gwconfig "orrery_v2/internal/gateway/config"
)

type Registry struct {
	factories     map[string]Factory
	feishuFactory FeishuFactory
	weixinFactory WeixinFactory
}

type StartedAdapter struct {
	Key       string
	AccountID string
	Handle    Handle
}

func NewRegistry() *Registry {
	return &Registry{
		factories: make(map[string]Factory),
	}
}

func (r *Registry) Register(key string, factory Factory) {
	if key == "" || factory == nil {
		return
	}
	r.factories[key] = factory
}

func (r *Registry) RegisterFeishu(factory FeishuFactory) {
	if factory == nil {
		return
	}
	r.feishuFactory = factory
}

func (r *Registry) RegisterWeixin(factory WeixinFactory) {
	if factory == nil {
		return
	}
	r.weixinFactory = factory
}

func (r *Registry) ValidateEnabled(cfg gwconfig.Config) error {
	if cfg.Adapters.Feishu.Enabled && r.feishuFactory == nil {
		return fmt.Errorf("gateway channel feishu is enabled but has no production adapter")
	}
	if cfg.Adapters.Weixin.Enabled && r.weixinFactory == nil {
		return fmt.Errorf("gateway channel weixin is enabled but has no production adapter")
	}
	if cfg.Adapters.DingTalk.Enabled && r.factories["dingtalk"] == nil {
		return fmt.Errorf("gateway channel dingtalk is enabled but has no production adapter")
	}
	return nil
}

func (r *Registry) StartFeishu(ctx context.Context, cfg gwconfig.FeishuAdapterConfig, deps StartDeps) (StartedAdapter, error) {
	if r.feishuFactory == nil {
		return StartedAdapter{}, fmt.Errorf("gateway channel feishu is not registered")
	}
	adapter := r.feishuFactory(cfg)
	if adapter == nil {
		return StartedAdapter{}, fmt.Errorf("gateway channel feishu factory returned nil")
	}
	handle, err := adapter.Start(ctx, deps)
	if err != nil {
		return StartedAdapter{}, fmt.Errorf("start gateway channel feishu: %w", err)
	}
	if handle == nil {
		return StartedAdapter{}, fmt.Errorf("start gateway channel feishu: adapter returned nil handle")
	}
	accountID := strings.TrimSpace(cfg.AccountID)
	if accountID == "" && strings.TrimSpace(cfg.AppID) != "" {
		accountID = "feishu:" + strings.TrimSpace(cfg.AppID)
	}
	return StartedAdapter{Key: "feishu", AccountID: accountID, Handle: handle}, nil
}

func (r *Registry) StartFeishuConfigured(ctx context.Context, cfg gwconfig.FeishuAdapterConfig, deps StartDeps) ([]StartedAdapter, error) {
	if len(cfg.Accounts) == 0 {
		started, err := r.StartFeishu(ctx, cfg, deps)
		if err != nil {
			return nil, err
		}
		return []StartedAdapter{started}, nil
	}
	keys := make([]string, 0, len(cfg.Accounts))
	for accountID := range cfg.Accounts {
		keys = append(keys, accountID)
	}
	sort.Strings(keys)
	started := make([]StartedAdapter, 0, len(keys))
	for _, accountID := range keys {
		account := cfg.Accounts[accountID]
		if !account.Enabled {
			continue
		}
		adapterConfig := gwconfig.FeishuAdapterConfig{
			AccountID: accountID, Enabled: true, AppID: account.AppID, AppSecret: account.AppSecret,
			EncryptKey: account.EncryptKey, VerifyToken: account.VerifyToken,
			DefaultSessionLabel: account.DefaultSessionLabel, ConnectionMode: account.ConnectionMode, DomainName: account.DomainName,
		}
		adapter, err := r.StartFeishu(ctx, adapterConfig, deps)
		if err != nil {
			return started, err
		}
		started = append(started, adapter)
	}
	return started, nil
}

func (r *Registry) StartWeixin(ctx context.Context, cfg gwconfig.WeixinAdapterConfig, deps StartDeps) (StartedAdapter, error) {
	if r.weixinFactory == nil {
		return StartedAdapter{}, fmt.Errorf("gateway channel weixin is not registered")
	}
	adapter := r.weixinFactory(cfg)
	if adapter == nil {
		return StartedAdapter{}, fmt.Errorf("gateway channel weixin factory returned nil")
	}
	handle, err := adapter.Start(ctx, deps)
	if err != nil {
		return StartedAdapter{}, fmt.Errorf("start gateway channel weixin: %w", err)
	}
	if handle == nil {
		return StartedAdapter{}, fmt.Errorf("start gateway channel weixin: adapter returned nil handle")
	}
	return StartedAdapter{Key: "weixin", Handle: handle}, nil
}

func (r *Registry) StartPlatform(ctx context.Context, key string, cfg gwconfig.PlatformAdapterConfig, deps StartDeps) (StartedAdapter, error) {
	factory := r.factories[key]
	if factory == nil {
		return StartedAdapter{}, fmt.Errorf("gateway channel %s is not registered", key)
	}
	adapter := factory(cfg)
	if adapter == nil {
		return StartedAdapter{}, fmt.Errorf("gateway channel %s factory returned nil", key)
	}
	handle, err := adapter.Start(ctx, deps)
	if err != nil {
		return StartedAdapter{}, fmt.Errorf("start gateway channel %s: %w", key, err)
	}
	if handle == nil {
		return StartedAdapter{}, fmt.Errorf("start gateway channel %s: adapter returned nil handle", key)
	}
	return StartedAdapter{Key: key, Handle: handle}, nil
}
