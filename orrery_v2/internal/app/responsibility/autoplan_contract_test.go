package responsibility

import (
	"testing"
	"time"
)

func TestAutoplanDedupeKeyIgnoresScheduledRunAndTracksSourceSnapshot(t *testing.T) {
	request := testAutoplanRequest()
	first, err := AutoplanDedupeKey(request)
	if err != nil {
		t.Fatalf("AutoplanDedupeKey: %v", err)
	}
	retried := request
	retried.ScheduledRunID = "run-retry"
	second, err := AutoplanDedupeKey(retried)
	if err != nil {
		t.Fatalf("AutoplanDedupeKey retry: %v", err)
	}
	if first != second {
		t.Fatalf("dedupe key changed for repeated pulse: %q != %q", first, second)
	}
	changed := request
	changed.SourceDigest = "sha256:changed"
	third, err := AutoplanDedupeKey(changed)
	if err != nil {
		t.Fatalf("AutoplanDedupeKey changed source: %v", err)
	}
	if third == first {
		t.Fatal("dedupe key did not change for a new source snapshot")
	}
	changedGoal := request
	changedGoal.GoalContextDigest = "sha256:changed-goal"
	fourth, err := AutoplanDedupeKey(changedGoal)
	if err != nil {
		t.Fatalf("AutoplanDedupeKey changed goal: %v", err)
	}
	if fourth == first {
		t.Fatal("dedupe key did not change for a new goal snapshot")
	}
}

func TestAutoplanProposalKeepsLegacyPlansCompatibleAndRequiresGoalAnchorForV2(t *testing.T) {
	legacy := testAutoplanProposal()
	legacy.Version = AutoplanLegacyProposalVersion
	legacy.Steps[0].ParentGoalRef = ""
	legacy.Steps[0].ParentGoal = ""
	if err := legacy.Validate(); err != nil {
		t.Fatalf("legacy proposal: %v", err)
	}
	legacy.Steps[0].ParentGoalRef = "people:position_goal:goal-1:v1"
	legacy.Steps[0].ParentGoal = "Improve delivery reliability"
	if err := legacy.Validate(); err == nil {
		t.Fatal("legacy proposal with unbound parent-goal fields was accepted")
	}

	current := testAutoplanProposal()
	current.Steps[0].ParentGoalRef = ""
	if err := current.Validate(); err == nil {
		t.Fatal("v2 proposal without a parent goal reference was accepted")
	}
}

func TestAutoplanProposalRequiresTraceableAcyclicSteps(t *testing.T) {
	proposal := testAutoplanProposal()
	if err := proposal.Validate(); err != nil {
		t.Fatalf("valid proposal: %v", err)
	}
	proposal.Steps[0].DependsOn = []string{"follow-up"}
	proposal.Steps = append(proposal.Steps, AutoplanStep{
		StepID: "follow-up", Title: "Follow up", Goal: "Complete the follow-up", AssigneeAgentID: "agent-1",
		ParentGoalRef: proposal.Steps[0].ParentGoalRef, ParentGoal: proposal.Steps[0].ParentGoal,
		Priority: 50, RiskLevel: AutoplanRiskLow, DependsOn: []string{"root"},
		AcceptanceCriteria: []string{"Follow-up evidence exists"},
		EvidenceRequirements: []AutoplanEvidenceRequirement{{
			EvidenceRequirementID: "evidence-follow-up", AcceptanceCriterion: "Follow-up evidence exists", Kind: "tool_log", Required: true,
		}},
	})
	if err := proposal.Validate(); err == nil {
		t.Fatal("cyclic proposal was accepted")
	}
}

func TestAutoplanRecordValidatesPlanDigestAndMaterializationReceipts(t *testing.T) {
	now := time.Date(2026, time.August, 14, 10, 0, 0, 0, time.UTC)
	record, err := NewAutoplanRecord(testAutoplanRequest(), now)
	if err != nil {
		t.Fatalf("NewAutoplanRecord: %v", err)
	}
	if err := record.Validate(); err != nil {
		t.Fatalf("planning record: %v", err)
	}
	proposal := testAutoplanProposal()
	digest, err := AutoplanProposalDigest(proposal)
	if err != nil {
		t.Fatalf("AutoplanProposalDigest: %v", err)
	}
	record.Status = AutoplanStatusMaterialized
	record.Version++
	record.UpdatedAt = now.Add(time.Minute)
	record.Proposal = proposal
	record.ProposalDigest = digest
	record.ApprovedProposalDigest = digest
	record.Materializations = []AutoplanMaterialization{{
		StepID: "root", MissionID: "mission-1", TaskID: "task-1", ResponsibilityID: "responsibility-1", MaterializedAt: now.Add(time.Minute),
	}}
	if err := record.Validate(); err != nil {
		t.Fatalf("materialized record: %v", err)
	}
	record.Materializations[0].TaskID = ""
	if err := record.Validate(); err == nil {
		t.Fatal("record with incomplete materialization receipt was accepted")
	}
}

func TestAutoplanProposalDigestPreservesLegacyV1Schema(t *testing.T) {
	proposal := testAutoplanProposal()
	proposal.Version = AutoplanLegacyProposalVersion
	proposal.Steps[0].ParentGoalRef = ""
	proposal.Steps[0].ParentGoal = ""
	digest, err := AutoplanProposalDigest(proposal)
	if err != nil {
		t.Fatalf("AutoplanProposalDigest: %v", err)
	}
	const want = "sha256:3ef66346c9940fc0291860df3ec7d9a2d2ef83674c9ab56b561d269415b50b70"
	if digest != want {
		t.Fatalf("legacy digest = %q, want %q", digest, want)
	}
}

func TestAutoplanCanBeSupersededBeforeProposalAndCannotLeaveTerminalState(t *testing.T) {
	now := time.Date(2026, time.August, 14, 10, 0, 0, 0, time.UTC)
	record, err := NewAutoplanRecord(testAutoplanRequest(), now)
	if err != nil {
		t.Fatalf("NewAutoplanRecord: %v", err)
	}
	if err := ValidateAutoplanTransition(AutoplanStatusPlanning, AutoplanStatusSuperseded); err != nil {
		t.Fatalf("planning -> superseded: %v", err)
	}
	record.Status = AutoplanStatusSuperseded
	record.SupersededBy = "portfolio-version:binding-1:v2"
	record.Version++
	record.UpdatedAt = now.Add(time.Minute)
	if err := record.Validate(); err != nil {
		t.Fatalf("superseded pre-proposal record: %v", err)
	}
	if err := ValidateAutoplanTransition(AutoplanStatusMaterialized, AutoplanStatusPlanning); err == nil {
		t.Fatal("terminal materialized plan was allowed to restart")
	}
}

func TestAutoplanPolicyRequiresExplicitContinuousOptIn(t *testing.T) {
	base := DefaultPolicy("tenant-1", "agent-1", WorkKindMissionTask)
	base.Enabled = true
	base.AutonomyLevel = AutonomyExecuteScoped
	if derived := AutoplanPolicyFromResponsibility(base); derived.Enabled {
		t.Fatal("scoped execution policy unexpectedly enabled autonomous planning")
	}
	base.AutonomyLevel = AutonomyContinuous
	if derived := AutoplanPolicyFromResponsibility(base); !derived.Enabled {
		t.Fatal("enabled continuous responsibility did not opt into autonomous planning")
	}
	policy := DefaultAutoplanPolicy("agent-1")
	if policy.Enabled {
		t.Fatal("default autonomous planning policy must be disabled")
	}
	decision, err := EvaluateAutoplanPolicy(policy, testAutoplanProposal())
	if err != nil {
		t.Fatalf("EvaluateAutoplanPolicy: %v", err)
	}
	if decision.Allowed {
		t.Fatal("disabled autonomous planning policy accepted a proposal")
	}
	policy.Enabled = true
	decision, err = EvaluateAutoplanPolicy(policy, testAutoplanProposal())
	if err != nil {
		t.Fatalf("EvaluateAutoplanPolicy enabled: %v", err)
	}
	if !decision.Allowed || decision.ApprovalRequired {
		t.Fatalf("low-risk scoped proposal decision = %+v", decision)
	}
	highRisk := testAutoplanProposal()
	highRisk.Steps[0].RiskLevel = AutoplanRiskHigh
	decision, err = EvaluateAutoplanPolicy(policy, highRisk)
	if err != nil {
		t.Fatalf("EvaluateAutoplanPolicy high risk: %v", err)
	}
	if !decision.Allowed || decision.ApprovalRequired {
		t.Fatalf("high-risk proposal decision = %+v", decision)
	}
	policy.ApprovalPolicy = ApprovalNone
	decision, err = EvaluateAutoplanPolicy(policy, highRisk)
	if err != nil {
		t.Fatalf("EvaluateAutoplanPolicy high risk without configured approval: %v", err)
	}
	if !decision.Allowed || decision.ApprovalRequired {
		t.Fatalf("high-risk system proposal required HIL when approval policy was none: %+v", decision)
	}
	sideEffect := testAutoplanProposal()
	sideEffect.Steps[0].HasSideEffect = true
	decision, err = EvaluateAutoplanPolicy(policy, sideEffect)
	if err != nil {
		t.Fatalf("EvaluateAutoplanPolicy side effect without configured approval: %v", err)
	}
	if !decision.Allowed || decision.ApprovalRequired {
		t.Fatalf("side-effect system proposal required HIL when approval policy was none: %+v", decision)
	}
	for _, scope := range []string{
		"enterprise_graph.read", "knowledge.read", "knowledge.write", "memory.read", "ontology.manage", "scheduler.read", "repository.read",
	} {
		toolScoped := testAutoplanProposal()
		toolScoped.Steps[0].ExecutionScopes = []string{scope}
		decision, err = EvaluateAutoplanPolicy(policy, toolScoped)
		if err != nil {
			t.Fatalf("EvaluateAutoplanPolicy %s work without configured approval: %v", scope, err)
		}
		if !decision.Allowed || decision.ApprovalRequired {
			t.Fatalf("authorized system proposal %s required HIL when approval policy was none: %+v", scope, decision)
		}
	}
	businessDecision := testAutoplanProposal()
	businessDecision.RequiredApprovals = []string{"Confirm the business meaning before applying the proposal"}
	decision, err = EvaluateAutoplanPolicy(policy, businessDecision)
	if err != nil {
		t.Fatalf("EvaluateAutoplanPolicy explicit business decision: %v", err)
	}
	if !decision.Allowed || !decision.ApprovalRequired {
		t.Fatalf("explicit business decision did not require approval: %+v", decision)
	}
}

func testAutoplanRequest() AutoplanRequest {
	return AutoplanRequest{
		TenantID: "tenant-1", PortfolioID: "portfolio-1", PortfolioBindingID: "binding-1", PortfolioVersionID: "binding-1:v1",
		SourceRevision: "1", SourceDigest: "sha256:source", GoalContextID: "goal-context-1", GoalContextDigest: "sha256:goal",
		AgentProfileID: "agent-1", ActorMemberID: "member-1",
		PolicyID: "policy-1", PolicyVersion: 1, TriggerKind: "portfolio_pulse", TriggerRef: "binding-1:v1",
		ScheduledTaskID: "pulse-1", ScheduledRunID: "run-1", TriggeredAt: time.Date(2026, time.August, 14, 10, 0, 0, 0, time.UTC),
	}
}

func testAutoplanProposal() AutoplanProposal {
	return AutoplanProposal{
		Version: AutoplanProposalVersion, Summary: "Produce the current responsibility deliverable", Rationale: "The portfolio contains a concrete outcome.",
		SourceRefs: []string{"people:responsibility:output-1", "people:position_goal:goal-1:v1"},
		Steps: []AutoplanStep{{
			StepID: "root", Title: "Produce deliverable", Description: "Create the verifiable responsibility output.", Goal: "Produce the current deliverable",
			ParentGoalRef: "people:position_goal:goal-1:v1", ParentGoal: "Improve delivery reliability",
			AssigneeAgentID: "agent-1", Priority: 50, RiskLevel: AutoplanRiskLow,
			AcceptanceCriteria: []string{"A traceable deliverable is recorded"},
			EvidenceRequirements: []AutoplanEvidenceRequirement{{
				EvidenceRequirementID: "evidence-root", AcceptanceCriterion: "A traceable deliverable is recorded", Kind: "tool_log", Required: true,
			}},
		}},
	}
}
