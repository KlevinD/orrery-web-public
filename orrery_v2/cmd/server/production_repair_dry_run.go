package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"orrery_v2/internal/app/actioncontract"
	"orrery_v2/internal/app/reliability"
)

const productionRepairActionTypeID actioncontract.ActionTypeID = "reliability.repair.execute"

const productionRepairPreflightTTL = 30 * time.Minute

type repairInvocation struct {
	TenantID      string
	ExecutionMode actioncontract.ExecutionMode
	ArgsHash      string
	ObjectRefs    []actioncontract.ObjectRef
	EvidenceRefs  []actioncontract.Ref
}

func repairInvocationArgsHash(incident reliability.FailureIncident, rule reliability.RepairRule, target *reliability.RepairTarget) string {
	parts := []string{incident.TenantID, incident.ID, strconv.FormatInt(incident.Version, 10), rule.ID, strconv.FormatInt(rule.Version, 10), string(rule.Strategy)}
	if target != nil {
		parts = append(parts, target.ObservationID, string(target.Kind), target.AuthorizationTenantID, target.MissionID, target.RunID, target.OccurredAt.UTC().Format(time.RFC3339Nano))
	}
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func currentRepairTarget(incident reliability.FailureIncident, target *reliability.RepairTarget) bool {
	return target != nil && target.Kind == reliability.RepairTargetMissionAgentRun && target.OccurredAt.Equal(incident.LastSeenAt)
}

func loadRepairInvocationTarget(ctx context.Context, repository reliability.Repository, incident reliability.FailureIncident, rule reliability.RepairRule) (*reliability.RepairTarget, error) {
	target, err := repository.GetLatestRepairTarget(ctx, incident.TenantID, incident.ID)
	if errors.Is(err, reliability.ErrNotFound) && rule.Strategy != reliability.RepairStrategyRetry {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if rule.Strategy == reliability.RepairStrategyRetry && !currentRepairTarget(incident, &target) {
		return nil, fmt.Errorf("%w: retry requires the exact latest mission agent run target", reliability.ErrInvalidRequest)
	}
	return &target, nil
}

type ontologyRepairExecutor interface {
	Validate(context.Context, repairInvocation) error
	Execute(context.Context, actioncontract.ActionRun) (actioncontract.ActionRun, error)
}

type repairDryRunEvidenceRecord struct {
	EvidenceID     string
	ProviderCallID string
	ResultID       string
	Checks         []string
	IncidentID     string
	RuleID         string
	Strategy       reliability.RepairStrategy
	Target         *reliability.RepairTarget
	ObservedAt     time.Time
}

type repairDryRunEvidenceRecorder interface {
	RecordRepairDryRunEvidence(context.Context, actioncontract.ActionRun, repairDryRunEvidenceRecord) error
}

type productionRepairDryRunExecutor struct {
	repository reliability.Repository
	providers  *reliability.RepairProviderRegistry
	actions    *actioncontract.Service
	evidence   repairDryRunEvidenceRecorder
	now        func() time.Time
}

func newProductionRepairDryRunExecutor(repository reliability.Repository, providers *reliability.RepairProviderRegistry, actions *actioncontract.Service, evidence repairDryRunEvidenceRecorder, now func() time.Time) *productionRepairDryRunExecutor {
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &productionRepairDryRunExecutor{repository: repository, providers: providers, actions: actions, evidence: evidence, now: now}
}

func (e *productionRepairDryRunExecutor) Validate(ctx context.Context, invocation repairInvocation) error {
	if e == nil || e.repository == nil || invocation.ExecutionMode != actioncontract.ExecutionModeDryRun || strings.TrimSpace(invocation.TenantID) == "" || strings.TrimSpace(invocation.ArgsHash) == "" {
		return fmt.Errorf("%w: repair dry-run invocation is invalid", reliability.ErrInvalidRequest)
	}
	incidentID, ruleID, err := repairActionTargets(invocation.ObjectRefs)
	if err != nil {
		return fmt.Errorf("%w: %v", reliability.ErrInvalidRequest, err)
	}
	incident, err := e.repository.GetIncident(ctx, invocation.TenantID, incidentID)
	if err != nil {
		return err
	}
	rule, err := e.repository.GetRepairRule(ctx, invocation.TenantID, ruleID)
	if err != nil {
		return err
	}
	if rule.SelectorFingerprint != incident.Fingerprint || rule.ActionTypeID != string(productionRepairActionTypeID) {
		return fmt.Errorf("%w: repair rule does not match the incident", reliability.ErrInvalidRequest)
	}
	target, err := loadRepairInvocationTarget(ctx, e.repository, incident, rule)
	if err != nil {
		return err
	}
	if invocation.ArgsHash != repairInvocationArgsHash(incident, rule, target) {
		return fmt.Errorf("%w: repair invocation binding is stale", reliability.ErrInvalidRequest)
	}
	return nil
}

func (e *productionRepairDryRunExecutor) Execute(ctx context.Context, run actioncontract.ActionRun) (actioncontract.ActionRun, error) {
	if e == nil || e.repository == nil || e.providers == nil || e.actions == nil || e.now == nil {
		return actioncontract.ActionRun{}, fmt.Errorf("repair dry-run dependencies are unavailable")
	}
	if run.ActionTypeID != productionRepairActionTypeID || run.ExecutionMode != actioncontract.ExecutionModeDryRun || run.Status != actioncontract.RunStatusStarted {
		return actioncontract.ActionRun{}, fmt.Errorf("repair dry-run requires a started dry_run action")
	}
	if e.evidence == nil {
		return e.fail(ctx, run, actioncontract.Ref{}, fmt.Errorf("repair dry-run evidence recorder is unavailable"))
	}
	incidentID, ruleID, err := repairActionTargets(run.ObjectRefs)
	if err != nil {
		return e.fail(ctx, run, actioncontract.Ref{}, err)
	}
	incident, err := e.repository.GetIncident(ctx, run.TenantID, incidentID)
	if err != nil {
		return e.fail(ctx, run, actioncontract.Ref{}, err)
	}
	rule, err := e.repository.GetRepairRule(ctx, run.TenantID, ruleID)
	if err != nil {
		return e.fail(ctx, run, actioncontract.Ref{}, err)
	}
	target, err := loadRepairInvocationTarget(ctx, e.repository, incident, rule)
	if err != nil {
		return e.fail(ctx, run, actioncontract.Ref{}, err)
	}
	if run.ArgsHash != repairInvocationArgsHash(incident, rule, target) {
		return e.fail(ctx, run, actioncontract.Ref{}, fmt.Errorf("%w: repair invocation binding is stale", reliability.ErrInvalidRequest))
	}
	providerRef := actioncontract.Ref{Kind: "repair_dry_run_provider", ID: "strategy:" + string(rule.Strategy)}
	observedAt := e.now().UTC()
	result, err := e.providers.DryRun(ctx, reliability.RepairDryRunRequest{
		ActionRunID: string(run.ID), TenantID: run.TenantID, ArgsHash: run.ArgsHash,
		Incident: incident, Rule: rule, Target: target, ObservedAt: observedAt,
	})
	if err != nil {
		return e.fail(ctx, run, providerRef, err)
	}
	if _, _, err := e.repository.RecordRepairPreflight(ctx, reliability.RepairPreflight{
		EvidenceID: result.EvidenceID, TenantID: run.TenantID, ActionRunID: string(run.ID),
		IncidentID: incident.ID, IncidentVersion: incident.Version, RuleID: rule.ID, RuleVersion: rule.Version,
		Strategy: rule.Strategy, ArgsHash: run.ArgsHash,
		Passed: true, CheckedAt: observedAt, ExpiresAt: observedAt.Add(productionRepairPreflightTTL),
		TargetObservationID: repairTargetObservationID(target), TargetKind: repairTargetKind(target), TargetRunID: repairTargetRunID(target),
	}); err != nil {
		return e.fail(ctx, run, providerRef, err)
	}
	if err := e.evidence.RecordRepairDryRunEvidence(ctx, run, repairDryRunEvidenceRecord{
		EvidenceID: result.EvidenceID, ProviderCallID: result.ProviderCallID, ResultID: result.ResultID,
		Checks: append([]string(nil), result.Checks...), IncidentID: incident.ID, RuleID: rule.ID,
		Strategy: rule.Strategy, Target: target, ObservedAt: observedAt,
	}); err != nil {
		return e.fail(ctx, run, providerRef, err)
	}
	completed, _, err := e.actions.CompleteActionRun(ctx, actioncontract.CompleteRunRequest{
		RunID:           run.ID,
		ProviderCallRef: actioncontract.Ref{Kind: "repair_dry_run_provider", ID: result.ProviderCallID},
		ResultRef:       actioncontract.Ref{Kind: "repair_dry_run_result", ID: result.ResultID},
		EvidenceRefs:    []actioncontract.Ref{{Kind: "evidence_item", ID: result.EvidenceID}},
	})
	if err != nil {
		return actioncontract.ActionRun{}, err
	}
	return completed, nil
}

func repairTargetObservationID(target *reliability.RepairTarget) string {
	if target == nil {
		return ""
	}
	return target.ObservationID
}

func repairTargetKind(target *reliability.RepairTarget) reliability.RepairTargetKind {
	if target == nil {
		return ""
	}
	return target.Kind
}

func repairTargetRunID(target *reliability.RepairTarget) string {
	if target == nil {
		return ""
	}
	return target.RunID
}

func (e *productionRepairDryRunExecutor) fail(ctx context.Context, run actioncontract.ActionRun, provider actioncontract.Ref, cause error) (actioncontract.ActionRun, error) {
	errorCode := "repair_dry_run_preflight_failed"
	if errors.Is(cause, reliability.ErrProviderUnavailable) {
		errorCode = "repair_dry_run_provider_unavailable"
	}
	failed, err := e.actions.FailActionRun(ctx, actioncontract.FailRunRequest{
		RunID: run.ID, ProviderCallRef: provider, ErrorCode: errorCode,
		ErrorMessage: "Repair dry-run preflight did not pass",
	})
	if err != nil {
		return actioncontract.ActionRun{}, errors.Join(cause, err)
	}
	return failed, nil
}

func productionActionExecutionMode(value string) (actioncontract.ExecutionMode, error) {
	switch actioncontract.ExecutionMode(strings.ToLower(strings.TrimSpace(value))) {
	case "", actioncontract.ExecutionModeApply:
		return actioncontract.ExecutionModeApply, nil
	case actioncontract.ExecutionModeDryRun:
		return actioncontract.ExecutionModeDryRun, nil
	default:
		return "", fmt.Errorf("execution_mode is invalid")
	}
}
