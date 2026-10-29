package main

import (
	"context"
	"testing"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/contextgraph"
	"orrery_v2/internal/app/ontology"
	"orrery_v2/internal/app/operations"
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/migration"
	toolres "orrery_v2/internal/resource/tool"
)

func TestProductionOrganizationUnitAndPositionWritePeopleOntologyV2ContextGraph(t *testing.T) {
	ctx := context.Background()
	databases, err := db.OpenRegistry(ctx, db.RegistryConfig{Databases: map[string]db.Config{
		db.RoleCore: {Driver: db.DriverMemory, Name: "production-people-ontology-v2"},
	}})
	if err != nil {
		t.Fatalf("open database registry: %v", err)
	}
	t.Cleanup(func() {
		if err := databases.Close(); err != nil {
			t.Fatalf("close database registry: %v", err)
		}
	})
	core, ok := databases.Get(db.RoleCore)
	if !ok || core == nil {
		t.Fatalf("core database missing")
	}
	coordinator := migration.NewCoordinator(databases)
	if err := ontology.RegisterMigrations(coordinator); err != nil {
		t.Fatalf("register ontology migrations: %v", err)
	}
	if err := contextgraph.RegisterMigrations(coordinator); err != nil {
		t.Fatalf("register context graph migrations: %v", err)
	}
	if err := coordinator.UpRole(ctx, db.RoleCore); err != nil {
		t.Fatalf("migrate core db: %v", err)
	}
	if err := ontology.InstallDefaultOntologyDefinitions(ctx, ontology.NewPlatformRegistry(core)); err != nil {
		t.Fatalf("install default ontology: %v", err)
	}
	ontologyRunner, err := newProductionOntologyProjectionRunner(ctx, core)
	if err != nil {
		t.Fatalf("new production ontology runner: %v", err)
	}
	orgCatalog := operations.NewOrganizationCatalogStore(db.NewRouter(databases))
	profiles := &organizationOntologyProfileStore{profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{}}
	command := operations.NewCommandService(profiles)
	command.SetOrganizationCatalogStore(orgCatalog)
	command.SetIDGenerator(&peopleOntologySequenceIDs{ids: []string{"org-1", "unit-1", "position-1"}})
	query := operations.NewQueryService(profiles, toolres.NewMemoryRegistry())
	query.SetOrganizationCatalogStore(orgCatalog)
	api := productionAPI{
		operations:      operations.NewService(query, command),
		ontologyProject: ontologyRunner,
	}
	viewer := operations.ViewerRef{
		ViewerID:        "admin-1",
		ViewerKind:      "user",
		PermissionScope: []string{operations.PermissionOrgManage},
	}

	organization, err := api.CreateOrganization(ctx, viewer, operations.CreateOrganizationCommand{Name: "Platform"})
	if err != nil {
		t.Fatalf("CreateOrganization: %v", err)
	}
	department, err := api.CreateOrganizationUnit(ctx, viewer, operations.CreateOrganizationUnitCommand{
		OrganizationID: organization.OrganizationID,
		Name:           "Platform Architecture",
	})
	if err != nil {
		t.Fatalf("CreateOrganizationUnit: %v", err)
	}
	position, err := api.CreateOrganizationPosition(ctx, viewer, operations.CreateOrganizationPositionCommand{
		OrganizationID: organization.OrganizationID,
		DepartmentID:   department.DepartmentID,
		Name:           "Ontology Architect",
	})
	if err != nil {
		t.Fatalf("CreateOrganizationPosition: %v", err)
	}

	objects, links, facts, evidence, err := contextgraph.NewPlatformStore(core).Snapshot(ctx, organization.OrganizationID)
	if err != nil {
		t.Fatalf("context graph snapshot: %v", err)
	}
	assertContextGraphObject(t, objects, "organization", organization.OrganizationID)
	assertContextGraphObject(t, objects, "department", department.DepartmentID)
	assertContextGraphObject(t, objects, "position", position.PositionID)
	assertContextGraphLink(t, links, "department_part_of_organization", department.DepartmentID, organization.OrganizationID)
	assertContextGraphLink(t, links, "position_belongs_to_department", position.PositionID, department.DepartmentID)
	assertContextGraphFact(t, facts, "department", department.DepartmentID, "name", "Platform Architecture")
	assertContextGraphFact(t, facts, "department", department.DepartmentID, "status", "active")
	assertContextGraphFact(t, facts, "position", position.PositionID, "name", "Ontology Architect")
	assertContextGraphFact(t, facts, "position", position.PositionID, "status", "active")
	assertContextGraphEvidence(t, evidence, "department-created:"+department.DepartmentID)
	assertContextGraphEvidence(t, evidence, "position-created:"+position.PositionID)
}

func TestProductionCreateWorkforceMemberWritesPeoplePositionAssignmentContextGraph(t *testing.T) {
	ctx := context.Background()
	databases, err := db.OpenRegistry(ctx, db.RegistryConfig{Databases: map[string]db.Config{
		db.RoleCore: {Driver: db.DriverMemory, Name: "production-people-assignment-ontology-v2"},
	}})
	if err != nil {
		t.Fatalf("open database registry: %v", err)
	}
	t.Cleanup(func() {
		if err := databases.Close(); err != nil {
			t.Fatalf("close database registry: %v", err)
		}
	})
	core, ok := databases.Get(db.RoleCore)
	if !ok || core == nil {
		t.Fatalf("core database missing")
	}
	coordinator := migration.NewCoordinator(databases)
	if err := ontology.RegisterMigrations(coordinator); err != nil {
		t.Fatalf("register ontology migrations: %v", err)
	}
	if err := contextgraph.RegisterMigrations(coordinator); err != nil {
		t.Fatalf("register context graph migrations: %v", err)
	}
	if err := coordinator.UpRole(ctx, db.RoleCore); err != nil {
		t.Fatalf("migrate core db: %v", err)
	}
	if err := ontology.InstallDefaultOntologyDefinitions(ctx, ontology.NewPlatformRegistry(core)); err != nil {
		t.Fatalf("install default ontology: %v", err)
	}
	ontologyRunner, err := newProductionOntologyProjectionRunner(ctx, core)
	if err != nil {
		t.Fatalf("new production ontology runner: %v", err)
	}
	orgCatalog := operations.NewOrganizationCatalogStore(db.NewRouter(databases))
	profiles := &organizationOntologyProfileStore{profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{}}
	command := operations.NewCommandService(profiles)
	command.SetOrganizationCatalogStore(orgCatalog)
	command.SetIDGenerator(&peopleOntologySequenceIDs{ids: []string{"org-1", "unit-1", "position-1", "profile-1"}})
	query := operations.NewQueryService(profiles, toolres.NewMemoryRegistry())
	query.SetOrganizationCatalogStore(orgCatalog)
	api := productionAPI{
		operations:      operations.NewService(query, command),
		ontologyProject: ontologyRunner,
	}
	viewer := operations.ViewerRef{
		ViewerID:        "admin-1",
		ViewerKind:      "user",
		PermissionScope: []string{operations.PermissionOrgManage, operations.PermissionWorkforceManage, operations.PermissionRolesManage},
	}

	organization, err := api.CreateOrganization(ctx, viewer, operations.CreateOrganizationCommand{Name: "Platform"})
	if err != nil {
		t.Fatalf("CreateOrganization: %v", err)
	}
	department, err := api.CreateOrganizationUnit(ctx, viewer, operations.CreateOrganizationUnitCommand{
		OrganizationID: organization.OrganizationID,
		Name:           "Platform Architecture",
	})
	if err != nil {
		t.Fatalf("CreateOrganizationUnit: %v", err)
	}
	position, err := api.CreateOrganizationPosition(ctx, viewer, operations.CreateOrganizationPositionCommand{
		OrganizationID: organization.OrganizationID,
		DepartmentID:   department.DepartmentID,
		Name:           "Ontology Architect",
	})
	if err != nil {
		t.Fatalf("CreateOrganizationPosition: %v", err)
	}
	member, err := api.CreateWorkforceMember(ctx, viewer, operations.UpsertWorkforceMemberCommand{
		RealName:       "Ontology Agent",
		AgentKind:      "ai",
		EmploymentType: "ai_agent",
		OrganizationID: organization.OrganizationID,
		DepartmentID:   department.DepartmentID,
		PositionID:     position.PositionID,
		RoleIDs:        []string{"architecture.owner"},
		RuntimeKind:    "codex",
		Enabled:        true,
		Persona: operations.WorkforcePersonaDTO{
			Backstory:              "负责本体论能力建模。",
			ResponsibilityBoundary: "负责本体论结构与岗位关系维护。",
		},
	})
	if err != nil {
		t.Fatalf("CreateWorkforceMember: %v", err)
	}

	objects, links, facts, evidence, err := contextgraph.NewPlatformStore(core).Snapshot(ctx, organization.OrganizationID)
	if err != nil {
		t.Fatalf("context graph snapshot: %v", err)
	}
	personID := "person:" + member.AgentProfileID
	agentID := "agent:" + member.AgentID
	assignmentID := member.AgentProfileID + ":" + position.PositionID + ":architecture.owner"
	assertContextGraphObject(t, objects, "person", personID)
	assertContextGraphObject(t, objects, "agent", agentID)
	assertContextGraphObject(t, objects, "role_assignment", assignmentID)
	assertContextGraphLink(t, links, "person_holds_position", personID, position.PositionID)
	assertContextGraphLink(t, links, "agent_fulfills_position", agentID, position.PositionID)
	assertContextGraphFact(t, facts, "person", personID, "display_name", "Ontology Agent")
	assertContextGraphFact(t, facts, "agent", agentID, "display_name", "Ontology Agent")
	assertContextGraphFact(t, facts, "agent", agentID, "runtime_kind", "codex")
	assertContextGraphFact(t, facts, "role_assignment", assignmentID, "role_id", "architecture.owner")
	assertContextGraphFact(t, facts, "role_assignment", assignmentID, "scope", department.DepartmentID)
	assertContextGraphFact(t, facts, "role_assignment", assignmentID, "status", "active")
	assertContextGraphEvidence(t, evidence, "person-updated:"+personID)
	assertContextGraphEvidence(t, evidence, "agent-updated:"+agentID)
	assertContextGraphEvidence(t, evidence, "position-assigned:"+assignmentID)
}

func TestProductionCreateWorkforceMemberDoesNotRequirePreprojectedPositionContextGraph(t *testing.T) {
	ctx := context.Background()
	databases, err := db.OpenRegistry(ctx, db.RegistryConfig{Databases: map[string]db.Config{
		db.RoleCore: {Driver: db.DriverMemory, Name: "production-people-assignment-existing-catalog"},
	}})
	if err != nil {
		t.Fatalf("open database registry: %v", err)
	}
	t.Cleanup(func() {
		if err := databases.Close(); err != nil {
			t.Fatalf("close database registry: %v", err)
		}
	})
	core, ok := databases.Get(db.RoleCore)
	if !ok || core == nil {
		t.Fatalf("core database missing")
	}
	coordinator := migration.NewCoordinator(databases)
	if err := ontology.RegisterMigrations(coordinator); err != nil {
		t.Fatalf("register ontology migrations: %v", err)
	}
	if err := contextgraph.RegisterMigrations(coordinator); err != nil {
		t.Fatalf("register context graph migrations: %v", err)
	}
	if err := coordinator.UpRole(ctx, db.RoleCore); err != nil {
		t.Fatalf("migrate core db: %v", err)
	}
	if err := ontology.InstallDefaultOntologyDefinitions(ctx, ontology.NewPlatformRegistry(core)); err != nil {
		t.Fatalf("install default ontology: %v", err)
	}
	ontologyRunner, err := newProductionOntologyProjectionRunner(ctx, core)
	if err != nil {
		t.Fatalf("new production ontology runner: %v", err)
	}
	orgCatalog := operations.NewOrganizationCatalogStore(db.NewRouter(databases))
	if err := orgCatalog.SaveOrganization(ctx, operations.OrganizationDTO{OrganizationID: "org-existing", Name: "Existing Org", DefaultDepartmentID: "dept-existing", DefaultPositionID: "pos-existing"}); err != nil {
		t.Fatalf("seed organization: %v", err)
	}
	if err := orgCatalog.SaveDepartment(ctx, operations.DepartmentDTO{DepartmentID: "dept-existing", OrganizationID: "org-existing", Name: "Existing Department"}); err != nil {
		t.Fatalf("seed department: %v", err)
	}
	if err := orgCatalog.SavePosition(ctx, operations.PositionDTO{PositionID: "pos-existing", DepartmentID: "dept-existing", Name: "Existing Position"}); err != nil {
		t.Fatalf("seed position: %v", err)
	}
	profiles := &organizationOntologyProfileStore{profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{}}
	command := operations.NewCommandService(profiles)
	command.SetOrganizationCatalogStore(orgCatalog)
	command.SetIDGenerator(&peopleOntologySequenceIDs{ids: []string{"profile-existing"}})
	query := operations.NewQueryService(profiles, toolres.NewMemoryRegistry())
	query.SetOrganizationCatalogStore(orgCatalog)
	api := productionAPI{
		operations:       operations.NewService(query, command),
		ontologyProject:  ontologyRunner,
		contextGraphCore: core,
	}
	viewer := operations.ViewerRef{
		ViewerID:        "admin-1",
		ViewerKind:      "user",
		PermissionScope: []string{operations.PermissionWorkforceManage, operations.PermissionWorkforceRead},
	}

	member, err := api.CreateWorkforceMember(ctx, viewer, operations.UpsertWorkforceMemberCommand{
		RealName:       "Existing Catalog Agent",
		AgentKind:      "ai",
		EmploymentType: "ai_agent",
		OrganizationID: "org-existing",
		DepartmentID:   "dept-existing",
		PositionID:     "pos-existing",
		RuntimeKind:    "codex",
		Enabled:        true,
		Persona: operations.WorkforcePersonaDTO{
			Backstory:              "负责现有组织目录同步。",
			ResponsibilityBoundary: "负责保持人员目录与任职记录可见。",
		},
	})
	if err != nil {
		t.Fatalf("CreateWorkforceMember should not require preprojected position context graph: %v", err)
	}
	page, err := api.ListWorkforceMembers(ctx, viewer, operations.WorkforceQuery{OrganizationID: "org-existing"})
	if err != nil {
		t.Fatalf("ListWorkforceMembers: %v", err)
	}
	if len(page.Members) != 1 || page.Members[0].AgentProfileID != member.AgentProfileID {
		t.Fatalf("workforce directory did not retain created member: %+v", page.Members)
	}
}

type peopleOntologySequenceIDs struct {
	ids []string
	pos int
}

func (g *peopleOntologySequenceIDs) NewID(prefix string) (string, error) {
	if g == nil || g.pos >= len(g.ids) {
		return prefix + "-1", nil
	}
	id := g.ids[g.pos]
	g.pos++
	return id, nil
}
