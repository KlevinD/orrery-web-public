package httptransport

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"orrery_v2/internal/app/identity"
	"orrery_v2/internal/app/userauth"
)

func (r *Router) handleOAuth2Route(w http.ResponseWriter, req *http.Request) bool {
	if req == nil || req.URL == nil {
		return false
	}
	path := strings.Trim(req.URL.Path, "/")
	parts := strings.Split(path, "/")
	if len(parts) < 3 || parts[0] != "api" || parts[1] != "oauth2" {
		return false
	}
	if r.rejectInsecureAuth(w, req) {
		return true
	}
	switch parts[2] {
	case "logout":
		if len(parts) != 3 {
			return false
		}
		if req.Method != http.MethodGet {
			writeJSON(w, http.StatusMethodNotAllowed, ErrorResponse{Code: ErrorInvalidRequest, Message: "method not allowed", CorrelationID: correlationID(req)})
			return true
		}
		r.handleOAuth2BrowserLogout(w, req)
		return true
	case "login":
		if len(parts) != 3 {
			return false
		}
		if req.Method != http.MethodGet {
			writeJSON(w, http.StatusMethodNotAllowed, ErrorResponse{Code: ErrorInvalidRequest, Message: "method not allowed", CorrelationID: correlationID(req)})
			return true
		}
		r.handleOAuth2Login(w, req)
		return true
	case "marketing":
		if len(parts) != 4 || parts[3] != "callback" {
			return false
		}
		if req.Method != http.MethodGet {
			writeJSON(w, http.StatusMethodNotAllowed, ErrorResponse{Code: ErrorInvalidRequest, Message: "method not allowed", CorrelationID: correlationID(req)})
			return true
		}
		r.handleMarketingOAuth2Callback(w, req)
		return true
	case "consent":
		return r.handleOAuth2ConsentRoute(w, req, parts)
	default:
		return false
	}
}

var (
	errMarketingOAuth2CallbackConfig   = errors.New("marketing oauth2 callback relay is not configured")
	errMarketingOAuth2CallbackResponse = errors.New("marketing oauth2 callback response is invalid")
)

func (r *Router) handleMarketingOAuth2Callback(w http.ResponseWriter, req *http.Request) {
	redirectTo, err := marketingOAuth2CallbackRedirect(r.marketingOAuth2CallbackURL, req.URL.Query())
	if errors.Is(err, errMarketingOAuth2CallbackConfig) {
		status, response := MapError(userauth.ErrServiceDependency, correlationID(req))
		writeJSON(w, status, response)
		return
	}
	if err != nil {
		status, response := invalidRequest(correlationID(req), "oauth2_response", "oauth2 response is invalid")
		writeJSON(w, status, response)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	http.Redirect(w, req, redirectTo, http.StatusSeeOther)
}

func marketingOAuth2CallbackRedirect(rawTarget string, query url.Values) (string, error) {
	target, err := url.Parse(strings.TrimSpace(rawTarget))
	if err != nil || target.Scheme == "" || target.Host == "" || target.User != nil ||
		(target.Scheme != "http" && target.Scheme != "https") || target.RawQuery != "" || target.Fragment != "" {
		return "", errMarketingOAuth2CallbackConfig
	}
	if len(query["state"]) != 1 || strings.TrimSpace(query.Get("state")) == "" {
		return "", errMarketingOAuth2CallbackResponse
	}
	codeCount, errorCount := len(query["code"]), len(query["error"])
	if codeCount+errorCount != 1 || (codeCount == 1 && strings.TrimSpace(query.Get("code")) == "") ||
		(errorCount == 1 && strings.TrimSpace(query.Get("error")) == "") {
		return "", errMarketingOAuth2CallbackResponse
	}

	relay := url.Values{"state": {query.Get("state")}}
	for _, key := range []string{"code", "error", "error_description", "error_uri", "iss"} {
		values := query[key]
		if len(values) > 1 {
			return "", errMarketingOAuth2CallbackResponse
		}
		if len(values) == 1 && values[0] != "" {
			relay.Set(key, values[0])
		}
	}
	target.RawQuery = relay.Encode()
	return target.String(), nil
}

func (r *Router) handleOAuth2BrowserLogout(w http.ResponseWriter, req *http.Request) {
	if r == nil || r.auth == nil {
		status, response := MapError(userauth.ErrServiceDependency, correlationID(req))
		writeJSON(w, status, response)
		return
	}
	returnTo, ok := allowedOAuth2LogoutReturn(req.URL.Query().Get("return_to"), r.oauth2LogoutReturnOrigins)
	if !ok {
		status, response := invalidRequest(correlationID(req), "return_to", "return_to is not allowed")
		writeJSON(w, status, response)
		return
	}
	credential := authCredentialFromRequest(req)
	clearAuthSessionCookie(w, req)
	if credential.token != "" && r.shouldRevokeCredential(credential) {
		if err := r.auth.RevokeSession(req.Context(), credential.token); err != nil {
			status, response := MapError(err, correlationID(req))
			writeJSON(w, status, response)
			return
		}
	}
	http.Redirect(w, req, returnTo, http.StatusSeeOther)
}

func allowedOAuth2LogoutReturn(raw string, allowedOrigins []string) (string, bool) {
	returnTo, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || returnTo.Scheme == "" || returnTo.Host == "" || returnTo.User != nil || (returnTo.Scheme != "http" && returnTo.Scheme != "https") {
		return "", false
	}
	origin := strings.ToLower(returnTo.Scheme) + "://" + canonicalOriginHost(returnTo.Host, strings.ToLower(returnTo.Scheme))
	for _, allowed := range allowedOrigins {
		candidate, candidateErr := url.Parse(strings.TrimSpace(allowed))
		if candidateErr != nil || candidate.User != nil || candidate.Scheme == "" || candidate.Host == "" || (candidate.Path != "" && candidate.Path != "/") || candidate.RawQuery != "" || candidate.Fragment != "" {
			continue
		}
		candidateScheme := strings.ToLower(candidate.Scheme)
		candidateOrigin := candidateScheme + "://" + canonicalOriginHost(candidate.Host, candidateScheme)
		if origin == candidateOrigin {
			return returnTo.String(), true
		}
	}
	return "", false
}

func (r *Router) handleOAuth2ConsentRoute(w http.ResponseWriter, req *http.Request, parts []string) bool {
	if len(parts) == 3 {
		if req.Method != http.MethodGet {
			writeJSON(w, http.StatusMethodNotAllowed, ErrorResponse{Code: ErrorInvalidRequest, Message: "method not allowed", CorrelationID: correlationID(req)})
			return true
		}
		r.handleOAuth2Consent(w, req)
		return true
	}
	if len(parts) != 4 {
		return false
	}
	switch parts[3] {
	case "pending":
		if req.Method != http.MethodGet {
			writeJSON(w, http.StatusMethodNotAllowed, ErrorResponse{Code: ErrorInvalidRequest, Message: "method not allowed", CorrelationID: correlationID(req)})
			return true
		}
		r.handleOAuth2ConsentPending(w, req)
		return true
	case "accept":
		if req.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, ErrorResponse{Code: ErrorInvalidRequest, Message: "method not allowed", CorrelationID: correlationID(req)})
			return true
		}
		if authCredentialFromRequest(req).source == authCredentialCookie && !cookieMutationOriginAllowed(req) {
			status, response := MapError(userauth.ErrUnauthorized, correlationID(req))
			writeJSON(w, status, response)
			return true
		}
		r.handleOAuth2ConsentAccept(w, req)
		return true
	case "reject":
		if req.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, ErrorResponse{Code: ErrorInvalidRequest, Message: "method not allowed", CorrelationID: correlationID(req)})
			return true
		}
		if authCredentialFromRequest(req).source == authCredentialCookie && !cookieMutationOriginAllowed(req) {
			status, response := MapError(userauth.ErrUnauthorized, correlationID(req))
			writeJSON(w, status, response)
			return true
		}
		r.handleOAuth2ConsentReject(w, req)
		return true
	default:
		return false
	}
}

func (r *Router) handleOAuth2Login(w http.ResponseWriter, req *http.Request) {
	if r == nil || r.oidcLoginConsent == nil {
		status, response := MapError(userauth.ErrServiceDependency, correlationID(req))
		writeJSON(w, status, response)
		return
	}
	challenge := strings.TrimSpace(req.URL.Query().Get("login_challenge"))
	if challenge == "" {
		status, response := invalidRequest(correlationID(req), "login_challenge", "login_challenge is required")
		writeJSON(w, status, response)
		return
	}
	principal, ok, err := r.appSessionPrincipalFromRequest(req)
	if err != nil {
		status, response := MapError(err, correlationID(req))
		writeJSON(w, status, response)
		return
	}
	if !ok {
		writeOAuth2LoginRequired(w, req)
		return
	}
	redirect, err := r.oidcLoginConsent.AcceptLogin(req.Context(), challenge, principal)
	if err != nil {
		r.writeOAuth2Error(w, req, err)
		return
	}
	r.writeOAuth2Redirect(w, req, redirect)
}

func (r *Router) handleOAuth2Consent(w http.ResponseWriter, req *http.Request) {
	if r == nil || r.oidcLoginConsent == nil {
		status, response := MapError(userauth.ErrServiceDependency, correlationID(req))
		writeJSON(w, status, response)
		return
	}
	challenge := strings.TrimSpace(req.URL.Query().Get("consent_challenge"))
	if challenge == "" {
		status, response := invalidRequest(correlationID(req), "consent_challenge", "consent_challenge is required")
		writeJSON(w, status, response)
		return
	}
	principal, ok, err := r.appSessionPrincipalFromRequest(req)
	if err != nil {
		status, response := MapError(err, correlationID(req))
		writeJSON(w, status, response)
		return
	}
	if !ok {
		writeOAuth2LoginRequired(w, req)
		return
	}
	redirect, err := r.oidcLoginConsent.AcceptConsent(req.Context(), challenge, principal)
	if err != nil {
		if errors.Is(err, identity.ErrConsentRequired) && !acceptsJSON(req) {
			http.Redirect(w, req, oauth2ConsentUIURL(challenge), http.StatusSeeOther)
			return
		}
		if errors.Is(err, identity.ErrConsentRequired) && acceptsJSON(req) {
			r.writeOAuth2ConsentPending(w, req, challenge, principal)
			return
		}
		r.writeOAuth2Error(w, req, err)
		return
	}
	r.writeOAuth2Redirect(w, req, redirect)
}

func (r *Router) handleOAuth2ConsentPending(w http.ResponseWriter, req *http.Request) {
	if r == nil || r.oidcLoginConsent == nil {
		status, response := MapError(userauth.ErrServiceDependency, correlationID(req))
		writeJSON(w, status, response)
		return
	}
	challenge := strings.TrimSpace(req.URL.Query().Get("consent_challenge"))
	if challenge == "" {
		status, response := invalidRequest(correlationID(req), "consent_challenge", "consent_challenge is required")
		writeJSON(w, status, response)
		return
	}
	principal, ok, err := r.appSessionPrincipalFromRequest(req)
	if err != nil {
		status, response := MapError(err, correlationID(req))
		writeJSON(w, status, response)
		return
	}
	if !ok {
		writeOAuth2LoginRequired(w, req)
		return
	}
	r.writeOAuth2ConsentPending(w, req, challenge, principal)
}

func (r *Router) handleOAuth2ConsentAccept(w http.ResponseWriter, req *http.Request) {
	r.handleOAuth2ConsentDecision(w, req, true)
}

func (r *Router) handleOAuth2ConsentReject(w http.ResponseWriter, req *http.Request) {
	r.handleOAuth2ConsentDecision(w, req, false)
}

func (r *Router) handleOAuth2ConsentDecision(w http.ResponseWriter, req *http.Request, approve bool) {
	if r == nil || r.oidcLoginConsent == nil {
		status, response := MapError(userauth.ErrServiceDependency, correlationID(req))
		writeJSON(w, status, response)
		return
	}
	challenge, ok := oauth2ConsentChallengeFromAction(w, req)
	if !ok {
		return
	}
	principal, ok, err := r.appSessionPrincipalFromRequest(req)
	if err != nil {
		status, response := MapError(err, correlationID(req))
		writeJSON(w, status, response)
		return
	}
	if !ok {
		writeOAuth2LoginRequired(w, req)
		return
	}
	var redirect identity.OAuth2Redirect
	if approve {
		redirect, err = r.oidcLoginConsent.ApproveConsent(req.Context(), challenge, principal)
	} else {
		redirect, err = r.oidcLoginConsent.RejectConsent(req.Context(), challenge, principal)
	}
	if err != nil {
		r.writeOAuth2Error(w, req, err)
		return
	}
	r.writeOAuth2Redirect(w, req, redirect)
}

func (r *Router) appSessionPrincipalFromRequest(req *http.Request) (identity.Principal, bool, error) {
	if r == nil || r.auth == nil {
		return identity.Principal{}, false, nil
	}
	credential := authCredentialFromRequest(req)
	if strings.TrimSpace(credential.token) == "" {
		return identity.Principal{}, false, nil
	}
	if credential.source == authCredentialBearer && !isAppSessionToken(credential.token) {
		return identity.Principal{}, false, nil
	}
	principal, err := r.auth.AuthenticatePrincipal(req.Context(), credential.token)
	if err != nil {
		if credential.source == authCredentialCookie && errors.Is(err, userauth.ErrUnauthorized) {
			return identity.Principal{}, false, nil
		}
		return identity.Principal{}, false, err
	}
	return principal, true, nil
}

func (r *Router) writeOAuth2ConsentPending(w http.ResponseWriter, req *http.Request, challenge string, principal identity.Principal) {
	preview, err := r.oidcLoginConsent.PreviewConsent(req.Context(), challenge, principal)
	if err != nil {
		r.writeOAuth2Error(w, req, err)
		return
	}
	writeJSON(w, http.StatusOK, oauth2ConsentPreview(preview))
}

func (r *Router) writeOAuth2Redirect(w http.ResponseWriter, req *http.Request, redirect identity.OAuth2Redirect) {
	if strings.TrimSpace(redirect.RedirectTo) == "" {
		status, response := MapError(userauth.ErrInvalidRequest, correlationID(req))
		writeJSON(w, status, response)
		return
	}
	if acceptsJSON(req) {
		writeJSON(w, http.StatusOK, redirect)
		return
	}
	http.Redirect(w, req, redirect.RedirectTo, http.StatusSeeOther)
}

func (r *Router) writeOAuth2Error(w http.ResponseWriter, req *http.Request, err error) {
	if errors.Is(err, identity.ErrConsentRequired) {
		writeJSON(w, http.StatusForbidden, ErrorResponse{
			Code:          ErrorForbidden,
			Message:       "oauth2 consent requires user approval",
			CorrelationID: correlationID(req),
		})
		return
	}
	status, response := MapError(identityAuthError(err), correlationID(req))
	writeJSON(w, status, response)
}

func companySSOURLForOAuth2Return(req *http.Request) string {
	returnTo := "/"
	if req != nil && req.URL != nil {
		returnTo = req.URL.RequestURI()
		if !strings.HasPrefix(returnTo, "/") || strings.HasPrefix(returnTo, "//") || strings.Contains(returnTo, "\\") {
			returnTo = "/"
		}
	}
	query := url.Values{}
	query.Set("return_to", returnTo)
	return "/api/auth/identity/kratos/begin?" + query.Encode()
}

func writeOAuth2LoginRequired(w http.ResponseWriter, req *http.Request) {
	if authCredentialFromRequest(req).source == authCredentialCookie {
		clearAuthSessionCookie(w, req)
	}
	if acceptsJSON(req) {
		status, response := MapError(userauth.ErrUnauthorized, correlationID(req))
		writeJSON(w, status, response)
		return
	}
	http.Redirect(w, req, companySSOURLForOAuth2Return(req), http.StatusSeeOther)
}

type oauth2ConsentActionRequest struct {
	ConsentChallenge string `json:"consent_challenge"`
}

type oauth2ConsentPreviewDTO struct {
	Challenge         string              `json:"challenge"`
	Subject           string              `json:"subject,omitempty"`
	Client            oauth2ConsentClient `json:"client"`
	RequestedScopes   []string            `json:"requested_scopes"`
	GrantScopes       []string            `json:"grant_scopes"`
	RequestedAudience []string            `json:"requested_audience,omitempty"`
	GrantAudience     []string            `json:"grant_audience,omitempty"`
}

type oauth2ConsentClient struct {
	ClientID    string `json:"client_id"`
	DisplayName string `json:"display_name,omitempty"`
	Kind        string `json:"kind,omitempty"`
	FirstParty  bool   `json:"first_party"`
}

func oauth2ConsentChallengeFromAction(w http.ResponseWriter, req *http.Request) (string, bool) {
	challenge := ""
	if req != nil && req.URL != nil {
		challenge = strings.TrimSpace(req.URL.Query().Get("consent_challenge"))
	}
	if challenge == "" && req != nil && req.Body != nil {
		var body oauth2ConsentActionRequest
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			status, response := invalidRequest(correlationID(req), "body", "request body is invalid")
			writeJSON(w, status, response)
			return "", false
		}
		challenge = strings.TrimSpace(body.ConsentChallenge)
	}
	if challenge == "" {
		status, response := invalidRequest(correlationID(req), "consent_challenge", "consent_challenge is required")
		writeJSON(w, status, response)
		return "", false
	}
	return challenge, true
}

func oauth2ConsentPreview(preview identity.OAuth2ConsentSummary) oauth2ConsentPreviewDTO {
	return oauth2ConsentPreviewDTO{
		Challenge: preview.Challenge,
		Subject:   preview.Subject,
		Client: oauth2ConsentClient{
			ClientID:    preview.Client.ClientID,
			DisplayName: preview.Client.DisplayName,
			Kind:        preview.Client.Kind,
			FirstParty:  preview.Client.FirstParty,
		},
		RequestedScopes:   append([]string(nil), preview.RequestedScopes...),
		GrantScopes:       append([]string(nil), preview.GrantScopes...),
		RequestedAudience: append([]string(nil), preview.RequestedAudience...),
		GrantAudience:     append([]string(nil), preview.GrantAudience...),
	}
}

func oauth2ConsentUIURL(challenge string) string {
	query := url.Values{}
	query.Set("consent_challenge", strings.TrimSpace(challenge))
	return "/oauth2/consent?" + query.Encode()
}
