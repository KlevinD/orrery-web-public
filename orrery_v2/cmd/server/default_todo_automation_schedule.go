package main

import (
	"context"
	"errors"
	"time"

	schedulerapp "orrery_v2/internal/app/scheduler"
)

const (
	defaultTodoAutomationScheduledTaskID = "todo-agent-automation-periodic-scan"
	defaultTodoAutomationCron            = "*/5 * * * *"
)

func ensureDefaultTodoAutomationScheduledTask(ctx context.Context, repo *schedulerapp.Repository, now time.Time) error {
	if repo == nil {
		return nil
	}
	existing, err := repo.GetTask(ctx, defaultTodoAutomationScheduledTaskID)
	if err == nil {
		if existing.CreatorProfileID == "" {
			existing.CreatorProfileID = defaultLeadAgentID
			existing.CreatedByKind = schedulerapp.ViewerKindSystem
			existing.CreatedByName = "Todo automation bootstrap"
			existing.UpdatedAt = now.UTC()
			return repo.SaveTask(ctx, existing)
		}
		return nil
	}
	if !errors.Is(err, schedulerapp.ErrNotFound) {
		return err
	}
	return repo.SaveTask(ctx, defaultTodoAutomationScheduledTask(now))
}

func defaultTodoAutomationScheduledTask(now time.Time) schedulerapp.ScheduledTask {
	if now.IsZero() {
		now = time.Now()
	}
	now = now.UTC()
	return schedulerapp.ScheduledTask{
		TenantID:        "system",
		ScheduledTaskID: defaultTodoAutomationScheduledTaskID,
		Title:           "Todo Agent periodic review",
		Description:     "Periodically checks actionable Todos, prepares group-chat plans, and requests human approval.",
		Schedule: schedulerapp.Schedule{
			Type:     schedulerapp.ScheduleTypeCron,
			Cron:     defaultTodoAutomationCron,
			Timezone: "Asia/Shanghai",
		},
		Target: schedulerapp.Target{
			Mode: schedulerapp.TargetModeTodoAutomationScan,
		},
		Policy: schedulerapp.Policy{
			Misfire:     schedulerapp.MisfireRunOnce,
			Concurrency: schedulerapp.ConcurrencyAllowParallel,
			MaxRetries:  1,
			RetryDelay:  "1m",
			FailureMode: "record_failure",
		},
		Status:           schedulerapp.TaskStatusEnabled,
		Enabled:          true,
		NextRunAt:        nextTodoAutomationScheduleRun(now),
		CreatedBy:        "system:todo-automation-bootstrap",
		CreatedByKind:    schedulerapp.ViewerKindSystem,
		CreatedByName:    "Todo automation bootstrap",
		CreatorProfileID: defaultLeadAgentID,
		CreatedAt:        now,
		UpdatedAt:        now,
	}
}

func nextTodoAutomationScheduleRun(now time.Time) time.Time {
	next := now.UTC().Truncate(time.Minute).Add(time.Minute)
	for next.Minute()%5 != 0 {
		next = next.Add(time.Minute)
	}
	return next
}
