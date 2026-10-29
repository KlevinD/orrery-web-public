package execution_test

import (
	"context"
	"testing"
	"time"

	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/execution"
)

func TestOpenBuildsUnifiedRuntimeInfrastructure(t *testing.T) {
	ctx := context.Background()
	jobs, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: "execution-jobs"})
	if err != nil {
		t.Fatalf("open jobs db: %v", err)
	}
	defer jobs.Close()
	runtime, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: "execution-runtime"})
	if err != nil {
		t.Fatalf("open runtime db: %v", err)
	}
	defer runtime.Close()

	infra, err := execution.Open(ctx, execution.Stores{Jobs: jobs, Runtime: runtime}, execution.Config{
		Queue:   execution.QueueConfig{ClaimTTL: time.Minute, MaxAttempts: 2},
		Sweeper: execution.SweeperConfig{BatchSize: 5},
	})
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	if infra.Queue == nil || infra.Claims == nil || infra.Liveness == nil || infra.Sweepers == nil {
		t.Fatalf("infra = %+v", infra)
	}

	now := time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC)
	if err := infra.Queue.Enqueue(ctx, execution.Job{ID: "job-1", Topic: "runtime"}, now); err != nil {
		t.Fatalf("enqueue through unified infra: %v", err)
	}
	if ok, err := infra.Claims.Acquire(ctx, "run-1", "worker-1", time.Minute, now); err != nil || !ok {
		t.Fatalf("acquire through unified infra ok=%v err=%v", ok, err)
	}
	if err := infra.Liveness.TouchHeartbeat(ctx, "runtime-1", time.Minute, now); err != nil {
		t.Fatalf("touch through unified infra: %v", err)
	}
}
