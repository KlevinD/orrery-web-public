package platform_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"orrery_v2/internal/platform"
	"orrery_v2/internal/platform/clock"
	"orrery_v2/internal/platform/config"
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/execution"
)

func TestRuntimeStartsRegisteredExecutionHandlerFromModule(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	configPath := filepath.Join(root, "orrery.yaml")
	writeRuntimeYAML(t, configPath, ".", "./artifacts", "./logs/platform.jsonl", "1ms")
	cfg, err := config.LoadYAML(configPath)
	if err != nil {
		t.Fatalf("LoadYAML returned error: %v", err)
	}

	processed := make(chan string, 1)
	module := platform.ModuleFunc(func(ctx context.Context, registrar *platform.ModuleRegistrar) error {
		return registrar.RegisterExecutionDeliveryHandler("business.worker.sync", func(ctx context.Context, delivery platform.ExecutionDelivery) error {
			processed <- string(delivery.Payload)
			return nil
		})
	})

	rt, err := platform.OpenWithModules(ctx, cfg, module)
	if err != nil {
		t.Fatalf("OpenWithModules returned error: %v", err)
	}
	defer rt.Close()

	executionID, err := rt.ExecutionQueue.Submit(ctx, execution.Message{
		Topic:   "business.worker.sync",
		Payload: []byte("profile-1"),
	})
	if err != nil {
		t.Fatalf("Submit returned error: %v", err)
	}
	if err := rt.StartExecutionRunner(ctx, platform.ExecutionRunnerRuntimeConfig{PollInterval: time.Millisecond}); err != nil {
		t.Fatalf("StartExecutionRunner returned error: %v", err)
	}

	select {
	case got := <-processed:
		if got != "profile-1" {
			t.Fatalf("processed payload = %q, want profile-1", got)
		}
	case <-time.After(time.Second):
		t.Fatalf("registered execution handler did not process queued job")
	}
	waitFor(t, time.Second, func() bool {
		job, ok, err := rt.ExecutionQueue.GetStatus(ctx, executionID)
		return err == nil && ok && job.Status == execution.StatusCompleted
	})
	if err := rt.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}
}

func TestRuntimeExecutionRunnerProcessesDistinctQueuesConcurrently(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	configPath := filepath.Join(root, "orrery.yaml")
	writeRuntimeYAML(t, configPath, ".", "./artifacts", "./logs/platform.jsonl", "1ms")
	cfg, err := config.LoadYAML(configPath)
	if err != nil {
		t.Fatalf("LoadYAML returned error: %v", err)
	}
	started := make(chan string, 2)
	release := make(chan struct{})
	module := platform.ModuleFunc(func(ctx context.Context, registrar *platform.ModuleRegistrar) error {
		return registrar.RegisterExecutionDeliveryHandler("business.concurrent", func(ctx context.Context, delivery platform.ExecutionDelivery) error {
			started <- string(delivery.Payload)
			select {
			case <-release:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		})
	})
	rt, err := platform.OpenWithModules(ctx, cfg, module)
	if err != nil {
		t.Fatalf("OpenWithModules returned error: %v", err)
	}
	defer rt.Close()
	for _, item := range []struct{ payload, queue string }{{"lane-1", "mission:1:agent:1"}, {"lane-2", "mission:1:agent:2"}} {
		if _, err := rt.ExecutionQueue.Submit(ctx, execution.Message{Topic: "business.concurrent", Payload: []byte(item.payload), QueueKey: item.queue}); err != nil {
			t.Fatalf("Submit returned error: %v", err)
		}
	}
	if err := rt.StartExecutionRunner(ctx, platform.ExecutionRunnerRuntimeConfig{PollInterval: time.Millisecond, Concurrency: 2}); err != nil {
		t.Fatalf("StartExecutionRunner returned error: %v", err)
	}
	for index := 0; index < 2; index++ {
		select {
		case <-started:
		case <-time.After(time.Second):
			close(release)
			t.Fatal("distinct execution queues did not run concurrently")
		}
	}
	close(release)
}

func TestRuntimeExecutionClaimsUsePlatformClock(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	configPath := filepath.Join(root, "orrery.yaml")
	writeRuntimeYAML(t, configPath, ".", "./artifacts", "./logs/platform.jsonl", "1ms")
	cfg, err := config.LoadYAML(configPath)
	if err != nil {
		t.Fatalf("LoadYAML returned error: %v", err)
	}
	now := time.Date(2026, 5, 31, 19, 30, 0, 0, time.UTC)
	rt, err := platform.OpenWithOptions(ctx, cfg, platform.RuntimeOptions{
		Clock: clock.NewFake(now),
	})
	if err != nil {
		t.Fatalf("OpenWithOptions returned error: %v", err)
	}
	defer rt.Close()

	if err := rt.Execution.Claims.Bump(ctx, "runtime-a"); err != nil {
		t.Fatalf("Bump returned error: %v", err)
	}
	var updatedAt int64
	if err := mustStore(t, rt.Databases, db.RoleRuntime).QueryRow(ctx, `SELECT updated_at FROM platform_negative_claim_cache WHERE cache_key = ?`, "runtime-a").Scan(&updatedAt); err != nil {
		t.Fatalf("query negative cache updated_at: %v", err)
	}
	if updatedAt != now.UnixNano() {
		t.Fatalf("updated_at = %d, want runtime clock %d", updatedAt, now.UnixNano())
	}
}

func TestRuntimeExecutionRunnerUsesPlatformClock(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	configPath := filepath.Join(root, "orrery.yaml")
	writeRuntimeYAML(t, configPath, ".", "./artifacts", "./logs/platform.jsonl", "1ms")
	cfg, err := config.LoadYAML(configPath)
	if err != nil {
		t.Fatalf("LoadYAML returned error: %v", err)
	}
	now := time.Date(2026, 5, 31, 20, 30, 0, 0, time.UTC)
	fakeClock := clock.NewFake(now)
	processed := make(chan struct{}, 1)
	module := platform.ModuleFunc(func(ctx context.Context, registrar *platform.ModuleRegistrar) error {
		return registrar.RegisterExecutionDeliveryHandler("business.clocked", func(ctx context.Context, delivery platform.ExecutionDelivery) error {
			fakeClock.Advance(25 * time.Millisecond)
			processed <- struct{}{}
			return nil
		})
	})

	rt, err := platform.OpenWithOptionsAndModules(ctx, cfg, platform.RuntimeOptions{Clock: fakeClock}, module)
	if err != nil {
		t.Fatalf("OpenWithOptionsAndModules returned error: %v", err)
	}
	defer rt.Close()

	executionID, err := rt.ExecutionQueue.Submit(ctx, execution.Message{Topic: "business.clocked"})
	if err != nil {
		t.Fatalf("Submit returned error: %v", err)
	}
	if err := rt.StartExecutionRunner(ctx, platform.ExecutionRunnerRuntimeConfig{PollInterval: time.Millisecond}); err != nil {
		t.Fatalf("StartExecutionRunner returned error: %v", err)
	}
	select {
	case <-processed:
	case <-time.After(time.Second):
		t.Fatalf("registered execution handler did not process queued job")
	}
	waitFor(t, time.Second, func() bool {
		job, ok, err := rt.ExecutionQueue.GetStatus(ctx, executionID)
		return err == nil && ok && job.Status == execution.StatusCompleted
	})

	job, ok, err := rt.ExecutionQueue.GetStatus(ctx, executionID)
	if err != nil || !ok {
		t.Fatalf("load execution job ok=%v err=%v", ok, err)
	}
	if !job.DispatchedAt.Equal(now) {
		t.Fatalf("DispatchedAt = %s, want runtime clock %s", job.DispatchedAt, now)
	}
	if !job.StartedAt.Equal(now) {
		t.Fatalf("StartedAt = %s, want runtime clock %s", job.StartedAt, now)
	}
	if !job.CompletedAt.Equal(now.Add(25 * time.Millisecond)) {
		t.Fatalf("CompletedAt = %s, want runtime clock %s", job.CompletedAt, now.Add(25*time.Millisecond))
	}
}

func TestRuntimeRejectsExecutionHandlerRegistrationAfterRunnerStarts(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	configPath := filepath.Join(root, "orrery.yaml")
	writeRuntimeYAML(t, configPath, ".", "./artifacts", "./logs/platform.jsonl", "1ms")

	rt, err := platform.OpenFromYAML(ctx, configPath)
	if err != nil {
		t.Fatalf("OpenFromYAML returned error: %v", err)
	}
	defer rt.Close()

	if err := rt.StartExecutionRunner(ctx, platform.ExecutionRunnerRuntimeConfig{PollInterval: time.Millisecond}); err != nil {
		t.Fatalf("StartExecutionRunner returned error: %v", err)
	}
	err = rt.RegisterExecutionHandler("business.worker.sync", func(context.Context, execution.Job) error { return nil })
	if err == nil {
		t.Fatalf("RegisterExecutionHandler returned nil error after runner was started")
	}
}

func TestRuntimeRejectsExecutionDeliveryHandlerRegistrationAfterRunnerStarts(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	configPath := filepath.Join(root, "orrery.yaml")
	writeRuntimeYAML(t, configPath, ".", "./artifacts", "./logs/platform.jsonl", "1ms")

	rt, err := platform.OpenFromYAML(ctx, configPath)
	if err != nil {
		t.Fatalf("OpenFromYAML returned error: %v", err)
	}
	defer rt.Close()

	if err := rt.StartExecutionRunner(ctx, platform.ExecutionRunnerRuntimeConfig{PollInterval: time.Millisecond}); err != nil {
		t.Fatalf("StartExecutionRunner returned error: %v", err)
	}
	err = rt.RegisterExecutionDeliveryHandler("business.worker.sync", func(context.Context, platform.ExecutionDelivery) error { return nil })
	if err == nil {
		t.Fatalf("RegisterExecutionDeliveryHandler returned nil error after runner was started")
	}
}

func TestRuntimeReadyReportsManagedExecutionRunnerUnexpectedExit(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	configPath := filepath.Join(root, "orrery.yaml")
	writeRuntimeYAML(t, configPath, ".", "./artifacts", "./logs/platform.jsonl", "1ms")

	rt, err := platform.OpenFromYAML(ctx, configPath)
	if err != nil {
		t.Fatalf("OpenFromYAML returned error: %v", err)
	}
	defer rt.Close()

	runCtx, cancel := context.WithCancel(ctx)
	if err := rt.StartExecutionRunner(runCtx, platform.ExecutionRunnerRuntimeConfig{PollInterval: time.Millisecond}); err != nil {
		t.Fatalf("StartExecutionRunner returned error: %v", err)
	}
	cancel()
	waitFor(t, time.Second, func() bool {
		_, ok := findReadyCheck(rt.Health.Ready(ctx), "runtime.worker.execution")
		return ok
	})
	ready := rt.Health.Ready(ctx)
	if check, ok := findReadyCheck(ready, "runtime.worker.execution"); !ok || check.OK {
		t.Fatalf("runtime.worker.execution check = %+v ok=%v, want not ready", check, ok)
	}
}
