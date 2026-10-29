package ontology

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"
)

func TestModelRunEvaluationTrendAlertNotificationWebhookSenderPostsPayload(t *testing.T) {
	ctx := context.Background()
	observedAt := time.Date(2026, 6, 22, 22, 0, 0, 0, time.UTC)
	event := modelRunEvaluationTrendAlertNotificationTestEvent(ModelRunEvaluationTrendAlertFiring, observedAt)
	plan := ModelRunEvaluationTrendAlertNotificationPlan{
		Decision: ModelRunEvaluationTrendAlertNotificationDecision{
			Notify: true,
			Reason: ModelRunEvaluationTrendAlertNotificationReasonFiring,
			Status: ModelRunEvaluationTrendAlertFiring,
		},
	}
	var payload map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s", r.Method)
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Fatalf("content-type = %q", r.Header.Get("Content-Type"))
		}
		if r.Header.Get("X-Orrery-Alert-Tenant") != "tenant-1" || r.Header.Get("X-Orrery-Alert-Rule") != "accuracy-release-gate.trend_failure" {
			t.Fatalf("headers = %+v", r.Header)
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode payload: %v", err)
		}
		w.Header().Set("X-Webhook-Message-ID", "msg-webhook-1")
		w.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()

	sender := NewModelRunEvaluationTrendAlertNotificationWebhookSender(ModelRunEvaluationTrendAlertNotificationWebhookSenderOptions{
		Endpoint:              server.URL,
		Client:                server.Client(),
		Channel:               "webhook",
		AllowPrivateEndpoints: true,
	})
	receipt, err := sender.SendModelRunEvaluationTrendAlertNotification(ctx, event, plan)
	if err != nil {
		t.Fatalf("SendModelRunEvaluationTrendAlertNotification: %v", err)
	}
	if receipt.Channel != "webhook" ||
		receipt.MessageID != "msg-webhook-1" ||
		receipt.Metadata["http_status"] != "202" ||
		receipt.SentAt.IsZero() {
		t.Fatalf("receipt = %+v", receipt)
	}
	if payload["tenant_id"] != "tenant-1" ||
		payload["rule_id"] != "accuracy-release-gate.trend_failure" ||
		payload["status"] != "firing" ||
		payload["reason"] != "firing" {
		t.Fatalf("payload = %+v", payload)
	}
	labels, ok := payload["labels"].(map[string]any)
	if !ok || labels["model"] != "gpt-5" {
		t.Fatalf("payload labels = %+v", payload["labels"])
	}
}

func TestModelRunEvaluationTrendAlertNotificationWebhookSenderRejectsResolvedPrivateEndpoint(t *testing.T) {
	ctx := context.Background()
	observedAt := time.Date(2026, 6, 22, 22, 8, 0, 0, time.UTC)
	event := modelRunEvaluationTrendAlertNotificationTestEvent(ModelRunEvaluationTrendAlertFiring, observedAt)
	client := &http.Client{
		Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			t.Fatal("webhook sender issued request to resolved private endpoint")
			return nil, nil
		}),
	}
	sender := NewModelRunEvaluationTrendAlertNotificationWebhookSender(ModelRunEvaluationTrendAlertNotificationWebhookSenderOptions{
		Endpoint: "https://alerts.example.test/webhook",
		Client:   client,
		Resolver: staticModelRunEvaluationTrendAlertNotificationEndpointResolver{
			"alerts.example.test": {netip.MustParseAddr("169.254.169.254")},
		},
	})

	_, err := sender.SendModelRunEvaluationTrendAlertNotification(ctx, event, ModelRunEvaluationTrendAlertNotificationPlan{})
	if err == nil || !strings.Contains(err.Error(), "not allowed") {
		t.Fatalf("err = %v", err)
	}
}

func TestModelRunEvaluationTrendAlertNotificationWebhookSenderRejectsPrivateEndpointAtDialTime(t *testing.T) {
	ctx := context.Background()
	observedAt := time.Date(2026, 6, 22, 22, 8, 30, 0, time.UTC)
	event := modelRunEvaluationTrendAlertNotificationTestEvent(ModelRunEvaluationTrendAlertFiring, observedAt)
	resolver := sequenceModelRunEvaluationTrendAlertNotificationEndpointResolver{
		"alerts.example.test": {
			{netip.MustParseAddr("203.0.113.10")},
			{netip.MustParseAddr("169.254.169.254")},
		},
	}
	dialer := &recordingModelRunEvaluationTrendAlertNotificationDialer{}
	sender := NewModelRunEvaluationTrendAlertNotificationWebhookSender(ModelRunEvaluationTrendAlertNotificationWebhookSenderOptions{
		Endpoint: "http://alerts.example.test/webhook",
		Resolver: resolver,
		Dialer:   dialer,
	})

	_, err := sender.SendModelRunEvaluationTrendAlertNotification(ctx, event, ModelRunEvaluationTrendAlertNotificationPlan{})
	if err == nil || !strings.Contains(err.Error(), "not allowed") {
		t.Fatalf("err = %v", err)
	}
	if dialer.called {
		t.Fatal("dialer was called for a resolved private endpoint")
	}
}

func TestModelRunEvaluationTrendAlertNotificationWebhookSenderAllowsPrivateEndpointWhenConfigured(t *testing.T) {
	ctx := context.Background()
	observedAt := time.Date(2026, 6, 22, 22, 9, 0, 0, time.UTC)
	event := modelRunEvaluationTrendAlertNotificationTestEvent(ModelRunEvaluationTrendAlertFiring, observedAt)
	var called bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.Header().Set("X-Webhook-Message-ID", "private-message")
		w.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()
	host, _, err := net.SplitHostPort(strings.TrimPrefix(server.URL, "http://"))
	if err != nil {
		t.Fatalf("SplitHostPort: %v", err)
	}
	sender := NewModelRunEvaluationTrendAlertNotificationWebhookSender(ModelRunEvaluationTrendAlertNotificationWebhookSenderOptions{
		Endpoint:              server.URL,
		Client:                server.Client(),
		AllowPrivateEndpoints: true,
		Resolver: staticModelRunEvaluationTrendAlertNotificationEndpointResolver{
			host: {netip.MustParseAddr(host)},
		},
	})

	receipt, err := sender.SendModelRunEvaluationTrendAlertNotification(ctx, event, ModelRunEvaluationTrendAlertNotificationPlan{})
	if err != nil {
		t.Fatalf("SendModelRunEvaluationTrendAlertNotification: %v", err)
	}
	if !called || receipt.MessageID != "private-message" {
		t.Fatalf("called=%v receipt=%+v", called, receipt)
	}
}

func TestModelRunEvaluationTrendAlertNotificationWebhookSenderFailsNon2xx(t *testing.T) {
	ctx := context.Background()
	observedAt := time.Date(2026, 6, 22, 22, 5, 0, 0, time.UTC)
	event := modelRunEvaluationTrendAlertNotificationTestEvent(ModelRunEvaluationTrendAlertFiring, observedAt)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "downstream unavailable", http.StatusBadGateway)
	}))
	defer server.Close()

	sender := NewModelRunEvaluationTrendAlertNotificationWebhookSender(ModelRunEvaluationTrendAlertNotificationWebhookSenderOptions{
		Endpoint:              server.URL,
		Client:                server.Client(),
		AllowPrivateEndpoints: true,
	})
	_, err := sender.SendModelRunEvaluationTrendAlertNotification(ctx, event, ModelRunEvaluationTrendAlertNotificationPlan{})
	if err == nil || !strings.Contains(err.Error(), "502") {
		t.Fatalf("err = %v", err)
	}
}

type staticModelRunEvaluationTrendAlertNotificationEndpointResolver map[string][]netip.Addr

func (r staticModelRunEvaluationTrendAlertNotificationEndpointResolver) LookupNetIP(ctx context.Context, host string) ([]netip.Addr, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return r[host], nil
}

type sequenceModelRunEvaluationTrendAlertNotificationEndpointResolver map[string][][]netip.Addr

func (r sequenceModelRunEvaluationTrendAlertNotificationEndpointResolver) LookupNetIP(ctx context.Context, host string) ([]netip.Addr, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	steps := r[host]
	if len(steps) == 0 {
		return nil, fmt.Errorf("missing host %s", host)
	}
	next := steps[0]
	if len(steps) > 1 {
		r[host] = steps[1:]
	}
	return next, nil
}

type recordingModelRunEvaluationTrendAlertNotificationDialer struct {
	called bool
}

func (d *recordingModelRunEvaluationTrendAlertNotificationDialer) DialContext(ctx context.Context, network string, address string) (net.Conn, error) {
	d.called = true
	return nil, fmt.Errorf("unexpected dial %s %s", network, address)
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}
