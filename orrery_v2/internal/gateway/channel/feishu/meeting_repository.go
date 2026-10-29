package feishu

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

type MeetingRepository struct {
	store *db.Store
}

func NewMeetingRepository(store *db.Store) *MeetingRepository {
	return &MeetingRepository{store: store}
}

func (r *MeetingRepository) SavePolicy(ctx context.Context, policy MeetingPolicy) error {
	if r == nil || r.store == nil {
		return ErrDependency
	}
	policy = normalizePolicy(policy)
	if err := validatePolicy(policy); err != nil {
		return err
	}
	if policy.Version <= 0 {
		policy.Version = 1
	}
	if policy.UpdatedAt.IsZero() {
		policy.UpdatedAt = time.Now().UTC()
	}
	chats, err := json.Marshal(policy.AllowedChatIDs)
	if err != nil {
		return fmt.Errorf("encode meeting policy chats: %w", err)
	}
	actors, err := json.Marshal(policy.AllowedActorIDs)
	if err != nil {
		return fmt.Errorf("encode meeting policy actors: %w", err)
	}
	_, err = r.store.Exec(ctx, `
INSERT INTO feishu_meeting_policies (
	tenant_id, provider, account_id, enabled, allowed_chat_ids_json, allowed_actor_ids_json,
	summary_agent_id, publish_target, raw_media_retention_ns, max_meeting_duration_ns,
	daily_transcription_time_ns, version, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(tenant_id, provider, account_id) DO UPDATE SET
	enabled = excluded.enabled,
	allowed_chat_ids_json = excluded.allowed_chat_ids_json,
	allowed_actor_ids_json = excluded.allowed_actor_ids_json,
	summary_agent_id = excluded.summary_agent_id,
	publish_target = excluded.publish_target,
	raw_media_retention_ns = excluded.raw_media_retention_ns,
	max_meeting_duration_ns = excluded.max_meeting_duration_ns,
	daily_transcription_time_ns = excluded.daily_transcription_time_ns,
	version = excluded.version,
	updated_at = excluded.updated_at`,
		policy.TenantID, policy.Provider, policy.AccountID, boolInt(policy.Enabled), string(chats), string(actors),
		policy.SummaryAgentID, policy.PublishTarget, int64(policy.RawMediaRetention), int64(policy.MaxMeetingDuration),
		int64(policy.DailyTranscriptionTime), policy.Version, timeToNano(policy.UpdatedAt),
	)
	if err != nil {
		return fmt.Errorf("save meeting automation policy: %w", err)
	}
	return nil
}

func (r *MeetingRepository) GetPolicy(ctx context.Context, tenantID, provider, accountID string) (MeetingPolicy, bool, error) {
	if r == nil || r.store == nil {
		return MeetingPolicy{}, false, ErrDependency
	}
	row := r.store.QueryRow(ctx, `
SELECT tenant_id, provider, account_id, enabled, allowed_chat_ids_json, allowed_actor_ids_json,
       summary_agent_id, publish_target, raw_media_retention_ns, max_meeting_duration_ns,
       daily_transcription_time_ns, version, updated_at
FROM feishu_meeting_policies
WHERE tenant_id = ? AND provider = ? AND account_id = ?`,
		strings.TrimSpace(tenantID), strings.ToLower(strings.TrimSpace(provider)), strings.TrimSpace(accountID),
	)
	policy, err := scanPolicy(row)
	if errors.Is(err, sql.ErrNoRows) {
		return MeetingPolicy{}, false, nil
	}
	if err != nil {
		return MeetingPolicy{}, false, err
	}
	return policy, true, nil
}

func (r *MeetingRepository) CreateSession(ctx context.Context, session MeetingSession) (MeetingSession, bool, error) {
	if r == nil || r.store == nil {
		return MeetingSession{}, false, ErrDependency
	}
	result, err := r.store.Exec(ctx, `
INSERT INTO feishu_meeting_sessions (
	id, tenant_id, provider, account_id, provider_tenant_key, source_chat_id, requested_by,
	owner_external_id, topic, reservation_id, meeting_id, meeting_no, meeting_url, minute_token,
	recording_duration_ns, status, summary_agent_id, publish_target, raw_media_retention_ns,
	max_meeting_duration_ns, daily_transcription_time_ns, policy_version, source_context_kind,
	source_context_id, idempotency_key, last_error_code, expires_at, started_at, ended_at,
	recording_ready_at, created_at, updated_at, version
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT DO NOTHING`, sessionArgs(session)...)
	if err != nil {
		return MeetingSession{}, false, fmt.Errorf("create meeting automation session: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return MeetingSession{}, false, fmt.Errorf("read meeting automation session insert result: %w", err)
	}
	if affected == 1 {
		return session, true, nil
	}
	existing, found, err := r.findSessionByIdempotency(ctx, session.TenantID, session.Provider, session.AccountID, session.IdempotencyKey)
	if err != nil {
		return MeetingSession{}, false, err
	}
	if !found {
		return MeetingSession{}, false, ErrConflict
	}
	return existing, false, nil
}

func (r *MeetingRepository) GetSession(ctx context.Context, tenantID, sessionID string) (MeetingSession, bool, error) {
	if r == nil || r.store == nil {
		return MeetingSession{}, false, ErrDependency
	}
	row := r.store.QueryRow(ctx, sessionSelectSQL()+` WHERE tenant_id = ? AND id = ?`, strings.TrimSpace(tenantID), strings.TrimSpace(sessionID))
	return scanOptionalSession(row)
}

func (r *MeetingRepository) FindSessionByMeeting(ctx context.Context, provider, accountID, providerTenantKey, meetingID string) (MeetingSession, bool, error) {
	if r == nil || r.store == nil {
		return MeetingSession{}, false, ErrDependency
	}
	row := r.store.QueryRow(ctx, sessionSelectSQL()+`
 WHERE provider = ? AND account_id = ? AND provider_tenant_key = ? AND meeting_id = ?`,
		strings.ToLower(strings.TrimSpace(provider)), strings.TrimSpace(accountID), strings.TrimSpace(providerTenantKey), strings.TrimSpace(meetingID),
	)
	return scanOptionalSession(row)
}

func (r *MeetingRepository) FindSessionByMeetingNo(ctx context.Context, provider, accountID, providerTenantKey, meetingNo string) (MeetingSession, bool, error) {
	if r == nil || r.store == nil {
		return MeetingSession{}, false, ErrDependency
	}
	row := r.store.QueryRow(ctx, sessionSelectSQL()+`
	WHERE provider = ? AND account_id = ? AND provider_tenant_key = ? AND meeting_no = ?
	  AND meeting_id = '' AND status IN (?, ?, ?)
	ORDER BY created_at DESC, id DESC LIMIT 1`,
		strings.ToLower(strings.TrimSpace(provider)), strings.TrimSpace(accountID), strings.TrimSpace(providerTenantKey), strings.TrimSpace(meetingNo),
		StatusReserved, StatusInProgress, StatusAwaitingRecording,
	)
	return scanOptionalSession(row)
}

func (r *MeetingRepository) ListUnboundSessions(ctx context.Context, provider, accountID, providerTenantKey string, limit int) ([]MeetingSession, error) {
	if r == nil || r.store == nil {
		return nil, ErrDependency
	}
	if limit <= 0 || limit > maxReconcileSessions {
		limit = maxReconcileSessions
	}
	rows, err := r.store.Query(ctx, sessionSelectSQL()+`
 WHERE provider = ? AND account_id = ? AND provider_tenant_key = ?
   AND meeting_id = '' AND reservation_id <> '' AND status IN (?, ?)
 ORDER BY created_at ASC, id ASC LIMIT ?`,
		strings.ToLower(strings.TrimSpace(provider)), strings.TrimSpace(accountID), strings.TrimSpace(providerTenantKey),
		StatusReserved, StatusInProgress, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("list unbound meeting automation sessions: %w", err)
	}
	defer rows.Close()
	out := make([]MeetingSession, 0)
	for rows.Next() {
		session, err := scanSession(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, session)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate unbound meeting automation sessions: %w", err)
	}
	return out, nil
}

func (r *MeetingRepository) CompareAndSaveSession(ctx context.Context, session MeetingSession, expectedVersion int64) (bool, error) {
	if r == nil || r.store == nil {
		return false, ErrDependency
	}
	args := sessionUpdateArgs(session)
	args = append(args, session.ID, session.TenantID, expectedVersion)
	result, err := r.store.Exec(ctx, `
UPDATE feishu_meeting_sessions SET
	provider = ?, account_id = ?, provider_tenant_key = ?, source_chat_id = ?, requested_by = ?,
	owner_external_id = ?, topic = ?, reservation_id = ?, meeting_id = ?, meeting_no = ?, meeting_url = ?,
	minute_token = ?, recording_duration_ns = ?, status = ?, summary_agent_id = ?, publish_target = ?,
	raw_media_retention_ns = ?, max_meeting_duration_ns = ?, daily_transcription_time_ns = ?,
	policy_version = ?, source_context_kind = ?, source_context_id = ?, idempotency_key = ?,
	last_error_code = ?, expires_at = ?, started_at = ?, ended_at = ?, recording_ready_at = ?,
	created_at = ?, updated_at = ?, version = ?
WHERE id = ? AND tenant_id = ? AND version = ?`, args...)
	if err != nil {
		return false, fmt.Errorf("update meeting automation session: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("read meeting automation session update result: %w", err)
	}
	return affected == 1, nil
}

func (r *MeetingRepository) findSessionByIdempotency(ctx context.Context, tenantID, provider, accountID, key string) (MeetingSession, bool, error) {
	row := r.store.QueryRow(ctx, sessionSelectSQL()+`
 WHERE tenant_id = ? AND provider = ? AND account_id = ? AND idempotency_key = ?`,
		strings.TrimSpace(tenantID), strings.ToLower(strings.TrimSpace(provider)), strings.TrimSpace(accountID), strings.TrimSpace(key),
	)
	return scanOptionalSession(row)
}

type rowScanner interface {
	Scan(...any) error
}

func scanPolicy(row rowScanner) (MeetingPolicy, error) {
	var policy MeetingPolicy
	var enabled int
	var chatsJSON, actorsJSON string
	var retention, maxDuration, daily, updatedAt int64
	err := row.Scan(
		&policy.TenantID, &policy.Provider, &policy.AccountID, &enabled, &chatsJSON, &actorsJSON,
		&policy.SummaryAgentID, &policy.PublishTarget, &retention, &maxDuration, &daily, &policy.Version, &updatedAt,
	)
	if err != nil {
		return MeetingPolicy{}, err
	}
	if err := json.Unmarshal([]byte(chatsJSON), &policy.AllowedChatIDs); err != nil {
		return MeetingPolicy{}, fmt.Errorf("decode meeting policy chats: %w", err)
	}
	if err := json.Unmarshal([]byte(actorsJSON), &policy.AllowedActorIDs); err != nil {
		return MeetingPolicy{}, fmt.Errorf("decode meeting policy actors: %w", err)
	}
	policy.Enabled = enabled != 0
	policy.RawMediaRetention = time.Duration(retention)
	policy.MaxMeetingDuration = time.Duration(maxDuration)
	policy.DailyTranscriptionTime = time.Duration(daily)
	policy.UpdatedAt = nanoToTime(updatedAt)
	return policy, nil
}

func scanOptionalSession(row rowScanner) (MeetingSession, bool, error) {
	session, err := scanSession(row)
	if errors.Is(err, sql.ErrNoRows) {
		return MeetingSession{}, false, nil
	}
	if err != nil {
		return MeetingSession{}, false, err
	}
	return session, true, nil
}

func scanSession(row rowScanner) (MeetingSession, error) {
	var session MeetingSession
	var recordingDuration, retention, maxDuration, daily int64
	var expiresAt, startedAt, endedAt, recordingReadyAt, createdAt, updatedAt int64
	err := row.Scan(
		&session.ID, &session.TenantID, &session.Provider, &session.AccountID, &session.ProviderTenantKey,
		&session.SourceChatID, &session.RequestedBy, &session.OwnerExternalID, &session.Topic,
		&session.ReservationID, &session.MeetingID, &session.MeetingNo, &session.MeetingURL, &session.MinuteToken,
		&recordingDuration, &session.Status, &session.Policy.SummaryAgentID, &session.Policy.PublishTarget,
		&retention, &maxDuration, &daily, &session.Policy.PolicyVersion, &session.SourceContextKind,
		&session.SourceContextID, &session.IdempotencyKey, &session.LastErrorCode, &expiresAt, &startedAt,
		&endedAt, &recordingReadyAt, &createdAt, &updatedAt, &session.Version,
	)
	if err != nil {
		return MeetingSession{}, err
	}
	session.RecordingDuration = time.Duration(recordingDuration)
	session.Policy.RawMediaRetention = time.Duration(retention)
	session.Policy.MaxMeetingDuration = time.Duration(maxDuration)
	session.Policy.DailyTranscriptionTime = time.Duration(daily)
	session.ExpiresAt = nanoToTime(expiresAt)
	session.StartedAt = nanoToTime(startedAt)
	session.EndedAt = nanoToTime(endedAt)
	session.RecordingReadyAt = nanoToTime(recordingReadyAt)
	session.CreatedAt = nanoToTime(createdAt)
	session.UpdatedAt = nanoToTime(updatedAt)
	return session, nil
}

func sessionSelectSQL() string {
	return `SELECT
	id, tenant_id, provider, account_id, provider_tenant_key, source_chat_id, requested_by,
	owner_external_id, topic, reservation_id, meeting_id, meeting_no, meeting_url, minute_token,
	recording_duration_ns, status, summary_agent_id, publish_target, raw_media_retention_ns,
	max_meeting_duration_ns, daily_transcription_time_ns, policy_version, source_context_kind,
	source_context_id, idempotency_key, last_error_code, expires_at, started_at, ended_at,
	recording_ready_at, created_at, updated_at, version
FROM feishu_meeting_sessions`
}

func sessionArgs(session MeetingSession) []any {
	return append([]any{session.ID, session.TenantID}, sessionUpdateArgs(session)...)
}

func sessionUpdateArgs(session MeetingSession) []any {
	return []any{
		session.Provider, session.AccountID, session.ProviderTenantKey, session.SourceChatID, session.RequestedBy,
		session.OwnerExternalID, session.Topic, session.ReservationID, session.MeetingID, session.MeetingNo, session.MeetingURL,
		session.MinuteToken, int64(session.RecordingDuration), session.Status, session.Policy.SummaryAgentID,
		session.Policy.PublishTarget, int64(session.Policy.RawMediaRetention), int64(session.Policy.MaxMeetingDuration),
		int64(session.Policy.DailyTranscriptionTime), session.Policy.PolicyVersion, session.SourceContextKind,
		session.SourceContextID, session.IdempotencyKey, session.LastErrorCode, timeToNano(session.ExpiresAt),
		timeToNano(session.StartedAt), timeToNano(session.EndedAt), timeToNano(session.RecordingReadyAt),
		timeToNano(session.CreatedAt), timeToNano(session.UpdatedAt), session.Version,
	}
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func timeToNano(value time.Time) int64 {
	if value.IsZero() {
		return 0
	}
	return value.UTC().UnixNano()
}

func nanoToTime(value int64) time.Time {
	if value == 0 {
		return time.Time{}
	}
	return time.Unix(0, value).UTC()
}
