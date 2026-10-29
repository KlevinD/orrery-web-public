package ontology

import (
	"context"
	"testing"
	"time"
)

func TestMemoryModelRunEvaluationTrendAlertNotificationHistoryStoreSaveAndGet(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryModelRunEvaluationTrendAlertNotificationHistoryStore()
	observedAt := time.Date(2026, 6, 22, 16, 0, 0, 0, time.UTC)
	event := modelRunEvaluationTrendAlertNotificationTestEvent(ModelRunEvaluationTrendAlertFiring, observedAt)

	if err := store.SaveModelRunEvaluationTrendAlertNotificationHistory(ctx, ModelRunEvaluationTrendAlertNotificationHistoryRecordFromEvent(event, observedAt)); err != nil {
		t.Fatalf("SaveModelRunEvaluationTrendAlertNotificationHistory: %v", err)
	}

	record, found, err := store.GetModelRunEvaluationTrendAlertNotificationHistory(ctx, ModelRunEvaluationTrendAlertNotificationHistoryQuery{
		TenantID: "tenant-1",
		RuleID:   "accuracy-release-gate.trend_failure",
		Labels: map[string]string{
			"engine":                   "ontology_designer",
			"model":                    "gpt-5",
			"metric_threshold_rule_id": "accuracy-release-gate",
			"tenant_id":                "tenant-1",
		},
	})
	if err != nil {
		t.Fatalf("GetModelRunEvaluationTrendAlertNotificationHistory: %v", err)
	}
	if !found {
		t.Fatalf("expected notification history")
	}
	if record.LastNotificationStatus != ModelRunEvaluationTrendAlertFiring ||
		!record.LastNotificationAt.Equal(observedAt) ||
		record.Labels["model"] != "gpt-5" {
		t.Fatalf("record = %+v", record)
	}

	updatedAt := observedAt.Add(15 * time.Minute)
	resolved := event
	resolved.Status = ModelRunEvaluationTrendAlertResolved
	resolved.ObservedAt = updatedAt
	if err := store.SaveModelRunEvaluationTrendAlertNotificationHistory(ctx, ModelRunEvaluationTrendAlertNotificationHistoryRecordFromEvent(resolved, updatedAt)); err != nil {
		t.Fatalf("SaveModelRunEvaluationTrendAlertNotificationHistory resolved: %v", err)
	}
	record, found, err = store.GetModelRunEvaluationTrendAlertNotificationHistory(ctx, ModelRunEvaluationTrendAlertNotificationHistoryQuery{
		TenantID: "tenant-1",
		RuleID:   "accuracy-release-gate.trend_failure",
		Labels:   event.Labels,
	})
	if err != nil {
		t.Fatalf("GetModelRunEvaluationTrendAlertNotificationHistory resolved: %v", err)
	}
	if !found || record.LastNotificationStatus != ModelRunEvaluationTrendAlertResolved || !record.LastNotificationAt.Equal(updatedAt) {
		t.Fatalf("updated record = %+v found=%v", record, found)
	}
}

func TestMemoryModelRunEvaluationTrendAlertNotificationHistoryStoreKeysByLabels(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryModelRunEvaluationTrendAlertNotificationHistoryStore()
	observedAt := time.Date(2026, 6, 22, 16, 30, 0, 0, time.UTC)
	event := modelRunEvaluationTrendAlertNotificationTestEvent(ModelRunEvaluationTrendAlertFiring, observedAt)
	if err := store.SaveModelRunEvaluationTrendAlertNotificationHistory(ctx, ModelRunEvaluationTrendAlertNotificationHistoryRecordFromEvent(event, observedAt)); err != nil {
		t.Fatalf("SaveModelRunEvaluationTrendAlertNotificationHistory: %v", err)
	}

	otherLabels := cloneStringMetadata(event.Labels)
	otherLabels["model"] = "gpt-4.1"
	_, found, err := store.GetModelRunEvaluationTrendAlertNotificationHistory(ctx, ModelRunEvaluationTrendAlertNotificationHistoryQuery{
		TenantID: "tenant-1",
		RuleID:   "accuracy-release-gate.trend_failure",
		Labels:   otherLabels,
	})
	if err != nil {
		t.Fatalf("GetModelRunEvaluationTrendAlertNotificationHistory: %v", err)
	}
	if found {
		t.Fatalf("history matched different labels")
	}
}

func TestMemoryModelRunEvaluationTrendAlertNotificationHistoryStoreIgnoresSnapshotLabel(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryModelRunEvaluationTrendAlertNotificationHistoryStore()
	observedAt := time.Date(2026, 6, 22, 16, 45, 0, 0, time.UTC)
	event := modelRunEvaluationTrendAlertNotificationTestEvent(ModelRunEvaluationTrendAlertFiring, observedAt)
	event.Labels["snapshot_recorded_to"] = observedAt.Format(time.RFC3339Nano)
	if err := store.SaveModelRunEvaluationTrendAlertNotificationHistory(ctx, ModelRunEvaluationTrendAlertNotificationHistoryRecordFromEvent(event, observedAt)); err != nil {
		t.Fatalf("SaveModelRunEvaluationTrendAlertNotificationHistory: %v", err)
	}

	nextLabels := cloneStringMetadata(event.Labels)
	nextLabels["snapshot_recorded_to"] = observedAt.Add(5 * time.Minute).Format(time.RFC3339Nano)
	_, found, err := store.GetModelRunEvaluationTrendAlertNotificationHistory(ctx, ModelRunEvaluationTrendAlertNotificationHistoryQuery{
		TenantID: "tenant-1",
		RuleID:   "accuracy-release-gate.trend_failure",
		Labels:   nextLabels,
	})
	if err != nil {
		t.Fatalf("GetModelRunEvaluationTrendAlertNotificationHistory: %v", err)
	}
	if !found {
		t.Fatalf("history should ignore snapshot_recorded_to when matching alert notification identity")
	}
}

func TestPlatformModelRunEvaluationTrendAlertNotificationHistoryStoreSaveAndGet(t *testing.T) {
	ctx := context.Background()
	core := newMigratedAlertAuditCore(t, "platform-ontology-alert-notification-history")
	store := NewPlatformModelRunEvaluationTrendAlertNotificationHistoryStore(core)
	observedAt := time.Date(2026, 6, 22, 17, 0, 0, 0, time.UTC)
	event := modelRunEvaluationTrendAlertNotificationTestEvent(ModelRunEvaluationTrendAlertFiring, observedAt)

	if err := store.SaveModelRunEvaluationTrendAlertNotificationHistory(ctx, ModelRunEvaluationTrendAlertNotificationHistoryRecordFromEvent(event, observedAt)); err != nil {
		t.Fatalf("SaveModelRunEvaluationTrendAlertNotificationHistory: %v", err)
	}

	record, found, err := store.GetModelRunEvaluationTrendAlertNotificationHistory(ctx, ModelRunEvaluationTrendAlertNotificationHistoryQuery{
		TenantID: "tenant-1",
		RuleID:   "accuracy-release-gate.trend_failure",
		Labels:   event.Labels,
	})
	if err != nil {
		t.Fatalf("GetModelRunEvaluationTrendAlertNotificationHistory: %v", err)
	}
	if !found {
		t.Fatalf("expected notification history")
	}
	if record.TenantID != "tenant-1" ||
		record.RuleID != "accuracy-release-gate.trend_failure" ||
		record.LastNotificationStatus != ModelRunEvaluationTrendAlertFiring ||
		!record.LastNotificationAt.Equal(observedAt) ||
		record.Labels["metric_threshold_rule_id"] != "accuracy-release-gate" {
		t.Fatalf("record = %+v", record)
	}
}
