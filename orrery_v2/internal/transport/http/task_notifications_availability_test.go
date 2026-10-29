package httptransport

import (
	"net/http/httptest"
	"testing"
)

func TestTaskNotificationInboxReportsUnavailableWhenServiceIsMissing(t *testing.T) {
	router := newTestRouter(RouterOptions{})
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, gatewayRouteRequest("GET", "/api/notifications", "", gatewayRoutePrincipal("tenant-a", "human-a")))
	if rec.Code != 503 {
		t.Fatalf("missing task notification route: status=%d body=%s", rec.Code, rec.Body.String())
	}
}
