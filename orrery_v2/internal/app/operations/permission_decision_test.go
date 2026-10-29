package operations

import (
	"context"
	"strings"
	"testing"
	"time"

	"orrery_v2/internal/app/rebac"
)

func TestPermissionDecisionDenyPolicyWinsOverRoleAllow(t *testing.T) {
	ctx := context.Background()
	service, store := newPermissionDecisionService(t, ctx)
	if err := store.SaveRoleAssignment(ctx, RoleAssignmentDTO{
		AssignmentID:       "assign-admin",
		Actor:              ActorRefDTO{ActorType: "human", ActorID: "profile-admin"},
		RoleID:             "org.admin",
		OrganizationNodeID: "org-1",
		OrganizationCode:   "01",
		ScopeMode:          RoleScopeSelf,
		Source:             RoleSourceDirect,
	}); err != nil {
		t.Fatalf("save assignment: %v", err)
	}

	decision, err := service.DecidePermission(ctx, permissionDecisionViewer(), PermissionDecisionRequestDTO{
		Subject:          ActorRefDTO{ActorType: "human", ActorID: "profile-admin"},
		Action:           "org.manage",
		ResourceType:     "organization",
		ResourceID:       "org-1",
		OrganizationCode: "01",
		Metadata:         map[string]string{"deny_actions": "org.manage"},
	})
	if err != nil {
		t.Fatalf("DecidePermission: %v", err)
	}

	assertPermissionDecision(t, decision, "deny", "deny_policy")
	if !strings.Contains(decision.Reason, "deny") {
		t.Fatalf("decision reason = %q, want deny policy", decision.Reason)
	}
}

func TestPermissionDecisionAllowsScopedRoleWithinOrganization(t *testing.T) {
	ctx := context.Background()
	service, store := newPermissionDecisionService(t, ctx)
	if err := store.SaveRoleAssignment(ctx, RoleAssignmentDTO{
		AssignmentID:       "assign-admin",
		Actor:              ActorRefDTO{ActorType: "human", ActorID: "profile-admin"},
		RoleID:             "org.admin",
		OrganizationNodeID: "org-1",
		OrganizationCode:   "01",
		ScopeMode:          RoleScopeSelf,
		Source:             RoleSourceDirect,
	}); err != nil {
		t.Fatalf("save assignment: %v", err)
	}

	decision, err := service.DecidePermission(ctx, permissionDecisionViewer(), PermissionDecisionRequestDTO{
		Subject:          ActorRefDTO{ActorType: "human", ActorID: "profile-admin"},
		Action:           "org.manage",
		ResourceType:     "organization",
		ResourceID:       "org-1",
		OrganizationCode: "01",
	})
	if err != nil {
		t.Fatalf("DecidePermission: %v", err)
	}

	assertPermissionDecision(t, decision, "allow", "role_assignment")
}

func TestPermissionDecisionAncestorRoleAppliesToDescendant(t *testing.T) {
	ctx := context.Background()
	service, store := newPermissionDecisionService(t, ctx)
	if err := store.SaveRoleAssignment(ctx, RoleAssignmentDTO{
		AssignmentID:       "assign-manager",
		Actor:              ActorRefDTO{ActorType: "human", ActorID: "profile-manager"},
		RoleID:             "org.manager",
		OrganizationNodeID: "org-1",
		OrganizationCode:   "01",
		ScopeMode:          RoleScopeSelfAndDescendants,
		Source:             RoleSourceDirect,
	}); err != nil {
		t.Fatalf("save assignment: %v", err)
	}

	decision, err := service.DecidePermission(ctx, permissionDecisionViewer(), PermissionDecisionRequestDTO{
		Subject:          ActorRefDTO{ActorType: "human", ActorID: "profile-manager"},
		Action:           "missions.assign",
		ResourceType:     "mission",
		ResourceID:       "mission-1",
		OrganizationCode: "01.01",
	})
	if err != nil {
		t.Fatalf("DecidePermission: %v", err)
	}

	assertPermissionDecision(t, decision, "allow", "ancestor_role_assignment")
}

func TestPermissionDecisionRequiresAgentResourceBinding(t *testing.T) {
	ctx := context.Background()
	service, store := newPermissionDecisionService(t, ctx)
	if err := store.SaveRoleAssignment(ctx, RoleAssignmentDTO{
		AssignmentID:       "assign-agent",
		Actor:              ActorRefDTO{ActorType: "agent", ActorID: "agent-ops"},
		RoleID:             "agent.operator",
		OrganizationNodeID: "org-1",
		OrganizationCode:   "01",
		ScopeMode:          RoleScopeSelfAndDescendants,
		Source:             RoleSourceDirect,
	}); err != nil {
		t.Fatalf("save assignment: %v", err)
	}

	decision, err := service.DecidePermission(ctx, permissionDecisionViewer(), PermissionDecisionRequestDTO{
		Subject:          ActorRefDTO{ActorType: "agent", ActorID: "agent-ops"},
		Action:           "agent.run",
		ResourceType:     "tool",
		ResourceID:       "tool.deploy",
		OrganizationCode: "01",
	})
	if err != nil {
		t.Fatalf("DecidePermission: %v", err)
	}

	assertPermissionDecision(t, decision, "deny", "resource_binding")
	if !strings.Contains(decision.Reason, "binding") {
		t.Fatalf("decision reason = %q, want missing binding", decision.Reason)
	}
}

func TestPermissionDecisionTemporaryGrantExpires(t *testing.T) {
	ctx := context.Background()
	service, _ := newPermissionDecisionService(t, ctx)
	service.SetPermissionGrants([]TemporaryPermissionGrantDTO{{
		GrantID:   "grant-expired",
		AgentID:   "agent-ops",
		ToolID:    "tool.deploy",
		Scope:     "tool.call",
		Reason:    "expired maintenance window",
		ExpiresAt: time.Now().UTC().Add(-time.Hour),
	}})

	decision, err := service.DecidePermission(ctx, permissionDecisionViewer(), PermissionDecisionRequestDTO{
		Subject:          ActorRefDTO{ActorType: "agent", ActorID: "agent-ops"},
		Action:           "tool.call",
		ResourceType:     "tool",
		ResourceID:       "tool.deploy",
		OrganizationCode: "01",
		Metadata:         map[string]string{"resource_binding": "allow"},
	})
	if err != nil {
		t.Fatalf("DecidePermission: %v", err)
	}

	assertPermissionDecision(t, decision, "deny", "temporary_grant")
	if !strings.Contains(decision.Reason, "expired") {
		t.Fatalf("decision reason = %q, want expired grant", decision.Reason)
	}
}

func TestPermissionDecisionTemporaryGrantMustMatchActionFingerprint(t *testing.T) {
	ctx := context.Background()
	service, _ := newPermissionDecisionService(t, ctx)
	service.SetPermissionGrants([]TemporaryPermissionGrantDTO{{
		GrantID:         "grant-approved-args",
		AgentID:         "agent-ops",
		EffectiveUserID: "human-ops",
		ToolID:          "tool.deploy",
		Scope:           "mcp.tool.call",
		ActionTypeID:    "mcp.tool.call",
		ObjectRefs:      []PermissionObjectRefDTO{{ResourceType: "tool", ResourceID: "tool.deploy"}},
		RequestedFields: []string{"payload", "result"},
		ArgsHash:        "sha256:approved",
		Reason:          "approved deployment",
		ExpiresAt:       time.Now().UTC().Add(time.Hour),
	}})

	decision, err := service.DecidePermission(ctx, permissionDecisionViewer(), PermissionDecisionRequestDTO{
		Subject:          ActorRefDTO{ActorType: "agent", ActorID: "agent-ops"},
		OnBehalfOf:       ActorRefDTO{ActorType: "human", ActorID: "human-ops"},
		ActionTypeID:     "mcp.tool.call",
		ResourceType:     "tool",
		ResourceID:       "tool.deploy",
		OrganizationCode: "01",
		ObjectRefs:       []PermissionObjectRefDTO{{ResourceType: "tool", ResourceID: "tool.deploy"}},
		RequestedFields:  []string{"payload", "result"},
		ArgsHash:         "sha256:changed",
		Metadata:         map[string]string{"resource_binding": "allow"},
	})
	if err != nil {
		t.Fatalf("DecidePermission: %v", err)
	}

	assertPermissionDecision(t, decision, PermissionDecisionDeny, permissionDecisionSourceMissing)
}

func TestPermissionDecisionApprovalDecisionAddsSource(t *testing.T) {
	ctx := context.Background()
	service, _ := newPermissionDecisionService(t, ctx)
	service.SetAuditStore(&permissionDecisionAuditStore{events: []AuditEventDTO{{
		AuditID:    "approval-1",
		ActorID:    "ops-lead",
		EntityType: "approval",
		EntityID:   "approval-tool-call",
		Action:     "approval.tool_access.approved",
		Result:     "succeeded",
		Metadata: map[string]string{
			"actor_type":        "agent",
			"agent_id":          "agent-ops",
			"tool_id":           "tool.deploy",
			"scope":             "tool.call",
			"organization_code": "01",
		},
	}}})

	decision, err := service.DecidePermission(ctx, permissionDecisionViewer(), PermissionDecisionRequestDTO{
		Subject:          ActorRefDTO{ActorType: "agent", ActorID: "agent-ops"},
		Action:           "tool.call",
		ResourceType:     "tool",
		ResourceID:       "tool.deploy",
		OrganizationCode: "01",
		Metadata:         map[string]string{"resource_binding": "allow"},
	})
	if err != nil {
		t.Fatalf("DecidePermission: %v", err)
	}

	assertPermissionDecision(t, decision, "allow", "approval_decision")
}

func TestPermissionDecisionUsesActionTypeIDAsActionForRoleDecision(t *testing.T) {
	ctx := context.Background()
	service, store := newPermissionDecisionService(t, ctx)
	if err := store.SaveRoleDefinition(ctx, RoleDefinitionDTO{
		RoleID:               "customer.action.runner",
		Name:                 "Customer Action Runner",
		Category:             "resource",
		Status:               organizationStatusActive,
		AssignableActorTypes: []string{"agent"},
		PermissionRefs:       []string{"customer.email.send"},
	}); err != nil {
		t.Fatalf("save role definition: %v", err)
	}
	if err := store.SaveRoleAssignment(ctx, RoleAssignmentDTO{
		AssignmentID:       "assign-customer-action",
		Actor:              ActorRefDTO{ActorType: "agent", ActorID: "agent-campaign"},
		RoleID:             "customer.action.runner",
		OrganizationNodeID: "org-1",
		OrganizationCode:   "01",
		ScopeMode:          RoleScopeSelf,
		Source:             RoleSourceDirect,
	}); err != nil {
		t.Fatalf("save role assignment: %v", err)
	}
	savePermissionDecisionRelation(t, ctx, service, rebac.RelationTuple{
		ID:        "rel-customer-editor",
		TenantID:  "default",
		Subject:   rebac.Ref{Kind: "human", ID: "profile-owner"},
		Relation:  "editor",
		Object:    rebac.Ref{Kind: "customer", ID: "customer-1"},
		Source:    rebac.Ref{Kind: "role_assignment", ID: "assign-customer-action"},
		CreatedAt: time.Now().UTC(),
	})

	decision, err := service.DecidePermission(ctx, permissionDecisionViewer(), PermissionDecisionRequestDTO{
		Subject:          ActorRefDTO{ActorType: "agent", ActorID: "agent-campaign"},
		OnBehalfOf:       ActorRefDTO{ActorType: "human", ActorID: "profile-owner"},
		ActionTypeID:     "customer.email.send",
		ResourceType:     "customer",
		ResourceID:       "customer-1",
		OrganizationCode: "01",
		RuntimeRef:       PermissionRuntimeRefDTO{RuntimeKind: "agent_runtime", RuntimeID: "run-1"},
		DelegationRef:    PermissionRefDTO{RefType: "delegation", RefID: "delegation-1"},
		ObjectRefs:       []PermissionObjectRefDTO{{ResourceType: "customer", ResourceID: "customer-1"}},
		RequestedFields:  []string{"email", "name"},
		ArgsHash:         "sha256:send-v1",
	})
	if err != nil {
		t.Fatalf("DecidePermission: %v", err)
	}

	assertPermissionDecision(t, decision, PermissionDecisionAllow, permissionDecisionSourceRole)
	assertPermissionDecision(t, decision, PermissionDecisionAllow, permissionDecisionSourceReBAC)
}

func TestPermissionDecisionActionTypeRoleAllowRequiresReBACRelation(t *testing.T) {
	ctx := context.Background()
	service, store := newPermissionDecisionService(t, ctx)
	if err := store.SaveRoleDefinition(ctx, RoleDefinitionDTO{
		RoleID:               "customer.action.runner",
		Name:                 "Customer Action Runner",
		Category:             "resource",
		Status:               organizationStatusActive,
		AssignableActorTypes: []string{"agent"},
		PermissionRefs:       []string{"customer.email.send"},
	}); err != nil {
		t.Fatalf("save role definition: %v", err)
	}
	if err := store.SaveRoleAssignment(ctx, RoleAssignmentDTO{
		AssignmentID:       "assign-customer-action",
		Actor:              ActorRefDTO{ActorType: "agent", ActorID: "agent-campaign"},
		RoleID:             "customer.action.runner",
		OrganizationNodeID: "org-1",
		OrganizationCode:   "01",
		ScopeMode:          RoleScopeSelf,
		Source:             RoleSourceDirect,
	}); err != nil {
		t.Fatalf("save role assignment: %v", err)
	}

	decision, err := service.DecidePermission(ctx, permissionDecisionViewer(), PermissionDecisionRequestDTO{
		Subject:          ActorRefDTO{ActorType: "agent", ActorID: "agent-campaign"},
		OnBehalfOf:       ActorRefDTO{ActorType: "human", ActorID: "profile-owner"},
		ActionTypeID:     "customer.email.send",
		ResourceType:     "customer",
		ResourceID:       "customer-1",
		OrganizationCode: "01",
		ArgsHash:         "sha256:send-v1",
	})
	if err != nil {
		t.Fatalf("DecidePermission: %v", err)
	}

	assertPermissionDecision(t, decision, PermissionDecisionDeny, permissionDecisionSourceMissingReBAC)
}

func TestPermissionDecisionApprovalDoesNotBypassMissingReBACRelation(t *testing.T) {
	ctx := context.Background()
	service, _ := newPermissionDecisionService(t, ctx)
	service.SetAuditStore(&permissionDecisionAuditStore{events: []AuditEventDTO{{
		AuditID:    "approval-customer-email",
		ActorID:    "ops-lead",
		EntityType: "approval",
		EntityID:   "approval-customer-email",
		Action:     "approval.action.approved",
		Result:     "succeeded",
		Metadata: map[string]string{
			"action_type_id":      "customer.email.send",
			"actor_type":          "agent",
			"actor_id":            "agent-campaign",
			"effective_user_type": "human",
			"effective_user_id":   "profile-owner",
			"resource_id":         "customer-1",
			"scope":               "customer.email.send",
			"organization_code":   "01",
			"args_hash":           "sha256:approved",
		},
	}}})

	decision, err := service.DecidePermission(ctx, permissionDecisionViewer(), PermissionDecisionRequestDTO{
		Subject:          ActorRefDTO{ActorType: "agent", ActorID: "agent-campaign"},
		OnBehalfOf:       ActorRefDTO{ActorType: "human", ActorID: "profile-owner"},
		ActionTypeID:     "customer.email.send",
		ResourceType:     "customer",
		ResourceID:       "customer-1",
		OrganizationCode: "01",
		ArgsHash:         "sha256:approved",
	})
	if err != nil {
		t.Fatalf("DecidePermission: %v", err)
	}

	assertPermissionDecision(t, decision, PermissionDecisionDeny, permissionDecisionSourceMissingReBAC)
}

func TestPermissionDecisionRequiresArgsHashForActionTypeRequest(t *testing.T) {
	ctx := context.Background()
	service, _ := newPermissionDecisionService(t, ctx)

	decision, err := service.DecidePermission(ctx, permissionDecisionViewer(), PermissionDecisionRequestDTO{
		Subject:          ActorRefDTO{ActorType: "agent", ActorID: "agent-campaign"},
		ActionTypeID:     "customer.email.send",
		ResourceType:     "customer",
		ResourceID:       "customer-1",
		OrganizationCode: "01",
	})
	if err != nil {
		t.Fatalf("DecidePermission: %v", err)
	}

	assertPermissionDecision(t, decision, PermissionDecisionDeny, permissionDecisionSourceValidation)
	if !strings.Contains(decision.Reason, "args_hash") {
		t.Fatalf("decision reason = %q, want args_hash validation", decision.Reason)
	}
}

func TestPermissionDecisionApprovalMustMatchActionArgsAndEffectiveUser(t *testing.T) {
	ctx := context.Background()
	service, _ := newPermissionDecisionService(t, ctx)
	service.SetAuditStore(&permissionDecisionAuditStore{events: []AuditEventDTO{{
		AuditID:    "approval-changed-args",
		ActorID:    "ops-lead",
		EntityType: "approval",
		EntityID:   "approval-customer-email",
		Action:     "approval.action.approved",
		Result:     "succeeded",
		Metadata: map[string]string{
			"action_type_id":      "customer.email.send",
			"actor_type":          "agent",
			"actor_id":            "agent-campaign",
			"effective_user_type": "human",
			"effective_user_id":   "profile-owner",
			"resource_id":         "customer-1",
			"scope":               "customer.email.send",
			"organization_code":   "01",
			"args_hash":           "sha256:approved",
			"requested_fields":    "email,name",
		},
	}}})

	decision, err := service.DecidePermission(ctx, permissionDecisionViewer(), PermissionDecisionRequestDTO{
		Subject:          ActorRefDTO{ActorType: "agent", ActorID: "agent-campaign"},
		OnBehalfOf:       ActorRefDTO{ActorType: "human", ActorID: "profile-owner"},
		ActionTypeID:     "customer.email.send",
		ResourceType:     "customer",
		ResourceID:       "customer-1",
		OrganizationCode: "01",
		RequestedFields:  []string{"email", "name"},
		ArgsHash:         "sha256:changed",
	})
	if err != nil {
		t.Fatalf("DecidePermission: %v", err)
	}

	assertPermissionDecision(t, decision, PermissionDecisionDeny, permissionDecisionSourceMissing)
}

func TestPermissionDecisionHighRiskToolCallResolvesOrganizationOwnerApprover(t *testing.T) {
	ctx := context.Background()
	service, store := newPermissionDecisionService(t, ctx)
	if err := store.SaveRoleDefinition(ctx, RoleDefinitionDTO{
		RoleID:               "tool.runner",
		Name:                 "Tool Runner",
		Category:             "resource",
		Status:               organizationStatusActive,
		AssignableActorTypes: []string{"agent"},
		PermissionRefs:       []string{"tool.call"},
		ApprovalCapability:   false,
	}); err != nil {
		t.Fatalf("save role definition: %v", err)
	}
	if err := store.SaveRoleAssignment(ctx, RoleAssignmentDTO{
		AssignmentID:       "assign-tool-runner",
		Actor:              ActorRefDTO{ActorType: "agent", ActorID: "agent-ops"},
		RoleID:             "tool.runner",
		OrganizationNodeID: "dept-ai",
		OrganizationCode:   "01.01",
		ScopeMode:          RoleScopeSelf,
		Source:             RoleSourceDirect,
	}); err != nil {
		t.Fatalf("save role assignment: %v", err)
	}

	decision, err := service.DecidePermission(ctx, permissionDecisionViewer(), PermissionDecisionRequestDTO{
		Subject:          ActorRefDTO{ActorType: "agent", ActorID: "agent-ops"},
		Action:           "tool.call",
		ResourceType:     "tool",
		ResourceID:       "tool.deploy",
		OrganizationCode: "01.01",
		RiskLevel:        "high",
		Metadata:         map[string]string{"resource_binding": "allow", "scope": "deploy:prod"},
	})
	if err != nil {
		t.Fatalf("DecidePermission: %v", err)
	}

	assertPermissionDecision(t, decision, PermissionDecisionNeedsHIL, "risk_policy")
	if len(decision.RequiredApprovals) != 1 {
		t.Fatalf("required approvals = %+v, want one", decision.RequiredApprovals)
	}
	requirement := decision.RequiredApprovals[0]
	if requirement.ApproverType != ApprovalTargetOrgOwner || requirement.ApproverRef != "human:dept-owner" {
		t.Fatalf("approval requirement = %+v, want current org owner ref", requirement)
	}
}

func TestPermissionDecisionCouncilPolicyRequiresCouncil(t *testing.T) {
	ctx := context.Background()
	service, store := newPermissionDecisionService(t, ctx)
	seedPermissionDecisionRole(t, ctx, store, "customer.operator", "customer.update", "agent-ops")

	decision, err := service.DecidePermission(ctx, permissionDecisionViewer(), PermissionDecisionRequestDTO{
		Subject:          ActorRefDTO{ActorType: "agent", ActorID: "agent-ops"},
		Action:           "customer.update",
		ResourceType:     "customer",
		ResourceID:       "customer-1",
		OrganizationCode: "01",
		RiskLevel:        "medium",
		Metadata:         map[string]string{"council_required": "true"},
	})
	if err != nil {
		t.Fatalf("DecidePermission: %v", err)
	}

	assertPermissionDecision(t, decision, PermissionDecisionNeedsCouncil, permissionDecisionSourceRiskPolicy)
	if len(decision.RequiredApprovals) != 1 || decision.RequiredApprovals[0].ApprovalType != "council" {
		t.Fatalf("required approvals = %+v, want council", decision.RequiredApprovals)
	}
}

func TestPermissionDecisionCouncilCannotReplaceHighRiskFixedApproval(t *testing.T) {
	ctx := context.Background()
	service, store := newPermissionDecisionService(t, ctx)
	seedPermissionDecisionRole(t, ctx, store, "customer.operator", "customer.delete", "agent-ops")

	decision, err := service.DecidePermission(ctx, permissionDecisionViewer(), PermissionDecisionRequestDTO{
		Subject:          ActorRefDTO{ActorType: "agent", ActorID: "agent-ops"},
		Action:           "customer.delete",
		ResourceType:     "customer",
		ResourceID:       "customer-1",
		OrganizationCode: "01",
		RiskLevel:        "high",
		Metadata:         map[string]string{"council_required": "true"},
	})
	if err != nil {
		t.Fatalf("DecidePermission: %v", err)
	}

	assertPermissionDecision(t, decision, PermissionDecisionNeedsApproval, permissionDecisionSourceRiskPolicy)
	if len(decision.RequiredApprovals) != 1 || decision.RequiredApprovals[0].ApprovalType != "approval" {
		t.Fatalf("required approvals = %+v, want fixed approval", decision.RequiredApprovals)
	}
}

func seedPermissionDecisionRole(t *testing.T, ctx context.Context, store *RoleStore, roleID, action, actorID string) {
	t.Helper()
	if err := store.SaveRoleDefinition(ctx, RoleDefinitionDTO{
		RoleID: roleID, Name: roleID, Category: "resource", Status: organizationStatusActive,
		AssignableActorTypes: []string{"agent"}, PermissionRefs: []string{action},
	}); err != nil {
		t.Fatalf("save role definition: %v", err)
	}
	if err := store.SaveRoleAssignment(ctx, RoleAssignmentDTO{
		AssignmentID: "assign-" + roleID, Actor: ActorRefDTO{ActorType: "agent", ActorID: actorID}, RoleID: roleID,
		OrganizationNodeID: "org-1", OrganizationCode: "01", ScopeMode: RoleScopeSelf, Source: RoleSourceDirect,
	}); err != nil {
		t.Fatalf("save role assignment: %v", err)
	}
}

func TestPermissionDecisionExpiredGrantDoesNotOverrideApprovalDecision(t *testing.T) {
	ctx := context.Background()
	service, _ := newPermissionDecisionService(t, ctx)
	service.SetPermissionGrants([]TemporaryPermissionGrantDTO{{
		GrantID:   "grant-expired",
		AgentID:   "agent-ops",
		ToolID:    "tool.deploy",
		Scope:     "tool.call",
		Reason:    "old window",
		ExpiresAt: time.Now().UTC().Add(-time.Hour),
	}})
	service.SetAuditStore(&permissionDecisionAuditStore{events: []AuditEventDTO{{
		AuditID:    "approval-2",
		ActorID:    "ops-lead",
		EntityType: "approval",
		EntityID:   "approval-tool-call",
		Action:     "approval.tool_access.approved",
		Result:     "succeeded",
		Metadata: map[string]string{
			"actor_type":        "agent",
			"agent_id":          "agent-ops",
			"tool_id":           "tool.deploy",
			"scope":             "tool.call",
			"organization_code": "01",
		},
	}}})

	decision, err := service.DecidePermission(ctx, permissionDecisionViewer(), PermissionDecisionRequestDTO{
		Subject:          ActorRefDTO{ActorType: "agent", ActorID: "agent-ops"},
		Action:           "tool.call",
		ResourceType:     "tool",
		ResourceID:       "tool.deploy",
		OrganizationCode: "01",
		Metadata:         map[string]string{"resource_binding": "allow"},
	})
	if err != nil {
		t.Fatalf("DecidePermission: %v", err)
	}

	assertPermissionDecision(t, decision, "allow", "approval_decision")
}

func TestPermissionDecisionMissingPolicyDenies(t *testing.T) {
	ctx := context.Background()
	service, _ := newPermissionDecisionService(t, ctx)

	decision, err := service.DecidePermission(ctx, permissionDecisionViewer(), PermissionDecisionRequestDTO{
		Subject:          ActorRefDTO{ActorType: "human", ActorID: "profile-unknown"},
		Action:           "org.manage",
		ResourceType:     "organization",
		ResourceID:       "org-1",
		OrganizationCode: "01",
	})
	if err != nil {
		t.Fatalf("DecidePermission: %v", err)
	}

	assertPermissionDecision(t, decision, "deny", "missing_policy")
}

func TestScopedPermissionReadDoesNotImplyUnrelatedOperationsRead(t *testing.T) {
	ctx := context.Background()
	service, _ := newPermissionDecisionService(t, ctx)
	viewer := ViewerRef{ViewerID: "policy-reader", PermissionScope: []string{PermissionPermissionsRead}}

	if _, err := service.DecidePermission(ctx, viewer, PermissionDecisionRequestDTO{
		Subject:      ActorRefDTO{ActorType: "human", ActorID: "profile-unknown"},
		Action:       "org.read",
		ResourceType: "organization",
		ResourceID:   "org-1",
	}); err != nil {
		t.Fatalf("DecidePermission with permissions.read: %v", err)
	}
	if _, err := service.GetOrgGraph(ctx, viewer, OrgGraphQuery{}); err != ErrPermissionDenied {
		t.Fatalf("GetOrgGraph with permissions.read error = %v, want ErrPermissionDenied", err)
	}
}
