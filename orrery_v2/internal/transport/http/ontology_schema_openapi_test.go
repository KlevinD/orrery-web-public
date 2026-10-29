package httptransport

import (
	"os"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestOpenAPIContractDocumentsBoundedOntologySchemaAPI(t *testing.T) {
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
		"/api/ontology/schema/summary",
		"/api/ontology/schema/definitions",
		"/api/ontology/schema/definitions/{kind}/{definition_id}",
		"/api/ontology/schema/definitions/{kind}/{definition_id}/references",
		"/api/ontology/schema/graph",
	} {
		if _, exists := paths[path]; !exists {
			t.Fatalf("OpenAPI missing %s", path)
		}
	}
	schemas := document["components"].(map[string]any)["schemas"].(map[string]any)
	for _, name := range []string{
		"OntologySchemaSummaryDTO",
		"OntologySchemaDefinitionPageDTO",
		"OntologySchemaDefinitionDetailDTO",
		"OntologySchemaDefinitionReferencePageDTO",
		"OntologySchemaGraphDTO",
	} {
		if _, exists := schemas[name]; !exists {
			t.Fatalf("OpenAPI missing schema %s", name)
		}
	}
	attributes := schemas["OntologySchemaDefinitionAttributesDTO"].(map[string]any)["properties"].(map[string]any)
	if _, exposesRuleBody := attributes["rule"]; exposesRuleBody {
		t.Fatal("schema detail must not expose mapping rule body")
	}
	graph := schemas["OntologySchemaGraphDTO"].(map[string]any)["properties"].(map[string]any)
	if graph["nodes"].(map[string]any)["maxItems"] != 500 || graph["edges"].(map[string]any)["maxItems"] != 1000 {
		t.Fatalf("graph bounds = %+v", graph)
	}
}
