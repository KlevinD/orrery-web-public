package httptransport

import (
	"net/http"
	"net/url"
	"strings"

	"orrery_v2/internal/app/identity"
	"orrery_v2/internal/app/userauth"
)

func (r *Router) handleGetProfileFeishuIdentity(w http.ResponseWriter, req *http.Request) {
	if r == nil || r.profileIdentity == nil {
		writeJSON(w, http.StatusOK, identity.SelfServiceBindingDTO{Provider: identity.ProviderFeishu, Configured: false})
		return
	}
	profileID, ok := profileIdentityAgentProfileID(w, req)
	if !ok {
		return
	}
	binding, err := r.profileIdentity.GetBinding(req.Context(), profileID)
	if err != nil {
		status, response := MapError(err, correlationID(req))
		writeJSON(w, status, response)
		return
	}
	writeJSON(w, http.StatusOK, binding)
}

func (r *Router) handleBeginProfileFeishuIdentity(w http.ResponseWriter, req *http.Request) {
	if r == nil || r.profileIdentity == nil {
		status, response := MapError(identity.ErrServiceDependency, correlationID(req))
		writeJSON(w, status, response)
		return
	}
	profileID, ok := profileIdentityAgentProfileID(w, req)
	if !ok {
		return
	}
	redirect, err := r.profileIdentity.BeginBinding(req.Context(), profileID, safeProfileReturnTo(req.URL.Query().Get("return_to")))
	if err != nil {
		status, response := MapError(err, correlationID(req))
		writeJSON(w, status, response)
		return
	}
	setIdentityFlowCookie(w, req, identity.ProviderFeishu, redirect)
	if acceptsJSON(req) {
		writeJSON(w, http.StatusOK, redirect)
		return
	}
	http.Redirect(w, req, redirect.URL, http.StatusSeeOther)
}

func (r *Router) handleCompleteProfileFeishuIdentity(w http.ResponseWriter, req *http.Request) {
	if r == nil || r.profileIdentity == nil {
		status, response := MapError(identity.ErrServiceDependency, correlationID(req))
		writeJSON(w, status, response)
		return
	}
	profileID, ok := profileIdentityAgentProfileID(w, req)
	if !ok {
		return
	}
	binding, err := r.profileIdentity.CompleteBinding(req.Context(), profileID, identity.CompleteLoginRequest{
		RequestURL: requestAbsoluteURL(req),
		Code:       req.URL.Query().Get("code"),
		State:      req.URL.Query().Get("state"),
		Cookie:     req.Header.Get("Cookie"),
	})
	clearIdentityFlowCookie(w, req, identity.ProviderFeishu)
	if err != nil {
		status, response := MapError(err, correlationID(req))
		writeJSON(w, status, response)
		return
	}
	if acceptsJSON(req) {
		writeJSON(w, http.StatusOK, binding)
		return
	}
	returnTo := r.profileIdentity.BindingReturnTo(req.URL.Query().Get("state"))
	http.Redirect(w, req, profileIdentityResultReturnTo(returnTo), http.StatusSeeOther)
}

func (r *Router) handleDeleteProfileFeishuIdentity(w http.ResponseWriter, req *http.Request) {
	if r == nil || r.profileIdentity == nil {
		status, response := MapError(identity.ErrServiceDependency, correlationID(req))
		writeJSON(w, status, response)
		return
	}
	profileID, ok := profileIdentityAgentProfileID(w, req)
	if !ok {
		return
	}
	binding, err := r.profileIdentity.Unbind(req.Context(), profileID)
	if err != nil {
		status, response := MapError(err, correlationID(req))
		writeJSON(w, status, response)
		return
	}
	writeJSON(w, http.StatusOK, binding)
}

func profileIdentityAgentProfileID(w http.ResponseWriter, req *http.Request) (string, bool) {
	principal, ok := identity.PrincipalFromContext(req.Context())
	if !ok || strings.TrimSpace(principal.AgentProfileID) == "" {
		status, response := MapError(userauth.ErrUnauthorized, correlationID(req))
		writeJSON(w, status, response)
		return "", false
	}
	return strings.TrimSpace(principal.AgentProfileID), true
}

func safeProfileReturnTo(value string) string {
	value = safeAuthReturnTo(value)
	if value == "/" {
		return "/profile"
	}
	return value
}

func profileIdentityResultReturnTo(value string) string {
	value = safeProfileReturnTo(value)
	parsed, err := url.Parse(value)
	if err != nil || parsed.IsAbs() || parsed.Host != "" {
		return "/profile?feishu_sso=bound"
	}
	query := parsed.Query()
	query.Set("feishu_sso", "bound")
	parsed.RawQuery = query.Encode()
	return parsed.String()
}
