package hil

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestWorkflowServicePublishesAndPreparesFrozenApproval(t *testing.T) {
	ctx := context.Background()
	repo, closeStore := openWorkflowRepository(t, "workflow-service-prepare")
	defer closeStore()
	resolver := fakeWorkflowAssigneeResolver{results: map[string][]ActorRef{
		"people_role:marketing-review": {{Kind: ActorHuman, ID: "human-reviewer"}},
		"requester_manager:":           {{Kind: ActorHuman, ID: "human-manager"}},
	}}
	workflow := NewWorkflowService(WorkflowServiceOptions{
		Repository: repo, Assignees: resolver, IDs: &sequenceHILIDs{}, Clock: func() time.Time { return testRepositoryNow }, NotificationChannel: "feishu",
	})
	definition := WorkflowDefinition{RiskLevels: []RiskLevel{RiskHigh}, Stages: []WorkflowStageDefinition{
		{
			Key: "review", Name: "Review", Order: 1, Type: WorkflowStageApproval,
			Approval: &ApprovalStagePolicy{
				Strategy: ApprovalAny, Assignees: []WorkflowActorSelector{{Kind: WorkflowSelectorPeopleRole, ID: "marketing-review"}},
				DeadlineMinutes: 60, Escalation: []WorkflowEscalationRung{{Actors: []WorkflowActorSelector{{Kind: WorkflowSelectorRequesterManager}}, DeadlineMinutes: 30}},
			},
		},
	}}
	template, err := workflow.SaveTemplateDraft(ctx, ActorRef{Kind: ActorHuman, ID: "admin"}, SaveTemplateDraftInput{
		ID: "template-marketing", TenantID: "tenant-1", Name: "Marketing approval", Definition: definition, IdempotencyKey: "save-template",
	})
	if err != nil {
		t.Fatalf("SaveTemplateDraft: %v", err)
	}
	version, err := workflow.PublishTemplate(ctx, ActorRef{Kind: ActorHuman, ID: "admin"}, PublishTemplateInput{
		Template: template, IdempotencyKey: "publish-template",
		Bindings: []WorkflowTemplateBinding{{
			ID: "binding-marketing", TenantID: "tenant-1", ActionTypeID: "tool_use",
			ToolName: "mcp_marketing_marketing_wechat_draft_create", RiskLevel: RiskHigh, TemplateID: template.ID, Active: true,
		}},
	})
	if err != nil || version.DefinitionHash == "" || version.Version != 1 {
		t.Fatalf("PublishTemplate version=%+v err=%v", version, err)
	}
	if _, err := workflow.SaveTemplateDraft(ctx, ActorRef{Kind: ActorHuman, ID: "admin"}, SaveTemplateDraftInput{
		ID: template.ID, TenantID: template.TenantID, Name: template.Name, Definition: definition,
		Status: WorkflowTemplateActive, LatestPublishedVersion: 1, IdempotencyKey: "edit-published-template",
	}); err != nil {
		t.Fatalf("edit published template draft: %v", err)
	}
	templates, err := workflow.ListTemplates(ctx, WorkflowTemplateListQuery{TenantID: "tenant-1"})
	if err != nil || len(templates) != 1 || templates[0].Status != WorkflowTemplateActive || templates[0].LatestPublishedVersion != 1 {
		t.Fatalf("published template after draft edit = %+v err=%v", templates, err)
	}
	decision := workflowServiceDecisionFixture()
	decision.ExpiresAt = testRepositoryNow.Add(30 * time.Minute)
	prepared, managed, err := workflow.PrepareDecisionWorkflow(ctx, decision)
	if err != nil || !managed {
		t.Fatalf("PrepareDecisionWorkflow managed=%v err=%v", managed, err)
	}
	if prepared.Instance.TemplateVersionID != version.ID || prepared.Instance.DefinitionHash != version.DefinitionHash || prepared.Instance.MaterialHash != "sha256:material" {
		t.Fatalf("prepared instance = %+v", prepared.Instance)
	}
	if len(prepared.Stages) != 1 || prepared.Stages[0].Status != WorkflowStageActive || len(prepared.Tasks) != 1 || prepared.Tasks[0].CurrentAssignee.ID != "human-reviewer" {
		t.Fatalf("prepared stages=%+v tasks=%+v", prepared.Stages, prepared.Tasks)
	}
	if !prepared.Stages[0].DeadlineAt.Equal(decision.ExpiresAt) || !prepared.Tasks[0].DeadlineAt.Equal(decision.ExpiresAt) {
		t.Fatalf("workflow deadline exceeds decision expiry: stage=%s task=%s decision=%s", prepared.Stages[0].DeadlineAt, prepared.Tasks[0].DeadlineAt, decision.ExpiresAt)
	}
	if len(prepared.EscalationActors[prepared.Stages[0].ID]) != 1 || prepared.EscalationActors[prepared.Stages[0].ID][0].ID != "human-manager" {
		t.Fatalf("escalation actors = %+v", prepared.EscalationActors)
	}
	if len(prepared.Notifications) != 1 || prepared.Notifications[0].Channel != "feishu" {
		t.Fatalf("notifications = %+v, want production channel", prepared.Notifications)
	}
}

func TestWorkflowServicePreparesAndAdvancesDistinctApproverStages(t *testing.T) {
	ctx := context.Background()
	repo, closeStore := openWorkflowRepository(t, "workflow-service-distinct-approvers")
	defer closeStore()
	resolver := fakeWorkflowAssigneeResolver{results: map[string][]ActorRef{
		"requester_manager:":            {{Kind: ActorHuman, ID: "human-manager"}},
		"people_role:approval.reviewer": {{Kind: ActorHuman, ID: "human-manager"}, {Kind: ActorHuman, ID: "human-control"}},
	}}
	ids := &sequenceHILIDs{}
	workflow := NewWorkflowService(WorkflowServiceOptions{
		Repository: repo, Assignees: resolver, IDs: ids, Clock: func() time.Time { return testRepositoryNow },
	})
	installDistinctApprovalTemplate(t, ctx, workflow, "tenant-1", "system.command.execute")
	service := NewService(repo, fixedContextProvider{snapshot: OntologyContextSnapshot{BundleID: "bundle-distinct", Complete: true}}, ids,
		WithClock(func() time.Time { return testRepositoryNow }), WithWorkflowService(workflow))
	created, err := service.CreateDecision(ctx, CreateDecisionInput{
		TenantID: "tenant-1", Requester: ActorRef{Kind: ActorAgent, ID: "agent-requester"},
		Action: "system.command.execute", ActionTypeID: "system.command.execute",
		Resource: ObjectRef{Kind: "runtime_approval", ID: "approval-distinct"}, ArgsHash: "sha256:args-distinct",
		RiskLevel: RiskHigh, ApprovalRequired: true, PreferredMode: ModeApproval,
		SubjectRefs:    []ObjectRef{{Kind: "agent_run", ID: "run-distinct"}},
		PayloadPreview: map[string]any{"approval_tool_name": "exec_command", "material_hash": "sha256:material-distinct"},
		IdempotencyKey: "create-distinct-approval", ExpiresAt: testRepositoryNow.Add(8 * time.Hour),
	})
	if err != nil {
		t.Fatalf("CreateDecision: %v", err)
	}
	detail, err := repo.GetWorkflowInstanceByDecisionID(ctx, "tenant-1", created.Decision.ID)
	if err != nil {
		t.Fatalf("GetWorkflowInstanceByDecisionID: %v", err)
	}
	if len(detail.Tasks) != 2 || detail.Tasks[0].CurrentAssignee.ID != "human-manager" || detail.Tasks[0].Status != TaskPending ||
		detail.Tasks[1].CurrentAssignee.ID != "human-control" || detail.Tasks[1].Status != TaskScheduled {
		t.Fatalf("distinct approval tasks = %+v", detail.Tasks)
	}

	detail, err = workflow.DecideTask(ctx, "tenant-1", detail.Tasks[0].ID, ActorRef{Kind: ActorHuman, ID: "human-manager"}, DecideWorkflowTaskInput{
		Decision: WorkflowTaskApprove, IdempotencyKey: "approve-manager-stage", ExpectedInstanceVersion: 1,
	})
	if err != nil {
		t.Fatalf("DecideTask manager stage: %v", err)
	}
	if detail.Instance.Status != WorkflowRunning || detail.Instance.CurrentStageKey != "control-owner-review" ||
		detail.Decision.Status != DecisionPending || detail.Tasks[1].Status != TaskPending {
		t.Fatalf("advanced distinct approval = %+v", detail)
	}
}

func TestWorkflowServiceFailsClosedWhenDistinctApproverCannotBeResolved(t *testing.T) {
	ctx := context.Background()
	repo, closeStore := openWorkflowRepository(t, "workflow-service-distinct-approver-missing")
	defer closeStore()
	resolver := fakeWorkflowAssigneeResolver{results: map[string][]ActorRef{
		"requester_manager:":            {{Kind: ActorHuman, ID: "human-manager"}},
		"people_role:approval.reviewer": {{Kind: ActorHuman, ID: "human-manager"}},
	}}
	workflow := NewWorkflowService(WorkflowServiceOptions{
		Repository: repo, Assignees: resolver, IDs: &sequenceHILIDs{}, Clock: func() time.Time { return testRepositoryNow },
	})
	installDistinctApprovalTemplate(t, ctx, workflow, "tenant-1", "system.command.execute")
	decision := workflowServiceDecisionFixture()
	decision.Action = "system.command.execute"
	decision.ActionTypeID = "system.command.execute"
	decision.PayloadPreview["approval_tool_name"] = "exec_command"
	if _, _, err := workflow.PrepareDecisionWorkflow(ctx, decision); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("PrepareDecisionWorkflow error = %v, want ErrInvalidRequest", err)
	}
}

func TestWorkflowServiceRejectsTemplateThatDoesNotSupportDecisionRisk(t *testing.T) {
	ctx := context.Background()
	repo, closeStore := openWorkflowRepository(t, "workflow-service-risk-mismatch")
	defer closeStore()
	resolver := fakeWorkflowAssigneeResolver{results: map[string][]ActorRef{
		"explicit_human:human-reviewer": {{Kind: ActorHuman, ID: "human-reviewer"}},
	}}
	workflow := NewWorkflowService(WorkflowServiceOptions{
		Repository: repo, Assignees: resolver, IDs: &sequenceHILIDs{}, Clock: func() time.Time { return testRepositoryNow },
	})
	definition := validWorkflowDefinition()
	template, err := workflow.SaveTemplateDraft(ctx, ActorRef{Kind: ActorHuman, ID: "admin"}, SaveTemplateDraftInput{
		ID: "template-low-only", TenantID: "tenant-1", Name: "Low only", Definition: definition, IdempotencyKey: "save-low-only",
	})
	if err != nil {
		t.Fatalf("SaveTemplateDraft: %v", err)
	}
	if _, err := workflow.PublishTemplate(ctx, ActorRef{Kind: ActorHuman, ID: "admin"}, PublishTemplateInput{
		Template: template, IdempotencyKey: "publish-low-only", Bindings: []WorkflowTemplateBinding{{
			ID: "binding-low-only", TenantID: "tenant-1", ActionTypeID: "system.command.execute", TemplateID: template.ID, Active: true,
		}},
	}); err != nil {
		t.Fatalf("PublishTemplate: %v", err)
	}
	decision := workflowServiceDecisionFixture()
	decision.ActionTypeID = "system.command.execute"
	decision.PayloadPreview["approval_tool_name"] = "exec_command"
	if _, _, err := workflow.PrepareDecisionWorkflow(ctx, decision); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("PrepareDecisionWorkflow error = %v, want ErrInvalidRequest", err)
	}
}

func TestWorkflowServicePublishesTemplateIdempotently(t *testing.T) {
	ctx := context.Background()
	repo, closeStore := openWorkflowRepository(t, "workflow-service-publish-idempotent")
	defer closeStore()
	workflow := NewWorkflowService(WorkflowServiceOptions{
		Repository: repo, IDs: &sequenceHILIDs{}, Clock: func() time.Time { return testRepositoryNow },
	})
	template, err := workflow.SaveTemplateDraft(ctx, ActorRef{Kind: ActorHuman, ID: "admin"}, SaveTemplateDraftInput{
		ID: "template-idempotent", TenantID: "tenant-1", Name: "Idempotent approval",
		Definition: validWorkflowDefinition(), IdempotencyKey: "save-idempotent-template",
	})
	if err != nil {
		t.Fatal(err)
	}
	bindings := []WorkflowTemplateBinding{{
		ActionTypeID: "tool_use", ToolName: "mcp_marketing_marketing_wechat_draft_create",
		RiskLevel: RiskHigh, Active: true,
	}}
	first, err := workflow.PublishTemplateByID(ctx, ActorRef{Kind: ActorHuman, ID: "admin"}, template.TenantID, template.ID, bindings, "publish-template-1")
	if err != nil {
		t.Fatalf("first publish: %v", err)
	}
	second, err := workflow.PublishTemplateByID(ctx, ActorRef{Kind: ActorHuman, ID: "admin"}, template.TenantID, template.ID, bindings, "publish-template-1")
	if err != nil {
		t.Fatalf("replayed publish: %v", err)
	}
	if second.ID != first.ID || second.Version != first.Version {
		t.Fatalf("replayed version = %+v, want %+v", second, first)
	}
	stored, err := workflow.GetTemplate(ctx, template.TenantID, template.ID)
	if err != nil || stored.LatestPublishedVersion != 1 {
		t.Fatalf("stored template = %+v err=%v", stored, err)
	}
	changedBindings := append([]WorkflowTemplateBinding(nil), bindings...)
	changedBindings[0].Active = false
	if _, err := workflow.PublishTemplateByID(ctx, ActorRef{Kind: ActorHuman, ID: "admin"}, template.TenantID, template.ID, changedBindings, "publish-template-1"); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("changed replay error = %v, want ErrIdempotencyConflict", err)
	}
}

func TestServiceCreateDecisionUsesBoundWorkflow(t *testing.T) {
	ctx := context.Background()
	repo, closeStore := openWorkflowRepository(t, "workflow-service-create")
	defer closeStore()
	resolver := fakeWorkflowAssigneeResolver{results: map[string][]ActorRef{
		"explicit_human:human-reviewer": {{Kind: ActorHuman, ID: "human-reviewer"}},
	}}
	ids := &sequenceHILIDs{}
	workflow := NewWorkflowService(WorkflowServiceOptions{Repository: repo, Assignees: resolver, IDs: ids, Clock: func() time.Time { return testRepositoryNow }})
	definition := WorkflowDefinition{RiskLevels: []RiskLevel{RiskHigh}, Stages: []WorkflowStageDefinition{{
		Key: "review", Name: "Review", Order: 1, Type: WorkflowStageApproval,
		Approval: &ApprovalStagePolicy{Strategy: ApprovalSingle, Assignees: []WorkflowActorSelector{{Kind: WorkflowSelectorHuman, ID: "human-reviewer"}}, DeadlineMinutes: 60},
	}}}
	template, err := workflow.SaveTemplateDraft(ctx, ActorRef{Kind: ActorHuman, ID: "admin"}, SaveTemplateDraftInput{
		ID: "template-create", TenantID: "tenant-1", Name: "Create approval", Definition: definition, IdempotencyKey: "save-create-template",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := workflow.PublishTemplate(ctx, ActorRef{Kind: ActorHuman, ID: "admin"}, PublishTemplateInput{
		Template: template, IdempotencyKey: "publish-create-template",
		Bindings: []WorkflowTemplateBinding{{
			ID: "binding-create", TenantID: "tenant-1", ActionTypeID: "tool_use",
			ToolName: "mcp_marketing_marketing_wechat_draft_create", RiskLevel: RiskHigh, TemplateID: template.ID, Active: true,
		}},
	}); err != nil {
		t.Fatal(err)
	}
	service := NewService(repo, fixedContextProvider{snapshot: OntologyContextSnapshot{BundleID: "bundle-workflow", Complete: true}}, ids,
		WithClock(func() time.Time { return testRepositoryNow }), WithWorkflowService(workflow))
	input := CreateDecisionInput{
		TenantID: "tenant-1", Requester: ActorRef{Kind: ActorAgent, ID: "jordan"}, Action: "runtime.approval", ActionTypeID: "tool_use",
		Resource: ObjectRef{Kind: "runtime_approval", ID: "approval-1"}, ArgsHash: "sha256:args", RiskLevel: RiskHigh,
		ApprovalRequired: true, PreferredMode: ModeApproval, SubjectRefs: []ObjectRef{{Kind: "agent_run", ID: "run-1"}},
		PayloadPreview: map[string]any{
			"approval_tool_name": "mcp_marketing_marketing_wechat_draft_create", "material_hash": "sha256:material",
		},
		IdempotencyKey: "create-workflow-decision", ExpiresAt: testRepositoryNow.Add(time.Hour),
	}
	result, err := service.CreateDecision(ctx, input)
	if err != nil {
		t.Fatalf("CreateDecision: %v", err)
	}
	page, err := repo.ListWorkflowInstances(ctx, WorkflowListQuery{TenantID: "tenant-1"})
	if err != nil || page.Total != 1 || page.Items[0].Decision.ID != result.Decision.ID || page.Items[0].Tasks[0].CurrentAssignee.ID != "human-reviewer" {
		t.Fatalf("workflow page = %+v err=%v", page, err)
	}
	if _, err := repo.GetCouncilByRequestID(ctx, "tenant-1", result.Decision.ID); err == nil {
		t.Fatal("bound approval unexpectedly created a legacy council")
	}
}

func TestServiceListDecisionsLeavesWorkflowExpiryToWorkflowLifecycle(t *testing.T) {
	ctx := context.Background()
	repo, closeStore := openWorkflowRepository(t, "workflow-service-list-expiry")
	defer closeStore()
	installWorkflowRepositoryTemplate(t, ctx, repo)
	fixture := workflowRepositoryInstanceFixture()
	fixture.decision.ExpiresAt = testRepositoryNow.Add(time.Minute)
	if err := repo.CreateDecisionWorkflow(ctx, fixture.decision, fixture.caseRecord, fixture.instance, fixture.stages, fixture.tasks, fixture.audit, fixture.notifications); err != nil {
		t.Fatalf("CreateDecisionWorkflow: %v", err)
	}

	now := testRepositoryNow.Add(2 * time.Minute)
	workflow := NewWorkflowService(WorkflowServiceOptions{Repository: repo, IDs: &sequenceHILIDs{}, Clock: func() time.Time { return now }})
	service := NewService(repo, fixedContextProvider{}, &sequenceHILIDs{}, WithClock(func() time.Time { return now }), WithWorkflowService(workflow))
	decision, err := service.GetDecision(ctx, fixture.decision.TenantID, fixture.decision.ID)
	if err != nil || decision.Status != DecisionPending {
		t.Fatalf("GetDecision = %+v err=%v, want workflow-managed pending decision", decision, err)
	}
	page, err := service.ListDecisions(ctx, DecisionListQuery{
		TenantID: fixture.decision.TenantID, Mode: fixture.decision.Route.Mode, Status: DecisionPending,
	})
	if err != nil {
		t.Fatalf("ListDecisions: %v", err)
	}
	if page.Total != 1 || len(page.Items) != 1 || page.Items[0].Status != DecisionPending {
		t.Fatalf("workflow-backed decision page = %+v, want pending decision", page)
	}
	detail, err := repo.GetWorkflowInstanceByDecisionID(ctx, fixture.decision.TenantID, fixture.decision.ID)
	if err != nil || detail.Instance.Status != WorkflowRunning || detail.Decision.Status != DecisionPending {
		t.Fatalf("workflow detail = %+v err=%v", detail, err)
	}
}

func TestWorkflowServiceDecidesTasksAndAdvancesStages(t *testing.T) {
	ctx := context.Background()
	repo, closeStore := openWorkflowRepository(t, "workflow-service-decide")
	defer closeStore()
	installWorkflowRepositoryTemplate(t, ctx, repo)
	fixture := workflowRepositoryInstanceFixture()
	if err := repo.CreateDecisionWorkflow(ctx, fixture.decision, fixture.caseRecord, fixture.instance, fixture.stages, fixture.tasks, fixture.audit, fixture.notifications); err != nil {
		t.Fatal(err)
	}
	workflow := NewWorkflowService(WorkflowServiceOptions{Repository: repo, IDs: &sequenceHILIDs{}, Clock: func() time.Time { return testRepositoryNow.Add(time.Minute) }})

	detail, err := workflow.DecideTask(ctx, "tenant-1", "task-review", ActorRef{Kind: ActorHuman, ID: "human-reviewer"}, DecideWorkflowTaskInput{
		Decision: WorkflowTaskApprove, IdempotencyKey: "approve-review", ExpectedInstanceVersion: 1,
	})
	if err != nil {
		t.Fatalf("approve review: %v", err)
	}
	if detail.Instance.Status != WorkflowRunning || detail.Instance.CurrentStageKey != "final" || detail.Instance.Version != 2 {
		t.Fatalf("advanced instance = %+v", detail.Instance)
	}
	if detail.Stages[0].Status != WorkflowStagePassed || detail.Stages[1].Status != WorkflowStageActive || detail.Tasks[0].Status != TaskApproved || detail.Tasks[1].Status != TaskPending {
		t.Fatalf("advanced stages=%+v tasks=%+v", detail.Stages, detail.Tasks)
	}
	if len(detail.Notifications) != 2 || detail.Notifications[1].Recipient.ID != "human-final" {
		t.Fatalf("activation notifications = %+v", detail.Notifications)
	}

	detail, err = workflow.DecideTask(ctx, "tenant-1", "task-final", ActorRef{Kind: ActorHuman, ID: "human-final"}, DecideWorkflowTaskInput{
		Decision: WorkflowTaskApprove, IdempotencyKey: "approve-final", ExpectedInstanceVersion: 2,
	})
	if err != nil {
		t.Fatalf("approve final: %v", err)
	}
	if detail.Instance.Status != WorkflowApproved || detail.Decision.Status != DecisionResolved || len(detail.Receipts) != 1 {
		t.Fatalf("approved detail = %+v", detail)
	}
	receipt := detail.Receipts[0]
	if receipt.Effect != EffectAuthorize || receipt.MaterialHash != fixture.instance.MaterialHash || receipt.ArgsHash != fixture.instance.ArgsHash || !receipt.SingleUse {
		t.Fatalf("approval receipt = %+v", receipt)
	}
	replayed, err := workflow.DecideTask(ctx, "tenant-1", "task-final", ActorRef{Kind: ActorHuman, ID: "human-final"}, DecideWorkflowTaskInput{
		Decision: WorkflowTaskApprove, IdempotencyKey: "approve-final", ExpectedInstanceVersion: 2,
	})
	if err != nil || replayed.Instance.Version != detail.Instance.Version || len(replayed.Receipts) != 1 {
		t.Fatalf("replayed approval=%+v err=%v", replayed, err)
	}
}

func TestServiceResolveDecisionDelegatesWorkflowAtomically(t *testing.T) {
	ctx := context.Background()
	repo, closeStore := openWorkflowRepository(t, "workflow-service-direct-resolution")
	defer closeStore()
	installWorkflowRepositoryTemplate(t, ctx, repo)
	fixture := workflowRepositoryInstanceFixture()
	fixture.stages = fixture.stages[:1]
	fixture.tasks = fixture.tasks[:1]
	if err := repo.CreateDecisionWorkflow(ctx, fixture.decision, fixture.caseRecord, fixture.instance, fixture.stages, fixture.tasks, fixture.audit, fixture.notifications); err != nil {
		t.Fatal(err)
	}
	workflow := NewWorkflowService(WorkflowServiceOptions{Repository: repo, IDs: &sequenceHILIDs{}, Clock: func() time.Time { return testRepositoryNow.Add(time.Minute) }})
	resolutions := make([]DecisionResolution, 0, 1)
	service := NewService(repo, fixedContextProvider{}, &sequenceHILIDs{},
		WithWorkflowService(workflow),
		WithResolutionObserver(ResolutionObserverFunc(func(_ context.Context, resolution DecisionResolution) {
			resolutions = append(resolutions, resolution)
		})),
	)
	resolution, err := service.ResolveDecision(ctx, fixture.decision.TenantID, fixture.decision.ID, fixture.tasks[0].CurrentAssignee, ResolveDecisionInput{
		Decision: "approve", IdempotencyKey: "direct-workflow-approval",
	})
	if err != nil {
		t.Fatalf("ResolveDecision: %v", err)
	}
	detail, err := repo.GetWorkflowInstance(ctx, fixture.decision.TenantID, fixture.instance.ID)
	if err != nil {
		t.Fatalf("GetWorkflowInstance: %v", err)
	}
	if detail.Instance.Status != WorkflowApproved || detail.Decision.Status != DecisionResolved || detail.Tasks[0].Status != TaskApproved || len(detail.Receipts) != 1 {
		t.Fatalf("resolved workflow = %+v", detail)
	}
	if resolution.Receipt.ID != detail.Receipts[0].ID || len(resolutions) != 1 || resolutions[0].Receipt.ID != resolution.Receipt.ID {
		t.Fatalf("resolution=%+v observed=%+v receipts=%+v", resolution, resolutions, detail.Receipts)
	}
	if _, err := service.ResolveDecision(ctx, fixture.decision.TenantID, fixture.decision.ID, ActorRef{Kind: ActorHuman, ID: "human-intruder"}, ResolveDecisionInput{
		Decision: "approve", IdempotencyKey: "unauthorized-terminal-replay",
	}); !errors.Is(err, ErrParticipantRequired) {
		t.Fatalf("unauthorized terminal replay error = %v, want ErrParticipantRequired", err)
	}
}

func TestServiceResolveDecisionRepairsExpiredRunningWorkflowAfterTimelyDirectResolution(t *testing.T) {
	ctx := context.Background()
	repo, closeStore := openWorkflowRepository(t, "workflow-service-direct-resolution-repair")
	defer closeStore()
	installWorkflowRepositoryTemplate(t, ctx, repo)
	fixture := workflowRepositoryInstanceFixture()
	fixture.stages = fixture.stages[:1]
	fixture.tasks = fixture.tasks[:1]
	if err := repo.CreateDecisionWorkflow(ctx, fixture.decision, fixture.caseRecord, fixture.instance, fixture.stages, fixture.tasks, fixture.audit, fixture.notifications); err != nil {
		t.Fatal(err)
	}
	direct := fixture.decision
	direct.Status = DecisionResolved
	if err := repo.ResolveDecision(ctx, direct, DecisionReceipt{
		ID: "receipt-direct-resolution", TenantID: direct.TenantID, RequestID: direct.ID,
		ArgsHash: direct.ArgsHash, Effect: EffectAuthorize, SingleUse: true,
		CreatedAt: testRepositoryNow, ExpiresAt: testRepositoryNow.Add(time.Hour),
	}); err != nil {
		t.Fatalf("ResolveDecision: %v", err)
	}

	workflow := NewWorkflowService(WorkflowServiceOptions{Repository: repo, IDs: &sequenceHILIDs{}, Clock: func() time.Time { return testRepositoryNow.Add(2 * time.Hour) }})
	resolutions := make([]DecisionResolution, 0, 1)
	service := NewService(repo, fixedContextProvider{}, &sequenceHILIDs{},
		WithWorkflowService(workflow),
		WithResolutionObserver(ResolutionObserverFunc(func(_ context.Context, resolution DecisionResolution) {
			resolutions = append(resolutions, resolution)
		})),
	)
	resolution, err := service.ResolveDecision(ctx, fixture.decision.TenantID, fixture.decision.ID, fixture.tasks[0].CurrentAssignee, ResolveDecisionInput{
		Decision: "approve", IdempotencyKey: "repair-direct-resolution",
	})
	if err != nil {
		t.Fatalf("ResolveDecision: %v", err)
	}
	detail, err := repo.GetWorkflowInstance(ctx, fixture.decision.TenantID, fixture.instance.ID)
	if err != nil {
		t.Fatalf("GetWorkflowInstance: %v", err)
	}
	if detail.Instance.Status != WorkflowApproved || detail.Decision.Status != DecisionResolved || detail.Tasks[0].Status != TaskApproved {
		t.Fatalf("repaired workflow = %+v", detail)
	}
	if len(detail.Receipts) != 1 || detail.Receipts[0].ID != "receipt-direct-resolution" || resolution.Receipt.ID != "receipt-direct-resolution" {
		t.Fatalf("repair minted a duplicate receipt: resolution=%+v receipts=%+v", resolution, detail.Receipts)
	}
	if len(resolutions) != 1 || resolutions[0].Receipt.ID != "receipt-direct-resolution" {
		t.Fatalf("repair observer resolutions = %+v", resolutions)
	}
}

func TestServiceResolveDecisionRejectsExpiredWorkflowWithLateDirectResolution(t *testing.T) {
	ctx := context.Background()
	repo, closeStore := openWorkflowRepository(t, "workflow-service-late-direct-resolution")
	defer closeStore()
	installWorkflowRepositoryTemplate(t, ctx, repo)
	fixture := workflowRepositoryInstanceFixture()
	fixture.stages = fixture.stages[:1]
	fixture.tasks = fixture.tasks[:1]
	if err := repo.CreateDecisionWorkflow(ctx, fixture.decision, fixture.caseRecord, fixture.instance, fixture.stages, fixture.tasks, fixture.audit, fixture.notifications); err != nil {
		t.Fatal(err)
	}
	direct := fixture.decision
	direct.Status = DecisionResolved
	if err := repo.ResolveDecision(ctx, direct, DecisionReceipt{
		ID: "receipt-late-direct-resolution", TenantID: direct.TenantID, RequestID: direct.ID,
		ArgsHash: direct.ArgsHash, Effect: EffectAuthorize, SingleUse: true,
		CreatedAt: testRepositoryNow.Add(2 * time.Hour), ExpiresAt: testRepositoryNow.Add(3 * time.Hour),
	}); err != nil {
		t.Fatalf("ResolveDecision: %v", err)
	}

	workflow := NewWorkflowService(WorkflowServiceOptions{Repository: repo, IDs: &sequenceHILIDs{}, Clock: func() time.Time { return testRepositoryNow.Add(3 * time.Hour) }})
	service := NewService(repo, fixedContextProvider{}, &sequenceHILIDs{}, WithWorkflowService(workflow))
	_, err := service.ResolveDecision(ctx, fixture.decision.TenantID, fixture.decision.ID, fixture.tasks[0].CurrentAssignee, ResolveDecisionInput{
		Decision: "approve", IdempotencyKey: "reject-late-direct-resolution",
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("ResolveDecision error = %v, want ErrConflict", err)
	}
	detail, err := repo.GetWorkflowInstance(ctx, fixture.decision.TenantID, fixture.instance.ID)
	if err != nil {
		t.Fatalf("GetWorkflowInstance: %v", err)
	}
	if detail.Instance.Version != 1 || detail.Instance.Status != WorkflowRunning || detail.Tasks[0].Status != TaskPending || len(detail.Receipts) != 1 {
		t.Fatalf("late direct resolution repaired workflow = %+v", detail)
	}
}

func TestServiceResolveDecisionRejectsNonterminalWorkflowShortcut(t *testing.T) {
	ctx := context.Background()
	repo, closeStore := openWorkflowRepository(t, "workflow-service-direct-resolution-multistage")
	defer closeStore()
	installWorkflowRepositoryTemplate(t, ctx, repo)
	fixture := workflowRepositoryInstanceFixture()
	if err := repo.CreateDecisionWorkflow(ctx, fixture.decision, fixture.caseRecord, fixture.instance, fixture.stages, fixture.tasks, fixture.audit, fixture.notifications); err != nil {
		t.Fatal(err)
	}
	workflow := NewWorkflowService(WorkflowServiceOptions{Repository: repo, IDs: &sequenceHILIDs{}, Clock: func() time.Time { return testRepositoryNow.Add(time.Minute) }})
	service := NewService(repo, fixedContextProvider{}, &sequenceHILIDs{}, WithWorkflowService(workflow))
	_, err := service.ResolveDecision(ctx, fixture.decision.TenantID, fixture.decision.ID, fixture.tasks[0].CurrentAssignee, ResolveDecisionInput{
		Decision: "approve", IdempotencyKey: "reject-multistage-shortcut",
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("ResolveDecision error = %v, want ErrConflict", err)
	}
	detail, err := repo.GetWorkflowInstance(ctx, fixture.decision.TenantID, fixture.instance.ID)
	if err != nil {
		t.Fatalf("GetWorkflowInstance: %v", err)
	}
	if detail.Instance.Version != 1 || detail.Instance.Status != WorkflowRunning || detail.Decision.Status != DecisionPending || detail.Tasks[0].Status != TaskPending || len(detail.Receipts) != 0 {
		t.Fatalf("nonterminal shortcut mutated workflow = %+v", detail)
	}
}

func TestWorkflowServiceRejectsRunningWorkflowRepairAgainstDifferentDecisionTerminalState(t *testing.T) {
	ctx := context.Background()
	repo, closeStore := openWorkflowRepository(t, "workflow-service-direct-resolution-conflict")
	defer closeStore()
	installWorkflowRepositoryTemplate(t, ctx, repo)
	fixture := workflowRepositoryInstanceFixture()
	fixture.stages = fixture.stages[:1]
	fixture.tasks = fixture.tasks[:1]
	if err := repo.CreateDecisionWorkflow(ctx, fixture.decision, fixture.caseRecord, fixture.instance, fixture.stages, fixture.tasks, fixture.audit, fixture.notifications); err != nil {
		t.Fatal(err)
	}
	direct := fixture.decision
	direct.Status = DecisionDenied
	if err := repo.ResolveDecision(ctx, direct, DecisionReceipt{
		ID: "receipt-direct-denial", TenantID: direct.TenantID, RequestID: direct.ID,
		ArgsHash: direct.ArgsHash, Effect: EffectDeny, CreatedAt: testRepositoryNow,
	}); err != nil {
		t.Fatalf("ResolveDecision: %v", err)
	}

	workflow := NewWorkflowService(WorkflowServiceOptions{Repository: repo, IDs: &sequenceHILIDs{}, Clock: func() time.Time { return testRepositoryNow.Add(time.Minute) }})
	_, err := workflow.DecideTask(ctx, fixture.decision.TenantID, fixture.tasks[0].ID, fixture.tasks[0].CurrentAssignee, DecideWorkflowTaskInput{
		Decision: WorkflowTaskApprove, IdempotencyKey: "reject-conflicting-repair", ExpectedInstanceVersion: 1,
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("DecideTask error = %v, want ErrConflict", err)
	}
}

func TestWorkflowServiceDoesNotAdvanceRunningWorkflowAfterDirectTerminalResolution(t *testing.T) {
	ctx := context.Background()
	repo, closeStore := openWorkflowRepository(t, "workflow-service-direct-resolution-no-advance")
	defer closeStore()
	installWorkflowRepositoryTemplate(t, ctx, repo)
	fixture := workflowRepositoryInstanceFixture()
	if err := repo.CreateDecisionWorkflow(ctx, fixture.decision, fixture.caseRecord, fixture.instance, fixture.stages, fixture.tasks, fixture.audit, fixture.notifications); err != nil {
		t.Fatal(err)
	}
	direct := fixture.decision
	direct.Status = DecisionResolved
	if err := repo.ResolveDecision(ctx, direct, DecisionReceipt{
		ID: "receipt-direct-multistage", TenantID: direct.TenantID, RequestID: direct.ID,
		ArgsHash: direct.ArgsHash, Effect: EffectAuthorize, SingleUse: true,
		CreatedAt: testRepositoryNow, ExpiresAt: testRepositoryNow.Add(time.Hour),
	}); err != nil {
		t.Fatalf("ResolveDecision: %v", err)
	}

	workflow := NewWorkflowService(WorkflowServiceOptions{Repository: repo, IDs: &sequenceHILIDs{}, Clock: func() time.Time { return testRepositoryNow.Add(time.Minute) }})
	_, err := workflow.DecideTask(ctx, fixture.decision.TenantID, fixture.tasks[0].ID, fixture.tasks[0].CurrentAssignee, DecideWorkflowTaskInput{
		Decision: WorkflowTaskApprove, IdempotencyKey: "do-not-advance-direct-resolution", ExpectedInstanceVersion: 1,
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("DecideTask error = %v, want ErrConflict", err)
	}
	detail, err := repo.GetWorkflowInstance(ctx, fixture.decision.TenantID, fixture.instance.ID)
	if err != nil {
		t.Fatalf("GetWorkflowInstance: %v", err)
	}
	if detail.Instance.Version != 1 || detail.Instance.CurrentStageKey != "review" || detail.Tasks[0].Status != TaskPending || detail.Tasks[1].Status != TaskScheduled || len(detail.Receipts) != 1 {
		t.Fatalf("direct terminal resolution advanced workflow = %+v", detail)
	}
}

func TestServiceWorkflowDecisionNotifiesResolutionObserver(t *testing.T) {
	ctx := context.Background()
	repo, closeStore := openWorkflowRepository(t, "workflow-service-observer")
	defer closeStore()
	installWorkflowRepositoryTemplate(t, ctx, repo)
	fixture := workflowRepositoryInstanceFixture()
	fixture.stages = fixture.stages[:1]
	fixture.tasks = fixture.tasks[:1]
	if err := repo.CreateDecisionWorkflow(ctx, fixture.decision, fixture.caseRecord, fixture.instance, fixture.stages, fixture.tasks, fixture.audit, fixture.notifications); err != nil {
		t.Fatal(err)
	}
	workflow := NewWorkflowService(WorkflowServiceOptions{Repository: repo, IDs: &sequenceHILIDs{}, Clock: func() time.Time { return testRepositoryNow.Add(time.Minute) }})
	resolutions := make([]DecisionResolution, 0, 1)
	service := NewService(repo, fixedContextProvider{}, &sequenceHILIDs{},
		WithWorkflowService(workflow),
		WithResolutionObserver(ResolutionObserverFunc(func(_ context.Context, resolution DecisionResolution) {
			resolutions = append(resolutions, resolution)
		})),
	)
	detail, err := service.DecideWorkflowTask(ctx, "tenant-1", "task-review", ActorRef{Kind: ActorHuman, ID: "human-reviewer"}, DecideWorkflowTaskInput{
		Decision: WorkflowTaskApprove, IdempotencyKey: "approve-observed-workflow", ExpectedInstanceVersion: 1,
	})
	if err != nil {
		t.Fatalf("DecideWorkflowTask: %v", err)
	}
	if detail.Decision.Status != DecisionResolved || len(resolutions) != 1 {
		t.Fatalf("detail=%+v resolutions=%+v", detail.Decision, resolutions)
	}
	if resolutions[0].Decision.ID != fixture.decision.ID || resolutions[0].Receipt.Effect != EffectAuthorize || resolutions[0].Actor.ID != "human-reviewer" {
		t.Fatalf("resolution = %+v", resolutions[0])
	}
	if _, err := service.DecideWorkflowTask(ctx, "tenant-1", "task-review", ActorRef{Kind: ActorHuman, ID: "human-reviewer"}, DecideWorkflowTaskInput{
		Decision: WorkflowTaskApprove, IdempotencyKey: "approve-observed-workflow", ExpectedInstanceVersion: 1,
	}); err != nil {
		t.Fatalf("replayed DecideWorkflowTask: %v", err)
	}
	if len(resolutions) != 2 || resolutions[1].Receipt.ID != resolutions[0].Receipt.ID {
		t.Fatalf("replayed resolutions = %+v", resolutions)
	}
}

func TestWorkflowServiceRejectsAndRequestsChanges(t *testing.T) {
	for _, test := range []struct {
		name         string
		decision     WorkflowTaskDecision
		reason       string
		wantStatus   WorkflowInstanceStatus
		wantDecision DecisionStatus
		wantReceipts int
		wantReceipt  DecisionEffect
	}{
		{name: "reject", decision: WorkflowTaskReject, reason: "brand claim is unsupported", wantStatus: WorkflowRejected, wantDecision: DecisionDenied, wantReceipts: 1, wantReceipt: EffectDeny},
		{name: "request changes", decision: WorkflowTaskRequestChanges, reason: "add the missing evidence", wantStatus: WorkflowChangesRequested, wantDecision: DecisionPending, wantReceipts: 1, wantReceipt: EffectRequiresApproval},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			repo, closeStore := openWorkflowRepository(t, "workflow-service-"+test.name)
			defer closeStore()
			installWorkflowRepositoryTemplate(t, ctx, repo)
			fixture := workflowRepositoryInstanceFixture()
			if err := repo.CreateDecisionWorkflow(ctx, fixture.decision, fixture.caseRecord, fixture.instance, fixture.stages, fixture.tasks, fixture.audit, fixture.notifications); err != nil {
				t.Fatal(err)
			}
			workflow := NewWorkflowService(WorkflowServiceOptions{Repository: repo, IDs: &sequenceHILIDs{}, Clock: func() time.Time { return testRepositoryNow.Add(time.Minute) }})
			detail, err := workflow.DecideTask(ctx, "tenant-1", "task-review", ActorRef{Kind: ActorHuman, ID: "human-reviewer"}, DecideWorkflowTaskInput{
				Decision: test.decision, Reason: test.reason, IdempotencyKey: "decision-" + test.name, ExpectedInstanceVersion: 1,
			})
			if err != nil {
				t.Fatal(err)
			}
			if detail.Instance.Status != test.wantStatus || detail.Decision.Status != test.wantDecision || len(detail.Receipts) != test.wantReceipts {
				t.Fatalf("detail = %+v", detail)
			}
			if test.wantReceipts == 1 && detail.Receipts[0].Effect != test.wantReceipt {
				t.Fatalf("receipt = %+v", detail.Receipts[0])
			}
		})
	}
}

func TestWorkflowServiceRejectsInvalidTaskAuthorityAndReason(t *testing.T) {
	ctx := context.Background()
	repo, closeStore := openWorkflowRepository(t, "workflow-service-authority")
	defer closeStore()
	installWorkflowRepositoryTemplate(t, ctx, repo)
	fixture := workflowRepositoryInstanceFixture()
	if err := repo.CreateDecisionWorkflow(ctx, fixture.decision, fixture.caseRecord, fixture.instance, fixture.stages, fixture.tasks, fixture.audit, fixture.notifications); err != nil {
		t.Fatal(err)
	}
	workflow := NewWorkflowService(WorkflowServiceOptions{Repository: repo, IDs: &sequenceHILIDs{}, Clock: func() time.Time { return testRepositoryNow.Add(time.Minute) }})
	for _, test := range []struct {
		actor    ActorRef
		decision WorkflowTaskDecision
		reason   string
	}{
		{actor: ActorRef{Kind: ActorAgent, ID: "jordan"}, decision: WorkflowTaskApprove},
		{actor: ActorRef{Kind: ActorHuman, ID: "someone-else"}, decision: WorkflowTaskApprove},
		{actor: ActorRef{Kind: ActorHuman, ID: "human-reviewer"}, decision: WorkflowTaskReject},
		{actor: ActorRef{Kind: ActorHuman, ID: "human-reviewer"}, decision: WorkflowTaskRequestChanges},
	} {
		_, err := workflow.DecideTask(ctx, "tenant-1", "task-review", test.actor, DecideWorkflowTaskInput{
			Decision: test.decision, Reason: test.reason, IdempotencyKey: "invalid-authority-" + string(test.decision) + "-" + test.actor.ID, ExpectedInstanceVersion: 1,
		})
		if !errors.Is(err, ErrInvalidRequest) {
			t.Fatalf("actor=%+v decision=%s err=%v, want ErrInvalidRequest", test.actor, test.decision, err)
		}
	}
}

func TestWorkflowServiceReportsStaleTaskVersionAsConflict(t *testing.T) {
	ctx := context.Background()
	repo, closeStore := openWorkflowRepository(t, "workflow-service-stale-version")
	defer closeStore()
	installWorkflowRepositoryTemplate(t, ctx, repo)
	fixture := workflowRepositoryInstanceFixture()
	if err := repo.CreateDecisionWorkflow(ctx, fixture.decision, fixture.caseRecord, fixture.instance, fixture.stages, fixture.tasks, fixture.audit, fixture.notifications); err != nil {
		t.Fatal(err)
	}
	workflow := NewWorkflowService(WorkflowServiceOptions{Repository: repo, IDs: &sequenceHILIDs{}, Clock: func() time.Time { return testRepositoryNow.Add(time.Minute) }})
	if _, err := workflow.DecideTask(ctx, "tenant-1", "task-review", ActorRef{Kind: ActorHuman, ID: "human-reviewer"}, DecideWorkflowTaskInput{
		Decision: WorkflowTaskApprove, IdempotencyKey: "stale-decision", ExpectedInstanceVersion: 2,
	}); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale decision error = %v, want ErrConflict", err)
	}
	if _, err := workflow.TransferTask(ctx, "tenant-1", "task-review", ActorRef{Kind: ActorHuman, ID: "human-reviewer"}, TransferWorkflowTaskInput{
		NewAssignee: ActorRef{Kind: ActorHuman, ID: "human-delegate"}, Reason: "delegate review",
		IdempotencyKey: "stale-transfer", ExpectedInstanceVersion: 2,
	}); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale transfer error = %v, want ErrConflict", err)
	}
}

func TestWorkflowServiceTransfersTaskOnceAndRevokesFormerAssignee(t *testing.T) {
	ctx := context.Background()
	repo, closeStore := openWorkflowRepository(t, "workflow-service-transfer")
	defer closeStore()
	installWorkflowRepositoryTemplate(t, ctx, repo)
	fixture := workflowRepositoryInstanceFixture()
	if err := repo.CreateDecisionWorkflow(ctx, fixture.decision, fixture.caseRecord, fixture.instance, fixture.stages, fixture.tasks, fixture.audit, fixture.notifications); err != nil {
		t.Fatal(err)
	}
	workflow := NewWorkflowService(WorkflowServiceOptions{Repository: repo, IDs: &sequenceHILIDs{}, Clock: func() time.Time { return testRepositoryNow.Add(time.Minute) }})

	detail, err := workflow.TransferTask(ctx, "tenant-1", "task-review", ActorRef{Kind: ActorHuman, ID: "human-reviewer"}, TransferWorkflowTaskInput{
		NewAssignee: ActorRef{Kind: ActorHuman, ID: "human-delegate"}, Reason: "delegate has channel ownership",
		IdempotencyKey: "transfer-review", ExpectedInstanceVersion: 1,
	})
	if err != nil {
		t.Fatalf("TransferTask: %v", err)
	}
	transferred := detail.Tasks[0]
	if transferred.Status != TaskPending || transferred.CurrentAssignee.ID != "human-delegate" || transferred.OriginalAssignee.ID != "human-reviewer" || transferred.TransferCount != 1 || transferred.TransferredFromID != "human-reviewer" || transferred.TransferredToID != "human-delegate" {
		t.Fatalf("transferred task = %+v", transferred)
	}
	if len(detail.Notifications) != 2 || detail.Notifications[1].Recipient.ID != "human-delegate" || detail.Notifications[1].EventType != "task.transferred" {
		t.Fatalf("transfer notifications = %+v", detail.Notifications)
	}
	if _, err := workflow.DecideTask(ctx, "tenant-1", "task-review", ActorRef{Kind: ActorHuman, ID: "human-reviewer"}, DecideWorkflowTaskInput{
		Decision: WorkflowTaskApprove, IdempotencyKey: "old-assignee-approve", ExpectedInstanceVersion: 2,
	}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("former assignee decision err=%v, want ErrInvalidRequest", err)
	}
	if _, err := workflow.TransferTask(ctx, "tenant-1", "task-review", ActorRef{Kind: ActorHuman, ID: "human-delegate"}, TransferWorkflowTaskInput{
		NewAssignee: ActorRef{Kind: ActorHuman, ID: "human-other"}, Reason: "second transfer",
		IdempotencyKey: "second-transfer", ExpectedInstanceVersion: 2,
	}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("second transfer err=%v, want ErrInvalidRequest", err)
	}
	replayed, err := workflow.TransferTask(ctx, "tenant-1", "task-review", ActorRef{Kind: ActorHuman, ID: "human-reviewer"}, TransferWorkflowTaskInput{
		NewAssignee: ActorRef{Kind: ActorHuman, ID: "human-delegate"}, Reason: "delegate has channel ownership",
		IdempotencyKey: "transfer-review", ExpectedInstanceVersion: 1,
	})
	if err != nil || replayed.Instance.Version != detail.Instance.Version || replayed.Tasks[0].CurrentAssignee.ID != "human-delegate" {
		t.Fatalf("replayed transfer=%+v err=%v", replayed, err)
	}
}

func TestWorkflowServiceAdministratorReassignsWithoutDeciding(t *testing.T) {
	ctx := context.Background()
	repo, closeStore := openWorkflowRepository(t, "workflow-service-reassign")
	defer closeStore()
	installWorkflowRepositoryTemplate(t, ctx, repo)
	fixture := workflowRepositoryInstanceFixture()
	if err := repo.CreateDecisionWorkflow(ctx, fixture.decision, fixture.caseRecord, fixture.instance, fixture.stages, fixture.tasks, fixture.audit, fixture.notifications); err != nil {
		t.Fatal(err)
	}
	workflow := NewWorkflowService(WorkflowServiceOptions{Repository: repo, IDs: &sequenceHILIDs{}, Clock: func() time.Time { return testRepositoryNow.Add(time.Minute) }})
	detail, err := workflow.ReassignTask(ctx, "tenant-1", "task-review", ActorRef{Kind: ActorHuman, ID: "approval-admin"}, ReassignWorkflowTaskInput{
		NewAssignee: ActorRef{Kind: ActorHuman, ID: "human-replacement"}, Reason: "original approver is unavailable",
		IdempotencyKey: "reassign-review", ExpectedInstanceVersion: 1,
	})
	if err != nil {
		t.Fatalf("ReassignTask: %v", err)
	}
	if detail.Tasks[0].CurrentAssignee.ID != "human-replacement" || detail.Tasks[0].Decision != "" || detail.Tasks[0].TransferCount != 0 || detail.Instance.Status != WorkflowRunning {
		t.Fatalf("reassigned detail = %+v", detail)
	}
}

func TestWorkflowServiceResubmitsChangesAsSuccessorRevision(t *testing.T) {
	ctx := context.Background()
	repo, closeStore := openWorkflowRepository(t, "workflow-service-resubmit")
	defer closeStore()
	installWorkflowRepositoryTemplate(t, ctx, repo)
	fixture := workflowRepositoryInstanceFixture()
	if err := repo.CreateDecisionWorkflow(ctx, fixture.decision, fixture.caseRecord, fixture.instance, fixture.stages, fixture.tasks, fixture.audit, fixture.notifications); err != nil {
		t.Fatal(err)
	}
	now := testRepositoryNow.Add(time.Minute)
	workflow := NewWorkflowService(WorkflowServiceOptions{Repository: repo, IDs: &sequenceHILIDs{}, Clock: func() time.Time { return now }})
	changed, err := workflow.DecideTask(ctx, "tenant-1", "task-review", ActorRef{Kind: ActorHuman, ID: "human-reviewer"}, DecideWorkflowTaskInput{
		Decision: WorkflowTaskRequestChanges, Reason: "add source links", IdempotencyKey: "request-changes", ExpectedInstanceVersion: 1,
	})
	if err != nil || changed.Instance.Status != WorkflowChangesRequested {
		t.Fatalf("request changes detail=%+v err=%v", changed, err)
	}
	if _, err := workflow.Resubmit(ctx, "tenant-1", fixture.instance.ID, fixture.caseRecord.Requester, ResubmitWorkflowInput{
		ArgsHash: "sha256:new-args", MaterialHash: "sha256:new-material",
		PayloadPreview: map[string]any{"material_hash": "sha256:new-material", "title": "Revised article"},
		ExpiresAt:      now.Add(2 * time.Hour), IdempotencyKey: "stale-resubmit", ExpectedInstanceVersion: 1,
	}); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale resubmit error = %v, want ErrConflict", err)
	}

	resubmitted, err := workflow.Resubmit(ctx, "tenant-1", fixture.instance.ID, fixture.caseRecord.Requester, ResubmitWorkflowInput{
		ArgsHash: "sha256:new-args", MaterialHash: "sha256:new-material",
		PayloadPreview: map[string]any{"material_hash": "sha256:new-material", "title": "Revised article"},
		ExpiresAt:      now.Add(2 * time.Hour), IdempotencyKey: "resubmit-revision-2", ExpectedInstanceVersion: 2,
	})
	if err != nil {
		t.Fatalf("Resubmit: %v", err)
	}
	if resubmitted.Instance.Revision != 2 || resubmitted.Instance.PredecessorInstanceID != fixture.instance.ID || resubmitted.Instance.Status != WorkflowRunning || resubmitted.Instance.ArgsHash != "sha256:new-args" || resubmitted.Instance.MaterialHash != "sha256:new-material" {
		t.Fatalf("successor instance = %+v", resubmitted.Instance)
	}
	if resubmitted.Decision.ID == fixture.decision.ID || resubmitted.Decision.Status != DecisionPending || resubmitted.Tasks[0].CurrentAssignee.ID != "human-reviewer" || resubmitted.Tasks[0].Status != TaskPending {
		t.Fatalf("successor detail = %+v", resubmitted)
	}
	old, err := repo.GetWorkflowInstance(ctx, "tenant-1", fixture.instance.ID)
	if err != nil || old.Instance.Status != WorkflowChangesRequested || len(old.Receipts) != 1 || old.Receipts[0].Effect != EffectRequiresApproval || old.Receipts[0].SingleUse {
		t.Fatalf("old revision = %+v err=%v", old, err)
	}
	replayed, err := workflow.Resubmit(ctx, "tenant-1", fixture.instance.ID, fixture.caseRecord.Requester, ResubmitWorkflowInput{
		ArgsHash: "sha256:new-args", MaterialHash: "sha256:new-material",
		PayloadPreview: map[string]any{"material_hash": "sha256:new-material", "title": "Revised article"},
		ExpiresAt:      now.Add(2 * time.Hour), IdempotencyKey: "resubmit-revision-2", ExpectedInstanceVersion: 2,
	})
	if err != nil || replayed.Instance.ID != resubmitted.Instance.ID {
		t.Fatalf("replayed resubmit=%+v err=%v", replayed, err)
	}
}

func TestWorkflowServiceSweepsReminderEscalationAndExhaustion(t *testing.T) {
	ctx := context.Background()
	repo, closeStore := openWorkflowRepository(t, "workflow-service-sweep")
	defer closeStore()
	installWorkflowRepositoryTemplate(t, ctx, repo)
	fixture := workflowRepositoryInstanceFixture()
	fixture.instance.ExpiresAt = testRepositoryNow.Add(3 * time.Hour)
	fixture.decision.ExpiresAt = fixture.instance.ExpiresAt
	fixture.stages = fixture.stages[:1]
	fixture.tasks = fixture.tasks[:1]
	fixture.stages[0].Definition.Approval.ReminderOffsetsMinutes = []int{30}
	fixture.stages[0].Definition.Approval.Escalation = []WorkflowEscalationRung{{
		Actors: []WorkflowActorSelector{{Kind: WorkflowSelectorHuman, ID: "human-manager"}}, DeadlineMinutes: 30,
	}}
	fixture.stages[0].ResolvedEscalationActors = []ActorRef{{Kind: ActorHuman, ID: "human-manager"}}
	fixture.stages[0].EscalationIndex = -1
	if err := repo.CreateDecisionWorkflow(ctx, fixture.decision, fixture.caseRecord, fixture.instance, fixture.stages, fixture.tasks, fixture.audit, fixture.notifications); err != nil {
		t.Fatal(err)
	}
	workflow := NewWorkflowService(WorkflowServiceOptions{Repository: repo, IDs: &sequenceHILIDs{}, Clock: func() time.Time { return testRepositoryNow }})

	reminderAt := fixture.stages[0].DeadlineAt.Add(-29 * time.Minute)
	result, err := workflow.SweepDue(ctx, "tenant-1", reminderAt, 20)
	if err != nil || result.Reminders != 1 {
		t.Fatalf("reminder sweep=%+v err=%v", result, err)
	}
	replayedReminder, err := workflow.SweepDue(ctx, "tenant-1", reminderAt, 20)
	if err != nil || replayedReminder.Reminders != 0 {
		t.Fatalf("idempotent reminder sweep=%+v err=%v", replayedReminder, err)
	}
	reminded, err := repo.GetWorkflowInstance(ctx, "tenant-1", fixture.instance.ID)
	if err != nil || reminded.Instance.Version != 1 || len(reminded.Notifications) != 2 || reminded.Notifications[1].EventType != "task.reminder" {
		t.Fatalf("reminded detail=%+v err=%v", reminded, err)
	}

	escalatedAt := fixture.stages[0].DeadlineAt.Add(time.Minute)
	result, err = workflow.SweepDue(ctx, "tenant-1", escalatedAt, 20)
	if err != nil || result.Escalations != 1 {
		t.Fatalf("escalation sweep=%+v err=%v", result, err)
	}
	escalated, err := repo.GetWorkflowInstance(ctx, "tenant-1", fixture.instance.ID)
	normalTaskIndex := workflowTaskIndex(escalated.Tasks, "task-review")
	escalationTaskIndex := -1
	for index := range escalated.Tasks {
		if escalated.Tasks[index].CurrentAssignee.ID == "human-manager" {
			escalationTaskIndex = index
		}
	}
	if err != nil || escalated.Instance.Status != WorkflowRunning || escalated.Stages[0].EscalationIndex != 0 || normalTaskIndex < 0 || escalated.Tasks[normalTaskIndex].Status != TaskExpired || len(escalated.Tasks) != 2 || escalationTaskIndex < 0 || escalated.Tasks[escalationTaskIndex].Status != TaskPending {
		t.Fatalf("escalated detail=%+v err=%v", escalated, err)
	}

	exhaustedAt := escalated.Stages[0].DeadlineAt.Add(time.Minute)
	result, err = workflow.SweepDue(ctx, "tenant-1", exhaustedAt, 20)
	if err != nil || result.Expired != 1 {
		t.Fatalf("exhaustion sweep=%+v err=%v", result, err)
	}
	expired, err := repo.GetWorkflowInstance(ctx, "tenant-1", fixture.instance.ID)
	expiredEscalationTaskIndex := -1
	for index := range expired.Tasks {
		if expired.Tasks[index].CurrentAssignee.ID == "human-manager" {
			expiredEscalationTaskIndex = index
		}
	}
	if err != nil || expired.Instance.Status != WorkflowExpired || expired.Decision.Status != DecisionDenied || expiredEscalationTaskIndex < 0 || expired.Tasks[expiredEscalationTaskIndex].Status != TaskExpired || len(expired.Receipts) != 1 || expired.Receipts[0].Effect != EffectDeny {
		t.Fatalf("expired detail=%+v err=%v", expired, err)
	}
}

func TestWorkflowServiceSweepUsesInstanceExpiryAndNotifiesObserver(t *testing.T) {
	ctx := context.Background()
	repo, closeStore := openWorkflowRepository(t, "workflow-service-expiry-observer")
	defer closeStore()
	installWorkflowRepositoryTemplate(t, ctx, repo)
	fixture := workflowRepositoryInstanceFixture()
	fixture.stages = fixture.stages[:1]
	fixture.tasks = fixture.tasks[:1]
	fixture.instance.ExpiresAt = testRepositoryNow.Add(15 * time.Minute)
	fixture.decision.ExpiresAt = fixture.instance.ExpiresAt
	fixture.stages[0].Definition.Approval.Escalation = []WorkflowEscalationRung{{
		Actors: []WorkflowActorSelector{{Kind: WorkflowSelectorHuman, ID: "human-manager"}}, DeadlineMinutes: 30,
	}}
	if err := repo.CreateDecisionWorkflow(ctx, fixture.decision, fixture.caseRecord, fixture.instance, fixture.stages, fixture.tasks, fixture.audit, fixture.notifications); err != nil {
		t.Fatal(err)
	}
	workflow := NewWorkflowService(WorkflowServiceOptions{Repository: repo, IDs: &sequenceHILIDs{}})
	resolutions := make([]DecisionResolution, 0, 1)
	_ = NewService(repo, fixedContextProvider{}, &sequenceHILIDs{},
		WithWorkflowService(workflow),
		WithResolutionObserver(ResolutionObserverFunc(func(_ context.Context, resolution DecisionResolution) {
			resolutions = append(resolutions, resolution)
		})),
	)
	result, err := workflow.SweepDue(ctx, "tenant-1", fixture.instance.ExpiresAt.Add(time.Minute), 20)
	if err != nil {
		t.Fatalf("SweepDue: %v", err)
	}
	if result.Expired != 1 || result.Escalations != 0 || len(resolutions) != 1 {
		t.Fatalf("result=%+v resolutions=%+v", result, resolutions)
	}
	if resolutions[0].Decision.Status != DecisionDenied || resolutions[0].Receipt.Effect != EffectDeny {
		t.Fatalf("resolution = %+v", resolutions[0])
	}
}

func TestWorkflowServiceSweepRecoversMissingAssignmentNotification(t *testing.T) {
	ctx := context.Background()
	repo, closeStore := openWorkflowRepository(t, "workflow-service-notification-recovery")
	defer closeStore()
	installWorkflowRepositoryTemplate(t, ctx, repo)
	fixture := workflowRepositoryInstanceFixture()
	fixture.notifications = nil
	if err := repo.CreateDecisionWorkflow(ctx, fixture.decision, fixture.caseRecord, fixture.instance, fixture.stages, fixture.tasks, fixture.audit, nil); err != nil {
		t.Fatal(err)
	}
	workflow := NewWorkflowService(WorkflowServiceOptions{Repository: repo, IDs: &sequenceHILIDs{}, Clock: func() time.Time { return testRepositoryNow }})
	if _, err := workflow.SweepDue(ctx, "tenant-1", testRepositoryNow.Add(time.Minute), 20); err != nil {
		t.Fatalf("SweepDue: %v", err)
	}
	detail, err := repo.GetWorkflowInstance(ctx, "tenant-1", fixture.instance.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(detail.Notifications) != 1 || detail.Notifications[0].EventType != "task.assigned" || detail.Notifications[0].Payload["recovered"] != true {
		t.Fatalf("notifications = %+v", detail.Notifications)
	}
	if _, err := workflow.SweepDue(ctx, "tenant-1", testRepositoryNow.Add(time.Minute), 20); err != nil {
		t.Fatalf("replayed SweepDue: %v", err)
	}
	replayed, err := repo.GetWorkflowInstance(ctx, "tenant-1", fixture.instance.ID)
	if err != nil || len(replayed.Notifications) != 1 {
		t.Fatalf("replayed notifications=%+v err=%v", replayed.Notifications, err)
	}
}

func TestWorkflowServiceExpiresCouncilWithoutAutoVote(t *testing.T) {
	ctx := context.Background()
	repo, closeStore := openWorkflowRepository(t, "workflow-service-council-expiry")
	defer closeStore()
	installWorkflowRepositoryTemplate(t, ctx, repo)
	fixture := workflowRepositoryInstanceFixture()
	fixture.stages = []WorkflowStageInstance{{
		ID: "stage-council", TenantID: "tenant-1", WorkflowInstanceID: fixture.instance.ID,
		Definition: WorkflowStageDefinition{Key: "council", Name: "Council", Order: 1, Type: WorkflowStageCouncil, Council: &CouncilStagePolicy{
			Participants: []WorkflowActorSelector{{Kind: WorkflowSelectorHuman, ID: "human-reviewer"}}, Quorum: 1, ApprovalThreshold: 1, DeadlineMinutes: 60,
		}},
		ResolvedParticipants: []ActorRef{{Kind: ActorHuman, ID: "human-reviewer"}}, Status: WorkflowStageActive,
		DeadlineAt: testRepositoryNow.Add(time.Hour), CreatedAt: testRepositoryNow, ActivatedAt: testRepositoryNow,
	}}
	fixture.tasks = nil
	fixture.instance.CurrentStageKey = "council"
	if err := repo.CreateDecisionWorkflow(ctx, fixture.decision, fixture.caseRecord, fixture.instance, fixture.stages, fixture.tasks, fixture.audit, nil); err != nil {
		t.Fatal(err)
	}
	workflow := NewWorkflowService(WorkflowServiceOptions{Repository: repo, IDs: &sequenceHILIDs{}})
	result, err := workflow.SweepDue(ctx, "tenant-1", fixture.stages[0].DeadlineAt.Add(time.Minute), 20)
	if err != nil || result.Expired != 1 {
		t.Fatalf("council expiry sweep=%+v err=%v", result, err)
	}
	detail, err := repo.GetWorkflowInstance(ctx, "tenant-1", fixture.instance.ID)
	if err != nil || detail.Instance.Status != WorkflowExpired || detail.Stages[0].Status != WorkflowStageExpired || len(detail.Receipts) != 1 || detail.Receipts[0].Effect != EffectDeny {
		t.Fatalf("expired council detail=%+v err=%v", detail, err)
	}
}

func TestWorkflowServiceCouncilRequiresContributionAndHandsOffToApproval(t *testing.T) {
	ctx := context.Background()
	repo, closeStore := openWorkflowRepository(t, "workflow-service-council")
	defer closeStore()
	installWorkflowRepositoryTemplate(t, ctx, repo)
	fixture := workflowRepositoryInstanceFixture()
	fixture.stages = []WorkflowStageInstance{
		{
			ID: "stage-council", TenantID: "tenant-1", WorkflowInstanceID: fixture.instance.ID,
			Definition: WorkflowStageDefinition{Key: "council", Name: "Council", Order: 1, Type: WorkflowStageCouncil, Council: &CouncilStagePolicy{
				Participants: []WorkflowActorSelector{{Kind: WorkflowSelectorHuman, ID: "human-advisor"}}, Quorum: 1, ApprovalThreshold: 1, DeadlineMinutes: 60,
			}},
			ResolvedParticipants: []ActorRef{{Kind: ActorHuman, ID: "human-advisor"}}, Status: WorkflowStageActive,
			DeadlineAt: testRepositoryNow.Add(time.Hour), EscalationIndex: -1, CreatedAt: testRepositoryNow, ActivatedAt: testRepositoryNow,
		},
		fixture.stages[1],
	}
	fixture.stages[1].Definition.Order = 2
	fixture.tasks = fixture.tasks[1:]
	fixture.instance.CurrentStageKey = "council"
	if err := repo.CreateDecisionWorkflow(ctx, fixture.decision, fixture.caseRecord, fixture.instance, fixture.stages, fixture.tasks, fixture.audit, nil); err != nil {
		t.Fatal(err)
	}
	workflow := NewWorkflowService(WorkflowServiceOptions{Repository: repo, IDs: &sequenceHILIDs{}, Clock: func() time.Time { return testRepositoryNow.Add(time.Minute) }})
	if _, err := workflow.CastCouncilVote(ctx, "tenant-1", "stage-council", ActorRef{Kind: ActorHuman, ID: "human-advisor"}, CastVoteInput{
		Choice: VoteApprove, IdempotencyKey: "vote-without-opinion",
	}); !errors.Is(err, ErrContributionRequired) {
		t.Fatalf("vote without contribution err=%v, want ErrContributionRequired", err)
	}
	if _, err := workflow.SubmitCouncilContribution(ctx, "tenant-1", "stage-council", ActorRef{Kind: ActorHuman, ID: "human-advisor"}, SubmitContributionInput{
		Position: "approve", Reasoning: "the revised evidence supports the claim", IdempotencyKey: "council-opinion",
	}); err != nil {
		t.Fatalf("SubmitCouncilContribution: %v", err)
	}
	detail, err := workflow.CastCouncilVote(ctx, "tenant-1", "stage-council", ActorRef{Kind: ActorHuman, ID: "human-advisor"}, CastVoteInput{
		Choice: VoteApprove, Reason: "recommend formal approval", IdempotencyKey: "council-vote",
	})
	if err != nil {
		t.Fatalf("CastCouncilVote: %v", err)
	}
	if detail.Instance.Status != WorkflowRunning || detail.Instance.CurrentStageKey != "final" || detail.Instance.Version != 2 || detail.Stages[0].Status != WorkflowStagePassed || detail.Stages[1].Status != WorkflowStageActive || detail.Tasks[0].Status != TaskPending {
		t.Fatalf("council handoff detail = %+v", detail)
	}
	if len(detail.Receipts) != 1 || detail.Receipts[0].Effect != EffectRequiresApproval || detail.Receipts[0].SingleUse {
		t.Fatalf("council receipt = %+v", detail.Receipts)
	}
	council, err := repo.GetCouncil(ctx, "tenant-1", "stage-council")
	if err != nil || council.Status != CouncilPassed {
		t.Fatalf("resolved council=%+v err=%v", council, err)
	}
	approved, err := workflow.DecideTask(ctx, "tenant-1", "task-final", ActorRef{Kind: ActorHuman, ID: "human-final"}, DecideWorkflowTaskInput{
		Decision: WorkflowTaskApprove, IdempotencyKey: "approve-after-council", ExpectedInstanceVersion: 2,
	})
	if err != nil || approved.Instance.Status != WorkflowApproved || len(approved.Receipts) != 2 || approved.Receipts[1].Effect != EffectAuthorize {
		t.Fatalf("final approval detail=%+v err=%v", approved, err)
	}
}

func workflowServiceDecisionFixture() DecisionRequest {
	return DecisionRequest{
		ID: "decision-workflow", TenantID: "tenant-1", Requester: ActorRef{Kind: ActorAgent, ID: "jordan"},
		Action: "runtime.approval", ActionTypeID: "tool_use", Resource: ObjectRef{Kind: "runtime_approval", ID: "approval-1"},
		ArgsHash: "sha256:args", RiskLevel: RiskHigh, Route: RouteDecision{Mode: ModeApproval}, Status: DecisionPending,
		PayloadPreview: map[string]any{"approval_tool_name": "mcp_marketing_marketing_wechat_draft_create", "material_hash": "sha256:material"},
		IdempotencyKey: "decision-workflow", RequestFingerprint: "decision-workflow-fingerprint", CreatedAt: testRepositoryNow, ExpiresAt: testRepositoryNow.Add(time.Hour),
	}
}

func installDistinctApprovalTemplate(t *testing.T, ctx context.Context, workflow *WorkflowService, tenantID, actionTypeID string) {
	t.Helper()
	definition := WorkflowDefinition{
		RiskLevels: []RiskLevel{RiskHigh, RiskCritical}, RequireDistinctApprovers: true,
		Stages: []WorkflowStageDefinition{
			{
				Key: "manager-review", Name: "Manager review", Order: 1, Type: WorkflowStageApproval,
				Approval: &ApprovalStagePolicy{
					Strategy: ApprovalSingle, Assignees: []WorkflowActorSelector{{Kind: WorkflowSelectorRequesterManager}}, DeadlineMinutes: 60,
				},
			},
			{
				Key: "control-owner-review", Name: "Control owner review", Order: 2, Type: WorkflowStageApproval,
				Approval: &ApprovalStagePolicy{
					Strategy: ApprovalAny, Assignees: []WorkflowActorSelector{{Kind: WorkflowSelectorPeopleRole, ID: "approval.reviewer"}}, DeadlineMinutes: 60,
				},
			},
		},
	}
	template, err := workflow.SaveTemplateDraft(ctx, ActorRef{Kind: ActorHuman, ID: "admin"}, SaveTemplateDraftInput{
		ID: "template-distinct", TenantID: tenantID, Name: "Distinct approval", Definition: definition, IdempotencyKey: "save-distinct-template",
	})
	if err != nil {
		t.Fatalf("SaveTemplateDraft: %v", err)
	}
	if _, err := workflow.PublishTemplate(ctx, ActorRef{Kind: ActorHuman, ID: "admin"}, PublishTemplateInput{
		Template: template, IdempotencyKey: "publish-distinct-template", Bindings: []WorkflowTemplateBinding{{
			ID: "binding-distinct", TenantID: tenantID, ActionTypeID: actionTypeID, RiskLevel: RiskHigh, TemplateID: template.ID, Active: true,
		}},
	}); err != nil {
		t.Fatalf("PublishTemplate: %v", err)
	}
}
