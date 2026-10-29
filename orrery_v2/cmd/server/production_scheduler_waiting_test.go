package main

import (
	"context"
	schedulerapp "orrery_v2/internal/app/scheduler"
	"orrery_v2/internal/platform/testkit"
	"testing"
	"time"
)

func TestProductionSchedulerWaitingDoesNotClaimRuntimeStarted(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	repo := schedulerapp.NewRepository(env.DBRouter)
	tracker := productionSchedulerRunTracker{repo: repo, clock: env.Clock}
	ctx := context.Background()
	for _, runtimeID := range []string{"", "actual-runtime"} {
		run := schedulerapp.ScheduledRun{ScheduledRunID: "waiting-" + runtimeID, ScheduledTaskID: "task", Status: schedulerapp.RunStatusRunning, RuntimeRunID: runtimeID, StartedAt: time.Now()}
		if err := repo.SaveRun(ctx, run); err != nil {
			t.Fatal(err)
		}
		if err := tracker.AwaitingResponsibility(ctx, run.ScheduledRunID, "permission: rsi.learning.read"); err != nil {
			t.Fatal(err)
		}
		got, err := repo.GetRun(ctx, run.ScheduledRunID)
		if err != nil {
			t.Fatal(err)
		}
		if runtimeID == "" && (got.Status != schedulerapp.RunStatusQueued || !got.StartedAt.IsZero() || got.FailureReason != "permission: rsi.learning.read") {
			t.Fatalf("false running state: %+v", got)
		}
		if runtimeID != "" && got.Status != schedulerapp.RunStatusRunning {
			t.Fatal("actual runtime was demoted")
		}
	}
}
