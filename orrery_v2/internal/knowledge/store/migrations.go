package store

import (
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/migration"
)

func RegisterMigrations(coordinator *migration.Coordinator) error {
	if coordinator == nil {
		return nil
	}
	if err := coordinator.RegisterInitializer(db.RoleCore, migration.Migration{
		Version:     "0100_knowledge_metadata",
		Description: "create knowledge metadata tables",
		Statements: []string{
			`CREATE TABLE IF NOT EXISTS knowledge_bases (id TEXT PRIMARY KEY, payload TEXT NOT NULL)`,
			`CREATE TABLE IF NOT EXISTS knowledge_bindings (id TEXT PRIMARY KEY, payload TEXT NOT NULL)`,
			`CREATE TABLE IF NOT EXISTS knowledge_documents (id TEXT PRIMARY KEY, payload TEXT NOT NULL)`,
			`CREATE TABLE IF NOT EXISTS knowledge_chunks (id TEXT PRIMARY KEY, payload TEXT NOT NULL)`,
			`CREATE TABLE IF NOT EXISTS knowledge_ingest_jobs (id TEXT PRIMARY KEY, payload TEXT NOT NULL)`,
		},
	}); err != nil {
		return err
	}
	return coordinator.RegisterInitializer(db.RoleCore, migration.Migration{
		Version:     "0101_knowledge_event_revisions",
		Description: "create knowledge event revision counters",
		Statements: []string{
			`CREATE TABLE IF NOT EXISTS knowledge_event_revisions (
				stream_id TEXT PRIMARY KEY,
				last_revision INTEGER NOT NULL
			)`,
		},
	})
}
