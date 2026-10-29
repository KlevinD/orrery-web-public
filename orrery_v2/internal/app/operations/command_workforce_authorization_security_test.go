package operations_test

import (
	"context"
	"errors"
	"testing"

	agentcore "orrery_v2/internal/agent/core"
	authorizationapp "orrery_v2/internal/app/authorization"
	"orrery_v2/internal/app/identity"
	"orrery_v2/internal/app/operations"
)

func TestWorkforceMoveAuthorizesSourceAndDestinationRoleScopes(t *testing.T) {
	ctx := context.Background()
	command, profiles, roles := newWorkforceMoveAuthorizationService(t, ctx)
	authz := &fakeOperationsAuthorizationChecker{}
	command.SetAuthorizationChecker(authz)

	_, err := command.UpsertWorkforceMember(ctx, workforceMoveViewer(), operations.UpsertWorkforceMemberCommand{
		AgentProfileID: "agent-mover",
		OrganizationID: "org-b",
		DepartmentID:   "dept-b",
		PositionID:     "position-b",
		UpdateMask:     []string{"organization_id", "department_id", "position_id"},
	})
	if err != nil {
		t.Fatalf("move workforce member: %v", err)
	}
	assertAuthorizationCallExists(t, authz.calls, authorizationappCall{action: "workforce.member.manage", objectType: "department", objectID: "dept-a", tenantID: "org-a"})
	assertAuthorizationCallExists(t, authz.calls, authorizationappCall{action: "workforce.member.manage", objectType: "department", objectID: "dept-b", tenantID: "org-b"})
	assertAuthorizationCallExists(t, authz.calls, authorizationappCall{action: "role.manage", objectType: "department", objectID: "dept-a", tenantID: "org-a"})
	assertAuthorizationCallExists(t, authz.calls, authorizationappCall{action: "role.manage", objectType: "department", objectID: "dept-b", tenantID: "org-b"})
	if profile := profiles.profiles["agent-mover"]; profile.OrgIdentity.OrganizationRef.ID != "org-b" {
		t.Fatalf("moved profile = %+v", profile.OrgIdentity)
	}
	if assignment := roles.assignments["workforce:agent-mover:member"]; assignment.OrganizationNodeID != "dept-b" {
		t.Fatalf("moved role assignment = %+v", assignment)
	}
}

func TestWorkforceMoveDenialDoesNotDetachSourceRole(t *testing.T) {
	ctx := context.Background()
	command, profiles, roles := newWorkforceMoveAuthorizationService(t, ctx)
	command.SetAuthorizationChecker(workforceMoveAuthorizationChecker{})

	_, err := command.UpsertWorkforceMember(ctx, workforceMoveViewer(), operations.UpsertWorkforceMemberCommand{
		AgentProfileID: "agent-mover",
		OrganizationID: "org-b",
		DepartmentID:   "dept-b",
		PositionID:     "position-b",
		UpdateMask:     []string{"organization_id", "department_id", "position_id"},
	})
	if !errors.Is(err, authorizationapp.ErrDenied) {
		t.Fatalf("move error = %v, want authorization denial", err)
	}
	if profile := profiles.profiles["agent-mover"]; profile.OrgIdentity.OrganizationRef.ID != "org-a" {
		t.Fatalf("denied move changed profile = %+v", profile.OrgIdentity)
	}
	if assignment := roles.assignments["workforce:agent-mover:member"]; assignment.OrganizationNodeID != "dept-a" {
		t.Fatalf("denied move changed role assignment = %+v", assignment)
	}
}

func TestWorkforceReleaseRequiresAuthorizationForRemovedPlatformRole(t *testing.T) {
	ctx := context.Background()
	command, profiles, roles := newWorkforceMoveAuthorizationService(t, ctx)
	if _, err := command.UpsertWorkforceMember(ctx, workforceMoveViewer(), operations.UpsertWorkforceMemberCommand{
		AgentProfileID: "agent-mover",
		RoleIDs:        []string{"member", "approval.reviewer"},
		UpdateMask:     []string{"role_ids"},
	}); err != nil {
		t.Fatalf("seed platform role: %v", err)
	}
	command.SetAuthorizationChecker(platformRoleRemovalDenyChecker{})

	err := command.ReleaseWorkforceMember(ctx, workforceMoveViewer(), operations.ReleaseWorkforceMemberCommand{AgentProfileID: "agent-mover"})
	if !errors.Is(err, authorizationapp.ErrDenied) {
		t.Fatalf("ReleaseWorkforceMember error = %v, want platform role authorization denial", err)
	}
	if profile := profiles.profiles["agent-mover"]; !profile.Placement.IsInternal(profile.OrgIdentity) {
		t.Fatalf("denied release changed profile placement = %+v", profile.Placement)
	}
	if assignment := roles.assignments["workforce:agent-mover:approval.reviewer"]; assignment.ScopeMode != operations.RoleScopePlatform {
		t.Fatalf("denied release removed platform role assignment = %+v", assignment)
	}
}

func TestWorkforcePasswordResetRequiresPlatformSecurityAuthorization(t *testing.T) {
	ctx := context.Background()
	profiles := &fakeWorkforceProfileStore{profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{}}
	orgs := newFakeOrganizationCatalogStore()
	seedOrganizationCatalog(t, ctx, orgs, "orrery", []operations.DepartmentDTO{{
		DepartmentID: "operations", OrganizationID: "orrery", Name: "Operations",
	}}, []operations.PositionDTO{{
		PositionID: "operator", DepartmentID: "operations", Name: "Operator",
	}})
	command := operations.NewCommandService(profiles)
	command.SetOrganizationCatalogStore(orgs)
	if _, err := command.UpsertWorkforceMember(ctx, operations.ViewerRef{
		ViewerID: "bootstrap", PermissionScope: []string{operations.PermissionWorkforceManage, operations.PermissionRolesManage, operations.PermissionPermissionsManage},
	}, operations.UpsertWorkforceMemberCommand{
		AgentProfileID: "human-operator",
		DisplayName:    "Operator",
		AgentKind:      "human",
		UserAccount: operations.WorkforceUserAccountDTO{
			Username: "operator", AccountEnabled: true, Password: "initial-password-1",
		},
		OrganizationID: "orrery",
		DepartmentID:   "operations",
		PositionID:     "operator",
		Persona:        operations.WorkforcePersonaDTO{Backstory: "Operates the service."},
		Enabled:        true,
	}); err != nil {
		t.Fatalf("seed workforce account: %v", err)
	}
	previousVerifier := profiles.profiles["human-operator"].UserAccount.PasswordVerifier
	command.SetAuthorizationChecker(platformSecurityDenyChecker{})

	_, err := command.UpsertWorkforceMember(ctx, operations.ViewerRef{
		ViewerID:        "workforce-admin",
		AgentProfileID:  "human-workforce-admin",
		PermissionScope: []string{operations.PermissionWorkforceManage, operations.PermissionPermissionsManage},
	}, operations.UpsertWorkforceMemberCommand{
		AgentProfileID: "human-operator",
		UserAccount:    operations.WorkforceUserAccountDTO{Password: "replacement-password-1"},
		UpdateMask:     []string{"user_account.password"},
	})
	if !errors.Is(err, authorizationapp.ErrDenied) {
		t.Fatalf("password reset error = %v, want platform security authorization denial", err)
	}
	if got := profiles.profiles["human-operator"].UserAccount.PasswordVerifier; got != previousVerifier {
		t.Fatalf("denied password reset changed verifier: before=%q after=%q", previousVerifier, got)
	}
}

func TestWorkforceRoleChangeAcceptsScopedRoleAuthorization(t *testing.T) {
	ctx := context.Background()
	command, profiles, roles := newWorkforceMoveAuthorizationService(t, ctx)
	authz := &fakeOperationsAuthorizationChecker{}
	command.SetAuthorizationChecker(authz)

	_, err := command.UpsertWorkforceMember(ctx, operations.ViewerRef{
		ViewerID:        "workforce-manager",
		AgentProfileID:  "human-workforce-manager",
		PermissionScope: []string{operations.PermissionWorkforceManage},
	}, operations.UpsertWorkforceMemberCommand{
		AgentProfileID: "agent-mover",
		RoleIDs:        []string{"org.manager"},
		UpdateMask:     []string{"role_ids"},
	})
	if err != nil {
		t.Fatalf("replace workforce role through scoped authorization: %v", err)
	}
	assertAuthorizationCallExists(t, authz.calls, authorizationappCall{action: "role.manage", objectType: "department", objectID: "dept-a", tenantID: "org-a"})
	if profile := profiles.profiles["agent-mover"]; len(profile.OrgIdentity.RoleRefs) != 1 || profile.OrgIdentity.RoleRefs[0].ID != "org.manager" {
		t.Fatalf("updated profile roles = %+v", profile.OrgIdentity.RoleRefs)
	}
	if _, exists := roles.assignments["workforce:agent-mover:member"]; exists {
		t.Fatal("previous workforce role assignment remains stored")
	}
	if assignment := roles.assignments["workforce:agent-mover:org.manager"]; assignment.RoleID != "org.manager" {
		t.Fatalf("replacement role assignment = %+v", assignment)
	}
}

func newWorkforceMoveAuthorizationService(t *testing.T, ctx context.Context) (*operations.CommandService, *fakeWorkforceProfileStore, *fakeRoleStore) {
	t.Helper()
	profiles := &fakeWorkforceProfileStore{profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{}}
	roles := newFakeRoleStore()
	orgs := newFakeOrganizationCatalogStore()
	seedOrganizationCatalog(t, ctx, orgs, "org-a", []operations.DepartmentDTO{{DepartmentID: "dept-a", OrganizationID: "org-a", Name: "A"}}, []operations.PositionDTO{{PositionID: "position-a", DepartmentID: "dept-a", Name: "A"}})
	seedOrganizationCatalog(t, ctx, orgs, "org-b", []operations.DepartmentDTO{{DepartmentID: "dept-b", OrganizationID: "org-b", Name: "B"}}, []operations.PositionDTO{{PositionID: "position-b", DepartmentID: "dept-b", Name: "B"}})
	command := operations.NewCommandService(profiles)
	command.SetOrganizationCatalogStore(orgs)
	command.SetRoleStore(roles)
	_, err := command.UpsertWorkforceMember(ctx, workforceMoveViewer(), operations.UpsertWorkforceMemberCommand{
		AgentProfileID: "agent-mover",
		DisplayName:    "Mover",
		AgentKind:      "ai",
		OrganizationID: "org-a",
		DepartmentID:   "dept-a",
		PositionID:     "position-a",
		RoleIDs:        []string{"member"},
		Persona:        operations.WorkforcePersonaDTO{Backstory: "Moves only with source and destination authorization."},
		Enabled:        true,
	})
	if err != nil {
		t.Fatalf("seed workforce member: %v", err)
	}
	return command, profiles, roles
}

func workforceMoveViewer() operations.ViewerRef {
	return operations.ViewerRef{
		ViewerID:        "admin",
		AgentProfileID:  "human-admin",
		PermissionScope: []string{operations.PermissionWorkforceManage, operations.PermissionRolesManage},
	}
}

type workforceMoveAuthorizationChecker struct{}

func (workforceMoveAuthorizationChecker) Can(_ context.Context, _ identity.Principal, action string, object identity.ObjectRef) error {
	if action == "role.manage" && object.Type == "department" && object.ID == "dept-a" {
		return authorizationapp.ErrDenied
	}
	return nil
}

type platformRoleRemovalDenyChecker struct{}

func (platformRoleRemovalDenyChecker) Can(_ context.Context, _ identity.Principal, action string, object identity.ObjectRef) error {
	if action == "role.manage" && object.Type == "platform" {
		return authorizationapp.ErrDenied
	}
	return nil
}

type platformSecurityDenyChecker struct{}

func (platformSecurityDenyChecker) Can(_ context.Context, _ identity.Principal, action string, object identity.ObjectRef) error {
	if action == operations.PermissionPermissionsManage && object.Type == "platform" {
		return authorizationapp.ErrDenied
	}
	return nil
}

type authorizationappCall struct {
	action     string
	objectType string
	objectID   string
	tenantID   string
}

func assertAuthorizationCallExists(t *testing.T, calls []fakeOperationsAuthorizationCall, want authorizationappCall) {
	t.Helper()
	for _, call := range calls {
		if call.action == want.action && call.object.Type == want.objectType && call.object.ID == want.objectID && call.object.TenantID == want.tenantID {
			return
		}
	}
	t.Fatalf("authorization call %+v not found in %+v", want, calls)
}
