package peopleingest

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"orrery_v2/internal/platform/db"
)

const (
	batchStatusPending  = "pending"
	batchStatusComplete = "completed"
	maximumStoredError  = 1000
)

type Store struct {
	db *db.Store
}

type BatchClaim struct {
	TenantID       string
	Stream         string
	BatchID        string
	LeaseToken     string
	RecordedCount  int
	ProjectedCount int
	Completed      bool
}

func NewStore(store *db.Store) *Store {
	return &Store{db: store}
}

func (store *Store) ClaimBatch(
	ctx context.Context,
	batch DeliveryBatch,
	requestSHA256 string,
	leaseToken string,
	now time.Time,
	leaseDuration time.Duration,
) (BatchClaim, error) {
	if store == nil || store.db == nil {
		return BatchClaim{}, errors.New("People ingestion store is required")
	}
	if requestSHA256 == "" || leaseToken == "" || leaseDuration <= 0 {
		return BatchClaim{}, errors.New("People ingestion claim is invalid")
	}
	nowMillis := now.UTC().UnixMilli()
	expiresMillis := now.Add(leaseDuration).UTC().UnixMilli()
	claim := BatchClaim{TenantID: batch.TenantID, Stream: batch.Stream, BatchID: batch.BatchID}
	err := store.db.WithTx(ctx, func(tx db.Tx) error {
		if _, err := tx.ExecContext(ctx, `
INSERT INTO people_ingestion_batches (
	tenant_id, stream, batch_id, request_sha256, first_cursor, last_cursor,
	event_count, status, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (tenant_id, stream, batch_id) DO NOTHING`,
			batch.TenantID, batch.Stream, batch.BatchID, requestSHA256, batch.FirstCursor, batch.LastCursor,
			len(batch.Events), batchStatusPending, nowMillis, nowMillis,
		); err != nil {
			return fmt.Errorf("create People ingestion batch: %w", err)
		}
		var storedHash, firstCursor, lastCursor, status, currentLease string
		var eventCount, recordedCount, projectedCount int
		var leaseExpiresAt int64
		err := tx.QueryRowContext(ctx, `
SELECT request_sha256, first_cursor, last_cursor, event_count, recorded_count,
	projected_count, status, lease_token, lease_expires_at
FROM people_ingestion_batches
WHERE tenant_id = ? AND stream = ? AND batch_id = ?`,
			batch.TenantID, batch.Stream, batch.BatchID,
		).Scan(&storedHash, &firstCursor, &lastCursor, &eventCount, &recordedCount, &projectedCount, &status, &currentLease, &leaseExpiresAt)
		if err != nil {
			return fmt.Errorf("read People ingestion batch: %w", err)
		}
		if storedHash != requestSHA256 || firstCursor != batch.FirstCursor || lastCursor != batch.LastCursor || eventCount != len(batch.Events) {
			return fmt.Errorf("%w: batch_id was used for a different delivery", ErrConflict)
		}
		claim.RecordedCount = recordedCount
		claim.ProjectedCount = projectedCount
		if status == batchStatusComplete {
			claim.Completed = true
			return nil
		}
		if status != batchStatusPending {
			return fmt.Errorf("%w: unsupported batch status %q", ErrConflict, status)
		}
		if currentLease != "" && currentLease != leaseToken && leaseExpiresAt > nowMillis {
			return ErrBusy
		}
		result, err := tx.ExecContext(ctx, `
UPDATE people_ingestion_batches
SET lease_token = ?, lease_expires_at = ?, updated_at = ?, last_error = ''
WHERE tenant_id = ? AND stream = ? AND batch_id = ? AND status = ?`,
			leaseToken, expiresMillis, nowMillis, batch.TenantID, batch.Stream, batch.BatchID, batchStatusPending,
		)
		if err != nil {
			return fmt.Errorf("claim People ingestion batch: %w", err)
		}
		if affected, _ := result.RowsAffected(); affected != 1 {
			return ErrBusy
		}
		claim.LeaseToken = leaseToken
		return nil
	})
	return claim, err
}

func (store *Store) RecordEvent(
	ctx context.Context,
	claim BatchClaim,
	index int,
	event DeliveryEvent,
	eventJSON []byte,
	eventSHA256 string,
	now time.Time,
	leaseDuration time.Duration,
) error {
	return store.withClaim(ctx, claim, now, leaseDuration, func(tx db.Tx, nowMillis, expiresMillis int64) error {
		var storedHash string
		var projectedAt int64
		err := tx.QueryRowContext(ctx, `
SELECT event_sha256, projected_at
FROM people_ingestion_events
WHERE tenant_id = ? AND event_id = ?`, event.TenantID, event.EventID).Scan(&storedHash, &projectedAt)
		switch {
		case err == nil:
			if storedHash != eventSHA256 {
				return fmt.Errorf("%w: event_id %q was used for a different event", ErrConflict, event.EventID)
			}
		case errors.Is(err, sql.ErrNoRows):
			if _, err := tx.ExecContext(ctx, `
INSERT INTO people_ingestion_events (
	event_id, tenant_id, stream, batch_id, event_index, event_sha256, event_json, recorded_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
				event.EventID, event.TenantID, claim.Stream, claim.BatchID, index, eventSHA256, string(eventJSON), nowMillis,
			); err != nil {
				return fmt.Errorf("record People source event: %w", err)
			}
		default:
			return fmt.Errorf("read People source event: %w", err)
		}
		return store.advanceBatch(ctx, tx, claim, "recorded_count", index+1, nowMillis, expiresMillis)
	})
}

func (store *Store) EventProjected(ctx context.Context, eventID, tenantID, eventSHA256 string) (bool, error) {
	if store == nil || store.db == nil {
		return false, errors.New("People ingestion store is required")
	}
	var storedHash string
	var projectedAt int64
	err := store.db.QueryRow(ctx, `
SELECT event_sha256, projected_at
FROM people_ingestion_events
WHERE tenant_id = ? AND event_id = ?`, tenantID, eventID).Scan(&storedHash, &projectedAt)
	if err != nil {
		return false, fmt.Errorf("read People source event projection state: %w", err)
	}
	if storedHash != eventSHA256 {
		return false, fmt.Errorf("%w: event_id %q was used for a different event", ErrConflict, eventID)
	}
	return projectedAt > 0, nil
}

func (store *Store) MarkProjected(
	ctx context.Context,
	claim BatchClaim,
	index int,
	tenantID string,
	eventID string,
	now time.Time,
	leaseDuration time.Duration,
) error {
	return store.withClaim(ctx, claim, now, leaseDuration, func(tx db.Tx, nowMillis, expiresMillis int64) error {
		if _, err := tx.ExecContext(ctx, `
UPDATE people_ingestion_events
SET projected_at = CASE WHEN projected_at = 0 THEN ? ELSE projected_at END
WHERE tenant_id = ? AND event_id = ?`, nowMillis, tenantID, eventID); err != nil {
			return fmt.Errorf("mark People source event projected: %w", err)
		}
		return store.advanceBatch(ctx, tx, claim, "projected_count", index+1, nowMillis, expiresMillis)
	})
}

func (store *Store) CompleteBatch(ctx context.Context, claim BatchClaim, eventCount int, now time.Time) error {
	if store == nil || store.db == nil {
		return errors.New("People ingestion store is required")
	}
	result, err := store.db.Exec(ctx, `
UPDATE people_ingestion_batches
SET status = ?, lease_token = '', lease_expires_at = 0, last_error = '', completed_at = ?, updated_at = ?
WHERE tenant_id = ? AND stream = ? AND batch_id = ? AND lease_token = ?
	AND recorded_count = ? AND projected_count = ? AND event_count = ?`,
		batchStatusComplete, now.UTC().UnixMilli(), now.UTC().UnixMilli(),
		claim.TenantID, claim.Stream, claim.BatchID, claim.LeaseToken,
		eventCount, eventCount, eventCount,
	)
	if err != nil {
		return fmt.Errorf("complete People ingestion batch: %w", err)
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return ErrBusy
	}
	return nil
}

func (store *Store) ReleaseBatch(ctx context.Context, claim BatchClaim, cause error, now time.Time) error {
	if store == nil || store.db == nil || claim.LeaseToken == "" {
		return nil
	}
	message := ""
	if cause != nil {
		message = strings.TrimSpace(cause.Error())
		if len(message) > maximumStoredError {
			message = message[:maximumStoredError]
		}
	}
	_, err := store.db.Exec(ctx, `
UPDATE people_ingestion_batches
SET lease_token = '', lease_expires_at = 0, last_error = ?, updated_at = ?
WHERE tenant_id = ? AND stream = ? AND batch_id = ? AND lease_token = ? AND status = ?`,
		message, now.UTC().UnixMilli(), claim.TenantID, claim.Stream, claim.BatchID, claim.LeaseToken, batchStatusPending,
	)
	return err
}

func (store *Store) withClaim(
	ctx context.Context,
	claim BatchClaim,
	now time.Time,
	leaseDuration time.Duration,
	fn func(db.Tx, int64, int64) error,
) error {
	if store == nil || store.db == nil || claim.LeaseToken == "" || fn == nil {
		return errors.New("People ingestion claim is invalid")
	}
	nowMillis := now.UTC().UnixMilli()
	expiresMillis := now.Add(leaseDuration).UTC().UnixMilli()
	return store.db.WithTx(ctx, func(tx db.Tx) error {
		var status, leaseToken string
		var leaseExpiresAt int64
		err := tx.QueryRowContext(ctx, `
SELECT status, lease_token, lease_expires_at
FROM people_ingestion_batches
WHERE tenant_id = ? AND stream = ? AND batch_id = ?`,
			claim.TenantID, claim.Stream, claim.BatchID,
		).Scan(&status, &leaseToken, &leaseExpiresAt)
		if err != nil {
			return fmt.Errorf("read People ingestion claim: %w", err)
		}
		if status != batchStatusPending || leaseToken != claim.LeaseToken || leaseExpiresAt <= nowMillis {
			return ErrBusy
		}
		return fn(tx, nowMillis, expiresMillis)
	})
}

func (store *Store) advanceBatch(
	ctx context.Context,
	tx db.Tx,
	claim BatchClaim,
	column string,
	count int,
	nowMillis int64,
	expiresMillis int64,
) error {
	if column != "recorded_count" && column != "projected_count" {
		return errors.New("unsupported People ingestion progress column")
	}
	query := `UPDATE people_ingestion_batches
SET ` + column + ` = CASE WHEN ` + column + ` < ? THEN ? ELSE ` + column + ` END,
	lease_expires_at = ?, updated_at = ?
WHERE tenant_id = ? AND stream = ? AND batch_id = ? AND lease_token = ? AND status = ?`
	result, err := tx.ExecContext(ctx, query,
		count, count, expiresMillis, nowMillis,
		claim.TenantID, claim.Stream, claim.BatchID, claim.LeaseToken, batchStatusPending,
	)
	if err != nil {
		return fmt.Errorf("advance People ingestion batch: %w", err)
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return ErrBusy
	}
	return nil
}
