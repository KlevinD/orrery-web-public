package httptransport

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	authorizationapp "orrery_v2/internal/app/authorization"
	"orrery_v2/internal/app/identity"
)

func TestHTTPAuthorizationDiagnosticsReturnsRelationBackfillReport(t *testing.T) {
	service := &fakeAuthorizationDiagnosticsService{result: authorizationapp.RelationBackfillResult{
		Mode:             "dry_run",
		Projects:         1,
		Missions:         2,
		Discussions:      3,
		ResourceBindings: 4,
		Tuples:           5,
		DryRun:           true,
		Report: authorizationapp.RelationBackfillReport{
			Projects:         1,
			Missions:         2,
			Discussions:      3,
			ResourceBindings: 4,
			Tuples:           5,
			ByObjectType:     map[string]int{"mission": 2},
			ByRelation:       map[string]int{"owner": 1},
			BySubjectKind:    map[string]int{"user": 1},
			ByTenant:         map[string]int{"global": 5},
		},
		Diff: authorizationapp.RelationBackfillDiff{
			Checked: 5,
			Missing: 1,
			MissingTuples: []authorizationapp.RelationTuple{{
				ID:       "global:user:planner:owner:project:project-1",
				TenantID: "global",
				Subject:  authorizationapp.SubjectRef{Kind: "user", ID: "planner"},
				Relation: "owner",
				Object:   identity.ObjectRef{Type: "project", ID: "project-1", TenantID: "global"},
			}},
		},
	}}
	handler := newTestRouter(RouterOptions{AuthorizationDiagnostics: service})
	req := authedRequest(http.MethodGet, "/api/authorization/relation-backfill/diagnostics", "")
	req.Header.Set("X-Permission-Scope", "permissions.read")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	var body AuthorizationRelationBackfillDiagnosticsDTO
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode diagnostics: %v", err)
	}
	if body.Mode != "dry_run" || !body.DryRun || body.Discussions != 3 || body.Report.Discussions != 3 || body.Report.ByObjectType["mission"] != 2 || body.Diff.Checked != 5 || body.Diff.Missing != 1 {
		t.Fatalf("diagnostics = %+v", body)
	}
	if len(body.Diff.MissingTuples) != 1 || body.Diff.MissingTuples[0].Object.ID != "project-1" {
		t.Fatalf("missing tuples = %+v", body.Diff.MissingTuples)
	}
	if service.calls != 1 {
		t.Fatalf("service calls = %d", service.calls)
	}
}

func TestHTTPAuthorizationDiagnosticsReturnsEnforceTransitionReport(t *testing.T) {
	service := &fakeAuthorizationDiagnosticsService{transition: authorizationapp.EnforceTransitionDiagnostics{
		Ready:             false,
		BackfillValidated: false,
		Shadow: authorizationapp.ShadowDecisionStats{
			ShadowDecisionCounter: authorizationapp.ShadowDecisionCounter{
				Total:                  4,
				Mismatches:             1,
				PrimaryAllowShadowDeny: 1,
			},
			ByObjectType: map[string]authorizationapp.ShadowDecisionCounter{
				"organization": {Total: 2, Mismatches: 1},
			},
			ByAction: map[string]authorizationapp.ShadowDecisionCounter{
				"organization.manage": {Total: 2, Mismatches: 1},
			},
		},
		Backfill: authorizationapp.RelationBackfillResult{
			Mode:   "dry_run",
			Tuples: 3,
			DryRun: true,
			Diff: authorizationapp.RelationBackfillDiff{
				Checked: 3,
				Missing: 1,
			},
		},
	}}
	handler := newTestRouter(RouterOptions{AuthorizationDiagnostics: service})
	req := authedRequest(http.MethodGet, "/api/authorization/enforce-transition/diagnostics", "")
	req.Header.Set("X-Permission-Scope", "permissions.read")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	var body AuthorizationEnforceTransitionDiagnosticsDTO
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode diagnostics: %v", err)
	}
	if body.Ready || body.BackfillValidated || body.Shadow.Mismatches != 1 || body.Shadow.ByObjectType["organization"].Mismatches != 1 || body.Backfill.Diff.Missing != 1 {
		t.Fatalf("diagnostics = %+v", body)
	}
	if service.transitionCalls != 1 {
		t.Fatalf("transition calls = %d", service.transitionCalls)
	}
}

func TestHTTPAuthorizationDiagnosticsRequiresPermissionsRead(t *testing.T) {
	handler := newTestRouter(RouterOptions{AuthorizationDiagnostics: &fakeAuthorizationDiagnosticsService{}})
	req := authedRequest(http.MethodGet, "/api/authorization/relation-backfill/diagnostics", "")
	req.Header.Set("X-Permission-Scope", "mission.read")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), `"code":"forbidden"`) {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
}

type fakeAuthorizationDiagnosticsService struct {
	result          authorizationapp.RelationBackfillResult
	transition      authorizationapp.EnforceTransitionDiagnostics
	err             error
	calls           int
	transitionCalls int
}

func (f *fakeAuthorizationDiagnosticsService) GetAuthorizationRelationBackfillDiagnostics(ctx context.Context) (authorizationapp.RelationBackfillResult, error) {
	_ = ctx
	f.calls++
	return f.result, f.err
}

func (f *fakeAuthorizationDiagnosticsService) GetAuthorizationEnforceTransitionDiagnostics(ctx context.Context) (authorizationapp.EnforceTransitionDiagnostics, error) {
	_ = ctx
	f.transitionCalls++
	return f.transition, f.err
}
