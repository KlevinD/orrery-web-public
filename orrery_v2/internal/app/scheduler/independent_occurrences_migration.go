package scheduler

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"orrery_v2/internal/platform/db"
)

const independentOccurrencesMigration = "independent-occurrences-v1"
const schedulerMigrationTable = "scheduler_policy_migrations"

// MigrateIndependentOccurrences upgrades existing schedules once, before workers
// start. It neither advances occurrences nor alters runs, approvals or task
// lifecycle state. A durable marker prevents later restarts from overwriting an
// operator's subsequent explicit concurrency choice.
func (r *Repository) MigrateIndependentOccurrences(ctx context.Context, now time.Time) error {
	if r == nil || r.router == nil {
		return ErrServiceDependency
	}
	store, table, err := r.router.StoreForTable(scheduledTaskTable)
	if err != nil {
		return err
	}
	err = store.WithTx(ctx, func(tx db.Tx) error {
		if err := db.LockRecordKeyTx(ctx, tx, schedulerMigrationTable, independentOccurrencesMigration); err != nil {
			return err
		}
		if _, done, err := db.GetRecordTx(ctx, tx, schedulerMigrationTable, independentOccurrencesMigration); err != nil || done {
			return err
		}
		changed := 0
		for after := ""; ; {
			page, err := db.ListRecordsTx(ctx, tx, table, after, 256)
			if err != nil {
				return err
			}
			for _, item := range page.Items {
				if err := db.LockRecordKeyTx(ctx, tx, table, item.Key); err != nil {
					return err
				}
				// Re-read after locking so a concurrent edit is preserved.
				record, found, err := db.GetRecordTx(ctx, tx, table, item.Key)
				if err != nil {
					return err
				}
				if !found {
					continue
				}
				var task ScheduledTask
				if err := decodePayload(record, &task); err != nil {
					return err
				}
				if task.ScheduledTaskID != item.Key {
					return fmt.Errorf("%w: scheduler task identity is inconsistent", ErrServiceDependency)
				}
				if task.Policy.Concurrency != "" && task.Policy.Concurrency != ConcurrencySkipIfRunning {
					continue
				}
				// Patch only these fields, retaining unknown payload/policy fields
				// and private record metadata as well as all execution contracts.
				var payload map[string]json.RawMessage
				if err := json.Unmarshal([]byte(recordString(record, schedulerPayload)), &payload); err != nil {
					return err
				}
				policy := map[string]json.RawMessage{}
				if value := payload["policy"]; len(value) > 0 && string(value) != "null" {
					if err := json.Unmarshal(value, &policy); err != nil {
						return err
					}
				}
				policy["concurrency"], _ = json.Marshal(ConcurrencyAllowParallel)
				payload["policy"], _ = json.Marshal(policy)
				updatedAt := nextScheduledTaskMutationTime(now, task.UpdatedAt)
				payload["updated_at"], _ = json.Marshal(updatedAt)
				encoded, err := json.Marshal(payload)
				if err != nil {
					return err
				}
				record[schedulerPayload] = string(encoded)
				record["updated_at"] = formatTime(updatedAt)
				if err := db.PutRecordTx(ctx, tx, table, item.Key, record); err != nil {
					return err
				}
				changed++
			}
			if !page.HasMore {
				break
			}
			after = page.Items[len(page.Items)-1].Key
		}
		return db.PutRecordTx(ctx, tx, schedulerMigrationTable, independentOccurrencesMigration, db.Record{
			"applied_at": formatTime(now), "changed_tasks": changed,
		})
	})
	if err == nil {
		r.invalidateTaskSnapshot()
	}
	return err
}
