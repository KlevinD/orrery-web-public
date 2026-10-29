package problemhub

import (
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/migration"
)

func RegisterMigrations(coordinator *migration.Coordinator) error {
	if coordinator == nil {
		return nil
	}
	return coordinator.RegisterInitializer(db.RoleCore, problemHubMigration())
}

func problemHubMigration() migration.Migration {
	return migration.Migration{
		Version:     "0211_problem_hub",
		Description: "persist the company problem routing and lesson-candidate ledger",
		Statements:  problemHubMigrationStatements("INTEGER"),
		StatementsByDialect: map[db.Dialect][]string{
			db.DialectPostgres: problemHubMigrationStatements("BIGINT"),
		},
	}
}

func problemHubMigrationStatements(timeType string) []string {
	return []string{
		`CREATE TABLE IF NOT EXISTS problem_hub_cases (
			tenant_id TEXT NOT NULL,
			id TEXT NOT NULL,
			fingerprint TEXT NOT NULL,
			source_domain TEXT NOT NULL,
			category TEXT NOT NULL,
			severity TEXT NOT NULL,
			status TEXT NOT NULL,
			title TEXT NOT NULL,
			redacted_summary TEXT NOT NULL DEFAULT '',
			owner_kind TEXT NOT NULL DEFAULT '',
			owner_id TEXT NOT NULL DEFAULT '',
			primary_source_kind TEXT NOT NULL,
			primary_source_id TEXT NOT NULL,
			occurrence_count BIGINT NOT NULL,
			latest_outcome_id TEXT NOT NULL DEFAULT '',
			latest_outcome_success BOOLEAN NOT NULL DEFAULT FALSE,
			latest_outcome_summary TEXT NOT NULL DEFAULT '',
			latest_resolution_kind TEXT NOT NULL DEFAULT '',
			latest_resolution_id TEXT NOT NULL DEFAULT '',
			lesson_candidate_id TEXT NOT NULL DEFAULT '',
			version BIGINT NOT NULL,
			first_seen_at ` + timeType + ` NOT NULL,
			last_seen_at ` + timeType + ` NOT NULL,
			resolved_at ` + timeType + `,
			created_at ` + timeType + ` NOT NULL,
			updated_at ` + timeType + ` NOT NULL,
			PRIMARY KEY (tenant_id, id),
			UNIQUE (tenant_id, fingerprint)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_problem_hub_cases_status ON problem_hub_cases (tenant_id, status, updated_at)`,
		`CREATE INDEX IF NOT EXISTS idx_problem_hub_cases_owner ON problem_hub_cases (tenant_id, owner_kind, owner_id, updated_at)`,
		`CREATE TABLE IF NOT EXISTS problem_hub_observations (
			tenant_id TEXT NOT NULL,
			id TEXT NOT NULL,
			problem_case_id TEXT NOT NULL,
			source_kind TEXT NOT NULL,
			source_id TEXT NOT NULL,
			evidence_id TEXT NOT NULL,
			occurred_at ` + timeType + ` NOT NULL,
			PRIMARY KEY (tenant_id, id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_problem_hub_observations_case ON problem_hub_observations (tenant_id, problem_case_id, occurred_at)`,
		`CREATE TABLE IF NOT EXISTS problem_hub_source_bindings (
			tenant_id TEXT NOT NULL,
			source_kind TEXT NOT NULL,
			source_id TEXT NOT NULL,
			problem_case_id TEXT NOT NULL,
			PRIMARY KEY (tenant_id, source_kind, source_id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_problem_hub_sources_case ON problem_hub_source_bindings (tenant_id, problem_case_id)`,
		`CREATE TABLE IF NOT EXISTS problem_hub_evidence (
			tenant_id TEXT NOT NULL,
			problem_case_id TEXT NOT NULL,
			evidence_id TEXT NOT NULL,
			PRIMARY KEY (tenant_id, problem_case_id, evidence_id)
		)`,
		`CREATE TABLE IF NOT EXISTS problem_hub_outcomes (
			tenant_id TEXT NOT NULL,
			id TEXT NOT NULL,
			problem_case_id TEXT NOT NULL,
			success BOOLEAN NOT NULL,
			resolution_kind TEXT NOT NULL,
			resolution_id TEXT NOT NULL,
			redacted_summary TEXT NOT NULL,
			evidence_id TEXT NOT NULL,
			actor_kind TEXT NOT NULL,
			actor_id TEXT NOT NULL,
			occurred_at ` + timeType + ` NOT NULL,
			PRIMARY KEY (tenant_id, id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_problem_hub_outcomes_case ON problem_hub_outcomes (tenant_id, problem_case_id, occurred_at)`,
		`CREATE TABLE IF NOT EXISTS problem_hub_route_commands (
			tenant_id TEXT NOT NULL,
			id TEXT NOT NULL,
			problem_case_id TEXT NOT NULL,
			owner_kind TEXT NOT NULL,
			owner_id TEXT NOT NULL,
			occurred_at ` + timeType + ` NOT NULL,
			PRIMARY KEY (tenant_id, id)
		)`,
	}
}
