package contextgraph

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"orrery_v2/internal/app/rebac"
)

func TestContextBundleFiltersHiddenObjectsAndMaskedFields(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	if err := store.SaveObject(ctx, Object{
		ID:           "mission-1",
		TenantID:     "tenant-1",
		ObjectType:   "mission",
		CanonicalKey: "mission-1",
		Label:        "Launch Orrery ontology",
		Properties: map[string]any{
			"title":        "Launch Orrery ontology",
			"secret_notes": "raw private note",
		},
		Sensitivity: "internal",
		Status:      "confirmed",
		EvidenceRefs: []Ref{{
			Kind: "knowledge_document",
			ID:   "doc-1",
		}},
	}); err != nil {
		t.Fatalf("save mission: %v", err)
	}
	if err := store.SaveObject(ctx, Object{
		ID:           "task-1",
		TenantID:     "tenant-1",
		ObjectType:   "task",
		CanonicalKey: "task-1",
		Label:        "Wire ContextBundle",
		Properties:   map[string]any{"status": "ready"},
		Sensitivity:  "internal",
		Status:       "confirmed",
	}); err != nil {
		t.Fatalf("save task: %v", err)
	}
	if err := store.SaveObject(ctx, Object{
		ID:           "person-secret",
		TenantID:     "tenant-1",
		ObjectType:   "person",
		CanonicalKey: "person-secret",
		Label:        "Hidden Person",
		Properties:   map[string]any{"name": "Hidden"},
		Sensitivity:  "restricted",
		Status:       "confirmed",
	}); err != nil {
		t.Fatalf("save hidden person: %v", err)
	}
	if err := store.SaveLink(ctx, Link{
		ID:           "link-mission-task",
		TenantID:     "tenant-1",
		LinkType:     "has_task",
		FromObjectID: "mission-1",
		ToObjectID:   "task-1",
		Status:       "confirmed",
		EvidenceRefs: []Ref{{Kind: "knowledge_document", ID: "doc-link"}},
	}); err != nil {
		t.Fatalf("save mission-task link: %v", err)
	}
	if err := store.SaveLink(ctx, Link{
		ID:           "link-mission-hidden",
		TenantID:     "tenant-1",
		LinkType:     "has_owner",
		FromObjectID: "mission-1",
		ToObjectID:   "person-secret",
		Status:       "confirmed",
	}); err != nil {
		t.Fatalf("save mission-hidden link: %v", err)
	}
	if err := store.SaveFact(ctx, Fact{
		ID:        "fact-1",
		TenantID:  "tenant-1",
		Subject:   Ref{Kind: "mission", ID: "mission-1"},
		Predicate: "goal",
		Value:     "Launch Orrery ontology",
		EvidenceRefs: []Ref{{
			Kind: "knowledge_document",
			ID:   "doc-1",
		}},
		Status: "confirmed",
	}); err != nil {
		t.Fatalf("save fact: %v", err)
	}
	if err := store.SaveEvidence(ctx, Evidence{
		ID:         "doc-1",
		TenantID:   "tenant-1",
		SourceRef:  Ref{Kind: "knowledge_document", ID: "doc-1"},
		Summary:    "Ontology launch plan",
		Confidence: 0.9,
	}); err != nil {
		t.Fatalf("save evidence: %v", err)
	}
	if err := store.SaveEvidence(ctx, Evidence{
		ID: "doc-link", TenantID: "tenant-1",
		SourceRef: Ref{Kind: "knowledge_document", ID: "doc-link"},
		Summary:   "Evidence for the visible relationship", Confidence: 0.8,
	}); err != nil {
		t.Fatalf("save link evidence: %v", err)
	}

	service := NewService(store,
		WithClock(func() time.Time { return time.Date(2026, 6, 19, 10, 0, 0, 0, time.UTC) }),
		WithPolicyProvider(fixedContextPolicy{
			decision: PermissionDecision{
				HiddenObjectIDs: []string{"person-secret"},
				MaskedFields:    map[string][]string{"mission-1": {"secret_notes"}},
				Constraints: []PermissionConstraint{
					{
						Scope:        "mission.read",
						ObjectRef:    Ref{Kind: "mission", ID: "mission-1"},
						MaskedFields: []string{"secret_notes"},
						Reason:       "field policy",
					},
					{
						Scope:     "person.read",
						ObjectRef: Ref{Kind: "person", ID: "person-secret"},
						Reason:    "hidden object policy",
					},
				},
			},
		}),
	)

	bundle, err := service.BuildContextBundle(ctx, BuildContextBundleRequest{
		TenantID:     "tenant-1",
		Consumer:     "iris",
		Purpose:      "mission_assignment",
		ActorRef:     Ref{Kind: "human", ID: "klevin"},
		SubjectRefs:  []Ref{{Kind: "mission", ID: "mission-1"}},
		IncludeKinds: []string{"mission", "task", "person"},
		MaxDepth:     1,
		TokenBudget:  8000,
	})
	if err != nil {
		t.Fatalf("BuildContextBundle: %v", err)
	}

	if bundle.BundleID == "" || bundle.Consumer != "iris" || bundle.Purpose != "mission_assignment" {
		t.Fatalf("bundle identity = %+v", bundle)
	}
	if !containsObject(bundle.Objects, "mission-1") || !containsObject(bundle.Objects, "task-1") {
		t.Fatalf("bundle objects = %+v, want mission and task", bundle.Objects)
	}
	if containsObject(bundle.Objects, "person-secret") {
		t.Fatalf("hidden object leaked into bundle: %+v", bundle.Objects)
	}
	mission := findObject(t, bundle.Objects, "mission-1")
	if mission.Properties["title"] != "Launch Orrery ontology" {
		t.Fatalf("mission properties = %+v", mission.Properties)
	}
	if _, ok := mission.Properties["secret_notes"]; ok {
		t.Fatalf("masked field leaked into mission properties: %+v", mission.Properties)
	}
	if containsLink(bundle.Links, "link-mission-hidden") {
		t.Fatalf("link to hidden object leaked into bundle: %+v", bundle.Links)
	}
	if !containsLink(bundle.Links, "link-mission-task") {
		t.Fatalf("visible link missing from bundle: %+v", bundle.Links)
	}
	if len(bundle.Facts) != 1 || len(bundle.Evidence) != 2 {
		t.Fatalf("facts/evidence = %+v / %+v", bundle.Facts, bundle.Evidence)
	}
	if len(bundle.PermissionConstraints) != 1 {
		t.Fatalf("permission constraints = %+v", bundle.PermissionConstraints)
	}
	if len(bundle.BuildTrace.HiddenObjectIDs) != 0 || !containsString(bundle.BuildTrace.MaskedFields, "mission-1.secret_notes") {
		t.Fatalf("build trace = %+v", bundle.BuildTrace)
	}
	if bundle.Freshness.AsOf.IsZero() || bundle.Confidence <= 0 {
		t.Fatalf("freshness/confidence = %+v / %v", bundle.Freshness, bundle.Confidence)
	}
}

func TestContextBundleFiltersObjectsWithoutReBACRelation(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	if err := store.SaveObject(ctx, Object{
		ID:           "mission-1",
		TenantID:     "tenant-1",
		ObjectType:   "mission",
		CanonicalKey: "mission-1",
		Label:        "Launch",
		EvidenceRefs: []Ref{{Kind: "knowledge_document", ID: "doc-mission"}},
	}); err != nil {
		t.Fatalf("save mission: %v", err)
	}
	if err := store.SaveObject(ctx, Object{
		ID:           "task-1",
		TenantID:     "tenant-1",
		ObjectType:   "task",
		CanonicalKey: "task-1",
		Label:        "Hidden task",
		EvidenceRefs: []Ref{{Kind: "knowledge_document", ID: "doc-task"}},
	}); err != nil {
		t.Fatalf("save task: %v", err)
	}
	if err := store.SaveLink(ctx, Link{
		ID:           "link-mission-task",
		TenantID:     "tenant-1",
		LinkType:     "has_task",
		FromObjectID: "mission-1",
		ToObjectID:   "task-1",
	}); err != nil {
		t.Fatalf("save link: %v", err)
	}
	if err := store.SaveFact(ctx, Fact{
		ID:           "fact-task",
		TenantID:     "tenant-1",
		Subject:      Ref{Kind: "task", ID: "task-1"},
		Predicate:    "status",
		Value:        "secret",
		EvidenceRefs: []Ref{{Kind: "knowledge_document", ID: "doc-task"}},
	}); err != nil {
		t.Fatalf("save fact: %v", err)
	}
	if err := store.SaveEvidence(ctx, Evidence{
		ID:         "doc-mission",
		TenantID:   "tenant-1",
		SourceRef:  Ref{Kind: "knowledge_document", ID: "doc-mission"},
		Summary:    "mission evidence",
		Confidence: 0.9,
	}); err != nil {
		t.Fatalf("save mission evidence: %v", err)
	}
	if err := store.SaveEvidence(ctx, Evidence{
		ID:         "doc-task",
		TenantID:   "tenant-1",
		SourceRef:  Ref{Kind: "knowledge_document", ID: "doc-task"},
		Summary:    "task evidence",
		Confidence: 0.9,
	}); err != nil {
		t.Fatalf("save task evidence: %v", err)
	}
	relations := rebac.NewMemoryStore()
	if err := relations.SaveRelationTuple(ctx, rebac.RelationTuple{
		ID:        "rel-mission-viewer",
		TenantID:  "tenant-1",
		Subject:   rebac.Ref{Kind: "human", ID: "klevin"},
		Relation:  "viewer",
		Object:    rebac.Ref{Kind: "mission", ID: "mission-1"},
		CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("save relation: %v", err)
	}
	service := NewService(store, WithReBACStore(relations), WithClock(func() time.Time {
		return time.Date(2026, 6, 19, 10, 0, 0, 0, time.UTC)
	}))

	bundle, err := service.BuildContextBundle(ctx, BuildContextBundleRequest{
		TenantID:     "tenant-1",
		Consumer:     "iris",
		Purpose:      "mission_assignment",
		ActorRef:     Ref{Kind: "human", ID: "klevin"},
		SubjectRefs:  []Ref{{Kind: "mission", ID: "mission-1"}},
		IncludeKinds: []string{"mission", "task"},
		MaxDepth:     1,
	})
	if err != nil {
		t.Fatalf("BuildContextBundle: %v", err)
	}

	if !containsObject(bundle.Objects, "mission-1") {
		t.Fatalf("mission should remain visible: %+v", bundle.Objects)
	}
	if containsObject(bundle.Objects, "task-1") {
		t.Fatalf("task without relation leaked into bundle: %+v", bundle.Objects)
	}
	if containsLink(bundle.Links, "link-mission-task") {
		t.Fatalf("link to unauthorized task leaked into bundle: %+v", bundle.Links)
	}
	if len(bundle.Facts) != 0 {
		t.Fatalf("facts for unauthorized task leaked: %+v", bundle.Facts)
	}
	if len(bundle.Evidence) != 1 || bundle.Evidence[0].ID != "doc-mission" {
		t.Fatalf("evidence = %+v, want only mission evidence", bundle.Evidence)
	}
	if len(bundle.BuildTrace.HiddenObjectIDs) != 0 {
		t.Fatalf("build trace leaked rebac-hidden task: %+v", bundle.BuildTrace)
	}
}

func TestContextBundleRejectsQueryOnlyAndExcludesSuggestedLinks(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	for _, object := range []Object{{ID: "a", TenantID: "tenant-1"}, {ID: "b", TenantID: "tenant-1"}} {
		if err := store.SaveObject(ctx, object); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.SaveLink(ctx, Link{ID: "suggested", TenantID: "tenant-1", FromObjectID: "a", ToObjectID: "b", Status: "suggested"}); err != nil {
		t.Fatal(err)
	}
	service := NewService(store)
	if _, err := service.BuildContextBundle(ctx, BuildContextBundleRequest{TenantID: "tenant-1", Consumer: "iris", Purpose: "research", ActorRef: Ref{Kind: "human", ID: "u"}, Query: "policy"}); !errors.Is(err, ErrQueryInvalid) {
		t.Fatalf("query-only BuildContextBundle error = %v, want ErrQueryInvalid", err)
	}
	bundle, err := service.BuildContextBundle(ctx, BuildContextBundleRequest{TenantID: "tenant-1", Consumer: "iris", Purpose: "research", ActorRef: Ref{Kind: "human", ID: "u"}, SubjectRefs: []Ref{{Kind: "object", ID: "a"}}, MaxDepth: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(bundle.Links) != 0 {
		t.Fatalf("suggested links leaked: %+v", bundle.Links)
	}
}

func TestContextBundleUsesCurrentSubgraphStoreForSeededRequest(t *testing.T) {
	store := &recordingCurrentSubgraphStore{
		objects: []Object{{ID: "mission-1", TenantID: "tenant-1", ObjectType: "mission"}},
	}
	service := NewService(store)

	bundle, err := service.BuildContextBundle(context.Background(), BuildContextBundleRequest{
		TenantID:    "tenant-1",
		Consumer:    "agent-runtime",
		Purpose:     "ontology.acceptance",
		ActorRef:    Ref{Kind: "agent", ID: "tangdou"},
		SubjectRefs: []Ref{{Kind: "mission", ID: "mission-1"}},
		MaxDepth:    1,
	})
	if err != nil {
		t.Fatalf("BuildContextBundle: %v", err)
	}
	if store.snapshotCalls != 0 || store.subgraphCalls != 1 {
		t.Fatalf("snapshot calls = %d, subgraph calls = %d", store.snapshotCalls, store.subgraphCalls)
	}
	if store.tenantID != "tenant-1" || store.maxDepth != 1 || len(store.subjectRefs) != 1 || store.subjectRefs[0].ID != "mission-1" {
		t.Fatalf("subgraph request tenant=%q refs=%+v depth=%d", store.tenantID, store.subjectRefs, store.maxDepth)
	}
	if len(bundle.Objects) != 1 || bundle.Objects[0].ID != "mission-1" {
		t.Fatalf("bundle objects = %+v", bundle.Objects)
	}
}

func TestContextBundleAppliesObjectVisibilityOnceAndConcealsDependentData(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	for _, object := range []Object{
		{ID: "mission-1", TenantID: "tenant-1", ObjectType: "mission", EvidenceRefs: []Ref{{Kind: "evidence", ID: "evidence-mission"}}},
		{ID: "task-secret", TenantID: "tenant-1", ObjectType: "task", EvidenceRefs: []Ref{{Kind: "evidence", ID: "evidence-secret"}}},
	} {
		if err := store.SaveObject(ctx, object); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.SaveLink(ctx, Link{ID: "link-secret", TenantID: "tenant-1", FromObjectID: "mission-1", ToObjectID: "task-secret"}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveFact(ctx, Fact{ID: "fact-secret", TenantID: "tenant-1", Subject: Ref{Kind: "task", ID: "task-secret"}, Predicate: "secret", Value: true}); err != nil {
		t.Fatal(err)
	}
	for _, item := range []Evidence{
		{ID: "evidence-mission", TenantID: "tenant-1"},
		{ID: "evidence-secret", TenantID: "tenant-1"},
	} {
		if err := store.SaveEvidence(ctx, item); err != nil {
			t.Fatal(err)
		}
	}
	visibility := &recordingObjectVisibility{visible: map[string]bool{"mission-1": true}}
	bundle, err := NewService(store, WithObjectVisibilityProvider(visibility)).BuildContextBundle(ctx, BuildContextBundleRequest{
		TenantID: "tenant-1", Consumer: "agent-runtime", Purpose: "task-read",
		ActorRef: Ref{Kind: "agent", ID: "agent-1"}, SubjectRefs: []Ref{{Kind: "mission", ID: "mission-1"}}, MaxDepth: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if visibility.calls != 1 || len(visibility.objects) != 2 {
		t.Fatalf("visibility calls=%d objects=%+v", visibility.calls, visibility.objects)
	}
	if len(bundle.Objects) != 1 || bundle.Objects[0].ID != "mission-1" || len(bundle.Links) != 0 || len(bundle.Facts) != 0 || len(bundle.Evidence) != 1 || bundle.Evidence[0].ID != "evidence-mission" {
		t.Fatalf("concealed data leaked: %+v", bundle)
	}
	if len(bundle.BuildTrace.HiddenObjectIDs) != 0 {
		t.Fatalf("concealed object id leaked through build trace: %+v", bundle.BuildTrace)
	}
}

func TestContextBundleFailsClosedWhenObjectVisibilityFails(t *testing.T) {
	store := NewMemoryStore()
	if err := store.SaveObject(context.Background(), Object{ID: "mission-1", TenantID: "tenant-1", ObjectType: "mission"}); err != nil {
		t.Fatal(err)
	}
	wantErr := errors.New("authorization unavailable")
	_, err := NewService(store, WithObjectVisibilityProvider(&recordingObjectVisibility{err: wantErr})).BuildContextBundle(context.Background(), BuildContextBundleRequest{
		TenantID: "tenant-1", Consumer: "agent-runtime", Purpose: "task-read",
		ActorRef: Ref{Kind: "agent", ID: "agent-1"}, SubjectRefs: []Ref{{Kind: "mission", ID: "mission-1"}},
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("BuildContextBundle error = %v, want %v", err, wantErr)
	}
}

func TestContextBundleIdentityBindsActorSubjectsAndSchema(t *testing.T) {
	store := NewMemoryStore()
	for _, object := range []Object{
		{ID: "mission-1", TenantID: "tenant-1", ObjectType: "mission"},
		{ID: "mission-2", TenantID: "tenant-1", ObjectType: "mission"},
	} {
		if err := store.SaveObject(context.Background(), object); err != nil {
			t.Fatal(err)
		}
	}
	schema := &fixedSchemaProvider{dictionary: SchemaDictionary{VersionHash: "schema-1"}}
	service := NewService(store, WithSchemaProvider(schema))
	request := BuildContextBundleRequest{
		TenantID: "tenant-1", Consumer: "agent-runtime", Purpose: "mission-read",
		ActorRef: Ref{Kind: "agent", ID: "agent-1"}, SubjectRefs: []Ref{{Kind: "mission", ID: "mission-1"}}, TokenBudget: 8000,
	}
	first, err := service.BuildContextBundle(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	request.ActorRef.ID = "agent-2"
	actorChanged, err := service.BuildContextBundle(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	request.ActorRef.ID = "agent-1"
	request.SubjectRefs = []Ref{{Kind: "mission", ID: "mission-2"}}
	subjectChanged, err := service.BuildContextBundle(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	schema.dictionary.VersionHash = "schema-2"
	schemaChanged, err := service.BuildContextBundle(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if first.BundleID == actorChanged.BundleID || first.BundleID == subjectChanged.BundleID || subjectChanged.BundleID == schemaChanged.BundleID {
		t.Fatalf("bundle IDs did not bind inputs: %q %q %q %q", first.BundleID, actorChanged.BundleID, subjectChanged.BundleID, schemaChanged.BundleID)
	}
}

func TestContextBundleTokenBudgetTruncatesDeterministically(t *testing.T) {
	store := NewMemoryStore()
	if err := store.SaveObject(context.Background(), Object{ID: "mission-1", TenantID: "tenant-1", ObjectType: "mission", Label: "Mission"}); err != nil {
		t.Fatal(err)
	}
	for index := range 40 {
		if err := store.SaveFact(context.Background(), Fact{
			ID: fmt.Sprintf("fact-%02d", index), TenantID: "tenant-1", Subject: Ref{Kind: "mission", ID: "mission-1"},
			Predicate: "note", Value: strings.Repeat("x", 200),
		}); err != nil {
			t.Fatal(err)
		}
	}
	request := BuildContextBundleRequest{
		TenantID: "tenant-1", Consumer: "agent-runtime", Purpose: "mission-read",
		ActorRef: Ref{Kind: "agent", ID: "agent-1"}, SubjectRefs: []Ref{{Kind: "mission", ID: "mission-1"}}, TokenBudget: 512,
	}
	now := time.Date(2026, 8, 22, 4, 0, 0, 0, time.UTC)
	first, err := NewService(store, WithClock(func() time.Time { return now })).BuildContextBundle(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewService(store, WithClock(func() time.Time { return now })).BuildContextBundle(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if !first.Partial || first.PartialReason != "token_budget_exceeded" || first.Truncated.Facts == 0 || len(first.Facts) >= 40 || !reflect.DeepEqual(first, second) {
		t.Fatalf("budgeted bundles = %+v / %+v", first, second)
	}
}

type fixedSchemaProvider struct {
	dictionary SchemaDictionary
}

func (p *fixedSchemaProvider) SchemaDictionary(context.Context, string) (SchemaDictionary, error) {
	return p.dictionary, nil
}

type recordingObjectVisibility struct {
	calls   int
	request BuildContextBundleRequest
	objects []Object
	visible map[string]bool
	err     error
}

func (p *recordingObjectVisibility) VisibleObjectIDs(_ context.Context, request BuildContextBundleRequest, objects []Object) (map[string]bool, error) {
	p.calls++
	p.request = request
	p.objects = append([]Object(nil), objects...)
	return p.visible, p.err
}

type recordingCurrentSubgraphStore struct {
	objects       []Object
	snapshotCalls int
	subgraphCalls int
	tenantID      string
	subjectRefs   []Ref
	maxDepth      int
}

func (s *recordingCurrentSubgraphStore) Snapshot(context.Context, string) ([]Object, []Link, []Fact, []Evidence, error) {
	s.snapshotCalls++
	return nil, nil, nil, nil, ErrQueryInvalid
}

func (s *recordingCurrentSubgraphStore) SnapshotCurrentSubgraph(_ context.Context, tenantID string, subjectRefs []Ref, maxDepth int, _ CurrentSubgraphLimits) ([]Object, []Link, []Fact, []Evidence, error) {
	s.subgraphCalls++
	s.tenantID = tenantID
	s.subjectRefs = append([]Ref(nil), subjectRefs...)
	s.maxDepth = maxDepth
	return append([]Object(nil), s.objects...), nil, nil, nil, nil
}

type fixedContextPolicy struct {
	decision PermissionDecision
}

func (p fixedContextPolicy) DecideContext(ctx context.Context, req BuildContextBundleRequest, objects []Object) (PermissionDecision, error) {
	return p.decision, nil
}

func containsObject(objects []Object, id string) bool {
	for _, object := range objects {
		if object.ID == id {
			return true
		}
	}
	return false
}

func findObject(t *testing.T, objects []Object, id string) Object {
	t.Helper()
	for _, object := range objects {
		if object.ID == id {
			return object
		}
	}
	t.Fatalf("missing object %s in %+v", id, objects)
	return Object{}
}

func containsLink(links []Link, id string) bool {
	for _, link := range links {
		if link.ID == id {
			return true
		}
	}
	return false
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
