package httptransport

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"orrery_v2/internal/app/userauth"
)

func TestAppSessionCookieCoversDeviceAuthorizationRoute(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "https://orrery.example/api/auth/login/complete", nil)
	recorder := httptest.NewRecorder()
	setAuthSessionCookie(recorder, request, userauth.SessionDTO{AccessToken: "iat-session"})
	cookies := recorder.Result().Cookies()
	if len(cookies) != 2 || cookies[0].Name != authAccessTokenCookieName || cookies[0].Path != "/" || !cookies[0].HttpOnly || !cookies[0].Secure || cookies[0].SameSite != http.SameSiteLaxMode || cookies[1].Path != "/api" || cookies[1].MaxAge >= 0 {
		t.Fatalf("session cookie = %+v", cookies)
	}

	recorder = httptest.NewRecorder()
	clearAuthSessionCookie(recorder, request)
	cookies = recorder.Result().Cookies()
	if len(cookies) != 2 || cookies[0].Path != "/" || cookies[0].MaxAge >= 0 || cookies[1].Path != "/api" || cookies[1].MaxAge >= 0 {
		t.Fatalf("cleared session cookie = %+v", cookies)
	}
}
