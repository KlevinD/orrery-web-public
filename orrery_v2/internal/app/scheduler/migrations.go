package scheduler

import (
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/migration"
)

func RegisterMigrations(coordinator *migration.Coordinator) error {
	if coordinator == nil {
		return nil
	}
	return coordinator.RegisterInitializer(db.RoleCore, maintenanceIndexMigration())
}

func maintenanceIndexMigration() migration.Migration {
	return migration.Migration{
		Version:     "0212_scheduler_run_maintenance_indexes",
		Description: "index task and automation retention candidate lookups",
		Statements: []string{
			`CREATE INDEX IF NOT EXISTS idx_scheduler_run_task_status ON platform_records (table_name, json_extract(payload_json, '$.scheduled_task_id'), json_extract(payload_json, '$.status'), record_key) WHERE table_name = 'scheduled_task_runs'`,
			`CREATE INDEX IF NOT EXISTS idx_scheduler_run_mode_status ON platform_records (table_name, json_extract(payload_json, '$.target_mode'), json_extract(payload_json, '$.status'), record_key) WHERE table_name = 'scheduled_task_runs'`,
		},
		StatementsByDialect: map[db.Dialect][]string{
			db.DialectPostgres: {
				`CREATE INDEX IF NOT EXISTS idx_scheduler_run_task_status ON platform_records (table_name, (payload_json::jsonb ->> 'scheduled_task_id'), (payload_json::jsonb ->> 'status'), record_key) WHERE table_name = 'scheduled_task_runs'`,
				`CREATE INDEX IF NOT EXISTS idx_scheduler_run_mode_status ON platform_records (table_name, (payload_json::jsonb ->> 'target_mode'), (payload_json::jsonb ->> 'status'), record_key) WHERE table_name = 'scheduled_task_runs'`,
			},
		},
	}
}
