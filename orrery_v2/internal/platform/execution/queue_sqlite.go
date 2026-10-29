package execution

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"orrery_v2/internal/platform/db"
)

type QueueConfig struct {
	ClaimTTL       time.Duration
	MaxAttempts    int
	SweepBatchSize int
}

type Queue struct {
	db             *db.Store
	claimTTL       time.Duration
	maxAttempts    int
	sweepBatchSize int
	activeMu       sync.Mutex
	activeHandlers map[string]*activeQueueHandler
}

type activeQueueHandler struct {
	cancel context.CancelFunc
}

func NewQueue(ctx context.Context, store *db.Store, cfg QueueConfig) (*Queue, error) {
	if store == nil {
		return nil, fmt.Errorf("sqlite queue requires database")
	}
	if cfg.ClaimTTL <= 0 {
		cfg.ClaimTTL = 30 * time.Second
	}
	if cfg.MaxAttempts <= 0 {
		cfg.MaxAttempts = 3
	}
	if cfg.SweepBatchSize <= 0 {
		cfg.SweepBatchSize = 100
	}
	q := &Queue{
		db: store, claimTTL: cfg.ClaimTTL, maxAttempts: cfg.MaxAttempts, sweepBatchSize: cfg.SweepBatchSize,
		activeHandlers: make(map[string]*activeQueueHandler),
	}
	if err := q.init(ctx); err != nil {
		return nil, err
	}
	return q, nil
}

func (q *Queue) Enqueue(ctx context.Context, job Job, now time.Time) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if job.ID == "" {
		return fmt.Errorf("job id is required")
	}
	if job.Topic == "" {
		return fmt.Errorf("job topic is required")
	}
	maxAttempts := job.MaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = q.maxAttempts
	}
	nextAttemptAt := job.NextAttemptAt
	if nextAttemptAt.IsZero() {
		nextAttemptAt = now
	}
	_, err := q.db.Exec(ctx, `
INSERT INTO platform_queue_jobs (
	id, topic, payload, queue_key, status, priority, attempts, max_attempts, next_attempt_at, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, 0, ?, ?, ?, ?)
ON CONFLICT(id) DO NOTHING`,
		job.ID, job.Topic, job.Payload, normalizedQueueKey(job.QueueKey), string(StatusQueued), job.Priority, maxAttempts, nextAttemptAt.UTC().UnixNano(), now.UTC().UnixNano(), now.UTC().UnixNano(),
	)
	if err != nil {
		return fmt.Errorf("enqueue sqlite job: %w", err)
	}
	return nil
}

func (q *Queue) Claim(ctx context.Context, workerID string, now time.Time) (Job, bool, error) {
	if err := ctx.Err(); err != nil {
		return Job{}, false, err
	}
	if workerID == "" {
		return Job{}, false, fmt.Errorf("worker id is required")
	}
	nowNano := now.UTC().UnixNano()
	lockedUntil := now.UTC().Add(q.claimTTL).UnixNano()
	var id string
	err := q.db.WithTx(ctx, func(tx db.Tx) error {
		row := tx.QueryRowContext(ctx, `
SELECT candidate.id FROM platform_queue_jobs candidate
WHERE candidate.status = ? AND candidate.next_attempt_at <= ?
AND NOT EXISTS (
	SELECT 1 FROM platform_queue_jobs active
	WHERE active.queue_key = candidate.queue_key
	AND active.id != candidate.id
	AND (
		active.status IN (?, ?)
		OR (active.status = ? AND active.attempts > 0 AND (active.created_at < candidate.created_at OR (active.created_at = candidate.created_at AND active.id < candidate.id)))
	)
)
ORDER BY candidate.priority DESC, candidate.created_at ASC
LIMIT 1`, string(StatusQueued), nowNano, string(StatusDispatched), string(StatusRunning), string(StatusQueued))
		if err := row.Scan(&id); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil
			}
			return fmt.Errorf("select sqlite queued job: %w", err)
		}
		result, err := tx.ExecContext(ctx, `
UPDATE platform_queue_jobs
SET status = ?, worker_id = ?, locked_until = ?, dispatched_at = ?, updated_at = ?
WHERE id = ? AND status = ?`,
			string(StatusDispatched), workerID, lockedUntil, nowNano, nowNano, id, string(StatusQueued),
		)
		if err != nil {
			return fmt.Errorf("claim sqlite job: %w", err)
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("read claim rows affected: %w", err)
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
	job, ok, err := q.Get(ctx, id)
	return job, ok, err
}

func (q *Queue) Start(ctx context.Context, jobID, workerID string, now time.Time) error {
	nowNano := now.UTC().UnixNano()
	lockedUntil := now.UTC().Add(q.claimTTL).UnixNano()
	result, err := q.db.Exec(ctx, `
UPDATE platform_queue_jobs
SET status = ?, locked_until = ?, started_at = ?, updated_at = ?
WHERE id = ? AND status = ? AND worker_id = ?`,
		string(StatusRunning), lockedUntil, nowNano, nowNano, jobID, string(StatusDispatched), workerID,
	)
	if err != nil {
		return fmt.Errorf("start sqlite job: %w", err)
	}
	return requireAffected(result, "start sqlite job")
}

func (q *Queue) ExtendLease(ctx context.Context, jobID, workerID string, leaseUntil time.Time) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if jobID == "" {
		return fmt.Errorf("job id is required")
	}
	if workerID == "" {
		return fmt.Errorf("worker id is required")
	}
	if leaseUntil.IsZero() {
		return fmt.Errorf("lease until is required")
	}
	result, err := q.db.Exec(ctx, `
UPDATE platform_queue_jobs
SET locked_until = ?, updated_at = ?
WHERE id = ? AND status IN (?, ?) AND worker_id = ?`,
		leaseUntil.UTC().UnixNano(), leaseUntil.UTC().UnixNano(),
		jobID, string(StatusDispatched), string(StatusRunning), workerID,
	)
	if err != nil {
		return fmt.Errorf("extend sqlite job lease: %w", err)
	}
	return requireAffected(result, "extend sqlite job lease")
}

func (q *Queue) renewLease(ctx context.Context, jobID, workerID string, now time.Time) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if jobID == "" {
		return fmt.Errorf("job id is required")
	}
	if workerID == "" {
		return fmt.Errorf("worker id is required")
	}
	leaseUntil := now.UTC().Add(q.claimTTL).UnixNano()
	result, err := q.db.Exec(ctx, `
UPDATE platform_queue_jobs
SET locked_until = CASE WHEN locked_until IS NULL OR locked_until < ? THEN ? ELSE locked_until END,
	updated_at = ?
WHERE id = ? AND status IN (?, ?) AND worker_id = ?`,
		leaseUntil, leaseUntil, now.UTC().UnixNano(),
		jobID, string(StatusDispatched), string(StatusRunning), workerID,
	)
	if err != nil {
		return fmt.Errorf("renew sqlite job lease: %w", err)
	}
	return requireAffected(result, "renew sqlite job lease")
}

func (q *Queue) Complete(ctx context.Context, jobID, workerID string, now time.Time) error {
	result, err := q.db.Exec(ctx, `
UPDATE platform_queue_jobs
SET status = ?, locked_until = NULL, completed_at = ?, updated_at = ?
WHERE id = ? AND status = ? AND worker_id = ?`,
		string(StatusCompleted), now.UTC().UnixNano(), now.UTC().UnixNano(), jobID, string(StatusRunning), workerID,
	)
	if err != nil {
		return fmt.Errorf("complete sqlite job: %w", err)
	}
	return requireAffected(result, "complete sqlite job")
}

func (q *Queue) Fail(ctx context.Context, jobID, workerID, message string, now time.Time) error {
	return q.FailAt(ctx, jobID, workerID, message, now, now)
}

func (q *Queue) FailAt(ctx context.Context, jobID, workerID, message string, now, nextAttemptAt time.Time) error {
	if nextAttemptAt.Before(now) {
		nextAttemptAt = now
	}
	err := q.db.WithTx(ctx, func(tx db.Tx) error {
		var attempts, maxAttempts int
		err := tx.QueryRowContext(ctx, `
SELECT attempts, max_attempts FROM platform_queue_jobs
WHERE id = ? AND status IN (?, ?) AND worker_id = ?`,
			jobID, string(StatusDispatched), string(StatusRunning), workerID,
		).Scan(&attempts, &maxAttempts)
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("fail sqlite job: no active job for worker")
		}
		if err != nil {
			return fmt.Errorf("load sqlite job attempts: %w", err)
		}
		attempts++
		status := StatusQueued
		worker := any(nil)
		lockedUntil := any(nil)
		completedAt := any(nil)
		failureReason := any(nil)
		if attempts >= maxAttempts {
			status = StatusFailed
			completedAt = now.UTC().UnixNano()
			failureReason = "max_attempts_exhausted"
		}
		result, err := tx.ExecContext(ctx, `
UPDATE platform_queue_jobs
SET status = ?, worker_id = ?, locked_until = ?, attempts = ?, completed_at = ?, last_error = ?, failure_reason = ?, next_attempt_at = ?,
	dispatched_at = CASE WHEN ? = ? THEN NULL ELSE dispatched_at END,
	started_at = CASE WHEN ? = ? THEN NULL ELSE started_at END,
	updated_at = ?
WHERE id = ? AND status IN (?, ?) AND worker_id = ?`,
			string(status), worker, lockedUntil, attempts, completedAt, message, failureReason, nextAttemptAt.UTC().UnixNano(),
			string(status), string(StatusQueued), string(status), string(StatusQueued), now.UTC().UnixNano(),
			jobID, string(StatusDispatched), string(StatusRunning), workerID,
		)
		if err != nil {
			return fmt.Errorf("fail sqlite job: %w", err)
		}
		return requireAffected(result, "fail sqlite job")
	})
	if err != nil {
		return fmt.Errorf("fail sqlite job: %w", err)
	}
	return nil
}

func (q *Queue) FailPermanently(ctx context.Context, jobID, workerID, message string, now time.Time) error {
	result, err := q.db.Exec(ctx, `
UPDATE platform_queue_jobs
SET status = ?, worker_id = NULL, locked_until = NULL, attempts = attempts + 1, completed_at = ?, last_error = ?, failure_reason = ?, updated_at = ?
WHERE id = ? AND status IN (?, ?) AND worker_id = ?`,
		string(StatusFailed), now.UTC().UnixNano(), message, "permanent_failure", now.UTC().UnixNano(),
		jobID, string(StatusDispatched), string(StatusRunning), workerID,
	)
	if err != nil {
		return fmt.Errorf("fail sqlite job permanently: %w", err)
	}
	return requireAffected(result, "fail sqlite job permanently")
}

func (q *Queue) Cancel(ctx context.Context, jobID, reason string, now time.Time) error {
	_, err := q.CancelWithResult(ctx, jobID, reason, now)
	return err
}

func (q *Queue) CancelWithResult(ctx context.Context, jobID, reason string, now time.Time) (CancelResult, error) {
	if reason == "" {
		reason = "cancelled"
	}
	result, err := q.db.Exec(ctx, `
UPDATE platform_queue_jobs
SET status = ?, worker_id = NULL, locked_until = NULL, failure_reason = ?, completed_at = ?, updated_at = ?
WHERE id = ? AND status IN (?, ?, ?)`,
		string(StatusCancelled), reason, now.UTC().UnixNano(), now.UTC().UnixNano(), jobID,
		string(StatusQueued), string(StatusDispatched), string(StatusRunning),
	)
	if err != nil {
		return CancelResult{}, fmt.Errorf("cancel sqlite job: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return CancelResult{}, fmt.Errorf("read cancel sqlite job rows affected: %w", err)
	}
	if affected == 1 {
		q.cancelActiveHandler(jobID)
		return CancelResult{Disposition: CancelApplied, Status: StatusCancelled}, nil
	}
	if affected != 0 {
		return CancelResult{}, fmt.Errorf("cancel sqlite job affected %d rows", affected)
	}
	{
		job, ok, getErr := q.Get(ctx, jobID)
		if getErr != nil {
			return CancelResult{}, getErr
		}
		if !ok {
			q.cancelActiveHandler(jobID)
			return CancelResult{Disposition: CancelMissing}, nil
		}
		switch job.Status {
		case StatusCancelled:
			q.cancelActiveHandler(jobID)
			return CancelResult{Disposition: CancelAlreadyCancelled, Status: job.Status}, nil
		case StatusCompleted, StatusFailed, StatusDeadLetter:
			q.cancelActiveHandler(jobID)
			return CancelResult{Disposition: CancelAlreadyTerminal, Status: job.Status}, nil
		default:
			return CancelResult{}, fmt.Errorf("cancel sqlite job did not update active job %s in status %s", jobID, job.Status)
		}
	}
}

func (q *Queue) registerActiveHandler(ctx context.Context, jobID string, cancel context.CancelFunc) (*activeQueueHandler, error) {
	if q == nil || strings.TrimSpace(jobID) == "" || cancel == nil {
		return nil, fmt.Errorf("active queue handler registration is invalid")
	}
	registration := &activeQueueHandler{cancel: cancel}
	q.activeMu.Lock()
	q.activeHandlers[jobID] = registration
	q.activeMu.Unlock()
	job, ok, err := q.Get(ctx, jobID)
	if err != nil {
		q.unregisterActiveHandler(jobID, registration)
		return nil, err
	}
	if !ok || job.Status == StatusCancelled {
		cancel()
	}
	return registration, nil
}

func (q *Queue) unregisterActiveHandler(jobID string, registration *activeQueueHandler) {
	if q == nil {
		return
	}
	q.activeMu.Lock()
	if q.activeHandlers[jobID] == registration {
		delete(q.activeHandlers, jobID)
	}
	q.activeMu.Unlock()
}

func (q *Queue) cancelActiveHandler(jobID string) {
	if q == nil {
		return
	}
	q.activeMu.Lock()
	registration := q.activeHandlers[jobID]
	q.activeMu.Unlock()
	if registration != nil {
		registration.cancel()
	}
}

func (q *Queue) RecoverExpired(ctx context.Context, now time.Time, batchSize int) (int, error) {
	if batchSize <= 0 {
		batchSize = q.sweepBatchSize
	}
	recovered := 0
	nullTime := queueNullTimeSQL(q.db.Dialect())
	err := q.db.WithTx(ctx, func(tx db.Tx) error {
		result, err := tx.ExecContext(ctx, `
UPDATE platform_queue_jobs
SET status = ?, worker_id = NULL, locked_until = NULL, dispatched_at = NULL, started_at = NULL, updated_at = ?
WHERE id IN (
	SELECT id FROM platform_queue_jobs
	WHERE status = ? AND locked_until <= ?
	ORDER BY locked_until ASC
	LIMIT ?
)`,
			string(StatusQueued), now.UTC().UnixNano(), string(StatusDispatched), now.UTC().UnixNano(), batchSize,
		)
		if err != nil {
			return fmt.Errorf("recover expired dispatched sqlite jobs: %w", err)
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("read dispatched recover rows affected: %w", err)
		}
		recovered += int(affected)
		remaining := batchSize - int(affected)
		if remaining <= 0 {
			return nil
		}
		result, err = tx.ExecContext(ctx, `
UPDATE platform_queue_jobs
SET status = CASE WHEN attempts + 1 >= max_attempts THEN ? ELSE ? END,
	worker_id = NULL,
	locked_until = NULL,
	attempts = attempts + 1,
	completed_at = CASE WHEN attempts + 1 >= max_attempts THEN ? ELSE `+nullTime+` END,
	last_error = ?,
	failure_reason = ?,
	dispatched_at = CASE WHEN attempts + 1 >= max_attempts THEN dispatched_at ELSE `+nullTime+` END,
	started_at = CASE WHEN attempts + 1 >= max_attempts THEN started_at ELSE `+nullTime+` END,
	updated_at = ?
WHERE id IN (
	SELECT id FROM platform_queue_jobs
	WHERE status = ? AND locked_until <= ?
	ORDER BY locked_until ASC
	LIMIT ?
)`,
			string(StatusFailed), string(StatusQueued), now.UTC().UnixNano(), "job lease expired", "timeout", now.UTC().UnixNano(),
			string(StatusRunning), now.UTC().UnixNano(), remaining,
		)
		if err != nil {
			return fmt.Errorf("recover expired running sqlite jobs: %w", err)
		}
		affected, err = result.RowsAffected()
		if err != nil {
			return fmt.Errorf("read running recover rows affected: %w", err)
		}
		recovered += int(affected)
		return nil
	})
	if err != nil {
		return 0, err
	}
	return recovered, nil
}

func queueNullTimeSQL(dialect db.Dialect) string {
	if dialect == db.DialectPostgres {
		return "CAST(NULL AS BIGINT)"
	}
	return "NULL"
}

func (q *Queue) ExpireQueued(ctx context.Context, olderThan time.Time, batchSize int, reason string, now time.Time) (int, error) {
	if batchSize <= 0 {
		batchSize = q.sweepBatchSize
	}
	if reason == "" {
		reason = "queued_expired"
	}
	result, err := q.db.Exec(ctx, `
UPDATE platform_queue_jobs
SET status = ?, worker_id = NULL, locked_until = NULL, completed_at = ?, last_error = ?, failure_reason = ?, updated_at = ?
WHERE id IN (
	SELECT id FROM platform_queue_jobs
	WHERE status = ? AND created_at <= ?
	ORDER BY created_at ASC
	LIMIT ?
)`,
		string(StatusFailed), now.UTC().UnixNano(), reason, reason, now.UTC().UnixNano(),
		string(StatusQueued), olderThan.UTC().UnixNano(), batchSize,
	)
	if err != nil {
		return 0, fmt.Errorf("expire queued sqlite jobs: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("read expire queued rows affected: %w", err)
	}
	return int(affected), nil
}

func (q *Queue) Get(ctx context.Context, jobID string) (Job, bool, error) {
	var job Job
	var status string
	var worker sql.NullString
	var queueKey sql.NullString
	var lastError sql.NullString
	var failureReason sql.NullString
	var createdAt, updatedAt, dispatchedAt, startedAt, lockedUntil, completedAt, nextAttemptAt sql.NullInt64
	err := q.db.QueryRow(ctx, `
SELECT id, topic, payload, queue_key, status, priority, attempts, max_attempts,
	worker_id, last_error, failure_reason, next_attempt_at, created_at, updated_at, dispatched_at, started_at, locked_until, completed_at
FROM platform_queue_jobs WHERE id = ?`, jobID).Scan(
		&job.ID, &job.Topic, &job.Payload, &queueKey, &status, &job.Priority, &job.Attempts, &job.MaxAttempts,
		&worker, &lastError, &failureReason, &nextAttemptAt, &createdAt, &updatedAt, &dispatchedAt, &startedAt, &lockedUntil, &completedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return Job{}, false, nil
	}
	if err != nil {
		return Job{}, false, fmt.Errorf("get sqlite job: %w", err)
	}
	job.Status = Status(status)
	job.WorkerID = nullString(worker)
	job.QueueKey = nullString(queueKey)
	job.LastError = nullString(lastError)
	job.FailureReason = nullString(failureReason)
	job.NextAttemptAt = nullTime(nextAttemptAt)
	job.CreatedAt = nullTime(createdAt)
	job.UpdatedAt = nullTime(updatedAt)
	job.DispatchedAt = nullTime(dispatchedAt)
	job.StartedAt = nullTime(startedAt)
	job.LeaseUntil = nullTime(lockedUntil)
	job.CompletedAt = nullTime(completedAt)
	return job, true, nil
}

func (q *Queue) StatusCounts(ctx context.Context) (map[string]int64, error) {
	rows, err := q.db.Query(ctx, `SELECT status, COUNT(*) FROM platform_queue_jobs GROUP BY status`)
	if err != nil {
		return nil, fmt.Errorf("count sqlite jobs by status: %w", err)
	}
	defer rows.Close()
	counts := map[string]int64{}
	for rows.Next() {
		var status string
		var count int64
		if err := rows.Scan(&status, &count); err != nil {
			return nil, fmt.Errorf("scan sqlite job status count: %w", err)
		}
		counts[status] = count
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate sqlite job status counts: %w", err)
	}
	return counts, nil
}

func (q *Queue) init(ctx context.Context) error {
	if _, err := q.db.Exec(ctx, queueTableSQL(q.db.Dialect())); err != nil {
		return fmt.Errorf("create platform_queue_jobs: %w", err)
	}
	if q.db.Dialect() == db.DialectSQLite {
		for _, column := range []struct {
			name       string
			definition string
		}{
			{name: "failure_reason", definition: "TEXT"},
			{name: "dispatched_at", definition: "INTEGER"},
			{name: "started_at", definition: "INTEGER"},
			{name: "queue_key", definition: "TEXT NOT NULL DEFAULT 'default'"},
			{name: "next_attempt_at", definition: "INTEGER NOT NULL DEFAULT 0"},
		} {
			if err := q.ensureColumn(ctx, "platform_queue_jobs", column.name, column.definition); err != nil {
				return err
			}
		}
	}
	if q.db.Dialect() == db.DialectPostgres {
		if _, err := q.db.Exec(ctx, `ALTER TABLE platform_queue_jobs ADD COLUMN IF NOT EXISTS next_attempt_at BIGINT NOT NULL DEFAULT 0`); err != nil {
			return fmt.Errorf("add queue next_attempt_at column: %w", err)
		}
	}
	if _, err := q.db.Exec(ctx, `CREATE INDEX IF NOT EXISTS idx_platform_queue_ready ON platform_queue_jobs (next_attempt_at, priority DESC, created_at ASC) WHERE status = 'queued'`); err != nil {
		return fmt.Errorf("create queue claim index: %w", err)
	}
	if _, err := q.db.Exec(ctx, `CREATE INDEX IF NOT EXISTS idx_platform_queue_key_active ON platform_queue_jobs (queue_key, status) WHERE status IN ('dispatched', 'running')`); err != nil {
		return fmt.Errorf("create queue key active index: %w", err)
	}
	if _, err := q.db.Exec(ctx, `CREATE INDEX IF NOT EXISTS idx_platform_queue_locked_until ON platform_queue_jobs (locked_until) WHERE status IN ('dispatched', 'running')`); err != nil {
		return fmt.Errorf("create queue locked index: %w", err)
	}
	return nil
}

func queueTableSQL(dialect db.Dialect) string {
	payloadType := "BLOB"
	timeType := "INTEGER"
	if dialect == db.DialectPostgres {
		payloadType = "BYTEA"
		timeType = "BIGINT"
	}
	return fmt.Sprintf(`
CREATE TABLE IF NOT EXISTS platform_queue_jobs (
	id TEXT PRIMARY KEY,
	topic TEXT NOT NULL,
	payload %s,
	queue_key TEXT NOT NULL DEFAULT 'default',
	status TEXT NOT NULL,
	priority INTEGER NOT NULL DEFAULT 0,
	attempts INTEGER NOT NULL DEFAULT 0,
	max_attempts INTEGER NOT NULL,
	next_attempt_at %[2]s NOT NULL DEFAULT 0,
	worker_id TEXT,
	locked_until %[2]s,
	last_error TEXT,
	failure_reason TEXT,
	created_at %[2]s NOT NULL,
	updated_at %[2]s NOT NULL,
	dispatched_at %[2]s,
	started_at %[2]s,
	completed_at %[2]s
)
`, payloadType, timeType)
}

func normalizedQueueKey(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "default"
	}
	return value
}

func (q *Queue) ensureColumn(ctx context.Context, table, name, definition string) error {
	rows, err := q.db.Query(ctx, "PRAGMA table_info("+table+")")
	if err != nil {
		return fmt.Errorf("inspect %s columns: %w", table, err)
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var columnName, columnType string
		var notNull int
		var defaultValue any
		var pk int
		if err := rows.Scan(&cid, &columnName, &columnType, &notNull, &defaultValue, &pk); err != nil {
			return fmt.Errorf("scan %s columns: %w", table, err)
		}
		if columnName == name {
			return nil
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate %s columns: %w", table, err)
	}
	if _, err := q.db.Exec(ctx, "ALTER TABLE "+table+" ADD COLUMN "+name+" "+definition); err != nil {
		return fmt.Errorf("add %s.%s column: %w", table, name, err)
	}
	return nil
}

func nullString(value sql.NullString) string {
	if !value.Valid {
		return ""
	}
	return value.String
}

func nullTime(value sql.NullInt64) time.Time {
	if !value.Valid || value.Int64 == 0 {
		return time.Time{}
	}
	return time.Unix(0, value.Int64).UTC()
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
