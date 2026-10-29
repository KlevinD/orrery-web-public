package delivery

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/websocket"

	devicedirectory "orrery_v2/internal/device/directory"
	deviceidentity "orrery_v2/internal/device/identity"
)

const (
	maxJSONBodyBytes = 64 * 1024
	maxFormBodyBytes = 32 * 1024
)

type IdentityService interface {
	Authorize(context.Context, deviceidentity.BrowserPrincipal, deviceidentity.AuthorizationRequest) (deviceidentity.AuthorizationGrant, error)
	ExchangeToken(context.Context, deviceidentity.TokenRequest) (deviceidentity.Token, error)
	RegisterDevice(context.Context, string, deviceidentity.DeviceRegistration) (deviceidentity.Device, error)
	RevokeDevice(context.Context, string, string) error
	AuthenticateAccessToken(context.Context, string) (deviceidentity.ConnectionIdentity, error)
	IssueChallenge(context.Context, string, string, int64) (deviceidentity.Challenge, error)
	AuthenticateConnection(context.Context, deviceidentity.ConnectionRequest) (deviceidentity.ConnectionIdentity, error)
}

type BrowserAuthenticator func(context.Context, *http.Request) (deviceidentity.BrowserPrincipal, error)
type TenantAuthorizer func(context.Context, deviceidentity.BrowserPrincipal, string) error

type AuditEvent struct {
	TenantID   string
	ActorID    string
	DeviceID   string
	Action     string
	Result     string
	OccurredAt time.Time
}

type AuditRecorder interface {
	RecordDeviceAudit(context.Context, AuditEvent) error
}

type SessionRunner interface {
	RunDeviceSession(context.Context, *websocket.Conn, deviceidentity.ConnectionIdentity, string) error
}

type AgentDirectory interface {
	PublishCatalog(context.Context, devicedirectory.OwnerScope, string, devicedirectory.PublishCatalogInput) error
}

type Options struct {
	Identity            IdentityService
	AuthenticateBrowser BrowserAuthenticator
	AuthorizeTenant     TenantAuthorizer
	Audit               AuditRecorder
	Sessions            SessionRunner
	AgentDirectory      AgentDirectory
	Artifacts           ArtifactUploadService
	Next                http.Handler
	DefaultTenantID     string
	PublicOrigin        string
	AllowRequestOrigin  bool
	Now                 func() time.Time
}

type Handler struct {
	identity            IdentityService
	authenticateBrowser BrowserAuthenticator
	authorizeTenant     TenantAuthorizer
	audit               AuditRecorder
	sessions            SessionRunner
	agentDirectory      AgentDirectory
	artifacts           ArtifactUploadService
	next                http.Handler
	defaultTenantID     string
	publicOrigin        *url.URL
	allowRequestOrigin  bool
	now                 func() time.Time
	upgrader            websocket.Upgrader
}

func NewHandler(options Options) (*Handler, error) {
	if options.Identity == nil || options.AuthenticateBrowser == nil || options.AuthorizeTenant == nil || options.Audit == nil || options.Sessions == nil || options.Next == nil {
		return nil, fmt.Errorf("device delivery requires identity, browser authentication, tenant authorization, audit, sessions, and next handler")
	}
	defaultTenantID := strings.TrimSpace(options.DefaultTenantID)
	if defaultTenantID == "" {
		return nil, fmt.Errorf("device delivery default tenant is required")
	}
	var publicOrigin *url.URL
	if strings.TrimSpace(options.PublicOrigin) != "" {
		parsed, err := parsePublicOrigin(options.PublicOrigin)
		if err != nil {
			return nil, err
		}
		publicOrigin = parsed
	}
	if publicOrigin == nil && !options.AllowRequestOrigin {
		return nil, fmt.Errorf("device delivery public origin is required")
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	handler := &Handler{
		identity: options.Identity, authenticateBrowser: options.AuthenticateBrowser, authorizeTenant: options.AuthorizeTenant,
		audit: options.Audit, sessions: options.Sessions, agentDirectory: options.AgentDirectory, next: options.Next, defaultTenantID: defaultTenantID,
		artifacts:    options.Artifacts,
		publicOrigin: publicOrigin, allowRequestOrigin: options.AllowRequestOrigin, now: options.Now,
	}
	handler.upgrader = websocket.Upgrader{
		HandshakeTimeout: 10 * time.Second,
		ReadBufferSize:   4096,
		WriteBufferSize:  4096,
		CheckOrigin:      handler.websocketOriginAllowed,
	}
	return handler, nil
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, request *http.Request) {
	if request == nil || request.URL == nil {
		h.next.ServeHTTP(w, request)
		return
	}
	path := request.URL.Path
	switch {
	case path == "/oauth/device/authorize" && request.Method == http.MethodGet:
		h.handleAuthorizationPage(w, request)
	case path == "/oauth/device/authorize" && request.Method == http.MethodPost:
		h.handleAuthorizationDecision(w, request)
	case path == "/oauth/device/token" && request.Method == http.MethodPost:
		h.handleToken(w, request)
	case path == "/api/device/v1/devices" && request.Method == http.MethodPost:
		h.handleRegisterDevice(w, request)
	case devicePathID(path, "/revoke") != "" && request.Method == http.MethodPost:
		h.handleRevokeDevice(w, request, devicePathID(path, "/revoke"))
	case devicePathID(path, "/connection-challenges") != "" && request.Method == http.MethodPost:
		h.handleConnectionChallenge(w, request, devicePathID(path, "/connection-challenges"))
	case devicePathID(path, "/agent-profiles") != "" && request.Method == http.MethodPut:
		h.handlePublishAgentProfiles(w, request, devicePathID(path, "/agent-profiles"))
	case path == "/api/device/v1/artifact-uploads" && request.Method == http.MethodPost:
		h.handleAuthorizeArtifactUpload(w, request)
	case artifactUploadPathToken(path) != "" && request.Method == http.MethodPut:
		h.handleArtifactUpload(w, request, artifactUploadPathToken(path))
	case path == "/api/devices/v1/connect" && request.Method == http.MethodGet:
		h.handleConnect(w, request)
	default:
		h.next.ServeHTTP(w, request)
	}
}

func (h *Handler) handlePublishAgentProfiles(w http.ResponseWriter, request *http.Request, deviceID string) {
	if h.agentDirectory == nil {
		writeError(w, http.StatusServiceUnavailable, "agent_directory_unavailable")
		return
	}
	accessToken, ok := bearerToken(request, "Bearer")
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	identity, err := h.identity.AuthenticateAccessToken(request.Context(), accessToken)
	if err != nil {
		h.writeIdentityError(w, err)
		return
	}
	if identity.DeviceID != deviceID {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	var input devicedirectory.PublishCatalogInput
	if err := decodeJSON(w, request, &input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	err = h.agentDirectory.PublishCatalog(request.Context(), devicedirectory.OwnerScope{
		TenantID: identity.TenantID,
		OwnerID:  identity.OwnerID,
	}, deviceID, input)
	if err != nil {
		h.writeAgentDirectoryError(w, err)
		return
	}
	h.recordAudit(request.Context(), AuditEvent{
		TenantID: identity.TenantID, ActorID: identity.OwnerID, DeviceID: identity.DeviceID,
		Action: "device.agent_profiles.published", Result: "succeeded", OccurredAt: h.now().UTC(),
	})
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) handleAuthorizeArtifactUpload(w http.ResponseWriter, request *http.Request) {
	if h.artifacts == nil {
		writeError(w, http.StatusServiceUnavailable, "artifact_upload_unavailable")
		return
	}
	accessToken, ok := bearerToken(request, "Bearer")
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	identity, err := h.identity.AuthenticateAccessToken(request.Context(), accessToken)
	if err != nil {
		h.writeIdentityError(w, err)
		return
	}
	var manifest ArtifactManifest
	if err := decodeJSON(w, request, &manifest); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	ticket, err := h.artifacts.AuthorizeArtifactUpload(request.Context(), identity, manifest)
	if err != nil {
		h.writeArtifactError(w, err)
		return
	}
	origin, err := h.originForRequest(request)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "server_configuration_error")
		return
	}
	uploadURL := *origin
	uploadURL.Path = "/api/device/v1/artifact-uploads/" + url.PathEscape(ticket.Ticket)
	h.recordAudit(request.Context(), AuditEvent{
		TenantID: identity.TenantID, ActorID: identity.OwnerID, DeviceID: identity.DeviceID,
		Action: "device.artifact_upload.authorized", Result: "succeeded", OccurredAt: h.now().UTC(),
	})
	writeJSON(w, http.StatusCreated, map[string]any{
		"url": uploadURL.String(), "expires_at": ticket.ExpiresAt.UTC().Format(time.RFC3339Nano),
	})
}

func (h *Handler) handleArtifactUpload(w http.ResponseWriter, request *http.Request, ticket string) {
	if h.artifacts == nil {
		writeError(w, http.StatusServiceUnavailable, "artifact_upload_unavailable")
		return
	}
	if err := h.artifacts.UploadArtifact(request.Context(), ticket, request.Header.Get("Content-Type"), request.ContentLength, request.Body); err != nil {
		h.writeArtifactError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) handleAuthorizationPage(w http.ResponseWriter, request *http.Request) {
	authorization := authorizationRequestFromValues(request.URL.Query())
	if err := deviceidentity.ValidateAuthorizationRequest(authorization); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	principal, err := h.authenticateBrowser(request.Context(), request)
	if errors.Is(err, deviceidentity.ErrUnauthorized) || strings.TrimSpace(principal.UserID) == "" {
		h.redirectAuthorizationLogin(w, request, authorization)
		return
	}
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "authentication_unavailable")
		return
	}
	tenantID := strings.TrimSpace(principal.TenantID)
	if tenantID == "" {
		tenantID = h.defaultTenantID
	}
	if err := h.authorizeTenant(request.Context(), principal, tenantID); err != nil {
		writeError(w, http.StatusForbidden, "tenant_forbidden")
		return
	}
	setBrowserSecurityHeaders(w)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_ = authorizationPage.Execute(w, authorizationPageData{
		Owner: firstNonBlank(principal.DisplayName, principal.UserID), TenantID: tenantID,
		ResponseType: authorization.ResponseType, ClientID: authorization.ClientID, RedirectURI: authorization.RedirectURI,
		CodeChallenge: authorization.CodeChallenge, CodeChallengeMethod: authorization.CodeChallengeMethod, State: authorization.State,
	})
}

func (h *Handler) redirectAuthorizationLogin(w http.ResponseWriter, request *http.Request, authorization deviceidentity.AuthorizationRequest) {
	origin, err := h.originForRequest(request)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "server_configuration_error")
		return
	}
	returnQuery := url.Values{
		"response_type":         {authorization.ResponseType},
		"client_id":             {authorization.ClientID},
		"redirect_uri":          {authorization.RedirectURI},
		"code_challenge":        {authorization.CodeChallenge},
		"code_challenge_method": {authorization.CodeChallengeMethod},
		"state":                 {authorization.State},
	}
	target := *origin
	target.Path = "/login"
	target.RawQuery = url.Values{
		"return_to": {"/oauth/device/authorize?" + returnQuery.Encode()},
	}.Encode()
	setBrowserSecurityHeaders(w)
	http.Redirect(w, request, target.String(), http.StatusFound)
}

func (h *Handler) handleAuthorizationDecision(w http.ResponseWriter, request *http.Request) {
	principal, ok := h.browserPrincipal(w, request)
	if !ok {
		return
	}
	if !h.sameOriginMutation(request) {
		writeError(w, http.StatusForbidden, "origin_rejected")
		return
	}
	request.Body = http.MaxBytesReader(w, request.Body, maxFormBodyBytes)
	if err := request.ParseForm(); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	authorization := authorizationRequestFromValues(request.PostForm)
	if err := deviceidentity.ValidateAuthorizationRequest(authorization); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	tenantID := strings.TrimSpace(request.PostForm.Get("tenant_id"))
	if tenantID == "" {
		tenantID = h.defaultTenantID
	}
	if err := h.authorizeTenant(request.Context(), principal, tenantID); err != nil {
		writeError(w, http.StatusForbidden, "tenant_forbidden")
		return
	}
	if request.PostForm.Get("decision") != "approve" {
		redirectAuthorizationResult(w, request, authorization.RedirectURI, authorization.State, "", "access_denied")
		return
	}
	principal.TenantID = tenantID
	grant, err := h.identity.Authorize(request.Context(), principal, authorization)
	if err != nil {
		h.writeIdentityError(w, err)
		return
	}
	h.recordAudit(request.Context(), AuditEvent{TenantID: tenantID, ActorID: principal.UserID, Action: "device.pairing.authorized", Result: "succeeded", OccurredAt: h.now().UTC()})
	redirectAuthorizationResult(w, request, grant.RedirectURI, grant.State, grant.Code, "")
}

func (h *Handler) handleToken(w http.ResponseWriter, request *http.Request) {
	var body struct {
		GrantType         string `json:"grant_type"`
		ClientID          string `json:"client_id"`
		Code              string `json:"code"`
		CodeVerifier      string `json:"code_verifier"`
		RedirectURI       string `json:"redirect_uri"`
		DeviceID          string `json:"device_id"`
		RefreshCredential string `json:"refresh_credential"`
	}
	if err := decodeJSON(w, request, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	token, err := h.identity.ExchangeToken(request.Context(), deviceidentity.TokenRequest{
		GrantType: body.GrantType, ClientID: body.ClientID, Code: body.Code, CodeVerifier: body.CodeVerifier,
		RedirectURI: body.RedirectURI, DeviceID: body.DeviceID, RefreshCredential: body.RefreshCredential,
	})
	if err != nil {
		h.writeIdentityError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"access_token": token.AccessToken, "refresh_credential": token.RefreshCredential, "expires_in": token.ExpiresIn})
}

func (h *Handler) handleRegisterDevice(w http.ResponseWriter, request *http.Request) {
	accessToken, ok := bearerToken(request, "Bearer")
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	var body struct {
		DeviceName    string                      `json:"device_name"`
		Platform      string                      `json:"platform"`
		Architecture  string                      `json:"architecture"`
		ClientVersion string                      `json:"client_version"`
		ProtocolMin   string                      `json:"protocol_min"`
		ProtocolMax   string                      `json:"protocol_max"`
		PublicKey     deviceidentity.PublicKeyJWK `json:"public_key"`
	}
	if err := decodeJSON(w, request, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	device, err := h.identity.RegisterDevice(request.Context(), accessToken, deviceidentity.DeviceRegistration{
		DeviceName: body.DeviceName, Platform: body.Platform, Architecture: body.Architecture, ClientVersion: body.ClientVersion,
		ProtocolMin: body.ProtocolMin, ProtocolMax: body.ProtocolMax, PublicKey: body.PublicKey,
	})
	if err != nil {
		h.writeIdentityError(w, err)
		return
	}
	h.recordAudit(request.Context(), AuditEvent{TenantID: device.TenantID, ActorID: device.OwnerID, DeviceID: device.DeviceID, Action: "device.registered", Result: "succeeded", OccurredAt: h.now().UTC()})
	writeJSON(w, http.StatusCreated, map[string]any{
		"device_id": device.DeviceID, "tenant_id": device.TenantID, "credential_revision": device.CredentialRevision,
		"allowed_capabilities": device.AllowedCapabilities,
	})
}

func (h *Handler) handleRevokeDevice(w http.ResponseWriter, request *http.Request, deviceID string) {
	var body struct {
		RefreshCredential string `json:"refresh_credential"`
	}
	if err := decodeJSON(w, request, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	if err := h.identity.RevokeDevice(request.Context(), deviceID, body.RefreshCredential); err != nil {
		h.writeIdentityError(w, err)
		return
	}
	if revoker, ok := h.sessions.(interface{ RevokeDeviceConnection(string) }); ok {
		revoker.RevokeDeviceConnection(deviceID)
	}
	h.recordAudit(request.Context(), AuditEvent{ActorID: "device:" + deviceID, DeviceID: deviceID, Action: "device.revoked", Result: "succeeded", OccurredAt: h.now().UTC()})
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) handleConnectionChallenge(w http.ResponseWriter, request *http.Request, deviceID string) {
	accessToken, ok := bearerToken(request, "Bearer")
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	var body struct {
		CredentialRevision int64 `json:"credential_revision"`
	}
	if err := decodeJSON(w, request, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	challenge, err := h.identity.IssueChallenge(request.Context(), accessToken, deviceID, body.CredentialRevision)
	if err != nil {
		h.writeIdentityError(w, err)
		return
	}
	gatewayURL, err := h.gatewayURL(request)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "server_configuration_error")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"nonce": challenge.Nonce, "gateway_url": gatewayURL, "expires_at": challenge.ExpiresAt.UTC().Format(time.RFC3339Nano), "browser_protocol": "1"})
}

func (h *Handler) handleConnect(w http.ResponseWriter, request *http.Request) {
	accessToken, ok := bearerToken(request, "DPoP")
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	deviceID := strings.TrimSpace(request.Header.Get("Orrery-Device-Id"))
	epoch := strings.TrimSpace(request.Header.Get("Orrery-Connection-Epoch"))
	revision, err := strconv.ParseInt(strings.TrimSpace(request.Header.Get("Orrery-Credential-Revision")), 10, 64)
	if err != nil || !validConnectionID(epoch) {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	target, err := h.dpopTarget(request)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	identity, err := h.identity.AuthenticateConnection(request.Context(), deviceidentity.ConnectionRequest{
		AccessToken: accessToken, DeviceID: deviceID, CredentialRevision: revision,
		Proof: request.Header.Get("DPoP"), Method: http.MethodGet, TargetURI: target,
	})
	if err != nil {
		h.writeIdentityError(w, err)
		return
	}
	connection, err := h.upgrader.Upgrade(w, request, nil)
	if err != nil {
		return
	}
	defer connection.Close()
	connection.SetReadLimit(1024 * 1024)
	h.recordAudit(request.Context(), AuditEvent{TenantID: identity.TenantID, ActorID: identity.OwnerID, DeviceID: identity.DeviceID, Action: "device.connected", Result: "succeeded", OccurredAt: h.now().UTC()})
	_ = h.sessions.RunDeviceSession(request.Context(), connection, identity, epoch)
}

func (h *Handler) browserPrincipal(w http.ResponseWriter, request *http.Request) (deviceidentity.BrowserPrincipal, bool) {
	principal, err := h.authenticateBrowser(request.Context(), request)
	if err != nil || strings.TrimSpace(principal.UserID) == "" {
		writeError(w, http.StatusUnauthorized, "login_required")
		return deviceidentity.BrowserPrincipal{}, false
	}
	return principal, true
}

func (h *Handler) gatewayURL(request *http.Request) (string, error) {
	origin, err := h.originForRequest(request)
	if err != nil {
		return "", err
	}
	copy := *origin
	if copy.Scheme == "https" {
		copy.Scheme = "wss"
	} else {
		copy.Scheme = "ws"
	}
	copy.Path = "/api/devices/v1/connect"
	return copy.String(), nil
}

func (h *Handler) dpopTarget(request *http.Request) (string, error) {
	origin, err := h.originForRequest(request)
	if err != nil {
		return "", err
	}
	copy := *origin
	copy.Path = "/api/devices/v1/connect"
	return copy.String(), nil
}

func (h *Handler) originForRequest(request *http.Request) (*url.URL, error) {
	if h.publicOrigin != nil {
		copy := *h.publicOrigin
		return &copy, nil
	}
	if !h.allowRequestOrigin || request == nil {
		return nil, errors.New("request origin is unavailable")
	}
	scheme := "http"
	if request.TLS != nil {
		scheme = "https"
	}
	return parsePublicOrigin(scheme + "://" + request.Host)
}

func (h *Handler) websocketOriginAllowed(request *http.Request) bool {
	originHeader := strings.TrimSpace(request.Header.Get("Origin"))
	if originHeader == "" {
		return true
	}
	expected, err := h.originForRequest(request)
	if err != nil {
		return false
	}
	actual, err := parsePublicOrigin(originHeader)
	return err == nil && strings.EqualFold(actual.Scheme, expected.Scheme) && strings.EqualFold(actual.Host, expected.Host)
}

func (h *Handler) sameOriginMutation(request *http.Request) bool {
	originHeader := strings.TrimSpace(request.Header.Get("Origin"))
	if originHeader == "" {
		return false
	}
	expected, err := h.originForRequest(request)
	if err != nil {
		return false
	}
	actual, err := parsePublicOrigin(originHeader)
	return err == nil && strings.EqualFold(actual.Scheme, expected.Scheme) && strings.EqualFold(actual.Host, expected.Host)
}

func (h *Handler) writeIdentityError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, deviceidentity.ErrInvalidRequest):
		writeError(w, http.StatusBadRequest, "invalid_request")
	case errors.Is(err, deviceidentity.ErrUnauthorized):
		writeError(w, http.StatusUnauthorized, "unauthorized")
	case errors.Is(err, deviceidentity.ErrConflict):
		writeError(w, http.StatusConflict, "conflict")
	default:
		writeError(w, http.StatusInternalServerError, "internal_error")
	}
}

func (h *Handler) writeArtifactError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrArtifactInvalidRequest):
		writeError(w, http.StatusBadRequest, "invalid_request")
	case errors.Is(err, ErrArtifactUnauthorized):
		writeError(w, http.StatusForbidden, "artifact_upload_forbidden")
	case errors.Is(err, ErrArtifactExpired):
		writeError(w, http.StatusGone, "artifact_upload_expired")
	case errors.Is(err, ErrArtifactUnavailable):
		writeError(w, http.StatusServiceUnavailable, "artifact_upload_unavailable")
	default:
		writeError(w, http.StatusInternalServerError, "internal_error")
	}
}

func (h *Handler) writeAgentDirectoryError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, devicedirectory.ErrInvalidRequest):
		writeError(w, http.StatusBadRequest, "invalid_request")
	case errors.Is(err, devicedirectory.ErrNotFound):
		writeError(w, http.StatusForbidden, "device_forbidden")
	case errors.Is(err, devicedirectory.ErrDependency):
		writeError(w, http.StatusServiceUnavailable, "agent_directory_unavailable")
	default:
		writeError(w, http.StatusInternalServerError, "internal_error")
	}
}

func (h *Handler) recordAudit(ctx context.Context, event AuditEvent) {
	_ = h.audit.RecordDeviceAudit(ctx, event)
}

func authorizationRequestFromValues(values url.Values) deviceidentity.AuthorizationRequest {
	return deviceidentity.AuthorizationRequest{
		ResponseType: values.Get("response_type"), ClientID: values.Get("client_id"), RedirectURI: values.Get("redirect_uri"),
		CodeChallenge: values.Get("code_challenge"), CodeChallengeMethod: values.Get("code_challenge_method"), State: values.Get("state"),
	}
}

func devicePathID(path, suffix string) string {
	const prefix = "/api/device/v1/devices/"
	if !strings.HasPrefix(path, prefix) || !strings.HasSuffix(path, suffix) {
		return ""
	}
	raw := strings.TrimSuffix(strings.TrimPrefix(path, prefix), suffix)
	if raw == "" || strings.Contains(raw, "/") {
		return ""
	}
	decoded, err := url.PathUnescape(raw)
	if err != nil || decoded == "" || strings.Contains(decoded, "/") {
		return ""
	}
	return decoded
}

func artifactUploadPathToken(path string) string {
	const prefix = "/api/device/v1/artifact-uploads/"
	if !strings.HasPrefix(path, prefix) {
		return ""
	}
	raw := strings.TrimPrefix(path, prefix)
	if raw == "" || strings.Contains(raw, "/") {
		return ""
	}
	decoded, err := url.PathUnescape(raw)
	if err != nil || len(decoded) < 16 || len(decoded) > 512 || strings.Contains(decoded, "/") {
		return ""
	}
	return decoded
}

func bearerToken(request *http.Request, scheme string) (string, bool) {
	header := strings.TrimSpace(request.Header.Get("Authorization"))
	prefix := scheme + " "
	if len(header) <= len(prefix) || !strings.EqualFold(header[:len(prefix)], prefix) {
		return "", false
	}
	token := strings.TrimSpace(header[len(prefix):])
	return token, token != ""
}

func decodeJSON(w http.ResponseWriter, request *http.Request, target any) error {
	if request.Header.Get("Content-Type") != "application/json" && !strings.HasPrefix(request.Header.Get("Content-Type"), "application/json;") {
		return errors.New("content type must be application/json")
	}
	request.Body = http.MaxBytesReader(w, request.Body, maxJSONBodyBytes)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("trailing JSON value")
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, code string) {
	writeJSON(w, status, map[string]string{"error": code})
}

func redirectAuthorizationResult(w http.ResponseWriter, request *http.Request, redirectURI, state, code, errorCode string) {
	target, err := url.Parse(redirectURI)
	if err != nil || target.String() != deviceidentity.DesktopRedirectURI {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	query := target.Query()
	query.Set("state", state)
	if code != "" {
		query.Set("code", code)
	}
	if errorCode != "" {
		query.Set("error", errorCode)
	}
	target.RawQuery = query.Encode()
	http.Redirect(w, request, target.String(), http.StatusFound)
}

func parsePublicOrigin(raw string) (*url.URL, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Hostname() == "" || parsed.User != nil || parsed.Path != "" || parsed.RawPath != "" || parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" || parsed.Opaque != "" {
		return nil, fmt.Errorf("device delivery public origin is invalid")
	}
	if parsed.Scheme != "https" && !(parsed.Scheme == "http" && isLoopbackHost(parsed.Hostname())) {
		return nil, fmt.Errorf("device delivery public origin must use HTTPS")
	}
	return parsed, nil
}

func isLoopbackHost(host string) bool {
	host = strings.Trim(strings.ToLower(strings.TrimSpace(host)), "[]")
	if host == "localhost" {
		return true
	}
	address := net.ParseIP(host)
	return address != nil && address.IsLoopback()
}

func validConnectionID(value string) bool {
	if len(value) < 3 || len(value) > 160 {
		return false
	}
	for index, char := range value {
		if index == 0 && !((char >= 'A' && char <= 'Z') || (char >= 'a' && char <= 'z')) {
			return false
		}
		if !((char >= 'A' && char <= 'Z') || (char >= 'a' && char <= 'z') || (char >= '0' && char <= '9') || char == '.' || char == '_' || char == '-') {
			return false
		}
	}
	return true
}

func setBrowserSecurityHeaders(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; form-action 'self'; base-uri 'none'; frame-ancestors 'none'")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "DENY")
}

func firstNonBlank(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

type authorizationPageData struct {
	Owner, TenantID, ResponseType, ClientID, RedirectURI, CodeChallenge, CodeChallengeMethod, State string
}

var authorizationPage = template.Must(template.New("device-authorization").Parse(`<!doctype html>
<html lang="zh-CN"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>连接 Orrery Desktop</title><style>
body{margin:0;background:#f4f5f7;color:#1f2933;font:14px system-ui,sans-serif}main{max-width:520px;margin:10vh auto;padding:0 24px}
h1{font-size:24px;margin:0 0 10px}p{line-height:1.6;color:#52606d}.panel{background:#fff;border:1px solid #d9e2ec;border-radius:8px;padding:22px;margin:22px 0}
dl{display:grid;grid-template-columns:100px 1fr;gap:10px;margin:0}dt{color:#7b8794}dd{margin:0;overflow-wrap:anywhere}.actions{display:flex;gap:10px;margin-top:22px}
button{border:1px solid #bcccdc;background:#fff;border-radius:6px;padding:9px 15px;font-weight:600;cursor:pointer}.primary{background:#1769aa;border-color:#1769aa;color:#fff}
</style></head><body><main><h1>连接 Orrery Desktop</h1><p>确认后，此桌面客户端将绑定到当前账号和租户。设备只能接收 Orrery Device Protocol 允许的任务。</p>
<div class="panel"><dl><dt>账号</dt><dd>{{.Owner}}</dd><dt>租户</dt><dd>{{.TenantID}}</dd><dt>客户端</dt><dd>Orrery Desktop</dd></dl>
<form method="post" action="/oauth/device/authorize"><input type="hidden" name="response_type" value="{{.ResponseType}}"><input type="hidden" name="client_id" value="{{.ClientID}}">
<input type="hidden" name="redirect_uri" value="{{.RedirectURI}}"><input type="hidden" name="code_challenge" value="{{.CodeChallenge}}"><input type="hidden" name="code_challenge_method" value="{{.CodeChallengeMethod}}">
<input type="hidden" name="state" value="{{.State}}"><input type="hidden" name="tenant_id" value="{{.TenantID}}"><div class="actions"><button class="primary" name="decision" value="approve">确认连接</button><button name="decision" value="deny">取消</button></div></form></div>
</main></body></html>`))
