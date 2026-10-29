package agenttrace

import (
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/migration"
)

func RegisterMigrations(coordinator *migration.Coordinator) error {
	if coordinator == nil {
		return nil
	}
	return coordinator.RegisterInitializer(db.RoleCore, agentRuntimeTracesMigration())
}

func agentRuntimeTracesMigration() migration.Migration {
	indexes := []string{
		`CREATE INDEX IF NOT EXISTS idx_agent_runtime_traces_retention ON agent_runtime_traces (status, retain_until, tenant_id, run_id)`,
		`CREATE INDEX IF NOT EXISTS idx_agent_runtime_traces_archive_purge ON agent_runtime_traces (status, purge_after, tenant_id, run_id)`,
		`CREATE INDEX IF NOT EXISTS idx_agent_runtime_traces_running ON agent_runtime_traces (status, latest_event_at, tenant_id, run_id)`,
		`CREATE INDEX IF NOT EXISTS idx_agent_runtime_traces_terminal ON agent_runtime_traces (tenant_id, terminal_status, completed_at)`,
		`CREATE INDEX IF NOT EXISTS idx_agent_runtime_traces_legal_hold ON agent_runtime_traces (legal_hold_at, tenant_id, run_id)`,
	}
	sqlite := append([]string{agentRuntimeTracesTableSQL("INTEGER")}, indexes...)
	postgres := append([]string{agentRuntimeTracesTableSQL("BIGINT")}, indexes...)
	return migration.Migration{
		Version:             "0203_agent_runtime_traces",
		Description:         "create durable Agent runtime trace index",
		Statements:          sqlite,
		StatementsByDialect: map[db.Dialect][]string{db.DialectPostgres: postgres},
	}
}

func agentRuntimeTracesTableSQL(timeType string) string {
	return `CREATE TABLE IF NOT EXISTS agent_runtime_traces (
		tenant_id TEXT NOT NULL,
		run_id TEXT NOT NULL,
		trace_version INTEGER NOT NULL,
		trace_ref TEXT NOT NULL,
		status TEXT NOT NULL,
		terminal_status TEXT NOT NULL DEFAULT '',
		event_count INTEGER NOT NULL DEFAULT 0,
		byte_count BIGINT NOT NULL DEFAULT 0,
		manifest_sha256 TEXT NOT NULL DEFAULT '',
		archive_ref TEXT NOT NULL DEFAULT '',
		archive_sha256 TEXT NOT NULL DEFAULT '',
		started_at ` + timeType + ` NOT NULL,
		latest_event_at ` + timeType + ` NOT NULL DEFAULT 0,
		completed_at ` + timeType + ` NOT NULL DEFAULT 0,
		retain_until ` + timeType + ` NOT NULL DEFAULT 0,
		archived_at ` + timeType + ` NOT NULL DEFAULT 0,
		hot_deleted_at ` + timeType + ` NOT NULL DEFAULT 0,
		purge_after ` + timeType + ` NOT NULL DEFAULT 0,
		purged_at ` + timeType + ` NOT NULL DEFAULT 0,
		legal_hold_actor TEXT NOT NULL DEFAULT '',
		legal_hold_reason TEXT NOT NULL DEFAULT '',
		legal_hold_at ` + timeType + ` NOT NULL DEFAULT 0,
		revision BIGINT NOT NULL DEFAULT 1,
		updated_at ` + timeType + ` NOT NULL,
		PRIMARY KEY (tenant_id, run_id, trace_version)
	)`
}
