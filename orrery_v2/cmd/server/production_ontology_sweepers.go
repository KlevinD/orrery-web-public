package main

import (
	"context"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/discussionsession"
	"orrery_v2/internal/app/missioncontrol"
	"orrery_v2/internal/app/ontology"
	resourceadmin "orrery_v2/internal/app/resourceadmin"
	"orrery_v2/internal/app/responsibility"
	schedulerapp "orrery_v2/internal/app/scheduler"
	knowledgestore "orrery_v2/internal/knowledge/store"
	mempageindex "orrery_v2/internal/memory/pageindex"
	memstore "orrery_v2/internal/memory/store"
	"orrery_v2/internal/platform"
	"orrery_v2/internal/platform/db"
	toolres "orrery_v2/internal/resource/tool"
)

const (
	productionMCPToolCacheSyncBatchSize     = 2
	productionMCPToolCacheSyncServerTimeout = 5 * time.Second
)

func registerProductionSweepers(
	rt *platform.Runtime,
	core *db.Store,
	tools toolres.Registry,
	mcp resourceadmin.MCPRuntimePort,
	schedulerRepository *schedulerapp.Repository,
	schedulerService *schedulerapp.Service,
	schedulerDispatcher schedulerapp.RunDispatcher,
	responsibilities *responsibility.Repository,
	repairExecutions *productionRepairExecutionReconciler,
	projectionRunner *ontology.MappingRuleProjectionRunner,
	scopes *productionOntologyScopeState,
	knowledgeRepo knowledgestore.Repository,
	knowledgeProviders dynamicKnowledgeProviderRegistry,
	memoryPageIndex *mempageindex.Service,
	memoryRepository memstore.Repository,
	settings productionSettingsStore,
	secrets platformSecretResolver,
	memoryProjector productionMaterializedMemoryMetadataSink,
	discussions *discussionsession.Repository,
	missions *missioncontrol.DBProjectionRepository,
	inference *productionOntologyInferenceAPI,
	profiles *agentcore.PlatformProfileRepository,
) error {
	if err := registerProductionContinuousInference(rt, core, inference, profiles, settings, secrets); err != nil {
		return err
	}
	if err := registerProductionDocumentInference(rt, core, inference, knowledgeRepo, knowledgeProviders, settings, secrets); err != nil {
		return err
	}
	if err := rt.RegisterSweeper("resource.mcp_tool_cache_sync", resourceadmin.NewMCPToolCacheSyncer(resourceadmin.MCPToolCacheSyncerOptions{
		Registry:      tools,
		Runtime:       mcp,
		BatchSize:     productionMCPToolCacheSyncBatchSize,
		ServerTimeout: productionMCPToolCacheSyncServerTimeout,
	})); err != nil {
		return err
	}
	if err := rt.RegisterSweeper("scheduler.scheduled_tasks", newProductionSchedulerSweeper(rt, schedulerRepository, schedulerService, schedulerDispatcher, responsibilities)); err != nil {
		return err
	}
	if err := registerProductionResponsibilityRunRetentionSweeper(rt, schedulerRepository); err != nil {
		return err
	}
	if err := rt.RegisterSweeper("reliability.repair_execution", platform.SweeperFunc(func(ctx context.Context) error {
		_, err := repairExecutions.Sweep(ctx, rt.Config.Sweeper.BatchSize)
		return err
	})); err != nil {
		return err
	}
	if err := registerProductionOntologySweepers(rt, core, projectionRunner, scopes); err != nil {
		return err
	}
	if err := registerProductionMemoryPageIndexSweeper(rt, memoryPageIndex, settings); err != nil {
		return err
	}
	if err := registerProductionMemoryOntologySweeper(rt, core, memoryRepository, missions, newProductionMemoryOntologyExtractor(settings, secrets), memoryProjector); err != nil {
		return err
	}
	if err := registerProductionAgentTraceSweeper(rt, core, discussions, missions); err != nil {
		return err
	}
	return registerProductionKnowledgeOntologySweeper(rt, core, knowledgeRepo, knowledgeProviders, projectionRunner)
}

func registerProductionOntologySweepers(
	rt *platform.Runtime,
	core *db.Store,
	projectionRunner mappingProjectionRefresher,
	scopes *productionOntologyScopeState,
) error {
	registrations := []func() error{
		func() error { return registerProductionScheduledRunOntologyRetentionSweeper(rt, core) },
		func() error { return registerProductionOntologyMetricRecordingSweeper(rt, core) },
		func() error { return registerProductionOntologyPolicyAlertAuditSweeper(rt, core) },
		func() error { return registerProductionOntologyAlertNotificationSweeperIfEnabled(rt, core) },
		func() error { return registerProductionOntologyAlertNotificationRetrySweeperIfEnabled(rt, core) },
		func() error { return registerProductionOntologyArtifactHealthSweeper(rt, core) },
		func() error { return registerProductionMappingProjectionRefreshSweeper(rt, core, projectionRunner) },
		func() error { return registerProductionOntologySourceDeletionRecoverySweeper(rt, scopes) },
	}
	for _, register := range registrations {
		if err := register(); err != nil {
			return err
		}
	}
	return nil
}
