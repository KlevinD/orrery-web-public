package knowledgeontology

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
	"orrery_v2/internal/app/knowledgeinstances"
	"orrery_v2/internal/app/ontology"
	"orrery_v2/internal/knowledge/provider"
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/migration"
)

func lifecycleDatabase(t *testing.T, postgres bool) *db.Store {
	t.Helper()
	ctx := context.Background()
	cfg := db.Config{Driver: db.DriverMemory, Name: t.Name()}
	if postgres {
		raw := os.Getenv("ORRERY_TEST_POSTGRES_URL")
		if raw == "" {
			t.Skip("isolated PostgreSQL URL is not configured")
		}
		u, err := url.Parse(raw)
		if err != nil || !strings.HasPrefix(u.Query().Get("search_path"), "orrery_inference_test_") {
			t.Fatal("isolated inference schema required")
		}
		admin, err := db.Open(ctx, db.Config{Driver: db.DriverPostgres, URL: raw})
		if err != nil {
			t.Fatal(err)
		}
		schema := fmt.Sprintf("orrery_inference_test_source_%d", time.Now().UnixNano())
		if _, err := admin.Exec(ctx, `CREATE SCHEMA "`+schema+`"`); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { admin.Exec(context.Background(), `DROP SCHEMA "`+schema+`" CASCADE`); admin.Close() })
		q := u.Query()
		q.Set("search_path", schema)
		u.RawQuery = q.Encode()
		cfg = db.Config{Driver: db.DriverPostgres, URL: u.String()}
	}
	r, err := db.OpenRegistry(ctx, db.RegistryConfig{Databases: map[string]db.Config{db.RoleCore: cfg}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close() })
	c := migration.NewCoordinator(r)
	for _, register := range []func(*migration.Coordinator) error{ontology.RegisterMigrations, contextgraph.RegisterMigrations, actioncontract.RegisterMigrations, knowledgeinstances.RegisterMigrations, RegisterMigrations} {
		if err := register(c); err != nil {
			t.Fatal(err)
		}
	}
	if err := c.UpRole(ctx, db.RoleCore); err != nil {
		t.Fatal(err)
	}
	core, err := r.Store(db.RoleCore)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range actioncontract.DefaultActionTypes() {
		if err := actioncontract.NewService(actioncontract.NewPlatformRepository(core)).RegisterActionType(ctx, a); err != nil {
			t.Fatal(err)
		}
	}
	return core
}

func sourceCommandHandler(core *db.Store) SourceWithdrawalHandler {
	return func(ctx context.Context, tx db.Tx, e SourceWithdrawal) error {
		h := e.Source
		input := knowledgeinstances.SourceWithdrawal{EventID: e.ID, EventType: e.EventType, Sequence: e.Sequence, Source: knowledgeinstances.Source{TenantID: h.TenantID, KnowledgeBaseID: h.KnowledgeBaseID, DocumentID: h.DocumentID, FolderID: h.FolderID, RootID: h.RootID, Revision: h.Revision, ContentHash: h.ContentHash, Access: h.Access, Generation: h.Generation}}
		if _, err := (&knowledgeinstances.Store{Core: core}).WithdrawSourceTx(ctx, tx, input, e.Fence); err != nil {
			return err
		}
		return nil
	}
}

func lifecycleFixture(t *testing.T, postgres bool) (*PlatformStore, Binding, Projection) {
	t.Helper()
	core := lifecycleDatabase(t, postgres)
	ctx := context.Background()
	s := NewPlatformStore(core).WithSourceLifecycle(sourceCommandHandler(core))
	b := Binding{TenantID: "tenant", KnowledgeBaseID: "kb", FolderID: "folder", Enabled: true, SourceAccessRef: contextgraph.AccessRef{Kind: "organization", ID: "tenant", TenantID: "tenant"}}
	if err := s.SaveBinding(ctx, b); err != nil {
		t.Fatal(err)
	}
	if err := contextgraph.NewPlatformStore(core).SaveObject(ctx, contextgraph.Object{TenantID: b.TenantID, ID: "root", ObjectType: "knowledge_document", Label: "Source", OwnerAccessRef: b.SourceAccessRef}); err != nil {
		t.Fatal(err)
	}
	p := Projection{TenantID: b.TenantID, KnowledgeBaseID: b.KnowledgeBaseID, DocumentID: "doc", KnowledgeDocumentObjectID: "root", Revision: 1, ContentHash: "sha256:original"}
	if err := s.SaveProjectionAndCursor(ctx, b, p, "10"); err != nil {
		t.Fatal(err)
	}
	instances := &knowledgeinstances.Store{Core: core}
	guard := func(context.Context, db.Tx) error { return nil }
	w, err := (&knowledgeinstances.WatchStore{Store: core}).Publish(ctx, knowledgeinstances.Watch{TenantID: b.TenantID, ID: "watch", AgentID: "agent", RootID: "root", Active: true, PublishedAt: time.Now().UTC()}, 0, guard)
	if err != nil {
		t.Fatal(err)
	}
	source := knowledgeinstances.Source{TenantID: b.TenantID, KnowledgeBaseID: b.KnowledgeBaseID, FolderID: b.FolderID, DocumentID: p.DocumentID, RootID: p.KnowledgeDocumentObjectID, Revision: p.Revision, ContentHash: p.ContentHash, Access: b.SourceAccessRef, Generation: 1, Content: "Acme operates in CN."}
	extraction := knowledgeinstances.Extraction{Reason: "Literal source evidence.", Entities: []knowledgeinstances.EntityInput{{Ref: "acme", Kind: "company", Name: "Acme", Quote: "Acme operates in CN", Properties: []knowledgeinstances.Property{{Predicate: "country", Value: "CN", Quote: "Acme operates in CN"}}}}}
	plan, err := knowledgeinstances.Prepare("initial", w, source, "schema", "authorization", extraction, nil, nil, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	batch, err := instances.SavePrepared(ctx, knowledgeinstances.Batch{ID: "initial", TenantID: b.TenantID, WatchID: w.ID, Plan: plan})
	if err != nil {
		t.Fatal(err)
	}
	batch, err = instances.SaveDecision(ctx, batch, knowledgeinstances.Decision{Decision: "approve", Reason: "Model checked the source.", ModelID: "test-model", PlanHash: knowledgeinstances.Digest(plan)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := instances.Apply(ctx, batch, knowledgeinstances.CommitContext{AgentID: w.AgentID, Guard: guard}); err != nil {
		t.Fatal(err)
	}
	return s, b, p
}

func testOrderedSourceLifecycle(t *testing.T, postgres bool) {
	s, b, p := lifecycleFixture(t, postgres)
	ctx := context.Background()
	c := provider.KnowledgeChange{Sequence: 20, FolderID: b.FolderID, DocumentID: p.DocumentID, EventType: "document.unpublished"}
	var wg sync.WaitGroup
	failures := make(chan error, 4)
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, err := s.applyTombstone(ctx, b, c); failures <- err }()
	}
	wg.Wait()
	close(failures)
	for err := range failures {
		if err != nil {
			t.Fatal(err)
		}
	}
	for table, want := range map[string]int{"knowledge_ontology_projections": 0, "knowledge_ontology_source_events": 1, "ontology_document_withdrawals": 1, "knowledge_instance_claims": 0, "context_graph_fact_assertions": 0} {
		var n int
		if err := s.store.QueryRow(ctx, `SELECT COUNT(*) FROM `+table).Scan(&n); err != nil || n != want {
			t.Fatalf("atomic lifecycle %s=%d want %d %v", table, n, want, err)
		}
	}
	oldHead, _ := s.SourceHead(ctx, b, p.DocumentID)
	if oldHead.Status != "withdrawn" || oldHead.Generation != 1 {
		t.Fatalf("withdrawn head %+v", oldHead)
	}
	if err := s.SaveProjectionAndCursor(ctx, b, p, ""); !errors.Is(err, ErrRevisionMismatch) {
		t.Fatal("unfenced snapshot resurrected invalid source")
	}
	if err := s.SaveProjectionAndCursor(ctx, b, p, "30"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.applyTombstone(ctx, b, c); err != nil {
		t.Fatal(err)
	}
	if applied, err := s.withdrawDocument(ctx, b, p.DocumentID, "document.missing", 0, &oldHead); err != nil || applied {
		t.Fatalf("old inventory removed republished source: %v %v", applied, err)
	}
	if err := s.SaveProjectionAndCursor(ctx, b, Projection{TenantID: b.TenantID, KnowledgeBaseID: b.KnowledgeBaseID, DocumentID: p.DocumentID, KnowledgeDocumentObjectID: "old-root", Revision: 1, ContentHash: "old-hash"}, "10"); err != nil {
		t.Fatal(err)
	}
	head, _ := s.SourceHead(ctx, b, p.DocumentID)
	if head.Status != "published" || head.Generation != 2 || head.Sequence != 30 || head.ContentHash != p.ContentHash || head.RootID != "root" {
		t.Fatalf("out-of-order event damaged source: %+v", head)
	}
	// Reconciliation may restore a source only against the pre-fetch head;
	// its watermark also protects the snapshot against old feed tombstones.
	c.Sequence = 40
	if _, err := s.applyTombstone(ctx, b, c); err != nil {
		t.Fatal(err)
	}
	expected, _ := s.SourceHead(ctx, b, p.DocumentID)
	if err := s.applyPublishedProjection(ctx, b, p, "", &expected, 50, nil); err != nil {
		t.Fatal(err)
	}
	c.Sequence = 45
	if _, err := s.applyTombstone(ctx, b, c); err != nil {
		t.Fatal(err)
	}
	head, _ = s.SourceHead(ctx, b, p.DocumentID)
	if head.Status != "published" || head.Generation != 3 || head.Sequence != 50 {
		t.Fatalf("snapshot watermark failed: %+v", head)
	}
}

func TestKnowledgeSourceLifecycleOrderedAtomicWithdrawal(t *testing.T) {
	testOrderedSourceLifecycle(t, false)
}
func TestPostgresKnowledgeSourceLifecycleOrderedAtomicWithdrawal(t *testing.T) {
	testOrderedSourceLifecycle(t, true)
}

type lifecycleInventory struct {
	*fakeIndexedRevisionSource
	inventory       []provider.FolderDocument
	inventoryErr    error
	beforeInventory func()
}

func (f *lifecycleInventory) ListFolderDocumentInventory(context.Context, provider.FolderDocumentsRequest) ([]provider.FolderDocument, error) {
	if f.beforeInventory != nil {
		f.beforeInventory()
	}
	return f.inventory, f.inventoryErr
}

func TestKnowledgeSourceLifecycleRequiresCompleteFencedInventory(t *testing.T) {
	for _, scenario := range []string{"reindexing", "missing", "forbidden", "timeout", "republish-race", "filtered-only"} {
		t.Run(scenario, func(t *testing.T) {
			s, b, p := lifecycleFixture(t, false)
			ctx := context.Background()
			filtered := &fakeIndexedRevisionSource{fakeRevisionSource: &fakeRevisionSource{}}
			inventory := &lifecycleInventory{fakeIndexedRevisionSource: filtered}
			var source provider.RevisionProvider = inventory
			switch scenario {
			case "reindexing":
				inventory.inventory = []provider.FolderDocument{{DocumentID: p.DocumentID, FolderID: b.FolderID, Status: "indexing"}}
			case "forbidden":
				inventory.inventoryErr = errors.New("403 forbidden")
			case "timeout":
				inventory.inventoryErr = context.DeadlineExceeded
			case "republish-race":
				inventory.beforeInventory = func() {
					p.Revision = 2
					p.ContentHash = "sha256:new"
					if err := s.SaveProjectionAndCursor(ctx, b, p, "30"); err != nil {
						t.Fatal(err)
					}
				}
			case "filtered-only":
				source = filtered
			}
			runner := NewRunner(source, contextgraph.NewPlatformStore(s.store), s, nil)
			_, err := runner.Run(ctx, b, 20)
			if (scenario == "forbidden" || scenario == "timeout") != (err != nil) {
				t.Fatalf("inventory error %v", err)
			}
			claims, err := (&knowledgeinstances.Store{Core: s.store}).Claims(ctx, b.TenantID, "watch")
			if err != nil {
				t.Fatal(err)
			}
			if scenario == "missing" {
				if len(claims) != 0 {
					t.Fatal("authoritative absence did not withdraw source")
				}
			} else if len(claims) != 3 {
				t.Fatal("non-authoritative or stale inventory withdrew source")
			}
		})
	}
}

func TestKnowledgeSourcePublicationUsesOneTransactionForHeadGraphAndAssociations(t *testing.T) {
	s, b, p := lifecycleFixture(t, false)
	ctx := context.Background()
	graph := contextgraph.NewPlatformStore(s.store)
	if err := graph.SaveObject(ctx, contextgraph.Object{TenantID: b.TenantID, ID: "target", ObjectType: "knowledge_document", CanonicalKey: "knowledge_document:target", Label: "Target", OwnerAccessRef: b.SourceAccessRef}); err != nil {
		t.Fatal(err)
	}
	registry := ontology.NewPlatformRegistry(s.store)
	if err := registry.SaveObjectType(ctx, ontology.ObjectType{ID: "knowledge_document", TenantScope: ontology.TenantScopeTenant, DisplayName: "Knowledge document", CanonicalKeyRule: "knowledge_document:{id}"}); err != nil {
		t.Fatal(err)
	}
	if err := registry.SaveLinkType(ctx, ontology.LinkType{ID: DocumentReferenceLinkType, FromObjectType: "knowledge_document", ToObjectType: "knowledge_document", Cardinality: ontology.CardinalityManyToMany, TenantScope: ontology.TenantScopeTenant}); err != nil {
		t.Fatal(err)
	}
	p.Revision = 2
	p.ContentHash = "new-content"
	p.Assertions = []AssertionProjection{{LinkType: DocumentReferenceLinkType, From: ObjectRef{Kind: "knowledge_document", ID: "root"}, To: ObjectRef{Kind: "knowledge_document", ID: "target"}, FromObjectID: "root", ToObjectID: "target", Status: AssertionConfirmed, Evidence: Evidence{Anchor: "reference", ObservedAt: time.Now().UTC()}}}
	applier := NewGraphApplier(graph, NewOntologyLinkValidator(registry, graph))
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := s.applyPublishedProjection(ctx, b, p, "20", nil, 0, applier); err != nil {
		t.Fatal(err)
	}
	var links int
	if err := s.store.QueryRow(ctx, `SELECT COUNT(*) FROM context_graph_links WHERE source_stream_id='knowledge_revision:doc'`).Scan(&links); err != nil || links != 1 {
		t.Fatalf("publication graph %d %v", links, err)
	}
	// A late association write failure must roll back an already-written head
	// and graph replacement. This also exercises the transaction-bound validator.
	if _, err := s.store.Exec(ctx, `DROP TABLE knowledge_ontology_claims`); err != nil {
		t.Fatal(err)
	}
	p.Revision = 3
	p.ContentHash = "broken-publication"
	p.Assertions = nil
	if err := s.applyPublishedProjection(ctx, b, p, "30", nil, 0, applier); err == nil {
		t.Fatal("partial publication committed")
	}
	head, err := s.SourceHead(ctx, b, p.DocumentID)
	if err != nil || head.Revision != 2 || head.ContentHash != "new-content" {
		t.Fatalf("head partially advanced: %+v %v", head, err)
	}
	if err := s.store.QueryRow(ctx, `SELECT COUNT(*) FROM context_graph_links WHERE source_stream_id='knowledge_revision:doc'`).Scan(&links); err != nil || links != 1 {
		t.Fatalf("graph partially removed: %d %v", links, err)
	}
}

func TestKnowledgeSourceLifecycleRejectsTombstonesForOlderContent(t *testing.T) {
	s, b, p := lifecycleFixture(t, false)
	ctx := context.Background()
	p.Revision = 2
	p.ContentHash = "sha256:newer"
	if err := s.SaveProjectionAndCursor(ctx, b, p, "20"); err != nil {
		t.Fatal(err)
	}
	c := provider.KnowledgeChange{Sequence: 30, FolderID: b.FolderID, DocumentID: p.DocumentID, Revision: 1, EventType: "document.deleted"}
	if applied, err := s.applyTombstone(ctx, b, c); err != nil || applied {
		t.Fatalf("older revision removed current publication: %v %v", applied, err)
	}
	c.Sequence = 31
	c.Revision = 2
	c.ContentHash = "sha256:older-content"
	if applied, err := s.applyTombstone(ctx, b, c); err != nil || applied {
		t.Fatalf("older content removed current publication: %v %v", applied, err)
	}
	head, _ := s.SourceHead(ctx, b, p.DocumentID)
	if head.Status != "published" || head.Revision != 2 || head.Sequence != 31 {
		t.Fatalf("newer head damaged %+v", head)
	}
	c.Sequence = 32
	c.ContentHash = p.ContentHash
	if applied, err := s.applyTombstone(ctx, b, c); err != nil || !applied {
		t.Fatalf("matching source tombstone rejected: %v %v", applied, err)
	}
}

func testHistoricalSourceRecovery(t *testing.T, postgres bool) {
	s, b, p := lifecycleFixture(t, postgres)
	ctx := context.Background()
	// Reproduce the old failure: disposable bridge projection already removed,
	// current claims remain and the provider feed cursor already passed deletion.
	for _, table := range []string{"knowledge_ontology_projections", "knowledge_ontology_source_heads", "knowledge_instance_claim_sources"} {
		if _, err := s.store.Exec(ctx, `DELETE FROM `+table); err != nil {
			t.Fatal(err)
		}
	}
	s.sourceCatalog = func(ctx context.Context, tx db.Tx, b Binding) ([]SourceHead, error) {
		sources, err := knowledgeinstances.SourceCatalogTx(ctx, tx, b.TenantID, b.KnowledgeBaseID)
		if err != nil {
			return nil, err
		}
		var result []SourceHead
		for _, source := range sources {
			result = append(result, SourceHead{TenantID: source.TenantID, KnowledgeBaseID: source.KnowledgeBaseID, DocumentID: source.DocumentID, RootID: source.RootID, Revision: source.Revision, ContentHash: source.ContentHash, Access: source.Access, Generation: source.Generation})
		}
		return result, nil
	}
	feed := &lifecycleInventory{fakeIndexedRevisionSource: &fakeIndexedRevisionSource{fakeRevisionSource: &fakeRevisionSource{}}}
	runner := NewRunner(feed, contextgraph.NewPlatformStore(s.store), s, nil)
	feed.inventoryErr = context.DeadlineExceeded
	if _, err := runner.Run(ctx, b, 20); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("historical source recovery treated provider failure as deletion")
	}
	claims, _ := (&knowledgeinstances.Store{Core: s.store}).Claims(ctx, b.TenantID, "watch")
	if len(claims) != 3 {
		t.Fatal("recovery removed claims without an attestation")
	}
	feed.inventoryErr = nil
	if report, err := runner.Run(ctx, b, 20); err != nil || report.Tombstoned != 1 {
		t.Fatalf("historical orphan did not converge: %+v %v", report, err)
	}
	claims, _ = (&knowledgeinstances.Store{Core: s.store}).Claims(ctx, b.TenantID, "watch")
	if len(claims) != 0 {
		t.Fatal("historical orphan claims survived")
	}
	head, _ := s.SourceHead(ctx, b, p.DocumentID)
	if head.Status != "withdrawn" {
		t.Fatalf("historical head missing %+v", head)
	}
}

func TestKnowledgeSourceLifecycleRecoversHistoricalOrphans(t *testing.T) {
	testHistoricalSourceRecovery(t, false)
}
func TestPostgresKnowledgeSourceLifecycleRecoversHistoricalOrphans(t *testing.T) {
	testHistoricalSourceRecovery(t, true)
}
