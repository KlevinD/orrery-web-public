package datavisibility

import (
	"context"
	"testing"

	"orrery_v2/internal/app/identity"
)

func TestPolicyServiceFieldAdapterClassifiesEveryRequestedPath(t *testing.T) {
	classifier := FieldClassifierFunc(func(_ context.Context, principal identity.Principal, purpose string, resource ResourceDescriptor, fieldPath string) (FieldEffect, error) {
		if principal.UserID != "user-1" || purpose != "ontology.lineage.read" || resource.Key != "evidence-1" {
			t.Fatalf("classifier input principal=%+v purpose=%s resource=%+v", principal, purpose, resource)
		}
		if fieldPath == "summary" {
			return FieldMask, nil
		}
		return FieldAllow, nil
	})
	entity := policyAdapterFunc(func(_ context.Context, req AdapterRequest) (AdapterDecision, error) {
		return AdapterDecision{Coverage: CoverageEntity, Resources: []AdapterResourceDecision{{Key: req.Resources[0].Key, ExistenceDisclosure: ExistenceVisible}}}, nil
	})
	service := NewService(Register(ResourceEvidence, entity), Register(ResourceEvidence, NewFieldPolicyAdapter(classifier, nil)))
	decision, err := service.Evaluate(context.Background(), EvaluateRequest{
		Principal: policyTestPrincipal(), Purpose: "ontology.lineage.read",
		Resources: []ResourceDescriptor{{Key: "evidence-1", Kind: ResourceEvidence, TenantID: "mission:1", FieldPaths: []string{"observed_at", "summary"}}},
	})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	item, _ := decision.Resource("evidence-1")
	if len(item.Fields.Allowed) != 1 || item.Fields.Allowed[0] != "observed_at" || len(item.Fields.Masked) != 1 || item.Fields.Masked[0] != "summary" {
		t.Fatalf("field decision = %+v", item)
	}
}
