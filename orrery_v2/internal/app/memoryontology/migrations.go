package memoryontology

import (
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/migration"
)

func RegisterMigrations(coordinator *migration.Coordinator) error {
	if coordinator == nil {
		return nil
	}
	return coordinator.RegisterInitializer(db.RoleCore, bridgeMigration())
}

func bridgeMigration() migration.Migration {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS memory_ontology_runs (
			tenant_id TEXT NOT NULL,
			memory_id TEXT NOT NULL,
			content_hash TEXT NOT NULL,
			extractor_version TEXT NOT NULL,
			status TEXT NOT NULL,
			attempts INTEGER NOT NULL DEFAULT 0,
			next_retry_at BIGINT NOT NULL DEFAULT 0,
			last_error TEXT NOT NULL DEFAULT '',
			model_id TEXT NOT NULL DEFAULT '',
			started_at BIGINT NOT NULL DEFAULT 0,
			completed_at BIGINT NOT NULL DEFAULT 0,
			updated_at BIGINT NOT NULL,
			PRIMARY KEY (tenant_id, memory_id, content_hash, extractor_version)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_memory_ontology_runs_ready
			ON memory_ontology_runs(status, next_retry_at, updated_at)`,
		`CREATE INDEX IF NOT EXISTS idx_memory_ontology_runs_memory
			ON memory_ontology_runs(tenant_id, memory_id, updated_at DESC)`,
		`CREATE TABLE IF NOT EXISTS memory_ontology_mentions (
			tenant_id TEXT NOT NULL,
			memory_id TEXT NOT NULL,
			content_hash TEXT NOT NULL,
			extractor_version TEXT NOT NULL,
			mention_index INTEGER NOT NULL,
			surface TEXT NOT NULL,
			object_type TEXT NOT NULL,
			object_hint TEXT NOT NULL DEFAULT '',
			confidence REAL NOT NULL,
			anchor TEXT NOT NULL DEFAULT '',
			resolution TEXT NOT NULL,
			resolved_object_id TEXT NOT NULL DEFAULT '',
			link_type TEXT NOT NULL DEFAULT '',
			evidence_id TEXT NOT NULL DEFAULT '',
			link_id TEXT NOT NULL DEFAULT '',
			created_at BIGINT NOT NULL,
			PRIMARY KEY (tenant_id, memory_id, content_hash, extractor_version, mention_index),
			FOREIGN KEY (tenant_id, memory_id, content_hash, extractor_version)
				REFERENCES memory_ontology_runs(tenant_id, memory_id, content_hash, extractor_version)
				ON DELETE CASCADE
		)`,
		`CREATE INDEX IF NOT EXISTS idx_memory_ontology_mentions_resolved
			ON memory_ontology_mentions(tenant_id, resolved_object_id, resolution)`,
	}
	return migration.Migration{
		Version:     "0201_memory_ontology_bridge",
		Description: "persist memory entity extraction runs and ontology mention projections",
		Statements:  statements,
		StatementsByDialect: map[db.Dialect][]string{
			db.DialectPostgres: append([]string(nil), statements...),
		},
	}
}
