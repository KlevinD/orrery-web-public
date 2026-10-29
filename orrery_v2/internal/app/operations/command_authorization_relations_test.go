package operations_test

import (
	"context"
	"testing"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/authorization"
	"orrery_v2/internal/app/operations"
)

func TestCommandServiceWritesAuthorizationRelationsForOrgAndWorkforceLifecycle(t *testing.T) {
	ctx := context.Background()
	store := &fakeWorkforceProfileStore{profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{
		"profile-owner": {
			ID:        "profile-owner",
			AgentKind: agentcore.AgentKindHuman,
			Enabled:   true,
			OrgIdentity: agentcore.AgentOrgIdentity{
				OrganizationRef: agentcore.OrganizationRef{ID: "orrery"},
			},
		},
	}}
	orgs := newFakeOrganizationCatalogStore()
	authz := &fakeAuthorizationRelations{}
	command := operations.NewCommandService(store)
	command.SetOrganizationCatalogStore(orgs)
	command.SetAuthorizationRelations(authz)
	viewer := operations.ViewerRef{ViewerID: "admin", ViewerKind: "user", PermissionScope: []string{operations.PermissionOrgManage, operations.PermissionWorkforceManage, operations.PermissionRolesManage}}

	organization, err := command.CreateOrganization(ctx, viewer, operations.CreateOrganizationCommand{Name: "Platform"})
	if err != nil {
		t.Fatalf("CreateOrganization: %v", err)
	}
	unit, err := command.CreateOrganizationUnit(ctx, viewer, operations.CreateOrganizationUnitCommand{OrganizationID: organization.OrganizationID, Name: "Research", DefaultPositionName: "Operator"})
	if err != nil {
		t.Fatalf("CreateOrganizationUnit: %v", err)
	}
	_, err = command.UpsertWorkforceMember(ctx, viewer, operations.UpsertWorkforceMemberCommand{
		AgentProfileID: "profile-agent",
		DisplayName:    "Ops",
		AgentKind:      "ai",
		OrganizationID: organization.OrganizationID,
		DepartmentID:   unit.DepartmentID,
		PositionID:     unit.DefaultPositionID,
		RoleIDs:        []string{"org.manager"},
		Persona:        operations.WorkforcePersonaDTO{ResponsibilityBoundary: "负责组织权限关系同步。"},
		Enabled:        true,
	})
	if err != nil {
		t.Fatalf("UpsertWorkforceMember: %v", err)
	}

	assertAuthorizationCall(t, authz.calls, "write_org_created", "user", "admin", organization.OrganizationID, "", "")
	assertAuthorizationCall(t, authz.calls, "write_dept_created", "organization", organization.OrganizationID, organization.OrganizationID, unit.DepartmentID, "")
	assertAuthorizationCall(t, authz.calls, "write_workforce", "agent", "profile-agent", organization.OrganizationID, unit.DepartmentID, "org.manager")
}

func TestCommandServiceDeletesAuthorizationRelationsWhenReleasingWorkforceMember(t *testing.T) {
	ctx := context.Background()
	store := &fakeWorkforceProfileStore{profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{}}
	orgs := newFakeOrganizationCatalogStore()
	seedOrganizationCatalog(t, ctx, orgs, "orrery", []operations.DepartmentDTO{
		{DepartmentID: "operations", OrganizationID: "orrery", Name: "Operations"},
	}, []operations.PositionDTO{
		{PositionID: "operator", DepartmentID: "operations", Name: "Operator"},
	})
	authz := &fakeAuthorizationRelations{}
	command := operations.NewCommandService(store)
	command.SetOrganizationCatalogStore(orgs)
	command.SetAuthorizationRelations(authz)
	viewer := operations.ViewerRef{ViewerID: "admin", ViewerKind: "user", PermissionScope: []string{operations.PermissionWorkforceManage, operations.PermissionRolesManage}}

	_, err := command.UpsertWorkforceMember(ctx, viewer, operations.UpsertWorkforceMemberCommand{
		AgentProfileID: "profile-agent",
		DisplayName:    "Ops",
		AgentKind:      "ai",
		OrganizationID: "orrery",
		DepartmentID:   "operations",
		PositionID:     "operator",
		RoleIDs:        []string{"org.manager"},
		Persona:        operations.WorkforcePersonaDTO{ResponsibilityBoundary: "负责组织权限关系同步。"},
		Enabled:        true,
	})
	if err != nil {
		t.Fatalf("UpsertWorkforceMember: %v", err)
	}
	if err := command.ReleaseWorkforceMember(ctx, viewer, operations.ReleaseWorkforceMemberCommand{AgentProfileID: "profile-agent"}); err != nil {
		t.Fatalf("ReleaseWorkforceMember: %v", err)
	}

	assertAuthorizationCall(t, authz.calls, "delete_workforce", "agent", "profile-agent", "orrery", "operations", "org.manager")
}

func TestCommandServiceWritesAuthorizationRelationsForRoleAssignments(t *testing.T) {
	ctx := context.Background()
	store := &fakeWorkforceProfileStore{profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{
		"profile-owner": {
			ID:        "profile-owner",
			AgentKind: agentcore.AgentKindHuman,
			Enabled:   true,
			OrgIdentity: agentcore.AgentOrgIdentity{
				OrganizationRef: agentcore.OrganizationRef{ID: "orrery"},
			},
		},
	}}
	orgs := newFakeOrganizationCatalogStore()
	seedOrganizationCatalog(t, ctx, orgs, "orrery", []operations.DepartmentDTO{
		{DepartmentID: "operations", OrganizationID: "orrery", Name: "Operations"},
	}, nil)
	roles := newFakeRoleStore()
	authz := &fakeAuthorizationRelations{}
	command := operations.NewCommandService(store)
	command.SetOrganizationCatalogStore(orgs)
	command.SetRoleStore(roles)
	command.SetAuthorizationRelations(authz)
	viewer := operations.ViewerRef{ViewerID: "admin", ViewerKind: "user", PermissionScope: []string{operations.PermissionRolesManage}}

	_, err := command.PutRoleAssignments(ctx, viewer, operations.PutRoleAssignmentsCommand{Assignments: []operations.RoleAssignmentDTO{{
		Actor:              operations.ActorRefDTO{ActorType: "human", ActorID: "profile-owner"},
		RoleID:             "org.owner",
		OrganizationNodeID: "operations",
		OrganizationCode:   "0101",
		ScopeMode:          operations.RoleScopeSelfAndDescendants,
	}}})
	if err != nil {
		t.Fatalf("PutRoleAssignments: %v", err)
	}

	assertAuthorizationCall(t, authz.calls, "write_role", "user", "profile-owner", "orrery", "operations", "org.owner")
}

type fakeAuthorizationCall struct {
	op           string
	subjectKind  string
	subjectID    string
	organization string
	department   string
	roleID       string
}

type fakeAuthorizationRelations struct {
	calls []fakeAuthorizationCall
}

func (f *fakeAuthorizationRelations) WriteOrganizationCreated(_ context.Context, admin authorization.SubjectRef, organizationID string, _ string) error {
	f.record("write_org_created", admin, organizationID, "", "")
	return nil
}

func (f *fakeAuthorizationRelations) WriteOrganizationAdmin(_ context.Context, admin authorization.SubjectRef, organizationID string, _ string) error {
	f.record("write_org_admin", admin, organizationID, "", "")
	return nil
}

func (f *fakeAuthorizationRelations) DeleteOrganizationAdmin(_ context.Context, admin authorization.SubjectRef, organizationID string, _ string) error {
	f.record("delete_org_admin", admin, organizationID, "", "")
	return nil
}

func (f *fakeAuthorizationRelations) WriteDepartmentCreated(_ context.Context, departmentID string, organizationID string, _ string) error {
	f.record("write_dept_created", authorization.SubjectRef{Kind: "organization", ID: organizationID}, organizationID, departmentID, "")
	return nil
}

func (f *fakeAuthorizationRelations) DeleteDepartmentCreated(_ context.Context, departmentID string, organizationID string, _ string) error {
	f.record("delete_dept_created", authorization.SubjectRef{Kind: "organization", ID: organizationID}, organizationID, departmentID, "")
	return nil
}

func (f *fakeAuthorizationRelations) WriteDepartmentManager(_ context.Context, manager authorization.SubjectRef, departmentID string, _ string) error {
	f.record("write_dept_manager", manager, "", departmentID, "")
	return nil
}

func (f *fakeAuthorizationRelations) DeleteDepartmentManager(_ context.Context, manager authorization.SubjectRef, departmentID string, _ string) error {
	f.record("delete_dept_manager", manager, "", departmentID, "")
	return nil
}

func (f *fakeAuthorizationRelations) WriteWorkforceMemberPlacement(_ context.Context, member authorization.SubjectRef, organizationID string, departmentID string, _ string, roleIDs []string) error {
	f.record("write_workforce", member, organizationID, departmentID, firstRoleID(roleIDs))
	return nil
}

func (f *fakeAuthorizationRelations) DeleteWorkforceMemberPlacement(_ context.Context, member authorization.SubjectRef, organizationID string, departmentID string, _ string, roleIDs []string) error {
	f.record("delete_workforce", member, organizationID, departmentID, firstRoleID(roleIDs))
	return nil
}

func (f *fakeAuthorizationRelations) WriteRoleAssignment(_ context.Context, assignment authorization.RoleAssignmentRelationSnapshot) error {
	f.record("write_role", assignment.Assignee, assignment.OrganizationID, assignment.OrganizationNodeID, assignment.RoleID)
	return nil
}

func (f *fakeAuthorizationRelations) DeleteRoleAssignment(_ context.Context, assignment authorization.RoleAssignmentRelationSnapshot) error {
	f.record("delete_role", assignment.Assignee, assignment.OrganizationID, assignment.OrganizationNodeID, assignment.RoleID)
	return nil
}

func (f *fakeAuthorizationRelations) record(op string, subject authorization.SubjectRef, organizationID string, departmentID string, roleID string) {
	f.calls = append(f.calls, fakeAuthorizationCall{
		op:           op,
		subjectKind:  subject.Kind,
		subjectID:    subject.ID,
		organization: organizationID,
		department:   departmentID,
		roleID:       roleID,
	})
}

func firstRoleID(roleIDs []string) string {
	if len(roleIDs) == 0 {
		return ""
	}
	return roleIDs[0]
}

func assertAuthorizationCall(t *testing.T, calls []fakeAuthorizationCall, op string, subjectKind string, subjectID string, organizationID string, departmentID string, roleID string) {
	t.Helper()
	for _, call := range calls {
		if call.op == op && call.subjectKind == subjectKind && call.subjectID == subjectID && call.organization == organizationID && call.department == departmentID && call.roleID == roleID {
			return
		}
	}
	t.Fatalf("authorization call %s %s:%s org=%s dept=%s role=%s missing from %+v", op, subjectKind, subjectID, organizationID, departmentID, roleID, calls)
}

type fakeRoleStore struct {
	assignments map[string]operations.RoleAssignmentDTO
}

func newFakeRoleStore() *fakeRoleStore {
	return &fakeRoleStore{assignments: map[string]operations.RoleAssignmentDTO{}}
}

func (f *fakeRoleStore) ListRoleDefinitions(context.Context) ([]operations.RoleDefinitionDTO, error) {
	return nil, nil
}

func (f *fakeRoleStore) SaveRoleDefinition(_ context.Context, role operations.RoleDefinitionDTO) error {
	_ = role
	return nil
}

func (f *fakeRoleStore) ListRoleAssignments(_ context.Context, query operations.RoleAssignmentQuery) ([]operations.RoleAssignmentDTO, error) {
	out := make([]operations.RoleAssignmentDTO, 0, len(f.assignments))
	for _, assignment := range f.assignments {
		if query.RoleID != "" && assignment.RoleID != query.RoleID {
			continue
		}
		if query.OrganizationCode != "" && assignment.OrganizationCode != query.OrganizationCode {
			continue
		}
		out = append(out, assignment)
	}
	return out, nil
}

func (f *fakeRoleStore) SaveRoleAssignment(_ context.Context, assignment operations.RoleAssignmentDTO) error {
	f.assignments[assignment.AssignmentID] = assignment
	return nil
}

func (f *fakeRoleStore) DeleteRoleAssignment(_ context.Context, assignmentID string) error {
	delete(f.assignments, assignmentID)
	return nil
}
