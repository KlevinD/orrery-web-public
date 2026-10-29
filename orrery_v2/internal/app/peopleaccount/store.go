package peopleaccount

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"orrery_v2/internal/app/identity"
	"orrery_v2/internal/platform/db"
)

type Store struct {
	database *db.Store
}

const projectionClaimTimeout = 5 * time.Minute

func (s *Store) ClaimLatest(ctx context.Context, personID string, revision int64) (bool, error) {
	if s == nil || s.database == nil {
		return false, fmt.Errorf("people account projection store is not configured")
	}
	claimed := false
	err := s.database.WithTx(ctx, func(tx db.Tx) error {
		now := time.Now().UTC()
		result, err := tx.ExecContext(ctx, `
UPDATE people_account_projection_state
SET status = ?, updated_at = ?
WHERE person_id = ? AND revision = ?
  AND (status IN (?, ?) OR (status = ? AND updated_at <= ?))`,
			ProjectionStatusProcessing, now.UnixMilli(), strings.TrimSpace(personID), revision,
			ProjectionStatusPending, ProjectionStatusFailed, ProjectionStatusProcessing, now.Add(-projectionClaimTimeout).UnixMilli())
		if err != nil {
			return fmt.Errorf("claim People account projection: %w", err)
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("read People account projection claim: %w", err)
		}
		claimed = affected == 1
		if !claimed {
			return nil
		}
		_, err = tx.ExecContext(ctx, `
UPDATE people_account_projection_outbox
SET status = ?, updated_at = ?
WHERE person_id = ? AND revision = ?`, ProjectionStatusProcessing, now.UnixMilli(), strings.TrimSpace(personID), revision)
		if err != nil {
			return fmt.Errorf("claim People account projection outbox: %w", err)
		}
		return nil
	})
	return claimed, err
}

func (s *Store) MarkApplied(ctx context.Context, personID string, revision int64, identityID string) error {
	if s == nil || s.database == nil {
		return fmt.Errorf("people account projection store is not configured")
	}
	now := time.Now().UTC().UnixMilli()
	return s.database.WithTx(ctx, func(tx db.Tx) error {
		result, err := tx.ExecContext(ctx, `
UPDATE people_account_projection_state
SET status = ?, applied_revision = ?, kratos_identity_id = ?, last_error = '', updated_at = ?
WHERE person_id = ? AND revision = ?`, ProjectionStatusApplied, revision, strings.TrimSpace(identityID), now, strings.TrimSpace(personID), revision)
		if err != nil {
			return fmt.Errorf("mark People account projection applied: %w", err)
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("read People account projection apply result: %w", err)
		}
		if affected != 1 {
			return ErrProjectionConflict
		}
		_, err = tx.ExecContext(ctx, `
UPDATE people_account_projection_outbox
SET status = ?, last_error = '', updated_at = ?
WHERE person_id = ? AND revision <= ?`, ProjectionStatusApplied, now, strings.TrimSpace(personID), revision)
		return err
	})
}

func (s *Store) MarkFailed(ctx context.Context, personID string, revision int64, projectionErr error) error {
	if s == nil || s.database == nil {
		return fmt.Errorf("people account projection store is not configured")
	}
	message := "projection failed"
	if projectionErr != nil {
		message = strings.TrimSpace(projectionErr.Error())
	}
	if len(message) > 1000 {
		message = message[:1000]
	}
	now := time.Now().UTC().UnixMilli()
	return s.database.WithTx(ctx, func(tx db.Tx) error {
		if _, err := tx.ExecContext(ctx, `
UPDATE people_account_projection_state
SET status = ?, last_error = ?, updated_at = ?
WHERE person_id = ? AND revision = ?`, ProjectionStatusFailed, message, now, strings.TrimSpace(personID), revision); err != nil {
			return fmt.Errorf("mark People account projection failed: %w", err)
		}
		_, err := tx.ExecContext(ctx, `
UPDATE people_account_projection_outbox
SET status = ?, attempts = attempts + 1, last_error = ?, updated_at = ?
WHERE person_id = ? AND revision = ?`, ProjectionStatusFailed, message, now, strings.TrimSpace(personID), revision)
		return err
	})
}

func (s *Store) PendingPersonIDs(ctx context.Context, limit int) ([]string, error) {
	if s == nil || s.database == nil {
		return nil, fmt.Errorf("people account projection store is not configured")
	}
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	staleBefore := time.Now().UTC().Add(-projectionClaimTimeout).UnixMilli()
	rows, err := s.database.Query(ctx, `
SELECT person_id
FROM people_account_projection_state
WHERE status IN (?, ?) OR (status = ? AND updated_at <= ?)
ORDER BY updated_at, person_id
LIMIT ?`, ProjectionStatusPending, ProjectionStatusFailed, ProjectionStatusProcessing, staleBefore, limit)
	if err != nil {
		return nil, fmt.Errorf("list pending People account projections: %w", err)
	}
	defer rows.Close()
	var personIDs []string
	for rows.Next() {
		var personID string
		if err := rows.Scan(&personID); err != nil {
			return nil, fmt.Errorf("scan pending People account projection: %w", err)
		}
		personIDs = append(personIDs, personID)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate pending People account projections: %w", err)
	}
	return personIDs, nil
}

func NewStore(database *db.Store) *Store {
	return &Store{database: database}
}

func (s *Store) LatestProjection(ctx context.Context, personID string) (Projection, error) {
	state, err := s.ProjectionState(ctx, personID)
	return state.Projection, err
}

func (s *Store) ProjectionState(ctx context.Context, personID string) (ProjectionState, error) {
	if s == nil || s.database == nil {
		return ProjectionState{}, fmt.Errorf("people account projection store is not configured")
	}
	personID = strings.TrimSpace(personID)
	var payload, status, identityID, lastError string
	var appliedRevision int64
	err := s.database.QueryRow(ctx, `
SELECT payload_json, status, applied_revision, kratos_identity_id, last_error
FROM people_account_projection_state
WHERE person_id = ?`, personID).Scan(&payload, &status, &appliedRevision, &identityID, &lastError)
	if errors.Is(err, sql.ErrNoRows) {
		return ProjectionState{}, ErrProjectionNotFound
	}
	if err != nil {
		return ProjectionState{}, fmt.Errorf("load People account projection: %w", err)
	}
	var projection Projection
	if err := json.Unmarshal([]byte(payload), &projection); err != nil {
		return ProjectionState{}, fmt.Errorf("decode People account projection: %w", err)
	}
	return ProjectionState{
		Projection:       projection,
		Status:           status,
		AppliedRevision:  appliedRevision,
		KratosIdentityID: identityID,
		LastError:        lastError,
	}, nil
}

func (s *Store) ValidatePersonAccess(ctx context.Context, personID string, revision int64) error {
	state, err := s.ProjectionState(ctx, personID)
	if err != nil {
		if errors.Is(err, ErrProjectionNotFound) {
			return identity.ErrUnauthorized
		}
		return err
	}
	if !state.Projection.Enabled || state.Projection.Revision != revision || state.Status != ProjectionStatusApplied || state.AppliedRevision != revision {
		return identity.ErrUnauthorized
	}
	return nil
}
