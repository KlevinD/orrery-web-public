package reliability

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"orrery_v2/internal/platform/db"
)

func (r *PlatformRepository) RecordRepairPreflight(ctx context.Context, candidate RepairPreflight) (RepairPreflight, bool, error) {
	if r == nil || r.store == nil {
		return RepairPreflight{}, false, fmt.Errorf("%w: store is required", ErrInvalidRequest)
	}
	if err := validateRepairPreflightCandidate(candidate); err != nil {
		return RepairPreflight{}, false, err
	}
	result, err := r.store.Exec(ctx, `
INSERT INTO reliability_repair_preflights (
	tenant_id, evidence_id, action_run_id, incident_id, incident_version, rule_id, rule_version, strategy, args_hash,
	target_observation_id, target_kind, target_run_id, passed, checked_at, expires_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(tenant_id, evidence_id) DO NOTHING`,
		candidate.TenantID, candidate.EvidenceID, candidate.ActionRunID, candidate.IncidentID, candidate.IncidentVersion,
		candidate.RuleID, candidate.RuleVersion, candidate.Strategy, candidate.ArgsHash, candidate.TargetObservationID, candidate.TargetKind, candidate.TargetRunID,
		candidate.Passed, timeToUnixNano(candidate.CheckedAt), timeToUnixNano(candidate.ExpiresAt),
	)
	if err != nil {
		return RepairPreflight{}, false, fmt.Errorf("insert repair preflight: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return RepairPreflight{}, false, fmt.Errorf("read repair preflight rows affected: %w", err)
	}
	stored, err := r.GetRepairPreflight(ctx, candidate.TenantID, candidate.EvidenceID)
	if err != nil {
		return RepairPreflight{}, false, err
	}
	if affected == 0 && !sameRepairPreflight(stored, candidate) {
		return RepairPreflight{}, false, fmt.Errorf("%w: preflight evidence id was reused with different data", ErrConflict)
	}
	return stored, affected == 1, nil
}

func (r *PlatformRepository) GetRepairPreflight(ctx context.Context, tenantID, evidenceID string) (RepairPreflight, error) {
	if r == nil || r.store == nil {
		return RepairPreflight{}, fmt.Errorf("%w: store is required", ErrInvalidRequest)
	}
	preflight, err := scanRepairPreflight(r.store.QueryRow(ctx, repairPreflightSelectSQL()+` WHERE tenant_id = ? AND evidence_id = ?`, tenantID, evidenceID))
	if errors.Is(err, sql.ErrNoRows) {
		return RepairPreflight{}, fmt.Errorf("%w: repair preflight %s", ErrNotFound, evidenceID)
	}
	return preflight, err
}

func (r *PlatformRepository) ListRepairPreflightsByIncident(ctx context.Context, tenantID, incidentID string, limit int) ([]RepairPreflight, error) {
	if r == nil || r.store == nil || tenantID == "" || incidentID == "" {
		return nil, fmt.Errorf("%w: store, tenant_id, and incident_id are required", ErrInvalidRequest)
	}
	if limit <= 0 || limit > 100 {
		limit = 25
	}
	rows, err := r.store.Query(ctx, repairPreflightSelectSQL()+`
 WHERE tenant_id = ? AND incident_id = ?
 ORDER BY checked_at DESC, evidence_id DESC
 LIMIT ?`, tenantID, incidentID, limit)
	if err != nil {
		return nil, fmt.Errorf("list repair preflights: %w", err)
	}
	defer rows.Close()
	items := make([]RepairPreflight, 0, limit)
	for rows.Next() {
		preflight, scanErr := scanRepairPreflight(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		items = append(items, preflight)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list repair preflights: %w", err)
	}
	return items, nil
}

func repairPreflightSelectSQL() string {
	return `SELECT evidence_id, tenant_id, action_run_id, incident_id, incident_version, rule_id, rule_version, strategy, args_hash,
	target_observation_id, target_kind, target_run_id, passed, checked_at, expires_at
FROM reliability_repair_preflights`
}

type repairExecutionScanner interface {
	Scan(...any) error
}

func scanRepairPreflight(scanner repairExecutionScanner) (RepairPreflight, error) {
	var preflight RepairPreflight
	var checkedAt, expiresAt int64
	if err := scanner.Scan(&preflight.EvidenceID, &preflight.TenantID, &preflight.ActionRunID, &preflight.IncidentID, &preflight.IncidentVersion,
		&preflight.RuleID, &preflight.RuleVersion, &preflight.Strategy, &preflight.ArgsHash, &preflight.TargetObservationID, &preflight.TargetKind,
		&preflight.TargetRunID, &preflight.Passed, &checkedAt, &expiresAt); err != nil {
		return RepairPreflight{}, err
	}
	preflight.CheckedAt, preflight.ExpiresAt = unixNanoToTime(checkedAt), unixNanoToTime(expiresAt)
	return preflight, nil
}

func (r *PlatformRepository) BeginRepairExecution(ctx context.Context, candidate RepairExecution) (RepairExecution, bool, error) {
	if r == nil || r.store == nil {
		return RepairExecution{}, false, fmt.Errorf("%w: store is required", ErrInvalidRequest)
	}
	if err := validateRepairExecutionCandidate(candidate); err != nil {
		return RepairExecution{}, false, err
	}
	var stored RepairExecution
	created := false
	err := r.store.WithTx(ctx, func(tx db.Tx) error {
		if err := db.LockRecordKeyTx(ctx, tx, "reliability_repair_execution_incident", candidate.TenantID+"\x00"+candidate.IncidentID); err != nil {
			return err
		}
		existing, err := scanRepairExecution(tx.QueryRowContext(ctx, repairExecutionSelectSQL()+` WHERE tenant_id = ? AND action_run_id = ?`, candidate.TenantID, candidate.ActionRunID))
		if err == nil {
			if !sameRepairExecutionIdentity(existing, candidate) {
				return fmt.Errorf("%w: action run was reused with different repair execution data", ErrConflict)
			}
			stored = existing
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		var activeActionRunID string
		err = tx.QueryRowContext(ctx, `
SELECT action_run_id FROM reliability_repair_executions
WHERE tenant_id = ? AND incident_id = ? AND status IN (?, ?)
LIMIT 1`, candidate.TenantID, candidate.IncidentID, RepairExecutionDispatching, RepairExecutionMonitoring).Scan(&activeActionRunID)
		if err == nil {
			return fmt.Errorf("%w: failure incident already has active repair execution %s", ErrConflict, activeActionRunID)
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		candidate.Status = RepairExecutionDispatching
		candidate.Version = 1
		result, err := tx.ExecContext(ctx, `
INSERT INTO reliability_repair_executions (
	tenant_id, action_run_id, incident_id, rule_id, preflight_evidence_id, strategy,
	target_observation_id, target_kind, target_authorization_tenant_id, target_mission_id, target_run_id, target_occurred_at,
	retry_run_id, status, failure_code, version, started_at, deadline_at, updated_at, completed_at
) SELECT ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?
FROM reliability_repair_preflights AS preflight
JOIN reliability_failure_incidents AS incident
	ON incident.tenant_id = preflight.tenant_id AND incident.id = preflight.incident_id AND incident.version = preflight.incident_version
JOIN reliability_repair_rules AS rule
	ON rule.tenant_id = preflight.tenant_id AND rule.id = preflight.rule_id AND rule.version = preflight.rule_version
JOIN reliability_failure_repair_targets AS target
	ON target.tenant_id = preflight.tenant_id AND target.observation_id = preflight.target_observation_id
WHERE preflight.tenant_id = ? AND preflight.evidence_id = ? AND preflight.incident_id = ? AND preflight.rule_id = ?
	AND preflight.strategy = ? AND preflight.target_observation_id = ? AND preflight.target_kind = ? AND preflight.target_run_id = ?
	AND preflight.passed = TRUE AND preflight.checked_at <= ? AND preflight.expires_at > ?
	AND target.incident_id = preflight.incident_id AND target.target_kind = preflight.target_kind AND target.run_id = preflight.target_run_id
	AND target.authorization_tenant_id = ? AND target.mission_id = ? AND target.occurred_at = ? AND target.occurred_at = incident.last_seen_at
	AND NOT EXISTS (
		SELECT 1 FROM reliability_failure_repair_targets AS newer
		WHERE newer.tenant_id = target.tenant_id AND newer.incident_id = target.incident_id
			AND (newer.occurred_at > target.occurred_at OR (newer.occurred_at = target.occurred_at AND newer.observation_id > target.observation_id))
	)`,
			candidate.TenantID, candidate.ActionRunID, candidate.IncidentID, candidate.RuleID, candidate.PreflightEvidenceID, candidate.Strategy,
			candidate.Target.ObservationID, candidate.Target.Kind, candidate.Target.AuthorizationTenantID, candidate.Target.MissionID, candidate.Target.RunID,
			timeToUnixNano(candidate.Target.OccurredAt), candidate.RetryRunID, candidate.Status, candidate.FailureCode, candidate.Version, timeToUnixNano(candidate.StartedAt),
			timeToUnixNano(candidate.DeadlineAt), timeToUnixNano(candidate.UpdatedAt), nil,
			candidate.TenantID, candidate.PreflightEvidenceID, candidate.IncidentID, candidate.RuleID, candidate.Strategy,
			candidate.Target.ObservationID, candidate.Target.Kind, candidate.Target.RunID, timeToUnixNano(candidate.StartedAt), timeToUnixNano(candidate.StartedAt),
			candidate.Target.AuthorizationTenantID, candidate.Target.MissionID, timeToUnixNano(candidate.Target.OccurredAt),
		)
		if err != nil {
			return fmt.Errorf("insert repair execution: %w", err)
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("read repair execution rows affected: %w", err)
		}
		if affected != 1 {
			return fmt.Errorf("%w: repair preflight is stale or does not match current state", ErrConflict)
		}
		stored, created = candidate, true
		return nil
	})
	if err != nil {
		return RepairExecution{}, false, err
	}
	return stored, created, nil
}

func (r *PlatformRepository) GetRepairExecution(ctx context.Context, tenantID, actionRunID string) (RepairExecution, error) {
	if r == nil || r.store == nil {
		return RepairExecution{}, fmt.Errorf("%w: store is required", ErrInvalidRequest)
	}
	execution, err := scanRepairExecution(r.store.QueryRow(ctx, repairExecutionSelectSQL()+` WHERE tenant_id = ? AND action_run_id = ?`, tenantID, actionRunID))
	if errors.Is(err, sql.ErrNoRows) {
		return RepairExecution{}, fmt.Errorf("%w: repair execution %s", ErrNotFound, actionRunID)
	}
	return execution, err
}

func (r *PlatformRepository) UpdateRepairExecution(ctx context.Context, update RepairExecutionUpdate) (RepairExecution, error) {
	if r == nil || r.store == nil {
		return RepairExecution{}, fmt.Errorf("%w: store is required", ErrInvalidRequest)
	}
	current, err := r.GetRepairExecution(ctx, update.TenantID, update.ActionRunID)
	if err != nil {
		return RepairExecution{}, err
	}
	if current.Version != update.ExpectedVersion {
		return RepairExecution{}, fmt.Errorf("%w: repair execution version changed", ErrConflict)
	}
	if !validRepairExecutionTransition(current.Status, update.Status) || (update.RetryRunID != "" && update.RetryRunID != current.RetryRunID) {
		return RepairExecution{}, fmt.Errorf("%w: invalid repair execution transition", ErrInvalidRequest)
	}
	var completedAt any
	if update.CompletedAt != nil {
		completedAt = timeToUnixNano(*update.CompletedAt)
	}
	result, err := r.store.Exec(ctx, `
UPDATE reliability_repair_executions
SET status = ?, failure_code = ?, version = version + 1, updated_at = ?, completed_at = ?
WHERE tenant_id = ? AND action_run_id = ? AND version = ?`,
		update.Status, update.FailureCode, timeToUnixNano(update.UpdatedAt), completedAt,
		update.TenantID, update.ActionRunID, update.ExpectedVersion,
	)
	if err != nil {
		return RepairExecution{}, fmt.Errorf("update repair execution: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return RepairExecution{}, err
	}
	if affected == 0 {
		return RepairExecution{}, fmt.Errorf("%w: repair execution version changed", ErrConflict)
	}
	return r.GetRepairExecution(ctx, update.TenantID, update.ActionRunID)
}

func (r *PlatformRepository) ListActiveRepairExecutions(ctx context.Context, limit int) ([]RepairExecution, error) {
	if r == nil || r.store == nil {
		return nil, fmt.Errorf("%w: store is required", ErrInvalidRequest)
	}
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := r.store.Query(ctx, repairExecutionSelectSQL()+`
 WHERE status IN (?, ?)
 ORDER BY updated_at, action_run_id
 LIMIT ?`, RepairExecutionDispatching, RepairExecutionMonitoring, limit)
	if err != nil {
		return nil, fmt.Errorf("list active repair executions: %w", err)
	}
	defer rows.Close()
	items := make([]RepairExecution, 0, limit)
	for rows.Next() {
		execution, err := scanRepairExecution(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, execution)
	}
	return items, rows.Err()
}

func (r *PlatformRepository) ListRepairExecutionsByIncident(ctx context.Context, tenantID, incidentID string, limit int) ([]RepairExecution, error) {
	if r == nil || r.store == nil || tenantID == "" || incidentID == "" {
		return nil, fmt.Errorf("%w: store, tenant_id, and incident_id are required", ErrInvalidRequest)
	}
	if limit <= 0 || limit > 100 {
		limit = 25
	}
	rows, err := r.store.Query(ctx, repairExecutionSelectSQL()+`
 WHERE tenant_id = ? AND incident_id = ?
 ORDER BY updated_at DESC, action_run_id DESC
 LIMIT ?`, tenantID, incidentID, limit)
	if err != nil {
		return nil, fmt.Errorf("list repair executions: %w", err)
	}
	defer rows.Close()
	items := make([]RepairExecution, 0, limit)
	for rows.Next() {
		execution, scanErr := scanRepairExecution(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		items = append(items, execution)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list repair executions: %w", err)
	}
	return items, nil
}

func repairExecutionSelectSQL() string {
	return `SELECT tenant_id, action_run_id, incident_id, rule_id, preflight_evidence_id, strategy,
	target_observation_id, target_kind, target_authorization_tenant_id, target_mission_id, target_run_id, target_occurred_at,
	retry_run_id, status, failure_code, version, started_at, deadline_at, updated_at, completed_at
FROM reliability_repair_executions`
}

func scanRepairExecution(scanner repairExecutionScanner) (RepairExecution, error) {
	var execution RepairExecution
	var targetOccurredAt, startedAt, deadlineAt, updatedAt int64
	var completedAt sql.NullInt64
	if err := scanner.Scan(&execution.TenantID, &execution.ActionRunID, &execution.IncidentID, &execution.RuleID,
		&execution.PreflightEvidenceID, &execution.Strategy, &execution.Target.ObservationID, &execution.Target.Kind,
		&execution.Target.AuthorizationTenantID, &execution.Target.MissionID, &execution.Target.RunID, &targetOccurredAt, &execution.RetryRunID,
		&execution.Status, &execution.FailureCode, &execution.Version, &startedAt, &deadlineAt, &updatedAt, &completedAt); err != nil {
		return RepairExecution{}, err
	}
	execution.Target.TenantID = execution.TenantID
	execution.Target.IncidentID = execution.IncidentID
	execution.Target.OccurredAt = unixNanoToTime(targetOccurredAt)
	execution.StartedAt, execution.DeadlineAt, execution.UpdatedAt = unixNanoToTime(startedAt), unixNanoToTime(deadlineAt), unixNanoToTime(updatedAt)
	if completedAt.Valid {
		value := unixNanoToTime(completedAt.Int64)
		execution.CompletedAt = &value
	}
	return execution, nil
}
