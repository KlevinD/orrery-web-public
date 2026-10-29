package platform_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"orrery_v2/internal/platform"
	"orrery_v2/internal/platform/db"
)

func TestRuntimeReloadFromFileUpdatesConfigSnapshotWithoutRebuildingInfrastructure(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	configPath := filepath.Join(root, "orrery.yaml")
	writeRuntimeYAML(t, configPath, ".", "./artifacts-a", "./logs/a.jsonl", "1ms")

	rt, err := platform.OpenFromYAML(ctx, configPath)
	if err != nil {
		t.Fatalf("OpenFromYAML returned error: %v", err)
	}
	defer rt.Close()

	rt.Logger.Info(ctx, "before reload")
	if _, err := os.Stat(filepath.Join(root, "logs", "a.jsonl")); err != nil {
		t.Fatalf("initial log file missing: %v", err)
	}

	writeRuntimeYAML(t, configPath, ".", "./artifacts-b", "./logs/b.jsonl", "2ms")
	if err := rt.ReloadFromFile(ctx, configPath); err != nil {
		t.Fatalf("ReloadFromFile returned error: %v", err)
	}

	if got, ok := rt.ConfigManager.String("filestore.root"); !ok || got != filepath.Join(root, "artifacts-b") {
		t.Fatalf("ConfigManager filestore.root = %q ok=%v", got, ok)
	}
	if rt.Config.FileStore.Root != filepath.Join(root, "artifacts-b") {
		t.Fatalf("Runtime config snapshot = %q", rt.Config.FileStore.Root)
	}
	if _, err := rt.FileStore.Put(ctx, "after.txt", "text/plain", strings.NewReader("after")); err != nil {
		t.Fatalf("FileStore.Put after reload returned error: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "artifacts-a")); err != nil {
		t.Fatalf("existing filestore root missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "artifacts-b")); err == nil {
		t.Fatalf("default config reload rebuilt filestore root")
	}
	rt.Logger.Info(ctx, "after config-only reload")
	if _, err := os.Stat(filepath.Join(root, "logs", "b.jsonl")); err == nil {
		t.Fatalf("default config reload rebuilt logger")
	}
	if rt.Health.Ready(ctx).CheckedAt.IsZero() {
		t.Fatalf("health checker not available after reload")
	}
}

func TestRuntimeReloadComponentsFromCurrentConfigExplicitlyRebuildsInfrastructure(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	configPath := filepath.Join(root, "orrery.yaml")
	writeRuntimeYAML(t, configPath, ".", "./artifacts-a", "./logs/a.jsonl", "1ms")

	rt, err := platform.OpenFromYAML(ctx, configPath)
	if err != nil {
		t.Fatalf("OpenFromYAML returned error: %v", err)
	}
	defer rt.Close()

	writeRuntimeYAML(t, configPath, ".", "./artifacts-b", "./logs/b.jsonl", "2ms")
	if err := rt.ReloadFromFile(ctx, configPath); err != nil {
		t.Fatalf("ReloadFromFile returned error: %v", err)
	}
	if err := rt.ReloadComponents(ctx); err != nil {
		t.Fatalf("ReloadComponents returned error: %v", err)
	}

	rt.Logger.Info(ctx, "after component reload")
	if _, err := os.Stat(filepath.Join(root, "logs", "b.jsonl")); err != nil {
		t.Fatalf("reloaded log file missing: %v", err)
	}
	if _, err := rt.FileStore.Put(ctx, "after.txt", "text/plain", strings.NewReader("after")); err != nil {
		t.Fatalf("FileStore.Put after component reload returned error: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "artifacts-b")); err != nil {
		t.Fatalf("reloaded filestore root missing: %v", err)
	}
}

func TestRuntimeReloadFromFileReportsRestartRequiredForDatabaseURLChanges(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	configPath := filepath.Join(root, "orrery.yaml")
	writeRuntimeYAML(t, configPath, ".", "./artifacts-a", "./logs/a.jsonl", "1ms")

	rt, err := platform.OpenFromYAML(ctx, configPath)
	if err != nil {
		t.Fatalf("OpenFromYAML returned error: %v", err)
	}
	defer rt.Close()

	writeRuntimeYAMLWithDatabaseRoot(t, configPath, ".", "./replacement-data", "./artifacts-a", "./logs/a.jsonl", "1ms")
	if err := rt.ReloadFromFile(ctx, configPath); err != nil {
		t.Fatalf("ReloadFromFile returned error: %v", err)
	}

	wantNewEventsURL := "file://" + filepath.Join(root, "replacement-data", "events.db")
	if got, ok := rt.ConfigManager.String("database.urls.events"); !ok || got != wantNewEventsURL {
		t.Fatalf("database.urls.events = %q ok=%v, want %q", got, ok, wantNewEventsURL)
	}
	if err := rt.ConfigManager.LastError(); err == nil || !strings.Contains(err.Error(), "restart required") {
		t.Fatalf("LastError = %v, want restart required", err)
	}
	report := rt.Health.Ready(ctx)
	if report.OK {
		t.Fatalf("ready report OK after static DB config drift: %+v", report)
	}
	var found bool
	for _, check := range report.Checks {
		if check.Name == "runtime.restart_required" && !check.OK && strings.Contains(check.Message, "database.urls.events") {
			found = true
		}
	}
	if !found {
		t.Fatalf("ready report missing restart_required database drift check: %+v", report.Checks)
	}
	if err := rt.DBRouter.PutRecord(ctx, "events.event_log", "event-1", db.Record{"kind": "event"}); err != nil {
		t.Fatalf("existing routed db write returned error: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "replacement-data", "events.db")); err == nil {
		t.Fatalf("config-only reload opened replacement events database")
	}
}

func TestRuntimeReloadFromFileKeepsLastKnownGoodInfrastructureOnInvalidConfig(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	configPath := filepath.Join(root, "orrery.yaml")
	writeRuntimeYAML(t, configPath, ".", "./artifacts-a", "./logs/a.jsonl", "1ms")

	rt, err := platform.OpenFromYAML(ctx, configPath)
	if err != nil {
		t.Fatalf("OpenFromYAML returned error: %v", err)
	}
	defer rt.Close()

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
  root: ./artifacts-b
logging:
  console: false
  file: ./logs/b.jsonl
health:
  readiness_cache_ttl: -1ms
`), 0o600); err != nil {
		t.Fatalf("write invalid yaml: %v", err)
	}

	if err := rt.ReloadFromFile(ctx, configPath); err == nil {
		t.Fatalf("ReloadFromFile returned nil error for invalid config")
	}
	if rt.Config.FileStore.Root != filepath.Join(root, "artifacts-a") {
		t.Fatalf("runtime changed config after invalid reload: %q", rt.Config.FileStore.Root)
	}
	rt.Logger.Info(ctx, "after failed reload")
	if _, err := os.Stat(filepath.Join(root, "logs", "a.jsonl")); err != nil {
		t.Fatalf("last known-good log file missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "logs", "b.jsonl")); err == nil {
		t.Fatalf("invalid reload created new log file")
	}
	if err := rt.ConfigManager.LastError(); err == nil {
		t.Fatalf("ConfigManager.LastError is nil after invalid reload")
	}
}

func TestRuntimeReloadFromFileKeepsLastKnownGoodWhenComponentAssemblyFails(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	configPath := filepath.Join(root, "orrery.yaml")
	writeRuntimeYAML(t, configPath, ".", "./artifacts-a", "./logs/a.jsonl", "1ms")

	rt, err := platform.OpenFromYAML(ctx, configPath)
	if err != nil {
		t.Fatalf("OpenFromYAML returned error: %v", err)
	}
	defer rt.Close()

	badLogPath := filepath.Join(root, "logs-as-file")
	if err := os.WriteFile(badLogPath, []byte("not a directory"), 0o600); err != nil {
		t.Fatalf("write bad log path: %v", err)
	}
	writeRuntimeYAML(t, configPath, ".", "./artifacts-b", "./logs-as-file/b.jsonl", "2ms")

	if err := rt.ReloadFromFile(ctx, configPath); err != nil {
		t.Fatalf("ReloadFromFile returned error for config-only reload: %v", err)
	}
	if err := rt.ReloadComponents(ctx); err == nil {
		t.Fatalf("ReloadComponents returned nil error for invalid runtime assembly")
	}
	if rt.Config.FileStore.Root != filepath.Join(root, "artifacts-b") {
		t.Fatalf("runtime did not keep latest config snapshot: %q", rt.Config.FileStore.Root)
	}
	if rt.ConfigManager.Current().FileStore.Root != filepath.Join(root, "artifacts-b") {
		t.Fatalf("config manager did not publish valid config snapshot: %q", rt.ConfigManager.Current().FileStore.Root)
	}
	rt.Logger.Info(ctx, "after failed assembly reload")
	if _, err := os.Stat(filepath.Join(root, "logs", "a.jsonl")); err != nil {
		t.Fatalf("last known-good logger missing: %v", err)
	}
	if err := rt.ConfigManager.LastError(); err == nil {
		t.Fatalf("ConfigManager.LastError is nil after failed assembly")
	}
}

func TestRuntimeReloadComponentsClearsRecoverableAssemblyError(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	configPath := filepath.Join(root, "orrery.yaml")
	writeRuntimeYAML(t, configPath, ".", "./artifacts-a", "./logs/a.jsonl", "1ms")

	rt, err := platform.OpenFromYAML(ctx, configPath)
	if err != nil {
		t.Fatalf("OpenFromYAML returned error: %v", err)
	}
	defer rt.Close()

	badLogPath := filepath.Join(root, "logs-as-file")
	if err := os.WriteFile(badLogPath, []byte("not a directory"), 0o600); err != nil {
		t.Fatalf("write bad log path: %v", err)
	}
	writeRuntimeYAML(t, configPath, ".", "./artifacts-b", "./logs-as-file/b.jsonl", "1ms")
	if err := rt.ReloadFromFile(ctx, configPath); err != nil {
		t.Fatalf("ReloadFromFile returned error: %v", err)
	}
	if err := rt.ReloadComponents(ctx); err == nil {
		t.Fatalf("ReloadComponents returned nil error for invalid log path")
	}
	if rt.ConfigManager.LastError() == nil {
		t.Fatalf("LastError is nil after failed component reload")
	}

	if err := os.Remove(badLogPath); err != nil {
		t.Fatalf("remove bad log path: %v", err)
	}
	if err := rt.ReloadComponents(ctx); err != nil {
		t.Fatalf("ReloadComponents after fixing path returned error: %v", err)
	}
	if err := rt.ConfigManager.LastError(); err != nil {
		t.Fatalf("LastError = %v, want nil after recovered component reload", err)
	}
}

func TestRuntimeReloadComponentsDoesNotClearRestartRequiredDrift(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	configPath := filepath.Join(root, "orrery.yaml")
	writeRuntimeYAML(t, configPath, ".", "./artifacts-a", "./logs/a.jsonl", "1ms")

	rt, err := platform.OpenFromYAML(ctx, configPath)
	if err != nil {
		t.Fatalf("OpenFromYAML returned error: %v", err)
	}
	defer rt.Close()

	writeRuntimeYAMLWithDatabaseRoot(t, configPath, ".", "./replacement-data", "./artifacts-a", "./logs/a.jsonl", "1ms")
	if err := rt.ReloadFromFile(ctx, configPath); err != nil {
		t.Fatalf("ReloadFromFile returned error: %v", err)
	}
	if err := rt.ReloadComponents(ctx); err != nil {
		t.Fatalf("ReloadComponents returned error: %v", err)
	}
	if err := rt.ConfigManager.LastError(); err == nil || !strings.Contains(err.Error(), "restart required") {
		t.Fatalf("LastError = %v, want restart required after component reload", err)
	}
}

func TestRuntimeWatchYAMLReloadsThroughRuntimePath(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	root := t.TempDir()
	configPath := filepath.Join(root, "orrery.yaml")
	writeRuntimeYAML(t, configPath, ".", "./artifacts-a", "./logs/a.jsonl", "1ms")

	rt, err := platform.OpenFromYAML(ctx, configPath)
	if err != nil {
		t.Fatalf("OpenFromYAML returned error: %v", err)
	}
	defer rt.Close()
	stop := rt.WatchYAML(ctx, configPath, 10*time.Millisecond)
	defer stop()

	time.Sleep(20 * time.Millisecond)
	writeRuntimeYAMLWithDatabaseRoot(t, configPath, ".", "./replacement-data", "./artifacts-b", "./logs/b.jsonl", "1ms")

	waitFor(t, 2*time.Second, func() bool {
		got, ok := rt.ConfigManager.String("filestore.root")
		if !ok || got != filepath.Join(root, "artifacts-b") {
			return false
		}
		err := rt.ConfigManager.LastError()
		return err != nil && strings.Contains(err.Error(), "restart required")
	})
	if err := rt.ConfigManager.LastError(); err == nil || !strings.Contains(err.Error(), "restart required") {
		t.Fatalf("LastError = %v, want restart required from runtime watcher path", err)
	}
	if _, err := os.Stat(filepath.Join(root, "replacement-data", "events.db")); err == nil {
		t.Fatalf("runtime watcher opened replacement events database")
	}
}

func TestRuntimeWatchYAMLStopPreventsFurtherReloads(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	configPath := filepath.Join(root, "orrery.yaml")
	writeRuntimeYAML(t, configPath, ".", "./artifacts-a", "./logs/a.jsonl", "1ms")

	rt, err := platform.OpenFromYAML(ctx, configPath)
	if err != nil {
		t.Fatalf("OpenFromYAML returned error: %v", err)
	}
	defer rt.Close()
	stop := rt.WatchYAML(ctx, configPath, 10*time.Millisecond)
	stop()

	writeRuntimeYAML(t, configPath, ".", "./artifacts-b", "./logs/b.jsonl", "1ms")
	time.Sleep(50 * time.Millisecond)
	if got, _ := rt.ConfigManager.String("filestore.root"); got != filepath.Join(root, "artifacts-a") {
		t.Fatalf("watcher reloaded after stop: filestore.root=%q", got)
	}
}

func TestRuntimeCloseStopsYAMLWatchers(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	configPath := filepath.Join(root, "orrery.yaml")
	writeRuntimeYAML(t, configPath, ".", "./artifacts-a", "./logs/a.jsonl", "1ms")

	rt, err := platform.OpenFromYAML(ctx, configPath)
	if err != nil {
		t.Fatalf("OpenFromYAML returned error: %v", err)
	}
	_ = rt.WatchYAML(ctx, configPath, 10*time.Millisecond)
	time.Sleep(20 * time.Millisecond)

	if err := rt.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}
	writeRuntimeYAML(t, configPath, ".", "./artifacts-b", "./logs/b.jsonl", "1ms")
	time.Sleep(50 * time.Millisecond)

	if got, _ := rt.ConfigManager.String("filestore.root"); got != filepath.Join(root, "artifacts-a") {
		t.Fatalf("watcher reloaded after Runtime.Close: filestore.root=%q", got)
	}
	if err := rt.Close(); err != nil {
		t.Fatalf("second Close returned error: %v", err)
	}
}

func TestRuntimeRejectsReloadAfterClose(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	configPath := filepath.Join(root, "orrery.yaml")
	writeRuntimeYAML(t, configPath, ".", "./artifacts-a", "./logs/a.jsonl", "1ms")

	rt, err := platform.OpenFromYAML(ctx, configPath)
	if err != nil {
		t.Fatalf("OpenFromYAML returned error: %v", err)
	}
	if err := rt.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}
	writeRuntimeYAML(t, configPath, ".", "./artifacts-b", "./logs/b.jsonl", "1ms")

	if err := rt.ReloadFromFile(ctx, configPath); err == nil || !strings.Contains(err.Error(), "runtime is closed") {
		t.Fatalf("ReloadFromFile after Close error = %v, want runtime is closed", err)
	}
	if err := rt.ReloadComponents(ctx); err == nil || !strings.Contains(err.Error(), "runtime is closed") {
		t.Fatalf("ReloadComponents after Close error = %v, want runtime is closed", err)
	}
	if got, _ := rt.ConfigManager.String("filestore.root"); got != filepath.Join(root, "artifacts-a") {
		t.Fatalf("closed runtime published config reload: filestore.root=%q", got)
	}
	if rt.Config.FileStore.Root != filepath.Join(root, "artifacts-a") {
		t.Fatalf("closed runtime changed config snapshot: %q", rt.Config.FileStore.Root)
	}
}

func TestRuntimeWatchYAMLKeepsLastKnownGoodAndRecoversAfterInvalidYAML(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	root := t.TempDir()
	configPath := filepath.Join(root, "orrery.yaml")
	writeRuntimeYAML(t, configPath, ".", "./artifacts-a", "./logs/a.jsonl", "1ms")

	rt, err := platform.OpenFromYAML(ctx, configPath)
	if err != nil {
		t.Fatalf("OpenFromYAML returned error: %v", err)
	}
	defer rt.Close()
	stop := rt.WatchYAML(ctx, configPath, 10*time.Millisecond)
	defer stop()

	time.Sleep(20 * time.Millisecond)
	if err := os.WriteFile(configPath, []byte("health:\n  readiness_cache_ttl: not-a-duration\n"), 0o600); err != nil {
		t.Fatalf("write invalid yaml: %v", err)
	}
	waitFor(t, 2*time.Second, func() bool {
		return rt.ConfigManager.LastError() != nil
	})
	if got, _ := rt.ConfigManager.String("filestore.root"); got != filepath.Join(root, "artifacts-a") {
		t.Fatalf("invalid watched reload changed config: %q", got)
	}

	writeRuntimeYAML(t, configPath, ".", "./artifacts-c", "./logs/c.jsonl", "1ms")
	waitFor(t, 2*time.Second, func() bool {
		got, ok := rt.ConfigManager.String("filestore.root")
		return ok && got == filepath.Join(root, "artifacts-c") && rt.ConfigManager.LastError() == nil
	})
}
