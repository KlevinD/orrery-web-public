package httptransport

import (
	"context"
	"net/http"
	"net/url"
	"strings"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/identity"
	missioncontrol "orrery_v2/internal/app/missioncontrol"
	"orrery_v2/internal/app/userauth"
)

type derivedOrganizationTenantContextKey struct{}

func contextWithDerivedOrganizationTenant(ctx context.Context, tenantID string) context.Context {
	tenantID = strings.TrimSpace(tenantID)
	if ctx == nil || tenantID == "" {
		return ctx
	}
	return context.WithValue(ctx, derivedOrganizationTenantContextKey{}, tenantID)
}

func derivedOrganizationTenantFromContext(ctx context.Context) (string, bool) {
	if ctx == nil {
		return "", false
	}
	tenantID, ok := ctx.Value(derivedOrganizationTenantContextKey{}).(string)
	tenantID = strings.TrimSpace(tenantID)
	return tenantID, ok && tenantID != ""
}

func ViewerFromRequest(r *http.Request) (missioncontrol.ViewerRef, error) {
	if r != nil {
		if principal, ok := identity.PrincipalFromContext(r.Context()); ok {
			viewer, err := viewerFromPrincipal(principal)
			if err != nil {
				return missioncontrol.ViewerRef{}, err
			}
			viewer.PermissionScope = mergePermissionScopes(viewer.PermissionScope, authorizedRoutePermissionScopes(r))
			return viewer, nil
		}
	}
	return viewerFromRequestHeaders(r)
}

func viewerFromPrincipal(principal identity.Principal) (missioncontrol.ViewerRef, error) {
	principal = identity.NormalizePrincipal(principal)
	viewerID := firstNonEmpty(principal.Username, principal.UserID, principal.Subject)
	if principal.ViewerKind == identity.ViewerKindAgent {
		// Usernames are case-normalized for login, while agent identities are
		// case-sensitive (for example, A000011). Keep the principal identity first.
		viewerID = firstNonEmpty(principal.UserID, principal.Subject, principal.AgentProfileID, principal.Username)
	}
	viewer := missioncontrol.ViewerRef{
		ViewerID:               viewerID,
		ViewerKind:             missioncontrol.ViewerKind(principal.ViewerKind),
		AuthorizationSubjectID: authorizationSubjectIDFromPrincipal(principal),
		TenantID:               principal.TenantID,
		PermissionScope:        append([]string(nil), principal.CoarseScopes...),
	}
	if strings.TrimSpace(principal.AgentProfileID) == agentcore.SystemSuperadminPrincipalID && strings.TrimSpace(principal.Username) == userauth.SuperadminUsername {
		viewer.ViewerID = agentcore.SystemSuperadminPrincipalID
		viewer.ViewerKind = missioncontrol.ViewerKindSystem
		viewer.AuthorizationSubjectID = agentcore.SystemSuperadminPrincipalID
	}
	if err := viewer.Validate(); err != nil {
		return missioncontrol.ViewerRef{}, err
	}
	return viewer, nil
}

func viewerFromRequestHeaders(r *http.Request) (missioncontrol.ViewerRef, error) {
	if r == nil || r.URL == nil {
		return missioncontrol.ViewerRef{}, missioncontrol.ErrInvalidViewer
	}
	query := r.URL.Query()
	if !acceptsQueryCredentials(r) {
		query = nil
	}
	viewerID := firstNonEmpty(strings.TrimSpace(r.Header.Get("X-User-ID")), strings.TrimSpace(query.Get("viewer_id")))
	if viewerID == "" {
		viewerID = firstNonEmpty(strings.TrimSpace(r.Header.Get("X-Agent-ID")), strings.TrimSpace(query.Get("agent_id")))
	}
	kind := missioncontrol.ViewerKind(firstNonEmpty(strings.TrimSpace(r.Header.Get("X-Viewer-Kind")), strings.TrimSpace(query.Get("viewer_kind"))))
	if kind == "" {
		if r.Header.Get("X-Agent-ID") != "" || query.Get("agent_id") != "" {
			kind = missioncontrol.ViewerKindAgent
		} else {
			kind = missioncontrol.ViewerKindUser
		}
	}
	viewer := missioncontrol.ViewerRef{
		ViewerID:               viewerID,
		ViewerKind:             kind,
		AuthorizationSubjectID: authorizationSubjectIDFromHeaders(r, kind, viewerID),
		TenantID:               strings.TrimSpace(r.Header.Get("X-Tenant-ID")),
		MissionRoles:           splitHeader(firstNonEmpty(r.Header.Get("X-Mission-Roles"), query.Get("mission_roles"))),
		PermissionScope:        splitHeader(firstNonEmpty(r.Header.Get("X-Permission-Scope"), query.Get("permission_scope"))),
	}
	if err := viewer.Validate(); err != nil {
		return missioncontrol.ViewerRef{}, err
	}
	return viewer, nil
}

func authorizationSubjectIDFromPrincipal(principal identity.Principal) string {
	principal = identity.NormalizePrincipal(principal)
	if principal.ViewerKind == identity.ViewerKindUser {
		return firstNonEmpty(principal.AgentProfileID, principal.UserID, principal.Username, principal.Subject)
	}
	return firstNonEmpty(principal.UserID, principal.Username, principal.Subject)
}

func accountOwnerIDFromPrincipal(principal identity.Principal) string {
	principal = identity.NormalizePrincipal(principal)
	return firstNonEmpty(principal.UserID, principal.Username, principal.AgentProfileID, principal.Subject)
}

func authorizationSubjectIDFromHeaders(r *http.Request, kind missioncontrol.ViewerKind, viewerID string) string {
	if r != nil && (kind == missioncontrol.ViewerKindUser || kind == missioncontrol.ViewerKindHuman) {
		return firstNonEmpty(strings.TrimSpace(r.Header.Get("X-Agent-Profile-ID")), viewerID)
	}
	return strings.TrimSpace(viewerID)
}

func displayNameFromRequest(r *http.Request) string {
	value := strings.TrimSpace(r.Header.Get("X-User-Display-Name"))
	if value == "" {
		return ""
	}
	decoded, err := url.QueryUnescape(value)
	if err != nil {
		return value
	}
	return strings.TrimSpace(decoded)
}

func acceptsEventStream(r *http.Request) bool {
	for _, value := range strings.Split(r.Header.Get("Accept"), ",") {
		if strings.TrimSpace(value) == "text/event-stream" {
			return true
		}
	}
	return false
}

func acceptsQueryCredentials(r *http.Request) bool {
	if r == nil || r.URL == nil {
		return false
	}
	path := r.URL.Path
	if path == "/api/realtime/connect" {
		return true
	}
	if acceptsEventStream(r) {
		return path == "/api/evaluations/stream" || isMissionStreamPath(path)
	}
	return false
}

func isMissionStreamPath(path string) bool {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	return len(parts) == 4 && parts[0] == "api" && parts[1] == "missions" && strings.TrimSpace(parts[2]) != "" && parts[3] == "stream"
}

func splitHeader(value string) []string {
	var out []string
	for _, part := range strings.Split(value, ",") {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}
