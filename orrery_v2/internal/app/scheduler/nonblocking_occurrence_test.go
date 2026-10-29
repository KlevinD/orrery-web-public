package scheduler

import (
	"context"
	"testing"
	"time"

	"orrery_v2/internal/platform/clock"
	"orrery_v2/internal/platform/idgen"
	"orrery_v2/internal/platform/testkit"
)

func TestNonblockingAgentScheduleDispatchesNewOccurrenceWithOldActiveRun(t *testing.T) {
	for _, oldStatus := range []RunStatus{RunStatusQueued, RunStatusRunning, RunStatusFailed} {
		t.Run(string(oldStatus), func(t *testing.T) {
			env := testkit.New(t)
			defer env.Close()
			ctx := context.Background()
			repo := NewRepository(env.DBRouter)
			fakeClock := clock.NewFake(schedulerTestTime)
			service := NewService(ServiceOptions{Clock: fakeClock, IDGenerator: idgen.NewDeterministic("new-occurrence"), Repository: repo, MissionLifecycle: &recordingScheduledMissionLifecycle{missionID: "article-mission"}})
			task := schedulerTestTask("daily-article", schedulerTestTime.Add(-time.Minute))
			task.Schedule = Schedule{Type: ScheduleTypeCron, Cron: "0 9 * * *", Timezone: "Asia/Shanghai"}
			task.Target = Target{Mode: TargetModeAgentTask, AgentID: "agent-author", ApprovalAssigneeID: "human-reviewer", TaskGoal: "Produce the article and submit for formal approval"}
			task.Execution.TimeoutSeconds = 5400
			if err := repo.SaveTask(ctx, task); err != nil {
				t.Fatal(err)
			}
			old := schedulerTestRun("old-unfinished", task.ScheduledTaskID, oldStatus, schedulerTestTime.Add(-48*time.Hour))
			old.CompletedAt = time.Time{}
			if err := repo.SaveRun(ctx, old); err != nil {
				t.Fatal(err)
			}
			policy := task.Policy
			policy.Concurrency = ConcurrencyAllowParallel
			updated, err := service.UpdateTask(ctx, schedulerTestViewer(), UpdateTaskRequest{ScheduledTaskID: task.ScheduledTaskID, Policy: &policy})
			if err != nil {
				t.Fatal(err)
			}
			// UpdateTask advances the next occurrence. Make that actual occurrence due.
			fakeClock.Advance(updated.NextRunAt.Sub(fakeClock.Now()))
			agent := &fakeAgentTaskPort{receipt: AgentTaskReceipt{CommandID: "new-responsibility", AgentID: "agent-author", MissionID: "article-mission", TaskID: "new-article-task", ResponsibilityID: "new-responsibility"}}
			dispatcher := NewDispatcher(DispatcherOptions{Clock: fakeClock, Repository: repo, AgentTasks: agent, CreatorPermissions: schedulerTestCreatorResolver()})
			sweeper := NewSweeper(SweeperOptions{Service: service, Repository: repo, Dispatcher: dispatcher, Clock: fakeClock})
			for range 2 {
				if err := sweeper.Sweep(ctx); err != nil {
					t.Fatal(err)
				}
			}
			runs, err := repo.ListRunsForTask(ctx, task.ScheduledTaskID)
			if err != nil || len(runs) != 2 || agent.calls != 1 {
				t.Fatalf("new occurrence not dispatched exactly once: runs=%+v calls=%d err=%v", runs, agent.calls, err)
			}
			var current ScheduledRun
			for _, run := range runs {
				if run.ScheduledRunID != old.ScheduledRunID {
					current = run
				}
			}
			if current.Status != RunStatusRunning || current.ConfigurationSnapshot == nil || current.ConfigurationSnapshot.Policy.Concurrency != ConcurrencyAllowParallel || agent.req.RunID != current.ScheduledRunID || agent.req.ApprovalAssigneeID != task.Target.ApprovalAssigneeID || agent.req.Execution.TimeoutSeconds != 5400 {
				t.Fatalf("new run lost its execution or approval boundaries: run=%+v request=%+v", current, agent.req)
			}
			if _, err := dispatcher.DispatchRun(ctx, updated, current); err != nil || agent.calls != 1 {
				t.Fatalf("same occurrence dispatched again: calls=%d err=%v", agent.calls, err)
			}
			previous, err := repo.GetRun(ctx, old.ScheduledRunID)
			if err != nil || previous.Status != oldStatus || !previous.CompletedAt.IsZero() {
				t.Fatalf("previous occurrence was rewritten: %+v err=%v", previous, err)
			}
			request := RunNowRequest{ScheduledTaskID: task.ScheduledTaskID, IdempotencyKey: "one-manual-request"}
			manual, err := service.PrepareManualRun(ctx, schedulerTestViewer(), request)
			if err != nil || manual.Status != RunStatusQueued {
				t.Fatalf("manual run blocked by previous work: %+v err=%v", manual, err)
			}
			replay, err := service.PrepareManualRun(ctx, schedulerTestViewer(), request)
			if err != nil || replay.ScheduledRunID != manual.ScheduledRunID {
				t.Fatalf("manual idempotency lost: %+v err=%v", replay, err)
			}
		})
	}
}
