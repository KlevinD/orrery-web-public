package hil

import (
	"context"
	"fmt"
)

type WorkflowAssigneeResolver interface {
	ResolveWorkflowAssignees(context.Context, WorkflowAssigneeRequest) ([]ActorRef, error)
}

type WorkflowAssigneeRequest struct {
	TenantID          string
	ActionTypeID      string
	TemplateID        string
	Requester         ActorRef
	Resource          ObjectRef
	StageKey          string
	Selector          WorkflowActorSelector
	RecommendedActors []ActorRef
}

type workflowAssigneeResolutionOptions struct {
	FormalApproval   bool
	ExcludeRequester bool
	ExactlyOne       bool
}

func resolveWorkflowActorSelectors(ctx context.Context, resolver WorkflowAssigneeResolver, request WorkflowAssigneeRequest, selectors []WorkflowActorSelector, options workflowAssigneeResolutionOptions) ([]ActorRef, error) {
	if resolver == nil || request.TenantID == "" || len(selectors) == 0 {
		return nil, ErrInvalidRequest
	}
	requesterKey := actorKey(normalizeActor(request.Requester))
	seen := map[string]bool{}
	actors := make([]ActorRef, 0)
	for _, selector := range selectors {
		request.Selector = selector
		resolved, err := resolver.ResolveWorkflowAssignees(ctx, request)
		if err != nil {
			return nil, fmt.Errorf("%w: resolve selector %q: %v", ErrInvalidRequest, selector.Kind, err)
		}
		for _, actor := range resolved {
			actor = normalizeActor(actor)
			if actor.Kind == "" || actor.ID == "" || (options.FormalApproval && actor.Kind != ActorHuman) {
				return nil, ErrInvalidRequest
			}
			key := actorKey(actor)
			if options.ExcludeRequester && key == requesterKey {
				continue
			}
			if seen[key] {
				continue
			}
			seen[key] = true
			actors = append(actors, actor)
		}
	}
	if len(actors) == 0 || (options.ExactlyOne && len(actors) != 1) {
		return nil, ErrInvalidRequest
	}
	return actors, nil
}
