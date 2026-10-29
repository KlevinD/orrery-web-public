package main

import (
	"context"
	"errors"
	"time"

	schedulerapp "orrery_v2/internal/app/scheduler"
)

func ensureDefaultProductionScheduledTasks(ctx context.Context, repo *schedulerapp.Repository, now time.Time) error {
	if err := removeRetiredDefaultLegalScheduledTasks(ctx, repo); err != nil {
		return err
	}
	if err := repairLegacyNativeMemoryScheduledTaskCreators(ctx, repo, now); err != nil {
		return err
	}
	for _, ensure := range []func(context.Context, *schedulerapp.Repository, time.Time) error{
		ensureDefaultTodoAutomationScheduledTask,
		ensureDefaultOntologyStateRefreshScheduledTask,
		ensureDefaultOntologyStewardScheduledTasks,
	} {
		if err := ensure(ctx, repo, now); err != nil {
			return err
		}
	}
	if repo != nil {
		return repo.MigrateIndependentOccurrences(ctx, now)
	}
	return nil
}

func removeRetiredDefaultLegalScheduledTasks(ctx context.Context, repo *schedulerapp.Repository) error {
	if repo == nil {
		return nil
	}
	for _, seed := range defaultLegalScheduleSeeds() {
		if err := repo.DeleteTask(ctx, seed.id); err != nil && !errors.Is(err, schedulerapp.ErrNotFound) {
			return err
		}
	}
	return nil
}
