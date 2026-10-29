package datavisibility

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"orrery_v2/internal/app/contextgraph"
	"orrery_v2/internal/app/identity"
)

type PolicyRevisionSource interface {
	Revision(context.Context, identity.Principal) (string, error)
}

func beginPolicyRevision(ctx context.Context, source PolicyRevisionSource, principal identity.Principal) (string, error) {
	if source == nil {
		return "", nil
	}
	revision, err := source.Revision(ctx, identity.NormalizePrincipal(principal))
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(revision), nil
}

func finishPolicyRevision(ctx context.Context, source PolicyRevisionSource, principal identity.Principal, before string) (string, error) {
	if source == nil {
		return "", nil
	}
	after, err := source.Revision(ctx, identity.NormalizePrincipal(principal))
	if err != nil {
		return "", err
	}
	after = strings.TrimSpace(after)
	if after != strings.TrimSpace(before) {
		return "", fmt.Errorf("%w: policy revision changed during evaluation", ErrDependencyUnavailable)
	}
	return after, nil
}

type ContextGraphPolicyAdapter struct {
	policy    contextgraph.PolicyProvider
	revisions PolicyRevisionSource
}

func NewContextGraphPolicyAdapter(policy contextgraph.PolicyProvider, revisions PolicyRevisionSource) *ContextGraphPolicyAdapter {
	return &ContextGraphPolicyAdapter{policy: policy, revisions: revisions}
}

func (a *ContextGraphPolicyAdapter) Evaluate(ctx context.Context, req AdapterRequest) (AdapterDecision, error) {
	if a == nil || a.policy == nil {
		return AdapterDecision{}, ErrDependencyUnavailable
	}
	revision, err := beginPolicyRevision(ctx, a.revisions, req.Principal)
	if err != nil {
		return AdapterDecision{}, err
	}
	actor := principalActorRef(req.Principal)
	if actor.ID == "" {
		return AdapterDecision{}, fmt.Errorf("%w: authenticated principal actor is required", ErrInvalidRequest)
	}
	type tenantBatch struct {
		objects   []contextgraph.Object
		resources []ResourceDescriptor
	}
	byTenant := map[string]*tenantBatch{}
	for _, resource := range req.Resources {
		if resource.Kind != ResourceObject {
			return AdapterDecision{}, fmt.Errorf("%w: context graph policy only supports object resources", ErrInvalidRequest)
		}
		object, ok := resource.Payload.(contextgraph.Object)
		if !ok || strings.TrimSpace(object.ID) == "" || strings.TrimSpace(object.TenantID) != resource.TenantID {
			return AdapterDecision{}, fmt.Errorf("%w: context graph object payload does not match descriptor %s", ErrInvalidRequest, resource.Key)
		}
		batch := byTenant[resource.TenantID]
		if batch == nil {
			batch = &tenantBatch{}
			byTenant[resource.TenantID] = batch
		}
		batch.objects = append(batch.objects, object)
		batch.resources = append(batch.resources, resource)
	}

	tenants := make([]string, 0, len(byTenant))
	for tenantID := range byTenant {
		tenants = append(tenants, tenantID)
	}
	sort.Strings(tenants)
	out := AdapterDecision{Coverage: CoverageFilter | CoverageFields, Resources: make([]AdapterResourceDecision, 0, len(req.Resources))}
	decisionsByKey := map[string]AdapterResourceDecision{}
	for _, tenantID := range tenants {
		batch := byTenant[tenantID]
		subjectRefs := make([]contextgraph.Ref, 0, len(batch.objects))
		for _, object := range batch.objects {
			subjectRefs = append(subjectRefs, contextgraph.Ref{Kind: object.ObjectType, ID: object.ID})
		}
		decision, err := a.policy.DecideContext(ctx, contextgraph.BuildContextBundleRequest{
			TenantID:    tenantID,
			Consumer:    "ontology_workbench",
			Purpose:     req.Purpose,
			ActorRef:    actor,
			SubjectRefs: subjectRefs,
		}, append([]contextgraph.Object(nil), batch.objects...))
		if err != nil {
			return AdapterDecision{}, err
		}
		hidden := sliceSet(decision.HiddenObjectIDs)
		for _, resource := range batch.resources {
			object := resource.Payload.(contextgraph.Object)
			_, isHidden := hidden[object.ID]
			item := AdapterResourceDecision{Key: resource.Key, Hidden: isHidden, ExistenceDisclosure: ExistenceVisible}
			if isHidden {
				item.ExistenceDisclosure = ExistenceConceal
			} else {
				masked := legacyObjectMaskedFields(resource.FieldPaths, decision.MaskedFields[object.ID])
				item.MaskedFieldPaths = masked
				item.AllowedFieldPaths = subtractPaths(resource.FieldPaths, masked)
			}
			decisionsByKey[resource.Key] = item
		}
	}
	for _, resource := range req.Resources {
		out.Resources = append(out.Resources, decisionsByKey[resource.Key])
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

func principalActorRef(principal identity.Principal) contextgraph.Ref {
	principal = identity.NormalizePrincipal(principal)
	kind := principal.ViewerKind
	id := principal.UserID
	if kind == identity.ViewerKindAgent && principal.AgentProfileID != "" {
		id = principal.AgentProfileID
	}
	if id == "" {
		id = principal.Subject
	}
	return contextgraph.Ref{Kind: kind, ID: id}
}

func legacyObjectMaskedFields(requested, legacy []string) []string {
	legacySet := sliceSet(normalizePaths(legacy))
	masked := make([]string, 0)
	for _, field := range requested {
		property := strings.TrimPrefix(field, "properties.")
		_, exact := legacySet[field]
		_, propertyMatch := legacySet[property]
		if exact || propertyMatch {
			masked = append(masked, field)
		}
	}
	return normalizePaths(masked)
}

func subtractPaths(all, remove []string) []string {
	removed := sliceSet(remove)
	out := make([]string, 0, len(all))
	for _, field := range all {
		if _, ok := removed[field]; !ok {
			out = append(out, field)
		}
	}
	return normalizePaths(out)
}
