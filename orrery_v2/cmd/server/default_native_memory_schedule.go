package main

import (
	"context"
	"errors"
	"strings"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	schedulerapp "orrery_v2/internal/app/scheduler"
	"orrery_v2/internal/migration/nativememory"
)

func repairLegacyNativeMemoryScheduledTaskCreators(ctx context.Context, repo *schedulerapp.Repository, now time.Time) error {
	if repo == nil {
		return nil
	}
	for _, expected := range nativememory.Catalog() {
		task, err := repo.GetTask(ctx, expected.ScheduledTaskID)
		if errors.Is(err, schedulerapp.ErrNotFound) {
			continue
		}
		if err != nil {
			return err
		}
		if !strings.EqualFold(strings.TrimSpace(task.CreatorProfileID), agentcore.LegacySuperadminAgentProfileID) {
			continue
		}
		task.CreatorProfileID = agentcore.SystemSuperadminPrincipalID
		task.UpdatedAt = now.UTC()
		if err := repo.SaveTask(ctx, task); err != nil {
			return err
		}
	}
	return nil
}
