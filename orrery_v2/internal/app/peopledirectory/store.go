package peopledirectory

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"orrery_v2/internal/platform/db"
)

type Store struct {
	database *db.Store
}

func NewStore(database *db.Store) *Store {
	return &Store{database: database}
}

func (s *Store) BeginSnapshot(ctx context.Context, snapshot Snapshot) error {
	if s == nil || s.database == nil {
		return fmt.Errorf("People directory store is not configured")
	}
	snapshot.ID = strings.TrimSpace(snapshot.ID)
	snapshot.Source = strings.TrimSpace(snapshot.Source)
	snapshot.TenantID = strings.TrimSpace(snapshot.TenantID)
	if snapshot.ID == "" || snapshot.Source == "" || snapshot.TenantID == "" {
		return fmt.Errorf("snapshot id, source, and tenant id are required")
	}
	now := time.Now().UTC().UnixMilli()
	_, err := s.database.Exec(ctx, `
INSERT INTO people_directory_snapshots (id, source, tenant_id, status, complete, page_count, last_error, created_at, updated_at)
VALUES (?, ?, ?, ?, 0, 0, '', ?, ?)`, snapshot.ID, snapshot.Source, snapshot.TenantID, SnapshotStatusScanning, now, now)
	if err != nil {
		return fmt.Errorf("begin People directory snapshot: %w", err)
	}
	return nil
}

func (s *Store) StagePage(ctx context.Context, snapshotID string, users []DirectoryUser, hasMore bool) error {
	if s == nil || s.database == nil {
		return fmt.Errorf("People directory store is not configured")
	}
	snapshotID = strings.TrimSpace(snapshotID)
	normalized := make([]DirectoryUser, 0, len(users))
	for _, user := range users {
		user = user.normalized()
		if user.SourceRecordID == "" {
			return fmt.Errorf("directory user source record id is required")
		}
		normalized = append(normalized, user)
	}
	return s.database.WithTx(ctx, func(tx db.Tx) error {
		var status string
		if err := tx.QueryRowContext(ctx, `SELECT status FROM people_directory_snapshots WHERE id = ?`, snapshotID).Scan(&status); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrSnapshotNotFound
			}
			return fmt.Errorf("load People directory snapshot: %w", err)
		}
		if status != SnapshotStatusScanning {
			return fmt.Errorf("People directory snapshot is not scanning: %s", status)
		}
		now := time.Now().UTC().UnixMilli()
		for _, user := range normalized {
			active := 0
			if user.Active {
				active = 1
			}
			if _, err := tx.ExecContext(ctx, `
INSERT INTO people_directory_staged_users (
	snapshot_id, source_record_id, employee_number, name, email, enterprise_email, active, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (snapshot_id, source_record_id) DO UPDATE SET
	employee_number = excluded.employee_number,
	name = excluded.name,
	email = excluded.email,
	enterprise_email = excluded.enterprise_email,
	active = excluded.active,
	updated_at = excluded.updated_at`, snapshotID, user.SourceRecordID, user.EmployeeNumber, user.Name, user.Email, user.EnterpriseEmail, active, now, now); err != nil {
				return fmt.Errorf("stage People directory user %q: %w", user.SourceRecordID, err)
			}
		}
		complete := 0
		nextStatus := SnapshotStatusScanning
		if !hasMore {
			complete = 1
			nextStatus = SnapshotStatusComplete
		}
		if _, err := tx.ExecContext(ctx, `
UPDATE people_directory_snapshots
SET status = ?, complete = ?, page_count = page_count + 1, last_error = '', updated_at = ?
WHERE id = ?`, nextStatus, complete, now, snapshotID); err != nil {
			return fmt.Errorf("advance People directory snapshot: %w", err)
		}
		return nil
	})
}

func (s *Store) MarkSnapshotFailed(ctx context.Context, snapshotID string, cause error) error {
	if s == nil || s.database == nil {
		return fmt.Errorf("People directory store is not configured")
	}
	message := "directory scan failed"
	if cause != nil && strings.TrimSpace(cause.Error()) != "" {
		message = strings.TrimSpace(cause.Error())
	}
	if len(message) > 1000 {
		message = message[:1000]
	}
	_, err := s.database.Exec(ctx, `
UPDATE people_directory_snapshots
SET status = ?, complete = 0, last_error = ?, updated_at = ?
WHERE id = ?`, SnapshotStatusFailed, message, time.Now().UTC().UnixMilli(), strings.TrimSpace(snapshotID))
	return err
}

func (s *Store) Snapshot(ctx context.Context, snapshotID string) (Snapshot, error) {
	if s == nil || s.database == nil {
		return Snapshot{}, fmt.Errorf("People directory store is not configured")
	}
	var snapshot Snapshot
	var complete int
	err := s.database.QueryRow(ctx, `
SELECT id, source, tenant_id, status, complete, page_count, last_error
FROM people_directory_snapshots
WHERE id = ?`, strings.TrimSpace(snapshotID)).Scan(
		&snapshot.ID, &snapshot.Source, &snapshot.TenantID, &snapshot.Status, &complete, &snapshot.PageCount, &snapshot.LastError,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return Snapshot{}, ErrSnapshotNotFound
	}
	if err != nil {
		return Snapshot{}, fmt.Errorf("load People directory snapshot: %w", err)
	}
	snapshot.Complete = complete == 1
	return snapshot, nil
}

func (s *Store) StagedUsers(ctx context.Context, snapshotID string) ([]DirectoryUser, error) {
	if s == nil || s.database == nil {
		return nil, fmt.Errorf("People directory store is not configured")
	}
	rows, err := s.database.Query(ctx, `
SELECT source_record_id, employee_number, name, email, enterprise_email, active
FROM people_directory_staged_users
WHERE snapshot_id = ?
ORDER BY source_record_id`, strings.TrimSpace(snapshotID))
	if err != nil {
		return nil, fmt.Errorf("list staged People directory users: %w", err)
	}
	defer rows.Close()
	var users []DirectoryUser
	for rows.Next() {
		var user DirectoryUser
		var active int
		if err := rows.Scan(&user.SourceRecordID, &user.EmployeeNumber, &user.Name, &user.Email, &user.EnterpriseEmail, &active); err != nil {
			return nil, fmt.Errorf("scan staged People directory user: %w", err)
		}
		user.Active = active == 1
		users = append(users, user)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate staged People directory users: %w", err)
	}
	return users, nil
}

func (s *Store) SaveMatchResults(ctx context.Context, snapshotID string, results []MatchResult) error {
	if s == nil || s.database == nil {
		return fmt.Errorf("People directory store is not configured")
	}
	snapshotID = strings.TrimSpace(snapshotID)
	return s.database.WithTx(ctx, func(tx db.Tx) error {
		var complete int
		var status string
		if err := tx.QueryRowContext(ctx, `SELECT complete, status FROM people_directory_snapshots WHERE id = ?`, snapshotID).Scan(&complete, &status); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrSnapshotNotFound
			}
			return fmt.Errorf("load People directory snapshot for matching: %w", err)
		}
		if complete != 1 || status != SnapshotStatusComplete {
			return ErrSnapshotIncomplete
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM people_directory_matches WHERE snapshot_id = ?`, snapshotID); err != nil {
			return fmt.Errorf("replace People directory matches: %w", err)
		}
		now := time.Now().UTC().UnixMilli()
		for _, result := range results {
			result.SourceRecordID = strings.TrimSpace(result.SourceRecordID)
			result.ProposedPersonID = strings.TrimSpace(result.ProposedPersonID)
			result.ConflictReason = strings.TrimSpace(result.ConflictReason)
			if result.SourceRecordID == "" || result.Action == "" || result.MatchKind == "" {
				return fmt.Errorf("match source record id, action, and kind are required")
			}
			if strings.TrimSpace(result.ApplyStatus) == "" {
				result.ApplyStatus = ApplyStatusPending
				if result.Action == MatchActionConflict {
					result.ApplyStatus = ApplyStatusPendingReview
				}
			}
			if _, err := tx.ExecContext(ctx, `
INSERT INTO people_directory_matches (
	snapshot_id, source_record_id, action, match_kind, proposed_person_id, conflict_reason, apply_status, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, snapshotID, result.SourceRecordID, result.Action, result.MatchKind, result.ProposedPersonID, result.ConflictReason, result.ApplyStatus, now, now); err != nil {
				return fmt.Errorf("save People directory match %q: %w", result.SourceRecordID, err)
			}
		}
		return nil
	})
}

func (s *Store) MatchResults(ctx context.Context, snapshotID string) ([]MatchResult, error) {
	if s == nil || s.database == nil {
		return nil, fmt.Errorf("People directory store is not configured")
	}
	rows, err := s.database.Query(ctx, `
SELECT source_record_id, action, match_kind, proposed_person_id, conflict_reason, apply_status
FROM people_directory_matches
WHERE snapshot_id = ?
ORDER BY source_record_id`, strings.TrimSpace(snapshotID))
	if err != nil {
		return nil, fmt.Errorf("list People directory matches: %w", err)
	}
	defer rows.Close()
	var results []MatchResult
	for rows.Next() {
		var result MatchResult
		if err := rows.Scan(&result.SourceRecordID, &result.Action, &result.MatchKind, &result.ProposedPersonID, &result.ConflictReason, &result.ApplyStatus); err != nil {
			return nil, fmt.Errorf("scan People directory match: %w", err)
		}
		results = append(results, result)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate People directory matches: %w", err)
	}
	return results, nil
}

func (s *Store) ReservePersonID(ctx context.Context, snapshotID, sourceRecordID, personID string) error {
	if s == nil || s.database == nil {
		return fmt.Errorf("People directory store is not configured")
	}
	snapshotID = strings.TrimSpace(snapshotID)
	sourceRecordID = strings.TrimSpace(sourceRecordID)
	personID = strings.TrimSpace(personID)
	if snapshotID == "" || sourceRecordID == "" || personID == "" {
		return fmt.Errorf("snapshot id, source record id, and person id are required")
	}
	result, err := s.database.Exec(ctx, `
UPDATE people_directory_matches
SET proposed_person_id = ?, updated_at = ?
WHERE snapshot_id = ? AND source_record_id = ? AND action = ? AND apply_status != ?
  AND (proposed_person_id = '' OR proposed_person_id = ?)`,
		personID, time.Now().UTC().UnixMilli(), snapshotID, sourceRecordID, MatchActionCreate, ApplyStatusApplied, personID)
	if err != nil {
		return fmt.Errorf("reserve People directory person id: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read People directory person id reservation: %w", err)
	}
	if affected != 1 {
		return ErrMatchConflict
	}
	return nil
}

func (s *Store) MarkApplied(ctx context.Context, snapshotID, sourceRecordID, personID string) error {
	if s == nil || s.database == nil {
		return fmt.Errorf("People directory store is not configured")
	}
	snapshotID = strings.TrimSpace(snapshotID)
	sourceRecordID = strings.TrimSpace(sourceRecordID)
	personID = strings.TrimSpace(personID)
	if snapshotID == "" || sourceRecordID == "" || personID == "" {
		return fmt.Errorf("snapshot id, source record id, and person id are required")
	}
	result, err := s.database.Exec(ctx, `
UPDATE people_directory_matches
SET proposed_person_id = ?, apply_status = ?, updated_at = ?
WHERE snapshot_id = ? AND source_record_id = ? AND action != ?
  AND (proposed_person_id = '' OR proposed_person_id = ?)`,
		personID, ApplyStatusApplied, time.Now().UTC().UnixMilli(), snapshotID, sourceRecordID, MatchActionConflict, personID)
	if err != nil {
		return fmt.Errorf("mark People directory match applied: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read People directory match apply result: %w", err)
	}
	if affected != 1 {
		return ErrMatchConflict
	}
	return nil
}
