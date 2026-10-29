package hil

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestServiceCreateDecisionBuildsOntologyCouncil(t *testing.T) {
	service, repo, closeStore := newTestHILService(t, OntologyContextSnapshot{
		BundleID:     "bundle-service-1",
		SchemaHash:   "schema-service-1",
		Confidence:   0.88,
		Complete:     true,
		Conflict:     true,
		EvidenceRefs: []string{"evidence-service-1"},
		RecommendedParticipants: []CouncilParticipant{
			{Actor: ActorRef{Kind: ActorHuman, ID: "ding"}, Required: true},
			{Actor: ActorRef{Kind: ActorAgent, ID: "agent-1"}},
			{Actor: ActorRef{Kind: ActorAgent, ID: "agent-2"}},
		},
	})
	defer closeStore()
	result, err := service.CreateDecision(context.Background(), testCreateDecisionInput())
	if err != nil {
		t.Fatalf("CreateDecision: %v", err)
	}
	if result.Decision.Route.Mode != ModeCouncil || result.Decision.Context.BundleID != "bundle-service-1" {
		t.Fatalf("result decision = %+v", result.Decision)
	}
	if result.Council == nil || len(result.Council.Participants) != 3 || result.Council.Quorum != 2 {
		t.Fatalf("result council = %+v", result.Council)
	}
	if result.Decision.CouncilID != result.Council.ID {
		t.Fatalf("decision council id = %q, want %q", result.Decision.CouncilID, result.Council.ID)
	}
	loaded, err := repo.GetDecision(context.Background(), "tenant-1", result.Decision.ID)
	if err != nil || loaded.RequestFingerprint == "" {
		t.Fatalf("persisted decision = %+v err=%v", loaded, err)
	}
}

func TestServiceListDecisionsIncludesCouncilID(t *testing.T) {
	service, _, closeStore := newTestHILService(t, councilTestContext())
	defer closeStore()
	created, err := service.CreateDecision(context.Background(), testCreateDecisionInput())
	if err != nil {
		t.Fatalf("CreateDecision: %v", err)
	}
	page, err := service.ListDecisions(context.Background(), DecisionListQuery{TenantID: "tenant-1", Mode: ModeCouncil})
	if err != nil {
		t.Fatalf("ListDecisions: %v", err)
	}
	if len(page.Items) != 1 || page.Items[0].CouncilID != created.Council.ID {
		t.Fatalf("page = %+v", page)
	}
}

func TestServiceGetDecisionExpiresCouncilAndNotifiesObserver(t *testing.T) {
	repo, closeStore := openHILRepository(t, "hil-service-expire-council-read")
	defer closeStore()
	now := testRepositoryNow
	var observed DecisionResolution
	service := NewService(repo, fixedContextProvider{snapshot: councilTestContext()}, &sequenceHILIDs{},
		WithClock(func() time.Time { return now }),
		WithResolutionObserver(ResolutionObserverFunc(func(_ context.Context, resolution DecisionResolution) { observed = resolution })),
	)
	input := testCreateDecisionInput()
	input.ExpiresAt = now.Add(time.Minute)
	created, err := service.CreateDecision(context.Background(), input)
	if err != nil {
		t.Fatalf("CreateDecision: %v", err)
	}
	now = now.Add(2 * time.Minute)
	decision, err := service.GetDecision(context.Background(), input.TenantID, created.Decision.ID)
	if err != nil {
		t.Fatalf("GetDecision: %v", err)
	}
	if decision.Status != DecisionDenied || decision.CouncilID != created.Council.ID {
		t.Fatalf("expired decision = %+v", decision)
	}
	if observed.Decision.ID != created.Decision.ID || observed.Decision.Status != DecisionDenied || observed.Receipt.Effect != EffectDeny || observed.Actor.ID != "hil:expiry" {
		t.Fatalf("expiry observer = %+v", observed)
	}
	storedCouncil, err := repo.GetCouncil(context.Background(), input.TenantID, created.Council.ID)
	if err != nil {
		t.Fatalf("GetCouncil: %v", err)
	}
	if storedCouncil.Status != CouncilExpired || storedCouncil.Effect != EffectDeny {
		t.Fatalf("stored council = %+v", storedCouncil)
	}
}

func TestServicePartialOntologyContextCannotDowngradeToAuto(t *testing.T) {
	service, _, closeStore := newTestHILService(t, OntologyContextSnapshot{BundleID: "bundle-partial", Complete: false})
	defer closeStore()
	input := testCreateDecisionInput()
	input.RiskLevel = RiskLow
	input.CouncilRequired = false
	result, err := service.CreateDecision(context.Background(), input)
	if err != nil {
		t.Fatalf("CreateDecision: %v", err)
	}
	if result.Decision.Route.Mode != ModeHIL || result.Council != nil {
		t.Fatalf("result = %+v, want runtime HIL", result)
	}
}

func TestServiceCreateDecisionIsIdempotentAcrossTime(t *testing.T) {
	repo, closeStore := openHILRepository(t, "hil-service-decision-idempotency")
	defer closeStore()
	now := testRepositoryNow
	service := NewService(repo, fixedContextProvider{snapshot: councilTestContext()}, &sequenceHILIDs{}, WithClock(func() time.Time { return now }))
	first, err := service.CreateDecision(context.Background(), testCreateDecisionInput())
	if err != nil {
		t.Fatalf("first CreateDecision: %v", err)
	}
	now = now.Add(10 * time.Minute)
	second, err := service.CreateDecision(context.Background(), testCreateDecisionInput())
	if err != nil {
		t.Fatalf("idempotent CreateDecision: %v", err)
	}
	if second.Decision.ID != first.Decision.ID || second.Council == nil || first.Council == nil || second.Council.ID != first.Council.ID {
		t.Fatalf("idempotent result = %+v, want decision %s council %s", second, first.Decision.ID, first.Council.ID)
	}
}

func TestServiceApprovalDisplayMetadataDoesNotChangeDecisionIdentity(t *testing.T) {
	service, _, closeStore := newTestHILService(t, councilTestContext())
	defer closeStore()
	input := testCreateDecisionInput()
	first, err := service.CreateDecision(context.Background(), input)
	if err != nil {
		t.Fatalf("first CreateDecision: %v", err)
	}
	input.PayloadPreview[ApprovalDisplayPayloadKey] = map[string]any{"title": "更清楚的展示标题"}
	second, err := service.CreateDecision(context.Background(), input)
	if err != nil {
		t.Fatalf("display-only idempotent CreateDecision: %v", err)
	}
	if second.Decision.ID != first.Decision.ID {
		t.Fatalf("display-only replay created decision %q, want %q", second.Decision.ID, first.Decision.ID)
	}

	input.PayloadPreview["field"] = "changed executable context"
	if _, err := service.CreateDecision(context.Background(), input); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("changed operational payload error = %v, want ErrIdempotencyConflict", err)
	}
}

func TestServiceListsAssignedRuntimeHILForExactHumanTenant(t *testing.T) {
	service, _, closeStore := newTestHILService(t, OntologyContextSnapshot{BundleID: "bundle-runtime-assigned", Complete: true})
	defer closeStore()
	input := testCreateDecisionInput()
	input.TenantID = "minvia"
	input.Action = "runtime.approval"
	input.Resource = ObjectRef{Kind: "runtime_approval", ID: "conversation-1:approval-1"}
	input.CouncilRequired = false
	input.HILRequired = true
	input.PreferredMode = ModeHIL
	input.PayloadPreview = map[string]any{approvalAssigneePayloadKey: "H000001"}
	input.IdempotencyKey = "runtime-approval-assigned-human"

	created, err := service.CreateDecision(context.Background(), input)
	if err != nil {
		t.Fatalf("CreateDecision: %v", err)
	}
	owner, err := service.ListDecisions(context.Background(), DecisionListQuery{
		TenantID: "minvia", Mode: ModeHIL, Status: DecisionPending, Mine: true,
		ActionableBy: ActorRef{Kind: ActorHuman, ID: "H000001"},
	})
	if err != nil || owner.Total != 1 || len(owner.Items) != 1 || owner.Items[0].ID != created.Decision.ID {
		t.Fatalf("owner runtime HIL inbox = %+v, err=%v", owner, err)
	}
	for _, query := range []DecisionListQuery{
		{TenantID: "minvia", Mode: ModeHIL, Status: DecisionPending, Mine: true, ActionableBy: ActorRef{Kind: ActorHuman, ID: "H000002"}},
		{TenantID: "other-tenant", Mode: ModeHIL, Status: DecisionPending, Mine: true, ActionableBy: ActorRef{Kind: ActorHuman, ID: "H000001"}},
	} {
		page, err := service.ListDecisions(context.Background(), query)
		if err != nil || page.Total != 0 || len(page.Items) != 0 {
			t.Fatalf("non-owner runtime HIL inbox = %+v, err=%v", page, err)
		}
	}
}

func TestServiceCreateDecisionReusesBusinessApprovalAcrossRuntimeAttempts(t *testing.T) {
	service, repo, closeStore := newTestHILService(t, councilTestContext())
	defer closeStore()
	input := testCreateDecisionInput()
	input.BusinessKey = "marketing.wechat_draft.intent:sha256:stable"
	first, err := service.CreateDecision(context.Background(), input)
	if err != nil {
		t.Fatalf("first CreateDecision: %v", err)
	}
	input.IdempotencyKey = "decision-service-retried-runtime"
	input.ArgsHash = "sha256:retried-runtime-args"
	input.Resource.ID = "customer-retried-runtime"
	input.Purpose = "same business intent from a later runtime attempt"
	input.ExpiresAt = input.ExpiresAt.Add(time.Hour)
	second, err := service.CreateDecision(context.Background(), input)
	if err != nil {
		t.Fatalf("business-idempotent CreateDecision: %v", err)
	}
	if second.Decision.ID != first.Decision.ID || second.Council == nil || second.Council.ID != first.Council.ID {
		t.Fatalf("business-idempotent result = %+v, want decision %s", second, first.Decision.ID)
	}
	if decisionPayloadString(second.Decision, approvalBusinessKeyPayloadKey) != input.BusinessKey {
		t.Fatalf("persisted business key = %#v", second.Decision.PayloadPreview)
	}
	page, err := repo.ListDecisions(context.Background(), DecisionListQuery{TenantID: input.TenantID, Limit: 100})
	if err != nil || page.Total != 1 {
		t.Fatalf("business-idempotent decisions = %+v, err=%v", page, err)
	}
}

func TestServiceCreateDecisionRejectsChangedIdempotentRequest(t *testing.T) {
	service, _, closeStore := newTestHILService(t, councilTestContext())
	defer closeStore()
	input := testCreateDecisionInput()
	if _, err := service.CreateDecision(context.Background(), input); err != nil {
		t.Fatalf("first CreateDecision: %v", err)
	}
	input.Action = "customer.delete"
	if _, err := service.CreateDecision(context.Background(), input); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("changed CreateDecision error = %v, want ErrIdempotencyConflict", err)
	}
}

func TestServiceFailsClosedWhenOntologyContextFails(t *testing.T) {
	repo, closeStore := openHILRepository(t, "hil-service-context-error")
	defer closeStore()
	service := NewService(repo, failingContextProvider{}, &sequenceHILIDs{}, WithClock(func() time.Time { return testRepositoryNow }))
	if _, err := service.CreateDecision(context.Background(), testCreateDecisionInput()); !errors.Is(err, ErrContextUnavailable) {
		t.Fatalf("CreateDecision error = %v, want ErrContextUnavailable", err)
	}
}

func TestServicePassesActionMetadataToDecisionContextProvider(t *testing.T) {
	repo, closeStore := openHILRepository(t, "hil-service-context-action-metadata")
	defer closeStore()
	provider := &recordingContextProvider{snapshot: OntologyContextSnapshot{BundleID: "bundle-context-action", Complete: false}}
	service := NewService(repo, provider, &sequenceHILIDs{}, WithClock(func() time.Time { return testRepositoryNow }))
	input := testCreateDecisionInput()
	input.Action = "runtime.approval"
	input.ActionTypeID = "tool_use"
	input.CouncilRequired = false
	input.HILRequired = true
	input.PayloadPreview = map[string]any{
		"approval_assigned_to": "human-ding",
		"approval_tool_name":   "mcp_marketing_marketing_wechat_draft_create",
	}

	if _, err := service.CreateDecision(context.Background(), input); err != nil {
		t.Fatalf("CreateDecision: %v", err)
	}
	if provider.request.Action != input.Action || provider.request.ActionTypeID != input.ActionTypeID {
		t.Fatalf("context request action = %+v", provider.request)
	}
	if got, _ := provider.request.PayloadPreview["approval_tool_name"].(string); got != "mcp_marketing_marketing_wechat_draft_create" {
		t.Fatalf("context request payload = %+v", provider.request.PayloadPreview)
	}
}

func TestServiceRejectsContributionFromNonParticipant(t *testing.T) {
	service, _, closeStore := newTestHILService(t, councilTestContext())
	defer closeStore()
	result, err := service.CreateDecision(context.Background(), testCreateDecisionInput())
	if err != nil {
		t.Fatalf("CreateDecision: %v", err)
	}
	err = service.SubmitContribution(context.Background(), "tenant-1", result.Council.ID, ActorRef{Kind: ActorAgent, ID: "outsider"}, SubmitContributionInput{
		Position: "reject", Reasoning: "not a member", IdempotencyKey: "contribution-outsider",
	})
	if !errors.Is(err, ErrParticipantRequired) {
		t.Fatalf("SubmitContribution error = %v, want ErrParticipantRequired", err)
	}
}

func TestServiceContributionCannotChangeAfterVote(t *testing.T) {
	service, _, closeStore := newTestHILService(t, councilTestContext())
	defer closeStore()
	result, err := service.CreateDecision(context.Background(), testCreateDecisionInput())
	if err != nil {
		t.Fatalf("CreateDecision: %v", err)
	}
	actor := ActorRef{Kind: ActorHuman, ID: "ding"}
	if err := service.SubmitContribution(context.Background(), "tenant-1", result.Council.ID, actor, SubmitContributionInput{
		Position: "approve", Reasoning: "initial", IdempotencyKey: "contribution-ding",
	}); err != nil {
		t.Fatalf("SubmitContribution: %v", err)
	}
	if _, err := service.CastVote(context.Background(), "tenant-1", result.Council.ID, actor, CastVoteInput{
		Choice: VoteApprove, Reason: "approved", IdempotencyKey: "vote-ding",
	}); err != nil {
		t.Fatalf("CastVote: %v", err)
	}
	err = service.SubmitContribution(context.Background(), "tenant-1", result.Council.ID, actor, SubmitContributionInput{
		Position: "reject", Reasoning: "changed", IdempotencyKey: "contribution-ding-2",
	})
	if !errors.Is(err, ErrVoteImmutable) {
		t.Fatalf("changed contribution error = %v, want ErrVoteImmutable", err)
	}
}

func TestServiceContributionAndVoteAreIdempotentAcrossTime(t *testing.T) {
	repo, closeStore := openHILRepository(t, "hil-service-council-idempotency")
	defer closeStore()
	now := testRepositoryNow
	service := NewService(repo, fixedContextProvider{snapshot: councilTestContext()}, &sequenceHILIDs{}, WithClock(func() time.Time { return now }))
	result, err := service.CreateDecision(context.Background(), testCreateDecisionInput())
	if err != nil {
		t.Fatalf("CreateDecision: %v", err)
	}
	actor := ActorRef{Kind: ActorHuman, ID: "ding"}
	contribution := SubmitContributionInput{Position: "approve", Reasoning: "supported", IdempotencyKey: "contribution-ding"}
	if err := service.SubmitContribution(context.Background(), "tenant-1", result.Council.ID, actor, contribution); err != nil {
		t.Fatalf("SubmitContribution: %v", err)
	}
	now = now.Add(time.Minute)
	if err := service.SubmitContribution(context.Background(), "tenant-1", result.Council.ID, actor, contribution); err != nil {
		t.Fatalf("idempotent SubmitContribution: %v", err)
	}
	vote := CastVoteInput{Choice: VoteApprove, Reason: "supported", IdempotencyKey: "vote-ding"}
	if _, err := service.CastVote(context.Background(), "tenant-1", result.Council.ID, actor, vote); err != nil {
		t.Fatalf("CastVote: %v", err)
	}
	now = now.Add(time.Minute)
	if _, err := service.CastVote(context.Background(), "tenant-1", result.Council.ID, actor, vote); err != nil {
		t.Fatalf("idempotent CastVote: %v", err)
	}
}

func TestServiceFinalVoteIsIdempotentAfterCouncilResolves(t *testing.T) {
	repo, closeStore := openHILRepository(t, "hil-service-final-vote-idempotency")
	defer closeStore()
	now := testRepositoryNow
	service := NewService(repo, fixedContextProvider{snapshot: councilTestContext()}, &sequenceHILIDs{}, WithClock(func() time.Time { return now }))
	result, err := service.CreateDecision(context.Background(), testCreateDecisionInput())
	if err != nil {
		t.Fatalf("CreateDecision: %v", err)
	}
	actors := []ActorRef{{Kind: ActorHuman, ID: "ding"}, {Kind: ActorAgent, ID: "agent-1"}}
	for _, actor := range actors {
		if err := service.SubmitContribution(context.Background(), "tenant-1", result.Council.ID, actor, SubmitContributionInput{
			Position: "approve", Reasoning: "supported", IdempotencyKey: "contribution-" + actor.ID,
		}); err != nil {
			t.Fatalf("SubmitContribution(%s): %v", actor.ID, err)
		}
	}
	if _, err := service.CastVote(context.Background(), "tenant-1", result.Council.ID, actors[0], CastVoteInput{Choice: VoteApprove, IdempotencyKey: "vote-ding"}); err != nil {
		t.Fatalf("first CastVote: %v", err)
	}
	finalVote := CastVoteInput{Choice: VoteApprove, Reason: "supported", IdempotencyKey: "vote-agent-1"}
	resolved, err := service.CastVote(context.Background(), "tenant-1", result.Council.ID, actors[1], finalVote)
	if err != nil || resolved.Status != CouncilPassed {
		t.Fatalf("final CastVote: session=%+v err=%v", resolved, err)
	}
	now = now.Add(time.Minute)
	replayed, err := service.CastVote(context.Background(), "tenant-1", result.Council.ID, actors[1], finalVote)
	if err != nil || replayed.Status != CouncilPassed || replayed.ResolvedAt != resolved.ResolvedAt {
		t.Fatalf("replayed CastVote: session=%+v err=%v", replayed, err)
	}
}

func TestServiceMediumRiskCouncilProducesAuthorizationReceipt(t *testing.T) {
	service, repo, closeStore := newTestHILService(t, councilTestContext())
	defer closeStore()
	result, err := service.CreateDecision(context.Background(), testCreateDecisionInput())
	if err != nil {
		t.Fatalf("CreateDecision: %v", err)
	}
	castCouncilApproveVotes(t, service, result.Council.ID)
	loaded, err := repo.GetCouncil(context.Background(), "tenant-1", result.Council.ID)
	if err != nil {
		t.Fatalf("GetCouncil: %v", err)
	}
	if loaded.Status != CouncilPassed || loaded.Effect != EffectAuthorize {
		t.Fatalf("resolved council = %+v", loaded)
	}
	receipts, err := repo.ListDecisionReceipts(context.Background(), "tenant-1", result.Decision.ID)
	if err != nil || len(receipts) != 1 || !receipts[0].SingleUse || receipts[0].Effect != EffectAuthorize {
		t.Fatalf("receipts = %+v err=%v", receipts, err)
	}
}

func TestServiceHighRiskCouncilProducesApprovalHandoff(t *testing.T) {
	service, repo, closeStore := newTestHILService(t, councilTestContext())
	defer closeStore()
	input := testCreateDecisionInput()
	input.RiskLevel = RiskHigh
	input.CouncilRequired = true
	input.PayloadPreview = map[string]any{"approval_assigned_to": "human-manager", "source": "responsibility_autoplan"}
	result, err := service.CreateDecision(context.Background(), input)
	if err != nil {
		t.Fatalf("CreateDecision: %v", err)
	}
	castCouncilApproveVotes(t, service, result.Council.ID)
	receipts, err := repo.ListDecisionReceipts(context.Background(), "tenant-1", result.Decision.ID)
	if err != nil || len(receipts) != 1 || receipts[0].Effect != EffectRequiresApproval || receipts[0].SingleUse {
		t.Fatalf("receipts = %+v err=%v", receipts, err)
	}
	handoff, err := service.GetDecision(context.Background(), "tenant-1", result.Decision.ID)
	if err != nil {
		t.Fatalf("GetDecision: %v", err)
	}
	if handoff.Status != DecisionPending || handoff.Route.Mode != ModeApproval {
		t.Fatalf("handoff = %+v, want pending fixed approval", handoff)
	}
	ownerPage, err := service.ListDecisions(context.Background(), DecisionListQuery{
		TenantID: "tenant-1", Mode: ModeApproval, Status: DecisionPending, Mine: true,
		ActionableBy: ActorRef{Kind: ActorHuman, ID: "human-manager"},
	})
	if err != nil || ownerPage.Total != 1 || len(ownerPage.Items) != 1 || ownerPage.Items[0].ID != result.Decision.ID {
		t.Fatalf("owner approval inbox = %+v err=%v", ownerPage, err)
	}
	outsiderPage, err := service.ListDecisions(context.Background(), DecisionListQuery{
		TenantID: "tenant-1", Mode: ModeApproval, Status: DecisionPending, Mine: true,
		ActionableBy: ActorRef{Kind: ActorHuman, ID: "other-human"},
	})
	if err != nil || outsiderPage.Total != 0 || len(outsiderPage.Items) != 0 {
		t.Fatalf("outsider approval inbox = %+v err=%v", outsiderPage, err)
	}
	if _, err := service.ListDecisions(context.Background(), DecisionListQuery{
		TenantID: "tenant-1", Mode: ModeApproval, Status: DecisionPending, Mine: true,
		ActionableBy: ActorRef{Kind: ActorAgent, ID: "human-manager"},
	}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("agent approval inbox error = %v, want ErrInvalidRequest", err)
	}
	if _, err := service.ResolveDecision(context.Background(), "tenant-1", result.Decision.ID, ActorRef{Kind: ActorHuman, ID: "other-human"}, ResolveDecisionInput{
		Decision: "approve", IdempotencyKey: "council-handoff-approval-other",
	}); !errors.Is(err, ErrParticipantRequired) {
		t.Fatalf("outsider ResolveDecision error = %v, want ErrParticipantRequired", err)
	}
	if _, err := service.ResolveDecision(context.Background(), "tenant-1", result.Decision.ID, ActorRef{Kind: ActorAgent, ID: "human-manager"}, ResolveDecisionInput{
		Decision: "approve", IdempotencyKey: "council-handoff-approval-agent",
	}); !errors.Is(err, ErrParticipantRequired) {
		t.Fatalf("agent ResolveDecision error = %v, want ErrParticipantRequired", err)
	}
	resolution, err := service.ResolveDecision(context.Background(), "tenant-1", result.Decision.ID, ActorRef{Kind: ActorHuman, ID: "human-manager"}, ResolveDecisionInput{
		Decision: "approve", IdempotencyKey: "test-idempotency-key",
	})
	if err != nil {
		t.Fatalf("ResolveDecision: %v", err)
	}
	if resolution.Decision.Status != DecisionResolved || resolution.Receipt.Effect != EffectAuthorize {
		t.Fatalf("handoff resolution = %+v", resolution)
	}
}

func TestServiceResolvesRuntimeHILDecisionAndNotifiesObserver(t *testing.T) {
	repo, closeStore := openHILRepository(t, "hil-runtime-resolution")
	defer closeStore()
	now := testRepositoryNow
	var observed DecisionResolution
	service := NewService(repo, fixedContextProvider{snapshot: OntologyContextSnapshot{BundleID: "bundle-runtime", Complete: true}}, &sequenceHILIDs{},
		WithClock(func() time.Time { return now }), WithResolutionObserver(ResolutionObserverFunc(func(_ context.Context, result DecisionResolution) { observed = result })))
	input := testCreateDecisionInput()
	input.CouncilRequired = false
	input.HILRequired = true
	result, err := service.CreateDecision(context.Background(), input)
	if err != nil {
		t.Fatalf("CreateDecision: %v", err)
	}
	if result.Decision.Route.Mode != ModeHIL || result.Decision.Status != DecisionPending {
		t.Fatalf("created decision = %+v", result.Decision)
	}
	resolution, err := service.ResolveDecision(context.Background(), input.TenantID, result.Decision.ID, ActorRef{Kind: ActorHuman, ID: "ding"}, ResolveDecisionInput{
		Decision: "approve", Reason: "reviewed", IdempotencyKey: "resolve-1",
	})
	if err != nil {
		t.Fatalf("ResolveDecision: %v", err)
	}
	if resolution.Decision.Status != DecisionResolved || resolution.Receipt.Effect != EffectAuthorize || !resolution.Receipt.SingleUse {
		t.Fatalf("resolution = %+v", resolution)
	}
	if observed.Decision.ID != result.Decision.ID || observed.Receipt.ID != resolution.Receipt.ID {
		t.Fatalf("observer result = %+v", observed)
	}
	replayed, err := service.ResolveDecision(context.Background(), input.TenantID, result.Decision.ID, ActorRef{Kind: ActorHuman, ID: "ding"}, ResolveDecisionInput{
		Decision: "approve", IdempotencyKey: "resolve-1-replay",
	})
	if err != nil || replayed.Receipt.ID != resolution.Receipt.ID {
		t.Fatalf("replayed resolution = %+v err=%v", replayed, err)
	}
}

func TestServiceResolvesFixedApprovalAsDenied(t *testing.T) {
	service, repo, closeStore := newTestHILService(t, OntologyContextSnapshot{BundleID: "bundle-approval", Complete: true})
	defer closeStore()
	input := testCreateDecisionInput()
	input.CouncilRequired = false
	input.ApprovalRequired = true
	input.PayloadPreview = map[string]any{"approval_assigned_to": "ding"}
	result, err := service.CreateDecision(context.Background(), input)
	if err != nil {
		t.Fatalf("CreateDecision: %v", err)
	}
	if result.Decision.Route.Mode != ModeApproval {
		t.Fatalf("route = %+v", result.Decision.Route)
	}
	resolution, err := service.ResolveDecision(context.Background(), input.TenantID, result.Decision.ID, ActorRef{Kind: ActorHuman, ID: "ding"}, ResolveDecisionInput{Decision: "reject", IdempotencyKey: "resolve-reject-1"})
	if err != nil {
		t.Fatalf("ResolveDecision: %v", err)
	}
	if resolution.Decision.Status != DecisionDenied || resolution.Receipt.Effect != EffectDeny || resolution.Receipt.SingleUse {
		t.Fatalf("resolution = %+v", resolution)
	}
	loaded, err := repo.GetDecision(context.Background(), input.TenantID, result.Decision.ID)
	if err != nil || loaded.Status != DecisionDenied {
		t.Fatalf("loaded = %+v err=%v", loaded, err)
	}
}

func TestServiceRejectsFixedApprovalWithoutCanonicalHumanAssignee(t *testing.T) {
	tests := []struct {
		name  string
		input CreateDecisionInput
	}{
		{
			name: "direct approval without owner",
			input: func() CreateDecisionInput {
				input := testCreateDecisionInput()
				input.CouncilRequired = false
				input.ApprovalRequired = true
				return input
			}(),
		},
		{
			name: "high risk council without owner",
			input: func() CreateDecisionInput {
				input := testCreateDecisionInput()
				input.RiskLevel = RiskHigh
				return input
			}(),
		},
		{
			name: "non-string owner",
			input: func() CreateDecisionInput {
				input := testCreateDecisionInput()
				input.CouncilRequired = false
				input.ApprovalRequired = true
				input.PayloadPreview = map[string]any{"approval_assigned_to": 42}
				return input
			}(),
		},
		{
			name: "blank owner",
			input: func() CreateDecisionInput {
				input := testCreateDecisionInput()
				input.CouncilRequired = false
				input.ApprovalRequired = true
				input.PayloadPreview = map[string]any{"approval_assigned_to": "  "}
				return input
			}(),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service, _, closeStore := newTestHILService(t, OntologyContextSnapshot{BundleID: "bundle-" + strings.ReplaceAll(test.name, " ", "-"), Complete: true})
			defer closeStore()
			if _, err := service.CreateDecision(context.Background(), test.input); !errors.Is(err, ErrInvalidRequest) {
				t.Fatalf("CreateDecision error = %v, want ErrInvalidRequest", err)
			}
		})
	}
}

func TestDecisionResolutionActorAllowedFailsClosedForInvalidFixedApprovalOwner(t *testing.T) {
	for _, decision := range []DecisionRequest{
		{Route: RouteDecision{Mode: ModeApproval}},
		{Route: RouteDecision{Mode: ModeApproval}, PayloadPreview: map[string]any{"approval_assigned_to": 42}},
		{Route: RouteDecision{Mode: ModeHIL}, PayloadPreview: map[string]any{"approval_assigned_to": ""}},
	} {
		if DecisionResolutionActorAllowed(decision, ActorRef{Kind: ActorHuman, ID: "human-manager"}) {
			t.Fatalf("invalid assigned decision allowed a human resolver: %+v", decision)
		}
	}
	if DecisionResolutionActorAllowed(DecisionRequest{Route: RouteDecision{Mode: ModeHIL}}, ActorRef{Kind: ActorAgent, ID: "agent-1"}) {
		t.Fatal("unassigned HIL decision allowed an agent resolver")
	}
}

func TestServiceResolvesAssignedFixedApprovalOnlyForAssignedHuman(t *testing.T) {
	service, _, closeStore := newTestHILService(t, OntologyContextSnapshot{BundleID: "bundle-assigned-approval", Complete: true})
	defer closeStore()
	input := testCreateDecisionInput()
	input.CouncilRequired = false
	input.ApprovalRequired = true
	input.PayloadPreview = map[string]any{"approval_assigned_to": "human-manager", "source": "responsibility_autoplan"}
	result, err := service.CreateDecision(context.Background(), input)
	if err != nil {
		t.Fatalf("CreateDecision: %v", err)
	}
	if _, err := service.ResolveDecision(context.Background(), input.TenantID, result.Decision.ID, ActorRef{Kind: ActorHuman, ID: "other-human"}, ResolveDecisionInput{Decision: "approve", IdempotencyKey: "assigned-approval-other"}); !errors.Is(err, ErrParticipantRequired) {
		t.Fatalf("outsider ResolveDecision error = %v, want ErrParticipantRequired", err)
	}
	if _, err := service.ResolveDecision(context.Background(), input.TenantID, result.Decision.ID, ActorRef{Kind: ActorAgent, ID: "human-manager"}, ResolveDecisionInput{Decision: "approve", IdempotencyKey: "assigned-approval-agent"}); !errors.Is(err, ErrParticipantRequired) {
		t.Fatalf("agent ResolveDecision error = %v, want ErrParticipantRequired", err)
	}
	resolution, err := service.ResolveDecision(context.Background(), input.TenantID, result.Decision.ID, ActorRef{Kind: ActorHuman, ID: "human-manager"}, ResolveDecisionInput{Decision: "approve", IdempotencyKey: "assigned-approval-owner"})
	if err != nil || resolution.Decision.Status != DecisionResolved {
		t.Fatalf("owner resolution = %+v err=%v", resolution, err)
	}
}

func TestServiceConsumesBoundActionDecisionExactlyOnce(t *testing.T) {
	service, _, closeStore := newTestHILService(t, OntologyContextSnapshot{BundleID: "bundle-bound-action", Complete: true})
	defer closeStore()
	input := testCreateDecisionInput()
	input.Action = "ontology.action.dry_run"
	input.ActionTypeID = "reliability.repair.execute"
	input.Resource = ObjectRef{Kind: "failure_incident", ID: "incident-1"}
	input.ObjectRefs = []ObjectRef{{Kind: "repair_rule", ID: "rule-1"}, input.Resource}
	input.SubjectRefs = append([]ObjectRef(nil), input.ObjectRefs...)
	input.RiskLevel = RiskHigh
	input.CouncilRequired = false
	input.ApprovalRequired = true
	input.PayloadPreview = map[string]any{"approval_assigned_to": "human-manager"}
	created, err := service.CreateDecision(context.Background(), input)
	if err != nil {
		t.Fatalf("CreateDecision: %v", err)
	}
	resolved, err := service.ResolveDecision(context.Background(), input.TenantID, created.Decision.ID, ActorRef{Kind: ActorHuman, ID: "human-manager"}, ResolveDecisionInput{
		Decision: "approve", IdempotencyKey: "resolve-bound-action",
	})
	if err != nil {
		t.Fatalf("ResolveDecision: %v", err)
	}
	available, err := service.GetActionDecisionReceipt(context.Background(), input.TenantID, created.Decision.ID, input.Requester)
	if err != nil || available.ID != resolved.Receipt.ID || !available.UsedAt.IsZero() {
		t.Fatalf("GetActionDecisionReceipt = %+v err=%v", available, err)
	}
	if _, err := service.GetActionDecisionReceipt(context.Background(), input.TenantID, created.Decision.ID, ActorRef{Kind: ActorHuman, ID: "other"}); !errors.Is(err, ErrParticipantRequired) {
		t.Fatalf("other requester receipt error = %v, want ErrParticipantRequired", err)
	}
	consume := ActionReceiptConsumeInput{
		TenantID: input.TenantID, DecisionID: created.Decision.ID, ReceiptID: resolved.Receipt.ID,
		Actor: input.Requester, Action: input.Action, ActionTypeID: input.ActionTypeID,
		ObjectRefs: []ObjectRef{input.Resource, {Kind: "repair_rule", ID: "rule-1"}},
		ArgsHash:   input.ArgsHash, Purpose: input.Purpose, RiskLevel: input.RiskLevel,
	}
	receipt, err := service.ConsumeActionDecision(context.Background(), consume)
	if err != nil {
		t.Fatalf("ConsumeActionDecision: %v", err)
	}
	if receipt.ID != resolved.Receipt.ID || receipt.UsedAt.IsZero() {
		t.Fatalf("consumed receipt = %+v", receipt)
	}
	if _, err := service.GetActionDecisionReceipt(context.Background(), input.TenantID, created.Decision.ID, input.Requester); !errors.Is(err, ErrNotFound) {
		t.Fatalf("used requester receipt error = %v, want ErrNotFound", err)
	}
	if _, err := service.ConsumeActionDecision(context.Background(), consume); !errors.Is(err, ErrConflict) {
		t.Fatalf("replayed ConsumeActionDecision error = %v, want ErrConflict", err)
	}
}

func TestServiceRejectsMismatchedBoundActionWithoutConsumingReceipt(t *testing.T) {
	service, _, closeStore := newTestHILService(t, OntologyContextSnapshot{BundleID: "bundle-bound-action-mismatch", Complete: true})
	defer closeStore()
	input := testCreateDecisionInput()
	input.Action = "ontology.action.dry_run"
	input.ActionTypeID = "reliability.repair.execute"
	input.Resource = ObjectRef{Kind: "failure_incident", ID: "incident-1"}
	input.ObjectRefs = []ObjectRef{input.Resource, {Kind: "repair_rule", ID: "rule-1"}}
	input.SubjectRefs = append([]ObjectRef(nil), input.ObjectRefs...)
	input.RiskLevel = RiskHigh
	input.CouncilRequired = false
	input.ApprovalRequired = true
	input.PayloadPreview = map[string]any{"approval_assigned_to": "human-manager"}
	created, err := service.CreateDecision(context.Background(), input)
	if err != nil {
		t.Fatalf("CreateDecision: %v", err)
	}
	resolved, err := service.ResolveDecision(context.Background(), input.TenantID, created.Decision.ID, ActorRef{Kind: ActorHuman, ID: "human-manager"}, ResolveDecisionInput{
		Decision: "approve", IdempotencyKey: "resolve-bound-action-mismatch",
	})
	if err != nil {
		t.Fatalf("ResolveDecision: %v", err)
	}
	consume := ActionReceiptConsumeInput{
		TenantID: input.TenantID, DecisionID: created.Decision.ID, ReceiptID: resolved.Receipt.ID,
		Actor: input.Requester, Action: input.Action, ActionTypeID: input.ActionTypeID,
		ObjectRefs: input.ObjectRefs, ArgsHash: "sha256:changed", Purpose: input.Purpose, RiskLevel: input.RiskLevel,
	}
	if _, err := service.ConsumeActionDecision(context.Background(), consume); !errors.Is(err, ErrConflict) {
		t.Fatalf("mismatched ConsumeActionDecision error = %v, want ErrConflict", err)
	}
	consume.ArgsHash = input.ArgsHash
	if _, err := service.ConsumeActionDecision(context.Background(), consume); err != nil {
		t.Fatalf("receipt was consumed by mismatched request: %v", err)
	}
}

func TestServiceSupersedesPendingFixedApprovalAndRemovesItFromMine(t *testing.T) {
	service, _, closeStore := newTestHILService(t, OntologyContextSnapshot{BundleID: "bundle-supersede-approval", Complete: true})
	defer closeStore()
	input := testCreateDecisionInput()
	input.CouncilRequired = false
	input.ApprovalRequired = true
	input.PayloadPreview = map[string]any{"approval_assigned_to": "human-manager", "source": "responsibility_autoplan"}
	created, err := service.CreateDecision(context.Background(), input)
	if err != nil {
		t.Fatalf("CreateDecision: %v", err)
	}
	before, err := service.ListDecisions(context.Background(), DecisionListQuery{
		TenantID: input.TenantID, Mode: ModeApproval, Status: DecisionPending, Mine: true,
		ActionableBy: ActorRef{Kind: ActorHuman, ID: "human-manager"},
	})
	if err != nil || before.Total != 1 {
		t.Fatalf("pending owner inbox = %+v err=%v", before, err)
	}
	resolution, err := service.SupersedeDecision(context.Background(), input.TenantID, created.Decision.ID, "approval assignee changed")
	if err != nil || resolution.Decision.Status != DecisionDenied || resolution.Receipt.Effect != EffectDeny || resolution.Actor.Kind != ActorService {
		t.Fatalf("SupersedeDecision = %+v err=%v", resolution, err)
	}
	after, err := service.ListDecisions(context.Background(), DecisionListQuery{
		TenantID: input.TenantID, Mode: ModeApproval, Status: DecisionPending, Mine: true,
		ActionableBy: ActorRef{Kind: ActorHuman, ID: "human-manager"},
	})
	if err != nil || after.Total != 0 || len(after.Items) != 0 {
		t.Fatalf("superseded owner inbox = %+v err=%v", after, err)
	}
	if _, err := service.ResolveDecision(context.Background(), input.TenantID, created.Decision.ID, ActorRef{Kind: ActorHuman, ID: "human-manager"}, ResolveDecisionInput{
		Decision: "approve", IdempotencyKey: "resolve-superseded-approval",
	}); !errors.Is(err, ErrConflict) {
		t.Fatalf("ResolveDecision after supersede error = %v, want ErrConflict", err)
	}
	replayed, err := service.SupersedeDecision(context.Background(), input.TenantID, created.Decision.ID, "approval assignee changed")
	if err != nil || replayed.Receipt.ID != resolution.Receipt.ID {
		t.Fatalf("replayed SupersedeDecision = %+v err=%v", replayed, err)
	}
}

func castCouncilApproveVotes(t *testing.T, service *Service, councilID string) {
	t.Helper()
	for index, actor := range []ActorRef{{Kind: ActorHuman, ID: "ding"}, {Kind: ActorAgent, ID: "agent-1"}} {
		if err := service.SubmitContribution(context.Background(), "tenant-1", councilID, actor, SubmitContributionInput{
			Position: "approve", Reasoning: "evidence supports approval", IdempotencyKey: "contribution-" + actor.ID,
		}); err != nil {
			t.Fatalf("SubmitContribution(%d): %v", index, err)
		}
		if _, err := service.CastVote(context.Background(), "tenant-1", councilID, actor, CastVoteInput{
			Choice: VoteApprove, Reason: "approve", IdempotencyKey: "vote-" + actor.ID,
		}); err != nil {
			t.Fatalf("CastVote(%d): %v", index, err)
		}
	}
}

func newTestHILService(t *testing.T, snapshot OntologyContextSnapshot) (*Service, *PlatformRepository, func()) {
	t.Helper()
	repo, closeStore := openHILRepository(t, "hil-service-"+snapshot.BundleID)
	service := NewService(repo, fixedContextProvider{snapshot: snapshot}, &sequenceHILIDs{}, WithClock(func() time.Time { return testRepositoryNow }))
	return service, repo, closeStore
}

func testCreateDecisionInput() CreateDecisionInput {
	return CreateDecisionInput{
		TenantID:        "tenant-1",
		Requester:       ActorRef{Kind: ActorAgent, ID: "agent-requester"},
		Action:          "customer.update",
		ActionTypeID:    "customer.update",
		Resource:        ObjectRef{Kind: "customer", ID: "customer-1"},
		ObjectRefs:      []ObjectRef{{Kind: "customer", ID: "customer-1"}},
		ArgsHash:        "sha256:service-args",
		Purpose:         "correct customer record",
		RiskLevel:       RiskMedium,
		CouncilRequired: true,
		SubjectRefs:     []ObjectRef{{Kind: "customer", ID: "customer-1"}},
		OntologyQuery:   "decision stakeholders",
		PayloadPreview:  map[string]any{"field": "value"},
		IdempotencyKey:  "decision-service-1",
		ExpiresAt:       testRepositoryNow.Add(time.Hour),
	}
}

func councilTestContext() OntologyContextSnapshot {
	return OntologyContextSnapshot{
		BundleID: "bundle-council", Complete: true, Confidence: 0.9,
		RecommendedParticipants: []CouncilParticipant{
			{Actor: ActorRef{Kind: ActorHuman, ID: "ding"}, Required: true},
			{Actor: ActorRef{Kind: ActorAgent, ID: "agent-1"}},
			{Actor: ActorRef{Kind: ActorAgent, ID: "agent-2"}},
		},
	}
}

type fixedContextProvider struct {
	snapshot OntologyContextSnapshot
}

func (p fixedContextProvider) BuildDecisionContext(context.Context, DecisionContextRequest) (OntologyContextSnapshot, error) {
	return p.snapshot, nil
}

type recordingContextProvider struct {
	snapshot OntologyContextSnapshot
	request  DecisionContextRequest
}

func (p *recordingContextProvider) BuildDecisionContext(_ context.Context, request DecisionContextRequest) (OntologyContextSnapshot, error) {
	p.request = request
	return p.snapshot, nil
}

type failingContextProvider struct{}

func (failingContextProvider) BuildDecisionContext(context.Context, DecisionContextRequest) (OntologyContextSnapshot, error) {
	return OntologyContextSnapshot{}, errors.New("context unavailable")
}

type sequenceHILIDs struct {
	n int
}

func (g *sequenceHILIDs) NewIDContext() (string, error) {
	g.n++
	return "hil-id-" + string(rune('0'+g.n)), nil
}
