package httptransport

import (
	"os"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestOpenAPIContractDocumentsOntologyReliabilityWorkspace(t *testing.T) {
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
		"/api/ontology/reliability/incidents/{incident_id}":                                                            {"get", "getOntologyReliabilityIncident"},
		"/api/ontology/reliability/incidents/{incident_id}/diagnoses":                                                  {"post", "createOntologyReliabilityDiagnosis"},
		"/api/ontology/reliability/incidents/{incident_id}/diagnoses/{diagnosis_id}/decision":                          {"post", "decideOntologyReliabilityDiagnosis"},
		"/api/ontology/reliability/incidents/{incident_id}/diagnoses/{diagnosis_id}/rules":                             {"post", "proposeOntologyReliabilityRepairRule"},
		"/api/ontology/reliability/rules/{rule_id}/activation-reviews":                                                 {"post", "requestOntologyReliabilityRuleActivationReview"},
		"/api/ontology/reliability/rules/{rule_id}/activation-reviews/{review_id}/decision":                            {"post", "decideOntologyReliabilityRuleActivationReview"},
		"/api/ontology/reliability/rules/{rule_id}/activation-reviews/{review_id}/activate":                            {"post", "activateOntologyReliabilityRepairRule"},
		"/api/ontology/reliability/incidents/{incident_id}/rules/{rule_id}/authorizations":                             {"post", "requestOntologyReliabilityAuthorization"},
		"/api/ontology/reliability/incidents/{incident_id}/rules/{rule_id}/authorizations/{authorization_id}/decision": {"post", "decideOntologyReliabilityAuthorization"},
		"/api/ontology/reliability/incidents/{incident_id}/rules/{rule_id}/authorizations/{authorization_id}/invoke":   {"post", "invokeOntologyReliabilityAuthorization"},
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
	for _, name := range []string{
		"OntologyReliabilityIncidentDetailDTO",
		"OntologyReliabilityEvidenceDTO",
		"OntologyReliabilityDiagnosisDTO",
		"OntologyReliabilityDiagnosisCreateRequest",
		"OntologyReliabilityDiagnosisDecisionRequest",
		"OntologyReliabilityRuleProposalRequest",
		"OntologyReliabilityAuthorizationRequest",
		"OntologyReliabilityActivationReviewDTO",
		"OntologyReliabilityActivationReviewRequest",
		"OntologyReliabilityActivationReviewResolutionDTO",
		"OntologyReliabilityActivationRequest",
		"OntologyReliabilityAuthorizationDecisionRequest",
		"OntologyReliabilityAuthorizationResolutionDTO",
		"OntologyReliabilityInvokeRequest",
	} {
		if _, exists := schemas[name]; !exists {
			t.Fatalf("OpenAPI missing schema %s", name)
		}
	}
	for _, name := range []string{"OntologyReliabilityDiagnosisCreateRequest", "OntologyReliabilityDiagnosisDecisionRequest", "OntologyReliabilityRuleProposalRequest", "OntologyReliabilityActivationReviewRequest", "OntologyReliabilityActivationRequest", "OntologyReliabilityAuthorizationRequest", "OntologyReliabilityAuthorizationDecisionRequest", "OntologyReliabilityInvokeRequest"} {
		schema := schemas[name].(map[string]any)
		if schema["additionalProperties"] != false {
			t.Fatalf("%s must reject client-supplied binding fields", name)
		}
		properties, _ := schema["properties"].(map[string]any)
		for _, forbidden := range []string{"args_hash", "incident_version", "rule_version", "target", "requester", "receipt_id", "hil_token_id"} {
			if _, exists := properties[forbidden]; exists {
				t.Fatalf("%s exposes server-owned input %s", name, forbidden)
			}
		}
	}
}
