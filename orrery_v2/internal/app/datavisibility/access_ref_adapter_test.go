package datavisibility

import (
	"context"
	"errors"
	"testing"

	"orrery_v2/internal/app/authorization"
	"orrery_v2/internal/app/identity"
)

type accessResolverFunc func(context.Context, AccessRef) (AccessAuthorizationTarget, bool, error)

func (f accessResolverFunc) ResolveAccessRef(ctx context.Context, ref AccessRef) (AccessAuthorizationTarget, bool, error) {
	return f(ctx, ref)
}

type batchAuthorizerFunc func(context.Context, []authorization.CheckRequest) ([]authorization.Decision, error)

func (f batchAuthorizerFunc) BatchCan(ctx context.Context, checks []authorization.CheckRequest) ([]authorization.Decision, error) {
	return f(ctx, checks)
}

func TestPolicyServiceAccessRefAdapterBatchesChecksAndHidesIncompleteRefs(t *testing.T) {
	resolver := accessResolverFunc(func(_ context.Context, ref AccessRef) (AccessAuthorizationTarget, bool, error) {
		return AccessAuthorizationTarget{Action: ref.Kind + ".view", Object: identity.ObjectRef{Type: ref.Kind, ID: ref.ID}, TenantID: ref.TenantID}, true, nil
	})
	batchCalls := 0
	authorizer := batchAuthorizerFunc(func(_ context.Context, checks []authorization.CheckRequest) ([]authorization.Decision, error) {
		batchCalls++
		if len(checks) != 2 {
			t.Fatalf("checks = %+v", checks)
		}
		return []authorization.Decision{{Allowed: true, Source: "test"}, {Allowed: false, Source: "test"}}, nil
	})
	adapter := NewAccessRefPolicyAdapter(resolver, authorizer, nil)
	fields := NewFieldPolicyAdapter(FieldClassifierFunc(func(context.Context, identity.Principal, string, ResourceDescriptor, string) (FieldEffect, error) {
		return FieldAllow, nil
	}), nil)
	decision, err := NewService(Register(ResourceFact, adapter), Register(ResourceFact, fields)).Evaluate(context.Background(), EvaluateRequest{
		Principal: policyTestPrincipal(), Purpose: "ontology.context.read",
		Resources: []ResourceDescriptor{
			{Key: "fact-1", Kind: ResourceFact, TenantID: "mission:1", FieldPaths: []string{"value"}, AccessRefs: []AccessRef{{Kind: "mission", ID: "1", TenantID: "mission:1"}}},
			{Key: "fact-2", Kind: ResourceFact, TenantID: "mission:1", FieldPaths: []string{"value"}, AccessRefs: []AccessRef{{Kind: "project", ID: "2", TenantID: "project:2"}}},
			{Key: "fact-3", Kind: ResourceFact, TenantID: "mission:1", FieldPaths: []string{"value"}},
		},
	})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if batchCalls != 1 {
		t.Fatalf("batch calls = %d", batchCalls)
	}
	first, _ := decision.Resource("fact-1")
	second, _ := decision.Resource("fact-2")
	third, _ := decision.Resource("fact-3")
	if first.Hidden || !second.Hidden || !third.Hidden || decision.Cacheable() {
		t.Fatalf("decisions = %+v", decision)
	}
}

func TestPolicyServiceAccessRefAdapterRejectsIncompleteAllowedDecision(t *testing.T) {
	resolver := accessResolverFunc(func(_ context.Context, ref AccessRef) (AccessAuthorizationTarget, bool, error) {
		return AccessAuthorizationTarget{Action: "mission.view", Object: identity.ObjectRef{Type: "mission", ID: ref.ID}, TenantID: ref.TenantID}, true, nil
	})
	authorizer := batchAuthorizerFunc(func(context.Context, []authorization.CheckRequest) ([]authorization.Decision, error) {
		return []authorization.Decision{{Allowed: true}}, nil
	})
	_, err := NewService(Register(ResourceObject, NewAccessRefPolicyAdapter(resolver, authorizer, nil))).Evaluate(context.Background(), EvaluateRequest{
		Principal: policyTestPrincipal(), Purpose: "read",
		Resources: []ResourceDescriptor{{Key: "object-1", Kind: ResourceObject, TenantID: "mission:1", AccessRefs: []AccessRef{{Kind: "mission", ID: "1", TenantID: "mission:1"}}}},
	})
	if !errors.Is(err, ErrIncompleteDecision) {
		t.Fatalf("incomplete authorization error = %v", err)
	}
}
