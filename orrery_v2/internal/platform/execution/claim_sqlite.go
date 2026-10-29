package execution

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"orrery_v2/internal/platform/clock"
	"orrery_v2/internal/platform/db"
)

type ClaimStore struct {
	db    *db.Store
	clock clock.Clock
}

type ClaimStoreOptions struct {
	Clock clock.Clock
}

func NewClaimStore(ctx context.Context, store *db.Store) (*ClaimStore, error) {
	return NewClaimStoreWithOptions(ctx, store, ClaimStoreOptions{})
}

func NewClaimStoreWithOptions(ctx context.Context, store *db.Store, opts ClaimStoreOptions) (*ClaimStore, error) {
	if store == nil {
		return nil, fmt.Errorf("sqlite claim store requires database")
	}
	claims := &ClaimStore{db: store, clock: claimClock(opts.Clock)}
	if err := claims.init(ctx); err != nil {
		return nil, err
	}
	return claims, nil
}

func (s *ClaimStore) Acquire(ctx context.Context, runID, workerID string, ttl time.Duration, now time.Time) (bool, error) {
	if err := validateLease(runID, workerID, ttl); err != nil {
		return false, err
	}
	nowNano := now.UTC().UnixNano()
	expiresAt := now.UTC().Add(ttl).UnixNano()
	var affected int64
	err := s.db.WithTx(ctx, func(tx db.Tx) error {
		result, err := tx.ExecContext(ctx, `
INSERT INTO platform_claim_leases (run_id, worker_id, expires_at, updated_at)
VALUES (?, ?, ?, ?)
ON CONFLICT(run_id) DO UPDATE SET
	worker_id = excluded.worker_id,
	expires_at = excluded.expires_at,
	updated_at = excluded.updated_at
WHERE platform_claim_leases.expires_at <= ?`,
			runID, workerID, expiresAt, nowNano, nowNano,
		)
		if err != nil {
			return fmt.Errorf("acquire claim lease: %w", err)
		}
		affected, err = result.RowsAffected()
		if err != nil {
			return fmt.Errorf("read claim acquire rows affected: %w", err)
		}
		return nil
	})
	return affected > 0, err
}

func (s *ClaimStore) Renew(ctx context.Context, runID, workerID string, ttl time.Duration, now time.Time) (bool, error) {
	if err := validateLease(runID, workerID, ttl); err != nil {
		return false, err
	}
	nowNano := now.UTC().UnixNano()
	expiresAt := now.UTC().Add(ttl).UnixNano()
	result, err := s.db.Exec(ctx, `
UPDATE platform_claim_leases
SET expires_at = ?, updated_at = ?
WHERE run_id = ? AND worker_id = ?`,
		expiresAt, nowNano, runID, workerID,
	)
	if err != nil {
		return false, fmt.Errorf("renew claim lease: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("read claim renew rows affected: %w", err)
	}
	return affected > 0, nil
}

func (s *ClaimStore) Release(ctx context.Context, runID, workerID string) (bool, error) {
	if strings.TrimSpace(runID) == "" || strings.TrimSpace(workerID) == "" {
		return false, fmt.Errorf("run id and worker id are required")
	}
	result, err := s.db.Exec(ctx, `DELETE FROM platform_claim_leases WHERE run_id = ? AND worker_id = ?`, runID, workerID)
	if err != nil {
		return false, fmt.Errorf("release claim lease: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("read claim release rows affected: %w", err)
	}
	return affected > 0, nil
}

func (s *ClaimStore) RecoverStale(ctx context.Context, now time.Time, batchSize int) (int, error) {
	if batchSize <= 0 {
		return 0, nil
	}
	result, err := s.db.Exec(ctx, `
DELETE FROM platform_claim_leases
WHERE run_id IN (
	SELECT run_id FROM platform_claim_leases
	WHERE expires_at <= ?
	ORDER BY expires_at ASC
	LIMIT ?
)`, now.UTC().UnixNano(), batchSize)
	if err != nil {
		return 0, fmt.Errorf("recover stale claim leases: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("read stale claim rows affected: %w", err)
	}
	return int(affected), nil
}

func (s *ClaimStore) CurrentVersion(ctx context.Context, key string) (int, error) {
	if strings.TrimSpace(key) == "" {
		return 0, fmt.Errorf("negative cache key is required")
	}
	var version int
	err := s.db.QueryRow(ctx, `SELECT version FROM platform_negative_claim_cache WHERE cache_key = ?`, key).Scan(&version)
	if errors.Is(err, sql.ErrNoRows) {
		if _, err := s.db.Exec(ctx, `INSERT INTO platform_negative_claim_cache (cache_key, version) VALUES (?, 0) ON CONFLICT(cache_key) DO NOTHING`, key); err != nil {
			return 0, fmt.Errorf("initialize negative cache version: %w", err)
		}
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("read negative cache version: %w", err)
	}
	return version, nil
}

func (s *ClaimStore) MarkEmpty(ctx context.Context, key string, observedVersion int) error {
	if _, err := s.CurrentVersion(ctx, key); err != nil {
		return err
	}
	_, err := s.db.Exec(ctx, `
UPDATE platform_negative_claim_cache
SET empty_version = ?, updated_at = ?
WHERE cache_key = ?`,
		observedVersion, s.now().UnixNano(), key,
	)
	if err != nil {
		return fmt.Errorf("mark negative cache empty: %w", err)
	}
	return nil
}

func (s *ClaimStore) Bump(ctx context.Context, key string) error {
	if strings.TrimSpace(key) == "" {
		return fmt.Errorf("negative cache key is required")
	}
	_, err := s.db.Exec(ctx, `
INSERT INTO platform_negative_claim_cache (cache_key, version, updated_at)
VALUES (?, 1, ?)
ON CONFLICT(cache_key) DO UPDATE SET
	version = platform_negative_claim_cache.version + 1,
	updated_at = excluded.updated_at`,
		key, s.now().UnixNano(),
	)
	if err != nil {
		return fmt.Errorf("bump negative cache version: %w", err)
	}
	return nil
}

func (s *ClaimStore) IsEmpty(ctx context.Context, key string) (bool, error) {
	if strings.TrimSpace(key) == "" {
		return false, fmt.Errorf("negative cache key is required")
	}
	var version int
	var emptyVersion sql.NullInt64
	err := s.db.QueryRow(ctx, `SELECT version, empty_version FROM platform_negative_claim_cache WHERE cache_key = ?`, key).Scan(&version, &emptyVersion)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read negative cache empty: %w", err)
	}
	return emptyVersion.Valid && int(emptyVersion.Int64) == version, nil
}

func (s *ClaimStore) init(ctx context.Context) error {
	timeType := "INTEGER"
	if s.db.Dialect() == db.DialectPostgres {
		timeType = "BIGINT"
	}
	if _, err := s.db.Exec(ctx, fmt.Sprintf(`
CREATE TABLE IF NOT EXISTS platform_claim_leases (
	run_id TEXT PRIMARY KEY,
	worker_id TEXT NOT NULL,
	expires_at %[1]s NOT NULL,
	updated_at %[1]s NOT NULL
)`, timeType)); err != nil {
		return fmt.Errorf("create platform_claim_leases: %w", err)
	}
	if _, err := s.db.Exec(ctx, `CREATE INDEX IF NOT EXISTS idx_platform_claim_leases_expires_at ON platform_claim_leases (expires_at)`); err != nil {
		return fmt.Errorf("create platform_claim_leases expires index: %w", err)
	}
	if _, err := s.db.Exec(ctx, fmt.Sprintf(`
CREATE TABLE IF NOT EXISTS platform_negative_claim_cache (
	cache_key TEXT PRIMARY KEY,
	version INTEGER NOT NULL DEFAULT 0,
	empty_version INTEGER,
	updated_at %[1]s NOT NULL DEFAULT 0
)`, timeType)); err != nil {
		return fmt.Errorf("create platform_negative_claim_cache: %w", err)
	}
	return nil
}

func (s *ClaimStore) now() time.Time {
	if s == nil || s.clock == nil {
		return time.Now().UTC()
	}
	return s.clock.Now().UTC()
}

func claimClock(c clock.Clock) clock.Clock {
	if c != nil {
		return c
	}
	return clock.Real{}
}

func validateLease(runID, workerID string, ttl time.Duration) error {
	if strings.TrimSpace(runID) == "" {
		return fmt.Errorf("run id is required")
	}
	if strings.TrimSpace(workerID) == "" {
		return fmt.Errorf("worker id is required")
	}
	if ttl <= 0 {
		return fmt.Errorf("claim ttl must be positive")
	}
	return nil
}
