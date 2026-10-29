package scheduler

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/platform/db"
)

const (
	scheduledTaskTable                    = "core.scheduled_tasks"
	scheduledRunTable                     = "core.scheduled_task_runs"
	responsibilityEvaluationManifestTable = "core.scheduled_responsibility_evaluation_manifests"
	responsibilityEvaluationItemTable     = "core.scheduled_responsibility_evaluation_items"
	schedulerPayload                      = "payload"
	schedulerTimeFmt                      = "2006-01-02T15:04:05.999999999Z07:00"

	schedulerSnapshotTTL = 500 * time.Millisecond
)

type Repository struct {
	router         *db.Router
	projectionSink ProjectionSink

	taskSnapshotMu sync.Mutex
	taskSnapshotAt time.Time
	taskSnapshot   []ScheduledTask
	runSnapshotMu  sync.Mutex
	runSnapshotAt  time.Time
	runSnapshot    []db.Record
}

type ProjectionSink interface {
	ProjectScheduledTask(context.Context, ScheduledTask) error
	ProjectScheduledRun(context.Context, ScheduledRun) error
}

func NewRepository(router *db.Router) *Repository {
	return &Repository{router: router}
}

func (r *Repository) SetProjectionSink(sink ProjectionSink) {
	if r != nil {
		r.projectionSink = sink
	}
}

func (r *Repository) SaveTask(ctx context.Context, task ScheduledTask) error {
	if task.AgentTaskID == "" {
		task.AgentTaskID = AgentTaskIDForScheduledTask(task.ScheduledTaskID)
	}
	if err := r.saveTaskRecord(ctx, task); err != nil {
		return err
	}
	if r.projectionSink != nil {
		return r.projectionSink.ProjectScheduledTask(ctx, task)
	}
	return nil
}

func (r *Repository) saveTaskRecord(ctx context.Context, task ScheduledTask) error {
	if r == nil || r.router == nil {
		return ErrServiceDependency
	}
	if task.AgentTaskID == "" {
		task.AgentTaskID = AgentTaskIDForScheduledTask(task.ScheduledTaskID)
	}
	record, err := scheduledTaskRecord(task)
	if err != nil {
		return err
	}
	store, table, err := r.router.StoreForTable(scheduledTaskTable)
	if err != nil {
		return err
	}
	if err := store.WithTx(ctx, func(tx db.Tx) error {
		if err := db.LockRecordKeyTx(ctx, tx, table, task.ScheduledTaskID); err != nil {
			return err
		}
		return db.PutRecordTx(ctx, tx, table, task.ScheduledTaskID, record)
	}); err != nil {
		return err
	}
	r.invalidateTaskSnapshot()
	return nil
}

// MutateTask serializes read-modify-write transitions across scheduler workers
// and application instances. Callers always inspect and mutate the latest
// durable task state while the database record lock is held.
func (r *Repository) MutateTask(ctx context.Context, scheduledTaskID string, mutate func(*ScheduledTask) error) (ScheduledTask, error) {
	if r == nil || r.router == nil || mutate == nil {
		return ScheduledTask{}, ErrServiceDependency
	}
	scheduledTaskID = strings.TrimSpace(scheduledTaskID)
	if scheduledTaskID == "" {
		return ScheduledTask{}, ErrInvalidRequest
	}
	store, table, err := r.router.StoreForTable(scheduledTaskTable)
	if err != nil {
		return ScheduledTask{}, err
	}
	updated := ScheduledTask{}
	err = store.WithTx(ctx, func(tx db.Tx) error {
		if err := db.LockRecordKeyTx(ctx, tx, table, scheduledTaskID); err != nil {
			return err
		}
		record, ok, err := db.GetRecordTx(ctx, tx, table, scheduledTaskID)
		if err != nil {
			return err
		}
		if !ok {
			return ErrNotFound
		}
		if err := decodePayload(record, &updated); err != nil {
			return err
		}
		hydrateTaskInternal(record, &updated)
		if updated.ScheduledTaskID != scheduledTaskID {
			return fmt.Errorf("%w: scheduler task record identity is inconsistent", ErrServiceDependency)
		}
		previous := updated
		previousRecord, err := scheduledTaskRecord(previous)
		if err != nil {
			return err
		}
		if err := mutate(&updated); err != nil {
			return err
		}
		if updated.AgentTaskID == "" {
			updated.AgentTaskID = AgentTaskIDForScheduledTask(scheduledTaskID)
		}
		if !scheduledTaskIdentityUnchanged(previous, updated) {
			return ErrInvalidRequest
		}
		next, err := scheduledTaskRecord(updated)
		if err != nil {
			return err
		}
		if previousRecord[schedulerPayload] != next[schedulerPayload] && !updated.UpdatedAt.After(previous.UpdatedAt) {
			updated.UpdatedAt = previous.UpdatedAt.UTC().Add(time.Nanosecond)
			next, err = scheduledTaskRecord(updated)
			if err != nil {
				return err
			}
		}
		if err := db.PutRecordTx(ctx, tx, table, scheduledTaskID, next); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return ScheduledTask{}, err
	}
	r.invalidateTaskSnapshot()
	if r.projectionSink != nil {
		return updated, r.projectionSink.ProjectScheduledTask(ctx, updated)
	}
	return updated, nil
}

func scheduledTaskIdentityUnchanged(previous, updated ScheduledTask) bool {
	return updated.ScheduledTaskID == previous.ScheduledTaskID &&
		updated.TenantID == previous.TenantID &&
		updated.AgentTaskID == previous.AgentTaskID &&
		updated.CreatedBy == previous.CreatedBy &&
		updated.CreatedByKind == previous.CreatedByKind &&
		updated.CreatedByName == previous.CreatedByName &&
		updated.CreatorProfileID == previous.CreatorProfileID &&
		updated.CreatedAt.Equal(previous.CreatedAt) &&
		updated.IdempotencyKey == previous.IdempotencyKey &&
		updated.IdempotencyHash == previous.IdempotencyHash
}

// AdoptTaskIdempotency repairs a missing create idempotency identity without
// allowing the task owner or an existing identity to be changed.
func (r *Repository) AdoptTaskIdempotency(ctx context.Context, scheduledTaskID string, owner ViewerRef, request CreateTaskRequest) (ScheduledTask, error) {
	if r == nil || r.router == nil {
		return ScheduledTask{}, ErrServiceDependency
	}
	scheduledTaskID = strings.TrimSpace(scheduledTaskID)
	owner.ViewerID = strings.TrimSpace(owner.ViewerID)
	owner.ProfileID = strings.TrimSpace(owner.ProfileID)
	request.IdempotencyKey = strings.TrimSpace(request.IdempotencyKey)
	if scheduledTaskID == "" || owner.ViewerID == "" || owner.ProfileID == "" || request.IdempotencyKey == "" {
		return ScheduledTask{}, ErrInvalidRequest
	}
	requestHash, err := schedulerRequestHash(request)
	if err != nil {
		return ScheduledTask{}, err
	}
	store, table, err := r.router.StoreForTable(scheduledTaskTable)
	if err != nil {
		return ScheduledTask{}, err
	}
	updated := ScheduledTask{}
	err = store.WithTx(ctx, func(tx db.Tx) error {
		if err := db.LockRecordKeyTx(ctx, tx, table, scheduledTaskID); err != nil {
			return err
		}
		record, ok, err := db.GetRecordTx(ctx, tx, table, scheduledTaskID)
		if err != nil {
			return err
		}
		if !ok {
			return ErrNotFound
		}
		if err := decodePayload(record, &updated); err != nil {
			return err
		}
		hydrateTaskInternal(record, &updated)
		if normalizeTenantID(updated.TenantID) != normalizeTenantID(owner.TenantID) ||
			strings.TrimSpace(updated.CreatedBy) != owner.ViewerID ||
			strings.TrimSpace(updated.CreatorProfileID) != owner.ProfileID {
			return ErrInvalidRequest
		}
		existingKey := strings.TrimSpace(updated.IdempotencyKey)
		if existingKey != "" && existingKey != request.IdempotencyKey {
			return ErrInvalidRequest
		}
		if existingKey == request.IdempotencyKey && updated.IdempotencyHash != "" && updated.IdempotencyHash != requestHash {
			return ErrInvalidRequest
		}
		updated.IdempotencyKey = request.IdempotencyKey
		updated.IdempotencyHash = requestHash
		next, err := scheduledTaskRecord(updated)
		if err != nil {
			return err
		}
		return db.PutRecordTx(ctx, tx, table, scheduledTaskID, next)
	})
	if err == nil {
		r.invalidateTaskSnapshot()
	}
	return updated, err
}

func (r *Repository) GetTask(ctx context.Context, scheduledTaskID string) (ScheduledTask, error) {
	if r == nil || r.router == nil {
		return ScheduledTask{}, ErrServiceDependency
	}
	record, ok, err := r.router.GetRecord(ctx, scheduledTaskTable, scheduledTaskID)
	if err != nil {
		return ScheduledTask{}, err
	}
	if !ok {
		return ScheduledTask{}, ErrNotFound
	}
	var out ScheduledTask
	if err := decodePayload(record, &out); err != nil {
		return ScheduledTask{}, err
	}
	hydrateTaskInternal(record, &out)
	return out, nil
}

func (r *Repository) DeleteTask(ctx context.Context, scheduledTaskID string) error {
	if r == nil || r.router == nil {
		return ErrServiceDependency
	}
	if _, err := r.GetTask(ctx, scheduledTaskID); err != nil {
		return err
	}
	store, table, err := r.router.StoreForTable(scheduledTaskTable)
	if err != nil {
		return err
	}
	if err := store.WithTx(ctx, func(tx db.Tx) error {
		if err := db.LockRecordKeyTx(ctx, tx, table, scheduledTaskID); err != nil {
			return err
		}
		return db.DeleteRecordTx(ctx, tx, table, scheduledTaskID)
	}); err != nil {
		return err
	}
	r.invalidateTaskSnapshot()
	return nil
}

func (r *Repository) FindTaskByIdempotency(ctx context.Context, tenantID, createdBy, idempotencyKey string) (ScheduledTask, bool, error) {
	if r == nil || r.router == nil {
		return ScheduledTask{}, false, ErrServiceDependency
	}
	// Idempotency checks must observe writes from other application instances
	// immediately; the short-lived workspace snapshot is intentionally bypassed.
	records, err := snapshot(ctx, r.router, scheduledTaskTable)
	if err != nil {
		return ScheduledTask{}, false, err
	}
	for _, record := range records {
		if got, _ := record["tenant_id"].(string); got != tenantID {
			continue
		}
		if got, _ := record["created_by"].(string); got != createdBy {
			continue
		}
		if got, _ := record["idempotency_key"].(string); got != idempotencyKey {
			continue
		}
		var task ScheduledTask
		if err := decodePayload(record, &task); err != nil {
			return ScheduledTask{}, false, err
		}
		hydrateTaskInternal(record, &task)
		if task.TenantID == tenantID && task.CreatedBy == createdBy && task.IdempotencyKey == idempotencyKey {
			return task, true, nil
		}
	}
	return ScheduledTask{}, false, nil
}

func (r *Repository) ListTasks(ctx context.Context, includeArchived bool) ([]ScheduledTask, error) {
	if r == nil || r.router == nil {
		return nil, ErrServiceDependency
	}
	tasks, err := r.loadTaskSnapshot(ctx)
	if err != nil {
		return nil, err
	}
	if includeArchived {
		return tasks, nil
	}
	out := tasks[:0]
	for _, task := range tasks {
		if task.Status != TaskStatusArchived {
			out = append(out, task)
		}
	}
	return out, nil
}

// loadTaskSnapshot coalesces the short burst of list requests made by the
// scheduler workspace while bounding staleness for writes from other instances.
func (r *Repository) loadTaskSnapshot(ctx context.Context) ([]ScheduledTask, error) {
	r.taskSnapshotMu.Lock()
	defer r.taskSnapshotMu.Unlock()

	if err := ctx.Err(); err != nil {
		return nil, err
	}
	now := time.Now()
	age := now.Sub(r.taskSnapshotAt)
	if !r.taskSnapshotAt.IsZero() && age >= 0 && age < schedulerSnapshotTTL {
		return append([]ScheduledTask(nil), r.taskSnapshot...), nil
	}
	records, err := snapshot(ctx, r.router, scheduledTaskTable)
	if err != nil {
		return nil, err
	}
	out := make([]ScheduledTask, 0, len(records))
	for _, record := range records {
		var task ScheduledTask
		if err := decodePayload(record, &task); err != nil {
			return nil, err
		}
		hydrateTaskInternal(record, &task)
		out = append(out, task)
	}
	sortTasks(out)
	r.taskSnapshot = out
	r.taskSnapshotAt = time.Now()
	return append([]ScheduledTask(nil), out...), nil
}

func (r *Repository) invalidateTaskSnapshot() {
	if r == nil {
		return
	}
	r.taskSnapshotMu.Lock()
	r.taskSnapshot = nil
	r.taskSnapshotAt = time.Time{}
	r.taskSnapshotMu.Unlock()
}

func (r *Repository) ListDueTasks(ctx context.Context, dueAt time.Time, limit int) ([]ScheduledTask, error) {
	tasks, err := r.ListTasks(ctx, false)
	if err != nil {
		return nil, err
	}
	out := make([]ScheduledTask, 0)
	for _, task := range tasks {
		if !task.Enabled || task.Status != TaskStatusEnabled || task.NextRunAt.IsZero() || task.NextRunAt.After(dueAt) {
			continue
		}
		out = append(out, task)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out, nil
}

func (r *Repository) SaveRun(ctx context.Context, run ScheduledRun) error {
	if r == nil || r.router == nil {
		return ErrServiceDependency
	}
	record, err := scheduledRunRecord(run)
	if err != nil {
		return err
	}
	if err := r.router.PutRecord(ctx, scheduledRunTable, run.ScheduledRunID, record); err != nil {
		return err
	}
	r.invalidateRunSnapshot()
	if r.projectionSink != nil {
		return r.projectionSink.ProjectScheduledRun(ctx, run)
	}
	return nil
}

func ResponsibilityEvaluationItemID(scheduledRunID, portfolioBindingID, portfolioVersionID string) string {
	digest := sha256.Sum256([]byte(strings.TrimSpace(portfolioBindingID) + "\x00" + strings.TrimSpace(portfolioVersionID)))
	return strings.TrimSpace(scheduledRunID) + ":" + fmt.Sprintf("%x", digest[:12])
}

func (r *Repository) GetResponsibilityEvaluationBatchManifest(ctx context.Context, scheduledRunID string) (ResponsibilityEvaluationBatchManifest, error) {
	if r == nil || r.router == nil {
		return ResponsibilityEvaluationBatchManifest{}, ErrServiceDependency
	}
	scheduledRunID = strings.TrimSpace(scheduledRunID)
	if scheduledRunID == "" {
		return ResponsibilityEvaluationBatchManifest{}, ErrInvalidRequest
	}
	record, ok, err := r.router.GetRecord(ctx, responsibilityEvaluationManifestTable, scheduledRunID)
	if err != nil {
		return ResponsibilityEvaluationBatchManifest{}, err
	}
	if !ok {
		return ResponsibilityEvaluationBatchManifest{}, ErrNotFound
	}
	var manifest ResponsibilityEvaluationBatchManifest
	if err := decodePayload(record, &manifest); err != nil {
		return ResponsibilityEvaluationBatchManifest{}, err
	}
	manifest.TenantID = recordString(record, "tenant_id")
	return manifest, nil
}

func (r *Repository) EnsureResponsibilityEvaluationBatchManifest(ctx context.Context, manifest ResponsibilityEvaluationBatchManifest) (ResponsibilityEvaluationBatchManifest, error) {
	if r == nil || r.router == nil {
		return ResponsibilityEvaluationBatchManifest{}, ErrServiceDependency
	}
	manifest.TenantID = strings.TrimSpace(manifest.TenantID)
	manifest.ScheduledTaskID = strings.TrimSpace(manifest.ScheduledTaskID)
	manifest.ScheduledRunID = strings.TrimSpace(manifest.ScheduledRunID)
	manifest.ScopeID = strings.TrimSpace(manifest.ScopeID)
	if manifest.TenantID == "" || manifest.ScheduledTaskID == "" || manifest.ScheduledRunID == "" || manifest.ScopeID == "" || manifest.Attempt <= 0 {
		return ResponsibilityEvaluationBatchManifest{}, ErrInvalidRequest
	}
	if manifest.CreatedAt.IsZero() {
		manifest.CreatedAt = time.Now().UTC()
	}
	runStore, runTable, err := r.router.StoreForTable(scheduledRunTable)
	if err != nil {
		return ResponsibilityEvaluationBatchManifest{}, err
	}
	manifestStore, manifestTable, err := r.router.StoreForTable(responsibilityEvaluationManifestTable)
	if err != nil {
		return ResponsibilityEvaluationBatchManifest{}, err
	}
	if runStore != manifestStore {
		return ResponsibilityEvaluationBatchManifest{}, ErrServiceDependency
	}
	stored := ResponsibilityEvaluationBatchManifest{}
	err = runStore.WithTx(ctx, func(tx db.Tx) error {
		if err := db.LockRecordKeyTx(ctx, tx, runTable, manifest.ScheduledRunID); err != nil {
			return err
		}
		if err := db.LockRecordKeyTx(ctx, tx, manifestTable, manifest.ScheduledRunID); err != nil {
			return err
		}
		runRecord, ok, err := db.GetRecordTx(ctx, tx, runTable, manifest.ScheduledRunID)
		if err != nil {
			return err
		}
		if !ok {
			return ErrNotFound
		}
		var run ScheduledRun
		if err := decodePayload(runRecord, &run); err != nil {
			return err
		}
		hydrateRunInternal(runRecord, &run)
		if run.ScheduledTaskID != manifest.ScheduledTaskID || strings.TrimSpace(run.TenantID) != manifest.TenantID ||
			run.Status != RunStatusRunning || run.Attempt != manifest.Attempt {
			return ErrRevisionConflict
		}
		existingRecord, found, err := db.GetRecordTx(ctx, tx, manifestTable, manifest.ScheduledRunID)
		if err != nil {
			return err
		}
		if found {
			if err := decodePayload(existingRecord, &stored); err != nil {
				return err
			}
			stored.TenantID = recordString(existingRecord, "tenant_id")
			if stored.TenantID != manifest.TenantID || stored.ScheduledTaskID != manifest.ScheduledTaskID ||
				stored.ScheduledRunID != manifest.ScheduledRunID || stored.ScopeID != manifest.ScopeID {
				return ErrRevisionConflict
			}
			return nil
		}
		payload, err := encodePayload(manifest)
		if err != nil {
			return err
		}
		record := db.Record{
			"tenant_id": manifest.TenantID, "scheduled_task_id": manifest.ScheduledTaskID,
			"scheduled_run_id": manifest.ScheduledRunID, schedulerPayload: payload,
		}
		if err := db.PutRecordTx(ctx, tx, manifestTable, manifest.ScheduledRunID, record); err != nil {
			return err
		}
		stored = manifest
		return nil
	})
	return stored, err
}

func (r *Repository) SaveResponsibilityEvaluationItem(ctx context.Context, item ResponsibilityEvaluationItem) error {
	if r == nil || r.router == nil {
		return ErrServiceDependency
	}
	item.ScheduledTaskID = strings.TrimSpace(item.ScheduledTaskID)
	item.ScheduledRunID = strings.TrimSpace(item.ScheduledRunID)
	item.TenantID = strings.TrimSpace(item.TenantID)
	item.PortfolioBindingID = strings.TrimSpace(item.PortfolioBindingID)
	item.PortfolioVersionID = strings.TrimSpace(item.PortfolioVersionID)
	item.AgentProfileID = strings.TrimSpace(item.AgentProfileID)
	item.Status = strings.TrimSpace(item.Status)
	item.ItemID = strings.TrimSpace(item.ItemID)
	expectedItemID := ResponsibilityEvaluationItemID(item.ScheduledRunID, item.PortfolioBindingID, item.PortfolioVersionID)
	if item.ItemID == "" {
		item.ItemID = expectedItemID
	}
	if item.ItemID == "" || item.ItemID != expectedItemID || item.TenantID == "" || item.ScheduledTaskID == "" || item.ScheduledRunID == "" || item.PortfolioBindingID == "" ||
		item.PortfolioVersionID == "" || item.AgentProfileID == "" || item.Attempt <= 0 ||
		(item.Status != ResponsibilityEvaluationItemCompleted && item.Status != ResponsibilityEvaluationItemFailed) {
		return ErrInvalidRequest
	}
	runStore, runTable, err := r.router.StoreForTable(scheduledRunTable)
	if err != nil {
		return err
	}
	itemStore, itemTable, err := r.router.StoreForTable(responsibilityEvaluationItemTable)
	if err != nil {
		return err
	}
	if runStore != itemStore {
		return ErrServiceDependency
	}
	return runStore.WithTx(ctx, func(tx db.Tx) error {
		if err := db.LockRecordKeyTx(ctx, tx, runTable, item.ScheduledRunID); err != nil {
			return err
		}
		if err := db.LockRecordKeyTx(ctx, tx, itemTable, item.ItemID); err != nil {
			return err
		}
		runRecord, ok, err := db.GetRecordTx(ctx, tx, runTable, item.ScheduledRunID)
		if err != nil {
			return err
		}
		if !ok {
			return ErrNotFound
		}
		var run ScheduledRun
		if err := decodePayload(runRecord, &run); err != nil {
			return err
		}
		hydrateRunInternal(runRecord, &run)
		if run.ScheduledTaskID != item.ScheduledTaskID || strings.TrimSpace(run.TenantID) != item.TenantID ||
			run.Status != RunStatusRunning || run.Attempt != item.Attempt {
			return ErrRevisionConflict
		}
		existingRecord, found, err := db.GetRecordTx(ctx, tx, itemTable, item.ItemID)
		if err != nil {
			return err
		}
		if found {
			var existing ResponsibilityEvaluationItem
			if err := decodePayload(existingRecord, &existing); err != nil {
				return err
			}
			existing.TenantID = recordString(existingRecord, "tenant_id")
			if existing.TenantID != item.TenantID || existing.ScheduledTaskID != item.ScheduledTaskID || existing.ScheduledRunID != item.ScheduledRunID ||
				existing.PortfolioBindingID != item.PortfolioBindingID || existing.PortfolioVersionID != item.PortfolioVersionID ||
				existing.AgentProfileID != item.AgentProfileID || existing.Attempt > item.Attempt {
				return ErrRevisionConflict
			}
			if !existing.CreatedAt.IsZero() {
				item.CreatedAt = existing.CreatedAt
			}
		}
		if item.CreatedAt.IsZero() {
			item.CreatedAt = time.Now().UTC()
		}
		if item.UpdatedAt.IsZero() {
			item.UpdatedAt = item.CreatedAt
		}
		payload, err := encodePayload(item)
		if err != nil {
			return err
		}
		record := db.Record{
			"tenant_id": item.TenantID, "scheduled_task_id": item.ScheduledTaskID, "scheduled_run_id": item.ScheduledRunID,
			"status": item.Status, schedulerPayload: payload,
		}
		return db.PutRecordTx(ctx, tx, itemTable, item.ItemID, record)
	})
}

func (r *Repository) GetResponsibilityEvaluationItem(ctx context.Context, itemID string) (ResponsibilityEvaluationItem, error) {
	if r == nil || r.router == nil {
		return ResponsibilityEvaluationItem{}, ErrServiceDependency
	}
	record, ok, err := r.router.GetRecord(ctx, responsibilityEvaluationItemTable, strings.TrimSpace(itemID))
	if err != nil {
		return ResponsibilityEvaluationItem{}, err
	}
	if !ok {
		return ResponsibilityEvaluationItem{}, ErrNotFound
	}
	var item ResponsibilityEvaluationItem
	if err := decodePayload(record, &item); err != nil {
		return ResponsibilityEvaluationItem{}, err
	}
	item.TenantID, _ = record["tenant_id"].(string)
	return item, nil
}

func (r *Repository) ListResponsibilityEvaluationItemsPage(
	ctx context.Context,
	scheduledRunID string,
	status string,
	cursor string,
	limit int,
) (ResponsibilityEvaluationItemPage, error) {
	if r == nil || r.router == nil {
		return ResponsibilityEvaluationItemPage{}, ErrServiceDependency
	}
	scheduledRunID, status, cursor = strings.TrimSpace(scheduledRunID), strings.TrimSpace(status), strings.TrimSpace(cursor)
	if scheduledRunID == "" || limit <= 0 || limit > 100 || (status != "" && status != ResponsibilityEvaluationItemCompleted && status != ResponsibilityEvaluationItemFailed) {
		return ResponsibilityEvaluationItemPage{}, ErrInvalidRequest
	}
	prefix := scheduledRunID + ":"
	if cursor != "" && !validResponsibilityEvaluationItemCursor(prefix, cursor) {
		return ResponsibilityEvaluationItemPage{}, ErrInvalidRequest
	}
	afterKey := cursor
	if afterKey == "" {
		afterKey = prefix
	}
	items := make([]ResponsibilityEvaluationItem, 0, limit+1)
	hasMore := false
	nextCursor := ""
	for len(items) <= limit {
		page, err := r.router.ListRecords(ctx, responsibilityEvaluationItemTable, afterKey, 100)
		if err != nil {
			return ResponsibilityEvaluationItemPage{}, err
		}
		if len(page.Items) == 0 {
			break
		}
		stop := false
		for _, keyed := range page.Items {
			afterKey = keyed.Key
			if !strings.HasPrefix(keyed.Key, prefix) {
				if keyed.Key > prefix+"\xff" {
					stop = true
					break
				}
				continue
			}
			var item ResponsibilityEvaluationItem
			if err := decodePayload(keyed.Record, &item); err != nil {
				return ResponsibilityEvaluationItemPage{}, err
			}
			item.TenantID, _ = keyed.Record["tenant_id"].(string)
			if status != "" && item.Status != status {
				continue
			}
			if len(items) == limit {
				hasMore = true
				break
			}
			items = append(items, item)
			nextCursor = keyed.Key
		}
		if hasMore || stop || !page.HasMore {
			break
		}
	}
	if !hasMore {
		nextCursor = ""
	}
	return ResponsibilityEvaluationItemPage{Items: items, NextCursor: nextCursor, HasMore: hasMore, Limit: limit}, nil
}

func validResponsibilityEvaluationItemCursor(prefix, cursor string) bool {
	if !strings.HasPrefix(cursor, prefix) {
		return false
	}
	digest := strings.TrimPrefix(cursor, prefix)
	if len(digest) != 24 {
		return false
	}
	for _, character := range digest {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}

// MutateRun serializes read-modify-write transitions across scheduler workers
// and application instances. Callers always mutate the latest durable state.
func (r *Repository) MutateRun(ctx context.Context, scheduledRunID string, mutate func(*ScheduledRun) error) (ScheduledRun, error) {
	if r == nil || r.router == nil || mutate == nil {
		return ScheduledRun{}, ErrServiceDependency
	}
	scheduledRunID = strings.TrimSpace(scheduledRunID)
	if scheduledRunID == "" {
		return ScheduledRun{}, ErrInvalidRequest
	}
	store, table, err := r.router.StoreForTable(scheduledRunTable)
	if err != nil {
		return ScheduledRun{}, err
	}
	updated := ScheduledRun{}
	err = store.WithTx(ctx, func(tx db.Tx) error {
		if err := db.LockRecordKeyTx(ctx, tx, table, scheduledRunID); err != nil {
			return err
		}
		record, ok, err := db.GetRecordTx(ctx, tx, table, scheduledRunID)
		if err != nil {
			return err
		}
		if !ok {
			return ErrNotFound
		}
		if err := decodePayload(record, &updated); err != nil {
			return err
		}
		hydrateRunInternal(record, &updated)
		if err := mutate(&updated); err != nil {
			return err
		}
		if strings.TrimSpace(updated.ScheduledRunID) != scheduledRunID || strings.TrimSpace(updated.ScheduledTaskID) == "" {
			return ErrInvalidRequest
		}
		next, err := scheduledRunRecord(updated)
		if err != nil {
			return err
		}
		return db.PutRecordTx(ctx, tx, table, scheduledRunID, next)
	})
	if err == nil {
		r.invalidateRunSnapshot()
	}
	return updated, err
}

// MutateRunWithTask holds the task and run record locks in a stable order so a
// run mutation can validate the exact task configuration that authorized it.
func (r *Repository) MutateRunWithTask(
	ctx context.Context,
	scheduledTaskID string,
	scheduledRunID string,
	mutate func(ScheduledTask, *ScheduledRun) error,
) (ScheduledRun, error) {
	if r == nil || r.router == nil || mutate == nil {
		return ScheduledRun{}, ErrServiceDependency
	}
	scheduledTaskID = strings.TrimSpace(scheduledTaskID)
	scheduledRunID = strings.TrimSpace(scheduledRunID)
	if scheduledTaskID == "" || scheduledRunID == "" {
		return ScheduledRun{}, ErrInvalidRequest
	}
	taskStore, taskTable, err := r.router.StoreForTable(scheduledTaskTable)
	if err != nil {
		return ScheduledRun{}, err
	}
	runStore, runTable, err := r.router.StoreForTable(scheduledRunTable)
	if err != nil {
		return ScheduledRun{}, err
	}
	if taskStore != runStore {
		return ScheduledRun{}, ErrServiceDependency
	}
	updated := ScheduledRun{}
	err = taskStore.WithTx(ctx, func(tx db.Tx) error {
		if err := db.LockRecordKeyTx(ctx, tx, taskTable, scheduledTaskID); err != nil {
			return err
		}
		if err := db.LockRecordKeyTx(ctx, tx, runTable, scheduledRunID); err != nil {
			return err
		}
		taskRecord, ok, err := db.GetRecordTx(ctx, tx, taskTable, scheduledTaskID)
		if err != nil {
			return err
		}
		if !ok {
			return ErrNotFound
		}
		var task ScheduledTask
		if err := decodePayload(taskRecord, &task); err != nil {
			return err
		}
		hydrateTaskInternal(taskRecord, &task)
		runRecord, ok, err := db.GetRecordTx(ctx, tx, runTable, scheduledRunID)
		if err != nil {
			return err
		}
		if !ok {
			return ErrNotFound
		}
		if err := decodePayload(runRecord, &updated); err != nil {
			return err
		}
		hydrateRunInternal(runRecord, &updated)
		if strings.TrimSpace(updated.ScheduledTaskID) != scheduledTaskID {
			return ErrNotFound
		}
		if err := mutate(task, &updated); err != nil {
			return err
		}
		if strings.TrimSpace(updated.ScheduledRunID) != scheduledRunID || strings.TrimSpace(updated.ScheduledTaskID) != scheduledTaskID {
			return ErrInvalidRequest
		}
		next, err := scheduledRunRecord(updated)
		if err != nil {
			return err
		}
		return db.PutRecordTx(ctx, tx, runTable, scheduledRunID, next)
	})
	if err == nil {
		r.invalidateRunSnapshot()
	}
	return updated, err
}

const todoAutomationRunMaintenanceLock = "todo-automation-run-maintenance"

// CoalesceEmptyTodoAutomationRun keeps one durable record per aggregation
// window while preserving every non-empty and failed scan as its own run.
func (r *Repository) CoalesceEmptyTodoAutomationRun(ctx context.Context, run ScheduledRun, window time.Duration) (string, bool, error) {
	if r == nil || r.router == nil {
		return "", false, ErrServiceDependency
	}
	if run.ScheduledRunID == "" || run.ScheduledTaskID == "" || run.Status != RunStatusCompleted || run.TargetMode != TargetModeTodoAutomationScan || run.TodoAutomationScan == nil || !todoAutomationScanIsEmpty(*run.TodoAutomationScan) || window <= 0 {
		return "", false, ErrInvalidRequest
	}
	store, table, err := r.router.StoreForTable(scheduledRunTable)
	if err != nil {
		return "", false, err
	}
	occurrenceAt := run.CompletedAt
	if occurrenceAt.IsZero() {
		occurrenceAt = run.CreatedAt
	}
	if occurrenceAt.IsZero() {
		occurrenceAt = time.Now().UTC()
	}
	aggregatedInto := ""
	err = store.WithTx(ctx, func(tx db.Tx) error {
		if err := db.LockRecordKeyTx(ctx, tx, table, todoAutomationRunMaintenanceLock); err != nil {
			return err
		}
		if err := db.LockRecordKeyTx(ctx, tx, table, "todo-automation-empty:"+run.ScheduledTaskID); err != nil {
			return err
		}
		if err := db.LockRecordKeyTx(ctx, tx, table, run.ScheduledRunID); err != nil {
			return err
		}
		rows, err := emptyAggregationRows(ctx, tx, table, run, occurrenceAt, window)
		if err != nil {
			return fmt.Errorf("list scheduler runs for aggregation: %w", err)
		}
		var candidate ScheduledRun
		var candidateActivityAt time.Time
		for rows.Next() {
			var key, raw string
			if err := rows.Scan(&key, &raw); err != nil {
				_ = rows.Close()
				return fmt.Errorf("scan scheduler run for aggregation: %w", err)
			}
			if key == run.ScheduledRunID {
				continue
			}
			var record db.Record
			if err := json.Unmarshal([]byte(raw), &record); err != nil {
				_ = rows.Close()
				return fmt.Errorf("decode scheduler aggregation record: %w", err)
			}
			var current ScheduledRun
			if err := decodePayload(record, &current); err != nil {
				_ = rows.Close()
				return err
			}
			hydrateRunInternal(record, &current)
			if current.ScheduledTaskID != run.ScheduledTaskID || current.Status != RunStatusCompleted || current.TargetMode != TargetModeTodoAutomationScan || current.TodoAutomationScan == nil || !todoAutomationScanIsEmpty(*current.TodoAutomationScan) || current.AggregatedIntoRunID != "" {
				continue
			}
			activityAt := current.LastOccurrenceAt
			if activityAt.IsZero() {
				activityAt = current.CompletedAt
			}
			if activityAt.IsZero() {
				activityAt = current.CreatedAt
			}
			distance := occurrenceAt.Sub(activityAt)
			if distance < 0 {
				distance = -distance
			}
			if distance > window {
				continue
			}
			candidateDistance := occurrenceAt.Sub(candidateActivityAt)
			if candidateDistance < 0 {
				candidateDistance = -candidateDistance
			}
			if candidate.ScheduledRunID == "" || distance < candidateDistance || (distance == candidateDistance && (activityAt.After(candidateActivityAt) || (activityAt.Equal(candidateActivityAt) && current.ScheduledRunID > candidate.ScheduledRunID))) {
				candidate = current
				candidateActivityAt = activityAt
			}
		}
		if err := rows.Close(); err != nil {
			return fmt.Errorf("close scheduler aggregation rows: %w", err)
		}
		if err := rows.Err(); err != nil {
			return fmt.Errorf("iterate scheduler aggregation rows: %w", err)
		}
		if candidate.ScheduledRunID == "" {
			run.AggregatedOccurrences = 1
			run.LastOccurrenceAt = occurrenceAt
			record, err := scheduledRunRecord(run)
			if err != nil {
				return err
			}
			return db.PutRecordTx(ctx, tx, table, run.ScheduledRunID, record)
		}
		if candidate.AggregatedOccurrences < 1 {
			candidate.AggregatedOccurrences = 1
		}
		candidate.AggregatedOccurrences++
		if candidate.LastOccurrenceAt.IsZero() || occurrenceAt.After(candidate.LastOccurrenceAt) {
			candidate.LastOccurrenceAt = occurrenceAt
		}
		record, err := scheduledRunRecord(candidate)
		if err != nil {
			return err
		}
		if err := db.PutRecordTx(ctx, tx, table, candidate.ScheduledRunID, record); err != nil {
			return err
		}
		if err := db.DeleteRecordTx(ctx, tx, table, run.ScheduledRunID); err != nil {
			return err
		}
		aggregatedInto = candidate.ScheduledRunID
		return nil
	})
	if err != nil {
		return "", false, err
	}
	r.invalidateRunSnapshot()
	return aggregatedInto, aggregatedInto != "", nil
}

// CleanupTodoAutomationRuns applies the shorter retention window only to
// successful empty scans. Failed, skipped, and non-empty scans retain their
// diagnostic record for the longer window; active runs are never removed.
func (r *Repository) CleanupTodoAutomationRuns(ctx context.Context, now time.Time, emptyRetention, diagnosticRetention time.Duration) (int, error) {
	if r == nil || r.router == nil {
		return 0, ErrServiceDependency
	}
	if now.IsZero() || emptyRetention <= 0 || diagnosticRetention <= 0 || diagnosticRetention < emptyRetention {
		return 0, ErrInvalidRequest
	}
	now = now.UTC()
	store, table, err := r.router.StoreForTable(scheduledRunTable)
	if err != nil {
		return 0, err
	}
	receiptStore, receiptTable, err := r.router.StoreForTable(scheduledRunReadReceiptTable)
	if err != nil {
		return 0, err
	}
	if store != receiptStore {
		return 0, ErrServiceDependency
	}
	deleted := 0
	err = store.WithTx(ctx, func(tx db.Tx) error {
		if err := db.LockRecordKeyTx(ctx, tx, table, todoAutomationRunMaintenanceLock); err != nil {
			return err
		}
		rows, err := todoRetentionRows(ctx, tx, table, now, emptyRetention, diagnosticRetention)
		if err != nil {
			return fmt.Errorf("list Todo automation runs for retention: %w", err)
		}
		type expiredRun struct {
			key string
			run ScheduledRun
		}
		expired := make([]expiredRun, 0)
		for rows.Next() {
			var key, raw string
			if err := rows.Scan(&key, &raw); err != nil {
				_ = rows.Close()
				return fmt.Errorf("scan Todo automation run for retention: %w", err)
			}
			var record db.Record
			if err := json.Unmarshal([]byte(raw), &record); err != nil {
				_ = rows.Close()
				return fmt.Errorf("decode Todo automation retention record: %w", err)
			}
			var run ScheduledRun
			if err := decodePayload(record, &run); err != nil {
				_ = rows.Close()
				return err
			}
			hydrateRunInternal(record, &run)
			if run.TargetMode != TargetModeTodoAutomationScan || (run.Status != RunStatusCompleted && run.Status != RunStatusFailed && run.Status != RunStatusSkipped) {
				continue
			}
			retention := diagnosticRetention
			anchor := run.CompletedAt
			if run.Status == RunStatusCompleted && run.TodoAutomationScan != nil && todoAutomationScanIsEmpty(*run.TodoAutomationScan) {
				retention = emptyRetention
				if !run.LastOccurrenceAt.IsZero() {
					anchor = run.LastOccurrenceAt
				}
			}
			if anchor.IsZero() {
				anchor = run.CreatedAt
			}
			if !anchor.IsZero() && anchor.UTC().Before(now.Add(-retention)) {
				expired = append(expired, expiredRun{key: key, run: run})
			}
		}
		if err := rows.Close(); err != nil {
			return fmt.Errorf("close Todo automation retention rows: %w", err)
		}
		if err := rows.Err(); err != nil {
			return fmt.Errorf("iterate Todo automation retention rows: %w", err)
		}
		for _, candidate := range expired {
			if candidate.key != candidate.run.ScheduledRunID {
				return fmt.Errorf("%w: scheduler run record identity is inconsistent", ErrServiceDependency)
			}
			if err := cleanupRunReadReceiptsTx(ctx, tx, receiptTable, candidate.key); err != nil {
				return err
			}
			if err := db.DeleteRecordTx(ctx, tx, table, candidate.key); err != nil {
				return err
			}
			deleted++
		}
		return nil
	})
	if err == nil && deleted > 0 {
		r.invalidateRunSnapshot()
	}
	return deleted, err
}

func scheduledTaskRecord(task ScheduledTask) (db.Record, error) {
	payload, err := encodePayload(task)
	if err != nil {
		return nil, err
	}
	return db.Record{
		"scheduled_task_id": task.ScheduledTaskID,
		"tenant_id":         task.TenantID,
		"created_by":        task.CreatedBy,
		"idempotency_key":   task.IdempotencyKey,
		"idempotency_hash":  task.IdempotencyHash,
		"status":            string(task.Status),
		"enabled":           task.Enabled,
		"next_run_at":       formatTime(task.NextRunAt),
		"updated_at":        formatTime(task.UpdatedAt),
		schedulerPayload:    payload,
	}, nil
}

func scheduledRunRecord(run ScheduledRun) (db.Record, error) {
	payload, err := encodePayload(run)
	if err != nil {
		return nil, err
	}
	return db.Record{
		"scheduled_run_id": run.ScheduledRunID, "scheduled_task_id": run.ScheduledTaskID,
		"status": string(run.Status), "target_mode": string(run.TargetMode),
		"trace_ref": run.TraceRef, "trace_version": run.TraceVersion, "trace_status": run.TraceStatus,
		"created_at": formatTime(run.CreatedAt), "requested_by": run.RequestedBy,
		"idempotency_key": run.IdempotencyKey, "idempotency_hash": run.IdempotencyHash,
		schedulerPayload: payload,
	}, nil
}

func mergeScheduledRunPersistenceFields(current, incoming ScheduledRun) ScheduledRun {
	if incoming.CommandID == "" {
		incoming.CommandID = current.CommandID
	}
	if incoming.ConversationID == "" {
		incoming.ConversationID = current.ConversationID
	}
	if incoming.AgentID == "" {
		incoming.AgentID = current.AgentID
	}
	if incoming.MissionID == "" {
		incoming.MissionID = current.MissionID
	}
	if incoming.TaskID == "" {
		incoming.TaskID = current.TaskID
	}
	if incoming.ResponsibilityID == "" {
		incoming.ResponsibilityID = current.ResponsibilityID
	}
	if incoming.ExecutionEnvelope == nil {
		incoming.ExecutionEnvelope = agentcore.CloneExecutionEnvelope(current.ExecutionEnvelope)
	}
	if incoming.TraceRef == "" {
		incoming.TraceRef = current.TraceRef
		incoming.TraceVersion = current.TraceVersion
		incoming.TraceAvailable = current.TraceAvailable
		incoming.TraceStatus = current.TraceStatus
	}
	if incoming.TodoAutomationScan == nil {
		incoming.TodoAutomationScan = current.TodoAutomationScan
	}
	if incoming.ResponsibilityEvaluation == nil {
		incoming.ResponsibilityEvaluation = current.ResponsibilityEvaluation
	}
	if incoming.ResponsibilityEvaluationBatch == nil {
		incoming.ResponsibilityEvaluationBatch = current.ResponsibilityEvaluationBatch
	}
	if incoming.ResponsibilityReconcile == nil {
		incoming.ResponsibilityReconcile = current.ResponsibilityReconcile
	}
	if incoming.AggregatedOccurrences == 0 {
		incoming.AggregatedOccurrences = current.AggregatedOccurrences
	}
	if incoming.LastOccurrenceAt.IsZero() {
		incoming.LastOccurrenceAt = current.LastOccurrenceAt
	}
	if incoming.AggregatedIntoRunID == "" {
		incoming.AggregatedIntoRunID = current.AggregatedIntoRunID
	}
	if incoming.StartedAt.IsZero() {
		incoming.StartedAt = current.StartedAt
	}
	if incoming.CreatedAt.IsZero() {
		incoming.CreatedAt = current.CreatedAt
	}
	if incoming.RequestedBy == "" {
		incoming.RequestedBy = current.RequestedBy
	}
	if incoming.IdempotencyKey == "" {
		incoming.IdempotencyKey = current.IdempotencyKey
	}
	if incoming.IdempotencyHash == "" {
		incoming.IdempotencyHash = current.IdempotencyHash
	}
	return incoming
}

func (r *Repository) GetRun(ctx context.Context, scheduledRunID string) (ScheduledRun, error) {
	if r == nil || r.router == nil {
		return ScheduledRun{}, ErrServiceDependency
	}
	record, ok, err := r.router.GetRecord(ctx, scheduledRunTable, scheduledRunID)
	if err != nil {
		return ScheduledRun{}, err
	}
	if !ok {
		return ScheduledRun{}, ErrNotFound
	}
	var out ScheduledRun
	if err := decodePayload(record, &out); err != nil {
		return ScheduledRun{}, err
	}
	hydrateRunInternal(record, &out)
	return out, nil
}

func (r *Repository) FindManualRunByIdempotency(ctx context.Context, scheduledTaskID, requestedBy, idempotencyKey string) (ScheduledRun, bool, error) {
	if r == nil || r.router == nil {
		return ScheduledRun{}, false, ErrServiceDependency
	}
	// Keep cross-instance manual-run idempotency strongly consistent.
	records, err := snapshot(ctx, r.router, scheduledRunTable)
	if err != nil {
		return ScheduledRun{}, false, err
	}
	for _, record := range records {
		if got, _ := record["scheduled_task_id"].(string); got != scheduledTaskID {
			continue
		}
		if got, _ := record["requested_by"].(string); got != requestedBy {
			continue
		}
		if got, _ := record["idempotency_key"].(string); got != idempotencyKey {
			continue
		}
		var run ScheduledRun
		if err := decodePayload(record, &run); err != nil {
			return ScheduledRun{}, false, err
		}
		hydrateRunInternal(record, &run)
		if run.TriggerType == RunTriggerManual && run.RequestedBy == requestedBy && run.IdempotencyKey == idempotencyKey {
			return run, true, nil
		}
	}
	return ScheduledRun{}, false, nil
}

func (r *Repository) ListRunsForTask(ctx context.Context, scheduledTaskID string) ([]ScheduledRun, error) {
	if r == nil || r.router == nil {
		return nil, ErrServiceDependency
	}
	records, err := snapshot(ctx, r.router, scheduledRunTable)
	if err != nil {
		return nil, err
	}
	out := make([]ScheduledRun, 0)
	for _, record := range records {
		if got, _ := record["scheduled_task_id"].(string); got != scheduledTaskID {
			continue
		}
		var run ScheduledRun
		if err := decodePayload(record, &run); err != nil {
			return nil, err
		}
		hydrateRunInternal(record, &run)
		out = append(out, run)
	}
	sortRuns(out)
	return out, nil
}

func (r *Repository) LastRunForTask(ctx context.Context, scheduledTaskID string) (ScheduledRun, bool, error) {
	scheduledTaskID = strings.TrimSpace(scheduledTaskID)
	runs, err := r.ListRunsForTask(ctx, scheduledTaskID)
	if err != nil {
		return ScheduledRun{}, false, err
	}
	if len(runs) == 0 {
		return ScheduledRun{}, false, nil
	}
	return runs[len(runs)-1], true, nil
}

func (r *Repository) LastRunsForTasks(ctx context.Context, scheduledTaskIDs []string) (map[string]ScheduledRun, error) {
	if r == nil || r.router == nil {
		return nil, ErrServiceDependency
	}
	wanted := make(map[string]struct{}, len(scheduledTaskIDs))
	for _, scheduledTaskID := range scheduledTaskIDs {
		if scheduledTaskID = strings.TrimSpace(scheduledTaskID); scheduledTaskID != "" {
			wanted[scheduledTaskID] = struct{}{}
		}
	}
	out := make(map[string]ScheduledRun, len(wanted))
	if len(wanted) == 0 {
		return out, nil
	}
	records, err := r.loadRunSnapshot(ctx)
	if err != nil {
		return nil, err
	}
	for _, record := range records {
		scheduledTaskID, _ := record["scheduled_task_id"].(string)
		if _, ok := wanted[scheduledTaskID]; !ok {
			continue
		}
		var run ScheduledRun
		if err := decodePayload(record, &run); err != nil {
			return nil, err
		}
		hydrateRunInternal(record, &run)
		current, ok := out[scheduledTaskID]
		if !ok || current.CreatedAt.Before(run.CreatedAt) || (current.CreatedAt.Equal(run.CreatedAt) && current.ScheduledRunID < run.ScheduledRunID) {
			out[scheduledTaskID] = run
		}
	}
	return out, nil
}

func (r *Repository) HasActiveRun(ctx context.Context, scheduledTaskID string) (bool, error) {
	runs, err := r.ListRunsForTask(ctx, scheduledTaskID)
	if err != nil {
		return false, err
	}
	for _, run := range runs {
		if run.Status == RunStatusQueued || run.Status == RunStatusRunning || (run.Status == RunStatusFailed && run.CompletedAt.IsZero()) {
			return true, nil
		}
	}
	return false, nil
}

func (r *Repository) ListActiveRuns(ctx context.Context) ([]ScheduledRun, error) {
	if r == nil || r.router == nil {
		return nil, ErrServiceDependency
	}
	records, err := snapshot(ctx, r.router, scheduledRunTable)
	if err != nil {
		return nil, err
	}
	out := make([]ScheduledRun, 0)
	for _, record := range records {
		var run ScheduledRun
		if err := decodePayload(record, &run); err != nil {
			return nil, err
		}
		hydrateRunInternal(record, &run)
		if run.Status == RunStatusQueued || run.Status == RunStatusRunning {
			out = append(out, run)
		}
	}
	sortRuns(out)
	return out, nil
}

func (r *Repository) loadRunSnapshot(ctx context.Context) ([]db.Record, error) {
	r.runSnapshotMu.Lock()
	defer r.runSnapshotMu.Unlock()

	if err := ctx.Err(); err != nil {
		return nil, err
	}
	now := time.Now()
	age := now.Sub(r.runSnapshotAt)
	if !r.runSnapshotAt.IsZero() && age >= 0 && age < schedulerSnapshotTTL {
		return append([]db.Record(nil), r.runSnapshot...), nil
	}
	records, err := snapshot(ctx, r.router, scheduledRunTable)
	if err != nil {
		return nil, err
	}
	r.runSnapshot = records
	r.runSnapshotAt = time.Now()
	return append([]db.Record(nil), records...), nil
}

func (r *Repository) invalidateRunSnapshot() {
	if r == nil {
		return
	}
	r.runSnapshotMu.Lock()
	r.runSnapshot = nil
	r.runSnapshotAt = time.Time{}
	r.runSnapshotMu.Unlock()
}

// ListPendingDeliveryRuns returns completed Agent occurrences and failed Agent
// occurrences with a durable result whose notification is not terminal yet.
func (r *Repository) ListPendingDeliveryRuns(ctx context.Context) ([]ScheduledRun, error) {
	if r == nil || r.router == nil {
		return nil, ErrServiceDependency
	}
	records, err := snapshot(ctx, r.router, scheduledRunTable)
	if err != nil {
		return nil, err
	}
	out := make([]ScheduledRun, 0)
	for _, record := range records {
		var run ScheduledRun
		if err := decodePayload(record, &run); err != nil {
			return nil, err
		}
		hydrateRunInternal(record, &run)
		if scheduledRunNeedsDeliveryReconciliation(run) {
			out = append(out, run)
		}
	}
	sortRuns(out)
	return out, nil
}

func scheduledRunNeedsDeliveryReconciliation(run ScheduledRun) bool {
	hasDurableResult := strings.TrimSpace(run.Result.Summary) != "" || len(run.Result.Artifacts) > 0
	resultIsTerminal := run.Status == RunStatusCompleted || run.Status == RunStatusFailed && hasDurableResult
	if !resultIsTerminal || run.TargetMode != TargetModeAgentTask || run.ConfigurationSnapshotVersion <= 0 ||
		!strings.EqualFold(strings.TrimSpace(run.Delivery.Mode), "announce") {
		return false
	}
	switch strings.TrimSpace(run.DeliveryStatus) {
	case "delivered", "failed", "dead_letter", "not_requested", "not_recovered":
		return false
	default:
		return true
	}
}

func sortTasks(tasks []ScheduledTask) {
	sort.Slice(tasks, func(i, j int) bool {
		if tasks[i].NextRunAt.Equal(tasks[j].NextRunAt) {
			return tasks[i].ScheduledTaskID < tasks[j].ScheduledTaskID
		}
		if tasks[i].NextRunAt.IsZero() {
			return false
		}
		if tasks[j].NextRunAt.IsZero() {
			return true
		}
		return tasks[i].NextRunAt.Before(tasks[j].NextRunAt)
	})
}

func sortRuns(runs []ScheduledRun) {
	sort.Slice(runs, func(i, j int) bool {
		if runs[i].CreatedAt.Equal(runs[j].CreatedAt) {
			return runs[i].ScheduledRunID < runs[j].ScheduledRunID
		}
		return runs[i].CreatedAt.Before(runs[j].CreatedAt)
	})
}

func snapshot(ctx context.Context, router *db.Router, logicalTable string) ([]db.Record, error) {
	out := make([]db.Record, 0)
	afterKey := ""
	for {
		page, err := router.ListRecords(ctx, logicalTable, afterKey, db.MaxRecordPageLimit)
		if err != nil {
			return nil, err
		}
		for _, item := range page.Items {
			out = append(out, item.Record)
			afterKey = item.Key
		}
		if !page.HasMore {
			return out, nil
		}
		if len(page.Items) == 0 {
			return nil, fmt.Errorf("scheduler record pagination for %s made no progress", logicalTable)
		}
	}
}

func encodePayload(value any) (string, error) {
	payload, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("encode scheduler payload: %w", err)
	}
	return string(payload), nil
}

func decodePayload(record db.Record, out any) error {
	raw, ok := record[schedulerPayload].(string)
	if !ok || raw == "" {
		return fmt.Errorf("scheduler repository payload missing")
	}
	if err := json.Unmarshal([]byte(raw), out); err != nil {
		return fmt.Errorf("decode scheduler payload: %w", err)
	}
	return nil
}

func formatTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(schedulerTimeFmt)
}

func hydrateTaskInternal(record db.Record, task *ScheduledTask) {
	if task == nil {
		return
	}
	task.TenantID = normalizeTenantID(recordString(record, "tenant_id"))
	task.IdempotencyKey = recordString(record, "idempotency_key")
	task.IdempotencyHash = recordString(record, "idempotency_hash")
	if task.AgentTaskID == "" {
		task.AgentTaskID = AgentTaskIDForScheduledTask(task.ScheduledTaskID)
	}
}

func hydrateRunInternal(record db.Record, run *ScheduledRun) {
	if run == nil {
		return
	}
	run.RequestedBy = recordString(record, "requested_by")
	run.IdempotencyKey = recordString(record, "idempotency_key")
	run.IdempotencyHash = recordString(record, "idempotency_hash")
	run.TraceRef = recordString(record, "trace_ref")
	run.TraceVersion = recordInt(record, "trace_version")
	run.TraceAvailable = strings.TrimSpace(run.TraceRef) != "" && run.TraceVersion > 0
	run.TraceStatus = recordString(record, "trace_status")
}

func recordInt(record db.Record, key string) int {
	switch value := record[key].(type) {
	case int:
		return value
	case int64:
		return int(value)
	case float64:
		return int(value)
	default:
		return 0
	}
}

func recordString(record db.Record, key string) string {
	value, _ := record[key].(string)
	return value
}
