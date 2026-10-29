package peopleingest

import (
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/migration"
)

func RegisterMigrations(coordinator *migration.Coordinator) error {
	if coordinator == nil {
		return nil
	}
	return coordinator.RegisterInitializer(db.RoleCore, ingestionMigration())
}

func ingestionMigration() migration.Migration {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS people_ingestion_batches (
			tenant_id TEXT NOT NULL,
			stream TEXT NOT NULL,
			batch_id TEXT NOT NULL,
			request_sha256 TEXT NOT NULL,
			first_cursor TEXT NOT NULL,
			last_cursor TEXT NOT NULL,
			event_count BIGINT NOT NULL,
			recorded_count BIGINT NOT NULL DEFAULT 0,
			projected_count BIGINT NOT NULL DEFAULT 0,
			status TEXT NOT NULL DEFAULT 'pending',
			lease_token TEXT NOT NULL DEFAULT '',
			lease_expires_at BIGINT NOT NULL DEFAULT 0,
			last_error TEXT NOT NULL DEFAULT '',
			created_at BIGINT NOT NULL,
			updated_at BIGINT NOT NULL,
			completed_at BIGINT NOT NULL DEFAULT 0,
			PRIMARY KEY (tenant_id, stream, batch_id)
		)`,
		`CREATE TABLE IF NOT EXISTS people_ingestion_events (
			tenant_id TEXT NOT NULL,
			event_id TEXT NOT NULL,
			stream TEXT NOT NULL,
			batch_id TEXT NOT NULL,
			event_index BIGINT NOT NULL,
			event_sha256 TEXT NOT NULL,
			event_json TEXT NOT NULL,
			recorded_at BIGINT NOT NULL,
			projected_at BIGINT NOT NULL DEFAULT 0,
			PRIMARY KEY (tenant_id, event_id),
			UNIQUE (tenant_id, stream, batch_id, event_index)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_people_ingestion_batches_status ON people_ingestion_batches (status, lease_expires_at, updated_at)`,
		`CREATE INDEX IF NOT EXISTS idx_people_ingestion_events_batch ON people_ingestion_events (tenant_id, stream, batch_id, event_index)`,
	}
	return migration.Migration{
		Version:     "0196_people_ingestion_ledger",
		Description: "create durable People delivery batch and source event ledger",
		Statements:  statements,
		StatementsByDialect: map[db.Dialect][]string{
			db.DialectPostgres: append([]string(nil), statements...),
		},
	}
}
