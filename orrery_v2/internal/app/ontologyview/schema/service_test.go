package schema

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sort"
	"testing"
	"time"

	"orrery_v2/internal/app/actioncontract"
	"orrery_v2/internal/app/authorization"
	"orrery_v2/internal/app/datavisibility"
	"orrery_v2/internal/app/identity"
	"orrery_v2/internal/app/ontology"
	"orrery_v2/internal/app/ontologyscope"
	ontologycontract "orrery_v2/internal/app/ontologyview/contract"
)

type schemaTestRegistry struct {
	definitions []ontology.RegistryDefinition
	listCalls   int
	listedKinds []ontology.RegistryDefinitionKind
	batchCalls  int
	listErr     error
	getErr      error
}

func (r *schemaTestRegistry) ListRegistryDefinitions(_ context.Context, query ontology.RegistryDefinitionQuery) (ontology.RegistryDefinitionPage, error) {
	r.listCalls++
	r.listedKinds = append(r.listedKinds, query.Kind)
	if r.listErr != nil {
		return ontology.RegistryDefinitionPage{}, r.listErr
	}
	items := make([]ontology.RegistryDefinition, 0)
	for _, item := range r.definitions {
		if item.Kind != query.Kind || item.ID <= query.AfterID {
			continue
		}
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	hasMore := len(items) > query.Limit
	if hasMore {
		items = items[:query.Limit]
	}
	return ontology.RegistryDefinitionPage{Items: items, HasMore: hasMore}, nil
}

func (r *schemaTestRegistry) GetRegistryDefinition(_ context.Context, kind ontology.RegistryDefinitionKind, id string) (ontology.RegistryDefinition, error) {
	if r.getErr != nil {
		return ontology.RegistryDefinition{}, r.getErr
	}
	for _, item := range r.definitions {
		if item.Kind == kind && item.ID == id {
			return item, nil
		}
	}
	return ontology.RegistryDefinition{}, ontology.ErrNotFound
}

func (r *schemaTestRegistry) GetRegistryDefinitions(_ context.Context, kind ontology.RegistryDefinitionKind, ids []string) ([]ontology.RegistryDefinition, error) {
	r.batchCalls++
	if r.getErr != nil {
		return nil, r.getErr
	}
	wanted := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		wanted[id] = struct{}{}
	}
	definitions := make([]ontology.RegistryDefinition, 0, len(ids))
	for _, item := range r.definitions {
		if item.Kind == kind {
			if _, ok := wanted[item.ID]; ok {
				definitions = append(definitions, item)
			}
		}
	}
	return definitions, nil
}

type schemaTestActions struct {
	items []actioncontract.ActionType
}

func (r schemaTestActions) ListActionTypePage(_ context.Context, _ string, after string, limit int) (actioncontract.ActionTypePage, error) {
	items := make([]actioncontract.ActionType, 0)
	for _, item := range r.items {
		if string(item.ID) > after {
			items = append(items, item)
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	hasMore := len(items) > limit
	if hasMore {
		items = items[:limit]
	}
	return actioncontract.ActionTypePage{Items: items, HasMore: hasMore}, nil
}

func (r schemaTestActions) GetActionType(_ context.Context, id actioncontract.ActionTypeID) (actioncontract.ActionType, error) {
	for _, item := range r.items {
		if item.ID == id {
			return item, nil
		}
	}
	return actioncontract.ActionType{}, actioncontract.ErrNotFound
}

type schemaTestResolver struct{ binding ontologyscope.ScopeBinding }

func (r schemaTestResolver) Resolve(context.Context, string) (ontologyscope.ScopeBinding, error) {
	return r.binding, nil
}

type schemaTestAuthorizer struct{ deny bool }

func (a schemaTestAuthorizer) AuthorizeFeature(context.Context, identity.Principal, string) error {
	if a.deny {
		return ontologyscope.ErrAccessDenied
	}
	return nil
}

func (a schemaTestAuthorizer) AuthorizeObject(context.Context, identity.Principal, string, identity.ObjectRef, string) error {
	if a.deny {
		return ontologyscope.ErrAccessDenied
	}
	return nil
}

type schemaTestSnapshots struct{}

func (schemaTestSnapshots) Capture(_ context.Context, principal identity.Principal) (authorization.AuthorizationSnapshot, error) {
	return authorization.AuthorizationSnapshot{PrincipalKey: authorization.PrincipalCacheKey(principal), AuthorizationRevision: "auth-1", PolicyRevision: "policy-1"}, nil
}

type schemaTestPolicyAdapter struct {
	hidden     map[string]bool
	masked     map[string][]string
	seenTenant map[string]string
}

type schemaChangingPolicyAdapter struct{ calls int }

func (a *schemaChangingPolicyAdapter) Evaluate(_ context.Context, req datavisibility.AdapterRequest) (datavisibility.AdapterDecision, error) {
	a.calls++
	revision := "schema-policy-1"
	if a.calls > 1 {
		revision = "schema-policy-2"
	}
	decision := datavisibility.AdapterDecision{Coverage: datavisibility.CoverageAll, PolicyRevision: revision}
	for _, resource := range req.Resources {
		decision.Resources = append(decision.Resources, datavisibility.AdapterResourceDecision{
			Key: resource.Key, AllowedFieldPaths: append([]string(nil), resource.FieldPaths...), ExistenceDisclosure: datavisibility.ExistenceVisible,
		})
	}
	return decision, nil
}

func (a schemaTestPolicyAdapter) Evaluate(_ context.Context, req datavisibility.AdapterRequest) (datavisibility.AdapterDecision, error) {
	decision := datavisibility.AdapterDecision{Coverage: datavisibility.CoverageAll, PolicyRevision: "schema-policy-1"}
	for _, resource := range req.Resources {
		if a.seenTenant != nil {
			a.seenTenant[resource.Key] = resource.TenantID
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
		if a.hidden[resource.Key] {
			disclosure = datavisibility.ExistenceConceal
			allowed = nil
			masked = nil
		}
		decision.Resources = append(decision.Resources, datavisibility.AdapterResourceDecision{
			Key: resource.Key, Hidden: a.hidden[resource.Key], AllowedFieldPaths: allowed, MaskedFieldPaths: masked, ExistenceDisclosure: disclosure,
		})
	}
	return decision, nil
}

func TestServiceListsOnlyVisibleDefinitionsAndBindsCursor(t *testing.T) {
	registry := &schemaTestRegistry{definitions: []ontology.RegistryDefinition{
		objectDefinition("alpha", "", "Alpha"),
		objectDefinition("hidden", "tenant-1", "Hidden"),
		objectDefinition("other", "tenant-2", "Other Tenant"),
		objectDefinition("zeta", "tenant-1", "Zeta"),
	}}
	service := newSchemaServiceForTest(t, registry, nil, schemaTestAuthorizer{}, schemaTestPolicyAdapter{hidden: map[string]bool{"object:hidden": true}, masked: map[string][]string{}})
	principal := schemaPrincipal("user-1")
	first, err := service.ListDefinitions(context.Background(), principal, DefinitionListQuery{TenantID: "tenant-1", Kind: KindObject, Limit: 1})
	if err != nil {
		t.Fatalf("ListDefinitions first: %v", err)
	}
	if len(first.Items) != 1 || first.Items[0].ID != "alpha" || !first.Page.HasMore {
		t.Fatalf("first page = %+v", first)
	}
	second, err := service.ListDefinitions(context.Background(), principal, DefinitionListQuery{TenantID: "tenant-1", Kind: KindObject, Limit: 1, Cursor: first.Page.NextCursor})
	if err != nil || len(second.Items) != 1 || second.Items[0].ID != "zeta" || second.Page.HasMore {
		t.Fatalf("second page = %+v, err=%v", second, err)
	}
	if _, err := service.ListDefinitions(context.Background(), schemaPrincipal("user-2"), DefinitionListQuery{TenantID: "tenant-1", Kind: KindObject, Limit: 1, Cursor: first.Page.NextCursor}); !errors.Is(err, ontologycontract.ErrCursorStale) {
		t.Fatalf("cross-principal cursor error = %v", err)
	}
}

func TestServiceMasksDetailAndDoesNotLeakHiddenReferenceTargets(t *testing.T) {
	registry := &schemaTestRegistry{definitions: []ontology.RegistryDefinition{
		objectDefinitionWithLinks("object-a", "tenant-1", "Object A", []string{"link-visible", "link-hidden"}),
		linkDefinition("link-visible", "tenant-1", "object-a", "object-b"),
		linkDefinition("link-hidden", "tenant-1", "object-a", "object-b"),
		objectDefinition("object-b", "tenant-1", "Object B"),
	}}
	policy := schemaTestPolicyAdapter{hidden: map[string]bool{"link:link-hidden": true}, masked: map[string][]string{"object:object-a": {"attributes.canonical_key_rule", "references.allows_link"}}}
	service := newSchemaServiceForTest(t, registry, nil, schemaTestAuthorizer{}, policy)
	detail, err := service.GetDefinition(context.Background(), schemaPrincipal("user-1"), "tenant-1", KindObject, "object-a")
	if err != nil {
		t.Fatalf("GetDefinition: %v", err)
	}
	if detail.Attributes.CanonicalKeyRule != "" {
		t.Fatalf("masked canonical key leaked: %+v", detail.Attributes)
	}
	references, err := service.ListReferences(context.Background(), schemaPrincipal("user-1"), ReferenceQuery{TenantID: "tenant-1", Kind: KindObject, DefinitionID: "object-a", Direction: "out", Limit: 10})
	if err != nil {
		t.Fatalf("ListReferences: %v", err)
	}
	if len(references.Items) != 0 {
		t.Fatalf("masked references leaked: %+v", references.Items)
	}
	for _, ref := range references.Items {
		if ref.ID == "link-hidden" {
			t.Fatalf("hidden reference leaked: %+v", references.Items)
		}
	}
}

func TestServiceSummaryAndGraphsIncludeVisibleIsolatedDefinitions(t *testing.T) {
	registry := &schemaTestRegistry{definitions: []ontology.RegistryDefinition{
		objectDefinition("object-a", "tenant-1", "Object A"),
		objectDefinition("isolated", "tenant-1", "Isolated"),
		linkDefinition("link-a", "tenant-1", "object-a", "isolated"),
		mappingDefinition("rule-a", "tenant-1", "mission.created", []string{"object-a"}),
	}}
	actions := []actioncontract.ActionType{{ID: "action-a", TenantID: "tenant-1", Name: "Act", ObjectTypeRefs: []string{"object-a"}, Enabled: true, Version: "1"}}
	service := newSchemaServiceForTest(t, registry, actions, schemaTestAuthorizer{}, schemaTestPolicyAdapter{hidden: map[string]bool{}, masked: map[string][]string{}})
	summary, err := service.Summary(context.Background(), schemaPrincipal("user-1"), "tenant-1")
	if err != nil || summary.Data.ObjectTypes.Value == nil || *summary.Data.ObjectTypes.Value != 2 || summary.Data.MappingRules.Value == nil || *summary.Data.MappingRules.Value != 1 {
		t.Fatalf("Summary = %+v, err=%v", summary, err)
	}
	semantic, err := service.Graph(context.Background(), schemaPrincipal("user-1"), GraphQuery{TenantID: "tenant-1", View: GraphSemantic, MaxNodes: 20})
	if err != nil {
		t.Fatalf("semantic Graph: %v", err)
	}
	if !graphHasNode(semantic, "object:isolated") || !graphHasEdge(semantic, "link:link-a", "object:isolated") {
		t.Fatalf("semantic graph = %+v", semantic)
	}
	projection, err := service.Graph(context.Background(), schemaPrincipal("user-1"), GraphQuery{TenantID: "tenant-1", View: GraphProjection, MaxNodes: 20})
	if err != nil || !graphHasNode(projection, "event:mission.created") || !graphHasEdge(projection, "mapping:rule-a", "object:object-a") {
		t.Fatalf("projection graph = %+v, err=%v", projection, err)
	}
}

func TestServiceAuthorizesScopeBeforeRegistryRead(t *testing.T) {
	registry := &schemaTestRegistry{definitions: []ontology.RegistryDefinition{objectDefinition("secret", "tenant-1", "Secret")}}
	service := newSchemaServiceForTest(t, registry, nil, schemaTestAuthorizer{deny: true}, schemaTestPolicyAdapter{hidden: map[string]bool{}, masked: map[string][]string{}})
	if _, err := service.ListDefinitions(context.Background(), schemaPrincipal("user-1"), DefinitionListQuery{TenantID: "tenant-1"}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("ListDefinitions error = %v", err)
	}
	if registry.listCalls != 0 {
		t.Fatalf("registry read before scope authorization: %d", registry.listCalls)
	}
}

func TestServiceFailsClosedWhenSchemaPolicyRevisionChangesDuringQuery(t *testing.T) {
	registry := &schemaTestRegistry{definitions: []ontology.RegistryDefinition{objectDefinition("visible", "tenant-1", "Visible")}}
	policy := &schemaChangingPolicyAdapter{}
	service := newSchemaServiceForTest(t, registry, nil, schemaTestAuthorizer{}, policy)

	_, err := service.ListDefinitions(context.Background(), schemaPrincipal("user-1"), DefinitionListQuery{TenantID: "tenant-1", Kind: KindObject})
	if !errors.Is(err, ontologyscope.ErrDependencyUnavailable) || policy.calls != 2 || registry.listCalls != 1 {
		t.Fatalf("revision change err=%v policy_calls=%d registry_calls=%d", err, policy.calls, registry.listCalls)
	}
}

func TestServiceExactReadsConcealMissingWrongTenantAndHiddenDefinitions(t *testing.T) {
	registry := &schemaTestRegistry{definitions: []ontology.RegistryDefinition{
		objectDefinition("other", "tenant-2", "Other"),
		objectDefinition("hidden", "tenant-1", "Hidden"),
	}}
	service := newSchemaServiceForTest(t, registry, nil, schemaTestAuthorizer{}, schemaTestPolicyAdapter{
		hidden: map[string]bool{"object:hidden": true}, masked: map[string][]string{},
	})
	for _, id := range []string{"missing", "other", "hidden"} {
		if _, err := service.GetDefinition(context.Background(), schemaPrincipal("user-1"), "tenant-1", KindObject, id); !errors.Is(err, ontologycontract.ErrUnavailable) {
			t.Fatalf("GetDefinition(%s) error = %v", id, err)
		}
	}

	registry.getErr = errors.New("database unavailable")
	if _, err := service.GetDefinition(context.Background(), schemaPrincipal("user-1"), "tenant-1", KindObject, "hidden"); !errors.Is(err, ontologyscope.ErrDependencyUnavailable) {
		t.Fatalf("dependency error = %v", err)
	}
}

func TestServiceUsesDefinitionOwnedTenantForVisibilityPolicy(t *testing.T) {
	registry := &schemaTestRegistry{definitions: []ontology.RegistryDefinition{
		objectDefinition("global", "", "Global"),
		objectDefinition("tenant", "tenant-1", "Tenant"),
	}}
	seen := map[string]string{}
	service := newSchemaServiceForTest(t, registry, nil, schemaTestAuthorizer{}, schemaTestPolicyAdapter{
		hidden: map[string]bool{}, masked: map[string][]string{}, seenTenant: seen,
	})
	if _, err := service.ListDefinitions(context.Background(), schemaPrincipal("user-1"), DefinitionListQuery{TenantID: "tenant-1", Kind: KindObject}); err != nil {
		t.Fatalf("ListDefinitions: %v", err)
	}
	if seen["object:global"] != "system" || seen["object:tenant"] != "tenant-1" {
		t.Fatalf("policy tenants = %+v", seen)
	}
}

func TestServiceDeduplicatesReferencesAndStrictlyBoundsGraphNodes(t *testing.T) {
	registry := &schemaTestRegistry{definitions: []ontology.RegistryDefinition{
		objectDefinitionWithLinks("object-a", "tenant-1", "Object A", []string{"link-a", "link-a"}),
		objectDefinition("object-b", "tenant-1", "Object B"),
		objectDefinition("object-c", "tenant-1", "Object C"),
		linkDefinition("link-a", "tenant-1", "object-a", "object-b"),
		mappingDefinition("rule-a", "tenant-1", "mission.created", []string{"object-a"}),
	}}
	service := newSchemaServiceForTest(t, registry, nil, schemaTestAuthorizer{}, schemaTestPolicyAdapter{hidden: map[string]bool{}, masked: map[string][]string{}})
	references, err := service.ListReferences(context.Background(), schemaPrincipal("user-1"), ReferenceQuery{TenantID: "tenant-1", Kind: KindObject, DefinitionID: "object-a", Direction: "out", Limit: 10})
	if err != nil || len(references.Items) != 1 || references.Items[0].ID != "link-a" {
		t.Fatalf("references = %+v, err=%v", references, err)
	}
	semantic, err := service.Graph(context.Background(), schemaPrincipal("user-1"), GraphQuery{TenantID: "tenant-1", View: GraphSemantic, MaxNodes: 2})
	if err != nil || len(semantic.Nodes) != 2 || !semantic.Truncated {
		t.Fatalf("semantic graph = %+v, err=%v", semantic, err)
	}
	projection, err := service.Graph(context.Background(), schemaPrincipal("user-1"), GraphQuery{TenantID: "tenant-1", View: GraphProjection, Search: "mission.created", MaxNodes: 1})
	if err != nil || len(projection.Nodes) != 1 || !projection.Truncated || !graphHasNode(projection, "mapping:rule-a") {
		t.Fatalf("projection graph = %+v, err=%v", projection, err)
	}
	projection, err = service.Graph(context.Background(), schemaPrincipal("user-1"), GraphQuery{TenantID: "tenant-1", View: GraphProjection, Search: "mission.created", MaxNodes: 3})
	if err != nil || len(projection.Nodes) != 3 || projection.Truncated || !graphHasNode(projection, "event:mission.created") || !graphHasNode(projection, "mapping:rule-a") || !graphHasNode(projection, "object:object-a") || len(projection.Edges) != 2 {
		t.Fatalf("complete projection search graph = %+v, err=%v", projection, err)
	}
	projection, err = service.Graph(context.Background(), schemaPrincipal("user-1"), GraphQuery{TenantID: "tenant-1", View: GraphProjection, Search: "Object A", MaxNodes: 3})
	if err != nil || len(projection.Nodes) != 3 || projection.Truncated || !graphHasNode(projection, "event:mission.created") || !graphHasNode(projection, "mapping:rule-a") || !graphHasNode(projection, "object:object-a") {
		t.Fatalf("target-matched projection search graph = %+v, err=%v", projection, err)
	}
}

func TestServicePaginatesIncomingReferencesWithoutExceedingTheReadBudget(t *testing.T) {
	definitions := []ontology.RegistryDefinition{objectDefinition("target", "tenant-1", "Target")}
	for index := 0; index < 1200; index++ {
		definitions = append(definitions, objectDefinition(fmt.Sprintf("unused-%04d", index), "tenant-1", "Unused"))
	}
	definitions = append(definitions, linkDefinition("link-incoming", "tenant-1", "target", "other"))
	registry := &schemaTestRegistry{definitions: definitions}
	service := newSchemaServiceForTest(t, registry, nil, schemaTestAuthorizer{}, schemaTestPolicyAdapter{hidden: map[string]bool{}, masked: map[string][]string{}})

	cursor := ""
	found := false
	for pageNumber := 0; pageNumber < 8; pageNumber++ {
		page, err := service.ListReferences(context.Background(), schemaPrincipal("user-1"), ReferenceQuery{TenantID: "tenant-1", Kind: KindObject, DefinitionID: "target", Direction: "in", Limit: 25, Cursor: cursor})
		if err != nil {
			t.Fatalf("ListReferences page %d: %v", pageNumber, err)
		}
		for _, item := range page.Items {
			if item.ID == "link-incoming" && item.Direction == "in" {
				found = true
			}
		}
		if !page.Page.HasMore {
			break
		}
		if page.Page.NextCursor == "" {
			t.Fatalf("page %d omitted its continuation cursor", pageNumber)
		}
		cursor = page.Page.NextCursor
	}
	if !found {
		t.Fatalf("incoming reference was not reached through bounded pages")
	}
}

func TestServiceSemanticGraphSearchExpandsTwoConnectedHops(t *testing.T) {
	registry := &schemaTestRegistry{definitions: []ontology.RegistryDefinition{
		objectDefinitionWithLinks("seed", "tenant-1", "Needle seed", []string{"link-one"}),
		objectDefinitionWithLinks("neighbor", "tenant-1", "Neighbor", []string{"link-two"}),
		objectDefinition("third-hop", "tenant-1", "Third hop"),
		linkDefinition("link-one", "tenant-1", "seed", "neighbor"),
		linkDefinition("link-two", "tenant-1", "neighbor", "third-hop"),
	}}
	service := newSchemaServiceForTest(t, registry, nil, schemaTestAuthorizer{}, schemaTestPolicyAdapter{hidden: map[string]bool{}, masked: map[string][]string{}})

	graph, err := service.Graph(context.Background(), schemaPrincipal("user-1"), GraphQuery{
		TenantID: "tenant-1", View: GraphSemantic, Search: "Needle", MaxNodes: 20,
	})
	if err != nil {
		t.Fatalf("semantic search graph: %v", err)
	}
	if !graphHasNode(graph, "object:seed") || !graphHasNode(graph, "link:link-one") || !graphHasNode(graph, "object:neighbor") {
		t.Fatalf("two-hop neighborhood missing: %+v", graph)
	}
	if graphHasNode(graph, "link:link-two") || graphHasNode(graph, "object:third-hop") {
		t.Fatalf("graph expanded beyond two hops: %+v", graph.Nodes)
	}
	if !graphHasEdge(graph, "object:seed", "link:link-one") || !graphHasEdge(graph, "link:link-one", "object:neighbor") {
		t.Fatalf("expanded graph lost connecting edges: %+v", graph.Edges)
	}
	for _, node := range graph.Nodes {
		if node.ID == "object:seed" && !node.Matched {
			t.Fatalf("direct match was not marked: %+v", node)
		}
		if node.ID != "object:seed" && node.Matched {
			t.Fatalf("indirect node was marked as direct: %+v", node)
		}
	}
}

func TestServiceProjectionGraphDoesNotScanUnreferencedDefinitions(t *testing.T) {
	definitions := make([]ontology.RegistryDefinition, 0, 1202)
	definitions = append(definitions, objectDefinition("target", "tenant-1", "Target"))
	for index := 0; index < 1200; index++ {
		definitions = append(definitions, objectDefinition(fmt.Sprintf("unused-%04d", index), "tenant-1", "Unused"))
	}
	definitions = append(definitions, mappingDefinition("rule-a", "tenant-1", "mission.created", []string{"target"}))
	registry := &schemaTestRegistry{definitions: definitions}
	service := newSchemaServiceForTest(t, registry, nil, schemaTestAuthorizer{}, schemaTestPolicyAdapter{hidden: map[string]bool{}, masked: map[string][]string{}})
	graph, err := service.Graph(context.Background(), schemaPrincipal("user-1"), GraphQuery{TenantID: "tenant-1", View: GraphProjection, MaxNodes: 3})
	if err != nil || graph.Truncated || len(graph.Nodes) != 3 || !graphHasNode(graph, "object:target") {
		t.Fatalf("bounded projection graph = %+v, err=%v", graph, err)
	}
	if registry.listCalls != 1 || len(registry.listedKinds) != 1 || registry.listedKinds[0] != ontology.RegistryDefinitionMapping || registry.batchCalls != 1 {
		t.Fatalf("projection registry reads: list=%d kinds=%v batch=%d", registry.listCalls, registry.listedKinds, registry.batchCalls)
	}
}

func TestServiceProjectionGraphSearchesVisibleTargetLabelsBeforeNodeLimit(t *testing.T) {
	registry := &schemaTestRegistry{definitions: []ontology.RegistryDefinition{
		objectDefinition("a-hidden", "tenant-1", "Hidden target"),
		objectDefinition("c-other", "tenant-1", "Other target"),
		objectDefinition("z-target", "tenant-1", "Needle target"),
		mappingDefinition("rule-a", "tenant-1", "mission.created", []string{"a-hidden", "b-missing", "c-other", "z-target"}),
	}}
	service := newSchemaServiceForTest(t, registry, nil, schemaTestAuthorizer{}, schemaTestPolicyAdapter{
		hidden: map[string]bool{"object:a-hidden": true}, masked: map[string][]string{},
	})
	graph, err := service.Graph(context.Background(), schemaPrincipal("user-1"), GraphQuery{
		TenantID: "tenant-1", View: GraphProjection, Search: "Needle target", MaxNodes: 3,
	})
	if err != nil || !graph.Truncated || len(graph.Nodes) != 3 || !graphHasNode(graph, "mapping:rule-a") ||
		!graphHasNode(graph, "event:mission.created") || !graphHasNode(graph, "object:z-target") {
		t.Fatalf("target-label projection graph=%+v err=%v", graph, err)
	}
	if graphHasNode(graph, "object:a-hidden") || graphHasNode(graph, "object:c-other") {
		t.Fatalf("hidden or lower-priority target consumed visible node bound: %+v", graph.Nodes)
	}
}

func TestServiceSummaryAndGraphUsePrincipalConcurrencyLimits(t *testing.T) {
	service := newSchemaServiceForTest(t, &schemaTestRegistry{}, nil, schemaTestAuthorizer{}, schemaTestPolicyAdapter{hidden: map[string]bool{}, masked: map[string][]string{}})
	principal := schemaPrincipal("user-1")
	principalKey := authorization.PrincipalCacheKey(principal)
	releaseOne, err := service.concurrency.Acquire(context.Background(), ontologycontract.ConcurrencySummary, principalKey)
	if err != nil {
		t.Fatalf("acquire summary one: %v", err)
	}
	defer releaseOne()
	releaseTwo, err := service.concurrency.Acquire(context.Background(), ontologycontract.ConcurrencySummary, principalKey)
	if err != nil {
		t.Fatalf("acquire summary two: %v", err)
	}
	defer releaseTwo()
	if _, err := service.Summary(context.Background(), principal, "tenant-1"); !errors.Is(err, ontologycontract.ErrRateLimited) {
		t.Fatalf("summary concurrency error = %v", err)
	}

	releaseGraph, err := service.concurrency.Acquire(context.Background(), ontologycontract.ConcurrencySchemaGraph, principalKey)
	if err != nil {
		t.Fatalf("acquire graph: %v", err)
	}
	defer releaseGraph()
	if _, err := service.Graph(context.Background(), principal, GraphQuery{TenantID: "tenant-1", MaxNodes: 10}); !errors.Is(err, ontologycontract.ErrRateLimited) {
		t.Fatalf("graph concurrency error = %v", err)
	}
}

func newSchemaServiceForTest(t *testing.T, registry *schemaTestRegistry, actions []actioncontract.ActionType, authorizer schemaTestAuthorizer, adapter datavisibility.Adapter) *Service {
	t.Helper()
	principal := schemaPrincipal("user-1")
	binding := ontologyscope.ScopeBinding{TenantID: "tenant-1", Kind: ontologyscope.KindCustom, SourceObject: identity.ObjectRef{Type: "workspace", ID: "tenant-1"}, AuthorizationTenantID: "tenant-1", ReadAction: "workspace.view", ManageAction: "workspace.manage", BindingVersion: 1, State: ontologyscope.BindingStateActive}
	gate := ontologyscope.NewScopeAccessGate(schemaTestResolver{binding: binding}, authorizer, schemaTestSnapshots{})
	codec, err := ontologycontract.NewCursorCodec(ontologycontract.CursorCodecOptions{PrimaryID: "test", Keys: []ontologycontract.CursorKey{{ID: "test", Key: bytes.Repeat([]byte{4}, 32)}}})
	if err != nil {
		t.Fatalf("NewCursorCodec: %v", err)
	}
	stateful, err := ontologycontract.NewStateCursorStore(ontologycontract.StateCursorOptions{MaxEntries: 100})
	if err != nil {
		t.Fatalf("NewStateCursorStore: %v", err)
	}
	visibility := datavisibility.NewService(datavisibility.Register(datavisibility.ResourceDefinition, adapter))
	service, err := NewService(gate, registry, schemaTestActions{items: actions}, visibility, CursorSet{Encrypted: codec, Stateful: stateful}, nil, nil)
	if err != nil {
		t.Fatalf("NewService(%+v): %v", principal, err)
	}
	service.now = func() time.Time { return time.Unix(1000, 0).UTC() }
	return service
}

func schemaPrincipal(id string) identity.Principal {
	return identity.NormalizePrincipal(identity.Principal{SubjectProvider: "oidc", Subject: id, UserID: id, ViewerKind: identity.ViewerKindUser, CoarseScopes: []string{"operations.read"}})
}

func objectDefinition(id, tenant, label string) ontology.RegistryDefinition {
	return objectDefinitionWithLinks(id, tenant, label, nil)
}

func objectDefinitionWithLinks(id, tenant, label string, links []string) ontology.RegistryDefinition {
	value := ontology.ObjectType{ID: id, TenantID: tenant, DisplayName: label, CanonicalKeyRule: id + ":{id}", AllowedLinkTypes: links, Version: 1, UpdatedAt: time.Unix(10, 0)}
	return ontology.RegistryDefinition{Kind: ontology.RegistryDefinitionObject, ID: id, TenantID: tenant, ObjectType: &value}
}

func linkDefinition(id, tenant, from, to string) ontology.RegistryDefinition {
	value := ontology.LinkType{ID: id, TenantID: tenant, DisplayName: id, FromObjectType: from, ToObjectType: to, Version: 1, UpdatedAt: time.Unix(11, 0)}
	return ontology.RegistryDefinition{Kind: ontology.RegistryDefinitionLink, ID: id, TenantID: tenant, LinkType: &value}
}

func mappingDefinition(id, tenant, event string, objects []string) ontology.RegistryDefinition {
	value := ontology.MappingRule{ID: id, TenantID: tenant, EventType: event, ObjectTypes: objects, Enabled: true, Version: 1, UpdatedAt: time.Unix(12, 0)}
	return ontology.RegistryDefinition{Kind: ontology.RegistryDefinitionMapping, ID: id, TenantID: tenant, MappingRule: &value}
}

func graphHasNode(graph Graph, id string) bool {
	for _, node := range graph.Nodes {
		if node.ID == id {
			return true
		}
	}
	return false
}

func graphHasEdge(graph Graph, source, target string) bool {
	for _, edge := range graph.Edges {
		if edge.SourceID == source && edge.TargetID == target {
			return true
		}
	}
	return false
}
