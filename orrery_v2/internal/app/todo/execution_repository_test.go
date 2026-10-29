package todo

import (
	"context"
	"errors"
	"testing"
	"time"

	"orrery_v2/internal/platform/clock"
	"orrery_v2/internal/platform/idgen"
	"orrery_v2/internal/platform/testkit"
)

func TestBeginTodoMissionExecutionReusesAttemptAcrossClaimsAndPartialRecovery(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	ctx := context.Background()
	repo := NewRepository(env.DBRouter)
	item, state := seedTodoMissionExecution(t, repo, "owner-a")

	first, acquired, err := repo.BeginTodoMissionExecution(ctx, item, state, "owner-a", "claim-1", todoTestTime)
	if err != nil || !acquired || first.ExecutionAttempt != 1 {
		t.Fatalf("first BeginTodoMissionExecution binding=%+v acquired=%v err=%v", first, acquired, err)
	}
	concurrent, acquired, err := repo.BeginTodoMissionExecution(ctx, item, state, "owner-a", "claim-2", todoTestTime.Add(time.Minute))
	if err != nil || acquired || concurrent.BindingID != first.BindingID || concurrent.ExecutionAttempt != 1 {
		t.Fatalf("concurrent claim binding=%+v acquired=%v err=%v", concurrent, acquired, err)
	}

	first.MissionID = "mission-1"
	first.MissionTaskIDs = []string{"task-1", "task-2"}
	first.Status = TodoMissionBindingStatusMaterialized
	first.UpdatedAt = todoTestTime.Add(2 * time.Minute)
	if err := repo.SaveTodoMissionBinding(ctx, first); err != nil {
		t.Fatalf("save materialized binding: %v", err)
	}
	first.Status = TodoMissionBindingStatusFailed
	first.FailureReason = "runtime provider unavailable"
	first.UpdatedAt = todoTestTime.Add(3 * time.Minute)
	if err := repo.SaveTodoMissionBinding(ctx, first); err != nil {
		t.Fatalf("save failed binding: %v", err)
	}
	storedFailed, err := repo.GetTodoMissionBinding(ctx, item.TenantID, item.TodoID)
	if err != nil || storedFailed.FailureReason != first.FailureReason {
		t.Fatalf("failed binding = %+v err=%v", storedFailed, err)
	}
	attempts, err := repo.ListTodoExecutionAttempts(ctx, item.TenantID, item.TodoID)
	if err != nil || len(attempts) != 1 || attempts[0].FailureReason != first.FailureReason {
		t.Fatalf("failed attempts = %+v err=%v", attempts, err)
	}
	recovered, acquired, err := repo.BeginTodoMissionExecution(ctx, item, state, "owner-a", "claim-recovery", todoTestTime.Add(4*time.Minute))
	if err != nil || !acquired {
		t.Fatalf("recovery binding=%+v acquired=%v err=%v", recovered, acquired, err)
	}
	if recovered.BindingID != first.BindingID || recovered.ExecutionAttempt != 1 || recovered.MissionID != "mission-1" || !sameStrings(recovered.MissionTaskIDs, []string{"task-1", "task-2"}) {
		t.Fatalf("recovery created a different Mission attempt: %+v", recovered)
	}
}

func TestListProjectExecutionTodoSnapshotsScopesTenantProjectAndFields(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	ctx := context.Background()
	repo := NewRepository(env.DBRouter)
	for _, item := range []TodoItem{
		{TenantID: "tenant-a", TodoID: "todo-a-target", DevelopmentProjectID: "project-target", WorkKind: TodoWorkKindDevelopment, Title: "Visible title", Description: "must remain private", AcceptanceCriteria: "must remain private", Status: TodoStatusPlanned, AutomationMode: TodoAutomationModeSuggest, CreatedBy: "owner-a", CreatedBySubjectID: "subject-owner-a", CreatedAt: todoTestTime, UpdatedAt: todoTestTime, Version: 1},
		{TenantID: "tenant-a", TodoID: "todo-a-other", DevelopmentProjectID: "project-other", WorkKind: TodoWorkKindDevelopment, Title: "Other project", Status: TodoStatusPlanned, CreatedBy: "owner-a", CreatedAt: todoTestTime, UpdatedAt: todoTestTime, Version: 1},
		{TenantID: "tenant-b", TodoID: "todo-b-target", DevelopmentProjectID: "project-target", WorkKind: TodoWorkKindDevelopment, Title: "Other tenant", Status: TodoStatusPlanned, CreatedBy: "owner-b", CreatedAt: todoTestTime, UpdatedAt: todoTestTime, Version: 1},
	} {
		if err := repo.CreateTodo(ctx, item); err != nil {
			t.Fatalf("CreateTodo(%s): %v", item.TodoID, err)
		}
	}

	items, err := repo.ListProjectExecutionTodoSnapshots(ctx, "tenant-a", "project-target")
	if err != nil || len(items) != 1 {
		t.Fatalf("project snapshots = %+v err=%v", items, err)
	}
	item := items[0]
	if item.TenantID != "tenant-a" || item.DevelopmentProjectID != "project-target" || item.TodoID != "todo-a-target" || item.Title != "Visible title" || item.CreatedBySubjectID != "subject-owner-a" {
		t.Fatalf("scoped project snapshot = %+v", item)
	}
	if _, err := repo.ListProjectExecutionTodoSnapshots(ctx, "", "project-target"); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("empty tenant error = %v, want ErrInvalidRequest", err)
	}
	if _, err := repo.ListProjectExecutionTodoSnapshots(ctx, "tenant-a", ""); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("empty project error = %v, want ErrInvalidRequest", err)
	}
}

func TestStaleTodoMissionExecutionLeaseCannotAdvanceRecoveredBinding(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	ctx := context.Background()
	repo := NewRepository(env.DBRouter)
	item, state := seedTodoMissionExecution(t, repo, "owner-a")
	first, acquired, err := repo.BeginTodoMissionExecution(ctx, item, state, "owner-a", "claim-stale", todoTestTime)
	if err != nil || !acquired {
		t.Fatalf("first claim acquired=%v err=%v", acquired, err)
	}
	recovered, acquired, err := repo.BeginTodoMissionExecution(ctx, item, state, "owner-a", "claim-current", todoTestTime.Add(todoMissionExecutionLease))
	if err != nil || !acquired {
		t.Fatalf("recovery claim acquired=%v err=%v", acquired, err)
	}
	first.MissionID = "mission-stale"
	first.Status = TodoMissionBindingStatusMaterialized
	first.UpdatedAt = todoTestTime.Add(todoMissionExecutionLease + time.Minute)
	if err := repo.SaveTodoMissionBinding(ctx, first); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale claimant save error = %v, want ErrConflict", err)
	}
	recovered.MissionID = "mission-current"
	recovered.Status = TodoMissionBindingStatusMaterialized
	recovered.UpdatedAt = first.UpdatedAt
	if err := repo.SaveTodoMissionBinding(ctx, recovered); err != nil {
		t.Fatalf("current claimant save: %v", err)
	}
	stored, err := repo.GetTodoMissionBinding(ctx, item.TenantID, item.TodoID)
	if err != nil || stored.MissionID != "mission-current" {
		t.Fatalf("stored recovered binding = %+v err=%v", stored, err)
	}
}

func TestExecutingAutomationRecoveryWaitsForExpiredOrchestrationLeaseAndSkipsActiveExecution(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	ctx := context.Background()
	repo := NewRepository(env.DBRouter)
	item, state := seedTodoMissionExecution(t, repo, "owner-a")
	state.UpdatedAt = todoTestTime
	if err := repo.SaveTodoAutomation(ctx, state); err != nil {
		t.Fatal(err)
	}

	candidates, err := repo.ListTodoAutomationExecutionRecoveryCandidates(ctx, todoTestTime.Add(todoMissionExecutionLease-time.Second), 10)
	if err != nil || len(candidates) != 0 {
		t.Fatalf("recovery before missing-binding lease expiry candidates=%+v err=%v", candidates, err)
	}
	candidates, err = repo.ListTodoAutomationExecutionRecoveryCandidates(ctx, todoTestTime.Add(todoMissionExecutionLease), 10)
	if err != nil || len(candidates) != 1 || candidates[0].TodoID != item.TodoID {
		t.Fatalf("missing-binding recovery candidates=%+v err=%v", candidates, err)
	}

	binding, acquired, err := repo.BeginTodoMissionExecution(ctx, item, state, "owner-a", "claim-1", todoTestTime.Add(todoMissionExecutionLease))
	if err != nil || !acquired {
		t.Fatalf("begin binding=%+v acquired=%v err=%v", binding, acquired, err)
	}
	candidates, err = repo.ListTodoAutomationExecutionRecoveryCandidates(ctx, todoTestTime.Add(2*todoMissionExecutionLease-time.Second), 10)
	if err != nil || len(candidates) != 0 {
		t.Fatalf("recovery before binding lease expiry candidates=%+v err=%v", candidates, err)
	}
	candidates, err = repo.ListTodoAutomationExecutionRecoveryCandidates(ctx, todoTestTime.Add(2*todoMissionExecutionLease), 10)
	if err != nil || len(candidates) != 1 {
		t.Fatalf("expired creating-binding recovery candidates=%+v err=%v", candidates, err)
	}

	binding.MissionID = "mission-active"
	binding.MissionTaskIDs = []string{"task-active"}
	binding.Status = TodoMissionBindingStatusMaterialized
	binding.UpdatedAt = todoTestTime.Add(2 * todoMissionExecutionLease)
	if err := repo.SaveTodoMissionBinding(ctx, binding); err != nil {
		t.Fatal(err)
	}
	binding.Status = TodoMissionBindingStatusDispatching
	binding.UpdatedAt = binding.UpdatedAt.Add(time.Minute)
	if err := repo.SaveTodoMissionBinding(ctx, binding); err != nil {
		t.Fatal(err)
	}
	binding.Status = TodoMissionBindingStatusExecuting
	binding.UpdatedAt = binding.UpdatedAt.Add(time.Minute)
	if err := repo.SaveTodoMissionBinding(ctx, binding); err != nil {
		t.Fatal(err)
	}
	candidates, err = repo.ListTodoAutomationExecutionRecoveryCandidates(ctx, todoTestTime.Add(10*todoMissionExecutionLease), 10)
	if err != nil || len(candidates) != 0 {
		t.Fatalf("active execution was selected for orchestration replay: candidates=%+v err=%v", candidates, err)
	}
}

func TestBeginTodoMissionExecutionAllowsNewAttemptOnlyAfterCurrentMissionIsTerminal(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	ctx := context.Background()
	repo := NewRepository(env.DBRouter)
	item, state := seedTodoMissionExecution(t, repo, "owner-a")
	binding, acquired, err := repo.BeginTodoMissionExecution(ctx, item, state, "owner-a", "claim-1", todoTestTime)
	if err != nil || !acquired {
		t.Fatalf("begin binding=%+v acquired=%v err=%v", binding, acquired, err)
	}
	binding.MissionID = "mission-1"
	binding.MissionTaskIDs = []string{"task-1"}
	binding.Status = TodoMissionBindingStatusMaterialized
	binding.UpdatedAt = todoTestTime.Add(time.Minute)
	if err := repo.SaveTodoMissionBinding(ctx, binding); err != nil {
		t.Fatal(err)
	}
	binding.Status = TodoMissionBindingStatusDispatching
	binding.UpdatedAt = todoTestTime.Add(2 * time.Minute)
	if err := repo.SaveTodoMissionBinding(ctx, binding); err != nil {
		t.Fatal(err)
	}
	binding.Status = TodoMissionBindingStatusExecuting
	binding.UpdatedAt = todoTestTime.Add(3 * time.Minute)
	if err := repo.SaveTodoMissionBinding(ctx, binding); err != nil {
		t.Fatal(err)
	}

	next := state
	next.ProposalID = "proposal-2"
	next.PlanDigest = "digest-2"
	next.ApprovedPlanDigest = "digest-2"
	next.UpdatedAt = todoTestTime.Add(4 * time.Minute)
	if err := repo.SaveTodoAutomation(ctx, next); err != nil {
		t.Fatal(err)
	}
	if _, _, err := repo.BeginTodoMissionExecution(ctx, item, next, "owner-a", "claim-2", todoTestTime.Add(4*time.Minute)); !errors.Is(err, ErrConflict) {
		t.Fatalf("new proposal while Mission active error = %v, want ErrConflict", err)
	}

	binding.Status = TodoMissionBindingStatusExecuted
	binding.UpdatedAt = todoTestTime.Add(5 * time.Minute)
	if err := repo.SaveTodoMissionBinding(ctx, binding); err != nil {
		t.Fatal(err)
	}
	second, acquired, err := repo.BeginTodoMissionExecution(ctx, item, next, "owner-a", "claim-2", todoTestTime.Add(6*time.Minute))
	if err != nil || !acquired || second.ExecutionAttempt != 2 || second.BindingID == binding.BindingID || second.MissionID != "" {
		t.Fatalf("second terminal attempt binding=%+v acquired=%v err=%v", second, acquired, err)
	}
}

func seedTodoMissionExecution(t *testing.T, repo *Repository, ownerID string) (TodoItem, TodoAutomationState) {
	t.Helper()
	service := NewService(ServiceOptions{
		Clock: clock.NewFake(todoTestTime), IDGenerator: idgen.NewDeterministic("todo-execution"), Repository: repo,
	})
	viewer := testViewer("tenant-a", ownerID)
	item := mustCreateTask(t, service, context.Background(), viewer, CreateTodoRequest{
		Title: "Mission execution", AutomationMode: TodoAutomationModeManaged,
	})
	state := TodoAutomationState{
		TenantID: item.TenantID, ProposalID: "proposal-1", TodoID: item.TodoID, TodoVersion: item.Version,
		Status: TodoAutomationStatusExecuting, ConversationID: "conversation-1", PlanDigest: "digest-1", ApprovedPlanDigest: "digest-1",
		DecisionBy: ownerID, CreatedAt: todoTestTime, UpdatedAt: todoTestTime,
	}
	recordTodoAutomationDecisionViewer(&state, viewer)
	if err := repo.SaveTodoAutomation(context.Background(), state); err != nil {
		t.Fatalf("SaveTodoAutomation: %v", err)
	}
	return item, state
}
