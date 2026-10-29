package main

import (
	"context"
	"testing"

	"orrery_v2/internal/app/authorization"
	"orrery_v2/internal/app/datapolicy"
	"orrery_v2/internal/app/identity"
	"orrery_v2/internal/app/rebac"
	"orrery_v2/internal/platform/testkit"
)

func TestProductionAuthorizationRevisionUsesPolicyStoreAndDisablesCacheWithoutProviderToken(t *testing.T) {
	ctx := context.Background()
	runtime := testkit.NewInitializedTestRuntime(t, OntologyModule())
	core, err := runtime.Databases.Store("core")
	if err != nil {
		t.Fatalf("core store: %v", err)
	}
	state, err := newProductionOntologySecurityState(core, nil)
	if err != nil {
		t.Fatalf("new security state: %v", err)
	}
	principal := identity.NormalizePrincipal(identity.Principal{SubjectProvider: "oidc", Subject: "subject-1", UserID: "user-1", ViewerKind: identity.ViewerKindUser})
	before, err := state.Snapshots.Capture(ctx, principal)
	if err != nil {
		t.Fatalf("capture before: %v", err)
	}
	if before.AuthorizationRevision != "" || before.PolicyRevision != "policy:0" || before.Cacheable() {
		t.Fatalf("before snapshot = %+v", before)
	}
	var invalidation authorization.InvalidationEvent
	state.Invalidations.Subscribe(func(event authorization.InvalidationEvent) { invalidation = event })
	if err := state.PolicyStore.SaveFieldPolicy(ctx, datapolicy.FieldPolicy{
		ID: "field-1", TenantID: "mission:1", Consumer: "ontology_workbench", Purpose: "ontology.context.read", ObjectType: "mission", FieldPath: "secret", Effect: datapolicy.EffectMask, Enabled: true,
	}); err != nil {
		t.Fatalf("save field policy: %v", err)
	}
	after, err := state.Snapshots.Capture(ctx, principal)
	if err != nil {
		t.Fatalf("capture after: %v", err)
	}
	if after.PolicyRevision != "policy:1" || invalidation.Kind != authorization.InvalidationPolicy || invalidation.Revision != 1 {
		t.Fatalf("after snapshot = %+v invalidation=%+v", after, invalidation)
	}
}

func TestProductionAuthorizationRevisionUsesCoreReBACRevision(t *testing.T) {
	ctx := context.Background()
	runtime := testkit.NewInitializedTestRuntime(t, OntologyModule())
	core, err := runtime.Databases.Store("core")
	if err != nil {
		t.Fatalf("core store: %v", err)
	}
	revisions := rebac.NewPlatformStore(core)
	state, err := newProductionOntologySecurityState(core, revisions)
	if err != nil {
		t.Fatalf("new security state: %v", err)
	}
	principal := identity.NormalizePrincipal(identity.Principal{SubjectProvider: "oidc", Subject: "subject-1", UserID: "user-1", ViewerKind: identity.ViewerKindUser})

	before, err := state.Snapshots.Capture(ctx, principal)
	if err != nil {
		t.Fatalf("capture before: %v", err)
	}
	if before.AuthorizationRevision != "authorization:0" || !before.Cacheable() {
		t.Fatalf("before snapshot = %+v", before)
	}
	if err := revisions.SaveRelationTuple(ctx, rebac.RelationTuple{
		ID: "relation-1", TenantID: "global", Subject: rebac.Ref{Kind: "user", ID: "user-1"},
		Relation: "manager", Object: rebac.Ref{Kind: "platform", ID: "global"},
	}); err != nil {
		t.Fatalf("save relation tuple: %v", err)
	}
	after, err := state.Snapshots.Capture(ctx, principal)
	if err != nil {
		t.Fatalf("capture after: %v", err)
	}
	if after.AuthorizationRevision != "authorization:1" {
		t.Fatalf("after snapshot = %+v", after)
	}
}

func TestProductionAuthorizationRevisionFailsClosedForOpenFGAEnforcement(t *testing.T) {
	ctx := context.Background()
	runtime := testkit.NewInitializedTestRuntime(t, OntologyModule())
	core, err := runtime.Databases.Store("core")
	if err != nil {
		t.Fatalf("core store: %v", err)
	}
	state, err := newProductionOntologySecurityState(core, rebac.NewPlatformStore(core), authorizationModeOpenFGAEnforce)
	if err != nil {
		t.Fatalf("new security state: %v", err)
	}
	principal := identity.NormalizePrincipal(identity.Principal{SubjectProvider: "oidc", Subject: "subject-1", UserID: "user-1", ViewerKind: identity.ViewerKindUser})
	snapshot, err := state.Snapshots.Capture(ctx, principal)
	if err != nil {
		t.Fatalf("capture: %v", err)
	}
	if snapshot.AuthorizationRevision != "" || snapshot.Cacheable() {
		t.Fatalf("OpenFGA snapshot must not claim a local authorization revision: %+v", snapshot)
	}
}
