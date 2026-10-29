package governance

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"orrery_v2/internal/app/authorization"
	"orrery_v2/internal/app/identity"
	"orrery_v2/internal/app/ontology"
	"orrery_v2/internal/app/ontologyscope"
	ontologycontract "orrery_v2/internal/app/ontologyview/contract"
)

var ErrStateConflict = ontologycontract.ErrStateConflict

const defaultPromotionPlanTTL = 10 * time.Minute

type CreatePromotionPlanRequest struct {
	TenantID         string
	ProposalID       string
	ExpectedRevision int64
}

type DecideProposalRequest struct {
	TenantID         string
	ProposalID       string
	ExpectedRevision int64
	Decision         ontology.MetadataProposalStatus
	Reason           string
	EvidenceRefs     []ontology.MetadataEvidenceRef
}

type PromotionPlanChangeResult struct {
	Operation       ontology.PromotionOperation `json:"operation"`
	TargetKind      string                      `json:"target_kind"`
	TargetID        string                      `json:"target_id"`
	Field           string                      `json:"field"`
	ExpectedVersion int                         `json:"expected_version,omitempty"`
	CurrentVersion  int                         `json:"current_version,omitempty"`
}

type PromotionPlanResult struct {
	PlanID           string                      `json:"plan_id"`
	ProposalID       string                      `json:"proposal_id"`
	ProposalRevision int64                       `json:"proposal_revision"`
	ExpiresAt        time.Time                   `json:"expires_at"`
	Changes          []PromotionPlanChangeResult `json:"changes"`
}

type ApplyPromotionPlanRequest struct {
	TenantID       string
	PlanID         string
	IdempotencyKey string
	Confirmed      bool
}

type ProposalCommandStore interface {
	ontology.MetadataPromotionProposalStore
}

type AuthorizedPromotionTarget struct {
	Target ontology.MetadataPromotionPlanTarget
	Scope  ontologyscope.AuthorizedScopeContext
}

type CommandPolicyRequest struct {
	Operation     string
	Proposal      ontology.OntologyMetadataProposal
	ProposalScope ontologyscope.AuthorizedScopeContext
	Targets       []AuthorizedPromotionTarget
	EvidenceRefs  []ontology.MetadataEvidenceRef
}

type CommandPolicy interface {
	Authorize(context.Context, identity.Principal, CommandPolicyRequest) error
}

type CommandServiceOptions struct {
	Gate        *ontologyscope.ScopeAccessGate
	Proposals   ProposalCommandStore
	Registry    ontology.Registry
	Plans       ontology.MetadataPromotionPlanStore
	UnitOfWork  ontology.MetadataPromotionUnitOfWork
	Policy      CommandPolicy
	Budgets     *ontologycontract.BudgetPolicy
	Concurrency *ontologycontract.ConcurrencyLimiter
	PlanTTL     time.Duration
	Now         func() time.Time
	NewPlanID   func() (string, error)
}

type CommandService struct {
	gate        *ontologyscope.ScopeAccessGate
	proposals   ProposalCommandStore
	registry    ontology.Registry
	plans       ontology.MetadataPromotionPlanStore
	unitOfWork  ontology.MetadataPromotionUnitOfWork
	policy      CommandPolicy
	budgets     *ontologycontract.BudgetPolicy
	concurrency *ontologycontract.ConcurrencyLimiter
	planTTL     time.Duration
	now         func() time.Time
	newPlanID   func() (string, error)
}

func NewCommandService(options CommandServiceOptions) (*CommandService, error) {
	if options.Gate == nil || options.Proposals == nil || options.Registry == nil || options.Plans == nil || options.UnitOfWork == nil || options.Policy == nil {
		return nil, ontologyscope.ErrDependencyUnavailable
	}
	var err error
	if options.Budgets == nil {
		options.Budgets, err = ontologycontract.NewBudgetPolicy(nil)
		if err != nil {
			return nil, err
		}
	}
	if options.Concurrency == nil {
		options.Concurrency, err = ontologycontract.NewConcurrencyLimiter(nil)
		if err != nil {
			return nil, err
		}
	}
	if options.PlanTTL == 0 {
		options.PlanTTL = defaultPromotionPlanTTL
	}
	if options.PlanTTL <= 0 || options.PlanTTL > defaultPromotionPlanTTL {
		return nil, fmt.Errorf("%w: promotion plan TTL must be positive and at most ten minutes", ontologycontract.ErrInvalidRequest)
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.NewPlanID == nil {
		options.NewPlanID = ontology.NewMetadataPromotionPlanID
	}
	return &CommandService{
		gate: options.Gate, proposals: options.Proposals, registry: options.Registry, plans: options.Plans,
		unitOfWork: options.UnitOfWork, policy: options.Policy, budgets: options.Budgets, concurrency: options.Concurrency,
		planTTL: options.PlanTTL, now: options.Now, newPlanID: options.NewPlanID,
	}, nil
}

func (s *CommandService) DecideProposal(ctx context.Context, principal identity.Principal, request DecideProposalRequest) (ontology.OntologyMetadataProposal, error) {
	request, err := normalizeDecideProposalRequest(request)
	if err != nil {
		return ontology.OntologyMetadataProposal{}, err
	}
	principal, principalKey, err := promotionPrincipal(principal)
	if err != nil {
		return ontology.OntologyMetadataProposal{}, err
	}
	actorID, actorKind, err := promotionActor(principal)
	if err != nil {
		return ontology.OntologyMetadataProposal{}, err
	}
	release, err := s.concurrency.Acquire(ctx, ontologycontract.ConcurrencyPlan, principalKey)
	if err != nil {
		return ontology.OntologyMetadataProposal{}, err
	}
	defer release()
	bounded, budget, cancel, err := s.budgets.Start(ctx, ontologycontract.QueryPlan)
	if err != nil {
		return ontology.OntologyMetadataProposal{}, err
	}
	defer cancel()

	authorized, err := s.gate.AuthorizeManage(bounded, principal, request.TenantID)
	if err != nil {
		return ontology.OntologyMetadataProposal{}, err
	}
	if err := requirePlanSnapshot(authorized.Snapshot(), principalKey); err != nil {
		return ontology.OntologyMetadataProposal{}, err
	}
	if err := budget.ConsumeStoreReads(1); err != nil {
		return ontology.OntologyMetadataProposal{}, err
	}
	proposal, err := s.proposals.GetMetadataProposalForTenant(bounded, request.TenantID, request.ProposalID)
	if err != nil {
		return ontology.OntologyMetadataProposal{}, exactQueryError(bounded, budget, err)
	}
	if err := validatePromotionProposalVersion(proposal, request.ExpectedRevision); err != nil {
		return ontology.OntologyMetadataProposal{}, err
	}
	if proposal.Status != ontology.MetadataProposalStatusProposed {
		return ontology.OntologyMetadataProposal{}, fmt.Errorf("%w: metadata proposal was already decided", ErrStateConflict)
	}
	targets, err := s.promotionDecisionTargets(bounded, s.registry, proposal, request.Decision)
	if err != nil {
		return ontology.OntologyMetadataProposal{}, err
	}
	if err := budget.ConsumeCandidates(len(targets)); err != nil {
		return ontology.OntologyMetadataProposal{}, err
	}
	authorizedTargets, err := s.authorizePromotionTargets(bounded, principal, authorized, targets)
	if err != nil {
		return ontology.OntologyMetadataProposal{}, err
	}
	if err := s.policy.Authorize(bounded, principal, CommandPolicyRequest{
		Operation: "ontology.governance.decision", Proposal: proposal, ProposalScope: authorized, Targets: authorizedTargets,
		EvidenceRefs: request.EvidenceRefs,
	}); err != nil {
		return ontology.OntologyMetadataProposal{}, err
	}

	if err := budget.ConsumeStoreReads(1); err != nil {
		return ontology.OntologyMetadataProposal{}, err
	}
	current, err := s.proposals.GetMetadataProposalForTenant(bounded, request.TenantID, request.ProposalID)
	if err != nil {
		return ontology.OntologyMetadataProposal{}, exactQueryError(bounded, budget, err)
	}
	if !samePromotionProposalVersion(proposal, current) || current.Status != ontology.MetadataProposalStatusProposed {
		return ontology.OntologyMetadataProposal{}, fmt.Errorf("%w: metadata proposal changed while deciding", ErrStateConflict)
	}
	currentTargets, err := s.promotionDecisionTargets(bounded, s.registry, current, request.Decision)
	if err != nil || !promotionTargetsEqual(targets, currentTargets) {
		if err == nil {
			err = fmt.Errorf("%w: metadata proposal target changed while deciding", ErrStateConflict)
		}
		return ontology.OntologyMetadataProposal{}, err
	}
	fresh, err := s.gate.AuthorizeManage(bounded, principal, request.TenantID)
	if err != nil {
		return ontology.OntologyMetadataProposal{}, err
	}
	if err := requirePlanSnapshot(fresh.Snapshot(), principalKey); err != nil {
		return ontology.OntologyMetadataProposal{}, err
	}
	if fresh.Binding().BindingVersion != authorized.Binding().BindingVersion || !sameAuthorizationSnapshot(authorized.Snapshot(), fresh.Snapshot()) {
		return ontology.OntologyMetadataProposal{}, fmt.Errorf("%w: authorization context changed while deciding", ErrStateConflict)
	}
	freshTargets, err := s.authorizePromotionTargets(bounded, principal, fresh, currentTargets)
	if err != nil {
		return ontology.OntologyMetadataProposal{}, err
	}
	if err := s.policy.Authorize(bounded, principal, CommandPolicyRequest{
		Operation: "ontology.governance.decision", Proposal: current, ProposalScope: fresh, Targets: freshTargets,
		EvidenceRefs: request.EvidenceRefs,
	}); err != nil {
		return ontology.OntologyMetadataProposal{}, err
	}
	if err := budget.ConsumeResults(1); err != nil {
		return ontology.OntologyMetadataProposal{}, err
	}
	var guardAuthorized ontologyscope.AuthorizedScopeContext
	guard := func(guardCtx context.Context) error {
		latest, guardErr := s.gate.AuthorizeManage(guardCtx, principal, request.TenantID)
		if guardErr != nil {
			return guardErr
		}
		if guardErr = requirePlanSnapshot(latest.Snapshot(), principalKey); guardErr != nil {
			return guardErr
		}
		if latest.Binding().BindingVersion != fresh.Binding().BindingVersion || !sameAuthorizationSnapshot(fresh.Snapshot(), latest.Snapshot()) {
			return fmt.Errorf("%w: authorization context changed while deciding", ErrStateConflict)
		}
		if guardErr = budget.ConsumeStoreReads(1); guardErr != nil {
			return guardErr
		}
		latestProposal, guardErr := s.proposals.GetMetadataProposalForTenant(guardCtx, request.TenantID, request.ProposalID)
		if guardErr != nil {
			return exactQueryError(guardCtx, budget, guardErr)
		}
		if !samePromotionProposalVersion(current, latestProposal) || latestProposal.Status != ontology.MetadataProposalStatusProposed {
			return fmt.Errorf("%w: metadata proposal changed while deciding", ErrStateConflict)
		}
		latestTargets, guardErr := s.promotionDecisionTargets(guardCtx, s.registry, latestProposal, request.Decision)
		if guardErr != nil || !promotionTargetsEqual(currentTargets, latestTargets) {
			if guardErr == nil {
				guardErr = fmt.Errorf("%w: metadata proposal target changed while deciding", ErrStateConflict)
			}
			return guardErr
		}
		latestAuthorizedTargets, guardErr := s.authorizePromotionTargets(guardCtx, principal, latest, latestTargets)
		if guardErr != nil {
			return guardErr
		}
		if guardErr = s.policy.Authorize(guardCtx, principal, CommandPolicyRequest{
			Operation: "ontology.governance.decision", Proposal: latestProposal, ProposalScope: latest, Targets: latestAuthorizedTargets,
			EvidenceRefs: request.EvidenceRefs,
		}); guardErr != nil {
			return guardErr
		}
		guardAuthorized = latest
		return nil
	}
	var decided ontology.OntologyMetadataProposal
	err = s.unitOfWork.WithinMetadataPromotionTransaction(bounded, guard, func(stores ontology.MetadataPromotionTransactionStores) error {
		if stores.Proposals == nil || stores.Registry == nil || stores.SecurityRevisions == nil || stores.ScopeBindings == nil {
			return ontologyscope.ErrDependencyUnavailable
		}
		binding := guardAuthorized.Binding()
		if err := stores.ScopeBindings.FenceScopeBinding(bounded, ontology.MetadataPromotionScopeBinding{
			TenantID:                binding.TenantID,
			BindingVersion:          binding.BindingVersion,
			PersistedBindingVersion: binding.PersistedBindingVersion,
			PersistenceRequired:     binding.PersistenceRequired || binding.Kind == ontologyscope.KindCustom,
		}); err != nil {
			return err
		}
		snapshot := guardAuthorized.Snapshot()
		if err := stores.SecurityRevisions.FenceSecurityRevisions(bounded, ontology.MetadataPromotionSecurityRevisions{
			Authorization: snapshot.AuthorizationRevision,
			Policy:        snapshot.PolicyRevision,
		}); err != nil {
			return err
		}
		if err := budget.ConsumeStoreReads(1); err != nil {
			return err
		}
		locked, err := stores.Proposals.GetMetadataProposalForTenant(bounded, request.TenantID, request.ProposalID)
		if err != nil {
			return err
		}
		if !samePromotionProposalVersion(current, locked) || locked.Status != ontology.MetadataProposalStatusProposed {
			return fmt.Errorf("%w: metadata proposal changed while deciding", ErrStateConflict)
		}
		lockedTargets, err := s.promotionDecisionTargets(bounded, stores.Registry, locked, request.Decision)
		if err != nil || !promotionTargetsEqual(currentTargets, lockedTargets) {
			if err == nil {
				err = fmt.Errorf("%w: metadata proposal target changed while deciding", ErrStateConflict)
			}
			return err
		}
		decided, err = stores.Proposals.DecideMetadataProposalForTenant(bounded, request.TenantID, ontology.MetadataProposalDecision{
			TenantID: request.TenantID, ProposalID: request.ProposalID, Status: request.Decision,
			ActorID: actorID, ActorKind: actorKind, Reason: request.Reason,
			EvidenceRefs: append([]ontology.MetadataEvidenceRef(nil), request.EvidenceRefs...), ExpectedRevision: request.ExpectedRevision,
		})
		return err
	})
	if errors.Is(err, ontology.ErrVersionConflict) || errors.Is(err, ontology.ErrNotFound) {
		return ontology.OntologyMetadataProposal{}, fmt.Errorf("%w: metadata proposal or authorization context changed while deciding", ErrStateConflict)
	}
	if err != nil {
		return ontology.OntologyMetadataProposal{}, err
	}
	return decided, nil
}

func (s *CommandService) CreatePromotionPlan(ctx context.Context, principal identity.Principal, request CreatePromotionPlanRequest) (PromotionPlanResult, error) {
	request, err := normalizeCreatePromotionPlanRequest(request)
	if err != nil {
		return PromotionPlanResult{}, err
	}
	principal, principalKey, err := promotionPrincipal(principal)
	if err != nil {
		return PromotionPlanResult{}, err
	}
	release, err := s.concurrency.Acquire(ctx, ontologycontract.ConcurrencyPlan, principalKey)
	if err != nil {
		return PromotionPlanResult{}, err
	}
	defer release()
	bounded, budget, cancel, err := s.budgets.Start(ctx, ontologycontract.QueryPlan)
	if err != nil {
		return PromotionPlanResult{}, err
	}
	defer cancel()

	authorized, err := s.gate.AuthorizeManage(bounded, principal, request.TenantID)
	if err != nil {
		return PromotionPlanResult{}, err
	}
	if err := requirePlanSnapshot(authorized.Snapshot(), principalKey); err != nil {
		return PromotionPlanResult{}, err
	}
	if err := budget.ConsumeStoreReads(1); err != nil {
		return PromotionPlanResult{}, err
	}
	proposal, err := s.proposals.GetMetadataProposalForTenant(bounded, request.TenantID, request.ProposalID)
	if err != nil {
		return PromotionPlanResult{}, exactQueryError(bounded, budget, err)
	}
	if err := validatePromotionProposalVersion(proposal, request.ExpectedRevision); err != nil {
		return PromotionPlanResult{}, err
	}
	material, err := ontology.BuildMetadataPromotionPlanMaterial(bounded, s.registry, proposal)
	if err != nil {
		return PromotionPlanResult{}, err
	}
	if err := budget.ConsumeCandidates(len(material.Targets)); err != nil {
		return PromotionPlanResult{}, err
	}
	authorizedTargets, err := s.authorizePromotionTargets(bounded, principal, authorized, material.Targets)
	if err != nil {
		return PromotionPlanResult{}, err
	}
	if err := s.policy.Authorize(bounded, principal, CommandPolicyRequest{
		Operation: "ontology.governance.plan", Proposal: proposal, ProposalScope: authorized, Targets: authorizedTargets,
	}); err != nil {
		return PromotionPlanResult{}, err
	}

	if err := budget.ConsumeStoreReads(1); err != nil {
		return PromotionPlanResult{}, err
	}
	current, err := s.proposals.GetMetadataProposalForTenant(bounded, request.TenantID, request.ProposalID)
	if err != nil {
		return PromotionPlanResult{}, exactQueryError(bounded, budget, err)
	}
	if !samePromotionProposalVersion(proposal, current) {
		return PromotionPlanResult{}, fmt.Errorf("%w: metadata proposal changed while creating the promotion plan", ontology.ErrVersionConflict)
	}
	currentMaterial, err := ontology.BuildMetadataPromotionPlanMaterial(bounded, s.registry, current)
	if err != nil {
		return PromotionPlanResult{}, err
	}
	if !promotionMaterialEqual(material, currentMaterial) {
		return PromotionPlanResult{}, fmt.Errorf("%w: metadata promotion target changed while creating the plan", ontology.ErrVersionConflict)
	}
	fresh, err := s.gate.AuthorizeManage(bounded, principal, request.TenantID)
	if err != nil {
		return PromotionPlanResult{}, err
	}
	if err := requirePlanSnapshot(fresh.Snapshot(), principalKey); err != nil {
		return PromotionPlanResult{}, err
	}
	if fresh.Binding().BindingVersion != authorized.Binding().BindingVersion || !sameAuthorizationSnapshot(authorized.Snapshot(), fresh.Snapshot()) {
		return PromotionPlanResult{}, fmt.Errorf("%w: authorization context changed while creating the plan", ErrStateConflict)
	}
	freshTargets, err := s.authorizePromotionTargets(bounded, principal, fresh, currentMaterial.Targets)
	if err != nil {
		return PromotionPlanResult{}, err
	}
	if err := s.policy.Authorize(bounded, principal, CommandPolicyRequest{
		Operation: "ontology.governance.plan", Proposal: current, ProposalScope: fresh, Targets: freshTargets,
	}); err != nil {
		return PromotionPlanResult{}, err
	}
	planID, err := s.newPlanID()
	if err != nil {
		return PromotionPlanResult{}, fmt.Errorf("%w: promotion plan id generation failed", ErrUnavailable)
	}
	now := s.now().UTC()
	snapshot := fresh.Snapshot()
	record := ontology.MetadataPromotionPlanRecord{
		PlanID: planID, PrincipalKey: principalKey, TenantID: request.TenantID,
		BindingVersion: fresh.Binding().BindingVersion,
		ProposalID:     current.ID, ProposalRevision: current.Revision, ProposalContentHash: current.ContentHash,
		Targets: currentMaterial.Targets, DiffHash: currentMaterial.DiffHash, Diff: currentMaterial.Diff,
		AuthorizationRevision: snapshot.AuthorizationRevision, PolicyRevision: snapshot.PolicyRevision,
		ExpiresAt: now.Add(s.planTTL), State: ontology.MetadataPromotionPlanActive, CreatedAt: now, UpdatedAt: now,
	}
	if err := s.plans.SaveMetadataPromotionPlan(bounded, record); err != nil {
		return PromotionPlanResult{}, err
	}
	if err := budget.ConsumeResults(1); err != nil {
		return PromotionPlanResult{}, err
	}
	return PromotionPlanResult{
		PlanID: record.PlanID, ProposalID: record.ProposalID, ProposalRevision: record.ProposalRevision,
		ExpiresAt: record.ExpiresAt, Changes: safePromotionPlanChanges(record.Diff),
	}, nil
}

func (s *CommandService) ApplyPromotionPlan(ctx context.Context, principal identity.Principal, request ApplyPromotionPlanRequest) (ontology.MetadataProposalPromotionReceipt, error) {
	request, err := normalizeApplyPromotionPlanRequest(request)
	if err != nil {
		return ontology.MetadataProposalPromotionReceipt{}, err
	}
	principal, principalKey, err := promotionPrincipal(principal)
	if err != nil {
		return ontology.MetadataProposalPromotionReceipt{}, err
	}
	actorID, actorKind, err := promotionActor(principal)
	if err != nil {
		return ontology.MetadataProposalPromotionReceipt{}, err
	}
	release, err := s.concurrency.Acquire(ctx, ontologycontract.ConcurrencyPlan, principalKey)
	if err != nil {
		return ontology.MetadataProposalPromotionReceipt{}, err
	}
	defer release()
	bounded, budget, cancel, err := s.budgets.Start(ctx, ontologycontract.QueryPlan)
	if err != nil {
		return ontology.MetadataProposalPromotionReceipt{}, err
	}
	defer cancel()

	authorized, err := s.gate.AuthorizeManage(bounded, principal, request.TenantID)
	if err != nil {
		return ontology.MetadataProposalPromotionReceipt{}, err
	}
	if err := requirePlanSnapshot(authorized.Snapshot(), principalKey); err != nil {
		return ontology.MetadataProposalPromotionReceipt{}, err
	}
	if err := budget.ConsumeStoreReads(1); err != nil {
		return ontology.MetadataProposalPromotionReceipt{}, err
	}
	plan, err := s.plans.GetMetadataPromotionPlan(bounded, request.PlanID)
	if errors.Is(err, ontology.ErrNotFound) {
		return ontology.MetadataProposalPromotionReceipt{}, ErrUnavailable
	}
	if err != nil {
		return ontology.MetadataProposalPromotionReceipt{}, err
	}
	if plan.TenantID != request.TenantID || plan.PrincipalKey != principalKey {
		return ontology.MetadataProposalPromotionReceipt{}, ErrUnavailable
	}
	if plan.State == ontology.MetadataPromotionPlanActive && !s.now().UTC().Before(plan.ExpiresAt) {
		return ontology.MetadataProposalPromotionReceipt{}, s.transitionPromotionPlanTerminal(
			bounded, plan, ontology.MetadataPromotionPlanExpired, "expired", fmt.Errorf("%w: promotion plan expired", ErrStateConflict),
		)
	}
	authorizedTargets, err := s.authorizePromotionTargets(bounded, principal, authorized, plan.Targets)
	if err != nil {
		if plan.State == ontology.MetadataPromotionPlanActive && errors.Is(err, ontologyscope.ErrAccessDenied) {
			return ontology.MetadataProposalPromotionReceipt{}, s.transitionPromotionPlanTerminal(
				bounded, plan, ontology.MetadataPromotionPlanInvalidated, "target_authorization_revoked", err,
			)
		}
		return ontology.MetadataProposalPromotionReceipt{}, err
	}
	if err := s.policy.Authorize(bounded, principal, CommandPolicyRequest{
		Operation: "ontology.governance.apply", Proposal: promotionPlanPolicyProposal(plan), ProposalScope: authorized, Targets: authorizedTargets,
	}); err != nil {
		if plan.State == ontology.MetadataPromotionPlanActive && errors.Is(err, ontologyscope.ErrAccessDenied) {
			return ontology.MetadataProposalPromotionReceipt{}, s.transitionPromotionPlanTerminal(
				bounded, plan, ontology.MetadataPromotionPlanInvalidated, "mutation_policy_revoked", err,
			)
		}
		return ontology.MetadataProposalPromotionReceipt{}, err
	}
	if plan.State != ontology.MetadataPromotionPlanActive && plan.State != ontology.MetadataPromotionPlanConsumed {
		return ontology.MetadataProposalPromotionReceipt{}, fmt.Errorf("%w: promotion plan is %s", ErrStateConflict, plan.State)
	}
	guardProposal := promotionPlanPolicyProposal(plan)
	if plan.State == ontology.MetadataPromotionPlanActive {
		if err := validatePromotionPlanAuthorization(plan, authorized); err != nil {
			return ontology.MetadataProposalPromotionReceipt{}, s.transitionPromotionPlanTerminal(
				bounded, plan, ontology.MetadataPromotionPlanInvalidated, "authorization_context_changed", err,
			)
		}
		if err := budget.ConsumeStoreReads(1); err != nil {
			return ontology.MetadataProposalPromotionReceipt{}, err
		}
		proposal, proposalErr := s.proposals.GetMetadataProposalForTenant(bounded, request.TenantID, plan.ProposalID)
		if errors.Is(proposalErr, ontology.ErrNotFound) {
			return ontology.MetadataProposalPromotionReceipt{}, s.transitionPromotionPlanTerminal(
				bounded, plan, ontology.MetadataPromotionPlanInvalidated, "proposal_unavailable", ErrUnavailable,
			)
		}
		if proposalErr != nil {
			return ontology.MetadataProposalPromotionReceipt{}, proposalErr
		}
		if !samePromotionProposalRecord(plan, proposal) {
			return ontology.MetadataProposalPromotionReceipt{}, s.transitionPromotionPlanTerminal(
				bounded, plan, ontology.MetadataPromotionPlanInvalidated, "proposal_changed", fmt.Errorf("%w: promotion proposal changed", ErrStateConflict),
			)
		}
		guardProposal = proposal
		if err := s.policy.Authorize(bounded, principal, CommandPolicyRequest{
			Operation: "ontology.governance.apply", Proposal: proposal, ProposalScope: authorized, Targets: authorizedTargets,
		}); err != nil {
			if errors.Is(err, ontologyscope.ErrAccessDenied) {
				return ontology.MetadataProposalPromotionReceipt{}, s.transitionPromotionPlanTerminal(
					bounded, plan, ontology.MetadataPromotionPlanInvalidated, "mutation_policy_revoked", err,
				)
			}
			return ontology.MetadataProposalPromotionReceipt{}, err
		}
		material, materialErr := ontology.BuildMetadataPromotionPlanMaterial(bounded, s.registry, proposal)
		if materialErr != nil || !ontology.MetadataPromotionPlanMaterialMatches(plan, material) {
			if materialErr == nil {
				materialErr = fmt.Errorf("%w: promotion target or diff changed", ErrStateConflict)
			}
			return ontology.MetadataProposalPromotionReceipt{}, s.transitionPromotionPlanTerminal(
				bounded, plan, ontology.MetadataPromotionPlanInvalidated, "target_or_diff_changed", materialErr,
			)
		}
		if err := budget.ConsumeCandidates(len(material.Targets)); err != nil {
			return ontology.MetadataProposalPromotionReceipt{}, err
		}
	}
	if err := budget.ConsumeResults(1); err != nil {
		return ontology.MetadataProposalPromotionReceipt{}, err
	}

	var receipt ontology.MetadataProposalPromotionReceipt
	var terminalErr error
	var guardAuthorized ontologyscope.AuthorizedScopeContext
	var guardReason string
	guard := func(guardCtx context.Context) error {
		fresh, guardErr := s.gate.AuthorizeManage(guardCtx, principal, request.TenantID)
		if guardErr != nil {
			if errors.Is(guardErr, ontologyscope.ErrAccessDenied) {
				guardReason = "scope_authorization_revoked"
			}
			return guardErr
		}
		if guardErr = requirePlanSnapshot(fresh.Snapshot(), principalKey); guardErr != nil {
			return guardErr
		}
		if plan.State == ontology.MetadataPromotionPlanActive {
			if guardErr = validatePromotionPlanAuthorization(plan, fresh); guardErr != nil {
				guardReason = "authorization_context_changed"
				return guardErr
			}
		}
		freshTargets, guardErr := s.authorizePromotionTargets(guardCtx, principal, fresh, plan.Targets)
		if guardErr != nil {
			if errors.Is(guardErr, ontologyscope.ErrAccessDenied) {
				guardReason = "target_authorization_revoked"
			}
			return guardErr
		}
		if guardErr = s.policy.Authorize(guardCtx, principal, CommandPolicyRequest{
			Operation: "ontology.governance.apply", Proposal: guardProposal, ProposalScope: fresh, Targets: freshTargets,
		}); guardErr != nil {
			if errors.Is(guardErr, ontologyscope.ErrAccessDenied) {
				guardReason = "mutation_policy_revoked"
			}
			return guardErr
		}
		guardAuthorized = fresh
		return nil
	}
	err = s.unitOfWork.WithinMetadataPromotionTransaction(bounded, guard, func(stores ontology.MetadataPromotionTransactionStores) error {
		if stores.Plans == nil || stores.Proposals == nil || stores.Registry == nil || stores.Receipts == nil || stores.SecurityRevisions == nil || stores.ScopeBindings == nil {
			return ontologyscope.ErrDependencyUnavailable
		}
		if err := budget.ConsumeStoreReads(1); err != nil {
			return err
		}
		locked, err := stores.Plans.GetMetadataPromotionPlanForUpdate(bounded, request.PlanID)
		if err != nil {
			return err
		}
		if locked.TenantID != request.TenantID || locked.PrincipalKey != principalKey {
			return ErrUnavailable
		}
		if locked.State == ontology.MetadataPromotionPlanConsumed {
			if locked.IdempotencyKey != request.IdempotencyKey {
				return fmt.Errorf("%w: promotion plan was already consumed", ErrStateConflict)
			}
			if err := budget.ConsumeStoreReads(1); err != nil {
				return err
			}
			receipt, err = stores.Receipts.GetPromotionReceipt(bounded, locked.ReceiptID)
			if err != nil {
				return err
			}
			if receipt.TenantID != locked.TenantID || receipt.ProposalID != locked.ProposalID || receipt.IdempotencyKey != request.IdempotencyKey {
				return ontologyscope.ErrDependencyUnavailable
			}
			return nil
		}
		if locked.State != ontology.MetadataPromotionPlanActive {
			return fmt.Errorf("%w: promotion plan is %s", ErrStateConflict, locked.State)
		}
		if !s.now().UTC().Before(locked.ExpiresAt) {
			return commitPromotionPlanTerminal(stores.Plans, bounded, locked.PlanID, ontology.MetadataPromotionPlanExpired, "expired", fmt.Errorf("%w: promotion plan expired", ErrStateConflict), &terminalErr)
		}
		if err := validatePromotionPlanAuthorization(locked, guardAuthorized); err != nil {
			return commitPromotionPlanTerminal(stores.Plans, bounded, locked.PlanID, ontology.MetadataPromotionPlanInvalidated, "authorization_context_changed", err, &terminalErr)
		}
		freshBinding := guardAuthorized.Binding()
		if err := stores.ScopeBindings.FenceScopeBinding(bounded, ontology.MetadataPromotionScopeBinding{
			TenantID:                locked.TenantID,
			BindingVersion:          locked.BindingVersion,
			PersistedBindingVersion: freshBinding.PersistedBindingVersion,
			PersistenceRequired:     freshBinding.PersistenceRequired || freshBinding.Kind == ontologyscope.KindCustom,
		}); err != nil {
			if errors.Is(err, ontology.ErrVersionConflict) {
				return commitPromotionPlanTerminal(stores.Plans, bounded, locked.PlanID, ontology.MetadataPromotionPlanInvalidated, "authorization_context_changed", fmt.Errorf("%w: promotion plan scope binding changed", ErrStateConflict), &terminalErr)
			}
			return err
		}
		if err := stores.SecurityRevisions.FenceSecurityRevisions(bounded, ontology.MetadataPromotionSecurityRevisions{
			Authorization: locked.AuthorizationRevision,
			Policy:        locked.PolicyRevision,
		}); err != nil {
			if errors.Is(err, ontology.ErrVersionConflict) {
				return commitPromotionPlanTerminal(stores.Plans, bounded, locked.PlanID, ontology.MetadataPromotionPlanInvalidated, "authorization_context_changed", fmt.Errorf("%w: promotion plan security context changed", ErrStateConflict), &terminalErr)
			}
			return err
		}
		if err := budget.ConsumeStoreReads(1); err != nil {
			return err
		}
		proposal, err := stores.Proposals.GetMetadataProposalForTenant(bounded, request.TenantID, locked.ProposalID)
		if err != nil {
			if errors.Is(err, ontology.ErrNotFound) {
				return commitPromotionPlanTerminal(stores.Plans, bounded, locked.PlanID, ontology.MetadataPromotionPlanInvalidated, "proposal_unavailable", ErrUnavailable, &terminalErr)
			}
			return err
		}
		if !samePromotionProposalRecord(locked, proposal) {
			return commitPromotionPlanTerminal(stores.Plans, bounded, locked.PlanID, ontology.MetadataPromotionPlanInvalidated, "proposal_changed", fmt.Errorf("%w: promotion proposal changed", ErrStateConflict), &terminalErr)
		}
		material, err := ontology.BuildMetadataPromotionPlanMaterial(bounded, stores.Registry, proposal)
		if err != nil || !ontology.MetadataPromotionPlanMaterialMatches(locked, material) {
			if err == nil {
				err = fmt.Errorf("%w: promotion target or diff changed", ErrStateConflict)
			}
			return commitPromotionPlanTerminal(stores.Plans, bounded, locked.PlanID, ontology.MetadataPromotionPlanInvalidated, "target_or_diff_changed", err, &terminalErr)
		}
		receipt, err = ontology.ApplyMetadataProposalPromotion(bounded, ontology.MetadataProposalPromotionApplyRequest{
			Registry: stores.Registry, ReceiptStore: stores.Receipts, Proposal: proposal, TenantID: locked.TenantID,
			ActorID: actorID, ActorKind: actorKind, IdempotencyKey: request.IdempotencyKey,
			ConfirmationToken: ontology.MetadataProposalPromotionConfirmationToken(material.DryRun),
		})
		if err != nil {
			return err
		}
		if ontology.MetadataPromotionRequiresMappingProjectionRefresh(receipt.Changes) {
			if stores.Refreshes == nil {
				return ontologyscope.ErrDependencyUnavailable
			}
			if err := stores.Refreshes.EnqueueMappingProjectionRefresh(bounded, receipt.ReceiptID, receipt.TenantID); err != nil {
				return err
			}
		}
		_, err = stores.Plans.TransitionMetadataPromotionPlan(bounded, ontology.MetadataPromotionPlanTransition{
			PlanID: locked.PlanID, ExpectedState: ontology.MetadataPromotionPlanActive, State: ontology.MetadataPromotionPlanConsumed,
			IdempotencyKey: request.IdempotencyKey, ReceiptID: receipt.ReceiptID,
		})
		return err
	})
	if err != nil {
		if guardReason != "" && plan.State == ontology.MetadataPromotionPlanActive {
			return ontology.MetadataProposalPromotionReceipt{}, s.transitionPromotionPlanTerminal(
				bounded, plan, ontology.MetadataPromotionPlanInvalidated, guardReason, err,
			)
		}
		return ontology.MetadataProposalPromotionReceipt{}, err
	}
	if terminalErr != nil {
		return ontology.MetadataProposalPromotionReceipt{}, terminalErr
	}
	return receipt, nil
}

func (s *CommandService) authorizePromotionTargets(ctx context.Context, principal identity.Principal, proposalScope ontologyscope.AuthorizedScopeContext, targets []ontology.MetadataPromotionPlanTarget) ([]AuthorizedPromotionTarget, error) {
	scopes := map[string]ontologyscope.AuthorizedScopeContext{proposalScope.Binding().TenantID: proposalScope}
	out := make([]AuthorizedPromotionTarget, 0, len(targets))
	for _, target := range targets {
		tenantID := strings.TrimSpace(target.DefinitionTenantID)
		if tenantID == "" || tenantID == "system" {
			tenantID = "system"
		} else if tenantID != proposalScope.Binding().TenantID {
			return nil, fmt.Errorf("%w: cross-scope definition target is not allowed", ontologyscope.ErrAccessDenied)
		}
		authorized, exists := scopes[tenantID]
		if !exists {
			var err error
			authorized, err = s.gate.AuthorizeManage(ctx, principal, tenantID)
			if err != nil {
				return nil, err
			}
			scopes[tenantID] = authorized
		}
		if !sameAuthorizationSnapshot(proposalScope.Snapshot(), authorized.Snapshot()) {
			return nil, ontologyscope.ErrDependencyUnavailable
		}
		out = append(out, AuthorizedPromotionTarget{Target: target, Scope: authorized})
	}
	return out, nil
}

func (s *CommandService) transitionPromotionPlanTerminal(ctx context.Context, plan ontology.MetadataPromotionPlanRecord, state ontology.MetadataPromotionPlanState, reason string, result error) error {
	if _, err := s.plans.TransitionMetadataPromotionPlan(ctx, ontology.MetadataPromotionPlanTransition{
		PlanID: plan.PlanID, ExpectedState: ontology.MetadataPromotionPlanActive, State: state, InvalidationReason: reason,
	}); err != nil {
		return err
	}
	return result
}

func commitPromotionPlanTerminal(store ontology.MetadataPromotionPlanStore, ctx context.Context, planID string, state ontology.MetadataPromotionPlanState, reason string, result error, terminalErr *error) error {
	if _, err := store.TransitionMetadataPromotionPlan(ctx, ontology.MetadataPromotionPlanTransition{
		PlanID: planID, ExpectedState: ontology.MetadataPromotionPlanActive, State: state, InvalidationReason: reason,
	}); err != nil {
		return err
	}
	*terminalErr = result
	return nil
}

func validatePromotionPlanAuthorization(plan ontology.MetadataPromotionPlanRecord, authorized ontologyscope.AuthorizedScopeContext) error {
	snapshot := authorized.Snapshot()
	if plan.BindingVersion != authorized.Binding().BindingVersion || plan.PrincipalKey != snapshot.PrincipalKey ||
		plan.AuthorizationRevision != snapshot.AuthorizationRevision || plan.PolicyRevision != snapshot.PolicyRevision {
		return fmt.Errorf("%w: promotion plan authorization context changed", ErrStateConflict)
	}
	return nil
}

func requirePlanSnapshot(snapshot authorization.AuthorizationSnapshot, principalKey string) error {
	if snapshot.PrincipalKey != principalKey || snapshot.AuthorizationRevision == "" || snapshot.PolicyRevision == "" {
		return ontologyscope.ErrDependencyUnavailable
	}
	return nil
}

func sameAuthorizationSnapshot(left, right authorization.AuthorizationSnapshot) bool {
	return left.PrincipalKey == right.PrincipalKey && left.AuthorizationRevision == right.AuthorizationRevision && left.PolicyRevision == right.PolicyRevision
}

func validatePromotionProposalVersion(proposal ontology.OntologyMetadataProposal, expectedRevision int64) error {
	if proposal.Revision != expectedRevision || proposal.ContentHash == "" || proposal.ContentHash != ontology.CanonicalMetadataProposalContentHash(proposal) {
		return fmt.Errorf("%w: metadata proposal revision or content changed", ontology.ErrVersionConflict)
	}
	return nil
}

func samePromotionProposalVersion(left, right ontology.OntologyMetadataProposal) bool {
	return left.ID == right.ID && left.TenantID == right.TenantID && left.Revision == right.Revision && left.ContentHash == right.ContentHash &&
		right.ContentHash == ontology.CanonicalMetadataProposalContentHash(right)
}

func samePromotionProposalRecord(plan ontology.MetadataPromotionPlanRecord, proposal ontology.OntologyMetadataProposal) bool {
	return plan.ProposalID == proposal.ID && plan.TenantID == proposal.TenantID && plan.ProposalRevision == proposal.Revision &&
		plan.ProposalContentHash == proposal.ContentHash && proposal.ContentHash == ontology.CanonicalMetadataProposalContentHash(proposal)
}

func promotionMaterialEqual(left, right ontology.MetadataPromotionPlanMaterial) bool {
	return left.DiffHash == right.DiffHash &&
		promotionChangesEqual(left.Diff, right.Diff) &&
		promotionTargetsEqual(left.Targets, right.Targets)
}

func promotionChangesEqual(left, right []ontology.MetadataProposalPromotionChange) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func promotionTargetsEqual(left, right []ontology.MetadataPromotionPlanTarget) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func promotionPrincipal(principal identity.Principal) (identity.Principal, string, error) {
	principal = identity.NormalizePrincipal(principal)
	principalKey := authorization.PrincipalCacheKey(principal)
	if !principal.Authenticated() || principalKey == "" {
		return identity.Principal{}, "", fmt.Errorf("%w: authenticated principal is required", ontologyscope.ErrAccessDenied)
	}
	return principal, principalKey, nil
}

func promotionActor(principal identity.Principal) (string, string, error) {
	principal = identity.NormalizePrincipal(principal)
	actorKind := strings.TrimSpace(principal.ViewerKind)
	actorID := strings.TrimSpace(principal.Subject)
	if actorKind == identity.ViewerKindUser {
		actorID = firstPromotionValue(principal.UserID, principal.Username, principal.Subject)
	} else if actorKind == identity.ViewerKindAgent {
		actorID = firstPromotionValue(principal.AgentProfileID, principal.Subject, principal.UserID)
	} else if actorKind == identity.ViewerKindSystem {
		actorID = firstPromotionValue(principal.Subject, principal.UserID, "system")
	}
	if actorID == "" || actorKind == "" {
		return "", "", fmt.Errorf("%w: promotion actor is unavailable", ontologyscope.ErrAccessDenied)
	}
	return actorID, actorKind, nil
}

func normalizeCreatePromotionPlanRequest(request CreatePromotionPlanRequest) (CreatePromotionPlanRequest, error) {
	request.TenantID = strings.TrimSpace(request.TenantID)
	request.ProposalID = strings.TrimSpace(request.ProposalID)
	if request.TenantID == "" || request.ProposalID == "" || request.ExpectedRevision <= 0 || len(request.TenantID) > 256 || len(request.ProposalID) > 512 {
		return CreatePromotionPlanRequest{}, fmt.Errorf("%w: tenant, proposal, and positive expected revision are required", ontologycontract.ErrInvalidRequest)
	}
	return request, nil
}

func normalizeDecideProposalRequest(request DecideProposalRequest) (DecideProposalRequest, error) {
	request.TenantID = strings.TrimSpace(request.TenantID)
	request.ProposalID = strings.TrimSpace(request.ProposalID)
	request.Reason = strings.TrimSpace(request.Reason)
	if request.TenantID == "" || request.ProposalID == "" || request.ExpectedRevision <= 0 || request.Reason == "" ||
		len(request.TenantID) > 256 || len(request.ProposalID) > 512 || len(request.Reason) > 4000 || !utf8.ValidString(request.Reason) || len(request.EvidenceRefs) > 100 {
		return DecideProposalRequest{}, fmt.Errorf("%w: tenant, proposal, decision, reason, and positive expected revision are required", ontologycontract.ErrInvalidRequest)
	}
	switch request.Decision {
	case ontology.MetadataProposalStatusAccepted, ontology.MetadataProposalStatusRejected:
	default:
		return DecideProposalRequest{}, fmt.Errorf("%w: decision must be accepted or rejected", ontologycontract.ErrInvalidRequest)
	}
	evidence := make([]ontology.MetadataEvidenceRef, 0, len(request.EvidenceRefs))
	seenEvidence := make(map[string]struct{}, len(request.EvidenceRefs))
	for _, ref := range request.EvidenceRefs {
		ref.Kind = strings.ToLower(strings.TrimSpace(ref.Kind))
		ref.ID = strings.TrimSpace(ref.ID)
		ref.Metadata = nil
		if ref.Kind == "" || ref.ID == "" || len(ref.Kind) > 64 || len(ref.ID) > 512 || !utf8.ValidString(ref.Kind) || !utf8.ValidString(ref.ID) {
			return DecideProposalRequest{}, fmt.Errorf("%w: evidence references require bounded kind and id", ontologycontract.ErrInvalidRequest)
		}
		key := ref.Kind + "\x00" + ref.ID
		if _, duplicate := seenEvidence[key]; duplicate {
			return DecideProposalRequest{}, fmt.Errorf("%w: duplicate evidence reference", ontologycontract.ErrInvalidRequest)
		}
		seenEvidence[key] = struct{}{}
		evidence = append(evidence, ref)
	}
	request.EvidenceRefs = evidence
	return request, nil
}

func normalizeApplyPromotionPlanRequest(request ApplyPromotionPlanRequest) (ApplyPromotionPlanRequest, error) {
	request.TenantID = strings.TrimSpace(request.TenantID)
	request.PlanID = strings.TrimSpace(request.PlanID)
	request.IdempotencyKey = strings.TrimSpace(request.IdempotencyKey)
	if request.TenantID == "" || request.PlanID == "" || request.IdempotencyKey == "" || !request.Confirmed ||
		len(request.TenantID) > 256 || len(request.PlanID) > 256 || len(request.IdempotencyKey) > 256 {
		return ApplyPromotionPlanRequest{}, fmt.Errorf("%w: tenant, plan, idempotency key, and explicit confirmation are required", ontologycontract.ErrInvalidRequest)
	}
	return request, nil
}

func firstPromotionValue(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

func safePromotionPlanChanges(changes []ontology.MetadataProposalPromotionChange) []PromotionPlanChangeResult {
	out := make([]PromotionPlanChangeResult, 0, len(changes))
	for _, change := range changes {
		out = append(out, PromotionPlanChangeResult{
			Operation: change.Operation, TargetKind: change.TargetKind, TargetID: change.TargetID, Field: change.Field,
			ExpectedVersion: change.ExpectedVersion, CurrentVersion: change.CurrentVersion,
		})
	}
	return out
}

func promotionPlanPolicyProposal(plan ontology.MetadataPromotionPlanRecord) ontology.OntologyMetadataProposal {
	proposal := ontology.OntologyMetadataProposal{ID: plan.ProposalID, TenantID: plan.TenantID}
	if len(plan.Targets) > 0 {
		proposal.TargetKind = plan.Targets[0].Kind
		proposal.TargetID = plan.Targets[0].ID
	}
	return proposal
}

func (s *CommandService) promotionDecisionTargets(ctx context.Context, registry ontology.Registry, proposal ontology.OntologyMetadataProposal, decision ontology.MetadataProposalStatus) ([]ontology.MetadataPromotionPlanTarget, error) {
	candidate := proposal
	candidate.Status = ontology.MetadataProposalStatusAccepted
	material, err := ontology.BuildMetadataPromotionPlanMaterial(ctx, registry, candidate)
	if err == nil {
		return material.Targets, nil
	}
	if decision == ontology.MetadataProposalStatusAccepted {
		return nil, err
	}
	target, targetErr := s.existingPromotionDecisionTarget(ctx, registry, proposal)
	if targetErr != nil {
		return nil, targetErr
	}
	return []ontology.MetadataPromotionPlanTarget{target}, nil
}

func (s *CommandService) existingPromotionDecisionTarget(ctx context.Context, registry ontology.Registry, proposal ontology.OntologyMetadataProposal) (ontology.MetadataPromotionPlanTarget, error) {
	target := ontology.MetadataPromotionPlanTarget{
		Kind: strings.ToLower(strings.TrimSpace(proposal.TargetKind)), ID: strings.ToLower(strings.TrimSpace(proposal.TargetID)),
		DefinitionTenantID: strings.TrimSpace(proposal.TenantID), ContentHash: "absent",
	}
	switch target.Kind {
	case "object_type":
		definition, err := registry.GetObjectType(ctx, target.ID)
		if errors.Is(err, ontology.ErrNotFound) {
			return target, nil
		}
		if err != nil {
			return ontology.MetadataPromotionPlanTarget{}, err
		}
		target.DefinitionTenantID, target.Version, target.ContentHash = definition.TenantID, definition.Version, fmt.Sprintf("existing:%d", definition.Version)
	case "link_type":
		definition, err := registry.GetLinkType(ctx, target.ID)
		if errors.Is(err, ontology.ErrNotFound) {
			return target, nil
		}
		if err != nil {
			return ontology.MetadataPromotionPlanTarget{}, err
		}
		target.DefinitionTenantID, target.Version, target.ContentHash = definition.TenantID, definition.Version, fmt.Sprintf("existing:%d", definition.Version)
	case "property_type":
		definition, err := registry.GetPropertyType(ctx, target.ID)
		if errors.Is(err, ontology.ErrNotFound) {
			return target, nil
		}
		if err != nil {
			return ontology.MetadataPromotionPlanTarget{}, err
		}
		target.DefinitionTenantID, target.Version, target.ContentHash = definition.TenantID, definition.Version, fmt.Sprintf("existing:%d", definition.Version)
	case "mapping_rule":
		rules, err := registry.ListMappingRules(ctx, proposal.TenantID)
		if err != nil {
			return ontology.MetadataPromotionPlanTarget{}, err
		}
		for _, definition := range rules {
			if strings.EqualFold(strings.TrimSpace(definition.ID), target.ID) {
				target.DefinitionTenantID, target.Version, target.ContentHash = definition.TenantID, definition.Version, fmt.Sprintf("existing:%d", definition.Version)
				break
			}
		}
	}
	return target, nil
}
