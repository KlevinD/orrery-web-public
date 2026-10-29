package responsibility

import (
	"context"
	"errors"
	"testing"
	"time"

	"orrery_v2/internal/agent/core"
	missiontask "orrery_v2/internal/mission/task"
	"orrery_v2/internal/platform/testkit"
	"orrery_v2/internal/platform/uow"
)

func TestAutoplanExecutionGrantBindsConsumedHILToAssignmentAndClearsCanonicalApprovalBlocker(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	ctx := context.Background()
	repository := NewRepository(env.DBRouter, env.UnitOfWork)
	policy := autoplanSaveTestPolicy(t, repository, env.Clock.Now(), AutonomyContinuous, ApprovalBeforeSideEffect)
	request := autoplanServiceTestPortfolioRequest(env.Clock.Now())
	autoplanSaveTestPortfolio(t, repository, request.Portfolio, env.Clock.Now())
	plan := autoplanClaimServiceTestRecord(t, repository, request, policy, env.Clock.Now())
	proposal := testAutoplanProposal()
	proposal.Steps[0].ExecutionScopes = []string{"repository.write"}
	digest, err := AutoplanProposalDigest(proposal)
	if err != nil {
		t.Fatal(err)
	}
	plan.Proposal = proposal
	plan.ProposalDigest = digest
	plan.Status = AutoplanStatusProposed
	plan = saveAutoplanExecutionGrantTestPlan(t, repository, plan, env.Clock.Now())
	plan.Status = AutoplanStatusPendingApproval
	plan.ApprovalID = "approval-1"
	plan.ApprovalAttempt = 1
	plan.ApprovalRequestedAt = env.Clock.Now()
	plan.HILDecisionID = "hil-decision-1"
	plan = saveAutoplanExecutionGrantTestPlan(t, repository, plan, env.Clock.Now())
	plan.Status = AutoplanStatusApproved
	plan.ApprovedProposalDigest = digest
	plan.HILReceiptID = "hil-receipt-1"
	plan = saveAutoplanExecutionGrantTestPlan(t, repository, plan, env.Clock.Now())
	plan.Status = AutoplanStatusMaterializing
	plan.HILReceiptConsumedAt = env.Clock.Now()
	plan = saveAutoplanExecutionGrantTestPlan(t, repository, plan, env.Clock.Now())

	step := proposal.Steps[0]
	materialization := AutoplanMaterialization{
		StepID: step.StepID, MissionID: "mission-" + step.StepID, TaskID: "task-" + step.StepID,
		ResponsibilityID: AssignmentID(plan.TenantID, WorkKindMissionTask, "task-"+step.StepID), MaterializedAt: env.Clock.Now(),
	}
	var activation ActivationRecord
	err = env.UnitOfWork.Do(ctx, func(ctx context.Context, tx uow.Tx) error {
		_, saved, assignErr := repository.AssignMissionTaskTx(ctx, tx, MissionTaskAssignmentInput{
			TenantID: plan.TenantID, MissionID: materialization.MissionID, TaskID: materialization.TaskID,
			AgentProfileID: step.AssigneeAgentID, Priority: step.Priority, AcceptBy: env.Clock.Now().Add(time.Minute),
			ActivationCause: ActivationCauseAssigned, SourceKind: autoplanExecutionGrantSourceKind, SourceID: plan.ID, SourceStepID: step.StepID,
		}, env.Clock.Now())
		activation = saved
		return assignErr
	})
	if err != nil {
		t.Fatal(err)
	}
	assignment, err := repository.GetAssignment(ctx, activation.ResponsibilityID)
	if err != nil {
		t.Fatal(err)
	}
	blocker, err := repository.OpenBlocker(ctx, OpenBlockerInput{
		TenantID: assignment.TenantID, ResponsibilityID: assignment.ID, Kind: BlockerKindApproval,
		Needed: "human approval is required before runtime execution", RequestedFrom: "mission_owner:" + assignment.MissionID,
		RecheckAt: env.Clock.Now().Add(time.Hour), DedupeKey: "dispatch-policy-approval", ApprovalScopeDigest: approvalScopeDigest(step.ExecutionScopes),
	}, env.Clock.Now())
	if err != nil {
		t.Fatal(err)
	}
	if blocker.Status != BlockerStatusOpen {
		t.Fatalf("canonical approval blocker = %+v", blocker)
	}
	tamperedPlan := plan
	tamperedPlan.HILReceiptID = "hil-receipt-tampered"
	if _, err := repository.GrantAutoplanExecution(ctx, tamperedPlan, materialization, env.Clock.Now()); !errors.Is(err, ErrConflict) {
		t.Fatalf("tampered persisted-plan grant error=%v, want conflict", err)
	}

	grant, err := repository.GrantAutoplanExecution(ctx, plan, materialization, env.Clock.Now())
	if err != nil {
		t.Fatalf("GrantAutoplanExecution: %v", err)
	}
	if grant.HILDecisionID != plan.HILDecisionID || grant.HILReceiptID != plan.HILReceiptID || grant.PolicyID != policy.ID || grant.PolicyVersion != policy.Version ||
		grant.ScopeDigest != approvalScopeDigest(step.ExecutionScopes) {
		t.Fatalf("execution grant binding = %+v", grant)
	}
	resolved, err := repository.GetBlocker(ctx, assignment.TenantID, blocker.ID)
	if err != nil || resolved.Status != BlockerStatusResolved || resolved.ApprovalScopeDigest != grant.ScopeDigest {
		t.Fatalf("canonical approval blocker after grant = %+v err=%v", resolved, err)
	}
	// This models the inverse interleaving: the HIL grant commits first and a
	// coordinator reads stale approval state before opening the canonical blocker.
	// OpenBlocker must immediately converge it back to resolved in its own UOW.
	reopened, err := repository.OpenBlocker(ctx, OpenBlockerInput{
		TenantID: assignment.TenantID, ResponsibilityID: assignment.ID, Kind: BlockerKindApproval,
		Needed: "human approval is required before runtime execution", RequestedFrom: "mission_owner:" + assignment.MissionID,
		RecheckAt: env.Clock.Now().Add(time.Hour), DedupeKey: "dispatch-policy-approval", ApprovalScopeDigest: grant.ScopeDigest,
	}, env.Clock.Now())
	if err != nil || reopened.Status != BlockerStatusResolved || reopened.ApprovalScopeDigest != grant.ScopeDigest {
		t.Fatalf("post-grant canonical approval blocker = %+v err=%v", reopened, err)
	}
	granted, err := repository.ApprovalGranted(ctx, assignment, policy, step.ExecutionScopes)
	if err != nil || !granted {
		t.Fatalf("HIL-backed execution approval granted=%v err=%v", granted, err)
	}
	if granted, err = repository.ApprovalGranted(ctx, assignment, policy, []string{"repository.admin"}); err != nil || granted {
		t.Fatalf("changed scope approval granted=%v err=%v", granted, err)
	}
	coordinator := NewCoordinator(CoordinatorOptions{Repository: repository})
	blockedKind, blockedReason, err := coordinator.policyBlockReason(ctx, assignment, policy, core.AgentProfile{ID: core.AgentProfileID(step.AssigneeAgentID), Enabled: true}, step.ExecutionScopes)
	if err != nil || blockedKind != "" || blockedReason != "" {
		t.Fatalf("HIL-backed policy decision kind=%q reason=%q err=%v", blockedKind, blockedReason, err)
	}

	forged := materialization
	forged.TaskID = "task-forged"
	forged.ResponsibilityID = AssignmentID(plan.TenantID, WorkKindMissionTask, forged.TaskID)
	if _, err := repository.GrantAutoplanExecution(ctx, plan, forged, env.Clock.Now()); !errors.Is(err, ErrConflict) {
		t.Fatalf("forged task grant error=%v, want conflict", err)
	}
}

func TestCoordinatorBlocksApprovalNoneHILAutoplanUntilExecutionGrantIsDurable(t *testing.T) {
	fixture := newAutoplanDispatchFixture(t, true)
	defer fixture.env.Close()

	// This is the state left after a Mission Task was created but the grant
	// writer failed or has not completed yet.
	stats, err := fixture.coordinator.Reconcile(context.Background(), 10)
	if err != nil || stats.PolicyBlocked != 1 || stats.Dispatched != 0 || len(fixture.dispatcher.requests) != 0 {
		t.Fatalf("HIL plan without grant stats=%+v dispatched=%+v err=%v", stats, fixture.dispatcher.requests, err)
	}
	blockers, err := fixture.repository.ListBlockers(context.Background(), fixture.assignment.TenantID, fixture.assignment.ID, true)
	if err != nil || len(blockers) != 1 || blockers[0].Kind != BlockerKindApproval || blockers[0].DedupeKey != "dispatch-policy-approval" {
		t.Fatalf("HIL plan without grant blockers=%+v err=%v", blockers, err)
	}

	grant, err := fixture.repository.GrantAutoplanExecution(context.Background(), fixture.plan, fixture.materialization, fixture.env.Clock.Now())
	if err != nil {
		t.Fatalf("persist autonomous-plan execution grant: %v", err)
	}
	if grant.TaskID != fixture.assignment.WorkID || grant.AutoplanID != fixture.plan.ID || grant.StepID != fixture.assignment.SourceStepID {
		t.Fatalf("persisted autonomous-plan execution grant = %+v", grant)
	}

	stats, err = fixture.coordinator.Reconcile(context.Background(), 10)
	if err != nil || stats.Dispatched != 1 || len(fixture.dispatcher.requests) != 1 {
		t.Fatalf("HIL plan with durable grant stats=%+v dispatched=%+v err=%v", stats, fixture.dispatcher.requests, err)
	}
}

func TestAutoplanDispatchAuthorizationRequiresExactDurableBinding(t *testing.T) {
	fixture := newAutoplanDispatchFixture(t, true)
	defer fixture.env.Close()
	ctx := context.Background()
	policy, err := fixture.repository.ResolvePolicy(ctx, fixture.assignment)
	if err != nil {
		t.Fatalf("resolve responsibility policy: %v", err)
	}
	if _, err := fixture.repository.GrantAutoplanExecution(ctx, fixture.plan, fixture.materialization, fixture.env.Clock.Now()); err != nil {
		t.Fatalf("persist autonomous-plan execution grant: %v", err)
	}
	if authorized, err := fixture.repository.AutoplanDispatchAuthorized(ctx, fixture.assignment, policy, []string{"repository.write"}); err != nil || !authorized {
		t.Fatalf("matching autonomous-plan dispatch authorization=%v err=%v", authorized, err)
	}

	testCases := []struct {
		name       string
		assignment AssignmentRecord
		policy     ResponsibilityPolicyRecord
		scopes     []string
	}{
		{
			name:       "source",
			assignment: func() AssignmentRecord { copy := fixture.assignment; copy.SourceID = "autoplan-other"; return copy }(),
			policy:     policy,
			scopes:     []string{"repository.write"},
		},
		{
			name:       "source step",
			assignment: func() AssignmentRecord { copy := fixture.assignment; copy.SourceStepID = "step-other"; return copy }(),
			policy:     policy,
			scopes:     []string{"repository.write"},
		},
		{
			name:       "task",
			assignment: func() AssignmentRecord { copy := fixture.assignment; copy.WorkID = "task-other"; return copy }(),
			policy:     policy,
			scopes:     []string{"repository.write"},
		},
		{
			name:       "mission",
			assignment: func() AssignmentRecord { copy := fixture.assignment; copy.MissionID = "mission-other"; return copy }(),
			policy:     policy,
			scopes:     []string{"repository.write"},
		},
		{
			name: "tenant",
			assignment: func() AssignmentRecord {
				copy := fixture.assignment
				copy.TenantID = "tenant-other"
				return copy
			}(),
			policy: func() ResponsibilityPolicyRecord {
				copy := policy
				copy.TenantID = "tenant-other"
				copy.ID = PolicyID(copy.TenantID, copy.AgentProfileID, copy.WorkKind)
				return copy
			}(),
			scopes: []string{"repository.write"},
		},
		{
			name:       "policy version",
			assignment: fixture.assignment,
			policy: func() ResponsibilityPolicyRecord {
				copy := policy
				copy.Version++
				return copy
			}(),
			scopes: []string{"repository.write"},
		},
		{
			name: "agent",
			assignment: func() AssignmentRecord {
				copy := fixture.assignment
				copy.AccountableAgent = "agent-other"
				return copy
			}(),
			policy: func() ResponsibilityPolicyRecord {
				copy := policy
				copy.AgentProfileID = "agent-other"
				copy.ID = PolicyID(copy.TenantID, copy.AgentProfileID, copy.WorkKind)
				return copy
			}(),
			scopes: []string{"repository.write"},
		},
		{
			name:       "scope",
			assignment: fixture.assignment,
			policy:     policy,
			scopes:     []string{"repository.admin"},
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			authorized, err := fixture.repository.AutoplanDispatchAuthorized(ctx, testCase.assignment, testCase.policy, testCase.scopes)
			if err != nil || authorized {
				t.Fatalf("mismatched autonomous-plan dispatch authorization=%v err=%v", authorized, err)
			}
		})
	}
}

func TestCoordinatorAllowsLowRiskAutoplanWithoutHILGrant(t *testing.T) {
	fixture := newAutoplanDispatchFixture(t, false)
	defer fixture.env.Close()

	stats, err := fixture.coordinator.Reconcile(context.Background(), 10)
	if err != nil || stats.PolicyBlocked != 0 || stats.Dispatched != 1 || len(fixture.dispatcher.requests) != 1 {
		t.Fatalf("low-risk autonomous plan stats=%+v dispatched=%+v err=%v", stats, fixture.dispatcher.requests, err)
	}
	blockers, err := fixture.repository.ListBlockers(context.Background(), fixture.assignment.TenantID, fixture.assignment.ID, true)
	if err != nil || len(blockers) != 0 {
		t.Fatalf("low-risk autonomous plan blockers=%+v err=%v", blockers, err)
	}
}

type autoplanDispatchFixture struct {
	env             *testkit.Env
	repository      *Repository
	plan            AutoplanRecord
	materialization AutoplanMaterialization
	assignment      AssignmentRecord
	coordinator     *Coordinator
	dispatcher      *capturingResponsibilityDispatcher
}

func newAutoplanDispatchFixture(t *testing.T, requiresHIL bool) autoplanDispatchFixture {
	t.Helper()
	ctx := context.Background()
	env := testkit.New(t)
	repository := NewRepository(env.DBRouter, env.UnitOfWork)
	policy := autoplanSaveTestPolicy(t, repository, env.Clock.Now(), AutonomyContinuous, ApprovalNone)
	request := autoplanServiceTestPortfolioRequest(env.Clock.Now())
	autoplanSaveTestPortfolio(t, repository, request.Portfolio, env.Clock.Now())
	plan := autoplanClaimServiceTestRecord(t, repository, request, policy, env.Clock.Now())
	proposal := testAutoplanProposal()
	if requiresHIL {
		proposal.Steps[0].ExecutionScopes = []string{"repository.write"}
	}
	digest, err := AutoplanProposalDigest(proposal)
	if err != nil {
		t.Fatal(err)
	}
	plan.Proposal, plan.ProposalDigest, plan.Status = proposal, digest, AutoplanStatusProposed
	plan = saveAutoplanExecutionGrantTestPlan(t, repository, plan, env.Clock.Now())

	step := proposal.Steps[0]
	materialization := AutoplanMaterialization{
		StepID: step.StepID, MissionID: "mission-dispatch-" + step.StepID, TaskID: "task-dispatch-" + step.StepID,
		ResponsibilityID: AssignmentID(plan.TenantID, WorkKindMissionTask, "task-dispatch-"+step.StepID), MaterializedAt: env.Clock.Now(),
	}
	if requiresHIL {
		plan.Status = AutoplanStatusPendingApproval
		plan.ApprovalID, plan.ApprovalAttempt, plan.ApprovalRequestedAt, plan.HILDecisionID = "approval-dispatch", 1, env.Clock.Now(), "hil-decision-dispatch"
		plan = saveAutoplanExecutionGrantTestPlan(t, repository, plan, env.Clock.Now())
		plan.Status, plan.ApprovedProposalDigest, plan.HILReceiptID = AutoplanStatusApproved, digest, "hil-receipt-dispatch"
		plan = saveAutoplanExecutionGrantTestPlan(t, repository, plan, env.Clock.Now())
		plan.Status, plan.HILReceiptConsumedAt = AutoplanStatusMaterializing, env.Clock.Now()
		plan = saveAutoplanExecutionGrantTestPlan(t, repository, plan, env.Clock.Now())
	} else {
		plan.Status, plan.ApprovedProposalDigest = AutoplanStatusApproved, digest
		plan = saveAutoplanExecutionGrantTestPlan(t, repository, plan, env.Clock.Now())
		plan.Status = AutoplanStatusMaterializing
		plan = saveAutoplanExecutionGrantTestPlan(t, repository, plan, env.Clock.Now())
		plan.Status, plan.Materializations = AutoplanStatusMaterialized, []AutoplanMaterialization{materialization}
		plan = saveAutoplanExecutionGrantTestPlan(t, repository, plan, env.Clock.Now())
	}

	tasks := missiontask.NewPlatformRepository()
	task := testTaskRecord(materialization.TaskID, materialization.MissionID, step.AssigneeAgentID, missiontask.TaskStatusAssigned)
	task.Contract.SourceKind = missiontask.SourceKindResponsibilityAutoplan
	task.Contract.SourceID = plan.ID
	task.Contract.SourceStepID = step.StepID
	task.Contract.ExecutionPermissionScopes = append([]string(nil), step.ExecutionScopes...)
	saveTaskRecord(t, env, tasks, task)

	var assignment AssignmentRecord
	err = env.UnitOfWork.Do(ctx, func(ctx context.Context, tx uow.Tx) error {
		var assignErr error
		assignment, _, assignErr = repository.AssignMissionTaskTx(ctx, tx, MissionTaskAssignmentInput{
			TenantID: plan.TenantID, MissionID: materialization.MissionID, TaskID: materialization.TaskID,
			AgentProfileID: step.AssigneeAgentID, Priority: step.Priority, AcceptBy: env.Clock.Now().Add(time.Minute),
			ActivationCause: ActivationCauseAssigned, SourceKind: autoplanExecutionGrantSourceKind, SourceID: plan.ID, SourceStepID: step.StepID,
		}, env.Clock.Now())
		return assignErr
	})
	if err != nil {
		env.Close()
		t.Fatalf("assign autonomous-plan task: %v", err)
	}
	dispatcher := &capturingResponsibilityDispatcher{}
	coordinator := NewCoordinator(CoordinatorOptions{
		Repository: repository, Service: NewService(ServiceOptions{Repository: repository, UnitOfWork: env.UnitOfWork, Tasks: tasks, Clock: env.Clock}),
		Profiles: staticResponsibilityProfiles{profiles: map[core.AgentProfileID]core.AgentProfile{
			core.AgentProfileID(step.AssigneeAgentID): {ID: core.AgentProfileID(step.AssigneeAgentID), Enabled: true, RuntimeSpec: core.RuntimeSpec{Kind: core.RuntimeKindPi}},
		}},
		Dispatcher: dispatcher, Clock: env.Clock, IDGenerator: env.IDGenerator,
	})
	return autoplanDispatchFixture{
		env: env, repository: repository, plan: plan, materialization: materialization, assignment: assignment,
		coordinator: coordinator, dispatcher: dispatcher,
	}
}

func saveAutoplanExecutionGrantTestPlan(t *testing.T, repository *Repository, plan AutoplanRecord, now time.Time) AutoplanRecord {
	t.Helper()
	updated, current, err := repository.CompareAndSaveAutoplan(context.Background(), plan, plan.Version, now)
	if err != nil || !updated {
		t.Fatalf("save autonomous plan updated=%v plan=%+v err=%v", updated, current, err)
	}
	return current
}
