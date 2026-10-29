package main

import (
	"context"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	schedulerapp "orrery_v2/internal/app/scheduler"
	"orrery_v2/internal/migration/nativememory"
	"orrery_v2/internal/platform/db"
)

func TestRepairLegacyNativeMemoryScheduledTaskCreatorsPreservesScheduleState(t *testing.T) {
	ctx := context.Background()
	registry, err := db.OpenRegistryFromURLs(ctx, map[string]string{db.RoleCore: "memory://repair-native-memory-schedules"})
	if err != nil {
		t.Fatal(err)
	}
	defer registry.Close()
	repo := schedulerapp.NewRepository(db.NewRouter(registry))
	createdAt := time.Date(2026, 7, 26, 7, 28, 31, 0, time.UTC)
	for _, task := range nativememory.Catalog() {
		task.CreatorProfileID = agentcore.LegacySuperadminAgentProfileID
		task.Enabled = true
		task.Status = schedulerapp.TaskStatusEnabled
		task.CreatedAt = createdAt
		task.UpdatedAt = createdAt
		if err := repo.SaveTask(ctx, task); err != nil {
			t.Fatal(err)
		}
	}

	now := createdAt.Add(24 * time.Hour)
	if err := repairLegacyNativeMemoryScheduledTaskCreators(ctx, repo, now); err != nil {
		t.Fatal(err)
	}
	for _, expected := range nativememory.Catalog() {
		task, err := repo.GetTask(ctx, expected.ScheduledTaskID)
		if err != nil {
			t.Fatal(err)
		}
		if task.CreatorProfileID != agentcore.SystemSuperadminPrincipalID || !task.Enabled || task.Status != schedulerapp.TaskStatusEnabled {
			t.Fatalf("task %s = %+v", task.ScheduledTaskID, task)
		}
		if !task.CreatedAt.Equal(createdAt) || !task.UpdatedAt.Equal(now) {
			t.Fatalf("task %s timestamps = created:%s updated:%s", task.ScheduledTaskID, task.CreatedAt, task.UpdatedAt)
		}
	}
}
