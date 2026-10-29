package knowledgeinstances

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"orrery_v2/internal/app/actioncontract"
	"orrery_v2/internal/app/contextgraph"
	"orrery_v2/internal/app/ontology"
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/migration"
)

var allowCommit = func(context.Context, db.Tx) error { return nil }

func instanceDatabase(t *testing.T, postgres bool) *db.Store {
	t.Helper()
	ctx := context.Background()
	cfg := db.Config{Driver: db.DriverMemory, Name: "knowledge-instances-" + t.Name()}
	if postgres {
		raw := os.Getenv("ORRERY_TEST_POSTGRES_URL")
		if raw == "" {
			t.Skip("isolated PostgreSQL URL is not configured")
		}
		u, err := url.Parse(raw)
		if err != nil || !strings.HasPrefix(u.Query().Get("search_path"), "orrery_inference_test_") {
			t.Fatal("an isolated inference test schema is required")
		}
		cfg = db.Config{Driver: db.DriverPostgres, URL: raw}
		admin, err := db.OpenRegistry(ctx, db.RegistryConfig{Databases: map[string]db.Config{db.RoleCore: cfg}})
		if err != nil {
			t.Fatal(err)
		}
		store, err := admin.Store(db.RoleCore)
		if err != nil {
			t.Fatal(err)
		}
		schema := fmt.Sprintf("orrery_inference_test_instances_%d", time.Now().UnixNano())
		if _, err := store.Exec(ctx, `CREATE SCHEMA "`+schema+`"`); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { store.Exec(context.Background(), `DROP SCHEMA "`+schema+`" CASCADE`); admin.Close() })
		query := u.Query()
		query.Set("search_path", schema)
		u.RawQuery = query.Encode()
		cfg.URL = u.String()
	}
	registry, err := db.OpenRegistry(ctx, db.RegistryConfig{Databases: map[string]db.Config{db.RoleCore: cfg}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { registry.Close() })
	c := migration.NewCoordinator(registry)
	for _, register := range []func(*migration.Coordinator) error{ontology.RegisterMigrations, contextgraph.RegisterMigrations, actioncontract.RegisterMigrations, RegisterMigrations} {
		if err := register(c); err != nil {
			t.Fatal(err)
		}
	}
	if err := c.UpRole(ctx, db.RoleCore); err != nil {
		t.Fatal(err)
	}
	core, err := registry.Store(db.RoleCore)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range actioncontract.DefaultActionTypes() {
		if err := actioncontract.NewService(actioncontract.NewPlatformRepository(core)).RegisterActionType(ctx, a); err != nil {
			t.Fatal(err)
		}
	}
	if err := contextgraph.NewPlatformStore(core).SaveObject(ctx, contextgraph.Object{ID: "doc-root", TenantID: "tenant-a", ObjectType: "knowledge_document", Label: "Source", OwnerAccessRef: instanceSource().Access}); err != nil {
		t.Fatal(err)
	}
	return core
}

func instanceSource() Source {
	return Source{TenantID: "tenant-a", RootID: "doc-root", KnowledgeBaseID: "kb", DocumentID: "provider-doc", Revision: 1, ContentHash: "sha256:fixture", Content: "Acme is a company in CN. Annual amount 100 USD.", Access: contextgraph.AccessRef{Kind: "organization", ID: "tenant-a", TenantID: "tenant-a"}}
}

func instanceExtraction(name string, existing string, amount float64) Extraction {
	return Extraction{Reason: "The published document explicitly states these values.", Entities: []EntityInput{{Ref: "entity", ExistingID: existing, Kind: "company", Name: name, Quote: name + " is a company in CN", Properties: []Property{{Predicate: "country", Value: "CN", Quote: name + " is a company in CN"}, {Predicate: "amount", Value: amount, Quote: fmt.Sprintf("Annual amount %.0f USD", amount)}}}}}
}

func publishWatch(t *testing.T, s *Store, id string) Watch {
	t.Helper()
	w, err := (&WatchStore{Store: s.Core}).Publish(context.Background(), Watch{TenantID: "tenant-a", ID: id, AgentID: "agent", RootID: "doc-root", Active: true, PublishedAt: time.Now().UTC()}, 0, allowCommit)
	if err != nil {
		t.Fatal(err)
	}
	return w
}

func preparedBatch(t *testing.T, s *Store, w Watch, source Source, extraction Extraction, id string) Batch {
	t.Helper()
	ctx := context.Background()
	candidates, err := s.Candidates(ctx, w.TenantID, source.Access)
	if err != nil {
		t.Fatal(err)
	}
	previous, err := s.Claims(ctx, w.TenantID, w.ID)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := Prepare(id, w, source, "schema", "authorization", extraction, candidates, previous, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.SavePrepared(ctx, Batch{ID: id, TenantID: w.TenantID, WatchID: w.ID, Plan: plan})
	if err != nil {
		t.Fatal(err)
	}
	b, err = s.SaveDecision(ctx, b, Decision{Decision: "approve", Reason: "Exact source quotes and identities checked.", ModelID: "test-model", PlanHash: Digest(plan)})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func applyBatch(t *testing.T, s *Store, b Batch) Receipt {
	t.Helper()
	r, err := s.Apply(context.Background(), b, CommitContext{AgentID: "agent", Now: time.Now().UTC(), Guard: allowCommit})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func testInstanceLifecycle(t *testing.T, postgres bool) {
	ctx := context.Background()
	s := &Store{Core: instanceDatabase(t, postgres)}
	watch := publishWatch(t, s, "source-a")
	source := instanceSource()
	b := preparedBatch(t, s, watch, source, instanceExtraction("Acme", "", 100), "batch-1")
	receipt := applyBatch(t, s, b)
	if len(receipt.EntityIDs) != 1 || receipt.ClaimCount != 4 {
		t.Fatalf("unexpected receipt: %+v", receipt)
	}
	id := receipt.EntityIDs[0]
	replayed, err := s.Apply(ctx, b, CommitContext{AgentID: "agent", Guard: allowCommit})
	if err != nil || Digest(replayed) != Digest(receipt) {
		t.Fatalf("duplicate execution changed receipt: %+v %v", replayed, err)
	}
	// Another document supports the same identity with a conflicting amount.
	other := publishWatch(t, s, "source-b")
	source.Revision = 2
	source.ContentHash = "revision-2"
	source.Content = strings.ReplaceAll(source.Content, "100", "200")
	applyBatch(t, s, preparedBatch(t, s, other, source, instanceExtraction("Acme", id, 200), "batch-2"))
	entities, _ := s.Candidates(ctx, source.TenantID, source.Access)
	if len(entities) != 1 || len(entities[0].Conflicts) != 1 || entities[0].Conflicts[0] != "amount" || entities[0].Properties["amount"] != nil {
		t.Fatalf("conflicting amounts were silently selected: %+v", entities)
	}
	// First document withdraws its claims; the other source and identity survive.
	source.Revision = 3
	source.ContentHash = "revision-3"
	source.Content = "No entity information remains."
	applyBatch(t, s, preparedBatch(t, s, watch, source, Extraction{Reason: "This revision no longer describes an entity."}, "batch-3"))
	entities, _ = s.Candidates(ctx, source.TenantID, source.Access)
	if entities[0].Properties["amount"] != float64(200) || len(entities[0].Conflicts) != 0 || entities[0].Status != "active" {
		t.Fatalf("foreign source damaged: %+v", entities)
	}
	claims, _ := s.Claims(ctx, source.TenantID, watch.ID)
	if len(claims) != 0 {
		t.Fatal("withdrawn source assertions remain")
	}
	// Withdrawal deletes current assertion rows as well as current fact rows.
	var assertions int
	if err := s.Core.QueryRow(ctx, `SELECT COUNT(*) FROM context_graph_fact_assertions WHERE source_stream_id=?`, claimStream(b.Plan)).Scan(&assertions); err != nil || assertions != 0 {
		t.Fatalf("withdrawn facts remain inference premises: %d %v", assertions, err)
	}
	applyBatch(t, s, preparedBatch(t, s, other, source, Extraction{Reason: "Second source withdraws its assertions too."}, "batch-4"))
	entities, _ = s.Candidates(ctx, source.TenantID, source.Access)
	if entities[0].Status != "withdrawn" {
		t.Fatal("unbacked identity still active")
	}
	// Historical retries retain the original result after later revisions.
	replayed, err = s.Apply(ctx, b, CommitContext{AgentID: "agent", Guard: allowCommit})
	if err != nil || Digest(replayed) != Digest(receipt) {
		t.Fatalf("historical receipt changed: %v", err)
	}
	for table, want := range map[string]int{"knowledge_instances": 1, "knowledge_instance_claims": 0, "ontology_document_receipts": 4, "action_runs": 4} {
		var count int
		if err := s.Core.QueryRow(ctx, `SELECT COUNT(*) FROM `+table).Scan(&count); err != nil || count != want {
			t.Fatalf("%s: count=%d want=%d err=%v", table, count, want, err)
		}
	}
}

func TestDocumentInstancesLifecycle(t *testing.T)         { testInstanceLifecycle(t, false) }
func TestPostgresDocumentInstancesLifecycle(t *testing.T) { testInstanceLifecycle(t, true) }

func TestDocumentInstancesRollbackAndForeignOwnership(t *testing.T) {
	for _, scenario := range []string{"disabled_action", "source_changed", "foreign_object", "foreign_claim", "paused_watch"} {
		t.Run(scenario, func(t *testing.T) {
			ctx := context.Background()
			s := &Store{Core: instanceDatabase(t, false)}
			watch := publishWatch(t, s, "watch")
			source := instanceSource()
			first := preparedBatch(t, s, watch, source, instanceExtraction("Acme", "", 100), "first")
			r := applyBatch(t, s, first)
			next := preparedBatch(t, s, watch, source, instanceExtraction("Acme", r.EntityIDs[0], 100), "second")
			guard := allowCommit
			switch scenario {
			case "disabled_action":
				s.Core.Exec(ctx, `UPDATE action_types SET enabled=FALSE WHERE id=?`, ActionID)
			case "source_changed":
				guard = func(context.Context, db.Tx) error { return ErrConflict }
			case "foreign_object":
				s.Core.Exec(ctx, `UPDATE context_graph_objects SET source_stream_id='foreign' WHERE id=?`, r.EntityIDs[0])
			case "foreign_claim":
				s.Core.Exec(ctx, `UPDATE context_graph_facts SET source_stream_id='foreign' WHERE id=?`, next.Plan.Previous[0].ID)
			case "paused_watch":
				watch.Active = false
				if _, err := (&WatchStore{Store: s.Core}).Publish(ctx, watch, watch.Version, allowCommit); err != nil {
					t.Fatal(err)
				}
			}
			before := map[string]string{}
			for _, table := range []string{"knowledge_instances", "knowledge_instance_claims", "context_graph_objects", "context_graph_facts", "context_graph_links", "context_graph_fact_assertions", "context_graph_evidence", "ontology_document_receipts", "action_runs"} {
				before[table] = tableDigest(t, s.Core, table)
			}
			if _, err := s.Apply(ctx, next, CommitContext{AgentID: "agent", Guard: guard}); err == nil {
				t.Fatal("invalid command committed")
			}
			for table, hash := range before {
				if tableDigest(t, s.Core, table) != hash {
					t.Fatalf("partial write to %s", table)
				}
			}
		})
	}
}

func tableDigest(t *testing.T, core *db.Store, table string) string {
	t.Helper()
	rows, err := core.Query(context.Background(), `SELECT * FROM `+table)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	columns, _ := rows.Columns()
	var result [][]any
	for rows.Next() {
		values := make([]any, len(columns))
		dest := make([]any, len(columns))
		for i := range values {
			dest[i] = &values[i]
		}
		if err := rows.Scan(dest...); err != nil {
			t.Fatal(err)
		}
		result = append(result, values)
	}
	return Digest(result)
}

func TestDocumentInstancesGroundingAndIdentityBoundaries(t *testing.T) {
	source := instanceSource()
	watch := Watch{TenantID: source.TenantID, ID: "watch", AgentID: "agent", RootID: source.RootID, Version: 1, Active: true}
	for _, scenario := range []string{"invented_quote", "unoffered_id", "wrong_kind", "invalid_number", "unknown_property", "duplicate_ref", "cross_scope"} {
		t.Run(scenario, func(t *testing.T) {
			extraction := instanceExtraction("Acme", "", 100)
			var candidates []Entity
			switch scenario {
			case "invented_quote":
				extraction.Entities[0].Quote = "A fabricated company"
			case "unoffered_id":
				extraction.Entities[0].ExistingID = "unoffered"
			case "wrong_kind":
				extraction.Entities[0].Kind = "organization"
			case "invalid_number":
				extraction.Entities[0].Properties[1].Value = float64(999)
			case "unknown_property":
				extraction.Entities[0].Properties[0].Predicate = "system_prompt"
			case "duplicate_ref":
				extraction.Entities = append(extraction.Entities, extraction.Entities[0])
			case "cross_scope":
				extraction.Entities[0].ExistingID = "private"
				candidates = []Entity{{ID: "private", TenantID: source.TenantID, Access: contextgraph.AccessRef{Kind: "person", ID: "private"}, Kind: "company"}}
			}
			if _, err := Prepare("batch", watch, source, "schema", "auth", extraction, candidates, nil, time.Now().UTC()); !errors.Is(err, ErrInvalid) {
				t.Fatalf("invalid extraction accepted: %v", err)
			}
		})
	}
}

func TestDocumentInstancesCreateReferencedEntitiesAndWithdrawOwnedRelation(t *testing.T) {
	s := &Store{Core: instanceDatabase(t, false)}
	watch := publishWatch(t, s, "related-watch")
	source := instanceSource()
	source.Content = "Acme supplies Beta."
	extraction := Extraction{Reason: "Both companies and the supplied relationship are explicit.", Entities: []EntityInput{
		{Ref: "a", Name: "Acme", Kind: "company", Quote: source.Content}, {Ref: "b", Name: "Beta", Kind: "company", Quote: source.Content},
	}, Relations: []RelationInput{{From: "a", To: "b", Relation: "supplies", Quote: source.Content}}}
	first := preparedBatch(t, s, watch, source, extraction, "relation-1")
	r := applyBatch(t, s, first)
	if len(r.EntityIDs) != 2 {
		t.Fatal("local entity references were not resolved")
	}
	var count int
	if err := s.Core.QueryRow(context.Background(), `SELECT COUNT(*) FROM context_graph_links WHERE link_type=?`, RelatedLink).Scan(&count); err != nil || count != 1 {
		t.Fatalf("new endpoints were not linked: %d %v", count, err)
	}
	for i, e := range first.Plan.Entities {
		extraction.Entities[i].ExistingID = e.ID
	}
	extraction.Relations = nil
	source.Revision++
	applyBatch(t, s, preparedBatch(t, s, watch, source, extraction, "relation-2"))
	if err := s.Core.QueryRow(context.Background(), `SELECT COUNT(*) FROM context_graph_links WHERE link_type=?`, RelatedLink).Scan(&count); err != nil || count != 0 {
		t.Fatalf("removed owned relation survived: %d %v", count, err)
	}
}

func TestPostgresDocumentInstancesConcurrentReplay(t *testing.T) {
	s := &Store{Core: instanceDatabase(t, true)}
	watch := publishWatch(t, s, "concurrent")
	batch := preparedBatch(t, s, watch, instanceSource(), instanceExtraction("Acme", "", 100), "concurrent-batch")
	var wg sync.WaitGroup
	results := make(chan Receipt, 2)
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, err := s.Apply(context.Background(), batch, CommitContext{AgentID: "agent", Guard: allowCommit})
			results <- r
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if Digest(<-results) != Digest(<-results) {
		t.Fatal("concurrent duplicate produced different receipts")
	}
}
