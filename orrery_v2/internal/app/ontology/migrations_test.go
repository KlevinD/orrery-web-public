package ontology_test

import (
	"context"
	"testing"

	"orrery_v2/internal/app/ontology"
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/migration"
)

func TestRegisterMigrationsCreatesOntologyRegistryTables(t *testing.T) {
	ctx := context.Background()
	databases, err := db.OpenRegistry(ctx, db.RegistryConfig{Databases: map[string]db.Config{
		db.RoleCore: {Driver: db.DriverMemory, Name: "ontology-migrations"},
	}})
	if err != nil {
		t.Fatalf("open database registry: %v", err)
	}
	defer databases.Close()
	core, err := databases.Store(db.RoleCore)
	if err != nil {
		t.Fatalf("core database: %v", err)
	}
	coordinator := migration.NewCoordinator(databases)

	if err := ontology.RegisterMigrations(coordinator); err != nil {
		t.Fatalf("RegisterMigrations returned error: %v", err)
	}
	if err := coordinator.UpRole(ctx, db.RoleCore); err != nil {
		t.Fatalf("migration up: %v", err)
	}

	for _, table := range []string{"ontology_object_types", "ontology_link_types", "ontology_property_types", "ontology_mapping_rules", "ontology_metadata_proposals", "ontology_metadata_promotion_receipts", "ontology_metadata_promotion_plans", "ontology_mapping_projection_refreshes", "ontology_model_runs", "ontology_model_run_evaluation_alert_audits", "ontology_model_run_evaluation_metric_recordings", "ontology_model_run_evaluation_alert_notification_history", "ontology_model_run_evaluation_alert_notification_receipts", "ontology_model_run_evaluation_alert_notification_policies"} {
		assertOntologyTableExists(t, core, table)
	}
	for _, index := range []string{
		"idx_ontology_object_types_tenant",
		"idx_ontology_link_types_from_to",
		"idx_ontology_property_types_object",
		"idx_ontology_mapping_rules_tenant_event",
		"idx_ontology_mapping_rules_source_domain",
		"idx_ontology_mapping_rules_event_schema_version",
		"idx_ontology_metadata_proposals_target",
		"idx_ontology_metadata_proposals_status",
		"idx_ontology_metadata_proposals_source_kind",
		"idx_ontology_metadata_proposals_reviewed",
		"idx_ontology_metadata_promotion_receipts_proposal",
		"idx_ontology_metadata_promotion_receipts_actor",
		"idx_ontology_model_runs_tenant_run",
		"idx_ontology_model_runs_status",
		"idx_ontology_model_run_eval_alert_audits_scope",
		"idx_ontology_model_run_eval_alert_audits_status",
		"idx_ontology_model_run_eval_metric_recordings_scope",
		"idx_ontology_model_run_eval_metric_recordings_metric",
		"idx_ontology_model_run_eval_alert_notification_history_scope",
		"idx_ontology_model_run_eval_alert_notification_receipts_scope",
		"idx_ontology_model_run_eval_alert_notification_receipts_status",
		"idx_ontology_model_run_eval_alert_notification_receipts_channel",
		"idx_ontology_model_run_eval_alert_notification_receipts_retry",
		"idx_ontology_model_run_eval_alert_notification_policies_scope",
		"idx_ontology_model_run_eval_alert_notification_policies_channel",
		"idx_ontology_object_types_workbench_seek",
		"idx_ontology_link_types_workbench_seek",
		"idx_ontology_property_types_workbench_seek",
		"idx_ontology_metadata_proposals_workbench_seek",
		"idx_ontology_metadata_receipts_workbench_seek",
		"idx_ontology_object_types_schema_seek",
		"idx_ontology_link_types_schema_seek",
		"idx_ontology_property_types_schema_seek",
		"idx_ontology_mapping_rules_schema_seek",
		"idx_ontology_metadata_proposals_revision_hash",
		"idx_ontology_metadata_receipts_proposal_binding",
		"idx_ontology_metadata_proposals_governance_seek",
		"idx_ontology_metadata_receipts_governance_seek",
		"idx_ontology_metadata_promotion_plans_active",
		"idx_ontology_metadata_promotion_plans_proposal",
		"idx_ontology_metadata_promotion_plans_idempotency",
		"idx_ontology_metadata_promotion_plans_receipt",
		"idx_ontology_mapping_projection_refresh_pending",
		"idx_ontology_object_types_temporal_mode",
		"idx_ontology_link_types_temporal_mode",
		"idx_ontology_property_types_temporal_mode",
	} {
		assertOntologyIndexExists(t, core, index)
	}
	for _, column := range []string{"source_domain", "source_ref", "event_schema_version", "rule_hash"} {
		assertOntologyColumnExists(t, core, "ontology_mapping_rules", column)
	}
	for _, table := range []string{"ontology_object_types", "ontology_link_types", "ontology_property_types"} {
		assertOntologyColumnExists(t, core, table, "temporal_mode")
	}
	for _, column := range []string{"target_kind", "target_id", "metadata_key", "proposed_value", "source", "source_kind", "source_ref", "source_version", "confidence", "status", "evidence_refs_json", "reviewed_by", "reviewed_by_kind", "reviewed_at", "review_reason", "review_evidence_refs_json", "previous_status", "proposal_revision", "proposal_content_hash"} {
		assertOntologyColumnExists(t, core, "ontology_metadata_proposals", column)
	}
	for _, column := range []string{"receipt_id", "tenant_id", "proposal_id", "proposal_revision", "proposal_content_hash", "proposal_binding_status", "actor_id", "actor_kind", "idempotency_key", "status", "applied", "changes_json", "issues_json", "error_message", "applied_at", "created_at", "updated_at"} {
		assertOntologyColumnExists(t, core, "ontology_metadata_promotion_receipts", column)
	}
	for _, column := range []string{"plan_id", "principal_key", "tenant_id", "binding_version", "proposal_id", "proposal_revision", "proposal_content_hash", "targets_json", "diff_hash", "diff_json", "authorization_revision", "policy_revision", "expires_at", "state", "idempotency_key", "receipt_id", "invalidation_reason", "created_at", "updated_at"} {
		assertOntologyColumnExists(t, core, "ontology_metadata_promotion_plans", column)
	}
	for _, column := range []string{"receipt_id", "tenant_id", "state", "attempts", "last_error_code", "next_attempt_at", "created_at", "updated_at"} {
		assertOntologyColumnExists(t, core, "ontology_mapping_projection_refreshes", column)
	}
	for _, column := range []string{"id", "tenant_id", "engine", "model", "model_version", "run_id", "input_hash", "prompt_hash", "source_ref", "source_version", "status", "candidate_count", "valid_candidate_count", "skipped_candidate_count", "proposal_count", "analysis_total", "analysis_blocking_count", "analysis_warning_count", "error_message", "recorded_at", "created_at", "updated_at"} {
		assertOntologyColumnExists(t, core, "ontology_model_runs", column)
	}
	for _, column := range []string{"id", "tenant_id", "event_type", "rule_id", "severity", "status", "labels_json", "details_json", "observed_at", "created_at", "updated_at"} {
		assertOntologyColumnExists(t, core, "ontology_model_run_evaluation_alert_audits", column)
	}
	for _, column := range []string{"id", "tenant_id", "engine", "model", "model_version", "metric_name", "value", "labels_json", "recorded_at", "created_at", "updated_at"} {
		assertOntologyColumnExists(t, core, "ontology_model_run_evaluation_metric_recordings", column)
	}
	for _, column := range []string{"id", "tenant_id", "rule_id", "labels_json", "last_notification_status", "last_notification_at", "created_at", "updated_at"} {
		assertOntologyColumnExists(t, core, "ontology_model_run_evaluation_alert_notification_history", column)
	}
	for _, column := range []string{"id", "tenant_id", "rule_id", "delivery_status", "alert_status", "reason", "channel", "message_id", "attempt", "retryable", "next_retry_at", "labels_json", "metadata_json", "error_message", "sent_at", "created_at", "updated_at"} {
		assertOntologyColumnExists(t, core, "ontology_model_run_evaluation_alert_notification_receipts", column)
	}
	for _, column := range []string{"id", "tenant_id", "rule_id", "channel", "endpoint", "headers_json", "repeat_window_seconds", "recovery_window_seconds", "max_attempts", "enabled", "created_at", "updated_at"} {
		assertOntologyColumnExists(t, core, "ontology_model_run_evaluation_alert_notification_policies", column)
	}
	assertOntologyColumnExists(t, core, "ontology_link_types", "allowed_actions_json")
	assertOntologyColumnExists(t, core, "ontology_property_types", "allowed_actions_json")
}

func assertOntologyTableExists(t *testing.T, store *db.Store, table string) {
	t.Helper()
	exists, err := store.TableExists(context.Background(), table)
	if err != nil {
		t.Fatalf("TableExists(%s): %v", table, err)
	}
	if !exists {
		t.Fatalf("missing table %s", table)
	}
}

func assertOntologyIndexExists(t *testing.T, store *db.Store, index string) {
	t.Helper()
	var name string
	err := store.QueryRow(context.Background(), `SELECT name FROM sqlite_master WHERE type = 'index' AND name = ?`, index).Scan(&name)
	if err != nil {
		t.Fatalf("missing index %s: %v", index, err)
	}
}

func assertOntologyColumnExists(t *testing.T, store *db.Store, table string, column string) {
	t.Helper()
	rows, err := store.Query(context.Background(), `PRAGMA table_info(`+table+`)`)
	if err != nil {
		t.Fatalf("table_info(%s): %v", table, err)
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, columnType string
		var notNull int
		var defaultValue any
		var primaryKey int
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			t.Fatalf("scan table_info(%s): %v", table, err)
		}
		if name == column {
			return
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("table_info(%s) rows: %v", table, err)
	}
	t.Fatalf("missing column %s.%s", table, column)
}
