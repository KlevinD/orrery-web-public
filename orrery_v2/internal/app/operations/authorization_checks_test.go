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

func TestQueryServiceAuthorizesDepartmentWorkforceRead(t *testing.T) {
	ctx := context.Background()
	profiles := &fakeWorkforceProfileStore{profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{}}
	orgs := newFakeOrganizationCatalogStore()
	seedOrganizationCatalog(t, ctx, orgs, "org-1", []operations.DepartmentDTO{
		{DepartmentID: "dept-1", OrganizationID: "org-1", Name: "Operations"},
	}, nil)
	authz := &fakeOperationsAuthorizationChecker{}
	service := operations.NewQueryService(profiles, fakeEmptyToolRegistry{})
	service.SetOrganizationCatalogStore(orgs)
	service.SetAuthorizationChecker(authz)

	_, err := service.ListWorkforceMembers(ctx, operations.ViewerRef{
		ViewerID:        "alice",
		ViewerKind:      "user",
		AgentProfileID:  "human-alice",
		PermissionScope: []string{operations.PermissionWorkforceRead},
	}, operations.WorkforceQuery{DepartmentID: "dept-1"})
	if err != nil {
		t.Fatalf("ListWorkforceMembers: %v", err)
	}

	authz.assertSingleCall(t, "workforce.member.read", "department", "dept-1", "org-1", "human-alice")
}

func TestQueryServicePropagatesOrganizationReadDenial(t *testing.T) {
	ctx := context.Background()
	authz := &fakeOperationsAuthorizationChecker{err: authorizationapp.ErrDenied}
	service := operations.NewQueryService(&fakeWorkforceProfileStore{}, fakeEmptyToolRegistry{})
	service.SetAuthorizationChecker(authz)

	_, err := service.GetOrgGraph(ctx, operations.ViewerRef{
		ViewerID:        "alice",
		AgentProfileID:  "human-alice",
		PermissionScope: []string{operations.PermissionOrgRead},
	}, operations.OrgGraphQuery{OrganizationID: "org-1"})
	if !errors.Is(err, authorizationapp.ErrDenied) {
		t.Fatalf("GetOrgGraph error = %v, want ErrDenied", err)
	}
}

func TestQueryServiceAuthorizesUnfilteredOrganizationGraphAtPlatformScope(t *testing.T) {
	ctx := context.Background()
	authz := &fakeOperationsAuthorizationChecker{err: authorizationapp.ErrDenied}
	service := operations.NewQueryService(&fakeWorkforceProfileStore{}, fakeEmptyToolRegistry{})
	service.SetAuthorizationChecker(authz)

	_, err := service.GetOrgGraph(ctx, operations.ViewerRef{
		ViewerID:           "alice",
		AgentProfileID:     "human-alice",
		PermissionScope:    []string{operations.PermissionOrgRead},
		AuthorizationScope: []string{},
	}, operations.OrgGraphQuery{})
	if !errors.Is(err, authorizationapp.ErrDenied) {
		t.Fatalf("GetOrgGraph error = %v, want ErrDenied", err)
	}
	authz.assertSingleCall(t, operations.PermissionOrgRead, "platform", "global", "global", "human-alice")
}

func TestCommandServiceAuthorizesOrganizationCreationAtPlatformScope(t *testing.T) {
	ctx := context.Background()
	authz := &fakeOperationsAuthorizationChecker{err: authorizationapp.ErrDenied}
	command := operations.NewCommandService(&fakeWorkforceProfileStore{})
	command.SetOrganizationCatalogStore(newFakeOrganizationCatalogStore())
	command.SetAuthorizationChecker(authz)

	_, err := command.CreateOrganization(ctx, operations.ViewerRef{
		ViewerID:           "alice",
		AgentProfileID:     "human-alice",
		PermissionScope:    []string{operations.PermissionOrgManage},
		AuthorizationScope: []string{},
	}, operations.CreateOrganizationCommand{Name: "Unauthorized organization"})
	if !errors.Is(err, authorizationapp.ErrDenied) {
		t.Fatalf("CreateOrganization error = %v, want ErrDenied", err)
	}
	authz.assertSingleCall(t, operations.PermissionOrgManage, "platform", "global", "global", "human-alice")
}

func TestCommandServiceAuthorizesWorkforceUpsertDepartment(t *testing.T) {
	ctx := context.Background()
	profiles := &fakeWorkforceProfileStore{profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{}}
	orgs := newFakeOrganizationCatalogStore()
	seedOrganizationCatalog(t, ctx, orgs, "org-1", []operations.DepartmentDTO{
		{DepartmentID: "dept-1", OrganizationID: "org-1", Name: "Operations"},
	}, []operations.PositionDTO{
		{PositionID: "position-1", DepartmentID: "dept-1", Name: "Operator"},
	})
	authz := &fakeOperationsAuthorizationChecker{}
	command := operations.NewCommandService(profiles)
	command.SetOrganizationCatalogStore(orgs)
	command.SetAuthorizationChecker(authz)

	_, err := command.UpsertWorkforceMember(ctx, operations.ViewerRef{
		ViewerID:        "admin",
		ViewerKind:      "human",
		AgentProfileID:  "human-admin",
		PermissionScope: []string{operations.PermissionWorkforceManage},
	}, operations.UpsertWorkforceMemberCommand{
		AgentProfileID: "profile-agent",
		DisplayName:    "Ops Agent",
		AgentKind:      "ai",
		OrganizationID: "org-1",
		DepartmentID:   "dept-1",
		PositionID:     "position-1",
		Persona:        operations.WorkforcePersonaDTO{ResponsibilityBoundary: "负责组织权限校验。"},
		Enabled:        true,
	})
	if err != nil {
		t.Fatalf("UpsertWorkforceMember: %v", err)
	}

	authz.assertSingleCall(t, "workforce.member.manage", "department", "dept-1", "org-1", "human-admin")
}

func TestCommandServiceAuthorizesRoleAssignmentsByOrganization(t *testing.T) {
	ctx := context.Background()
	profiles := &fakeWorkforceProfileStore{profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{
		"human-owner": {
			ID:        "human-owner",
			AgentKind: agentcore.AgentKindHuman,
			Enabled:   true,
			OrgIdentity: agentcore.AgentOrgIdentity{
				OrganizationRef: agentcore.OrganizationRef{ID: "org-1"},
			},
		},
	}}
	orgs := newFakeOrganizationCatalogStore()
	seedOrganizationCatalog(t, ctx, orgs, "org-1", []operations.DepartmentDTO{
		{DepartmentID: "dept-1", OrganizationID: "org-1", Name: "Operations", Code: "0101"},
	}, nil)
	authz := &fakeOperationsAuthorizationChecker{}
	roles := newFakeRoleStore()
	command := operations.NewCommandService(profiles)
	command.SetOrganizationCatalogStore(orgs)
	command.SetRoleStore(roles)
	command.SetAuthorizationChecker(authz)

	_, err := command.PutRoleAssignments(ctx, operations.ViewerRef{
		ViewerID:        "admin",
		AgentProfileID:  "human-admin",
		PermissionScope: []string{operations.PermissionRolesManage},
	}, operations.PutRoleAssignmentsCommand{Assignments: []operations.RoleAssignmentDTO{{
		Actor:              operations.ActorRefDTO{ActorType: "human", ActorID: "human-owner"},
		RoleID:             "org.owner",
		OrganizationNodeID: "dept-1",
		OrganizationCode:   "0101",
		ScopeMode:          operations.RoleScopeSelfAndDescendants,
	}}})
	if err != nil {
		t.Fatalf("PutRoleAssignments: %v", err)
	}

	authz.assertSingleCall(t, "role.manage", "department", "dept-1", "org-1", "human-admin")
}

func TestCommandServiceAuthorizesAndNormalizesRoleDefinitions(t *testing.T) {
	ctx := context.Background()
	authz := &fakeOperationsAuthorizationChecker{}
	roles := newFakeRoleStore()
	command := operations.NewCommandService(&fakeWorkforceProfileStore{})
	command.SetRoleStore(roles)
	command.SetAuthorizationChecker(authz)

	page, err := command.PutRoleDefinitions(ctx, operations.ViewerRef{
		ViewerID:        "admin",
		ViewerKind:      "user",
		AgentProfileID:  "human-admin",
		PermissionScope: []string{operations.PermissionRolesManage},
	}, operations.PutRoleDefinitionsCommand{Roles: []operations.RoleDefinitionDTO{{
		RoleID:         " Custom.Resource.Owner ",
		Name:           "Resource owner",
		PermissionRefs: []string{"resources.read", "resources.manage"},
	}}})
	if err != nil {
		t.Fatalf("PutRoleDefinitions: %v", err)
	}
	if len(page.Items) != 1 || page.Items[0].RoleID != "custom.resource.owner" || len(page.Items[0].PermissionRefs) != 2 || page.Items[0].PermissionRefs[0] != "resource.read" || page.Items[0].PermissionRefs[1] != "resource.manage" {
		t.Fatalf("roles = %+v", page.Items)
	}
	authz.assertSingleCall(t, operations.PermissionRolesManage, "platform", "global", "global", "human-admin")
}

func TestCommandServiceAuthorizesRoleAssignmentDeletion(t *testing.T) {
	ctx := context.Background()
	orgs := newFakeOrganizationCatalogStore()
	seedOrganizationCatalog(t, ctx, orgs, "org-1", []operations.DepartmentDTO{{
		DepartmentID: "dept-1", OrganizationID: "org-1", Name: "Operations", Code: "0101",
	}}, nil)
	roles := newFakeRoleStore()
	roles.assignments["assign-1"] = operations.RoleAssignmentDTO{
		AssignmentID:       "assign-1",
		Actor:              operations.ActorRefDTO{ActorType: "human", ActorID: "human-owner"},
		RoleID:             "org.owner",
		OrganizationNodeID: "dept-1",
		OrganizationCode:   "0101",
		ScopeMode:          operations.RoleScopeSelfAndDescendants,
	}
	authz := &fakeOperationsAuthorizationChecker{}
	command := operations.NewCommandService(&fakeWorkforceProfileStore{})
	command.SetOrganizationCatalogStore(orgs)
	command.SetRoleStore(roles)
	command.SetAuthorizationChecker(authz)

	err := command.DeleteRoleAssignment(ctx, operations.ViewerRef{
		ViewerID:        "admin",
		ViewerKind:      "user",
		AgentProfileID:  "human-admin",
		PermissionScope: []string{operations.PermissionRolesManage},
	}, "assign-1")
	if err != nil {
		t.Fatalf("DeleteRoleAssignment: %v", err)
	}
	if _, ok := roles.assignments["assign-1"]; ok {
		t.Fatalf("assignment was not deleted: %+v", roles.assignments)
	}
	authz.assertSingleCall(t, "role.manage", "department", "dept-1", "org-1", "human-admin")
}

func TestCommandServiceRejectsMismatchedRoleAssignmentNodeAndCode(t *testing.T) {
	ctx := context.Background()
	orgs := newFakeOrganizationCatalogStore()
	seedOrganizationCatalog(t, ctx, orgs, "org-1", []operations.DepartmentDTO{
		{DepartmentID: "dept-1", OrganizationID: "org-1", Name: "Operations", Code: "0101"},
		{DepartmentID: "dept-2", OrganizationID: "org-1", Name: "Sales", Code: "0102"},
	}, nil)
	roles := newFakeRoleStore()
	command := operations.NewCommandService(&fakeWorkforceProfileStore{})
	command.SetOrganizationCatalogStore(orgs)
	command.SetRoleStore(roles)

	_, err := command.PutRoleAssignments(ctx, operations.ViewerRef{
		ViewerID:        "admin",
		AgentProfileID:  "human-admin",
		PermissionScope: []string{operations.PermissionRolesManage},
	}, operations.PutRoleAssignmentsCommand{Assignments: []operations.RoleAssignmentDTO{{
		Actor:              operations.ActorRefDTO{ActorType: "human", ActorID: "human-owner"},
		RoleID:             "org.owner",
		OrganizationNodeID: "dept-2",
		OrganizationCode:   "0101",
		ScopeMode:          operations.RoleScopeSelf,
	}}})
	if !errors.Is(err, operations.ErrInvalidRequest) {
		t.Fatalf("PutRoleAssignments error = %v, want ErrInvalidRequest", err)
	}
	if len(roles.assignments) != 0 {
		t.Fatalf("mismatched role assignment was saved: %+v", roles.assignments)
	}
}

func TestCommandServiceRejectsReservedRoleAssignmentFieldsAndUnknownRoles(t *testing.T) {
	ctx := context.Background()
	orgs := newFakeOrganizationCatalogStore()
	seedOrganizationCatalog(t, ctx, orgs, "org-1", []operations.DepartmentDTO{{
		DepartmentID: "dept-1", OrganizationID: "org-1", Name: "Operations", Code: "0101",
	}}, nil)
	tests := []struct {
		name      string
		roleID    string
		actorType string
		source    string
	}{
		{name: "reserved system source", roleID: "org.owner", actorType: "human", source: operations.RoleSourceSystem},
		{name: "unknown role", roleID: "unknown.admin", actorType: "human"},
		{name: "unsupported actor for role", roleID: "security.admin", actorType: "agent"},
		{name: "unsupported squad actor", roleID: "org.owner", actorType: "squad"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			roles := newFakeRoleStore()
			command := operations.NewCommandService(&fakeWorkforceProfileStore{})
			command.SetOrganizationCatalogStore(orgs)
			command.SetRoleStore(roles)
			_, err := command.PutRoleAssignments(ctx, operations.ViewerRef{
				ViewerID:        "admin",
				AgentProfileID:  "human-admin",
				PermissionScope: []string{operations.PermissionRolesManage},
			}, operations.PutRoleAssignmentsCommand{Assignments: []operations.RoleAssignmentDTO{{
				Actor:              operations.ActorRefDTO{ActorType: tt.actorType, ActorID: "actor-1"},
				RoleID:             tt.roleID,
				OrganizationNodeID: "dept-1",
				OrganizationCode:   "0101",
				ScopeMode:          operations.RoleScopeSelf,
				Source:             tt.source,
			}}})
			if !errors.Is(err, operations.ErrInvalidRequest) {
				t.Fatalf("PutRoleAssignments error = %v, want ErrInvalidRequest", err)
			}
			if len(roles.assignments) != 0 {
				t.Fatalf("invalid role assignment was saved: %+v", roles.assignments)
			}
		})
	}
}

func TestCommandServiceRejectsMissingRoleAssignmentActor(t *testing.T) {
	ctx := context.Background()
	orgs := newFakeOrganizationCatalogStore()
	seedOrganizationCatalog(t, ctx, orgs, "org-1", []operations.DepartmentDTO{{
		DepartmentID: "dept-1", OrganizationID: "org-1", Name: "Operations", Code: "0101",
	}}, nil)
	roles := newFakeRoleStore()
	command := operations.NewCommandService(&fakeWorkforceProfileStore{profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{}})
	command.SetOrganizationCatalogStore(orgs)
	command.SetRoleStore(roles)
	_, err := command.PutRoleAssignments(ctx, operations.ViewerRef{
		ViewerID:        "admin",
		PermissionScope: []string{operations.PermissionRolesManage},
	}, operations.PutRoleAssignmentsCommand{Assignments: []operations.RoleAssignmentDTO{{
		Actor:              operations.ActorRefDTO{ActorType: "human", ActorID: "future-profile"},
		RoleID:             "org.owner",
		OrganizationNodeID: "dept-1",
		OrganizationCode:   "0101",
		ScopeMode:          operations.RoleScopeSelf,
	}}})
	if !errors.Is(err, operations.ErrInvalidRequest) {
		t.Fatalf("PutRoleAssignments error = %v, want ErrInvalidRequest", err)
	}
	if len(roles.assignments) != 0 {
		t.Fatalf("missing actor assignment was saved: %+v", roles.assignments)
	}
}

func TestCommandServiceRejectsCrossOrganizationRoleAssignmentActor(t *testing.T) {
	ctx := context.Background()
	orgs := newFakeOrganizationCatalogStore()
	seedOrganizationCatalog(t, ctx, orgs, "org-1", []operations.DepartmentDTO{{
		DepartmentID: "dept-1", OrganizationID: "org-1", Name: "Operations", Code: "0101",
	}}, nil)
	seedOrganizationCatalog(t, ctx, orgs, "org-2", []operations.DepartmentDTO{{
		DepartmentID: "dept-2", OrganizationID: "org-2", Name: "External", Code: "0201",
	}}, nil)
	profiles := &fakeWorkforceProfileStore{profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{
		"human-external": {
			ID:        "human-external",
			AgentKind: agentcore.AgentKindHuman,
			Enabled:   true,
			OrgIdentity: agentcore.AgentOrgIdentity{
				OrganizationRef: agentcore.OrganizationRef{ID: "org-2"},
			},
		},
	}}
	roles := newFakeRoleStore()
	command := operations.NewCommandService(profiles)
	command.SetOrganizationCatalogStore(orgs)
	command.SetRoleStore(roles)
	viewer := operations.ViewerRef{ViewerID: "admin", PermissionScope: []string{operations.PermissionRolesManage}}

	_, err := command.PutRoleAssignments(ctx, viewer, operations.PutRoleAssignmentsCommand{Assignments: []operations.RoleAssignmentDTO{{
		Actor:              operations.ActorRefDTO{ActorType: "human", ActorID: "human-external"},
		RoleID:             "org.admin",
		OrganizationNodeID: "dept-1",
		OrganizationCode:   "0101",
		ScopeMode:          operations.RoleScopeSelf,
	}}})
	if !errors.Is(err, operations.ErrInvalidRequest) {
		t.Fatalf("cross-organization role assignment error = %v, want ErrInvalidRequest", err)
	}
	if len(roles.assignments) != 0 {
		t.Fatalf("cross-organization role assignment was saved: %+v", roles.assignments)
	}

	_, err = command.PutRoleAssignments(ctx, viewer, operations.PutRoleAssignmentsCommand{Assignments: []operations.RoleAssignmentDTO{{
		Actor:     operations.ActorRefDTO{ActorType: "human", ActorID: "human-external"},
		RoleID:    "security.admin",
		ScopeMode: operations.RoleScopePlatform,
	}}})
	if err != nil {
		t.Fatalf("platform role assignment for internal actor: %v", err)
	}
}

func TestCommandServiceRejectsCrossOrganizationOwner(t *testing.T) {
	ctx := context.Background()
	orgs := newFakeOrganizationCatalogStore()
	seedOrganizationCatalog(t, ctx, orgs, "org-1", []operations.DepartmentDTO{{
		DepartmentID: "dept-1", OrganizationID: "org-1", Name: "Operations", Code: "0101",
	}}, nil)
	profiles := &fakeWorkforceProfileStore{profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{
		"human-external-owner": {
			ID:        "human-external-owner",
			AgentKind: agentcore.AgentKindHuman,
			Enabled:   true,
			OrgIdentity: agentcore.AgentOrgIdentity{
				OrganizationRef: agentcore.OrganizationRef{ID: "org-2"},
			},
		},
	}}
	roles := newFakeRoleStore()
	command := operations.NewCommandService(profiles)
	command.SetOrganizationCatalogStore(orgs)
	command.SetRoleStore(roles)

	_, err := command.UpdateOrganizationOwner(ctx, operations.ViewerRef{
		ViewerID:        "admin",
		PermissionScope: []string{operations.PermissionOrgManage},
	}, operations.UpdateOrganizationOwnerCommand{
		OrganizationNodeID: "org-1",
		OwnerProfileID:     "human-external-owner",
	})
	if !errors.Is(err, operations.ErrInvalidRequest) {
		t.Fatalf("cross-organization owner error = %v, want ErrInvalidRequest", err)
	}
	if owner := orgs.organizations["org-1"].OwnerProfileID; owner != "" {
		t.Fatalf("cross-organization owner was saved: %q", owner)
	}
	if len(roles.assignments) != 0 {
		t.Fatalf("cross-organization owner role was saved: %+v", roles.assignments)
	}
}

func TestCommandServiceRejectsInactiveRoleAssignmentActors(t *testing.T) {
	ctx := context.Background()
	orgs := newFakeOrganizationCatalogStore()
	seedOrganizationCatalog(t, ctx, orgs, "org-1", []operations.DepartmentDTO{{
		DepartmentID: "dept-1", OrganizationID: "org-1", Name: "Operations", Code: "0101",
	}}, nil)
	tests := []struct {
		name    string
		profile agentcore.AgentProfile
	}{
		{
			name:    "disabled profile",
			profile: agentcore.AgentProfile{ID: "human-owner", AgentKind: agentcore.AgentKindHuman, Enabled: false},
		},
		{
			name: "talent market profile",
			profile: agentcore.AgentProfile{
				ID:        "human-owner",
				AgentKind: agentcore.AgentKindHuman,
				Enabled:   true,
				Placement: agentcore.TalentPlacement{Status: agentcore.TalentPlacementMarket},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			roles := newFakeRoleStore()
			profiles := &fakeWorkforceProfileStore{profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{
				"human-owner": tt.profile,
			}}
			command := operations.NewCommandService(profiles)
			command.SetOrganizationCatalogStore(orgs)
			command.SetRoleStore(roles)

			_, err := command.PutRoleAssignments(ctx, operations.ViewerRef{
				ViewerID:        "admin",
				PermissionScope: []string{operations.PermissionRolesManage},
			}, operations.PutRoleAssignmentsCommand{Assignments: []operations.RoleAssignmentDTO{{
				Actor:              operations.ActorRefDTO{ActorType: "human", ActorID: "human-owner"},
				RoleID:             "org.owner",
				OrganizationNodeID: "dept-1",
				OrganizationCode:   "0101",
				ScopeMode:          operations.RoleScopeSelf,
			}}})
			if !errors.Is(err, operations.ErrInvalidRequest) {
				t.Fatalf("PutRoleAssignments error = %v, want ErrInvalidRequest", err)
			}
			if len(roles.assignments) != 0 {
				t.Fatalf("inactive actor assignment was saved: %+v", roles.assignments)
			}
		})
	}
}

func TestCommandServiceRejectsBuiltInRoleOverrideAndNonAtomicBatchStore(t *testing.T) {
	command := operations.NewCommandService(&fakeWorkforceProfileStore{})
	roles := newFakeRoleStore()
	command.SetRoleStore(roles)
	viewer := operations.ViewerRef{ViewerID: "admin", PermissionScope: []string{operations.PermissionRolesManage}}
	_, err := command.PutRoleDefinitions(context.Background(), viewer, operations.PutRoleDefinitionsCommand{Roles: []operations.RoleDefinitionDTO{{
		RoleID: "security.admin", Name: "Backdoor", PermissionRefs: []string{"*"},
	}}})
	if !errors.Is(err, operations.ErrInvalidRequest) {
		t.Fatalf("built-in override error = %v, want ErrInvalidRequest", err)
	}
	_, err = command.PutRoleDefinitions(context.Background(), viewer, operations.PutRoleDefinitionsCommand{Roles: []operations.RoleDefinitionDTO{
		{RoleID: "custom.one", Name: "One"},
		{RoleID: "custom.two", Name: "Two"},
	}})
	if !errors.Is(err, operations.ErrServiceDependency) {
		t.Fatalf("non-atomic batch error = %v, want ErrServiceDependency", err)
	}
}

func TestCommandServiceRejectsDuplicateRoleAssignmentIDsBeforeWriting(t *testing.T) {
	ctx := context.Background()
	orgs := newFakeOrganizationCatalogStore()
	seedOrganizationCatalog(t, ctx, orgs, "org-1", []operations.DepartmentDTO{{
		DepartmentID: "dept-1", OrganizationID: "org-1", Name: "Operations", Code: "0101",
	}}, nil)
	roles := newFakeRoleStore()
	command := operations.NewCommandService(&fakeWorkforceProfileStore{})
	command.SetOrganizationCatalogStore(orgs)
	command.SetRoleStore(roles)
	assignment := operations.RoleAssignmentDTO{
		AssignmentID:       "duplicate-id",
		Actor:              operations.ActorRefDTO{ActorType: "human", ActorID: "human-owner"},
		RoleID:             "org.owner",
		OrganizationNodeID: "dept-1",
		OrganizationCode:   "0101",
		ScopeMode:          operations.RoleScopeSelf,
	}

	_, err := command.PutRoleAssignments(ctx, operations.ViewerRef{
		ViewerID:        "admin",
		AgentProfileID:  "human-admin",
		PermissionScope: []string{operations.PermissionRolesManage},
	}, operations.PutRoleAssignmentsCommand{Assignments: []operations.RoleAssignmentDTO{assignment, assignment}})
	if !errors.Is(err, operations.ErrInvalidRequest) {
		t.Fatalf("PutRoleAssignments error = %v, want ErrInvalidRequest", err)
	}
	if len(roles.assignments) != 0 {
		t.Fatalf("duplicate role assignment batch wrote partial state: %+v", roles.assignments)
	}
}

func TestCommandServiceRejectsSystemRoleAssignmentDeletion(t *testing.T) {
	roles := newFakeRoleStore()
	roles.assignments["owner:org-1"] = operations.RoleAssignmentDTO{
		AssignmentID: "owner:org-1",
		Actor:        operations.ActorRefDTO{ActorType: "human", ActorID: "human-owner"},
		RoleID:       "org.owner",
		Source:       operations.RoleSourceSystem,
	}
	command := operations.NewCommandService(&fakeWorkforceProfileStore{})
	command.SetRoleStore(roles)

	err := command.DeleteRoleAssignment(context.Background(), operations.ViewerRef{
		ViewerID:        "admin",
		PermissionScope: []string{operations.PermissionRolesManage},
	}, "owner:org-1")
	if !errors.Is(err, operations.ErrInvalidRequest) {
		t.Fatalf("DeleteRoleAssignment error = %v, want ErrInvalidRequest", err)
	}
	if _, ok := roles.assignments["owner:org-1"]; !ok {
		t.Fatal("system assignment was deleted")
	}
}

type fakeOperationsAuthorizationCall struct {
	principal identity.Principal
	action    string
	object    identity.ObjectRef
}

type fakeOperationsAuthorizationChecker struct {
	calls []fakeOperationsAuthorizationCall
	err   error
}

func (f *fakeOperationsAuthorizationChecker) Can(_ context.Context, principal identity.Principal, action string, object identity.ObjectRef) error {
	f.calls = append(f.calls, fakeOperationsAuthorizationCall{principal: principal, action: action, object: object})
	return f.err
}

func (f *fakeOperationsAuthorizationChecker) assertSingleCall(t *testing.T, action string, objectType string, objectID string, tenantID string, subjectID string) {
	t.Helper()
	if len(f.calls) != 1 {
		t.Fatalf("authorization calls = %d, want 1: %+v", len(f.calls), f.calls)
	}
	call := f.calls[0]
	if call.action != action {
		t.Fatalf("authorization action = %q, want %q", call.action, action)
	}
	if call.object.Type != objectType || call.object.ID != objectID || call.object.TenantID != tenantID {
		t.Fatalf("authorization object = %+v, want type=%s id=%s tenant=%s", call.object, objectType, objectID, tenantID)
	}
	if call.principal.UserID != subjectID || call.principal.Subject != subjectID {
		t.Fatalf("authorization principal subject = user_id:%q subject:%q, want %q", call.principal.UserID, call.principal.Subject, subjectID)
	}
}
