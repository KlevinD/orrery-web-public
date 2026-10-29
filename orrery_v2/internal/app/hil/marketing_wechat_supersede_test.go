package hil

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestMarketingSupersedeEndsPendingWorkflowAndReplaysOnlySameReplacement(t *testing.T) {
	ctx := context.Background()
	repo, closeStore := openWorkflowRepository(t, "marketing-supersede")
	defer closeStore()
	installWorkflowRepositoryTemplate(t, ctx, repo)
	fixture := workflowRepositoryInstanceFixture()
	fixture.decision.Requester = ActorRef{Kind: ActorService, ID: "marketing"}
	fixture.decision.ActionTypeID = MarketingWechatDraftApprovalActionType
	fixture.decision.Resource = ObjectRef{Kind: MarketingWechatGenerationResourceKind, ID: "run-1"}
	fixture.decision.PayloadPreview["account_id"] = "account-1"
	if err := repo.CreateDecisionWorkflow(ctx, fixture.decision, fixture.caseRecord, fixture.instance, fixture.stages, fixture.tasks, fixture.audit, fixture.notifications); err != nil {
		t.Fatal(err)
	}
	observed := []DecisionResolution{}
	service := NewService(repo, fixedContextProvider{}, &sequenceHILIDs{}, WithClock(func() time.Time { return testRepositoryNow.Add(time.Minute) }), WithResolutionObserver(ResolutionObserverFunc(func(_ context.Context, resolution DecisionResolution) { observed = append(observed, resolution) })))
	input := MarketingWechatApprovalInput{TenantID: fixture.decision.TenantID, RequesterID: "marketing", GenerationID: "run-1", AccountID: "account-1", ProjectID: "project-1", ContentRevision: 2, SubmissionID: "submission-2", BundleHash: "sha256:bundle-2", SupersedesDecisionID: fixture.decision.ID}
	wrong := input
	wrong.AccountID = "other-account"
	if err := service.SupersedeMarketingApproval(ctx, wrong); !errors.Is(err, ErrConflict) {
		t.Fatalf("wrong account: %v", err)
	}
	if err := service.SupersedeMarketingApproval(ctx, input); err != nil {
		t.Fatal(err)
	}
	detail, err := repo.GetWorkflowInstance(ctx, input.TenantID, fixture.instance.ID)
	if err != nil {
		t.Fatal(err)
	}
	if detail.Instance.Status != WorkflowSuperseded || detail.Decision.Status != DecisionDenied || len(detail.Receipts) != 1 {
		t.Fatalf("replacement state: %+v", detail)
	}
	for _, task := range detail.Tasks {
		if task.Status != TaskSuperseded {
			t.Fatalf("old task still actionable: %+v", task)
		}
	}
	for _, stage := range detail.Stages {
		if stage.Status != WorkflowStageSuperseded {
			t.Fatalf("old stage still active: %+v", stage)
		}
	}
	if detail.Receipts[0].ResolverSummary["lifecycle_outcome"] != "superseded" || detail.Receipts[0].ResolverActorKind != ActorService {
		t.Fatalf("replacement is not a service lifecycle receipt: %+v", detail.Receipts)
	}
	if err := service.SupersedeMarketingApproval(ctx, input); err != nil {
		t.Fatalf("replay: %v", err)
	}
	if len(observed) != 2 || observed[0].Receipt.ID != observed[1].Receipt.ID {
		t.Fatalf("durable callback replay: %+v", observed)
	}
	wrong = input
	wrong.SubmissionID = "submission-3"
	if err := service.SupersedeMarketingApproval(ctx, wrong); !errors.Is(err, ErrConflict) {
		t.Fatalf("different replacement: %v", err)
	}
	workflow := NewWorkflowService(WorkflowServiceOptions{Repository: repo, IDs: &sequenceHILIDs{}, Clock: func() time.Time { return testRepositoryNow.Add(time.Minute) }})
	_, err = workflow.DecideTask(ctx, input.TenantID, fixture.tasks[0].ID, fixture.tasks[0].CurrentAssignee, DecideWorkflowTaskInput{Decision: WorkflowTaskApprove, IdempotencyKey: "late-old-approval", ExpectedInstanceVersion: detail.Instance.Version})
	if err == nil {
		t.Fatal("superseded material was approvable")
	}
}

func TestMarketingSupersedeRefusesAlreadyApprovedWorkflow(t *testing.T) {
	ctx := context.Background()
	repo, closeStore := openWorkflowRepository(t, "marketing-approved-conflict")
	defer closeStore()
	installWorkflowRepositoryTemplate(t, ctx, repo)
	fixture := workflowRepositoryInstanceFixture()
	fixture.stages = fixture.stages[:1]
	fixture.tasks = fixture.tasks[:1]
	fixture.decision.Requester = ActorRef{Kind: ActorService, ID: "marketing"}
	fixture.decision.ActionTypeID = MarketingWechatDraftApprovalActionType
	fixture.decision.Resource = ObjectRef{Kind: MarketingWechatGenerationResourceKind, ID: "run-1"}
	fixture.decision.PayloadPreview["account_id"] = "account-1"
	if err := repo.CreateDecisionWorkflow(ctx, fixture.decision, fixture.caseRecord, fixture.instance, fixture.stages, fixture.tasks, fixture.audit, fixture.notifications); err != nil {
		t.Fatal(err)
	}
	workflow := NewWorkflowService(WorkflowServiceOptions{Repository: repo, IDs: &sequenceHILIDs{}, Clock: func() time.Time { return testRepositoryNow.Add(time.Minute) }})
	service := NewService(repo, fixedContextProvider{}, &sequenceHILIDs{}, WithWorkflowService(workflow))
	if _, err := service.ResolveDecision(ctx, fixture.decision.TenantID, fixture.decision.ID, fixture.tasks[0].CurrentAssignee, ResolveDecisionInput{Decision: "approve", IdempotencyKey: "approve-old"}); err != nil {
		t.Fatal(err)
	}
	err := service.SupersedeMarketingApproval(ctx, MarketingWechatApprovalInput{TenantID: fixture.decision.TenantID, RequesterID: "marketing", GenerationID: "run-1", AccountID: "account-1", ProjectID: "project-1", ContentRevision: 2, SubmissionID: "submission-2", BundleHash: "sha256:bundle-2", SupersedesDecisionID: fixture.decision.ID})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("approved replacement error: %v", err)
	}
	detail, err := repo.GetWorkflowInstance(ctx, fixture.decision.TenantID, fixture.instance.ID)
	if err != nil || detail.Instance.Status != WorkflowApproved || len(detail.Receipts) != 1 {
		t.Fatalf("approved history changed: %+v %v", detail, err)
	}
}
