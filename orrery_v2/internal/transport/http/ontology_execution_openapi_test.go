package httptransport

import (
	"os"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestOpenAPIContractDocumentsOntologyExecutions(t *testing.T) {
	raw, err := os.ReadFile("../../../docs/api/mission-control.openapi.yaml")
	if err != nil {
		t.Fatalf("read OpenAPI: %v", err)
	}
	var document map[string]any
	if err := yaml.Unmarshal(raw, &document); err != nil {
		t.Fatalf("parse OpenAPI: %v", err)
	}
	paths := document["paths"].(map[string]any)
	want := map[string]string{
		"/api/ontology/executions/tasks":                 "listOntologyAgentTasks",
		"/api/ontology/executions/tasks/{task_id}":       "getOntologyAgentTask",
		"/api/ontology/executions/runs/{run_id}":         "getOntologyAgentRun",
		"/api/ontology/executions/runs/{run_id}/lineage": "getOntologyAgentRunLineage",
	}
	for path, operationID := range want {
		entry, exists := paths[path]
		if !exists {
			t.Fatalf("OpenAPI missing %s", path)
		}
		operation := entry.(map[string]any)["get"].(map[string]any)
		if operation["operationId"] != operationID {
			t.Fatalf("%s operationId = %v", path, operation["operationId"])
		}
	}
}

func TestOpenAPIContractDocumentsGovernedOntologyActionInvoke(t *testing.T) {
	raw, err := os.ReadFile("../../../docs/api/mission-control.openapi.yaml")
	if err != nil {
		t.Fatalf("read OpenAPI: %v", err)
	}
	var document map[string]any
	if err := yaml.Unmarshal(raw, &document); err != nil {
		t.Fatalf("parse OpenAPI: %v", err)
	}
	paths := document["paths"].(map[string]any)
	entry, exists := paths["/api/ontology/actions/{action_type_id}/invoke"]
	if !exists {
		t.Fatal("OpenAPI missing governed ontology action invoke")
	}
	operation := entry.(map[string]any)["post"].(map[string]any)
	if operation["operationId"] != "invokeOntologyAction" {
		t.Fatalf("action invoke operationId = %v", operation["operationId"])
	}
	components := document["components"].(map[string]any)["schemas"].(map[string]any)
	request := components["OntologyActionInvokeRequest"].(map[string]any)
	properties := request["properties"].(map[string]any)
	executionMode := properties["execution_mode"].(map[string]any)
	if executionMode["default"] != "apply" {
		t.Fatalf("action execution_mode default = %v", executionMode["default"])
	}
	for _, forbidden := range []string{"actor", "effective_user", "policy_decision_id"} {
		if _, exists := properties[forbidden]; exists {
			t.Fatalf("action invoke request exposes server-owned field %s", forbidden)
		}
	}
}
