package main

import (
	"context"
	"errors"
	"testing"
	"time"

	schedulerapp "orrery_v2/internal/app/scheduler"
)

func TestResponsibilityRunRetentionUsesBoundedInternalPolicy(t *testing.T) {
	now := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	store := &fakeScheduledTaskRunRetentionStore{}
	sweeper := productionResponsibilityRunRetentionSweeper{runs: store, now: func() time.Time { return now }}

	if err := sweeper.Sweep(context.Background()); err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if store.taskID != defaultResponsibilityReconcileScheduledTaskID {
		t.Fatalf("task ID = %q", store.taskID)
	}
	if !store.now.Equal(now) || store.completedRetention != 24*time.Hour || store.diagnosticRetention != 7*24*time.Hour || store.limit != 500 {
		t.Fatalf("retention call = %+v", store)
	}
}

func TestResponsibilityRunRetentionPropagatesFailure(t *testing.T) {
	want := errors.New("retention failed")
	sweeper := productionResponsibilityRunRetentionSweeper{
		runs: &fakeScheduledTaskRunRetentionStore{err: want},
		now:  time.Now,
	}
	if err := sweeper.Sweep(context.Background()); !errors.Is(err, want) {
		t.Fatalf("Sweep error = %v, want %v", err, want)
	}
}

func TestResponsibilityRunRetentionCleansManagedBatchTasks(t *testing.T) {
	now := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	manager := productionResponsibilityBatchSchedule{orreryTenantID: "tenant-a", peopleTenantID: "people-a"}
	managed := testProductionResponsibilityBatchTask(manager, "batch-task")
	foreign := managed
	foreign.ScheduledTaskID = "foreign-task"
	foreign.CreatedBy = "someone-else"
	store := &fakeScheduledTaskRunRetentionStore{tasks: []schedulerapp.ScheduledTask{managed, foreign}}
	sweeper := productionResponsibilityRunRetentionSweeper{runs: store, now: func() time.Time { return now }}

	if err := sweeper.Sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(store.calls) != 2 {
		t.Fatalf("cleanup calls = %+v, want reconcile and one managed batch", store.calls)
	}
	call := store.calls[1]
	if call.taskID != managed.ScheduledTaskID || call.completedRetention != 90*24*time.Hour ||
		call.diagnosticRetention != 180*24*time.Hour || call.limit != 5 {
		t.Fatalf("batch retention call = %+v", call)
	}
}

func TestResponsibilityRunRetentionContinuesAfterOneBatchFailure(t *testing.T) {
	managerA := productionResponsibilityBatchSchedule{orreryTenantID: "tenant-a", peopleTenantID: "people-a"}
	managerB := productionResponsibilityBatchSchedule{orreryTenantID: "tenant-b", peopleTenantID: "people-b"}
	taskA := testProductionResponsibilityBatchTask(managerA, "batch-a")
	taskB := testProductionResponsibilityBatchTask(managerB, "batch-b")
	want := errors.New("batch-a failed")
	store := &fakeScheduledTaskRunRetentionStore{
		tasks: []schedulerapp.ScheduledTask{taskA, taskB}, errorsByTask: map[string]error{taskA.ScheduledTaskID: want},
	}
	sweeper := productionResponsibilityRunRetentionSweeper{runs: store, now: time.Now}

	if err := sweeper.Sweep(context.Background()); !errors.Is(err, want) {
		t.Fatalf("Sweep error = %v, want %v", err, want)
	}
	if len(store.calls) != 3 || store.calls[2].taskID != taskB.ScheduledTaskID {
		t.Fatalf("cleanup calls = %+v, want second batch after failure", store.calls)
	}
}

type scheduledTaskRunRetentionCall struct {
	taskID              string
	now                 time.Time
	completedRetention  time.Duration
	diagnosticRetention time.Duration
	limit               int
}

type fakeScheduledTaskRunRetentionStore struct {
	tasks               []schedulerapp.ScheduledTask
	calls               []scheduledTaskRunRetentionCall
	taskID              string
	now                 time.Time
	completedRetention  time.Duration
	diagnosticRetention time.Duration
	limit               int
	err                 error
	errorsByTask        map[string]error
}

func (s *fakeScheduledTaskRunRetentionStore) CleanupTaskRuns(_ context.Context, taskID string, now time.Time, completedRetention, diagnosticRetention time.Duration, limit int) (int, error) {
	s.calls = append(s.calls, scheduledTaskRunRetentionCall{taskID: taskID, now: now, completedRetention: completedRetention, diagnosticRetention: diagnosticRetention, limit: limit})
	s.taskID = taskID
	s.now = now
	s.completedRetention = completedRetention
	s.diagnosticRetention = diagnosticRetention
	s.limit = limit
	if err := s.errorsByTask[taskID]; err != nil {
		return 0, err
	}
	return 0, s.err
}

func (s *fakeScheduledTaskRunRetentionStore) ListTasks(context.Context, bool) ([]schedulerapp.ScheduledTask, error) {
	return append([]schedulerapp.ScheduledTask(nil), s.tasks...), nil
}
