package ontology

import (
	"context"
	"testing"
	"time"
)

func TestMemoryModelRunEvaluationTrendAlertNotificationPolicyStoreSaveAndList(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryModelRunEvaluationTrendAlertNotificationPolicyStore()
	if err := store.SaveModelRunEvaluationTrendAlertNotificationPolicy(ctx, ModelRunEvaluationTrendAlertNotificationPolicyRecord{
		TenantID:       "tenant-1",
		RuleID:         "accuracy-release-gate.trend_failure",
		Channel:        "webhook",
		Endpoint:       "https://alerts.example.test/webhook",
		Headers:        map[string]string{"Authorization": "Bearer tenant-token"},
		RepeatWindow:   30 * time.Minute,
		RecoveryWindow: time.Hour,
		MaxAttempts:    4,
		Enabled:        true,
	}); err != nil {
		t.Fatalf("SaveModelRunEvaluationTrendAlertNotificationPolicy: %v", err)
	}

	page, err := store.ListModelRunEvaluationTrendAlertNotificationPolicies(ctx, ModelRunEvaluationTrendAlertNotificationPolicyQuery{
		TenantID:    "tenant-1",
		RuleID:      "accuracy-release-gate.trend_failure",
		EnabledOnly: true,
		Limit:       10,
	})
	if err != nil {
		t.Fatalf("ListModelRunEvaluationTrendAlertNotificationPolicies: %v", err)
	}
	if page.Page.Total != 1 || len(page.Policies) != 1 {
		t.Fatalf("page = %+v", page)
	}
	policy := page.Policies[0]
	if policy.ID == "" ||
		policy.Channel != "webhook" ||
		policy.Endpoint != "https://alerts.example.test/webhook" ||
		policy.Headers["Authorization"] != "Bearer tenant-token" ||
		policy.RepeatWindow != 30*time.Minute ||
		policy.RecoveryWindow != time.Hour ||
		policy.MaxAttempts != 4 {
		t.Fatalf("policy = %+v", policy)
	}
	policy.Headers["Authorization"] = "mutated"
	next, err := store.ListModelRunEvaluationTrendAlertNotificationPolicies(ctx, ModelRunEvaluationTrendAlertNotificationPolicyQuery{
		TenantID: "tenant-1",
		Limit:    10,
	})
	if err != nil {
		t.Fatalf("ListModelRunEvaluationTrendAlertNotificationPolicies next: %v", err)
	}
	if next.Policies[0].Headers["Authorization"] != "Bearer tenant-token" {
		t.Fatalf("headers were not cloned: %+v", next.Policies[0].Headers)
	}
}

func TestPlatformModelRunEvaluationTrendAlertNotificationPolicyStoreSaveAndList(t *testing.T) {
	ctx := context.Background()
	core := newMigratedAlertAuditCore(t, "platform-ontology-alert-notification-policy")
	store := NewPlatformModelRunEvaluationTrendAlertNotificationPolicyStore(core)
	if err := store.SaveModelRunEvaluationTrendAlertNotificationPolicy(ctx, ModelRunEvaluationTrendAlertNotificationPolicyRecord{
		TenantID:       "tenant-1",
		RuleID:         "accuracy-release-gate.trend_failure",
		Channel:        "webhook",
		Endpoint:       "https://alerts.example.test/webhook",
		Headers:        map[string]string{"Authorization": "Bearer tenant-token"},
		RepeatWindow:   15 * time.Minute,
		RecoveryWindow: 45 * time.Minute,
		MaxAttempts:    5,
		Enabled:        true,
	}); err != nil {
		t.Fatalf("SaveModelRunEvaluationTrendAlertNotificationPolicy: %v", err)
	}

	page, err := store.ListModelRunEvaluationTrendAlertNotificationPolicies(ctx, ModelRunEvaluationTrendAlertNotificationPolicyQuery{
		TenantID: "tenant-1",
		Channel:  "webhook",
		Limit:    10,
	})
	if err != nil {
		t.Fatalf("ListModelRunEvaluationTrendAlertNotificationPolicies: %v", err)
	}
	if page.Page.Total != 1 || len(page.Policies) != 1 {
		t.Fatalf("page = %+v", page)
	}
	policy := page.Policies[0]
	if policy.RuleID != "accuracy-release-gate.trend_failure" ||
		policy.RepeatWindow != 15*time.Minute ||
		policy.RecoveryWindow != 45*time.Minute ||
		policy.MaxAttempts != 5 ||
		policy.Headers["Authorization"] != "Bearer tenant-token" {
		t.Fatalf("policy = %+v", policy)
	}
}
