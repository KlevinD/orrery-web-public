package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	ontologycontract "orrery_v2/internal/app/ontologyview/contract"
	governanceview "orrery_v2/internal/app/ontologyview/governance"
	"orrery_v2/internal/app/operations"
	"strconv"
	"strings"
	"testing"
)

func TestProductionHandlerExposesOntologyGovernanceRoute(t *testing.T) {
	root := t.TempDir()
	setTestConfigOverride(t, "paths.base_dir", root)
	setTestConfigOverride(t, "database.urls", productionTestDatabaseURLs(root))
	executable := writeCodexTextRuntimeFixture(t)
	setTestConfigOverride(t, "discussion.agent.runtime_kind", "codex")
	setTestConfigOverride(t, "discussion.agent.runtime_config", `{"executable":"`+jsonPath(executable)+`","extra_args":["--model","gpt-5-codex"]}`)
	setTestConfigOverride(t, "discussion.agent.timeout", "2s")
	selectTestServerConfig(t)

	handler, cleanup, err := newServerHandler(context.Background(), false)
	if err != nil {
		t.Fatalf("newServerHandler production: %v", err)
	}
	defer cleanup()

	governanceReq := httptest.NewRequest(http.MethodGet, "/api/ontology/governance/summary?tenant_id=system", nil)
	governanceReq.Header.Set("X-User-ID", "operator")
	governanceReq.Header.Set("X-Viewer-Kind", "user")
	governanceReq.Header.Set("X-Agent-Profile-ID", "human-superadmin")
	governanceReq.Header.Set("X-Permission-Scope", operations.PermissionOperationsRead)
	rec := httptest.NewRecorder()
	serveServerTestHTTP(t, handler, rec, governanceReq)
	if rec.Code != http.StatusOK {
		t.Fatalf("ontology governance summary status = %d body = %s", rec.Code, rec.Body.String())
	}
	var governanceSummary ontologycontract.Summary[governanceview.SummaryData]
	if err := json.NewDecoder(rec.Body).Decode(&governanceSummary); err != nil {
		t.Fatalf("decode ontology governance summary: %v", err)
	}
	authorizationRevision := ""
	if governanceSummary.Meta.AuthorizationRevision != nil {
		authorizationRevision = strings.TrimSpace(*governanceSummary.Meta.AuthorizationRevision)
	}
	_, revisionErr := strconv.ParseUint(strings.TrimPrefix(authorizationRevision, "authorization:"), 10, 64)
	if governanceSummary.Data.ActionableProposals.Value == nil || *governanceSummary.Data.ActionableProposals.Value != 0 || governanceSummary.Meta.DataRevision == "" ||
		!strings.HasPrefix(authorizationRevision, "authorization:") || revisionErr != nil {
		t.Fatalf("ontology governance summary = %+v", governanceSummary)
	}

	legacyRoutes := []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/api/ontology/registry?tenant_id=system"},
		{http.MethodGet, "/api/ontology/context-graph?tenant_id=system"},
		{http.MethodGet, "/api/projection-audits?tenant_id=system"},
		{http.MethodGet, "/api/ontology-events/replay-history?tenant_id=system"},
		{http.MethodGet, "/api/ontology/reasoning/runs?tenant_id=system"},
		{http.MethodGet, "/api/ontology/actions/runs?tenant_id=system"},
		{http.MethodGet, "/api/ontology/metadata-proposals?tenant_id=system"},
		{http.MethodGet, "/api/ontology/model-runs?tenant_id=system"},
	}
	for _, legacy := range legacyRoutes {
		t.Run("legacy route "+legacy.path, func(t *testing.T) {
			req := httptest.NewRequest(legacy.method, legacy.path, nil)
			req.Header.Set("X-User-ID", "operator")
			req.Header.Set("X-Viewer-Kind", "user")
			req.Header.Set("X-Permission-Scope", operations.PermissionOperationsRead+","+operations.PermissionOperationsManage)
			rec := httptest.NewRecorder()
			serveServerTestHTTP(t, handler, rec, req)
			if rec.Code != http.StatusForbidden {
				t.Fatalf("legacy ontology route status = %d, want %d; body = %s", rec.Code, http.StatusForbidden, rec.Body.String())
			}
		})
	}
}
