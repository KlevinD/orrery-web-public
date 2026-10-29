package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	agentassembly "orrery_v2/internal/agent/assembly"
	agentcore "orrery_v2/internal/agent/core"
	agentlifecycle "orrery_v2/internal/agent/lifecycle"
	runtimecore "orrery_v2/internal/agent/runtime/core"
	runtimedaemon "orrery_v2/internal/agent/runtime/daemon"
	runtimesession "orrery_v2/internal/agent/runtime/session"
	"orrery_v2/internal/app/agenttrace"
	"orrery_v2/internal/app/developmentproject"
	"orrery_v2/internal/app/discussionsession"
	"orrery_v2/internal/app/improvement"
	"orrery_v2/internal/app/missioncontrol"
	schedulerapp "orrery_v2/internal/app/scheduler"
	"orrery_v2/internal/gateway/channel"
	memcapture "orrery_v2/internal/memory/capture"
	memretrieval "orrery_v2/internal/memory/retrieval"
	missioncore "orrery_v2/internal/mission/core"
	missionevent "orrery_v2/internal/mission/event"
	missiontask "orrery_v2/internal/mission/task"
	"orrery_v2/internal/platform"
	"orrery_v2/internal/platform/config"
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/eventstore"
	"orrery_v2/internal/platform/execution"
	"orrery_v2/internal/platform/filestore"
	"orrery_v2/internal/platform/logging"
	"orrery_v2/internal/platform/uow"
	skillres "orrery_v2/internal/resource/skill"
	toolres "orrery_v2/internal/resource/tool"
)

type productionAgentRunWorkerRegistration struct {
	Runtime         *platform.Runtime
	Core            *db.Store
	Profiles        *agentcore.PlatformProfileRepository
	Memory          memretrieval.Repository
	MemoryRepo      memcapture.Repository
	Tools           toolres.Registry
	Skills          skillres.Registry
	Lifecycle       agentlifecycle.SignalBus
	TraceStore      filestore.Store
	EvidenceJournal improvement.RuntimeFeedbackJournal
	Delivery        agentRunTaskDeliveryPort
	Tasks           interface {
		GetTask(ctx context.Context, tx uow.Tx, id missiontask.TaskID) (missiontask.TaskRecord, error)
		LatestSubmission(ctx context.Context, tx uow.Tx, taskID missiontask.TaskID) (missiontask.TaskSubmission, bool, error)
	}
	MissionCatalog              *missioncontrol.DBProjectionRepository
	MissionRepo                 productionMissionProjectionRepository
	MissionSink                 missionevent.Sink
	ExecutionOntology           runtimedaemon.RuntimeLifecyclePublisher
	MemoryAudit                 memcapture.AuditRecorder
	Settings                    productionSettingsStore
	Secrets                     platformSecretResolver
	Scheduler                   schedulerRunLifecycleTracker
	SchedulerTasks              schedulerTaskContractSource
	DiaryFlush                  agentcore.DiaryFlushCoordinator
	ToolDispatcher              runtimecore.RuntimeToolDispatcher
	ApprovalBroker              runtimecore.ApprovalBroker
	RuntimeRegistry             *runtimesession.ConnectionRegistry
	Responsibility              responsibilityRunLifecycleTracker
	Notifier                    agentcore.Notifier
	MissionWorkspaces           missionAgentRunWorkspacePreparer
	DevelopmentExecution        developmentAgentRunCoordinator
	ScheduledDelivery           channel.OutboundSender
	ScheduledDeliveryAuthorizer scheduledAgentDeliveryAuthorizer
}

type developmentAgentRunCoordinator interface {
	PrepareAgentRun(context.Context, string, string, string, string, string) (developmentproject.PreparedAgentRun, bool, error)
	CommitAgentRunResult(context.Context, string, string, string, string, string, string) (developmentproject.TodoCommitRecord, bool, error)
	FailAgentRun(context.Context, string, string, string, string, string, error) error
}

type missionAgentRunWorkspacePreparer interface {
	PrepareAgentRunWorkspace(context.Context, string, string) (discussionsession.PreparedAgentRunWorkspace, error)
}

type schedulerRunLifecycleTracker interface {
	Started(context.Context, string, int) error
	AttemptFailed(context.Context, string, int, bool, error) error
	CompleteAgentRun(context.Context, schedulerapp.ScheduledRun, schedulerapp.RunResult, string, time.Time) error
}

type schedulerBlockedRunLifecycleTracker interface {
	FailAgentRun(context.Context, schedulerapp.ScheduledRun, schedulerapp.RunResult, string, time.Time, error) error
}

type responsibilityRunLifecycleTracker interface {
	Completed(context.Context, string, string, string) error
	Failed(context.Context, string, string, string, runtimedaemon.RuntimeLifecycleKind, error) error
}

func registerProductionAgentRunWorker(opts productionAgentRunWorkerRegistration) error {
	if opts.Runtime == nil || opts.MissionCatalog == nil || opts.Tasks == nil || opts.Notifier == nil || opts.Runtime.OutboxPublisher == nil || opts.Scheduler == nil || opts.SchedulerTasks == nil {
		return fmt.Errorf("platform runtime is not configured")
	}
	refresher := productionAPI{
		missionRepo:  opts.MissionRepo,
		eventReader:  missionevent.NewStoreReader(eventstore.New(opts.Core)),
		runtimeTasks: productionRuntimeTaskProjectionReader{unit: opts.Runtime.UnitOfWork, tasks: opts.Tasks},
	}
	deps := productionAgentRunWorkerDependencies(opts)
	missionCommitter := missionTaskAgentResultCommitter{
		delivery:  opts.Delivery,
		tasks:     opts.Tasks,
		unit:      opts.Runtime.UnitOfWork,
		refresher: refresher,
	}
	deps.Committer = developmentAgentResultCommitter{next: missionCommitter, projects: opts.DevelopmentExecution}
	assembler, err := newProductionDiscussionAgentAssembler(opts.Memory, opts.Tools, opts.Skills, deps)
	if err != nil {
		return err
	}
	schedulerDeps := deps
	schedulerDeps.SignalSink = noopScheduledAgentSignalSink{}
	schedulerAssembler, err := newProductionDiscussionAgentAssembler(opts.Memory, opts.Tools, opts.Skills, schedulerDeps)
	if err != nil {
		return err
	}
	runtimeLifecycle := missioncontrol.NewRuntimeLifecyclePublisher(missioncontrol.RuntimeLifecyclePublisherOptions{
		Events:      opts.MissionSink,
		EventReader: missionevent.NewStoreReader(eventstore.New(opts.Core)),
		Repo:        opts.MissionRepo,
		IDGenerator: opts.Runtime.IDGenerator,
		Clock:       opts.Runtime.Clock,
	})
	combinedLifecycle := productionRuntimeLifecyclePublisher{mission: runtimeLifecycle, ontology: opts.ExecutionOntology, logger: opts.Runtime.Logger}
	postCommitRepair := &productionAgentPostCommitRepair{publisher: opts.Runtime.OutboxPublisher, notifier: opts.Notifier}
	if err := opts.Runtime.RegisterOutboxHandler(productionAgentPostCommitRepairTopic, postCommitRepair.HandleOutbox); err != nil {
		return err
	}
	committedResults, ok := any(opts.Tasks).(scheduledCommittedResultRepository)
	if !ok {
		return fmt.Errorf("mission task repository does not support scheduled result recovery")
	}
	scheduledArtifacts := newProductionSchedulerArtifactStore(opts.Runtime.Config.BaseDir, opts.Runtime.Config.FileStore, opts.Runtime.FileStore, opts.Profiles)
	scheduledDeliveryOutbox, err := registerProductionScheduledAgentDelivery(opts, scheduledArtifacts)
	if err != nil {
		return err
	}
	worker := productionAgentRunWorker{
		assembler:                 assembler,
		schedulerAssembler:        schedulerAssembler,
		profiles:                  opts.Profiles,
		lifecycle:                 combinedLifecycle,
		config:                    opts.Runtime.Config,
		timeout:                   configuredMissionAgentRunTimeout(opts.Runtime.Config),
		workerID:                  "orrery-agent-runtime",
		liveness:                  opts.Runtime.ExecutionLive,
		livenessHeartbeatInterval: opts.Runtime.Config.Liveness.HeartbeatTTL / 3,
		scheduler:                 opts.Scheduler,
		schedulerTasks:            opts.SchedulerTasks,
		scheduledDeliveryOutbox:   scheduledDeliveryOutbox,
		approvalConversations:     missionCatalogRuntimeApprovalConversationResolver{catalog: opts.MissionCatalog},
		missionWorkspaces:         opts.MissionWorkspaces,
		responsibility:            opts.Responsibility,
		postCommitRepair:          postCommitRepair,
		development:               opts.DevelopmentExecution,
		committedResults:          committedResults,
		unit:                      opts.Runtime.UnitOfWork,
		logger:                    opts.Runtime.Logger,
	}
	repair := &productionRuntimeLifecycleRepair{
		catalog: opts.MissionCatalog, tasks: opts.Tasks, unit: opts.Runtime.UnitOfWork, publisher: combinedLifecycle,
		notifier: opts.Notifier, responsibility: opts.Responsibility, execution: opts.Runtime.ExecutionQueue, logger: opts.Runtime.Logger,
	}
	if err := opts.Runtime.RegisterSweeper("mission.runtime_lifecycle_repair", platform.SweeperFunc(func(ctx context.Context) error {
		_, err := repair.Reconcile(ctx, opts.Runtime.Config.Sweeper.BatchSize)
		return err
	})); err != nil {
		return err
	}
	return opts.Runtime.RegisterExecutionDeliveryHandler(runtimedaemon.AgentRunTopic, worker.HandleExecution)
}

func productionAgentRunWorkerDependencies(opts productionAgentRunWorkerRegistration) productionDiscussionAgentDependencies {
	var traceIndexSink agentcore.TraceIndexSink
	if opts.Core != nil {
		traceIndexSink = agenttrace.NewPlatformStore(opts.Core)
	}
	deps := newProductionDiscussionAgentDependencies(opts.Lifecycle, productionTraceStore{store: opts.TraceStore, indexSink: traceIndexSink, evidenceCore: opts.Core, evidenceJournal: opts.EvidenceJournal}, nil)
	if opts.Runtime != nil {
		deps.Config = opts.Runtime.Config
	}
	var scheduledArtifacts *productionSchedulerArtifactStore
	if opts.Runtime != nil {
		scheduledArtifacts = newProductionSchedulerArtifactStore(opts.Runtime.Config.BaseDir, opts.Runtime.Config.FileStore, opts.Runtime.FileStore, opts.Profiles)
	}
	normalizer := missionTaskAgentResultNormalizer{store: opts.TraceStore, scheduledArtifacts: scheduledArtifacts}
	if reviewer := newProductionScheduledResultReviewer(opts.Settings, opts.Secrets); reviewer != nil {
		normalizer.resultReviewer = reviewer
	}
	deps.Normalizer = normalizer
	deps.LLMProfiles = opts.Settings
	deps.RuntimeProfiles = opts.Settings
	deps.WorkspaceRoot = opts.Settings
	deps.PromptBlocks = opts.Settings
	deps.MemorySettings = opts.Settings
	deps.Secrets = opts.Secrets
	deps.ToolDispatcher = opts.ToolDispatcher
	deps.MemoryRepository = opts.MemoryRepo
	deps.MemoryAudit = opts.MemoryAudit
	deps.ApprovalBroker = opts.ApprovalBroker
	deps.RuntimeRegistry = opts.RuntimeRegistry
	deps.DiaryFlush = opts.DiaryFlush
	if opts.Notifier != nil {
		deps.Notifier = opts.Notifier
	}
	return deps
}

type productionAgentRunWorker struct {
	assembler                 agentassembly.AgentAssembler
	schedulerAssembler        agentassembly.AgentAssembler
	profiles                  *agentcore.PlatformProfileRepository
	lifecycle                 runtimedaemon.RuntimeLifecyclePublisher
	config                    config.Config
	timeout                   time.Duration
	workerID                  string
	liveness                  productionAgentRunLiveness
	livenessHeartbeatInterval time.Duration
	scheduler                 schedulerRunLifecycleTracker
	schedulerTasks            schedulerTaskContractSource
	scheduledDeliveryOutbox   scheduledAgentDeliveryOutboxScheduler
	approvalConversations     missionRuntimeApprovalConversationResolver
	missionWorkspaces         missionAgentRunWorkspacePreparer
	responsibility            responsibilityRunLifecycleTracker
	postCommitRepair          interface {
		Schedule(context.Context, agentcore.AgentInput, agentcore.AgentResult) error
	}
	logger           logging.Logger
	development      developmentAgentRunCoordinator
	committedResults scheduledCommittedResultRepository
	unit             *uow.UnitOfWork
}

func (w productionAgentRunWorker) HandleExecution(ctx context.Context, delivery platform.ExecutionDelivery) error {
	if w.assembler == nil || w.profiles == nil || w.lifecycle == nil {
		return fmt.Errorf("production agent run worker is not configured")
	}
	if delivery.Topic != runtimedaemon.AgentRunTopic {
		return fmt.Errorf("unsupported agent runtime topic %q", delivery.Topic)
	}
	stopHeartbeat := startExecutionLeaseHeartbeat(ctx, delivery, time.Minute)
	defer stopHeartbeat()
	run, err := runtimedaemon.DecodeAgentRunPayload(delivery.Payload)
	if err != nil {
		return err
	}
	stopRuntimeHeartbeat := startAgentRunLivenessHeartbeat(ctx, w.liveness, run.RunID.String(), w.livenessHeartbeatInterval, w.logger)
	defer stopRuntimeHeartbeat()
	if err := w.loadScheduledResultContract(ctx, &run); err != nil {
		return err
	}
	scheduled := isSchedulerAgentRun(run)
	approvalCtx, err := w.runtimeApprovalContext(ctx, &run)
	if err != nil {
		return err
	}
	attempt := delivery.Attempts + 1
	if run.Attempt > attempt {
		attempt = run.Attempt
	}
	if scheduled && w.scheduler != nil {
		if err := w.startScheduledAgentRun(ctx, run, attempt); err != nil {
			return schedulerAgentRetryError(run, err)
		}
	}
	workerID := strings.TrimSpace(w.workerID)
	if workerID == "" {
		workerID = "orrery-agent-runtime"
	}
	if scheduled && w.committedResults != nil && w.unit != nil {
		committed, found, err := loadCommittedScheduledAgentResult(ctx, w.unit, w.committedResults, run)
		if err != nil {
			return schedulerAgentRetryError(run, err)
		}
		if found {
			return w.completeCommittedScheduledResult(ctx, run, committed, agentcore.RuntimeResponse{}, workerID)
		}
	}
	prepared, managed, err := w.developmentWorkspace(ctx, run)
	if err != nil {
		if scheduled && w.scheduler != nil {
			_ = w.scheduler.AttemptFailed(ctx, scheduledRunIDFromRun(run), attempt, deliveryAttemptIsTerminal(delivery), err)
		}
		return err
	}
	startedEvent := productionRuntimeLifecycleEvent(run)
	startedEvent.Kind = runtimedaemon.RuntimeLifecycleStarted
	startedEvent.Status = runtimedaemon.RunStatusRunning.String()
	startedEvent.WorkerID = workerID
	startedEvent.Message = "agent run started"
	startedEvent.StartedAt = time.Now().UTC()
	if err := w.lifecycle.PublishRuntimeLifecycle(ctx, startedEvent); err != nil {
		if scheduled && w.scheduler != nil {
			_ = w.scheduler.AttemptFailed(ctx, scheduledRunIDFromRun(run), attempt, deliveryAttemptIsTerminal(delivery), err)
			return schedulerAgentRetryError(run, err)
		}
		return err
	}
	instance, err := w.buildAgent(ctx, run)
	if err != nil {
		w.failDevelopmentRun(ctx, run, managed, err)
		terminal := deliveryAttemptIsTerminal(delivery)
		var recoveryErr error
		var lifecycleErr error
		if terminal {
			lifecycleErr = w.publishFailure(ctx, run, workerID, "agent_build_failed", err.Error())
			if runtimeLifecycleTerminalAlreadyWon(lifecycleErr) {
				return nil
			}
			_, recoveryErr = w.recordTerminalResponsibilityFailure(ctx, run, err, lifecycleErr, 0)
		}
		if scheduled && w.scheduler != nil {
			_ = w.scheduler.AttemptFailed(ctx, scheduledRunIDFromRun(run), attempt, terminal, err)
		}
		if recoveryErr != nil {
			return errors.Join(err, recoveryErr)
		}
		return err
	}
	configureQueuedAgentCoreTask(&run, instance.Base().RuntimeSpec().Kind)
	if scheduled && !queuedAgentCoreTaskEnabled(run, instance.Base().RuntimeSpec().Kind) {
		applyScheduledFallbackModel(&run, attempt)
	}
	var resultWorkspace discussionsession.PreparedAgentRunWorkspace
	if !managed {
		resultWorkspace, err = w.prepareAgentRunWorkspace(ctx, &run, instance.Profile().WorkspaceRoot, scheduled)
		if err != nil {
			terminal := deliveryAttemptIsTerminal(delivery)
			var recoveryErr error
			var lifecycleErr error
			if terminal {
				lifecycleErr = w.publishFailure(ctx, run, workerID, "agent_workspace_failed", err.Error())
				if runtimeLifecycleTerminalAlreadyWon(lifecycleErr) {
					return nil
				}
				_, recoveryErr = w.recordTerminalResponsibilityFailure(ctx, run, err, lifecycleErr, 0)
			}
			if scheduled && w.scheduler != nil {
				_ = w.scheduler.AttemptFailed(ctx, scheduledRunIDFromRun(run), attempt, terminal, err)
			}
			if recoveryErr != nil {
				return errors.Join(err, recoveryErr)
			}
			return err
		}
	}
	runCtx := approvalCtx
	if strings.TrimSpace(resultWorkspace.ConversationID) != "" {
		runCtx = contextWithProductionAgentResultWorkspace(runCtx, resultWorkspace)
	}
	cancel := func() {}
	runTimeout := scheduledAgentRunTimeout(run, w.timeout)
	if runTimeout > 0 {
		runCtx, cancel = context.WithTimeout(runCtx, runTimeout)
	}
	defer cancel()
	input := agentInputForProductionAgentRun(run)
	if managed {
		applyPreparedDevelopmentWorkspace(&input, prepared)
	}
	if input.RuntimeOptions.ProviderSpecific == nil {
		input.RuntimeOptions.ProviderSpecific = map[string]string{}
	}
	input.RuntimeOptions.ProviderSpecific[agentcore.MissionLifecycleAuthorityProviderKey] = agentcore.MissionLifecycleAuthorityRunWorker
	result, err := instance.Base().Handle(runCtx, input)
	runtimeResponse, _ := productionAgentRunRuntimeResponse(instance, run.RunID)
	w.recordTraceRef(ctx, &run, result.InternalTraceRef, scheduled)
	if err != nil {
		var committedErr agentcore.PostCommitPipelineError
		if errors.As(err, &committedErr) {
			if w.logger != nil {
				w.logger.Error(ctx, "Agent post-commit pipeline stage failed", logging.String("run_id", run.RunID.String()), logging.String("task_id", run.TaskID), logging.String("stage", committedErr.Stage), logging.Any("error", err))
			}
			if w.postCommitRepair == nil {
				return nil
			}
			if repairErr := w.postCommitRepair.Schedule(ctx, input, result); repairErr != nil {
				if w.logger != nil {
					w.logger.Error(ctx, "schedule Agent post-commit repair", logging.String("run_id", run.RunID.String()), logging.String("task_id", run.TaskID), logging.Any("error", repairErr))
				}
				return nil
			}
		} else {
			recoverable := queuedAgentCoreTaskCanRetry(run, instance.Base().RuntimeSpec().Kind, err)
			mustStop := queuedAgentCoreTaskMustStop(run, instance.Base().RuntimeSpec().Kind, err)
			terminal := mustStop || (isRuntimePipelineTerminalError(err) && !recoverable) || deliveryAttemptIsTerminal(delivery)
			if !recoverable || terminal {
				w.failDevelopmentRun(ctx, run, managed, err)
			}
			var lifecycleErr error
			if terminal {
				lifecycleErr = w.publishFailure(ctx, run, workerID, runtimeFailureCodeFromError(err, "agent_runtime_failed"), err.Error(), runtimeFailureMetadataFromError(err))
				if runtimeLifecycleTerminalAlreadyWon(lifecycleErr) {
					return nil
				}
			}
			responsibilityTracked := false
			var recoveryErr error
			if terminal {
				responsibilityTracked, recoveryErr = w.recordTerminalResponsibilityFailure(ctx, run, err, lifecycleErr, totalRuntimeUsageTokens(runtimeResponse.Usage))
			}
			if scheduled && w.scheduler != nil {
				_ = w.scheduler.AttemptFailed(ctx, scheduledRunIDFromRun(run), attempt, terminal && !responsibilityTracked, err)
			}
			if recoveryErr != nil {
				return errors.Join(err, recoveryErr)
			}
			if recoverable && !terminal {
				return execution.RetryAfter(err, 2*time.Second)
			}
			if mustStop || isRuntimePipelineTerminalError(err) {
				return nil
			}
			return err
		}
	}
	message := strings.TrimSpace(result.Summary.Text)
	if message == "" {
		message = "agent run completed"
	}
	if scheduled {
		return w.completeCommittedScheduledResult(ctx, run, result, runtimeResponse, workerID)
	}
	runResult, verifiedArtifacts, verifiedEvidence, err := evaluateScheduledAgentResult(run, message, startedEvent.StartedAt)
	if err != nil {
		if lifecycleErr := w.publishFailure(ctx, run, workerID, "agent_result_contract_failed", err.Error()); runtimeLifecycleTerminalAlreadyWon(lifecycleErr) {
			return nil
		}
		if scheduled && w.scheduler != nil {
			_ = w.scheduler.AttemptFailed(ctx, scheduledRunIDFromRun(run), attempt, deliveryAttemptIsTerminal(delivery), err)
			return schedulerAgentRetryError(run, err)
		}
		return err
	}
	allArtifacts := mergeRuntimeArtifacts(runtimeLifecycleArtifacts(result, runtimeResponse), verifiedArtifacts)
	if len(runResult.Artifacts) == 0 {
		runResult.Artifacts = schedulerRunArtifacts(allArtifacts)
	}
	completedEvent := productionRuntimeLifecycleEvent(run)
	completedEvent.Kind = runtimedaemon.RuntimeLifecycleCompleted
	completedEvent.Status = runtimedaemon.RunStatusCompleted.String()
	completedEvent.WorkerID = workerID
	completedEvent.Message = message
	completedEvent.Usage = cloneProductionRuntimeUsage(runtimeResponse.Usage)
	completedEvent.TraceRef = strings.TrimSpace(result.InternalTraceRef)
	completedEvent.Artifacts = allArtifacts
	completedEvent.Evidence = append(runtimeLifecycleEvidence(result), verifiedEvidence...)
	completedEvent.KnowledgeRefs = runtimeLifecycleKnowledgeRefs(runtimeResponse)
	completedEvent.DurationMs = runtimeResponse.DurationMs
	completedEvent.CompletedAt = runtimeResponse.CompletedAt
	if completedEvent.CompletedAt.IsZero() {
		completedEvent.CompletedAt = time.Now().UTC()
	}
	if err := w.lifecycle.PublishRuntimeLifecycle(ctx, completedEvent); err != nil {
		if runtimeLifecycleTerminalAlreadyWon(err) {
			return nil
		}
		if w.logger != nil {
			w.logger.Error(ctx, "publish completed runtime lifecycle", logging.String("run_id", run.RunID.String()), logging.String("task_id", run.TaskID), logging.Any("error", err))
		}
	}
	if w.responsibility != nil {
		var err error
		if tracker, ok := w.responsibility.(responsibilityRunUsageLifecycleTracker); ok {
			err = tracker.CompletedWithTraceAndUsage(ctx, run.Request.TenantID, run.TaskID, run.RunID.String(), result.InternalTraceRef, totalRuntimeUsageTokens(runtimeResponse.Usage))
		} else if tracker, ok := w.responsibility.(responsibilityRunTraceLifecycleTracker); ok {
			err = tracker.CompletedWithTrace(ctx, run.Request.TenantID, run.TaskID, run.RunID.String(), result.InternalTraceRef)
		} else {
			err = w.responsibility.Completed(ctx, run.Request.TenantID, run.TaskID, run.RunID.String())
		}
		if err != nil {
			// The task result is already committed at this point. Replaying the
			// delivery would rerun Agent tools, so the coordinator recovers the
			// responsibility from the durable Task state instead.
			if w.logger != nil {
				w.logger.Error(ctx, "record completed responsibility attempt", logging.String("run_id", run.RunID.String()), logging.String("task_id", run.TaskID), logging.Any("error", err))
			}
		}
	}
	return nil
}

func (w productionAgentRunWorker) developmentWorkspace(ctx context.Context, run runtimedaemon.AgentRun) (developmentproject.PreparedAgentRun, bool, error) {
	if w.development == nil {
		return developmentproject.PreparedAgentRun{}, false, nil
	}
	return w.development.PrepareAgentRun(ctx, run.Request.TenantID, run.MissionID, run.TaskID, run.AgentID.String(), run.RunID.String())
}

func (w productionAgentRunWorker) prepareAgentRunWorkspace(ctx context.Context, run *runtimedaemon.AgentRun, profileWorkspaceRoot string, scheduled bool) (discussionsession.PreparedAgentRunWorkspace, error) {
	if run == nil {
		return discussionsession.PreparedAgentRunWorkspace{}, fmt.Errorf("agent run is required")
	}
	conversationID := strings.TrimSpace(run.Request.ContextPack.ConversationID)
	if !scheduled && conversationID != "" && w.missionWorkspaces != nil && !productionRuntimeWorkspaceConfigured(run.Request.RuntimeOptions) && strings.TrimSpace(run.Request.RuntimeOptions.WorkingDirectory) == "" {
		workspace, err := w.missionWorkspaces.PrepareAgentRunWorkspace(ctx, conversationID, run.AgentID.String())
		if err != nil {
			return discussionsession.PreparedAgentRunWorkspace{}, fmt.Errorf("prepare canonical Mission discussion workspace: %w", err)
		}
		options := &run.Request.RuntimeOptions
		options.WorkingDirectory = workspace.WorkDir
		options.WorkspacePolicy = workspace.Policy
		options.SandboxProfile = workspace.Policy.Profile.Clone()
		if options.ProviderSpecific == nil {
			options.ProviderSpecific = map[string]string{}
		}
		options.ProviderSpecific[agentcore.CanonicalDiscussionWorkspaceProviderKey] = "true"
		return workspace, nil
	}
	if err := prepareProductionAgentRunWorkspace(run, w.config, profileWorkspaceRoot); err != nil {
		return discussionsession.PreparedAgentRunWorkspace{}, err
	}
	return discussionsession.PreparedAgentRunWorkspace{}, nil
}

func (w productionAgentRunWorker) failDevelopmentRun(ctx context.Context, run runtimedaemon.AgentRun, managed bool, cause error) {
	if !managed || w.development == nil {
		return
	}
	if err := w.development.FailAgentRun(context.WithoutCancel(ctx), run.Request.TenantID, run.MissionID, run.TaskID, run.AgentID.String(), run.RunID.String(), cause); err != nil && w.logger != nil {
		w.logger.Error(ctx, "release failed development Agent run", logging.String("run_id", run.RunID.String()), logging.Any("error", err))
	}
}

func applyPreparedDevelopmentWorkspace(input *agentcore.AgentInput, prepared developmentproject.PreparedAgentRun) {
	if input == nil {
		return
	}
	network := agentcore.RuntimeNetworkDisabled
	if prepared.NetworkMode == "allowed" {
		network = agentcore.RuntimeNetworkAllowed
	}
	profile := agentcore.RuntimeSandboxProfile{
		Mode: agentcore.RuntimeSandboxWorkspaceWrite, Approval: agentcore.RuntimeApprovalOnRequest,
		Network: network, RuntimeHomeIsolation: agentcore.RuntimeHomeIsolatedPerRun,
		Retention: agentcore.RuntimeRetentionKeepOnFailure, Root: prepared.RootDir, WorkDir: prepared.WorkDir,
		WritableRoots: []string{prepared.RootDir}, EnforceBoundary: true,
	}
	input.RuntimeOptions.WorkingDirectory = prepared.WorkDir
	input.RuntimeOptions.WorkspacePolicy = profile.WorkspacePolicy()
	input.RuntimeOptions.SandboxProfile = profile
	if input.RuntimeOptions.ProviderSpecific == nil {
		input.RuntimeOptions.ProviderSpecific = map[string]string{}
	}
	input.RuntimeOptions.ProviderSpecific["development_execution_run_id"] = prepared.ExecutionRunID
	input.RuntimeOptions.ProviderSpecific["development_workspace_id"] = prepared.WorkspaceID
	if len(prepared.AllowedHosts) > 0 {
		input.RuntimeOptions.ProviderSpecific["development_allowed_hosts"] = strings.Join(prepared.AllowedHosts, ",")
	}
}

type developmentAgentResultCommitter struct {
	next     agentcore.ResultCommitter
	projects developmentAgentRunCoordinator
}

func (c developmentAgentResultCommitter) SubmitAgentResult(ctx context.Context, result agentcore.AgentResult, opts agentcore.CommitOptions) error {
	if c.next == nil {
		return fmt.Errorf("mission result committer is not configured")
	}
	if c.projects != nil {
		_, managed, err := c.projects.CommitAgentRunResult(ctx, opts.TenantID, result.MissionID.String(), result.TaskID.String(), result.AgentID.String(), result.RunID.String(), result.Summary.Text)
		if err != nil {
			return err
		}
		_ = managed
	}
	return c.next.SubmitAgentResult(ctx, result, opts)
}

func deliveryAttemptIsTerminal(delivery platform.ExecutionDelivery) bool {
	return delivery.MaxAttempts > 0 && delivery.Attempts+1 >= delivery.MaxAttempts
}

func schedulerAgentRetryError(run runtimedaemon.AgentRun, err error) error {
	delay, parseErr := time.ParseDuration(strings.TrimSpace(run.Request.RuntimeOptions.ProviderSpecific["retry_delay"]))
	if parseErr != nil || delay < 0 {
		delay = 0
	}
	return execution.RetryAfter(err, delay)
}

func isSchedulerAgentRun(run runtimedaemon.AgentRun) bool {
	providerSpecific := run.Request.RuntimeOptions.ProviderSpecific
	return strings.TrimSpace(providerSpecific["run_source"]) == schedulerAgentTaskRunSource ||
		strings.TrimSpace(providerSpecific["scheduled_task_id"]) != ""
}

func (w productionAgentRunWorker) buildAgent(ctx context.Context, run runtimedaemon.AgentRun) (agentcore.AgentInstance, error) {
	profileID := run.AgentProfileID
	if profileID.IsZero() {
		profileID = agentcore.AgentProfileID(run.AgentID)
	}
	profile, err := w.profiles.GetProfile(ctx, profileID)
	if err != nil {
		return nil, err
	}
	if profile.ID.IsZero() || !profile.Enabled {
		return nil, fmt.Errorf("agent profile %s is not available", profileID)
	}
	if run.RuntimeID != "" {
		requestedKind := agentcore.RuntimeKind(run.RuntimeID)
		configuredSpec := configuredConversationAgentRuntimeOptions(w.config, profile.ID.String()).RuntimeSpec
		switch {
		case configuredSpec.Kind == requestedKind && configuredSpec.HasConfig():
			profile.RuntimeSpec = configuredSpec
		case profile.RuntimeSpec.Kind != requestedKind:
			profile.RuntimeSpec = agentcore.RuntimeSpec{Kind: requestedKind}
		}
	} else if spec := configuredConversationAgentRuntimeOptions(w.config, profile.ID.String()).RuntimeSpec; spec.HasKind() {
		profile.RuntimeSpec = spec
	}
	if !profile.RuntimeSpec.HasKind() && systemManagedDiscussionProfile(profile) {
		profile.RuntimeSpec = defaultAgentRuntimeSpec(w.config, profile.ID.String())
	}
	instanceID := run.AgentInstanceID
	if instanceID.IsZero() {
		instanceID = agentcore.AgentInstanceID(profile.ID)
	}
	assembler := w.assembler
	if isSchedulerAgentRun(run) && w.schedulerAssembler != nil {
		assembler = w.schedulerAssembler
	}
	return assembler.Build(ctx, profile, agentassembly.BuildOptions{
		AgentID:     run.AgentID,
		InstanceID:  instanceID,
		Permissions: runtimePermissionScope(run),
	})
}

func (w productionAgentRunWorker) publishFailure(ctx context.Context, run runtimedaemon.AgentRun, workerID, code, message string, metadata ...map[string]string) error {
	var eventMetadata map[string]string
	if len(metadata) > 0 {
		eventMetadata = cloneStringMap(metadata[0])
	}
	return w.lifecycle.PublishRuntimeFailure(ctx, runtimedaemon.RuntimeFailureEvent{
		Kind:            runtimeLifecycleKindFromFailureCode(code),
		TenantID:        run.Request.TenantID,
		MissionID:       missionIDFromRun(run),
		TaskID:          taskIDFromRun(run),
		AgentID:         run.AgentID,
		RunID:           run.RunID,
		AgentTaskID:     productionAgentTaskOntologyID(run),
		ScheduledTaskID: run.Request.RuntimeOptions.ProviderSpecific["scheduled_task_id"],
		ScheduledRunID:  firstNonBlank(run.Request.RuntimeOptions.ProviderSpecific["scheduled_run_id"], scheduledRunIDFromRun(run)),
		RuntimeKind:     run.RuntimeID,
		QueueID:         agentRunQueueID(run),
		WorkerID:        workerID,
		Code:            code,
		Message:         message,
		TraceRef:        strings.TrimSpace(run.Request.RuntimeOptions.ProviderSpecific["trace_ref"]),
		Metadata:        eventMetadata,
	})
}

func runtimeFailureCodeFromError(err error, fallback string) string {
	var runtimeErr agentcore.RuntimePipelineError
	if !errors.As(err, &runtimeErr) {
		return fallback
	}
	switch runtimeErr.Status {
	case agentcore.RuntimeStatusBlocked:
		return "agent_runtime_blocked"
	case agentcore.RuntimeStatusTimeout:
		return "agent_runtime_timeout"
	case agentcore.RuntimeStatusCancelled:
		return "agent_runtime_cancelled"
	default:
		return fallback
	}
}

func runtimeLifecycleKindFromFailureCode(code string) runtimedaemon.RuntimeLifecycleKind {
	switch strings.TrimSpace(code) {
	case "agent_runtime_blocked":
		return runtimedaemon.RuntimeLifecycleBlocked
	case "agent_runtime_timeout":
		return runtimedaemon.RuntimeLifecycleTimedOut
	case "agent_runtime_cancelled":
		return runtimedaemon.RuntimeLifecycleCancelled
	default:
		return runtimedaemon.RuntimeLifecycleFailed
	}
}

func runtimeFailureMetadataFromError(err error) map[string]string {
	var runtimeErr agentcore.RuntimePipelineError
	if errors.As(err, &runtimeErr) {
		return cloneStringMap(runtimeErr.ProviderMetadata)
	}
	return nil
}

func isRuntimePipelineTerminalError(err error) bool {
	var runtimeErr agentcore.RuntimePipelineError
	return errors.As(err, &runtimeErr) && runtimeErr.Status.IsTerminal()
}

func cloneStringMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]string, len(values))
	for key, value := range values {
		if strings.TrimSpace(key) != "" && strings.TrimSpace(value) != "" {
			out[key] = strings.TrimSpace(value)
		}
	}
	return out
}

func agentInputForProductionAgentRun(run runtimedaemon.AgentRun) agentcore.AgentInput {
	input := runtimedaemon.AgentInputFromRun(run)
	if generationID := strings.TrimSpace(input.RuntimeOptions.ProviderSpecific["generation_id"]); generationID != "" {
		triggerType := strings.TrimSpace(input.RuntimeOptions.ProviderSpecific["scheduled_trigger_type"])
		generationInstruction := "Orrery generation identity: generation_id=" + generationID + ", trigger_type=" + triggerType + ". Internal retries must reuse this exact generation. Never resume another generation or use historical article state as recovery input."
		if scheduledTaskID := strings.TrimSpace(input.RuntimeOptions.ProviderSpecific["scheduled_task_id"]); scheduledTaskID != "" {
			generationInstruction += " Scheduled task identity: scheduled_task_id=" + scheduledTaskID + ", scheduled_run_id=" + strings.TrimSpace(input.RuntimeOptions.ProviderSpecific["scheduled_run_id"]) + ". Use these exact IDs for the current occurrence's tool requests and receipts."
		}
		input.ContextPack.WorkerPersonalGoal = strings.TrimSpace(input.ContextPack.WorkerPersonalGoal + "\n\n" + generationInstruction)
		input.Task.WorkerPersonalGoal = strings.TrimSpace(input.Task.WorkerPersonalGoal + "\n\n" + generationInstruction)
		blocks := agentcore.NormalizeRuntimePromptBlocks(input.RuntimeOptions.PromptBlocks)
		generationBlock := agentcore.RuntimePromptBlock{
			BlockID: scheduledGenerationPromptBlockID,
			Role:    agentcore.RuntimePromptRoleUser,
			Content: generationInstruction,
			Enabled: true,
		}
		replaced := false
		for index := range blocks {
			if blocks[index].BlockID == scheduledGenerationPromptBlockID {
				blocks[index] = generationBlock
				replaced = true
				break
			}
		}
		if !replaced {
			blocks = append(blocks, generationBlock)
		}
		input.RuntimeOptions.PromptBlocks = blocks
	}
	if isSchedulerAgentRun(run) {
		input.RuntimeOptions.PromptBlocks = append(agentcore.NormalizeRuntimePromptBlocks(input.RuntimeOptions.PromptBlocks), agentcore.RuntimePromptBlock{
			BlockID: scheduledFeishuPublicSummaryPromptBlockID,
			Role:    agentcore.RuntimePromptRoleUser,
			Content: scheduledFeishuPublicSummaryInstruction,
			Enabled: true,
		})
	}
	instruction := formalMissionTaskResultInstruction(input.Task)
	if isSchedulerAgentRun(run) {
		instruction = scheduledMissionTaskResultInstruction(input.Task)
	}
	if instruction != "" {
		input.RuntimeOptions.PromptBlocks = append(agentcore.NormalizeRuntimePromptBlocks(input.RuntimeOptions.PromptBlocks), agentcore.RuntimePromptBlock{
			BlockID: formalMissionTaskResultPromptBlockID,
			Role:    agentcore.RuntimePromptRoleUser,
			Content: instruction,
			Enabled: true,
		})
	}
	return input
}

func missionIDFromRun(run runtimedaemon.AgentRun) missioncore.MissionID {
	return missioncore.MissionID(strings.TrimSpace(run.MissionID))
}

func taskIDFromRun(run runtimedaemon.AgentRun) missioncore.TaskID {
	return missioncore.TaskID(strings.TrimSpace(run.TaskID))
}
