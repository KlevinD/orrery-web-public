package main

import (
	"time"

	memorydiaryapp "orrery_v2/internal/app/memorydiary"
	"orrery_v2/internal/platform/db"
)

func newProductionAgentDiaryRuntime(core *db.Store, settings productionSettingsStore, now func() time.Time) (*memorydiaryapp.PlatformFlushStateStore, *memorydiaryapp.Coordinator) {
	location := time.Local
	if configured, err := time.LoadLocation("Asia/Shanghai"); err == nil {
		location = configured
	}
	state := memorydiaryapp.NewPlatformFlushStateStore(core)
	coordinator := memorydiaryapp.NewCoordinator(memorydiaryapp.CoordinatorOptions{
		State: state, Enabled: settings.MemoryEnabled, Location: location, Now: now,
	})
	return state, coordinator
}
