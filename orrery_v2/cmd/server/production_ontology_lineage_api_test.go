package main

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/authorization"
	"orrery_v2/internal/app/contextgraph"
	"orrery_v2/internal/app/datapolicy"
	"orrery_v2/internal/app/identity"
	missioncontrol "orrery_v2/internal/app/missioncontrol"
	"orrery_v2/internal/app/ontology"
	ontologycontract "orrery_v2/internal/app/ontologyview/contract"
	lineageview "orrery_v2/internal/app/ontologyview/lineage"
	"orrery_v2/internal/app/operations"
	"orrery_v2/internal/app/projectadmin"
	knowledgestore "orrery_v2/internal/knowledge/store"
	memstore "orrery_v2/internal/memory/store"
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/eventstore"
	"orrery_v2/internal/platform/graphprotocol"
	"orrery_v2/internal/platform/testkit"
	toolres "orrery_v2/internal/resource/tool"
)

func TestProductionSafeOntologyEventReaderExcludesPayloadAndVerifiesScope(t *testing.T) {
	ctx := context.Background()
	store, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: t.Name()})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	accessRef := contextgraph.AccessRef{Kind: "organization", ID: "tenant-1", TenantID: "tenant-1"}
	metadata, err := json.Marshal(ontologyEventMetadata{
		TenantID: "tenant-1", StreamID: "mission-1", Version: 4, SchemaVersion: "v2",
		OccurredAt:      time.Unix(20, 0).UTC().Format(time.RFC3339Nano),
		SourceAccessRef: graphprotocol.AccessRef{Kind: accessRef.Kind, ID: accessRef.ID, TenantID: accessRef.TenantID},
	})
	if err != nil {
		t.Fatalf("marshal metadata: %v", err)
	}
	if err := eventstore.New(store).Append(ctx, eventstore.Envelope{
		EventID: "event-1", StreamID: "ontology.event-1", StreamVersion: 1,
		EventType: "ontology.mission.updated", EventVersion: 1,
		Payload: []byte(`{"private_secret":"must-not-leak"}`), Metadata: metadata, CreatedAt: time.Unix(20, 0).UTC(),
	}); err != nil {
		t.Fatalf("append event: %v", err)
	}
	reader := productionSafeOntologyEventReader{store: eventstore.New(store)}
	event, err := reader.GetSafeOntologyEvent(ctx, "tenant-1", "event-1", accessRef)
	if err != nil || event.Metadata.Type != "mission.updated" || event.Metadata.StreamID != "mission-1" || event.Metadata.Version != 4 {
		t.Fatalf("safe event = %+v, err=%v", event, err)
	}
	encoded, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("marshal safe event: %v", err)
	}
	if strings.Contains(string(encoded), "private_secret") || strings.Contains(string(encoded), "must-not-leak") {
		t.Fatalf("safe event exposed payload: %s", encoded)
	}
	for _, request := range []struct {
		tenant string
		ref    contextgraph.AccessRef
	}{
		{"tenant-2", accessRef},
		{"tenant-1", contextgraph.AccessRef{Kind: "organization", ID: "tenant-2", TenantID: "tenant-2"}},
	} {
		if _, err := reader.GetSafeOntologyEvent(ctx, request.tenant, "event-1", request.ref); !errors.Is(err, lineageview.ErrSafeEventUnavailable) {
			t.Fatalf("unsafe event lookup tenant=%s ref=%+v error=%v", request.tenant, request.ref, err)
		}
	}
}

func TestProductionOntologyLineageAPIAppliesScopeSourceAndFieldPolicies(t *testing.T) {
	ctx := context.Background()
	runtime := testkit.NewInitializedTestRuntime(t, OntologyModule())
	core, err := runtime.Databases.Store(db.RoleCore)
	if err != nil {
		t.Fatalf("core store: %v", err)
	}
	organizations := operations.NewOrganizationCatalogStore(runtime.DBRouter)
	if err := organizations.SaveOrganization(ctx, operations.OrganizationDTO{OrganizationID: "tenant-1", Name: "Tenant One"}); err != nil {
		t.Fatalf("save organization: %v", err)
	}
	authorizer := authorization.NewService(fixedProductionAuthorizationAdapter{decision: authorization.Decision{Allowed: true, Source: authorization.DecisionSourceOpenFGA}})
	tools := toolres.NewMemoryRegistry()
	if err := tools.Register(ctx, toolres.ToolDefinition{ID: agentcore.ToolID("lineage-tool"), Kind: toolres.KindAPITool, Name: "Lineage Tool", Version: "1"}); err != nil {
		t.Fatalf("register tool: %v", err)
	}
	scopes, err := newProductionOntologyScopeState(
		core, organizations, projectadmin.NewMemoryRepository(), missioncontrol.NewMemoryProjectionRepository(), tools,
		authorizer, authorization.NewSnapshotService(nil, nil), &fixedProductionRoleAuthorizer{allowed: true},
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
	service, err := newProductionOntologyLineageAPI(
		core, runtime.DBRouter, scopes, cursors, concurrency, policy, authorizer,
		knowledgestore.NewPlatformStore(core), memstore.NewPlatformStore(core),
	)
	if err != nil {
		t.Fatalf("new lineage API: %v", err)
	}
	accessRef := contextgraph.AccessRef{Kind: "organization", ID: "tenant-1", TenantID: "tenant-1"}
	graph := contextgraph.NewPlatformStore(core)
	if err := graph.SaveObject(ctx, contextgraph.Object{
		ID: "mission-1", TenantID: "tenant-1", ObjectType: "lineage_mission", CanonicalKey: "lineage_mission:1",
		Label: "Mission One", OwnerAccessRef: accessRef, UpdatedAt: time.Unix(30, 0).UTC(),
	}); err != nil {
		t.Fatalf("save subject: %v", err)
	}
	fact := contextgraph.Fact{
		ID: "fact-1", TenantID: "tenant-1", Subject: contextgraph.Ref{Kind: "lineage_mission", ID: "mission-1"},
		Predicate: "status", Value: "active", Confidence: 0.9,
		Provenance: contextgraph.Ref{Kind: "event", ID: "event-1"}, EvidenceRefs: []contextgraph.Ref{{Kind: "event", ID: "event-1"}},
		SourceAccessRef: accessRef, SourceStreamID: "mission-1", SourceVersion: 2, UpdatedAt: time.Unix(31, 0).UTC(),
	}
	if err := graph.SaveFact(ctx, fact); err != nil {
		t.Fatalf("save fact: %v", err)
	}
	if err := graph.SaveEvidence(ctx, contextgraph.Evidence{
		ID: "event-1", TenantID: "tenant-1", SourceRef: contextgraph.Ref{Kind: "lineage.mission.updated", ID: "event-1"},
		SourceAccessRef: accessRef, Summary: "safe source", Confidence: 1, ObservedAt: time.Unix(31, 0).UTC(),
	}); err != nil {
		t.Fatalf("save evidence: %v", err)
	}
	if err := graph.SaveProjectionAudit(ctx, contextgraph.ProjectionAudit{
		ID: "audit-1", TenantID: "tenant-1", SourceEventID: "event-1", SourceEventType: "lineage.mission.updated",
		Status: "applied", SourceAccessRef: accessRef, ObservedAt: time.Unix(32, 0).UTC(),
	}); err != nil {
		t.Fatalf("save projection audit: %v", err)
	}
	metadata, _ := json.Marshal(ontologyEventMetadata{
		TenantID: "tenant-1", StreamID: "mission-1", Version: 2,
		SourceAccessRef: graphprotocol.AccessRef{Kind: accessRef.Kind, ID: accessRef.ID, TenantID: accessRef.TenantID},
	})
	if err := eventstore.New(core).Append(ctx, eventstore.Envelope{
		EventID: "event-1", StreamID: "ontology.event-1", StreamVersion: 1,
		EventType: "ontology.lineage.mission.updated", EventVersion: 1,
		Payload: []byte(`{"private":"payload"}`), Metadata: metadata, CreatedAt: time.Unix(31, 0).UTC(),
	}); err != nil {
		t.Fatalf("append event: %v", err)
	}
	registry := ontology.NewPlatformRegistry(core)
	if err := registry.SaveObjectType(ctx, ontology.ObjectType{ID: "lineage_mission", DisplayName: "Lineage Mission", CanonicalKeyRule: "lineage_mission:{id}"}); err != nil {
		t.Fatalf("save object type: %v", err)
	}
	if err := registry.SaveMappingRule(ctx, ontology.MappingRule{
		ID: "lineage-rule", TenantID: "tenant-1", EventType: "lineage.mission.updated",
		ObjectTypes: []string{"lineage_mission"}, Enabled: true,
		Rule: map[string]any{"objects": []any{map[string]any{"type": "lineage_mission", "id": "$.id", "canonical_key": "lineage_mission:{id}"}}},
	}); err != nil {
		t.Fatalf("save mapping rule: %v", err)
	}
	principal := identity.NormalizePrincipal(identity.Principal{
		SubjectProvider: "oidc", Subject: "subject-1", UserID: "user-1", ViewerKind: identity.ViewerKindUser,
		CoarseScopes: []string{"operations.read"},
	})
	anchor, err := service.GetProvenance(ctx, principal, "tenant-1", "fact-1")
	if err != nil || anchor.Event == nil || anchor.Event.ID != "event-1" || anchor.Mapping.Status != lineageview.MappingCandidate || len(anchor.Mapping.Candidates) != 1 {
		t.Fatalf("provenance anchor = %+v, err=%v", anchor, err)
	}
	assertions, err := service.ListAssertions(ctx, principal, lineageview.FactPageQuery{TenantID: "tenant-1", FactID: "fact-1"})
	if err != nil || len(assertions.Items) != 1 {
		t.Fatalf("assertions = %+v, err=%v", assertions, err)
	}
	evidence, err := service.ListEvidence(ctx, principal, lineageview.FactPageQuery{TenantID: "tenant-1", FactID: "fact-1"})
	if err != nil || len(evidence.Items) != 1 || evidence.Items[0].Status != lineageview.ReferenceResolved {
		t.Fatalf("evidence = %+v, err=%v", evidence, err)
	}
	trace, err := service.ListProjectionTrace(ctx, principal, lineageview.FactPageQuery{TenantID: "tenant-1", FactID: "fact-1"})
	if err != nil || len(trace.Items) != 1 || trace.Items[0].ID != "audit-1" {
		t.Fatalf("projection trace = %+v, err=%v", trace, err)
	}
	if err := policy.SaveFieldPolicy(ctx, datapolicy.FieldPolicy{
		ID: "lineage-mask-provenance", TenantID: "tenant-1", Consumer: "ontology_workbench", Purpose: "ontology.lineage.read",
		ObjectType: "status", FieldPath: "provenance", Effect: datapolicy.EffectMask, Enabled: true,
	}); err != nil {
		t.Fatalf("save field policy: %v", err)
	}
	anchor, err = service.GetProvenance(ctx, principal, "tenant-1", "fact-1")
	if err != nil || anchor.ProvenanceStatus != lineageview.ReferenceUnavailable || anchor.Event != nil {
		t.Fatalf("field-masked provenance = %+v, err=%v", anchor, err)
	}
}

func TestProductionOntologyLineageAPIFailsClosedWithoutDependencies(t *testing.T) {
	if _, err := newProductionOntologyLineageAPI(nil, nil, nil, nil, nil, nil, nil, nil, nil); err == nil {
		t.Fatal("missing dependencies returned nil error")
	}
}
