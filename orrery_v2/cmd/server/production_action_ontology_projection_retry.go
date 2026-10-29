package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	"orrery_v2/internal/platform/graphprotocol"
)

const (
	productionActionOntologyProjectionBudget         = 30 * time.Second
	productionActionOntologyProjectionAttemptTimeout = 8 * time.Second
)

var productionActionOntologyProjectionRetryDelays = []time.Duration{250 * time.Millisecond, 750 * time.Millisecond}

type productionActionOntologyEventApply func(context.Context, graphprotocol.DomainEvent) error

func projectActionOntologyEvents(parent context.Context, events []graphprotocol.DomainEvent, retryDelays []time.Duration, apply productionActionOntologyEventApply) error {
	if apply == nil {
		return fmt.Errorf("action ontology projection apply function is required")
	}
	projectionCtx, cancel := context.WithTimeout(context.WithoutCancel(parent), productionActionOntologyProjectionBudget)
	defer cancel()
	for _, event := range events {
		if err := projectActionOntologyEvent(projectionCtx, event, retryDelays, apply); err != nil {
			return err
		}
	}
	return nil
}

func projectActionOntologyEvent(ctx context.Context, event graphprotocol.DomainEvent, retryDelays []time.Duration, apply productionActionOntologyEventApply) error {
	if len(retryDelays) > 2 {
		retryDelays = retryDelays[:2]
	}
	var lastErr error
	for attempt := 0; attempt <= len(retryDelays); attempt++ {
		attemptCtx, cancel := context.WithTimeout(ctx, productionActionOntologyProjectionAttemptTimeout)
		lastErr = apply(attemptCtx, event)
		cancel()
		if lastErr == nil {
			return nil
		}
		if !errors.Is(lastErr, context.DeadlineExceeded) {
			return lastErr
		}
		if attempt == len(retryDelays) {
			break
		}
		timer := time.NewTimer(retryDelays[attempt])
		select {
		case <-ctx.Done():
			timer.Stop()
			return fmt.Errorf("project action ontology event %s: %w", event.Type, ctx.Err())
		case <-timer.C:
		}
	}
	return fmt.Errorf("project action ontology event %s after %d attempts: %w", event.Type, len(retryDelays)+1, lastErr)
}
