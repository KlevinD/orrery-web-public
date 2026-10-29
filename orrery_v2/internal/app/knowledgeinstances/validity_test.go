package knowledgeinstances

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"orrery_v2/internal/app/contextgraph"
	"orrery_v2/internal/platform/db"
)

func TestAssertionValidityGroundingAndCalendarBoundaries(t *testing.T) {
	s := instanceSource()
	s.Content = "北京时间：2026年10月17日至2026年10月18日（含当日），金额100。"
	v := &Validity{From: "2026-10-17", Until: "2026-10-18", UntilInclusive: true, Timezone: "Asia/Shanghai", Quote: s.Content}
	if err := validateValidity(s, v); err != nil {
		t.Fatal(err)
	}
	from, end, err := v.Bounds()
	if err != nil || from.Format(time.RFC3339) != "2026-10-16T16:00:00Z" || end.Format(time.RFC3339) != "2026-10-18T16:00:00Z" {
		t.Fatalf("calendar bounds: %v %v %v", from, end, err)
	}
	if !claimActiveAt(Claim{Validity: v}, from) || claimActiveAt(Claim{Validity: v}, *end) {
		t.Fatal("window is not [from,until)")
	}
	dst := &Validity{From: "2026-03-08", Until: "2026-03-08", UntilInclusive: true, Timezone: "America/New_York"}
	start, finish, err := dst.Bounds()
	if err != nil || finish.Sub(start) != 23*time.Hour {
		t.Fatalf("DST inclusive day: %v %v", finish, err)
	}
	for _, mode := range []string{"missing-zone", "invented-zone", "invented-quote", "invented-date", "reversed", "inclusive-instant", "out-of-range"} {
		t.Run(mode, func(t *testing.T) {
			copy := *v
			switch mode {
			case "missing-zone":
				copy.Timezone = ""
			case "invented-zone":
				copy.Timezone = "America/New_York"
			case "invented-quote":
				copy.Quote = "invented excerpt"
			case "invented-date":
				copy.From = "2026-10-16"
			case "reversed":
				copy.From = "2026-10-20"
			case "inclusive-instant":
				copy.Until = "2026-10-18T00:00:00+08:00"
			case "out-of-range":
				copy.From = "9999-10-17"
			}
			if !errors.Is(validateValidity(s, &copy), ErrInvalid) {
				t.Fatal("ungrounded/invalid interval accepted")
			}
		})
	}
	if literalDate("12026-10-17", "2026-10-17") {
		t.Fatal("partial numeric date matched")
	}
}

func timedExtraction(from, end time.Time, amount float64) (Source, Extraction) {
	s := instanceSource()
	quote := fmt.Sprintf("Annual amount %.0f USD from %s until %s.", amount, from.Format(time.RFC3339Nano), end.Format(time.RFC3339Nano))
	s.Content = "Acme is a company in CN. " + quote
	s.ContentHash = Digest(s.Content)
	x := instanceExtraction("Acme", "", amount)
	x.Entities[0].Properties[1].Quote = quote
	x.Entities[0].Properties[1].Validity = &Validity{From: from.Format(time.RFC3339Nano), Until: end.Format(time.RFC3339Nano), Quote: quote}
	return s, x
}

func TestAssertionValidityAllowsSeparatePeriodsAndFiltersConflicts(t *testing.T) {
	now := time.Now().UTC()
	s, x := timedExtraction(now.Add(-time.Hour), now.Add(time.Hour), 100)
	other, second := timedExtraction(now.Add(time.Hour), now.Add(2*time.Hour), 200)
	s.Content += " " + other.Content
	x.Entities[0].Properties = append(x.Entities[0].Properties, second.Entities[0].Properties[1])
	w := Watch{ID: "w", TenantID: s.TenantID, RootID: s.RootID, Active: true}
	p, err := Prepare("periods", w, s, "schema", "auth", x, nil, nil, now)
	if err != nil || len(p.Claims) != 5 || p.TemporalProtocol != TemporalProtocol {
		t.Fatalf("separate periods: %+v %v", p, err)
	}
	all := replacementClaims(p, nil)
	if len(conflictCases(activeSourceClaimsAt(all, now))) != 0 || len(conflictCases(activeSourceClaimsAt(all, now.Add(time.Hour)))) != 0 {
		t.Fatal("nonoverlapping values became a conflict")
	}
	x.Entities[0].Properties = append(x.Entities[0].Properties, x.Entities[0].Properties[1])
	if _, err := Prepare("duplicate", w, s, "schema", "auth", x, nil, nil, now); !errors.Is(err, ErrInvalid) {
		t.Fatal("duplicate window accepted")
	}
}

func TestAssertionHistoryPreservesPriorKnowledgeAndWithdrawal(t *testing.T) {
	ctx := context.Background()
	s := &Store{Core: instanceDatabase(t, false)}
	w := publishWatch(t, s, "history")
	source := instanceSource()
	first := preparedBatch(t, s, w, source, instanceExtraction("Acme", "", 100), "history-v1")
	r1 := applyBatch(t, s, first)
	source = amountSource("200", 2)
	second := preparedBatch(t, s, w, source, instanceExtraction("Acme", r1.EntityIDs[0], 200), "history-v2")
	r2 := applyBatch(t, s, second)
	graph := contextgraph.NewPlatformStore(s.Core)
	amountAt := func(known time.Time, want float64) {
		t.Helper()
		_, _, facts, _, err := graph.SnapshotAsOf(ctx, w.TenantID, r1.AppliedAt.Add(-24*time.Hour), known)
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, f := range facts {
			if f.Predicate == "amount" {
				found = true
				if f.Value != want || !f.Temporal.BusinessTimeUnspecified || !f.Temporal.BusinessValidFrom.IsZero() {
					t.Fatalf("history/uninvented start: %+v", f)
				}
			}
		}
		if !found {
			t.Fatal("historical assertion missing")
		}
	}
	amountAt(r1.AppliedAt, 100)
	amountAt(r2.AppliedAt, 200)
	withdraw := preparedBatch(t, s, w, source, Extraction{Reason: "No current assertions remain."}, "history-withdraw")
	r3 := applyBatch(t, s, withdraw)
	amountAt(r1.AppliedAt, 100)
	objects, links, facts, _, err := graph.SnapshotSubgraphAsOf(ctx, w.TenantID, []contextgraph.Ref{{ID: source.RootID}}, 2, contextgraph.DefaultCurrentSubgraphLimits(), r1.AppliedAt, r1.AppliedAt)
	if err != nil || len(objects) != 2 || len(links) != 1 || len(facts) != 4 {
		t.Fatalf("withdrawn history traversal: %d/%d/%d %v", len(objects), len(links), len(facts), err)
	}
	_, _, facts, _, err = graph.SnapshotAsOf(ctx, w.TenantID, r1.AppliedAt, r3.AppliedAt)
	if err != nil || len(facts) != 0 {
		t.Fatalf("withdrawn knowledge remained current: %+v %v", facts, err)
	}
	var versions int
	if err := s.Core.QueryRow(ctx, `SELECT COUNT(*) FROM context_graph_resource_versions`).Scan(&versions); err != nil {
		t.Fatal(err)
	}
	applyBatch(t, s, second) // durable replay must not append or restore an old source
	var after int
	s.Core.QueryRow(ctx, `SELECT COUNT(*) FROM context_graph_resource_versions`).Scan(&after)
	if versions != after {
		t.Fatal("replay changed immutable history")
	}
}

func boundaryFromStore(t *testing.T, s *Store, w Watch) BoundaryJob {
	t.Helper()
	j := BoundaryJob{TenantID: w.TenantID, WatchID: w.ID}
	if err := s.Core.QueryRow(context.Background(), `SELECT watch_version,plan_hash,source_stamp,due_at FROM ontology_document_boundaries WHERE tenant_id=? AND watch_id=?`, w.TenantID, w.ID).Scan(&j.Version, &j.PlanHash, &j.SourceStamp, &j.DueAt); err != nil {
		t.Fatal(err)
	}
	return j
}

func testValidityBoundaryAtomicity(t *testing.T, postgres bool) {
	ctx := context.Background()
	now := time.Now().UTC()
	s := &Store{Core: instanceDatabase(t, postgres)}
	w := publishWatch(t, s, "expiry")
	source, x := timedExtraction(now.Add(-time.Hour), now.Add(time.Hour), 100)
	batch := preparedBatch(t, s, w, source, x, "expiry-initial")
	r := applyBatch(t, s, batch)
	j := boundaryFromStore(t, s, w)
	at := time.Unix(0, j.DueAt).UTC()
	s = &Store{Core: s.Core} // restart with persisted schedule and outbox
	if err := s.ObserveBoundaries(ctx, at, 20); err != nil {
		t.Fatal(err)
	}
	var dispatched int
	if err := s.Core.QueryRow(ctx, `SELECT COUNT(*) FROM platform_outbox_jobs WHERE topic=?`, BoundaryTopic).Scan(&dispatched); err != nil || dispatched != 1 {
		t.Fatalf("durable boundary dispatch: %d %v", dispatched, err)
	}
	trusted := BoundaryCommitContext{CommitContext: CommitContext{AgentID: w.AgentID, Now: at, Guard: allowCommit}, QueueChanges: func(context.Context, db.Tx, string, string) error { return errors.New("late failure") }}
	if _, err := s.MaintainBoundary(ctx, j, trusted); err == nil || !strings.Contains(err.Error(), "late failure") {
		t.Fatalf("late failure: %v", err)
	}
	entities, _ := s.Candidates(ctx, w.TenantID, source.Access)
	if entities[0].Properties["amount"] != float64(100) {
		t.Fatal("partial maintenance escaped rollback")
	}
	trusted.QueueChanges = nil
	first, err := s.MaintainBoundary(ctx, j, trusted)
	if err != nil || first.EventID != j.EventID() {
		t.Fatalf("maintenance: %+v %v", first, err)
	}
	entities, _ = s.Candidates(ctx, w.TenantID, source.Access)
	if entities[0].Properties["amount"] != nil || entities[0].Status != "active" {
		t.Fatal("expiry failed to clear only the expiring property")
	}
	var currentStatus string
	if err := s.Core.QueryRow(ctx, `SELECT status FROM context_graph_facts WHERE tenant_id=? AND subject_id=? AND predicate='amount'`, w.TenantID, r.EntityIDs[0]).Scan(&currentStatus); err != nil || currentStatus != "expired" {
		t.Fatalf("expiry status: %s %v", currentStatus, err)
	}
	trusted.Now = at.Add(time.Hour)
	second, err := s.MaintainBoundary(ctx, j, trusted)
	if err != nil || Digest(first) != Digest(second) {
		t.Fatal("duplicate boundary did not return original receipt")
	}
	_, _, facts, _, err := contextgraph.NewPlatformStore(s.Core).SnapshotAsOf(ctx, w.TenantID, now, at.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, f := range facts {
		if f.Predicate == "amount" {
			found = f.Value == float64(100) && f.Status == "active"
		}
	}
	if !found {
		t.Fatal("clock expiry overwrote the assertion's historical status")
	}
	// An old event cannot change a newer replacement.
	j.PlanHash = "obsolete"
	if receipt, err := s.MaintainBoundary(ctx, j, trusted); err != nil || receipt.EventID != "" {
		t.Fatalf("stale event: %+v %v", receipt, err)
	}
}

func TestAssertionValidityBoundaryAtomicity(t *testing.T) { testValidityBoundaryAtomicity(t, false) }
func TestPostgresAssertionValidityBoundaryAtomicity(t *testing.T) {
	testValidityBoundaryAtomicity(t, true)
}

func TestAssertionValidityFutureActivationAndLastSourceExpiry(t *testing.T) {
	now := time.Now().UTC()
	ctx := context.Background()
	s := &Store{Core: instanceDatabase(t, false)}
	a, b := publishWatch(t, s, "period-a"), publishWatch(t, s, "period-b")
	start, end1, end2 := now.Add(time.Hour), now.Add(2*time.Hour), now.Add(3*time.Hour)
	source, x := timedExtraction(start, end1, 100)
	r := applyBatch(t, s, preparedBatch(t, s, a, source, x, "period-a-initial"))
	other, y := timedExtraction(start, end2, 100)
	other.DocumentID = "other-document"
	y.Entities[0].ExistingID = r.EntityIDs[0]
	applyBatch(t, s, preparedBatch(t, s, b, other, y, "period-b-initial"))
	check := func(want any) {
		t.Helper()
		entities, err := s.Candidates(ctx, a.TenantID, source.Access)
		if err != nil || len(entities) != 1 || entities[0].Properties["amount"] != want {
			t.Fatalf("active aggregate: %+v %v", entities, err)
		}
	}
	check(nil)
	maintain := func(w Watch, at time.Time) {
		t.Helper()
		j := boundaryFromStore(t, s, w)
		_, err := s.MaintainBoundary(ctx, j, BoundaryCommitContext{CommitContext: CommitContext{AgentID: w.AgentID, Now: at, Guard: allowCommit}})
		if err != nil {
			t.Fatal(err)
		}
	}
	maintain(a, start)
	maintain(b, start)
	check(float64(100))
	maintain(a, end1)
	check(float64(100))
	maintain(b, end2)
	check(nil)
}

func TestAssertionValidityRejectsModelDecisionAcrossBoundary(t *testing.T) {
	now := time.Now().UTC()
	s := &Store{Core: instanceDatabase(t, false)}
	w := publishWatch(t, s, "model-boundary")
	start := now.Add(time.Minute)
	source, x := timedExtraction(start, start.Add(time.Minute), 100)
	b := preparedBatch(t, s, w, source, x, "model-crossing")
	if _, err := s.Apply(context.Background(), b, CommitContext{AgentID: w.AgentID, Now: start, Guard: allowCommit}); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale temporal judgment committed: %v", err)
	}
	var receipts int
	if err := s.Core.QueryRow(context.Background(), `SELECT COUNT(*) FROM ontology_document_receipts`).Scan(&receipts); err != nil || receipts != 0 {
		t.Fatal("stale decision wrote a receipt")
	}
}

func TestAssertionValidityConflictUsesSurvivingEqualValueSupport(t *testing.T) {
	now := time.Now().UTC()
	ctx := context.Background()
	s := &Store{Core: instanceDatabase(t, false)}
	a, b, c := publishWatch(t, s, "support-short"), publishWatch(t, s, "support-long"), publishWatch(t, s, "competing")
	source, x := timedExtraction(now.Add(-2*time.Hour), now.Add(time.Hour), 100)
	r := applyBatch(t, s, preparedBatch(t, s, a, source, x, "short-source"))
	other, y := timedExtraction(now.Add(-time.Hour), now.Add(3*time.Hour), 100)
	other.DocumentID = "long-source"
	y.Entities[0].ExistingID = r.EntityIDs[0]
	applyBatch(t, s, preparedBatch(t, s, b, other, y, "long-source"))
	competing, z := timedExtraction(now.Add(-3*time.Hour), now.Add(2*time.Hour), 200)
	competing.DocumentID = "competing-source"
	z.Entities[0].ExistingID = r.EntityIDs[0]
	applyBatch(t, s, conflictBatch(t, s, c, competing, z, "conflict-source", a.ID))
	_, _, facts, _, err := contextgraph.NewPlatformStore(s.Core).SnapshotAsOf(ctx, a.TenantID, now.Add(90*time.Minute), time.Now().UTC().Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	selected, disputed := 0, 0
	for _, f := range facts {
		if f.Predicate != "amount" {
			continue
		}
		status := contextgraph.AssertionStatusAt(f, now.Add(90*time.Minute))
		if f.Value == float64(100) && status == "active" {
			selected++
		}
		if f.Value == float64(200) && status == "disputed" {
			disputed++
		}
	}
	if selected != 1 || disputed != 1 {
		t.Fatalf("short support expiration ended surviving model choice: selected=%d disputed=%d", selected, disputed)
	}
}
