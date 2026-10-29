package responsibility

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"orrery_v2/internal/app/hil"
	"orrery_v2/internal/platform/testkit"
)

var (
	errAutoplanTestPlanLinkInterrupted    = errors.New("autoplan test plan link interrupted")
	errAutoplanTestReceiptSaveInterrupted = errors.New("autoplan test receipt save interrupted")
)

func TestAutoplanServiceRetriesHILCreateWithStableExpiryAfterPlanLinkFailure(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	ctx := context.Background()
	baseRepository := NewRepository(env.DBRouter, env.UnitOfWork)
	autoplanSaveTestPolicy(t, baseRepository, env.Clock.Now(), AutonomyContinuous, ApprovalOnlyHighRisk)

	proposal := testAutoplanProposal()
	proposal.Steps[0].RiskLevel = AutoplanRiskHigh
	proposal.RequiredApprovals = []string{"Confirm the business plan before materialization"}
	planner := &autoplanServiceTestPlanner{proposal: proposal}
	materializer := &autoplanServiceTestMaterializer{now: env.Clock.Now}
	hilGateway := &autoplanHILRetryProbe{delegate: newAutoplanServiceTestHIL(env.Clock.Now)}
	repository := &autoplanServiceFaultRepository{
		AutoplanRepositoryPort: baseRepository,
		failPlanLinkOnce:       true,
	}
	service := newAutoplanServiceWithRepositoryUnderTest(t, repository, planner, materializer, hilGateway, env.Clock.Now)

	input := autoplanServiceTestPortfolioRequest(env.Clock.Now())
	autoplanSaveTestPortfolio(t, baseRepository, input.Portfolio, env.Clock.Now())
	_, err := service.EvaluatePortfolio(ctx, input)
	if !errors.Is(err, errAutoplanTestPlanLinkInterrupted) {
		t.Fatalf("initial evaluation error = %v, want interrupted plan link", err)
	}
	if len(hilGateway.inputs) != 1 {
		t.Fatalf("initial HIL create calls = %d, want 1", len(hilGateway.inputs))
	}
	plans, err := baseRepository.ListAutoplans(ctx, "tenant-1", nil, 10)
	if err != nil || len(plans) != 1 {
		t.Fatalf("load unlinked plan = %+v err=%v", plans, err)
	}
	plan := plans[0]
	if plan.Status != AutoplanStatusPendingApproval || plan.HILDecisionID != "" {
		t.Fatalf("plan after failed link = %+v", plan)
	}
	first := hilGateway.inputs[0]
	if !first.ExpiresAt.Equal(autoplanHILExpiresAt(plan)) {
		t.Fatalf("initial HIL expiry = %s, want plan-created expiry %s", first.ExpiresAt, autoplanHILExpiresAt(plan))
	}

	recovered, err := service.Reconcile(ctx, 10)
	if err != nil {
		t.Fatalf("reconcile after failed plan link: %v", err)
	}
	if recovered.HILCreated != 1 {
		t.Fatalf("reconcile result = %+v, want one linked HIL decision", recovered)
	}
	if len(hilGateway.inputs) != 2 {
		t.Fatalf("HIL create calls after retry = %d, want 2", len(hilGateway.inputs))
	}
	second := hilGateway.inputs[1]
	if second.IdempotencyKey != first.IdempotencyKey || !second.ExpiresAt.Equal(first.ExpiresAt) ||
		second.ArgsHash != first.ArgsHash || second.Resource != first.Resource {
		t.Fatalf("HIL retry input changed: first=%+v second=%+v", first, second)
	}
	plan, err = baseRepository.GetAutoplan(ctx, plan.ID)
	if err != nil || plan.HILDecisionID == "" || plan.HILDecisionID != hilGateway.decisionID {
		t.Fatalf("retried plan = %+v err=%v decision=%q", plan, err, hilGateway.decisionID)
	}
}

func TestAutoplanServiceRecoversAfterHILReceiptConsumedBeforePlanPersistence(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	ctx := context.Background()
	baseRepository := NewRepository(env.DBRouter, env.UnitOfWork)
	autoplanSaveTestPolicy(t, baseRepository, env.Clock.Now(), AutonomyContinuous, ApprovalOnlyHighRisk)

	proposal := testAutoplanProposal()
	proposal.Steps[0].RiskLevel = AutoplanRiskHigh
	proposal.RequiredApprovals = []string{"Confirm the business plan before materialization"}
	planner := &autoplanServiceTestPlanner{proposal: proposal}
	hilGateway := newAutoplanServiceTestHIL(env.Clock.Now)
	repository := &autoplanServiceFaultRepository{AutoplanRepositoryPort: baseRepository}
	firstMaterializer := &autoplanServiceTestMaterializer{now: env.Clock.Now}
	service := newAutoplanServiceWithRepositoryUnderTest(t, repository, planner, firstMaterializer, hilGateway, env.Clock.Now)

	input := autoplanServiceTestPortfolioRequest(env.Clock.Now())
	autoplanSaveTestPortfolio(t, baseRepository, input.Portfolio, env.Clock.Now())
	evaluation, err := service.EvaluatePortfolio(ctx, input)
	if err != nil || len(evaluation.PlanIDs) != 1 {
		t.Fatalf("evaluate high-risk plan = %+v err=%v", evaluation, err)
	}
	planID := evaluation.PlanIDs[0]
	plan, err := baseRepository.GetAutoplan(ctx, planID)
	if err != nil {
		t.Fatalf("load pending plan: %v", err)
	}
	if err := hilGateway.authorize(plan.HILDecisionID); err != nil {
		t.Fatalf("authorize decision: %v", err)
	}
	if _, err := service.Reconcile(ctx, 10); err != nil {
		t.Fatalf("persist approval: %v", err)
	}
	plan, err = baseRepository.GetAutoplan(ctx, planID)
	if err != nil || plan.Status != AutoplanStatusApproved || plan.HILReceiptID == "" {
		t.Fatalf("approved plan = %+v err=%v", plan, err)
	}

	repository.failReceiptSaveOnce = true
	_, err = service.Reconcile(ctx, 10)
	if !errors.Is(err, errAutoplanTestReceiptSaveInterrupted) {
		t.Fatalf("materialize with interrupted receipt persistence = %v", err)
	}
	if len(hilGateway.consumeInputs) != 1 || len(firstMaterializer.calls) != 0 {
		t.Fatalf("after interrupted receipt persistence consumes=%d materializations=%v", len(hilGateway.consumeInputs), firstMaterializer.calls)
	}
	plan, err = baseRepository.GetAutoplan(ctx, planID)
	if err != nil || plan.Status != AutoplanStatusApproved || !plan.HILReceiptConsumedAt.IsZero() {
		t.Fatalf("plan after simulated crash point = %+v err=%v", plan, err)
	}

	// A fresh service instance represents a process restart. The durable HIL receipt is
	// already consumed, so recovery must persist that fact and materialize without a
	// second consumption attempt.
	recoveredMaterializer := &autoplanServiceTestMaterializer{now: env.Clock.Now}
	recoveredService := newAutoplanServiceWithRepositoryUnderTest(t, repository, planner, recoveredMaterializer, hilGateway, env.Clock.Now)
	reconciled, err := recoveredService.Reconcile(ctx, 10)
	if err != nil {
		t.Fatalf("recover after consumed receipt: %v", err)
	}
	if reconciled.Materialized != 1 || len(hilGateway.consumeInputs) != 1 || len(recoveredMaterializer.calls) != 1 {
		t.Fatalf("recovery result = %+v consumes=%d materializations=%v", reconciled, len(hilGateway.consumeInputs), recoveredMaterializer.calls)
	}
	plan, err = baseRepository.GetAutoplan(ctx, planID)
	if err != nil || plan.Status != AutoplanStatusMaterialized || plan.HILReceiptConsumedAt.IsZero() {
		t.Fatalf("recovered plan = %+v err=%v", plan, err)
	}
}

func TestAutoplanServicePlanningLeaseDefersConcurrentPlanner(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	ctx := context.Background()
	repository := NewRepository(env.DBRouter, env.UnitOfWork)
	autoplanSaveTestPolicy(t, repository, env.Clock.Now(), AutonomyContinuous, ApprovalOnlyHighRisk)
	input := autoplanServiceTestPortfolioRequest(env.Clock.Now())
	autoplanSaveTestPortfolio(t, repository, input.Portfolio, env.Clock.Now())
	planner := &blockingAutoplanPlanner{proposal: testAutoplanProposal(), started: make(chan struct{}), release: make(chan struct{})}
	materializer := &autoplanServiceTestMaterializer{now: env.Clock.Now}
	first := newAutoplanServiceWithRepositoryUnderTest(t, repository, planner, materializer, nil, env.Clock.Now)
	second := newAutoplanServiceWithRepositoryUnderTest(t, repository, planner, materializer, nil, env.Clock.Now)

	firstDone := make(chan error, 1)
	go func() {
		_, err := first.EvaluatePortfolio(ctx, input)
		firstDone <- err
	}()
	<-planner.started
	result, err := second.EvaluatePortfolio(ctx, input)
	if err != nil || len(result.PlanIDs) != 1 || planner.CallCount() != 1 {
		t.Fatalf("concurrent evaluation result=%+v planner_calls=%d err=%v", result, planner.CallCount(), err)
	}
	plans, err := repository.ListAutoplans(ctx, input.TenantID, []AutoplanStatus{AutoplanStatusPlanning}, 10)
	if err != nil || len(plans) != 1 || plans[0].PlanningLeaseOwner == "" || plans[0].FailureReason != "" {
		t.Fatalf("leased plan=%+v err=%v", plans, err)
	}
	close(planner.release)
	if err := <-firstDone; err != nil {
		t.Fatalf("first evaluation: %v", err)
	}
	plan, err := repository.GetAutoplan(ctx, plans[0].ID)
	if err != nil || plan.Status != AutoplanStatusApproved || plan.PlanningLeaseOwner != "" {
		t.Fatalf("completed leased plan=%+v err=%v", plan, err)
	}
}

func TestAutoplanServicePlanningFailureUsesBackoffBeforeRetry(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	ctx := context.Background()
	now := env.Clock.Now()
	clock := func() time.Time { return now }
	repository := NewRepository(env.DBRouter, env.UnitOfWork)
	autoplanSaveTestPolicy(t, repository, now, AutonomyContinuous, ApprovalOnlyHighRisk)
	input := autoplanServiceTestPortfolioRequest(now)
	autoplanSaveTestPortfolio(t, repository, input.Portfolio, now)
	planner := &retryingAutoplanPlanner{proposal: testAutoplanProposal(), failures: []error{errors.New("temporary model outage")}}
	materializer := &autoplanServiceTestMaterializer{now: clock}
	service := newAutoplanServiceWithRepositoryUnderTest(t, repository, planner, materializer, nil, clock)

	if _, err := service.EvaluatePortfolio(ctx, input); err == nil {
		t.Fatal("initial planning error = nil, want model failure")
	}
	plans, err := repository.ListAutoplans(ctx, input.TenantID, []AutoplanStatus{AutoplanStatusFailed}, 10)
	if err != nil || len(plans) != 1 || !plans[0].NextPlanningAttemptAt.After(now) || planner.CallCount() != 1 {
		t.Fatalf("failed plan=%+v planner_calls=%d err=%v", plans, planner.CallCount(), err)
	}
	if _, err := service.Reconcile(ctx, 10); err != nil || planner.CallCount() != 1 {
		t.Fatalf("immediate retry should defer planner_calls=%d err=%v", planner.CallCount(), err)
	}
	now = plans[0].NextPlanningAttemptAt.Add(time.Second)
	recovered, err := service.Reconcile(ctx, 10)
	if err != nil || recovered.Approved != 1 || planner.CallCount() != 2 {
		t.Fatalf("backoff recovery=%+v planner_calls=%d err=%v", recovered, planner.CallCount(), err)
	}
}

func TestAutoplanServiceBoundsPlannerCallToPlanningLease(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	ctx := context.Background()
	repository := NewRepository(env.DBRouter, env.UnitOfWork)
	autoplanSaveTestPolicy(t, repository, env.Clock.Now(), AutonomyContinuous, ApprovalOnlyHighRisk)
	input := autoplanServiceTestPortfolioRequest(env.Clock.Now())
	autoplanSaveTestPortfolio(t, repository, input.Portfolio, env.Clock.Now())
	planner := &deadlineAutoplanPlanner{proposal: testAutoplanProposal()}
	service, err := NewAutoplanService(AutoplanServiceOptions{
		Repository: repository, Planner: planner, Materializer: &autoplanServiceTestMaterializer{now: env.Clock.Now},
		GoalContext:     fixedAutoplanGoalContextProvider{value: autoplanServiceTestGoalContext()},
		ExecutionGrants: autoplanServiceTestExecutionGrantWriter{}, Now: env.Clock.Now, PlanningLeaseFor: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.EvaluatePortfolio(ctx, input); err != nil {
		t.Fatalf("EvaluatePortfolio: %v", err)
	}
	if !planner.sawDeadline {
		t.Fatal("planner context had no deadline bounded by the planning lease")
	}
}

type blockingAutoplanPlanner struct {
	proposal AutoplanProposal
	started  chan struct{}
	release  chan struct{}
	once     sync.Once
	mu       sync.Mutex
	calls    int
}

func (p *blockingAutoplanPlanner) PlanAutoplan(_ context.Context, _ AutoplanPlanningRequest) (AutoplanProposal, error) {
	p.mu.Lock()
	p.calls++
	p.mu.Unlock()
	p.once.Do(func() { close(p.started) })
	<-p.release
	return p.proposal, nil
}

func (p *blockingAutoplanPlanner) CallCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

type retryingAutoplanPlanner struct {
	proposal AutoplanProposal
	failures []error
	mu       sync.Mutex
	calls    int
}

type deadlineAutoplanPlanner struct {
	proposal    AutoplanProposal
	sawDeadline bool
}

func (p *deadlineAutoplanPlanner) PlanAutoplan(ctx context.Context, _ AutoplanPlanningRequest) (AutoplanProposal, error) {
	_, p.sawDeadline = ctx.Deadline()
	return p.proposal, nil
}

func (p *retryingAutoplanPlanner) PlanAutoplan(_ context.Context, _ AutoplanPlanningRequest) (AutoplanProposal, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++
	if len(p.failures) > 0 {
		err := p.failures[0]
		p.failures = p.failures[1:]
		return AutoplanProposal{}, err
	}
	return p.proposal, nil
}

func (p *retryingAutoplanPlanner) CallCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

func newAutoplanServiceWithRepositoryUnderTest(t *testing.T, repository AutoplanRepositoryPort, planner AutoplanPlanner, materializer AutoplanStepMaterializer, hilGateway AutoplanHILDecisionService, now func() time.Time) *AutoplanService {
	t.Helper()
	service, err := NewAutoplanService(AutoplanServiceOptions{
		Repository: repository, Planner: planner, Materializer: materializer,
		GoalContext:     fixedAutoplanGoalContextProvider{value: autoplanServiceTestGoalContext()},
		ExecutionGrants: autoplanServiceTestExecutionGrantWriter{}, HIL: hilGateway, Now: now,
	})
	if err != nil {
		t.Fatalf("NewAutoplanService: %v", err)
	}
	return service
}

type autoplanServiceFaultRepository struct {
	AutoplanRepositoryPort
	failPlanLinkOnce    bool
	failReceiptSaveOnce bool
}

func (r *autoplanServiceFaultRepository) LinkAutoplanHILDecision(ctx context.Context, planID, decisionID string, expectedVersion int64, now time.Time) (AutoplanRecord, error) {
	if r.failPlanLinkOnce {
		r.failPlanLinkOnce = false
		return AutoplanRecord{}, errAutoplanTestPlanLinkInterrupted
	}
	return r.AutoplanRepositoryPort.LinkAutoplanHILDecision(ctx, planID, decisionID, expectedVersion, now)
}

func (r *autoplanServiceFaultRepository) CompareAndSaveAutoplan(ctx context.Context, record AutoplanRecord, expectedVersion int64, now time.Time) (bool, AutoplanRecord, error) {
	if r.failReceiptSaveOnce && !record.HILReceiptConsumedAt.IsZero() {
		r.failReceiptSaveOnce = false
		return false, AutoplanRecord{}, errAutoplanTestReceiptSaveInterrupted
	}
	return r.AutoplanRepositoryPort.CompareAndSaveAutoplan(ctx, record, expectedVersion, now)
}

type autoplanHILRetryProbe struct {
	delegate   *autoplanServiceTestHIL
	inputs     []hil.CreateDecisionInput
	decisionID string
}

func (h *autoplanHILRetryProbe) CreateDecision(ctx context.Context, input hil.CreateDecisionInput) (hil.CreateDecisionResult, error) {
	h.inputs = append(h.inputs, input)
	if len(h.inputs) > 1 {
		first := h.inputs[0]
		if input.IdempotencyKey != first.IdempotencyKey || !input.ExpiresAt.Equal(first.ExpiresAt) ||
			input.ArgsHash != first.ArgsHash || input.Resource != first.Resource {
			return hil.CreateDecisionResult{}, fmt.Errorf("HIL retry changed idempotency fingerprint")
		}
	}
	created, err := h.delegate.CreateDecision(ctx, input)
	if err == nil {
		h.decisionID = created.Decision.ID
	}
	return created, err
}

func (h *autoplanHILRetryProbe) GetDecisionResolution(ctx context.Context, tenantID, decisionID string) (hil.DecisionResolution, error) {
	return h.delegate.GetDecisionResolution(ctx, tenantID, decisionID)
}

func (h *autoplanHILRetryProbe) ConsumeGatewayDecision(ctx context.Context, input hil.GatewayReceiptConsumeInput) (hil.DecisionReceipt, error) {
	return h.delegate.ConsumeGatewayDecision(ctx, input)
}
