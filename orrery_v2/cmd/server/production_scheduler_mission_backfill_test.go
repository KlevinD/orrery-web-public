package main

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/missioncontrol"
	"orrery_v2/internal/app/responsibility"
	schedulerapp "orrery_v2/internal/app/scheduler"
	missionevent "orrery_v2/internal/mission/event"
	missiontask "orrery_v2/internal/mission/task"
	"orrery_v2/internal/platform/testkit"
	"orrery_v2/internal/platform/uow"
)

func TestProductionSchedulerMissionFactBackfillImportsTerminalRunsWithoutActivations(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	ctx := context.Background()
	const tenantID = "tenant-scheduler-history"
	const agentID = "agent-scheduler-history"
	const scheduledTaskID = "scheduled-history"

	artifactPath := filepath.Join(t.TempDir(), "report.md")
	artifactBody := []byte("verified historical report")
	if err := os.WriteFile(artifactPath, artifactBody, 0o600); err != nil {
		t.Fatal(err)
	}

	profiles := agentcore.NewPlatformProfileRepository(env.UnitOfWork)
	seedProductionChatProfile(t, ctx, profiles, agentID, "任历史", "Worker")
	projectionRepo := missioncontrol.NewDBProjectionRepository(env.DBRouter)
	tasks := missiontask.NewPlatformRepository()
	responsibilities := responsibility.NewRepository(env.DBRouter, env.UnitOfWork)
	responsibilityService := responsibility.NewService(responsibility.ServiceOptions{
		Repository: responsibilities, UnitOfWork: env.UnitOfWork, Tasks: tasks, Clock: env.Clock,
	})
	auth := newProductionMissionAuthorizationTestFixture()
	lifecycle := missioncontrol.NewLifecycleService(env.UnitOfWork, env.DBRouter, projectionRepo, missioncontrol.LifecycleOptions{
		Clock: env.Clock, IDGenerator: env.IDGenerator, TaskRepo: tasks, AgentDirectory: profiles,
		AuthorizationService: auth.service, AuthorizationRelations: auth.relations,
		TaskResponsibility: productionTaskResponsibilityAssignment{service: responsibilityService},
	})
	delivery := missiontask.NewDeliveryService(env.UnitOfWork, tasks, missionevent.NewTaskEventSink(), missiontask.DeliveryServiceOptions{Clock: env.Clock, IDGenerator: env.IDGenerator})
	repository := schedulerapp.NewRepository(env.DBRouter)
	scheduledTask := schedulerapp.ScheduledTask{
		TenantID: tenantID, ScheduledTaskID: scheduledTaskID, Title: "Historical scheduler",
		Target: schedulerapp.Target{
			Mode: schedulerapp.TargetModeAgentTask, AgentID: agentID, TaskGoal: "Produce the scheduled report", Prompt: "Produce the report with evidence",
			ResultContract: schedulerapp.AgentResultContract{Artifacts: []schedulerapp.ArtifactRequirement{{ID: "mutable-report", Kind: "visual", PathPattern: artifactPath, MinBytes: 8}}},
		},
		Status: schedulerapp.TaskStatusEnabled, Enabled: true, CreatedBy: "scheduler-owner", CreatorProfileID: "scheduler-owner-profile",
		CreatedAt: env.Clock.Now().Add(-time.Hour), UpdatedAt: env.Clock.Now().Add(-time.Hour),
	}
	if err := repository.SaveTask(ctx, scheduledTask); err != nil {
		t.Fatal(err)
	}
	modeEditedTask := schedulerapp.ScheduledTask{
		TenantID: tenantID, ScheduledTaskID: "scheduled-history-mode-edited", Title: "Current non-Agent task",
		Target: schedulerapp.Target{Mode: schedulerapp.TargetModeDiscussionWorkflow, Message: "Current workflow"},
		Status: schedulerapp.TaskStatusEnabled, Enabled: true, CreatedBy: "scheduler-owner", CreatorProfileID: "scheduler-owner-profile",
		CreatedAt: env.Clock.Now().Add(-time.Hour), UpdatedAt: env.Clock.Now().Add(-time.Hour),
	}
	if err := repository.SaveTask(ctx, modeEditedTask); err != nil {
		t.Fatal(err)
	}
	modeEditedSnapshot := modeEditedTask
	modeEditedSnapshot.Title = "Original Agent occurrence"
	modeEditedSnapshot.Target = schedulerapp.Target{Mode: schedulerapp.TargetModeAgentTask, AgentID: agentID, TaskGoal: "Run the original Agent occurrence"}
	modeEditedMissionID, err := (productionSchedulerMissionLifecycle{missions: lifecycle}).EnsureScheduledMission(ctx, modeEditedSnapshot)
	if err != nil || modeEditedMissionID != scheduledMissionID(tenantID, modeEditedTask.ScheduledTaskID) {
		t.Fatalf("seed mode-edited occurrence Mission: id=%q err=%v", modeEditedMissionID, err)
	}
	modeEditedSnapshot.MissionID = modeEditedMissionID
	historicalSnapshot := scheduledTask
	historicalSnapshot.MissionID = scheduledMissionID(tenantID, scheduledTaskID)
	historicalSnapshot.Title = "Historical scheduler snapshot"
	historicalSnapshot.Target.ResultContract = schedulerapp.AgentResultContract{Artifacts: []schedulerapp.ArtifactRequirement{{ID: "report", Kind: "report", PathPattern: artifactPath, MinBytes: 8}}}
	historicalSnapshot.Delivery = schedulerapp.DeliveryPolicy{Mode: "announce", Channel: "feishu", RecipientID: "oc_snapshot", AccountID: "main", ContentArtifactID: "report", Format: "markdown"}
	runs := []schedulerapp.ScheduledRun{
		{
			ScheduledRunID: "run-approved", ScheduledTaskID: scheduledTaskID, Status: schedulerapp.RunStatusCompleted, TargetMode: schedulerapp.TargetModeAgentTask,
			ConfigurationSnapshotVersion: 1, ConfigurationSnapshot: &historicalSnapshot, TenantID: tenantID, ScheduledTaskTitle: "Historical scheduler snapshot",
			ResultContract: schedulerapp.AgentResultContract{Artifacts: []schedulerapp.ArtifactRequirement{{ID: "report", Kind: "report", PathPattern: artifactPath, MinBytes: 8}}},
			Delivery:       historicalSnapshot.Delivery,
			AgentID:        agentID, StartedAt: env.Clock.Now().Add(-30 * time.Minute), CompletedAt: env.Clock.Now().Add(-29 * time.Minute), CreatedAt: env.Clock.Now().Add(-31 * time.Minute),
			Result: schedulerapp.RunResult{Summary: "Verified report", Artifacts: []schedulerapp.RunArtifact{{ID: "artifact-approved", Kind: "report", URI: artifactPath, SizeBytes: int64(len(artifactBody)), EvidenceID: "evidence-approved"}}},
		},
		{ScheduledRunID: "run-submitted", ScheduledTaskID: scheduledTaskID, Status: schedulerapp.RunStatusCompleted, TargetMode: schedulerapp.TargetModeAgentTask, AgentID: agentID, CompletedAt: env.Clock.Now().Add(-20 * time.Minute), CreatedAt: env.Clock.Now().Add(-21 * time.Minute), Result: schedulerapp.RunResult{Summary: "Stored summary without artifact"}},
		{ScheduledRunID: "run-failed", ScheduledTaskID: scheduledTaskID, Status: schedulerapp.RunStatusFailed, TargetMode: schedulerapp.TargetModeAgentTask, AgentID: agentID, CompletedAt: env.Clock.Now().Add(-10 * time.Minute), CreatedAt: env.Clock.Now().Add(-11 * time.Minute), FailureReason: "stored runtime failure"},
		{ScheduledRunID: "run-retired", ScheduledTaskID: scheduledTaskID, Status: schedulerapp.RunStatusFailed, TargetMode: schedulerapp.TargetModeAgentTask, AgentID: "agent-retired", CompletedAt: env.Clock.Now().Add(-8 * time.Minute), CreatedAt: env.Clock.Now().Add(-9 * time.Minute), FailureReason: "stored retired agent failure"},
		{ScheduledRunID: "run-skipped", ScheduledTaskID: scheduledTaskID, Status: schedulerapp.RunStatusSkipped, TargetMode: schedulerapp.TargetModeAgentTask, CompletedAt: env.Clock.Now().Add(-5 * time.Minute), CreatedAt: env.Clock.Now().Add(-6 * time.Minute), FailureReason: "stored scheduler skip"},
		{ScheduledRunID: "run-queued", ScheduledTaskID: scheduledTaskID, Status: schedulerapp.RunStatusQueued, TargetMode: schedulerapp.TargetModeAgentTask, CreatedAt: env.Clock.Now().Add(-3 * time.Minute)},
	}
	for _, run := range runs {
		if err := repository.SaveRun(ctx, run); err != nil {
			t.Fatal(err)
		}
	}
	modeEditedRun := schedulerapp.ScheduledRun{
		ScheduledRunID: "run-mode-edited", ScheduledTaskID: modeEditedTask.ScheduledTaskID, Status: schedulerapp.RunStatusFailed,
		TargetMode: schedulerapp.TargetModeAgentTask, ConfigurationSnapshotVersion: 1, ConfigurationSnapshot: &modeEditedSnapshot,
		TenantID: tenantID, ScheduledTaskTitle: modeEditedSnapshot.Title, AgentID: agentID,
		CompletedAt: env.Clock.Now().Add(-7 * time.Minute), CreatedAt: env.Clock.Now().Add(-8 * time.Minute), FailureReason: "stored failure after task mode edit",
	}
	if err := repository.SaveRun(ctx, modeEditedRun); err != nil {
		t.Fatal(err)
	}

	backfill := newProductionSchedulerMissionFactBackfill(productionSchedulerMissionFactBackfillOptions{
		Repository: repository, Missions: lifecycle, Outcomes: delivery,
		CreatorPermissions: schedulerCreatorPermissionResolver{permissions: []string{"memory.read"}}, Clock: env.Clock,
	})
	report, err := backfill.Run(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if report.ScannedTasks != 2 || report.EnsuredMissions != 2 || report.ImportedRuns != 6 || report.ApprovedRuns != 1 || report.SubmittedRuns != 1 || report.BlockedRuns != 3 || report.CancelledRuns != 1 || report.InsufficientEvidence != 1 || report.ActiveRunsUnchanged != 1 || report.StaleActiveRuns != 1 || report.FailedRecords != 0 || len(report.Issues) != 1 {
		t.Fatalf("backfill report = %+v", report)
	}

	storedTask, err := repository.GetTask(ctx, scheduledTaskID)
	if err != nil {
		t.Fatal(err)
	}
	missionID := scheduledMissionID(tenantID, scheduledTaskID)
	if storedTask.MissionID != missionID {
		t.Fatalf("scheduled Mission binding = %q, want %q", storedTask.MissionID, missionID)
	}
	wantTaskStates := map[string]missiontask.TaskStatus{
		"run-approved": missiontask.TaskStatusApproved, "run-submitted": missiontask.TaskStatusSubmitted,
		"run-failed": missiontask.TaskStatusBlocked, "run-retired": missiontask.TaskStatusBlocked, "run-skipped": missiontask.TaskStatusCancelled,
	}
	wantResponsibilityStates := map[string]responsibility.AssignmentState{
		"run-approved": responsibility.AssignmentStateFulfilled, "run-submitted": responsibility.AssignmentStateSubmitted,
		"run-failed": responsibility.AssignmentStateTerminated, "run-retired": responsibility.AssignmentStateTerminated, "run-skipped": responsibility.AssignmentStateTerminated,
	}
	for runID, wantStatus := range wantTaskStates {
		storedRun, err := repository.GetRun(ctx, runID)
		if err != nil {
			t.Fatal(err)
		}
		wantTaskID := scheduledMissionTaskID(tenantID, runID)
		wantResponsibilityID := responsibility.AssignmentID(tenantID, responsibility.WorkKindMissionTask, wantTaskID)
		wantAgentID := agentID
		if runID == "run-retired" {
			wantAgentID = "agent-retired"
		}
		if storedRun.MissionID != missionID || storedRun.TaskID != wantTaskID || storedRun.ResponsibilityID != wantResponsibilityID || storedRun.AgentID != wantAgentID {
			t.Fatalf("run %s fact links = %+v", runID, storedRun)
		}
		var taskRecord missiontask.TaskRecord
		if err := env.UnitOfWork.Do(ctx, func(ctx context.Context, tx uow.Tx) error {
			var getErr error
			taskRecord, getErr = tasks.GetTask(ctx, tx, missiontask.TaskID(wantTaskID))
			return getErr
		}); err != nil {
			t.Fatal(err)
		}
		if taskRecord.Status != wantStatus {
			t.Fatalf("run %s Task status = %q, want %q", runID, taskRecord.Status, wantStatus)
		}
		if runID == "run-approved" && (len(taskRecord.Contract.ExpectedDeliverables) != 1 || taskRecord.Contract.ExpectedDeliverables[0].ID != "report") {
			t.Fatalf("run %s expected deliverables = %+v, want immutable run snapshot contract", runID, taskRecord.Contract.ExpectedDeliverables)
		}
		assignment, err := responsibilities.GetAssignmentForWork(ctx, tenantID, responsibility.WorkKindMissionTask, wantTaskID)
		if err != nil {
			t.Fatal(err)
		}
		if assignment.State != wantResponsibilityStates[runID] {
			t.Fatalf("run %s Responsibility status = %q, want %q", runID, assignment.State, wantResponsibilityStates[runID])
		}
		activations, err := responsibilities.ListActivations(ctx, tenantID, wantResponsibilityID)
		if err != nil {
			t.Fatal(err)
		}
		attempts, err := responsibilities.ListAttempts(ctx, wantResponsibilityID)
		if err != nil {
			t.Fatal(err)
		}
		if len(activations) != 0 || len(attempts) != 0 {
			t.Fatalf("run %s created activations=%d attempts=%d", runID, len(activations), len(attempts))
		}
	}
	queued, err := repository.GetRun(ctx, "run-queued")
	if err != nil {
		t.Fatal(err)
	}
	if queued.MissionID != "" || queued.TaskID != "" || queued.ResponsibilityID != "" {
		t.Fatalf("active run was bound during history import: %+v", queued)
	}
	storedModeEditedTask, err := repository.GetTask(ctx, modeEditedTask.ScheduledTaskID)
	if err != nil {
		t.Fatal(err)
	}
	storedModeEditedRun, err := repository.GetRun(ctx, modeEditedRun.ScheduledRunID)
	if err != nil {
		t.Fatal(err)
	}
	wantModeEditedMissionID := scheduledMissionID(tenantID, modeEditedTask.ScheduledTaskID)
	wantModeEditedTaskID := scheduledMissionTaskID(tenantID, modeEditedRun.ScheduledRunID)
	if storedModeEditedTask.Target.Mode != schedulerapp.TargetModeDiscussionWorkflow || storedModeEditedTask.MissionID != wantModeEditedMissionID ||
		storedModeEditedRun.MissionID != wantModeEditedMissionID || storedModeEditedRun.TaskID != wantModeEditedTaskID || storedModeEditedRun.ResponsibilityID == "" {
		t.Fatalf("mode-edited v1 occurrence was not imported from its snapshot: task=%+v run=%+v", storedModeEditedTask, storedModeEditedRun)
	}

	replayed, err := backfill.Run(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if replayed.ReusedRuns != 6 || replayed.ImportedRuns != 0 || replayed.FailedRecords != 0 {
		t.Fatalf("replayed backfill report = %+v", replayed)
	}
}

func TestHistoricalScheduledTaskConfigurationUsesImmutableRunSnapshot(t *testing.T) {
	mutableContract := schedulerapp.AgentResultContract{Artifacts: []schedulerapp.ArtifactRequirement{{
		ID: "mutable", Kind: "report", PathPattern: `C:\mutable\report.md`, MinBytes: 1,
	}}}
	snapshotContract := schedulerapp.AgentResultContract{Artifacts: []schedulerapp.ArtifactRequirement{{
		ID: "snapshot", Kind: "report", PathPattern: `C:\snapshot\report.md`, MinBytes: 8,
	}}}
	mutableDelivery := schedulerapp.DeliveryPolicy{Mode: "announce", Channel: "feishu", RecipientID: "oc_mutable", ContentArtifactID: "mutable", Format: "text"}
	snapshotDelivery := schedulerapp.DeliveryPolicy{Mode: "announce", Channel: "feishu", RecipientID: "oc_snapshot", ContentArtifactID: "snapshot", Format: "markdown"}
	task := schedulerapp.ScheduledTask{
		TenantID: "tenant-history", ScheduledTaskID: "scheduled-history", Title: "Mutable title",
		Description: "Mutable description", CreatedBy: "mutable-owner", CreatedByKind: schedulerapp.ViewerKindHuman, CreatorProfileID: "mutable-profile",
		Target: schedulerapp.Target{
			Mode: schedulerapp.TargetModeAgentTask, AgentID: "agent-mutable", ApprovalAssigneeID: "human-mutable",
			TaskGoal: "Mutable goal", Prompt: "Mutable prompt", ResultContract: mutableContract,
		},
		Policy:    schedulerapp.Policy{MaxRetries: 9, RetryDelay: "9m"},
		Execution: schedulerapp.ExecutionPolicy{TimeoutSeconds: 60, LLMProfileID: "llm-mutable", AllowedToolIDs: []string{"tool-mutable"}},
		Delivery:  mutableDelivery,
	}
	snapshotTask := task
	snapshotTask.Title = "Snapshot title"
	snapshotTask.Description = "Snapshot description"
	snapshotTask.CreatedBy = "snapshot-owner"
	snapshotTask.CreatorProfileID = "snapshot-profile"
	snapshotTask.Target.AgentID = "agent-snapshot"
	snapshotTask.Target.ApprovalAssigneeID = "human-snapshot"
	snapshotTask.Target.TaskGoal = "Snapshot goal"
	snapshotTask.Target.Prompt = "Snapshot prompt"
	snapshotTask.Target.ResultContract = snapshotContract
	snapshotTask.Policy = schedulerapp.Policy{MaxRetries: 2, RetryDelay: "15s"}
	snapshotTask.Execution = schedulerapp.ExecutionPolicy{TimeoutSeconds: 900, LLMProfileID: "llm-snapshot", AllowedToolIDs: []string{"tool-snapshot"}}
	snapshotTask.Delivery = snapshotDelivery
	run := schedulerapp.ScheduledRun{
		ConfigurationSnapshotVersion: 1, TenantID: task.TenantID, ScheduledTaskID: task.ScheduledTaskID,
		ConfigurationSnapshot: &snapshotTask, ScheduledTaskTitle: "Snapshot title", TargetMode: schedulerapp.TargetModeAgentTask,
		ResultContract: snapshotContract, Delivery: snapshotDelivery, AgentID: "agent-snapshot",
	}
	run.ExecutionEnvelope = &agentcore.ExecutionEnvelope{
		TenantID: task.TenantID, RunID: "scheduled-run-history", ScheduledTaskID: task.ScheduledTaskID,
		ScheduledRunID: "scheduled-run-history", AgentID: "agent-snapshot", AgentProfileID: "agent-snapshot",
		PermissionScopes: []string{"memory.read"}, AllowedToolIDs: []string{"memory.search"},
	}
	if err := run.ExecutionEnvelope.SealAt(time.Date(2026, 9, 21, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("seal historical execution envelope: %v", err)
	}

	historical, err := historicalScheduledTaskConfiguration(task, run)
	if err != nil {
		t.Fatal(err)
	}
	if historical.TenantID != run.TenantID || historical.Title != run.ScheduledTaskTitle || historical.Target.Mode != run.TargetMode ||
		!reflect.DeepEqual(historical.Target.ResultContract, snapshotContract) || !reflect.DeepEqual(historical.Delivery, snapshotDelivery) {
		t.Fatalf("historical configuration = %+v, want immutable run snapshot", historical)
	}
	if historical.Description != "Snapshot description" || historical.Target.AgentID != "agent-snapshot" || historical.Target.ApprovalAssigneeID != "human-snapshot" ||
		historical.Target.TaskGoal != "Snapshot goal" || historical.Target.Prompt != "Snapshot prompt" || historical.Policy.MaxRetries != 2 ||
		historical.Execution.TimeoutSeconds != 900 || historical.CreatedBy != "snapshot-owner" || historical.CreatorProfileID != "snapshot-profile" {
		t.Fatalf("historical full configuration = %+v, want immutable run snapshot", historical)
	}
	if !reflect.DeepEqual(task.Target.ResultContract, mutableContract) || !reflect.DeepEqual(task.Delivery, mutableDelivery) {
		t.Fatalf("mutable task was modified: %+v", task)
	}
	req := scheduledAgentTaskRequestForHistory(historical, run, "agent-history", []string{"memory.read"})
	if req.TenantID != run.TenantID || req.Title != run.ScheduledTaskTitle || req.Description != "Snapshot description" || req.AgentID != "agent-history" ||
		req.TaskGoal != "Snapshot goal" || req.Prompt != "Snapshot prompt" || req.MaxAttempts != 3 || req.RetryDelay != "15s" ||
		req.Execution.TimeoutSeconds != 900 || req.PrincipalID != "snapshot-owner" || !reflect.DeepEqual(req.ResultContract, snapshotContract) || !reflect.DeepEqual(req.Delivery, snapshotDelivery) {
		t.Fatalf("historical Agent task request = %+v, want immutable run snapshot", req)
	}
	if req.ExecutionEnvelope == nil || req.ExecutionEnvelope.EnvelopeDigest != run.ExecutionEnvelope.EnvelopeDigest {
		t.Fatalf("historical Agent task request lost the persisted execution envelope: %+v", req.ExecutionEnvelope)
	}
}

func TestHistoricalScheduledTaskConfigurationRejectsInvalidImmutableSnapshot(t *testing.T) {
	task := schedulerapp.ScheduledTask{
		TenantID: "tenant-history", ScheduledTaskID: "scheduled-history", Title: "Mutable title",
		Target: schedulerapp.Target{Mode: schedulerapp.TargetModeAgentTask, AgentID: "agent-history", TaskGoal: "Run history task"},
	}
	snapshotTask := task
	snapshotTask.Title = "Snapshot title"
	snapshotTask.Target.ResultContract = schedulerapp.AgentResultContract{Artifacts: []schedulerapp.ArtifactRequirement{{ID: "report", Kind: "report", PathPattern: "/reports/report.md"}}}
	snapshotTask.Delivery = schedulerapp.DeliveryPolicy{Mode: "announce", Channel: "feishu", RecipientID: "oc_snapshot", ContentArtifactID: "report", Format: "markdown"}
	valid := schedulerapp.ScheduledRun{
		ConfigurationSnapshotVersion: 1, TenantID: task.TenantID, ScheduledTaskID: task.ScheduledTaskID,
		ConfigurationSnapshot: &snapshotTask, ScheduledTaskTitle: "Snapshot title", TargetMode: schedulerapp.TargetModeAgentTask, AgentID: "agent-history",
		ResultContract: schedulerapp.AgentResultContract{Artifacts: []schedulerapp.ArtifactRequirement{{ID: "report", Kind: "report", PathPattern: "/reports/report.md"}}},
		Delivery:       schedulerapp.DeliveryPolicy{Mode: "announce", Channel: "feishu", RecipientID: "oc_snapshot", ContentArtifactID: "report", Format: "markdown"},
	}
	tests := map[string]func(*schedulerapp.ScheduledRun){
		"negative version": func(run *schedulerapp.ScheduledRun) { run.ConfigurationSnapshotVersion = -1 },
		"missing snapshot": func(run *schedulerapp.ScheduledRun) { run.ConfigurationSnapshot = nil },
		"task mismatch":    func(run *schedulerapp.ScheduledRun) { run.ScheduledTaskID = "other-task" },
		"missing tenant":   func(run *schedulerapp.ScheduledRun) { run.TenantID = "" },
		"tenant mismatch":  func(run *schedulerapp.ScheduledRun) { run.TenantID = "other-tenant" },
		"missing title":    func(run *schedulerapp.ScheduledRun) { run.ScheduledTaskTitle = "" },
		"missing mode":     func(run *schedulerapp.ScheduledRun) { run.TargetMode = "" },
		"wrong mode":       func(run *schedulerapp.ScheduledRun) { run.TargetMode = schedulerapp.TargetModeDiscussionWorkflow },
		"duplicate contract artifact": func(run *schedulerapp.ScheduledRun) {
			run.ResultContract.Artifacts = append(run.ResultContract.Artifacts, run.ResultContract.Artifacts[0])
		},
		"incomplete delivery": func(run *schedulerapp.ScheduledRun) { run.Delivery.RecipientID = "" },
		"invalid format":      func(run *schedulerapp.ScheduledRun) { run.Delivery.Format = "html" },
		"unknown delivery artifact": func(run *schedulerapp.ScheduledRun) {
			run.Delivery.ContentArtifactID = "other-report"
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			run := valid
			run.ResultContract.Artifacts = append([]schedulerapp.ArtifactRequirement(nil), valid.ResultContract.Artifacts...)
			mutate(&run)
			if _, err := historicalScheduledTaskConfiguration(task, run); err == nil {
				t.Fatalf("invalid immutable snapshot was accepted: %+v", run)
			}
		})
	}
}

func TestHistoricalScheduledTaskConfigurationLegacyRunUsesMutableTask(t *testing.T) {
	task := schedulerapp.ScheduledTask{
		TenantID: "tenant-legacy", ScheduledTaskID: "scheduled-legacy", Title: "Mutable legacy title",
		Target:   schedulerapp.Target{Mode: schedulerapp.TargetModeAgentTask, ResultContract: schedulerapp.AgentResultContract{Artifacts: []schedulerapp.ArtifactRequirement{{ID: "legacy"}}}},
		Delivery: schedulerapp.DeliveryPolicy{Mode: "none"},
	}
	run := schedulerapp.ScheduledRun{ScheduledTaskID: task.ScheduledTaskID}

	historical, err := historicalScheduledTaskConfiguration(task, run)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(historical, task) {
		t.Fatalf("legacy historical configuration = %+v, want mutable task %+v", historical, task)
	}
}
