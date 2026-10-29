package responsibility

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"orrery_v2/internal/app/hil"
	"orrery_v2/internal/platform/testkit"
)

func TestAutoplanServiceContinuousPlanWaitsForChangedSnapshotAfterMaterialization(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	ctx := context.Background()
	repository := NewRepository(env.DBRouter, env.UnitOfWork)
	autoplanSaveTestPolicy(t, repository, env.Clock.Now(), AutonomyContinuous, ApprovalNone)
	planner := &autoplanServiceTestPlanner{proposal: testAutoplanProposal()}
	materializer := &autoplanServiceTestMaterializer{now: env.Clock.Now}
	service := newAutoplanServiceUnderTest(t, repository, planner, materializer, nil, env.Clock.Now)
	input := autoplanServiceTestPortfolioRequest(env.Clock.Now())
	autoplanSaveTestPortfolio(t, repository, input.Portfolio, env.Clock.Now())

	first, err := service.EvaluatePortfolio(ctx, input)
	if err != nil {
		t.Fatalf("first EvaluatePortfolio: %v", err)
	}
	if !first.Evaluated || first.Approved != 1 || len(first.PlanIDs) != 1 || first.Reason != "autoplan_approved_for_materialization" {
		t.Fatalf("first evaluation = %+v", first)
	}
	planID := first.PlanIDs[0]
	plan, err := repository.GetAutoplan(ctx, planID)
	if err != nil || plan.Status != AutoplanStatusApproved || plan.ApprovedProposalDigest != plan.ProposalDigest {
		t.Fatalf("approved plan = %+v err=%v", plan, err)
	}

	replayed, err := service.EvaluatePortfolio(ctx, input)
	if err != nil {
		t.Fatalf("replayed EvaluatePortfolio: %v", err)
	}
	if replayed.Reused != 1 || len(replayed.PlanIDs) != 1 || replayed.PlanIDs[0] != planID || planner.calls != 1 {
		t.Fatalf("replayed evaluation = %+v planner calls=%d", replayed, planner.calls)
	}

	firstReconcile, err := service.Reconcile(ctx, 10)
	if err != nil {
		t.Fatalf("first Reconcile: %v", err)
	}
	if firstReconcile.Candidates != 1 || firstReconcile.Materialized != 1 || len(materializer.calls) != 1 {
		t.Fatalf("first reconcile = %+v materializer calls=%v", firstReconcile, materializer.calls)
	}
	plan, err = repository.GetAutoplan(ctx, planID)
	if err != nil || plan.Status != AutoplanStatusMaterialized || len(plan.Materializations) != 1 {
		t.Fatalf("materialized plan = %+v err=%v", plan, err)
	}

	secondReconcile, err := service.Reconcile(ctx, 10)
	if err != nil {
		t.Fatalf("second Reconcile: %v", err)
	}
	if secondReconcile.Candidates != 0 || len(materializer.calls) != 1 {
		t.Fatalf("second reconcile = %+v materializer calls=%v", secondReconcile, materializer.calls)
	}
	postMaterialization, err := service.EvaluatePortfolio(ctx, input)
	if err != nil {
		t.Fatalf("post-materialization EvaluatePortfolio: %v", err)
	}
	if postMaterialization.Approved != 0 || postMaterialization.Reused != 1 || postMaterialization.WaitingForSignal != 1 ||
		postMaterialization.Reason != "autoplan_waiting_for_new_signal" || len(postMaterialization.PlanIDs) != 1 ||
		postMaterialization.PlanIDs[0] != planID || planner.calls != 1 {
		t.Fatalf("post-materialization evaluation = %+v planner calls=%d", postMaterialization, planner.calls)
	}

	changed := input
	changed.Portfolio.SourceRevision = "2"
	changed.Portfolio.SourceDigest = "sha256:changed"
	changed.Portfolio.Responsibilities = append([]PeopleResolvedResponsibility(nil), input.Portfolio.Responsibilities...)
	changed.Portfolio.Responsibilities[0].Revision = 2
	autoplanSaveTestPortfolio(t, repository, changed.Portfolio, env.Clock.Now().Add(time.Minute))
	changedEvaluation, err := service.EvaluatePortfolio(ctx, changed)
	if err != nil {
		t.Fatalf("changed-snapshot EvaluatePortfolio: %v", err)
	}
	if changedEvaluation.Approved != 1 || changedEvaluation.Reused != 0 || changedEvaluation.WaitingForSignal != 0 ||
		len(changedEvaluation.PlanIDs) != 1 || changedEvaluation.PlanIDs[0] == planID || planner.calls != 2 {
		t.Fatalf("changed-snapshot evaluation = %+v planner calls=%d", changedEvaluation, planner.calls)
	}
	successor, err := repository.GetAutoplan(ctx, changedEvaluation.PlanIDs[0])
	if err != nil || successor.PlanningCycle != 1 || successor.Status != AutoplanStatusApproved || successor.SourceDigest != changed.Portfolio.SourceDigest {
		t.Fatalf("changed-snapshot plan = %+v err=%v", successor, err)
	}
}

func TestAutoplanServiceTreatsChangedGoalAsNewPlanningSignal(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	ctx := context.Background()
	repository := NewRepository(env.DBRouter, env.UnitOfWork)
	autoplanSaveTestPolicy(t, repository, env.Clock.Now(), AutonomyContinuous, ApprovalNone)
	input := autoplanServiceTestPortfolioRequest(env.Clock.Now())
	autoplanSaveTestPortfolio(t, repository, input.Portfolio, env.Clock.Now())
	goals := &mutableAutoplanGoalContextProvider{value: autoplanServiceTestGoalContext()}
	planner := &autoplanServiceTestPlanner{proposal: testAutoplanProposal()}
	service, err := NewAutoplanService(AutoplanServiceOptions{
		Repository: repository, Planner: planner, GoalContext: goals,
		Materializer:    &autoplanServiceTestMaterializer{now: env.Clock.Now},
		ExecutionGrants: autoplanServiceTestExecutionGrantWriter{}, Now: env.Clock.Now,
	})
	if err != nil {
		t.Fatal(err)
	}

	first, err := service.EvaluatePortfolio(ctx, input)
	if err != nil || first.Approved != 1 || len(first.PlanIDs) != 1 {
		t.Fatalf("first evaluation=%+v err=%v", first, err)
	}
	if _, err := service.Reconcile(ctx, 10); err != nil {
		t.Fatalf("first reconcile: %v", err)
	}
	firstPlan, err := repository.GetAutoplan(ctx, first.PlanIDs[0])
	if err != nil {
		t.Fatal(err)
	}

	goals.value.ContextID = "goal-context-2"
	goals.value.Goals[0].Ref = "people:position_goal:goal-1:v2"
	goals.value.Goals[0].Goal = "Reduce delivery defects below one percent"
	goals.value.SourceRefs = []string{goals.value.Goals[0].Ref}
	planner.proposal.Steps[0].ParentGoalRef = goals.value.Goals[0].Ref
	planner.proposal.Steps[0].ParentGoal = goals.value.Goals[0].Goal
	planner.proposal.SourceRefs = []string{responsibilityAutoplanTriggerRef(input.Portfolio.Responsibilities[0]), goals.value.Goals[0].Ref}

	second, err := service.EvaluatePortfolio(ctx, input)
	if err != nil || second.Approved != 1 || len(second.PlanIDs) != 1 || second.PlanIDs[0] == first.PlanIDs[0] {
		t.Fatalf("changed-goal evaluation=%+v err=%v", second, err)
	}
	secondPlan, err := repository.GetAutoplan(ctx, second.PlanIDs[0])
	if err != nil || secondPlan.GoalContextDigest == firstPlan.GoalContextDigest {
		t.Fatalf("goal digests first=%q second=%q err=%v", firstPlan.GoalContextDigest, secondPlan.GoalContextDigest, err)
	}
}

func TestAutoplanServiceResumesInterruptedPlanningStatesOnNextPulse(t *testing.T) {
	testCases := []struct {
		name             string
		prepare          func(t *testing.T, repository *Repository, request AutoplanPortfolioRequest, policy ResponsibilityPolicyRecord, now time.Time) AutoplanRecord
		wantPlannerCalls int
	}{
		{
			name: "planning",
			prepare: func(t *testing.T, repository *Repository, request AutoplanPortfolioRequest, policy ResponsibilityPolicyRecord, now time.Time) AutoplanRecord {
				return autoplanClaimServiceTestRecord(t, repository, request, policy, now)
			},
			wantPlannerCalls: 1,
		},
		{
			name: "proposed",
			prepare: func(t *testing.T, repository *Repository, request AutoplanPortfolioRequest, policy ResponsibilityPolicyRecord, now time.Time) AutoplanRecord {
				record := autoplanClaimServiceTestRecord(t, repository, request, policy, now)
				proposal := testAutoplanProposal()
				digest, err := AutoplanProposalDigest(proposal)
				if err != nil {
					t.Fatalf("AutoplanProposalDigest: %v", err)
				}
				record.Status, record.Proposal, record.ProposalDigest = AutoplanStatusProposed, proposal, digest
				updated, current, err := repository.CompareAndSaveAutoplan(context.Background(), record, record.Version, now)
				if err != nil || !updated {
					t.Fatalf("persist proposed plan updated=%v plan=%+v err=%v", updated, current, err)
				}
				return current
			},
			wantPlannerCalls: 0,
		},
		{
			name: "failed",
			prepare: func(t *testing.T, repository *Repository, request AutoplanPortfolioRequest, policy ResponsibilityPolicyRecord, now time.Time) AutoplanRecord {
				record := autoplanClaimServiceTestRecord(t, repository, request, policy, now)
				record.Status, record.FailureReason = AutoplanStatusFailed, "transient model outage"
				updated, current, err := repository.CompareAndSaveAutoplan(context.Background(), record, record.Version, now)
				if err != nil || !updated {
					t.Fatalf("persist failed plan updated=%v plan=%+v err=%v", updated, current, err)
				}
				return current
			},
			wantPlannerCalls: 1,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			env := testkit.New(t)
			defer env.Close()
			repository := NewRepository(env.DBRouter, env.UnitOfWork)
			policy := autoplanSaveTestPolicy(t, repository, env.Clock.Now(), AutonomyContinuous, ApprovalNone)
			request := autoplanServiceTestPortfolioRequest(env.Clock.Now())
			autoplanSaveTestPortfolio(t, repository, request.Portfolio, env.Clock.Now())
			prepared := testCase.prepare(t, repository, request, policy, env.Clock.Now())
			planner := &autoplanServiceTestPlanner{proposal: testAutoplanProposal()}
			service := newAutoplanServiceUnderTest(t, repository, planner, &autoplanServiceTestMaterializer{now: env.Clock.Now}, nil, env.Clock.Now)

			result, err := service.EvaluatePortfolio(context.Background(), request)
			if err != nil || result.Approved != 1 || len(result.PlanIDs) != 1 || result.PlanIDs[0] != prepared.ID || planner.calls != testCase.wantPlannerCalls {
				t.Fatalf("recovered %s result=%+v planner=%d err=%v", testCase.name, result, planner.calls, err)
			}
			recovered, err := repository.GetAutoplan(context.Background(), prepared.ID)
			if err != nil || recovered.Status != AutoplanStatusApproved {
				t.Fatalf("recovered %s plan=%+v err=%v", testCase.name, recovered, err)
			}
		})
	}
}

func TestAutoplanServiceReconcileRecoversProposedPlan(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	repository := NewRepository(env.DBRouter, env.UnitOfWork)
	policy := autoplanSaveTestPolicy(t, repository, env.Clock.Now(), AutonomyContinuous, ApprovalNone)
	request := autoplanServiceTestPortfolioRequest(env.Clock.Now())
	autoplanSaveTestPortfolio(t, repository, request.Portfolio, env.Clock.Now())
	record := autoplanClaimServiceTestRecord(t, repository, request, policy, env.Clock.Now())
	proposal := testAutoplanProposal()
	digest, err := AutoplanProposalDigest(proposal)
	if err != nil {
		t.Fatalf("AutoplanProposalDigest: %v", err)
	}
	record.Status, record.Proposal, record.ProposalDigest = AutoplanStatusProposed, proposal, digest
	updated, record, err := repository.CompareAndSaveAutoplan(context.Background(), record, record.Version, env.Clock.Now())
	if err != nil || !updated {
		t.Fatalf("persist proposed plan updated=%v plan=%+v err=%v", updated, record, err)
	}
	planner := &autoplanServiceTestPlanner{proposal: testAutoplanProposal()}
	service := newAutoplanServiceUnderTest(t, repository, planner, &autoplanServiceTestMaterializer{now: env.Clock.Now}, nil, env.Clock.Now)

	result, err := service.Reconcile(context.Background(), 10)
	if err != nil || result.Candidates != 1 || result.Approved != 1 || planner.calls != 0 {
		t.Fatalf("reconcile proposed plan result=%+v planner=%d err=%v", result, planner.calls, err)
	}
	recovered, err := repository.GetAutoplan(context.Background(), record.ID)
	if err != nil || recovered.Status != AutoplanStatusApproved {
		t.Fatalf("reconciled proposed plan=%+v err=%v", recovered, err)
	}
}

func TestAutoplanServiceDefersFailedPlanRetryWhenCapacityIsOccupied(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	repository := NewRepository(env.DBRouter, env.UnitOfWork)
	policy := autoplanSaveTestPolicy(t, repository, env.Clock.Now(), AutonomyContinuous, ApprovalNone)
	request := autoplanServiceTestPortfolioRequest(env.Clock.Now())
	autoplanSaveTestPortfolio(t, repository, request.Portfolio, env.Clock.Now())
	failed := autoplanClaimServiceTestRecord(t, repository, request, policy, env.Clock.Now())
	failed.Status, failed.FailureReason = AutoplanStatusFailed, "transient planner outage"
	updated, failed, err := repository.CompareAndSaveAutoplan(context.Background(), failed, failed.Version, env.Clock.Now())
	if err != nil || !updated {
		t.Fatalf("persist failed plan updated=%v plan=%+v err=%v", updated, failed, err)
	}
	other := PeopleResolvedResponsibility{ID: "responsibility-other", Title: "Other work", Revision: 1}
	otherGoalContext := autoplanServiceTestGoalContext()
	otherGoalContext.ResponsibilityID = other.ID
	otherRequest := autoplanRequestForResponsibility(request, policy, other, otherGoalContext, 1)
	open, err := NewAutoplanRecord(otherRequest, env.Clock.Now())
	if err != nil {
		t.Fatalf("NewAutoplanRecord open: %v", err)
	}
	if _, claimed, err := repository.ClaimAutoplan(context.Background(), open, 1, env.Clock.Now()); err != nil || !claimed {
		t.Fatalf("claim open plan claimed=%v err=%v", claimed, err)
	}
	planner := &autoplanServiceTestPlanner{proposal: testAutoplanProposal()}
	service := newAutoplanServiceUnderTest(t, repository, planner, &autoplanServiceTestMaterializer{now: env.Clock.Now}, nil, env.Clock.Now)

	result, err := service.EvaluatePortfolio(context.Background(), request)
	if err != nil || result.CapacityBlocked != 1 || planner.calls != 0 {
		t.Fatalf("capacity-blocked retry result=%+v planner=%d err=%v", result, planner.calls, err)
	}
	stored, err := repository.GetAutoplan(context.Background(), failed.ID)
	if err != nil || stored.Status != AutoplanStatusFailed {
		t.Fatalf("failed plan after deferred retry=%+v err=%v", stored, err)
	}
}

func TestAutoplanServiceReconcileRetriesFailedPlanningWithoutWaitingForNextPulse(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	repository := NewRepository(env.DBRouter, env.UnitOfWork)
	policy := autoplanSaveTestPolicy(t, repository, env.Clock.Now(), AutonomyContinuous, ApprovalNone)
	request := autoplanServiceTestPortfolioRequest(env.Clock.Now())
	autoplanSaveTestPortfolio(t, repository, request.Portfolio, env.Clock.Now())
	failed := autoplanClaimServiceTestRecord(t, repository, request, policy, env.Clock.Now())
	failed.Status, failed.FailureReason = AutoplanStatusFailed, "temporary planner outage"
	updated, failed, err := repository.CompareAndSaveAutoplan(context.Background(), failed, failed.Version, env.Clock.Now())
	if err != nil || !updated {
		t.Fatalf("persist failed plan updated=%v plan=%+v err=%v", updated, failed, err)
	}
	planner := &autoplanServiceTestPlanner{proposal: testAutoplanProposal()}
	service := newAutoplanServiceUnderTest(t, repository, planner, &autoplanServiceTestMaterializer{now: env.Clock.Now}, nil, env.Clock.Now)

	result, err := service.Reconcile(context.Background(), 10)
	if err != nil || result.Candidates != 1 || result.Approved != 1 || planner.calls != 1 {
		t.Fatalf("failed-plan reconcile result=%+v planner=%d err=%v", result, planner.calls, err)
	}
	recovered, err := repository.GetAutoplan(context.Background(), failed.ID)
	if err != nil || recovered.Status != AutoplanStatusApproved {
		t.Fatalf("recovered failed plan=%+v err=%v", recovered, err)
	}
}

func TestAutoplanServiceRecoversTerminalHILDecisionAfterRequestDeadline(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	ctx := context.Background()
	currentTime := time.Date(2026, time.August, 14, 6, 0, 0, 0, time.UTC)
	now := func() time.Time { return currentTime }
	repository := NewRepository(env.DBRouter, env.UnitOfWork)
	autoplanSaveTestPolicy(t, repository, env.Clock.Now(), AutonomyContinuous, ApprovalOnlyHighRisk)
	proposal := testAutoplanProposal()
	proposal.Steps[0].RiskLevel = AutoplanRiskHigh
	proposal.RequiredApprovals = []string{"Confirm the business plan before materialization"}
	planner := &autoplanServiceTestPlanner{proposal: proposal}
	hilGateway := newAutoplanServiceTestHIL(now)
	service := newAutoplanServiceUnderTest(t, repository, planner, &autoplanServiceTestMaterializer{now: now}, hilGateway, now)
	request := autoplanServiceTestPortfolioRequest(currentTime)
	autoplanSaveTestPortfolio(t, repository, request.Portfolio, env.Clock.Now())

	evaluation, err := service.EvaluatePortfolio(ctx, request)
	if err != nil || len(evaluation.PlanIDs) != 1 {
		t.Fatalf("EvaluatePortfolio result=%+v err=%v", evaluation, err)
	}
	plan, err := repository.GetAutoplan(ctx, evaluation.PlanIDs[0])
	if err != nil || plan.HILDecisionID == "" {
		t.Fatalf("pending HIL plan=%+v err=%v", plan, err)
	}
	currentTime = autoplanHILExpiresAt(plan).Add(-time.Minute)
	if err := hilGateway.authorize(plan.HILDecisionID); err != nil {
		t.Fatalf("authorize HIL decision: %v", err)
	}
	currentTime = currentTime.Add(2 * time.Minute)

	result, err := service.Reconcile(ctx, 10)
	if err != nil || result.Approved != 1 || result.Rejected != 0 {
		t.Fatalf("post-deadline terminal HIL reconcile result=%+v err=%v", result, err)
	}
	plan, err = repository.GetAutoplan(ctx, plan.ID)
	if err != nil || plan.Status != AutoplanStatusApproved || plan.HILReceiptID == "" {
		t.Fatalf("recovered terminal HIL plan=%+v err=%v", plan, err)
	}
}

func TestAutoplanServiceHighRiskOrSideEffectWaitsForHILThenConsumesReceipt(t *testing.T) {
	testCases := []struct {
		name           string
		approvalPolicy ApprovalPolicy
		mutate         func(*AutoplanProposal)
	}{
		{
			name:           "high risk",
			approvalPolicy: ApprovalOnlyHighRisk,
			mutate: func(proposal *AutoplanProposal) {
				proposal.Steps[0].RiskLevel = AutoplanRiskHigh
				proposal.RequiredApprovals = []string{"Confirm the high-risk business plan"}
			},
		},
		{
			name:           "side effect",
			approvalPolicy: ApprovalBeforeSideEffect,
			mutate: func(proposal *AutoplanProposal) {
				proposal.Steps[0].HasSideEffect = true
				proposal.RequiredApprovals = []string{"Confirm the side-effecting business plan"}
			},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			env := testkit.New(t)
			defer env.Close()
			ctx := context.Background()
			repository := NewRepository(env.DBRouter, env.UnitOfWork)
			autoplanSaveTestPolicy(t, repository, env.Clock.Now(), AutonomyContinuous, testCase.approvalPolicy)
			proposal := testAutoplanProposal()
			testCase.mutate(&proposal)
			planner := &autoplanServiceTestPlanner{proposal: proposal}
			materializer := &autoplanServiceTestMaterializer{now: env.Clock.Now}
			hilGateway := newAutoplanServiceTestHIL(env.Clock.Now)
			service := newAutoplanServiceUnderTest(t, repository, planner, materializer, hilGateway, env.Clock.Now)
			input := autoplanServiceTestPortfolioRequest(env.Clock.Now())
			autoplanSaveTestPortfolio(t, repository, input.Portfolio, env.Clock.Now())

			evaluation, err := service.EvaluatePortfolio(ctx, input)
			if err != nil {
				t.Fatalf("EvaluatePortfolio: %v", err)
			}
			if evaluation.PendingApproval != 1 || len(evaluation.PlanIDs) != 1 || len(hilGateway.createInputs) != 1 {
				t.Fatalf("evaluation = %+v HIL creates=%d", evaluation, len(hilGateway.createInputs))
			}
			planID := evaluation.PlanIDs[0]
			plan, err := repository.GetAutoplan(ctx, planID)
			if err != nil || plan.Status != AutoplanStatusPendingApproval || plan.HILDecisionID == "" {
				t.Fatalf("pending plan = %+v err=%v", plan, err)
			}
			created := hilGateway.createInputs[0]
			if created.Action != AutoplanHILAction || created.ArgsHash != plan.ProposalDigest || created.IdempotencyKey != autoplanHILIdempotencyKey(plan) {
				t.Fatalf("HIL create input = %+v plan=%+v", created, plan)
			}
			if assigned, _ := created.PayloadPreview["approval_assigned_to"].(string); assigned != input.ApprovalAssigneeID {
				t.Fatalf("HIL assigned approver = %q, want %q", assigned, input.ApprovalAssigneeID)
			}
			display, ok := created.PayloadPreview[hil.ApprovalDisplayPayloadKey].(map[string]any)
			if !ok {
				t.Fatalf("HIL display payload = %#v", created.PayloadPreview[hil.ApprovalDisplayPayloadKey])
			}
			for key, expected := range map[string]string{
				"summary":          proposal.Summary,
				"business_context": proposal.Rationale,
				"approval_summary": "是否同意按这份职责计划继续执行？",
				"impact_scope":     "同意后系统将创建并推进 1 项工作；拒绝后不会执行。",
			} {
				if actual, _ := display[key].(string); actual != expected {
					t.Fatalf("HIL readable payload %s = %q, want %q; payload=%+v", key, actual, expected, created.PayloadPreview)
				}
			}
			steps, ok := display["steps"].([]map[string]any)
			if !ok || len(steps) != 1 || steps[0]["title"] != proposal.Steps[0].Title || steps[0]["goal"] != proposal.Steps[0].Goal {
				t.Fatalf("HIL readable steps = %#v", display["steps"])
			}

			if err := hilGateway.authorize(plan.HILDecisionID); err != nil {
				t.Fatalf("authorize HIL decision: %v", err)
			}
			approvalReconcile, err := service.Reconcile(ctx, 10)
			if err != nil {
				t.Fatalf("approval Reconcile: %v", err)
			}
			if approvalReconcile.Approved != 1 || len(materializer.calls) != 0 || len(hilGateway.consumeInputs) != 0 {
				t.Fatalf("approval reconcile = %+v materializer=%v consumes=%d", approvalReconcile, materializer.calls, len(hilGateway.consumeInputs))
			}
			plan, err = repository.GetAutoplan(ctx, planID)
			if err != nil || plan.Status != AutoplanStatusApproved || plan.HILReceiptID == "" {
				t.Fatalf("approved HIL plan = %+v err=%v", plan, err)
			}

			materializeReconcile, err := service.Reconcile(ctx, 10)
			if err != nil {
				t.Fatalf("materialize Reconcile: %v", err)
			}
			if materializeReconcile.Materialized != 1 || len(materializer.calls) != 1 || len(hilGateway.consumeInputs) != 1 {
				t.Fatalf("materialize reconcile = %+v materializer=%v consumes=%d", materializeReconcile, materializer.calls, len(hilGateway.consumeInputs))
			}
			plan, err = repository.GetAutoplan(ctx, planID)
			if err != nil || plan.Status != AutoplanStatusMaterialized || plan.HILReceiptConsumedAt.IsZero() {
				t.Fatalf("materialized HIL plan = %+v err=%v", plan, err)
			}

			replayedReconcile, err := service.Reconcile(ctx, 10)
			if err != nil {
				t.Fatalf("replayed Reconcile: %v", err)
			}
			if replayedReconcile.Candidates != 0 || len(materializer.calls) != 1 || len(hilGateway.consumeInputs) != 1 {
				t.Fatalf("replayed reconcile = %+v materializer=%v consumes=%d", replayedReconcile, materializer.calls, len(hilGateway.consumeInputs))
			}
		})
	}
}

func TestAutoplanServiceSkipsHILWhenHumanReviewDisabled(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	ctx := context.Background()
	repository := NewRepository(env.DBRouter, env.UnitOfWork)
	autoplanSaveTestPolicy(t, repository, env.Clock.Now(), AutonomyContinuous, ApprovalOnlyHighRisk)
	proposal := testAutoplanProposal()
	proposal.Steps[0].RiskLevel = AutoplanRiskHigh
	proposal.RequiredApprovals = []string{"Confirm the business plan before materialization"}
	hilGateway := newAutoplanServiceTestHIL(env.Clock.Now)
	service, err := NewAutoplanService(AutoplanServiceOptions{
		Repository: repository, Planner: &autoplanServiceTestPlanner{proposal: proposal},
		GoalContext:  fixedAutoplanGoalContextProvider{value: autoplanServiceTestGoalContext()},
		Materializer: &autoplanServiceTestMaterializer{now: env.Clock.Now}, ExecutionGrants: autoplanServiceTestExecutionGrantWriter{},
		HIL: hilGateway, Now: env.Clock.Now, HumanReviewEnabled: func(context.Context) (bool, error) { return false, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	input := autoplanServiceTestPortfolioRequest(env.Clock.Now())
	input.ApprovalAssigneeID = ""
	autoplanSaveTestPortfolio(t, repository, input.Portfolio, env.Clock.Now())

	evaluation, err := service.EvaluatePortfolio(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	if evaluation.Approved != 1 || evaluation.PendingApproval != 0 || len(hilGateway.createInputs) != 0 || len(evaluation.PlanIDs) != 1 {
		t.Fatalf("evaluation=%+v HIL creates=%d", evaluation, len(hilGateway.createInputs))
	}
	plan, err := repository.GetAutoplan(ctx, evaluation.PlanIDs[0])
	if err != nil || plan.Status != AutoplanStatusApproved || plan.HILDecisionID != "" || plan.ApprovedProposalDigest != plan.ProposalDigest {
		t.Fatalf("plan=%+v err=%v", plan, err)
	}
}

func TestAutoplanServicePersistsNoGoalProposalAsNotActionable(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	ctx := context.Background()
	repository := NewRepository(env.DBRouter, env.UnitOfWork)
	policy := autoplanSaveTestPolicy(t, repository, env.Clock.Now(), AutonomyContinuous, ApprovalNone)
	input := autoplanServiceTestPortfolioRequest(env.Clock.Now())
	autoplanSaveTestPortfolio(t, repository, input.Portfolio, env.Clock.Now())

	legacyInput := input
	legacyInput.ScheduledRunID = "legacy-run"
	legacyRequest := autoplanRequestForResponsibility(
		legacyInput, policy, legacyInput.Portfolio.Responsibilities[0], AutoplanGoalContext{}, 1,
	)
	legacy, err := NewAutoplanRecord(legacyRequest, env.Clock.Now())
	if err != nil {
		t.Fatalf("NewAutoplanRecord legacy: %v", err)
	}
	legacy, claimed, err := repository.ClaimAutoplan(ctx, legacy, 1, env.Clock.Now())
	if err != nil || !claimed {
		t.Fatalf("ClaimAutoplan legacy: claimed=%v plan=%+v err=%v", claimed, legacy, err)
	}
	legacyProposal := testAutoplanProposal()
	legacyProposal.Version = AutoplanLegacyProposalVersion
	legacyProposal.Steps[0].ParentGoalRef = ""
	legacyProposal.Steps[0].ParentGoal = ""
	legacyDigest, err := AutoplanProposalDigest(legacyProposal)
	if err != nil {
		t.Fatalf("AutoplanProposalDigest legacy: %v", err)
	}
	legacy.Status, legacy.Proposal, legacy.ProposalDigest = AutoplanStatusProposed, legacyProposal, legacyDigest
	updated, legacy, err := repository.CompareAndSaveAutoplan(ctx, legacy, legacy.Version, env.Clock.Now())
	if err != nil || !updated {
		t.Fatalf("save proposed legacy plan: updated=%v plan=%+v err=%v", updated, legacy, err)
	}
	legacy.Status = AutoplanStatusRejected
	legacy.RejectionReason = "legacy terminal plan"
	updated, legacy, err = repository.CompareAndSaveAutoplan(ctx, legacy, legacy.Version, env.Clock.Now())
	if err != nil || !updated {
		t.Fatalf("save rejected legacy plan: updated=%v plan=%+v err=%v", updated, legacy, err)
	}

	goalContext := AutoplanGoalContext{ContextID: "goal-context-no-goal"}
	service, err := NewAutoplanService(AutoplanServiceOptions{
		Repository: repository,
		Planner: &autoplanServiceTestPlanner{proposal: AutoplanProposal{
			Version:        AutoplanProposalVersion,
			NoActionReason: "responsibility has no explicit existing goal from which work can be decomposed",
		}},
		GoalContext:     fixedAutoplanGoalContextProvider{value: goalContext},
		Materializer:    &autoplanServiceTestMaterializer{now: env.Clock.Now},
		ExecutionGrants: autoplanServiceTestExecutionGrantWriter{},
		Now:             env.Clock.Now,
	})
	if err != nil {
		t.Fatal(err)
	}

	evaluation, err := service.EvaluatePortfolio(ctx, input)
	if err != nil {
		t.Fatalf("EvaluatePortfolio: %v", err)
	}
	if evaluation.NotActionable != 1 || len(evaluation.PlanIDs) != 1 {
		t.Fatalf("evaluation = %+v", evaluation)
	}
	plan, err := repository.GetAutoplan(ctx, evaluation.PlanIDs[0])
	if err != nil || plan.Status != AutoplanStatusNotActionable || plan.Proposal.Version != AutoplanProposalVersion || len(plan.Proposal.Steps) != 0 {
		t.Fatalf("not-actionable plan = %+v err=%v", plan, err)
	}
}

func TestAutoplanServiceRejectsApprovalRequiredPlanWithoutHumanAssignee(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	ctx := context.Background()
	repository := NewRepository(env.DBRouter, env.UnitOfWork)
	autoplanSaveTestPolicy(t, repository, env.Clock.Now(), AutonomyContinuous, ApprovalOnlyHighRisk)
	proposal := testAutoplanProposal()
	proposal.Steps[0].RiskLevel = AutoplanRiskHigh
	proposal.RequiredApprovals = []string{"Confirm the business plan before materialization"}
	planner := &autoplanServiceTestPlanner{proposal: proposal}
	materializer := &autoplanServiceTestMaterializer{now: env.Clock.Now}
	hilGateway := newAutoplanServiceTestHIL(env.Clock.Now)
	service := newAutoplanServiceUnderTest(t, repository, planner, materializer, hilGateway, env.Clock.Now)
	input := autoplanServiceTestPortfolioRequest(env.Clock.Now())
	input.ApprovalAssigneeID = ""
	autoplanSaveTestPortfolio(t, repository, input.Portfolio, env.Clock.Now())

	evaluation, err := service.EvaluatePortfolio(ctx, input)
	if err != nil {
		t.Fatalf("EvaluatePortfolio: %v", err)
	}
	if evaluation.Rejected != 1 || len(evaluation.PlanIDs) != 1 || len(hilGateway.createInputs) != 0 {
		t.Fatalf("evaluation = %+v HIL creates=%d", evaluation, len(hilGateway.createInputs))
	}
	plan, err := repository.GetAutoplan(ctx, evaluation.PlanIDs[0])
	if err != nil || plan.Status != AutoplanStatusRejected || plan.HILDecisionID != "" || plan.RejectionReason != "autonomous plan requires a configured human approval assignee" {
		t.Fatalf("rejected plan = %+v err=%v", plan, err)
	}
}

func TestAutoplanServiceSupersedesPendingApprovalWhenAssigneeChanges(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	ctx := context.Background()
	repository := NewRepository(env.DBRouter, env.UnitOfWork)
	autoplanSaveTestPolicy(t, repository, env.Clock.Now(), AutonomyContinuous, ApprovalOnlyHighRisk)
	proposal := testAutoplanProposal()
	proposal.Steps[0].RiskLevel = AutoplanRiskHigh
	proposal.RequiredApprovals = []string{"Confirm the business plan before materialization"}
	planner := &autoplanServiceTestPlanner{proposal: proposal}
	materializer := &autoplanServiceTestMaterializer{now: env.Clock.Now}
	hilGateway := newAutoplanServiceTestHIL(env.Clock.Now)
	service := newAutoplanServiceUnderTest(t, repository, planner, materializer, hilGateway, env.Clock.Now)
	input := autoplanServiceTestPortfolioRequest(env.Clock.Now())
	autoplanSaveTestPortfolio(t, repository, input.Portfolio, env.Clock.Now())

	first, err := service.EvaluatePortfolio(ctx, input)
	if err != nil || first.PendingApproval != 1 || len(first.PlanIDs) != 1 {
		t.Fatalf("first EvaluatePortfolio = %+v err=%v", first, err)
	}
	oldPlan, err := repository.GetAutoplan(ctx, first.PlanIDs[0])
	if err != nil {
		t.Fatal(err)
	}
	input.ApprovalAssigneeID = "human-successor"
	second, err := service.EvaluatePortfolio(ctx, input)
	if err != nil || second.PendingApproval != 1 || len(second.PlanIDs) != 1 || second.PlanIDs[0] == oldPlan.ID {
		t.Fatalf("second EvaluatePortfolio = %+v err=%v", second, err)
	}
	stale, err := repository.GetAutoplan(ctx, oldPlan.ID)
	if err != nil || stale.Status != AutoplanStatusSuperseded || stale.SupersededBy != "approval_assignee_changed" {
		t.Fatalf("stale plan = %+v err=%v", stale, err)
	}
	oldDecision := hilGateway.decisions[oldPlan.HILDecisionID]
	if oldDecision.Decision.Status != hil.DecisionDenied || oldDecision.Receipt.Effect != hil.EffectDeny {
		t.Fatalf("old HIL decision = %+v, want terminal denial", oldDecision)
	}
	if err := hilGateway.authorize(oldPlan.HILDecisionID); !errors.Is(err, hil.ErrConflict) {
		t.Fatalf("superseded HIL approval became authorizable: %v", err)
	}
	if len(hilGateway.supersedeCalls) != 1 || hilGateway.supersedeCalls[0] != oldPlan.HILDecisionID {
		t.Fatalf("HIL supersede calls = %+v", hilGateway.supersedeCalls)
	}
	successor, err := repository.GetAutoplan(ctx, second.PlanIDs[0])
	if err != nil || successor.PlanningCycle != oldPlan.PlanningCycle+1 || successor.ActorMemberID != "human-successor" {
		t.Fatalf("successor plan = %+v err=%v", successor, err)
	}
	if len(hilGateway.createInputs) != 2 {
		t.Fatalf("HIL creates=%d, want 2", len(hilGateway.createInputs))
	}
	if assigned, _ := hilGateway.createInputs[1].PayloadPreview["approval_assigned_to"].(string); assigned != "human-successor" {
		t.Fatalf("successor HIL assignee=%q", assigned)
	}
}

func TestAutoplanServiceSupersedesApprovedPlanAfterPolicyRevision(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	ctx := context.Background()
	repository := NewRepository(env.DBRouter, env.UnitOfWork)
	policy := autoplanSaveTestPolicy(t, repository, env.Clock.Now(), AutonomyContinuous, ApprovalNone)
	planner := &autoplanServiceTestPlanner{proposal: testAutoplanProposal()}
	materializer := &autoplanServiceTestMaterializer{now: env.Clock.Now}
	service := newAutoplanServiceUnderTest(t, repository, planner, materializer, nil, env.Clock.Now)
	input := autoplanServiceTestPortfolioRequest(env.Clock.Now())
	autoplanSaveTestPortfolio(t, repository, input.Portfolio, env.Clock.Now())

	evaluation, err := service.EvaluatePortfolio(ctx, input)
	if err != nil || evaluation.Approved != 1 || len(evaluation.PlanIDs) != 1 {
		t.Fatalf("EvaluatePortfolio = %+v err=%v", evaluation, err)
	}
	updatedPolicy := policy
	updatedPolicy.MaxParallel = 2
	if _, err := repository.SavePolicy(ctx, updatedPolicy, "manager-1", "autoplan-policy-revision", policy.Version, env.Clock.Now()); err != nil {
		t.Fatalf("SavePolicy revision: %v", err)
	}

	reconcile, err := service.Reconcile(ctx, 10)
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if reconcile.Superseded != 1 || len(materializer.calls) != 0 {
		t.Fatalf("policy-fenced reconcile = %+v materializer=%v", reconcile, materializer.calls)
	}
	plan, err := repository.GetAutoplan(ctx, evaluation.PlanIDs[0])
	if err != nil || plan.Status != AutoplanStatusSuperseded || plan.SupersededBy != "responsibility_policy_changed" {
		t.Fatalf("policy-fenced plan = %+v err=%v", plan, err)
	}
	successor, err := service.EvaluatePortfolio(ctx, input)
	if err != nil || successor.Approved != 1 || len(successor.PlanIDs) != 1 || successor.PlanIDs[0] == plan.ID {
		t.Fatalf("policy-revision successor = %+v err=%v", successor, err)
	}
}

func TestAutoplanServiceStopsMaterializingPlanAfterPolicyRevision(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	ctx := context.Background()
	repository := NewRepository(env.DBRouter, env.UnitOfWork)
	policy := autoplanSaveTestPolicy(t, repository, env.Clock.Now(), AutonomyContinuous, ApprovalNone)
	planner := &autoplanServiceTestPlanner{proposal: testAutoplanProposal()}
	materializer := &autoplanServiceTestMaterializer{now: env.Clock.Now}
	service := newAutoplanServiceUnderTest(t, repository, planner, materializer, nil, env.Clock.Now)
	input := autoplanServiceTestPortfolioRequest(env.Clock.Now())
	autoplanSaveTestPortfolio(t, repository, input.Portfolio, env.Clock.Now())

	evaluation, err := service.EvaluatePortfolio(ctx, input)
	if err != nil || len(evaluation.PlanIDs) != 1 {
		t.Fatalf("EvaluatePortfolio = %+v err=%v", evaluation, err)
	}
	plan, err := repository.GetAutoplan(ctx, evaluation.PlanIDs[0])
	if err != nil {
		t.Fatalf("load approved plan: %v", err)
	}
	plan.Status = AutoplanStatusMaterializing
	updated, plan, err := repository.CompareAndSaveAutoplan(ctx, plan, plan.Version, env.Clock.Now())
	if err != nil || !updated {
		t.Fatalf("mark materializing updated=%v plan=%+v err=%v", updated, plan, err)
	}
	updatedPolicy := policy
	updatedPolicy.Enabled = false
	if _, err := repository.SavePolicy(ctx, updatedPolicy, "manager-1", "autoplan-disable-during-materialize", policy.Version, env.Clock.Now()); err != nil {
		t.Fatalf("SavePolicy disable: %v", err)
	}

	reconcile, err := service.Reconcile(ctx, 10)
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if reconcile.Superseded != 1 || len(materializer.calls) != 0 {
		t.Fatalf("materializing policy fence = %+v calls=%v", reconcile, materializer.calls)
	}
	plan, err = repository.GetAutoplan(ctx, plan.ID)
	if err != nil || plan.Status != AutoplanStatusSuperseded || plan.SupersededBy != "responsibility_policy_changed" {
		t.Fatalf("fenced materializing plan = %+v err=%v", plan, err)
	}
}

func TestAutoplanServiceReissuesExpiredHILReceiptBeforeMaterialization(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	ctx := context.Background()
	currentTime := time.Date(2026, time.August, 14, 6, 0, 0, 0, time.UTC)
	now := func() time.Time { return currentTime }
	repository := NewRepository(env.DBRouter, env.UnitOfWork)
	autoplanSaveTestPolicy(t, repository, env.Clock.Now(), AutonomyContinuous, ApprovalOnlyHighRisk)
	proposal := testAutoplanProposal()
	proposal.Steps[0].RiskLevel = AutoplanRiskHigh
	proposal.RequiredApprovals = []string{"Confirm the business plan before materialization"}
	planner := &autoplanServiceTestPlanner{proposal: proposal}
	materializer := &autoplanServiceTestMaterializer{now: now}
	hilGateway := newAutoplanServiceTestHIL(now)
	service := newAutoplanServiceUnderTest(t, repository, planner, materializer, hilGateway, now)
	input := autoplanServiceTestPortfolioRequest(now())
	autoplanSaveTestPortfolio(t, repository, input.Portfolio, env.Clock.Now())

	evaluation, err := service.EvaluatePortfolio(ctx, input)
	if err != nil || len(evaluation.PlanIDs) != 1 {
		t.Fatalf("EvaluatePortfolio = %+v err=%v", evaluation, err)
	}
	plan, err := repository.GetAutoplan(ctx, evaluation.PlanIDs[0])
	if err != nil {
		t.Fatalf("load pending plan: %v", err)
	}
	firstDecisionID := plan.HILDecisionID
	if err := hilGateway.authorize(firstDecisionID); err != nil {
		t.Fatalf("authorize first HIL decision: %v", err)
	}
	if _, err := service.Reconcile(ctx, 10); err != nil {
		t.Fatalf("persist first approval: %v", err)
	}
	currentTime = currentTime.Add(16 * time.Minute)
	reissued, err := service.Reconcile(ctx, 10)
	if err != nil {
		t.Fatalf("reconcile expired receipt: %v", err)
	}
	plan, err = repository.GetAutoplan(ctx, plan.ID)
	if err != nil || plan.Status != AutoplanStatusPendingApproval || plan.ApprovalAttempt != 2 || plan.HILDecisionID == "" || plan.HILDecisionID == firstDecisionID {
		t.Fatalf("reissued plan = %+v reconcile=%+v err=%v", plan, reissued, err)
	}
	if len(materializer.calls) != 0 || len(hilGateway.consumeInputs) != 0 {
		t.Fatalf("expired receipt materialized or consumed work calls=%v consumes=%d", materializer.calls, len(hilGateway.consumeInputs))
	}
	if err := hilGateway.authorize(plan.HILDecisionID); err != nil {
		t.Fatalf("authorize reissued HIL decision: %v", err)
	}
	if _, err := service.Reconcile(ctx, 10); err != nil {
		t.Fatalf("persist reissued approval: %v", err)
	}
	if _, err := service.Reconcile(ctx, 10); err != nil {
		t.Fatalf("materialize reissued approval: %v", err)
	}
	plan, err = repository.GetAutoplan(ctx, plan.ID)
	if err != nil || plan.Status != AutoplanStatusMaterialized || len(materializer.calls) != 1 || len(hilGateway.consumeInputs) != 1 {
		t.Fatalf("materialized reissued plan = %+v calls=%v consumes=%d err=%v", plan, materializer.calls, len(hilGateway.consumeInputs), err)
	}
}

func TestAutoplanServiceDoesNotEnableScopedResponsibilityPolicy(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	ctx := context.Background()
	repository := NewRepository(env.DBRouter, env.UnitOfWork)
	autoplanSaveTestPolicy(t, repository, env.Clock.Now(), AutonomyExecuteScoped, ApprovalNone)
	planner := &autoplanServiceTestPlanner{proposal: testAutoplanProposal()}
	materializer := &autoplanServiceTestMaterializer{now: env.Clock.Now}
	service := newAutoplanServiceUnderTest(t, repository, planner, materializer, nil, env.Clock.Now)

	result, err := service.EvaluatePortfolio(ctx, autoplanServiceTestPortfolioRequest(env.Clock.Now()))
	if err != nil {
		t.Fatalf("EvaluatePortfolio: %v", err)
	}
	if !result.Evaluated || result.Reason != "autoplan_policy_not_continuous" || len(result.PlanIDs) != 0 || planner.calls != 0 {
		t.Fatalf("scoped evaluation = %+v planner calls=%d", result, planner.calls)
	}
	plans, err := repository.ListAutoplans(ctx, "tenant-1", nil, 10)
	if err != nil || len(plans) != 0 {
		t.Fatalf("plans after scoped evaluation = %+v err=%v", plans, err)
	}
}

func TestBaselineAutoplanPlannerDerivesAuthorizedEvidenceReadScopes(t *testing.T) {
	now := time.Date(2026, time.August, 14, 0, 0, 0, 0, time.UTC)
	request := autoplanServiceTestPortfolioRequest(now)
	policy := DefaultAutoplanPolicy("agent-1")
	policy.Enabled = true
	policy.AllowedExecutionScopes = []string{"operations.deploy", "memory.read", "enterprise_graph.read", "knowledge.read"}
	proposal, err := (BaselineAutoplanPlanner{}).PlanAutoplan(context.Background(), AutoplanPlanningRequest{
		PlanRequest:    testAutoplanRequest(),
		Portfolio:      request.Portfolio,
		Responsibility: request.Portfolio.Responsibilities[0],
		GoalContext:    autoplanServiceTestGoalContext(),
		Policy:         policy,
	})
	if err != nil || len(proposal.Steps) != 1 {
		t.Fatalf("baseline proposal = %+v err=%v", proposal, err)
	}
	step := proposal.Steps[0]
	wantScopes := []string{"enterprise_graph.read", "knowledge.read", "memory.read"}
	if strings.Join(step.ExecutionScopes, ",") != strings.Join(wantScopes, ",") || step.HasSideEffect || step.RiskLevel != AutoplanRiskLow || !strings.Contains(step.Goal, "Do not perform external changes") {
		t.Fatalf("baseline step is not responsibility-scoped read-only work: %+v", step)
	}
}

func TestBaselineAutoplanPlannerReturnsNoActionWithoutEvidenceReadScope(t *testing.T) {
	now := time.Date(2026, time.August, 14, 0, 0, 0, 0, time.UTC)
	request := autoplanServiceTestPortfolioRequest(now)
	policy := DefaultAutoplanPolicy("agent-1")
	policy.Enabled = true
	policy.AllowedExecutionScopes = []string{"operations.deploy"}
	proposal, err := (BaselineAutoplanPlanner{}).PlanAutoplan(context.Background(), AutoplanPlanningRequest{
		PlanRequest:    testAutoplanRequest(),
		Portfolio:      request.Portfolio,
		Responsibility: request.Portfolio.Responsibilities[0],
		GoalContext:    autoplanServiceTestGoalContext(),
		Policy:         policy,
	})
	if err != nil || len(proposal.Steps) != 0 || !strings.Contains(proposal.NoActionReason, "no authorized evidence-read scope") {
		t.Fatalf("baseline no-action proposal = %+v err=%v", proposal, err)
	}
}

func autoplanSaveTestPolicy(t *testing.T, repository *Repository, now time.Time, autonomy AutonomyLevel, approval ApprovalPolicy) ResponsibilityPolicyRecord {
	t.Helper()
	policy := DefaultPolicy("tenant-1", "agent-1", WorkKindMissionTask)
	policy.AutonomyLevel = autonomy
	policy.ApprovalPolicy = approval
	policy.MaxParallel = 1
	saved, err := repository.SavePolicy(context.Background(), policy, "manager-1", "autoplan-policy-"+string(autonomy)+"-"+string(approval), policy.Version, now)
	if err != nil {
		t.Fatalf("SavePolicy: %v", err)
	}
	return saved
}

func autoplanSaveTestPortfolio(t *testing.T, repository *Repository, portfolio PeopleResponsibilityPortfolio, now time.Time) PeopleResponsibilityPortfolio {
	t.Helper()
	saved, _, err := repository.SavePeopleResponsibilityPortfolio(context.Background(), portfolio, now)
	if err != nil {
		t.Fatalf("SavePeopleResponsibilityPortfolio: %v", err)
	}
	return saved
}

func autoplanClaimServiceTestRecord(t *testing.T, repository *Repository, request AutoplanPortfolioRequest, policy ResponsibilityPolicyRecord, now time.Time) AutoplanRecord {
	t.Helper()
	planRequest := autoplanRequestForResponsibility(request, policy, request.Portfolio.Responsibilities[0], autoplanServiceTestGoalContext(), 1)
	candidate, err := NewAutoplanRecord(planRequest, now)
	if err != nil {
		t.Fatalf("NewAutoplanRecord: %v", err)
	}
	record, claimed, err := repository.ClaimAutoplan(context.Background(), candidate, 1, now)
	if err != nil || !claimed {
		t.Fatalf("ClaimAutoplan claimed=%v plan=%+v err=%v", claimed, record, err)
	}
	return record
}

func newAutoplanServiceUnderTest(t *testing.T, repository *Repository, planner AutoplanPlanner, materializer AutoplanStepMaterializer, hilGateway AutoplanHILDecisionService, now func() time.Time) *AutoplanService {
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

func autoplanServiceTestGoalContext() AutoplanGoalContext {
	return AutoplanGoalContext{
		ContextID: "goal-context-1", ResponsibilityID: "responsibility-1", ResponsibilityVersion: 1, AgentProfileID: "agent-1",
		Goals: []AutoplanGoalAnchor{{
			Ref: "people:position_goal:goal-1:v1", Scope: "position", Goal: "Improve delivery reliability",
			SuccessCriteria: []string{"99% pass rate"},
		}},
		SourceRefs: []string{"people:position_goal:goal-1:v1"},
	}
}

type fixedAutoplanGoalContextProvider struct {
	value AutoplanGoalContext
	err   error
}

type mutableAutoplanGoalContextProvider struct {
	value AutoplanGoalContext
}

func (p *mutableAutoplanGoalContextProvider) ResolveAutoplanGoalContext(_ context.Context, request ContextRequest) (AutoplanGoalContext, error) {
	value := p.value
	value.ResponsibilityID = request.ResponsibilityID
	value.ResponsibilityVersion = request.AssignmentVersion
	value.AgentProfileID = request.AgentProfileID
	return value, nil
}

func (p fixedAutoplanGoalContextProvider) ResolveAutoplanGoalContext(_ context.Context, request ContextRequest) (AutoplanGoalContext, error) {
	if p.err != nil {
		return AutoplanGoalContext{}, p.err
	}
	value := p.value
	value.ResponsibilityID = request.ResponsibilityID
	value.ResponsibilityVersion = request.AssignmentVersion
	value.AgentProfileID = request.AgentProfileID
	return value, nil
}

func autoplanServiceTestPortfolioRequest(now time.Time) AutoplanPortfolioRequest {
	return AutoplanPortfolioRequest{
		TenantID: "tenant-1", ScheduledTaskID: "pulse-1", ScheduledRunID: "run-1", ApprovalAssigneeID: "human-approver", TriggeredAt: now,
		Portfolio: PeopleResponsibilityPortfolio{
			ID: PeopleResponsibilityPortfolioID("binding-1"), TenantID: "tenant-1", BindingID: "binding-1", PeopleTenantID: "people-1", SubjectID: "subject-1",
			AgentProfileID: "agent-1", SourceRevision: "1", SourceDigest: "sha256:source", Status: PeoplePortfolioStatusActive,
			Responsibilities: []PeopleResolvedResponsibility{{
				ID: "responsibility-1", Code: "DELIVERY", Title: "Deliver the current outcome", Description: "Create the traceable current deliverable.",
				Revision: 1,
			}},
		},
	}
}

type autoplanServiceTestPlanner struct {
	proposal AutoplanProposal
	calls    int
}

func (p *autoplanServiceTestPlanner) PlanAutoplan(_ context.Context, _ AutoplanPlanningRequest) (AutoplanProposal, error) {
	p.calls++
	return p.proposal, nil
}

type autoplanServiceTestMaterializer struct {
	now   func() time.Time
	calls []string
}

type autoplanServiceTestExecutionGrantWriter struct{}

func (autoplanServiceTestExecutionGrantWriter) GrantAutoplanExecution(_ context.Context, _ AutoplanRecord, _ AutoplanMaterialization, _ time.Time) (AutoplanExecutionGrant, error) {
	return AutoplanExecutionGrant{ID: "test-grant"}, nil
}

func (m *autoplanServiceTestMaterializer) MaterializeAutoplanStep(_ context.Context, plan AutoplanRecord, stepID string) (AutoplanMaterialization, error) {
	m.calls = append(m.calls, plan.ID+":"+stepID)
	return AutoplanMaterialization{
		StepID: stepID, MissionID: "mission-" + stepID, TaskID: "task-" + stepID,
		ResponsibilityID: "responsibility-" + stepID, MaterializedAt: m.now().UTC(),
	}, nil
}

type autoplanServiceTestHIL struct {
	now              func() time.Time
	createInputs     []hil.CreateDecisionInput
	consumeInputs    []hil.GatewayReceiptConsumeInput
	supersedeCalls   []string
	decisions        map[string]hil.DecisionResolution
	byIdempotencyKey map[string]string
	sequence         int
}

func newAutoplanServiceTestHIL(now func() time.Time) *autoplanServiceTestHIL {
	return &autoplanServiceTestHIL{
		now: now, decisions: make(map[string]hil.DecisionResolution), byIdempotencyKey: make(map[string]string),
	}
}

func (h *autoplanServiceTestHIL) CreateDecision(_ context.Context, input hil.CreateDecisionInput) (hil.CreateDecisionResult, error) {
	key := input.TenantID + "|" + input.IdempotencyKey
	if decisionID, exists := h.byIdempotencyKey[key]; exists {
		return hil.CreateDecisionResult{Decision: h.decisions[decisionID].Decision}, nil
	}
	h.sequence++
	decisionID := fmt.Sprintf("hil-autoplan-%d", h.sequence)
	decision := hil.DecisionRequest{
		ID: decisionID, TenantID: input.TenantID, Requester: input.Requester, Action: input.Action, ActionTypeID: input.ActionTypeID,
		Resource: input.Resource, ObjectRefs: append([]hil.ObjectRef(nil), input.ObjectRefs...), ArgsHash: input.ArgsHash,
		Purpose: input.Purpose, RiskLevel: input.RiskLevel, PreferredMode: input.PreferredMode,
		Route: hil.RouteDecision{Mode: hil.ModeApproval}, PayloadPreview: cloneAutoplanServiceTestPayload(input.PayloadPreview),
		IdempotencyKey: input.IdempotencyKey, Status: hil.DecisionPending, CreatedAt: h.now().UTC(), ExpiresAt: input.ExpiresAt,
	}
	h.createInputs = append(h.createInputs, input)
	h.decisions[decisionID] = hil.DecisionResolution{Decision: decision}
	h.byIdempotencyKey[key] = decisionID
	return hil.CreateDecisionResult{Decision: decision}, nil
}

func (h *autoplanServiceTestHIL) GetDecisionResolution(_ context.Context, tenantID, decisionID string) (hil.DecisionResolution, error) {
	resolution, ok := h.decisions[decisionID]
	if !ok || resolution.Decision.TenantID != tenantID {
		return hil.DecisionResolution{}, hil.ErrNotFound
	}
	if resolution.Decision.Status == hil.DecisionPending {
		return hil.DecisionResolution{}, hil.ErrConflict
	}
	return resolution, nil
}

func (h *autoplanServiceTestHIL) ConsumeGatewayDecision(_ context.Context, input hil.GatewayReceiptConsumeInput) (hil.DecisionReceipt, error) {
	key := input.TenantID + "|" + input.IdempotencyKey
	decisionID, ok := h.byIdempotencyKey[key]
	if !ok {
		return hil.DecisionReceipt{}, hil.ErrNotFound
	}
	resolution := h.decisions[decisionID]
	if resolution.Decision.Requester != input.Actor || resolution.Decision.Resource != input.Resource || resolution.Decision.ArgsHash != input.ArgsHash ||
		resolution.Decision.Status != hil.DecisionResolved || resolution.Receipt.Effect != hil.EffectAuthorize || !resolution.Receipt.UsedAt.IsZero() {
		return hil.DecisionReceipt{}, hil.ErrConflict
	}
	h.consumeInputs = append(h.consumeInputs, input)
	resolution.Receipt.UsedAt = h.now().UTC()
	h.decisions[decisionID] = resolution
	return resolution.Receipt, nil
}

func (h *autoplanServiceTestHIL) SupersedeDecision(_ context.Context, tenantID, decisionID, reason string) (hil.DecisionResolution, error) {
	resolution, ok := h.decisions[decisionID]
	if !ok || resolution.Decision.TenantID != tenantID {
		return hil.DecisionResolution{}, hil.ErrNotFound
	}
	h.supersedeCalls = append(h.supersedeCalls, decisionID)
	if resolution.Decision.Status == hil.DecisionPending {
		resolution.Decision.Status = hil.DecisionDenied
		resolution.Receipt = hil.DecisionReceipt{
			ID: "superseded-" + decisionID, TenantID: resolution.Decision.TenantID, RequestID: decisionID,
			ArgsHash: resolution.Decision.ArgsHash, Effect: hil.EffectDeny, CreatedAt: h.now().UTC(),
		}
		h.decisions[decisionID] = resolution
	}
	resolution.Actor = hil.ActorRef{Kind: hil.ActorService, ID: "hil:supersede"}
	resolution.Reason = reason
	return resolution, nil
}

func (h *autoplanServiceTestHIL) authorize(decisionID string) error {
	resolution, ok := h.decisions[decisionID]
	if !ok {
		return hil.ErrNotFound
	}
	if resolution.Decision.Status != hil.DecisionPending {
		return hil.ErrConflict
	}
	resolution.Decision.Status = hil.DecisionResolved
	resolution.Receipt = hil.DecisionReceipt{
		ID: "receipt-" + decisionID, TenantID: resolution.Decision.TenantID, RequestID: decisionID,
		ArgsHash: resolution.Decision.ArgsHash, Effect: hil.EffectAuthorize, SingleUse: true, CreatedAt: h.now().UTC(), ExpiresAt: h.now().UTC().Add(15 * time.Minute),
	}
	h.decisions[decisionID] = resolution
	return nil
}

func cloneAutoplanServiceTestPayload(value map[string]any) map[string]any {
	if len(value) == 0 {
		return nil
	}
	copy := make(map[string]any, len(value))
	for key, item := range value {
		copy[key] = item
	}
	return copy
}
