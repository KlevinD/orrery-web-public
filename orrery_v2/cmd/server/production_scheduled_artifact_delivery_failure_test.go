package main

import (
	"context"
	"strings"
	"testing"

	schedulerapp "orrery_v2/internal/app/scheduler"
	"orrery_v2/internal/platform/testkit"
)

func TestScheduledDeliveryUnverifiedArtifactStopsWithoutSendingOrRetrying(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	ctx := context.Background()
	repository := schedulerapp.NewRepository(env.DBRouter)
	run := scheduledDeliveryOutboxTestRun(env, "scheduled-missing-briefing", "runtime-missing-briefing")
	run.Status = schedulerapp.RunStatusFailed
	run.FailureReason = "briefing failed artifact verification"
	run.Result.Summary = run.FailureReason
	run.Delivery.ContentArtifactID = "rss-briefing"
	run.Result.Artifacts = nil
	if err := repository.SaveRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	outbox := productionScheduledAgentDeliveryOutbox{jobs: env.Outbox}
	if err := outbox.Schedule(ctx, scheduledDeliveryOutboxTestRuntime(run, run.RuntimeRunID), run); err != nil {
		t.Fatal(err)
	}
	job, _ := env.Outbox.Get(ctx, productionScheduledAgentDeliveryJobID(run.ScheduledRunID))
	sender := &capturingScheduledDeliverySender{}
	handler := productionScheduledAgentDeliveryHandler{
		runs: repository, tracker: productionSchedulerRunTracker{repo: repository, clock: env.Clock},
		sender: sender, authorizer: allowScheduledDeliveryAuthorizer{},
	}
	for attempt := 0; attempt < 2; attempt++ {
		if err := handler.HandleOutbox(ctx, job); err != nil {
			t.Fatalf("immutable missing artifact should not be retried: %v", err)
		}
	}
	stored, err := repository.GetRun(ctx, run.ScheduledRunID)
	if err != nil {
		t.Fatal(err)
	}
	if sender.calls != 0 || stored.DeliveryStatus != "failed" || !strings.Contains(stored.DeliveryError, "artifact") || strings.Contains(stored.DeliveryError, "receipt") {
		t.Fatalf("wrong failure classification: calls=%d status=%s error=%s", sender.calls, stored.DeliveryStatus, stored.DeliveryError)
	}
	if stored.Status != schedulerapp.RunStatusFailed || stored.FailureReason != run.FailureReason {
		t.Fatal("delivery repair rewrote the business failure")
	}
}
