package contextview

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

type contextTestGraph struct {
	tenantIDs         []string
	refs              []contextgraph.AccessRef
	overviewGroups    []contextgraph.ContextOverviewGroup
	overviewDelay     time.Duration
	objects           []contextgraph.Object
	facts             []contextgraph.Fact
	relations         []contextgraph.Relation
	evidence          []contextgraph.Evidence
	neighborhood      contextgraph.Neighborhood
	counts            contextgraph.VisibleContextCounts
	readCalls         int
	countCalls        int
	lastVisibility    contextgraph.VisibilityConstraint
	lastObjectQuery   contextgraph.ObjectQuery
	lastRelationQuery contextgraph.RelationQuery
	lastNeighborhood  contextgraph.NeighborhoodQuery
	err               error
}

type contextTestDefinitions struct{}

func (g *contextTestGraph) ListContextTenantIDs(_ context.Context, limit int) (contextgraph.ContextTenantPage, error) {
	if g.err != nil {
		return contextgraph.ContextTenantPage{}, g.err
	}
	tenantIDs := append([]string(nil), g.tenantIDs...)
	hasMore := len(tenantIDs) > limit
	if hasMore {
		tenantIDs = tenantIDs[:limit]
	}
	return contextgraph.ContextTenantPage{TenantIDs: tenantIDs, HasMore: hasMore}, nil
}

func (g *contextTestGraph) ListContextScopeTenantIDs(ctx context.Context, limit int) (contextgraph.ContextTenantPage, error) {
	return g.ListContextTenantIDs(ctx, limit)
}

func (contextTestDefinitions) GetRegistryDefinitions(_ context.Context, kind ontology.RegistryDefinitionKind, ids []string) ([]ontology.RegistryDefinition, error) {
	if kind != ontology.RegistryDefinitionLink {
		return nil, nil
	}
	out := make([]ontology.RegistryDefinition, 0, len(ids))
	for _, id := range ids {
		label := id
		if id == "has_task" {
			label = "使命包含任务"
		}
		value := ontology.LinkType{ID: id, DisplayName: label}
		out = append(out, ontology.RegistryDefinition{Kind: kind, ID: id, LinkType: &value})
	}
	return out, nil
}

func (g *contextTestGraph) ListContextEntityAccessRefs(_ context.Context, query contextgraph.AccessRefQuery) (contextgraph.AccessRefPage, error) {
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

func (g *contextTestGraph) CountVisibleContext(_ context.Context, _ string, visibility contextgraph.VisibilityConstraint) (contextgraph.VisibleContextCounts, error) {
	g.readCalls++
	g.countCalls++
	g.lastVisibility = visibility
	if g.err != nil {
		return contextgraph.VisibleContextCounts{}, g.err
	}
	return g.counts, nil
}

func (g *contextTestGraph) ReadContextOverviewGroups(ctx context.Context, _ string) ([]contextgraph.ContextOverviewGroup, error) {
	g.readCalls++
	if g.err != nil {
		return nil, g.err
	}
	if g.overviewDelay > 0 {
		timer := time.NewTimer(g.overviewDelay)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
	return append([]contextgraph.ContextOverviewGroup(nil), g.overviewGroups...), nil
}

func (g *contextTestGraph) ListVisibleObjects(_ context.Context, query contextgraph.ObjectQuery) (contextgraph.ObjectPage, error) {
	g.readCalls++
	g.lastVisibility = query.Visibility
	g.lastObjectQuery = query
	if g.err != nil {
		return contextgraph.ObjectPage{}, g.err
	}
	items := make([]contextgraph.Object, 0)
	for _, object := range g.objects {
		if object.TenantID != query.TenantID || !testRefAllowed(query.Visibility, object.OwnerAccessRef) || !afterObjectSeek(object.UpdatedAt, object.ID, query.After) {
			continue
		}
		if query.ObjectType != "" && object.ObjectType != query.ObjectType || query.Status != "" && object.Status != query.Status || query.SourceKind != "" && object.OwnerAccessRef.Kind != query.SourceKind {
			continue
		}
		items = append(items, object)
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
	return contextgraph.ObjectPage{Items: items, HasMore: hasMore}, nil
}

func (g *contextTestGraph) GetVisibleObject(_ context.Context, tenantID, objectID string, visibility contextgraph.VisibilityConstraint) (contextgraph.Object, error) {
	g.readCalls++
	g.lastVisibility = visibility
	if g.err != nil {
		return contextgraph.Object{}, g.err
	}
	for _, object := range g.objects {
		if object.TenantID == tenantID && object.ID == objectID && testRefAllowed(visibility, object.OwnerAccessRef) {
			return object, nil
		}
	}
	return contextgraph.Object{}, contextgraph.ErrNotFound
}

func (g *contextTestGraph) ListVisibleFacts(_ context.Context, query contextgraph.FactQuery) (contextgraph.FactPage, error) {
	g.readCalls++
	items := make([]contextgraph.Fact, 0)
	for _, fact := range g.facts {
		if fact.TenantID == query.TenantID && fact.Subject.Kind == query.ObjectType && fact.Subject.ID == query.ObjectID && testRefAllowed(query.Visibility, fact.SourceAccessRef) && afterObjectSeek(fact.UpdatedAt, fact.ID, query.After) {
			items = append(items, fact)
		}
	}
	hasMore := len(items) > query.Limit
	if hasMore {
		items = items[:query.Limit]
	}
	return contextgraph.FactPage{Items: items, HasMore: hasMore}, g.err
}

func (g *contextTestGraph) ListVisibleRelations(_ context.Context, query contextgraph.RelationQuery) (contextgraph.RelationPage, error) {
	g.readCalls++
	g.lastRelationQuery = query
	items := make([]contextgraph.Relation, 0)
	for _, relation := range g.relations {
		link := relation.Link
		if link.TenantID != query.TenantID || !testRefAllowed(query.Visibility, link.SourceAccessRef) || !testRefAllowed(query.Visibility, relation.OtherObject.OwnerAccessRef) || !afterObjectSeek(link.UpdatedAt, link.ID, query.After) {
			continue
		}
		if query.LinkType != "" && link.LinkType != query.LinkType || query.Direction != contextgraph.RelationBoth && relation.Direction != query.Direction {
			continue
		}
		items = append(items, relation)
	}
	hasMore := len(items) > query.Limit
	if hasMore {
		items = items[:query.Limit]
	}
	return contextgraph.RelationPage{Items: items, HasMore: hasMore}, g.err
}

func (g *contextTestGraph) ListVisibleEvidence(_ context.Context, query contextgraph.EvidenceQuery) (contextgraph.EvidencePage, error) {
	g.readCalls++
	refs := map[contextgraph.Ref]bool{}
	ids := map[string]bool{}
	for _, ref := range query.SourceRefs {
		refs[ref] = true
	}
	for _, id := range query.EvidenceIDs {
		ids[id] = true
	}
	items := make([]contextgraph.Evidence, 0)
	for _, item := range g.evidence {
		if item.TenantID == query.TenantID && (refs[item.SourceRef] || ids[item.ID]) && testRefAllowed(query.Visibility, item.SourceAccessRef) && afterObjectSeek(item.ObservedAt, item.ID, query.After) {
			items = append(items, item)
		}
	}
	hasMore := len(items) > query.Limit
	if hasMore {
		items = items[:query.Limit]
	}
	return contextgraph.EvidencePage{Items: items, HasMore: hasMore}, g.err
}

func (g *contextTestGraph) BuildVisibleNeighborhood(_ context.Context, query contextgraph.NeighborhoodQuery) (contextgraph.Neighborhood, error) {
	g.readCalls++
	g.lastVisibility = query.Visibility
	g.lastNeighborhood = query
	if g.err != nil {
		return contextgraph.Neighborhood{}, g.err
	}
	return g.neighborhood, nil
}

func testRefAllowed(visibility contextgraph.VisibilityConstraint, ref contextgraph.AccessRef) bool {
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

func afterObjectSeek(updatedAt time.Time, id string, after contextgraph.ObjectSeek) bool {
	return after.ID == "" || updatedAt.Before(after.UpdatedAt) || updatedAt.Equal(after.UpdatedAt) && id > after.ID
}

type contextTestResolver struct{ binding ontologyscope.ScopeBinding }

func (r contextTestResolver) Resolve(context.Context, string) (ontologyscope.ScopeBinding, error) {
	return r.binding, nil
}

type contextTestAuthorizer struct{ deny bool }

func (a contextTestAuthorizer) AuthorizeFeature(context.Context, identity.Principal, string) error {
	if a.deny {
		return ontologyscope.ErrAccessDenied
	}
	return nil
}

func (a contextTestAuthorizer) AuthorizeObject(context.Context, identity.Principal, string, identity.ObjectRef, string) error {
	if a.deny {
		return ontologyscope.ErrAccessDenied
	}
	return nil
}

type contextTestSnapshots struct{}

func (contextTestSnapshots) Capture(_ context.Context, principal identity.Principal) (authorization.AuthorizationSnapshot, error) {
	return authorization.AuthorizationSnapshot{PrincipalKey: authorization.PrincipalCacheKey(principal), AuthorizationRevision: "auth-1", PolicyRevision: "policy-1"}, nil
}

type contextTestPolicy struct {
	hidden map[string]bool
	masked map[string][]string
	filter bool
}

type contextChangingPolicy struct {
	calls map[datavisibility.ResourceKind]int
}

type contextOverviewBatchPolicy struct {
	batchCalls        int
	maxBatchRefs      int
	maxBatchResources int
	aggregateBatched  bool
}

type contextRecentBudgetPolicy struct {
	contextTestPolicy
}

func (p *contextChangingPolicy) Evaluate(_ context.Context, req datavisibility.AdapterRequest) (datavisibility.AdapterDecision, error) {
	kind := req.Resources[0].Kind
	p.calls[kind]++
	revision := string(kind) + "-1"
	if kind == datavisibility.ResourceFact && p.calls[kind] > 1 {
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

func (p *contextOverviewBatchPolicy) Evaluate(_ context.Context, req datavisibility.AdapterRequest) (datavisibility.AdapterDecision, error) {
	out := datavisibility.AdapterDecision{Coverage: datavisibility.CoverageAll, PolicyRevision: "context-policy-1"}
	refs := map[datavisibility.AccessRef]bool{}
	isOverviewBatch := false
	isAggregateProbe := false
	for _, resource := range req.Resources {
		isOverviewBatch = isOverviewBatch || strings.HasPrefix(resource.Key, "context-overview-group:")
		isAggregateProbe = isAggregateProbe || strings.HasPrefix(resource.Key, "context-overview-aggregate:")
		for _, ref := range resource.AccessRefs {
			refs[ref] = true
		}
		out.Resources = append(out.Resources, datavisibility.AdapterResourceDecision{
			Key: resource.Key, AllowedFieldPaths: append([]string(nil), resource.FieldPaths...), ExistenceDisclosure: datavisibility.ExistenceVisible,
		})
	}
	if isOverviewBatch {
		p.batchCalls++
		p.aggregateBatched = p.aggregateBatched || isAggregateProbe
		if len(req.Resources) > p.maxBatchResources {
			p.maxBatchResources = len(req.Resources)
		}
		if len(refs) > p.maxBatchRefs {
			p.maxBatchRefs = len(refs)
		}
	}
	return out, nil
}

func (p contextRecentBudgetPolicy) Evaluate(ctx context.Context, req datavisibility.AdapterRequest) (datavisibility.AdapterDecision, error) {
	for _, resource := range req.Resources {
		if strings.HasPrefix(resource.Key, "object:") {
			return datavisibility.AdapterDecision{}, ontologycontract.ErrQueryBudgetExceeded
		}
	}
	return p.contextTestPolicy.Evaluate(ctx, req)
}

func (a contextTestPolicy) Evaluate(_ context.Context, req datavisibility.AdapterRequest) (datavisibility.AdapterDecision, error) {
	coverage := datavisibility.CoverageAll
	if a.filter {
		coverage |= datavisibility.CoverageFilter
	}
	out := datavisibility.AdapterDecision{Coverage: coverage, PolicyRevision: "context-policy-1"}
	for _, resource := range req.Resources {
		hidden := a.hidden[resource.Key]
		for _, ref := range resource.AccessRefs {
			hidden = hidden || ref.ID == "denied"
		}
		masked := append([]string(nil), a.masked[resource.Key]...)
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
		out.Resources = append(out.Resources, datavisibility.AdapterResourceDecision{Key: resource.Key, Hidden: hidden, AllowedFieldPaths: allowed, MaskedFieldPaths: masked, ExistenceDisclosure: disclosure})
	}
	return out, nil
}

func TestServiceListsOnlyAuthorizedObjectsAndBindsCursor(t *testing.T) {
	graph := contextGraphFixture()
	service := newContextServiceForTest(t, graph, contextTestAuthorizer{}, contextTestPolicy{hidden: map[string]bool{"object:policy-hidden": true, "object:hidden-node": true}, masked: map[string][]string{}})
	first, err := service.ListObjects(context.Background(), contextPrincipal("user-1"), ObjectListQuery{TenantID: "tenant-1", Limit: 1})
	if err != nil || len(first.Items) != 1 || first.Items[0].ID != "alpha" || !first.Page.HasMore {
		t.Fatalf("first objects page = %+v, err=%v", first, err)
	}
	second, err := service.ListObjects(context.Background(), contextPrincipal("user-1"), ObjectListQuery{TenantID: "tenant-1", Limit: 1, Cursor: first.Page.NextCursor})
	if err != nil || len(second.Items) != 1 || second.Items[0].ID != "zeta" || second.Page.HasMore {
		t.Fatalf("second objects page = %+v, err=%v", second, err)
	}
	if _, err := service.ListObjects(context.Background(), contextPrincipal("user-2"), ObjectListQuery{TenantID: "tenant-1", Limit: 1, Cursor: first.Page.NextCursor}); !errors.Is(err, ontologycontract.ErrCursorStale) {
		t.Fatalf("cross-principal cursor error = %v", err)
	}
	if !graph.lastVisibility.IsCandidateScan() {
		t.Fatalf("SQL visibility constraint = %+v", graph.lastVisibility)
	}
	if !strings.HasPrefix(first.Page.NextCursor, "ocs1.") {
		t.Fatalf("candidate continuation is not stateful: %q", first.Page.NextCursor)
	}
}

func TestServiceFiltersObjectsOnlyAfterFieldPolicy(t *testing.T) {
	graph := contextGraphFixture()
	policy := contextTestPolicy{hidden: map[string]bool{}, masked: map[string][]string{
		"object:alpha": {"canonical_key", "label", "object_type", "source", "status"},
	}}
	service := newContextServiceForTest(t, graph, contextTestAuthorizer{}, policy)
	principal := contextPrincipal("user-1")
	queries := []ObjectListQuery{
		{TenantID: "tenant-1", Search: "Alpha Label"},
		{TenantID: "tenant-1", Search: "mission:alpha"},
		{TenantID: "tenant-1", ObjectType: "mission"},
		{TenantID: "tenant-1", Status: "active"},
		{TenantID: "tenant-1", Source: "mission"},
	}
	for _, query := range queries {
		page, err := service.ListObjects(context.Background(), principal, query)
		if err != nil {
			t.Fatalf("masked query %+v returned err=%v", query, err)
		}
		for _, item := range page.Items {
			if item.ID == "alpha" {
				t.Fatalf("masked query %+v exposed alpha in page=%+v", query, page)
			}
		}
	}
	page, err := service.ListObjects(context.Background(), principal, ObjectListQuery{TenantID: "tenant-1", Search: "alpha"})
	if err != nil || len(page.Items) != 1 || page.Items[0].ID != "alpha" {
		t.Fatalf("public id search page=%+v err=%v", page, err)
	}
	if query := graph.lastObjectQuery; query.ObjectType != "" || query.Status != "" || query.SourceKind != "" || query.Search != "" {
		t.Fatalf("sensitive object filters reached candidate store: %+v", query)
	}
}

func TestServiceCandidateScanFailsClosedAtScanBoundary(t *testing.T) {
	allowed := contextgraph.AccessRef{Kind: "mission", ID: "allowed", TenantID: "auth"}
	graph := &contextTestGraph{refs: []contextgraph.AccessRef{allowed}}
	hidden := make(map[string]bool, contextCandidateScanBatches*contextScanBatch)
	for index := 0; index < contextCandidateScanBatches*contextScanBatch; index++ {
		id := fmt.Sprintf("hidden-%04d", index)
		graph.objects = append(graph.objects, contextgraph.Object{
			ID: id, TenantID: "tenant-1", ObjectType: "mission", Label: id,
			OwnerAccessRef: allowed, UpdatedAt: time.Unix(int64(10_000-index), 0).UTC(),
		})
		hidden["object:"+id] = true
	}
	graph.objects = append(graph.objects, contextgraph.Object{
		ID: "visible-after-boundary", TenantID: "tenant-1", ObjectType: "mission", Label: "Visible",
		OwnerAccessRef: allowed, UpdatedAt: time.Unix(1, 0).UTC(),
	})
	service := newContextServiceForTest(t, graph, contextTestAuthorizer{}, contextTestPolicy{hidden: hidden, masked: map[string][]string{}})

	page, err := service.ListObjects(context.Background(), contextPrincipal("user-1"), ObjectListQuery{TenantID: "tenant-1", Limit: 1})
	if !errors.Is(err, ontologycontract.ErrQueryBudgetExceeded) || len(page.Items) != 0 || page.Page.HasMore || page.Page.NextCursor != "" {
		t.Fatalf("boundary page = %+v err=%v", page, err)
	}
}

func TestServiceSummarizesVisibleDataAndMasksObjectDetail(t *testing.T) {
	graph := contextGraphFixture()
	policy := contextTestPolicy{hidden: map[string]bool{}, masked: map[string][]string{"object:alpha": {"canonical_key", "properties.secret"}}}
	service := newContextServiceForTest(t, graph, contextTestAuthorizer{}, policy)
	summary, err := service.Summary(context.Background(), contextPrincipal("user-1"), "tenant-1")
	if err != nil || summary.Data.Objects.Value == nil || *summary.Data.Objects.Value != 2 || summary.Meta.DataRevision == "" || graph.countCalls != 1 {
		t.Fatalf("Summary = %+v, err=%v", summary, err)
	}
	if summary.Data.Evidence.Status != ontologycontract.MetricUnavailable || summary.Data.Evidence.Value != nil {
		t.Fatalf("evidence summary must remain unavailable without a visibility-safe reference aggregate: %+v", summary.Data.Evidence)
	}
	detail, err := service.GetObject(context.Background(), contextPrincipal("user-1"), "tenant-1", "alpha")
	if err != nil {
		t.Fatalf("GetObject: %v", err)
	}
	if detail.CanonicalKey != "" || detail.Properties["secret"] != nil || detail.Properties["public"] != "ok" {
		t.Fatalf("masked object detail = %+v", detail)
	}
	if _, err := service.GetObject(context.Background(), contextPrincipal("user-1"), "tenant-1", "denied-object"); !errors.Is(err, ontologycontract.ErrUnavailable) {
		t.Fatalf("denied exact object error = %v", err)
	}
}

func TestServiceLoadsPolicyFilteredChildrenAndNeighborhood(t *testing.T) {
	graph := contextGraphFixture()
	policy := contextTestPolicy{
		hidden: map[string]bool{"object:hidden-node": true},
		masked: map[string][]string{"fact:fact-1": {"value"}, "evidence:evidence-1": {"summary"}},
	}
	service := newContextServiceForTest(t, graph, contextTestAuthorizer{}, policy)
	principal := contextPrincipal("user-1")
	facts, err := service.ListFacts(context.Background(), principal, ChildQuery{TenantID: "tenant-1", ObjectID: "alpha"})
	if err != nil || len(facts.Items) != 1 || facts.Items[0].Value != nil {
		t.Fatalf("facts = %+v, err=%v", facts, err)
	}
	relations, err := service.ListRelations(context.Background(), principal, RelationListQuery{TenantID: "tenant-1", ObjectID: "alpha", Direction: "both"})
	if err != nil || len(relations.Items) != 1 || relations.Items[0].OtherObject.ID != "zeta" || relations.Items[0].LinkTypeLabel != "使命包含任务" {
		t.Fatalf("relations = %+v, err=%v", relations, err)
	}
	evidence, err := service.ListEvidence(context.Background(), principal, ChildQuery{TenantID: "tenant-1", ObjectID: "alpha"})
	if err != nil || len(evidence.Items) != 2 || evidence.Items[0].Summary != "" || evidence.Items[1].ID != "event-2" {
		t.Fatalf("evidence = %+v, err=%v", evidence, err)
	}
	neighborhood, err := service.Neighborhood(context.Background(), principal, NeighborhoodQuery{TenantID: "tenant-1", RootID: "alpha", LinkType: "has_task", Depth: 1, MaxNodes: 2, MaxEdges: 2})
	if err != nil || neighborhood.VisibleNodes != 2 || neighborhood.VisibleEdges != 1 || neighborhood.Nodes[0].ID != "alpha" || neighborhood.Edges[0].LinkTypeLabel != "使命包含任务" || neighborhood.Truncated {
		t.Fatalf("neighborhood = %+v, err=%v", neighborhood, err)
	}
	if graph.lastNeighborhood.LinkType != "" || graph.lastNeighborhood.MaxNodes != contextgraph.MaxNeighborhoodCandidateNodes || graph.lastNeighborhood.MaxEdges != contextgraph.MaxNeighborhoodCandidateEdges {
		t.Fatalf("neighborhood candidate limits = %+v", graph.lastNeighborhood)
	}
}

func TestServiceFiltersLinkTypeOnlyAfterFieldPolicy(t *testing.T) {
	graph := contextGraphFixture()
	policy := contextTestPolicy{
		hidden: map[string]bool{"object:hidden-node": true},
		masked: map[string][]string{"link:link-visible": {"link_type"}},
	}
	service := newContextServiceForTest(t, graph, contextTestAuthorizer{}, policy)
	principal := contextPrincipal("user-1")
	relations, err := service.ListRelations(context.Background(), principal, RelationListQuery{TenantID: "tenant-1", ObjectID: "alpha", LinkType: "has_task"})
	if err != nil || len(relations.Items) != 0 || graph.lastRelationQuery.LinkType != "" {
		t.Fatalf("masked relations=%+v err=%v storeQuery=%+v", relations, err, graph.lastRelationQuery)
	}
	neighborhood, err := service.Neighborhood(context.Background(), principal, NeighborhoodQuery{TenantID: "tenant-1", RootID: "alpha", LinkType: "has_task", Depth: 1})
	if err != nil || neighborhood.VisibleNodes != 1 || neighborhood.VisibleEdges != 0 || graph.lastNeighborhood.LinkType != "" {
		t.Fatalf("masked neighborhood=%+v err=%v storeQuery=%+v", neighborhood, err, graph.lastNeighborhood)
	}
}

func TestServicePushesOnlyPolicyAuthorizedContextFilters(t *testing.T) {
	graph := contextGraphFixture()
	service := newContextServiceForTest(t, graph, contextTestAuthorizer{}, contextTestPolicy{
		hidden: map[string]bool{"object:hidden-node": true}, masked: map[string][]string{}, filter: true,
	})
	principal := contextPrincipal("user-1")
	page, err := service.ListObjects(context.Background(), principal, ObjectListQuery{TenantID: "tenant-1", ObjectType: "mission"})
	if err != nil || len(page.Items) != 1 || page.Items[0].ID != "alpha" || graph.lastObjectQuery.ObjectType != "mission" {
		t.Fatalf("object page=%+v err=%v storeQuery=%+v", page, err, graph.lastObjectQuery)
	}

	neighborhood, err := service.Neighborhood(context.Background(), principal, NeighborhoodQuery{
		TenantID: "tenant-1", RootID: "alpha", LinkTypes: []string{"has_task"}, Depth: 1, MaxNodes: 2, MaxEdges: 2,
	})
	if err != nil || neighborhood.VisibleEdges != 1 || len(graph.lastNeighborhood.LinkTypes) != 1 || graph.lastNeighborhood.LinkTypes[0] != "has_task" {
		t.Fatalf("neighborhood=%+v err=%v storeQuery=%+v", neighborhood, err, graph.lastNeighborhood)
	}
}

func TestServiceNeighborhoodFailsClosedWhenCandidateGraphIsIncomplete(t *testing.T) {
	graph := contextGraphFixture()
	graph.neighborhood.Truncated = true
	service := newContextServiceForTest(t, graph, contextTestAuthorizer{}, contextTestPolicy{hidden: map[string]bool{}, masked: map[string][]string{}})
	result, err := service.Neighborhood(context.Background(), contextPrincipal("user-1"), NeighborhoodQuery{TenantID: "tenant-1", RootID: "alpha", Depth: 1})
	if !errors.Is(err, ontologycontract.ErrQueryBudgetExceeded) || len(result.Nodes) != 0 || result.Truncated {
		t.Fatalf("incomplete candidate graph result=%+v err=%v", result, err)
	}
}

func TestServiceReturnsBoundedVisibleNeighborhoodWhenCandidateGraphIsIncomplete(t *testing.T) {
	graph := contextGraphFixture()
	graph.neighborhood.Truncated = true
	service := newContextServiceForTest(t, graph, contextTestAuthorizer{}, contextTestPolicy{hidden: map[string]bool{}, masked: map[string][]string{}})

	result, err := service.Neighborhood(context.Background(), contextPrincipal("user-1"), NeighborhoodQuery{
		TenantID: "tenant-1", RootID: "alpha", Depth: 1, MaxNodes: 2, MaxEdges: 2,
	})
	if err != nil || !result.Truncated || result.VisibleNodes != 2 || result.VisibleEdges != 1 {
		t.Fatalf("bounded incomplete candidate graph result=%+v err=%v", result, err)
	}
}

func TestServiceFailsClosedWhenFactPolicyRevisionChangesDuringQuery(t *testing.T) {
	policy := &contextChangingPolicy{calls: map[datavisibility.ResourceKind]int{}}
	service := newContextServiceForTest(t, contextGraphFixture(), contextTestAuthorizer{}, policy)

	_, err := service.ListFacts(context.Background(), contextPrincipal("user-1"), ChildQuery{TenantID: "tenant-1", ObjectID: "alpha"})
	if !errors.Is(err, ontologyscope.ErrDependencyUnavailable) || policy.calls[datavisibility.ResourceFact] != 2 {
		t.Fatalf("fact revision change err=%v calls=%+v", err, policy.calls)
	}
}

func TestLimitVisibleNeighborhoodReportsOnlyVisibleTruncation(t *testing.T) {
	nodes := []ObjectItem{{ID: "root"}, {ID: "a"}, {ID: "b"}}
	edges := []NeighborhoodEdge{
		{ID: "root-a", SourceID: "root", TargetID: "a"},
		{ID: "root-b", SourceID: "root", TargetID: "b"},
	}
	limitedNodes, limitedEdges, truncated := limitVisibleNeighborhood("root", 1, nodes, edges, 2, 2)
	if !truncated || len(limitedNodes) != 2 || len(limitedEdges) != 1 || limitedNodes[0].ID != "root" {
		t.Fatalf("limited nodes=%+v edges=%+v truncated=%t", limitedNodes, limitedEdges, truncated)
	}
	_, limitedEdges, truncated = limitVisibleNeighborhood("root", 1, nodes[:2], append(edges[:1], NeighborhoodEdge{ID: "a-root", SourceID: "a", TargetID: "root"}), 2, 1)
	if !truncated || len(limitedEdges) != 1 {
		t.Fatalf("edge-limited edges=%+v truncated=%t", limitedEdges, truncated)
	}
}

func TestServiceAuthorizesScopeBeforeGraphReadAndConcealsExactFailures(t *testing.T) {
	graph := contextGraphFixture()
	service := newContextServiceForTest(t, graph, contextTestAuthorizer{deny: true}, contextTestPolicy{hidden: map[string]bool{}, masked: map[string][]string{}})
	if _, err := service.ListObjects(context.Background(), contextPrincipal("user-1"), ObjectListQuery{TenantID: "tenant-1"}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("denied ListObjects error = %v", err)
	}
	if graph.readCalls != 0 {
		t.Fatalf("graph was read before scope authorization: %d", graph.readCalls)
	}
	service = newContextServiceForTest(t, graph, contextTestAuthorizer{}, contextTestPolicy{hidden: map[string]bool{}, masked: map[string][]string{}})
	if _, err := service.GetObject(context.Background(), contextPrincipal("user-1"), "tenant-1", "missing"); !errors.Is(err, ontologycontract.ErrUnavailable) {
		t.Fatalf("missing exact object error = %v", err)
	}
	graph.err = errors.New("database unavailable")
	if _, err := service.GetObject(context.Background(), contextPrincipal("user-1"), "tenant-1", "alpha"); !errors.Is(err, ontologyscope.ErrDependencyUnavailable) {
		t.Fatalf("dependency exact object error = %v", err)
	}
}

func TestServiceMarksSummaryUnavailableWhenVisibilityCannotBeProvenWithinBudget(t *testing.T) {
	graph := contextGraphFixture()
	graph.refs = make([]contextgraph.AccessRef, contextgraph.MaxVisibilityAccessRefs+1)
	for index := range graph.refs {
		graph.refs[index] = contextgraph.AccessRef{Kind: "mission", ID: string(rune('a' + index)), TenantID: "auth"}
	}
	service := newContextServiceForTest(t, graph, contextTestAuthorizer{}, contextTestPolicy{hidden: map[string]bool{}, masked: map[string][]string{}})
	summary, err := service.Summary(context.Background(), contextPrincipal("user-1"), "tenant-1")
	if err != nil || summary.Data.Objects.Status != ontologycontract.MetricUnavailable || graph.countCalls != 0 {
		t.Fatalf("budget-limited summary = %+v, count calls=%d, err=%v", summary, graph.countCalls, err)
	}
	if page, err := service.ListObjects(context.Background(), contextPrincipal("user-1"), ObjectListQuery{TenantID: "tenant-1"}); err != nil || len(page.Items) != 4 {
		t.Fatalf("large-cardinality candidate page = %+v, err=%v", page, err)
	}
}

func TestServiceMarksSummaryUnavailableWhenAggregatePolicyHidesRows(t *testing.T) {
	graph := contextGraphFixture()
	service := newContextServiceForTest(t, graph, contextTestAuthorizer{}, contextTestPolicy{
		hidden: map[string]bool{"context-scope-probe": true}, masked: map[string][]string{},
	})
	summary, err := service.Summary(context.Background(), contextPrincipal("user-1"), "tenant-1")
	if err != nil || summary.Data.Objects.Status != ontologycontract.MetricUnavailable || graph.countCalls != 0 {
		t.Fatalf("policy-limited summary = %+v, count calls=%d, err=%v", summary, graph.countCalls, err)
	}
}

func TestServiceOverviewCountsVisibleCompanyDataAndRecentObjects(t *testing.T) {
	graph := contextOverviewGraphFixture()
	service := newContextServiceForTest(t, graph, contextTestAuthorizer{}, contextTestPolicy{hidden: map[string]bool{}, masked: map[string][]string{}})

	overview, err := service.Overview(context.Background(), contextPrincipal("user-1"), "tenant-1")
	if err != nil {
		t.Fatalf("Overview: %v", err)
	}
	assertContextCount(t, "objects", overview.Data.Objects, 5)
	assertContextCount(t, "facts", overview.Data.Facts, 1)
	assertContextCount(t, "relations", overview.Data.Relations, 2)
	assertContextCount(t, "evidence", overview.Data.Evidence, 2)
	assertContextCount(t, "domains", overview.Data.DomainCount, 2)
	assertOverviewDomain(t, overview.Data.Domains, "execution", 4)
	assertOverviewDomain(t, overview.Data.Domains, "other", 1)
	if len(overview.Data.RecentObjects) != 5 {
		t.Fatalf("recent objects = %+v", overview.Data.RecentObjects)
	}
	wantRecent := []string{"alpha", "policy-hidden", "zeta", "hidden-node", "other-object"}
	for index, item := range overview.Data.RecentObjects {
		if item.ID != wantRecent[index] || item.TenantID != "tenant-1" {
			t.Fatalf("recent object %d = %+v", index, item)
		}
	}
	if overview.Data.NewestUpdatedAt == nil || !overview.Data.NewestUpdatedAt.Equal(time.Unix(10, 0).UTC()) {
		t.Fatalf("newest update = %v", overview.Data.NewestUpdatedAt)
	}
}

func TestServiceOverviewUsesCompanyOverviewQueryBudget(t *testing.T) {
	graph := contextOverviewGraphFixture()
	graph.overviewDelay = 30 * time.Millisecond
	service := newContextServiceForTest(t, graph, contextTestAuthorizer{}, contextTestPolicy{hidden: map[string]bool{}, masked: map[string][]string{}})
	budgets, err := ontologycontract.NewBudgetPolicy(map[ontologycontract.QueryClass]ontologycontract.QueryBudget{
		ontologycontract.QueryStandard:        {Deadline: 5 * time.Millisecond},
		ontologycontract.QueryCompanyOverview: {Deadline: 250 * time.Millisecond},
	})
	if err != nil {
		t.Fatalf("NewBudgetPolicy: %v", err)
	}
	service.budgets = budgets

	if _, err := service.Overview(context.Background(), contextPrincipal("user-1"), "tenant-1"); err != nil {
		t.Fatalf("Overview: %v", err)
	}
}

func TestServiceOverviewReturnsPartialRecentObjectsWhenScanBudgetIsExhausted(t *testing.T) {
	graph := &contextTestGraph{}
	accessRef := contextgraph.AccessRef{Kind: "mission", ID: "allowed", TenantID: "auth"}
	graph.overviewGroups = []contextgraph.ContextOverviewGroup{{
		Kind: contextgraph.ContextOverviewObject, ObjectType: "task",
		Count: int64(contextCandidateScanBatches * contextScanBatch), AccessRefs: []contextgraph.AccessRef{accessRef},
	}}
	hidden := make(map[string]bool, contextCandidateScanBatches*contextScanBatch)
	for index := 0; index < contextCandidateScanBatches*contextScanBatch; index++ {
		id := fmt.Sprintf("hidden-%04d", index)
		graph.objects = append(graph.objects, contextgraph.Object{
			ID: id, TenantID: "tenant-1", ObjectType: "task", Label: id,
			OwnerAccessRef: accessRef,
			UpdatedAt:      time.Unix(int64(index+1), 0).UTC(),
		})
		hidden["object:"+id] = true
	}
	service := newContextServiceForTest(t, graph, contextTestAuthorizer{}, contextTestPolicy{hidden: hidden, masked: map[string][]string{}})

	overview, err := service.Overview(context.Background(), contextPrincipal("user-1"), "tenant-1")
	if err != nil {
		t.Fatalf("Overview: %v", err)
	}
	if len(overview.Data.RecentObjects) != 0 {
		t.Fatalf("recent objects = %+v, want empty partial result", overview.Data.RecentObjects)
	}
	assertContextCount(t, "objects", overview.Data.Objects, int64(contextCandidateScanBatches*contextScanBatch))
}

func TestServiceOverviewReturnsCoreMetricsWhenRecentVisibilityBudgetIsExhausted(t *testing.T) {
	graph := contextOverviewGraphFixture()
	service := newContextServiceForTest(t, graph, contextTestAuthorizer{}, contextRecentBudgetPolicy{contextTestPolicy: contextTestPolicy{
		hidden: map[string]bool{}, masked: map[string][]string{},
	}})

	overview, err := service.Overview(context.Background(), contextPrincipal("user-1"), "tenant-1")
	if err != nil {
		t.Fatalf("Overview: %v", err)
	}
	if len(overview.Data.RecentObjects) != 0 {
		t.Fatalf("recent objects = %+v, want empty partial result", overview.Data.RecentObjects)
	}
	assertContextCount(t, "objects", overview.Data.Objects, 5)
}

func TestServiceOverviewBatchesByVisibilityResourceLimit(t *testing.T) {
	graph := &contextTestGraph{}
	for index := 0; index < datavisibility.MaxEvaluationResources; index++ {
		ref := contextgraph.AccessRef{Kind: "mission", ID: fmt.Sprintf("ref-%03d", index), TenantID: "auth"}
		graph.overviewGroups = append(graph.overviewGroups, contextgraph.ContextOverviewGroup{
			Kind: contextgraph.ContextOverviewObject, ObjectType: "mission", Count: 1, AccessRefs: []contextgraph.AccessRef{ref},
		})
	}
	policy := &contextOverviewBatchPolicy{}
	service := newContextServiceForTest(t, graph, contextTestAuthorizer{}, policy)

	overview, err := service.Overview(context.Background(), contextPrincipal("user-1"), "tenant-1")
	if err != nil {
		t.Fatalf("Overview: %v", err)
	}
	assertContextCount(t, "objects", overview.Data.Objects, int64(datavisibility.MaxEvaluationResources))
	expectedBatches := (datavisibility.MaxEvaluationResources + 1 + contextgraph.MaxVisibilityAccessRefs - 1) / contextgraph.MaxVisibilityAccessRefs
	if policy.batchCalls != expectedBatches || policy.maxBatchResources > datavisibility.MaxEvaluationResources || policy.maxBatchRefs > contextgraph.MaxVisibilityAccessRefs || !policy.aggregateBatched {
		t.Fatalf("overview batches=%d max_resources=%d max_refs=%d aggregate_batched=%t", policy.batchCalls, policy.maxBatchResources, policy.maxBatchRefs, policy.aggregateBatched)
	}
}

func TestServiceOverviewSupportsCurrentEnterpriseAccessRefCardinality(t *testing.T) {
	const accessRefCount = 1030
	graph := &contextTestGraph{}
	for index := 0; index < accessRefCount; index++ {
		ref := contextgraph.AccessRef{Kind: "mission", ID: fmt.Sprintf("ref-%04d", index), TenantID: "auth"}
		graph.overviewGroups = append(graph.overviewGroups, contextgraph.ContextOverviewGroup{
			Kind: contextgraph.ContextOverviewObject, ObjectType: "mission", Count: 1, AccessRefs: []contextgraph.AccessRef{ref},
		})
	}
	policy := &contextOverviewBatchPolicy{}
	service := newContextServiceForTest(t, graph, contextTestAuthorizer{}, policy)

	overview, err := service.Overview(context.Background(), contextPrincipal("user-1"), "tenant-1")
	if err != nil {
		t.Fatalf("Overview: %v", err)
	}
	assertContextCount(t, "objects", overview.Data.Objects, accessRefCount)
	expectedBatches := (accessRefCount + 1 + contextgraph.MaxVisibilityAccessRefs - 1) / contextgraph.MaxVisibilityAccessRefs
	if policy.batchCalls != expectedBatches || policy.maxBatchResources > datavisibility.MaxEvaluationResources || policy.maxBatchRefs > contextgraph.MaxVisibilityAccessRefs {
		t.Fatalf("overview batches=%d max_resources=%d max_refs=%d", policy.batchCalls, policy.maxBatchResources, policy.maxBatchRefs)
	}
}

func TestServiceOverviewBatchesMoreThanVisibilityResourceLimit(t *testing.T) {
	graph := &contextTestGraph{}
	ref := contextgraph.AccessRef{Kind: "mission", ID: "allowed", TenantID: "auth"}
	for index := 0; index <= datavisibility.MaxEvaluationResources; index++ {
		graph.overviewGroups = append(graph.overviewGroups, contextgraph.ContextOverviewGroup{
			Kind: contextgraph.ContextOverviewObject, ObjectType: fmt.Sprintf("type-%03d", index), Count: 1,
			AccessRefs: []contextgraph.AccessRef{ref},
		})
	}
	policy := &contextOverviewBatchPolicy{}
	service := newContextServiceForTest(t, graph, contextTestAuthorizer{}, policy)

	overview, err := service.Overview(context.Background(), contextPrincipal("user-1"), "tenant-1")
	if err != nil {
		t.Fatalf("Overview: %v", err)
	}
	assertContextCount(t, "objects", overview.Data.Objects, datavisibility.MaxEvaluationResources+1)
	if policy.batchCalls < 2 || policy.maxBatchResources > datavisibility.MaxEvaluationResources {
		t.Fatalf("overview batches=%d max_resources=%d", policy.batchCalls, policy.maxBatchResources)
	}
}

func TestServiceOverviewKeepsUnavailableMetricsAndFailsClosedOnRevisionChange(t *testing.T) {
	graph := contextOverviewGraphFixture()
	service := newContextServiceForTest(t, graph, contextTestAuthorizer{}, contextTestPolicy{
		hidden: map[string]bool{"context-overview-aggregate:object": true}, masked: map[string][]string{},
	})
	overview, err := service.Overview(context.Background(), contextPrincipal("user-1"), "tenant-1")
	if err != nil || overview.Data.Objects.Status != ontologycontract.MetricUnavailable || overview.Data.DomainCount.Status != ontologycontract.MetricUnavailable {
		t.Fatalf("unavailable overview=%+v err=%v", overview.Data, err)
	}
	if overview.Data.Facts.Status != ontologycontract.MetricExact || len(overview.Data.RecentObjects) != 5 {
		t.Fatalf("partial overview=%+v", overview.Data)
	}

	changing := &contextChangingPolicy{calls: map[datavisibility.ResourceKind]int{}}
	service = newContextServiceForTest(t, contextOverviewGraphFixture(), contextTestAuthorizer{}, changing)
	if _, err := service.Overview(context.Background(), contextPrincipal("user-1"), "tenant-1"); !errors.Is(err, ontologyscope.ErrDependencyUnavailable) {
		t.Fatalf("revision change error = %v", err)
	}
}

func TestServiceOverviewFailsClosedWithoutOutageForOversizedHistoricalAccessRef(t *testing.T) {
	graph := contextOverviewGraphFixture()
	oversized := contextgraph.AccessRef{Kind: "memory_record", ID: strings.Repeat("x", datavisibility.MaxAccessRefIDBytes+1), TenantID: "tenant-1"}
	graph.overviewGroups = append(graph.overviewGroups, contextgraph.ContextOverviewGroup{
		Kind: contextgraph.ContextOverviewObject, ObjectType: "memory_record", Count: 1, AccessRefs: []contextgraph.AccessRef{oversized},
	})
	graph.objects = append(graph.objects, contextgraph.Object{
		ID: "malformed-memory", TenantID: "tenant-1", ObjectType: "memory_record", Label: "Malformed historical memory",
		OwnerAccessRef: oversized, UpdatedAt: time.Unix(1000, 0).UTC(),
	})
	service := newContextServiceForTest(t, graph, contextTestAuthorizer{}, contextTestPolicy{hidden: map[string]bool{}, masked: map[string][]string{}})

	overview, err := service.Overview(context.Background(), contextPrincipal("user-1"), "tenant-1")
	if err != nil {
		t.Fatalf("Overview: %v", err)
	}
	if overview.Data.Objects.Status != ontologycontract.MetricUnavailable || overview.Data.DomainCount.Status != ontologycontract.MetricUnavailable {
		t.Fatalf("malformed object metrics = %+v", overview.Data)
	}
	assertContextCount(t, "facts", overview.Data.Facts, 1)
	for _, object := range overview.Data.RecentObjects {
		if object.ID == "malformed-memory" {
			t.Fatalf("malformed object was disclosed: %+v", object)
		}
	}
}

func TestServiceOverviewAuthorizesScopeBeforeGraphRead(t *testing.T) {
	graph := contextOverviewGraphFixture()
	service := newContextServiceForTest(t, graph, contextTestAuthorizer{deny: true}, contextTestPolicy{hidden: map[string]bool{}, masked: map[string][]string{}})
	if _, err := service.Overview(context.Background(), contextPrincipal("user-1"), "tenant-1"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("denied overview error = %v", err)
	}
	if graph.readCalls != 0 {
		t.Fatalf("graph was read before overview authorization: %d", graph.readCalls)
	}
}

func assertContextCount(t *testing.T, label string, metric ontologycontract.CountMetric, want int64) {
	t.Helper()
	if metric.Status != ontologycontract.MetricExact || metric.Value == nil || *metric.Value != want {
		t.Fatalf("%s metric = %+v, want=%d", label, metric, want)
	}
}

func assertOverviewDomain(t *testing.T, domains []OverviewDomain, id string, want int64) {
	t.Helper()
	for _, domain := range domains {
		if domain.ID == id {
			assertContextCount(t, "domain "+id, domain.Objects, want)
			return
		}
	}
	t.Fatalf("missing domain %q in %+v", id, domains)
}

func contextOverviewGraphFixture() *contextTestGraph {
	graph := contextGraphFixture()
	allowed := graph.refs[0]
	denied := graph.refs[1]
	graph.objects = append(graph.objects, contextgraph.Object{
		ID: "other-object", TenantID: "tenant-1", ObjectType: "custom_record", Label: "Other object",
		OwnerAccessRef: allowed, UpdatedAt: time.Unix(6, 0).UTC(),
	})
	graph.overviewGroups = []contextgraph.ContextOverviewGroup{
		{Kind: contextgraph.ContextOverviewObject, ObjectType: "mission", Count: 1, NewestAt: timePointer(time.Unix(10, 0).UTC()), AccessRefs: []contextgraph.AccessRef{allowed}},
		{Kind: contextgraph.ContextOverviewObject, ObjectType: "task", Count: 3, NewestAt: timePointer(time.Unix(9, 0).UTC()), AccessRefs: []contextgraph.AccessRef{allowed}},
		{Kind: contextgraph.ContextOverviewObject, ObjectType: "custom_record", Count: 1, NewestAt: timePointer(time.Unix(6, 0).UTC()), AccessRefs: []contextgraph.AccessRef{allowed}},
		{Kind: contextgraph.ContextOverviewObject, ObjectType: "task", Count: 1, NewestAt: timePointer(time.Unix(11, 0).UTC()), AccessRefs: []contextgraph.AccessRef{denied}},
		{Kind: contextgraph.ContextOverviewFact, Count: 1, NewestAt: timePointer(time.Unix(2, 0).UTC()), AccessRefs: []contextgraph.AccessRef{allowed}},
		{Kind: contextgraph.ContextOverviewFact, Count: 2, NewestAt: timePointer(time.Unix(4, 0).UTC()), AccessRefs: []contextgraph.AccessRef{allowed, denied}},
		{Kind: contextgraph.ContextOverviewLink, Count: 2, NewestAt: timePointer(time.Unix(6, 0).UTC()), AccessRefs: []contextgraph.AccessRef{allowed}},
		{Kind: contextgraph.ContextOverviewLink, Count: 2, NewestAt: timePointer(time.Unix(4, 0).UTC()), AccessRefs: []contextgraph.AccessRef{allowed, denied}},
		{Kind: contextgraph.ContextOverviewEvidence, Count: 1, NewestAt: timePointer(time.Unix(1, 0).UTC()), AccessRefs: []contextgraph.AccessRef{allowed}},
		{Kind: contextgraph.ContextOverviewEvidence, Count: 1, NewestAt: timePointer(time.Unix(2, 0).UTC()), AccessRefs: []contextgraph.AccessRef{denied}},
		{Kind: contextgraph.ContextOverviewEvidence, Count: 1, NewestAt: timePointer(time.Unix(0, 0).UTC()), AccessRefs: []contextgraph.AccessRef{{Kind: "mission", ID: "evidence-only", TenantID: "auth"}}},
	}
	return graph
}

func timePointer(value time.Time) *time.Time { return &value }

func newContextServiceForTest(t *testing.T, graph *contextTestGraph, authorizer contextTestAuthorizer, adapter datavisibility.Adapter) *Service {
	t.Helper()
	binding := ontologyscope.ScopeBinding{
		TenantID: "tenant-1", Kind: ontologyscope.KindCustom, SourceObject: identity.ObjectRef{Type: "workspace", ID: "tenant-1"},
		AuthorizationTenantID: "tenant-1", ReadAction: "workspace.view", ManageAction: "workspace.manage", BindingVersion: 1, State: ontologyscope.BindingStateActive,
	}
	gate := ontologyscope.NewScopeAccessGate(contextTestResolver{binding: binding}, authorizer, contextTestSnapshots{})
	codec, err := ontologycontract.NewCursorCodec(ontologycontract.CursorCodecOptions{PrimaryID: "test", Keys: []ontologycontract.CursorKey{{ID: "test", Key: bytes.Repeat([]byte{7}, 32)}}})
	if err != nil {
		t.Fatalf("NewCursorCodec: %v", err)
	}
	stateful, err := ontologycontract.NewStateCursorStore(ontologycontract.StateCursorOptions{MaxEntries: 100})
	if err != nil {
		t.Fatalf("NewStateCursorStore: %v", err)
	}
	visibility := datavisibility.NewService(
		datavisibility.Register(datavisibility.ResourceObject, adapter),
		datavisibility.Register(datavisibility.ResourceLink, adapter),
		datavisibility.Register(datavisibility.ResourceFact, adapter),
		datavisibility.Register(datavisibility.ResourceEvidence, adapter),
	)
	service, err := NewService(gate, graph, contextTestDefinitions{}, visibility, CursorSet{Encrypted: codec, Stateful: stateful}, nil, nil)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	service.now = func() time.Time { return time.Unix(100, 0).UTC() }
	return service
}

func contextPrincipal(id string) identity.Principal {
	return identity.NormalizePrincipal(identity.Principal{SubjectProvider: "oidc", Subject: id, UserID: id, ViewerKind: identity.ViewerKindUser, CoarseScopes: []string{"operations.read"}})
}

func contextGraphFixture() *contextTestGraph {
	allowed := contextgraph.AccessRef{Kind: "mission", ID: "allowed", TenantID: "auth"}
	denied := contextgraph.AccessRef{Kind: "mission", ID: "denied", TenantID: "auth"}
	alpha := contextgraph.Object{ID: "alpha", TenantID: "tenant-1", ObjectType: "mission", CanonicalKey: "mission:alpha", Label: "Alpha Label", Status: "active", Properties: map[string]any{"public": "ok", "secret": "masked"}, Provenance: contextgraph.Ref{Kind: "event", ID: "event-alpha"}, EvidenceRefs: []contextgraph.Ref{{Kind: "document", ID: "doc-1"}, {Kind: "event", ID: "event-2"}}, OwnerAccessRef: allowed, UpdatedAt: time.Unix(10, 0).UTC()}
	zeta := contextgraph.Object{ID: "zeta", TenantID: "tenant-1", ObjectType: "task", CanonicalKey: "task:zeta", Label: "Zeta", OwnerAccessRef: allowed, UpdatedAt: time.Unix(8, 0).UTC()}
	hidden := contextgraph.Object{ID: "hidden-node", TenantID: "tenant-1", ObjectType: "task", CanonicalKey: "task:hidden", Label: "Hidden", OwnerAccessRef: allowed, UpdatedAt: time.Unix(7, 0).UTC()}
	policyHidden := contextgraph.Object{ID: "policy-hidden", TenantID: "tenant-1", ObjectType: "task", CanonicalKey: "task:policy-hidden", Label: "Policy hidden", OwnerAccessRef: allowed, UpdatedAt: time.Unix(9, 0).UTC()}
	deniedObject := contextgraph.Object{ID: "denied-object", TenantID: "tenant-1", ObjectType: "task", CanonicalKey: "task:denied", Label: "Denied", OwnerAccessRef: denied, UpdatedAt: time.Unix(11, 0).UTC()}
	visibleLink := contextgraph.Link{ID: "link-visible", TenantID: "tenant-1", LinkType: "has_task", FromObjectID: "alpha", ToObjectID: "zeta", SourceAccessRef: allowed, UpdatedAt: time.Unix(6, 0).UTC()}
	hiddenLink := contextgraph.Link{ID: "link-hidden", TenantID: "tenant-1", LinkType: "has_task", FromObjectID: "alpha", ToObjectID: "hidden-node", SourceAccessRef: allowed, UpdatedAt: time.Unix(5, 0).UTC()}
	oldest, newest := time.Unix(1, 0).UTC(), time.Unix(10, 0).UTC()
	return &contextTestGraph{
		refs: []contextgraph.AccessRef{allowed, denied}, objects: []contextgraph.Object{deniedObject, alpha, policyHidden, zeta, hidden},
		facts:     []contextgraph.Fact{{ID: "fact-1", TenantID: "tenant-1", Subject: contextgraph.Ref{Kind: "mission", ID: "alpha"}, Predicate: "status", Value: "active", Provenance: contextgraph.Ref{Kind: "event", ID: "event-fact-1"}, SourceAccessRef: allowed, UpdatedAt: time.Unix(4, 0).UTC()}},
		relations: []contextgraph.Relation{{Link: visibleLink, OtherObject: zeta, Direction: contextgraph.RelationOutgoing}},
		evidence: []contextgraph.Evidence{
			{ID: "evidence-1", TenantID: "tenant-1", SourceRef: contextgraph.Ref{Kind: "document", ID: "doc-1"}, SourceAccessRef: allowed, Summary: "secret summary", ObservedAt: oldest},
			{ID: "doc-1", TenantID: "tenant-1", SourceRef: contextgraph.Ref{Kind: "unrelated", ID: "other"}, SourceAccessRef: allowed, Summary: "id collision", ObservedAt: oldest.Add(500 * time.Millisecond)},
			{ID: "event-2", TenantID: "tenant-1", SourceRef: contextgraph.Ref{Kind: "mission.updated", ID: "event-2"}, SourceAccessRef: allowed, Summary: "projection evidence", ObservedAt: oldest.Add(time.Second)},
		},
		neighborhood: contextgraph.Neighborhood{RootID: "alpha", Objects: []contextgraph.Object{alpha, zeta, hidden}, Links: []contextgraph.Link{visibleLink, hiddenLink}},
		counts:       contextgraph.VisibleContextCounts{Objects: 2, Links: 1, Facts: 1, OldestAt: &oldest, NewestAt: &newest},
	}
}
