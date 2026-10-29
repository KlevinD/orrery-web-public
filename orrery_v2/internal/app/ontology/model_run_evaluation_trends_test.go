package ontology

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"
)

func TestListModelRunEvaluationTrendsAggregatesRunsByEngineAndModelVersion(t *testing.T) {
	ctx := context.Background()
	ledger := NewMemoryModelRunLedger()

	for _, run := range []OntologyModelRun{{
		TenantID:              "tenant-1",
		Engine:                "ontology_designer",
		Model:                 "gpt-5",
		ModelVersion:          "2026-06-21",
		RunID:                 "run-trend-passed",
		InputHash:             "sha256:input-1",
		PromptHash:            "sha256:prompt-1",
		Status:                ModelRunStatusSucceeded,
		ProposalCount:         2,
		AnalysisTotal:         2,
		AnalysisBlockingCount: 0,
	}, {
		TenantID:              "tenant-1",
		Engine:                "ontology_designer",
		Model:                 "gpt-5",
		ModelVersion:          "2026-06-21",
		RunID:                 "run-trend-blocked",
		InputHash:             "sha256:input-2",
		PromptHash:            "sha256:prompt-2",
		Status:                ModelRunStatusSucceeded,
		ProposalCount:         2,
		AnalysisTotal:         2,
		AnalysisBlockingCount: 1,
	}, {
		TenantID:              "tenant-2",
		Engine:                "ontology_designer",
		Model:                 "gpt-5",
		ModelVersion:          "2026-06-21",
		RunID:                 "run-trend-other-tenant",
		InputHash:             "sha256:input-3",
		PromptHash:            "sha256:prompt-3",
		Status:                ModelRunStatusSucceeded,
		ProposalCount:         1,
		AnalysisTotal:         1,
		AnalysisBlockingCount: 0,
	}} {
		if err := ledger.SaveModelRun(ctx, run); err != nil {
			t.Fatalf("SaveModelRun: %v", err)
		}
	}

	page, err := ListModelRunEvaluationTrends(ctx, ledger, ModelRunEvaluationTrendQuery{
		TenantID: "tenant-1",
		Engine:   "ontology_designer",
		Limit:    10,
	})
	if err != nil {
		t.Fatalf("ListModelRunEvaluationTrends: %v", err)
	}
	if page.Page.Total != 1 || len(page.Buckets) != 1 {
		t.Fatalf("page = %+v", page)
	}
	bucket := page.Buckets[0]
	if bucket.Engine != "ontology_designer" || bucket.Model != "gpt-5" || bucket.ModelVersion != "2026-06-21" {
		t.Fatalf("bucket = %+v", bucket)
	}
	if bucket.RunTotal != 2 ||
		bucket.StatusCounts[ModelRunEvaluationGatePassed] != 1 ||
		bucket.StatusCounts[ModelRunEvaluationGateBlocked] != 1 ||
		bucket.ReasonCounts[ModelRunEvaluationReasonBlockingIssues] != 1 {
		t.Fatalf("bucket = %+v", bucket)
	}
}

func TestListModelRunEvaluationTrendsAggregatesBeyondModelRunPageLimit(t *testing.T) {
	ctx := context.Background()
	ledger := NewMemoryModelRunLedger()

	for i := 0; i < 205; i++ {
		run := OntologyModelRun{
			TenantID:      "tenant-1",
			Engine:        "ontology_designer",
			Model:         "gpt-5",
			ModelVersion:  "2026-06-21",
			RunID:         fmt.Sprintf("run-trend-page-%03d", i),
			InputHash:     fmt.Sprintf("sha256:input-%03d", i),
			PromptHash:    fmt.Sprintf("sha256:prompt-%03d", i),
			Status:        ModelRunStatusSucceeded,
			ProposalCount: 1,
		}
		if err := ledger.SaveModelRun(ctx, run); err != nil {
			t.Fatalf("SaveModelRun: %v", err)
		}
	}

	page, err := ListModelRunEvaluationTrends(ctx, ledger, ModelRunEvaluationTrendQuery{
		TenantID: "tenant-1",
		Engine:   "ontology_designer",
		Limit:    10,
	})
	if err != nil {
		t.Fatalf("ListModelRunEvaluationTrends: %v", err)
	}
	if page.Page.Total != 1 || len(page.Buckets) != 1 {
		t.Fatalf("page = %+v", page)
	}
	bucket := page.Buckets[0]
	if bucket.RunTotal != 205 || bucket.ProposalTotal != 205 || bucket.StatusCounts[ModelRunEvaluationGatePassed] != 205 {
		t.Fatalf("bucket = %+v", bucket)
	}
}

func TestListModelRunEvaluationTrendsFiltersByRecordedWindow(t *testing.T) {
	ctx := context.Background()
	ledger := NewMemoryModelRunLedger()
	from := time.Date(2026, 6, 21, 0, 0, 0, 0, time.UTC)
	to := from.Add(24 * time.Hour)

	for _, run := range []OntologyModelRun{{
		TenantID:      "tenant-1",
		Engine:        "ontology_designer",
		Model:         "gpt-5",
		ModelVersion:  "2026-06-21",
		RunID:         "run-trend-before-window",
		InputHash:     "sha256:input-before",
		PromptHash:    "sha256:prompt-before",
		Status:        ModelRunStatusSucceeded,
		ProposalCount: 1,
		RecordedAt:    from.Add(-time.Nanosecond),
	}, {
		TenantID:      "tenant-1",
		Engine:        "ontology_designer",
		Model:         "gpt-5",
		ModelVersion:  "2026-06-21",
		RunID:         "run-trend-inside-window",
		InputHash:     "sha256:input-inside",
		PromptHash:    "sha256:prompt-inside",
		Status:        ModelRunStatusSucceeded,
		ProposalCount: 1,
		RecordedAt:    from.Add(12 * time.Hour),
	}, {
		TenantID:      "tenant-1",
		Engine:        "ontology_designer",
		Model:         "gpt-5",
		ModelVersion:  "2026-06-21",
		RunID:         "run-trend-at-window-end",
		InputHash:     "sha256:input-at-end",
		PromptHash:    "sha256:prompt-at-end",
		Status:        ModelRunStatusSucceeded,
		ProposalCount: 1,
		RecordedAt:    to,
	}} {
		if err := ledger.SaveModelRun(ctx, run); err != nil {
			t.Fatalf("SaveModelRun: %v", err)
		}
	}

	page, err := ListModelRunEvaluationTrends(ctx, ledger, ModelRunEvaluationTrendQuery{
		TenantID:     "tenant-1",
		Engine:       "ontology_designer",
		RecordedFrom: from,
		RecordedTo:   to,
		Limit:        10,
	})
	if err != nil {
		t.Fatalf("ListModelRunEvaluationTrends: %v", err)
	}
	if page.Page.Total != 1 || len(page.Buckets) != 1 {
		t.Fatalf("page = %+v", page)
	}
	bucket := page.Buckets[0]
	if bucket.RunTotal != 1 || bucket.ProposalTotal != 1 {
		t.Fatalf("bucket = %+v", bucket)
	}
}

func TestListModelRunEvaluationTrendsFiltersBucketsByMinRunTotal(t *testing.T) {
	ctx := context.Background()
	ledger := NewMemoryModelRunLedger()

	for _, run := range []OntologyModelRun{{
		TenantID:      "tenant-1",
		Engine:        "ontology_designer",
		Model:         "gpt-5",
		ModelVersion:  "2026-06-21-low-sample",
		RunID:         "run-trend-min-sample-low",
		InputHash:     "sha256:input-low",
		PromptHash:    "sha256:prompt-low",
		Status:        ModelRunStatusSucceeded,
		ProposalCount: 1,
	}, {
		TenantID:      "tenant-1",
		Engine:        "ontology_designer",
		Model:         "gpt-5",
		ModelVersion:  "2026-06-21-enough-sample",
		RunID:         "run-trend-min-sample-enough-1",
		InputHash:     "sha256:input-enough-1",
		PromptHash:    "sha256:prompt-enough-1",
		Status:        ModelRunStatusSucceeded,
		ProposalCount: 1,
	}, {
		TenantID:      "tenant-1",
		Engine:        "ontology_designer",
		Model:         "gpt-5",
		ModelVersion:  "2026-06-21-enough-sample",
		RunID:         "run-trend-min-sample-enough-2",
		InputHash:     "sha256:input-enough-2",
		PromptHash:    "sha256:prompt-enough-2",
		Status:        ModelRunStatusSucceeded,
		ProposalCount: 1,
	}} {
		if err := ledger.SaveModelRun(ctx, run); err != nil {
			t.Fatalf("SaveModelRun: %v", err)
		}
	}

	page, err := ListModelRunEvaluationTrends(ctx, ledger, ModelRunEvaluationTrendQuery{
		TenantID:    "tenant-1",
		Engine:      "ontology_designer",
		MinRunTotal: 2,
		Limit:       10,
	})
	if err != nil {
		t.Fatalf("ListModelRunEvaluationTrends: %v", err)
	}
	if page.Page.Total != 1 || len(page.Buckets) != 1 {
		t.Fatalf("page = %+v", page)
	}
	bucket := page.Buckets[0]
	if bucket.ModelVersion != "2026-06-21-enough-sample" || bucket.RunTotal != 2 {
		t.Fatalf("bucket = %+v", bucket)
	}
}

func TestListModelRunEvaluationTrendsAnnotatesBaselineComparison(t *testing.T) {
	ctx := context.Background()
	ledger := NewMemoryModelRunLedger()

	for _, run := range []OntologyModelRun{{
		TenantID:      "tenant-1",
		Engine:        "ontology_designer",
		Model:         "gpt-5",
		ModelVersion:  "2026-06-20-baseline",
		RunID:         "run-trend-baseline-1",
		InputHash:     "sha256:input-baseline-1",
		PromptHash:    "sha256:prompt-baseline-1",
		Status:        ModelRunStatusSucceeded,
		ProposalCount: 1,
	}, {
		TenantID:              "tenant-1",
		Engine:                "ontology_designer",
		Model:                 "gpt-5",
		ModelVersion:          "2026-06-20-baseline",
		RunID:                 "run-trend-baseline-2",
		InputHash:             "sha256:input-baseline-2",
		PromptHash:            "sha256:prompt-baseline-2",
		Status:                ModelRunStatusSucceeded,
		ProposalCount:         1,
		AnalysisBlockingCount: 1,
	}, {
		TenantID:      "tenant-1",
		Engine:        "ontology_designer",
		Model:         "gpt-5",
		ModelVersion:  "2026-06-21-candidate",
		RunID:         "run-trend-candidate-1",
		InputHash:     "sha256:input-candidate-1",
		PromptHash:    "sha256:prompt-candidate-1",
		Status:        ModelRunStatusSucceeded,
		ProposalCount: 1,
	}, {
		TenantID:      "tenant-1",
		Engine:        "ontology_designer",
		Model:         "gpt-5",
		ModelVersion:  "2026-06-21-candidate",
		RunID:         "run-trend-candidate-2",
		InputHash:     "sha256:input-candidate-2",
		PromptHash:    "sha256:prompt-candidate-2",
		Status:        ModelRunStatusSucceeded,
		ProposalCount: 1,
	}, {
		TenantID:      "tenant-1",
		Engine:        "ontology_designer",
		Model:         "gpt-5",
		ModelVersion:  "2026-06-21-candidate",
		RunID:         "run-trend-candidate-3",
		InputHash:     "sha256:input-candidate-3",
		PromptHash:    "sha256:prompt-candidate-3",
		Status:        ModelRunStatusSucceeded,
		ProposalCount: 1,
	}} {
		if err := ledger.SaveModelRun(ctx, run); err != nil {
			t.Fatalf("SaveModelRun: %v", err)
		}
	}

	page, err := ListModelRunEvaluationTrends(ctx, ledger, ModelRunEvaluationTrendQuery{
		TenantID:             "tenant-1",
		Engine:               "ontology_designer",
		BaselineModelVersion: "2026-06-20-baseline",
		Limit:                10,
	})
	if err != nil {
		t.Fatalf("ListModelRunEvaluationTrends: %v", err)
	}
	if page.Page.Total != 2 || len(page.Buckets) != 2 {
		t.Fatalf("page = %+v", page)
	}
	candidate := page.Buckets[1]
	if candidate.ModelVersion != "2026-06-21-candidate" {
		t.Fatalf("candidate bucket = %+v", candidate)
	}
	if candidate.BaselineModelVersion != "2026-06-20-baseline" ||
		candidate.BaselineRunTotal != 2 ||
		candidate.BaselineProposalTotal != 2 ||
		candidate.BaselineBlockingTotal != 1 ||
		candidate.BaselineWarningTotal != 0 ||
		candidate.BaselineGateWarningTotal != 0 ||
		candidate.RunTotalDelta != 1 ||
		!floatPtrEqual(candidate.RunTotalDeltaPercent, 50) ||
		candidate.ProposalTotalDelta != 1 ||
		!floatPtrEqual(candidate.ProposalTotalDeltaPercent, 50) ||
		candidate.BlockingTotalDelta != -1 ||
		!floatPtrEqual(candidate.BlockingTotalDeltaPercent, -100) ||
		candidate.WarningTotalDelta != 0 ||
		candidate.WarningTotalDeltaPercent != nil ||
		candidate.GateWarningTotalDelta != 0 ||
		candidate.GateWarningTotalDeltaPercent != nil {
		t.Fatalf("candidate baseline comparison = %+v", candidate)
	}
	baseline := page.Buckets[0]
	if baseline.ModelVersion != "2026-06-20-baseline" ||
		baseline.RunTotalDelta != 0 ||
		!floatPtrEqual(baseline.RunTotalDeltaPercent, 0) ||
		baseline.ProposalTotalDelta != 0 ||
		!floatPtrEqual(baseline.ProposalTotalDeltaPercent, 0) ||
		baseline.BlockingTotalDelta != 0 ||
		!floatPtrEqual(baseline.BlockingTotalDeltaPercent, 0) {
		t.Fatalf("baseline comparison = %+v", baseline)
	}
}

func TestModelRunEvaluationTrendBucketSerializesZeroBaselineDeltas(t *testing.T) {
	bucket := ModelRunEvaluationTrendBucket{
		Engine:                    "ontology_designer",
		Model:                     "gpt-5",
		ModelVersion:              "2026-06-21-candidate",
		RunTotal:                  3,
		StatusCounts:              map[ModelRunEvaluationGateStatus]int{},
		ReasonCounts:              map[ModelRunEvaluationReasonCode]int{},
		WarningCounts:             map[ModelRunEvaluationReasonCode]int{},
		BaselineModelVersion:      "2026-06-20-baseline",
		BaselineRunTotal:          3,
		RunTotalDelta:             0,
		RunTotalDeltaPercent:      ptrFloat64(0),
		BlockingTotalDelta:        0,
		BlockingTotalDeltaPercent: ptrFloat64(0),
		WarningTotalDelta:         0,
		GateWarningTotalDelta:     0,
		BaselineBlockingTotal:     0,
		BaselineWarningTotal:      0,
		BaselineGateWarningTotal:  0,
	}

	payload, err := json.Marshal(bucket)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var encoded map[string]any
	if err := json.Unmarshal(payload, &encoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	for _, key := range []string{"run_total_delta", "blocking_total_delta", "warning_total_delta", "gate_warning_total_delta", "run_total_delta_percent", "blocking_total_delta_percent"} {
		if value, ok := encoded[key]; !ok || value != float64(0) {
			t.Fatalf("encoded %s = %v ok=%v payload=%s", key, value, ok, string(payload))
		}
	}
}

func floatPtrEqual(value *float64, want float64) bool {
	return value != nil && *value == want
}

func ptrFloat64(value float64) *float64 {
	return &value
}

func TestNormalizeModelRunEvaluationTrendSnapshotIncludesCurrentTickForDefaultNow(t *testing.T) {
	snapshot := time.Date(2026, 6, 21, 12, 0, 0, 123, time.UTC)

	query, err := normalizeModelRunEvaluationTrendSnapshot(ModelRunEvaluationTrendQuery{}, func() time.Time {
		return snapshot
	})
	if err != nil {
		t.Fatalf("normalizeModelRunEvaluationTrendSnapshot: %v", err)
	}
	if !query.SnapshotAt.Equal(snapshot) {
		t.Fatalf("snapshot_at = %v want %v", query.SnapshotAt, snapshot)
	}
	if !query.RecordedTo.Equal(snapshot.Add(time.Nanosecond)) {
		t.Fatalf("recorded_to = %v want %v", query.RecordedTo, snapshot.Add(time.Nanosecond))
	}

	renormalized, err := normalizeModelRunEvaluationTrendSnapshot(query, func() time.Time {
		return snapshot.Add(time.Hour)
	})
	if err != nil {
		t.Fatalf("renormalize ModelRunEvaluationTrendQuery: %v", err)
	}
	if !renormalized.SnapshotAt.Equal(snapshot) || !renormalized.RecordedTo.Equal(snapshot.Add(time.Nanosecond)) {
		t.Fatalf("renormalized query = %+v", renormalized)
	}
}

func TestNormalizeModelRunEvaluationTrendSnapshotKeepsExplicitRecordedToExclusive(t *testing.T) {
	recordedTo := time.Date(2026, 6, 21, 12, 0, 0, 0, time.UTC)

	query, err := normalizeModelRunEvaluationTrendSnapshot(ModelRunEvaluationTrendQuery{RecordedTo: recordedTo}, nil)
	if err != nil {
		t.Fatalf("normalizeModelRunEvaluationTrendSnapshot: %v", err)
	}
	if !query.RecordedTo.Equal(recordedTo) || !query.SnapshotAt.Equal(recordedTo) {
		t.Fatalf("query = %+v want recorded_to and snapshot_at %v", query, recordedTo)
	}
}

func TestListModelRunEvaluationTrendsWithEvaluationPolicyProfilesUsesStoreAggregationWhenNoProfiles(t *testing.T) {
	ctx := context.Background()
	ledger := &trackingModelRunEvaluationTrendAggregator{
		page: ModelRunEvaluationTrendPage{
			Buckets: []ModelRunEvaluationTrendBucket{{
				TenantID:     "tenant-1",
				Engine:       "ontology_designer",
				Model:        "gpt-5",
				ModelVersion: "2026-06-21",
				RunTotal:     42,
				StatusCounts: map[ModelRunEvaluationGateStatus]int{
					ModelRunEvaluationGatePassed: 42,
				},
				ReasonCounts:  map[ModelRunEvaluationReasonCode]int{},
				WarningCounts: map[ModelRunEvaluationReasonCode]int{},
			}},
			Page: Page{Limit: 10, Total: 1, Returned: 1},
		},
	}
	artifacts := NewMemoryModelRunEvaluationArtifactStore()
	policies := NewMemoryModelRunEvaluationPolicyProfileStore()

	page, err := ListModelRunEvaluationTrendsWithEvaluationPolicyProfiles(ctx, ledger, artifacts, policies, ModelRunEvaluationTrendQuery{
		TenantID: "tenant-1",
		Engine:   "ontology_designer",
		Limit:    10,
	})
	if err != nil {
		t.Fatalf("ListModelRunEvaluationTrendsWithEvaluationPolicyProfiles: %v", err)
	}
	if !ledger.aggregated {
		t.Fatalf("expected trend aggregation path to be used")
	}
	if ledger.query.TenantID != "tenant-1" || ledger.query.Engine != "ontology_designer" {
		t.Fatalf("aggregated query = %+v", ledger.query)
	}
	if page.Page.Total != 1 || len(page.Buckets) != 1 || page.Buckets[0].RunTotal != 42 {
		t.Fatalf("page = %+v", page)
	}
}

type trackingModelRunEvaluationTrendAggregator struct {
	aggregated bool
	query      ModelRunEvaluationTrendQuery
	page       ModelRunEvaluationTrendPage
}

func (l *trackingModelRunEvaluationTrendAggregator) SaveModelRun(ctx context.Context, run OntologyModelRun) error {
	return nil
}

func (l *trackingModelRunEvaluationTrendAggregator) ListModelRuns(ctx context.Context, query ModelRunQuery) (ModelRunPage, error) {
	return ModelRunPage{}, fmt.Errorf("ListModelRuns should not be called")
}

func (l *trackingModelRunEvaluationTrendAggregator) ListModelRunEvaluationTrendBuckets(ctx context.Context, query ModelRunEvaluationTrendQuery) (ModelRunEvaluationTrendPage, error) {
	l.aggregated = true
	l.query = query
	return l.page, nil
}

func TestListModelRunEvaluationTrendsUsesConsistentSnapshotWhenScanningRuns(t *testing.T) {
	ctx := context.Background()
	snapshot := time.Date(2026, 6, 21, 12, 0, 0, 0, time.UTC)
	ledger := &mutatingModelRunLedger{
		runs: []OntologyModelRun{{
			TenantID:      "tenant-1",
			Engine:        "ontology_designer",
			Model:         "gpt-5",
			ModelVersion:  "2026-06-21",
			RunID:         "run-before-snapshot",
			InputHash:     "sha256:before",
			PromptHash:    "sha256:before-prompt",
			Status:        ModelRunStatusSucceeded,
			ProposalCount: 1,
			RecordedAt:    snapshot.Add(-time.Second),
		}},
		insertAfterFirstList: OntologyModelRun{
			TenantID:      "tenant-1",
			Engine:        "ontology_designer",
			Model:         "gpt-5",
			ModelVersion:  "2026-06-21",
			RunID:         "run-after-snapshot",
			InputHash:     "sha256:after",
			PromptHash:    "sha256:after-prompt",
			Status:        ModelRunStatusSucceeded,
			ProposalCount: 1,
			RecordedAt:    snapshot.Add(time.Second),
		},
	}

	page, err := ListModelRunEvaluationTrends(ctx, ledger, ModelRunEvaluationTrendQuery{
		TenantID:   "tenant-1",
		Engine:     "ontology_designer",
		SnapshotAt: snapshot,
		Limit:      10,
	})
	if err != nil {
		t.Fatalf("ListModelRunEvaluationTrends: %v", err)
	}
	if page.SnapshotRecordedTo.IsZero() || !page.SnapshotRecordedTo.Equal(snapshot) {
		t.Fatalf("snapshot_recorded_to = %v want %v", page.SnapshotRecordedTo, snapshot)
	}
	if page.Page.Total != 1 || len(page.Buckets) != 1 {
		t.Fatalf("page = %+v", page)
	}
	if page.Buckets[0].RunTotal != 1 {
		t.Fatalf("bucket = %+v", page.Buckets[0])
	}
	for i, query := range ledger.queries {
		if !query.RecordedTo.Equal(snapshot) {
			t.Fatalf("query[%d].RecordedTo = %v want %v", i, query.RecordedTo, snapshot)
		}
	}
}

type mutatingModelRunLedger struct {
	runs                 []OntologyModelRun
	insertAfterFirstList OntologyModelRun
	inserted             bool
	queries              []ModelRunQuery
}

func (l *mutatingModelRunLedger) SaveModelRun(ctx context.Context, run OntologyModelRun) error {
	l.runs = append(l.runs, run)
	return nil
}

func (l *mutatingModelRunLedger) ListModelRuns(ctx context.Context, query ModelRunQuery) (ModelRunPage, error) {
	l.queries = append(l.queries, query)
	if !l.inserted {
		l.inserted = true
		l.runs = append(l.runs, l.insertAfterFirstList)
	}
	matched := make([]OntologyModelRun, 0, len(l.runs))
	for _, run := range l.runs {
		if modelRunMatches(run, query) {
			matched = append(matched, run)
		}
	}
	sort.Slice(matched, func(i, j int) bool {
		if matched[i].RecordedAt.Equal(matched[j].RecordedAt) {
			return strings.Compare(matched[i].RunID, matched[j].RunID) < 0
		}
		return matched[i].RecordedAt.Before(matched[j].RecordedAt)
	})
	return modelRunPage(matched, query), nil
}

func TestListModelRunEvaluationTrendsFiltersBucketsByWarningAndReason(t *testing.T) {
	ctx := context.Background()
	ledger := NewMemoryModelRunLedger()
	artifacts := NewMemoryModelRunEvaluationArtifactStore()
	policies := NewMemoryModelRunEvaluationPolicyProfileStore()

	for _, run := range []OntologyModelRun{{
		TenantID:      "tenant-1",
		Engine:        "ontology_designer",
		Model:         "gpt-5",
		ModelVersion:  "2026-06-21-warning",
		RunID:         "run-trend-filter-warning",
		InputHash:     "sha256:input-warning",
		PromptHash:    "sha256:prompt-warning",
		Status:        ModelRunStatusSucceeded,
		ProposalCount: 1,
	}, {
		TenantID:              "tenant-1",
		Engine:                "ontology_designer",
		Model:                 "gpt-5",
		ModelVersion:          "2026-06-21-blocked",
		RunID:                 "run-trend-filter-blocked",
		InputHash:             "sha256:input-blocked",
		PromptHash:            "sha256:prompt-blocked",
		Status:                ModelRunStatusSucceeded,
		ProposalCount:         1,
		AnalysisBlockingCount: 1,
	}, {
		TenantID:      "tenant-1",
		Engine:        "ontology_designer",
		Model:         "gpt-5",
		ModelVersion:  "2026-06-21-clean",
		RunID:         "run-trend-filter-clean",
		InputHash:     "sha256:input-clean",
		PromptHash:    "sha256:prompt-clean",
		Status:        ModelRunStatusSucceeded,
		ProposalCount: 1,
	}} {
		if err := ledger.SaveModelRun(ctx, run); err != nil {
			t.Fatalf("SaveModelRun: %v", err)
		}
	}
	if err := policies.SaveModelRunEvaluationPolicyProfile(ctx, ModelRunEvaluationPolicyProfile{
		TenantID:              "tenant-1",
		Engine:                "ontology_designer",
		Model:                 "gpt-5",
		ModelVersion:          "2026-06-21-warning",
		RequiredArtifactKind:  "evaluation_report",
		RequiredDatasetRef:    "ontology-eval-20260621",
		RequiredPolicyVersion: "v1",
		MetricThresholds: []ModelRunEvaluationMetricThreshold{{
			MetricKey: "accuracy",
			Operator:  ModelRunEvaluationMetricOperatorGTE,
			Threshold: 0.95,
			Blocking:  false,
		}},
		Enabled: true,
	}); err != nil {
		t.Fatalf("SaveModelRunEvaluationPolicyProfile: %v", err)
	}
	if err := artifacts.SaveModelRunEvaluationArtifact(ctx, ModelRunEvaluationArtifact{
		TenantID:     "tenant-1",
		Engine:       "ontology_designer",
		RunID:        "run-trend-filter-warning",
		ArtifactKind: "evaluation_report",
		ArtifactURI:  "s3://orrery-eval/run-trend-filter-warning/report.json",
		Metadata: map[string]string{
			"dataset_ref":    "ontology-eval-20260621",
			"policy_version": "v1",
			"accuracy":       "0.91",
		},
	}); err != nil {
		t.Fatalf("SaveModelRunEvaluationArtifact: %v", err)
	}

	for _, tc := range []struct {
		name         string
		query        ModelRunEvaluationTrendQuery
		wantVersions []string
	}{{
		name: "warnings only",
		query: ModelRunEvaluationTrendQuery{
			WarningsOnly: true,
		},
		wantVersions: []string{"2026-06-21-warning"},
	}, {
		name: "warning code",
		query: ModelRunEvaluationTrendQuery{
			WarningCode: ModelRunEvaluationReasonMetricThresholdFailed,
		},
		wantVersions: []string{"2026-06-21-warning"},
	}, {
		name: "reason code",
		query: ModelRunEvaluationTrendQuery{
			ReasonCode: ModelRunEvaluationReasonBlockingIssues,
		},
		wantVersions: []string{"2026-06-21-blocked"},
	}, {
		name: "warning and reason codes are conjunctive",
		query: ModelRunEvaluationTrendQuery{
			WarningCode: ModelRunEvaluationReasonMetricThresholdFailed,
			ReasonCode:  ModelRunEvaluationReasonBlockingIssues,
		},
		wantVersions: []string{},
	}} {
		t.Run(tc.name, func(t *testing.T) {
			tc.query.TenantID = "tenant-1"
			tc.query.Engine = "ontology_designer"
			tc.query.Limit = 10
			page, err := ListModelRunEvaluationTrendsWithEvaluationPolicyProfiles(ctx, ledger, artifacts, policies, tc.query)
			if err != nil {
				t.Fatalf("ListModelRunEvaluationTrendsWithEvaluationPolicyProfiles: %v", err)
			}
			if page.Page.Total != len(tc.wantVersions) || len(page.Buckets) != len(tc.wantVersions) {
				t.Fatalf("page = %+v want versions %v", page, tc.wantVersions)
			}
			for i, want := range tc.wantVersions {
				if page.Buckets[i].ModelVersion != want {
					t.Fatalf("bucket[%d] = %+v want model_version %s", i, page.Buckets[i], want)
				}
			}
		})
	}
}

func TestListModelRunEvaluationTrendsAggregatesNonBlockingThresholdWarnings(t *testing.T) {
	ctx := context.Background()
	ledger := NewMemoryModelRunLedger()
	artifacts := NewMemoryModelRunEvaluationArtifactStore()
	policies := NewMemoryModelRunEvaluationPolicyProfileStore()

	if err := ledger.SaveModelRun(ctx, OntologyModelRun{
		TenantID:      "tenant-1",
		Engine:        "ontology_designer",
		Model:         "gpt-5",
		ModelVersion:  "2026-06-21",
		RunID:         "run-trend-warning",
		InputHash:     "sha256:input-warning",
		PromptHash:    "sha256:prompt-warning",
		Status:        ModelRunStatusSucceeded,
		ProposalCount: 1,
	}); err != nil {
		t.Fatalf("SaveModelRun: %v", err)
	}
	if err := policies.SaveModelRunEvaluationPolicyProfile(ctx, ModelRunEvaluationPolicyProfile{
		TenantID:              "tenant-1",
		Engine:                "ontology_designer",
		Model:                 "gpt-5",
		ModelVersion:          "2026-06-21",
		RequiredArtifactKind:  "evaluation_report",
		RequiredDatasetRef:    "ontology-eval-20260621",
		RequiredPolicyVersion: "v1",
		MetricThresholds: []ModelRunEvaluationMetricThreshold{{
			MetricKey: "accuracy",
			Operator:  ModelRunEvaluationMetricOperatorGTE,
			Threshold: 0.95,
			Blocking:  false,
		}},
		Enabled: true,
	}); err != nil {
		t.Fatalf("SaveModelRunEvaluationPolicyProfile: %v", err)
	}
	if err := artifacts.SaveModelRunEvaluationArtifact(ctx, ModelRunEvaluationArtifact{
		TenantID:     "tenant-1",
		Engine:       "ontology_designer",
		RunID:        "run-trend-warning",
		ArtifactKind: "evaluation_report",
		ArtifactURI:  "s3://orrery-eval/run-trend-warning/report.json",
		Metadata: map[string]string{
			"dataset_ref":    "ontology-eval-20260621",
			"policy_version": "v1",
			"accuracy":       "0.91",
		},
	}); err != nil {
		t.Fatalf("SaveModelRunEvaluationArtifact: %v", err)
	}

	page, err := ListModelRunEvaluationTrendsWithEvaluationPolicyProfiles(ctx, ledger, artifacts, policies, ModelRunEvaluationTrendQuery{
		TenantID: "tenant-1",
		Engine:   "ontology_designer",
		Limit:    10,
	})
	if err != nil {
		t.Fatalf("ListModelRunEvaluationTrendsWithEvaluationPolicyProfiles: %v", err)
	}
	if page.Page.Total != 1 || len(page.Buckets) != 1 {
		t.Fatalf("page = %+v", page)
	}
	bucket := page.Buckets[0]
	if bucket.StatusCounts[ModelRunEvaluationGatePassed] != 1 ||
		bucket.WarningCounts[ModelRunEvaluationReasonMetricThresholdFailed] != 1 ||
		bucket.GateWarningTotal != 1 ||
		bucket.ReasonCounts[ModelRunEvaluationReasonMetricThresholdFailed] != 0 {
		t.Fatalf("bucket = %+v", bucket)
	}
}

func TestListModelRunEvaluationTrendsAggregatesMetricThresholdResultCounts(t *testing.T) {
	ctx := context.Background()
	ledger := NewMemoryModelRunLedger()
	artifacts := NewMemoryModelRunEvaluationArtifactStore()
	policies := NewMemoryModelRunEvaluationPolicyProfileStore()

	if err := ledger.SaveModelRun(ctx, OntologyModelRun{
		TenantID:      "tenant-1",
		Engine:        "ontology_designer",
		Model:         "gpt-5",
		ModelVersion:  "2026-06-21",
		RunID:         "run-trend-threshold-counts",
		InputHash:     "sha256:input-threshold-counts",
		PromptHash:    "sha256:prompt-threshold-counts",
		Status:        ModelRunStatusSucceeded,
		ProposalCount: 1,
	}); err != nil {
		t.Fatalf("SaveModelRun: %v", err)
	}
	if err := policies.SaveModelRunEvaluationPolicyProfile(ctx, ModelRunEvaluationPolicyProfile{
		TenantID:              "tenant-1",
		Engine:                "ontology_designer",
		Model:                 "gpt-5",
		ModelVersion:          "2026-06-21",
		RequiredArtifactKind:  "evaluation_report",
		RequiredDatasetRef:    "ontology-eval-20260621",
		RequiredPolicyVersion: "v1",
		MetricThresholds: []ModelRunEvaluationMetricThreshold{{
			RuleID:      "accuracy-release-gate",
			DisplayName: "Accuracy release gate",
			MetricKey:   "accuracy",
			Operator:    ModelRunEvaluationMetricOperatorGTE,
			Threshold:   0.95,
			Blocking:    true,
			Severity:    ModelRunEvaluationThresholdSeverityCritical,
		}, {
			RuleID:      "drift-warning",
			DisplayName: "Drift warning",
			MetricKey:   "drift_score",
			Operator:    ModelRunEvaluationMetricOperatorLTE,
			Threshold:   0.2,
			Blocking:    false,
			Severity:    ModelRunEvaluationThresholdSeverityWarning,
		}},
		Enabled: true,
	}); err != nil {
		t.Fatalf("SaveModelRunEvaluationPolicyProfile: %v", err)
	}
	if err := artifacts.SaveModelRunEvaluationArtifact(ctx, ModelRunEvaluationArtifact{
		TenantID:     "tenant-1",
		Engine:       "ontology_designer",
		RunID:        "run-trend-threshold-counts",
		ArtifactKind: "evaluation_report",
		ArtifactURI:  "s3://orrery-eval/run-trend-threshold-counts/report.json",
		Metadata: map[string]string{
			"dataset_ref":    "ontology-eval-20260621",
			"policy_version": "v1",
			"accuracy":       "0.91",
			"drift_score":    "0.30",
		},
	}); err != nil {
		t.Fatalf("SaveModelRunEvaluationArtifact: %v", err)
	}

	page, err := ListModelRunEvaluationTrendsWithEvaluationPolicyProfiles(ctx, ledger, artifacts, policies, ModelRunEvaluationTrendQuery{
		TenantID: "tenant-1",
		Engine:   "ontology_designer",
		Limit:    10,
	})
	if err != nil {
		t.Fatalf("ListModelRunEvaluationTrendsWithEvaluationPolicyProfiles: %v", err)
	}
	if page.Page.Total != 1 || len(page.Buckets) != 1 {
		t.Fatalf("page = %+v", page)
	}
	bucket := page.Buckets[0]
	if bucket.MetricThresholdResultCounts["accuracy-release-gate"][ModelRunEvaluationThresholdResultFailed] != 1 ||
		bucket.MetricThresholdResultCounts["drift-warning"][ModelRunEvaluationThresholdResultFailed] != 1 {
		t.Fatalf("bucket threshold counts = %+v", bucket.MetricThresholdResultCounts)
	}
}
