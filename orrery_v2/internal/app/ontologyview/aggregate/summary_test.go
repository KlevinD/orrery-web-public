package aggregate

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	contextview "orrery_v2/internal/app/ontologyview/context"
	ontologycontract "orrery_v2/internal/app/ontologyview/contract"
	governanceview "orrery_v2/internal/app/ontologyview/governance"
	lineageview "orrery_v2/internal/app/ontologyview/lineage"
	projectionview "orrery_v2/internal/app/ontologyview/projection"
)

func TestAggregateContextSummaryReadsEveryVisibleScopeAndMergesCounts(t *testing.T) {
	scopes := newTestScopeSet(t, "org-1", "system")
	calls := make(chan string, 2)
	result, err := aggregateContextSummary(context.Background(), scopes, func(_ context.Context, tenantID string) (ontologycontract.Summary[contextview.SummaryData], error) {
		calls <- tenantID
		value := int64(1)
		if tenantID == "system" {
			value = 2
		}
		observed := time.Date(2026, 8, int(value), 0, 0, 0, 0, time.UTC)
		return ontologycontract.Summary[contextview.SummaryData]{
			Data: contextview.SummaryData{
				Objects: ontologycontract.ExactCount(value), Relations: ontologycontract.ExactCount(value + 1),
				Facts: ontologycontract.ExactCount(value + 2), Evidence: ontologycontract.UnavailableCount(),
				OldestUpdatedAt: &observed, NewestUpdatedAt: &observed,
			},
			Meta: ontologycontract.SummaryMeta{AsOf: observed, DataRevision: tenantID},
		}, nil
	})
	if err != nil {
		t.Fatalf("aggregateContextSummary: %v", err)
	}
	gotCalls := []string{<-calls, <-calls}
	slices.Sort(gotCalls)
	if !slices.Equal(gotCalls, []string{"org-1", "system"}) {
		t.Fatalf("calls = %v", gotCalls)
	}
	if result.Data.Objects.Value == nil || *result.Data.Objects.Value != 3 || result.Data.Relations.Value == nil || *result.Data.Relations.Value != 5 || result.Data.Facts.Value == nil || *result.Data.Facts.Value != 7 {
		t.Fatalf("summary = %+v", result.Data)
	}
	if result.Data.Evidence.Status != ontologycontract.MetricUnavailable || result.Data.OldestUpdatedAt == nil || result.Data.NewestUpdatedAt == nil || !result.Data.OldestUpdatedAt.Before(*result.Data.NewestUpdatedAt) {
		t.Fatalf("summary bounds = %+v", result.Data)
	}
}

func TestReadScopeValuesUsesBoundedConcurrencyAndPreservesOrder(t *testing.T) {
	scopes := newTestScopeSet(t, "scope-1", "scope-2", "scope-3", "scope-4", "scope-5")
	entered := make(chan string, 5)
	release := make(chan struct{})
	type readResult struct {
		values []string
		err    error
	}
	done := make(chan readResult, 1)
	go func() {
		values, err := readScopeValues(context.Background(), scopes, func(ctx context.Context, tenantID string) (string, error) {
			entered <- tenantID
			select {
			case <-release:
				return tenantID, nil
			case <-ctx.Done():
				return "", ctx.Err()
			}
		})
		done <- readResult{values: values, err: err}
	}()

	initial := make(map[string]bool, 4)
	for len(initial) < 4 {
		select {
		case tenantID := <-entered:
			initial[tenantID] = true
		case <-time.After(time.Second):
			close(release)
			<-done
			t.Fatalf("only %d scope reads entered before release", len(initial))
		}
	}
	select {
	case tenantID := <-entered:
		close(release)
		<-done
		t.Fatalf("fifth scope %q entered before the four-read window was released", tenantID)
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	result := <-done
	if result.err != nil {
		t.Fatalf("readScopeValues: %v", result.err)
	}
	want := []string{"scope-1", "scope-2", "scope-3", "scope-4", "scope-5"}
	if !slices.Equal(result.values, want) {
		t.Fatalf("values = %v, want %v", result.values, want)
	}
}

func TestReadScopeValuesWaitsForInternalSummaryRateLimits(t *testing.T) {
	limiter, err := ontologycontract.NewConcurrencyLimiter(map[ontologycontract.ConcurrencyClass]ontologycontract.ConcurrencyLimit{
		ontologycontract.ConcurrencySummary: {Global: 1, PerPrincipal: 1, RetryAfter: 100 * time.Millisecond},
	})
	if err != nil {
		t.Fatalf("NewConcurrencyLimiter: %v", err)
	}
	scopes := newTestScopeSet(t, "scope-1", "scope-2", "scope-3")
	values, err := readScopeValues(context.Background(), scopes, func(ctx context.Context, tenantID string) (string, error) {
		release, err := limiter.Acquire(ctx, ontologycontract.ConcurrencySummary, "user-1")
		if err != nil {
			return "", err
		}
		defer release()
		time.Sleep(10 * time.Millisecond)
		return tenantID, nil
	})
	if err != nil {
		t.Fatalf("readScopeValues: %v", err)
	}
	if want := []string{"scope-1", "scope-2", "scope-3"}; !slices.Equal(values, want) {
		t.Fatalf("values = %v, want %v", values, want)
	}
}

func TestReadScopeValuesCancelsWindowOnDelegateFailure(t *testing.T) {
	want := errors.New("backend unavailable")
	scopes := newTestScopeSet(t, "scope-1", "scope-2", "scope-3", "scope-4")
	entered := make(chan string, 4)
	fail := make(chan struct{})
	canceled := make(chan string, 3)
	done := make(chan error, 1)
	go func() {
		_, err := readScopeValues(context.Background(), scopes, func(ctx context.Context, tenantID string) (string, error) {
			entered <- tenantID
			if tenantID == "scope-1" {
				<-fail
				return "", want
			}
			<-ctx.Done()
			canceled <- tenantID
			return "", ctx.Err()
		})
		done <- err
	}()

	seen := make(map[string]bool, 4)
	for len(seen) < 4 {
		select {
		case tenantID := <-entered:
			seen[tenantID] = true
		case <-time.After(time.Second):
			close(fail)
			<-done
			t.Fatalf("only %d scope reads entered before failure", len(seen))
		}
	}
	close(fail)
	if err := <-done; !errors.Is(err, want) {
		t.Fatalf("error = %v, want %v", err, want)
	}
	for range 3 {
		select {
		case <-canceled:
		case <-time.After(time.Second):
			t.Fatal("blocked scope read did not observe cancellation")
		}
	}
}

func TestReadScopeValuesReturnsBudgetErrorWhenEveryScopeExceedsBudget(t *testing.T) {
	scopes := newTestScopeSet(t, "scope-1", "scope-2")
	_, err := readScopeValues(context.Background(), scopes, func(context.Context, string) (string, error) {
		return "", ontologycontract.ErrQueryBudgetExceeded
	})
	if !errors.Is(err, ontologycontract.ErrQueryBudgetExceeded) {
		t.Fatalf("error = %v, want query budget exceeded", err)
	}
}

func TestReadScopeValuesRetainsPartialResultsAfterOneScopeExceedsBudget(t *testing.T) {
	scopes := newTestScopeSet(t, "scope-1", "scope-2")
	values, err := readScopeValues(context.Background(), scopes, func(_ context.Context, tenantID string) (string, error) {
		if tenantID == "scope-2" {
			return "", ontologycontract.ErrQueryBudgetExceeded
		}
		return tenantID, nil
	})
	if err != nil {
		t.Fatalf("readScopeValues: %v", err)
	}
	if !slices.Equal(values, []string{"scope-1", ""}) {
		t.Fatalf("values = %v", values)
	}
}

func TestAggregateLineageGovernanceAndProjectionSummaries(t *testing.T) {
	scopes := newTestScopeSet(t, "org-1", "system")
	first := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	second := time.Date(2026, 8, 2, 0, 0, 0, 0, time.UTC)

	lineage, err := aggregateLineageSummary(context.Background(), scopes, func(_ context.Context, tenantID string) (ontologycontract.Summary[lineageview.SummaryData], error) {
		value := int64(1)
		observed := first
		if tenantID == "system" {
			value = 2
			observed = second
		}
		return ontologycontract.Summary[lineageview.SummaryData]{Data: lineageview.SummaryData{
			FactProvenanceCoverage:         ontologycontract.NewRatio(value, value+1),
			FactEvidenceReferenceCoverage:  ontologycontract.NewRatio(value, value+2),
			FactResolvableEvidenceCoverage: ontologycontract.NewRatio(value, value+3),
			ConfidenceDistribution:         lineageview.DistributionMetric{Status: ontologycontract.MetricExact, Low: int64Pointer(value), Medium: int64Pointer(value + 1), High: int64Pointer(value + 2), Unscored: int64Pointer(0)},
			Freshness:                      lineageview.FreshnessMetric{Status: ontologycontract.MetricExact, Last24Hours: int64Pointer(value), DaysOneToSeven: int64Pointer(value + 1), Older: int64Pointer(value + 2), NewestUpdatedAt: &observed, OldestUpdatedAt: &observed},
		}, Meta: ontologycontract.SummaryMeta{AsOf: observed, DataRevision: tenantID}}, nil
	})
	if err != nil || lineage.Data.FactProvenanceCoverage.Numerator == nil || *lineage.Data.FactProvenanceCoverage.Numerator != 3 || lineage.Data.ConfidenceDistribution.Low == nil || *lineage.Data.ConfidenceDistribution.Low != 3 || lineage.Data.Freshness.NewestUpdatedAt == nil || !lineage.Data.Freshness.NewestUpdatedAt.Equal(second) {
		t.Fatalf("lineage = %+v err=%v", lineage, err)
	}

	governance, err := aggregateGovernanceSummary(context.Background(), scopes, func(_ context.Context, tenantID string) (ontologycontract.Summary[governanceview.SummaryData], error) {
		applied := first
		count := int64(1)
		if tenantID == "system" {
			applied = second
			count = 2
		}
		return ontologycontract.Summary[governanceview.SummaryData]{Data: governanceview.SummaryData{
			ActionableProposals: ontologycontract.ExactCount(count),
			LatestReceipt:       governanceview.LatestReceiptMetric{Status: ontologycontract.MetricExact, Receipt: &governanceview.SuccessfulReceiptRef{ReceiptID: tenantID, AppliedAt: &applied}},
		}, Meta: ontologycontract.SummaryMeta{AsOf: applied, DataRevision: tenantID}}, nil
	})
	if err != nil || governance.Data.ActionableProposals.Value == nil || *governance.Data.ActionableProposals.Value != 3 || governance.Data.LatestReceipt.Receipt == nil || governance.Data.LatestReceipt.Receipt.ReceiptID != "system" {
		t.Fatalf("governance = %+v err=%v", governance, err)
	}

	projection, err := aggregateProjectionSummary(context.Background(), scopes, func(_ context.Context, tenantID string) (ontologycontract.Summary[projectionview.SummaryData], error) {
		observed := first
		count := int64(1)
		if tenantID == "system" {
			observed = second
			count = 2
		}
		return ontologycontract.Summary[projectionview.SummaryData]{Data: projectionview.SummaryData{
			UnresolvedAudits: ontologycontract.ExactCount(count),
			LatestActivity:   projectionview.LatestActivityMetric{Status: ontologycontract.MetricExact, ObservedAt: &observed},
		}, Meta: ontologycontract.SummaryMeta{AsOf: observed, DataRevision: tenantID}}, nil
	})
	if err != nil || projection.Data.UnresolvedAudits.Value == nil || *projection.Data.UnresolvedAudits.Value != 3 || projection.Data.LatestActivity.ObservedAt == nil || !projection.Data.LatestActivity.ObservedAt.Equal(second) {
		t.Fatalf("projection = %+v err=%v", projection, err)
	}
}

func TestAggregateSummaryStopsOnDelegateFailure(t *testing.T) {
	want := errors.New("backend unavailable")
	_, err := aggregateContextSummary(context.Background(), newTestScopeSet(t, "org-1", "system"), func(_ context.Context, tenantID string) (ontologycontract.Summary[contextview.SummaryData], error) {
		if tenantID == "org-1" {
			return ontologycontract.Summary[contextview.SummaryData]{}, want
		}
		return ontologycontract.Summary[contextview.SummaryData]{}, nil
	})
	if !errors.Is(err, want) {
		t.Fatalf("error = %v", err)
	}
}

func int64Pointer(value int64) *int64 { return &value }
