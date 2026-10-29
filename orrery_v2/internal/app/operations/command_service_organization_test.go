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

func TestCommandServiceGeneratesOpaqueIDsForCreateOperations(t *testing.T) {
	ctx := context.Background()
	store := &fakeWorkforceProfileStore{profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{}}
	service := operations.NewCommandService(store)
	orgs := newFakeOrganizationCatalogStore()
	service.SetOrganizationCatalogStore(orgs)
	viewer := operations.ViewerRef{ViewerID: "admin", PermissionScope: []string{operations.PermissionOrgManage, operations.PermissionWorkforceManage}}

	organization, err := service.CreateOrganization(ctx, viewer, operations.CreateOrganizationCommand{
		Name: "Platform",
	})
	if err != nil {
		t.Fatalf("CreateOrganization: %v", err)
	}
	assertOpaqueGeneratedID(t, "organization id", "org", organization.OrganizationID)
	if organization.DefaultDepartmentID != "" || organization.DefaultPositionID != "" {
		t.Fatalf("organization should not auto-create default department or position: %+v", organization)
	}

	unit, err := service.CreateOrganizationUnit(ctx, viewer, operations.CreateOrganizationUnitCommand{
		OrganizationID:      organization.OrganizationID,
		Name:                "Research",
		DefaultPositionName: "Researcher",
	})
	if err != nil {
		t.Fatalf("CreateOrganizationUnit: %v", err)
	}
	assertOpaqueGeneratedID(t, "department id", "unit", unit.DepartmentID)
	assertOpaqueGeneratedID(t, "unit default position id", "position", unit.DefaultPositionID)

	member, err := service.CreateWorkforceMember(ctx, viewer, operations.UpsertWorkforceMemberCommand{
		RealName:       "Ops Agent",
		AgentKind:      "ai",
		EmploymentType: "ai_agent",
		OrganizationID: organization.OrganizationID,
		DepartmentID:   unit.DepartmentID,
		PositionID:     unit.DefaultPositionID,
		RuntimeKind:    "codex",
		Enabled:        true,
		Persona:        operations.WorkforcePersonaDTO{Backstory: "Handles platform operations."},
	})
	if err != nil {
		t.Fatalf("CreateWorkforceMember: %v", err)
	}
	assertOpaqueGeneratedID(t, "profile id", "profile", member.AgentProfileID)
	if member.AgentID != member.AgentProfileID {
		t.Fatalf("agent id should be the stable profile id: %+v", member)
	}
	if member.AgentInstanceID != member.AgentProfileID {
		t.Fatalf("agent instance id should be the stable profile id: %+v", member)
	}
	if member.DepartmentID != unit.DepartmentID || member.PositionID != unit.DefaultPositionID || member.PositionDisplayName != "Researcher" {
		t.Fatalf("member should attach to generated child unit position: member=%+v unit=%+v", member, unit)
	}
}

func TestCommandServiceCreateOrganizationDoesNotRequireDefaultDepartmentOrPosition(t *testing.T) {
	ctx := context.Background()
	store := &fakeWorkforceProfileStore{profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{}}
	orgs := newFakeOrganizationCatalogStore()
	command := operations.NewCommandService(store)
	command.SetOrganizationCatalogStore(orgs)
	viewer := operations.ViewerRef{ViewerID: "admin", PermissionScope: []string{operations.PermissionOrgManage}}

	organization, err := command.CreateOrganization(ctx, viewer, operations.CreateOrganizationCommand{Name: "平台中心"})
	if err != nil {
		t.Fatalf("CreateOrganization: %v", err)
	}
	if organization.Name != "平台中心" || organization.DefaultDepartmentID != "" || organization.DefaultPositionID != "" {
		t.Fatalf("organization should only persist the root organization node: %+v", organization)
	}
	if len(orgs.departments) != 0 || len(orgs.positions) != 0 {
		t.Fatalf("creating an organization must not create default children: departments=%+v positions=%+v", orgs.departments, orgs.positions)
	}
}

func TestCommandServiceCreatesLifecycleKnowledgeForOrganizationAndPositions(t *testing.T) {
	ctx := context.Background()
	store := &fakeWorkforceProfileStore{profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{}}
	orgs := newFakeOrganizationCatalogStore()
	knowledge := &fakeKnowledgeLifecycle{}
	command := operations.NewCommandService(store)
	command.SetOrganizationCatalogStore(orgs)
	command.SetKnowledgeLifecycle(knowledge)
	viewer := operations.ViewerRef{ViewerID: "admin", PermissionScope: []string{operations.PermissionOrgManage}}

	organization, err := command.CreateOrganization(ctx, viewer, operations.CreateOrganizationCommand{Name: "平台中心"})
	if err != nil {
		t.Fatalf("CreateOrganization: %v", err)
	}
	if organization.KnowledgeBaseID == "" || orgs.organizations[organization.OrganizationID].KnowledgeBaseID != organization.KnowledgeBaseID {
		t.Fatalf("organization knowledge base was not bound: dto=%+v saved=%+v", organization, orgs.organizations[organization.OrganizationID])
	}
	if len(knowledge.created) != 1 ||
		knowledge.created[0].cmd.OwnerType != "organization" ||
		knowledge.created[0].cmd.OwnerID != organization.OrganizationID ||
		knowledge.created[0].cmd.Visibility != "scoped" ||
		knowledge.created[0].viewer.OrganizationIDs[0] != organization.OrganizationID {
		t.Fatalf("organization knowledge create = %+v", knowledge.created)
	}

	unit, err := command.CreateOrganizationUnit(ctx, viewer, operations.CreateOrganizationUnitCommand{
		OrganizationID:      organization.OrganizationID,
		Name:                "研发部",
		DefaultPositionName: "研发工程师",
	})
	if err != nil {
		t.Fatalf("CreateOrganizationUnit: %v", err)
	}
	defaultPosition := orgs.positions[unit.DefaultPositionID]
	if defaultPosition.KnowledgeBaseID == "" {
		t.Fatalf("default position knowledge base was not bound: %+v", defaultPosition)
	}

	position, err := command.CreateOrganizationPosition(ctx, viewer, operations.CreateOrganizationPositionCommand{
		OrganizationID: organization.OrganizationID,
		DepartmentID:   unit.DepartmentID,
		Name:           "产品经理",
	})
	if err != nil {
		t.Fatalf("CreateOrganizationPosition: %v", err)
	}
	if position.KnowledgeBaseID == "" || orgs.positions[position.PositionID].KnowledgeBaseID != position.KnowledgeBaseID {
		t.Fatalf("position knowledge base was not bound: dto=%+v saved=%+v", position, orgs.positions[position.PositionID])
	}
	if len(knowledge.created) != 3 {
		t.Fatalf("knowledge created = %+v", knowledge.created)
	}
	for _, created := range knowledge.created[1:] {
		if created.cmd.OwnerType != "position" || created.cmd.Visibility != "scoped" || len(created.viewer.PositionIDs) != 1 || created.viewer.PositionIDs[0] != created.cmd.OwnerID {
			t.Fatalf("position knowledge create = %+v", created)
		}
	}
}

func TestCommandServiceCreateOrganizationCleansKnowledgeWhenCatalogSaveFails(t *testing.T) {
	ctx := context.Background()
	saveErr := errors.New("save organization failed")
	store := &fakeWorkforceProfileStore{profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{}}
	orgs := newFakeOrganizationCatalogStore()
	orgs.saveOrgError = saveErr
	knowledge := &fakeKnowledgeLifecycle{}
	command := operations.NewCommandService(store)
	command.SetOrganizationCatalogStore(orgs)
	command.SetKnowledgeLifecycle(knowledge)
	viewer := operations.ViewerRef{ViewerID: "admin", PermissionScope: []string{operations.PermissionOrgManage}}

	_, err := command.CreateOrganization(ctx, viewer, operations.CreateOrganizationCommand{Name: "平台中心"})
	if !errors.Is(err, saveErr) {
		t.Fatalf("CreateOrganization err = %v, want save error", err)
	}
	if len(knowledge.created) != 1 || len(knowledge.deleted) != 1 || knowledge.deleted[0].id != knowledge.created[0].id {
		t.Fatalf("created knowledge should be cleaned after save failure: created=%+v deleted=%+v", knowledge.created, knowledge.deleted)
	}
}

func TestCommandServicePersistsOrganizationCatalogForEmptyOrgGraph(t *testing.T) {
	ctx := context.Background()
	store := &fakeWorkforceProfileStore{profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{}}
	orgs := newFakeOrganizationCatalogStore()
	command := operations.NewCommandService(store)
	command.SetOrganizationCatalogStore(orgs)
	query := operations.NewQueryService(&fakeEmptyAgentDirectory{}, &fakeEmptyToolRegistry{})
	query.SetOrganizationCatalogStore(orgs)
	service := operations.NewService(query, command)
	viewer := operations.ViewerRef{ViewerID: "admin", PermissionScope: []string{operations.PermissionOrgManage, operations.PermissionOrgRead}}

	organization, err := service.CreateOrganization(ctx, viewer, operations.CreateOrganizationCommand{
		Name: "Platform",
	})
	if err != nil {
		t.Fatalf("CreateOrganization: %v", err)
	}
	unit, err := service.CreateOrganizationUnit(ctx, viewer, operations.CreateOrganizationUnitCommand{
		OrganizationID: organization.OrganizationID,
		Name:           "Research",
	})
	if err != nil {
		t.Fatalf("CreateOrganizationUnit: %v", err)
	}

	graph, err := service.GetOrgGraph(ctx, viewer, operations.OrgGraphQuery{OrganizationID: organization.OrganizationID})
	if err != nil {
		t.Fatalf("GetOrgGraph: %v", err)
	}
	if len(graph.Members) != 0 {
		t.Fatalf("graph should not need members to show catalog: %+v", graph.Members)
	}
	if len(graph.Organizations) != 1 || graph.Organizations[0].Name != "Platform" {
		t.Fatalf("organizations = %+v", graph.Organizations)
	}
	if len(graph.Departments) != 1 {
		t.Fatalf("departments = %+v", graph.Departments)
	}
	if graph.Departments[0].DepartmentID != unit.DepartmentID {
		t.Fatalf("created child unit missing from graph: %+v", graph.Departments)
	}
	if len(graph.Positions) != 0 {
		t.Fatalf("positions = %+v", graph.Positions)
	}
	if organization.Code != "01" || organization.DisplayCode != "01" || organization.Status != "active" || organization.NodeType != "company" {
		t.Fatalf("organization code metadata = %+v", organization)
	}
	createdUnit := findDepartmentByID(graph.Departments, unit.DepartmentID)
	if createdUnit.Code != "0101" || createdUnit.DisplayCode != "01.01" || createdUnit.Status != "active" || createdUnit.NodeType != "department" {
		t.Fatalf("created unit code metadata = %+v", createdUnit)
	}
	if len(graph.OrganizationNodes) != 1 || graph.OrganizationNodes[0].TotalMemberCount != 0 || graph.Summary.OrganizationCount != 2 {
		t.Fatalf("organization nodes not populated for empty graph: nodes=%+v summary=%+v", graph.OrganizationNodes, graph.Summary)
	}
}

func findDepartmentByID(departments []operations.DepartmentDTO, id string) operations.DepartmentDTO {
	for _, department := range departments {
		if department.DepartmentID == id {
			return department
		}
	}
	return operations.DepartmentDTO{}
}

func TestCommandServiceCreateOrganizationPositionPersistsUnderDepartment(t *testing.T) {
	ctx := context.Background()
	store := &fakeWorkforceProfileStore{profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{}}
	audit := &fakeOperationsAuditRecorder{}
	orgs := newFakeOrganizationCatalogStore()
	seedOrganizationCatalog(t, ctx, orgs, "org-1", []operations.DepartmentDTO{
		{DepartmentID: "dept-default", OrganizationID: "org-1", Name: "Default"},
		{DepartmentID: "dept-ai", OrganizationID: "org-1", Name: "AI"},
	}, []operations.PositionDTO{
		{PositionID: "pos-default", DepartmentID: "dept-default", Name: "Member"},
	})
	command := operations.NewCommandService(store)
	command.SetAuditRecorder(audit)
	command.SetOrganizationCatalogStore(orgs)
	viewer := operations.ViewerRef{ViewerID: "admin", PermissionScope: []string{operations.PermissionOrgManage}, RequestID: "corr-create-position"}

	position, err := command.CreateOrganizationPosition(ctx, viewer, operations.CreateOrganizationPositionCommand{
		OrganizationID: "org-1",
		DepartmentID:   "dept-ai",
		Name:           "算法工程师",
	})
	if err != nil {
		t.Fatalf("CreateOrganizationPosition: %v", err)
	}

	assertOpaqueGeneratedID(t, "position id", "position", position.PositionID)
	if position.DepartmentID != "dept-ai" || position.Name != "算法工程师" {
		t.Fatalf("position = %+v", position)
	}
	saved := orgs.positions[position.PositionID]
	if saved.PositionID == "" || saved.DepartmentID != "dept-ai" || saved.Name != "算法工程师" {
		t.Fatalf("saved position = %+v", saved)
	}
	if len(audit.events) == 0 || audit.events[len(audit.events)-1].Action != "organization.position.create" {
		t.Fatalf("create position audit missing: %+v", audit.events)
	}
}

func TestCommandServiceCreateOrganizationPositionUsesDefaultDepartmentForOrganization(t *testing.T) {
	ctx := context.Background()
	store := &fakeWorkforceProfileStore{profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{}}
	orgs := newFakeOrganizationCatalogStore()
	seedOrganizationCatalog(t, ctx, orgs, "org-1", []operations.DepartmentDTO{
		{DepartmentID: "dept-default", OrganizationID: "org-1", Name: "Default"},
	}, []operations.PositionDTO{
		{PositionID: "pos-default", DepartmentID: "dept-default", Name: "Member"},
	})
	command := operations.NewCommandService(store)
	command.SetOrganizationCatalogStore(orgs)
	viewer := operations.ViewerRef{ViewerID: "admin", PermissionScope: []string{operations.PermissionOrgManage}}

	position, err := command.CreateOrganizationPosition(ctx, viewer, operations.CreateOrganizationPositionCommand{
		OrganizationID: "org-1",
		Name:           "产品经理",
	})
	if err != nil {
		t.Fatalf("CreateOrganizationPosition: %v", err)
	}
	if position.DepartmentID != "dept-default" {
		t.Fatalf("position should be attached to default department: %+v", position)
	}
}

func TestCommandServiceCreateOrganizationPositionRequiresDepartmentWhenOrganizationHasNoDefaultDepartment(t *testing.T) {
	ctx := context.Background()
	store := &fakeWorkforceProfileStore{profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{}}
	orgs := newFakeOrganizationCatalogStore()
	seedOrganizationCatalog(t, ctx, orgs, "org-1", nil, nil)
	command := operations.NewCommandService(store)
	command.SetOrganizationCatalogStore(orgs)
	viewer := operations.ViewerRef{ViewerID: "admin", PermissionScope: []string{operations.PermissionOrgManage}}

	_, err := command.CreateOrganizationPosition(ctx, viewer, operations.CreateOrganizationPositionCommand{
		OrganizationID: "org-1",
		Name:           "产品经理",
	})
	if !errors.Is(err, operations.ErrInvalidRequest) {
		t.Fatalf("CreateOrganizationPosition err = %v, want ErrInvalidRequest", err)
	}
}

func TestCommandServiceCreateOrganizationUnitDoesNotCreateDefaultPositionWhenNameOmitted(t *testing.T) {
	ctx := context.Background()
	store := &fakeWorkforceProfileStore{profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{}}
	orgs := newFakeOrganizationCatalogStore()
	seedOrganizationCatalog(t, ctx, orgs, "org-1", []operations.DepartmentDTO{
		{DepartmentID: "dept-default", OrganizationID: "org-1", Name: "Default"},
	}, []operations.PositionDTO{
		{PositionID: "pos-default", DepartmentID: "dept-default", Name: "Member"},
	})
	command := operations.NewCommandService(store)
	command.SetOrganizationCatalogStore(orgs)
	viewer := operations.ViewerRef{ViewerID: "admin", PermissionScope: []string{operations.PermissionOrgManage}}

	unit, err := command.CreateOrganizationUnit(ctx, viewer, operations.CreateOrganizationUnitCommand{
		OrganizationID:     "org-1",
		Name:               "研究部",
		ParentDepartmentID: "dept-default",
	})
	if err != nil {
		t.Fatalf("CreateOrganizationUnit: %v", err)
	}
	if unit.DefaultPositionID != "" {
		t.Fatalf("unit should not create a default position without a position name: %+v", unit)
	}
	if len(orgs.positions) != 1 {
		t.Fatalf("unit creation should not add positions: %+v", orgs.positions)
	}
}

func TestCommandServiceDeleteOrganizationUnitRemovesSquadAndKeepsMembers(t *testing.T) {
	ctx := context.Background()
	store := &fakeWorkforceProfileStore{profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{}}
	audit := &fakeOperationsAuditRecorder{}
	orgs := newFakeOrganizationCatalogStore()
	seedOrganizationCatalog(t, ctx, orgs, "org-1", []operations.DepartmentDTO{
		{DepartmentID: "dept-default", OrganizationID: "org-1", Name: "Default"},
		{DepartmentID: "dept-squad", OrganizationID: "org-1", Name: "Squad"},
	}, []operations.PositionDTO{
		{PositionID: "pos-default", DepartmentID: "dept-default", Name: "Member"},
		{PositionID: "pos-squad", DepartmentID: "dept-squad", Name: "Specialist"},
	})
	command := operations.NewCommandService(store)
	command.SetAuditRecorder(audit)
	command.SetOrganizationCatalogStore(orgs)
	roles := newFakeRoleStore()
	command.SetRoleStore(roles)
	viewer := operations.ViewerRef{ViewerID: "admin", PermissionScope: []string{operations.PermissionOrgManage, operations.PermissionWorkforceManage, operations.PermissionRolesManage}, RequestID: "corr-delete-unit"}
	created, err := command.CreateWorkforceMember(ctx, viewer, operations.UpsertWorkforceMemberCommand{
		RealName:       "Squad Agent",
		AgentKind:      "ai",
		EmploymentType: "ai_agent",
		OrganizationID: "org-1",
		DepartmentID:   "dept-squad",
		PositionID:     "pos-squad",
		RoleIDs:        []string{"member"},
		RuntimeKind:    "codex",
		Enabled:        true,
		Persona:        operations.WorkforcePersonaDTO{Backstory: "Works in a squad."},
	})
	if err != nil {
		t.Fatalf("CreateWorkforceMember: %v", err)
	}

	if err := command.DeleteOrganizationUnit(ctx, viewer, "dept-squad"); err != nil {
		t.Fatalf("DeleteOrganizationUnit: %v", err)
	}

	if _, ok := orgs.departments["dept-squad"]; ok {
		t.Fatalf("department should be deleted: %+v", orgs.departments)
	}
	if _, ok := orgs.positions["pos-squad"]; ok {
		t.Fatalf("department positions should be deleted: %+v", orgs.positions)
	}
	if store.deleted[agentcore.AgentProfileID(created.AgentProfileID)] {
		t.Fatalf("delete organization unit must not delete members: %+v", store.deleted)
	}
	profile := store.profiles[agentcore.AgentProfileID(created.AgentProfileID)]
	if profile.OrgIdentity.DepartmentRef.ID != "dept-default" || profile.OrgIdentity.PositionRef.ID != "pos-default" {
		t.Fatalf("member should be moved to default org unit, got: %+v", profile.OrgIdentity)
	}
	assignment := roles.assignments["workforce:"+created.AgentProfileID+":member"]
	if assignment.OrganizationNodeID != "dept-default" || assignment.ScopeMode != operations.RoleScopeSelf {
		t.Fatalf("workforce role assignment should move to default org unit: %+v", assignment)
	}
	if len(audit.events) == 0 || audit.events[len(audit.events)-1].Action != "organization.unit.delete" {
		t.Fatalf("delete unit audit missing: %+v", audit.events)
	}
}

func TestCommandServiceDeleteOrganizationUnitRequiresTargetDepartmentAuthorization(t *testing.T) {
	ctx := context.Background()
	store := &fakeWorkforceProfileStore{profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{}}
	orgs := newFakeOrganizationCatalogStore()
	seedOrganizationCatalog(t, ctx, orgs, "org-1", []operations.DepartmentDTO{
		{DepartmentID: "dept-default", OrganizationID: "org-1", Name: "Default"},
		{DepartmentID: "dept-squad", OrganizationID: "org-1", Name: "Squad"},
	}, []operations.PositionDTO{
		{PositionID: "pos-default", DepartmentID: "dept-default", Name: "Member"},
		{PositionID: "pos-squad", DepartmentID: "dept-squad", Name: "Specialist"},
	})
	roles := newFakeRoleStore()
	command := operations.NewCommandService(store)
	command.SetOrganizationCatalogStore(orgs)
	command.SetRoleStore(roles)
	viewer := operations.ViewerRef{ViewerID: "admin", PermissionScope: []string{operations.PermissionOrgManage, operations.PermissionWorkforceManage, operations.PermissionRolesManage}}
	created, err := command.CreateWorkforceMember(ctx, viewer, operations.UpsertWorkforceMemberCommand{
		RealName:       "Scoped Squad Agent",
		AgentKind:      "ai",
		EmploymentType: "ai_agent",
		OrganizationID: "org-1",
		DepartmentID:   "dept-squad",
		PositionID:     "pos-squad",
		RoleIDs:        []string{"member"},
		RuntimeKind:    "codex",
		Enabled:        true,
		Persona:        operations.WorkforcePersonaDTO{Backstory: "Must not cross an unauthorized department boundary."},
	})
	if err != nil {
		t.Fatalf("CreateWorkforceMember: %v", err)
	}
	assignmentID := "workforce:" + created.AgentProfileID + ":member"
	previousProfile := store.profiles[agentcore.AgentProfileID(created.AgentProfileID)]
	previousAssignment := roles.assignments[assignmentID]
	command.SetAuthorizationChecker(targetDepartmentDenyChecker{})

	err = command.DeleteOrganizationUnit(ctx, viewer, "dept-squad")

	if !errors.Is(err, authorizationapp.ErrDenied) {
		t.Fatalf("DeleteOrganizationUnit error = %v, want target department denial", err)
	}
	if profile := store.profiles[agentcore.AgentProfileID(created.AgentProfileID)]; profile.OrgIdentity.DepartmentRef.ID != previousProfile.OrgIdentity.DepartmentRef.ID || profile.OrgIdentity.PositionRef.ID != previousProfile.OrgIdentity.PositionRef.ID {
		t.Fatalf("denied department move changed profile: before=%+v after=%+v", previousProfile.OrgIdentity, profile.OrgIdentity)
	}
	if assignment := roles.assignments[assignmentID]; assignment != previousAssignment {
		t.Fatalf("denied department move changed role assignment: before=%+v after=%+v", previousAssignment, assignment)
	}
	if _, exists := orgs.departments["dept-squad"]; !exists {
		t.Fatal("denied department move deleted the source department")
	}
}

func TestCommandServiceDeleteOrganizationUnitRollsBackProfileWhenRoleSyncFails(t *testing.T) {
	ctx := context.Background()
	store := &fakeWorkforceProfileStore{profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{}}
	orgs := newFakeOrganizationCatalogStore()
	seedOrganizationCatalog(t, ctx, orgs, "org-1", []operations.DepartmentDTO{
		{DepartmentID: "dept-default", OrganizationID: "org-1", Name: "Default"},
		{DepartmentID: "dept-squad", OrganizationID: "org-1", Name: "Squad"},
	}, []operations.PositionDTO{
		{PositionID: "pos-default", DepartmentID: "dept-default", Name: "Member"},
		{PositionID: "pos-squad", DepartmentID: "dept-squad", Name: "Specialist"},
	})
	roles := newFakeRoleStore()
	command := operations.NewCommandService(store)
	command.SetOrganizationCatalogStore(orgs)
	command.SetRoleStore(roles)
	viewer := operations.ViewerRef{ViewerID: "admin", PermissionScope: []string{operations.PermissionOrgManage, operations.PermissionWorkforceManage, operations.PermissionRolesManage}}
	created, err := command.CreateWorkforceMember(ctx, viewer, operations.UpsertWorkforceMemberCommand{
		RealName:       "Role Sync Agent",
		AgentKind:      "ai",
		EmploymentType: "ai_agent",
		OrganizationID: "org-1",
		DepartmentID:   "dept-squad",
		PositionID:     "pos-squad",
		RoleIDs:        []string{"member"},
		RuntimeKind:    "codex",
		Enabled:        true,
		Persona:        operations.WorkforcePersonaDTO{Backstory: "Keeps personnel and role placement consistent."},
	})
	if err != nil {
		t.Fatalf("CreateWorkforceMember: %v", err)
	}
	assignmentID := "workforce:" + created.AgentProfileID + ":member"
	previousProfile := store.profiles[agentcore.AgentProfileID(created.AgentProfileID)]
	previousAssignment := roles.assignments[assignmentID]
	syncErr := errors.New("role assignment store unavailable")
	command.SetRoleStore(&failNextRoleAssignmentStore{fakeRoleStore: roles, err: syncErr})

	err = command.DeleteOrganizationUnit(ctx, viewer, "dept-squad")

	if !errors.Is(err, syncErr) {
		t.Fatalf("DeleteOrganizationUnit error = %v, want role sync error", err)
	}
	profile := store.profiles[agentcore.AgentProfileID(created.AgentProfileID)]
	if profile.OrgIdentity.DepartmentRef.ID != previousProfile.OrgIdentity.DepartmentRef.ID || profile.OrgIdentity.PositionRef.ID != previousProfile.OrgIdentity.PositionRef.ID {
		t.Fatalf("failed role sync left profile moved: before=%+v after=%+v", previousProfile.OrgIdentity, profile.OrgIdentity)
	}
	if assignment := roles.assignments[assignmentID]; assignment != previousAssignment {
		t.Fatalf("failed role sync left assignment moved: before=%+v after=%+v", previousAssignment, assignment)
	}
	if _, exists := orgs.departments["dept-squad"]; !exists {
		t.Fatal("failed role sync deleted the source department")
	}
}

type targetDepartmentDenyChecker struct{}

func (targetDepartmentDenyChecker) Can(_ context.Context, _ identity.Principal, action string, object identity.ObjectRef) error {
	if action == "department.manage" && object.Type == "department" && object.ID == "dept-default" {
		return authorizationapp.ErrDenied
	}
	return nil
}

type failNextRoleAssignmentStore struct {
	*fakeRoleStore
	err    error
	failed bool
}

func (f *failNextRoleAssignmentStore) SaveRoleAssignment(ctx context.Context, assignment operations.RoleAssignmentDTO) error {
	if !f.failed {
		f.failed = true
		return f.err
	}
	return f.fakeRoleStore.SaveRoleAssignment(ctx, assignment)
}

func TestCommandServiceDeleteOrganizationUnitRejectsInternalMembersWhenNoDefaultTarget(t *testing.T) {
	ctx := context.Background()
	store := &fakeWorkforceProfileStore{profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{}}
	orgs := newFakeOrganizationCatalogStore()
	seedOrganizationCatalog(t, ctx, orgs, "org-1", []operations.DepartmentDTO{
		{DepartmentID: "dept-squad", OrganizationID: "org-1", Name: "Squad"},
	}, []operations.PositionDTO{
		{PositionID: "pos-squad", DepartmentID: "dept-squad", Name: "Specialist"},
	})
	command := operations.NewCommandService(store)
	command.SetOrganizationCatalogStore(orgs)
	viewer := operations.ViewerRef{ViewerID: "admin", PermissionScope: []string{operations.PermissionOrgManage, operations.PermissionWorkforceManage}}
	if _, err := command.CreateWorkforceMember(ctx, viewer, operations.UpsertWorkforceMemberCommand{
		RealName:       "Squad Agent",
		AgentKind:      "ai",
		EmploymentType: "ai_agent",
		OrganizationID: "org-1",
		DepartmentID:   "dept-squad",
		PositionID:     "pos-squad",
		RuntimeKind:    "codex",
		Enabled:        true,
		Persona:        operations.WorkforcePersonaDTO{Backstory: "Works in a squad."},
	}); err != nil {
		t.Fatalf("CreateWorkforceMember: %v", err)
	}

	err := command.DeleteOrganizationUnit(ctx, viewer, "dept-squad")

	if !errors.Is(err, operations.ErrInvalidRequest) {
		t.Fatalf("DeleteOrganizationUnit err = %v, want ErrInvalidRequest", err)
	}
	if _, ok := orgs.departments["dept-squad"]; !ok {
		t.Fatalf("department with internal members must not be deleted without default target")
	}
}

func TestCommandServiceDeleteOrganizationPositionRemovesEmptyPosition(t *testing.T) {
	ctx := context.Background()
	store := &fakeWorkforceProfileStore{profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{}}
	audit := &fakeOperationsAuditRecorder{}
	orgs := newFakeOrganizationCatalogStore()
	seedOrganizationCatalog(t, ctx, orgs, "org-1", []operations.DepartmentDTO{
		{DepartmentID: "dept-ai", OrganizationID: "org-1", Name: "AI"},
	}, []operations.PositionDTO{
		{PositionID: "pos-empty", DepartmentID: "dept-ai", Name: "算法工程师"},
	})
	command := operations.NewCommandService(store)
	command.SetAuditRecorder(audit)
	command.SetOrganizationCatalogStore(orgs)
	viewer := operations.ViewerRef{ViewerID: "admin", PermissionScope: []string{operations.PermissionOrgManage}, RequestID: "corr-delete-position"}

	if err := command.DeleteOrganizationPosition(ctx, viewer, "pos-empty"); err != nil {
		t.Fatalf("DeleteOrganizationPosition: %v", err)
	}
	if _, ok := orgs.positions["pos-empty"]; ok {
		t.Fatalf("position should be deleted: %+v", orgs.positions)
	}
	if len(audit.events) == 0 || audit.events[len(audit.events)-1].Action != "organization.position.delete" {
		t.Fatalf("delete position audit missing: %+v", audit.events)
	}
}

func TestCommandServiceDeleteOrganizationPositionRemovesBoundKnowledgeBase(t *testing.T) {
	ctx := context.Background()
	store := &fakeWorkforceProfileStore{profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{}}
	orgs := newFakeOrganizationCatalogStore()
	knowledge := &fakeKnowledgeLifecycle{}
	seedOrganizationCatalog(t, ctx, orgs, "org-1", []operations.DepartmentDTO{
		{DepartmentID: "dept-ai", OrganizationID: "org-1", Name: "AI"},
	}, []operations.PositionDTO{
		{PositionID: "pos-empty", DepartmentID: "dept-ai", Name: "算法工程师", KnowledgeBaseID: "kb-position"},
	})
	command := operations.NewCommandService(store)
	command.SetOrganizationCatalogStore(orgs)
	command.SetKnowledgeLifecycle(knowledge)
	viewer := operations.ViewerRef{ViewerID: "admin", PermissionScope: []string{operations.PermissionOrgManage}}

	if err := command.DeleteOrganizationPosition(ctx, viewer, "pos-empty"); err != nil {
		t.Fatalf("DeleteOrganizationPosition: %v", err)
	}
	if _, ok := orgs.positions["pos-empty"]; ok {
		t.Fatalf("position should be deleted")
	}
	if len(knowledge.deleted) != 1 || knowledge.deleted[0].id != "kb-position" || knowledge.deleted[0].viewer.PositionIDs[0] != "pos-empty" {
		t.Fatalf("position knowledge delete = %+v", knowledge.deleted)
	}
}

func TestCommandServiceDeleteOrganizationPositionRejectsInternalMembers(t *testing.T) {
	ctx := context.Background()
	store := &fakeWorkforceProfileStore{profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{}}
	orgs := newFakeOrganizationCatalogStore()
	seedOrganizationCatalog(t, ctx, orgs, "org-1", []operations.DepartmentDTO{
		{DepartmentID: "dept-ai", OrganizationID: "org-1", Name: "AI"},
	}, []operations.PositionDTO{
		{PositionID: "pos-ai", DepartmentID: "dept-ai", Name: "算法工程师"},
	})
	command := operations.NewCommandService(store)
	command.SetOrganizationCatalogStore(orgs)
	viewer := operations.ViewerRef{ViewerID: "admin", PermissionScope: []string{operations.PermissionOrgManage, operations.PermissionWorkforceManage}}
	if _, err := command.CreateWorkforceMember(ctx, viewer, operations.UpsertWorkforceMemberCommand{
		RealName:       "AI Agent",
		AgentKind:      "ai",
		EmploymentType: "ai_agent",
		OrganizationID: "org-1",
		DepartmentID:   "dept-ai",
		PositionID:     "pos-ai",
		RuntimeKind:    "codex",
		Enabled:        true,
		Persona:        operations.WorkforcePersonaDTO{Backstory: "Works in AI."},
	}); err != nil {
		t.Fatalf("CreateWorkforceMember: %v", err)
	}

	err := command.DeleteOrganizationPosition(ctx, viewer, "pos-ai")

	if !errors.Is(err, operations.ErrInvalidRequest) {
		t.Fatalf("DeleteOrganizationPosition err = %v, want ErrInvalidRequest", err)
	}
	if _, ok := orgs.positions["pos-ai"]; !ok {
		t.Fatalf("position with internal members must not be deleted")
	}
}

func TestCommandServiceDeleteOrganizationRemovesEmptyCatalogTree(t *testing.T) {
	ctx := context.Background()
	store := &fakeWorkforceProfileStore{profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{}}
	audit := &fakeOperationsAuditRecorder{}
	orgs := newFakeOrganizationCatalogStore()
	seedOrganizationCatalog(t, ctx, orgs, "org-empty", []operations.DepartmentDTO{
		{DepartmentID: "dept-default", OrganizationID: "org-empty", Name: "Default"},
		{DepartmentID: "dept-child", OrganizationID: "org-empty", Name: "Child", ParentDepartmentID: "dept-default"},
	}, []operations.PositionDTO{
		{PositionID: "pos-default", DepartmentID: "dept-default", Name: "Member"},
		{PositionID: "pos-child", DepartmentID: "dept-child", Name: "Specialist"},
	})
	command := operations.NewCommandService(store)
	command.SetAuditRecorder(audit)
	command.SetOrganizationCatalogStore(orgs)
	sourceDeletion := &operationsSourceDeletionLifecycleSpy{}
	command.SetSourceDeletionLifecycle(sourceDeletion)
	viewer := operations.ViewerRef{ViewerID: "admin", PermissionScope: []string{operations.PermissionOrgManage}, RequestID: "corr-delete-org"}

	if err := command.DeleteOrganization(ctx, viewer, "org-empty"); err != nil {
		t.Fatalf("DeleteOrganization: %v", err)
	}

	if _, ok := orgs.organizations["org-empty"]; ok {
		t.Fatalf("organization should be deleted: %+v", orgs.organizations)
	}
	if len(orgs.departments) != 0 || len(orgs.positions) != 0 {
		t.Fatalf("organization catalog children should be deleted: departments=%+v positions=%+v", orgs.departments, orgs.positions)
	}
	if len(audit.events) == 0 || audit.events[len(audit.events)-1].Action != "organization.delete" {
		t.Fatalf("delete organization audit missing: %+v", audit.events)
	}
	if sourceDeletion.calls != 1 || sourceDeletion.sourceType != "organization" || sourceDeletion.sourceID != "org-empty" || sourceDeletion.rollbacks != 1 {
		t.Fatalf("organization source deletion lifecycle = %+v", sourceDeletion)
	}
}

func TestCommandServiceDeleteOrganizationRemovesBoundKnowledgeBases(t *testing.T) {
	ctx := context.Background()
	store := &fakeWorkforceProfileStore{profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{}}
	orgs := newFakeOrganizationCatalogStore()
	knowledge := &fakeKnowledgeLifecycle{}
	seedOrganizationCatalog(t, ctx, orgs, "org-empty", []operations.DepartmentDTO{
		{DepartmentID: "dept-default", OrganizationID: "org-empty", Name: "Default"},
	}, []operations.PositionDTO{
		{PositionID: "pos-default", DepartmentID: "dept-default", Name: "Member", KnowledgeBaseID: "kb-position"},
	})
	organization := orgs.organizations["org-empty"]
	organization.KnowledgeBaseID = "kb-org"
	if err := orgs.SaveOrganization(ctx, organization); err != nil {
		t.Fatalf("save organization knowledge binding: %v", err)
	}
	command := operations.NewCommandService(store)
	command.SetOrganizationCatalogStore(orgs)
	command.SetKnowledgeLifecycle(knowledge)
	viewer := operations.ViewerRef{ViewerID: "admin", PermissionScope: []string{operations.PermissionOrgManage}}

	if err := command.DeleteOrganization(ctx, viewer, "org-empty"); err != nil {
		t.Fatalf("DeleteOrganization: %v", err)
	}
	if _, ok := orgs.organizations["org-empty"]; ok {
		t.Fatalf("organization should be deleted")
	}
	gotDeleted := []string{}
	for _, deleted := range knowledge.deleted {
		gotDeleted = append(gotDeleted, deleted.id)
	}
	wantDeleted := []string{"kb-position", "kb-org"}
	if len(gotDeleted) != len(wantDeleted) {
		t.Fatalf("deleted knowledge = %+v, want %+v", gotDeleted, wantDeleted)
	}
	for i := range wantDeleted {
		if gotDeleted[i] != wantDeleted[i] {
			t.Fatalf("deleted knowledge = %+v, want %+v", gotDeleted, wantDeleted)
		}
	}
}

func TestCommandServiceDeleteOrganizationRejectsInternalMembers(t *testing.T) {
	ctx := context.Background()
	store := &fakeWorkforceProfileStore{profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{}}
	orgs := newFakeOrganizationCatalogStore()
	seedOrganizationCatalog(t, ctx, orgs, "org-1", []operations.DepartmentDTO{
		{DepartmentID: "dept-default", OrganizationID: "org-1", Name: "Default"},
	}, []operations.PositionDTO{
		{PositionID: "pos-default", DepartmentID: "dept-default", Name: "Member"},
	})
	command := operations.NewCommandService(store)
	command.SetOrganizationCatalogStore(orgs)
	sourceDeletion := &operationsSourceDeletionLifecycleSpy{}
	command.SetSourceDeletionLifecycle(sourceDeletion)
	viewer := operations.ViewerRef{ViewerID: "admin", PermissionScope: []string{operations.PermissionOrgManage, operations.PermissionWorkforceManage}}
	if _, err := command.CreateWorkforceMember(ctx, viewer, operations.UpsertWorkforceMemberCommand{
		RealName:       "Org Member",
		AgentKind:      "ai",
		EmploymentType: "ai_agent",
		OrganizationID: "org-1",
		DepartmentID:   "dept-default",
		PositionID:     "pos-default",
		RuntimeKind:    "codex",
		Enabled:        true,
		Persona:        operations.WorkforcePersonaDTO{Backstory: "Works in the org."},
	}); err != nil {
		t.Fatalf("CreateWorkforceMember: %v", err)
	}

	err := command.DeleteOrganization(ctx, viewer, "org-1")

	if !errors.Is(err, operations.ErrInvalidRequest) {
		t.Fatalf("DeleteOrganization err = %v, want ErrInvalidRequest", err)
	}
	if _, ok := orgs.organizations["org-1"]; !ok {
		t.Fatalf("organization with internal members must not be deleted")
	}
	if sourceDeletion.calls != 0 {
		t.Fatalf("source deletion prepared before organization invariant checks: %+v", sourceDeletion)
	}
}

type operationsSourceDeletionLifecycleSpy struct {
	calls      int
	sourceType string
	sourceID   string
	rollbacks  int
}

func (s *operationsSourceDeletionLifecycleSpy) PrepareSourceDeletion(_ context.Context, sourceType, sourceID string, _ []string, _ string) (func(context.Context) error, error) {
	s.calls++
	s.sourceType = sourceType
	s.sourceID = sourceID
	return func(context.Context) error {
		s.rollbacks++
		return nil
	}, nil
}
