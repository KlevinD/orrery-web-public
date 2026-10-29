package ontology

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"orrery_v2/internal/platform/db"
)

type MappingProjectionRefreshState string

const (
	MappingProjectionRefreshPending   MappingProjectionRefreshState = "pending"
	MappingProjectionRefreshSucceeded MappingProjectionRefreshState = "succeeded"
	MappingProjectionRefreshAbandoned MappingProjectionRefreshState = "abandoned"
)

type MappingProjectionRefreshRecord struct {
	ReceiptID     string
	TenantID      string
	State         MappingProjectionRefreshState
	Attempts      int
	LastErrorCode string
	NextAttemptAt time.Time
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

type MappingProjectionRefreshRetryReceipt struct {
	Status           MappingProjectionRefreshState
	IdempotentReplay bool
	Requeued         bool
}

type MappingProjectionRefreshStore interface {
	EnqueueMappingProjectionRefresh(context.Context, string, string) error
	GetMappingProjectionRefresh(context.Context, string) (MappingProjectionRefreshRecord, error)
	ListPendingMappingProjectionRefreshes(context.Context, time.Time, int) ([]MappingProjectionRefreshRecord, error)
	RecordMappingProjectionRefreshFailure(context.Context, string, string, int, time.Time, int) error
	CompleteMappingProjectionRefresh(context.Context, string) error
}

// MappingProjectionRevisionReader exposes the durable mapping-change epoch used
// by each server process to keep its in-memory projection registry current.
type MappingProjectionRevisionReader interface {
	MappingProjectionRevision(context.Context) (int64, error)
}

type PlatformMappingProjectionRefreshStore struct {
	store *db.Store
	tx    db.Tx
	now   func() time.Time
}

func NewPlatformMappingProjectionRefreshStore(store *db.Store) *PlatformMappingProjectionRefreshStore {
	return &PlatformMappingProjectionRefreshStore{store: store, now: time.Now}
}

func NewPlatformMappingProjectionRefreshStoreTx(tx db.Tx) *PlatformMappingProjectionRefreshStore {
	return &PlatformMappingProjectionRefreshStore{tx: tx, now: time.Now}
}

func (s *PlatformMappingProjectionRefreshStore) ready() error {
	if s == nil || s.store == nil && s.tx == nil {
		return fmt.Errorf("%w: mapping projection refresh store is required", ErrInvalidDefinition)
	}
	return nil
}

func (s *PlatformMappingProjectionRefreshStore) exec(ctx context.Context, query string, args ...any) (sql.Result, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	if s.tx != nil {
		return s.tx.ExecContext(ctx, query, args...)
	}
	return s.store.Exec(ctx, query, args...)
}

func (s *PlatformMappingProjectionRefreshStore) queryRow(ctx context.Context, query string, args ...any) rowScanner {
	if s.tx != nil {
		return s.tx.QueryRowContext(ctx, query, args...)
	}
	return s.store.QueryRow(ctx, query, args...)
}

func (s *PlatformMappingProjectionRefreshStore) EnqueueMappingProjectionRefresh(ctx context.Context, receiptID, tenantID string) error {
	if err := s.ready(); err != nil {
		return err
	}
	receiptID, tenantID = strings.TrimSpace(receiptID), strings.TrimSpace(tenantID)
	if receiptID == "" || tenantID == "" || len(receiptID) > 256 || len(tenantID) > 256 {
		return fmt.Errorf("%w: mapping projection refresh receipt and tenant are required", ErrInvalidDefinition)
	}
	now := s.now().UTC()
	result, err := s.exec(ctx, `
INSERT INTO ontology_mapping_projection_refreshes (
	receipt_id, tenant_id, state, attempts, last_error_code, next_attempt_at, created_at, updated_at
) VALUES (?, ?, ?, 0, '', ?, ?, ?)
ON CONFLICT(receipt_id) DO NOTHING`, receiptID, tenantID, MappingProjectionRefreshPending, timeToUnixNano(now), timeToUnixNano(now), timeToUnixNano(now))
	if err != nil {
		return fmt.Errorf("enqueue mapping projection refresh: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		existing, getErr := s.GetMappingProjectionRefresh(ctx, receiptID)
		if getErr != nil {
			return getErr
		}
		if existing.TenantID != tenantID {
			return fmt.Errorf("%w: mapping projection refresh receipt tenant changed", ErrVersionConflict)
		}
	}
	return nil
}

func (s *PlatformMappingProjectionRefreshStore) GetMappingProjectionRefresh(ctx context.Context, receiptID string) (MappingProjectionRefreshRecord, error) {
	if err := s.ready(); err != nil {
		return MappingProjectionRefreshRecord{}, err
	}
	record, err := scanMappingProjectionRefresh(s.queryRow(ctx, mappingProjectionRefreshSelect+` WHERE receipt_id = ?`, strings.TrimSpace(receiptID)))
	if errors.Is(err, sql.ErrNoRows) {
		return MappingProjectionRefreshRecord{}, fmt.Errorf("%w: mapping projection refresh %s", ErrNotFound, receiptID)
	}
	return record, err
}

func (s *PlatformMappingProjectionRefreshStore) MappingProjectionRevision(ctx context.Context) (int64, error) {
	if err := s.ready(); err != nil {
		return 0, err
	}
	var revision int64
	if err := s.queryRow(ctx, `SELECT COUNT(*) FROM ontology_mapping_projection_refreshes`).Scan(&revision); err != nil {
		return 0, fmt.Errorf("read mapping projection revision: %w", err)
	}
	return revision, nil
}

func (s *PlatformMappingProjectionRefreshStore) ExecuteMappingProjectionRefreshRetry(
	ctx context.Context,
	tenantID, receiptID, idempotencyKey string,
) (MappingProjectionRefreshRetryReceipt, error) {
	if err := s.ready(); err != nil {
		return MappingProjectionRefreshRetryReceipt{}, err
	}
	if s.store == nil {
		return MappingProjectionRefreshRetryReceipt{}, fmt.Errorf("%w: retry command requires a database store", ErrInvalidDefinition)
	}
	tenantID, receiptID, idempotencyKey = strings.TrimSpace(tenantID), strings.TrimSpace(receiptID), strings.TrimSpace(idempotencyKey)
	if tenantID == "" || receiptID == "" || idempotencyKey == "" || len(tenantID) > 256 || len(receiptID) > 256 || len(idempotencyKey) > 256 {
		return MappingProjectionRefreshRetryReceipt{}, fmt.Errorf("%w: retry command tenant, receipt, and idempotency key are required", ErrInvalidDefinition)
	}
	requestHash := mappingProjectionRefreshRetryHash(tenantID, receiptID)
	now := s.now().UTC()
	var receipt MappingProjectionRefreshRetryReceipt
	err := s.store.WithTx(ctx, func(tx db.Tx) error {
		result, err := tx.ExecContext(ctx, `
INSERT INTO ontology_mapping_projection_refresh_commands (
	tenant_id, idempotency_key, receipt_id, request_hash, state, result_status, created_at, updated_at
) VALUES (?, ?, ?, ?, 'processing', '', ?, ?)
ON CONFLICT(tenant_id, idempotency_key) DO NOTHING`, tenantID, idempotencyKey, receiptID, requestHash, timeToUnixNano(now), timeToUnixNano(now))
		if err != nil {
			return fmt.Errorf("claim mapping projection refresh retry: %w", err)
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if affected == 0 {
			var storedReceiptID, storedHash, state, status string
			if err := tx.QueryRowContext(ctx, `
SELECT receipt_id, request_hash, state, result_status
FROM ontology_mapping_projection_refresh_commands
WHERE tenant_id = ? AND idempotency_key = ?`, tenantID, idempotencyKey).Scan(&storedReceiptID, &storedHash, &state, &status); err != nil {
				return err
			}
			if storedReceiptID != receiptID || storedHash != requestHash || state != "completed" {
				return fmt.Errorf("%w: projection refresh retry idempotency key was reused", ErrVersionConflict)
			}
			receipt.Status = MappingProjectionRefreshState(status)
			if !validMappingProjectionRefreshState(receipt.Status) {
				return fmt.Errorf("%w: stored projection refresh retry result is invalid", ErrVersionConflict)
			}
			receipt.IdempotentReplay = true
			return nil
		}

		txStore := NewPlatformMappingProjectionRefreshStoreTx(tx)
		txStore.now = s.now
		job, err := txStore.GetMappingProjectionRefresh(ctx, receiptID)
		if err != nil {
			return err
		}
		if job.TenantID != tenantID {
			return ErrNotFound
		}
		receipt.Status = job.State
		if job.State == MappingProjectionRefreshAbandoned {
			job, receipt.Requeued, err = txStore.RequeueAbandonedMappingProjectionRefresh(ctx, receiptID, tenantID)
			if err != nil {
				return err
			}
			receipt.Status = job.State
		}
		if !validMappingProjectionRefreshState(receipt.Status) {
			return fmt.Errorf("%w: projection refresh state is invalid", ErrVersionConflict)
		}
		result, err = tx.ExecContext(ctx, `
UPDATE ontology_mapping_projection_refresh_commands
SET state = 'completed', result_status = ?, updated_at = ?
WHERE tenant_id = ? AND idempotency_key = ? AND state = 'processing'`, receipt.Status, timeToUnixNano(now), tenantID, idempotencyKey)
		if err != nil {
			return err
		}
		affected, err = result.RowsAffected()
		if err != nil {
			return err
		}
		if affected != 1 {
			return fmt.Errorf("%w: projection refresh retry command claim was lost", ErrVersionConflict)
		}
		return nil
	})
	if err != nil {
		return MappingProjectionRefreshRetryReceipt{}, err
	}
	return receipt, nil
}

func mappingProjectionRefreshRetryHash(tenantID, receiptID string) string {
	digest := sha256.Sum256([]byte(tenantID + "\x00" + receiptID))
	return hex.EncodeToString(digest[:])
}

func validMappingProjectionRefreshState(state MappingProjectionRefreshState) bool {
	switch state {
	case MappingProjectionRefreshPending, MappingProjectionRefreshSucceeded, MappingProjectionRefreshAbandoned:
		return true
	default:
		return false
	}
}

func (s *PlatformMappingProjectionRefreshStore) ListPendingMappingProjectionRefreshes(ctx context.Context, dueAt time.Time, limit int) ([]MappingProjectionRefreshRecord, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	if dueAt.IsZero() || limit <= 0 || limit > 100 {
		return nil, fmt.Errorf("%w: mapping projection refresh due time and limit between 1 and 100 are required", ErrInvalidDefinition)
	}
	var rows *sql.Rows
	var err error
	query := mappingProjectionRefreshSelect + ` WHERE state = ? AND next_attempt_at <= ? ORDER BY next_attempt_at ASC, created_at ASC, receipt_id ASC LIMIT ?`
	if s.tx != nil {
		rows, err = s.tx.QueryContext(ctx, query, MappingProjectionRefreshPending, timeToUnixNano(dueAt.UTC()), limit)
	} else {
		rows, err = s.store.Query(ctx, query, MappingProjectionRefreshPending, timeToUnixNano(dueAt.UTC()), limit)
	}
	if err != nil {
		return nil, fmt.Errorf("list pending mapping projection refreshes: %w", err)
	}
	defer rows.Close()
	out := make([]MappingProjectionRefreshRecord, 0, limit)
	for rows.Next() {
		record, scanErr := scanMappingProjectionRefresh(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		out = append(out, record)
	}
	return out, rows.Err()
}

func (s *PlatformMappingProjectionRefreshStore) ListMappingProjectionRefreshes(ctx context.Context, receiptIDs []string) (map[string]MappingProjectionRefreshRecord, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	unique := make([]string, 0, len(receiptIDs))
	seen := make(map[string]struct{}, len(receiptIDs))
	for _, raw := range receiptIDs {
		receiptID := strings.TrimSpace(raw)
		if receiptID == "" || len(receiptID) > 256 {
			return nil, fmt.Errorf("%w: mapping projection refresh receipt ids are invalid", ErrInvalidDefinition)
		}
		if _, exists := seen[receiptID]; exists {
			continue
		}
		seen[receiptID] = struct{}{}
		unique = append(unique, receiptID)
	}
	if len(unique) == 0 {
		return map[string]MappingProjectionRefreshRecord{}, nil
	}
	if len(unique) > 100 {
		return nil, fmt.Errorf("%w: at most 100 mapping projection refreshes can be read", ErrInvalidDefinition)
	}
	placeholders := make([]string, len(unique))
	args := make([]any, len(unique))
	for i, receiptID := range unique {
		placeholders[i] = "?"
		args[i] = receiptID
	}
	var rows *sql.Rows
	var err error
	query := mappingProjectionRefreshSelect + ` WHERE receipt_id IN (` + strings.Join(placeholders, ",") + `)`
	if s.tx != nil {
		rows, err = s.tx.QueryContext(ctx, query, args...)
	} else {
		rows, err = s.store.Query(ctx, query, args...)
	}
	if err != nil {
		return nil, fmt.Errorf("list mapping projection refreshes: %w", err)
	}
	defer rows.Close()
	out := make(map[string]MappingProjectionRefreshRecord, len(unique))
	for rows.Next() {
		record, scanErr := scanMappingProjectionRefresh(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		out[record.ReceiptID] = record
	}
	return out, rows.Err()
}

func (s *PlatformMappingProjectionRefreshStore) RequeueAbandonedMappingProjectionRefresh(ctx context.Context, receiptID, tenantID string) (MappingProjectionRefreshRecord, bool, error) {
	if err := s.ready(); err != nil {
		return MappingProjectionRefreshRecord{}, false, err
	}
	receiptID, tenantID = strings.TrimSpace(receiptID), strings.TrimSpace(tenantID)
	if receiptID == "" || tenantID == "" || len(receiptID) > 256 || len(tenantID) > 256 {
		return MappingProjectionRefreshRecord{}, false, fmt.Errorf("%w: mapping projection refresh receipt and tenant are required", ErrInvalidDefinition)
	}
	now := s.now().UTC()
	result, err := s.exec(ctx, `
UPDATE ontology_mapping_projection_refreshes
SET state = ?, attempts = 0, last_error_code = '', next_attempt_at = ?, updated_at = ?
WHERE receipt_id = ? AND tenant_id = ? AND state = ?`,
		MappingProjectionRefreshPending, timeToUnixNano(now), timeToUnixNano(now), receiptID, tenantID, MappingProjectionRefreshAbandoned)
	if err != nil {
		return MappingProjectionRefreshRecord{}, false, fmt.Errorf("requeue abandoned mapping projection refresh: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return MappingProjectionRefreshRecord{}, false, err
	}
	record, getErr := s.GetMappingProjectionRefresh(ctx, receiptID)
	if getErr != nil {
		return MappingProjectionRefreshRecord{}, false, getErr
	}
	if record.TenantID != tenantID {
		return MappingProjectionRefreshRecord{}, false, fmt.Errorf("%w: mapping projection refresh %s", ErrNotFound, receiptID)
	}
	if affected == 0 && record.State != MappingProjectionRefreshPending && record.State != MappingProjectionRefreshSucceeded {
		return MappingProjectionRefreshRecord{}, false, fmt.Errorf("%w: mapping projection refresh is %s", ErrVersionConflict, record.State)
	}
	return record, affected == 1, nil
}

func (s *PlatformMappingProjectionRefreshStore) RecordMappingProjectionRefreshFailure(ctx context.Context, receiptID, errorCode string, expectedAttempts int, nextAttemptAt time.Time, maxAttempts int) error {
	if err := s.ready(); err != nil {
		return err
	}
	receiptID, errorCode = strings.TrimSpace(receiptID), strings.TrimSpace(errorCode)
	if receiptID == "" || errorCode == "" || len(errorCode) > 128 || expectedAttempts < 0 || nextAttemptAt.IsZero() || maxAttempts <= 0 || maxAttempts > 100 {
		return fmt.Errorf("%w: mapping projection refresh failure is invalid", ErrInvalidDefinition)
	}
	now := s.now().UTC()
	result, err := s.exec(ctx, `
UPDATE ontology_mapping_projection_refreshes
SET state = CASE WHEN attempts + 1 >= ? THEN ? ELSE ? END,
	attempts = attempts + 1, last_error_code = ?, next_attempt_at = ?, updated_at = ?
WHERE receipt_id = ? AND state = ? AND attempts = ?`,
		maxAttempts, MappingProjectionRefreshAbandoned, MappingProjectionRefreshPending,
		errorCode, timeToUnixNano(nextAttemptAt.UTC()), timeToUnixNano(now), receiptID, MappingProjectionRefreshPending, expectedAttempts)
	if err != nil {
		return fmt.Errorf("record mapping projection refresh failure: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		record, getErr := s.GetMappingProjectionRefresh(ctx, receiptID)
		if getErr != nil {
			return getErr
		}
		return fmt.Errorf("%w: mapping projection refresh is %s", ErrVersionConflict, record.State)
	}
	return nil
}

func (s *PlatformMappingProjectionRefreshStore) CompleteMappingProjectionRefresh(ctx context.Context, receiptID string) error {
	return s.transition(ctx, receiptID, MappingProjectionRefreshSucceeded, "")
}

func (s *PlatformMappingProjectionRefreshStore) transition(ctx context.Context, receiptID string, state MappingProjectionRefreshState, errorCode string) error {
	if err := s.ready(); err != nil {
		return err
	}
	receiptID = strings.TrimSpace(receiptID)
	if receiptID == "" || len(errorCode) > 128 {
		return fmt.Errorf("%w: mapping projection refresh transition is invalid", ErrInvalidDefinition)
	}
	now := s.now().UTC()
	result, err := s.exec(ctx, `
UPDATE ontology_mapping_projection_refreshes
SET state = ?, attempts = attempts + 1, last_error_code = ?, next_attempt_at = ?, updated_at = ?
WHERE receipt_id = ? AND state = ?`, state, errorCode, timeToUnixNano(now), timeToUnixNano(now), receiptID, MappingProjectionRefreshPending)
	if err != nil {
		return fmt.Errorf("transition mapping projection refresh: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		record, getErr := s.GetMappingProjectionRefresh(ctx, receiptID)
		if getErr != nil {
			return getErr
		}
		if state == MappingProjectionRefreshSucceeded && record.State == state {
			return nil
		}
		return fmt.Errorf("%w: mapping projection refresh is %s", ErrVersionConflict, record.State)
	}
	return nil
}

const mappingProjectionRefreshSelect = `SELECT receipt_id, tenant_id, state, attempts, last_error_code, next_attempt_at, created_at, updated_at
FROM ontology_mapping_projection_refreshes`

type mappingProjectionRefreshScanner interface {
	Scan(...any) error
}

func scanMappingProjectionRefresh(scanner mappingProjectionRefreshScanner) (MappingProjectionRefreshRecord, error) {
	var record MappingProjectionRefreshRecord
	var nextAttemptAt, createdAt, updatedAt int64
	if err := scanner.Scan(&record.ReceiptID, &record.TenantID, &record.State, &record.Attempts, &record.LastErrorCode, &nextAttemptAt, &createdAt, &updatedAt); err != nil {
		return MappingProjectionRefreshRecord{}, err
	}
	record.NextAttemptAt, record.CreatedAt, record.UpdatedAt = unixNanoToTime(nextAttemptAt), unixNanoToTime(createdAt), unixNanoToTime(updatedAt)
	return record, nil
}

func MetadataPromotionRequiresMappingProjectionRefresh(changes []MetadataProposalPromotionChange) bool {
	for _, change := range changes {
		if change.TargetKind == "mapping_rule" && change.Operation != PromotionOperationNoop && change.AppliedVersion > 0 {
			return true
		}
	}
	return false
}
