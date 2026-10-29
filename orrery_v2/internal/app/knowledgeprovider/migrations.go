package knowledgeprovider

import (
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/migration"
)

func RegisterMigrations(coordinator *migration.Coordinator) error {
	if coordinator == nil {
		return nil
	}
	for _, item := range Migrations() {
		if err := coordinator.RegisterInitializer(db.RoleCore, item); err != nil {
			return err
		}
	}
	return nil
}

func Migrations() []migration.Migration {
	return []migration.Migration{{
		Version:     "0187_mcp_knowledge_provider_bindings",
		Description: "create MCP knowledge provider bindings and external mappings",
		Statements:  knowledgeProviderMigrationStatements("TEXT"),
		StatementsByDialect: map[db.Dialect][]string{
			db.DialectPostgres: knowledgeProviderMigrationStatements("JSONB"),
		},
	}}
}

func knowledgeProviderMigrationStatements(jsonType string) []string {
	return []string{
		`CREATE TABLE IF NOT EXISTS knowledge_provider_bindings (
			id TEXT PRIMARY KEY,
			mcp_server_id TEXT NOT NULL,
			status TEXT NOT NULL,
			payload ` + jsonType + ` NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_knowledge_provider_bindings_server ON knowledge_provider_bindings (mcp_server_id, status)`,
		`CREATE TABLE IF NOT EXISTS knowledge_external_mappings (
			id TEXT PRIMARY KEY,
			binding_id TEXT NOT NULL,
			kind TEXT NOT NULL,
			external_id TEXT NOT NULL,
			local_id TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			UNIQUE (binding_id, kind, external_id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_knowledge_external_mappings_local ON knowledge_external_mappings (binding_id, kind, local_id)`,
	}
}
