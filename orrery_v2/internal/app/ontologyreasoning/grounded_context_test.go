package ontologyreasoning

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

func readGrounding(t *testing.T, s *Service, pack ContextPack, rules []Rule, candidates []GroundingCandidate) GroundedContext {
	t.Helper()
	record := evaluationRecord(rules)
	record.RootID = "Project-A"
	out, err := s.GroundContext(context.Background(), pack, record, GroundedContextRequest{TenantID: pack.TenantID, RootID: record.RootID, Rules: rules, Candidates: candidates}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func groundedRisk(t *testing.T, context GroundedContext) GroundedClaim {
	t.Helper()
	for _, claim := range context.Claims {
		if claim.Atom.Predicate == "risk" {
			return claim
		}
	}
	t.Fatal("derived risk claim missing")
	return GroundedClaim{}
}

func TestGroundedContextSeparatesClaimFromExactIndependentSupports(t *testing.T) {
	pack, rules := inferenceFixture()
	store := NewMemoryStore()
	service := NewService(ServiceOptions{Store: store})
	first := readGrounding(t, service, pack, rules, nil)
	risk := groundedRisk(t, first)
	if len(risk.Proofs) != 2 {
		t.Fatalf("independent proofs: %+v", risk)
	}
	var old ExactSupport
	for _, support := range first.Supports {
		if support.AssertionRef == "fact:delay-1" {
			old = support.ExactSupport
		}
	}
	candidates := []GroundingCandidate{{ID: "same-claim", RequiredClaims: []string{risk.ID}}, {ID: "exact-old-source", RequiredSupports: []ExactSupport{old}}}
	pack.Facts = pack.Facts[1:]
	second := readGrounding(t, service, pack, rules, candidates)
	if !second.Decisions[0].Eligible || second.Decisions[1].Eligible || groundedRisk(t, second).ID != risk.ID || len(groundedRisk(t, second).Proofs) != 1 {
		t.Fatalf("OR/exact support: %+v", second)
	}
	pack.Facts = nil
	last := readGrounding(t, service, pack, rules, candidates)
	if last.Decisions[0].Eligible || last.Decisions[1].Eligible {
		t.Fatalf("last support disappeared: %+v", last.Decisions)
	}
	if len(risk.Proofs) != 2 {
		t.Fatal("prior snapshot mutated")
	}
	runs, err := store.ListRuns(context.Background(), pack.TenantID)
	if err != nil || len(runs) != 0 {
		t.Fatalf("read created durable candidates/runs: %v %+v", err, runs)
	}
}

func TestGroundedContextExactVersionChangesAndWholeConjunction(t *testing.T) {
	pack, rules := inferenceFixture()
	service := NewService(ServiceOptions{})
	first := readGrounding(t, service, pack, rules, nil)
	var old ExactSupport
	for _, support := range first.Supports {
		if support.AssertionRef == "fact:delay-1" {
			old = support.ExactSupport
		}
	}
	pack.Facts[0].SourceVersion++
	second := readGrounding(t, service, pack, rules, []GroundingCandidate{{ID: "exact", RequiredSupports: []ExactSupport{old}}})
	if second.Decisions[0].Eligible || groundedRisk(t, second).ID != groundedRisk(t, first).ID {
		t.Fatalf("same statement revived an obsolete support version: old=%+v decisions=%+v supports=%+v", old, second.Decisions, second.Supports)
	}
	pack.NeighborLinks = nil
	third := readGrounding(t, service, pack, rules, []GroundingCandidate{{ID: "join", RequiredClaims: []string{groundedRisk(t, first).ID}}})
	if third.Decisions[0].Eligible {
		t.Fatal("partial AND join supported a conclusion")
	}
}

func TestGroundedContextDisputesAndClockBoundaryAreReadOnly(t *testing.T) {
	pack, rules := inferenceFixture()
	service := NewService(ServiceOptions{})
	first := readGrounding(t, service, pack, rules, nil)
	pack.Facts[1].Value = "on_time"
	conflict := readGrounding(t, service, pack, rules, []GroundingCandidate{{ID: "conflict", RequiredClaims: []string{groundedRisk(t, first).ID}}})
	if conflict.Decisions[0].Eligible || len(conflict.Conflicts) != 1 || len(conflict.Disputed) != 2 {
		t.Fatalf("conflict became definite or disappeared: %+v", conflict)
	}
	pack.Facts[1].Value = "delayed"
	record := evaluationRecord(rules)
	record.RootID = "Project-A"
	end := record.BusinessAsOf.Add(time.Second)
	for i := range pack.Facts {
		pack.Facts[i].Temporal.Mode = "state_interval"
		pack.Facts[i].Temporal.BusinessValidTo = &end
	}
	req := GroundedContextRequest{TenantID: pack.TenantID, RootID: "Project-A", Rules: rules, Candidates: []GroundingCandidate{{ID: "clock", RequiredClaims: []string{groundedRisk(t, first).ID}}}}
	before, err := service.GroundContext(context.Background(), pack, record, req, nil)
	if err != nil || !before.Decisions[0].Eligible || before.NextBoundary == nil {
		t.Fatalf("before boundary: %+v %v", before, err)
	}
	record.BusinessAsOf, record.KnownAt = end, end
	after, err := service.GroundContext(context.Background(), pack, record, req, nil)
	if err != nil || after.Decisions[0].Eligible {
		t.Fatalf("expiry needed a cleanup write: %+v %v", after, err)
	}
	if len(pack.Facts) != 2 || pack.Facts[0].Temporal.BusinessValidTo == nil {
		t.Fatal("read deleted historical source material")
	}
}

func TestGroundedContextRejectsEmptyDependenciesAndStableSnapshots(t *testing.T) {
	pack, rules := inferenceFixture()
	service := NewService(ServiceOptions{})
	first := readGrounding(t, service, pack, rules, nil)
	second := readGrounding(t, service, pack, rules, nil)
	if first.SnapshotHash != second.SnapshotHash || !reflect.DeepEqual(first.Claims, second.Claims) {
		t.Fatal("unchanged source invented a new proof version")
	}
	req := GroundedContextRequest{TenantID: pack.TenantID, RootID: "Project-A", Rules: rules, Candidates: []GroundingCandidate{{ID: "unannotated"}}}
	if !errors.Is(ValidateGroundedContextRequest(req), ErrInvalidRequest) {
		t.Fatal("missing annotations treated as supported")
	}
	req.Candidates = nil
	record := evaluationRecord(rules)
	record.RootID = "Project-A"
	record.Historical = true
	if _, err := service.GroundContext(context.Background(), pack, record, req, nil); !errors.Is(err, ErrInvalidRequest) {
		t.Fatal("history confused with current read")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	record.Historical = false
	if _, err := service.GroundContext(ctx, pack, record, req, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation: %v", err)
	}
}
