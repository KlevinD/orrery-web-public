package reliability

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"orrery_v2/internal/platform/db"
)

type PlatformRepository struct {
	store *db.Store
}

func NewPlatformRepository(store *db.Store) *PlatformRepository {
	return &PlatformRepository{store: store}
}

func (r *PlatformRepository) ObserveFailure(ctx context.Context, observation FailureObservation, candidate FailureIncident) (FailureIncident, bool, error) {
	if r == nil || r.store == nil {
		return FailureIncident{}, false, fmt.Errorf("%w: store is required", ErrInvalidRequest)
	}
	var incident FailureIncident
	created := false
	err := r.store.WithTx(ctx, func(tx db.Tx) error {
		result, err := tx.ExecContext(ctx, `
INSERT INTO reliability_failure_observations (tenant_id, id, incident_id, occurred_at)
VALUES (?, ?, ?, ?)
ON CONFLICT(tenant_id, id) DO NOTHING`, observation.TenantID, observation.ID, candidate.ID, timeToUnixNano(observation.OccurredAt))
		if err != nil {
			return fmt.Errorf("insert failure observation: %w", err)
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("read failure observation rows affected: %w", err)
		}
		if affected == 0 {
			var incidentID string
			if err := tx.QueryRowContext(ctx, `SELECT incident_id FROM reliability_failure_observations WHERE tenant_id = ? AND id = ?`, observation.TenantID, observation.ID).Scan(&incidentID); err != nil {
				return fmt.Errorf("load failure observation: %w", err)
			}
			if incidentID != candidate.ID {
				return fmt.Errorf("%w: observation id was reused for a different failure", ErrConflict)
			}
			if err := validateRepairTargetReplayTx(ctx, tx, observation, incidentID); err != nil {
				return err
			}
			incident, err = scanFailureIncident(tx.QueryRowContext(ctx, failureIncidentSelectSQL()+` WHERE tenant_id = ? AND id = ?`, observation.TenantID, incidentID))
			return err
		}
		created = true
		if err := insertRepairTargetTx(ctx, tx, observation, candidate.ID); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `
INSERT INTO reliability_failure_incidents (
	tenant_id, id, fingerprint, source, component, category, severity, status, error_code,
	redacted_summary, retryable, first_seen_at, last_seen_at, occurrence_count, version, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(tenant_id, fingerprint) DO UPDATE SET
	first_seen_at = CASE WHEN excluded.first_seen_at < reliability_failure_incidents.first_seen_at THEN excluded.first_seen_at ELSE reliability_failure_incidents.first_seen_at END,
	last_seen_at = CASE WHEN excluded.last_seen_at > reliability_failure_incidents.last_seen_at THEN excluded.last_seen_at ELSE reliability_failure_incidents.last_seen_at END,
	redacted_summary = CASE
		WHEN excluded.last_seen_at >= reliability_failure_incidents.last_seen_at AND excluded.redacted_summary <> '' THEN excluded.redacted_summary
		ELSE reliability_failure_incidents.redacted_summary
	END,
	retryable = reliability_failure_incidents.retryable OR excluded.retryable,
	severity = CASE
		WHEN excluded.severity = 'critical' THEN excluded.severity
		WHEN excluded.severity = 'error' AND reliability_failure_incidents.severity = 'warning' THEN excluded.severity
		ELSE reliability_failure_incidents.severity
	END,
	status = CASE
		WHEN reliability_failure_incidents.status = 'resolved' AND excluded.last_seen_at >= reliability_failure_incidents.last_seen_at THEN 'open'
		ELSE reliability_failure_incidents.status
	END,
	occurrence_count = reliability_failure_incidents.occurrence_count + 1,
	version = reliability_failure_incidents.version + 1,
	updated_at = excluded.updated_at`,
			candidate.TenantID, candidate.ID, candidate.Fingerprint, candidate.Source, candidate.Component,
			candidate.Category, candidate.Severity, candidate.Status, candidate.ErrorCode, candidate.RedactedSummary,
			candidate.Retryable, timeToUnixNano(candidate.FirstSeenAt), timeToUnixNano(candidate.LastSeenAt),
			candidate.OccurrenceCount, candidate.Version, timeToUnixNano(candidate.CreatedAt), timeToUnixNano(candidate.UpdatedAt),
		)
		if err != nil {
			return fmt.Errorf("upsert failure incident: %w", err)
		}
		incident, err = scanFailureIncident(tx.QueryRowContext(ctx, failureIncidentSelectSQL()+` WHERE tenant_id = ? AND fingerprint = ?`, candidate.TenantID, candidate.Fingerprint))
		return err
	})
	if err != nil {
		return FailureIncident{}, false, err
	}
	return incident, created, nil
}

func insertRepairTargetTx(ctx context.Context, tx db.Tx, observation FailureObservation, incidentID string) error {
	if observation.RepairTarget == nil {
		return nil
	}
	target := *observation.RepairTarget
	_, err := tx.ExecContext(ctx, `
INSERT INTO reliability_failure_repair_targets (
	tenant_id, observation_id, incident_id, target_kind, authorization_tenant_id, mission_id, run_id, occurred_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		observation.TenantID, observation.ID, incidentID, target.Kind, target.AuthorizationTenantID,
		target.MissionID, target.RunID, timeToUnixNano(observation.OccurredAt),
	)
	if err != nil {
		return fmt.Errorf("insert failure repair target: %w", err)
	}
	return nil
}

func validateRepairTargetReplayTx(ctx context.Context, tx db.Tx, observation FailureObservation, incidentID string) error {
	if observation.RepairTarget == nil {
		return nil
	}
	existing, err := scanRepairTarget(tx.QueryRowContext(ctx, repairTargetSelectSQL()+` WHERE tenant_id = ? AND observation_id = ?`, observation.TenantID, observation.ID))
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%w: observation target is missing", ErrConflict)
	}
	if err != nil {
		return err
	}
	candidate := *observation.RepairTarget
	candidate.TenantID, candidate.ObservationID, candidate.IncidentID, candidate.OccurredAt = observation.TenantID, observation.ID, incidentID, observation.OccurredAt
	if !sameRepairTarget(existing, candidate) {
		return fmt.Errorf("%w: observation id was reused with a different repair target", ErrConflict)
	}
	return nil
}

func (r *PlatformRepository) GetLatestRepairTarget(ctx context.Context, tenantID, incidentID string) (RepairTarget, error) {
	if r == nil || r.store == nil {
		return RepairTarget{}, fmt.Errorf("%w: store is required", ErrInvalidRequest)
	}
	target, err := scanRepairTarget(r.store.QueryRow(ctx, repairTargetSelectSQL()+`
 WHERE tenant_id = ? AND incident_id = ?
 ORDER BY occurred_at DESC, observation_id DESC
 LIMIT 1`, tenantID, incidentID))
	if errors.Is(err, sql.ErrNoRows) {
		return RepairTarget{}, fmt.Errorf("%w: repair target for failure incident %s", ErrNotFound, incidentID)
	}
	return target, err
}

func repairTargetSelectSQL() string {
	return `SELECT tenant_id, observation_id, incident_id, target_kind, authorization_tenant_id, mission_id, run_id, occurred_at
FROM reliability_failure_repair_targets`
}

type repairTargetScanner interface {
	Scan(...any) error
}

func scanRepairTarget(scanner repairTargetScanner) (RepairTarget, error) {
	var target RepairTarget
	var occurredAt int64
	if err := scanner.Scan(&target.TenantID, &target.ObservationID, &target.IncidentID, &target.Kind,
		&target.AuthorizationTenantID, &target.MissionID, &target.RunID, &occurredAt); err != nil {
		return RepairTarget{}, err
	}
	target.OccurredAt = unixNanoToTime(occurredAt)
	return target, nil
}

func (r *PlatformRepository) GetIncident(ctx context.Context, tenantID, incidentID string) (FailureIncident, error) {
	if r == nil || r.store == nil {
		return FailureIncident{}, fmt.Errorf("%w: store is required", ErrInvalidRequest)
	}
	incident, err := scanFailureIncident(r.store.QueryRow(ctx, failureIncidentSelectSQL()+` WHERE tenant_id = ? AND id = ?`, tenantID, incidentID))
	if errors.Is(err, sql.ErrNoRows) {
		return FailureIncident{}, fmt.Errorf("%w: failure incident %s", ErrNotFound, incidentID)
	}
	return incident, err
}

func (r *PlatformRepository) UpdateIncidentStatus(ctx context.Context, tenantID, incidentID string, expectedVersion int64, status IncidentStatus, updatedAt time.Time) (FailureIncident, error) {
	if r == nil || r.store == nil {
		return FailureIncident{}, fmt.Errorf("%w: store is required", ErrInvalidRequest)
	}
	result, err := r.store.Exec(ctx, `
UPDATE reliability_failure_incidents
SET status = ?, version = version + 1, updated_at = ?
WHERE tenant_id = ? AND id = ? AND version = ?`, status, timeToUnixNano(updatedAt), tenantID, incidentID, expectedVersion)
	if err != nil {
		return FailureIncident{}, fmt.Errorf("update failure incident status: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return FailureIncident{}, fmt.Errorf("read failure incident rows affected: %w", err)
	}
	if affected == 0 {
		if _, getErr := r.GetIncident(ctx, tenantID, incidentID); getErr != nil {
			return FailureIncident{}, getErr
		}
		return FailureIncident{}, fmt.Errorf("%w: failure incident version changed", ErrConflict)
	}
	return r.GetIncident(ctx, tenantID, incidentID)
}

func (r *PlatformRepository) UpsertRepairRule(ctx context.Context, commandID string, candidate RepairRule) (RepairRule, bool, error) {
	if r == nil || r.store == nil {
		return RepairRule{}, false, fmt.Errorf("%w: store is required", ErrInvalidRequest)
	}
	var rule RepairRule
	changed := false
	err := r.store.WithTx(ctx, func(tx db.Tx) error {
		if err := db.LockRecordKeyTx(ctx, tx, "reliability_repair_rules", candidate.TenantID+"\x00"+candidate.ID); err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, `
INSERT INTO reliability_repair_rule_commands (tenant_id, id, rule_id, created_at)
VALUES (?, ?, ?, ?)
ON CONFLICT(tenant_id, id) DO NOTHING`, candidate.TenantID, commandID, candidate.ID, timeToUnixNano(candidate.UpdatedAt))
		if err != nil {
			return fmt.Errorf("insert repair rule command: %w", err)
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("read repair rule command rows affected: %w", err)
		}
		if affected == 0 {
			var ruleID string
			if err := tx.QueryRowContext(ctx, `SELECT rule_id FROM reliability_repair_rule_commands WHERE tenant_id = ? AND id = ?`, candidate.TenantID, commandID).Scan(&ruleID); err != nil {
				return fmt.Errorf("load repair rule command: %w", err)
			}
			if ruleID != candidate.ID {
				return fmt.Errorf("%w: command id was reused for a different repair rule", ErrConflict)
			}
			rule, err = scanRepairRule(tx.QueryRowContext(ctx, repairRuleSelectSQL()+` WHERE tenant_id = ? AND id = ?`, candidate.TenantID, ruleID))
			return err
		}
		changed = true
		existing, err := scanRepairRule(tx.QueryRowContext(ctx, repairRuleSelectSQL()+` WHERE tenant_id = ? AND id = ?`, candidate.TenantID, candidate.ID))
		if err == nil {
			if err := validateRepairRuleUpdate(existing, candidate); err != nil {
				return err
			}
			candidate.CreatedAt = existing.CreatedAt
			candidate.Version = existing.Version + 1
		} else if errors.Is(err, sql.ErrNoRows) {
			candidate.Version = 1
		} else {
			return err
		}
		_, err = tx.ExecContext(ctx, `
INSERT INTO reliability_repair_rules (
	tenant_id, id, selector_fingerprint, strategy, action_type_id, risk_level, status, confidence,
	support_count, success_count, failure_count, reversible, idempotent, auto_apply, version,
	source_incident_id, diagnosis_case_id, activation_decision_id, activated_by_kind, activated_by_id,
	activated_at, last_verified_at, expires_at, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(tenant_id, id) DO UPDATE SET
	selector_fingerprint = excluded.selector_fingerprint,
	strategy = excluded.strategy,
	action_type_id = excluded.action_type_id,
	risk_level = excluded.risk_level,
	status = excluded.status,
	confidence = excluded.confidence,
	support_count = excluded.support_count,
	success_count = excluded.success_count,
	failure_count = excluded.failure_count,
	reversible = excluded.reversible,
	idempotent = excluded.idempotent,
	auto_apply = excluded.auto_apply,
	version = excluded.version,
	source_incident_id = excluded.source_incident_id,
	diagnosis_case_id = excluded.diagnosis_case_id,
	activation_decision_id = excluded.activation_decision_id,
	activated_by_kind = excluded.activated_by_kind,
	activated_by_id = excluded.activated_by_id,
	activated_at = excluded.activated_at,
	last_verified_at = excluded.last_verified_at,
	expires_at = excluded.expires_at,
	updated_at = excluded.updated_at`, repairRuleArgs(candidate)...)
		if err != nil {
			return fmt.Errorf("upsert repair rule: %w", err)
		}
		rule = candidate
		return nil
	})
	if err != nil {
		return RepairRule{}, false, err
	}
	return rule, changed, nil
}

func (r *PlatformRepository) GetRepairRule(ctx context.Context, tenantID, ruleID string) (RepairRule, error) {
	if r == nil || r.store == nil {
		return RepairRule{}, fmt.Errorf("%w: store is required", ErrInvalidRequest)
	}
	rule, err := scanRepairRule(r.store.QueryRow(ctx, repairRuleSelectSQL()+` WHERE tenant_id = ? AND id = ?`, tenantID, ruleID))
	if errors.Is(err, sql.ErrNoRows) {
		return RepairRule{}, fmt.Errorf("%w: repair rule %s", ErrNotFound, ruleID)
	}
	return rule, err
}

func (r *PlatformRepository) ListRepairRules(ctx context.Context, tenantID, fingerprint string) ([]RepairRule, error) {
	if r == nil || r.store == nil {
		return nil, fmt.Errorf("%w: store is required", ErrInvalidRequest)
	}
	query := repairRuleSelectSQL() + ` WHERE tenant_id = ?`
	args := []any{tenantID}
	if fingerprint != "" {
		query += ` AND selector_fingerprint = ?`
		args = append(args, fingerprint)
	}
	query += ` ORDER BY id`
	rows, err := r.store.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list repair rules: %w", err)
	}
	defer rows.Close()
	var rules []RepairRule
	for rows.Next() {
		rule, err := scanRepairRule(rows)
		if err != nil {
			return nil, err
		}
		rules = append(rules, rule)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list repair rules: %w", err)
	}
	return rules, nil
}

func (r *PlatformRepository) RecordRepairOutcome(ctx context.Context, outcome RepairOutcome, updatedAt time.Time) (RepairRule, bool, error) {
	if r == nil || r.store == nil {
		return RepairRule{}, false, fmt.Errorf("%w: store is required", ErrInvalidRequest)
	}
	var rule RepairRule
	changed := false
	err := r.store.WithTx(ctx, func(tx db.Tx) error {
		if err := db.LockRecordKeyTx(ctx, tx, "reliability_repair_rules", outcome.TenantID+"\x00"+outcome.RuleID); err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, `
INSERT INTO reliability_repair_outcomes (tenant_id, id, rule_id, incident_id, success, evidence_id, occurred_at)
VALUES (?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(tenant_id, id) DO NOTHING`, outcome.TenantID, outcome.ID, outcome.RuleID, outcome.IncidentID, outcome.Success, outcome.EvidenceID, timeToUnixNano(outcome.OccurredAt))
		if err != nil {
			return fmt.Errorf("insert repair outcome: %w", err)
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("read repair outcome rows affected: %w", err)
		}
		if affected == 0 {
			var ruleID, incidentID, evidenceID string
			var success bool
			if err := tx.QueryRowContext(ctx, `SELECT rule_id, incident_id, success, evidence_id FROM reliability_repair_outcomes WHERE tenant_id = ? AND id = ?`, outcome.TenantID, outcome.ID).Scan(&ruleID, &incidentID, &success, &evidenceID); err != nil {
				return fmt.Errorf("load repair outcome: %w", err)
			}
			if ruleID != outcome.RuleID || incidentID != outcome.IncidentID || success != outcome.Success || evidenceID != outcome.EvidenceID {
				return fmt.Errorf("%w: outcome id was reused with different repair outcome data", ErrConflict)
			}
			rule, err = scanRepairRule(tx.QueryRowContext(ctx, repairRuleSelectSQL()+` WHERE tenant_id = ? AND id = ?`, outcome.TenantID, ruleID))
			return err
		}
		changed = true
		rule, err = scanRepairRule(tx.QueryRowContext(ctx, repairRuleSelectSQL()+` WHERE tenant_id = ? AND id = ?`, outcome.TenantID, outcome.RuleID))
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%w: repair rule %s", ErrNotFound, outcome.RuleID)
		}
		if err != nil {
			return err
		}
		expectedVersion := rule.Version
		applyRepairOutcome(&rule, outcome, updatedAt)
		result, err = tx.ExecContext(ctx, `
UPDATE reliability_repair_rules SET
	status = ?, confidence = ?, success_count = ?, failure_count = ?, auto_apply = ?, version = ?,
	last_verified_at = ?, updated_at = ?
WHERE tenant_id = ? AND id = ? AND version = ?`, rule.Status, rule.Confidence, rule.SuccessCount, rule.FailureCount,
			rule.AutoApply, rule.Version, optionalTimeToUnixNano(rule.LastVerifiedAt), timeToUnixNano(rule.UpdatedAt),
			rule.TenantID, rule.ID, expectedVersion)
		if err != nil {
			return fmt.Errorf("update repair rule outcome: %w", err)
		}
		affected, err = result.RowsAffected()
		if err != nil {
			return fmt.Errorf("read repair rule outcome rows affected: %w", err)
		}
		if affected == 0 {
			return fmt.Errorf("%w: repair rule version changed", ErrConflict)
		}
		return nil
	})
	if err != nil {
		return RepairRule{}, false, err
	}
	return rule, changed, nil
}

type rowScanner interface {
	Scan(...any) error
}

func failureIncidentSelectSQL() string {
	return `SELECT tenant_id, id, fingerprint, source, component, category, severity, status, error_code,
	redacted_summary, retryable, first_seen_at, last_seen_at, occurrence_count, version, created_at, updated_at
FROM reliability_failure_incidents`
}

func scanFailureIncident(row rowScanner) (FailureIncident, error) {
	var incident FailureIncident
	var firstSeenAt, lastSeenAt, createdAt, updatedAt int64
	err := row.Scan(
		&incident.TenantID, &incident.ID, &incident.Fingerprint, &incident.Source, &incident.Component,
		&incident.Category, &incident.Severity, &incident.Status, &incident.ErrorCode, &incident.RedactedSummary,
		&incident.Retryable, &firstSeenAt, &lastSeenAt, &incident.OccurrenceCount, &incident.Version, &createdAt, &updatedAt,
	)
	if err != nil {
		return FailureIncident{}, err
	}
	incident.FirstSeenAt = unixNanoToTime(firstSeenAt)
	incident.LastSeenAt = unixNanoToTime(lastSeenAt)
	incident.CreatedAt = unixNanoToTime(createdAt)
	incident.UpdatedAt = unixNanoToTime(updatedAt)
	return incident, nil
}

func repairRuleSelectSQL() string {
	return `SELECT tenant_id, id, selector_fingerprint, strategy, action_type_id, risk_level, status, confidence,
	support_count, success_count, failure_count, reversible, idempotent, auto_apply, version, source_incident_id,
	diagnosis_case_id, activation_decision_id, activated_by_kind, activated_by_id, activated_at,
	last_verified_at, expires_at, created_at, updated_at
FROM reliability_repair_rules`
}

func scanRepairRule(row rowScanner) (RepairRule, error) {
	var rule RepairRule
	var activatedAt, lastVerifiedAt, expiresAt sql.NullInt64
	var createdAt, updatedAt int64
	err := row.Scan(
		&rule.TenantID, &rule.ID, &rule.SelectorFingerprint, &rule.Strategy, &rule.ActionTypeID,
		&rule.RiskLevel, &rule.Status, &rule.Confidence, &rule.SupportCount, &rule.SuccessCount,
		&rule.FailureCount, &rule.Reversible, &rule.Idempotent, &rule.AutoApply, &rule.Version,
		&rule.SourceIncidentID, &rule.DiagnosisCaseID, &rule.ActivationDecisionID, &rule.ActivatedBy.Kind,
		&rule.ActivatedBy.ID, &activatedAt, &lastVerifiedAt, &expiresAt, &createdAt, &updatedAt,
	)
	if err != nil {
		return RepairRule{}, err
	}
	if activatedAt.Valid {
		value := unixNanoToTime(activatedAt.Int64)
		rule.ActivatedAt = &value
	}
	if lastVerifiedAt.Valid {
		value := unixNanoToTime(lastVerifiedAt.Int64)
		rule.LastVerifiedAt = &value
	}
	if expiresAt.Valid {
		value := unixNanoToTime(expiresAt.Int64)
		rule.ExpiresAt = &value
	}
	rule.CreatedAt = unixNanoToTime(createdAt)
	rule.UpdatedAt = unixNanoToTime(updatedAt)
	return rule, nil
}

func repairRuleArgs(rule RepairRule) []any {
	return []any{
		rule.TenantID, rule.ID, rule.SelectorFingerprint, rule.Strategy, rule.ActionTypeID, rule.RiskLevel,
		rule.Status, rule.Confidence, rule.SupportCount, rule.SuccessCount, rule.FailureCount, rule.Reversible,
		rule.Idempotent, rule.AutoApply, rule.Version, rule.SourceIncidentID, rule.DiagnosisCaseID,
		rule.ActivationDecisionID, rule.ActivatedBy.Kind, rule.ActivatedBy.ID, optionalTimeToUnixNano(rule.ActivatedAt), optionalTimeToUnixNano(rule.LastVerifiedAt),
		optionalTimeToUnixNano(rule.ExpiresAt), timeToUnixNano(rule.CreatedAt), timeToUnixNano(rule.UpdatedAt),
	}
}

func timeToUnixNano(value time.Time) int64 {
	return value.UTC().UnixNano()
}

func optionalTimeToUnixNano(value *time.Time) any {
	if value == nil {
		return nil
	}
	return timeToUnixNano(*value)
}

func unixNanoToTime(value int64) time.Time {
	return time.Unix(0, value).UTC()
}
