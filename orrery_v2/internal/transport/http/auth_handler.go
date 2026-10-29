package httptransport

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/identity"
	"orrery_v2/internal/app/userauth"
)

const (
	authAccessTokenCookieName     = "orrery_access_token"
	maxAuthRequestBytes           = 16 << 10
	identityLogoutProviderTimeout = 10 * time.Second
)

func (r *Router) handleAuthRoute(w http.ResponseWriter, req *http.Request) bool {
	path := strings.Trim(req.URL.Path, "/")
	parts := strings.Split(path, "/")
	if authRouteRequiresHTTPS(parts) && r.rejectInsecureAuth(w, req) {
		return true
	}
	if len(parts) == 4 && parts[0] == "api" && parts[1] == "auth" && parts[2] == "password" && parts[3] == "change" {
		if req.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, ErrorResponse{Code: ErrorInvalidRequest, Message: "method not allowed", CorrelationID: correlationID(req)})
			return true
		}
		if !cookieMutationOriginAllowed(req) {
			status, body := MapError(userauth.ErrUnauthorized, correlationID(req))
			writeJSON(w, status, body)
			return true
		}
		r.handleChangePassword(w, req)
		return true
	}
	if len(parts) == 4 && parts[0] == "api" && parts[1] == "auth" && parts[2] == "session" && parts[3] == "rotate" {
		if req.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, ErrorResponse{Code: ErrorInvalidRequest, Message: "method not allowed", CorrelationID: correlationID(req)})
			return true
		}
		if !cookieMutationOriginAllowed(req) {
			status, body := MapError(userauth.ErrUnauthorized, correlationID(req))
			writeJSON(w, status, body)
			return true
		}
		r.handleRotateSession(w, req)
		return true
	}
	if len(parts) == 3 && parts[0] == "api" && parts[1] == "auth" && parts[2] == "sessions" {
		if req.Method != http.MethodGet {
			writeJSON(w, http.StatusMethodNotAllowed, ErrorResponse{Code: ErrorInvalidRequest, Message: "method not allowed", CorrelationID: correlationID(req)})
			return true
		}
		r.handleListSessions(w, req)
		return true
	}
	if len(parts) == 4 && parts[0] == "api" && parts[1] == "auth" && parts[2] == "sessions" {
		if req.Method != http.MethodDelete {
			writeJSON(w, http.StatusMethodNotAllowed, ErrorResponse{Code: ErrorInvalidRequest, Message: "method not allowed", CorrelationID: correlationID(req)})
			return true
		}
		if !cookieMutationOriginAllowed(req) {
			status, body := MapError(userauth.ErrUnauthorized, correlationID(req))
			writeJSON(w, status, body)
			return true
		}
		r.handleRevokeSessionByID(w, req, parts[3])
		return true
	}
	if len(parts) == 3 && parts[0] == "api" && parts[1] == "auth" && parts[2] == "session" {
		if req.Method != http.MethodGet {
			writeJSON(w, http.StatusMethodNotAllowed, ErrorResponse{Code: ErrorInvalidRequest, Message: "method not allowed", CorrelationID: correlationID(req)})
			return true
		}
		r.handleCurrentSession(w, req)
		return true
	}
	if len(parts) == 3 && parts[0] == "api" && parts[1] == "auth" && parts[2] == "providers" {
		if req.Method != http.MethodGet {
			writeJSON(w, http.StatusMethodNotAllowed, ErrorResponse{Code: ErrorInvalidRequest, Message: "method not allowed", CorrelationID: correlationID(req)})
			return true
		}
		r.handleAuthProviders(w, req)
		return true
	}
	if len(parts) == 5 && parts[0] == "api" && parts[1] == "auth" && parts[2] == "identity" {
		switch parts[4] {
		case "begin":
			if req.Method != http.MethodGet {
				writeJSON(w, http.StatusMethodNotAllowed, ErrorResponse{Code: ErrorInvalidRequest, Message: "method not allowed", CorrelationID: correlationID(req)})
				return true
			}
			r.handleBeginIdentityLogin(w, req, parts[3])
			return true
		case "callback":
			if req.Method != http.MethodGet {
				writeJSON(w, http.StatusMethodNotAllowed, ErrorResponse{Code: ErrorInvalidRequest, Message: "method not allowed", CorrelationID: correlationID(req)})
				return true
			}
			r.handleCompleteIdentityLogin(w, req, parts[3])
			return true
		case "login-flow":
			if req.Method != http.MethodGet {
				writeJSON(w, http.StatusMethodNotAllowed, ErrorResponse{Code: ErrorInvalidRequest, Message: "method not allowed", CorrelationID: correlationID(req)})
				return true
			}
			r.handleGetIdentityLoginFlow(w, req, parts[3])
			return true
		case "logout":
			if req.Method != http.MethodGet {
				writeJSON(w, http.StatusMethodNotAllowed, ErrorResponse{Code: ErrorInvalidRequest, Message: "method not allowed", CorrelationID: correlationID(req)})
				return true
			}
			r.handleBeginIdentityLogout(w, req, parts[3])
			return true
		}
	}
	if len(parts) == 3 && parts[0] == "api" && parts[1] == "auth" && parts[2] == "logout" {
		if r == nil || r.auth == nil {
			status, body := MapError(userauth.ErrServiceDependency, correlationID(req))
			writeJSON(w, status, body)
			return true
		}
		if req.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, ErrorResponse{Code: ErrorInvalidRequest, Message: "method not allowed", CorrelationID: correlationID(req)})
			return true
		}
		if authCredentialFromRequest(req).source == authCredentialCookie && !cookieMutationOriginAllowed(req) {
			status, body := MapError(userauth.ErrUnauthorized, correlationID(req))
			writeJSON(w, status, body)
			return true
		}
		r.handleLogout(w, req)
		return true
	}
	if len(parts) != 4 || parts[0] != "api" || parts[1] != "auth" || parts[2] != "login" {
		return false
	}
	if r != nil && r.identityAuth != nil && !r.identityAuth.LocalLoginEnabled(req.Context()) {
		writeJSON(w, http.StatusForbidden, ErrorResponse{Code: ErrorUnauthorized, Message: "local login is disabled", CorrelationID: correlationID(req)})
		return true
	}
	if r == nil || r.auth == nil {
		status, body := MapError(userauth.ErrServiceDependency, correlationID(req))
		writeJSON(w, status, body)
		return true
	}
	if req.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, ErrorResponse{Code: ErrorInvalidRequest, Message: "method not allowed", CorrelationID: correlationID(req)})
		return true
	}
	if !browserRequestOriginAllowed(req, false) {
		status, body := MapError(userauth.ErrUnauthorized, correlationID(req))
		writeJSON(w, status, body)
		return true
	}
	switch parts[3] {
	case "begin":
		r.handleBeginLogin(w, req)
		return true
	case "complete":
		r.handleCompleteLogin(w, req)
		return true
	default:
		return false
	}
}

func authRouteRequiresHTTPS(parts []string) bool {
	if len(parts) < 3 || parts[0] != "api" || parts[1] != "auth" {
		return false
	}
	switch parts[2] {
	case "identity", "login", "logout", "password", "session", "sessions":
		return true
	default:
		return false
	}
}

func (r *Router) handleCurrentSession(w http.ResponseWriter, req *http.Request) {
	if r == nil || r.auth == nil {
		status, body := MapError(userauth.ErrServiceDependency, correlationID(req))
		writeJSON(w, status, body)
		return
	}
	credential := authCredentialFromRequest(req)
	if credential.source == authCredentialBearer && credential.token != "" && !isAppSessionToken(credential.token) {
		r.handleDesktopCurrentSession(w, req, credential.token)
		return
	}
	if credential.token == "" || (credential.source == authCredentialBearer && !isAppSessionToken(credential.token)) {
		status, body := MapError(userauth.ErrUnauthorized, correlationID(req))
		writeJSON(w, status, body)
		return
	}
	session, err := r.auth.AuthenticateToken(req.Context(), credential.token)
	if err != nil {
		status, body := MapError(err, correlationID(req))
		writeJSON(w, status, body)
		return
	}
	setRequestAuditActor(req, auditActorFromPrincipal(userauth.PrincipalFromSession(session)))
	session.AccessToken = ""
	writeJSON(w, http.StatusOK, session)
}

func (r *Router) rejectInsecureAuth(w http.ResponseWriter, req *http.Request) bool {
	if r == nil || !r.authRequireHTTPS || requestIsHTTPS(req) {
		return false
	}
	writeJSON(w, http.StatusUpgradeRequired, ErrorResponse{
		Code:          ErrorHTTPSRequired,
		Message:       "HTTPS is required for authentication",
		CorrelationID: correlationID(req),
	})
	return true
}

type authProviderResponse struct {
	Providers []authProviderDTO `json:"providers"`
}

type authProviderDTO struct {
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`
	Kind        string `json:"kind"`
	Enabled     bool   `json:"enabled"`
	Primary     bool   `json:"primary"`
	BeginURL    string `json:"begin_url,omitempty"`
}

func (r *Router) handleAuthProviders(w http.ResponseWriter, req *http.Request) {
	providers := []identity.ProviderInfo{{Name: identity.ProviderLocal, DisplayName: "Local", Kind: "local", Enabled: r != nil && r.auth != nil, Primary: true}}
	if r != nil && r.identityAuth != nil {
		providers = r.identityAuth.Providers(req.Context())
	}
	out := make([]authProviderDTO, 0, len(providers))
	for _, provider := range providers {
		provider.Name = strings.TrimSpace(provider.Name)
		if provider.Name == "" || !provider.Enabled {
			continue
		}
		dto := authProviderDTO{
			Name:        provider.Name,
			DisplayName: firstNonEmpty(provider.DisplayName, provider.Name),
			Kind:        provider.Kind,
			Enabled:     provider.Enabled,
			Primary:     provider.Primary,
		}
		if provider.Kind == "browser" {
			dto.BeginURL = "/api/auth/identity/" + url.PathEscape(provider.Name) + "/begin"
		}
		out = append(out, dto)
	}
	writeJSON(w, http.StatusOK, authProviderResponse{Providers: out})
}

func (r *Router) handleBeginIdentityLogin(w http.ResponseWriter, req *http.Request, provider string) {
	if r == nil || r.identityAuth == nil {
		status, body := MapError(userauth.ErrServiceDependency, correlationID(req))
		writeJSON(w, status, body)
		return
	}
	redirect, err := r.identityAuth.BeginBrowserLogin(req.Context(), provider, identity.BeginLoginRequest{ReturnTo: safeAuthReturnTo(req.URL.Query().Get("return_to"))})
	if err != nil {
		status, response := MapError(identityAuthError(err), correlationID(req))
		writeJSON(w, status, response)
		return
	}
	setIdentityFlowCookie(w, req, provider, redirect)
	if acceptsJSON(req) {
		writeJSON(w, http.StatusOK, redirect)
		return
	}
	http.Redirect(w, req, redirect.URL, http.StatusSeeOther)
}

func (r *Router) handleBeginIdentityLogout(w http.ResponseWriter, req *http.Request, provider string) {
	if r == nil || r.identityAuth == nil {
		status, body := MapError(userauth.ErrServiceDependency, correlationID(req))
		writeJSON(w, status, body)
		return
	}
	logoutCtx, cancel := context.WithTimeout(context.WithoutCancel(req.Context()), identityLogoutProviderTimeout)
	defer cancel()
	redirect, err := r.identityAuth.BeginBrowserLogout(logoutCtx, provider, identity.BeginLogoutRequest{
		ReturnTo: safeAuthLogoutReturnTo(req.URL.Query().Get("return_to")),
		Cookie:   req.Header.Get("Cookie"),
	})
	if err != nil {
		status, response := MapError(identityAuthError(err), correlationID(req))
		writeJSON(w, status, response)
		return
	}
	http.Redirect(w, req, redirect.URL, http.StatusSeeOther)
}

func (r *Router) handleGetIdentityLoginFlow(w http.ResponseWriter, req *http.Request, provider string) {
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Add("Vary", "Cookie")
	if r == nil || r.identityAuth == nil {
		status, body := MapError(userauth.ErrServiceDependency, correlationID(req))
		writeJSON(w, status, body)
		return
	}
	flow, err := r.identityAuth.GetBrowserLoginFlow(req.Context(), provider, identity.LoginFlowRequest{
		ID:     req.URL.Query().Get("id"),
		Cookie: req.Header.Get("Cookie"),
	})
	if err != nil {
		status, response := MapError(identityAuthError(err), correlationID(req))
		writeJSON(w, status, response)
		return
	}
	writeJSON(w, http.StatusOK, flow)
}

func (r *Router) handleCompleteIdentityLogin(w http.ResponseWriter, req *http.Request, provider string) {
	if r == nil || r.identityAuth == nil {
		status, body := MapError(userauth.ErrServiceDependency, correlationID(req))
		writeJSON(w, status, body)
		return
	}
	session, err := r.identityAuth.CompleteBrowserLogin(req.Context(), provider, identity.CompleteLoginRequest{
		RequestURL: requestAbsoluteURL(req),
		Code:       req.URL.Query().Get("code"),
		State:      req.URL.Query().Get("state"),
		Cookie:     req.Header.Get("Cookie"),
	})
	clearIdentityFlowCookie(w, req, provider)
	if err != nil {
		if strings.EqualFold(strings.TrimSpace(provider), identity.ProviderKratos) && errors.Is(err, identity.ErrBrowserSessionUnavailable) {
			http.Redirect(w, req, "/login", http.StatusSeeOther)
			return
		}
		status, response := MapError(identityAuthError(err), correlationID(req))
		writeJSON(w, status, response)
		return
	}
	setRequestAuditActor(req, auditActorFromPrincipal(userauth.PrincipalFromSession(session)))
	setAuthSessionCookie(w, req, session)
	returnTo := safeAuthReturnTo(req.URL.Query().Get("return_to"))
	if redirector, ok := r.identityAuth.(interface{ CallbackReturnTo(string, string) string }); ok {
		if providerReturnTo := redirector.CallbackReturnTo(provider, req.URL.Query().Get("state")); providerReturnTo != "/" {
			returnTo = safeAuthReturnTo(providerReturnTo)
		}
	}
	http.Redirect(w, req, returnTo, http.StatusSeeOther)
}

func setIdentityFlowCookie(w http.ResponseWriter, req *http.Request, provider string, redirect identity.LoginRedirect) {
	if strings.TrimSpace(redirect.FlowState) == "" {
		return
	}
	expiresAt := redirect.ExpiresAt
	if expiresAt.IsZero() {
		expiresAt = time.Now().UTC().Add(10 * time.Minute)
	}
	http.SetCookie(w, &http.Cookie{
		Name:     identity.BrowserFlowCookieName(provider),
		Value:    redirect.FlowState,
		Path:     "/api/",
		HttpOnly: true,
		Secure:   requestIsHTTPS(req),
		SameSite: http.SameSiteLaxMode,
		Expires:  expiresAt,
		MaxAge:   int(time.Until(expiresAt).Seconds()),
	})
}

func clearIdentityFlowCookie(w http.ResponseWriter, req *http.Request, provider string) {
	if req == nil {
		return
	}
	if _, err := req.Cookie(identity.BrowserFlowCookieName(provider)); err != nil {
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     identity.BrowserFlowCookieName(provider),
		Value:    "",
		Path:     "/api/",
		HttpOnly: true,
		Secure:   requestIsHTTPS(req),
		SameSite: http.SameSiteLaxMode,
		Expires:  time.Unix(1, 0),
		MaxAge:   -1,
	})
}

func (r *Router) handleLogout(w http.ResponseWriter, req *http.Request) {
	credential := authCredentialFromRequest(req)
	if credential.token != "" {
		if principal, err := r.authenticateCredential(req.Context(), credential); err == nil {
			setRequestAuditActor(req, auditActorFromPrincipal(principal))
		}
	}
	clearAuthSessionCookie(w, req)
	if credential.token != "" && r.shouldRevokeCredential(credential) {
		if err := r.auth.RevokeSession(req.Context(), credential.token); err != nil {
			status, response := MapError(err, correlationID(req))
			writeJSON(w, status, response)
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

func (r *Router) handleBeginLogin(w http.ResponseWriter, req *http.Request) {
	cid := correlationID(req)
	var body userauth.BeginLoginRequest
	if err := decodeAuthRequest(w, req, &body); err != nil {
		status, response := invalidRequest(cid, "body", "request body is invalid")
		writeJSON(w, status, response)
		return
	}
	setRequestAuditActor(req, httpAuditActor{ActorID: body.Username, ViewerID: body.Username, ViewerKind: "login_attempt"})
	body.IPAddress = clientIPFromRequest(req)
	challenge, err := r.auth.BeginLogin(req.Context(), body)
	if err != nil {
		status, response := MapError(err, cid)
		writeJSON(w, status, response)
		return
	}
	writeJSON(w, http.StatusOK, challenge)
}

func (r *Router) handleCompleteLogin(w http.ResponseWriter, req *http.Request) {
	cid := correlationID(req)
	var body userauth.CompleteLoginRequest
	if err := decodeAuthRequest(w, req, &body); err != nil {
		status, response := invalidRequest(cid, "body", "request body is invalid")
		writeJSON(w, status, response)
		return
	}
	setRequestAuditActor(req, httpAuditActor{ActorID: body.Username, ViewerID: body.Username, ViewerKind: "login_attempt"})
	body.IPAddress = clientIPFromRequest(req)
	body.UserAgent = req.UserAgent()
	session, err := r.auth.CompleteLogin(req.Context(), body)
	if err != nil {
		status, response := MapError(err, cid)
		writeJSON(w, status, response)
		return
	}
	setRequestAuditActor(req, auditActorFromPrincipal(userauth.PrincipalFromSession(session)))
	setAuthSessionCookie(w, req, session)
	session.AccessToken = ""
	writeJSON(w, http.StatusOK, session)
}

type passwordSessionAuthService interface {
	ChangePassword(ctx context.Context, accessToken string, newPassword string, ipAddress string, userAgent string) (userauth.SessionDTO, error)
	RotateSession(ctx context.Context, accessToken string, ipAddress string, userAgent string) (userauth.SessionDTO, error)
	ListSessions(ctx context.Context, accessToken string) (userauth.SessionPageDTO, error)
	RevokeSessionByID(ctx context.Context, accessToken string, sessionID string) error
}

func (r *Router) passwordSessionService() (passwordSessionAuthService, error) {
	if r == nil || r.auth == nil {
		return nil, userauth.ErrServiceDependency
	}
	service, ok := r.auth.(passwordSessionAuthService)
	if !ok {
		return nil, userauth.ErrServiceDependency
	}
	return service, nil
}

func (r *Router) handleChangePassword(w http.ResponseWriter, req *http.Request) {
	service, err := r.passwordSessionService()
	if err != nil {
		status, body := MapError(err, correlationID(req))
		writeJSON(w, status, body)
		return
	}
	credential := authCredentialFromRequest(req)
	if credential.token == "" || credential.source == authCredentialBearer && !isAppSessionToken(credential.token) {
		status, body := MapError(userauth.ErrUnauthorized, correlationID(req))
		writeJSON(w, status, body)
		return
	}
	var body userauth.ChangePasswordRequest
	if err := decodeAuthRequest(w, req, &body); err != nil {
		status, response := invalidRequest(correlationID(req), "body", "request body is invalid")
		writeJSON(w, status, response)
		return
	}
	session, err := service.ChangePassword(req.Context(), credential.token, body.NewPassword, clientIPFromRequest(req), req.UserAgent())
	if err != nil {
		status, response := MapError(err, correlationID(req))
		writeJSON(w, status, response)
		return
	}
	setAuthSessionCookie(w, req, session)
	session.AccessToken = ""
	writeJSON(w, http.StatusOK, session)
}

func (r *Router) handleRotateSession(w http.ResponseWriter, req *http.Request) {
	service, err := r.passwordSessionService()
	if err != nil {
		status, body := MapError(err, correlationID(req))
		writeJSON(w, status, body)
		return
	}
	credential := authCredentialFromRequest(req)
	if credential.token == "" || credential.source == authCredentialBearer && !isAppSessionToken(credential.token) {
		status, body := MapError(userauth.ErrUnauthorized, correlationID(req))
		writeJSON(w, status, body)
		return
	}
	session, err := service.RotateSession(req.Context(), credential.token, clientIPFromRequest(req), req.UserAgent())
	if err != nil {
		status, response := MapError(err, correlationID(req))
		writeJSON(w, status, response)
		return
	}
	setAuthSessionCookie(w, req, session)
	session.AccessToken = ""
	writeJSON(w, http.StatusOK, session)
}

func (r *Router) handleListSessions(w http.ResponseWriter, req *http.Request) {
	service, err := r.passwordSessionService()
	if err != nil {
		status, body := MapError(err, correlationID(req))
		writeJSON(w, status, body)
		return
	}
	credential := authCredentialFromRequest(req)
	if credential.token == "" || credential.source == authCredentialBearer && !isAppSessionToken(credential.token) {
		status, body := MapError(userauth.ErrUnauthorized, correlationID(req))
		writeJSON(w, status, body)
		return
	}
	page, err := service.ListSessions(req.Context(), credential.token)
	if err != nil {
		status, response := MapError(err, correlationID(req))
		writeJSON(w, status, response)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

func (r *Router) handleRevokeSessionByID(w http.ResponseWriter, req *http.Request, sessionID string) {
	service, err := r.passwordSessionService()
	if err != nil {
		status, body := MapError(err, correlationID(req))
		writeJSON(w, status, body)
		return
	}
	credential := authCredentialFromRequest(req)
	if credential.token == "" || credential.source == authCredentialBearer && !isAppSessionToken(credential.token) {
		status, body := MapError(userauth.ErrUnauthorized, correlationID(req))
		writeJSON(w, status, body)
		return
	}
	if err := service.RevokeSessionByID(req.Context(), credential.token, sessionID); err != nil {
		status, response := MapError(err, correlationID(req))
		writeJSON(w, status, response)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func decodeAuthRequest(w http.ResponseWriter, req *http.Request, target any) error {
	if req == nil || req.Body == nil || target == nil {
		return userauth.ErrInvalidRequest
	}
	req.Body = http.MaxBytesReader(w, req.Body, maxAuthRequestBytes)
	decoder := json.NewDecoder(req.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return userauth.ErrInvalidRequest
	}
	return nil
}

func (r *Router) applyAuthenticatedViewer(w http.ResponseWriter, req *http.Request) bool {
	if internalViewerAuthorizationMatches(req) {
		viewer, err := viewerFromRequestHeaders(req)
		if err != nil {
			status, response := MapError(userauth.ErrUnauthorized, correlationID(req))
			writeJSON(w, status, response)
			return false
		}
		principal := principalForRouteAuthorization(req, viewer)
		principal.AgentProfileID = strings.TrimSpace(req.Header.Get("X-Agent-Profile-ID"))
		principal.TenantID = strings.TrimSpace(firstNonEmpty(req.Header.Get("X-Tenant-ID"), req.Header.Get("X-Tenant")))
		ctx := identity.ContextWithPrincipal(req.Context(), principal)
		ctx = identity.ContextWithTrustedInternalService(ctx)
		*req = *req.WithContext(ctx)
		setRequestAuditActor(req, auditActorFromTrustedViewerHeaders(req))
		return true
	}
	credential := authCredentialFromRequest(req)
	if credential.token == "" {
		if r != nil && (r.allowsAnonymousRequest(req) || !isAPIRoute(req)) {
			return true
		}
		status, response := MapError(userauth.ErrUnauthorized, correlationID(req))
		writeJSON(w, status, response)
		return false
	}
	if r == nil {
		status, response := MapError(userauth.ErrUnauthorized, correlationID(req))
		writeJSON(w, status, response)
		return false
	}
	principal, err := r.authenticateCredential(req.Context(), credential)
	if err != nil {
		status, response := MapError(err, correlationID(req))
		writeJSON(w, status, response)
		return false
	}
	*req = *req.WithContext(identity.ContextWithPrincipal(req.Context(), principal))
	applyPrincipalViewer(req, principal)
	r.applyPrincipalOrganizationScope(req, principal)
	return true
}

func (r *Router) applyPrincipalOrganizationScope(req *http.Request, principal identity.Principal) {
	if r == nil || r.agentProfiles == nil || req == nil {
		return
	}
	principal = identity.NormalizePrincipal(principal)
	profileID := strings.TrimSpace(principal.AgentProfileID)
	if profileID == "" || agentcore.IsReservedNonWorkforceProfileID(profileID) {
		return
	}
	profile, err := r.agentProfiles.GetProfile(req.Context(), agentcore.AgentProfileID(profileID))
	if err != nil || !profile.Enabled || profile.Placement.Status == agentcore.TalentPlacementMarket {
		return
	}
	if organizationID := strings.TrimSpace(string(profile.OrgIdentity.OrganizationRef.ID)); organizationID != "" {
		req.Header.Set("X-Organization-ID", organizationID)
		if principal.TenantID == "" {
			principal.TenantID = organizationID
			req.Header.Set("X-Tenant-ID", organizationID)
			ctx := identity.ContextWithPrincipal(req.Context(), principal)
			ctx = contextWithDerivedOrganizationTenant(ctx, organizationID)
			*req = *req.WithContext(ctx)
		}
	}
	if departmentID := strings.TrimSpace(string(profile.OrgIdentity.DepartmentRef.ID)); departmentID != "" {
		req.Header.Set("X-Department-ID", departmentID)
	}
}

func (r *Router) allowsAnonymousRequest(req *http.Request) bool {
	if req == nil || req.URL == nil {
		return false
	}
	path := strings.Trim(req.URL.Path, "/")
	parts := strings.Split(path, "/")
	return req.Method == http.MethodGet && len(parts) == 2 && parts[0] == "api" && parts[1] == "health"
}

type authCredentialSource string

const (
	authCredentialNone   authCredentialSource = ""
	authCredentialBearer authCredentialSource = "bearer"
	authCredentialCookie authCredentialSource = "cookie"
)

type authCredential struct {
	source authCredentialSource
	token  string
}

func (r *Router) authenticateCredential(ctx context.Context, credential authCredential) (identity.Principal, error) {
	token := strings.TrimSpace(credential.token)
	if token == "" {
		return identity.Principal{}, userauth.ErrUnauthorized
	}
	if credential.source == authCredentialBearer && r != nil && r.bearerAuth != nil && !isAppSessionToken(token) {
		return r.bearerAuth.AuthenticateBearer(ctx, token)
	}
	if r == nil || r.auth == nil {
		return identity.Principal{}, userauth.ErrUnauthorized
	}
	return r.auth.AuthenticatePrincipal(ctx, token)
}

func (r *Router) shouldRevokeCredential(credential authCredential) bool {
	if r == nil || r.auth == nil || strings.TrimSpace(credential.token) == "" {
		return false
	}
	if credential.source != authCredentialBearer {
		return true
	}
	return r.bearerAuth == nil || isAppSessionToken(credential.token)
}

func isAppSessionToken(token string) bool {
	return strings.HasPrefix(strings.TrimSpace(token), "iat-")
}

func authTokenFromRequest(req *http.Request) string {
	return authCredentialFromRequest(req).token
}

func authCredentialFromRequest(req *http.Request) authCredential {
	if req == nil {
		return authCredential{}
	}
	header := strings.TrimSpace(req.Header.Get("Authorization"))
	if strings.HasPrefix(strings.ToLower(header), "bearer ") {
		return authCredential{source: authCredentialBearer, token: strings.TrimSpace(header[7:])}
	}
	if cookie, err := req.Cookie(authAccessTokenCookieName); err == nil {
		if token := strings.TrimSpace(cookie.Value); token != "" {
			return authCredential{source: authCredentialCookie, token: token}
		}
	}
	return authCredential{}
}

func setAuthSessionCookie(w http.ResponseWriter, req *http.Request, session userauth.SessionDTO) {
	token := strings.TrimSpace(session.AccessToken)
	if token == "" {
		return
	}
	cookie := &http.Cookie{
		Name:     authAccessTokenCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   requestIsHTTPS(req),
	}
	if !session.ExpiresAt.IsZero() {
		cookie.Expires = session.ExpiresAt
		cookie.MaxAge = int(time.Until(session.ExpiresAt).Seconds())
		if cookie.MaxAge < 0 {
			cookie.MaxAge = 0
		}
	}
	http.SetCookie(w, cookie)
	expireAuthSessionCookie(w, req, "/api")
}

func clearAuthSessionCookie(w http.ResponseWriter, req *http.Request) {
	expireAuthSessionCookie(w, req, "/")
	expireAuthSessionCookie(w, req, "/api")
}

func expireAuthSessionCookie(w http.ResponseWriter, req *http.Request, path string) {
	http.SetCookie(w, &http.Cookie{
		Name:     authAccessTokenCookieName,
		Value:    "",
		Path:     path,
		MaxAge:   -1,
		Expires:  time.Unix(0, 0),
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   requestIsHTTPS(req),
	})
}

func requestIsHTTPS(req *http.Request) bool {
	if req == nil {
		return false
	}
	if req.TLS != nil {
		return true
	}
	return trustedForwardingProxy(req, cleanClientIP(req.RemoteAddr)) && strings.EqualFold(lastForwardedHeaderValue(req.Header.Get("X-Forwarded-Proto")), "https")
}

func requestAbsoluteURL(req *http.Request) string {
	if req == nil || req.URL == nil {
		return ""
	}
	scheme := "http"
	if requestIsHTTPS(req) {
		scheme = "https"
	}
	host := strings.TrimSpace(req.Host)
	if host == "" {
		host = strings.TrimSpace(req.URL.Host)
	}
	if host == "" {
		return req.URL.String()
	}
	copied := *req.URL
	copied.Scheme = scheme
	copied.Host = host
	return copied.String()
}

func safeAuthReturnTo(value string) string {
	value = strings.TrimSpace(value)
	if strings.HasPrefix(value, "/") && !strings.HasPrefix(value, "//") && !strings.Contains(value, "\\") {
		return value
	}
	return "/"
}

func safeAuthLogoutReturnTo(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "/login"
	}
	returnTo := safeAuthReturnTo(value)
	if returnTo == "/" && value != "/" {
		return "/login"
	}
	return returnTo
}

func acceptsJSON(req *http.Request) bool {
	if req == nil {
		return false
	}
	for _, value := range strings.Split(req.Header.Get("Accept"), ",") {
		if strings.Contains(strings.TrimSpace(value), "application/json") {
			return true
		}
	}
	return false
}

func identityAuthError(err error) error {
	switch {
	case errors.Is(err, identity.ErrInvalidRequest):
		return userauth.ErrInvalidRequest
	case errors.Is(err, identity.ErrUnauthorized):
		return userauth.ErrUnauthorized
	default:
		return err
	}
}

func internalViewerAuthorizationMatches(req *http.Request) bool {
	if req == nil {
		return false
	}
	security := deploymentSecurityFromRequest(req)
	if !security.trustedViewerHeaders {
		return false
	}
	if !internalViewerRequestAllowed(req) {
		return false
	}
	expected := security.internalViewerAuth
	if expected == "" {
		return false
	}
	actual := strings.TrimSpace(req.Header.Get("X-Orrery-Internal-Viewer-Authorization"))
	if actual == "" || len(actual) != len(expected) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(actual), []byte(expected)) == 1
}

func internalViewerRequestAllowed(req *http.Request) bool {
	if !isLoopbackRequest(req) {
		return false
	}
	for _, header := range []string{"Forwarded", "X-Forwarded-For", "X-Forwarded-Host", "X-Forwarded-Proto", "X-Real-IP"} {
		if strings.TrimSpace(req.Header.Get(header)) != "" {
			return false
		}
	}
	return true
}

func applySessionViewer(req *http.Request, session userauth.SessionDTO) {
	applyPrincipalViewer(req, userauth.PrincipalFromSession(session))
}

func applyPrincipalViewer(req *http.Request, principal identity.Principal) {
	if req == nil {
		return
	}
	principal = identity.NormalizePrincipal(principal)
	for _, header := range []string{
		"X-User-ID", "X-Agent-ID", "X-Agent-IDs", "X-Human-ID", "X-Human-IDs",
		"X-Organization-ID", "X-Organization-IDs", "X-Department-ID", "X-Department-IDs",
		"X-Project-ID", "X-Project-IDs", "X-Mission-ID", "X-Mission-IDs",
		"X-Discussion-ID", "X-Discussion-IDs", "X-Position-ID", "X-Position-IDs",
		"X-Role-ID", "X-Role-IDs", "X-Tenant-ID", "X-Tenant", "X-Mission-Roles",
		"X-Permission-Scope", "X-Viewer-Kind", "X-Agent-Profile-ID", "X-User-Display-Name",
	} {
		req.Header.Del(header)
	}
	displayName := strings.TrimSpace(principal.DisplayName)
	if displayName == "" {
		displayName = strings.TrimSpace(principal.Username)
	}
	req.Header.Set("X-User-ID", strings.TrimSpace(principal.Username))
	req.Header.Set("X-Viewer-Kind", strings.TrimSpace(principal.ViewerKind))
	req.Header.Set("X-Agent-Profile-ID", strings.TrimSpace(principal.AgentProfileID))
	req.Header.Set("X-User-Display-Name", url.QueryEscape(displayName))
	req.Header.Set("X-Permission-Scope", principal.PermissionScopeHeader())
	if principal.TenantID != "" {
		req.Header.Set("X-Tenant-ID", principal.TenantID)
	}
}

func userauthStatus(err error) (int, bool) {
	switch {
	case errors.Is(err, userauth.ErrPlaintextPassword), errors.Is(err, userauth.ErrInvalidRequest):
		return http.StatusBadRequest, true
	case errors.Is(err, userauth.ErrIPLocked), errors.Is(err, userauth.ErrUsernameLocked), errors.Is(err, userauth.ErrLoginThrottled):
		return http.StatusTooManyRequests, true
	case errors.Is(err, userauth.ErrPasswordChangeRequired), errors.Is(err, userauth.ErrMFARequired):
		return http.StatusPreconditionRequired, true
	case errors.Is(err, userauth.ErrInvalidCredentials), errors.Is(err, userauth.ErrUnauthorized), errors.Is(err, userauth.ErrAccountNotFound), errors.Is(err, userauth.ErrMFAInvalid):
		return http.StatusUnauthorized, true
	case errors.Is(err, userauth.ErrServiceDependency):
		return http.StatusServiceUnavailable, true
	default:
		return 0, false
	}
}

func clientIPFromRequest(req *http.Request) string {
	if req == nil {
		return ""
	}
	remoteIP := cleanClientIP(req.RemoteAddr)
	if !trustedForwardingProxy(req, remoteIP) {
		return remoteIP
	}
	if forwarded := strings.TrimSpace(req.Header.Get("X-Forwarded-For")); forwarded != "" {
		parts := strings.Split(forwarded, ",")
		trustedHop := ""
		for i := len(parts) - 1; i >= 0; i-- {
			ip := cleanClientIP(parts[i])
			if ip == "" {
				break
			}
			trustedHop = ip
			if !trustedForwardingProxy(req, ip) {
				return ip
			}
		}
		if trustedHop != "" {
			return trustedHop
		}
	}
	if realIP := cleanClientIP(req.Header.Get("X-Real-IP")); realIP != "" {
		return realIP
	}
	return remoteIP
}

func cleanClientIP(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if host, _, err := net.SplitHostPort(value); err == nil {
		value = host
	}
	if ip := net.ParseIP(value); ip != nil {
		return ip.String()
	}
	return ""
}

func trustedForwardingProxy(req *http.Request, remoteIP string) bool {
	ip := net.ParseIP(strings.TrimSpace(remoteIP))
	if ip == nil {
		return false
	}
	cidrs := deploymentSecurityFromRequest(req).trustedProxyCIDRs
	if len(cidrs) == 0 {
		cidrs = []string{"127.0.0.1/8", "::1/128"}
	}
	for _, cidr := range cidrs {
		_, network, err := net.ParseCIDR(cidr)
		if err == nil && network.Contains(ip) {
			return true
		}
	}
	return false
}

type deploymentSecurityContextKey struct{}

type deploymentSecurityConfig struct {
	trustedProxyCIDRs    []string
	trustedViewerHeaders bool
	internalViewerAuth   string
	runtimeMCPAuth       string
}

func withDeploymentSecurity(req *http.Request, security deploymentSecurityConfig) *http.Request {
	if req == nil {
		return nil
	}
	security.trustedProxyCIDRs = append([]string(nil), security.trustedProxyCIDRs...)
	return req.WithContext(context.WithValue(req.Context(), deploymentSecurityContextKey{}, security))
}

func deploymentSecurityFromRequest(req *http.Request) deploymentSecurityConfig {
	if req != nil {
		if security, ok := req.Context().Value(deploymentSecurityContextKey{}).(deploymentSecurityConfig); ok {
			security.trustedProxyCIDRs = append([]string(nil), security.trustedProxyCIDRs...)
			return security
		}
	}
	return deploymentSecurityConfig{trustedProxyCIDRs: []string{"127.0.0.1/8", "::1/128"}}
}
