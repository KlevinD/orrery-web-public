package main

import (
	"context"
	"errors"
	"time"

	schedulerapp "orrery_v2/internal/app/scheduler"
)

const (
	defaultResponsibilityReconcileCron = "* * * * *"
)

func ensureDefaultResponsibilityReconcileScheduledTask(ctx context.Context, repo *schedulerapp.Repository, now time.Time) error {
	if repo == nil {
		return nil
	}
	_, err := repo.GetTask(ctx, defaultResponsibilityReconcileScheduledTaskID)
	if err == nil {
		return nil
	}
	if !errors.Is(err, schedulerapp.ErrNotFound) {
		return err
	}
	return repo.SaveTask(ctx, defaultResponsibilityReconcileScheduledTask(now))
}

func defaultResponsibilityReconcileScheduledTask(now time.Time) schedulerapp.ScheduledTask {
	if now.IsZero() {
		now = time.Now()
	}
	now = now.UTC()
	return schedulerapp.ScheduledTask{
		TenantID:        "system",
		ScheduledTaskID: defaultResponsibilityReconcileScheduledTaskID,
		Title:           "Agent responsibility reconciliation",
		Description:     "Recovers and advances durable Agent responsibilities, attempts, blockers, and supervision alerts.",
		Schedule: schedulerapp.Schedule{
			Type: schedulerapp.ScheduleTypeCron, Cron: defaultResponsibilityReconcileCron, Timezone: "Asia/Shanghai",
		},
		Target: schedulerapp.Target{Mode: schedulerapp.TargetModeResponsibilityReconcile},
		Policy: schedulerapp.Policy{
			Misfire: schedulerapp.MisfireRunOnce, Concurrency: schedulerapp.ConcurrencySkipIfRunning,
			MaxRetries: 1, RetryDelay: "30s", FailureMode: "record_failure",
		},
		Status: schedulerapp.TaskStatusEnabled, Enabled: true,
		NextRunAt:        now.Truncate(time.Minute).Add(time.Minute),
		CreatedBy:        "system:responsibility-coordinator-bootstrap",
		CreatedByKind:    schedulerapp.ViewerKindSystem,
		CreatedByName:    "Responsibility coordinator bootstrap",
		CreatorProfileID: defaultLeadAgentID,
		CreatedAt:        now,
		UpdatedAt:        now,
	}
}
