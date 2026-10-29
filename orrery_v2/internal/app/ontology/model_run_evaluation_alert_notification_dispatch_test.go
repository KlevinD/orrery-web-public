package ontology

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func TestDispatchModelRunEvaluationTrendAlertNotificationSendsAndRecordsHistory(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryModelRunEvaluationTrendAlertNotificationHistoryStore()
	sender := &recordingModelRunEvaluationTrendAlertNotificationSender{}
	observedAt := time.Date(2026, 6, 22, 19, 0, 0, 0, time.UTC)
	event := modelRunEvaluationTrendAlertNotificationTestEvent(ModelRunEvaluationTrendAlertFiring, observedAt)

	result, err := DispatchModelRunEvaluationTrendAlertNotification(ctx, store, sender, event, ModelRunEvaluationTrendAlertNotificationPolicy{}, observedAt)
	if err != nil {
		t.Fatalf("DispatchModelRunEvaluationTrendAlertNotification: %v", err)
	}
	if !result.Delivered ||
		!result.Plan.Decision.Notify ||
		result.Receipt.Channel != "test" ||
		result.Receipt.MessageID != "message-1" ||
		len(sender.events) != 1 {
		t.Fatalf("result = %+v sender events=%d", result, len(sender.events))
	}

	record, found, err := store.GetModelRunEvaluationTrendAlertNotificationHistory(ctx, ModelRunEvaluationTrendAlertNotificationHistoryQuery{
		TenantID: event.TenantID,
		RuleID:   event.RuleID,
		Labels:   event.Labels,
	})
	if err != nil {
		t.Fatalf("GetModelRunEvaluationTrendAlertNotificationHistory: %v", err)
	}
	if !found || record.LastNotificationStatus != ModelRunEvaluationTrendAlertFiring || !record.LastNotificationAt.Equal(observedAt) {
		t.Fatalf("record = %+v found=%v", record, found)
	}
}

func TestDispatchModelRunEvaluationTrendAlertNotificationDoesNotRecordFailedSend(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryModelRunEvaluationTrendAlertNotificationHistoryStore()
	sender := &recordingModelRunEvaluationTrendAlertNotificationSender{err: fmt.Errorf("send failed")}
	observedAt := time.Date(2026, 6, 22, 19, 10, 0, 0, time.UTC)
	event := modelRunEvaluationTrendAlertNotificationTestEvent(ModelRunEvaluationTrendAlertFiring, observedAt)

	result, err := DispatchModelRunEvaluationTrendAlertNotification(ctx, store, sender, event, ModelRunEvaluationTrendAlertNotificationPolicy{}, observedAt)
	if err == nil {
		t.Fatal("DispatchModelRunEvaluationTrendAlertNotification succeeded, want send error")
	}
	if result.Delivered || !result.Plan.Decision.Notify || len(sender.events) != 1 {
		t.Fatalf("result = %+v sender events=%d", result, len(sender.events))
	}

	_, found, getErr := store.GetModelRunEvaluationTrendAlertNotificationHistory(ctx, ModelRunEvaluationTrendAlertNotificationHistoryQuery{
		TenantID: event.TenantID,
		RuleID:   event.RuleID,
		Labels:   event.Labels,
	})
	if getErr != nil {
		t.Fatalf("GetModelRunEvaluationTrendAlertNotificationHistory: %v", getErr)
	}
	if found {
		t.Fatalf("failed send recorded notification history")
	}
}

func TestDispatchModelRunEvaluationTrendAlertNotificationWithReceiptsRecordsDeliveryAttempt(t *testing.T) {
	ctx := context.Background()
	history := NewMemoryModelRunEvaluationTrendAlertNotificationHistoryStore()
	receipts := NewMemoryModelRunEvaluationTrendAlertNotificationReceiptStore()
	sender := &recordingModelRunEvaluationTrendAlertNotificationSender{}
	observedAt := time.Date(2026, 6, 22, 19, 15, 0, 0, time.UTC)
	event := modelRunEvaluationTrendAlertNotificationTestEvent(ModelRunEvaluationTrendAlertFiring, observedAt)

	result, err := DispatchModelRunEvaluationTrendAlertNotificationWithReceipts(ctx, history, receipts, sender, event, ModelRunEvaluationTrendAlertNotificationPolicy{}, observedAt)
	if err != nil {
		t.Fatalf("DispatchModelRunEvaluationTrendAlertNotificationWithReceipts: %v", err)
	}
	if !result.Delivered || result.DeliveryReceipt.Status != ModelRunEvaluationTrendAlertNotificationDeliveryDelivered {
		t.Fatalf("result = %+v", result)
	}
	page, err := receipts.ListModelRunEvaluationTrendAlertNotificationReceipts(ctx, ModelRunEvaluationTrendAlertNotificationReceiptQuery{
		TenantID: "tenant-1",
		Status:   ModelRunEvaluationTrendAlertNotificationDeliveryDelivered,
		Limit:    10,
	})
	if err != nil {
		t.Fatalf("ListModelRunEvaluationTrendAlertNotificationReceipts: %v", err)
	}
	if len(page.Receipts) != 1 ||
		page.Receipts[0].Channel != "test" ||
		page.Receipts[0].MessageID != "message-1" ||
		page.Receipts[0].Attempt != 1 ||
		page.Receipts[0].Retryable {
		t.Fatalf("receipt page = %+v", page)
	}
	record, found, err := history.GetModelRunEvaluationTrendAlertNotificationHistory(ctx, ModelRunEvaluationTrendAlertNotificationHistoryQuery{
		TenantID: event.TenantID,
		RuleID:   event.RuleID,
		Labels:   event.Labels,
	})
	if err != nil {
		t.Fatalf("GetModelRunEvaluationTrendAlertNotificationHistory: %v", err)
	}
	if !found || !record.LastNotificationAt.Equal(observedAt) {
		t.Fatalf("history record = %+v found=%v", record, found)
	}
}

func TestDispatchModelRunEvaluationTrendAlertNotificationWithReceiptsRecordsFailedAttemptWithoutHistory(t *testing.T) {
	ctx := context.Background()
	history := NewMemoryModelRunEvaluationTrendAlertNotificationHistoryStore()
	receipts := NewMemoryModelRunEvaluationTrendAlertNotificationReceiptStore()
	sender := &recordingModelRunEvaluationTrendAlertNotificationSender{err: fmt.Errorf("send failed")}
	observedAt := time.Date(2026, 6, 22, 19, 18, 0, 0, time.UTC)
	event := modelRunEvaluationTrendAlertNotificationTestEvent(ModelRunEvaluationTrendAlertFiring, observedAt)

	result, err := DispatchModelRunEvaluationTrendAlertNotificationWithReceipts(ctx, history, receipts, sender, event, ModelRunEvaluationTrendAlertNotificationPolicy{}, observedAt)
	if err == nil {
		t.Fatal("DispatchModelRunEvaluationTrendAlertNotificationWithReceipts succeeded, want send error")
	}
	if result.Delivered || result.DeliveryReceipt.Status != ModelRunEvaluationTrendAlertNotificationDeliveryFailed {
		t.Fatalf("result = %+v", result)
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
	if len(page.Receipts) != 1 ||
		page.Receipts[0].Attempt != 1 ||
		!page.Receipts[0].Retryable ||
		!page.Receipts[0].NextRetryAt.Equal(observedAt) ||
		page.Receipts[0].ErrorMessage != "send failed" {
		t.Fatalf("failed receipt page = %+v", page)
	}
	_, found, getErr := history.GetModelRunEvaluationTrendAlertNotificationHistory(ctx, ModelRunEvaluationTrendAlertNotificationHistoryQuery{
		TenantID: event.TenantID,
		RuleID:   event.RuleID,
		Labels:   event.Labels,
	})
	if getErr != nil {
		t.Fatalf("GetModelRunEvaluationTrendAlertNotificationHistory: %v", getErr)
	}
	if found {
		t.Fatalf("failed send recorded notification history")
	}
}

func TestDispatchModelRunEvaluationTrendAlertNotificationSkipsSuppressedDecision(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryModelRunEvaluationTrendAlertNotificationHistoryStore()
	sender := &recordingModelRunEvaluationTrendAlertNotificationSender{}
	previousAt := time.Date(2026, 6, 22, 19, 20, 0, 0, time.UTC)
	event := modelRunEvaluationTrendAlertNotificationTestEvent(ModelRunEvaluationTrendAlertFiring, previousAt)
	if err := store.SaveModelRunEvaluationTrendAlertNotificationHistory(ctx, ModelRunEvaluationTrendAlertNotificationHistoryRecordFromEvent(event, previousAt)); err != nil {
		t.Fatalf("SaveModelRunEvaluationTrendAlertNotificationHistory: %v", err)
	}

	now := previousAt.Add(2 * time.Minute)
	result, err := DispatchModelRunEvaluationTrendAlertNotification(ctx, store, sender, event, ModelRunEvaluationTrendAlertNotificationPolicy{
		RepeatWindow: 10 * time.Minute,
	}, now)
	if err != nil {
		t.Fatalf("DispatchModelRunEvaluationTrendAlertNotification: %v", err)
	}
	if result.Delivered ||
		result.Plan.Decision.Notify ||
		result.Plan.Decision.Reason != ModelRunEvaluationTrendAlertNotificationReasonRepeatWindow ||
		len(sender.events) != 0 {
		t.Fatalf("result = %+v sender events=%d", result, len(sender.events))
	}
}

type recordingModelRunEvaluationTrendAlertNotificationSender struct {
	events []ModelRunEvaluationTrendAlertAuditEvent
	err    error
}

func (s *recordingModelRunEvaluationTrendAlertNotificationSender) SendModelRunEvaluationTrendAlertNotification(ctx context.Context, event ModelRunEvaluationTrendAlertAuditEvent, plan ModelRunEvaluationTrendAlertNotificationPlan) (ModelRunEvaluationTrendAlertNotificationReceipt, error) {
	if err := ctx.Err(); err != nil {
		return ModelRunEvaluationTrendAlertNotificationReceipt{}, err
	}
	s.events = append(s.events, event)
	if s.err != nil {
		return ModelRunEvaluationTrendAlertNotificationReceipt{}, s.err
	}
	return ModelRunEvaluationTrendAlertNotificationReceipt{
		Channel:   "test",
		MessageID: "message-1",
		SentAt:    event.ObservedAt,
	}, nil
}
