package httptransport

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/gorilla/websocket"

	"orrery_v2/internal/app/userauth"
	"orrery_v2/internal/device/delivery"
	deviceidentity "orrery_v2/internal/device/identity"
	"orrery_v2/internal/platform/db"
)

func TestLocalLoginReturnsToDeviceAuthorizationAndIssuesCallbackCode(t *testing.T) {
	ctx := context.Background()
	authStore := userauth.NewMemoryAccountStore()
	if err := userauth.EnsureBootstrapSuperadmin(ctx, authStore, userauth.SuperadminPassword()); err != nil {
		t.Fatalf("bootstrap superadmin: %v", err)
	}
	auth := userauth.NewService(authStore)

	deviceDB, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: t.Name()})
	if err != nil {
		t.Fatalf("open device database: %v", err)
	}
	t.Cleanup(func() { _ = deviceDB.Close() })
	deviceStore, err := deviceidentity.NewSQLStore(ctx, deviceDB)
	if err != nil {
		t.Fatalf("create device store: %v", err)
	}
	deviceService, err := deviceidentity.NewService(deviceidentity.Options{Store: deviceStore})
	if err != nil {
		t.Fatalf("create device identity service: %v", err)
	}

	base := NewRouter(RouterOptions{Auth: auth})
	handler, err := delivery.NewHandler(delivery.Options{
		Identity: deviceService,
		AuthenticateBrowser: func(ctx context.Context, request *http.Request) (deviceidentity.BrowserPrincipal, error) {
			cookie, err := request.Cookie(authAccessTokenCookieName)
			if err != nil || strings.TrimSpace(cookie.Value) == "" {
				return deviceidentity.BrowserPrincipal{}, deviceidentity.ErrUnauthorized
			}
			principal, err := auth.AuthenticatePrincipal(ctx, cookie.Value)
			if err != nil {
				return deviceidentity.BrowserPrincipal{}, deviceidentity.ErrUnauthorized
			}
			return deviceidentity.BrowserPrincipal{
				UserID: principal.UserID, DisplayName: principal.DisplayName, TenantID: principal.TenantID,
			}, nil
		},
		AuthorizeTenant: func(_ context.Context, principal deviceidentity.BrowserPrincipal, tenantID string) error {
			if strings.TrimSpace(principal.UserID) == "" || tenantID != "global" {
				return deviceidentity.ErrUnauthorized
			}
			return nil
		},
		Audit: &deviceOAuthTestAudit{}, Sessions: deviceOAuthTestSessions{}, Next: base,
		DefaultTenantID: "global", AllowRequestOrigin: true,
	})
	if err != nil {
		t.Fatalf("create device delivery handler: %v", err)
	}
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

	verifier := strings.Repeat("v", 48)
	digest := sha256.Sum256([]byte(verifier))
	authorization := url.Values{
		"response_type":         {"code"},
		"client_id":             {deviceidentity.DesktopClientID},
		"redirect_uri":          {deviceidentity.DesktopRedirectURI},
		"code_challenge":        {base64.RawURLEncoding.EncodeToString(digest[:])},
		"code_challenge_method": {"S256"},
		"state":                 {"state_0123456789abcdef"},
	}
	authorizeURL := server.URL + "/oauth/device/authorize?" + authorization.Encode()

	unauthenticated, err := client.Get(authorizeURL)
	if err != nil {
		t.Fatalf("open device authorization: %v", err)
	}
	_ = unauthenticated.Body.Close()
	if unauthenticated.StatusCode != http.StatusFound {
		t.Fatalf("unauthenticated authorization status = %d", unauthenticated.StatusCode)
	}
	loginURL, err := url.Parse(unauthenticated.Header.Get("Location"))
	if err != nil || loginURL.Path != "/login" {
		t.Fatalf("login redirect = %q, err = %v", unauthenticated.Header.Get("Location"), err)
	}
	returnTo := loginURL.Query().Get("return_to")
	if returnTo != "/oauth/device/authorize?"+authorization.Encode() {
		t.Fatalf("login return_to = %q", returnTo)
	}

	challengeResponse := postDeviceOAuthJSON(t, client, server.URL+"/api/auth/login/begin", server.URL, map[string]string{"username": "superadmin"}, nil)
	if challengeResponse.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(challengeResponse.Body)
		_ = challengeResponse.Body.Close()
		t.Fatalf("login begin status = %d body = %s", challengeResponse.StatusCode, body)
	}
	var challenge userauth.LoginChallengeDTO
	if err := json.NewDecoder(challengeResponse.Body).Decode(&challenge); err != nil {
		t.Fatalf("decode login challenge: %v", err)
	}
	_ = challengeResponse.Body.Close()

	completeResponse := postDeviceOAuthJSON(t, client, server.URL+"/api/auth/login/complete", server.URL, map[string]string{
		"username": "superadmin", "challenge_id": challenge.ChallengeID, "password": userauth.SuperadminPassword(),
	}, nil)
	if completeResponse.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(completeResponse.Body)
		_ = completeResponse.Body.Close()
		t.Fatalf("login complete status = %d body = %s", completeResponse.StatusCode, body)
	}
	_ = completeResponse.Body.Close()
	var sessionCookie *http.Cookie
	for _, cookie := range completeResponse.Cookies() {
		if cookie.Name == authAccessTokenCookieName && cookie.Path == "/" && cookie.Value != "" {
			sessionCookie = cookie
			break
		}
	}
	if sessionCookie == nil {
		t.Fatalf("login did not issue a root-scoped browser session cookie: %+v", completeResponse.Cookies())
	}

	authorizationRequest, _ := http.NewRequest(http.MethodGet, server.URL+returnTo, nil)
	authorizationRequest.AddCookie(sessionCookie)
	authorizationPage, err := client.Do(authorizationRequest)
	if err != nil {
		t.Fatalf("return to authorization page: %v", err)
	}
	pageBody, _ := io.ReadAll(authorizationPage.Body)
	_ = authorizationPage.Body.Close()
	if authorizationPage.StatusCode != http.StatusOK || !bytes.Contains(pageBody, []byte("确认连接")) {
		t.Fatalf("authorization page status = %d body = %s", authorizationPage.StatusCode, pageBody)
	}

	decision := url.Values{}
	for key, values := range authorization {
		decision[key] = append([]string(nil), values...)
	}
	decision.Set("tenant_id", "global")
	decision.Set("decision", "approve")
	decisionRequest, _ := http.NewRequest(http.MethodPost, server.URL+"/oauth/device/authorize", strings.NewReader(decision.Encode()))
	decisionRequest.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	decisionRequest.Header.Set("Origin", server.URL)
	decisionRequest.AddCookie(sessionCookie)
	decisionResponse, err := client.Do(decisionRequest)
	if err != nil {
		t.Fatalf("approve device authorization: %v", err)
	}
	_ = decisionResponse.Body.Close()
	callback, err := url.Parse(decisionResponse.Header.Get("Location"))
	if decisionResponse.StatusCode != http.StatusFound || err != nil || callback.Scheme != "orrery" || callback.Query().Get("code") == "" || callback.Query().Get("state") != authorization.Get("state") {
		t.Fatalf("device callback status = %d location = %q err = %v", decisionResponse.StatusCode, decisionResponse.Header.Get("Location"), err)
	}

	tokenResponse := postDeviceOAuthJSON(t, client, server.URL+"/oauth/device/token", "", map[string]string{
		"grant_type": "authorization_code", "client_id": deviceidentity.DesktopClientID,
		"code": callback.Query().Get("code"), "code_verifier": verifier, "redirect_uri": deviceidentity.DesktopRedirectURI,
	}, nil)
	defer tokenResponse.Body.Close()
	if tokenResponse.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(tokenResponse.Body)
		t.Fatalf("token exchange status = %d body = %s", tokenResponse.StatusCode, body)
	}
	var token struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(tokenResponse.Body).Decode(&token); err != nil || token.AccessToken == "" {
		t.Fatalf("token exchange response = %+v err = %v", token, err)
	}
}

func postDeviceOAuthJSON(t *testing.T, client *http.Client, endpoint, origin string, body any, cookie *http.Cookie) *http.Response {
	t.Helper()
	payload, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	request, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("create request: %v", err)
	}
	request.Header.Set("Content-Type", "application/json")
	if origin != "" {
		request.Header.Set("Origin", origin)
	}
	if cookie != nil {
		request.AddCookie(cookie)
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("post %s: %v", endpoint, err)
	}
	return response
}

type deviceOAuthTestAudit struct{}

func (*deviceOAuthTestAudit) RecordDeviceAudit(context.Context, delivery.AuditEvent) error {
	return nil
}

type deviceOAuthTestSessions struct{}

func (deviceOAuthTestSessions) RunDeviceSession(context.Context, *websocket.Conn, deviceidentity.ConnectionIdentity, string) error {
	return nil
}
