package contextgraph

import "orrery_v2/internal/platform/migration"

func contextGraphStoreTestMigrations() []migration.Migration {
	return []migration.Migration{contextGraphMigration(), contextGraphAccessRefMigration(), contextGraphTemporalHistoryMigration()}
}

func contextGraphAuditStoreTestMigrations() []migration.Migration {
	return []migration.Migration{contextGraphProjectionAuditMigration(), contextGraphAuditAccessRefMigration(), contextGraphProjectionCommandMigration(), contextGraphProjectionCommandLeaseMigration()}
}

func contextGraphCompleteStoreTestMigrations() []migration.Migration {
	return []migration.Migration{
		contextGraphMigration(),
		contextGraphProjectionAuditMigration(),
		contextGraphAccessRefMigration(),
		contextGraphAuditAccessRefMigration(),
		contextGraphAccessRefBackfillAuditMigration(),
		contextGraphProjectionCommandMigration(),
		contextGraphProjectionCommandLeaseMigration(),
		contextGraphTemporalHistoryMigration(),
		contextGraphAccessRefBoundsMigration(),
	}
}
