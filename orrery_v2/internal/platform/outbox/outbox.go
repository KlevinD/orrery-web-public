package outbox

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"orrery_v2/internal/platform/clock"
	"orrery_v2/internal/platform/db"
)

type Status string

const (
	StatusPending    Status = "pending"
	StatusRunning    Status = "running"
	StatusDone       Status = "done"
	StatusDeadLetter Status = "dead_letter"
)

type Job struct {
	ID             string
	Topic          string
	Payload        []byte
	IdempotencyKey string
	Status         Status
	WorkerID       string
	Attempts       int
	MaxAttempts    int
	NextAttemptAt  time.Time
	LockedUntil    time.Time
	LastError      string
}

type Store struct {
	db       *db.Store
	mu       sync.Mutex
	leaseTTL time.Duration
	clock    clock.Clock
}

type Tx interface {
	db.Tx
}

type TxStore struct {
	tx    Tx
	clock clock.Clock
}

type StoreOptions struct {
	Clock clock.Clock
}

func New(store *db.Store) *Store {
	return NewWithOptions(store, StoreOptions{})
}

func NewWithOptions(store *db.Store, opts StoreOptions) *Store {
	return &Store{db: store, leaseTTL: time.Minute, clock: outboxClock(opts.Clock)}
}

func (s *Store) WithLeaseTTL(ttl time.Duration) *Store {
	if ttl > 0 {
		s.leaseTTL = ttl
	}
	return s
}

func Open(ctx context.Context, store *db.Store) (*Store, error) {
	s := New(store)
	if err := s.init(ctx); err != nil {
		return nil, err
	}
	return s, nil
}

func NewTx(tx Tx) *TxStore {
	return NewTxWithOptions(tx, StoreOptions{})
}

func NewTxWithOptions(tx Tx, opts StoreOptions) *TxStore {
	return &TxStore{tx: tx, clock: outboxClock(opts.Clock)}
}

func outboxClock(c clock.Clock) clock.Clock {
	if c != nil {
		return c
	}
	return clock.Real{}
}

func (s *Store) Enqueue(ctx context.Context, job Job) error {
	if err := s.db.Ping(ctx); err != nil {
		return err
	}
	if job.ID == "" {
		return fmt.Errorf("job id is required")
	}
	if job.Status == "" {
		job.Status = StatusPending
	}
	if job.MaxAttempts <= 0 {
		job.MaxAttempts = 3
	}
	now := s.clock.Now().UTC()
	if job.NextAttemptAt.IsZero() {
		job.NextAttemptAt = now
	}
	if err := s.init(ctx); err != nil {
		return err
	}
	_, err := s.db.Exec(ctx, enqueueSQL(s.db.Dialect()),
		job.ID, job.Topic, job.Payload, nullString(job.IdempotencyKey), string(job.Status), nullString(job.WorkerID),
		job.Attempts, job.MaxAttempts, job.NextAttemptAt.UTC().UnixNano(), timeToNano(job.LockedUntil), job.LastError,
		now.UnixNano(), now.UnixNano(),
	)
	if err != nil {
		return fmt.Errorf("enqueue outbox job: %w", err)
	}
	return nil
}

func (s *Store) Claim(ctx context.Context, workerID string, now time.Time) (Job, bool, error) {
	workerID = strings.TrimSpace(workerID)
	if workerID == "" {
		return Job{}, false, fmt.Errorf("outbox worker id is required")
	}
	if err := s.db.Ping(ctx); err != nil {
		return Job{}, false, err
	}
	if err := s.init(ctx); err != nil {
		return Job{}, false, err
	}
	nowNano := now.UTC().UnixNano()
	lockedUntil := now.Add(s.leaseTTL).UTC().UnixNano()
	if s.db.Dialect() == db.DialectPostgres {
		return s.claimPostgres(ctx, workerID, nowNano, lockedUntil)
	}
	var id string
	err := s.db.WithTx(ctx, func(tx db.Tx) error {
		row := tx.QueryRowContext(ctx, `
SELECT id FROM platform_outbox_jobs
WHERE status = ? AND next_attempt_at <= ?
ORDER BY next_attempt_at ASC, created_at ASC
LIMIT 1`, string(StatusPending), nowNano)
		if err := row.Scan(&id); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil
			}
			return fmt.Errorf("select outbox claim candidate: %w", err)
		}
		result, err := tx.ExecContext(ctx, `
UPDATE platform_outbox_jobs
SET status = ?, worker_id = ?, locked_until = ?, updated_at = ?
WHERE id = ? AND status = ?`,
			string(StatusRunning), workerID, lockedUntil, nowNano, id, string(StatusPending),
		)
		if err != nil {
			return fmt.Errorf("claim outbox job: %w", err)
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("read outbox claim rows affected: %w", err)
		}
		if affected == 0 {
			id = ""
		}
		return nil
	})
	if err != nil {
		return Job{}, false, err
	}
	if id == "" {
		return Job{}, false, nil
	}
	job, ok := s.Get(ctx, id)
	return job, ok, nil
}

func (s *Store) claimPostgres(ctx context.Context, workerID string, nowNano, lockedUntil int64) (Job, bool, error) {
	var claimed Job
	found := false
	err := s.db.WithTx(ctx, func(tx db.Tx) error {
		row := tx.QueryRowContext(ctx, postgresClaimSQL(),
			string(StatusPending), nowNano, string(StatusRunning), workerID, lockedUntil, nowNano,
		)
		job, err := scanJob(row)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("claim postgres outbox job: %w", err)
		}
		claimed = job
		found = true
		return nil
	})
	if err != nil {
		return Job{}, false, err
	}
	return claimed, found, nil
}

func (s *Store) RecoverStale(ctx context.Context, now time.Time, limit int) (int, error) {
	if err := s.db.Ping(ctx); err != nil {
		return 0, err
	}
	if err := s.init(ctx); err != nil {
		return 0, err
	}
	if limit <= 0 {
		limit = 100
	}
	query := `
UPDATE platform_outbox_jobs
SET status = CASE WHEN attempts + 1 >= max_attempts THEN ? ELSE ? END,
	worker_id = NULL,
	locked_until = NULL,
	attempts = attempts + 1,
	next_attempt_at = CASE WHEN attempts + 1 >= max_attempts THEN 0 ELSE ? END,
	last_error = ?,
	updated_at = ?
WHERE status = ? AND locked_until IS NOT NULL AND locked_until <= ? AND id IN (
	SELECT id FROM platform_outbox_jobs
	WHERE status = ? AND locked_until IS NOT NULL AND locked_until <= ?
	ORDER BY locked_until ASC
	LIMIT ?
)`
	args := []any{
		string(StatusDeadLetter), string(StatusPending), now.UTC().UnixNano(), "outbox lease expired", now.UTC().UnixNano(),
		string(StatusRunning), now.UTC().UnixNano(), string(StatusRunning), now.UTC().UnixNano(), limit,
	}
	if s.db.Dialect() == db.DialectPostgres {
		query = postgresRecoverStaleSQL()
		args = []any{
			string(StatusRunning), now.UTC().UnixNano(), limit,
			string(StatusDeadLetter), string(StatusPending), now.UTC().UnixNano(), "outbox lease expired", now.UTC().UnixNano(),
			string(StatusRunning), now.UTC().UnixNano(),
		}
	}
	result, err := s.db.Exec(ctx, query, args...)
	if err != nil {
		return 0, fmt.Errorf("recover stale outbox jobs: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("recover stale outbox jobs rows affected: %w", err)
	}
	return int(affected), nil
}

// RenewLease extends an unexpired running job lease only for its current owner.
func (s *Store) RenewLease(ctx context.Context, id, workerID string, now time.Time) (bool, error) {
	id = strings.TrimSpace(id)
	workerID = strings.TrimSpace(workerID)
	if id == "" || workerID == "" {
		return false, fmt.Errorf("outbox job id and worker id are required")
	}
	if now.IsZero() {
		now = s.clock.Now().UTC()
	} else {
		now = now.UTC()
	}
	lockedUntil := now.Add(s.leaseTTL).UnixNano()
	result, err := s.db.Exec(ctx, `
UPDATE platform_outbox_jobs
SET locked_until = ?, updated_at = ?
WHERE id = ? AND status = ? AND worker_id = ?
	AND locked_until IS NOT NULL AND locked_until > ?`,
		lockedUntil, now.UnixNano(), id, string(StatusRunning), workerID, now.UnixNano(),
	)
	if err != nil {
		return false, fmt.Errorf("renew outbox job lease: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("renew outbox job lease rows affected: %w", err)
	}
	return affected > 0, nil
}

func (s *Store) Complete(ctx context.Context, id string, workerID string) error {
	workerID = strings.TrimSpace(workerID)
	if workerID == "" {
		return fmt.Errorf("outbox worker id is required")
	}
	now := s.clock.Now().UTC()
	result, err := s.db.Exec(ctx, `
UPDATE platform_outbox_jobs
SET status = ?, locked_until = NULL, updated_at = ?
WHERE id = ? AND status = ? AND worker_id = ?`,
		string(StatusDone), now.UnixNano(), id, string(StatusRunning), workerID,
	)
	if err != nil {
		return fmt.Errorf("complete outbox job: %w", err)
	}
	return requireAffected(result, "complete outbox job")
}

func (s *Store) Fail(ctx context.Context, id, workerID, reason string, nextAttemptAt time.Time) error {
	workerID = strings.TrimSpace(workerID)
	if workerID == "" {
		return fmt.Errorf("outbox worker id is required")
	}
	return s.db.WithTx(ctx, func(tx db.Tx) error {
		var attempts, maxAttempts int
		err := tx.QueryRowContext(ctx, `SELECT attempts, max_attempts FROM platform_outbox_jobs
			WHERE id = ? AND status = ? AND worker_id = ?`, id, string(StatusRunning), workerID).Scan(&attempts, &maxAttempts)
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("outbox job is not owned by worker %q: %s", workerID, id)
		}
		if err != nil {
			return fmt.Errorf("load outbox attempts: %w", err)
		}
		attempts++
		status := StatusPending
		nextAt := nextAttemptAt.UTC().UnixNano()
		if attempts >= maxAttempts {
			status = StatusDeadLetter
			nextAt = 0
		}
		now := s.clock.Now().UTC()
		result, err := tx.ExecContext(ctx, `
UPDATE platform_outbox_jobs
SET status = ?, worker_id = ?, locked_until = NULL, attempts = ?, next_attempt_at = ?, last_error = ?, updated_at = ?
WHERE id = ? AND status = ? AND worker_id = ?`,
			string(status), nil, attempts, nextAt, reason, now.UnixNano(), id, string(StatusRunning), workerID,
		)
		if err != nil {
			return fmt.Errorf("fail outbox job: %w", err)
		}
		return requireAffected(result, "fail outbox job")
	})
}

func (s *Store) Get(ctx context.Context, id string) (Job, bool) {
	if err := s.db.Ping(ctx); err != nil {
		return Job{}, false
	}
	if err := s.init(ctx); err != nil {
		return Job{}, false
	}
	job, ok, err := s.getSQL(ctx, id)
	if err != nil {
		return Job{}, false
	}
	return job, ok
}

func (s *Store) StatusCounts(ctx context.Context) (map[string]int64, error) {
	if err := s.db.Ping(ctx); err != nil {
		return nil, err
	}
	if err := s.init(ctx); err != nil {
		return nil, err
	}
	rows, err := s.db.Query(ctx, `SELECT status, COUNT(*) FROM platform_outbox_jobs GROUP BY status`)
	if err != nil {
		return nil, fmt.Errorf("count outbox jobs by status: %w", err)
	}
	defer rows.Close()
	counts := map[string]int64{}
	for rows.Next() {
		var status string
		var count int64
		if err := rows.Scan(&status, &count); err != nil {
			return nil, fmt.Errorf("scan outbox status count: %w", err)
		}
		counts[status] = count
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate outbox status counts: %w", err)
	}
	return counts, nil
}

func (s *Store) PruneCompleted(ctx context.Context, before time.Time, limit int) (int, error) {
	if err := s.db.Ping(ctx); err != nil {
		return 0, err
	}
	if err := s.init(ctx); err != nil {
		return 0, err
	}
	if limit <= 0 {
		limit = 1000
	}
	result, err := s.db.Exec(ctx, `DELETE FROM platform_outbox_jobs WHERE id IN (
		SELECT id FROM platform_outbox_jobs
		WHERE status = ? AND updated_at < ?
		ORDER BY updated_at ASC
		LIMIT ?
	)`, string(StatusDone), before.UTC().UnixNano(), limit)
	if err != nil {
		return 0, fmt.Errorf("prune completed outbox jobs: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("prune completed outbox jobs rows affected: %w", err)
	}
	return int(affected), nil
}

func (s *TxStore) Enqueue(ctx context.Context, job Job) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if job.ID == "" {
		return fmt.Errorf("job id is required")
	}
	if job.Status == "" {
		job.Status = StatusPending
	}
	if job.MaxAttempts <= 0 {
		job.MaxAttempts = 3
	}
	now := s.clock.Now().UTC()
	if job.NextAttemptAt.IsZero() {
		job.NextAttemptAt = now
	}
	if err := initTx(ctx, s.tx); err != nil {
		return err
	}
	_, err := s.tx.ExecContext(ctx, enqueueSQL(txDialect(s.tx)),
		job.ID, job.Topic, job.Payload, nullString(job.IdempotencyKey), string(job.Status), nullString(job.WorkerID),
		job.Attempts, job.MaxAttempts, job.NextAttemptAt.UTC().UnixNano(), timeToNano(job.LockedUntil), job.LastError,
		now.UnixNano(), now.UnixNano(),
	)
	if err != nil {
		return fmt.Errorf("enqueue tx outbox job: %w", err)
	}
	return nil
}

func (s *TxStore) Get(ctx context.Context, id string) (Job, bool) {
	if err := ctx.Err(); err != nil {
		return Job{}, false
	}
	if err := initTx(ctx, s.tx); err != nil {
		return Job{}, false
	}
	job, ok, err := getSQL(ctx, s.tx, id)
	if err != nil {
		return Job{}, false
	}
	return job, ok
}

func initTx(ctx context.Context, tx db.Tx) error {
	dialect := txDialect(tx)
	if _, err := tx.ExecContext(ctx, createOutboxTableSQL(dialect)); err != nil {
		return fmt.Errorf("create platform_outbox_jobs: %w", err)
	}
	if dialect == db.DialectSQLite {
		if _, err := tx.ExecContext(ctx, `ALTER TABLE platform_outbox_jobs ADD COLUMN locked_until INTEGER`); err != nil && !isDuplicateColumn(err) {
			return fmt.Errorf("add outbox locked_until column: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `ALTER TABLE platform_outbox_jobs ADD COLUMN idempotency_key TEXT`); err != nil && !isDuplicateColumn(err) {
			return fmt.Errorf("add outbox idempotency_key column: %w", err)
		}
	}
	if _, err := tx.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_platform_outbox_pending ON platform_outbox_jobs (next_attempt_at, created_at) WHERE status = 'pending'`); err != nil {
		return fmt.Errorf("create outbox pending index: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_platform_outbox_locked_until ON platform_outbox_jobs (locked_until) WHERE status = 'running'`); err != nil {
		return fmt.Errorf("create outbox locked_until index: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `CREATE UNIQUE INDEX IF NOT EXISTS idx_platform_outbox_idempotency ON platform_outbox_jobs (topic, idempotency_key) WHERE idempotency_key IS NOT NULL AND idempotency_key != ''`); err != nil {
		return fmt.Errorf("create outbox idempotency index: %w", err)
	}
	return nil
}

func (s *Store) getSQL(ctx context.Context, id string) (Job, bool, error) {
	return getSQL(ctx, storeQuerier{s.db}, id)
}

func getSQL(ctx context.Context, querier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}, id string) (Job, bool, error) {
	job, err := scanJob(querier.QueryRowContext(ctx, `
SELECT id, topic, payload, COALESCE(idempotency_key, ''), status, COALESCE(worker_id, ''), attempts, max_attempts, next_attempt_at, locked_until, COALESCE(last_error, '')
FROM platform_outbox_jobs WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Job{}, false, nil
	}
	if err != nil {
		return Job{}, false, fmt.Errorf("get outbox job: %w", err)
	}
	return job, true, nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanJob(row rowScanner) (Job, error) {
	var job Job
	var status string
	var nextAttemptAt int64
	var lockedUntil sql.NullInt64
	err := row.Scan(
		&job.ID, &job.Topic, &job.Payload, &job.IdempotencyKey, &status, &job.WorkerID, &job.Attempts, &job.MaxAttempts,
		&nextAttemptAt, &lockedUntil, &job.LastError,
	)
	if err != nil {
		return Job{}, err
	}
	job.Status = Status(status)
	if nextAttemptAt > 0 {
		job.NextAttemptAt = time.Unix(0, nextAttemptAt).UTC()
	}
	if lockedUntil.Valid && lockedUntil.Int64 > 0 {
		job.LockedUntil = time.Unix(0, lockedUntil.Int64).UTC()
	}
	return job, nil
}

type storeQuerier struct {
	store *db.Store
}

func (q storeQuerier) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	return q.store.QueryRow(ctx, query, args...)
}

func (s *Store) init(ctx context.Context) error {
	if _, err := s.db.Exec(ctx, createOutboxTableSQL(s.db.Dialect())); err != nil {
		return fmt.Errorf("create platform_outbox_jobs: %w", err)
	}
	if s.db.Dialect() == db.DialectSQLite {
		if _, err := s.db.Exec(ctx, `ALTER TABLE platform_outbox_jobs ADD COLUMN locked_until INTEGER`); err != nil && !isDuplicateColumn(err) {
			return fmt.Errorf("add outbox locked_until column: %w", err)
		}
		if _, err := s.db.Exec(ctx, `ALTER TABLE platform_outbox_jobs ADD COLUMN idempotency_key TEXT`); err != nil && !isDuplicateColumn(err) {
			return fmt.Errorf("add outbox idempotency_key column: %w", err)
		}
	}
	if _, err := s.db.Exec(ctx, `CREATE INDEX IF NOT EXISTS idx_platform_outbox_pending ON platform_outbox_jobs (next_attempt_at, created_at) WHERE status = 'pending'`); err != nil {
		return fmt.Errorf("create outbox pending index: %w", err)
	}
	if _, err := s.db.Exec(ctx, `CREATE INDEX IF NOT EXISTS idx_platform_outbox_locked_until ON platform_outbox_jobs (locked_until) WHERE status = 'running'`); err != nil {
		return fmt.Errorf("create outbox locked_until index: %w", err)
	}
	if _, err := s.db.Exec(ctx, `CREATE UNIQUE INDEX IF NOT EXISTS idx_platform_outbox_idempotency ON platform_outbox_jobs (topic, idempotency_key) WHERE idempotency_key IS NOT NULL AND idempotency_key != ''`); err != nil {
		return fmt.Errorf("create outbox idempotency index: %w", err)
	}
	return nil
}

func timeToNano(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t.UTC().UnixNano()
}

func isDuplicateColumn(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "duplicate column")
}

func nullString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func enqueueSQL(dialect db.Dialect) string {
	prefix := "INSERT OR IGNORE"
	suffix := ""
	if dialect == db.DialectPostgres {
		prefix = "INSERT"
		suffix = " ON CONFLICT DO NOTHING"
	}
	return fmt.Sprintf(`
%s INTO platform_outbox_jobs (
	id, topic, payload, idempotency_key, status, worker_id, attempts, max_attempts,
	next_attempt_at, locked_until, last_error, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)%s
`, prefix, suffix)
}

func postgresClaimSQL() string {
	return `
WITH candidate AS (
	SELECT id
	FROM platform_outbox_jobs
	WHERE status = ? AND next_attempt_at <= ?
	ORDER BY next_attempt_at ASC, created_at ASC
	FOR UPDATE SKIP LOCKED
	LIMIT 1
)
UPDATE platform_outbox_jobs AS jobs
SET status = ?, worker_id = ?, locked_until = ?, updated_at = ?
FROM candidate
WHERE jobs.id = candidate.id
RETURNING jobs.id, jobs.topic, jobs.payload, COALESCE(jobs.idempotency_key, ''), jobs.status,
	COALESCE(jobs.worker_id, ''), jobs.attempts, jobs.max_attempts, jobs.next_attempt_at,
	jobs.locked_until, COALESCE(jobs.last_error, '')`
}

func postgresRecoverStaleSQL() string {
	return `
WITH candidates AS (
	SELECT id
	FROM platform_outbox_jobs
	WHERE status = ? AND locked_until IS NOT NULL AND locked_until <= ?
	ORDER BY locked_until ASC
	FOR UPDATE SKIP LOCKED
	LIMIT ?
)
UPDATE platform_outbox_jobs AS jobs
SET status = CASE WHEN jobs.attempts + 1 >= jobs.max_attempts THEN ? ELSE ? END,
	worker_id = NULL,
	locked_until = NULL,
	attempts = jobs.attempts + 1,
	next_attempt_at = CASE WHEN jobs.attempts + 1 >= jobs.max_attempts THEN 0::BIGINT ELSE ?::BIGINT END,
	last_error = ?,
	updated_at = ?
FROM candidates
WHERE jobs.id = candidates.id AND jobs.status = ? AND jobs.locked_until IS NOT NULL AND jobs.locked_until <= ?`
}

func createOutboxTableSQL(dialect db.Dialect) string {
	payloadType := "BLOB"
	timeType := "INTEGER"
	if dialect == db.DialectPostgres {
		payloadType = "BYTEA"
		timeType = "BIGINT"
	}
	return fmt.Sprintf(`
CREATE TABLE IF NOT EXISTS platform_outbox_jobs (
	id TEXT PRIMARY KEY,
	topic TEXT NOT NULL,
	payload %s,
	idempotency_key TEXT,
	status TEXT NOT NULL,
	worker_id TEXT,
	attempts INTEGER NOT NULL DEFAULT 0,
	max_attempts INTEGER NOT NULL,
	next_attempt_at %[2]s NOT NULL DEFAULT 0,
	locked_until %[2]s,
	last_error TEXT,
	created_at %[2]s NOT NULL,
	updated_at %[2]s NOT NULL
)`, payloadType, timeType)
}

func txDialect(tx db.Tx) db.Dialect {
	if dialecter, ok := tx.(interface{ Dialect() db.Dialect }); ok {
		return dialecter.Dialect()
	}
	return db.DialectSQLite
}

func requireAffected(result sql.Result, operation string) error {
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("%s rows affected: %w", operation, err)
	}
	if affected == 0 {
		return fmt.Errorf("%s: no matching row", operation)
	}
	return nil
}
