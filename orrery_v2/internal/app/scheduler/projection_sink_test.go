package scheduler

import (
	"context"
	"errors"
	"testing"

	"orrery_v2/internal/platform/testkit"
)

type recordingProjectionSink struct {
	tasks []ScheduledTask
	runs  []ScheduledRun
}

type failFirstTaskProjectionSink struct {
	tasks []ScheduledTask
	err   error
}

func (s *failFirstTaskProjectionSink) ProjectScheduledTask(_ context.Context, task ScheduledTask) error {
	s.tasks = append(s.tasks, task)
	if len(s.tasks) == 1 {
		return s.err
	}
	return nil
}

func (s *failFirstTaskProjectionSink) ProjectScheduledRun(context.Context, ScheduledRun) error {
	return nil
}

func (s *recordingProjectionSink) ProjectScheduledTask(_ context.Context, task ScheduledTask) error {
	s.tasks = append(s.tasks, task)
	return nil
}

func (s *recordingProjectionSink) ProjectScheduledRun(_ context.Context, run ScheduledRun) error {
	s.runs = append(s.runs, run)
	return nil
}

func TestRepositoryProjectsSchedulerRecordsAfterDurableSave(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	repo := NewRepository(env.DBRouter)
	sink := &recordingProjectionSink{}
	repo.SetProjectionSink(sink)
	ctx := context.Background()

	task := schedulerTestTask("task-1", schedulerTestTime)
	if err := repo.SaveTask(ctx, task); err != nil {
		t.Fatal(err)
	}
	run := ScheduledRun{ScheduledRunID: "run-1", ScheduledTaskID: task.ScheduledTaskID, Status: RunStatusQueued, CreatedAt: schedulerTestTime}
	if err := repo.SaveRun(ctx, run); err != nil {
		t.Fatal(err)
	}

	if len(sink.tasks) != 1 || sink.tasks[0].ScheduledTaskID != task.ScheduledTaskID {
		t.Fatalf("projected tasks = %+v", sink.tasks)
	}
	if len(sink.runs) != 1 || sink.runs[0].ScheduledRunID != run.ScheduledRunID {
		t.Fatalf("projected runs = %+v", sink.runs)
	}
	if _, err := repo.GetTask(ctx, task.ScheduledTaskID); err != nil {
		t.Fatalf("task was not durably saved before projection: %v", err)
	}
}

func TestRepositoryMutateTaskRetriesProjectionAfterDurableNoop(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	repo := NewRepository(env.DBRouter)
	ctx := context.Background()
	task := schedulerTestTask("task-projection-retry", schedulerTestTime)
	if err := repo.SaveTask(ctx, task); err != nil {
		t.Fatal(err)
	}
	failure := errors.New("projection unavailable")
	sink := &failFirstTaskProjectionSink{err: failure}
	repo.SetProjectionSink(sink)

	_, err := repo.MutateTask(ctx, task.ScheduledTaskID, func(current *ScheduledTask) error {
		current.Enabled = false
		current.Status = TaskStatusArchived
		return nil
	})
	if !errors.Is(err, failure) {
		t.Fatalf("first projection error = %v, want %v", err, failure)
	}
	stored, err := repo.GetTask(ctx, task.ScheduledTaskID)
	if err != nil || stored.Enabled || stored.Status != TaskStatusArchived {
		t.Fatalf("durable task after projection failure = %+v, err=%v", stored, err)
	}
	projected, err := repo.MutateTask(ctx, task.ScheduledTaskID, func(*ScheduledTask) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	if projected.Enabled || projected.Status != TaskStatusArchived || len(sink.tasks) != 2 || sink.tasks[1].Status != TaskStatusArchived {
		t.Fatalf("projection retry = task:%+v calls:%+v", projected, sink.tasks)
	}
}
