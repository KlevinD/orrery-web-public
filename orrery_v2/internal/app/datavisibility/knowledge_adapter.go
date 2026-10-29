package datavisibility

import (
	"context"
	"strings"

	"orrery_v2/internal/app/identity"
	"orrery_v2/internal/app/knowledgeadmin"
	"orrery_v2/internal/knowledge/core"
)

type KnowledgeSubjectResolver interface {
	ResolveKnowledgeSubject(context.Context, identity.Principal) (knowledgeadmin.AccessSubject, error)
}

type KnowledgeResourceResolver interface {
	ResolveKnowledgeBase(context.Context, AccessRef) (core.KnowledgeBase, bool, error)
}

type KnowledgePolicyAdapter struct {
	policy    *knowledgeadmin.AccessPolicy
	subjects  KnowledgeSubjectResolver
	resources KnowledgeResourceResolver
	revisions PolicyRevisionSource
}

func NewKnowledgePolicyAdapter(policy *knowledgeadmin.AccessPolicy, subjects KnowledgeSubjectResolver, resources KnowledgeResourceResolver, revisions PolicyRevisionSource) *KnowledgePolicyAdapter {
	return &KnowledgePolicyAdapter{policy: policy, subjects: subjects, resources: resources, revisions: revisions}
}

func (a *KnowledgePolicyAdapter) Evaluate(ctx context.Context, req AdapterRequest) (AdapterDecision, error) {
	if a == nil {
		return AdapterDecision{}, ErrDependencyUnavailable
	}
	applicable := false
	for _, resource := range req.Resources {
		for _, ref := range resource.AccessRefs {
			applicable = applicable || isKnowledgeAccessRef(ref)
		}
	}
	if !applicable {
		out := AdapterDecision{Coverage: CoverageFilter, Resources: make([]AdapterResourceDecision, 0, len(req.Resources))}
		for _, resource := range req.Resources {
			out.Resources = append(out.Resources, AdapterResourceDecision{Key: resource.Key, Skip: true})
		}
		return out, nil
	}
	if a.policy == nil || a.subjects == nil || a.resources == nil {
		return AdapterDecision{}, ErrDependencyUnavailable
	}
	revision, err := beginPolicyRevision(ctx, a.revisions, req.Principal)
	if err != nil {
		return AdapterDecision{}, err
	}
	subject, err := a.subjects.ResolveKnowledgeSubject(ctx, identity.NormalizePrincipal(req.Principal))
	if err != nil {
		return AdapterDecision{}, err
	}
	out := AdapterDecision{Coverage: CoverageFilter, Resources: make([]AdapterResourceDecision, 0, len(req.Resources))}
	for _, resource := range req.Resources {
		hidden := false
		matched := false
		for _, ref := range resource.AccessRefs {
			if !isKnowledgeAccessRef(ref) {
				continue
			}
			matched = true
			base, found, err := a.resources.ResolveKnowledgeBase(ctx, ref)
			if err != nil {
				return AdapterDecision{}, err
			}
			if !found || !a.policy.CanRead(ctx, subject, base) {
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

func isKnowledgeAccessRef(ref AccessRef) bool {
	switch strings.ToLower(strings.TrimSpace(ref.Kind)) {
	case "knowledge_base", "knowledge_document", "knowledge_chunk", "knowledge_page", "knowledge_anchor":
		return true
	default:
		return false
	}
}
