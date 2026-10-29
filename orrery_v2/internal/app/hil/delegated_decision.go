package hil

import (
	"context"
	"errors"
	"strings"
	"time"

	"orrery_v2/internal/app/actioncontract"
)

// DelegationAuthority is installed by trusted server wiring, never by a tool or
// an HTTP decision payload. Authorization is reread after the review finishes.
type DelegationAuthority interface {
	AuthorizeDelegation(context.Context, DecisionRequest, ActorRef, ActorRef) (DelegationEvidence, error)
}

type DelegationEvidence struct {
	GrantID           string    `json:"grant_id"`
	PolicyDigest      string    `json:"policy_digest"`
	ConsentReference  string    `json:"consent_reference"`
	ActionTypeID      string    `json:"action_type_id"`
	Scope             ObjectRef `json:"scope"`
	TenantID          string    `json:"tenant_id"`
	OnBehalfOf        ActorRef  `json:"on_behalf_of"`
	Agent             ActorRef  `json:"agent"`
	ExpiresAt         time.Time `json:"expires_at"`
	ReviewFingerprint string    `json:"review_fingerprint"`
	DecisionKey       string    `json:"decision_key"`
}

type DelegatedDecisionInput struct {
	Decision                string
	Reason                  string
	IdempotencyKey          string
	OnBehalfOf              ActorRef
	ReviewFingerprint       string
	ReviewedAt              time.Time
	TaskID                  string
	ExpectedWorkflowVersion int64
}

func WithDelegationAuthority(authority DelegationAuthority) Option {
	return func(s *Service) { s.delegations = authority }
}

// SetDelegationAuthority is used once during bootstrap, before workers start.
func (s *Service) SetDelegationAuthority(authority DelegationAuthority) { s.delegations = authority }

// DecisionReviewFingerprint includes display material as well as the executable
// binding. A review of an earlier revision cannot approve changed material.
func DecisionReviewFingerprint(decision DecisionRequest) string {
	decision.Status = DecisionPending
	return stableFingerprint(decision)
}

func DelegatedDecisionEligible(decision DecisionRequest) bool {
	if decision.Status != DecisionPending || decision.RiskLevel != RiskLow ||
		(decision.Route.Mode != ModeHIL && decision.Route.Mode != ModeApproval) ||
		!decision.Context.Complete || decision.Context.Conflict || len(decision.Context.EvidenceRefs) == 0 ||
		strings.TrimSpace(decision.ArgsHash) == "" || strings.TrimSpace(decision.Purpose) == "" ||
		actioncontract.IsHumanBusinessDecisionAction(actioncontract.ActionTypeID(decision.ActionTypeID)) {
		return false
	}
	for _, key := range []string{"human_intent_required", "human_only", "requires_human_confirmation", "approval_forced_human", "approval_force_manual"} {
		if value, exists := decision.PayloadPreview[key]; exists && value != false && value != "false" {
			return false
		}
	}
	if source, _ := decision.PayloadPreview["approval_decision_source"].(string); source == "forced_human" {
		return false
	}
	if mode, _ := decision.PayloadPreview["approval_effective_hil_mode"].(string); mode == "manual" {
		return false
	}
	return true
}

func (s *Service) ResolveDelegatedDecision(ctx context.Context, tenantID, decisionID string, actor ActorRef, input DelegatedDecisionInput) (DecisionResolution, error) {
	if s == nil || s.repository == nil || s.ids == nil || s.delegations == nil {
		return DecisionResolution{}, ErrParticipantRequired
	}
	actor, input.OnBehalfOf = normalizeActor(actor), normalizeActor(input.OnBehalfOf)
	status, effect, valid := normalizeResolution(input.Decision)
	if !valid || actor.Kind != ActorAgent || actor.ID == "" || input.OnBehalfOf.Kind != ActorHuman || input.OnBehalfOf.ID == "" ||
		strings.TrimSpace(input.Reason) == "" || strings.TrimSpace(input.IdempotencyKey) == "" {
		return DecisionResolution{}, ErrInvalidRequest
	}
	decision, err := s.repository.GetDecision(ctx, tenantID, decisionID)
	if err != nil {
		return DecisionResolution{}, err
	}
	now := s.now().UTC()
	if !DelegatedDecisionEligible(decision) || decision.TenantID != tenantID ||
		input.ReviewFingerprint != DecisionReviewFingerprint(decision) || input.ReviewedAt.IsZero() ||
		input.ReviewedAt.After(now) || now.Sub(input.ReviewedAt) > 5*time.Minute ||
		(!decision.ExpiresAt.IsZero() && !now.Before(decision.ExpiresAt)) || actorKey(decision.Requester) == actorKey(actor) {
		return DecisionResolution{}, ErrConflict
	}
	evidence, err := s.delegations.AuthorizeDelegation(ctx, decision, input.OnBehalfOf, actor)
	if err != nil {
		return DecisionResolution{}, err
	}
	if evidence.GrantID == "" || evidence.PolicyDigest == "" || evidence.TenantID != tenantID ||
		evidence.Agent != actor || evidence.OnBehalfOf != input.OnBehalfOf || !now.Before(evidence.ExpiresAt) {
		return DecisionResolution{}, ErrParticipantRequired
	}
	evidence.ReviewFingerprint = input.ReviewFingerprint
	evidence.DecisionKey = input.IdempotencyKey
	if s.workflow != nil && s.workflow.repository != nil {
		detail, workflowErr := s.workflow.repository.GetWorkflowInstanceByDecisionID(ctx, tenantID, decisionID)
		if workflowErr == nil {
			if input.TaskID == "" || detail.Instance.Version != input.ExpectedWorkflowVersion {
				return DecisionResolution{}, ErrConflict
			}
			choice := WorkflowTaskApprove
			if status == DecisionDenied {
				choice = WorkflowTaskReject
			}
			result, err := s.workflow.DecideTask(ctx, tenantID, input.TaskID, actor, DecideWorkflowTaskInput{
				Decision: choice, Reason: input.Reason, IdempotencyKey: input.IdempotencyKey,
				ExpectedInstanceVersion: input.ExpectedWorkflowVersion, delegation: &evidence,
			})
			if err != nil {
				return DecisionResolution{}, err
			}
			if result.Instance.Status == WorkflowRunning {
				return DecisionResolution{Decision: result.Decision, Actor: actor, Reason: input.Reason}, nil
			}
			return workflowDecisionResolution(result, actor, input.Reason, effect)
		}
		if !errors.Is(workflowErr, ErrNotFound) {
			return DecisionResolution{}, workflowErr
		}
	}
	assigned, assignedOK := decisionApprovalAssignee(decision)
	if input.TaskID != "" || !assignedOK || assigned != input.OnBehalfOf.ID || !DecisionResolutionActorAllowed(decision, input.OnBehalfOf) {
		return DecisionResolution{}, ErrParticipantRequired
	}
	id, err := s.ids.NewIDContext()
	if err != nil {
		return DecisionResolution{}, err
	}
	receipt := DecisionReceipt{
		ID: id, TenantID: tenantID, RequestID: decisionID, ArgsHash: decision.ArgsHash,
		Effect: effect, ResolverActorKind: actor.Kind, ResolverActorID: actor.ID, ResolverReason: input.Reason,
		ResolverSummary: map[string]any{"delegation": evidence}, CreatedAt: now, SingleUse: status == DecisionResolved,
	}
	if receipt.SingleUse {
		receipt.ExpiresAt = now.Add(15 * time.Minute)
	}
	decision.Status = status
	if err := s.repository.ResolveDecision(ctx, decision, receipt); err != nil {
		return DecisionResolution{}, err
	}
	result := DecisionResolution{Decision: decision, Receipt: receipt, Actor: actor, Reason: input.Reason}
	if s.observer != nil {
		s.observer.DecisionResolved(ctx, result)
	}
	return result, nil
}
