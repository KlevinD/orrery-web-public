package hil

import (
	"context"
	"testing"
	"time"
)

func TestServicePendingNotificationRunsAfterCommitAndNotOnFailure(t *testing.T) {
	service, repo, cleanup := newTestHILService(t, councilTestContext())
	defer cleanup()
	calls := 0
	service.SetNotificationWake(func() {
		calls++
		page, err := repo.ListDecisions(context.Background(), DecisionListQuery{TenantID: "tenant-1", Status: DecisionPending})
		if err != nil || page.Total != 1 {
			t.Fatalf("notification before commit: %+v %v", page, err)
		}
	})
	if _, err := service.CreateDecision(context.Background(), CreateDecisionInput{}); err == nil || calls != 0 {
		t.Fatal("invalid request woke notifications")
	}
	if _, err := service.CreateDecision(context.Background(), testCreateDecisionInput()); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("wakes=%d", calls)
	}
	if _, err := service.ListDecisions(context.Background(), DecisionListQuery{TenantID: "tenant-1"}); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatal("read created a notification wake loop")
	}
	service.SetNotificationWake(nil)
	if _, err := service.CreateDecision(context.Background(), testCreateDecisionInput()); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatal("removed observer still called")
	}
}

func TestServicePendingNotificationIncludesWorkflowReassignment(t *testing.T) {
	ctx := context.Background()
	repo, closeStore := openWorkflowRepository(t, "workflow-notification-transfer")
	defer closeStore()
	installWorkflowRepositoryTemplate(t, ctx, repo)
	fixture := workflowRepositoryInstanceFixture()
	if err := repo.CreateDecisionWorkflow(ctx, fixture.decision, fixture.caseRecord, fixture.instance, fixture.stages, fixture.tasks, fixture.audit, fixture.notifications); err != nil {
		t.Fatal(err)
	}
	workflow := NewWorkflowService(WorkflowServiceOptions{Repository: repo, IDs: &sequenceHILIDs{}, Clock: func() time.Time { return testRepositoryNow.Add(time.Minute) }})
	service := NewService(repo, nil, nil, WithWorkflowService(workflow))
	wakes := 0
	service.SetNotificationWake(func() {
		wakes++
		detail, err := repo.GetWorkflowInstanceByTaskID(ctx, "tenant-1", "task-review")
		if err != nil || detail.Tasks[0].CurrentAssignee.ID != "human-delegate" {
			t.Fatalf("transfer notification before commit: %v", err)
		}
	})
	if _, err := workflow.TransferTask(ctx, "tenant-1", "task-review", ActorRef{Kind: ActorHuman, ID: "human-reviewer"}, TransferWorkflowTaskInput{
		NewAssignee: ActorRef{Kind: ActorHuman, ID: "human-delegate"}, Reason: "Delegate review", IdempotencyKey: "transfer", ExpectedInstanceVersion: 1,
	}); err != nil {
		t.Fatal(err)
	}
	if wakes != 1 {
		t.Fatalf("transfer wakes=%d", wakes)
	}
}
