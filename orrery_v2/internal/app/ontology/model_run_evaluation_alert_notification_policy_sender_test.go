package ontology

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
	"time"
)

func TestModelRunEvaluationTrendAlertNotificationPolicySenderUsesWebhookPolicy(t *testing.T) {
	ctx := context.Background()
	observedAt := time.Date(2026, 6, 22, 23, 0, 0, 0, time.UTC)
	event := modelRunEvaluationTrendAlertNotificationTestEvent(ModelRunEvaluationTrendAlertFiring, observedAt)
	policies := NewMemoryModelRunEvaluationTrendAlertNotificationPolicyStore()
	var called bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		if r.Header.Get("Authorization") != "Bearer tenant-token" {
			t.Fatalf("authorization header = %q", r.Header.Get("Authorization"))
		}
		w.Header().Set("X-Webhook-Message-ID", "tenant-webhook-message")
		w.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()
	if err := policies.SaveModelRunEvaluationTrendAlertNotificationPolicy(ctx, ModelRunEvaluationTrendAlertNotificationPolicyRecord{
		TenantID: event.TenantID,
		RuleID:   event.RuleID,
		Channel:  "tenant-webhook",
		Endpoint: server.URL,
		Headers:  map[string]string{"Authorization": "Bearer tenant-token"},
		Enabled:  true,
	}); err != nil {
		t.Fatalf("SaveModelRunEvaluationTrendAlertNotificationPolicy: %v", err)
	}
	defaultSender := &recordingModelRunEvaluationTrendAlertNotificationSender{}
	sender := NewModelRunEvaluationTrendAlertNotificationPolicySender(ModelRunEvaluationTrendAlertNotificationPolicySenderOptions{
		Policies:              policies,
		Default:               defaultSender,
		Client:                server.Client(),
		AllowPrivateEndpoints: true,
	})

	receipt, err := sender.SendModelRunEvaluationTrendAlertNotification(ctx, event, ModelRunEvaluationTrendAlertNotificationPlan{})
	if err != nil {
		t.Fatalf("SendModelRunEvaluationTrendAlertNotification: %v", err)
	}
	if !called || receipt.Channel != "tenant-webhook" || receipt.MessageID != "tenant-webhook-message" {
		t.Fatalf("called=%v receipt=%+v", called, receipt)
	}
	if len(defaultSender.events) != 0 {
		t.Fatalf("default sender events = %+v", defaultSender.events)
	}
}

func TestModelRunEvaluationTrendAlertNotificationPolicySenderResolvesWebhookPolicyHeaderSecrets(t *testing.T) {
	ctx := context.Background()
	observedAt := time.Date(2026, 6, 22, 23, 3, 0, 0, time.UTC)
	event := modelRunEvaluationTrendAlertNotificationTestEvent(ModelRunEvaluationTrendAlertFiring, observedAt)
	policies := NewMemoryModelRunEvaluationTrendAlertNotificationPolicyStore()
	var called bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		if r.Header.Get("Authorization") != "Bearer resolved-token" {
			t.Fatalf("authorization header = %q", r.Header.Get("Authorization"))
		}
		w.Header().Set("X-Webhook-Message-ID", "tenant-webhook-secret-message")
		w.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()
	if err := policies.SaveModelRunEvaluationTrendAlertNotificationPolicy(ctx, ModelRunEvaluationTrendAlertNotificationPolicyRecord{
		TenantID: event.TenantID,
		RuleID:   event.RuleID,
		Channel:  "tenant-webhook",
		Endpoint: server.URL,
		Headers:  map[string]string{"Authorization": "secret://alerts/token"},
		Enabled:  true,
	}); err != nil {
		t.Fatalf("SaveModelRunEvaluationTrendAlertNotificationPolicy: %v", err)
	}
	sender := NewModelRunEvaluationTrendAlertNotificationPolicySender(ModelRunEvaluationTrendAlertNotificationPolicySenderOptions{
		Policies:              policies,
		Client:                server.Client(),
		AllowPrivateEndpoints: true,
		Secrets: staticModelRunEvaluationTrendAlertNotificationSecretResolver{
			"secret://alerts/token": "Bearer resolved-token",
		},
	})

	receipt, err := sender.SendModelRunEvaluationTrendAlertNotification(ctx, event, ModelRunEvaluationTrendAlertNotificationPlan{})
	if err != nil {
		t.Fatalf("SendModelRunEvaluationTrendAlertNotification: %v", err)
	}
	if !called || receipt.MessageID != "tenant-webhook-secret-message" {
		t.Fatalf("called=%v receipt=%+v", called, receipt)
	}
}

func TestModelRunEvaluationTrendAlertNotificationPolicySenderResolvesWebhookPolicyHeaderSecretTemplates(t *testing.T) {
	ctx := context.Background()
	observedAt := time.Date(2026, 6, 22, 23, 4, 0, 0, time.UTC)
	event := modelRunEvaluationTrendAlertNotificationTestEvent(ModelRunEvaluationTrendAlertFiring, observedAt)
	policies := NewMemoryModelRunEvaluationTrendAlertNotificationPolicyStore()
	var called bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		if r.Header.Get("Authorization") != "Bearer resolved-token" {
			t.Fatalf("authorization header = %q", r.Header.Get("Authorization"))
		}
		if r.Header.Get("X-Alert-Key") != "tenant-1:resolved-key" {
			t.Fatalf("x-alert-key header = %q", r.Header.Get("X-Alert-Key"))
		}
		w.Header().Set("X-Webhook-Message-ID", "tenant-webhook-secret-template-message")
		w.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()
	if err := policies.SaveModelRunEvaluationTrendAlertNotificationPolicy(ctx, ModelRunEvaluationTrendAlertNotificationPolicyRecord{
		TenantID: event.TenantID,
		RuleID:   event.RuleID,
		Channel:  "tenant-webhook",
		Endpoint: server.URL,
		Headers: map[string]string{
			"Authorization": "Bearer ${secret://alerts/token}",
			"X-Alert-Key":   "tenant-1:${settings://alerts/key}",
		},
		Enabled: true,
	}); err != nil {
		t.Fatalf("SaveModelRunEvaluationTrendAlertNotificationPolicy: %v", err)
	}
	sender := NewModelRunEvaluationTrendAlertNotificationPolicySender(ModelRunEvaluationTrendAlertNotificationPolicySenderOptions{
		Policies:              policies,
		Client:                server.Client(),
		AllowPrivateEndpoints: true,
		Secrets: staticModelRunEvaluationTrendAlertNotificationSecretResolver{
			"secret://alerts/token": "resolved-token",
			"settings://alerts/key": "resolved-key",
		},
	})

	receipt, err := sender.SendModelRunEvaluationTrendAlertNotification(ctx, event, ModelRunEvaluationTrendAlertNotificationPlan{})
	if err != nil {
		t.Fatalf("SendModelRunEvaluationTrendAlertNotification: %v", err)
	}
	if !called || receipt.MessageID != "tenant-webhook-secret-template-message" {
		t.Fatalf("called=%v receipt=%+v", called, receipt)
	}
}

func TestModelRunEvaluationTrendAlertNotificationPolicySenderRejectsResolvedPrivateWebhookEndpoint(t *testing.T) {
	ctx := context.Background()
	observedAt := time.Date(2026, 6, 22, 23, 8, 0, 0, time.UTC)
	event := modelRunEvaluationTrendAlertNotificationTestEvent(ModelRunEvaluationTrendAlertFiring, observedAt)
	policies := NewMemoryModelRunEvaluationTrendAlertNotificationPolicyStore()
	if err := policies.SaveModelRunEvaluationTrendAlertNotificationPolicy(ctx, ModelRunEvaluationTrendAlertNotificationPolicyRecord{
		TenantID: event.TenantID,
		RuleID:   event.RuleID,
		Channel:  "tenant-webhook",
		Endpoint: "https://alerts.example.test/webhook",
		Enabled:  true,
	}); err != nil {
		t.Fatalf("SaveModelRunEvaluationTrendAlertNotificationPolicy: %v", err)
	}
	defaultSender := &recordingModelRunEvaluationTrendAlertNotificationSender{}
	sender := NewModelRunEvaluationTrendAlertNotificationPolicySender(ModelRunEvaluationTrendAlertNotificationPolicySenderOptions{
		Policies: policies,
		Default:  defaultSender,
		Resolver: staticModelRunEvaluationTrendAlertNotificationEndpointResolver{
			"alerts.example.test": {netip.MustParseAddr("10.0.0.7")},
		},
	})

	_, err := sender.SendModelRunEvaluationTrendAlertNotification(ctx, event, ModelRunEvaluationTrendAlertNotificationPlan{})
	if err == nil {
		t.Fatal("SendModelRunEvaluationTrendAlertNotification succeeded, want unsafe endpoint error")
	}
	if len(defaultSender.events) != 0 {
		t.Fatalf("default sender events = %+v", defaultSender.events)
	}
}

type staticModelRunEvaluationTrendAlertNotificationSecretResolver map[string]string

func (r staticModelRunEvaluationTrendAlertNotificationSecretResolver) ResolveSecret(ctx context.Context, ref string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return r[ref], nil
}

func TestModelRunEvaluationTrendAlertNotificationPolicySenderRejectsUnsafeWebhookEndpoint(t *testing.T) {
	ctx := context.Background()
	observedAt := time.Date(2026, 6, 22, 23, 5, 0, 0, time.UTC)
	event := modelRunEvaluationTrendAlertNotificationTestEvent(ModelRunEvaluationTrendAlertFiring, observedAt)
	policies := NewMemoryModelRunEvaluationTrendAlertNotificationPolicyStore()
	if err := policies.SaveModelRunEvaluationTrendAlertNotificationPolicy(ctx, ModelRunEvaluationTrendAlertNotificationPolicyRecord{
		TenantID: event.TenantID,
		RuleID:   event.RuleID,
		Channel:  "tenant-webhook",
		Endpoint: "http://169.254.169.254/latest/meta-data",
		Enabled:  true,
	}); err != nil {
		t.Fatalf("SaveModelRunEvaluationTrendAlertNotificationPolicy: %v", err)
	}
	defaultSender := &recordingModelRunEvaluationTrendAlertNotificationSender{}
	sender := NewModelRunEvaluationTrendAlertNotificationPolicySender(ModelRunEvaluationTrendAlertNotificationPolicySenderOptions{
		Policies: policies,
		Default:  defaultSender,
	})

	_, err := sender.SendModelRunEvaluationTrendAlertNotification(ctx, event, ModelRunEvaluationTrendAlertNotificationPlan{})
	if err == nil {
		t.Fatal("SendModelRunEvaluationTrendAlertNotification succeeded, want unsafe endpoint error")
	}
	if len(defaultSender.events) != 0 {
		t.Fatalf("default sender events = %+v", defaultSender.events)
	}
}
