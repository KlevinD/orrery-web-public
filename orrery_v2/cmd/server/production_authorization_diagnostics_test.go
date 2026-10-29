package main

import (
	"context"
	"testing"

	authorizationapp "orrery_v2/internal/app/authorization"
	"orrery_v2/internal/app/operations"
)

func TestProductionAuthorizationDiagnosticsUsesDedicatedBackfillReader(t *testing.T) {
	sink := &authzRelationSink{}
	api := productionAPI{
		authorizationRelations: authorizationapp.NewRelationProjector(sink),
		authorizationBackfill: authzBackfillOperations{graph: operations.OrgGraphDTO{
			Organizations: []operations.OrganizationDTO{{OrganizationID: "org-1", OwnerProfileID: "owner-1"}},
		}},
	}

	result, err := api.GetAuthorizationRelationBackfillDiagnostics(context.Background())
	if err != nil {
		t.Fatalf("GetAuthorizationRelationBackfillDiagnostics: %v", err)
	}
	if result.Organizations != 1 || result.Tuples != 1 || result.Diff.Checked != 1 || result.Diff.Missing != 1 {
		t.Fatalf("diagnostics = %+v", result)
	}
	if len(sink.checked) != 0 {
		t.Fatalf("dry-run diagnostics should list relations instead of issuing per-tuple checks: %+v", sink.checked)
	}
}
