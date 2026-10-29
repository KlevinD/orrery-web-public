package scheduler

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"orrery_v2/internal/platform/db"
)

const scheduledRunReadReceiptTable = "core.scheduled_run_read_receipts"

type RunInboxReadState string

const (
	RunInboxReadAll    RunInboxReadState = "all"
	RunInboxReadUnread RunInboxReadState = "unread"
	RunInboxReadRead   RunInboxReadState = "read"
)

type ListRunInboxQuery struct {
	ScheduledTaskID string
	ScheduledRunID  string
	Status          RunStatus
	ReadState       RunInboxReadState
	AttentionOnly   bool
	GroupByTask     bool
	Limit           int
	Offset          int
}

type ScheduledRunInboxItem struct {
	Run               ScheduledRun
	TaskTitle         string
	IsRead            bool
	ReadAt            time.Time
	AttentionRequired bool
}

type ScheduledRunInboxPage struct {
	Items          []ScheduledRunInboxItem
	Total          int
	UnreadTotal    int
	AttentionTotal int
	Limit          int
	Offset         int
}

type SetRunReadStateRequest struct {
	ScheduledRunID string
	Read           bool
}

type ScheduledRunReadState struct {
	ScheduledRunID string
	IsRead         bool
	ReadAt         time.Time
}

type scheduledRunReadReceipt struct {
	TenantID       string     `json:"tenant_id"`
	ViewerID       string     `json:"viewer_id"`
	ViewerKind     ViewerKind `json:"viewer_kind"`
	ScheduledRunID string     `json:"scheduled_run_id"`
	ReadAt         time.Time  `json:"read_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

func (r *Repository) ListRunsForTenant(ctx context.Context, tenantID string) ([]ScheduledRun, error) {
	if r == nil || r.router == nil {
		return nil, ErrServiceDependency
	}
	tenantID = normalizeTenantID(tenantID)
	records, err := r.loadRunSnapshot(ctx)
	if err != nil {
		return nil, err
	}
	runs := make([]ScheduledRun, 0)
	for _, record := range records {
		var run ScheduledRun
		if err := decodePayload(record, &run); err != nil {
			return nil, err
		}
		hydrateRunInternal(record, &run)
		if normalizeTenantID(run.TenantID) == tenantID {
			runs = append(runs, run)
		}
	}
	sortRuns(runs)
	return runs, nil
}

func (r *Repository) ListRunReadReceipts(ctx context.Context, tenantID, viewerID string, viewerKind ViewerKind) (map[string]scheduledRunReadReceipt, error) {
	if r == nil || r.router == nil {
		return nil, ErrServiceDependency
	}
	tenantID = normalizeTenantID(tenantID)
	viewerID = strings.TrimSpace(viewerID)
	viewerKind = normalizeViewerKind(viewerKind)
	if viewerID == "" {
		return nil, ErrInvalidRequest
	}
	records, err := snapshot(ctx, r.router, scheduledRunReadReceiptTable)
	if err != nil {
		return nil, err
	}
	receipts := make(map[string]scheduledRunReadReceipt)
	for _, record := range records {
		if recordString(record, "tenant_id") != tenantID || recordString(record, "viewer_id") != viewerID || ViewerKind(recordString(record, "viewer_kind")) != viewerKind {
			continue
		}
		var receipt scheduledRunReadReceipt
		if err := decodePayload(record, &receipt); err != nil {
			return nil, err
		}
		if receipt.ScheduledRunID != "" && !receipt.ReadAt.IsZero() {
			receipts[receipt.ScheduledRunID] = receipt
		}
	}
	return receipts, nil
}

func (r *Repository) SetRunReadState(ctx context.Context, receipt scheduledRunReadReceipt, read bool) error {
	if r == nil || r.router == nil {
		return ErrServiceDependency
	}
	receipt.TenantID = normalizeTenantID(receipt.TenantID)
	receipt.ViewerID = strings.TrimSpace(receipt.ViewerID)
	receipt.ViewerKind = normalizeViewerKind(receipt.ViewerKind)
	receipt.ScheduledRunID = strings.TrimSpace(receipt.ScheduledRunID)
	if receipt.ViewerID == "" || receipt.ScheduledRunID == "" {
		return ErrInvalidRequest
	}
	key := scheduledRunReadReceiptKey(receipt.TenantID, receipt.ViewerID, receipt.ViewerKind, receipt.ScheduledRunID)
	store, table, err := r.router.StoreForTable(scheduledRunReadReceiptTable)
	if err != nil {
		return err
	}
	return store.WithTx(ctx, func(tx db.Tx) error {
		if err := db.LockRecordKeyTx(ctx, tx, table, key); err != nil {
			return err
		}
		if !read {
			return db.DeleteRecordTx(ctx, tx, table, key)
		}
		if receipt.ReadAt.IsZero() || receipt.UpdatedAt.IsZero() {
			return ErrInvalidRequest
		}
		payload, err := encodePayload(receipt)
		if err != nil {
			return err
		}
		return db.PutRecordTx(ctx, tx, table, key, db.Record{
			"tenant_id": receipt.TenantID, "viewer_id": receipt.ViewerID, "viewer_kind": string(receipt.ViewerKind),
			"scheduled_run_id": receipt.ScheduledRunID, schedulerPayload: payload,
		})
	})
}

func scheduledRunReadReceiptKey(tenantID, viewerID string, viewerKind ViewerKind, scheduledRunID string) string {
	scheduledRunID = strings.TrimSpace(scheduledRunID)
	digest := sha256.Sum256([]byte(normalizeTenantID(tenantID) + "\x00" + string(normalizeViewerKind(viewerKind)) + "\x00" + strings.TrimSpace(viewerID)))
	return scheduledRunID + ":" + hex.EncodeToString(digest[:])
}

func cleanupRunReadReceiptsTx(ctx context.Context, tx db.Tx, table, scheduledRunID string) error {
	scheduledRunID = strings.TrimSpace(scheduledRunID)
	if scheduledRunID == "" {
		return ErrInvalidRequest
	}
	prefix := scheduledRunID + ":"
	rows, err := tx.QueryContext(ctx, `SELECT record_key FROM platform_records WHERE table_name = ? AND record_key >= ? AND record_key < ? ORDER BY record_key`, table, prefix, scheduledRunID+";")
	if err != nil {
		return fmt.Errorf("list scheduled run read receipts for retention: %w", err)
	}
	keys := make([]string, 0)
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			_ = rows.Close()
			return fmt.Errorf("scan scheduled run read receipt for retention: %w", err)
		}
		keys = append(keys, key)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close scheduled run read receipt rows: %w", err)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate scheduled run read receipt rows: %w", err)
	}
	for _, key := range keys {
		if err := db.LockRecordKeyTx(ctx, tx, table, key); err != nil {
			return err
		}
		if err := db.DeleteRecordTx(ctx, tx, table, key); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) ListRunInbox(ctx context.Context, viewer ViewerRef, query ListRunInboxQuery) (ScheduledRunInboxPage, error) {
	if s == nil || s.repo == nil {
		return ScheduledRunInboxPage{}, ErrServiceDependency
	}
	tenantID := normalizeTenantID(viewer.TenantID)
	viewerID := strings.TrimSpace(viewer.ViewerID)
	viewerKind := normalizeViewerKind(viewer.ViewerKind)
	query.ScheduledTaskID = strings.TrimSpace(query.ScheduledTaskID)
	query.ScheduledRunID = strings.TrimSpace(query.ScheduledRunID)
	if viewerID == "" || (query.Status != "" && !isRunStatus(query.Status)) || !validRunInboxReadState(query.ReadState) || query.Offset < 0 || query.Limit > 100 {
		return ScheduledRunInboxPage{}, ErrInvalidRequest
	}
	if query.Limit <= 0 {
		query.Limit = 30
	}
	runs, err := s.repo.ListRunsForTenant(ctx, tenantID)
	if err != nil {
		return ScheduledRunInboxPage{}, err
	}
	receipts, err := s.repo.ListRunReadReceipts(ctx, tenantID, viewerID, viewerKind)
	if err != nil {
		return ScheduledRunInboxPage{}, err
	}
	tasks, err := s.repo.ListTasks(ctx, true)
	if err != nil {
		return ScheduledRunInboxPage{}, err
	}
	taskIndex := make(map[string]ScheduledTask, len(tasks))
	for _, task := range tasks {
		if normalizeTenantID(task.TenantID) == tenantID {
			taskIndex[task.ScheduledTaskID] = task
		}
	}

	items := make([]ScheduledRunInboxItem, 0, len(runs))
	seenTasks := make(map[string]bool)
	unreadTotal, attentionTotal := 0, 0
	for index := len(runs) - 1; index >= 0; index-- {
		run := runs[index]
		if !isTerminalRunStatus(run.Status) {
			continue
		}
		if !scheduledRunVisibleInInbox(run, taskIndex[run.ScheduledTaskID]) {
			continue
		}
		// Select the latest visible terminal outcome before applying state filters.
		// An older failure must not reappear after a successful run.
		if query.GroupByTask {
			if seenTasks[run.ScheduledTaskID] {
				continue
			}
			seenTasks[run.ScheduledTaskID] = true
		}
		receipt, isRead := receipts[run.ScheduledRunID]
		attention := scheduledRunNeedsAttention(run)
		if !isRead {
			unreadTotal++
		}
		if attention {
			attentionTotal++
		}
		if query.ScheduledTaskID != "" && run.ScheduledTaskID != query.ScheduledTaskID {
			continue
		}
		if query.ScheduledRunID != "" && run.ScheduledRunID != query.ScheduledRunID {
			continue
		}
		if query.Status != "" && run.Status != query.Status {
			continue
		}
		if query.AttentionOnly && !attention {
			continue
		}
		if query.ReadState == RunInboxReadUnread && isRead {
			continue
		}
		if query.ReadState == RunInboxReadRead && !isRead {
			continue
		}
		title := strings.TrimSpace(run.ScheduledTaskTitle)
		if task, ok := taskIndex[run.ScheduledTaskID]; ok && strings.TrimSpace(task.Title) != "" {
			title = strings.TrimSpace(task.Title)
		}
		if title == "" {
			title = run.ScheduledTaskID
		}
		if query.GroupByTask {
			run = ScheduledRun{
				ScheduledRunID: run.ScheduledRunID, ScheduledTaskID: run.ScheduledTaskID,
				Status: run.Status, TargetMode: run.TargetMode, TriggerType: run.TriggerType,
				CreatedAt: run.CreatedAt, CompletedAt: run.CompletedAt,
				FailureReason: run.FailureReason, DeliveryStatus: run.DeliveryStatus,
				Result: RunResult{Summary: run.Result.Summary},
			}
		}
		items = append(items, ScheduledRunInboxItem{
			Run: run, TaskTitle: title, IsRead: isRead, ReadAt: receipt.ReadAt, AttentionRequired: attention,
		})
	}
	total := len(items)
	offset := query.Offset
	if offset > total {
		offset = total
	}
	limit := query.Limit
	if limit > total-offset {
		limit = total - offset
	}
	pageItems := append([]ScheduledRunInboxItem(nil), items[offset:offset+limit]...)
	for index := range pageItems {
		pageItems[index].Run.Result.Artifacts = append([]RunArtifact(nil), pageItems[index].Run.Result.Artifacts...)
	}
	if !query.GroupByTask {
		s.annotateInboxArtifactAvailability(ctx, taskIndex, pageItems)
	}
	return ScheduledRunInboxPage{
		Items: pageItems, Total: total, UnreadTotal: unreadTotal, AttentionTotal: attentionTotal,
		Limit: query.Limit, Offset: query.Offset,
	}, nil
}

func (s *Service) SetRunReadState(ctx context.Context, viewer ViewerRef, request SetRunReadStateRequest) (ScheduledRunReadState, error) {
	if s == nil || s.repo == nil || s.clock == nil {
		return ScheduledRunReadState{}, ErrServiceDependency
	}
	tenantID := normalizeTenantID(viewer.TenantID)
	viewerID := strings.TrimSpace(viewer.ViewerID)
	viewerKind := normalizeViewerKind(viewer.ViewerKind)
	runID := strings.TrimSpace(request.ScheduledRunID)
	if viewerID == "" || runID == "" {
		return ScheduledRunReadState{}, ErrInvalidRequest
	}
	run, err := s.repo.GetRun(ctx, runID)
	if err != nil {
		return ScheduledRunReadState{}, err
	}
	if normalizeTenantID(run.TenantID) != tenantID {
		return ScheduledRunReadState{}, ErrNotFound
	}
	if !isTerminalRunStatus(run.Status) {
		return ScheduledRunReadState{}, ErrInvalidRequest
	}
	task, taskErr := s.repo.GetTask(ctx, run.ScheduledTaskID)
	if taskErr != nil && !errors.Is(taskErr, ErrNotFound) {
		return ScheduledRunReadState{}, taskErr
	}
	if !scheduledRunVisibleInInbox(run, task) {
		return ScheduledRunReadState{}, ErrNotFound
	}
	state := ScheduledRunReadState{ScheduledRunID: runID, IsRead: request.Read}
	receipt := scheduledRunReadReceipt{TenantID: tenantID, ViewerID: viewerID, ViewerKind: viewerKind, ScheduledRunID: runID}
	if request.Read {
		state.ReadAt = s.clock.Now().UTC()
		receipt.ReadAt = state.ReadAt
		receipt.UpdatedAt = state.ReadAt
	}
	if err := s.repo.SetRunReadState(ctx, receipt, request.Read); err != nil {
		return ScheduledRunReadState{}, err
	}
	return state, nil
}

func validRunInboxReadState(state RunInboxReadState) bool {
	return state == "" || state == RunInboxReadAll || state == RunInboxReadUnread || state == RunInboxReadRead
}

func isTerminalRunStatus(status RunStatus) bool {
	return status == RunStatusCompleted || status == RunStatusFailed || status == RunStatusSkipped
}

func scheduledRunVisibleInInbox(run ScheduledRun, task ScheduledTask) bool {
	mode := run.TargetMode
	if mode == "" && run.ConfigurationSnapshot != nil {
		mode = run.ConfigurationSnapshot.Target.Mode
	}
	if mode == "" {
		mode = task.Target.Mode
	}
	return !isSystemManagedTargetMode(mode)
}

func scheduledRunNeedsAttention(run ScheduledRun) bool {
	if run.Status == RunStatusFailed {
		return true
	}
	switch strings.TrimSpace(run.DeliveryStatus) {
	case "failed", "dead_letter", "not_recovered":
		return true
	default:
		return false
	}
}

func (s *Service) annotateInboxArtifactAvailability(ctx context.Context, tasks map[string]ScheduledTask, items []ScheduledRunInboxItem) {
	checker, ok := s.artifactStore.(ArtifactAvailabilityChecker)
	if !ok {
		return
	}
	for itemIndex := range items {
		for artifactIndex := range items[itemIndex].Run.Result.Artifacts {
			items[itemIndex].Run.Result.Artifacts[artifactIndex].ContentAvailable = false
			items[itemIndex].Run.Result.Artifacts[artifactIndex].ContentAvailabilityChecked = false
		}
	}
	checked := 0
	for itemIndex := range items {
		task, found := tasks[items[itemIndex].Run.ScheduledTaskID]
		if !found {
			continue
		}
		for artifactIndex := range items[itemIndex].Run.Result.Artifacts {
			if ctx.Err() != nil || checked >= maxScheduledArtifactAvailabilityChecks {
				return
			}
			checked++
			artifact := items[itemIndex].Run.Result.Artifacts[artifactIndex]
			items[itemIndex].Run.Result.Artifacts[artifactIndex].ContentAvailable = checker.ArtifactAvailable(ctx, RunArtifactAccess{
				Task: task, Run: items[itemIndex].Run, Artifact: artifact,
			})
			items[itemIndex].Run.Result.Artifacts[artifactIndex].ContentAvailabilityChecked = true
		}
	}
}
