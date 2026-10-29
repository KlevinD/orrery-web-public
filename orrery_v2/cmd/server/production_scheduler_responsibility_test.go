package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"orrery_v2/internal/app/responsibility"
	schedulerapp "orrery_v2/internal/app/scheduler"
	"orrery_v2/internal/platform/testkit"
	"orrery_v2/internal/platform/uow"
)

func TestProductionSchedulerResponsibilityPortMapsCoordinatorStatsAndErrors(t *testing.T) {
	reconcileErr := errors.New("partial reconciliation failure")
	coordinator := &recordingResponsibilityCoordinator{stats: responsibility.ReconcileStats{
		Candidates: 7, DependencyBlocked: 1, QABlocked: 2, QAEscalated: 1, CapacityBlocked: 3,
		PolicyBlocked: 4, BudgetBlocked: 5, RiskBlocked: 6, BlockersRechecked: 7, BlockersResolved: 8,
		BlockersEscalated: 9, AlertsOpened: 10, AlertsEscalated: 11, AlertsResolved: 12, Heartbeats: 13,
		TimedOut: 14, Claimed: 15, Dispatched: 16, Synchronized: 17, RecoveredOutcomes: 18, Cancelled: 19, Failed: 20,
	}, err: reconcileErr}
	port := productionSchedulerResponsibilityPort{coordinator: coordinator, batchSize: 41}

	result, err := port.ReconcileResponsibilities(context.Background(), schedulerapp.ResponsibilityReconcileRequest{ScheduledRunID: "run-1"})
	if !errors.Is(err, reconcileErr) || coordinator.calls != 1 || coordinator.limit != 41 {
		t.Fatalf("calls=%d limit=%d err=%v", coordinator.calls, coordinator.limit, err)
	}
	if result.Candidates != 7 || result.Dispatched != 16 || result.RecoveredOutcomes != 18 || result.Failed != 20 || result.AlertsEscalated != 11 {
		t.Fatalf("mapped result = %+v", result)
	}
}

func TestProductionSchedulerResponsibilityPortRequiresCoordinator(t *testing.T) {
	_, err := (&productionSchedulerResponsibilityPort{}).ReconcileResponsibilities(context.Background(), schedulerapp.ResponsibilityReconcileRequest{})
	if !errors.Is(err, schedulerapp.ErrServiceDependency) {
		t.Fatalf("error = %v, want ErrServiceDependency", err)
	}
}

func TestProductionSchedulerResponsibilityPortReconcilesPeopleBeforeAssignedWork(t *testing.T) {
	peopleErr := errors.New("People source unavailable")
	people := &recordingPeopleResponsibilityReconciler{
		stats: responsibility.PeopleResponsibilityReconcileStats{
			SourceAttempted: true, BindingsScanned: 3, PortfoliosChanged: 2, RoleProfilesChanged: 1, PulsesEnsured: 2, GapsOpen: 1, Failed: 1,
		},
		err: peopleErr,
	}
	coordinator := &recordingResponsibilityCoordinator{stats: responsibility.ReconcileStats{Candidates: 4, Dispatched: 2, Failed: 3}}
	port := &productionSchedulerResponsibilityPort{coordinator: coordinator, people: people, batchSize: 10}

	result, err := port.ReconcileResponsibilities(context.Background(), schedulerapp.ResponsibilityReconcileRequest{})
	if !errors.Is(err, peopleErr) || people.calls != 1 || coordinator.calls != 1 {
		t.Fatalf("people calls=%d coordinator calls=%d err=%v", people.calls, coordinator.calls, err)
	}
	if !result.PeopleSourceAttempted || result.PeopleBindingsScanned != 3 || result.PeopleRoleProfilesChanged != 1 || result.PeoplePulsesEnsured != 2 ||
		result.PeopleFailed != 1 || result.Candidates != 4 || result.Dispatched != 2 || result.Failed != 4 {
		t.Fatalf("mapped result = %+v", result)
	}
}

func TestProductionSchedulerResponsibilityEvaluationDoesNotCreateWorkFromPortfolioText(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	repository := responsibility.NewRepository(env.DBRouter, env.UnitOfWork)
	portfolio := saveResponsibilityEvaluationPortfolio(t, repository, env.Clock.Now())
	autoplan := &recordingResponsibilityAutoplanScheduler{result: schedulerapp.ResponsibilityEvaluationResult{
		DecisionKind: schedulerapp.ResponsibilityEvaluationNoAction, Reason: "must-not-run",
	}}
	port := &productionSchedulerResponsibilityPort{repository: repository, autoplan: autoplan}

	decision, err := port.EvaluateResponsibility(context.Background(), schedulerapp.ResponsibilityEvaluationRequest{
		TenantID: portfolio.TenantID, AgentProfileID: portfolio.AgentProfileID, PortfolioBindingID: portfolio.BindingID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !decision.Result.Evaluated || decision.Result.Actionable || decision.Result.Reason != "no_concrete_obligation" ||
		decision.Result.DecisionKind != schedulerapp.ResponsibilityEvaluationNoAction || decision.Result.PortfolioBindingID != portfolio.BindingID ||
		decision.Result.PortfolioVersionID != responsibility.PeopleResponsibilityPortfolioVersionID(portfolio.BindingID, portfolio.SourceDigest) ||
		decision.Result.SourceRevision != portfolio.SourceRevision || decision.Result.SourceDigest != portfolio.SourceDigest ||
		decision.Result.CandidateCount != 0 || decision.Action != nil || decision.MissionID != "" || decision.TaskID != "" {
		t.Fatalf("decision = %+v", decision)
	}
	if autoplan.evaluateCalls != 0 {
		t.Fatalf("autoplan calls = %d, want 0 without a stored continuous policy", autoplan.evaluateCalls)
	}
}

func TestProductionSchedulerResponsibilityBatchUsesFrozenPortfolioVersionAndCurrentLifecycle(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	repository := responsibility.NewRepository(env.DBRouter, env.UnitOfWork)
	v1 := saveResponsibilityEvaluationPortfolio(t, repository, env.Clock.Now())
	v1VersionID := responsibility.PeopleResponsibilityPortfolioVersionID(v1.BindingID, v1.SourceDigest)
	v2 := v1
	v2.SourceDigest = "digest-2"
	v2.SourceRevision = "revision-2"
	v2.Status = responsibility.PeoplePortfolioStatusPaused
	v2.Responsibilities = append([]responsibility.PeopleResolvedResponsibility(nil), v1.Responsibilities...)
	v2.Responsibilities[0].Title = "Changed after discovery"
	if _, changed, err := repository.SavePeopleResponsibilityPortfolio(context.Background(), v2, env.Clock.Now().Add(time.Minute)); err != nil || !changed {
		t.Fatalf("save v2 changed=%v err=%v", changed, err)
	}

	decision, err := (&productionSchedulerResponsibilityPort{repository: repository}).EvaluateResponsibility(context.Background(), schedulerapp.ResponsibilityEvaluationRequest{
		TenantID: v1.TenantID, AgentProfileID: v1.AgentProfileID, PortfolioBindingID: v1.BindingID, PortfolioVersionID: v1VersionID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if decision.Result.PortfolioVersionID != v1VersionID || decision.Result.SourceDigest != v1.SourceDigest ||
		decision.Result.SourceRevision != v1.SourceRevision || decision.Result.Reason != "portfolio_inactive" ||
		decision.Result.Actionable || decision.Result.DecisionKind != schedulerapp.ResponsibilityEvaluationNoAction {
		t.Fatalf("decision = %+v, want frozen v1 content with current paused lifecycle", decision)
	}
}

func TestProductionSchedulerResponsibilityEvaluationDelegatesEmptyContinuousPortfolioToAutoplan(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	repository := responsibility.NewRepository(env.DBRouter, env.UnitOfWork)
	portfolio := saveResponsibilityEvaluationPortfolio(t, repository, env.Clock.Now())
	saveResponsibilityAutoplanContinuousPolicy(t, repository, portfolio, env.Clock.Now())
	autoplan := &recordingResponsibilityAutoplanScheduler{result: schedulerapp.ResponsibilityEvaluationResult{
		AgentProfileID: portfolio.AgentProfileID, PortfolioBindingID: portfolio.BindingID,
		DecisionKind:      schedulerapp.ResponsibilityEvaluationNoAction,
		DecisionSourceRef: "responsibility-autoplan:plan-1", Reason: "autoplan_pending_approval",
	}}
	port := &productionSchedulerResponsibilityPort{repository: repository, autoplan: autoplan}
	request := schedulerapp.ResponsibilityEvaluationRequest{
		TenantID: portfolio.TenantID, AgentProfileID: portfolio.AgentProfileID, PortfolioBindingID: portfolio.BindingID,
		ScheduledTaskID: "pulse-1", ScheduledRunID: "run-1", TriggeredAt: env.Clock.Now(),
	}

	decision, err := port.EvaluateResponsibility(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if autoplan.evaluateCalls != 1 || autoplan.request != request || autoplan.portfolio.ID != portfolio.ID {
		t.Fatalf("autoplan calls=%d request=%+v portfolio=%+v", autoplan.evaluateCalls, autoplan.request, autoplan.portfolio)
	}
	if !decision.Result.Evaluated || decision.Result.Actionable || decision.Result.DecisionKind != schedulerapp.ResponsibilityEvaluationNoAction ||
		decision.Result.Reason != "autoplan_pending_approval" || decision.Result.DecisionSourceRef != "responsibility-autoplan:plan-1" ||
		decision.Result.PortfolioID != portfolio.ID || decision.Result.PortfolioVersionID != responsibility.PeopleResponsibilityPortfolioVersionID(portfolio.BindingID, portfolio.SourceDigest) ||
		decision.Action != nil || decision.MissionID != "" || decision.TaskID != "" || decision.ResponsibilityID != "" {
		t.Fatalf("decision = %+v", decision)
	}
}

func TestProductionSchedulerResponsibilityEvaluationRequiresContinuousPolicyForAutoplan(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	repository := responsibility.NewRepository(env.DBRouter, env.UnitOfWork)
	portfolio := saveResponsibilityEvaluationPortfolio(t, repository, env.Clock.Now())
	policy := responsibility.DefaultPolicy(portfolio.TenantID, portfolio.AgentProfileID, responsibility.WorkKindMissionTask)
	if _, err := repository.SavePolicy(context.Background(), policy, "test", "non-continuous-policy", policy.Version, env.Clock.Now()); err != nil {
		t.Fatal(err)
	}
	autoplan := &recordingResponsibilityAutoplanScheduler{result: schedulerapp.ResponsibilityEvaluationResult{
		DecisionKind: schedulerapp.ResponsibilityEvaluationNoAction, Reason: "must-not-run",
	}}

	decision, err := (&productionSchedulerResponsibilityPort{repository: repository, autoplan: autoplan}).EvaluateResponsibility(context.Background(), schedulerapp.ResponsibilityEvaluationRequest{
		TenantID: portfolio.TenantID, AgentProfileID: portfolio.AgentProfileID, PortfolioBindingID: portfolio.BindingID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if autoplan.evaluateCalls != 0 || decision.Result.Actionable || decision.Result.Reason != "no_concrete_obligation" ||
		decision.Result.DecisionKind != schedulerapp.ResponsibilityEvaluationNoAction || decision.Action != nil {
		t.Fatalf("autoplan calls=%d decision=%+v", autoplan.evaluateCalls, decision)
	}
}

func TestProductionSchedulerResponsibilityEvaluationRejectsActionableAutoplanResult(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	repository := responsibility.NewRepository(env.DBRouter, env.UnitOfWork)
	portfolio := saveResponsibilityEvaluationPortfolio(t, repository, env.Clock.Now())
	saveResponsibilityAutoplanContinuousPolicy(t, repository, portfolio, env.Clock.Now())
	autoplan := &recordingResponsibilityAutoplanScheduler{result: schedulerapp.ResponsibilityEvaluationResult{
		Actionable: true, DecisionKind: schedulerapp.ResponsibilityEvaluationMaterializeAction,
	}}

	_, err := (&productionSchedulerResponsibilityPort{repository: repository, autoplan: autoplan}).EvaluateResponsibility(context.Background(), schedulerapp.ResponsibilityEvaluationRequest{
		TenantID: portfolio.TenantID, AgentProfileID: portfolio.AgentProfileID, PortfolioBindingID: portfolio.BindingID,
	})
	if !errors.Is(err, schedulerapp.ErrInvalidTarget) || autoplan.evaluateCalls != 1 {
		t.Fatalf("autoplan calls=%d error=%v", autoplan.evaluateCalls, err)
	}
}

func TestProductionSchedulerResponsibilityEvaluationLinksConcreteAssignment(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	repository := responsibility.NewRepository(env.DBRouter, env.UnitOfWork)
	portfolio := saveResponsibilityEvaluationPortfolio(t, repository, env.Clock.Now())
	var assignment responsibility.AssignmentRecord
	err := env.UnitOfWork.Do(context.Background(), func(ctx context.Context, tx uow.Tx) error {
		var assignErr error
		assignment, _, assignErr = repository.AssignMissionTaskTx(ctx, tx, responsibility.MissionTaskAssignmentInput{
			TenantID: portfolio.TenantID, MissionID: "mission-concrete", TaskID: "task-concrete",
			SourceKind: "todo_plan_step", SourceID: "todo-1", SourceStepID: "step-1",
			AgentProfileID: portfolio.AgentProfileID, Priority: 10, AcceptBy: env.Clock.Now().Add(time.Hour), DueAt: env.Clock.Now().Add(2 * time.Hour),
			ActivationCause: responsibility.ActivationCauseAssigned, ActivationDueAt: env.Clock.Now(), ActorID: "test", Reason: "structured obligation",
		}, env.Clock.Now())
		return assignErr
	})
	if err != nil {
		t.Fatal(err)
	}
	saveResponsibilityAutoplanContinuousPolicy(t, repository, portfolio, env.Clock.Now())
	autoplan := &recordingResponsibilityAutoplanScheduler{result: schedulerapp.ResponsibilityEvaluationResult{
		DecisionKind: schedulerapp.ResponsibilityEvaluationNoAction,
	}}
	port := &productionSchedulerResponsibilityPort{repository: repository, autoplan: autoplan}

	decision, err := port.EvaluateResponsibility(context.Background(), schedulerapp.ResponsibilityEvaluationRequest{
		TenantID: portfolio.TenantID, AgentProfileID: portfolio.AgentProfileID, PortfolioBindingID: portfolio.BindingID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !decision.Result.Actionable || decision.Result.Reason != "existing_mission_task" || decision.Result.CandidateCount != 1 ||
		decision.Result.ActionableCount != 1 || decision.Result.DecisionKind != schedulerapp.ResponsibilityEvaluationLinkExistingMission ||
		decision.Result.SelectedMissionID != assignment.MissionID || decision.Result.SelectedTaskID != assignment.WorkID ||
		decision.Result.SelectedResponsibilityID != assignment.ID || decision.Result.SelectedAssignmentVersion != assignment.Version ||
		decision.Result.SelectedAssignmentState != string(assignment.State) || decision.Result.SelectedAssignmentSourceKind != assignment.SourceKind ||
		decision.Result.SelectedAssignmentSourceID != assignment.SourceID || decision.Result.SelectedAssignmentSourceStepID != assignment.SourceStepID ||
		decision.Result.SelectedDueAt == nil || !decision.Result.SelectedDueAt.Equal(assignment.DueAt) ||
		decision.Action != nil || decision.MissionID != assignment.MissionID ||
		decision.TaskID != assignment.WorkID || decision.ResponsibilityID != assignment.ID {
		t.Fatalf("decision = %+v assignment=%+v", decision, assignment)
	}
	if autoplan.evaluateCalls != 0 {
		t.Fatalf("autoplan calls = %d, want 0 when a concrete assignment exists", autoplan.evaluateCalls)
	}
}

func TestProductionSchedulerResponsibilityEvaluationClassifiesWaitingAssignment(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	repository := responsibility.NewRepository(env.DBRouter, env.UnitOfWork)
	portfolio := saveResponsibilityEvaluationPortfolio(t, repository, env.Clock.Now())
	var assignment responsibility.AssignmentRecord
	if err := env.UnitOfWork.Do(context.Background(), func(ctx context.Context, tx uow.Tx) error {
		var err error
		assignment, _, err = repository.AssignMissionTaskTx(ctx, tx, responsibility.MissionTaskAssignmentInput{
			TenantID: portfolio.TenantID, MissionID: "mission-waiting", TaskID: "task-waiting",
			AgentProfileID: portfolio.AgentProfileID, Priority: 5, AcceptBy: env.Clock.Now().Add(time.Hour),
			ActivationCause: responsibility.ActivationCauseAssigned,
		}, env.Clock.Now())
		return err
	}); err != nil {
		t.Fatal(err)
	}
	for _, state := range []responsibility.AssignmentState{responsibility.AssignmentStateAccepted, responsibility.AssignmentStateActive, responsibility.AssignmentStateWaiting} {
		var err error
		assignment, err = repository.TransitionAssignment(context.Background(), assignment.ID, state, env.Clock.Now())
		if err != nil {
			t.Fatal(err)
		}
	}

	decision, err := (&productionSchedulerResponsibilityPort{repository: repository}).EvaluateResponsibility(context.Background(), schedulerapp.ResponsibilityEvaluationRequest{
		TenantID: portfolio.TenantID, AgentProfileID: portfolio.AgentProfileID, PortfolioBindingID: portfolio.BindingID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if decision.Result.Actionable || decision.Result.DecisionKind != schedulerapp.ResponsibilityEvaluationNoAction || decision.Result.Reason != "concrete_obligations_not_runnable" ||
		decision.Result.CandidateCount != 1 || decision.Result.WaitingCount != 1 || decision.Result.ActionableCount != 0 {
		t.Fatalf("decision = %+v", decision)
	}
}

func TestProductionSchedulerResponsibilityPortReconcilesAutoplansBeforeAssignedWork(t *testing.T) {
	autoplanErr := errors.New("autoplan reconciliation failure")
	autoplan := &recordingResponsibilityAutoplanScheduler{reconcileErr: autoplanErr}
	coordinator := &recordingResponsibilityCoordinator{stats: responsibility.ReconcileStats{Candidates: 2}}
	port := &productionSchedulerResponsibilityPort{coordinator: coordinator, autoplan: autoplan, batchSize: 9}

	result, err := port.ReconcileResponsibilities(context.Background(), schedulerapp.ResponsibilityReconcileRequest{})
	if !errors.Is(err, autoplanErr) || autoplan.reconcileCalls != 1 || autoplan.reconcileLimit != 9 || coordinator.calls != 1 {
		t.Fatalf("autoplan calls=%d limit=%d coordinator calls=%d err=%v", autoplan.reconcileCalls, autoplan.reconcileLimit, coordinator.calls, err)
	}
	if result.Candidates != 2 {
		t.Fatalf("result = %+v", result)
	}
}

func TestResponsibilityAssignmentPreferredUsesPriorityThenEarliestDueDate(t *testing.T) {
	now := time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC)
	current := responsibility.AssignmentRecord{ResponsibilityAssignment: responsibility.ResponsibilityAssignment{
		ID: "current", Priority: 10, DueAt: now.Add(4 * time.Hour), AcceptBy: now.Add(time.Hour),
	}}
	candidate := responsibility.AssignmentRecord{ResponsibilityAssignment: responsibility.ResponsibilityAssignment{
		ID: "candidate", Priority: 10, DueAt: now.Add(2 * time.Hour), AcceptBy: now.Add(time.Hour),
	}}
	if !responsibilityAssignmentPreferred(candidate, current) {
		t.Fatal("earlier due assignment was not preferred")
	}
	candidate.Priority = 9
	if responsibilityAssignmentPreferred(candidate, current) {
		t.Fatal("lower priority assignment was preferred")
	}
}

func saveResponsibilityEvaluationPortfolio(t *testing.T, repository *responsibility.Repository, now time.Time) responsibility.PeopleResponsibilityPortfolio {
	t.Helper()
	portfolio := testProductionPeoplePortfolio()
	portfolio.ID = responsibility.PeopleResponsibilityPortfolioID(portfolio.BindingID)
	portfolio.SourceDigest = "digest-1"
	portfolio.Status = responsibility.PeoplePortfolioStatusActive
	portfolio.SourceAsOf = now
	portfolio.SyncedAt = now
	stored, _, err := repository.SavePeopleResponsibilityPortfolio(context.Background(), portfolio, now)
	if err != nil {
		t.Fatal(err)
	}
	return stored
}

func saveResponsibilityAutoplanContinuousPolicy(t *testing.T, repository *responsibility.Repository, portfolio responsibility.PeopleResponsibilityPortfolio, now time.Time) {
	t.Helper()
	policy := responsibility.DefaultPolicy(portfolio.TenantID, portfolio.AgentProfileID, responsibility.WorkKindMissionTask)
	policy.AutonomyLevel = responsibility.AutonomyContinuous
	if _, err := repository.SavePolicy(context.Background(), policy, "test", "continuous-autoplan-policy", policy.Version, now); err != nil {
		t.Fatal(err)
	}
}

type recordingResponsibilityCoordinator struct {
	calls int
	limit int
	stats responsibility.ReconcileStats
	err   error
}

type recordingPeopleResponsibilityReconciler struct {
	calls int
	stats responsibility.PeopleResponsibilityReconcileStats
	err   error
}

type recordingResponsibilityAutoplanScheduler struct {
	evaluateCalls  int
	reconcileCalls int
	request        schedulerapp.ResponsibilityEvaluationRequest
	portfolio      responsibility.PeopleResponsibilityPortfolio
	result         schedulerapp.ResponsibilityEvaluationResult
	evaluateErr    error
	reconcileLimit int
	reconcileErr   error
}

func (r *recordingResponsibilityAutoplanScheduler) EvaluateAutoplan(_ context.Context, request schedulerapp.ResponsibilityEvaluationRequest, portfolio responsibility.PeopleResponsibilityPortfolio) (schedulerapp.ResponsibilityEvaluationResult, error) {
	r.evaluateCalls++
	r.request = request
	r.portfolio = portfolio
	return r.result, r.evaluateErr
}

func (r *recordingResponsibilityAutoplanScheduler) ReconcileAutoplans(_ context.Context, limit int) error {
	r.reconcileCalls++
	r.reconcileLimit = limit
	return r.reconcileErr
}

func (r *recordingPeopleResponsibilityReconciler) Reconcile(context.Context) (responsibility.PeopleResponsibilityReconcileStats, error) {
	r.calls++
	return r.stats, r.err
}

func (r *recordingResponsibilityCoordinator) Reconcile(_ context.Context, limit int) (responsibility.ReconcileStats, error) {
	r.calls++
	r.limit = limit
	return r.stats, r.err
}
