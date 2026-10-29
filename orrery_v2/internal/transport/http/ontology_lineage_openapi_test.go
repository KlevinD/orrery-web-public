package httptransport

import (
	"os"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestOpenAPIContractDocumentsBoundedOntologyLineageAPI(t *testing.T) {
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
		"/api/ontology/lineage/summary",
		"/api/ontology/lineage/facts/{fact_id}/provenance",
		"/api/ontology/lineage/facts/{fact_id}/assertions",
		"/api/ontology/lineage/facts/{fact_id}/evidence",
		"/api/ontology/lineage/facts/{fact_id}/projection-trace",
	} {
		if _, exists := paths[path]; !exists {
			t.Fatalf("OpenAPI missing %s", path)
		}
	}
	schemas := document["components"].(map[string]any)["schemas"].(map[string]any)
	for _, name := range []string{
		"OntologyLineageSummaryDTO",
		"OntologyLineageProvenanceAnchorDTO",
		"OntologyLineageAssertionPageDTO",
		"OntologyLineageEvidencePageDTO",
		"OntologyLineageProjectionTracePageDTO",
	} {
		if _, exists := schemas[name]; !exists {
			t.Fatalf("OpenAPI missing schema %s", name)
		}
	}
	if _, exists := schemas["FactLineageDTO"]; exists {
		t.Fatal("OpenAPI must not expose an all-resource FactLineageDTO")
	}
	anchor := schemas["OntologyLineageProvenanceAnchorDTO"].(map[string]any)["properties"].(map[string]any)
	for _, forbidden := range []string{"assertions", "evidence", "projection_trace", "payload", "raw_payload", "source_access_ref"} {
		if _, exists := anchor[forbidden]; exists {
			t.Fatalf("provenance anchor exposes %s", forbidden)
		}
	}
	event := schemas["OntologyLineageSafeEventMetadataDTO"].(map[string]any)["properties"].(map[string]any)
	for _, forbidden := range []string{"payload", "raw_payload", "metadata", "source_access_ref"} {
		if _, exists := event[forbidden]; exists {
			t.Fatalf("safe event metadata exposes %s", forbidden)
		}
	}
	for _, name := range []string{"OntologyLineageAssertionPageDTO", "OntologyLineageEvidencePageDTO", "OntologyLineageProjectionTracePageDTO"} {
		items := schemas[name].(map[string]any)["properties"].(map[string]any)["items"].(map[string]any)
		if items["maxItems"] != 100 {
			t.Fatalf("%s maxItems = %v", name, items["maxItems"])
		}
	}
}
