package lock

import (
	"context"
	"fmt"
	"strings"
	"time"

	"orrery_v2/internal/platform/db"
)

type Store struct {
	db *db.Store
}

type Lease struct {
	store *Store
	key   string
	owner string
}

type SQLite = Store
type SQLiteLease = Lease

func New(ctx context.Context, store *db.Store) (*Store, error) {
	if store == nil {
		return nil, fmt.Errorf("lock store requires database")
	}
	locks := &Store{db: store}
	if err := locks.init(ctx); err != nil {
		return nil, err
	}
	return locks, nil
}

func NewSQLite(ctx context.Context, store *db.Store) (*SQLite, error) {
	return New(ctx, store)
}

func (s *Store) TryAcquire(ctx context.Context, key, owner string, ttl time.Duration, now time.Time) (*Lease, bool, error) {
	if err := validateLeaseInput(key, owner, ttl); err != nil {
		return nil, false, err
	}
	expiresAt := now.UTC().Add(ttl).UnixNano()
	nowNano := now.UTC().UnixNano()
	var affected int64
	err := s.db.WithTx(ctx, func(tx db.Tx) error {
		result, err := tx.ExecContext(ctx, `
INSERT INTO platform_locks (lock_key, owner, expires_at, updated_at)
VALUES (?, ?, ?, ?)
ON CONFLICT(lock_key) DO UPDATE SET
	owner = excluded.owner,
	expires_at = excluded.expires_at,
	updated_at = excluded.updated_at
WHERE platform_locks.expires_at <= ?`,
			key, owner, expiresAt, nowNano, nowNano,
		)
		if err != nil {
			return fmt.Errorf("acquire database lock: %w", err)
		}
		affected, err = result.RowsAffected()
		if err != nil {
			return fmt.Errorf("read database lock rows affected: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, false, err
	}
	if affected == 0 {
		return nil, false, nil
	}
	return &Lease{store: s, key: key, owner: owner}, true, nil
}

func (s *Store) Release(ctx context.Context, key, owner string) (bool, error) {
	if strings.TrimSpace(key) == "" || strings.TrimSpace(owner) == "" {
		return false, fmt.Errorf("lock key and owner are required")
	}
	var affected int64
	err := s.db.WithTx(ctx, func(tx db.Tx) error {
		result, err := tx.ExecContext(ctx, `DELETE FROM platform_locks WHERE lock_key = ? AND owner = ?`, key, owner)
		if err != nil {
			return fmt.Errorf("release database lock: %w", err)
		}
		affected, err = result.RowsAffected()
		if err != nil {
			return fmt.Errorf("read database lock release rows affected: %w", err)
		}
		return nil
	})
	return affected > 0, err
}

func (s *Store) Renew(ctx context.Context, key, owner string, ttl time.Duration, now time.Time) (bool, error) {
	if err := validateLeaseInput(key, owner, ttl); err != nil {
		return false, err
	}
	nowNano := now.UTC().UnixNano()
	expiresAt := now.UTC().Add(ttl).UnixNano()
	var affected int64
	err := s.db.WithTx(ctx, func(tx db.Tx) error {
		result, err := tx.ExecContext(ctx, `
UPDATE platform_locks
SET expires_at = ?, updated_at = ?
WHERE lock_key = ? AND owner = ? AND expires_at > ?`,
			expiresAt, nowNano, key, owner, nowNano,
		)
		if err != nil {
			return fmt.Errorf("renew database lock: %w", err)
		}
		affected, err = result.RowsAffected()
		if err != nil {
			return fmt.Errorf("read database lock renew rows affected: %w", err)
		}
		return nil
	})
	return affected > 0, err
}

func (s *Store) SweepExpired(ctx context.Context, now time.Time, batchSize int) (int, error) {
	if batchSize <= 0 {
		return 0, nil
	}
	nowNano := now.UTC().UnixNano()
	var affected int64
	err := s.db.WithTx(ctx, func(tx db.Tx) error {
		result, err := tx.ExecContext(ctx, `
DELETE FROM platform_locks
WHERE lock_key IN (
	SELECT lock_key FROM platform_locks
	WHERE expires_at <= ?
	ORDER BY expires_at ASC
	LIMIT ?
)`, nowNano, batchSize)
		if err != nil {
			return fmt.Errorf("sweep expired database locks: %w", err)
		}
		affected, err = result.RowsAffected()
		if err != nil {
			return fmt.Errorf("read database lock sweep rows affected: %w", err)
		}
		return nil
	})
	return int(affected), err
}

func (l *Lease) Release(ctx context.Context) error {
	if l == nil || l.store == nil {
		return nil
	}
	_, err := l.store.Release(ctx, l.key, l.owner)
	return err
}

func (l *Lease) Renew(ctx context.Context, ttl time.Duration, now time.Time) (bool, error) {
	if l == nil || l.store == nil {
		return false, nil
	}
	return l.store.Renew(ctx, l.key, l.owner, ttl, now)
}

func (s *Store) init(ctx context.Context) error {
	timeType := "INTEGER"
	if s.db.Dialect() == db.DialectPostgres {
		timeType = "BIGINT"
	}
	_, err := s.db.Exec(ctx, fmt.Sprintf(`
CREATE TABLE IF NOT EXISTS platform_locks (
	lock_key TEXT PRIMARY KEY,
	owner TEXT NOT NULL,
	expires_at %[1]s NOT NULL,
	updated_at %[1]s NOT NULL
)`, timeType))
	if err != nil {
		return fmt.Errorf("create platform_locks: %w", err)
	}
	if _, err := s.db.Exec(ctx, `CREATE INDEX IF NOT EXISTS idx_platform_locks_expires_at ON platform_locks (expires_at)`); err != nil {
		return fmt.Errorf("create platform_locks expires index: %w", err)
	}
	return nil
}

func validateLeaseInput(key, owner string, ttl time.Duration) error {
	if strings.TrimSpace(key) == "" {
		return fmt.Errorf("lock key is required")
	}
	if strings.TrimSpace(owner) == "" {
		return fmt.Errorf("lock owner is required")
	}
	if ttl <= 0 {
		return fmt.Errorf("lock ttl must be positive")
	}
	return nil
}
