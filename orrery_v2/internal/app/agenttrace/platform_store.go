package agenttrace

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/platform/db"
)

const (
	completedTraceHotRetention = 30 * 24 * time.Hour
	abnormalTraceHotRetention  = 90 * 24 * time.Hour
)

type PlatformStore struct {
	store *db.Store
	now   func() time.Time
}

func NewPlatformStore(store *db.Store) *PlatformStore {
	return &PlatformStore{
		store: store,
		now:   func() time.Time { return time.Now().UTC() },
	}
}

func (s *PlatformStore) Save(ctx context.Context, record TraceRecord) error {
	if err := s.validate(); err != nil {
		return err
	}
	record = normalizeTraceRecord(record, s.now())
	if err := validateTraceRecord(record); err != nil {
		return err
	}
	if record.Revision == 0 {
		return s.insert(ctx, record)
	}
	result, err := s.store.Exec(ctx, `
UPDATE agent_runtime_traces SET
	trace_ref = ?, status = ?, terminal_status = ?, event_count = ?, byte_count = ?,
	manifest_sha256 = ?, archive_ref = ?, archive_sha256 = ?, started_at = ?,
	latest_event_at = ?, completed_at = ?, retain_until = ?, archived_at = ?, hot_deleted_at = ?,
	purge_after = ?, purged_at = ?, legal_hold_actor = ?, legal_hold_reason = ?,
	legal_hold_at = ?, revision = revision + 1, updated_at = ?
WHERE tenant_id = ? AND run_id = ? AND trace_version = ? AND revision = ?`,
		record.TraceRef, record.Status, record.TerminalStatus, record.EventCount, record.ByteCount,
		record.ManifestSHA256, record.ArchiveRef, record.ArchiveSHA256, timeToInt(record.StartedAt),
		timeToInt(record.LatestEventAt), timeToInt(record.CompletedAt), timeToInt(record.RetainUntil), timeToInt(record.ArchivedAt),
		timeToInt(record.HotDeletedAt),
		timeToInt(record.PurgeAfter), timeToInt(record.PurgedAt), record.LegalHoldActor, record.LegalHoldReason,
		timeToInt(record.LegalHoldAt), timeToInt(record.UpdatedAt),
		record.TenantID, record.RunID, record.TraceVersion, record.Revision,
	)
	if err != nil {
		return fmt.Errorf("update agent runtime trace: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read agent runtime trace update count: %w", err)
	}
	if affected != 1 {
		return ErrTraceRevisionConflict
	}
	return nil
}

func (s *PlatformStore) insert(ctx context.Context, record TraceRecord) error {
	result, err := s.store.Exec(ctx, `
INSERT INTO agent_runtime_traces (
	tenant_id, run_id, trace_version, trace_ref, status, terminal_status,
	event_count, byte_count, manifest_sha256, archive_ref, archive_sha256,
	started_at, latest_event_at, completed_at, retain_until, archived_at, hot_deleted_at,
	purge_after, purged_at, legal_hold_actor, legal_hold_reason, legal_hold_at,
	revision, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1, ?)
ON CONFLICT(tenant_id, run_id, trace_version) DO NOTHING`,
		record.TenantID, record.RunID, record.TraceVersion, record.TraceRef, record.Status, record.TerminalStatus,
		record.EventCount, record.ByteCount, record.ManifestSHA256, record.ArchiveRef, record.ArchiveSHA256,
		timeToInt(record.StartedAt), timeToInt(record.LatestEventAt), timeToInt(record.CompletedAt), timeToInt(record.RetainUntil), timeToInt(record.ArchivedAt),
		timeToInt(record.HotDeletedAt),
		timeToInt(record.PurgeAfter), timeToInt(record.PurgedAt), record.LegalHoldActor, record.LegalHoldReason, timeToInt(record.LegalHoldAt),
		timeToInt(record.UpdatedAt),
	)
	if err != nil {
		return fmt.Errorf("insert agent runtime trace: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read agent runtime trace insert count: %w", err)
	}
	if affected != 1 {
		return ErrTraceRevisionConflict
	}
	return nil
}

func (s *PlatformStore) Get(ctx context.Context, tenantID, runID string, traceVersion int) (TraceRecord, bool, error) {
	if err := s.validate(); err != nil {
		return TraceRecord{}, false, err
	}
	tenantID = strings.TrimSpace(tenantID)
	runID = strings.TrimSpace(runID)
	if tenantID == "" || runID == "" || traceVersion <= 0 {
		return TraceRecord{}, false, fmt.Errorf("tenant id, run id, and trace version are required")
	}
	row := s.store.QueryRow(ctx, `SELECT `+traceRecordColumns+`
FROM agent_runtime_traces
WHERE tenant_id = ? AND run_id = ? AND trace_version = ?`, tenantID, runID, traceVersion)
	record, err := scanTraceRecord(row)
	if errors.Is(err, sql.ErrNoRows) {
		return TraceRecord{}, false, nil
	}
	if err != nil {
		return TraceRecord{}, false, fmt.Errorf("get agent runtime trace: %w", err)
	}
	return record, true, nil
}

func (s *PlatformStore) ApplyLegalHold(ctx context.Context, tenantID, runID string, traceVersion int, actor, reason string, heldAt time.Time) error {
	if err := s.validate(); err != nil {
		return err
	}
	tenantID = strings.TrimSpace(tenantID)
	runID = strings.TrimSpace(runID)
	actor = strings.TrimSpace(actor)
	reason = strings.TrimSpace(reason)
	if tenantID == "" || runID == "" || traceVersion <= 0 || actor == "" || reason == "" {
		return fmt.Errorf("tenant id, run id, trace version, actor, and reason are required")
	}
	if heldAt.IsZero() {
		heldAt = s.now()
	}
	result, err := s.store.Exec(ctx, `
UPDATE agent_runtime_traces SET
	legal_hold_actor = ?, legal_hold_reason = ?, legal_hold_at = ?,
	revision = revision + 1, updated_at = ?
WHERE tenant_id = ? AND run_id = ? AND trace_version = ?`,
		actor, reason, timeToInt(heldAt), timeToInt(s.now()), tenantID, runID, traceVersion,
	)
	if err != nil {
		return fmt.Errorf("apply agent runtime trace legal hold: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read legal hold update count: %w", err)
	}
	if affected != 1 {
		return ErrTraceNotFound
	}
	return nil
}

func (s *PlatformStore) ReleaseLegalHold(ctx context.Context, tenantID, runID string, traceVersion int) error {
	if err := s.validate(); err != nil {
		return err
	}
	result, err := s.store.Exec(ctx, `UPDATE agent_runtime_traces SET
	legal_hold_actor = '', legal_hold_reason = '', legal_hold_at = 0,
	revision = revision + 1, updated_at = ?
WHERE tenant_id = ? AND run_id = ? AND trace_version = ?`, timeToInt(s.now()), strings.TrimSpace(tenantID), strings.TrimSpace(runID), traceVersion)
	if err != nil {
		return fmt.Errorf("release agent runtime trace legal hold: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return ErrTraceNotFound
	}
	return nil
}

func (s *PlatformStore) ListRetentionCandidates(ctx context.Context, now time.Time, limit int) ([]TraceRecord, error) {
	if err := s.validate(); err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 100 {
		limit = 100
	}
	rows, err := s.store.Query(ctx, `SELECT `+traceRecordColumns+`
FROM agent_runtime_traces
WHERE (status = ? AND retain_until > 0 AND retain_until <= ?)
	OR (status = ? AND updated_at <= ?)
	OR (status = ? AND hot_deleted_at = 0)
	OR (status = ? AND purge_after > 0 AND purge_after <= ? AND legal_hold_at = 0)
ORDER BY CASE WHEN status = ? THEN retain_until ELSE purge_after END, tenant_id, run_id
LIMIT ?`, TraceHot, timeToInt(now), TraceMissing, timeToInt(now.Add(-24*time.Hour)), TraceArchived, TraceArchived, timeToInt(now), TraceHot, limit)
	if err != nil {
		return nil, fmt.Errorf("list trace retention candidates: %w", err)
	}
	defer rows.Close()
	out := make([]TraceRecord, 0, limit)
	for rows.Next() {
		record, err := scanTraceRecord(rows)
		if err != nil {
			return nil, fmt.Errorf("scan trace retention candidate: %w", err)
		}
		out = append(out, record)
	}
	return out, rows.Err()
}

func (s *PlatformStore) ListInterruptedCandidates(ctx context.Context, before time.Time, limit int) ([]TraceRecord, error) {
	if err := s.validate(); err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 100 {
		limit = 100
	}
	rows, err := s.store.Query(ctx, `SELECT `+traceRecordColumns+`
FROM agent_runtime_traces
WHERE status = ? AND latest_event_at <= ?
ORDER BY latest_event_at, tenant_id, run_id
LIMIT ?`, TraceRunning, timeToInt(before), limit)
	if err != nil {
		return nil, fmt.Errorf("list interrupted trace candidates: %w", err)
	}
	defer rows.Close()
	out := make([]TraceRecord, 0, limit)
	for rows.Next() {
		record, err := scanTraceRecord(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, record)
	}
	return out, rows.Err()
}

func (s *PlatformStore) SaveTraceIndex(ctx context.Context, index agentcore.TraceIndexRecord) error {
	if err := s.validate(); err != nil {
		return err
	}
	index.TenantID = strings.TrimSpace(index.TenantID)
	index.RunID = strings.TrimSpace(index.RunID)
	index.TraceRef = strings.TrimSpace(index.TraceRef)
	if index.TenantID == "" || index.RunID == "" || index.TraceVersion <= 0 || index.TraceRef == "" {
		return fmt.Errorf("tenant id, run id, trace version, and trace ref are required")
	}
	if index.EventCount < 0 || index.ByteCount < 0 {
		return fmt.Errorf("agent runtime trace counts cannot be negative")
	}
	now := s.now()
	status := TraceRunning
	completedAt := time.Time{}
	retainUntil := time.Time{}
	if strings.TrimSpace(index.TerminalStatus) != "" {
		status = TraceHot
		completedAt = index.CompletedAt
		if completedAt.IsZero() {
			completedAt = now
		}
		retainUntil = index.RetainUntil
		if retainUntil.IsZero() {
			retention := abnormalTraceHotRetention
			if strings.EqualFold(strings.TrimSpace(index.TerminalStatus), "completed") {
				retention = completedTraceHotRetention
			}
			retainUntil = completedAt.Add(retention)
		}
	}
	startedAt := index.StartedAt
	if startedAt.IsZero() {
		startedAt = now
	}
	latestEventAt := startedAt
	if !index.LatestEventAt.IsZero() {
		latestEventAt = index.LatestEventAt
	}
	if !completedAt.IsZero() {
		latestEventAt = completedAt
	}
	result, err := s.store.Exec(ctx, `
INSERT INTO agent_runtime_traces (
	tenant_id, run_id, trace_version, trace_ref, status, terminal_status,
	event_count, byte_count, started_at, latest_event_at, completed_at,
	retain_until, revision, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1, ?)
ON CONFLICT(tenant_id, run_id, trace_version) DO UPDATE SET
	trace_ref = excluded.trace_ref,
	status = CASE WHEN agent_runtime_traces.terminal_status <> '' AND excluded.terminal_status = '' THEN agent_runtime_traces.status ELSE excluded.status END,
	terminal_status = CASE WHEN excluded.terminal_status = '' THEN agent_runtime_traces.terminal_status ELSE excluded.terminal_status END,
	event_count = CASE WHEN excluded.terminal_status = '' THEN agent_runtime_traces.event_count ELSE excluded.event_count END,
	byte_count = CASE WHEN excluded.terminal_status = '' THEN agent_runtime_traces.byte_count ELSE excluded.byte_count END,
	latest_event_at = CASE WHEN excluded.terminal_status = '' AND agent_runtime_traces.terminal_status <> '' THEN agent_runtime_traces.latest_event_at ELSE excluded.latest_event_at END,
	completed_at = CASE WHEN excluded.terminal_status = '' THEN agent_runtime_traces.completed_at ELSE excluded.completed_at END,
	retain_until = CASE WHEN excluded.terminal_status = '' THEN agent_runtime_traces.retain_until ELSE excluded.retain_until END,
	revision = agent_runtime_traces.revision + 1,
	updated_at = excluded.updated_at`,
		index.TenantID, index.RunID, index.TraceVersion,
		index.TraceRef, status, strings.TrimSpace(index.TerminalStatus),
		index.EventCount, index.ByteCount, timeToInt(startedAt), timeToInt(latestEventAt),
		timeToInt(completedAt), timeToInt(retainUntil), timeToInt(now),
	)
	if err != nil {
		return fmt.Errorf("upsert agent runtime trace index: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read agent runtime trace index count: %w", err)
	}
	if affected != 1 {
		return fmt.Errorf("upsert agent runtime trace index affected %d rows", affected)
	}
	return nil
}

func (s *PlatformStore) validate() error {
	if s == nil || s.store == nil {
		return fmt.Errorf("agent runtime trace platform store is required")
	}
	if s.now == nil {
		return fmt.Errorf("agent runtime trace clock is required")
	}
	return nil
}

func normalizeTraceRecord(record TraceRecord, now time.Time) TraceRecord {
	record.TenantID = strings.TrimSpace(record.TenantID)
	record.RunID = strings.TrimSpace(record.RunID)
	record.TraceRef = strings.TrimSpace(record.TraceRef)
	record.TerminalStatus = strings.TrimSpace(record.TerminalStatus)
	record.ManifestSHA256 = strings.TrimSpace(record.ManifestSHA256)
	record.ArchiveRef = strings.TrimSpace(record.ArchiveRef)
	record.ArchiveSHA256 = strings.TrimSpace(record.ArchiveSHA256)
	record.LegalHoldActor = strings.TrimSpace(record.LegalHoldActor)
	record.LegalHoldReason = strings.TrimSpace(record.LegalHoldReason)
	if record.LatestEventAt.IsZero() {
		record.LatestEventAt = record.StartedAt
	}
	record.UpdatedAt = now.UTC()
	return record
}

func validateTraceRecord(record TraceRecord) error {
	if record.TenantID == "" || record.RunID == "" || record.TraceVersion <= 0 || record.TraceRef == "" {
		return fmt.Errorf("tenant id, run id, trace version, and trace ref are required")
	}
	if !record.Status.IsValid() {
		return fmt.Errorf("invalid agent runtime trace status %q", record.Status)
	}
	if record.StartedAt.IsZero() {
		return fmt.Errorf("agent runtime trace started time is required")
	}
	if record.EventCount < 0 || record.ByteCount < 0 || record.Revision < 0 {
		return fmt.Errorf("agent runtime trace counts and revision cannot be negative")
	}
	return nil
}

const traceRecordColumns = `tenant_id, run_id, trace_version, trace_ref, status, terminal_status,
event_count, byte_count, manifest_sha256, archive_ref, archive_sha256,
started_at, latest_event_at, completed_at, retain_until, archived_at, hot_deleted_at, purge_after,
purged_at, legal_hold_actor, legal_hold_reason, legal_hold_at, revision, updated_at`

type traceRecordScanner interface {
	Scan(...any) error
}

func scanTraceRecord(scanner traceRecordScanner) (TraceRecord, error) {
	var record TraceRecord
	var startedAt, latestEventAt, completedAt, retainUntil, archivedAt, hotDeletedAt, purgeAfter, purgedAt, legalHoldAt, updatedAt int64
	if err := scanner.Scan(
		&record.TenantID, &record.RunID, &record.TraceVersion, &record.TraceRef, &record.Status, &record.TerminalStatus,
		&record.EventCount, &record.ByteCount, &record.ManifestSHA256, &record.ArchiveRef, &record.ArchiveSHA256,
		&startedAt, &latestEventAt, &completedAt, &retainUntil, &archivedAt, &hotDeletedAt, &purgeAfter,
		&purgedAt, &record.LegalHoldActor, &record.LegalHoldReason, &legalHoldAt, &record.Revision, &updatedAt,
	); err != nil {
		return TraceRecord{}, err
	}
	record.StartedAt = intToTime(startedAt)
	record.LatestEventAt = intToTime(latestEventAt)
	record.CompletedAt = intToTime(completedAt)
	record.RetainUntil = intToTime(retainUntil)
	record.ArchivedAt = intToTime(archivedAt)
	record.HotDeletedAt = intToTime(hotDeletedAt)
	record.PurgeAfter = intToTime(purgeAfter)
	record.PurgedAt = intToTime(purgedAt)
	record.LegalHoldAt = intToTime(legalHoldAt)
	record.UpdatedAt = intToTime(updatedAt)
	return record, nil
}

func timeToInt(value time.Time) int64 {
	if value.IsZero() {
		return 0
	}
	return value.UTC().UnixNano()
}

func intToTime(value int64) time.Time {
	if value == 0 {
		return time.Time{}
	}
	return time.Unix(0, value).UTC()
}

var _ agentcore.TraceIndexSink = (*PlatformStore)(nil)
