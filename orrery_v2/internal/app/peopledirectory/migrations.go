package peopledirectory

import (
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/migration"
)

const (
	directorySnapshotTable   = "people_directory_snapshots"
	directoryStagedUserTable = "people_directory_staged_users"
	directoryMatchTable      = "people_directory_matches"
)

func RegisterMigrations(coordinator *migration.Coordinator) error {
	if coordinator == nil {
		return nil
	}
	return coordinator.RegisterInitializer(db.RoleCore, directoryMigration())
}

func directoryMigration() migration.Migration {
	return migration.Migration{
		Version:     "0192_people_directory_snapshots",
		Description: "stage complete People directory snapshots and reviewable matches",
		Statements: []string{
			`CREATE TABLE IF NOT EXISTS people_directory_snapshots (
				id TEXT PRIMARY KEY,
				source TEXT NOT NULL,
				tenant_id TEXT NOT NULL,
				status TEXT NOT NULL,
				complete INTEGER NOT NULL DEFAULT 0,
				page_count INTEGER NOT NULL DEFAULT 0,
				last_error TEXT NOT NULL DEFAULT '',
				created_at BIGINT NOT NULL,
				updated_at BIGINT NOT NULL
			)`,
			`CREATE TABLE IF NOT EXISTS people_directory_staged_users (
				snapshot_id TEXT NOT NULL,
				source_record_id TEXT NOT NULL,
				employee_number TEXT NOT NULL DEFAULT '',
				name TEXT NOT NULL DEFAULT '',
				email TEXT NOT NULL DEFAULT '',
				enterprise_email TEXT NOT NULL DEFAULT '',
				active INTEGER NOT NULL DEFAULT 0,
				created_at BIGINT NOT NULL,
				updated_at BIGINT NOT NULL,
				PRIMARY KEY (snapshot_id, source_record_id)
			)`,
			`CREATE INDEX IF NOT EXISTS idx_people_directory_staged_users_employee ON people_directory_staged_users (snapshot_id, employee_number)`,
			`CREATE INDEX IF NOT EXISTS idx_people_directory_staged_users_email ON people_directory_staged_users (snapshot_id, enterprise_email)`,
			`CREATE TABLE IF NOT EXISTS people_directory_matches (
				snapshot_id TEXT NOT NULL,
				source_record_id TEXT NOT NULL,
				action TEXT NOT NULL,
				match_kind TEXT NOT NULL,
				proposed_person_id TEXT NOT NULL DEFAULT '',
				conflict_reason TEXT NOT NULL DEFAULT '',
				apply_status TEXT NOT NULL,
				created_at BIGINT NOT NULL,
				updated_at BIGINT NOT NULL,
				PRIMARY KEY (snapshot_id, source_record_id)
			)`,
			`CREATE INDEX IF NOT EXISTS idx_people_directory_matches_review ON people_directory_matches (snapshot_id, apply_status, action)`,
		},
	}
}
