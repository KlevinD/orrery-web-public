package datavisibility

import (
	"context"
	"fmt"
	"strings"

	"orrery_v2/internal/app/authorization"
	"orrery_v2/internal/app/identity"
)

type AccessAuthorizationTarget struct {
	Action   string
	Object   identity.ObjectRef
	TenantID string
}

type AccessRefResolver interface {
	ResolveAccessRef(context.Context, AccessRef) (AccessAuthorizationTarget, bool, error)
}

type BatchAuthorizer interface {
	BatchCan(context.Context, []authorization.CheckRequest) ([]authorization.Decision, error)
}

type AccessRefPolicyAdapter struct {
	resolver   AccessRefResolver
	authorizer BatchAuthorizer
	revisions  PolicyRevisionSource
}

func NewAccessRefPolicyAdapter(resolver AccessRefResolver, authorizer BatchAuthorizer, revisions PolicyRevisionSource) *AccessRefPolicyAdapter {
	return &AccessRefPolicyAdapter{resolver: resolver, authorizer: authorizer, revisions: revisions}
}

func (a *AccessRefPolicyAdapter) Evaluate(ctx context.Context, req AdapterRequest) (AdapterDecision, error) {
	if a == nil || a.resolver == nil || a.authorizer == nil {
		return AdapterDecision{}, ErrDependencyUnavailable
	}
	revision, err := beginPolicyRevision(ctx, a.revisions, req.Principal)
	if err != nil {
		return AdapterDecision{}, err
	}
	type resourceState struct {
		resource ResourceDescriptor
		hidden   bool
		checks   []int
	}
	states := make([]resourceState, len(req.Resources))
	checks := make([]authorization.CheckRequest, 0)
	for resourceIndex, resource := range req.Resources {
		states[resourceIndex].resource = resource
		if len(resource.AccessRefs) == 0 {
			states[resourceIndex].hidden = true
			continue
		}
		for _, ref := range resource.AccessRefs {
			if !ref.Resolved() {
				states[resourceIndex].hidden = true
				continue
			}
			target, found, err := a.resolver.ResolveAccessRef(ctx, ref)
			if err != nil {
				return AdapterDecision{}, err
			}
			target.Action = strings.ToLower(strings.TrimSpace(target.Action))
			target.Object.Type = strings.ToLower(strings.TrimSpace(target.Object.Type))
			target.Object.ID = strings.TrimSpace(target.Object.ID)
			target.TenantID = strings.TrimSpace(target.TenantID)
			if !found || target.Action == "" || target.Object.Type == "" || target.Object.ID == "" || target.TenantID == "" {
				states[resourceIndex].hidden = true
				continue
			}
			target.Object.TenantID = target.TenantID
			states[resourceIndex].checks = append(states[resourceIndex].checks, len(checks))
			checks = append(checks, authorization.CheckRequest{
				Principal: req.Principal,
				Action:    target.Action,
				Object:    target.Object,
				TenantID:  target.TenantID,
			})
		}
	}
	decisions := make([]authorization.Decision, 0)
	if len(checks) > 0 {
		var err error
		decisions, err = a.authorizer.BatchCan(ctx, checks)
		if err != nil {
			return AdapterDecision{}, err
		}
		if len(decisions) != len(checks) {
			return AdapterDecision{}, fmt.Errorf("%w: authorization returned %d decisions for %d checks", ErrIncompleteDecision, len(decisions), len(checks))
		}
		for _, decision := range decisions {
			if decision.Allowed && strings.TrimSpace(decision.Source) == "" {
				return AdapterDecision{}, fmt.Errorf("%w: allowed authorization decision is missing its source", ErrIncompleteDecision)
			}
		}
	}
	out := AdapterDecision{Coverage: CoverageEntity, Resources: make([]AdapterResourceDecision, 0, len(states))}
	for _, state := range states {
		hidden := state.hidden
		for _, checkIndex := range state.checks {
			if !decisions[checkIndex].Allowed {
				hidden = true
			}
		}
		item := AdapterResourceDecision{Key: state.resource.Key, Hidden: hidden, ExistenceDisclosure: ExistenceVisible}
		if hidden {
			item.ExistenceDisclosure = ExistenceConceal
		}
		out.Resources = append(out.Resources, item)
	}
	if a.revisions != nil {
		revision, err = finishPolicyRevision(ctx, a.revisions, req.Principal, revision)
		if err != nil {
			return AdapterDecision{}, err
		}
		out.PolicyRevision = revision
	}
	return out, nil
}
