package ontologyscope

import (
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/migration"
)

func RegisterMigrations(coordinator *migration.Coordinator) error {
	if coordinator == nil {
		return nil
	}
	if err := coordinator.RegisterInitializer(db.RoleCore, scopeBindingMigration()); err != nil {
		return err
	}
	if err := coordinator.RegisterInitializer(db.RoleCore, scopeBindingSeekIndexMigration()); err != nil {
		return err
	}
	return coordinator.RegisterInitializer(db.RoleCore, scopeBindingSourceDeletionMigration())
}

func scopeBindingSourceDeletionMigration() migration.Migration {
	statements := sourceDeletionIntentStatements("INTEGER")
	return migration.Migration{
		Version:     "0182_ontology_scope_binding_source_deletions",
		Description: "persist recoverable ontology scope binding source deletions",
		Statements:  statements,
		StatementsByDialect: map[db.Dialect][]string{
			db.DialectPostgres: sourceDeletionIntentStatements("BIGINT"),
		},
	}
}

func sourceDeletionIntentStatements(timeType string) []string {
	return []string{
		`CREATE TABLE IF NOT EXISTS ontology_scope_binding_source_deletions (
			intent_id TEXT PRIMARY KEY,
			source_object_type TEXT NOT NULL,
			source_object_id TEXT NOT NULL,
			canonical_tenant_ids_json TEXT NOT NULL,
			bindings_json TEXT NOT NULL,
			changed_by TEXT NOT NULL,
			recover_after ` + timeType + ` NOT NULL,
			created_at ` + timeType + ` NOT NULL,
			UNIQUE (source_object_type, source_object_id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_ontology_scope_binding_source_deletions_recovery
			ON ontology_scope_binding_source_deletions (recover_after, intent_id)`,
	}
}

func scopeBindingSeekIndexMigration() migration.Migration {
	statements := []string{
		`CREATE INDEX IF NOT EXISTS idx_ontology_scope_bindings_activity_seek ON ontology_scope_bindings (state, kind, updated_at, tenant_id)`,
	}
	return migration.Migration{
		Version:     "0166_ontology_scope_binding_seek_indexes",
		Description: "add bounded ontology scope catalog seek indexes",
		Statements:  statements,
		StatementsByDialect: map[db.Dialect][]string{
			db.DialectPostgres: append([]string(nil), statements...),
		},
	}
}

func scopeBindingMigration() migration.Migration {
	return migration.Migration{
		Version:     "0160_ontology_scope_bindings",
		Description: "create canonical ontology scope bindings and audit history",
		Statements:  scopeBindingStatements("INTEGER"),
		StatementsByDialect: map[db.Dialect][]string{
			db.DialectPostgres: scopeBindingStatements("BIGINT"),
		},
	}
}

func scopeBindingStatements(timeType string) []string {
	return []string{
		`CREATE TABLE IF NOT EXISTS ontology_scope_bindings (
			tenant_id TEXT PRIMARY KEY,
			kind TEXT NOT NULL,
			source_object_type TEXT NOT NULL,
			source_object_id TEXT NOT NULL,
			authorization_tenant_id TEXT NOT NULL,
			read_action TEXT NOT NULL,
			manage_action TEXT NOT NULL,
			label_object_type TEXT NOT NULL DEFAULT '',
			label_object_id TEXT NOT NULL DEFAULT '',
			label_tenant_id TEXT NOT NULL DEFAULT '',
			binding_version BIGINT NOT NULL,
			state TEXT NOT NULL,
			created_at ` + timeType + ` NOT NULL,
			updated_at ` + timeType + ` NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_ontology_scope_bindings_catalog ON ontology_scope_bindings (state, kind, tenant_id)`,
		`CREATE TABLE IF NOT EXISTS ontology_scope_binding_audits (
			tenant_id TEXT NOT NULL,
			binding_version BIGINT NOT NULL,
			kind TEXT NOT NULL,
			source_object_type TEXT NOT NULL,
			source_object_id TEXT NOT NULL,
			authorization_tenant_id TEXT NOT NULL,
			read_action TEXT NOT NULL,
			manage_action TEXT NOT NULL,
			label_object_type TEXT NOT NULL DEFAULT '',
			label_object_id TEXT NOT NULL DEFAULT '',
			label_tenant_id TEXT NOT NULL DEFAULT '',
			state TEXT NOT NULL,
			changed_by TEXT NOT NULL,
			changed_at ` + timeType + ` NOT NULL,
			PRIMARY KEY (tenant_id, binding_version)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_ontology_scope_binding_audits_changed ON ontology_scope_binding_audits (changed_at, tenant_id)`,
	}
}
