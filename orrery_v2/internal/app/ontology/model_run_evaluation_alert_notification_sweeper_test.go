package ontology

import (
	"context"
	"testing"
	"time"

	"orrery_v2/internal/platform/clock"
)

func TestModelRunEvaluationTrendAlertNotificationSweeperDispatchesCurrentState(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 6, 22, 20, 0, 0, 0, time.UTC)
	alerts := NewMemoryModelRunEvaluationTrendAlertAuditStore()
	history := NewMemoryModelRunEvaluationTrendAlertNotificationHistoryStore()
	receipts := NewMemoryModelRunEvaluationTrendAlertNotificationReceiptStore()
	sender := &recordingModelRunEvaluationTrendAlertNotificationSender{}
	event := modelRunEvaluationTrendAlertNotificationTestEvent(ModelRunEvaluationTrendAlertFiring, now.Add(-time.Minute))
	if err := alerts.SaveModelRunEvaluationTrendAlertAuditEvent(ctx, event); err != nil {
		t.Fatalf("SaveModelRunEvaluationTrendAlertAuditEvent: %v", err)
	}

	sweeper := NewModelRunEvaluationTrendAlertNotificationSweeper(ModelRunEvaluationTrendAlertNotificationSweeperOptions{
		Alerts:   alerts,
		History:  history,
		Receipts: receipts,
		Sender:   sender,
		Clock:    clock.NewFake(now),
		Query: ModelRunEvaluationTrendAlertAuditQuery{
			TenantID: "tenant-1",
			Limit:    10,
		},
	})

	if sweeper.Name() != "ontology.model_run_evaluation_alert_notification.dispatcher" {
		t.Fatalf("sweeper name = %q", sweeper.Name())
	}
	if err := sweeper.Sweep(ctx); err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if report := sweeper.LastReport(); report.States != 1 || report.Delivered != 1 || report.Suppressed != 0 || report.Deduplicated != 0 {
		t.Fatalf("report = %+v", report)
	}
	if len(sender.events) != 1 || sender.events[0].Status != ModelRunEvaluationTrendAlertFiring {
		t.Fatalf("sender events = %+v", sender.events)
	}
	page, err := receipts.ListModelRunEvaluationTrendAlertNotificationReceipts(ctx, ModelRunEvaluationTrendAlertNotificationReceiptQuery{
		TenantID: "tenant-1",
		Status:   ModelRunEvaluationTrendAlertNotificationDeliveryDelivered,
		Limit:    10,
	})
	if err != nil {
		t.Fatalf("ListModelRunEvaluationTrendAlertNotificationReceipts: %v", err)
	}
	if len(page.Receipts) != 1 || page.Receipts[0].Channel != "test" {
		t.Fatalf("receipt page = %+v", page)
	}
}

func TestModelRunEvaluationTrendAlertNotificationSweeperSuppressesRepeatCurrentState(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 6, 22, 20, 10, 0, 0, time.UTC)
	alerts := NewMemoryModelRunEvaluationTrendAlertAuditStore()
	history := NewMemoryModelRunEvaluationTrendAlertNotificationHistoryStore()
	sender := &recordingModelRunEvaluationTrendAlertNotificationSender{}
	event := modelRunEvaluationTrendAlertNotificationTestEvent(ModelRunEvaluationTrendAlertFiring, now.Add(-2*time.Minute))
	if err := alerts.SaveModelRunEvaluationTrendAlertAuditEvent(ctx, event); err != nil {
		t.Fatalf("SaveModelRunEvaluationTrendAlertAuditEvent: %v", err)
	}
	if err := history.SaveModelRunEvaluationTrendAlertNotificationHistory(ctx, ModelRunEvaluationTrendAlertNotificationHistoryRecordFromEvent(event, now.Add(-2*time.Minute))); err != nil {
		t.Fatalf("SaveModelRunEvaluationTrendAlertNotificationHistory: %v", err)
	}

	sweeper := NewModelRunEvaluationTrendAlertNotificationSweeper(ModelRunEvaluationTrendAlertNotificationSweeperOptions{
		Alerts:  alerts,
		History: history,
		Sender:  sender,
		Clock:   clock.NewFake(now),
		Policy: ModelRunEvaluationTrendAlertNotificationPolicy{
			RepeatWindow: 10 * time.Minute,
		},
		Query: ModelRunEvaluationTrendAlertAuditQuery{
			TenantID: "tenant-1",
			Limit:    10,
		},
	})

	if err := sweeper.Sweep(ctx); err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if report := sweeper.LastReport(); report.States != 1 || report.Delivered != 0 || report.Suppressed != 1 || report.Deduplicated != 0 {
		t.Fatalf("report = %+v", report)
	}
	if len(sender.events) != 0 {
		t.Fatalf("sender events = %+v", sender.events)
	}
}

func TestModelRunEvaluationTrendAlertNotificationSweeperUsesPolicyStoreWindows(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 6, 22, 20, 20, 0, 0, time.UTC)
	alerts := NewMemoryModelRunEvaluationTrendAlertAuditStore()
	history := NewMemoryModelRunEvaluationTrendAlertNotificationHistoryStore()
	policies := NewMemoryModelRunEvaluationTrendAlertNotificationPolicyStore()
	sender := &recordingModelRunEvaluationTrendAlertNotificationSender{}
	event := modelRunEvaluationTrendAlertNotificationTestEvent(ModelRunEvaluationTrendAlertFiring, now.Add(-2*time.Minute))
	if err := alerts.SaveModelRunEvaluationTrendAlertAuditEvent(ctx, event); err != nil {
		t.Fatalf("SaveModelRunEvaluationTrendAlertAuditEvent: %v", err)
	}
	if err := history.SaveModelRunEvaluationTrendAlertNotificationHistory(ctx, ModelRunEvaluationTrendAlertNotificationHistoryRecordFromEvent(event, now.Add(-2*time.Minute))); err != nil {
		t.Fatalf("SaveModelRunEvaluationTrendAlertNotificationHistory: %v", err)
	}
	if err := policies.SaveModelRunEvaluationTrendAlertNotificationPolicy(ctx, ModelRunEvaluationTrendAlertNotificationPolicyRecord{
		TenantID:     event.TenantID,
		RuleID:       event.RuleID,
		Channel:      "webhook",
		RepeatWindow: 10 * time.Minute,
		Enabled:      true,
	}); err != nil {
		t.Fatalf("SaveModelRunEvaluationTrendAlertNotificationPolicy: %v", err)
	}

	sweeper := NewModelRunEvaluationTrendAlertNotificationSweeper(ModelRunEvaluationTrendAlertNotificationSweeperOptions{
		Alerts:   alerts,
		History:  history,
		Sender:   sender,
		Clock:    clock.NewFake(now),
		Policies: policies,
		Query: ModelRunEvaluationTrendAlertAuditQuery{
			TenantID: "tenant-1",
			Limit:    10,
		},
	})

	if err := sweeper.Sweep(ctx); err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if report := sweeper.LastReport(); report.States != 1 || report.Delivered != 0 || report.Suppressed != 1 || report.Deduplicated != 0 {
		t.Fatalf("report = %+v", report)
	}
	if len(sender.events) != 0 {
		t.Fatalf("sender events = %+v", sender.events)
	}
}

func TestModelRunEvaluationTrendAlertNotificationSweeperDeduplicatesSnapshotLabels(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 6, 22, 20, 30, 0, 0, time.UTC)
	alerts := NewMemoryModelRunEvaluationTrendAlertAuditStore()
	history := NewMemoryModelRunEvaluationTrendAlertNotificationHistoryStore()
	sender := &recordingModelRunEvaluationTrendAlertNotificationSender{}
	older := modelRunEvaluationTrendAlertNotificationTestEvent(ModelRunEvaluationTrendAlertFiring, now.Add(-10*time.Minute))
	older.ID = "alert-audit-older"
	older.Labels["snapshot_recorded_to"] = older.ObservedAt.Format(time.RFC3339Nano)
	latest := modelRunEvaluationTrendAlertNotificationTestEvent(ModelRunEvaluationTrendAlertFiring, now.Add(-5*time.Minute))
	latest.ID = "alert-audit-latest"
	latest.Labels["snapshot_recorded_to"] = latest.ObservedAt.Format(time.RFC3339Nano)
	for _, event := range []ModelRunEvaluationTrendAlertAuditEvent{older, latest} {
		if err := alerts.SaveModelRunEvaluationTrendAlertAuditEvent(ctx, event); err != nil {
			t.Fatalf("SaveModelRunEvaluationTrendAlertAuditEvent: %v", err)
		}
	}

	sweeper := NewModelRunEvaluationTrendAlertNotificationSweeper(ModelRunEvaluationTrendAlertNotificationSweeperOptions{
		Alerts:  alerts,
		History: history,
		Sender:  sender,
		Clock:   clock.NewFake(now),
		Query: ModelRunEvaluationTrendAlertAuditQuery{
			TenantID: "tenant-1",
			Limit:    10,
		},
	})

	if err := sweeper.Sweep(ctx); err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if report := sweeper.LastReport(); report.States != 2 || report.Delivered != 1 || report.Suppressed != 0 || report.Deduplicated != 1 {
		t.Fatalf("report = %+v", report)
	}
	if len(sender.events) != 1 || !sender.events[0].ObservedAt.Equal(latest.ObservedAt) {
		t.Fatalf("sender events = %+v", sender.events)
	}
}
