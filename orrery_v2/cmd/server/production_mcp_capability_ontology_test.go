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

func TestProductionMCPRegistrationProjectsCapabilitiesIntoContextGraph(t *testing.T) {
	ctx, core := openProductionMCPProjectionCore(t, "production-mcp-capability-ontology")
	api := productionAPI{
		contextGraphCore: core,
		resourceCommand: resourceadmin.NewCommandService(
			toolres.NewPlatformRegistry(core),
			skillres.NewMemoryRegistry(),
		),
	}

	registered, err := api.RegisterMCPServer(ctx, resourceadmin.ViewerRef{
		ViewerID:        "admin",
		PermissionScope: []string{resourceadmin.PermissionResourceManage},
	}, resourceadmin.RegisterMCPServerRequest{
		ToolID:      "mcp.github",
		Name:        "GitHub MCP",
		ServerName:  "github",
		Transport:   string(toolres.TransportStdio),
		Command:     "github-mcp-server",
		Description: "GitHub MCP server",
		ExposedToolDetails: []resourceadmin.MCPExposedToolDTO{{
			Name:        "search_issues",
			Description: "Search GitHub issues.",
			InputSchema: map[string]any{
				"type": "object",
			},
			OutputSchema: map[string]any{
				"type": "object",
			},
		}},
	})
	if err != nil {
		t.Fatalf("RegisterMCPServer: %v", err)
	}
	serverID := registered.ToolID
	toolID := serverID + ".search_issues"

	objects, links, facts, evidence, err := contextgraph.NewPlatformStore(core).Snapshot(ctx, "system")
	if err != nil {
		t.Fatalf("context graph snapshot: %v", err)
	}
	assertContextGraphObject(t, objects, "mcp_server", serverID)
	assertContextGraphObject(t, objects, "tool", toolID)
	assertContextGraphLink(t, links, "tool_backed_by_mcp_server", toolID, serverID)
	assertContextGraphFact(t, facts, "mcp_server", serverID, "server_name", "github")
	assertContextGraphFact(t, facts, "mcp_server", serverID, "transport", string(toolres.TransportStdio))
	assertContextGraphFact(t, facts, "tool", toolID, "name", "search_issues")
	assertContextGraphFact(t, facts, "tool", toolID, "risk_level", string(toolres.RiskMedium))
	assertContextGraphFact(t, facts, "tool", toolID, "provider", "mcp")
	assertContextGraphFact(t, facts, "tool", toolID, "provider_tool_id", serverID)
	assertContextGraphFact(t, facts, "tool", toolID, "description", "Search GitHub issues.")
	assertContextGraphFact(t, facts, "tool", toolID, "input_schema", map[string]any{"type": "object"})
	assertContextGraphFact(t, facts, "tool", toolID, "output_schema", map[string]any{"type": "object"})
	assertContextGraphEvidence(t, evidence, "mcp-capability-projected:"+serverID)
}

func TestProductionMCPUpdateRefreshesProjectedCapabilities(t *testing.T) {
	ctx, core := openProductionMCPProjectionCore(t, "production-mcp-capability-refresh")
	api := productionAPI{
		contextGraphCore: core,
		resourceCommand: resourceadmin.NewCommandService(
			toolres.NewPlatformRegistry(core),
			skillres.NewMemoryRegistry(),
		),
	}

	viewer := resourceadmin.ViewerRef{
		ViewerID:        "admin",
		PermissionScope: []string{resourceadmin.PermissionResourceManage},
	}
	registered, err := api.RegisterMCPServer(ctx, viewer, resourceadmin.RegisterMCPServerRequest{
		ToolID:     "mcp.github",
		Name:       "GitHub MCP",
		ServerName: "github",
		Transport:  string(toolres.TransportStdio),
		Command:    "github-mcp-server",
		ExposedToolDetails: []resourceadmin.MCPExposedToolDTO{{
			Name:        "search_issues",
			Description: "Search GitHub issues.",
			InputSchema: map[string]any{
				"type": "object",
			},
			OutputSchema: map[string]any{
				"type": "object",
			},
		}},
	})
	if err != nil {
		t.Fatalf("RegisterMCPServer: %v", err)
	}
	serverID := registered.ToolID
	oldToolID := serverID + ".search_issues"
	newToolID := serverID + ".create_issue"

	_, err = api.UpdateMCPServer(ctx, viewer, resourceadmin.RegisterMCPServerRequest{
		ToolID:     serverID,
		Name:       "GitHub MCP",
		ServerName: "github",
		Transport:  string(toolres.TransportStdio),
		Command:    "github-mcp-server",
		ExposedToolDetails: []resourceadmin.MCPExposedToolDTO{{
			Name:        "create_issue",
			Description: "Create GitHub issues.",
		}},
	})
	if err != nil {
		t.Fatalf("UpdateMCPServer: %v", err)
	}

	objects, links, facts, _, err := contextgraph.NewPlatformStore(core).Snapshot(ctx, "system")
	if err != nil {
		t.Fatalf("context graph snapshot: %v", err)
	}
	assertContextGraphObject(t, objects, "tool", newToolID)
	assertContextGraphLink(t, links, "tool_backed_by_mcp_server", newToolID, serverID)
	assertNoContextGraphObject(t, objects, "tool", oldToolID)
	assertNoContextGraphLink(t, links, "tool_backed_by_mcp_server", oldToolID, serverID)
	assertNoContextGraphFact(t, facts, "tool", oldToolID, "name")
	assertNoContextGraphFact(t, facts, "tool", oldToolID, "provider")
	assertNoContextGraphFact(t, facts, "tool", oldToolID, "provider_tool_id")
	assertNoContextGraphFact(t, facts, "tool", oldToolID, "input_schema")
	assertNoContextGraphFact(t, facts, "tool", oldToolID, "output_schema")
}

func openProductionMCPProjectionCore(t *testing.T, name string) (context.Context, *db.Store) {
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
	core, err := databases.Store(db.RoleCore)
	if err != nil {
		t.Fatalf("core database: %v", err)
	}
	coordinator := migration.NewCoordinator(databases)
	if err := ontology.RegisterMigrations(coordinator); err != nil {
		t.Fatalf("register ontology migrations: %v", err)
	}
	if err := contextgraph.RegisterMigrations(coordinator); err != nil {
		t.Fatalf("register context graph migrations: %v", err)
	}
	if err := coordinator.UpRole(ctx, db.RoleCore); err != nil {
		t.Fatalf("migration up: %v", err)
	}
	if err := ontology.InstallDefaultOntologyDefinitions(ctx, ontology.NewPlatformRegistry(core)); err != nil {
		t.Fatalf("install default ontology: %v", err)
	}
	return ctx, core
}

func assertNoContextGraphFact(t *testing.T, facts []contextgraph.Fact, subjectKind string, subjectID string, predicate string) {
	t.Helper()
	for _, fact := range facts {
		if fact.Subject.Kind == subjectKind && fact.Subject.ID == subjectID && fact.Predicate == predicate {
			t.Fatalf("fact %s/%s/%s should not exist", subjectKind, subjectID, predicate)
		}
	}
}
