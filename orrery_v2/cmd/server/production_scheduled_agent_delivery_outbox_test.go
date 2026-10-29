package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	agentcore "orrery_v2/internal/agent/core"
	runtimedaemon "orrery_v2/internal/agent/runtime/daemon"
	schedulerapp "orrery_v2/internal/app/scheduler"
	"orrery_v2/internal/gateway/channel"
	"orrery_v2/internal/platform/testkit"
)

func TestProductionScheduledAgentDeliveryOutboxIsIdentityIdempotent(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	ctx := context.Background()
	scheduledRun := scheduledDeliveryOutboxTestRun(env, "scheduled-delivery-idempotent", "runtime-delivery-idempotent")
	runtimeRun := scheduledDeliveryOutboxTestRuntime(scheduledRun, scheduledRun.RuntimeRunID)
	deliveryOutbox := productionScheduledAgentDeliveryOutbox{jobs: env.Outbox}

	if err := deliveryOutbox.Schedule(ctx, runtimeRun, scheduledRun); err != nil {
		t.Fatal(err)
	}
	if err := deliveryOutbox.Schedule(ctx, runtimeRun, scheduledRun); err != nil {
		t.Fatalf("repeat schedule: %v", err)
	}
	jobID := productionScheduledAgentDeliveryJobID(scheduledRun.ScheduledRunID)
	job, ok := env.Outbox.Get(ctx, jobID)
	if !ok || job.Topic != productionScheduledAgentDeliveryTopic || job.IdempotencyKey != jobID {
		t.Fatalf("persisted delivery job = %+v, found=%v", job, ok)
	}

	conflicting := runtimeRun
	conflicting.RunID = "runtime-delivery-conflict"
	if err := deliveryOutbox.Schedule(ctx, conflicting, scheduledRun); err == nil {
		t.Fatal("conflicting runtime identity reused an existing delivery job")
	}
}

func TestProductionScheduledAgentDeliveryHandlerPersistsReceipt(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	ctx := context.Background()
	repository := schedulerapp.NewRepository(env.DBRouter)
	scheduledRun := scheduledDeliveryOutboxTestRun(env, "scheduled-delivery-receipt", "runtime-delivery-receipt")
	if err := repository.SaveRun(ctx, scheduledRun); err != nil {
		t.Fatal(err)
	}
	runtimeRun := scheduledDeliveryOutboxTestRuntime(scheduledRun, scheduledRun.RuntimeRunID)
	deliveryOutbox := productionScheduledAgentDeliveryOutbox{jobs: env.Outbox}
	if err := deliveryOutbox.Schedule(ctx, runtimeRun, scheduledRun); err != nil {
		t.Fatal(err)
	}
	job, ok := env.Outbox.Get(ctx, productionScheduledAgentDeliveryJobID(scheduledRun.ScheduledRunID))
	if !ok {
		t.Fatal("scheduled delivery job was not found")
	}
	sender := &capturingScheduledDeliverySender{result: channel.DeliveryResult{ExternalMessageID: "feishu-message-1"}}
	handler := productionScheduledAgentDeliveryHandler{
		runs: repository, tracker: productionSchedulerRunTracker{repo: repository, clock: env.Clock},
		sender: sender, authorizer: allowScheduledDeliveryAuthorizer{},
	}
	if err := handler.HandleOutbox(ctx, job); err != nil {
		t.Fatal(err)
	}
	stored, err := repository.GetRun(ctx, scheduledRun.ScheduledRunID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.DeliveryStatus != "delivered" || stored.ExternalMessageID != "feishu-message-1" || stored.DeliveryError != "" {
		t.Fatalf("delivery state = %+v", stored)
	}
	expectedKey := "scheduled-result:" + runtimeRun.RunID.String()
	if sender.calls != 1 || sender.delivery.ID != expectedKey || sender.delivery.IdempotencyKey != expectedKey {
		t.Fatalf("delivery = %+v, calls=%d", sender.delivery, sender.calls)
	}
}

func TestProductionScheduledAgentDeliveryHandlerSendsDurableBlockedReport(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	ctx := context.Background()
	repository := schedulerapp.NewRepository(env.DBRouter)
	scheduledRun := scheduledDeliveryOutboxTestRun(env, "scheduled-delivery-blocked", "runtime-delivery-blocked")
	scheduledRun.Status = schedulerapp.RunStatusFailed
	scheduledRun.FailureReason = "Ontology association was not confirmed."
	scheduledRun.Result.Summary = scheduledRun.FailureReason
	if err := repository.SaveRun(ctx, scheduledRun); err != nil {
		t.Fatal(err)
	}
	runtimeRun := scheduledDeliveryOutboxTestRuntime(scheduledRun, scheduledRun.RuntimeRunID)
	deliveryOutbox := productionScheduledAgentDeliveryOutbox{jobs: env.Outbox}
	if err := deliveryOutbox.Schedule(ctx, runtimeRun, scheduledRun); err != nil {
		t.Fatal(err)
	}
	job, ok := env.Outbox.Get(ctx, productionScheduledAgentDeliveryJobID(scheduledRun.ScheduledRunID))
	if !ok {
		t.Fatal("blocked result delivery job was not found")
	}
	sender := &capturingScheduledDeliverySender{result: channel.DeliveryResult{ExternalMessageID: "feishu-message-blocked"}}
	handler := productionScheduledAgentDeliveryHandler{
		runs: repository, tracker: productionSchedulerRunTracker{repo: repository, clock: env.Clock},
		sender: sender, authorizer: allowScheduledDeliveryAuthorizer{},
	}
	if err := handler.HandleOutbox(ctx, job); err != nil {
		t.Fatal(err)
	}
	stored, err := repository.GetRun(ctx, scheduledRun.ScheduledRunID)
	if err != nil {
		t.Fatal(err)
	}
	if sender.calls != 1 || sender.delivery.Text != scheduledRun.Result.Summary || stored.DeliveryStatus != "delivered" || stored.ExternalMessageID != "feishu-message-blocked" {
		t.Fatalf("blocked report delivery=%+v calls=%d stored=%+v", sender.delivery, sender.calls, stored)
	}
}

func TestProductionScheduledAgentDeliveryHandlerRetriesReceiptPersistenceWithStableKey(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	ctx := context.Background()
	repository := schedulerapp.NewRepository(env.DBRouter)
	scheduledRun := scheduledDeliveryOutboxTestRun(env, "scheduled-delivery-retry", "runtime-delivery-retry")
	if err := repository.SaveRun(ctx, scheduledRun); err != nil {
		t.Fatal(err)
	}
	runtimeRun := scheduledDeliveryOutboxTestRuntime(scheduledRun, scheduledRun.RuntimeRunID)
	deliveryOutbox := productionScheduledAgentDeliveryOutbox{jobs: env.Outbox}
	if err := deliveryOutbox.Schedule(ctx, runtimeRun, scheduledRun); err != nil {
		t.Fatal(err)
	}
	job, _ := env.Outbox.Get(ctx, productionScheduledAgentDeliveryJobID(scheduledRun.ScheduledRunID))
	sender := &capturingScheduledDeliverySender{result: channel.DeliveryResult{ExternalMessageID: "feishu-message-retry"}}
	tracker := &failingScheduledDeliveryRecorder{
		next:       productionSchedulerRunTracker{repo: repository, clock: env.Clock},
		failStatus: "delivered", err: errors.New("scheduler receipt store unavailable"),
	}
	handler := productionScheduledAgentDeliveryHandler{
		runs: repository, tracker: tracker, sender: sender, authorizer: allowScheduledDeliveryAuthorizer{},
	}
	if err := handler.HandleOutbox(ctx, job); !errors.Is(err, tracker.err) {
		t.Fatalf("first handler error = %v", err)
	}
	tracker.err = nil
	job.Attempts++
	if err := handler.HandleOutbox(ctx, job); err != nil {
		t.Fatalf("retry handler: %v", err)
	}
	if sender.calls != 2 || sender.delivery.ID != "scheduled-result:"+runtimeRun.RunID.String() ||
		sender.delivery.IdempotencyKey != "scheduled-result:"+runtimeRun.RunID.String() {
		t.Fatalf("stable retry delivery = %+v, calls=%d", sender.delivery, sender.calls)
	}
	stored, err := repository.GetRun(ctx, scheduledRun.ScheduledRunID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.DeliveryStatus != "delivered" || stored.ExternalMessageID != "feishu-message-retry" {
		t.Fatalf("retried delivery state = %+v", stored)
	}
}

type failingScheduledDeliveryRecorder struct {
	next       schedulerRunDeliveryTracker
	failStatus string
	err        error
}

func (r *failingScheduledDeliveryRecorder) RecordDelivery(ctx context.Context, runID, status, deliveryError, messageID string) error {
	if status == r.failStatus && r.err != nil {
		return r.err
	}
	return r.next.RecordDelivery(ctx, runID, status, deliveryError, messageID)
}

func TestProductionSchedulerRunReconcilerSchedulesPendingDeliveryAndClosesDeadLetter(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	ctx := context.Background()
	repository := schedulerapp.NewRepository(env.DBRouter)
	run := scheduledDeliveryOutboxTestRun(env, "scheduled-delivery-reconcile", "runtime-delivery-reconcile")
	if err := repository.SaveRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	deliveryOutbox := productionScheduledAgentDeliveryOutbox{jobs: env.Outbox}
	reconciler := productionSchedulerRunReconciler{
		repo: repository, queue: env.ExecutionQueue, clock: env.Clock, deliveries: deliveryOutbox,
	}
	if err := reconciler.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	jobID := productionScheduledAgentDeliveryJobID(run.ScheduledRunID)
	job, ok := env.Outbox.Get(ctx, jobID)
	if !ok || job.Status != "pending" {
		t.Fatalf("reconciled delivery job = %+v, found=%v", job, ok)
	}
	for attempt := 0; attempt < job.MaxAttempts; attempt++ {
		claimed, claimedOK, err := env.Outbox.Claim(ctx, "delivery-test-worker", env.Clock.Now())
		if err != nil || !claimedOK {
			t.Fatalf("claim attempt %d: claimed=%v err=%v", attempt+1, claimedOK, err)
		}
		if err := env.Outbox.Fail(ctx, claimed.ID, "delivery-test-worker", "temporary delivery failure", env.Clock.Now()); err != nil {
			t.Fatalf("fail attempt %d: %v", attempt+1, err)
		}
	}
	dead, _ := env.Outbox.Get(ctx, jobID)
	if dead.Status != "dead_letter" {
		t.Fatalf("exhausted delivery job status = %s", dead.Status)
	}
	if err := reconciler.Reconcile(ctx); err != nil {
		t.Fatalf("close dead-letter delivery: %v", err)
	}
	closed, _ := env.Outbox.Get(ctx, jobID)
	if closed.Status != "dead_letter" || closed.Attempts != job.MaxAttempts {
		t.Fatalf("closed delivery job = %+v", closed)
	}
	stored, err := repository.GetRun(ctx, run.ScheduledRunID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.DeliveryStatus != "dead_letter" || stored.DeliveryError != scheduledDeliveryDeadLetterPublicError() {
		t.Fatalf("closed scheduled delivery state = %+v", stored)
	}
}

func TestProductionScheduledAgentDeliveryHandlerDeadLettersWithPublicError(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	ctx := context.Background()
	repository := schedulerapp.NewRepository(env.DBRouter)
	run := scheduledDeliveryOutboxTestRun(env, "scheduled-delivery-dead-letter", "runtime-delivery-dead-letter")
	if err := repository.SaveRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	runtimeRun := scheduledDeliveryOutboxTestRuntime(run, run.RuntimeRunID)
	deliveryOutbox := productionScheduledAgentDeliveryOutbox{jobs: env.Outbox}
	if err := deliveryOutbox.Schedule(ctx, runtimeRun, run); err != nil {
		t.Fatal(err)
	}
	job, _ := env.Outbox.Get(ctx, productionScheduledAgentDeliveryJobID(run.ScheduledRunID))
	job.MaxAttempts = 1
	sender := &capturingScheduledDeliverySender{err: errors.New("provider secret token=do-not-expose")}
	handler := productionScheduledAgentDeliveryHandler{
		runs: repository, tracker: productionSchedulerRunTracker{repo: repository, clock: env.Clock},
		sender: sender, authorizer: allowScheduledDeliveryAuthorizer{},
	}
	err := handler.HandleOutbox(ctx, job)
	if err == nil || strings.Contains(err.Error(), "do-not-expose") {
		t.Fatalf("terminal handler error = %v, want a sanitized retry error", err)
	}
	stored, getErr := repository.GetRun(ctx, run.ScheduledRunID)
	if getErr != nil {
		t.Fatal(getErr)
	}
	if stored.DeliveryStatus != "dead_letter" || stored.DeliveryError != scheduledDeliveryPublicError(sender.err) {
		t.Fatalf("dead-letter delivery state = %+v", stored)
	}
}

func scheduledDeliveryOutboxTestRun(env *testkit.Env, id, runtimeRunID string) schedulerapp.ScheduledRun {
	return schedulerapp.ScheduledRun{
		ScheduledRunID: id, ScheduledTaskID: "task-" + id,
		Status: schedulerapp.RunStatusCompleted, TargetMode: schedulerapp.TargetModeAgentTask,
		ConfigurationSnapshotVersion: 1, TenantID: "system", ScheduledTaskTitle: "Scheduled delivery test",
		Delivery:  schedulerapp.DeliveryPolicy{Mode: "announce", Channel: "feishu", AccountID: "orrery", RecipientID: "oc_delivery", Format: "text"},
		MissionID: "mission-" + id, TaskID: "mission-task-" + id, AgentID: "agent-" + id, RuntimeRunID: runtimeRunID,
		ResponsibilityID: "responsibility-" + id, Result: schedulerapp.RunResult{Summary: "durable scheduled result"},
		CreatedAt: env.Clock.Now(), CompletedAt: env.Clock.Now(),
	}
}

func scheduledDeliveryOutboxTestRuntime(run schedulerapp.ScheduledRun, runtimeRunID string) runtimedaemon.AgentRun {
	return runtimedaemon.AgentRun{
		RunID: agentcore.AgentRunID(runtimeRunID), AgentID: agentcore.AgentID(run.AgentID), MissionID: run.MissionID, TaskID: run.TaskID,
		Request: agentcore.RuntimeRequest{
			TenantID: "system",
			ContextPack: agentcore.ContextPack{ResponsibilityContext: &agentcore.ResponsibilityContext{
				ResponsibilityID: run.ResponsibilityID, MissionID: run.MissionID, TaskID: run.TaskID, AgentProfileID: run.AgentID,
			}},
			RuntimeOptions: agentcore.RuntimeOptions{ProviderSpecific: map[string]string{
				"run_source": schedulerAgentTaskRunSource, "scheduled_task_id": run.ScheduledTaskID, "scheduled_run_id": run.ScheduledRunID,
			}},
		},
	}
}
