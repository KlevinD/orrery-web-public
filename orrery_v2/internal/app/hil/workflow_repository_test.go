package hil

import (
	"context"
	"errors"
	"testing"
	"time"

	"orrery_v2/internal/platform/migration"
)

func TestWorkflowRepositoryPublishesAndResolvesExactBinding(t *testing.T) {
	ctx := context.Background()
	repo, closeStore := openWorkflowRepository(t, "workflow-template")
	defer closeStore()
	template, version, binding := workflowRepositoryTemplateFixture()
	if err := repo.SaveTemplateDraft(ctx, template, "template-draft-1", "fingerprint-draft-1"); err != nil {
		t.Fatalf("SaveTemplateDraft: %v", err)
	}
	if _, err := repo.PublishTemplate(ctx, template, version, []WorkflowTemplateBinding{binding}, "publish-template-1", "publish-fingerprint-1"); err != nil {
		t.Fatalf("PublishTemplate: %v", err)
	}
	if replayed, err := repo.PublishTemplate(ctx, template, version, []WorkflowTemplateBinding{binding}, "publish-template-1", "publish-fingerprint-1"); err != nil || replayed.ID != version.ID {
		t.Fatalf("idempotent PublishTemplate: %v", err)
	}

	resolved, err := repo.ResolveTemplate(ctx, WorkflowTemplateSelector{
		TenantID: "tenant-1", ActionTypeID: "tool_use", ToolName: "mcp_marketing_marketing_wechat_draft_create", RiskLevel: RiskHigh,
	})
	if err != nil {
		t.Fatalf("ResolveTemplate: %v", err)
	}
	if resolved.ID != version.ID || resolved.DefinitionHash != version.DefinitionHash || resolved.Definition.Stages[0].Key != "review" {
		t.Fatalf("resolved template = %+v", resolved)
	}
	stored, err := repo.GetWorkflowTemplate(ctx, "tenant-1", template.ID)
	if err != nil || stored.ID != template.ID || stored.Status != WorkflowTemplateActive || stored.LatestPublishedVersion != 1 || stored.DraftDefinition.Stages[0].Key != "review" {
		t.Fatalf("stored template = %+v err=%v", stored, err)
	}
	templates, err := repo.ListTemplates(ctx, WorkflowTemplateListQuery{TenantID: "tenant-1"})
	if err != nil || len(templates) != 1 || templates[0].LatestPublishedVersion != 1 || templates[0].Status != WorkflowTemplateActive {
		t.Fatalf("templates = %+v err=%v", templates, err)
	}

	changed := version
	changed.DefinitionHash = "sha256:changed"
	if _, err := repo.PublishTemplate(ctx, template, changed, nil, "publish-template-changed", "publish-fingerprint-changed"); !errors.Is(err, ErrConflict) {
		t.Fatalf("mutated published version error = %v, want ErrConflict", err)
	}
	if err := repo.DisableTemplate(ctx, "tenant-1", template.ID, ActorRef{Kind: ActorHuman, ID: "admin"}, "retired"); err != nil {
		t.Fatalf("DisableTemplate: %v", err)
	}
	if _, err := repo.ResolveTemplate(ctx, WorkflowTemplateSelector{
		TenantID: "tenant-1", ActionTypeID: "tool_use", ToolName: binding.ToolName, RiskLevel: RiskHigh,
	}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("disabled ResolveTemplate error = %v, want ErrNotFound", err)
	}
}

func TestWorkflowRepositoryResolutionPrioritizesTenantThenRisk(t *testing.T) {
	ctx := context.Background()
	repo, closeStore := openWorkflowRepository(t, "workflow-resolution-priority")
	defer closeStore()

	actionTypeID := "system.command.execute"
	installWorkflowResolutionTemplate(t, ctx, repo, BuiltInWorkflowTenantID, "system-generic", "system-generic", actionTypeID, "", "")
	installWorkflowResolutionTemplate(t, ctx, repo, BuiltInWorkflowTenantID, "system-tool", "system-tool", actionTypeID, "exec_command", "")
	installWorkflowResolutionTemplate(t, ctx, repo, BuiltInWorkflowTenantID, "system-high", "system-high", actionTypeID, "", RiskHigh)
	installWorkflowResolutionTemplate(t, ctx, repo, "tenant-1", "tenant-generic", "tenant-generic", actionTypeID, "", "")

	selector := WorkflowTemplateSelector{
		TenantID: "tenant-1", ActionTypeID: actionTypeID, ToolName: "exec_command", RiskLevel: RiskHigh,
	}
	resolved, err := repo.ResolveTemplate(ctx, selector)
	if err != nil {
		t.Fatalf("ResolveTemplate tenant override: %v", err)
	}
	if resolved.TemplateID != "tenant-generic" {
		t.Fatalf("tenant resolution selected %q, want tenant-generic", resolved.TemplateID)
	}

	if err := repo.DisableTemplate(ctx, "tenant-1", "tenant-generic", ActorRef{Kind: ActorHuman, ID: "admin"}, "test system fallback"); err != nil {
		t.Fatalf("DisableTemplate tenant override: %v", err)
	}
	resolved, err = repo.ResolveTemplate(ctx, selector)
	if err != nil {
		t.Fatalf("ResolveTemplate system fallback: %v", err)
	}
	if resolved.TemplateID != "system-high" {
		t.Fatalf("system resolution selected %q, want exact-risk system-high", resolved.TemplateID)
	}
}

func TestWorkflowRepositoryCreatesDecisionAndFrozenInstanceAtomically(t *testing.T) {
	ctx := context.Background()
	repo, closeStore := openWorkflowRepository(t, "workflow-create")
	defer closeStore()
	installWorkflowRepositoryTemplate(t, ctx, repo)
	fixture := workflowRepositoryInstanceFixture()
	if err := repo.CreateDecisionWorkflow(ctx, fixture.decision, fixture.caseRecord, fixture.instance, fixture.stages, fixture.tasks, fixture.audit, fixture.notifications); err != nil {
		t.Fatalf("CreateDecisionWorkflow: %v", err)
	}
	if err := repo.CreateDecisionWorkflow(ctx, fixture.decision, fixture.caseRecord, fixture.instance, fixture.stages, fixture.tasks, fixture.audit, fixture.notifications); err != nil {
		t.Fatalf("idempotent CreateDecisionWorkflow: %v", err)
	}

	detail, err := repo.GetWorkflowInstance(ctx, "tenant-1", fixture.instance.ID)
	if err != nil {
		t.Fatalf("GetWorkflowInstance: %v", err)
	}
	if detail.Instance.DefinitionHash != "sha256:definition" || detail.Instance.MaterialHash != "sha256:material" || detail.Instance.Version != 1 {
		t.Fatalf("instance = %+v", detail.Instance)
	}
	if len(detail.Stages) != 2 || detail.Stages[0].Status != WorkflowStageActive || detail.Stages[1].Status != WorkflowStageScheduled {
		t.Fatalf("stages = %+v", detail.Stages)
	}
	if len(detail.Stages[0].ResolvedEscalationActors) != 1 || detail.Stages[0].ResolvedEscalationActors[0].ID != "human-manager" {
		t.Fatalf("frozen escalation actors = %+v", detail.Stages[0].ResolvedEscalationActors)
	}
	if len(detail.Tasks) != 2 || detail.Tasks[0].CurrentAssignee != (ActorRef{Kind: ActorHuman, ID: "human-reviewer"}) || detail.Tasks[1].Status != TaskScheduled {
		t.Fatalf("tasks = %+v", detail.Tasks)
	}
	if len(detail.Audit) != 1 || len(detail.Notifications) != 1 {
		t.Fatalf("audit=%+v notifications=%+v", detail.Audit, detail.Notifications)
	}
	if detail.Receipts == nil {
		t.Fatal("receipts are nil, want an empty slice")
	}
	page, err := repo.ListWorkflowInstances(ctx, WorkflowListQuery{TenantID: "tenant-1", ActionableBy: ActorRef{Kind: ActorHuman, ID: "human-reviewer"}})
	if err != nil || page.Total != 1 || len(page.Items) != 1 {
		t.Fatalf("workflow page = %+v err=%v", page, err)
	}
	for _, lookup := range []struct {
		tenant, decision string
		total            int
	}{
		{"tenant-1", fixture.decision.ID, 1}, {"tenant-1", "unknown-decision", 0}, {"other-tenant", fixture.decision.ID, 0},
	} {
		page, err := repo.ListWorkflowInstances(ctx, WorkflowListQuery{TenantID: lookup.tenant, DecisionRequestID: lookup.decision})
		if err != nil || page.Total != lookup.total {
			t.Fatalf("decision lookup %+v = %+v, %v", lookup, page, err)
		}
	}
	if _, err := repo.store.Exec(ctx, `UPDATE hil_decision_requests SET status = ? WHERE tenant_id = ? AND id = ?`, DecisionDenied, fixture.decision.TenantID, fixture.decision.ID); err != nil {
		t.Fatalf("deny decision: %v", err)
	}
	page, err = repo.ListWorkflowInstances(ctx, WorkflowListQuery{TenantID: "tenant-1", ActionableBy: ActorRef{Kind: ActorHuman, ID: "human-reviewer"}})
	if err != nil || page.Total != 0 || len(page.Items) != 0 {
		t.Fatalf("non-pending decision remained actionable: page=%+v err=%v", page, err)
	}

	conflict := fixture
	conflict.decision.RequestFingerprint = "different"
	if err := repo.CreateDecisionWorkflow(ctx, conflict.decision, conflict.caseRecord, conflict.instance, conflict.stages, conflict.tasks, conflict.audit, conflict.notifications); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("conflicting create error = %v, want ErrIdempotencyConflict", err)
	}
}

func TestWorkflowRepositoryRejectsInvalidAggregateBeforeWritingDecision(t *testing.T) {
	ctx := context.Background()
	repo, closeStore := openWorkflowRepository(t, "workflow-invalid")
	defer closeStore()
	installWorkflowRepositoryTemplate(t, ctx, repo)
	fixture := workflowRepositoryInstanceFixture()
	fixture.stages[1].Status = WorkflowStageActive
	if err := repo.CreateDecisionWorkflow(ctx, fixture.decision, fixture.caseRecord, fixture.instance, fixture.stages, fixture.tasks, fixture.audit, fixture.notifications); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("CreateDecisionWorkflow error = %v, want ErrInvalidRequest", err)
	}
	if _, err := repo.GetDecision(ctx, fixture.decision.TenantID, fixture.decision.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("decision after rejected aggregate error = %v, want ErrNotFound", err)
	}
}

func TestWorkflowRepositoryAppliesCommandWithOptimisticLockAndOneReceipt(t *testing.T) {
	ctx := context.Background()
	repo, closeStore := openWorkflowRepository(t, "workflow-command")
	defer closeStore()
	installWorkflowRepositoryTemplate(t, ctx, repo)
	fixture := workflowRepositoryInstanceFixture()
	if err := repo.CreateDecisionWorkflow(ctx, fixture.decision, fixture.caseRecord, fixture.instance, fixture.stages, fixture.tasks, fixture.audit, fixture.notifications); err != nil {
		t.Fatal(err)
	}
	completedAt := testRepositoryNow.Add(time.Minute)
	task := fixture.tasks[0]
	task.Status = TaskApproved
	task.Decision = WorkflowTaskApprove
	task.CompletedAt = completedAt
	stage := fixture.stages[0]
	stage.Status = WorkflowStagePassed
	stage.ResolvedAt = completedAt
	command := WorkflowCommand{
		TenantID: "tenant-1", InstanceID: fixture.instance.ID, ExpectedInstanceVersion: 1,
		Status: WorkflowApproved, CurrentStageKey: "", UpdatedAt: completedAt,
		Task: &task, Stages: []WorkflowStageInstance{stage}, DecisionStatus: DecisionResolved,
		Receipt: &DecisionReceipt{
			ID: "workflow-receipt-1", TenantID: "tenant-1", RequestID: fixture.decision.ID,
			WorkflowInstanceID: fixture.instance.ID, ArgsHash: fixture.instance.ArgsHash, MaterialHash: fixture.instance.MaterialHash,
			Effect: EffectAuthorize, ResolverActorKind: ActorHuman, ResolverActorID: "human-reviewer", SingleUse: true, CreatedAt: completedAt,
		},
		Audit: []WorkflowAuditEvent{{
			ID: "audit-approved", TenantID: "tenant-1", CaseID: fixture.caseRecord.ID, WorkflowInstanceID: fixture.instance.ID,
			StageInstanceID: stage.ID, TaskID: task.ID, Actor: ActorRef{Kind: ActorHuman, ID: "human-reviewer"}, EventType: "task.approved",
			IdempotencyKey: "approve-command", RequestFingerprint: "approve-fingerprint", CreatedAt: completedAt,
		}},
		IdempotencyKey: "approve-command", RequestFingerprint: "approve-fingerprint",
	}
	detail, err := repo.ApplyWorkflowCommand(ctx, command)
	if err != nil {
		t.Fatalf("ApplyWorkflowCommand: %v", err)
	}
	if detail.Instance.Status != WorkflowApproved || detail.Instance.Version != 2 || detail.Tasks[0].Status != TaskApproved || len(detail.Receipts) != 1 {
		t.Fatalf("completed detail = %+v", detail)
	}
	replayed, err := repo.ApplyWorkflowCommand(ctx, command)
	if err != nil || replayed.Instance.Version != 2 || len(replayed.Receipts) != 1 {
		t.Fatalf("replayed command detail=%+v err=%v", replayed, err)
	}
	changed := command
	changed.RequestFingerprint = "changed-fingerprint"
	if _, err := repo.ApplyWorkflowCommand(ctx, changed); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("changed command error = %v, want ErrIdempotencyConflict", err)
	}
	stale := command
	stale.IdempotencyKey = "stale-command"
	stale.RequestFingerprint = "stale-fingerprint"
	if _, err := repo.ApplyWorkflowCommand(ctx, stale); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale command error = %v, want ErrConflict", err)
	}
}

func TestWorkflowRepositoryRecordsViewedWithoutChangingInstance(t *testing.T) {
	ctx := context.Background()
	repo, closeStore := openWorkflowRepository(t, "workflow-viewed")
	defer closeStore()
	installWorkflowRepositoryTemplate(t, ctx, repo)
	fixture := workflowRepositoryInstanceFixture()
	if err := repo.CreateDecisionWorkflow(ctx, fixture.decision, fixture.caseRecord, fixture.instance, fixture.stages, fixture.tasks, fixture.audit, fixture.notifications); err != nil {
		t.Fatal(err)
	}
	actor := ActorRef{Kind: ActorHuman, ID: "human-reviewer"}
	if err := repo.RecordWorkflowViewed(ctx, "tenant-1", fixture.instance.ID, actor, "view-1", "view-fingerprint"); err != nil {
		t.Fatalf("RecordWorkflowViewed: %v", err)
	}
	if err := repo.RecordWorkflowViewed(ctx, "tenant-1", fixture.instance.ID, actor, "view-1", "view-fingerprint"); err != nil {
		t.Fatalf("idempotent RecordWorkflowViewed: %v", err)
	}
	if err := repo.RecordWorkflowViewed(ctx, "tenant-1", fixture.instance.ID, ActorRef{Kind: ActorHuman, ID: "human-admin"}, "view-1", "other-viewer-fingerprint"); err != nil {
		t.Fatalf("other viewer replay RecordWorkflowViewed: %v", err)
	}
	if err := repo.RecordWorkflowViewed(ctx, "tenant-1", fixture.instance.ID, actor, "view-2", "new-revision-fingerprint"); err != nil {
		t.Fatalf("new revision RecordWorkflowViewed: %v", err)
	}
	if err := repo.RecordWorkflowViewed(ctx, "tenant-1", "other-workflow", actor, "view-1", "other-workflow-fingerprint"); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("other workflow view error = %v, want ErrIdempotencyConflict", err)
	}
	detail, err := repo.GetWorkflowInstance(ctx, "tenant-1", fixture.instance.ID)
	if err != nil || detail.Instance.Version != 1 || len(detail.Audit) != 2 {
		t.Fatalf("viewed detail = %+v err=%v", detail, err)
	}
	if detail.Audit[1].Actor != actor {
		t.Fatalf("viewed actor = %+v, want first viewer %+v", detail.Audit[1].Actor, actor)
	}
}

func TestWorkflowRepositoryClaimsAndCompletesNotificationWithReceipt(t *testing.T) {
	ctx := context.Background()
	repo, closeStore := openWorkflowRepository(t, "workflow-notification")
	defer closeStore()
	installWorkflowRepositoryTemplate(t, ctx, repo)
	fixture := workflowRepositoryInstanceFixture()
	if err := repo.CreateDecisionWorkflow(ctx, fixture.decision, fixture.caseRecord, fixture.instance, fixture.stages, fixture.tasks, fixture.audit, fixture.notifications); err != nil {
		t.Fatal(err)
	}
	claimed, err := repo.ClaimWorkflowNotifications(ctx, "tenant-1", testRepositoryNow, 10)
	if err != nil || len(claimed) != 1 || claimed[0].Status != WorkflowNotificationProcessing {
		t.Fatalf("claimed notifications = %+v err=%v", claimed, err)
	}
	recovered, err := repo.ClaimWorkflowNotifications(ctx, "tenant-1", testRepositoryNow.Add(6*time.Minute), 10)
	if err != nil || len(recovered) != 1 || recovered[0].AttemptCount != claimed[0].AttemptCount+1 {
		t.Fatalf("recovered stale processing notification = %+v err=%v", recovered, err)
	}
	claimed = recovered
	if err := repo.CompleteWorkflowNotification(ctx, WorkflowNotificationResult{
		TenantID: "tenant-1", NotificationID: claimed[0].ID, Pending: true,
		ProviderReceipt: "gwd_pending", NextAttemptAt: testRepositoryNow.Add(7 * time.Minute), CompletedAt: testRepositoryNow.Add(6 * time.Minute),
	}); err != nil {
		t.Fatalf("CompleteWorkflowNotification pending: %v", err)
	}
	claimed, err = repo.ClaimWorkflowNotifications(ctx, "tenant-1", testRepositoryNow.Add(7*time.Minute), 10)
	if err != nil || len(claimed) != 1 || claimed[0].ProviderReceipt != "gwd_pending" {
		t.Fatalf("reclaimed pending notifications = %+v err=%v", claimed, err)
	}
	if err := repo.CompleteWorkflowNotification(ctx, WorkflowNotificationResult{
		TenantID: "tenant-1", NotificationID: claimed[0].ID, Delivered: true, CompletedAt: testRepositoryNow,
	}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("receipt-less completion error = %v, want ErrInvalidRequest", err)
	}
	if err := repo.CompleteWorkflowNotification(ctx, WorkflowNotificationResult{
		TenantID: "tenant-1", NotificationID: claimed[0].ID, Delivered: true,
		ProviderReceipt: "om_message_1", CompletedAt: testRepositoryNow,
	}); err != nil {
		t.Fatalf("CompleteWorkflowNotification: %v", err)
	}
	detail, err := repo.GetWorkflowInstance(ctx, "tenant-1", fixture.instance.ID)
	if err != nil || detail.Notifications[0].Status != WorkflowNotificationDelivered || detail.Notifications[0].ProviderReceipt != "om_message_1" {
		t.Fatalf("completed notifications = %+v err=%v", detail.Notifications, err)
	}
	due, err := repo.ClaimDueWorkflowWork(ctx, "tenant-1", testRepositoryNow.Add(2*time.Hour), 10)
	if err != nil || len(due) != 1 || due[0].Stage.ID != fixture.stages[0].ID {
		t.Fatalf("due work = %+v err=%v", due, err)
	}
}

func TestWorkflowNotificationTerminalHoldAndCancellationStopClaims(t *testing.T) {
	for _, scenario := range []string{"cancelled", "needs_attention"} {
		t.Run(scenario, func(t *testing.T) {
			ctx := context.Background()
			repo, closeStore := openWorkflowRepository(t, "notification-terminal-"+scenario)
			defer closeStore()
			installWorkflowRepositoryTemplate(t, ctx, repo)
			fixture := workflowRepositoryInstanceFixture()
			if err := repo.CreateDecisionWorkflow(ctx, fixture.decision, fixture.caseRecord, fixture.instance, fixture.stages, fixture.tasks, fixture.audit, fixture.notifications); err != nil {
				t.Fatal(err)
			}
			claimed, err := repo.ClaimWorkflowNotifications(ctx, "tenant-1", testRepositoryNow, 10)
			if err != nil || len(claimed) != 1 {
				t.Fatalf("claim: %+v %v", claimed, err)
			}
			result := WorkflowNotificationResult{TenantID: "tenant-1", NotificationID: claimed[0].ID, Failure: "original diagnostic", CompletedAt: testRepositoryNow, NextAttemptAt: testRepositoryNow.Add(time.Minute), Cancelled: scenario == "cancelled", NeedsAttention: scenario == "needs_attention"}
			if err := repo.CompleteWorkflowNotification(ctx, result); err != nil {
				t.Fatal(err)
			}
			claimed, err = repo.ClaimWorkflowNotifications(ctx, "tenant-1", testRepositoryNow.Add(48*time.Hour), 10)
			if err != nil || len(claimed) != 0 {
				t.Fatalf("terminal notice reclaimed: %+v %v", claimed, err)
			}
			detail, err := repo.GetWorkflowInstance(ctx, "tenant-1", fixture.instance.ID)
			if err != nil || string(detail.Notifications[0].Status) != scenario || !detail.Notifications[0].DeliveredAt.IsZero() || !detail.Notifications[0].NextAttemptAt.IsZero() {
				t.Fatalf("false delivery or retry: %+v %v", detail.Notifications, err)
			}
		})
	}
}

type workflowRepositoryFixture struct {
	decision      DecisionRequest
	caseRecord    WorkflowCase
	instance      WorkflowInstance
	stages        []WorkflowStageInstance
	tasks         []WorkflowTask
	audit         []WorkflowAuditEvent
	notifications []WorkflowNotification
}

func workflowRepositoryTemplateFixture() (WorkflowTemplate, WorkflowTemplateVersion, WorkflowTemplateBinding) {
	definition := validWorkflowDefinition()
	template := WorkflowTemplate{
		ID: "template-1", TenantID: "tenant-1", Name: "Marketing review", Status: WorkflowTemplateDisabled,
		DraftDefinition: definition, CreatedBy: ActorRef{Kind: ActorHuman, ID: "admin"}, CreatedAt: testRepositoryNow, UpdatedAt: testRepositoryNow,
	}
	version := WorkflowTemplateVersion{
		ID: "template-version-1", TenantID: "tenant-1", TemplateID: template.ID, Version: 1,
		Definition: definition, DefinitionHash: "sha256:definition", PublishedBy: template.CreatedBy, PublishedAt: testRepositoryNow,
	}
	binding := WorkflowTemplateBinding{
		ID: "binding-1", TenantID: "tenant-1", ActionTypeID: "tool_use",
		ToolName: "mcp_marketing_marketing_wechat_draft_create", RiskLevel: RiskHigh,
		TemplateID: template.ID, Active: true, CreatedBy: template.CreatedBy, CreatedAt: testRepositoryNow, UpdatedAt: testRepositoryNow,
	}
	return template, version, binding
}

func installWorkflowRepositoryTemplate(t *testing.T, ctx context.Context, repo *PlatformRepository) {
	t.Helper()
	template, version, _ := workflowRepositoryTemplateFixture()
	if err := repo.SaveTemplateDraft(ctx, template, "install-template", "install-template-fingerprint"); err != nil {
		t.Fatalf("SaveTemplateDraft fixture: %v", err)
	}
	if _, err := repo.PublishTemplate(ctx, template, version, nil, "install-template-publish", "install-template-publish-fingerprint"); err != nil {
		t.Fatalf("PublishTemplate fixture: %v", err)
	}
}

func workflowRepositoryInstanceFixture() workflowRepositoryFixture {
	decision := testDecisionRequest()
	decision.Route.Mode = ModeApproval
	decision.RiskLevel = RiskHigh
	decision.IdempotencyKey = "workflow-decision-create"
	decision.RequestFingerprint = "workflow-decision-fingerprint"
	decision.PayloadPreview = map[string]any{"material_hash": "sha256:material", "approval_tool_name": "mcp_marketing_marketing_wechat_draft_create"}
	caseRecord := WorkflowCase{
		ID: "case-1", TenantID: "tenant-1", Requester: decision.Requester, ActionTypeID: decision.ActionTypeID,
		ToolName: "mcp_marketing_marketing_wechat_draft_create", Resource: decision.Resource, CreatedAt: testRepositoryNow,
	}
	instance := WorkflowInstance{
		ID: "workflow-1", TenantID: "tenant-1", CaseID: caseRecord.ID, Revision: 1,
		DecisionRequestID: decision.ID, TemplateVersionID: "template-version-1", DefinitionHash: "sha256:definition",
		MaterialHash: "sha256:material", ArgsHash: decision.ArgsHash, Status: WorkflowRunning,
		CurrentStageKey: "review", Version: 1, CreatedAt: testRepositoryNow, UpdatedAt: testRepositoryNow,
	}
	stages := []WorkflowStageInstance{
		{
			ID: "stage-review", TenantID: "tenant-1", WorkflowInstanceID: instance.ID,
			Definition:               WorkflowStageDefinition{Key: "review", Name: "Review", Order: 1, Type: WorkflowStageApproval, Approval: &ApprovalStagePolicy{Strategy: ApprovalSingle, Assignees: []WorkflowActorSelector{{Kind: WorkflowSelectorHuman, ID: "human-reviewer"}}, DeadlineMinutes: 60}},
			ResolvedParticipants:     []ActorRef{{Kind: ActorHuman, ID: "human-reviewer"}},
			ResolvedEscalationActors: []ActorRef{{Kind: ActorHuman, ID: "human-manager"}}, Status: WorkflowStageActive,
			DeadlineAt: testRepositoryNow.Add(time.Hour), CreatedAt: testRepositoryNow, ActivatedAt: testRepositoryNow,
		},
		{
			ID: "stage-final", TenantID: "tenant-1", WorkflowInstanceID: instance.ID,
			Definition:           WorkflowStageDefinition{Key: "final", Name: "Final", Order: 2, Type: WorkflowStageApproval, Approval: &ApprovalStagePolicy{Strategy: ApprovalSingle, Assignees: []WorkflowActorSelector{{Kind: WorkflowSelectorHuman, ID: "human-final"}}, DeadlineMinutes: 60}},
			ResolvedParticipants: []ActorRef{{Kind: ActorHuman, ID: "human-final"}}, Status: WorkflowStageScheduled, CreatedAt: testRepositoryNow,
		},
	}
	tasks := []WorkflowTask{
		{ID: "task-review", TenantID: "tenant-1", WorkflowInstanceID: instance.ID, StageInstanceID: stages[0].ID, SeatKey: "seat-review", OriginalAssignee: ActorRef{Kind: ActorHuman, ID: "human-reviewer"}, CurrentAssignee: ActorRef{Kind: ActorHuman, ID: "human-reviewer"}, Status: TaskPending, DeadlineAt: stages[0].DeadlineAt, CreatedAt: testRepositoryNow, UpdatedAt: testRepositoryNow},
		{ID: "task-final", TenantID: "tenant-1", WorkflowInstanceID: instance.ID, StageInstanceID: stages[1].ID, SeatKey: "seat-final", OriginalAssignee: ActorRef{Kind: ActorHuman, ID: "human-final"}, CurrentAssignee: ActorRef{Kind: ActorHuman, ID: "human-final"}, Status: TaskScheduled, CreatedAt: testRepositoryNow, UpdatedAt: testRepositoryNow},
	}
	audit := []WorkflowAuditEvent{{
		ID: "audit-created", TenantID: "tenant-1", CaseID: caseRecord.ID, WorkflowInstanceID: instance.ID,
		Actor: decision.Requester, EventType: "workflow.created", IdempotencyKey: "workflow-created", RequestFingerprint: "workflow-created-fingerprint", CreatedAt: testRepositoryNow,
	}}
	notifications := []WorkflowNotification{{
		ID: "notification-1", TenantID: "tenant-1", WorkflowInstanceID: instance.ID, StageInstanceID: stages[0].ID, TaskID: tasks[0].ID,
		Channel: "in_app", Recipient: tasks[0].CurrentAssignee, EventType: "task.assigned", Status: WorkflowNotificationPending,
		NextAttemptAt: testRepositoryNow, IdempotencyKey: "notify-task-review", CreatedAt: testRepositoryNow, UpdatedAt: testRepositoryNow,
	}}
	return workflowRepositoryFixture{decision: decision, caseRecord: caseRecord, instance: instance, stages: stages, tasks: tasks, audit: audit, notifications: notifications}
}

func openWorkflowRepository(t *testing.T, name string) (*PlatformRepository, func()) {
	t.Helper()
	store := openHILTestStore(t, name)
	if err := migration.NewRunner([]migration.Migration{hilDecisionMigration(), hilWorkflowMigration()}).Up(context.Background(), store); err != nil {
		store.Close()
		t.Fatalf("migration up: %v", err)
	}
	return NewPlatformRepository(store), func() { _ = store.Close() }
}

func installWorkflowResolutionTemplate(
	t *testing.T,
	ctx context.Context,
	repo *PlatformRepository,
	tenantID, templateID, stageKey, actionTypeID, toolName string,
	risk RiskLevel,
) {
	t.Helper()
	definition := validWorkflowDefinition()
	definition.RiskLevels = []RiskLevel{RiskLow, RiskMedium, RiskHigh, RiskCritical}
	definition.Stages[0].Key = stageKey
	definition.Stages[0].Name = stageKey
	actor := ActorRef{Kind: ActorService, ID: "test-bootstrap"}
	template := WorkflowTemplate{
		ID: templateID, TenantID: tenantID, Name: templateID, Status: WorkflowTemplateDisabled,
		DraftDefinition: definition, CreatedBy: actor, CreatedAt: testRepositoryNow, UpdatedAt: testRepositoryNow,
	}
	version := WorkflowTemplateVersion{
		ID: templateID + "-version", TenantID: tenantID, TemplateID: templateID, Version: 1,
		Definition: definition, DefinitionHash: "sha256:" + templateID, PublishedBy: actor, PublishedAt: testRepositoryNow,
	}
	binding := WorkflowTemplateBinding{
		ID: templateID + "-binding", TenantID: tenantID, ActionTypeID: actionTypeID,
		ToolName: toolName, RiskLevel: risk, TemplateID: templateID, Active: true,
		CreatedBy: actor, CreatedAt: testRepositoryNow, UpdatedAt: testRepositoryNow,
	}
	if err := repo.SaveTemplateDraft(ctx, template, "save:"+templateID, "save-fingerprint:"+templateID); err != nil {
		t.Fatalf("SaveTemplateDraft %s: %v", templateID, err)
	}
	if _, err := repo.PublishTemplate(ctx, template, version, []WorkflowTemplateBinding{binding}, "publish:"+templateID, "publish-fingerprint:"+templateID); err != nil {
		t.Fatalf("PublishTemplate %s: %v", templateID, err)
	}
}
