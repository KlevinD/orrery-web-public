package main

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/responsibility"
	schedulerapp "orrery_v2/internal/app/scheduler"
)

type productionSchedulerResponsibilityPort struct {
	coordinator responsibilityCoordinatorPort
	people      peopleResponsibilityReconcilerPort
	repository  responsibilityEvaluationRepository
	autoplan    responsibilityAutoplanSchedulerPort
	profiles    responsibility.AgentProfilePort
	batchSize   int
}

func newProductionSchedulerResponsibilityPort(coordinator responsibilityCoordinatorPort, repository responsibilityEvaluationRepository, profiles responsibility.AgentProfilePort, batchSize int) *productionSchedulerResponsibilityPort {
	return &productionSchedulerResponsibilityPort{coordinator: coordinator, repository: repository, profiles: profiles, batchSize: batchSize}
}

type responsibilityCoordinatorPort interface {
	Reconcile(context.Context, int) (responsibility.ReconcileStats, error)
}

type peopleResponsibilityReconcilerPort interface {
	Reconcile(context.Context) (responsibility.PeopleResponsibilityReconcileStats, error)
}

type responsibilityEvaluationRepository interface {
	GetPeopleResponsibilityPortfolioByBindingID(context.Context, string) (responsibility.PeopleResponsibilityPortfolio, bool, error)
	GetPeopleResponsibilityPortfolioVersion(context.Context, string) (responsibility.PeopleResponsibilityPortfolioVersion, bool, error)
	ListActivePeopleResponsibilityPortfoliosPage(context.Context, string, string, string, int) (responsibility.PeopleResponsibilityPortfolioPage, error)
	ListAssignments(context.Context, responsibility.AssignmentListQuery) ([]responsibility.AssignmentRecord, error)
	ListAssignmentsForAgents(context.Context, string, []string, []responsibility.AssignmentState) (map[string][]responsibility.AssignmentRecord, error)
}

const maxResponsibilityEvaluationBatchSize = 1000

func (p *productionSchedulerResponsibilityPort) DiscoverResponsibilityEvaluations(
	ctx context.Context,
	request schedulerapp.ResponsibilityEvaluationBatchRequest,
) ([]schedulerapp.ResponsibilityEvaluationBatchTarget, error) {
	if p == nil || p.repository == nil {
		return nil, schedulerapp.ErrServiceDependency
	}
	request.TenantID = strings.TrimSpace(request.TenantID)
	request.PeopleTenantID = strings.TrimSpace(request.PeopleTenantID)
	if request.TenantID == "" || request.PeopleTenantID == "" ||
		(strings.TrimSpace(request.ScheduledRunID) == "" && strings.TrimSpace(request.EvaluationID) == "") {
		return nil, schedulerapp.ErrInvalidRequest
	}
	const pageSize = 100
	cursor := ""
	targets := make([]schedulerapp.ResponsibilityEvaluationBatchTarget, 0)
	for {
		page, err := p.repository.ListActivePeopleResponsibilityPortfoliosPage(ctx, request.TenantID, request.PeopleTenantID, cursor, pageSize)
		if err != nil {
			return nil, err
		}
		for _, portfolio := range page.Items {
			targets = append(targets, schedulerapp.ResponsibilityEvaluationBatchTarget{
				TenantID: portfolio.TenantID, AgentProfileID: portfolio.AgentProfileID, PortfolioID: portfolio.ID,
				PortfolioBindingID: portfolio.BindingID,
				PortfolioVersionID: responsibility.PeopleResponsibilityPortfolioVersionID(portfolio.BindingID, portfolio.SourceDigest),
				ApprovalAssigneeID: p.approvalAssignee(ctx, portfolio.AgentProfileID),
			})
			if len(targets) > maxResponsibilityEvaluationBatchSize {
				return nil, fmt.Errorf("%w: active People responsibility portfolios exceed the batch limit of %d", schedulerapp.ErrInvalidTarget, maxResponsibilityEvaluationBatchSize)
			}
		}
		if !page.HasMore {
			break
		}
		if page.NextCursor == "" || page.NextCursor == cursor {
			return nil, schedulerapp.ErrServiceDependency
		}
		cursor = page.NextCursor
	}
	return targets, nil
}

func (p *productionSchedulerResponsibilityPort) approvalAssignee(ctx context.Context, agentProfileID string) string {
	if p == nil || p.profiles == nil {
		return ""
	}
	profile, err := p.profiles.GetProfile(ctx, agentcore.AgentProfileID(strings.TrimSpace(agentProfileID)))
	if err != nil {
		return ""
	}
	managerID := profile.OrgIdentity.ManagerAgentRef.ID
	if managerID == "" {
		return ""
	}
	manager, err := p.profiles.GetProfile(ctx, managerID)
	if err != nil || !manager.Enabled || manager.AgentKind != agentcore.AgentKindHuman {
		return ""
	}
	return strings.TrimSpace(manager.ID.String())
}

func (p *productionSchedulerResponsibilityPort) EvaluateResponsibilityBatch(
	ctx context.Context,
	request schedulerapp.ResponsibilityEvaluationBatchRequest,
	targets []schedulerapp.ResponsibilityEvaluationBatchTarget,
) ([]schedulerapp.ResponsibilityEvaluationBatchDecision, error) {
	if p == nil || p.repository == nil {
		return nil, schedulerapp.ErrServiceDependency
	}
	agentIDs := make([]string, 0, len(targets))
	seen := make(map[string]struct{}, len(targets))
	for _, target := range targets {
		agentID := strings.TrimSpace(target.AgentProfileID)
		if agentID == "" || strings.TrimSpace(target.TenantID) == "" || strings.TrimSpace(target.PortfolioBindingID) == "" ||
			strings.TrimSpace(target.PortfolioVersionID) == "" || target.TenantID != strings.TrimSpace(request.TenantID) {
			return nil, schedulerapp.ErrInvalidRequest
		}
		if _, ok := seen[agentID]; !ok {
			seen[agentID] = struct{}{}
			agentIDs = append(agentIDs, agentID)
		}
	}
	assignments, err := p.repository.ListAssignmentsForAgents(ctx, strings.TrimSpace(request.TenantID), agentIDs, responsibilityEvaluationAssignmentStates())
	if err != nil {
		return nil, err
	}
	batchPort := *p
	batchPort.repository = preloadedResponsibilityEvaluationRepository{base: p.repository, assignments: assignments}
	type indexedTarget struct {
		index  int
		target schedulerapp.ResponsibilityEvaluationBatchTarget
	}
	grouped := make(map[string][]indexedTarget, len(agentIDs))
	for index, target := range targets {
		grouped[target.AgentProfileID] = append(grouped[target.AgentProfileID], indexedTarget{index: index, target: target})
	}
	sort.Strings(agentIDs)
	groups := make(chan []indexedTarget)
	out := make([]schedulerapp.ResponsibilityEvaluationBatchDecision, len(targets))
	workerCount := min(4, len(agentIDs))
	var workers sync.WaitGroup
	workers.Add(workerCount)
	for range workerCount {
		go func() {
			defer workers.Done()
			for group := range groups {
				for _, indexed := range group {
					if err := ctx.Err(); err != nil {
						now := time.Now().UTC()
						out[indexed.index] = schedulerapp.ResponsibilityEvaluationBatchDecision{
							Target: indexed.target, Err: err, StartedAt: now, CompletedAt: now,
						}
						continue
					}
					startedAt := time.Now().UTC()
					decision, evaluateErr := batchPort.EvaluateResponsibility(ctx, schedulerapp.ResponsibilityEvaluationRequest{
						TenantID: indexed.target.TenantID, ScheduledTaskID: request.ScheduledTaskID, ScheduledRunID: request.ScheduledRunID,
						AgentProfileID: indexed.target.AgentProfileID, PortfolioBindingID: indexed.target.PortfolioBindingID,
						PortfolioVersionID: indexed.target.PortfolioVersionID,
						ApprovalAssigneeID: indexed.target.ApprovalAssigneeID, TriggeredAt: request.TriggeredAt,
					})
					out[indexed.index] = schedulerapp.ResponsibilityEvaluationBatchDecision{
						Target: indexed.target, Decision: decision, Err: evaluateErr,
						StartedAt: startedAt, CompletedAt: time.Now().UTC(),
					}
				}
			}
		}()
	}
	for _, agentID := range agentIDs {
		groups <- grouped[agentID]
	}
	close(groups)
	workers.Wait()
	return out, nil
}

type preloadedResponsibilityEvaluationRepository struct {
	base        responsibilityEvaluationRepository
	assignments map[string][]responsibility.AssignmentRecord
}

func (r preloadedResponsibilityEvaluationRepository) GetPeopleResponsibilityPortfolioByBindingID(ctx context.Context, bindingID string) (responsibility.PeopleResponsibilityPortfolio, bool, error) {
	return r.base.GetPeopleResponsibilityPortfolioByBindingID(ctx, bindingID)
}

func (r preloadedResponsibilityEvaluationRepository) GetPeopleResponsibilityPortfolioVersion(ctx context.Context, versionID string) (responsibility.PeopleResponsibilityPortfolioVersion, bool, error) {
	return r.base.GetPeopleResponsibilityPortfolioVersion(ctx, versionID)
}

func (r preloadedResponsibilityEvaluationRepository) ListActivePeopleResponsibilityPortfoliosPage(ctx context.Context, tenantID, peopleTenantID, cursor string, limit int) (responsibility.PeopleResponsibilityPortfolioPage, error) {
	return r.base.ListActivePeopleResponsibilityPortfoliosPage(ctx, tenantID, peopleTenantID, cursor, limit)
}

func (r preloadedResponsibilityEvaluationRepository) ListAssignments(_ context.Context, query responsibility.AssignmentListQuery) ([]responsibility.AssignmentRecord, error) {
	return append([]responsibility.AssignmentRecord(nil), r.assignments[strings.TrimSpace(query.AgentID)]...), nil
}

func (r preloadedResponsibilityEvaluationRepository) ListAssignmentsForAgents(_ context.Context, _ string, agentIDs []string, _ []responsibility.AssignmentState) (map[string][]responsibility.AssignmentRecord, error) {
	out := make(map[string][]responsibility.AssignmentRecord, len(agentIDs))
	for _, agentID := range agentIDs {
		out[agentID] = append([]responsibility.AssignmentRecord(nil), r.assignments[strings.TrimSpace(agentID)]...)
	}
	return out, nil
}

func (r preloadedResponsibilityEvaluationRepository) GetPolicy(ctx context.Context, tenantID, agentProfileID string, workKind responsibility.WorkKind) (responsibility.ResponsibilityPolicyRecord, error) {
	policies, ok := r.base.(responsibilityAutoplanPolicyResolver)
	if !ok {
		return responsibility.ResponsibilityPolicyRecord{}, responsibility.ErrNotFound
	}
	return policies.GetPolicy(ctx, tenantID, agentProfileID, workKind)
}

func responsibilityEvaluationAssignmentStates() []responsibility.AssignmentState {
	return []responsibility.AssignmentState{
		responsibility.AssignmentStateOffered, responsibility.AssignmentStateAccepted, responsibility.AssignmentStateActive,
		responsibility.AssignmentStateWaiting, responsibility.AssignmentStateSubmitted, responsibility.AssignmentStateEscalated,
	}
}

// responsibilityAutoplanSchedulerPort keeps the scheduler boundary narrow.
// Evaluation may only persist or update an Autoplan and must return a
// no-action result; a later reconciliation owns HIL receipt consumption and
// Mission materialization. Keeping those phases separate prevents a pulse
// from turning an LLM response directly into a task-creation side effect.
type responsibilityAutoplanSchedulerPort interface {
	EvaluateAutoplan(context.Context, schedulerapp.ResponsibilityEvaluationRequest, responsibility.PeopleResponsibilityPortfolio) (schedulerapp.ResponsibilityEvaluationResult, error)
	ReconcileAutoplans(context.Context, int) error
}

type responsibilityAutoplanPolicyResolver interface {
	GetPolicy(context.Context, string, string, responsibility.WorkKind) (responsibility.ResponsibilityPolicyRecord, error)
}

func (p *productionSchedulerResponsibilityPort) EvaluateResponsibility(ctx context.Context, request schedulerapp.ResponsibilityEvaluationRequest) (schedulerapp.ResponsibilityEvaluationDecision, error) {
	result := schedulerapp.ResponsibilityEvaluationResult{AgentProfileID: strings.TrimSpace(request.AgentProfileID)}
	if p == nil || p.repository == nil {
		return schedulerapp.ResponsibilityEvaluationDecision{Result: result}, schedulerapp.ErrServiceDependency
	}
	request.TenantID = strings.TrimSpace(request.TenantID)
	request.AgentProfileID = strings.TrimSpace(request.AgentProfileID)
	request.PortfolioBindingID = strings.TrimSpace(request.PortfolioBindingID)
	request.PortfolioVersionID = strings.TrimSpace(request.PortfolioVersionID)
	if request.TenantID == "" || request.AgentProfileID == "" || request.PortfolioBindingID == "" {
		return schedulerapp.ResponsibilityEvaluationDecision{Result: result}, schedulerapp.ErrInvalidRequest
	}
	portfolio, found, err := p.responsibilityEvaluationPortfolio(ctx, request)
	if err != nil {
		return schedulerapp.ResponsibilityEvaluationDecision{Result: result}, err
	}
	if !found {
		return schedulerapp.ResponsibilityEvaluationDecision{Result: result}, fmt.Errorf("%w: People responsibility portfolio was not found", schedulerapp.ErrInvalidTarget)
	}
	result.PortfolioID = strings.TrimSpace(portfolio.ID)
	result.PortfolioBindingID = strings.TrimSpace(portfolio.BindingID)
	result.PortfolioVersionID = responsibility.PeopleResponsibilityPortfolioVersionID(portfolio.BindingID, portfolio.SourceDigest)
	result.SourceRevision = strings.TrimSpace(portfolio.SourceRevision)
	result.SourceDigest = strings.TrimSpace(portfolio.SourceDigest)
	result.DecisionKind = schedulerapp.ResponsibilityEvaluationNoAction
	result.DecisionSourceRef = result.PortfolioVersionID
	if portfolio.TenantID != request.TenantID || portfolio.BindingID != request.PortfolioBindingID || portfolio.AgentProfileID != request.AgentProfileID {
		return schedulerapp.ResponsibilityEvaluationDecision{Result: result}, fmt.Errorf("%w: People responsibility portfolio does not match the scheduled target", schedulerapp.ErrInvalidTarget)
	}
	result.Evaluated = true
	if portfolio.Status != responsibility.PeoplePortfolioStatusActive {
		result.Reason = "portfolio_inactive"
		return schedulerapp.ResponsibilityEvaluationDecision{Result: result}, nil
	}
	assignmentsByAgent, err := p.repository.ListAssignmentsForAgents(
		ctx, request.TenantID, []string{request.AgentProfileID}, responsibilityEvaluationAssignmentStates(),
	)
	if err != nil {
		return schedulerapp.ResponsibilityEvaluationDecision{Result: result}, err
	}
	assignments := assignmentsByAgent[request.AgentProfileID]
	result.CandidateCount = len(assignments)
	var selected *responsibility.AssignmentRecord
	for index := range assignments {
		assignment := &assignments[index]
		if assignment.Paused {
			result.PausedCount++
			continue
		}
		if assignment.WorkKind != responsibility.WorkKindMissionTask || strings.TrimSpace(assignment.MissionID) == "" || strings.TrimSpace(assignment.WorkID) == "" {
			result.InvalidCount++
			continue
		}
		switch assignment.State {
		case responsibility.AssignmentStateOffered, responsibility.AssignmentStateAccepted, responsibility.AssignmentStateActive:
			result.ActionableCount++
			if selected == nil || responsibilityAssignmentPreferred(*assignment, *selected) {
				selected = assignment
			}
		case responsibility.AssignmentStateWaiting:
			result.WaitingCount++
		case responsibility.AssignmentStateSubmitted:
			result.ReviewCount++
		case responsibility.AssignmentStateEscalated:
			result.EscalatedCount++
		default:
			result.InvalidCount++
		}
	}
	if selected == nil {
		result.Reason = "no_concrete_obligation"
		if result.CandidateCount > 0 {
			result.Reason = "concrete_obligations_not_runnable"
			return schedulerapp.ResponsibilityEvaluationDecision{Result: result}, nil
		}
		if p.autoplan != nil {
			enabled, policyErr := p.autoplanEnabled(ctx, request.TenantID, request.AgentProfileID)
			if policyErr != nil {
				return schedulerapp.ResponsibilityEvaluationDecision{Result: result}, policyErr
			}
			if enabled {
				autoplanResult, autoplanErr := p.autoplan.EvaluateAutoplan(ctx, request, portfolio)
				if autoplanErr != nil {
					return schedulerapp.ResponsibilityEvaluationDecision{Result: result}, autoplanErr
				}
				result, autoplanErr = responsibilityAutoplanNoActionResult(result, autoplanResult)
				if autoplanErr != nil {
					return schedulerapp.ResponsibilityEvaluationDecision{Result: result}, autoplanErr
				}
			}
		}
		return schedulerapp.ResponsibilityEvaluationDecision{Result: result}, nil
	}

	// A portfolio declares durable accountability; only a concrete Mission Task assignment is executable work.
	result.Actionable = true
	result.DecisionKind = schedulerapp.ResponsibilityEvaluationLinkExistingMission
	result.DecisionSourceRef = "responsibility_assignment:" + selected.ID + ":v" + strconv.FormatInt(selected.Version, 10)
	result.Reason = "existing_mission_task"
	result.SelectedMissionID = strings.TrimSpace(selected.MissionID)
	result.SelectedTaskID = strings.TrimSpace(selected.WorkID)
	result.SelectedResponsibilityID = strings.TrimSpace(selected.ID)
	result.SelectedAssignmentState = string(selected.State)
	result.SelectedAssignmentVersion = selected.Version
	result.SelectedAssignmentPriority = selected.Priority
	result.SelectedAssignmentSourceKind = strings.TrimSpace(selected.SourceKind)
	result.SelectedAssignmentSourceID = strings.TrimSpace(selected.SourceID)
	result.SelectedAssignmentSourceStepID = strings.TrimSpace(selected.SourceStepID)
	if !selected.DueAt.IsZero() {
		dueAt := selected.DueAt.UTC()
		result.SelectedDueAt = &dueAt
	}
	return schedulerapp.ResponsibilityEvaluationDecision{
		Result: result, MissionID: selected.MissionID, TaskID: selected.WorkID, ResponsibilityID: selected.ID,
	}, nil
}

func (p *productionSchedulerResponsibilityPort) responsibilityEvaluationPortfolio(
	ctx context.Context,
	request schedulerapp.ResponsibilityEvaluationRequest,
) (responsibility.PeopleResponsibilityPortfolio, bool, error) {
	if request.PortfolioVersionID == "" {
		return p.repository.GetPeopleResponsibilityPortfolioByBindingID(ctx, request.PortfolioBindingID)
	}
	version, found, err := p.repository.GetPeopleResponsibilityPortfolioVersion(ctx, request.PortfolioVersionID)
	if err != nil || !found {
		return responsibility.PeopleResponsibilityPortfolio{}, found, err
	}
	if version.ID != request.PortfolioVersionID || version.BindingID != request.PortfolioBindingID ||
		version.TenantID != request.TenantID || version.AgentProfileID != request.AgentProfileID {
		return responsibility.PeopleResponsibilityPortfolio{}, false, fmt.Errorf("%w: People responsibility portfolio version does not match the batch target", schedulerapp.ErrInvalidTarget)
	}
	current, currentFound, err := p.repository.GetPeopleResponsibilityPortfolioByBindingID(ctx, request.PortfolioBindingID)
	if err != nil || !currentFound {
		return responsibility.PeopleResponsibilityPortfolio{}, currentFound, err
	}
	if current.TenantID != version.TenantID || current.BindingID != version.BindingID || current.AgentProfileID != version.AgentProfileID {
		return responsibility.PeopleResponsibilityPortfolio{}, false, fmt.Errorf("%w: current People responsibility portfolio does not match the batch target", schedulerapp.ErrInvalidTarget)
	}
	return responsibility.PeopleResponsibilityPortfolio{
		ID: version.PortfolioID, TenantID: version.TenantID, BindingID: version.BindingID,
		PeopleTenantID: version.PeopleTenantID, SubjectID: version.SubjectID, AgentProfileID: version.AgentProfileID,
		SourceRevision: version.SourceRevision, SourceDigest: version.SourceDigest, Responsibilities: version.Responsibilities,
		Status: current.Status, SourceAsOf: version.SourceAsOf,
	}, true, nil
}

func (p *productionSchedulerResponsibilityPort) autoplanEnabled(ctx context.Context, tenantID, agentProfileID string) (bool, error) {
	if p == nil || p.autoplan == nil {
		return false, nil
	}
	policies, ok := p.repository.(responsibilityAutoplanPolicyResolver)
	if !ok {
		return false, nil
	}
	policy, err := policies.GetPolicy(ctx, strings.TrimSpace(tenantID), strings.TrimSpace(agentProfileID), responsibility.WorkKindMissionTask)
	if errors.Is(err, responsibility.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return policy.Enabled && policy.AutonomyLevel == responsibility.AutonomyContinuous, nil
}

func responsibilityAutoplanNoActionResult(base, autoplan schedulerapp.ResponsibilityEvaluationResult) (schedulerapp.ResponsibilityEvaluationResult, error) {
	if autoplan.Actionable || (autoplan.DecisionKind != "" && autoplan.DecisionKind != schedulerapp.ResponsibilityEvaluationNoAction) {
		return base, fmt.Errorf("%w: autonomous responsibility evaluation must return no_action", schedulerapp.ErrInvalidTarget)
	}
	if strings.TrimSpace(autoplan.AgentProfileID) != "" && strings.TrimSpace(autoplan.AgentProfileID) != strings.TrimSpace(base.AgentProfileID) {
		return base, fmt.Errorf("%w: autonomous responsibility evaluation Agent does not match the scheduled target", schedulerapp.ErrInvalidTarget)
	}
	if strings.TrimSpace(autoplan.PortfolioBindingID) != "" && strings.TrimSpace(autoplan.PortfolioBindingID) != strings.TrimSpace(base.PortfolioBindingID) {
		return base, fmt.Errorf("%w: autonomous responsibility evaluation portfolio does not match the scheduled target", schedulerapp.ErrInvalidTarget)
	}
	autoplan.PortfolioID = base.PortfolioID
	autoplan.PortfolioBindingID = base.PortfolioBindingID
	autoplan.PortfolioVersionID = base.PortfolioVersionID
	autoplan.SourceRevision = base.SourceRevision
	autoplan.SourceDigest = base.SourceDigest
	autoplan.AgentProfileID = base.AgentProfileID
	autoplan.Evaluated = true
	autoplan.Actionable = false
	autoplan.DecisionKind = schedulerapp.ResponsibilityEvaluationNoAction
	autoplan.CandidateCount = base.CandidateCount
	autoplan.ActionableCount = base.ActionableCount
	autoplan.WaitingCount = base.WaitingCount
	autoplan.ReviewCount = base.ReviewCount
	autoplan.EscalatedCount = base.EscalatedCount
	autoplan.PausedCount = base.PausedCount
	autoplan.InvalidCount = base.InvalidCount
	autoplan.SelectedMissionID = ""
	autoplan.SelectedTaskID = ""
	autoplan.SelectedResponsibilityID = ""
	autoplan.SelectedAssignmentState = ""
	autoplan.SelectedAssignmentVersion = 0
	autoplan.SelectedAssignmentPriority = 0
	autoplan.SelectedAssignmentSourceKind = ""
	autoplan.SelectedAssignmentSourceID = ""
	autoplan.SelectedAssignmentSourceStepID = ""
	autoplan.SelectedDueAt = nil
	autoplan.LinkedExistingMission = false
	autoplan.MissionTaskMaterialized = false
	if strings.TrimSpace(autoplan.DecisionSourceRef) == "" {
		autoplan.DecisionSourceRef = base.DecisionSourceRef
	}
	if strings.TrimSpace(autoplan.Reason) == "" {
		autoplan.Reason = "autoplan_evaluated"
	}
	return autoplan, nil
}

func responsibilityAssignmentPreferred(candidate, current responsibility.AssignmentRecord) bool {
	if candidate.Priority != current.Priority {
		return candidate.Priority > current.Priority
	}
	if !candidate.DueAt.Equal(current.DueAt) {
		if candidate.DueAt.IsZero() {
			return false
		}
		if current.DueAt.IsZero() {
			return true
		}
		return candidate.DueAt.Before(current.DueAt)
	}
	if !candidate.AcceptBy.Equal(current.AcceptBy) {
		return candidate.AcceptBy.Before(current.AcceptBy)
	}
	if !candidate.CreatedAt.Equal(current.CreatedAt) {
		return candidate.CreatedAt.Before(current.CreatedAt)
	}
	return candidate.ID < current.ID
}

func (p *productionSchedulerResponsibilityPort) ReconcileResponsibilities(ctx context.Context, _ schedulerapp.ResponsibilityReconcileRequest) (schedulerapp.ResponsibilityReconcileResult, error) {
	if p.coordinator == nil {
		return schedulerapp.ResponsibilityReconcileResult{}, schedulerapp.ErrServiceDependency
	}
	result := schedulerapp.ResponsibilityReconcileResult{}
	var reconcileErrors []error
	if p.people != nil {
		stats, err := p.people.Reconcile(ctx)
		result.PeopleSourceAttempted = stats.SourceAttempted
		result.PeopleSourceThrottled = stats.SourceThrottled
		result.PeopleBindingsScanned = stats.BindingsScanned
		result.PeopleBindingsChanged = stats.BindingsChanged
		result.PeoplePortfoliosChanged = stats.PortfoliosChanged
		result.PeopleRoleProfilesChanged = stats.RoleProfilesChanged
		result.PeoplePulsesEnsured = stats.PulsesEnsured
		result.PeoplePulsesDisabled = stats.PulsesDisabled
		result.PeopleGapsOpen = stats.GapsOpen
		result.PeopleGapsResolved = stats.GapsResolved
		result.PeopleFailed = stats.Failed
		result.Failed += stats.Failed
		if err != nil {
			reconcileErrors = append(reconcileErrors, err)
		}
	}
	if p.autoplan != nil {
		if err := p.autoplan.ReconcileAutoplans(ctx, p.batchSize); err != nil {
			reconcileErrors = append(reconcileErrors, err)
		}
	}
	stats, err := p.coordinator.Reconcile(ctx, p.batchSize)
	result.Candidates = stats.Candidates
	result.DependencyBlocked = stats.DependencyBlocked
	result.QABlocked = stats.QABlocked
	result.QAEscalated = stats.QAEscalated
	result.CapacityBlocked = stats.CapacityBlocked
	result.PolicyBlocked = stats.PolicyBlocked
	result.BudgetBlocked = stats.BudgetBlocked
	result.RiskBlocked = stats.RiskBlocked
	result.BlockersRechecked = stats.BlockersRechecked
	result.BlockersResolved = stats.BlockersResolved
	result.BlockersEscalated = stats.BlockersEscalated
	result.AlertsOpened = stats.AlertsOpened
	result.AlertsEscalated = stats.AlertsEscalated
	result.AlertsResolved = stats.AlertsResolved
	result.Heartbeats = stats.Heartbeats
	result.TimedOut = stats.TimedOut
	result.Claimed = stats.Claimed
	result.Dispatched = stats.Dispatched
	result.Synchronized = stats.Synchronized
	result.RecoveredOutcomes = stats.RecoveredOutcomes
	result.Cancelled = stats.Cancelled
	result.Failed += stats.Failed
	if err != nil {
		reconcileErrors = append(reconcileErrors, err)
	}
	return result, errors.Join(reconcileErrors...)
}
