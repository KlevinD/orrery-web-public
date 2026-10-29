package scheduler

import (
	"context"
	"orrery_v2/internal/platform/testkit"
	"testing"
	"time"
)

func TestListNotificationRunsFiltersAndRefreshesAfterResultCommit(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	repo := NewRepository(env.DBRouter)
	ctx := context.Background()
	for _, id := range []string{"current", "old", "other-task", "foreign", "running"} {
		run := schedulerTestRun(id, "task", RunStatusCompleted, schedulerTestTime.Add(time.Hour))
		run.TenantID = "org"
		switch id {
		case "old":
			run.CreatedAt = schedulerTestTime.Add(-time.Hour)
		case "other-task":
			run.ScheduledTaskID = "elsewhere"
		case "foreign":
			run.TenantID = "other-org"
		case "running":
			run.Status = RunStatusRunning
		}
		if err := repo.SaveRun(ctx, run); err != nil {
			t.Fatal(err)
		}
	}
	runs, err := repo.ListNotificationRuns(ctx, "org", "task", schedulerTestTime)
	if err != nil || len(runs) != 1 || runs[0].ScheduledRunID != "current" {
		t.Fatalf("filtered runs=%+v err=%v", runs, err)
	}
	if _, err := repo.MutateRun(ctx, "running", func(r *ScheduledRun) error { r.Status = RunStatusCompleted; return nil }); err != nil {
		t.Fatal(err)
	}
	runs, err = repo.ListNotificationRuns(ctx, "org", "task", schedulerTestTime)
	if err != nil || len(runs) != 2 {
		t.Fatalf("new terminal result not visible: %+v %v", runs, err)
	}
}
