package actioncontract

import (
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/migration"
)

func RegisterMigrations(coordinator *migration.Coordinator) error {
	if coordinator == nil {
		return nil
	}
	if err := coordinator.RegisterInitializer(db.RoleCore, actionContractMigration()); err != nil {
		return err
	}
	if err := coordinator.RegisterInitializer(db.RoleCore, actionTypeSchemaSeekIndexMigration()); err != nil {
		return err
	}
	if err := coordinator.RegisterInitializer(db.RoleCore, actionTypeSemanticsMigration()); err != nil {
		return err
	}
	return coordinator.RegisterInitializer(db.RoleCore, actionRunExecutionModeMigration())
}

func actionTypeSemanticsMigration() migration.Migration {
	return migration.Migration{
		Version:     "0203_action_type_semantics",
		Description: "persist action verbs, semantic roles, preconditions, effects, and decomposition",
		Statements: []string{
			`ALTER TABLE action_types ADD COLUMN semantics_json TEXT NOT NULL DEFAULT '{}'`,
		},
		StatementsByDialect: map[db.Dialect][]string{
			db.DialectPostgres: {
				`ALTER TABLE action_types ADD COLUMN semantics_json JSONB NOT NULL DEFAULT '{}'`,
			},
		},
	}
}

func actionRunExecutionModeMigration() migration.Migration {
	statements := []string{
		`ALTER TABLE action_runs ADD COLUMN execution_mode TEXT NOT NULL DEFAULT 'apply'`,
	}
	return migration.Migration{
		Version:     "0206_action_run_execution_mode",
		Description: "distinguish dry-run action verification from production mutation",
		Statements:  statements,
		StatementsByDialect: map[db.Dialect][]string{
			db.DialectPostgres: append([]string(nil), statements...),
		},
	}
}

func actionTypeSchemaSeekIndexMigration() migration.Migration {
	statements := []string{
		`CREATE INDEX IF NOT EXISTS idx_action_types_schema_seek ON action_types (tenant_id, id)`,
	}
	return migration.Migration{
		Version:     "0169_action_type_schema_seek_index",
		Description: "add tenant and id seek index for bounded action type schema reads",
		Statements:  statements,
		StatementsByDialect: map[db.Dialect][]string{
			db.DialectPostgres: append([]string(nil), statements...),
		},
	}
}

func actionContractMigration() migration.Migration {
	return migration.Migration{
		Version:     "0110_action_contract",
		Description: "create ontology action contract tables",
		Statements: []string{
			actionTypesTableSQL("TEXT", "INTEGER"),
			`CREATE INDEX IF NOT EXISTS idx_action_types_tenant_category ON action_types (tenant_id, category)`,
			`CREATE INDEX IF NOT EXISTS idx_action_types_tenant_provider ON action_types (tenant_id, provider_kind, provider_ref)`,
			actionRunsTableSQL("TEXT", "INTEGER"),
			`CREATE INDEX IF NOT EXISTS idx_action_runs_tenant_action_type ON action_runs (tenant_id, action_type_id, created_at)`,
			`CREATE INDEX IF NOT EXISTS idx_action_runs_tenant_status ON action_runs (tenant_id, status, created_at)`,
			`CREATE INDEX IF NOT EXISTS idx_action_runs_effective_user ON action_runs (tenant_id, effective_user_kind, effective_user_id, created_at)`,
			actionWritebackReceiptsTableSQL("TEXT", "INTEGER"),
			`CREATE INDEX IF NOT EXISTS idx_action_writebacks_run ON action_writeback_receipts (tenant_id, action_run_id, created_at)`,
			`CREATE INDEX IF NOT EXISTS idx_action_writebacks_target ON action_writeback_receipts (tenant_id, target_system, created_at)`,
		},
		StatementsByDialect: map[db.Dialect][]string{
			db.DialectPostgres: {
				actionTypesTableSQL("JSONB", "BIGINT"),
				`CREATE INDEX IF NOT EXISTS idx_action_types_tenant_category ON action_types (tenant_id, category)`,
				`CREATE INDEX IF NOT EXISTS idx_action_types_tenant_provider ON action_types (tenant_id, provider_kind, provider_ref)`,
				`CREATE INDEX IF NOT EXISTS idx_action_types_input_schema_gin ON action_types USING GIN (input_schema_json)`,
				actionRunsTableSQL("JSONB", "BIGINT"),
				`CREATE INDEX IF NOT EXISTS idx_action_runs_tenant_action_type ON action_runs (tenant_id, action_type_id, created_at)`,
				`CREATE INDEX IF NOT EXISTS idx_action_runs_tenant_status ON action_runs (tenant_id, status, created_at)`,
				`CREATE INDEX IF NOT EXISTS idx_action_runs_effective_user ON action_runs (tenant_id, effective_user_kind, effective_user_id, created_at)`,
				`CREATE INDEX IF NOT EXISTS idx_action_runs_object_refs_gin ON action_runs USING GIN (object_refs_json)`,
				actionWritebackReceiptsTableSQL("JSONB", "BIGINT"),
				`CREATE INDEX IF NOT EXISTS idx_action_writebacks_run ON action_writeback_receipts (tenant_id, action_run_id, created_at)`,
				`CREATE INDEX IF NOT EXISTS idx_action_writebacks_target ON action_writeback_receipts (tenant_id, target_system, created_at)`,
			},
		},
	}
}

func actionTypesTableSQL(jsonType, timeType string) string {
	return `CREATE TABLE IF NOT EXISTS action_types (
		id TEXT PRIMARY KEY,
		tenant_id TEXT NOT NULL,
		name TEXT NOT NULL,
		category TEXT NOT NULL,
		description TEXT NOT NULL DEFAULT '',
		object_type_refs_json ` + jsonType + ` NOT NULL DEFAULT '[]',
		provider_kind TEXT NOT NULL,
		provider_ref TEXT NOT NULL DEFAULT '',
		input_schema_json ` + jsonType + ` NOT NULL DEFAULT '{}',
		output_schema_json ` + jsonType + ` NOT NULL DEFAULT '{}',
		resource_resolver TEXT NOT NULL DEFAULT '',
		permission_scope TEXT NOT NULL DEFAULT '',
		field_profile TEXT NOT NULL DEFAULT '',
		data_policy TEXT NOT NULL DEFAULT '',
		purpose_policy TEXT NOT NULL DEFAULT '',
		risk_level TEXT NOT NULL,
		hil_policy_json ` + jsonType + ` NOT NULL DEFAULT '{}',
		dry_run_supported BOOLEAN NOT NULL DEFAULT FALSE,
		writeback_target_json ` + jsonType + ` NOT NULL DEFAULT '{}',
		idempotency_policy_json ` + jsonType + ` NOT NULL DEFAULT '{}',
		audit_policy_json ` + jsonType + ` NOT NULL DEFAULT '{}',
		failure_policy_json ` + jsonType + ` NOT NULL DEFAULT '{}',
		enabled BOOLEAN NOT NULL DEFAULT TRUE,
		version TEXT NOT NULL,
		created_at ` + timeType + ` NOT NULL,
		updated_at ` + timeType + ` NOT NULL
	)`
}

func actionRunsTableSQL(jsonType, timeType string) string {
	return `CREATE TABLE IF NOT EXISTS action_runs (
		id TEXT PRIMARY KEY,
		tenant_id TEXT NOT NULL,
		workspace_id TEXT NOT NULL DEFAULT '',
		action_type_id TEXT NOT NULL,
		action_category TEXT NOT NULL,
		actor_kind TEXT NOT NULL,
		actor_id TEXT NOT NULL,
		actor_name TEXT NOT NULL DEFAULT '',
		effective_user_kind TEXT NOT NULL DEFAULT '',
		effective_user_id TEXT NOT NULL DEFAULT '',
		effective_user_name TEXT NOT NULL DEFAULT '',
		delegation_json ` + jsonType + ` NOT NULL DEFAULT '{}',
		runtime_kind TEXT NOT NULL,
		runtime_id TEXT NOT NULL,
		session_json ` + jsonType + ` NOT NULL DEFAULT '{}',
		human_intent_json ` + jsonType + ` NOT NULL DEFAULT '{}',
		approval_json ` + jsonType + ` NOT NULL DEFAULT '{}',
		purpose TEXT NOT NULL DEFAULT '',
		object_refs_json ` + jsonType + ` NOT NULL DEFAULT '[]',
		requested_fields_json ` + jsonType + ` NOT NULL DEFAULT '[]',
		args_hash TEXT NOT NULL,
		risk_level TEXT NOT NULL,
		policy_decision_id TEXT NOT NULL DEFAULT '',
		hil_token_id TEXT NOT NULL DEFAULT '',
		status TEXT NOT NULL,
		provider_call_ref_json ` + jsonType + ` NOT NULL DEFAULT '{}',
		result_ref_json ` + jsonType + ` NOT NULL DEFAULT '{}',
		evidence_refs_json ` + jsonType + ` NOT NULL DEFAULT '[]',
		audit_ref_json ` + jsonType + ` NOT NULL DEFAULT '{}',
		error_code TEXT NOT NULL DEFAULT '',
		error_message TEXT NOT NULL DEFAULT '',
		created_at ` + timeType + ` NOT NULL,
		completed_at ` + timeType + `,
		FOREIGN KEY (action_type_id) REFERENCES action_types(id)
	)`
}

func actionWritebackReceiptsTableSQL(jsonType, timeType string) string {
	return `CREATE TABLE IF NOT EXISTS action_writeback_receipts (
		id TEXT PRIMARY KEY,
		tenant_id TEXT NOT NULL,
		action_run_id TEXT NOT NULL,
		target_system TEXT NOT NULL,
		target_resource_refs_json ` + jsonType + ` NOT NULL DEFAULT '[]',
		before_hash TEXT NOT NULL DEFAULT '',
		after_hash TEXT NOT NULL DEFAULT '',
		changed_fields_json ` + jsonType + ` NOT NULL DEFAULT '[]',
		external_operation_id TEXT NOT NULL DEFAULT '',
		idempotency_key TEXT NOT NULL DEFAULT '',
		status TEXT NOT NULL,
		evidence_refs_json ` + jsonType + ` NOT NULL DEFAULT '[]',
		audit_ref_json ` + jsonType + ` NOT NULL DEFAULT '{}',
		created_at ` + timeType + ` NOT NULL,
		FOREIGN KEY (action_run_id) REFERENCES action_runs(id)
	)`
}
