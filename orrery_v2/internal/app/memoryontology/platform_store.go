package memoryontology

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"orrery_v2/internal/platform/db"
)

type PlatformStore struct {
	store *db.Store
}

func NewPlatformStore(store *db.Store) *PlatformStore {
	return &PlatformStore{store: store}
}

func (s *PlatformStore) GetRun(ctx context.Context, key RunKey) (Run, bool, error) {
	if s == nil || s.store == nil {
		return Run{}, false, fmt.Errorf("memory ontology platform store is required")
	}
	var run Run
	var nextRetryAt, startedAt, completedAt, updatedAt int64
	err := s.store.QueryRow(ctx, `
SELECT status, attempts, next_retry_at, last_error, model_id, started_at, completed_at, updated_at
FROM memory_ontology_runs
WHERE tenant_id = ? AND memory_id = ? AND content_hash = ? AND extractor_version = ?`,
		key.TenantID, key.MemoryID, key.ContentHash, key.ExtractorVersion,
	).Scan(&run.Status, &run.Attempts, &nextRetryAt, &run.LastError, &run.ModelID, &startedAt, &completedAt, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Run{}, false, nil
	}
	if err != nil {
		return Run{}, false, fmt.Errorf("get memory ontology run: %w", err)
	}
	run.RunKey = key
	run.NextRetryAt = fromUnixNano(nextRetryAt)
	run.StartedAt = fromUnixNano(startedAt)
	run.CompletedAt = fromUnixNano(completedAt)
	run.UpdatedAt = fromUnixNano(updatedAt)
	return run, true, nil
}

func (s *PlatformStore) ClaimRun(ctx context.Context, key RunKey, modelID string, now time.Time) (Run, bool, error) {
	if s == nil || s.store == nil {
		return Run{}, false, fmt.Errorf("memory ontology platform store is required")
	}
	now = now.UTC()
	result, err := s.store.Exec(ctx, `
INSERT INTO memory_ontology_runs (
	tenant_id, memory_id, content_hash, extractor_version, status, attempts,
	next_retry_at, last_error, model_id, started_at, completed_at, updated_at
) VALUES (?, ?, ?, ?, ?, 1, 0, '', ?, ?, 0, ?)
ON CONFLICT(tenant_id, memory_id, content_hash, extractor_version) DO NOTHING`,
		key.TenantID, key.MemoryID, key.ContentHash, key.ExtractorVersion, RunRunning,
		modelID, toUnixNano(now), toUnixNano(now),
	)
	if err != nil {
		return Run{}, false, fmt.Errorf("claim new memory ontology run: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return Run{}, false, fmt.Errorf("read memory ontology claim result: %w", err)
	}
	claimed := affected == 1
	if !claimed {
		staleBefore := now.Add(-10 * time.Minute)
		result, err = s.store.Exec(ctx, `
UPDATE memory_ontology_runs SET
	status = ?, attempts = attempts + 1, last_error = '', model_id = ?,
	started_at = ?, completed_at = 0, updated_at = ?
WHERE tenant_id = ? AND memory_id = ? AND content_hash = ? AND extractor_version = ?
	AND attempts < ?
	AND (
		status = ? OR
		(status = ? AND next_retry_at <= ?) OR
		(status = ? AND updated_at <= ?)
	)`,
			RunRunning, modelID, toUnixNano(now), toUnixNano(now),
			key.TenantID, key.MemoryID, key.ContentHash, key.ExtractorVersion,
			MaximumAttempts, RunPending, RunFailed, toUnixNano(now), RunRunning, toUnixNano(staleBefore),
		)
		if err != nil {
			return Run{}, false, fmt.Errorf("claim existing memory ontology run: %w", err)
		}
		affected, err = result.RowsAffected()
		if err != nil {
			return Run{}, false, fmt.Errorf("read memory ontology retry claim result: %w", err)
		}
		claimed = affected == 1
	}
	run, exists, err := s.GetRun(ctx, key)
	if err != nil {
		return Run{}, false, err
	}
	if !exists {
		return Run{}, false, fmt.Errorf("claimed memory ontology run is missing")
	}
	return run, claimed, nil
}

func (s *PlatformStore) SaveRun(ctx context.Context, run Run) error {
	if s == nil || s.store == nil {
		return fmt.Errorf("memory ontology platform store is required")
	}
	_, err := s.store.Exec(ctx, `
INSERT INTO memory_ontology_runs (
	tenant_id, memory_id, content_hash, extractor_version, status, attempts,
	next_retry_at, last_error, model_id, started_at, completed_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(tenant_id, memory_id, content_hash, extractor_version) DO UPDATE SET
	status = excluded.status,
	attempts = excluded.attempts,
	next_retry_at = excluded.next_retry_at,
	last_error = excluded.last_error,
	model_id = excluded.model_id,
	started_at = excluded.started_at,
	completed_at = excluded.completed_at,
	updated_at = excluded.updated_at`,
		run.TenantID, run.MemoryID, run.ContentHash, run.ExtractorVersion, run.Status, run.Attempts,
		toUnixNano(run.NextRetryAt), run.LastError, run.ModelID, toUnixNano(run.StartedAt), toUnixNano(run.CompletedAt), toUnixNano(run.UpdatedAt),
	)
	if err != nil {
		return fmt.Errorf("save memory ontology run: %w", err)
	}
	return nil
}

// LatestExtraction returns the most recent successful extraction for the same
// memory content but a previous ontology dictionary version.
func (s *PlatformStore) LatestExtraction(ctx context.Context, key RunKey) (Extraction, bool, error) {
	if s == nil || s.store == nil {
		return Extraction{}, false, fmt.Errorf("memory ontology platform store is required")
	}
	var version string
	err := s.store.QueryRow(ctx, `
SELECT extractor_version
FROM memory_ontology_runs
WHERE tenant_id = ? AND memory_id = ? AND content_hash = ?
	AND extractor_version <> ? AND extractor_version LIKE ? AND status = ?
ORDER BY completed_at DESC, updated_at DESC
LIMIT 1`, key.TenantID, key.MemoryID, key.ContentHash, key.ExtractorVersion, ExtractorVersion+":%", RunSucceeded).Scan(&version)
	if errors.Is(err, sql.ErrNoRows) {
		return Extraction{}, false, nil
	}
	if err != nil {
		return Extraction{}, false, fmt.Errorf("find prior memory ontology extraction: %w", err)
	}
	rows, err := s.store.Query(ctx, `
SELECT surface, object_type, object_hint, confidence, anchor
FROM memory_ontology_mentions
WHERE tenant_id = ? AND memory_id = ? AND content_hash = ? AND extractor_version = ?
ORDER BY mention_index ASC`, key.TenantID, key.MemoryID, key.ContentHash, version)
	if err != nil {
		return Extraction{}, false, fmt.Errorf("list prior memory ontology mentions: %w", err)
	}
	defer rows.Close()
	extraction := Extraction{Mentions: []Mention{}}
	for rows.Next() {
		var mention Mention
		if err := rows.Scan(&mention.Surface, &mention.ObjectType, &mention.ObjectHint, &mention.Confidence, &mention.Anchor); err != nil {
			return Extraction{}, false, fmt.Errorf("scan prior memory ontology mention: %w", err)
		}
		mention.Surface = strings.TrimSpace(mention.Surface)
		extraction.Mentions = append(extraction.Mentions, mention)
	}
	if err := rows.Err(); err != nil {
		return Extraction{}, false, fmt.Errorf("iterate prior memory ontology mentions: %w", err)
	}
	return extraction, true, nil
}

func (s *PlatformStore) FailExhaustedStaleRuns(ctx context.Context, now time.Time, staleAfter time.Duration, limit int) (int, error) {
	if s == nil || s.store == nil {
		return 0, fmt.Errorf("memory ontology platform store is required")
	}
	if now.IsZero() || staleAfter <= 0 || limit <= 0 {
		return 0, fmt.Errorf("memory ontology stale run maintenance parameters are invalid")
	}
	now = now.UTC()
	staleBefore := now.Add(-staleAfter)
	updated := 0
	err := s.store.WithTx(ctx, func(tx db.Tx) error {
		rows, err := tx.QueryContext(ctx, `
SELECT tenant_id, memory_id, content_hash, extractor_version
FROM memory_ontology_runs
WHERE status = ? AND attempts >= ? AND updated_at < ?
ORDER BY updated_at ASC, tenant_id ASC, memory_id ASC, content_hash ASC, extractor_version ASC
LIMIT ?`, RunRunning, MaximumAttempts, toUnixNano(staleBefore), limit)
		if err != nil {
			return fmt.Errorf("list exhausted stale memory ontology runs: %w", err)
		}
		keys := make([]RunKey, 0, limit)
		for rows.Next() {
			var key RunKey
			if err := rows.Scan(&key.TenantID, &key.MemoryID, &key.ContentHash, &key.ExtractorVersion); err != nil {
				_ = rows.Close()
				return fmt.Errorf("scan exhausted stale memory ontology run: %w", err)
			}
			keys = append(keys, key)
		}
		if err := rows.Close(); err != nil {
			return fmt.Errorf("close exhausted stale memory ontology runs: %w", err)
		}
		if err := rows.Err(); err != nil {
			return fmt.Errorf("iterate exhausted stale memory ontology runs: %w", err)
		}
		for _, key := range keys {
			result, err := tx.ExecContext(ctx, `
UPDATE memory_ontology_runs SET
	status = ?, next_retry_at = 0,
	last_error = 'stale extraction run exhausted after maximum attempts',
	completed_at = ?, updated_at = ?
WHERE tenant_id = ? AND memory_id = ? AND content_hash = ? AND extractor_version = ?
	AND status = ? AND attempts >= ? AND updated_at < ?`,
				RunFailed, toUnixNano(now), toUnixNano(now),
				key.TenantID, key.MemoryID, key.ContentHash, key.ExtractorVersion,
				RunRunning, MaximumAttempts, toUnixNano(staleBefore),
			)
			if err != nil {
				return fmt.Errorf("fail exhausted stale memory ontology run: %w", err)
			}
			affected, err := result.RowsAffected()
			if err != nil {
				return fmt.Errorf("read exhausted stale memory ontology update result: %w", err)
			}
			updated += int(affected)
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return updated, nil
}

func (s *PlatformStore) RecoverSourceProjectionFailures(ctx context.Context, currentKeys []RunKey, now time.Time) (SourceProjectionRecoveryReport, error) {
	report := SourceProjectionRecoveryReport{}
	if s == nil || s.store == nil {
		return report, fmt.Errorf("memory ontology platform store is required")
	}
	rows, err := s.store.Query(ctx, `
SELECT tenant_id, memory_id, content_hash, extractor_version
FROM memory_ontology_runs
WHERE (status = ? AND last_error LIKE 'memory ontology source object % is not projected')
	OR (status = ? AND attempts = 0 AND last_error = '')`, RunFailed, RunPending)
	if err != nil {
		return report, fmt.Errorf("list memory ontology source projection failures: %w", err)
	}
	defer rows.Close()
	candidates := []RunKey{}
	for rows.Next() {
		var key RunKey
		if err := rows.Scan(&key.TenantID, &key.MemoryID, &key.ContentHash, &key.ExtractorVersion); err != nil {
			return report, fmt.Errorf("scan memory ontology source projection failure: %w", err)
		}
		candidates = append(candidates, key)
	}
	if err := rows.Err(); err != nil {
		return report, fmt.Errorf("iterate memory ontology source projection failures: %w", err)
	}
	current := make(map[RunKey]struct{}, len(currentKeys))
	for _, key := range currentKeys {
		current[key] = struct{}{}
	}
	nowValue := toUnixNano(now.UTC())
	err = s.store.WithTx(ctx, func(tx db.Tx) error {
		for _, key := range candidates {
			if _, ok := current[key]; ok {
				if _, err := tx.ExecContext(ctx, `
UPDATE memory_ontology_runs SET
	status = ?, attempts = 0, next_retry_at = 0, last_error = '',
	started_at = 0, completed_at = 0, updated_at = ?
WHERE tenant_id = ? AND memory_id = ? AND content_hash = ? AND extractor_version = ?`,
					RunPending, nowValue, key.TenantID, key.MemoryID, key.ContentHash, key.ExtractorVersion); err != nil {
					return err
				}
				report.Reset++
				continue
			}
			if _, err := tx.ExecContext(ctx, `
UPDATE memory_ontology_runs SET
	status = ?, next_retry_at = 0, last_error = '', completed_at = ?, updated_at = ?
WHERE tenant_id = ? AND memory_id = ? AND content_hash = ? AND extractor_version = ?`,
				RunSuperseded, nowValue, nowValue, key.TenantID, key.MemoryID, key.ContentHash, key.ExtractorVersion); err != nil {
				return err
			}
			report.Superseded++
		}
		return nil
	})
	if err != nil {
		return SourceProjectionRecoveryReport{}, fmt.Errorf("recover memory ontology source projection failures: %w", err)
	}
	return report, nil
}

func (s *PlatformStore) ReplaceMentions(ctx context.Context, key RunKey, projections []MentionProjection, createdAt time.Time) error {
	if s == nil || s.store == nil {
		return fmt.Errorf("memory ontology platform store is required")
	}
	return s.store.WithTx(ctx, func(tx db.Tx) error {
		if _, err := tx.ExecContext(ctx, `
DELETE FROM memory_ontology_mentions
WHERE tenant_id = ? AND memory_id = ? AND content_hash = ? AND extractor_version = ?`,
			key.TenantID, key.MemoryID, key.ContentHash, key.ExtractorVersion,
		); err != nil {
			return fmt.Errorf("replace memory ontology mentions: %w", err)
		}
		for _, projection := range projections {
			mention := projection.Mention
			if _, err := tx.ExecContext(ctx, `
INSERT INTO memory_ontology_mentions (
	tenant_id, memory_id, content_hash, extractor_version, mention_index,
	surface, object_type, object_hint, confidence, anchor, resolution,
	resolved_object_id, link_type, evidence_id, link_id, created_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
				key.TenantID, key.MemoryID, key.ContentHash, key.ExtractorVersion, projection.Index,
				mention.Surface, mention.ObjectType, mention.ObjectHint, mention.Confidence, mention.Anchor, projection.Resolution,
				projection.ResolvedObjectID, projection.LinkType, projection.EvidenceID, projection.LinkID, toUnixNano(createdAt),
			); err != nil {
				return fmt.Errorf("insert memory ontology mention: %w", err)
			}
		}
		return nil
	})
}

func toUnixNano(value time.Time) int64 {
	if value.IsZero() {
		return 0
	}
	return value.UTC().UnixNano()
}

func fromUnixNano(value int64) time.Time {
	if value <= 0 {
		return time.Time{}
	}
	return time.Unix(0, value).UTC()
}
