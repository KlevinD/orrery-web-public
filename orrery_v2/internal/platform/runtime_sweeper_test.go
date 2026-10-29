package platform_test

import (
	"context"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"orrery_v2/internal/platform"
	"orrery_v2/internal/platform/config"
	"orrery_v2/internal/platform/health"
)

func TestRuntimeRunsRegisteredSweeperFromModuleAndStopsOnClose(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	configPath := filepath.Join(root, "orrery.yaml")
	writeRuntimeYAML(t, configPath, ".", "./artifacts", "./logs/platform.jsonl", "1ms")
	cfg, err := config.LoadYAML(configPath)
	if err != nil {
		t.Fatalf("LoadYAML returned error: %v", err)
	}

	var sweeps atomic.Int32
	module := platform.ModuleFunc(func(ctx context.Context, registrar *platform.ModuleRegistrar) error {
		return registrar.RegisterSweeper("business.cleanup", platform.SweeperFunc(func(ctx context.Context) error {
			sweeps.Add(1)
			return nil
		}))
	})

	rt, err := platform.OpenWithModules(ctx, cfg, module)
	if err != nil {
		t.Fatalf("OpenWithModules returned error: %v", err)
	}

	if err := rt.StartSweeper(ctx, platform.SweeperRuntimeConfig{Interval: time.Millisecond}); err != nil {
		t.Fatalf("StartSweeper returned error: %v", err)
	}
	waitFor(t, time.Second, func() bool {
		return sweeps.Load() > 0
	})
	if err := rt.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}
	afterClose := sweeps.Load()
	time.Sleep(30 * time.Millisecond)
	if sweeps.Load() != afterClose {
		t.Fatalf("registered sweeper kept running after Runtime.Close")
	}
}

func TestRuntimeManagedSweeperHonorsInitialDelay(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	configPath := filepath.Join(root, "orrery.yaml")
	writeRuntimeYAML(t, configPath, ".", "./artifacts", "./logs/platform.jsonl", "1ms")
	cfg, err := config.LoadYAML(configPath)
	if err != nil {
		t.Fatalf("LoadYAML returned error: %v", err)
	}

	var sweeps atomic.Int32
	module := platform.ModuleFunc(func(ctx context.Context, registrar *platform.ModuleRegistrar) error {
		return registrar.RegisterSweeper("business.delayed", platform.SweeperFunc(func(ctx context.Context) error {
			sweeps.Add(1)
			return nil
		}))
	})
	rt, err := platform.OpenWithModules(ctx, cfg, module)
	if err != nil {
		t.Fatalf("OpenWithModules returned error: %v", err)
	}
	defer rt.Close()

	if err := rt.StartSweeper(ctx, platform.SweeperRuntimeConfig{
		Interval: time.Millisecond, InitialDelay: 150 * time.Millisecond,
	}); err != nil {
		t.Fatalf("StartSweeper returned error: %v", err)
	}
	time.Sleep(30 * time.Millisecond)
	if got := sweeps.Load(); got != 0 {
		t.Fatalf("sweeps before initial delay = %d, want 0", got)
	}
	waitFor(t, time.Second, func() bool {
		return sweeps.Load() > 0
	})
}

func TestRuntimeReloadComponentsKeepsRegisteredSweepers(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	configPath := filepath.Join(root, "orrery.yaml")
	writeRuntimeYAML(t, configPath, ".", "./artifacts-a", "./logs/a.jsonl", "1ms")
	cfg, err := config.LoadYAML(configPath)
	if err != nil {
		t.Fatalf("LoadYAML returned error: %v", err)
	}

	var sweeps atomic.Int32
	module := platform.ModuleFunc(func(ctx context.Context, registrar *platform.ModuleRegistrar) error {
		return registrar.RegisterSweeper("business.cleanup", platform.SweeperFunc(func(ctx context.Context) error {
			sweeps.Add(1)
			return nil
		}))
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
	if err := rt.ReloadComponents(ctx); err != nil {
		t.Fatalf("ReloadComponents returned error: %v", err)
	}
	rt.Sweeper.SweepOnce(ctx)
	if sweeps.Load() == 0 {
		t.Fatalf("registered sweeper was not preserved after component reload")
	}
}

func TestRuntimeManagedSweeperUsesReloadedSweeperRunner(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	configPath := filepath.Join(root, "orrery.yaml")
	writeRuntimeYAMLWithSweeperBatchSize(t, configPath, ".", "./artifacts-a", "./logs/a.jsonl", "5")
	cfg, err := config.LoadYAML(configPath)
	if err != nil {
		t.Fatalf("LoadYAML returned error: %v", err)
	}

	var first atomic.Int32
	var second atomic.Int32
	module := platform.ModuleFunc(func(ctx context.Context, registrar *platform.ModuleRegistrar) error {
		if err := registrar.RegisterSweeper("business.first", platform.SweeperFunc(func(ctx context.Context) error {
			first.Add(1)
			return nil
		})); err != nil {
			return err
		}
		return registrar.RegisterSweeper("business.second", platform.SweeperFunc(func(ctx context.Context) error {
			second.Add(1)
			return nil
		}))
	})
	rt, err := platform.OpenWithModules(ctx, cfg, module)
	if err != nil {
		t.Fatalf("OpenWithModules returned error: %v", err)
	}
	defer rt.Close()

	if err := rt.StartSweeper(ctx, platform.SweeperRuntimeConfig{Interval: time.Millisecond}); err != nil {
		t.Fatalf("StartSweeper returned error: %v", err)
	}
	waitFor(t, time.Second, func() bool {
		return first.Load() > 0
	})
	if second.Load() != 0 {
		t.Fatalf("second sweeper ran before batch size was reloaded")
	}

	writeRuntimeYAMLWithSweeperBatchSize(t, configPath, ".", "./artifacts-b", "./logs/b.jsonl", "6")
	if err := rt.ReloadFromFile(ctx, configPath); err != nil {
		t.Fatalf("ReloadFromFile returned error: %v", err)
	}
	if err := rt.ReloadComponents(ctx); err != nil {
		t.Fatalf("ReloadComponents returned error: %v", err)
	}
	waitFor(t, time.Second, func() bool {
		return second.Load() > 0
	})
}

func TestRuntimeManagedSweeperSurvivesRegisteredSweeperPanic(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	configPath := filepath.Join(root, "orrery.yaml")
	writeRuntimeYAMLWithSweeperBatchSize(t, configPath, ".", "./artifacts", "./logs/platform.jsonl", "6")
	cfg, err := config.LoadYAML(configPath)
	if err != nil {
		t.Fatalf("LoadYAML returned error: %v", err)
	}

	var healthy atomic.Int32
	module := platform.ModuleFunc(func(ctx context.Context, registrar *platform.ModuleRegistrar) error {
		if err := registrar.RegisterSweeper("business.panic", platform.SweeperFunc(func(ctx context.Context) error {
			panic("business cleanup failed")
		})); err != nil {
			return err
		}
		return registrar.RegisterSweeper("business.healthy", platform.SweeperFunc(func(ctx context.Context) error {
			healthy.Add(1)
			return nil
		}))
	})
	rt, err := platform.OpenWithModules(ctx, cfg, module)
	if err != nil {
		t.Fatalf("OpenWithModules returned error: %v", err)
	}
	defer rt.Close()

	if err := rt.StartSweeper(ctx, platform.SweeperRuntimeConfig{Interval: time.Millisecond}); err != nil {
		t.Fatalf("StartSweeper returned error: %v", err)
	}
	waitFor(t, time.Second, func() bool {
		return healthy.Load() > 0
	})
}

func TestRuntimeReadyReportsManagedSweeperUnexpectedExit(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	configPath := filepath.Join(root, "orrery.yaml")
	writeRuntimeYAMLWithSweeperBatchSize(t, configPath, ".", "./artifacts", "./logs/platform.jsonl", "6")

	rt, err := platform.OpenFromYAML(ctx, configPath)
	if err != nil {
		t.Fatalf("OpenFromYAML returned error: %v", err)
	}
	defer rt.Close()

	runCtx, cancel := context.WithCancel(ctx)
	if err := rt.StartSweeper(runCtx, platform.SweeperRuntimeConfig{Interval: time.Millisecond}); err != nil {
		t.Fatalf("StartSweeper returned error: %v", err)
	}
	cancel()
	waitFor(t, time.Second, func() bool {
		_, ok := findReadyCheck(rt.Health.Ready(ctx), "runtime.worker.sweeper")
		return ok
	})

	ready := rt.Health.Ready(ctx)
	if check, ok := findReadyCheck(ready, "runtime.worker.sweeper"); !ok || check.OK {
		t.Fatalf("runtime.worker.sweeper check = %+v ok=%v, want not ready", check, ok)
	}
}

func writeRuntimeYAMLWithSweeperBatchSize(t *testing.T, path, baseDir, filestoreRoot, logFile, batchSize string) {
	t.Helper()
	writeRuntimeYAML(t, path, baseDir, filestoreRoot, logFile, "1ms")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read runtime yaml: %v", err)
	}
	raw = append(raw, []byte("\nsweeper:\n  timeout: 1s\n  batch_size: "+batchSize+"\n")...)
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatalf("write runtime yaml sweeper: %v", err)
	}
}

func findReadyCheck(report health.Report, name string) (health.Check, bool) {
	for _, check := range report.Checks {
		if check.Name == name {
			return check, true
		}
	}
	return health.Check{}, false
}
