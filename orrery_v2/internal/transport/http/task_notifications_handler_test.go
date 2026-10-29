package httptransport

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	core "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/identity"
	"orrery_v2/internal/app/notification"
	"orrery_v2/internal/platform/db"
)

func TestTaskNotificationRoutesAllowSelfServiceAndRejectManagerEscalation(t *testing.T) {
	ctx := context.Background()
	store, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: t.Name()})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	profiles := &fakeProfileWeixinDirectory{entries: []core.AgentDirectoryEntry{{AgentProfileID: "human-a", AgentKind: core.AgentKindHuman, Enabled: true, OrgIdentity: core.AgentOrgIdentity{OrganizationRef: core.OrganizationRef{ID: "tenant-a"}}}}}
	service := notification.New(store, nil, profiles, nil, nil, nil, "")
	if err := service.EnsureSchema(ctx); err != nil {
		t.Fatal(err)
	}
	router := newTestRouter(RouterOptions{TaskNotifications: service, AgentProfiles: profiles})
	for _, tc := range []struct {
		method, path, body, tenant string
		want                       int
	}{
		{"GET", "/api/notifications", "", "tenant-a", 200},
		{"GET", "/api/notifications/summary", "", "tenant-a", 200},
		{"GET", "/api/notifications/channels", "", "tenant-a", 200},
		{"PUT", "/api/notifications/preferences", `{"channels":["weixin"]}`, "tenant-a", 200},
		{"PUT", "/api/notifications/preferences", `{"channels":["sms"]}`, "tenant-a", 400},
		{"PUT", "/api/notifications/preferences", `{"channels":[],"human_id":"human-b"}`, "tenant-a", 400},
		{"PUT", "/api/notifications/preferences", `{"channels":[]} {}`, "tenant-a", 400},
		{"GET", "/api/notifications?offset=-1", "", "tenant-a", 400},
		{"POST", "/api/notifications/sources", `{"task_id":"task","audience":"all"}`, "tenant-a", 403},
		{"GET", "/api/notifications/members", "", "tenant-a", 403},
		{"GET", "/api/notifications/events/event/deliveries", "", "tenant-a", 403},
		{"GET", "/api/notifications", "", "tenant-b", 403},
	} {
		t.Run(tc.method+tc.path+tc.body+tc.tenant, func(t *testing.T) {
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, gatewayRouteRequest(tc.method, tc.path, tc.body, gatewayRoutePrincipal(tc.tenant, "human-a")))
			if rec.Code != tc.want {
				t.Fatalf("status %d want %d: %s", rec.Code, tc.want, rec.Body.String())
			}
			if rec.Header().Get("Cache-Control") != "private, no-store" {
				t.Fatal("personal result must not be cached")
			}
		})
	}
}

func TestTaskNotificationSystemPublicationRequiresTrustedIdentity(t *testing.T) {
	for _, trusted := range []bool{false, true} {
		req := gatewayRouteRequest(http.MethodGet, "/api/notifications/sources", "", identity.Principal{Subject: "system", UserID: "system", AgentProfileID: core.SystemSuperadminPrincipalID, ViewerKind: identity.ViewerKindSystem, TenantID: "org", CoarseScopes: []string{"scheduler.write"}})
		if trusted {
			req = req.WithContext(identity.ContextWithTrustedInternalService(req.Context()))
		}
		v, ok := (&Router{}).notificationViewer(httptest.NewRecorder(), req)
		if !ok || v.System != trusted || !v.Manage {
			t.Fatalf("trusted=%v viewer=%+v ok=%v", trusted, v, ok)
		}
	}
}
