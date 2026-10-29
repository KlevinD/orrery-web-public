package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"orrery_v2/internal/app/ontology"
	"orrery_v2/internal/platform"
	"orrery_v2/internal/platform/testkit"
)

func TestRegisterProductionOntologyAlertNotificationSweeperRequiresEnabledChannel(t *testing.T) {
	rt := testkit.NewInitializedTestRuntime(t, OntologyModule())
	core, err := rt.Databases.Store("core")
	if err != nil {
		t.Fatalf("core store: %v", err)
	}

	if err := registerProductionOntologyAlertNotificationSweeper(rt, core); err == nil {
		t.Fatal("registerProductionOntologyAlertNotificationSweeper succeeded without enabled channel")
	}
}

func TestRegisterProductionOntologyAlertNotificationSweeperIfEnabledSkipsDefaultConfig(t *testing.T) {
	rt := testkit.NewInitializedTestRuntime(t, OntologyModule())
	core, err := rt.Databases.Store("core")
	if err != nil {
		t.Fatalf("core store: %v", err)
	}

	if err := registerProductionOntologyAlertNotificationSweeperIfEnabled(rt, core); err != nil {
		t.Fatalf("registerProductionOntologyAlertNotificationSweeperIfEnabled: %v", err)
	}
}

func TestRegisterProductionOntologyAlertNotificationSweeperIfEnabledRegistersWebhookChannel(t *testing.T) {
	ctx := context.Background()
	var called bool
	webhook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.Header().Set("X-Webhook-Message-ID", "if-enabled-webhook-message")
		w.WriteHeader(http.StatusAccepted)
	}))
	defer webhook.Close()
	rt := newOntologyAlertNotificationSweeperWebhookRuntime(t, webhook.URL)
	core, err := rt.Databases.Store("core")
	if err != nil {
		t.Fatalf("core store: %v", err)
	}
	observedAt := rt.Clock.Now().UTC().Add(-time.Minute)
	event := ontology.ModelRunEvaluationTrendAlertAuditEvent{
		EventType:  "ontology.model_run_evaluation_alert.firing",
		TenantID:   "tenant-1",
		RuleID:     "accuracy-warning.trend_failure",
		Status:     ontology.ModelRunEvaluationTrendAlertFiring,
		ObservedAt: observedAt,
		Labels: map[string]string{
			"tenant_id": "tenant-1",
			"model":     "gpt-5",
		},
	}
	if err := ontology.NewPlatformModelRunEvaluationTrendAlertAuditStore(core).SaveModelRunEvaluationTrendAlertAuditEvent(ctx, event); err != nil {
		t.Fatalf("SaveModelRunEvaluationTrendAlertAuditEvent: %v", err)
	}

	if err := registerProductionOntologyAlertNotificationSweeperIfEnabled(rt, core); err != nil {
		t.Fatalf("registerProductionOntologyAlertNotificationSweeperIfEnabled: %v", err)
	}
	report := rt.Sweeper.SweepOnce(ctx)
	if report.Failed != 0 {
		t.Fatalf("sweeper report = %+v", report)
	}
	if !called {
		t.Fatal("webhook channel was not registered")
	}
}

func TestRegisterProductionOntologyAlertNotificationSweeperUsesLogChannel(t *testing.T) {
	ctx := context.Background()
	rt := newOntologyAlertNotificationSweeperConfiguredRuntime(t)
	core, err := rt.Databases.Store("core")
	if err != nil {
		t.Fatalf("core store: %v", err)
	}
	observedAt := rt.Clock.Now().UTC().Add(-time.Minute)
	event := ontology.ModelRunEvaluationTrendAlertAuditEvent{
		EventType:  "ontology.model_run_evaluation_alert.firing",
		TenantID:   "tenant-1",
		RuleID:     "accuracy-warning.trend_failure",
		Status:     ontology.ModelRunEvaluationTrendAlertFiring,
		ObservedAt: observedAt,
		Labels: map[string]string{
			"tenant_id":                "tenant-1",
			"engine":                   "ontology_designer",
			"model":                    "gpt-5",
			"metric_threshold_rule_id": "accuracy-warning",
			"snapshot_recorded_to":     observedAt.Format(time.RFC3339Nano),
		},
	}
	if err := ontology.NewPlatformModelRunEvaluationTrendAlertAuditStore(core).SaveModelRunEvaluationTrendAlertAuditEvent(ctx, event); err != nil {
		t.Fatalf("SaveModelRunEvaluationTrendAlertAuditEvent: %v", err)
	}

	if err := registerProductionOntologyAlertNotificationSweeper(rt, core); err != nil {
		t.Fatalf("registerProductionOntologyAlertNotificationSweeper: %v", err)
	}
	report := rt.Sweeper.SweepOnce(ctx)
	if report.Failed != 0 {
		t.Fatalf("sweeper report = %+v", report)
	}

	record, found, err := ontology.NewPlatformModelRunEvaluationTrendAlertNotificationHistoryStore(core).GetModelRunEvaluationTrendAlertNotificationHistory(ctx, ontology.ModelRunEvaluationTrendAlertNotificationHistoryQuery{
		TenantID: "tenant-1",
		RuleID:   "accuracy-warning.trend_failure",
		Labels:   event.Labels,
	})
	if err != nil {
		t.Fatalf("GetModelRunEvaluationTrendAlertNotificationHistory: %v", err)
	}
	if !found || record.LastNotificationStatus != ontology.ModelRunEvaluationTrendAlertFiring {
		t.Fatalf("record = %+v found=%v", record, found)
	}
	receiptPage, err := ontology.NewPlatformModelRunEvaluationTrendAlertNotificationReceiptStore(core).ListModelRunEvaluationTrendAlertNotificationReceipts(ctx, ontology.ModelRunEvaluationTrendAlertNotificationReceiptQuery{
		TenantID: "tenant-1",
		RuleID:   "accuracy-warning.trend_failure",
		Status:   ontology.ModelRunEvaluationTrendAlertNotificationDeliveryDelivered,
		Channel:  "log",
		Limit:    10,
	})
	if err != nil {
		t.Fatalf("ListModelRunEvaluationTrendAlertNotificationReceipts: %v", err)
	}
	if len(receiptPage.Receipts) != 1 || receiptPage.Receipts[0].MessageID == "" {
		t.Fatalf("receipt page = %+v", receiptPage)
	}
}

func TestRegisterProductionOntologyAlertNotificationSweeperUsesWebhookChannel(t *testing.T) {
	ctx := context.Background()
	var payload map[string]any
	webhook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-token" {
			t.Fatalf("authorization header = %q", r.Header.Get("Authorization"))
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode payload: %v", err)
		}
		w.Header().Set("X-Webhook-Message-ID", "production-webhook-message")
		w.WriteHeader(http.StatusAccepted)
	}))
	defer webhook.Close()
	rt := newOntologyAlertNotificationSweeperWebhookRuntime(t, webhook.URL)
	core, err := rt.Databases.Store("core")
	if err != nil {
		t.Fatalf("core store: %v", err)
	}
	observedAt := rt.Clock.Now().UTC().Add(-time.Minute)
	event := ontology.ModelRunEvaluationTrendAlertAuditEvent{
		EventType:  "ontology.model_run_evaluation_alert.firing",
		TenantID:   "tenant-1",
		RuleID:     "accuracy-warning.trend_failure",
		Status:     ontology.ModelRunEvaluationTrendAlertFiring,
		ObservedAt: observedAt,
		Labels: map[string]string{
			"tenant_id": "tenant-1",
			"model":     "gpt-5",
		},
	}
	if err := ontology.NewPlatformModelRunEvaluationTrendAlertAuditStore(core).SaveModelRunEvaluationTrendAlertAuditEvent(ctx, event); err != nil {
		t.Fatalf("SaveModelRunEvaluationTrendAlertAuditEvent: %v", err)
	}

	if err := registerProductionOntologyAlertNotificationSweeper(rt, core); err != nil {
		t.Fatalf("registerProductionOntologyAlertNotificationSweeper: %v", err)
	}
	report := rt.Sweeper.SweepOnce(ctx)
	if report.Failed != 0 {
		t.Fatalf("sweeper report = %+v", report)
	}
	if payload["tenant_id"] != "tenant-1" || payload["rule_id"] != "accuracy-warning.trend_failure" {
		t.Fatalf("payload = %+v", payload)
	}
	receiptPage, err := ontology.NewPlatformModelRunEvaluationTrendAlertNotificationReceiptStore(core).ListModelRunEvaluationTrendAlertNotificationReceipts(ctx, ontology.ModelRunEvaluationTrendAlertNotificationReceiptQuery{
		TenantID: "tenant-1",
		Status:   ontology.ModelRunEvaluationTrendAlertNotificationDeliveryDelivered,
		Channel:  "webhook",
		Limit:    10,
	})
	if err != nil {
		t.Fatalf("ListModelRunEvaluationTrendAlertNotificationReceipts: %v", err)
	}
	if len(receiptPage.Receipts) != 1 || receiptPage.Receipts[0].MessageID != "production-webhook-message" {
		t.Fatalf("receipt page = %+v", receiptPage)
	}
}

func TestRegisterProductionOntologyAlertNotificationSweeperUsesStoredWebhookPolicy(t *testing.T) {
	ctx := context.Background()
	var called bool
	webhook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		if r.Header.Get("Authorization") != "Bearer tenant-token" {
			t.Fatalf("authorization header = %q", r.Header.Get("Authorization"))
		}
		w.Header().Set("X-Webhook-Message-ID", "stored-policy-message")
		w.WriteHeader(http.StatusAccepted)
	}))
	defer webhook.Close()
	rt := newOntologyAlertNotificationSweeperWebhookRuntime(t, webhook.URL)
	core, err := rt.Databases.Store("core")
	if err != nil {
		t.Fatalf("core store: %v", err)
	}
	observedAt := rt.Clock.Now().UTC().Add(-time.Minute)
	event := ontology.ModelRunEvaluationTrendAlertAuditEvent{
		EventType:  "ontology.model_run_evaluation_alert.firing",
		TenantID:   "tenant-1",
		RuleID:     "accuracy-warning.trend_failure",
		Status:     ontology.ModelRunEvaluationTrendAlertFiring,
		ObservedAt: observedAt,
		Labels: map[string]string{
			"tenant_id": "tenant-1",
			"model":     "gpt-5",
		},
	}
	if err := ontology.NewPlatformModelRunEvaluationTrendAlertAuditStore(core).SaveModelRunEvaluationTrendAlertAuditEvent(ctx, event); err != nil {
		t.Fatalf("SaveModelRunEvaluationTrendAlertAuditEvent: %v", err)
	}
	if err := ontology.NewPlatformModelRunEvaluationTrendAlertNotificationPolicyStore(core).SaveModelRunEvaluationTrendAlertNotificationPolicy(ctx, ontology.ModelRunEvaluationTrendAlertNotificationPolicyRecord{
		TenantID: event.TenantID,
		RuleID:   event.RuleID,
		Channel:  "stored-webhook",
		Endpoint: webhook.URL,
		Headers:  map[string]string{"Authorization": "Bearer tenant-token"},
		Enabled:  true,
	}); err != nil {
		t.Fatalf("SaveModelRunEvaluationTrendAlertNotificationPolicy: %v", err)
	}

	if err := registerProductionOntologyAlertNotificationSweeper(rt, core); err != nil {
		t.Fatalf("registerProductionOntologyAlertNotificationSweeper: %v", err)
	}
	report := rt.Sweeper.SweepOnce(ctx)
	if report.Failed != 0 {
		t.Fatalf("sweeper report = %+v", report)
	}
	if !called {
		t.Fatal("stored webhook policy was not called")
	}
	receiptPage, err := ontology.NewPlatformModelRunEvaluationTrendAlertNotificationReceiptStore(core).ListModelRunEvaluationTrendAlertNotificationReceipts(ctx, ontology.ModelRunEvaluationTrendAlertNotificationReceiptQuery{
		TenantID: "tenant-1",
		Status:   ontology.ModelRunEvaluationTrendAlertNotificationDeliveryDelivered,
		Channel:  "stored-webhook",
		Limit:    10,
	})
	if err != nil {
		t.Fatalf("ListModelRunEvaluationTrendAlertNotificationReceipts: %v", err)
	}
	if len(receiptPage.Receipts) != 1 || receiptPage.Receipts[0].MessageID != "stored-policy-message" {
		t.Fatalf("receipt page = %+v", receiptPage)
	}
}

func TestRegisterProductionOntologyAlertNotificationSweeperResolvesStoredWebhookPolicyHeaderSecrets(t *testing.T) {
	ctx := context.Background()
	t.Setenv("ORRERY_SECRET_ALERTS_TOKEN", "tenant-secret")
	var called bool
	webhook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		if r.Header.Get("Authorization") != "Bearer tenant-secret" {
			t.Fatalf("authorization header = %q", r.Header.Get("Authorization"))
		}
		w.Header().Set("X-Webhook-Message-ID", "stored-policy-secret-message")
		w.WriteHeader(http.StatusAccepted)
	}))
	defer webhook.Close()
	rt := newOntologyAlertNotificationSweeperWebhookRuntime(t, webhook.URL)
	core, err := rt.Databases.Store("core")
	if err != nil {
		t.Fatalf("core store: %v", err)
	}
	observedAt := rt.Clock.Now().UTC().Add(-time.Minute)
	event := ontology.ModelRunEvaluationTrendAlertAuditEvent{
		EventType:  "ontology.model_run_evaluation_alert.firing",
		TenantID:   "tenant-1",
		RuleID:     "accuracy-warning.trend_failure",
		Status:     ontology.ModelRunEvaluationTrendAlertFiring,
		ObservedAt: observedAt,
		Labels: map[string]string{
			"tenant_id": "tenant-1",
			"model":     "gpt-5",
		},
	}
	if err := ontology.NewPlatformModelRunEvaluationTrendAlertAuditStore(core).SaveModelRunEvaluationTrendAlertAuditEvent(ctx, event); err != nil {
		t.Fatalf("SaveModelRunEvaluationTrendAlertAuditEvent: %v", err)
	}
	if err := ontology.NewPlatformModelRunEvaluationTrendAlertNotificationPolicyStore(core).SaveModelRunEvaluationTrendAlertNotificationPolicy(ctx, ontology.ModelRunEvaluationTrendAlertNotificationPolicyRecord{
		TenantID: event.TenantID,
		RuleID:   event.RuleID,
		Channel:  "stored-webhook",
		Endpoint: webhook.URL,
		Headers:  map[string]string{"Authorization": "Bearer ${secret://alerts/token}"},
		Enabled:  true,
	}); err != nil {
		t.Fatalf("SaveModelRunEvaluationTrendAlertNotificationPolicy: %v", err)
	}

	if err := registerProductionOntologyAlertNotificationSweeper(rt, core); err != nil {
		t.Fatalf("registerProductionOntologyAlertNotificationSweeper: %v", err)
	}
	report := rt.Sweeper.SweepOnce(ctx)
	if report.Failed != 0 {
		t.Fatalf("sweeper report = %+v", report)
	}
	if !called {
		t.Fatal("stored webhook policy was not called")
	}
}

func TestRegisterProductionOntologyAlertNotificationSweeperRejectsUnsafeStoredWebhookPolicy(t *testing.T) {
	ctx := context.Background()
	rt := newOntologyAlertNotificationSweeperConfiguredRuntime(t)
	core, err := rt.Databases.Store("core")
	if err != nil {
		t.Fatalf("core store: %v", err)
	}
	observedAt := rt.Clock.Now().UTC().Add(-time.Minute)
	event := ontology.ModelRunEvaluationTrendAlertAuditEvent{
		EventType:  "ontology.model_run_evaluation_alert.firing",
		TenantID:   "tenant-1",
		RuleID:     "accuracy-warning.trend_failure",
		Status:     ontology.ModelRunEvaluationTrendAlertFiring,
		ObservedAt: observedAt,
		Labels: map[string]string{
			"tenant_id": "tenant-1",
			"model":     "gpt-5",
		},
	}
	if err := ontology.NewPlatformModelRunEvaluationTrendAlertAuditStore(core).SaveModelRunEvaluationTrendAlertAuditEvent(ctx, event); err != nil {
		t.Fatalf("SaveModelRunEvaluationTrendAlertAuditEvent: %v", err)
	}
	if err := ontology.NewPlatformModelRunEvaluationTrendAlertNotificationPolicyStore(core).SaveModelRunEvaluationTrendAlertNotificationPolicy(ctx, ontology.ModelRunEvaluationTrendAlertNotificationPolicyRecord{
		TenantID: event.TenantID,
		RuleID:   event.RuleID,
		Channel:  "unsafe-webhook",
		Endpoint: "http://169.254.169.254/latest/meta-data",
		Enabled:  true,
	}); err != nil {
		t.Fatalf("SaveModelRunEvaluationTrendAlertNotificationPolicy: %v", err)
	}

	if err := registerProductionOntologyAlertNotificationSweeper(rt, core); err != nil {
		t.Fatalf("registerProductionOntologyAlertNotificationSweeper: %v", err)
	}
	report := rt.Sweeper.SweepOnce(ctx)
	if report.Failed == 0 {
		t.Fatalf("sweeper report = %+v", report)
	}
	receiptPage, err := ontology.NewPlatformModelRunEvaluationTrendAlertNotificationReceiptStore(core).ListModelRunEvaluationTrendAlertNotificationReceipts(ctx, ontology.ModelRunEvaluationTrendAlertNotificationReceiptQuery{
		TenantID:  "tenant-1",
		Status:    ontology.ModelRunEvaluationTrendAlertNotificationDeliveryFailed,
		Retryable: true,
		Limit:     10,
	})
	if err != nil {
		t.Fatalf("ListModelRunEvaluationTrendAlertNotificationReceipts: %v", err)
	}
	if len(receiptPage.Receipts) != 1 || receiptPage.Receipts[0].Channel != "" || receiptPage.Receipts[0].ErrorMessage == "" {
		t.Fatalf("receipt page = %+v", receiptPage)
	}
}

func newOntologyAlertNotificationSweeperConfiguredRuntime(t *testing.T) *platform.Runtime {
	t.Helper()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "orrery.yaml")
	if err := os.WriteFile(path, []byte(`
database:
  default_url: file://./stage0-test.db
ontology:
  alert_notification_sweeper:
    limit: 10
    repeat_window: 10m
    recovery_window: 30m
    log:
      enabled: true
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

func newOntologyAlertNotificationSweeperWebhookRuntime(t *testing.T, endpoint string) *platform.Runtime {
	t.Helper()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "orrery.yaml")
	if err := os.WriteFile(path, []byte(`
database:
  default_url: file://./stage0-test.db
ontology:
  alert_notification_sweeper:
    limit: 10
    webhook:
      enabled: true
      endpoint: `+endpoint+`
      allow_private_endpoints: true
      headers:
        Authorization: Bearer test-token
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
