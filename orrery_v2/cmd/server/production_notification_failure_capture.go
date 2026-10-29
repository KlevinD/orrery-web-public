package main

import (
	"context"
	"fmt"
	"time"

	"orrery_v2/internal/app/reliability"
	gwoutbound "orrery_v2/internal/gateway/outbound"
)

type productionNotificationFailureCapture interface {
	Enqueue(context.Context, reliability.CapturedFailure) error
}

func productionGatewayNotificationFailureObserver(inbox productionNotificationFailureCapture) func(context.Context, gwoutbound.DeliveryFailure) error {
	return func(ctx context.Context, failure gwoutbound.DeliveryFailure) error {
		delivery := failure.Delivery
		return captureProductionNotificationFailure(ctx, inbox, productionNotificationFailure{
			ID:       fmt.Sprintf("gateway-notification:%s:%d:%s", delivery.ID, failure.Attempt, failure.Code),
			TenantID: delivery.TenantID, Channel: delivery.ChannelKey, AccountID: delivery.AccountID, HumanProfileID: delivery.HumanProfileID,
			Source: "gateway_notification", Code: failure.Code, Message: fmt.Sprintf("%s: %v", failure.Code, failure.Cause),
			Category: reliability.FailureCategoryUnknown, CorrelationID: delivery.ID, OccurredAt: time.Now().UTC(),
		})
	}
}

type productionNotificationFailure struct {
	ID, TenantID, Channel, AccountID, HumanProfileID, Source, Code, Message, CorrelationID, TaskID string
	Category                                                                                       reliability.FailureCategory
	OccurredAt                                                                                     time.Time
}

// Use the existing durable inbox. Routing identity separates unrelated failures;
// original notification IDs deduplicate a retried persistence operation.
func captureProductionNotificationFailure(ctx context.Context, inbox productionNotificationFailureCapture, failure productionNotificationFailure) error {
	if inbox == nil {
		return nil
	}
	return inbox.Enqueue(ctx, reliability.CapturedFailure{
		Observation: reliability.FailureObservation{
			ID: failure.ID, TenantID: failure.TenantID, Source: failure.Source,
			Component: diagnosticComponent("notification/" + failure.Channel + "/" + failure.AccountID + "/" + failure.HumanProfileID),
			ErrorCode: failure.Code, Message: failure.Message, Category: failure.Category,
			Severity: reliability.FailureSeverityError, CorrelationID: failure.CorrelationID,
			OccurredAt: failure.OccurredAt, Actor: reliability.ActorRef{Kind: "service", ID: "notification-delivery"},
		},
		Diagnostics: map[string]string{"cause": failure.Message, "task_id": failure.TaskID, "operation": failure.Source},
	})
}
