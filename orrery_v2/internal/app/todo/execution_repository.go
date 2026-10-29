package todo

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"orrery_v2/internal/platform/db"
)

const todoMissionExecutionLease = 15 * time.Minute

func (r *Repository) BeginTodoMissionExecution(ctx context.Context, item TodoItem, state TodoAutomationState, createdBy, claimToken string, now time.Time) (TodoMissionBinding, bool, error) {
	if r == nil || r.router == nil {
		return TodoMissionBinding{}, false, ErrServiceDependency
	}
	item = normalizeTodoItem(item)
	state = normalizeTodoAutomationState(state)
	createdBy = strings.TrimSpace(createdBy)
	claimToken = strings.TrimSpace(claimToken)
	if item.TodoID == "" || item.Version <= 0 || state.TodoID != item.TodoID || state.TodoVersion != item.Version || state.ProposalID == "" || state.PlanDigest == "" || state.ApprovedPlanDigest != state.PlanDigest || createdBy == "" || claimToken == "" {
		return TodoMissionBinding{}, false, ErrInvalidRequest
	}
	store, bindingTable, err := r.router.StoreForTable(todoMissionBindingTable)
	if err != nil {
		return TodoMissionBinding{}, false, err
	}
	automationStore, automationTable, err := r.router.StoreForTable(todoAutomationTable)
	if err != nil {
		return TodoMissionBinding{}, false, err
	}
	todoStore, todoTableName, err := r.router.StoreForTable(todoTable)
	if err != nil {
		return TodoMissionBinding{}, false, err
	}
	attemptStore, attemptTable, err := r.router.StoreForTable(todoExecutionAttemptTable)
	if err != nil {
		return TodoMissionBinding{}, false, err
	}
	if store != automationStore || store != todoStore || store != attemptStore {
		return TodoMissionBinding{}, false, ErrServiceDependency
	}
	key := scopedRecordKey(item.TenantID, item.TodoID)
	created := false
	binding := TodoMissionBinding{}
	err = store.WithTx(ctx, func(tx db.Tx) error {
		if err := db.LockRecordKeyTx(ctx, tx, todoTableName, key); err != nil {
			return err
		}
		if err := db.LockRecordKeyTx(ctx, tx, automationTable, key); err != nil {
			return err
		}
		if err := db.LockRecordKeyTx(ctx, tx, bindingTable, key); err != nil {
			return err
		}
		todoRecord, ok, err := db.GetRecordTx(ctx, tx, todoTableName, key)
		if err != nil {
			return err
		}
		if !ok {
			return ErrNotFound
		}
		var currentItem TodoItem
		if err := decodePayload(todoRecord, &currentItem); err != nil {
			return err
		}
		currentItem = normalizeTodoItem(currentItem)
		if currentItem.Version != item.Version || !todoEligibleForAutomation(currentItem) {
			return ErrConflict
		}
		automationRecord, ok, err := db.GetRecordTx(ctx, tx, automationTable, key)
		if err != nil {
			return err
		}
		if !ok {
			return ErrNotFound
		}
		var currentAutomation TodoAutomationState
		if err := decodePayload(automationRecord, &currentAutomation); err != nil {
			return err
		}
		currentAutomation = normalizeTodoAutomationState(currentAutomation)
		if currentAutomation.Status != TodoAutomationStatusExecuting || currentAutomation.ProposalID != state.ProposalID || currentAutomation.TodoVersion != state.TodoVersion || currentAutomation.PlanDigest != state.PlanDigest || currentAutomation.ApprovedPlanDigest != state.PlanDigest {
			return ErrConflict
		}

		attempt := 1
		if currentRecord, ok, err := db.GetRecordTx(ctx, tx, bindingTable, key); err != nil {
			return err
		} else if ok {
			var current TodoMissionBinding
			if err := decodePayload(currentRecord, &current); err != nil {
				return err
			}
			hydrateTodoMissionBindingInternal(currentRecord, &current)
			current = normalizeTodoMissionBinding(current)
			if current.ProposalID == state.ProposalID && current.TodoVersion == state.TodoVersion && current.PlanDigest == state.PlanDigest {
				if current.Status == TodoMissionBindingStatusExecuted || (!current.leaseExpiresAt.IsZero() && now.UTC().Before(current.leaseExpiresAt)) {
					binding = current
					return nil
				}
				current.leaseToken = claimToken
				current.leaseExpiresAt = now.UTC().Add(todoMissionExecutionLease)
				current.UpdatedAt = now.UTC()
				current.Version++
				next, err := todoMissionBindingRecord(current)
				if err != nil {
					return err
				}
				if err := db.PutRecordTx(ctx, tx, bindingTable, key, next); err != nil {
					return err
				}
				binding = current
				created = true
				return nil
			}
			if !todoMissionBindingTerminal(current.Status) {
				return ErrConflict
			}
			attempt = current.ExecutionAttempt + 1
		}

		binding = normalizeTodoMissionBinding(TodoMissionBinding{
			TenantID: item.TenantID, BindingID: todoMissionBindingID(item.TenantID, item.TodoID, attempt),
			TodoID: item.TodoID, ConversationID: state.ConversationID, DevelopmentProjectID: item.DevelopmentProjectID,
			ProposalID: state.ProposalID, TodoVersion: state.TodoVersion, ExecutionAttempt: attempt,
			PlanDigest: state.PlanDigest, Status: TodoMissionBindingStatusCreating, CreatedBy: createdBy,
			CreatedAt: now.UTC(), UpdatedAt: now.UTC(), Version: 1,
		})
		binding.leaseToken = claimToken
		binding.leaseExpiresAt = now.UTC().Add(todoMissionExecutionLease)
		bindingRecord, err := todoMissionBindingRecord(binding)
		if err != nil {
			return err
		}
		if err := db.PutRecordTx(ctx, tx, bindingTable, key, bindingRecord); err != nil {
			return err
		}
		attemptRecord := TodoExecutionAttempt{
			TenantID: binding.TenantID, TodoID: binding.TodoID, ExecutionAttempt: binding.ExecutionAttempt,
			DevelopmentProjectID: binding.DevelopmentProjectID,
			ProposalID:           binding.ProposalID, TodoVersion: binding.TodoVersion, PlanDigest: binding.PlanDigest,
			Status: binding.Status, StartedAt: binding.CreatedAt, UpdatedAt: binding.UpdatedAt,
		}
		record, err := todoExecutionAttemptRecord(attemptRecord)
		if err != nil {
			return err
		}
		attemptKey := todoExecutionAttemptKey(binding.TenantID, binding.TodoID, binding.ExecutionAttempt)
		if err := db.LockRecordKeyTx(ctx, tx, attemptTable, attemptKey); err != nil {
			return err
		}
		if err := db.PutRecordTx(ctx, tx, attemptTable, attemptKey, record); err != nil {
			return err
		}
		created = true
		return nil
	})
	return binding, created, err
}

func (r *Repository) SaveTodoDiscussionBinding(ctx context.Context, binding TodoDiscussionBinding) (TodoDiscussionBinding, error) {
	if r == nil || r.router == nil {
		return TodoDiscussionBinding{}, ErrServiceDependency
	}
	binding = normalizeTodoDiscussionBinding(binding)
	if binding.TodoID == "" || binding.ConversationID == "" || binding.ManagedSource == "" {
		return TodoDiscussionBinding{}, ErrInvalidRequest
	}
	key := scopedRecordKey(binding.TenantID, binding.TodoID)
	store, table, err := r.router.StoreForTable(todoDiscussionBindingTable)
	if err != nil {
		return TodoDiscussionBinding{}, err
	}
	var saved TodoDiscussionBinding
	err = store.WithTx(ctx, func(tx db.Tx) error {
		if err := db.LockRecordKeyTx(ctx, tx, table, key); err != nil {
			return err
		}
		currentRecord, ok, err := db.GetRecordTx(ctx, tx, table, key)
		if err != nil {
			return err
		}
		if ok {
			var current TodoDiscussionBinding
			if err := decodePayload(currentRecord, &current); err != nil {
				return err
			}
			current = normalizeTodoDiscussionBinding(current)
			if current.ConversationID != binding.ConversationID || current.ManagedSource != binding.ManagedSource {
				return ErrConflict
			}
			binding.CreatedAt = current.CreatedAt
			binding.Version = current.Version + 1
		} else {
			binding.Version = 1
		}
		record, err := todoDiscussionBindingRecord(binding)
		if err != nil {
			return err
		}
		if err := db.PutRecordTx(ctx, tx, table, key, record); err != nil {
			return err
		}
		saved = binding
		return nil
	})
	return saved, err
}

func (r *Repository) GetTodoDiscussionBinding(ctx context.Context, tenantID, todoID string) (TodoDiscussionBinding, error) {
	if r == nil || r.router == nil {
		return TodoDiscussionBinding{}, ErrServiceDependency
	}
	record, ok, err := r.router.GetRecord(ctx, todoDiscussionBindingTable, scopedRecordKey(tenantID, todoID))
	if err != nil {
		return TodoDiscussionBinding{}, err
	}
	if !ok {
		return TodoDiscussionBinding{}, ErrNotFound
	}
	var binding TodoDiscussionBinding
	if err := decodePayload(record, &binding); err != nil {
		return TodoDiscussionBinding{}, err
	}
	binding = normalizeTodoDiscussionBinding(binding)
	if binding.TenantID != normalizedTenantID(tenantID) || binding.TodoID != strings.TrimSpace(todoID) {
		return TodoDiscussionBinding{}, fmt.Errorf("%w: Todo discussion binding identity is inconsistent", ErrServiceDependency)
	}
	return binding, nil
}

func (r *Repository) SaveTodoMissionBinding(ctx context.Context, binding TodoMissionBinding) error {
	if r == nil || r.router == nil {
		return ErrServiceDependency
	}
	binding = normalizeTodoMissionBinding(binding)
	if binding.BindingID == "" || binding.TodoID == "" || binding.ConversationID == "" || binding.ProposalID == "" || binding.TodoVersion <= 0 || binding.ExecutionAttempt <= 0 || binding.PlanDigest == "" || binding.Status == "" || binding.CreatedBy == "" {
		return ErrInvalidRequest
	}
	if binding.Status != TodoMissionBindingStatusCreating && binding.Status != TodoMissionBindingStatusFailed && binding.MissionID == "" {
		return ErrInvalidRequest
	}
	store, table, err := r.router.StoreForTable(todoMissionBindingTable)
	if err != nil {
		return err
	}
	attemptStore, attemptTable, err := r.router.StoreForTable(todoExecutionAttemptTable)
	if err != nil {
		return err
	}
	ownerStore, ownerTable, err := r.router.StoreForTable(todoMissionOwnerTable)
	if err != nil {
		return err
	}
	if store != attemptStore || store != ownerStore {
		return ErrServiceDependency
	}
	key := scopedRecordKey(binding.TenantID, binding.TodoID)
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
		var current TodoMissionBinding
		if err := decodePayload(currentRecord, &current); err != nil {
			return err
		}
		hydrateTodoMissionBindingInternal(currentRecord, &current)
		current = normalizeTodoMissionBinding(current)
		if current.BindingID != binding.BindingID || current.ExecutionAttempt != binding.ExecutionAttempt || current.ProposalID != binding.ProposalID || current.TodoVersion != binding.TodoVersion || current.PlanDigest != binding.PlanDigest || current.ConversationID != binding.ConversationID {
			return ErrConflict
		}
		if current.leaseToken != binding.leaseToken {
			return ErrConflict
		}
		if current.MissionID != "" && binding.MissionID != current.MissionID {
			return ErrConflict
		}
		if !todoMissionBindingTransitionAllowed(current.Status, binding.Status) {
			return ErrConflict
		}
		binding.CreatedAt = current.CreatedAt
		binding.CreatedBy = current.CreatedBy
		binding.Version = current.Version + 1
		if binding.Status == TodoMissionBindingStatusExecuted || binding.Status == TodoMissionBindingStatusFailed || binding.Status == TodoMissionBindingStatusWaiting || binding.Status == TodoMissionBindingStatusProvisioning || binding.Status == TodoMissionBindingStatusPauseRequested || binding.Status == TodoMissionBindingStatusPaused {
			binding.leaseToken = ""
			binding.leaseExpiresAt = time.Time{}
		}
		if binding.MissionID != "" {
			ownerKey := scopedRecordKey(binding.TenantID, binding.MissionID)
			if err := db.LockRecordKeyTx(ctx, tx, ownerTable, ownerKey); err != nil {
				return err
			}
			if owner, ok, err := db.GetRecordTx(ctx, tx, ownerTable, ownerKey); err != nil {
				return err
			} else if ok && (recordString(owner, "todo_id") != binding.TodoID || recordString(owner, "binding_id") != binding.BindingID) {
				return ErrConflict
			}
			if err := db.PutRecordTx(ctx, tx, ownerTable, ownerKey, db.Record{
				"tenant_id": binding.TenantID, "mission_id": binding.MissionID, "todo_id": binding.TodoID, "binding_id": binding.BindingID,
				"execution_attempt": binding.ExecutionAttempt,
			}); err != nil {
				return err
			}
		}
		next, err := todoMissionBindingRecord(binding)
		if err != nil {
			return err
		}
		if err := db.PutRecordTx(ctx, tx, table, key, next); err != nil {
			return err
		}
		attempt := TodoExecutionAttempt{
			TenantID: binding.TenantID, TodoID: binding.TodoID, ExecutionAttempt: binding.ExecutionAttempt,
			DevelopmentProjectID: binding.DevelopmentProjectID,
			MissionID:            binding.MissionID, MissionTaskIDs: binding.MissionTaskIDs, ProposalID: binding.ProposalID,
			WorkspaceID: binding.WorkspaceID, ExecutionRunIDs: append([]string(nil), binding.ExecutionRunIDs...),
			TodoVersion: binding.TodoVersion, PlanDigest: binding.PlanDigest, Status: binding.Status, FailureReason: binding.FailureReason,
			StartedAt: binding.CreatedAt, UpdatedAt: binding.UpdatedAt,
		}
		if todoMissionBindingTerminal(binding.Status) {
			attempt.CompletedAt = binding.UpdatedAt
		}
		attemptRecord, err := todoExecutionAttemptRecord(attempt)
		if err != nil {
			return err
		}
		attemptKey := todoExecutionAttemptKey(binding.TenantID, binding.TodoID, binding.ExecutionAttempt)
		if err := db.LockRecordKeyTx(ctx, tx, attemptTable, attemptKey); err != nil {
			return err
		}
		return db.PutRecordTx(ctx, tx, attemptTable, attemptKey, attemptRecord)
	})
}

func (r *Repository) GetTodoMissionBinding(ctx context.Context, tenantID, todoID string) (TodoMissionBinding, error) {
	if r == nil || r.router == nil {
		return TodoMissionBinding{}, ErrServiceDependency
	}
	record, ok, err := r.router.GetRecord(ctx, todoMissionBindingTable, scopedRecordKey(tenantID, todoID))
	if err != nil {
		return TodoMissionBinding{}, err
	}
	if !ok {
		return TodoMissionBinding{}, ErrNotFound
	}
	var binding TodoMissionBinding
	if err := decodePayload(record, &binding); err != nil {
		return TodoMissionBinding{}, err
	}
	hydrateTodoMissionBindingInternal(record, &binding)
	binding = normalizeTodoMissionBinding(binding)
	if binding.TenantID != normalizedTenantID(tenantID) || binding.TodoID != strings.TrimSpace(todoID) {
		return TodoMissionBinding{}, fmt.Errorf("%w: Todo Mission binding identity is inconsistent", ErrServiceDependency)
	}
	return binding, nil
}

func (r *Repository) SaveTodoExecutionAttempt(ctx context.Context, attempt TodoExecutionAttempt) error {
	if r == nil || r.router == nil {
		return ErrServiceDependency
	}
	attempt = normalizeTodoExecutionAttempt(attempt)
	if attempt.TodoID == "" || attempt.ExecutionAttempt <= 0 || attempt.ProposalID == "" || attempt.TodoVersion <= 0 || attempt.PlanDigest == "" || attempt.Status == "" {
		return ErrInvalidRequest
	}
	record, err := todoExecutionAttemptRecord(attempt)
	if err != nil {
		return err
	}
	return r.router.PutRecord(ctx, todoExecutionAttemptTable, todoExecutionAttemptKey(attempt.TenantID, attempt.TodoID, attempt.ExecutionAttempt), record)
}

func (r *Repository) ListTodoExecutionAttempts(ctx context.Context, tenantID, todoID string) ([]TodoExecutionAttempt, error) {
	records, err := snapshotForTenant(ctx, r.router, todoExecutionAttemptTable, tenantID)
	if err != nil {
		return nil, err
	}
	out := make([]TodoExecutionAttempt, 0)
	for _, record := range records {
		if recordString(record, "todo_id") != strings.TrimSpace(todoID) {
			continue
		}
		var attempt TodoExecutionAttempt
		if err := decodePayload(record, &attempt); err != nil {
			return nil, err
		}
		out = append(out, normalizeTodoExecutionAttempt(attempt))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ExecutionAttempt > out[j].ExecutionAttempt })
	return out, nil
}

func (r *Repository) attachTodoExecution(ctx context.Context, item TodoItem) (TodoItem, error) {
	if binding, err := r.GetTodoDiscussionBinding(ctx, item.TenantID, item.TodoID); err == nil {
		item.DiscussionBinding = &binding
	} else if !errors.Is(err, ErrNotFound) {
		return TodoItem{}, err
	}
	summary := TodoExecutionSummary{Attempts: []TodoExecutionAttempt{}}
	if binding, err := r.GetTodoMissionBinding(ctx, item.TenantID, item.TodoID); err == nil {
		summary.Current = &binding
	} else if !errors.Is(err, ErrNotFound) {
		return TodoItem{}, err
	}
	attempts, err := r.ListTodoExecutionAttempts(ctx, item.TenantID, item.TodoID)
	if err != nil {
		return TodoItem{}, err
	}
	summary.Attempts = attempts
	if summary.Current != nil || len(summary.Attempts) > 0 {
		item.Execution = &summary
	}
	return item, nil
}

func todoDiscussionBindingRecord(binding TodoDiscussionBinding) (db.Record, error) {
	payload, err := encodePayload(binding)
	if err != nil {
		return nil, err
	}
	return db.Record{"tenant_id": binding.TenantID, "todo_id": binding.TodoID, "conversation_id": binding.ConversationID, payloadField: payload}, nil
}

func todoMissionBindingRecord(binding TodoMissionBinding) (db.Record, error) {
	payload, err := encodePayload(binding)
	if err != nil {
		return nil, err
	}
	return db.Record{
		"tenant_id": binding.TenantID, "binding_id": binding.BindingID, "todo_id": binding.TodoID,
		"mission_id": binding.MissionID, "execution_attempt": binding.ExecutionAttempt,
		"development_project_id": binding.DevelopmentProjectID, "status": binding.Status,
		"lease_token": binding.leaseToken, "lease_expires_at": formatTime(binding.leaseExpiresAt), payloadField: payload,
	}, nil
}

func todoExecutionAttemptRecord(attempt TodoExecutionAttempt) (db.Record, error) {
	payload, err := encodePayload(attempt)
	if err != nil {
		return nil, err
	}
	return db.Record{
		"tenant_id": attempt.TenantID, "todo_id": attempt.TodoID, "mission_id": attempt.MissionID,
		"execution_attempt": attempt.ExecutionAttempt, "development_project_id": attempt.DevelopmentProjectID, "status": attempt.Status,
		payloadField: payload,
	}, nil
}

func todoExecutionAttemptKey(tenantID, todoID string, attempt int) string {
	return fmt.Sprintf("%s::%s::%09d", normalizedTenantID(tenantID), strings.TrimSpace(todoID), attempt)
}

func normalizeTodoDiscussionBinding(binding TodoDiscussionBinding) TodoDiscussionBinding {
	binding.TenantID = normalizedTenantID(binding.TenantID)
	binding.TodoID = strings.TrimSpace(binding.TodoID)
	binding.ConversationID = strings.TrimSpace(binding.ConversationID)
	binding.ManagedSource = strings.TrimSpace(binding.ManagedSource)
	return binding
}

func normalizeTodoMissionBinding(binding TodoMissionBinding) TodoMissionBinding {
	binding.TenantID = normalizedTenantID(binding.TenantID)
	binding.BindingID = strings.TrimSpace(binding.BindingID)
	binding.TodoID = strings.TrimSpace(binding.TodoID)
	binding.MissionID = strings.TrimSpace(binding.MissionID)
	binding.ConversationID = strings.TrimSpace(binding.ConversationID)
	binding.DevelopmentProjectID = strings.TrimSpace(binding.DevelopmentProjectID)
	binding.ProposalID = strings.TrimSpace(binding.ProposalID)
	binding.PlanDigest = strings.TrimSpace(binding.PlanDigest)
	binding.Status = strings.TrimSpace(binding.Status)
	binding.PauseReason = strings.TrimSpace(binding.PauseReason)
	binding.FailureReason = strings.TrimSpace(binding.FailureReason)
	binding.CreatedBy = strings.TrimSpace(binding.CreatedBy)
	binding.MissionTaskIDs = normalizeTags(binding.MissionTaskIDs)
	binding.WorkspaceID = strings.TrimSpace(binding.WorkspaceID)
	binding.ExecutionRunIDs = normalizeTags(binding.ExecutionRunIDs)
	binding.IdempotencyKey = todoMissionExecutionIdempotencyKey(binding)
	return binding
}

func hydrateTodoMissionBindingInternal(record db.Record, binding *TodoMissionBinding) {
	if binding == nil {
		return
	}
	binding.leaseToken = recordString(record, "lease_token")
	if value := recordString(record, "lease_expires_at"); value != "" {
		binding.leaseExpiresAt, _ = time.Parse(timeFormat, value)
	}
}

func normalizeTodoExecutionAttempt(attempt TodoExecutionAttempt) TodoExecutionAttempt {
	attempt.TenantID = normalizedTenantID(attempt.TenantID)
	attempt.TodoID = strings.TrimSpace(attempt.TodoID)
	attempt.MissionID = strings.TrimSpace(attempt.MissionID)
	attempt.DevelopmentProjectID = strings.TrimSpace(attempt.DevelopmentProjectID)
	attempt.ProposalID = strings.TrimSpace(attempt.ProposalID)
	attempt.PlanDigest = strings.TrimSpace(attempt.PlanDigest)
	attempt.Status = strings.TrimSpace(attempt.Status)
	attempt.FailureReason = strings.TrimSpace(attempt.FailureReason)
	attempt.MissionTaskIDs = normalizeTags(attempt.MissionTaskIDs)
	attempt.WorkspaceID = strings.TrimSpace(attempt.WorkspaceID)
	attempt.ExecutionRunIDs = normalizeTags(attempt.ExecutionRunIDs)
	return attempt
}

func todoMissionBindingID(tenantID, todoID string, attempt int) string {
	digest := sha256.Sum256([]byte(normalizedTenantID(tenantID) + "\x00" + strings.TrimSpace(todoID) + "\x00" + fmt.Sprint(attempt)))
	return "todo-binding-" + hex.EncodeToString(digest[:12])
}

func todoMissionExecutionIdempotencyKey(binding TodoMissionBinding) string {
	if strings.TrimSpace(binding.BindingID) == "" || strings.TrimSpace(binding.ProposalID) == "" || strings.TrimSpace(binding.PlanDigest) == "" {
		return ""
	}
	return "todo-mission:" + strings.TrimSpace(binding.BindingID) + ":" + strings.TrimSpace(binding.ProposalID) + ":" + strings.TrimSpace(binding.PlanDigest)
}

func todoMissionBindingTerminal(status string) bool {
	switch strings.TrimSpace(status) {
	case TodoMissionBindingStatusExecuted, TodoMissionBindingStatusFailed, TodoMissionBindingStatusPaused:
		return true
	default:
		return false
	}
}

func todoMissionBindingTransitionAllowed(from, to string) bool {
	from = strings.TrimSpace(from)
	to = strings.TrimSpace(to)
	if from == to {
		return true
	}
	switch from {
	case TodoMissionBindingStatusCreating:
		return to == TodoMissionBindingStatusMaterialized || to == TodoMissionBindingStatusFailed || to == TodoMissionBindingStatusPauseRequested || to == TodoMissionBindingStatusPaused
	case TodoMissionBindingStatusMaterialized:
		return to == TodoMissionBindingStatusProvisioning || to == TodoMissionBindingStatusDispatching || to == TodoMissionBindingStatusFailed || to == TodoMissionBindingStatusPauseRequested || to == TodoMissionBindingStatusPaused
	case TodoMissionBindingStatusProvisioning:
		return to == TodoMissionBindingStatusDispatching || to == TodoMissionBindingStatusFailed || to == TodoMissionBindingStatusPauseRequested || to == TodoMissionBindingStatusPaused
	case TodoMissionBindingStatusDispatching:
		return to == TodoMissionBindingStatusExecuting || to == TodoMissionBindingStatusWaiting || to == TodoMissionBindingStatusExecuted || to == TodoMissionBindingStatusFailed || to == TodoMissionBindingStatusPauseRequested || to == TodoMissionBindingStatusPaused
	case TodoMissionBindingStatusExecuting:
		return to == TodoMissionBindingStatusWaiting || to == TodoMissionBindingStatusExecuted || to == TodoMissionBindingStatusFailed || to == TodoMissionBindingStatusPauseRequested || to == TodoMissionBindingStatusPaused
	case TodoMissionBindingStatusWaiting:
		return to == TodoMissionBindingStatusExecuting || to == TodoMissionBindingStatusExecuted || to == TodoMissionBindingStatusFailed || to == TodoMissionBindingStatusPauseRequested || to == TodoMissionBindingStatusPaused
	case TodoMissionBindingStatusPauseRequested:
		return to == TodoMissionBindingStatusPaused
	case TodoMissionBindingStatusFailed:
		return to == TodoMissionBindingStatusMaterialized || to == TodoMissionBindingStatusProvisioning || to == TodoMissionBindingStatusDispatching || to == TodoMissionBindingStatusExecuting || to == TodoMissionBindingStatusWaiting || to == TodoMissionBindingStatusExecuted
	case TodoMissionBindingStatusExecuted, TodoMissionBindingStatusPaused:
		return false
	default:
		return false
	}
}
