package aggregate

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"orrery_v2/internal/app/identity"
	"orrery_v2/internal/app/ontologyscope"
	ontologycontract "orrery_v2/internal/app/ontologyview/contract"
	schemaview "orrery_v2/internal/app/ontologyview/schema"
)

type fakeSchemaReader struct {
	definitionCalls []schemaview.DefinitionListQuery
	graphCalls      []schemaview.GraphQuery
}

type largeSchemaReader struct {
	fakeSchemaReader
	items []schemaview.DefinitionItem
}

func (r *largeSchemaReader) ListDefinitions(_ context.Context, _ identity.Principal, query schemaview.DefinitionListQuery) (ontologycontract.CursorPage[schemaview.DefinitionItem], error) {
	if query.TenantID != "system" || query.Origin != schemaview.OriginSystem {
		return fakePage([]schemaview.DefinitionItem{}, query.Limit, false, ""), nil
	}
	offset := 0
	if query.Cursor != "" {
		var err error
		offset, err = strconv.Atoi(query.Cursor)
		if err != nil {
			return ontologycontract.CursorPage[schemaview.DefinitionItem]{}, err
		}
	}
	end := min(offset+query.Limit, len(r.items))
	hasMore := end < len(r.items)
	next := ""
	if hasMore {
		next = strconv.Itoa(end)
	}
	return fakePage(r.items[offset:end], query.Limit, hasMore, next), nil
}

func (*fakeSchemaReader) Summary(context.Context, identity.Principal, string) (ontologycontract.Summary[schemaview.SummaryData], error) {
	panic("aggregate schema summary must derive counts from definitions")
}

func (f *fakeSchemaReader) ListDefinitions(_ context.Context, _ identity.Principal, query schemaview.DefinitionListQuery) (ontologycontract.CursorPage[schemaview.DefinitionItem], error) {
	f.definitionCalls = append(f.definitionCalls, query)
	updated := time.Date(2026, 8, 6, 0, 0, 0, 0, time.UTC)
	switch {
	case query.TenantID == "system" && query.Origin == schemaview.OriginSystem:
		items := []schemaview.DefinitionItem{
			{Kind: schemaview.KindObject, ID: "global-object", Origin: schemaview.OriginSystem, UpdatedAt: &updated},
			{Kind: schemaview.KindProperty, ID: "global-property", Origin: schemaview.OriginSystem, UpdatedAt: &updated},
		}
		return fakePage(filterDefinitionKind(items, query.Kind), query.Limit, false, ""), nil
	case query.TenantID == "org-1" && query.Origin == schemaview.OriginTenant:
		items := []schemaview.DefinitionItem{{Kind: schemaview.KindObject, ID: "local-object", Origin: schemaview.OriginTenant, UpdatedAt: &updated}}
		return fakePage(filterDefinitionKind(items, query.Kind), query.Limit, false, ""), nil
	default:
		return fakePage([]schemaview.DefinitionItem{}, query.Limit, false, ""), nil
	}
}

func (*fakeSchemaReader) GetDefinition(context.Context, identity.Principal, string, schemaview.DefinitionKind, string) (schemaview.DefinitionDetail, error) {
	return schemaview.DefinitionDetail{}, nil
}
func (*fakeSchemaReader) ListReferences(context.Context, identity.Principal, schemaview.ReferenceQuery) (ontologycontract.CursorPage[schemaview.DefinitionReference], error) {
	return ontologycontract.CursorPage[schemaview.DefinitionReference]{}, nil
}
func (f *fakeSchemaReader) Graph(_ context.Context, _ identity.Principal, query schemaview.GraphQuery) (schemaview.Graph, error) {
	f.graphCalls = append(f.graphCalls, query)
	global := schemaview.GraphNode{ID: "object:global", Kind: schemaview.KindObject, Origin: schemaview.OriginSystem}
	if query.TenantID == "system" {
		return schemaview.Graph{View: query.View, Nodes: []schemaview.GraphNode{global}, VisibleNodes: 1, AppliedMaxNodes: query.MaxNodes}, nil
	}
	local := schemaview.GraphNode{ID: "object:local", Kind: schemaview.KindObject, Origin: schemaview.OriginTenant}
	edge := schemaview.GraphEdge{ID: "edge", SourceID: global.ID, TargetID: local.ID, Relation: "uses"}
	return schemaview.Graph{View: query.View, Nodes: []schemaview.GraphNode{global, local}, Edges: []schemaview.GraphEdge{edge}, VisibleNodes: 2, VisibleEdges: 1, AppliedMaxNodes: query.MaxNodes}, nil
}

func filterDefinitionKind(items []schemaview.DefinitionItem, kind schemaview.DefinitionKind) []schemaview.DefinitionItem {
	if kind == "" {
		return items
	}
	filtered := make([]schemaview.DefinitionItem, 0, len(items))
	for _, item := range items {
		if item.Kind == kind {
			filtered = append(filtered, item)
		}
	}
	return filtered
}

func TestSchemaServiceReadsGlobalDefinitionsOnceAndTenantDefinitionsPerScope(t *testing.T) {
	catalog := &fakeScopeCatalog{pages: map[string]ontologycontract.CursorPage[ontologyscope.ScopeCatalogItem]{"": scopePage([]string{"org-1", "system"}, false, "")}}
	reader := &fakeSchemaReader{}
	service, err := NewSchemaService(newTestScopeEnumerator(t, catalog), reader)
	if err != nil {
		t.Fatalf("NewSchemaService: %v", err)
	}
	principal := testPrincipal("user-1")

	page, err := service.ListDefinitions(context.Background(), principal, schemaview.DefinitionListQuery{Limit: 10})
	if err != nil {
		t.Fatalf("ListDefinitions: %v", err)
	}
	if len(page.Items) != 3 || page.Items[0].TenantID != "system" || page.Items[0].Origin != schemaview.OriginSystem || page.Items[2].TenantID != "org-1" || page.Items[2].Origin != schemaview.OriginTenant {
		t.Fatalf("definitions = %+v", page.Items)
	}
	if len(reader.definitionCalls) != 2 || reader.definitionCalls[0].TenantID != "system" || reader.definitionCalls[0].Origin != schemaview.OriginSystem || reader.definitionCalls[1].TenantID != "org-1" || reader.definitionCalls[1].Origin != schemaview.OriginTenant {
		t.Fatalf("definition calls = %+v", reader.definitionCalls)
	}

	reader.definitionCalls = nil
	summary, err := service.Summary(context.Background(), principal, "")
	if err != nil {
		t.Fatalf("Summary: %v", err)
	}
	if summary.Data.ObjectTypes.Value == nil || *summary.Data.ObjectTypes.Value != 2 || summary.Data.PropertyTypes.Value == nil || *summary.Data.PropertyTypes.Value != 1 {
		t.Fatalf("summary = %+v", summary.Data)
	}
}

func TestSchemaServiceSummaryHashesLargeDefinitionSets(t *testing.T) {
	catalog := &fakeScopeCatalog{pages: map[string]ontologycontract.CursorPage[ontologyscope.ScopeCatalogItem]{"": scopePage([]string{"system"}, false, "")}}
	updated := time.Date(2026, 8, 6, 0, 0, 0, 0, time.UTC)
	reader := &largeSchemaReader{items: make([]schemaview.DefinitionItem, 600)}
	for index := range reader.items {
		reader.items[index] = schemaview.DefinitionItem{
			Kind:      schemaview.KindObject,
			ID:        fmt.Sprintf("object-type-%04d", index),
			Version:   "v1",
			Status:    schemaview.StatusActive,
			Origin:    schemaview.OriginSystem,
			UpdatedAt: &updated,
		}
	}
	service, err := NewSchemaService(newTestScopeEnumerator(t, catalog), reader)
	if err != nil {
		t.Fatalf("NewSchemaService: %v", err)
	}

	summary, err := service.Summary(context.Background(), testPrincipal("user-1"), "")
	if err != nil {
		t.Fatalf("Summary: %v", err)
	}
	if summary.Data.ObjectTypes.Value == nil || *summary.Data.ObjectTypes.Value != 600 || summary.Meta.DataRevision == "" {
		t.Fatalf("summary = %+v meta = %+v", summary.Data, summary.Meta)
	}
}

func TestSchemaServiceGraphDeduplicatesGlobalsAndQualifiesTenantNodes(t *testing.T) {
	catalog := &fakeScopeCatalog{pages: map[string]ontologycontract.CursorPage[ontologyscope.ScopeCatalogItem]{"": scopePage([]string{"org-1", "system"}, false, "")}}
	reader := &fakeSchemaReader{}
	service, err := NewSchemaService(newTestScopeEnumerator(t, catalog), reader)
	if err != nil {
		t.Fatalf("NewSchemaService: %v", err)
	}
	graph, err := service.Graph(context.Background(), testPrincipal("user-1"), schemaview.GraphQuery{MaxNodes: 10})
	if err != nil {
		t.Fatalf("Graph: %v", err)
	}
	if len(graph.Nodes) != 2 || len(graph.Edges) != 1 || graph.Nodes[0].ID == "object:global" || graph.Nodes[0].DefinitionID == "" || graph.Nodes[0].TenantID == "" {
		t.Fatalf("graph = %+v", graph)
	}
	if graph.Edges[0].SourceID == "object:global" || graph.Edges[0].TargetID == "object:local" || !strings.HasPrefix(graph.Edges[0].ID, "q1.") {
		t.Fatalf("qualified edge = %+v", graph.Edges[0])
	}
}
