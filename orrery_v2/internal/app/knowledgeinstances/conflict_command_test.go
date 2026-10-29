package knowledgeinstances

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

func conflictBatch(t *testing.T, s *Store, w Watch, source Source, extraction Extraction, id, selectWatch string) Batch {
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
	p, err := Prepare(id, w, source, "schema", "authorization", extraction, candidates, previous, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	p, err = s.PreviewConflicts(ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	p = SealPlan(p)
	b, err := s.SavePrepared(ctx, Batch{ID: id, TenantID: w.TenantID, WatchID: w.ID, Plan: p})
	if err != nil {
		t.Fatal(err)
	}
	d := Decision{Decision: "approve", Reason: "Replacement and competing literal evidence checked independently.", ModelID: "conflict-model", PlanHash: Digest(p)}
	for _, c := range p.Conflicts {
		choice := ResolutionChoice{EntityID: c.EntityID, Predicate: c.Predicate, Decision: "defer", Reason: "The evidence does not establish one current value."}
		for _, a := range c.Assertions {
			if a.WatchID == selectWatch {
				choice.Decision, choice.ClaimID, choice.Reason = "accept", a.Claim.ID, "This offered assertion states the applicable current audited value."
			}
		}
		d.Resolutions = append(d.Resolutions, choice)
	}
	b, err = s.SaveDecision(ctx, b, d)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func amountSource(amount string, revision int) Source {
	s := instanceSource()
	s.Revision, s.ContentHash = revision, "sha256:"+amount
	s.Content = strings.ReplaceAll(s.Content, "100", amount)
	return s
}

func assertAmounts(t *testing.T, s *Store, active, disputed int) {
	t.Helper()
	for _, table := range []string{"context_graph_facts", "context_graph_fact_assertions"} {
		for status, want := range map[string]int{"active": active, "disputed": disputed} {
			var got int
			if err := s.Core.QueryRow(context.Background(), `SELECT COUNT(*) FROM `+table+` WHERE tenant_id='tenant-a' AND predicate='amount' AND status=?`, status).Scan(&got); err != nil || got != want {
				t.Fatalf("%s %s=%d want %d: %v", table, status, got, want, err)
			}
		}
	}
}

func testConflictLifecycle(t *testing.T, postgres bool) {
	ctx := context.Background()
	s := &Store{Core: instanceDatabase(t, postgres)}
	a, b, c := publishWatch(t, s, "source-a"), publishWatch(t, s, "source-b"), publishWatch(t, s, "source-c")
	r := applyBatch(t, s, conflictBatch(t, s, a, amountSource("100", 1), instanceExtraction("Acme", "", 100), "a-initial", ""))
	id := r.EntityIDs[0]
	accepted := conflictBatch(t, s, b, amountSource("200", 1), instanceExtraction("Acme", id, 200), "b-accept-a", a.ID)
	r = applyBatch(t, s, accepted)
	entities, _ := s.Candidates(ctx, a.TenantID, instanceSource().Access)
	e := entities[0]
	if r.ResolutionCount != 1 || len(e.Conflicts) != 0 || e.Properties["amount"] != float64(100) || len(e.Resolutions) != 1 || e.Resolutions[0].ModelID != "conflict-model" || e.Resolutions[0].InputHash == "" {
		t.Fatalf("accepted projection/receipt: %+v %+v", e, r)
	}
	assertAmounts(t, s, 1, 1)
	// Raw values, quotes and source metadata remain present and independently
	// owned. Retry returns the exact durable receipt, including after expiry.
	claims, _ := s.Claims(ctx, b.TenantID, b.ID)
	for _, claim := range claims {
		if claim.Source == nil || claim.Source.Content != "" {
			t.Fatal("missing source stamp or leaked source text")
		}
		if claim.Predicate == "amount" && (claim.Value != float64(200) || claim.Quote != "Annual amount 200 USD") {
			t.Fatal("losing source was rewritten")
		}
	}
	got, err := s.Apply(ctx, accepted, CommitContext{AgentID: "agent", Now: time.Now().Add(time.Hour), Guard: allowCommit})
	if err != nil || Digest(got) != Digest(r) {
		t.Fatalf("receipt replay: %+v %v", got, err)
	}
	// A new source changes the complete evidence set. The model defers, so the
	// previous acceptance disappears and all contradictory values are withheld.
	applyBatch(t, s, conflictBatch(t, s, c, amountSource("200", 1), instanceExtraction("Acme", id, 200), "c-defer", ""))
	entities, _ = s.Candidates(ctx, a.TenantID, instanceSource().Access)
	if entities[0].Properties["amount"] != nil || len(entities[0].Conflicts) != 1 || len(entities[0].Resolutions) != 0 {
		t.Fatalf("old decision survived: %+v", entities[0])
	}
	assertAmounts(t, s, 3, 0)
	// Equal-value sources provide independent support for the chosen value.
	applyBatch(t, s, conflictBatch(t, s, a, amountSource("100", 2), instanceExtraction("Acme", id, 100), "a-accept-b", b.ID))
	entities, _ = s.Candidates(ctx, a.TenantID, instanceSource().Access)
	if entities[0].Properties["amount"] != float64(200) || len(entities[0].Resolutions[0].SupportingClaimIDs) != 2 {
		t.Fatalf("support aggregation: %+v", entities[0])
	}
	assertAmounts(t, s, 2, 1)
	// Legacy/no-adjudication callers cannot preserve an obsolete acceptance.
	applyBatch(t, s, preparedBatch(t, s, b, amountSource("200", 2), Extraction{Reason: "Source B withdraws all its claims."}, "b-withdraw"))
	entities, _ = s.Candidates(ctx, a.TenantID, instanceSource().Access)
	if entities[0].Properties["amount"] != nil || len(entities[0].Resolutions) != 0 {
		t.Fatal("obsolete acceptance was reused")
	}
	assertAmounts(t, s, 2, 0)
	applyBatch(t, s, conflictBatch(t, s, c, amountSource("200", 2), Extraction{Reason: "Source C withdraws all its claims."}, "c-withdraw", ""))
	entities, _ = s.Candidates(ctx, a.TenantID, instanceSource().Access)
	if entities[0].Properties["amount"] != float64(100) || len(entities[0].Conflicts) != 0 {
		t.Fatalf("agreement recovery: %+v", entities[0])
	}
	assertAmounts(t, s, 1, 0)
	// Historical audit decisions remain immutable after all later replacements.
	history, _ := s.GetBatch(ctx, accepted.TenantID, accepted.ID)
	if Digest(history.Decision) != Digest(accepted.Decision) || Digest(history.Receipt) != Digest(r) {
		t.Fatal("historical adjudication changed")
	}
}

func TestDocumentInstancesConflictAdjudicationLifecycle(t *testing.T) {
	testConflictLifecycle(t, false)
}
func TestPostgresDocumentInstancesConflictAdjudicationLifecycle(t *testing.T) {
	testConflictLifecycle(t, true)
}

func TestDocumentInstancesConflictFencesAndRollback(t *testing.T) {
	for _, mode := range []string{"late-action-failure", "stale-claim-set", "foreign-projection", "partial-case"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			s := &Store{Core: instanceDatabase(t, false)}
			a, b := publishWatch(t, s, "source-a"), publishWatch(t, s, "source-b")
			r := applyBatch(t, s, conflictBatch(t, s, a, amountSource("100", 1), instanceExtraction("Acme", "", 100), "initial", ""))
			id := r.EntityIDs[0]
			plan := conflictBatch(t, s, b, amountSource("200", 1), instanceExtraction("Acme", id, 200), "adjudication", a.ID)
			want := ErrConflict
			switch mode {
			case "late-action-failure":
				if _, err := s.Core.Exec(ctx, `UPDATE action_types SET enabled=FALSE WHERE id=?`, ActionID); err != nil {
					t.Fatal(err)
				}
			case "stale-claim-set":
				applyBatch(t, s, conflictBatch(t, s, a, amountSource("150", 2), instanceExtraction("Acme", id, 150), "concurrent-source", ""))
			case "foreign-projection":
				if _, err := s.Core.Exec(ctx, `UPDATE context_graph_facts SET status='foreign-edit' WHERE tenant_id=? AND predicate='amount'`, a.TenantID); err != nil {
					t.Fatal(err)
				}
			case "partial-case":
				plan.Plan.Conflicts[0].Assertions = plan.Plan.Conflicts[0].Assertions[:1]
				plan.Decision.Resolutions[0].ClaimID = plan.Plan.Conflicts[0].Assertions[0].Claim.ID
				plan.Decision.PlanHash = Digest(plan.Plan)
				// Simulate an incorrectly prepared but consistently persisted input.
				if _, err := s.Core.Exec(ctx, `UPDATE ontology_document_batches SET body_json=? WHERE tenant_id=? AND id=?`, body(plan), b.TenantID, plan.ID); err != nil {
					t.Fatal(err)
				}
			}
			before, _ := s.Candidates(ctx, a.TenantID, instanceSource().Access)
			_, err := s.Apply(ctx, plan, CommitContext{AgentID: "agent", Guard: allowCommit})
			if err == nil || (mode != "late-action-failure" && !errors.Is(err, want)) {
				t.Fatalf("write was not fenced: %v", err)
			}
			after, _ := s.Candidates(ctx, a.TenantID, instanceSource().Access)
			if Digest(before) != Digest(after) {
				t.Fatal("failed transaction changed entity or accepted projection")
			}
			claims, _ := s.Claims(ctx, b.TenantID, b.ID)
			var receipts, evidence int
			s.Core.QueryRow(ctx, `SELECT COUNT(*) FROM ontology_document_receipts WHERE tenant_id=? AND batch_id=?`, b.TenantID, plan.ID).Scan(&receipts)
			s.Core.QueryRow(ctx, `SELECT COUNT(*) FROM context_graph_evidence WHERE tenant_id=? AND id=?`, b.TenantID, stableID("knowledge-document-evidence:", plan.ID)).Scan(&evidence)
			if len(claims) != 0 || receipts != 0 || evidence != 0 {
				t.Fatalf("partial commit: %d claims %d receipts %d evidence", len(claims), receipts, evidence)
			}
		})
	}
}

func TestDocumentInstancesConflictModelCannotInventSelection(t *testing.T) {
	p := Plan{ClaimSetHash: "frozen", Conflicts: []ConflictCase{{EntityID: "e", Predicate: "amount", Assertions: []SourceClaim{{Claim: Claim{ID: "offered"}}}}}}
	valid := Decision{Decision: "approve", Resolutions: []ResolutionChoice{{EntityID: "e", Predicate: "amount", Decision: "accept", ClaimID: "offered", Reason: "Supported current value."}}}
	for _, mode := range []string{"unknown-id", "missing-choice", "wrong-predicate", "duplicate", "blank-reason", "defer-with-id", "rejected-with-choice", "human-review", "missing-fence"} {
		t.Run(mode, func(t *testing.T) {
			var d Decision
			json.Unmarshal([]byte(body(valid)), &d)
			plan := p
			switch mode {
			case "unknown-id":
				d.Resolutions[0].ClaimID = "invented"
			case "missing-choice":
				d.Resolutions = nil
			case "wrong-predicate":
				d.Resolutions[0].Predicate = "currency"
			case "duplicate":
				d.Resolutions = append(d.Resolutions, d.Resolutions[0])
			case "blank-reason":
				d.Resolutions[0].Reason = " "
			case "defer-with-id":
				d.Resolutions[0].Decision = "defer"
			case "rejected-with-choice":
				d.Decision = "reject"
			case "human-review":
				d.Resolutions[0].Decision = "ask_human"
			case "missing-fence":
				plan.ClaimSetHash = ""
			}
			if !errors.Is(ValidateResolutionChoices(plan, d), ErrInvalid) {
				t.Fatal("invalid model choice accepted")
			}
		})
	}
}

func TestDocumentInstancesLegacyConflictSourcesUpgrade(t *testing.T) {
	ctx := context.Background()
	s := &Store{Core: instanceDatabase(t, false)}
	a, b := publishWatch(t, s, "legacy"), publishWatch(t, s, "current")
	r := applyBatch(t, s, preparedBatch(t, s, a, amountSource("100", 1), instanceExtraction("Acme", "", 100), "old-batch"))
	claims, _ := s.Claims(ctx, a.TenantID, a.ID)
	for _, claim := range claims {
		claim.Source = nil
		if _, err := s.Core.Exec(ctx, `UPDATE knowledge_instance_claims SET body_json=? WHERE tenant_id=? AND id=?`, body(claim), a.TenantID, claim.ID); err != nil {
			t.Fatal(err)
		}
	}
	plan := conflictBatch(t, s, b, amountSource("200", 1), instanceExtraction("Acme", r.EntityIDs[0], 200), "legacy-conflict", a.ID)
	if len(plan.Plan.Conflicts) != 1 {
		t.Fatal("legacy conflict was not offered")
	}
	for _, a := range plan.Plan.Conflicts[0].Assertions {
		if a.Claim.Source == nil || a.Claim.Source.Content != "" || a.Claim.Source.Revision != 1 {
			t.Fatal("legacy source was not recovered without content")
		}
	}
	applyBatch(t, s, plan)
	assertAmounts(t, s, 1, 1)
}

func TestPostgresDocumentInstancesConcurrentAdjudication(t *testing.T) {
	ctx := context.Background()
	s := &Store{Core: instanceDatabase(t, true)}
	a, b, c := publishWatch(t, s, "a"), publishWatch(t, s, "b"), publishWatch(t, s, "c")
	r := applyBatch(t, s, conflictBatch(t, s, a, amountSource("100", 1), instanceExtraction("Acme", "", 100), "initial", ""))
	first := conflictBatch(t, s, b, amountSource("200", 1), instanceExtraction("Acme", r.EntityIDs[0], 200), "choose-a", a.ID)
	second := conflictBatch(t, s, c, amountSource("300", 1), instanceExtraction("Acme", r.EntityIDs[0], 300), "choose-c", c.ID)
	var wait sync.WaitGroup
	errs := make(chan error, 2)
	start := make(chan struct{})
	for _, plan := range []Batch{first, second} {
		wait.Add(1)
		go func(plan Batch) {
			defer wait.Done()
			<-start
			_, err := s.Apply(ctx, plan, CommitContext{AgentID: "agent", Guard: allowCommit})
			errs <- err
		}(plan)
	}
	close(start)
	wait.Wait()
	close(errs)
	ok, conflict := 0, 0
	for err := range errs {
		if err == nil {
			ok++
		} else if errors.Is(err, ErrConflict) {
			conflict++
		} else {
			t.Fatal(err)
		}
	}
	if ok != 1 || conflict != 1 {
		t.Fatalf("concurrent adjudications: %d success %d conflicts", ok, conflict)
	}
	assertAmounts(t, s, 1, 1)
}
