package hil

import (
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/migration"
)

func RegisterMigrations(coordinator *migration.Coordinator) error {
	if coordinator == nil {
		return nil
	}
	if err := coordinator.RegisterInitializer(db.RoleCore, hilDecisionMigration()); err != nil {
		return err
	}
	return coordinator.RegisterInitializer(db.RoleCore, hilWorkflowMigration())
}

func hilDecisionMigration() migration.Migration {
	return migration.Migration{
		Version:     "0170_hil_decision_control_plane",
		Description: "create HIL decision and council ledgers",
		Statements:  hilDecisionStatements("TEXT", "INTEGER"),
		StatementsByDialect: map[db.Dialect][]string{
			db.DialectPostgres: hilDecisionStatements("JSONB", "BIGINT"),
		},
	}
}

func hilDecisionStatements(jsonType, timeType string) []string {
	return []string{
		`CREATE TABLE IF NOT EXISTS hil_decision_requests (
			id TEXT PRIMARY KEY,
			tenant_id TEXT NOT NULL,
			requester_kind TEXT NOT NULL,
			requester_id TEXT NOT NULL,
			action TEXT NOT NULL,
			action_type_id TEXT NOT NULL DEFAULT '',
			resource_kind TEXT NOT NULL,
			resource_id TEXT NOT NULL,
			object_refs_json ` + jsonType + ` NOT NULL DEFAULT '[]',
			args_hash TEXT NOT NULL,
			purpose TEXT NOT NULL DEFAULT '',
			risk_level TEXT NOT NULL,
			preferred_mode TEXT NOT NULL DEFAULT '',
			route_mode TEXT NOT NULL,
			route_effect TEXT NOT NULL DEFAULT '',
			route_reasons_json ` + jsonType + ` NOT NULL DEFAULT '[]',
			context_json ` + jsonType + ` NOT NULL DEFAULT '{}',
			payload_preview_json ` + jsonType + ` NOT NULL DEFAULT '{}',
			idempotency_key TEXT NOT NULL,
			request_fingerprint TEXT NOT NULL,
			status TEXT NOT NULL,
			created_at ` + timeType + ` NOT NULL,
			expires_at ` + timeType + `,
			UNIQUE (tenant_id, idempotency_key)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_hil_decisions_tenant_status ON hil_decision_requests (tenant_id, status, created_at)`,
		`CREATE INDEX IF NOT EXISTS idx_hil_decisions_tenant_mode ON hil_decision_requests (tenant_id, route_mode, created_at)`,
		`CREATE TABLE IF NOT EXISTS hil_council_sessions (
			id TEXT PRIMARY KEY,
			tenant_id TEXT NOT NULL,
			request_id TEXT NOT NULL,
			args_hash TEXT NOT NULL,
			risk_level TEXT NOT NULL,
			status TEXT NOT NULL,
			effect TEXT NOT NULL DEFAULT '',
			quorum INTEGER NOT NULL,
			approval_threshold INTEGER NOT NULL,
			dissent_json ` + jsonType + ` NOT NULL DEFAULT '[]',
			created_at ` + timeType + ` NOT NULL,
			updated_at ` + timeType + ` NOT NULL,
			expires_at ` + timeType + `,
			resolved_at ` + timeType + `,
			UNIQUE (tenant_id, request_id),
			FOREIGN KEY (request_id) REFERENCES hil_decision_requests(id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_hil_councils_tenant_status ON hil_council_sessions (tenant_id, status, created_at)`,
		`CREATE TABLE IF NOT EXISTS hil_council_participants (
			tenant_id TEXT NOT NULL,
			council_id TEXT NOT NULL,
			actor_kind TEXT NOT NULL,
			actor_id TEXT NOT NULL,
			display_name TEXT NOT NULL DEFAULT '',
			role TEXT NOT NULL DEFAULT '',
			required BOOLEAN NOT NULL DEFAULT FALSE,
			PRIMARY KEY (tenant_id, council_id, actor_kind, actor_id),
			FOREIGN KEY (council_id) REFERENCES hil_council_sessions(id)
		)`,
		`CREATE TABLE IF NOT EXISTS hil_council_contributions (
			tenant_id TEXT NOT NULL,
			council_id TEXT NOT NULL,
			actor_kind TEXT NOT NULL,
			actor_id TEXT NOT NULL,
			position TEXT NOT NULL,
			reasoning TEXT NOT NULL,
			evidence_refs_json ` + jsonType + ` NOT NULL DEFAULT '[]',
			idempotency_key TEXT NOT NULL,
			request_fingerprint TEXT NOT NULL,
			created_at ` + timeType + ` NOT NULL,
			updated_at ` + timeType + ` NOT NULL,
			PRIMARY KEY (tenant_id, council_id, actor_kind, actor_id),
			UNIQUE (tenant_id, idempotency_key),
			FOREIGN KEY (council_id) REFERENCES hil_council_sessions(id)
		)`,
		`CREATE TABLE IF NOT EXISTS hil_council_votes (
			tenant_id TEXT NOT NULL,
			council_id TEXT NOT NULL,
			actor_kind TEXT NOT NULL,
			actor_id TEXT NOT NULL,
			choice TEXT NOT NULL,
			reason TEXT NOT NULL DEFAULT '',
			idempotency_key TEXT NOT NULL,
			request_fingerprint TEXT NOT NULL,
			created_at ` + timeType + ` NOT NULL,
			PRIMARY KEY (tenant_id, council_id, actor_kind, actor_id),
			UNIQUE (tenant_id, idempotency_key),
			FOREIGN KEY (council_id) REFERENCES hil_council_sessions(id)
		)`,
		`CREATE TABLE IF NOT EXISTS hil_decision_receipts (
			id TEXT PRIMARY KEY,
			tenant_id TEXT NOT NULL,
			request_id TEXT NOT NULL,
			council_id TEXT NOT NULL DEFAULT '',
			args_hash TEXT NOT NULL,
			effect TEXT NOT NULL,
			single_use BOOLEAN NOT NULL DEFAULT TRUE,
			created_at ` + timeType + ` NOT NULL,
			expires_at ` + timeType + `,
			used_at ` + timeType + `,
			FOREIGN KEY (request_id) REFERENCES hil_decision_requests(id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_hil_receipts_request ON hil_decision_receipts (tenant_id, request_id, created_at)`,
	}
}

func hilWorkflowMigration() migration.Migration {
	return migration.Migration{
		Version:     "0199_hil_approval_workflows",
		Description: "create versioned HIL approval workflows",
		Statements:  hilWorkflowStatements("TEXT", "INTEGER"),
		StatementsByDialect: map[db.Dialect][]string{
			db.DialectPostgres: hilWorkflowStatements("JSONB", "BIGINT"),
		},
	}
}

func hilWorkflowStatements(jsonType, timeType string) []string {
	return []string{
		`CREATE TABLE IF NOT EXISTS hil_workflow_templates (
			id TEXT PRIMARY KEY,
			tenant_id TEXT NOT NULL,
			name TEXT NOT NULL,
			description TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL,
			draft_definition_json ` + jsonType + ` NOT NULL DEFAULT '{}',
			latest_published_version INTEGER NOT NULL DEFAULT 0,
			created_by_kind TEXT NOT NULL,
			created_by_id TEXT NOT NULL,
			created_at ` + timeType + ` NOT NULL,
			updated_at ` + timeType + ` NOT NULL,
			UNIQUE (tenant_id, name)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_hil_workflow_templates_tenant_status ON hil_workflow_templates (tenant_id, status, updated_at)`,
		`CREATE TABLE IF NOT EXISTS hil_workflow_template_versions (
			id TEXT PRIMARY KEY,
			tenant_id TEXT NOT NULL,
			template_id TEXT NOT NULL,
			version INTEGER NOT NULL,
			definition_json ` + jsonType + ` NOT NULL,
			definition_hash TEXT NOT NULL,
			published_by_kind TEXT NOT NULL,
			published_by_id TEXT NOT NULL,
			published_at ` + timeType + ` NOT NULL,
			UNIQUE (tenant_id, template_id, version),
			FOREIGN KEY (template_id) REFERENCES hil_workflow_templates(id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_hil_workflow_versions_template ON hil_workflow_template_versions (tenant_id, template_id, version)`,
		`CREATE TABLE IF NOT EXISTS hil_workflow_template_bindings (
			id TEXT PRIMARY KEY,
			tenant_id TEXT NOT NULL,
			action_type_id TEXT NOT NULL,
			tool_name TEXT NOT NULL DEFAULT '',
			risk_level TEXT NOT NULL DEFAULT '',
			template_id TEXT NOT NULL,
			active BOOLEAN NOT NULL DEFAULT TRUE,
			created_by_kind TEXT NOT NULL,
			created_by_id TEXT NOT NULL,
			created_at ` + timeType + ` NOT NULL,
			updated_at ` + timeType + ` NOT NULL,
			UNIQUE (tenant_id, action_type_id, tool_name, risk_level),
			FOREIGN KEY (template_id) REFERENCES hil_workflow_templates(id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_hil_workflow_bindings_lookup ON hil_workflow_template_bindings (tenant_id, active, action_type_id, tool_name, risk_level)`,
		`CREATE TABLE IF NOT EXISTS hil_workflow_cases (
			id TEXT PRIMARY KEY,
			tenant_id TEXT NOT NULL,
			requester_kind TEXT NOT NULL,
			requester_id TEXT NOT NULL,
			action_type_id TEXT NOT NULL,
			tool_name TEXT NOT NULL DEFAULT '',
			resource_kind TEXT NOT NULL,
			resource_id TEXT NOT NULL,
			created_at ` + timeType + ` NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_hil_workflow_cases_tenant_created ON hil_workflow_cases (tenant_id, created_at)`,
		`CREATE TABLE IF NOT EXISTS hil_workflow_instances (
			id TEXT PRIMARY KEY,
			tenant_id TEXT NOT NULL,
			case_id TEXT NOT NULL,
			revision INTEGER NOT NULL,
			decision_request_id TEXT NOT NULL,
			template_version_id TEXT NOT NULL,
			predecessor_instance_id TEXT NOT NULL DEFAULT '',
			definition_hash TEXT NOT NULL,
			material_hash TEXT NOT NULL,
			args_hash TEXT NOT NULL,
			status TEXT NOT NULL,
			current_stage_key TEXT NOT NULL DEFAULT '',
			version INTEGER NOT NULL DEFAULT 1,
			created_at ` + timeType + ` NOT NULL,
			updated_at ` + timeType + ` NOT NULL,
			resolved_at ` + timeType + `,
			expires_at ` + timeType + `,
			UNIQUE (tenant_id, case_id, revision),
			FOREIGN KEY (case_id) REFERENCES hil_workflow_cases(id),
			FOREIGN KEY (decision_request_id) REFERENCES hil_decision_requests(id),
			FOREIGN KEY (template_version_id) REFERENCES hil_workflow_template_versions(id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_hil_workflow_instances_tenant_status ON hil_workflow_instances (tenant_id, status, created_at)`,
		`CREATE INDEX IF NOT EXISTS idx_hil_workflow_instances_case_revision ON hil_workflow_instances (tenant_id, case_id, revision)`,
		`CREATE TABLE IF NOT EXISTS hil_workflow_stage_instances (
			id TEXT PRIMARY KEY,
			tenant_id TEXT NOT NULL,
			workflow_instance_id TEXT NOT NULL,
			stage_key TEXT NOT NULL,
			stage_order INTEGER NOT NULL,
			stage_type TEXT NOT NULL,
			policy_json ` + jsonType + ` NOT NULL,
			resolved_participants_json ` + jsonType + ` NOT NULL DEFAULT '[]',
			resolved_escalation_actors_json ` + jsonType + ` NOT NULL DEFAULT '[]',
			status TEXT NOT NULL,
			deadline_at ` + timeType + `,
			escalation_index INTEGER NOT NULL DEFAULT -1,
			created_at ` + timeType + ` NOT NULL,
			activated_at ` + timeType + `,
			resolved_at ` + timeType + `,
			UNIQUE (tenant_id, workflow_instance_id, stage_key),
			UNIQUE (tenant_id, workflow_instance_id, stage_order),
			FOREIGN KEY (workflow_instance_id) REFERENCES hil_workflow_instances(id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_hil_workflow_stages_active_deadline ON hil_workflow_stage_instances (tenant_id, status, deadline_at)`,
		`CREATE TABLE IF NOT EXISTS hil_workflow_tasks (
			id TEXT PRIMARY KEY,
			tenant_id TEXT NOT NULL,
			workflow_instance_id TEXT NOT NULL,
			stage_instance_id TEXT NOT NULL,
			seat_key TEXT NOT NULL,
			original_assignee_kind TEXT NOT NULL,
			original_assignee_id TEXT NOT NULL,
			current_assignee_kind TEXT NOT NULL,
			current_assignee_id TEXT NOT NULL,
			status TEXT NOT NULL,
			decision TEXT NOT NULL DEFAULT '',
			reason TEXT NOT NULL DEFAULT '',
			transfer_count INTEGER NOT NULL DEFAULT 0,
			transferred_from_id TEXT NOT NULL DEFAULT '',
			transferred_to_id TEXT NOT NULL DEFAULT '',
			transfer_reason TEXT NOT NULL DEFAULT '',
			deadline_at ` + timeType + `,
			created_at ` + timeType + ` NOT NULL,
			updated_at ` + timeType + ` NOT NULL,
			completed_at ` + timeType + `,
			UNIQUE (tenant_id, stage_instance_id, seat_key),
			FOREIGN KEY (workflow_instance_id) REFERENCES hil_workflow_instances(id),
			FOREIGN KEY (stage_instance_id) REFERENCES hil_workflow_stage_instances(id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_hil_workflow_tasks_actionable_assignee ON hil_workflow_tasks (tenant_id, status, current_assignee_kind, current_assignee_id, created_at)`,
		`CREATE INDEX IF NOT EXISTS idx_hil_workflow_tasks_instance ON hil_workflow_tasks (tenant_id, workflow_instance_id, stage_instance_id)`,
		`CREATE TABLE IF NOT EXISTS hil_workflow_audit_events (
			id TEXT PRIMARY KEY,
			tenant_id TEXT NOT NULL,
			case_id TEXT NOT NULL,
			workflow_instance_id TEXT NOT NULL,
			stage_instance_id TEXT NOT NULL DEFAULT '',
			task_id TEXT NOT NULL DEFAULT '',
			actor_kind TEXT NOT NULL,
			actor_id TEXT NOT NULL,
			event_type TEXT NOT NULL,
			reason TEXT NOT NULL DEFAULT '',
			metadata_json ` + jsonType + ` NOT NULL DEFAULT '{}',
			correlation_id TEXT NOT NULL DEFAULT '',
			idempotency_key TEXT NOT NULL,
			request_fingerprint TEXT NOT NULL,
			created_at ` + timeType + ` NOT NULL,
			UNIQUE (tenant_id, idempotency_key),
			FOREIGN KEY (case_id) REFERENCES hil_workflow_cases(id),
			FOREIGN KEY (workflow_instance_id) REFERENCES hil_workflow_instances(id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_hil_workflow_audit_instance ON hil_workflow_audit_events (tenant_id, workflow_instance_id, created_at)`,
		`CREATE TABLE IF NOT EXISTS hil_workflow_notification_outbox (
			id TEXT PRIMARY KEY,
			tenant_id TEXT NOT NULL,
			workflow_instance_id TEXT NOT NULL,
			stage_instance_id TEXT NOT NULL DEFAULT '',
			task_id TEXT NOT NULL DEFAULT '',
			channel TEXT NOT NULL,
			recipient_kind TEXT NOT NULL,
			recipient_id TEXT NOT NULL,
			event_type TEXT NOT NULL,
			payload_json ` + jsonType + ` NOT NULL DEFAULT '{}',
			status TEXT NOT NULL,
			attempt_count INTEGER NOT NULL DEFAULT 0,
			next_attempt_at ` + timeType + `,
			provider_receipt TEXT NOT NULL DEFAULT '',
			last_error TEXT NOT NULL DEFAULT '',
			idempotency_key TEXT NOT NULL,
			created_at ` + timeType + ` NOT NULL,
			updated_at ` + timeType + ` NOT NULL,
			delivered_at ` + timeType + `,
			UNIQUE (tenant_id, idempotency_key),
			FOREIGN KEY (workflow_instance_id) REFERENCES hil_workflow_instances(id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_hil_workflow_notifications_pending_retry ON hil_workflow_notification_outbox (tenant_id, status, next_attempt_at)`,
		`ALTER TABLE hil_decision_receipts ADD COLUMN workflow_instance_id TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE hil_decision_receipts ADD COLUMN material_hash TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE hil_decision_receipts ADD COLUMN resolver_actor_kind TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE hil_decision_receipts ADD COLUMN resolver_actor_id TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE hil_decision_receipts ADD COLUMN resolver_reason TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE hil_decision_receipts ADD COLUMN resolver_summary_json ` + jsonType + ` NOT NULL DEFAULT '{}'`,
	}
}
