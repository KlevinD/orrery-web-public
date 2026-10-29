package realtime

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"orrery_v2/internal/platform/db"
)

const appendRetryLimit = 20

type Store struct {
	db          *db.Store
	now         func() time.Time
	leaseTTL    time.Duration
	maxAttempts int
}

func NewStore(ctx context.Context, store *db.Store, opts StoreOptions) (*Store, error) {
	if store == nil {
		return nil, fmt.Errorf("realtime outbox requires database")
	}
	now := opts.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	leaseTTL := opts.LeaseTTL
	if leaseTTL <= 0 {
		leaseTTL = 30 * time.Second
	}
	maxAttempts := opts.MaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = 5
	}
	outbox := &Store{db: store, now: now, leaseTTL: leaseTTL, maxAttempts: maxAttempts}
	if err := outbox.init(ctx); err != nil {
		return nil, err
	}
	return outbox, nil
}

func (s *Store) Append(ctx context.Context, event AppendEvent) (Envelope, error) {
	if err := ctx.Err(); err != nil {
		return Envelope{}, err
	}
	if len(event.Payload) == 0 {
		event.Payload = json.RawMessage(`{}`)
	}
	var lastErr error
	for attempt := 0; attempt < appendRetryLimit; attempt++ {
		now := s.now().UTC()
		var envelope Envelope
		err := s.db.WithTx(ctx, func(tx db.Tx) error {
			version, err := s.nextChannelVersion(ctx, tx, event.Channel)
			if err != nil {
				return err
			}
			envelope = Envelope{
				EventID:       event.EventID,
				Type:          event.Type,
				Channel:       event.Channel,
				AggregateType: event.AggregateType,
				AggregateID:   event.AggregateID,
				Version:       version,
				OccurredAt:    now,
				Payload:       append(json.RawMessage(nil), event.Payload...),
			}
			payload, err := envelope.MarshalPayload()
			if err != nil {
				return err
			}
			_, err = tx.ExecContext(ctx, `
INSERT INTO realtime_outbox (
	id, channel, aggregate_type, aggregate_id, event_type, payload_json, version, status,
	attempts, max_attempts, next_attempt_at, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, 0, ?, ?, ?, ?)`,
				envelope.EventID, envelope.Channel, envelope.AggregateType, envelope.AggregateID, envelope.Type, payload,
				envelope.Version, string(StatusPending), s.maxAttempts, now.UnixNano(), now.UnixNano(), now.UnixNano(),
			)
			if err != nil {
				return fmt.Errorf("append realtime outbox: %w", err)
			}
			return nil
		})
		if err != nil {
			if isDuplicateEventIDError(err) {
				existing, ok, getErr := s.Get(ctx, event.EventID)
				if getErr != nil {
					return Envelope{}, getErr
				}
				if ok {
					return existing.Envelope, nil
				}
			}
			if isRetryableAppendError(err) {
				lastErr = err
				delay := time.Duration(attempt+1) * 5 * time.Millisecond
				if delay > 50*time.Millisecond {
					delay = 50 * time.Millisecond
				}
				timer := time.NewTimer(delay)
				select {
				case <-ctx.Done():
					timer.Stop()
					return Envelope{}, ctx.Err()
				case <-timer.C:
				}
				continue
			}
			return Envelope{}, err
		}
		return envelope, nil
	}
	return Envelope{}, fmt.Errorf("append realtime outbox after retries: %w", lastErr)
}

func (s *Store) Claim(ctx context.Context, workerID string, limit int) ([]OutboxEvent, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if workerID == "" {
		return nil, fmt.Errorf("worker id is required")
	}
	if limit <= 0 {
		limit = 1
	}
	now := s.now().UTC()
	leaseUntil := now.Add(s.leaseTTL)
	var ids []string
	err := s.db.WithTx(ctx, func(tx db.Tx) error {
		var candidateIDs []string
		rows, err := tx.QueryContext(ctx, `
SELECT id FROM realtime_outbox
WHERE status = ? AND next_attempt_at <= ?
ORDER BY next_attempt_at ASC, created_at ASC
LIMIT ?`, string(StatusPending), now.UnixNano(), limit)
		if err != nil {
			return fmt.Errorf("select realtime outbox claims: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				return fmt.Errorf("scan realtime outbox claim id: %w", err)
			}
			candidateIDs = append(candidateIDs, id)
		}
		if err := rows.Err(); err != nil {
			return fmt.Errorf("iterate realtime outbox claim ids: %w", err)
		}
		for _, id := range candidateIDs {
			result, err := tx.ExecContext(ctx, `
UPDATE realtime_outbox
SET status = ?, worker_id = ?, locked_until = ?, updated_at = ?
WHERE id = ? AND status = ?`,
				string(StatusPublishing), workerID, leaseUntil.UnixNano(), now.UnixNano(), id, string(StatusPending),
			)
			if err != nil {
				return fmt.Errorf("claim realtime outbox event: %w", err)
			}
			affected, err := result.RowsAffected()
			if err != nil {
				return fmt.Errorf("read realtime outbox claim affected rows: %w", err)
			}
			if affected > 0 {
				ids = append(ids, id)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	events := make([]OutboxEvent, 0, len(ids))
	for _, id := range ids {
		event, ok, err := s.Get(ctx, id)
		if err != nil {
			return nil, err
		}
		if ok {
			events = append(events, event)
		}
	}
	return events, nil
}

func (s *Store) MarkPublished(ctx context.Context, id, workerID string) error {
	now := s.now().UTC()
	result, err := s.db.Exec(ctx, `
UPDATE realtime_outbox
SET status = ?, worker_id = NULL, locked_until = NULL, published_at = ?, updated_at = ?
WHERE id = ? AND status = ? AND worker_id = ?`,
		string(StatusPublished), now.UnixNano(), now.UnixNano(), id, string(StatusPublishing), workerID,
	)
	if err != nil {
		return fmt.Errorf("mark realtime outbox published: %w", err)
	}
	return requireAffected(result, "mark realtime outbox published")
}

func (s *Store) MarkFailed(ctx context.Context, id, workerID, message string) error {
	now := s.now().UTC()
	err := s.db.WithTx(ctx, func(tx db.Tx) error {
		var attempts, maxAttempts int
		err := tx.QueryRowContext(ctx, `
SELECT attempts, max_attempts FROM realtime_outbox
WHERE id = ? AND status = ? AND worker_id = ?`, id, string(StatusPublishing), workerID).Scan(&attempts, &maxAttempts)
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("realtime outbox event not claimed by worker")
		}
		if err != nil {
			return fmt.Errorf("load realtime outbox attempts: %w", err)
		}
		attempts++
		status := StatusPending
		if attempts >= maxAttempts {
			status = StatusFailed
		}
		nextAttemptAt := now.Add(time.Duration(attempts) * time.Second)
		result, err := tx.ExecContext(ctx, `
UPDATE realtime_outbox
SET status = ?, attempts = ?, worker_id = NULL, locked_until = NULL, next_attempt_at = ?, last_error = ?, updated_at = ?
WHERE id = ? AND status = ? AND worker_id = ?`,
			string(status), attempts, nextAttemptAt.UnixNano(), message, now.UnixNano(),
			id, string(StatusPublishing), workerID,
		)
		if err != nil {
			return fmt.Errorf("mark realtime outbox failed: %w", err)
		}
		return requireAffected(result, "mark realtime outbox failed")
	})
	if err != nil {
		return fmt.Errorf("mark realtime outbox failed: %w", err)
	}
	return nil
}

func (s *Store) RecoverExpired(ctx context.Context, limit int) (int, error) {
	if limit <= 0 {
		limit = 100
	}
	now := s.now().UTC()
	result, err := s.db.Exec(ctx, `
UPDATE realtime_outbox
SET status = ?, worker_id = NULL, locked_until = NULL, next_attempt_at = ?, updated_at = ?
WHERE id IN (
	SELECT id FROM realtime_outbox
	WHERE status = ? AND locked_until <= ?
	ORDER BY locked_until ASC
	LIMIT ?
)`,
		string(StatusPending), now.UnixNano(), now.UnixNano(), string(StatusPublishing), now.UnixNano(), limit,
	)
	if err != nil {
		return 0, fmt.Errorf("recover expired realtime outbox events: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("read realtime outbox recover affected rows: %w", err)
	}
	return int(affected), nil
}

func (s *Store) Get(ctx context.Context, id string) (OutboxEvent, bool, error) {
	var event OutboxEvent
	var envelope Envelope
	var payload []byte
	var status string
	var worker sql.NullString
	var lockedUntil, publishedAt sql.NullInt64
	var lastError sql.NullString
	var occurredAt, createdAt, updatedAt int64
	err := s.db.QueryRow(ctx, `
SELECT id, payload_json, status, attempts, max_attempts, worker_id, locked_until, last_error,
	created_at, updated_at, published_at
FROM realtime_outbox WHERE id = ?`, id).Scan(
		&event.ID, &payload, &status, &event.Attempts, &event.MaxAttempts, &worker, &lockedUntil, &lastError,
		&createdAt, &updatedAt, &publishedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return OutboxEvent{}, false, nil
	}
	if err != nil {
		return OutboxEvent{}, false, fmt.Errorf("get realtime outbox event: %w", err)
	}
	if err := json.Unmarshal(payload, &envelope); err != nil {
		return OutboxEvent{}, false, fmt.Errorf("decode realtime outbox envelope: %w", err)
	}
	occurredAt = envelope.OccurredAt.UTC().UnixNano()
	event.Envelope = envelope
	event.Status = Status(status)
	event.WorkerID = nullString(worker)
	event.LeaseUntil = nullTime(lockedUntil)
	event.LastError = nullString(lastError)
	event.CreatedAt = time.Unix(0, createdAt).UTC()
	event.UpdatedAt = time.Unix(0, updatedAt).UTC()
	event.PublishedAt = nullTime(publishedAt)
	if occurredAt == 0 {
		event.Envelope.OccurredAt = event.CreatedAt
	}
	return event, true, nil
}

func (s *Store) nextChannelVersion(ctx context.Context, tx db.Tx, channel string) (int64, error) {
	var current sql.NullInt64
	err := tx.QueryRowContext(ctx, `SELECT MAX(version) FROM realtime_outbox WHERE channel = ?`, channel).Scan(&current)
	if err != nil {
		return 0, fmt.Errorf("load realtime outbox channel version: %w", err)
	}
	if !current.Valid {
		return 1, nil
	}
	return current.Int64 + 1, nil
}

func (s *Store) init(ctx context.Context) error {
	for _, statement := range outboxSchemaStatements(s.db.Dialect()) {
		if _, err := s.db.Exec(ctx, statement); err != nil {
			return fmt.Errorf("apply realtime outbox schema: %w", err)
		}
	}
	return nil
}

func outboxSchemaStatements(dialect db.Dialect) []string {
	payloadType := "BLOB"
	timeType := "INTEGER"
	if dialect == db.DialectPostgres {
		payloadType = "BYTEA"
		timeType = "BIGINT"
	}
	return []string{
		fmt.Sprintf(`
CREATE TABLE IF NOT EXISTS realtime_outbox (
	id TEXT PRIMARY KEY,
	channel TEXT NOT NULL,
	aggregate_type TEXT NOT NULL,
	aggregate_id TEXT NOT NULL,
	event_type TEXT NOT NULL,
	payload_json %s NOT NULL,
	version %[2]s NOT NULL,
	status TEXT NOT NULL,
	attempts INTEGER NOT NULL DEFAULT 0,
	max_attempts INTEGER NOT NULL,
	worker_id TEXT,
	locked_until %[2]s,
	next_attempt_at %[2]s NOT NULL,
	last_error TEXT,
	created_at %[2]s NOT NULL,
	updated_at %[2]s NOT NULL,
	published_at %[2]s
)`, payloadType, timeType),
		`CREATE INDEX IF NOT EXISTS idx_realtime_outbox_claim ON realtime_outbox (next_attempt_at, created_at) WHERE status = 'pending'`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_realtime_outbox_channel_version ON realtime_outbox (channel, version)`,
		`CREATE INDEX IF NOT EXISTS idx_realtime_outbox_aggregate ON realtime_outbox (aggregate_type, aggregate_id, created_at)`,
		`CREATE INDEX IF NOT EXISTS idx_realtime_outbox_status ON realtime_outbox (status, updated_at)`,
	}
}

func requireAffected(result sql.Result, action string) error {
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("%s rows affected: %w", action, err)
	}
	if affected == 0 {
		return fmt.Errorf("%s: no rows affected", action)
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

func isRetryableAppendError(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "database is locked") ||
		strings.Contains(message, "sqlite_busy") ||
		strings.Contains(message, "realtime_outbox.channel, realtime_outbox.version") ||
		strings.Contains(message, "idx_realtime_outbox_channel_version")
}

func isDuplicateEventIDError(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "unique constraint failed: realtime_outbox.id") ||
		strings.Contains(message, "constraint failed: realtime_outbox.id") ||
		strings.Contains(message, "realtime_outbox_pkey")
}
