package ontologyreasoning_test

import (
	"context"
	"testing"

	"orrery_v2/internal/app/ontologyreasoning"
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/migration"
)

func TestRegisterMigrationsCreatesOntologyReasoningTables(t *testing.T) {
	ctx := context.Background()
	databases, err := db.OpenRegistry(ctx, db.RegistryConfig{Databases: map[string]db.Config{
		db.RoleCore: {Driver: db.DriverMemory, Name: "ontology-reasoning-migrations"},
	}})
	if err != nil {
		t.Fatalf("open database registry: %v", err)
	}
	defer databases.Close()
	core, err := databases.Store(db.RoleCore)
	if err != nil {
		t.Fatalf("core database: %v", err)
	}
	coordinator := migration.NewCoordinator(databases)

	if err := ontologyreasoning.RegisterMigrations(coordinator); err != nil {
		t.Fatalf("RegisterMigrations returned error: %v", err)
	}
	if err := coordinator.UpRole(ctx, db.RoleCore); err != nil {
		t.Fatalf("migration up: %v", err)
	}

	if exists, err := core.TableExists(ctx, "ontology_reasoning_runs"); err != nil {
		t.Fatalf("TableExists: %v", err)
	} else if !exists {
		t.Fatalf("missing ontology_reasoning_runs table")
	}
	for _, index := range []string{
		"idx_ontology_reasoning_runs_tenant_status",
		"idx_ontology_reasoning_runs_model",
	} {
		var name string
		if err := core.QueryRow(ctx, `SELECT name FROM sqlite_master WHERE type = 'index' AND name = ?`, index).Scan(&name); err != nil {
			t.Fatalf("missing index %s: %v", index, err)
		}
	}
}
