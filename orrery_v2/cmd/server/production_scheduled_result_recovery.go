package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	runtimedaemon "orrery_v2/internal/agent/runtime/daemon"
	schedulerapp "orrery_v2/internal/app/scheduler"
	missiontask "orrery_v2/internal/mission/task"
	"orrery_v2/internal/platform/logging"
	"orrery_v2/internal/platform/uow"
)

type scheduledCommittedResultRepository interface {
	GetTask(context.Context, uow.Tx, missiontask.TaskID) (missiontask.TaskRecord, error)
	FindSubmissionByIdempotencyKey(context.Context, uow.Tx, missiontask.TaskID, string) (missiontask.TaskSubmission, bool, error)
	ListDeliverableSubmissions(context.Context, uow.Tx, missiontask.TaskSubmissionID) ([]missiontask.DeliverableSubmission, error)
	ListEvidenceSubmissions(context.Context, uow.Tx, missiontask.TaskSubmissionID) ([]missiontask.EvidenceSubmission, error)
}

func loadCommittedScheduledAgentResult(ctx context.Context, unit *uow.UnitOfWork, repository scheduledCommittedResultRepository, run runtimedaemon.AgentRun) (agentcore.AgentResult, bool, error) {
	if unit == nil || repository == nil {
		return agentcore.AgentResult{}, false, fmt.Errorf("scheduled committed-result recovery is not configured")
	}
	taskID := missiontask.TaskID(strings.TrimSpace(run.TaskID))
	if taskID.IsZero() || run.RunID.IsZero() || run.AgentID.IsZero() {
		return agentcore.AgentResult{}, false, fmt.Errorf("scheduled run identity is incomplete")
	}
	var task missiontask.TaskRecord
	var submission missiontask.TaskSubmission
	var deliverables []missiontask.DeliverableSubmission
	var evidence []missiontask.EvidenceSubmission
	found := false
	err := unit.Do(ctx, func(ctx context.Context, tx uow.Tx) error {
		var err error
		task, err = repository.GetTask(ctx, tx, taskID)
		if err != nil {
			return err
		}
		submission, found, err = repository.FindSubmissionByIdempotencyKey(ctx, tx, taskID, run.RunID.String())
		if err != nil || !found {
			return err
		}
		deliverables, err = repository.ListDeliverableSubmissions(ctx, tx, submission.ID)
		if err != nil {
			return err
		}
		evidence, err = repository.ListEvidenceSubmissions(ctx, tx, submission.ID)
		return err
	})
	if err != nil || !found {
		return agentcore.AgentResult{}, found, err
	}
	if task.Contract.ID != taskID || task.Contract.MissionID.String() != strings.TrimSpace(run.MissionID) ||
		submission.TaskID != taskID || submission.RunID != run.RunID || submission.AgentID != run.AgentID ||
		strings.TrimSpace(submission.IdempotencyKey) != run.RunID.String() {
		return agentcore.AgentResult{}, true, fmt.Errorf("scheduled committed result identity does not match the runtime run")
	}
	if len(deliverables) != len(submission.DeliverableRefs) || len(evidence) != len(submission.EvidenceRefs) {
		return agentcore.AgentResult{}, true, fmt.Errorf("scheduled committed result child references are incomplete")
	}

	deliverableRefs := make(map[agentcore.DeliverableID]bool, len(submission.DeliverableRefs))
	for _, id := range submission.DeliverableRefs {
		if id == "" || deliverableRefs[id] {
			return agentcore.AgentResult{}, true, fmt.Errorf("scheduled committed result has duplicate deliverable references")
		}
		deliverableRefs[id] = true
	}
	evidenceRefs := make(map[agentcore.EvidenceID]bool, len(submission.EvidenceRefs))
	for _, id := range submission.EvidenceRefs {
		if id == "" || evidenceRefs[id] {
			return agentcore.AgentResult{}, true, fmt.Errorf("scheduled committed result has duplicate evidence references")
		}
		evidenceRefs[id] = true
	}
	result := agentcore.AgentResult{
		ID: submission.AgentResultRef, MissionID: task.Contract.MissionID, TaskID: task.Contract.ID,
		AgentID: submission.AgentID, RunID: submission.RunID, Summary: submission.Summary,
		UserReport:       submission.UserReport,
		Blocker:          cloneScheduledAgentResultBlocker(submission.Blocker),
		InternalTraceRef: submission.InternalTraceRef, CreatedAt: submission.SubmittedAt,
	}
	for _, item := range deliverables {
		if item.SubmissionID != submission.ID || item.TaskID != taskID || item.AgentID != run.AgentID || !deliverableRefs[item.Deliverable.ID] {
			return agentcore.AgentResult{}, true, fmt.Errorf("scheduled committed deliverable identity is inconsistent")
		}
		delete(deliverableRefs, item.Deliverable.ID)
		result.Deliverables = append(result.Deliverables, item.Deliverable)
	}
	for _, item := range evidence {
		if item.SubmissionID != submission.ID || item.TaskID != taskID || item.AgentID != run.AgentID || !evidenceRefs[item.Evidence.ID] {
			return agentcore.AgentResult{}, true, fmt.Errorf("scheduled committed evidence identity is inconsistent")
		}
		delete(evidenceRefs, item.Evidence.ID)
		result.Evidence = append(result.Evidence, item.Evidence)
	}
	if len(deliverableRefs) != 0 || len(evidenceRefs) != 0 {
		return agentcore.AgentResult{}, true, fmt.Errorf("scheduled committed result references do not match stored children")
	}
	return result, true, nil
}

func cloneScheduledAgentResultBlocker(blocker *agentcore.Blocker) *agentcore.Blocker {
	if blocker == nil {
		return nil
	}
	return &agentcore.Blocker{Summary: strings.TrimSpace(blocker.Summary), Report: blocker.Report}
}

func scheduledRunResultFromCommittedAgentResult(run runtimedaemon.AgentRun, result agentcore.AgentResult) (schedulerapp.RunResult, error) {
	if result.RunID != run.RunID || result.TaskID.String() != strings.TrimSpace(run.TaskID) || result.AgentID != run.AgentID {
		return schedulerapp.RunResult{}, fmt.Errorf("committed Agent result does not match scheduled run")
	}
	evidenceByArtifact := make(map[string]string, len(result.Evidence))
	for _, item := range result.Evidence {
		if item.ArtifactRef.IsZero() {
			continue
		}
		key := strings.TrimSpace(item.ArtifactRef.ID) + "\x00" + strings.TrimSpace(item.ArtifactRef.URI)
		if _, duplicate := evidenceByArtifact[key]; duplicate {
			return schedulerapp.RunResult{}, fmt.Errorf("committed Agent result has duplicate artifact evidence")
		}
		evidenceByArtifact[key] = string(item.ID)
	}

	contractIDs := map[string]schedulerapp.ArtifactRequirement{}
	if raw := strings.TrimSpace(run.Request.RuntimeOptions.ProviderSpecific[schedulerResultContractProviderKey]); raw != "" {
		contract, err := decodeSchedulerResultContract(raw)
		if err != nil {
			return schedulerapp.RunResult{}, err
		}
		for _, requirement := range contract.Artifacts {
			id := strings.TrimSpace(requirement.ID)
			if id == "" || contractIDs[id].ID != "" {
				return schedulerapp.RunResult{}, fmt.Errorf("scheduled result contract has duplicate artifact IDs")
			}
			contractIDs[id] = requirement
		}
	}

	out := schedulerapp.RunResult{}
	artifacts := append([]agentcore.Deliverable(nil), result.Deliverables...)
	if result.Blocker != nil && !result.Blocker.Report.IsZero() {
		artifacts = append(artifacts, agentcore.Deliverable{ArtifactRef: result.Blocker.Report})
	}
	seen := make(map[string]bool, len(artifacts))
	for index, deliverable := range artifacts {
		ref := deliverable.ArtifactRef
		key := strings.TrimSpace(ref.ID) + "\x00" + strings.TrimSpace(ref.URI)
		if ref.IsZero() || seen[key] {
			return schedulerapp.RunResult{}, fmt.Errorf("committed Agent result has a missing or duplicate deliverable artifact")
		}
		fileName, sizeBytes, digest, ok := scheduledArtifactMetadataFromTypedURI(ref.URI)
		if !ok {
			return schedulerapp.RunResult{}, fmt.Errorf("committed scheduled artifact %q is not an immutable snapshot", ref.ID)
		}
		storedKey, _ := scheduledArtifactKeyFromTypedURI(ref.URI)
		expectedKey := scheduledArtifactStorageKey(
			run.Request.TenantID, scheduledRunIDFromRun(run), ref.ID, sizeBytes, digest, fileName,
		)
		if storedKey != expectedKey {
			return schedulerapp.RunResult{}, fmt.Errorf("committed scheduled artifact %q does not belong to this scheduled run", ref.ID)
		}
		if len(contractIDs) > 0 && index < len(result.Deliverables) {
			requirementID := strings.TrimSpace(deliverable.ExpectedDeliverableID)
			requirement, exists := contractIDs[requirementID]
			if !exists || ref.ID != "scheduled-artifact:"+run.RunID.String()+":"+requirementID || strings.TrimSpace(string(ref.Kind)) != strings.TrimSpace(requirement.Kind) {
				return schedulerapp.RunResult{}, fmt.Errorf("committed scheduled artifact %q does not match its result contract", ref.ID)
			}
			delete(contractIDs, requirementID)
		}
		seen[key] = true
		out.Artifacts = append(out.Artifacts, schedulerapp.RunArtifact{
			ID: ref.ID, Kind: string(ref.Kind), URI: ref.URI, FileName: fileName,
			SizeBytes: sizeBytes, SHA256: digest, EvidenceID: evidenceByArtifact[key],
		})
	}
	if result.Blocker == nil && len(contractIDs) != 0 {
		return schedulerapp.RunResult{}, fmt.Errorf("committed Agent result does not cover every scheduled artifact")
	}
	out.Summary = publicScheduledAgentSummary(result.Summary.Text, len(out.Artifacts))
	if result.UserReport != "" {
		out.AgentReport = &schedulerapp.AgentReport{AgentID: result.AgentID.String(), RuntimeRunID: result.RunID.String(), Body: result.UserReport}
		out.Summary = result.UserReport
	}
	return out, nil
}

func (w productionAgentRunWorker) completeCommittedScheduledResult(ctx context.Context, run runtimedaemon.AgentRun, result agentcore.AgentResult, response agentcore.RuntimeResponse, workerID string) error {
	runResult, err := scheduledRunResultFromCommittedAgentResult(run, result)
	if err != nil {
		if lifecycleErr := w.publishFailure(ctx, run, workerID, "agent_result_contract_failed", err.Error()); runtimeLifecycleTerminalAlreadyWon(lifecycleErr) {
			return nil
		}
		return schedulerAgentRetryError(run, err)
	}
	runResult = scheduledAgentDeliveryRunResult(run, runResult, response)
	if w.scheduler == nil {
		return nil
	}
	if result.Blocker != nil {
		return w.failCommittedScheduledResult(ctx, run, result, runResult, response, workerID)
	}

	message := strings.TrimSpace(result.Summary.Text)
	if message == "" {
		message = "agent run completed"
	}
	completedEvent := productionRuntimeLifecycleEvent(run)
	completedEvent.Kind = runtimedaemon.RuntimeLifecycleCompleted
	completedEvent.Status = runtimedaemon.RunStatusCompleted.String()
	completedEvent.WorkerID = workerID
	completedEvent.Message = message
	completedEvent.Usage = cloneProductionRuntimeUsage(response.Usage)
	completedEvent.TraceRef = strings.TrimSpace(result.InternalTraceRef)
	completedEvent.Artifacts = runtimeLifecycleArtifacts(result, agentcore.RuntimeResponse{})
	completedEvent.Evidence = runtimeLifecycleEvidence(result)
	completedEvent.KnowledgeRefs = runtimeLifecycleKnowledgeRefs(response)
	completedEvent.DurationMs = response.DurationMs
	completedEvent.CompletedAt = response.CompletedAt
	if completedEvent.CompletedAt.IsZero() {
		completedEvent.CompletedAt = result.CreatedAt
	}
	if completedEvent.CompletedAt.IsZero() {
		completedEvent.CompletedAt = time.Now().UTC()
	}
	if w.lifecycle != nil {
		if err := w.lifecycle.PublishRuntimeLifecycle(ctx, completedEvent); err != nil {
			if runtimeLifecycleTerminalAlreadyWon(err) {
				return nil
			}
			if w.logger != nil {
				w.logger.Error(ctx, "publish completed runtime lifecycle", logging.String("run_id", run.RunID.String()), logging.String("task_id", run.TaskID), logging.Any("error", err))
			}
		}
	}

	expectedRun, err := scheduledRunIdentityFromAgentRun(run)
	if err != nil {
		return schedulerAgentRetryError(run, err)
	}
	scheduledRunID := expectedRun.ScheduledRunID
	deliveryRequested := strings.EqualFold(strings.TrimSpace(run.Request.RuntimeOptions.ProviderSpecific["delivery_mode"]), "announce")
	if err := w.scheduler.CompleteAgentRun(ctx, expectedRun, runResult, result.InternalTraceRef, completedEvent.CompletedAt); err != nil {
		return schedulerAgentRetryError(run, err)
	}
	defer w.recordCommittedScheduledResponsibility(ctx, run, result, response)
	tracker, tracksDelivery := w.scheduler.(schedulerRunDeliveryTracker)
	if !tracksDelivery {
		return schedulerAgentRetryError(run, errors.New("scheduled result delivery tracking is not configured"))
	}
	if deliveryRequested {
		if w.scheduledDeliveryOutbox == nil {
			return schedulerAgentRetryError(run, errors.New("scheduled result delivery outbox is not configured"))
		}
		if err := w.scheduledDeliveryOutbox.Schedule(ctx, run, expectedRun); errors.Is(err, errScheduledAgentDeliveryDeadLetter) {
			if recordErr := tracker.RecordDelivery(ctx, scheduledRunID, "dead_letter", scheduledDeliveryDeadLetterPublicError(), ""); recordErr != nil {
				return schedulerAgentRetryError(run, recordErr)
			}
			return nil
		} else if err != nil {
			return schedulerAgentRetryError(run, err)
		}
	}
	if !deliveryRequested {
		if err := tracker.RecordDelivery(ctx, scheduledRunID, "not_requested", "", ""); err != nil {
			return schedulerAgentRetryError(run, err)
		}
	}
	return nil
}

func (w productionAgentRunWorker) failCommittedScheduledResult(
	ctx context.Context,
	run runtimedaemon.AgentRun,
	result agentcore.AgentResult,
	runResult schedulerapp.RunResult,
	response agentcore.RuntimeResponse,
	workerID string,
) error {
	reason := strings.TrimSpace(result.Blocker.Summary)
	if reason == "" {
		reason = "Agent reported that the scheduled acceptance criteria are blocked."
	}
	cause := errors.New(reason)
	completedAt := response.CompletedAt
	if completedAt.IsZero() {
		completedAt = result.CreatedAt
	}
	if completedAt.IsZero() {
		completedAt = time.Now().UTC()
	}
	if w.lifecycle != nil {
		if lifecycleErr := w.publishFailure(ctx, run, workerID, "agent_runtime_blocked", reason); runtimeLifecycleTerminalAlreadyWon(lifecycleErr) {
			return nil
		}
	}
	expectedRun, err := scheduledRunIdentityFromAgentRun(run)
	if err != nil {
		return schedulerAgentRetryError(run, err)
	}
	tracker, ok := w.scheduler.(schedulerBlockedRunLifecycleTracker)
	if !ok {
		return schedulerAgentRetryError(run, errors.New("scheduled blocked-result tracking is not configured"))
	}
	if err := tracker.FailAgentRun(ctx, expectedRun, runResult, result.InternalTraceRef, completedAt, cause); err != nil {
		return schedulerAgentRetryError(run, err)
	}
	defer w.recordCommittedScheduledResponsibility(ctx, run, result, response)

	deliveryRequested := strings.EqualFold(strings.TrimSpace(run.Request.RuntimeOptions.ProviderSpecific["delivery_mode"]), "announce")
	deliveryTracker, tracksDelivery := w.scheduler.(schedulerRunDeliveryTracker)
	if !tracksDelivery {
		return schedulerAgentRetryError(run, errors.New("scheduled result delivery tracking is not configured"))
	}
	if deliveryRequested {
		if w.scheduledDeliveryOutbox == nil {
			return schedulerAgentRetryError(run, errors.New("scheduled result delivery outbox is not configured"))
		}
		if err := w.scheduledDeliveryOutbox.Schedule(ctx, run, expectedRun); errors.Is(err, errScheduledAgentDeliveryDeadLetter) {
			if recordErr := deliveryTracker.RecordDelivery(ctx, expectedRun.ScheduledRunID, "dead_letter", scheduledDeliveryDeadLetterPublicError(), ""); recordErr != nil {
				return schedulerAgentRetryError(run, recordErr)
			}
		} else if err != nil {
			return schedulerAgentRetryError(run, err)
		}
	} else if err := deliveryTracker.RecordDelivery(ctx, expectedRun.ScheduledRunID, "not_requested", "", ""); err != nil {
		return schedulerAgentRetryError(run, err)
	}
	return nil
}

func (w productionAgentRunWorker) recordCommittedScheduledResponsibility(ctx context.Context, run runtimedaemon.AgentRun, result agentcore.AgentResult, response agentcore.RuntimeResponse) {
	if w.responsibility == nil {
		return
	}
	var responsibilityErr error
	if result.Blocker != nil {
		cause := errors.New(firstNonBlank(result.Blocker.Summary, "Agent reported that the scheduled acceptance criteria are blocked."))
		if tracker, ok := w.responsibility.(responsibilityRunUsageLifecycleTracker); ok {
			responsibilityErr = tracker.FailedWithTraceAndUsage(ctx, run.Request.TenantID, run.TaskID, run.RunID.String(), runtimedaemon.RuntimeLifecycleBlocked, cause, result.InternalTraceRef, totalRuntimeUsageTokens(response.Usage))
		} else if tracker, ok := w.responsibility.(responsibilityRunTraceLifecycleTracker); ok {
			responsibilityErr = tracker.FailedWithTrace(ctx, run.Request.TenantID, run.TaskID, run.RunID.String(), runtimedaemon.RuntimeLifecycleBlocked, cause, result.InternalTraceRef)
		} else {
			responsibilityErr = w.responsibility.Failed(ctx, run.Request.TenantID, run.TaskID, run.RunID.String(), runtimedaemon.RuntimeLifecycleBlocked, cause)
		}
	} else if tracker, ok := w.responsibility.(responsibilityRunUsageLifecycleTracker); ok {
		responsibilityErr = tracker.CompletedWithTraceAndUsage(ctx, run.Request.TenantID, run.TaskID, run.RunID.String(), result.InternalTraceRef, totalRuntimeUsageTokens(response.Usage))
	} else if tracker, ok := w.responsibility.(responsibilityRunTraceLifecycleTracker); ok {
		responsibilityErr = tracker.CompletedWithTrace(ctx, run.Request.TenantID, run.TaskID, run.RunID.String(), result.InternalTraceRef)
	} else {
		responsibilityErr = w.responsibility.Completed(ctx, run.Request.TenantID, run.TaskID, run.RunID.String())
	}
	if responsibilityErr != nil && w.logger != nil {
		w.logger.Error(ctx, "record committed responsibility attempt", logging.String("run_id", run.RunID.String()), logging.String("task_id", run.TaskID), logging.Any("error", responsibilityErr))
	}
}

func scheduledDeliveryDeadLetterPublicError() string {
	return "scheduled delivery exhausted its retry budget without a confirmed channel receipt"
}

func scheduledRunIdentityFromAgentRun(run runtimedaemon.AgentRun) (schedulerapp.ScheduledRun, error) {
	metadata := run.Request.RuntimeOptions.ProviderSpecific
	expected := schedulerapp.ScheduledRun{
		ScheduledRunID:  strings.TrimSpace(metadata["scheduled_run_id"]),
		ScheduledTaskID: strings.TrimSpace(metadata["scheduled_task_id"]),
		TargetMode:      schedulerapp.TargetModeAgentTask,
		TenantID:        strings.TrimSpace(run.Request.TenantID),
		AgentID:         run.AgentID.String(),
		RuntimeRunID:    strings.TrimSpace(run.RunID.String()),
		MissionID:       strings.TrimSpace(run.MissionID),
		TaskID:          strings.TrimSpace(run.TaskID),
	}
	if responsibility := run.Request.ContextPack.ResponsibilityContext; responsibility != nil {
		expected.ResponsibilityID = strings.TrimSpace(responsibility.ResponsibilityID)
		if strings.TrimSpace(responsibility.MissionID) != "" && strings.TrimSpace(responsibility.MissionID) != expected.MissionID ||
			strings.TrimSpace(responsibility.TaskID) != "" && strings.TrimSpace(responsibility.TaskID) != expected.TaskID ||
			strings.TrimSpace(responsibility.AgentProfileID) != "" && strings.TrimSpace(responsibility.AgentProfileID) != expected.AgentID {
			return schedulerapp.ScheduledRun{}, fmt.Errorf("scheduled runtime responsibility identity is inconsistent")
		}
	}
	if expected.ScheduledRunID == "" || expected.ScheduledTaskID == "" || expected.AgentID == "" || expected.RuntimeRunID == "" || expected.MissionID == "" || expected.TaskID == "" {
		return schedulerapp.ScheduledRun{}, fmt.Errorf("scheduled runtime identity is incomplete")
	}
	return expected, nil
}

func decodeSchedulerResultContract(raw string) (schedulerapp.AgentResultContract, error) {
	var contract schedulerapp.AgentResultContract
	if err := json.Unmarshal([]byte(raw), &contract); err != nil {
		return schedulerapp.AgentResultContract{}, fmt.Errorf("decode scheduler result contract: %w", err)
	}
	return contract, nil
}
