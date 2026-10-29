package main

import (
	"context"
	"fmt"
	"testing"
	"time"

	schedulerapp "orrery_v2/internal/app/scheduler"
	"orrery_v2/internal/platform/testkit"
)

func TestProductionSchedulerIndependentOccurrencesCanBeClaimedWhileOldJobRuns(t *testing.T) {
	for _, concurrency := range []schedulerapp.ConcurrencyPolicy{"", schedulerapp.ConcurrencyAllowParallel} {
		t.Run(string(concurrency), func(t *testing.T) {
			env := testkit.New(t)
			defer env.Close()
			ctx := context.Background()
			repo := schedulerapp.NewRepository(env.DBRouter)
			dispatcher := productionSchedulerDispatcher{queue: env.ExecutionQueue, repo: repo, clock: env.Clock}
			// The immutable occurrence snapshot controls execution even if the
			// task definition is edited back to serial execution afterwards.
			snapshot := schedulerapp.ScheduledTask{ScheduledTaskID: "daily", Policy: schedulerapp.Policy{Concurrency: concurrency, MaxRetries: 2}}
			task := snapshot
			task.Policy = schedulerapp.Policy{Concurrency: schedulerapp.ConcurrencySkipIfRunning}
			for n := range 2 {
				run := schedulerapp.ScheduledRun{ScheduledRunID: fmt.Sprintf("run-%d", n), ScheduledTaskID: task.ScheduledTaskID, Status: schedulerapp.RunStatusQueued, ConfigurationSnapshot: &snapshot}
				if err := repo.SaveRun(ctx, run); err != nil {
					t.Fatal(err)
				}
				// A duplicate delivery must still identify the same durable job.
				for range 2 {
					if _, err := dispatcher.DispatchRun(ctx, task, run); err != nil {
						t.Fatal(err)
					}
				}
				job, found, err := env.ExecutionQueue.Get(ctx, "scheduler-dispatch:"+run.ScheduledRunID)
				if err != nil || !found || job.QueueKey != "scheduler-run:"+run.ScheduledRunID || job.MaxAttempts != 3 {
					t.Fatalf("incorrect durable job: %+v found=%v err=%v", job, found, err)
				}
			}
			first, ok, err := env.Queue.Claim(ctx, "worker-old", env.Clock.Now())
			if err != nil || !ok {
				t.Fatalf("claim old occurrence: ok=%v err=%v", ok, err)
			}
			if err := env.Queue.Start(ctx, first.ID, "worker-old", env.Clock.Now()); err != nil {
				t.Fatal(err)
			}
			second, ok, err := env.Queue.Claim(ctx, "worker-new", env.Clock.Now().Add(time.Second))
			if err != nil || !ok || second.ID == first.ID || second.QueueKey == first.QueueKey {
				t.Fatalf("old runtime blocked the next occurrence: first=%+v second=%+v ok=%v err=%v", first, second, ok, err)
			}
			if third, ok, err := env.Queue.Claim(ctx, "worker-duplicate", env.Clock.Now().Add(2*time.Second)); err != nil || ok {
				t.Fatalf("duplicate job dispatched: %+v ok=%v err=%v", third, ok, err)
			}
		})
	}
}

func TestEnsureDefaultProductionSchedulesUpgradesExistingConcurrencyOnly(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	ctx := context.Background()
	repo := schedulerapp.NewRepository(env.DBRouter)
	for _, status := range []schedulerapp.TaskStatus{schedulerapp.TaskStatusEnabled, schedulerapp.TaskStatusDisabled, schedulerapp.TaskStatusArchived} {
		task := schedulerapp.ScheduledTask{
			TenantID: "system", ScheduledTaskID: "existing-" + string(status), Title: "Existing work",
			Status: status, Enabled: status == schedulerapp.TaskStatusEnabled,
			Schedule:  schedulerapp.Schedule{Type: schedulerapp.ScheduleTypeCron, Cron: "0 9 * * *", Timezone: "Asia/Shanghai"},
			NextRunAt: env.Clock.Now().Add(time.Hour),
			Policy:    schedulerapp.Policy{Concurrency: schedulerapp.ConcurrencySkipIfRunning, MaxRetries: 2},
			Target:    schedulerapp.Target{Mode: schedulerapp.TargetModeAgentTask, AgentID: "author", ApprovalAssigneeID: "reviewer"},
		}
		if err := repo.SaveTask(ctx, task); err != nil {
			t.Fatal(err)
		}
	}
	if err := ensureDefaultProductionScheduledTasks(ctx, repo, env.Clock.Now()); err != nil {
		t.Fatal(err)
	}
	for _, status := range []schedulerapp.TaskStatus{schedulerapp.TaskStatusEnabled, schedulerapp.TaskStatusDisabled, schedulerapp.TaskStatusArchived} {
		task, err := repo.GetTask(ctx, "existing-"+string(status))
		if err != nil || task.Status != status || task.Enabled != (status == schedulerapp.TaskStatusEnabled) || !task.NextRunAt.Equal(env.Clock.Now().Add(time.Hour)) || task.Policy.Concurrency != schedulerapp.ConcurrencyAllowParallel || task.Policy.MaxRetries != 2 || task.Target.ApprovalAssigneeID != "reviewer" {
			t.Fatalf("startup upgrade changed the schedule contract: %+v err=%v", task, err)
		}
	}
}
