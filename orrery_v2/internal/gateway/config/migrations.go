package config

import (
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/migration"
)

func RegisterMigrations(coordinator *migration.Coordinator) error {
	if coordinator == nil {
		return nil
	}
	if err := coordinator.RegisterInitializer(db.RoleCore, gatewayConfigMigration()); err != nil {
		return err
	}
	if err := coordinator.RegisterInitializer(db.RoleCore, gatewayConfigTenantMigration()); err != nil {
		return err
	}
	return coordinator.RegisterInitializer(db.RoleCore, gatewayWeixinPersonalAssistantMigration())
}

func gatewayWeixinPersonalAssistantMigration() migration.Migration {
	return migration.Migration{
		Version:     "0198_gateway_weixin_personal_assistant",
		Description: "bind personal Weixin accounts to account-scoped personal assistants",
		Statements:  gatewayWeixinPersonalAssistantMigrationStatements("INTEGER"),
		StatementsByDialect: map[db.Dialect][]string{
			db.DialectPostgres: gatewayWeixinPersonalAssistantMigrationStatements("BIGINT"),
		},
	}
}

func gatewayWeixinPersonalAssistantMigrationStatements(timeType string) []string {
	return []string{
		`CREATE TABLE IF NOT EXISTS gateway_weixin_personal_assistant_bindings (
			tenant_id TEXT NOT NULL,
			weixin_user_id TEXT NOT NULL,
			owner_id TEXT NOT NULL,
			created_at ` + timeType + ` NOT NULL DEFAULT 0,
			updated_at ` + timeType + ` NOT NULL DEFAULT 0,
			PRIMARY KEY (tenant_id, weixin_user_id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_gateway_weixin_personal_assistant_owner ON gateway_weixin_personal_assistant_bindings (tenant_id, owner_id)`,
	}
}

func gatewayConfigMigration() migration.Migration {
	return migration.Migration{
		Version:     "0132_gateway_config",
		Description: "create structured gateway configuration tables",
		Statements:  gatewayConfigMigrationStatements("INTEGER"),
		StatementsByDialect: map[db.Dialect][]string{
			db.DialectPostgres: gatewayConfigMigrationStatements("BIGINT"),
		},
	}
}

func gatewayConfigMigrationStatements(timeType string) []string {
	return []string{
		`CREATE TABLE IF NOT EXISTS gateway_settings (
			id TEXT PRIMARY KEY,
			port INTEGER NOT NULL,
			bind_address TEXT NOT NULL,
			idle_session_timeout_minutes INTEGER NOT NULL,
			static_assets_path TEXT NOT NULL DEFAULT '',
			max_per_session_mcp_instances INTEGER NOT NULL,
			updated_at ` + timeType + ` NOT NULL DEFAULT 0
		)`,
		`CREATE TABLE IF NOT EXISTS gateway_adapters (
			adapter_key TEXT PRIMARY KEY,
			adapter_kind TEXT NOT NULL,
			enabled INTEGER NOT NULL DEFAULT 0,
			auto_connect_server INTEGER NOT NULL DEFAULT 0,
			token TEXT NOT NULL DEFAULT '',
			api_key TEXT NOT NULL DEFAULT '',
			webhook_url TEXT NOT NULL DEFAULT '',
			app_id TEXT NOT NULL DEFAULT '',
			app_secret TEXT NOT NULL DEFAULT '',
			encrypt_key TEXT NOT NULL DEFAULT '',
			verify_token TEXT NOT NULL DEFAULT '',
			default_session_label TEXT NOT NULL DEFAULT '',
			connection_mode TEXT NOT NULL DEFAULT '',
			domain_name TEXT NOT NULL DEFAULT '',
			updated_at ` + timeType + ` NOT NULL DEFAULT 0
		)`,
		`CREATE TABLE IF NOT EXISTS gateway_adapter_extra (
			adapter_key TEXT NOT NULL,
			extra_key TEXT NOT NULL,
			value_kind TEXT NOT NULL,
			string_value TEXT NOT NULL DEFAULT '',
			int_value INTEGER,
			real_value REAL,
			bool_value INTEGER,
			json_value TEXT NOT NULL DEFAULT '',
			PRIMARY KEY (adapter_key, extra_key)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_gateway_adapter_extra_adapter ON gateway_adapter_extra (adapter_key)`,
		`CREATE TABLE IF NOT EXISTS gateway_weixin_accounts (
			weixin_user_id TEXT PRIMARY KEY,
			bot_id TEXT NOT NULL DEFAULT '',
			token TEXT NOT NULL DEFAULT '',
			ilink_base_url TEXT NOT NULL DEFAULT '',
			state_path TEXT NOT NULL DEFAULT '',
			x_wechat_uin TEXT NOT NULL DEFAULT '',
			created_at ` + timeType + ` NOT NULL DEFAULT 0,
			updated_at ` + timeType + ` NOT NULL DEFAULT 0
		)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_gateway_weixin_accounts_bot_id ON gateway_weixin_accounts (bot_id) WHERE bot_id <> ''`,
		`CREATE TABLE IF NOT EXISTS gateway_weixin_user_bindings (
			weixin_user_id TEXT PRIMARY KEY,
			human_profile_id TEXT NOT NULL,
			created_at ` + timeType + ` NOT NULL DEFAULT 0,
			updated_at ` + timeType + ` NOT NULL DEFAULT 0
		)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_gateway_weixin_user_bindings_human ON gateway_weixin_user_bindings (human_profile_id)`,
		`CREATE TABLE IF NOT EXISTS gateway_weixin_agent_bindings (
			weixin_user_id TEXT PRIMARY KEY,
			default_agent_id TEXT NOT NULL,
			active_agent_id TEXT NOT NULL DEFAULT '',
			created_at ` + timeType + ` NOT NULL DEFAULT 0,
			updated_at ` + timeType + ` NOT NULL DEFAULT 0
		)`,
		`CREATE INDEX IF NOT EXISTS idx_gateway_weixin_agent_bindings_default_agent ON gateway_weixin_agent_bindings (default_agent_id)`,
		`CREATE INDEX IF NOT EXISTS idx_gateway_weixin_agent_bindings_active_agent ON gateway_weixin_agent_bindings (active_agent_id)`,
	}
}

func gatewayConfigTenantMigration() migration.Migration {
	return migration.Migration{
		Version:     "0133_gateway_weixin_tenants",
		Description: "scope Weixin Gateway accounts and bindings by tenant",
		Statements:  gatewayConfigTenantMigrationStatements("INTEGER"),
		StatementsByDialect: map[db.Dialect][]string{
			db.DialectPostgres: gatewayConfigTenantMigrationStatements("BIGINT"),
		},
	}
}

func gatewayConfigTenantMigrationStatements(timeType string) []string {
	return []string{
		`ALTER TABLE gateway_weixin_accounts RENAME TO gateway_weixin_accounts_legacy`,
		`CREATE TABLE gateway_weixin_accounts (
			tenant_id TEXT NOT NULL,
			weixin_user_id TEXT NOT NULL,
			bot_id TEXT NOT NULL DEFAULT '',
			token TEXT NOT NULL DEFAULT '',
			ilink_base_url TEXT NOT NULL DEFAULT '',
			state_path TEXT NOT NULL DEFAULT '',
			x_wechat_uin TEXT NOT NULL DEFAULT '',
			created_at ` + timeType + ` NOT NULL DEFAULT 0,
			updated_at ` + timeType + ` NOT NULL DEFAULT 0,
			PRIMARY KEY (tenant_id, weixin_user_id)
		)`,
		`INSERT INTO gateway_weixin_accounts (tenant_id, weixin_user_id, bot_id, token, ilink_base_url, state_path, x_wechat_uin, created_at, updated_at)
		 SELECT 'system', weixin_user_id, bot_id, token, ilink_base_url, state_path, x_wechat_uin, created_at, updated_at FROM gateway_weixin_accounts_legacy`,
		`DROP TABLE gateway_weixin_accounts_legacy`,
		`CREATE UNIQUE INDEX idx_gateway_weixin_accounts_bot_id ON gateway_weixin_accounts (bot_id) WHERE bot_id <> ''`,
		`ALTER TABLE gateway_weixin_user_bindings RENAME TO gateway_weixin_user_bindings_legacy`,
		`CREATE TABLE gateway_weixin_user_bindings (
			tenant_id TEXT NOT NULL,
			weixin_user_id TEXT NOT NULL,
			human_profile_id TEXT NOT NULL,
			created_at ` + timeType + ` NOT NULL DEFAULT 0,
			updated_at ` + timeType + ` NOT NULL DEFAULT 0,
			PRIMARY KEY (tenant_id, weixin_user_id)
		)`,
		`INSERT INTO gateway_weixin_user_bindings (tenant_id, weixin_user_id, human_profile_id, created_at, updated_at)
		 SELECT 'system', weixin_user_id, human_profile_id, created_at, updated_at FROM gateway_weixin_user_bindings_legacy`,
		`DROP TABLE gateway_weixin_user_bindings_legacy`,
		`CREATE UNIQUE INDEX idx_gateway_weixin_user_bindings_human ON gateway_weixin_user_bindings (tenant_id, human_profile_id)`,
		`ALTER TABLE gateway_weixin_agent_bindings RENAME TO gateway_weixin_agent_bindings_legacy`,
		`CREATE TABLE gateway_weixin_agent_bindings (
			tenant_id TEXT NOT NULL,
			weixin_user_id TEXT NOT NULL,
			default_agent_id TEXT NOT NULL,
			active_agent_id TEXT NOT NULL DEFAULT '',
			created_at ` + timeType + ` NOT NULL DEFAULT 0,
			updated_at ` + timeType + ` NOT NULL DEFAULT 0,
			PRIMARY KEY (tenant_id, weixin_user_id)
		)`,
		`INSERT INTO gateway_weixin_agent_bindings (tenant_id, weixin_user_id, default_agent_id, active_agent_id, created_at, updated_at)
		 SELECT 'system', weixin_user_id, default_agent_id, active_agent_id, created_at, updated_at FROM gateway_weixin_agent_bindings_legacy`,
		`DROP TABLE gateway_weixin_agent_bindings_legacy`,
		`CREATE INDEX idx_gateway_weixin_agent_bindings_default_agent ON gateway_weixin_agent_bindings (tenant_id, default_agent_id)`,
		`CREATE INDEX idx_gateway_weixin_agent_bindings_active_agent ON gateway_weixin_agent_bindings (tenant_id, active_agent_id)`,
	}
}
