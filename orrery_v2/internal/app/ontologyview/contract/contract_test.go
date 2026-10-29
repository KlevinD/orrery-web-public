package contract

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func TestCursorPageEnforcesLimitAndCursorInvariant(t *testing.T) {
	page, err := NewCursorPage([]string{"a", "b"}, 2, true, "opaque")
	if err != nil {
		t.Fatalf("NewCursorPage: %v", err)
	}
	if len(page.Items) != 2 || !page.Page.HasMore || page.Page.NextCursor != "opaque" {
		t.Fatalf("page = %+v", page)
	}
	if _, err := NewCursorPage([]string{"a"}, 1, false, "unexpected"); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("cursor invariant error = %v", err)
	}
	if limit, err := NormalizeLimit(0, DefaultListLimit, MaxPageLimit); err != nil || limit != DefaultListLimit {
		t.Fatalf("default limit = %d err=%v", limit, err)
	}
	if _, err := NormalizeLimit(MaxPageLimit+1, DefaultListLimit, MaxPageLimit); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("hard limit error = %v", err)
	}
	empty, err := NewCursorPage[string](nil, 1, false, "")
	if err != nil {
		t.Fatalf("empty page: %v", err)
	}
	raw, err := json.Marshal(empty)
	if err != nil {
		t.Fatalf("marshal empty page: %v", err)
	}
	if string(raw) != `{"items":[],"page":{"limit":1,"has_more":false}}` {
		t.Fatalf("empty page JSON = %s", raw)
	}
}

func TestQueryBudgetEnforcesCandidatesReadsResultsAndDeadline(t *testing.T) {
	policy, err := NewBudgetPolicy(map[QueryClass]QueryBudget{QueryStandard: {Deadline: time.Second, ResultLimit: 10, CandidateRows: 20, StoreReads: 2}})
	if err != nil {
		t.Fatalf("NewBudgetPolicy: %v", err)
	}
	ctx, tracker, cancel, err := policy.Start(context.Background(), QueryStandard)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer cancel()
	if err := tracker.ConsumeCandidates(20); err != nil {
		t.Fatalf("consume candidates: %v", err)
	}
	if err := tracker.ConsumeCandidates(1); !errors.Is(err, ErrQueryBudgetExceeded) {
		t.Fatalf("candidate budget error = %v", err)
	}
	if err := tracker.ConsumeStoreReads(2); err != nil {
		t.Fatalf("consume reads: %v", err)
	}
	if err := tracker.ConsumeResults(11); !errors.Is(err, ErrQueryBudgetExceeded) {
		t.Fatalf("result budget error = %v", err)
	}
	cancel()
	if err := tracker.CheckContext(ctx); !errors.Is(err, ErrQueryBudgetExceeded) {
		t.Fatalf("deadline error = %v", err)
	}
	if _, err := NewBudgetPolicy(map[QueryClass]QueryBudget{QueryStandard: {Deadline: 4 * time.Second}}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("relaxed budget error = %v", err)
	}
}

func TestCompanyOverviewQueryBudgetHasBoundedEnterpriseCandidateLimit(t *testing.T) {
	policy, err := NewBudgetPolicy(nil)
	if err != nil {
		t.Fatalf("NewBudgetPolicy: %v", err)
	}
	standard := policy.budgets[QueryStandard]
	overview, ok := policy.budgets[QueryCompanyOverview]
	if !ok {
		t.Fatal("company overview budget is missing")
	}
	if overview.Deadline != 15*time.Second {
		t.Fatalf("company overview deadline = %s", overview.Deadline)
	}
	if overview.ResultLimit != standard.ResultLimit || overview.StoreReads != standard.StoreReads {
		t.Fatalf("company overview limits = %+v, standard = %+v", overview, standard)
	}
	if overview.CandidateRows != 4096 || overview.CandidateRows <= standard.CandidateRows {
		t.Fatalf("company overview candidate limit = %d, standard = %d", overview.CandidateRows, standard.CandidateRows)
	}
	_, tracker, cancel, err := policy.Start(context.Background(), QueryCompanyOverview)
	if err != nil {
		t.Fatalf("Start company overview: %v", err)
	}
	defer cancel()
	if err := tracker.ConsumeCandidates(1030); err != nil {
		t.Fatalf("current production cardinality should fit: %v", err)
	}
	if err := tracker.ConsumeCandidates(4096 - 1030 + 1); !errors.Is(err, ErrQueryBudgetExceeded) {
		t.Fatalf("candidate ceiling error = %v", err)
	}
	if _, err := NewBudgetPolicy(map[QueryClass]QueryBudget{QueryCompanyOverview: {Deadline: 14 * time.Second}}); err != nil {
		t.Fatalf("restrict company overview deadline: %v", err)
	}
	if _, err := NewBudgetPolicy(map[QueryClass]QueryBudget{QueryCompanyOverview: {Deadline: 16 * time.Second}}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("relaxed company overview deadline error = %v", err)
	}
}

func TestQueryBudgetEnforcesNeighborhoodShape(t *testing.T) {
	policy, err := NewBudgetPolicy(nil)
	if err != nil {
		t.Fatalf("NewBudgetPolicy: %v", err)
	}
	_, tracker, cancel, err := policy.Start(context.Background(), QueryNeighborhood)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer cancel()
	if err := tracker.ValidateDepth(2); err != nil {
		t.Fatalf("depth 2: %v", err)
	}
	if err := tracker.ValidateDepth(3); !errors.Is(err, ErrQueryBudgetExceeded) {
		t.Fatalf("depth budget error = %v", err)
	}
	if err := tracker.ConsumeNodes(201); !errors.Is(err, ErrQueryBudgetExceeded) {
		t.Fatalf("node budget error = %v", err)
	}
}

func TestQueryBudgetConcurrencyLimiterEnforcesGlobalAndPrincipalLimits(t *testing.T) {
	limiter, err := NewConcurrencyLimiter(map[ConcurrencyClass]ConcurrencyLimit{
		ConcurrencySummary: {Global: 2, PerPrincipal: 1, RetryAfter: 250 * time.Millisecond},
	})
	if err != nil {
		t.Fatalf("NewConcurrencyLimiter: %v", err)
	}
	releaseOne, err := limiter.Acquire(context.Background(), ConcurrencySummary, "principal-1")
	if err != nil {
		t.Fatalf("acquire one: %v", err)
	}
	if _, err := limiter.Acquire(context.Background(), ConcurrencySummary, "principal-1"); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("principal limit error = %v", err)
	}
	releaseTwo, err := limiter.Acquire(context.Background(), ConcurrencySummary, "principal-2")
	if err != nil {
		t.Fatalf("acquire two: %v", err)
	}
	if _, err := limiter.Acquire(context.Background(), ConcurrencySummary, "principal-3"); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("global limit error = %v", err)
	}
	releaseOne()
	releaseOne()
	releaseTwo()
	releaseThree, err := limiter.Acquire(context.Background(), ConcurrencySummary, "principal-3")
	if err != nil {
		t.Fatalf("acquire after release: %v", err)
	}
	releaseThree()
}

func TestQueryBudgetNilReceiversFailClosed(t *testing.T) {
	var tracker *BudgetTracker
	checks := []error{
		tracker.ConsumeCandidates(1),
		tracker.ConsumeStoreReads(1),
		tracker.ConsumeResults(1),
		tracker.ConsumeNodes(1),
		tracker.ConsumeEdges(1),
		tracker.CheckContext(nil),
	}
	for _, err := range checks {
		if !errors.Is(err, ErrQueryBudgetExceeded) {
			t.Fatalf("nil tracker error = %v", err)
		}
	}
	policy, err := NewBudgetPolicy(nil)
	if err != nil {
		t.Fatalf("NewBudgetPolicy: %v", err)
	}
	if _, _, _, err := policy.Start(nil, QueryStandard); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("nil context error = %v", err)
	}
	limiter, err := NewConcurrencyLimiter(map[ConcurrencyClass]ConcurrencyLimit{ConcurrencySummary: {Global: 1}})
	if err != nil {
		t.Fatalf("partial concurrency override: %v", err)
	}
	if _, err := limiter.Acquire(nil, ConcurrencySummary, "principal-1"); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("nil limiter context error = %v", err)
	}
	if _, err := NewConcurrencyLimiter(map[ConcurrencyClass]ConcurrencyLimit{ConcurrencySummary: {RetryAfter: time.Millisecond}}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("retry lower bound error = %v", err)
	}
}
