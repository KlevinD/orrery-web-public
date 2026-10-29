package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	runtimedaemon "orrery_v2/internal/agent/runtime/daemon"
	"orrery_v2/internal/app/authorization"
	"orrery_v2/internal/app/missioncontrol"
	"orrery_v2/internal/app/responsibility"
	missioncore "orrery_v2/internal/mission/core"
	missionevent "orrery_v2/internal/mission/event"
	missiontask "orrery_v2/internal/mission/task"
	"orrery_v2/internal/platform"
	"orrery_v2/internal/platform/clock"
	"orrery_v2/internal/platform/execution"
	"orrery_v2/internal/platform/uow"
)

type productionResponsibilityServices struct {
	repository *responsibility.Repository
	service    *responsibility.Service
	rework     *missiontask.ReworkService
}

func newProductionResponsibilityServices(rt *platform.Runtime, tasks missiontask.Repository, events missiontask.EventSink) productionResponsibilityServices {
	repository := responsibility.NewRepository(rt.DBRouter, rt.UnitOfWork)
	return productionResponsibilityServices{
		repository: repository,
		service: responsibility.NewService(responsibility.ServiceOptions{
			Repository: repository, UnitOfWork: rt.UnitOfWork, Tasks: tasks, Clock: rt.Clock,
			MaxAttempts: rt.Config.Queue.MaxAttempts,
		}),
		rework: missiontask.NewReworkService(rt.UnitOfWork, tasks, events, missiontask.ReworkServiceOptions{Clock: rt.Clock}),
	}
}

func (s productionResponsibilityServices) operationsService(profiles *agentcore.PlatformProfileRepository, commands *missioncontrol.CommandService, platformClock clock.Clock) *responsibility.OperationsService {
	return responsibility.NewOperationsService(responsibility.OperationsServiceOptions{
		Repository: s.repository, Service: s.service, Profiles: profiles,
		RuntimeControl:   productionResponsibilityRuntimeDispatcher{commands: commands},
		TaskReassignment: productionResponsibilityTaskReassignment{commands: commands}, Clock: platformClock,
	})
}

type productionResponsibilityTaskReassignment struct {
	commands *missioncontrol.CommandService
}

func (p productionResponsibilityTaskReassignment) ReassignTask(ctx context.Context, request responsibility.TaskReassignmentRequest) error {
	if p.commands == nil || strings.TrimSpace(request.TenantID) == "" || strings.TrimSpace(request.MissionID) == "" || strings.TrimSpace(request.TaskID) == "" || strings.TrimSpace(request.ActorID) == "" || strings.TrimSpace(request.IdempotencyKey) == "" {
		return responsibility.ErrInvalidContract
	}
	_, err := p.commands.AssignTask(ctx, missioncontrol.ViewerRef{
		ViewerID: request.ActorID, ViewerKind: missioncontrol.ViewerKindSystem, TenantID: request.TenantID,
		AuthorizationSubjectID: "responsibility-coordinator", PermissionScope: []string{"mission.write"},
	}, missioncontrol.AssignTaskCommand{
		RequestID: request.IdempotencyKey, IdempotencyKey: request.IdempotencyKey,
		MissionID: missioncore.MissionID(request.MissionID), TaskID: missioncore.TaskID(request.TaskID),
		AssigneeID: request.AgentProfileID, Reason: request.Reason,
		ResponsibilityExpectedVersion: request.ExpectedVersion,
	})
	return err
}

func (s productionResponsibilityServices) coordinator(
	rt *platform.Runtime,
	contextProvider responsibility.ContextProvider,
	profiles *agentcore.PlatformProfileRepository,
	commands *missioncontrol.CommandService,
	tasks missiontask.Repository,
	lifecycle qaPlanTaskLifecycle,
	qa *missiontask.QAService,
	refresher missionProjectionRefresher,
	eventReader missionevent.Reader,
	relations *authorization.RelationProjector,
	humanReviewEnabled func(context.Context) (bool, error),
) *responsibility.Coordinator {
	commands.SetTaskResponsibilityReviewPort(productionTaskResponsibilityReview{service: s.service})
	return responsibility.NewCoordinator(responsibility.CoordinatorOptions{
		Repository: s.repository, Service: s.service, Profiles: profiles, Directory: profiles,
		ContextProvider: contextProvider,
		Dispatcher:      productionResponsibilityRuntimeDispatcher{commands: commands},
		RuntimeOutcomes: productionResponsibilityRuntimeOutcomeSource{events: eventReader, jobs: rt.ExecutionQueue},
		RuntimeHealth: productionResponsibilityRuntimeHealthSource{
			events: eventReader, liveness: rt.ExecutionLive, jobs: rt.ExecutionQueue, clock: rt.Clock,
		},
		RuntimeControl: productionResponsibilityRuntimeDispatcher{commands: commands},
		Rework:         productionResponsibilityReworkPreparer{service: s.rework},
		QAPlans: productionQAPlanCoordinator{
			unit: rt.UnitOfWork, tasks: tasks, lifecycle: lifecycle, qa: qa,
			responsibilities: s.repository, refresher: refresher, relations: relations,
		},
		QAChecks: productionQACheckCoordinator{
			unit: rt.UnitOfWork, tasks: tasks, lifecycle: lifecycle, qa: qa,
			responsibilities: s.repository, refresher: refresher, relations: relations,
		},
		Clock: rt.Clock, IDGenerator: rt.IDGenerator,
		WorkerID: "orrery-responsibility-coordinator", ClaimLease: rt.Config.Queue.ClaimTTL,
		HumanReviewEnabled: humanReviewEnabled,
	})
}

func newProductionResponsibilityContextProvider(ctx context.Context, rt *platform.Runtime) (responsibility.ContextProvider, error) {
	if rt == nil {
		return nil, responsibility.ErrInvalidContract
	}
	cfg := rt.Config.Integrations.People
	if !cfg.Enabled {
		return nil, nil
	}
	token, err := (platformSecretResolver{provider: rt.Secrets}).ResolveSecret(ctx, cfg.TokenRef)
	if err != nil {
		return nil, fmt.Errorf("resolve People responsibility context token: %w", err)
	}
	provider := responsibility.NewHTTPContextProviderWithOptions(responsibility.HTTPContextProviderOptions{
		BaseURL: cfg.BaseURL,
		Token:   token,
	})
	if provider == nil {
		return nil, fmt.Errorf("configure People responsibility context provider: empty base URL")
	}
	return newPeopleBoundResponsibilityContextProvider(provider, cfg.AgentBindings), nil
}

type peopleBoundResponsibilityContextProvider struct {
	inner            responsibility.ContextProvider
	subjectByProfile map[string]string
}

func newPeopleBoundResponsibilityContextProvider(inner responsibility.ContextProvider, bindings map[string]string) responsibility.ContextProvider {
	if inner == nil {
		return nil
	}
	subjectByProfile := make(map[string]string, len(bindings))
	for subjectID, profileID := range bindings {
		subjectID, profileID = strings.TrimSpace(subjectID), strings.TrimSpace(profileID)
		if subjectID == "" || profileID == "" {
			continue
		}
		subjectByProfile[profileID] = subjectID
	}
	return peopleBoundResponsibilityContextProvider{inner: inner, subjectByProfile: subjectByProfile}
}

func (p peopleBoundResponsibilityContextProvider) ResolveResponsibilityContext(ctx context.Context, request responsibility.ContextRequest) (agentcore.ResponsibilityContext, error) {
	orreryMemberID := strings.TrimSpace(request.ActorMemberID)
	peopleSubjectID := strings.TrimSpace(p.subjectByProfile[strings.TrimSpace(request.AgentProfileID)])
	if peopleSubjectID == "" {
		peopleSubjectID = orreryMemberID
	}
	request.ActorMemberID = peopleSubjectID
	resolved, err := p.inner.ResolveResponsibilityContext(ctx, request)
	if err != nil {
		return agentcore.ResponsibilityContext{}, err
	}
	if peopleSubjectID != "" {
		ref := "people:subject:" + peopleSubjectID
		if !containsResponsibilitySourceRef(resolved.SourceRefs, ref) {
			resolved.SourceRefs = append(resolved.SourceRefs, ref)
		}
	}
	resolved.ActorMemberID = orreryMemberID
	return resolved, nil
}

func (p peopleBoundResponsibilityContextProvider) ResolveAutoplanGoalContext(ctx context.Context, request responsibility.ContextRequest) (responsibility.AutoplanGoalContext, error) {
	inner, ok := p.inner.(responsibility.AutoplanGoalContextProvider)
	if !ok {
		return responsibility.AutoplanGoalContext{}, fmt.Errorf("People responsibility context provider does not expose autonomous planning goals")
	}
	peopleSubjectID := strings.TrimSpace(p.subjectByProfile[strings.TrimSpace(request.AgentProfileID)])
	if peopleSubjectID == "" {
		peopleSubjectID = strings.TrimSpace(request.ActorMemberID)
	}
	request.ActorMemberID = peopleSubjectID
	return inner.ResolveAutoplanGoalContext(ctx, request)
}

func containsResponsibilitySourceRef(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

type productionResponsibilityRuntimeOutcomeSource struct {
	events missionevent.Reader
	jobs   productionResponsibilityRuntimeJobSource
}

type productionResponsibilityRuntimeJobSource interface {
	GetStatus(context.Context, string) (execution.JobStatus, bool, error)
}

type productionResponsibilityRuntimeHealthSource struct {
	events   missionevent.Reader
	liveness interface {
		SnapshotBatch(context.Context, []string) (map[string]execution.LivenessSnapshot, error)
	}
	jobs  productionResponsibilityRuntimeJobSource
	clock clock.Clock
}

func (s productionResponsibilityRuntimeHealthSource) FindRuntimeHealth(ctx context.Context, tenantID, missionID, taskID, runID string) (responsibility.RuntimeHealthFact, bool, error) {
	tenantID = strings.TrimSpace(tenantID)
	missionID = strings.TrimSpace(missionID)
	taskID = strings.TrimSpace(taskID)
	runID = strings.TrimSpace(runID)
	if s.events == nil || s.liveness == nil || s.jobs == nil || tenantID == "" || missionID == "" || taskID == "" || runID == "" {
		return responsibility.RuntimeHealthFact{}, false, responsibility.ErrInvalidContract
	}
	events, err := loadProductionMissionEvents(ctx, s.events, tenantID, missioncore.MissionID(missionID))
	if err != nil {
		return responsibility.RuntimeHealthFact{}, false, err
	}
	fact := responsibility.RuntimeHealthFact{TenantID: tenantID, TaskID: taskID, RunID: runID}
	eventFound := false
	for _, event := range events {
		if strings.TrimSpace(event.TenantID) != tenantID || event.MissionID.String() != missionID {
			return responsibility.RuntimeHealthFact{}, false, responsibility.ErrConflict
		}
		payload, ok := runtimeLifecyclePayload(event.Payload)
		if !ok || payload.TaskID.String() != taskID || payload.RunID.String() != runID {
			continue
		}
		eventFound = true
		heartbeat := payload.LastHeartbeatAt
		if heartbeat.IsZero() {
			heartbeat = event.OccurredAt
		}
		fact.LastHeartbeatAt = laterResponsibilityRuntimeTime(fact.LastHeartbeatAt, heartbeat)
		fact.LeaseExpiresAt = laterResponsibilityRuntimeTime(fact.LeaseExpiresAt, payload.LeaseExpiresAt)
		if usageTokens := totalRuntimeUsageTokens(payload.Usage); usageTokens > fact.UsageTokens {
			fact.UsageTokens = usageTokens
		}
		if event.EventType == missionevent.EventAgentRunCompleted {
			fact.Terminal = true
		}
	}
	for _, event := range events {
		terminal, ok := responsibilityRuntimeOutcomeFact(event)
		if !ok || terminal.TaskID != taskID || terminal.RunID != runID {
			continue
		}
		eventFound = true
		fact.Terminal = true
		fact.LastHeartbeatAt = laterResponsibilityRuntimeTime(fact.LastHeartbeatAt, event.OccurredAt)
	}
	if fact.Terminal {
		fact.LeaseExpiresAt = time.Time{}
		return fact, eventFound, nil
	}

	job, jobFound, err := findProductionResponsibilityRuntimeJob(ctx, s.jobs, tenantID, missionID, taskID, runID)
	if err != nil {
		return responsibility.RuntimeHealthFact{}, false, err
	}
	queueFound := false
	if jobFound {
		switch job.Status {
		case execution.StatusQueued:
			queueFound = true
			fact.LastHeartbeatAt = laterResponsibilityRuntimeTime(fact.LastHeartbeatAt, laterResponsibilityRuntimeTime(job.CreatedAt, job.UpdatedAt))
			fact.TimeoutBaseAt = laterResponsibilityRuntimeTime(fact.TimeoutBaseAt, job.NextAttemptAt)
		case execution.StatusDispatched, execution.StatusRunning:
			queueFound = true
			fact.LastHeartbeatAt = laterResponsibilityRuntimeTime(fact.LastHeartbeatAt, responsibilityRuntimeActiveTime(job, s.now()))
			fact.LeaseExpiresAt = laterResponsibilityRuntimeTime(fact.LeaseExpiresAt, job.LeaseUntil)
		case execution.StatusCompleted, execution.StatusFailed, execution.StatusCancelled, execution.StatusDeadLetter:
			queueFound = true
			fact.Terminal = true
			fact.LastHeartbeatAt = laterResponsibilityRuntimeTime(fact.LastHeartbeatAt, responsibilityRuntimeTerminalTime(job))
			// Queue terminality prevents another cancel; lifecycle recovery remains authoritative for the outcome.
		default:
			return responsibility.RuntimeHealthFact{}, false, responsibility.ErrConflict
		}
	}
	if fact.Terminal {
		fact.LeaseExpiresAt = time.Time{}
		return fact, eventFound || queueFound, nil
	}

	runtimeID := "agent-run:" + runID
	snapshots, err := s.liveness.SnapshotBatch(ctx, []string{runtimeID})
	if err != nil {
		return responsibility.RuntimeHealthFact{}, false, err
	}
	if snapshot, ok := snapshots[runtimeID]; ok && (eventFound || queueFound) {
		if strings.TrimSpace(snapshot.RuntimeID) != runtimeID {
			return responsibility.RuntimeHealthFact{}, false, responsibility.ErrConflict
		}
		fact.LastHeartbeatAt = laterResponsibilityRuntimeTime(fact.LastHeartbeatAt, snapshot.LastSeenAt)
		fact.LeaseExpiresAt = laterResponsibilityRuntimeTime(fact.LeaseExpiresAt, snapshot.ExpiresAt)
	}
	return fact, eventFound || queueFound, nil
}

func responsibilityRuntimeTerminalTime(job execution.JobStatus) time.Time {
	for _, candidate := range []time.Time{job.CompletedAt, job.UpdatedAt, job.StartedAt, job.DispatchedAt, job.CreatedAt} {
		if !candidate.IsZero() {
			return candidate.UTC()
		}
	}
	return time.Time{}
}

func responsibilityRuntimeStartTime(job execution.JobStatus) time.Time {
	for _, candidate := range []time.Time{job.StartedAt, job.DispatchedAt, job.CreatedAt} {
		if !candidate.IsZero() {
			return candidate.UTC()
		}
	}
	return time.Time{}
}

func responsibilityRuntimeActiveTime(job execution.JobStatus, observedAt time.Time) time.Time {
	activeAt := responsibilityRuntimeStartTime(job)
	// Automatic renewals write the renewal time, while explicit extensions write
	// the future lease deadline into UpdatedAt. Only the former is a heartbeat.
	if !job.UpdatedAt.After(observedAt) && (job.LeaseUntil.IsZero() || job.UpdatedAt.Before(job.LeaseUntil)) {
		activeAt = laterResponsibilityRuntimeTime(activeAt, job.UpdatedAt)
	}
	return activeAt
}

func (s productionResponsibilityRuntimeHealthSource) now() time.Time {
	if s.clock == nil {
		return time.Now().UTC()
	}
	return s.clock.Now().UTC()
}

func laterResponsibilityRuntimeTime(current, candidate time.Time) time.Time {
	if candidate.After(current) {
		return candidate.UTC()
	}
	return current
}

func findProductionResponsibilityRuntimeJob(ctx context.Context, jobs productionResponsibilityRuntimeJobSource, tenantID, missionID, taskID, runID string) (execution.JobStatus, bool, error) {
	job, found, err := jobs.GetStatus(ctx, runID)
	if err != nil || !found {
		return execution.JobStatus{}, found, err
	}
	if strings.TrimSpace(job.ID) != runID || strings.TrimSpace(job.Topic) != runtimedaemon.AgentRunTopic {
		return execution.JobStatus{}, false, responsibility.ErrConflict
	}
	queuedRun, err := runtimedaemon.DecodeAgentRunPayload(job.Payload)
	if err != nil {
		return execution.JobStatus{}, false, fmt.Errorf("decode responsibility runtime queue payload: %w", err)
	}
	if strings.TrimSpace(queuedRun.RunID.String()) != runID || strings.TrimSpace(queuedRun.Request.TenantID) != tenantID ||
		missionIDFromRun(queuedRun).String() != missionID || taskIDFromRun(queuedRun).String() != taskID {
		return execution.JobStatus{}, false, responsibility.ErrConflict
	}
	return job, true, nil
}

func runtimeLifecyclePayload(value any) (missionevent.RuntimeLifecyclePayload, bool) {
	switch payload := value.(type) {
	case missionevent.RuntimeLifecyclePayload:
		return payload, true
	case *missionevent.RuntimeLifecyclePayload:
		if payload != nil {
			return *payload, true
		}
	}
	return missionevent.RuntimeLifecyclePayload{}, false
}

func totalRuntimeUsageTokens(usage map[string]agentcore.TokenUsage) int64 {
	var total int64
	for _, item := range usage {
		total += item.InputTokens + item.OutputTokens + item.CacheReadTokens + item.CacheWriteTokens
	}
	return total
}

func (s productionResponsibilityRuntimeOutcomeSource) FindTerminalRuntimeOutcome(ctx context.Context, tenantID, missionID, taskID, runID string) (responsibility.RuntimeOutcomeFact, bool, error) {
	tenantID = strings.TrimSpace(tenantID)
	missionID = strings.TrimSpace(missionID)
	taskID = strings.TrimSpace(taskID)
	runID = strings.TrimSpace(runID)
	if s.events == nil || tenantID == "" || missionID == "" || taskID == "" || runID == "" {
		return responsibility.RuntimeOutcomeFact{}, false, responsibility.ErrInvalidContract
	}
	events, err := loadProductionMissionEvents(ctx, s.events, tenantID, missioncore.MissionID(missionID))
	if err != nil {
		return responsibility.RuntimeOutcomeFact{}, false, err
	}
	var matched responsibility.RuntimeOutcomeFact
	found := false
	for _, event := range events {
		if strings.TrimSpace(event.TenantID) != tenantID || event.MissionID.String() != missionID {
			return responsibility.RuntimeOutcomeFact{}, false, responsibility.ErrConflict
		}
		fact, terminal := responsibilityRuntimeOutcomeFact(event)
		if !terminal || fact.TaskID != taskID || fact.RunID != runID {
			continue
		}
		fact.TenantID = tenantID
		if found && matched.Outcome != fact.Outcome {
			return responsibility.RuntimeOutcomeFact{}, false, responsibility.ErrConflict
		}
		if !found {
			matched = fact
			found = true
		}
	}
	if found {
		return matched, true, nil
	}
	if s.jobs == nil {
		return responsibility.RuntimeOutcomeFact{}, false, responsibility.ErrInvalidContract
	}
	job, jobFound, err := findProductionResponsibilityRuntimeJob(ctx, s.jobs, tenantID, missionID, taskID, runID)
	if err != nil || !jobFound {
		return responsibility.RuntimeOutcomeFact{}, false, err
	}
	fact := responsibility.RuntimeOutcomeFact{TenantID: tenantID, TaskID: taskID, RunID: runID}
	switch job.Status {
	case execution.StatusFailed, execution.StatusDeadLetter:
		fact.Outcome = responsibility.AttemptOutcomeFailed
		fact.Error = responsibilityRuntimeQueueError(job)
		return fact, true, nil
	case execution.StatusCancelled:
		fact.Outcome = responsibility.AttemptOutcomeCancelled
		fact.Error = responsibilityRuntimeQueueCancellationError(job)
		return fact, true, nil
	case execution.StatusQueued, execution.StatusDispatched, execution.StatusRunning, execution.StatusCompleted:
		return responsibility.RuntimeOutcomeFact{}, false, nil
	default:
		return responsibility.RuntimeOutcomeFact{}, false, responsibility.ErrConflict
	}
}

func responsibilityRuntimeQueueError(job execution.JobStatus) string {
	if message := strings.TrimSpace(job.LastError); message != "" {
		return message
	}
	return strings.TrimSpace(job.FailureReason)
}

func responsibilityRuntimeQueueCancellationError(job execution.JobStatus) string {
	if message := strings.TrimSpace(job.FailureReason); message != "" {
		return message
	}
	return strings.TrimSpace(job.LastError)
}

func responsibilityRuntimeOutcomeFact(event missionevent.Envelope) (responsibility.RuntimeOutcomeFact, bool) {
	switch payload := event.Payload.(type) {
	case missionevent.RuntimeLifecyclePayload:
		if event.EventType == missionevent.EventAgentRunCompleted {
			return responsibility.RuntimeOutcomeFact{TaskID: payload.TaskID.String(), RunID: payload.RunID.String(), Outcome: responsibility.AttemptOutcomeCompleted}, true
		}
	case *missionevent.RuntimeLifecyclePayload:
		if payload != nil && event.EventType == missionevent.EventAgentRunCompleted {
			return responsibility.RuntimeOutcomeFact{TaskID: payload.TaskID.String(), RunID: payload.RunID.String(), Outcome: responsibility.AttemptOutcomeCompleted}, true
		}
	case missionevent.RuntimeFailurePayload:
		if outcome, ok := responsibilityRuntimeFailureOutcome(event.EventType); ok {
			return responsibility.RuntimeOutcomeFact{TaskID: payload.TaskID.String(), RunID: payload.RunID.String(), Outcome: outcome, Error: payload.Message}, true
		}
	case *missionevent.RuntimeFailurePayload:
		if outcome, ok := responsibilityRuntimeFailureOutcome(event.EventType); payload != nil && ok {
			return responsibility.RuntimeOutcomeFact{TaskID: payload.TaskID.String(), RunID: payload.RunID.String(), Outcome: outcome, Error: payload.Message}, true
		}
	}
	return responsibility.RuntimeOutcomeFact{}, false
}

func responsibilityRuntimeFailureOutcome(eventType missionevent.EventType) (responsibility.AttemptOutcome, bool) {
	switch eventType {
	case missionevent.EventAgentRunFailed:
		return responsibility.AttemptOutcomeFailed, true
	case missionevent.EventAgentRunTimedOut:
		return responsibility.AttemptOutcomeTimedOut, true
	case missionevent.EventAgentRunCancelled:
		return responsibility.AttemptOutcomeCancelled, true
	case missionevent.EventAgentRunBlocked:
		return responsibility.AttemptOutcomeBlocked, true
	default:
		return "", false
	}
}

type productionTaskResponsibilityAssignment struct {
	service *responsibility.Service
}

func (a productionTaskResponsibilityAssignment) RecordTaskAssignment(ctx context.Context, tx uow.Tx, signal missioncontrol.TaskResponsibilityAssignmentSignal) error {
	if a.service == nil {
		return responsibility.ErrInvalidContract
	}
	_, _, err := a.service.AssignMissionTaskTx(ctx, tx, responsibility.MissionTaskAssignmentInput{
		TenantID: signal.TenantID, MissionID: signal.MissionID, TaskID: signal.TaskID,
		SourceKind: signal.SourceKind, SourceID: signal.SourceID, SourceStepID: signal.SourceStepID,
		ActorMemberID:  signal.ActorMemberID,
		AgentProfileID: signal.AgentProfileID, Priority: signal.Priority, DueAt: signal.DueAt,
		PolicyRef: "mission-task-default", ActivationCause: responsibility.ActivationCauseAssigned,
		ActorID: signal.ActorID, Reason: signal.Reason, ExpectedAssignmentVersion: signal.ExpectedResponsibilityVersion,
	})
	return err
}

func (a productionTaskResponsibilityAssignment) RecordHistoricalTaskAssignment(ctx context.Context, tx uow.Tx, signal missioncontrol.HistoricalTaskResponsibilityAssignmentSignal) error {
	if a.service == nil {
		return responsibility.ErrInvalidContract
	}
	_, err := a.service.ImportHistoricalMissionTaskTx(ctx, tx, responsibility.HistoricalMissionTaskAssignmentInput{
		TenantID: signal.TenantID, MissionID: signal.MissionID, TaskID: signal.TaskID,
		SourceKind: signal.SourceKind, SourceID: signal.SourceID, SourceStepID: signal.SourceStepID,
		AgentProfileID: signal.AgentProfileID, State: responsibility.AssignmentState(signal.State),
		Priority: signal.Priority, DueAt: signal.DueAt, ActorID: signal.ActorID, Reason: signal.Reason,
	}, signal.OccurredAt)
	return err
}

type productionTaskResponsibilityReview struct {
	service *responsibility.Service
}

func (o productionTaskResponsibilityReview) ObserveTaskReview(ctx context.Context, signal missioncontrol.TaskResponsibilityReviewSignal) error {
	if o.service == nil {
		return responsibility.ErrInvalidContract
	}
	return o.service.ObserveTaskReview(ctx, responsibility.TaskReviewSignal{
		TenantID: signal.TenantID, TaskID: signal.TaskID, Approved: signal.Approved,
	})
}

type productionResponsibilityRuntimeDispatcher struct {
	commands *missioncontrol.CommandService
}

func (d productionResponsibilityRuntimeDispatcher) DispatchResponsibility(ctx context.Context, request responsibility.RuntimeDispatchRequest) (string, error) {
	if d.commands == nil {
		return "", responsibility.ErrInvalidContract
	}
	receipt, err := d.commands.DispatchResponsibilityRun(ctx, missioncontrol.ViewerRef{
		ViewerID: request.AgentProfileID, ViewerKind: missioncontrol.ViewerKindAgent, TenantID: request.TenantID,
		AuthorizationSubjectID: request.AgentProfileID, PermissionScope: []string{"mission.read"},
	}, missioncontrol.StartAgentRunCommand{
		RequestID: request.IdempotencyKey, IdempotencyKey: request.IdempotencyKey,
		MissionID: missioncore.MissionID(request.MissionID), TaskID: missioncore.TaskID(request.TaskID),
		AgentID: request.AgentProfileID, RuntimeKind: request.RuntimeKind, RunID: request.RunID,
		Attempt:               request.Attempt,
		PermissionScope:       append([]string(nil), request.PermissionScopes...),
		ResponsibilityContext: agentcore.CloneResponsibilityContext(&request.Context),
		ExecutionEnvelope:     agentcore.CloneExecutionEnvelope(request.ExecutionEnvelope),
	})
	if err != nil {
		if errors.Is(err, missioncontrol.ErrInvalidRequest) || errors.Is(err, missioncontrol.ErrAgentRunAlreadyActive) {
			return "", fmt.Errorf("%w: %v", responsibility.ErrDispatchRejected, err)
		}
		return "", err
	}
	return receipt.CommandID, nil
}

func (d productionResponsibilityRuntimeDispatcher) CancelResponsibility(ctx context.Context, request responsibility.RuntimeCancelRequest) error {
	if d.commands == nil || strings.TrimSpace(request.TenantID) == "" || strings.TrimSpace(request.MissionID) == "" || strings.TrimSpace(request.RunID) == "" {
		return responsibility.ErrInvalidContract
	}
	key := "responsibility-cancel-" + strings.TrimSpace(request.RunID)
	_, err := d.commands.CancelAgentRun(ctx, missioncontrol.ViewerRef{
		ViewerID: "responsibility-coordinator", ViewerKind: missioncontrol.ViewerKindSystem, TenantID: request.TenantID,
		AuthorizationSubjectID: "responsibility-coordinator", PermissionScope: []string{"mission.write"},
	}, missioncontrol.CancelAgentRunCommand{
		RequestID: key, IdempotencyKey: key, MissionID: missioncore.MissionID(request.MissionID),
		TaskID: missioncore.TaskID(request.TaskID), AgentID: agentcore.AgentID(request.AgentID),
		RunID: agentcore.AgentRunID(request.RunID), Reason: request.Reason,
	})
	if errors.Is(err, missioncontrol.ErrAgentRunTerminal) {
		return responsibility.ErrRuntimeTerminal
	}
	if errors.Is(err, missioncontrol.ErrAgentRunMissing) {
		return nil
	}
	return err
}

type productionResponsibilityReworkPreparer struct {
	service *missiontask.ReworkService
}

func (p productionResponsibilityReworkPreparer) PrepareRework(ctx context.Context, taskID, accountableAgent string) error {
	if p.service == nil || strings.TrimSpace(taskID) == "" || strings.TrimSpace(accountableAgent) == "" {
		return responsibility.ErrInvalidContract
	}
	_, err := p.service.ReassignOpenRework(ctx, missiontask.TaskID(taskID), agentcore.AgentID(accountableAgent))
	return err
}

func (p productionResponsibilityReworkPreparer) ResolveRework(ctx context.Context, taskID, accountableAgent string) error {
	if p.service == nil || strings.TrimSpace(taskID) == "" || strings.TrimSpace(accountableAgent) == "" {
		return responsibility.ErrInvalidContract
	}
	_, err := p.service.ResolveRework(ctx, missiontask.TaskID(taskID), agentcore.AgentID(accountableAgent))
	return err
}

type productionResponsibilityRuntimeLifecycle struct {
	service *responsibility.Service
}

func (l productionResponsibilityRuntimeLifecycle) Completed(ctx context.Context, tenantID, taskID, runID string) error {
	return l.CompletedWithTrace(ctx, tenantID, taskID, runID, "")
}

func (l productionResponsibilityRuntimeLifecycle) CompletedWithTrace(ctx context.Context, tenantID, taskID, runID, traceRef string) error {
	return l.CompletedWithTraceAndUsage(ctx, tenantID, taskID, runID, traceRef, 0)
}

func (l productionResponsibilityRuntimeLifecycle) CompletedWithTraceAndUsage(ctx context.Context, tenantID, taskID, runID, traceRef string, usageTokens int64) error {
	if l.service == nil {
		return responsibility.ErrInvalidContract
	}
	return l.service.RecordRuntimeOutcome(ctx, responsibility.RuntimeOutcomeInput{
		TenantID: tenantID, TaskID: taskID, RunID: runID, Outcome: responsibility.AttemptOutcomeCompleted, TraceRef: traceRef, UsageTokens: usageTokens,
	})
}

func (l productionResponsibilityRuntimeLifecycle) Failed(ctx context.Context, tenantID, taskID, runID string, kind runtimedaemon.RuntimeLifecycleKind, cause error) error {
	return l.FailedWithTrace(ctx, tenantID, taskID, runID, kind, cause, "")
}

func (l productionResponsibilityRuntimeLifecycle) FailedWithTrace(ctx context.Context, tenantID, taskID, runID string, kind runtimedaemon.RuntimeLifecycleKind, cause error, traceRef string) error {
	return l.FailedWithTraceAndUsage(ctx, tenantID, taskID, runID, kind, cause, traceRef, 0)
}

func (l productionResponsibilityRuntimeLifecycle) FailedWithTraceAndUsage(ctx context.Context, tenantID, taskID, runID string, kind runtimedaemon.RuntimeLifecycleKind, cause error, traceRef string, usageTokens int64) error {
	if l.service == nil {
		return responsibility.ErrInvalidContract
	}
	outcome, ok := responsibilityOutcomeFromRuntimeLifecycle(kind)
	if !ok {
		return responsibility.ErrInvalidContract
	}
	return l.service.RecordRuntimeOutcome(ctx, responsibility.RuntimeOutcomeInput{
		TenantID: tenantID, TaskID: taskID, RunID: runID, Outcome: outcome, Error: cause, TraceRef: traceRef, UsageTokens: usageTokens,
	})
}

func responsibilityOutcomeFromRuntimeLifecycle(kind runtimedaemon.RuntimeLifecycleKind) (responsibility.AttemptOutcome, bool) {
	switch kind {
	case runtimedaemon.RuntimeLifecycleFailed:
		return responsibility.AttemptOutcomeFailed, true
	case runtimedaemon.RuntimeLifecycleTimedOut:
		return responsibility.AttemptOutcomeTimedOut, true
	case runtimedaemon.RuntimeLifecycleCancelled:
		return responsibility.AttemptOutcomeCancelled, true
	case runtimedaemon.RuntimeLifecycleBlocked:
		return responsibility.AttemptOutcomeBlocked, true
	default:
		return "", false
	}
}
