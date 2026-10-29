package execution

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"orrery_v2/internal/platform/db"
)

type LivenessStore struct {
	db *db.Store
}

type LivenessSnapshot struct {
	RuntimeID  string
	LastSeenAt time.Time
	ExpiresAt  time.Time
	Alive      bool
}

func NewLivenessStore(ctx context.Context, store *db.Store) (*LivenessStore, error) {
	if store == nil {
		return nil, fmt.Errorf("sqlite liveness store requires database")
	}
	live := &LivenessStore{db: store}
	if err := live.init(ctx); err != nil {
		return nil, err
	}
	return live, nil
}

func (s *LivenessStore) Touch(ctx context.Context, runtimeID string, ttl time.Duration, now time.Time) {
	_ = s.TouchHeartbeat(ctx, runtimeID, ttl, now)
}

func (s *LivenessStore) TouchHeartbeat(ctx context.Context, runtimeID string, ttl time.Duration, now time.Time) error {
	if ctx.Err() != nil || strings.TrimSpace(runtimeID) == "" || ttl <= 0 {
		return nil
	}
	nowNano := now.UTC().UnixNano()
	expiresAt := now.UTC().Add(ttl).UnixNano()
	_, err := s.db.Exec(ctx, `
INSERT INTO platform_liveness (runtime_id, last_seen_at, expires_at)
VALUES (?, ?, ?)
ON CONFLICT(runtime_id) DO UPDATE SET
	last_seen_at = excluded.last_seen_at,
	expires_at = excluded.expires_at`,
		runtimeID, nowNano, expiresAt,
	)
	if err != nil {
		return fmt.Errorf("touch liveness heartbeat: %w", err)
	}
	return nil
}

func (s *LivenessStore) IsAliveBatch(ctx context.Context, runtimeIDs []string, now time.Time) map[string]bool {
	result := map[string]bool{}
	if ctx.Err() != nil {
		return result
	}
	for _, id := range runtimeIDs {
		result[id] = false
	}
	if len(runtimeIDs) == 0 {
		return result
	}
	nowNano := now.UTC().UnixNano()
	for _, id := range runtimeIDs {
		if strings.TrimSpace(id) == "" {
			continue
		}
		var expiresAt int64
		err := s.db.QueryRow(ctx, `SELECT expires_at FROM platform_liveness WHERE runtime_id = ?`, id).Scan(&expiresAt)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return map[string]bool{}
		}
		result[id] = expiresAt > nowNano
	}
	return result
}

func (s *LivenessStore) SnapshotBatch(ctx context.Context, runtimeIDs []string, now time.Time) (map[string]LivenessSnapshot, error) {
	result := map[string]LivenessSnapshot{}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	nowNano := now.UTC().UnixNano()
	for _, id := range runtimeIDs {
		if strings.TrimSpace(id) == "" {
			continue
		}
		var lastSeenAt int64
		var expiresAt int64
		err := s.db.QueryRow(ctx, `SELECT last_seen_at, expires_at FROM platform_liveness WHERE runtime_id = ?`, id).Scan(&lastSeenAt, &expiresAt)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("snapshot liveness heartbeat: %w", err)
		}
		result[id] = LivenessSnapshot{
			RuntimeID:  id,
			LastSeenAt: time.Unix(0, lastSeenAt).UTC(),
			ExpiresAt:  time.Unix(0, expiresAt).UTC(),
			Alive:      expiresAt > nowNano,
		}
	}
	return result, nil
}

func (s *LivenessStore) Forget(ctx context.Context, runtimeID string) {
	_ = s.ForgetHeartbeat(ctx, runtimeID)
}

func (s *LivenessStore) ForgetHeartbeat(ctx context.Context, runtimeID string) error {
	if ctx.Err() != nil || strings.TrimSpace(runtimeID) == "" {
		return nil
	}
	if _, err := s.db.Exec(ctx, `DELETE FROM platform_liveness WHERE runtime_id = ?`, runtimeID); err != nil {
		return fmt.Errorf("forget liveness heartbeat: %w", err)
	}
	return nil
}

func (s *LivenessStore) SweepStale(ctx context.Context, now time.Time, batchSize int) (int, error) {
	if batchSize <= 0 {
		return 0, nil
	}
	result, err := s.db.Exec(ctx, `
DELETE FROM platform_liveness
WHERE runtime_id IN (
	SELECT runtime_id FROM platform_liveness
	WHERE expires_at <= ?
	ORDER BY expires_at ASC
	LIMIT ?
)`, now.UTC().UnixNano(), batchSize)
	if err != nil {
		return 0, fmt.Errorf("sweep stale liveness records: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("read liveness sweep rows affected: %w", err)
	}
	return int(affected), nil
}

func (s *LivenessStore) init(ctx context.Context) error {
	timeType := "INTEGER"
	if s.db.Dialect() == db.DialectPostgres {
		timeType = "BIGINT"
	}
	if _, err := s.db.Exec(ctx, fmt.Sprintf(`
CREATE TABLE IF NOT EXISTS platform_liveness (
	runtime_id TEXT PRIMARY KEY,
	last_seen_at %[1]s NOT NULL,
	expires_at %[1]s NOT NULL
)`, timeType)); err != nil {
		return fmt.Errorf("create platform_liveness: %w", err)
	}
	if _, err := s.db.Exec(ctx, `CREATE INDEX IF NOT EXISTS idx_platform_liveness_expires_at ON platform_liveness (expires_at)`); err != nil {
		return fmt.Errorf("create platform_liveness expires index: %w", err)
	}
	return nil
}
