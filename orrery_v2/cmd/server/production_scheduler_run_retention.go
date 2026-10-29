package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	schedulerapp "orrery_v2/internal/app/scheduler"
	"orrery_v2/internal/platform"
)

const (
	responsibilityReconcileCompletedRetention  = 24 * time.Hour
	responsibilityReconcileDiagnosticRetention = 7 * 24 * time.Hour
	responsibilityReconcileRunDeleteLimit      = 500
	responsibilityBatchCompletedRetention      = 90 * 24 * time.Hour
	responsibilityBatchDiagnosticRetention     = 180 * 24 * time.Hour
	responsibilityBatchRunDeleteLimit          = 5
)

type scheduledTaskRunRetentionStore interface {
	CleanupTaskRuns(ctx context.Context, taskID string, now time.Time, completedRetention, diagnosticRetention time.Duration, limit int) (int, error)
	ListTasks(ctx context.Context, includeArchived bool) ([]schedulerapp.ScheduledTask, error)
}

type productionResponsibilityRunRetentionSweeper struct {
	runs scheduledTaskRunRetentionStore
	now  func() time.Time
}

func registerProductionResponsibilityRunRetentionSweeper(rt *platform.Runtime, runs *schedulerapp.Repository) error {
	if rt == nil || runs == nil {
		return fmt.Errorf("responsibility run retention dependencies are required")
	}
	sweeper := productionResponsibilityRunRetentionSweeper{runs: runs, now: rt.Clock.Now}
	return rt.RegisterSweeper("scheduler.responsibility_run_retention", newProductionCadencedSweeper(
		platform.SweeperFunc(sweeper.Sweep), rt.Clock.Now, 5*time.Minute,
	))
}

func (s productionResponsibilityRunRetentionSweeper) Sweep(ctx context.Context) error {
	if s.runs == nil || s.now == nil {
		return fmt.Errorf("responsibility run retention dependencies are required")
	}
	now := s.now().UTC()
	var cleanupErrors []error
	if _, err := s.runs.CleanupTaskRuns(
		ctx,
		defaultResponsibilityReconcileScheduledTaskID,
		now,
		responsibilityReconcileCompletedRetention,
		responsibilityReconcileDiagnosticRetention,
		responsibilityReconcileRunDeleteLimit,
	); err != nil {
		cleanupErrors = append(cleanupErrors, fmt.Errorf("cleanup responsibility reconciliation runs: %w", err))
	}
	tasks, err := s.runs.ListTasks(ctx, true)
	if err != nil {
		cleanupErrors = append(cleanupErrors, fmt.Errorf("list responsibility batch schedules: %w", err))
		return errors.Join(cleanupErrors...)
	}
	for _, task := range tasks {
		if !productionResponsibilityBatchTaskIdentity(task) {
			continue
		}
		if _, err := s.runs.CleanupTaskRuns(
			ctx,
			task.ScheduledTaskID,
			now,
			responsibilityBatchCompletedRetention,
			responsibilityBatchDiagnosticRetention,
			responsibilityBatchRunDeleteLimit,
		); err != nil {
			cleanupErrors = append(cleanupErrors, fmt.Errorf("cleanup responsibility batch runs for %s: %w", task.ScheduledTaskID, err))
		}
	}
	return errors.Join(cleanupErrors...)
}
