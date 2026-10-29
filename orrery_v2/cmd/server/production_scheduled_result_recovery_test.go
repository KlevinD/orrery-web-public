package main

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	runtimedaemon "orrery_v2/internal/agent/runtime/daemon"
	"orrery_v2/internal/app/responsibility"
	schedulerapp "orrery_v2/internal/app/scheduler"
	missiontask "orrery_v2/internal/mission/task"
	"orrery_v2/internal/platform/testkit"
	"orrery_v2/internal/platform/uow"
)

func TestLoadCommittedScheduledAgentResultUsesExactRawRunID(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	ctx := context.Background()
	repository := missiontask.NewPlatformRepository()
	taskID := missiontask.TaskID("task-recovery-raw-run")
	run := runtimedaemon.AgentRun{
		RunID: "runtime-run-exact", AgentID: "agent-recovery", MissionID: "mission-recovery", TaskID: taskID.String(),
	}

	desired := missiontask.TaskSubmission{
		ID: "submission-exact", TaskID: taskID, AgentID: run.AgentID, RunID: run.RunID,
		AgentResultRef: "result-exact", Summary: agentcore.Summary{Text: "exact committed result"},
		UserReport: "本次资料核对尚未完成，仍在等待投影更新。",
		Blocker: &agentcore.Blocker{Summary: "waiting for ontology projection", Report: agentcore.ArtifactRef{
			ID: "blocked-report", Kind: agentcore.ArtifactKindReport, URI: "artifact://blocked-report",
		}},
		InternalTraceRef: "artifact://runtime-traces/runtime-run-exact",
		IdempotencyKey:   run.RunID.String(), SubmittedAt: env.Clock.Now().Add(-time.Minute),
	}
	newerOtherRun := missiontask.TaskSubmission{
		ID: "submission-newer", TaskID: taskID, AgentID: run.AgentID, RunID: "runtime-run-newer",
		AgentResultRef: "result-newer", Summary: agentcore.Summary{Text: "newer unrelated result"},
		IdempotencyKey: "runtime-run-newer", SubmittedAt: env.Clock.Now(),
	}
	if err := env.UnitOfWork.Do(ctx, func(ctx context.Context, tx uow.Tx) error {
		if err := repository.SaveTask(ctx, tx, missiontask.TaskRecord{
			Contract: missiontask.TaskContract{ID: taskID, MissionID: "mission-recovery"},
			Status:   missiontask.TaskStatusApproved, Version: 2,
		}); err != nil {
			return err
		}
		if err := repository.SaveSubmission(ctx, tx, desired, nil, nil); err != nil {
			return err
		}
		return repository.SaveSubmission(ctx, tx, newerOtherRun, nil, nil)
	}); err != nil {
		t.Fatal(err)
	}

	got, found, err := loadCommittedScheduledAgentResult(ctx, env.UnitOfWork, repository, run)
	if err != nil {
		t.Fatalf("load committed result: %v", err)
	}
	if !found {
		t.Fatal("exact committed result was not found")
	}
	if got.ID != desired.AgentResultRef || got.RunID != run.RunID || got.Summary.Text != desired.Summary.Text || got.Blocker == nil || got.Blocker.Summary != desired.Blocker.Summary ||
		got.InternalTraceRef != desired.InternalTraceRef || got.Blocker.Report != desired.Blocker.Report || got.UserReport != desired.UserReport {
		t.Fatalf("recovered result = %+v, want exact submission %+v", got, desired)
	}
}

func TestLoadCommittedScheduledAgentResultRejectsInconsistentChildren(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	run := runtimedaemon.AgentRun{
		RunID: "runtime-run-children", AgentID: "agent-recovery", MissionID: "mission-recovery", TaskID: "task-recovery-children",
	}
	base := scheduledCommittedResultRepositoryStub{
		task: missiontask.TaskRecord{
			Contract: missiontask.TaskContract{ID: "task-recovery-children", MissionID: "mission-recovery"},
			Status:   missiontask.TaskStatusApproved,
		},
		submission: missiontask.TaskSubmission{
			ID: "submission-children", TaskID: "task-recovery-children", AgentID: run.AgentID, RunID: run.RunID,
			AgentResultRef: "result-children", Summary: agentcore.Summary{Text: "committed"}, IdempotencyKey: run.RunID.String(),
			DeliverableRefs: []agentcore.DeliverableID{"deliverable-expected"},
			EvidenceRefs:    []agentcore.EvidenceID{"evidence-expected"},
		},
		deliverables: []missiontask.DeliverableSubmission{{
			ID: "deliverable-submission", SubmissionID: "submission-children", TaskID: "task-recovery-children", AgentID: run.AgentID,
			Deliverable: agentcore.Deliverable{ID: "deliverable-expected"},
		}},
		evidence: []missiontask.EvidenceSubmission{{
			ID: "evidence-submission", SubmissionID: "submission-children", TaskID: "task-recovery-children", AgentID: run.AgentID,
			Evidence: agentcore.Evidence{ID: "evidence-expected"},
		}},
	}

	tests := []struct {
		name   string
		mutate func(*scheduledCommittedResultRepositoryStub)
	}{
		{
			name: "child reference differs from parent",
			mutate: func(repository *scheduledCommittedResultRepositoryStub) {
				repository.deliverables[0].Deliverable.ID = "deliverable-unreferenced"
			},
		},
		{
			name: "child identity belongs to another Agent",
			mutate: func(repository *scheduledCommittedResultRepositoryStub) {
				repository.evidence[0].AgentID = "agent-other"
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repository := base.clone()
			tt.mutate(&repository)
			got, found, err := loadCommittedScheduledAgentResult(context.Background(), env.UnitOfWork, &repository, run)
			if !found || err == nil {
				t.Fatalf("result=%+v found=%v err=%v, want found corrupt submission rejected", got, found, err)
			}
			if !strings.Contains(err.Error(), "identity") && !strings.Contains(err.Error(), "references") {
				t.Fatalf("error = %v, want child integrity failure", err)
			}
		})
	}
}

func TestScheduledRunResultFromCommittedAgentResultRebuildsTypedArtifact(t *testing.T) {
	run := runtimedaemon.AgentRun{
		RunID: "runtime-run-typed", AgentID: "agent-recovery", MissionID: "mission-recovery", TaskID: "task-recovery-typed",
		Request: agentcore.RuntimeRequest{
			TenantID: "tenant-recovery",
			RuntimeOptions: agentcore.RuntimeOptions{ProviderSpecific: map[string]string{
				"run_source": schedulerAgentTaskRunSource, "scheduled_task_id": "scheduled-task-typed", "scheduled_run_id": "scheduled-run-typed",
			}},
		},
	}
	contract, err := encodeSchedulerResultContract(schedulerapp.AgentResultContract{Artifacts: []schedulerapp.ArtifactRequirement{{
		ID: "report", Kind: "report", PathPattern: "/workspace/report.md", MinBytes: 1,
	}}})
	if err != nil {
		t.Fatal(err)
	}
	run.Request.RuntimeOptions.ProviderSpecific[schedulerResultContractProviderKey] = contract

	const sizeBytes = int64(37)
	digest := strings.Repeat("a", 64)
	artifactID := "scheduled-artifact:" + run.RunID.String() + ":report"
	uri := "artifact://" + scheduledArtifactStorageKey(
		run.Request.TenantID, "scheduled-run-typed", artifactID, sizeBytes, digest, "report.md",
	)
	artifact := agentcore.ArtifactRef{ID: artifactID, Kind: agentcore.ArtifactKindReport, URI: uri}
	committed := agentcore.AgentResult{
		ID: "result-typed", MissionID: "mission-recovery", TaskID: "task-recovery-typed", AgentID: run.AgentID, RunID: run.RunID,
		Summary: agentcore.Summary{Text: "typed result recovered"},
		Deliverables: []agentcore.Deliverable{{
			ID: "deliverable-report", ExpectedDeliverableID: "report", Title: "Report", ArtifactRef: artifact,
		}},
		Evidence: []agentcore.Evidence{{
			ID: "evidence-report", EvidenceRequirementID: "evidence-report", Kind: agentcore.EvidenceKindArtifact, ArtifactRef: artifact,
		}},
	}

	got, err := scheduledRunResultFromCommittedAgentResult(run, committed)
	if err != nil {
		t.Fatalf("rebuild scheduled result: %v", err)
	}
	if got.Summary != committed.Summary.Text || len(got.Artifacts) != 1 {
		t.Fatalf("scheduled result = %+v", got)
	}
	gotArtifact := got.Artifacts[0]
	if gotArtifact.ID != artifactID || gotArtifact.URI != uri || gotArtifact.Kind != "report" ||
		gotArtifact.FileName != "report.md" || gotArtifact.SizeBytes != sizeBytes || gotArtifact.SHA256 != digest ||
		gotArtifact.EvidenceID != "evidence-report" {
		t.Fatalf("rebuilt artifact = %+v", gotArtifact)
	}
}

func TestScheduledRunResultFromCommittedAgentResultAllowsBlockedContractWithoutArtifacts(t *testing.T) {
	run := runtimedaemon.AgentRun{
		RunID: "runtime-run-blocked-contract", AgentID: "agent-recovery", MissionID: "mission-recovery", TaskID: "task-recovery-blocked",
		Request: agentcore.RuntimeRequest{TenantID: "tenant-recovery", RuntimeOptions: agentcore.RuntimeOptions{ProviderSpecific: map[string]string{
			"run_source": schedulerAgentTaskRunSource, "scheduled_task_id": "scheduled-task-blocked", "scheduled_run_id": "scheduled-run-blocked",
		}}},
	}
	contract, err := encodeSchedulerResultContract(schedulerapp.AgentResultContract{Artifacts: []schedulerapp.ArtifactRequirement{{
		ID: "report", Kind: "report", PathPattern: "/workspace/report.md", MinBytes: 1,
	}}})
	if err != nil {
		t.Fatal(err)
	}
	run.Request.RuntimeOptions.ProviderSpecific[schedulerResultContractProviderKey] = contract
	committed := agentcore.AgentResult{
		ID: "result-blocked-contract", MissionID: "mission-recovery", TaskID: "task-recovery-blocked", AgentID: run.AgentID, RunID: run.RunID,
		Summary: agentcore.Summary{Text: "Ontology projection is still pending."},
		Blocker: &agentcore.Blocker{Summary: "Ontology projection is still pending."},
	}

	got, err := scheduledRunResultFromCommittedAgentResult(run, committed)
	if err != nil {
		t.Fatalf("rebuild blocked scheduled result: %v", err)
	}
	if got.Summary != committed.Summary.Text || len(got.Artifacts) != 0 {
		t.Fatalf("blocked scheduled result = %+v", got)
	}
}

func TestScheduledRunResultFromCommittedAgentResultRejectsAnotherRunSnapshot(t *testing.T) {
	run := runtimedaemon.AgentRun{
		RunID: "runtime-run-owner", AgentID: "agent-recovery", MissionID: "mission-recovery", TaskID: "task-recovery-owner",
		Request: agentcore.RuntimeRequest{TenantID: "tenant-recovery", RuntimeOptions: agentcore.RuntimeOptions{ProviderSpecific: map[string]string{
			"run_source": schedulerAgentTaskRunSource, "scheduled_task_id": "scheduled-task-owner", "scheduled_run_id": "scheduled-run-owner",
		}}},
	}
	artifactID := "scheduled-artifact:" + run.RunID.String() + ":report"
	digest := strings.Repeat("b", 64)
	foreignURI := "artifact://" + scheduledArtifactStorageKey("tenant-recovery", "scheduled-run-foreign", artifactID, 12, digest, "report.md")
	_, err := scheduledRunResultFromCommittedAgentResult(run, agentcore.AgentResult{
		ID: "result-owner", MissionID: "mission-recovery", TaskID: "task-recovery-owner", AgentID: run.AgentID, RunID: run.RunID,
		Summary: agentcore.Summary{Text: "done"},
		Deliverables: []agentcore.Deliverable{{ID: "deliverable-report", ArtifactRef: agentcore.ArtifactRef{
			ID: artifactID, Kind: agentcore.ArtifactKindReport, URI: foreignURI,
		}}},
	})
	if err == nil || !strings.Contains(err.Error(), "does not belong") {
		t.Fatalf("foreign snapshot error = %v", err)
	}
}

func TestProductionSchedulerRunReconcilerKeepsFulfilledRunActiveUntilResultIsDurable(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	ctx := context.Background()
	repository := schedulerapp.NewRepository(env.DBRouter)
	responsibilities := responsibility.NewRepository(env.DBRouter, env.UnitOfWork)
	var assignment responsibility.AssignmentRecord
	if err := env.UnitOfWork.Do(ctx, func(ctx context.Context, tx uow.Tx) error {
		var err error
		assignment, _, err = responsibilities.AssignMissionTaskTx(ctx, tx, responsibility.MissionTaskAssignmentInput{
			TenantID: "global", MissionID: "mission-recovery-pending", TaskID: "task-recovery-pending", AgentProfileID: "agent-recovery-pending",
			SourceKind: schedulerAgentTaskRunSource, SourceID: "scheduled-task-recovery-pending", SourceStepID: "scheduled-run-recovery-pending",
			ActivationCause: responsibility.ActivationCauseAssigned,
		}, env.Clock.Now())
		return err
	}); err != nil {
		t.Fatal(err)
	}
	for _, state := range []responsibility.AssignmentState{
		responsibility.AssignmentStateAccepted,
		responsibility.AssignmentStateActive,
		responsibility.AssignmentStateSubmitted,
		responsibility.AssignmentStateFulfilled,
	} {
		var err error
		assignment, err = responsibilities.TransitionAssignment(ctx, assignment.ID, state, env.Clock.Now())
		if err != nil {
			t.Fatal(err)
		}
	}
	run := schedulerapp.ScheduledRun{
		ScheduledRunID: "scheduled-run-recovery-pending", ScheduledTaskID: "scheduled-task-recovery-pending",
		Status: schedulerapp.RunStatusRunning, TargetMode: schedulerapp.TargetModeAgentTask, Attempt: 2,
		MissionID: assignment.MissionID, TaskID: assignment.WorkID, ResponsibilityID: assignment.ID,
		AgentID: assignment.AccountableAgent, FailureReason: "scheduler result write pending", CreatedAt: env.Clock.Now(),
	}
	if err := repository.SaveRun(ctx, run); err != nil {
		t.Fatal(err)
	}

	reconciler := productionSchedulerRunReconciler{
		repo: repository, queue: env.ExecutionQueue, responsibilities: responsibilities, clock: env.Clock,
		unit: env.UnitOfWork, committedResults: missiontask.NewPlatformRepository(),
	}
	if err := reconciler.Reconcile(ctx); err == nil || !strings.Contains(err.Error(), "no runtime attempt") {
		t.Fatalf("reconcile error = %v, want missing durable runtime attempt", err)
	}
	stored, err := repository.GetRun(ctx, run.ScheduledRunID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != schedulerapp.RunStatusRunning || !stored.CompletedAt.IsZero() ||
		stored.Result.Summary != "" || len(stored.Result.Artifacts) != 0 {
		t.Fatalf("fulfilled run with pending scheduler result = %+v, want active", stored)
	}
	active, err := repository.ListActiveRuns(ctx)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, candidate := range active {
		found = found || candidate.ScheduledRunID == run.ScheduledRunID
	}
	if !found {
		t.Fatalf("pending run %q is not listed as active", run.ScheduledRunID)
	}
}

func TestProductionSchedulerRunReconcilerRecoversCommittedResultAndTrace(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	ctx := context.Background()
	const (
		tenantID        = "global"
		scheduledTaskID = "scheduled-task-recovery-committed"
		scheduledRunID  = "scheduled-run-recovery-committed"
		missionID       = "mission-recovery-committed"
		taskID          = "task-recovery-committed"
		agentID         = "agent-recovery-committed"
		runtimeRunID    = "runtime-run-recovery-committed"
		traceRef        = "artifact://runtime-traces/runtime-run-recovery-committed"
	)
	schedulerRepository := schedulerapp.NewRepository(env.DBRouter)
	responsibilities := responsibility.NewRepository(env.DBRouter, env.UnitOfWork)
	missionTasks := missiontask.NewPlatformRepository()
	var assignment responsibility.AssignmentRecord
	var activation responsibility.ActivationRecord
	if err := env.UnitOfWork.Do(ctx, func(ctx context.Context, tx uow.Tx) error {
		var err error
		assignment, activation, err = responsibilities.AssignMissionTaskTx(ctx, tx, responsibility.MissionTaskAssignmentInput{
			TenantID: tenantID, MissionID: missionID, TaskID: taskID, AgentProfileID: agentID,
			SourceKind: schedulerAgentTaskRunSource, SourceID: scheduledTaskID, SourceStepID: scheduledRunID,
			ActivationCause: responsibility.ActivationCauseAssigned,
		}, env.Clock.Now())
		return err
	}); err != nil {
		t.Fatal(err)
	}
	_, claimed, err := responsibilities.ClaimActivation(ctx, activation.ID, "worker-recovery", "claim-recovery", env.Clock.Now(), time.Minute, 1)
	if err != nil || !claimed {
		t.Fatalf("claim activation: claimed=%v err=%v", claimed, err)
	}
	attempt, err := responsibilities.MarkDispatched(ctx, activation.ID, "claim-recovery", runtimeRunID, string(agentcore.RuntimeKindOpenClaw), env.Clock.Now())
	if err != nil {
		t.Fatal(err)
	}
	attempt, err = responsibilities.CompleteAttempt(ctx, attempt.ID, responsibility.AttemptOutcomeCompleted, nil, env.Clock.Now())
	if err != nil {
		t.Fatal(err)
	}
	assignment, err = responsibilities.GetAssignment(ctx, assignment.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, state := range []responsibility.AssignmentState{responsibility.AssignmentStateSubmitted, responsibility.AssignmentStateFulfilled} {
		assignment, err = responsibilities.TransitionAssignment(ctx, assignment.ID, state, env.Clock.Now())
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := env.UnitOfWork.Do(ctx, func(ctx context.Context, tx uow.Tx) error {
		if err := missionTasks.SaveTask(ctx, tx, missiontask.TaskRecord{
			Contract: missiontask.TaskContract{ID: taskID, MissionID: missionID}, Status: missiontask.TaskStatusApproved, Version: 1,
		}); err != nil {
			return err
		}
		return missionTasks.SaveSubmission(ctx, tx, missiontask.TaskSubmission{
			ID: "submission-recovery-committed", TaskID: taskID, AgentID: agentID, RunID: runtimeRunID,
			AgentResultRef: "result-recovery-committed", InternalTraceRef: traceRef, Summary: agentcore.Summary{Text: "recovered durable result"},
			IdempotencyKey: runtimeRunID, SubmittedAt: env.Clock.Now(), TaskVersionAccepted: 1,
		}, nil, nil)
	}); err != nil {
		t.Fatal(err)
	}
	if err := schedulerRepository.SaveTask(ctx, schedulerapp.ScheduledTask{
		TenantID: tenantID, ScheduledTaskID: scheduledTaskID, Title: "Recover committed result",
		Target:   schedulerapp.Target{Mode: schedulerapp.TargetModeAgentTask, AgentID: agentID},
		Delivery: schedulerapp.DeliveryPolicy{Mode: "announce"}, Status: schedulerapp.TaskStatusEnabled, Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	run := schedulerapp.ScheduledRun{
		ScheduledRunID: scheduledRunID, ScheduledTaskID: scheduledTaskID, Status: schedulerapp.RunStatusRunning,
		TargetMode: schedulerapp.TargetModeAgentTask, MissionID: missionID, TaskID: taskID, ResponsibilityID: assignment.ID,
		AgentID: agentID, Attempt: attempt.Attempt, FailureReason: "scheduler result write pending", CreatedAt: env.Clock.Now(),
		ConfigurationSnapshotVersion: 1, TenantID: tenantID, ScheduledTaskTitle: "Recover committed result",
		Delivery: schedulerapp.DeliveryPolicy{Mode: "announce", Channel: "feishu", AccountID: "orrery", RecipientID: "oc_recovery"},
	}
	if err := schedulerRepository.SaveRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	deliveryOutbox := &capturingScheduledDeliveryOutbox{}
	reconciler := productionSchedulerRunReconciler{
		repo: schedulerRepository, queue: env.ExecutionQueue, responsibilities: responsibilities, clock: env.Clock,
		unit: env.UnitOfWork, committedResults: missionTasks, deliveries: deliveryOutbox,
	}
	if err := reconciler.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	stored, err := schedulerRepository.GetRun(ctx, scheduledRunID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != schedulerapp.RunStatusCompleted || strings.TrimSpace(stored.Result.Summary) == "" ||
		stored.RuntimeRunID != runtimeRunID || stored.TraceRef != traceRef || !stored.TraceAvailable || stored.TraceStatus != "hot" || !stored.CompletedAt.Equal(attempt.CompletedAt) {
		t.Fatalf("recovered scheduled run = %+v", stored)
	}
	if stored.DeliveryStatus != "pending" || stored.DeliveryError != "" || stored.ExternalMessageID != "" || deliveryOutbox.calls != 1 {
		t.Fatalf("recovered delivery state = status=%q error=%q message=%q", stored.DeliveryStatus, stored.DeliveryError, stored.ExternalMessageID)
	}
	if err := reconciler.Reconcile(ctx); err != nil {
		t.Fatalf("idempotent recovery: %v", err)
	}
	replayed, err := schedulerRepository.GetRun(ctx, scheduledRunID)
	if err != nil {
		t.Fatal(err)
	}
	if !replayed.CompletedAt.Equal(stored.CompletedAt) || replayed.Result.Summary != stored.Result.Summary || replayed.TraceRef != stored.TraceRef {
		t.Fatalf("replayed recovery changed durable facts: before=%+v after=%+v", stored, replayed)
	}
}

func TestProductionSchedulerRunTrackerRecoveryDoesNotOverrideWorkerCompletion(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	ctx := context.Background()
	repository := schedulerapp.NewRepository(env.DBRouter)
	expected := schedulerapp.ScheduledRun{
		ScheduledRunID: "scheduled-run-worker-wins", ScheduledTaskID: "scheduled-task-worker-wins",
		Status: schedulerapp.RunStatusRunning, TargetMode: schedulerapp.TargetModeAgentTask,
		MissionID: "mission-worker-wins", TaskID: "task-worker-wins", ResponsibilityID: "responsibility-worker-wins",
		AgentID: "agent-worker-wins", CreatedAt: env.Clock.Now(),
	}
	if err := repository.SaveRun(ctx, expected); err != nil {
		t.Fatal(err)
	}
	tracker := productionSchedulerRunTracker{repo: repository, clock: env.Clock}
	workerResult := schedulerapp.RunResult{Summary: "worker durable result"}
	if err := tracker.Completed(ctx, expected.ScheduledRunID, workerResult); err != nil {
		t.Fatal(err)
	}
	err := tracker.RecoverCompleted(ctx, expected, schedulerapp.RunResult{Summary: "different recovered result"}, "", "not_requested", "", env.Clock.Now())
	if !errors.Is(err, responsibility.ErrConflict) {
		t.Fatalf("recovery conflict = %v, want responsibility conflict", err)
	}
	stored, err := repository.GetRun(ctx, expected.ScheduledRunID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Result.Summary != workerResult.Summary || stored.DeliveryStatus != "" {
		t.Fatalf("worker completion was overwritten: %+v", stored)
	}
}

func TestProductionSchedulerRunTrackerRecoveryRejectsVersionedTenantMismatch(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	ctx := context.Background()
	repository := schedulerapp.NewRepository(env.DBRouter)
	run := schedulerapp.ScheduledRun{
		ScheduledRunID: "scheduled-run-recovery-tenant", ScheduledTaskID: "scheduled-task-recovery-tenant",
		Status: schedulerapp.RunStatusRunning, TargetMode: schedulerapp.TargetModeAgentTask,
		ConfigurationSnapshotVersion: 1, TenantID: "tenant-recovery-cas",
		RuntimeRunID: "runtime-recovery-tenant",
		MissionID:    "mission-recovery-tenant", TaskID: "task-recovery-tenant", ResponsibilityID: "responsibility-recovery-tenant",
		AgentID: "agent-recovery-tenant", CreatedAt: env.Clock.Now(),
	}
	if err := repository.SaveRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	tracker := productionSchedulerRunTracker{repo: repository, clock: env.Clock}
	for _, tenantID := range []string{"", "tenant-other"} {
		expected := run
		expected.TenantID = tenantID
		err := tracker.RecoverCompleted(ctx, expected, schedulerapp.RunResult{Summary: "recovered result"}, "trace://recovery-tenant", "not_requested", "", env.Clock.Now())
		if !errors.Is(err, responsibility.ErrConflict) {
			t.Fatalf("tenant %q recovery error = %v, want responsibility conflict", tenantID, err)
		}
	}
	stored, err := repository.GetRun(ctx, run.ScheduledRunID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != schedulerapp.RunStatusRunning || scheduledRunHasResult(stored.Result) || stored.TraceRef != "" || stored.DeliveryStatus != "" {
		t.Fatalf("tenant conflict mutated scheduled run = %+v", stored)
	}
}

type scheduledCommittedResultRepositoryStub struct {
	task         missiontask.TaskRecord
	submission   missiontask.TaskSubmission
	deliverables []missiontask.DeliverableSubmission
	evidence     []missiontask.EvidenceSubmission
}

func (r scheduledCommittedResultRepositoryStub) GetTask(context.Context, uow.Tx, missiontask.TaskID) (missiontask.TaskRecord, error) {
	return r.task, nil
}

func (r scheduledCommittedResultRepositoryStub) FindSubmissionByIdempotencyKey(context.Context, uow.Tx, missiontask.TaskID, string) (missiontask.TaskSubmission, bool, error) {
	return r.submission, true, nil
}

func (r scheduledCommittedResultRepositoryStub) ListDeliverableSubmissions(context.Context, uow.Tx, missiontask.TaskSubmissionID) ([]missiontask.DeliverableSubmission, error) {
	return append([]missiontask.DeliverableSubmission(nil), r.deliverables...), nil
}

func (r scheduledCommittedResultRepositoryStub) ListEvidenceSubmissions(context.Context, uow.Tx, missiontask.TaskSubmissionID) ([]missiontask.EvidenceSubmission, error) {
	return append([]missiontask.EvidenceSubmission(nil), r.evidence...), nil
}

func (r scheduledCommittedResultRepositoryStub) clone() scheduledCommittedResultRepositoryStub {
	r.deliverables = append([]missiontask.DeliverableSubmission(nil), r.deliverables...)
	r.evidence = append([]missiontask.EvidenceSubmission(nil), r.evidence...)
	r.submission.DeliverableRefs = append([]agentcore.DeliverableID(nil), r.submission.DeliverableRefs...)
	r.submission.EvidenceRefs = append([]agentcore.EvidenceID(nil), r.submission.EvidenceRefs...)
	return r
}
