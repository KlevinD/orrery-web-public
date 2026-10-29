package todo

import (
	"context"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/testkit"
)

func TestTodoAutomationStatusAndDiscussionFingerprintDoNotOverwriteEachOther(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	repo := NewRepository(env.DBRouter)
	ctx := context.Background()
	item, state := todoAutomationConcurrencyFixture(t, "tenant-concurrency", "todo-concurrency", todoTestTime)
	if err := repo.CreateTodo(ctx, item); err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveTodoAutomation(ctx, state); err != nil {
		t.Fatal(err)
	}
	if err := repo.MarkTodoAutomationDiscussionMembersSynced(ctx, item.TenantID, item.TodoID, state.ProposalID, "members-v1"); err != nil {
		t.Fatal(err)
	}
	state.Status = TodoAutomationStatusApproved
	state.ApprovedPlanDigest = state.PlanDigest
	updated, _, err := repo.CompareAndSaveTodoAutomation(ctx, state, TodoAutomationStatusPendingApproval)
	if err != nil || !updated {
		t.Fatalf("approve automation updated=%v err=%v", updated, err)
	}
	loaded, err := repo.GetTodoAutomation(ctx, item.TenantID, item.TodoID)
	if err != nil || loaded.Status != TodoAutomationStatusApproved || loaded.discussionMembersFingerprint != "members-v1" {
		t.Fatalf("merged automation = %+v err=%v", loaded, err)
	}
}

func TestPostgresTodoAutomationSerializesApprovalAndMemberSync(t *testing.T) {
	rawURL := os.Getenv("ORRERY_TEST_POSTGRES_URL")
	if rawURL == "" {
		t.Skip("ORRERY_TEST_POSTGRES_URL is not set")
	}
	ctx := context.Background()
	registries := make([]*db.Registry, 2)
	repositories := make([]*Repository, 2)
	for index := range registries {
		registry, err := db.OpenRegistry(ctx, db.RegistryConfig{Databases: map[string]db.Config{
			db.RoleCore: {Driver: db.DriverPostgres, URL: rawURL, Name: fmt.Sprintf("todo-concurrency-%d", index)},
		}})
		if err != nil {
			t.Fatalf("open postgres registry %d: %v", index, err)
		}
		registries[index] = registry
		repositories[index] = NewRepository(db.NewRouter(registry))
	}
	t.Cleanup(func() {
		for _, registry := range registries {
			_ = registry.Close()
		}
	})

	for iteration := 0; iteration < 12; iteration++ {
		todoID := fmt.Sprintf("todo-concurrent-%d-%d", time.Now().UnixNano(), iteration)
		item, state := todoAutomationConcurrencyFixture(t, "tenant-concurrent", todoID, todoTestTime.Add(time.Duration(iteration)*time.Second))
		if err := repositories[0].CreateTodo(ctx, item); err != nil {
			t.Fatal(err)
		}
		if err := repositories[0].SaveTodoAutomation(ctx, state); err != nil {
			t.Fatal(err)
		}
		approved := state
		approved.Status = TodoAutomationStatusApproved
		approved.ApprovedPlanDigest = approved.PlanDigest
		fingerprint := fmt.Sprintf("members-%d", iteration)
		start := make(chan struct{})
		errorsByOperation := make(chan error, 2)
		var wait sync.WaitGroup
		wait.Add(2)
		go func() {
			defer wait.Done()
			<-start
			updated, _, err := repositories[0].CompareAndSaveTodoAutomation(ctx, approved, TodoAutomationStatusPendingApproval)
			if err == nil && !updated {
				err = fmt.Errorf("approval compare did not update")
			}
			errorsByOperation <- err
		}()
		go func() {
			defer wait.Done()
			<-start
			errorsByOperation <- repositories[1].MarkTodoAutomationDiscussionMembersSynced(ctx, item.TenantID, item.TodoID, state.ProposalID, fingerprint)
		}()
		close(start)
		wait.Wait()
		close(errorsByOperation)
		for err := range errorsByOperation {
			if err != nil {
				t.Fatalf("iteration %d concurrent mutation: %v", iteration, err)
			}
		}
		loaded, err := repositories[0].GetTodoAutomation(ctx, item.TenantID, item.TodoID)
		if err != nil || loaded.Status != TodoAutomationStatusApproved || loaded.discussionMembersFingerprint != fingerprint {
			t.Fatalf("iteration %d merged automation = %+v err=%v", iteration, loaded, err)
		}
		_ = repositories[0].router.DeleteRecord(ctx, todoAutomationTable, scopedRecordKey(item.TenantID, item.TodoID))
		_ = repositories[0].router.DeleteRecord(ctx, todoTable, scopedRecordKey(item.TenantID, item.TodoID))
		_ = repositories[0].router.DeleteRecord(ctx, todoExecutionPlanTable, todoExecutionPlanKey(state.TenantID, state.TodoID, state.Plan.ProposalRevision, state.Plan.PlanID))
	}
}

func todoAutomationConcurrencyFixture(t *testing.T, tenantID, todoID string, at time.Time) (TodoItem, TodoAutomationState) {
	t.Helper()
	item := normalizeTodoItem(TodoItem{
		TenantID: tenantID, TodoID: todoID, Title: "Concurrent automation", Status: TodoStatusPlanned,
		AutomationMode: TodoAutomationModeSuggest, CreatedBy: "owner-1", CreatedBySubjectID: "owner-1", CreatedAt: at, UpdatedAt: at, Version: 1,
	})
	state := todoExecutionPlanHistoryState(t, item.TenantID, item.TodoID, "proposal-1", 1, at)
	state.ConversationID = "conversation-1"
	state.Plan.ConversationID = state.ConversationID
	digest, err := todoExecutionPlanDigest(state.Plan)
	if err != nil {
		t.Fatal(err)
	}
	state.PlanDigest = digest
	return item, state
}
