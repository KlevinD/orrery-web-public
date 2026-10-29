package httptransport

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"orrery_v2/internal/app/userauth"
)

func TestCookieMutationOriginRequiresSameOriginEvidence(t *testing.T) {
	tests := []struct {
		name   string
		origin string
		fetch  string
		want   bool
	}{
		{name: "missing evidence", want: false},
		{name: "same origin", origin: "https://app.example.test", want: true},
		{name: "same origin explicit default port", origin: "https://app.example.test:443", want: true},
		{name: "cross origin", origin: "https://evil.example.test", want: false},
		{name: "same origin fetch metadata", fetch: "same-origin", want: true},
		{name: "same site is insufficient", fetch: "same-site", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "https://app.example.test/api/settings", nil)
			if tt.origin != "" {
				req.Header.Set("Origin", tt.origin)
			}
			if tt.fetch != "" {
				req.Header.Set("Sec-Fetch-Site", tt.fetch)
			}
			if got := cookieMutationOriginAllowed(req); got != tt.want {
				t.Fatalf("cookieMutationOriginAllowed = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestRequestOriginUsesForwardedHostOnlyFromTrustedProxy(t *testing.T) {
	trusted := httptest.NewRequest(http.MethodPost, "http://internal:8080/api/settings", nil)
	trusted.RemoteAddr = "127.0.0.1:32100"
	trusted.Header.Set("X-Forwarded-Proto", "https")
	trusted.Header.Set("X-Forwarded-Host", "app.example.test")
	trusted.Header.Set("Origin", "https://app.example.test")
	if !cookieMutationOriginAllowed(trusted) {
		t.Fatalf("trusted forwarded host should define the public request origin")
	}
	trusted.Header.Set("Origin", "https://internal:8080")
	if cookieMutationOriginAllowed(trusted) {
		t.Fatalf("internal upstream host must not remain an allowed browser origin behind a trusted proxy")
	}
	trusted.Header.Set("Origin", "https://app.example.test")

	untrusted := trusted.Clone(trusted.Context())
	untrusted.RemoteAddr = "203.0.113.10:32100"
	if cookieMutationOriginAllowed(untrusted) {
		t.Fatalf("untrusted forwarded host must not affect origin validation")
	}
}

func TestRequestOriginUsesProxyAppendedForwardedHost(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "http://internal:8080/api/settings", nil)
	req.RemoteAddr = "127.0.0.1:32100"
	req.Header.Set("X-Forwarded-Proto", "https")
	req.Header.Set("X-Forwarded-Host", "evil.example.test, app.example.test")
	req.Header.Set("Origin", "https://app.example.test")
	if !cookieMutationOriginAllowed(req) {
		t.Fatalf("rightmost proxy-appended forwarded host should define the public origin")
	}
	req.Header.Set("Origin", "https://evil.example.test")
	if cookieMutationOriginAllowed(req) {
		t.Fatalf("attacker-controlled leftmost forwarded host was accepted")
	}
	if got := effectiveRequestHostname(req); got != "app.example.test" {
		t.Fatalf("effectiveRequestHostname = %q, want proxy-appended host", got)
	}
}

func TestMissionWebSocketOriginRejectsExternalSites(t *testing.T) {
	external := httptest.NewRequest(http.MethodGet, "https://app.example.test/api/missions/mission-1/stream", nil)
	external.Header.Set("Origin", "https://evil.example.test")
	if websocketOriginAllowed(external) {
		t.Fatalf("external websocket origin was accepted")
	}

	local := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8080/api/missions/mission-1/stream", nil)
	local.Header.Set("Origin", "http://localhost:5178")
	if !websocketOriginAllowed(local) {
		t.Fatalf("loopback development origins on different ports should be accepted")
	}

	proxiedPublic := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8080/api/missions/mission-1/stream", nil)
	proxiedPublic.RemoteAddr = "127.0.0.1:32100"
	proxiedPublic.Header.Set("X-Forwarded-Host", "app.example.test")
	proxiedPublic.Header.Set("Origin", "http://localhost:5178")
	if websocketOriginAllowed(proxiedPublic) {
		t.Fatalf("loopback exception must not apply to a public host behind a trusted proxy")
	}
}

func TestLocalLoginRejectsExplicitCrossOriginRequest(t *testing.T) {
	handler := NewRouter(RouterOptions{Auth: userauth.NewService(userauth.NewMemoryAccountStore())})
	crossOrigin := httptest.NewRequest(http.MethodPost, "http://app.example.test/api/auth/login/begin", strings.NewReader(`{"username":"missing"}`))
	crossOrigin.Header.Set("Origin", "http://evil.example.test")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, crossOrigin)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("cross-origin login status = %d body = %s", recorder.Code, recorder.Body.String())
	}

	sameOrigin := httptest.NewRequest(http.MethodPost, "http://app.example.test/api/auth/login/begin", strings.NewReader(`{"username":"missing"}`))
	sameOrigin.Header.Set("Origin", "http://app.example.test")
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, sameOrigin)
	if recorder.Code != http.StatusOK {
		t.Fatalf("same-origin login status = %d body = %s", recorder.Code, recorder.Body.String())
	}
}
