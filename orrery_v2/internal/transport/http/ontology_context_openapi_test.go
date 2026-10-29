package httptransport

import (
	"os"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestOpenAPIContractDocumentsBoundedOntologyContextAPI(t *testing.T) {
	raw, err := os.ReadFile("../../../docs/api/mission-control.openapi.yaml")
	if err != nil {
		t.Fatalf("read OpenAPI: %v", err)
	}
	var document map[string]any
	if err := yaml.Unmarshal(raw, &document); err != nil {
		t.Fatalf("parse OpenAPI: %v", err)
	}
	paths := document["paths"].(map[string]any)
	for _, path := range []string{
		"/api/ontology/context/company-overview",
		"/api/ontology/context/overview",
		"/api/ontology/context/summary",
		"/api/ontology/context/objects",
		"/api/ontology/context/objects/{object_id}",
		"/api/ontology/context/objects/{object_id}/facts",
		"/api/ontology/context/objects/{object_id}/relations",
		"/api/ontology/context/objects/{object_id}/evidence",
		"/api/ontology/context/neighborhood",
	} {
		if _, exists := paths[path]; !exists {
			t.Fatalf("OpenAPI missing %s", path)
		}
	}
	neighborhood := paths["/api/ontology/context/neighborhood"].(map[string]any)["get"].(map[string]any)
	hasLinkType, hasLinkTypes := false, false
	for _, raw := range neighborhood["parameters"].([]any) {
		parameter, ok := raw.(map[string]any)
		if ok && parameter["name"] == "link_type" && parameter["in"] == "query" {
			hasLinkType = true
		}
		if ok && parameter["name"] == "link_types" && parameter["in"] == "query" {
			hasLinkTypes = true
		}
	}
	if !hasLinkType {
		t.Fatal("OpenAPI neighborhood query missing link_type")
	}
	if !hasLinkTypes {
		t.Fatal("OpenAPI neighborhood query missing link_types")
	}
	schemas := document["components"].(map[string]any)["schemas"].(map[string]any)
	for _, name := range []string{
		"OntologyContextCompanyOverviewDTO",
		"OntologyContextCompanyOverviewDataDTO",
		"OntologyContextOverviewDomainDTO",
		"OntologyContextSummaryDTO",
		"OntologyContextObjectPageDTO",
		"OntologyContextObjectDetailDTO",
		"OntologyContextFactPageDTO",
		"OntologyContextRelationPageDTO",
		"OntologyContextEvidencePageDTO",
		"OntologyContextNeighborhoodDTO",
	} {
		if _, exists := schemas[name]; !exists {
			t.Fatalf("OpenAPI missing schema %s", name)
		}
	}
	objectDetail := schemas["OntologyContextObjectDetailDTO"].(map[string]any)["allOf"].([]any)[1].(map[string]any)["properties"].(map[string]any)
	for _, forbidden := range []string{"facts", "relations", "evidence", "neighborhood", "owner_access_ref", "source_access_ref"} {
		if _, exists := objectDetail[forbidden]; exists {
			t.Fatalf("object detail exposes %s", forbidden)
		}
	}
	graph := schemas["OntologyContextNeighborhoodDTO"].(map[string]any)["properties"].(map[string]any)
	if graph["nodes"].(map[string]any)["maxItems"] != 200 || graph["edges"].(map[string]any)["maxItems"] != 400 {
		t.Fatalf("neighborhood bounds = %+v", graph)
	}
}
