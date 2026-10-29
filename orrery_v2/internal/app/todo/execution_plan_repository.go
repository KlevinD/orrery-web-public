package todo

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"orrery_v2/internal/platform/db"
)

func (r *Repository) ListTodoExecutionPlans(ctx context.Context, tenantID, todoID string) ([]TodoExecutionPlanView, error) {
	tenantID = normalizedTenantID(tenantID)
	todoID = strings.TrimSpace(todoID)
	if r == nil || r.router == nil {
		return nil, ErrServiceDependency
	}
	if todoID == "" {
		return nil, ErrInvalidRequest
	}
	records, err := snapshotForTenant(ctx, r.router, todoExecutionPlanTable, tenantID)
	if err != nil {
		return nil, err
	}
	out := make([]TodoExecutionPlanView, 0)
	for _, record := range records {
		if recordString(record, "todo_id") != todoID {
			continue
		}
		var view TodoExecutionPlanView
		if err := decodePayload(record, &view); err != nil {
			return nil, err
		}
		view = normalizeTodoExecutionPlanView(view, tenantID)
		if err := validateTodoExecutionPlanView(view, tenantID, todoID); err != nil {
			return nil, err
		}
		out = append(out, view)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Plan.ProposalRevision == out[j].Plan.ProposalRevision {
			return out[i].Plan.PlanID > out[j].Plan.PlanID
		}
		return out[i].Plan.ProposalRevision > out[j].Plan.ProposalRevision
	})
	return out, nil
}

func putTodoExecutionPlanTx(ctx context.Context, tx db.Tx, table string, state TodoAutomationState) error {
	if strings.TrimSpace(state.PlanDigest) == "" || strings.TrimSpace(state.Plan.PlanID) == "" {
		return nil
	}
	view := normalizeTodoExecutionPlanView(TodoExecutionPlanView{
		Plan: state.Plan, PlanDigest: state.PlanDigest, LifecycleStatus: state.Status,
	}, state.TenantID)
	if err := validateTodoExecutionPlanView(view, state.TenantID, state.TodoID); err != nil {
		return err
	}
	key := todoExecutionPlanKey(view.Plan.TenantID, view.Plan.TodoID, view.Plan.ProposalRevision, view.Plan.PlanID)
	if err := db.LockRecordKeyTx(ctx, tx, table, key); err != nil {
		return err
	}
	existing, found, err := db.GetRecordTx(ctx, tx, table, key)
	if err != nil {
		return err
	}
	if found {
		var current TodoExecutionPlanView
		if err := decodePayload(existing, &current); err != nil {
			return err
		}
		current = normalizeTodoExecutionPlanView(current, state.TenantID)
		if current.PlanDigest != view.PlanDigest || todoExecutionPlanKey(current.Plan.TenantID, current.Plan.TodoID, current.Plan.ProposalRevision, current.Plan.PlanID) != key {
			return fmt.Errorf("%w: execution plan revision is already bound to different content", ErrConflict)
		}
		current.LifecycleStatus = view.LifecycleStatus
		record, err := todoExecutionPlanRecord(current)
		if err != nil {
			return err
		}
		return db.PutRecordTx(ctx, tx, table, key, record)
	}
	record, err := todoExecutionPlanRecord(view)
	if err != nil {
		return err
	}
	return db.PutRecordTx(ctx, tx, table, key, record)
}

func todoExecutionPlanRecord(view TodoExecutionPlanView) (db.Record, error) {
	payload, err := encodePayload(view)
	if err != nil {
		return nil, err
	}
	return db.Record{
		"tenant_id": view.Plan.TenantID, "todo_id": view.Plan.TodoID, "plan_id": view.Plan.PlanID,
		"proposal_revision": view.Plan.ProposalRevision, "plan_digest": view.PlanDigest, payloadField: payload,
	}, nil
}

func normalizeTodoExecutionPlanView(view TodoExecutionPlanView, tenantID string) TodoExecutionPlanView {
	view.Plan.TenantID = normalizedTenantID(firstNonBlank(view.Plan.TenantID, tenantID))
	view.PlanDigest = strings.TrimSpace(view.PlanDigest)
	return view
}

func validateTodoExecutionPlanView(view TodoExecutionPlanView, tenantID, todoID string) error {
	digest, err := todoExecutionPlanDigest(view.Plan)
	if err != nil {
		return err
	}
	if view.Plan.TenantID != normalizedTenantID(tenantID) || view.Plan.TodoID != strings.TrimSpace(todoID) ||
		view.Plan.PlanID == "" || view.Plan.ProposalRevision <= 0 || view.PlanDigest == "" || digest != view.PlanDigest {
		return fmt.Errorf("%w: Todo execution plan snapshot is inconsistent", ErrServiceDependency)
	}
	return nil
}

func todoExecutionPlanKey(tenantID, todoID string, revision int, planID string) string {
	return fmt.Sprintf("%s::%s::%09d::%s", normalizedTenantID(tenantID), strings.TrimSpace(todoID), revision, strings.TrimSpace(planID))
}
