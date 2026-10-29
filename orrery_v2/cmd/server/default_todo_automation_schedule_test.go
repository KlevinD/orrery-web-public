package main

import (
	"context"
	"testing"
	"time"

	schedulerapp "orrery_v2/internal/app/scheduler"
	"orrery_v2/internal/platform/testkit"
)

func TestEnsureDefaultTodoAutomationScheduledTaskCreatesEnabledRecurringTask(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	repo := schedulerapp.NewRepository(env.DBRouter)
	now := time.Date(2026, 7, 24, 8, 2, 34, 0, time.UTC)

	if err := ensureDefaultTodoAutomationScheduledTask(context.Background(), repo, now); err != nil {
		t.Fatal(err)
	}
	task, err := repo.GetTask(context.Background(), defaultTodoAutomationScheduledTaskID)
	if err != nil {
		t.Fatal(err)
	}
	if !task.Enabled || task.Status != schedulerapp.TaskStatusEnabled {
		t.Fatalf("task status = %s enabled=%t", task.Status, task.Enabled)
	}
	if task.Schedule.Type != schedulerapp.ScheduleTypeCron || task.Schedule.Cron != defaultTodoAutomationCron || task.Target.Mode != schedulerapp.TargetModeTodoAutomationScan {
		t.Fatalf("task schedule/target = %+v %+v", task.Schedule, task.Target)
	}
	wantNext := time.Date(2026, 7, 24, 8, 5, 0, 0, time.UTC)
	if !task.NextRunAt.Equal(wantNext) {
		t.Fatalf("next run = %s, want %s", task.NextRunAt, wantNext)
	}
	if task.Policy.Concurrency != schedulerapp.ConcurrencyAllowParallel || task.Policy.Misfire != schedulerapp.MisfireRunOnce {
		t.Fatalf("policy = %+v", task.Policy)
	}
}

func TestEnsureDefaultTodoAutomationScheduledTaskPreservesUserConfiguration(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	repo := schedulerapp.NewRepository(env.DBRouter)
	ctx := context.Background()
	now := time.Date(2026, 7, 24, 8, 2, 34, 0, time.UTC)

	if err := ensureDefaultTodoAutomationScheduledTask(ctx, repo, now); err != nil {
		t.Fatal(err)
	}
	task, err := repo.GetTask(ctx, defaultTodoAutomationScheduledTaskID)
	if err != nil {
		t.Fatal(err)
	}
	task.Enabled = false
	task.Status = schedulerapp.TaskStatusDisabled
	task.Schedule.Cron = "*/15 * * * *"
	if err := repo.SaveTask(ctx, task); err != nil {
		t.Fatal(err)
	}

	if err := ensureDefaultTodoAutomationScheduledTask(ctx, repo, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	got, err := repo.GetTask(ctx, defaultTodoAutomationScheduledTaskID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Enabled || got.Status != schedulerapp.TaskStatusDisabled || got.Schedule.Cron != "*/15 * * * *" {
		t.Fatalf("customized task was overwritten: %+v", got)
	}
}
