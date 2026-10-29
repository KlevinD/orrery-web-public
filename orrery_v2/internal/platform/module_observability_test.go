package platform_test

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"orrery_v2/internal/platform"
	"orrery_v2/internal/platform/clock"
	"orrery_v2/internal/platform/config"
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/eventstore"
)

func TestRuntimeRegistersBusinessModuleHealthAndMetricsInOnePlace(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	configPath := filepath.Join(root, "orrery.yaml")
	if err := os.WriteFile(configPath, []byte(`
paths:
  base_dir: .
database:
  urls:
    core: file://./data/core.db
    audit: file://./data/audit.db
    events: file://./data/events.db
    jobs: file://./data/jobs.db
    runtime: file://./data/runtime.db
    observability: file://./data/observability.db
filestore:
  backend: local
  root: ./artifacts
logging:
  console: false
  file: ./logs/platform.jsonl
`), 0o600); err != nil {
		t.Fatalf("write yaml: %v", err)
	}
	cfg, err := config.LoadYAML(configPath)
	if err != nil {
		t.Fatalf("LoadYAML returned error: %v", err)
	}
	module := platform.ModuleFunc(func(ctx context.Context, registry *platform.ModuleRegistrar) error {
		if err := registry.RegisterReadyProbe("business.index", func(ctx context.Context) platform.ReadyStatus {
			return platform.ReadyStatus{OK: true}
		}); err != nil {
			return err
		}
		return registry.RegisterStatusProbe("business", "workers", "core", func(ctx context.Context) (map[string]int64, error) {
			return map[string]int64{"ready": 2, "blocked": 1}, nil
		})
	})

	rt, err := platform.OpenWithModules(ctx, cfg, module)
	if err != nil {
		t.Fatalf("OpenWithModules returned error: %v", err)
	}
	defer rt.Close()
	if err := rt.Migrations.Up(ctx); err != nil {
		t.Fatalf("migration up: %v", err)
	}

	ready := rt.Ready(ctx)
	if !ready.OK {
		t.Fatalf("ready report not ok: %+v", ready)
	}
	if !hasPlatformReadyCheck(ready, "business.index") {
		t.Fatalf("business health check missing from ready report: %+v", ready.Checks)
	}
	rec := httptest.NewRecorder()
	platform.NewMetricsHandler(rt).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body, _ := io.ReadAll(rec.Body)
	text := string(body)
	for _, want := range []string{
		`orrery_business_workers{role="core",status="blocked"} 1`,
		`orrery_business_workers{role="core",status="ready"} 2`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("metrics output missing %q: %s", want, text)
		}
	}
}

func TestBusinessModuleRegistersPullBasedConfigChangeHook(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	configPath := filepath.Join(root, "orrery.yaml")
	writeRuntimeYAML(t, configPath, ".", "./artifacts-a", "./logs/a.jsonl", "1ms")
	cfg, err := config.LoadYAML(configPath)
	if err != nil {
		t.Fatalf("LoadYAML returned error: %v", err)
	}

	seen := make(chan string, 1)
	module := platform.ModuleFunc(func(ctx context.Context, registry *platform.ModuleRegistrar) error {
		return registry.RegisterConfigChangeHook(func(version int64) {
			if version != registry.ConfigManager().Version() {
				t.Fatalf("config hook version = %d, manager version = %d", version, registry.ConfigManager().Version())
			}
			seen <- registry.ConfigManager().Current().FileStore.Root
		})
	})
	rt, err := platform.OpenWithModules(ctx, cfg, module)
	if err != nil {
		t.Fatalf("OpenWithModules returned error: %v", err)
	}
	defer rt.Close()

	writeRuntimeYAML(t, configPath, ".", "./artifacts-b", "./logs/b.jsonl", "1ms")
	if err := rt.ReloadFromFile(ctx, configPath); err != nil {
		t.Fatalf("ReloadFromFile returned error: %v", err)
	}

	select {
	case got := <-seen:
		if got != filepath.Join(root, "artifacts-b") {
			t.Fatalf("config hook pulled filestore root %q", got)
		}
	default:
		t.Fatalf("config hook was not notified")
	}
}

func TestBusinessModuleUsesRegistrarEventStore(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	configPath := filepath.Join(root, "orrery.yaml")
	writeRuntimeYAML(t, configPath, ".", "./artifacts", "./logs/platform.jsonl", "1ms")
	cfg, err := config.LoadYAML(configPath)
	if err != nil {
		t.Fatalf("LoadYAML returned error: %v", err)
	}

	createdAt := time.Date(2026, 5, 31, 23, 15, 0, 0, time.UTC)
	module := platform.ModuleFunc(func(ctx context.Context, registry *platform.ModuleRegistrar) error {
		return registry.EventStore().Append(ctx, eventstore.Envelope{
			EventID:       "business-event-1",
			StreamID:      "business-stream-1",
			StreamVersion: 1,
			EventType:     "business.created",
			EventVersion:  1,
			Payload:       []byte(`{"id":"business-1"}`),
		})
	})

	rt, err := platform.OpenWithOptionsAndModules(ctx, cfg, platform.RuntimeOptions{Clock: clock.NewFake(createdAt)}, module)
	if err != nil {
		t.Fatalf("OpenWithOptionsAndModules returned error: %v", err)
	}
	defer rt.Close()

	events, err := rt.EventStore.Load(ctx, "business-stream-1")
	if err != nil {
		t.Fatalf("load registered business event: %v", err)
	}
	if len(events) != 1 || events[0].EventID != "business-event-1" {
		t.Fatalf("events = %+v, want registered business event", events)
	}
	if !events[0].CreatedAt.Equal(createdAt) {
		t.Fatalf("CreatedAt = %s, want runtime clock %s", events[0].CreatedAt, createdAt)
	}
	if coreEvents, err := eventstore.New(mustStore(t, rt.Databases, db.RoleCore)).Load(ctx, "business-stream-1"); err != nil || len(coreEvents) != 0 {
		t.Fatalf("business event leaked into core database: events=%+v err=%v", coreEvents, err)
	}
}

func TestBusinessModuleGeneratesIDsThroughRegistrar(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	configPath := filepath.Join(root, "orrery.yaml")
	writeRuntimeYAML(t, configPath, ".", "./artifacts", "./logs/platform.jsonl", "1ms")
	cfg, err := config.LoadYAML(configPath)
	if err != nil {
		t.Fatalf("LoadYAML returned error: %v", err)
	}

	ids := make(chan string, 1)
	module := platform.ModuleFunc(func(ctx context.Context, registry *platform.ModuleRegistrar) error {
		id, err := registry.IDGenerator().NewIDContext()
		if err != nil {
			return err
		}
		ids <- id
		return nil
	})

	rt, err := platform.OpenWithModules(ctx, cfg, module)
	if err != nil {
		t.Fatalf("OpenWithModules returned error: %v", err)
	}
	defer rt.Close()

	select {
	case got := <-ids:
		if !strings.HasPrefix(got, "orrery-") {
			t.Fatalf("generated id = %q, want orrery prefix", got)
		}
	default:
		t.Fatalf("module did not generate id through registrar")
	}
}

func TestRuntimeMetricsServerIncludesBusinessModuleMetrics(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatalf("close listener: %v", err)
	}
	configPath := filepath.Join(root, "orrery.yaml")
	writeRuntimeYAMLWithMetrics(t, configPath, ".", "./artifacts", "./logs/platform.jsonl", "1ms", addr)
	cfg, err := config.LoadYAML(configPath)
	if err != nil {
		t.Fatalf("LoadYAML returned error: %v", err)
	}
	module := platform.ModuleFunc(func(ctx context.Context, registry *platform.ModuleRegistrar) error {
		return registry.RegisterStatusProbe("business", "workers", "core", func(ctx context.Context) (map[string]int64, error) {
			return map[string]int64{"ready": 3}, nil
		})
	})

	rt, err := platform.OpenWithModules(ctx, cfg, module)
	if err != nil {
		t.Fatalf("OpenWithModules returned error: %v", err)
	}
	defer rt.Close()

	resp, err := http.Get("http://" + addr + "/metrics")
	if err != nil {
		t.Fatalf("get /metrics: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	text := string(body)
	if !strings.Contains(text, `orrery_business_workers{role="core",status="ready"} 3`) {
		t.Fatalf("metrics server output missing business module metrics: %s", text)
	}
}

func TestRuntimeReloadComponentsKeepsBusinessModuleReadyChecks(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	configPath := filepath.Join(root, "orrery.yaml")
	writeRuntimeYAML(t, configPath, ".", "./artifacts-a", "./logs/a.jsonl", "1ms")
	cfg, err := config.LoadYAML(configPath)
	if err != nil {
		t.Fatalf("LoadYAML returned error: %v", err)
	}
	module := platform.ModuleFunc(func(ctx context.Context, registry *platform.ModuleRegistrar) error {
		return registry.RegisterReadyProbe("business.index", func(ctx context.Context) platform.ReadyStatus {
			return platform.ReadyStatus{OK: true}
		})
	})
	rt, err := platform.OpenWithModules(ctx, cfg, module)
	if err != nil {
		t.Fatalf("OpenWithModules returned error: %v", err)
	}
	defer rt.Close()
	if err := rt.Migrations.Up(ctx); err != nil {
		t.Fatalf("migration up: %v", err)
	}
	if !hasPlatformReadyCheck(rt.Ready(ctx), "business.index") {
		t.Fatalf("business ready check missing before reload")
	}

	writeRuntimeYAML(t, configPath, ".", "./artifacts-b", "./logs/b.jsonl", "1ms")
	if err := rt.ReloadFromFile(ctx, configPath); err != nil {
		t.Fatalf("ReloadFromFile returned error: %v", err)
	}
	if err := rt.ReloadComponents(ctx); err != nil {
		t.Fatalf("ReloadComponents returned error: %v", err)
	}

	ready := rt.Ready(ctx)
	if !hasPlatformReadyCheck(ready, "business.index") {
		t.Fatalf("business ready check missing after component reload: %+v", ready.Checks)
	}
}
