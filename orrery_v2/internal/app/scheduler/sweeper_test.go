package scheduler

import (
	"context"
	"errors"
	"testing"
	"time"

	discussionsession "orrery_v2/internal/app/discussionsession"
	"orrery_v2/internal/platform/clock"
	"orrery_v2/internal/platform/idgen"
	"orrery_v2/internal/platform/testkit"
)

func TestSweeperDispatchesDueEnabledTaskAndDisablesOneTimeSchedule(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	repo := NewRepository(env.DBRouter)
	ctx := context.Background()
	task := schedulerTestTask("sched-due", schedulerTestTime.Add(-time.Minute))
	if err := repo.SaveTask(ctx, task); err != nil {
		t.Fatal(err)
	}
	discussion := &fakeDiscussionPort{conversation: discussionsession.ConversationDTO{ConversationID: "conversation-due"}}
	service := NewService(ServiceOptions{
		Clock:       clock.NewFake(schedulerTestTime),
		IDGenerator: idgen.NewDeterministic("run"),
		Repository:  repo,
	})
	sweeper := NewSweeper(SweeperOptions{
		Service:    service,
		Repository: repo,
		Dispatcher: NewDispatcher(DispatcherOptions{
			Clock:      clock.NewFake(schedulerTestTime.Add(time.Minute)),
			Repository: repo,
			Discussion: discussion,
		}),
		Clock:     clock.NewFake(schedulerTestTime),
		BatchSize: 10,
	})

	if err := sweeper.Sweep(ctx); err != nil {
		t.Fatal(err)
	}
	runs, err := repo.ListRunsForTask(ctx, task.ScheduledTaskID)
	if err != nil {
		t.Fatal(err)
	}
	updated, err := repo.GetTask(ctx, task.ScheduledTaskID)
	if err != nil {
		t.Fatal(err)
	}

	if len(runs) != 1 || runs[0].Status != RunStatusCompleted || runs[0].TriggerType != RunTriggerDue {
		t.Fatalf("runs = %+v", runs)
	}
	if updated.Enabled || updated.Status != TaskStatusDisabled || !updated.NextRunAt.IsZero() {
		t.Fatalf("updated one-time task = %+v", updated)
	}
	if discussion.cmd.IdempotencyKey != "scheduled-run:run-000001" {
		t.Fatalf("discussion idempotency key = %q", discussion.cmd.IdempotencyKey)
	}
}

func TestSweeperRepairsDisabledOccurrenceWithoutCreatingRun(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	repo := NewRepository(env.DBRouter)
	ctx := context.Background()
	task := schedulerTestTask("disabled-missing-occurrence", time.Time{})
	task.Schedule = Schedule{Type: ScheduleTypeCron, Cron: "*/15 * * * *", Timezone: "UTC"}
	task.Enabled = false
	task.Status = TaskStatusDisabled
	if err := repo.SaveTask(ctx, task); err != nil {
		t.Fatal(err)
	}
	dispatcher := &selectiveFailDispatcher{}
	service := NewService(ServiceOptions{Clock: clock.NewFake(schedulerTestTime), IDGenerator: idgen.NewDeterministic("run"), Repository: repo})
	sweeper := NewSweeper(SweeperOptions{Service: service, Repository: repo, Dispatcher: dispatcher, Clock: clock.NewFake(schedulerTestTime)})

	if err := sweeper.Sweep(ctx); err != nil {
		t.Fatal(err)
	}
	stored, err := repo.GetTask(ctx, task.ScheduledTaskID)
	if err != nil {
		t.Fatal(err)
	}
	runs, err := repo.ListRunsForTask(ctx, task.ScheduledTaskID)
	if err != nil {
		t.Fatal(err)
	}
	want := time.Date(2026, 6, 8, 9, 45, 0, 0, time.UTC)
	if stored.Enabled || stored.Status != TaskStatusDisabled || !stored.NextRunAt.Equal(want) {
		t.Fatalf("stored disabled task = %+v, want occurrence %s", stored, want)
	}
	if len(dispatcher.dispatched) != 0 || len(runs) != 0 {
		t.Fatalf("disabled task dispatched=%+v runs=%+v", dispatcher.dispatched, runs)
	}
}

func TestSweeperDoesNotFailForInvalidDisabledOccurrence(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	repo := NewRepository(env.DBRouter)
	task := schedulerTestTask("disabled-invalid-occurrence", time.Time{})
	task.Schedule = Schedule{Type: ScheduleTypeCron, Cron: "not-a-cron", Timezone: "UTC"}
	task.Enabled = false
	task.Status = TaskStatusDisabled
	if err := repo.SaveTask(context.Background(), task); err != nil {
		t.Fatal(err)
	}
	service := NewService(ServiceOptions{Clock: clock.NewFake(schedulerTestTime), Repository: repo})
	sweeper := NewSweeper(SweeperOptions{Service: service, Repository: repo, Dispatcher: &selectiveFailDispatcher{}, Clock: clock.NewFake(schedulerTestTime)})

	if err := sweeper.Sweep(context.Background()); err != nil {
		t.Fatalf("sweep returned non-fatal occurrence issue: %v", err)
	}
}

type selectiveFailDispatcher struct {
	failTaskID string
	dispatched []string
}

func (d *selectiveFailDispatcher) DispatchRun(_ context.Context, task ScheduledTask, run ScheduledRun) (ScheduledRun, error) {
	d.dispatched = append(d.dispatched, task.ScheduledTaskID)
	if task.ScheduledTaskID == d.failTaskID {
		return run, errors.New("dispatch unavailable")
	}
	return run, nil
}

func TestSweeperContinuesAfterOneTaskFails(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	repo := NewRepository(env.DBRouter)
	ctx := context.Background()
	failedTask := schedulerTestTask("sched-a-fails", schedulerTestTime.Add(-time.Minute))
	successTask := schedulerTestTask("sched-b-succeeds", schedulerTestTime.Add(-time.Minute))
	if err := repo.SaveTask(ctx, failedTask); err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveTask(ctx, successTask); err != nil {
		t.Fatal(err)
	}
	dispatcher := &selectiveFailDispatcher{failTaskID: failedTask.ScheduledTaskID}
	service := NewService(ServiceOptions{Clock: clock.NewFake(schedulerTestTime), IDGenerator: idgen.NewDeterministic("run"), Repository: repo})
	sweeper := NewSweeper(SweeperOptions{Service: service, Repository: repo, Dispatcher: dispatcher, Clock: clock.NewFake(schedulerTestTime)})

	if err := sweeper.Sweep(ctx); err == nil {
		t.Fatal("sweep error = nil, want aggregated dispatch failure")
	}
	if len(dispatcher.dispatched) != 2 {
		t.Fatalf("dispatched tasks = %+v, want both due tasks", dispatcher.dispatched)
	}
	failedAfter, err := repo.GetTask(ctx, failedTask.ScheduledTaskID)
	if err != nil {
		t.Fatal(err)
	}
	successAfter, err := repo.GetTask(ctx, successTask.ScheduledTaskID)
	if err != nil {
		t.Fatal(err)
	}
	if !failedAfter.Enabled || successAfter.Enabled {
		t.Fatalf("task advancement after partial failure: failed=%+v success=%+v", failedAfter, successAfter)
	}
}

func TestSweeperSkipsExpiredMisfireWithoutDispatch(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	repo := NewRepository(env.DBRouter)
	ctx := context.Background()
	task := schedulerTestTask("sched-misfire", schedulerTestTime.Add(-5*time.Minute))
	task.Policy.Misfire = MisfireSkip
	if err := repo.SaveTask(ctx, task); err != nil {
		t.Fatal(err)
	}
	dispatcher := &selectiveFailDispatcher{}
	service := NewService(ServiceOptions{Clock: clock.NewFake(schedulerTestTime), IDGenerator: idgen.NewDeterministic("run"), Repository: repo})
	sweeper := NewSweeper(SweeperOptions{Service: service, Repository: repo, Dispatcher: dispatcher, Clock: clock.NewFake(schedulerTestTime)})

	if err := sweeper.Sweep(ctx); err != nil {
		t.Fatal(err)
	}
	runs, err := repo.ListRunsForTask(ctx, task.ScheduledTaskID)
	if err != nil {
		t.Fatal(err)
	}
	if len(dispatcher.dispatched) != 0 || len(runs) != 1 || runs[0].Status != RunStatusSkipped {
		t.Fatalf("misfire result: dispatched=%+v runs=%+v", dispatcher.dispatched, runs)
	}
}

func TestSweeperAdvancesRecurringTaskAfterDispatch(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	repo := NewRepository(env.DBRouter)
	ctx := context.Background()
	task := schedulerTestTask("sched-cron", schedulerTestTime.Add(-time.Minute))
	task.Schedule = Schedule{Type: ScheduleTypeCron, Cron: "45 9 * * *", Timezone: "UTC"}
	if err := repo.SaveTask(ctx, task); err != nil {
		t.Fatal(err)
	}
	service := NewService(ServiceOptions{
		Clock:       clock.NewFake(schedulerTestTime),
		IDGenerator: idgen.NewDeterministic("run"),
		Repository:  repo,
	})
	sweeper := NewSweeper(SweeperOptions{
		Service:    service,
		Repository: repo,
		Dispatcher: NewDispatcher(DispatcherOptions{
			Clock:      clock.NewFake(schedulerTestTime.Add(time.Minute)),
			Repository: repo,
			Discussion: &fakeDiscussionPort{conversation: discussionsession.ConversationDTO{ConversationID: "conversation-cron"}},
		}),
		Clock:     clock.NewFake(schedulerTestTime),
		BatchSize: 10,
	})

	if err := sweeper.Sweep(ctx); err != nil {
		t.Fatal(err)
	}
	updated, err := repo.GetTask(ctx, task.ScheduledTaskID)
	if err != nil {
		t.Fatal(err)
	}

	wantNext := time.Date(2026, 6, 8, 9, 45, 0, 0, time.UTC)
	if !updated.Enabled || updated.Status != TaskStatusEnabled || !updated.NextRunAt.Equal(wantNext) {
		t.Fatalf("updated cron task = %+v, want next %s", updated, wantNext)
	}
}

func TestSweeperCreatesSkippedRunWhenDueTaskAlreadyActive(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	repo := NewRepository(env.DBRouter)
	ctx := context.Background()
	task := schedulerTestTask("sched-active", schedulerTestTime.Add(-time.Minute))
	if err := repo.SaveTask(ctx, task); err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveRun(ctx, schedulerTestRun("active-run", task.ScheduledTaskID, RunStatusRunning, schedulerTestTime.Add(-time.Minute))); err != nil {
		t.Fatal(err)
	}
	service := NewService(ServiceOptions{
		Clock:       clock.NewFake(schedulerTestTime),
		IDGenerator: idgen.NewDeterministic("run"),
		Repository:  repo,
	})
	sweeper := NewSweeper(SweeperOptions{
		Service:    service,
		Repository: repo,
		Dispatcher: NewDispatcher(DispatcherOptions{
			Clock:      clock.NewFake(schedulerTestTime.Add(time.Minute)),
			Repository: repo,
			Discussion: &fakeDiscussionPort{conversation: discussionsession.ConversationDTO{ConversationID: "conversation-skipped"}},
		}),
		Clock:     clock.NewFake(schedulerTestTime),
		BatchSize: 10,
	})

	if err := sweeper.Sweep(ctx); err != nil {
		t.Fatal(err)
	}
	runs, err := repo.ListRunsForTask(ctx, task.ScheduledTaskID)
	if err != nil {
		t.Fatal(err)
	}

	if len(runs) != 2 || runs[1].Status != RunStatusSkipped || runs[1].FailureReason == "" {
		t.Fatalf("runs = %+v", runs)
	}
}
