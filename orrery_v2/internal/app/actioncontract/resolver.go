package actioncontract

import (
	"context"
	"fmt"
	"strings"
)

type ResolveRequest struct {
	ActionTypeID ActionTypeID
	Runtime      RuntimeRef
	Category     ActionCategory
	ProviderKind ProviderKind
	Resource     ObjectRef
	Intent       string
}

type Resolver struct {
	repository Repository
}

func NewResolver(repository Repository) *Resolver {
	return &Resolver{repository: repository}
}

func (r *Resolver) Resolve(ctx context.Context, req ResolveRequest) (ActionType, error) {
	if err := ctx.Err(); err != nil {
		return ActionType{}, err
	}
	if r == nil || r.repository == nil {
		return ActionType{}, fmt.Errorf("%w: repository is required", ErrInvalidRequest)
	}
	if req.ActionTypeID != "" {
		return r.repository.GetActionType(ctx, req.ActionTypeID)
	}
	types, err := r.repository.ListActionTypes(ctx)
	if err != nil {
		return ActionType{}, err
	}
	var matches []ActionType
	for _, actionType := range types {
		if actionTypeMatchesResolveRequest(actionType, req) {
			matches = append(matches, actionType)
		}
	}
	if len(matches) != 1 {
		return ActionType{}, fmt.Errorf("%w: action type for runtime=%s resource=%s intent=%s", ErrNotFound, req.Runtime.Kind, req.Resource.Kind, req.Intent)
	}
	return cloneActionType(matches[0]), nil
}

func actionTypeMatchesResolveRequest(actionType ActionType, req ResolveRequest) bool {
	if !actionType.Enabled {
		return false
	}
	if req.ProviderKind != "" && actionType.ProviderKind != req.ProviderKind {
		return false
	}
	if req.Category != "" && actionType.Category != req.Category {
		return false
	}
	if req.Runtime.Kind == RuntimeMCPGateway && actionType.ID != "mcp.tool.call" {
		return false
	}
	if req.Runtime.Kind == RuntimeIRIS && actionType.ID != "iris.accept_recommendation" {
		return false
	}
	if req.Resource.Kind != "" && actionType.ResourceResolver != "" && normalizeActionToken(req.Resource.Kind) != normalizeActionToken(actionType.ResourceResolver) {
		return false
	}
	if strings.TrimSpace(req.Intent) != "" && !actionTypeMatchesIntent(actionType, req.Intent) {
		return false
	}
	if req.Runtime.Kind != RuntimeMCPGateway && req.Runtime.Kind != RuntimeIRIS && strings.TrimSpace(req.Intent) == "" {
		return false
	}
	return true
}

func actionTypeMatchesIntent(actionType ActionType, intent string) bool {
	if actionTypeIDMatchesIntent(actionType.ID, intent) {
		return true
	}
	normalizedIntent := normalizeSemanticText(intent)
	for _, verb := range semanticVerbs(actionType) {
		normalizedVerb := normalizeSemanticText(verb)
		if normalizedVerb != "" && (normalizedIntent == normalizedVerb || strings.Contains(normalizedIntent, normalizedVerb)) {
			return true
		}
	}
	return false
}

func actionTypeIDMatchesIntent(id ActionTypeID, intent string) bool {
	intent = normalizeActionToken(intent)
	if intent == "" {
		return false
	}
	parts := strings.Split(string(id), ".")
	if len(parts) == 0 {
		return false
	}
	return normalizeActionToken(parts[len(parts)-1]) == intent
}

func normalizeActionToken(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	value = strings.ReplaceAll(value, "-", "_")
	value = strings.ReplaceAll(value, " ", "_")
	return value
}
