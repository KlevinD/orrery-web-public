package main

import (
	"context"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/contextgraph"
	"orrery_v2/internal/app/memoryadmin"
	"orrery_v2/internal/app/ontology"
	"orrery_v2/internal/app/operations"
	projectadmin "orrery_v2/internal/app/projectadmin"
	memcore "orrery_v2/internal/memory/core"
	memstore "orrery_v2/internal/memory/store"
	memtestkit "orrery_v2/internal/memory/testkit"
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/migration"
	toolres "orrery_v2/internal/resource/tool"
)

func TestProductionCreateProjectWritesOntologyContextGraph(t *testing.T) {
	ctx := context.Background()
	databases, err := db.OpenRegistry(ctx, db.RegistryConfig{Databases: map[string]db.Config{
		db.RoleCore: {Driver: db.DriverMemory, Name: "production-project-ontology"},
	}})
	if err != nil {
		t.Fatalf("open database registry: %v", err)
	}
	defer databases.Close()
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
	now := time.Date(2026, 6, 20, 14, 30, 0, 0, time.UTC)
	api := productionAPI{
		projectCommand:   newAuthorizedProjectTestService(t, projectadmin.NewMemoryRepository(), sequenceProjectIDs{"project-1"}, fixedProjectClock{now: now}, authorizedProjectTestViewer("planner-1"), "org-1"),
		ontologyProject:  ontologyRunner,
		contextGraphCore: core,
		ontologyEvents:   newProductionOntologyEventLog(core),
	}

	project, err := api.CreateProject(ctx, authorizedProjectTestViewer("planner-1"), projectadmin.CreateProjectCommand{
		Title:          "Ontology project",
		OrganizationID: "org-1",
	})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	objects, _, facts, evidence, err := contextgraph.NewPlatformStore(core).Snapshot(ctx, projectScopeTenantID(project.TenantID, project.ProjectID))
	if err != nil {
		t.Fatalf("context graph snapshot: %v", err)
	}
	assertContextGraphObject(t, objects, "project", project.ProjectID)
	assertContextGraphFact(t, facts, "project", project.ProjectID, "title", "Ontology project")
	assertContextGraphFact(t, facts, "project", project.ProjectID, "status", "active")
	assertContextGraphEvidence(t, evidence, "project-created:"+project.ProjectID)
	events, err := api.ontologyEvents.List(ctx)
	if err != nil {
		t.Fatalf("list ontology events: %v", err)
	}
	assertOntologyEvent(t, events, "project-created:"+project.ProjectID, "project.created")
}

func TestProductionCreatedProjectAllowsMemoryScopeLinks(t *testing.T) {
	ctx := context.Background()
	databases, err := db.OpenRegistry(ctx, db.RegistryConfig{Databases: map[string]db.Config{
		db.RoleCore: {Driver: db.DriverMemory, Name: "production-project-memory-scope-ontology"},
	}})
	if err != nil {
		t.Fatalf("open database registry: %v", err)
	}
	defer databases.Close()
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
	now := time.Date(2026, 6, 20, 15, 0, 0, 0, time.UTC)
	api := productionAPI{
		projectCommand:   newAuthorizedProjectTestService(t, projectadmin.NewMemoryRepository(), sequenceProjectIDs{"project-1"}, fixedProjectClock{now: now}, authorizedProjectTestViewer("planner-1"), "org-1"),
		ontologyProject:  ontologyRunner,
		contextGraphCore: core,
	}
	project, err := api.CreateProject(ctx, authorizedProjectTestViewer("planner-1"), projectadmin.CreateProjectCommand{
		Title:          "Memory scope project",
		OrganizationID: "org-1",
	})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	memoryStore := memstore.NewPlatformStore(core)
	if err := memoryStore.SaveCandidate(ctx, memcore.MemoryCandidate{
		ID:            "candidate-project-scope",
		MissionID:     "mission-1",
		SourceType:    memcore.SourceEvidence,
		SourceRef:     "evidence-project-scope",
		ProposedScope: memcore.MemoryScope{Kind: memcore.ScopeProject, ID: project.ProjectID},
		ProposedKind:  agentcore.MemoryKindProject,
		ProposedLayer: memcore.MemoryLayerFact,
		Topic:         "project",
		Summary:       "Project scoped memory should link to authoritative Project object.",
		EvidenceRefs:  []string{"evidence:project-scope"},
		Confidence:    0.9,
		Status:        memcore.CandidateProposed,
		CreatedBy:     agentcore.AgentID("agent-1"),
		CreatedAt:     now,
		UpdatedAt:     now,
	}); err != nil {
		t.Fatalf("save candidate: %v", err)
	}
	sink, err := newProductionOntologyMemoryRecordMetadataSink(ctx, core)
	if err != nil {
		t.Fatalf("new production ontology memory sink: %v", err)
	}
	memoryAdmin := memoryadmin.NewService(memoryStore, &memtestkit.MemoryAuditRecorder{}, func() time.Time { return now }).
		WithMemoryRecordMetadataSink(sink)

	record, err := memoryAdmin.AcceptCandidate(ctx, memoryadmin.ViewerRef{
		ViewerID:        "reviewer-1",
		PermissionScope: []string{memoryadmin.PermissionMemoryReview},
		ProjectIDs:      []string{project.ProjectID},
	}, "candidate-project-scope", memoryadmin.AcceptCandidateRequest{
		ProfileID: "memory-profile-1",
		MemoryID:  "memory-project-scope",
	})
	if err != nil {
		t.Fatalf("AcceptCandidate: %v", err)
	}

	objects, links, _, evidence, err := contextgraph.NewPlatformStore(core).Snapshot(ctx, projectScopeTenantID(project.TenantID, project.ProjectID))
	if err != nil {
		t.Fatalf("context graph snapshot: %v", err)
	}
	assertContextGraphObject(t, objects, "project", project.ProjectID)
	assertContextGraphObject(t, objects, "memory_record", record.MemoryID)
	assertContextGraphLink(t, links, "memory_about_project", record.MemoryID, project.ProjectID)
	assertContextGraphEvidence(t, evidence, "memory-scope-linked:"+record.MemoryID)
}

func TestProductionProjectOrganizationLinkWritesOrganizationView(t *testing.T) {
	ctx := context.Background()
	databases, err := db.OpenRegistry(ctx, db.RegistryConfig{Databases: map[string]db.Config{
		db.RoleCore: {Driver: db.DriverMemory, Name: "production-project-organization-link"},
	}})
	if err != nil {
		t.Fatalf("open database registry: %v", err)
	}
	defer databases.Close()
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
	command := operations.NewCommandService(&organizationOntologyProfileStore{profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{}})
	command.SetOrganizationCatalogStore(orgCatalog)
	command.SetIDGenerator(sequenceOperationsIDs{"org-1"})
	query := operations.NewQueryService(&organizationOntologyProfileStore{}, toolres.NewMemoryRegistry())
	query.SetOrganizationCatalogStore(orgCatalog)
	clock := &steppingProjectClock{values: []time.Time{
		time.Date(2026, 6, 20, 17, 0, 0, 0, time.UTC),
		time.Date(2026, 6, 20, 17, 5, 0, 0, time.UTC),
		time.Date(2026, 6, 20, 17, 10, 0, 0, time.UTC),
	}}
	api := productionAPI{
		operations:       operations.NewService(query, command),
		projectCommand:   newAuthorizedProjectTestService(t, projectadmin.NewMemoryRepository(), sequenceProjectIDs{"project-1"}, clock, authorizedProjectTestViewer("planner-1"), "org-1"),
		ontologyProject:  ontologyRunner,
		contextGraphCore: core,
	}
	org, err := api.CreateOrganization(ctx, operations.ViewerRef{
		ViewerID:        "admin-1",
		ViewerKind:      "user",
		PermissionScope: []string{operations.PermissionOrgManage},
	}, operations.CreateOrganizationCommand{Name: "Platform"})
	if err != nil {
		t.Fatalf("CreateOrganization: %v", err)
	}
	viewer := authorizedProjectTestViewer("planner-1")
	project, err := api.CreateProject(ctx, viewer, projectadmin.CreateProjectCommand{
		Title:          "Organization project",
		OrganizationID: org.OrganizationID,
	})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	project, err = api.UpdateProject(ctx, viewer, projectadmin.UpdateProjectCommand{ProjectID: project.ProjectID, Title: "Renamed organization project", ExpectedVersion: project.Version})
	if err != nil {
		t.Fatalf("UpdateProject: %v", err)
	}
	project, err = api.ArchiveProject(ctx, viewer, projectadmin.ArchiveProjectCommand{ProjectID: project.ProjectID, ExpectedVersion: project.Version})
	if err != nil {
		t.Fatalf("ArchiveProject: %v", err)
	}

	objects, links, facts, evidence, err := contextgraph.NewPlatformStore(core).Snapshot(ctx, org.OrganizationID)
	if err != nil {
		t.Fatalf("organization context graph snapshot: %v", err)
	}
	assertContextGraphObject(t, objects, "organization", org.OrganizationID)
	assertContextGraphObject(t, objects, "project", "project:"+project.ProjectID)
	assertContextGraphLink(t, links, "project_belongs_to_organization", "project:"+project.ProjectID, org.OrganizationID)
	assertContextGraphFact(t, facts, "project", "project:"+project.ProjectID, "title", "Renamed organization project")
	assertContextGraphFact(t, facts, "project", "project:"+project.ProjectID, "status", projectadmin.ProjectStatusArchived)
	assertContextGraphEvidence(t, evidence, "project-organization-linked:"+project.ProjectID+":"+org.OrganizationID+":rev-1")
	assertContextGraphEvidence(t, evidence, "project-organization-linked:"+project.ProjectID+":"+org.OrganizationID+":rev-2")
	assertContextGraphEvidence(t, evidence, "project-organization-linked:"+project.ProjectID+":"+org.OrganizationID+":rev-3")

}

func TestProductionProjectQueriesIncludeContextGraphStatus(t *testing.T) {
	ctx := context.Background()
	databases, err := db.OpenRegistry(ctx, db.RegistryConfig{Databases: map[string]db.Config{
		db.RoleCore: {Driver: db.DriverMemory, Name: "production-project-query-context-graph"},
	}})
	if err != nil {
		t.Fatalf("open database registry: %v", err)
	}
	defer databases.Close()
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
	command := operations.NewCommandService(&organizationOntologyProfileStore{profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{}})
	command.SetOrganizationCatalogStore(orgCatalog)
	command.SetIDGenerator(sequenceOperationsIDs{"org-1"})
	query := operations.NewQueryService(&organizationOntologyProfileStore{}, toolres.NewMemoryRegistry())
	query.SetOrganizationCatalogStore(orgCatalog)
	api := productionAPI{
		operations:       operations.NewService(query, command),
		projectCommand:   newAuthorizedProjectTestService(t, projectadmin.NewMemoryRepository(), sequenceProjectIDs{"project-1"}, fixedProjectClock{now: time.Date(2026, 6, 20, 19, 0, 0, 0, time.UTC)}, authorizedProjectTestViewer("planner-1"), "org-1"),
		ontologyProject:  ontologyRunner,
		contextGraphCore: core,
	}
	org, err := api.CreateOrganization(ctx, operations.ViewerRef{
		ViewerID:        "admin-1",
		ViewerKind:      "user",
		PermissionScope: []string{operations.PermissionOrgManage},
	}, operations.CreateOrganizationCommand{Name: "Platform"})
	if err != nil {
		t.Fatalf("CreateOrganization: %v", err)
	}
	viewer := authorizedProjectTestViewer("planner-1")
	project, err := api.CreateProject(ctx, viewer, projectadmin.CreateProjectCommand{
		Title:          "Queryable project",
		OrganizationID: org.OrganizationID,
	})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	view, err := api.GetProjectView(ctx, viewer, project.ProjectID)
	if err != nil {
		t.Fatalf("GetProjectView: %v", err)
	}
	if view.Project.ProjectID != project.ProjectID || view.Project.OrganizationID != org.OrganizationID {
		t.Fatalf("project view = %+v", view)
	}
	if !view.ContextGraph.ProjectTenantProjected || !view.ContextGraph.OrganizationObjectProjected || !view.ContextGraph.OrganizationViewProjected {
		t.Fatalf("context graph status = %+v", view.ContextGraph)
	}

	page, err := api.ListProjects(ctx, viewer, projectadmin.ListProjectsQuery{OrganizationID: org.OrganizationID, Status: projectadmin.ProjectStatusActive, Limit: 20})
	if err != nil {
		t.Fatalf("ListProjects: %v", err)
	}
	if page.Page.Total != 1 || page.Page.Returned != 1 {
		t.Fatalf("page = %+v", page.Page)
	}
	if len(page.Projects) != 1 || page.Projects[0].Project.ProjectID != project.ProjectID || !page.Projects[0].ContextGraph.OrganizationViewProjected {
		t.Fatalf("projects = %+v", page.Projects)
	}
}

func TestProductionProjectMissingOrganizationStillWritesProjectTenant(t *testing.T) {
	ctx := context.Background()
	databases, err := db.OpenRegistry(ctx, db.RegistryConfig{Databases: map[string]db.Config{
		db.RoleCore: {Driver: db.DriverMemory, Name: "production-project-missing-organization"},
	}})
	if err != nil {
		t.Fatalf("open database registry: %v", err)
	}
	defer databases.Close()
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
	now := time.Date(2026, 6, 20, 18, 0, 0, 0, time.UTC)
	api := productionAPI{
		projectCommand:   newAuthorizedProjectTestService(t, projectadmin.NewMemoryRepository(), sequenceProjectIDs{"project-1"}, fixedProjectClock{now: now}, authorizedProjectTestViewer("planner-1"), "org-missing"),
		ontologyProject:  ontologyRunner,
		contextGraphCore: core,
	}

	project, err := api.CreateProject(ctx, authorizedProjectTestViewer("planner-1"), projectadmin.CreateProjectCommand{
		Title:          "Missing organization project",
		OrganizationID: "org-missing",
	})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	objects, _, facts, _, err := contextgraph.NewPlatformStore(core).Snapshot(ctx, projectScopeTenantID(project.TenantID, project.ProjectID))
	if err != nil {
		t.Fatalf("project context graph snapshot: %v", err)
	}
	assertContextGraphObject(t, objects, "project", project.ProjectID)
	assertContextGraphFact(t, facts, "project", project.ProjectID, "title", "Missing organization project")
	orgObjects, orgLinks, _, _, err := contextgraph.NewPlatformStore(core).Snapshot(ctx, "org-missing")
	if err != nil {
		t.Fatalf("missing organization context graph snapshot: %v", err)
	}
	if len(orgObjects) != 0 || len(orgLinks) != 0 {
		t.Fatalf("missing organization should not receive project view: objects=%+v links=%+v", orgObjects, orgLinks)
	}
	view, err := api.GetProjectView(ctx, authorizedProjectTestViewer("planner-1"), project.ProjectID)
	if err != nil {
		t.Fatalf("GetProjectView: %v", err)
	}
	if !view.ContextGraph.ProjectTenantProjected || view.ContextGraph.OrganizationObjectProjected || view.ContextGraph.OrganizationViewProjected {
		t.Fatalf("context graph status = %+v", view.ContextGraph)
	}
	audit := assertProjectionAudit(t, ctx, contextgraph.NewPlatformStore(core), "org-missing", "projection-skipped:project-organization-linked:"+project.ProjectID+":org-missing:rev-1:missing-organization-target", "project.organization.linked", "missing_organization_target")
	assertProjectionAuditTarget(t, audit, "missing_targets", "organization", "org-missing")
}
