package main

import (
	"context"
	"testing"
	"time"

	schedulerapp "orrery_v2/internal/app/scheduler"
	"orrery_v2/internal/platform/testkit"
)

func TestEnsureDefaultResponsibilityReconcileScheduledTask(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	repo := schedulerapp.NewRepository(env.DBRouter)
	now := time.Date(2026, 7, 27, 10, 0, 30, 0, time.UTC)
	if err := ensureDefaultResponsibilityReconcileScheduledTask(context.Background(), repo, now); err != nil {
		t.Fatal(err)
	}
	task, err := repo.GetTask(context.Background(), defaultResponsibilityReconcileScheduledTaskID)
	if err != nil {
		t.Fatal(err)
	}
	if task.TenantID != "system" || task.CreatedByKind != schedulerapp.ViewerKindSystem || task.Target.Mode != schedulerapp.TargetModeResponsibilityReconcile {
		t.Fatalf("system responsibility task = %+v", task)
	}
	if task.Schedule.Type != schedulerapp.ScheduleTypeCron || task.Schedule.Cron != defaultResponsibilityReconcileCron || task.Policy.Concurrency != schedulerapp.ConcurrencySkipIfRunning {
		t.Fatalf("responsibility schedule = %+v policy=%+v", task.Schedule, task.Policy)
	}
	if !task.NextRunAt.Equal(time.Date(2026, 7, 27, 10, 1, 0, 0, time.UTC)) {
		t.Fatalf("next run = %s", task.NextRunAt)
	}

	original := task
	if err := ensureDefaultResponsibilityReconcileScheduledTask(context.Background(), repo, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	unchanged, err := repo.GetTask(context.Background(), task.ScheduledTaskID)
	if err != nil {
		t.Fatal(err)
	}
	if unchanged.UpdatedAt != original.UpdatedAt || unchanged.NextRunAt != original.NextRunAt {
		t.Fatalf("bootstrap rewrote existing task: before=%+v after=%+v", original, unchanged)
	}
}
