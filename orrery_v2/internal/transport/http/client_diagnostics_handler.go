package httptransport

import (
	"context"
	"errors"
	"net/http"

	"orrery_v2/internal/app/identity"
)

var ErrDiagnosticRateLimited = errors.New("diagnostic report rate limit exceeded")
var ErrDiagnosticActorMismatch = errors.New("diagnostic report actor does not match the session")

type ClientErrorReport struct {
	ActorID   string `json:"actor_id,omitempty"`
	ID        string `json:"id"`
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	Message   string `json:"message"`
	Stack     string `json:"stack,omitempty"`
	Path      string `json:"path"`
	RequestID string `json:"request_id,omitempty"`
}
type ClientErrorReceipt struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}
type ClientDiagnosticsService interface {
	ReportClientError(context.Context, identity.Principal, ClientErrorReport) (ClientErrorReceipt, error)
}

func (r *Router) handleClientDiagnostics(w http.ResponseWriter, req *http.Request) {
	setOntologyPrivateNoStore(w)
	principal, ok := ontologyPrincipal(req)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, ErrorResponse{Code: ErrorUnauthorized, Message: "authorization is required", CorrelationID: correlationID(req)})
		return
	}
	if !ontologyMutationOriginAllowed(req) {
		writeJSON(w, http.StatusForbidden, ErrorResponse{Code: ErrorForbidden, Message: "permission denied", CorrelationID: correlationID(req)})
		return
	}
	if r.clientDiagnostics == nil {
		writeJSON(w, http.StatusServiceUnavailable, ErrorResponse{Code: ErrorUnavailable, Message: "diagnostics unavailable", CorrelationID: correlationID(req)})
		return
	}
	req.Body = http.MaxBytesReader(w, req.Body, 16*1024)
	var report ClientErrorReport
	if err := decodeOntologyMutationBody(req, &report); err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Code: ErrorInvalidRequest, Message: "invalid diagnostic", CorrelationID: correlationID(req)})
		return
	}
	receipt, err := r.clientDiagnostics.ReportClientError(req.Context(), principal, report)
	if errors.Is(err, ErrDiagnosticActorMismatch) {
		writeJSON(w, http.StatusForbidden, ErrorResponse{Code: ErrorForbidden, Message: "diagnostic actor does not match the session", CorrelationID: correlationID(req)})
		return
	}
	if errors.Is(err, ErrDiagnosticRateLimited) {
		w.Header().Set("Retry-After", "60")
		writeJSON(w, http.StatusTooManyRequests, ErrorResponse{Code: "rate_limited", Message: "diagnostic report rate limit exceeded", CorrelationID: correlationID(req)})
		return
	}
	if err != nil {
		status, body := MapError(err, correlationID(req))
		writeJSON(w, status, body)
		return
	}
	writeJSON(w, http.StatusAccepted, receipt)
}
