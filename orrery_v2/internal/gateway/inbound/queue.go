package inbound

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"orrery_v2/internal/gateway/channel"
	platformdb "orrery_v2/internal/platform/db"
)

type rowScanner interface {
	Scan(dest ...any) error
}

const (
	inboundJobLease       = 5 * time.Minute
	inboundJobRetention   = 30 * 24 * time.Hour
	inboundJobMaxPayload  = 4 << 20
	inboundJobMaxAttempts = 12
	recentPayloadMaxRows  = 100
)

func (s *Store) EnsureQueueSchema(ctx context.Context) error {
	if s == nil || s.db == nil {
		return errors.New("gateway inbound job store is not configured")
	}
	s.schemaMu.Lock()
	defer s.schemaMu.Unlock()
	if s.queueSchemaReady {
		return nil
	}
	payloadType := "BLOB"
	timeType := "INTEGER"
	if s.db.Dialect() == platformdb.DialectPostgres {
		payloadType = "BYTEA"
		timeType = "BIGINT"
	}
	for _, statement := range queueMigrationStatements(payloadType, timeType) {
		if _, err := s.db.Exec(ctx, statement); err != nil {
			return fmt.Errorf("ensure gateway inbound job schema: %w", err)
		}
	}
	s.queueSchemaReady = true
	return nil
}

func (s *Store) EnqueueInboundJob(ctx context.Context, job channel.InboundJob) (bool, error) {
	job.ChannelKey = strings.ToLower(strings.TrimSpace(job.ChannelKey))
	job.AccountID = strings.TrimSpace(job.AccountID)
	job.EventID = strings.TrimSpace(job.EventID)
	if !validInboundEventKey(job.ChannelKey, job.AccountID, job.EventID) || len(job.Payload) == 0 || len(job.Payload) > inboundJobMaxPayload {
		return false, ErrInvalidInput
	}
	if err := s.EnsureQueueSchema(ctx); err != nil {
		return false, err
	}
	maxAttempts := job.MaxAttempts
	if maxAttempts <= 0 || maxAttempts > inboundJobMaxAttempts {
		maxAttempts = inboundJobMaxAttempts
	}
	now := s.now().UTC()
	result, err := s.db.Exec(ctx, `INSERT INTO gateway_inbound_jobs (
		channel_key, account_id, event_id, payload, status, claim_token, lease_until, attempts,
		max_attempts, next_attempt_at, last_error, created_at, updated_at
	) VALUES (?, ?, ?, ?, 'pending', '', 0, 0, ?, ?, '', ?, ?)
	ON CONFLICT (channel_key, account_id, event_id) DO NOTHING`,
		job.ChannelKey, job.AccountID, job.EventID, job.Payload, maxAttempts,
		now.UnixNano(), now.UnixNano(), now.UnixNano())
	if err != nil {
		return false, fmt.Errorf("enqueue gateway inbound job: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("enqueue gateway inbound job rows affected: %w", err)
	}
	return affected == 1, nil
}

// ListRecentInboundJobPayloads lets a channel recover provider context from
// its own durable callbacks without exposing payloads across channel accounts.
func (s *Store) ListRecentInboundJobPayloads(ctx context.Context, channelKey, accountID string, limit int) ([][]byte, error) {
	channelKey = strings.ToLower(strings.TrimSpace(channelKey))
	accountID = strings.TrimSpace(accountID)
	if !validInboundQueueScope(channelKey, accountID) || limit <= 0 {
		return nil, ErrInvalidInput
	}
	if limit > recentPayloadMaxRows {
		limit = recentPayloadMaxRows
	}
	if err := s.EnsureQueueSchema(ctx); err != nil {
		return nil, err
	}
	rows, err := s.db.Query(ctx, `SELECT payload FROM gateway_inbound_jobs
		WHERE channel_key = ? AND account_id = ?
		ORDER BY created_at DESC, event_id DESC LIMIT ?`, channelKey, accountID, limit)
	if err != nil {
		return nil, fmt.Errorf("list recent gateway inbound payloads: %w", err)
	}
	defer rows.Close()
	payloads := make([][]byte, 0, limit)
	for rows.Next() {
		var payload []byte
		if err := rows.Scan(&payload); err != nil {
			return nil, fmt.Errorf("scan recent gateway inbound payload: %w", err)
		}
		payloads = append(payloads, append([]byte(nil), payload...))
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate recent gateway inbound payloads: %w", err)
	}
	return payloads, nil
}

func (s *Store) ClaimInboundJob(ctx context.Context, channelKey, accountID string) (channel.InboundJob, bool, error) {
	channelKey = strings.ToLower(strings.TrimSpace(channelKey))
	accountID = strings.TrimSpace(accountID)
	if !validInboundQueueScope(channelKey, accountID) {
		return channel.InboundJob{}, false, ErrInvalidInput
	}
	if err := s.EnsureQueueSchema(ctx); err != nil {
		return channel.InboundJob{}, false, err
	}
	token, err := newClaimToken()
	if err != nil {
		return channel.InboundJob{}, false, err
	}
	now := s.now().UTC()
	if s.db.Dialect() == platformdb.DialectPostgres {
		return s.claimInboundJobPostgres(ctx, channelKey, accountID, token, now)
	}
	return s.claimInboundJobSQLite(ctx, channelKey, accountID, token, now)
}

func (s *Store) claimInboundJobPostgres(ctx context.Context, channelKey, accountID, token string, now time.Time) (channel.InboundJob, bool, error) {
	row := s.db.QueryRow(ctx, `WITH candidate AS (
		SELECT channel_key, account_id, event_id
		FROM gateway_inbound_jobs
		WHERE channel_key = ? AND account_id = ? AND (
			(status = 'pending' AND next_attempt_at <= ?) OR
			(status = 'processing' AND lease_until <= ?)
		)
		ORDER BY created_at, event_id
		FOR UPDATE SKIP LOCKED
		LIMIT 1
	)
	UPDATE gateway_inbound_jobs AS jobs
	SET status = 'processing', claim_token = ?, lease_until = ?, attempts = jobs.attempts + 1,
		last_error = '', updated_at = ?
	FROM candidate
	WHERE jobs.channel_key = candidate.channel_key AND jobs.account_id = candidate.account_id AND jobs.event_id = candidate.event_id
	RETURNING jobs.channel_key, jobs.account_id, jobs.event_id, jobs.payload, jobs.claim_token, jobs.attempts, jobs.max_attempts`,
		channelKey, accountID, now.UnixNano(), now.UnixNano(), token, now.Add(inboundJobLease).UnixNano(), now.UnixNano())
	job, err := scanInboundJob(row)
	if errors.Is(err, sql.ErrNoRows) {
		return channel.InboundJob{}, false, nil
	}
	if err != nil {
		return channel.InboundJob{}, false, fmt.Errorf("claim gateway inbound job: %w", err)
	}
	return job, true, nil
}

func (s *Store) claimInboundJobSQLite(ctx context.Context, channelKey, accountID, token string, now time.Time) (channel.InboundJob, bool, error) {
	var job channel.InboundJob
	err := s.db.WithTx(ctx, func(tx platformdb.Tx) error {
		var eventID string
		err := tx.QueryRowContext(ctx, `SELECT event_id FROM gateway_inbound_jobs
			WHERE channel_key = ? AND account_id = ? AND (
				(status = 'pending' AND next_attempt_at <= ?) OR
				(status = 'processing' AND lease_until <= ?)
			) ORDER BY created_at, event_id LIMIT 1`, channelKey, accountID, now.UnixNano(), now.UnixNano()).Scan(&eventID)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, `UPDATE gateway_inbound_jobs SET status = 'processing', claim_token = ?,
			lease_until = ?, attempts = attempts + 1, last_error = '', updated_at = ?
			WHERE channel_key = ? AND account_id = ? AND event_id = ? AND (
				(status = 'pending' AND next_attempt_at <= ?) OR
				(status = 'processing' AND lease_until <= ?)
			)`, token, now.Add(inboundJobLease).UnixNano(), now.UnixNano(), channelKey, accountID, eventID, now.UnixNano(), now.UnixNano())
		if err != nil {
			return err
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if affected != 1 {
			return nil
		}
		job, err = scanInboundJob(tx.QueryRowContext(ctx, `SELECT channel_key, account_id, event_id, payload,
			claim_token, attempts, max_attempts FROM gateway_inbound_jobs
			WHERE channel_key = ? AND account_id = ? AND event_id = ? AND claim_token = ?`, channelKey, accountID, eventID, token))
		return err
	})
	if err != nil {
		return channel.InboundJob{}, false, fmt.Errorf("claim gateway inbound job: %w", err)
	}
	return job, strings.TrimSpace(job.ClaimToken) != "", nil
}

func (s *Store) RenewInboundJob(ctx context.Context, job channel.InboundJob) error {
	if !validInboundJobClaim(job) {
		return ErrInvalidInput
	}
	now := s.now().UTC()
	result, err := s.db.Exec(ctx, `UPDATE gateway_inbound_jobs SET lease_until = ?, updated_at = ?
		WHERE channel_key = ? AND account_id = ? AND event_id = ? AND status = 'processing' AND claim_token = ?`,
		now.Add(inboundJobLease).UnixNano(), now.UnixNano(), job.ChannelKey, job.AccountID, job.EventID, job.ClaimToken)
	if err != nil {
		return fmt.Errorf("renew gateway inbound job: %w", err)
	}
	return requireSingleAffected(result, "renew gateway inbound job")
}

func (s *Store) CompleteInboundJob(ctx context.Context, job channel.InboundJob) error {
	if !validInboundJobClaim(job) {
		return ErrInvalidInput
	}
	now := s.now().UTC()
	result, err := s.db.Exec(ctx, `UPDATE gateway_inbound_jobs SET status = 'completed', claim_token = '',
		lease_until = 0, last_error = '', updated_at = ?
		WHERE channel_key = ? AND account_id = ? AND event_id = ? AND status = 'processing' AND claim_token = ?`,
		now.UnixNano(), job.ChannelKey, job.AccountID, job.EventID, job.ClaimToken)
	if err != nil {
		return fmt.Errorf("complete gateway inbound job: %w", err)
	}
	if err := requireSingleAffected(result, "complete gateway inbound job"); err != nil {
		return err
	}
	_, _ = s.db.Exec(ctx, `DELETE FROM gateway_inbound_jobs WHERE status = 'completed' AND updated_at < ?`, now.Add(-inboundJobRetention).UnixNano())
	return nil
}

func (s *Store) ReleaseInboundJob(ctx context.Context, job channel.InboundJob) error {
	if !validInboundJobClaim(job) {
		return ErrInvalidInput
	}
	now := s.now().UTC()
	result, err := s.db.Exec(ctx, `UPDATE gateway_inbound_jobs SET status = 'pending', claim_token = '', lease_until = 0,
		attempts = CASE WHEN attempts > 0 THEN attempts - 1 ELSE 0 END,
		next_attempt_at = ?, last_error = '', updated_at = ?
		WHERE channel_key = ? AND account_id = ? AND event_id = ? AND status = 'processing' AND claim_token = ?`,
		now.UnixNano(), now.UnixNano(), job.ChannelKey, job.AccountID, job.EventID, job.ClaimToken)
	if err != nil {
		return fmt.Errorf("release gateway inbound job: %w", err)
	}
	return requireSingleAffected(result, "release gateway inbound job")
}

func (s *Store) FailInboundJob(ctx context.Context, job channel.InboundJob, reason string, nextAttemptAt time.Time) error {
	if !validInboundJobClaim(job) {
		return ErrInvalidInput
	}
	status := "pending"
	nextAttempt := nextAttemptAt.UTC().UnixNano()
	if nextAttemptAt.IsZero() || job.MaxAttempts > 0 && job.Attempts >= job.MaxAttempts {
		status = "dead_letter"
		nextAttempt = 0
	}
	reason = strings.TrimSpace(reason)
	if len([]rune(reason)) > 500 {
		reason = string([]rune(reason)[:500])
	}
	result, err := s.db.Exec(ctx, `UPDATE gateway_inbound_jobs SET status = ?, claim_token = '', lease_until = 0,
		next_attempt_at = ?, last_error = ?, updated_at = ?
		WHERE channel_key = ? AND account_id = ? AND event_id = ? AND status = 'processing' AND claim_token = ?`,
		status, nextAttempt, reason, s.now().UTC().UnixNano(), job.ChannelKey, job.AccountID, job.EventID, job.ClaimToken)
	if err != nil {
		return fmt.Errorf("fail gateway inbound job: %w", err)
	}
	return requireSingleAffected(result, "fail gateway inbound job")
}

func scanInboundJob(row rowScanner) (channel.InboundJob, error) {
	var job channel.InboundJob
	err := row.Scan(&job.ChannelKey, &job.AccountID, &job.EventID, &job.Payload, &job.ClaimToken, &job.Attempts, &job.MaxAttempts)
	return job, err
}

func validInboundQueueScope(channelKey, accountID string) bool {
	return channelKey != "" && accountID != "" && len([]rune(channelKey)) <= 64 && len([]rune(accountID)) <= 512
}

func validInboundJobClaim(job channel.InboundJob) bool {
	return validInboundEventKey(strings.TrimSpace(job.ChannelKey), strings.TrimSpace(job.AccountID), strings.TrimSpace(job.EventID)) &&
		strings.TrimSpace(job.ClaimToken) != ""
}
