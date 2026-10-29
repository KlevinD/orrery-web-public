package ontology

import (
	"context"
	"testing"
	"time"
)

func TestPlanModelRunEvaluationTrendAlertNotificationSendsFirstFiringWithNoHistory(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryModelRunEvaluationTrendAlertNotificationHistoryStore()
	observedAt := time.Date(2026, 6, 22, 18, 0, 0, 0, time.UTC)
	event := modelRunEvaluationTrendAlertNotificationTestEvent(ModelRunEvaluationTrendAlertFiring, observedAt)

	plan, err := PlanModelRunEvaluationTrendAlertNotification(ctx, store, event, ModelRunEvaluationTrendAlertNotificationPolicy{
		RepeatWindow: 10 * time.Minute,
	}, observedAt)
	if err != nil {
		t.Fatalf("PlanModelRunEvaluationTrendAlertNotification: %v", err)
	}
	if plan.HistoryFound {
		t.Fatalf("HistoryFound = true")
	}
	if !plan.Decision.Notify || plan.Decision.Reason != ModelRunEvaluationTrendAlertNotificationReasonFiring {
		t.Fatalf("decision = %+v", plan.Decision)
	}
}

func TestPlanModelRunEvaluationTrendAlertNotificationSuppressesRepeatFromStore(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryModelRunEvaluationTrendAlertNotificationHistoryStore()
	previousAt := time.Date(2026, 6, 22, 18, 5, 0, 0, time.UTC)
	event := modelRunEvaluationTrendAlertNotificationTestEvent(ModelRunEvaluationTrendAlertFiring, previousAt)
	event.Labels["snapshot_recorded_to"] = previousAt.Format(time.RFC3339Nano)
	if err := store.SaveModelRunEvaluationTrendAlertNotificationHistory(ctx, ModelRunEvaluationTrendAlertNotificationHistoryRecordFromEvent(event, previousAt)); err != nil {
		t.Fatalf("SaveModelRunEvaluationTrendAlertNotificationHistory: %v", err)
	}

	now := previousAt.Add(5 * time.Minute)
	nextEvent := modelRunEvaluationTrendAlertNotificationTestEvent(ModelRunEvaluationTrendAlertFiring, now)
	nextEvent.Labels["snapshot_recorded_to"] = now.Format(time.RFC3339Nano)
	plan, err := PlanModelRunEvaluationTrendAlertNotification(ctx, store, nextEvent, ModelRunEvaluationTrendAlertNotificationPolicy{
		RepeatWindow: 10 * time.Minute,
	}, now)
	if err != nil {
		t.Fatalf("PlanModelRunEvaluationTrendAlertNotification: %v", err)
	}
	if !plan.HistoryFound {
		t.Fatalf("HistoryFound = false")
	}
	if plan.Decision.Notify ||
		plan.Decision.Reason != ModelRunEvaluationTrendAlertNotificationReasonRepeatWindow ||
		!plan.Decision.NextEligibleAt.Equal(previousAt.Add(10*time.Minute)) {
		t.Fatalf("decision = %+v", plan.Decision)
	}
}

func TestPlanModelRunEvaluationTrendAlertNotificationDoesNotRecordUnsentDecision(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryModelRunEvaluationTrendAlertNotificationHistoryStore()
	observedAt := time.Date(2026, 6, 22, 18, 15, 0, 0, time.UTC)
	event := modelRunEvaluationTrendAlertNotificationTestEvent(ModelRunEvaluationTrendAlertFiring, observedAt)

	plan, err := PlanModelRunEvaluationTrendAlertNotification(ctx, store, event, ModelRunEvaluationTrendAlertNotificationPolicy{}, observedAt)
	if err != nil {
		t.Fatalf("PlanModelRunEvaluationTrendAlertNotification: %v", err)
	}
	if !plan.Decision.Notify {
		t.Fatalf("decision = %+v", plan.Decision)
	}

	_, found, err := store.GetModelRunEvaluationTrendAlertNotificationHistory(ctx, ModelRunEvaluationTrendAlertNotificationHistoryQuery{
		TenantID: event.TenantID,
		RuleID:   event.RuleID,
		Labels:   event.Labels,
	})
	if err != nil {
		t.Fatalf("GetModelRunEvaluationTrendAlertNotificationHistory: %v", err)
	}
	if found {
		t.Fatalf("planner recorded notification history before a sender acknowledged delivery")
	}
}
