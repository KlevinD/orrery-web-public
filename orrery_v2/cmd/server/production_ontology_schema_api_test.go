package main

import (
	"context"
	"errors"
	"testing"

	"orrery_v2/internal/app/authorization"
	"orrery_v2/internal/app/datapolicy"
	"orrery_v2/internal/app/datavisibility"
	"orrery_v2/internal/app/identity"
	missioncontrol "orrery_v2/internal/app/missioncontrol"
	"orrery_v2/internal/app/ontology"
	ontologycontract "orrery_v2/internal/app/ontologyview/contract"
	ontologyschema "orrery_v2/internal/app/ontologyview/schema"
	"orrery_v2/internal/app/operations"
	"orrery_v2/internal/app/projectadmin"
	"orrery_v2/internal/platform/testkit"
	toolres "orrery_v2/internal/resource/tool"
)

func TestProductionOntologySchemaAPIEnforcesDefinitionTenantAndSafeFieldBoundaries(t *testing.T) {
	ctx := context.Background()
	runtime := testkit.NewInitializedTestRuntime(t, OntologyModule())
	core, err := runtime.Databases.Store("core")
	if err != nil {
		t.Fatalf("core store: %v", err)
	}
	organizations := operations.NewOrganizationCatalogStore(runtime.DBRouter)
	if err := organizations.SaveOrganization(ctx, operations.OrganizationDTO{OrganizationID: "tenant-1", Name: "Tenant One"}); err != nil {
		t.Fatalf("save organization: %v", err)
	}
	authorizer := authorization.NewService(fixedProductionAuthorizationAdapter{decision: authorization.Decision{Allowed: true, Source: authorization.DecisionSourceOpenFGA}})
	scopes, err := newProductionOntologyScopeState(
		core,
		organizations,
		projectadmin.NewMemoryRepository(),
		missioncontrol.NewMemoryProjectionRepository(),
		toolres.NewMemoryRegistry(),
		authorizer,
		authorization.NewSnapshotService(nil, nil),
		&fixedProductionRoleAuthorizer{allowed: true},
	)
	if err != nil {
		t.Fatalf("new scope state: %v", err)
	}
	cursors, err := newProductionOntologyCursorState(ctx, nil, nil, false)
	if err != nil {
		t.Fatalf("new cursor state: %v", err)
	}
	concurrency, err := ontologycontract.NewConcurrencyLimiter(nil)
	if err != nil {
		t.Fatalf("new concurrency limiter: %v", err)
	}
	policy := datapolicy.NewPlatformStore(core)
	service, err := newProductionOntologySchemaAPI(core, scopes, cursors, concurrency, policy)
	if err != nil {
		t.Fatalf("new schema API: %v", err)
	}

	registry := ontology.NewPlatformRegistry(core)
	for _, objectType := range []ontology.ObjectType{
		{ID: "schema-test-global", TenantScope: ontology.TenantScopeGlobal, DisplayName: "Global Definition", CanonicalKeyRule: "global:{id}"},
		{ID: "schema-test-one", TenantID: "tenant-1", TenantScope: ontology.TenantScopeTenant, DisplayName: "Tenant One Definition", CanonicalKeyRule: "one:{id}"},
		{ID: "schema-test-two", TenantID: "tenant-2", TenantScope: ontology.TenantScopeTenant, DisplayName: "Tenant Two Definition", CanonicalKeyRule: "two:{id}"},
	} {
		if err := registry.SaveObjectType(ctx, objectType); err != nil {
			t.Fatalf("SaveObjectType(%s): %v", objectType.ID, err)
		}
	}
	rule := ontology.MappingRule{
		ID: "schema-test-rule", TenantID: "tenant-1", EventType: "schema.test.created", ObjectTypes: []string{"schema-test-one"}, Enabled: true,
		Rule: map[string]any{"objects": []any{map[string]any{"type": "schema-test-one", "id": "$.id", "canonical_key": "one:{id}"}}},
	}
	if err := registry.SaveMappingRule(ctx, rule); err != nil {
		t.Fatalf("SaveMappingRule: %v", err)
	}
	if err := policy.SaveFieldPolicy(ctx, datapolicy.FieldPolicy{
		ID: "schema-mask-canonical-key", TenantID: "tenant-1", Consumer: "ontology_workbench",
		Purpose: "ontology.schema.read", ObjectType: "object", FieldPath: "attributes.canonical_key_rule",
		Effect: datapolicy.EffectMask, Enabled: true,
	}); err != nil {
		t.Fatalf("SaveFieldPolicy: %v", err)
	}

	principal := identity.NormalizePrincipal(identity.Principal{
		SubjectProvider: "oidc", Subject: "subject-1", UserID: "user-1", ViewerKind: identity.ViewerKindUser,
		CoarseScopes: []string{"operations.read"},
	})
	page, err := service.ListDefinitions(ctx, principal, ontologyschema.DefinitionListQuery{TenantID: "tenant-1", Kind: ontologyschema.KindObject, Search: "schema-test", Limit: 20})
	if err != nil {
		t.Fatalf("ListDefinitions: %v", err)
	}
	if !definitionPageHasID(page.Items, "schema-test-global") || !definitionPageHasID(page.Items, "schema-test-one") || definitionPageHasID(page.Items, "schema-test-two") {
		t.Fatalf("tenant-filtered definitions = %+v", page.Items)
	}
	if _, err := service.GetDefinition(ctx, principal, "tenant-1", ontologyschema.KindObject, "schema-test-two"); !errors.Is(err, ontologycontract.ErrUnavailable) {
		t.Fatalf("wrong-tenant exact error = %v", err)
	}
	objectDetail, err := service.GetDefinition(ctx, principal, "tenant-1", ontologyschema.KindObject, "schema-test-one")
	if err != nil || objectDetail.Attributes.CanonicalKeyRule != "" {
		t.Fatalf("field-policy detail = %+v, err=%v", objectDetail, err)
	}
	detail, err := service.GetDefinition(ctx, principal, "tenant-1", ontologyschema.KindMapping, "schema-test-rule")
	if err != nil {
		t.Fatalf("GetDefinition(mapping): %v", err)
	}
	if detail.Attributes.EventType != "schema.test.created" {
		t.Fatalf("safe mapping attributes = %+v", detail.Attributes)
	}
}

func TestProductionOntologySchemaAPIFailsClosedWithoutDependencies(t *testing.T) {
	if _, err := newProductionOntologySchemaAPI(nil, nil, nil, nil, nil); err == nil {
		t.Fatal("missing dependencies returned nil error")
	}
	decision, err := (productionSchemaDefinitionPolicy{}).Evaluate(context.Background(), datavisibility.AdapterRequest{Resources: []datavisibility.ResourceDescriptor{{Key: "mapping:one", Type: "mapping", FieldPaths: []string{"attributes.event_type", "attributes.rule"}}}})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if len(decision.Resources) != 1 || len(decision.Resources[0].AllowedFieldPaths) != 1 || decision.Resources[0].AllowedFieldPaths[0] != "attributes.event_type" || len(decision.Resources[0].MaskedFieldPaths) != 1 || decision.Resources[0].MaskedFieldPaths[0] != "attributes.rule" {
		t.Fatalf("safe-field decision = %+v", decision)
	}
}

func definitionPageHasID(items []ontologyschema.DefinitionItem, id string) bool {
	for _, item := range items {
		if item.ID == id {
			return true
		}
	}
	return false
}
