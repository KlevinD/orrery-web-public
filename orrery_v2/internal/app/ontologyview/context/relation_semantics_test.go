package contextview

import (
	"context"
	"errors"
	"testing"

	"orrery_v2/internal/app/contextgraph"
	"orrery_v2/internal/app/ontologyscope"
)

func TestServicePreservesOnlyVisibleGovernedRelationMeaning(t *testing.T) {
	for _, test := range []struct {
		name   string
		masked []string
		hidden bool
		want   string
	}{
		{"visible", nil, false, "subsidiary_of"},
		{"semantic-mask", []string{"properties.relation_code"}, false, ""},
		{"properties-mask", []string{"properties"}, false, ""},
		{"properties-wildcard", []string{"properties.*"}, false, ""},
		{"type-mask", []string{"link_type"}, false, ""},
		{"status-mask", []string{"status"}, false, ""},
		{"hidden", nil, true, ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			graph := contextGraphFixture()
			link := graph.neighborhood.Links[0]
			link.LinkType = "knowledge_entity_related_to"
			link.Properties = map[string]any{"relation_code": "subsidiary_of", "quote": "private source quote"}
			graph.neighborhood.Links = []contextgraph.Link{link}
			graph.relations[0].Link = link
			policy := contextTestPolicy{hidden: map[string]bool{"link:" + link.ID: test.hidden}, masked: map[string][]string{"link:" + link.ID: test.masked}}
			service := newContextServiceForTest(t, graph, contextTestAuthorizer{}, policy)
			neighborhood, err := service.Neighborhood(context.Background(), contextPrincipal("user-1"), NeighborhoodQuery{TenantID: "tenant-1", RootID: "alpha", Depth: 1})
			if err != nil {
				// Parent-only masks from this adapter do not classify the requested
				// child path; the policy engine must reject that incomplete decision.
				if (test.name == "properties-mask" || test.name == "properties-wildcard") && errors.Is(err, ontologyscope.ErrDependencyUnavailable) {
					return
				}
				t.Fatal(err)
			}
			if test.hidden {
				if len(neighborhood.Edges) != 0 {
					t.Fatal("hidden link disclosed")
				}
			} else if len(neighborhood.Edges) != 1 || neighborhood.Edges[0].RelationCode != test.want {
				t.Fatalf("semantic neighborhood: %+v", neighborhood)
			}
			relations, err := service.ListRelations(context.Background(), contextPrincipal("user-1"), RelationListQuery{TenantID: "tenant-1", ObjectID: "alpha"})
			if err != nil {
				t.Fatal(err)
			}
			if test.hidden {
				if len(relations.Items) != 0 {
					t.Fatal("hidden relation disclosed")
				}
			} else if len(relations.Items) != 1 || relations.Items[0].RelationCode != test.want {
				t.Fatalf("semantic relations: %+v", relations)
			}
		})
	}
}
