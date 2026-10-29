package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	schedulerapp "orrery_v2/internal/app/scheduler"
	"orrery_v2/internal/platform/clock"
	"orrery_v2/internal/platform/logging"
	"orrery_v2/internal/platform/testkit"
)

func TestRunProductionSchedulerOccurrenceReconciliationRepairsAndReports(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	repo := schedulerapp.NewRepository(env.DBRouter)
	now := time.Date(2026, 8, 6, 9, 30, 0, 0, time.UTC)
	missing := schedulerapp.ScheduledTask{
		TenantID: "system", ScheduledTaskID: "disabled-missing", Title: "Missing occurrence",
		Schedule: schedulerapp.Schedule{Type: schedulerapp.ScheduleTypeCron, Cron: "*/15 * * * *", Timezone: "UTC"},
		Target:   schedulerapp.Target{Mode: schedulerapp.TargetModeDiscussionWorkflow, Message: "Run task."},
		Status:   schedulerapp.TaskStatusDisabled, Enabled: false, CreatedBy: "operator", CreatorProfileID: "operator", CreatedAt: now, UpdatedAt: now,
	}
	invalid := missing
	invalid.ScheduledTaskID = "disabled-invalid"
	invalid.Schedule.Cron = "not-a-cron"
	for _, task := range []schedulerapp.ScheduledTask{missing, invalid} {
		if err := repo.SaveTask(context.Background(), task); err != nil {
			t.Fatal(err)
		}
	}
	service := schedulerapp.NewService(schedulerapp.ServiceOptions{Clock: clock.NewFake(now), Repository: repo})
	var logs bytes.Buffer

	if err := runProductionSchedulerOccurrenceReconciliation(context.Background(), service, now, logging.NewJSONLogger(&logs, logging.LevelInfo)); err != nil {
		t.Fatal(err)
	}
	stored, err := repo.GetTask(context.Background(), missing.ScheduledTaskID)
	if err != nil {
		t.Fatal(err)
	}
	want := time.Date(2026, 8, 6, 9, 45, 0, 0, time.UTC)
	if !stored.NextRunAt.Equal(want) || stored.Enabled || stored.Status != schedulerapp.TaskStatusDisabled {
		t.Fatalf("stored task = %+v, want disabled occurrence %s", stored, want)
	}
	if output := logs.String(); !strings.Contains(output, invalid.ScheduledTaskID) || !strings.Contains(output, `"repaired":1`) {
		t.Fatalf("reconciliation logs = %s", output)
	}
}
