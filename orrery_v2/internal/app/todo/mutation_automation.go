package todo

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"orrery_v2/internal/platform/db"
)

const todoMutationInvalidationReason = "Todo changed after the execution plan was prepared"

type todoMutationResult struct {
	Automation   *TodoAutomationState
	PauseRequest TodoMissionPauseRequest
}

// UpdateTodoAndInvalidateAutomation commits the Todo version change together
// with approval invalidation and a durable Mission pause request.
func (r *Repository) UpdateTodoAndInvalidateAutomation(ctx context.Context, item TodoItem, expectedVersion int64, now time.Time) (todoMutationResult, error) {
	if r == nil || r.router == nil {
		return todoMutationResult{}, ErrServiceDependency
	}
	item = normalizeTodoItem(item)
	if expectedVersion <= 0 || item.Version <= 1 {
		return todoMutationResult{}, ErrInvalidRequest
	}
	if now.IsZero() {
		now = time.Now().UTC()
	} else {
		now = now.UTC()
	}
	todoRecordValue, err := todoRecord(item)
	if err != nil {
		return todoMutationResult{}, err
	}
	store, todoTableName, err := r.router.StoreForTable(todoTable)
	if err != nil {
		return todoMutationResult{}, err
	}
	automationStore, automationTableName, err := r.router.StoreForTable(todoAutomationTable)
	if err != nil {
		return todoMutationResult{}, err
	}
	bindingStore, bindingTableName, err := r.router.StoreForTable(todoMissionBindingTable)
	if err != nil {
		return todoMutationResult{}, err
	}
	attemptStore, attemptTableName, err := r.router.StoreForTable(todoExecutionAttemptTable)
	if err != nil {
		return todoMutationResult{}, err
	}
	if store != automationStore || store != bindingStore || store != attemptStore {
		return todoMutationResult{}, ErrServiceDependency
	}

	key := scopedRecordKey(item.TenantID, item.TodoID)
	result := todoMutationResult{}
	err = store.WithTx(ctx, func(tx db.Tx) error {
		if err := db.LockRecordKeyTx(ctx, tx, todoTableName, key); err != nil {
			return err
		}
		if err := db.LockRecordKeyTx(ctx, tx, automationTableName, key); err != nil {
			return err
		}
		if err := db.LockRecordKeyTx(ctx, tx, bindingTableName, key); err != nil {
			return err
		}
		currentRecord, found, err := db.GetRecordTx(ctx, tx, todoTableName, key)
		if err != nil {
			return err
		}
		if !found {
			return ErrNotFound
		}
		current, err := todoFromRecord(currentRecord, item.TenantID, item.TodoID)
		if err != nil {
			return err
		}
		if current.Version != expectedVersion {
			return ErrConflict
		}
		if item.Version != current.Version+1 {
			return ErrInvalidRequest
		}
		if err := db.PutRecordTx(ctx, tx, todoTableName, key, todoRecordValue); err != nil {
			return err
		}

		var automation TodoAutomationState
		automationFound := false
		if automationRecord, found, err := db.GetRecordTx(ctx, tx, automationTableName, key); err != nil {
			return err
		} else if found {
			if err := decodePayload(automationRecord, &automation); err != nil {
				return err
			}
			hydrateTodoAutomationInternal(automationRecord, &automation)
			automation = normalizeTodoAutomationState(automation)
			automationFound = true
			if automation.TodoVersion == current.Version && todoAutomationInvalidatedByMutation(automation.Status) {
				automation.Status = TodoAutomationStatusStale
				automation.ApprovedPlanDigest = ""
				automation.FailureReason = todoMutationInvalidationReason
				automation.NextCheckAt = time.Time{}
				automation.UpdatedAt = now
				next, err := todoAutomationRecord(automation)
				if err != nil {
					return err
				}
				if err := db.PutRecordTx(ctx, tx, automationTableName, key, next); err != nil {
					return err
				}
				copy := automation
				result.Automation = &copy
			}
		}

		bindingRecord, found, err := db.GetRecordTx(ctx, tx, bindingTableName, key)
		if err != nil {
			return err
		}
		if !found {
			return nil
		}
		var binding TodoMissionBinding
		if err := decodePayload(bindingRecord, &binding); err != nil {
			return err
		}
		hydrateTodoMissionBindingInternal(bindingRecord, &binding)
		binding = normalizeTodoMissionBinding(binding)
		if binding.TodoVersion != current.Version || todoMissionBindingTerminal(binding.Status) {
			return nil
		}
		if !automationFound || automation.ProposalID != binding.ProposalID || automation.TodoVersion != binding.TodoVersion || automation.PlanDigest != binding.PlanDigest {
			return fmt.Errorf("%w: Todo automation and active Mission binding identity differ", ErrConflict)
		}
		binding.PauseReason = todoMutationInvalidationReason
		binding.Status = TodoMissionBindingStatusPaused
		if binding.MissionID != "" {
			binding.Status = TodoMissionBindingStatusPauseRequested
		}
		binding.UpdatedAt = now
		binding.Version++
		binding.leaseToken = ""
		binding.leaseExpiresAt = time.Time{}
		nextBinding, err := todoMissionBindingRecord(binding)
		if err != nil {
			return err
		}
		if err := db.PutRecordTx(ctx, tx, bindingTableName, key, nextBinding); err != nil {
			return err
		}

		attemptKey := todoExecutionAttemptKey(binding.TenantID, binding.TodoID, binding.ExecutionAttempt)
		if err := db.LockRecordKeyTx(ctx, tx, attemptTableName, attemptKey); err != nil {
			return err
		}
		attemptRecord, found, err := db.GetRecordTx(ctx, tx, attemptTableName, attemptKey)
		if err != nil {
			return err
		}
		if !found {
			return fmt.Errorf("%w: active Todo execution attempt is missing", ErrServiceDependency)
		}
		var attempt TodoExecutionAttempt
		if err := decodePayload(attemptRecord, &attempt); err != nil {
			return err
		}
		attempt = normalizeTodoExecutionAttempt(attempt)
		if attempt.MissionID != binding.MissionID || attempt.ProposalID != binding.ProposalID || attempt.TodoVersion != binding.TodoVersion || attempt.PlanDigest != binding.PlanDigest {
			return fmt.Errorf("%w: Todo execution attempt and Mission binding identity differ", ErrConflict)
		}
		attempt.Status = binding.Status
		attempt.FailureReason = binding.PauseReason
		attempt.UpdatedAt = now
		if binding.Status == TodoMissionBindingStatusPaused {
			attempt.CompletedAt = now
		}
		nextAttempt, err := todoExecutionAttemptRecord(attempt)
		if err != nil {
			return err
		}
		if err := db.PutRecordTx(ctx, tx, attemptTableName, attemptKey, nextAttempt); err != nil {
			return err
		}
		if binding.Status == TodoMissionBindingStatusPauseRequested {
			result.PauseRequest = todoMissionPauseRequest(binding)
		}
		return nil
	})
	return result, err
}

func (r *Repository) CompleteTodoMissionPause(ctx context.Context, request TodoMissionPauseRequest, completedAt time.Time) error {
	if r == nil || r.router == nil {
		return ErrServiceDependency
	}
	request = normalizeTodoMissionPauseRequest(request)
	if request.TodoID == "" || request.BindingID == "" || request.MissionID == "" || request.ExecutionAttempt <= 0 {
		return ErrInvalidRequest
	}
	if completedAt.IsZero() {
		completedAt = time.Now().UTC()
	} else {
		completedAt = completedAt.UTC()
	}
	store, bindingTableName, err := r.router.StoreForTable(todoMissionBindingTable)
	if err != nil {
		return err
	}
	attemptStore, attemptTableName, err := r.router.StoreForTable(todoExecutionAttemptTable)
	if err != nil {
		return err
	}
	if store != attemptStore {
		return ErrServiceDependency
	}
	key := scopedRecordKey(request.TenantID, request.TodoID)
	return store.WithTx(ctx, func(tx db.Tx) error {
		if err := db.LockRecordKeyTx(ctx, tx, bindingTableName, key); err != nil {
			return err
		}
		record, found, err := db.GetRecordTx(ctx, tx, bindingTableName, key)
		if err != nil {
			return err
		}
		if !found {
			return ErrNotFound
		}
		var binding TodoMissionBinding
		if err := decodePayload(record, &binding); err != nil {
			return err
		}
		hydrateTodoMissionBindingInternal(record, &binding)
		binding = normalizeTodoMissionBinding(binding)
		if binding.BindingID != request.BindingID || binding.MissionID != request.MissionID || binding.ExecutionAttempt != request.ExecutionAttempt {
			return ErrConflict
		}
		if binding.Status == TodoMissionBindingStatusPaused {
			return nil
		}
		if binding.Status != TodoMissionBindingStatusPauseRequested {
			return ErrConflict
		}
		binding.Status = TodoMissionBindingStatusPaused
		binding.UpdatedAt = completedAt
		binding.Version++
		nextBinding, err := todoMissionBindingRecord(binding)
		if err != nil {
			return err
		}
		if err := db.PutRecordTx(ctx, tx, bindingTableName, key, nextBinding); err != nil {
			return err
		}

		attemptKey := todoExecutionAttemptKey(binding.TenantID, binding.TodoID, binding.ExecutionAttempt)
		if err := db.LockRecordKeyTx(ctx, tx, attemptTableName, attemptKey); err != nil {
			return err
		}
		attemptRecord, found, err := db.GetRecordTx(ctx, tx, attemptTableName, attemptKey)
		if err != nil {
			return err
		}
		if !found {
			return ErrNotFound
		}
		var attempt TodoExecutionAttempt
		if err := decodePayload(attemptRecord, &attempt); err != nil {
			return err
		}
		attempt = normalizeTodoExecutionAttempt(attempt)
		if attempt.MissionID != binding.MissionID || attempt.ExecutionAttempt != binding.ExecutionAttempt {
			return ErrConflict
		}
		attempt.Status = TodoMissionBindingStatusPaused
		attempt.FailureReason = binding.PauseReason
		attempt.CompletedAt = completedAt
		attempt.UpdatedAt = completedAt
		nextAttempt, err := todoExecutionAttemptRecord(attempt)
		if err != nil {
			return err
		}
		return db.PutRecordTx(ctx, tx, attemptTableName, attemptKey, nextAttempt)
	})
}

func (r *Repository) ListTodoMissionPauseRequests(ctx context.Context, limit int) ([]TodoMissionPauseRequest, error) {
	if r == nil || r.router == nil {
		return nil, ErrServiceDependency
	}
	if limit <= 0 {
		return []TodoMissionPauseRequest{}, nil
	}
	store, table, err := r.router.StoreForTable(todoMissionBindingTable)
	if err != nil {
		return nil, err
	}
	records, err := store.ListRecordTable(ctx, table)
	if err != nil {
		return nil, err
	}
	out := make([]TodoMissionPauseRequest, 0, limit)
	for _, record := range records {
		if recordString(record, "status") != TodoMissionBindingStatusPauseRequested {
			continue
		}
		var binding TodoMissionBinding
		if err := decodePayload(record, &binding); err != nil {
			return nil, err
		}
		binding = normalizeTodoMissionBinding(binding)
		if binding.Status != TodoMissionBindingStatusPauseRequested || binding.MissionID == "" {
			return nil, fmt.Errorf("%w: Todo Mission pause request is inconsistent", ErrServiceDependency)
		}
		out = append(out, todoMissionPauseRequest(binding))
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].TenantID == out[j].TenantID {
			return out[i].TodoID < out[j].TodoID
		}
		return out[i].TenantID < out[j].TenantID
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func todoAutomationInvalidatedByMutation(status TodoAutomationStatus) bool {
	switch status {
	case TodoAutomationStatusRejected, TodoAutomationStatusStale, TodoAutomationStatusExecuted:
		return false
	default:
		return true
	}
}

func todoMissionPauseRequest(binding TodoMissionBinding) TodoMissionPauseRequest {
	request := TodoMissionPauseRequest{
		TenantID: binding.TenantID, TodoID: binding.TodoID, BindingID: binding.BindingID,
		MissionID: binding.MissionID, ExecutionAttempt: binding.ExecutionAttempt, Reason: binding.PauseReason,
	}
	request.IdempotencyKey = "todo-mission-pause:" + binding.BindingID
	return normalizeTodoMissionPauseRequest(request)
}

func normalizeTodoMissionPauseRequest(request TodoMissionPauseRequest) TodoMissionPauseRequest {
	request.TenantID = normalizedTenantID(request.TenantID)
	request.TodoID = strings.TrimSpace(request.TodoID)
	request.BindingID = strings.TrimSpace(request.BindingID)
	request.MissionID = strings.TrimSpace(request.MissionID)
	request.Reason = strings.TrimSpace(request.Reason)
	request.IdempotencyKey = strings.TrimSpace(request.IdempotencyKey)
	return request
}
