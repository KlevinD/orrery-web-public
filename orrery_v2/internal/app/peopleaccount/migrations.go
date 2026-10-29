package peopleaccount

import (
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/migration"
)

const (
	projectionStateTable  = "people_account_projection_state"
	projectionOutboxTable = "people_account_projection_outbox"
)

func RegisterMigrations(coordinator *migration.Coordinator) error {
	if coordinator == nil {
		return nil
	}
	return coordinator.RegisterInitializer(db.RoleCore, projectionMigration())
}

func projectionMigration() migration.Migration {
	return migration.Migration{
		Version:     "0188_people_account_projection",
		Description: "create People account projection state and transactional outbox",
		Statements: []string{
			`CREATE TABLE IF NOT EXISTS people_account_projection_state (
				person_id TEXT PRIMARY KEY,
				revision BIGINT NOT NULL,
				payload_json TEXT NOT NULL,
				status TEXT NOT NULL DEFAULT 'pending',
				applied_revision BIGINT NOT NULL DEFAULT 0,
				kratos_identity_id TEXT NOT NULL DEFAULT '',
				last_error TEXT NOT NULL DEFAULT '',
				updated_at BIGINT NOT NULL,
				UNIQUE (person_id, revision)
			)`,
			`CREATE TABLE IF NOT EXISTS people_account_projection_outbox (
				id TEXT PRIMARY KEY,
				person_id TEXT NOT NULL,
				revision BIGINT NOT NULL,
				payload_json TEXT NOT NULL,
				status TEXT NOT NULL DEFAULT 'pending',
				attempts INTEGER NOT NULL DEFAULT 0,
				last_error TEXT NOT NULL DEFAULT '',
				created_at BIGINT NOT NULL,
				updated_at BIGINT NOT NULL,
				UNIQUE (person_id, revision)
			)`,
			`CREATE INDEX IF NOT EXISTS idx_people_account_projection_outbox_pending ON people_account_projection_outbox (status, updated_at, person_id, revision)`,
		},
	}
}
