package inbound

import (
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/migration"
)

func RegisterMigrations(coordinator *migration.Coordinator) error {
	if coordinator == nil {
		return nil
	}
	if err := coordinator.RegisterInitializer(db.RoleCore, migration.Migration{
		Version:     "0179_gateway_inbound_events",
		Description: "create durable Gateway inbound event deduplication claims",
		Statements:  migrationStatements("INTEGER"),
		StatementsByDialect: map[db.Dialect][]string{
			db.DialectPostgres: migrationStatements("BIGINT"),
		},
	}); err != nil {
		return err
	}
	return coordinator.RegisterInitializer(db.RoleCore, migration.Migration{
		Version:     "0184_gateway_inbound_jobs",
		Description: "create durable Gateway provider callback queue",
		Statements:  queueMigrationStatements("BLOB", "INTEGER"),
		StatementsByDialect: map[db.Dialect][]string{
			db.DialectPostgres: queueMigrationStatements("BYTEA", "BIGINT"),
		},
	})
}

func migrationStatements(timeType string) []string {
	return []string{
		`CREATE TABLE IF NOT EXISTS gateway_inbound_events (
			channel_key TEXT NOT NULL,
			account_id TEXT NOT NULL,
			event_id TEXT NOT NULL,
			status TEXT NOT NULL,
			claim_token TEXT NOT NULL,
			lease_until ` + timeType + ` NOT NULL,
			created_at ` + timeType + ` NOT NULL,
			updated_at ` + timeType + ` NOT NULL,
			PRIMARY KEY (channel_key, account_id, event_id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_gateway_inbound_events_retention
		 ON gateway_inbound_events (status, updated_at)`,
	}
}

func queueMigrationStatements(payloadType, timeType string) []string {
	return []string{
		`CREATE TABLE IF NOT EXISTS gateway_inbound_jobs (
			channel_key TEXT NOT NULL,
			account_id TEXT NOT NULL,
			event_id TEXT NOT NULL,
			payload ` + payloadType + ` NOT NULL,
			status TEXT NOT NULL,
			claim_token TEXT NOT NULL DEFAULT '',
			lease_until ` + timeType + ` NOT NULL DEFAULT 0,
			attempts INTEGER NOT NULL DEFAULT 0,
			max_attempts INTEGER NOT NULL DEFAULT 12,
			next_attempt_at ` + timeType + ` NOT NULL DEFAULT 0,
			last_error TEXT NOT NULL DEFAULT '',
			created_at ` + timeType + ` NOT NULL,
			updated_at ` + timeType + ` NOT NULL,
			PRIMARY KEY (channel_key, account_id, event_id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_gateway_inbound_jobs_claim
		 ON gateway_inbound_jobs (channel_key, account_id, status, next_attempt_at, created_at)`,
		`CREATE INDEX IF NOT EXISTS idx_gateway_inbound_jobs_lease
		 ON gateway_inbound_jobs (channel_key, account_id, status, lease_until, created_at)`,
		`CREATE INDEX IF NOT EXISTS idx_gateway_inbound_jobs_retention
		 ON gateway_inbound_jobs (status, updated_at)`,
	}
}
