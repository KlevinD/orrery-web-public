package main

import (
	"context"
	"testing"
	"time"

	"orrery_v2/internal/app/actioncontract"
	"orrery_v2/internal/app/contextgraph"
	contextconsumer "orrery_v2/internal/app/contextgraph/consumer"
	"orrery_v2/internal/app/datapolicy"
	"orrery_v2/internal/app/ontology"
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/migration"
)

func TestProductionEnterpriseGraphConsumerUsesDataPolicyStore(t *testing.T) {
	ctx := context.Background()
	databases, err := db.OpenRegistry(ctx, db.RegistryConfig{Databases: map[string]db.Config{
		db.RoleCore: {Driver: db.DriverMemory, Name: "enterprise-graph-consumer"},
	}})
	if err != nil {
		t.Fatalf("open database registry: %v", err)
	}
	defer databases.Close()
	core, ok := databases.Get(db.RoleCore)
	if !ok || core == nil {
		t.Fatalf("core database missing")
	}
	coordinator := migration.NewCoordinator(databases)
	if err := contextgraph.RegisterMigrations(coordinator); err != nil {
		t.Fatalf("register contextgraph migrations: %v", err)
	}
	if err := datapolicy.RegisterMigrations(coordinator); err != nil {
		t.Fatalf("register datapolicy migrations: %v", err)
	}
	if err := ontology.RegisterMigrations(coordinator); err != nil {
		t.Fatalf("register ontology migrations: %v", err)
	}
	if err := actioncontract.RegisterMigrations(coordinator); err != nil {
		t.Fatalf("register actioncontract migrations: %v", err)
	}
	if err := coordinator.UpRole(ctx, db.RoleCore); err != nil {
		t.Fatalf("migrate core db: %v", err)
	}

	graphStore := contextgraph.NewPlatformStore(core)
	if err := graphStore.SaveObject(ctx, contextgraph.Object{
		ID:           "mission-1",
		TenantID:     "tenant-1",
		ObjectType:   "mission",
		CanonicalKey: "mission-1",
		Label:        "Mission",
		Properties: map[string]any{
			"title":        "Launch",
			"secret_notes": "private",
		},
		Status:         "confirmed",
		OwnerAccessRef: contextgraph.AccessRef{Kind: "organization", ID: "tenant-1", TenantID: "tenant-1"},
	}); err != nil {
		t.Fatalf("save object: %v", err)
	}
	policyStore := datapolicy.NewPlatformStore(core)
	if err := graphStore.SaveFact(ctx, contextgraph.Fact{
		ID: "mission-secret", TenantID: "tenant-1", Subject: contextgraph.Ref{Kind: "mission", ID: "mission-1"},
		Predicate: "secret_notes", Value: "private", SourceStreamID: "test:mission-secret",
		SourceAccessRef: contextgraph.AccessRef{Kind: "organization", ID: "tenant-1", TenantID: "tenant-1"},
	}); err != nil {
		t.Fatalf("save masked fact: %v", err)
	}
	now := time.Unix(700, 0).UTC()
	if err := policyStore.SaveFieldPolicy(ctx, datapolicy.FieldPolicy{
		ID:         "mask-mission-secret",
		TenantID:   "tenant-1",
		Consumer:   "iris",
		Purpose:    "mission_assignment",
		ObjectType: "mission",
		FieldPath:  "secret_notes",
		Effect:     datapolicy.EffectMask,
		Reason:     "secret notes are not needed",
		Enabled:    true,
		CreatedAt:  now,
		UpdatedAt:  now,
	}); err != nil {
		t.Fatalf("save field policy: %v", err)
	}

	consumer := newProductionEnterpriseGraphConsumer(core)
	bundle, err := consumer.BuildContextBundle(ctx, contextconsumer.BuildContextBundleInput{
		TenantID:    "tenant-1",
		Consumer:    "iris",
		Purpose:     "mission_assignment",
		ActorRef:    contextgraph.Ref{Kind: "human", ID: "klevin"},
		SubjectRefs: []contextgraph.Ref{{Kind: "mission", ID: "mission-1"}},
	})
	if err != nil {
		t.Fatalf("BuildContextBundle: %v", err)
	}
	if len(bundle.Objects) != 1 {
		t.Fatalf("bundle objects = %+v", bundle.Objects)
	}
	if _, ok := bundle.Objects[0].Properties["secret_notes"]; ok {
		t.Fatalf("data policy field leaked into consumer bundle: %+v", bundle.Objects[0].Properties)
	}
	if bundle.Objects[0].Properties["title"] != "Launch" {
		t.Fatalf("non-sensitive field missing: %+v", bundle.Objects[0].Properties)
	}
	if len(bundle.Facts) != 0 {
		t.Fatalf("masked field leaked through facts: %+v", bundle.Facts)
	}
	// Exercise the same production store, schema, and policy wiring at explicit coordinates.
	jan := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	feb := jan.AddDate(0, 1, 0)
	for i, start := range []time.Time{jan, feb} {
		if err := graphStore.SaveObject(ctx, contextgraph.Object{
			ID: "history-1", TenantID: "tenant-1", ObjectType: "mission", CanonicalKey: "history-1", Label: start.Month().String(),
			OwnerAccessRef: contextgraph.AccessRef{Kind: "organization", ID: "tenant-1", TenantID: "tenant-1"},
			SourceStreamID: "test:history", SourceVersion: int64(i + 1), UpdatedAt: start,
			Temporal: contextgraph.TemporalMetadata{Mode: "state_interval", BusinessValidFrom: start, SourceRecordedAt: start},
		}); err != nil {
			t.Fatal(err)
		}
	}
	historical, err := newProductionContextGraphService(core).BuildContextBundle(ctx, contextgraph.BuildContextBundleRequest{
		TenantID: "tenant-1", Consumer: "iris", Purpose: "mission_assignment", ActorRef: contextgraph.Ref{Kind: "human", ID: "klevin"},
		SubjectRefs: []contextgraph.Ref{{Kind: "mission", ID: "history-1"}}, BusinessAsOf: jan.Add(time.Hour), KnownAt: jan.Add(time.Hour),
	})
	if err != nil || len(historical.Objects) != 1 || historical.Objects[0].Label != "January" {
		t.Fatalf("production historical bundle = %+v, %v", historical.Objects, err)
	}
	if len(bundle.PermissionConstraints) != 1 || bundle.PermissionConstraints[0].Reason != "secret notes are not needed" {
		t.Fatalf("permission constraints = %+v", bundle.PermissionConstraints)
	}
}

func TestProductionEnterpriseGraphConsumerIncludesOntologySchemaDictionary(t *testing.T) {
	ctx := context.Background()
	databases, err := db.OpenRegistry(ctx, db.RegistryConfig{Databases: map[string]db.Config{
		db.RoleCore: {Driver: db.DriverMemory, Name: "enterprise-graph-consumer-schema"},
	}})
	if err != nil {
		t.Fatalf("open database registry: %v", err)
	}
	defer databases.Close()
	core, ok := databases.Get(db.RoleCore)
	if !ok || core == nil {
		t.Fatalf("core database missing")
	}
	coordinator := migration.NewCoordinator(databases)
	if err := contextgraph.RegisterMigrations(coordinator); err != nil {
		t.Fatalf("register contextgraph migrations: %v", err)
	}
	if err := ontology.RegisterMigrations(coordinator); err != nil {
		t.Fatalf("register ontology migrations: %v", err)
	}
	if err := actioncontract.RegisterMigrations(coordinator); err != nil {
		t.Fatalf("register actioncontract migrations: %v", err)
	}
	if err := datapolicy.RegisterMigrations(coordinator); err != nil {
		t.Fatalf("register datapolicy migrations: %v", err)
	}
	if err := coordinator.UpRole(ctx, db.RoleCore); err != nil {
		t.Fatalf("migrate core db: %v", err)
	}
	registry := ontology.NewPlatformRegistry(core)
	if err := registry.SaveObjectType(ctx, ontology.ObjectType{
		ID:               "mission",
		TenantScope:      ontology.TenantScopeGlobal,
		DisplayName:      "Mission",
		CanonicalKeyRule: "mission:{id}",
		Properties: []ontology.PropertyDefinition{{
			PropertyID: "title",
			ValueType:  ontology.ValueTypeString,
			Required:   true,
		}},
	}); err != nil {
		t.Fatalf("save object type: %v", err)
	}
	if err := registry.SaveObjectType(ctx, ontology.ObjectType{
		ID: "unrelated", TenantScope: ontology.TenantScopeGlobal, DisplayName: "Unrelated", CanonicalKeyRule: "unrelated:{id}",
	}); err != nil {
		t.Fatal(err)
	}
	if err := registry.SavePropertyType(ctx, ontology.PropertyType{
		ID:             "mission.title",
		ObjectTypeID:   "mission",
		PropertyID:     "title",
		DisplayName:    "Title",
		ValueType:      ontology.ValueTypeString,
		ConflictPolicy: ontology.ConflictPolicyAuthorityWins,
	}); err != nil {
		t.Fatalf("save property type: %v", err)
	}
	if err := registry.SavePropertyType(ctx, ontology.PropertyType{
		ID: "unrelated.value", ObjectTypeID: "unrelated", PropertyID: "value", ValueType: ontology.ValueTypeString,
		ConflictPolicy: ontology.ConflictPolicyAuthorityWins,
	}); err != nil {
		t.Fatal(err)
	}
	actionRepository := actioncontract.NewPlatformRepository(core)
	if err := actionRepository.SaveActionType(ctx, actioncontract.ActionType{
		ID: "development.submit_code", TenantID: "system", Name: "Submit code",
		Category: actioncontract.CategoryAgent, ObjectTypeRefs: []string{"mission"}, Enabled: true,
		Semantics: actioncontract.ActionSemanticProfile{
			Verbs:         []string{"submit code", "提交代码"},
			Roles:         []actioncontract.SemanticRole{{Name: "repository", ObjectType: "mission", Required: true}},
			Preconditions: []string{"changes are ready"}, Effects: []string{"changes are reviewable"},
			KnowledgeTerms: []string{"contribution workflow"}, MemoryTerms: []string{"recent code submissions"},
		},
	}); err != nil {
		t.Fatalf("save action type: %v", err)
	}
	graphStore := contextgraph.NewPlatformStore(core)
	if err := graphStore.SaveObject(ctx, contextgraph.Object{
		ID:             "mission-1",
		TenantID:       "tenant-1",
		ObjectType:     "mission",
		CanonicalKey:   "mission:mission-1",
		Label:          "Mission",
		Properties:     map[string]any{"title": "Launch"},
		Status:         "confirmed",
		OwnerAccessRef: contextgraph.AccessRef{Kind: "organization", ID: "tenant-1", TenantID: "tenant-1"},
	}); err != nil {
		t.Fatalf("save object: %v", err)
	}

	consumer := newProductionEnterpriseGraphConsumer(core)
	bundle, err := consumer.BuildContextBundle(ctx, contextconsumer.BuildContextBundleInput{
		TenantID:    "tenant-1",
		Consumer:    "iris",
		Purpose:     "mission_assignment",
		ActorRef:    contextgraph.Ref{Kind: "human", ID: "klevin"},
		SubjectRefs: []contextgraph.Ref{{Kind: "mission", ID: "mission-1"}},
	})
	if err != nil {
		t.Fatalf("BuildContextBundle: %v", err)
	}

	if len(bundle.Schema.ObjectTypes) != 1 || bundle.Schema.ObjectTypes[0].ID != "mission" {
		t.Fatalf("schema object types = %+v", bundle.Schema.ObjectTypes)
	}
	if len(bundle.Schema.PropertyTypes) != 1 ||
		bundle.Schema.PropertyTypes[0].ObjectTypeID != "mission" ||
		bundle.Schema.PropertyTypes[0].PropertyID != "title" {
		t.Fatalf("schema property types = %+v", bundle.Schema.PropertyTypes)
	}
	if len(bundle.Schema.ActionTypes) != 1 ||
		bundle.Schema.ActionTypes[0].ID != "development.submit_code" ||
		len(bundle.Schema.ActionTypes[0].Verbs) != 2 ||
		len(bundle.Schema.ActionTypes[0].Roles) != 1 ||
		bundle.Schema.ActionTypes[0].Roles[0].ObjectType != "mission" ||
		len(bundle.Schema.ActionTypes[0].Preconditions) != 1 ||
		len(bundle.Schema.ActionTypes[0].Effects) != 1 ||
		len(bundle.Schema.ActionTypes[0].KnowledgeTerms) != 1 ||
		len(bundle.Schema.ActionTypes[0].MemoryTerms) != 1 {
		t.Fatalf("schema action types = %+v", bundle.Schema.ActionTypes)
	}
	if bundle.Schema.VersionHash == "" {
		t.Fatalf("schema version hash is empty: %+v", bundle.Schema)
	}
}
