package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"

	agentassembly "orrery_v2/internal/agent/assembly"
	agentcore "orrery_v2/internal/agent/core"
	agentlifecycle "orrery_v2/internal/agent/lifecycle"
	"orrery_v2/internal/agent/runtime/agentkernel"
	runtimecore "orrery_v2/internal/agent/runtime/core"
	llmprofile "orrery_v2/internal/agent/runtime/llmprofile"
	deviceprovider "orrery_v2/internal/agent/runtime/providers/device"
	runtimesession "orrery_v2/internal/agent/runtime/session"
	"orrery_v2/internal/app/actioncontract"
	"orrery_v2/internal/app/agenttrace"
	"orrery_v2/internal/app/discussionsession"
	"orrery_v2/internal/app/improvement"
	"orrery_v2/internal/app/knowledgeadmin"
	missioncontrol "orrery_v2/internal/app/missioncontrol"
	knowledgeprovider "orrery_v2/internal/knowledge/provider"
	memcapture "orrery_v2/internal/memory/capture"
	memretrieval "orrery_v2/internal/memory/retrieval"
	missioncore "orrery_v2/internal/mission/core"
	missionevent "orrery_v2/internal/mission/event"
	missiontask "orrery_v2/internal/mission/task"
	"orrery_v2/internal/platform/config"
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/eventstore"
	"orrery_v2/internal/platform/filestore"
	"orrery_v2/internal/platform/logging"
	"orrery_v2/internal/platform/uow"
	skillres "orrery_v2/internal/resource/skill"
	toolres "orrery_v2/internal/resource/tool"
)

func newConfiguredDiscussionAgentRunner(ctx context.Context, cfg config.Config, profiles *agentcore.PlatformProfileRepository, memory memretrieval.Repository, tools toolres.Registry, skills skillres.Registry, deps productionDiscussionAgentDependencies) (discussionsession.DiscussionAgentRunner, error) {
	agentID := configuredDiscussionAgentID(cfg)
	runOptions := configuredConversationAgentRuntimeOptions(cfg, agentID)
	spec := runOptions.RuntimeSpec
	var profile agentcore.AgentProfile
	hasProfile := false
	if profiles != nil {
		if loaded, err := profiles.GetProfile(ctx, agentcore.AgentProfileID(agentID)); err == nil && !loaded.ID.IsZero() {
			profile = loaded
			hasProfile = true
		}
	}
	hasLLMProfiles := deps.LLMProfiles != nil
	if !spec.HasKind() {
		if !hasProfile {
			return nil, fmt.Errorf("%w: discussion agent profile %q is required when runtime kind is not configured", discussionsession.ErrRuntimeUnavailable, agentID)
		}
		if !profile.RuntimeSpec.HasKind() {
			if !systemManagedDiscussionProfile(profile) {
				return nil, fmt.Errorf("%w: discussion agent profile %q has no runtime spec", discussionsession.ErrRuntimeUnavailable, agentID)
			}
			spec = defaultAgentRuntimeSpec(cfg, agentID)
			if spec.Kind == agentcore.RuntimeKindManaged && !hasLLMProfiles {
				return nil, fmt.Errorf("%w: discussion agent profile %q requires llm profile settings", discussionsession.ErrRuntimeUnavailable, agentID)
			}
		} else {
			spec = profile.RuntimeSpec
		}
	}
	if !spec.Kind.IsValid() {
		return nil, fmt.Errorf("discussion agent runtime kind %q is not valid", spec.Kind)
	}
	if !hasProfile {
		return nil, fmt.Errorf("discussion agent profile %q is required", agentID)
	}
	instanceID, err := discussionAgentInstanceID(ctx, profiles, profile.ID)
	if err != nil {
		return nil, err
	}
	profile.RuntimeSpec = spec
	systemPrompt := strings.TrimSpace(runOptions.SystemPrompt)
	workingDirectory, err := productionRuntimeWorkingDirectory(cfg.BaseDir, runOptions.WorkingDirectory)
	if err != nil {
		return nil, err
	}
	deps = withProductionDiscussionAgentDefaults(deps)
	assembler, err := newProductionDiscussionAgentAssembler(memory, tools, skills, deps)
	if err != nil {
		return nil, err
	}
	runner, err := discussionsession.NewAssembledDiscussionAgentRunner(ctx, discussionsession.AssembledDiscussionAgentRunnerOptions{
		Assembler:             assembler,
		Profile:               profile,
		AgentID:               agentID,
		InstanceID:            instanceID,
		SystemPrompt:          systemPrompt,
		PromptBlockProvider:   deps.PromptBlocks,
		ActionInterpreter:     deps.ActionSemantics,
		AttachmentProcessor:   discussionsession.NewRuntimeAttachmentProcessor(newProductionSpeechProvider(deps.LLMProfiles, deps.Secrets)),
		Model:                 strings.TrimSpace(runOptions.Model),
		WorkingDirectory:      workingDirectory,
		WorkspaceRootProvider: deps.WorkspaceRoot,
		Timeout:               runOptions.Timeout,
	})
	if err != nil {
		return nil, err
	}
	if deps.PersonalAssistant == nil {
		return runner, nil
	}
	deviceProfile := profile
	deviceProfile.RuntimeSpec = agentcore.RuntimeSpec{Kind: agentcore.RuntimeKindDevice}
	deviceRunner, err := discussionsession.NewAssembledDiscussionAgentRunner(ctx, discussionsession.AssembledDiscussionAgentRunnerOptions{
		Assembler:             assembler,
		Profile:               deviceProfile,
		AgentID:               agentID,
		InstanceID:            instanceID,
		SystemPrompt:          systemPrompt,
		PromptBlockProvider:   deps.PromptBlocks,
		ActionInterpreter:     deps.ActionSemantics,
		WorkingDirectory:      workingDirectory,
		WorkspaceRootProvider: deps.WorkspaceRoot,
		Timeout:               runOptions.Timeout,
	})
	if err != nil {
		return nil, err
	}
	resolver := &productionPersonalAssistantBackingRunnerFactory{
		config: cfg, profiles: profiles, memory: memory, tools: tools, skills: skills, deps: deps,
	}
	return newProductionPersonalAssistantDiscussionAgentRunner(runner, deviceRunner, deps.PersonalAssistant, resolver), nil
}

type productionPersonalAssistantBackingRunnerFactory struct {
	config   config.Config
	profiles *agentcore.PlatformProfileRepository
	memory   memretrieval.Repository
	tools    toolres.Registry
	skills   skillres.Registry
	deps     productionDiscussionAgentDependencies
	cache    sync.Map
}

func (f *productionPersonalAssistantBackingRunnerFactory) ResolvePersonalAssistantRunner(ctx context.Context, tenantID string, agentID string, target string) (discussionsession.DiscussionAgentRunner, error) {
	if f == nil || f.profiles == nil {
		return nil, fmt.Errorf("%w: agent profile directory unavailable", discussionsession.ErrRuntimeUnavailable)
	}
	agentID = strings.TrimSpace(agentID)
	target = strings.ToLower(strings.TrimSpace(target))
	if agentID == "" || (target != personalAssistantRouteManaged && target != personalAssistantRouteDevice) {
		return nil, discussionsession.ErrInvalidRequest
	}
	tenantID = strings.TrimSpace(tenantID)
	profile, err := f.profiles.GetProfile(ctx, agentcore.AgentProfileID(agentID))
	if err != nil {
		return nil, err
	}
	if profile.ID.IsZero() || !profile.Enabled || profile.AgentKind != agentcore.AgentKindAI || !profile.IsPersonalAssistant() || !profile.Placement.IsInternal(profile.OrgIdentity) {
		return nil, fmt.Errorf("%w: personal assistant AgentProfile %s is not available", discussionsession.ErrRuntimeUnavailable, agentID)
	}
	if tenantID != "" && !strings.EqualFold(strings.TrimSpace(profile.PersonalAssistantTenantID), tenantID) {
		return nil, fmt.Errorf("%w: personal assistant AgentProfile %s is outside tenant %s", discussionsession.ErrRuntimeUnavailable, agentID, tenantID)
	}
	instanceID, err := discussionAgentInstanceID(ctx, f.profiles, profile.ID)
	if err != nil {
		return nil, err
	}
	runOptions := configuredConversationAgentRuntimeOptions(f.config, agentID)
	spec := profile.RuntimeSpec
	if target == personalAssistantRouteDevice {
		spec = agentcore.RuntimeSpec{Kind: agentcore.RuntimeKindDevice}
	} else if !spec.HasKind() {
		spec = runOptions.RuntimeSpec
		if !spec.HasKind() && systemManagedDiscussionProfile(profile) {
			spec = defaultAgentRuntimeSpec(f.config, agentID)
		}
	}
	if !spec.Kind.IsValid() {
		return nil, fmt.Errorf("%w: personal assistant AgentProfile %s runtime kind %q is invalid", discussionsession.ErrRuntimeUnavailable, agentID, spec.Kind)
	}
	profile.RuntimeSpec = spec
	profileVersion, err := json.Marshal(profile)
	if err != nil {
		return nil, fmt.Errorf("encode personal assistant AgentProfile %s: %w", agentID, err)
	}
	cacheKey := fmt.Sprintf("%s\x00%s\x00%s\x00%x", tenantID, agentID, target, sha256.Sum256(profileVersion))
	if cached, ok := f.cache.Load(cacheKey); ok {
		if runner, ok := cached.(discussionsession.DiscussionAgentRunner); ok {
			return runner, nil
		}
	}
	workingDirectory, err := productionRuntimeWorkingDirectory(f.config.BaseDir, runOptions.WorkingDirectory)
	if err != nil {
		return nil, err
	}
	assembler, err := newProductionDiscussionAgentAssembler(f.memory, f.tools, f.skills, f.deps)
	if err != nil {
		return nil, err
	}
	options := discussionsession.AssembledDiscussionAgentRunnerOptions{
		Assembler:             assembler,
		Profile:               profile,
		AgentID:               agentID,
		InstanceID:            instanceID,
		SystemPrompt:          profile.BaseAgent.SystemPrompt,
		PromptBlockProvider:   f.deps.PromptBlocks,
		ActionInterpreter:     f.deps.ActionSemantics,
		AttachmentProcessor:   discussionsession.NewRuntimeAttachmentProcessor(newProductionSpeechProvider(f.deps.LLMProfiles, f.deps.Secrets)),
		WorkingDirectory:      workingDirectory,
		WorkspaceRootProvider: f.deps.WorkspaceRoot,
		Timeout:               runOptions.Timeout,
	}
	if target == personalAssistantRouteManaged {
		options.Model = strings.TrimSpace(runOptions.Model)
	}
	runner, err := discussionsession.NewAssembledDiscussionAgentRunner(ctx, options)
	if err != nil {
		return nil, err
	}
	actual, _ := f.cache.LoadOrStore(cacheKey, discussionsession.DiscussionAgentRunner(runner))
	return actual.(discussionsession.DiscussionAgentRunner), nil
}

func (f *productionPersonalAssistantBackingRunnerFactory) DeleteDiscussionRuntimeRecord(ctx context.Context, runID string) error {
	if f == nil {
		return nil
	}
	var cleanup []error
	f.cache.Range(func(_, value any) bool {
		if cleaner, ok := value.(discussionsession.DiscussionRuntimeRecordCleaner); ok {
			cleanup = append(cleanup, cleaner.DeleteDiscussionRuntimeRecord(ctx, runID))
		}
		return true
	})
	return errors.Join(cleanup...)
}

func (f *productionPersonalAssistantBackingRunnerFactory) ReleaseRuntimeWorkspace(ctx context.Context, workspaceRoot string) error {
	if f == nil {
		return nil
	}
	var cleanup []error
	f.cache.Range(func(_, value any) bool {
		if releaser, ok := value.(discussionsession.DiscussionRuntimeWorkspaceReleaser); ok {
			cleanup = append(cleanup, releaser.ReleaseRuntimeWorkspace(ctx, workspaceRoot))
		}
		return true
	})
	return errors.Join(cleanup...)
}

func productionRuntimeWorkingDirectory(baseDir, configured string) (string, error) {
	baseDir = strings.TrimSpace(baseDir)
	workingDirectory := strings.TrimSpace(configured)
	if workingDirectory == "" {
		workingDirectory = baseDir
	} else if !filepath.IsAbs(workingDirectory) && baseDir != "" {
		workingDirectory = filepath.Join(baseDir, workingDirectory)
	}
	if workingDirectory == "" {
		return "", fmt.Errorf("production runtime working directory is required")
	}
	absolute, err := filepath.Abs(workingDirectory)
	if err != nil {
		return "", fmt.Errorf("resolve production runtime working directory: %w", err)
	}
	return filepath.Clean(absolute), nil
}

func discussionAgentInstanceID(ctx context.Context, profiles *agentcore.PlatformProfileRepository, profileID agentcore.AgentProfileID) (agentcore.AgentInstanceID, error) {
	if profiles == nil {
		return "", fmt.Errorf("%w: agent profile directory unavailable", discussionsession.ErrRuntimeUnavailable)
	}
	availability, err := profiles.GetAvailability(ctx, profileID)
	if err != nil {
		return "", err
	}
	if availability.AgentInstanceID.IsZero() {
		return "", fmt.Errorf("%w: discussion agent %s has no bound runtime instance", discussionsession.ErrRuntimeUnavailable, profileID)
	}
	return availability.AgentInstanceID, nil
}

func systemManagedDiscussionProfile(profile agentcore.AgentProfile) bool {
	return profile.BaseAgent.InheritsBaseAgent &&
		profile.BaseAgent.AuditRequired &&
		strings.TrimSpace(profile.BaseAgent.SystemPrompt) != "" &&
		strings.TrimSpace(profile.BaseAgent.LLMAPIKeyRef) != ""
}

type productionDiscussionAgentDependencies struct {
	Config            config.Config
	Committer         agentcore.ResultCommitter
	SignalSink        agentcore.SignalSink
	TraceRecorder     agentcore.TraceRecorder
	Notifier          agentcore.Notifier
	LLMProfiles       llmprofile.ProfileProvider
	RuntimeProfiles   agentassembly.RuntimeSpecResolver
	WorkspaceRoot     discussionsession.WorkspaceRootProvider
	PromptBlocks      productionPromptBlockProvider
	ActionSemantics   actioncontract.ActionSemanticInterpreter
	MemorySettings    productionMemorySettingsProvider
	Secrets           llmprofile.SecretResolver
	ToolDispatcher    runtimecore.RuntimeToolDispatcher
	Normalizer        agentcore.ResultNormalizer
	MemoryRepository  memcapture.Repository
	MemoryAudit       memcapture.AuditRecorder
	MemoryMetadata    memcapture.RecordMetadataSink
	ApprovalBroker    runtimecore.ApprovalBroker
	RuntimeRegistry   *runtimesession.ConnectionRegistry
	KernelJournal     agentkernel.ToolJournal
	KernelCheckpoints agentkernel.CheckpointStore
	DeviceBroker      deviceprovider.DeviceWorkOrderBroker
	PersonalAssistant productionPersonalAssistantDirectory
	DiaryFlush        agentcore.DiaryFlushCoordinator
}

func (d productionDiscussionAgentDependencies) IsZero() bool {
	return d.Config.Environment == "" && d.Config.Profile == "" && d.Committer == nil && d.SignalSink == nil && d.TraceRecorder == nil && d.LLMProfiles == nil && d.RuntimeProfiles == nil && d.WorkspaceRoot == nil && d.PromptBlocks == nil && d.ActionSemantics == nil && d.MemorySettings == nil && d.Secrets == nil && d.ToolDispatcher == nil && d.Normalizer == nil && d.MemoryRepository == nil && d.MemoryAudit == nil && d.MemoryMetadata == nil && d.ApprovalBroker == nil && d.RuntimeRegistry == nil && d.KernelJournal == nil && d.KernelCheckpoints == nil && d.DeviceBroker == nil && d.PersonalAssistant == nil && d.DiaryFlush == nil
}

type productionPromptBlockProvider interface {
	PromptBlocks(ctx context.Context) ([]agentcore.RuntimePromptBlock, error)
}

type productionMemorySettingsProvider interface {
	MemoryEnabled(ctx context.Context) (bool, error)
}

func newProductionDiscussionAgentDependencies(bus agentlifecycle.SignalBus, traceStore agentcore.TraceStore, commands discussionAgentMissionCommandPort) productionDiscussionAgentDependencies {
	if bus == nil {
		bus = agentlifecycle.NewInMemorySignalBus(agentlifecycle.SignalBusOptions{})
	}
	deps := productionDiscussionAgentDependencies{
		Committer:  &discussionAgentConversationResultCommitter{commands: commands},
		SignalSink: agentlifecycle.NewCoreSignalSink(bus),
		Notifier:   &productionAgentResultNotifier{},
	}
	if traceStore != nil {
		recorder := agentcore.StoreTraceRecorder{Store: traceStore}
		if provider, ok := traceStore.(interface {
			TraceIndexSink() agentcore.TraceIndexSink
		}); ok {
			recorder.IndexSink = provider.TraceIndexSink()
		}
		deps.TraceRecorder = productionRuntimeEvidenceRecorder(recorder, traceStore)
	}
	return deps
}

func newProductionTraceIndexSink(store *db.Store) agentcore.TraceIndexSink {
	if store == nil {
		return nil
	}
	return agenttrace.NewPlatformStore(store)
}

func withProductionDiscussionAgentDefaults(deps productionDiscussionAgentDependencies) productionDiscussionAgentDependencies {
	defaults := newProductionDiscussionAgentDependencies(nil, nil, nil)
	if deps.Committer == nil {
		deps.Committer = defaults.Committer
	}
	if deps.SignalSink == nil {
		deps.SignalSink = defaults.SignalSink
	}
	if deps.TraceRecorder == nil {
		deps.TraceRecorder = defaults.TraceRecorder
	}
	if deps.Notifier == nil {
		deps.Notifier = defaults.Notifier
	}
	if deps.Normalizer == nil {
		deps.Normalizer = discussionsession.DiscussionAgentResultNormalizer{}
	}
	return deps
}

func productionMemoryCaptureCommitter(deps productionDiscussionAgentDependencies) agentcore.ResultCommitter {
	delivery := deps.Committer
	if delivery == nil || deps.MemoryRepository == nil || deps.MemoryAudit == nil {
		return delivery
	}
	writer := memcapture.NewEpisodicWriter(memcapture.EpisodicWriterOptions{
		Repository: deps.MemoryRepository,
		Audit:      deps.MemoryAudit,
		Metadata:   deps.MemoryMetadata,
		Policy:     memcapture.DefaultCapturePolicy{},
	})
	if deps.MemorySettings != nil {
		return memcapture.NewAgentResultCaptureCommitterWithEnabled(delivery, writer, deps.MemorySettings.MemoryEnabled)
	}
	return memcapture.NewAgentResultCaptureCommitter(delivery, writer)
}

func subscribeMissionLifecycleProjection(bus agentlifecycle.SignalBus, sink missionevent.Sink, reader missionevent.TenantReader) error {
	if bus == nil || sink == nil || reader == nil {
		return fmt.Errorf("mission lifecycle projection requires signal bus, event sink, and event reader")
	}
	handler := missionLifecycleProjectionHandler{
		mapper: missionevent.NewSignalMapper(),
		sink:   sink,
		reader: reader,
	}
	for _, name := range agentlifecycle.AllSignalNames() {
		if _, err := bus.Subscribe(name, handler, agentlifecycle.HandlerOptions{
			FailurePolicy:     agentlifecycle.FailurePolicyContinue,
			DegradeCapability: "mission_lifecycle_projection",
		}); err != nil {
			return err
		}
	}
	return nil
}

type missionLifecycleProjectionHandler struct {
	mapper missionevent.SignalMapper
	sink   missionevent.Sink
	reader missionevent.TenantReader
}

func (h missionLifecycleProjectionHandler) Name() string {
	return "mission.lifecycle.projection"
}

func (h missionLifecycleProjectionHandler) HandleSignal(ctx context.Context, signal agentlifecycle.SignalEnvelope) error {
	if signal.Name == agentlifecycle.AfterResultCommit {
		return nil
	}
	if workerOwnsTerminalMissionLifecycle(signal) {
		return nil
	}
	events, err := h.mapper.Map(signal)
	if err != nil {
		return err
	}
	for _, event := range events {
		if err := h.appendMissionEvent(ctx, event); err != nil {
			return err
		}
	}
	return nil
}

func workerOwnsTerminalMissionLifecycle(signal agentlifecycle.SignalEnvelope) bool {
	switch signal.Name {
	case agentlifecycle.AgentBlocked, agentlifecycle.AgentFailed, agentlifecycle.AgentCancelled, agentlifecycle.AgentTimeout:
		return strings.TrimSpace(signal.Metadata[agentcore.MissionLifecycleAuthorityProviderKey]) == agentcore.MissionLifecycleAuthorityRunWorker
	default:
		return false
	}
}

func (h missionLifecycleProjectionHandler) appendMissionEvent(ctx context.Context, event missionevent.Envelope) error {
	if h.sink == nil || h.reader == nil {
		return fmt.Errorf("mission lifecycle projection event dependencies are unavailable")
	}
	for attempt := 0; attempt < 32; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		persisted, err := h.reader.LoadMissionForTenant(ctx, event.Tenant(), event.MissionID)
		if err != nil {
			return err
		}
		nextVersion := 1
		for _, item := range persisted {
			if item.EventID == event.EventID || (event.IdempotencyKey != "" && item.IdempotencyKey == event.IdempotencyKey) {
				return nil
			}
			if item.StreamVersion >= nextVersion {
				nextVersion = item.StreamVersion + 1
			}
		}
		event.StreamID = missionevent.MissionStreamID(event.Tenant(), event.MissionID)
		event.StreamVersion = nextVersion
		if err := h.sink.Append(ctx, event); err != nil {
			if errors.Is(err, eventstore.ErrConflict) {
				continue
			}
			return err
		}
		return nil
	}
	return eventstore.ErrConflict
}

func newProductionDiscussionAgentAssembler(memory memretrieval.Repository, tools toolres.Registry, skills skillres.Registry, deps productionDiscussionAgentDependencies) (agentassembly.AgentAssembler, error) {
	deps = withProductionDiscussionAgentDefaults(deps)
	delivery := productionMemoryCaptureCommitter(deps)
	components, err := agentassembly.NewProductionComponentRegistry(agentassembly.ProductionComponentRegistry{
		DeliveryCommitterValue: delivery,
		EvidenceValidator:      productionEvidenceResultValidator{},
		QAValidator:            productionQAResultValidator{},
		SummaryGeneratorValue:  agentcore.DefaultSummaryGenerator{},
		ReplyNotifier:          deps.Notifier,
		PolicyValidator:        productionPolicyResultValidator{},
		SignalSinkValue:        deps.SignalSink,
		TraceRecorderValue:     deps.TraceRecorder,
		NormalizerValue:        deps.Normalizer,
	})
	if err != nil {
		return nil, err
	}
	memoryBinder := agentassembly.MemoryBinder(disabledProductionMemoryBinder{})
	if memory != nil {
		memoryBinder = productionMemoryBinder{inner: memretrieval.NewBinder(memretrieval.NewRetriever(
			memory,
			memretrieval.NewCompressor(memretrieval.CompressionOptions{}),
		))}
	}
	return agentassembly.NewAssembler(agentassembly.AssemblerOptions{
		ToolResolver:      toolres.NewResolver(tools, toolres.ResolverOptions{}),
		SkillResolver:     skillres.NewResolver(skills, skillres.ResolverOptions{}),
		MemoryBinder:      memoryBinder,
		RuntimeRegistry:   newProductionRuntimeRegistry(deps),
		RuntimeResolver:   deps.RuntimeProfiles,
		ComponentRegistry: components,
		Pipeline: agentassembly.ComponentPipelineProvider{
			Registry:   components,
			Signals:    deps.SignalSink,
			Trace:      deps.TraceRecorder,
			Normalizer: deps.Normalizer,
		},
		DiaryFlush: deps.DiaryFlush,
	}), nil
}

type discussionSessionLogger struct {
	logger logging.Logger
}

func (l discussionSessionLogger) Info(ctx context.Context, msg string, fields ...any) {
	if l.logger != nil {
		l.logger.Info(ctx, msg, managedRuntimeLogFields(fields...)...)
	}
}

func (l discussionSessionLogger) Debug(ctx context.Context, msg string, fields ...any) {
	if l.logger != nil {
		l.logger.Debug(ctx, msg, managedRuntimeLogFields(fields...)...)
	}
}

func (l discussionSessionLogger) Warn(ctx context.Context, msg string, fields ...any) {
	if l.logger != nil {
		l.logger.Warn(ctx, msg, managedRuntimeLogFields(fields...)...)
	}
}

func managedRuntimeLogFields(fields ...any) []logging.Field {
	out := make([]logging.Field, 0, len(fields)/2)
	for i := 0; i+1 < len(fields); i += 2 {
		key, ok := fields[i].(string)
		if !ok || strings.TrimSpace(key) == "" {
			continue
		}
		out = append(out, logging.Any(key, fields[i+1]))
	}
	return out
}

type discussionAgentMissionCommandPort interface {
	SubmitAgentResult(ctx context.Context, viewer missioncontrol.ViewerRef, cmd missioncontrol.SubmitResultCommand) (missioncontrol.CommandReceiptDTO, error)
}

type agentRunTaskDeliveryPort interface {
	AcceptTask(ctx context.Context, taskID missiontask.TaskID, agent agentcore.AgentInstanceRef) error
	StartTask(ctx context.Context, taskID missiontask.TaskID, agent agentcore.AgentInstanceRef) error
	SubmitAgentResult(ctx context.Context, taskID missiontask.TaskID, result agentcore.AgentResult, opts missiontask.SubmitOptions) (missiontask.TaskSubmission, error)
}

type missionProjectionRefresher interface {
	refreshMissionProjection(ctx context.Context, tenantID string, missionID missioncore.MissionID) error
}

type discussionAgentConversationResultCommitter struct {
	commands discussionAgentMissionCommandPort
}

type missionTaskAgentResultCommitter struct {
	delivery agentRunTaskDeliveryPort
	tasks    interface {
		GetTask(ctx context.Context, tx uow.Tx, id missiontask.TaskID) (missiontask.TaskRecord, error)
	}
	unit      *uow.UnitOfWork
	refresher missionProjectionRefresher
}

func (c *discussionAgentConversationResultCommitter) SubmitAgentResult(ctx context.Context, result agentcore.AgentResult, opts agentcore.CommitOptions) error {
	if c == nil {
		return fmt.Errorf("discussion result committer is not configured")
	}
	if shouldSkipDiscussionResultCommit(result, opts) {
		if strings.TrimSpace(result.InternalTraceRef) == "" {
			return fmt.Errorf("discussion result requires a persisted runtime trace before caller handoff")
		}
		return nil
	}
	if c.commands == nil {
		return fmt.Errorf("mission result command service is not configured")
	}
	_, err := c.commands.SubmitAgentResult(ctx, missioncontrol.ViewerRef{
		ViewerID:   result.AgentID.String(),
		ViewerKind: missioncontrol.ViewerKindAgent,
		TenantID:   opts.TenantID,
	}, missioncontrol.SubmitResultCommand{
		RequestID:      result.RunID.String(),
		IdempotencyKey: opts.IdempotencyKey,
		MissionID:      result.MissionID,
		TaskID:         result.TaskID,
		Result:         result,
	})
	return err
}

func shouldSkipDiscussionResultCommit(result agentcore.AgentResult, opts agentcore.CommitOptions) bool {
	if result.MissionID.IsZero() || result.TaskID.IsZero() {
		return true
	}
	if strings.HasPrefix(result.MissionID.String(), "discussion-") {
		return true
	}
	for _, ref := range opts.ContextSourceRefs {
		if ref.Kind == agentcore.ContextSourceKindUserInstruction && ref.Ref.UserInstructionRef != nil &&
			ref.Ref.UserInstructionRef.ID == agentcore.UserInstructionID(missioncontrol.MentionAgentRunSource) {
			return true
		}
	}
	return false
}

func (c missionTaskAgentResultCommitter) SubmitAgentResult(ctx context.Context, result agentcore.AgentResult, opts agentcore.CommitOptions) error {
	if c.delivery == nil {
		return fmt.Errorf("mission task delivery is not configured")
	}
	if result.MissionID.IsZero() || result.TaskID.IsZero() {
		return fmt.Errorf("mission task result requires mission_id and task_id")
	}
	agent := agentcore.AgentInstanceRef{ID: agentcore.AgentInstanceID(result.AgentID)}
	if agent.ID.IsZero() {
		return fmt.Errorf("mission task result requires agent_id")
	}
	taskID := missiontask.TaskID(result.TaskID)
	if opts.ApprovedContinuation {
		status, err := c.taskStatus(ctx, taskID)
		if err != nil {
			return err
		}
		if status == missiontask.TaskStatusApproved {
			return nil
		}
	}
	if err := c.advanceTaskForSubmission(ctx, taskID, agent); err != nil {
		return err
	}
	if _, err := c.delivery.SubmitAgentResult(ctx, taskID, result, missiontask.SubmitOptions{IdempotencyKey: opts.IdempotencyKey}); err != nil {
		return err
	}
	if c.refresher != nil {
		if err := c.refresher.refreshMissionProjection(ctx, opts.TenantID, result.MissionID); err != nil {
			return agentcore.NewPostCommitPipelineError("mission_projection_refresh", err)
		}
	}
	return nil
}

func (c missionTaskAgentResultCommitter) advanceTaskForSubmission(ctx context.Context, taskID missiontask.TaskID, agent agentcore.AgentInstanceRef) error {
	status, err := c.taskStatus(ctx, taskID)
	if err != nil {
		return err
	}
	switch status {
	case missiontask.TaskStatusAssigned:
		if err := c.delivery.AcceptTask(ctx, taskID, agent); err != nil {
			return err
		}
		return c.delivery.StartTask(ctx, taskID, agent)
	case missiontask.TaskStatusAccepted:
		return c.delivery.StartTask(ctx, taskID, agent)
	case missiontask.TaskStatusInProgress, missiontask.TaskStatusBlocked:
		return nil
	default:
		return fmt.Errorf("task must be assigned, accepted, in_progress, or blocked before runtime submission; current status is %s", status)
	}
}

func (c missionTaskAgentResultCommitter) taskStatus(ctx context.Context, taskID missiontask.TaskID) (missiontask.TaskStatus, error) {
	if c.tasks == nil || c.unit == nil {
		return "", fmt.Errorf("mission task status reader is not configured")
	}
	var record missiontask.TaskRecord
	err := c.unit.Do(ctx, func(ctx context.Context, tx uow.Tx) error {
		var getErr error
		record, getErr = c.tasks.GetTask(ctx, tx, taskID)
		return getErr
	})
	if err != nil {
		return "", err
	}
	return record.Status, nil
}

type productionTraceStore struct {
	store           filestore.Store
	indexSink       agentcore.TraceIndexSink
	evidenceCore    *db.Store
	evidenceJournal improvement.RuntimeFeedbackJournal
}

func (s productionTraceStore) PutTrace(ctx context.Context, key string, data []byte) (string, error) {
	if s.store == nil {
		return "", fmt.Errorf("trace store is not configured")
	}
	return s.store.Upload(ctx, key, data, "application/json", filepath.Base(key))
}

func (s productionTraceStore) TraceIndexSink() agentcore.TraceIndexSink {
	return s.indexSink
}

func (p *productionDiscussionKnowledgeProvider) DiscussionAgentKnowledgeContext(ctx context.Context, request discussionsession.DiscussionAgentRequest) (discussionsession.DiscussionAgentKnowledgeContext, error) {
	if p == nil || p.search == nil {
		return discussionsession.DiscussionAgentKnowledgeContext{}, nil
	}
	query := strings.TrimSpace(request.Content)
	if query == "" {
		query = strings.TrimSpace(request.TriggerMessage.Content)
	}
	if query == "" {
		return discussionsession.DiscussionAgentKnowledgeContext{}, nil
	}
	agentID := strings.TrimSpace(request.AgentID)
	if agentID == "" {
		agentID = defaultLeadAgentID
	}
	viewer, err := productionKnowledgeViewer(ctx, p.profiles, agentID, knowledgeadmin.ViewerRef{
		ViewerID:        firstNonBlank(agentID, request.Viewer.ViewerID),
		AgentIDs:        []string{agentID},
		PermissionScope: []string{knowledgeadmin.PermissionKnowledgeRead},
	})
	if err != nil {
		return discussionsession.DiscussionAgentKnowledgeContext{}, err
	}
	page, err := p.search.SearchKnowledge(ctx, viewer, knowledgeadmin.SearchKnowledgeCommand{
		Query:         query,
		Limit:         6,
		RetrievalMode: knowledgeprovider.RetrievalModeAuto,
	})
	if err != nil {
		return discussionsession.DiscussionAgentKnowledgeContext{}, err
	}
	return mapDiscussionKnowledgeContext(page.Results), nil
}
