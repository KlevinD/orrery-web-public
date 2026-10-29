package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"orrery_v2/internal/app/actioncontract"
	"orrery_v2/internal/app/authorization"
	"orrery_v2/internal/app/hil"
	"orrery_v2/internal/app/identity"
	"orrery_v2/internal/app/ontologyscope"
	contextview "orrery_v2/internal/app/ontologyview/context"
	ontologycontract "orrery_v2/internal/app/ontologyview/contract"
	"orrery_v2/internal/app/reliability"
	"orrery_v2/internal/platform/idgen"
	httptransport "orrery_v2/internal/transport/http"
)

type reliabilityTestBindingResolver struct{}

func (reliabilityTestBindingResolver) Resolve(_ context.Context, tenantID string) (ontologyscope.ScopeBinding, error) {
	return ontologyscope.ScopeBinding{
		TenantID: tenantID, Kind: ontologyscope.KindOrganization,
		SourceObject: identity.ObjectRef{Type: "organization", ID: tenantID}, AuthorizationTenantID: tenantID,
		ReadAction: "organization.read", ManageAction: "organization.manage", BindingVersion: 1, State: ontologyscope.BindingStateActive,
	}, nil
}

type reliabilityTestAccessAuthorizer struct{}

func (reliabilityTestAccessAuthorizer) AuthorizeFeature(context.Context, identity.Principal, string) error {
	return nil
}

func (reliabilityTestAccessAuthorizer) AuthorizeObject(context.Context, identity.Principal, string, identity.ObjectRef, string) error {
	return nil
}

type reliabilityTestSnapshots struct{}

func (reliabilityTestSnapshots) Capture(_ context.Context, principal identity.Principal) (authorization.AuthorizationSnapshot, error) {
	return authorization.AuthorizationSnapshot{
		PrincipalKey: authorization.PrincipalCacheKey(principal), AuthorizationRevision: "auth-1", PolicyRevision: "policy-1",
	}, nil
}

type reliabilityTestDecisions struct {
	nextID   int
	items    map[string]hil.DecisionRequest
	receipts map[string]hil.DecisionReceipt
	created  hil.CreateDecisionInput
	consumed int
}

func newReliabilityTestDecisions() *reliabilityTestDecisions {
	return &reliabilityTestDecisions{items: map[string]hil.DecisionRequest{}, receipts: map[string]hil.DecisionReceipt{}}
}

func (d *reliabilityTestDecisions) CreateDecision(_ context.Context, input hil.CreateDecisionInput) (hil.CreateDecisionResult, error) {
	d.created = input
	d.nextID++
	id := "repair-decision-" + string(rune('0'+d.nextID))
	decision := hil.DecisionRequest{
		ID: id, TenantID: input.TenantID, Requester: input.Requester, Action: input.Action, ActionTypeID: input.ActionTypeID,
		Resource: input.Resource, ObjectRefs: append([]hil.ObjectRef(nil), input.ObjectRefs...), ArgsHash: input.ArgsHash,
		Purpose: input.Purpose, RiskLevel: input.RiskLevel, PreferredMode: input.PreferredMode,
		Route: hil.RouteDecision{Mode: hil.ModeApproval, Reasons: []string{"test approval"}}, Context: hil.OntologyContextSnapshot{BundleID: "test", Complete: true},
		PayloadPreview: input.PayloadPreview, Status: hil.DecisionPending, CreatedAt: time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC), ExpiresAt: input.ExpiresAt,
	}
	d.items[id] = decision
	return hil.CreateDecisionResult{Decision: decision}, nil
}

func (d *reliabilityTestDecisions) ListDecisions(_ context.Context, query hil.DecisionListQuery) (hil.DecisionPage, error) {
	page := hil.DecisionPage{Items: []hil.DecisionRequest{}}
	for _, item := range d.items {
		if item.TenantID == query.TenantID {
			page.Items = append(page.Items, item)
		}
	}
	page.Total = len(page.Items)
	return page, nil
}

func (d *reliabilityTestDecisions) GetDecision(_ context.Context, tenantID, decisionID string) (hil.DecisionRequest, error) {
	decision, ok := d.items[decisionID]
	if !ok || decision.TenantID != tenantID {
		return hil.DecisionRequest{}, hil.ErrNotFound
	}
	return decision, nil
}

func (d *reliabilityTestDecisions) ResolveDecision(_ context.Context, tenantID, decisionID string, actor hil.ActorRef, input hil.ResolveDecisionInput) (hil.DecisionResolution, error) {
	decision, err := d.GetDecision(context.Background(), tenantID, decisionID)
	if err != nil {
		return hil.DecisionResolution{}, err
	}
	if actor.Kind != hil.ActorHuman || actor.ID == "" {
		return hil.DecisionResolution{}, hil.ErrParticipantRequired
	}
	effect := hil.EffectAuthorize
	singleUse := true
	if input.Decision == "reject" || input.Decision == "deny" {
		decision.Status = hil.DecisionDenied
		effect = hil.EffectDeny
		singleUse = false
	} else {
		decision.Status = hil.DecisionResolved
	}
	receipt := hil.DecisionReceipt{
		ID: "receipt-" + decisionID, TenantID: tenantID, RequestID: decisionID, ArgsHash: decision.ArgsHash,
		Effect: effect, SingleUse: singleUse, CreatedAt: decision.CreatedAt, ExpiresAt: decision.ExpiresAt,
	}
	d.items[decisionID], d.receipts[decisionID] = decision, receipt
	return hil.DecisionResolution{Decision: decision, Receipt: receipt, Actor: actor, Reason: input.Reason}, nil
}

func (d *reliabilityTestDecisions) GetActionDecisionReceipt(_ context.Context, tenantID, decisionID string, actor hil.ActorRef) (hil.DecisionReceipt, error) {
	decision, err := d.GetDecision(context.Background(), tenantID, decisionID)
	if err != nil {
		return hil.DecisionReceipt{}, err
	}
	if decision.Requester != actor {
		return hil.DecisionReceipt{}, hil.ErrParticipantRequired
	}
	receipt, ok := d.receipts[decisionID]
	if !ok || !receipt.UsedAt.IsZero() {
		return hil.DecisionReceipt{}, hil.ErrNotFound
	}
	return receipt, nil
}

func (d *reliabilityTestDecisions) ConsumeActionDecision(_ context.Context, input hil.ActionReceiptConsumeInput) (hil.DecisionReceipt, error) {
	decision, err := d.GetDecision(context.Background(), input.TenantID, input.DecisionID)
	if err != nil {
		return hil.DecisionReceipt{}, err
	}
	receipt, ok := d.receipts[input.DecisionID]
	if !ok || receipt.ID != input.ReceiptID || !receipt.UsedAt.IsZero() {
		return hil.DecisionReceipt{}, hil.ErrConflict
	}
	if decision.Status != hil.DecisionResolved || decision.Requester != input.Actor || decision.Action != input.Action ||
		decision.ActionTypeID != input.ActionTypeID || decision.ArgsHash != input.ArgsHash || decision.Purpose != input.Purpose ||
		decision.RiskLevel != input.RiskLevel || len(decision.ObjectRefs) != len(input.ObjectRefs) {
		return hil.DecisionReceipt{}, hil.ErrConflict
	}
	for _, wanted := range decision.ObjectRefs {
		found := false
		for _, candidate := range input.ObjectRefs {
			found = found || candidate == wanted
		}
		if !found {
			return hil.DecisionReceipt{}, hil.ErrConflict
		}
	}
	receipt.UsedAt = decision.CreatedAt.Add(time.Minute)
	d.receipts[input.DecisionID] = receipt
	d.consumed++
	return receipt, nil
}

type reliabilityTestActionAuthorization struct{}

func (reliabilityTestActionAuthorization) VerifyActionAuthorization(context.Context, actioncontract.StartRunRequest, actioncontract.ActionType) error {
	return nil
}

func reliabilityTestGate() *ontologyscope.ScopeAccessGate {
	return ontologyscope.NewScopeAccessGate(reliabilityTestBindingResolver{}, reliabilityTestAccessAuthorizer{}, reliabilityTestSnapshots{})
}

func reliabilityTestPrincipal() identity.Principal {
	return identity.Principal{
		SubjectProvider: "kratos", Subject: "identity-1", UserID: "operator-1", Username: "operator", DisplayName: "Operator", ViewerKind: identity.ViewerKindUser,
	}
}

type reliabilityEvidenceContext struct {
	neighborhood contextview.Neighborhood
}

func (c *reliabilityEvidenceContext) ListObjects(context.Context, identity.Principal, contextview.ObjectListQuery) (ontologycontract.CursorPage[contextview.ObjectItem], error) {
	return ontologycontract.CursorPage[contextview.ObjectItem]{}, nil
}

func (c *reliabilityEvidenceContext) GetObject(context.Context, identity.Principal, string, string) (contextview.ObjectDetail, error) {
	return contextview.ObjectDetail{}, nil
}

func (c *reliabilityEvidenceContext) Neighborhood(context.Context, identity.Principal, contextview.NeighborhoodQuery) (contextview.Neighborhood, error) {
	return c.neighborhood, nil
}

func TestProductionOntologyReliabilityAPIGatesLearnedRulesOnVisibleConfirmedEvidence(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 9, 1, 9, 0, 0, 0, time.UTC)
	repository, incident, _ := testRepairDryRunReliabilityState(t, ctx, now, reliability.RepairStrategyRetry)
	evidenceID := "failure-observation-evidence:" + incident.ID + ":1"
	contextAPI := &reliabilityEvidenceContext{neighborhood: contextview.Neighborhood{
		RootID: incident.ID,
		Nodes: []contextview.NeighborhoodNode{
			{ObjectItem: contextview.ObjectItem{ID: incident.ID, ObjectType: "failure_incident", Label: incident.RedactedSummary}},
			{ObjectItem: contextview.ObjectItem{ID: evidenceID, ObjectType: "evidence_item", Label: "provider timeout", Status: "observed", Confidence: 1}},
		},
	}}
	control := reliability.NewService(repository, reliability.WithClock(func() time.Time { return now }))
	api := newProductionOntologyReliabilityAPI(repository, control, contextAPI, reliabilityTestGate(), newReliabilityTestDecisions(), nil, func() time.Time { return now })

	detail, err := api.GetIncident(ctx, reliabilityTestPrincipal(), incident.TenantID, incident.ID)
	if err != nil || len(detail.AvailableEvidence) != 1 || detail.AvailableEvidence[0].ID != evidenceID {
		t.Fatalf("available evidence detail=%+v err=%v", detail, err)
	}
	diagnosis, err := api.CreateDiagnosis(ctx, reliabilityTestPrincipal(), incident.TenantID, incident.ID, httptransport.OntologyReliabilityDiagnosisCreateRequest{
		RootCauseCategory: "transient", RedactedSummary: "upstream provider timeout", Confidence: 0.84,
		EvidenceIDs: []string{evidenceID}, IdempotencyKey: "diagnosis-create-1",
	})
	if err != nil || diagnosis.Status != "proposed" || diagnosis.IncidentVersion != incident.Version {
		t.Fatalf("create diagnosis=%+v err=%v", diagnosis, err)
	}
	agent := identity.Principal{ViewerKind: identity.ViewerKindAgent, AgentProfileID: "diagnoser-1", Subject: "diagnoser-1"}
	if _, err := api.DecideDiagnosis(ctx, agent, incident.TenantID, incident.ID, diagnosis.ID, httptransport.OntologyReliabilityDiagnosisDecisionRequest{
		Decision: "confirm", Reason: "self-confirm", IdempotencyKey: "diagnosis-agent-confirm-1",
	}); !errors.Is(err, ontologyscope.ErrAccessDenied) {
		t.Fatalf("agent diagnosis decision error=%v, want access denied", err)
	}
	confirmed, err := api.DecideDiagnosis(ctx, reliabilityTestPrincipal(), incident.TenantID, incident.ID, diagnosis.ID, httptransport.OntologyReliabilityDiagnosisDecisionRequest{
		Decision: "confirm", Reason: "evidence reviewed", IdempotencyKey: "diagnosis-confirm-1",
	})
	if err != nil || confirmed.Status != "confirmed" || confirmed.ReviewedBy == nil || confirmed.ReviewedBy.Kind != "human" {
		t.Fatalf("confirm diagnosis=%+v err=%v", confirmed, err)
	}
	rule, err := api.ProposeRepairRule(ctx, reliabilityTestPrincipal(), incident.TenantID, incident.ID, diagnosis.ID, httptransport.OntologyReliabilityRuleProposalRequest{
		Strategy: "retry", Reversible: true, Idempotent: true, IdempotencyKey: "rule-proposal-1",
	})
	if err != nil || rule.DiagnosisCaseID != diagnosis.ID || rule.Status != "proposed" || rule.RiskLevel != "high" || rule.AutoApply {
		t.Fatalf("proposed rule=%+v err=%v", rule, err)
	}

	if _, err := api.CreateDiagnosis(ctx, reliabilityTestPrincipal(), incident.TenantID, incident.ID, httptransport.OntologyReliabilityDiagnosisCreateRequest{
		RootCauseCategory: "transient", RedactedSummary: "unbound claim", Confidence: 0.5,
		EvidenceIDs: []string{"evidence-not-linked"}, IdempotencyKey: "diagnosis-create-unbound",
	}); !errors.Is(err, ontologycontract.ErrInvalidRequest) {
		t.Fatalf("unbound evidence error=%v, want invalid request", err)
	}
}

func TestProductionOntologyReliabilityAPIExposesExactBindingAndRejectsStaleDecision(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
	repository, incident, rule := testRepairDryRunReliabilityState(t, ctx, now, reliability.RepairStrategyRetry)
	decisions := newReliabilityTestDecisions()
	api := newProductionOntologyReliabilityAPI(repository, reliability.NewService(repository), nil, reliabilityTestGate(), decisions, nil, func() time.Time { return now })

	detail, err := api.GetIncident(ctx, reliabilityTestPrincipal(), incident.TenantID, incident.ID)
	if err != nil {
		t.Fatalf("GetIncident: %v", err)
	}
	if detail.Target == nil || detail.Target.RunID != "run-failed" || len(detail.Rules) != 1 || !detail.Rules[0].DryRunReady || detail.Rules[0].ApplyReady {
		t.Fatalf("detail = %+v", detail)
	}
	authorization, err := api.RequestRepairAuthorization(ctx, reliabilityTestPrincipal(), incident.TenantID, incident.ID, rule.ID, httptransport.OntologyReliabilityAuthorizationRequest{
		ExecutionMode: "dry_run", Purpose: "verify exact retry", IdempotencyKey: "request-preflight-1",
	})
	if err != nil {
		t.Fatalf("RequestRepairAuthorization: %v", err)
	}
	if authorization.ID == "" || decisions.created.Action != "ontology.action.dry_run" || decisions.created.ArgsHash != detail.Rules[0].ArgsHash || len(decisions.created.ObjectRefs) != 2 {
		t.Fatalf("authorization=%+v input=%+v", authorization, decisions.created)
	}

	retryable := true
	control := reliability.NewService(repository, reliability.WithClock(func() time.Time { return now.Add(time.Minute) }))
	if _, err := control.ObserveFailure(ctx, reliability.FailureObservation{
		ID: "new-observation", TenantID: incident.TenantID, Source: "agent_runtime", Component: "pi", ErrorCode: "timeout",
		Message: "provider timed out again", Retryable: &retryable, OccurredAt: now.Add(time.Minute),
		RepairTarget: &reliability.RepairTarget{Kind: reliability.RepairTargetMissionAgentRun, AuthorizationTenantID: "tenant-a", MissionID: "mission-1", RunID: "run-new"},
	}); err != nil {
		t.Fatalf("ObserveFailure: %v", err)
	}
	_, err = api.ResolveRepairAuthorization(ctx, reliabilityTestPrincipal(), incident.TenantID, incident.ID, rule.ID, authorization.ID, httptransport.OntologyReliabilityAuthorizationDecisionRequest{
		Decision: "approve", IdempotencyKey: "resolve-stale-1",
	})
	if !errors.Is(err, ontologycontract.ErrStateConflict) {
		t.Fatalf("stale decision error = %v, want state conflict", err)
	}
}

func TestProductionOntologyReliabilityAPIInvokesApprovedApplyOnce(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 9, 1, 11, 0, 0, 0, time.UTC)
	repository, incident, proposed := testRepairDryRunReliabilityState(t, ctx, now, reliability.RepairStrategyRetry)
	active := proposed
	active.Status = reliability.RepairRuleStatusActive
	active.UpdatedAt = now
	active, _, err := repository.UpsertRepairRule(ctx, "activate-retry", active)
	if err != nil {
		t.Fatalf("activate rule: %v", err)
	}
	target, err := repository.GetLatestRepairTarget(ctx, incident.TenantID, incident.ID)
	if err != nil {
		t.Fatalf("GetLatestRepairTarget: %v", err)
	}
	apiForBinding := newProductionOntologyReliabilityAPI(repository, reliability.NewService(repository), nil, reliabilityTestGate(), newReliabilityTestDecisions(), nil, func() time.Time { return now })
	material, err := apiForBinding.loadMaterial(ctx, incident.TenantID, incident.ID)
	if err != nil {
		t.Fatal(err)
	}
	binding, err := apiForBinding.bindingForRule(material, active.ID)
	if err != nil {
		t.Fatal(err)
	}
	preflight, _, err := repository.RecordRepairPreflight(ctx, reliability.RepairPreflight{
		EvidenceID: "preflight-1", TenantID: incident.TenantID, ActionRunID: "dry-run-1", IncidentID: incident.ID, IncidentVersion: incident.Version,
		RuleID: active.ID, RuleVersion: active.Version, Strategy: active.Strategy, ArgsHash: binding.argsHash,
		TargetObservationID: target.ObservationID, TargetKind: target.Kind, TargetRunID: target.RunID,
		Passed: true, CheckedAt: now, ExpiresAt: now.Add(20 * time.Minute),
	})
	if err != nil {
		t.Fatalf("RecordRepairPreflight: %v", err)
	}

	actionRepository := actioncontract.NewMemoryRepository()
	actionService := actioncontract.NewService(actionRepository, actioncontract.WithClock(func() time.Time { return now }), actioncontract.WithAuthorizationVerifier(reliabilityTestActionAuthorization{}))
	if err := actioncontract.InstallDefaultActionTypes(ctx, actionService); err != nil {
		t.Fatal(err)
	}
	executor := &recordingOntologyRepairExecutor{}
	executions := newProductionOntologyExecutionAPI(&fakeOntologyExecutionContext{details: map[string]contextview.ObjectDetail{
		incident.ID:          {ObjectItem: contextview.ObjectItem{ObjectType: "failure_incident"}},
		active.ID:            {ObjectItem: contextview.ObjectItem{ObjectType: "repair_rule"}},
		preflight.EvidenceID: {ObjectItem: contextview.ObjectItem{ObjectType: "evidence_item"}},
	}})
	actionAPI := newProductionOntologyActionAPI(actionRepository, actionService, &recordingOntologyActionAuthorizer{}, idgen.NewDeterministic("reliability-workspace"), executions, executor)
	decisions := newReliabilityTestDecisions()
	api := newProductionOntologyReliabilityAPI(repository, reliability.NewService(repository), nil, reliabilityTestGate(), decisions, actionAPI, func() time.Time { return now })

	detail, err := api.GetIncident(ctx, reliabilityTestPrincipal(), incident.TenantID, incident.ID)
	if err != nil || len(detail.Rules) != 1 || !detail.Rules[0].ApplyReady || len(detail.Preflights) != 1 || !detail.Preflights[0].Current {
		t.Fatalf("apply detail=%+v err=%v", detail, err)
	}
	authorization, err := api.RequestRepairAuthorization(ctx, reliabilityTestPrincipal(), incident.TenantID, incident.ID, active.ID, httptransport.OntologyReliabilityAuthorizationRequest{
		ExecutionMode: "apply", IdempotencyKey: "request-apply-1",
	})
	if err != nil {
		t.Fatalf("RequestRepairAuthorization: %v", err)
	}
	if _, err := api.ResolveRepairAuthorization(ctx, reliabilityTestPrincipal(), incident.TenantID, incident.ID, active.ID, authorization.ID, httptransport.OntologyReliabilityAuthorizationDecisionRequest{
		Decision: "approve", IdempotencyKey: "test-idempotency-key",
	}); err != nil {
		t.Fatalf("ResolveRepairAuthorization: %v", err)
	}
	first, err := api.InvokeAuthorizedRepair(ctx, reliabilityTestPrincipal(), incident.TenantID, incident.ID, active.ID, authorization.ID, httptransport.OntologyReliabilityInvokeRequest{IdempotencyKey: "test-idempotency-key"})
	if err != nil {
		t.Fatalf("InvokeAuthorizedRepair: %v", err)
	}
	second, err := api.InvokeAuthorizedRepair(ctx, reliabilityTestPrincipal(), incident.TenantID, incident.ID, active.ID, authorization.ID, httptransport.OntologyReliabilityInvokeRequest{IdempotencyKey: "test-idempotency-key-retry"})
	if err != nil || second.ID != first.ID || executor.executeCalls != 1 {
		t.Fatalf("idempotent invoke first=%+v second=%+v calls=%d err=%v", first, second, executor.executeCalls, err)
	}
}

func TestProductionOntologyReliabilityAPIActivatesOnlyCurrentPreflightedRuleThroughSeparateReview(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 9, 2, 8, 0, 0, 0, time.UTC)
	repository, incident, rule, preflight := testActivationReadyRepairRule(t, ctx, now)
	decisions := newReliabilityTestDecisions()
	authorizer := productionRepairRuleActivationAuthorizer{repository: repository, decisions: decisions, now: func() time.Time { return now }}
	control := reliability.NewService(repository, reliability.WithClock(func() time.Time { return now }), reliability.WithRepairRuleActivationAuthorizer(authorizer))
	api := newProductionOntologyReliabilityAPI(repository, control, nil, reliabilityTestGate(), decisions, nil, func() time.Time { return now })

	detail, err := api.GetIncident(ctx, reliabilityTestPrincipal(), incident.TenantID, incident.ID)
	if err != nil || len(detail.Rules) != 1 || !detail.Rules[0].ActivationReady || len(detail.Rules[0].ActivationBlockers) != 0 {
		t.Fatalf("activation-ready detail=%+v err=%v", detail, err)
	}
	agent := identity.Principal{ViewerKind: identity.ViewerKindAgent, AgentProfileID: "learner-1", Subject: "learner-1"}
	if _, err := api.RequestRuleActivationReview(ctx, agent, incident.TenantID, rule.ID, httptransport.OntologyReliabilityActivationReviewRequest{IdempotencyKey: "agent-activation-review"}); !errors.Is(err, ontologyscope.ErrAccessDenied) {
		t.Fatalf("agent activation review error=%v, want access denied", err)
	}
	review, err := api.RequestRuleActivationReview(ctx, reliabilityTestPrincipal(), incident.TenantID, rule.ID, httptransport.OntologyReliabilityActivationReviewRequest{
		Purpose: "promote preflighted retry rule", IdempotencyKey: "activation-review-1",
	})
	if err != nil {
		t.Fatalf("RequestRuleActivationReview: %v", err)
	}
	if review.RuleID != rule.ID || review.RuleVersion != rule.Version || review.PreflightEvidenceID != preflight.EvidenceID ||
		decisions.created.Action != productionRepairRuleActivationAction || decisions.created.ActionTypeID != productionRepairRuleActivationActionTypeID ||
		decisions.created.ArgsHash == "" || decisions.created.ArgsHash == detail.Rules[0].ArgsHash || len(decisions.created.ObjectRefs) < 4 {
		t.Fatalf("activation review=%+v input=%+v", review, decisions.created)
	}
	if _, err := api.ResolveRuleActivationReview(ctx, reliabilityTestPrincipal(), incident.TenantID, rule.ID, review.ID, httptransport.OntologyReliabilityAuthorizationDecisionRequest{
		Decision: "authorize", Reason: "diagnosis and preflight verified", IdempotencyKey: "activation-review-decision-1",
	}); err != nil {
		t.Fatalf("ResolveRuleActivationReview: %v", err)
	}
	activated, err := api.ActivateRepairRule(ctx, reliabilityTestPrincipal(), incident.TenantID, rule.ID, review.ID, httptransport.OntologyReliabilityActivationRequest{IdempotencyKey: "activate-rule-1"})
	if err != nil {
		t.Fatalf("ActivateRepairRule: %v", err)
	}
	if activated.Status != "active" || activated.ActivationDecisionID != review.ID || activated.ActivatedBy == nil ||
		activated.ActivatedBy.Kind != "human" || activated.AutoApply || activated.ActivationReady || activated.ApplyReady || decisions.consumed != 1 {
		t.Fatalf("activated rule=%+v consumed=%d", activated, decisions.consumed)
	}
	replayed, err := api.ActivateRepairRule(ctx, reliabilityTestPrincipal(), incident.TenantID, rule.ID, review.ID, httptransport.OntologyReliabilityActivationRequest{IdempotencyKey: "activate-rule-retry"})
	if err != nil || replayed.Version != activated.Version || decisions.consumed != 1 {
		t.Fatalf("activation replay=%+v consumed=%d err=%v", replayed, decisions.consumed, err)
	}
	detail, err = api.GetIncident(ctx, reliabilityTestPrincipal(), incident.TenantID, incident.ID)
	if err != nil || len(detail.ActivationReviews) != 1 || !detail.ActivationReviews[0].Activated || detail.ActivationReviews[0].Current || len(detail.Authorizations) != 0 {
		t.Fatalf("activated detail=%+v err=%v", detail, err)
	}
}

func TestProductionOntologyReliabilityAPIRejectsStaleActivationReview(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 9, 2, 9, 0, 0, 0, time.UTC)
	repository, incident, rule, _ := testActivationReadyRepairRule(t, ctx, now)
	decisions := newReliabilityTestDecisions()
	authorizer := productionRepairRuleActivationAuthorizer{repository: repository, decisions: decisions, now: func() time.Time { return now.Add(time.Minute) }}
	control := reliability.NewService(repository, reliability.WithClock(func() time.Time { return now.Add(time.Minute) }), reliability.WithRepairRuleActivationAuthorizer(authorizer))
	api := newProductionOntologyReliabilityAPI(repository, control, nil, reliabilityTestGate(), decisions, nil, func() time.Time { return now.Add(time.Minute) })
	review, err := api.RequestRuleActivationReview(ctx, reliabilityTestPrincipal(), incident.TenantID, rule.ID, httptransport.OntologyReliabilityActivationReviewRequest{IdempotencyKey: "stale-activation-review"})
	if err != nil {
		t.Fatal(err)
	}
	retryable := true
	observer := reliability.NewService(repository, reliability.WithClock(func() time.Time { return now.Add(2 * time.Minute) }))
	if _, err := observer.ObserveFailure(ctx, reliability.FailureObservation{
		ID: "activation-stale-observation", TenantID: incident.TenantID, Source: "agent_runtime", Component: "pi", ErrorCode: "timeout",
		Retryable: &retryable, OccurredAt: now.Add(2 * time.Minute),
		RepairTarget: &reliability.RepairTarget{Kind: reliability.RepairTargetMissionAgentRun, AuthorizationTenantID: incident.TenantID, MissionID: "mission-1", RunID: "run-new"},
	}); err != nil {
		t.Fatal(err)
	}
	_, err = api.ResolveRuleActivationReview(ctx, reliabilityTestPrincipal(), incident.TenantID, rule.ID, review.ID, httptransport.OntologyReliabilityAuthorizationDecisionRequest{
		Decision: "authorize", Reason: "stale attempt", IdempotencyKey: "stale-activation-decision",
	})
	if !errors.Is(err, ontologycontract.ErrStateConflict) {
		t.Fatalf("stale activation decision error=%v, want state conflict", err)
	}
}

func TestProductionOntologyReliabilityAPIRejectsDeniedActivationReview(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 9, 2, 9, 30, 0, 0, time.UTC)
	repository, incident, rule, _ := testActivationReadyRepairRule(t, ctx, now)
	decisions := newReliabilityTestDecisions()
	authorizer := productionRepairRuleActivationAuthorizer{repository: repository, decisions: decisions, now: func() time.Time { return now }}
	control := reliability.NewService(repository, reliability.WithClock(func() time.Time { return now }), reliability.WithRepairRuleActivationAuthorizer(authorizer))
	api := newProductionOntologyReliabilityAPI(repository, control, nil, reliabilityTestGate(), decisions, nil, func() time.Time { return now })

	review, err := api.RequestRuleActivationReview(ctx, reliabilityTestPrincipal(), incident.TenantID, rule.ID, httptransport.OntologyReliabilityActivationReviewRequest{IdempotencyKey: "denied-activation-review"})
	if err != nil {
		t.Fatal(err)
	}
	resolution, err := api.ResolveRuleActivationReview(ctx, reliabilityTestPrincipal(), incident.TenantID, rule.ID, review.ID, httptransport.OntologyReliabilityAuthorizationDecisionRequest{
		Decision: "deny", Reason: "insufficient operational confidence", IdempotencyKey: "deny-activation-review",
	})
	if err != nil || resolution.Effect != string(hil.EffectDeny) || resolution.Review.Status != string(hil.DecisionDenied) {
		t.Fatalf("denied activation resolution=%+v err=%v", resolution, err)
	}
	_, err = api.ActivateRepairRule(ctx, reliabilityTestPrincipal(), incident.TenantID, rule.ID, review.ID, httptransport.OntologyReliabilityActivationRequest{IdempotencyKey: "activate-denied-rule"})
	if !errors.Is(err, ontologycontract.ErrStateConflict) || decisions.consumed != 0 {
		t.Fatalf("denied activation error=%v consumed=%d, want state conflict without receipt consumption", err, decisions.consumed)
	}
}

func testActivationReadyRepairRule(t *testing.T, ctx context.Context, now time.Time) (*reliability.MemoryRepository, reliability.FailureIncident, reliability.RepairRule, reliability.RepairPreflight) {
	t.Helper()
	repository := reliability.NewMemoryRepository()
	control := reliability.NewService(repository, reliability.WithClock(func() time.Time { return now }))
	retryable := true
	incident, err := control.ObserveFailure(ctx, reliability.FailureObservation{
		ID: "activation-observation", TenantID: "tenant-a", Source: "agent_runtime", Component: "pi", ErrorCode: "timeout",
		Message: "provider timed out", Retryable: &retryable, OccurredAt: now,
		RepairTarget: &reliability.RepairTarget{Kind: reliability.RepairTargetMissionAgentRun, AuthorizationTenantID: "tenant-a", MissionID: "mission-1", RunID: "run-failed"},
	})
	if err != nil {
		t.Fatal(err)
	}
	diagnosis, err := control.CreateDiagnosisCase(ctx, reliability.DiagnosisCaseCommand{
		ID: "activation-diagnosis-create", Actor: reliability.ActorRef{Kind: "agent", ID: "diagnoser-1"},
		Diagnosis: reliability.DiagnosisCase{TenantID: incident.TenantID, IncidentID: incident.ID, RootCauseCategory: reliability.FailureCategoryTransient,
			RedactedSummary: "temporary upstream timeout", Confidence: 0.9, EvidenceIDs: []string{"diagnosis-evidence-1"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	diagnosis, err = control.DecideDiagnosisCase(ctx, reliability.DiagnosisDecisionCommand{
		ID: "activation-diagnosis-confirm", TenantID: incident.TenantID, DiagnosisCaseID: diagnosis.ID,
		Decision: reliability.DiagnosisDecisionConfirm, Reason: "evidence verified", Actor: reliability.ActorRef{Kind: "human", ID: "operator-1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	rule, err := control.ProposeRepairRuleFromDiagnosis(ctx, reliability.RepairRuleCommand{
		ID: "activation-rule-proposal", Actor: reliability.ActorRef{Kind: "agent", ID: "learner-1"},
		Rule: reliability.RepairRule{TenantID: incident.TenantID, DiagnosisCaseID: diagnosis.ID, Strategy: reliability.RepairStrategyRetry,
			ActionTypeID: string(productionRepairActionTypeID), RiskLevel: reliability.RiskHigh, Reversible: true, Idempotent: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	target, err := repository.GetLatestRepairTarget(ctx, incident.TenantID, incident.ID)
	if err != nil {
		t.Fatal(err)
	}
	preflight, _, err := repository.RecordRepairPreflight(ctx, reliability.RepairPreflight{
		EvidenceID: "activation-preflight", TenantID: incident.TenantID, ActionRunID: "activation-dry-run", IncidentID: incident.ID,
		IncidentVersion: incident.Version, RuleID: rule.ID, RuleVersion: rule.Version, Strategy: rule.Strategy,
		ArgsHash: repairInvocationArgsHash(incident, rule, &target), TargetObservationID: target.ObservationID, TargetKind: target.Kind,
		TargetRunID: target.RunID, Passed: true, CheckedAt: now, ExpiresAt: now.Add(20 * time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}
	return repository, incident, rule, preflight
}
