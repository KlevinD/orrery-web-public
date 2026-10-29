package peopledirectory

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"orrery_v2/internal/platform/db"
)

type SnapshotRetentionPolicy struct {
	KeepLatest int
	MinimumAge time.Duration
	Limit      int
}

func (s *Store) LatestSnapshotStartedAt(ctx context.Context, source, tenantID string) (time.Time, bool, error) {
	if s == nil || s.database == nil {
		return time.Time{}, false, fmt.Errorf("People directory store is not configured")
	}
	source = strings.TrimSpace(source)
	tenantID = strings.TrimSpace(tenantID)
	if source == "" || tenantID == "" {
		return time.Time{}, false, fmt.Errorf("snapshot source and tenant id are required")
	}
	var createdAt int64
	err := s.database.QueryRow(ctx, `
SELECT created_at
FROM people_directory_snapshots
WHERE source = ? AND tenant_id = ?
ORDER BY created_at DESC, id DESC
LIMIT 1`, source, tenantID).Scan(&createdAt)
	if err == nil {
		return time.UnixMilli(createdAt).UTC(), true, nil
	}
	if err == sql.ErrNoRows {
		return time.Time{}, false, nil
	}
	return time.Time{}, false, fmt.Errorf("load latest People directory snapshot time: %w", err)
}

func (s *Store) PruneSnapshots(ctx context.Context, now time.Time, policy SnapshotRetentionPolicy) (int, error) {
	if s == nil || s.database == nil {
		return 0, fmt.Errorf("People directory store is not configured")
	}
	if now.IsZero() || policy.KeepLatest <= 0 || policy.MinimumAge <= 0 || policy.Limit <= 0 || policy.Limit > 1000 {
		return 0, fmt.Errorf("People directory snapshot retention policy is invalid")
	}
	cutoff := now.UTC().Add(-policy.MinimumAge).UnixMilli()
	deleted := 0
	err := s.database.WithTx(ctx, func(tx db.Tx) error {
		rows, err := tx.QueryContext(ctx, `
WITH ranked AS (
	SELECT id, source, tenant_id, status, created_at,
		ROW_NUMBER() OVER (PARTITION BY source, tenant_id ORDER BY created_at DESC, id DESC) AS recency_rank
	FROM people_directory_snapshots
)
SELECT ranked.id
FROM ranked
WHERE ranked.recency_rank > ?
	AND ranked.created_at < ?
	AND (
		ranked.status IN (?, ?)
		OR (
			ranked.status = ?
			AND NOT EXISTS (
				SELECT 1
				FROM people_directory_matches matches
				WHERE matches.snapshot_id = ranked.id
					AND matches.apply_status IN (?, ?)
			)
		)
	)
ORDER BY ranked.created_at ASC, ranked.id ASC
LIMIT ?`,
			policy.KeepLatest,
			cutoff,
			SnapshotStatusFailed,
			SnapshotStatusScanning,
			SnapshotStatusComplete,
			ApplyStatusPendingReview,
			ApplyStatusApplied,
			policy.Limit,
		)
		if err != nil {
			return fmt.Errorf("list People directory snapshots for retention: %w", err)
		}
		var snapshotIDs []string
		for rows.Next() {
			var snapshotID string
			if err := rows.Scan(&snapshotID); err != nil {
				_ = rows.Close()
				return fmt.Errorf("scan People directory snapshot retention candidate: %w", err)
			}
			snapshotIDs = append(snapshotIDs, snapshotID)
		}
		if err := rows.Close(); err != nil {
			return fmt.Errorf("close People directory snapshot retention candidates: %w", err)
		}
		if err := rows.Err(); err != nil {
			return fmt.Errorf("iterate People directory snapshot retention candidates: %w", err)
		}
		for _, snapshotID := range snapshotIDs {
			if _, err := tx.ExecContext(ctx, `DELETE FROM people_directory_matches WHERE snapshot_id = ?`, snapshotID); err != nil {
				return fmt.Errorf("delete People directory snapshot matches: %w", err)
			}
			if _, err := tx.ExecContext(ctx, `DELETE FROM people_directory_staged_users WHERE snapshot_id = ?`, snapshotID); err != nil {
				return fmt.Errorf("delete People directory staged users: %w", err)
			}
			result, err := tx.ExecContext(ctx, `DELETE FROM people_directory_snapshots WHERE id = ?`, snapshotID)
			if err != nil {
				return fmt.Errorf("delete People directory snapshot: %w", err)
			}
			rowsAffected, err := result.RowsAffected()
			if err != nil {
				return fmt.Errorf("count deleted People directory snapshots: %w", err)
			}
			deleted += int(rowsAffected)
		}
		return nil
	})
	return deleted, err
}
