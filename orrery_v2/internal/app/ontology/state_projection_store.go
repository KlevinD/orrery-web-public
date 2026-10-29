package ontology

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"orrery_v2/internal/platform/db"
)

type PlatformStateProjectionStore struct {
	store *db.Store
}

type currentProjectionState struct {
	semanticHash    string
	appliedVersion  int64
	observedVersion int64
	sourceUpdatedAt int64
	projectedAt     int64
}

func NewPlatformStateProjectionStore(store *db.Store) *PlatformStateProjectionStore {
	return &PlatformStateProjectionStore{store: store}
}

func (s *PlatformStateProjectionStore) DeleteDailySnapshotsBefore(ctx context.Context, cutoffDate string, limit int) (int, error) {
	cutoffDate = strings.TrimSpace(cutoffDate)
	parsed, err := time.Parse("2006-01-02", cutoffDate)
	if err != nil || parsed.Format("2006-01-02") != cutoffDate {
		return 0, fmt.Errorf("ontology daily snapshot cutoff must be UTC YYYY-MM-DD")
	}
	if limit <= 0 {
		return 0, fmt.Errorf("ontology daily snapshot retention limit must be positive")
	}
	if s == nil || s.store == nil {
		return 0, fmt.Errorf("ontology state projection store is required")
	}
	type snapshotKey struct {
		tenantID     string
		eventType    string
		entityID     string
		snapshotDate string
	}
	deleted := 0
	err = s.store.WithTx(ctx, func(tx db.Tx) error {
		rows, err := tx.QueryContext(ctx, `
SELECT tenant_id, event_type, entity_id, snapshot_date
FROM ontology_daily_snapshots
WHERE snapshot_date < ?
ORDER BY snapshot_date ASC, tenant_id ASC, event_type ASC, entity_id ASC
LIMIT ?`, cutoffDate, limit)
		if err != nil {
			return fmt.Errorf("list expired ontology daily snapshots: %w", err)
		}
		keys := make([]snapshotKey, 0, limit)
		for rows.Next() {
			var key snapshotKey
			if err := rows.Scan(&key.tenantID, &key.eventType, &key.entityID, &key.snapshotDate); err != nil {
				_ = rows.Close()
				return fmt.Errorf("scan expired ontology daily snapshot: %w", err)
			}
			keys = append(keys, key)
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return fmt.Errorf("iterate expired ontology daily snapshots: %w", err)
		}
		if err := rows.Close(); err != nil {
			return fmt.Errorf("close expired ontology daily snapshots: %w", err)
		}
		for _, key := range keys {
			result, err := tx.ExecContext(ctx, `
DELETE FROM ontology_daily_snapshots
WHERE tenant_id = ? AND event_type = ? AND entity_id = ? AND snapshot_date = ?`,
				key.tenantID, key.eventType, key.entityID, key.snapshotDate)
			if err != nil {
				return fmt.Errorf("delete expired ontology daily snapshot: %w", err)
			}
			count, err := result.RowsAffected()
			if err != nil {
				return fmt.Errorf("inspect expired ontology daily snapshot deletion: %w", err)
			}
			deleted += int(count)
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return deleted, nil
}

func (s *PlatformStateProjectionStore) Inspect(ctx context.Context, input StateProjectionInput) (StateProjectionReport, error) {
	canonical, err := CanonicalStateProjection(input)
	if err != nil {
		return StateProjectionReport{}, err
	}
	if s == nil || s.store == nil {
		return StateProjectionReport{}, fmt.Errorf("ontology state projection store is required")
	}
	report := StateProjectionReport{SemanticHash: canonical.SemanticHash, SnapshotDate: canonical.SnapshotDate}
	err = s.store.WithTx(ctx, func(tx db.Tx) error {
		lockKey := strings.Join([]string{canonical.TenantID, canonical.EventType, canonical.EntityID}, "\x00")
		if err := db.LockRecordKeyTx(ctx, tx, "ontology_projection_state", lockKey); err != nil {
			return err
		}
		current, exists, err := getCurrentProjectionState(ctx, tx, canonical)
		if err != nil {
			return err
		}
		if !exists {
			report.Outcome = StateProjectionChanged
			return nil
		}
		order, err := compareStateProjectionOrder(canonical, current)
		if err != nil {
			return err
		}
		if order < 0 {
			report.Outcome = StateProjectionStale
			return nil
		}
		if canonical.SemanticHash != current.semanticHash {
			report.Outcome = StateProjectionChanged
			return nil
		}
		snapshotExists, err := hasDailyStateSnapshot(ctx, tx, canonical)
		if err != nil {
			return err
		}
		if snapshotExists {
			report.Outcome = StateProjectionNoop
		} else {
			report.Outcome = StateProjectionSnapshotOnly
		}
		return nil
	})
	if err != nil {
		return StateProjectionReport{}, err
	}
	return report, nil
}

func (s *PlatformStateProjectionStore) Apply(
	ctx context.Context,
	input StateProjectionInput,
	applyGraph func(context.Context, db.Tx) error,
) (StateProjectionReport, error) {
	canonical, err := CanonicalStateProjection(input)
	if err != nil {
		return StateProjectionReport{}, err
	}
	if s == nil || s.store == nil {
		return StateProjectionReport{}, fmt.Errorf("ontology state projection store is required")
	}
	report := StateProjectionReport{SemanticHash: canonical.SemanticHash, SnapshotDate: canonical.SnapshotDate}
	err = s.store.WithTx(ctx, func(tx db.Tx) error {
		lockKey := strings.Join([]string{canonical.TenantID, canonical.EventType, canonical.EntityID}, "\x00")
		if err := db.LockRecordKeyTx(ctx, tx, "ontology_projection_state", lockKey); err != nil {
			return err
		}
		current, exists, err := getCurrentProjectionState(ctx, tx, canonical)
		if err != nil {
			return err
		}
		snapshotExists, err := hasDailyStateSnapshot(ctx, tx, canonical)
		if err != nil {
			return err
		}
		if exists {
			order, err := compareStateProjectionOrder(canonical, current)
			if err != nil {
				return err
			}
			if order < 0 {
				report.Outcome = StateProjectionStale
				return nil
			}
			if canonical.SemanticHash == current.semanticHash {
				if order > 0 {
					if err := updateProjectionWatermark(ctx, tx, canonical); err != nil {
						return err
					}
				}
				if snapshotExists {
					report.Outcome = StateProjectionNoop
					return nil
				}
				if err := upsertDailyStateSnapshot(ctx, tx, canonical); err != nil {
					return err
				}
				report.Outcome = StateProjectionSnapshotOnly
				return nil
			}
		}
		if applyGraph == nil {
			return fmt.Errorf("ontology state graph apply function is required")
		}
		if err := applyGraph(ctx, tx); err != nil {
			return err
		}
		if err := upsertCurrentProjectionState(ctx, tx, canonical); err != nil {
			return err
		}
		if err := upsertDailyStateSnapshot(ctx, tx, canonical); err != nil {
			return err
		}
		report.Outcome = StateProjectionChanged
		return nil
	})
	if err != nil {
		return StateProjectionReport{}, err
	}
	return report, nil
}

func getCurrentProjectionState(ctx context.Context, tx db.Tx, input CanonicalStateProjectionInput) (currentProjectionState, bool, error) {
	var state currentProjectionState
	err := tx.QueryRowContext(ctx, `
SELECT semantic_hash, applied_version, observed_version, source_updated_at, projected_at
FROM ontology_projection_state
WHERE tenant_id = ? AND event_type = ? AND entity_id = ?`, input.TenantID, input.EventType, input.EntityID).Scan(
		&state.semanticHash,
		&state.appliedVersion,
		&state.observedVersion,
		&state.sourceUpdatedAt,
		&state.projectedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return currentProjectionState{}, false, nil
	}
	if err != nil {
		return currentProjectionState{}, false, fmt.Errorf("read ontology projection state: %w", err)
	}
	return state, true, nil
}

func hasDailyStateSnapshot(ctx context.Context, tx db.Tx, input CanonicalStateProjectionInput) (bool, error) {
	var found int
	err := tx.QueryRowContext(ctx, `
SELECT 1
FROM ontology_daily_snapshots
WHERE tenant_id = ? AND event_type = ? AND entity_id = ? AND snapshot_date = ?`,
		input.TenantID, input.EventType, input.EntityID, input.SnapshotDate,
	).Scan(&found)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read ontology daily snapshot: %w", err)
	}
	return true, nil
}

func compareStateProjectionOrder(input CanonicalStateProjectionInput, current currentProjectionState) (int, error) {
	if input.SourceVersion > 0 || current.observedVersion > 0 {
		if input.SourceVersion <= 0 || current.observedVersion <= 0 {
			if input.SourceVersion <= 0 {
				return 0, fmt.Errorf("%w: versioned state cannot accept an unversioned observation", ErrStateProjectionConflict)
			}
			return 1, nil
		}
		switch {
		case input.SourceVersion < current.observedVersion:
			return -1, nil
		case input.SourceVersion > current.observedVersion:
			return 1, nil
		case input.SemanticHash != current.semanticHash:
			if input.AllowEqualOrderRepair {
				return 1, nil
			}
			return 0, fmt.Errorf("%w: source version has multiple semantic payloads", ErrStateProjectionConflict)
		default:
			return 0, nil
		}
	}
	incoming := input.SourceUpdatedAt.UnixNano()
	switch {
	case incoming < current.sourceUpdatedAt:
		return -1, nil
	case incoming > current.sourceUpdatedAt:
		return 1, nil
	case input.SemanticHash != current.semanticHash:
		if input.AllowEqualOrderRepair {
			return 1, nil
		}
		return 0, fmt.Errorf("%w: source timestamp has multiple semantic payloads", ErrStateProjectionConflict)
	default:
		return 0, nil
	}
}

func updateProjectionWatermark(ctx context.Context, tx db.Tx, input CanonicalStateProjectionInput) error {
	_, err := tx.ExecContext(ctx, `
UPDATE ontology_projection_state
SET observed_version = ?, source_updated_at = ?
WHERE tenant_id = ? AND event_type = ? AND entity_id = ?`,
		input.SourceVersion,
		input.SourceUpdatedAt.UnixNano(),
		input.TenantID,
		input.EventType,
		input.EntityID,
	)
	if err != nil {
		return fmt.Errorf("update ontology projection watermark: %w", err)
	}
	return nil
}

func upsertCurrentProjectionState(ctx context.Context, tx db.Tx, input CanonicalStateProjectionInput) error {
	_, err := tx.ExecContext(ctx, `
INSERT INTO ontology_projection_state (
	tenant_id, event_type, entity_id, semantic_hash, applied_version,
	observed_version, source_updated_at, projected_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(tenant_id, event_type, entity_id) DO UPDATE SET
	semantic_hash = excluded.semantic_hash,
	applied_version = excluded.applied_version,
	observed_version = excluded.observed_version,
	source_updated_at = excluded.source_updated_at,
	projected_at = excluded.projected_at`,
		input.TenantID,
		input.EventType,
		input.EntityID,
		input.SemanticHash,
		input.SourceVersion,
		input.SourceVersion,
		input.SourceUpdatedAt.UnixNano(),
		input.SnapshotAt.UnixNano(),
	)
	if err != nil {
		return fmt.Errorf("save ontology projection state: %w", err)
	}
	return nil
}

func upsertDailyStateSnapshot(ctx context.Context, tx db.Tx, input CanonicalStateProjectionInput) error {
	_, err := tx.ExecContext(ctx, `
INSERT INTO ontology_daily_snapshots (
	tenant_id, event_type, entity_id, snapshot_date, semantic_hash, payload_json,
	source_version, source_updated_at, projected_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(tenant_id, event_type, entity_id, snapshot_date) DO UPDATE SET
	semantic_hash = excluded.semantic_hash,
	payload_json = excluded.payload_json,
	source_version = excluded.source_version,
	source_updated_at = excluded.source_updated_at,
	projected_at = excluded.projected_at`,
		input.TenantID,
		input.EventType,
		input.EntityID,
		input.SnapshotDate,
		input.SemanticHash,
		input.PayloadJSON,
		input.SourceVersion,
		input.SourceUpdatedAt.UnixNano(),
		input.SnapshotAt.UnixNano(),
	)
	if err != nil {
		return fmt.Errorf("save ontology daily snapshot: %w", err)
	}
	return nil
}
