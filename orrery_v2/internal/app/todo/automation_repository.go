package todo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"orrery_v2/internal/app/operations"
	"orrery_v2/internal/platform/db"
)

const (
	todoAutomationDiscussionMembersFingerprintField = "discussion_members_fingerprint"
	todoAutomationPendingAuditsField                = "pending_audit_events"
	maxTodoAutomationPendingAudits                  = 256
)

type todoAutomationAuditIntent struct {
	TenantID   string
	TodoID     string
	ProposalID string
	Event      operations.AuditEventDTO
}

func (r *Repository) ClaimTodoAutomation(ctx context.Context, item TodoItem, state TodoAutomationState, now time.Time) (bool, error) {
	if r == nil || r.router == nil {
		return false, ErrServiceDependency
	}
	item = normalizeTodoItem(item)
	state = normalizeTodoAutomationState(state)
	if item.TodoID == "" || state.ProposalID == "" || state.TodoID != item.TodoID || state.TodoVersion != item.Version {
		return false, ErrInvalidRequest
	}
	store, table, err := r.router.StoreForTable(todoAutomationTable)
	if err != nil {
		return false, err
	}
	todoStore, todoTableName, err := r.router.StoreForTable(todoTable)
	if err != nil {
		return false, err
	}
	if store != todoStore {
		return false, ErrServiceDependency
	}
	key := scopedRecordKey(item.TenantID, item.TodoID)
	claimed := false
	err = store.WithTx(ctx, func(tx db.Tx) error {
		if err := db.LockRecordKeyTx(ctx, tx, todoTableName, key); err != nil {
			return err
		}
		if err := db.LockRecordKeyTx(ctx, tx, table, key); err != nil {
			return err
		}
		itemRecord, ok, err := db.GetRecordTx(ctx, tx, todoTableName, key)
		if err != nil {
			return err
		}
		if !ok {
			return ErrNotFound
		}
		var currentItem TodoItem
		if err := decodePayload(itemRecord, &currentItem); err != nil {
			return err
		}
		currentItem = normalizeTodoItem(currentItem)
		if currentItem.Version != item.Version || !todoEligibleForAutomation(currentItem) {
			return nil
		}
		currentRecord, ok, err := db.GetRecordTx(ctx, tx, table, key)
		if err != nil {
			return err
		}
		if ok {
			var current TodoAutomationState
			if err := decodePayload(currentRecord, &current); err != nil {
				return err
			}
			hydrateTodoAutomationInternal(currentRecord, &current)
			current = normalizeTodoAutomationState(current)
			if current.TodoVersion == item.Version && !todoAutomationCanRetry(current, now) {
				return nil
			}
			state.pendingAuditEvents = mergeTodoAutomationAuditEvents(current.pendingAuditEvents, state.pendingAuditEvents)
		}
		record, err := todoAutomationRecord(state)
		if err != nil {
			return err
		}
		if err := db.PutRecordTx(ctx, tx, table, key, record); err != nil {
			return err
		}
		claimed = true
		return nil
	})
	return claimed, err
}

func (r *Repository) SaveTodoAutomation(ctx context.Context, state TodoAutomationState) error {
	if r == nil || r.router == nil {
		return ErrServiceDependency
	}
	state = normalizeTodoAutomationState(state)
	if state.TodoID == "" || state.ProposalID == "" || state.TodoVersion <= 0 {
		return ErrInvalidRequest
	}
	store, table, err := r.router.StoreForTable(todoAutomationTable)
	if err != nil {
		return err
	}
	planStore, planTable, err := r.router.StoreForTable(todoExecutionPlanTable)
	if err != nil {
		return err
	}
	if store != planStore {
		return ErrServiceDependency
	}
	key := scopedRecordKey(state.TenantID, state.TodoID)
	return store.WithTx(ctx, func(tx db.Tx) error {
		if err := db.LockRecordKeyTx(ctx, tx, table, key); err != nil {
			return err
		}
		if currentRecord, ok, err := db.GetRecordTx(ctx, tx, table, key); err != nil {
			return err
		} else if ok {
			var current TodoAutomationState
			if err := decodePayload(currentRecord, &current); err != nil {
				return err
			}
			hydrateTodoAutomationInternal(currentRecord, &current)
			state.pendingAuditEvents = mergeTodoAutomationAuditEvents(current.pendingAuditEvents, state.pendingAuditEvents)
			if state.discussionMembersFingerprint == "" {
				state.discussionMembersFingerprint = current.discussionMembersFingerprint
			}
		}
		record, err := todoAutomationRecord(state)
		if err != nil {
			return err
		}
		if err := putTodoExecutionPlanTx(ctx, tx, planTable, state); err != nil {
			return err
		}
		return db.PutRecordTx(ctx, tx, table, key, record)
	})
}

func (r *Repository) CompareAndSaveTodoAutomation(ctx context.Context, state TodoAutomationState, expectedStatus TodoAutomationStatus) (bool, TodoAutomationState, error) {
	if r == nil || r.router == nil {
		return false, TodoAutomationState{}, ErrServiceDependency
	}
	state = normalizeTodoAutomationState(state)
	store, table, err := r.router.StoreForTable(todoAutomationTable)
	if err != nil {
		return false, TodoAutomationState{}, err
	}
	todoStore, todoTableName, err := r.router.StoreForTable(todoTable)
	if err != nil {
		return false, TodoAutomationState{}, err
	}
	planStore, planTable, err := r.router.StoreForTable(todoExecutionPlanTable)
	if err != nil {
		return false, TodoAutomationState{}, err
	}
	if store != todoStore || store != planStore {
		return false, TodoAutomationState{}, ErrServiceDependency
	}
	key := scopedRecordKey(state.TenantID, state.TodoID)
	updated := false
	current := TodoAutomationState{}
	err = store.WithTx(ctx, func(tx db.Tx) error {
		if err := db.LockRecordKeyTx(ctx, tx, todoTableName, key); err != nil {
			return err
		}
		if err := db.LockRecordKeyTx(ctx, tx, table, key); err != nil {
			return err
		}
		todoRecord, ok, err := db.GetRecordTx(ctx, tx, todoTableName, scopedRecordKey(state.TenantID, state.TodoID))
		if err != nil {
			return err
		}
		if !ok {
			return ErrNotFound
		}
		var item TodoItem
		if err := decodePayload(todoRecord, &item); err != nil {
			return err
		}
		item = normalizeTodoItem(item)
		if state.Status != TodoAutomationStatusStale && (item.Version != state.TodoVersion || !todoEligibleForAutomation(item)) {
			return ErrConflict
		}
		record, ok, err := db.GetRecordTx(ctx, tx, table, key)
		if err != nil {
			return err
		}
		if !ok {
			return ErrNotFound
		}
		if err := decodePayload(record, &current); err != nil {
			return err
		}
		hydrateTodoAutomationInternal(record, &current)
		current = normalizeTodoAutomationState(current)
		if current.ProposalID != state.ProposalID {
			return ErrConflict
		}
		if current.Status != expectedStatus {
			return nil
		}
		state.pendingAuditEvents = mergeTodoAutomationAuditEvents(current.pendingAuditEvents, state.pendingAuditEvents)
		if state.discussionMembersFingerprint == "" {
			state.discussionMembersFingerprint = current.discussionMembersFingerprint
		}
		next, err := todoAutomationRecord(state)
		if err != nil {
			return err
		}
		if err := putTodoExecutionPlanTx(ctx, tx, planTable, state); err != nil {
			return err
		}
		if err := db.PutRecordTx(ctx, tx, table, key, next); err != nil {
			return err
		}
		current = state
		updated = true
		return nil
	})
	return updated, current, err
}

// LinkTodoAutomationHILDecision adds the durable HIL identity without
// changing the Todo approval status. It is intentionally separate from the
// status CAS so an old approve/reject request cannot overwrite the link.
func (r *Repository) LinkTodoAutomationHILDecision(ctx context.Context, state TodoAutomationState) (TodoAutomationState, error) {
	if r == nil || r.router == nil {
		return TodoAutomationState{}, ErrServiceDependency
	}
	state = normalizeTodoAutomationState(state)
	if state.TenantID == "" || state.TodoID == "" || state.ProposalID == "" || state.HILDecisionID == "" {
		return TodoAutomationState{}, ErrInvalidRequest
	}
	store, table, err := r.router.StoreForTable(todoAutomationTable)
	if err != nil {
		return TodoAutomationState{}, err
	}
	todoStore, todoTableName, err := r.router.StoreForTable(todoTable)
	if err != nil {
		return TodoAutomationState{}, err
	}
	if store != todoStore {
		return TodoAutomationState{}, ErrServiceDependency
	}
	key := scopedRecordKey(state.TenantID, state.TodoID)
	var linked TodoAutomationState
	err = store.WithTx(ctx, func(tx db.Tx) error {
		if err := db.LockRecordKeyTx(ctx, tx, todoTableName, key); err != nil {
			return err
		}
		if err := db.LockRecordKeyTx(ctx, tx, table, key); err != nil {
			return err
		}
		record, ok, err := db.GetRecordTx(ctx, tx, table, key)
		if err != nil {
			return err
		}
		if !ok {
			return ErrNotFound
		}
		if err := decodePayload(record, &linked); err != nil {
			return err
		}
		hydrateTodoAutomationInternal(record, &linked)
		linked = normalizeTodoAutomationState(linked)
		if linked.ProposalID != state.ProposalID {
			return ErrConflict
		}
		if linked.Status != TodoAutomationStatusPendingApproval {
			// A legacy approval may have won the race while HIL was down. Keep
			// the old terminal state authoritative and leave the HIL record
			// discoverable only through its deterministic idempotency key.
			return nil
		}
		if linked.HILDecisionID != "" {
			if linked.HILDecisionID != state.HILDecisionID {
				return ErrConflict
			}
			return nil
		}
		linked.HILDecisionID = state.HILDecisionID
		if !state.UpdatedAt.IsZero() {
			linked.UpdatedAt = state.UpdatedAt
		}
		next, err := todoAutomationRecord(linked)
		if err != nil {
			return err
		}
		return db.PutRecordTx(ctx, tx, table, key, next)
	})
	return linked, err
}

func (r *Repository) GetTodoAutomation(ctx context.Context, tenantID, todoID string) (TodoAutomationState, error) {
	if r == nil || r.router == nil {
		return TodoAutomationState{}, ErrServiceDependency
	}
	record, ok, err := r.router.GetRecord(ctx, todoAutomationTable, scopedRecordKey(tenantID, todoID))
	if err != nil {
		return TodoAutomationState{}, err
	}
	if !ok {
		return TodoAutomationState{}, ErrNotFound
	}
	var state TodoAutomationState
	if err := decodePayload(record, &state); err != nil {
		return TodoAutomationState{}, err
	}
	hydrateTodoAutomationInternal(record, &state)
	return normalizeTodoAutomationState(state), nil
}

func (r *Repository) ListTodoAutomationsForTenant(ctx context.Context, tenantID string) ([]TodoAutomationState, error) {
	records, err := snapshotForTenant(ctx, r.router, todoAutomationTable, tenantID)
	if err != nil {
		return nil, err
	}
	out := make([]TodoAutomationState, 0, len(records))
	for _, record := range records {
		var state TodoAutomationState
		if err := decodePayload(record, &state); err != nil {
			return nil, err
		}
		hydrateTodoAutomationInternal(record, &state)
		out = append(out, normalizeTodoAutomationState(state))
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].ProposalID < out[j].ProposalID
		}
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	return out, nil
}

func (r *Repository) enqueueTodoAutomationAudit(ctx context.Context, state TodoAutomationState, event operations.AuditEventDTO) error {
	if r == nil || r.router == nil {
		return ErrServiceDependency
	}
	state = normalizeTodoAutomationState(state)
	if state.TodoID == "" || state.ProposalID == "" || strings.TrimSpace(event.AuditID) == "" ||
		normalizedTenantID(event.TenantID) != state.TenantID || strings.TrimSpace(event.EntityID) != state.ProposalID {
		return ErrInvalidRequest
	}
	store, table, err := r.router.StoreForTable(todoAutomationTable)
	if err != nil {
		return err
	}
	key := scopedRecordKey(state.TenantID, state.TodoID)
	return store.WithTx(ctx, func(tx db.Tx) error {
		if err := db.LockRecordKeyTx(ctx, tx, table, key); err != nil {
			return err
		}
		record, ok, err := db.GetRecordTx(ctx, tx, table, key)
		if err != nil {
			return err
		}
		if !ok {
			return ErrNotFound
		}
		var current TodoAutomationState
		if err := decodePayload(record, &current); err != nil {
			return err
		}
		hydrateTodoAutomationInternal(record, &current)
		current = normalizeTodoAutomationState(current)
		if current.ProposalID != state.ProposalID {
			return ErrConflict
		}
		for _, pending := range current.pendingAuditEvents {
			if pending.AuditID == event.AuditID {
				return nil
			}
		}
		if len(current.pendingAuditEvents) >= maxTodoAutomationPendingAudits {
			return fmt.Errorf("%w: Todo automation pending audit limit exceeded", ErrConflict)
		}
		current.pendingAuditEvents = append(current.pendingAuditEvents, cloneTodoAutomationAuditEvent(event))
		next, err := todoAutomationRecord(current)
		if err != nil {
			return err
		}
		return db.PutRecordTx(ctx, tx, table, key, next)
	})
}

func (r *Repository) listTodoAutomationAuditIntents(ctx context.Context, limit int) ([]todoAutomationAuditIntent, error) {
	if r == nil || r.router == nil {
		return nil, ErrServiceDependency
	}
	if limit <= 0 || limit > maxTodoAutomationPendingAudits {
		limit = 100
	}
	store, table, err := r.router.StoreForTable(todoAutomationTable)
	if err != nil {
		return nil, err
	}
	records, err := store.ListRecordTable(ctx, table)
	if err != nil {
		return nil, err
	}
	keys := make([]string, 0, len(records))
	for key := range records {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]todoAutomationAuditIntent, 0, limit)
	for _, key := range keys {
		record := records[key]
		var state TodoAutomationState
		if err := decodePayload(record, &state); err != nil {
			return nil, err
		}
		hydrateTodoAutomationInternal(record, &state)
		state = normalizeTodoAutomationState(state)
		for _, event := range state.pendingAuditEvents {
			out = append(out, todoAutomationAuditIntent{TenantID: state.TenantID, TodoID: state.TodoID, ProposalID: event.EntityID, Event: cloneTodoAutomationAuditEvent(event)})
			if len(out) >= limit {
				return out, nil
			}
		}
	}
	return out, nil
}

func (r *Repository) clearTodoAutomationAuditIntent(ctx context.Context, intent todoAutomationAuditIntent) error {
	if r == nil || r.router == nil {
		return ErrServiceDependency
	}
	store, table, err := r.router.StoreForTable(todoAutomationTable)
	if err != nil {
		return err
	}
	key := scopedRecordKey(intent.TenantID, intent.TodoID)
	return store.WithTx(ctx, func(tx db.Tx) error {
		if err := db.LockRecordKeyTx(ctx, tx, table, key); err != nil {
			return err
		}
		record, ok, err := db.GetRecordTx(ctx, tx, table, key)
		if err != nil {
			return err
		}
		if !ok {
			return nil
		}
		var current TodoAutomationState
		if err := decodePayload(record, &current); err != nil {
			return err
		}
		hydrateTodoAutomationInternal(record, &current)
		current = normalizeTodoAutomationState(current)
		index := -1
		for i := range current.pendingAuditEvents {
			if current.pendingAuditEvents[i].AuditID == intent.Event.AuditID {
				index = i
				break
			}
		}
		if index < 0 {
			return nil
		}
		current.pendingAuditEvents = append(current.pendingAuditEvents[:index:index], current.pendingAuditEvents[index+1:]...)
		next, err := todoAutomationRecord(current)
		if err != nil {
			return err
		}
		return db.PutRecordTx(ctx, tx, table, key, next)
	})
}

func (r *Repository) ListTodoAutomationDiscussionSyncCandidates(ctx context.Context, agentIDs []string, executionSupportIDs []string, limit int) ([]TodoItem, error) {
	if r == nil || r.router == nil {
		return nil, ErrServiceDependency
	}
	store, table, err := r.router.StoreForTable(todoAutomationTable)
	if err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	rows, err := store.Query(ctx, `
SELECT payload_json
FROM platform_records
WHERE table_name = ?
ORDER BY record_key ASC`, table)
	if err != nil {
		return nil, fmt.Errorf("query todo discussion member sync candidates: %w", err)
	}
	states := make([]TodoAutomationState, 0)
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, fmt.Errorf("scan todo discussion member sync candidate: %w", err)
		}
		var record db.Record
		if err := json.Unmarshal([]byte(raw), &record); err != nil {
			return nil, fmt.Errorf("decode todo discussion member sync candidate: %w", err)
		}
		var state TodoAutomationState
		if err := decodePayload(record, &state); err != nil {
			return nil, err
		}
		hydrateTodoAutomationInternal(record, &state)
		state = normalizeTodoAutomationState(state)
		if state.ConversationID == "" {
			continue
		}
		states = append(states, state)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, fmt.Errorf("iterate todo discussion member sync candidates: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close todo discussion member sync candidates: %w", err)
	}
	out := make([]TodoItem, 0, limit)
	for _, state := range states {
		item, err := r.GetTodoForTenant(ctx, state.TenantID, state.TodoID)
		if err != nil {
			if err == ErrNotFound {
				continue
			}
			return nil, err
		}
		stateCopy := state
		item.Automation = &stateCopy
		if state.discussionMembersFingerprint == todoAutomationDiscussionMembersFingerprint(item, state, agentIDs, executionSupportIDs) {
			continue
		}
		out = append(out, item)
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

func (r *Repository) MarkTodoAutomationDiscussionMembersSynced(ctx context.Context, tenantID, todoID, proposalID, fingerprint string) error {
	if r == nil || r.router == nil {
		return ErrServiceDependency
	}
	store, table, err := r.router.StoreForTable(todoAutomationTable)
	if err != nil {
		return err
	}
	key := scopedRecordKey(tenantID, todoID)
	return store.WithTx(ctx, func(tx db.Tx) error {
		if err := db.LockRecordKeyTx(ctx, tx, table, key); err != nil {
			return err
		}
		record, ok, err := db.GetRecordTx(ctx, tx, table, key)
		if err != nil {
			return err
		}
		if !ok {
			return ErrNotFound
		}
		var current TodoAutomationState
		if err := decodePayload(record, &current); err != nil {
			return err
		}
		hydrateTodoAutomationInternal(record, &current)
		current = normalizeTodoAutomationState(current)
		if current.ProposalID != strings.TrimSpace(proposalID) {
			return nil
		}
		current.discussionMembersFingerprint = strings.TrimSpace(fingerprint)
		next, err := todoAutomationRecord(current)
		if err != nil {
			return err
		}
		return db.PutRecordTx(ctx, tx, table, key, next)
	})
}

func (r *Repository) ListTodoAutomationCandidates(ctx context.Context, now time.Time, limit int) ([]TodoItem, error) {
	if r == nil || r.router == nil {
		return nil, ErrServiceDependency
	}
	store, table, err := r.router.StoreForTable(todoTable)
	if err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	rows, err := store.Query(ctx, `
SELECT payload_json
FROM platform_records
WHERE table_name = ?
ORDER BY record_key ASC`, table)
	if err != nil {
		return nil, fmt.Errorf("query todo automation candidates: %w", err)
	}
	items := make([]TodoItem, 0)
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, fmt.Errorf("scan todo automation candidate: %w", err)
		}
		var record db.Record
		if err := json.Unmarshal([]byte(raw), &record); err != nil {
			return nil, fmt.Errorf("decode todo automation candidate record: %w", err)
		}
		item, err := todoFromRecord(record, "", "")
		if err != nil {
			return nil, err
		}
		if todoEligibleForAutomation(item) {
			items = append(items, item)
		}
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, fmt.Errorf("iterate todo automation candidates: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close todo automation candidates: %w", err)
	}
	eligibleByTenant := map[string][]TodoItem{}
	tenantIDs := make([]string, 0)
	for _, item := range items {
		state, stateErr := r.GetTodoAutomation(ctx, item.TenantID, item.TodoID)
		if stateErr == nil {
			stateCopy := state
			item.Automation = &stateCopy
		}
		if stateErr != nil && stateErr != ErrNotFound {
			return nil, stateErr
		}
		if !todoAutomationPlanningCandidate(item, now) {
			continue
		}
		tenantID := normalizedTenantID(item.TenantID)
		if _, exists := eligibleByTenant[tenantID]; !exists {
			tenantIDs = append(tenantIDs, tenantID)
		}
		eligibleByTenant[tenantID] = append(eligibleByTenant[tenantID], item)
	}
	sort.Strings(tenantIDs)
	out := make([]TodoItem, 0, limit)
	for round := 0; len(out) < limit; round++ {
		added := false
		for _, tenantID := range tenantIDs {
			items := eligibleByTenant[tenantID]
			if round >= len(items) {
				continue
			}
			out = append(out, items[round])
			added = true
			if len(out) >= limit {
				break
			}
		}
		if !added {
			break
		}
	}
	return out, nil
}

// ListTodoAutomationHILCandidates returns pending Todo approvals that still
// need their durable HIL decision link. It deliberately reads the automation
// rows first and only joins Todo records after closing the result set so this
// remains compatible with SQLite's single-connection test/development setup.
func (r *Repository) ListTodoAutomationHILCandidates(ctx context.Context, limit int) ([]TodoItem, error) {
	return r.listTodoAutomationPendingHILCandidates(ctx, limit, false)
}

// ListTodoAutomationHILResolutionCandidates returns pending Todo approvals
// that already hold a durable HIL decision ID. Callers use it to replay a
// terminal decision when a post-commit observer was unavailable.
func (r *Repository) ListTodoAutomationHILResolutionCandidates(ctx context.Context, limit int) ([]TodoItem, error) {
	return r.listTodoAutomationPendingHILCandidates(ctx, limit, true)
}

func (r *Repository) listTodoAutomationPendingHILCandidates(ctx context.Context, limit int, requireLinkedDecision bool) ([]TodoItem, error) {
	if r == nil || r.router == nil {
		return nil, ErrServiceDependency
	}
	store, table, err := r.router.StoreForTable(todoAutomationTable)
	if err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	rows, err := store.Query(ctx, `
SELECT payload_json
FROM platform_records
WHERE table_name = ?
ORDER BY record_key ASC`, table)
	if err != nil {
		return nil, fmt.Errorf("query Todo HIL candidates: %w", err)
	}
	states := make([]TodoAutomationState, 0, limit)
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("scan Todo HIL candidate: %w", err)
		}
		var record db.Record
		if err := json.Unmarshal([]byte(raw), &record); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("decode Todo HIL candidate: %w", err)
		}
		var state TodoAutomationState
		if err := decodePayload(record, &state); err != nil {
			_ = rows.Close()
			return nil, err
		}
		hydrateTodoAutomationInternal(record, &state)
		state = normalizeTodoAutomationState(state)
		if state.Status != TodoAutomationStatusPendingApproval || state.ApprovalID == "" || state.PlanDigest == "" ||
			(requireLinkedDecision && state.HILDecisionID == "") || (!requireLinkedDecision && state.HILDecisionID != "") {
			continue
		}
		states = append(states, state)
		if len(states) >= limit {
			break
		}
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, fmt.Errorf("iterate Todo HIL candidates: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close Todo HIL candidates: %w", err)
	}
	out := make([]TodoItem, 0, len(states))
	for _, state := range states {
		item, err := r.GetTodoForTenant(ctx, state.TenantID, state.TodoID)
		if err != nil {
			if errors.Is(err, ErrNotFound) {
				continue
			}
			return nil, err
		}
		if item.Version != state.TodoVersion || !todoEligibleForAutomation(item) {
			continue
		}
		stateCopy := state
		item.Automation = &stateCopy
		out = append(out, item)
	}
	return out, nil
}

func (r *Repository) ListTodoAutomationExecutionRecoveryCandidates(ctx context.Context, now time.Time, limit int) ([]TodoItem, error) {
	if r == nil || r.router == nil {
		return nil, ErrServiceDependency
	}
	store, table, err := r.router.StoreForTable(todoAutomationTable)
	if err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	rows, err := store.Query(ctx, `
SELECT payload_json
FROM platform_records
WHERE table_name = ?
ORDER BY record_key ASC`, table)
	if err != nil {
		return nil, fmt.Errorf("query Todo execution recovery candidates: %w", err)
	}
	states := make([]TodoAutomationState, 0, limit)
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, fmt.Errorf("scan Todo execution recovery candidate: %w", err)
		}
		var record db.Record
		if err := json.Unmarshal([]byte(raw), &record); err != nil {
			return nil, fmt.Errorf("decode Todo execution recovery candidate: %w", err)
		}
		var state TodoAutomationState
		if err := decodePayload(record, &state); err != nil {
			return nil, err
		}
		hydrateTodoAutomationInternal(record, &state)
		state = normalizeTodoAutomationState(state)
		if state.ApprovedPlanDigest == "" || state.ApprovedPlanDigest != state.PlanDigest {
			continue
		}
		switch state.Status {
		case TodoAutomationStatusExecutionFailed:
			if state.NextCheckAt.IsZero() || now.Before(state.NextCheckAt) {
				continue
			}
		case TodoAutomationStatusExecuting:
		default:
			continue
		}
		states = append(states, state)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, fmt.Errorf("iterate Todo execution recovery candidates: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close Todo execution recovery candidates: %w", err)
	}

	// SQLite test and single-connection deployments cannot issue a nested query
	// while the automation scan still owns the only result-set connection.
	out := make([]TodoItem, 0, limit)
	for _, state := range states {
		if state.Status == TodoAutomationStatusExecuting {
			due, err := r.todoMissionOrchestrationRecoveryDue(ctx, state, now)
			if err != nil {
				return nil, err
			}
			if !due {
				continue
			}
		}
		item, err := r.GetTodoForTenant(ctx, state.TenantID, state.TodoID)
		if err != nil {
			if errors.Is(err, ErrNotFound) {
				continue
			}
			return nil, err
		}
		if item.Version != state.TodoVersion || !todoEligibleForAutomation(item) {
			continue
		}
		stateCopy := state
		item.Automation = &stateCopy
		out = append(out, item)
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

func (r *Repository) todoMissionOrchestrationRecoveryDue(ctx context.Context, state TodoAutomationState, now time.Time) (bool, error) {
	binding, err := r.GetTodoMissionBinding(ctx, state.TenantID, state.TodoID)
	if errors.Is(err, ErrNotFound) {
		return !now.Before(state.UpdatedAt.Add(todoMissionExecutionLease)), nil
	}
	if err != nil {
		return false, err
	}
	if binding.ProposalID != state.ProposalID || binding.TodoVersion != state.TodoVersion || binding.PlanDigest != state.PlanDigest {
		return false, fmt.Errorf("%w: Todo automation and Mission binding identity differ", ErrConflict)
	}
	switch binding.Status {
	case TodoMissionBindingStatusCreating, TodoMissionBindingStatusMaterialized, TodoMissionBindingStatusDispatching:
		leaseExpiresAt := binding.leaseExpiresAt
		if leaseExpiresAt.IsZero() {
			leaseExpiresAt = binding.UpdatedAt.Add(todoMissionExecutionLease)
		}
		return !now.Before(leaseExpiresAt), nil
	default:
		return false, nil
	}
}

func (r *Repository) attachTodoAutomation(ctx context.Context, item TodoItem) (TodoItem, error) {
	state, err := r.GetTodoAutomation(ctx, item.TenantID, item.TodoID)
	if err == nil {
		if state.TodoVersion != item.Version && state.Status == TodoAutomationStatusPendingApproval {
			state.Status = TodoAutomationStatusStale
		}
		item.Automation = &state
		return item, nil
	}
	if err == ErrNotFound {
		return item, nil
	}
	return TodoItem{}, err
}

func (r *Repository) attachTodoAutomations(ctx context.Context, tenantID string, items []TodoItem) ([]TodoItem, error) {
	states, err := r.ListTodoAutomationsForTenant(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	byTodo := make(map[string]TodoAutomationState, len(states))
	for _, state := range states {
		byTodo[state.TodoID] = state
	}
	for idx := range items {
		if state, ok := byTodo[items[idx].TodoID]; ok {
			copy := state
			if copy.TodoVersion != items[idx].Version && copy.Status == TodoAutomationStatusPendingApproval {
				copy.Status = TodoAutomationStatusStale
			}
			items[idx].Automation = &copy
		}
	}
	return items, nil
}

func todoAutomationRecord(state TodoAutomationState) (db.Record, error) {
	payload, err := encodePayload(state)
	if err != nil {
		return nil, err
	}
	pendingAudits, err := json.Marshal(state.pendingAuditEvents)
	if err != nil {
		return nil, fmt.Errorf("encode Todo automation pending audits: %w", err)
	}
	return db.Record{
		"tenant_id":       state.TenantID,
		"todo_id":         state.TodoID,
		"proposal_id":     state.ProposalID,
		"approval_id":     state.ApprovalID,
		"hil_decision_id": state.HILDecisionID,
		"todo_version":    state.TodoVersion,
		"status":          string(state.Status),
		"approver_id":     state.ApproverID,
		"conversation_id": state.ConversationID,
		"created_at":      formatTime(state.CreatedAt),
		"updated_at":      formatTime(state.UpdatedAt),
		todoAutomationDiscussionMembersFingerprintField: state.discussionMembersFingerprint,
		"decision_viewer_kind":                          string(state.decisionViewerKind),
		"decision_authorization_subject_id":             state.decisionAuthorizationSubjectID,
		"decision_permission_scope":                     append([]string(nil), state.decisionPermissionScope...),
		"decision_display_name":                         state.decisionDisplayName,
		"execution_requester_viewer_kind":               string(state.executionRequesterViewerKind),
		"execution_requester_authorization_subject_id":  state.executionRequesterAuthorizationSubjectID,
		"execution_requester_permission_scope":          append([]string(nil), state.executionRequesterPermissionScope...),
		"execution_requester_display_name":              state.executionRequesterDisplayName,
		todoAutomationPendingAuditsField:                string(pendingAudits),
		payloadField:                                    payload,
	}, nil
}

func normalizeTodoAutomationState(state TodoAutomationState) TodoAutomationState {
	state.TenantID = normalizedTenantID(state.TenantID)
	state.Plan.TenantID = state.TenantID
	state.ProposalID = strings.TrimSpace(state.ProposalID)
	state.ApprovalID = strings.TrimSpace(state.ApprovalID)
	state.HILDecisionID = strings.TrimSpace(state.HILDecisionID)
	state.TodoID = strings.TrimSpace(state.TodoID)
	state.ConversationID = strings.TrimSpace(state.ConversationID)
	state.RequestedBy = strings.TrimSpace(state.RequestedBy)
	state.ApproverID = strings.TrimSpace(state.ApproverID)
	state.DecisionBy = strings.TrimSpace(state.DecisionBy)
	state.ExecutionRequestedBy = strings.TrimSpace(state.ExecutionRequestedBy)
	state.discussionMembersFingerprint = strings.TrimSpace(state.discussionMembersFingerprint)
	state.decisionViewerKind = ViewerKind(strings.ToLower(strings.TrimSpace(string(state.decisionViewerKind))))
	state.decisionAuthorizationSubjectID = strings.TrimSpace(state.decisionAuthorizationSubjectID)
	state.decisionPermissionScope = normalizeTags(state.decisionPermissionScope)
	state.decisionDisplayName = strings.TrimSpace(state.decisionDisplayName)
	state.executionRequesterViewerKind = ViewerKind(strings.ToLower(strings.TrimSpace(string(state.executionRequesterViewerKind))))
	state.executionRequesterAuthorizationSubjectID = strings.TrimSpace(state.executionRequesterAuthorizationSubjectID)
	state.executionRequesterPermissionScope = normalizeTags(state.executionRequesterPermissionScope)
	state.executionRequesterDisplayName = strings.TrimSpace(state.executionRequesterDisplayName)
	state.pendingAuditEvents = normalizeTodoAutomationAuditEvents(state.pendingAuditEvents)
	return state
}

func hydrateTodoAutomationInternal(record db.Record, state *TodoAutomationState) {
	if state == nil {
		return
	}
	state.discussionMembersFingerprint = recordString(record, todoAutomationDiscussionMembersFingerprintField)
	if raw := recordString(record, todoAutomationPendingAuditsField); raw != "" {
		_ = json.Unmarshal([]byte(raw), &state.pendingAuditEvents)
	}
	state.decisionViewerKind = ViewerKind(recordString(record, "decision_viewer_kind"))
	state.decisionAuthorizationSubjectID = recordString(record, "decision_authorization_subject_id")
	state.decisionPermissionScope = recordStrings(record, "decision_permission_scope")
	state.decisionDisplayName = recordString(record, "decision_display_name")
	state.executionRequesterViewerKind = ViewerKind(recordString(record, "execution_requester_viewer_kind"))
	state.executionRequesterAuthorizationSubjectID = recordString(record, "execution_requester_authorization_subject_id")
	state.executionRequesterPermissionScope = recordStrings(record, "execution_requester_permission_scope")
	state.executionRequesterDisplayName = recordString(record, "execution_requester_display_name")
}

func normalizeTodoAutomationAuditEvents(events []operations.AuditEventDTO) []operations.AuditEventDTO {
	if len(events) == 0 {
		return nil
	}
	out := make([]operations.AuditEventDTO, 0, len(events))
	seen := make(map[string]struct{}, len(events))
	for _, event := range events {
		event.AuditID = strings.TrimSpace(event.AuditID)
		if event.AuditID == "" {
			continue
		}
		if _, ok := seen[event.AuditID]; ok {
			continue
		}
		seen[event.AuditID] = struct{}{}
		out = append(out, cloneTodoAutomationAuditEvent(event))
	}
	return out
}

func mergeTodoAutomationAuditEvents(groups ...[]operations.AuditEventDTO) []operations.AuditEventDTO {
	var merged []operations.AuditEventDTO
	for _, events := range groups {
		merged = append(merged, events...)
	}
	return normalizeTodoAutomationAuditEvents(merged)
}

func cloneTodoAutomationAuditEvent(event operations.AuditEventDTO) operations.AuditEventDTO {
	if event.Metadata != nil {
		metadata := make(map[string]string, len(event.Metadata))
		for key, value := range event.Metadata {
			metadata[key] = value
		}
		event.Metadata = metadata
	}
	return event
}

func todoAutomationCanRetry(state TodoAutomationState, now time.Time) bool {
	if state.Status != TodoAutomationStatusNotReady && state.Status != TodoAutomationStatusPlanningFailed {
		return false
	}
	return !state.NextCheckAt.IsZero() && !now.Before(state.NextCheckAt)
}

func todoAutomationPlanningCandidate(item TodoItem, now time.Time) bool {
	if !todoEligibleForAutomation(item) {
		return false
	}
	return item.Automation == nil || item.Automation.TodoVersion != item.Version || todoAutomationCanRetry(*item.Automation, now)
}
