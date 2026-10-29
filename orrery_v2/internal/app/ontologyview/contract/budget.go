package contract

import (
	"context"
	"fmt"
	"sync"
	"time"
)

type QueryClass string

const (
	QueryStandard         QueryClass = "standard"
	QuerySchemaSummary    QueryClass = "schema_summary"
	QueryCompanyOverview  QueryClass = "company_overview"
	QuerySchemaGraph      QueryClass = "schema_graph"
	QueryNeighborhood     QueryClass = "neighborhood"
	QueryPlan             QueryClass = "governance_plan"
	QueryInferenceHistory QueryClass = "inference_history"
)

type QueryBudget struct {
	Deadline      time.Duration
	ResultLimit   int
	CandidateRows int
	StoreReads    int
	NodeLimit     int
	EdgeLimit     int
	DepthLimit    int
}

var defaultQueryBudgets = map[QueryClass]QueryBudget{
	QueryInferenceHistory: {Deadline: 10 * time.Second, NodeLimit: 25, EdgeLimit: 100, DepthLimit: 2, CandidateRows: 1600, StoreReads: 40},
	QueryStandard:         {Deadline: 3 * time.Second, ResultLimit: 100, CandidateRows: 1000, StoreReads: 10},
	// A summary scans five definition kinds inside each authorized scope. It
	// needs a bounded snapshot budget rather than a single detail-page budget.
	QuerySchemaSummary: {Deadline: 5 * time.Second, ResultLimit: 100, CandidateRows: 4096, StoreReads: 50},
	// Company overviews authorize the distinct access references represented by
	// all aggregate groups before producing counts. Enterprise scopes can have
	// more than the standard 1,000-row query budget, so keep a separate bounded
	// ceiling with room for the current production cardinality and near-term
	// growth. The overview also runs four bounded aggregate queries plus batched
	// authorization over every distinct access reference, so its deadline must
	// cover an enterprise snapshot without relaxing any cardinality ceiling.
	QueryCompanyOverview: {Deadline: 15 * time.Second, ResultLimit: 100, CandidateRows: 4096, StoreReads: 10},
	QuerySchemaGraph:     {Deadline: 5 * time.Second, CandidateRows: 1000, NodeLimit: 500, EdgeLimit: 1000, StoreReads: 10},
	QueryNeighborhood:    {Deadline: 5 * time.Second, NodeLimit: 200, EdgeLimit: 400, DepthLimit: 2, CandidateRows: 1000, StoreReads: 10},
	QueryPlan:            {Deadline: 10 * time.Second, ResultLimit: 100, CandidateRows: 1000, StoreReads: 10},
}

type BudgetPolicy struct {
	budgets map[QueryClass]QueryBudget
}

func NewBudgetPolicy(overrides map[QueryClass]QueryBudget) (*BudgetPolicy, error) {
	budgets := make(map[QueryClass]QueryBudget, len(defaultQueryBudgets))
	for class, budget := range defaultQueryBudgets {
		budgets[class] = budget
	}
	for class, override := range overrides {
		base, ok := defaultQueryBudgets[class]
		if !ok || !validBudgetOverride(base, override) {
			return nil, fmt.Errorf("%w: query budget override for %s is invalid or less restrictive", ErrInvalidRequest, class)
		}
		budgets[class] = mergeBudgetOverride(base, override)
	}
	return &BudgetPolicy{budgets: budgets}, nil
}

func (p *BudgetPolicy) Start(ctx context.Context, class QueryClass) (context.Context, *BudgetTracker, context.CancelFunc, error) {
	if p == nil || ctx == nil {
		return nil, nil, nil, fmt.Errorf("%w: query budget policy is required", ErrInvalidRequest)
	}
	budget, ok := p.budgets[class]
	if !ok || budget.Deadline <= 0 {
		return nil, nil, nil, fmt.Errorf("%w: unsupported query class %s", ErrInvalidRequest, class)
	}
	bounded, cancel := context.WithTimeout(ctx, budget.Deadline)
	return bounded, &BudgetTracker{budget: budget}, cancel, nil
}

type BudgetTracker struct {
	mu         sync.Mutex
	budget     QueryBudget
	candidates int
	reads      int
	results    int
	nodes      int
	edges      int
}

func (t *BudgetTracker) ConsumeCandidates(count int) error {
	if t == nil {
		return unavailableBudget("candidate rows")
	}
	return t.consume(count, &t.candidates, t.budget.CandidateRows, "candidate rows")
}

func (t *BudgetTracker) ConsumeStoreReads(count int) error {
	if t == nil {
		return unavailableBudget("store reads")
	}
	return t.consume(count, &t.reads, t.budget.StoreReads, "store reads")
}

func (t *BudgetTracker) ConsumeResults(count int) error {
	if t == nil {
		return unavailableBudget("results")
	}
	return t.consume(count, &t.results, t.budget.ResultLimit, "results")
}

func (t *BudgetTracker) ConsumeNodes(count int) error {
	if t == nil {
		return unavailableBudget("nodes")
	}
	return t.consume(count, &t.nodes, t.budget.NodeLimit, "nodes")
}

func (t *BudgetTracker) ConsumeEdges(count int) error {
	if t == nil {
		return unavailableBudget("edges")
	}
	return t.consume(count, &t.edges, t.budget.EdgeLimit, "edges")
}

func (t *BudgetTracker) ValidateDepth(depth int) error {
	if t == nil || depth < 0 || t.budget.DepthLimit <= 0 || depth > t.budget.DepthLimit {
		return fmt.Errorf("%w: graph depth exceeds its limit", ErrQueryBudgetExceeded)
	}
	return nil
}

func (t *BudgetTracker) CheckContext(ctx context.Context) error {
	if ctx == nil {
		return unavailableBudget("context")
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("%w: %v", ErrQueryBudgetExceeded, err)
	}
	return nil
}

func unavailableBudget(label string) error {
	return fmt.Errorf("%w: %s budget is unavailable", ErrQueryBudgetExceeded, label)
}

func (t *BudgetTracker) consume(count int, current *int, limit int, label string) error {
	if t == nil || count < 0 || current == nil || limit <= 0 {
		return fmt.Errorf("%w: %s budget is unavailable", ErrQueryBudgetExceeded, label)
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if count > limit-*current {
		return fmt.Errorf("%w: %s limit reached", ErrQueryBudgetExceeded, label)
	}
	*current += count
	return nil
}

func validBudgetOverride(base, override QueryBudget) bool {
	return validLowerDuration(base.Deadline, override.Deadline) &&
		validLowerInt(base.ResultLimit, override.ResultLimit) &&
		validLowerInt(base.CandidateRows, override.CandidateRows) &&
		validLowerInt(base.StoreReads, override.StoreReads) &&
		validLowerInt(base.NodeLimit, override.NodeLimit) &&
		validLowerInt(base.EdgeLimit, override.EdgeLimit) &&
		validLowerInt(base.DepthLimit, override.DepthLimit)
}

func validLowerDuration(base, override time.Duration) bool {
	return override == 0 || base > 0 && override > 0 && override <= base
}

func validLowerInt(base, override int) bool {
	return override == 0 || base > 0 && override > 0 && override <= base
}

func mergeBudgetOverride(base, override QueryBudget) QueryBudget {
	if override.Deadline > 0 {
		base.Deadline = override.Deadline
	}
	if override.ResultLimit > 0 {
		base.ResultLimit = override.ResultLimit
	}
	if override.CandidateRows > 0 {
		base.CandidateRows = override.CandidateRows
	}
	if override.StoreReads > 0 {
		base.StoreReads = override.StoreReads
	}
	if override.NodeLimit > 0 {
		base.NodeLimit = override.NodeLimit
	}
	if override.EdgeLimit > 0 {
		base.EdgeLimit = override.EdgeLimit
	}
	if override.DepthLimit > 0 {
		base.DepthLimit = override.DepthLimit
	}
	return base
}
