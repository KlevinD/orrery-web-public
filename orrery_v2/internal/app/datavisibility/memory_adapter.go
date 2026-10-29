package datavisibility

import (
	"context"
	"strings"

	"orrery_v2/internal/app/identity"
	"orrery_v2/internal/app/memoryadmin"
	memcore "orrery_v2/internal/memory/core"
)

type MemoryViewerResolver interface {
	ResolveMemoryViewer(context.Context, identity.Principal) (memoryadmin.ViewerRef, error)
}

type MemoryResourceResolver interface {
	ResolveMemoryRecord(context.Context, AccessRef) (memcore.MemoryRecord, bool, error)
}

type MemoryPolicyAdapter struct {
	viewers   MemoryViewerResolver
	resources MemoryResourceResolver
	revisions PolicyRevisionSource
}

func NewMemoryPolicyAdapter(viewers MemoryViewerResolver, resources MemoryResourceResolver, revisions PolicyRevisionSource) *MemoryPolicyAdapter {
	return &MemoryPolicyAdapter{viewers: viewers, resources: resources, revisions: revisions}
}

func (a *MemoryPolicyAdapter) Evaluate(ctx context.Context, req AdapterRequest) (AdapterDecision, error) {
	if a == nil {
		return AdapterDecision{}, ErrDependencyUnavailable
	}
	applicable := false
	for _, resource := range req.Resources {
		for _, ref := range resource.AccessRefs {
			applicable = applicable || strings.ToLower(strings.TrimSpace(ref.Kind)) == "memory_record"
		}
	}
	if !applicable {
		out := AdapterDecision{Coverage: CoverageFilter, Resources: make([]AdapterResourceDecision, 0, len(req.Resources))}
		for _, resource := range req.Resources {
			out.Resources = append(out.Resources, AdapterResourceDecision{Key: resource.Key, Skip: true})
		}
		return out, nil
	}
	if a.viewers == nil || a.resources == nil {
		return AdapterDecision{}, ErrDependencyUnavailable
	}
	revision, err := beginPolicyRevision(ctx, a.revisions, req.Principal)
	if err != nil {
		return AdapterDecision{}, err
	}
	viewer, err := a.viewers.ResolveMemoryViewer(ctx, identity.NormalizePrincipal(req.Principal))
	if err != nil {
		return AdapterDecision{}, err
	}
	out := AdapterDecision{Coverage: CoverageFilter, Resources: make([]AdapterResourceDecision, 0, len(req.Resources))}
	for _, resource := range req.Resources {
		hidden := false
		matched := false
		for _, ref := range resource.AccessRefs {
			if strings.ToLower(strings.TrimSpace(ref.Kind)) != "memory_record" {
				continue
			}
			matched = true
			record, found, err := a.resources.ResolveMemoryRecord(ctx, ref)
			if err != nil {
				return AdapterDecision{}, err
			}
			if !found || !memoryadmin.CanReadRecord(viewer, record) {
				hidden = true
			}
		}
		item := AdapterResourceDecision{Key: resource.Key}
		if !matched {
			item.Skip = true
			out.Resources = append(out.Resources, item)
			continue
		}
		item.Hidden = hidden
		item.ExistenceDisclosure = ExistenceVisible
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
