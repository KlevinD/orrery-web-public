package operations

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	authorizationapp "orrery_v2/internal/app/authorization"
	"orrery_v2/internal/app/identity"
)

func TestRolePermissionAuthorizerHonorsAssignmentScope(t *testing.T) {
	now := time.Date(2026, 7, 13, 10, 0, 0, 0, time.UTC)
	orgs := roleAuthorizerOrganizationStore{graph: roleAuthorizerOrganizationGraph()}
	principal := identity.Principal{
		SubjectProvider: "local",
		Subject:         "operator",
		Username:        "operator",
		AgentProfileID:  "human-operator",
		ViewerKind:      identity.ViewerKindUser,
	}

	tests := []struct {
		name       string
		scopeMode  string
		objectType string
		objectID   string
		want       bool
	}{
		{name: "self node", scopeMode: RoleScopeSelf, objectType: "department", objectID: "dept-ops", want: true},
		{name: "self excludes descendant", scopeMode: RoleScopeSelf, objectType: "department", objectID: "dept-platform", want: false},
		{name: "self excludes sibling", scopeMode: RoleScopeSelf, objectType: "department", objectID: "dept-sales", want: false},
		{name: "self and descendants includes descendant", scopeMode: RoleScopeSelfAndDescendants, objectType: "department", objectID: "dept-platform", want: true},
		{name: "self and descendants excludes sibling", scopeMode: RoleScopeSelfAndDescendants, objectType: "department", objectID: "dept-sales", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			roles := &roleAuthorizerRoleStore{assignments: []RoleAssignmentDTO{{
				AssignmentID:       "assignment-1",
				Actor:              ActorRefDTO{ActorType: "human", ActorID: "human-operator"},
				RoleID:             "org.admin",
				OrganizationNodeID: "dept-ops",
				OrganizationCode:   "0101",
				ScopeMode:          tt.scopeMode,
				StartsAt:           now.Add(-time.Hour),
				ExpiresAt:          now.Add(time.Hour),
			}}}
			authorizer := NewRolePermissionAuthorizer(roles, orgs, activeRoleAuthorizationProfileStore())
			authorizer.now = func() time.Time { return now }

			allowed, err := authorizer.AuthorizeRolePermission(context.Background(), principal, PermissionOrgManage, []identity.ObjectRef{{Type: tt.objectType, ID: tt.objectID}})
			if err != nil {
				t.Fatalf("AuthorizeRolePermission: %v", err)
			}
			if allowed != tt.want {
				t.Fatalf("allowed = %v, want %v", allowed, tt.want)
			}
		})
	}
}

func TestRolePermissionAuthorizerRejectsScopedRoleForPlatformObject(t *testing.T) {
	authorizer := NewRolePermissionAuthorizer(&roleAuthorizerRoleStore{assignments: []RoleAssignmentDTO{{
		Actor:            ActorRefDTO{ActorType: "human", ActorID: "human-operator"},
		RoleID:           "security.admin",
		OrganizationCode: "0101",
		ScopeMode:        RoleScopeSelfAndDescendants,
	}}}, roleAuthorizerOrganizationStore{graph: roleAuthorizerOrganizationGraph()}, activeRoleAuthorizationProfileStore())

	allowed, err := authorizer.AuthorizeRolePermission(context.Background(), identity.Principal{
		AgentProfileID: "human-operator",
		ViewerKind:     identity.ViewerKindUser,
	}, PermissionPermissionsManage, []identity.ObjectRef{{Type: "platform", ID: "global", TenantID: "global"}})
	if err != nil {
		t.Fatalf("AuthorizeRolePermission: %v", err)
	}
	if allowed {
		t.Fatalf("organization-scoped role must not authorize a platform-global object")
	}
}

func TestRolePermissionAuthorizerPreservesCanonicalProfileIDCase(t *testing.T) {
	profiles := &roleAuthorizationProfileStore{profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{
		"H000001": {
			ID:        "H000001",
			AgentKind: agentcore.AgentKindHuman,
			Enabled:   true,
			UserAccount: agentcore.UserAccountSpec{
				Username:       "ding",
				AccountEnabled: true,
			},
			OrgIdentity: agentcore.AgentOrgIdentity{
				OrganizationRef: agentcore.OrganizationRef{ID: "org-1"},
			},
		},
	}}
	authorizer := NewRolePermissionAuthorizer(&roleAuthorizerRoleStore{assignments: []RoleAssignmentDTO{{
		Actor:     ActorRefDTO{ActorType: "human", ActorID: "h000001"},
		RoleID:    "security.admin",
		ScopeMode: RoleScopePlatform,
	}}}, roleAuthorizerOrganizationStore{graph: roleAuthorizerOrganizationGraph()}, profiles)

	allowed, err := authorizer.AuthorizeRolePermission(context.Background(), identity.Principal{
		AgentProfileID: "H000001",
		ViewerKind:     identity.ViewerKindUser,
	}, PermissionPermissionsManage, []identity.ObjectRef{{Type: "platform", ID: "global"}})
	if err != nil {
		t.Fatalf("AuthorizeRolePermission: %v", err)
	}
	if !allowed {
		t.Fatal("mixed-case profile ID and role assignment should authorize")
	}
}

func TestRolePermissionAuthorizerRejectsForgedTenantForUnresolvedObjects(t *testing.T) {
	authorizer := NewRolePermissionAuthorizer(&roleAuthorizerRoleStore{assignments: []RoleAssignmentDTO{
		{
			Actor:            ActorRefDTO{ActorType: "human", ActorID: "human-operator"},
			RoleID:           "resource.owner",
			OrganizationCode: "0101",
			ScopeMode:        RoleScopeSelfAndDescendants,
		},
		{
			Actor:            ActorRefDTO{ActorType: "human", ActorID: "human-operator"},
			RoleID:           "agent.operator",
			OrganizationCode: "0101",
			ScopeMode:        RoleScopeSelfAndDescendants,
		},
	}}, roleAuthorizerOrganizationStore{graph: roleAuthorizerOrganizationGraph()}, activeRoleAuthorizationProfileStore())
	principal := identity.Principal{AgentProfileID: "human-operator", ViewerKind: identity.ViewerKindUser}
	for _, test := range []struct {
		permission string
		object     identity.ObjectRef
	}{
		{permission: "resource.manage", object: identity.ObjectRef{Type: "resource", ID: "other-tenant-tool", TenantID: "org-1"}},
		{permission: "agent.run", object: identity.ObjectRef{Type: "mission", ID: "other-tenant-mission", TenantID: "org-1"}},
	} {
		allowed, err := authorizer.AuthorizeRolePermission(context.Background(), principal, test.permission, []identity.ObjectRef{test.object})
		if err != nil {
			t.Fatalf("AuthorizeRolePermission(%s): %v", test.object.Type, err)
		}
		if allowed {
			t.Fatalf("forged tenant authorized %s %s", test.permission, test.object.ID)
		}
	}
}

func TestRolePermissionAuthorizerAllowsExplicitPlatformRoleOnlyForPlatformObject(t *testing.T) {
	authorizer := NewRolePermissionAuthorizer(&roleAuthorizerRoleStore{assignments: []RoleAssignmentDTO{{
		Actor:     ActorRefDTO{ActorType: "human", ActorID: "human-operator"},
		RoleID:    "security.admin",
		ScopeMode: RoleScopePlatform,
	}}}, roleAuthorizerOrganizationStore{graph: roleAuthorizerOrganizationGraph()}, activeRoleAuthorizationProfileStore())
	allowed, err := authorizer.AuthorizeRolePermission(context.Background(), identity.Principal{
		AgentProfileID: "human-operator",
		ViewerKind:     identity.ViewerKindUser,
	}, PermissionPermissionsManage, []identity.ObjectRef{{Type: "platform", ID: "global", TenantID: "global"}})
	if err != nil {
		t.Fatalf("AuthorizeRolePermission: %v", err)
	}
	if !allowed {
		t.Fatal("explicit platform role should authorize its platform permission")
	}
}

func TestRolePermissionAuthorizerUsesPlatformRoleForWorkforceObject(t *testing.T) {
	profiles := activeRoleAuthorizationProfileStore()
	profiles.profiles["human-reviewer"] = agentcore.AgentProfile{
		ID:        "human-reviewer",
		AgentKind: agentcore.AgentKindHuman,
		Enabled:   true,
		UserAccount: agentcore.UserAccountSpec{
			Username:       "reviewer",
			AccountEnabled: true,
		},
		OrgIdentity: agentcore.AgentOrgIdentity{
			OrganizationRef: agentcore.OrganizationRef{ID: "org-1"},
			DepartmentRef:   agentcore.DepartmentRef{ID: "dept-sales"},
		},
	}
	principal := identity.Principal{
		AgentProfileID: "human-operator",
		ViewerKind:     identity.ViewerKindUser,
	}
	tests := []struct {
		name       string
		roleID     string
		permission string
		want       bool
	}{
		{name: "platform admin reads workforce", roleID: RoleIDPlatformAdmin, permission: PermissionWorkforceRead, want: true},
		{name: "platform admin manages workforce", roleID: RoleIDPlatformAdmin, permission: PermissionWorkforceManage, want: true},
		{name: "basic user cannot manage workforce", roleID: RoleIDBasicUser, permission: PermissionWorkforceManage, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			authorizer := NewRolePermissionAuthorizer(&roleAuthorizerRoleStore{assignments: []RoleAssignmentDTO{{
				Actor:     ActorRefDTO{ActorType: "human", ActorID: "human-operator"},
				RoleID:    tt.roleID,
				ScopeMode: RoleScopePlatform,
			}}}, roleAuthorizerOrganizationStore{graph: roleAuthorizerOrganizationGraph()}, profiles)

			allowed, err := authorizer.AuthorizeRolePermission(context.Background(), principal, tt.permission, []identity.ObjectRef{{
				Type: "workforce",
				ID:   "human-reviewer",
			}})
			if err != nil {
				t.Fatalf("AuthorizeRolePermission: %v", err)
			}
			if allowed != tt.want {
				t.Fatalf("allowed = %v, want %v", allowed, tt.want)
			}
		})
	}
}

func TestRolePermissionAuthorizerUsesPlatformAdminAcrossOrganizations(t *testing.T) {
	principal := identity.Principal{AgentProfileID: "human-operator", ViewerKind: identity.ViewerKindUser}
	for _, test := range []struct {
		name   string
		roleID string
		want   bool
	}{
		{name: "platform admin", roleID: RoleIDPlatformAdmin, want: true},
		{name: "basic user", roleID: RoleIDBasicUser, want: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			authorizer := NewRolePermissionAuthorizer(&roleAuthorizerRoleStore{assignments: []RoleAssignmentDTO{{
				Actor:     ActorRefDTO{ActorType: "human", ActorID: "human-operator"},
				RoleID:    test.roleID,
				ScopeMode: RoleScopePlatform,
			}}}, roleAuthorizerOrganizationStore{graph: roleAuthorizerOrganizationGraph()}, activeRoleAuthorizationProfileStore())

			allowed, err := authorizer.AuthorizeRolePermission(context.Background(), principal, PermissionOrgRead, []identity.ObjectRef{{
				Type:     "organization",
				ID:       "org-1",
				TenantID: "org-1",
			}})
			if err != nil {
				t.Fatalf("AuthorizeRolePermission: %v", err)
			}
			if allowed != test.want {
				t.Fatalf("allowed = %v, want %v", allowed, test.want)
			}
		})
	}
}

func TestRolePermissionAuthorizerLoadsOrganizationGraphLazily(t *testing.T) {
	roles := &roleAuthorizerRoleStore{assignments: []RoleAssignmentDTO{
		{
			AssignmentID: "platform-reader",
			Actor:        ActorRefDTO{ActorType: "human", ActorID: "human-operator"},
			RoleID:       RoleIDBasicUser,
			ScopeMode:    RoleScopePlatform,
		},
		{
			AssignmentID:       "org-admin",
			Actor:              ActorRefDTO{ActorType: "human", ActorID: "human-operator"},
			RoleID:             "org.admin",
			OrganizationNodeID: "dept-ops",
			OrganizationCode:   "0101",
			ScopeMode:          RoleScopeSelfAndDescendants,
		},
	}}
	orgs := &countingRoleAuthorizerOrganizationStore{graph: roleAuthorizerOrganizationGraph()}
	authorizer := NewRolePermissionAuthorizer(roles, orgs, activeRoleAuthorizationProfileStore())
	principal := identity.Principal{AgentProfileID: "human-operator", ViewerKind: identity.ViewerKindUser}

	allowed, err := authorizer.AuthorizeRolePermission(context.Background(), principal, PermissionWorkforceRead, []identity.ObjectRef{{
		Type: "workforce",
		ID:   "human-reviewer",
	}})
	if err != nil || !allowed {
		t.Fatalf("workforce authorization allowed=%v err=%v", allowed, err)
	}
	if orgs.calls != 0 {
		t.Fatalf("platform workforce authorization loaded organization graph %d times", orgs.calls)
	}

	allowed, err = authorizer.AuthorizeRolePermission(context.Background(), principal, PermissionOrgRead, []identity.ObjectRef{
		{Type: "department", ID: "dept-ops"},
		{Type: "department", ID: "dept-platform"},
	})
	if err != nil || !allowed {
		t.Fatalf("organization authorization allowed=%v err=%v", allowed, err)
	}
	if orgs.calls != 1 {
		t.Fatalf("organization authorization loaded graph %d times, want 1", orgs.calls)
	}
}

func TestRolePermissionAuthorizerAllowsPlatformRoleToManagePlatformAssignment(t *testing.T) {
	assignments := []RoleAssignmentDTO{
		{
			AssignmentID: "caller-security-role",
			Actor:        ActorRefDTO{ActorType: "human", ActorID: "human-operator"},
			RoleID:       "security.admin",
			ScopeMode:    RoleScopePlatform,
		},
		{
			AssignmentID: "target-platform-role",
			Actor:        ActorRefDTO{ActorType: "human", ActorID: "human-reviewer"},
			RoleID:       "audit.viewer",
			ScopeMode:    RoleScopePlatform,
		},
	}
	authorizer := NewRolePermissionAuthorizer(&roleAuthorizerRoleStore{assignments: assignments}, roleAuthorizerOrganizationStore{graph: roleAuthorizerOrganizationGraph()}, activeRoleAuthorizationProfileStore())
	allowed, err := authorizer.AuthorizeRolePermission(context.Background(), identity.Principal{
		AgentProfileID: "human-operator",
		ViewerKind:     identity.ViewerKindUser,
	}, PermissionRolesManage, []identity.ObjectRef{{Type: "role_assignment", ID: "target-platform-role"}})
	if err != nil {
		t.Fatalf("AuthorizeRolePermission: %v", err)
	}
	if !allowed {
		t.Fatal("platform security role should manage a platform-scoped role assignment")
	}
}

func TestRolePermissionAuthorizerDoesNotMatchHumanRoleByUsername(t *testing.T) {
	authorizer := NewRolePermissionAuthorizer(&roleAuthorizerRoleStore{assignments: []RoleAssignmentDTO{{
		Actor:              ActorRefDTO{ActorType: "human", ActorID: "operator"},
		RoleID:             "org.admin",
		OrganizationNodeID: "dept-ops",
		OrganizationCode:   "0101",
		ScopeMode:          RoleScopeSelf,
	}}}, roleAuthorizerOrganizationStore{graph: roleAuthorizerOrganizationGraph()}, activeRoleAuthorizationProfileStore())

	allowed, err := authorizer.AuthorizeRolePermission(context.Background(), identity.Principal{
		Username:       "operator",
		AgentProfileID: "human-operator",
		ViewerKind:     identity.ViewerKindUser,
	}, PermissionOrgManage, []identity.ObjectRef{{Type: "department", ID: "dept-ops"}})
	if err != nil {
		t.Fatalf("AuthorizeRolePermission: %v", err)
	}
	if allowed {
		t.Fatalf("username-only role assignment must not match an authoritative human profile ID")
	}
}

func TestRolePermissionAuthorizerRestrictsBearerRolePermissionsToCredentialScopes(t *testing.T) {
	authorizer := NewRolePermissionAuthorizer(&roleAuthorizerRoleStore{assignments: []RoleAssignmentDTO{{
		Actor:              ActorRefDTO{ActorType: "human", ActorID: "human-operator"},
		RoleID:             "org.admin",
		OrganizationNodeID: "dept-ops",
		OrganizationCode:   "0101",
		ScopeMode:          RoleScopeSelf,
	}}}, roleAuthorizerOrganizationStore{graph: roleAuthorizerOrganizationGraph()}, activeRoleAuthorizationProfileStore())
	principal := identity.Principal{
		AgentProfileID:            "human-operator",
		ViewerKind:                identity.ViewerKindUser,
		CredentialScopes:          []string{PermissionOrgRead},
		CredentialScopeRestricted: true,
	}

	allowed, err := authorizer.AuthorizeRolePermission(context.Background(), principal, PermissionOrgManage, []identity.ObjectRef{{Type: "department", ID: "dept-ops"}})
	if err != nil {
		t.Fatalf("AuthorizeRolePermission: %v", err)
	}
	if allowed {
		t.Fatalf("bearer credential scope must constrain role-derived permissions")
	}
}

func TestRolePermissionAuthorizerRejectsDisabledActor(t *testing.T) {
	profiles := activeRoleAuthorizationProfileStore()
	profile := profiles.profiles["human-operator"]
	profile.Enabled = false
	profiles.profiles[profile.ID] = profile
	authorizer := NewRolePermissionAuthorizer(&roleAuthorizerRoleStore{assignments: []RoleAssignmentDTO{{
		Actor:     ActorRefDTO{ActorType: "human", ActorID: "human-operator"},
		RoleID:    "security.admin",
		ScopeMode: RoleScopePlatform,
	}}}, roleAuthorizerOrganizationStore{graph: roleAuthorizerOrganizationGraph()}, profiles)

	allowed, err := authorizer.AuthorizeRolePermission(context.Background(), identity.Principal{
		AgentProfileID: "human-operator",
		ViewerKind:     identity.ViewerKindUser,
	}, PermissionPermissionsManage, []identity.ObjectRef{{Type: "platform", ID: "global", TenantID: "global"}})
	if err != nil {
		t.Fatalf("AuthorizeRolePermission: %v", err)
	}
	if allowed {
		t.Fatal("disabled role actor retained role-derived permission")
	}
}

func TestRolePermissionAuthorizerHonorsTrustedInternalServiceScopes(t *testing.T) {
	ctx := identity.ContextWithTrustedInternalService(context.Background())
	authorizer := &RolePermissionAuthorizer{}
	allowed, err := authorizer.AuthorizeRolePermission(ctx, identity.Principal{
		SubjectProvider: "transport",
		Subject:         "scheduler",
		ViewerKind:      identity.ViewerKindAgent,
		CoarseScopes:    []string{"scheduler.write"},
	}, "scheduler.write", []identity.ObjectRef{{Type: "platform", ID: "global"}})
	if err != nil {
		t.Fatalf("AuthorizeRolePermission: %v", err)
	}
	if !allowed {
		t.Fatal("trusted internal service scope was denied")
	}
}

func TestRolePermissionAuthorizerRejectsDisabledHumanAccount(t *testing.T) {
	profiles := activeRoleAuthorizationProfileStore()
	profile := profiles.profiles["human-operator"]
	profile.UserAccount.AccountEnabled = false
	profiles.profiles[profile.ID] = profile
	authorizer := NewRolePermissionAuthorizer(&roleAuthorizerRoleStore{assignments: []RoleAssignmentDTO{{
		Actor:     ActorRefDTO{ActorType: "human", ActorID: "human-operator"},
		RoleID:    "security.admin",
		ScopeMode: RoleScopePlatform,
	}}}, roleAuthorizerOrganizationStore{graph: roleAuthorizerOrganizationGraph()}, profiles)

	allowed, err := authorizer.AuthorizeRolePermission(context.Background(), identity.Principal{
		AgentProfileID: "human-operator",
		ViewerKind:     identity.ViewerKindUser,
	}, PermissionPermissionsManage, []identity.ObjectRef{{Type: "platform", ID: "global", TenantID: "global"}})
	if err != nil {
		t.Fatalf("AuthorizeRolePermission: %v", err)
	}
	if allowed {
		t.Fatal("disabled human account retained role-derived permission")
	}
}

func TestRolePermissionAuthorizerRejectsActorFromAnotherOrganization(t *testing.T) {
	profiles := activeRoleAuthorizationProfileStore()
	profile := profiles.profiles["human-operator"]
	profile.OrgIdentity.OrganizationRef.ID = "org-2"
	profiles.profiles[profile.ID] = profile
	authorizer := NewRolePermissionAuthorizer(&roleAuthorizerRoleStore{assignments: []RoleAssignmentDTO{{
		Actor:              ActorRefDTO{ActorType: "human", ActorID: "human-operator"},
		RoleID:             "org.admin",
		OrganizationNodeID: "dept-ops",
		OrganizationCode:   "0101",
		ScopeMode:          RoleScopeSelf,
	}}}, roleAuthorizerOrganizationStore{graph: roleAuthorizerOrganizationGraph()}, profiles)

	allowed, err := authorizer.AuthorizeRolePermission(context.Background(), identity.Principal{
		AgentProfileID: "human-operator",
		ViewerKind:     identity.ViewerKindUser,
	}, PermissionOrgManage, []identity.ObjectRef{{Type: "department", ID: "dept-ops", TenantID: "org-1"}})
	if err != nil {
		t.Fatalf("AuthorizeRolePermission: %v", err)
	}
	if allowed {
		t.Fatal("organization role followed an actor into another organization")
	}
}

func TestRolePermissionAuthorizerRejectsStaleOrganizationAssignment(t *testing.T) {
	authorizer := NewRolePermissionAuthorizer(&roleAuthorizerRoleStore{assignments: []RoleAssignmentDTO{{
		Actor:              ActorRefDTO{ActorType: "human", ActorID: "human-operator"},
		RoleID:             "org.admin",
		OrganizationNodeID: "deleted-department",
		OrganizationCode:   "0101",
		ScopeMode:          RoleScopeSelf,
	}}}, roleAuthorizerOrganizationStore{graph: roleAuthorizerOrganizationGraph()}, activeRoleAuthorizationProfileStore())

	allowed, err := authorizer.AuthorizeRolePermission(context.Background(), identity.Principal{
		AgentProfileID: "human-operator",
		ViewerKind:     identity.ViewerKindUser,
	}, PermissionOrgManage, []identity.ObjectRef{{Type: "department", ID: "dept-ops", TenantID: "org-1"}})
	if err != nil {
		t.Fatalf("AuthorizeRolePermission: %v", err)
	}
	if allowed {
		t.Fatal("stale role assignment authorized a reused organization code")
	}
}

func TestRolePermissionAuthorizerAllowsActiveBreakGlassDirectScope(t *testing.T) {
	authorizer := NewRolePermissionAuthorizer(nil, roleAuthorizerOrganizationStore{}, nil)
	principal := identity.Principal{
		AgentProfileID: "system-superadmin",
		ViewerKind:     identity.ViewerKindUser,
		CoarseScopes:   []string{PermissionPermissionsManage},
	}

	allowed, err := authorizer.AuthorizeRolePermission(context.Background(), principal, PermissionPermissionsManage, []identity.ObjectRef{{Type: "platform", ID: "global"}})
	if err != nil {
		t.Fatalf("AuthorizeRolePermission: %v", err)
	}
	if !allowed {
		t.Fatal("active break-glass principal should retain its direct coarse permission")
	}

	principal.CoarseScopes = nil
	allowed, err = authorizer.AuthorizeRolePermission(context.Background(), principal, PermissionPermissionsManage, []identity.ObjectRef{{Type: "platform", ID: "global"}})
	if err != nil {
		t.Fatalf("AuthorizeRolePermission without direct scope: %v", err)
	}
	if allowed {
		t.Fatal("break-glass identity without the direct coarse permission must be denied")
	}
}

func TestRoleAwareAuthorizationCheckerAppliesScopedRoleInsideApplicationService(t *testing.T) {
	roles := NewRolePermissionAuthorizer(&roleAuthorizerRoleStore{assignments: []RoleAssignmentDTO{{
		Actor:              ActorRefDTO{ActorType: "human", ActorID: "human-operator"},
		RoleID:             "org.admin",
		OrganizationNodeID: "dept-ops",
		OrganizationCode:   "0101",
		ScopeMode:          RoleScopeSelf,
	}}}, roleAuthorizerOrganizationStore{graph: roleAuthorizerOrganizationGraph()}, activeRoleAuthorizationProfileStore())
	checker := NewRoleAwareAuthorizationChecker(deniedRoleAuthorizerBase{}, roles)

	err := checker.Can(context.Background(), identity.Principal{
		AgentProfileID: "human-operator",
		ViewerKind:     identity.ViewerKindUser,
	}, authorizationActionDepartmentManage, identity.ObjectRef{Type: "department", ID: "dept-ops", TenantID: "org-1"})
	if err != nil {
		t.Fatalf("Can scoped department: %v", err)
	}

	err = checker.Can(context.Background(), identity.Principal{
		AgentProfileID: "human-operator",
		ViewerKind:     identity.ViewerKindUser,
	}, authorizationActionDepartmentManage, identity.ObjectRef{Type: "department", ID: "dept-sales", TenantID: "org-1"})
	if !errors.Is(err, authorizationapp.ErrDenied) {
		t.Fatalf("Can sibling department error = %v, want authorization denied", err)
	}
}

func TestRoleAwareAuthorizationCheckerDoesNotLetBaseScopeBypassRoleBoundary(t *testing.T) {
	roles := NewRolePermissionAuthorizer(&roleAuthorizerRoleStore{assignments: []RoleAssignmentDTO{{
		Actor:              ActorRefDTO{ActorType: "human", ActorID: "human-operator"},
		RoleID:             "org.admin",
		OrganizationNodeID: "dept-ops",
		OrganizationCode:   "0101",
		ScopeMode:          RoleScopeSelf,
	}}}, roleAuthorizerOrganizationStore{graph: roleAuthorizerOrganizationGraph()}, activeRoleAuthorizationProfileStore())
	checker := NewRoleAwareAuthorizationChecker(allowedRoleAuthorizerBase{}, roles)

	err := checker.Can(context.Background(), identity.Principal{
		AgentProfileID: "human-operator",
		ViewerKind:     identity.ViewerKindUser,
	}, authorizationActionDepartmentManage, identity.ObjectRef{Type: "department", ID: "dept-sales", TenantID: "org-1"})
	if !errors.Is(err, authorizationapp.ErrDenied) {
		t.Fatalf("Can sibling department error = %v, want authorization denied", err)
	}
}

func TestRoleAwareAuthorizationCheckerFailsClosedWithoutRoleAuthorizer(t *testing.T) {
	checker := NewRoleAwareAuthorizationChecker(allowedRoleAuthorizerBase{}, nil)
	err := checker.Can(context.Background(), identity.Principal{
		AgentProfileID: "human-operator",
		ViewerKind:     identity.ViewerKindUser,
	}, authorizationActionDepartmentManage, identity.ObjectRef{Type: "department", ID: "dept-ops", TenantID: "org-1"})
	if !errors.Is(err, ErrServiceDependency) {
		t.Fatalf("Can without role authorizer error = %v, want ErrServiceDependency", err)
	}
}

func TestCommandRoleAssignmentReplacementRequiresPreviousScope(t *testing.T) {
	ctx := context.Background()
	orgs := roleAuthorizerOrganizationStore{graph: roleAuthorizerOrganizationGraph()}
	roles := &roleAuthorizerRoleStore{assignments: []RoleAssignmentDTO{
		{
			AssignmentID:       "caller-role",
			Actor:              ActorRefDTO{ActorType: "human", ActorID: "human-operator"},
			RoleID:             "org.admin",
			OrganizationNodeID: "dept-ops",
			OrganizationCode:   "0101",
			ScopeMode:          RoleScopeSelf,
		},
		{
			AssignmentID:       "replace-me",
			Actor:              ActorRefDTO{ActorType: "human", ActorID: "human-reviewer"},
			RoleID:             "org.admin",
			OrganizationNodeID: "dept-sales",
			OrganizationCode:   "0102",
			ScopeMode:          RoleScopeSelf,
			Source:             RoleSourceDirect,
		},
	}}
	roleAuthorizer := NewRolePermissionAuthorizer(roles, orgs, activeRoleAuthorizationProfileStore())
	base := authorizationapp.NewService(authorizationapp.NewScopeAdapter(nil))
	command := NewCommandService(&roleAuthorizationProfileStore{profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{
		"human-reviewer": {
			ID:        "human-reviewer",
			AgentKind: agentcore.AgentKindHuman,
			Enabled:   true,
			OrgIdentity: agentcore.AgentOrgIdentity{
				OrganizationRef: agentcore.OrganizationRef{ID: "org-1"},
			},
		},
	}})
	command.SetOrganizationCatalogStore(orgs)
	command.SetRoleStore(roles)
	command.SetAuthorizationChecker(NewRoleAwareAuthorizationChecker(base, roleAuthorizer))

	_, err := command.PutRoleAssignments(ctx, ViewerRef{
		ViewerID:           "operator",
		ViewerKind:         "user",
		AgentProfileID:     "human-operator",
		PermissionScope:    []string{PermissionRolesManage},
		AuthorizationScope: []string{},
	}, PutRoleAssignmentsCommand{Assignments: []RoleAssignmentDTO{{
		AssignmentID:       "replace-me",
		Actor:              ActorRefDTO{ActorType: "human", ActorID: "human-reviewer"},
		RoleID:             "org.admin",
		OrganizationNodeID: "dept-ops",
		OrganizationCode:   "0101",
		ScopeMode:          RoleScopeSelf,
	}}})
	if !errors.Is(err, authorizationapp.ErrDenied) {
		t.Fatalf("PutRoleAssignments error = %v, want authorization denied for previous scope", err)
	}
}

type deniedRoleAuthorizerBase struct{}

func (deniedRoleAuthorizerBase) Can(context.Context, identity.Principal, string, identity.ObjectRef) error {
	return fmt.Errorf("%w: fixture denied", authorizationapp.ErrDenied)
}

type allowedRoleAuthorizerBase struct{}

func (allowedRoleAuthorizerBase) Can(context.Context, identity.Principal, string, identity.ObjectRef) error {
	return nil
}

type roleAuthorizerRoleStore struct {
	roles       []RoleDefinitionDTO
	assignments []RoleAssignmentDTO
}

type roleAuthorizationProfileStore struct {
	profiles map[agentcore.AgentProfileID]agentcore.AgentProfile
}

func activeRoleAuthorizationProfileStore() *roleAuthorizationProfileStore {
	return &roleAuthorizationProfileStore{profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{
		"human-operator": {
			ID:        "human-operator",
			AgentKind: agentcore.AgentKindHuman,
			Enabled:   true,
			UserAccount: agentcore.UserAccountSpec{
				Username:       "operator",
				AccountEnabled: true,
			},
			OrgIdentity: agentcore.AgentOrgIdentity{
				OrganizationRef: agentcore.OrganizationRef{ID: "org-1"},
				DepartmentRef:   agentcore.DepartmentRef{ID: "dept-ops"},
			},
		},
	}}
}

func (s *roleAuthorizationProfileStore) GetProfile(_ context.Context, id agentcore.AgentProfileID) (agentcore.AgentProfile, error) {
	profile, ok := s.profiles[id]
	if !ok {
		return agentcore.AgentProfile{}, ErrNotFound
	}
	return profile, nil
}

func (s *roleAuthorizationProfileStore) SaveProfile(_ context.Context, profile agentcore.AgentProfile) error {
	if s.profiles == nil {
		s.profiles = map[agentcore.AgentProfileID]agentcore.AgentProfile{}
	}
	s.profiles[profile.ID] = profile
	return nil
}

func (*roleAuthorizationProfileStore) SaveAvailability(context.Context, agentcore.AgentAvailability) error {
	return nil
}

func (s *roleAuthorizerRoleStore) ListRoleDefinitions(context.Context) ([]RoleDefinitionDTO, error) {
	return append([]RoleDefinitionDTO(nil), s.roles...), nil
}

func (s *roleAuthorizerRoleStore) SaveRoleDefinition(context.Context, RoleDefinitionDTO) error {
	return nil
}

func (s *roleAuthorizerRoleStore) ListRoleAssignments(context.Context, RoleAssignmentQuery) ([]RoleAssignmentDTO, error) {
	return append([]RoleAssignmentDTO(nil), s.assignments...), nil
}

func (s *roleAuthorizerRoleStore) SaveRoleAssignment(context.Context, RoleAssignmentDTO) error {
	return nil
}

func (s *roleAuthorizerRoleStore) DeleteRoleAssignment(context.Context, string) error {
	return nil
}

type roleAuthorizerOrganizationStore struct {
	graph OrgGraphDTO
}

type countingRoleAuthorizerOrganizationStore struct {
	graph OrgGraphDTO
	calls int
}

func (s *countingRoleAuthorizerOrganizationStore) ListOrganizations(context.Context, OrgGraphQuery) (OrgGraphDTO, error) {
	s.calls++
	return s.graph, nil
}

func (s roleAuthorizerOrganizationStore) ListOrganizations(context.Context, OrgGraphQuery) (OrgGraphDTO, error) {
	return s.graph, nil
}

func (roleAuthorizerOrganizationStore) SaveOrganization(context.Context, OrganizationDTO) error {
	return nil
}

func (roleAuthorizerOrganizationStore) SaveDepartment(context.Context, DepartmentDTO) error {
	return nil
}

func (roleAuthorizerOrganizationStore) SavePosition(context.Context, PositionDTO) error {
	return nil
}

func (roleAuthorizerOrganizationStore) DeleteOrganization(context.Context, string) error {
	return nil
}

func (roleAuthorizerOrganizationStore) DeleteDepartment(context.Context, string) error {
	return nil
}

func (roleAuthorizerOrganizationStore) DeletePosition(context.Context, string) error {
	return nil
}

func roleAuthorizerOrganizationGraph() OrgGraphDTO {
	return OrgGraphDTO{
		Organizations: []OrganizationDTO{{OrganizationID: "org-1", Code: "01", Status: "active"}},
		Departments: []DepartmentDTO{
			{DepartmentID: "dept-ops", OrganizationID: "org-1", Code: "0101", Status: "active"},
			{DepartmentID: "dept-platform", OrganizationID: "org-1", ParentDepartmentID: "dept-ops", Code: "010101", Status: "active"},
			{DepartmentID: "dept-sales", OrganizationID: "org-1", Code: "0102", Status: "active"},
		},
	}
}
