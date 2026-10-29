package todo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"orrery_v2/internal/platform/db"
)

func (r *Repository) SaveTodo(ctx context.Context, item TodoItem) error {
	item = normalizeTodoItem(item)
	current, err := r.GetTodoForTenant(ctx, item.TenantID, item.TodoID)
	if errors.Is(err, ErrNotFound) {
		return r.CreateTodo(ctx, item)
	}
	if err != nil {
		return err
	}
	item.Version = current.Version + 1
	return r.UpdateTodo(ctx, item, current.Version)
}

func (r *Repository) CreateTodo(ctx context.Context, item TodoItem) error {
	if r == nil || r.router == nil {
		return ErrServiceDependency
	}
	item = normalizeTodoItem(item)
	store, table, err := r.router.StoreForTable(todoTable)
	if err != nil {
		return err
	}
	record, err := todoRecord(item)
	if err != nil {
		return err
	}
	return store.WithTx(ctx, func(tx db.Tx) error {
		inserted, err := db.InsertRecordTx(ctx, tx, table, scopedRecordKey(item.TenantID, item.TodoID), record)
		if err != nil {
			return err
		}
		if !inserted {
			return ErrConflict
		}
		return enqueueTodoCreationTx(ctx, tx, item, "todo.create")
	})
}

func (r *Repository) UpdateTodo(ctx context.Context, item TodoItem, expectedVersion int64) error {
	if r == nil || r.router == nil {
		return ErrServiceDependency
	}
	item = normalizeTodoItem(item)
	store, table, err := r.router.StoreForTable(todoTable)
	if err != nil {
		return err
	}
	record, err := todoRecord(item)
	if err != nil {
		return err
	}
	key := scopedRecordKey(item.TenantID, item.TodoID)
	return store.WithTx(ctx, func(tx db.Tx) error {
		if err := db.LockRecordKeyTx(ctx, tx, table, key); err != nil {
			return err
		}
		currentRecord, ok, err := db.GetRecordTx(ctx, tx, table, key)
		if err != nil {
			return err
		}
		if !ok {
			return ErrNotFound
		}
		current, err := todoFromRecord(currentRecord, item.TenantID, item.TodoID)
		if err != nil {
			return err
		}
		if expectedVersion > 0 && current.Version != expectedVersion {
			return ErrConflict
		}
		return db.PutRecordTx(ctx, tx, table, key, record)
	})
}

func (r *Repository) GetTodo(ctx context.Context, todoID string) (TodoItem, error) {
	return r.GetTodoForTenant(ctx, defaultTenantID, todoID)
}

func (r *Repository) GetTodoForTenant(ctx context.Context, tenantID, todoID string) (TodoItem, error) {
	if r == nil || r.router == nil {
		return TodoItem{}, ErrServiceDependency
	}
	todoID = strings.TrimSpace(todoID)
	if todoID == "" {
		return TodoItem{}, ErrInvalidRequest
	}
	record, ok, err := r.router.GetRecord(ctx, todoTable, scopedRecordKey(tenantID, todoID))
	if err != nil {
		return TodoItem{}, err
	}
	if !ok {
		return TodoItem{}, ErrNotFound
	}
	out, err := todoFromRecord(record, tenantID, todoID)
	if err != nil {
		return TodoItem{}, err
	}
	out, err = r.attachTodoAutomation(ctx, out)
	if err != nil {
		return TodoItem{}, err
	}
	return r.attachTodoExecution(ctx, out)
}

func (r *Repository) DeleteTodo(ctx context.Context, todoID string) error {
	return r.DeleteTodoForTenant(ctx, defaultTenantID, todoID, 0)
}

func (r *Repository) DeleteTodoForTenant(ctx context.Context, tenantID, todoID string, expectedVersion int64) error {
	if r == nil || r.router == nil {
		return ErrServiceDependency
	}
	store, table, err := r.router.StoreForTable(todoTable)
	if err != nil {
		return err
	}
	commentStore, commentTable, err := r.router.StoreForTable(issueCommentTable)
	if err != nil {
		return err
	}
	if store != commentStore {
		return ErrServiceDependency
	}
	automationStore, automationTable, err := r.router.StoreForTable(todoAutomationTable)
	if err != nil {
		return err
	}
	if store != automationStore {
		return ErrServiceDependency
	}
	key := scopedRecordKey(tenantID, todoID)
	return store.WithTx(ctx, func(tx db.Tx) error {
		if err := db.LockRecordKeyTx(ctx, tx, table, key); err != nil {
			return err
		}
		if err := db.LockRecordKeyTx(ctx, tx, automationTable, key); err != nil {
			return err
		}
		record, ok, err := db.GetRecordTx(ctx, tx, table, key)
		if err != nil {
			return err
		}
		if !ok {
			return ErrNotFound
		}
		item, err := todoFromRecord(record, tenantID, todoID)
		if err != nil {
			return err
		}
		if expectedVersion > 0 && item.Version != expectedVersion {
			return ErrConflict
		}
		if err := deleteIssueCommentsTx(ctx, tx, commentTable, normalizedTenantID(tenantID), IssueKindTodo, todoID); err != nil {
			return err
		}
		if automationRecord, found, err := db.GetRecordTx(ctx, tx, automationTable, key); err != nil {
			return err
		} else if found {
			var automation TodoAutomationState
			if err := decodePayload(automationRecord, &automation); err != nil {
				return err
			}
			hydrateTodoAutomationInternal(automationRecord, &automation)
			if len(automation.pendingAuditEvents) > 0 {
				return fmt.Errorf("%w: Todo automation audit delivery is pending", ErrConflict)
			}
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM platform_records WHERE table_name = ? AND record_key = ?`, automationTable, key); err != nil {
			return fmt.Errorf("delete todo automation record: %w", err)
		}
		return deleteRecordTx(ctx, tx, table, key)
	})
}

func todoRecord(item TodoItem) (db.Record, error) {
	item.Automation = nil
	if item.Source != nil {
		source := *item.Source
		source.CanOpen = false
		item.Source = &source
	}
	payload, err := encodePayload(item)
	if err != nil {
		return nil, err
	}
	sourceAttempt := ""
	if item.Source != nil {
		sourceAttempt = formatTime(item.Source.LastAttemptAt)
	}
	return db.Record{
		"attachment_records":         item.attachmentRecords,
		"source_notice_attempted_at": sourceAttempt,
		"source_notices":             item.sourceNotices,
		"source_notice_pending":      fmt.Sprint(len(item.sourceNotices) > 0),
		"tenant_id":                  item.TenantID,
		"organization_id":            item.OrganizationID,
		"department_id":              item.DepartmentID,
		"project_id":                 item.ProjectID,
		"work_kind":                  string(item.WorkKind),
		"automation_mode":            string(item.AutomationMode),
		"development_project_id":     item.DevelopmentProjectID,
		"source_idea_id":             item.SourceIdeaID,
		"todo_id":                    item.TodoID,
		"created_by":                 item.CreatedBy,
		"created_by_subject_id":      item.CreatedBySubjectID,
		"created_by_kind":            string(item.CreatedByKind),
		"status":                     string(item.Status),
		"priority":                   string(item.Priority),
		"assignee_id":                item.AssigneeID,
		"reviewer_id":                item.ReviewerID,
		"search_text":                todoSearchText(item),
		"due_at":                     formatTime(item.DueAt),
		"updated_at":                 formatTime(item.UpdatedAt),
		"completed_at":               formatTime(item.CompletedAt),
		"version":                    item.Version,
		payloadField:                 payload,
	}, nil
}

func (r *Repository) ListTodoCatalog(ctx context.Context, afterKey string, limit int) ([]TodoItem, bool, error) {
	if r == nil || r.router == nil {
		return nil, false, ErrServiceDependency
	}
	if limit <= 0 {
		return nil, false, nil
	}
	page, err := r.router.ListRecords(ctx, todoTable, strings.TrimSpace(afterKey), limit)
	if err != nil {
		return nil, false, err
	}
	items := make([]TodoItem, 0, len(page.Items))
	for _, entry := range page.Items {
		item, err := todoFromRecord(entry.Record, "", "")
		if err != nil {
			return nil, false, err
		}
		if scopedRecordKey(item.TenantID, item.TodoID) != entry.Key {
			return nil, false, fmt.Errorf("%w: Todo record key does not match its identity", ErrServiceDependency)
		}
		items = append(items, item)
	}
	return items, page.HasMore, nil
}

func todoFromRecord(record db.Record, expectedTenantID, expectedTodoID string) (TodoItem, error) {
	var item TodoItem
	if err := decodePayload(record, &item); err != nil {
		return TodoItem{}, err
	}
	if value, ok := record["attachment_records"]; ok {
		raw, err := json.Marshal(value)
		if err != nil {
			return TodoItem{}, err
		}
		if err := json.Unmarshal(raw, &item.attachmentRecords); err != nil {
			return TodoItem{}, err
		}
	}
	if value, ok := record["source_notices"]; ok {
		raw, err := json.Marshal(value)
		if err != nil {
			return TodoItem{}, err
		}
		if err := json.Unmarshal(raw, &item.sourceNotices); err != nil {
			return TodoItem{}, err
		}
	}
	item = normalizeTodoItem(item)
	recordTenantID := normalizedTenantID(recordString(record, "tenant_id"))
	recordTodoID := recordString(record, "todo_id")
	if recordTenantID != item.TenantID || recordTodoID != item.TodoID ||
		recordString(record, "created_by") != strings.TrimSpace(item.CreatedBy) ||
		recordString(record, "created_by_kind") != string(item.CreatedByKind) ||
		recordString(record, "status") != string(item.Status) ||
		recordString(record, "priority") != string(item.Priority) ||
		recordInt64Value(record["version"]) != item.Version ||
		(expectedTenantID != "" && item.TenantID != normalizedTenantID(expectedTenantID)) ||
		(expectedTodoID != "" && item.TodoID != strings.TrimSpace(expectedTodoID)) {
		return TodoItem{}, fmt.Errorf("%w: Todo record identity is inconsistent", ErrServiceDependency)
	}
	item.CreatedBySubjectID = recordString(record, "created_by_subject_id")
	if item.CreatedBySubjectID == "" {
		item.CreatedBySubjectID = item.CreatedBy
	}
	if !validTodoIdentity(item.TenantID) || !validTodoIdentity(item.TodoID) ||
		!validTodoIdentity(item.CreatedBy) || !validTodoIdentity(item.CreatedBySubjectID) {
		return TodoItem{}, fmt.Errorf("%w: Todo ownership identity is invalid", ErrServiceDependency)
	}
	return item, nil
}

func recordInt64Value(value any) int64 {
	switch typed := value.(type) {
	case int:
		return int64(typed)
	case int64:
		return typed
	case float64:
		return int64(typed)
	case json.Number:
		parsed, _ := typed.Int64()
		return parsed
	default:
		return 0
	}
}

func normalizeTodoItem(item TodoItem) TodoItem {
	item.TenantID = normalizedTenantID(item.TenantID)
	item.TodoID = strings.TrimSpace(item.TodoID)
	item.Status = normalizeTodoStatus(item.Status)
	if item.Status == "" {
		item.Status = TodoStatusInbox
	}
	if item.Priority == "" {
		item.Priority = TodoPriorityMedium
	}
	item.WorkKind = normalizeTodoWorkKind(item.WorkKind)
	if item.WorkKind == "" {
		item.WorkKind = TodoWorkKindGeneral
	}
	item.AutomationMode = normalizeTodoAutomationMode(item.AutomationMode)
	if item.AutomationMode == "" {
		item.AutomationMode = TodoAutomationModeManual
	}
	item.DevelopmentProjectID = strings.TrimSpace(item.DevelopmentProjectID)
	if item.Version <= 0 {
		item.Version = 1
	}
	if item.IssueNumber == "" {
		item.IssueNumber = issueNumberFromID("task", item.TodoID)
	}
	if item.CreatedByKind == "" || item.CreatedByKind == ViewerKindHuman {
		item.CreatedByKind = ViewerKindUser
	}
	if strings.TrimSpace(item.CreatedBySubjectID) == "" {
		item.CreatedBySubjectID = strings.TrimSpace(item.CreatedBy)
	}
	return item
}
