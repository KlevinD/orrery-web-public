package main

import (
	"context"

	agentcore "orrery_v2/internal/agent/core"
	mempageindex "orrery_v2/internal/memory/pageindex"
	memstore "orrery_v2/internal/memory/store"
	"orrery_v2/internal/platform"
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/logging"
)

func newProductionProfileStores(ctx context.Context, rt *platform.Runtime, core *db.Store, disableMigrations bool) (*agentcore.PlatformProfileRepository, *memstore.PlatformStore, error) {
	profiles := agentcore.NewPlatformProfileRepository(rt.UnitOfWork)
	memory := memstore.NewPlatformStore(core)
	if disableMigrations {
		return profiles, memory, nil
	}
	report, err := migrateCanonicalAgentMemoryProfiles(ctx, profiles, memory, mempageindex.NewPlatformState(core))
	if err != nil {
		return nil, nil, err
	}
	if report.ProfilesMoved > 0 {
		rt.Logger.Info(ctx, "canonicalized agent memory profile identities",
			logging.Int("profiles", report.ProfilesMoved),
			logging.Int("records", report.RecordsMoved),
			logging.Int("pageindex_moved", report.PageIndexBindingsMoved),
			logging.Int("pageindex_recreated", report.PageIndexBindingsRecreated))
	}
	retired, err := disableRetiredEmptyMemoryProfiles(ctx, profiles, memory)
	if err != nil {
		return nil, nil, err
	}
	if retired.Disabled > 0 {
		rt.Logger.Info(ctx, "disabled retired empty memory profiles",
			logging.Int("scanned", retired.Scanned),
			logging.Int("disabled", retired.Disabled))
	}
	return profiles, memory, nil
}
