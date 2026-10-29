package channel

import (
	"context"
	"testing"

	gwconfig "orrery_v2/internal/gateway/config"
)

type fakeAdapter struct {
	key     string
	started bool
	handle  Handle
}

func (f *fakeAdapter) Key() string { return f.key }

func (f *fakeAdapter) Start(context.Context, StartDeps) (Handle, error) {
	f.started = true
	return f.handle, nil
}

type noopHandle struct{}

func (noopHandle) Stop(context.Context, string) error { return nil }

func TestRegistryStartsRegisteredPlatformAdapter(t *testing.T) {
	adapter := &fakeAdapter{key: "dingtalk", handle: noopHandle{}}
	registry := NewRegistry()
	registry.Register("dingtalk", func(gwconfig.PlatformAdapterConfig) Adapter { return adapter })

	started, err := registry.StartPlatform(context.Background(), "dingtalk", gwconfig.PlatformAdapterConfig{Enabled: true}, StartDeps{})
	if err != nil {
		t.Fatalf("StartPlatform failed: %v", err)
	}
	if !adapter.started || started.Key != "dingtalk" || started.Handle == nil {
		t.Fatalf("started adapter = %+v", started)
	}
}

func TestRegistryRejectsUnregisteredPlatformAdapter(t *testing.T) {
	registry := NewRegistry()
	if _, err := registry.StartPlatform(context.Background(), "telegram", gwconfig.PlatformAdapterConfig{}, StartDeps{}); err == nil {
		t.Fatal("StartPlatform accepted an unregistered adapter")
	}
}

func TestRegistryValidateEnabledRejectsMissingProductionAdapter(t *testing.T) {
	registry := NewRegistry()
	cfg := gwconfig.Config{}
	cfg.Adapters.DingTalk.Enabled = true

	if err := registry.ValidateEnabled(cfg); err == nil {
		t.Fatal("ValidateEnabled error = nil, want missing adapter error")
	}
}

func TestRegistryRejectsNilPlatformHandle(t *testing.T) {
	registry := NewRegistry()
	registry.Register("dingtalk", func(gwconfig.PlatformAdapterConfig) Adapter {
		return &fakeAdapter{key: "dingtalk"}
	})
	if _, err := registry.StartPlatform(context.Background(), "dingtalk", gwconfig.PlatformAdapterConfig{}, StartDeps{}); err == nil {
		t.Fatal("StartPlatform accepted a nil runtime handle")
	}
}

func TestRegistryStartsDedicatedWeixinAdapter(t *testing.T) {
	adapter := &fakeAdapter{key: "weixin", handle: noopHandle{}}
	registry := NewRegistry()
	registry.RegisterWeixin(func(cfg gwconfig.WeixinAdapterConfig) Adapter {
		if cfg.Extra["pollIntervalSeconds"] != 3 {
			t.Fatalf("weixin config = %+v", cfg)
		}
		return adapter
	})

	started, err := registry.StartWeixin(context.Background(), gwconfig.WeixinAdapterConfig{
		Enabled: true,
		Extra:   map[string]any{"pollIntervalSeconds": 3},
	}, StartDeps{})
	if err != nil {
		t.Fatalf("StartWeixin failed: %v", err)
	}
	if !adapter.started || started.Key != "weixin" || started.Handle == nil {
		t.Fatalf("started adapter = %+v", started)
	}
}
