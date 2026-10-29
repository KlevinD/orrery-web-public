package lineage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"orrery_v2/internal/app/contextgraph"
	"orrery_v2/internal/app/datavisibility"
	"orrery_v2/internal/app/identity"
	ontologycontract "orrery_v2/internal/app/ontologyview/contract"
)

type summaryFact struct {
	raw      contextgraph.Fact
	decision datavisibility.ResourceDecision
}

func (s *Service) Summary(ctx context.Context, principal identity.Principal, tenantID string) (ontologycontract.Summary[SummaryData], error) {
	tenantID = strings.TrimSpace(tenantID)
	if !validID(tenantID) {
		return ontologycontract.Summary[SummaryData]{}, ontologycontract.ErrInvalidRequest
	}
	authorized, err := s.authorize(ctx, principal, tenantID)
	if err != nil {
		return ontologycontract.Summary[SummaryData]{}, err
	}
	asOf := s.now().UTC()
	release, err := s.concurrency.Acquire(ctx, ontologycontract.ConcurrencySummary, authorized.Snapshot().PrincipalKey)
	if err != nil {
		return ontologycontract.Summary[SummaryData]{}, err
	}
	defer release()
	bounded, budget, cancel, err := s.budgets.Start(ctx, ontologycontract.QueryStandard)
	if err != nil {
		return ontologycontract.Summary[SummaryData]{}, err
	}
	defer cancel()
	visibility, err := s.resolveVisibility(bounded, principal, authorized, budget, datavisibility.ResourceFact, datavisibility.ResourceEvidence)
	if err != nil {
		return ontologycontract.Summary[SummaryData]{}, err
	}
	facts, complete, err := s.scanSummaryFacts(bounded, principal, visibility, budget)
	if err != nil {
		return ontologycontract.Summary[SummaryData]{}, err
	}
	data, err := s.computeSummaryData(bounded, principal, visibility, budget, facts, asOf)
	if err != nil {
		return ontologycontract.Summary[SummaryData]{}, err
	}
	if !complete {
		data = sampledSummaryData(data)
	}
	return ontologycontract.Summary[SummaryData]{
		Data: data,
		Meta: summaryMeta(asOf, authorized, lineageDataRevision(facts, complete, data)),
	}, nil
}

func (s *Service) scanSummaryFacts(ctx context.Context, principal identity.Principal, visibility visibilityState, budget *ontologycontract.BudgetTracker) ([]summaryFact, bool, error) {
	after := contextgraph.ObjectSeek{}
	out := make([]summaryFact, 0, lineageScanBatch)
	scanned := 0
	for scanned < maxSummaryFacts {
		limit := lineageScanBatch
		if remaining := maxSummaryFacts - scanned; remaining < limit {
			limit = remaining
		}
		if err := budget.ConsumeStoreReads(1); err != nil {
			return nil, false, err
		}
		page, err := s.graph.ListVisibleLineageFacts(ctx, contextgraph.VisibleFactQuery{
			TenantID: visibility.scope.Binding().TenantID, Visibility: visibility.constraint, Limit: limit, After: after,
		})
		if err != nil {
			return nil, false, queryError(ctx, budget, err)
		}
		scanned += len(page.Items)
		if err := budget.ConsumeCandidates(len(page.Items)); err != nil {
			return nil, false, err
		}
		visible, err := s.visibleSummaryFacts(ctx, principal, visibility, page.Items)
		if err != nil {
			return nil, false, err
		}
		out = append(out, visible...)
		if !page.HasMore {
			return out, true, nil
		}
		if len(page.Items) == 0 {
			return nil, false, dependencyError(contextgraph.ErrQueryInvalid)
		}
		last := page.Items[len(page.Items)-1]
		after = contextgraph.ObjectSeek{UpdatedAt: last.UpdatedAt, ID: last.ID}
	}
	return out, false, nil
}

func (s *Service) visibleSummaryFacts(ctx context.Context, principal identity.Principal, state visibilityState, rows []contextgraph.Fact) ([]summaryFact, error) {
	if len(rows) == 0 {
		return nil, nil
	}
	resources := make([]datavisibility.ResourceDescriptor, 0, len(rows))
	for _, row := range rows {
		if !contextgraph.NormalizeAccessRef(row.SourceAccessRef).Resolved() || !contextgraph.NormalizeAccessRef(row.SubjectAccessRef).Resolved() {
			return nil, dependencyError(contextgraph.ErrAccessRefConflict)
		}
		resources = append(resources, datavisibility.ResourceDescriptor{
			Key: "summary-fact:" + row.ID, Kind: datavisibility.ResourceFact,
			TenantID: row.TenantID, Type: row.Predicate,
			FieldPaths: []string{"provenance", "evidence_refs", "confidence", "updated_at"},
			AccessRefs: visibilityAccessRefs(row.SourceAccessRef, row.SubjectAccessRef), Payload: row,
		})
	}
	decision, err := s.visibility.Evaluate(ctx, datavisibility.EvaluateRequest{
		Principal: identity.NormalizePrincipal(principal), Purpose: "ontology.lineage.summary", Resources: resources,
	})
	if err != nil {
		return nil, dependencyError(err)
	}
	if err := verifyVisibilityRevision(state, datavisibility.ResourceFact, decision); err != nil {
		return nil, err
	}
	out := make([]summaryFact, 0, len(rows))
	for _, row := range rows {
		resource, ok := decision.Resource("summary-fact:" + row.ID)
		if !ok {
			return nil, dependencyError(datavisibility.ErrIncompleteDecision)
		}
		if hiddenDecision(resource) {
			continue
		}
		out = append(out, summaryFact{raw: row, decision: resource})
	}
	return out, nil
}

func (s *Service) computeSummaryData(ctx context.Context, principal identity.Principal, visibility visibilityState, budget *ontologycontract.BudgetTracker, facts []summaryFact, asOf time.Time) (SummaryData, error) {
	data := unavailableSummaryData()
	denominator := int64(len(facts))
	var provenanceCount, evidenceCount int64
	var low, medium, high, unscored int64
	var last24h, daysOneToSeven, older int64
	provenanceExact, evidenceExact, confidenceExact, freshnessExact := true, true, true, true
	var newest, oldestAt time.Time
	for _, fact := range facts {
		masked := maskedFieldSet(fact.decision)
		if masked["provenance"] {
			provenanceExact = false
		} else if strings.TrimSpace(fact.raw.Provenance.ID) != "" {
			provenanceCount++
		}
		if masked["evidence_refs"] {
			evidenceExact = false
		} else if len(fact.raw.EvidenceRefs) > 0 {
			evidenceCount++
		}
		if masked["confidence"] {
			confidenceExact = false
		} else if fact.raw.Confidence <= 0 || fact.raw.Confidence > 1 {
			// Persisted zero predates presence tracking. Treat it as unscored so
			// missing confidence remains visible as a data-quality signal.
			unscored++
		} else {
			switch {
			case fact.raw.Confidence < 0.5:
				low++
			case fact.raw.Confidence < 0.8:
				medium++
			default:
				high++
			}
		}
		if masked["updated_at"] || fact.raw.UpdatedAt.IsZero() {
			freshnessExact = false
		} else {
			updatedAt := fact.raw.UpdatedAt.UTC()
			if newest.IsZero() || updatedAt.After(newest) {
				newest = updatedAt
			}
			if oldestAt.IsZero() || updatedAt.Before(oldestAt) {
				oldestAt = updatedAt
			}
			age := asOf.Sub(updatedAt)
			switch {
			case age <= 24*time.Hour:
				last24h++
			case age <= 7*24*time.Hour:
				daysOneToSeven++
			default:
				older++
			}
		}
	}
	if provenanceExact {
		data.FactProvenanceCoverage = ontologycontract.NewRatio(provenanceCount, denominator)
	}
	if evidenceExact {
		data.FactEvidenceReferenceCoverage = ontologycontract.NewRatio(evidenceCount, denominator)
		resolvable, err := s.resolvableEvidenceCoverage(ctx, principal, visibility, budget, facts, denominator)
		if err != nil {
			return SummaryData{}, err
		}
		data.FactResolvableEvidenceCoverage = resolvable
	}
	if confidenceExact {
		data.ConfidenceDistribution = exactDistribution(low, medium, high, unscored)
	}
	if freshnessExact {
		data.Freshness = exactFreshness(last24h, daysOneToSeven, older, newest, oldestAt)
	}
	return data, nil
}

func (s *Service) resolvableEvidenceCoverage(ctx context.Context, principal identity.Principal, visibility visibilityState, budget *ontologycontract.BudgetTracker, facts []summaryFact, denominator int64) (ontologycontract.RatioMetric, error) {
	type factRefs struct {
		refs []contextgraph.Ref
	}
	byFact := make([]factRefs, 0, len(facts))
	unique := map[contextgraph.Ref]struct{}{}
	for _, fact := range facts {
		refs, err := normalizedEvidenceRefs(fact.raw.EvidenceRefs)
		if err != nil {
			return ontologycontract.RatioMetric{}, err
		}
		byFact = append(byFact, factRefs{refs: refs})
		for _, ref := range refs {
			unique[ref] = struct{}{}
		}
	}
	refs := make([]contextgraph.Ref, 0, len(unique))
	for ref := range unique {
		refs = append(refs, ref)
	}
	sort.Slice(refs, func(i, j int) bool {
		return refs[i].Kind < refs[j].Kind || refs[i].Kind == refs[j].Kind && refs[i].ID < refs[j].ID
	})
	resolved := map[contextgraph.Ref]bool{}
	for start := 0; start < len(refs); start += contextgraph.MaxQueryPageSize {
		end := start + contextgraph.MaxQueryPageSize
		if end > len(refs) {
			end = len(refs)
		}
		chunk := refs[start:end]
		if err := budget.ConsumeStoreReads(1); err != nil {
			return ontologycontract.RatioMetric{Status: ontologycontract.MetricUnavailable}, nil
		}
		ids := contextgraph.EvidenceRecordIDsForRefs(chunk)
		page, err := s.graph.ListVisibleEvidence(ctx, contextgraph.EvidenceQuery{
			TenantID: visibility.scope.Binding().TenantID, Visibility: visibility.constraint,
			SourceRefs: chunk, EvidenceIDs: ids, Limit: contextgraph.MaxQueryPageSize,
		})
		if err != nil {
			return ontologycontract.RatioMetric{}, queryError(ctx, budget, err)
		}
		if page.HasMore {
			return ontologycontract.RatioMetric{Status: ontologycontract.MetricUnavailable}, nil
		}
		if err := budget.ConsumeCandidates(len(page.Items)); err != nil {
			return ontologycontract.RatioMetric{Status: ontologycontract.MetricUnavailable}, nil
		}
		rows, err := s.visibleEvidenceRows(ctx, principal, visibility, page.Items)
		if err != nil {
			return ontologycontract.RatioMetric{}, err
		}
		for _, ref := range chunk {
			for _, row := range rows {
				if row.item.RefKind != "" && row.item.RefID != "" && contextgraph.EvidenceMatchesRef(row.raw, ref) {
					resolved[ref] = true
					break
				}
			}
		}
	}
	var numerator int64
	for _, item := range byFact {
		for _, ref := range item.refs {
			if resolved[ref] {
				numerator++
				break
			}
		}
	}
	return ontologycontract.NewRatio(numerator, denominator), nil
}

func unavailableSummaryData() SummaryData {
	return SummaryData{
		FactProvenanceCoverage:         ontologycontract.RatioMetric{Status: ontologycontract.MetricUnavailable},
		FactEvidenceReferenceCoverage:  ontologycontract.RatioMetric{Status: ontologycontract.MetricUnavailable},
		FactResolvableEvidenceCoverage: ontologycontract.RatioMetric{Status: ontologycontract.MetricUnavailable},
		ConfidenceDistribution:         DistributionMetric{Status: ontologycontract.MetricUnavailable},
		Freshness:                      FreshnessMetric{Status: ontologycontract.MetricUnavailable},
	}
}

func exactDistribution(low, medium, high, unscored int64) DistributionMetric {
	return DistributionMetric{Status: ontologycontract.MetricExact, Low: &low, Medium: &medium, High: &high, Unscored: &unscored}
}

func sampledSummaryData(data SummaryData) SummaryData {
	if data.FactProvenanceCoverage.Status == ontologycontract.MetricExact {
		data.FactProvenanceCoverage.Status = ontologycontract.MetricSampled
	}
	if data.FactEvidenceReferenceCoverage.Status == ontologycontract.MetricExact {
		data.FactEvidenceReferenceCoverage.Status = ontologycontract.MetricSampled
	}
	if data.FactResolvableEvidenceCoverage.Status == ontologycontract.MetricExact {
		data.FactResolvableEvidenceCoverage.Status = ontologycontract.MetricSampled
	}
	if data.ConfidenceDistribution.Status == ontologycontract.MetricExact {
		data.ConfidenceDistribution.Status = ontologycontract.MetricSampled
	}
	if data.Freshness.Status == ontologycontract.MetricExact {
		data.Freshness.Status = ontologycontract.MetricSampled
	}
	return data
}

func exactFreshness(last24h, daysOneToSeven, older int64, newest, oldest time.Time) FreshnessMetric {
	metric := FreshnessMetric{
		Status: ontologycontract.MetricExact, Last24Hours: &last24h,
		DaysOneToSeven: &daysOneToSeven, Older: &older,
	}
	if !newest.IsZero() {
		value := newest.UTC()
		metric.NewestUpdatedAt = &value
	}
	if !oldest.IsZero() {
		value := oldest.UTC()
		metric.OldestUpdatedAt = &value
	}
	return metric
}

func lineageDataRevision(facts []summaryFact, complete bool, data SummaryData) string {
	hash := sha256.New()
	_, _ = fmt.Fprintf(hash, "%t:%d", complete, len(facts))
	for _, fact := range facts {
		_, _ = fmt.Fprintf(hash, "\x00%s", fact.raw.ID)
	}
	if encoded, err := json.Marshal(data); err == nil {
		_, _ = hash.Write(encoded)
	}
	return hex.EncodeToString(hash.Sum(nil))
}
