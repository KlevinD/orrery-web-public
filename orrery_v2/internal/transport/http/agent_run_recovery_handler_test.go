package httptransport

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"orrery_v2/internal/app/agentrunrecovery"
	"orrery_v2/internal/app/missioncontrol"
	missioncore "orrery_v2/internal/mission/core"
)

type recoveryRouteStub struct {
	*fakeMissionQuery
	reads, reviews int
	mission        missioncore.MissionID
	run, key       string
	viewer         missioncontrol.ViewerRef
}

func (s *recoveryRouteStub) GetAgentRunRecovery(_ context.Context, viewer missioncontrol.ViewerRef, mission missioncore.MissionID, run string) (agentrunrecovery.View, error) {
	s.reads++
	s.viewer, s.mission, s.run = viewer, mission, run
	return agentrunrecovery.View{}, nil
}
func (s *recoveryRouteStub) ReviewAgentRunRecovery(_ context.Context, viewer missioncontrol.ViewerRef, mission missioncore.MissionID, run, key string, input agentrunrecovery.ReviewInput) (agentrunrecovery.Review, error) {
	s.reviews++
	s.viewer, s.mission, s.run, s.key = viewer, mission, run, key
	return agentrunrecovery.Review{TaskReviewInput: input, ActorID: viewer.ViewerID}, nil
}

func TestAgentRunRecoveryRoutesPermissionsAndStrictReviewBody(t *testing.T) {
	const root = "/api/missions/mission-1/agent-runs/run-1/recovery"
	const valid = `{"revision":"9007199254740993","conclusion":"uncertain","note":"checked","evidence_refs":["receipt:42"]}`
	for _, tc := range []struct {
		name, method, path, scope, body, key string
		status                               int
		called                               bool
	}{
		{"read", "GET", root, "operations.read", "", "", 200, true},
		{"manage-can-read", "GET", root, "operations.manage", "", "", 200, true},
		{"mission-only-denied", "GET", root, "mission.read", "", "", 403, false},
		{"read-cannot-review", "POST", root + "/reviews", "operations.read", valid, "key", 403, false},
		{"review", "POST", root + "/reviews", "operations.manage", valid, "key", 201, true},
		{"missing-key", "POST", root + "/reviews", "operations.manage", valid, "", 400, false},
		{"actor-injection", "POST", root + "/reviews", "operations.manage", strings.TrimSuffix(valid, "}") + `,"actor_id":"admin"}`, "key", 400, false},
		{"tenant-injection", "POST", root + "/reviews", "operations.manage", strings.TrimSuffix(valid, "}") + `,"tenant_id":"other"}`, "key", 400, false},
		{"trailing-json", "POST", root + "/reviews", "operations.manage", valid + ` {}`, "key", 400, false},
		{"too-large", "POST", root + "/reviews", "operations.manage", `{"note":"` + strings.Repeat("x", 9000) + `"}`, "key", 400, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			service := &recoveryRouteStub{fakeMissionQuery: &fakeMissionQuery{}}
			handler := newTestRouter(RouterOptions{MissionQuery: service})
			req := authedRequest(tc.method, tc.path, tc.body)
			req.Header.Set("X-Permission-Scope", tc.scope)
			req.Header.Set("X-Viewer-Kind", "user")
			req.Header.Set("Idempotency-Key", tc.key)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != tc.status {
				t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
			}
			if (service.reads+service.reviews == 1) != tc.called {
				t.Fatal("invalid request reached service")
			}
			if tc.called && (service.mission != "mission-1" || service.run != "run-1" || service.viewer.Tenant() != "system" || rec.Header().Get("Cache-Control") != "no-store") {
				t.Fatal("route lost trusted context or cache protection")
			}
		})
	}
	service := &recoveryRouteStub{fakeMissionQuery: &fakeMissionQuery{}}
	rec := httptest.NewRecorder()
	newTestRouter(RouterOptions{MissionQuery: service}).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, root, nil))
	if rec.Code != 401 || service.reads != 0 {
		t.Fatal("anonymous access reached recovery service")
	}
}
