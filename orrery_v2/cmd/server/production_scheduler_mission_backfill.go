package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/missioncontrol"
	"orrery_v2/internal/app/responsibility"
	schedulerapp "orrery_v2/internal/app/scheduler"
	missioncore "orrery_v2/internal/mission/core"
	missiontask "orrery_v2/internal/mission/task"
	"orrery_v2/internal/platform/clock"
	"orrery_v2/internal/platform/logging"
)

type schedulerMissionFactRepository interface {
	ListTasks(context.Context, bool) ([]schedulerapp.ScheduledTask, error)
	SaveTask(context.Context, schedulerapp.ScheduledTask) error
	ListRunsForTask(context.Context, string) ([]schedulerapp.ScheduledRun, error)
	MutateRun(context.Context, string, func(*schedulerapp.ScheduledRun) error) (schedulerapp.ScheduledRun, error)
}

type schedulerHistoricalMissionCommandPort interface {
	schedulerMissionCommandPort
	ImportHistoricalTaskAssignment(context.Context, missioncontrol.ViewerRef, missioncontrol.ImportHistoricalTaskAssignmentCommand) (missioncontrol.CommandReceiptDTO, error)
}

type schedulerHistoricalTaskOutcomePort interface {
	ImportHistoricalOutcome(context.Context, missiontask.HistoricalTaskOutcomeInput) (missiontask.HistoricalTaskOutcomeResult, error)
}

type productionSchedulerMissionFactBackfillOptions struct {
	Repository         schedulerMissionFactRepository
	Missions           schedulerHistoricalMissionCommandPort
	Outcomes           schedulerHistoricalTaskOutcomePort
	CreatorPermissions schedulerapp.CreatorPermissionResolver
	Clock              clock.Clock
}

type productionSchedulerMissionFactBackfill struct {
	repository         schedulerMissionFactRepository
	missions           schedulerHistoricalMissionCommandPort
	outcomes           schedulerHistoricalTaskOutcomePort
	creatorPermissions schedulerapp.CreatorPermissionResolver
	clock              clock.Clock
}

type schedulerMissionFactBackfillIssue struct {
	ScheduledTaskID string `json:"scheduled_task_id"`
	ScheduledRunID  string `json:"scheduled_run_id,omitempty"`
	Stage           string `json:"stage"`
	Error           string `json:"error"`
}

type schedulerMissionFactBackfillReport struct {
	ScannedTasks         int                                 `json:"scanned_tasks"`
	ScannedRuns          int                                 `json:"scanned_runs"`
	EnsuredMissions      int                                 `json:"ensured_missions"`
	ImportedRuns         int                                 `json:"imported_runs"`
	ReusedRuns           int                                 `json:"reused_runs"`
	ApprovedRuns         int                                 `json:"approved_runs"`
	SubmittedRuns        int                                 `json:"submitted_runs"`
	BlockedRuns          int                                 `json:"blocked_runs"`
	CancelledRuns        int                                 `json:"cancelled_runs"`
	InsufficientEvidence int                                 `json:"insufficient_evidence"`
	ActiveRunsUnchanged  int                                 `json:"active_runs_unchanged"`
	StaleActiveRuns      int                                 `json:"stale_active_runs"`
	FailedRecords        int                                 `json:"failed_records"`
	Issues               []schedulerMissionFactBackfillIssue `json:"issues,omitempty"`
}

func newProductionSchedulerMissionFactBackfill(opts productionSchedulerMissionFactBackfillOptions) *productionSchedulerMissionFactBackfill {
	backfillClock := opts.Clock
	if backfillClock == nil {
		backfillClock = clock.Real{}
	}
	return &productionSchedulerMissionFactBackfill{
		repository: opts.Repository, missions: opts.Missions, outcomes: opts.Outcomes,
		creatorPermissions: opts.CreatorPermissions, clock: backfillClock,
	}
}

func runProductionSchedulerMissionFactBackfill(
	ctx context.Context,
	repository schedulerMissionFactRepository,
	missions schedulerHistoricalMissionCommandPort,
	outcomes schedulerHistoricalTaskOutcomePort,
	profiles schedulerapp.AgentProfilePermissionSource,
	platformClock clock.Clock,
	logger logging.Logger,
) {
	backfill := newProductionSchedulerMissionFactBackfill(productionSchedulerMissionFactBackfillOptions{
		Repository: repository, Missions: missions, Outcomes: outcomes,
		CreatorPermissions: schedulerapp.NewAgentProfileCreatorPermissionResolver(profiles), Clock: platformClock,
	})
	report, err := backfill.Run(ctx)
	if err != nil {
		logger.Warn(ctx, "scheduler Mission fact backfill unavailable", logging.Any("error", err))
		return
	}
	for _, issue := range report.Issues {
		logger.Warn(ctx, "scheduler Mission fact record not imported", logging.Any("issue", issue))
	}
	logger.Info(ctx, "scheduler Mission fact backfill completed", logging.Any("report", report))
}

func (b *productionSchedulerMissionFactBackfill) Run(ctx context.Context) (schedulerMissionFactBackfillReport, error) {
	var report schedulerMissionFactBackfillReport
	if b == nil || b.repository == nil || b.missions == nil || b.outcomes == nil || b.clock == nil {
		return report, schedulerapp.ErrServiceDependency
	}
	tasks, err := b.repository.ListTasks(ctx, true)
	if err != nil {
		return report, err
	}
	missionLifecycle := productionSchedulerMissionLifecycle{missions: b.missions}
	for _, task := range tasks {
		runs, err := b.repository.ListRunsForTask(ctx, task.ScheduledTaskID)
		if err != nil {
			if task.Target.Mode == schedulerapp.TargetModeAgentTask {
				report.ScannedTasks++
				report.addIssue(task.ScheduledTaskID, "", "list_runs", err)
			}
			continue
		}
		historicalTasks := make(map[string]schedulerapp.ScheduledTask, len(runs))
		historicalErrors := make(map[string]error)
		hasVersionedAgentRun := false
		for _, run := range runs {
			if run.ConfigurationSnapshotVersion <= 0 || run.TargetMode != schedulerapp.TargetModeAgentTask {
				continue
			}
			hasVersionedAgentRun = true
			historicalTask, configErr := historicalScheduledTaskConfiguration(task, run)
			if configErr != nil {
				historicalErrors[run.ScheduledRunID] = configErr
				continue
			}
			historicalTasks[run.ScheduledRunID] = historicalTask
		}
		if task.Target.Mode != schedulerapp.TargetModeAgentTask && !hasVersionedAgentRun {
			continue
		}
		report.ScannedTasks++
		missionTask := task
		if task.Target.Mode != schedulerapp.TargetModeAgentTask {
			missionTask = schedulerapp.ScheduledTask{}
			for _, run := range runs {
				if historicalTask, ok := historicalTasks[run.ScheduledRunID]; ok {
					missionTask = historicalTask
					break
				}
			}
			if strings.TrimSpace(missionTask.ScheduledTaskID) == "" {
				for _, run := range runs {
					if configErr := historicalErrors[run.ScheduledRunID]; configErr != nil {
						report.ScannedRuns++
						report.addIssue(task.ScheduledTaskID, run.ScheduledRunID, "validate_run", configErr)
					}
				}
				continue
			}
		}
		expectedMissionID := scheduledMissionID(task.TenantID, task.ScheduledTaskID)
		if task.MissionID != "" && strings.TrimSpace(task.MissionID) != expectedMissionID {
			report.addIssue(task.ScheduledTaskID, "", "ensure_mission", fmt.Errorf("stored Mission %q does not match stable scheduler Mission %q", task.MissionID, expectedMissionID))
			continue
		}
		missionID, err := missionLifecycle.EnsureScheduledMission(ctx, missionTask)
		if err != nil {
			report.addIssue(task.ScheduledTaskID, "", "ensure_mission", err)
			continue
		}
		if missionID != expectedMissionID {
			report.addIssue(task.ScheduledTaskID, "", "ensure_mission", fmt.Errorf("resolved Mission %q does not match stable scheduler Mission %q", missionID, expectedMissionID))
			continue
		}
		if strings.TrimSpace(task.MissionID) == "" {
			task.MissionID = missionID
			task.UpdatedAt = b.clock.Now().UTC()
			if err := b.repository.SaveTask(ctx, task); err != nil {
				report.addIssue(task.ScheduledTaskID, "", "bind_mission", err)
				continue
			}
		}
		report.EnsuredMissions++
		var permissions []string
		var permissionErr error
		permissionsResolved := false
		for _, run := range runs {
			if run.ConfigurationSnapshotVersion > 0 && run.TargetMode != schedulerapp.TargetModeAgentTask {
				continue
			}
			if run.ConfigurationSnapshotVersion == 0 && task.Target.Mode != schedulerapp.TargetModeAgentTask {
				continue
			}
			report.ScannedRuns++
			switch run.Status {
			case schedulerapp.RunStatusQueued, schedulerapp.RunStatusRunning:
				report.ActiveRunsUnchanged++
				if !run.CreatedAt.IsZero() && run.CreatedAt.Before(b.clock.Now().Add(-2*time.Minute)) {
					report.StaleActiveRuns++
					report.addAnomaly(task.ScheduledTaskID, run.ScheduledRunID, "active_run_unchanged", fmt.Errorf("stored %s run is stale and remains unbound for the scheduler reconciler", run.Status))
				}
				continue
			case schedulerapp.RunStatusCompleted, schedulerapp.RunStatusFailed, schedulerapp.RunStatusSkipped:
			default:
				continue
			}
			historicalTask, ok := historicalTasks[run.ScheduledRunID]
			if !ok {
				var configErr error
				historicalTask, configErr = historicalScheduledTaskConfiguration(task, run)
				if configErr != nil {
					report.addIssue(task.ScheduledTaskID, run.ScheduledRunID, "validate_run", configErr)
					continue
				}
			}
			if storedMissionID := strings.TrimSpace(historicalTask.MissionID); storedMissionID != "" && storedMissionID != missionID {
				report.addIssue(task.ScheduledTaskID, run.ScheduledRunID, "validate_run", fmt.Errorf("stored configuration snapshot Mission %q does not match stable scheduler Mission %q", storedMissionID, missionID))
				continue
			}
			historicalTask.MissionID = missionID
			if runHasFactLinks(run) {
				if runHasExpectedFactLinks(historicalTask, run) {
					report.ReusedRuns++
				} else {
					report.addIssue(task.ScheduledTaskID, run.ScheduledRunID, "validate_links", fmt.Errorf("stored scheduler fact links conflict with deterministic identities"))
				}
				continue
			}
			if !permissionsResolved {
				permissions, permissionErr = b.resolveCreatorPermissions(ctx, historicalTask)
				permissionsResolved = true
			}
			if permissionErr != nil {
				report.addIssue(task.ScheduledTaskID, run.ScheduledRunID, "resolve_creator_permissions", permissionErr)
				continue
			}
			if err := b.importRun(ctx, historicalTask, run, permissions, &report); err != nil {
				report.addIssue(task.ScheduledTaskID, run.ScheduledRunID, "import_run", err)
			}
		}
	}
	return report, nil
}

func (b *productionSchedulerMissionFactBackfill) importRun(ctx context.Context, task schedulerapp.ScheduledTask, run schedulerapp.ScheduledRun, permissions []string, report *schedulerMissionFactBackfillReport) error {
	if run.MissionID != "" || run.TaskID != "" || run.ResponsibilityID != "" {
		return fmt.Errorf("partial scheduler fact links cannot be overwritten")
	}
	occurredAt := historicalScheduledRunTime(run)
	if occurredAt.IsZero() {
		return fmt.Errorf("stored terminal run has no occurrence time")
	}
	agentID := firstNonBlank(run.AgentID, task.Target.AgentID)
	if strings.TrimSpace(agentID) == "" {
		return fmt.Errorf("stored terminal run has no Agent identity")
	}
	outcome, result, evidenceValidated, responsibilityState, reason, err := historicalScheduledRunOutcome(task, run, occurredAt)
	if err != nil {
		return err
	}
	req := scheduledAgentTaskRequestForHistory(task, run, agentID, permissions)
	taskCommand := scheduledMissionTaskCommand(req, task.MissionID)
	created, err := b.missions.CreateTask(ctx, schedulerMissionViewer(task.TenantID), taskCommand)
	if errors.Is(err, missioncontrol.ErrInvalidRequest) {
		// Historical runs can reference an agent profile that has since been retired.
		taskCommand.AssigneeID = ""
		created, err = b.missions.CreateTask(ctx, schedulerMissionViewer(task.TenantID), taskCommand)
	}
	if err != nil {
		return err
	}
	expectedTaskID := scheduledMissionTaskID(task.TenantID, run.ScheduledRunID)
	if created.TaskID != expectedTaskID {
		return fmt.Errorf("created Task %q does not match deterministic Task %q", created.TaskID, expectedTaskID)
	}

	runKey := "scheduler-agent-task:" + run.ScheduledRunID
	if _, err := b.missions.ImportHistoricalTaskAssignment(ctx, schedulerMissionViewer(task.TenantID), missioncontrol.ImportHistoricalTaskAssignmentCommand{
		IdempotencyKey: runKey + ":historical-assign", MissionID: missioncore.MissionID(task.MissionID), TaskID: missioncore.TaskID(expectedTaskID),
		ScheduledTaskID: task.ScheduledTaskID, ScheduledRunID: run.ScheduledRunID, AssigneeID: agentID,
		ResponsibilityState: responsibilityState, Reason: reason, OccurredAt: occurredAt,
	}); err != nil {
		return err
	}
	imported, err := b.outcomes.ImportHistoricalOutcome(ctx, missiontask.HistoricalTaskOutcomeInput{
		TaskID: missiontask.TaskID(expectedTaskID), MissionID: missioncore.MissionID(task.MissionID), AgentProfileID: agentcore.AgentProfileID(agentID),
		Outcome: outcome, Result: result, EvidenceValidated: evidenceValidated, FailureReason: run.FailureReason,
		IdempotencyKey: runKey + ":historical-outcome", OccurredAt: occurredAt,
	})
	if err != nil {
		return err
	}
	expectedResponsibilityID := responsibility.AssignmentID(task.TenantID, responsibility.WorkKindMissionTask, expectedTaskID)
	if _, err := b.repository.MutateRun(ctx, run.ScheduledRunID, func(current *schedulerapp.ScheduledRun) error {
		if current.Status != run.Status || current.ScheduledTaskID != task.ScheduledTaskID || current.MissionID != "" || current.TaskID != "" || current.ResponsibilityID != "" {
			return schedulerapp.ErrRevisionConflict
		}
		current.MissionID = task.MissionID
		current.TaskID = expectedTaskID
		current.ResponsibilityID = expectedResponsibilityID
		current.AgentID = agentID
		if strings.TrimSpace(current.CommandID) == "" {
			current.CommandID = expectedResponsibilityID
		}
		return nil
	}); err != nil {
		return err
	}
	report.ImportedRuns++
	if run.Status == schedulerapp.RunStatusCompleted && !evidenceValidated {
		report.InsufficientEvidence++
	}
	switch imported.Status {
	case missiontask.TaskStatusApproved:
		report.ApprovedRuns++
	case missiontask.TaskStatusSubmitted:
		report.SubmittedRuns++
	case missiontask.TaskStatusBlocked:
		report.BlockedRuns++
	case missiontask.TaskStatusCancelled:
		report.CancelledRuns++
	}
	return nil
}

func (b *productionSchedulerMissionFactBackfill) resolveCreatorPermissions(ctx context.Context, task schedulerapp.ScheduledTask) ([]string, error) {
	if b.creatorPermissions == nil {
		return nil, nil
	}
	resolved, err := b.creatorPermissions.ResolveCreatorPermissions(ctx, schedulerapp.CreatorRef{
		ViewerID: strings.TrimSpace(task.CreatedBy), ViewerKind: task.CreatedByKind,
		DisplayName: strings.TrimSpace(task.CreatedByName), ProfileID: strings.TrimSpace(task.CreatorProfileID),
	})
	if err != nil {
		return nil, err
	}
	return cleanScheduledPermissionScopes(resolved.PermissionScope), nil
}

func (r *schedulerMissionFactBackfillReport) addIssue(scheduledTaskID, scheduledRunID, stage string, err error) {
	r.FailedRecords++
	r.addAnomaly(scheduledTaskID, scheduledRunID, stage, err)
}

func (r *schedulerMissionFactBackfillReport) addAnomaly(scheduledTaskID, scheduledRunID, stage string, err error) {
	r.Issues = append(r.Issues, schedulerMissionFactBackfillIssue{
		ScheduledTaskID: scheduledTaskID, ScheduledRunID: scheduledRunID, Stage: stage, Error: err.Error(),
	})
}

func runHasFactLinks(run schedulerapp.ScheduledRun) bool {
	return strings.TrimSpace(run.MissionID) != "" || strings.TrimSpace(run.TaskID) != "" || strings.TrimSpace(run.ResponsibilityID) != ""
}

func historicalScheduledTaskConfiguration(task schedulerapp.ScheduledTask, run schedulerapp.ScheduledRun) (schedulerapp.ScheduledTask, error) {
	if run.ConfigurationSnapshotVersion < 0 {
		return schedulerapp.ScheduledTask{}, fmt.Errorf("stored configuration snapshot version %d is invalid", run.ConfigurationSnapshotVersion)
	}
	if run.ConfigurationSnapshotVersion == 0 {
		if run.TargetMode != "" && run.TargetMode != schedulerapp.TargetModeAgentTask {
			return schedulerapp.ScheduledTask{}, fmt.Errorf("stored target mode %q is not agent_task", run.TargetMode)
		}
		return task, nil
	}
	historical, err := schedulerapp.ResolveScheduledRunConfiguration(task, run)
	if err != nil {
		return schedulerapp.ScheduledTask{}, err
	}
	if historical.Target.Mode != schedulerapp.TargetModeAgentTask {
		return schedulerapp.ScheduledTask{}, fmt.Errorf("stored configuration snapshot target mode %q is not agent_task", historical.Target.Mode)
	}
	if err := validateHistoricalScheduledRunContracts(historical.Target.ResultContract, historical.Delivery); err != nil {
		return schedulerapp.ScheduledTask{}, err
	}
	return historical, nil
}

func validateHistoricalScheduledRunContracts(contract schedulerapp.AgentResultContract, delivery schedulerapp.DeliveryPolicy) error {
	artifactIDs := make(map[string]bool, len(contract.Artifacts))
	for _, requirement := range contract.Artifacts {
		artifactID := strings.TrimSpace(requirement.ID)
		if artifactID == "" || artifactIDs[artifactID] {
			return fmt.Errorf("stored configuration snapshot result contract has a missing or duplicate artifact identity")
		}
		artifactIDs[artifactID] = true
	}
	mode := strings.TrimSpace(delivery.Mode)
	switch mode {
	case "", "none":
	case "announce":
		if strings.TrimSpace(delivery.Channel) == "" || strings.TrimSpace(delivery.RecipientID) == "" {
			return fmt.Errorf("stored configuration snapshot delivery target is incomplete")
		}
		format := strings.ToLower(strings.TrimSpace(delivery.Format))
		if format != "" && format != "text" && format != "markdown" {
			return fmt.Errorf("stored configuration snapshot delivery format %q is invalid", delivery.Format)
		}
	default:
		return fmt.Errorf("stored configuration snapshot delivery mode %q is invalid", delivery.Mode)
	}
	contentArtifactID := strings.TrimSpace(delivery.ContentArtifactID)
	if contentArtifactID != "" && !artifactIDs[contentArtifactID] {
		return fmt.Errorf("stored configuration snapshot delivery artifact %q is absent from the result contract", contentArtifactID)
	}
	return nil
}

func runHasExpectedFactLinks(task schedulerapp.ScheduledTask, run schedulerapp.ScheduledRun) bool {
	taskID := scheduledMissionTaskID(task.TenantID, run.ScheduledRunID)
	return strings.TrimSpace(run.MissionID) == scheduledMissionID(task.TenantID, task.ScheduledTaskID) &&
		strings.TrimSpace(run.TaskID) == taskID &&
		strings.TrimSpace(run.ResponsibilityID) == responsibility.AssignmentID(task.TenantID, responsibility.WorkKindMissionTask, taskID)
}

func historicalScheduledRunTime(run schedulerapp.ScheduledRun) time.Time {
	if !run.CompletedAt.IsZero() {
		return run.CompletedAt.UTC()
	}
	if !run.StartedAt.IsZero() {
		return run.StartedAt.UTC()
	}
	return run.CreatedAt.UTC()
}

func scheduledAgentTaskRequestForHistory(task schedulerapp.ScheduledTask, run schedulerapp.ScheduledRun, agentID string, permissions []string) schedulerapp.AgentTaskRequest {
	return schedulerapp.AgentTaskRequest{
		TenantID: task.TenantID, MissionID: task.MissionID, RunID: run.ScheduledRunID, AgentTaskID: firstNonBlank(task.AgentTaskID, schedulerapp.AgentTaskIDForScheduledTask(task.ScheduledTaskID)),
		ScheduledTaskID: task.ScheduledTaskID, Title: task.Title, Description: task.Description,
		AgentID: agentID, TaskGoal: task.Target.TaskGoal, Prompt: firstNonBlank(task.Target.Prompt, task.Target.TaskGoal),
		PrincipalID: task.CreatedBy, CreatorProfileID: task.CreatorProfileID, CreatorKind: task.CreatedByKind, CreatorName: task.CreatedByName,
		Permissions: append([]string(nil), permissions...), MaxAttempts: max(1, task.Policy.MaxRetries+1), RetryDelay: task.Policy.RetryDelay,
		ResultContract: task.Target.ResultContract, Execution: task.Execution, Delivery: task.Delivery,
		ExecutionEnvelope: agentcore.CloneExecutionEnvelope(run.ExecutionEnvelope),
	}
}

func historicalScheduledRunOutcome(task schedulerapp.ScheduledTask, run schedulerapp.ScheduledRun, occurredAt time.Time) (missiontask.HistoricalTaskOutcome, agentcore.AgentResult, bool, string, string, error) {
	switch run.Status {
	case schedulerapp.RunStatusCompleted:
		result, validated := historicalScheduledAgentResult(task, run, occurredAt)
		if validated {
			return missiontask.HistoricalTaskOutcomeCompleted, result, true, "fulfilled", "stored completed run with revalidated evidence", nil
		}
		return missiontask.HistoricalTaskOutcomeCompleted, result, false, "submitted", "stored completed run without sufficient evidence", nil
	case schedulerapp.RunStatusFailed:
		if strings.TrimSpace(run.FailureReason) == "" {
			return "", agentcore.AgentResult{}, false, "", "", fmt.Errorf("stored failed run has no failure reason")
		}
		return missiontask.HistoricalTaskOutcomeFailed, agentcore.AgentResult{}, false, "terminated", run.FailureReason, nil
	case schedulerapp.RunStatusSkipped:
		if strings.TrimSpace(run.FailureReason) == "" {
			return "", agentcore.AgentResult{}, false, "", "", fmt.Errorf("stored skipped run has no skip reason")
		}
		return missiontask.HistoricalTaskOutcomeSkipped, agentcore.AgentResult{}, false, "terminated", run.FailureReason, nil
	default:
		return "", agentcore.AgentResult{}, false, "", "", fmt.Errorf("run status %q is not terminal", run.Status)
	}
}

func historicalScheduledAgentResult(task schedulerapp.ScheduledTask, run schedulerapp.ScheduledRun, occurredAt time.Time) (agentcore.AgentResult, bool) {
	result := agentcore.AgentResult{
		ID:        agentcore.AgentResultID("scheduler-history-result:" + run.ScheduledRunID),
		MissionID: missioncore.MissionID(task.MissionID), TaskID: missioncore.TaskID(scheduledMissionTaskID(task.TenantID, run.ScheduledRunID)),
		AgentID: agentcore.AgentID(firstNonBlank(run.AgentID, task.Target.AgentID)), Summary: agentcore.Summary{Text: strings.TrimSpace(run.Result.Summary)}, CreatedAt: occurredAt,
	}
	if result.Summary.IsZero() || len(task.Target.ResultContract.Artifacts) == 0 {
		return result, false
	}
	used := make(map[int]bool, len(run.Result.Artifacts))
	artifactIDs := make(map[string]bool, len(task.Target.ResultContract.Artifacts))
	evidenceIDs := make(map[string]bool, len(task.Target.ResultContract.Artifacts))
	for _, requirement := range task.Target.ResultContract.Artifacts {
		artifact, index, ok := revalidateHistoricalRunArtifact(requirement, run, used)
		if !ok || artifactIDs[artifact.ID] || evidenceIDs[artifact.EvidenceID] {
			return result, false
		}
		used[index] = true
		artifactIDs[artifact.ID] = true
		evidenceIDs[artifact.EvidenceID] = true
		ref := agentcore.ArtifactRef{ID: artifact.ID, Kind: agentcore.ArtifactKind(artifact.Kind), URI: artifact.URI}
		result.Deliverables = append(result.Deliverables, agentcore.Deliverable{
			ID:                    agentcore.DeliverableID("scheduler-history-deliverable:" + run.ScheduledRunID + ":" + requirement.ID),
			ExpectedDeliverableID: requirement.ID, Title: requirement.Kind + " artifact", ArtifactRef: ref,
		})
		result.Evidence = append(result.Evidence, agentcore.Evidence{
			ID: agentcore.EvidenceID(artifact.EvidenceID), EvidenceRequirementID: "evidence-" + requirement.ID,
			AcceptanceCriterionID: "acceptance-1", Kind: agentcore.EvidenceKindArtifact, ArtifactRef: ref,
		})
	}
	return result, true
}

func revalidateHistoricalRunArtifact(requirement schedulerapp.ArtifactRequirement, run schedulerapp.ScheduledRun, used map[int]bool) (schedulerapp.RunArtifact, int, bool) {
	if strings.TrimSpace(requirement.ID) == "" || strings.TrimSpace(requirement.Kind) == "" || strings.TrimSpace(requirement.PathPattern) == "" || requirement.MinBytes < 0 {
		return schedulerapp.RunArtifact{}, 0, false
	}
	for index, artifact := range run.Result.Artifacts {
		if used[index] || strings.TrimSpace(artifact.ID) == "" || strings.TrimSpace(artifact.EvidenceID) == "" || artifact.Kind != requirement.Kind || strings.TrimSpace(artifact.URI) == "" {
			continue
		}
		matched, err := filepath.Match(requirement.PathPattern, artifact.URI)
		if err != nil || !matched {
			continue
		}
		info, err := os.Stat(artifact.URI)
		minimumSize := requirement.MinBytes
		if minimumSize == 0 {
			minimumSize = 1
		}
		if err != nil || !info.Mode().IsRegular() || info.Size() < minimumSize || artifact.SizeBytes != info.Size() {
			continue
		}
		if !run.StartedAt.IsZero() && info.ModTime().Before(run.StartedAt.Add(-2*time.Second)) {
			continue
		}
		return artifact, index, true
	}
	return schedulerapp.RunArtifact{}, 0, false
}
