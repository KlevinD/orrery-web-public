package main

import (
	"context"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/memorypreference"
	"orrery_v2/internal/decision"
	knowledgecore "orrery_v2/internal/knowledge/core"
	memcapture "orrery_v2/internal/memory/capture"
	mempageindex "orrery_v2/internal/memory/pageindex"
	"orrery_v2/internal/platform"
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/logging"
)

type productionMemoryPageIndexRepository interface {
	memorypreference.Repository
	mempageindex.Repository
	productionDiscussionMemoryRepository
}

func newProductionMemoryRuntime(
	rt *platform.Runtime,
	core *db.Store,
	memory productionMemoryPageIndexRepository,
	providers dynamicKnowledgeProviderRegistry,
	settings productionSettingsStore,
	profiles *agentcore.PlatformProfileRepository,
	audit memcapture.AuditRecorder,
	metadata memcapture.RecordMetadataSink,
	shadow ...*decision.Observer,
) (*mempageindex.Service, *productionDiscussionMemoryProvider, error) {
	var observer *decision.Observer
	if len(shadow) > 0 {
		observer = shadow[0]
	}
	service := mempageindex.NewService(mempageindex.ServiceOptions{
		Memory: memory,
		State:  mempageindex.NewPlatformState(core),
		Provider: func() mempageindex.Provider {
			return providers.Provider(knowledgecore.ProviderPageIndex)
		},
		Now:           rt.Clock.Now,
		ObserveRecall: mempageindex.NewDecisionRecallObserver(observer),
	})
	preferences := memorypreference.NewService(memorypreference.Options{
		Repository: memory, Enabled: settings.MemoryEnabled, Now: rt.Clock.Now, Audit: audit, Metadata: metadata,
		Extractor: newProductionPreferenceExtractor(settings, platformSecretResolver{provider: rt.Secrets}),
	})
	if err := registerProductionPreferenceWorker(rt, preferences); err != nil {
		return nil, nil, err
	}
	discussion := newProductionDiscussionMemoryProvider(profiles, memory, productionDiscussionMemoryProviderOptions{
		PreferenceQueue: rt.ExecutionQueue,
		Audit:           audit, Metadata: metadata, Now: rt.Clock.Now, Enabled: settings.MemoryEnabled, Recall: service,
	})
	return service, discussion, nil
}

func registerProductionMemoryPageIndexSweeper(rt *platform.Runtime, service *mempageindex.Service, settings productionSettingsStore) error {
	sweeper := platform.SweeperFunc(func(ctx context.Context) error {
		current, err := settings.GetSettings(ctx)
		if err != nil {
			return err
		}
		if !current.Memory.Enabled || !current.Knowledge.Enabled || !pageIndexKnowledgeConfigured(current.Knowledge.PageIndex) {
			return nil
		}
		report, err := service.Reconcile(ctx, 0)
		if err != nil {
			return err
		}
		if report.Failed > 0 {
			rt.Logger.Warn(ctx, "memory PageIndex reconciliation completed with failures",
				logging.Int("scanned", report.Scanned), logging.Int("failed", report.Failed))
		}
		return nil
	})
	return rt.RegisterSweeper("memory.pageindex", newProductionCadencedSweeper(
		sweeper, rt.Clock.Now, productionIncrementalMaintenanceInterval,
	))
}
