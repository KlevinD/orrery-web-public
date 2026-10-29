package ontologyreasoning

import (
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/migration"
)

func RegisterMigrations(coordinator *migration.Coordinator) error {
	if coordinator == nil {
		return nil
	}
	if err := coordinator.RegisterInitializer(db.RoleCore, ontologyReasoningRunsMigration()); err != nil {
		return err
	}
	if err := coordinator.RegisterInitializer(db.RoleCore, inferenceLifecycleMigration()); err != nil {
		return err
	}
	return coordinator.RegisterInitializer(db.RoleCore, continuousInferenceMigration())
}

func continuousInferenceMigration() migration.Migration {
	return migration.Migration{Version: "0240_ontology_continuous_inference", Description: "persist published inference rules, observations, model judgments and replayable effect receipts", Statements: []string{
		`CREATE TABLE IF NOT EXISTS ontology_inference_rule_sets (tenant_id TEXT NOT NULL,id TEXT NOT NULL,agent_id TEXT NOT NULL,version BIGINT NOT NULL,active BOOLEAN NOT NULL,body_json TEXT NOT NULL,observed_stamp TEXT NOT NULL DEFAULT '',sequence BIGINT NOT NULL DEFAULT 0,next_check_at BIGINT NOT NULL DEFAULT 0,PRIMARY KEY(tenant_id,id))`,
		`CREATE INDEX IF NOT EXISTS idx_ontology_inference_rule_sets_due ON ontology_inference_rule_sets(active,next_check_at,tenant_id,id)`,
		`CREATE TABLE IF NOT EXISTS ontology_inference_rule_set_versions (tenant_id TEXT NOT NULL,rule_set_id TEXT NOT NULL,version BIGINT NOT NULL,body_json TEXT NOT NULL,PRIMARY KEY(tenant_id,rule_set_id,version))`,
		`CREATE TABLE IF NOT EXISTS ontology_inference_dependencies (tenant_id TEXT NOT NULL,rule_set_id TEXT NOT NULL,candidate_id TEXT NOT NULL,dependency_ref TEXT NOT NULL,dependency_hash TEXT NOT NULL,PRIMARY KEY(tenant_id,rule_set_id,candidate_id,dependency_ref))`,
		`CREATE INDEX IF NOT EXISTS idx_ontology_inference_dependencies_source ON ontology_inference_dependencies(tenant_id,dependency_ref,rule_set_id)`,
		`CREATE TABLE IF NOT EXISTS ontology_inference_receipts (tenant_id TEXT NOT NULL,actor_hash TEXT NOT NULL,query_hash TEXT NOT NULL,idempotency_key TEXT NOT NULL,candidate_id TEXT NOT NULL,plan_id TEXT NOT NULL,receipt_json TEXT NOT NULL,PRIMARY KEY(tenant_id,actor_hash,query_hash,idempotency_key))`,
		`CREATE TABLE IF NOT EXISTS ontology_inference_decisions (job_id TEXT PRIMARY KEY,tenant_id TEXT NOT NULL,rule_set_id TEXT NOT NULL,candidate_id TEXT NOT NULL,support_hash TEXT NOT NULL,decision_json TEXT NOT NULL,created_at BIGINT NOT NULL)`,
	}}
}

func ontologyReasoningRunsMigration() migration.Migration {
	return migration.Migration{
		Version:     "0150_ontology_reasoning_runs",
		Description: "create ontology reasoning run ledger",
		Statements: []string{
			ontologyReasoningRunsTableSQL("TEXT", "INTEGER"),
			`CREATE INDEX IF NOT EXISTS idx_ontology_reasoning_runs_tenant_status ON ontology_reasoning_runs (tenant_id, status, created_at)`,
			`CREATE INDEX IF NOT EXISTS idx_ontology_reasoning_runs_model ON ontology_reasoning_runs (tenant_id, engine, model, created_at)`,
		},
		StatementsByDialect: map[db.Dialect][]string{
			db.DialectPostgres: {
				ontologyReasoningRunsTableSQL("JSONB", "BIGINT"),
				`CREATE INDEX IF NOT EXISTS idx_ontology_reasoning_runs_tenant_status ON ontology_reasoning_runs (tenant_id, status, created_at)`,
				`CREATE INDEX IF NOT EXISTS idx_ontology_reasoning_runs_model ON ontology_reasoning_runs (tenant_id, engine, model, created_at)`,
				`CREATE INDEX IF NOT EXISTS idx_ontology_reasoning_runs_candidates_gin ON ontology_reasoning_runs USING GIN (candidates_json)`,
				`CREATE INDEX IF NOT EXISTS idx_ontology_reasoning_runs_validation_gin ON ontology_reasoning_runs USING GIN (validation_json)`,
			},
		},
	}
}

func ontologyReasoningRunsTableSQL(jsonType, timeType string) string {
	return `CREATE TABLE IF NOT EXISTS ontology_reasoning_runs (
		id TEXT PRIMARY KEY,
		tenant_id TEXT NOT NULL,
		engine TEXT NOT NULL DEFAULT '',
		model TEXT NOT NULL DEFAULT '',
		model_version TEXT NOT NULL DEFAULT '',
		intent_hash TEXT NOT NULL DEFAULT '',
		input_hash TEXT NOT NULL DEFAULT '',
		prompt_hash TEXT NOT NULL DEFAULT '',
		context_pack_hash TEXT NOT NULL DEFAULT '',
		status TEXT NOT NULL,
		candidate_count INTEGER NOT NULL DEFAULT 0,
		valid_candidate_count INTEGER NOT NULL DEFAULT 0,
		rejected_candidate_count INTEGER NOT NULL DEFAULT 0,
		review_required_count INTEGER NOT NULL DEFAULT 0,
		request_json ` + jsonType + ` NOT NULL DEFAULT '{}',
		context_pack_json ` + jsonType + ` NOT NULL DEFAULT '{}',
		candidates_json ` + jsonType + ` NOT NULL DEFAULT '[]',
		validation_json ` + jsonType + ` NOT NULL DEFAULT '[]',
		proposals_json ` + jsonType + ` NOT NULL DEFAULT '[]',
		error_message TEXT NOT NULL DEFAULT '',
		created_at ` + timeType + ` NOT NULL,
		updated_at ` + timeType + ` NOT NULL,
		completed_at ` + timeType + ` NOT NULL DEFAULT 0
	)`
}
