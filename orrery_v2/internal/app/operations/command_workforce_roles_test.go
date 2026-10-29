package operations_test

import (
	"context"
	"errors"
	"testing"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/operations"
)

func TestWorkforceRolesFollowPersonnelAndTalentMarketLifecycle(t *testing.T) {
	ctx := context.Background()
	profiles := &fakeWorkforceProfileStore{profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{}}
	roles := newFakeRoleStore()
	orgs := newFakeOrganizationCatalogStore()
	seedOrganizationCatalog(t, ctx, orgs, "orrery", []operations.DepartmentDTO{
		{DepartmentID: "operations", OrganizationID: "orrery", Name: "Operations"},
	}, []operations.PositionDTO{
		{PositionID: "operator", DepartmentID: "operations", Name: "Operator"},
	})
	command := operations.NewCommandService(profiles)
	command.SetOrganizationCatalogStore(orgs)
	command.SetRoleStore(roles)
	command.SetWorkforceSessionRevoker(&fakeWorkforceSessionRevoker{})
	authz := &fakeAuthorizationRelations{}
	command.SetAuthorizationRelations(authz)
	viewer := operations.ViewerRef{ViewerID: "admin", PermissionScope: []string{operations.PermissionWorkforceManage, operations.PermissionRolesManage, operations.PermissionPermissionsManage}}

	_, err := command.UpsertWorkforceMember(ctx, viewer, operations.UpsertWorkforceMemberCommand{
		AgentProfileID: "human-operator",
		DisplayName:    "Operator",
		AgentKind:      "human",
		UserAccount: operations.WorkforceUserAccountDTO{
			Username:       "operator",
			AccountEnabled: true,
			Password:       "correct-horse-1",
		},
		OrganizationID: "orrery",
		DepartmentID:   "operations",
		PositionID:     "operator",
		RoleIDs:        []string{"org.admin", "approval.reviewer"},
		Persona:        operations.WorkforcePersonaDTO{Backstory: "Manages operational access."},
		Enabled:        true,
	})
	if err != nil {
		t.Fatalf("create workforce user: %v", err)
	}
	organizationRole := roles.assignments["workforce:human-operator:org.admin"]
	if organizationRole.OrganizationNodeID != "operations" || organizationRole.OrganizationCode == "" || organizationRole.ScopeMode != operations.RoleScopeSelf {
		t.Fatalf("organization role assignment = %+v", organizationRole)
	}
	platformRole := roles.assignments["workforce:human-operator:approval.reviewer"]
	if platformRole.OrganizationNodeID != "" || platformRole.OrganizationCode != "" || platformRole.ScopeMode != operations.RoleScopePlatform {
		t.Fatalf("platform role assignment = %+v", platformRole)
	}
	assertAuthorizationCall(t, authz.calls, "write_role", "user", "human-operator", "orrery", "operations", "org.admin")
	assertAuthorizationCall(t, authz.calls, "write_role", "user", "human-operator", "global", "", "approval.reviewer")

	_, err = command.UpsertWorkforceMember(ctx, viewer, operations.UpsertWorkforceMemberCommand{
		AgentProfileID: "human-operator",
		RoleIDs:        []string{"org.manager"},
		UpdateMask:     []string{"role_ids"},
	})
	if err != nil {
		t.Fatalf("replace workforce roles: %v", err)
	}
	if _, exists := roles.assignments["workforce:human-operator:org.admin"]; exists {
		t.Fatal("removed org.admin assignment is still stored")
	}
	if _, exists := roles.assignments["workforce:human-operator:approval.reviewer"]; exists {
		t.Fatal("removed platform assignment is still stored")
	}
	if assignment := roles.assignments["workforce:human-operator:org.manager"]; assignment.RoleID != "org.manager" {
		t.Fatalf("replacement assignment = %+v", assignment)
	}

	if err := command.ReleaseWorkforceMember(ctx, viewer, operations.ReleaseWorkforceMemberCommand{AgentProfileID: "human-operator"}); err != nil {
		t.Fatalf("release workforce user: %v", err)
	}
	if len(roles.assignments) != 0 {
		t.Fatalf("release should remove workforce-owned role assignments: %+v", roles.assignments)
	}

	_, err = command.HireTalentFromMarket(ctx, viewer, operations.HireTalentFromMarketCommand{
		AgentProfileID: "human-operator",
		OrganizationID: "orrery",
		DepartmentID:   "operations",
		PositionID:     "operator",
		RoleIDs:        []string{"org.admin"},
	})
	if err != nil {
		t.Fatalf("rehire workforce user: %v", err)
	}
	if assignment := roles.assignments["workforce:human-operator:org.admin"]; assignment.RoleID != "org.admin" {
		t.Fatalf("rehire assignment = %+v", assignment)
	}
}

func TestWorkforceRegistrationRemainsCompatibleWhenRoleStoreIsNotConfigured(t *testing.T) {
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

	_, err := command.UpsertWorkforceMember(ctx, operations.ViewerRef{
		ViewerID:        "admin",
		PermissionScope: []string{operations.PermissionWorkforceManage, operations.PermissionRolesManage, operations.PermissionPermissionsManage},
	}, operations.UpsertWorkforceMemberCommand{
		AgentProfileID: "human-without-role-store",
		DisplayName:    "Legacy Role User",
		AgentKind:      "human",
		UserAccount: operations.WorkforceUserAccountDTO{
			Username:       "legacy-role-user",
			Email:          "legacy@example.test",
			Role:           "architect",
			AccountEnabled: true,
			Password:       "correct-horse-1",
		},
		EmploymentType: "employee",
		OrganizationID: "orrery",
		DepartmentID:   "operations",
		PositionID:     "operator",
		RoleIDs:        []string{"architect"},
		Persona: operations.WorkforcePersonaDTO{
			Backstory:              "Coordinates analytical architecture decisions.",
			ResponsibilityBoundary: "Owns workforce architecture reviews.",
		},
		RuntimeKind: "human",
		Enabled:     true,
	})
	if err != nil {
		t.Fatalf("register workforce user without role store: %v", err)
	}
}

func TestWorkforceUserAccountRejectsDirectPermissionsAndIncompleteCredentials(t *testing.T) {
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
	command.SetRoleStore(newFakeRoleStore())
	viewer := operations.ViewerRef{ViewerID: "admin", PermissionScope: []string{operations.PermissionWorkforceManage, operations.PermissionRolesManage, operations.PermissionPermissionsManage}}
	base := operations.UpsertWorkforceMemberCommand{
		DisplayName:    "Operator",
		AgentKind:      "human",
		OrganizationID: "orrery",
		DepartmentID:   "operations",
		PositionID:     "operator",
		Persona:        operations.WorkforcePersonaDTO{Backstory: "Manages operational access."},
		Enabled:        true,
	}

	directScope := base
	directScope.AgentProfileID = "human-direct-scope"
	directScope.UserAccount = operations.WorkforceUserAccountDTO{
		Username:        "direct-scope",
		AccountEnabled:  true,
		Password:        "correct-horse-1",
		PermissionScope: []string{operations.PermissionOperationsManage},
	}
	if _, err := command.UpsertWorkforceMember(ctx, viewer, directScope); !errors.Is(err, operations.ErrInvalidRequest) {
		t.Fatalf("direct permission scope error = %v, want ErrInvalidRequest", err)
	}

	missingPassword := base
	missingPassword.AgentProfileID = "human-no-password"
	missingPassword.UserAccount = operations.WorkforceUserAccountDTO{Username: "no-password", AccountEnabled: true}
	if _, err := command.UpsertWorkforceMember(ctx, viewer, missingPassword); !errors.Is(err, operations.ErrInvalidRequest) {
		t.Fatalf("missing password error = %v, want ErrInvalidRequest", err)
	}

	ownerRole := base
	ownerRole.AgentProfileID = "human-owner-role"
	ownerRole.UserAccount = operations.WorkforceUserAccountDTO{Username: "owner-role", AccountEnabled: true, Password: "correct-horse-1"}
	ownerRole.RoleIDs = []string{"org.owner"}
	if _, err := command.UpsertWorkforceMember(ctx, viewer, ownerRole); !errors.Is(err, operations.ErrInvalidRequest) {
		t.Fatalf("org.owner assignment error = %v, want ErrInvalidRequest", err)
	}

	roleWithoutPermission := base
	roleWithoutPermission.AgentProfileID = "human-role-denied"
	roleWithoutPermission.UserAccount = operations.WorkforceUserAccountDTO{Username: "role-denied", AccountEnabled: true, Password: "correct-horse-1"}
	roleWithoutPermission.RoleIDs = []string{"member"}
	if _, err := command.UpsertWorkforceMember(ctx, operations.ViewerRef{
		ViewerID:        "workforce-manager",
		PermissionScope: []string{operations.PermissionWorkforceManage},
	}, roleWithoutPermission); !errors.Is(err, operations.ErrPermissionDenied) {
		t.Fatalf("role assignment permission error = %v, want ErrPermissionDenied", err)
	}

	if len(profiles.profiles) != 0 {
		t.Fatalf("invalid user registrations were persisted: %+v", profiles.profiles)
	}
}

func TestWorkforceLegacyAccountScopesRemainUntilRolesAreExplicitlyManaged(t *testing.T) {
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
	command.SetRoleStore(newFakeRoleStore())
	viewer := operations.ViewerRef{ViewerID: "admin", PermissionScope: []string{operations.PermissionWorkforceManage, operations.PermissionRolesManage, operations.PermissionPermissionsManage}}

	_, err := command.UpsertWorkforceMember(ctx, viewer, operations.UpsertWorkforceMemberCommand{
		AgentProfileID: "human-legacy-scope",
		DisplayName:    "Legacy Scope",
		AgentKind:      "human",
		UserAccount: operations.WorkforceUserAccountDTO{
			Username:       "legacy-scope",
			AccountEnabled: true,
			Password:       "correct-horse-1",
		},
		OrganizationID: "orrery",
		DepartmentID:   "operations",
		PositionID:     "operator",
		RoleIDs:        []string{"member"},
		Persona:        operations.WorkforcePersonaDTO{Backstory: "Tests legacy scope migration."},
		Enabled:        true,
	})
	if err != nil {
		t.Fatalf("create workforce user: %v", err)
	}
	profile := profiles.profiles["human-legacy-scope"]
	profile.UserAccount.PermissionScope = []string{operations.PermissionOperationsRead}
	profiles.profiles[profile.ID] = profile

	_, err = command.UpsertWorkforceMember(ctx, viewer, operations.UpsertWorkforceMemberCommand{
		AgentProfileID: "human-legacy-scope",
		UserAccount:    operations.WorkforceUserAccountDTO{Email: "legacy@example.test"},
		UpdateMask:     []string{"user_account.email"},
	})
	if err != nil {
		t.Fatalf("unrelated account update: %v", err)
	}
	if scopes := profiles.profiles["human-legacy-scope"].UserAccount.PermissionScope; len(scopes) != 1 || scopes[0] != operations.PermissionOperationsRead {
		t.Fatalf("unrelated update should preserve legacy account scopes: %+v", scopes)
	}

	_, err = command.UpsertWorkforceMember(ctx, viewer, operations.UpsertWorkforceMemberCommand{
		AgentProfileID: "human-legacy-scope",
		RoleIDs:        []string{"member"},
		UpdateMask:     []string{"role_ids"},
	})
	if err != nil {
		t.Fatalf("explicit role update: %v", err)
	}
	if scopes := profiles.profiles["human-legacy-scope"].UserAccount.PermissionScope; len(scopes) != 0 {
		t.Fatalf("explicit role management should clear legacy account scopes: %+v", scopes)
	}
}

func TestWorkforceExplicitRoleUpdateClearsOrdinaryAccountScopes(t *testing.T) {
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
	command.SetRoleStore(newFakeRoleStore())
	command.SetWorkforceSessionRevoker(&fakeWorkforceSessionRevoker{})
	viewer := operations.ViewerRef{ViewerID: "admin", PermissionScope: []string{operations.PermissionWorkforceManage, operations.PermissionRolesManage, operations.PermissionPermissionsManage}}

	if _, err := command.UpsertWorkforceMember(ctx, viewer, operations.UpsertWorkforceMemberCommand{
		AgentProfileID: "human-scope-admin",
		DisplayName:    "Scope Administrator",
		AgentKind:      "human",
		UserAccount: operations.WorkforceUserAccountDTO{
			Username:       "scope-admin",
			AccountEnabled: true,
			Password:       "correct-horse-1",
		},
		OrganizationID: "orrery",
		DepartmentID:   "operations",
		PositionID:     "operator",
		RoleIDs:        []string{"member"},
		Persona:        operations.WorkforcePersonaDTO{Backstory: "Ordinary administrator used for role migration."},
		Enabled:        true,
	}); err != nil {
		t.Fatalf("create workforce user: %v", err)
	}
	profile := profiles.profiles["human-scope-admin"]
	profile.UserAccount.PermissionScope = []string{operations.PermissionPermissionsManage, operations.PermissionOperationsManage}
	profiles.profiles[profile.ID] = profile

	if _, err := command.UpsertWorkforceMember(ctx, viewer, operations.UpsertWorkforceMemberCommand{
		AgentProfileID: "human-scope-admin",
		RoleIDs:        []string{"member"},
		UpdateMask:     []string{"role_ids"},
	}); err != nil {
		t.Fatalf("explicit role update: %v", err)
	}
	got := profiles.profiles["human-scope-admin"].UserAccount.PermissionScope
	if len(got) != 0 {
		t.Fatalf("explicit role update retained account scopes: %+v", got)
	}
	if err := command.SetWorkforceMemberEnabled(ctx, viewer, operations.SetWorkforceMemberEnabledCommand{
		AgentProfileID: "human-scope-admin",
		Enabled:        false,
	}); err != nil {
		t.Fatalf("disable ordinary profile: %v", err)
	}
	if profiles.profiles["human-scope-admin"].Enabled {
		t.Fatal("ordinary profile remains enabled")
	}
}

func TestReconcileWorkforceRoleAssignmentsMigratesKnownRolesAndRemovesStaleRows(t *testing.T) {
	ctx := context.Background()
	profiles := &fakeWorkforceProfileStore{profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{
		"human-pm": {
			ID:        "human-pm",
			AgentKind: agentcore.AgentKindHuman,
			OrgIdentity: agentcore.AgentOrgIdentity{
				OrganizationRef: agentcore.OrganizationRef{ID: "orrery"},
				DepartmentRef:   agentcore.DepartmentRef{ID: "operations"},
				PositionRef:     agentcore.PositionRef{ID: "operator"},
				RoleRefs:        []agentcore.RoleRef{{ID: "pm"}, {ID: "legacy-human-role"}},
			},
			Enabled: true,
		},
		"agent-manager": {
			ID:        "agent-manager",
			AgentKind: agentcore.AgentKindAI,
			OrgIdentity: agentcore.AgentOrgIdentity{
				OrganizationRef: agentcore.OrganizationRef{ID: "orrery"},
				DepartmentRef:   agentcore.DepartmentRef{ID: "operations"},
				PositionRef:     agentcore.PositionRef{ID: "operator"},
				RoleRefs:        []agentcore.RoleRef{{ID: "org.manager"}},
			},
			Enabled: true,
		},
		"legacy-placement": {
			ID:        "legacy-placement",
			AgentKind: agentcore.AgentKindAI,
			OrgIdentity: agentcore.AgentOrgIdentity{
				OrganizationRef: agentcore.OrganizationRef{ID: "removed-organization"},
				DepartmentRef:   agentcore.DepartmentRef{ID: "removed-department"},
				PositionRef:     agentcore.PositionRef{ID: "removed-position"},
				RoleRefs:        []agentcore.RoleRef{{ID: "org.manager"}},
			},
			Enabled: true,
		},
	}}
	orgs := newFakeOrganizationCatalogStore()
	seedOrganizationCatalog(t, ctx, orgs, "orrery", []operations.DepartmentDTO{{
		DepartmentID: "operations", OrganizationID: "orrery", Name: "Operations",
	}}, []operations.PositionDTO{{
		PositionID: "operator", DepartmentID: "operations", Name: "Operator",
	}})
	roles := newFakeRoleStore()
	roles.assignments["workforce:missing-profile:org.admin"] = operations.RoleAssignmentDTO{
		AssignmentID:       "workforce:missing-profile:org.admin",
		Actor:              operations.ActorRefDTO{ActorType: "human", ActorID: "missing-profile"},
		RoleID:             "org.admin",
		OrganizationNodeID: "operations",
		OrganizationCode:   "0101",
		ScopeMode:          operations.RoleScopeSelf,
		Source:             operations.RoleSourceSystem,
	}
	command := operations.NewCommandService(profiles)
	command.SetOrganizationCatalogStore(orgs)
	command.SetRoleStore(roles)

	if err := command.ReconcileWorkforceRoleAssignments(ctx, "system:test"); err != nil {
		t.Fatalf("ReconcileWorkforceRoleAssignments: %v", err)
	}
	if _, exists := roles.assignments["workforce:missing-profile:org.admin"]; exists {
		t.Fatal("orphan workforce role assignment was not removed")
	}
	if assignment := roles.assignments["workforce:human-pm:pm"]; assignment.ScopeMode != operations.RoleScopePlatform || assignment.AssignedBy != "system:test" {
		t.Fatalf("PM role assignment = %+v", assignment)
	}
	if assignment := roles.assignments["workforce:agent-manager:org.manager"]; assignment.Actor.ActorType != "agent" || assignment.OrganizationNodeID != "operations" {
		t.Fatalf("agent role assignment = %+v", assignment)
	}
	if _, exists := roles.assignments["workforce:legacy-placement:org.manager"]; exists {
		t.Fatal("legacy placement outside the current organization catalog should be skipped during reconciliation")
	}
	if _, exists := roles.assignments["workforce:human-pm:legacy-human-role"]; exists {
		t.Fatal("unknown legacy role should not become an authoritative assignment")
	}
}

func TestReconcileWorkforceRoleAssignmentsPreservesLegacyCrossOrganizationPlacement(t *testing.T) {
	ctx := context.Background()
	profiles := &fakeWorkforceProfileStore{profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{
		"legacy-agent": {
			ID: "legacy-agent", AgentKind: agentcore.AgentKindAI, Enabled: true,
			OrgIdentity: agentcore.AgentOrgIdentity{
				OrganizationRef: agentcore.OrganizationRef{ID: "new-org"},
				DepartmentRef:   agentcore.DepartmentRef{ID: "legacy-department"},
				RoleRefs:        []agentcore.RoleRef{{ID: "org.manager"}},
			},
		},
	}}
	orgs := newFakeOrganizationCatalogStore()
	seedOrganizationCatalog(t, ctx, orgs, "legacy-org", []operations.DepartmentDTO{{
		DepartmentID: "legacy-department", OrganizationID: "legacy-org", Name: "Legacy",
	}}, nil)
	roles := newFakeRoleStore()
	roles.assignments["workforce:legacy-agent:org.manager"] = operations.RoleAssignmentDTO{
		AssignmentID: "workforce:legacy-agent:org.manager", Actor: operations.ActorRefDTO{ActorType: "agent", ActorID: "legacy-agent"},
		RoleID: "org.manager", OrganizationNodeID: "legacy-department", Source: operations.RoleSourceSystem,
	}
	command := operations.NewCommandService(profiles)
	command.SetOrganizationCatalogStore(orgs)
	command.SetRoleStore(roles)

	if err := command.ReconcileWorkforceRoleAssignments(ctx, "system:test"); err != nil {
		t.Fatalf("ReconcileWorkforceRoleAssignments: %v", err)
	}
	if _, exists := roles.assignments["workforce:legacy-agent:org.manager"]; !exists {
		t.Fatal("legacy assignment was removed while its organization placement is being migrated")
	}
}

func TestWorkforceRoleSaveFailureRollsBackNewProfile(t *testing.T) {
	ctx := context.Background()
	profiles := &fakeWorkforceProfileStore{profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{}}
	orgs := newFakeOrganizationCatalogStore()
	seedOrganizationCatalog(t, ctx, orgs, "orrery", []operations.DepartmentDTO{{
		DepartmentID: "operations", OrganizationID: "orrery", Name: "Operations",
	}}, []operations.PositionDTO{{
		PositionID: "operator", DepartmentID: "operations", Name: "Operator",
	}})
	saveErr := errors.New("role store unavailable")
	roles := &failingWorkforceRoleStore{fakeRoleStore: newFakeRoleStore(), saveErr: saveErr}
	command := operations.NewCommandService(profiles)
	command.SetOrganizationCatalogStore(orgs)
	command.SetRoleStore(roles)

	_, err := command.UpsertWorkforceMember(ctx, operations.ViewerRef{
		ViewerID:        "admin",
		PermissionScope: []string{operations.PermissionWorkforceManage, operations.PermissionRolesManage, operations.PermissionPermissionsManage},
	}, operations.UpsertWorkforceMemberCommand{
		AgentProfileID: "human-rollback",
		DisplayName:    "Rollback User",
		AgentKind:      "human",
		UserAccount: operations.WorkforceUserAccountDTO{
			Username:       "rollback-user",
			AccountEnabled: true,
			Password:       "correct-horse-1",
		},
		OrganizationID: "orrery",
		DepartmentID:   "operations",
		PositionID:     "operator",
		RoleIDs:        []string{"org.admin"},
		Persona:        operations.WorkforcePersonaDTO{Backstory: "Exercises rollback behavior."},
		Enabled:        true,
	})
	if !errors.Is(err, saveErr) {
		t.Fatalf("role save error = %v, want %v", err, saveErr)
	}
	if _, exists := profiles.profiles["human-rollback"]; exists {
		t.Fatal("new profile remained after role synchronization failure")
	}
}

type failingWorkforceRoleStore struct {
	*fakeRoleStore
	saveErr error
}

func (f *failingWorkforceRoleStore) SaveRoleAssignment(_ context.Context, _ operations.RoleAssignmentDTO) error {
	return f.saveErr
}
