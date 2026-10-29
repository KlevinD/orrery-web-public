package main

import (
	"context"
	"errors"
	"fmt"
	"strings"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/actioncontract"
	"orrery_v2/internal/app/hil"
)

const (
	productionHILTenantID                        = hil.BuiltInWorkflowTenantID
	productionHILBootstrapActorID                = "orrery-bootstrap"
	productionDefaultEscalationApproverID        = "H000001"
	productionApprovalReviewerRoleID             = "approval.reviewer"
	productionMarketingApproverID                = productionDefaultEscalationApproverID
	productionMarketingWorkflowTemplateID        = hil.BuiltInMarketingWorkflowTemplateID
	productionMarketingWorkflowTemplateName      = "Marketing WeChat draft approval"
	productionBusinessWorkflowTemplateID         = hil.BuiltInBusinessWorkflowTemplateID
	productionExternalEmailWorkflowTemplateID    = hil.BuiltInExternalEmailWorkflowTemplateID
	productionCommandExecutionWorkflowTemplateID = hil.BuiltInCommandExecutionWorkflowTemplateID
	productionHighRiskWorkflowTemplateID         = hil.BuiltInHighRiskWorkflowTemplateID
	productionMarketingApprovalDeadline          = 7 * 24 * 60
	productionMarketingApprovalReminder          = 24 * 60
	productionBusinessApprovalDeadline           = 24 * 60
	productionBusinessApprovalReminder           = 60
	productionBusinessApprovalEscalationDeadline = 4 * 60
	productionExternalEmailApprovalDeadline      = 4 * 60
	productionExternalEmailApprovalReminder      = 30
	productionExternalEmailEscalationDeadline    = 2 * 60
	productionCommandExecutionApprovalDeadline   = 60
	productionCommandExecutionApprovalReminder   = 15
	productionCommandExecutionEscalationDeadline = 30
	productionHighRiskManagerDeadline            = 4 * 60
	productionHighRiskManagerReminder            = 60
	productionHighRiskFinalDeadline              = 4 * 60
	productionHighRiskFinalReminder              = 60
)

type productionHILWorkflowTemplateAdmin interface {
	GetTemplate(context.Context, string, string) (hil.WorkflowTemplate, error)
	SaveTemplateDraft(context.Context, hil.ActorRef, hil.SaveTemplateDraftInput) (hil.WorkflowTemplate, error)
	PublishTemplate(context.Context, hil.ActorRef, hil.PublishTemplateInput) (hil.WorkflowTemplateVersion, error)
}

type productionHILWorkflowBindingAdmin interface {
	PutTemplateBinding(context.Context, hil.ActorRef, hil.PutTemplateBindingInput) (hil.WorkflowTemplateBinding, error)
}

type productionHILWorkflowAdmin interface {
	productionHILWorkflowTemplateAdmin
	productionHILWorkflowBindingAdmin
}

type productionHILDefaultWorkflow struct {
	ID          string
	Name        string
	Description string
	Definition  hil.WorkflowDefinition
	Bindings    []hil.WorkflowTemplateBinding
}

type productionHILProfileDirectory interface {
	GetProfile(context.Context, agentcore.AgentProfileID) (agentcore.AgentProfile, error)
	QueryDirectory(context.Context, agentcore.AgentDirectoryQuery) (agentcore.AgentDirectoryPage, error)
}

type productionHILAssigneeResolver struct {
	profiles productionHILProfileDirectory
}

func (r productionHILAssigneeResolver) ResolveWorkflowAssignees(ctx context.Context, request hil.WorkflowAssigneeRequest) ([]hil.ActorRef, error) {
	if r.profiles == nil || strings.TrimSpace(request.TenantID) == "" {
		return nil, fmt.Errorf("production HIL profile directory is required")
	}
	// A shared template is not a cross-organization grant. Legacy system
	// decisions use the verified requester's organization for every selector.
	var requester agentcore.AgentProfile
	if request.Requester.Kind == hil.ActorService {
		if request.TenantID == productionHILTenantID {
			// Preserve the existing fixed-owner Marketing service contract. This
			// binding comes from the resolved workflow, not model tool arguments.
			if request.Requester.ID != "marketing" || request.ActionTypeID != hil.MarketingWechatDraftApprovalActionType ||
				request.TemplateID != productionMarketingWorkflowTemplateID || request.Resource.Kind != hil.MarketingWechatGenerationResourceKind ||
				request.Selector.Kind != hil.WorkflowSelectorHuman || request.Selector.ID != productionMarketingApproverID {
				return nil, fmt.Errorf("approval_service_tenant_unbound: shared service approval requires an explicit organization binding")
			}
			owner, err := r.profiles.GetProfile(ctx, agentcore.AgentProfileID(productionMarketingApproverID))
			if err != nil || owner.ID.String() != productionMarketingApproverID || !owner.Enabled || owner.AgentKind != agentcore.AgentKindHuman {
				return nil, fmt.Errorf("approval_assignee_unavailable: enabled service contract owner required")
			}
			request.TenantID = productionHILProfileTenant(owner.OrgIdentity)
		}
		// Other services already have an explicit decision tenant. They use
		// tenant-bound selectors; a service name is not a personnel profile.
	} else if id := strings.TrimSpace(request.Requester.ID); id != "" {
		var err error
		requester, err = r.profiles.GetProfile(ctx, agentcore.AgentProfileID(id))
		if err != nil || requester.ID.String() != id || !requester.Enabled {
			return nil, fmt.Errorf("approval_requester_unavailable: enabled requester profile required")
		}
		if request.TenantID == productionHILTenantID {
			request.TenantID = productionHILProfileTenant(requester.OrgIdentity)
		} else if !productionHILRoutingTenantAllowsProfile(request.TenantID, requester.OrgIdentity) {
			return nil, fmt.Errorf("approval_requester_tenant_mismatch: requester is outside the decision tenant")
		}
	}
	switch request.Selector.Kind {
	case hil.WorkflowSelectorHuman:
		actor, err := r.profileActor(ctx, request.TenantID, request.Selector.ID, true)
		if err != nil {
			return nil, err
		}
		return []hil.ActorRef{actor}, nil
	case hil.WorkflowSelectorPeopleRole:
		return r.queryHumanActors(ctx, request.TenantID, agentcore.AgentDirectoryQuery{
			Roles: []agentcore.RoleID{agentcore.RoleID(strings.TrimSpace(request.Selector.ID))},
		})
	case hil.WorkflowSelectorOrganizationPosition:
		return r.queryHumanActors(ctx, request.TenantID, agentcore.AgentDirectoryQuery{
			Position: agentcore.PositionID(strings.TrimSpace(request.Selector.ID)),
		})
	case hil.WorkflowSelectorRequesterManager:
		if requester.ID.IsZero() {
			return nil, fmt.Errorf("approval_requester_unavailable: requester profile required for manager routing")
		}
		managerID := requester.OrgIdentity.ManagerAgentRef.ID.String()
		if managerID == "" {
			return nil, fmt.Errorf("approval_manager_missing: configure a human manager in the requester tenant")
		}
		actor, err := r.profileActor(ctx, request.TenantID, managerID, true)
		if err != nil {
			return nil, err
		}
		return []hil.ActorRef{actor}, nil
	case hil.WorkflowSelectorResourceOwner:
		kind := strings.ToLower(strings.TrimSpace(request.Resource.Kind))
		if kind != "human" && kind != "person" && kind != "agent" && kind != "agent_profile" && kind != "profile" {
			return nil, fmt.Errorf("workflow resource does not identify a profile owner")
		}
		actor, err := r.profileActor(ctx, request.TenantID, request.Resource.ID, false)
		if err != nil {
			return nil, err
		}
		return []hil.ActorRef{actor}, nil
	case hil.WorkflowSelectorOntologyRecommended:
		actors := make([]hil.ActorRef, 0, len(request.RecommendedActors))
		for _, recommended := range request.RecommendedActors {
			actor, err := r.profileActor(ctx, request.TenantID, recommended.ID, false)
			if err != nil {
				return nil, err
			}
			actors = append(actors, actor)
		}
		return actors, nil
	default:
		return nil, fmt.Errorf("unsupported production HIL assignee selector %q", request.Selector.Kind)
	}
}

func (r productionHILAssigneeResolver) queryHumanActors(ctx context.Context, tenantID string, query agentcore.AgentDirectoryQuery) ([]hil.ActorRef, error) {
	query.AgentKind = agentcore.AgentKindHuman
	query.EnabledOnly = true
	query.IncludeHumans = true
	query.Page = agentcore.Page{Limit: 100}
	if tenantID != productionHILTenantID {
		query.Organization = agentcore.OrganizationID(tenantID)
	}
	page, err := r.profiles.QueryDirectory(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("query workflow assignees: %w", err)
	}
	actors := make([]hil.ActorRef, 0, len(page.Entries))
	for _, entry := range page.Entries {
		if !entry.Enabled || entry.AgentKind != agentcore.AgentKindHuman || !productionHILRoutingTenantAllowsProfile(tenantID, entry.OrgIdentity) {
			return nil, fmt.Errorf("workflow assignee directory returned an invalid human profile")
		}
		actors = append(actors, hil.ActorRef{Kind: hil.ActorHuman, ID: entry.AgentProfileID.String()})
	}
	return actors, nil
}

func (r productionHILAssigneeResolver) profileActor(ctx context.Context, tenantID, profileID string, requireHuman bool) (hil.ActorRef, error) {
	profileID = strings.TrimSpace(profileID)
	if profileID == "" {
		return hil.ActorRef{}, fmt.Errorf("approval_assignee_unavailable: assignee profile id is required")
	}
	profile, err := r.profiles.GetProfile(ctx, agentcore.AgentProfileID(profileID))
	if err != nil {
		return hil.ActorRef{}, fmt.Errorf("approval_assignee_unavailable: load assignee profile: %w", err)
	}
	if profile.ID.String() != profileID || !profile.Enabled {
		return hil.ActorRef{}, fmt.Errorf("approval_assignee_unavailable: enabled assignee profile required")
	}
	if !productionHILRoutingTenantAllowsProfile(tenantID, profile.OrgIdentity) {
		return hil.ActorRef{}, fmt.Errorf("approval_assignee_tenant_mismatch: assignee is outside the requester tenant")
	}
	if profile.AgentKind == agentcore.AgentKindHuman {
		return hil.ActorRef{Kind: hil.ActorHuman, ID: profile.ID.String()}, nil
	}
	if requireHuman || (profile.AgentKind != agentcore.AgentKindAI && profile.AgentKind != agentcore.AgentKindComposite) {
		return hil.ActorRef{}, fmt.Errorf("workflow assignee profile %q is not an enabled human", profileID)
	}
	return hil.ActorRef{Kind: hil.ActorAgent, ID: profile.ID.String()}, nil
}

func productionHILTenantAllowsProfile(tenantID string, identity agentcore.AgentOrgIdentity) bool {
	// Existing notification/delegation flows separately verify the persisted
	// assignee or owner. Their system records retain the legacy namespace.
	tenantID = strings.TrimSpace(tenantID)
	return tenantID == productionHILTenantID || strings.TrimSpace(string(identity.OrganizationRef.ID)) == tenantID
}

func productionHILRoutingTenantAllowsProfile(tenantID string, identity agentcore.AgentOrgIdentity) bool {
	tenantID = strings.TrimSpace(tenantID)
	return tenantID != "" && productionHILProfileTenant(identity) == tenantID
}

func productionHILProfileTenant(identity agentcore.AgentOrgIdentity) string {
	if tenant := strings.TrimSpace(string(identity.OrganizationRef.ID)); tenant != "" {
		return tenant
	}
	// Profiles created before organization ownership existed belong only to
	// the legacy system namespace, never to an arbitrary organization.
	return productionHILTenantID
}

func ensureProductionMarketingWorkflow(ctx context.Context, admin productionHILWorkflowTemplateAdmin) error {
	if admin == nil {
		return fmt.Errorf("production HIL workflow template administration is required")
	}
	actor := hil.ActorRef{Kind: hil.ActorService, ID: productionHILBootstrapActorID}
	definition := productionMarketingWorkflowDefinition()
	publishIdempotencyKey := "bootstrap:" + productionMarketingWorkflowTemplateID + ":publish:v1"
	template, err := admin.GetTemplate(ctx, productionHILTenantID, productionMarketingWorkflowTemplateID)
	if err == nil {
		resumableBootstrapDraft := template.Status == hil.WorkflowTemplateDisabled && template.LatestPublishedVersion == 0 &&
			template.CreatedBy == actor
		legacyPublishedTemplate := template.Status == hil.WorkflowTemplateActive && template.LatestPublishedVersion > 0 &&
			template.CreatedBy == actor && productionMarketingLegacyWorkflowDefinition(template.DraftDefinition)
		if !resumableBootstrapDraft && !legacyPublishedTemplate {
			return nil
		}
		template, err = admin.SaveTemplateDraft(ctx, actor, hil.SaveTemplateDraftInput{
			ID: template.ID, TenantID: template.TenantID, Name: template.Name, Description: template.Description,
			Status: template.Status, Definition: definition, LatestPublishedVersion: template.LatestPublishedVersion,
			IdempotencyKey: "bootstrap:" + productionMarketingWorkflowTemplateID + ":deadline:v3:draft",
		})
		if err != nil {
			return fmt.Errorf("upgrade built-in Marketing approval workflow deadline: %w", err)
		}
		publishIdempotencyKey = "bootstrap:" + productionMarketingWorkflowTemplateID + ":deadline:v3:publish"
	} else if !errors.Is(err, hil.ErrNotFound) {
		return fmt.Errorf("look up built-in Marketing approval workflow: %w", err)
	} else {
		template, err = admin.SaveTemplateDraft(ctx, actor, hil.SaveTemplateDraftInput{
			ID:             productionMarketingWorkflowTemplateID,
			TenantID:       productionHILTenantID,
			Name:           productionMarketingWorkflowTemplateName,
			Description:    "One-person approval of immutable Marketing WeChat draft material.",
			Status:         hil.WorkflowTemplateDisabled,
			Definition:     definition,
			IdempotencyKey: "bootstrap:" + productionMarketingWorkflowTemplateID + ":draft:v1",
		})
		if err != nil {
			return fmt.Errorf("save built-in Marketing approval workflow: %w", err)
		}
	}
	_, err = admin.PublishTemplate(ctx, actor, hil.PublishTemplateInput{
		Template: template,
		Bindings: []hil.WorkflowTemplateBinding{{
			ID: "builtin-marketing-wechat-draft-approval-binding", TenantID: productionHILTenantID,
			ActionTypeID: hil.MarketingWechatDraftApprovalActionType,
			TemplateID:   productionMarketingWorkflowTemplateID, Active: true,
		}},
		IdempotencyKey: publishIdempotencyKey,
	})
	if err != nil {
		return fmt.Errorf("publish built-in Marketing approval workflow: %w", err)
	}
	return nil
}

func ensureProductionApprovalWorkflows(ctx context.Context, admin productionHILWorkflowAdmin) error {
	if admin == nil {
		return fmt.Errorf("production HIL workflow administration is required")
	}
	if err := ensureProductionMarketingWorkflow(ctx, admin); err != nil {
		return err
	}
	workflows := productionDefaultWorkflows()
	for _, workflow := range workflows {
		if err := ensureProductionDefaultWorkflow(ctx, admin, workflow); err != nil {
			return err
		}
	}
	bindings := []hil.WorkflowTemplateBinding{{
		ID: "builtin-marketing-wechat-draft-approval-binding", TenantID: productionHILTenantID,
		ActionTypeID: hil.MarketingWechatDraftApprovalActionType,
		TemplateID:   productionMarketingWorkflowTemplateID, Active: true,
	}, {
		ID: "builtin-marketing-wechat-draft-binding", TenantID: productionHILTenantID,
		ActionTypeID: "tool_use", ToolName: marketingWechatDraftApprovalTool,
		TemplateID: productionMarketingWorkflowTemplateID, Active: false,
	}, {
		ID: "builtin-marketing-wechat-canonical-binding", TenantID: productionHILTenantID,
		ActionTypeID: string(actioncontract.ActionCommunicationWechatDraftCreate),
		TemplateID:   productionMarketingWorkflowTemplateID, Active: false,
	}}
	for _, workflow := range workflows {
		bindings = append(bindings, workflow.Bindings...)
	}
	return ensureProductionWorkflowBindings(ctx, admin, bindings)
}

func ensureProductionDefaultWorkflow(ctx context.Context, admin productionHILWorkflowTemplateAdmin, workflow productionHILDefaultWorkflow) error {
	actor := hil.ActorRef{Kind: hil.ActorService, ID: productionHILBootstrapActorID}
	template, err := admin.GetTemplate(ctx, productionHILTenantID, workflow.ID)
	if err == nil {
		resumableBootstrapDraft := template.Status == hil.WorkflowTemplateDisabled && template.LatestPublishedVersion == 0 &&
			template.CreatedBy == actor
		if !resumableBootstrapDraft {
			return nil
		}
	} else if !errors.Is(err, hil.ErrNotFound) {
		return fmt.Errorf("look up built-in workflow %q: %w", workflow.ID, err)
	} else {
		template = hil.WorkflowTemplate{}
	}
	template, err = admin.SaveTemplateDraft(ctx, actor, hil.SaveTemplateDraftInput{
		ID: workflow.ID, TenantID: productionHILTenantID, Name: workflow.Name, Description: workflow.Description,
		Status: hil.WorkflowTemplateDisabled, Definition: workflow.Definition,
		LatestPublishedVersion: template.LatestPublishedVersion,
		IdempotencyKey:         "bootstrap:" + workflow.ID + ":draft:v1",
	})
	if err != nil {
		return fmt.Errorf("save built-in workflow %q: %w", workflow.ID, err)
	}
	if _, err := admin.PublishTemplate(ctx, actor, hil.PublishTemplateInput{
		Template: template, IdempotencyKey: "bootstrap:" + workflow.ID + ":publish:v1",
	}); err != nil {
		return fmt.Errorf("publish built-in workflow %q: %w", workflow.ID, err)
	}
	return nil
}

func ensureProductionWorkflowBindings(ctx context.Context, admin productionHILWorkflowBindingAdmin, bindings []hil.WorkflowTemplateBinding) error {
	actor := hil.ActorRef{Kind: hil.ActorService, ID: productionHILBootstrapActorID}
	for _, binding := range bindings {
		if _, err := admin.PutTemplateBinding(ctx, actor, hil.PutTemplateBindingInput{
			Binding: binding,
			Reason:  "Install a default system-owned approval workflow binding.",
		}); err != nil {
			return fmt.Errorf("install built-in workflow binding %q: %w", binding.ID, err)
		}
	}
	return nil
}

func productionDefaultWorkflows() []productionHILDefaultWorkflow {
	return []productionHILDefaultWorkflow{
		{
			ID: productionBusinessWorkflowTemplateID, Name: "通用业务审批",
			Description: "业务审批先由发起智能体的直属真人负责人处理，截止前 1 小时提醒；24 小时未处理则升级给丁皮。",
			Definition: productionManagerApprovalWorkflowDefinition(
				productionBusinessApprovalDeadline,
				productionBusinessApprovalReminder,
				productionBusinessApprovalEscalationDeadline,
			),
			Bindings: []hil.WorkflowTemplateBinding{{
				ID: "builtin-business-approval-binding", TenantID: productionHILTenantID,
				ActionTypeID: "business_approval", ToolName: hil.BusinessApprovalToolName,
				TemplateID: productionBusinessWorkflowTemplateID, Active: true,
			}, {
				ID: "builtin-business-canonical-approval-binding", TenantID: productionHILTenantID,
				ActionTypeID: string(actioncontract.ActionBusinessApprovalRequest),
				TemplateID:   productionBusinessWorkflowTemplateID, Active: true,
			}},
		},
		{
			ID: productionExternalEmailWorkflowTemplateID, Name: "外发邮件审批",
			Description: "外发邮件在发送前由发起智能体的直属真人负责人处理，截止前 30 分钟提醒；4 小时未处理则升级给丁皮。",
			Definition: productionManagerApprovalWorkflowDefinition(
				productionExternalEmailApprovalDeadline,
				productionExternalEmailApprovalReminder,
				productionExternalEmailEscalationDeadline,
			),
			Bindings: []hil.WorkflowTemplateBinding{{
				ID: "builtin-external-email-approval-binding", TenantID: productionHILTenantID,
				ActionTypeID: "tool_use", ToolName: "email_send",
				TemplateID: productionExternalEmailWorkflowTemplateID, Active: true,
			}, {
				ID: "builtin-external-email-canonical-binding", TenantID: productionHILTenantID,
				ActionTypeID: string(actioncontract.ActionCommunicationEmailSend),
				TemplateID:   productionExternalEmailWorkflowTemplateID, Active: true,
			}},
		},
		{
			ID: productionCommandExecutionWorkflowTemplateID, Name: "命令执行审批",
			Description: "命令执行先由发起智能体的直属真人负责人处理，截止前 15 分钟提醒；1 小时未处理则升级给丁皮。",
			Definition: productionManagerApprovalWorkflowDefinition(
				productionCommandExecutionApprovalDeadline,
				productionCommandExecutionApprovalReminder,
				productionCommandExecutionEscalationDeadline,
			),
			Bindings: []hil.WorkflowTemplateBinding{
				{
					ID: "builtin-command-execution-approval-binding", TenantID: productionHILTenantID,
					ActionTypeID: "command_execution", ToolName: "exec_command",
					TemplateID: productionCommandExecutionWorkflowTemplateID, Active: false,
				},
				{
					ID: "builtin-personal-assistant-shell-approval-binding", TenantID: productionHILTenantID,
					ActionTypeID: "tool_use", ToolName: "personal_assistant_shell_exec",
					TemplateID: productionCommandExecutionWorkflowTemplateID, Active: false,
				},
				{
					ID: "builtin-command-execution-canonical-binding", TenantID: productionHILTenantID,
					ActionTypeID: string(actioncontract.ActionSystemCommandExecute),
					TemplateID:   productionCommandExecutionWorkflowTemplateID, Active: false,
				},
				{
					ID: "builtin-file-change-canonical-binding", TenantID: productionHILTenantID,
					ActionTypeID: string(actioncontract.ActionSystemFileChange),
					TemplateID:   productionCommandExecutionWorkflowTemplateID, Active: false,
				},
			},
		},
		{
			ID: productionHighRiskWorkflowTemplateID, Name: "High-risk two-stage approval",
			Description: "High-risk actions require sequential manager and control-owner approval.",
			Definition:  productionHighRiskApprovalWorkflowDefinition(),
			Bindings:    productionHighRiskWorkflowBindings(),
		},
	}
}

func productionHighRiskWorkflowBindings() []hil.WorkflowTemplateBinding {
	actions := []struct {
		key string
		id  actioncontract.ActionTypeID
	}{
		{"business", actioncontract.ActionBusinessApprovalRequest},
		{"external-email", actioncontract.ActionCommunicationEmailSend},
		{"command", actioncontract.ActionSystemCommandExecute},
		{"file-change", actioncontract.ActionSystemFileChange},
		{"network", actioncontract.ActionNetworkExternalAccess},
		{"information", actioncontract.ActionInformationRead},
		{"external-tool", actioncontract.ActionToolExternalExecute},
		{"unclassified-tool", actioncontract.ActionToolUnclassifiedExecute},
	}
	bindings := make([]hil.WorkflowTemplateBinding, 0, len(actions)*2)
	for _, action := range actions {
		for _, risk := range []hil.RiskLevel{hil.RiskHigh, hil.RiskCritical} {
			bindings = append(bindings, hil.WorkflowTemplateBinding{
				ID:       "builtin-high-risk-" + action.key + "-" + string(risk) + "-binding",
				TenantID: productionHILTenantID, ActionTypeID: string(action.id), RiskLevel: risk,
				TemplateID: productionHighRiskWorkflowTemplateID, Active: actioncontract.IsHumanBusinessDecisionAction(action.id),
			})
		}
	}
	return bindings
}

func productionHighRiskApprovalWorkflowDefinition() hil.WorkflowDefinition {
	return hil.WorkflowDefinition{
		RiskLevels: []hil.RiskLevel{hil.RiskHigh, hil.RiskCritical}, RequireDistinctApprovers: true,
		Stages: []hil.WorkflowStageDefinition{
			{
				Key: "manager-review", Name: "Manager review", Order: 1, Type: hil.WorkflowStageApproval,
				Approval: &hil.ApprovalStagePolicy{
					Strategy: hil.ApprovalSingle, Assignees: []hil.WorkflowActorSelector{{Kind: hil.WorkflowSelectorRequesterManager}},
					DeadlineMinutes: productionHighRiskManagerDeadline, ReminderOffsetsMinutes: []int{productionHighRiskManagerReminder},
				},
			},
			{
				Key: "control-owner-review", Name: "Control owner review", Order: 2, Type: hil.WorkflowStageApproval,
				Approval: &hil.ApprovalStagePolicy{
					Strategy:        hil.ApprovalAny,
					Assignees:       []hil.WorkflowActorSelector{{Kind: hil.WorkflowSelectorPeopleRole, ID: productionApprovalReviewerRoleID}},
					DeadlineMinutes: productionHighRiskFinalDeadline, ReminderOffsetsMinutes: []int{productionHighRiskFinalReminder},
				},
			},
		},
	}
}

func productionManagerApprovalWorkflowDefinition(deadlineMinutes, reminderMinutes, escalationDeadlineMinutes int) hil.WorkflowDefinition {
	return hil.WorkflowDefinition{
		RiskLevels: []hil.RiskLevel{hil.RiskLow, hil.RiskMedium, hil.RiskHigh, hil.RiskCritical},
		Stages: []hil.WorkflowStageDefinition{{
			Key: "manager-review", Name: "直属负责人审批", Order: 1, Type: hil.WorkflowStageApproval,
			Approval: &hil.ApprovalStagePolicy{
				Strategy:               hil.ApprovalSingle,
				Assignees:              []hil.WorkflowActorSelector{{Kind: hil.WorkflowSelectorRequesterManager}},
				DeadlineMinutes:        deadlineMinutes,
				ReminderOffsetsMinutes: []int{reminderMinutes},
				Escalation: []hil.WorkflowEscalationRung{{
					Actors:          []hil.WorkflowActorSelector{{Kind: hil.WorkflowSelectorHuman, ID: productionDefaultEscalationApproverID}},
					DeadlineMinutes: escalationDeadlineMinutes,
				}},
			},
		}},
	}
}

func productionMarketingWorkflowDefinition() hil.WorkflowDefinition {
	return hil.WorkflowDefinition{
		RiskLevels: []hil.RiskLevel{hil.RiskLow, hil.RiskMedium, hil.RiskHigh, hil.RiskCritical},
		Stages: []hil.WorkflowStageDefinition{{
			Key: "human-review", Name: "Human review", Order: 1, Type: hil.WorkflowStageApproval,
			Approval: &hil.ApprovalStagePolicy{
				Strategy:               hil.ApprovalSingle,
				Assignees:              []hil.WorkflowActorSelector{{Kind: hil.WorkflowSelectorHuman, ID: productionMarketingApproverID}},
				DeadlineMinutes:        productionMarketingApprovalDeadline,
				ReminderOffsetsMinutes: []int{productionMarketingApprovalReminder},
			},
		}},
	}
}

func productionMarketingLegacyWorkflowDefinition(definition hil.WorkflowDefinition) bool {
	if len(definition.RiskLevels) != 4 || definition.RiskLevels[0] != hil.RiskLow || definition.RiskLevels[1] != hil.RiskMedium ||
		definition.RiskLevels[2] != hil.RiskHigh || definition.RiskLevels[3] != hil.RiskCritical || len(definition.Stages) != 1 {
		return false
	}
	stage := definition.Stages[0]
	if stage.Key != "human-review" || stage.Order != 1 || stage.Type != hil.WorkflowStageApproval || stage.Approval == nil {
		return false
	}
	approval := stage.Approval
	legacyDeadline := approval.DeadlineMinutes == 10 && len(approval.ReminderOffsetsMinutes) == 1 && approval.ReminderOffsetsMinutes[0] == 5 ||
		approval.DeadlineMinutes == 24*60 && len(approval.ReminderOffsetsMinutes) == 1 && approval.ReminderOffsetsMinutes[0] == 60
	return approval.Strategy == hil.ApprovalSingle && len(approval.Assignees) == 1 &&
		approval.Assignees[0] == (hil.WorkflowActorSelector{Kind: hil.WorkflowSelectorHuman, ID: productionMarketingApproverID}) &&
		legacyDeadline &&
		len(approval.Escalation) == 0
}
