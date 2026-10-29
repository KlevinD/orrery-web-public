package todo

import (
	"context"
	"testing"
	"time"

	"orrery_v2/internal/platform/testkit"
)

func TestTodoExecutionPlanHistoryPersistsImmutableRevisions(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	repo := NewRepository(env.DBRouter)
	ctx := context.Background()
	at := time.Date(2026, 7, 26, 9, 0, 0, 0, time.UTC)

	first := todoExecutionPlanHistoryState(t, "tenant-a", "todo-1", "plan-1", 1, at)
	if err := repo.SaveTodoAutomation(ctx, first); err != nil {
		t.Fatalf("save first plan: %v", err)
	}
	first.Status = TodoAutomationStatusRejected
	if err := repo.SaveTodoAutomation(ctx, first); err != nil {
		t.Fatalf("update first lifecycle: %v", err)
	}
	second := todoExecutionPlanHistoryState(t, "tenant-a", "todo-1", "plan-2", 2, at.Add(time.Hour))
	if err := repo.SaveTodoAutomation(ctx, second); err != nil {
		t.Fatalf("save second plan: %v", err)
	}

	reopened := NewRepository(env.DBRouter)
	plans, err := reopened.ListTodoExecutionPlans(ctx, "tenant-a", "todo-1")
	if err != nil {
		t.Fatalf("list plans: %v", err)
	}
	if len(plans) != 2 || plans[0].Plan.PlanID != "plan-2" || plans[0].LifecycleStatus != TodoAutomationStatusPendingApproval || plans[1].Plan.PlanID != "plan-1" || plans[1].LifecycleStatus != TodoAutomationStatusRejected {
		t.Fatalf("plan history = %+v", plans)
	}
	otherTenant, err := reopened.ListTodoExecutionPlans(ctx, "tenant-b", "todo-1")
	if err != nil || len(otherTenant) != 0 {
		t.Fatalf("cross-tenant plan history = %+v err=%v", otherTenant, err)
	}
}

func todoExecutionPlanHistoryState(t *testing.T, tenantID, todoID, planID string, revision int, at time.Time) TodoAutomationState {
	t.Helper()
	plan := TodoExecutionPlan{
		TenantID: tenantID, Version: todoExecutionPlanVersion, Summary: "Plan " + planID,
		PlanID: planID, TodoID: todoID, TodoVersion: 1, ProposalRevision: revision,
		ConversationID: "discussion-1", Status: "proposed", CreatedAt: at, UpdatedAt: at,
	}
	digest, err := todoExecutionPlanDigest(plan)
	if err != nil {
		t.Fatalf("digest plan: %v", err)
	}
	return TodoAutomationState{
		TenantID: tenantID, ProposalID: planID, TodoID: todoID, TodoVersion: 1,
		Status: TodoAutomationStatusPendingApproval, Plan: plan, PlanDigest: digest,
		CreatedAt: at, UpdatedAt: at,
	}
}
