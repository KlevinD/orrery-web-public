package aggregate

import (
	"context"
	"errors"
	"sort"
	"sync"
	"time"

	contextview "orrery_v2/internal/app/ontologyview/context"
	ontologycontract "orrery_v2/internal/app/ontologyview/contract"
	governanceview "orrery_v2/internal/app/ontologyview/governance"
	lineageview "orrery_v2/internal/app/ontologyview/lineage"
	projectionview "orrery_v2/internal/app/ontologyview/projection"
)

const (
	companyOverviewRecentLimit = 5
	aggregateReadConcurrency   = 4
)

var companyOverviewDomainOrder = []string{
	"organization", "workforce", "execution", "automation", "resources",
	"knowledge", "memory", "evidence", "other",
}

func aggregateCompanyOverview(ctx context.Context, scopes ScopeSet, read func(context.Context, string) (ontologycontract.Summary[contextview.CompanyOverviewData], error)) (ontologycontract.Summary[contextview.CompanyOverviewData], error) {
	values, err := readScopeValues(ctx, scopes, read)
	if err != nil {
		return ontologycontract.Summary[contextview.CompanyOverviewData]{}, err
	}
	objects := make([]ontologycontract.CountMetric, 0, len(scopes.items))
	facts := make([]ontologycontract.CountMetric, 0, len(scopes.items))
	relations := make([]ontologycontract.CountMetric, 0, len(scopes.items))
	evidence := make([]ontologycontract.CountMetric, 0, len(scopes.items))
	newestValues := make([]*time.Time, 0, len(scopes.items))
	meta := make([]ontologycontract.SummaryMeta, 0, len(scopes.items))
	domainValues := make(map[string][]ontologycontract.CountMetric, len(companyOverviewDomainOrder))
	recent := make([]contextview.ObjectItem, 0, len(scopes.items)*companyOverviewRecentLimit)

	for index, value := range values {
		scope := scopes.items[index]
		objects = append(objects, value.Data.Objects)
		facts = append(facts, value.Data.Facts)
		relations = append(relations, value.Data.Relations)
		evidence = append(evidence, value.Data.Evidence)
		newestValues = append(newestValues, value.Data.NewestUpdatedAt)
		meta = append(meta, value.Meta)

		byDomain := make(map[string]ontologycontract.CountMetric, len(value.Data.Domains))
		for _, domain := range value.Data.Domains {
			byDomain[domain.ID] = domain.Objects
		}
		for _, domainID := range companyOverviewDomainOrder {
			metric, ok := byDomain[domainID]
			if !ok {
				metric = ontologycontract.ExactCount(0)
			}
			domainValues[domainID] = append(domainValues[domainID], metric)
		}
		for _, item := range value.Data.RecentObjects {
			item.TenantID = scope.TenantID
			recent = append(recent, item)
		}
	}

	mergedObjects := mergeCounts(objects...)
	domains := make([]contextview.OverviewDomain, 0, len(companyOverviewDomainOrder))
	domainCount := int64(0)
	for _, domainID := range companyOverviewDomainOrder {
		metric := mergeCounts(domainValues[domainID]...)
		if mergedObjects.Status != ontologycontract.MetricExact || mergedObjects.Value == nil {
			metric = ontologycontract.UnavailableCount()
		} else if metric.Status == ontologycontract.MetricExact && metric.Value != nil && *metric.Value > 0 {
			domainCount++
		}
		domains = append(domains, contextview.OverviewDomain{ID: domainID, Objects: metric})
	}
	mergedDomainCount := ontologycontract.ExactCount(domainCount)
	if mergedObjects.Status != ontologycontract.MetricExact || mergedObjects.Value == nil {
		mergedDomainCount = ontologycontract.UnavailableCount()
	}

	sort.SliceStable(recent, func(left, right int) bool {
		leftTime, rightTime := recent[left].UpdatedAt, recent[right].UpdatedAt
		switch {
		case leftTime != nil && rightTime != nil && !leftTime.Equal(*rightTime):
			return leftTime.After(*rightTime)
		case leftTime != nil && rightTime == nil:
			return true
		case leftTime == nil && rightTime != nil:
			return false
		case recent[left].TenantID != recent[right].TenantID:
			return recent[left].TenantID < recent[right].TenantID
		default:
			return recent[left].ID < recent[right].ID
		}
	})
	if len(recent) > companyOverviewRecentLimit {
		recent = recent[:companyOverviewRecentLimit]
	}

	return ontologycontract.Summary[contextview.CompanyOverviewData]{
		Data: contextview.CompanyOverviewData{
			Objects: mergedObjects, Facts: mergeCounts(facts...), Relations: mergeCounts(relations...), Evidence: mergeCounts(evidence...),
			DomainCount: mergedDomainCount, Domains: domains, RecentObjects: recent, NewestUpdatedAt: latest(newestValues...),
		},
		Meta: mergeSummaryMeta(meta...),
	}, nil
}

func aggregateContextSummary(ctx context.Context, scopes ScopeSet, read func(context.Context, string) (ontologycontract.Summary[contextview.SummaryData], error)) (ontologycontract.Summary[contextview.SummaryData], error) {
	values, err := readScopeValues(ctx, scopes, read)
	if err != nil {
		return ontologycontract.Summary[contextview.SummaryData]{}, err
	}
	objects := make([]ontologycontract.CountMetric, 0, len(values))
	relations := make([]ontologycontract.CountMetric, 0, len(values))
	facts := make([]ontologycontract.CountMetric, 0, len(values))
	evidence := make([]ontologycontract.CountMetric, 0, len(values))
	oldestValues := make([]*time.Time, 0, len(values))
	newestValues := make([]*time.Time, 0, len(values))
	meta := make([]ontologycontract.SummaryMeta, 0, len(values))
	for _, value := range values {
		objects = append(objects, value.Data.Objects)
		relations = append(relations, value.Data.Relations)
		facts = append(facts, value.Data.Facts)
		evidence = append(evidence, value.Data.Evidence)
		oldestValues = append(oldestValues, value.Data.OldestUpdatedAt)
		newestValues = append(newestValues, value.Data.NewestUpdatedAt)
		meta = append(meta, value.Meta)
	}
	return ontologycontract.Summary[contextview.SummaryData]{
		Data: contextview.SummaryData{
			Objects: mergeCounts(objects...), Relations: mergeCounts(relations...),
			Facts: mergeCounts(facts...), Evidence: mergeCounts(evidence...),
			OldestUpdatedAt: earliest(oldestValues...), NewestUpdatedAt: latest(newestValues...),
		},
		Meta: mergeSummaryMeta(meta...),
	}, nil
}

func aggregateLineageSummary(ctx context.Context, scopes ScopeSet, read func(context.Context, string) (ontologycontract.Summary[lineageview.SummaryData], error)) (ontologycontract.Summary[lineageview.SummaryData], error) {
	values, err := readScopeValues(ctx, scopes, read)
	if err != nil {
		return ontologycontract.Summary[lineageview.SummaryData]{}, err
	}
	provenance := make([]ontologycontract.RatioMetric, 0, len(values))
	evidenceReferences := make([]ontologycontract.RatioMetric, 0, len(values))
	resolvableEvidence := make([]ontologycontract.RatioMetric, 0, len(values))
	distributions := make([]lineageview.DistributionMetric, 0, len(values))
	freshness := make([]lineageview.FreshnessMetric, 0, len(values))
	meta := make([]ontologycontract.SummaryMeta, 0, len(values))
	for _, value := range values {
		provenance = append(provenance, value.Data.FactProvenanceCoverage)
		evidenceReferences = append(evidenceReferences, value.Data.FactEvidenceReferenceCoverage)
		resolvableEvidence = append(resolvableEvidence, value.Data.FactResolvableEvidenceCoverage)
		distributions = append(distributions, value.Data.ConfidenceDistribution)
		freshness = append(freshness, value.Data.Freshness)
		meta = append(meta, value.Meta)
	}
	return ontologycontract.Summary[lineageview.SummaryData]{
		Data: lineageview.SummaryData{
			FactProvenanceCoverage:         mergeRatios(provenance...),
			FactEvidenceReferenceCoverage:  mergeRatios(evidenceReferences...),
			FactResolvableEvidenceCoverage: mergeRatios(resolvableEvidence...),
			ConfidenceDistribution:         mergeDistribution(distributions...),
			Freshness:                      mergeFreshness(freshness...),
		},
		Meta: mergeSummaryMeta(meta...),
	}, nil
}

func aggregateGovernanceSummary(ctx context.Context, scopes ScopeSet, read func(context.Context, string) (ontologycontract.Summary[governanceview.SummaryData], error)) (ontologycontract.Summary[governanceview.SummaryData], error) {
	values, err := readScopeValues(ctx, scopes, read)
	if err != nil {
		return ontologycontract.Summary[governanceview.SummaryData]{}, err
	}
	counts := make([]ontologycontract.CountMetric, 0, len(values))
	latestReceipt := governanceview.LatestReceiptMetric{Status: ontologycontract.MetricExact}
	meta := make([]ontologycontract.SummaryMeta, 0, len(values))
	for _, value := range values {
		counts = append(counts, value.Data.ActionableProposals)
		meta = append(meta, value.Meta)
		metric := value.Data.LatestReceipt
		if metric.Status != ontologycontract.MetricExact || metric.Receipt != nil && metric.Receipt.AppliedAt == nil {
			latestReceipt = governanceview.LatestReceiptMetric{Status: ontologycontract.MetricUnavailable}
			continue
		}
		if latestReceipt.Status == ontologycontract.MetricExact && metric.Receipt != nil && (latestReceipt.Receipt == nil || metric.Receipt.AppliedAt.After(*latestReceipt.Receipt.AppliedAt)) {
			receipt := *metric.Receipt
			latestReceipt.Receipt = &receipt
		}
	}
	return ontologycontract.Summary[governanceview.SummaryData]{
		Data: governanceview.SummaryData{ActionableProposals: mergeCounts(counts...), LatestReceipt: latestReceipt},
		Meta: mergeSummaryMeta(meta...),
	}, nil
}

func aggregateProjectionSummary(ctx context.Context, scopes ScopeSet, read func(context.Context, string) (ontologycontract.Summary[projectionview.SummaryData], error)) (ontologycontract.Summary[projectionview.SummaryData], error) {
	values, err := readScopeValues(ctx, scopes, read)
	if err != nil {
		return ontologycontract.Summary[projectionview.SummaryData]{}, err
	}
	counts := make([]ontologycontract.CountMetric, 0, len(values))
	latestActivity := projectionview.LatestActivityMetric{Status: ontologycontract.MetricExact}
	meta := make([]ontologycontract.SummaryMeta, 0, len(values))
	for _, value := range values {
		counts = append(counts, value.Data.UnresolvedAudits)
		meta = append(meta, value.Meta)
		metric := value.Data.LatestActivity
		if metric.Status != ontologycontract.MetricExact {
			latestActivity = projectionview.LatestActivityMetric{Status: ontologycontract.MetricUnavailable}
			continue
		}
		if latestActivity.Status == ontologycontract.MetricExact && metric.ObservedAt != nil && (latestActivity.ObservedAt == nil || metric.ObservedAt.After(*latestActivity.ObservedAt)) {
			observed := metric.ObservedAt.UTC()
			latestActivity.ObservedAt = &observed
		}
	}
	return ontologycontract.Summary[projectionview.SummaryData]{
		Data: projectionview.SummaryData{UnresolvedAudits: mergeCounts(counts...), LatestActivity: latestActivity},
		Meta: mergeSummaryMeta(meta...),
	}, nil
}

func readScopeValues[T any](ctx context.Context, scopes ScopeSet, read func(context.Context, string) (T, error)) ([]T, error) {
	values := make([]T, len(scopes.items))
	succeeded := make([]bool, len(scopes.items))
	for start := 0; start < len(scopes.items); start += aggregateReadConcurrency {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		end := min(start+aggregateReadConcurrency, len(scopes.items))
		windowCtx, cancel := context.WithCancel(ctx)
		var wg sync.WaitGroup
		var firstErr error
		var firstErrOnce sync.Once
		for index := start; index < end; index++ {
			wg.Add(1)
			go func(index int) {
				defer wg.Done()
				value, err := readScopeValue(windowCtx, scopes.items[index].TenantID, read)
				if err != nil {
					if errors.Is(err, ontologycontract.ErrQueryBudgetExceeded) {
						return
					}
					firstErrOnce.Do(func() {
						firstErr = err
						cancel()
					})
					return
				}
				values[index] = value
				succeeded[index] = true
			}(index)
		}
		wg.Wait()
		cancel()
		if firstErr != nil {
			return nil, firstErr
		}
	}
	for _, ok := range succeeded {
		if ok {
			return values, nil
		}
	}
	if len(scopes.items) > 0 {
		return nil, ontologycontract.ErrQueryBudgetExceeded
	}
	return values, nil
}

func readScopeValue[T any](ctx context.Context, tenantID string, read func(context.Context, string) (T, error)) (T, error) {
	for {
		value, err := read(ctx, tenantID)
		if err == nil || !errors.Is(err, ontologycontract.ErrRateLimited) {
			return value, err
		}
		retryAfter := time.Second
		var rateLimit ontologycontract.RateLimitError
		if errors.As(err, &rateLimit) && rateLimit.RetryAfter > 0 {
			retryAfter = rateLimit.RetryAfter
		}
		timer := time.NewTimer(retryAfter)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			var zero T
			return zero, ctx.Err()
		case <-timer.C:
		}
	}
}

func mergeDistribution(values ...lineageview.DistributionMetric) lineageview.DistributionMetric {
	low := int64(0)
	medium := int64(0)
	high := int64(0)
	unscored := int64(0)
	status := ontologycontract.MetricExact
	for _, value := range values {
		if (value.Status != ontologycontract.MetricExact && value.Status != ontologycontract.MetricSampled) || value.Low == nil || value.Medium == nil || value.High == nil || value.Unscored == nil {
			return lineageview.DistributionMetric{Status: ontologycontract.MetricUnavailable}
		}
		if value.Status == ontologycontract.MetricSampled {
			status = ontologycontract.MetricSampled
		}
		low += *value.Low
		medium += *value.Medium
		high += *value.High
		unscored += *value.Unscored
	}
	return lineageview.DistributionMetric{Status: status, Low: &low, Medium: &medium, High: &high, Unscored: &unscored}
}

func mergeFreshness(values ...lineageview.FreshnessMetric) lineageview.FreshnessMetric {
	last24Hours := int64(0)
	daysOneToSeven := int64(0)
	older := int64(0)
	newestValues := make([]*time.Time, 0, len(values))
	oldestValues := make([]*time.Time, 0, len(values))
	status := ontologycontract.MetricExact
	for _, value := range values {
		if (value.Status != ontologycontract.MetricExact && value.Status != ontologycontract.MetricSampled) || value.Last24Hours == nil || value.DaysOneToSeven == nil || value.Older == nil {
			return lineageview.FreshnessMetric{Status: ontologycontract.MetricUnavailable}
		}
		if value.Status == ontologycontract.MetricSampled {
			status = ontologycontract.MetricSampled
		}
		last24Hours += *value.Last24Hours
		daysOneToSeven += *value.DaysOneToSeven
		older += *value.Older
		newestValues = append(newestValues, value.NewestUpdatedAt)
		oldestValues = append(oldestValues, value.OldestUpdatedAt)
	}
	return lineageview.FreshnessMetric{
		Status: status, Last24Hours: &last24Hours, DaysOneToSeven: &daysOneToSeven, Older: &older,
		NewestUpdatedAt: latest(newestValues...), OldestUpdatedAt: earliest(oldestValues...),
	}
}
