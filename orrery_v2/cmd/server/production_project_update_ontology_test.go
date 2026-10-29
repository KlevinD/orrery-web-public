package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"orrery_v2/internal/app/contextgraph"
	"orrery_v2/internal/app/ontology"
	projectadmin "orrery_v2/internal/app/projectadmin"
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/migration"
)

func TestProductionProjectionAuditAcknowledgeDoesNotOverrideReplayedAudit(t *testing.T) {
	ctx := context.Background()
	databases, err := db.OpenRegistry(ctx, db.RegistryConfig{Databases: map[string]db.Config{
		db.RoleCore: {Driver: db.DriverMemory, Name: "production-projection-audit-ack-replayed"},
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
	if err := contextgraph.RegisterMigrations(coordinator); err != nil {
		t.Fatalf("register context graph migrations: %v", err)
	}
	if err := coordinator.UpRole(ctx, db.RoleCore); err != nil {
		t.Fatalf("migrate core db: %v", err)
	}
	graphStore := contextgraph.NewPlatformStore(core)
	audit := contextgraph.ProjectionAudit{
		ID:              "projection-skipped:event-1:missing-target",
		TenantID:        "org-1",
		SourceEventID:   "event-1",
		SourceEventType: "project.organization.linked",
		Status:          "replayed",
		ReasonCode:      "missing_organization_target",
		Reason:          "target restored",
		Details:         map[string]any{"replayed_by": "operator-1"},
		ObservedAt:      time.Date(2026, 6, 20, 22, 0, 0, 0, time.UTC),
	}
	if err := graphStore.SaveProjectionAudit(ctx, audit); err != nil {
		t.Fatalf("SaveProjectionAudit: %v", err)
	}
	api := productionAPI{contextGraphCore: core}

	_, err = api.computeProjectionAuditAcknowledgement(ctx, audit, contextgraph.ProjectionAuditAcknowledgeRequest{
		ActorKind: "user",
		ActorID:   "operator-2",
		Reason:    "reviewed after replay",
	})
	if !errors.Is(err, projectadmin.ErrInvalidRequest) {
		t.Fatalf("AcknowledgeProjectionAudit error = %v, want ErrInvalidRequest", err)
	}
	stored, err := graphStore.GetProjectionAudit(ctx, "org-1", audit.ID)
	if err != nil {
		t.Fatalf("GetProjectionAudit: %v", err)
	}
	if stored.Status != "replayed" || stored.Reason != "target restored" || stored.Revision != 1 || !stored.ObservedAt.Equal(audit.ObservedAt) {
		t.Fatalf("replayed audit was changed: before=%+v after=%+v", audit, stored)
	}
}

func TestProductionUpdateAndArchiveProjectWriteOntologyContextGraph(t *testing.T) {
	ctx := context.Background()
	databases, err := db.OpenRegistry(ctx, db.RegistryConfig{Databases: map[string]db.Config{
		db.RoleCore: {Driver: db.DriverMemory, Name: "production-project-lifecycle-ontology"},
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
	clock := &steppingProjectClock{values: []time.Time{
		time.Date(2026, 6, 20, 16, 0, 0, 0, time.UTC),
		time.Date(2026, 6, 20, 16, 5, 0, 0, time.UTC),
		time.Date(2026, 6, 20, 16, 10, 0, 0, time.UTC),
	}}
	viewer := authorizedProjectTestViewer("planner-1")
	api := productionAPI{
		projectCommand:   newAuthorizedProjectTestService(t, projectadmin.NewMemoryRepository(), sequenceProjectIDs{"project-1"}, clock, viewer, "org-1"),
		ontologyProject:  ontologyRunner,
		ontologyEvents:   newProductionOntologyEventLog(core),
		contextGraphCore: core,
	}
	project, err := api.CreateProject(ctx, viewer, projectadmin.CreateProjectCommand{Title: "Original project", OrganizationID: "org-1"})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	project, err = api.UpdateProject(ctx, viewer, projectadmin.UpdateProjectCommand{ProjectID: project.ProjectID, Title: "Updated project", ExpectedVersion: project.Version})
	if err != nil {
		t.Fatalf("UpdateProject: %v", err)
	}
	project, err = api.ArchiveProject(ctx, viewer, projectadmin.ArchiveProjectCommand{ProjectID: project.ProjectID, ExpectedVersion: project.Version})
	if err != nil {
		t.Fatalf("ArchiveProject: %v", err)
	}

	objects, _, facts, evidence, err := contextgraph.NewPlatformStore(core).Snapshot(ctx, projectScopeTenantID(project.TenantID, project.ProjectID))
	if err != nil {
		t.Fatalf("context graph snapshot: %v", err)
	}
	assertContextGraphObject(t, objects, "project", project.ProjectID)
	assertContextGraphFact(t, facts, "project", project.ProjectID, "title", "Updated project")
	assertContextGraphFact(t, facts, "project", project.ProjectID, "status", projectadmin.ProjectStatusArchived)
	assertContextGraphEvidence(t, evidence, "project-created:"+project.ProjectID)
	assertContextGraphEvidence(t, evidence, "project-updated:"+project.ProjectID+":rev-2")
	assertContextGraphEvidence(t, evidence, "project-archived:"+project.ProjectID+":rev-3")
	events, err := api.ontologyEvents.List(ctx)
	if err != nil {
		t.Fatalf("list ontology events: %v", err)
	}
	assertOntologyEvent(t, events, "project-updated:"+project.ProjectID+":rev-2", "project.updated")
	assertOntologyEvent(t, events, "project-archived:"+project.ProjectID+":rev-3", "project.archived")
}
