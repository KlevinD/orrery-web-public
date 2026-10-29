package main

import (
	"context"
	"testing"
	"time"

	"orrery_v2/internal/app/responsibility"
	schedulerapp "orrery_v2/internal/app/scheduler"
	"orrery_v2/internal/platform/testkit"
	"orrery_v2/internal/platform/uow"
)

func TestProductionSchedulerIdleOccurrenceExpiry(t *testing.T) {
	for _, test := range []struct {
		name        string
		age         time.Duration
		live        bool
		completed   bool
		wantExpired bool
	}{
		{name: "expired unaccepted occurrence", age: 48 * time.Hour, wantExpired: true},
		{name: "current unaccepted occurrence", age: time.Minute},
		{name: "live attempt survives an old occurrence", age: 48 * time.Hour, live: true},
		{name: "successful runtime awaits business recovery", age: 48 * time.Hour, completed: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			env := testkit.New(t)
			defer env.Close()
			ctx := context.Background()
			repo := schedulerapp.NewRepository(env.DBRouter)
			responsibilities := responsibility.NewRepository(env.DBRouter, env.UnitOfWork)
			var assignment responsibility.AssignmentRecord
			var activation responsibility.ActivationRecord
			if err := env.UnitOfWork.Do(ctx, func(ctx context.Context, tx uow.Tx) error {
				var err error
				assignment, activation, err = responsibilities.AssignMissionTaskTx(ctx, tx, responsibility.MissionTaskAssignmentInput{
					TenantID: "tenant-1", MissionID: "mission-expiry", TaskID: "task-expiry", AgentProfileID: "agent-1",
					SourceKind: schedulerAgentTaskRunSource, SourceID: "sched-expiry", SourceStepID: "run-expiry",
					ActivationCause: responsibility.ActivationCauseAssigned, AcceptBy: env.Clock.Now().Add(time.Minute),
				}, env.Clock.Now())
				return err
			}); err != nil {
				t.Fatal(err)
			}
			if test.live || test.completed {
				claimed, ok, err := responsibilities.ClaimActivation(ctx, activation.ID, "worker", "claim-expiry", env.Clock.Now(), time.Minute, 1)
				if err != nil || !ok {
					t.Fatalf("claim ok=%v err=%v", ok, err)
				}
				attempt, err := responsibilities.MarkDispatched(ctx, activation.ID, claimed.ClaimToken, responsibility.RunIDForActivation(activation.ID), "pi", env.Clock.Now())
				if err != nil {
					t.Fatal(err)
				}
				if test.completed {
					if _, err := responsibilities.CompleteAttempt(ctx, attempt.ID, responsibility.AttemptOutcomeCompleted, nil, env.Clock.Now()); err != nil {
						t.Fatal(err)
					}
				}
				assignment, _ = responsibilities.GetAssignment(ctx, assignment.ID)
			}
			task := schedulerapp.ScheduledTask{
				ScheduledTaskID: "sched-expiry", TenantID: "tenant-1", Target: schedulerapp.Target{Mode: schedulerapp.TargetModeAgentTask},
				Schedule: schedulerapp.Schedule{Type: schedulerapp.ScheduleTypeCron, Cron: "0 9 * * *", Timezone: "Asia/Shanghai"},
				Policy:   schedulerapp.Policy{Concurrency: schedulerapp.ConcurrencySkipIfRunning},
			}
			run := schedulerapp.ScheduledRun{
				ScheduledRunID: "run-expiry", ScheduledTaskID: "sched-expiry", TenantID: "tenant-1", TargetMode: schedulerapp.TargetModeAgentTask,
				ConfigurationSnapshot: &task, ConfigurationSnapshotVersion: 1, Status: schedulerapp.RunStatusRunning,
				ResponsibilityID: assignment.ID, MissionID: assignment.MissionID, TaskID: assignment.WorkID, AgentID: assignment.AccountableAgent,
				CreatedAt: env.Clock.Now().Add(-test.age), Attempt: 1,
			}
			if err := repo.SaveRun(ctx, run); err != nil {
				t.Fatal(err)
			}
			r := productionSchedulerRunReconciler{repo: repo, responsibilities: responsibilities, clock: env.Clock}
			expired, err := r.expireIdleScheduledOccurrence(ctx, run, assignment)
			if err != nil || expired != test.wantExpired {
				t.Fatalf("expired=%v want=%v err=%v", expired, test.wantExpired, err)
			}
			got, err := repo.GetRun(ctx, run.ScheduledRunID)
			if err != nil {
				t.Fatal(err)
			}
			if test.wantExpired && (got.Status != schedulerapp.RunStatusFailed || got.CompletedAt.IsZero() || got.FailureReason == "") {
				t.Fatalf("expired occurrence was not terminal: %+v", got)
			}
			if !test.wantExpired && got.Status != schedulerapp.RunStatusRunning {
				t.Fatalf("current/live/successful occurrence was changed: %+v", got)
			}
		})
	}
}
