package main

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	schedulerapp "orrery_v2/internal/app/scheduler"
	"orrery_v2/internal/platform/testkit"
)

func TestEnsureDefaultProductionScheduledTasksSeedsCurrentDefaultsAndRemovesRetiredLegalTasks(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	repo := schedulerapp.NewRepository(env.DBRouter)
	ctx := context.Background()
	now := time.Date(2026, 7, 29, 9, 0, 0, 0, time.UTC)
	for _, seed := range defaultLegalScheduleSeeds() {
		if err := repo.SaveTask(ctx, defaultLegalScheduledTask(seed, now)); err != nil {
			t.Fatalf("seed retired legal task %s: %v", seed.id, err)
		}
	}

	if err := ensureDefaultProductionScheduledTasks(ctx, repo, now); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.GetTask(ctx, defaultResponsibilityReconcileScheduledTaskID); !errors.Is(err, schedulerapp.ErrNotFound) {
		t.Fatalf("background responsibility runtime must not seed a cron entry: %v", err)
	}

	taskIDs := []string{defaultTodoAutomationScheduledTaskID, defaultOntologyStateRefreshScheduledTaskID}
	for _, taskID := range taskIDs {
		if _, err := repo.GetTask(ctx, taskID); err != nil {
			t.Fatal(fmt.Errorf("get default task %s: %w", taskID, err))
		}
	}
	for _, seed := range defaultLegalScheduleSeeds() {
		if _, err := repo.GetTask(ctx, seed.id); !errors.Is(err, schedulerapp.ErrNotFound) {
			t.Fatalf("retired legal task %s should be removed: %v", seed.id, err)
		}
	}
	if _, err := repo.GetTask(ctx, "website-faq-weekly-maintenance"); !errors.Is(err, schedulerapp.ErrNotFound) {
		t.Fatalf("website schedule should be configuration-owned: %v", err)
	}
}
