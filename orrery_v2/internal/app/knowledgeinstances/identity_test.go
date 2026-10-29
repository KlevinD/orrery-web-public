package knowledgeinstances

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"orrery_v2/internal/platform/db"
)

func qualifiedExtraction(name, value, jurisdiction, authority string) (Source, Extraction) {
	source := instanceSource()
	source.Content = fmt.Sprintf("%s is registered in %s by %s with number %s.", name, jurisdiction, authority, value)
	return source, Extraction{Reason: "The source explicitly identifies the company and registration scope.", Entities: []EntityInput{{Ref: "company", Kind: "company", Name: name, Quote: source.Content, Identifiers: []IdentifierInput{{Identifier: Identifier{Scheme: "company_registration", Jurisdiction: jurisdiction, Authority: authority, Value: value}, Quote: source.Content}}}}}
}

func TestDocumentInstancesQualifiedIdentityAndHomonyms(t *testing.T) {
	source, x := qualifiedExtraction("Acme", "00123", "CN", "Registry A")
	w := Watch{TenantID: source.TenantID, ID: "watch", RootID: source.RootID, Active: true}
	prepare := func(s Source, extraction Extraction, candidates []Entity) (Plan, error) {
		return Prepare("batch", w, s, "schema", "auth", extraction, candidates, nil, time.Now())
	}
	first, err := prepare(source, x, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, change := range []string{"different_number", "different_jurisdiction", "different_authority"} {
		s, other := qualifiedExtraction("Acme", "00123", "CN", "Registry A")
		switch change {
		case "different_number":
			s, other = qualifiedExtraction("Acme", "00456", "CN", "Registry A")
		case "different_jurisdiction":
			s, other = qualifiedExtraction("Acme", "00123", "US", "Registry A")
		case "different_authority":
			s, other = qualifiedExtraction("Acme", "00123", "CN", "Registry B")
		}
		p, err := prepare(s, other, first.Entities)
		if err != nil || p.Entities[0].ID == first.Entities[0].ID {
			t.Fatalf("homonyms merged (%s): %v", change, err)
		}
	}
	for _, bad := range []string{"missing_scope", "invented_scope", "partial_number", "non_company", "metadata_property"} {
		s, other := qualifiedExtraction("Acme", "00123", "CN", "Registry A")
		switch bad {
		case "missing_scope":
			other.Entities[0].Identifiers[0].Authority = ""
		case "invented_scope":
			other.Entities[0].Identifiers[0].Authority = "Registry B"
		case "partial_number":
			other.Entities[0].Identifiers[0].Value = "0012"
		case "non_company":
			other.Entities[0].Kind = "person"
		case "metadata_property":
			other.Entities[0].Properties = []Property{{Predicate: "identity_keys", Value: []any{"forged"}, Quote: s.Content}}
		}
		if _, err := prepare(s, other, nil); !errors.Is(err, ErrInvalid) {
			t.Fatalf("ungrounded identity accepted (%s): %v", bad, err)
		}
	}
	// Explicit reuse preserves a legacy ID even when its qualified key is new.
	legacy := first.Entities[0]
	legacy.ID, legacy.Version, legacy.IdentityKeys = "knowledge-entity:legacy-name-id", 7, nil
	x.Entities[0].ExistingID = legacy.ID
	p, err := prepare(source, x, []Entity{legacy})
	if err != nil || p.Entities[0].ID != legacy.ID || len(p.Entities[0].IdentityKeys) != 1 {
		t.Fatalf("legacy identity was replaced: %+v %v", p.Entities, err)
	}
	conflictSource, conflict := qualifiedExtraction("Acme", "00456", "CN", "Registry A")
	conflict.Entities[0].ExistingID = first.Entities[0].ID
	if _, err := prepare(conflictSource, conflict, first.Entities); !errors.Is(err, ErrConflict) {
		t.Fatalf("contradictory strong identities merged: %v", err)
	}
	// Unqualified names are scoped to a source mention, not globally unique.
	x.Entities[0].ExistingID, x.Entities[0].Identifiers = "", nil
	a, _ := prepare(source, x, nil)
	source.DocumentID = "another-document"
	b, err := prepare(source, x, a.Entities)
	if err != nil || a.Entities[0].ID == b.Entities[0].ID {
		t.Fatalf("unqualified homonyms collapsed: %v", err)
	}
}

func TestDocumentInstancesQualifiedIdentityHistoryAndAtomicity(t *testing.T) {
	ctx := context.Background()
	s := &Store{Core: instanceDatabase(t, false)}
	w := publishWatch(t, s, "identity-watch")
	source, x := qualifiedExtraction("Acme", "00123", "CN", "Registry A")
	first := preparedBatch(t, s, w, source, x, "identity-first")
	r := applyBatch(t, s, first)
	if len(r.EntityIDs) != 1 {
		t.Fatal("identity missing")
	}
	id := r.EntityIDs[0]
	// A late action failure rolls back bindings and indexes as well as facts.
	otherSource, other := qualifiedExtraction("Other", "00999", "CN", "Registry A")
	w2 := publishWatch(t, s, "identity-other")
	next := preparedBatch(t, s, w2, otherSource, other, "identity-rollback")
	if _, err := s.Core.Exec(ctx, `UPDATE action_types SET enabled=FALSE WHERE id=?`, ActionID); err != nil {
		t.Fatal(err)
	}
	tables := []string{"knowledge_instance_identity_keys", "knowledge_instance_identity_terms", "knowledge_instance_identity_index_state", "knowledge_instances", "knowledge_instance_claims", "context_graph_objects", "context_graph_facts", "context_graph_links", "context_graph_fact_assertions", "context_graph_evidence", "ontology_document_receipts", "action_runs"}
	before := map[string]string{}
	for _, table := range tables {
		before[table] = tableDigest(t, s.Core, table)
	}
	if _, err := s.Apply(ctx, next, CommitContext{AgentID: "agent", Guard: allowCommit}); err == nil {
		t.Fatal("disabled action committed")
	}
	for _, table := range tables {
		if tableDigest(t, s.Core, table) != before[table] {
			t.Fatalf("partial identity transaction: %s", table)
		}
	}
	if _, err := s.Core.Exec(ctx, `UPDATE action_types SET enabled=TRUE WHERE id=?`, ActionID); err != nil {
		t.Fatal(err)
	}
	source.Revision++
	source.Content = "No current company assertions remain."
	applyBatch(t, s, preparedBatch(t, s, w, source, Extraction{Reason: "Source withdrew all assertions."}, "identity-withdraw"))
	query := RecallQuery{Mentions: x.Entities}
	query.Mentions[0].Name = "Unrelated alias"
	set, err := s.RecallCandidates(ctx, source.TenantID, source.Access, query)
	if err != nil || len(set.Entities) != 1 || set.Entities[0].ID != id || set.Entities[0].Status != "withdrawn" || len(set.Entities[0].IdentityKeys) != 1 {
		t.Fatalf("historical registration not recalled: %+v %v", set, err)
	}
	var claims int
	if err := s.Core.QueryRow(ctx, `SELECT COUNT(*) FROM knowledge_instance_claims WHERE entity_id=?`, id).Scan(&claims); err != nil || claims != 0 {
		t.Fatalf("historical identity became an assertion: %d %v", claims, err)
	}
	source, x = qualifiedExtraction("Renamed Acme", "00123", "CN", "Registry A")
	x.Entities[0].ExistingID = id
	restored := applyBatch(t, s, preparedBatch(t, s, w, source, x, "identity-restored"))
	if !slices.Equal(restored.EntityIDs, []string{id}) {
		t.Fatal("reactivation replaced historical identity")
	}
}

func seedIdentityEntity(t *testing.T, tx db.Tx, e Entity) {
	t.Helper()
	if _, err := tx.ExecContext(context.Background(), `INSERT INTO knowledge_instances(tenant_id,id,access_hash,version,body_json) VALUES(?,?,?,?,?)`, e.TenantID, e.ID, Digest(e.Access), e.Version, body(e)); err != nil {
		t.Fatal(err)
	}
}

func TestDocumentInstancesRecallBeyondGlobalBudgetAndBoundaries(t *testing.T) {
	ctx := context.Background()
	s := &Store{Core: instanceDatabase(t, false)}
	source := instanceSource()
	if err := s.Core.WithTx(ctx, func(tx db.Tx) error {
		for i := 0; i < 1000; i++ {
			e := Entity{ID: fmt.Sprintf("unrelated-%04d", i), TenantID: source.TenantID, Access: source.Access, Kind: "company", Name: fmt.Sprintf("Unrelated%d", i), Version: 1, Status: "withdrawn"}
			seedIdentityEntity(t, tx, e)
		}
		for i := 0; i < 210; i++ {
			seedIdentityEntity(t, tx, Entity{ID: fmt.Sprintf("other-kind-%03d", i), TenantID: source.TenantID, Access: source.Access, Kind: "person", Name: "Acme Alias", Version: 1})
		}
		for _, id := range []string{"target", "other-scope", "other-tenant"} {
			e := Entity{ID: id, TenantID: source.TenantID, Access: source.Access, Kind: "company", Name: "历史企业", Key: "legacy-label", Version: 3, Properties: map[string]any{"aliases": []any{"Acme Alias"}, "registration_id": "00123"}}
			if id == "other-scope" {
				e.Access.ID = "private"
			}
			if id == "other-tenant" {
				e.TenantID, e.Access.TenantID = "tenant-b", "tenant-b"
			}
			seedIdentityEntity(t, tx, e)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"Acme Alias", "历史企业", "企业"} {
		set, err := s.RecallCandidates(ctx, source.TenantID, source.Access, RecallQuery{Mentions: []EntityInput{{Name: name, Kind: "company"}}})
		if err != nil || set.Summary.Truncated || len(set.Entities) != 1 || set.Entities[0].ID != "target" {
			t.Fatalf("legacy relevant recall failed: %s %+v %v", name, set, err)
		}
	}
	if err := s.Core.WithTx(ctx, func(tx db.Tx) error {
		for i := 0; i < 210; i++ {
			seedIdentityEntity(t, tx, Entity{ID: fmt.Sprintf("crowded-%03d", i), TenantID: source.TenantID, Access: source.Access, Kind: "company", Name: "Crowded", Version: 1})
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	query := RecallQuery{Mentions: []EntityInput{{Kind: "company", Name: "Crowded"}}, Previous: []Claim{{EntityID: "target"}}}
	set, err := s.RecallCandidates(ctx, source.TenantID, source.Access, query)
	if err != nil || !set.Summary.Truncated || len(set.Entities) != MaxIdentityCandidates || set.Entities[0].ID != "target" {
		t.Fatalf("recall did not preserve mandatory endpoint/truncation: %+v %v", set.Summary, err)
	}
	query.Previous = []Claim{{EntityID: "other-scope"}}
	if _, err := s.RecallCandidates(ctx, source.TenantID, source.Access, query); !errors.Is(err, ErrConflict) {
		t.Fatalf("foreign endpoint accepted: %v", err)
	}
	visible := Entity{Kind: "company", Name: "Visible", Properties: map[string]any{}}
	if MatchesVisibleCandidate(RecallQuery{Mentions: []EntityInput{{Kind: "company", Name: "Hidden Alias"}}}, visible) {
		t.Fatal("masked raw index fields became visible recall signals")
	}
}

func TestPostgresDocumentInstancesConcurrentQualifiedBinding(t *testing.T) {
	ctx := context.Background()
	s := &Store{Core: instanceDatabase(t, true)}
	source, x := qualifiedExtraction("Acme", "00123", "CN", "Registry A")
	a, b := publishWatch(t, s, "identity-a"), publishWatch(t, s, "identity-b")
	first := preparedBatch(t, s, a, source, x, "identity-race-a")
	// Add another scoped registration until its primary deterministic ID differs.
	var second Batch
	for i := 0; i < 100; i++ {
		alternate := x
		alternate.Entities = slices.Clone(x.Entities)
		alternate.Entities[0].Identifiers = slices.Clone(x.Entities[0].Identifiers)
		quote := fmt.Sprintf("Acme in US by Registry B has number %d.", i+100)
		otherSource := source
		otherSource.Content += " " + quote
		alternate.Entities[0].Identifiers = append(alternate.Entities[0].Identifiers, IdentifierInput{Identifier: Identifier{Scheme: "company_registration", Jurisdiction: "US", Authority: "Registry B", Value: fmt.Sprint(i + 100)}, Quote: quote})
		plan, err := Prepare("identity-race-b", b, otherSource, "schema", "authorization", alternate, nil, nil, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		if plan.Entities[0].ID != first.Plan.Entities[0].ID {
			second = preparedBatch(t, s, b, otherSource, alternate, "identity-race-b")
			break
		}
	}
	if second.ID == "" {
		t.Fatal("could not construct independent identity ID")
	}
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for _, batch := range []Batch{first, second} {
		wg.Add(1)
		go func(value Batch) {
			defer wg.Done()
			_, err := s.Apply(ctx, value, CommitContext{AgentID: "agent", Guard: allowCommit})
			errs <- err
		}(batch)
	}
	wg.Wait()
	close(errs)
	success, conflict := 0, 0
	for err := range errs {
		if err == nil {
			success++
		} else if errors.Is(err, ErrConflict) {
			conflict++
		} else {
			t.Fatal(err)
		}
	}
	if success != 1 || conflict != 1 {
		t.Fatalf("registration race: success=%d conflict=%d", success, conflict)
	}
	var count int
	if err := s.Core.QueryRow(ctx, `SELECT COUNT(*) FROM knowledge_instances`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("duplicate registered entities: %d %v", count, err)
	}
	set, err := s.RecallCandidates(ctx, source.TenantID, source.Access, RecallQuery{Mentions: x.Entities})
	if err != nil || len(set.Entities) != 1 {
		t.Fatalf("registration recall after race: %+v %v", set, err)
	}
	x.Entities[0].ExistingID = set.Entities[0].ID
	applyBatch(t, s, preparedBatch(t, s, publishWatch(t, s, "identity-retry"), source, x, "identity-race-retry"))
}

func TestDocumentInstancesIdentityValueBoundaries(t *testing.T) {
	for _, quote := range []string{"number A00123Z", "number 001234", "number 000123"} {
		if containsIdentifierValue(strings.ToLower(quote), "00123") {
			t.Fatalf("partial identifier accepted: %s", quote)
		}
	}
	if !containsIdentifierValue("登记号00123。", "00123") {
		t.Fatal("Chinese adjacent labels rejected")
	}
}
