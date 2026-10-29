package ontologyscope

import (
	"context"
	"testing"

	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/migration"
)

func TestPlatformQuerySeekIndexesCoverScopeCatalogForSQLiteAndPostgres(t *testing.T) {
	ctx := context.Background()
	store, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: "ontology-scope-seek-indexes"})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer store.Close()
	seekMigration := scopeBindingSeekIndexMigration()
	if err := migration.NewRunner([]migration.Migration{scopeBindingMigration(), seekMigration}).Up(ctx, store); err != nil {
		t.Fatalf("run migrations: %v", err)
	}
	const indexName = "idx_ontology_scope_bindings_activity_seek"
	var found string
	if err := store.QueryRow(ctx, `SELECT name FROM sqlite_master WHERE type = 'index' AND name = ?`, indexName).Scan(&found); err != nil {
		t.Fatalf("query SQLite seek index: %v", err)
	}
	if found != indexName {
		t.Fatalf("SQLite index = %q", found)
	}
	postgres := seekMigration.StatementsByDialect[db.DialectPostgres]
	if len(postgres) != len(seekMigration.Statements) || len(postgres) == 0 {
		t.Fatalf("PostgreSQL seek statements = %v", postgres)
	}
}
