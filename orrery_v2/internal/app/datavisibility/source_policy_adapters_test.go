package datavisibility

import (
	"context"
	"testing"

	"orrery_v2/internal/app/identity"
	"orrery_v2/internal/app/knowledgeadmin"
	"orrery_v2/internal/app/memoryadmin"
	"orrery_v2/internal/knowledge/core"
	knowledgetestkit "orrery_v2/internal/knowledge/testkit"
	memcore "orrery_v2/internal/memory/core"
)

type knowledgeSubjectResolverFunc func(context.Context, identity.Principal) (knowledgeadmin.AccessSubject, error)

func (f knowledgeSubjectResolverFunc) ResolveKnowledgeSubject(ctx context.Context, principal identity.Principal) (knowledgeadmin.AccessSubject, error) {
	return f(ctx, principal)
}

type knowledgeResourceResolverFunc func(context.Context, AccessRef) (core.KnowledgeBase, bool, error)

func (f knowledgeResourceResolverFunc) ResolveKnowledgeBase(ctx context.Context, ref AccessRef) (core.KnowledgeBase, bool, error) {
	return f(ctx, ref)
}

func TestPolicyServiceKnowledgeAdapterUsesExistingKnowledgeAccessPolicy(t *testing.T) {
	public := core.KnowledgeBase{ID: "kb-public", OwnerType: core.OwnerGlobal, Visibility: core.VisibilityPublic, Status: core.KnowledgeActive}
	private := core.KnowledgeBase{ID: "kb-private", OwnerType: core.OwnerAgent, OwnerID: "other", Visibility: core.VisibilityPrivate, Status: core.KnowledgeActive}
	resources := knowledgeResourceResolverFunc(func(_ context.Context, ref AccessRef) (core.KnowledgeBase, bool, error) {
		switch ref.ID {
		case "doc-public":
			return public, true, nil
		case "doc-private":
			return private, true, nil
		default:
			return core.KnowledgeBase{}, false, nil
		}
	})
	subjects := knowledgeSubjectResolverFunc(func(context.Context, identity.Principal) (knowledgeadmin.AccessSubject, error) {
		return knowledgeadmin.AccessSubject{ViewerID: "user-1"}, nil
	})
	adapter := NewKnowledgePolicyAdapter(knowledgeadmin.NewAccessPolicy(knowledgetestkit.NewFakeRepository()), subjects, resources, nil)
	fields := NewFieldPolicyAdapter(FieldClassifierFunc(func(context.Context, identity.Principal, string, ResourceDescriptor, string) (FieldEffect, error) {
		return FieldAllow, nil
	}), nil)
	decision, err := NewService(Register(ResourceEvidence, allowEntityAdapter()), Register(ResourceEvidence, adapter), Register(ResourceEvidence, fields)).Evaluate(context.Background(), EvaluateRequest{
		Principal: policyTestPrincipal(), Purpose: "ontology.lineage.read",
		Resources: []ResourceDescriptor{
			{Key: "public", Kind: ResourceEvidence, TenantID: "system", FieldPaths: []string{"summary"}, AccessRefs: []AccessRef{{Kind: "knowledge_document", ID: "doc-public", TenantID: "system"}}},
			{Key: "private", Kind: ResourceEvidence, TenantID: "system", FieldPaths: []string{"summary"}, AccessRefs: []AccessRef{{Kind: "knowledge_document", ID: "doc-private", TenantID: "system"}}},
		},
	})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	visible, _ := decision.Resource("public")
	hidden, _ := decision.Resource("private")
	if visible.Hidden || !hidden.Hidden {
		t.Fatalf("knowledge decisions = %+v", decision.Resources)
	}
}

type memoryViewerResolverFunc func(context.Context, identity.Principal) (memoryadmin.ViewerRef, error)

func (f memoryViewerResolverFunc) ResolveMemoryViewer(ctx context.Context, principal identity.Principal) (memoryadmin.ViewerRef, error) {
	return f(ctx, principal)
}

type memoryResourceResolverFunc func(context.Context, AccessRef) (memcore.MemoryRecord, bool, error)

func (f memoryResourceResolverFunc) ResolveMemoryRecord(ctx context.Context, ref AccessRef) (memcore.MemoryRecord, bool, error) {
	return f(ctx, ref)
}

func TestPolicyServiceMemoryAdapterUsesExistingMemoryVisibilityPolicy(t *testing.T) {
	resources := memoryResourceResolverFunc(func(_ context.Context, ref AccessRef) (memcore.MemoryRecord, bool, error) {
		switch ref.ID {
		case "memory-visible":
			return memcore.MemoryRecord{ID: ref.ID, Scope: memcore.MemoryScope{Kind: memcore.ScopeMission, ID: "mission-1"}, Visibility: memcore.Visibility{Mode: memcore.VisibilityScopedMembers, ScopeKind: memcore.ScopeMission, ScopeID: "mission-1"}}, true, nil
		case "memory-private":
			return memcore.MemoryRecord{ID: ref.ID, Scope: memcore.MemoryScope{Kind: memcore.ScopeAgent, ID: "other"}, Visibility: memcore.Visibility{Mode: memcore.VisibilityOwnerOnly}}, true, nil
		default:
			return memcore.MemoryRecord{}, false, nil
		}
	})
	viewers := memoryViewerResolverFunc(func(context.Context, identity.Principal) (memoryadmin.ViewerRef, error) {
		return memoryadmin.ViewerRef{ViewerID: "user-1", MissionIDs: []string{"mission-1"}, PermissionScope: []string{memoryadmin.PermissionMemoryRead}}, nil
	})
	adapter := NewMemoryPolicyAdapter(viewers, resources, nil)
	fields := NewFieldPolicyAdapter(FieldClassifierFunc(func(context.Context, identity.Principal, string, ResourceDescriptor, string) (FieldEffect, error) {
		return FieldAllow, nil
	}), nil)
	decision, err := NewService(Register(ResourceObject, allowEntityAdapter()), Register(ResourceObject, adapter), Register(ResourceObject, fields)).Evaluate(context.Background(), EvaluateRequest{
		Principal: policyTestPrincipal(), Purpose: "ontology.context.read",
		Resources: []ResourceDescriptor{
			{Key: "visible", Kind: ResourceObject, TenantID: "mission:mission-1", FieldPaths: []string{"properties.summary"}, AccessRefs: []AccessRef{{Kind: "memory_record", ID: "memory-visible", TenantID: "mission:mission-1"}}},
			{Key: "private", Kind: ResourceObject, TenantID: "mission:mission-1", FieldPaths: []string{"properties.summary"}, AccessRefs: []AccessRef{{Kind: "memory_record", ID: "memory-private", TenantID: "mission:mission-1"}}},
		},
	})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	visible, _ := decision.Resource("visible")
	hidden, _ := decision.Resource("private")
	if visible.Hidden || !hidden.Hidden {
		t.Fatalf("memory decisions = %+v", decision.Resources)
	}
}

func TestPolicyServiceSourceAdaptersSkipUnrelatedResources(t *testing.T) {
	fields := NewFieldPolicyAdapter(FieldClassifierFunc(func(context.Context, identity.Principal, string, ResourceDescriptor, string) (FieldEffect, error) {
		return FieldAllow, nil
	}), nil)
	decision, err := NewService(
		Register(ResourceObject, allowEntityAdapter()),
		Register(ResourceObject, NewKnowledgePolicyAdapter(nil, nil, nil, nil)),
		Register(ResourceObject, NewMemoryPolicyAdapter(nil, nil, nil)),
		Register(ResourceObject, fields),
	).Evaluate(context.Background(), EvaluateRequest{
		Principal: policyTestPrincipal(), Purpose: "read",
		Resources: []ResourceDescriptor{{Key: "mission-1", Kind: ResourceObject, TenantID: "mission:1", FieldPaths: []string{"label"}, AccessRefs: []AccessRef{{Kind: "mission", ID: "1", TenantID: "mission:1"}}}},
	})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	item, _ := decision.Resource("mission-1")
	if item.Hidden {
		t.Fatalf("unrelated source adapter hid resource: %+v", item)
	}
}

func allowEntityAdapter() Adapter {
	return policyAdapterFunc(func(_ context.Context, req AdapterRequest) (AdapterDecision, error) {
		out := AdapterDecision{Coverage: CoverageEntity, Resources: make([]AdapterResourceDecision, 0, len(req.Resources))}
		for _, resource := range req.Resources {
			out.Resources = append(out.Resources, AdapterResourceDecision{Key: resource.Key, ExistenceDisclosure: ExistenceVisible})
		}
		return out, nil
	})
}
