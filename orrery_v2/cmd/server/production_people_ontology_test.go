package main

import (
	"context"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/contextgraph"
	"orrery_v2/internal/app/ontology"
	"orrery_v2/internal/app/operations"
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/graphprotocol"
	"orrery_v2/internal/platform/migration"
	toolres "orrery_v2/internal/resource/tool"
)

func TestProductionCreateWorkforceMemberWritesPersonOntologyContextGraph(t *testing.T) {
	ctx, core, api, viewer := newProductionPeopleOntologyTestAPI(t, "production-people-person-ontology", []string{
		"org-people-1",
		"dept-people-1",
		"position-people-1",
		"profile-people-1",
	})

	organization, err := api.CreateOrganization(ctx, viewer, operations.CreateOrganizationCommand{Name: "Orrery"})
	if err != nil {
		t.Fatalf("CreateOrganization: %v", err)
	}
	department, err := api.CreateOrganizationUnit(ctx, viewer, operations.CreateOrganizationUnitCommand{
		OrganizationID:      organization.OrganizationID,
		Name:                "Operations",
		DefaultPositionName: "Architect",
	})
	if err != nil {
		t.Fatalf("CreateOrganizationUnit: %v", err)
	}
	member, err := api.UpsertWorkforceMember(ctx, viewer, operations.UpsertWorkforceMemberCommand{
		AgentProfileID: "profile-people-1",
		DisplayName:    "Ada Lovelace",
		AgentKind:      "human",
		EmploymentType: "employee",
		OrganizationID: organization.OrganizationID,
		DepartmentID:   department.DepartmentID,
		PositionID:     department.DefaultPositionID,
		RoleIDs:        []string{"architect"},
		Persona: operations.WorkforcePersonaDTO{
			Backstory:              "Coordinates analytical architecture decisions.",
			ResponsibilityBoundary: "Owns workforce architecture reviews.",
		},
		UserAccount: operations.WorkforceUserAccountDTO{
			Username:       "ada",
			Email:          "ada@example.test",
			Role:           "architect",
			AccountEnabled: true,
			Password:       "correct-horse-1",
		},
		RuntimeKind: "human",
		Enabled:     true,
	})
	if err != nil {
		t.Fatalf("UpsertWorkforceMember: %v", err)
	}

	personID := "person:" + member.AgentProfileID
	agentID := "agent:" + member.AgentID
	assignmentID := member.AgentProfileID + ":" + department.DefaultPositionID + ":architect"
	objects, links, facts, evidence, err := contextgraph.NewPlatformStore(core).Snapshot(ctx, organization.OrganizationID)
	if err != nil {
		t.Fatalf("context graph snapshot: %v", err)
	}
	assertContextGraphObject(t, objects, "person", personID)
	assertContextGraphObject(t, objects, "agent", agentID)
	assertContextGraphObject(t, objects, "role_assignment", assignmentID)
	assertContextGraphLink(t, links, "person_holds_position", personID, department.DefaultPositionID)
	assertContextGraphLink(t, links, "agent_fulfills_position", agentID, department.DefaultPositionID)
	assertContextGraphFact(t, facts, "person", personID, "display_name", "Ada Lovelace")
	assertContextGraphFact(t, facts, "person", personID, "role", "architect")
	assertContextGraphFact(t, facts, "person", personID, "email", "ada@example.test")
	assertContextGraphFact(t, facts, "person", personID, "status", "active")
	assertContextGraphFact(t, facts, "agent", agentID, "display_name", "Ada Lovelace")
	assertContextGraphFact(t, facts, "agent", agentID, "runtime_kind", "human")
	assertContextGraphEvidence(t, evidence, "person-updated:"+personID)
	assertContextGraphEvidence(t, evidence, "agent-updated:"+agentID)
	assertContextGraphEvidence(t, evidence, "position-assigned:"+assignmentID)

	events, err := api.ontologyEvents.List(ctx)
	if err != nil {
		t.Fatalf("list ontology events: %v", err)
	}
	assertOntologyEvent(t, events, "person-updated:"+personID, "people.person.updated")
	assertOntologyEvent(t, events, "agent-updated:"+agentID, "people.agent.updated")
	assertOntologyEvent(t, events, "position-assigned:"+assignmentID, "people.position.assigned")
}

func TestProductionWorkforceRenameOverridesOlderAgentObservation(t *testing.T) {
	ctx, core, api, viewer := newProductionPeopleOntologyTestAPI(t, "production-people-agent-rename", []string{
		"org-people-rename-1",
		"dept-people-rename-1",
		"position-people-rename-1",
		"gangbeng",
	})

	organization, err := api.CreateOrganization(ctx, viewer, operations.CreateOrganizationCommand{Name: "Minvia"})
	if err != nil {
		t.Fatalf("CreateOrganization: %v", err)
	}
	department, err := api.CreateOrganizationUnit(ctx, viewer, operations.CreateOrganizationUnitCommand{
		OrganizationID:      organization.OrganizationID,
		Name:                "R&D Center",
		DefaultPositionName: "Engineer",
	})
	if err != nil {
		t.Fatalf("CreateOrganizationUnit: %v", err)
	}

	runner := api.ontologyProject
	api.ontologyProject = nil
	member, err := api.UpsertWorkforceMember(ctx, viewer, operations.UpsertWorkforceMemberCommand{
		AgentProfileID: "gangbeng",
		RealName:       "钢镚",
		DisplayName:    "钢镚",
		AgentKind:      "ai",
		EmploymentType: "ai_agent",
		OrganizationID: organization.OrganizationID,
		DepartmentID:   department.DepartmentID,
		PositionID:     department.DefaultPositionID,
		RoleIDs:        []string{"engineer"},
		RuntimeKind:    "pi",
		Persona: operations.WorkforcePersonaDTO{
			ResponsibilityBoundary: "负责研发中心的软件交付。",
		},
		Enabled: true,
	})
	if err != nil {
		t.Fatalf("seed workforce member: %v", err)
	}
	api.ontologyProject = runner

	agentID := workforceAgentOntologyID(member.AgentID)
	observedAt := time.Now().UTC().Add(-time.Hour)
	if err := api.applyOntologyEvent(ctx, graphprotocol.DomainEvent{
		ID:         "runtime-agent-observed:" + agentID,
		TenantID:   organization.OrganizationID,
		Type:       "people.agent.updated",
		StreamID:   agentID,
		Version:    observedAt.UnixNano(),
		OccurredAt: observedAt,
		Payload: map[string]any{
			"id": agentID, "display_name": "gangbeng", "runtime_kind": "pi",
		},
		PublishedBy:     graphprotocol.ActorRef{Kind: "system", ID: "runtime"},
		SourceAccessRef: organizationOntologySourceRef(organization.OrganizationID),
	}); err != nil {
		t.Fatalf("seed runtime observation: %v", err)
	}

	if _, err := api.UpsertWorkforceMember(ctx, viewer, operations.UpsertWorkforceMemberCommand{
		AgentProfileID: "gangbeng",
		RealName:       "贴饼",
		DisplayName:    "贴饼",
		UpdateMask:     []string{"real_name", "display_name"},
	}); err != nil {
		t.Fatalf("rename workforce member: %v", err)
	}

	_, _, facts, _, err := contextgraph.NewPlatformStore(core).Snapshot(ctx, organization.OrganizationID)
	if err != nil {
		t.Fatalf("context graph snapshot: %v", err)
	}
	assertContextGraphFact(t, facts, "agent", agentID, "display_name", "贴饼")
}

func TestProductionUpsertWorkforceMemberWritesMembershipOntologyContextGraph(t *testing.T) {
	ctx, core, api, viewer := newProductionPeopleOntologyTestAPI(t, "production-people-membership-ontology", []string{
		"org-people-membership-1",
		"dept-people-membership-1",
		"position-people-membership-1",
		"profile-people-membership-1",
	})

	organization, err := api.CreateOrganization(ctx, viewer, operations.CreateOrganizationCommand{Name: "Orrery"})
	if err != nil {
		t.Fatalf("CreateOrganization: %v", err)
	}
	department, err := api.CreateOrganizationUnit(ctx, viewer, operations.CreateOrganizationUnitCommand{
		OrganizationID:      organization.OrganizationID,
		Name:                "Engineering",
		DefaultPositionName: "Engineer",
	})
	if err != nil {
		t.Fatalf("CreateOrganizationUnit: %v", err)
	}
	member, err := api.UpsertWorkforceMember(ctx, viewer, operations.UpsertWorkforceMemberCommand{
		AgentProfileID: "profile-people-membership-1",
		DisplayName:    "Grace Hopper",
		AgentKind:      "human",
		EmploymentType: "employee",
		OrganizationID: organization.OrganizationID,
		DepartmentID:   department.DepartmentID,
		PositionID:     department.DefaultPositionID,
		RoleIDs:        []string{"engineer"},
		Persona: operations.WorkforcePersonaDTO{
			Backstory:              "Maintains engineering delivery loops.",
			ResponsibilityBoundary: "Owns engineering membership routing.",
		},
		UserAccount: operations.WorkforceUserAccountDTO{
			Username:       "grace",
			Email:          "grace@example.test",
			Role:           "engineer",
			AccountEnabled: true,
			Password:       "correct-horse-1",
		},
		RuntimeKind: "human",
		Enabled:     true,
	})
	if err != nil {
		t.Fatalf("UpsertWorkforceMember: %v", err)
	}

	personID := "person:" + member.AgentProfileID
	agentID := "agent:" + member.AgentID
	assignmentID := member.AgentProfileID + ":" + department.DefaultPositionID + ":engineer"
	_, links, _, evidence, err := contextgraph.NewPlatformStore(core).Snapshot(ctx, organization.OrganizationID)
	if err != nil {
		t.Fatalf("context graph snapshot: %v", err)
	}
	assertContextGraphLink(t, links, "person_holds_position", personID, department.DefaultPositionID)
	assertContextGraphLink(t, links, "agent_fulfills_position", agentID, department.DefaultPositionID)
	assertContextGraphEvidence(t, evidence, "position-assigned:"+assignmentID)
	events, err := api.ontologyEvents.List(ctx)
	if err != nil {
		t.Fatalf("list ontology events: %v", err)
	}
	assertOntologyEvent(t, events, "department-created:"+department.DepartmentID, "people.department.created")
	assertOntologyEvent(t, events, "position-created:"+department.DefaultPositionID, "people.position.created")
	assertOntologyEvent(t, events, "position-assigned:"+assignmentID, "people.position.assigned")
}

func TestProductionCreateOrganizationUnitWritesTeamOntologyContextGraph(t *testing.T) {
	ctx, core, api, viewer := newProductionPeopleOntologyTestAPI(t, "production-people-team-ontology", []string{
		"org-people-team-1",
		"dept-people-team-1",
		"position-people-team-1",
	})

	organization, err := api.CreateOrganization(ctx, viewer, operations.CreateOrganizationCommand{Name: "Orrery"})
	if err != nil {
		t.Fatalf("CreateOrganization: %v", err)
	}
	department, err := api.CreateOrganizationUnit(ctx, viewer, operations.CreateOrganizationUnitCommand{
		OrganizationID:      organization.OrganizationID,
		Name:                "Platform Team",
		DefaultPositionName: "Platform Engineer",
	})
	if err != nil {
		t.Fatalf("CreateOrganizationUnit: %v", err)
	}

	objects, links, facts, evidence, err := contextgraph.NewPlatformStore(core).Snapshot(ctx, organization.OrganizationID)
	if err != nil {
		t.Fatalf("context graph snapshot: %v", err)
	}
	assertContextGraphObject(t, objects, "department", department.DepartmentID)
	assertContextGraphObject(t, objects, "position", department.DefaultPositionID)
	assertContextGraphLink(t, links, "department_part_of_organization", department.DepartmentID, organization.OrganizationID)
	assertContextGraphLink(t, links, "position_belongs_to_department", department.DefaultPositionID, department.DepartmentID)
	assertContextGraphFact(t, facts, "department", department.DepartmentID, "name", "Platform Team")
	assertContextGraphFact(t, facts, "department", department.DepartmentID, "status", "active")
	assertContextGraphFact(t, facts, "position", department.DefaultPositionID, "name", "Platform Engineer")
	assertContextGraphFact(t, facts, "position", department.DefaultPositionID, "status", "active")
	assertContextGraphEvidence(t, evidence, "department-created:"+department.DepartmentID)
	assertContextGraphEvidence(t, evidence, "position-created:"+department.DefaultPositionID)
	events, err := api.ontologyEvents.List(ctx)
	if err != nil {
		t.Fatalf("list ontology events: %v", err)
	}
	assertOntologyEvent(t, events, "department-created:"+department.DepartmentID, "people.department.created")
	assertOntologyEvent(t, events, "position-created:"+department.DefaultPositionID, "people.position.created")
}

func newProductionPeopleOntologyTestAPI(t *testing.T, name string, ids []string) (context.Context, *db.Store, productionAPI, operations.ViewerRef) {
	t.Helper()
	ctx := context.Background()
	databases, err := db.OpenRegistry(ctx, db.RegistryConfig{Databases: map[string]db.Config{
		db.RoleCore: {Driver: db.DriverMemory, Name: name},
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
	profiles := &organizationOntologyProfileStore{profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{}}
	orgCatalog := operations.NewOrganizationCatalogStore(db.NewRouter(databases))
	command := operations.NewCommandService(profiles)
	command.SetOrganizationCatalogStore(orgCatalog)
	command.SetIDGenerator(&peopleOntologySequenceOperationsIDs{ids: ids})
	query := operations.NewQueryService(profiles, toolres.NewMemoryRegistry())
	query.SetOrganizationCatalogStore(orgCatalog)
	api := productionAPI{
		operations:      operations.NewService(query, command),
		ontologyProject: ontologyRunner,
		ontologyEvents:  newProductionOntologyEventLog(core),
	}
	viewer := operations.ViewerRef{
		ViewerID:        "ops-admin",
		ViewerKind:      "user",
		PermissionScope: []string{operations.PermissionOrgManage, operations.PermissionWorkforceManage, operations.PermissionRolesManage, operations.PermissionPermissionsManage},
	}
	return ctx, core, api, viewer
}

type peopleOntologySequenceOperationsIDs struct {
	ids []string
	pos int
}

func (g *peopleOntologySequenceOperationsIDs) NewID(prefix string) (string, error) {
	if g == nil || g.pos >= len(g.ids) {
		return prefix + "-people-1", nil
	}
	id := g.ids[g.pos]
	g.pos++
	return id, nil
}
