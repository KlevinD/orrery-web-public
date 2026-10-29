package feishu

import (
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/migration"
)

func RegisterMeetingMigrations(coordinator *migration.Coordinator) error {
	if coordinator == nil {
		return nil
	}
	return coordinator.RegisterInitializer(db.RoleCore, meetingAutomationMigration())
}

func meetingAutomationMigration() migration.Migration {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS feishu_meeting_policies (
			tenant_id TEXT NOT NULL,
			provider TEXT NOT NULL,
			account_id TEXT NOT NULL,
			enabled INTEGER NOT NULL DEFAULT 0,
			allowed_chat_ids_json TEXT NOT NULL DEFAULT '[]',
			allowed_actor_ids_json TEXT NOT NULL DEFAULT '[]',
			summary_agent_id TEXT NOT NULL DEFAULT '',
			publish_target TEXT NOT NULL DEFAULT 'source_chat',
			raw_media_retention_ns BIGINT NOT NULL DEFAULT 0,
			max_meeting_duration_ns BIGINT NOT NULL DEFAULT 0,
			daily_transcription_time_ns BIGINT NOT NULL DEFAULT 0,
			version BIGINT NOT NULL DEFAULT 1,
			updated_at BIGINT NOT NULL DEFAULT 0,
			PRIMARY KEY (tenant_id, provider, account_id)
		)`,
		`CREATE TABLE IF NOT EXISTS feishu_meeting_sessions (
			id TEXT PRIMARY KEY,
			tenant_id TEXT NOT NULL,
			provider TEXT NOT NULL,
			account_id TEXT NOT NULL,
			provider_tenant_key TEXT NOT NULL,
			source_chat_id TEXT NOT NULL,
			requested_by TEXT NOT NULL,
			owner_external_id TEXT NOT NULL,
			topic TEXT NOT NULL,
			reservation_id TEXT NOT NULL DEFAULT '',
			meeting_id TEXT NOT NULL DEFAULT '',
			meeting_no TEXT NOT NULL DEFAULT '',
			meeting_url TEXT NOT NULL DEFAULT '',
			minute_token TEXT NOT NULL DEFAULT '',
			recording_duration_ns BIGINT NOT NULL DEFAULT 0,
			status TEXT NOT NULL,
			summary_agent_id TEXT NOT NULL,
			publish_target TEXT NOT NULL,
			raw_media_retention_ns BIGINT NOT NULL DEFAULT 0,
			max_meeting_duration_ns BIGINT NOT NULL DEFAULT 0,
			daily_transcription_time_ns BIGINT NOT NULL DEFAULT 0,
			policy_version BIGINT NOT NULL DEFAULT 0,
			source_context_kind TEXT NOT NULL DEFAULT '',
			source_context_id TEXT NOT NULL DEFAULT '',
			idempotency_key TEXT NOT NULL,
			last_error_code TEXT NOT NULL DEFAULT '',
			expires_at BIGINT NOT NULL,
			started_at BIGINT NOT NULL DEFAULT 0,
			ended_at BIGINT NOT NULL DEFAULT 0,
			recording_ready_at BIGINT NOT NULL DEFAULT 0,
			created_at BIGINT NOT NULL,
			updated_at BIGINT NOT NULL,
			version BIGINT NOT NULL
		)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_feishu_meeting_session_idempotency
		 ON feishu_meeting_sessions (tenant_id, provider, account_id, idempotency_key)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_feishu_meeting_session_external_meeting
		 ON feishu_meeting_sessions (provider, account_id, provider_tenant_key, meeting_id) WHERE meeting_id <> ''`,
		`CREATE INDEX IF NOT EXISTS idx_feishu_meeting_session_external_number
		 ON feishu_meeting_sessions (provider, account_id, provider_tenant_key, meeting_no, status, created_at) WHERE meeting_no <> ''`,
		`CREATE INDEX IF NOT EXISTS idx_feishu_meeting_session_unbound
		 ON feishu_meeting_sessions (provider, account_id, provider_tenant_key, status, created_at)`,
		`CREATE INDEX IF NOT EXISTS idx_feishu_meeting_session_tenant_status
		 ON feishu_meeting_sessions (tenant_id, status, updated_at)`,
	}
	return migration.Migration{
		Version:     "0185_feishu_meeting_automation",
		Description: "create Feishu meeting automation policies and sessions",
		Statements:  statements,
		StatementsByDialect: map[db.Dialect][]string{
			db.DialectPostgres: append([]string(nil), statements...),
		},
	}
}
