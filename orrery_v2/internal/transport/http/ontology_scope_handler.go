package httptransport

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"orrery_v2/internal/app/identity"
	"orrery_v2/internal/app/ontology"
	"orrery_v2/internal/app/ontologyscope"
	ontologycontract "orrery_v2/internal/app/ontologyview/contract"
)

func (r *Router) handleListOntologyScopes(w http.ResponseWriter, req *http.Request) {
	setOntologyPrivateNoStore(w)
	cid := correlationID(req)
	principal, ok := ontologyPrincipal(req)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, ErrorResponse{Code: ErrorUnauthorized, Message: "authorization is required", CorrelationID: cid})
		return
	}
	if r.ontologyScopes == nil {
		writeOntologyViewError(w, ontologyscope.ErrDependencyUnavailable, cid)
		return
	}
	limit, err := optionalOntologyLimit(req.URL.Query().Get("limit"))
	if err != nil {
		writeOntologyViewError(w, err, cid)
		return
	}
	page, err := r.ontologyScopes.List(req.Context(), principal, ontologyscope.CatalogQuery{
		Search: req.URL.Query().Get("search"),
		Kind:   ontologyscope.Kind(req.URL.Query().Get("kind")),
		Cursor: req.URL.Query().Get("cursor"),
		Limit:  limit,
	})
	if err != nil {
		writeOntologyViewError(w, err, cid)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

func (r *Router) handleResolveOntologyScope(w http.ResponseWriter, req *http.Request) {
	setOntologyPrivateNoStore(w)
	cid := correlationID(req)
	principal, ok := ontologyPrincipal(req)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, ErrorResponse{Code: ErrorUnauthorized, Message: "authorization is required", CorrelationID: cid})
		return
	}
	if r.ontologyScopes == nil {
		writeOntologyViewError(w, ontologyscope.ErrDependencyUnavailable, cid)
		return
	}
	tenantID := strings.TrimSpace(req.URL.Query().Get("tenant_id"))
	if tenantID == "" {
		writeOntologyViewError(w, ontologycontract.ErrInvalidRequest, cid)
		return
	}
	resolved, err := r.ontologyScopes.Resolve(req.Context(), principal, tenantID)
	if err != nil {
		writeOntologyViewError(w, err, cid)
		return
	}
	writeJSON(w, http.StatusOK, resolved)
}

func ontologyPrincipal(req *http.Request) (identity.Principal, bool) {
	if req == nil {
		return identity.Principal{}, false
	}
	principal, ok := identity.PrincipalFromContext(req.Context())
	principal = identity.NormalizePrincipal(principal)
	return principal, ok && principal.Authenticated()
}

func optionalOntologyLimit(raw string) (int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, nil
	}
	limit, err := strconv.Atoi(raw)
	if err != nil || limit <= 0 || limit > ontologycontract.MaxPageLimit {
		return 0, ontologycontract.ErrInvalidRequest
	}
	return limit, nil
}

func setOntologyPrivateNoStore(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "private, no-store")
}

func writeOntologyViewError(w http.ResponseWriter, err error, correlationID string) {
	status := http.StatusInternalServerError
	response := ErrorResponse{Code: ErrorInternal, Message: "internal server error", CorrelationID: correlationID, cause: err}
	switch {
	case errors.Is(err, ontologyscope.ErrBindingNotFound):
		status = http.StatusNotFound
		response.Code = ErrorUnavailable
		response.Message = "resource unavailable"
	case errors.Is(err, ontologycontract.ErrUnavailable):
		status = http.StatusNotFound
		response.Code = ErrorUnavailable
		response.Message = "resource unavailable"
	case errors.Is(err, ontologyscope.ErrAccessDenied):
		status = http.StatusForbidden
		response.Code = ErrorForbidden
		response.Message = "permission denied"
	case errors.Is(err, ontologycontract.ErrCursorStale):
		status = http.StatusConflict
		response.Code = ErrorCursorStale
		response.Message = "cursor is stale"
	case errors.Is(err, ontologycontract.ErrStateConflict):
		status = http.StatusConflict
		response.Code = ErrorStateConflict
		response.Message = "resource state changed"
	case errors.Is(err, ontology.ErrVersionConflict):
		status = http.StatusConflict
		response.Code = ErrorVersionConflict
		response.Message = "resource version changed"
	case errors.Is(err, ontology.ErrIdempotencyConflict):
		status = http.StatusConflict
		response.Code = ErrorIdempotencyConflict
		response.Message = "idempotency key conflicts with an existing request"
	case errors.Is(err, ontology.ErrNotFound):
		status = http.StatusNotFound
		response.Code = ErrorUnavailable
		response.Message = "resource unavailable"
	case errors.Is(err, ontologycontract.ErrInvalidRequest), errors.Is(err, ontologycontract.ErrInvalidCursor), errors.Is(err, ontologyscope.ErrInvalidBinding), errors.Is(err, ontology.ErrInvalidDefinition):
		status = http.StatusBadRequest
		response.Code = ErrorInvalidRequest
		response.Message = "request is invalid"
	case errors.Is(err, ontologycontract.ErrQueryBudgetExceeded):
		status = http.StatusServiceUnavailable
		response.Code = ErrorQueryBudgetExceeded
		response.Message = "query budget exceeded"
		response.Retryable = true
	case errors.Is(err, ontologycontract.ErrRateLimited):
		status = http.StatusTooManyRequests
		response.Code = ErrorRateLimited
		response.Message = "request is rate limited"
		response.Retryable = true
		var limited ontologycontract.RateLimitError
		if errors.As(err, &limited) {
			retryAfter := limited.RetryAfter
			if retryAfter < time.Second {
				retryAfter = time.Second
			}
			w.Header().Set("Retry-After", strconv.FormatInt(int64((retryAfter+time.Second-1)/time.Second), 10))
		}
	case errors.Is(err, ontologyscope.ErrDependencyUnavailable), errors.Is(err, ontologyscope.ErrBindingConflict):
		status = http.StatusServiceUnavailable
		response.Code = ErrorDependencyUnavailable
		response.Message = "service dependency unavailable"
		response.Retryable = true
	}
	writeJSON(w, status, response)
}
