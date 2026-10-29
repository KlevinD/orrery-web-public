package main

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/actioncontract"
	"orrery_v2/internal/app/hil"
	gwoutbound "orrery_v2/internal/gateway/outbound"
	"orrery_v2/internal/platform"
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/testkit"
)

type recordingProductionHILTemplateAdmin struct {
	template     hil.WorkflowTemplate
	getErr       error
	saved        []hil.SaveTemplateDraftInput
	published    []hil.PublishTemplateInput
	bindings     []hil.PutTemplateBindingInput
	saveActor    hil.ActorRef
	pubActor     hil.ActorRef
	bindingActor hil.ActorRef
}

func (a *recordingProductionHILTemplateAdmin) GetTemplate(context.Context, string, string) (hil.WorkflowTemplate, error) {
	return a.template, a.getErr
}

func (a *recordingProductionHILTemplateAdmin) SaveTemplateDraft(_ context.Context, actor hil.ActorRef, input hil.SaveTemplateDraftInput) (hil.WorkflowTemplate, error) {
	a.saveActor = actor
	a.saved = append(a.saved, input)
	return hil.WorkflowTemplate{
		ID: input.ID, TenantID: input.TenantID, Name: input.Name, Description: input.Description,
		Status: input.Status, DraftDefinition: input.Definition, LatestPublishedVersion: input.LatestPublishedVersion,
		CreatedBy: actor,
	}, nil
}

func (a *recordingProductionHILTemplateAdmin) PublishTemplate(_ context.Context, actor hil.ActorRef, input hil.PublishTemplateInput) (hil.WorkflowTemplateVersion, error) {
	a.pubActor = actor
	a.published = append(a.published, input)
	return hil.WorkflowTemplateVersion{ID: "version-1", TemplateID: input.Template.ID, Version: 1}, nil
}

func (a *recordingProductionHILTemplateAdmin) PutTemplateBinding(_ context.Context, actor hil.ActorRef, input hil.PutTemplateBindingInput) (hil.WorkflowTemplateBinding, error) {
	a.bindingActor = actor
	a.bindings = append(a.bindings, input)
	return input.Binding, nil
}

func TestEnsureProductionMarketingWorkflowInstallsFixedHumanApproval(t *testing.T) {
	admin := &recordingProductionHILTemplateAdmin{getErr: hil.ErrNotFound}
	if err := ensureProductionMarketingWorkflow(context.Background(), admin); err != nil {
		t.Fatalf("ensureProductionMarketingWorkflow: %v", err)
	}
	if admin.saveActor != (hil.ActorRef{Kind: hil.ActorService, ID: productionHILBootstrapActorID}) || admin.pubActor != admin.saveActor {
		t.Fatalf("bootstrap actors save=%+v publish=%+v", admin.saveActor, admin.pubActor)
	}
	if len(admin.saved) != 1 || len(admin.published) != 1 {
		t.Fatalf("saved=%+v published=%+v", admin.saved, admin.published)
	}
	definition := admin.saved[0].Definition
	if len(definition.Stages) != 1 || definition.Stages[0].Approval == nil || definition.Stages[0].Approval.Strategy != hil.ApprovalSingle {
		t.Fatalf("definition = %+v", definition)
	}
	assignees := definition.Stages[0].Approval.Assignees
	if len(assignees) != 1 || assignees[0] != (hil.WorkflowActorSelector{Kind: hil.WorkflowSelectorHuman, ID: productionMarketingApproverID}) {
		t.Fatalf("assignees = %+v", assignees)
	}
	if definition.Stages[0].Approval.DeadlineMinutes != 7*24*60 {
		t.Fatalf("deadline minutes = %d, want %d", definition.Stages[0].Approval.DeadlineMinutes, 7*24*60)
	}
	reminders := definition.Stages[0].Approval.ReminderOffsetsMinutes
	if len(reminders) != 1 || reminders[0] != 24*60 {
		t.Fatalf("reminder offsets = %+v, want [1440]", reminders)
	}
	bindings := admin.published[0].Bindings
	if len(bindings) != 1 || bindings[0].ActionTypeID != hil.MarketingWechatDraftApprovalActionType || bindings[0].ToolName != "" || bindings[0].RiskLevel != "" || !bindings[0].Active {
		t.Fatalf("bindings = %+v", bindings)
	}
}

func TestEnsureProductionApprovalWorkflowsInstallsSeparateDefaults(t *testing.T) {
	admin := &recordingProductionHILTemplateAdmin{getErr: hil.ErrNotFound}
	if err := ensureProductionApprovalWorkflows(context.Background(), admin); err != nil {
		t.Fatalf("ensureProductionApprovalWorkflows: %v", err)
	}
	bootstrapActor := hil.ActorRef{Kind: hil.ActorService, ID: productionHILBootstrapActorID}
	if admin.saveActor != bootstrapActor || admin.pubActor != bootstrapActor || admin.bindingActor != bootstrapActor {
		t.Fatalf("bootstrap actors save=%+v publish=%+v binding=%+v", admin.saveActor, admin.pubActor, admin.bindingActor)
	}
	if len(admin.saved) != 5 || len(admin.published) != 5 || len(admin.bindings) != 27 {
		t.Fatalf("saved=%d published=%d bindings=%d", len(admin.saved), len(admin.published), len(admin.bindings))
	}
	templates := map[string]hil.SaveTemplateDraftInput{}
	for _, saved := range admin.saved {
		templates[saved.ID] = saved
	}
	for _, templateID := range []string{
		productionMarketingWorkflowTemplateID,
		productionBusinessWorkflowTemplateID,
		productionExternalEmailWorkflowTemplateID,
		productionCommandExecutionWorkflowTemplateID,
		productionHighRiskWorkflowTemplateID,
	} {
		if _, ok := templates[templateID]; !ok {
			t.Fatalf("template %q was not installed", templateID)
		}
	}
	bindings := map[string]hil.WorkflowTemplateBinding{}
	for _, input := range admin.bindings {
		bindings[input.Binding.ID] = input.Binding
	}
	assertProductionWorkflowBinding(t, bindings, "builtin-business-approval-binding", "business_approval", hil.BusinessApprovalToolName, productionBusinessWorkflowTemplateID)
	assertProductionWorkflowBinding(t, bindings, "builtin-external-email-approval-binding", "tool_use", "email_send", productionExternalEmailWorkflowTemplateID)
	assertProductionInactiveWorkflowBinding(t, bindings, "builtin-command-execution-approval-binding", "command_execution", "exec_command", productionCommandExecutionWorkflowTemplateID)
	assertProductionInactiveWorkflowBinding(t, bindings, "builtin-personal-assistant-shell-approval-binding", "tool_use", "personal_assistant_shell_exec", productionCommandExecutionWorkflowTemplateID)
	assertProductionWorkflowBinding(t, bindings, "builtin-marketing-wechat-draft-approval-binding", hil.MarketingWechatDraftApprovalActionType, "", productionMarketingWorkflowTemplateID)
	assertProductionInactiveWorkflowBinding(t, bindings, "builtin-marketing-wechat-draft-binding", "tool_use", marketingWechatDraftApprovalTool, productionMarketingWorkflowTemplateID)
	assertProductionInactiveWorkflowBinding(t, bindings, "builtin-marketing-wechat-canonical-binding", string(actioncontract.ActionCommunicationWechatDraftCreate), "", productionMarketingWorkflowTemplateID)
	assertProductionWorkflowBinding(t, bindings, "builtin-business-canonical-approval-binding", string(actioncontract.ActionBusinessApprovalRequest), "", productionBusinessWorkflowTemplateID)
	assertProductionWorkflowBinding(t, bindings, "builtin-external-email-canonical-binding", string(actioncontract.ActionCommunicationEmailSend), "", productionExternalEmailWorkflowTemplateID)
	assertProductionInactiveWorkflowBinding(t, bindings, "builtin-command-execution-canonical-binding", string(actioncontract.ActionSystemCommandExecute), "", productionCommandExecutionWorkflowTemplateID)
	assertProductionInactiveWorkflowBinding(t, bindings, "builtin-high-risk-command-high-binding", string(actioncontract.ActionSystemCommandExecute), "", productionHighRiskWorkflowTemplateID)

	activeBindings := 0
	for _, binding := range bindings {
		if !binding.Active {
			continue
		}
		activeBindings++
		legacyBusinessBinding := (binding.ActionTypeID == "business_approval" && binding.ToolName == hil.BusinessApprovalToolName) ||
			(binding.ActionTypeID == "tool_use" && binding.ToolName == "email_send")
		if !legacyBusinessBinding && !actioncontract.IsHumanBusinessDecisionAction(actioncontract.ActionTypeID(binding.ActionTypeID)) {
			t.Fatalf("active non-business workflow binding = %+v", binding)
		}
	}
	if activeBindings != 9 {
		t.Fatalf("active workflow bindings = %d, want 9 business bindings", activeBindings)
	}

	highRisk := templates[productionHighRiskWorkflowTemplateID].Definition
	if !highRisk.RequireDistinctApprovers || len(highRisk.Stages) != 2 || highRisk.Stages[0].Key != "manager-review" || highRisk.Stages[1].Key != "control-owner-review" {
		t.Fatalf("high-risk workflow = %+v", highRisk)
	}
	finalApproval := highRisk.Stages[1].Approval
	if finalApproval == nil || finalApproval.Strategy != hil.ApprovalAny || len(finalApproval.Assignees) != 1 ||
		finalApproval.Assignees[0] != (hil.WorkflowActorSelector{Kind: hil.WorkflowSelectorPeopleRole, ID: productionApprovalReviewerRoleID}) {
		t.Fatalf("high-risk final approval = %+v", finalApproval)
	}

	for _, templateID := range []string{productionBusinessWorkflowTemplateID, productionExternalEmailWorkflowTemplateID, productionCommandExecutionWorkflowTemplateID} {
		approval := templates[templateID].Definition.Stages[0].Approval
		if approval == nil || approval.Strategy != hil.ApprovalSingle || len(approval.Assignees) != 1 || approval.Assignees[0].Kind != hil.WorkflowSelectorRequesterManager {
			t.Fatalf("template %q manager approval = %+v", templateID, approval)
		}
		if len(approval.Escalation) != 1 || len(approval.Escalation[0].Actors) != 1 || approval.Escalation[0].Actors[0] != (hil.WorkflowActorSelector{Kind: hil.WorkflowSelectorHuman, ID: productionDefaultEscalationApproverID}) {
			t.Fatalf("template %q escalation = %+v", templateID, approval.Escalation)
		}
	}
}

func assertProductionWorkflowBinding(t *testing.T, bindings map[string]hil.WorkflowTemplateBinding, id, actionTypeID, toolName, templateID string) {
	t.Helper()
	binding, ok := bindings[id]
	if !ok || binding.ActionTypeID != actionTypeID || binding.ToolName != toolName || binding.TemplateID != templateID || !binding.Active {
		t.Fatalf("binding %q = %+v", id, binding)
	}
}

func assertProductionInactiveWorkflowBinding(t *testing.T, bindings map[string]hil.WorkflowTemplateBinding, id, actionTypeID, toolName, templateID string) {
	t.Helper()
	binding, ok := bindings[id]
	if !ok || binding.ActionTypeID != actionTypeID || binding.ToolName != toolName || binding.TemplateID != templateID || binding.Active {
		t.Fatalf("inactive binding %q = %+v", id, binding)
	}
}

func TestEnsureProductionMarketingWorkflowUpgradesLegacyBootstrapDeadline(t *testing.T) {
	admin := &recordingProductionHILTemplateAdmin{template: hil.WorkflowTemplate{
		ID: productionMarketingWorkflowTemplateID, TenantID: productionHILTenantID,
		Name: productionMarketingWorkflowTemplateName, Description: "One-person approval of immutable Marketing WeChat draft material.",
		Status: hil.WorkflowTemplateActive, LatestPublishedVersion: 1,
		CreatedBy: hil.ActorRef{Kind: hil.ActorService, ID: productionHILBootstrapActorID},
		DraftDefinition: hil.WorkflowDefinition{
			RiskLevels: []hil.RiskLevel{hil.RiskLow, hil.RiskMedium, hil.RiskHigh, hil.RiskCritical},
			Stages: []hil.WorkflowStageDefinition{{
				Key: "human-review", Name: "Human review", Order: 1, Type: hil.WorkflowStageApproval,
				Approval: &hil.ApprovalStagePolicy{
					Strategy: hil.ApprovalSingle, Assignees: []hil.WorkflowActorSelector{{Kind: hil.WorkflowSelectorHuman, ID: productionMarketingApproverID}},
					DeadlineMinutes: 10, ReminderOffsetsMinutes: []int{5},
				},
			}},
		},
	}}
	if err := ensureProductionMarketingWorkflow(context.Background(), admin); err != nil {
		t.Fatalf("ensureProductionMarketingWorkflow: %v", err)
	}
	if len(admin.saved) != 1 || len(admin.published) != 1 {
		t.Fatalf("saved=%+v published=%+v", admin.saved, admin.published)
	}
	approval := admin.saved[0].Definition.Stages[0].Approval
	if approval == nil || approval.DeadlineMinutes != 7*24*60 || len(approval.ReminderOffsetsMinutes) != 1 || approval.ReminderOffsetsMinutes[0] != 24*60 {
		t.Fatalf("upgraded approval policy = %+v", approval)
	}
	if admin.saved[0].LatestPublishedVersion != 1 || admin.published[0].Template.LatestPublishedVersion != 1 {
		t.Fatalf("published versions save=%d publish=%d", admin.saved[0].LatestPublishedVersion, admin.published[0].Template.LatestPublishedVersion)
	}
}

func TestEnsureProductionMarketingWorkflowPreservesExistingTemplate(t *testing.T) {
	admin := &recordingProductionHILTemplateAdmin{template: hil.WorkflowTemplate{
		ID: productionMarketingWorkflowTemplateID, TenantID: productionHILTenantID, Status: hil.WorkflowTemplateDisabled,
	}}
	if err := ensureProductionMarketingWorkflow(context.Background(), admin); err != nil {
		t.Fatalf("ensureProductionMarketingWorkflow: %v", err)
	}
	if len(admin.saved) != 0 || len(admin.published) != 0 {
		t.Fatalf("existing template was overwritten: saved=%d published=%d", len(admin.saved), len(admin.published))
	}
}

func TestEnsureProductionMarketingWorkflowResumesBootstrapDraft(t *testing.T) {
	admin := &recordingProductionHILTemplateAdmin{template: hil.WorkflowTemplate{
		ID: productionMarketingWorkflowTemplateID, TenantID: productionHILTenantID,
		Status: hil.WorkflowTemplateDisabled, LatestPublishedVersion: 0,
		CreatedBy: hil.ActorRef{Kind: hil.ActorService, ID: productionHILBootstrapActorID},
		DraftDefinition: hil.WorkflowDefinition{
			RiskLevels: []hil.RiskLevel{hil.RiskMedium},
			Stages: []hil.WorkflowStageDefinition{{
				Key: "human-review", Name: "Human review", Order: 1, Type: hil.WorkflowStageApproval,
				Approval: &hil.ApprovalStagePolicy{Strategy: hil.ApprovalSingle, Assignees: []hil.WorkflowActorSelector{{Kind: hil.WorkflowSelectorHuman, ID: productionMarketingApproverID}}, DeadlineMinutes: 10},
			}},
		},
	}}
	if err := ensureProductionMarketingWorkflow(context.Background(), admin); err != nil {
		t.Fatalf("ensureProductionMarketingWorkflow: %v", err)
	}
	if len(admin.saved) != 1 || len(admin.published) != 1 || admin.published[0].Template.ID != productionMarketingWorkflowTemplateID {
		t.Fatalf("bootstrap draft was not resumed: saved=%+v published=%+v", admin.saved, admin.published)
	}
	approval := admin.saved[0].Definition.Stages[0].Approval
	if approval == nil || approval.DeadlineMinutes != 7*24*60 || len(approval.ReminderOffsetsMinutes) != 1 || approval.ReminderOffsetsMinutes[0] != 24*60 {
		t.Fatalf("resumed approval policy = %+v", approval)
	}
}

func TestEnsureProductionMarketingWorkflowPropagatesLookupFailure(t *testing.T) {
	want := errors.New("database unavailable")
	admin := &recordingProductionHILTemplateAdmin{getErr: want}
	if err := ensureProductionMarketingWorkflow(context.Background(), admin); !errors.Is(err, want) {
		t.Fatalf("error = %v, want lookup failure", err)
	}
}

type recordingProductionHILProfileDirectory struct {
	profiles map[agentcore.AgentProfileID]agentcore.AgentProfile
	page     agentcore.AgentDirectoryPage
	query    agentcore.AgentDirectoryQuery
}

func (d *recordingProductionHILProfileDirectory) GetProfile(_ context.Context, id agentcore.AgentProfileID) (agentcore.AgentProfile, error) {
	profile, ok := d.profiles[id]
	if !ok {
		return agentcore.AgentProfile{}, agentcore.ErrNotFound
	}
	return profile, nil
}

func (d *recordingProductionHILProfileDirectory) QueryDirectory(_ context.Context, query agentcore.AgentDirectoryQuery) (agentcore.AgentDirectoryPage, error) {
	d.query = query
	return d.page, nil
}

func TestProductionHILAssigneeResolverValidatesExplicitHuman(t *testing.T) {
	directory := &recordingProductionHILProfileDirectory{profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{
		"human-ding": {
			ID: "human-ding", AgentKind: agentcore.AgentKindHuman, Enabled: true,
			OrgIdentity: agentcore.AgentOrgIdentity{OrganizationRef: agentcore.OrganizationRef{ID: "tenant-a"}},
		},
	}}
	resolver := productionHILAssigneeResolver{profiles: directory}
	actors, err := resolver.ResolveWorkflowAssignees(context.Background(), hil.WorkflowAssigneeRequest{
		TenantID: "tenant-a", Selector: hil.WorkflowActorSelector{Kind: hil.WorkflowSelectorHuman, ID: "human-ding"},
	})
	if err != nil || len(actors) != 1 || actors[0] != (hil.ActorRef{Kind: hil.ActorHuman, ID: "human-ding"}) {
		t.Fatalf("actors=%+v err=%v", actors, err)
	}
	directory.profiles["human-ding"] = agentcore.AgentProfile{ID: "human-ding", AgentKind: agentcore.AgentKindHuman, Enabled: false}
	if _, err := resolver.ResolveWorkflowAssignees(context.Background(), hil.WorkflowAssigneeRequest{
		TenantID: "tenant-a", Selector: hil.WorkflowActorSelector{Kind: hil.WorkflowSelectorHuman, ID: "human-ding"},
	}); err == nil {
		t.Fatal("disabled human was accepted")
	}
}

func TestProductionHILAssigneeResolverQueriesTenantRole(t *testing.T) {
	directory := &recordingProductionHILProfileDirectory{page: agentcore.AgentDirectoryPage{Entries: []agentcore.AgentDirectoryEntry{{
		AgentProfileID: "human-reviewer", AgentKind: agentcore.AgentKindHuman, Enabled: true,
		OrgIdentity: agentcore.AgentOrgIdentity{OrganizationRef: agentcore.OrganizationRef{ID: "tenant-a"}},
	}}}}
	resolver := productionHILAssigneeResolver{profiles: directory}
	actors, err := resolver.ResolveWorkflowAssignees(context.Background(), hil.WorkflowAssigneeRequest{
		TenantID: "tenant-a", Selector: hil.WorkflowActorSelector{Kind: hil.WorkflowSelectorPeopleRole, ID: "marketing-review"},
	})
	if err != nil || len(actors) != 1 || actors[0].ID != "human-reviewer" {
		t.Fatalf("actors=%+v err=%v", actors, err)
	}
	if directory.query.Organization != "tenant-a" || len(directory.query.Roles) != 1 || directory.query.Roles[0] != "marketing-review" || !directory.query.EnabledOnly || !directory.query.IncludeHumans {
		t.Fatalf("query = %+v", directory.query)
	}
}

func TestProductionHILAssigneeResolverUsesVerifiedOntologyRecommendations(t *testing.T) {
	directory := &recordingProductionHILProfileDirectory{profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{
		"human-advisor": {ID: "human-advisor", AgentKind: agentcore.AgentKindHuman, Enabled: true},
		"agent-advisor": {ID: "agent-advisor", AgentKind: agentcore.AgentKindAI, Enabled: true},
	}}
	resolver := productionHILAssigneeResolver{profiles: directory}
	actors, err := resolver.ResolveWorkflowAssignees(context.Background(), hil.WorkflowAssigneeRequest{
		TenantID:          productionHILTenantID,
		Selector:          hil.WorkflowActorSelector{Kind: hil.WorkflowSelectorOntologyRecommended},
		RecommendedActors: []hil.ActorRef{{Kind: hil.ActorHuman, ID: "human-advisor"}, {Kind: hil.ActorAgent, ID: "agent-advisor"}},
	})
	if err != nil || len(actors) != 2 || actors[0].ID != "human-advisor" || actors[1].ID != "agent-advisor" {
		t.Fatalf("actors=%+v err=%v", actors, err)
	}
}

type recordingProductionHILNotificationStore struct {
	detail  hil.WorkflowInstanceDetail
	results []hil.WorkflowNotificationResult
}

func (s *recordingProductionHILNotificationStore) GetWorkflowInstance(context.Context, string, string) (hil.WorkflowInstanceDetail, error) {
	return s.detail, nil
}

func (s *recordingProductionHILNotificationStore) CompleteWorkflowNotification(_ context.Context, result hil.WorkflowNotificationResult) error {
	s.results = append(s.results, result)
	return nil
}

type recordingProductionHILGatewayDeliveries struct {
	commands           []gwoutbound.EnqueueCommand
	enqueued           gwoutbound.Delivery
	stored             gwoutbound.Delivery
	existingKeys       map[string]bool
	idempotencyLookups []string
	idempotencyErr     error
}

func TestProductionHILWorkflowNotificationSuppressesSystemToolDecision(t *testing.T) {
	store := &recordingProductionHILNotificationStore{detail: hil.WorkflowInstanceDetail{
		Decision: hil.DecisionRequest{ActionTypeID: string(actioncontract.ActionSystemFileChange)},
		Instance: hil.WorkflowInstance{ID: "workflow-system-tool", TenantID: productionHILTenantID},
	}}
	deliveries := &recordingProductionHILGatewayDeliveries{}
	dispatcher := productionHILWorkflowNotificationDispatcher{
		workflows: store, profiles: &recordingProductionHILProfileDirectory{}, deliveries: deliveries,
		now: func() time.Time { return time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC) },
	}
	notification := hil.WorkflowNotification{
		ID: "notification-system-tool", TenantID: productionHILTenantID, WorkflowInstanceID: "workflow-system-tool",
		Channel: "feishu", Recipient: hil.ActorRef{Kind: hil.ActorHuman, ID: "human-ding"}, EventType: "task.reminder",
	}
	if err := dispatcher.Dispatch(context.Background(), notification); err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if len(deliveries.commands) != 0 {
		t.Fatalf("system tool notification was queued: %+v", deliveries.commands)
	}
	if len(store.results) != 1 || !store.results[0].Cancelled || store.results[0].Delivered || store.results[0].Pending || store.results[0].ProviderReceipt != "" {
		t.Fatalf("suppressed notification result = %+v", store.results)
	}
}

func (d *recordingProductionHILGatewayDeliveries) Enqueue(_ context.Context, command gwoutbound.EnqueueCommand) ([]gwoutbound.Delivery, error) {
	d.commands = append(d.commands, command)
	return []gwoutbound.Delivery{d.enqueued}, nil
}

func (d *recordingProductionHILGatewayDeliveries) Get(context.Context, string, string) (gwoutbound.Delivery, error) {
	return d.stored, nil
}

func (d *recordingProductionHILGatewayDeliveries) HasIdempotencyKey(_ context.Context, tenantID, humanProfileID, key string) (bool, error) {
	d.idempotencyLookups = append(d.idempotencyLookups, strings.Join([]string{tenantID, humanProfileID, key}, "|"))
	return d.existingKeys[key], d.idempotencyErr
}

func TestProductionHILWorkflowNotificationWaitsForExternalReceipt(t *testing.T) {
	now := time.Date(2026, 8, 17, 2, 0, 0, 0, time.UTC)
	store := &recordingProductionHILNotificationStore{detail: hil.WorkflowInstanceDetail{
		Case:     hil.WorkflowCase{Requester: hil.ActorRef{Kind: hil.ActorAgent, ID: "jordan-belfort"}},
		Instance: hil.WorkflowInstance{ID: "workflow-1", TenantID: productionHILTenantID, MaterialHash: "sha256:material", Status: hil.WorkflowRunning},
		Decision: hil.DecisionRequest{
			ActionTypeID: string(actioncontract.ActionCommunicationWechatDraftCreate),
			Purpose:      "create WeChat draft", RiskLevel: hil.RiskMedium, Status: hil.DecisionPending,
			PayloadPreview: map[string]any{
				"title": "Reviewed title", "summary": "Reviewed summary", "content": "# Reviewed article\n![cover](imgs/cover.png)\nBody for approval.",
				"material_hash": "sha256:material", "fields": map[string]any{"account_id": "wechat-main", "author": "Jordan"},
				"artifacts": []any{map[string]any{
					"role": "cover", "path": "imgs/cover.png", "size_bytes": float64(1024),
					"media_type": "image/png", "sha256": "sha256:cover",
				}},
			},
		},
		Tasks: []hil.WorkflowTask{{ID: "task-1", Status: hil.TaskPending, CurrentAssignee: hil.ActorRef{Kind: hil.ActorHuman, ID: "human-ding"}, DeadlineAt: now.Add(8 * time.Minute)}},
	}}
	profiles := &recordingProductionHILProfileDirectory{profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{
		"human-ding": {
			ID: "human-ding", AgentKind: agentcore.AgentKindHuman, Enabled: true,
			OrgIdentity: agentcore.AgentOrgIdentity{OrganizationRef: agentcore.OrganizationRef{ID: "orrery"}},
		},
	}}
	deliveries := &recordingProductionHILGatewayDeliveries{enqueued: gwoutbound.Delivery{ID: "gwd_1", TenantID: "orrery", Status: "pending"}}
	dispatcher := productionHILWorkflowNotificationDispatcher{
		workflows: store, profiles: profiles, deliveries: deliveries,
		accountID: productionHILFeishuAccountID, publicOrigin: "https://orrery.example.test", now: func() time.Time { return now },
	}
	notification := hil.WorkflowNotification{
		ID: "notification-1", TenantID: productionHILTenantID, WorkflowInstanceID: "workflow-1", TaskID: "task-1",
		Channel: "feishu", Recipient: hil.ActorRef{Kind: hil.ActorHuman, ID: "human-ding"}, EventType: "task.assigned",
		IdempotencyKey: "workflow:workflow-1:task:task-1:assigned",
	}
	if err := dispatcher.Dispatch(context.Background(), notification); err != nil {
		t.Fatalf("Dispatch pending: %v", err)
	}
	if len(deliveries.commands) != 1 {
		t.Fatalf("commands = %+v", deliveries.commands)
	}
	command := deliveries.commands[0]
	if command.TenantID != "orrery" || command.HumanProfileID != "human-ding" || command.AccountID != productionHILFeishuAccountID || len(command.ChannelKeys) != 1 || command.ChannelKeys[0] != "feishu" {
		t.Fatalf("command route = %+v", command)
	}
	for _, expected := range []string{"Reviewed title", "Reviewed summary", "Reviewed article", "sha256:material", "wechat-main", "审批附件", "cover", "imgs/cover.png", "image/png", "1024 bytes", "sha256:cover", "https://orrery.example.test/approvals?mode=hil&workflow_id=workflow-1"} {
		if !strings.Contains(command.Body, expected) {
			t.Fatalf("notification body missing %q: %s", expected, command.Body)
		}
	}
	if strings.Contains(command.Body, "![cover](imgs/cover.png)") {
		t.Fatalf("notification body contains a local Markdown image reference: %s", command.Body)
	}
	if len(store.results) != 1 || !store.results[0].Pending || store.results[0].Delivered || store.results[0].ProviderReceipt != "gwd_1" {
		t.Fatalf("pending results = %+v", store.results)
	}

	store.results = nil
	notification.ProviderReceipt = "gwd_1"
	deliveries.stored = gwoutbound.Delivery{ID: "gwd_1", TenantID: "orrery", Status: "delivered", ExternalMessageID: "om_external_1"}
	if err := dispatcher.Dispatch(context.Background(), notification); err != nil {
		t.Fatalf("Dispatch delivered: %v", err)
	}
	if len(store.results) != 1 || !store.results[0].Delivered || store.results[0].Pending || store.results[0].ProviderReceipt != "om_external_1" {
		t.Fatalf("delivered results = %+v", store.results)
	}
}

type recordingProductionHILTenantLister struct {
	tenants []string
	calls   *[]string
}

func (l recordingProductionHILTenantLister) ListWorkflowTenants(context.Context, time.Time) ([]string, error) {
	*l.calls = append(*l.calls, "tenants")
	return l.tenants, nil
}

type recordingProductionHILLifecycle struct {
	calls *[]string
}

func (l recordingProductionHILLifecycle) SweepDue(_ context.Context, tenantID string, _ time.Time, _ int) (hil.WorkflowSweepResult, error) {
	*l.calls = append(*l.calls, "due:"+tenantID)
	return hil.WorkflowSweepResult{}, nil
}

type recordingProductionHILOutbox struct {
	notifications []hil.WorkflowNotification
	calls         *[]string
}

func (o recordingProductionHILOutbox) ClaimWorkflowNotifications(_ context.Context, tenantID string, _ time.Time, _ int) ([]hil.WorkflowNotification, error) {
	*o.calls = append(*o.calls, "claim:"+tenantID)
	return o.notifications, nil
}

type recordingProductionHILDispatchPort struct {
	calls *[]string
}

func (d recordingProductionHILDispatchPort) Dispatch(_ context.Context, notification hil.WorkflowNotification) error {
	*d.calls = append(*d.calls, "dispatch:"+notification.ID)
	return nil
}

func TestProductionHILWorkflowSweeperProcessesDeadlinesBeforeNotifications(t *testing.T) {
	calls := []string{}
	sweeper := productionHILWorkflowSweeper{
		tenants:   recordingProductionHILTenantLister{tenants: []string{"system"}, calls: &calls},
		lifecycle: recordingProductionHILLifecycle{calls: &calls},
		outbox: recordingProductionHILOutbox{
			notifications: []hil.WorkflowNotification{{ID: "notification-1"}}, calls: &calls,
		},
		dispatcher: recordingProductionHILDispatchPort{calls: &calls},
		now:        func() time.Time { return time.Date(2026, 8, 17, 2, 0, 0, 0, time.UTC) },
	}
	if err := sweeper.Sweep(context.Background()); err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	want := []string{"tenants", "due:system", "claim:system", "dispatch:notification-1"}
	if strings.Join(calls, ",") != strings.Join(want, ",") {
		t.Fatalf("calls = %+v, want %+v", calls, want)
	}
}

func TestProductionHILServiceCreatesMarketingWorkflowTask(t *testing.T) {
	ctx := context.Background()
	rt, err := platform.OpenWithModules(ctx, testkit.NewTestConfig(t), OntologyModule())
	if err != nil {
		t.Fatalf("open runtime: %v", err)
	}
	t.Cleanup(func() { _ = rt.Close() })
	if err := rt.Initialize(ctx); err != nil {
		t.Fatalf("initialize runtime: %v", err)
	}
	core, err := rt.Databases.Store(db.RoleCore)
	if err != nil {
		t.Fatalf("core store: %v", err)
	}
	profiles := agentcore.NewPlatformProfileRepository(rt.UnitOfWork)
	profile := defaultHumanAgentProfile(productionMarketingApproverID, "Ding")
	if err := profiles.SaveProfile(ctx, profile); err != nil {
		t.Fatalf("save approver profile: %v", err)
	}
	service, _, repository, err := newProductionHILService(ctx, core, rt, profiles, true, nil)
	if err != nil {
		t.Fatalf("newProductionHILService: %v", err)
	}
	approval := hil.NewMarketingWechatApprovalService(service, repository, rt.Clock.Now)
	request := hil.MarketingWechatApprovalInput{
		TenantID: productionHILTenantID, RequesterID: "marketing", GenerationID: "scheduled-run-marketing-1", AccountID: "wechat-main",
		ContentHash: "sha256:content", AssetHashes: []string{"sha256:cover"},
		Material: hil.BusinessApprovalMaterial{
			Title: "公众号文章草稿审批", DecisionRequest: "是否批准将本期文章写入公众号草稿箱？",
			BusinessContext: "文章和图片已经完成质量检查。", ProposedAction: "将冻结内容写入公众号草稿箱。",
			ExpectedResult: "生成一份可继续人工检查的公众号草稿。", ImpactScope: "公众号主账号草稿箱，不公开发布。",
			Risks: []hil.BusinessApprovalRisk{{Description: "内容可能不准确。", Mitigation: "检查冻结预览，生成后仍不公开发布。"}},
		},
		IdempotencyKey: "production-marketing-workflow",
	}
	created, err := approval.RequestMarketingWechatApproval(ctx, request)
	if err != nil {
		t.Fatalf("RequestMarketingWechatApproval: %v", err)
	}
	replayed, err := approval.RequestMarketingWechatApproval(ctx, request)
	if err != nil || replayed.DecisionID != created.DecisionID || replayed.WorkflowInstanceID != created.WorkflowInstanceID {
		t.Fatalf("replayed approval = %+v err=%v, want %+v", replayed, err, created)
	}
	page, err := repository.ListWorkflowInstances(ctx, hil.WorkflowListQuery{TenantID: productionHILTenantID})
	if err != nil || page.Total != 1 || len(page.Items) != 1 {
		t.Fatalf("workflow page = %+v err=%v", page, err)
	}
	detail := page.Items[0]
	if detail.Decision.ID != created.DecisionID || created.WorkflowInstanceID != detail.Instance.ID || len(detail.Tasks) != 1 || detail.Tasks[0].CurrentAssignee.ID != productionMarketingApproverID {
		t.Fatalf("workflow detail = %+v", detail)
	}
	deadline := detail.Tasks[0].DeadlineAt.Sub(detail.Instance.CreatedAt)
	if deadline < 7*24*time.Hour-time.Second || deadline > 7*24*time.Hour {
		t.Fatalf("workflow deadline = %s, want 168h", deadline)
	}
	if len(detail.Notifications) != 1 || detail.Notifications[0].Channel != "feishu" || detail.Notifications[0].Recipient.ID != productionMarketingApproverID {
		t.Fatalf("workflow notifications = %+v", detail.Notifications)
	}
	tenants, err := (productionHILWorkflowTenantStore{store: core}).ListWorkflowTenants(ctx, rt.Clock.Now())
	if err != nil || len(tenants) != 1 || tenants[0] != productionHILTenantID {
		t.Fatalf("workflow tenants = %+v err=%v", tenants, err)
	}
}

func TestProductionHILServiceRunsCanonicalHighRiskBusinessTwoStageApproval(t *testing.T) {
	ctx := context.Background()
	rt, err := platform.OpenWithModules(ctx, testkit.NewTestConfig(t), OntologyModule())
	if err != nil {
		t.Fatalf("open runtime: %v", err)
	}
	t.Cleanup(func() { _ = rt.Close() })
	if err := rt.Initialize(ctx); err != nil {
		t.Fatalf("initialize runtime: %v", err)
	}
	core, err := rt.Databases.Store(db.RoleCore)
	if err != nil {
		t.Fatalf("core store: %v", err)
	}
	profiles := agentcore.NewPlatformProfileRepository(rt.UnitOfWork)
	requester := defaultHumanAgentProfile("agent-high-risk", "高风险执行代理")
	requester.AgentKind = agentcore.AgentKindAI
	requester.OrgIdentity.EmploymentType = agentcore.EmploymentTypeAIAgent
	requester.OrgIdentity.ManagerAgentRef = agentcore.AgentProfileRef{ID: "human-manager"}
	requester.Persona.ResponsibilityBoundary = "执行需要两级审批的高风险操作。"
	manager := defaultHumanAgentProfile("human-manager", "Manager")
	control := defaultHumanAgentProfile("human-control", "Control Reviewer")
	control.OrgIdentity.RoleRefs = append(control.OrgIdentity.RoleRefs, agentcore.RoleRef{ID: agentcore.RoleID(productionApprovalReviewerRoleID)})
	for _, profile := range []agentcore.AgentProfile{requester, manager, control} {
		if err := profiles.SaveProfile(ctx, profile); err != nil {
			t.Fatalf("save profile %s: %v", profile.ID, err)
		}
	}
	service, workflow, repository, err := newProductionHILService(ctx, core, rt, profiles, true, nil)
	if err != nil {
		t.Fatalf("newProductionHILService: %v", err)
	}
	created, err := service.CreateDecision(ctx, hil.CreateDecisionInput{
		TenantID: productionHILTenantID, Requester: hil.ActorRef{Kind: hil.ActorAgent, ID: requester.ID.String()},
		Action: string(actioncontract.ActionBusinessApprovalRequest), ActionTypeID: string(actioncontract.ActionBusinessApprovalRequest),
		Resource: hil.ObjectRef{Kind: "business_decision", ID: "approval-high-risk"}, ArgsHash: "sha256:args-high-risk",
		Purpose: "approve a high-risk business decision", RiskLevel: hil.RiskHigh, ApprovalRequired: true, PreferredMode: hil.ModeApproval,
		SubjectRefs:    []hil.ObjectRef{{Kind: "agent_run", ID: "run-high-risk"}},
		PayloadPreview: map[string]any{"approval_tool_name": hil.BusinessApprovalToolName, "material_hash": "sha256:material-high-risk"},
		IdempotencyKey: "production-high-risk-workflow", ExpiresAt: rt.Clock.Now().Add(8 * time.Hour),
	})
	if err != nil {
		t.Fatalf("CreateDecision: %v", err)
	}
	detail, err := repository.GetWorkflowInstanceByDecisionID(ctx, productionHILTenantID, created.Decision.ID)
	if err != nil {
		t.Fatalf("GetWorkflowInstanceByDecisionID: %v", err)
	}
	if detail.Instance.TemplateVersionID == "" || len(detail.Stages) != 2 || len(detail.Tasks) != 2 ||
		detail.Tasks[0].CurrentAssignee.ID != manager.ID.String() || detail.Tasks[0].Status != hil.TaskPending ||
		detail.Tasks[1].CurrentAssignee.ID != control.ID.String() || detail.Tasks[1].Status != hil.TaskScheduled {
		t.Fatalf("initial high-risk workflow = %+v", detail)
	}
	detail, err = workflow.DecideTask(ctx, productionHILTenantID, detail.Tasks[0].ID, hil.ActorRef{Kind: hil.ActorHuman, ID: manager.ID.String()}, hil.DecideWorkflowTaskInput{
		Decision: hil.WorkflowTaskApprove, IdempotencyKey: "approve-high-risk-manager", ExpectedInstanceVersion: 1,
	})
	if err != nil {
		t.Fatalf("approve manager stage: %v", err)
	}
	if detail.Decision.Status != hil.DecisionPending || detail.Instance.Status != hil.WorkflowRunning || len(detail.Receipts) != 0 || detail.Tasks[1].Status != hil.TaskPending {
		t.Fatalf("manager stage bypassed final approval: %+v", detail)
	}
	detail, err = workflow.DecideTask(ctx, productionHILTenantID, detail.Tasks[1].ID, hil.ActorRef{Kind: hil.ActorHuman, ID: control.ID.String()}, hil.DecideWorkflowTaskInput{
		Decision: hil.WorkflowTaskApprove, IdempotencyKey: "approve-high-risk-control", ExpectedInstanceVersion: 2,
	})
	if err != nil {
		t.Fatalf("approve control stage: %v", err)
	}
	if detail.Decision.Status != hil.DecisionResolved || detail.Instance.Status != hil.WorkflowApproved || len(detail.Receipts) != 1 || detail.Receipts[0].Effect != hil.EffectAuthorize {
		t.Fatalf("completed high-risk workflow = %+v", detail)
	}
}

func TestProductionBusinessApprovalToolCreatesReadableWorkflowTask(t *testing.T) {
	ctx := context.Background()
	rt, err := platform.OpenWithModules(ctx, testkit.NewTestConfig(t), OntologyModule())
	if err != nil {
		t.Fatalf("open runtime: %v", err)
	}
	t.Cleanup(func() { _ = rt.Close() })
	if err := rt.Initialize(ctx); err != nil {
		t.Fatalf("initialize runtime: %v", err)
	}
	core, err := rt.Databases.Store(db.RoleCore)
	if err != nil {
		t.Fatalf("core store: %v", err)
	}
	profiles := agentcore.NewPlatformProfileRepository(rt.UnitOfWork)
	if err := profiles.SaveProfile(ctx, defaultHumanAgentProfile(productionMarketingApproverID, "Ding")); err != nil {
		t.Fatalf("save approver profile: %v", err)
	}
	control := defaultHumanAgentProfile("business-control", "Business Control")
	control.OrgIdentity.RoleRefs = append(control.OrgIdentity.RoleRefs, agentcore.RoleRef{ID: agentcore.RoleID(productionApprovalReviewerRoleID)})
	if err := profiles.SaveProfile(ctx, control); err != nil {
		t.Fatalf("save control profile: %v", err)
	}
	requester := defaultHumanAgentProfile("jordan-belfort", "Jordan Belfort")
	requester.OrgIdentity.ManagerAgentRef = agentcore.AgentProfileRef{ID: productionMarketingApproverID}
	if err := profiles.SaveProfile(ctx, requester); err != nil {
		t.Fatalf("save requester profile: %v", err)
	}
	service, _, repository, err := newProductionHILService(ctx, core, rt, profiles, true, nil)
	if err != nil {
		t.Fatalf("newProductionHILService: %v", err)
	}
	executor := hil.NewBusinessApprovalToolExecutor(service, rt.Clock.Now)
	result, err := executor.Execute(ctx, hil.BusinessApprovalToolRequest{
		TenantID: productionHILTenantID, AgentID: "jordan-belfort", Arguments: map[string]any{
			"title": "公众号文章发布审批", "decision_request": "是否批准发布本期公众号文章？",
			"business_context": "本周产品更新需要对外同步。", "proposed_action": "使用主账号发布已完成校对的文章。",
			"expected_result": "用户能在公众号看到本期更新。", "impact_scope": "公众号主账号及其订阅用户。",
			"risks":         []any{map[string]any{"description": "内容表述可能产生歧义。", "mitigation": "发布前由市场负责人完成终审。"}},
			"resource_kind": "marketing_campaign", "resource_id": "campaign-2026-08", "idempotency_key": "business-approval-campaign-2026-08",
		},
	})
	if err != nil {
		t.Fatalf("Execute business approval: %v", err)
	}
	page, err := repository.ListWorkflowInstances(ctx, hil.WorkflowListQuery{TenantID: productionHILTenantID})
	if err != nil || page.Total != 1 || len(page.Items) != 1 {
		t.Fatalf("workflow page = %+v err=%v", page, err)
	}
	detail := page.Items[0]
	material, _ := detail.Decision.PayloadPreview["business_approval_material"].(map[string]any)
	if result.DecisionID != detail.Decision.ID || detail.Instance.MaterialHash != result.MaterialHash || material["title"] != "公众号文章发布审批" {
		t.Fatalf("workflow result=%+v detail=%+v material=%+v", result, detail, material)
	}
	if len(detail.Tasks) != 2 || detail.Tasks[0].CurrentAssignee.ID != productionMarketingApproverID || detail.Tasks[0].Status != hil.TaskPending ||
		detail.Tasks[1].CurrentAssignee.ID != control.ID.String() || detail.Tasks[1].Status != hil.TaskScheduled || len(detail.Notifications) != 1 {
		t.Fatalf("workflow task/notifications = %+v / %+v", detail.Tasks, detail.Notifications)
	}
}
