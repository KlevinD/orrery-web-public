package httptransport

import (
	"os"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestOpenAPIContractDocumentsNarrowOntologyProjectionReadAPI(t *testing.T) {
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
		"/api/ontology/projections/summary",
		"/api/ontology/projections/audits",
		"/api/ontology/projections/audits/{audit_id}",
		"/api/ontology/projections/audits/{audit_id}/replay-history",
		"/api/ontology/projections/audits/{audit_id}/replays",
		"/api/ontology/projections/audits/{audit_id}/acknowledgements",
	} {
		if _, exists := paths[path]; !exists {
			t.Fatalf("OpenAPI missing %s", path)
		}
	}
	for _, path := range []string{
		"/api/ontology/projections/audits/{audit_id}/replays",
		"/api/ontology/projections/audits/{audit_id}/acknowledgements",
	} {
		operation := paths[path].(map[string]any)["post"].(map[string]any)
		var found bool
		for _, rawParameter := range operation["parameters"].([]any) {
			parameter, ok := rawParameter.(map[string]any)
			if !ok || parameter["name"] != "Idempotency-Key" {
				continue
			}
			found = parameter["in"] == "header" && parameter["required"] == true
			schema := parameter["schema"].(map[string]any)
			if schema["minLength"] != 1 || schema["maxLength"] != 256 {
				t.Fatalf("%s idempotency bounds = %+v", path, schema)
			}
		}
		if !found {
			t.Fatalf("OpenAPI %s does not require Idempotency-Key", path)
		}
	}
	schemas := document["components"].(map[string]any)["schemas"].(map[string]any)
	for _, name := range []string{
		"OntologyProjectionSummaryDTO",
		"OntologyProjectionAuditPageDTO",
		"OntologyProjectionAuditDetailDTO",
		"OntologyProjectionReplayHistoryPageDTO",
		"OntologyProjectionReplayRequestDTO",
		"OntologyProjectionAcknowledgeRequestDTO",
		"OntologyProjectionMutationResultDTO",
	} {
		if _, exists := schemas[name]; !exists {
			t.Fatalf("OpenAPI missing schema %s", name)
		}
	}
	if _, exists := schemas["OntologyProjectionWorkspaceDTO"]; exists {
		t.Fatal("OpenAPI must not expose an all-resource projection workspace DTO")
	}
	auditItem := schemas["OntologyProjectionAuditItemDTO"].(map[string]any)["properties"].(map[string]any)
	if _, exists := auditItem["tenant_id"]; !exists {
		t.Fatal("projection audit list item must expose its authorized source tenant")
	}
	for _, forbidden := range []string{"source_event_id", "reason", "details", "source_access_ref", "access_state"} {
		if _, exists := auditItem[forbidden]; exists {
			t.Fatalf("projection audit list item exposes %s", forbidden)
		}
	}
	detail := schemas["OntologyProjectionAuditDetailDTO"].(map[string]any)["allOf"].([]any)[1].(map[string]any)["properties"].(map[string]any)
	for _, forbidden := range []string{"tenant_id", "source_access_ref", "access_state", "replay_history"} {
		if _, exists := detail[forbidden]; exists {
			t.Fatalf("projection audit detail exposes %s", forbidden)
		}
	}
	history := schemas["OntologyProjectionReplayHistoryItemDTO"].(map[string]any)["properties"].(map[string]any)
	for _, forbidden := range []string{"tenant_id", "audit_id", "source_event_id", "source_event_type", "details", "source_access_ref", "access_state"} {
		if _, exists := history[forbidden]; exists {
			t.Fatalf("projection replay-history item exposes %s", forbidden)
		}
	}
	for _, name := range []string{"OntologyProjectionAuditPageDTO", "OntologyProjectionReplayHistoryPageDTO"} {
		items := schemas[name].(map[string]any)["properties"].(map[string]any)["items"].(map[string]any)
		if items["maxItems"] != 100 {
			t.Fatalf("%s maxItems = %v", name, items["maxItems"])
		}
	}
	mutation := schemas["OntologyProjectionMutationResultDTO"].(map[string]any)["properties"].(map[string]any)
	for _, forbidden := range []string{"tenant_id", "source_event_id", "source_event_type", "reason", "details", "actor_id", "actor_kind", "source_access_ref", "access_state"} {
		if _, exists := mutation[forbidden]; exists {
			t.Fatalf("projection mutation result exposes %s", forbidden)
		}
	}
	ack := schemas["OntologyProjectionAcknowledgeRequestDTO"].(map[string]any)["properties"].(map[string]any)
	for _, forbidden := range []string{"tenant_id", "audit_id", "actor_id", "actor_kind"} {
		if _, exists := ack[forbidden]; exists {
			t.Fatalf("projection acknowledgement request accepts %s", forbidden)
		}
	}
}
