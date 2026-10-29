package main

import (
	"context"
	"errors"
	"reflect"
	"testing"

	agentcore "orrery_v2/internal/agent/core"
	runtimedaemon "orrery_v2/internal/agent/runtime/daemon"
	"orrery_v2/internal/app/responsibility"
	schedulerapp "orrery_v2/internal/app/scheduler"
	"orrery_v2/internal/platform/testkit"
)

type scheduledRetryAttemptFixture struct {
	assignment responsibility.AssignmentRecord
	attempts   []responsibility.AttemptRecord
}

func (f scheduledRetryAttemptFixture) GetAssignment(context.Context, string) (responsibility.AssignmentRecord, error) {
	return f.assignment, nil
}

func (f scheduledRetryAttemptFixture) ListAttempts(context.Context, string) ([]responsibility.AttemptRecord, error) {
	return f.attempts, nil
}

func scheduledRetryFixture() (schedulerapp.ScheduledRun, scheduledRetryAttemptFixture) {
	run := schedulerapp.ScheduledRun{
		ScheduledRunID: "generation", ScheduledTaskID: "schedule", TargetMode: schedulerapp.TargetModeAgentTask,
		TenantID: "tenant", MissionID: "mission", TaskID: "task", AgentID: "agent", ResponsibilityID: "responsibility",
		RuntimeRunID: "old-runtime", Attempt: 3, ConfigurationSnapshotVersion: 1,
		Status: schedulerapp.RunStatusFailed, FailureReason: "old blocker",
		Result: schedulerapp.RunResult{Summary: "old blocker"}, TraceRef: "trace://old",
	}
	f := scheduledRetryAttemptFixture{assignment: responsibility.AssignmentRecord{
		ResponsibilityAssignment: responsibility.ResponsibilityAssignment{ID: run.ResponsibilityID, TenantID: run.TenantID,
			WorkID: run.TaskID, AccountableAgent: run.AgentID, State: responsibility.AssignmentStateActive},
		MissionID: run.MissionID, SourceKind: schedulerAgentTaskRunSource, SourceID: run.ScheduledTaskID, SourceStepID: run.ScheduledRunID,
	}}
	for i, runtimeID := range []string{"old-runtime", "new-runtime"} {
		f.attempts = append(f.attempts, responsibility.AttemptRecord{
			ResponsibilityAttempt: responsibility.ResponsibilityAttempt{TenantID: run.TenantID, ResponsibilityID: run.ResponsibilityID,
				AgentRunID: runtimeID, Attempt: i + 3}, AgentProfileID: run.AgentID,
		})
	}
	f.attempts[0].Outcome = responsibility.AttemptOutcomeBlocked
	return run, f
}

func TestScheduledResponsibilityRetryPersistsLatestResult(t *testing.T) {
	for _, blocked := range []bool{false, true} {
		t.Run(map[bool]string{false: "completed", true: "blocked"}[blocked], func(t *testing.T) {
			env := testkit.New(t)
			defer env.Close()
			ctx := context.Background()
			run, fixture := scheduledRetryFixture()
			fixture.attempts[1].StartedAt = env.Clock.Now()
			repo := schedulerapp.NewRepository(env.DBRouter)
			if err := repo.SaveRun(ctx, run); err != nil {
				t.Fatal(err)
			}
			tracker := productionSchedulerRunTracker{repo: repo, clock: env.Clock, responsibilities: fixture}
			expected := run
			expected.RuntimeRunID = "new-runtime"
			if err := tracker.StartAgentRun(ctx, expected, 1); err != nil {
				t.Fatal(err)
			}
			active, err := repo.GetRun(ctx, run.ScheduledRunID)
			if err != nil {
				t.Fatal(err)
			}
			if active.RuntimeRunID != expected.RuntimeRunID || active.Status != schedulerapp.RunStatusRunning ||
				active.Attempt != 4 || active.TraceRef != "" || scheduledRunHasResult(active.Result) || !active.CompletedAt.IsZero() {
				t.Fatalf("retry was not rebound to the latest recorded attempt: %+v", active)
			}
			result := schedulerapp.RunResult{Summary: "latest durable result"}
			if blocked {
				err = tracker.FailAgentRun(ctx, expected, result, "trace://new", env.Clock.Now(), errors.New("quality blocked"))
			} else {
				err = tracker.CompleteAgentRun(ctx, expected, result, "trace://new", env.Clock.Now())
			}
			if err != nil {
				t.Fatal(err)
			}
			terminal, err := repo.GetRun(ctx, run.ScheduledRunID)
			if err != nil {
				t.Fatal(err)
			}
			if terminal.Result.Summary != result.Summary || terminal.TraceRef != "trace://new" || terminal.RuntimeRunID != "new-runtime" {
				t.Fatalf("latest result was not persisted: %+v", terminal)
			}
			if err := tracker.StartAgentRun(ctx, expected, 3); err != nil {
				t.Fatal(err)
			}
			replayed, _ := repo.GetRun(ctx, run.ScheduledRunID)
			if !reflect.DeepEqual(terminal, replayed) {
				t.Fatal("queue replay changed a durable result")
			}
			if err := tracker.CompleteAgentRun(ctx, run, result, "trace://old", env.Clock.Now()); !errors.Is(err, responsibility.ErrConflict) {
				t.Fatalf("stale completion error = %v", err)
			}
		})
	}
}

func TestScheduledResponsibilityRetryRejectsUnrelatedExecution(t *testing.T) {
	cases := []struct {
		name   string
		change func(*schedulerapp.ScheduledRun, *scheduledRetryAttemptFixture)
	}{
		{"stale runtime", func(r *schedulerapp.ScheduledRun, f *scheduledRetryAttemptFixture) { r.RuntimeRunID = "old-runtime" }},
		{"other tenant", func(r *schedulerapp.ScheduledRun, f *scheduledRetryAttemptFixture) { f.attempts[1].TenantID = "other" }},
		{"other agent", func(r *schedulerapp.ScheduledRun, f *scheduledRetryAttemptFixture) {
			f.attempts[1].AgentProfileID = "other"
		}},
		{"other generation", func(r *schedulerapp.ScheduledRun, f *scheduledRetryAttemptFixture) {
			f.assignment.SourceStepID = "other"
		}},
		{"live previous runtime", func(r *schedulerapp.ScheduledRun, f *scheduledRetryAttemptFixture) { f.attempts[0].Outcome = "" }},
		{"unrelated previous runtime", func(r *schedulerapp.ScheduledRun, f *scheduledRetryAttemptFixture) {
			f.attempts[0].AgentRunID = "other"
		}},
		{"completed occurrence", func(r *schedulerapp.ScheduledRun, f *scheduledRetryAttemptFixture) {
			r.Status = schedulerapp.RunStatusCompleted
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := testkit.New(t)
			defer env.Close()
			ctx := context.Background()
			run, fixture := scheduledRetryFixture()
			expected := run
			expected.RuntimeRunID = "new-runtime"
			tc.change(&expected, &fixture)
			if tc.name == "completed occurrence" {
				run.Status = expected.Status
			}
			repo := schedulerapp.NewRepository(env.DBRouter)
			if err := repo.SaveRun(ctx, run); err != nil {
				t.Fatal(err)
			}
			tracker := productionSchedulerRunTracker{repo: repo, clock: env.Clock, responsibilities: fixture}
			if err := tracker.StartAgentRun(ctx, expected, 1); !errors.Is(err, responsibility.ErrConflict) {
				t.Fatalf("error = %v", err)
			}
			stored, _ := repo.GetRun(ctx, run.ScheduledRunID)
			if !reflect.DeepEqual(run, stored) {
				t.Fatal("rejected retry mutated the stored occurrence")
			}
		})
	}
}

func TestScheduledResponsibilityRetryBindsFirstExecution(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	ctx := context.Background()
	run, fixture := scheduledRetryFixture()
	run.RuntimeRunID, run.TraceRef, run.FailureReason = "", "", ""
	run.Result = schedulerapp.RunResult{}
	run.Status, run.Attempt = schedulerapp.RunStatusQueued, 0
	fixture.attempts = fixture.attempts[1:]
	fixture.attempts[0].Attempt = 1
	expected := run
	expected.RuntimeRunID = "new-runtime"
	repo := schedulerapp.NewRepository(env.DBRouter)
	if err := repo.SaveRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	tracker := productionSchedulerRunTracker{repo: repo, clock: env.Clock, responsibilities: fixture}
	if err := tracker.StartAgentRun(ctx, expected, 1); err != nil {
		t.Fatal(err)
	}
	stored, err := repo.GetRun(ctx, run.ScheduledRunID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.RuntimeRunID != "new-runtime" || stored.Attempt != 1 || stored.Status != schedulerapp.RunStatusRunning {
		t.Fatalf("first execution was not bound: %+v", stored)
	}
}

type scheduledRetryStartSpy struct {
	productionSchedulerRunTracker
	expected schedulerapp.ScheduledRun
	attempt  int
}

func (s *scheduledRetryStartSpy) StartAgentRun(_ context.Context, expected schedulerapp.ScheduledRun, attempt int) error {
	s.expected, s.attempt = expected, attempt
	return nil
}

func TestScheduledResponsibilityRetryWorkerUsesRunIdentity(t *testing.T) {
	run, _ := scheduledRetryFixture()
	spy := &scheduledRetryStartSpy{}
	worker := productionAgentRunWorker{scheduler: spy}
	runtimeRun := runtimedaemon.AgentRun{RunID: agentcore.AgentRunID("new-runtime"), AgentID: agentcore.AgentID(run.AgentID),
		MissionID: run.MissionID, TaskID: run.TaskID, Request: agentcore.RuntimeRequest{
			TenantID:       run.TenantID,
			ContextPack:    agentcore.ContextPack{ResponsibilityContext: &agentcore.ResponsibilityContext{ResponsibilityID: run.ResponsibilityID}},
			RuntimeOptions: agentcore.RuntimeOptions{ProviderSpecific: map[string]string{"scheduled_task_id": run.ScheduledTaskID, "scheduled_run_id": run.ScheduledRunID}},
		}}
	if err := worker.startScheduledAgentRun(context.Background(), runtimeRun, 2); err != nil {
		t.Fatal(err)
	}
	if spy.attempt != 2 || spy.expected.RuntimeRunID != "new-runtime" || spy.expected.ResponsibilityID != run.ResponsibilityID ||
		spy.expected.ScheduledRunID != run.ScheduledRunID || spy.expected.TenantID != run.TenantID {
		t.Fatalf("worker did not pass the queued execution identity: %+v", spy)
	}
}
