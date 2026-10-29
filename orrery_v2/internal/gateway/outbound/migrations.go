package outbound

import (
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/migration"
)

func RegisterMigrations(coordinator *migration.Coordinator) error {
	if coordinator == nil {
		return nil
	}
	return coordinator.RegisterInitializer(db.RoleJobs, migration.Migration{
		Version:     "0178_gateway_message_deliveries",
		Description: "create durable Gateway outbound delivery records",
		Statements:  migrationStatements("INTEGER"),
		StatementsByDialect: map[db.Dialect][]string{
			db.DialectPostgres: migrationStatements("BIGINT"),
		},
	})
}

func migrationStatements(timeType string) []string {
	return []string{
		`CREATE TABLE IF NOT EXISTS gateway_message_delivery_requests (
			tenant_id TEXT NOT NULL,
			human_profile_id TEXT NOT NULL,
			idempotency_key TEXT NOT NULL,
			command_fingerprint TEXT NOT NULL,
			created_at ` + timeType + ` NOT NULL,
			PRIMARY KEY (tenant_id, human_profile_id, idempotency_key)
		)`,
		`CREATE TABLE IF NOT EXISTS gateway_message_deliveries (
			id TEXT PRIMARY KEY,
			tenant_id TEXT NOT NULL,
			channel_key TEXT NOT NULL,
			account_id TEXT NOT NULL,
			binding_id TEXT NOT NULL,
			human_profile_id TEXT NOT NULL,
			external_tenant_id TEXT NOT NULL DEFAULT '',
			recipient_id TEXT NOT NULL,
			event_type TEXT NOT NULL DEFAULT 'manual',
			title TEXT NOT NULL DEFAULT '',
			body TEXT NOT NULL,
			message_format TEXT NOT NULL DEFAULT 'text',
			idempotency_key TEXT NOT NULL,
			status TEXT NOT NULL DEFAULT 'pending',
			attempts INTEGER NOT NULL DEFAULT 0,
			external_message_id TEXT NOT NULL DEFAULT '',
			last_error TEXT NOT NULL DEFAULT '',
			created_at ` + timeType + ` NOT NULL,
			updated_at ` + timeType + ` NOT NULL,
			delivered_at ` + timeType + ` NOT NULL DEFAULT 0,
			UNIQUE (tenant_id, channel_key, binding_id, idempotency_key)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_gateway_deliveries_status
		 ON gateway_message_deliveries (status, created_at)`,
		`CREATE INDEX IF NOT EXISTS idx_gateway_deliveries_tenant
		 ON gateway_message_deliveries (tenant_id, created_at)`,
	}
}
