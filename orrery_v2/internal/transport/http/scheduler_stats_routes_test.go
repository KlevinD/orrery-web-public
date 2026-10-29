package httptransport

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"orrery_v2/internal/app/scheduler"
)

func (f *fakeSchedulerService) GetTaskStats(_ context.Context, tenantID string) (scheduler.ScheduledTaskStats, error) {
	f.statsTenantID = tenantID
	return f.stats, f.err
}

func TestSchedulerStatsRouteReturnsTenantScopedCounts(t *testing.T) {
	service := &fakeSchedulerService{stats: scheduler.ScheduledTaskStats{Total: 251, Enabled: 200, Executors: 12, System: 40, Failed: 8}}
	handler := newTestRouter(RouterOptions{Scheduler: service})
	req := authedRequest(http.MethodGet, "/api/scheduled-tasks/summary?tenant_id=tenant-b&limit=1", "")
	req.Header.Set("X-Tenant-ID", "tenant-a")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var got scheduler.ScheduledTaskStats
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got != service.stats || service.statsTenantID != "tenant-a" || service.listCalled || service.getTaskID != "" {
		t.Fatalf("stats=%+v tenant=%q list=%v task=%q", got, service.statsTenantID, service.listCalled, service.getTaskID)
	}
}

func TestSchedulerStatsRoutePreservesBrowserTenantRules(t *testing.T) {
	for _, tt := range []struct {
		name, explicit, wantTenant string
		scopes                     []string
		unscoped                   bool
	}{
		{name: "organization", scopes: []string{"scheduler.read"}, wantTenant: "minvia"},
		{name: "system manager", scopes: []string{"scheduler.read", "scheduler.write", "operations.manage"}, wantTenant: "system"},
		{name: "explicit", scopes: []string{"scheduler.read"}, explicit: "tenant-explicit", wantTenant: "tenant-explicit"},
		{name: "unscoped", scopes: []string{"scheduler.read"}, unscoped: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			service := &fakeSchedulerService{}
			router := schedulerBrowserTestRouter(service)
			if tt.unscoped {
				router = &Router{scheduler: service}
			}
			req := schedulerBrowserTestRequest(router, http.MethodGet, "/api/scheduled-tasks/summary", tt.scopes, tt.explicit)
			req.Header.Set("X-Permission-Scope", "scheduler.read,scheduler.write,operations.manage")
			rec := httptest.NewRecorder()
			router.handleScheduledTaskStats(rec, req)
			wantStatus := http.StatusOK
			if tt.unscoped {
				wantStatus = http.StatusForbidden
			}
			if rec.Code != wantStatus || service.statsTenantID != tt.wantTenant {
				t.Fatalf("status=%d tenant=%q body=%s", rec.Code, service.statsTenantID, rec.Body.String())
			}
		})
	}
}

func TestSchedulerStatsRouteRejectsMissingPermissionAndPropagatesErrors(t *testing.T) {
	service := &fakeSchedulerService{err: scheduler.ErrServiceDependency}
	handler := newTestRouter(RouterOptions{Scheduler: service})
	req := authedRequest(http.MethodGet, "/api/scheduled-tasks/summary", "")
	req.Header.Set("X-Permission-Scope", "discussion.read")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden || service.statsTenantID != "" {
		t.Fatalf("unauthorized status=%d tenant=%q", rec.Code, service.statsTenantID)
	}
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, authedRequest(http.MethodGet, "/api/scheduled-tasks/summary", ""))
	if rec.Code < 500 {
		t.Fatalf("dependency error status=%d body=%s", rec.Code, rec.Body.String())
	}
}
