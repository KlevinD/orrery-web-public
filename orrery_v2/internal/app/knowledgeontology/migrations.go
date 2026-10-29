package knowledgeontology

import (
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/migration"
)

func RegisterMigrations(coordinator *migration.Coordinator) error {
	if coordinator == nil {
		return nil
	}
	if err := coordinator.RegisterInitializer(db.RoleCore, bridgeMigration()); err != nil {
		return err
	}
	if err := coordinator.RegisterInitializer(db.RoleCore, associationMigration()); err != nil {
		return err
	}
	return coordinator.RegisterInitializer(db.RoleCore, sourceLifecycleMigration())
}

func bridgeMigration() migration.Migration {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS knowledge_ontology_bindings (
			tenant_id TEXT NOT NULL,
			knowledge_base_id TEXT NOT NULL,
			folder_id TEXT NOT NULL,
			source_access_kind TEXT NOT NULL DEFAULT '',
			source_access_id TEXT NOT NULL DEFAULT '',
			source_access_tenant_id TEXT NOT NULL DEFAULT '',
			enabled INTEGER NOT NULL DEFAULT 1,
			updated_at BIGINT NOT NULL,
			PRIMARY KEY (tenant_id, knowledge_base_id),
			UNIQUE (tenant_id, folder_id)
		)`,
		`CREATE TABLE IF NOT EXISTS knowledge_ontology_cursors (
			tenant_id TEXT NOT NULL,
			knowledge_base_id TEXT NOT NULL,
			cursor TEXT NOT NULL DEFAULT '',
			updated_at BIGINT NOT NULL,
			PRIMARY KEY (tenant_id, knowledge_base_id),
			FOREIGN KEY (tenant_id, knowledge_base_id)
				REFERENCES knowledge_ontology_bindings(tenant_id, knowledge_base_id)
				ON DELETE CASCADE
		)`,
		`CREATE TABLE IF NOT EXISTS knowledge_ontology_projections (
			tenant_id TEXT NOT NULL,
			knowledge_base_id TEXT NOT NULL,
			document_id TEXT NOT NULL,
			revision INTEGER NOT NULL,
			content_hash TEXT NOT NULL,
			payload TEXT NOT NULL,
			updated_at BIGINT NOT NULL,
			PRIMARY KEY (tenant_id, document_id, revision),
			FOREIGN KEY (tenant_id, knowledge_base_id)
				REFERENCES knowledge_ontology_bindings(tenant_id, knowledge_base_id)
				ON DELETE CASCADE
		)`,
		`CREATE INDEX IF NOT EXISTS idx_knowledge_ontology_projection_document
			ON knowledge_ontology_projections(tenant_id, knowledge_base_id, document_id, revision DESC)`,
	}
	return migration.Migration{
		Version:     "0186_knowledge_ontology_bridge",
		Description: "persist PageIndex ontology bindings cursors and exact revision projections",
		Statements:  statements,
		StatementsByDialect: map[db.Dialect][]string{
			db.DialectPostgres: append([]string(nil), statements...),
		},
	}
}

func associationMigration() migration.Migration {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS knowledge_ontology_dictionary_versions (
			tenant_id TEXT NOT NULL,
			knowledge_base_id TEXT NOT NULL,
			dictionary_version TEXT NOT NULL,
			updated_at BIGINT NOT NULL,
			PRIMARY KEY (tenant_id, knowledge_base_id)
		)`,
		`CREATE TABLE IF NOT EXISTS knowledge_ontology_mentions (
			tenant_id TEXT NOT NULL,
			knowledge_base_id TEXT NOT NULL,
			document_id TEXT NOT NULL,
			revision INTEGER NOT NULL,
			mention_index INTEGER NOT NULL,
			object_type TEXT NOT NULL,
			resolved_object_id TEXT NOT NULL,
			resolution TEXT NOT NULL,
			status TEXT NOT NULL,
			surface TEXT NOT NULL DEFAULT '',
			anchor TEXT NOT NULL DEFAULT '',
			extraction_method TEXT NOT NULL DEFAULT '',
			confidence REAL NOT NULL DEFAULT 0,
			observed_at BIGINT NOT NULL,
			PRIMARY KEY (tenant_id, document_id, revision, mention_index)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_knowledge_ontology_mentions_object
			ON knowledge_ontology_mentions(tenant_id, resolved_object_id, resolution, knowledge_base_id, document_id, revision DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_knowledge_ontology_mentions_document
			ON knowledge_ontology_mentions(tenant_id, knowledge_base_id, document_id, revision DESC)`,
		`CREATE TABLE IF NOT EXISTS knowledge_ontology_claims (
			tenant_id TEXT NOT NULL,
			knowledge_base_id TEXT NOT NULL,
			document_id TEXT NOT NULL,
			revision INTEGER NOT NULL,
			claim_index INTEGER NOT NULL,
			link_type TEXT NOT NULL,
			from_object_type TEXT NOT NULL,
			from_object_id TEXT NOT NULL,
			to_object_type TEXT NOT NULL,
			to_object_id TEXT NOT NULL,
			status TEXT NOT NULL,
			anchor TEXT NOT NULL DEFAULT '',
			extraction_method TEXT NOT NULL DEFAULT '',
			confidence REAL NOT NULL DEFAULT 0,
			observed_at BIGINT NOT NULL,
			PRIMARY KEY (tenant_id, document_id, revision, claim_index)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_knowledge_ontology_claims_from
			ON knowledge_ontology_claims(tenant_id, from_object_id, knowledge_base_id, document_id, revision DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_knowledge_ontology_claims_to
			ON knowledge_ontology_claims(tenant_id, to_object_id, knowledge_base_id, document_id, revision DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_knowledge_ontology_claims_document
			ON knowledge_ontology_claims(tenant_id, knowledge_base_id, document_id, revision DESC)`,
	}
	return migration.Migration{
		Version:     "0202_knowledge_ontology_associations",
		Description: "index bidirectional ontology mentions and claims for knowledge retrieval",
		Statements:  statements,
		StatementsByDialect: map[db.Dialect][]string{
			db.DialectPostgres: append([]string(nil), statements...),
		},
	}
}
