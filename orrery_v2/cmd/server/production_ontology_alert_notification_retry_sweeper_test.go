package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"orrery_v2/internal/app/ontology"
	"orrery_v2/internal/platform"
	"orrery_v2/internal/platform/testkit"
)

func TestRegisterProductionOntologyAlertNotificationRetrySweeperIfEnabledSkipsDefaultConfig(t *testing.T) {
	rt := testkit.NewInitializedTestRuntime(t, OntologyModule())
	core, err := rt.Databases.Store("core")
	if err != nil {
		t.Fatalf("core store: %v", err)
	}

	if err := registerProductionOntologyAlertNotificationRetrySweeperIfEnabled(rt, core); err != nil {
		t.Fatalf("registerProductionOntologyAlertNotificationRetrySweeperIfEnabled: %v", err)
	}
}

func TestRegisterProductionOntologyAlertNotificationRetrySweeperAbandonsMaxAttempts(t *testing.T) {
	ctx := context.Background()
	rt := newOntologyAlertNotificationRetrySweeperConfiguredRuntime(t)
	core, err := rt.Databases.Store("core")
	if err != nil {
		t.Fatalf("core store: %v", err)
	}
	now := rt.Clock.Now().UTC()
	receipts := ontology.NewPlatformModelRunEvaluationTrendAlertNotificationReceiptStore(core)
	if err := receipts.SaveModelRunEvaluationTrendAlertNotificationReceipt(ctx, ontology.ModelRunEvaluationTrendAlertNotificationDeliveryReceipt{
		ID:          "receipt-production-max-attempts",
		TenantID:    "tenant-1",
		RuleID:      "accuracy-release-gate.trend_failure",
		Status:      ontology.ModelRunEvaluationTrendAlertNotificationDeliveryFailed,
		AlertStatus: ontology.ModelRunEvaluationTrendAlertFiring,
		Reason:      ontology.ModelRunEvaluationTrendAlertNotificationReasonFiring,
		Attempt:     2,
		Retryable:   true,
		NextRetryAt: now.Add(-time.Minute),
		SentAt:      now.Add(-20 * time.Minute),
	}); err != nil {
		t.Fatalf("SaveModelRunEvaluationTrendAlertNotificationReceipt: %v", err)
	}

	if err := registerProductionOntologyAlertNotificationRetrySweeper(rt, core); err != nil {
		t.Fatalf("registerProductionOntologyAlertNotificationRetrySweeper: %v", err)
	}
	report := rt.Sweeper.SweepOnce(ctx)
	if report.Failed != 0 {
		t.Fatalf("sweeper report = %+v", report)
	}
	page, err := receipts.ListModelRunEvaluationTrendAlertNotificationReceipts(ctx, ontology.ModelRunEvaluationTrendAlertNotificationReceiptQuery{
		TenantID: "tenant-1",
		Status:   ontology.ModelRunEvaluationTrendAlertNotificationDeliveryFailed,
		Limit:    10,
	})
	if err != nil {
		t.Fatalf("ListModelRunEvaluationTrendAlertNotificationReceipts: %v", err)
	}
	if len(page.Receipts) != 1 ||
		page.Receipts[0].Retryable ||
		page.Receipts[0].ErrorMessage != "max retry attempts reached" {
		t.Fatalf("receipt page = %+v", page)
	}
}

func TestRegisterProductionOntologyAlertNotificationRetrySweeperResendsWithLogChannel(t *testing.T) {
	ctx := context.Background()
	rt := newOntologyAlertNotificationRetrySweeperWithLogChannelRuntime(t)
	core, err := rt.Databases.Store("core")
	if err != nil {
		t.Fatalf("core store: %v", err)
	}
	now := rt.Clock.Now().UTC()
	receipts := ontology.NewPlatformModelRunEvaluationTrendAlertNotificationReceiptStore(core)
	labels := map[string]string{
		"tenant_id": "tenant-1",
		"model":     "gpt-5",
	}
	if err := receipts.SaveModelRunEvaluationTrendAlertNotificationReceipt(ctx, ontology.ModelRunEvaluationTrendAlertNotificationDeliveryReceipt{
		ID:           "receipt-production-resend",
		TenantID:     "tenant-1",
		RuleID:       "accuracy-release-gate.trend_failure",
		Status:       ontology.ModelRunEvaluationTrendAlertNotificationDeliveryFailed,
		AlertStatus:  ontology.ModelRunEvaluationTrendAlertFiring,
		Reason:       ontology.ModelRunEvaluationTrendAlertNotificationReasonFiring,
		Attempt:      1,
		Retryable:    true,
		NextRetryAt:  now.Add(-time.Minute),
		Labels:       labels,
		ErrorMessage: "temporary webhook failure",
		SentAt:       now.Add(-20 * time.Minute),
	}); err != nil {
		t.Fatalf("SaveModelRunEvaluationTrendAlertNotificationReceipt: %v", err)
	}

	if err := registerProductionOntologyAlertNotificationRetrySweeper(rt, core); err != nil {
		t.Fatalf("registerProductionOntologyAlertNotificationRetrySweeper: %v", err)
	}
	report := rt.Sweeper.SweepOnce(ctx)
	if report.Failed != 0 {
		t.Fatalf("sweeper report = %+v", report)
	}
	delivered, err := receipts.ListModelRunEvaluationTrendAlertNotificationReceipts(ctx, ontology.ModelRunEvaluationTrendAlertNotificationReceiptQuery{
		TenantID: "tenant-1",
		Status:   ontology.ModelRunEvaluationTrendAlertNotificationDeliveryDelivered,
		Channel:  "log",
		Limit:    10,
	})
	if err != nil {
		t.Fatalf("ListModelRunEvaluationTrendAlertNotificationReceipts delivered: %v", err)
	}
	if len(delivered.Receipts) != 1 || delivered.Receipts[0].Attempt != 2 {
		t.Fatalf("delivered receipt page = %+v", delivered)
	}
	failed, err := receipts.ListModelRunEvaluationTrendAlertNotificationReceipts(ctx, ontology.ModelRunEvaluationTrendAlertNotificationReceiptQuery{
		TenantID: "tenant-1",
		Status:   ontology.ModelRunEvaluationTrendAlertNotificationDeliveryFailed,
		Limit:    10,
	})
	if err != nil {
		t.Fatalf("ListModelRunEvaluationTrendAlertNotificationReceipts failed: %v", err)
	}
	if len(failed.Receipts) != 1 || failed.Receipts[0].Retryable || !failed.Receipts[0].NextRetryAt.IsZero() {
		t.Fatalf("failed receipt page = %+v", failed)
	}
	record, found, err := ontology.NewPlatformModelRunEvaluationTrendAlertNotificationHistoryStore(core).GetModelRunEvaluationTrendAlertNotificationHistory(ctx, ontology.ModelRunEvaluationTrendAlertNotificationHistoryQuery{
		TenantID: "tenant-1",
		RuleID:   "accuracy-release-gate.trend_failure",
		Labels:   labels,
	})
	if err != nil {
		t.Fatalf("GetModelRunEvaluationTrendAlertNotificationHistory: %v", err)
	}
	if !found || record.LastNotificationStatus != ontology.ModelRunEvaluationTrendAlertFiring {
		t.Fatalf("history record = %+v found=%v", record, found)
	}
}

func newOntologyAlertNotificationRetrySweeperConfiguredRuntime(t *testing.T) *platform.Runtime {
	t.Helper()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "orrery.yaml")
	if err := os.WriteFile(path, []byte(`
database:
  default_url: file://./stage0-test.db
ontology:
  alert_notification_retry_sweeper:
    enabled: true
    limit: 10
    max_attempts: 2
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

func newOntologyAlertNotificationRetrySweeperWithLogChannelRuntime(t *testing.T) *platform.Runtime {
	t.Helper()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "orrery.yaml")
	if err := os.WriteFile(path, []byte(`
database:
  default_url: file://./stage0-test.db
ontology:
  alert_notification_sweeper:
    log:
      enabled: true
  alert_notification_retry_sweeper:
    enabled: true
    limit: 10
    max_attempts: 3
    backoff: 5m
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
