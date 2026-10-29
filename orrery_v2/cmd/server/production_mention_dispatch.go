package main

import (
	"context"

	"orrery_v2/internal/app/missioncontrol"
	"orrery_v2/internal/platform"
	"orrery_v2/internal/platform/execution"
)

func registerProductionMentionDispatch(rt *platform.Runtime, runner *missioncontrol.MentionAgentRunner) error {
	return rt.RegisterExecutionDeliveryHandler(missioncontrol.MentionDispatchTopic, func(ctx context.Context, delivery platform.ExecutionDelivery) error {
		return runner.HandleExecution(ctx, execution.Job{ID: delivery.ID, Topic: delivery.Topic, Payload: delivery.Payload, Priority: delivery.Priority, Attempts: delivery.Attempts, MaxAttempts: delivery.MaxAttempts})
	})
}
