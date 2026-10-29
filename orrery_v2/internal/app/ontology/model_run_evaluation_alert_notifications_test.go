package ontology

import (
	"testing"
	"time"
)

func TestDecideModelRunEvaluationTrendAlertNotificationSendsFirstFiring(t *testing.T) {
	observedAt := time.Date(2026, 6, 22, 15, 0, 0, 0, time.UTC)

	decision := DecideModelRunEvaluationTrendAlertNotification(modelRunEvaluationTrendAlertNotificationTestEvent(ModelRunEvaluationTrendAlertFiring, observedAt), ModelRunEvaluationTrendAlertNotificationHistory{}, ModelRunEvaluationTrendAlertNotificationPolicy{
		RepeatWindow: 10 * time.Minute,
	}, observedAt)

	if !decision.Notify ||
		decision.Reason != ModelRunEvaluationTrendAlertNotificationReasonFiring ||
		decision.Status != ModelRunEvaluationTrendAlertFiring {
		t.Fatalf("decision = %+v", decision)
	}
}

func TestDecideModelRunEvaluationTrendAlertNotificationSuppressesRepeatFiringWithinWindow(t *testing.T) {
	observedAt := time.Date(2026, 6, 22, 15, 5, 0, 0, time.UTC)

	decision := DecideModelRunEvaluationTrendAlertNotification(modelRunEvaluationTrendAlertNotificationTestEvent(ModelRunEvaluationTrendAlertFiring, observedAt), ModelRunEvaluationTrendAlertNotificationHistory{
		LastNotificationStatus: ModelRunEvaluationTrendAlertFiring,
		LastNotificationAt:     observedAt.Add(-5 * time.Minute),
	}, ModelRunEvaluationTrendAlertNotificationPolicy{
		RepeatWindow: 10 * time.Minute,
	}, observedAt)

	if decision.Notify ||
		decision.Reason != ModelRunEvaluationTrendAlertNotificationReasonRepeatWindow ||
		decision.NextEligibleAt != observedAt.Add(5*time.Minute).UTC() {
		t.Fatalf("decision = %+v", decision)
	}
}

func TestDecideModelRunEvaluationTrendAlertNotificationSuppressesSilencedAlerts(t *testing.T) {
	observedAt := time.Date(2026, 6, 22, 15, 10, 0, 0, time.UTC)
	event := modelRunEvaluationTrendAlertNotificationTestEvent(ModelRunEvaluationTrendAlertFiring, observedAt)

	decision := DecideModelRunEvaluationTrendAlertNotification(event, ModelRunEvaluationTrendAlertNotificationHistory{}, ModelRunEvaluationTrendAlertNotificationPolicy{
		SilencedUntil: observedAt.Add(30 * time.Minute),
	}, observedAt)
	if decision.Notify ||
		decision.Reason != ModelRunEvaluationTrendAlertNotificationReasonSilencedWindow ||
		!decision.NextEligibleAt.Equal(observedAt.Add(30*time.Minute).UTC()) {
		t.Fatalf("silenced window decision = %+v", decision)
	}

	decision = DecideModelRunEvaluationTrendAlertNotification(event, ModelRunEvaluationTrendAlertNotificationHistory{}, ModelRunEvaluationTrendAlertNotificationPolicy{
		SilenceLabels: map[string]string{"model": "gpt-5", "metric_threshold_rule_id": "accuracy-release-gate"},
	}, observedAt)
	if decision.Notify ||
		decision.Reason != ModelRunEvaluationTrendAlertNotificationReasonSilencedLabels {
		t.Fatalf("silenced label decision = %+v", decision)
	}
}

func TestDecideModelRunEvaluationTrendAlertNotificationSendsResolvedAfterFiringAndSuppressesDuplicateRecovery(t *testing.T) {
	observedAt := time.Date(2026, 6, 22, 15, 30, 0, 0, time.UTC)
	resolved := modelRunEvaluationTrendAlertNotificationTestEvent(ModelRunEvaluationTrendAlertResolved, observedAt)

	decision := DecideModelRunEvaluationTrendAlertNotification(resolved, ModelRunEvaluationTrendAlertNotificationHistory{
		LastNotificationStatus: ModelRunEvaluationTrendAlertFiring,
		LastNotificationAt:     observedAt.Add(-20 * time.Minute),
	}, ModelRunEvaluationTrendAlertNotificationPolicy{
		RecoveryWindow: 30 * time.Minute,
	}, observedAt)
	if !decision.Notify ||
		decision.Reason != ModelRunEvaluationTrendAlertNotificationReasonResolved ||
		decision.Status != ModelRunEvaluationTrendAlertResolved {
		t.Fatalf("resolved decision = %+v", decision)
	}

	decision = DecideModelRunEvaluationTrendAlertNotification(resolved, ModelRunEvaluationTrendAlertNotificationHistory{
		LastNotificationStatus: ModelRunEvaluationTrendAlertResolved,
		LastNotificationAt:     observedAt.Add(-5 * time.Minute),
	}, ModelRunEvaluationTrendAlertNotificationPolicy{
		RecoveryWindow: 30 * time.Minute,
	}, observedAt)
	if decision.Notify ||
		decision.Reason != ModelRunEvaluationTrendAlertNotificationReasonRecoveryWindow ||
		!decision.NextEligibleAt.Equal(observedAt.Add(25*time.Minute).UTC()) {
		t.Fatalf("duplicate resolved decision = %+v", decision)
	}
}

func modelRunEvaluationTrendAlertNotificationTestEvent(status ModelRunEvaluationTrendAlertStatus, observedAt time.Time) ModelRunEvaluationTrendAlertAuditEvent {
	return ModelRunEvaluationTrendAlertAuditEvent{
		ID:        "alert-audit-1",
		EventType: "ontology.model_run_evaluation_alert." + string(status),
		TenantID:  "tenant-1",
		RuleID:    "accuracy-release-gate.trend_failure",
		Severity:  ModelRunEvaluationThresholdSeverityCritical,
		Status:    status,
		Labels: map[string]string{
			"tenant_id":                "tenant-1",
			"engine":                   "ontology_designer",
			"model":                    "gpt-5",
			"metric_threshold_rule_id": "accuracy-release-gate",
		},
		ObservedAt: observedAt,
	}
}
