package ontology

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
)

const modelRunEvaluationTrendScanLimit = 200

type ModelRunEvaluationTrendQuery struct {
	TenantID             string
	Engine               string
	Status               ModelRunStatus
	RecordedFrom         time.Time
	RecordedTo           time.Time
	SnapshotAt           time.Time
	WarningsOnly         bool
	WarningCode          ModelRunEvaluationReasonCode
	ReasonCode           ModelRunEvaluationReasonCode
	MinRunTotal          int
	BaselineModelVersion string
	Limit                int
	Offset               int
}

type ModelRunEvaluationTrendPage struct {
	Buckets            []ModelRunEvaluationTrendBucket `json:"buckets"`
	Page               Page                            `json:"page"`
	SnapshotRecordedTo time.Time                       `json:"snapshot_recorded_to"`
}

type ModelRunEvaluationTrendBucket struct {
	TenantID                     string                                                     `json:"tenant_id,omitempty"`
	Engine                       string                                                     `json:"engine"`
	Model                        string                                                     `json:"model"`
	ModelVersion                 string                                                     `json:"model_version"`
	RunTotal                     int                                                        `json:"run_total"`
	StatusCounts                 map[ModelRunEvaluationGateStatus]int                       `json:"status_counts"`
	ReasonCounts                 map[ModelRunEvaluationReasonCode]int                       `json:"reason_counts"`
	WarningCounts                map[ModelRunEvaluationReasonCode]int                       `json:"warning_counts"`
	MetricThresholdResultCounts  map[string]map[ModelRunEvaluationThresholdResultStatus]int `json:"metric_threshold_result_counts"`
	ProposalTotal                int                                                        `json:"proposal_total"`
	BlockingTotal                int                                                        `json:"blocking_total"`
	WarningTotal                 int                                                        `json:"warning_total"`
	GateWarningTotal             int                                                        `json:"gate_warning_total"`
	BaselineModelVersion         string                                                     `json:"baseline_model_version,omitempty"`
	BaselineRunTotal             int                                                        `json:"baseline_run_total,omitempty"`
	RunTotalDelta                int                                                        `json:"run_total_delta"`
	RunTotalDeltaPercent         *float64                                                   `json:"run_total_delta_percent,omitempty"`
	BlockingTotalDelta           int                                                        `json:"blocking_total_delta"`
	BlockingTotalDeltaPercent    *float64                                                   `json:"blocking_total_delta_percent,omitempty"`
	WarningTotalDelta            int                                                        `json:"warning_total_delta"`
	WarningTotalDeltaPercent     *float64                                                   `json:"warning_total_delta_percent,omitempty"`
	GateWarningTotalDelta        int                                                        `json:"gate_warning_total_delta"`
	GateWarningTotalDeltaPercent *float64                                                   `json:"gate_warning_total_delta_percent,omitempty"`
	ProposalTotalDelta           int                                                        `json:"proposal_total_delta,omitempty"`
	ProposalTotalDeltaPercent    *float64                                                   `json:"proposal_total_delta_percent,omitempty"`
	BaselineProposalTotal        int                                                        `json:"baseline_proposal_total,omitempty"`
	BaselineBlockingTotal        int                                                        `json:"baseline_blocking_total,omitempty"`
	BaselineWarningTotal         int                                                        `json:"baseline_warning_total,omitempty"`
	BaselineGateWarningTotal     int                                                        `json:"baseline_gate_warning_total,omitempty"`
}

func ListModelRunEvaluationTrends(ctx context.Context, ledger ModelRunLedger, query ModelRunEvaluationTrendQuery) (ModelRunEvaluationTrendPage, error) {
	return listModelRunEvaluationTrends(ctx, ledger, nil, nil, query)
}

func ListModelRunEvaluationTrendsWithEvaluationPolicyProfiles(ctx context.Context, ledger ModelRunLedger, artifacts ModelRunEvaluationArtifactStore, policies ModelRunEvaluationPolicyProfileStore, query ModelRunEvaluationTrendQuery) (ModelRunEvaluationTrendPage, error) {
	if policies != nil && artifacts == nil {
		return ModelRunEvaluationTrendPage{}, fmt.Errorf("%w: model run evaluation artifact store is required", ErrInvalidDefinition)
	}
	return listModelRunEvaluationTrends(ctx, ledger, artifacts, policies, query)
}

func listModelRunEvaluationTrends(ctx context.Context, ledger ModelRunLedger, artifacts ModelRunEvaluationArtifactStore, policies ModelRunEvaluationPolicyProfileStore, query ModelRunEvaluationTrendQuery) (ModelRunEvaluationTrendPage, error) {
	if ledger == nil {
		return ModelRunEvaluationTrendPage{}, fmt.Errorf("%w: model run ledger is required", ErrInvalidDefinition)
	}
	normalizedQuery, err := normalizeModelRunEvaluationTrendSnapshot(query, time.Now)
	if err != nil {
		return ModelRunEvaluationTrendPage{}, err
	}
	query = normalizedQuery
	if artifacts == nil && policies == nil {
		if aggregator, ok := ledger.(ModelRunEvaluationTrendAggregator); ok {
			return aggregator.ListModelRunEvaluationTrendBuckets(ctx, query)
		}
	} else if policies != nil {
		if aggregator, ok := ledger.(ModelRunEvaluationTrendAggregator); ok {
			hasPolicies, err := hasEnabledModelRunEvaluationPolicyProfiles(ctx, policies, query)
			if err != nil {
				return ModelRunEvaluationTrendPage{}, err
			}
			if !hasPolicies {
				return aggregator.ListModelRunEvaluationTrendBuckets(ctx, query)
			}
		}
	}
	runs, err := listModelRunEvaluationTrendRuns(ctx, ledger, ModelRunQuery{
		TenantID:     strings.TrimSpace(query.TenantID),
		Engine:       query.Engine,
		Status:       query.Status,
		RecordedFrom: query.RecordedFrom,
		RecordedTo:   query.RecordedTo,
	})
	if err != nil {
		return ModelRunEvaluationTrendPage{}, err
	}
	bucketsByKey := map[string]*ModelRunEvaluationTrendBucket{}
	for _, run := range runs {
		key := modelRunEvaluationTrendKey(run)
		bucket, ok := bucketsByKey[key]
		if !ok {
			bucket = &ModelRunEvaluationTrendBucket{
				TenantID:                    strings.TrimSpace(run.TenantID),
				Engine:                      run.Engine,
				Model:                       run.Model,
				ModelVersion:                run.ModelVersion,
				StatusCounts:                map[ModelRunEvaluationGateStatus]int{},
				ReasonCounts:                map[ModelRunEvaluationReasonCode]int{},
				WarningCounts:               map[ModelRunEvaluationReasonCode]int{},
				MetricThresholdResultCounts: map[string]map[ModelRunEvaluationThresholdResultStatus]int{},
			}
			bucketsByKey[key] = bucket
		}
		gate, err := evaluationGateForModelRunEvaluationTrend(ctx, artifacts, policies, run)
		if err != nil {
			return ModelRunEvaluationTrendPage{}, err
		}
		bucket.RunTotal++
		bucket.StatusCounts[gate.Status]++
		for _, reason := range gate.Reasons {
			bucket.ReasonCounts[reason]++
		}
		for _, warning := range gate.Warnings {
			bucket.WarningCounts[warning]++
			bucket.GateWarningTotal++
		}
		recordModelRunEvaluationTrendThresholdResults(bucket, gate.MetricThresholdResults)
		bucket.ProposalTotal += run.ProposalCount
		bucket.BlockingTotal += run.AnalysisBlockingCount
		bucket.WarningTotal += run.AnalysisWarningCount
	}
	buckets := make([]ModelRunEvaluationTrendBucket, 0, len(bucketsByKey))
	allBuckets := make([]ModelRunEvaluationTrendBucket, 0, len(bucketsByKey))
	for _, bucket := range bucketsByKey {
		allBuckets = append(allBuckets, *bucket)
	}
	for _, bucket := range allBuckets {
		if modelRunEvaluationTrendBucketMatches(bucket, query) {
			buckets = append(buckets, bucket)
		}
	}
	annotateModelRunEvaluationTrendBaselineComparisons(buckets, allBuckets, query)
	sort.Slice(buckets, func(i, j int) bool {
		if buckets[i].Engine != buckets[j].Engine {
			return buckets[i].Engine < buckets[j].Engine
		}
		if buckets[i].Model != buckets[j].Model {
			return buckets[i].Model < buckets[j].Model
		}
		return buckets[i].ModelVersion < buckets[j].ModelVersion
	})
	return modelRunEvaluationTrendPage(buckets, query), nil
}

func recordModelRunEvaluationTrendThresholdResults(bucket *ModelRunEvaluationTrendBucket, results []ModelRunEvaluationMetricThresholdResult) {
	if bucket == nil {
		return
	}
	if bucket.MetricThresholdResultCounts == nil {
		bucket.MetricThresholdResultCounts = map[string]map[ModelRunEvaluationThresholdResultStatus]int{}
	}
	for _, result := range results {
		ruleID := normalizeID(result.RuleID)
		if ruleID == "" || result.Status == "" {
			continue
		}
		if bucket.MetricThresholdResultCounts[ruleID] == nil {
			bucket.MetricThresholdResultCounts[ruleID] = map[ModelRunEvaluationThresholdResultStatus]int{}
		}
		bucket.MetricThresholdResultCounts[ruleID][result.Status]++
	}
}

func normalizeModelRunEvaluationTrendSnapshot(query ModelRunEvaluationTrendQuery, now func() time.Time) (ModelRunEvaluationTrendQuery, error) {
	if !query.RecordedFrom.IsZero() {
		query.RecordedFrom = query.RecordedFrom.UTC()
	}
	if query.RecordedTo.IsZero() {
		if query.SnapshotAt.IsZero() {
			if now == nil {
				now = time.Now
			}
			query.SnapshotAt = now().UTC()
			query.RecordedTo = query.SnapshotAt.Add(time.Nanosecond)
		} else {
			query.RecordedTo = query.SnapshotAt.UTC()
			query.SnapshotAt = query.RecordedTo
		}
	} else {
		query.RecordedTo = query.RecordedTo.UTC()
		if !query.SnapshotAt.IsZero() {
			query.SnapshotAt = query.SnapshotAt.UTC()
		}
		if query.SnapshotAt.IsZero() || !query.RecordedTo.Equal(query.SnapshotAt.Add(time.Nanosecond)) {
			query.SnapshotAt = query.RecordedTo
		}
	}
	if !query.RecordedFrom.IsZero() && !query.RecordedFrom.Before(query.RecordedTo) {
		return ModelRunEvaluationTrendQuery{}, fmt.Errorf("%w: recorded_from must be before recorded_to", ErrInvalidDefinition)
	}
	return query, nil
}

func hasEnabledModelRunEvaluationPolicyProfiles(ctx context.Context, policies ModelRunEvaluationPolicyProfileStore, query ModelRunEvaluationTrendQuery) (bool, error) {
	page, err := policies.ListModelRunEvaluationPolicyProfiles(ctx, ModelRunEvaluationPolicyProfileQuery{
		TenantID:    strings.TrimSpace(query.TenantID),
		Engine:      query.Engine,
		EnabledOnly: true,
		Limit:       1,
	})
	if err != nil {
		return false, err
	}
	return page.Page.Total > 0 || len(page.Profiles) > 0, nil
}

func annotateModelRunEvaluationTrendBaselineComparisons(buckets []ModelRunEvaluationTrendBucket, allBuckets []ModelRunEvaluationTrendBucket, query ModelRunEvaluationTrendQuery) {
	baselineModelVersion := strings.TrimSpace(query.BaselineModelVersion)
	if baselineModelVersion == "" {
		return
	}
	baselines := map[string]ModelRunEvaluationTrendBucket{}
	for _, bucket := range allBuckets {
		if bucket.ModelVersion != baselineModelVersion {
			continue
		}
		baselines[modelRunEvaluationTrendBaselineKey(bucket)] = bucket
	}
	for i := range buckets {
		baseline, ok := baselines[modelRunEvaluationTrendBaselineKey(buckets[i])]
		if !ok {
			continue
		}
		buckets[i].BaselineModelVersion = baseline.ModelVersion
		buckets[i].BaselineRunTotal = baseline.RunTotal
		buckets[i].BaselineProposalTotal = baseline.ProposalTotal
		buckets[i].BaselineBlockingTotal = baseline.BlockingTotal
		buckets[i].BaselineWarningTotal = baseline.WarningTotal
		buckets[i].BaselineGateWarningTotal = baseline.GateWarningTotal
		buckets[i].RunTotalDelta = buckets[i].RunTotal - baseline.RunTotal
		buckets[i].RunTotalDeltaPercent = modelRunEvaluationTrendDeltaPercent(buckets[i].RunTotalDelta, baseline.RunTotal)
		buckets[i].ProposalTotalDelta = buckets[i].ProposalTotal - baseline.ProposalTotal
		buckets[i].ProposalTotalDeltaPercent = modelRunEvaluationTrendDeltaPercent(buckets[i].ProposalTotalDelta, baseline.ProposalTotal)
		buckets[i].BlockingTotalDelta = buckets[i].BlockingTotal - baseline.BlockingTotal
		buckets[i].BlockingTotalDeltaPercent = modelRunEvaluationTrendDeltaPercent(buckets[i].BlockingTotalDelta, baseline.BlockingTotal)
		buckets[i].WarningTotalDelta = buckets[i].WarningTotal - baseline.WarningTotal
		buckets[i].WarningTotalDeltaPercent = modelRunEvaluationTrendDeltaPercent(buckets[i].WarningTotalDelta, baseline.WarningTotal)
		buckets[i].GateWarningTotalDelta = buckets[i].GateWarningTotal - baseline.GateWarningTotal
		buckets[i].GateWarningTotalDeltaPercent = modelRunEvaluationTrendDeltaPercent(buckets[i].GateWarningTotalDelta, baseline.GateWarningTotal)
	}
}

func modelRunEvaluationTrendDeltaPercent(delta int, baseline int) *float64 {
	if baseline == 0 {
		return nil
	}
	value := float64(delta) / float64(baseline) * 100
	return &value
}

func modelRunEvaluationTrendBucketMatches(bucket ModelRunEvaluationTrendBucket, query ModelRunEvaluationTrendQuery) bool {
	if query.WarningsOnly && bucket.GateWarningTotal == 0 {
		return false
	}
	if query.WarningCode != "" && bucket.WarningCounts[query.WarningCode] == 0 {
		return false
	}
	if query.ReasonCode != "" && bucket.ReasonCounts[query.ReasonCode] == 0 {
		return false
	}
	if query.MinRunTotal > 0 && bucket.RunTotal < query.MinRunTotal {
		return false
	}
	return true
}

func listModelRunEvaluationTrendRuns(ctx context.Context, ledger ModelRunLedger, query ModelRunQuery) ([]OntologyModelRun, error) {
	query.Limit = modelRunEvaluationTrendScanLimit
	query.Offset = 0
	runs := []OntologyModelRun{}
	for {
		page, err := ledger.ListModelRuns(ctx, query)
		if err != nil {
			return nil, err
		}
		runs = append(runs, page.Runs...)
		if !page.Page.HasMore || len(page.Runs) == 0 {
			return runs, nil
		}
		query.Offset += len(page.Runs)
	}
}

func evaluationGateForModelRunEvaluationTrend(ctx context.Context, artifacts ModelRunEvaluationArtifactStore, policies ModelRunEvaluationPolicyProfileStore, run OntologyModelRun) (ModelRunEvaluationGate, error) {
	gate := evaluationGateForModelRunLedgerRecord(run)
	if artifacts == nil || policies == nil {
		return gate, nil
	}
	profile, ok, err := ResolveModelRunEvaluationPolicyProfile(ctx, policies, run)
	if err != nil {
		return ModelRunEvaluationGate{}, err
	}
	if !ok {
		return gate, nil
	}
	artifactGate, err := evaluationGateForModelRunArtifactPolicy(ctx, artifacts, run, ModelRunProposalQueryFromPolicyProfile(ModelRunProposalQuery{}, profile))
	if err != nil {
		return ModelRunEvaluationGate{}, err
	}
	return mergeModelRunEvaluationGates(gate, artifactGate), nil
}

func evaluationGateForModelRunLedgerRecord(run OntologyModelRun) ModelRunEvaluationGate {
	reasons := []ModelRunEvaluationReasonCode{}
	if run.Status == ModelRunStatusFailed {
		reasons = append(reasons, ModelRunEvaluationReasonRunFailed)
	}
	if run.Status != ModelRunStatusSucceeded && run.Status != ModelRunStatusFailed {
		reasons = append(reasons, ModelRunEvaluationReasonRunPending)
	}
	if run.ProposalCount == 0 {
		reasons = append(reasons, ModelRunEvaluationReasonNoProposals)
	}
	if run.AnalysisBlockingCount > 0 {
		reasons = append(reasons, ModelRunEvaluationReasonBlockingIssues)
	}
	if len(reasons) > 0 {
		status := ModelRunEvaluationGateBlocked
		if len(reasons) == 1 && reasons[0] == ModelRunEvaluationReasonRunPending {
			status = ModelRunEvaluationGatePending
		}
		return ModelRunEvaluationGate{Status: status, Reasons: reasons}
	}
	return ModelRunEvaluationGate{Status: ModelRunEvaluationGatePassed}
}

func modelRunEvaluationTrendKey(run OntologyModelRun) string {
	return strings.Join([]string{
		strings.TrimSpace(run.TenantID),
		normalizeID(run.Engine),
		strings.TrimSpace(run.Model),
		strings.TrimSpace(run.ModelVersion),
	}, "\x00")
}

func modelRunEvaluationTrendBaselineKey(bucket ModelRunEvaluationTrendBucket) string {
	return strings.Join([]string{
		strings.TrimSpace(bucket.TenantID),
		normalizeID(bucket.Engine),
		strings.TrimSpace(bucket.Model),
	}, "\x00")
}

func modelRunEvaluationTrendPage(buckets []ModelRunEvaluationTrendBucket, query ModelRunEvaluationTrendQuery) ModelRunEvaluationTrendPage {
	limit := normalizeMetadataProposalLimit(query.Limit)
	offset := normalizeMetadataProposalOffset(query.Offset)
	total := len(buckets)
	if offset > total {
		offset = total
	}
	end := offset + limit
	if end > total {
		end = total
	}
	return ModelRunEvaluationTrendPage{
		Buckets:            append([]ModelRunEvaluationTrendBucket(nil), buckets[offset:end]...),
		SnapshotRecordedTo: query.SnapshotAt,
		Page: Page{
			Limit:    limit,
			Offset:   offset,
			Total:    total,
			Returned: end - offset,
			HasMore:  end < total,
		},
	}
}
