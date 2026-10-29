package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	agentlifecycle "orrery_v2/internal/agent/lifecycle"
	deviceprovider "orrery_v2/internal/agent/runtime/providers/device"
	"orrery_v2/internal/app/discussionsession"
	"orrery_v2/internal/app/hil"
	"orrery_v2/internal/app/identity"
	"orrery_v2/internal/app/knowledgeadmin"
	"orrery_v2/internal/app/knowledgeontology"
	"orrery_v2/internal/app/memoryadmin"
	memorydiaryapp "orrery_v2/internal/app/memorydiary"
	"orrery_v2/internal/app/memorydistill"
	missioncontrol "orrery_v2/internal/app/missioncontrol"
	"orrery_v2/internal/app/operations"
	projectadmin "orrery_v2/internal/app/projectadmin"
	"orrery_v2/internal/app/rebac"
	resourceadmin "orrery_v2/internal/app/resourceadmin"
	schedulerapp "orrery_v2/internal/app/scheduler"
	todoapp "orrery_v2/internal/app/todo"
	"orrery_v2/internal/app/userauth"
	gwbindings "orrery_v2/internal/gateway/binding"
	emailchannel "orrery_v2/internal/gateway/channel/email"
	knowledgeconfig "orrery_v2/internal/knowledge/config"
	knowledgeingest "orrery_v2/internal/knowledge/ingest"
	"orrery_v2/internal/knowledge/vector/jsonstore"
	memaudit "orrery_v2/internal/memory/audit"
	missionevent "orrery_v2/internal/mission/event"
	"orrery_v2/internal/platform"
	"orrery_v2/internal/platform/document/chunker/slidingwindow"
	docextractor "orrery_v2/internal/platform/document/extractor"
	"orrery_v2/internal/platform/eventstore"
	"orrery_v2/internal/platform/logging"
	skillres "orrery_v2/internal/resource/skill"
	httptransport "orrery_v2/internal/transport/http"
)

func newProductionHandlerWithLaya(ctx context.Context, rt *platform.Runtime, opts productionHandlerOptions, observer *productionLayaObserver) (http.Handler, func(), error) {
	core, ontologySecurity, err := prepareProductionOntologyRuntime(ctx, rt, opts.DisableBackgroundWorkers)
	if err != nil {
		return nil, func() {}, err
	}
	profiles, memoryStore, err := newProductionProfileStores(ctx, rt, core, opts.DisableBackgroundWorkers)
	if err != nil {
		return nil, func() {}, err
	}
	toolRegistry, skillRegistry, skillBundleRegistry, skillPackageStore, err := newProductionResourceRegistries(ctx, core, rt.Config.BaseDir)
	if err != nil {
		return nil, func() {}, err
	}
	audit, orgCatalog, roleStore := operations.NewAuditStore(rt.DBRouter, rt.Clock, rt.IDGenerator), operations.NewOrganizationCatalogStore(rt.DBRouter), operations.NewRoleStore(rt.DBRouter)
	rolePermissionAuthorizer := operations.NewRolePermissionAuthorizer(roleStore, orgCatalog, profiles)
	evals := operations.NewPlatformEvaluationProjectionStore(rt.DBRouter)
	authorizationService, authorizationShadowStats, err := authorizationServiceFromRuntime(rt)
	if err != nil {
		return nil, func() {}, err
	}
	authorizationRelations := authorizationRelationsFromPlatform(rt.Config, authorizationService)
	todoRepository, baseMissionRepo, projectRepo := todoapp.NewRepository(rt.DBRouter), missioncontrol.NewDBProjectionRepository(rt.DBRouter), projectadmin.NewPlatformRepository(rt.DBRouter)
	missionRepo := newProductionTodoProgressProjectionRepository(operations.NewAutoProjectingMissionProjectionRepository(baseMissionRepo, evals), baseMissionRepo, todoRepository, rt.Clock)
	missionRepo.SetAuditStore(audit)
	if err := registerProductionTodoProgressProjectionSweeper(rt, missionRepo); err != nil {
		return nil, func() {}, err
	}
	ontologyScopeAPI, ontologySchemaAPI, err := newProductionOntologyScopeAndSchemaAPIs(
		core, ontologySecurity, orgCatalog, projectRepo, baseMissionRepo,
		toolRegistry, authorizationService, rolePermissionAuthorizer,
	)
	if err != nil {
		return nil, func() {}, err
	}
	profileAuthStore := userauth.NewProfileAccountStore(profiles)
	authStateStore := userauth.NewPlatformAuthStateStore(core)
	authStore := userauth.NewSplitAccountStore(profileAuthStore, audit).WithAuthStateStore(authStateStore)
	if !opts.DisableBackgroundWorkers {
		if err := ensureProductionAuthenticationBootstrap(context.Background(), rt.Config, profiles, orgCatalog,
			productionOntologySourceDeletionLifecycle{state: ontologySecurity.Scopes}, authStore, roleStore); err != nil {
			return nil, func() {}, fmt.Errorf("bootstrap production authentication: %w", err)
		}
		if err := ensureDefaultOntologyStewardPeopleProjection(context.Background(), rt); err != nil {
			rt.Logger.Warn(context.Background(), "bootstrap production People projection unavailable", logging.Any("error", err))
		}
	}
	identitySubjects := identity.NewPlatformSubjectStore(core)
	roleScopeMode, err := authorizationRoleScopeModeFromPlatform(rt.Config)
	if err != nil {
		return nil, func() {}, err
	}
	roleScopeResolver := operations.NewRolePermissionScopeResolver(roleStore).WithMode(roleScopeMode)
	authService := userauth.NewService(authStore).
		WithIdentitySubjectStore(identitySubjects).
		WithPermissionScopeResolver(roleScopeResolver)
	if err := registerAuthorizationRoleScopeTransitionReadiness(rt, roleScopeResolver, profiles); err != nil {
		return nil, func() {}, err
	}
	identityAuthCfg, err := identityAuthConfigFromPlatform(rt.Config)
	if err != nil {
		return nil, func() {}, err
	}
	peopleProjectionStore, peopleProjection, err := configureProductionPeopleAccountProjection(rt, core, profiles, identityAuthCfg)
	if err != nil {
		return nil, func() {}, err
	}
	identityProviders, err := configureProductionBrowserIdentityProviders(identityAuthCfg, authStateStore, authService)
	if err != nil {
		return nil, func() {}, err
	}
	identityAdmin := identity.NewSubjectAdminService(identitySubjects).WithSessionRevoker(authService)
	identityAuth := userauth.NewBrowserLoginService(authService, identity.NewSubjectMapper(identitySubjects).WithAutoLink(identityAuthCfg.AutoLinkEnabled), identityAuthCfg.LocalEnabled, identityProviders...)
	if peopleProjectionStore != nil {
		identityAuth.WithPersonAccessValidator(peopleProjectionStore)
		identityAuth.WithInitialPasswordRequirementReader(identityAuthCfg.KratosAdmin)
		authService.WithExternalPasswordChanger(identityAuthCfg.KratosAdmin)
	}
	if identityAuthCfg.Feishu != nil && identityAuthCfg.FeishuAutoProvision {
		identityAuth.RegisterProvisioner(identity.ProviderFeishu, feishuIdentityProvisioner{
			profiles: profiles,
			subjects: identityAdmin,
		})
	}
	var profileIdentity *identity.SelfServiceBindingService
	if identityAuthCfg.Feishu != nil {
		profileIdentity = identity.NewSelfServiceBindingService(identityAuthCfg.Feishu, identitySubjects, authService)
	}
	var bearerAuth *userauth.TokenPrincipalService
	if identityAuthCfg.OIDC != nil {
		bearerAuth = userauth.NewTokenPrincipalService(authService, identity.NewSubjectMapper(identitySubjects).WithAutoLink(identityAuthCfg.AutoLinkEnabled), identityAuthCfg.OIDC)
	}
	oidcClientAdmin := identityAuthCfg.OIDCClientAdmin
	if oidcClientAdmin == nil {
		oidcClientAdmin = identity.NewOIDCClientAdminService(identityAuthCfg.OIDCClients)
	}
	settingsStore := productionSettingsStore{router: rt.DBRouter, config: rt.Config}
	diaryState, diaryCoordinator, knowledgeRepo, err := newProductionBusinessTenantRuntimeState(ctx, opts.DisableBackgroundWorkers, core, rt.DBRouter, orgCatalog, profiles, memoryStore, settingsStore, rt.Clock.Now, rt.Logger)
	if err != nil {
		return nil, func() {}, fmt.Errorf("reconcile business tenant runtime: %w", err)
	}
	ontologyAggregateAPIs, ontologyExecutionAPI, err := newProductionOntologyAggregateReadAPIs(
		core, rt.DBRouter, ontologySecurity, ontologyScopeAPI, ontologySchemaAPI,
		authorizationService, knowledgeRepo, memoryStore, audit,
	)
	if err != nil {
		return nil, func() {}, err
	}
	knowledgeCfg, err := knowledgeconfig.FromPlatformConfig(rt.Config)
	if err != nil {
		return nil, func() {}, err
	}
	knowledgeRoot := knowledgeCfg.Root
	knowledgeVector := jsonstore.NewStore(knowledgeRoot)
	knowledgeObs := knowledgeMetrics(rt)
	knowledgeEmbedding := newKnowledgeEmbeddingProvider(settingsStore, platformSecretResolver{provider: rt.Secrets}, http.DefaultClient).WithMetrics(knowledgeObs)
	mcpRuntime := newProductionExternalMCPRuntime(rt)
	externalKnowledgeProvider, knowledgeProviders, err := newProductionKnowledgeProviderServices(rt, core, toolRegistry, mcpRuntime, knowledgeRepo, opts.DisableBackgroundWorkers)
	if err != nil {
		return nil, func() {}, err
	}
	knowledgeSettingsCache := newProductionKnowledgeSettingsCache(ctx, settingsStore)
	knowledgeProviderRegistry := dynamicKnowledgeProviderRegistry{
		settings:      settingsStore,
		settingsCache: knowledgeSettingsCache,
		secrets:       platformSecretResolver{provider: rt.Secrets},
		repo:          knowledgeRepo,
		vector:        knowledgeVector,
		embedding:     knowledgeEmbedding,
		ids:           knowledgeIDGenerator{generator: rt.IDGenerator},
		clock:         rt.Clock,
		artifacts:     localArtifactWriterAdapter{next: platformKnowledgeArtifacts{store: rt.Artifacts}},
		execution:     platformKnowledgeExecutionSubmitter{queue: rt.ExecutionQueue},
		maxBytes:      knowledgeCfg.Ingest.MaxDocumentBytes,
		mcp:           externalKnowledgeProvider,
	}
	ontologyProjectionRunner, err := newProductionOntologyProjectionRunner(ctx, core)
	if err != nil {
		return nil, func() {}, err
	}
	knowledgeMetadataSink, err := newProductionOntologyDocumentMetadataSinkWithRunner(core, ontologyProjectionRunner)
	if err != nil {
		return nil, func() {}, err
	}
	knowledgeMetadataSink = knowledgeMetadataSink.WithTenantResolver(newProductionKnowledgeOntologyEventTenantResolver(knowledgeRepo, orgCatalog))
	knowledgeCommands := knowledgeadmin.NewCommandService(knowledgeRepo, knowledgeVector, knowledgeIDGenerator{generator: rt.IDGenerator}, rt.Clock).
		WithDefaultVectorRoot(knowledgeRoot).
		WithDefaultChunkPolicy(knowledgeCfg.DefaultChunkPolicy).
		WithIngestPolicy(knowledgeadmin.IngestPolicy{MaxDocumentBytes: knowledgeCfg.Ingest.MaxDocumentBytes}).
		WithArtifactStore(platformKnowledgeArtifacts{store: rt.Artifacts}).
		WithExecutionQueue(platformKnowledgeExecutionSubmitter{queue: rt.ExecutionQueue}).
		WithProviderRegistry(knowledgeProviderRegistry).
		WithDocumentMetadataSink(knowledgeMetadataSink)
	bootstrapKnowledgeDefaults := !opts.DisableBackgroundWorkers && shouldBootstrapKnowledgeDefaults(context.Background(), settingsStore)
	if bootstrapKnowledgeDefaults {
		if err := ensureDefaultCoordinatorKnowledgeBases(context.Background(), profiles, knowledgeCommands, knowledgeRepo, knowledgeRoot); err != nil {
			return nil, func() {}, err
		}
	}
	humanProfiles := newProductionHumanProfileBootstrap(profiles)
	if bootstrapKnowledgeDefaults {
		humanProfiles = humanProfiles.WithKnowledgeEnsurer(knowledgeCommands, knowledgeRoot)
	}
	knowledgeQueries := knowledgeadmin.NewQueryService(knowledgeRepo)
	knowledgeSearch := knowledgeSearchMetricsService{
		next:    knowledgeadmin.NewSearchService(knowledgeRepo, knowledgeVector, knowledgeEmbedding).WithProviderRegistry(knowledgeProviderRegistry).WithClock(rt.Clock.Now).WithReranker(knowledgeadmin.NewDecisionShadowReranker(observer)),
		metrics: knowledgeObs,
		now:     rt.Clock.Now,
	}
	knowledgeIngest := knowledgeingest.NewService(knowledgeRepo, rt.Clock, knowledgeingest.Options{
		Artifacts:             platformKnowledgeArtifacts{store: rt.Artifacts},
		Extractors:            docextractor.NewDefaultRegistry(),
		Chunker:               slidingwindow.New(),
		Embedding:             knowledgeEmbedding,
		VectorStore:           knowledgeVector,
		DescriptorForBase:     knowledgeDescriptorBuilder(knowledgeRoot),
		SupportedContentTypes: append([]string(nil), knowledgeCfg.Ingest.SupportedContentTypes...),
		MaxDocumentBytes:      knowledgeCfg.Ingest.MaxDocumentBytes,
		Metrics:               knowledgeObs,
		DocumentStatusSink:    knowledgeMetadataSink,
	})
	if err := registerKnowledgeExecutionHandlers(rt, knowledgeIngest, knowledgeRepo, knowledgeCommands); err != nil {
		return nil, func() {}, err
	}
	if !opts.DisableBackgroundWorkers {
		if err := ensureProductionAgentRuntimeDefaults(context.Background(), rt.Config, profiles, memoryStore, toolRegistry, skillRegistry, skillPackageStore); err != nil {
			return nil, func() {}, fmt.Errorf("ensure production agent runtime defaults: %w", err)
		}
	}
	skillRuntimeTools := skillres.NewRuntimeToolService(skillres.RuntimeToolServiceOptions{
		Registry: skillRegistry,
		Store:    skillPackageStore,
	})
	operationsQuery := operations.NewQueryService(profiles, toolRegistry)
	authorizationBackfillQuery := operations.NewQueryService(profiles, toolRegistry)
	operationsCommand := operations.NewCommandService(profiles)
	operationsCommand.SetWorkforceSessionRevoker(authService)
	configureProductionPeopleAccountOperations(operationsQuery, operationsCommand, peopleProjectionStore, peopleProjection)
	operationsCommand.SetSourceDeletionLifecycle(productionOntologySourceDeletionLifecycle{state: ontologySecurity.Scopes})
	configureProductionOperationsQuery(operationsQuery, audit, orgCatalog, roleStore, evals, rebac.NewPlatformStore(core))
	configureProductionOperationsQuery(authorizationBackfillQuery, audit, orgCatalog, roleStore, evals, rebac.NewPlatformStore(core))
	configureProductionOperationsCommand(operationsCommand, audit, orgCatalog, roleStore, authorizationRelations, memoryStore, skillRegistry, skillPackageStore, settingsAwareOperationsKnowledgeLifecycle{settings: settingsStore, next: knowledgeCommands})
	configureProductionOperationsAuthorization(operationsQuery, operationsCommand, operations.NewRoleAwareAuthorizationChecker(authorizationService, rolePermissionAuthorizer))
	if !opts.DisableBackgroundWorkers {
		if err := configureProductionPeopleWorkforce(ctx, rt, core, profiles, operationsCommand, identityAuthCfg); err != nil {
			return nil, func() {}, fmt.Errorf("configure production people workforce: %w", err)
		}
	}
	operationsService := operations.NewService(operationsQuery, operationsCommand)
	authorizationBackfillOperations := operations.NewService(authorizationBackfillQuery, nil)
	resourceQuery := resourceadmin.NewQueryServiceWithAgents(toolRegistry, skillRegistry, profiles)
	resourceQuery.SetMemoryProfileReader(memoryStore)
	resourceCommand := resourceadmin.NewCommandService(toolRegistry, skillRegistry)
	configureProductionResourceSkillStorage(resourceQuery, resourceCommand, skillPackageStore, skillBundleRegistry, resourceadmin.NewPlatformSkillVersionEvaluationStore(core))
	if err := configureProductionResourceCommand(rt, resourceCommand, audit, profiles, authorizationRelations); err != nil {
		return nil, func() {}, err
	}
	resourceCommand.SetSourceDeletionLifecycle(productionOntologySourceDeletionLifecycle{state: ontologySecurity.Scopes})
	if discoverer := configuredMCPDiscoverer(rt.Config); discoverer != nil {
		resourceQuery.SetDiscoverer(discoverer)
	}
	taskRepository, taskEvents, delivery, qa := newProductionMissionTaskServices(rt)
	responsibilityServices := newProductionResponsibilityServices(rt, taskRepository, taskEvents)
	discussions, staffing := newProductionMissionCollaborationServices(rt, profiles)
	missionEventSink := missionevent.NewStoreSink(eventstore.New(core))
	missionEventReader := missionevent.NewStoreReader(eventstore.New(core))
	systemRoles, err := defaultSystemRoleBootstrapsFromConfig(rt.Config)
	if err != nil {
		return nil, func() {}, err
	}
	lifecycle := newProductionLifecycleService(rt, missionRepo, profiles, discussions, staffing, humanProfiles, systemRoles, authorizationRelations, authorizationService, productionTaskResponsibilityAssignment{service: responsibilityServices.service}, missionEventSink)
	if err := rt.RegisterSweeper("mission.creation_intents", platform.SweeperFunc(func(sweepCtx context.Context) error {
		return lifecycle.ReconcilePendingMissionCreations(sweepCtx, 100)
	})); err != nil {
		return nil, func() {}, err
	}
	missionQuery := missioncontrol.NewQueryServiceWithCatalog(missionRepo, lifecycle)
	missionQuery.SetAuthorizationService(authorizationService)
	missionQuery.SetRuntimeHealthPort(productionRuntimeHealthPort{liveness: rt.ExecutionLive})
	missionQuery.SetAgentResourcePort(productionAgentResourcePort{profiles: profiles, tools: toolRegistry, skills: skillRegistry})
	learningSamples, learningErr := productionRSISampleStore(rt)
	if learningErr != nil {
		return nil, func() {}, learningErr
	}
	learningAdvice := productionLearningAdvice{samples: learningSamples}
	missionQuery.SetRecoveryAdvicePort(learningAdvice)
	missionCommand := missioncontrol.NewCommandServiceWithMissionPorts(delivery, qa, discussions, discussions, lifecycle)
	missionCommand.SetAuthorizationService(authorizationService)
	missionCommand.SetAuditRecorder(operations.NewMissionCommandAuditRecorder(audit))
	missionCommand.SetRuntimeCommandPorts(productionAgentRunQueue{queue: rt.ExecutionQueue}, missionRepo, missionEventSink, rt.IDGenerator, rt.Clock)
	missionCommand.SetRuntimeEventReader(missionEventReader)
	runtimeTaskProjectionReader := productionRuntimeTaskProjectionReader{unit: rt.UnitOfWork, tasks: taskRepository}
	missionCommand.SetRuntimeTaskProjectionReader(runtimeTaskProjectionReader)
	missionCommand.SetPromptBlockProvider(settingsStore)
	qaPlanRefresher := &productionAPI{
		missionRepo:  baseMissionRepo,
		eventReader:  missionevent.NewStoreReader(eventstore.New(core)),
		runtimeTasks: runtimeTaskProjectionReader,
	}
	responsibilityContextProvider, err := newProductionResponsibilityContextProvider(ctx, rt)
	if err != nil {
		return nil, func() {}, err
	}
	responsibilityCoordinator := responsibilityServices.coordinator(
		rt, responsibilityContextProvider, profiles, missionCommand, taskRepository, lifecycle, qa,
		qaPlanRefresher, missionEventReader, authorizationRelations,
		settingsStore.HumanReviewEnabled,
	)
	approvals := missioncontrol.NewApprovalService(missionRepo, missionCommand)
	approvals.SetAuthorizationService(authorizationService)
	discussionLifecycleBus := agentlifecycle.NewInMemorySignalBus(agentlifecycle.SignalBusOptions{
		Logger:  rt.Logger,
		Clock:   rt.Clock,
		Metrics: agentlifecycle.NewMetricSink(rt.Metrics),
	})
	if err := subscribeMissionLifecycleProjection(discussionLifecycleBus, missionEventSink, missionEventReader); err != nil {
		return nil, func() {}, err
	}
	discussionRepository := discussionsession.NewRepository(rt.DBRouter)
	missionCommand.SetRuntimeConversationContextProvider(productionRuntimeConversationContextProvider{repository: discussionRepository})
	realtimeStore, err := newProductionRealtimeStore(ctx, rt)
	if err != nil {
		return nil, func() {}, err
	}
	approvalWiring, err := newProductionHILAutoplanWiring(ctx, core, rt, missionRepo, discussionRepository, realtimeStore, settingsStore, profiles, !opts.DisableBackgroundWorkers, responsibilityServices.repository, missionCommand, responsibilityContextProvider)
	if err != nil {
		return nil, func() {}, err
	}
	marketingApprovalAuthorization := productionAuthorizationFromEnv("ORRERY_MARKETING_APPROVAL_AUTHORIZATION", "ORRERY_MARKETING_APPROVAL_TOKEN")
	if err := registerProductionMarketingWechatApprovalResolution(
		rt, approvalWiring.hilObservers, marketingApprovalAuthorization,
		os.Getenv("ORRERY_MARKETING_APPROVAL_CALLBACK_URL"),
		productionAuthorizationFromEnv("ORRERY_MARKETING_APPROVAL_CALLBACK_AUTHORIZATION", "ORRERY_MARKETING_APPROVAL_CALLBACK_TOKEN"),
	); err != nil {
		return nil, func() {}, err
	}
	discussionAttachmentStore := discussionsession.NewLocalAttachmentStore(filepath.Join(rt.Config.BaseDir, "data", "discussions"))
	discussionMissionToolCommands := &productionDiscussionMissionToolCommands{}
	discussionMissionTooling := discussionsession.NewMissionToolApplier(discussionMissionToolCommands)
	mcpGatewayRef, runtimeToolDispatcher := newProductionRuntimeTooling(rt, discussionRepository, discussionMissionTooling, profiles, skillRuntimeTools, diaryCoordinator, observer)
	runtimeToolDispatcher.images = &productionImageGenerator{repo: discussionRepository, settings: settingsStore, secrets: platformSecretResolver{provider: rt.Secrets}, root: filepath.Join(rt.Config.BaseDir, "data", "image-generation"), publicOrigin: rt.Config.DeviceDelivery.PublicOrigin}
	memoryAuditRecorder := memaudit.NewEventRecorder(rt.EventRecorder)
	memoryMetadataSink, materializedMemoryMetadata, err := newProductionMaterializedMemoryMetadataSinks(core, ontologyProjectionRunner)
	if err != nil {
		return nil, func() {}, err
	}
	diaryCoordinator.WithTerminalAppender(memorydiaryapp.NewDiaryService(memorydiaryapp.DiaryServiceOptions{
		Repository: memoryStore,
		Enabled:    settingsStore.MemoryEnabled,
		State:      diaryState,
		Audit:      memoryAuditRecorder,
		Metadata:   memoryMetadataSink,
		Now:        rt.Clock.Now,
	}))
	ontologyGovernanceCommands, err := newProductionOntologyGovernanceCommandAPI(
		core,
		rt.DBRouter,
		ontologySecurity.Scopes,
		ontologySecurity.Concurrency,
		ontologySecurity.PolicyStore,
		authorizationService,
		knowledgeRepo,
		memoryStore,
		ontologyProjectionRunner,
	)
	if err != nil {
		return nil, func() {}, err
	}
	projectCommand := projectadmin.NewService(projectRepo, rt.IDGenerator, rt.Clock)
	projectCommand.SetAuthorizationRelations(authorizationRelations)
	projectCommand.SetAuthorizationService(authorizationService)
	developmentResources, err := newProductionDevelopmentResources(rt, authorizationService, authorizationRelations, audit)
	if err != nil {
		return nil, func() {}, err
	}
	if err := registerDevelopmentResourceEffectReconciler(rt, developmentResources); err != nil {
		return nil, func() {}, err
	}
	if !opts.DisableBackgroundWorkers {
		if err := runAuthorizationRelationBackfillFromRuntime(ctx, rt, authorizationShadowStats, authorizationRelations, baseMissionRepo, discussionRepository, projectadmin.NewPlatformRepository(rt.DBRouter), developmentResources.projectRepository, developmentResources.connectionRepository, todoRepository, authorizationBackfillOperations, profiles); err != nil {
			return nil, func() {}, err
		}
	}
	if err := registerAuthorizationRelationReconciliationSweeper(rt, authorizationRelations, baseMissionRepo, discussionRepository, projectadmin.NewPlatformRepository(rt.DBRouter), developmentResources.projectRepository, developmentResources.connectionRepository, todoRepository, authorizationBackfillOperations, profiles); err != nil {
		return nil, func() {}, err
	}
	memoryAdmin := memoryadmin.NewService(memoryStore, memoryAuditRecorder, rt.Clock.Now).
		WithMemoryRecordMetadataSink(memoryMetadataSink).
		WithCitationEvidenceResolver(newProductionMemoryCitationEvidenceResolver(core))
	memoryDistill := memorydistill.NewService(memoryStore, memoryAuditRecorder, rt.Clock.Now)
	schedulerRepository, err := newProductionSchedulerRepositoryWithTenantMigration(ctx, opts.DisableBackgroundWorkers, rt.DBRouter)
	if err != nil {
		return nil, func() {}, err
	}
	agentExecutionOntology, ontologyActionAPI, repairExecutionReconciler, ontologyReliabilityAPI, ontologyProblemAPI := newProductionAgentExecutionServices(
		core, ontologyProjectionRunner, schedulerRepository, profiles, authorizationService, rt.IDGenerator, ontologyExecutionAPI,
		rt.Clock.Now, rt.MetricRecorder, ontologySecurity.Scopes.Gate, approvalWiring.hilService, missionCommand, rt.ExecutionQueue, memoryStore,
	)
	ontologyStateRefresher := newProductionOntologyStateRefresher(core, schedulerRepository, agentExecutionOntology, rt.Clock.Now, rt.MetricRecorder)
	clientDiagnostics, err := registerProductionFailureRecovery(rt, agentExecutionOntology.reliability, ontologyReliabilityAPI, knowledgeProviderRegistry, opts.DisableBackgroundWorkers)
	if err != nil {
		return nil, func() {}, err
	}
	deviceBroker, err := newProductionDeviceBroker(ctx, rt)
	if err != nil {
		return nil, func() {}, err
	}
	personalAssistant, err := newProductionPersonalAssistantService(ctx, rt, deviceBroker, profiles, memoryStore, enabledDefaultKnowledgeEnsurer(opts.DisableBackgroundWorkers, knowledgeCommands), knowledgeRepo, knowledgeRoot)
	if err != nil {
		return nil, func() {}, err
	}
	runtimeToolDispatcher.desktopTools = &deviceprovider.WorkspaceToolDispatcher{Broker: deviceBroker, Resolve: productionDesktopToolResolver(personalAssistant), Now: rt.Clock.Now}
	runtimeFeedbackJournal, err := newProductionRuntimeFeedbackJournal(rt.Config.BaseDir, opts.DisableBackgroundWorkers)
	if err != nil {
		return nil, func() {}, err
	}
	discussionAgentDeps := newProductionDiscussionAgentDependencies(discussionLifecycleBus, productionTraceStore{store: rt.FileStore, indexSink: newProductionTraceIndexSink(core), evidenceCore: core, evidenceJournal: runtimeFeedbackJournal}, nil)
	agentResultNotifier := &productionAgentResultNotifier{mission: discussions}
	discussionAgentDeps.Notifier = agentResultNotifier
	discussionAgentDeps = configureProductionDiscussionAgentDependencies(discussionAgentDeps, rt, settingsStore, runtimeToolDispatcher, memoryStore, memoryAuditRecorder, materializedMemoryMetadata, approvalWiring.runtimeApprovalCoordinator, diaryCoordinator)
	discussionAgentDeps.DeviceBroker = deviceBroker
	discussionAgentDeps.PersonalAssistant = personalAssistant
	sharedDiscussionRuntimeRegistry, agentCoreRecovery := configureProductionRuntimeTaskCommands(&discussionAgentDeps, missionCommand)
	var discussionAgentSetupError error
	discussionAgent, err := newConfiguredDiscussionAgentRunner(context.Background(), rt.Config, profiles, memoryStore, toolRegistry, skillRegistry, discussionAgentDeps)
	if err != nil {
		if !errors.Is(err, discussionsession.ErrRuntimeUnavailable) {
			return nil, func() {}, err
		}
		rt.Logger.Warn(context.Background(), "discussion runtime unavailable", logging.Any("error", err))
		discussionAgentSetupError = discussionsession.RuntimeUnavailableError(err)
		discussionAgent = nil
	}
	scheduledDelivery := &scheduledAgentDeliveryRelay{}
	if err := registerProductionAgentRunWorker(productionAgentRunWorkerRegistration{
		Runtime: rt, Core: core, Profiles: profiles,
		Memory: memoryStore, MemoryRepo: memoryStore,
		Tools: toolRegistry, Skills: skillRegistry, Lifecycle: discussionLifecycleBus,
		TraceStore: rt.FileStore, Delivery: delivery, Tasks: taskRepository,
		EvidenceJournal: runtimeFeedbackJournal,
		MissionCatalog:  baseMissionRepo, MissionRepo: baseMissionRepo, MissionSink: missionEventSink,
		ExecutionOntology: agentExecutionOntology, MemoryAudit: memoryAuditRecorder, Settings: settingsStore,
		Secrets: platformSecretResolver{provider: rt.Secrets}, DiaryFlush: diaryCoordinator, ToolDispatcher: runtimeToolDispatcher,
		ApprovalBroker: approvalWiring.runtimeApprovalCoordinator, RuntimeRegistry: sharedDiscussionRuntimeRegistry, Responsibility: productionResponsibilityRuntimeLifecycle{service: responsibilityServices.service},
		Notifier: agentResultNotifier, MissionWorkspaces: agentResultNotifier, DevelopmentExecution: developmentResources.projects, Scheduler: productionSchedulerRunTracker{repo: schedulerRepository, clock: rt.Clock, responsibilities: responsibilityServices.repository}, SchedulerTasks: schedulerRepository,
		ScheduledDelivery:           scheduledDelivery,
		ScheduledDeliveryAuthorizer: gatewayScheduledAgentDeliveryAuthorizer{bindings: gwbindings.NewStore(core)},
	}); err != nil {
		return nil, func() {}, err
	}
	api := productionAPI{
		agentCoreRecovery:            agentCoreRecovery,
		missionQuery:                 missionQuery,
		missionCommand:               missionCommand,
		missionRepo:                  missionRepo,
		missionMembers:               discussions,
		discussionRepository:         discussionRepository,
		missionLinkCleaner:           discussionRepository,
		missionResponsibilityCleaner: responsibilityServices.repository,
		authorizationRelations:       authorizationRelations,
		authorizationShadow:          authorizationShadowStats,
		authorizationBackfill:        authorizationBackfillOperations,
		unitOfWork:                   rt.UnitOfWork,
		taskRepo:                     taskRepository,
		approvals:                    approvals,
		stream:                       missioncontrol.NewRepositoryStreamService(missionRepo),
		eventReader:                  missionevent.NewStoreReader(eventstore.New(core)),
		runtimeTasks:                 runtimeTaskProjectionReader,
		resourceQuery:                resourceQuery,
		resourceCommand:              resourceCommand,
		operations:                   operationsService,
		humanProfiles:                humanProfiles,
		projectCommand:               projectCommand,
		projectRepository:            projectRepo,
		developmentProjectRepository: developmentResources.projectRepository,
		scmConnectionRepository:      developmentResources.connectionRepository,
		todoRepository:               todoRepository,
		authorizationService:         authorizationService,
		ontologyProject:              ontologyProjectionRunner,
		ontologyEvents:               newProductionOntologyEventLog(core),
		contextGraphCore:             core,
		ontologySecurity:             ontologySecurity,
	}
	discussionMissionToolCommands.api = &api
	canonicalMissionCommands := newProductionSchedulerMissionCommands(missionCommand, &api)
	approvalWiring.AttachAutoplanMissionCommands(canonicalMissionCommands)
	ontologyProjectionCommands, err := newProductionOntologyProjectionCommandAPI(
		core,
		rt.DBRouter,
		ontologySecurity.Scopes,
		ontologySecurity.PolicyStore,
		authorizationService,
		knowledgeRepo,
		memoryStore,
		productionOntologyProjectionExecutor{api: api},
	)
	if err != nil {
		return nil, func() {}, err
	}
	mentionAgentDeps := newProductionDiscussionAgentDependencies(discussionLifecycleBus, productionTraceStore{store: rt.FileStore, indexSink: newProductionTraceIndexSink(core), evidenceCore: core, evidenceJournal: runtimeFeedbackJournal}, nil)
	mentionAgentDeps.Notifier = agentResultNotifier
	mentionAgentDeps = configureProductionDiscussionAgentDependencies(mentionAgentDeps, rt, settingsStore, runtimeToolDispatcher, memoryStore, memoryAuditRecorder, materializedMemoryMetadata, approvalWiring.runtimeApprovalCoordinator, diaryCoordinator)
	mentionAgentDeps.Committer = &discussionAgentConversationResultCommitter{commands: missionCommand}
	mentionAgentDeps.RuntimeRegistry = sharedDiscussionRuntimeRegistry
	mentionAssembler, err := newProductionDiscussionAgentAssembler(memoryStore, toolRegistry, skillRegistry, mentionAgentDeps)
	if err != nil {
		return nil, func() {}, err
	}
	mentionRunner := missioncontrol.NewMentionAgentRunner(missioncontrol.MentionAgentRunnerOptions{
		Assembler:     mentionAssembler,
		Profiles:      configuredRuntimeProfileStore{inner: profiles, cfg: rt.Config},
		Commands:      api,
		Query:         api,
		Repo:          baseMissionRepo,
		Events:        missionevent.NewStoreSink(eventstore.New(core)),
		EventReader:   missionevent.NewStoreReader(eventstore.New(core)),
		Clock:         rt.Clock,
		IDGenerator:   rt.IDGenerator,
		Logger:        rt.Logger,
		Timeout:       configuredDiscussionAgentTimeout(rt.Config),
		PromptBlocks:  settingsStore,
		WorkspaceRoot: firstNonBlank(rt.Config.Runtime.ExecEnvRoot, rt.Config.BaseDir),
	})
	if err := registerProductionMentionDispatch(rt, mentionRunner); err != nil {
		return nil, func() {}, err
	}
	memoryPageIndex, discussionMemoryProvider, err := newProductionMemoryRuntime(rt, core, memoryStore, knowledgeProviderRegistry, settingsStore, profiles, memoryAuditRecorder, materializedMemoryMetadata, observer)
	if err != nil {
		return nil, func() {}, err
	}
	memoryDistill.
		WithSemanticRecall(memoryPageIndex).
		WithNaturalLanguageSynthesizer(newProductionMemoryNaturalLanguageSynthesizer(settingsStore, platformSecretResolver{provider: rt.Secrets}))
	initialAgentMembers, err := initialAgentMemberSeedsFromConfig(rt.Config)
	if err != nil {
		return nil, func() {}, err
	}
	discussionSessions := discussionsession.NewService(withProductionDiscussionExecution(rt, discussionsession.ServiceOptions{
		Repository:                 discussionRepository,
		AuthorizationRelations:     productionDiscussionAuthorizationRelations{projector: authorizationRelations},
		DiscussionAgent:            discussionAgent,
		RealtimePublisher:          approvalWiring.realtimePublisher,
		RuntimeApprovalCoordinator: approvalWiring.runtimeApprovalCoordinator,
		RuntimeSetupError:          discussionAgentSetupError,
		TurnRecoveryTimeout:        2 * configuredDiscussionAgentTimeout(rt.Config),
		InitialAgentMembers:        initialAgentMembers,
		WorkspaceAllowlist:         settingsStore,
		ManagedWorkspaces:          productionManagedWorkspaceResolver{projects: developmentResources.projects},
		MentionResponder: productionDiscussionAgentMentionResponder{
			profiles:                profiles,
			memory:                  memoryStore,
			tools:                   toolRegistry,
			skills:                  skillRegistry,
			deps:                    mentionAgentDeps,
			personalAssistantRunner: discussionAgent,
			workspaceRootProvider:   productionSettingsStore{router: rt.DBRouter, config: rt.Config},
			clock:                   rt.Clock,
			idGenerator:             rt.IDGenerator,
			runtimeConfig:           rt.Config,
		},
		MentionConfig:           settingsStore,
		MissionTooling:          discussionMissionTooling,
		MissionMessenger:        productionDiscussionMissionMessenger{api: &api},
		MissionLinkAvailability: newProductionDiscussionMissionLinkAvailability(baseMissionRepo),
		ArtifactCleaner:         productionDiscussionArtifactCleaner{store: rt.FileStore},
		AttachmentStore:         discussionAttachmentStore,
		MemoryProvider:          discussionMemoryProvider,
		KnowledgeProvider:       newProductionDiscussionKnowledgeProvider(profiles, knowledgeSearch),
		AgentDirectory:          profiles,
		HumanProfiles:           api.humanProfiles,
		EvaluationReviews:       productionEvaluationReviewUsageRecorder{store: evals},
		Clock:                   rt.Clock,
		IDGenerator:             rt.IDGenerator,
		Logger:                  discussionSessionLogger{logger: rt.Logger},
	}))
	bindProductionDiscussionSession(&api, approvalWiring, discussionSessions, missionRepo, lifecycle, agentResultNotifier)
	runtimeToolDispatcher.images.discussions = discussionSessions
	developmentResources.projects.SetExecutionMonitor(productionDevelopmentExecutionMonitor{api: &api, todos: todoRepository})
	realtimeHandler, realtimeCleanup, err := newProductionRealtimeWithStore(ctx, rt, discussionSessions, developmentResources.projects, realtimeStore)
	if err != nil {
		return nil, func() {}, err
	}
	todoService := newProductionTodoService(
		rt,
		todoRepository,
		settingsStore,
		discussionSessions,
		audit,
		&api,
		developmentResources.projects,
		productionTodoDecisionPrincipalResolver{accounts: authStore, scopes: roleScopeResolver},
		authorizationService,
		authorizationRelations,
	)
	approvalWiring.AttachTodoService(todoService)
	if err := runProductionStartup(ctx, opts.DisableBackgroundWorkers, schedulerRepository, rt); err != nil {
		realtimeCleanup()
		return nil, func() {}, err
	}
	responsibilityReconcilePort := newProductionSchedulerResponsibilityPort(responsibilityCoordinator, responsibilityServices.repository, profiles, rt.Config.Sweeper.BatchSize)
	approvalWiring.AttachSchedulerPort(responsibilityReconcilePort)
	schedulerService, schedulerDispatcher, err := newProductionSchedulerRuntime(
		rt, schedulerRepository, discussionSessions, memoryDistill, memoryAdmin, productionSchedulerToolCallPort{gateway: mcpGatewayRef},
		todoService, responsibilityReconcilePort, responsibilityReconcilePort, ontologyStateRefresher, schedulerapp.NewAgentProfileCreatorPermissionResolver(profiles), profiles, canonicalMissionCommands,
	)
	if err != nil {
		realtimeCleanup()
		return nil, func() {}, err
	}
	if !opts.DisableBackgroundWorkers {
		if err := configureProductionResponsibilityRuntime(ctx, rt, profiles, responsibilityServices.repository, schedulerRepository, schedulerService, responsibilityReconcilePort); err != nil {
			realtimeCleanup()
			return nil, func() {}, err
		}
	}
	todoOntologyService, err := newProductionTodoOntologyRuntime(rt, todoService, &api, opts.DisableBackgroundWorkers)
	if err != nil {
		realtimeCleanup()
		return nil, func() {}, err
	}
	gatewaySettings, websiteKnowledgeStore, websiteFAQCatalog, err := newProductionGatewayAndWebsiteKnowledgeRuntime(ctx, opts.DisableBackgroundWorkers, core, rt)
	if err != nil {
		realtimeCleanup()
		return nil, func() {}, err
	}
	enterpriseGraphRuntime, err := newProductionEnterpriseGraphRuntimeForServer(core, rt, ontologySecurity, authorizationService, knowledgeRepo, memoryStore)
	if err != nil {
		realtimeCleanup()
		return nil, func() {}, err
	}
	mcpGateway := newProductionMCPGateway(productionMCPGatewayOptions{
		Registry:                 toolRegistry,
		SchedulerExecutor:        newProductionSchedulerToolExecutor(schedulerService, schedulerDispatcher),
		TodoExecutor:             todoapp.NewToolExecutor(todoapp.ToolExecutorOptions{Service: todoOntologyService}),
		BusinessApprovalExecutor: hil.NewBusinessApprovalToolExecutor(approvalWiring.hilService, rt.Clock.Now),
		EvaluationExecutor: productionEvaluationToolExecutor{executor: operations.NewEvaluationToolExecutor(operations.EvaluationToolExecutorOptions{
			Store: evals,
			Now:   rt.Clock.Now,
		})},
		MemorySearchExecutor:   productionMemorySearchToolExecutor{provider: discussionMemoryProvider},
		SessionHistoryExecutor: discussionsession.NewService(discussionsession.ServiceOptions{Repository: discussionRepository}),
		RSILearningExecutor:    learningAdvice,
		MemoryDiaryExecutor: productionMemoryDiaryToolExecutor{executor: memorydiaryapp.NewToolExecutor(memorydiaryapp.ToolExecutorOptions{
			Repository: memoryStore,
			Enabled:    settingsStore.MemoryEnabled,
			State:      diaryState,
			Audit:      memoryAuditRecorder,
			Metadata:   memoryMetadataSink,
			Now:        rt.Clock.Now,
		})},
		KnowledgeExecutor:              productionKnowledgeToolExecutor{profiles: profiles, accounts: profileAuthStore, settings: settingsStore, search: knowledgeSearch, baseFinder: knowledgeRepo, ensureDefault: knowledgeCommands, upload: knowledgeCommands},
		EmailExecutor:                  newProductionEmailToolExecutor(gatewaySettings, profiles, emailchannel.SMTPSender{}),
		PersonalAssistantShellExecutor: productionPersonalAssistantShellExecutor{},
		PersonalBrowserExecutor:        productionPersonalBrowserExecutor{broker: deviceBroker, profiles: profiles, now: rt.Clock.Now},
		EnterpriseGraphExecutor:        enterpriseGraphRuntime,
		OntologyExecutor:               productionOntologyStewardToolExecutor{governance: ontologyGovernanceCommands, inference: ontologyAggregateAPIs.Inference},
		SkillExecutor:                  skillRuntimeTools,
		WebsiteKnowledgeExecutor:       newProductionWebsiteKnowledgeCuration(websiteKnowledgeStore, websiteFAQCatalog, rt.Clock.Now),
		AuditStore:                     audit,
		ExternalMCP:                    mcpRuntime,
		Profiles:                       profiles,
		Lifecycle:                      newProductionMCPActionLifecycle(ontologyActionAPI.service, rt.IDGenerator),
		ResponsibilityAuthority:        productionResponsibilityExecutionAuthority{jobs: rt.ExecutionQueue, sessions: sharedDiscussionRuntimeRegistry},
		Now:                            rt.Clock.Now,
	})
	mcpGatewayRef.gateway, api.mcpGateway = mcpGateway, mcpGateway
	resourceCommand.SetMCPRuntime(mcpRuntime)
	if err := registerProductionSweepers(rt, core, toolRegistry, mcpRuntime, schedulerRepository, schedulerService, schedulerDispatcher, responsibilityServices.repository, repairExecutionReconciler, ontologyProjectionRunner, ontologySecurity.Scopes, knowledgeRepo, knowledgeProviderRegistry, memoryPageIndex, memoryStore, settingsStore, platformSecretResolver{provider: rt.Secrets}, materializedMemoryMetadata, discussionRepository, baseMissionRepo, ontologyAggregateAPIs.Inference, profiles); err != nil {
		realtimeCleanup()
		return nil, func() {}, err
	}
	if err := registerProductionImprovementSweeper(rt, agentExecutionOntology); err != nil {
		realtimeCleanup()
		return nil, func() {}, err
	}
	if err := registerProductionRuntimeFeedbackRecovery(rt, core, runtimeFeedbackJournal); err != nil {
		realtimeCleanup()
		return nil, func() {}, err
	}
	notifications := productionPersonalAssistantNotificationWiring{
		runtime: rt, core: core, profiles: profiles, accounts: authStore, scopes: roleScopeResolver,
		todos: todoService, discussions: discussionSessions, runner: discussionAgent,
		reportRegistry: sharedDiscussionRuntimeRegistry, reportRecorder: discussionAgentDeps.TraceRecorder,
		hil: approvalWiring.hilService, workflow: approvalWiring.workflowRepository, settings: settingsStore,
		systemAccess: newProductionContextAccessPolicy(core, rt.DBRouter, ontologySecurity.Scopes, authorizationService, knowledgeRepo, memoryStore).personalAssistantSystemAccess,
	}
	notifications.setFailureCapture(clientDiagnostics)
	gatewayLifecycle, err := notifications.StartGateway(ctx, discussionSessions, profiles, gatewaySettings, audit, approvalWiring, opts.DisableBackgroundWorkers)
	if err != nil {
		realtimeCleanup()
		return nil, func() {}, err
	}
	gatewayChannels := gatewayLifecycle.manager
	scheduledDelivery.SetSender(gatewayChannels)
	taskNotifications, err := startProductionTaskNotifications(ctx, rt, core, schedulerRepository, profiles, gatewayLifecycle, schedulerService, opts.DisableBackgroundWorkers)
	if err != nil {
		gatewayLifecycle.Stop()
		realtimeCleanup()
		return nil, func() {}, err
	}
	websiteAdvisor, err := newProductionWebsiteAdvisor(rt, core, gatewayChannels.Gateway(), mcpGateway, websiteKnowledgeStore, websiteFAQCatalog)
	if err != nil {
		gatewayLifecycle.Stop()
		realtimeCleanup()
		return nil, func() {}, err
	}
	authRequireHTTPS, _ := rt.Config.Bool("auth.require_https")
	baseHandler := httptransport.NewRouter(withProductionOntologyReadAPIs(httptransport.RouterOptions{
		Readiness:                  runtimeReadinessProbe(rt, opts.DisableBackgroundWorkers),
		Environment:                rt.Config.Environment,
		LifecycleContext:           gatewayLifecycle.ctx,
		AuthRequireHTTPS:           authRequireHTTPS,
		TrustedProxyCIDRs:          rt.Config.Server.TrustedProxyCIDRs,
		TrustedViewerHeaders:       rt.Config.Server.TrustedViewerHeadersEnabled,
		InternalViewerAuth:         productionAuthorizationFromEnv("ORRERY_INTERNAL_VIEWER_AUTHORIZATION", "ORRERY_INTERNAL_VIEWER_TOKEN"),
		RuntimeMCPAuth:             productionAuthorizationFromEnv("ORRERY_RUNTIME_MCP_AUTHORIZATION", "ORRERY_RUNTIME_MCP_TOKEN"),
		AgentTraces:                newProductionAgentTraceService(core, rt.FileStore),
		Auth:                       authService,
		BearerAuth:                 bearerAuth,
		IdentityAuth:               identityAuth,
		IdentityAdmin:              identityAdmin,
		ProfileIdentity:            profileIdentity,
		PersonalAssistant:          personalAssistant,
		PersonalAssistantSpeech:    newProductionPersonalAssistantSpeech(discussionSessions, settingsStore, platformSecretResolver{provider: rt.Secrets}),
		OIDCLoginConsent:           identityAuthCfg.OIDCLoginConsent,
		OIDCClientAdmin:            oidcClientAdmin,
		OAuth2LogoutReturnOrigins:  websiteAdvisorAllowedOrigins(),
		MarketingOAuth2CallbackURL: strings.TrimSpace(os.Getenv("ORRERY_MARKETING_OAUTH2_CALLBACK_URL")),
		Realtime:                   realtimeHandler,
		WebsiteAdvisor:             websiteAdvisor,
		WebsiteKnowledgeGovernance: websiteKnowledgeStore,
		WebsiteFAQCatalog:          websiteFAQCatalog,
		MissionQuery:               api,
		MissionCommand:             api,
		MissionStream:              api,
		ProjectCommand:             api,
		DevelopmentProjects:        developmentResources.projects,
		SCMConnections:             developmentResources.connections,
		OntologyScopes:             ontologyScopeAPI,
		OntologyActions:            ontologyActionAPI,
		OntologyReliability:        ontologyReliabilityAPI,
		OntologyProblems:           ontologyProblemAPI,
		OntologyGovernanceCommand:  ontologyGovernanceCommands,
		OntologyProjectionCommand:  ontologyProjectionCommands,
		AuditRecorder:              audit,
		Approvals:                  api,
		HIL:                        approvalWiring.hilService,
		MarketingWechatApproval:    httptransport.MarketingWechatApprovalRoute{Service: hil.NewMarketingWechatApprovalService(approvalWiring.hilService, approvalWiring.workflowRepository, rt.Clock.Now), Authorization: marketingApprovalAuthorization},
		ResourceQuery:              api,
		ResourceCommand:            api,
		MemoryQuery:                memoryAdmin,
		MemoryCommand:              memoryAdmin,
		MemoryDistill:              memoryDistill,
		Operations:                 api,
		Responsibilities:           responsibilityServices.operationsService(profiles, missionCommand, rt.Clock),
		WorkforceAvatars:           httptransport.NewLocalWorkforceAvatarStore(filepath.Join(rt.Config.BaseDir, "data", "avatars")),
		KnowledgeQuery:             knowledgeQueries,
		KnowledgeSearch:            knowledgeSearch,
		KnowledgeCommand:           knowledgeCommands,
		KnowledgeProviders:         knowledgeProviders,
		KnowledgeWorkspace:         productionKnowledgeWorkspace{query: knowledgeQueries, repo: knowledgeRepo, registry: knowledgeProviderRegistry, bridge: knowledgeontology.NewPlatformStore(core)},
		KnowledgeProfiles:          profiles,
		Discussions:                discussionSessions,
		Scheduler:                  schedulerService,
		TaskNotifications:          taskNotifications,
		SchedulerDispatcher:        schedulerDispatcher,
		Todo:                       todoOntologyService,
		TodoAutomation:             todoService,
		MCPGateway:                 mcpGateway,
		GatewaySettings:            gatewaySettings,
		GatewaySettingsChanged:     gatewayChannels.ReloadGatewaySettings,
		GatewayBindingsChanged:     gatewayChannels.ReloadWeixinSettings,
		GatewayChannelBindings:     gatewayChannels.BindingStore(),
		GatewayDeliveries:          gatewayLifecycle.deliveries,
		GatewayAccounts:            gatewayChannels,
		AgentProfiles:              profiles,
		Settings:                   productionSettingsStore{router: rt.DBRouter, config: rt.Config},
		SettingsSecrets:            platformSecretResolver{provider: rt.Secrets},
		Authorization:              authorizationService,
		RoleAuthorization:          rolePermissionAuthorizer,
		AuthorizationDiagnostics:   api,
		ToolResultPolicy:           ontologySecurity.PolicyStore,
		Logger:                     httpRequestLogger{logger: rt.Logger},
		ClientDiagnostics:          clientDiagnostics,
	}, ontologyAggregateAPIs))
	developmentCleanup := productionDevelopmentResourceCleanup(rt, developmentResources, realtimeCleanup)
	cleanup := productionHandlerCleanup(rt, gatewayLifecycle, developmentCleanup, sharedDiscussionRuntimeRegistry)
	peopleHandler, err := newProductionPeopleIngestionHandler(ctx, rt, core, profiles, ontologyProjectionRunner, baseHandler)
	if err != nil {
		cleanup()
		return nil, func() {}, err
	}
	handler, err := newProductionDeviceDeliveryHandler(ctx, rt, authService, audit, deviceBroker, personalAssistant, peopleHandler)
	if err != nil {
		cleanup()
		return nil, func() {}, err
	}
	return handler, cleanup, nil
}
