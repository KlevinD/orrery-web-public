package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"orrery_v2/internal/app/hil"
	"orrery_v2/internal/app/reliability"
	gwoutbound "orrery_v2/internal/gateway/outbound"
)

type notificationFailureCaptureStub struct {
	items []reliability.CapturedFailure
	err   error
}

func (s *notificationFailureCaptureStub) Enqueue(_ context.Context, item reliability.CapturedFailure) error {
	s.items = append(s.items, item)
	return s.err
}

func TestHILNotificationStopsStaleApprovalWithoutSending(t *testing.T) {
	now := time.Now().UTC()
	recipient := hil.ActorRef{Kind: hil.ActorHuman, ID: "human-a"}
	for _, scenario := range []string{"approved", "expired", "transferred", "completed", "missing_task", "decision_closed"} {
		t.Run(scenario, func(t *testing.T) {
			detail := hil.WorkflowInstanceDetail{
				Instance: hil.WorkflowInstance{Status: hil.WorkflowRunning},
				Decision: hil.DecisionRequest{Action: "business_approval", Status: hil.DecisionPending},
				Tasks:    []hil.WorkflowTask{{ID: "task", Status: hil.TaskPending, CurrentAssignee: recipient}},
			}
			switch scenario {
			case "approved":
				detail.Instance.Status = hil.WorkflowApproved
			case "expired":
				detail.Instance.ExpiresAt = now.Add(-time.Second)
			case "transferred":
				detail.Tasks[0].CurrentAssignee.ID = "other-human"
			case "completed":
				detail.Tasks[0].Status = hil.TaskApproved
			case "missing_task":
				detail.Tasks = nil
			case "decision_closed":
				detail.Decision.Status = hil.DecisionResolved
			}
			store := &recordingProductionHILNotificationStore{detail: detail}
			deliveries := &recordingProductionHILGatewayDeliveries{}
			capture := &notificationFailureCaptureStub{}
			d := productionHILWorkflowNotificationDispatcher{workflows: store, profiles: &recordingProductionHILProfileDirectory{}, deliveries: deliveries, failures: capture, now: func() time.Time { return now }}
			if err := d.Dispatch(context.Background(), hil.WorkflowNotification{ID: "notice", TenantID: "tenant", TaskID: "task", Recipient: recipient, Channel: "feishu", ProviderReceipt: "original-delivery"}); err != nil {
				t.Fatal(err)
			}
			if len(deliveries.commands) != 0 || len(capture.items) != 0 || len(store.results) != 1 || !store.results[0].Cancelled || store.results[0].Delivered {
				t.Fatalf("stale approval sent or misreported: %+v %+v", store.results, deliveries.commands)
			}
		})
	}
}

func TestHILFailureDurableCapturePrecedesTerminalHold(t *testing.T) {
	ctx := context.Background()
	control := reliability.NewService(reliability.NewMemoryRepository())
	inbox, err := reliability.NewCaptureInbox(t.TempDir(), control)
	if err != nil {
		t.Fatal(err)
	}
	store := &recordingProductionHILNotificationStore{}
	d := productionHILWorkflowNotificationDispatcher{workflows: store, failures: inbox}
	n := hil.WorkflowNotification{ID: "notice", TenantID: "tenant-a", Channel: "feishu", Recipient: hil.ActorRef{Kind: hil.ActorHuman, ID: "human-a"}, AttemptCount: 40000, ProviderReceipt: "original-delivery"}
	for i := 0; i < 2; i++ {
		if err := d.completeFailure(ctx, n, time.Now(), "approval notification binding is missing"); err != nil {
			t.Fatal(err)
		}
	}
	if inbox.Status().Pending != 1 {
		t.Fatalf("duplicate diagnostic: %+v", inbox.Status())
	}
	if err := inbox.Sweep(ctx); err != nil {
		t.Fatal(err)
	}
	if inbox.Status().Delivered != 1 {
		t.Fatal(inbox.Status())
	}
	result := store.results[0]
	if !result.NeedsAttention || result.Delivered || result.ProviderReceipt != "original-delivery" {
		t.Fatalf("result = %+v", result)
	}
	failed := &notificationFailureCaptureStub{err: errors.New("disk unavailable")}
	d.failures = failed
	store.results = nil
	if err := d.completeFailure(ctx, n, time.Now(), "approval notification could not be queued"); err == nil || len(store.results) != 0 {
		t.Fatal("failure acknowledged without durable evidence")
	}
}

func TestGatewayProviderCauseIsCapturedAndRedacted(t *testing.T) {
	ctx := context.Background()
	control := reliability.NewService(reliability.NewMemoryRepository())
	directory := t.TempDir()
	inbox, err := reliability.NewCaptureInbox(directory, control)
	if err != nil {
		t.Fatal(err)
	}
	observe := productionGatewayNotificationFailureObserver(inbox)
	failure := gwoutbound.DeliveryFailure{Delivery: gwoutbound.Delivery{ID: "delivery", TenantID: "tenant-a", ChannelKey: "weixin", AccountID: "account", HumanProfileID: "human-a"}, Attempt: 1, Code: "channel_send_failed", Cause: errors.New("provider timeout token=private")}
	if err := observe(ctx, failure); err != nil {
		t.Fatal(err)
	}
	if err := observe(ctx, failure); err != nil {
		t.Fatal(err)
	}
	if inbox.Status().Pending != 1 {
		t.Fatal(inbox.Status())
	}
	if err := inbox.Sweep(ctx); err != nil {
		t.Fatal(err)
	}
	// CaptureInbox validates and redacts before writing the existing fault store.
	if inbox.Status().Delivered != 1 {
		t.Fatal(inbox.Status())
	}
	history, err := os.ReadDir(filepath.Join(directory, "history"))
	if err != nil || len(history) != 1 {
		t.Fatalf("history: %v %v", history, err)
	}
	stored, err := os.ReadFile(filepath.Join(directory, "history", history[0].Name()))
	if err != nil || strings.Contains(string(stored), "private") || !strings.Contains(string(stored), "provider timeout") {
		t.Fatalf("unsafe or missing diagnostics: %s %v", stored, err)
	}
	stub := &notificationFailureCaptureStub{}
	if err := productionGatewayNotificationFailureObserver(stub)(ctx, failure); err != nil {
		t.Fatal(err)
	}
	if len(stub.items) != 1 || !strings.Contains(stub.items[0].Observation.Message, "provider timeout") || stub.items[0].Observation.TenantID != "tenant-a" {
		t.Fatal(stub.items)
	}
}
