package main

import (
	"context"
	"errors"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	runtimedaemon "orrery_v2/internal/agent/runtime/daemon"
	schedulerapp "orrery_v2/internal/app/scheduler"
	missioncore "orrery_v2/internal/mission/core"
	"orrery_v2/internal/platform/testkit"
)

func TestScheduledRetryDeliveryPreservesPreviousAttempt(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	ctx := context.Background()
	run := scheduledDeliveryOutboxTestRun(env, "retry-delivery", "old-runtime")
	out := productionScheduledAgentDeliveryOutbox{jobs: env.Outbox}
	if err := out.Schedule(ctx, scheduledDeliveryOutboxTestRuntime(run, run.RuntimeRunID), run); err != nil {
		t.Fatal(err)
	}
	legacyID := productionScheduledAgentDeliveryJobID(run.ScheduledRunID)
	previous, _ := env.Outbox.Get(ctx, legacyID)
	run.RuntimeRunID = "new-runtime"
	for i := 0; i < 2; i++ {
		if err := out.Schedule(ctx, scheduledDeliveryOutboxTestRuntime(run, run.RuntimeRunID), run); err != nil {
			t.Fatal(err)
		}
	}
	latest, found := env.Outbox.Get(ctx, legacyID+":"+run.RuntimeRunID)
	if !found || latest.IdempotencyKey != latest.ID {
		t.Fatal("retry delivery was not persisted with a stable execution key")
	}
	unchanged, _ := env.Outbox.Get(ctx, legacyID)
	if string(previous.Payload) != string(unchanged.Payload) {
		t.Fatal("retry overwrote the previous delivery")
	}
	run.MissionID = "unrelated-mission"
	if err := out.Schedule(ctx, scheduledDeliveryOutboxTestRuntime(run, run.RuntimeRunID), run); err == nil {
		t.Fatal("unrelated delivery reused an occurrence")
	}
}

type scheduledCommittedTrackerStub struct{ productionSchedulerRunTracker }

func (scheduledCommittedTrackerStub) CompleteAgentRun(context.Context, schedulerapp.ScheduledRun, schedulerapp.RunResult, string, time.Time) error {
	return nil
}
func (scheduledCommittedTrackerStub) FailAgentRun(context.Context, schedulerapp.ScheduledRun, schedulerapp.RunResult, string, time.Time, error) error {
	return nil
}

type scheduledDeliveryFailureStub struct{ err error }

func (s scheduledDeliveryFailureStub) Schedule(context.Context, runtimedaemon.AgentRun, schedulerapp.ScheduledRun) error {
	return s.err
}

type scheduledResponsibilityReceiptStub struct{ completed, blocked int }

func (s *scheduledResponsibilityReceiptStub) Completed(context.Context, string, string, string) error {
	s.completed++
	return nil
}
func (s *scheduledResponsibilityReceiptStub) Failed(context.Context, string, string, string, runtimedaemon.RuntimeLifecycleKind, error) error {
	s.blocked++
	return nil
}

func TestScheduledRetryDeliveryFailureStillRecordsResponsibility(t *testing.T) {
	for _, blocked := range []bool{false, true} {
		t.Run(map[bool]string{false: "completed", true: "blocked"}[blocked], func(t *testing.T) {
			env := testkit.New(t)
			defer env.Close()
			scheduled := scheduledDeliveryOutboxTestRun(env, "delivery-failure", "runtime")
			run := scheduledDeliveryOutboxTestRuntime(scheduled, scheduled.RuntimeRunID)
			run.Request.RuntimeOptions.ProviderSpecific["delivery_mode"] = "announce"
			receipt := &scheduledResponsibilityReceiptStub{}
			deliveryErr := errors.New("delivery persistence unavailable")
			worker := productionAgentRunWorker{scheduler: scheduledCommittedTrackerStub{}, responsibility: receipt,
				scheduledDeliveryOutbox: scheduledDeliveryFailureStub{err: deliveryErr}}
			result := agentcore.AgentResult{RunID: run.RunID, AgentID: run.AgentID,
				TaskID: missioncore.TaskID(run.TaskID), MissionID: missioncore.MissionID(run.MissionID),
				Summary: agentcore.Summary{Text: "one completed article with four images"}}
			var err error
			if blocked {
				result.Blocker = &agentcore.Blocker{Summary: "quality review blocked"}
				err = worker.failCommittedScheduledResult(context.Background(), run, result, schedulerapp.RunResult{Summary: "blocked"}, agentcore.RuntimeResponse{}, "worker")
			} else {
				err = worker.completeCommittedScheduledResult(context.Background(), run, result, agentcore.RuntimeResponse{}, "worker")
			}
			if !errors.Is(err, deliveryErr) {
				t.Fatalf("delivery error = %v", err)
			}
			if blocked && receipt.blocked != 1 || !blocked && receipt.completed != 1 {
				t.Fatalf("responsibility was left active: %+v", receipt)
			}
		})
	}
}
