package scheduler

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"orrery_v2/internal/platform/db"
)

const taskRunMaintenanceLockPrefix = "task-run-maintenance:"

type taskRunRetentionCandidate struct {
	key      string
	anchorAt time.Time
}

// CleanupTaskRuns removes expired terminal runs for one exact scheduled task.
func (r *Repository) CleanupTaskRuns(ctx context.Context, taskID string, now time.Time, completedRetention, diagnosticRetention time.Duration, limit int) (int, error) {
	if r == nil || r.router == nil {
		return 0, ErrServiceDependency
	}
	taskID = strings.TrimSpace(taskID)
	if taskID == "" || now.IsZero() || completedRetention <= 0 || diagnosticRetention < completedRetention || limit <= 0 {
		return 0, ErrInvalidRequest
	}
	now = now.UTC()
	store, table, err := r.router.StoreForTable(scheduledRunTable)
	if err != nil {
		return 0, err
	}
	manifestStore, manifestTable, err := r.router.StoreForTable(responsibilityEvaluationManifestTable)
	if err != nil {
		return 0, err
	}
	itemStore, itemTable, err := r.router.StoreForTable(responsibilityEvaluationItemTable)
	if err != nil {
		return 0, err
	}
	receiptStore, receiptTable, err := r.router.StoreForTable(scheduledRunReadReceiptTable)
	if err != nil {
		return 0, err
	}
	if store != manifestStore || store != itemStore || store != receiptStore {
		return 0, ErrServiceDependency
	}

	candidates := make([]taskRunRetentionCandidate, 0)
	err = store.WithReadTx(ctx, func(tx db.Tx) error {
		rows, err := taskRetentionRows(ctx, tx, table, taskID, now, completedRetention, diagnosticRetention, limit)
		if err != nil {
			return fmt.Errorf("list scheduled task runs for retention: %w", err)
		}
		for rows.Next() {
			var key, raw string
			if err := rows.Scan(&key, &raw); err != nil {
				_ = rows.Close()
				return fmt.Errorf("scan scheduled task run for retention: %w", err)
			}
			var record db.Record
			if err := json.Unmarshal([]byte(raw), &record); err != nil {
				_ = rows.Close()
				return fmt.Errorf("decode scheduled task run retention record: %w", err)
			}
			var run ScheduledRun
			if err := decodePayload(record, &run); err != nil {
				_ = rows.Close()
				return err
			}
			hydrateRunInternal(record, &run)
			if anchor, expired := taskRunRetentionAnchor(run, taskID, now, completedRetention, diagnosticRetention); expired {
				if key != run.ScheduledRunID {
					_ = rows.Close()
					return fmt.Errorf("%w: scheduler run record identity is inconsistent", ErrServiceDependency)
				}
				candidates = append(candidates, taskRunRetentionCandidate{key: key, anchorAt: anchor})
			}
		}
		if err := rows.Close(); err != nil {
			return fmt.Errorf("close scheduled task run retention rows: %w", err)
		}
		if err := rows.Err(); err != nil {
			return fmt.Errorf("iterate scheduled task run retention rows: %w", err)
		}
		sort.Slice(candidates, func(i, j int) bool {
			if candidates[i].anchorAt.Equal(candidates[j].anchorAt) {
				return candidates[i].key < candidates[j].key
			}
			return candidates[i].anchorAt.Before(candidates[j].anchorAt)
		})
		return nil
	})
	if err != nil {
		return 0, err
	}
	if len(candidates) == 0 {
		return 0, nil
	}
	deleted := 0
	err = store.WithTx(ctx, func(tx db.Tx) error {
		if err := db.LockRecordKeyTx(ctx, tx, table, taskRunMaintenanceLockPrefix+taskID); err != nil {
			return err
		}
		deleted, err = cleanupTaskRunCandidatesTx(
			ctx, tx, table, manifestTable, itemTable, receiptTable, taskID, candidates, now, completedRetention, diagnosticRetention, limit,
		)
		return err
	})
	if err != nil {
		return 0, err
	}
	if deleted > 0 {
		r.invalidateRunSnapshot()
	}
	return deleted, nil
}

func cleanupTaskRunCandidatesTx(
	ctx context.Context,
	tx db.Tx,
	table string,
	manifestTable string,
	itemTable string,
	receiptTable string,
	taskID string,
	candidates []taskRunRetentionCandidate,
	now time.Time,
	completedRetention time.Duration,
	diagnosticRetention time.Duration,
	limit int,
) (int, error) {
	deleted := 0
	for _, candidate := range candidates {
		if deleted >= limit {
			break
		}
		// Cleanup for one task is serialized by the maintenance lock. Taking only
		// the run lock here cannot cycle with MutateRunWithTask's task-then-run order.
		if err := db.LockRecordKeyTx(ctx, tx, table, candidate.key); err != nil {
			return deleted, err
		}
		record, ok, err := db.GetRecordTx(ctx, tx, table, candidate.key)
		if err != nil {
			return deleted, err
		}
		if !ok {
			continue
		}
		var run ScheduledRun
		if err := decodePayload(record, &run); err != nil {
			return deleted, err
		}
		hydrateRunInternal(record, &run)
		if candidate.key != run.ScheduledRunID {
			return deleted, fmt.Errorf("%w: scheduler run record identity is inconsistent", ErrServiceDependency)
		}
		if _, expired := taskRunRetentionAnchor(run, taskID, now, completedRetention, diagnosticRetention); !expired {
			continue
		}
		if err := cleanupResponsibilityEvaluationBatchChildrenTx(ctx, tx, manifestTable, itemTable, candidate.key); err != nil {
			return deleted, err
		}
		if err := cleanupRunReadReceiptsTx(ctx, tx, receiptTable, candidate.key); err != nil {
			return deleted, err
		}
		if err := db.DeleteRecordTx(ctx, tx, table, candidate.key); err != nil {
			return deleted, err
		}
		deleted++
	}
	return deleted, nil
}

func cleanupResponsibilityEvaluationBatchChildrenTx(ctx context.Context, tx db.Tx, manifestTable, itemTable, runID string) error {
	if err := db.LockRecordKeyTx(ctx, tx, manifestTable, runID); err != nil {
		return err
	}
	prefix := runID + ":"
	rows, err := tx.QueryContext(ctx, `SELECT record_key FROM platform_records WHERE table_name = ? AND record_key >= ? AND record_key < ? ORDER BY record_key`, itemTable, prefix, runID+";")
	if err != nil {
		return fmt.Errorf("list responsibility evaluation items for retention: %w", err)
	}
	itemKeys := make([]string, 0)
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			_ = rows.Close()
			return fmt.Errorf("scan responsibility evaluation item for retention: %w", err)
		}
		itemKeys = append(itemKeys, key)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close responsibility evaluation item retention rows: %w", err)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate responsibility evaluation item retention rows: %w", err)
	}
	for _, key := range itemKeys {
		if err := db.LockRecordKeyTx(ctx, tx, itemTable, key); err != nil {
			return err
		}
		if err := db.DeleteRecordTx(ctx, tx, itemTable, key); err != nil {
			return err
		}
	}
	if err := db.DeleteRecordTx(ctx, tx, manifestTable, runID); err != nil {
		return err
	}
	return nil
}

func taskRunRetentionAnchor(run ScheduledRun, taskID string, now time.Time, completedRetention, diagnosticRetention time.Duration) (time.Time, bool) {
	if run.ScheduledTaskID != taskID {
		return time.Time{}, false
	}
	retention := diagnosticRetention
	switch run.Status {
	case RunStatusCompleted:
		retention = completedRetention
	case RunStatusFailed, RunStatusSkipped:
	default:
		return time.Time{}, false
	}
	anchor := run.CompletedAt
	if anchor.IsZero() {
		anchor = run.CreatedAt
	}
	if anchor.IsZero() {
		return time.Time{}, false
	}
	anchor = anchor.UTC()
	return anchor, anchor.Before(now.UTC().Add(-retention))
}
