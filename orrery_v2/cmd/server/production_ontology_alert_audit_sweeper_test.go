package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"orrery_v2/internal/app/ontology"
	"orrery_v2/internal/platform"
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/testkit"
)

func TestRegisterProductionOntologyPolicyAlertAuditSweeperRecordsPolicyAlerts(t *testing.T) {
	ctx := context.Background()
	rt := testkit.NewInitializedTestRuntime(t, OntologyModule())
	core, err := rt.Databases.Store("core")
	if err != nil {
		t.Fatalf("core store: %v", err)
	}
	seedProductionPolicyAlertAuditSweeperRun(t, ctx, core, rt.Clock.Now().UTC().Add(-time.Minute), "run-policy-alert-production-1")

	if err := registerProductionOntologyPolicyAlertAuditSweeper(rt, core); err != nil {
		t.Fatalf("registerProductionOntologyPolicyAlertAuditSweeper: %v", err)
	}
	report := rt.Sweeper.SweepOnce(ctx)
	if report.Failed != 0 {
		t.Fatalf("sweeper report = %+v", report)
	}
	audits, err := ontology.NewPlatformModelRunEvaluationTrendAlertAuditStore(core).ListModelRunEvaluationTrendAlertAuditEvents(ctx, ontology.ModelRunEvaluationTrendAlertAuditQuery{
		TenantID: "tenant-1",
		RuleID:   "accuracy-warning.trend_failure",
		Status:   ontology.ModelRunEvaluationTrendAlertFiring,
		Limit:    10,
	})
	if err != nil {
		t.Fatalf("ListModelRunEvaluationTrendAlertAuditEvents: %v", err)
	}
	if audits.Page.Total != 1 || audits.Events[0].Labels["metric_threshold_rule_id"] != "accuracy-warning" {
		t.Fatalf("audits = %+v", audits)
	}
}

func TestRegisterProductionOntologyPolicyAlertAuditSweeperUsesRuntimeConfig(t *testing.T) {
	ctx := context.Background()
	rt := newOntologyPolicyAlertAuditSweeperConfiguredRuntime(t, ctx)
	core, err := rt.Databases.Store("core")
	if err != nil {
		t.Fatalf("core store: %v", err)
	}
	seedProductionPolicyAlertAuditSweeperRun(t, ctx, core, rt.Clock.Now().UTC().Add(-20*time.Minute), "run-policy-alert-production-config")

	if err := registerProductionOntologyPolicyAlertAuditSweeper(rt, core); err != nil {
		t.Fatalf("registerProductionOntologyPolicyAlertAuditSweeper: %v", err)
	}
	report := rt.Sweeper.SweepOnce(ctx)
	if report.Failed != 0 {
		t.Fatalf("sweeper report = %+v", report)
	}
	audits, err := ontology.NewPlatformModelRunEvaluationTrendAlertAuditStore(core).ListModelRunEvaluationTrendAlertAuditEvents(ctx, ontology.ModelRunEvaluationTrendAlertAuditQuery{
		TenantID: "tenant-1",
		RuleID:   "accuracy-warning.trend_failure",
		Status:   ontology.ModelRunEvaluationTrendAlertFiring,
		Limit:    10,
	})
	if err != nil {
		t.Fatalf("ListModelRunEvaluationTrendAlertAuditEvents: %v", err)
	}
	if audits.Page.Total != 1 {
		t.Fatalf("audits = %+v", audits)
	}
}

func seedProductionPolicyAlertAuditSweeperRun(t *testing.T, ctx context.Context, core *db.Store, recordedAt time.Time, runID string) {
	t.Helper()
	if err := ontology.NewPlatformModelRunEvaluationPolicyProfileStore(core).SaveModelRunEvaluationPolicyProfile(ctx, ontology.ModelRunEvaluationPolicyProfile{
		TenantID:              "tenant-1",
		Engine:                "ontology_designer",
		Model:                 "gpt-5",
		ModelVersion:          "2026-06-21",
		RequiredArtifactKind:  "evaluation_report",
		RequiredDatasetRef:    "ontology-eval-20260621",
		RequiredPolicyVersion: "v1",
		MetricThresholds: []ontology.ModelRunEvaluationMetricThreshold{{
			RuleID:      "accuracy-warning",
			DisplayName: "Accuracy warning",
			MetricKey:   "accuracy",
			Operator:    ontology.ModelRunEvaluationMetricOperatorGTE,
			Threshold:   0.95,
			Severity:    ontology.ModelRunEvaluationThresholdSeverityWarning,
		}},
		Enabled: true,
	}); err != nil {
		t.Fatalf("SaveModelRunEvaluationPolicyProfile: %v", err)
	}
	if err := ontology.NewPlatformModelRunLedger(core).SaveModelRun(ctx, ontology.OntologyModelRun{
		RunID:        runID,
		TenantID:     "tenant-1",
		Engine:       "ontology_designer",
		Model:        "gpt-5",
		ModelVersion: "2026-06-21",
		InputHash:    "sha256:" + runID + "-input",
		PromptHash:   "sha256:" + runID + "-prompt",
		RecordedAt:   recordedAt,
		Status:       ontology.ModelRunStatusSucceeded,
	}); err != nil {
		t.Fatalf("SaveModelRun: %v", err)
	}
	if err := ontology.NewPlatformModelRunEvaluationArtifactStore(core).SaveModelRunEvaluationArtifact(ctx, ontology.ModelRunEvaluationArtifact{
		TenantID:     "tenant-1",
		Engine:       "ontology_designer",
		RunID:        runID,
		ArtifactKind: "evaluation_report",
		ArtifactURI:  "s3://orrery-eval/" + runID + "/report.json",
		Metadata: map[string]string{
			"dataset_ref":    "ontology-eval-20260621",
			"policy_version": "v1",
			"accuracy":       "0.91",
		},
	}); err != nil {
		t.Fatalf("SaveModelRunEvaluationArtifact: %v", err)
	}
}

func newOntologyPolicyAlertAuditSweeperConfiguredRuntime(t *testing.T, ctx context.Context) *platform.Runtime {
	t.Helper()
	path := filepath.Join(t.TempDir(), "orrery.yaml")
	if err := os.WriteFile(path, []byte(`
database:
  default_url: file://./stage0-test.db
ontology:
  policy_alert_audit_sweeper:
    lookback: 30m
    trend_limit: 5
    profile_limit: 5
    min_failures: 1
`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	cfg := testkit.NewTestConfigFromYAML(t, path)
	rt, err := platform.OpenWithModules(ctx, cfg, OntologyModule())
	if err != nil {
		t.Fatalf("open runtime: %v", err)
	}
	t.Cleanup(func() { _ = rt.Close() })
	if err := rt.Initialize(ctx); err != nil {
		t.Fatalf("initialize runtime: %v", err)
	}
	return rt
}
