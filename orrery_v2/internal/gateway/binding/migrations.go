package binding

import (
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/migration"
)

func RegisterMigrations(coordinator *migration.Coordinator) error {
	if coordinator == nil {
		return nil
	}
	if err := coordinator.RegisterInitializer(db.RoleCore, migration.Migration{
		Version:     "0177_gateway_channel_bindings",
		Description: "create tenant-scoped Gateway channel accounts and personal bindings",
		Statements:  migrationStatements("INTEGER"),
		StatementsByDialect: map[db.Dialect][]string{
			db.DialectPostgres: migrationStatements("BIGINT"),
		},
	}); err != nil {
		return err
	}
	if err := coordinator.RegisterInitializer(db.RoleCore, migration.Migration{
		Version:     "0180_gateway_binding_code_channel_scope",
		Description: "limit each user to one active Gateway binding code per channel",
		Statements:  channelScopedBindingCodeStatements(),
	}); err != nil {
		return err
	}
	if err := coordinator.RegisterInitializer(db.RoleCore, migration.Migration{
		Version:     "0181_gateway_binding_code_replay_receipt",
		Description: "record the binding created by a consumed Gateway binding code",
		Statements: []string{
			`ALTER TABLE gateway_channel_binding_codes ADD COLUMN consumed_binding_id TEXT NOT NULL DEFAULT ''`,
		},
	}); err != nil {
		return err
	}
	if err := coordinator.RegisterInitializer(db.RoleCore, migration.Migration{
		Version:     "0193_gateway_binding_account_scope",
		Description: "allow one personal Feishu binding per named account",
		Statements:  accountScopedBindingStatements(),
		StatementsByDialect: map[db.Dialect][]string{
			db.DialectPostgres: postgresAccountScopedBindingStatements(),
		},
	}); err != nil {
		return err
	}
	if err := coordinator.RegisterInitializer(db.RoleCore, migration.Migration{
		Version:     "0194_feishu_agent_channel_bindings",
		Description: "assign named Feishu accounts to Orrery AI employees",
		Statements:  agentChannelBindingSchemaStatements("INTEGER"),
		StatementsByDialect: map[db.Dialect][]string{
			db.DialectPostgres: agentChannelBindingSchemaStatements("BIGINT"),
		},
	}); err != nil {
		return err
	}
	return coordinator.RegisterInitializer(db.RoleCore, migration.Migration{
		Version:     "0200_gateway_binding_account_scope_constraint_repair",
		Description: "remove the truncated legacy one-binding-per-channel constraint",
		StatementsByDialect: map[db.Dialect][]string{
			db.DialectPostgres: postgresAccountScopedBindingConstraintRepairStatements(),
		},
	})
}

func migrationStatements(timeType string) []string {
	statements := bindingSchemaStatements(timeType, false, false)
	index := len(statements) - 1
	statements = append(statements, "")
	copy(statements[index+1:], statements[index:])
	statements[index] = deduplicateActiveBindingCodesSQL
	return statements
}

const deduplicateActiveBindingCodesSQL = `UPDATE gateway_channel_binding_codes SET consumed_at = created_at
		 WHERE consumed_at = 0 AND EXISTS (
			SELECT 1 FROM gateway_channel_binding_codes AS newer
			WHERE newer.consumed_at = 0
			  AND newer.tenant_id = gateway_channel_binding_codes.tenant_id
			  AND newer.human_profile_id = gateway_channel_binding_codes.human_profile_id
			  AND newer.channel_key = gateway_channel_binding_codes.channel_key
			  AND (newer.created_at > gateway_channel_binding_codes.created_at OR
			      (newer.created_at = gateway_channel_binding_codes.created_at AND newer.code_hash > gateway_channel_binding_codes.code_hash))
		 )`

func channelScopedBindingCodeStatements() []string {
	return []string{
		deduplicateActiveBindingCodesSQL,
		`DROP INDEX IF EXISTS idx_gateway_channel_binding_codes_active`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_gateway_channel_binding_codes_active
		 ON gateway_channel_binding_codes (tenant_id, human_profile_id, channel_key)
		 WHERE consumed_at = 0`,
	}
}

func schemaStatements(timeType string) []string {
	return append(bindingSchemaStatements(timeType, true, true), agentChannelBindingSchemaStatements(timeType)...)
}

func bindingSchemaStatements(timeType string, includeReplayReceipt, accountScoped bool) []string {
	replayReceiptColumn := ""
	if includeReplayReceipt {
		replayReceiptColumn = "\n\t\t\tconsumed_binding_id TEXT NOT NULL DEFAULT '',"
	}
	humanUniqueConstraint := ",\n\t\t\tUNIQUE (tenant_id, channel_key, human_profile_id)"
	if accountScoped {
		humanUniqueConstraint = ""
	}
	statements := []string{
		`CREATE TABLE IF NOT EXISTS gateway_channel_accounts (
			tenant_id TEXT NOT NULL,
			channel_key TEXT NOT NULL,
			account_id TEXT NOT NULL,
			external_tenant_id TEXT NOT NULL DEFAULT '',
			display_name TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT 'connected',
			created_at ` + timeType + ` NOT NULL DEFAULT 0,
			updated_at ` + timeType + ` NOT NULL DEFAULT 0,
			PRIMARY KEY (tenant_id, channel_key, account_id)
		)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_gateway_channel_accounts_external
		 ON gateway_channel_accounts (channel_key, account_id, external_tenant_id)
		 WHERE external_tenant_id <> ''`,
		`CREATE TABLE IF NOT EXISTS gateway_channel_bindings (
			id TEXT PRIMARY KEY,
			tenant_id TEXT NOT NULL,
			channel_key TEXT NOT NULL,
			account_id TEXT NOT NULL,
			external_tenant_id TEXT NOT NULL DEFAULT '',
			external_user_id TEXT NOT NULL,
			human_profile_id TEXT NOT NULL,
			display_name TEXT NOT NULL DEFAULT '',
			default_agent_id TEXT NOT NULL DEFAULT '',
			active_agent_id TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT 'active',
			verified_at ` + timeType + ` NOT NULL DEFAULT 0,
			created_at ` + timeType + ` NOT NULL DEFAULT 0,
			updated_at ` + timeType + ` NOT NULL DEFAULT 0,
			UNIQUE (channel_key, account_id, external_tenant_id, external_user_id)` + humanUniqueConstraint + `
		)`,
		`CREATE INDEX IF NOT EXISTS idx_gateway_channel_bindings_external
		 ON gateway_channel_bindings (channel_key, account_id, external_tenant_id, external_user_id, status)`,
		`CREATE INDEX IF NOT EXISTS idx_gateway_channel_bindings_human
		 ON gateway_channel_bindings (tenant_id, human_profile_id, status)`,
		`CREATE TABLE IF NOT EXISTS gateway_channel_binding_codes (
			code_hash TEXT PRIMARY KEY,
			tenant_id TEXT NOT NULL,
			channel_key TEXT NOT NULL,
			account_id TEXT NOT NULL,
			human_profile_id TEXT NOT NULL,
			default_agent_id TEXT NOT NULL DEFAULT '',
			expires_at ` + timeType + ` NOT NULL,
			consumed_at ` + timeType + ` NOT NULL DEFAULT 0,` + replayReceiptColumn + `
			created_at ` + timeType + ` NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_gateway_channel_binding_codes_owner
		 ON gateway_channel_binding_codes (tenant_id, human_profile_id, channel_key, expires_at)`,
	}
	if !accountScoped {
		return append(statements, `CREATE UNIQUE INDEX IF NOT EXISTS idx_gateway_channel_binding_codes_active
		 ON gateway_channel_binding_codes (tenant_id, human_profile_id, channel_key)
		 WHERE consumed_at = 0`)
	}
	return append(statements,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_gateway_channel_binding_codes_active_feishu
		 ON gateway_channel_binding_codes (tenant_id, human_profile_id, channel_key, account_id)
		 WHERE consumed_at = 0 AND channel_key = 'feishu'`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_gateway_channel_binding_codes_active
		 ON gateway_channel_binding_codes (tenant_id, human_profile_id, channel_key)
		 WHERE consumed_at = 0 AND channel_key <> 'feishu'`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_gateway_channel_bindings_human_feishu
		 ON gateway_channel_bindings (tenant_id, channel_key, account_id, human_profile_id)
		 WHERE channel_key = 'feishu'`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_gateway_channel_bindings_human_channel
		 ON gateway_channel_bindings (tenant_id, channel_key, human_profile_id)
		 WHERE channel_key <> 'feishu'`,
	)
}

func accountScopedBindingStatements() []string {
	return []string{
		`ALTER TABLE gateway_channel_bindings RENAME TO gateway_channel_bindings_channel_scoped`,
		`CREATE TABLE gateway_channel_bindings (
			id TEXT PRIMARY KEY,
			tenant_id TEXT NOT NULL,
			channel_key TEXT NOT NULL,
			account_id TEXT NOT NULL,
			external_tenant_id TEXT NOT NULL DEFAULT '',
			external_user_id TEXT NOT NULL,
			human_profile_id TEXT NOT NULL,
			display_name TEXT NOT NULL DEFAULT '',
			default_agent_id TEXT NOT NULL DEFAULT '',
			active_agent_id TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT 'active',
			verified_at INTEGER NOT NULL DEFAULT 0,
			created_at INTEGER NOT NULL DEFAULT 0,
			updated_at INTEGER NOT NULL DEFAULT 0,
			UNIQUE (channel_key, account_id, external_tenant_id, external_user_id)
		)`,
		`INSERT INTO gateway_channel_bindings (
			id, tenant_id, channel_key, account_id, external_tenant_id, external_user_id, human_profile_id,
			display_name, default_agent_id, active_agent_id, status, verified_at, created_at, updated_at
		) SELECT id, tenant_id, channel_key, account_id, external_tenant_id, external_user_id, human_profile_id,
			display_name, default_agent_id, active_agent_id, status, verified_at, created_at, updated_at
		  FROM gateway_channel_bindings_channel_scoped`,
		`DROP TABLE gateway_channel_bindings_channel_scoped`,
		`CREATE INDEX idx_gateway_channel_bindings_external
		 ON gateway_channel_bindings (channel_key, account_id, external_tenant_id, external_user_id, status)`,
		`CREATE INDEX idx_gateway_channel_bindings_human
		 ON gateway_channel_bindings (tenant_id, human_profile_id, status)`,
		`CREATE UNIQUE INDEX idx_gateway_channel_bindings_human_feishu
		 ON gateway_channel_bindings (tenant_id, channel_key, account_id, human_profile_id)
		 WHERE channel_key = 'feishu'`,
		`CREATE UNIQUE INDEX idx_gateway_channel_bindings_human_channel
		 ON gateway_channel_bindings (tenant_id, channel_key, human_profile_id)
		 WHERE channel_key <> 'feishu'`,
		`DROP INDEX IF EXISTS idx_gateway_channel_binding_codes_active`,
		`DROP INDEX IF EXISTS idx_gateway_channel_binding_codes_active_feishu`,
		`CREATE UNIQUE INDEX idx_gateway_channel_binding_codes_active_feishu
		 ON gateway_channel_binding_codes (tenant_id, human_profile_id, channel_key, account_id)
		 WHERE consumed_at = 0 AND channel_key = 'feishu'`,
		`CREATE UNIQUE INDEX idx_gateway_channel_binding_codes_active
		 ON gateway_channel_binding_codes (tenant_id, human_profile_id, channel_key)
		 WHERE consumed_at = 0 AND channel_key <> 'feishu'`,
	}
}

func postgresAccountScopedBindingStatements() []string {
	return []string{
		`ALTER TABLE gateway_channel_bindings
		 DROP CONSTRAINT IF EXISTS gateway_channel_bindings_tenant_id_channel_key_human_profile_id_key`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_gateway_channel_bindings_human_feishu
		 ON gateway_channel_bindings (tenant_id, channel_key, account_id, human_profile_id)
		 WHERE channel_key = 'feishu'`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_gateway_channel_bindings_human_channel
		 ON gateway_channel_bindings (tenant_id, channel_key, human_profile_id)
		 WHERE channel_key <> 'feishu'`,
		`DROP INDEX IF EXISTS idx_gateway_channel_binding_codes_active`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_gateway_channel_binding_codes_active_feishu
		 ON gateway_channel_binding_codes (tenant_id, human_profile_id, channel_key, account_id)
		 WHERE consumed_at = 0 AND channel_key = 'feishu'`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_gateway_channel_binding_codes_active
		 ON gateway_channel_binding_codes (tenant_id, human_profile_id, channel_key)
		 WHERE consumed_at = 0 AND channel_key <> 'feishu'`,
	}
}

func postgresAccountScopedBindingConstraintRepairStatements() []string {
	return []string{
		`ALTER TABLE gateway_channel_bindings
		 DROP CONSTRAINT IF EXISTS gateway_channel_bindings_tenant_id_channel_key_human_profil_key`,
		`ALTER TABLE gateway_channel_bindings
		 DROP CONSTRAINT IF EXISTS gateway_channel_bindings_tenant_id_channel_key_human_profile_id_key`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_gateway_channel_bindings_human_feishu
		 ON gateway_channel_bindings (tenant_id, channel_key, account_id, human_profile_id)
		 WHERE channel_key = 'feishu'`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_gateway_channel_bindings_human_channel
		 ON gateway_channel_bindings (tenant_id, channel_key, human_profile_id)
		 WHERE channel_key <> 'feishu'`,
	}
}

func agentChannelBindingSchemaStatements(timeType string) []string {
	return []string{
		`CREATE TABLE IF NOT EXISTS gateway_agent_channel_bindings (
			tenant_id TEXT NOT NULL,
			channel_key TEXT NOT NULL CHECK (channel_key = 'feishu'),
			account_id TEXT NOT NULL,
			agent_profile_id TEXT NOT NULL,
			inbound_enabled INTEGER NOT NULL DEFAULT 0,
			revision INTEGER NOT NULL DEFAULT 1,
			created_at ` + timeType + ` NOT NULL DEFAULT 0,
			updated_at ` + timeType + ` NOT NULL DEFAULT 0,
			PRIMARY KEY (tenant_id, channel_key, account_id),
			UNIQUE (channel_key, account_id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_gateway_agent_channel_bindings_agent
		 ON gateway_agent_channel_bindings (tenant_id, agent_profile_id, channel_key, account_id)`,
	}
}
