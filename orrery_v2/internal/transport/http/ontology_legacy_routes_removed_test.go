package httptransport

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestLegacyOntologyRoutesRemainUnregisteredAndFailClosed(t *testing.T) {
	tests := []struct {
		method string
		path   string
		body   string
	}{
		{http.MethodGet, "/api/ontology/registry?tenant_id=mission%3A1", ""},
		{http.MethodGet, "/api/ontology/context-graph?tenant_id=mission%3A1", ""},
		{http.MethodGet, "/api/projection-audits?tenant_id=mission%3A1", ""},
		{http.MethodGet, "/api/projection-audits/replay-history?tenant_id=mission%3A1", ""},
		{http.MethodPost, "/api/projection-audits/replay", `{"tenant_id":"mission:1"}`},
		{http.MethodPost, "/api/projection-audits/audit-1/replay?tenant_id=mission%3A1", `{}`},
		{http.MethodPost, "/api/projection-audits/audit-1/acknowledge?tenant_id=mission%3A1", `{"reason":"reviewed"}`},
		{http.MethodGet, "/api/ontology-events/replay-history?tenant_id=mission%3A1", ""},
		{http.MethodPost, "/api/ontology-events/replay", `{"tenant_id":"mission:1"}`},
		{http.MethodPost, "/api/ontology/reasoning/context-pack", `{"tenant_id":"mission:1"}`},
		{http.MethodGet, "/api/ontology/reasoning/runs?tenant_id=mission%3A1", ""},
		{http.MethodPost, "/api/ontology/reasoning/runs", `{"tenant_id":"mission:1"}`},
		{http.MethodGet, "/api/ontology/reasoning/runs/run-1?tenant_id=mission%3A1", ""},
		{http.MethodPost, "/api/ontology/reasoning/runs/run-1/validate?tenant_id=mission%3A1", `{}`},
		{http.MethodPost, "/api/ontology/reasoning/runs/run-1/proposals?tenant_id=mission%3A1", `{}`},
		{http.MethodGet, "/api/ontology/actions/types", ""},
		{http.MethodGet, "/api/ontology/actions/runs?tenant_id=mission%3A1", ""},
		{http.MethodPost, "/api/ontology/actions/runs", `{"tenant_id":"mission:1"}`},
		{http.MethodGet, "/api/ontology/actions/runs/run-1?tenant_id=mission%3A1", ""},
		{http.MethodPost, "/api/ontology/actions/runs/run-1/complete?tenant_id=mission%3A1", `{}`},
		{http.MethodGet, "/api/ontology/actions/runs/run-1/writebacks?tenant_id=mission%3A1", ""},
		{http.MethodGet, "/api/ontology/metadata-proposals?tenant_id=mission%3A1", ""},
		{http.MethodGet, "/api/ontology/metadata-proposals/analysis?tenant_id=mission%3A1", ""},
		{http.MethodPost, "/api/ontology/metadata-proposals/model-inference", `{"tenant_id":"mission:1"}`},
		{http.MethodPost, "/api/ontology/metadata-proposals/proposal-1/decision?tenant_id=mission%3A1", `{"status":"accepted"}`},
		{http.MethodGet, "/api/ontology/metadata-proposals/proposal-1/promotion/dry-run?tenant_id=mission%3A1", ""},
		{http.MethodPost, "/api/ontology/metadata-proposals/proposal-1/promotion/apply?tenant_id=mission%3A1", `{}`},
		{http.MethodPost, "/api/ontology/metadata-proposals/promotion/dry-run", `{"tenant_id":"mission:1"}`},
		{http.MethodPost, "/api/ontology/metadata-proposals/promotion/apply", `{"tenant_id":"mission:1"}`},
		{http.MethodGet, "/api/ontology/metadata-proposals/proposal-1/promotion/receipts?tenant_id=mission%3A1", ""},
		{http.MethodGet, "/api/ontology/model-runs?tenant_id=mission%3A1", ""},
		{http.MethodGet, "/api/ontology/model-run-proposals?tenant_id=mission%3A1", ""},
		{http.MethodGet, "/api/ontology/model-run-evaluations?tenant_id=mission%3A1", ""},
		{http.MethodGet, "/api/ontology/model-run-evaluation-alert-audits?tenant_id=mission%3A1", ""},
		{http.MethodGet, "/api/ontology/model-run-evaluation-alert-states?tenant_id=mission%3A1", ""},
		{http.MethodGet, "/api/ontology/model-run-evaluation-metric-recordings?tenant_id=mission%3A1", ""},
		{http.MethodGet, "/api/ontology/model-run-evaluation-alert-notification-receipts?tenant_id=mission%3A1", ""},
		{http.MethodGet, "/api/ontology/model-run-evaluation-alert-notification-receipt-summary?tenant_id=mission%3A1", ""},
		{http.MethodGet, "/api/ontology/model-run-evaluation-alert-notification-policies?tenant_id=mission%3A1", ""},
		{http.MethodPost, "/api/ontology/model-run-evaluation-alert-notification-policies", `{"tenant_id":"mission:1"}`},
		{http.MethodGet, "/api/ontology/model-run-evaluation-artifacts?tenant_id=mission%3A1", ""},
		{http.MethodPost, "/api/ontology/model-run-evaluation-artifacts", `{"tenant_id":"mission:1"}`},
		{http.MethodGet, "/api/ontology/model-run-evaluation-policies?tenant_id=mission%3A1", ""},
		{http.MethodPost, "/api/ontology/model-run-evaluation-policies", `{"tenant_id":"mission:1"}`},
	}

	handler := newTestRouter(RouterOptions{})
	for _, test := range tests {
		t.Run(test.method+" "+test.path, func(t *testing.T) {
			req := authedOntologyRequest(test.method, test.path, test.body)
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)

			if rec.Code != http.StatusForbidden {
				t.Fatalf("status = %d, want %d; body = %s", rec.Code, http.StatusForbidden, rec.Body.String())
			}
			if capabilities := defaultPermissionCapabilitiesForRequest(req); len(capabilities) != 0 {
				t.Fatalf("legacy route still has capabilities: %+v", capabilities)
			}
		})
	}
}

func defaultPermissionCapabilitiesForRequest(req *http.Request) []PermissionCapabilityDTO {
	router := &Router{permissions: defaultPermissionCapabilities()}
	return router.permissionsForRequest(req)
}
