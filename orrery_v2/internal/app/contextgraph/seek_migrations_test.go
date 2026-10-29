package contextgraph

import (
	"context"
	"testing"

	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/migration"
)

func TestPlatformQueryWorkbenchSeekIndexes(t *testing.T) {
	ctx := context.Background()
	database, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: "context-graph-seek-indexes"})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer database.Close()
	migrations := []migration.Migration{
		contextGraphMigration(),
		contextGraphProjectionAuditMigration(),
		contextGraphTenantIsolationMigration(),
		contextGraphAccessRefMigration(),
		contextGraphAuditAccessRefMigration(),
		contextGraphAccessRefBackfillAuditMigration(),
		contextGraphSeekIndexMigration(),
		contextGraphLineageSeekIndexMigration(),
	}
	if err := migration.NewRunner(migrations).Up(ctx, database); err != nil {
		t.Fatalf("run migrations: %v", err)
	}
	for _, indexName := range []string{
		"idx_context_graph_objects_workbench_seek",
		"idx_context_graph_links_from_workbench_seek",
		"idx_context_graph_links_to_workbench_seek",
		"idx_context_graph_facts_workbench_seek",
		"idx_context_graph_evidence_workbench_seek",
		"idx_context_graph_assertions_workbench_seek",
		"idx_context_graph_projection_audits_workbench_seek",
		"idx_context_graph_projection_replay_workbench_seek",
		"idx_context_graph_facts_lineage_seek",
		"idx_context_graph_projection_audits_lineage_seek",
	} {
		var found string
		if err := database.QueryRow(ctx, `SELECT name FROM sqlite_master WHERE type = 'index' AND name = ?`, indexName).Scan(&found); err != nil {
			t.Fatalf("query SQLite index %s: %v", indexName, err)
		}
	}
	seekMigration := contextGraphSeekIndexMigration()
	postgres := seekMigration.StatementsByDialect[db.DialectPostgres]
	if len(postgres) != len(seekMigration.Statements) || len(postgres) == 0 {
		t.Fatalf("PostgreSQL seek statements = %v", postgres)
	}
	lineageMigration := contextGraphLineageSeekIndexMigration()
	postgres = lineageMigration.StatementsByDialect[db.DialectPostgres]
	if len(postgres) != len(lineageMigration.Statements) || len(postgres) == 0 {
		t.Fatalf("PostgreSQL lineage seek statements = %v", postgres)
	}
}
