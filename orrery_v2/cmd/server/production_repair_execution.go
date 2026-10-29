package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/actioncontract"
	"orrery_v2/internal/app/missioncontrol"
	"orrery_v2/internal/app/reliability"
	missioncore "orrery_v2/internal/mission/core"
	"orrery_v2/internal/platform/execution"
)

const (
	productionRepairExecutionDeadline = 30 * time.Minute
	productionRepairDispatchGrace     = time.Minute
)

type repairVerificationEvidenceRecord struct {
	EvidenceID    string
	IncidentID    string
	RuleID        string
	OriginalRunID string
	RetryRunID    string
	Status        string
	QueueStatus   string
	FailureCode   string
	ObservedAt    time.Time
}

type repairVerificationEvidenceRecorder interface {
	RecordRepairVerificationEvidence(context.Context, actioncontract.ActionRun, repairVerificationEvidenceRecord) error
}

type missionAgentRetryCommand interface {
	RetryAgentRun(context.Context, missioncontrol.ViewerRef, missioncontrol.RetryAgentRunCommand) (missioncontrol.CommandReceiptDTO, error)
}

type repairRuntimeQueue interface {
	GetStatus(context.Context, string) (execution.JobStatus, bool, error)
	Cancel(context.Context, string, string) error
}

type productionRepairExecutor struct {
	dryRun *productionRepairDryRunExecutor
	apply  *productionRepairApplyExecutor
}

func (e *productionRepairExecutor) Validate(ctx context.Context, invocation repairInvocation) error {
	if invocation.ExecutionMode == actioncontract.ExecutionModeDryRun {
		if e == nil || e.dryRun == nil {
			return fmt.Errorf("repair dry-run executor is unavailable")
		}
		return e.dryRun.Validate(ctx, invocation)
	}
	if invocation.ExecutionMode != actioncontract.ExecutionModeApply || e == nil || e.apply == nil {
		return fmt.Errorf("%w: repair execution mode is unavailable", reliability.ErrInvalidRequest)
	}
	_, err := e.apply.loadMaterial(ctx, invocation)
	return err
}

func (e *productionRepairExecutor) Execute(ctx context.Context, run actioncontract.ActionRun) (actioncontract.ActionRun, error) {
	if run.ExecutionMode == actioncontract.ExecutionModeDryRun {
		return e.dryRun.Execute(ctx, run)
	}
	if run.ExecutionMode != actioncontract.ExecutionModeApply || e == nil || e.apply == nil {
		return actioncontract.ActionRun{}, fmt.Errorf("repair apply executor is unavailable")
	}
	return e.apply.Execute(ctx, run)
}

type productionRepairApplyExecutor struct {
	repository reliability.Repository
	control    *reliability.Service
	actions    *actioncontract.Service
	commands   missionAgentRetryCommand
	evidence   repairVerificationEvidenceRecorder
	now        func() time.Time
}

type repairApplyMaterial struct {
	incident  reliability.FailureIncident
	rule      reliability.RepairRule
	target    reliability.RepairTarget
	preflight reliability.RepairPreflight
}

func (e *productionRepairApplyExecutor) loadMaterial(ctx context.Context, invocation repairInvocation) (repairApplyMaterial, error) {
	if e == nil || e.repository == nil || e.actions == nil || strings.TrimSpace(invocation.TenantID) == "" || strings.TrimSpace(invocation.ArgsHash) == "" {
		return repairApplyMaterial{}, fmt.Errorf("%w: repair apply dependencies and binding are required", reliability.ErrInvalidRequest)
	}
	incidentID, ruleID, err := repairActionTargets(invocation.ObjectRefs)
	if err != nil {
		return repairApplyMaterial{}, fmt.Errorf("%w: %v", reliability.ErrInvalidRequest, err)
	}
	incident, err := e.repository.GetIncident(ctx, invocation.TenantID, incidentID)
	if err != nil {
		return repairApplyMaterial{}, err
	}
	rule, err := e.repository.GetRepairRule(ctx, invocation.TenantID, ruleID)
	if err != nil {
		return repairApplyMaterial{}, err
	}
	if rule.Strategy != reliability.RepairStrategyRetry || rule.Status != reliability.RepairRuleStatusActive ||
		rule.ActionTypeID != string(productionRepairActionTypeID) || rule.SelectorFingerprint != incident.Fingerprint ||
		!rule.Reversible || !rule.Idempotent || !incident.Retryable {
		return repairApplyMaterial{}, fmt.Errorf("%w: only an active reversible idempotent retry rule may be applied", reliability.ErrInvalidRequest)
	}
	if rule.ExpiresAt != nil && !rule.ExpiresAt.After(e.nowUTC()) {
		return repairApplyMaterial{}, fmt.Errorf("%w: repair rule has expired", reliability.ErrInvalidRequest)
	}
	switch incident.Status {
	case reliability.IncidentStatusOpen, reliability.IncidentStatusContained, reliability.IncidentStatusDiagnosed,
		reliability.IncidentStatusEscalated, reliability.IncidentStatusQuarantined:
	default:
		return repairApplyMaterial{}, fmt.Errorf("%w: failure incident is not eligible for retry apply", reliability.ErrInvalidRequest)
	}
	target, err := e.repository.GetLatestRepairTarget(ctx, invocation.TenantID, incidentID)
	if err != nil {
		return repairApplyMaterial{}, err
	}
	if target.Kind != reliability.RepairTargetMissionAgentRun || !target.OccurredAt.Equal(incident.LastSeenAt) {
		return repairApplyMaterial{}, fmt.Errorf("%w: latest failure does not have an exact mission agent run target", reliability.ErrInvalidRequest)
	}
	if invocation.ArgsHash != repairInvocationArgsHash(incident, rule, &target) {
		return repairApplyMaterial{}, fmt.Errorf("%w: repair invocation binding is stale", reliability.ErrInvalidRequest)
	}
	preflight, err := e.boundPreflight(ctx, invocation, incident, rule, target)
	if err != nil {
		return repairApplyMaterial{}, err
	}
	return repairApplyMaterial{incident: incident, rule: rule, target: target, preflight: preflight}, nil
}

func (e *productionRepairApplyExecutor) boundPreflight(ctx context.Context, invocation repairInvocation, incident reliability.FailureIncident, rule reliability.RepairRule, target reliability.RepairTarget) (reliability.RepairPreflight, error) {
	var matched *reliability.RepairPreflight
	for _, ref := range invocation.EvidenceRefs {
		if ref.Kind != "evidence_item" || strings.TrimSpace(ref.ID) == "" {
			continue
		}
		candidate, err := e.repository.GetRepairPreflight(ctx, invocation.TenantID, ref.ID)
		if errors.Is(err, reliability.ErrNotFound) {
			continue
		}
		if err != nil {
			return reliability.RepairPreflight{}, err
		}
		if matched != nil {
			return reliability.RepairPreflight{}, fmt.Errorf("%w: apply must reference exactly one repair preflight", reliability.ErrInvalidRequest)
		}
		candidateCopy := candidate
		matched = &candidateCopy
	}
	if matched == nil || !matched.Passed || !matched.ExpiresAt.After(e.nowUTC()) || matched.IncidentID != incident.ID ||
		matched.IncidentVersion != incident.Version || matched.RuleID != rule.ID || matched.RuleVersion != rule.Version ||
		matched.Strategy != rule.Strategy || matched.ArgsHash != invocation.ArgsHash || matched.TargetObservationID != target.ObservationID ||
		matched.TargetKind != target.Kind || matched.TargetRunID != target.RunID {
		return reliability.RepairPreflight{}, fmt.Errorf("%w: a current exact repair preflight is required", reliability.ErrInvalidRequest)
	}
	source, err := e.actions.GetActionRun(ctx, actioncontract.ActionRunID(matched.ActionRunID))
	if err != nil {
		return reliability.RepairPreflight{}, err
	}
	sourceIncidentID, sourceRuleID, targetErr := repairActionTargets(source.ObjectRefs)
	if targetErr != nil || source.TenantID != invocation.TenantID || source.ExecutionMode != actioncontract.ExecutionModeDryRun ||
		source.Status != actioncontract.RunStatusCompleted || source.ArgsHash != invocation.ArgsHash || sourceIncidentID != incident.ID || sourceRuleID != rule.ID ||
		!actionRunHasEvidence(source, matched.EvidenceID) {
		return reliability.RepairPreflight{}, fmt.Errorf("%w: repair preflight source action is not a completed exact dry-run", reliability.ErrInvalidRequest)
	}
	return *matched, nil
}

func (e *productionRepairApplyExecutor) Execute(ctx context.Context, run actioncontract.ActionRun) (actioncontract.ActionRun, error) {
	if e == nil || e.repository == nil || e.control == nil || e.actions == nil || e.commands == nil || e.evidence == nil || e.now == nil ||
		run.ActionTypeID != productionRepairActionTypeID || run.ExecutionMode != actioncontract.ExecutionModeApply || run.Status != actioncontract.RunStatusStarted {
		return actioncontract.ActionRun{}, fmt.Errorf("repair apply dependencies or action state are invalid")
	}
	material, err := e.loadMaterial(ctx, repairInvocation{
		TenantID: run.TenantID, ExecutionMode: run.ExecutionMode, ArgsHash: run.ArgsHash,
		ObjectRefs: run.ObjectRefs, EvidenceRefs: run.EvidenceRefs,
	})
	if err != nil {
		return e.failBeforeDispatch(ctx, run, "repair_apply_precondition_failed", err)
	}
	now := e.nowUTC()
	retryRunID := deterministicRepairRetryRunID(string(run.ID))
	executionRecord, _, err := e.repository.BeginRepairExecution(ctx, reliability.RepairExecution{
		TenantID: run.TenantID, ActionRunID: string(run.ID), IncidentID: material.incident.ID, RuleID: material.rule.ID,
		PreflightEvidenceID: material.preflight.EvidenceID, Strategy: material.rule.Strategy, Target: material.target,
		RetryRunID: retryRunID, Status: reliability.RepairExecutionDispatching, Version: 1,
		StartedAt: now, DeadlineAt: now.Add(productionRepairExecutionDeadline), UpdatedAt: now,
	})
	if err != nil {
		return e.failBeforeDispatch(ctx, run, "repair_execution_conflict", err)
	}
	if err := e.ensureIncidentRepairing(ctx, run.TenantID, material.incident.ID, run.Actor); err != nil {
		return e.failExecutionBeforeDispatch(ctx, run, executionRecord, "repair_incident_transition_failed", err)
	}
	receipt, err := e.commands.RetryAgentRun(ctx, repairMissionViewer(run, material.target.AuthorizationTenantID), missioncontrol.RetryAgentRunCommand{
		RequestID: string(run.ID), IdempotencyKey: "reliability.repair.retry:" + string(run.ID),
		MissionID: missioncore.MissionID(material.target.MissionID), RunID: agentcore.AgentRunID(material.target.RunID),
		ReplacementRunID: agentcore.AgentRunID(retryRunID),
	})
	if err != nil || strings.TrimSpace(receipt.CommandID) != retryRunID {
		if err == nil {
			err = fmt.Errorf("retry command returned unexpected replacement run id")
		}
		return e.failExecutionBeforeDispatch(ctx, run, executionRecord, "repair_retry_dispatch_failed", err)
	}
	updated, err := e.repository.UpdateRepairExecution(ctx, reliability.RepairExecutionUpdate{
		TenantID: run.TenantID, ActionRunID: string(run.ID), ExpectedVersion: executionRecord.Version,
		Status: reliability.RepairExecutionMonitoring, RetryRunID: retryRunID, UpdatedAt: e.nowUTC(),
	})
	if err == nil {
		_ = e.ensureIncidentMonitoring(ctx, updated.TenantID, updated.IncidentID, run.Actor)
	}
	// A persisted dispatching row already names the deterministic retry run, so
	// the reconciler can recover even if recording monitoring failed here.
	return run, nil
}

func (e *productionRepairApplyExecutor) failBeforeDispatch(ctx context.Context, run actioncontract.ActionRun, code string, cause error) (actioncontract.ActionRun, error) {
	failed, err := e.actions.FailActionRun(ctx, actioncontract.FailRunRequest{
		RunID: run.ID, ErrorCode: code, ErrorMessage: "Governed retry did not start",
	})
	if err != nil {
		return actioncontract.ActionRun{}, errors.Join(cause, err)
	}
	return failed, nil
}

func (e *productionRepairApplyExecutor) failExecutionBeforeDispatch(ctx context.Context, run actioncontract.ActionRun, executionRecord reliability.RepairExecution, code string, cause error) (actioncontract.ActionRun, error) {
	completedAt := e.nowUTC()
	_, updateErr := e.repository.UpdateRepairExecution(ctx, reliability.RepairExecutionUpdate{
		TenantID: executionRecord.TenantID, ActionRunID: executionRecord.ActionRunID, ExpectedVersion: executionRecord.Version,
		Status: reliability.RepairExecutionFailed, RetryRunID: executionRecord.RetryRunID, FailureCode: code,
		UpdatedAt: completedAt, CompletedAt: &completedAt,
	})
	failed, failErr := e.actions.FailActionRun(ctx, actioncontract.FailRunRequest{
		RunID: run.ID, ProviderCallRef: actioncontract.Ref{Kind: "mission_agent_retry", ID: executionRecord.RetryRunID},
		ErrorCode: code, ErrorMessage: "Governed retry could not be dispatched",
	})
	_ = e.ensureIncidentQuarantined(ctx, executionRecord.TenantID, executionRecord.IncidentID, run.Actor)
	if failErr != nil {
		return actioncontract.ActionRun{}, errors.Join(cause, updateErr, failErr)
	}
	return failed, nil
}

func (e *productionRepairApplyExecutor) nowUTC() time.Time {
	if e == nil || e.now == nil {
		return time.Now().UTC()
	}
	return e.now().UTC()
}

type productionRepairExecutionReconciler struct {
	repository reliability.Repository
	control    *reliability.Service
	actions    *actioncontract.Service
	queue      repairRuntimeQueue
	evidence   repairVerificationEvidenceRecorder
	now        func() time.Time
}

func (r *productionRepairExecutionReconciler) Sweep(ctx context.Context, limit int) (int, error) {
	if r == nil || r.repository == nil || r.control == nil || r.actions == nil || r.queue == nil || r.evidence == nil {
		return 0, fmt.Errorf("repair execution reconciler dependencies are unavailable")
	}
	items, err := r.repository.ListActiveRepairExecutions(ctx, limit)
	if err != nil {
		return 0, err
	}
	processed := 0
	var sweepErrors []error
	for _, item := range items {
		if err := r.reconcile(ctx, item); err != nil {
			sweepErrors = append(sweepErrors, fmt.Errorf("repair execution %s: %w", item.ActionRunID, err))
			continue
		}
		processed++
	}
	return processed, errors.Join(sweepErrors...)
}

func (r *productionRepairExecutionReconciler) reconcile(ctx context.Context, item reliability.RepairExecution) error {
	run, err := r.actions.GetActionRun(ctx, actioncontract.ActionRunID(item.ActionRunID))
	if err != nil {
		return err
	}
	if run.Status == actioncontract.RunStatusCompleted {
		return r.closeExecution(ctx, item, reliability.RepairExecutionSucceeded, "", run.Actor)
	}
	if run.Status == actioncontract.RunStatusFailed {
		return r.closeExecution(ctx, item, reliability.RepairExecutionFailed, firstNonBlank(run.ErrorCode, "repair_retry_failed"), run.Actor)
	}
	status, found, err := r.queue.GetStatus(ctx, item.RetryRunID)
	if err != nil {
		return err
	}
	now := r.nowUTC()
	if !found {
		if now.Sub(item.UpdatedAt) < productionRepairDispatchGrace {
			return nil
		}
		return r.failRun(ctx, item, run, "repair_retry_dispatch_unknown", "missing")
	}
	if item.Status == reliability.RepairExecutionDispatching {
		updated, err := r.repository.UpdateRepairExecution(ctx, reliability.RepairExecutionUpdate{
			TenantID: item.TenantID, ActionRunID: item.ActionRunID, ExpectedVersion: item.Version,
			Status: reliability.RepairExecutionMonitoring, RetryRunID: item.RetryRunID, UpdatedAt: now,
		})
		if err != nil {
			return err
		}
		item = updated
		_ = r.ensureIncidentMonitoring(ctx, item.TenantID, item.IncidentID, run.Actor)
	}
	switch status.Status {
	case execution.StatusCompleted:
		return r.completeRun(ctx, item, run, string(status.Status))
	case execution.StatusFailed, execution.StatusCancelled, execution.StatusDeadLetter:
		return r.failRun(ctx, item, run, "repair_retry_terminal_"+string(status.Status), string(status.Status))
	case execution.StatusQueued, execution.StatusDispatched, execution.StatusRunning:
		if !now.Before(item.DeadlineAt) {
			if err := r.queue.Cancel(ctx, item.RetryRunID, "governed repair verification deadline exceeded"); err != nil {
				return err
			}
			return r.failRun(ctx, item, run, "repair_retry_terminal_timeout", "deadline_exceeded")
		}
		return nil
	default:
		return fmt.Errorf("unsupported retry queue status %s", status.Status)
	}
}

func (r *productionRepairExecutionReconciler) completeRun(ctx context.Context, item reliability.RepairExecution, run actioncontract.ActionRun, queueStatus string) error {
	now := r.nowUTC()
	evidenceID := deterministicRepairVerificationEvidenceID(item.ActionRunID, item.RetryRunID, "succeeded")
	if err := r.evidence.RecordRepairVerificationEvidence(ctx, run, repairVerificationEvidenceRecord{
		EvidenceID: evidenceID, IncidentID: item.IncidentID, RuleID: item.RuleID, OriginalRunID: item.Target.RunID,
		RetryRunID: item.RetryRunID, Status: "passed", QueueStatus: queueStatus, ObservedAt: now,
	}); err != nil {
		return err
	}
	completed, _, err := r.actions.CompleteActionRun(ctx, actioncontract.CompleteRunRequest{
		RunID: run.ID, ProviderCallRef: actioncontract.Ref{Kind: "mission_agent_retry", ID: item.RetryRunID},
		ResultRef:    actioncontract.Ref{Kind: "agent_run", ID: ontologyAgentRunID(item.RetryRunID)},
		EvidenceRefs: []actioncontract.Ref{{Kind: "evidence_item", ID: evidenceID}},
		Writeback: &actioncontract.WritebackReceipt{
			ID: actioncontract.WritebackReceiptID("repair-writeback:" + item.ActionRunID), TargetSystem: "agent_runtime",
			TargetResourceRefs: []actioncontract.ObjectRef{{Kind: "agent_run", ID: ontologyAgentRunID(item.RetryRunID)}},
			BeforeHash:         repairRunHash(item.Target.RunID), AfterHash: repairRunHash(item.RetryRunID), ChangedFields: []string{"retry_run_id"},
			ExternalOperationID: item.RetryRunID, IdempotencyKey: item.ActionRunID, Status: actioncontract.WritebackStatusSucceeded,
			EvidenceRefs: []actioncontract.Ref{{Kind: "evidence_item", ID: evidenceID}}, CreatedAt: now,
		},
	})
	if err != nil {
		current, getErr := r.actions.GetActionRun(ctx, run.ID)
		if getErr != nil || current.Status != actioncontract.RunStatusCompleted {
			return err
		}
		completed = current
	}
	return r.closeExecution(ctx, item, reliability.RepairExecutionSucceeded, "", completed.Actor)
}

func (r *productionRepairExecutionReconciler) failRun(ctx context.Context, item reliability.RepairExecution, run actioncontract.ActionRun, code, queueStatus string) error {
	now := r.nowUTC()
	evidenceID := deterministicRepairVerificationEvidenceID(item.ActionRunID, item.RetryRunID, code)
	if err := r.evidence.RecordRepairVerificationEvidence(ctx, run, repairVerificationEvidenceRecord{
		EvidenceID: evidenceID, IncidentID: item.IncidentID, RuleID: item.RuleID, OriginalRunID: item.Target.RunID,
		RetryRunID: item.RetryRunID, Status: "failed", QueueStatus: queueStatus, FailureCode: code, ObservedAt: now,
	}); err != nil {
		return err
	}
	failed, err := r.actions.FailActionRun(ctx, actioncontract.FailRunRequest{
		RunID: run.ID, ProviderCallRef: actioncontract.Ref{Kind: "mission_agent_retry", ID: item.RetryRunID}, ErrorCode: code,
		ErrorMessage: "Retried agent run did not pass terminal health verification",
		EvidenceRefs: []actioncontract.Ref{{Kind: "evidence_item", ID: evidenceID}},
	})
	if err != nil {
		current, getErr := r.actions.GetActionRun(ctx, run.ID)
		if getErr != nil || current.Status != actioncontract.RunStatusFailed {
			return err
		}
		failed = current
	}
	return r.closeExecution(ctx, item, reliability.RepairExecutionFailed, code, failed.Actor)
}

func (r *productionRepairExecutionReconciler) closeExecution(ctx context.Context, item reliability.RepairExecution, status reliability.RepairExecutionStatus, failureCode string, actor actioncontract.ActionSubject) error {
	if item.Status == status {
		return nil
	}
	now := r.nowUTC()
	_, err := r.repository.UpdateRepairExecution(ctx, reliability.RepairExecutionUpdate{
		TenantID: item.TenantID, ActionRunID: item.ActionRunID, ExpectedVersion: item.Version,
		Status: status, RetryRunID: item.RetryRunID, FailureCode: failureCode, UpdatedAt: now, CompletedAt: &now,
	})
	if err != nil {
		current, getErr := r.repository.GetRepairExecution(ctx, item.TenantID, item.ActionRunID)
		if getErr != nil || current.Status != status {
			return err
		}
	}
	if status == reliability.RepairExecutionSucceeded {
		return r.ensureIncidentResolved(ctx, item.TenantID, item.IncidentID, actor)
	}
	return r.ensureIncidentQuarantined(ctx, item.TenantID, item.IncidentID, actor)
}

func (r *productionRepairExecutionReconciler) nowUTC() time.Time {
	if r == nil || r.now == nil {
		return time.Now().UTC()
	}
	return r.now().UTC()
}

func repairMissionViewer(run actioncontract.ActionRun, tenantID string) missioncontrol.ViewerRef {
	kind := missioncontrol.ViewerKindUser
	switch run.Actor.Kind {
	case actioncontract.SubjectAgent:
		kind = missioncontrol.ViewerKindAgent
	case actioncontract.SubjectService, actioncontract.SubjectWorker:
		kind = missioncontrol.ViewerKindSystem
	}
	return missioncontrol.ViewerRef{
		ViewerID: run.Actor.ID, ViewerKind: kind, TenantID: tenantID, AuthorizationSubjectID: run.Actor.ID,
		PermissionScope: []string{"mission.write"},
	}
}

func deterministicRepairRetryRunID(actionRunID string) string {
	sum := sha256.Sum256([]byte("repair-retry\x00" + strings.TrimSpace(actionRunID)))
	return "repair-retry:" + hex.EncodeToString(sum[:12])
}

func deterministicRepairVerificationEvidenceID(actionRunID, retryRunID, result string) string {
	sum := sha256.Sum256([]byte(strings.Join([]string{"repair-verification", actionRunID, retryRunID, result}, "\x00")))
	return "repair-verification-evidence:" + hex.EncodeToString(sum[:12])
}

func repairRunHash(runID string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(runID)))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func actionRunHasEvidence(run actioncontract.ActionRun, evidenceID string) bool {
	for _, ref := range run.EvidenceRefs {
		if ref.Kind == "evidence_item" && ref.ID == evidenceID {
			return true
		}
	}
	return false
}

func reliabilityActor(actor actioncontract.ActionSubject) reliability.ActorRef {
	return reliability.ActorRef{Kind: string(actor.Kind), ID: actor.ID}
}

func (e *productionRepairApplyExecutor) ensureIncidentRepairing(ctx context.Context, tenantID, incidentID string, actor actioncontract.ActionSubject) error {
	return ensureRepairIncidentStatus(ctx, e.repository, e.control, tenantID, incidentID, reliability.IncidentStatusRepairing, actor)
}

func (e *productionRepairApplyExecutor) ensureIncidentMonitoring(ctx context.Context, tenantID, incidentID string, actor actioncontract.ActionSubject) error {
	return ensureRepairIncidentStatus(ctx, e.repository, e.control, tenantID, incidentID, reliability.IncidentStatusMonitoring, actor)
}

func (e *productionRepairApplyExecutor) ensureIncidentQuarantined(ctx context.Context, tenantID, incidentID string, actor actioncontract.ActionSubject) error {
	return ensureRepairIncidentStatus(ctx, e.repository, e.control, tenantID, incidentID, reliability.IncidentStatusQuarantined, actor)
}

func (r *productionRepairExecutionReconciler) ensureIncidentMonitoring(ctx context.Context, tenantID, incidentID string, actor actioncontract.ActionSubject) error {
	return ensureRepairIncidentStatus(ctx, r.repository, r.control, tenantID, incidentID, reliability.IncidentStatusMonitoring, actor)
}

func (r *productionRepairExecutionReconciler) ensureIncidentResolved(ctx context.Context, tenantID, incidentID string, actor actioncontract.ActionSubject) error {
	return ensureRepairIncidentStatus(ctx, r.repository, r.control, tenantID, incidentID, reliability.IncidentStatusResolved, actor)
}

func (r *productionRepairExecutionReconciler) ensureIncidentQuarantined(ctx context.Context, tenantID, incidentID string, actor actioncontract.ActionSubject) error {
	return ensureRepairIncidentStatus(ctx, r.repository, r.control, tenantID, incidentID, reliability.IncidentStatusQuarantined, actor)
}

func ensureRepairIncidentStatus(ctx context.Context, repository reliability.Repository, control *reliability.Service, tenantID, incidentID string, wanted reliability.IncidentStatus, actor actioncontract.ActionSubject) error {
	if strings.TrimSpace(tenantID) == "" {
		return fmt.Errorf("repair incident tenant binding is required")
	}
	for attempts := 0; attempts < 6; attempts++ {
		incident, err := repository.GetIncident(ctx, tenantID, incidentID)
		if err != nil {
			return err
		}
		if incident.Status == wanted {
			_, err = control.TransitionIncident(ctx, tenantID, incidentID, wanted, reliabilityActor(actor))
			return err
		}
		next, ok := nextRepairIncidentStatus(incident.Status, wanted)
		if !ok {
			return fmt.Errorf("%w: incident transition %s -> %s is unavailable", reliability.ErrInvalidRequest, incident.Status, wanted)
		}
		if _, err := control.TransitionIncident(ctx, tenantID, incidentID, next, reliabilityActor(actor)); err != nil {
			return err
		}
	}
	return fmt.Errorf("%w: incident transition did not converge", reliability.ErrConflict)
}

func nextRepairIncidentStatus(current, wanted reliability.IncidentStatus) (reliability.IncidentStatus, bool) {
	if wanted == reliability.IncidentStatusRepairing {
		if current == reliability.IncidentStatusOpen {
			return reliability.IncidentStatusDiagnosed, true
		}
		return reliability.IncidentStatusRepairing, current == reliability.IncidentStatusContained || current == reliability.IncidentStatusDiagnosed ||
			current == reliability.IncidentStatusMonitoring || current == reliability.IncidentStatusEscalated || current == reliability.IncidentStatusQuarantined
	}
	if wanted == reliability.IncidentStatusMonitoring {
		if current == reliability.IncidentStatusOpen || current == reliability.IncidentStatusContained || current == reliability.IncidentStatusDiagnosed ||
			current == reliability.IncidentStatusEscalated || current == reliability.IncidentStatusQuarantined {
			return nextRepairIncidentStatus(current, reliability.IncidentStatusRepairing)
		}
		return reliability.IncidentStatusMonitoring, current == reliability.IncidentStatusRepairing
	}
	if wanted == reliability.IncidentStatusResolved {
		if current != reliability.IncidentStatusMonitoring {
			return nextRepairIncidentStatus(current, reliability.IncidentStatusMonitoring)
		}
		return reliability.IncidentStatusResolved, true
	}
	if wanted == reliability.IncidentStatusQuarantined {
		switch current {
		case reliability.IncidentStatusOpen, reliability.IncidentStatusContained, reliability.IncidentStatusDiagnosed,
			reliability.IncidentStatusRepairing, reliability.IncidentStatusMonitoring, reliability.IncidentStatusEscalated:
			return reliability.IncidentStatusQuarantined, true
		}
	}
	return "", false
}
