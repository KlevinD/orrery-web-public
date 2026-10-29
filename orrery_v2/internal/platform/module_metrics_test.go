package platform_test

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"orrery_v2/internal/platform"
	"orrery_v2/internal/platform/config"
)

func TestBusinessModuleCapturesMetricsThroughRegistrarRecorder(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	configPath := filepath.Join(root, "orrery.yaml")
	writeRuntimeYAML(t, configPath, ".", "./artifacts", "./logs/platform.jsonl", "1ms")
	cfg, err := config.LoadYAML(configPath)
	if err != nil {
		t.Fatalf("LoadYAML returned error: %v", err)
	}

	module := platform.ModuleFunc(func(ctx context.Context, registry *platform.ModuleRegistrar) error {
		if ok := registry.MetricRecorder().Capture("business.worker.started", map[string]any{
			"worker_id": "worker-a",
			"role":      "indexer",
		}); !ok {
			t.Fatalf("metric recorder dropped first event")
		}
		return nil
	})

	rt, err := platform.OpenWithModules(ctx, cfg, module)
	if err != nil {
		t.Fatalf("OpenWithModules returned error: %v", err)
	}
	defer rt.Close()

	events := rt.Metrics.Drain(1)
	if len(events) != 1 || events[0].Name != "business.worker.started" || events[0].Fields["worker_id"] != "worker-a" {
		t.Fatalf("metrics events = %+v, want business worker event", events)
	}
}

func TestBusinessModuleRegistersMetricWriterAndRuntimeDrainsRelayOnClose(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	configPath := filepath.Join(root, "orrery.yaml")
	writeRuntimeYAML(t, configPath, ".", "./artifacts", "./logs/platform.jsonl", "1ms")
	cfg, err := config.LoadYAML(configPath)
	if err != nil {
		t.Fatalf("LoadYAML returned error: %v", err)
	}

	var (
		mu     sync.Mutex
		events []platform.MetricEvent
	)
	module := platform.ModuleFunc(func(ctx context.Context, registry *platform.ModuleRegistrar) error {
		if err := registry.RegisterMetricEventWriter("business.audit", platform.MetricEventWriterFunc(func(ctx context.Context, batch []platform.MetricEvent) error {
			mu.Lock()
			defer mu.Unlock()
			events = append(events, batch...)
			return nil
		})); err != nil {
			return err
		}
		registry.MetricRecorder().Capture("business.worker.started", map[string]any{
			"worker_id": "worker-a",
		})
		return nil
	})

	rt, err := platform.OpenWithModules(ctx, cfg, module)
	if err != nil {
		t.Fatalf("OpenWithModules returned error: %v", err)
	}
	if err := rt.StartMetricsRelay(ctx, platform.MetricsRelayRuntimeConfig{
		BatchSize:  10,
		FlushEvery: time.Hour,
	}); err != nil {
		_ = rt.Close()
		t.Fatalf("StartMetricsRelay returned error: %v", err)
	}
	if err := rt.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(events) != 1 || events[0].Name != "business.worker.started" || events[0].Fields["worker_id"] != "worker-a" {
		t.Fatalf("metric writer events = %+v, want business worker event", events)
	}
}

func TestRuntimeRejectsInvalidMetricWriterRegistrations(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	configPath := filepath.Join(root, "orrery.yaml")
	writeRuntimeYAML(t, configPath, ".", "./artifacts", "./logs/platform.jsonl", "1ms")
	cfg, err := config.LoadYAML(configPath)
	if err != nil {
		t.Fatalf("LoadYAML returned error: %v", err)
	}
	writer := platform.MetricEventWriterFunc(func(context.Context, []platform.MetricEvent) error {
		return nil
	})

	rt, err := platform.Open(ctx, cfg)
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	defer rt.Close()

	if err := rt.RegisterMetricEventWriter("", writer); err == nil {
		t.Fatalf("blank writer name returned nil error")
	}
	if err := rt.RegisterMetricEventWriter("audit", nil); err == nil {
		t.Fatalf("nil writer returned nil error")
	}
	if err := rt.RegisterMetricEventWriter("audit", writer); err != nil {
		t.Fatalf("RegisterMetricEventWriter returned error: %v", err)
	}
	if err := rt.RegisterMetricEventWriter("audit", writer); err == nil {
		t.Fatalf("duplicate writer returned nil error")
	}
	if err := rt.StartMetricsRelay(ctx, platform.MetricsRelayRuntimeConfig{BatchSize: 1, FlushEvery: time.Hour}); err != nil {
		t.Fatalf("StartMetricsRelay returned error: %v", err)
	}
	if err := rt.RegisterMetricEventWriter("late", writer); err == nil {
		t.Fatalf("late writer registration after relay start returned nil error")
	}
}
