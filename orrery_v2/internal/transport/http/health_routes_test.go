package httptransport

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHealthRouteReturnsRuntimeReadinessWithoutAuthHeaders(t *testing.T) {
	router := newTestRouter(RouterOptions{Readiness: func(context.Context) ReadinessReport {
		return ReadinessReport{OK: true, Checks: []ReadinessCheck{
			{Name: "db.core", OK: true},
			{Name: "authorization.relation_backfill", OK: true},
		}}
	}})

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/health", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/health status = %d body=%s", rec.Code, rec.Body.String())
	}
	var body HealthDTO
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode health response: %v", err)
	}
	if body.Status != "ready" || body.Service != "orrery-v2-api" || len(body.Checks) != 2 {
		t.Fatalf("health response = %+v", body)
	}
	for _, check := range body.Checks {
		if check.Status != "ready" {
			t.Fatalf("health check = %+v", check)
		}
	}
	if cacheControl := rec.Header().Get("Cache-Control"); cacheControl != "no-store" {
		t.Fatalf("Cache-Control = %q", cacheControl)
	}
}

func TestHealthRouteReturnsServiceUnavailableForFailedRuntimeReadiness(t *testing.T) {
	router := newTestRouter(RouterOptions{Readiness: func(context.Context) ReadinessReport {
		return ReadinessReport{OK: false, Checks: []ReadinessCheck{
			{Name: "db.core", OK: true},
			{Name: "authorization.relation_backfill", OK: false},
		}}
	}})

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/health", nil))

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("GET /api/health status = %d body=%s", rec.Code, rec.Body.String())
	}
	var body HealthDTO
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode health response: %v", err)
	}
	if body.Status != "not_ready" || len(body.Checks) != 2 || body.Checks[1].Status != "not_ready" {
		t.Fatalf("health response = %+v", body)
	}
}

func TestHealthRouteFailsClosedWithoutReadinessProbe(t *testing.T) {
	router := newTestRouter(RouterOptions{})
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/health", nil))

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("GET /api/health status = %d body=%s", rec.Code, rec.Body.String())
	}
}
