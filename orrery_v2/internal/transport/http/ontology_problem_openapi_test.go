package httptransport

import (
	"os"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestOpenAPIContractDocumentsOntologyProblemHub(t *testing.T) {
	raw, err := os.ReadFile("../../../docs/api/mission-control.openapi.yaml")
	if err != nil {
		t.Fatalf("read OpenAPI: %v", err)
	}
	var document map[string]any
	if err := yaml.Unmarshal(raw, &document); err != nil {
		t.Fatalf("parse OpenAPI: %v", err)
	}
	paths := document["paths"].(map[string]any)
	want := map[string]struct {
		method      string
		operationID string
	}{
		"/api/ontology/problems":                    {"get", "listOntologyProblems"},
		"/api/ontology/problems/{problem_id}":       {"get", "getOntologyProblem"},
		"/api/ontology/problems/{problem_id}/route": {"post", "routeOntologyProblem"},
	}
	for path, expected := range want {
		entry, exists := paths[path]
		if !exists {
			t.Fatalf("OpenAPI missing %s", path)
		}
		operation := entry.(map[string]any)[expected.method].(map[string]any)
		if operation["operationId"] != expected.operationID {
			t.Fatalf("%s operationId = %v", path, operation["operationId"])
		}
	}

	schemas := document["components"].(map[string]any)["schemas"].(map[string]any)
	for _, name := range []string{"OntologyProblemActorDTO", "OntologyProblemRefDTO", "OntologyProblemCaseDTO", "OntologyProblemListDTO", "OntologyProblemRouteRequest"} {
		if _, exists := schemas[name]; !exists {
			t.Fatalf("OpenAPI missing schema %s", name)
		}
	}
	routeRequest := schemas["OntologyProblemRouteRequest"].(map[string]any)
	if routeRequest["additionalProperties"] != false {
		t.Fatal("OntologyProblemRouteRequest must reject client-supplied fields")
	}
	properties := routeRequest["properties"].(map[string]any)
	for _, forbidden := range []string{"actor", "status", "problem_id", "tenant_id"} {
		if _, exists := properties[forbidden]; exists {
			t.Fatalf("OntologyProblemRouteRequest exposes server-owned input %s", forbidden)
		}
	}
}
