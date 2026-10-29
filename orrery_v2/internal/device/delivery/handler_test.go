package delivery

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	devicedirectory "orrery_v2/internal/device/directory"
	deviceidentity "orrery_v2/internal/device/identity"
	"orrery_v2/internal/platform/db"
)

func TestHandlerCompletesDesktopPairingAndAuthenticatedWebSocketUpgrade(t *testing.T) {
	now := time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)
	identityService := deliveryTestIdentity(t, &now)
	runner := &recordingSessionRunner{called: make(chan struct{}, 1)}
	audit := &recordingAudit{}
	uploads := &recordingArtifactUploads{now: func() time.Time { return now }}
	handler, err := NewHandler(Options{
		Identity: identityService,
		AuthenticateBrowser: func(_ context.Context, request *http.Request) (deviceidentity.BrowserPrincipal, error) {
			cookie, err := request.Cookie("orrery_access_token")
			if err != nil || cookie.Value != "browser-session" {
				return deviceidentity.BrowserPrincipal{}, deviceidentity.ErrUnauthorized
			}
			return deviceidentity.BrowserPrincipal{UserID: "user-1", DisplayName: "Owner", TenantID: "global"}, nil
		},
		AuthorizeTenant: func(_ context.Context, principal deviceidentity.BrowserPrincipal, tenantID string) error {
			if principal.UserID != "user-1" || tenantID != "global" {
				return deviceidentity.ErrUnauthorized
			}
			return nil
		},
		Audit: audit, Sessions: runner, Artifacts: uploads, Next: http.NotFoundHandler(), DefaultTenantID: "global", AllowRequestOrigin: true,
		Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()

	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	publicJWK := deviceidentity.PublicKeyJWK{KTY: "OKP", CRV: "Ed25519", X: base64.RawURLEncoding.EncodeToString(public)}
	verifier := base64.RawURLEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef"))
	digest := sha256.Sum256([]byte(verifier))
	query := url.Values{
		"response_type": {"code"}, "client_id": {deviceidentity.DesktopClientID},
		"redirect_uri": {deviceidentity.DesktopRedirectURI}, "code_challenge": {base64.RawURLEncoding.EncodeToString(digest[:])},
		"code_challenge_method": {"S256"}, "state": {"state_0123456789abcdef"},
	}
	authorizeURL := server.URL + "/oauth/device/authorize?" + query.Encode()
	pageRequest, _ := http.NewRequest(http.MethodGet, authorizeURL, nil)
	pageRequest.AddCookie(&http.Cookie{Name: "orrery_access_token", Value: "browser-session"})
	pageResponse, err := http.DefaultClient.Do(pageRequest)
	if err != nil {
		t.Fatal(err)
	}
	pageBody, _ := io.ReadAll(pageResponse.Body)
	_ = pageResponse.Body.Close()
	if pageResponse.StatusCode != http.StatusOK || !bytes.Contains(pageBody, []byte("确认连接")) || pageResponse.Header.Get("Content-Security-Policy") == "" {
		t.Fatalf("authorization page status = %d, body = %s", pageResponse.StatusCode, pageBody)
	}

	form := query
	form.Set("tenant_id", "global")
	form.Set("decision", "approve")
	decisionRequest, _ := http.NewRequest(http.MethodPost, server.URL+"/oauth/device/authorize", strings.NewReader(form.Encode()))
	decisionRequest.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	decisionRequest.Header.Set("Origin", server.URL)
	decisionRequest.AddCookie(&http.Cookie{Name: "orrery_access_token", Value: "browser-session"})
	noRedirectClient := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	decisionResponse, err := noRedirectClient.Do(decisionRequest)
	if err != nil {
		t.Fatal(err)
	}
	_ = decisionResponse.Body.Close()
	if decisionResponse.StatusCode != http.StatusFound {
		t.Fatalf("authorization decision status = %d", decisionResponse.StatusCode)
	}
	callback, err := url.Parse(decisionResponse.Header.Get("Location"))
	if err != nil || callback.Scheme != "orrery" || callback.Query().Get("state") != query.Get("state") || callback.Query().Get("code") == "" {
		t.Fatalf("callback = %q, error = %v", decisionResponse.Header.Get("Location"), err)
	}

	tokenResponse := postJSON(t, server.URL+"/oauth/device/token", "", map[string]any{
		"grant_type": "authorization_code", "client_id": deviceidentity.DesktopClientID, "code": callback.Query().Get("code"),
		"code_verifier": verifier, "redirect_uri": deviceidentity.DesktopRedirectURI,
	})
	var token struct {
		AccessToken       string `json:"access_token"`
		RefreshCredential string `json:"refresh_credential"`
		ExpiresIn         int64  `json:"expires_in"`
	}
	decodeResponse(t, tokenResponse, http.StatusOK, &token)

	registrationResponse := postJSON(t, server.URL+"/api/device/v1/devices", "Bearer "+token.AccessToken, map[string]any{
		"device_name": "Desktop", "platform": "win32", "architecture": "x64", "client_version": "1.0.0",
		"protocol_min": "1.0", "protocol_max": "1.0", "public_key": publicJWK,
	})
	var registration struct {
		DeviceID           string   `json:"device_id"`
		TenantID           string   `json:"tenant_id"`
		CredentialRevision int64    `json:"credential_revision"`
		Capabilities       []string `json:"allowed_capabilities"`
	}
	decodeResponse(t, registrationResponse, http.StatusCreated, &registration)
	if registration.TenantID != "global" || registration.CredentialRevision != 1 || len(registration.Capabilities) != 7 {
		t.Fatalf("registration = %+v", registration)
	}

	refreshResponse := postJSON(t, server.URL+"/oauth/device/token", "", map[string]any{
		"grant_type": "refresh_token", "client_id": deviceidentity.DesktopClientID,
		"device_id": registration.DeviceID, "refresh_credential": token.RefreshCredential,
	})
	decodeResponse(t, refreshResponse, http.StatusOK, &token)
	artifactContent := []byte("completed")
	artifactDigest := sha256.Sum256(artifactContent)
	uploadResponse := postJSON(t, server.URL+"/api/device/v1/artifact-uploads", "Bearer "+token.AccessToken, map[string]any{
		"work_order_id": "work_order_001", "artifact_id": "artifact_001", "class": "summary",
		"display_name": "summary.md", "media_type": "text/markdown", "size_bytes": len(artifactContent),
		"sha256": fmt.Sprintf("%x", artifactDigest),
	})
	var upload struct {
		URL       string `json:"url"`
		ExpiresAt string `json:"expires_at"`
	}
	decodeResponse(t, uploadResponse, http.StatusCreated, &upload)
	uploadRequest, _ := http.NewRequest(http.MethodPut, upload.URL, bytes.NewReader(artifactContent))
	uploadRequest.Header.Set("Content-Type", "text/markdown")
	storedResponse, err := http.DefaultClient.Do(uploadRequest)
	if err != nil {
		t.Fatal(err)
	}
	_ = storedResponse.Body.Close()
	if storedResponse.StatusCode != http.StatusNoContent || string(uploads.content) != string(artifactContent) {
		t.Fatalf("artifact upload status = %d, content = %q", storedResponse.StatusCode, uploads.content)
	}
	challengeResponse := postJSON(t, server.URL+"/api/device/v1/devices/"+registration.DeviceID+"/connection-challenges", "Bearer "+token.AccessToken, map[string]any{"credential_revision": 1})
	var challenge struct {
		Nonce      string `json:"nonce"`
		GatewayURL string `json:"gateway_url"`
		ExpiresAt  string `json:"expires_at"`
	}
	decodeResponse(t, challengeResponse, http.StatusCreated, &challenge)
	if !strings.HasPrefix(challenge.GatewayURL, "ws://") || challenge.Nonce == "" {
		t.Fatalf("challenge = %+v", challenge)
	}
	proofTarget := strings.Replace(challenge.GatewayURL, "ws://", "http://", 1)
	proof := signDeliveryTestDPoP(t, private, publicJWK, token.AccessToken, challenge.Nonce, proofTarget, now)
	headers := http.Header{
		"Authorization":              {"DPoP " + token.AccessToken},
		"DPoP":                       {proof},
		"Orrery-Connection-Epoch":    {"conn_0123456789abcdef"},
		"Orrery-Credential-Revision": {"1"},
		"Orrery-Device-Id":           {registration.DeviceID},
	}
	connection, response, err := websocket.DefaultDialer.Dial(challenge.GatewayURL, headers)
	if err != nil {
		if response != nil {
			t.Fatalf("websocket dial status = %d, error = %v", response.StatusCode, err)
		}
		t.Fatal(err)
	}
	_ = connection.Close()
	select {
	case <-runner.called:
	case <-time.After(2 * time.Second):
		t.Fatal("device session runner was not called")
	}
	if runner.identity.DeviceID != registration.DeviceID || runner.identity.TenantID != "global" || runner.epoch != "conn_0123456789abcdef" {
		t.Fatalf("session runner identity = %+v, epoch = %s", runner.identity, runner.epoch)
	}
	if !audit.contains("device.pairing.authorized") || !audit.contains("device.registered") || !audit.contains("device.artifact_upload.authorized") || !audit.contains("device.connected") {
		t.Fatalf("audit events = %+v", audit.events)
	}
}

func TestHandlerMapsArtifactCapacityToServiceUnavailable(t *testing.T) {
	recorder := httptest.NewRecorder()
	(&Handler{}).writeArtifactError(recorder, ErrArtifactUnavailable)
	if recorder.Code != http.StatusServiceUnavailable || !strings.Contains(recorder.Body.String(), "artifact_upload_unavailable") {
		t.Fatalf("response status = %d body = %s", recorder.Code, recorder.Body.String())
	}
}

func TestHandlerPublishesAgentProfilesForAuthenticatedDeviceOnly(t *testing.T) {
	directory := &recordingAgentDirectory{}
	handler, err := NewHandler(Options{
		Identity: &stubIdentityService{}, AgentDirectory: directory,
		AuthenticateBrowser: func(context.Context, *http.Request) (deviceidentity.BrowserPrincipal, error) {
			return deviceidentity.BrowserPrincipal{}, deviceidentity.ErrUnauthorized
		},
		AuthorizeTenant: func(context.Context, deviceidentity.BrowserPrincipal, string) error { return nil },
		Audit:           &recordingAudit{}, Sessions: &recordingSessionRunner{}, Next: http.NotFoundHandler(),
		DefaultTenantID: "global", AllowRequestOrigin: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	body := `{"catalog_digest":"` + strings.Repeat("a", 64) + `","observed_at":"2026-08-06T04:00:00Z","profiles":[{"profile_id":"profile_openclaw","display_name":"My OpenClaw","runtime_kind":"openclaw","capabilities":["workspace.read"],"available":true,"workspace_grants":[{"grant_id":"grant_openclaw_001","display_name":"Product workspace","capabilities":["workspace.read"],"available":true}]}]}`
	req := httptest.NewRequest(http.MethodPut, "/api/device/v1/devices/device_test_001/agent-profiles", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer device-token")
	req.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("publish status = %d body = %s", recorder.Code, recorder.Body.String())
	}
	if directory.deviceID != "device_test_001" || directory.scope.TenantID != "global" || directory.scope.OwnerID != "user-1" || len(directory.input.Profiles) != 1 {
		t.Fatalf("published catalog = scope %+v device %q input %+v", directory.scope, directory.deviceID, directory.input)
	}

	mismatch := httptest.NewRequest(http.MethodPut, "/api/device/v1/devices/device_other_001/agent-profiles", strings.NewReader(body))
	mismatch.Header.Set("Authorization", "Bearer device-token")
	mismatch.Header.Set("Content-Type", "application/json")
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, mismatch)
	if recorder.Code != http.StatusUnauthorized || directory.calls != 1 {
		t.Fatalf("mismatched device status = %d calls = %d", recorder.Code, directory.calls)
	}
}

func TestHandlerRedirectsUnauthenticatedDeviceAuthorizationToLogin(t *testing.T) {
	handler, err := NewHandler(Options{
		Identity: &stubIdentityService{},
		AuthenticateBrowser: func(context.Context, *http.Request) (deviceidentity.BrowserPrincipal, error) {
			return deviceidentity.BrowserPrincipal{}, deviceidentity.ErrUnauthorized
		},
		AuthorizeTenant: func(context.Context, deviceidentity.BrowserPrincipal, string) error { return nil },
		Audit:           &recordingAudit{}, Sessions: &recordingSessionRunner{}, Next: http.NotFoundHandler(),
		DefaultTenantID: "global", PublicOrigin: "https://orrery.example.com",
	})
	if err != nil {
		t.Fatal(err)
	}
	query := url.Values{
		"response_type": {"code"}, "client_id": {deviceidentity.DesktopClientID},
		"redirect_uri": {deviceidentity.DesktopRedirectURI}, "code_challenge": {strings.Repeat("A", 43)},
		"code_challenge_method": {"S256"}, "state": {"state_0123456789abcdef"}, "ignored": {"value"},
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "http://api.internal/oauth/device/authorize?"+query.Encode(), nil))
	if recorder.Code != http.StatusFound || recorder.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("authorization redirect status = %d headers = %+v", recorder.Code, recorder.Header())
	}
	location, err := url.Parse(recorder.Header().Get("Location"))
	if err != nil || location.Scheme != "https" || location.Host != "orrery.example.com" || location.Path != "/login" {
		t.Fatalf("login redirect = %q, error = %v", recorder.Header().Get("Location"), err)
	}
	returnTo, err := url.Parse(location.Query().Get("return_to"))
	if err != nil || returnTo.Path != "/oauth/device/authorize" || returnTo.Query().Get("state") != query.Get("state") {
		t.Fatalf("login return_to = %q, error = %v", location.Query().Get("return_to"), err)
	}
	if returnTo.Query().Has("ignored") {
		t.Fatalf("login return_to preserved an unknown parameter: %q", returnTo.RawQuery)
	}
}

func TestPublicOriginRejectsNonOriginURLsAndAllowsLoopbackDevelopmentHTTP(t *testing.T) {
	for _, test := range []struct {
		value string
		valid bool
	}{
		{value: "https://orrery.example.com", valid: true},
		{value: "http://127.0.0.2:8080", valid: true},
		{value: "http://orrery.example.com", valid: false},
		{value: "https://orrery.example.com/", valid: false},
		{value: "https://orrery.example.com?", valid: false},
		{value: "https://user@orrery.example.com", valid: false},
	} {
		t.Run(test.value, func(t *testing.T) {
			_, err := parsePublicOrigin(test.value)
			if (err == nil) != test.valid {
				t.Fatalf("parsePublicOrigin(%q) error = %v, valid = %t", test.value, err, test.valid)
			}
		})
	}
}

func TestHandlerRejectsCrossOriginAuthorizationMutationAndDelegatesOtherRoutes(t *testing.T) {
	service := &stubIdentityService{}
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTeapot) })
	handler, err := NewHandler(Options{
		Identity: service,
		AuthenticateBrowser: func(context.Context, *http.Request) (deviceidentity.BrowserPrincipal, error) {
			return deviceidentity.BrowserPrincipal{UserID: "user-1", TenantID: "global"}, nil
		},
		AuthorizeTenant: func(context.Context, deviceidentity.BrowserPrincipal, string) error { return nil },
		Audit:           &recordingAudit{}, Sessions: &recordingSessionRunner{}, Next: next, DefaultTenantID: "global", AllowRequestOrigin: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "http://127.0.0.1/other", nil))
	if recorder.Code != http.StatusTeapot {
		t.Fatalf("delegated status = %d", recorder.Code)
	}
	form := url.Values{"response_type": {"code"}, "client_id": {deviceidentity.DesktopClientID}, "redirect_uri": {deviceidentity.DesktopRedirectURI}, "code_challenge": {strings.Repeat("A", 43)}, "code_challenge_method": {"S256"}, "state": {"state_0123456789abcdef"}, "tenant_id": {"global"}, "decision": {"approve"}}
	request := httptest.NewRequest(http.MethodPost, "http://127.0.0.1/oauth/device/authorize", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Origin", "https://attacker.example")
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusForbidden || service.authorizeCalls != 0 {
		t.Fatalf("cross-origin status = %d, authorize calls = %d", recorder.Code, service.authorizeCalls)
	}
}

func deliveryTestIdentity(t *testing.T, now *time.Time) *deviceidentity.Service {
	t.Helper()
	store, err := db.Open(context.Background(), db.Config{Driver: db.DriverMemory, Name: t.Name()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	persistence, err := deviceidentity.NewSQLStore(context.Background(), store)
	if err != nil {
		t.Fatal(err)
	}
	service, err := deviceidentity.NewService(deviceidentity.Options{Store: persistence, Now: func() time.Time { return *now }})
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func postJSON(t *testing.T, target, authorization string, body any) *http.Response {
	t.Helper()
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	request, _ := http.NewRequest(http.MethodPost, target, bytes.NewReader(encoded))
	request.Header.Set("Content-Type", "application/json")
	if authorization != "" {
		request.Header.Set("Authorization", authorization)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	return response
}

func decodeResponse(t *testing.T, response *http.Response, expected int, target any) {
	t.Helper()
	defer response.Body.Close()
	if response.StatusCode != expected {
		body, _ := io.ReadAll(response.Body)
		t.Fatalf("response status = %d, want %d, body = %s", response.StatusCode, expected, body)
	}
	if err := json.NewDecoder(response.Body).Decode(target); err != nil {
		t.Fatal(err)
	}
}

func signDeliveryTestDPoP(t *testing.T, private ed25519.PrivateKey, public deviceidentity.PublicKeyJWK, accessToken, nonce, target string, now time.Time) string {
	t.Helper()
	header, _ := json.Marshal(map[string]any{"typ": "dpop+jwt", "alg": "EdDSA", "jwk": public})
	digest := sha256.Sum256([]byte(accessToken))
	claims, _ := json.Marshal(map[string]any{"jti": "proof_0123456789abcdef", "htm": "GET", "htu": target, "iat": now.Unix(), "ath": base64.RawURLEncoding.EncodeToString(digest[:]), "nonce": nonce})
	left, right := base64.RawURLEncoding.EncodeToString(header), base64.RawURLEncoding.EncodeToString(claims)
	input := left + "." + right
	return fmt.Sprintf("%s.%s", input, base64.RawURLEncoding.EncodeToString(ed25519.Sign(private, []byte(input))))
}

type recordingSessionRunner struct {
	called   chan struct{}
	identity deviceidentity.ConnectionIdentity
	epoch    string
}

func (runner *recordingSessionRunner) RunDeviceSession(_ context.Context, _ *websocket.Conn, identity deviceidentity.ConnectionIdentity, epoch string) error {
	runner.identity, runner.epoch = identity, epoch
	if runner.called != nil {
		runner.called <- struct{}{}
	}
	return nil
}

type recordingAudit struct {
	mu     sync.Mutex
	events []AuditEvent
}

func (audit *recordingAudit) RecordDeviceAudit(_ context.Context, event AuditEvent) error {
	audit.mu.Lock()
	defer audit.mu.Unlock()
	audit.events = append(audit.events, event)
	return nil
}

func (audit *recordingAudit) contains(action string) bool {
	audit.mu.Lock()
	defer audit.mu.Unlock()
	for _, event := range audit.events {
		if event.Action == action {
			return true
		}
	}
	return false
}

type stubIdentityService struct {
	authorizeCalls int
}

type recordingAgentDirectory struct {
	scope    devicedirectory.OwnerScope
	deviceID string
	input    devicedirectory.PublishCatalogInput
	calls    int
}

func (directory *recordingAgentDirectory) PublishCatalog(_ context.Context, scope devicedirectory.OwnerScope, deviceID string, input devicedirectory.PublishCatalogInput) error {
	directory.scope, directory.deviceID, directory.input = scope, deviceID, input
	directory.calls++
	return nil
}

func (service *stubIdentityService) Authorize(context.Context, deviceidentity.BrowserPrincipal, deviceidentity.AuthorizationRequest) (deviceidentity.AuthorizationGrant, error) {
	service.authorizeCalls++
	return deviceidentity.AuthorizationGrant{}, nil
}
func (*stubIdentityService) ExchangeToken(context.Context, deviceidentity.TokenRequest) (deviceidentity.Token, error) {
	return deviceidentity.Token{}, nil
}
func (*stubIdentityService) RegisterDevice(context.Context, string, deviceidentity.DeviceRegistration) (deviceidentity.Device, error) {
	return deviceidentity.Device{}, nil
}
func (*stubIdentityService) RevokeDevice(context.Context, string, string) error { return nil }
func (*stubIdentityService) AuthenticateAccessToken(context.Context, string) (deviceidentity.ConnectionIdentity, error) {
	return deviceidentity.ConnectionIdentity{DeviceID: "device_test_001", TenantID: "global", OwnerID: "user-1", CredentialRevision: 1}, nil
}
func (*stubIdentityService) IssueChallenge(context.Context, string, string, int64) (deviceidentity.Challenge, error) {
	return deviceidentity.Challenge{}, nil
}

type recordingArtifactUploads struct {
	now      func() time.Time
	manifest ArtifactManifest
	identity deviceidentity.ConnectionIdentity
	content  []byte
}

func (uploads *recordingArtifactUploads) AuthorizeArtifactUpload(_ context.Context, identity deviceidentity.ConnectionIdentity, manifest ArtifactManifest) (ArtifactUploadTicket, error) {
	uploads.identity, uploads.manifest = identity, manifest
	return ArtifactUploadTicket{Ticket: "upload_ticket_0123456789", ExpiresAt: uploads.now().Add(5 * time.Minute)}, nil
}

func (uploads *recordingArtifactUploads) UploadArtifact(_ context.Context, ticket, contentType string, contentLength int64, reader io.Reader) error {
	if ticket != "upload_ticket_0123456789" || contentType != uploads.manifest.MediaType || contentLength != uploads.manifest.SizeBytes {
		return ErrArtifactInvalidRequest
	}
	content, err := io.ReadAll(reader)
	if err != nil {
		return err
	}
	uploads.content = content
	return nil
}
func (*stubIdentityService) AuthenticateConnection(context.Context, deviceidentity.ConnectionRequest) (deviceidentity.ConnectionIdentity, error) {
	return deviceidentity.ConnectionIdentity{}, nil
}
