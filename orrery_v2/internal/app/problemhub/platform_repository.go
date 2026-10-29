package problemhub

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"orrery_v2/internal/platform/db"
)

type PlatformRepository struct {
	store *db.Store
}

func NewPlatformRepository(store *db.Store) *PlatformRepository {
	return &PlatformRepository{store: store}
}

func (r *PlatformRepository) Observe(ctx context.Context, observation Observation, candidate ProblemCase) (ProblemCase, bool, error) {
	if r == nil || r.store == nil {
		return ProblemCase{}, false, fmt.Errorf("%w: store is required", ErrInvalidRequest)
	}
	changed := false
	err := r.store.WithTx(ctx, func(tx db.Tx) error {
		result, err := tx.ExecContext(ctx, `
INSERT INTO problem_hub_observations (tenant_id, id, problem_case_id, source_kind, source_id, evidence_id, occurred_at)
VALUES (?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(tenant_id, id) DO NOTHING`, observation.TenantID, observation.ID, candidate.ID,
			observation.Source.Kind, observation.Source.ID, observation.EvidenceID, toUnixNano(observation.OccurredAt))
		if err != nil {
			return fmt.Errorf("insert problem observation: %w", err)
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("read problem observation rows affected: %w", err)
		}
		if affected == 0 {
			var caseID, sourceKind, sourceID, evidenceID string
			if err := tx.QueryRowContext(ctx, `SELECT problem_case_id, source_kind, source_id, evidence_id
FROM problem_hub_observations WHERE tenant_id = ? AND id = ?`, observation.TenantID, observation.ID).
				Scan(&caseID, &sourceKind, &sourceID, &evidenceID); err != nil {
				return fmt.Errorf("load problem observation: %w", err)
			}
			if caseID != candidate.ID || sourceKind != observation.Source.Kind || sourceID != observation.Source.ID || evidenceID != observation.EvidenceID {
				return fmt.Errorf("%w: observation id was reused with different problem material", ErrConflict)
			}
			return nil
		}
		changed = true
		_, err = tx.ExecContext(ctx, `
INSERT INTO problem_hub_cases (
	tenant_id, id, fingerprint, source_domain, category, severity, status, title, redacted_summary,
	owner_kind, owner_id, primary_source_kind, primary_source_id, occurrence_count,
	latest_outcome_id, latest_outcome_success, latest_outcome_summary, latest_resolution_kind,
	latest_resolution_id, lesson_candidate_id, version, first_seen_at, last_seen_at, resolved_at, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(tenant_id, fingerprint) DO UPDATE SET
	category = excluded.category,
	severity = CASE
		WHEN excluded.severity = 'critical' THEN excluded.severity
		WHEN excluded.severity = 'error' AND problem_hub_cases.severity NOT IN ('critical') THEN excluded.severity
		WHEN excluded.severity = 'warning' AND problem_hub_cases.severity NOT IN ('critical', 'error') THEN excluded.severity
		ELSE problem_hub_cases.severity
	END,
	title = CASE WHEN excluded.last_seen_at >= problem_hub_cases.last_seen_at THEN excluded.title ELSE problem_hub_cases.title END,
	redacted_summary = CASE WHEN excluded.last_seen_at >= problem_hub_cases.last_seen_at THEN excluded.redacted_summary ELSE problem_hub_cases.redacted_summary END,
	status = CASE WHEN problem_hub_cases.status = 'resolved' THEN 'open' ELSE problem_hub_cases.status END,
	latest_outcome_id = CASE WHEN problem_hub_cases.status = 'resolved' THEN '' ELSE problem_hub_cases.latest_outcome_id END,
	latest_outcome_success = CASE WHEN problem_hub_cases.status = 'resolved' THEN FALSE ELSE problem_hub_cases.latest_outcome_success END,
	latest_outcome_summary = CASE WHEN problem_hub_cases.status = 'resolved' THEN '' ELSE problem_hub_cases.latest_outcome_summary END,
	latest_resolution_kind = CASE WHEN problem_hub_cases.status = 'resolved' THEN '' ELSE problem_hub_cases.latest_resolution_kind END,
	latest_resolution_id = CASE WHEN problem_hub_cases.status = 'resolved' THEN '' ELSE problem_hub_cases.latest_resolution_id END,
	lesson_candidate_id = CASE WHEN problem_hub_cases.status = 'resolved' THEN '' ELSE problem_hub_cases.lesson_candidate_id END,
	resolved_at = CASE WHEN problem_hub_cases.status = 'resolved' THEN NULL ELSE problem_hub_cases.resolved_at END,
	first_seen_at = CASE WHEN excluded.first_seen_at < problem_hub_cases.first_seen_at THEN excluded.first_seen_at ELSE problem_hub_cases.first_seen_at END,
	last_seen_at = CASE WHEN excluded.last_seen_at > problem_hub_cases.last_seen_at THEN excluded.last_seen_at ELSE problem_hub_cases.last_seen_at END,
	occurrence_count = problem_hub_cases.occurrence_count + 1,
	version = problem_hub_cases.version + 1,
	updated_at = excluded.updated_at`, caseArgs(candidate)...)
		if err != nil {
			return fmt.Errorf("upsert problem case: %w", err)
		}
		result, err = tx.ExecContext(ctx, `
INSERT INTO problem_hub_source_bindings (tenant_id, source_kind, source_id, problem_case_id)
VALUES (?, ?, ?, ?)
ON CONFLICT(tenant_id, source_kind, source_id) DO NOTHING`, observation.TenantID, observation.Source.Kind, observation.Source.ID, candidate.ID)
		if err != nil {
			return fmt.Errorf("bind problem source: %w", err)
		}
		affected, err = result.RowsAffected()
		if err != nil {
			return fmt.Errorf("read problem source rows affected: %w", err)
		}
		if affected == 0 {
			var boundCaseID string
			if err := tx.QueryRowContext(ctx, `SELECT problem_case_id FROM problem_hub_source_bindings
WHERE tenant_id = ? AND source_kind = ? AND source_id = ?`, observation.TenantID, observation.Source.Kind, observation.Source.ID).Scan(&boundCaseID); err != nil {
				return fmt.Errorf("load problem source binding: %w", err)
			}
			if boundCaseID != candidate.ID {
				return fmt.Errorf("%w: source is already bound to another problem case", ErrConflict)
			}
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO problem_hub_evidence (tenant_id, problem_case_id, evidence_id)
VALUES (?, ?, ?) ON CONFLICT(tenant_id, problem_case_id, evidence_id) DO NOTHING`, observation.TenantID, candidate.ID, observation.EvidenceID); err != nil {
			return fmt.Errorf("attach problem evidence: %w", err)
		}
		return nil
	})
	if err != nil {
		return ProblemCase{}, false, err
	}
	problem, err := r.Get(ctx, observation.TenantID, candidate.ID)
	return problem, changed, err
}

func (r *PlatformRepository) Get(ctx context.Context, tenantID, problemCaseID string) (ProblemCase, error) {
	if r == nil || r.store == nil {
		return ProblemCase{}, fmt.Errorf("%w: store is required", ErrInvalidRequest)
	}
	problem, err := scanProblemCase(r.store.QueryRow(ctx, problemCaseSelectSQL()+` WHERE tenant_id = ? AND id = ?`, tenantID, problemCaseID))
	if errors.Is(err, sql.ErrNoRows) {
		return ProblemCase{}, fmt.Errorf("%w: %s", ErrNotFound, problemCaseID)
	}
	if err != nil {
		return ProblemCase{}, err
	}
	return r.hydrate(ctx, problem)
}

func (r *PlatformRepository) GetBySource(ctx context.Context, tenantID string, source ObjectRef) (ProblemCase, error) {
	if r == nil || r.store == nil {
		return ProblemCase{}, fmt.Errorf("%w: store is required", ErrInvalidRequest)
	}
	var problemCaseID string
	err := r.store.QueryRow(ctx, `SELECT problem_case_id FROM problem_hub_source_bindings
WHERE tenant_id = ? AND source_kind = ? AND source_id = ?`, tenantID, source.Kind, source.ID).Scan(&problemCaseID)
	if errors.Is(err, sql.ErrNoRows) {
		return ProblemCase{}, fmt.Errorf("%w: source %s:%s", ErrNotFound, source.Kind, source.ID)
	}
	if err != nil {
		return ProblemCase{}, fmt.Errorf("get problem by source: %w", err)
	}
	return r.Get(ctx, tenantID, problemCaseID)
}

func (r *PlatformRepository) List(ctx context.Context, query ListQuery) ([]ProblemCase, error) {
	if r == nil || r.store == nil {
		return nil, fmt.Errorf("%w: store is required", ErrInvalidRequest)
	}
	sqlQuery := problemCaseSelectSQL() + ` WHERE tenant_id = ?`
	args := []any{query.TenantID}
	if query.Status != "" {
		sqlQuery += ` AND status = ?`
		args = append(args, query.Status)
	}
	if query.OwnerKind != "" {
		sqlQuery += ` AND owner_kind = ?`
		args = append(args, query.OwnerKind)
	}
	if query.OwnerID != "" {
		sqlQuery += ` AND owner_id = ?`
		args = append(args, query.OwnerID)
	}
	if query.SourceDomain != "" {
		sqlQuery += ` AND source_domain = ?`
		args = append(args, query.SourceDomain)
	}
	if query.Search != "" {
		pattern := "%" + escapeProblemSearch(query.Search) + "%"
		sqlQuery += ` AND (LOWER(title) LIKE LOWER(?) ESCAPE '\' OR LOWER(redacted_summary) LIKE LOWER(?) ESCAPE '\' OR LOWER(fingerprint) LIKE LOWER(?) ESCAPE '\')`
		args = append(args, pattern, pattern, pattern)
	}
	sqlQuery += ` ORDER BY updated_at DESC, id LIMIT ?`
	args = append(args, query.Limit)
	rows, err := r.store.Query(ctx, sqlQuery, args...)
	if err != nil {
		return nil, fmt.Errorf("list problem cases: %w", err)
	}
	var problems []ProblemCase
	for rows.Next() {
		problem, scanErr := scanProblemCase(rows)
		if scanErr != nil {
			_ = rows.Close()
			return nil, scanErr
		}
		problems = append(problems, problem)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, fmt.Errorf("list problem cases: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	for index := range problems {
		problems[index], err = r.hydrate(ctx, problems[index])
		if err != nil {
			return nil, err
		}
	}
	return problems, nil
}

func (r *PlatformRepository) SyncStatusBySource(ctx context.Context, tenantID string, source ObjectRef, status Status, updatedAt time.Time) (ProblemCase, bool, error) {
	if r == nil || r.store == nil {
		return ProblemCase{}, false, fmt.Errorf("%w: store is required", ErrInvalidRequest)
	}
	var problemCaseID string
	changed := false
	err := r.store.WithTx(ctx, func(tx db.Tx) error {
		if err := tx.QueryRowContext(ctx, `SELECT problem_case_id FROM problem_hub_source_bindings
WHERE tenant_id = ? AND source_kind = ? AND source_id = ?`, tenantID, source.Kind, source.ID).Scan(&problemCaseID); errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%w: source %s:%s", ErrNotFound, source.Kind, source.ID)
		} else if err != nil {
			return err
		}
		problem, err := scanProblemCase(tx.QueryRowContext(ctx, problemCaseSelectSQL()+` WHERE tenant_id = ? AND id = ?`, tenantID, problemCaseID))
		if err != nil {
			return err
		}
		if problem.Status == status {
			return nil
		}
		resolvedAt := any(nil)
		if status == StatusResolved {
			resolvedAt = toUnixNano(updatedAt)
		}
		clearCycle := problem.Status == StatusResolved && status != StatusResolved
		result, err := tx.ExecContext(ctx, `UPDATE problem_hub_cases SET
	status = ?, resolved_at = ?,
	latest_outcome_id = CASE WHEN ? THEN '' ELSE latest_outcome_id END,
	latest_outcome_success = CASE WHEN ? THEN FALSE ELSE latest_outcome_success END,
	latest_outcome_summary = CASE WHEN ? THEN '' ELSE latest_outcome_summary END,
	latest_resolution_kind = CASE WHEN ? THEN '' ELSE latest_resolution_kind END,
	latest_resolution_id = CASE WHEN ? THEN '' ELSE latest_resolution_id END,
	lesson_candidate_id = CASE WHEN ? THEN '' ELSE lesson_candidate_id END,
	version = version + 1, updated_at = ?
WHERE tenant_id = ? AND id = ? AND version = ?`, status, resolvedAt,
			clearCycle, clearCycle, clearCycle, clearCycle, clearCycle, clearCycle,
			toUnixNano(updatedAt), tenantID, problemCaseID, problem.Version)
		if err != nil {
			return fmt.Errorf("sync problem status: %w", err)
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if affected == 0 {
			return fmt.Errorf("%w: problem case version changed", ErrConflict)
		}
		changed = true
		return nil
	})
	if err != nil {
		return ProblemCase{}, false, err
	}
	problem, err := r.Get(ctx, tenantID, problemCaseID)
	return problem, changed, err
}

func (r *PlatformRepository) RecordOutcome(ctx context.Context, outcome Outcome, updatedAt time.Time) (ProblemCase, bool, error) {
	if r == nil || r.store == nil {
		return ProblemCase{}, false, fmt.Errorf("%w: store is required", ErrInvalidRequest)
	}
	problemCaseID := outcome.ProblemCaseID
	changed := false
	err := r.store.WithTx(ctx, func(tx db.Tx) error {
		if problemCaseID == "" {
			if err := tx.QueryRowContext(ctx, `SELECT problem_case_id FROM problem_hub_source_bindings
WHERE tenant_id = ? AND source_kind = ? AND source_id = ?`, outcome.TenantID, outcome.Source.Kind, outcome.Source.ID).Scan(&problemCaseID); errors.Is(err, sql.ErrNoRows) {
				return fmt.Errorf("%w: source %s:%s", ErrNotFound, outcome.Source.Kind, outcome.Source.ID)
			} else if err != nil {
				return err
			}
		}
		result, err := tx.ExecContext(ctx, `INSERT INTO problem_hub_outcomes (
	tenant_id, id, problem_case_id, success, resolution_kind, resolution_id, redacted_summary,
	evidence_id, actor_kind, actor_id, occurred_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(tenant_id, id) DO NOTHING`, outcome.TenantID, outcome.ID, problemCaseID, outcome.Success,
			outcome.Resolution.Kind, outcome.Resolution.ID, outcome.RedactedSummary, outcome.EvidenceID,
			outcome.Actor.Kind, outcome.Actor.ID, toUnixNano(outcome.OccurredAt))
		if err != nil {
			return fmt.Errorf("insert problem outcome: %w", err)
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if affected == 0 {
			var existingCaseID, resolutionKind, resolutionID, summary, evidenceID string
			var success bool
			if err := tx.QueryRowContext(ctx, `SELECT problem_case_id, success, resolution_kind, resolution_id, redacted_summary, evidence_id
FROM problem_hub_outcomes WHERE tenant_id = ? AND id = ?`, outcome.TenantID, outcome.ID).
				Scan(&existingCaseID, &success, &resolutionKind, &resolutionID, &summary, &evidenceID); err != nil {
				return err
			}
			if existingCaseID != problemCaseID || success != outcome.Success || resolutionKind != outcome.Resolution.Kind ||
				resolutionID != outcome.Resolution.ID || summary != outcome.RedactedSummary || evidenceID != outcome.EvidenceID {
				return fmt.Errorf("%w: outcome id was reused with different material", ErrConflict)
			}
			return nil
		}
		changed = true
		result, err = tx.ExecContext(ctx, `UPDATE problem_hub_cases SET
	latest_outcome_id = ?, latest_outcome_success = ?, latest_outcome_summary = ?,
	latest_resolution_kind = ?, latest_resolution_id = ?, version = version + 1, updated_at = ?
WHERE tenant_id = ? AND id = ?`, outcome.ID, outcome.Success, outcome.RedactedSummary,
			outcome.Resolution.Kind, outcome.Resolution.ID, toUnixNano(updatedAt), outcome.TenantID, problemCaseID)
		if err != nil {
			return fmt.Errorf("update problem outcome: %w", err)
		}
		affected, err = result.RowsAffected()
		if err != nil {
			return err
		}
		if affected == 0 {
			return fmt.Errorf("%w: %s", ErrNotFound, problemCaseID)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO problem_hub_evidence (tenant_id, problem_case_id, evidence_id)
VALUES (?, ?, ?) ON CONFLICT(tenant_id, problem_case_id, evidence_id) DO NOTHING`, outcome.TenantID, problemCaseID, outcome.EvidenceID); err != nil {
			return fmt.Errorf("attach outcome evidence: %w", err)
		}
		return nil
	})
	if err != nil {
		return ProblemCase{}, false, err
	}
	problem, err := r.Get(ctx, outcome.TenantID, problemCaseID)
	return problem, changed, err
}

func (r *PlatformRepository) Route(ctx context.Context, command RouteCommand, updatedAt time.Time) (ProblemCase, bool, error) {
	if r == nil || r.store == nil {
		return ProblemCase{}, false, fmt.Errorf("%w: store is required", ErrInvalidRequest)
	}
	changed := false
	err := r.store.WithTx(ctx, func(tx db.Tx) error {
		result, err := tx.ExecContext(ctx, `INSERT INTO problem_hub_route_commands
(tenant_id, id, problem_case_id, owner_kind, owner_id, occurred_at) VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT(tenant_id, id) DO NOTHING`, command.TenantID, command.ID, command.ProblemCaseID,
			command.Owner.Kind, command.Owner.ID, toUnixNano(command.OccurredAt))
		if err != nil {
			return fmt.Errorf("insert problem route command: %w", err)
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if affected == 0 {
			var caseID, ownerKind, ownerID string
			if err := tx.QueryRowContext(ctx, `SELECT problem_case_id, owner_kind, owner_id FROM problem_hub_route_commands
WHERE tenant_id = ? AND id = ?`, command.TenantID, command.ID).Scan(&caseID, &ownerKind, &ownerID); err != nil {
				return err
			}
			if caseID != command.ProblemCaseID || ownerKind != command.Owner.Kind || ownerID != command.Owner.ID {
				return fmt.Errorf("%w: route command id was reused", ErrConflict)
			}
			return nil
		}
		problem, err := scanProblemCase(tx.QueryRowContext(ctx, problemCaseSelectSQL()+` WHERE tenant_id = ? AND id = ?`, command.TenantID, command.ProblemCaseID))
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%w: %s", ErrNotFound, command.ProblemCaseID)
		}
		if err != nil {
			return err
		}
		if problem.Owner == command.Owner {
			return nil
		}
		if _, err := tx.ExecContext(ctx, `UPDATE problem_hub_cases SET owner_kind = ?, owner_id = ?, version = version + 1, updated_at = ?
WHERE tenant_id = ? AND id = ?`, command.Owner.Kind, command.Owner.ID, toUnixNano(updatedAt), command.TenantID, command.ProblemCaseID); err != nil {
			return fmt.Errorf("route problem case: %w", err)
		}
		changed = true
		return nil
	})
	if err != nil {
		return ProblemCase{}, false, err
	}
	problem, err := r.Get(ctx, command.TenantID, command.ProblemCaseID)
	return problem, changed, err
}

func (r *PlatformRepository) SetLessonCandidate(ctx context.Context, tenantID, problemCaseID string, expectedVersion int64, candidateID string, updatedAt time.Time) (ProblemCase, bool, error) {
	if r == nil || r.store == nil {
		return ProblemCase{}, false, fmt.Errorf("%w: store is required", ErrInvalidRequest)
	}
	current, err := r.Get(ctx, tenantID, problemCaseID)
	if err != nil {
		return ProblemCase{}, false, err
	}
	if current.LessonCandidateID == candidateID {
		return current, false, nil
	}
	if current.LessonCandidateID != "" {
		return ProblemCase{}, false, fmt.Errorf("%w: problem case already has a lesson candidate", ErrConflict)
	}
	result, err := r.store.Exec(ctx, `UPDATE problem_hub_cases SET lesson_candidate_id = ?, version = version + 1, updated_at = ?
WHERE tenant_id = ? AND id = ? AND version = ? AND lesson_candidate_id = ''`, candidateID, toUnixNano(updatedAt), tenantID, problemCaseID, expectedVersion)
	if err != nil {
		return ProblemCase{}, false, fmt.Errorf("set problem lesson candidate: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return ProblemCase{}, false, err
	}
	if affected == 0 {
		latest, getErr := r.Get(ctx, tenantID, problemCaseID)
		if getErr == nil && latest.LessonCandidateID == candidateID {
			return latest, false, nil
		}
		return ProblemCase{}, false, fmt.Errorf("%w: problem case version changed", ErrConflict)
	}
	updated, err := r.Get(ctx, tenantID, problemCaseID)
	return updated, true, err
}

func (r *PlatformRepository) hydrate(ctx context.Context, problem ProblemCase) (ProblemCase, error) {
	rows, err := r.store.Query(ctx, `SELECT source_kind, source_id FROM problem_hub_source_bindings
WHERE tenant_id = ? AND problem_case_id = ? ORDER BY source_kind, source_id`, problem.TenantID, problem.ID)
	if err != nil {
		return ProblemCase{}, fmt.Errorf("load problem sources: %w", err)
	}
	for rows.Next() {
		var ref ObjectRef
		if err := rows.Scan(&ref.Kind, &ref.ID); err != nil {
			_ = rows.Close()
			return ProblemCase{}, err
		}
		problem.SourceRefs = append(problem.SourceRefs, ref)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return ProblemCase{}, err
	}
	if err := rows.Close(); err != nil {
		return ProblemCase{}, err
	}
	evidenceRows, err := r.store.Query(ctx, `SELECT evidence_id FROM problem_hub_evidence
WHERE tenant_id = ? AND problem_case_id = ? ORDER BY evidence_id`, problem.TenantID, problem.ID)
	if err != nil {
		return ProblemCase{}, fmt.Errorf("load problem evidence: %w", err)
	}
	for evidenceRows.Next() {
		var evidenceID string
		if err := evidenceRows.Scan(&evidenceID); err != nil {
			_ = evidenceRows.Close()
			return ProblemCase{}, err
		}
		problem.EvidenceIDs = append(problem.EvidenceIDs, evidenceID)
	}
	if err := evidenceRows.Err(); err != nil {
		_ = evidenceRows.Close()
		return ProblemCase{}, err
	}
	if err := evidenceRows.Close(); err != nil {
		return ProblemCase{}, err
	}
	return problem, nil
}

func problemCaseSelectSQL() string {
	return `SELECT tenant_id, id, fingerprint, source_domain, category, severity, status, title, redacted_summary,
	owner_kind, owner_id, primary_source_kind, primary_source_id, occurrence_count,
	latest_outcome_id, latest_outcome_success, latest_outcome_summary, latest_resolution_kind,
	latest_resolution_id, lesson_candidate_id, version, first_seen_at, last_seen_at, resolved_at, created_at, updated_at
FROM problem_hub_cases`
}

type rowScanner interface {
	Scan(...any) error
}

func scanProblemCase(scanner rowScanner) (ProblemCase, error) {
	var problem ProblemCase
	var firstSeenAt, lastSeenAt, createdAt, updatedAt int64
	var resolvedAt sql.NullInt64
	err := scanner.Scan(
		&problem.TenantID, &problem.ID, &problem.Fingerprint, &problem.SourceDomain, &problem.Category,
		&problem.Severity, &problem.Status, &problem.Title, &problem.RedactedSummary,
		&problem.Owner.Kind, &problem.Owner.ID, &problem.PrimarySource.Kind, &problem.PrimarySource.ID,
		&problem.OccurrenceCount, &problem.LatestOutcomeID, &problem.LatestOutcomeSuccess,
		&problem.LatestOutcomeSummary, &problem.LatestResolution.Kind, &problem.LatestResolution.ID,
		&problem.LessonCandidateID, &problem.Version, &firstSeenAt, &lastSeenAt, &resolvedAt, &createdAt, &updatedAt,
	)
	if err != nil {
		return ProblemCase{}, err
	}
	problem.FirstSeenAt, problem.LastSeenAt = fromUnixNano(firstSeenAt), fromUnixNano(lastSeenAt)
	problem.CreatedAt, problem.UpdatedAt = fromUnixNano(createdAt), fromUnixNano(updatedAt)
	if resolvedAt.Valid {
		value := fromUnixNano(resolvedAt.Int64)
		problem.ResolvedAt = &value
	}
	return problem, nil
}

func caseArgs(problem ProblemCase) []any {
	return []any{
		problem.TenantID, problem.ID, problem.Fingerprint, problem.SourceDomain, problem.Category,
		problem.Severity, problem.Status, problem.Title, problem.RedactedSummary,
		problem.Owner.Kind, problem.Owner.ID, problem.PrimarySource.Kind, problem.PrimarySource.ID,
		problem.OccurrenceCount, problem.LatestOutcomeID, problem.LatestOutcomeSuccess,
		problem.LatestOutcomeSummary, problem.LatestResolution.Kind, problem.LatestResolution.ID,
		problem.LessonCandidateID, problem.Version, toUnixNano(problem.FirstSeenAt), toUnixNano(problem.LastSeenAt),
		optionalUnixNano(problem.ResolvedAt), toUnixNano(problem.CreatedAt), toUnixNano(problem.UpdatedAt),
	}
}

func toUnixNano(value time.Time) int64 {
	return value.UTC().UnixNano()
}

func optionalUnixNano(value *time.Time) any {
	if value == nil || value.IsZero() {
		return nil
	}
	return toUnixNano(*value)
}

func fromUnixNano(value int64) time.Time {
	return time.Unix(0, value).UTC()
}

var _ Repository = (*PlatformRepository)(nil)

func escapeProblemSearch(value string) string {
	replacer := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return replacer.Replace(strings.TrimSpace(value))
}
