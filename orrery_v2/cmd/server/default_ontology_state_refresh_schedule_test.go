package main

import (
	"context"
	"testing"
	"time"

	schedulerapp "orrery_v2/internal/app/scheduler"
	"orrery_v2/internal/platform/testkit"
)

func TestEnsureDefaultOntologyStateRefreshScheduledTask(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	repo := schedulerapp.NewRepository(env.DBRouter)
	ctx := context.Background()
	now := time.Date(2026, 8, 21, 0, 0, 0, 0, time.UTC)

	if err := ensureDefaultOntologyStateRefreshScheduledTask(ctx, repo, now); err != nil {
		t.Fatal(err)
	}
	task, err := repo.GetTask(ctx, defaultOntologyStateRefreshScheduledTaskID)
	if err != nil {
		t.Fatal(err)
	}
	if task.TenantID != "system" || task.ScheduledTaskID != "ontology-state-daily-refresh" ||
		task.CreatedByKind != schedulerapp.ViewerKindSystem || task.Target.Mode != schedulerapp.TargetModeOntologyStateRefresh ||
		task.Target.Prompt != "" || task.Target.ToolID != "" || len(task.Target.ToolArguments) != 0 {
		t.Fatalf("ontology state refresh task = %+v", task)
	}
	if task.Schedule != (schedulerapp.Schedule{Type: schedulerapp.ScheduleTypeCron, Cron: "10 8 * * *", Timezone: "Asia/Shanghai"}) {
		t.Fatalf("schedule = %+v", task.Schedule)
	}
	wantPolicy := schedulerapp.Policy{
		Misfire: schedulerapp.MisfireRunOnce, Concurrency: schedulerapp.ConcurrencyAllowParallel,
		MaxRetries: 1, RetryDelay: "5m", FailureMode: "record_failure",
	}
	if task.Policy != wantPolicy || !task.Enabled || task.Status != schedulerapp.TaskStatusEnabled {
		t.Fatalf("policy/status = %+v enabled=%t status=%s", task.Policy, task.Enabled, task.Status)
	}

	task.Schedule.Cron = "20 9 * * *"
	task.Enabled = false
	task.Status = schedulerapp.TaskStatusDisabled
	task.UpdatedAt = now.Add(time.Hour)
	if err := repo.SaveTask(ctx, task); err != nil {
		t.Fatal(err)
	}
	if err := ensureDefaultOntologyStateRefreshScheduledTask(ctx, repo, now.Add(24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	preserved, err := repo.GetTask(ctx, task.ScheduledTaskID)
	if err != nil {
		t.Fatal(err)
	}
	if preserved.Schedule.Cron != task.Schedule.Cron || preserved.Enabled || preserved.Status != schedulerapp.TaskStatusDisabled || !preserved.UpdatedAt.Equal(task.UpdatedAt) {
		t.Fatalf("operator changes were overwritten: before=%+v after=%+v", task, preserved)
	}
}
