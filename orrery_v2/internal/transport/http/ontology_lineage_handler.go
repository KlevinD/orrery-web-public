package httptransport

import (
	"context"
	"net/http"
	"strings"

	"orrery_v2/internal/app/identity"
	"orrery_v2/internal/app/ontologyscope"
	ontologycontract "orrery_v2/internal/app/ontologyview/contract"
	lineageview "orrery_v2/internal/app/ontologyview/lineage"
)

type OntologyLineageService interface {
	Summary(context.Context, identity.Principal, string) (ontologycontract.Summary[lineageview.SummaryData], error)
	GetProvenance(context.Context, identity.Principal, string, string) (lineageview.ProvenanceAnchor, error)
	ListAssertions(context.Context, identity.Principal, lineageview.FactPageQuery) (ontologycontract.CursorPage[lineageview.AssertionItem], error)
	ListEvidence(context.Context, identity.Principal, lineageview.FactPageQuery) (ontologycontract.CursorPage[lineageview.EvidenceItem], error)
	ListProjectionTrace(context.Context, identity.Principal, lineageview.FactPageQuery) (ontologycontract.CursorPage[lineageview.ProjectionTraceItem], error)
}

func (r *Router) handleOntologyLineage(w http.ResponseWriter, req *http.Request, parts []string) {
	setOntologyPrivateNoStore(w)
	cid := correlationID(req)
	principal, ok := ontologyPrincipal(req)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, ErrorResponse{Code: ErrorUnauthorized, Message: "authorization is required", CorrelationID: cid})
		return
	}
	if r.ontologyLineage == nil {
		writeOntologyViewError(w, ontologyscope.ErrDependencyUnavailable, cid)
		return
	}
	tenantID := strings.TrimSpace(req.URL.Query().Get("tenant_id"))
	if !validOptionalOntologyTenantID(tenantID) {
		writeOntologyViewError(w, ontologycontract.ErrInvalidRequest, cid)
		return
	}
	if req.Method != http.MethodGet {
		writeJSON(w, http.StatusNotFound, ErrorResponse{Code: ErrorNotFound, Message: "resource not found", CorrelationID: cid})
		return
	}
	switch {
	case len(parts) == 4 && parts[3] == "summary":
		result, err := r.ontologyLineage.Summary(req.Context(), principal, tenantID)
		writeOntologyLineageResult(w, result, err, cid)
	case len(parts) == 6 && parts[3] == "facts" && parts[5] == "provenance":
		if tenantID == "" || !validOntologyLineageID(parts[4]) {
			writeOntologyViewError(w, ontologycontract.ErrInvalidRequest, cid)
			return
		}
		result, err := r.ontologyLineage.GetProvenance(req.Context(), principal, tenantID, parts[4])
		writeOntologyLineageResult(w, result, err, cid)
	case len(parts) == 6 && parts[3] == "facts" && parts[5] == "assertions":
		if tenantID == "" {
			writeOntologyViewError(w, ontologycontract.ErrInvalidRequest, cid)
			return
		}
		query, ok := ontologyLineagePageQuery(req, tenantID, parts[4])
		if !ok {
			writeOntologyViewError(w, ontologycontract.ErrInvalidRequest, cid)
			return
		}
		result, err := r.ontologyLineage.ListAssertions(req.Context(), principal, query)
		writeOntologyLineageResult(w, result, err, cid)
	case len(parts) == 6 && parts[3] == "facts" && parts[5] == "evidence":
		if tenantID == "" {
			writeOntologyViewError(w, ontologycontract.ErrInvalidRequest, cid)
			return
		}
		query, ok := ontologyLineagePageQuery(req, tenantID, parts[4])
		if !ok {
			writeOntologyViewError(w, ontologycontract.ErrInvalidRequest, cid)
			return
		}
		result, err := r.ontologyLineage.ListEvidence(req.Context(), principal, query)
		writeOntologyLineageResult(w, result, err, cid)
	case len(parts) == 6 && parts[3] == "facts" && parts[5] == "projection-trace":
		if tenantID == "" {
			writeOntologyViewError(w, ontologycontract.ErrInvalidRequest, cid)
			return
		}
		query, ok := ontologyLineagePageQuery(req, tenantID, parts[4])
		if !ok {
			writeOntologyViewError(w, ontologycontract.ErrInvalidRequest, cid)
			return
		}
		result, err := r.ontologyLineage.ListProjectionTrace(req.Context(), principal, query)
		writeOntologyLineageResult(w, result, err, cid)
	default:
		writeJSON(w, http.StatusNotFound, ErrorResponse{Code: ErrorNotFound, Message: "resource not found", CorrelationID: cid})
	}
}

func ontologyLineagePageQuery(req *http.Request, tenantID, factID string) (lineageview.FactPageQuery, bool) {
	if !validOntologyLineageID(factID) {
		return lineageview.FactPageQuery{}, false
	}
	cursor := strings.TrimSpace(req.URL.Query().Get("cursor"))
	if len(cursor) > 4096 {
		return lineageview.FactPageQuery{}, false
	}
	limit, err := optionalOntologyLimit(req.URL.Query().Get("limit"))
	if err != nil {
		return lineageview.FactPageQuery{}, false
	}
	return lineageview.FactPageQuery{
		TenantID: tenantID, FactID: factID, Limit: limit, Cursor: cursor,
	}, true
}

func validOntologyLineageID(value string) bool {
	value = strings.TrimSpace(value)
	return value != "" && len(value) <= 256
}

func writeOntologyLineageResult(w http.ResponseWriter, result any, err error, cid string) {
	if err != nil {
		writeOntologyViewError(w, err, cid)
		return
	}
	writeJSON(w, http.StatusOK, result)
}
