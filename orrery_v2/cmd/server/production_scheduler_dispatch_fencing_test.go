package main

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	schedulerapp "orrery_v2/internal/app/scheduler"
	"orrery_v2/internal/platform"
	"orrery_v2/internal/platform/testkit"
)

func TestProductionSchedulerDispatchHandlerSkipsArchivedQueuedTask(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	repo := schedulerapp.NewRepository(env.DBRouter)
	ctx := context.Background()
	task := schedulerapp.ScheduledTask{
		TenantID: "tenant-a", ScheduledTaskID: "sched-archived", Title: "Archived schedule",
		Status: schedulerapp.TaskStatusArchived, Enabled: false,
	}
	run := schedulerapp.ScheduledRun{
		TenantID: task.TenantID, ScheduledRunID: "run-archived", ScheduledTaskID: task.ScheduledTaskID,
		Status: schedulerapp.RunStatusQueued,
	}
	if err := repo.SaveTask(ctx, task); err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(productionSchedulerDispatchPayload{ScheduledTaskID: task.ScheduledTaskID, ScheduledRunID: run.ScheduledRunID})
	if err != nil {
		t.Fatal(err)
	}
	dispatcher := &recordingProductionRunDispatcher{}
	handler := productionSchedulerDispatchHandler(repo, dispatcher, env.Clock)
	if err := handler(ctx, platform.ExecutionDelivery{Payload: payload, MaxAttempts: 2}); err != nil {
		t.Fatal(err)
	}
	if dispatcher.calls != 0 {
		t.Fatalf("dispatcher calls = %d, want 0", dispatcher.calls)
	}
	stored, err := repo.GetRun(ctx, run.ScheduledRunID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != schedulerapp.RunStatusSkipped || stored.CompletedAt.IsZero() || stored.FailureReason == "" {
		t.Fatalf("stored run = %+v, want skipped archived run", stored)
	}
}

func TestProductionSchedulerDispatchHandlerRejectsMismatchedArchivedTaskPayload(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	repo := schedulerapp.NewRepository(env.DBRouter)
	ctx := context.Background()
	task := schedulerapp.ScheduledTask{
		TenantID: "tenant-a", ScheduledTaskID: "sched-archived-a", Title: "Archived schedule A",
		Status: schedulerapp.TaskStatusArchived, Enabled: false,
	}
	run := schedulerapp.ScheduledRun{
		TenantID: task.TenantID, ScheduledRunID: "run-b", ScheduledTaskID: "sched-b", Status: schedulerapp.RunStatusQueued,
	}
	if err := repo.SaveTask(ctx, task); err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(productionSchedulerDispatchPayload{ScheduledTaskID: task.ScheduledTaskID, ScheduledRunID: run.ScheduledRunID})
	if err != nil {
		t.Fatal(err)
	}
	handler := productionSchedulerDispatchHandler(repo, &recordingProductionRunDispatcher{}, env.Clock)
	if err := handler(ctx, platform.ExecutionDelivery{Payload: payload, MaxAttempts: 2}); !errors.Is(err, schedulerapp.ErrRevisionConflict) {
		t.Fatalf("handler error = %v, want ErrRevisionConflict", err)
	}
	stored, err := repo.GetRun(ctx, run.ScheduledRunID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != schedulerapp.RunStatusQueued || !stored.CompletedAt.IsZero() {
		t.Fatalf("mismatched run changed = %+v", stored)
	}
}

func TestProductionSchedulerRunTrackerRejectsStaleAttemptCallbacks(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	repo := schedulerapp.NewRepository(env.DBRouter)
	ctx := context.Background()
	run := schedulerapp.ScheduledRun{
		ScheduledRunID: "run-attempt-fence", ScheduledTaskID: "sched-attempt-fence",
		Status: schedulerapp.RunStatusRunning, Attempt: 2, FailureReason: "attempt 2 running",
	}
	if err := repo.SaveRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	tracker := productionSchedulerRunTracker{repo: repo, clock: env.Clock}
	if err := tracker.Started(ctx, run.ScheduledRunID, 1); err != nil {
		t.Fatal(err)
	}
	if err := tracker.AttemptFailed(ctx, run.ScheduledRunID, 1, true, errors.New("stale attempt 1")); err != nil {
		t.Fatal(err)
	}
	stored, err := repo.GetRun(ctx, run.ScheduledRunID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != schedulerapp.RunStatusRunning || stored.Attempt != 2 || stored.FailureReason != run.FailureReason || !stored.CompletedAt.IsZero() {
		t.Fatalf("stored run = %+v, want unchanged attempt 2", stored)
	}
}

type recordingProductionRunDispatcher struct {
	calls int
}

func (d *recordingProductionRunDispatcher) DispatchRun(_ context.Context, _ schedulerapp.ScheduledTask, run schedulerapp.ScheduledRun) (schedulerapp.ScheduledRun, error) {
	d.calls++
	return run, nil
}
