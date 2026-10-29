package datavisibility

import (
	"context"
	"errors"
	"testing"

	"orrery_v2/internal/app/contextgraph"
	"orrery_v2/internal/app/identity"
)

type contextPolicyFunc func(context.Context, contextgraph.BuildContextBundleRequest, []contextgraph.Object) (contextgraph.PermissionDecision, error)

func (f contextPolicyFunc) DecideContext(ctx context.Context, req contextgraph.BuildContextBundleRequest, objects []contextgraph.Object) (contextgraph.PermissionDecision, error) {
	return f(ctx, req, objects)
}

func TestContextGraphAdapterFailsClosedWhenRevisionChangesDuringEvaluation(t *testing.T) {
	policy := contextPolicyFunc(func(_ context.Context, _ contextgraph.BuildContextBundleRequest, _ []contextgraph.Object) (contextgraph.PermissionDecision, error) {
		return contextgraph.PermissionDecision{}, nil
	})
	calls := 0
	adapter := NewContextGraphPolicyAdapter(policy, revisionSourceFunc(func(context.Context, identity.Principal) (string, error) {
		calls++
		if calls == 1 {
			return "policy-1", nil
		}
		return "policy-2", nil
	}))
	_, err := adapter.Evaluate(context.Background(), AdapterRequest{
		Principal: identity.NormalizePrincipal(identity.Principal{SubjectProvider: "oidc", Subject: "user-1", UserID: "user-1", ViewerKind: identity.ViewerKindUser}),
		Purpose:   "ontology.context.read",
		Resources: []ResourceDescriptor{{
			Key: "object-1", Kind: ResourceObject, TenantID: "tenant-1", FieldPaths: []string{"label"},
			Payload: contextgraph.Object{ID: "object-1", TenantID: "tenant-1", ObjectType: "mission"},
		}},
	})
	if !errors.Is(err, ErrDependencyUnavailable) || calls != 2 {
		t.Fatalf("revision fence err=%v calls=%d", err, calls)
	}
}

type revisionSourceFunc func(context.Context, identity.Principal) (string, error)

func (f revisionSourceFunc) Revision(ctx context.Context, principal identity.Principal) (string, error) {
	return f(ctx, principal)
}

func TestPolicyServiceContextGraphAdapterUsesServerPrincipalAndMasksProperties(t *testing.T) {
	policy := contextPolicyFunc(func(_ context.Context, req contextgraph.BuildContextBundleRequest, objects []contextgraph.Object) (contextgraph.PermissionDecision, error) {
		if req.ActorRef.Kind != identity.ViewerKindAgent || req.ActorRef.ID != "agent-1" || req.TenantID != "mission:1" || len(objects) != 2 {
			t.Fatalf("context policy request = %+v objects=%+v", req, objects)
		}
		return contextgraph.PermissionDecision{
			HiddenObjectIDs: []string{"object-2"},
			MaskedFields:    map[string][]string{"object-1": {"email"}},
		}, nil
	})
	adapter := NewContextGraphPolicyAdapter(policy, revisionSourceFunc(func(context.Context, identity.Principal) (string, error) { return "field-policy-3", nil }))
	authorization := policyAdapterFunc(func(_ context.Context, req AdapterRequest) (AdapterDecision, error) {
		out := AdapterDecision{Coverage: CoverageEntity, PolicyRevision: "authorization-2", Resources: make([]AdapterResourceDecision, 0, len(req.Resources))}
		for _, resource := range req.Resources {
			out.Resources = append(out.Resources, AdapterResourceDecision{Key: resource.Key, ExistenceDisclosure: ExistenceVisible})
		}
		return out, nil
	})
	decision, err := NewService(Register(ResourceObject, authorization), Register(ResourceObject, adapter)).Evaluate(context.Background(), EvaluateRequest{
		Principal: identity.NormalizePrincipal(identity.Principal{SubjectProvider: "internal", Subject: "agent-1", AgentProfileID: "agent-1", ViewerKind: identity.ViewerKindAgent}),
		Purpose:   "ontology.context.read",
		Resources: []ResourceDescriptor{
			{Key: "object-1", Kind: ResourceObject, TenantID: "mission:1", FieldPaths: []string{"label", "properties.email"}, Payload: contextgraph.Object{ID: "object-1", TenantID: "mission:1", ObjectType: "person"}},
			{Key: "object-2", Kind: ResourceObject, TenantID: "mission:1", FieldPaths: []string{"label"}, Payload: contextgraph.Object{ID: "object-2", TenantID: "mission:1", ObjectType: "person"}},
		},
	})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	visible, _ := decision.Resource("object-1")
	if visible.Hidden || len(visible.Fields.Masked) != 1 || visible.Fields.Masked[0] != "properties.email" || len(visible.Fields.Allowed) != 1 || visible.Fields.Allowed[0] != "label" {
		t.Fatalf("visible decision = %+v", visible)
	}
	hidden, _ := decision.Resource("object-2")
	if !hidden.Hidden || hidden.ExistenceDisclosure != ExistenceConceal {
		t.Fatalf("hidden decision = %+v", hidden)
	}
}
