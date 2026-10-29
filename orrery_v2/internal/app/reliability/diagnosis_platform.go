package reliability

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"orrery_v2/internal/platform/db"
)

func (r *PlatformRepository) CreateDiagnosisCase(ctx context.Context, commandID string, candidate DiagnosisCase) (DiagnosisCase, bool, error) {
	if r == nil || r.store == nil {
		return DiagnosisCase{}, false, fmt.Errorf("%w: store is required", ErrInvalidRequest)
	}
	if err := validateDiagnosisCase(candidate); err != nil {
		return DiagnosisCase{}, false, err
	}
	var stored DiagnosisCase
	created := false
	err := r.store.WithTx(ctx, func(tx db.Tx) error {
		if err := db.LockRecordKeyTx(ctx, tx, "reliability_diagnosis_incident", candidate.TenantID+"\x00"+candidate.IncidentID); err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, `
INSERT INTO reliability_diagnosis_commands (
	tenant_id, id, diagnosis_id, operation, decision, reason, actor_kind, actor_id, created_at
) VALUES (?, ?, ?, 'create', '', '', ?, ?, ?)
ON CONFLICT(tenant_id, id) DO NOTHING`, candidate.TenantID, commandID, candidate.ID, candidate.ProposedBy.Kind, candidate.ProposedBy.ID, timeToUnixNano(candidate.CreatedAt))
		if err != nil {
			return fmt.Errorf("insert diagnosis create command: %w", err)
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("read diagnosis create command rows affected: %w", err)
		}
		if affected == 0 {
			var diagnosisID, operation string
			if err := tx.QueryRowContext(ctx, `SELECT diagnosis_id, operation FROM reliability_diagnosis_commands WHERE tenant_id = ? AND id = ?`, candidate.TenantID, commandID).Scan(&diagnosisID, &operation); err != nil {
				return fmt.Errorf("load diagnosis create command: %w", err)
			}
			if operation != "create" || diagnosisID != candidate.ID {
				return fmt.Errorf("%w: diagnosis command id was reused", ErrConflict)
			}
			stored, err = loadDiagnosisCaseTx(ctx, tx, candidate.TenantID, diagnosisID)
			if err != nil {
				return err
			}
			if !sameDiagnosisProposal(stored, candidate) {
				return fmt.Errorf("%w: diagnosis command id was reused with different data", ErrConflict)
			}
			return nil
		}
		var incidentVersion int64
		if err := tx.QueryRowContext(ctx, `SELECT version FROM reliability_failure_incidents WHERE tenant_id = ? AND id = ?`, candidate.TenantID, candidate.IncidentID).Scan(&incidentVersion); errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%w: failure incident %s", ErrNotFound, candidate.IncidentID)
		} else if err != nil {
			return err
		}
		if incidentVersion != candidate.IncidentVersion {
			return fmt.Errorf("%w: diagnosis incident version changed", ErrConflict)
		}
		candidate.Version = 1
		if _, err := tx.ExecContext(ctx, `
INSERT INTO reliability_diagnosis_cases (
	tenant_id, id, incident_id, incident_version, root_cause_category, redacted_summary, confidence, status,
	proposed_by_kind, proposed_by_id, reviewed_by_kind, reviewed_by_id, review_reason, version, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, '', '', '', ?, ?, ?)`,
			candidate.TenantID, candidate.ID, candidate.IncidentID, candidate.IncidentVersion, candidate.RootCauseCategory,
			candidate.RedactedSummary, candidate.Confidence, candidate.Status, candidate.ProposedBy.Kind, candidate.ProposedBy.ID,
			candidate.Version, timeToUnixNano(candidate.CreatedAt), timeToUnixNano(candidate.UpdatedAt)); err != nil {
			return fmt.Errorf("insert diagnosis case: %w", err)
		}
		for index, evidenceID := range candidate.EvidenceIDs {
			if _, err := tx.ExecContext(ctx, `INSERT INTO reliability_diagnosis_evidence (tenant_id, diagnosis_id, position, evidence_id) VALUES (?, ?, ?, ?)`,
				candidate.TenantID, candidate.ID, index, evidenceID); err != nil {
				return fmt.Errorf("insert diagnosis evidence: %w", err)
			}
		}
		stored, created = cloneDiagnosisCase(candidate), true
		return nil
	})
	if err != nil {
		return DiagnosisCase{}, false, err
	}
	return stored, created, nil
}

func (r *PlatformRepository) GetDiagnosisCase(ctx context.Context, tenantID, diagnosisID string) (DiagnosisCase, error) {
	if r == nil || r.store == nil {
		return DiagnosisCase{}, fmt.Errorf("%w: store is required", ErrInvalidRequest)
	}
	diagnosis, err := scanDiagnosisCase(r.store.QueryRow(ctx, diagnosisCaseSelectSQL()+` WHERE tenant_id = ? AND id = ?`, tenantID, diagnosisID))
	if errors.Is(err, sql.ErrNoRows) {
		return DiagnosisCase{}, fmt.Errorf("%w: diagnosis case %s", ErrNotFound, diagnosisID)
	}
	if err != nil {
		return DiagnosisCase{}, err
	}
	diagnosis.EvidenceIDs, err = r.listDiagnosisEvidence(ctx, tenantID, diagnosisID)
	return diagnosis, err
}

func (r *PlatformRepository) ListDiagnosisCasesByIncident(ctx context.Context, tenantID, incidentID string, limit int) ([]DiagnosisCase, error) {
	if r == nil || r.store == nil || tenantID == "" || incidentID == "" {
		return nil, fmt.Errorf("%w: store, tenant_id, and incident_id are required", ErrInvalidRequest)
	}
	if limit <= 0 || limit > 100 {
		limit = 25
	}
	rows, err := r.store.Query(ctx, diagnosisCaseSelectSQL()+`
 WHERE tenant_id = ? AND incident_id = ?
 ORDER BY updated_at DESC, id DESC
 LIMIT ?`, tenantID, incidentID, limit)
	if err != nil {
		return nil, fmt.Errorf("list diagnosis cases: %w", err)
	}
	items := make([]DiagnosisCase, 0, limit)
	for rows.Next() {
		diagnosis, scanErr := scanDiagnosisCase(rows)
		if scanErr != nil {
			rows.Close()
			return nil, scanErr
		}
		items = append(items, diagnosis)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("list diagnosis cases: %w", err)
	}
	rows.Close()
	for index := range items {
		items[index].EvidenceIDs, err = r.listDiagnosisEvidence(ctx, tenantID, items[index].ID)
		if err != nil {
			return nil, err
		}
	}
	return items, nil
}

func (r *PlatformRepository) DecideDiagnosisCase(ctx context.Context, command DiagnosisDecisionCommand, updatedAt time.Time) (DiagnosisCase, bool, error) {
	if r == nil || r.store == nil {
		return DiagnosisCase{}, false, fmt.Errorf("%w: store is required", ErrInvalidRequest)
	}
	if err := validateDiagnosisDecisionCommand(command); err != nil {
		return DiagnosisCase{}, false, err
	}
	var stored DiagnosisCase
	changed := false
	err := r.store.WithTx(ctx, func(tx db.Tx) error {
		if err := db.LockRecordKeyTx(ctx, tx, "reliability_diagnosis_case", command.TenantID+"\x00"+command.DiagnosisCaseID); err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, `
INSERT INTO reliability_diagnosis_commands (
	tenant_id, id, diagnosis_id, operation, decision, reason, actor_kind, actor_id, created_at
) VALUES (?, ?, ?, 'decision', ?, ?, ?, ?, ?)
ON CONFLICT(tenant_id, id) DO NOTHING`, command.TenantID, command.ID, command.DiagnosisCaseID, command.Decision, command.Reason,
			command.Actor.Kind, command.Actor.ID, timeToUnixNano(updatedAt))
		if err != nil {
			return fmt.Errorf("insert diagnosis decision command: %w", err)
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("read diagnosis decision command rows affected: %w", err)
		}
		if affected == 0 {
			var diagnosisID, operation string
			var decision DiagnosisDecision
			var reason, actorKind, actorID string
			if err := tx.QueryRowContext(ctx, `SELECT diagnosis_id, operation, decision, reason, actor_kind, actor_id FROM reliability_diagnosis_commands WHERE tenant_id = ? AND id = ?`, command.TenantID, command.ID).
				Scan(&diagnosisID, &operation, &decision, &reason, &actorKind, &actorID); err != nil {
				return fmt.Errorf("load diagnosis decision command: %w", err)
			}
			if diagnosisID != command.DiagnosisCaseID || operation != "decision" || decision != command.Decision || reason != command.Reason ||
				actorKind != command.Actor.Kind || actorID != command.Actor.ID {
				return fmt.Errorf("%w: diagnosis command id was reused with different data", ErrConflict)
			}
			stored, err = loadDiagnosisCaseTx(ctx, tx, command.TenantID, diagnosisID)
			return err
		}
		stored, err = loadDiagnosisCaseTx(ctx, tx, command.TenantID, command.DiagnosisCaseID)
		if err != nil {
			return err
		}
		if stored.Status != DiagnosisCaseStatusProposed {
			return fmt.Errorf("%w: diagnosis case is already decided", ErrConflict)
		}
		if command.Decision == DiagnosisDecisionConfirm {
			var incidentVersion int64
			if err := tx.QueryRowContext(ctx, `SELECT version FROM reliability_failure_incidents WHERE tenant_id = ? AND id = ?`, command.TenantID, stored.IncidentID).Scan(&incidentVersion); err != nil {
				return err
			}
			if incidentVersion != stored.IncidentVersion {
				return fmt.Errorf("%w: diagnosis no longer matches the current incident version", ErrConflict)
			}
			stored.Status = DiagnosisCaseStatusConfirmed
		} else {
			stored.Status = DiagnosisCaseStatusRejected
		}
		expectedVersion := stored.Version
		stored.ReviewedBy = command.Actor
		stored.ReviewReason = command.Reason
		stored.Version++
		stored.UpdatedAt = updatedAt.UTC()
		result, err = tx.ExecContext(ctx, `
UPDATE reliability_diagnosis_cases SET
	status = ?, reviewed_by_kind = ?, reviewed_by_id = ?, review_reason = ?, version = ?, updated_at = ?
WHERE tenant_id = ? AND id = ? AND version = ? AND status = ?`, stored.Status, stored.ReviewedBy.Kind, stored.ReviewedBy.ID,
			stored.ReviewReason, stored.Version, timeToUnixNano(stored.UpdatedAt), stored.TenantID, stored.ID, expectedVersion, DiagnosisCaseStatusProposed)
		if err != nil {
			return fmt.Errorf("update diagnosis decision: %w", err)
		}
		affected, err = result.RowsAffected()
		if err != nil {
			return err
		}
		if affected != 1 {
			return fmt.Errorf("%w: diagnosis case version changed", ErrConflict)
		}
		changed = true
		return nil
	})
	if err != nil {
		return DiagnosisCase{}, false, err
	}
	return stored, changed, nil
}

func diagnosisCaseSelectSQL() string {
	return `SELECT tenant_id, id, incident_id, incident_version, root_cause_category, redacted_summary, confidence, status,
	proposed_by_kind, proposed_by_id, reviewed_by_kind, reviewed_by_id, review_reason, version, created_at, updated_at
FROM reliability_diagnosis_cases`
}

func scanDiagnosisCase(row rowScanner) (DiagnosisCase, error) {
	var diagnosis DiagnosisCase
	var createdAt, updatedAt int64
	err := row.Scan(&diagnosis.TenantID, &diagnosis.ID, &diagnosis.IncidentID, &diagnosis.IncidentVersion,
		&diagnosis.RootCauseCategory, &diagnosis.RedactedSummary, &diagnosis.Confidence, &diagnosis.Status,
		&diagnosis.ProposedBy.Kind, &diagnosis.ProposedBy.ID, &diagnosis.ReviewedBy.Kind, &diagnosis.ReviewedBy.ID,
		&diagnosis.ReviewReason, &diagnosis.Version, &createdAt, &updatedAt)
	if err != nil {
		return DiagnosisCase{}, err
	}
	diagnosis.CreatedAt = unixNanoToTime(createdAt)
	diagnosis.UpdatedAt = unixNanoToTime(updatedAt)
	return diagnosis, nil
}

func loadDiagnosisCaseTx(ctx context.Context, tx db.Tx, tenantID, diagnosisID string) (DiagnosisCase, error) {
	diagnosis, err := scanDiagnosisCase(tx.QueryRowContext(ctx, diagnosisCaseSelectSQL()+` WHERE tenant_id = ? AND id = ?`, tenantID, diagnosisID))
	if errors.Is(err, sql.ErrNoRows) {
		return DiagnosisCase{}, fmt.Errorf("%w: diagnosis case %s", ErrNotFound, diagnosisID)
	}
	if err != nil {
		return DiagnosisCase{}, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT evidence_id FROM reliability_diagnosis_evidence WHERE tenant_id = ? AND diagnosis_id = ? ORDER BY position`, tenantID, diagnosisID)
	if err != nil {
		return DiagnosisCase{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var evidenceID string
		if err := rows.Scan(&evidenceID); err != nil {
			return DiagnosisCase{}, err
		}
		diagnosis.EvidenceIDs = append(diagnosis.EvidenceIDs, evidenceID)
	}
	return diagnosis, rows.Err()
}

func (r *PlatformRepository) listDiagnosisEvidence(ctx context.Context, tenantID, diagnosisID string) ([]string, error) {
	rows, err := r.store.Query(ctx, `SELECT evidence_id FROM reliability_diagnosis_evidence WHERE tenant_id = ? AND diagnosis_id = ? ORDER BY position`, tenantID, diagnosisID)
	if err != nil {
		return nil, fmt.Errorf("list diagnosis evidence: %w", err)
	}
	defer rows.Close()
	var evidenceIDs []string
	for rows.Next() {
		var evidenceID string
		if err := rows.Scan(&evidenceID); err != nil {
			return nil, err
		}
		evidenceIDs = append(evidenceIDs, evidenceID)
	}
	return evidenceIDs, rows.Err()
}
