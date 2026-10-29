package scheduler

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"testing"
	"time"

	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/testkit"
)

func TestIndependentOccurrencesMigrationPreservesContractsAndHistory(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	ctx := context.Background()
	repo := NewRepository(env.DBRouter)
	before := map[string]ScheduledTask{}
	for _, state := range []TaskStatus{TaskStatusEnabled, TaskStatusDisabled, TaskStatusArchived} {
		task := schedulerTestTask(string(state), schedulerTestTime.Add(-24*time.Hour))
		task.TenantID = "tenant-" + string(state)
		task.Status, task.Enabled = state, state == TaskStatusEnabled
		task.Policy.MaxRetries, task.Policy.RetryDelay = 2, "10m"
		task.Execution.TimeoutSeconds = 5400
		task.Target.ApprovalAssigneeID = "human-reviewer"
		task.IdempotencyKey, task.IdempotencyHash = "create-key", "create-hash"
		if err := repo.SaveTask(ctx, task); err != nil {
			t.Fatal(err)
		}
		before[task.ScheduledTaskID], _ = repo.GetTask(ctx, task.ScheduledTaskID)
	}
	run := schedulerTestRun("old-running", "enabled", RunStatusRunning, schedulerTestTime.Add(-48*time.Hour))
	snapshot := before["enabled"]
	run.ConfigurationSnapshot = &snapshot
	if err := repo.SaveRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	runBefore, _ := repo.GetRun(ctx, run.ScheduledRunID)
	// Future fields and internal record metadata must survive the upgrade.
	record, _, _ := env.DBRouter.GetRecord(ctx, scheduledTaskTable, "disabled")
	var payload map[string]any
	if err := json.Unmarshal([]byte(recordString(record, schedulerPayload)), &payload); err != nil {
		t.Fatal(err)
	}
	payload["future_contract"] = "retained"
	payload["policy"].(map[string]any)["future_policy"] = "retained"
	encoded, _ := json.Marshal(payload)
	record[schedulerPayload], record["private_metadata"] = string(encoded), "retained"
	if err := env.DBRouter.PutRecord(ctx, scheduledTaskTable, "disabled", record); err != nil {
		t.Fatal(err)
	}
	if err := repo.MigrateIndependentOccurrences(ctx, schedulerTestTime); err != nil {
		t.Fatal(err)
	}
	for id, original := range before {
		got, err := repo.GetTask(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		want := original
		want.Policy.Concurrency = ConcurrencyAllowParallel
		want.UpdatedAt = original.UpdatedAt.Add(time.Nanosecond)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("migration changed fields beyond policy/revision: got=%+v want=%+v", got, want)
		}
		runs, err := repo.ListRunsForTask(ctx, id)
		if err != nil || (id != "enabled" && len(runs) != 0) {
			t.Fatalf("upgrade replayed an occurrence: %s %+v %v", id, runs, err)
		}
	}
	runAfter, _ := repo.GetRun(ctx, run.ScheduledRunID)
	if !reflect.DeepEqual(runBefore, runAfter) {
		t.Fatal("historical run or its configuration snapshot changed")
	}
	record, _, _ = env.DBRouter.GetRecord(ctx, scheduledTaskTable, "disabled")
	if err := json.Unmarshal([]byte(recordString(record, schedulerPayload)), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["future_contract"] != "retained" || payload["policy"].(map[string]any)["future_policy"] != "retained" || record["private_metadata"] != "retained" {
		t.Fatal("upgrade dropped future or private fields")
	}
	// The migration runs once. Explicit later choices survive a restart.
	changed, err := repo.MutateTask(ctx, "enabled", func(task *ScheduledTask) error {
		task.Policy.Concurrency = ConcurrencySkipIfRunning
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := NewRepository(env.DBRouter).MigrateIndependentOccurrences(ctx, schedulerTestTime.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	got, _ := repo.GetTask(ctx, "enabled")
	if !reflect.DeepEqual(got, changed) {
		t.Fatal("restart overwrote an explicit choice after migration")
	}
}

func TestIndependentOccurrencesMigrationRollsBackOnInvalidRecord(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	ctx := context.Background()
	repo := NewRepository(env.DBRouter)
	task := schedulerTestTask("a-valid", schedulerTestTime)
	if err := repo.SaveTask(ctx, task); err != nil {
		t.Fatal(err)
	}
	before, _ := repo.GetTask(ctx, task.ScheduledTaskID)
	if err := env.DBRouter.PutRecord(ctx, scheduledTaskTable, "z-invalid", db.Record{schedulerPayload: "invalid-json"}); err != nil {
		t.Fatal(err)
	}
	if err := repo.MigrateIndependentOccurrences(ctx, schedulerTestTime); err == nil {
		t.Fatal("invalid task should fail the atomic migration")
	}
	got, _ := repo.GetTask(ctx, task.ScheduledTaskID)
	if !reflect.DeepEqual(got, before) {
		t.Fatal("failed migration partially changed tasks")
	}
	if _, done, err := env.DBRouter.GetRecord(ctx, "core."+schedulerMigrationTable, independentOccurrencesMigration); err != nil || done {
		t.Fatalf("failed migration recorded completion: done=%v err=%v", done, err)
	}
}

func TestNewSchedulesDefaultToIndependentOccurrences(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	ctx := context.Background()
	repo := NewRepository(env.DBRouter)
	service := newServiceForTestWithRepository(repo)
	task, err := service.CreateTask(ctx, schedulerTestViewer(), CreateTaskRequest{
		Title: "Independent daily work", IdempotencyKey: "default-concurrency",
		Schedule: Schedule{Type: ScheduleTypeCron, Cron: "0 * * * *", Timezone: "UTC"},
		Target:   Target{Mode: TargetModeDiscussionWorkflow, Message: "Do scheduled work"},
	})
	if err != nil || task.Policy.Concurrency != ConcurrencyAllowParallel {
		t.Fatalf("new schedule policy: %+v err=%v", task.Policy, err)
	}
	if err := repo.SaveRun(ctx, schedulerTestRun("old-run", task.ScheduledTaskID, RunStatusRunning, schedulerTestTime)); err != nil {
		t.Fatal(err)
	}
	run, err := service.PrepareDueRun(ctx, task)
	if err != nil || run.Status != RunStatusQueued {
		t.Fatalf("old occurrence blocked a default schedule: %+v err=%v", run, err)
	}
}

func TestIndependentOccurrencesMigrationCoversAllPagesAndInvalidatesCache(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	ctx := context.Background()
	repo := NewRepository(env.DBRouter)
	for n := range 257 {
		task := schedulerTestTask(fmt.Sprintf("task-%03d", n), schedulerTestTime)
		if n%2 == 0 {
			task.Policy.Concurrency = ""
		}
		if err := repo.SaveTask(ctx, task); err != nil {
			t.Fatal(err)
		}
	}
	if tasks, err := repo.ListTasks(ctx, true); err != nil || len(tasks) != 257 {
		t.Fatalf("populate cache: tasks=%d err=%v", len(tasks), err)
	}
	if err := repo.MigrateIndependentOccurrences(ctx, schedulerTestTime); err != nil {
		t.Fatal(err)
	}
	tasks, err := repo.ListTasks(ctx, true)
	if err != nil || len(tasks) != 257 {
		t.Fatalf("list migrated tasks: tasks=%d err=%v", len(tasks), err)
	}
	for _, task := range tasks {
		if task.Policy.Concurrency != ConcurrencyAllowParallel {
			t.Fatalf("unmigrated or stale task: %+v", task)
		}
	}
}
