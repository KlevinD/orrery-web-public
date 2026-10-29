package ontology

import (
	"context"
	"testing"
	"time"

	"orrery_v2/internal/platform/clock"
)

func TestModelRunEvaluationTrendAlertNotificationRetrySweeperAbandonsMaxAttempts(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 6, 22, 21, 0, 0, 0, time.UTC)
	receipts := NewMemoryModelRunEvaluationTrendAlertNotificationReceiptStore()
	if err := receipts.SaveModelRunEvaluationTrendAlertNotificationReceipt(ctx, ModelRunEvaluationTrendAlertNotificationDeliveryReceipt{
		ID:          "receipt-max-attempts",
		TenantID:    "tenant-1",
		RuleID:      "accuracy-release-gate.trend_failure",
		Status:      ModelRunEvaluationTrendAlertNotificationDeliveryFailed,
		AlertStatus: ModelRunEvaluationTrendAlertFiring,
		Reason:      ModelRunEvaluationTrendAlertNotificationReasonFiring,
		Attempt:     3,
		Retryable:   true,
		NextRetryAt: now.Add(-time.Minute),
		SentAt:      now.Add(-30 * time.Minute),
	}); err != nil {
		t.Fatalf("SaveModelRunEvaluationTrendAlertNotificationReceipt: %v", err)
	}

	sweeper := NewModelRunEvaluationTrendAlertNotificationRetrySweeper(ModelRunEvaluationTrendAlertNotificationRetrySweeperOptions{
		Receipts:    receipts,
		Clock:       clock.NewFake(now),
		MaxAttempts: 3,
		Query: ModelRunEvaluationTrendAlertNotificationReceiptQuery{
			TenantID: "tenant-1",
			Limit:    10,
		},
	})

	if sweeper.Name() != "ontology.model_run_evaluation_alert_notification.retry" {
		t.Fatalf("sweeper name = %q", sweeper.Name())
	}
	if err := sweeper.Sweep(ctx); err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if report := sweeper.LastReport(); report.Due != 1 || report.Abandoned != 1 || report.Retryable != 0 {
		t.Fatalf("report = %+v", report)
	}
	page, err := receipts.ListModelRunEvaluationTrendAlertNotificationReceipts(ctx, ModelRunEvaluationTrendAlertNotificationReceiptQuery{
		TenantID: "tenant-1",
		Status:   ModelRunEvaluationTrendAlertNotificationDeliveryFailed,
		Limit:    10,
	})
	if err != nil {
		t.Fatalf("ListModelRunEvaluationTrendAlertNotificationReceipts: %v", err)
	}
	if len(page.Receipts) != 1 ||
		page.Receipts[0].Retryable ||
		!page.Receipts[0].NextRetryAt.IsZero() ||
		page.Receipts[0].ErrorMessage != "max retry attempts reached" ||
		!page.Receipts[0].UpdatedAt.Equal(now) {
		t.Fatalf("receipt page = %+v", page)
	}
}

func TestModelRunEvaluationTrendAlertNotificationRetrySweeperKeepsRetryableBelowMaxAttempts(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 6, 22, 21, 10, 0, 0, time.UTC)
	receipts := NewMemoryModelRunEvaluationTrendAlertNotificationReceiptStore()
	if err := receipts.SaveModelRunEvaluationTrendAlertNotificationReceipt(ctx, ModelRunEvaluationTrendAlertNotificationDeliveryReceipt{
		ID:          "receipt-below-max",
		TenantID:    "tenant-1",
		RuleID:      "accuracy-release-gate.trend_failure",
		Status:      ModelRunEvaluationTrendAlertNotificationDeliveryFailed,
		AlertStatus: ModelRunEvaluationTrendAlertFiring,
		Reason:      ModelRunEvaluationTrendAlertNotificationReasonFiring,
		Attempt:     2,
		Retryable:   true,
		NextRetryAt: now.Add(-time.Minute),
		SentAt:      now.Add(-30 * time.Minute),
	}); err != nil {
		t.Fatalf("SaveModelRunEvaluationTrendAlertNotificationReceipt: %v", err)
	}

	sweeper := NewModelRunEvaluationTrendAlertNotificationRetrySweeper(ModelRunEvaluationTrendAlertNotificationRetrySweeperOptions{
		Receipts:    receipts,
		Clock:       clock.NewFake(now),
		MaxAttempts: 3,
		Query: ModelRunEvaluationTrendAlertNotificationReceiptQuery{
			TenantID: "tenant-1",
			Limit:    10,
		},
	})

	if err := sweeper.Sweep(ctx); err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if report := sweeper.LastReport(); report.Due != 1 || report.Abandoned != 0 || report.Retryable != 1 {
		t.Fatalf("report = %+v", report)
	}
	page, err := receipts.ListModelRunEvaluationTrendAlertNotificationReceipts(ctx, ModelRunEvaluationTrendAlertNotificationReceiptQuery{
		TenantID:  "tenant-1",
		Status:    ModelRunEvaluationTrendAlertNotificationDeliveryFailed,
		Retryable: true,
		Limit:     10,
	})
	if err != nil {
		t.Fatalf("ListModelRunEvaluationTrendAlertNotificationReceipts: %v", err)
	}
	if len(page.Receipts) != 1 || page.Receipts[0].ID != "receipt-below-max" {
		t.Fatalf("receipt page = %+v", page)
	}
}

func TestModelRunEvaluationTrendAlertNotificationRetrySweeperResendsDueReceipt(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 6, 22, 21, 20, 0, 0, time.UTC)
	receipts := NewMemoryModelRunEvaluationTrendAlertNotificationReceiptStore()
	history := NewMemoryModelRunEvaluationTrendAlertNotificationHistoryStore()
	sender := &retryRecordingModelRunEvaluationTrendAlertNotificationSender{sentAt: now}
	labels := map[string]string{
		"tenant_id": "tenant-1",
		"model_id":  "recommendation-ranker",
	}
	if err := receipts.SaveModelRunEvaluationTrendAlertNotificationReceipt(ctx, ModelRunEvaluationTrendAlertNotificationDeliveryReceipt{
		ID:           "receipt-retry-success",
		TenantID:     "tenant-1",
		RuleID:       "accuracy-release-gate.trend_failure",
		Status:       ModelRunEvaluationTrendAlertNotificationDeliveryFailed,
		AlertStatus:  ModelRunEvaluationTrendAlertFiring,
		Reason:       ModelRunEvaluationTrendAlertNotificationReasonFiring,
		Attempt:      1,
		Retryable:    true,
		NextRetryAt:  now.Add(-time.Minute),
		Labels:       labels,
		ErrorMessage: "temporary webhook failure",
		SentAt:       now.Add(-20 * time.Minute),
	}); err != nil {
		t.Fatalf("SaveModelRunEvaluationTrendAlertNotificationReceipt: %v", err)
	}

	sweeper := NewModelRunEvaluationTrendAlertNotificationRetrySweeper(ModelRunEvaluationTrendAlertNotificationRetrySweeperOptions{
		Receipts:    receipts,
		History:     history,
		Sender:      sender,
		Clock:       clock.NewFake(now),
		MaxAttempts: 3,
		Query: ModelRunEvaluationTrendAlertNotificationReceiptQuery{
			TenantID: "tenant-1",
			Limit:    10,
		},
	})

	if err := sweeper.Sweep(ctx); err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if report := sweeper.LastReport(); report.Due != 1 || report.Attempted != 1 || report.Delivered != 1 || report.Failed != 0 || report.Retryable != 0 || report.Abandoned != 0 {
		t.Fatalf("report = %+v", report)
	}
	if len(sender.events) != 1 ||
		sender.events[0].TenantID != "tenant-1" ||
		sender.events[0].RuleID != "accuracy-release-gate.trend_failure" ||
		sender.events[0].Status != ModelRunEvaluationTrendAlertFiring ||
		sender.events[0].Labels["model_id"] != "recommendation-ranker" {
		t.Fatalf("sender events = %+v", sender.events)
	}

	delivered, err := receipts.ListModelRunEvaluationTrendAlertNotificationReceipts(ctx, ModelRunEvaluationTrendAlertNotificationReceiptQuery{
		TenantID: "tenant-1",
		Status:   ModelRunEvaluationTrendAlertNotificationDeliveryDelivered,
		Limit:    10,
	})
	if err != nil {
		t.Fatalf("ListModelRunEvaluationTrendAlertNotificationReceipts delivered: %v", err)
	}
	if len(delivered.Receipts) != 1 ||
		delivered.Receipts[0].Attempt != 2 ||
		delivered.Receipts[0].Channel != "test" ||
		delivered.Receipts[0].MessageID != "retry-message" ||
		!delivered.Receipts[0].SentAt.Equal(now) ||
		delivered.Receipts[0].Retryable {
		t.Fatalf("delivered receipt page = %+v", delivered)
	}
	failed, err := receipts.ListModelRunEvaluationTrendAlertNotificationReceipts(ctx, ModelRunEvaluationTrendAlertNotificationReceiptQuery{
		TenantID: "tenant-1",
		Status:   ModelRunEvaluationTrendAlertNotificationDeliveryFailed,
		Limit:    10,
	})
	if err != nil {
		t.Fatalf("ListModelRunEvaluationTrendAlertNotificationReceipts failed: %v", err)
	}
	if len(failed.Receipts) != 1 ||
		failed.Receipts[0].Retryable ||
		!failed.Receipts[0].NextRetryAt.IsZero() ||
		failed.Receipts[0].ErrorMessage != "temporary webhook failure" {
		t.Fatalf("failed receipt page = %+v", failed)
	}
	record, found, err := history.GetModelRunEvaluationTrendAlertNotificationHistory(ctx, ModelRunEvaluationTrendAlertNotificationHistoryQuery{
		TenantID: "tenant-1",
		RuleID:   "accuracy-release-gate.trend_failure",
		Labels:   labels,
	})
	if err != nil {
		t.Fatalf("GetModelRunEvaluationTrendAlertNotificationHistory: %v", err)
	}
	if !found || record.LastNotificationStatus != ModelRunEvaluationTrendAlertFiring || !record.LastNotificationAt.Equal(now) {
		t.Fatalf("history record = %+v found=%v", record, found)
	}
}

func TestModelRunEvaluationTrendAlertNotificationRetrySweeperSchedulesBackoffOnFailedResend(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 6, 22, 21, 30, 0, 0, time.UTC)
	backoff := 5 * time.Minute
	receipts := NewMemoryModelRunEvaluationTrendAlertNotificationReceiptStore()
	history := NewMemoryModelRunEvaluationTrendAlertNotificationHistoryStore()
	sender := &retryRecordingModelRunEvaluationTrendAlertNotificationSender{err: errModelRunEvaluationTrendAlertNotificationRetrySender}
	if err := receipts.SaveModelRunEvaluationTrendAlertNotificationReceipt(ctx, ModelRunEvaluationTrendAlertNotificationDeliveryReceipt{
		ID:           "receipt-retry-failure",
		TenantID:     "tenant-1",
		RuleID:       "accuracy-release-gate.trend_failure",
		Status:       ModelRunEvaluationTrendAlertNotificationDeliveryFailed,
		AlertStatus:  ModelRunEvaluationTrendAlertFiring,
		Reason:       ModelRunEvaluationTrendAlertNotificationReasonFiring,
		Attempt:      1,
		Retryable:    true,
		NextRetryAt:  now.Add(-time.Minute),
		Labels:       map[string]string{"tenant_id": "tenant-1"},
		ErrorMessage: "temporary webhook failure",
		SentAt:       now.Add(-20 * time.Minute),
	}); err != nil {
		t.Fatalf("SaveModelRunEvaluationTrendAlertNotificationReceipt: %v", err)
	}

	sweeper := NewModelRunEvaluationTrendAlertNotificationRetrySweeper(ModelRunEvaluationTrendAlertNotificationRetrySweeperOptions{
		Receipts:     receipts,
		History:      history,
		Sender:       sender,
		Clock:        clock.NewFake(now),
		MaxAttempts:  3,
		RetryBackoff: backoff,
		Query: ModelRunEvaluationTrendAlertNotificationReceiptQuery{
			TenantID: "tenant-1",
			Limit:    10,
		},
	})

	if err := sweeper.Sweep(ctx); err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if report := sweeper.LastReport(); report.Due != 1 || report.Attempted != 1 || report.Delivered != 0 || report.Failed != 1 || report.Retryable != 1 || report.Abandoned != 0 {
		t.Fatalf("report = %+v", report)
	}
	dueNow, err := receipts.ListModelRunEvaluationTrendAlertNotificationReceipts(ctx, ModelRunEvaluationTrendAlertNotificationReceiptQuery{
		TenantID:       "tenant-1",
		Status:         ModelRunEvaluationTrendAlertNotificationDeliveryFailed,
		Retryable:      true,
		NextRetryDueAt: now,
		Limit:          10,
	})
	if err != nil {
		t.Fatalf("ListModelRunEvaluationTrendAlertNotificationReceipts due now: %v", err)
	}
	if len(dueNow.Receipts) != 0 {
		t.Fatalf("due now receipt page = %+v", dueNow)
	}
	retryable, err := receipts.ListModelRunEvaluationTrendAlertNotificationReceipts(ctx, ModelRunEvaluationTrendAlertNotificationReceiptQuery{
		TenantID:  "tenant-1",
		Status:    ModelRunEvaluationTrendAlertNotificationDeliveryFailed,
		Retryable: true,
		Limit:     10,
	})
	if err != nil {
		t.Fatalf("ListModelRunEvaluationTrendAlertNotificationReceipts retryable: %v", err)
	}
	if len(retryable.Receipts) != 1 ||
		retryable.Receipts[0].Attempt != 2 ||
		!retryable.Receipts[0].Retryable ||
		!retryable.Receipts[0].NextRetryAt.Equal(now.Add(backoff)) ||
		retryable.Receipts[0].ErrorMessage != errModelRunEvaluationTrendAlertNotificationRetrySender.Error() {
		t.Fatalf("retryable receipt page = %+v", retryable)
	}
}

var errModelRunEvaluationTrendAlertNotificationRetrySender = errStatic("retry send failed")

type retryRecordingModelRunEvaluationTrendAlertNotificationSender struct {
	events []ModelRunEvaluationTrendAlertAuditEvent
	sentAt time.Time
	err    error
}

func (s *retryRecordingModelRunEvaluationTrendAlertNotificationSender) SendModelRunEvaluationTrendAlertNotification(ctx context.Context, event ModelRunEvaluationTrendAlertAuditEvent, plan ModelRunEvaluationTrendAlertNotificationPlan) (ModelRunEvaluationTrendAlertNotificationReceipt, error) {
	if err := ctx.Err(); err != nil {
		return ModelRunEvaluationTrendAlertNotificationReceipt{}, err
	}
	s.events = append(s.events, event)
	if s.err != nil {
		return ModelRunEvaluationTrendAlertNotificationReceipt{}, s.err
	}
	return ModelRunEvaluationTrendAlertNotificationReceipt{
		Channel:   "test",
		MessageID: "retry-message",
		SentAt:    s.sentAt,
	}, nil
}

type errStatic string

func (e errStatic) Error() string {
	return string(e)
}
