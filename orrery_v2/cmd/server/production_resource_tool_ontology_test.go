package main

import (
	"context"
	"testing"

	"orrery_v2/internal/app/contextgraph"
	"orrery_v2/internal/app/ontology"
	resourceadmin "orrery_v2/internal/app/resourceadmin"
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/migration"
	skillres "orrery_v2/internal/resource/skill"
	toolres "orrery_v2/internal/resource/tool"
)

func TestProductionRegisterToolDoesNotWriteLegacyToolOntologyEvent(t *testing.T) {
	ctx := context.Background()
	databases, err := db.OpenRegistry(ctx, db.RegistryConfig{Databases: map[string]db.Config{
		db.RoleCore: {Driver: db.DriverMemory, Name: "production-resource-tool-ontology"},
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
	api := productionAPI{
		resourceCommand:  resourceadmin.NewCommandService(toolres.NewMemoryRegistry(), skillres.NewMemoryRegistry()),
		ontologyEvents:   newProductionOntologyEventLog(core),
		contextGraphCore: core,
	}

	tool, err := api.RegisterTool(ctx, resourceadmin.ViewerRef{
		ViewerID:        "resource-admin-1",
		PermissionScope: []string{resourceadmin.PermissionResourceManage},
	}, resourceadmin.ToolDefinitionRequest{
		ToolID:      "tool.deploy",
		Kind:        string(toolres.KindCLITool),
		Name:        "Deploy",
		Description: "Deploy service",
		Version:     "1.0.0",
		RiskLevel:   string(toolres.RiskHigh),
		Command:     "deploy",
	})
	if err != nil {
		t.Fatalf("RegisterTool: %v", err)
	}
	if tool.ToolID == "" {
		t.Fatal("registered tool id is empty")
	}

	objects, _, _, evidence, err := contextgraph.NewPlatformStore(core).Snapshot(ctx, "tool:"+tool.ToolID)
	if err != nil {
		t.Fatalf("context graph snapshot: %v", err)
	}
	assertNoContextGraphObject(t, objects, "tool", tool.ToolID)
	assertNoContextGraphEvidence(t, evidence, "resource-tool-registered:"+tool.ToolID)
	events, err := api.ontologyEvents.List(ctx)
	if err != nil {
		t.Fatalf("list ontology events: %v", err)
	}
	assertNoOntologyEvent(t, events, "resource-tool-registered:"+tool.ToolID, "resource.tool.registered")
}
