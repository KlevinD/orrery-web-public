package contextgraph

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/migration"
)

type temporalBundleTestStore interface {
	Store
	TemporalSubgraphStore
	SaveObject(context.Context, Object) error
	SaveLink(context.Context, Link) error
	SaveFact(context.Context, Fact) error
	SaveEvidence(context.Context, Evidence) error
}

func forTemporalBundleStores(t *testing.T, check func(*testing.T, temporalBundleTestStore)) {
	t.Helper()
	t.Run("memory", func(t *testing.T) { check(t, NewMemoryStore()) })
	for _, driver := range []db.Driver{db.DriverMemory, db.DriverPostgres} {
		name := string(driver)
		if driver == db.DriverMemory {
			name = "sqlite"
		}
		t.Run(name, func(t *testing.T) {
			config := db.Config{Driver: driver, Name: fmt.Sprintf("context-regression-%x", sha256.Sum256([]byte(t.Name())))[:40]}
			if driver == db.DriverPostgres {
				config.URL = os.Getenv("ORRERY_TEST_POSTGRES_URL")
				if config.URL == "" {
					t.Skip("ORRERY_TEST_POSTGRES_URL is not set")
				}
				admin, err := db.Open(context.Background(), config)
				if err != nil {
					t.Fatal(err)
				}
				schema := fmt.Sprintf("context_regression_%x", sha256.Sum256([]byte(t.Name()+time.Now().String())))[:43]
				if _, err := admin.Exec(context.Background(), "CREATE SCHEMA "+schema); err != nil {
					admin.Close()
					t.Fatal(err)
				}
				t.Cleanup(func() { _, _ = admin.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE"); _ = admin.Close() })
				parsed, err := url.Parse(config.URL)
				if err != nil {
					t.Fatal(err)
				}
				query := parsed.Query()
				query.Set("search_path", schema)
				parsed.RawQuery = query.Encode()
				config.URL = parsed.String()
			}
			database, err := db.Open(context.Background(), config)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = database.Close() })
			if err := migration.NewRunner(contextGraphCompleteStoreTestMigrations()).Up(context.Background(), database); err != nil {
				t.Fatal(err)
			}
			check(t, NewPlatformStore(database))
		})
	}
}

func regressionRequest(business, known time.Time) BuildContextBundleRequest {
	return BuildContextBundleRequest{TenantID: "tenant-1", Consumer: "agent-runtime", Purpose: "mission-read", ActorRef: Ref{Kind: "agent", ID: "agent-1"}, SubjectRefs: []Ref{{Kind: "task", ID: "object-1"}}, BusinessAsOf: business, KnownAt: known}
}

func TestContextBundleHistoricalCoordinatesSelectContent(t *testing.T) {
	forTemporalBundleStores(t, func(t *testing.T, store temporalBundleTestStore) {
		ctx := context.Background()
		jan := temporalTestTime(2026, time.January, 1)
		feb := temporalTestTime(2026, time.February, 1)
		mar := temporalTestTime(2026, time.March, 1)
		apr := temporalTestTime(2026, time.April, 1)
		for _, object := range []Object{
			temporalVersionTestObject("January", 1, jan, nil, jan),
			temporalVersionTestObject("February", 2, feb, nil, feb),
			temporalVersionTestObject("Corrected January", 3, jan, nil, mar),
			temporalVersionTestObject("Ended February", 4, feb, &feb, apr),
		} {
			object.ID, object.CanonicalKey = "object-1", "object-1"
			object.OwnerAccessRef = AccessRef{Kind: "mission", ID: "mission-1", TenantID: "tenant-1"}
			if err := store.SaveObject(ctx, object); err != nil {
				t.Fatal(err)
			}
		}
		other := temporalVersionTestObject("Other tenant secret", 1, jan, nil, jan)
		other.ID, other.CanonicalKey = "object-1", "object-1"
		other.TenantID = "tenant-2"
		other.OwnerAccessRef = AccessRef{Kind: "mission", ID: "mission-2", TenantID: "tenant-2"}
		if err := store.SaveObject(ctx, other); err != nil {
			t.Fatal(err)
		}
		for _, test := range []struct {
			name            string
			business, known time.Time
			label           string
		}{
			{"original knowledge", jan.Add(24 * time.Hour), jan.Add(24 * time.Hour), "January"},
			{"late correction", jan.Add(24 * time.Hour), mar.Add(24 * time.Hour), "Corrected January"},
			{"business start wins", feb.Add(24 * time.Hour), mar.Add(24 * time.Hour), "February"},
			{"ended interval never resurfaces", feb.Add(24 * time.Hour), apr.Add(24 * time.Hour), ""},
			{"not yet known", jan.Add(24 * time.Hour), jan.Add(-time.Hour), ""},
		} {
			t.Run(test.name, func(t *testing.T) {
				bundle, err := NewService(store).BuildContextBundle(ctx, regressionRequest(test.business, test.known))
				if err != nil {
					t.Fatal(err)
				}
				if test.label == "" {
					if len(bundle.Objects) != 0 {
						t.Fatalf("unexpected historical objects: %+v", bundle.Objects)
					}
				} else if len(bundle.Objects) != 1 || bundle.Objects[0].Label != test.label {
					t.Fatalf("historical objects = %+v, want %q", bundle.Objects, test.label)
				}
				if !bundle.Freshness.BusinessAsOf.Equal(test.business) || !bundle.Freshness.KnownAt.Equal(test.known) {
					t.Fatalf("incorrect freshness: %+v", bundle.Freshness)
				}
			})
		}
	})
}

func TestContextBundleHistoricalTopologyFactsAndEvidence(t *testing.T) {
	forTemporalBundleStores(t, func(t *testing.T, store temporalBundleTestStore) {
		ctx := context.Background()
		jan, feb := temporalTestTime(2026, time.January, 1), temporalTestTime(2026, time.February, 1)
		access := AccessRef{Kind: "mission", ID: "mission-1", TenantID: "tenant-1"}
		for _, id := range []string{"object-1", "old", "new", "grand"} {
			if err := store.SaveObject(ctx, Object{ID: id, TenantID: "tenant-1", ObjectType: "task", CanonicalKey: id, OwnerAccessRef: access}); err != nil {
				t.Fatal(err)
			}
		}
		for i, target := range []string{"old", "new"} {
			start := []time.Time{jan, feb}[i]
			metadata := TemporalMetadata{Mode: "state_interval", BusinessValidFrom: start, SourceRecordedAt: start}
			if err := store.SaveLink(ctx, Link{ID: "a-parent", TenantID: "tenant-1", LinkType: "related", FromObjectID: "object-1", ToObjectID: target, SourceAccessRef: access, SourceVersion: int64(i + 1), Temporal: metadata, UpdatedAt: start}); err != nil {
				t.Fatal(err)
			}
			if err := store.SaveFact(ctx, Fact{ID: "status", TenantID: "tenant-1", Subject: Ref{Kind: "task", ID: target}, Predicate: "status", Value: target, SourceAccessRef: access, SubjectAccessRef: access, SourceVersion: int64(i + 1), Temporal: metadata, EvidenceRefs: []Ref{{Kind: "evidence", ID: "known"}, {Kind: "evidence", ID: "future"}}, UpdatedAt: start}); err != nil {
				t.Fatal(err)
			}
		}
		if err := store.SaveLink(ctx, Link{ID: "b-grandchild", TenantID: "tenant-1", LinkType: "related", FromObjectID: "old", ToObjectID: "grand", SourceAccessRef: access}); err != nil {
			t.Fatal(err)
		}
		for _, evidence := range []Evidence{
			{ID: "known", TenantID: "tenant-1", SourceAccessRef: access, ObservedAt: jan},
			{ID: "future", TenantID: "tenant-1", SourceAccessRef: access, ObservedAt: feb},
		} {
			if err := store.SaveEvidence(ctx, evidence); err != nil {
				t.Fatal(err)
			}
		}
		request := regressionRequest(jan.Add(time.Hour), jan.Add(time.Hour))
		request.MaxDepth = 1
		bundle, err := NewService(store).BuildContextBundle(ctx, request)
		if err != nil {
			t.Fatal(err)
		}
		if len(bundle.Objects) != 2 || !containsObject(bundle.Objects, "old") || containsObject(bundle.Objects, "new") || containsObject(bundle.Objects, "grand") {
			t.Fatalf("wrong historical neighborhood: %+v", bundle.Objects)
		}
		if len(bundle.Links) != 1 || bundle.Links[0].ToObjectID != "old" {
			t.Fatalf("wrong links: %+v", bundle.Links)
		}
		if len(bundle.Facts) != 1 || bundle.Facts[0].Subject.ID != "old" || bundle.Facts[0].Value != "old" {
			t.Fatalf("wrong facts: %+v", bundle.Facts)
		}
		if len(bundle.Evidence) != 1 || bundle.Evidence[0].ID != "known" {
			t.Fatalf("future evidence returned: %+v", bundle.Evidence)
		}
		request.MaxDepth = 2
		bundle, err = NewService(store).BuildContextBundle(ctx, request)
		if err != nil || !containsObject(bundle.Objects, "grand") {
			t.Fatalf("two-hop historical neighborhood = %+v, %v", bundle.Objects, err)
		}
	})
}

func TestContextBundleMaskedFactCannotDiscloseValueOrEvidence(t *testing.T) {
	forTemporalBundleStores(t, func(t *testing.T, store temporalBundleTestStore) {
		ctx := context.Background()
		access := AccessRef{Kind: "mission", ID: "mission-1", TenantID: "tenant-1"}
		for _, id := range []string{"object-1", "object-2"} {
			if err := store.SaveObject(ctx, Object{ID: id, TenantID: "tenant-1", ObjectType: "task", CanonicalKey: id, Properties: map[string]any{"budget": "confidential-budget", "public": "visible"}, OwnerAccessRef: access}); err != nil {
				t.Fatal(err)
			}
			fact := Fact{ID: id + "-budget", TenantID: "tenant-1", Subject: Ref{Kind: "task", ID: id}, Predicate: "budget", Value: "confidential-budget", SourceAccessRef: access, SubjectAccessRef: access}
			fact.SourceStreamID = "regression:" + fact.ID
			if id == "object-1" {
				fact.EvidenceRefs = []Ref{{Kind: "evidence", ID: "secret-evidence"}}
			} else {
				fact.Value = "other-object-budget"
			}
			if err := store.SaveFact(ctx, fact); err != nil {
				t.Fatal(err)
			}
		}
		if err := store.SaveEvidence(ctx, Evidence{ID: "secret-evidence", TenantID: "tenant-1", Summary: "confidential-budget", SourceAccessRef: access}); err != nil {
			t.Fatal(err)
		}
		if err := store.SaveFact(ctx, Fact{ID: "public", TenantID: "tenant-1", Subject: Ref{Kind: "task", ID: "object-1"}, Predicate: "public", Value: "visible", SourceStreamID: "regression:public", SourceAccessRef: access, SubjectAccessRef: access}); err != nil {
			t.Fatal(err)
		}
		policy := fixedContextPolicy{decision: PermissionDecision{MaskedFields: map[string][]string{"object-1": {" budget "}}}}
		service := NewService(store, WithPolicyProvider(policy))
		request := regressionRequest(time.Time{}, time.Time{})
		bundle, err := service.BuildContextBundle(ctx, request)
		if err != nil {
			t.Fatal(err)
		}
		encoded, err := json.Marshal(bundle)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(encoded), "confidential-budget") || len(bundle.Facts) != 1 || bundle.Facts[0].Predicate != "public" || len(bundle.Evidence) != 0 {
			t.Fatalf("masked content escaped: %s", encoded)
		}
		request.SubjectRefs = append(request.SubjectRefs, Ref{Kind: "task", ID: "object-2"})
		bundle, err = service.BuildContextBundle(ctx, request)
		if err != nil || len(bundle.Facts) != 2 {
			t.Fatalf("other object's same predicate was masked: %+v, %v", bundle.Facts, err)
		}
		if len(bundle.BuildTrace.MaskedFields) != 1 || bundle.BuildTrace.MaskedFields[0] != "object-1.budget" {
			t.Fatalf("mask trace = %+v", bundle.BuildTrace)
		}
	})
}

func TestContextBundleHistoricalReadFailsClosedWithoutBoundedStore(t *testing.T) {
	store := &recordingCurrentSubgraphStore{}
	_, err := NewService(store).BuildContextBundle(context.Background(), regressionRequest(temporalTestTime(2026, time.January, 1), time.Time{}))
	if !errors.Is(err, ErrQueryInvalid) || store.snapshotCalls != 0 || store.subgraphCalls != 0 {
		t.Fatalf("unsupported temporal read used current or unbounded data: %v, %+v", err, store)
	}
}

func TestTemporalSubgraphHonorsLimits(t *testing.T) {
	fixture := newPlatformQueryFixture(t)
	now := time.Now().UTC()
	for _, test := range []struct {
		name   string
		limits CurrentSubgraphLimits
		depth  int
	}{
		{"objects", CurrentSubgraphLimits{Objects: 1, Links: 400, Facts: 1000, Evidence: 400}, 1},
		{"links", CurrentSubgraphLimits{Objects: 200, Links: 1, Facts: 1000, Evidence: 400}, 1},
		{"facts", CurrentSubgraphLimits{Objects: 200, Links: 400, Facts: 1, Evidence: 400}, 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, _, _, _, err := fixture.graph.SnapshotSubgraphAsOf(context.Background(), fixture.tenant, []Ref{{ID: "root"}}, test.depth, test.limits, now, now)
			if !errors.Is(err, ErrQueryBudgetExceeded) {
				t.Fatalf("unbounded temporal read: %v", err)
			}
		})
	}
}

func TestTemporalSubgraphDoesNotResurfaceUnresolvedCorrection(t *testing.T) {
	fixture := newPlatformQueryFixture(t)
	ctx := context.Background()
	jan := temporalTestTime(2026, time.January, 1)
	for i := range 2 {
		object := Object{ID: "root", TenantID: fixture.tenant, ObjectType: "mission", CanonicalKey: "mission:root", SourceVersion: int64(i + 1), UpdatedAt: jan.Add(time.Duration(i) * time.Hour), Temporal: TemporalMetadata{Mode: "state_interval", BusinessValidFrom: jan, SourceRecordedAt: jan.Add(time.Duration(i) * time.Hour)}}
		if i == 0 {
			object.OwnerAccessRef = fixture.allowed
		}
		if err := fixture.graph.SaveObject(ctx, object); err != nil {
			t.Fatal(err)
		}
	}
	// Access-reference backfills can invalidate a stored assertion independently
	// of its JSON payload. Selection must honor the canonical access columns.
	if _, err := fixture.graph.exec(ctx, "UPDATE context_graph_resource_versions SET access_state = 'unresolved' WHERE tenant_id = ? AND resource_id = 'root' AND source_version = 2", fixture.tenant); err != nil {
		t.Fatal(err)
	}
	objects, _, _, _, err := fixture.graph.SnapshotSubgraphAsOf(ctx, fixture.tenant, []Ref{{ID: "root"}}, 0, DefaultCurrentSubgraphLimits(), jan.Add(3*time.Hour), jan.Add(3*time.Hour))
	if err != nil || len(objects) != 0 {
		t.Fatalf("unresolved correction resurfaced resolved content: %+v, %v", objects, err)
	}
	reader := temporalSubgraphReader{store: fixture.graph, tenant: fixture.tenant, remainingVersions: 1}
	if _, err := reader.versions(ctx, temporalResourceObject, []string{"root"}); !errors.Is(err, ErrQueryBudgetExceeded) {
		t.Fatalf("history materialization exceeded its budget: %v", err)
	}
}

func TestTemporalSubgraphDoesNotMaterializeUnrelatedHistory(t *testing.T) {
	fixture := newPlatformQueryFixture(t)
	ctx := context.Background()
	// These unrelated rows deliberately have no decodable resource identity.
	// An unbounded tenant snapshot would either load all of them or fail decoding.
	_, err := fixture.graph.exec(ctx, `WITH RECURSIVE n(x) AS (SELECT 1 UNION ALL SELECT x+1 FROM n WHERE x < 6001)
INSERT INTO context_graph_resource_versions (tenant_id, resource_kind, resource_id, assertion_id, payload_json)
SELECT ?, 'object', 'unrelated-' || x, 'unrelated-' || x, '{}' FROM n`, fixture.tenant)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	objects, _, _, _, err := fixture.graph.SnapshotSubgraphAsOf(ctx, fixture.tenant, []Ref{{ID: "root"}}, 0, DefaultCurrentSubgraphLimits(), now, now)
	if err != nil || len(objects) != 1 || objects[0].ID != "root" {
		t.Fatalf("unrelated history affected bounded read: %+v, %v", objects, err)
	}
}
