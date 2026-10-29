package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	runtimedaemon "orrery_v2/internal/agent/runtime/daemon"
	"orrery_v2/internal/app/responsibility"
	schedulerapp "orrery_v2/internal/app/scheduler"
	missiontask "orrery_v2/internal/mission/task"
	"orrery_v2/internal/platform"
	"orrery_v2/internal/platform/clock"
	"orrery_v2/internal/platform/execution"
	"orrery_v2/internal/platform/idgen"
	"orrery_v2/internal/platform/uow"
)

const productionSchedulerDispatchTopic = "scheduler.run.dispatch"

func newProductionSchedulerRuntime(
	rt *platform.Runtime,
	repo *schedulerapp.Repository,
	discussion schedulerapp.DiscussionPort,
	memoryDistillation schedulerapp.MemoryDistillationPort,
	memoryLifecycle schedulerapp.MemoryLifecyclePort,
	toolCalls schedulerapp.ToolCallPort,
	todoAutomation schedulerapp.TodoAutomationPort,
	responsibilityEvaluation schedulerapp.ResponsibilityEvaluationPort,
	responsibilities schedulerapp.ResponsibilityReconcilePort,
	ontologyStateRefresh schedulerapp.OntologyStateRefreshPort,
	creatorPermissions schedulerapp.CreatorPermissionResolver,
	profiles *agentcore.PlatformProfileRepository,
	missionCommands schedulerMissionCommandPort,
) (*schedulerapp.Service, schedulerapp.RunDispatcher, error) {
	service := schedulerapp.NewService(schedulerapp.ServiceOptions{
		Clock:            rt.Clock,
		IDGenerator:      rt.IDGenerator,
		Repository:       repo,
		ArtifactStore:    newProductionSchedulerArtifactStore(rt.Config.BaseDir, rt.Config.FileStore, rt.FileStore, profiles),
		Locker:           productionSchedulerLocker{locks: rt.Locks, ids: rt.IDGenerator},
		MissionLifecycle: productionSchedulerMissionLifecycle{missions: missionCommands},
	})
	runDispatcher := schedulerapp.NewDispatcher(schedulerapp.DispatcherOptions{
		Clock:                    rt.Clock,
		Repository:               repo,
		Discussion:               discussion,
		AgentTasks:               productionSchedulerAgentTaskPort{missions: missionCommands, runs: repo},
		MemoryDistillation:       memoryDistillation,
		MemoryLifecycle:          memoryLifecycle,
		TodoAutomation:           todoAutomation,
		ResponsibilityEvaluation: responsibilityEvaluation,
		Responsibilities:         responsibilities,
		OntologyStateRefresh:     ontologyStateRefresh,
		ToolCalls:                toolCalls,
		CreatorPermissions:       creatorPermissions,
	})
	if err := runProductionSchedulerOccurrenceReconciliation(context.Background(), service, rt.Clock.Now(), rt.Logger); err != nil {
		return nil, nil, fmt.Errorf("reconcile disabled scheduler occurrences: %w", err)
	}
	if err := registerProductionSchedulerDispatch(rt, repo, runDispatcher); err != nil {
		return nil, nil, err
	}
	return service, productionSchedulerDispatcher{queue: rt.ExecutionQueue, repo: repo, clock: rt.Clock}, nil
}

func newProductionSchedulerSweeper(rt *platform.Runtime, repo *schedulerapp.Repository, service *schedulerapp.Service, dispatcher schedulerapp.RunDispatcher, responsibilities *responsibility.Repository) *schedulerapp.Sweeper {
	return schedulerapp.NewSweeper(schedulerapp.SweeperOptions{
		Service:    service,
		Repository: repo,
		Dispatcher: dispatcher,
		Clock:      rt.Clock,
		Reconciler: productionSchedulerRunReconciler{
			repo: repo, queue: rt.ExecutionQueue, responsibilities: responsibilities, clock: rt.Clock,
			unit: rt.UnitOfWork, committedResults: missiontask.NewPlatformRepository(),
			deliveries: productionScheduledAgentDeliveryOutbox{jobs: rt.Outbox},
		},
		MisfireGrace: 2 * time.Minute,
	})
}

type productionSchedulerDispatchPayload struct {
	ScheduledTaskID string `json:"scheduled_task_id"`
	ScheduledRunID  string `json:"scheduled_run_id"`
}

type productionSchedulerDispatcher struct {
	queue *execution.QueueService
	repo  *schedulerapp.Repository
	clock clock.Clock
}

func (d productionSchedulerDispatcher) DispatchRun(ctx context.Context, task schedulerapp.ScheduledTask, run schedulerapp.ScheduledRun) (schedulerapp.ScheduledRun, error) {
	if d.queue == nil || d.repo == nil {
		return schedulerapp.ScheduledRun{}, schedulerapp.ErrServiceDependency
	}
	payload, err := json.Marshal(productionSchedulerDispatchPayload{
		ScheduledTaskID: strings.TrimSpace(task.ScheduledTaskID),
		ScheduledRunID:  strings.TrimSpace(run.ScheduledRunID),
	})
	if err != nil {
		return schedulerapp.ScheduledRun{}, fmt.Errorf("encode scheduler dispatch: %w", err)
	}
	policy := task.Policy
	if run.ConfigurationSnapshot != nil {
		policy = run.ConfigurationSnapshot.Policy
	}
	queueKey := "scheduler-run:" + strings.TrimSpace(run.ScheduledRunID)
	if policy.Concurrency == schedulerapp.ConcurrencySkipIfRunning {
		queueKey = "scheduler:" + strings.TrimSpace(task.ScheduledTaskID)
	}
	job := execution.Job{
		ID:          "scheduler-dispatch:" + strings.TrimSpace(run.ScheduledRunID),
		Topic:       productionSchedulerDispatchTopic,
		Payload:     payload,
		QueueKey:    queueKey,
		MaxAttempts: max(1, policy.MaxRetries+1),
	}
	if err := d.queue.Enqueue(ctx, job); err != nil {
		persisted, saveErr := d.repo.MutateRun(ctx, run.ScheduledRunID, func(current *schedulerapp.ScheduledRun) error {
			current.Status = schedulerapp.RunStatusFailed
			current.FailureReason = err.Error()
			current.CompletedAt = d.now()
			return nil
		})
		if saveErr != nil {
			return run, errors.Join(err, saveErr)
		}
		return persisted, err
	}
	return run, nil
}

func (d productionSchedulerDispatcher) now() time.Time {
	if d.clock == nil {
		return time.Now().UTC()
	}
	return d.clock.Now().UTC()
}

func registerProductionSchedulerDispatch(rt *platform.Runtime, repo *schedulerapp.Repository, dispatcher schedulerapp.RunDispatcher) error {
	if rt == nil || repo == nil || dispatcher == nil {
		return schedulerapp.ErrServiceDependency
	}
	return rt.RegisterExecutionDeliveryHandler(productionSchedulerDispatchTopic, productionSchedulerDispatchHandler(repo, dispatcher, rt.Clock))
}

func productionSchedulerDispatchHandler(repo *schedulerapp.Repository, dispatcher schedulerapp.RunDispatcher, dispatchClock clock.Clock) platform.ExecutionDeliveryHandler {
	return func(ctx context.Context, delivery platform.ExecutionDelivery) error {
		stopHeartbeat := startExecutionLeaseHeartbeat(ctx, delivery, time.Minute)
		defer stopHeartbeat()
		var payload productionSchedulerDispatchPayload
		if err := json.Unmarshal(delivery.Payload, &payload); err != nil {
			return fmt.Errorf("decode scheduler dispatch: %w", err)
		}
		task, err := repo.GetTask(ctx, strings.TrimSpace(payload.ScheduledTaskID))
		if err != nil {
			return err
		}
		run, err := repo.GetRun(ctx, strings.TrimSpace(payload.ScheduledRunID))
		if err != nil {
			return err
		}
		if strings.TrimSpace(run.ScheduledTaskID) != strings.TrimSpace(task.ScheduledTaskID) ||
			(strings.TrimSpace(run.TenantID) != "" && strings.TrimSpace(run.TenantID) != strings.TrimSpace(task.TenantID)) {
			return schedulerapp.ErrRevisionConflict
		}
		if run.Status == schedulerapp.RunStatusCompleted || run.Status == schedulerapp.RunStatusSkipped {
			return nil
		}
		if !task.Enabled || task.Status != schedulerapp.TaskStatusEnabled {
			_, err := repo.MutateRun(ctx, run.ScheduledRunID, func(current *schedulerapp.ScheduledRun) error {
				if current.Status != schedulerapp.RunStatusQueued && current.Status != schedulerapp.RunStatusRunning {
					return nil
				}
				current.Status = schedulerapp.RunStatusSkipped
				current.FailureReason = "scheduled task was disabled or archived before dispatch"
				if dispatchClock == nil {
					current.CompletedAt = time.Now().UTC()
				} else {
					current.CompletedAt = dispatchClock.Now().UTC()
				}
				return nil
			})
			return err
		}
		run.Attempt = delivery.Attempts + 1
		_, err = dispatcher.DispatchRun(ctx, task, run)
		if err == nil {
			return nil
		}
		if acknowledgeSchedulerPersistenceError(err) {
			return nil
		}
		if delivery.MaxAttempts <= 0 || delivery.Attempts+1 < delivery.MaxAttempts {
			tracker := productionSchedulerRunTracker{repo: repo, clock: dispatchClock}
			if trackErr := tracker.AttemptFailed(ctx, run.ScheduledRunID, delivery.Attempts+1, false, err); trackErr != nil {
				err = errors.Join(err, trackErr)
			}
		}
		return execution.RetryAfter(err, schedulerRetryDelay(task.Policy))
	}
}

func acknowledgeSchedulerPersistenceError(err error) bool {
	var persistenceErr schedulerapp.RunPersistenceError
	return errors.As(err, &persistenceErr) && (persistenceErr.Run.Status == schedulerapp.RunStatusCompleted ||
		(persistenceErr.Run.TargetMode == schedulerapp.TargetModeAgentTask && persistenceErr.Run.CommandID != ""))
}

func schedulerRetryDelay(policy schedulerapp.Policy) time.Duration {
	delay, err := time.ParseDuration(strings.TrimSpace(policy.RetryDelay))
	if err != nil || delay < 0 {
		return 0
	}
	return delay
}

func startExecutionLeaseHeartbeat(ctx context.Context, delivery platform.ExecutionDelivery, leaseTTL time.Duration) func() {
	if leaseTTL <= 0 {
		leaseTTL = time.Minute
	}
	heartbeatCtx, cancel := context.WithCancel(ctx)
	_ = delivery.ExtendLease(heartbeatCtx, time.Now().UTC().Add(leaseTTL))
	go func() {
		ticker := time.NewTicker(leaseTTL / 3)
		defer ticker.Stop()
		for {
			select {
			case <-heartbeatCtx.Done():
				return
			case <-ticker.C:
				_ = delivery.ExtendLease(heartbeatCtx, time.Now().UTC().Add(leaseTTL))
			}
		}
	}()
	return cancel
}

type productionSchedulerLocker struct {
	locks *platform.LockManager
	ids   idgen.ErrorGenerator
}

func (l productionSchedulerLocker) Acquire(ctx context.Context, key string) (func(), error) {
	if l.locks == nil || l.ids == nil {
		return nil, schedulerapp.ErrServiceDependency
	}
	owner, err := l.ids.NewIDContext()
	if err != nil {
		return nil, err
	}
	lockKey := "scheduler:" + strings.TrimSpace(key)
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		lease, acquired, err := l.locks.TryAcquire(ctx, lockKey, owner)
		if err != nil {
			return nil, err
		}
		if acquired {
			return func() { _ = lease.Release(context.Background()) }, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
		}
	}
}

type productionSchedulerRunTracker struct {
	repo             *schedulerapp.Repository
	clock            clock.Clock
	responsibilities scheduledAgentAttemptSource
}

type productionSchedulerRunReconciler struct {
	repo             *schedulerapp.Repository
	queue            *execution.QueueService
	responsibilities *responsibility.Repository
	clock            clock.Clock
	unit             *uow.UnitOfWork
	committedResults scheduledCommittedResultRepository
	deliveries       scheduledAgentDeliveryOutboxScheduler
}

func (r productionSchedulerRunReconciler) Reconcile(ctx context.Context) error {
	if r.repo == nil || r.queue == nil {
		return schedulerapp.ErrServiceDependency
	}
	runs, err := r.repo.ListActiveRuns(ctx)
	if err != nil {
		return err
	}
	var reconcileErrors []error
	for _, run := range runs {
		if err := r.reconcileRun(ctx, run); err != nil {
			reconcileErrors = append(reconcileErrors, fmt.Errorf("reconcile run %s: %w", run.ScheduledRunID, err))
		}
	}
	pendingDeliveries, err := r.repo.ListPendingDeliveryRuns(ctx)
	if err != nil {
		reconcileErrors = append(reconcileErrors, fmt.Errorf("list pending scheduled deliveries: %w", err))
	} else {
		for _, run := range pendingDeliveries {
			if err := r.reconcileDelivery(ctx, run); err != nil {
				reconcileErrors = append(reconcileErrors, fmt.Errorf("reconcile delivery for run %s: %w", run.ScheduledRunID, err))
			}
		}
	}
	return errors.Join(reconcileErrors...)
}

func (r productionSchedulerRunReconciler) reconcileDelivery(ctx context.Context, run schedulerapp.ScheduledRun) error {
	if r.deliveries == nil {
		return schedulerapp.ErrServiceDependency
	}
	runtimeRun, err := scheduledDeliveryRuntimeRunFromStored(run)
	if err != nil {
		return err
	}
	if err := r.deliveries.Schedule(ctx, runtimeRun, run); err != nil {
		if errors.Is(err, errScheduledAgentDeliveryDeadLetter) {
			return (productionSchedulerRunTracker{repo: r.repo, clock: r.clock}).RecordDelivery(
				ctx, run.ScheduledRunID, "dead_letter", scheduledDeliveryDeadLetterPublicError(), "",
			)
		}
		return err
	}
	return nil
}

func (r productionSchedulerRunReconciler) reconcileRun(ctx context.Context, run schedulerapp.ScheduledRun) error {
	tracker := productionSchedulerRunTracker{repo: r.repo, clock: r.clock}
	if run.TargetMode == schedulerapp.TargetModeAgentTask {
		if r.responsibilities == nil {
			return schedulerapp.ErrServiceDependency
		}
		if strings.TrimSpace(run.ResponsibilityID) != "" {
			assignment, err := r.responsibilities.GetAssignment(ctx, run.ResponsibilityID)
			if err != nil {
				return err
			}
			if assignment.MissionID != run.MissionID || assignment.WorkID != run.TaskID || assignment.AccountableAgent != run.AgentID {
				return responsibility.ErrConflict
			}
			if expired, err := r.expireIdleScheduledOccurrence(ctx, run, assignment); err != nil || expired {
				return err
			}
			switch assignment.State {
			case responsibility.AssignmentStateFulfilled:
				if strings.TrimSpace(run.Result.Summary) == "" && len(run.Result.Artifacts) == 0 {
					return r.recoverFulfilledAgentRun(ctx, tracker, run, assignment)
				}
				return tracker.Completed(ctx, run.ScheduledRunID, run.Result)
			case responsibility.AssignmentStateDeclined, responsibility.AssignmentStateEscalated, responsibility.AssignmentStateReassigned, responsibility.AssignmentStateTerminated:
				reason := firstNonBlank(assignment.TerminationNote, "scheduled responsibility ended in "+string(assignment.State))
				return tracker.AttemptFailed(ctx, run.ScheduledRunID, run.Attempt, true, errors.New(reason))
			default:
				attempts, err := r.responsibilities.ListAttempts(ctx, assignment.ID)
				if err != nil {
					return err
				}
				if len(attempts) == 0 {
					blockers, err := r.responsibilities.ListBlockers(ctx, assignment.TenantID, assignment.ID, true)
					if err != nil {
						return err
					}
					reason := "Waiting for the accountable Agent to accept this occurrence"
					for _, blocker := range blockers {
						if blocker.Kind == responsibility.BlockerKindPermission {
							reason = string(blocker.Kind) + ": " + blocker.Needed
							break
						}
						reason = string(blocker.Kind) + ": " + blocker.Needed
					}
					return tracker.AwaitingResponsibility(ctx, run.ScheduledRunID, reason)
				}
				return tracker.Started(ctx, run.ScheduledRunID, max(1, run.Attempt))
			}
		}
	}
	dispatchID := "scheduler-dispatch:" + run.ScheduledRunID
	status, ok, err := r.queue.GetStatus(ctx, dispatchID)
	if err != nil {
		return err
	}
	if ok {
		if run.TargetMode == schedulerapp.TargetModeAgentTask && status.Status == execution.StatusCompleted {
			latest, err := r.repo.GetRun(ctx, run.ScheduledRunID)
			if err != nil {
				return err
			}
			if strings.TrimSpace(latest.ResponsibilityID) != "" {
				return r.reconcileRun(ctx, latest)
			}
			return tracker.AttemptFailed(ctx, run.ScheduledRunID, run.Attempt, true, errors.New("scheduled Agent work was not linked to a Mission Task responsibility"))
		}
		return reconcileSchedulerRunStatus(ctx, tracker, run, status)
	}
	if !run.CreatedAt.IsZero() && run.CreatedAt.Before(r.now().Add(-2*time.Minute)) {
		return tracker.AttemptFailed(ctx, run.ScheduledRunID, run.Attempt, true, errors.New("scheduler execution job is missing"))
	}
	return nil
}

func (t productionSchedulerRunTracker) AwaitingResponsibility(ctx context.Context, runID, reason string) error {
	return t.update(ctx, runID, func(run *schedulerapp.ScheduledRun) {
		if run.RuntimeRunID != "" || run.Status == schedulerapp.RunStatusCompleted || run.Status == schedulerapp.RunStatusFailed || run.Status == schedulerapp.RunStatusSkipped {
			return
		}
		// queued is active and recoverable, and does not claim a runtime started.
		run.Status = schedulerapp.RunStatusQueued
		run.StartedAt = time.Time{}
		run.FailureReason = reason
	})
}

func (r productionSchedulerRunReconciler) recoverFulfilledAgentRun(ctx context.Context, tracker productionSchedulerRunTracker, run schedulerapp.ScheduledRun, assignment responsibility.AssignmentRecord) error {
	if r.unit == nil || r.committedResults == nil {
		return schedulerapp.ErrServiceDependency
	}
	if assignment.SourceKind != schedulerAgentTaskRunSource || assignment.SourceID != run.ScheduledTaskID || assignment.SourceStepID != run.ScheduledRunID {
		return fmt.Errorf("%w: fulfilled responsibility does not belong to the scheduled run", responsibility.ErrConflict)
	}
	attempts, err := r.responsibilities.ListAttempts(ctx, assignment.ID)
	if err != nil {
		return err
	}
	if len(attempts) == 0 {
		return errors.New("fulfilled scheduled responsibility has no runtime attempt")
	}
	attempt := attempts[len(attempts)-1]
	if attempt.Outcome != responsibility.AttemptOutcomeCompleted || attempt.TenantID != assignment.TenantID ||
		attempt.ResponsibilityID != assignment.ID || attempt.AgentProfileID != assignment.AccountableAgent || strings.TrimSpace(attempt.AgentRunID) == "" {
		return fmt.Errorf("%w: fulfilled scheduled responsibility has no matching completed runtime attempt", responsibility.ErrConflict)
	}
	run.RuntimeRunID = strings.TrimSpace(attempt.AgentRunID)
	providerSpecific := schedulerAgentRunProviderSpecific(schedulerapp.AgentTaskRequest{
		ScheduledTaskID: run.ScheduledTaskID, Delivery: run.Delivery,
	}, "")
	providerSpecific["run_source"] = schedulerAgentTaskRunSource
	providerSpecific["scheduled_run_id"] = run.ScheduledRunID
	providerSpecific["generation_id"] = run.ScheduledRunID
	providerSpecific["scheduled_trigger_type"] = strings.TrimSpace(string(run.TriggerType))
	if run.ConfigurationSnapshotVersion > 0 {
		contract, err := encodeSchedulerResultContract(run.ResultContract)
		if err != nil {
			return err
		}
		if contract != "" {
			providerSpecific[schedulerResultContractProviderKey] = contract
		}
	}
	recoveryEnvelope, err := agentcore.CloneExecutionEnvelopeForRun(run.ExecutionEnvelope, attempt.AgentRunID, time.Now().UTC())
	if err != nil {
		return fmt.Errorf("rekey recovered execution envelope: %w", err)
	}
	runtimeRun := runtimedaemon.AgentRun{
		RunID: agentcore.AgentRunID(attempt.AgentRunID), AgentID: agentcore.AgentID(run.AgentID), MissionID: run.MissionID, TaskID: run.TaskID,
		Request: agentcore.RuntimeRequest{
			TenantID:          assignment.TenantID,
			RunID:             agentcore.AgentRunID(attempt.AgentRunID),
			AgentID:           agentcore.AgentID(run.AgentID),
			ExecutionEnvelope: recoveryEnvelope,
			ContextPack: agentcore.ContextPack{ResponsibilityContext: &agentcore.ResponsibilityContext{
				ResponsibilityID: run.ResponsibilityID, MissionID: run.MissionID, TaskID: run.TaskID, AgentProfileID: run.AgentID,
			}},
			RuntimeOptions: agentcore.RuntimeOptions{ProviderSpecific: providerSpecific},
		},
	}
	committed, found, err := loadCommittedScheduledAgentResult(ctx, r.unit, r.committedResults, runtimeRun)
	if err != nil {
		return err
	}
	if !found {
		return errors.New("fulfilled scheduled responsibility has no committed task submission")
	}
	result, err := scheduledRunResultFromCommittedAgentResult(runtimeRun, committed)
	if err != nil {
		return err
	}
	result = scheduledAgentDeliveryRunResult(runtimeRun, result, agentcore.RuntimeResponse{})
	deliveryStatus, deliveryError := "not_requested", ""
	if run.ConfigurationSnapshotVersion <= 0 {
		deliveryStatus = "not_recovered"
		deliveryError = "immutable delivery configuration is unavailable for this legacy run"
	} else if strings.EqualFold(strings.TrimSpace(run.Delivery.Mode), "announce") {
		deliveryStatus = "pending"
	}
	completedAt := attempt.CompletedAt
	if completedAt.IsZero() {
		completedAt = committed.CreatedAt
	}
	return tracker.RecoverCompleted(ctx, run, result, committed.InternalTraceRef, deliveryStatus, deliveryError, completedAt)
}

func reconcileSchedulerRunStatus(ctx context.Context, tracker productionSchedulerRunTracker, run schedulerapp.ScheduledRun, status execution.JobStatus) error {
	attempt := max(1, status.Attempts+1)
	switch status.Status {
	case execution.StatusCompleted:
		if run.TargetMode == schedulerapp.TargetModeAgentTask && strings.TrimSpace(run.Result.Summary) == "" && len(run.Result.Artifacts) == 0 {
			return tracker.AttemptFailed(ctx, run.ScheduledRunID, attempt, true, errors.New("agent execution completed without a durable result"))
		}
		return tracker.Completed(ctx, run.ScheduledRunID, run.Result)
	case execution.StatusFailed, execution.StatusCancelled, execution.StatusDeadLetter:
		reason := strings.TrimSpace(status.LastError)
		if reason == "" {
			reason = strings.TrimSpace(status.FailureReason)
		}
		if reason == "" {
			reason = "scheduler execution failed"
		}
		return tracker.AttemptFailed(ctx, run.ScheduledRunID, attempt, true, errors.New(reason))
	case execution.StatusQueued, execution.StatusDispatched, execution.StatusRunning:
		return tracker.Started(ctx, run.ScheduledRunID, attempt)
	default:
		return nil
	}
}

func (r productionSchedulerRunReconciler) now() time.Time {
	if r.clock == nil {
		return time.Now().UTC()
	}
	return r.clock.Now().UTC()
}

func (t productionSchedulerRunTracker) Started(ctx context.Context, runID string, attempt int) error {
	return t.update(ctx, runID, func(run *schedulerapp.ScheduledRun) {
		incomingAttempt := max(1, attempt)
		if run.Attempt > incomingAttempt || run.Status == schedulerapp.RunStatusCompleted || run.Status == schedulerapp.RunStatusSkipped ||
			(run.Status == schedulerapp.RunStatusFailed && (run.Attempt >= incomingAttempt || scheduledRunHasResult(run.Result))) {
			return
		}
		run.Status = schedulerapp.RunStatusRunning
		run.Attempt = incomingAttempt
		run.FailureReason = ""
		if run.StartedAt.IsZero() {
			run.StartedAt = t.now()
		}
		run.CompletedAt = time.Time{}
		run.Result = schedulerapp.RunResult{}
	})
}

func (t productionSchedulerRunTracker) AttemptFailed(ctx context.Context, runID string, attempt int, terminal bool, cause error) error {
	return t.update(ctx, runID, func(run *schedulerapp.ScheduledRun) {
		incomingAttempt := max(1, attempt)
		if run.Attempt > incomingAttempt || run.Status == schedulerapp.RunStatusCompleted || run.Status == schedulerapp.RunStatusSkipped ||
			(run.Status == schedulerapp.RunStatusFailed && (run.Attempt >= incomingAttempt || scheduledRunHasResult(run.Result))) {
			return
		}
		run.Attempt = incomingAttempt
		if cause != nil {
			run.FailureReason = cause.Error()
		}
		if terminal {
			run.Status = schedulerapp.RunStatusFailed
			run.CompletedAt = t.now()
		} else {
			run.Status = schedulerapp.RunStatusRunning
			run.CompletedAt = time.Time{}
		}
		run.Result = schedulerapp.RunResult{}
	})
}

func (t productionSchedulerRunTracker) Completed(ctx context.Context, runID string, result schedulerapp.RunResult) error {
	if t.repo == nil {
		return schedulerapp.ErrServiceDependency
	}
	_, err := t.repo.MutateRun(ctx, strings.TrimSpace(runID), func(run *schedulerapp.ScheduledRun) error {
		if run.Status == schedulerapp.RunStatusSkipped {
			return nil
		}
		if run.Status == schedulerapp.RunStatusCompleted {
			if !reflect.DeepEqual(run.Result, result) {
				return fmt.Errorf("%w: scheduled run already completed with a different result", responsibility.ErrConflict)
			}
			return nil
		}
		run.Status = schedulerapp.RunStatusCompleted
		run.FailureReason = ""
		run.CompletedAt = t.now()
		run.Result = result
		return nil
	})
	return err
}

func (t productionSchedulerRunTracker) CompleteAgentRun(ctx context.Context, expected schedulerapp.ScheduledRun, result schedulerapp.RunResult, traceRef string, completedAt time.Time) error {
	if t.repo == nil {
		return schedulerapp.ErrServiceDependency
	}
	if err := validateScheduledAgentRunCompletionIdentity(expected, expected); err != nil {
		return err
	}
	_, err := t.repo.MutateRun(ctx, strings.TrimSpace(expected.ScheduledRunID), func(run *schedulerapp.ScheduledRun) error {
		if err := validateScheduledAgentRunCompletionIdentity(*run, expected); err != nil {
			return err
		}
		if err := bindScheduledRunRuntimeIdentity(run, expected.RuntimeRunID); err != nil {
			return err
		}
		switch run.Status {
		case schedulerapp.RunStatusCompleted:
			if !reflect.DeepEqual(run.Result, result) {
				return fmt.Errorf("%w: scheduled agent run already completed with a different result", responsibility.ErrConflict)
			}
			if err := mergeScheduledRunCompletionTrace(run, traceRef); err != nil {
				return err
			}
			initializeScheduledRunDeliveryStatus(run)
			return nil
		case schedulerapp.RunStatusQueued, schedulerapp.RunStatusRunning:
		default:
			return fmt.Errorf("%w: scheduled agent run is not active", responsibility.ErrConflict)
		}
		if scheduledRunHasResult(run.Result) && !reflect.DeepEqual(run.Result, result) {
			return fmt.Errorf("%w: scheduled agent run acquired a different result", responsibility.ErrConflict)
		}
		if err := mergeScheduledRunCompletionTrace(run, traceRef); err != nil {
			return err
		}
		if completedAt.IsZero() {
			completedAt = t.now()
		}
		run.Status = schedulerapp.RunStatusCompleted
		run.FailureReason = ""
		run.CompletedAt = completedAt.UTC()
		run.Result = result
		initializeScheduledRunDeliveryStatus(run)
		return nil
	})
	if errors.Is(err, schedulerapp.ErrNotFound) {
		return fmt.Errorf("%w: scheduled agent run identity no longer exists", responsibility.ErrConflict)
	}
	return err
}

func initializeScheduledRunDeliveryStatus(run *schedulerapp.ScheduledRun) {
	if run == nil || run.ConfigurationSnapshotVersion <= 0 || strings.TrimSpace(run.DeliveryStatus) != "" {
		return
	}
	if strings.EqualFold(strings.TrimSpace(run.Delivery.Mode), "announce") {
		run.DeliveryStatus = "pending"
		return
	}
	run.DeliveryStatus = "not_requested"
}

func (t productionSchedulerRunTracker) RecordDelivery(ctx context.Context, runID, status, deliveryError, externalMessageID string) error {
	if t.repo == nil {
		return schedulerapp.ErrServiceDependency
	}
	status = strings.TrimSpace(status)
	_, err := t.repo.MutateRun(ctx, strings.TrimSpace(runID), func(run *schedulerapp.ScheduledRun) error {
		if (run.Status != schedulerapp.RunStatusCompleted && (run.Status != schedulerapp.RunStatusFailed || !scheduledRunHasResult(run.Result))) || status == "" {
			return fmt.Errorf("%w: scheduled delivery status requires a terminal run with a durable result", responsibility.ErrConflict)
		}
		current := strings.TrimSpace(run.DeliveryStatus)
		if scheduledDeliveryTerminalStatus(current) {
			if current != status || strings.TrimSpace(run.DeliveryError) != strings.TrimSpace(deliveryError) ||
				strings.TrimSpace(run.ExternalMessageID) != strings.TrimSpace(externalMessageID) {
				return fmt.Errorf("%w: scheduled delivery already has terminal status %s", responsibility.ErrConflict, current)
			}
			return nil
		}
		run.DeliveryStatus = status
		run.DeliveryError = strings.TrimSpace(deliveryError)
		run.ExternalMessageID = strings.TrimSpace(externalMessageID)
		return nil
	})
	return err
}

func scheduledDeliveryTerminalStatus(status string) bool {
	switch strings.TrimSpace(status) {
	case "delivered", "failed", "dead_letter", "not_requested", "not_recovered":
		return true
	default:
		return false
	}
}

func (t productionSchedulerRunTracker) RecoverCompleted(ctx context.Context, expected schedulerapp.ScheduledRun, result schedulerapp.RunResult, traceRef, deliveryStatus, deliveryError string, completedAt time.Time) error {
	if t.repo == nil {
		return schedulerapp.ErrServiceDependency
	}
	_, err := t.repo.MutateRun(ctx, strings.TrimSpace(expected.ScheduledRunID), func(run *schedulerapp.ScheduledRun) error {
		if run.ScheduledTaskID != expected.ScheduledTaskID || run.TargetMode != expected.TargetMode ||
			run.MissionID != expected.MissionID || run.TaskID != expected.TaskID || run.ResponsibilityID != expected.ResponsibilityID || run.AgentID != expected.AgentID ||
			run.ConfigurationSnapshotVersion > 0 && (strings.TrimSpace(run.TenantID) == "" || strings.TrimSpace(run.TenantID) != strings.TrimSpace(expected.TenantID)) {
			return fmt.Errorf("%w: scheduled run identity changed during recovery", responsibility.ErrConflict)
		}
		if err := bindScheduledRunRuntimeIdentity(run, expected.RuntimeRunID); err != nil {
			return err
		}
		if run.Status == schedulerapp.RunStatusSkipped {
			return nil
		}
		if run.Status == schedulerapp.RunStatusCompleted {
			if !reflect.DeepEqual(run.Result, result) {
				return fmt.Errorf("%w: scheduled run completed with a different result during recovery", responsibility.ErrConflict)
			}
			return mergeScheduledRunCompletionTrace(run, traceRef)
		}
		if scheduledRunHasResult(run.Result) && !reflect.DeepEqual(run.Result, result) {
			return fmt.Errorf("%w: scheduled run acquired a different result during recovery", responsibility.ErrConflict)
		}
		if err := mergeScheduledRunCompletionTrace(run, traceRef); err != nil {
			return err
		}
		run.Status = schedulerapp.RunStatusCompleted
		run.FailureReason = ""
		if completedAt.IsZero() {
			completedAt = t.now()
		}
		run.CompletedAt = completedAt.UTC()
		run.Result = result
		if strings.TrimSpace(run.DeliveryStatus) == "" {
			run.DeliveryStatus = strings.TrimSpace(deliveryStatus)
			run.DeliveryError = strings.TrimSpace(deliveryError)
			run.ExternalMessageID = ""
		}
		return nil
	})
	return err
}

func (t productionSchedulerRunTracker) RecordTrace(ctx context.Context, runID, traceRef, traceStatus string) error {
	return t.update(ctx, runID, func(run *schedulerapp.ScheduledRun) {
		applyScheduledRunTrace(run, traceRef, traceStatus)
	})
}

func applyScheduledRunTrace(run *schedulerapp.ScheduledRun, traceRef, traceStatus string) {
	run.TraceRef = strings.TrimSpace(traceRef)
	if run.TraceRef == "" {
		run.TraceVersion = 0
		run.TraceAvailable = false
		run.TraceStatus = ""
		return
	}
	run.TraceVersion = 2
	run.TraceAvailable = true
	run.TraceStatus = strings.TrimSpace(traceStatus)
}

func (t productionSchedulerRunTracker) update(ctx context.Context, runID string, apply func(*schedulerapp.ScheduledRun)) error {
	if t.repo == nil {
		return schedulerapp.ErrServiceDependency
	}
	_, err := t.repo.MutateRun(ctx, strings.TrimSpace(runID), func(run *schedulerapp.ScheduledRun) error {
		apply(run)
		return nil
	})
	return err
}

func (t productionSchedulerRunTracker) now() time.Time {
	if t.clock == nil {
		return time.Now().UTC()
	}
	return t.clock.Now().UTC()
}
