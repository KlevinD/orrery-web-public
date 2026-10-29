package platform_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"orrery_v2/internal/platform"
	"orrery_v2/internal/platform/clock"
	"orrery_v2/internal/platform/config"
	"orrery_v2/internal/platform/execution"
)

func TestRuntimeAssemblesLockStoreFromRuntimeDatabase(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	configPath := filepath.Join(root, "orrery.yaml")
	writeRuntimeYAML(t, configPath, ".", "./artifacts", "./logs/platform.jsonl", "1ms")

	rt, err := platform.OpenFromYAML(ctx, configPath)
	if err != nil {
		t.Fatalf("OpenFromYAML returned error: %v", err)
	}
	defer rt.Close()

	lease, ok, err := rt.Lock.TryAcquire(ctx, "runtime-resource", "worker-a", rt.Config.Lock.DefaultTTL, time.Now())
	if err != nil || !ok || lease == nil {
		t.Fatalf("runtime lock acquire ok=%v lease=%v err=%v", ok, lease, err)
	}
}

func TestRuntimeRegistersPlatformHousekeepingSweepers(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	configPath := filepath.Join(root, "orrery.yaml")
	writeRuntimeYAMLWithSweeperBatchSize(t, configPath, ".", "./artifacts", "./logs/platform.jsonl", "10")
	cfg, err := config.LoadYAML(configPath)
	if err != nil {
		t.Fatalf("LoadYAML returned error: %v", err)
	}

	rt, err := platform.Open(ctx, cfg)
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	defer rt.Close()

	now := time.Now()
	if _, ok, err := rt.Lock.TryAcquire(ctx, "expired-lock", "worker-a", time.Millisecond, now.Add(-time.Second)); err != nil || !ok {
		t.Fatalf("acquire expired lock fixture ok=%v err=%v", ok, err)
	}
	if ok, err := rt.Execution.Claims.Acquire(ctx, "expired-claim", "worker-a", time.Millisecond, now.Add(-time.Second)); err != nil || !ok {
		t.Fatalf("acquire expired claim fixture ok=%v err=%v", ok, err)
	}
	if err := rt.Execution.Liveness.TouchHeartbeat(ctx, "expired-runtime", time.Millisecond, now.Add(-time.Second)); err != nil {
		t.Fatalf("touch expired liveness fixture: %v", err)
	}
	if err := rt.Execution.Queue.Enqueue(ctx, execution.Job{ID: "expired-dispatched", Topic: "business"}, now.Add(-time.Minute)); err != nil {
		t.Fatalf("enqueue expired queue fixture: %v", err)
	}
	if _, ok, err := rt.Execution.Queue.Claim(ctx, "worker-a", now.Add(-time.Minute)); err != nil || !ok {
		t.Fatalf("claim expired queue fixture ok=%v err=%v", ok, err)
	}

	rt.Sweeper.SweepOnce(ctx)

	if _, ok, err := rt.Lock.TryAcquire(ctx, "expired-lock", "worker-b", rt.Config.Lock.DefaultTTL, now); err != nil || !ok {
		t.Fatalf("expired lock was not swept ok=%v err=%v", ok, err)
	}
	if ok, err := rt.Execution.Claims.Acquire(ctx, "expired-claim", "worker-b", rt.Config.Queue.ClaimTTL, now); err != nil || !ok {
		t.Fatalf("expired claim was not recovered ok=%v err=%v", ok, err)
	}
	if alive := rt.Execution.Liveness.IsAliveBatch(ctx, []string{"expired-runtime"}, now)["expired-runtime"]; alive {
		t.Fatalf("expired liveness heartbeat was not swept")
	}
	job, ok, err := rt.Execution.Queue.Get(ctx, "expired-dispatched")
	if err != nil || !ok {
		t.Fatalf("get expired queue job ok=%v err=%v", ok, err)
	}
	if job.Status != execution.StatusQueued {
		t.Fatalf("expired dispatched queue job status = %s, want queued after recovery", job.Status)
	}
}

func TestRuntimePlatformHousekeepingUsesRuntimeClock(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	configPath := filepath.Join(root, "orrery.yaml")
	writeRuntimeYAMLWithSweeperBatchSize(t, configPath, ".", "./artifacts", "./logs/platform.jsonl", "10")
	cfg, err := config.LoadYAML(configPath)
	if err != nil {
		t.Fatalf("LoadYAML returned error: %v", err)
	}

	now := time.Date(2099, 5, 31, 16, 0, 0, 0, time.UTC)
	rt, err := platform.OpenWithOptions(ctx, cfg, platform.RuntimeOptions{
		Clock: clock.NewFake(now),
	})
	if err != nil {
		t.Fatalf("OpenWithOptions returned error: %v", err)
	}
	defer rt.Close()

	acquiredAt := now.Add(-time.Hour)
	probeBeforeExpiry := now.Add(-45 * time.Minute)
	if _, ok, err := rt.Lock.TryAcquire(ctx, "clocked-lock", "worker-a", 30*time.Minute, acquiredAt); err != nil || !ok {
		t.Fatalf("acquire expired lock fixture ok=%v err=%v", ok, err)
	}
	rt.Sweeper.SweepOnce(ctx)
	if _, ok, err := rt.Lock.TryAcquire(ctx, "clocked-lock", "worker-b", rt.Config.Lock.DefaultTTL, probeBeforeExpiry); err != nil || !ok {
		t.Fatalf("housekeeping did not use runtime clock for expired lock ok=%v err=%v", ok, err)
	}
}

func TestRuntimeReloadComponentsKeepsPlatformHousekeepingSweepers(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	configPath := filepath.Join(root, "orrery.yaml")
	writeRuntimeYAMLWithSweeperBatchSize(t, configPath, ".", "./artifacts-a", "./logs/a.jsonl", "10")

	rt, err := platform.OpenFromYAML(ctx, configPath)
	if err != nil {
		t.Fatalf("OpenFromYAML returned error: %v", err)
	}
	defer rt.Close()

	writeRuntimeYAMLWithSweeperBatchSize(t, configPath, ".", "./artifacts-b", "./logs/b.jsonl", "10")
	if err := rt.ReloadFromFile(ctx, configPath); err != nil {
		t.Fatalf("ReloadFromFile returned error: %v", err)
	}
	if err := rt.ReloadComponents(ctx); err != nil {
		t.Fatalf("ReloadComponents returned error: %v", err)
	}

	now := time.Now()
	if ok, err := rt.Execution.Claims.Acquire(ctx, "expired-after-reload", "worker-a", time.Millisecond, now.Add(-time.Second)); err != nil || !ok {
		t.Fatalf("acquire expired claim fixture ok=%v err=%v", ok, err)
	}
	rt.Sweeper.SweepOnce(ctx)
	if ok, err := rt.Execution.Claims.Acquire(ctx, "expired-after-reload", "worker-b", rt.Config.Queue.ClaimTTL, now); err != nil || !ok {
		t.Fatalf("platform housekeeping was not reattached after reload ok=%v err=%v", ok, err)
	}
}
