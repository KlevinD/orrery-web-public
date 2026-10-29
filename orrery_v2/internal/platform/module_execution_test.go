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
	"orrery_v2/internal/platform/idgen"
)

func TestBusinessModuleUsesRegistrarLockFacadeWithRuntimeClock(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	configPath := filepath.Join(root, "orrery.yaml")
	writeRuntimeYAML(t, configPath, ".", "./artifacts", "./logs/platform.jsonl", "1ms")
	cfg, err := config.LoadYAML(configPath)
	if err != nil {
		t.Fatalf("LoadYAML returned error: %v", err)
	}

	now := time.Date(2026, 5, 31, 20, 0, 0, 0, time.UTC)
	fakeClock := clock.NewFake(now)
	acquired := make(chan bool, 1)
	module := platform.ModuleFunc(func(ctx context.Context, registry *platform.ModuleRegistrar) error {
		lease, ok, err := registry.Locks().TryAcquire(ctx, "business-resource", "worker-a")
		if err != nil {
			return err
		}
		if !ok || lease == nil {
			t.Fatalf("first lock acquire ok=%v lease=%v", ok, lease)
		}
		_, ok, err = registry.Locks().TryAcquire(ctx, "business-resource", "worker-b")
		if err != nil {
			return err
		}
		if ok {
			t.Fatalf("second lock acquire succeeded before runtime-clock expiry")
		}
		fakeClock.Advance(cfg.Lock.DefaultTTL + time.Second)
		_, ok, err = registry.Locks().TryAcquire(ctx, "business-resource", "worker-b")
		if err != nil {
			return err
		}
		acquired <- ok
		return nil
	})

	rt, err := platform.OpenWithOptionsAndModules(ctx, cfg, platform.RuntimeOptions{Clock: fakeClock}, module)
	if err != nil {
		t.Fatalf("OpenWithOptionsAndModules returned error: %v", err)
	}
	defer rt.Close()

	select {
	case ok := <-acquired:
		if !ok {
			t.Fatalf("second owner could not acquire after runtime-clock expiry")
		}
	default:
		t.Fatalf("module did not exercise lock facade")
	}
}

func TestBusinessModuleSubmitsExecutionJobThroughRegistrarQueueWithRuntimeClock(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	configPath := filepath.Join(root, "orrery.yaml")
	writeRuntimeYAML(t, configPath, ".", "./artifacts", "./logs/platform.jsonl", "1ms")
	cfg, err := config.LoadYAML(configPath)
	if err != nil {
		t.Fatalf("LoadYAML returned error: %v", err)
	}

	now := time.Date(2026, 5, 31, 21, 0, 0, 0, time.UTC)
	var submittedID string
	module := platform.ModuleFunc(func(ctx context.Context, registry *platform.ModuleRegistrar) error {
		var err error
		submittedID, err = registry.ExecutionQueue().Submit(ctx, execution.Message{
			Topic:   "business.execution.requested",
			Payload: []byte(`{"id":"business-1"}`),
		})
		return err
	})

	rt, err := platform.OpenWithOptionsAndModules(ctx, cfg, platform.RuntimeOptions{
		Clock:       clock.NewFake(now),
		IDGenerator: idgen.NewDeterministic("business-execution"),
	}, module)
	if err != nil {
		t.Fatalf("OpenWithOptionsAndModules returned error: %v", err)
	}
	defer rt.Close()

	if submittedID != "business-execution-000001" {
		t.Fatalf("submitted id = %q, want platform-generated id", submittedID)
	}
	job, ok, err := rt.ExecutionQueue.GetStatus(ctx, submittedID)
	if err != nil {
		t.Fatalf("GetStatus returned error: %v", err)
	}
	if !ok {
		t.Fatalf("business execution job was not enqueued")
	}
	if job.Status != execution.StatusQueued || !job.CreatedAt.Equal(now) || !job.UpdatedAt.Equal(now) {
		t.Fatalf("job = %+v, want queued with runtime clock %s", job, now)
	}
}

func TestBusinessModuleUsesRegistrarExecutionClaimsWithRuntimeClockAndDefaultTTL(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	configPath := filepath.Join(root, "orrery.yaml")
	writeRuntimeYAML(t, configPath, ".", "./artifacts", "./logs/platform.jsonl", "1ms")
	cfg, err := config.LoadYAML(configPath)
	if err != nil {
		t.Fatalf("LoadYAML returned error: %v", err)
	}

	now := time.Date(2026, 5, 31, 22, 0, 0, 0, time.UTC)
	fakeClock := clock.NewFake(now)
	acquired := make(chan bool, 1)
	module := platform.ModuleFunc(func(ctx context.Context, registry *platform.ModuleRegistrar) error {
		ok, err := registry.ExecutionClaims().Acquire(ctx, "business-run-1", "worker-a")
		if err != nil {
			return err
		}
		if !ok {
			t.Fatalf("first claim acquire returned false")
		}
		ok, err = registry.ExecutionClaims().Acquire(ctx, "business-run-1", "worker-b")
		if err != nil {
			return err
		}
		if ok {
			t.Fatalf("second claim acquire succeeded before default TTL expiry")
		}
		fakeClock.Advance(cfg.Queue.ClaimTTL + time.Second)
		ok, err = registry.ExecutionClaims().Acquire(ctx, "business-run-1", "worker-b")
		if err != nil {
			return err
		}
		acquired <- ok
		return nil
	})

	rt, err := platform.OpenWithOptionsAndModules(ctx, cfg, platform.RuntimeOptions{Clock: fakeClock}, module)
	if err != nil {
		t.Fatalf("OpenWithOptionsAndModules returned error: %v", err)
	}
	defer rt.Close()

	select {
	case ok := <-acquired:
		if !ok {
			t.Fatalf("second worker could not acquire after runtime-clock claim TTL expiry")
		}
	default:
		t.Fatalf("module did not exercise execution claim facade")
	}
}

func TestBusinessModuleUsesRegistrarExecutionLivenessWithRuntimeClockAndDefaultTTL(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	configPath := filepath.Join(root, "orrery.yaml")
	writeRuntimeYAML(t, configPath, ".", "./artifacts", "./logs/platform.jsonl", "1ms")
	cfg, err := config.LoadYAML(configPath)
	if err != nil {
		t.Fatalf("LoadYAML returned error: %v", err)
	}

	now := time.Date(2026, 5, 31, 22, 30, 0, 0, time.UTC)
	fakeClock := clock.NewFake(now)
	aliveAfterExpiry := make(chan bool, 1)
	module := platform.ModuleFunc(func(ctx context.Context, registry *platform.ModuleRegistrar) error {
		if err := registry.ExecutionLiveness().Touch(ctx, "runtime-business-1"); err != nil {
			return err
		}
		alive, err := registry.ExecutionLiveness().IsAlive(ctx, "runtime-business-1")
		if err != nil {
			return err
		}
		if !alive {
			t.Fatalf("runtime-business-1 should be alive immediately after touch")
		}
		fakeClock.Advance(cfg.Liveness.HeartbeatTTL + time.Second)
		alive, err = registry.ExecutionLiveness().IsAlive(ctx, "runtime-business-1")
		if err != nil {
			return err
		}
		aliveAfterExpiry <- alive
		return nil
	})

	rt, err := platform.OpenWithOptionsAndModules(ctx, cfg, platform.RuntimeOptions{Clock: fakeClock}, module)
	if err != nil {
		t.Fatalf("OpenWithOptionsAndModules returned error: %v", err)
	}
	defer rt.Close()

	select {
	case alive := <-aliveAfterExpiry:
		if alive {
			t.Fatalf("runtime-business-1 remained alive after runtime-clock heartbeat TTL expiry")
		}
	default:
		t.Fatalf("module did not exercise execution liveness facade")
	}
}
