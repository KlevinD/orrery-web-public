package governance

import (
	"context"
	"fmt"
	"strings"

	"orrery_v2/internal/app/contextgraph"
	"orrery_v2/internal/app/datavisibility"
	"orrery_v2/internal/app/identity"
	"orrery_v2/internal/app/ontologyscope"
)

type VisibilityCommandPolicy struct {
	visibility *datavisibility.Service
}

func NewVisibilityCommandPolicy(visibility *datavisibility.Service) *VisibilityCommandPolicy {
	return &VisibilityCommandPolicy{visibility: visibility}
}

func (p *VisibilityCommandPolicy) Authorize(ctx context.Context, principal identity.Principal, request CommandPolicyRequest) error {
	if p == nil || p.visibility == nil {
		return ontologyscope.ErrDependencyUnavailable
	}
	resources := make([]datavisibility.ResourceDescriptor, 0, len(request.Targets)+len(request.EvidenceRefs)+1)
	proposalKey := "governance-command-scope"
	proposalType := "proposal"
	if id := strings.TrimSpace(request.Proposal.ID); id != "" {
		proposalKey = "proposal:" + id
		proposalType = strings.ToLower(strings.TrimSpace(request.Proposal.TargetKind))
	}
	resources = append(resources, commandPolicyDescriptor(
		proposalKey, datavisibility.ResourceProposal, proposalType, request.ProposalScope,
	))
	seen := map[string]struct{}{}
	for _, target := range request.Targets {
		key := strings.Join([]string{
			strings.ToLower(strings.TrimSpace(target.Target.Kind)), strings.ToLower(strings.TrimSpace(target.Target.ID)),
		}, "\x00")
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		resources = append(resources, commandPolicyDescriptor(
			"definition:"+strings.ReplaceAll(key, "\x00", ":"),
			datavisibility.ResourceDefinition,
			commandPolicyDefinitionType(target.Target.Kind),
			target.Scope,
		))
	}
	for index, ref := range request.EvidenceRefs {
		binding := request.ProposalScope.Binding()
		resources = append(resources, datavisibility.ResourceDescriptor{
			Key: fmt.Sprintf("governance-command-evidence:%03d", index), Kind: datavisibility.ResourceEvidence,
			TenantID: binding.TenantID, Type: strings.ToLower(strings.TrimSpace(ref.Kind)),
			AccessRefs: []datavisibility.AccessRef{
				{Kind: binding.SourceObject.Type, ID: binding.SourceObject.ID, TenantID: binding.TenantID},
				{Kind: ref.Kind, ID: ref.ID, TenantID: binding.TenantID},
			},
			Payload: ref,
		})
	}
	decision, err := p.visibility.Evaluate(ctx, datavisibility.EvaluateRequest{
		Principal: identity.NormalizePrincipal(principal), Purpose: strings.TrimSpace(request.Operation), Resources: resources,
	})
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("%w: governance mutation policy evaluation failed", ontologyscope.ErrDependencyUnavailable)
	}
	for _, resource := range resources {
		item, ok := decision.Resource(resource.Key)
		if !ok {
			return ontologyscope.ErrDependencyUnavailable
		}
		if item.Hidden || item.ExistenceDisclosure != datavisibility.ExistenceVisible {
			return ontologyscope.ErrAccessDenied
		}
	}
	return nil
}

func commandPolicyDescriptor(key string, kind datavisibility.ResourceKind, resourceType string, scope ontologyscope.AuthorizedScopeContext) datavisibility.ResourceDescriptor {
	binding := scope.Binding()
	return datavisibility.ResourceDescriptor{
		Key: key, Kind: kind, TenantID: binding.TenantID, Type: strings.ToLower(strings.TrimSpace(resourceType)),
		AccessRefs: []datavisibility.AccessRef{{
			Kind: binding.SourceObject.Type, ID: binding.SourceObject.ID, TenantID: binding.TenantID,
		}},
		Payload: contextgraph.Object{ID: key, TenantID: binding.TenantID, ObjectType: strings.ToLower(strings.TrimSpace(resourceType))},
	}
}

func commandPolicyDefinitionType(kind string) string {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "object_type":
		return "object"
	case "link_type":
		return "link"
	case "property_type":
		return "property"
	case "mapping_rule":
		return "mapping"
	default:
		return "definition"
	}
}
