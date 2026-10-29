package lineage

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	"orrery_v2/internal/app/authorization"
	"orrery_v2/internal/app/contextgraph"
	"orrery_v2/internal/app/datavisibility"
	"orrery_v2/internal/app/identity"
	"orrery_v2/internal/app/ontology"
	"orrery_v2/internal/app/ontologyscope"
	ontologycontract "orrery_v2/internal/app/ontologyview/contract"
)

type lineageTestGraph struct {
	refs       []contextgraph.AccessRef
	objects    []contextgraph.Object
	facts      []contextgraph.Fact
	assertions []contextgraph.FactAssertion
	evidence   []contextgraph.Evidence
	audits     []contextgraph.ProjectionAudit
	readCalls  int
	err        error
}

func (g *lineageTestGraph) ListTenantAccessRefs(_ context.Context, query contextgraph.AccessRefQuery) (contextgraph.AccessRefPage, error) {
	g.readCalls++
	if g.err != nil {
		return contextgraph.AccessRefPage{}, g.err
	}
	items := append([]contextgraph.AccessRef(nil), g.refs...)
	hasMore := len(items) > query.Limit
	if hasMore {
		items = items[:query.Limit]
	}
	return contextgraph.AccessRefPage{Items: items, HasMore: hasMore}, nil
}

func (g *lineageTestGraph) GetVisibleFact(_ context.Context, tenantID, factID string, visibility contextgraph.VisibilityConstraint) (contextgraph.FactDetail, error) {
	g.readCalls++
	if g.err != nil {
		return contextgraph.FactDetail{}, g.err
	}
	for _, fact := range g.facts {
		if fact.TenantID != tenantID || fact.ID != factID || !lineageRefAllowed(visibility, fact.SourceAccessRef) {
			continue
		}
		for _, object := range g.objects {
			if object.TenantID == tenantID && object.ID == fact.Subject.ID && object.ObjectType == fact.Subject.Kind && lineageRefAllowed(visibility, object.OwnerAccessRef) {
				return contextgraph.FactDetail{Fact: fact, Subject: object}, nil
			}
		}
	}
	return contextgraph.FactDetail{}, contextgraph.ErrNotFound
}

func (g *lineageTestGraph) ListVisibleLineageFacts(_ context.Context, query contextgraph.VisibleFactQuery) (contextgraph.FactPage, error) {
	g.readCalls++
	if g.err != nil {
		return contextgraph.FactPage{}, g.err
	}
	items := make([]contextgraph.Fact, 0)
	for _, fact := range g.facts {
		if fact.TenantID != query.TenantID || !lineageRefAllowed(query.Visibility, fact.SourceAccessRef) || !lineageAfterFact(fact, query.After) || !g.subjectVisible(fact, query.Visibility) {
			continue
		}
		for _, object := range g.objects {
			if object.TenantID == fact.TenantID && object.ID == fact.Subject.ID && object.ObjectType == fact.Subject.Kind {
				fact.SubjectAccessRef = object.OwnerAccessRef
				break
			}
		}
		items = append(items, fact)
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].UpdatedAt.Equal(items[j].UpdatedAt) {
			return items[i].ID < items[j].ID
		}
		return items[i].UpdatedAt.After(items[j].UpdatedAt)
	})
	hasMore := len(items) > query.Limit
	if hasMore {
		items = items[:query.Limit]
	}
	return contextgraph.FactPage{Items: items, HasMore: hasMore}, nil
}

func (g *lineageTestGraph) ListVisibleFactAssertions(_ context.Context, query contextgraph.FactAssertionQuery) (contextgraph.FactAssertionPage, error) {
	g.readCalls++
	if g.err != nil {
		return contextgraph.FactAssertionPage{}, g.err
	}
	if _, err := g.GetVisibleFact(context.Background(), query.TenantID, query.FactID, query.Visibility); err != nil {
		return contextgraph.FactAssertionPage{}, nil
	}
	items := make([]contextgraph.FactAssertion, 0)
	for _, item := range g.assertions {
		if item.Fact.TenantID != query.TenantID || item.Fact.ID != query.FactID || !lineageRefAllowed(query.Visibility, item.Fact.SourceAccessRef) || !g.subjectVisible(item.Fact, query.Visibility) {
			continue
		}
		if query.After.ID != "" && !(item.ObservedAt.Before(query.After.ObservedAt) || item.ObservedAt.Equal(query.After.ObservedAt) && item.AssertionID < query.After.ID) {
			continue
		}
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].ObservedAt.Equal(items[j].ObservedAt) {
			return items[i].AssertionID > items[j].AssertionID
		}
		return items[i].ObservedAt.After(items[j].ObservedAt)
	})
	hasMore := len(items) > query.Limit
	if hasMore {
		items = items[:query.Limit]
	}
	return contextgraph.FactAssertionPage{Items: items, HasMore: hasMore}, nil
}

func (g *lineageTestGraph) ListVisibleEvidence(_ context.Context, query contextgraph.EvidenceQuery) (contextgraph.EvidencePage, error) {
	g.readCalls++
	if g.err != nil {
		return contextgraph.EvidencePage{}, g.err
	}
	ids := map[string]bool{}
	refs := map[contextgraph.Ref]bool{}
	for _, id := range query.EvidenceIDs {
		ids[id] = true
	}
	for _, ref := range query.SourceRefs {
		refs[ref] = true
	}
	items := make([]contextgraph.Evidence, 0)
	for _, item := range g.evidence {
		if item.TenantID == query.TenantID && lineageRefAllowed(query.Visibility, item.SourceAccessRef) && (ids[item.ID] || refs[item.SourceRef]) {
			items = append(items, item)
		}
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].ObservedAt.Equal(items[j].ObservedAt) {
			return items[i].ID < items[j].ID
		}
		return items[i].ObservedAt.After(items[j].ObservedAt)
	})
	hasMore := len(items) > query.Limit
	if hasMore {
		items = items[:query.Limit]
	}
	return contextgraph.EvidencePage{Items: items, HasMore: hasMore}, nil
}

func (g *lineageTestGraph) ListVisibleProjectionTrace(_ context.Context, query contextgraph.ProjectionTraceQuery) (contextgraph.ProjectionTracePage, error) {
	g.readCalls++
	if g.err != nil {
		return contextgraph.ProjectionTracePage{}, g.err
	}
	items := make([]contextgraph.ProjectionAudit, 0)
	for _, item := range g.audits {
		if item.TenantID != query.TenantID || item.SourceEventID != query.SourceEventID || item.SourceEventType != query.SourceEventType || !lineageRefAllowed(query.Visibility, item.SourceAccessRef) {
			continue
		}
		if query.After.ID != "" && !(item.ObservedAt.Before(query.After.ObservedAt) || item.ObservedAt.Equal(query.After.ObservedAt) && item.ID < query.After.ID) {
			continue
		}
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].ObservedAt.Equal(items[j].ObservedAt) {
			return items[i].ID > items[j].ID
		}
		return items[i].ObservedAt.After(items[j].ObservedAt)
	})
	hasMore := len(items) > query.Limit
	if hasMore {
		items = items[:query.Limit]
	}
	return contextgraph.ProjectionTracePage{Items: items, HasMore: hasMore}, nil
}

func (g *lineageTestGraph) subjectVisible(fact contextgraph.Fact, visibility contextgraph.VisibilityConstraint) bool {
	for _, object := range g.objects {
		if object.TenantID == fact.TenantID && object.ID == fact.Subject.ID && object.ObjectType == fact.Subject.Kind {
			return lineageRefAllowed(visibility, object.OwnerAccessRef)
		}
	}
	return false
}

func lineageRefAllowed(visibility contextgraph.VisibilityConstraint, ref contextgraph.AccessRef) bool {
	if visibility.IsCandidateScan() {
		return contextgraph.NormalizeAccessRef(ref).Resolved()
	}
	ref = contextgraph.NormalizeAccessRef(ref)
	for _, allowed := range visibility.AllowedAccessRefs {
		if contextgraph.NormalizeAccessRef(allowed) == ref {
			return true
		}
	}
	return false
}

func lineageAfterFact(fact contextgraph.Fact, after contextgraph.ObjectSeek) bool {
	return after.ID == "" || fact.UpdatedAt.Before(after.UpdatedAt) || fact.UpdatedAt.Equal(after.UpdatedAt) && fact.ID > after.ID
}

type lineageTestEvents struct {
	values map[string]SafeEvent
	err    error
	calls  int
}

func (r *lineageTestEvents) GetSafeOntologyEvent(_ context.Context, tenantID, eventID string, expected contextgraph.AccessRef) (SafeEvent, error) {
	r.calls++
	if r.err != nil {
		return SafeEvent{}, r.err
	}
	event, ok := r.values[eventID]
	if !ok || event.TenantID != tenantID || contextgraph.NormalizeAccessRef(event.SourceAccessRef) != contextgraph.NormalizeAccessRef(expected) {
		return SafeEvent{}, ErrSafeEventUnavailable
	}
	return event, nil
}

type lineageTestRules struct {
	items []ontology.MappingRule
	err   error
}

func (r lineageTestRules) ListEnabledMappingRulesByEventType(_ context.Context, tenantID, eventType string, limit int) (ontology.MappingRuleCandidatePage, error) {
	if r.err != nil {
		return ontology.MappingRuleCandidatePage{}, r.err
	}
	items := make([]ontology.MappingRule, 0)
	for _, item := range r.items {
		if item.Enabled && item.EventType == eventType && mappingTenantVisible(item.TenantID, tenantID) {
			items = append(items, item)
		}
	}
	hasMore := len(items) > limit
	if hasMore {
		items = items[:limit]
	}
	return ontology.MappingRuleCandidatePage{Items: items, HasMore: hasMore}, nil
}

type lineageTestPolicy struct {
	hidden map[string]bool
	masked map[string][]string
	err    error
}

type lineageChangingPolicy struct {
	calls map[datavisibility.ResourceKind]int
}

func (p *lineageChangingPolicy) Evaluate(_ context.Context, req datavisibility.AdapterRequest) (datavisibility.AdapterDecision, error) {
	kind := req.Resources[0].Kind
	p.calls[kind]++
	revision := string(kind) + "-1"
	if kind == datavisibility.ResourceEvent && p.calls[kind] > 1 {
		revision = string(kind) + "-2"
	}
	out := datavisibility.AdapterDecision{Coverage: datavisibility.CoverageAll, PolicyRevision: revision}
	for _, resource := range req.Resources {
		out.Resources = append(out.Resources, datavisibility.AdapterResourceDecision{
			Key: resource.Key, AllowedFieldPaths: append([]string(nil), resource.FieldPaths...), ExistenceDisclosure: datavisibility.ExistenceVisible,
		})
	}
	return out, nil
}

func (p lineageTestPolicy) Evaluate(_ context.Context, req datavisibility.AdapterRequest) (datavisibility.AdapterDecision, error) {
	if p.err != nil {
		return datavisibility.AdapterDecision{}, p.err
	}
	out := datavisibility.AdapterDecision{Coverage: datavisibility.CoverageAll, PolicyRevision: "lineage-policy-1"}
	for _, resource := range req.Resources {
		hidden := p.hidden[resource.Key]
		for _, ref := range resource.AccessRefs {
			hidden = hidden || ref.ID == "denied"
		}
		masked := append([]string(nil), p.masked[resource.Key]...)
		maskedSet := map[string]bool{}
		for _, field := range masked {
			maskedSet[field] = true
		}
		allowed := make([]string, 0, len(resource.FieldPaths))
		for _, field := range resource.FieldPaths {
			if !maskedSet[field] {
				allowed = append(allowed, field)
			}
		}
		disclosure := datavisibility.ExistenceVisible
		if hidden {
			disclosure, allowed, masked = datavisibility.ExistenceConceal, nil, nil
		}
		out.Resources = append(out.Resources, datavisibility.AdapterResourceDecision{
			Key: resource.Key, Hidden: hidden, AllowedFieldPaths: allowed, MaskedFieldPaths: masked, ExistenceDisclosure: disclosure,
		})
	}
	return out, nil
}

type lineageTestResolver struct{ binding ontologyscope.ScopeBinding }

func (r lineageTestResolver) Resolve(_ context.Context, tenantID string) (ontologyscope.ScopeBinding, error) {
	if tenantID != r.binding.TenantID {
		return ontologyscope.ScopeBinding{}, ontologyscope.ErrBindingNotFound
	}
	return r.binding, nil
}

type lineageTestAuthorizer struct{ deny bool }

func (a lineageTestAuthorizer) AuthorizeFeature(context.Context, identity.Principal, string) error {
	if a.deny {
		return ontologyscope.ErrAccessDenied
	}
	return nil
}

func (a lineageTestAuthorizer) AuthorizeObject(context.Context, identity.Principal, string, identity.ObjectRef, string) error {
	if a.deny {
		return ontologyscope.ErrAccessDenied
	}
	return nil
}

type lineageTestSnapshots struct{}

func (lineageTestSnapshots) Capture(_ context.Context, principal identity.Principal) (authorization.AuthorizationSnapshot, error) {
	return authorization.AuthorizationSnapshot{
		PrincipalKey: authorization.PrincipalCacheKey(principal), AuthorizationRevision: "auth-1", PolicyRevision: "policy-1",
	}, nil
}

func TestServiceReturnsSafeProvenanceAndCandidateMappings(t *testing.T) {
	graph, events, rules := lineageFixture()
	service := newLineageServiceForTest(t, graph, events, rules, lineageTestAuthorizer{}, lineageTestPolicy{hidden: map[string]bool{}, masked: map[string][]string{}})
	anchor, err := service.GetProvenance(context.Background(), lineagePrincipal("user-1"), "tenant-1", "fact-1")
	if err != nil {
		t.Fatalf("GetProvenance: %v", err)
	}
	if anchor.ProvenanceStatus != ReferenceResolved || anchor.Event == nil || anchor.Event.ID != "event-1" || anchor.Event.Type != "mission.updated" {
		t.Fatalf("provenance anchor = %+v", anchor)
	}
	if anchor.Mapping.Status != MappingCandidate || len(anchor.Mapping.Candidates) != 1 || anchor.Mapping.Candidates[0].ID != "rule-1" {
		t.Fatalf("mapping resolution = %+v", anchor.Mapping)
	}
	if anchor.Mapping.Status == MappingExact {
		t.Fatal("event-type candidate must not be presented as exact")
	}
}

func TestServiceFailsClosedWhenEventPolicyRevisionChangesDuringQuery(t *testing.T) {
	graph, events, rules := lineageFixture()
	policy := &lineageChangingPolicy{calls: map[datavisibility.ResourceKind]int{}}
	service := newLineageServiceForTest(t, graph, events, rules, lineageTestAuthorizer{}, policy)

	_, err := service.GetProvenance(context.Background(), lineagePrincipal("user-1"), "tenant-1", "fact-1")
	if !errors.Is(err, ontologyscope.ErrDependencyUnavailable) || policy.calls[datavisibility.ResourceEvent] != 2 {
		t.Fatalf("event revision change err=%v calls=%+v", err, policy.calls)
	}
}

func TestServiceConcealsUnavailableEventAndExactFact(t *testing.T) {
	graph, events, rules := lineageFixture()
	policy := lineageTestPolicy{hidden: map[string]bool{"event:event-1": true}, masked: map[string][]string{}}
	service := newLineageServiceForTest(t, graph, events, rules, lineageTestAuthorizer{}, policy)
	anchor, err := service.GetProvenance(context.Background(), lineagePrincipal("user-1"), "tenant-1", "fact-1")
	if err != nil || anchor.ProvenanceStatus != ReferenceUnavailable || anchor.Event != nil || anchor.Mapping.Status != MappingUnresolved {
		t.Fatalf("concealed event anchor = %+v, err=%v", anchor, err)
	}
	if _, err := service.GetProvenance(context.Background(), lineagePrincipal("user-1"), "tenant-1", "missing"); !errors.Is(err, ontologycontract.ErrUnavailable) {
		t.Fatalf("missing fact error = %v", err)
	}
	if _, err := service.GetProvenance(context.Background(), lineagePrincipal("user-1"), "tenant-2", "fact-1"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("wrong tenant error = %v", err)
	}
}

func TestServiceRejectsEventSourceAccessMismatch(t *testing.T) {
	graph, events, rules := lineageFixture()
	event := events.values["event-1"]
	event.SourceAccessRef = contextgraph.AccessRef{Kind: "mission", ID: "denied", TenantID: "auth"}
	events.values["event-1"] = event
	service := newLineageServiceForTest(t, graph, events, rules, lineageTestAuthorizer{}, lineageTestPolicy{hidden: map[string]bool{}, masked: map[string][]string{}})
	anchor, err := service.GetProvenance(context.Background(), lineagePrincipal("user-1"), "tenant-1", "fact-1")
	if err != nil || anchor.ProvenanceStatus != ReferenceUnavailable || anchor.Event != nil {
		t.Fatalf("mismatched event source anchor = %+v, err=%v", anchor, err)
	}
}

func TestServicePaginatesAndMasksAssertions(t *testing.T) {
	graph, events, rules := lineageFixture()
	policy := lineageTestPolicy{hidden: map[string]bool{}, masked: map[string][]string{"assertion:a-3": {"value"}}}
	service := newLineageServiceForTest(t, graph, events, rules, lineageTestAuthorizer{}, policy)
	first, err := service.ListAssertions(context.Background(), lineagePrincipal("user-1"), FactPageQuery{TenantID: "tenant-1", FactID: "fact-1", Limit: 1})
	if err != nil || len(first.Items) != 1 || first.Items[0].ID != "a-3" || first.Items[0].Value != nil || !first.Page.HasMore {
		t.Fatalf("first assertion page = %+v, err=%v", first, err)
	}
	second, err := service.ListAssertions(context.Background(), lineagePrincipal("user-1"), FactPageQuery{TenantID: "tenant-1", FactID: "fact-1", Limit: 1, Cursor: first.Page.NextCursor})
	if err != nil || len(second.Items) != 1 || second.Items[0].ID != "a-2" {
		t.Fatalf("second assertion page = %+v, err=%v", second, err)
	}
	if _, err := service.ListAssertions(context.Background(), lineagePrincipal("user-2"), FactPageQuery{TenantID: "tenant-1", FactID: "fact-1", Limit: 1, Cursor: first.Page.NextCursor}); !errors.Is(err, ontologycontract.ErrCursorStale) {
		t.Fatalf("cross-principal cursor error = %v", err)
	}
	if !strings.HasPrefix(first.Page.NextCursor, "ocs1.") {
		t.Fatalf("candidate continuation is not stateful: %q", first.Page.NextCursor)
	}
}

func TestServiceAssertionsFailClosedAtCandidateScanBoundary(t *testing.T) {
	graph, events, rules := lineageFixture()
	base := graph.assertions[0].Fact
	graph.assertions = nil
	hidden := make(map[string]bool, lineageCandidateScanBatches*lineageScanBatch)
	for index := 0; index < lineageCandidateScanBatches*lineageScanBatch; index++ {
		id := fmt.Sprintf("hidden-%04d", index)
		graph.assertions = append(graph.assertions, contextgraph.FactAssertion{
			AssertionID: id,
			Fact:        base,
			ObservedAt:  time.Unix(int64(10_000-index), 0).UTC(),
		})
		hidden["assertion:"+id] = true
	}
	graph.assertions = append(graph.assertions, contextgraph.FactAssertion{
		AssertionID: "visible-after-boundary",
		Fact:        base,
		ObservedAt:  time.Unix(1, 0).UTC(),
	})
	service := newLineageServiceForTest(t, graph, events, rules, lineageTestAuthorizer{}, lineageTestPolicy{hidden: hidden, masked: map[string][]string{}})

	page, err := service.ListAssertions(context.Background(), lineagePrincipal("user-1"), FactPageQuery{
		TenantID: "tenant-1", FactID: "fact-1", Limit: 1,
	})
	if !errors.Is(err, ontologycontract.ErrQueryBudgetExceeded) || len(page.Items) != 0 || page.Page.HasMore || page.Page.NextCursor != "" {
		t.Fatalf("boundary assertion page = %+v err=%v", page, err)
	}
}

func TestServiceResolvesEvidenceWithoutLeakingMissingReferences(t *testing.T) {
	graph, events, rules := lineageFixture()
	service := newLineageServiceForTest(t, graph, events, rules, lineageTestAuthorizer{}, lineageTestPolicy{hidden: map[string]bool{}, masked: map[string][]string{}})
	page, err := service.ListEvidence(context.Background(), lineagePrincipal("user-1"), FactPageQuery{TenantID: "tenant-1", FactID: "fact-1", Limit: 10})
	if err != nil || len(page.Items) != 2 {
		t.Fatalf("evidence page = %+v, err=%v", page, err)
	}
	if page.Items[0].Status != ReferenceResolved || page.Items[0].EvidenceID != "event-1" {
		t.Fatalf("resolved evidence = %+v", page.Items[0])
	}
	if page.Items[1].Status != ReferenceUnavailable || page.Items[1].RefID != "" || page.Items[1].EvidenceID != "" {
		t.Fatalf("unavailable evidence leaked reference = %+v", page.Items[1])
	}
}

func TestServiceFiltersProjectionTraceByEventAndSourceAccess(t *testing.T) {
	graph, events, rules := lineageFixture()
	service := newLineageServiceForTest(t, graph, events, rules, lineageTestAuthorizer{}, lineageTestPolicy{hidden: map[string]bool{"audit:audit-hidden": true}, masked: map[string][]string{}})
	page, err := service.ListProjectionTrace(context.Background(), lineagePrincipal("user-1"), FactPageQuery{TenantID: "tenant-1", FactID: "fact-1", Limit: 10})
	if err != nil || len(page.Items) != 1 || page.Items[0].ID != "audit-1" {
		t.Fatalf("projection trace = %+v, err=%v", page, err)
	}
}

func TestServiceComputesDefinedLineageMetricsAndHonorsFieldMasks(t *testing.T) {
	graph, events, rules := lineageFixture()
	service := newLineageServiceForTest(t, graph, events, rules, lineageTestAuthorizer{}, lineageTestPolicy{hidden: map[string]bool{}, masked: map[string][]string{}})
	summary, err := service.Summary(context.Background(), lineagePrincipal("user-1"), "tenant-1")
	if err != nil {
		t.Fatalf("Summary: %v", err)
	}
	assertRatio(t, summary.Data.FactProvenanceCoverage, 2, 3)
	assertRatio(t, summary.Data.FactEvidenceReferenceCoverage, 2, 3)
	assertRatio(t, summary.Data.FactResolvableEvidenceCoverage, 1, 3)
	if summary.Data.ConfidenceDistribution.Status != ontologycontract.MetricExact || *summary.Data.ConfidenceDistribution.Low != 1 || *summary.Data.ConfidenceDistribution.Medium != 1 || *summary.Data.ConfidenceDistribution.High != 1 {
		t.Fatalf("confidence distribution = %+v", summary.Data.ConfidenceDistribution)
	}
	if summary.Data.Freshness.Status != ontologycontract.MetricExact || *summary.Data.Freshness.Last24Hours != 1 || *summary.Data.Freshness.DaysOneToSeven != 1 || *summary.Data.Freshness.Older != 1 {
		t.Fatalf("freshness = %+v", summary.Data.Freshness)
	}
	masked := lineageTestPolicy{hidden: map[string]bool{}, masked: map[string][]string{"summary-fact:fact-2": {"confidence", "provenance"}}}
	service = newLineageServiceForTest(t, graph, events, rules, lineageTestAuthorizer{}, masked)
	summary, err = service.Summary(context.Background(), lineagePrincipal("user-1"), "tenant-1")
	if err != nil || summary.Data.FactProvenanceCoverage.Status != ontologycontract.MetricUnavailable || summary.Data.ConfidenceDistribution.Status != ontologycontract.MetricUnavailable {
		t.Fatalf("masked summary = %+v, err=%v", summary, err)
	}
}

func TestServiceDoesNotReportPerfectCoverageForEmptyDenominator(t *testing.T) {
	graph, events, rules := lineageFixture()
	graph.facts = nil
	service := newLineageServiceForTest(t, graph, events, rules, lineageTestAuthorizer{}, lineageTestPolicy{hidden: map[string]bool{}, masked: map[string][]string{}})
	summary, err := service.Summary(context.Background(), lineagePrincipal("user-1"), "tenant-1")
	metric := summary.Data.FactProvenanceCoverage
	if err != nil || metric.Status != ontologycontract.MetricUnavailable || metric.Value != nil || metric.Numerator == nil || *metric.Numerator != 0 || metric.Denominator == nil || *metric.Denominator != 0 {
		t.Fatalf("empty-denominator summary = %+v, err=%v", summary, err)
	}
}

func TestServiceReportsUnscoredConfidenceForLegacyZero(t *testing.T) {
	graph, events, rules := lineageFixture()
	graph.facts[0].Confidence = 0
	service := newLineageServiceForTest(t, graph, events, rules, lineageTestAuthorizer{}, lineageTestPolicy{hidden: map[string]bool{}, masked: map[string][]string{}})

	summary, err := service.Summary(context.Background(), lineagePrincipal("user-1"), "tenant-1")
	if err != nil {
		t.Fatalf("Summary: %v", err)
	}
	metric := summary.Data.ConfidenceDistribution
	if metric.Status != ontologycontract.MetricExact || metric.Unscored == nil || *metric.Unscored != 1 {
		t.Fatalf("confidence distribution = %+v", summary.Data.ConfidenceDistribution)
	}
}

func TestServiceAuthorizesBeforeStoreReadAndFailsClosedOnDependencies(t *testing.T) {
	graph, events, rules := lineageFixture()
	service := newLineageServiceForTest(t, graph, events, rules, lineageTestAuthorizer{deny: true}, lineageTestPolicy{hidden: map[string]bool{}, masked: map[string][]string{}})
	if _, err := service.GetProvenance(context.Background(), lineagePrincipal("user-1"), "tenant-1", "fact-1"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("denied provenance error = %v", err)
	}
	if graph.readCalls != 0 {
		t.Fatalf("store read before scope authorization = %d", graph.readCalls)
	}
	service = newLineageServiceForTest(t, graph, events, rules, lineageTestAuthorizer{}, lineageTestPolicy{hidden: map[string]bool{}, masked: map[string][]string{}})
	graph.err = errors.New("database unavailable")
	if _, err := service.Summary(context.Background(), lineagePrincipal("user-1"), "tenant-1"); !errors.Is(err, ontologyscope.ErrDependencyUnavailable) {
		t.Fatalf("dependency summary error = %v", err)
	}
}

func TestServiceSummaryScansMoreThanLegacyAccessRefLimitInBatches(t *testing.T) {
	graph, events, rules := lineageFixture()
	graph.refs = make([]contextgraph.AccessRef, contextgraph.MaxVisibilityAccessRefs+1)
	graph.facts = nil
	for index := range graph.refs {
		graph.refs[index] = contextgraph.AccessRef{Kind: "mission", ID: fmt.Sprintf("ref-%03d", index), TenantID: "auth"}
		graph.facts = append(graph.facts, contextgraph.Fact{
			ID: fmt.Sprintf("fact-%03d", index), TenantID: "tenant-1", Subject: contextgraph.Ref{Kind: "mission", ID: "mission-1"},
			Predicate: "status", SourceAccessRef: graph.refs[index], UpdatedAt: time.Unix(int64(index+1), 0).UTC(),
		})
	}
	service := newLineageServiceForTest(t, graph, events, rules, lineageTestAuthorizer{}, lineageTestPolicy{hidden: map[string]bool{}, masked: map[string][]string{}})
	summary, err := service.Summary(context.Background(), lineagePrincipal("user-1"), "tenant-1")
	if err != nil || summary.Data.FactProvenanceCoverage.Status != ontologycontract.MetricExact || summary.Data.FactProvenanceCoverage.Denominator == nil || *summary.Data.FactProvenanceCoverage.Denominator != int64(len(graph.refs)) {
		t.Fatalf("large-cardinality summary = %+v, err=%v", summary, err)
	}
}

func TestServiceMarksSummarySampledBeyondBoundedFactScan(t *testing.T) {
	graph, events, rules := lineageFixture()
	allowed := graph.refs[0]
	graph.facts = make([]contextgraph.Fact, maxSummaryFacts+1)
	for index := range graph.facts {
		graph.facts[index] = contextgraph.Fact{
			ID: fmt.Sprintf("fact-%03d", index), TenantID: "tenant-1",
			Subject: contextgraph.Ref{Kind: "mission", ID: "mission-1"}, Predicate: "status",
			SourceAccessRef: allowed, UpdatedAt: time.Unix(int64(maxSummaryFacts-index+1), 0).UTC(),
		}
	}
	service := newLineageServiceForTest(t, graph, events, rules, lineageTestAuthorizer{}, lineageTestPolicy{hidden: map[string]bool{}, masked: map[string][]string{}})
	summary, err := service.Summary(context.Background(), lineagePrincipal("user-1"), "tenant-1")
	metric := summary.Data.FactProvenanceCoverage
	if err != nil || metric.Status != ontologycontract.MetricSampled || metric.Denominator == nil || *metric.Denominator != maxSummaryFacts || summary.Data.Freshness.Status != ontologycontract.MetricSampled {
		t.Fatalf("scan-limited summary = %+v, err=%v", summary, err)
	}
}

func assertRatio(t *testing.T, metric ontologycontract.RatioMetric, numerator, denominator int64) {
	t.Helper()
	if metric.Status != ontologycontract.MetricExact || metric.Numerator == nil || *metric.Numerator != numerator || metric.Denominator == nil || *metric.Denominator != denominator {
		t.Fatalf("ratio = %+v, want %d/%d", metric, numerator, denominator)
	}
}

func newLineageServiceForTest(t *testing.T, graph *lineageTestGraph, events *lineageTestEvents, rules lineageTestRules, authorizer lineageTestAuthorizer, adapter datavisibility.Adapter) *Service {
	t.Helper()
	binding := ontologyscope.ScopeBinding{
		TenantID: "tenant-1", Kind: ontologyscope.KindCustom,
		SourceObject: identity.ObjectRef{Type: "workspace", ID: "tenant-1"}, AuthorizationTenantID: "tenant-1",
		ReadAction: "workspace.view", ManageAction: "workspace.manage", BindingVersion: 1, State: ontologyscope.BindingStateActive,
	}
	gate := ontologyscope.NewScopeAccessGate(lineageTestResolver{binding: binding}, authorizer, lineageTestSnapshots{})
	codec, err := ontologycontract.NewCursorCodec(ontologycontract.CursorCodecOptions{
		PrimaryID: "test", Keys: []ontologycontract.CursorKey{{ID: "test", Key: bytes.Repeat([]byte{9}, 32)}},
	})
	if err != nil {
		t.Fatalf("NewCursorCodec: %v", err)
	}
	stateful, err := ontologycontract.NewStateCursorStore(ontologycontract.StateCursorOptions{MaxEntries: 100})
	if err != nil {
		t.Fatalf("NewStateCursorStore: %v", err)
	}
	registrations := make([]datavisibility.Registration, 0, 12)
	for _, kind := range []datavisibility.ResourceKind{
		datavisibility.ResourceObject, datavisibility.ResourceFact, datavisibility.ResourceEvidence,
		datavisibility.ResourceEvent, datavisibility.ResourceDefinition, datavisibility.ResourceAudit,
	} {
		registrations = append(registrations, datavisibility.Register(kind, adapter))
	}
	service, err := NewService(gate, graph, events, rules, datavisibility.NewService(registrations...), CursorSet{Encrypted: codec, Stateful: stateful}, nil, nil)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	service.now = func() time.Time { return time.Date(2026, 7, 11, 12, 0, 0, 0, time.UTC) }
	return service
}

func lineagePrincipal(id string) identity.Principal {
	return identity.NormalizePrincipal(identity.Principal{
		SubjectProvider: "oidc", Subject: id, UserID: id, ViewerKind: identity.ViewerKindUser, CoarseScopes: []string{"operations.read"},
	})
}

func lineageFixture() (*lineageTestGraph, *lineageTestEvents, lineageTestRules) {
	now := time.Date(2026, 7, 11, 12, 0, 0, 0, time.UTC)
	allowed := contextgraph.AccessRef{Kind: "mission", ID: "allowed", TenantID: "auth"}
	denied := contextgraph.AccessRef{Kind: "mission", ID: "denied", TenantID: "auth"}
	subject := contextgraph.Object{ID: "mission-1", TenantID: "tenant-1", ObjectType: "mission", Label: "Mission One", OwnerAccessRef: allowed}
	facts := []contextgraph.Fact{
		{ID: "fact-1", TenantID: "tenant-1", Subject: contextgraph.Ref{Kind: "mission", ID: "mission-1"}, Predicate: "status", Value: "active", Status: "current", Confidence: 0.9, Provenance: contextgraph.Ref{Kind: "event", ID: "event-1"}, EvidenceRefs: []contextgraph.Ref{{Kind: "event", ID: "event-1"}, {Kind: "event", ID: "missing"}}, SourceAccessRef: allowed, SourceVersion: 3, UpdatedAt: now.Add(-time.Hour)},
		{ID: "fact-2", TenantID: "tenant-1", Subject: contextgraph.Ref{Kind: "mission", ID: "mission-1"}, Predicate: "risk", Value: "medium", Confidence: 0.6, SourceAccessRef: allowed, SourceVersion: 2, UpdatedAt: now.Add(-72 * time.Hour)},
		{ID: "fact-3", TenantID: "tenant-1", Subject: contextgraph.Ref{Kind: "mission", ID: "mission-1"}, Predicate: "owner", Value: "private", Confidence: 0.2, Provenance: contextgraph.Ref{Kind: "event", ID: "event-3"}, EvidenceRefs: []contextgraph.Ref{{Kind: "event", ID: "event-3"}}, SourceAccessRef: allowed, SourceVersion: 1, UpdatedAt: now.Add(-10 * 24 * time.Hour)},
		{ID: "fact-denied", TenantID: "tenant-1", Subject: contextgraph.Ref{Kind: "mission", ID: "mission-1"}, Predicate: "secret", SourceAccessRef: denied, UpdatedAt: now},
	}
	assertions := []contextgraph.FactAssertion{
		{AssertionID: "a-3", Fact: facts[0], ObservedAt: now},
		{AssertionID: "a-2", Fact: facts[0], ObservedAt: now},
		{AssertionID: "a-1", Fact: facts[0], ObservedAt: now.Add(-time.Hour)},
	}
	for index := range assertions {
		assertions[index].Fact.SubjectAccessRef = subject.OwnerAccessRef
	}
	hiddenSubject := facts[0]
	hiddenSubject.SubjectAccessRef = denied
	assertions = append(assertions, contextgraph.FactAssertion{AssertionID: "a-hidden-subject", Fact: hiddenSubject, ObservedAt: now.Add(time.Minute)})
	evidence := []contextgraph.Evidence{
		{ID: "event-1", TenantID: "tenant-1", SourceRef: contextgraph.Ref{Kind: "mission.updated", ID: "event-1"}, SourceAccessRef: allowed, Summary: "safe summary", Confidence: 1, ObservedAt: now},
		{ID: "event-3", TenantID: "tenant-1", SourceRef: contextgraph.Ref{Kind: "mission.updated", ID: "event-3"}, SourceAccessRef: denied, Summary: "hidden", Confidence: 1, ObservedAt: now},
	}
	audits := []contextgraph.ProjectionAudit{
		{ID: "audit-1", TenantID: "tenant-1", SourceEventID: "event-1", SourceEventType: "mission.updated", Status: "applied", SourceAccessRef: allowed, ObservedAt: now},
		{ID: "audit-hidden", TenantID: "tenant-1", SourceEventID: "event-1", SourceEventType: "mission.updated", Status: "skipped", SourceAccessRef: allowed, ObservedAt: now.Add(-time.Minute)},
		{ID: "audit-wrong-source", TenantID: "tenant-1", SourceEventID: "event-1", SourceEventType: "mission.updated", Status: "applied", SourceAccessRef: denied, ObservedAt: now.Add(-2 * time.Minute)},
	}
	graph := &lineageTestGraph{refs: []contextgraph.AccessRef{allowed, denied}, objects: []contextgraph.Object{subject}, facts: facts, assertions: assertions, evidence: evidence, audits: audits}
	events := &lineageTestEvents{values: map[string]SafeEvent{
		"event-1": {TenantID: "tenant-1", SourceAccessRef: allowed, Metadata: SafeEventMetadata{ID: "event-1", Type: "mission.updated", StreamID: "mission-1", Version: 3, OccurredAt: visibleTime(now)}},
		"event-3": {TenantID: "tenant-1", SourceAccessRef: allowed, Metadata: SafeEventMetadata{ID: "event-3", Type: "mission.updated", StreamID: "mission-1", Version: 1, OccurredAt: visibleTime(now)}},
	}}
	rules := lineageTestRules{items: []ontology.MappingRule{
		{ID: "rule-1", TenantID: "tenant-1", EventType: "mission.updated", Enabled: true, Version: 2, UpdatedAt: now},
		{ID: "rule-other", TenantID: "tenant-2", EventType: "mission.updated", Enabled: true},
	}}
	return graph, events, rules
}
