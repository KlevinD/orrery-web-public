package ontology

import (
	"context"
	"strconv"
	"testing"
	"time"

	"orrery_v2/internal/platform/clock"
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/migration"
)

func TestEvaluateModelRunEvaluationTrendAlertRules(t *testing.T) {
	snapshot := time.Date(2026, 6, 21, 12, 0, 0, 0, time.UTC)
	page := ModelRunEvaluationTrendPage{
		Buckets: []ModelRunEvaluationTrendBucket{{
			TenantID:         "tenant-1",
			Engine:           "ontology_designer",
			Model:            "gpt-5",
			ModelVersion:     "2026-06-21",
			RunTotal:         10,
			BlockingTotal:    2,
			GateWarningTotal: 3,
			StatusCounts: map[ModelRunEvaluationGateStatus]int{
				ModelRunEvaluationGateBlocked: 2,
			},
			ReasonCounts: map[ModelRunEvaluationReasonCode]int{
				ModelRunEvaluationReasonBlockingIssues: 2,
			},
			WarningCounts: map[ModelRunEvaluationReasonCode]int{
				ModelRunEvaluationReasonMetricThresholdFailed: 3,
			},
			MetricThresholdResultCounts: map[string]map[ModelRunEvaluationThresholdResultStatus]int{
				"accuracy-release-gate": {
					ModelRunEvaluationThresholdResultFailed: 2,
				},
				"drift-warning": {
					ModelRunEvaluationThresholdResultFailed: 1,
				},
			},
		}},
		SnapshotRecordedTo: snapshot,
	}

	results := EvaluateModelRunEvaluationTrendAlertRules(page, []ModelRunEvaluationTrendAlertRule{{
		RuleID:      "ontology.blocking-runs",
		DisplayName: "Blocking model runs",
		Severity:    ModelRunEvaluationThresholdSeverityCritical,
		Metric:      ModelRunEvaluationTrendAlertMetricBlockingTotal,
		Operator:    ModelRunEvaluationMetricOperatorGTE,
		Threshold:   1,
		ReasonCode:  ModelRunEvaluationReasonBlockingIssues,
	}, {
		RuleID:    "ontology.warning-runs",
		Severity:  ModelRunEvaluationThresholdSeverityWarning,
		Metric:    ModelRunEvaluationTrendAlertMetricGateWarningTotal,
		Operator:  ModelRunEvaluationMetricOperatorGT,
		Threshold: 10,
	}, {
		RuleID:                "accuracy-release-gate.trend_failure",
		Severity:              ModelRunEvaluationThresholdSeverityWarning,
		Metric:                ModelRunEvaluationTrendAlertMetricThresholdResultTotal,
		MetricThresholdRuleID: "accuracy-release-gate",
		ThresholdStatus:       ModelRunEvaluationThresholdResultFailed,
		Operator:              ModelRunEvaluationMetricOperatorGTE,
		Threshold:             2,
		WarningCode:           ModelRunEvaluationReasonMetricThresholdFailed,
	}})

	if len(results) != 3 {
		t.Fatalf("results = %+v", results)
	}
	firing := results[0]
	if firing.RuleID != "ontology.blocking-runs" ||
		firing.Status != ModelRunEvaluationTrendAlertFiring ||
		firing.Severity != ModelRunEvaluationThresholdSeverityCritical ||
		firing.ObservedValue != 2 ||
		firing.Threshold != 1 ||
		firing.Labels["tenant_id"] != "tenant-1" ||
		firing.Labels["engine"] != "ontology_designer" ||
		firing.Labels["reason"] != "blocking_issues" ||
		firing.Labels["snapshot_recorded_to"] != snapshot.Format(time.RFC3339Nano) {
		t.Fatalf("firing result = %+v", firing)
	}
	passing := results[1]
	if passing.RuleID != "ontology.warning-runs" ||
		passing.Status != ModelRunEvaluationTrendAlertResolved ||
		passing.ObservedValue != 3 {
		t.Fatalf("passing result = %+v", passing)
	}
	threshold := results[2]
	if threshold.RuleID != "accuracy-release-gate.trend_failure" ||
		threshold.Status != ModelRunEvaluationTrendAlertFiring ||
		threshold.ObservedValue != 2 ||
		threshold.Labels["metric_threshold_rule_id"] != "accuracy-release-gate" ||
		threshold.Labels["threshold_status"] != "failed" ||
		threshold.Labels["warning"] != "metric_threshold_failed" {
		t.Fatalf("threshold result = %+v", threshold)
	}
}

func TestBuildModelRunEvaluationTrendAlertRulesFromPolicyProfile(t *testing.T) {
	rules := BuildModelRunEvaluationTrendAlertRulesFromPolicyProfile(ModelRunEvaluationPolicyProfile{
		TenantID: "tenant-1",
		Engine:   "ontology_designer",
		MetricThresholds: []ModelRunEvaluationMetricThreshold{{
			RuleID:      "accuracy-release-gate",
			DisplayName: "Accuracy release gate",
			MetricKey:   "accuracy",
			Operator:    ModelRunEvaluationMetricOperatorGTE,
			Threshold:   0.95,
			Blocking:    true,
			Severity:    ModelRunEvaluationThresholdSeverityCritical,
		}, {
			RuleID:      "drift-warning",
			DisplayName: "Drift warning",
			MetricKey:   "drift_score",
			Operator:    ModelRunEvaluationMetricOperatorLTE,
			Threshold:   0.2,
			Blocking:    false,
			Severity:    ModelRunEvaluationThresholdSeverityWarning,
		}},
	}, 1)

	if len(rules) != 2 {
		t.Fatalf("rules = %+v", rules)
	}
	blocking := rules[0]
	if blocking.RuleID != "accuracy-release-gate.trend_failure" ||
		blocking.DisplayName != "Accuracy release gate trend failures" ||
		blocking.Metric != ModelRunEvaluationTrendAlertMetricThresholdResultTotal ||
		blocking.MetricThresholdRuleID != "accuracy-release-gate" ||
		blocking.ThresholdStatus != ModelRunEvaluationThresholdResultFailed ||
		blocking.Operator != ModelRunEvaluationMetricOperatorGTE ||
		blocking.Threshold != 1 ||
		blocking.Severity != ModelRunEvaluationThresholdSeverityCritical ||
		blocking.ReasonCode != ModelRunEvaluationReasonMetricThresholdFailed {
		t.Fatalf("blocking rule = %+v", blocking)
	}
	warning := rules[1]
	if warning.RuleID != "drift-warning.trend_failure" ||
		warning.DisplayName != "Drift warning trend failures" ||
		warning.Metric != ModelRunEvaluationTrendAlertMetricThresholdResultTotal ||
		warning.MetricThresholdRuleID != "drift-warning" ||
		warning.ThresholdStatus != ModelRunEvaluationThresholdResultFailed ||
		warning.Operator != ModelRunEvaluationMetricOperatorGTE ||
		warning.Threshold != 1 ||
		warning.Severity != ModelRunEvaluationThresholdSeverityWarning ||
		warning.WarningCode != ModelRunEvaluationReasonMetricThresholdFailed {
		t.Fatalf("warning rule = %+v", warning)
	}
}

func TestBuildModelRunEvaluationTrendAlertAuditEvents(t *testing.T) {
	observedAt := time.Date(2026, 6, 22, 9, 0, 0, 0, time.UTC)
	results := []ModelRunEvaluationTrendAlertResult{{
		RuleID:        "ontology.blocking-runs",
		DisplayName:   "Blocking model runs",
		Severity:      ModelRunEvaluationThresholdSeverityCritical,
		Status:        ModelRunEvaluationTrendAlertFiring,
		Metric:        ModelRunEvaluationTrendAlertMetricBlockingTotal,
		Operator:      ModelRunEvaluationMetricOperatorGTE,
		Threshold:     1,
		ObservedValue: 2,
		Labels: map[string]string{
			"tenant_id":                "tenant-1",
			"engine":                   "ontology_designer",
			"model":                    "gpt-5",
			"model_version":            "2026-06-21",
			"reason":                   "blocking_issues",
			"metric_threshold_rule_id": "accuracy-release-gate",
			"threshold_status":         "failed",
			"snapshot_recorded_to":     "2026-06-21T12:00:00Z",
		},
	}}

	events := BuildModelRunEvaluationTrendAlertAuditEvents(results, observedAt)
	if len(events) != 1 {
		t.Fatalf("events = %+v", events)
	}
	event := events[0]
	if event.ID == "" ||
		event.EventType != "ontology.model_run_evaluation_alert.firing" ||
		event.RuleID != "ontology.blocking-runs" ||
		event.TenantID != "tenant-1" ||
		event.Severity != ModelRunEvaluationThresholdSeverityCritical ||
		event.Status != ModelRunEvaluationTrendAlertFiring ||
		!event.ObservedAt.Equal(observedAt) ||
		event.Labels["engine"] != "ontology_designer" ||
		event.Details["metric"] != "blocking_total" ||
		event.Details["operator"] != "gte" ||
		event.Details["threshold"] != "1" ||
		event.Details["observed_value"] != "2" ||
		event.Details["metric_threshold_rule_id"] != "accuracy-release-gate" ||
		event.Details["threshold_status"] != "failed" ||
		event.Details["display_name"] != "Blocking model runs" {
		t.Fatalf("event = %+v", event)
	}
}

func TestRecordModelRunEvaluationTrendAlertAudits(t *testing.T) {
	ctx := context.Background()
	observedAt := time.Date(2026, 6, 22, 9, 30, 0, 0, time.UTC)
	ledger := NewMemoryModelRunLedger()
	artifacts := NewMemoryModelRunEvaluationArtifactStore()
	policies := NewMemoryModelRunEvaluationPolicyProfileStore()
	store := NewMemoryModelRunEvaluationTrendAlertAuditStore()

	if err := ledger.SaveModelRun(ctx, OntologyModelRun{
		RunID:                 "run-alert-audit-1",
		TenantID:              "tenant-1",
		Engine:                "ontology_designer",
		Model:                 "gpt-5",
		ModelVersion:          "2026-06-21",
		InputHash:             "sha256:input",
		PromptHash:            "sha256:prompt",
		RecordedAt:            observedAt.Add(-time.Hour),
		Status:                ModelRunStatusSucceeded,
		ProposalCount:         1,
		AnalysisTotal:         1,
		AnalysisBlockingCount: 1,
	}); err != nil {
		t.Fatalf("SaveModelRun: %v", err)
	}

	recorded, page, err := RecordModelRunEvaluationTrendAlertAudits(ctx, ledger, artifacts, policies, store, ModelRunEvaluationTrendQuery{
		TenantID:     "tenant-1",
		RecordedFrom: observedAt.Add(-2 * time.Hour),
		RecordedTo:   observedAt,
		Limit:        10,
	}, []ModelRunEvaluationTrendAlertRule{{
		RuleID:     "ontology.blocking-runs",
		Severity:   ModelRunEvaluationThresholdSeverityCritical,
		Metric:     ModelRunEvaluationTrendAlertMetricBlockingTotal,
		Operator:   ModelRunEvaluationMetricOperatorGTE,
		Threshold:  1,
		ReasonCode: ModelRunEvaluationReasonBlockingIssues,
	}}, observedAt)
	if err != nil {
		t.Fatalf("RecordModelRunEvaluationTrendAlertAudits: %v", err)
	}
	if recorded != 1 || len(page.Buckets) != 1 {
		t.Fatalf("recorded=%d page=%+v", recorded, page)
	}

	audits, err := store.ListModelRunEvaluationTrendAlertAuditEvents(ctx, ModelRunEvaluationTrendAlertAuditQuery{
		TenantID: "tenant-1",
		Status:   ModelRunEvaluationTrendAlertFiring,
		Limit:    10,
	})
	if err != nil {
		t.Fatalf("ListModelRunEvaluationTrendAlertAuditEvents: %v", err)
	}
	if audits.Page.Total != 1 || len(audits.Events) != 1 {
		t.Fatalf("audits = %+v", audits)
	}
	event := audits.Events[0]
	if event.RuleID != "ontology.blocking-runs" ||
		event.TenantID != "tenant-1" ||
		event.Status != ModelRunEvaluationTrendAlertFiring ||
		event.Details["observed_value"] != "1" ||
		event.Labels["snapshot_recorded_to"] != observedAt.Format(time.RFC3339Nano) {
		t.Fatalf("event = %+v", event)
	}
}

func TestRecordModelRunEvaluationTrendAlertAuditsWithPolicyDerivedRules(t *testing.T) {
	ctx := context.Background()
	observedAt := time.Date(2026, 6, 22, 10, 0, 0, 0, time.UTC)
	ledger := NewMemoryModelRunLedger()
	artifacts := NewMemoryModelRunEvaluationArtifactStore()
	policies := NewMemoryModelRunEvaluationPolicyProfileStore()
	store := NewMemoryModelRunEvaluationTrendAlertAuditStore()
	profile := ModelRunEvaluationPolicyProfile{
		TenantID:              "tenant-1",
		Engine:                "ontology_designer",
		Model:                 "gpt-5",
		ModelVersion:          "2026-06-21",
		RequiredArtifactKind:  "evaluation_report",
		RequiredDatasetRef:    "ontology-eval-20260621",
		RequiredPolicyVersion: "v1",
		MetricThresholds: []ModelRunEvaluationMetricThreshold{{
			RuleID:      "accuracy-warning",
			DisplayName: "Accuracy warning",
			MetricKey:   "accuracy",
			Operator:    ModelRunEvaluationMetricOperatorGTE,
			Threshold:   0.95,
			Blocking:    false,
			Severity:    ModelRunEvaluationThresholdSeverityWarning,
		}},
		Enabled: true,
	}

	if err := ledger.SaveModelRun(ctx, OntologyModelRun{
		RunID:        "run-policy-derived-alert-1",
		TenantID:     "tenant-1",
		Engine:       "ontology_designer",
		Model:        "gpt-5",
		ModelVersion: "2026-06-21",
		InputHash:    "sha256:input",
		PromptHash:   "sha256:prompt",
		RecordedAt:   observedAt.Add(-time.Hour),
		Status:       ModelRunStatusSucceeded,
	}); err != nil {
		t.Fatalf("SaveModelRun: %v", err)
	}
	if err := policies.SaveModelRunEvaluationPolicyProfile(ctx, profile); err != nil {
		t.Fatalf("SaveModelRunEvaluationPolicyProfile: %v", err)
	}
	if err := artifacts.SaveModelRunEvaluationArtifact(ctx, ModelRunEvaluationArtifact{
		TenantID:     "tenant-1",
		Engine:       "ontology_designer",
		RunID:        "run-policy-derived-alert-1",
		ArtifactKind: "evaluation_report",
		ArtifactURI:  "s3://orrery-eval/run-policy-derived-alert-1/report.json",
		Metadata: map[string]string{
			"dataset_ref":    "ontology-eval-20260621",
			"policy_version": "v1",
			"accuracy":       "0.91",
		},
	}); err != nil {
		t.Fatalf("SaveModelRunEvaluationArtifact: %v", err)
	}

	recorded, _, err := RecordModelRunEvaluationTrendAlertAudits(ctx, ledger, artifacts, policies, store, ModelRunEvaluationTrendQuery{
		TenantID:     "tenant-1",
		Engine:       "ontology_designer",
		RecordedFrom: observedAt.Add(-2 * time.Hour),
		RecordedTo:   observedAt,
		Limit:        10,
	}, BuildModelRunEvaluationTrendAlertRulesFromPolicyProfile(profile, 1), observedAt)
	if err != nil {
		t.Fatalf("RecordModelRunEvaluationTrendAlertAudits: %v", err)
	}
	if recorded != 1 {
		t.Fatalf("recorded = %d", recorded)
	}
	audits, err := store.ListModelRunEvaluationTrendAlertAuditEvents(ctx, ModelRunEvaluationTrendAlertAuditQuery{
		TenantID: "tenant-1",
		RuleID:   "accuracy-warning.trend_failure",
		Status:   ModelRunEvaluationTrendAlertFiring,
		Limit:    10,
	})
	if err != nil {
		t.Fatalf("ListModelRunEvaluationTrendAlertAuditEvents: %v", err)
	}
	if audits.Page.Total != 1 || audits.Events[0].Labels["warning"] != "metric_threshold_failed" ||
		audits.Events[0].Labels["metric_threshold_rule_id"] != "accuracy-warning" ||
		audits.Events[0].Labels["threshold_status"] != "failed" ||
		audits.Events[0].Details["display_name"] != "Accuracy warning trend failures" {
		t.Fatalf("audits = %+v", audits)
	}
}

func TestModelRunEvaluationTrendAlertAuditSweeperRecordsRollingWindowAudits(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 6, 22, 11, 0, 0, 0, time.UTC)
	ledger := NewMemoryModelRunLedger()
	artifacts := NewMemoryModelRunEvaluationArtifactStore()
	policies := NewMemoryModelRunEvaluationPolicyProfileStore()
	store := NewMemoryModelRunEvaluationTrendAlertAuditStore()
	if err := ledger.SaveModelRun(ctx, OntologyModelRun{
		RunID:                 "run-alert-sweeper-1",
		TenantID:              "tenant-1",
		Engine:                "ontology_designer",
		Model:                 "gpt-5",
		ModelVersion:          "2026-06-21",
		InputHash:             "sha256:input",
		PromptHash:            "sha256:prompt",
		RecordedAt:            now.Add(-30 * time.Minute),
		Status:                ModelRunStatusSucceeded,
		ProposalCount:         1,
		AnalysisTotal:         1,
		AnalysisBlockingCount: 1,
	}); err != nil {
		t.Fatalf("SaveModelRun: %v", err)
	}
	sweeper := NewModelRunEvaluationTrendAlertAuditSweeper(ModelRunEvaluationTrendAlertAuditSweeperOptions{
		Ledger:    ledger,
		Artifacts: artifacts,
		Policies:  policies,
		Store:     store,
		Clock:     clock.NewFake(now),
		Lookback:  time.Hour,
		Query: ModelRunEvaluationTrendQuery{
			TenantID: "tenant-1",
			Limit:    10,
		},
		Rules: []ModelRunEvaluationTrendAlertRule{{
			RuleID:     "ontology.blocking-runs",
			Severity:   ModelRunEvaluationThresholdSeverityCritical,
			Metric:     ModelRunEvaluationTrendAlertMetricBlockingTotal,
			Operator:   ModelRunEvaluationMetricOperatorGTE,
			Threshold:  1,
			ReasonCode: ModelRunEvaluationReasonBlockingIssues,
		}},
	})

	if sweeper.Name() != "ontology.model_run_evaluation_alert_audit.recorder" {
		t.Fatalf("sweeper name = %q", sweeper.Name())
	}
	if err := sweeper.Sweep(ctx); err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if report := sweeper.LastReport(); report.Recorded != 1 || report.Buckets != 1 {
		t.Fatalf("last report = %+v", report)
	}
	audits, err := store.ListModelRunEvaluationTrendAlertAuditEvents(ctx, ModelRunEvaluationTrendAlertAuditQuery{
		TenantID: "tenant-1",
		Status:   ModelRunEvaluationTrendAlertFiring,
		Limit:    10,
	})
	if err != nil {
		t.Fatalf("ListModelRunEvaluationTrendAlertAuditEvents: %v", err)
	}
	if audits.Page.Total != 1 || !audits.Events[0].ObservedAt.Equal(now) {
		t.Fatalf("audits = %+v", audits)
	}
}

func TestModelRunEvaluationTrendPolicyAlertAuditSweeperRecordsPolicyDerivedRules(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 6, 22, 12, 0, 0, 0, time.UTC)
	ledger := NewMemoryModelRunLedger()
	artifacts := NewMemoryModelRunEvaluationArtifactStore()
	policies := NewMemoryModelRunEvaluationPolicyProfileStore()
	store := NewMemoryModelRunEvaluationTrendAlertAuditStore()
	profile := ModelRunEvaluationPolicyProfile{
		TenantID:              "tenant-1",
		Engine:                "ontology_designer",
		Model:                 "gpt-5",
		ModelVersion:          "2026-06-21",
		RequiredArtifactKind:  "evaluation_report",
		RequiredDatasetRef:    "ontology-eval-20260621",
		RequiredPolicyVersion: "v1",
		MetricThresholds: []ModelRunEvaluationMetricThreshold{{
			RuleID:      "accuracy-warning",
			DisplayName: "Accuracy warning",
			MetricKey:   "accuracy",
			Operator:    ModelRunEvaluationMetricOperatorGTE,
			Threshold:   0.95,
			Blocking:    false,
			Severity:    ModelRunEvaluationThresholdSeverityWarning,
		}},
		Enabled: true,
	}

	if err := policies.SaveModelRunEvaluationPolicyProfile(ctx, profile); err != nil {
		t.Fatalf("SaveModelRunEvaluationPolicyProfile: %v", err)
	}
	if err := ledger.SaveModelRun(ctx, OntologyModelRun{
		RunID:        "run-policy-alert-sweeper-1",
		TenantID:     "tenant-1",
		Engine:       "ontology_designer",
		Model:        "gpt-5",
		ModelVersion: "2026-06-21",
		InputHash:    "sha256:input",
		PromptHash:   "sha256:prompt",
		RecordedAt:   now.Add(-30 * time.Minute),
		Status:       ModelRunStatusSucceeded,
	}); err != nil {
		t.Fatalf("SaveModelRun: %v", err)
	}
	if err := artifacts.SaveModelRunEvaluationArtifact(ctx, ModelRunEvaluationArtifact{
		TenantID:     "tenant-1",
		Engine:       "ontology_designer",
		RunID:        "run-policy-alert-sweeper-1",
		ArtifactKind: "evaluation_report",
		ArtifactURI:  "s3://orrery-eval/run-policy-alert-sweeper-1/report.json",
		Metadata: map[string]string{
			"dataset_ref":    "ontology-eval-20260621",
			"policy_version": "v1",
			"accuracy":       "0.91",
		},
	}); err != nil {
		t.Fatalf("SaveModelRunEvaluationArtifact: %v", err)
	}

	sweeper := NewModelRunEvaluationTrendPolicyAlertAuditSweeper(ModelRunEvaluationTrendPolicyAlertAuditSweeperOptions{
		Ledger:    ledger,
		Artifacts: artifacts,
		Policies:  policies,
		Store:     store,
		Clock:     clock.NewFake(now),
		Lookback:  time.Hour,
		Query: ModelRunEvaluationTrendQuery{
			TenantID: "tenant-1",
			Limit:    10,
		},
		ProfileQuery: ModelRunEvaluationPolicyProfileQuery{
			TenantID: "tenant-1",
			Engine:   "ontology_designer",
			Limit:    10,
		},
		MinFailures: 1,
	})

	if sweeper.Name() != "ontology.model_run_evaluation_policy_alert_audit.recorder" {
		t.Fatalf("sweeper name = %q", sweeper.Name())
	}
	if err := sweeper.Sweep(ctx); err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if report := sweeper.LastReport(); report.Profiles != 1 || report.Recorded != 1 || report.Buckets != 1 {
		t.Fatalf("last report = %+v", report)
	}
	audits, err := store.ListModelRunEvaluationTrendAlertAuditEvents(ctx, ModelRunEvaluationTrendAlertAuditQuery{
		TenantID: "tenant-1",
		RuleID:   "accuracy-warning.trend_failure",
		Status:   ModelRunEvaluationTrendAlertFiring,
		Limit:    10,
	})
	if err != nil {
		t.Fatalf("ListModelRunEvaluationTrendAlertAuditEvents: %v", err)
	}
	if audits.Page.Total != 1 ||
		audits.Events[0].Labels["metric_threshold_rule_id"] != "accuracy-warning" ||
		audits.Events[0].Labels["threshold_status"] != "failed" ||
		audits.Events[0].Labels["warning"] != "metric_threshold_failed" ||
		!audits.Events[0].ObservedAt.Equal(now) {
		t.Fatalf("audits = %+v", audits)
	}
}

func TestModelRunEvaluationTrendPolicyAlertAuditSweeperPaginatesUntilProfileBucket(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 6, 22, 12, 30, 0, 0, time.UTC)
	ledger := NewMemoryModelRunLedger()
	artifacts := NewMemoryModelRunEvaluationArtifactStore()
	policies := NewMemoryModelRunEvaluationPolicyProfileStore()
	store := NewMemoryModelRunEvaluationTrendAlertAuditStore()
	if err := ledger.SaveModelRun(ctx, OntologyModelRun{
		RunID:        "run-policy-alert-page-a",
		TenantID:     "tenant-1",
		Engine:       "ontology_designer",
		Model:        "alpha-model",
		ModelVersion: "2026-06-21",
		InputHash:    "sha256:alpha-input",
		PromptHash:   "sha256:alpha-prompt",
		RecordedAt:   now.Add(-20 * time.Minute),
		Status:       ModelRunStatusSucceeded,
	}); err != nil {
		t.Fatalf("SaveModelRun alpha: %v", err)
	}
	if err := ledger.SaveModelRun(ctx, OntologyModelRun{
		RunID:        "run-policy-alert-page-z",
		TenantID:     "tenant-1",
		Engine:       "ontology_designer",
		Model:        "zeta-model",
		ModelVersion: "2026-06-21",
		InputHash:    "sha256:zeta-input",
		PromptHash:   "sha256:zeta-prompt",
		RecordedAt:   now.Add(-10 * time.Minute),
		Status:       ModelRunStatusSucceeded,
	}); err != nil {
		t.Fatalf("SaveModelRun zeta: %v", err)
	}
	if err := policies.SaveModelRunEvaluationPolicyProfile(ctx, ModelRunEvaluationPolicyProfile{
		TenantID:              "tenant-1",
		Engine:                "ontology_designer",
		Model:                 "zeta-model",
		ModelVersion:          "2026-06-21",
		RequiredArtifactKind:  "evaluation_report",
		RequiredDatasetRef:    "ontology-eval-20260621",
		RequiredPolicyVersion: "v1",
		MetricThresholds: []ModelRunEvaluationMetricThreshold{{
			RuleID:    "accuracy-warning",
			MetricKey: "accuracy",
			Operator:  ModelRunEvaluationMetricOperatorGTE,
			Threshold: 0.95,
			Severity:  ModelRunEvaluationThresholdSeverityWarning,
		}},
		Enabled: true,
	}); err != nil {
		t.Fatalf("SaveModelRunEvaluationPolicyProfile: %v", err)
	}
	if err := artifacts.SaveModelRunEvaluationArtifact(ctx, ModelRunEvaluationArtifact{
		TenantID:     "tenant-1",
		Engine:       "ontology_designer",
		RunID:        "run-policy-alert-page-z",
		ArtifactKind: "evaluation_report",
		ArtifactURI:  "s3://orrery-eval/run-policy-alert-page-z/report.json",
		Metadata: map[string]string{
			"dataset_ref":    "ontology-eval-20260621",
			"policy_version": "v1",
			"accuracy":       "0.91",
		},
	}); err != nil {
		t.Fatalf("SaveModelRunEvaluationArtifact: %v", err)
	}
	sweeper := NewModelRunEvaluationTrendPolicyAlertAuditSweeper(ModelRunEvaluationTrendPolicyAlertAuditSweeperOptions{
		Ledger:    ledger,
		Artifacts: artifacts,
		Policies:  policies,
		Store:     store,
		Clock:     clock.NewFake(now),
		Lookback:  time.Hour,
		Query: ModelRunEvaluationTrendQuery{
			TenantID: "tenant-1",
			Engine:   "ontology_designer",
			Limit:    1,
		},
		ProfileQuery: ModelRunEvaluationPolicyProfileQuery{
			TenantID: "tenant-1",
			Engine:   "ontology_designer",
			Limit:    10,
		},
		MinFailures: 1,
	})

	if err := sweeper.Sweep(ctx); err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if report := sweeper.LastReport(); report.Recorded != 1 || report.Buckets != 1 {
		t.Fatalf("last report = %+v", report)
	}
	audits, err := store.ListModelRunEvaluationTrendAlertAuditEvents(ctx, ModelRunEvaluationTrendAlertAuditQuery{
		TenantID: "tenant-1",
		RuleID:   "accuracy-warning.trend_failure",
		Status:   ModelRunEvaluationTrendAlertFiring,
		Limit:    10,
	})
	if err != nil {
		t.Fatalf("ListModelRunEvaluationTrendAlertAuditEvents: %v", err)
	}
	if audits.Page.Total != 1 || audits.Events[0].Labels["model"] != "zeta-model" {
		t.Fatalf("audits = %+v", audits)
	}
}

func TestModelRunEvaluationTrendPolicyAlertAuditSweeperCollectsAllWildcardProfileBuckets(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 6, 22, 13, 0, 0, 0, time.UTC)
	ledger := NewMemoryModelRunLedger()
	artifacts := NewMemoryModelRunEvaluationArtifactStore()
	policies := NewMemoryModelRunEvaluationPolicyProfileStore()
	store := NewMemoryModelRunEvaluationTrendAlertAuditStore()
	for _, run := range []OntologyModelRun{{
		RunID:        "run-policy-alert-wildcard-a",
		TenantID:     "tenant-1",
		Engine:       "ontology_designer",
		Model:        "alpha-model",
		ModelVersion: "2026-06-21",
		InputHash:    "sha256:alpha-input",
		PromptHash:   "sha256:alpha-prompt",
		RecordedAt:   now.Add(-20 * time.Minute),
		Status:       ModelRunStatusSucceeded,
	}, {
		RunID:        "run-policy-alert-wildcard-z",
		TenantID:     "tenant-1",
		Engine:       "ontology_designer",
		Model:        "zeta-model",
		ModelVersion: "2026-06-21",
		InputHash:    "sha256:zeta-input",
		PromptHash:   "sha256:zeta-prompt",
		RecordedAt:   now.Add(-10 * time.Minute),
		Status:       ModelRunStatusSucceeded,
	}} {
		if err := ledger.SaveModelRun(ctx, run); err != nil {
			t.Fatalf("SaveModelRun(%s): %v", run.RunID, err)
		}
		if err := artifacts.SaveModelRunEvaluationArtifact(ctx, ModelRunEvaluationArtifact{
			TenantID:     run.TenantID,
			Engine:       run.Engine,
			RunID:        run.RunID,
			ArtifactKind: "evaluation_report",
			ArtifactURI:  "s3://orrery-eval/" + run.RunID + "/report.json",
			Metadata: map[string]string{
				"dataset_ref":    "ontology-eval-20260621",
				"policy_version": "v1",
				"accuracy":       "0.91",
			},
		}); err != nil {
			t.Fatalf("SaveModelRunEvaluationArtifact(%s): %v", run.RunID, err)
		}
	}
	if err := policies.SaveModelRunEvaluationPolicyProfile(ctx, ModelRunEvaluationPolicyProfile{
		TenantID:              "tenant-1",
		Engine:                "ontology_designer",
		RequiredArtifactKind:  "evaluation_report",
		RequiredDatasetRef:    "ontology-eval-20260621",
		RequiredPolicyVersion: "v1",
		MetricThresholds: []ModelRunEvaluationMetricThreshold{{
			RuleID:    "accuracy-warning",
			MetricKey: "accuracy",
			Operator:  ModelRunEvaluationMetricOperatorGTE,
			Threshold: 0.95,
			Severity:  ModelRunEvaluationThresholdSeverityWarning,
		}},
		Enabled: true,
	}); err != nil {
		t.Fatalf("SaveModelRunEvaluationPolicyProfile: %v", err)
	}
	sweeper := NewModelRunEvaluationTrendPolicyAlertAuditSweeper(ModelRunEvaluationTrendPolicyAlertAuditSweeperOptions{
		Ledger:    ledger,
		Artifacts: artifacts,
		Policies:  policies,
		Store:     store,
		Clock:     clock.NewFake(now),
		Lookback:  time.Hour,
		Query: ModelRunEvaluationTrendQuery{
			TenantID: "tenant-1",
			Engine:   "ontology_designer",
			Limit:    1,
		},
		ProfileQuery: ModelRunEvaluationPolicyProfileQuery{
			TenantID: "tenant-1",
			Engine:   "ontology_designer",
			Limit:    10,
		},
		MinFailures: 1,
	})

	if err := sweeper.Sweep(ctx); err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if report := sweeper.LastReport(); report.Recorded != 2 || report.Buckets != 2 {
		t.Fatalf("last report = %+v", report)
	}
	audits, err := store.ListModelRunEvaluationTrendAlertAuditEvents(ctx, ModelRunEvaluationTrendAlertAuditQuery{
		TenantID: "tenant-1",
		RuleID:   "accuracy-warning.trend_failure",
		Status:   ModelRunEvaluationTrendAlertFiring,
		Limit:    10,
	})
	if err != nil {
		t.Fatalf("ListModelRunEvaluationTrendAlertAuditEvents: %v", err)
	}
	if audits.Page.Total != 2 {
		t.Fatalf("audits = %+v", audits)
	}
}

func TestMemoryModelRunEvaluationTrendAlertAuditStoreSaveAndList(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryModelRunEvaluationTrendAlertAuditStore()
	observedAt := time.Date(2026, 6, 22, 9, 0, 0, 0, time.UTC)

	if err := store.SaveModelRunEvaluationTrendAlertAuditEvent(ctx, ModelRunEvaluationTrendAlertAuditEvent{
		ID:        "alert-audit-1",
		EventType: "ontology.model_run_evaluation_alert.firing",
		TenantID:  "tenant-1",
		RuleID:    "ontology.blocking-runs",
		Severity:  ModelRunEvaluationThresholdSeverityCritical,
		Status:    ModelRunEvaluationTrendAlertFiring,
		Labels: map[string]string{
			"engine":        "ontology_designer",
			"model":         "gpt-5",
			"model_version": "2026-06-21",
		},
		Details: map[string]string{
			"metric":         "blocking_total",
			"observed_value": "2",
		},
		ObservedAt: observedAt,
	}); err != nil {
		t.Fatalf("SaveModelRunEvaluationTrendAlertAuditEvent: %v", err)
	}
	if err := store.SaveModelRunEvaluationTrendAlertAuditEvent(ctx, ModelRunEvaluationTrendAlertAuditEvent{
		ID:         "alert-audit-tenant-2",
		EventType:  "ontology.model_run_evaluation_alert.resolved",
		TenantID:   "tenant-2",
		RuleID:     "ontology.blocking-runs",
		Status:     ModelRunEvaluationTrendAlertResolved,
		ObservedAt: observedAt.Add(time.Minute),
	}); err != nil {
		t.Fatalf("SaveModelRunEvaluationTrendAlertAuditEvent tenant-2: %v", err)
	}

	page, err := store.ListModelRunEvaluationTrendAlertAuditEvents(ctx, ModelRunEvaluationTrendAlertAuditQuery{
		TenantID: "tenant-1",
		Status:   ModelRunEvaluationTrendAlertFiring,
		Limit:    10,
	})
	if err != nil {
		t.Fatalf("ListModelRunEvaluationTrendAlertAuditEvents: %v", err)
	}
	if page.Page.Total != 1 || len(page.Events) != 1 {
		t.Fatalf("page = %+v", page)
	}
	event := page.Events[0]
	if event.ID != "alert-audit-1" ||
		event.Labels["engine"] != "ontology_designer" ||
		event.Details["metric"] != "blocking_total" ||
		!event.ObservedAt.Equal(observedAt) {
		t.Fatalf("event = %+v", event)
	}
}

func TestModelRunEvaluationTrendAlertAuditStoreFiltersObservedWindow(t *testing.T) {
	ctx := context.Background()
	from := time.Date(2026, 6, 22, 9, 0, 0, 0, time.UTC)
	to := from.Add(time.Hour)
	for name, store := range map[string]ModelRunEvaluationTrendAlertAuditStore{
		"memory":   NewMemoryModelRunEvaluationTrendAlertAuditStore(),
		"platform": NewPlatformModelRunEvaluationTrendAlertAuditStore(newMigratedAlertAuditCore(t, "ontology-alert-audit-window")),
	} {
		t.Run(name, func(t *testing.T) {
			for _, observedAt := range []time.Time{
				from.Add(-time.Nanosecond),
				from.Add(30 * time.Minute),
				to,
			} {
				if err := store.SaveModelRunEvaluationTrendAlertAuditEvent(ctx, ModelRunEvaluationTrendAlertAuditEvent{
					EventType:  "ontology.model_run_evaluation_alert.firing",
					TenantID:   "tenant-1",
					RuleID:     "accuracy-warning.trend_failure",
					Status:     ModelRunEvaluationTrendAlertFiring,
					ObservedAt: observedAt,
					Labels: map[string]string{
						"tenant_id": "tenant-1",
						"engine":    "ontology_designer",
					},
					Details: map[string]string{"observed_value": "1"},
				}); err != nil {
					t.Fatalf("SaveModelRunEvaluationTrendAlertAuditEvent %s: %v", observedAt, err)
				}
			}
			page, err := store.ListModelRunEvaluationTrendAlertAuditEvents(ctx, ModelRunEvaluationTrendAlertAuditQuery{
				TenantID:     "tenant-1",
				ObservedFrom: from,
				ObservedTo:   to,
				Limit:        10,
			})
			if err != nil {
				t.Fatalf("ListModelRunEvaluationTrendAlertAuditEvents: %v", err)
			}
			if page.Page.Total != 1 || len(page.Events) != 1 || !page.Events[0].ObservedAt.Equal(from.Add(30*time.Minute)) {
				t.Fatalf("page = %+v", page)
			}
		})
	}
}

func TestModelRunEvaluationTrendAlertAuditStoreFiltersLabels(t *testing.T) {
	ctx := context.Background()
	observedAt := time.Date(2026, 6, 22, 9, 30, 0, 0, time.UTC)
	for name, store := range map[string]ModelRunEvaluationTrendAlertAuditStore{
		"memory":   NewMemoryModelRunEvaluationTrendAlertAuditStore(),
		"platform": NewPlatformModelRunEvaluationTrendAlertAuditStore(newMigratedAlertAuditCore(t, "ontology-alert-audit-label")),
	} {
		t.Run(name, func(t *testing.T) {
			for _, event := range []ModelRunEvaluationTrendAlertAuditEvent{
				{
					EventType:  "ontology.model_run_evaluation_alert.firing",
					TenantID:   "tenant-1",
					RuleID:     "accuracy-warning.trend_failure",
					Status:     ModelRunEvaluationTrendAlertFiring,
					ObservedAt: observedAt,
					Labels: map[string]string{
						"tenant_id":     "tenant-1",
						"engine":        "ontology_designer",
						"model_version": "2026-06-22",
					},
					Details: map[string]string{"observed_value": "1"},
				},
				{
					EventType:  "ontology.model_run_evaluation_alert.firing",
					TenantID:   "tenant-1",
					RuleID:     "accuracy-warning.trend_failure",
					Status:     ModelRunEvaluationTrendAlertFiring,
					ObservedAt: observedAt.Add(time.Minute),
					Labels: map[string]string{
						"tenant_id":     "tenant-1",
						"engine":        "batch_classifier",
						"model_version": "2026-06-22",
					},
					Details: map[string]string{"observed_value": "1"},
				},
			} {
				if err := store.SaveModelRunEvaluationTrendAlertAuditEvent(ctx, event); err != nil {
					t.Fatalf("SaveModelRunEvaluationTrendAlertAuditEvent: %v", err)
				}
			}
			page, err := store.ListModelRunEvaluationTrendAlertAuditEvents(ctx, ModelRunEvaluationTrendAlertAuditQuery{
				TenantID:   "tenant-1",
				LabelKey:   "engine",
				LabelValue: "ontology_designer",
				Limit:      10,
			})
			if err != nil {
				t.Fatalf("ListModelRunEvaluationTrendAlertAuditEvents: %v", err)
			}
			if page.Page.Total != 1 || len(page.Events) != 1 || page.Events[0].Labels["engine"] != "ontology_designer" {
				t.Fatalf("page = %+v", page)
			}
			statePage, err := store.ListModelRunEvaluationTrendAlertAuditCurrentStates(ctx, ModelRunEvaluationTrendAlertAuditQuery{
				TenantID:   "tenant-1",
				LabelKey:   "engine",
				LabelValue: "ontology_designer",
				Limit:      10,
			})
			if err != nil {
				t.Fatalf("ListModelRunEvaluationTrendAlertAuditCurrentStates: %v", err)
			}
			if statePage.Page.Total != 1 || len(statePage.States) != 1 || statePage.States[0].Labels["engine"] != "ontology_designer" {
				t.Fatalf("statePage = %+v", statePage)
			}
		})
	}
}

func TestModelRunEvaluationTrendAlertAuditStoreListCurrentStatesByLabelSet(t *testing.T) {
	ctx := context.Background()
	firstObservedAt := time.Date(2026, 6, 22, 9, 0, 0, 0, time.UTC)
	secondObservedAt := firstObservedAt.Add(10 * time.Minute)
	thirdObservedAt := firstObservedAt.Add(20 * time.Minute)
	for name, store := range map[string]ModelRunEvaluationTrendAlertAuditStore{
		"memory":   NewMemoryModelRunEvaluationTrendAlertAuditStore(),
		"platform": NewPlatformModelRunEvaluationTrendAlertAuditStore(newMigratedAlertAuditCore(t, "ontology-alert-audit-current-states")),
	} {
		t.Run(name, func(t *testing.T) {
			events := []ModelRunEvaluationTrendAlertAuditEvent{{
				EventType:  "ontology.model_run_evaluation_alert.firing",
				TenantID:   "tenant-1",
				RuleID:     "accuracy-release-gate.trend_failure",
				Status:     ModelRunEvaluationTrendAlertFiring,
				ObservedAt: firstObservedAt,
				Labels: map[string]string{
					"tenant_id":     "tenant-1",
					"engine":        "ontology_designer",
					"model":         "gpt-5",
					"model_version": "2026-06-21",
				},
				Details: map[string]string{"observed_value": "2"},
			}, {
				EventType:  "ontology.model_run_evaluation_alert.resolved",
				TenantID:   "tenant-1",
				RuleID:     "accuracy-release-gate.trend_failure",
				Status:     ModelRunEvaluationTrendAlertResolved,
				ObservedAt: secondObservedAt,
				Labels: map[string]string{
					"tenant_id":     "tenant-1",
					"engine":        "ontology_designer",
					"model":         "gpt-5",
					"model_version": "2026-06-21",
				},
				Details: map[string]string{"observed_value": "0"},
			}, {
				EventType:  "ontology.model_run_evaluation_alert.firing",
				TenantID:   "tenant-1",
				RuleID:     "accuracy-release-gate.trend_failure",
				Status:     ModelRunEvaluationTrendAlertFiring,
				ObservedAt: thirdObservedAt,
				Labels: map[string]string{
					"tenant_id":     "tenant-1",
					"engine":        "ontology_designer",
					"model":         "gpt-5",
					"model_version": "2026-06-22",
				},
				Details: map[string]string{"observed_value": "1"},
			}}
			for _, event := range events {
				if err := store.SaveModelRunEvaluationTrendAlertAuditEvent(ctx, event); err != nil {
					t.Fatalf("SaveModelRunEvaluationTrendAlertAuditEvent: %v", err)
				}
			}

			page, err := store.ListModelRunEvaluationTrendAlertAuditCurrentStates(ctx, ModelRunEvaluationTrendAlertAuditQuery{
				TenantID: "tenant-1",
				RuleID:   "accuracy-release-gate.trend_failure",
				Limit:    10,
			})
			if err != nil {
				t.Fatalf("ListModelRunEvaluationTrendAlertAuditCurrentStates: %v", err)
			}
			if page.Page.Total != 2 || len(page.States) != 2 {
				t.Fatalf("page = %+v", page)
			}
			if page.States[0].Labels["model_version"] != "2026-06-22" ||
				page.States[0].Status != ModelRunEvaluationTrendAlertFiring ||
				!page.States[0].ObservedAt.Equal(thirdObservedAt) {
				t.Fatalf("latest firing state = %+v", page.States[0])
			}
			if page.States[1].Labels["model_version"] != "2026-06-21" ||
				page.States[1].Status != ModelRunEvaluationTrendAlertResolved ||
				page.States[1].Details["observed_value"] != "0" ||
				!page.States[1].ObservedAt.Equal(secondObservedAt) {
				t.Fatalf("latest resolved state = %+v", page.States[1])
			}

			firing, err := store.ListModelRunEvaluationTrendAlertAuditCurrentStates(ctx, ModelRunEvaluationTrendAlertAuditQuery{
				TenantID: "tenant-1",
				Status:   ModelRunEvaluationTrendAlertFiring,
				Limit:    10,
			})
			if err != nil {
				t.Fatalf("ListModelRunEvaluationTrendAlertAuditCurrentStates firing: %v", err)
			}
			if firing.Page.Total != 1 || len(firing.States) != 1 || firing.States[0].Labels["model_version"] != "2026-06-22" {
				t.Fatalf("firing page = %+v", firing)
			}
		})
	}
}

func TestPlatformModelRunEvaluationTrendAlertAuditStoreCurrentStatesScansBeyondDefaultPage(t *testing.T) {
	ctx := context.Background()
	store := NewPlatformModelRunEvaluationTrendAlertAuditStore(newMigratedAlertAuditCore(t, "ontology-alert-audit-current-states-pages"))
	observedAt := time.Date(2026, 6, 22, 9, 0, 0, 0, time.UTC)
	const total = 101
	for idx := 0; idx < total; idx++ {
		if err := store.SaveModelRunEvaluationTrendAlertAuditEvent(ctx, ModelRunEvaluationTrendAlertAuditEvent{
			EventType:  "ontology.model_run_evaluation_alert.firing",
			TenantID:   "tenant-1",
			RuleID:     "accuracy-release-gate.trend_failure",
			Status:     ModelRunEvaluationTrendAlertFiring,
			ObservedAt: observedAt.Add(time.Duration(idx) * time.Minute),
			Labels: map[string]string{
				"tenant_id":     "tenant-1",
				"engine":        "ontology_designer",
				"model":         "gpt-5",
				"model_version": "2026-06-" + strconv.Itoa(100+idx),
			},
			Details: map[string]string{"observed_value": "1"},
		}); err != nil {
			t.Fatalf("SaveModelRunEvaluationTrendAlertAuditEvent %d: %v", idx, err)
		}
	}

	page, err := store.ListModelRunEvaluationTrendAlertAuditCurrentStates(ctx, ModelRunEvaluationTrendAlertAuditQuery{
		TenantID: "tenant-1",
		RuleID:   "accuracy-release-gate.trend_failure",
		Limit:    1,
		Offset:   100,
	})
	if err != nil {
		t.Fatalf("ListModelRunEvaluationTrendAlertAuditCurrentStates: %v", err)
	}
	if page.Page.Total != total || len(page.States) != 1 || page.States[0].Labels["model_version"] != "2026-06-100" {
		t.Fatalf("page = %+v", page)
	}
}

func TestPlatformModelRunEvaluationTrendAlertAuditStoreStatusCounts(t *testing.T) {
	ctx := context.Background()
	store := NewPlatformModelRunEvaluationTrendAlertAuditStore(newMigratedAlertAuditCore(t, "ontology-alert-audit-status-counts"))
	observedAt := time.Date(2026, 6, 22, 9, 0, 0, 0, time.UTC)
	for idx, status := range []ModelRunEvaluationTrendAlertStatus{
		ModelRunEvaluationTrendAlertFiring,
		ModelRunEvaluationTrendAlertFiring,
		ModelRunEvaluationTrendAlertResolved,
	} {
		if err := store.SaveModelRunEvaluationTrendAlertAuditEvent(ctx, ModelRunEvaluationTrendAlertAuditEvent{
			EventType:  "ontology.model_run_evaluation_alert." + string(status),
			TenantID:   "tenant-1",
			RuleID:     "accuracy-warning.trend_failure",
			Status:     status,
			ObservedAt: observedAt.Add(time.Duration(idx) * time.Minute),
			Labels:     map[string]string{"tenant_id": "tenant-1"},
			Details:    map[string]string{"observed_value": "1"},
		}); err != nil {
			t.Fatalf("SaveModelRunEvaluationTrendAlertAuditEvent %s: %v", status, err)
		}
	}

	counts, err := store.StatusCounts(ctx)
	if err != nil {
		t.Fatalf("StatusCounts: %v", err)
	}
	if counts[string(ModelRunEvaluationTrendAlertFiring)] != 2 || counts[string(ModelRunEvaluationTrendAlertResolved)] != 1 {
		t.Fatalf("counts = %+v", counts)
	}
}

func TestPlatformModelRunEvaluationTrendAlertAuditStoreSaveAndList(t *testing.T) {
	ctx := context.Background()
	core := newMigratedAlertAuditCore(t, "platform-ontology-alert-audit")
	store := NewPlatformModelRunEvaluationTrendAlertAuditStore(core)
	observedAt := time.Date(2026, 6, 22, 9, 15, 0, 0, time.UTC)

	if err := store.SaveModelRunEvaluationTrendAlertAuditEvent(ctx, ModelRunEvaluationTrendAlertAuditEvent{
		ID:        "alert-audit-platform-1",
		EventType: "ontology.model_run_evaluation_alert.firing",
		TenantID:  "tenant-1",
		RuleID:    "ontology.blocking-runs",
		Severity:  ModelRunEvaluationThresholdSeverityCritical,
		Status:    ModelRunEvaluationTrendAlertFiring,
		Labels: map[string]string{
			"engine":        "ontology_designer",
			"model":         "gpt-5",
			"model_version": "2026-06-21",
		},
		Details: map[string]string{
			"metric":         "blocking_total",
			"observed_value": "2",
		},
		ObservedAt: observedAt,
	}); err != nil {
		t.Fatalf("SaveModelRunEvaluationTrendAlertAuditEvent: %v", err)
	}

	page, err := store.ListModelRunEvaluationTrendAlertAuditEvents(ctx, ModelRunEvaluationTrendAlertAuditQuery{
		TenantID: "tenant-1",
		RuleID:   "ontology.blocking-runs",
		Status:   ModelRunEvaluationTrendAlertFiring,
		Limit:    10,
	})
	if err != nil {
		t.Fatalf("ListModelRunEvaluationTrendAlertAuditEvents: %v", err)
	}
	if page.Page.Total != 1 || len(page.Events) != 1 {
		t.Fatalf("page = %+v", page)
	}
	event := page.Events[0]
	if event.ID != "alert-audit-platform-1" ||
		event.TenantID != "tenant-1" ||
		event.RuleID != "ontology.blocking-runs" ||
		event.Severity != ModelRunEvaluationThresholdSeverityCritical ||
		event.Labels["model_version"] != "2026-06-21" ||
		event.Details["observed_value"] != "2" ||
		!event.ObservedAt.Equal(observedAt) {
		t.Fatalf("event = %+v", event)
	}
}

func newMigratedAlertAuditCore(t *testing.T, name string) *db.Store {
	t.Helper()
	ctx := context.Background()
	databases, err := db.OpenRegistry(ctx, db.RegistryConfig{Databases: map[string]db.Config{
		db.RoleCore: {Driver: db.DriverMemory, Name: name},
	}})
	if err != nil {
		t.Fatalf("open database registry: %v", err)
	}
	t.Cleanup(func() { _ = databases.Close() })
	coordinator := migration.NewCoordinator(databases)
	if err := RegisterMigrations(coordinator); err != nil {
		t.Fatalf("RegisterMigrations: %v", err)
	}
	if err := coordinator.UpRole(ctx, db.RoleCore); err != nil {
		t.Fatalf("migration up: %v", err)
	}
	core, err := databases.Store(db.RoleCore)
	if err != nil {
		t.Fatalf("core database: %v", err)
	}
	return core
}
