package httptransport

import (
	"context"
	"net/http"

	"orrery_v2/internal/app/userauth"
)

type desktopSessionAuthenticator interface {
	AuthenticateBearerSession(context.Context, string) (userauth.SessionDTO, error)
}

func (r *Router) handleDesktopCurrentSession(w http.ResponseWriter, req *http.Request, token string) {
	service, ok := r.bearerAuth.(desktopSessionAuthenticator)
	if !ok {
		status, body := MapError(userauth.ErrUnauthorized, correlationID(req))
		writeJSON(w, status, body)
		return
	}
	session, err := service.AuthenticateBearerSession(req.Context(), token)
	if err != nil {
		status, body := MapError(err, correlationID(req))
		writeJSON(w, status, body)
		return
	}
	setRequestAuditActor(req, auditActorFromPrincipal(userauth.PrincipalFromSession(session)))
	session.AccessToken = ""
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, session)
}
