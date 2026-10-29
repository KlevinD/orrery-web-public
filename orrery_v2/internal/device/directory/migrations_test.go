package directory

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"orrery_v2/internal/app/peopledirectory"
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/migration"
)

func TestDirectoryMigrationCreatesSchemaForSupportedDialects(t *testing.T) {
	for _, dialect := range []db.Dialect{db.DialectSQLite, db.DialectPostgres} {
		joined := strings.Join(schemaStatements(dialect), "\n")
		if joined == "" || strings.Contains(joined, "%!") || strings.Contains(joined, "%[") {
			t.Fatalf("%s schema contains unresolved formatting: %s", dialect, joined)
		}
	}

	ctx := context.Background()
	registry, err := db.OpenRegistry(ctx, db.RegistryConfig{Databases: map[string]db.Config{
		db.RoleCore: {Driver: db.DriverFile, URL: "file://" + filepath.Join(t.TempDir(), "core.db")},
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer registry.Close()
	coordinator := migration.NewCoordinator(registry)
	if err := peopledirectory.RegisterMigrations(coordinator); err != nil {
		t.Fatal(err)
	}
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
	for _, table := range []string{"owned_agent_profiles", "personal_assistant_hosting", "personal_assistant_avatars", "personal_assistant_notification_preferences"} {
		exists, err := store.TableExists(ctx, table)
		if err != nil || !exists {
			t.Fatalf("table %s exists = %v, error = %v", table, exists, err)
		}
	}
	var assignedAgentID string
	if err := store.QueryRow(ctx, `SELECT assigned_agent_id FROM personal_assistant_hosting LIMIT 1`).Scan(&assignedAgentID); err != nil && !strings.Contains(strings.ToLower(err.Error()), "no rows") {
		t.Fatalf("assigned_agent_id column is unavailable: %v", err)
	}
}
