package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"orrery_v2/internal/app/actioncontract"
	"orrery_v2/internal/app/ontology"
	"orrery_v2/internal/platform"
	"orrery_v2/internal/platform/testkit"
)

func TestOntologyModuleInstallsDefaultActionTypes(t *testing.T) {
	ctx := context.Background()
	rt := testkit.NewInitializedTestRuntime(t, OntologyModule())
	core, err := rt.Databases.Store("core")
	if err != nil {
		t.Fatalf("core store: %v", err)
	}
	repo := actioncontract.NewPlatformRepository(core)

	actionType, err := repo.GetActionType(ctx, "mcp.tool.call")
	if err != nil {
		t.Fatalf("get default mcp action: %v", err)
	}
	if actionType.ProviderKind != actioncontract.ProviderMCPTool || actionType.PermissionScope != "tool.call" {
		t.Fatalf("mcp action = %#v", actionType)
	}
	registry := ontology.NewPlatformRegistry(core)
	for _, id := range []string{"mission", "task", "person", "team", "department", "position", "role_assignment", "idea", "requirement", "decision", "design_artifact", "legal_entity", "contract", "policy", "approval_case", "repository", "issue", "pull_request", "release", "value_objective", "metric", "initiative", "experiment", "tool", "mcp_server", "agent_run", "responsibility_assignment", "responsibility_context", "responsibility_policy", "iris_snapshot", "action_run"} {
		if _, err := registry.GetObjectType(ctx, id); err != nil {
			t.Fatalf("default ontology object type %s missing: %v", id, err)
		}
	}
	if _, err := registry.GetLinkType(ctx, "mission_has_task"); err != nil {
		t.Fatalf("default ontology link type missing: %v", err)
	}
	if _, err := registry.GetPropertyType(ctx, "mission.title"); err != nil {
		t.Fatalf("default ontology property type missing: %v", err)
	}
	for objectTypeID, actionIDs := range map[string][]string{
		"task":          {"task.complete", "iris.accept_recommendation"},
		"mission":       {"mission.plan", "iris.accept_recommendation"},
		"person":        {"people.assign_owner"},
		"team":          {"people.assign_owner"},
		"contract":      {"legal.review_contract"},
		"policy":        {"legal.review_contract"},
		"approval_case": {"legal.review_contract"},
		"repository":    {"development.create_commit", "development.push_branch", "development.submit_code", "development.open_pull_request", "development.merge_pull_request"},
		"issue":         {"development.open_pull_request"},
		"pull_request":  {"development.open_pull_request", "development.merge_pull_request"},
		"release":       {"development.merge_pull_request"},
		"value_objective": {
			"value.record_metric",
			"value.evaluate_initiative",
		},
		"metric":                    {"value.record_metric", "value.evaluate_initiative"},
		"initiative":                {"value.evaluate_initiative"},
		"tool":                      {"mcp.tool.call"},
		"mcp_server":                {"mcp.tool.call"},
		"agent_run":                 {"mcp.tool.call"},
		"responsibility_assignment": {"mcp.tool.call"},
		"responsibility_context":    {"mcp.tool.call"},
		"responsibility_policy":     {"mcp.tool.call"},
		"iris_snapshot":             {"iris.accept_recommendation"},
	} {
		objectType, err := registry.GetObjectType(ctx, objectTypeID)
		if err != nil {
			t.Fatalf("GetObjectType(%s): %v", objectTypeID, err)
		}
		for _, actionID := range actionIDs {
			if !containsString(objectType.AllowedActions, actionID) {
				t.Fatalf("object type %s allowed actions = %+v, want %s", objectTypeID, objectType.AllowedActions, actionID)
			}
		}
	}
	for _, table := range []string{"ontology_object_types", "ontology_link_types", "ontology_property_types", "ontology_mapping_rules", "ontology_scope_bindings", "ontology_scope_binding_audits", "people_ingestion_batches", "people_ingestion_events", "rebac_authorization_revision", "data_policy_revision", "hil_decision_requests", "hil_council_sessions"} {
		exists, err := core.TableExists(ctx, table)
		if err != nil {
			t.Fatalf("TableExists(%s): %v", table, err)
		}
		if !exists {
			t.Fatalf("ontology module missing table %s", table)
		}
	}
}

func TestOntologyModuleRegistersAlertAuditStatusMetrics(t *testing.T) {
	ctx := context.Background()
	rt := testkit.NewInitializedTestRuntime(t, OntologyModule())
	core, err := rt.Databases.Store("core")
	if err != nil {
		t.Fatalf("core store: %v", err)
	}
	store := ontology.NewPlatformModelRunEvaluationTrendAlertAuditStore(core)
	if err := store.SaveModelRunEvaluationTrendAlertAuditEvent(ctx, ontology.ModelRunEvaluationTrendAlertAuditEvent{
		EventType:  "ontology.model_run_evaluation_alert.firing",
		TenantID:   "tenant-1",
		RuleID:     "accuracy-warning.trend_failure",
		Status:     ontology.ModelRunEvaluationTrendAlertFiring,
		ObservedAt: time.Date(2026, 6, 22, 9, 0, 0, 0, time.UTC),
		Labels:     map[string]string{"tenant_id": "tenant-1"},
		Details:    map[string]string{"observed_value": "1"},
	}); err != nil {
		t.Fatalf("SaveModelRunEvaluationTrendAlertAuditEvent: %v", err)
	}

	rec := httptest.NewRecorder()
	platform.NewMetricsHandler(rt).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := rec.Body.String()
	if !strings.Contains(body, `orrery_ontology_model_run_evaluation_alert_audits_total{role="core",status="firing"} 1`) {
		t.Fatalf("metrics body missing alert audit status count:\n%s", body)
	}
}
