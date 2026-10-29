package scheduler

import (
	"context"
	"errors"
	"testing"
	"time"

	todoapp "orrery_v2/internal/app/todo"
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/migration"
	"orrery_v2/internal/platform/testpg"
)

func stallSchedulerRepository(t *testing.T) *Repository {
	registry := testpg.Registry(t)
	core, err := registry.Store(db.RoleCore)
	if err != nil {
		t.Fatal(err)
	}
	if err := migration.NewRunner([]migration.Migration{maintenanceIndexMigration()}).Up(context.Background(), core); err != nil {
		t.Fatal(err)
	}
	return NewRepository(db.NewRouter(registry))
}

func TestPostgresStallRetentionIsTaskScopedBoundedAndPreservesActiveRuns(t *testing.T) {
	repo := stallSchedulerRepository(t)
	ctx := context.Background()
	now := time.Date(2026, 10, 14, 0, 0, 0, 0, time.UTC)
	runs := []ScheduledRun{
		maintenanceRetentionRun("stall-oldest", "stall-task", RunStatusFailed, now.Add(-9*24*time.Hour)),
		maintenanceRetentionRun("stall-old", "stall-task", RunStatusCompleted, now.Add(-2*24*time.Hour)),
		maintenanceRetentionRun("stall-recent-failure", "stall-task", RunStatusFailed, now.Add(-2*24*time.Hour)),
		maintenanceRetentionRun("stall-active", "stall-task", RunStatusRunning, now.Add(-9*24*time.Hour)),
		maintenanceRetentionRun("stall-other", "other-task", RunStatusCompleted, now.Add(-9*24*time.Hour)),
		maintenanceRetentionRun("stall-at-cutoff", "stall-task", RunStatusCompleted, now.Add(-24*time.Hour)),
	}
	for _, run := range runs {
		if err := repo.SaveRun(ctx, run); err != nil {
			t.Fatal(err)
		}
	}
	deleted, err := repo.CleanupTaskRuns(ctx, "stall-task", now, 24*time.Hour, 7*24*time.Hour, 1)
	if err != nil || deleted != 1 {
		t.Fatalf("deleted=%d err=%v", deleted, err)
	}
	if _, err := repo.GetRun(ctx, "stall-oldest"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("oldest retained: %v", err)
	}
	deleted, err = repo.CleanupTaskRuns(ctx, "stall-task", now, 24*time.Hour, 7*24*time.Hour, 100)
	if err != nil || deleted != 1 {
		t.Fatalf("second deleted=%d err=%v", deleted, err)
	}
	for _, id := range []string{"stall-recent-failure", "stall-active", "stall-other", "stall-at-cutoff"} {
		if _, err := repo.GetRun(ctx, id); err != nil {
			t.Fatalf("protected run %s: %v", id, err)
		}
	}
}

func TestPostgresStallTodoAggregationAndRetentionRespectResults(t *testing.T) {
	repo := stallSchedulerRepository(t)
	ctx := context.Background()
	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	makeRun := func(id, task string, at time.Time, empty bool) ScheduledRun {
		run := maintenanceRetentionRun(id, task, RunStatusCompleted, at)
		run.TargetMode = TargetModeTodoAutomationScan
		run.TodoAutomationScan = &todoapp.TodoAutomationScanResult{}
		if !empty {
			run.TodoAutomationScan.ScannedCount = 1
		}
		return run
	}
	for _, run := range []ScheduledRun{makeRun("stall-aggregate-far", "stall-aggregate", now.Add(-50*time.Minute), true), makeRun("stall-aggregate-near", "stall-aggregate", now.Add(-10*time.Minute), true), makeRun("stall-aggregate-nonempty", "stall-aggregate", now.Add(-time.Minute), false)} {
		if err := repo.SaveRun(ctx, run); err != nil {
			t.Fatal(err)
		}
	}
	into, merged, err := repo.CoalesceEmptyTodoAutomationRun(ctx, makeRun("stall-aggregate-new", "stall-aggregate", now, true), time.Hour)
	if err != nil || !merged || into != "stall-aggregate-near" {
		t.Fatalf("into=%s merged=%t err=%v", into, merged, err)
	}
	for _, run := range []ScheduledRun{makeRun("stall-empty-expired", "stall-todo", now.Add(-48*time.Hour), true), makeRun("stall-result-protected", "stall-todo", now.Add(-48*time.Hour), false)} {
		if err := repo.SaveRun(ctx, run); err != nil {
			t.Fatal(err)
		}
	}
	deleted, err := repo.CleanupTodoAutomationRuns(ctx, now, 24*time.Hour, 7*24*time.Hour)
	if err != nil || deleted != 1 {
		t.Fatalf("deleted=%d err=%v", deleted, err)
	}
	if _, err := repo.GetRun(ctx, "stall-result-protected"); err != nil {
		t.Fatalf("nonempty result removed: %v", err)
	}
}
