package ontology

import (
	"context"
	"testing"
	"time"
)

func TestMemoryModelRunEvaluationTrendAlertNotificationReceiptStoreSaveAndList(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryModelRunEvaluationTrendAlertNotificationReceiptStore()
	deliveredAt := time.Date(2026, 6, 22, 18, 0, 0, 0, time.UTC)
	failedAt := deliveredAt.Add(5 * time.Minute)

	if err := store.SaveModelRunEvaluationTrendAlertNotificationReceipt(ctx, ModelRunEvaluationTrendAlertNotificationDeliveryReceipt{
		ID:          "receipt-delivered",
		TenantID:    "tenant-1",
		RuleID:      "accuracy-release-gate.trend_failure",
		Status:      ModelRunEvaluationTrendAlertNotificationDeliveryDelivered,
		AlertStatus: ModelRunEvaluationTrendAlertFiring,
		Reason:      ModelRunEvaluationTrendAlertNotificationReasonFiring,
		Channel:     "webhook",
		MessageID:   "msg-1",
		Labels: map[string]string{
			"tenant_id": "tenant-1",
			"engine":    "ontology_designer",
			"model":     "gpt-5",
		},
		Metadata: map[string]string{"provider": "internal-webhook"},
		SentAt:   deliveredAt,
	}); err != nil {
		t.Fatalf("SaveModelRunEvaluationTrendAlertNotificationReceipt delivered: %v", err)
	}
	if err := store.SaveModelRunEvaluationTrendAlertNotificationReceipt(ctx, ModelRunEvaluationTrendAlertNotificationDeliveryReceipt{
		ID:           "receipt-failed",
		TenantID:     "tenant-1",
		RuleID:       "accuracy-release-gate.trend_failure",
		Status:       ModelRunEvaluationTrendAlertNotificationDeliveryFailed,
		AlertStatus:  ModelRunEvaluationTrendAlertFiring,
		Reason:       ModelRunEvaluationTrendAlertNotificationReasonFiring,
		Channel:      "webhook",
		Attempt:      2,
		Retryable:    true,
		NextRetryAt:  failedAt.Add(10 * time.Minute),
		ErrorMessage: "connection refused",
		Labels:       map[string]string{"tenant_id": "tenant-1", "engine": "ontology_designer"},
		SentAt:       failedAt,
	}); err != nil {
		t.Fatalf("SaveModelRunEvaluationTrendAlertNotificationReceipt failed: %v", err)
	}

	page, err := store.ListModelRunEvaluationTrendAlertNotificationReceipts(ctx, ModelRunEvaluationTrendAlertNotificationReceiptQuery{
		TenantID: "tenant-1",
		Status:   ModelRunEvaluationTrendAlertNotificationDeliveryDelivered,
		Channel:  "webhook",
		Limit:    10,
	})
	if err != nil {
		t.Fatalf("ListModelRunEvaluationTrendAlertNotificationReceipts: %v", err)
	}
	if page.Page.Total != 1 || len(page.Receipts) != 1 {
		t.Fatalf("page = %+v", page)
	}
	receipt := page.Receipts[0]
	if receipt.ID != "receipt-delivered" ||
		receipt.MessageID != "msg-1" ||
		receipt.Metadata["provider"] != "internal-webhook" ||
		!receipt.SentAt.Equal(deliveredAt) {
		t.Fatalf("receipt = %+v", receipt)
	}
	receipt.Labels["model"] = "mutated"
	next, err := store.ListModelRunEvaluationTrendAlertNotificationReceipts(ctx, ModelRunEvaluationTrendAlertNotificationReceiptQuery{
		TenantID: "tenant-1",
		Status:   ModelRunEvaluationTrendAlertNotificationDeliveryDelivered,
		Limit:    10,
	})
	if err != nil {
		t.Fatalf("ListModelRunEvaluationTrendAlertNotificationReceipts next: %v", err)
	}
	if next.Receipts[0].Labels["model"] != "gpt-5" {
		t.Fatalf("receipt labels were not cloned: %+v", next.Receipts[0].Labels)
	}
	failed, err := store.ListModelRunEvaluationTrendAlertNotificationReceipts(ctx, ModelRunEvaluationTrendAlertNotificationReceiptQuery{
		TenantID:  "tenant-1",
		Status:    ModelRunEvaluationTrendAlertNotificationDeliveryFailed,
		Retryable: true,
		Limit:     10,
	})
	if err != nil {
		t.Fatalf("ListModelRunEvaluationTrendAlertNotificationReceipts failed: %v", err)
	}
	if len(failed.Receipts) != 1 ||
		failed.Receipts[0].Attempt != 2 ||
		!failed.Receipts[0].Retryable ||
		!failed.Receipts[0].NextRetryAt.Equal(failedAt.Add(10*time.Minute)) {
		t.Fatalf("failed receipt = %+v", failed)
	}
}

func TestPlatformModelRunEvaluationTrendAlertNotificationReceiptStoreSaveAndList(t *testing.T) {
	ctx := context.Background()
	core := newMigratedAlertAuditCore(t, "platform-ontology-alert-notification-receipts")
	store := NewPlatformModelRunEvaluationTrendAlertNotificationReceiptStore(core)
	sentAt := time.Date(2026, 6, 22, 18, 30, 0, 0, time.UTC)

	if err := store.SaveModelRunEvaluationTrendAlertNotificationReceipt(ctx, ModelRunEvaluationTrendAlertNotificationDeliveryReceipt{
		ID:          "receipt-platform-delivered",
		TenantID:    "tenant-1",
		RuleID:      "accuracy-release-gate.trend_failure",
		Status:      ModelRunEvaluationTrendAlertNotificationDeliveryDelivered,
		AlertStatus: ModelRunEvaluationTrendAlertResolved,
		Reason:      ModelRunEvaluationTrendAlertNotificationReasonResolved,
		Channel:     "log",
		MessageID:   "msg-platform-1",
		Attempt:     1,
		Labels:      map[string]string{"tenant_id": "tenant-1", "metric_threshold_rule_id": "accuracy-release-gate"},
		Metadata:    map[string]string{"sink": "dev-log"},
		SentAt:      sentAt,
	}); err != nil {
		t.Fatalf("SaveModelRunEvaluationTrendAlertNotificationReceipt: %v", err)
	}

	page, err := store.ListModelRunEvaluationTrendAlertNotificationReceipts(ctx, ModelRunEvaluationTrendAlertNotificationReceiptQuery{
		TenantID: "tenant-1",
		RuleID:   "accuracy-release-gate.trend_failure",
		Status:   ModelRunEvaluationTrendAlertNotificationDeliveryDelivered,
		Channel:  "log",
		Limit:    10,
	})
	if err != nil {
		t.Fatalf("ListModelRunEvaluationTrendAlertNotificationReceipts: %v", err)
	}
	if page.Page.Total != 1 || len(page.Receipts) != 1 {
		t.Fatalf("page = %+v", page)
	}
	receipt := page.Receipts[0]
	if receipt.ID != "receipt-platform-delivered" ||
		receipt.AlertStatus != ModelRunEvaluationTrendAlertResolved ||
		receipt.Reason != ModelRunEvaluationTrendAlertNotificationReasonResolved ||
		receipt.Attempt != 1 ||
		receipt.Metadata["sink"] != "dev-log" ||
		!receipt.SentAt.Equal(sentAt) {
		t.Fatalf("receipt = %+v", receipt)
	}
}

func TestModelRunEvaluationTrendAlertNotificationReceiptStoreSummarizesReceipts(t *testing.T) {
	ctx := context.Background()
	sentAt := time.Date(2026, 6, 22, 18, 45, 0, 0, time.UTC)
	for name, store := range map[string]ModelRunEvaluationTrendAlertNotificationReceiptStore{
		"memory":   NewMemoryModelRunEvaluationTrendAlertNotificationReceiptStore(),
		"platform": NewPlatformModelRunEvaluationTrendAlertNotificationReceiptStore(newMigratedAlertAuditCore(t, "platform-ontology-alert-notification-summary")),
	} {
		t.Run(name, func(t *testing.T) {
			receipts := []ModelRunEvaluationTrendAlertNotificationDeliveryReceipt{
				{
					ID:          "tenant-1-delivered-log",
					TenantID:    "tenant-1",
					RuleID:      "accuracy-release-gate.trend_failure",
					Status:      ModelRunEvaluationTrendAlertNotificationDeliveryDelivered,
					AlertStatus: ModelRunEvaluationTrendAlertFiring,
					Reason:      ModelRunEvaluationTrendAlertNotificationReasonFiring,
					Channel:     "log",
					SentAt:      sentAt,
				},
				{
					ID:          "tenant-1-failed-webhook-1",
					TenantID:    "tenant-1",
					RuleID:      "accuracy-release-gate.trend_failure",
					Status:      ModelRunEvaluationTrendAlertNotificationDeliveryFailed,
					AlertStatus: ModelRunEvaluationTrendAlertFiring,
					Reason:      ModelRunEvaluationTrendAlertNotificationReasonFiring,
					Channel:     "stored-webhook",
					Retryable:   true,
					SentAt:      sentAt.Add(time.Minute),
				},
				{
					ID:          "tenant-1-failed-webhook-2",
					TenantID:    "tenant-1",
					RuleID:      "accuracy-release-gate.trend_failure",
					Status:      ModelRunEvaluationTrendAlertNotificationDeliveryFailed,
					AlertStatus: ModelRunEvaluationTrendAlertFiring,
					Reason:      ModelRunEvaluationTrendAlertNotificationReasonFiring,
					Channel:     "stored-webhook",
					Retryable:   true,
					SentAt:      sentAt.Add(2 * time.Minute),
				},
				{
					ID:          "tenant-1-failed-webhook-final",
					TenantID:    "tenant-1",
					RuleID:      "accuracy-release-gate.trend_failure",
					Status:      ModelRunEvaluationTrendAlertNotificationDeliveryFailed,
					AlertStatus: ModelRunEvaluationTrendAlertFiring,
					Reason:      ModelRunEvaluationTrendAlertNotificationReasonFiring,
					Channel:     "stored-webhook",
					Retryable:   false,
					SentAt:      sentAt.Add(3 * time.Minute),
				},
				{
					ID:          "tenant-2-failed-webhook",
					TenantID:    "tenant-2",
					RuleID:      "accuracy-release-gate.trend_failure",
					Status:      ModelRunEvaluationTrendAlertNotificationDeliveryFailed,
					AlertStatus: ModelRunEvaluationTrendAlertFiring,
					Reason:      ModelRunEvaluationTrendAlertNotificationReasonFiring,
					Channel:     "stored-webhook",
					Retryable:   true,
					SentAt:      sentAt.Add(4 * time.Minute),
				},
			}
			for _, receipt := range receipts {
				if err := store.SaveModelRunEvaluationTrendAlertNotificationReceipt(ctx, receipt); err != nil {
					t.Fatalf("SaveModelRunEvaluationTrendAlertNotificationReceipt(%s): %v", receipt.ID, err)
				}
			}

			page, err := store.SummarizeModelRunEvaluationTrendAlertNotificationReceipts(ctx, ModelRunEvaluationTrendAlertNotificationReceiptQuery{
				TenantID: "tenant-1",
				RuleID:   "accuracy-release-gate.trend_failure",
				Limit:    10,
			})
			if err != nil {
				t.Fatalf("SummarizeModelRunEvaluationTrendAlertNotificationReceipts: %v", err)
			}
			if page.Page.Total != 3 || len(page.Summaries) != 3 {
				t.Fatalf("summary page = %+v", page)
			}
			assertModelRunEvaluationTrendAlertNotificationReceiptSummary(t, page.Summaries, "tenant-1", "accuracy-release-gate.trend_failure", "stored-webhook", ModelRunEvaluationTrendAlertNotificationDeliveryFailed, true, 2)
			assertModelRunEvaluationTrendAlertNotificationReceiptSummary(t, page.Summaries, "tenant-1", "accuracy-release-gate.trend_failure", "stored-webhook", ModelRunEvaluationTrendAlertNotificationDeliveryFailed, false, 1)
			assertModelRunEvaluationTrendAlertNotificationReceiptSummary(t, page.Summaries, "tenant-1", "accuracy-release-gate.trend_failure", "log", ModelRunEvaluationTrendAlertNotificationDeliveryDelivered, false, 1)

			failed, err := store.SummarizeModelRunEvaluationTrendAlertNotificationReceipts(ctx, ModelRunEvaluationTrendAlertNotificationReceiptQuery{
				TenantID:  "tenant-1",
				Status:    ModelRunEvaluationTrendAlertNotificationDeliveryFailed,
				Channel:   "stored-webhook",
				Retryable: true,
				Limit:     1,
			})
			if err != nil {
				t.Fatalf("SummarizeModelRunEvaluationTrendAlertNotificationReceipts failed filter: %v", err)
			}
			if failed.Page.Total != 1 || len(failed.Summaries) != 1 || failed.Summaries[0].Count != 2 {
				t.Fatalf("failed summary page = %+v", failed)
			}

			finalFailed, err := store.SummarizeModelRunEvaluationTrendAlertNotificationReceipts(ctx, ModelRunEvaluationTrendAlertNotificationReceiptQuery{
				TenantID:     "tenant-1",
				Status:       ModelRunEvaluationTrendAlertNotificationDeliveryFailed,
				Channel:      "stored-webhook",
				Retryable:    false,
				RetryableSet: true,
				Limit:        10,
			})
			if err != nil {
				t.Fatalf("SummarizeModelRunEvaluationTrendAlertNotificationReceipts final failed filter: %v", err)
			}
			if finalFailed.Page.Total != 1 || len(finalFailed.Summaries) != 1 || finalFailed.Summaries[0].Retryable || finalFailed.Summaries[0].Count != 1 {
				t.Fatalf("final failed summary page = %+v", finalFailed)
			}
		})
	}
}

func TestModelRunEvaluationTrendAlertNotificationReceiptStoreFiltersRetryableFalse(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 6, 22, 18, 55, 0, 0, time.UTC)
	for name, store := range map[string]ModelRunEvaluationTrendAlertNotificationReceiptStore{
		"memory":   NewMemoryModelRunEvaluationTrendAlertNotificationReceiptStore(),
		"platform": NewPlatformModelRunEvaluationTrendAlertNotificationReceiptStore(newMigratedAlertAuditCore(t, "platform-ontology-alert-notification-retryable-false")),
	} {
		t.Run(name, func(t *testing.T) {
			for _, receipt := range []ModelRunEvaluationTrendAlertNotificationDeliveryReceipt{
				{
					ID:          "retryable-failed",
					TenantID:    "tenant-1",
					RuleID:      "accuracy-release-gate.trend_failure",
					Status:      ModelRunEvaluationTrendAlertNotificationDeliveryFailed,
					AlertStatus: ModelRunEvaluationTrendAlertFiring,
					Reason:      ModelRunEvaluationTrendAlertNotificationReasonFiring,
					Channel:     "stored-webhook",
					Retryable:   true,
					SentAt:      now,
				},
				{
					ID:          "final-failed",
					TenantID:    "tenant-1",
					RuleID:      "accuracy-release-gate.trend_failure",
					Status:      ModelRunEvaluationTrendAlertNotificationDeliveryFailed,
					AlertStatus: ModelRunEvaluationTrendAlertFiring,
					Reason:      ModelRunEvaluationTrendAlertNotificationReasonFiring,
					Channel:     "stored-webhook",
					Retryable:   false,
					SentAt:      now.Add(time.Minute),
				},
			} {
				if err := store.SaveModelRunEvaluationTrendAlertNotificationReceipt(ctx, receipt); err != nil {
					t.Fatalf("SaveModelRunEvaluationTrendAlertNotificationReceipt(%s): %v", receipt.ID, err)
				}
			}

			page, err := store.ListModelRunEvaluationTrendAlertNotificationReceipts(ctx, ModelRunEvaluationTrendAlertNotificationReceiptQuery{
				TenantID:     "tenant-1",
				Status:       ModelRunEvaluationTrendAlertNotificationDeliveryFailed,
				Retryable:    false,
				RetryableSet: true,
				Limit:        10,
			})
			if err != nil {
				t.Fatalf("ListModelRunEvaluationTrendAlertNotificationReceipts: %v", err)
			}
			if page.Page.Total != 1 || len(page.Receipts) != 1 || page.Receipts[0].ID != "final-failed" || page.Receipts[0].Retryable {
				t.Fatalf("retryable false page = %+v", page)
			}
		})
	}
}

func TestModelRunEvaluationTrendAlertNotificationReceiptStoreListsDueRetries(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 6, 22, 19, 0, 0, 0, time.UTC)
	for name, store := range map[string]ModelRunEvaluationTrendAlertNotificationReceiptStore{
		"memory":   NewMemoryModelRunEvaluationTrendAlertNotificationReceiptStore(),
		"platform": NewPlatformModelRunEvaluationTrendAlertNotificationReceiptStore(newMigratedAlertAuditCore(t, "platform-ontology-alert-notification-due-retries")),
	} {
		t.Run(name, func(t *testing.T) {
			for _, receipt := range []ModelRunEvaluationTrendAlertNotificationDeliveryReceipt{
				{
					ID:          "due-retry",
					TenantID:    "tenant-1",
					RuleID:      "accuracy-release-gate.trend_failure",
					Status:      ModelRunEvaluationTrendAlertNotificationDeliveryFailed,
					AlertStatus: ModelRunEvaluationTrendAlertFiring,
					Reason:      ModelRunEvaluationTrendAlertNotificationReasonFiring,
					Retryable:   true,
					NextRetryAt: now.Add(-time.Minute),
					SentAt:      now.Add(-10 * time.Minute),
				},
				{
					ID:          "future-retry",
					TenantID:    "tenant-1",
					RuleID:      "accuracy-release-gate.trend_failure",
					Status:      ModelRunEvaluationTrendAlertNotificationDeliveryFailed,
					AlertStatus: ModelRunEvaluationTrendAlertFiring,
					Reason:      ModelRunEvaluationTrendAlertNotificationReasonFiring,
					Retryable:   true,
					NextRetryAt: now.Add(time.Minute),
					SentAt:      now.Add(-9 * time.Minute),
				},
				{
					ID:          "not-retryable",
					TenantID:    "tenant-1",
					RuleID:      "accuracy-release-gate.trend_failure",
					Status:      ModelRunEvaluationTrendAlertNotificationDeliveryFailed,
					AlertStatus: ModelRunEvaluationTrendAlertFiring,
					Reason:      ModelRunEvaluationTrendAlertNotificationReasonFiring,
					Retryable:   false,
					NextRetryAt: now.Add(-time.Minute),
					SentAt:      now.Add(-8 * time.Minute),
				},
			} {
				if err := store.SaveModelRunEvaluationTrendAlertNotificationReceipt(ctx, receipt); err != nil {
					t.Fatalf("SaveModelRunEvaluationTrendAlertNotificationReceipt(%s): %v", receipt.ID, err)
				}
			}

			page, err := store.ListModelRunEvaluationTrendAlertNotificationReceipts(ctx, ModelRunEvaluationTrendAlertNotificationReceiptQuery{
				TenantID:       "tenant-1",
				Status:         ModelRunEvaluationTrendAlertNotificationDeliveryFailed,
				Retryable:      true,
				NextRetryDueAt: now,
				Limit:          10,
			})
			if err != nil {
				t.Fatalf("ListModelRunEvaluationTrendAlertNotificationReceipts: %v", err)
			}
			if len(page.Receipts) != 1 || page.Receipts[0].ID != "due-retry" {
				t.Fatalf("due retry page = %+v", page)
			}
		})
	}
}

func assertModelRunEvaluationTrendAlertNotificationReceiptSummary(t *testing.T, summaries []ModelRunEvaluationTrendAlertNotificationReceiptSummary, tenantID string, ruleID string, channel string, status ModelRunEvaluationTrendAlertNotificationDeliveryStatus, retryable bool, count int) {
	t.Helper()
	for _, summary := range summaries {
		if summary.TenantID == tenantID &&
			summary.RuleID == ruleID &&
			summary.Channel == channel &&
			summary.Status == status &&
			summary.Retryable == retryable {
			if summary.Count != count {
				t.Fatalf("summary count for %s/%s/%s/%s/%t = %d, want %d", tenantID, ruleID, channel, status, retryable, summary.Count, count)
			}
			return
		}
	}
	t.Fatalf("summary not found for %s/%s/%s/%s/%t in %+v", tenantID, ruleID, channel, status, retryable, summaries)
}

func TestModelRunEvaluationTrendAlertNotificationReceiptStoreMarksRetryAbandoned(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 6, 22, 19, 30, 0, 0, time.UTC)
	for name, store := range map[string]ModelRunEvaluationTrendAlertNotificationReceiptStore{
		"memory":   NewMemoryModelRunEvaluationTrendAlertNotificationReceiptStore(),
		"platform": NewPlatformModelRunEvaluationTrendAlertNotificationReceiptStore(newMigratedAlertAuditCore(t, "platform-ontology-alert-notification-abandon-retry")),
	} {
		t.Run(name, func(t *testing.T) {
			if err := store.SaveModelRunEvaluationTrendAlertNotificationReceipt(ctx, ModelRunEvaluationTrendAlertNotificationDeliveryReceipt{
				ID:          "retry-abandoned",
				TenantID:    "tenant-1",
				RuleID:      "accuracy-release-gate.trend_failure",
				Status:      ModelRunEvaluationTrendAlertNotificationDeliveryFailed,
				AlertStatus: ModelRunEvaluationTrendAlertFiring,
				Reason:      ModelRunEvaluationTrendAlertNotificationReasonFiring,
				Attempt:     3,
				Retryable:   true,
				NextRetryAt: now.Add(-time.Minute),
				SentAt:      now.Add(-10 * time.Minute),
			}); err != nil {
				t.Fatalf("SaveModelRunEvaluationTrendAlertNotificationReceipt: %v", err)
			}

			if err := store.MarkModelRunEvaluationTrendAlertNotificationReceiptRetryAbandoned(ctx, "retry-abandoned", "max attempts reached", now); err != nil {
				t.Fatalf("MarkModelRunEvaluationTrendAlertNotificationReceiptRetryAbandoned: %v", err)
			}
			page, err := store.ListModelRunEvaluationTrendAlertNotificationReceipts(ctx, ModelRunEvaluationTrendAlertNotificationReceiptQuery{
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
				page.Receipts[0].ErrorMessage != "max attempts reached" ||
				!page.Receipts[0].UpdatedAt.Equal(now) {
				t.Fatalf("abandoned receipt = %+v", page)
			}
		})
	}
}

func TestModelRunEvaluationTrendAlertNotificationReceiptStoreMarksRetryConsumed(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 6, 22, 19, 45, 0, 0, time.UTC)
	for name, store := range map[string]ModelRunEvaluationTrendAlertNotificationReceiptStore{
		"memory":   NewMemoryModelRunEvaluationTrendAlertNotificationReceiptStore(),
		"platform": NewPlatformModelRunEvaluationTrendAlertNotificationReceiptStore(newMigratedAlertAuditCore(t, "platform-ontology-alert-notification-consume-retry")),
	} {
		t.Run(name, func(t *testing.T) {
			if err := store.SaveModelRunEvaluationTrendAlertNotificationReceipt(ctx, ModelRunEvaluationTrendAlertNotificationDeliveryReceipt{
				ID:           "retry-consumed",
				TenantID:     "tenant-1",
				RuleID:       "accuracy-release-gate.trend_failure",
				Status:       ModelRunEvaluationTrendAlertNotificationDeliveryFailed,
				AlertStatus:  ModelRunEvaluationTrendAlertFiring,
				Reason:       ModelRunEvaluationTrendAlertNotificationReasonFiring,
				Attempt:      1,
				Retryable:    true,
				NextRetryAt:  now.Add(-time.Minute),
				ErrorMessage: "temporary webhook failure",
				SentAt:       now.Add(-10 * time.Minute),
			}); err != nil {
				t.Fatalf("SaveModelRunEvaluationTrendAlertNotificationReceipt: %v", err)
			}

			if err := store.MarkModelRunEvaluationTrendAlertNotificationReceiptRetryConsumed(ctx, "retry-consumed", now); err != nil {
				t.Fatalf("MarkModelRunEvaluationTrendAlertNotificationReceiptRetryConsumed: %v", err)
			}
			page, err := store.ListModelRunEvaluationTrendAlertNotificationReceipts(ctx, ModelRunEvaluationTrendAlertNotificationReceiptQuery{
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
				page.Receipts[0].ErrorMessage != "temporary webhook failure" ||
				!page.Receipts[0].UpdatedAt.Equal(now) {
				t.Fatalf("consumed receipt = %+v", page)
			}
		})
	}
}
