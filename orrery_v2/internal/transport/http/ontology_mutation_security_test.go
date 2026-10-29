package httptransport

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDecodeOntologyMutationBodyRejectsOversizedPayload(t *testing.T) {
	body := `{"value":"` + strings.Repeat("a", maxOntologyMutationBodyBytes) + `"}`
	req := httptest.NewRequest(http.MethodPost, "/api/ontology/mutation", strings.NewReader(body))
	var payload struct {
		Value string `json:"value"`
	}
	if err := decodeOntologyMutationBody(req, &payload); err == nil {
		t.Fatal("oversized ontology mutation body was accepted")
	}
}
