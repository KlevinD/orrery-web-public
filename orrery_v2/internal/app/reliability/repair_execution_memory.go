package reliability

import (
	"context"
	"fmt"
	"sort"
)

func (r *MemoryRepository) RecordRepairPreflight(ctx context.Context, candidate RepairPreflight) (RepairPreflight, bool, error) {
	if r == nil {
		return RepairPreflight{}, false, fmt.Errorf("%w: repository is required", ErrInvalidRequest)
	}
	if err := validateRepairPreflightCandidate(candidate); err != nil {
		return RepairPreflight{}, false, err
	}
	if err := ctx.Err(); err != nil {
		return RepairPreflight{}, false, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	key := reliabilityKey(candidate.TenantID, candidate.EvidenceID)
	if existing, ok := r.preflights[key]; ok {
		if !sameRepairPreflight(existing, candidate) {
			return RepairPreflight{}, false, fmt.Errorf("%w: preflight evidence id was reused with different data", ErrConflict)
		}
		return existing, false, nil
	}
	r.preflights[key] = candidate
	return candidate, true, nil
}

func (r *MemoryRepository) GetRepairPreflight(ctx context.Context, tenantID, evidenceID string) (RepairPreflight, error) {
	if r == nil {
		return RepairPreflight{}, fmt.Errorf("%w: repository is required", ErrInvalidRequest)
	}
	if err := ctx.Err(); err != nil {
		return RepairPreflight{}, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	preflight, ok := r.preflights[reliabilityKey(tenantID, evidenceID)]
	if !ok {
		return RepairPreflight{}, fmt.Errorf("%w: repair preflight %s", ErrNotFound, evidenceID)
	}
	return preflight, nil
}

func (r *MemoryRepository) ListRepairPreflightsByIncident(ctx context.Context, tenantID, incidentID string, limit int) ([]RepairPreflight, error) {
	if r == nil || tenantID == "" || incidentID == "" {
		return nil, fmt.Errorf("%w: repository, tenant_id, and incident_id are required", ErrInvalidRequest)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 100 {
		limit = 25
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	items := make([]RepairPreflight, 0, limit)
	for _, preflight := range r.preflights {
		if preflight.TenantID == tenantID && preflight.IncidentID == incidentID {
			items = append(items, preflight)
		}
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].CheckedAt.Equal(items[j].CheckedAt) {
			return items[i].EvidenceID > items[j].EvidenceID
		}
		return items[i].CheckedAt.After(items[j].CheckedAt)
	})
	if len(items) > limit {
		items = items[:limit]
	}
	return items, nil
}

func (r *MemoryRepository) BeginRepairExecution(ctx context.Context, candidate RepairExecution) (RepairExecution, bool, error) {
	if r == nil {
		return RepairExecution{}, false, fmt.Errorf("%w: repository is required", ErrInvalidRequest)
	}
	if err := validateRepairExecutionCandidate(candidate); err != nil {
		return RepairExecution{}, false, err
	}
	if err := ctx.Err(); err != nil {
		return RepairExecution{}, false, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	key := reliabilityKey(candidate.TenantID, candidate.ActionRunID)
	if existing, ok := r.executions[key]; ok {
		if !sameRepairExecutionIdentity(existing, candidate) {
			return RepairExecution{}, false, fmt.Errorf("%w: action run was reused with different repair execution data", ErrConflict)
		}
		return cloneRepairExecution(existing), false, nil
	}
	for _, execution := range r.executions {
		if execution.TenantID == candidate.TenantID && execution.IncidentID == candidate.IncidentID && repairExecutionActive(execution.Status) {
			return RepairExecution{}, false, fmt.Errorf("%w: failure incident already has an active repair execution", ErrConflict)
		}
	}
	preflight, ok := r.preflights[reliabilityKey(candidate.TenantID, candidate.PreflightEvidenceID)]
	if !ok {
		return RepairExecution{}, false, fmt.Errorf("%w: repair preflight binding is unavailable", ErrConflict)
	}
	incident, ok := r.incidents[reliabilityKey(candidate.TenantID, candidate.IncidentID)]
	if !ok {
		return RepairExecution{}, false, fmt.Errorf("%w: repair incident binding is unavailable", ErrConflict)
	}
	rule, ok := r.rules[reliabilityKey(candidate.TenantID, candidate.RuleID)]
	if !ok {
		return RepairExecution{}, false, fmt.Errorf("%w: repair rule binding is unavailable", ErrConflict)
	}
	var latestTarget RepairTarget
	foundTarget := false
	for _, target := range r.repairTargets {
		if target.TenantID != candidate.TenantID || target.IncidentID != candidate.IncidentID {
			continue
		}
		if !foundTarget || target.OccurredAt.After(latestTarget.OccurredAt) ||
			(target.OccurredAt.Equal(latestTarget.OccurredAt) && target.ObservationID > latestTarget.ObservationID) {
			latestTarget, foundTarget = target, true
		}
	}
	if !foundTarget || !repairExecutionMatchesCurrentBinding(candidate, preflight, incident, rule, latestTarget) {
		return RepairExecution{}, false, fmt.Errorf("%w: repair preflight is stale or does not match current state", ErrConflict)
	}
	candidate.Status = RepairExecutionDispatching
	candidate.Version = 1
	r.executions[key] = cloneRepairExecution(candidate)
	return cloneRepairExecution(candidate), true, nil
}

func (r *MemoryRepository) GetRepairExecution(ctx context.Context, tenantID, actionRunID string) (RepairExecution, error) {
	if r == nil {
		return RepairExecution{}, fmt.Errorf("%w: repository is required", ErrInvalidRequest)
	}
	if err := ctx.Err(); err != nil {
		return RepairExecution{}, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	execution, ok := r.executions[reliabilityKey(tenantID, actionRunID)]
	if !ok {
		return RepairExecution{}, fmt.Errorf("%w: repair execution %s", ErrNotFound, actionRunID)
	}
	return cloneRepairExecution(execution), nil
}

func (r *MemoryRepository) UpdateRepairExecution(ctx context.Context, update RepairExecutionUpdate) (RepairExecution, error) {
	if r == nil {
		return RepairExecution{}, fmt.Errorf("%w: repository is required", ErrInvalidRequest)
	}
	if err := ctx.Err(); err != nil {
		return RepairExecution{}, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	key := reliabilityKey(update.TenantID, update.ActionRunID)
	execution, ok := r.executions[key]
	if !ok {
		return RepairExecution{}, fmt.Errorf("%w: repair execution %s", ErrNotFound, update.ActionRunID)
	}
	if execution.Version != update.ExpectedVersion {
		return RepairExecution{}, fmt.Errorf("%w: repair execution version changed", ErrConflict)
	}
	if !validRepairExecutionTransition(execution.Status, update.Status) || (update.RetryRunID != "" && update.RetryRunID != execution.RetryRunID) {
		return RepairExecution{}, fmt.Errorf("%w: invalid repair execution transition", ErrInvalidRequest)
	}
	execution.Status = update.Status
	execution.FailureCode = update.FailureCode
	execution.UpdatedAt = update.UpdatedAt.UTC()
	execution.CompletedAt = cloneTime(update.CompletedAt)
	execution.Version++
	r.executions[key] = cloneRepairExecution(execution)
	return cloneRepairExecution(execution), nil
}

func (r *MemoryRepository) ListActiveRepairExecutions(ctx context.Context, limit int) ([]RepairExecution, error) {
	if r == nil {
		return nil, fmt.Errorf("%w: repository is required", ErrInvalidRequest)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	items := make([]RepairExecution, 0, limit)
	for _, execution := range r.executions {
		if repairExecutionActive(execution.Status) {
			items = append(items, cloneRepairExecution(execution))
		}
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].UpdatedAt.Equal(items[j].UpdatedAt) {
			return items[i].ActionRunID < items[j].ActionRunID
		}
		return items[i].UpdatedAt.Before(items[j].UpdatedAt)
	})
	if len(items) > limit {
		items = items[:limit]
	}
	return items, nil
}

func (r *MemoryRepository) ListRepairExecutionsByIncident(ctx context.Context, tenantID, incidentID string, limit int) ([]RepairExecution, error) {
	if r == nil || tenantID == "" || incidentID == "" {
		return nil, fmt.Errorf("%w: repository, tenant_id, and incident_id are required", ErrInvalidRequest)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 100 {
		limit = 25
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	items := make([]RepairExecution, 0, limit)
	for _, execution := range r.executions {
		if execution.TenantID == tenantID && execution.IncidentID == incidentID {
			items = append(items, cloneRepairExecution(execution))
		}
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].UpdatedAt.Equal(items[j].UpdatedAt) {
			return items[i].ActionRunID > items[j].ActionRunID
		}
		return items[i].UpdatedAt.After(items[j].UpdatedAt)
	})
	if len(items) > limit {
		items = items[:limit]
	}
	return items, nil
}
