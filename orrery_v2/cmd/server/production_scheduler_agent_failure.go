package main

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"

	"orrery_v2/internal/app/responsibility"
	schedulerapp "orrery_v2/internal/app/scheduler"
)

func (t productionSchedulerRunTracker) FailAgentRun(ctx context.Context, expected schedulerapp.ScheduledRun, result schedulerapp.RunResult, traceRef string, completedAt time.Time, cause error) error {
	if t.repo == nil {
		return schedulerapp.ErrServiceDependency
	}
	if err := validateScheduledAgentRunCompletionIdentity(expected, expected); err != nil {
		return err
	}
	failureReason := "Agent reported that the scheduled acceptance criteria are blocked."
	if cause != nil && strings.TrimSpace(cause.Error()) != "" {
		failureReason = strings.TrimSpace(cause.Error())
	}
	_, err := t.repo.MutateRun(ctx, strings.TrimSpace(expected.ScheduledRunID), func(run *schedulerapp.ScheduledRun) error {
		if err := validateScheduledAgentRunCompletionIdentity(*run, expected); err != nil {
			return err
		}
		if err := bindScheduledRunRuntimeIdentity(run, expected.RuntimeRunID); err != nil {
			return err
		}
		switch run.Status {
		case schedulerapp.RunStatusFailed:
			if !reflect.DeepEqual(run.Result, result) || strings.TrimSpace(run.FailureReason) != failureReason {
				return fmt.Errorf("%w: scheduled agent run already failed with a different result", responsibility.ErrConflict)
			}
			if err := mergeScheduledRunCompletionTrace(run, traceRef); err != nil {
				return err
			}
			initializeScheduledRunDeliveryStatus(run)
			return nil
		case schedulerapp.RunStatusQueued, schedulerapp.RunStatusRunning:
		default:
			return fmt.Errorf("%w: scheduled agent run is not active", responsibility.ErrConflict)
		}
		if scheduledRunHasResult(run.Result) && !reflect.DeepEqual(run.Result, result) {
			return fmt.Errorf("%w: scheduled agent run acquired a different result", responsibility.ErrConflict)
		}
		if err := mergeScheduledRunCompletionTrace(run, traceRef); err != nil {
			return err
		}
		if completedAt.IsZero() {
			completedAt = t.now()
		}
		run.Status = schedulerapp.RunStatusFailed
		run.FailureReason = failureReason
		run.CompletedAt = completedAt.UTC()
		run.Result = result
		initializeScheduledRunDeliveryStatus(run)
		return nil
	})
	if errors.Is(err, schedulerapp.ErrNotFound) {
		return fmt.Errorf("%w: scheduled agent run identity no longer exists", responsibility.ErrConflict)
	}
	return err
}
