package httptransport

import (
	"context"
	"net/http"
	"strings"

	"orrery_v2/internal/app/identity"
	"orrery_v2/internal/app/ontologyscope"
	contextview "orrery_v2/internal/app/ontologyview/context"
	ontologycontract "orrery_v2/internal/app/ontologyview/contract"
)

type OntologyExecutionService interface {
	ListTasks(context.Context, identity.Principal, contextview.ObjectListQuery) (ontologycontract.CursorPage[contextview.ObjectItem], error)
	GetTask(context.Context, identity.Principal, string, string) (contextview.ObjectDetail, error)
	GetRun(context.Context, identity.Principal, string, string) (contextview.ObjectDetail, error)
	RunLineage(context.Context, identity.Principal, contextview.NeighborhoodQuery) (contextview.Neighborhood, error)
}

func (r *Router) handleOntologyExecutions(w http.ResponseWriter, req *http.Request, parts []string) {
	setOntologyPrivateNoStore(w)
	cid := correlationID(req)
	principal, ok := ontologyPrincipal(req)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, ErrorResponse{Code: ErrorUnauthorized, Message: "authorization is required", CorrelationID: cid})
		return
	}
	if r.ontologyExecutions == nil {
		writeOntologyViewError(w, ontologyscope.ErrDependencyUnavailable, cid)
		return
	}
	tenantID := strings.TrimSpace(req.URL.Query().Get("tenant_id"))
	if req.Method != http.MethodGet || !validOptionalOntologyTenantID(tenantID) {
		writeOntologyViewError(w, ontologycontract.ErrInvalidRequest, cid)
		return
	}

	switch {
	case len(parts) == 4 && parts[3] == "tasks":
		limit, err := optionalOntologyLimit(req.URL.Query().Get("limit"))
		if err != nil {
			writeOntologyViewError(w, err, cid)
			return
		}
		query := contextview.ObjectListQuery{
			TenantID: tenantID,
			Status:   req.URL.Query().Get("status"),
			Search:   req.URL.Query().Get("search"),
			Limit:    limit,
			Cursor:   req.URL.Query().Get("cursor"),
		}
		if !validOptionalOntologyContextID(query.Status) || !validSchemaSearch(query.Search) {
			writeOntologyViewError(w, ontologycontract.ErrInvalidRequest, cid)
			return
		}
		result, err := r.ontologyExecutions.ListTasks(req.Context(), principal, query)
		writeOntologyContextResult(w, result, err, cid)
	case len(parts) == 5 && parts[3] == "tasks" && validOntologyContextID(parts[4]):
		if tenantID == "" {
			writeOntologyViewError(w, ontologycontract.ErrInvalidRequest, cid)
			return
		}
		result, err := r.ontologyExecutions.GetTask(req.Context(), principal, tenantID, parts[4])
		writeOntologyContextResult(w, result, err, cid)
	case len(parts) == 5 && parts[3] == "runs" && validOntologyContextID(parts[4]):
		if tenantID == "" {
			writeOntologyViewError(w, ontologycontract.ErrInvalidRequest, cid)
			return
		}
		result, err := r.ontologyExecutions.GetRun(req.Context(), principal, tenantID, parts[4])
		writeOntologyContextResult(w, result, err, cid)
	case len(parts) == 6 && parts[3] == "runs" && validOntologyContextID(parts[4]) && parts[5] == "lineage":
		if tenantID == "" {
			writeOntologyViewError(w, ontologycontract.ErrInvalidRequest, cid)
			return
		}
		result, err := r.ontologyExecutions.RunLineage(req.Context(), principal, contextview.NeighborhoodQuery{
			TenantID: tenantID,
			RootID:   parts[4],
			Depth:    2,
			MaxNodes: 100,
			MaxEdges: 200,
		})
		writeOntologyContextResult(w, result, err, cid)
	default:
		writeJSON(w, http.StatusNotFound, ErrorResponse{Code: ErrorNotFound, Message: "resource not found", CorrelationID: cid})
	}
}
