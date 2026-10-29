package identity

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/migration"
)

func TestSchemaStatementsRenderForSupportedDialects(t *testing.T) {
	for _, dialect := range []db.Dialect{db.DialectSQLite, db.DialectPostgres} {
		joined := strings.Join(schemaStatements(dialect), "\n")
		if joined == "" || strings.Contains(joined, "%!") || strings.Contains(joined, "%[") {
			t.Fatalf("%s schema contains unresolved formatting: %s", dialect, joined)
		}
		if dialect == db.DialectPostgres && !strings.Contains(joined, "BIGINT") {
			t.Fatalf("Postgres schema does not use BIGINT: %s", joined)
		}
		if dialect == db.DialectSQLite && !strings.Contains(joined, "INTEGER") {
			t.Fatalf("SQLite schema does not use INTEGER: %s", joined)
		}
	}
}

func TestMigrationCreatesIdentitySchema(t *testing.T) {
	ctx := context.Background()
	registry, err := db.OpenRegistry(ctx, db.RegistryConfig{Databases: map[string]db.Config{
		db.RoleCore: {Driver: db.DriverFile, URL: "file://" + filepath.Join(t.TempDir(), "core.db")},
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer registry.Close()
	coordinator := migration.NewCoordinator(registry)
	if err := RegisterMigrations(coordinator); err != nil {
		t.Fatal(err)
	}
	if err := coordinator.UpRole(ctx, db.RoleCore); err != nil {
		t.Fatal(err)
	}
	store, err := registry.Store(db.RoleCore)
	if err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"device_oauth_codes", "device_pairings", "devices", "device_access_tokens", "device_connection_challenges"} {
		exists, err := store.TableExists(ctx, table)
		if err != nil || !exists {
			t.Fatalf("table %s exists = %v, error = %v", table, exists, err)
		}
	}
	var version string
	if err := store.QueryRow(ctx, `SELECT version FROM schema_migrations WHERE version = ?`, "0191_device_delivery_identity").Scan(&version); err != nil {
		t.Fatalf("migration record: %v", err)
	}
}
