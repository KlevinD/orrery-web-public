package main

import (
	"context"
	"time"

	schedulerapp "orrery_v2/internal/app/scheduler"
	"orrery_v2/internal/platform/logging"
)

func runProductionSchedulerOccurrenceReconciliation(ctx context.Context, service *schedulerapp.Service, now time.Time, logger logging.Logger) error {
	report, err := service.ReconcileDisabledOccurrences(ctx, now)
	if err != nil {
		return err
	}
	if logger == nil {
		return nil
	}
	for _, issue := range report.Issues {
		logger.Warn(ctx, "scheduler disabled occurrence not reconciled",
			logging.String("scheduled_task_id", issue.TaskID),
			logging.String("error", issue.Message),
		)
	}
	logger.Info(ctx, "scheduler disabled occurrence reconciliation completed",
		logging.Int("repaired", report.Repaired),
		logging.Int("issues", len(report.Issues)),
	)
	return nil
}
