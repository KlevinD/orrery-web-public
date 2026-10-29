package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/contextgraph"
	"orrery_v2/internal/app/discussionsession"
	"orrery_v2/internal/app/knowledgeontology"
	memorydiaryapp "orrery_v2/internal/app/memorydiary"
	"orrery_v2/internal/app/memoryontology"
	"orrery_v2/internal/app/operations"
	schedulerapp "orrery_v2/internal/app/scheduler"
	gwbindings "orrery_v2/internal/gateway/binding"
	websitechannel "orrery_v2/internal/gateway/channel/website"
	gwconfig "orrery_v2/internal/gateway/config"
	knowledgecore "orrery_v2/internal/knowledge/core"
	knowledgestore "orrery_v2/internal/knowledge/store"
	memcore "orrery_v2/internal/memory/core"
	mempageindex "orrery_v2/internal/memory/pageindex"
	memstore "orrery_v2/internal/memory/store"
	"orrery_v2/internal/platform"
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/logging"
)

type businessTenantDiaryState interface {
	ReassignTenant(context.Context, string, string) (int, error)
}

type businessTenantContextGraphState interface {
	ReassignTenant(context.Context, string, string) (int, error)
}

type businessTenantDiscussionState interface {
	ReassignTenant(context.Context, string, string) (int, error)
}

type businessTenantMemoryOntologyState interface {
	ReassignTenant(context.Context, string, string) (int, error)
}

func legacyBusinessOrganizationIDs() []string {
	return []string{
		legacySystemSupportOrganizationID,
		legacyAgentCoordinationOrgID,
		legacyHumanCollaboratorsOrgID,
	}
}

func isLegacyBusinessOrganizationID(id string) bool {
	id = strings.TrimSpace(id)
	for _, legacyID := range legacyBusinessOrganizationIDs() {
		if id == legacyID {
			return true
		}
	}
	return false
}

func reconcileProductionBusinessTenantRuntimeState(
	ctx context.Context,
	disabled bool,
	core *db.Store,
	router *db.Router,
	organizations *operations.OrganizationCatalogStore,
	profiles *agentcore.PlatformProfileRepository,
	memory *memstore.PlatformStore,
	diary businessTenantDiaryState,
	knowledge knowledgestore.Repository,
	now func() time.Time,
) error {
	if disabled {
		return nil
	}
	graph := contextgraph.NewPlatformStore(core)
	if err := reconcileRetiredPrincipalOntologyTenants(ctx, graph); err != nil {
		return err
	}
	if err := reconcileLegacyBusinessGraphAliases(ctx, graph); err != nil {
		return err
	}
	if err := reconcileLegacyBusinessTenantState(
		ctx, organizations, profiles, memory, mempageindex.NewPlatformState(core), memoryontology.NewPlatformStore(core), diary,
		graph, discussionsession.NewRepository(router), knowledge, now,
	); err != nil {
		return err
	}
	return reconcileProductionKnowledgeOntologyBindings(ctx, knowledge, knowledgeontology.NewPlatformStore(core), organizations)
}

func newProductionBusinessTenantRuntimeState(
	ctx context.Context,
	disabled bool,
	core *db.Store,
	router *db.Router,
	organizations *operations.OrganizationCatalogStore,
	profiles *agentcore.PlatformProfileRepository,
	memory *memstore.PlatformStore,
	settings productionSettingsStore,
	now func() time.Time,
	logger logging.Logger,
) (*memorydiaryapp.PlatformFlushStateStore, *memorydiaryapp.Coordinator, *knowledgestore.PlatformStore, error) {
	diary, coordinator := newProductionAgentDiaryRuntime(core, settings, now)
	knowledge := knowledgestore.NewPlatformStore(core)
	if err := reconcileProductionBusinessTenantRuntimeState(ctx, disabled, core, router, organizations, profiles, memory, diary, knowledge, now); err != nil {
		return nil, nil, nil, err
	}
	if !disabled {
		report, err := migrateCanonicalAgentKnowledgeNames(ctx, profiles, knowledge, now)
		if err != nil {
			return nil, nil, nil, err
		}
		if report.Renamed > 0 {
			logger.Info(ctx, "canonicalized agent knowledge base names", logging.Int("renamed", report.Renamed))
		}
		retired, err := disableRetiredEmptyKnowledgeBases(ctx, profiles, knowledge, now)
		if err != nil {
			return nil, nil, nil, err
		}
		if retired.Disabled > 0 {
			logger.Info(ctx, "disabled retired empty knowledge bases",
				logging.Int("scanned", retired.Scanned),
				logging.Int("disabled", retired.Disabled))
		}
	}
	return diary, coordinator, knowledge, nil
}

func newProductionSchedulerRepositoryWithTenantMigration(ctx context.Context, disabled bool, router *db.Router) (*schedulerapp.Repository, error) {
	repository := schedulerapp.NewRepository(router)
	if !disabled {
		if err := reconcileLegacyScheduledMemoryScopes(ctx, repository); err != nil {
			return nil, err
		}
	}
	return repository, nil
}

func validatedProductionGatewayConfigStoreWithTenantMigration(ctx context.Context, disabled bool, core *db.Store, rt *platform.Runtime) (*gwconfig.Store, error) {
	gateway, err := validatedProductionGatewayConfigStore(rt)
	if err != nil {
		return nil, err
	}
	if !disabled {
		if err := reconcileLegacyGatewayTenantState(ctx, gateway, gwbindings.NewStore(core)); err != nil {
			return nil, err
		}
	}
	return gateway, nil
}

func newProductionGatewayAndWebsiteKnowledgeRuntime(ctx context.Context, disabled bool, core *db.Store, rt *platform.Runtime) (*gwconfig.Store, *websitechannel.SQLWebsiteGapStore, *websitechannel.WebsiteFAQCatalog, error) {
	gateway, err := validatedProductionGatewayConfigStoreWithTenantMigration(ctx, disabled, core, rt)
	if err != nil {
		return nil, nil, nil, err
	}
	store, catalog, err := newProductionWebsiteKnowledgeRuntime(ctx, core)
	if err != nil {
		return nil, nil, nil, err
	}
	if err := registerProductionWebsiteDistillationRetention(rt, store); err != nil {
		return nil, nil, nil, err
	}
	return gateway, store, catalog, nil
}

func reconcileLegacyGatewayTenantState(ctx context.Context, gateway *gwconfig.Store, bindings *gwbindings.Store) error {
	if gateway == nil || bindings == nil {
		return fmt.Errorf("gateway config and binding stores are required")
	}
	for _, legacyID := range legacyBusinessOrganizationIDs() {
		if _, err := gateway.ReassignWeixinTenant(legacyID, defaultBusinessOrganizationID); err != nil {
			return fmt.Errorf("migrate Weixin gateway tenant %s: %w", legacyID, err)
		}
		if _, err := bindings.ReassignTenant(ctx, legacyID, defaultBusinessOrganizationID); err != nil {
			return fmt.Errorf("migrate Gateway channel tenant %s: %w", legacyID, err)
		}
	}
	return nil
}

func reconcileLegacyScheduledMemoryScopes(ctx context.Context, repository *schedulerapp.Repository) error {
	if repository == nil {
		return fmt.Errorf("scheduler repository is required")
	}
	tasks, err := repository.ListTasks(ctx, true)
	if err != nil {
		return err
	}
	for _, task := range tasks {
		changed := false
		if task.Target.TargetScopeKind == "organization" && isLegacyBusinessOrganizationID(task.Target.TargetScopeID) {
			task.Target.TargetScopeID = defaultBusinessOrganizationID
			changed = true
		}
		if task.Target.SourceScopeKind == "organization" && isLegacyBusinessOrganizationID(task.Target.SourceScopeID) {
			task.Target.SourceScopeID = defaultBusinessOrganizationID
			changed = true
		}
		if changed {
			if err := repository.SaveTask(ctx, task); err != nil {
				return fmt.Errorf("migrate scheduled task %s organization scope: %w", task.ScheduledTaskID, err)
			}
		}
	}
	return nil
}

func reconcileLegacyBusinessTenantState(
	ctx context.Context,
	organizations *operations.OrganizationCatalogStore,
	profiles *agentcore.PlatformProfileRepository,
	memory *memstore.PlatformStore,
	memoryPageIndex *mempageindex.PlatformState,
	memoryOntology businessTenantMemoryOntologyState,
	diary businessTenantDiaryState,
	graph businessTenantContextGraphState,
	discussions businessTenantDiscussionState,
	knowledge knowledgestore.Repository,
	now func() time.Time,
) error {
	if profiles == nil || memory == nil || memoryPageIndex == nil || memoryOntology == nil || diary == nil || graph == nil || discussions == nil || knowledge == nil {
		return fmt.Errorf("business tenant migration dependencies are required")
	}
	if err := migrateLegacySystemSupportProfiles(ctx, profiles); err != nil {
		return fmt.Errorf("migrate workforce organization: %w", err)
	}
	businessMemoryProfileID := memcore.ScopeMemoryProfileID(memcore.MemoryScope{Kind: memcore.ScopeOrganization, ID: defaultBusinessOrganizationID})
	for _, legacyID := range legacyBusinessOrganizationIDs() {
		legacyMemoryProfileID := memcore.ScopeMemoryProfileID(memcore.MemoryScope{Kind: memcore.ScopeOrganization, ID: legacyID})
		if _, err := memory.ReassignOrganizationScope(ctx, legacyID, defaultBusinessOrganizationID); err != nil {
			return fmt.Errorf("migrate memory organization %s: %w", legacyID, err)
		}
		if err := memoryPageIndex.ReassignProfile(ctx, legacyMemoryProfileID, businessMemoryProfileID); err != nil {
			return fmt.Errorf("migrate memory PageIndex binding %s: %w", legacyID, err)
		}
		if _, err := memoryOntology.ReassignTenant(ctx, legacyID, defaultBusinessOrganizationID); err != nil {
			return fmt.Errorf("migrate memory ontology tenant %s: %w", legacyID, err)
		}
		if _, err := diary.ReassignTenant(ctx, legacyID, defaultBusinessOrganizationID); err != nil {
			return fmt.Errorf("migrate memory diary tenant %s: %w", legacyID, err)
		}
		if _, err := graph.ReassignTenant(ctx, legacyID, defaultBusinessOrganizationID); err != nil {
			return fmt.Errorf("migrate context graph tenant %s: %w", legacyID, err)
		}
		if _, err := discussions.ReassignTenant(ctx, legacyID, defaultBusinessOrganizationID); err != nil {
			return fmt.Errorf("migrate discussion tenant %s: %w", legacyID, err)
		}
		page, err := knowledge.ListKnowledgeBases(ctx, knowledgecore.KnowledgeBaseQuery{
			OwnerType: knowledgecore.OwnerOrganization,
			OwnerID:   legacyID,
		})
		if err != nil {
			return fmt.Errorf("list legacy organization %s knowledge bases: %w", legacyID, err)
		}
		for _, base := range page.Items {
			base.OwnerID = defaultBusinessOrganizationID
			if now != nil {
				base.UpdatedAt = now().UTC()
			}
			if err := knowledge.SaveKnowledgeBase(ctx, base); err != nil {
				return fmt.Errorf("migrate knowledge base %s owner: %w", base.ID, err)
			}
		}
		bindings, err := knowledge.ListBindings(ctx, knowledgecore.BindingQuery{
			BoundToType: knowledgecore.BindingSubjectOrganization,
			BoundToID:   legacyID,
		})
		if err != nil {
			return fmt.Errorf("list legacy organization %s knowledge access: %w", legacyID, err)
		}
		for _, binding := range bindings {
			binding.BoundToID = defaultBusinessOrganizationID
			if err := knowledge.SaveBinding(ctx, binding); err != nil {
				return fmt.Errorf("migrate knowledge binding %s: %w", binding.ID, err)
			}
		}
		if organizations != nil {
			if err := organizations.DeleteOrganization(ctx, legacyID); err != nil {
				return fmt.Errorf("remove legacy organization %s: %w", legacyID, err)
			}
		}
	}
	return nil
}
