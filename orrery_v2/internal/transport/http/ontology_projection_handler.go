package httptransport

import (
	"context"
	"net/http"
	"strings"
	"unicode/utf8"

	"orrery_v2/internal/app/identity"
	"orrery_v2/internal/app/ontologyscope"
	ontologycontract "orrery_v2/internal/app/ontologyview/contract"
	projectionview "orrery_v2/internal/app/ontologyview/projection"
)

type OntologyProjectionService interface {
	Summary(context.Context, identity.Principal, string) (ontologycontract.Summary[projectionview.SummaryData], error)
	ListAudits(context.Context, identity.Principal, projectionview.AuditListQuery) (ontologycontract.CursorPage[projectionview.AuditItem], error)
	GetAudit(context.Context, identity.Principal, string, string) (projectionview.AuditDetail, error)
	ListReplayHistory(context.Context, identity.Principal, projectionview.ReplayHistoryListQuery) (ontologycontract.CursorPage[projectionview.ReplayHistoryItem], error)
}

type OntologyProjectionCommandService interface {
	Replay(context.Context, identity.Principal, projectionview.ReplayRequest) (projectionview.MutationResult, error)
	Acknowledge(context.Context, identity.Principal, projectionview.AcknowledgeRequest) (projectionview.MutationResult, error)
}

type ontologyProjectionReplayBody struct{}

type ontologyProjectionAcknowledgeBody struct {
	Reason string `json:"reason"`
}

func (r *Router) handleOntologyProjection(w http.ResponseWriter, req *http.Request, parts []string) {
	setOntologyPrivateNoStore(w)
	cid := correlationID(req)
	principal, ok := ontologyPrincipal(req)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, ErrorResponse{Code: ErrorUnauthorized, Message: "authorization is required", CorrelationID: cid})
		return
	}
	tenantID := strings.TrimSpace(req.URL.Query().Get("tenant_id"))
	if !validOptionalOntologyTenantID(tenantID) {
		writeOntologyViewError(w, ontologycontract.ErrInvalidRequest, cid)
		return
	}
	if req.Method != http.MethodGet && req.Method != http.MethodPost {
		writeJSON(w, http.StatusNotFound, ErrorResponse{Code: ErrorNotFound, Message: "resource not found", CorrelationID: cid})
		return
	}
	if req.Method == http.MethodPost {
		if tenantID == "" {
			writeOntologyViewError(w, ontologycontract.ErrInvalidRequest, cid)
			return
		}
		r.handleOntologyProjectionMutation(w, req, principal, tenantID, parts, cid)
		return
	}
	if r.ontologyProjection == nil {
		writeOntologyViewError(w, ontologyscope.ErrDependencyUnavailable, cid)
		return
	}
	switch {
	case len(parts) == 4 && parts[3] == "summary":
		result, err := r.ontologyProjection.Summary(req.Context(), principal, tenantID)
		writeOntologyProjectionResult(w, result, err, cid)
	case len(parts) == 4 && parts[3] == "audits":
		query, ok := ontologyProjectionAuditListQuery(req, tenantID)
		if !ok {
			writeOntologyViewError(w, ontologycontract.ErrInvalidRequest, cid)
			return
		}
		result, err := r.ontologyProjection.ListAudits(req.Context(), principal, query)
		writeOntologyProjectionResult(w, result, err, cid)
	case len(parts) == 5 && parts[3] == "audits":
		if tenantID == "" || !validOntologyProjectionID(parts[4]) {
			writeOntologyViewError(w, ontologycontract.ErrInvalidRequest, cid)
			return
		}
		result, err := r.ontologyProjection.GetAudit(req.Context(), principal, tenantID, parts[4])
		writeOntologyProjectionResult(w, result, err, cid)
	case len(parts) == 6 && parts[3] == "audits" && parts[5] == "replay-history":
		if tenantID == "" {
			writeOntologyViewError(w, ontologycontract.ErrInvalidRequest, cid)
			return
		}
		query, ok := ontologyProjectionReplayHistoryQuery(req, tenantID, parts[4])
		if !ok {
			writeOntologyViewError(w, ontologycontract.ErrInvalidRequest, cid)
			return
		}
		result, err := r.ontologyProjection.ListReplayHistory(req.Context(), principal, query)
		writeOntologyProjectionResult(w, result, err, cid)
	default:
		writeJSON(w, http.StatusNotFound, ErrorResponse{Code: ErrorNotFound, Message: "resource not found", CorrelationID: cid})
	}
}

func (r *Router) handleOntologyProjectionMutation(w http.ResponseWriter, req *http.Request, principal identity.Principal, tenantID string, parts []string, cid string) {
	if r.ontologyProjectionCommand == nil {
		writeOntologyViewError(w, ontologyscope.ErrDependencyUnavailable, cid)
		return
	}
	if !ontologyMutationOriginAllowed(req) {
		writeJSON(w, http.StatusForbidden, ErrorResponse{Code: ErrorForbidden, Message: "request origin is not allowed", CorrelationID: cid})
		return
	}
	if len(parts) != 6 || parts[3] != "audits" || parts[5] != "replays" && parts[5] != "acknowledgements" {
		writeJSON(w, http.StatusNotFound, ErrorResponse{Code: ErrorNotFound, Message: "resource not found", CorrelationID: cid})
		return
	}
	if !validOntologyProjectionID(parts[4]) {
		writeOntologyViewError(w, ontologycontract.ErrInvalidRequest, cid)
		return
	}
	commandKey := idempotencyKey(req)
	if commandKey == "" || len(commandKey) > 256 || !utf8.ValidString(commandKey) {
		writeOntologyViewError(w, ontologycontract.ErrInvalidRequest, cid)
		return
	}
	switch parts[5] {
	case "replays":
		var body ontologyProjectionReplayBody
		if err := decodeOntologyMutationBody(req, &body); err != nil {
			writeOntologyViewError(w, ontologycontract.ErrInvalidRequest, cid)
			return
		}
		result, err := r.ontologyProjectionCommand.Replay(req.Context(), principal, projectionview.ReplayRequest{TenantID: tenantID, AuditID: parts[4], IdempotencyKey: commandKey})
		writeOntologyProjectionResult(w, result, err, cid)
	case "acknowledgements":
		var body ontologyProjectionAcknowledgeBody
		if err := decodeOntologyMutationBody(req, &body); err != nil {
			writeOntologyViewError(w, ontologycontract.ErrInvalidRequest, cid)
			return
		}
		result, err := r.ontologyProjectionCommand.Acknowledge(req.Context(), principal, projectionview.AcknowledgeRequest{
			TenantID: tenantID, AuditID: parts[4], Reason: body.Reason, IdempotencyKey: commandKey,
		})
		writeOntologyProjectionResult(w, result, err, cid)
	}
}

func ontologyProjectionAuditListQuery(req *http.Request, tenantID string) (projectionview.AuditListQuery, bool) {
	limit, err := optionalOntologyLimit(req.URL.Query().Get("limit"))
	if err != nil {
		return projectionview.AuditListQuery{}, false
	}
	query := projectionview.AuditListQuery{
		TenantID: tenantID, Status: strings.ToLower(strings.TrimSpace(req.URL.Query().Get("status"))),
		ReasonCode: strings.ToLower(strings.TrimSpace(req.URL.Query().Get("reason_code"))),
		Limit:      limit, Cursor: strings.TrimSpace(req.URL.Query().Get("cursor")),
	}
	if query.Status != "" {
		switch query.Status {
		case "applied", "skipped", "replayed", "acknowledged", "failed":
		default:
			return projectionview.AuditListQuery{}, false
		}
	}
	if !utf8.ValidString(query.ReasonCode) || len(query.ReasonCode) > 256 || len(query.Cursor) > 4096 {
		return projectionview.AuditListQuery{}, false
	}
	return query, true
}

func ontologyProjectionReplayHistoryQuery(req *http.Request, tenantID, auditID string) (projectionview.ReplayHistoryListQuery, bool) {
	if !validOntologyProjectionID(auditID) {
		return projectionview.ReplayHistoryListQuery{}, false
	}
	limit, err := optionalOntologyLimit(req.URL.Query().Get("limit"))
	if err != nil {
		return projectionview.ReplayHistoryListQuery{}, false
	}
	cursor := strings.TrimSpace(req.URL.Query().Get("cursor"))
	if len(cursor) > 4096 {
		return projectionview.ReplayHistoryListQuery{}, false
	}
	return projectionview.ReplayHistoryListQuery{TenantID: tenantID, AuditID: auditID, Limit: limit, Cursor: cursor}, true
}

func validOntologyProjectionID(value string) bool {
	value = strings.TrimSpace(value)
	return value != "" && len(value) <= 256
}

func writeOntologyProjectionResult(w http.ResponseWriter, result any, err error, cid string) {
	if err != nil {
		writeOntologyViewError(w, err, cid)
		return
	}
	writeJSON(w, http.StatusOK, result)
}
