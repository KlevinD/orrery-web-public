package hil

import (
	"context"
	"errors"
	"testing"
	"time"
)

type delegationAuthorityFunc func(context.Context, DecisionRequest, ActorRef, ActorRef) (DelegationEvidence, error)

func (f delegationAuthorityFunc) AuthorizeDelegation(ctx context.Context, d DecisionRequest, h, a ActorRef) (DelegationEvidence, error) {
	return f(ctx, d, h, a)
}

func TestDelegatedDecisionKeepsAgentAttributionAndSingleReceipt(t *testing.T) {
	for _, choice := range []string{"approve", "reject"} {
		t.Run(choice, func(t *testing.T) {
			s, repo, closeStore := newTestHILService(t, OntologyContextSnapshot{Complete: true, EvidenceRefs: []string{"evidence-1"}})
			defer closeStore()
			input := testCreateDecisionInput()
			input.CouncilRequired, input.HILRequired, input.RiskLevel = false, true, RiskLow
			input.PayloadPreview = map[string]any{"approval_assigned_to": "human-1"}
			created, err := s.CreateDecision(context.Background(), input)
			if err != nil {
				t.Fatal(err)
			}
			agent, human := ActorRef{Kind: ActorAgent, ID: "assistant-1"}, ActorRef{Kind: ActorHuman, ID: "human-1"}
			s.delegations = delegationAuthorityFunc(func(_ context.Context, d DecisionRequest, h, a ActorRef) (DelegationEvidence, error) {
				return DelegationEvidence{GrantID: "grant-1", PolicyDigest: "policy-1", TenantID: d.TenantID, OnBehalfOf: h, Agent: a, ExpiresAt: s.now().Add(time.Hour)}, nil
			})
			command := DelegatedDecisionInput{Decision: choice, Reason: "Evidence checked", IdempotencyKey: "review-1", OnBehalfOf: human, ReviewFingerprint: DecisionReviewFingerprint(created.Decision), ReviewedAt: s.now()}
			result, err := s.ResolveDelegatedDecision(context.Background(), input.TenantID, created.Decision.ID, agent, command)
			if err != nil {
				t.Fatal(err)
			}
			if result.Actor != agent {
				t.Fatal("agent impersonated human")
			}
			receipts, err := repo.ListDecisionReceipts(context.Background(), input.TenantID, created.Decision.ID)
			if err != nil || len(receipts) != 1 || receipts[0].ResolverActorKind != ActorAgent || receipts[0].ResolverActorID != agent.ID || receipts[0].ResolverSummary["delegation"] == nil {
				t.Fatalf("missing durable attribution: %+v %v", receipts, err)
			}
			if _, err = s.ResolveDelegatedDecision(context.Background(), input.TenantID, created.Decision.ID, agent, command); !errors.Is(err, ErrConflict) {
				t.Fatalf("replayed decision: %v", err)
			}
		})
	}
}

func TestDelegatedDecisionRejectsStaleOrUnauthorizedReview(t *testing.T) {
	for _, scenario := range []string{"no_grant", "revoked", "wrong_human", "wrong_tenant", "self_review", "stale_review", "future_review", "changed_material", "expired_grant"} {
		t.Run(scenario, func(t *testing.T) {
			s, repo, closeStore := newTestHILService(t, OntologyContextSnapshot{Complete: true, EvidenceRefs: []string{"evidence"}})
			defer closeStore()
			input := testCreateDecisionInput()
			input.CouncilRequired, input.HILRequired, input.RiskLevel = false, true, RiskLow
			input.PayloadPreview = map[string]any{"approval_assigned_to": "human-1"}
			created, err := s.CreateDecision(context.Background(), input)
			if err != nil {
				t.Fatal(err)
			}
			agent, human := ActorRef{Kind: ActorAgent, ID: "assistant-1"}, ActorRef{Kind: ActorHuman, ID: "human-1"}
			s.delegations = delegationAuthorityFunc(func(_ context.Context, d DecisionRequest, h, a ActorRef) (DelegationEvidence, error) {
				if scenario == "revoked" {
					return DelegationEvidence{}, ErrParticipantRequired
				}
				e := DelegationEvidence{GrantID: "grant-1", PolicyDigest: "policy-1", TenantID: d.TenantID, OnBehalfOf: h, Agent: a, ExpiresAt: s.now().Add(time.Hour)}
				if scenario == "expired_grant" {
					e.ExpiresAt = s.now()
				}
				return e, nil
			})
			cmd := DelegatedDecisionInput{Decision: "approve", Reason: "checked", IdempotencyKey: "review-1", OnBehalfOf: human, ReviewFingerprint: DecisionReviewFingerprint(created.Decision), ReviewedAt: s.now()}
			tenant := input.TenantID
			switch scenario {
			case "no_grant":
				s.delegations = nil
			case "wrong_human":
				cmd.OnBehalfOf.ID = "another-human"
			case "wrong_tenant":
				tenant = "other-tenant"
			case "self_review":
				agent = created.Decision.Requester
			case "stale_review":
				cmd.ReviewedAt = s.now().Add(-6 * time.Minute)
			case "future_review":
				cmd.ReviewedAt = s.now().Add(time.Minute)
			case "changed_material":
				cmd.ReviewFingerprint = "old-fingerprint"
			}
			if _, err := s.ResolveDelegatedDecision(context.Background(), tenant, created.Decision.ID, agent, cmd); err == nil {
				t.Fatal("unauthorized delegation accepted")
			}
			stored, _ := repo.GetDecision(context.Background(), input.TenantID, created.Decision.ID)
			if stored.Status != DecisionPending {
				t.Fatal("decision changed on failure")
			}
		})
	}
}

func TestDelegatedEligibilityRejectsRiskAndMandatoryHuman(t *testing.T) {
	base := DecisionRequest{Status: DecisionPending, RiskLevel: RiskLow, Route: RouteDecision{Mode: ModeHIL}, ArgsHash: "hash", Purpose: "Read", Context: OntologyContextSnapshot{Complete: true, EvidenceRefs: []string{"evidence"}}}
	if !DelegatedDecisionEligible(base) {
		t.Fatal("baseline should be eligible")
	}
	for _, mutate := range []func(*DecisionRequest){
		func(d *DecisionRequest) { d.RiskLevel = RiskHigh },
		func(d *DecisionRequest) { d.RiskLevel = "" },
		func(d *DecisionRequest) { d.Context.Complete = false },
		func(d *DecisionRequest) { d.Context.Conflict = true },
		func(d *DecisionRequest) { d.Context.EvidenceRefs = nil },
		func(d *DecisionRequest) { d.Route.Mode = ModeCouncil },
		func(d *DecisionRequest) { d.PayloadPreview = map[string]any{"human_intent_required": true} },
		func(d *DecisionRequest) { d.ActionTypeID = "business.approval.request" },
	} {
		d := base
		mutate(&d)
		if DelegatedDecisionEligible(d) {
			t.Fatalf("unsafe decision eligible: %+v", d)
		}
	}
}

func TestDelegatedDecisionFencesChangesAfterAuthorization(t *testing.T) {
	for _, scenario := range []string{"material_changed", "human_won"} {
		t.Run(scenario, func(t *testing.T) {
			s, repo, closeStore := newTestHILService(t, OntologyContextSnapshot{Complete: true, EvidenceRefs: []string{"evidence"}})
			defer closeStore()
			input := testCreateDecisionInput()
			input.CouncilRequired, input.HILRequired, input.RiskLevel = false, true, RiskLow
			input.PayloadPreview = map[string]any{"approval_assigned_to": "human-1"}
			created, err := s.CreateDecision(context.Background(), input)
			if err != nil {
				t.Fatal(err)
			}
			agent, human := ActorRef{Kind: ActorAgent, ID: "assistant-1"}, ActorRef{Kind: ActorHuman, ID: "human-1"}
			s.delegations = delegationAuthorityFunc(func(ctx context.Context, d DecisionRequest, h, a ActorRef) (DelegationEvidence, error) {
				if scenario == "material_changed" {
					if _, err := repo.store.Exec(ctx, `UPDATE hil_decision_requests SET purpose='changed' WHERE tenant_id=? AND id=?`, d.TenantID, d.ID); err != nil {
						t.Fatal(err)
					}
				} else {
					if _, err := s.ResolveDecision(ctx, d.TenantID, d.ID, human, ResolveDecisionInput{Decision: "reject", IdempotencyKey: "human-first"}); err != nil {
						t.Fatal(err)
					}
				}
				return DelegationEvidence{GrantID: "grant", PolicyDigest: "policy", TenantID: d.TenantID, OnBehalfOf: h, Agent: a, ExpiresAt: s.now().Add(time.Hour)}, nil
			})
			_, err = s.ResolveDelegatedDecision(context.Background(), input.TenantID, created.Decision.ID, agent, DelegatedDecisionInput{Decision: "approve", Reason: "reviewed", IdempotencyKey: "assistant", OnBehalfOf: human, ReviewFingerprint: DecisionReviewFingerprint(created.Decision), ReviewedAt: s.now()})
			if !errors.Is(err, ErrConflict) {
				t.Fatalf("stale result committed: %v", err)
			}
			receipts, err := repo.ListDecisionReceipts(context.Background(), input.TenantID, created.Decision.ID)
			if err != nil {
				t.Fatal(err)
			}
			for _, receipt := range receipts {
				if receipt.ResolverActorKind == ActorAgent {
					t.Fatal("stale agent receipt")
				}
			}
		})
	}
}

func TestDelegatedWorkflowOnlyCompletesOwnersSeatAndKeepsAudit(t *testing.T) {
	for _, finalActor := range []ActorKind{ActorHuman, ActorAgent} {
		t.Run(string(finalActor), func(t *testing.T) { testDelegatedWorkflowSeats(t, finalActor) })
	}
}

func testDelegatedWorkflowSeats(t *testing.T, finalActor ActorKind) {
	t.Helper()
	ctx := context.Background()
	repo, closeStore := openWorkflowRepository(t, "delegated-workflow")
	defer closeStore()
	ids := &sequenceHILIDs{}
	workflow := NewWorkflowService(WorkflowServiceOptions{Repository: repo, IDs: ids, Clock: func() time.Time { return testRepositoryNow }, Assignees: fakeWorkflowAssigneeResolver{results: map[string][]ActorRef{
		"explicit_human:human-1": {{Kind: ActorHuman, ID: "human-1"}}, "explicit_human:human-2": {{Kind: ActorHuman, ID: "human-2"}},
	}}})
	definition := WorkflowDefinition{RiskLevels: []RiskLevel{RiskLow}, Stages: []WorkflowStageDefinition{
		{Key: "review", Name: "Review", Order: 1, Type: WorkflowStageApproval, Approval: &ApprovalStagePolicy{Strategy: ApprovalSingle, Assignees: []WorkflowActorSelector{{Kind: WorkflowSelectorHuman, ID: "human-1"}}, DeadlineMinutes: 60}},
		{Key: "final", Name: "Final", Order: 2, Type: WorkflowStageApproval, Approval: &ApprovalStagePolicy{Strategy: ApprovalSingle, Assignees: []WorkflowActorSelector{{Kind: WorkflowSelectorHuman, ID: "human-2"}}, DeadlineMinutes: 60}},
	}}
	admin := ActorRef{Kind: ActorHuman, ID: "admin"}
	template, err := workflow.SaveTemplateDraft(ctx, admin, SaveTemplateDraftInput{ID: "delegated-template", TenantID: "tenant-1", Name: "Delegated low-risk review", Definition: definition, IdempotencyKey: "save"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := workflow.PublishTemplate(ctx, admin, PublishTemplateInput{Template: template, IdempotencyKey: "publish", Bindings: []WorkflowTemplateBinding{{ID: "delegated-binding", TenantID: "tenant-1", ActionTypeID: "information.read", RiskLevel: RiskLow, TemplateID: template.ID, Active: true}}}); err != nil {
		t.Fatal(err)
	}
	service := NewService(repo, fixedContextProvider{snapshot: OntologyContextSnapshot{Complete: true, EvidenceRefs: []string{"evidence"}}}, ids, WithWorkflowService(workflow), WithClock(func() time.Time { return testRepositoryNow }), WithDelegationAuthority(delegationAuthorityFunc(func(_ context.Context, d DecisionRequest, h, a ActorRef) (DelegationEvidence, error) {
		if h.ID != "human-1" {
			return DelegationEvidence{}, ErrParticipantRequired
		}
		return DelegationEvidence{GrantID: "grant", PolicyDigest: "policy", TenantID: d.TenantID, OnBehalfOf: h, Agent: a, ExpiresAt: testRepositoryNow.Add(time.Hour)}, nil
	})))
	created, err := service.CreateDecision(ctx, CreateDecisionInput{TenantID: "tenant-1", Requester: ActorRef{Kind: ActorAgent, ID: "worker"}, Action: "information.read", ActionTypeID: "information.read", Resource: ObjectRef{Kind: "knowledge", ID: "knowledge-1"}, ArgsHash: "sha256:args", Purpose: "Read checklist", RiskLevel: RiskLow, ApprovalRequired: true, PreferredMode: ModeApproval, IdempotencyKey: "create", ExpiresAt: testRepositoryNow.Add(time.Hour), SubjectRefs: []ObjectRef{{Kind: "knowledge", ID: "knowledge-1"}}, PayloadPreview: map[string]any{"material_hash": "sha256:material"}})
	if err != nil {
		t.Fatal(err)
	}
	detail, err := repo.GetWorkflowInstanceByDecisionID(ctx, "tenant-1", created.Decision.ID)
	if err != nil {
		t.Fatal(err)
	}
	agent := ActorRef{Kind: ActorAgent, ID: "assistant-1"}
	command := DelegatedDecisionInput{Decision: "approve", Reason: "Read-only evidence checked", IdempotencyKey: "delegated-first-seat", OnBehalfOf: ActorRef{Kind: ActorHuman, ID: "human-1"}, ReviewFingerprint: DecisionReviewFingerprint(created.Decision), ReviewedAt: testRepositoryNow, TaskID: detail.Tasks[0].ID, ExpectedWorkflowVersion: detail.Instance.Version}
	result, err := service.ResolveDelegatedDecision(ctx, "tenant-1", created.Decision.ID, agent, command)
	if err != nil || result.Receipt.ID != "" || result.Decision.Status != DecisionPending {
		t.Fatalf("first seat bypassed final stage: %+v %v", result, err)
	}
	detail, err = repo.GetWorkflowInstanceByDecisionID(ctx, "tenant-1", created.Decision.ID)
	if err != nil {
		t.Fatal(err)
	}
	if detail.Instance.Status != WorkflowRunning || detail.Tasks[0].Status != TaskApproved || detail.Tasks[1].Status != TaskPending || len(detail.Receipts) != 0 {
		t.Fatalf("wrong seat state: %+v", detail)
	}
	found := false
	for _, event := range detail.Audit {
		if event.IdempotencyKey == command.IdempotencyKey {
			found = true
			if event.Actor != agent || event.Metadata["delegation"] == nil {
				t.Fatal("missing delegated task audit")
			}
		}
	}
	if !found {
		t.Fatal("task audit not persisted")
	}
	command.TaskID, command.ExpectedWorkflowVersion = detail.Tasks[1].ID, detail.Instance.Version
	if _, err := service.ResolveDelegatedDecision(ctx, "tenant-1", created.Decision.ID, agent, command); err == nil {
		t.Fatal("assistant approved someone else's seat")
	}
	if finalActor == ActorAgent {
		service.SetDelegationAuthority(delegationAuthorityFunc(func(_ context.Context, d DecisionRequest, h, a ActorRef) (DelegationEvidence, error) {
			if h.ID != "human-2" || a.ID != "assistant-2" {
				return DelegationEvidence{}, ErrParticipantRequired
			}
			return DelegationEvidence{GrantID: "grant-human-2", PolicyDigest: "policy-2", TenantID: d.TenantID, OnBehalfOf: h, Agent: a, ExpiresAt: testRepositoryNow.Add(time.Hour)}, nil
		}))
		command.OnBehalfOf.ID, command.IdempotencyKey = "human-2", "delegated-final-seat"
		result, err := service.ResolveDelegatedDecision(ctx, "tenant-1", created.Decision.ID, ActorRef{Kind: ActorAgent, ID: "assistant-2"}, command)
		if err != nil || result.Decision.Status != DecisionResolved || result.Receipt.ResolverActorID != "assistant-2" {
			t.Fatalf("delegated final seat: %+v %v", result, err)
		}
		receipts, err := repo.ListDecisionReceipts(ctx, "tenant-1", created.Decision.ID)
		if err != nil || len(receipts) != 1 || receipts[0].ResolverActorKind != ActorAgent || receipts[0].ResolverActorID != "assistant-2" || receipts[0].ResolverSummary["delegation"] == nil {
			t.Fatalf("delegated final attribution not durable: %+v %v", receipts, err)
		}
		return
	}
	final, err := workflow.DecideTask(ctx, "tenant-1", detail.Tasks[1].ID, ActorRef{Kind: ActorHuman, ID: "human-2"}, DecideWorkflowTaskInput{Decision: WorkflowTaskApprove, Reason: "Final human review", IdempotencyKey: "human-final", ExpectedInstanceVersion: detail.Instance.Version})
	if err != nil || len(final.Receipts) != 1 || final.Receipts[0].ResolverActorKind != ActorHuman {
		t.Fatalf("human final review: %+v %v", final, err)
	}
}
