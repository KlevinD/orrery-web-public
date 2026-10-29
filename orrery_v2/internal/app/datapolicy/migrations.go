package datapolicy

import (
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/migration"
)

func RegisterMigrations(coordinator *migration.Coordinator) error {
	if coordinator == nil {
		return nil
	}
	if err := coordinator.RegisterInitializer(db.RoleCore, dataPolicyMigration()); err != nil {
		return err
	}
	return coordinator.RegisterInitializer(db.RoleCore, dataPolicyRevisionMigration())
}

func dataPolicyRevisionMigration() migration.Migration {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS data_policy_revision (
			id TEXT PRIMARY KEY,
			revision BIGINT NOT NULL
		)`,
		`INSERT INTO data_policy_revision (id, revision) VALUES ('global', 0) ON CONFLICT(id) DO NOTHING`,
	}
	return migration.Migration{
		Version:     "0165_data_policy_revision",
		Description: "create monotonic data policy revision",
		Statements:  statements,
		StatementsByDialect: map[db.Dialect][]string{
			db.DialectPostgres: append([]string(nil), statements...),
		},
	}
}

func dataPolicyMigration() migration.Migration {
	return migration.Migration{
		Version:     "0140_data_policy",
		Description: "create field and purpose policy tables",
		Statements: []string{
			fieldPoliciesTableSQL("INTEGER"),
			`CREATE INDEX IF NOT EXISTS idx_field_policies_tenant_scope ON field_policies (tenant_id, consumer, purpose, object_type)`,
			purposePoliciesTableSQL("INTEGER"),
			`CREATE INDEX IF NOT EXISTS idx_purpose_policies_tenant_scope ON purpose_policies (tenant_id, consumer, purpose, object_type)`,
		},
		StatementsByDialect: map[db.Dialect][]string{
			db.DialectPostgres: {
				fieldPoliciesTableSQL("BIGINT"),
				`CREATE INDEX IF NOT EXISTS idx_field_policies_tenant_scope ON field_policies (tenant_id, consumer, purpose, object_type)`,
				purposePoliciesTableSQL("BIGINT"),
				`CREATE INDEX IF NOT EXISTS idx_purpose_policies_tenant_scope ON purpose_policies (tenant_id, consumer, purpose, object_type)`,
			},
		},
	}
}

func fieldPoliciesTableSQL(timeType string) string {
	return `CREATE TABLE IF NOT EXISTS field_policies (
		id TEXT PRIMARY KEY,
		tenant_id TEXT NOT NULL,
		consumer TEXT NOT NULL,
		purpose TEXT NOT NULL,
		object_type TEXT NOT NULL,
		field_path TEXT NOT NULL,
		effect TEXT NOT NULL,
		reason TEXT NOT NULL DEFAULT '',
		enabled BOOLEAN NOT NULL DEFAULT TRUE,
		created_at ` + timeType + ` NOT NULL,
		updated_at ` + timeType + ` NOT NULL
	)`
}

func purposePoliciesTableSQL(timeType string) string {
	return `CREATE TABLE IF NOT EXISTS purpose_policies (
		id TEXT PRIMARY KEY,
		tenant_id TEXT NOT NULL,
		consumer TEXT NOT NULL,
		purpose TEXT NOT NULL,
		object_type TEXT NOT NULL,
		effect TEXT NOT NULL,
		reason TEXT NOT NULL DEFAULT '',
		enabled BOOLEAN NOT NULL DEFAULT TRUE,
		created_at ` + timeType + ` NOT NULL,
		updated_at ` + timeType + ` NOT NULL
	)`
}
