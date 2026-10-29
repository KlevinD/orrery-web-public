package responsibility

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	missiontask "orrery_v2/internal/mission/task"
	"orrery_v2/internal/platform/clock"
	"orrery_v2/internal/platform/idgen"
)

type AgentProfilePort interface {
	GetProfile(ctx context.Context, id agentcore.AgentProfileID) (agentcore.AgentProfile, error)
}

type AgentProfileBatchPort interface {
	GetProfiles(ctx context.Context, ids []agentcore.AgentProfileID) (map[agentcore.AgentProfileID]agentcore.AgentProfile, error)
}

type AgentDirectoryPort interface {
	GetDirectoryEntry(ctx context.Context, id agentcore.AgentProfileID) (agentcore.AgentDirectoryEntry, error)
}

type RuntimeDispatchRequest struct {
	TenantID          string
	MissionID         string
	TaskID            string
	AgentProfileID    string
	RuntimeKind       string
	RunID             string
	Attempt           int
	IdempotencyKey    string
	PermissionScopes  []string
	Context           agentcore.ResponsibilityContext
	ExecutionEnvelope *agentcore.ExecutionEnvelope
}

func newResponsibilityExecutionEnvelope(tenantID, missionID, taskID, agentProfileID, runID, sourceKind, sourceID, sourceStepID string, scopes []string, context agentcore.ResponsibilityContext, parent *agentcore.ExecutionEnvelope, at time.Time) (*agentcore.ExecutionEnvelope, error) {
	envelope := &agentcore.ExecutionEnvelope{
		TenantID:              strings.TrimSpace(tenantID),
		RunID:                 strings.TrimSpace(runID),
		ScheduledTaskID:       strings.TrimSpace(sourceID),
		ScheduledRunID:        strings.TrimSpace(sourceStepID),
		MissionID:             strings.TrimSpace(missionID),
		TaskID:                strings.TrimSpace(taskID),
		AgentID:               strings.TrimSpace(agentProfileID),
		AgentProfileID:        strings.TrimSpace(agentProfileID),
		PrincipalID:           strings.TrimSpace(agentProfileID),
		PrincipalKind:         agentcore.ExecutionPrincipalAgent,
		PermissionScopes:      append([]string(nil), scopes...),
		ResponsibilityContext: agentcore.CloneResponsibilityContext(&context),
	}
	if parent != nil {
		if err := parent.Verify(); err != nil {
			return nil, fmt.Errorf("verify task execution envelope: %w", err)
		}
		if !parent.ExpiresAt.IsZero() && !parent.ExpiresAt.After(at.UTC()) {
			return nil, fmt.Errorf("task execution envelope has expired")
		}
		if (parent.TenantID != "" && strings.TrimSpace(parent.TenantID) != strings.TrimSpace(tenantID)) ||
			(parent.MissionID != "" && strings.TrimSpace(parent.MissionID) != strings.TrimSpace(missionID)) ||
			(parent.TaskID != "" && strings.TrimSpace(parent.TaskID) != strings.TrimSpace(taskID)) ||
			(parent.AgentID != "" && strings.TrimSpace(parent.AgentID) != strings.TrimSpace(agentProfileID)) ||
			(parent.AgentProfileID != "" && strings.TrimSpace(parent.AgentProfileID) != strings.TrimSpace(agentProfileID)) {
			return nil, fmt.Errorf("task execution envelope identity does not match responsibility assignment")
		}
		permissionScopes, err := boundedResponsibilitySelectors(parent.PermissionScopes, scopes)
		if err != nil {
			return nil, fmt.Errorf("task execution envelope permission scopes do not cover responsibility scopes")
		}
		contextCopy := agentcore.CloneResponsibilityContext(&context)
		if contextCopy == nil {
			return nil, fmt.Errorf("responsibility context is required")
		}
		contextCopy.AllowedTools, err = boundedResponsibilitySelectors(parent.AllowedToolIDs, contextCopy.AllowedTools)
		if err != nil {
			return nil, fmt.Errorf("bound responsibility tools: %w", err)
		}
		contextCopy.AllowedDataScopes, err = boundedResponsibilitySelectors(parent.DataScopes, contextCopy.AllowedDataScopes)
		if err != nil {
			return nil, fmt.Errorf("bound responsibility data scopes: %w", err)
		}
		if parent.OrganizationID != "" && contextCopy.OrganizationID != "" && parent.OrganizationID != contextCopy.OrganizationID {
			return nil, fmt.Errorf("task execution envelope organization does not match responsibility context")
		}
		if parent.OrganizationName != "" && contextCopy.OrganizationName != "" && parent.OrganizationName != contextCopy.OrganizationName {
			return nil, fmt.Errorf("task execution envelope organization name does not match responsibility context")
		}
		child := agentcore.CloneExecutionEnvelope(parent)
		child.RunID = strings.TrimSpace(runID)
		child.EnvelopeID = ""
		child.EnvelopeDigest = ""
		child.IssuedAt = time.Time{}
		child.TenantID = strings.TrimSpace(tenantID)
		child.MissionID = strings.TrimSpace(missionID)
		child.TaskID = strings.TrimSpace(taskID)
		child.AgentID = strings.TrimSpace(agentProfileID)
		child.AgentProfileID = strings.TrimSpace(agentProfileID)
		child.PermissionScopes = permissionScopes
		child.AllowedToolIDs = append([]string(nil), contextCopy.AllowedTools...)
		child.DataScopes = append([]string(nil), contextCopy.AllowedDataScopes...)
		child.ResponsibilityContext = contextCopy
		child.ResponsibilityID = strings.TrimSpace(contextCopy.ResponsibilityID)
		child.ResponsibilityVersion = contextCopy.ResponsibilityVersion
		child.PolicyID = strings.TrimSpace(contextCopy.PolicyID)
		child.PolicyVersion = contextCopy.PolicyVersion
		child.ForbiddenActions = unionResponsibilitySelectors(parent.ForbiddenActions, contextCopy.ForbiddenActions)
		if child.ExpiresAt.IsZero() || (!contextCopy.ExpiresAt.IsZero() && contextCopy.ExpiresAt.Before(child.ExpiresAt)) {
			child.ExpiresAt = contextCopy.ExpiresAt
		}
		envelope = child
	}
	if strings.TrimSpace(sourceKind) != "scheduler_agent_task" {
		envelope.ScheduledTaskID = ""
		envelope.ScheduledRunID = ""
	}
	if err := envelope.SealAt(at); err != nil {
		return nil, fmt.Errorf("seal responsibility execution envelope: %w", err)
	}
	return envelope, nil
}

func boundedResponsibilitySelectors(parent, child []string) ([]string, error) {
	if len(parent) == 0 {
		return append([]string(nil), child...), nil
	}
	if len(child) == 0 {
		return append([]string(nil), parent...), nil
	}
	result := make([]string, 0, len(child))
	for _, candidate := range child {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			continue
		}
		for _, allowed := range parent {
			allowed = strings.TrimSpace(allowed)
			if allowed == "*" || candidate == allowed || strings.HasSuffix(allowed, "*") && strings.HasPrefix(candidate, strings.TrimSuffix(allowed, "*")) || strings.HasPrefix(candidate, allowed+".") {
				result = append(result, candidate)
				break
			}
		}
	}
	if len(result) == 0 {
		return nil, fmt.Errorf("parent snapshot and child snapshot have no common selectors")
	}
	return result, nil
}

func unionResponsibilitySelectors(groups ...[]string) []string {
	seen := map[string]struct{}{}
	result := make([]string, 0)
	for _, group := range groups {
		for _, value := range group {
			value = strings.TrimSpace(value)
			if value == "" {
				continue
			}
			if _, ok := seen[value]; ok {
				continue
			}
			seen[value] = struct{}{}
			result = append(result, value)
		}
	}
	return result
}

type RuntimeDispatcher interface {
	DispatchResponsibility(ctx context.Context, request RuntimeDispatchRequest) (string, error)
}

type RuntimeOutcomeFact struct {
	TenantID string
	TaskID   string
	RunID    string
	Outcome  AttemptOutcome
	Error    string
}

type RuntimeOutcomeSource interface {
	FindTerminalRuntimeOutcome(ctx context.Context, tenantID, missionID, taskID, runID string) (RuntimeOutcomeFact, bool, error)
}

type RuntimeHealthFact struct {
	TenantID        string
	TaskID          string
	RunID           string
	LastHeartbeatAt time.Time
	LeaseExpiresAt  time.Time
	TimeoutBaseAt   time.Time
	UsageTokens     int64
	Terminal        bool
}

type RuntimeHealthSource interface {
	FindRuntimeHealth(ctx context.Context, tenantID, missionID, taskID, runID string) (RuntimeHealthFact, bool, error)
}

type RuntimeCancelRequest struct {
	TenantID  string
	MissionID string
	TaskID    string
	RunID     string
	AgentID   string
	Reason    string
}

type RuntimeController interface {
	CancelResponsibility(ctx context.Context, request RuntimeCancelRequest) error
}

type ReworkCoordinator interface {
	PrepareRework(ctx context.Context, taskID, accountableAgent string) error
	ResolveRework(ctx context.Context, taskID, accountableAgent string) error
}

type QAPlanState string

const (
	QAPlanStatePending   QAPlanState = "pending"
	QAPlanStateCompleted QAPlanState = "completed"
	QAPlanStateEscalated QAPlanState = "escalated"
)

type QAPlanCoordinator interface {
	ReconcileQAPlan(ctx context.Context, assignment AssignmentRecord, task missiontask.TaskRecord) (QAPlanState, error)
}

type QACheckState string

const (
	QACheckStatePending   QACheckState = "pending"
	QACheckStateCompleted QACheckState = "completed"
	QACheckStateEscalated QACheckState = "escalated"
)

type QACheckCoordinator interface {
	ReconcileQACheck(ctx context.Context, assignment AssignmentRecord, task missiontask.TaskRecord) (QACheckState, error)
}

func QAPlanTaskID(parentTaskID string) string {
	return "qa-plan-task-" + stableHex(strings.TrimSpace(parentTaskID))[:24]
}

func QAPlanID(parentTaskID string) missiontask.QAPlanID {
	return missiontask.QAPlanID("qa-plan-" + stableHex(strings.TrimSpace(parentTaskID))[:24])
}

func QACheckTaskID(parentTaskID, submissionID string) string {
	return "qa-check-task-" + stableHex(strings.TrimSpace(parentTaskID), strings.TrimSpace(submissionID))[:24]
}

type CoordinatorOptions struct {
	Repository         *Repository
	Service            *Service
	Profiles           AgentProfilePort
	Directory          AgentDirectoryPort
	ContextProvider    ContextProvider
	Dispatcher         RuntimeDispatcher
	RuntimeOutcomes    RuntimeOutcomeSource
	RuntimeHealth      RuntimeHealthSource
	RuntimeControl     RuntimeController
	Rework             ReworkCoordinator
	QAPlans            QAPlanCoordinator
	QAChecks           QACheckCoordinator
	Clock              clock.Clock
	IDGenerator        idgen.ErrorGenerator
	WorkerID           string
	ClaimLease         time.Duration
	MaxParallel        int
	DispatchBackoff    time.Duration
	HumanReviewEnabled func(context.Context) (bool, error)
}

type Coordinator struct {
	repository         *Repository
	service            *Service
	profiles           AgentProfilePort
	directory          AgentDirectoryPort
	contextProvider    ContextProvider
	dispatcher         RuntimeDispatcher
	runtimeOutcomes    RuntimeOutcomeSource
	runtimeHealth      RuntimeHealthSource
	runtimeControl     RuntimeController
	rework             ReworkCoordinator
	qaPlans            QAPlanCoordinator
	qaChecks           QACheckCoordinator
	clock              clock.Clock
	ids                idgen.ErrorGenerator
	workerID           string
	claimLease         time.Duration
	maxParallel        int
	dispatchBackoff    time.Duration
	humanReviewEnabled func(context.Context) (bool, error)
}

type ReconcileStats struct {
	Candidates        int
	DependencyBlocked int
	QABlocked         int
	QAEscalated       int
	CapacityBlocked   int
	PolicyBlocked     int
	BudgetBlocked     int
	RiskBlocked       int
	BlockersRechecked int
	BlockersResolved  int
	BlockersEscalated int
	AlertsOpened      int
	AlertsEscalated   int
	AlertsResolved    int
	Heartbeats        int
	TimedOut          int
	Claimed           int
	Dispatched        int
	Synchronized      int
	RecoveredOutcomes int
	Cancelled         int
	Failed            int
}

func NewCoordinator(opts CoordinatorOptions) *Coordinator {
	workerID := strings.TrimSpace(opts.WorkerID)
	if workerID == "" {
		workerID = "responsibility-coordinator"
	}
	claimLease := opts.ClaimLease
	if claimLease <= 0 {
		claimLease = 2 * time.Minute
	}
	maxParallel := opts.MaxParallel
	if maxParallel <= 0 {
		maxParallel = 2
	}
	dispatchBackoff := opts.DispatchBackoff
	if dispatchBackoff <= 0 {
		dispatchBackoff = 30 * time.Second
	}
	return &Coordinator{
		repository: opts.Repository, service: opts.Service, profiles: opts.Profiles, directory: opts.Directory, dispatcher: opts.Dispatcher,
		contextProvider: opts.ContextProvider,
		runtimeOutcomes: opts.RuntimeOutcomes, runtimeHealth: opts.RuntimeHealth, runtimeControl: opts.RuntimeControl,
		rework: opts.Rework, qaPlans: opts.QAPlans, qaChecks: opts.QAChecks,
		clock: opts.Clock, ids: opts.IDGenerator, workerID: workerID,
		claimLease: claimLease, maxParallel: maxParallel, dispatchBackoff: dispatchBackoff,
		humanReviewEnabled: opts.HumanReviewEnabled,
	}
}

func (c *Coordinator) Reconcile(ctx context.Context, limit int) (ReconcileStats, error) {
	if c == nil || c.repository == nil || c.service == nil || c.profiles == nil || c.dispatcher == nil {
		return ReconcileStats{}, ErrInvalidContract
	}
	stats := ReconcileStats{}
	errList := make([]error, 0)
	cancelled, cancelErr := c.reconcileControlCancellations(ctx, limit)
	stats.Cancelled += cancelled
	if cancelErr != nil {
		errList = append(errList, cancelErr)
	}
	alertStats, alertErr := c.reconcileSupervisionAlerts(ctx, limit)
	stats.AlertsEscalated += alertStats.AlertsEscalated
	stats.AlertsResolved += alertStats.AlertsResolved
	if alertErr != nil {
		errList = append(errList, alertErr)
	}
	blockerStats, blockerErr := c.reconcileDueBlockers(ctx, limit)
	stats.BlockersRechecked += blockerStats.BlockersRechecked
	stats.BlockersResolved += blockerStats.BlockersResolved
	stats.BlockersEscalated += blockerStats.BlockersEscalated
	if blockerErr != nil {
		errList = append(errList, blockerErr)
	}
	runtimeStats, runtimeErr := c.reconcileRuntimeAttempts(ctx, limit)
	stats.Heartbeats += runtimeStats.Heartbeats
	stats.TimedOut += runtimeStats.TimedOut
	stats.BudgetBlocked += runtimeStats.BudgetBlocked
	stats.RecoveredOutcomes += runtimeStats.RecoveredOutcomes
	stats.Failed += runtimeStats.Failed
	if runtimeErr != nil {
		errList = append(errList, runtimeErr)
	}
	assignments, err := c.repository.ListAssignmentsForReconcile(ctx, limit)
	if err != nil {
		return stats, err
	}
	for _, assignment := range assignments {
		policy, policyErr := c.repository.ResolvePolicy(ctx, assignment)
		if policyErr != nil {
			stats.Failed++
			errList = append(errList, fmt.Errorf("resolve assignment %s policy: %w", assignment.ID, policyErr))
			continue
		}
		opened, supervisionErr := c.ensureDeadlineSupervision(ctx, assignment, policy)
		stats.AlertsOpened += opened
		if supervisionErr != nil {
			stats.Failed++
			errList = append(errList, fmt.Errorf("supervise assignment %s deadline: %w", assignment.ID, supervisionErr))
			continue
		}
		if assignment.State == AssignmentStateOffered && !assignment.AcceptBy.IsZero() && !assignment.AcceptBy.After(c.now()) {
			if _, blockerErr := c.repository.OpenBlocker(ctx, OpenBlockerInput{
				TenantID: assignment.TenantID, ResponsibilityID: assignment.ID, Kind: BlockerKindCapacity,
				Needed: "the accountable Agent did not accept before accept_by", RequestedFrom: "agent:" + assignment.AccountableAgent,
				RecheckAt: c.now().Add(policy.RecheckInterval), DedupeKey: "accept-timeout",
			}, c.now()); blockerErr != nil {
				stats.Failed++
				errList = append(errList, fmt.Errorf("record assignment %s accept timeout: %w", assignment.ID, blockerErr))
			}
			continue
		}
		if assignment.State == AssignmentStateEscalated || assignment.Paused {
			continue
		}
		resolved, resolveErr := c.resolveReadyDependencyBlockers(ctx, assignment)
		if resolveErr != nil {
			stats.Failed++
			errList = append(errList, fmt.Errorf("resolve assignment %s dependency blocker: %w", assignment.ID, resolveErr))
			continue
		}
		if resolved > 0 {
			stats.BlockersResolved += resolved
			assignment, policyErr = c.repository.GetAssignment(ctx, assignment.ID)
			if policyErr != nil {
				stats.Failed++
				errList = append(errList, policyErr)
				continue
			}
		}
		advanced, recoverErr := c.service.RecoverAssignment(ctx, assignment)
		if recoverErr != nil {
			if errors.Is(recoverErr, missiontask.ErrNotFound) {
				if terminateErr := c.terminateOrphanedTaskAssignment(ctx, assignment); terminateErr != nil {
					stats.Failed++
					errList = append(errList, fmt.Errorf("terminate assignment %s for missing task: %w", assignment.ID, terminateErr))
				}
				continue
			}
			stats.Failed++
			errList = append(errList, fmt.Errorf("recover assignment %s: %w", assignment.ID, recoverErr))
			continue
		}
		if advanced {
			stats.Synchronized++
		}
		task, taskErr := c.service.Task(ctx, assignment.WorkID)
		if taskErr != nil {
			if errors.Is(taskErr, missiontask.ErrNotFound) {
				if terminateErr := c.terminateOrphanedTaskAssignment(ctx, assignment); terminateErr != nil {
					stats.Failed++
					errList = append(errList, fmt.Errorf("terminate assignment %s for missing task: %w", assignment.ID, terminateErr))
				}
				continue
			}
			stats.Failed++
			errList = append(errList, fmt.Errorf("load assignment %s task: %w", assignment.ID, taskErr))
			continue
		}
		if c.rework != nil && taskStatusResolvesRework(task.Status) {
			if resolveErr := c.rework.ResolveRework(ctx, assignment.WorkID, assignment.AccountableAgent); resolveErr != nil {
				stats.Failed++
				errList = append(errList, fmt.Errorf("resolve assignment %s rework: %w", assignment.ID, resolveErr))
				continue
			}
		}
		if task.Status != missiontask.TaskStatusInReview || !task.Contract.QACheckRequired {
			continue
		}
		if c.qaChecks == nil {
			stats.Failed++
			errList = append(errList, fmt.Errorf("reconcile assignment %s QA check: %w", assignment.ID, ErrInvalidContract))
			continue
		}
		qaState, qaErr := c.qaChecks.ReconcileQACheck(ctx, assignment, task)
		if qaErr != nil {
			stats.Failed++
			errList = append(errList, fmt.Errorf("reconcile assignment %s QA check: %w", assignment.ID, qaErr))
			continue
		}
		switch qaState {
		case QACheckStatePending:
			stats.QABlocked++
		case QACheckStateEscalated:
			if err := c.service.EscalateAssignment(ctx, assignment); err != nil {
				stats.Failed++
				errList = append(errList, fmt.Errorf("escalate assignment %s QA check: %w", assignment.ID, err))
			} else {
				stats.QAEscalated++
			}
		case QACheckStateCompleted:
			current, err := c.repository.GetAssignment(ctx, assignment.ID)
			if err != nil {
				stats.Failed++
				errList = append(errList, fmt.Errorf("reload assignment %s after QA check: %w", assignment.ID, err))
				continue
			}
			if _, err := c.service.RecoverAssignment(ctx, current); err != nil {
				stats.Failed++
				errList = append(errList, fmt.Errorf("synchronize assignment %s after QA check: %w", assignment.ID, err))
			} else {
				stats.Synchronized++
			}
		default:
			stats.Failed++
			errList = append(errList, fmt.Errorf("reconcile assignment %s QA check: %w", assignment.ID, ErrInvalidContract))
		}
	}
	activations, err := c.repository.ListReadyActivations(ctx, c.now(), limit)
	if err != nil {
		return stats, errors.Join(append(errList, err)...)
	}
	stats.Candidates = len(activations)
	for _, activation := range activations {
		if err := ctx.Err(); err != nil {
			return stats, err
		}
		result, err := c.reconcileActivation(ctx, activation)
		stats.DependencyBlocked += result.DependencyBlocked
		stats.QABlocked += result.QABlocked
		stats.QAEscalated += result.QAEscalated
		stats.CapacityBlocked += result.CapacityBlocked
		stats.PolicyBlocked += result.PolicyBlocked
		stats.BudgetBlocked += result.BudgetBlocked
		stats.RiskBlocked += result.RiskBlocked
		stats.Claimed += result.Claimed
		stats.Dispatched += result.Dispatched
		stats.Synchronized += result.Synchronized
		if err != nil {
			stats.Failed++
			errList = append(errList, fmt.Errorf("activation %s: %w", activation.ID, err))
		}
	}
	return stats, errors.Join(errList...)
}

func (c *Coordinator) reconcileSupervisionAlerts(ctx context.Context, limit int) (ReconcileStats, error) {
	stats := ReconcileStats{}
	alerts, err := c.repository.ListOpenSupervisionAlerts(ctx, limit)
	if err != nil {
		return stats, err
	}
	errList := make([]error, 0)
	for _, alert := range alerts {
		assignment, err := c.repository.GetAssignment(ctx, alert.ResponsibilityID)
		if err != nil {
			errList = append(errList, err)
			continue
		}
		if assignment.TenantID != alert.TenantID {
			errList = append(errList, ErrConflict)
			continue
		}
		policy, err := c.repository.ResolvePolicy(ctx, assignment)
		if err != nil {
			errList = append(errList, err)
			continue
		}
		resolve := assignmentTerminal(assignment.State) || assignment.DueAt.IsZero()
		if alert.Kind == SupervisionAlertDeadlineWarning && !assignment.DueAt.IsZero() {
			resolve = resolve || c.now().Before(assignment.DueAt.Add(-policy.DeadlineWarningWindow))
		}
		if alert.Kind == SupervisionAlertOverdue && !assignment.DueAt.IsZero() {
			resolve = resolve || c.now().Before(assignment.DueAt)
		}
		if resolve {
			count, resolveErr := c.repository.ResolveSupervisionAlerts(ctx, assignment, "deadline supervision condition cleared", c.now())
			stats.AlertsResolved += count
			if resolveErr != nil {
				errList = append(errList, resolveErr)
			}
			continue
		}
		if alert.Kind == SupervisionAlertOverdue {
			_, escalated, escalateErr := c.repository.EscalateSupervisionAlert(ctx, alert.ID, policy, c.now())
			if escalateErr != nil {
				errList = append(errList, escalateErr)
			} else if escalated {
				stats.AlertsEscalated++
			}
		}
	}
	return stats, errors.Join(errList...)
}

func (c *Coordinator) ensureDeadlineSupervision(ctx context.Context, assignment AssignmentRecord, policy ResponsibilityPolicyRecord) (int, error) {
	if assignment.DueAt.IsZero() || assignmentTerminal(assignment.State) {
		return 0, nil
	}
	kind := SupervisionAlertKind("")
	if !assignment.DueAt.After(c.now()) {
		kind = SupervisionAlertOverdue
	} else if policy.DeadlineWarningWindow > 0 && !assignment.DueAt.Add(-policy.DeadlineWarningWindow).After(c.now()) {
		kind = SupervisionAlertDeadlineWarning
	}
	if kind == "" {
		return 0, nil
	}
	_, created, err := c.repository.EnsureSupervisionAlert(ctx, assignment, policy, kind, c.now())
	if err != nil {
		return 0, err
	}
	if created {
		return 1, nil
	}
	return 0, nil
}

func (c *Coordinator) resolveReadyDependencyBlockers(ctx context.Context, assignment AssignmentRecord) (int, error) {
	blockers, err := c.repository.ListBlockers(ctx, assignment.TenantID, assignment.ID, true)
	if err != nil {
		return 0, err
	}
	if len(blockers) == 0 {
		return 0, nil
	}
	task, err := c.service.Task(ctx, assignment.WorkID)
	if err != nil {
		return 0, err
	}
	ready, err := c.service.DependenciesSatisfied(ctx, task)
	if err != nil || !ready {
		return 0, err
	}
	resolved := 0
	for _, blocker := range blockers {
		if blocker.Kind != BlockerKindDependency {
			continue
		}
		if _, _, err := c.repository.ResolveBlocker(ctx, blocker.TenantID, blocker.ID, c.workerID, "Mission Task dependencies are approved", "dependency-resolved:"+blocker.ID+":"+fmt.Sprint(blocker.Version), blocker.Version, ActivationCauseDependencyResolved, c.now()); err != nil {
			return resolved, err
		}
		resolved++
	}
	return resolved, nil
}

func (c *Coordinator) reconcileDueBlockers(ctx context.Context, limit int) (ReconcileStats, error) {
	stats := ReconcileStats{}
	blockers, err := c.repository.ListDueBlockers(ctx, c.now(), limit)
	if err != nil {
		return stats, err
	}
	errList := make([]error, 0)
	for _, blocker := range blockers {
		stats.BlockersRechecked++
		assignment, err := c.repository.GetAssignment(ctx, blocker.ResponsibilityID)
		if err != nil {
			errList = append(errList, err)
			continue
		}
		policy, err := c.repository.ResolvePolicy(ctx, assignment)
		if err != nil {
			errList = append(errList, err)
			continue
		}
		resolve := blocker.Kind == BlockerKindCapacity && blocker.DedupeKey != "accept-timeout"
		if blocker.Kind == BlockerKindDependency {
			task, taskErr := c.service.Task(ctx, assignment.WorkID)
			if taskErr != nil {
				errList = append(errList, taskErr)
				continue
			}
			resolve, err = c.service.DependenciesSatisfied(ctx, task)
			if err != nil {
				errList = append(errList, err)
				continue
			}
		}
		if resolve {
			if _, _, err := c.repository.ResolveBlocker(ctx, blocker.TenantID, blocker.ID, c.workerID, "supervision condition is ready for another attempt", "recheck-resolved:"+blocker.ID+":"+fmt.Sprint(blocker.Version), blocker.Version, ActivationCauseRecheckDue, c.now()); err != nil {
				errList = append(errList, err)
			} else {
				stats.BlockersResolved++
			}
			continue
		}
		_, escalated, err := c.repository.EscalateBlocker(ctx, blocker.ID, policy, c.now())
		if err != nil {
			errList = append(errList, err)
			continue
		}
		if escalated {
			stats.BlockersEscalated++
		}
	}
	return stats, errors.Join(errList...)
}

func (c *Coordinator) reconcileRuntimeHealth(ctx context.Context, assignment AssignmentRecord, policy ResponsibilityPolicyRecord) (ReconcileStats, error) {
	stats := ReconcileStats{}
	if c.runtimeHealth == nil {
		return stats, nil
	}
	attempts, err := c.repository.ListAttempts(ctx, assignment.ID)
	if err != nil {
		return stats, err
	}
	var totalUsage int64
	for _, attempt := range attempts {
		totalUsage += attempt.UsageTokens
	}
	for _, attempt := range attempts {
		if attempt.Outcome != "" {
			continue
		}
		updated, fact, found, err := c.observeRuntimeHealth(ctx, assignment, attempt)
		if err != nil {
			return stats, err
		}
		if found {
			stats.Heartbeats++
			totalUsage += updated.UsageTokens - attempt.UsageTokens
			if fact.Terminal || updated.Outcome != "" {
				continue
			}
		}

		budgetExceeded := found && policy.MaxTokenBudget > 0 && totalUsage > policy.MaxTokenBudget
		timedOut := runtimeHealthTimedOut(c.now(), updated, fact, found, policy.HeartbeatTimeout)
		if !budgetExceeded && !timedOut {
			continue
		}

		beforeRefreshUsage := updated.UsageTokens
		refreshed, refreshedFact, refreshedFound, err := c.observeRuntimeHealth(ctx, assignment, updated)
		if err != nil {
			return stats, err
		}
		if refreshedFound {
			if !found {
				stats.Heartbeats++
			}
			totalUsage += refreshed.UsageTokens - beforeRefreshUsage
			if refreshedFact.Terminal || refreshed.Outcome != "" {
				continue
			}
		}
		updated, fact, found = refreshed, refreshedFact, refreshedFound
		budgetExceeded = found && policy.MaxTokenBudget > 0 && totalUsage > policy.MaxTokenBudget
		timedOut = runtimeHealthTimedOut(c.now(), updated, fact, found, policy.HeartbeatTimeout)
		if !budgetExceeded && !timedOut {
			continue
		}
		terminal, terminalErr := c.runtimeOutcomeBecameTerminal(ctx, assignment, updated)
		if terminalErr != nil {
			return stats, terminalErr
		}
		if terminal {
			continue
		}
		if budgetExceeded {
			if err := c.cancelAttempt(ctx, assignment, updated, "responsibility token budget exceeded"); err != nil {
				if errors.Is(err, ErrRuntimeTerminal) {
					continue
				}
				return stats, err
			}
			if _, err := c.repository.OpenBlocker(ctx, OpenBlockerInput{TenantID: assignment.TenantID, ResponsibilityID: assignment.ID, Kind: BlockerKindBudget, Needed: "increase the responsibility token budget or reduce scope", RequestedFrom: "mission_owner:" + assignment.MissionID, RecheckAt: c.now().Add(policy.RecheckInterval), DedupeKey: "token-budget"}, c.now()); err != nil {
				return stats, err
			}
			stats.BudgetBlocked++
			continue
		}
		reason := "responsibility heartbeat timed out"
		if !found {
			reason = "responsibility heartbeat was never observed before timeout"
		}
		if err := c.cancelAttempt(ctx, assignment, updated, reason); err != nil {
			if errors.Is(err, ErrRuntimeTerminal) {
				continue
			}
			return stats, err
		}
		stats.TimedOut++
	}
	return stats, nil
}

func (c *Coordinator) observeRuntimeHealth(ctx context.Context, assignment AssignmentRecord, attempt AttemptRecord) (AttemptRecord, RuntimeHealthFact, bool, error) {
	fact, found, err := c.runtimeHealth.FindRuntimeHealth(ctx, assignment.TenantID, assignment.MissionID, assignment.WorkID, attempt.AgentRunID)
	if err != nil || !found {
		return attempt, RuntimeHealthFact{}, false, err
	}
	if fact.TenantID != assignment.TenantID || fact.TaskID != assignment.WorkID || fact.RunID != attempt.AgentRunID {
		return attempt, RuntimeHealthFact{}, false, ErrConflict
	}
	heartbeat := fact.LastHeartbeatAt
	if fact.Terminal && heartbeat.IsZero() {
		heartbeat = attempt.StartedAt
	}
	updated, err := c.repository.UpdateAttemptHeartbeat(ctx, fact.TenantID, fact.RunID, heartbeat, fact.UsageTokens, c.now())
	if err != nil {
		return attempt, RuntimeHealthFact{}, false, err
	}
	return updated, fact, true, nil
}

func runtimeHealthTimedOut(now time.Time, attempt AttemptRecord, fact RuntimeHealthFact, found bool, timeout time.Duration) bool {
	if !found {
		return now.Sub(attempt.StartedAt) > timeout
	}
	lastHeartbeat := attempt.HeartbeatAt
	if lastHeartbeat.IsZero() {
		lastHeartbeat = attempt.StartedAt
	}
	if fact.TimeoutBaseAt.After(lastHeartbeat) {
		lastHeartbeat = fact.TimeoutBaseAt
	}
	return now.Sub(lastHeartbeat) > timeout && (fact.LeaseExpiresAt.IsZero() || !fact.LeaseExpiresAt.After(now))
}

func (c *Coordinator) runtimeOutcomeBecameTerminal(ctx context.Context, assignment AssignmentRecord, attempt AttemptRecord) (bool, error) {
	if c.runtimeOutcomes == nil {
		return false, nil
	}
	fact, found, err := c.runtimeOutcomes.FindTerminalRuntimeOutcome(ctx, assignment.TenantID, assignment.MissionID, assignment.WorkID, attempt.AgentRunID)
	if err != nil || !found {
		return false, err
	}
	if fact.TenantID != assignment.TenantID || fact.TaskID != assignment.WorkID || fact.RunID != attempt.AgentRunID || !fact.Outcome.valid() {
		return false, ErrConflict
	}
	var cause error
	if strings.TrimSpace(fact.Error) != "" {
		cause = errors.New(fact.Error)
	}
	if err := c.service.RecordRuntimeOutcome(ctx, RuntimeOutcomeInput{
		TenantID: fact.TenantID, TaskID: fact.TaskID, RunID: fact.RunID, Outcome: fact.Outcome, Error: cause,
	}); err != nil {
		return false, err
	}
	return true, nil
}

func (c *Coordinator) cancelAttempt(ctx context.Context, assignment AssignmentRecord, attempt AttemptRecord, reason string) error {
	return c.cancelAttemptWithOutcome(ctx, assignment, attempt, AttemptOutcomeTimedOut, reason)
}

func (c *Coordinator) cancelAttemptWithOutcome(ctx context.Context, assignment AssignmentRecord, attempt AttemptRecord, outcome AttemptOutcome, reason string) error {
	if c.runtimeControl == nil {
		return fmt.Errorf("%w: runtime controller is required for supervised cancellation", ErrInvalidContract)
	}
	agentID := strings.TrimSpace(attempt.AgentProfileID)
	if agentID == "" {
		agentID = assignment.AccountableAgent
	}
	if err := c.runtimeControl.CancelResponsibility(ctx, RuntimeCancelRequest{TenantID: assignment.TenantID, MissionID: assignment.MissionID, TaskID: assignment.WorkID, RunID: attempt.AgentRunID, AgentID: agentID, Reason: reason}); err != nil {
		if errors.Is(err, ErrRuntimeTerminal) {
			_, terminalErr := c.runtimeOutcomeBecameTerminal(ctx, assignment, attempt)
			if terminalErr != nil {
				return terminalErr
			}
		}
		return err
	}
	return c.service.RecordRuntimeOutcome(ctx, RuntimeOutcomeInput{TenantID: assignment.TenantID, TaskID: assignment.WorkID, RunID: attempt.AgentRunID, Outcome: outcome, Error: errors.New(reason)})
}

func (c *Coordinator) reconcileControlCancellations(ctx context.Context, limit int) (int, error) {
	assignments, err := c.repository.listReconcileAssignments(ctx, "control-cancellations", limit, true)
	if err != nil {
		return 0, err
	}
	cancelled := 0
	errList := make([]error, 0)
	for _, assignment := range assignments {
		attempts, err := c.repository.ListAttempts(ctx, assignment.ID)
		if err != nil {
			errList = append(errList, err)
			continue
		}
		for _, attempt := range attempts {
			if attempt.Outcome != "" {
				continue
			}
			disallowed := assignment.Paused || assignmentTerminal(assignment.State) || assignment.State == AssignmentStateReassigned
			if attempt.AgentProfileID != "" && attempt.AgentProfileID != assignment.AccountableAgent {
				disallowed = true
			}
			if assignment.State == AssignmentStateOffered && attempt.AgentProfileID == "" {
				disallowed = true
			}
			if !disallowed {
				continue
			}
			if err := c.cancelAttemptWithOutcome(ctx, assignment, attempt, AttemptOutcomeCancelled, "responsibility control state no longer permits this runtime attempt"); err != nil {
				if errors.Is(err, ErrRuntimeTerminal) {
					continue
				}
				errList = append(errList, fmt.Errorf("cancel assignment %s run %s: %w", assignment.ID, attempt.AgentRunID, err))
				continue
			}
			cancelled++
		}
	}
	return cancelled, errors.Join(errList...)
}

func taskStatusResolvesRework(status missiontask.TaskStatus) bool {
	switch status {
	case missiontask.TaskStatusAccepted, missiontask.TaskStatusInProgress, missiontask.TaskStatusSubmitted,
		missiontask.TaskStatusInReview, missiontask.TaskStatusApproved:
		return true
	default:
		return false
	}
}

func (c *Coordinator) recoverTerminalRuntimeOutcomes(ctx context.Context, assignment AssignmentRecord) (int, error) {
	if c.runtimeOutcomes == nil {
		return 0, nil
	}
	attempts, err := c.repository.ListAttempts(ctx, assignment.ID)
	if err != nil {
		return 0, err
	}
	recovered := 0
	for _, attempt := range attempts {
		if attempt.Outcome != "" {
			continue
		}
		fact, found, err := c.runtimeOutcomes.FindTerminalRuntimeOutcome(ctx, assignment.TenantID, assignment.MissionID, assignment.WorkID, attempt.AgentRunID)
		if err != nil {
			return recovered, err
		}
		if !found {
			continue
		}
		if fact.TenantID != assignment.TenantID || fact.TaskID != assignment.WorkID || fact.RunID != attempt.AgentRunID || !fact.Outcome.valid() {
			return recovered, ErrConflict
		}
		var cause error
		if message := strings.TrimSpace(fact.Error); message != "" {
			cause = errors.New(message)
		}
		if err := c.service.RecordRuntimeOutcome(ctx, RuntimeOutcomeInput{
			TenantID: fact.TenantID, TaskID: fact.TaskID, RunID: fact.RunID, Outcome: fact.Outcome, Error: cause,
		}); err != nil {
			return recovered, err
		}
		recovered++
	}
	return recovered, nil
}

func (c *Coordinator) reconcileActivation(ctx context.Context, activation ActivationRecord) (ReconcileStats, error) {
	stats := ReconcileStats{}
	assignment, err := c.repository.GetAssignment(ctx, activation.ResponsibilityID)
	if err != nil {
		return stats, err
	}
	if assignment.Version != activation.AssignmentVersion || assignment.TenantID != activation.TenantID {
		return stats, nil
	}
	if assignment.State == AssignmentStateEscalated {
		if err := c.repository.SupersedeActivation(ctx, activation.ID, c.now()); err != nil {
			return stats, err
		}
		return stats, nil
	}
	if assignment.Paused {
		return stats, nil
	}
	openBlockers, err := c.repository.ListBlockers(ctx, assignment.TenantID, assignment.ID, true)
	if err != nil {
		return stats, err
	}
	if len(openBlockers) > 0 {
		for _, blocker := range openBlockers {
			switch blocker.Kind {
			case BlockerKindCapacity:
				stats.CapacityBlocked++
			case BlockerKindBudget:
				stats.BudgetBlocked++
			case BlockerKindRisk:
				stats.RiskBlocked++
			case BlockerKindDependency:
				stats.DependencyBlocked++
			default:
				stats.PolicyBlocked++
			}
		}
		return stats, nil
	}
	policy, err := c.repository.ResolvePolicy(ctx, assignment)
	if err != nil {
		return stats, err
	}
	if err := validatePolicyForAssignment(policy, assignment); err != nil {
		return stats, err
	}
	task, err := c.service.Task(ctx, assignment.WorkID)
	if err != nil {
		if errors.Is(err, missiontask.ErrNotFound) {
			return stats, c.terminateOrphanedTaskAssignment(ctx, assignment)
		}
		return stats, err
	}
	if task.Contract.MissionID.String() != assignment.MissionID || task.Contract.Assignee.AgentProfileRef == nil || task.Contract.Assignee.AgentProfileRef.ID.String() != assignment.AccountableAgent {
		return stats, ErrConflict
	}
	switch task.Status {
	case missiontask.TaskStatusApproved, missiontask.TaskStatusSubmitted, missiontask.TaskStatusInReview:
		if err := c.service.SynchronizeTask(ctx, assignment.TenantID, assignment.WorkID); err != nil {
			return stats, err
		}
		if err := c.repository.SupersedeActivation(ctx, activation.ID, c.now()); err != nil {
			return stats, err
		}
		stats.Synchronized++
		return stats, nil
	case missiontask.TaskStatusRejected, missiontask.TaskStatusReworkRequested:
		if c.rework == nil {
			return stats, ErrInvalidContract
		}
		if err := c.rework.PrepareRework(ctx, assignment.WorkID, assignment.AccountableAgent); err != nil {
			return stats, err
		}
		task, err = c.service.Task(ctx, assignment.WorkID)
		if err != nil {
			return stats, err
		}
	case missiontask.TaskStatusAssigned, missiontask.TaskStatusAccepted, missiontask.TaskStatusInProgress:
		// Ready-state checks continue below.
	default:
		return stats, nil
	}
	dependenciesReady, err := c.service.DependenciesSatisfied(ctx, task)
	if err != nil {
		return stats, err
	}
	if !dependenciesReady {
		_, openErr := c.repository.OpenBlocker(ctx, OpenBlockerInput{TenantID: assignment.TenantID, ResponsibilityID: assignment.ID, Kind: BlockerKindDependency, Needed: "all Mission Task dependencies must be approved", RequestedFrom: "system:dependency", RecheckAt: c.now().Add(policy.RecheckInterval), DedupeKey: "task-dependencies"}, c.now())
		if openErr != nil {
			return stats, openErr
		}
		stats.DependencyBlocked++
		return stats, nil
	}
	if task.Contract.QAPlanRequired && !task.Contract.QAPlanCompleted {
		if c.qaPlans == nil {
			return stats, fmt.Errorf("%w: QA plan coordinator is required for QA-enabled tasks", ErrInvalidContract)
		}
		state, err := c.qaPlans.ReconcileQAPlan(ctx, assignment, task)
		if err != nil {
			return stats, err
		}
		switch state {
		case QAPlanStatePending:
			stats.QABlocked++
			return stats, nil
		case QAPlanStateEscalated:
			if err := c.service.EscalateAssignment(ctx, assignment); err != nil {
				return stats, err
			}
			if err := c.repository.SupersedeActivation(ctx, activation.ID, c.now()); err != nil {
				return stats, err
			}
			stats.QAEscalated++
			return stats, nil
		case QAPlanStateCompleted:
			task, err = c.service.Task(ctx, assignment.WorkID)
			if err != nil {
				return stats, err
			}
			if !task.Contract.QAPlanCompleted || task.Contract.QAPlanRef == "" || task.Contract.QAPlanArtifactRef.ID == "" {
				return stats, fmt.Errorf("%w: QA plan coordinator reported completion without a durable plan", ErrConflict)
			}
		default:
			return stats, fmt.Errorf("%w: unsupported QA plan state %q", ErrInvalidContract, state)
		}
	}
	profile, err := c.profiles.GetProfile(ctx, agentcore.AgentProfileID(assignment.AccountableAgent))
	if err != nil {
		return stats, err
	}
	if profile.ID.String() == assignment.AccountableAgent && profile.AgentKind == agentcore.AgentKindHuman {
		if err := c.repository.SupersedeActivation(ctx, activation.ID, c.now()); err != nil {
			return stats, err
		}
		return stats, nil
	}
	if profile.ID.String() != assignment.AccountableAgent || !profile.Enabled || !profile.RuntimeSpec.Kind.IsValid() {
		return stats, fmt.Errorf("%w: accountable Agent profile is disabled or has no valid runtime kind", ErrInvalidContract)
	}
	if task.Contract.RequiresExplicitToolScopes() && !profile.RuntimeSpec.Kind.SupportsExplicitToolScopeFence() {
		_, openErr := c.repository.OpenBlocker(ctx, OpenBlockerInput{
			TenantID: assignment.TenantID, ResponsibilityID: assignment.ID, Kind: BlockerKindPermission,
			Needed: "the assigned runtime cannot enforce explicit task tool scopes", RequestedFrom: "system:runtime-tool-scope-fence",
			RecheckAt: c.now().Add(policy.RecheckInterval), DedupeKey: "dispatch-runtime-tool-scope-fence",
		}, c.now())
		if openErr != nil {
			return stats, openErr
		}
		stats.PolicyBlocked++
		return stats, nil
	}
	blockedKind, blockedReason, err := c.policyBlockReason(ctx, assignment, policy, profile, task.Contract.ExecutionPermissionScopes)
	if err != nil {
		return stats, err
	}
	if blockedReason != "" {
		blockerInput := OpenBlockerInput{TenantID: assignment.TenantID, ResponsibilityID: assignment.ID, Kind: blockedKind, Needed: blockedReason, RequestedFrom: "mission_owner:" + assignment.MissionID, RecheckAt: c.now().Add(policy.RecheckInterval), DedupeKey: "dispatch-policy-" + string(blockedKind)}
		if blockedKind == BlockerKindApproval {
			blockerInput.ApprovalScopeDigest = approvalScopeDigest(task.Contract.ExecutionPermissionScopes)
		}
		_, openErr := c.repository.OpenBlocker(ctx, blockerInput, c.now())
		if openErr != nil {
			return stats, openErr
		}
		switch blockedKind {
		case BlockerKindBudget:
			stats.BudgetBlocked++
		case BlockerKindRisk:
			stats.RiskBlocked++
		default:
			stats.PolicyBlocked++
		}
		return stats, nil
	}
	claimToken, err := c.claimToken(activation.ID)
	if err != nil {
		return stats, err
	}
	maxParallel := policy.MaxParallel
	if c.maxParallel > 0 && c.maxParallel < maxParallel {
		maxParallel = c.maxParallel
	}
	claimed, ok, err := c.repository.ClaimActivation(ctx, activation.ID, c.workerID, claimToken, c.now(), c.claimLease, maxParallel)
	if err != nil {
		return stats, err
	}
	if !ok {
		_, openErr := c.repository.OpenBlocker(ctx, OpenBlockerInput{TenantID: assignment.TenantID, ResponsibilityID: assignment.ID, Kind: BlockerKindCapacity, Needed: "Agent concurrency capacity must become available", RequestedFrom: "agent:" + assignment.AccountableAgent, RecheckAt: c.now().Add(policy.RecheckInterval), DedupeKey: "agent-capacity"}, c.now())
		if openErr != nil {
			return stats, openErr
		}
		stats.CapacityBlocked++
		return stats, nil
	}
	stats.Claimed++
	runID := RunIDForActivation(claimed.ID)
	contextRequest := ContextRequest{
		TenantID:          assignment.TenantID,
		ActorMemberID:     strings.TrimSpace(assignment.ActorMemberID),
		AgentProfileID:    assignment.AccountableAgent,
		MissionID:         assignment.MissionID,
		TaskID:            assignment.WorkID,
		ResponsibilityID:  assignment.ID,
		AssignmentVersion: assignment.Version,
	}
	runtimeContext := fallbackRuntimeContext(contextRequest)
	if c.contextProvider != nil && contextRequest.ActorMemberID != "" {
		runtimeContext, err = c.contextProvider.ResolveResponsibilityContext(ctx, contextRequest)
		if err != nil {
			releaseErr := c.repository.ReleaseClaim(ctx, claimed.ID, claimToken, c.now().Add(c.dispatchBackoff), err, c.now())
			return stats, errors.Join(err, releaseErr)
		}
		runtimeContext = normalizeRuntimeContext(runtimeContext, contextRequest)
	}
	runtimeContext.PolicyID = policy.ID
	runtimeContext.PolicyVersion = policy.Version
	runtimeContext.PolicyToolScopes = cleanPolicyStrings(policy.ToolScopes)
	runtimeContext.PolicyDataScopes = cleanPolicyStrings(policy.DataScopes)
	runtimeContext.TaskPermissionScopes = cleanPolicyStrings(task.Contract.ExecutionPermissionScopes)
	runtimeContext.RequireExplicitToolScopes = runtimeContext.RequireExplicitToolScopes || task.Contract.RequiresExplicitToolScopes()
	policySourceRef := fmt.Sprintf("orrery:responsibility-policy:%s:v%d", policy.ID, policy.Version)
	if !containsString(runtimeContext.SourceRefs, policySourceRef) {
		runtimeContext.SourceRefs = append(runtimeContext.SourceRefs, policySourceRef)
	}
	attempts, err := c.repository.ListAttempts(ctx, assignment.ID)
	if err != nil {
		return stats, err
	}
	nextAttempt := 1
	for _, prior := range attempts {
		if prior.Attempt >= nextAttempt {
			nextAttempt = prior.Attempt + 1
		}
	}
	executionEnvelope, envelopeErr := newResponsibilityExecutionEnvelope(assignment.TenantID, assignment.MissionID, assignment.WorkID, assignment.AccountableAgent, runID, assignment.SourceKind, assignment.SourceID, assignment.SourceStepID, task.Contract.ExecutionPermissionScopes, runtimeContext, task.Contract.ExecutionEnvelope, c.now())
	if envelopeErr != nil {
		releaseErr := c.repository.ReleaseClaim(ctx, claimed.ID, claimToken, c.now().Add(c.dispatchBackoff), envelopeErr, c.now())
		return stats, errors.Join(envelopeErr, releaseErr)
	}
	// Dispatch the same bounded snapshot that was sealed into the envelope.
	// The task snapshot may narrow the original People or fallback context.
	runtimeContext = *agentcore.CloneResponsibilityContext(executionEnvelope.ResponsibilityContext)
	dispatchedRunID, dispatchErr := c.dispatcher.DispatchResponsibility(ctx, RuntimeDispatchRequest{
		TenantID: assignment.TenantID, MissionID: assignment.MissionID, TaskID: assignment.WorkID,
		AgentProfileID: assignment.AccountableAgent, RuntimeKind: string(profile.RuntimeSpec.Kind), RunID: runID,
		Attempt:           nextAttempt,
		IdempotencyKey:    claimed.DedupeKey + "|dispatch",
		PermissionScopes:  append([]string(nil), task.Contract.ExecutionPermissionScopes...),
		Context:           runtimeContext,
		ExecutionEnvelope: executionEnvelope,
	})
	if dispatchErr != nil {
		if errors.Is(dispatchErr, ErrDispatchRejected) {
			if err := c.repository.SupersedeActivation(ctx, claimed.ID, c.now()); err != nil {
				return stats, errors.Join(dispatchErr, err)
			}
			return stats, nil
		}
		releaseErr := c.repository.ReleaseClaim(ctx, claimed.ID, claimToken, c.now().Add(c.dispatchBackoff), dispatchErr, c.now())
		return stats, errors.Join(dispatchErr, releaseErr)
	}
	if strings.TrimSpace(dispatchedRunID) != runID {
		err := fmt.Errorf("%w: dispatcher returned run %q, want %q", ErrConflict, dispatchedRunID, runID)
		releaseErr := c.repository.ReleaseClaim(ctx, claimed.ID, claimToken, c.now().Add(c.dispatchBackoff), err, c.now())
		return stats, errors.Join(err, releaseErr)
	}
	attempt, err := c.repository.MarkDispatched(ctx, claimed.ID, claimToken, runID, string(profile.RuntimeSpec.Kind), c.now())
	if err != nil {
		return stats, err
	}
	if attempt.Outcome != "" {
		if _, err := c.service.RecoverAssignment(ctx, assignment); err != nil {
			return stats, err
		}
	}
	stats.Dispatched++
	return stats, nil
}

func (c *Coordinator) terminateOrphanedTaskAssignment(ctx context.Context, assignment AssignmentRecord) error {
	_, err := c.repository.TerminateAssignment(ctx, AssignmentControl{
		TenantID: assignment.TenantID, AssignmentID: assignment.ID,
		ActorID: c.workerID, Reason: "mission task no longer exists",
		ExpectedVersion: assignment.Version, IdempotencyKey: "missing-mission-task|" + assignment.ID,
	}, c.now())
	return err
}

func (c *Coordinator) policyBlockReason(ctx context.Context, assignment AssignmentRecord, policy ResponsibilityPolicyRecord, profile agentcore.AgentProfile, requestedExecutionScopes []string) (BlockerKind, string, error) {
	if !policy.Enabled {
		return BlockerKindPermission, "responsibility policy is paused", nil
	}
	if isAutoplanExecutionAssignment(assignment) {
		authorized, err := c.repository.AutoplanDispatchAuthorized(ctx, assignment, policy, requestedExecutionScopes)
		if err != nil {
			return "", "", err
		}
		if !authorized {
			return BlockerKindApproval, "autonomous HIL plan requires its durable execution grant before runtime dispatch", nil
		}
	}
	humanReviewEnabled := true
	if c.humanReviewEnabled != nil {
		var err error
		humanReviewEnabled, err = c.humanReviewEnabled(ctx)
		if err != nil {
			return "", "", err
		}
	}
	if humanReviewEnabled && (policy.AutonomyLevel == AutonomyAdvise || policy.ApprovalPolicy == ApprovalBeforeSideEffect) {
		approved, err := c.approvalGranted(ctx, assignment, policy, requestedExecutionScopes)
		if err != nil {
			return "", "", err
		}
		if !approved {
			return BlockerKindApproval, "human approval is required before runtime execution", nil
		}
	}
	if len(policy.ToolScopes) > 0 {
		for _, scope := range requestedExecutionScopes {
			if !policyScopeAllows(policy.ToolScopes, scope) {
				return BlockerKindPermission, "task execution scope is outside the responsibility policy", nil
			}
		}
	}
	if c.directory == nil {
		if policy.MaxCostPerTask > 0 || len(policy.AllowedRiskLevels) > 0 || len(policy.DataScopes) > 0 {
			return "", "", fmt.Errorf("%w: Agent directory is required for budget, risk, and data policy", ErrInvalidContract)
		}
		return "", "", nil
	}
	entry, err := c.directory.GetDirectoryEntry(ctx, profile.ID)
	if err != nil {
		return "", "", err
	}
	if entry.AgentProfileID != profile.ID {
		return "", "", ErrConflict
	}
	availability := entry.Availability
	if availability.Status != agentcore.AvailabilityUnknown {
		if !availability.Status.IsAvailable() {
			return BlockerKindCapacity, "Agent availability is " + string(availability.Status), nil
		}
		if availability.Capacity > 0 && availability.CurrentLoad >= availability.Capacity {
			return BlockerKindCapacity, "Agent availability capacity is exhausted", nil
		}
		if !availability.AvailableFrom.IsZero() && availability.AvailableFrom.After(c.now()) {
			return BlockerKindCapacity, "Agent availability window has not started", nil
		}
		if !availability.AvailableUntil.IsZero() && !availability.AvailableUntil.After(c.now()) {
			return BlockerKindCapacity, "Agent availability window has ended", nil
		}
	}
	if policy.MaxCostPerTask > 0 && entry.CostProfile.EstimatedCostPerTask > policy.MaxCostPerTask {
		return BlockerKindBudget, fmt.Sprintf("estimated task cost %.2f exceeds budget %.2f", entry.CostProfile.EstimatedCostPerTask, policy.MaxCostPerTask), nil
	}
	if len(policy.AllowedRiskLevels) > 0 && !containsString(policy.AllowedRiskLevels, string(entry.RiskProfile.RiskLevel)) {
		return BlockerKindRisk, "Agent risk level is outside the responsibility policy", nil
	}
	if humanReviewEnabled && policy.ApprovalPolicy == ApprovalOnlyHighRisk && (entry.RiskProfile.RiskLevel == agentcore.RiskLevelHigh || entry.RiskProfile.RiskLevel == agentcore.RiskLevelCritical) {
		approved, err := c.approvalGranted(ctx, assignment, policy, requestedExecutionScopes)
		if err != nil {
			return "", "", err
		}
		if !approved {
			return BlockerKindApproval, "high-risk Agent execution requires approval", nil
		}
	}
	if len(policy.DataScopes) > 0 {
		for _, scope := range policy.DataScopes {
			if !authorityScopeAllows(entry.OrgIdentity.AuthorityScope, scope) {
				return BlockerKindPermission, "Agent data authority is outside the responsibility policy", nil
			}
		}
	}
	return "", "", nil
}

func (c *Coordinator) approvalGranted(ctx context.Context, assignment AssignmentRecord, policy ResponsibilityPolicyRecord, requestedExecutionScopes []string) (bool, error) {
	if c == nil || c.repository == nil {
		return false, ErrInvalidContract
	}
	return c.repository.ApprovalGranted(ctx, assignment, policy, requestedExecutionScopes)
}

func policyScopeAllows(granted []string, required string) bool {
	return authorityScopeAllows(strings.Join(granted, ","), required)
}

func authorityScopeAllows(granted, required string) bool {
	required = strings.TrimSpace(required)
	if required == "" {
		return true
	}
	for _, value := range strings.FieldsFunc(granted, func(r rune) bool { return r == ',' || r == ';' || r == ' ' || r == '\n' || r == '\t' }) {
		value = strings.TrimSpace(value)
		if value == "*" || value == required {
			return true
		}
		if strings.HasSuffix(value, "*") && strings.HasPrefix(required, strings.TrimSuffix(value, "*")) {
			return true
		}
	}
	return false
}

func containsString(values []string, target string) bool {
	target = strings.TrimSpace(target)
	for _, value := range values {
		if strings.TrimSpace(value) == target {
			return true
		}
	}
	return false
}

func (c *Coordinator) claimToken(activationID string) (string, error) {
	if c.ids != nil {
		id, err := c.ids.NewIDContext()
		if err != nil {
			return "", err
		}
		if strings.TrimSpace(id) != "" {
			return "responsibility-claim-" + strings.TrimSpace(id), nil
		}
	}
	return "responsibility-claim-" + stableHex(c.workerID, activationID, c.now().Format(time.RFC3339Nano))[:24], nil
}

func (c *Coordinator) now() time.Time {
	if c != nil && c.clock != nil {
		return c.clock.Now().UTC()
	}
	return time.Now().UTC()
}
