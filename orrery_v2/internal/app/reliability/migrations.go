package reliability

import (
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/migration"
)

func RegisterMigrations(coordinator *migration.Coordinator) error {
	if coordinator == nil {
		return nil
	}
	if err := coordinator.RegisterInitializer(db.RoleCore, reliabilityMigration()); err != nil {
		return err
	}
	if err := coordinator.RegisterInitializer(db.RoleCore, reliabilityRepairExecutionMigration()); err != nil {
		return err
	}
	if err := coordinator.RegisterInitializer(db.RoleCore, reliabilityDiagnosisMigration()); err != nil {
		return err
	}
	return coordinator.RegisterInitializer(db.RoleCore, reliabilityRuleActivationMigration())
}

func reliabilityRuleActivationMigration() migration.Migration {
	return migration.Migration{
		Version:     "0210_reliability_rule_activation",
		Description: "persist governed repair rule activation provenance",
		Statements:  reliabilityRuleActivationMigrationStatements("INTEGER"),
		StatementsByDialect: map[db.Dialect][]string{
			db.DialectPostgres: reliabilityRuleActivationMigrationStatements("BIGINT"),
		},
	}
}

func reliabilityRuleActivationMigrationStatements(timeType string) []string {
	return []string{
		`ALTER TABLE reliability_repair_rules ADD COLUMN activation_decision_id TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE reliability_repair_rules ADD COLUMN activated_by_kind TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE reliability_repair_rules ADD COLUMN activated_by_id TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE reliability_repair_rules ADD COLUMN activated_at ` + timeType,
		`CREATE INDEX IF NOT EXISTS idx_reliability_rules_activation_decision ON reliability_repair_rules (tenant_id, activation_decision_id)`,
	}
}

func reliabilityDiagnosisMigration() migration.Migration {
	return migration.Migration{
		Version:     "0209_reliability_diagnosis_cases",
		Description: "persist evidence-backed diagnosis cases and bind new repair proposals to them",
		Statements:  reliabilityDiagnosisMigrationStatements("INTEGER", "REAL"),
		StatementsByDialect: map[db.Dialect][]string{
			db.DialectPostgres: reliabilityDiagnosisMigrationStatements("BIGINT", "DOUBLE PRECISION"),
		},
	}
}

func reliabilityDiagnosisMigrationStatements(timeType, floatType string) []string {
	return []string{
		`CREATE TABLE IF NOT EXISTS reliability_diagnosis_cases (
			tenant_id TEXT NOT NULL,
			id TEXT NOT NULL,
			incident_id TEXT NOT NULL,
			incident_version BIGINT NOT NULL,
			root_cause_category TEXT NOT NULL,
			redacted_summary TEXT NOT NULL,
			confidence ` + floatType + ` NOT NULL,
			status TEXT NOT NULL,
			proposed_by_kind TEXT NOT NULL,
			proposed_by_id TEXT NOT NULL,
			reviewed_by_kind TEXT NOT NULL DEFAULT '',
			reviewed_by_id TEXT NOT NULL DEFAULT '',
			review_reason TEXT NOT NULL DEFAULT '',
			version BIGINT NOT NULL,
			created_at ` + timeType + ` NOT NULL,
			updated_at ` + timeType + ` NOT NULL,
			PRIMARY KEY (tenant_id, id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_reliability_diagnoses_incident ON reliability_diagnosis_cases (tenant_id, incident_id, updated_at)`,
		`CREATE TABLE IF NOT EXISTS reliability_diagnosis_evidence (
			tenant_id TEXT NOT NULL,
			diagnosis_id TEXT NOT NULL,
			position BIGINT NOT NULL,
			evidence_id TEXT NOT NULL,
			PRIMARY KEY (tenant_id, diagnosis_id, evidence_id),
			UNIQUE (tenant_id, diagnosis_id, position)
		)`,
		`CREATE TABLE IF NOT EXISTS reliability_diagnosis_commands (
			tenant_id TEXT NOT NULL,
			id TEXT NOT NULL,
			diagnosis_id TEXT NOT NULL,
			operation TEXT NOT NULL,
			decision TEXT NOT NULL DEFAULT '',
			reason TEXT NOT NULL DEFAULT '',
			actor_kind TEXT NOT NULL,
			actor_id TEXT NOT NULL,
			created_at ` + timeType + ` NOT NULL,
			PRIMARY KEY (tenant_id, id)
		)`,
		`ALTER TABLE reliability_repair_rules ADD COLUMN diagnosis_case_id TEXT NOT NULL DEFAULT ''`,
		`CREATE INDEX IF NOT EXISTS idx_reliability_rules_diagnosis ON reliability_repair_rules (tenant_id, diagnosis_case_id)`,
	}
}

func reliabilityMigration() migration.Migration {
	return migration.Migration{
		Version:     "0207_reliability_control_plane",
		Description: "persist failure incidents, repair rules, and verified repair outcomes",
		Statements:  reliabilityMigrationStatements("INTEGER", "REAL"),
		StatementsByDialect: map[db.Dialect][]string{
			db.DialectPostgres: reliabilityMigrationStatements("BIGINT", "DOUBLE PRECISION"),
		},
	}
}

func reliabilityMigrationStatements(timeType, floatType string) []string {
	return []string{
		`CREATE TABLE IF NOT EXISTS reliability_failure_incidents (
			tenant_id TEXT NOT NULL,
			id TEXT NOT NULL,
			fingerprint TEXT NOT NULL,
			source TEXT NOT NULL,
			component TEXT NOT NULL,
			category TEXT NOT NULL,
			severity TEXT NOT NULL,
			status TEXT NOT NULL,
			error_code TEXT NOT NULL,
			redacted_summary TEXT NOT NULL DEFAULT '',
			retryable BOOLEAN NOT NULL DEFAULT FALSE,
			first_seen_at ` + timeType + ` NOT NULL,
			last_seen_at ` + timeType + ` NOT NULL,
			occurrence_count BIGINT NOT NULL,
			version BIGINT NOT NULL,
			created_at ` + timeType + ` NOT NULL,
			updated_at ` + timeType + ` NOT NULL,
			PRIMARY KEY (tenant_id, id),
			UNIQUE (tenant_id, fingerprint)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_reliability_incidents_status ON reliability_failure_incidents (tenant_id, status, updated_at)`,
		`CREATE INDEX IF NOT EXISTS idx_reliability_incidents_component ON reliability_failure_incidents (tenant_id, component, last_seen_at)`,
		`CREATE TABLE IF NOT EXISTS reliability_failure_observations (
			tenant_id TEXT NOT NULL,
			id TEXT NOT NULL,
			incident_id TEXT NOT NULL,
			occurred_at ` + timeType + ` NOT NULL,
			PRIMARY KEY (tenant_id, id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_reliability_observations_incident ON reliability_failure_observations (tenant_id, incident_id, occurred_at)`,
		`CREATE TABLE IF NOT EXISTS reliability_repair_rules (
			tenant_id TEXT NOT NULL,
			id TEXT NOT NULL,
			selector_fingerprint TEXT NOT NULL,
			strategy TEXT NOT NULL,
			action_type_id TEXT NOT NULL,
			risk_level TEXT NOT NULL,
			status TEXT NOT NULL,
			confidence ` + floatType + ` NOT NULL,
			support_count BIGINT NOT NULL,
			success_count BIGINT NOT NULL,
			failure_count BIGINT NOT NULL,
			reversible BOOLEAN NOT NULL DEFAULT FALSE,
			idempotent BOOLEAN NOT NULL DEFAULT FALSE,
			auto_apply BOOLEAN NOT NULL DEFAULT FALSE,
			version BIGINT NOT NULL,
			source_incident_id TEXT NOT NULL,
			last_verified_at ` + timeType + `,
			expires_at ` + timeType + `,
			created_at ` + timeType + ` NOT NULL,
			updated_at ` + timeType + ` NOT NULL,
			PRIMARY KEY (tenant_id, id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_reliability_rules_selector ON reliability_repair_rules (tenant_id, selector_fingerprint, status)`,
		`CREATE TABLE IF NOT EXISTS reliability_repair_rule_commands (
			tenant_id TEXT NOT NULL,
			id TEXT NOT NULL,
			rule_id TEXT NOT NULL,
			created_at ` + timeType + ` NOT NULL,
			PRIMARY KEY (tenant_id, id)
		)`,
		`CREATE TABLE IF NOT EXISTS reliability_repair_outcomes (
			tenant_id TEXT NOT NULL,
			id TEXT NOT NULL,
			rule_id TEXT NOT NULL,
			incident_id TEXT NOT NULL,
			success BOOLEAN NOT NULL,
			evidence_id TEXT NOT NULL DEFAULT '',
			occurred_at ` + timeType + ` NOT NULL,
			PRIMARY KEY (tenant_id, id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_reliability_outcomes_rule ON reliability_repair_outcomes (tenant_id, rule_id, occurred_at)`,
	}
}

func reliabilityRepairExecutionMigration() migration.Migration {
	return migration.Migration{
		Version:     "0208_reliability_repair_execution",
		Description: "persist exact repair targets, governed preflights, and monitored repair executions",
		Statements:  reliabilityRepairExecutionStatements("INTEGER"),
		StatementsByDialect: map[db.Dialect][]string{
			db.DialectPostgres: reliabilityRepairExecutionStatements("BIGINT"),
		},
	}
}

func reliabilityRepairExecutionStatements(timeType string) []string {
	return []string{
		`CREATE TABLE IF NOT EXISTS reliability_failure_repair_targets (
			tenant_id TEXT NOT NULL,
			observation_id TEXT NOT NULL,
			incident_id TEXT NOT NULL,
			target_kind TEXT NOT NULL,
			authorization_tenant_id TEXT NOT NULL,
			mission_id TEXT NOT NULL,
			run_id TEXT NOT NULL,
			occurred_at ` + timeType + ` NOT NULL,
			PRIMARY KEY (tenant_id, observation_id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_reliability_repair_targets_incident ON reliability_failure_repair_targets (tenant_id, incident_id, occurred_at)`,
		`CREATE TABLE IF NOT EXISTS reliability_repair_preflights (
			tenant_id TEXT NOT NULL,
			evidence_id TEXT NOT NULL,
			action_run_id TEXT NOT NULL,
			incident_id TEXT NOT NULL,
			incident_version BIGINT NOT NULL,
			rule_id TEXT NOT NULL,
			rule_version BIGINT NOT NULL,
			strategy TEXT NOT NULL,
			args_hash TEXT NOT NULL,
			target_observation_id TEXT NOT NULL DEFAULT '',
			target_kind TEXT NOT NULL DEFAULT '',
			target_run_id TEXT NOT NULL DEFAULT '',
			passed BOOLEAN NOT NULL,
			checked_at ` + timeType + ` NOT NULL,
			expires_at ` + timeType + ` NOT NULL,
			PRIMARY KEY (tenant_id, evidence_id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_reliability_preflights_binding ON reliability_repair_preflights (tenant_id, incident_id, rule_id, expires_at)`,
		`CREATE TABLE IF NOT EXISTS reliability_repair_executions (
			tenant_id TEXT NOT NULL,
			action_run_id TEXT NOT NULL,
			incident_id TEXT NOT NULL,
			rule_id TEXT NOT NULL,
			preflight_evidence_id TEXT NOT NULL,
			strategy TEXT NOT NULL,
			target_observation_id TEXT NOT NULL,
			target_kind TEXT NOT NULL,
			target_authorization_tenant_id TEXT NOT NULL,
			target_mission_id TEXT NOT NULL,
			target_run_id TEXT NOT NULL,
			target_occurred_at ` + timeType + ` NOT NULL,
			retry_run_id TEXT NOT NULL,
			status TEXT NOT NULL,
			failure_code TEXT NOT NULL DEFAULT '',
			version BIGINT NOT NULL,
			started_at ` + timeType + ` NOT NULL,
			deadline_at ` + timeType + ` NOT NULL,
			updated_at ` + timeType + ` NOT NULL,
			completed_at ` + timeType + `,
			PRIMARY KEY (tenant_id, action_run_id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_reliability_repair_executions_active ON reliability_repair_executions (status, updated_at)`,
		`CREATE INDEX IF NOT EXISTS idx_reliability_repair_executions_incident ON reliability_repair_executions (tenant_id, incident_id, status)`,
	}
}
