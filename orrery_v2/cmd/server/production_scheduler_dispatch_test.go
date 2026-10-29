package main

import (
	"context"
	"errors"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	runtimedaemon "orrery_v2/internal/agent/runtime/daemon"
	"orrery_v2/internal/app/missioncontrol"
	"orrery_v2/internal/app/responsibility"
	schedulerapp "orrery_v2/internal/app/scheduler"
	todoapp "orrery_v2/internal/app/todo"
	missioncore "orrery_v2/internal/mission/core"
	missiontask "orrery_v2/internal/mission/task"
	"orrery_v2/internal/platform/testkit"
	"orrery_v2/internal/platform/uow"
)

func TestProductionSchedulerAgentTaskPropagatesRuntimeAndDeliveryPolicy(t *testing.T) {
	req := schedulerapp.AgentTaskRequest{
		RunID: "run-policy", ScheduledTaskID: "schedule-policy", AgentID: "tiedan", TaskGoal: "collect evidence",
		ApprovalAssigneeID: "human-ding",
		Execution: schedulerapp.ExecutionPolicy{
			TimeoutSeconds: 1800, LLMProfileID: "llm-minimax", SourceModel: "minimax/MiniMax-M3",
			AllowedToolIDs: []string{"mcp.marketing.marketing_wechat_draft_create"},
		},
		Delivery: schedulerapp.DeliveryPolicy{
			Mode: "announce", Channel: "feishu", RecipientID: "oc_group", AccountID: "main",
			ContentArtifactID: "rss-briefing", Format: "markdown", BestEffort: true,
		},
	}
	metadata := schedulerAgentRunProviderSpecific(req, "queue-policy")
	if metadata["timeout_seconds"] != "1800" || metadata["llm_profile_id"] != "llm-minimax" || metadata["source_model"] != "minimax/MiniMax-M3" {
		t.Fatalf("runtime policy metadata = %+v", metadata)
	}
	if metadata[agentcore.RuntimeAllowedToolIDsProviderKey] != `["mcp.marketing.marketing_wechat_draft_create"]` {
		t.Fatalf("allowed tool IDs metadata = %+v", metadata)
	}
	if metadata["approval_assigned_to"] != "human-ding" {
		t.Fatalf("approval assignee metadata = %+v", metadata)
	}
	if metadata["delivery_mode"] != "announce" || metadata["delivery_channel"] != "feishu" || metadata["delivery_recipient_id"] != "oc_group" || metadata["delivery_account_id"] != "main" || metadata["delivery_content_artifact_id"] != "rss-briefing" || metadata["delivery_format"] != "markdown" || metadata["delivery_best_effort"] != "true" {
		t.Fatalf("delivery policy metadata = %+v", metadata)
	}
	run := runtimedaemon.AgentRun{Request: agentcore.RuntimeRequest{RuntimeOptions: agentcore.RuntimeOptions{ProviderSpecific: metadata}}}
	if got := scheduledAgentRunTimeout(run, 10*time.Minute); got != 30*time.Minute {
		t.Fatalf("scheduled run timeout = %s", got)
	}
}

func TestProductionSchedulerDispatcherEnqueuesDurableSerializedJob(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	repo := schedulerapp.NewRepository(env.DBRouter)
	dispatcher := productionSchedulerDispatcher{queue: env.ExecutionQueue, repo: repo, clock: env.Clock}
	task := schedulerapp.ScheduledTask{
		ScheduledTaskID: "sched-1",
		Policy:          schedulerapp.Policy{Concurrency: schedulerapp.ConcurrencySkipIfRunning, MaxRetries: 2},
	}
	run := schedulerapp.ScheduledRun{ScheduledRunID: "run-1", ScheduledTaskID: task.ScheduledTaskID, Status: schedulerapp.RunStatusQueued}
	if err := repo.SaveRun(context.Background(), run); err != nil {
		t.Fatal(err)
	}

	queued, err := dispatcher.DispatchRun(context.Background(), task, run)
	if err != nil {
		t.Fatal(err)
	}
	job, ok, err := env.ExecutionQueue.Get(context.Background(), "scheduler-dispatch:run-1")
	if err != nil {
		t.Fatal(err)
	}
	if !ok || job.Topic != productionSchedulerDispatchTopic || job.QueueKey != "scheduler:sched-1" || job.MaxAttempts != 3 {
		t.Fatalf("dispatch job = %+v, found=%v", job, ok)
	}
	if queued.Status != schedulerapp.RunStatusQueued {
		t.Fatalf("queued run = %+v", queued)
	}
}

func TestSchedulerDispatchAcknowledgesPersistenceFailureAfterSideEffect(t *testing.T) {
	completed := schedulerapp.RunPersistenceError{
		Run: schedulerapp.ScheduledRun{ScheduledRunID: "run-completed", Status: schedulerapp.RunStatusCompleted},
		Err: errors.New("store unavailable"),
	}
	agentAccepted := schedulerapp.RunPersistenceError{
		Run: schedulerapp.ScheduledRun{ScheduledRunID: "run-agent", Status: schedulerapp.RunStatusRunning, TargetMode: schedulerapp.TargetModeAgentTask, CommandID: "run-agent"},
		Err: errors.New("store unavailable"),
	}
	beforeSideEffect := schedulerapp.RunPersistenceError{
		Run: schedulerapp.ScheduledRun{ScheduledRunID: "run-before", Status: schedulerapp.RunStatusRunning},
		Err: errors.New("store unavailable"),
	}
	if !acknowledgeSchedulerPersistenceError(completed) || !acknowledgeSchedulerPersistenceError(agentAccepted) || acknowledgeSchedulerPersistenceError(beforeSideEffect) {
		t.Fatal("scheduler persistence acknowledgement classification is incorrect")
	}
}

func TestProductionSchedulerRunTrackerPersistsAgentLifecycle(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	repo := schedulerapp.NewRepository(env.DBRouter)
	run := schedulerapp.ScheduledRun{ScheduledRunID: "run-agent", ScheduledTaskID: "sched-agent", Status: schedulerapp.RunStatusRunning, TargetMode: schedulerapp.TargetModeAgentTask}
	if err := repo.SaveRun(context.Background(), run); err != nil {
		t.Fatal(err)
	}
	tracker := productionSchedulerRunTracker{repo: repo, clock: env.Clock}

	if err := tracker.Started(context.Background(), run.ScheduledRunID, 2); err != nil {
		t.Fatal(err)
	}
	if err := tracker.AttemptFailed(context.Background(), run.ScheduledRunID, 2, false, errors.New("temporary")); err != nil {
		t.Fatal(err)
	}
	retrying, err := repo.GetRun(context.Background(), run.ScheduledRunID)
	if err != nil {
		t.Fatal(err)
	}
	if retrying.Status != schedulerapp.RunStatusRunning || retrying.Attempt != 2 || retrying.FailureReason != "temporary" || !retrying.CompletedAt.IsZero() {
		t.Fatalf("retrying run = %+v", retrying)
	}
	env.Clock.Advance(time.Minute)
	result := schedulerapp.RunResult{Summary: "completed with evidence", Artifacts: []schedulerapp.RunArtifact{{
		ID: "report", Kind: "report", URI: "/reports/report.md", SizeBytes: 42, EvidenceID: "evidence:report",
	}}}
	if err := tracker.Completed(context.Background(), run.ScheduledRunID, result); err != nil {
		t.Fatal(err)
	}
	completed, err := repo.GetRun(context.Background(), run.ScheduledRunID)
	if err != nil {
		t.Fatal(err)
	}
	if completed.Status != schedulerapp.RunStatusCompleted || completed.CompletedAt.IsZero() || completed.FailureReason != "" || completed.Result.Summary != result.Summary || len(completed.Result.Artifacts) != 1 {
		t.Fatalf("completed run = %+v", completed)
	}
}

func TestProductionSchedulerRunTrackerPersistsBlockedAgentReport(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	ctx := context.Background()
	repo := schedulerapp.NewRepository(env.DBRouter)
	expected := schedulerapp.ScheduledRun{
		ScheduledRunID: "run-agent-blocked", ScheduledTaskID: "sched-agent-blocked", Status: schedulerapp.RunStatusRunning,
		TargetMode: schedulerapp.TargetModeAgentTask, MissionID: "mission-agent-blocked", TaskID: "task-agent-blocked",
		AgentID: "agent-blocked", ResponsibilityID: "responsibility-agent-blocked", RuntimeRunID: "runtime-agent-blocked",
		ConfigurationSnapshotVersion: 1, TenantID: "minvia",
	}
	if err := repo.SaveRun(ctx, expected); err != nil {
		t.Fatal(err)
	}
	result := schedulerapp.RunResult{Summary: "Ontology association was not confirmed.", Artifacts: []schedulerapp.RunArtifact{{ID: "report", Kind: "report", URI: "artifact://report"}}}
	tracker := productionSchedulerRunTracker{repo: repo, clock: env.Clock}
	if err := tracker.FailAgentRun(ctx, expected, result, "artifact://trace", env.Clock.Now(), errors.New("Ontology association was not confirmed.")); err != nil {
		t.Fatal(err)
	}
	failed, err := repo.GetRun(ctx, expected.ScheduledRunID)
	if err != nil {
		t.Fatal(err)
	}
	if failed.Status != schedulerapp.RunStatusFailed || failed.FailureReason != result.Summary || failed.Result.Summary != result.Summary || len(failed.Result.Artifacts) != 1 || failed.DeliveryStatus != "not_requested" {
		t.Fatalf("failed run = %+v", failed)
	}
	if err := tracker.Started(ctx, expected.ScheduledRunID, 2); err != nil {
		t.Fatal(err)
	}
	if err := tracker.AttemptFailed(ctx, expected.ScheduledRunID, 2, true, errors.New("late queue retry")); err != nil {
		t.Fatal(err)
	}
	failed, err = repo.GetRun(ctx, expected.ScheduledRunID)
	if err != nil {
		t.Fatal(err)
	}
	if failed.Status != schedulerapp.RunStatusFailed || failed.FailureReason != result.Summary || failed.Result.Summary != result.Summary || len(failed.Result.Artifacts) != 1 {
		t.Fatalf("durable blocked run was reopened by a late retry: %+v", failed)
	}
	if err := tracker.FailAgentRun(ctx, expected, result, "artifact://trace", env.Clock.Now(), errors.New(result.Summary)); err != nil {
		t.Fatalf("idempotent blocked result: %v", err)
	}
}

func TestProductionSchedulerRunTrackerDoesNotRegressCompletedRun(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	repo := schedulerapp.NewRepository(env.DBRouter)
	ctx := context.Background()
	result := schedulerapp.ScheduledRun{
		ScheduledRunID: "run-terminal", ScheduledTaskID: "sched-terminal", Status: schedulerapp.RunStatusCompleted,
		TargetMode: schedulerapp.TargetModeTodoAutomationScan, Attempt: 2, CommandID: "todo-automation:run-terminal",
		TodoAutomationScan: &todoapp.TodoAutomationScanResult{EligibleCount: 1, PlannedCount: 1}, CompletedAt: env.Clock.Now(),
	}
	if err := repo.SaveRun(ctx, result); err != nil {
		t.Fatal(err)
	}
	tracker := productionSchedulerRunTracker{repo: repo, clock: env.Clock}
	if err := tracker.Started(ctx, result.ScheduledRunID, 2); err != nil {
		t.Fatal(err)
	}
	if err := tracker.AttemptFailed(ctx, result.ScheduledRunID, 2, true, errors.New("late failure")); err != nil {
		t.Fatal(err)
	}
	stored, err := repo.GetRun(ctx, result.ScheduledRunID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != schedulerapp.RunStatusCompleted || stored.CommandID != result.CommandID || stored.TodoAutomationScan == nil || stored.TodoAutomationScan.PlannedCount != 1 || stored.FailureReason != "" {
		t.Fatalf("completed run regressed = %+v", stored)
	}
}

func TestProductionSchedulerRunTrackerCompletesAgentRunWithIdentityCAS(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	ctx := context.Background()
	repo := schedulerapp.NewRepository(env.DBRouter)
	expected := schedulerapp.ScheduledRun{
		ScheduledRunID: "run-agent-cas", ScheduledTaskID: "sched-agent-cas", Status: schedulerapp.RunStatusRunning,
		TargetMode: schedulerapp.TargetModeAgentTask, MissionID: "mission-agent-cas", TaskID: "task-agent-cas",
		AgentID: "agent-cas", ResponsibilityID: "responsibility-agent-cas", FailureReason: "temporary",
		RuntimeRunID:                 "runtime-agent-cas",
		ConfigurationSnapshotVersion: 1, TenantID: "tenant-agent-cas", Delivery: schedulerapp.DeliveryPolicy{Mode: "announce", Channel: "feishu"},
	}
	if err := repo.SaveRun(ctx, expected); err != nil {
		t.Fatal(err)
	}
	tracker := productionSchedulerRunTracker{repo: repo, clock: env.Clock}
	result := schedulerapp.RunResult{Summary: "durable result", Artifacts: []schedulerapp.RunArtifact{{ID: "artifact-1", URI: "artifact://scheduled-runs/artifact-1"}}}
	completedAt := env.Clock.Now().Add(2 * time.Minute)
	if err := tracker.CompleteAgentRun(ctx, expected, result, "trace://agent-cas", completedAt); err != nil {
		t.Fatalf("complete Agent run: %v", err)
	}
	completed, err := repo.GetRun(ctx, expected.ScheduledRunID)
	if err != nil {
		t.Fatal(err)
	}
	if completed.Status != schedulerapp.RunStatusCompleted || !completed.CompletedAt.Equal(completedAt) || completed.FailureReason != "" || completed.Result.Summary != result.Summary || completed.RuntimeRunID != expected.RuntimeRunID {
		t.Fatalf("completed run = %+v", completed)
	}
	if completed.DeliveryStatus != "pending" {
		t.Fatalf("completed delivery status = %q, want pending", completed.DeliveryStatus)
	}
	if completed.TraceRef != "trace://agent-cas" || completed.TraceVersion != 2 || !completed.TraceAvailable || completed.TraceStatus != "hot" {
		t.Fatalf("completed trace = %+v", completed)
	}

	// A retry with the same durable result is idempotent. An empty retry trace
	// must not erase the trace already committed by the first completion.
	if err := tracker.CompleteAgentRun(ctx, expected, result, "", completedAt.Add(time.Hour)); err != nil {
		t.Fatalf("repeat Agent completion: %v", err)
	}
	repeated, err := repo.GetRun(ctx, expected.ScheduledRunID)
	if err != nil {
		t.Fatal(err)
	}
	if !repeated.CompletedAt.Equal(completedAt) || repeated.TraceRef != completed.TraceRef || repeated.TraceStatus != completed.TraceStatus {
		t.Fatalf("idempotent completion changed durable fields: before=%+v after=%+v", completed, repeated)
	}

	conflictingResult := schedulerapp.RunResult{Summary: "different result"}
	if err := tracker.CompleteAgentRun(ctx, expected, conflictingResult, "trace://agent-cas", completedAt); !errors.Is(err, responsibility.ErrConflict) {
		t.Fatalf("conflicting result error = %v, want responsibility.ErrConflict", err)
	}
	if err := tracker.CompleteAgentRun(ctx, expected, result, "trace://different", completedAt); !errors.Is(err, responsibility.ErrConflict) {
		t.Fatalf("conflicting trace error = %v, want responsibility.ErrConflict", err)
	}
	conflictingRuntime := expected
	conflictingRuntime.RuntimeRunID = "runtime-agent-cas-other"
	if err := tracker.CompleteAgentRun(ctx, conflictingRuntime, result, "trace://agent-cas", completedAt); !errors.Is(err, responsibility.ErrConflict) {
		t.Fatalf("conflicting runtime run error = %v, want responsibility.ErrConflict", err)
	}
}

func TestProductionSchedulerRunTrackerRejectsChangedAgentRunIdentity(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	ctx := context.Background()
	repo := schedulerapp.NewRepository(env.DBRouter)
	run := schedulerapp.ScheduledRun{
		ScheduledRunID: "run-agent-identity", ScheduledTaskID: "sched-agent-identity", Status: schedulerapp.RunStatusRunning,
		TargetMode: schedulerapp.TargetModeAgentTask, MissionID: "mission-agent-identity", TaskID: "task-agent-identity",
		AgentID: "agent-identity", ResponsibilityID: "responsibility-agent-identity",
	}
	if err := repo.SaveRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	tracker := productionSchedulerRunTracker{repo: repo, clock: env.Clock}
	result := schedulerapp.RunResult{Summary: "result"}
	tests := []struct {
		name   string
		change func(*schedulerapp.ScheduledRun)
	}{
		{name: "scheduled run", change: func(expected *schedulerapp.ScheduledRun) { expected.ScheduledRunID = "run-other" }},
		{name: "scheduled task", change: func(expected *schedulerapp.ScheduledRun) { expected.ScheduledTaskID = "sched-other" }},
		{name: "Mission", change: func(expected *schedulerapp.ScheduledRun) { expected.MissionID = "mission-other" }},
		{name: "Mission task", change: func(expected *schedulerapp.ScheduledRun) { expected.TaskID = "task-other" }},
		{name: "Agent", change: func(expected *schedulerapp.ScheduledRun) { expected.AgentID = "agent-other" }},
		{name: "responsibility", change: func(expected *schedulerapp.ScheduledRun) { expected.ResponsibilityID = "responsibility-other" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			expected := run
			test.change(&expected)
			if err := tracker.CompleteAgentRun(ctx, expected, result, "trace://identity", env.Clock.Now()); !errors.Is(err, responsibility.ErrConflict) {
				t.Fatalf("completion error = %v, want responsibility.ErrConflict", err)
			}
		})
	}
	stored, err := repo.GetRun(ctx, run.ScheduledRunID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != schedulerapp.RunStatusRunning || scheduledRunHasResult(stored.Result) || stored.TraceRef != "" {
		t.Fatalf("identity conflict mutated run = %+v", stored)
	}
}

func TestProductionSchedulerRunTrackerOnlyCompletesActiveAgentRun(t *testing.T) {
	for _, status := range []schedulerapp.RunStatus{schedulerapp.RunStatusFailed, schedulerapp.RunStatusSkipped} {
		t.Run(string(status), func(t *testing.T) {
			env := testkit.New(t)
			defer env.Close()
			ctx := context.Background()
			repo := schedulerapp.NewRepository(env.DBRouter)
			run := schedulerapp.ScheduledRun{
				ScheduledRunID: "run-agent-" + string(status), ScheduledTaskID: "sched-agent-terminal", Status: status,
				TargetMode: schedulerapp.TargetModeAgentTask, MissionID: "mission-terminal", TaskID: "task-terminal",
				AgentID: "agent-terminal", ResponsibilityID: "responsibility-terminal", FailureReason: "terminal",
				CompletedAt: env.Clock.Now(),
			}
			if err := repo.SaveRun(ctx, run); err != nil {
				t.Fatal(err)
			}
			tracker := productionSchedulerRunTracker{repo: repo, clock: env.Clock}
			if err := tracker.CompleteAgentRun(ctx, run, schedulerapp.RunResult{Summary: "late result"}, "trace://late", env.Clock.Now()); !errors.Is(err, responsibility.ErrConflict) {
				t.Fatalf("completion error = %v, want responsibility.ErrConflict", err)
			}
			stored, err := repo.GetRun(ctx, run.ScheduledRunID)
			if err != nil {
				t.Fatal(err)
			}
			if stored.Status != status || stored.FailureReason != run.FailureReason || scheduledRunHasResult(stored.Result) || stored.TraceRef != "" {
				t.Fatalf("terminal run mutated = %+v", stored)
			}
		})
	}
}

func TestProductionSchedulerRunTrackerRecoveryPreservesActiveTrace(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	ctx := context.Background()
	repo := schedulerapp.NewRepository(env.DBRouter)
	newRun := func(id string) schedulerapp.ScheduledRun {
		return schedulerapp.ScheduledRun{
			ScheduledRunID: id, ScheduledTaskID: "sched-recovery-trace", Status: schedulerapp.RunStatusRunning,
			TargetMode: schedulerapp.TargetModeAgentTask, MissionID: "mission-recovery-trace", TaskID: "task-" + id,
			AgentID: "agent-recovery-trace", ResponsibilityID: "responsibility-" + id,
			TraceRef: "trace://persisted", TraceVersion: 2, TraceAvailable: true, TraceStatus: "cold",
		}
	}
	result := schedulerapp.RunResult{Summary: "recovered result"}
	tracker := productionSchedulerRunTracker{repo: repo, clock: env.Clock}

	preserved := newRun("run-recovery-trace-empty")
	if err := repo.SaveRun(ctx, preserved); err != nil {
		t.Fatal(err)
	}
	if err := tracker.RecoverCompleted(ctx, preserved, result, "", "not_requested", "", env.Clock.Now()); err != nil {
		t.Fatalf("recover with empty trace: %v", err)
	}
	stored, err := repo.GetRun(ctx, preserved.ScheduledRunID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != schedulerapp.RunStatusCompleted || stored.TraceRef != preserved.TraceRef || stored.TraceVersion != preserved.TraceVersion || stored.TraceStatus != preserved.TraceStatus {
		t.Fatalf("recovery cleared persisted trace = %+v", stored)
	}

	conflicted := newRun("run-recovery-trace-conflict")
	if err := repo.SaveRun(ctx, conflicted); err != nil {
		t.Fatal(err)
	}
	if err := tracker.RecoverCompleted(ctx, conflicted, result, "trace://different", "not_requested", "", env.Clock.Now()); !errors.Is(err, responsibility.ErrConflict) {
		t.Fatalf("conflicting recovery trace error = %v, want responsibility.ErrConflict", err)
	}
	stored, err = repo.GetRun(ctx, conflicted.ScheduledRunID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != schedulerapp.RunStatusRunning || stored.TraceRef != conflicted.TraceRef || scheduledRunHasResult(stored.Result) || stored.DeliveryStatus != "" {
		t.Fatalf("conflicting recovery mutated run = %+v", stored)
	}
}

func TestProductionSchedulerRunReconcilerCompletesRunFromFulfilledResponsibility(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	ctx := context.Background()
	repo := schedulerapp.NewRepository(env.DBRouter)
	responsibilities := responsibility.NewRepository(env.DBRouter, env.UnitOfWork)
	var assignment responsibility.AssignmentRecord
	if err := env.UnitOfWork.Do(ctx, func(ctx context.Context, tx uow.Tx) error {
		var err error
		assignment, _, err = responsibilities.AssignMissionTaskTx(ctx, tx, responsibility.MissionTaskAssignmentInput{
			TenantID: "global", MissionID: "mission-reconcile", TaskID: "task-reconcile", AgentProfileID: "agent-reconcile",
			ActivationCause: responsibility.ActivationCauseAssigned,
		}, env.Clock.Now())
		return err
	}); err != nil {
		t.Fatal(err)
	}
	for _, state := range []responsibility.AssignmentState{responsibility.AssignmentStateAccepted, responsibility.AssignmentStateActive, responsibility.AssignmentStateSubmitted, responsibility.AssignmentStateFulfilled} {
		var err error
		assignment, err = responsibilities.TransitionAssignment(ctx, assignment.ID, state, env.Clock.Now())
		if err != nil {
			t.Fatal(err)
		}
	}
	run := schedulerapp.ScheduledRun{
		ScheduledRunID:   "run-reconcile",
		ScheduledTaskID:  "sched-reconcile",
		Status:           schedulerapp.RunStatusRunning,
		TargetMode:       schedulerapp.TargetModeAgentTask,
		MissionID:        assignment.MissionID,
		TaskID:           assignment.WorkID,
		ResponsibilityID: assignment.ID,
		AgentID:          assignment.AccountableAgent,
		Result:           schedulerapp.RunResult{Summary: "durable agent result"},
		CreatedAt:        env.Clock.Now(),
	}
	if err := repo.SaveRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	reconciler := productionSchedulerRunReconciler{repo: repo, queue: env.ExecutionQueue, responsibilities: responsibilities, clock: env.Clock}
	if err := reconciler.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	completed, err := repo.GetRun(ctx, run.ScheduledRunID)
	if err != nil {
		t.Fatal(err)
	}
	if completed.Status != schedulerapp.RunStatusCompleted || completed.CompletedAt.IsZero() {
		t.Fatalf("reconciled run = %+v", completed)
	}
}

func TestProductionSchedulerAgentTaskReplayAcrossServiceRestartReusesMaterializedWork(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	ctx := context.Background()
	const tenantID = "tenant-scheduler-replay"
	const agentID = "agent-scheduler-replay"
	profiles := agentcore.NewPlatformProfileRepository(env.UnitOfWork)
	seedProductionChatProfile(t, ctx, profiles, agentID, "任定时", "Worker")
	auth := newProductionMissionAuthorizationTestFixture()

	request := schedulerapp.AgentTaskRequest{
		TenantID: tenantID, RunID: "run-scheduler-replay", ScheduledTaskID: "scheduled-replay",
		Title: "Replay scheduled work", AgentID: agentID, TaskGoal: "Produce a durable report",
		Prompt: "Produce the report with evidence", PrincipalID: "scheduler-owner",
		CreatorProfileID: "scheduler-owner-profile", Permissions: []string{"memory.read"},
	}
	firstPort, firstProjection, firstResponsibilities := newProductionSchedulerAgentTaskTestPort(env, profiles, auth)
	first, err := firstPort.StartAgentTask(ctx, request)
	if err != nil {
		t.Fatalf("first materialization: %v", err)
	}

	// Recreate every application repository and service around the same durable stores.
	restartedPort, restartedProjection, restartedResponsibilities := newProductionSchedulerAgentTaskTestPort(env, profiles, auth)
	replayed, err := restartedPort.StartAgentTask(ctx, request)
	if err != nil {
		t.Fatalf("materialization after service restart: %v", err)
	}
	if replayed != first {
		t.Fatalf("replayed receipt = %+v, want %+v", replayed, first)
	}
	model, err := restartedProjection.GetMissionControlForTenant(ctx, tenantID, missioncore.MissionID(first.MissionID))
	if err != nil {
		t.Fatalf("get restarted Mission projection: %v", err)
	}
	if len(model.TaskBoard.Tasks) != 1 || model.TaskBoard.Tasks[0].TaskID.String() != first.TaskID {
		t.Fatalf("restarted task board = %+v", model.TaskBoard.Tasks)
	}
	assignments, err := restartedResponsibilities.ListAssignments(ctx, responsibility.AssignmentListQuery{TenantID: tenantID})
	if err != nil {
		t.Fatalf("list restarted responsibilities: %v", err)
	}
	if len(assignments) != 1 || assignments[0].ID != first.ResponsibilityID {
		t.Fatalf("restarted responsibilities = %+v", assignments)
	}
	if firstProjection == restartedProjection || firstResponsibilities == restartedResponsibilities {
		t.Fatal("test did not recreate production repositories")
	}
}

func TestProductionSchedulerAgentTaskReusesMissionAcrossScheduledRuns(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	ctx := context.Background()
	const tenantID = "tenant-scheduler-stable-mission"
	const agentID = "agent-scheduler-stable-mission"
	profiles := agentcore.NewPlatformProfileRepository(env.UnitOfWork)
	seedProductionChatProfile(t, ctx, profiles, agentID, "任稳定", "Worker")
	port, projection, responsibilities := newProductionSchedulerAgentTaskTestPort(env, profiles, newProductionMissionAuthorizationTestFixture())

	request := schedulerapp.AgentTaskRequest{
		TenantID: tenantID, ScheduledTaskID: "scheduled-stable-mission",
		Title: "Stable scheduled work", AgentID: agentID, TaskGoal: "Produce each occurrence",
		Prompt: "Produce this occurrence with evidence", PrincipalID: "scheduler-owner",
		CreatorProfileID: "scheduler-owner-profile", Permissions: []string{"memory.read"},
	}
	request.RunID = "run-stable-mission-first"
	first, err := port.StartAgentTask(ctx, request)
	if err != nil {
		t.Fatalf("materialize first occurrence: %v", err)
	}
	request.RunID = "run-stable-mission-second"
	second, err := port.StartAgentTask(ctx, request)
	if err != nil {
		t.Fatalf("materialize second occurrence: %v", err)
	}

	if second.MissionID != first.MissionID {
		t.Fatalf("second Mission = %q, want stable Mission %q", second.MissionID, first.MissionID)
	}
	if second.TaskID == first.TaskID {
		t.Fatalf("occurrences reused Task %q", first.TaskID)
	}
	if second.ResponsibilityID == first.ResponsibilityID {
		t.Fatalf("occurrences reused Responsibility %q", first.ResponsibilityID)
	}
	model, err := projection.GetMissionControlForTenant(ctx, tenantID, missioncore.MissionID(first.MissionID))
	if err != nil {
		t.Fatalf("get stable Mission projection: %v", err)
	}
	if len(model.TaskBoard.Tasks) != 2 {
		t.Fatalf("stable Mission task count = %d, want 2", len(model.TaskBoard.Tasks))
	}
	assignments, err := responsibilities.ListAssignments(ctx, responsibility.AssignmentListQuery{TenantID: tenantID})
	if err != nil {
		t.Fatalf("list occurrence responsibilities: %v", err)
	}
	if len(assignments) != 2 {
		t.Fatalf("responsibility count = %d, want 2", len(assignments))
	}
}

func TestProductionSchedulerAgentTaskUsesPersistedMissionAfterCreatorMetadataChanges(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	ctx := context.Background()
	const tenantID = "tenant-scheduler-persisted-mission"
	const agentID = "agent-scheduler-persisted-mission"
	profiles := agentcore.NewPlatformProfileRepository(env.UnitOfWork)
	seedProductionChatProfile(t, ctx, profiles, agentID, "任持久", "Worker")
	port, _, _ := newProductionSchedulerAgentTaskTestPort(env, profiles, newProductionMissionAuthorizationTestFixture())
	lifecycle := productionSchedulerMissionLifecycle{missions: port.missions}
	task := schedulerapp.ScheduledTask{
		TenantID: tenantID, ScheduledTaskID: "scheduled-persisted-mission", Title: "Persisted scheduled work",
		Target: schedulerapp.Target{Mode: schedulerapp.TargetModeAgentTask, AgentID: agentID, TaskGoal: "Produce the occurrence"},
		Status: schedulerapp.TaskStatusEnabled, Enabled: true, CreatedBy: "system:migration",
		CreatedByKind: schedulerapp.ViewerKindSystem, CreatedByName: "Scheduled migration",
	}
	missionID, err := lifecycle.EnsureScheduledMission(ctx, task)
	if err != nil {
		t.Fatalf("ensure scheduled Mission: %v", err)
	}

	receipt, err := port.StartAgentTask(ctx, schedulerapp.AgentTaskRequest{
		TenantID: tenantID, MissionID: missionID, RunID: "run-persisted-mission", ScheduledTaskID: task.ScheduledTaskID,
		Title: task.Title, AgentID: agentID, TaskGoal: task.Target.TaskGoal, Prompt: task.Target.TaskGoal,
		PrincipalID: task.CreatedBy, CreatorProfileID: agentID, CreatorKind: task.CreatedByKind, CreatorName: task.CreatedByName,
	})
	if err != nil {
		t.Fatalf("materialize occurrence with changed creator metadata: %v", err)
	}
	if receipt.MissionID != missionID {
		t.Fatalf("materialized Mission = %q, want persisted Mission %q", receipt.MissionID, missionID)
	}
}

func TestProductionSchedulerMissionLifecycleEnsuresAndSynchronizesMission(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	ctx := context.Background()
	const tenantID = "tenant-scheduler-lifecycle"
	const agentID = "agent-scheduler-lifecycle"
	profiles := agentcore.NewPlatformProfileRepository(env.UnitOfWork)
	seedProductionChatProfile(t, ctx, profiles, agentID, "任同步", "Worker")
	port, projectionRepo, _ := newProductionSchedulerAgentTaskTestPort(env, profiles, newProductionMissionAuthorizationTestFixture())
	lifecycle := productionSchedulerMissionLifecycle{missions: port.missions}
	task := schedulerapp.ScheduledTask{
		TenantID: tenantID, ScheduledTaskID: "scheduled-lifecycle", Title: "Scheduled lifecycle",
		Description: "Keep the recurring Mission synchronized",
		Target:      schedulerapp.Target{Mode: schedulerapp.TargetModeAgentTask, AgentID: agentID, TaskGoal: "Produce every occurrence"},
		Status:      schedulerapp.TaskStatusEnabled, Enabled: true, CreatedBy: "scheduler-owner",
		CreatedByKind: schedulerapp.ViewerKindUser, CreatedByName: "Scheduler Owner", CreatorProfileID: "scheduler-owner-profile",
	}

	missionID, err := lifecycle.EnsureScheduledMission(ctx, task)
	if err != nil {
		t.Fatalf("ensure enabled Mission: %v", err)
	}
	task.MissionID = missionID
	task.Status = schedulerapp.TaskStatusDisabled
	task.Enabled = false
	if reused, err := lifecycle.EnsureScheduledMission(ctx, task); err != nil || reused != missionID {
		t.Fatalf("sync disabled Mission = %q, err=%v, want %q", reused, err, missionID)
	}
	paused, err := projectionRepo.GetMissionControlForTenant(ctx, tenantID, missioncore.MissionID(missionID))
	if err != nil {
		t.Fatal(err)
	}
	if paused.Mission.Status != "paused" {
		t.Fatalf("disabled Mission status = %q, want paused", paused.Mission.Status)
	}
	task.Status = schedulerapp.TaskStatusArchived
	if reused, err := lifecycle.EnsureScheduledMission(ctx, task); err != nil || reused != missionID {
		t.Fatalf("sync archived Mission = %q, err=%v, want %q", reused, err, missionID)
	}
	cancelled, err := projectionRepo.GetMissionControlForTenant(ctx, tenantID, missioncore.MissionID(missionID))
	if err != nil {
		t.Fatal(err)
	}
	if cancelled.Mission.Status != "cancelled" || cancelled.Mission.OriginID != task.ScheduledTaskID {
		t.Fatalf("archived Mission = %+v", cancelled.Mission)
	}
}

func TestProductionSchedulerAgentTaskPersistsLinksForRestartReconciliation(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	ctx := context.Background()
	const tenantID = "tenant-scheduler-links"
	const agentID = "agent-scheduler-links"
	profiles := agentcore.NewPlatformProfileRepository(env.UnitOfWork)
	seedProductionChatProfile(t, ctx, profiles, agentID, "任链接", "Worker")
	auth := newProductionMissionAuthorizationTestFixture()
	port, _, responsibilities := newProductionSchedulerAgentTaskTestPort(env, profiles, auth)
	repo := schedulerapp.NewRepository(env.DBRouter)
	task := schedulerapp.ScheduledTask{
		TenantID: tenantID, ScheduledTaskID: "scheduled-links", Title: "Persist scheduler links",
		Description: "Keep Mission linkage across a process restart",
		Target:      schedulerapp.Target{Mode: schedulerapp.TargetModeAgentTask, AgentID: agentID, TaskGoal: "Publish linked work", Prompt: "Publish with evidence"},
		Policy:      schedulerapp.Policy{MaxRetries: 1}, CreatedBy: "scheduler-owner", CreatorProfileID: "scheduler-owner-profile",
	}
	run := schedulerapp.ScheduledRun{
		ScheduledRunID: "run-scheduler-links", ScheduledTaskID: task.ScheduledTaskID,
		Status: schedulerapp.RunStatusQueued, TargetMode: schedulerapp.TargetModeAgentTask, CreatedAt: env.Clock.Now(),
	}
	if err := repo.SaveRun(ctx, run); err != nil {
		t.Fatalf("save scheduled run: %v", err)
	}
	port.runs = repo
	checked := &schedulerIdentityBeforeTaskPort{schedulerMissionCommandPort: port.missions, repo: repo, runID: run.ScheduledRunID, t: t}
	port.missions = checked
	dispatcher := schedulerapp.NewDispatcher(schedulerapp.DispatcherOptions{
		Clock: env.Clock, Repository: repo, AgentTasks: port,
		CreatorPermissions: schedulerCreatorPermissionResolver{permissions: []string{"memory.read"}},
	})
	started, err := dispatcher.DispatchRun(ctx, task, run)
	if err != nil {
		t.Fatalf("dispatch scheduled Agent task: %v", err)
	}
	if started.MissionID == "" || started.TaskID == "" || started.ResponsibilityID == "" {
		t.Fatalf("started run is missing durable links: %+v", started)
	}
	if checked.calls != 1 {
		t.Fatalf("identity check before task activation called %d times", checked.calls)
	}

	// A process restart reconstructs repositories and the reconciler from persisted state.
	restartedRepo := schedulerapp.NewRepository(env.DBRouter)
	persisted, err := restartedRepo.GetRun(ctx, run.ScheduledRunID)
	if err != nil {
		t.Fatalf("get restarted run: %v", err)
	}
	if persisted.MissionID != started.MissionID || persisted.TaskID != started.TaskID || persisted.ResponsibilityID != started.ResponsibilityID {
		t.Fatalf("persisted links = %+v, want %+v", persisted, started)
	}
	if _, err := restartedRepo.MutateRun(ctx, persisted.ScheduledRunID, func(run *schedulerapp.ScheduledRun) error {
		run.Result = schedulerapp.RunResult{Summary: "durable agent result"}
		return nil
	}); err != nil {
		t.Fatalf("persist durable result before restart reconciliation: %v", err)
	}
	restartedResponsibilities := responsibility.NewRepository(env.DBRouter, env.UnitOfWork)
	assignment, err := restartedResponsibilities.GetAssignment(ctx, persisted.ResponsibilityID)
	if err != nil {
		t.Fatalf("get restarted responsibility: %v", err)
	}
	for _, state := range []responsibility.AssignmentState{
		responsibility.AssignmentStateAccepted,
		responsibility.AssignmentStateActive,
		responsibility.AssignmentStateSubmitted,
		responsibility.AssignmentStateFulfilled,
	} {
		assignment, err = restartedResponsibilities.TransitionAssignment(ctx, assignment.ID, state, env.Clock.Now())
		if err != nil {
			t.Fatalf("transition restarted responsibility to %s: %v", state, err)
		}
	}
	reconciler := productionSchedulerRunReconciler{
		repo: restartedRepo, queue: env.ExecutionQueue, responsibilities: restartedResponsibilities, clock: env.Clock,
	}
	if err := reconciler.Reconcile(ctx); err != nil {
		t.Fatalf("reconcile after restart: %v", err)
	}
	completed, err := restartedRepo.GetRun(ctx, run.ScheduledRunID)
	if err != nil {
		t.Fatalf("get reconciled run: %v", err)
	}
	if completed.Status != schedulerapp.RunStatusCompleted || completed.CompletedAt.IsZero() {
		t.Fatalf("reconciled persisted run = %+v", completed)
	}
	if responsibilities == restartedResponsibilities {
		t.Fatal("test did not recreate the responsibility repository")
	}
}

func newProductionSchedulerAgentTaskTestPort(env *testkit.Env, profiles *agentcore.PlatformProfileRepository, auth productionMissionAuthorizationTestFixture) (productionSchedulerAgentTaskPort, *missioncontrol.DBProjectionRepository, *responsibility.Repository) {
	projectionRepo := missioncontrol.NewDBProjectionRepository(env.DBRouter)
	tasks := missiontask.NewPlatformRepository()
	responsibilities := responsibility.NewRepository(env.DBRouter, env.UnitOfWork)
	responsibilityService := responsibility.NewService(responsibility.ServiceOptions{
		Repository: responsibilities, UnitOfWork: env.UnitOfWork, Tasks: tasks, Clock: env.Clock,
	})
	lifecycle := missioncontrol.NewLifecycleService(env.UnitOfWork, env.DBRouter, projectionRepo, missioncontrol.LifecycleOptions{
		Clock: env.Clock, IDGenerator: env.IDGenerator, TaskRepo: tasks, AgentDirectory: profiles,
		AuthorizationService: auth.service, AuthorizationRelations: auth.relations,
		TaskResponsibility: productionTaskResponsibilityAssignment{service: responsibilityService},
	})
	commands := missioncontrol.NewCommandServiceWithMissionPorts(nil, nil, nil, nil, lifecycle)
	auth.wireCommand(commands)
	return productionSchedulerAgentTaskPort{missions: commands}, projectionRepo, responsibilities
}

type schedulerCreatorPermissionResolver struct {
	permissions []string
}

func (r schedulerCreatorPermissionResolver) ResolveCreatorPermissions(context.Context, schedulerapp.CreatorRef) (schedulerapp.CreatorPermissions, error) {
	return schedulerapp.CreatorPermissions{PermissionScope: append([]string(nil), r.permissions...)}, nil
}

func TestProductionSchedulerRunReconcilerFailsOrphanedRun(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	ctx := context.Background()
	repo := schedulerapp.NewRepository(env.DBRouter)
	run := schedulerapp.ScheduledRun{
		ScheduledRunID:  "run-orphaned",
		ScheduledTaskID: "sched-orphaned",
		Status:          schedulerapp.RunStatusQueued,
		TargetMode:      schedulerapp.TargetModeDiscussionWorkflow,
		CreatedAt:       env.Clock.Now().Add(-3 * time.Minute),
	}
	if err := repo.SaveRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	reconciler := productionSchedulerRunReconciler{repo: repo, queue: env.ExecutionQueue, clock: env.Clock}
	if err := reconciler.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	failed, err := repo.GetRun(ctx, run.ScheduledRunID)
	if err != nil {
		t.Fatal(err)
	}
	if failed.Status != schedulerapp.RunStatusFailed || failed.FailureReason == "" {
		t.Fatalf("orphaned run = %+v", failed)
	}
}
