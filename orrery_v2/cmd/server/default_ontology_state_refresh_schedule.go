package main

import (
	"context"
	"errors"
	"time"

	schedulerapp "orrery_v2/internal/app/scheduler"
)

const (
	defaultOntologyStateRefreshScheduledTaskID = "ontology-state-daily-refresh"
	defaultOntologyStateRefreshCron            = "10 8 * * *"
)

func ensureDefaultOntologyStateRefreshScheduledTask(ctx context.Context, repo *schedulerapp.Repository, now time.Time) error {
	if repo == nil {
		return nil
	}
	if _, err := repo.GetTask(ctx, defaultOntologyStateRefreshScheduledTaskID); err == nil {
		return nil
	} else if !errors.Is(err, schedulerapp.ErrNotFound) {
		return err
	}
	task, err := defaultOntologyStateRefreshScheduledTask(now)
	if err != nil {
		return err
	}
	return repo.SaveTask(ctx, task)
}

func defaultOntologyStateRefreshScheduledTask(now time.Time) (schedulerapp.ScheduledTask, error) {
	if now.IsZero() {
		now = time.Now()
	}
	now = now.UTC()
	schedule := schedulerapp.Schedule{
		Type: schedulerapp.ScheduleTypeCron, Cron: defaultOntologyStateRefreshCron, Timezone: "Asia/Shanghai",
	}
	nextRunAt, err := schedulerapp.NextOccurrence(schedule, now)
	if err != nil {
		return schedulerapp.ScheduledTask{}, err
	}
	return schedulerapp.ScheduledTask{
		TenantID:        "system",
		ScheduledTaskID: defaultOntologyStateRefreshScheduledTaskID,
		Title:           "Daily ontology state refresh",
		Description:     "Reconciles current source state into the ontology without replaying historical runs.",
		Schedule:        schedule,
		Target:          schedulerapp.Target{Mode: schedulerapp.TargetModeOntologyStateRefresh},
		Policy: schedulerapp.Policy{
			Misfire: schedulerapp.MisfireRunOnce, Concurrency: schedulerapp.ConcurrencyAllowParallel,
			MaxRetries: 1, RetryDelay: "5m", FailureMode: "record_failure",
		},
		Status: schedulerapp.TaskStatusEnabled, Enabled: true,
		NextRunAt:        nextRunAt,
		CreatedBy:        "system:ontology-state-refresh-bootstrap",
		CreatedByKind:    schedulerapp.ViewerKindSystem,
		CreatedByName:    "Ontology state refresh bootstrap",
		CreatorProfileID: defaultLeadAgentID,
		CreatedAt:        now,
		UpdatedAt:        now,
	}, nil
}
