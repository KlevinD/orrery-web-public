package todo

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"time"

	"orrery_v2/internal/mission/projection"
	"orrery_v2/internal/platform/db"
)

type TodoCodeSubmissionProgress struct {
	SubmissionRevision   int64
	CodeSubmissionStatus string
	PullRequestStatus    string
	MergeStatus          string
	ProjectedAt          time.Time
}

// ProjectTodoExecutionProgress consumes an authoritative Mission projection.
// Duplicate and older Mission versions are idempotently ignored; conflicting
// content at the same version is rejected.
func (r *Repository) ProjectTodoExecutionProgress(ctx context.Context, model projection.ReadModel, projectedAt time.Time) (TodoExecutionProgressProjection, bool, error) {
	if r == nil || r.router == nil {
		return TodoExecutionProgressProjection{}, false, ErrServiceDependency
	}
	todoID := strings.TrimSpace(model.Mission.OriginTodoID)
	if todoID == "" {
		return TodoExecutionProgressProjection{}, false, nil
	}
	tenantID := normalizedTenantID(firstNonBlank(model.TenantID, model.Mission.TenantID))
	missionID := strings.TrimSpace(model.Mission.MissionID.String())
	if missionID == "" || model.Checkpoint.Version <= 0 || strings.TrimSpace(model.Mission.TenantID) == "" || normalizedTenantID(model.Mission.TenantID) != tenantID {
		return TodoExecutionProgressProjection{}, false, ErrInvalidRequest
	}
	if projectedAt.IsZero() {
		projectedAt = time.Now().UTC()
	} else {
		projectedAt = projectedAt.UTC()
	}

	store, progressTable, err := r.router.StoreForTable(todoExecutionProgressTable)
	if err != nil {
		return TodoExecutionProgressProjection{}, false, err
	}
	ownerStore, ownerTable, err := r.router.StoreForTable(todoMissionOwnerTable)
	if err != nil {
		return TodoExecutionProgressProjection{}, false, err
	}
	attemptStore, attemptTable, err := r.router.StoreForTable(todoExecutionAttemptTable)
	if err != nil {
		return TodoExecutionProgressProjection{}, false, err
	}
	todoStore, todoTableName, err := r.router.StoreForTable(todoTable)
	if err != nil {
		return TodoExecutionProgressProjection{}, false, err
	}
	bindingStore, bindingTable, err := r.router.StoreForTable(todoMissionBindingTable)
	if err != nil {
		return TodoExecutionProgressProjection{}, false, err
	}
	automationStore, automationTable, err := r.router.StoreForTable(todoAutomationTable)
	if err != nil {
		return TodoExecutionProgressProjection{}, false, err
	}
	if store != ownerStore || store != attemptStore || store != todoStore || store != bindingStore || store != automationStore {
		return TodoExecutionProgressProjection{}, false, ErrServiceDependency
	}

	var out TodoExecutionProgressProjection
	applied := false
	err = store.WithTx(ctx, func(tx db.Tx) error {
		attempt, err := todoExecutionAttemptForMissionTx(ctx, tx, ownerTable, attemptTable, tenantID, missionID)
		if err != nil {
			return err
		}
		if attempt.TodoID != todoID || attempt.MissionID != missionID {
			return fmt.Errorf("%w: Mission origin does not match its Todo execution binding", ErrConflict)
		}
		incoming := normalizeTodoExecutionProgressProjection(todoExecutionProgressFromMission(attempt, model, projectedAt))
		key := todoExecutionProgressKey(tenantID, todoID, attempt.ExecutionAttempt)
		if err := db.LockRecordKeyTx(ctx, tx, progressTable, key); err != nil {
			return err
		}
		record, found, err := db.GetRecordTx(ctx, tx, progressTable, key)
		if err != nil {
			return err
		}
		maySynchronizeLifecycle := true
		if found {
			var current TodoExecutionProgressProjection
			if err := decodePayload(record, &current); err != nil {
				return err
			}
			current = normalizeTodoExecutionProgressProjection(current)
			if err := validateTodoExecutionProgressProjection(current, tenantID, todoID, attempt.ExecutionAttempt, missionID); err != nil {
				return err
			}
			if incoming.MissionProjectionVersion < current.MissionProjectionVersion {
				out = current
				maySynchronizeLifecycle = false
			} else {
				incoming.SubmissionRevision = current.SubmissionRevision
				incoming.CodeSubmissionStatus = current.CodeSubmissionStatus
				incoming.PullRequestStatus = current.PullRequestStatus
				incoming.MergeStatus = current.MergeStatus
				if incoming.MissionProjectionVersion == current.MissionProjectionVersion {
					incoming.ProjectedAt = current.ProjectedAt
					if !reflect.DeepEqual(incoming, current) {
						return fmt.Errorf("%w: Mission projection version %d has conflicting Todo progress content", ErrConflict, incoming.MissionProjectionVersion)
					}
					out = current
				} else {
					next, err := todoExecutionProgressRecord(incoming, attempt.DevelopmentProjectID)
					if err != nil {
						return err
					}
					if err := db.PutRecordTx(ctx, tx, progressTable, key, next); err != nil {
						return err
					}
					out, applied = incoming, true
				}
			}
		} else {
			next, err := todoExecutionProgressRecord(incoming, attempt.DevelopmentProjectID)
			if err != nil {
				return err
			}
			if err := db.PutRecordTx(ctx, tx, progressTable, key, next); err != nil {
				return err
			}
			out, applied = incoming, true
		}
		if !maySynchronizeLifecycle {
			return nil
		}
		return synchronizeGeneralTodoExecutionCompletionTx(ctx, tx, todoTableName, bindingTable, attemptTable, automationTable, attempt, model, projectedAt)
	})
	return out, applied, err
}

func synchronizeGeneralTodoExecutionCompletionTx(
	ctx context.Context,
	tx db.Tx,
	todoTableName, bindingTable, attemptTable, automationTable string,
	attempt TodoExecutionAttempt,
	model projection.ReadModel,
	completedAt time.Time,
) error {
	if len(model.TaskBoard.Tasks) == 0 {
		return nil
	}
	for _, task := range model.TaskBoard.Tasks {
		if task.Status != projection.TaskStatusApproved {
			return nil
		}
	}
	key := scopedRecordKey(attempt.TenantID, attempt.TodoID)
	if err := db.LockRecordKeyTx(ctx, tx, todoTableName, key); err != nil {
		return err
	}
	if err := db.LockRecordKeyTx(ctx, tx, automationTable, key); err != nil {
		return err
	}
	if err := db.LockRecordKeyTx(ctx, tx, bindingTable, key); err != nil {
		return err
	}
	attemptKey := todoExecutionAttemptKey(attempt.TenantID, attempt.TodoID, attempt.ExecutionAttempt)
	if err := db.LockRecordKeyTx(ctx, tx, attemptTable, attemptKey); err != nil {
		return err
	}
	attemptRecord, found, err := db.GetRecordTx(ctx, tx, attemptTable, attemptKey)
	if err != nil {
		return err
	}
	if !found {
		return ErrNotFound
	}
	var currentAttempt TodoExecutionAttempt
	if err := decodePayload(attemptRecord, &currentAttempt); err != nil {
		return err
	}
	currentAttempt = normalizeTodoExecutionAttempt(currentAttempt)
	if currentAttempt.TenantID != attempt.TenantID || currentAttempt.TodoID != attempt.TodoID || currentAttempt.ExecutionAttempt != attempt.ExecutionAttempt || currentAttempt.MissionID != attempt.MissionID {
		return fmt.Errorf("%w: Todo execution attempt identity changed during projection", ErrConflict)
	}
	attempt = currentAttempt
	todoRecord, found, err := db.GetRecordTx(ctx, tx, todoTableName, key)
	if err != nil {
		return err
	}
	if !found {
		return ErrNotFound
	}
	var item TodoItem
	if err := decodePayload(todoRecord, &item); err != nil {
		return err
	}
	item = normalizeTodoItem(item)
	if item.WorkKind == TodoWorkKindDevelopment {
		return nil
	}
	bindingRecord, found, err := db.GetRecordTx(ctx, tx, bindingTable, key)
	if err != nil {
		return err
	}
	if !found {
		return ErrNotFound
	}
	var binding TodoMissionBinding
	if err := decodePayload(bindingRecord, &binding); err != nil {
		return err
	}
	hydrateTodoMissionBindingInternal(bindingRecord, &binding)
	binding = normalizeTodoMissionBinding(binding)
	if binding.BindingID == "" || binding.ExecutionAttempt != attempt.ExecutionAttempt || binding.MissionID != attempt.MissionID {
		return nil
	}
	if item.Version != binding.TodoVersion || item.Version != attempt.TodoVersion || binding.Status == TodoMissionBindingStatusPauseRequested || binding.Status == TodoMissionBindingStatusPaused {
		return nil
	}
	if binding.Status != TodoMissionBindingStatusExecuted {
		if !todoMissionBindingTransitionAllowed(binding.Status, TodoMissionBindingStatusExecuted) {
			return nil
		}
		binding.Status = TodoMissionBindingStatusExecuted
		binding.UpdatedAt = completedAt.UTC()
		binding.Version++
		binding.leaseToken = ""
		binding.leaseExpiresAt = time.Time{}
		nextBinding, err := todoMissionBindingRecord(binding)
		if err != nil {
			return err
		}
		if err := db.PutRecordTx(ctx, tx, bindingTable, key, nextBinding); err != nil {
			return err
		}
	}
	attempt.Status = TodoMissionBindingStatusExecuted
	attempt.CompletedAt = completedAt.UTC()
	attempt.UpdatedAt = completedAt.UTC()
	nextAttempt, err := todoExecutionAttemptRecord(attempt)
	if err != nil {
		return err
	}
	if err := db.PutRecordTx(ctx, tx, attemptTable, attemptKey, nextAttempt); err != nil {
		return err
	}
	automationRecord, found, err := db.GetRecordTx(ctx, tx, automationTable, key)
	if err != nil {
		return err
	}
	if !found {
		return ErrNotFound
	}
	var automation TodoAutomationState
	if err := decodePayload(automationRecord, &automation); err != nil {
		return err
	}
	hydrateTodoAutomationInternal(automationRecord, &automation)
	automation = normalizeTodoAutomationState(automation)
	if automation.ProposalID != binding.ProposalID || automation.TodoVersion != binding.TodoVersion || automation.PlanDigest != binding.PlanDigest {
		return fmt.Errorf("%w: Todo automation and Mission binding identity differ", ErrConflict)
	}
	if automation.Status != TodoAutomationStatusExecuted {
		if !todoAutomationWasApproved(automation.Status) {
			return fmt.Errorf("%w: Todo automation is not in an executable state", ErrConflict)
		}
		automation.Status = TodoAutomationStatusExecuted
		automation.FailureReason = ""
		automation.NextCheckAt = time.Time{}
		automation.ExecutedAt = completedAt.UTC()
		automation.UpdatedAt = completedAt.UTC()
		nextAutomation, err := todoAutomationRecord(automation)
		if err != nil {
			return err
		}
		if err := db.PutRecordTx(ctx, tx, automationTable, key, nextAutomation); err != nil {
			return err
		}
	}
	return nil
}

func (r *Repository) ProjectTodoCodeSubmissionProgress(ctx context.Context, tenantID, todoID string, executionAttempt int, update TodoCodeSubmissionProgress) (TodoExecutionProgressProjection, bool, error) {
	if r == nil || r.router == nil {
		return TodoExecutionProgressProjection{}, false, ErrServiceDependency
	}
	tenantID = normalizedTenantID(tenantID)
	todoID = strings.TrimSpace(todoID)
	if todoID == "" || executionAttempt <= 0 || update.SubmissionRevision <= 0 {
		return TodoExecutionProgressProjection{}, false, ErrInvalidRequest
	}
	if update.ProjectedAt.IsZero() {
		update.ProjectedAt = time.Now().UTC()
	} else {
		update.ProjectedAt = update.ProjectedAt.UTC()
	}
	store, table, err := r.router.StoreForTable(todoExecutionProgressTable)
	if err != nil {
		return TodoExecutionProgressProjection{}, false, err
	}
	key := todoExecutionProgressKey(tenantID, todoID, executionAttempt)
	var out TodoExecutionProgressProjection
	applied := false
	err = store.WithTx(ctx, func(tx db.Tx) error {
		if err := db.LockRecordKeyTx(ctx, tx, table, key); err != nil {
			return err
		}
		record, found, err := db.GetRecordTx(ctx, tx, table, key)
		if err != nil {
			return err
		}
		if !found {
			return ErrNotFound
		}
		if err := decodePayload(record, &out); err != nil {
			return err
		}
		out = normalizeTodoExecutionProgressProjection(out)
		if update.SubmissionRevision < out.SubmissionRevision {
			return nil
		}
		codeStatus := strings.TrimSpace(update.CodeSubmissionStatus)
		pullRequestStatus := strings.TrimSpace(update.PullRequestStatus)
		mergeStatus := strings.TrimSpace(update.MergeStatus)
		if update.SubmissionRevision == out.SubmissionRevision {
			if out.CodeSubmissionStatus != codeStatus || out.PullRequestStatus != pullRequestStatus || out.MergeStatus != mergeStatus {
				return fmt.Errorf("%w: submission revision %d has conflicting Todo progress content", ErrConflict, update.SubmissionRevision)
			}
			return nil
		}
		out.SubmissionRevision = update.SubmissionRevision
		out.CodeSubmissionStatus = codeStatus
		out.PullRequestStatus = pullRequestStatus
		out.MergeStatus = mergeStatus
		out.ProjectedAt = update.ProjectedAt
		next, err := todoExecutionProgressRecord(out, recordString(record, "development_project_id"))
		if err != nil {
			return err
		}
		if err := db.PutRecordTx(ctx, tx, table, key, next); err != nil {
			return err
		}
		applied = true
		return nil
	})
	return out, applied, err
}

func (r *Repository) GetTodoExecutionProgressProjection(ctx context.Context, tenantID, todoID string, executionAttempt int) (TodoExecutionProgressProjection, error) {
	if r == nil || r.router == nil {
		return TodoExecutionProgressProjection{}, ErrServiceDependency
	}
	tenantID = normalizedTenantID(tenantID)
	todoID = strings.TrimSpace(todoID)
	if todoID == "" || executionAttempt <= 0 {
		return TodoExecutionProgressProjection{}, ErrInvalidRequest
	}
	record, found, err := r.router.GetRecord(ctx, todoExecutionProgressTable, todoExecutionProgressKey(tenantID, todoID, executionAttempt))
	if err != nil {
		return TodoExecutionProgressProjection{}, err
	}
	if !found {
		return TodoExecutionProgressProjection{}, ErrNotFound
	}
	var out TodoExecutionProgressProjection
	if err := decodePayload(record, &out); err != nil {
		return TodoExecutionProgressProjection{}, err
	}
	out = normalizeTodoExecutionProgressProjection(out)
	if err := validateTodoExecutionProgressProjection(out, tenantID, todoID, executionAttempt, ""); err != nil {
		return TodoExecutionProgressProjection{}, err
	}
	return out, nil
}

func todoExecutionAttemptForMissionTx(ctx context.Context, tx db.Tx, ownerTable, attemptTable, tenantID, missionID string) (TodoExecutionAttempt, error) {
	owner, found, err := db.GetRecordTx(ctx, tx, ownerTable, scopedRecordKey(tenantID, missionID))
	if err != nil {
		return TodoExecutionAttempt{}, err
	}
	if !found || recordString(owner, "tenant_id") != tenantID || recordString(owner, "mission_id") != missionID {
		return TodoExecutionAttempt{}, ErrNotFound
	}
	todoID := recordString(owner, "todo_id")
	executionAttempt := int(recordInt64Value(owner["execution_attempt"]))
	if todoID != "" && executionAttempt > 0 {
		record, found, err := db.GetRecordTx(ctx, tx, attemptTable, todoExecutionAttemptKey(tenantID, todoID, executionAttempt))
		if err != nil {
			return TodoExecutionAttempt{}, err
		}
		if found {
			var attempt TodoExecutionAttempt
			if err := decodePayload(record, &attempt); err != nil {
				return TodoExecutionAttempt{}, err
			}
			attempt = normalizeTodoExecutionAttempt(attempt)
			if attempt.MissionID == missionID {
				return attempt, nil
			}
		}
	}
	rows, err := tx.QueryContext(ctx, `SELECT payload_json FROM platform_records WHERE table_name = ?`, attemptTable)
	if err != nil {
		return TodoExecutionAttempt{}, fmt.Errorf("list Todo execution attempts by Mission: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return TodoExecutionAttempt{}, fmt.Errorf("scan Todo execution attempt by Mission: %w", err)
		}
		var record db.Record
		if err := json.Unmarshal([]byte(raw), &record); err != nil {
			return TodoExecutionAttempt{}, fmt.Errorf("decode Todo execution attempt by Mission: %w", err)
		}
		if recordString(record, "tenant_id") != tenantID || recordString(record, "mission_id") != missionID {
			continue
		}
		var attempt TodoExecutionAttempt
		if err := decodePayload(record, &attempt); err != nil {
			return TodoExecutionAttempt{}, err
		}
		return normalizeTodoExecutionAttempt(attempt), nil
	}
	if err := rows.Err(); err != nil {
		return TodoExecutionAttempt{}, fmt.Errorf("iterate Todo execution attempts by Mission: %w", err)
	}
	return TodoExecutionAttempt{}, ErrNotFound
}

func todoExecutionProgressFromMission(attempt TodoExecutionAttempt, model projection.ReadModel, projectedAt time.Time) TodoExecutionProgressProjection {
	out := TodoExecutionProgressProjection{
		TenantID: attempt.TenantID, TodoID: attempt.TodoID, MissionID: attempt.MissionID,
		ExecutionAttempt: attempt.ExecutionAttempt, MissionStatus: string(model.Mission.Status),
		CurrentPhase: missionProgressPhase(model), TotalTaskCount: len(model.TaskBoard.Tasks),
		LastActivityAt: model.UpdatedAt.UTC(), MissionProjectionVersion: model.Checkpoint.Version, ProjectedAt: projectedAt,
	}
	agents := map[string]struct{}{}
	for _, task := range model.TaskBoard.Tasks {
		switch task.Status {
		case projection.TaskStatusAssigned, projection.TaskStatusAccepted, projection.TaskStatusStarted, projection.TaskStatusSubmitted, projection.TaskStatusReworkRequested:
			out.ActiveTaskCount++
		case projection.TaskStatusApproved:
			out.DoneTaskCount++
		}
		if task.Status == projection.TaskStatusBlocked || len(task.BlockerRefs) > 0 {
			out.BlockedTaskCount++
		}
		if task.QAStatus == projection.QAStatusRequested {
			out.PendingApprovalCount++
		}
		switch task.Status {
		case projection.TaskStatusAssigned, projection.TaskStatusAccepted, projection.TaskStatusStarted, projection.TaskStatusSubmitted, projection.TaskStatusReworkRequested, projection.TaskStatusBlocked:
			if agentID := strings.TrimSpace(task.Assignee.String()); agentID != "" {
				agents[agentID] = struct{}{}
			}
		}
	}
	for _, approval := range model.ApprovalRequests {
		switch strings.ToLower(strings.TrimSpace(approval.Status)) {
		case "pending", "requested", "open", "waiting":
			out.PendingApprovalCount++
		}
	}
	if out.TotalTaskCount > 0 {
		out.DeliveryRate = float64(out.DoneTaskCount) / float64(out.TotalTaskCount)
	}
	for agentID := range agents {
		out.CurrentAgentIDs = append(out.CurrentAgentIDs, agentID)
	}
	sort.Strings(out.CurrentAgentIDs)
	return out
}

func missionProgressPhase(model projection.ReadModel) string {
	if model.Mission.Status == projection.MissionStatusCompleted {
		return "closed"
	}
	if len(model.QA.Checks) > 0 || len(model.QA.Approvals) > 0 || len(model.QA.Rejections) > 0 {
		return "qa"
	}
	if len(model.TaskBoard.Tasks) > 0 {
		return "execution"
	}
	return "intake"
}

func todoExecutionProgressRecord(progress TodoExecutionProgressProjection, developmentProjectID string) (db.Record, error) {
	payload, err := encodePayload(progress)
	if err != nil {
		return nil, err
	}
	return db.Record{
		"tenant_id": progress.TenantID, "todo_id": progress.TodoID, "mission_id": progress.MissionID,
		"execution_attempt": progress.ExecutionAttempt, "development_project_id": strings.TrimSpace(developmentProjectID),
		"mission_projection_version": progress.MissionProjectionVersion, "submission_revision": progress.SubmissionRevision,
		payloadField: payload,
	}, nil
}

func todoExecutionProgressKey(tenantID, todoID string, executionAttempt int) string {
	return fmt.Sprintf("%s::%s::%09d", normalizedTenantID(tenantID), strings.TrimSpace(todoID), executionAttempt)
}

func normalizeTodoExecutionProgressProjection(progress TodoExecutionProgressProjection) TodoExecutionProgressProjection {
	progress.TenantID = normalizedTenantID(progress.TenantID)
	progress.TodoID = strings.TrimSpace(progress.TodoID)
	progress.MissionID = strings.TrimSpace(progress.MissionID)
	progress.MissionStatus = strings.TrimSpace(progress.MissionStatus)
	progress.CurrentPhase = strings.TrimSpace(progress.CurrentPhase)
	progress.CodeSubmissionStatus = strings.TrimSpace(progress.CodeSubmissionStatus)
	progress.PullRequestStatus = strings.TrimSpace(progress.PullRequestStatus)
	progress.MergeStatus = strings.TrimSpace(progress.MergeStatus)
	progress.CurrentAgentIDs = normalizeTags(progress.CurrentAgentIDs)
	return progress
}

func validateTodoExecutionProgressProjection(progress TodoExecutionProgressProjection, tenantID, todoID string, executionAttempt int, missionID string) error {
	if progress.TenantID != normalizedTenantID(tenantID) || progress.TodoID != strings.TrimSpace(todoID) || progress.ExecutionAttempt != executionAttempt ||
		progress.MissionID == "" || (strings.TrimSpace(missionID) != "" && progress.MissionID != strings.TrimSpace(missionID)) ||
		progress.MissionProjectionVersion <= 0 || progress.ProjectedAt.IsZero() {
		return fmt.Errorf("%w: Todo execution progress identity is inconsistent", ErrServiceDependency)
	}
	return nil
}

func firstNonBlank(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
