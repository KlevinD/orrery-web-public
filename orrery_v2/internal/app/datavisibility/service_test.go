package datavisibility

import (
	"context"
	"errors"
	"strings"
	"testing"

	"orrery_v2/internal/app/identity"
)

type policyAdapterFunc func(context.Context, AdapterRequest) (AdapterDecision, error)

func (f policyAdapterFunc) Evaluate(ctx context.Context, req AdapterRequest) (AdapterDecision, error) {
	return f(ctx, req)
}

func TestPolicyServiceBatchesAndCombinesVisibilityDecisions(t *testing.T) {
	firstCalls := 0
	first := policyAdapterFunc(func(_ context.Context, req AdapterRequest) (AdapterDecision, error) {
		firstCalls++
		if len(req.Resources) != 2 || req.Principal.UserID != "user-1" {
			t.Fatalf("adapter request = %+v", req)
		}
		return AdapterDecision{Coverage: CoverageAll, PolicyRevision: "policy-1", Resources: []AdapterResourceDecision{
			{Key: "object-1", AllowedFieldPaths: []string{"label"}, MaskedFieldPaths: []string{"properties.secret"}, ExistenceDisclosure: ExistenceVisible},
			{Key: "object-2", Hidden: true, ExistenceDisclosure: ExistenceConceal},
		}}, nil
	})
	secondCalls := 0
	second := policyAdapterFunc(func(_ context.Context, req AdapterRequest) (AdapterDecision, error) {
		secondCalls++
		return AdapterDecision{Coverage: CoverageAll, PolicyRevision: "source-4", Resources: []AdapterResourceDecision{
			{Key: "object-1", AllowedFieldPaths: []string{"label", "properties.secret"}, ExistenceDisclosure: ExistenceRestricted},
			{Key: "object-2", AllowedFieldPaths: []string{"label"}, MaskedFieldPaths: []string{"properties.secret"}, ExistenceDisclosure: ExistenceVisible},
		}}, nil
	})
	service := NewService(Register(ResourceObject, first), Register(ResourceObject, second))
	decision, err := service.Evaluate(context.Background(), EvaluateRequest{
		Principal: policyTestPrincipal(),
		Purpose:   "ontology.context.read",
		Resources: []ResourceDescriptor{
			{Key: "object-1", Kind: ResourceObject, TenantID: "mission:1", FieldPaths: []string{"label", "properties.secret"}},
			{Key: "object-2", Kind: ResourceObject, TenantID: "mission:1", FieldPaths: []string{"label", "properties.secret"}},
		},
	})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if firstCalls != 1 || secondCalls != 1 {
		t.Fatalf("adapter calls = %d/%d, want one batch each", firstCalls, secondCalls)
	}
	if !decision.Cacheable() || decision.PolicyRevision == "" {
		t.Fatalf("policy revision = %q", decision.PolicyRevision)
	}
	object1, ok := decision.Resource("object-1")
	if !ok || object1.Hidden || object1.ExistenceDisclosure != ExistenceRestricted || len(object1.Fields.Allowed) != 1 || object1.Fields.Allowed[0] != "label" || len(object1.Fields.Masked) != 1 || object1.Fields.Masked[0] != "properties.secret" {
		t.Fatalf("object-1 decision = %+v", object1)
	}
	object2, ok := decision.Resource("object-2")
	if !ok || !object2.Hidden || object2.ExistenceDisclosure != ExistenceConceal || len(object2.Fields.Allowed) != 0 || len(object2.Fields.Masked) != 0 {
		t.Fatalf("object-2 decision = %+v", object2)
	}
	if got := decision.HiddenResourceKeys(); len(got) != 1 || got[0] != "object-2" {
		t.Fatalf("hidden keys = %+v", got)
	}
}

func TestPolicyServiceFailsClosedForMissingOrIncompleteAdapter(t *testing.T) {
	req := EvaluateRequest{
		Principal: policyTestPrincipal(), Purpose: "ontology.context.read",
		Resources: []ResourceDescriptor{{Key: "fact-1", Kind: ResourceFact, TenantID: "mission:1", FieldPaths: []string{"value"}}},
	}
	if _, err := NewService().Evaluate(context.Background(), req); !errors.Is(err, ErrDependencyUnavailable) {
		t.Fatalf("missing adapter error = %v", err)
	}
	incomplete := policyAdapterFunc(func(context.Context, AdapterRequest) (AdapterDecision, error) {
		return AdapterDecision{Coverage: CoverageAll, Resources: []AdapterResourceDecision{{Key: "fact-1", ExistenceDisclosure: ExistenceVisible}}}, nil
	})
	if _, err := NewService(Register(ResourceFact, incomplete)).Evaluate(context.Background(), req); !errors.Is(err, ErrIncompleteDecision) {
		t.Fatalf("incomplete fields error = %v", err)
	}
	missingRow := policyAdapterFunc(func(context.Context, AdapterRequest) (AdapterDecision, error) {
		return AdapterDecision{Coverage: CoverageAll}, nil
	})
	if _, err := NewService(Register(ResourceFact, missingRow)).Evaluate(context.Background(), req); !errors.Is(err, ErrIncompleteDecision) {
		t.Fatalf("missing row error = %v", err)
	}
}

func TestPolicyServiceAllowsExplicitAdapterAbstention(t *testing.T) {
	skipped := policyAdapterFunc(func(_ context.Context, req AdapterRequest) (AdapterDecision, error) {
		return AdapterDecision{Coverage: CoverageEntity, Resources: []AdapterResourceDecision{{Key: req.Resources[0].Key, Skip: true}}}, nil
	})
	covering := policyAdapterFunc(func(_ context.Context, req AdapterRequest) (AdapterDecision, error) {
		return AdapterDecision{Coverage: CoverageAll, PolicyRevision: "covering-1", Resources: []AdapterResourceDecision{{
			Key: req.Resources[0].Key, AllowedFieldPaths: req.Resources[0].FieldPaths, ExistenceDisclosure: ExistenceVisible,
		}}}, nil
	})
	decision, err := NewService(Register(ResourceObject, skipped), Register(ResourceObject, covering)).Evaluate(context.Background(), EvaluateRequest{
		Principal: policyTestPrincipal(), Purpose: "read",
		Resources: []ResourceDescriptor{{Key: "object-1", Kind: ResourceObject, TenantID: "tenant-1", FieldPaths: []string{"label"}}},
	})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if !decision.Cacheable() {
		t.Fatalf("skipped adapter incorrectly disabled revision: %+v", decision)
	}
}

func TestPolicyServiceReportsFilterableFieldsOnlyFromFilterCoverage(t *testing.T) {
	entity := policyAdapterFunc(func(_ context.Context, req AdapterRequest) (AdapterDecision, error) {
		return AdapterDecision{Coverage: CoverageEntity, Resources: []AdapterResourceDecision{{
			Key: req.Resources[0].Key, ExistenceDisclosure: ExistenceVisible,
		}}}, nil
	})
	fields := policyAdapterFunc(func(_ context.Context, req AdapterRequest) (AdapterDecision, error) {
		return AdapterDecision{Coverage: CoverageFilter | CoverageFields, Resources: []AdapterResourceDecision{{
			Key: req.Resources[0].Key, AllowedFieldPaths: []string{"object_type"}, MaskedFieldPaths: []string{"status"}, ExistenceDisclosure: ExistenceVisible,
		}}}, nil
	})
	request := EvaluateRequest{
		Principal: policyTestPrincipal(), Purpose: "read",
		Resources: []ResourceDescriptor{{Key: "scope", Kind: ResourceObject, TenantID: "tenant-1", FieldPaths: []string{"object_type", "status"}}},
	}
	decision, err := NewService(Register(ResourceObject, entity), Register(ResourceObject, fields)).Evaluate(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	resource, ok := decision.Resource("scope")
	if !ok || len(resource.FilterableFields) != 1 || resource.FilterableFields[0] != "object_type" {
		t.Fatalf("filterable decision = %+v", resource)
	}
	maskingFields := policyAdapterFunc(func(_ context.Context, req AdapterRequest) (AdapterDecision, error) {
		return AdapterDecision{Coverage: CoverageFields, Resources: []AdapterResourceDecision{{
			Key: req.Resources[0].Key, AllowedFieldPaths: []string{"status"}, MaskedFieldPaths: []string{"object_type"},
		}}}, nil
	})
	decision, err = NewService(Register(ResourceObject, entity), Register(ResourceObject, fields), Register(ResourceObject, maskingFields)).Evaluate(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	resource, _ = decision.Resource("scope")
	if len(resource.FilterableFields) != 0 {
		t.Fatalf("field masking did not revoke filter pushdown: %+v", resource)
	}

	allWithoutFilter := policyAdapterFunc(func(_ context.Context, req AdapterRequest) (AdapterDecision, error) {
		return AdapterDecision{Coverage: CoverageAll, Resources: []AdapterResourceDecision{{
			Key: req.Resources[0].Key, AllowedFieldPaths: req.Resources[0].FieldPaths, ExistenceDisclosure: ExistenceVisible,
		}}}, nil
	})
	decision, err = NewService(Register(ResourceObject, allWithoutFilter)).Evaluate(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	resource, _ = decision.Resource("scope")
	if len(resource.FilterableFields) != 0 {
		t.Fatalf("entity/field coverage granted filter pushdown: %+v", resource)
	}
}

func TestPolicyServiceRejectsInvalidPrincipalKindAndDuplicateKeys(t *testing.T) {
	allow := policyAdapterFunc(func(_ context.Context, req AdapterRequest) (AdapterDecision, error) {
		out := AdapterDecision{Coverage: CoverageAll, Resources: make([]AdapterResourceDecision, 0, len(req.Resources))}
		for _, resource := range req.Resources {
			out.Resources = append(out.Resources, AdapterResourceDecision{Key: resource.Key, AllowedFieldPaths: resource.FieldPaths, ExistenceDisclosure: ExistenceVisible})
		}
		return out, nil
	})
	service := NewService(Register(ResourceObject, allow))
	var nilService *Service
	if _, err := nilService.Evaluate(context.Background(), EvaluateRequest{}); !errors.Is(err, ErrDependencyUnavailable) {
		t.Fatalf("nil service error = %v", err)
	}
	if _, err := service.Evaluate(context.Background(), EvaluateRequest{Purpose: "read", Resources: []ResourceDescriptor{{Key: "one", Kind: ResourceObject, TenantID: "t"}}}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("unauthenticated error = %v", err)
	}
	if _, err := service.Evaluate(context.Background(), EvaluateRequest{Principal: policyTestPrincipal(), Purpose: "read", Resources: []ResourceDescriptor{{Key: "one", Kind: "unknown", TenantID: "t"}}}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("unknown kind error = %v", err)
	}
	if _, err := service.Evaluate(context.Background(), EvaluateRequest{Principal: policyTestPrincipal(), Purpose: "read", Resources: []ResourceDescriptor{{Key: "one", Kind: ResourceObject, TenantID: "t"}, {Key: "one", Kind: ResourceObject, TenantID: "t"}}}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("duplicate key error = %v", err)
	}
	tooManyRefs := make([]AccessRef, maxResourceAccessRefs+1)
	if _, err := service.Evaluate(context.Background(), EvaluateRequest{Principal: policyTestPrincipal(), Purpose: "read", Resources: []ResourceDescriptor{{Key: "one", Kind: ResourceObject, TenantID: "t", AccessRefs: tooManyRefs}}}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("access ref limit error = %v", err)
	}
	oversized := AccessRef{Kind: "memory_record", ID: strings.Repeat("x", MaxAccessRefIDBytes+1), TenantID: "t"}
	if oversized.WithinLimits() {
		t.Fatal("oversized access ref unexpectedly passed the shared limit check")
	}
	if _, err := service.Evaluate(context.Background(), EvaluateRequest{Principal: policyTestPrincipal(), Purpose: "read", Resources: []ResourceDescriptor{{Key: "one", Kind: ResourceObject, TenantID: "t", AccessRefs: []AccessRef{oversized}}}}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("oversized access ref error = %v", err)
	}
}

func policyTestPrincipal() identity.Principal {
	return identity.NormalizePrincipal(identity.Principal{SubjectProvider: "oidc", Subject: "subject-1", UserID: "user-1", ViewerKind: identity.ViewerKindUser})
}
