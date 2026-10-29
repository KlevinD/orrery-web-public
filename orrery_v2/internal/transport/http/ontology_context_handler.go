package httptransport

import (
	"context"
	"net/http"
	"strings"

	"orrery_v2/internal/app/contextgraph"
	"orrery_v2/internal/app/identity"
	"orrery_v2/internal/app/ontologyscope"
	contextview "orrery_v2/internal/app/ontologyview/context"
	ontologycontract "orrery_v2/internal/app/ontologyview/contract"
)

type OntologyContextService interface {
	CompanyOverview(context.Context, identity.Principal) (ontologycontract.Summary[contextview.CompanyOverviewData], error)
	Overview(context.Context, identity.Principal, string) (ontologycontract.Summary[contextview.CompanyOverviewData], error)
	Summary(context.Context, identity.Principal, string) (ontologycontract.Summary[contextview.SummaryData], error)
	ListObjects(context.Context, identity.Principal, contextview.ObjectListQuery) (ontologycontract.CursorPage[contextview.ObjectItem], error)
	GetObject(context.Context, identity.Principal, string, string) (contextview.ObjectDetail, error)
	ListFacts(context.Context, identity.Principal, contextview.ChildQuery) (ontologycontract.CursorPage[contextview.FactItem], error)
	ListRelations(context.Context, identity.Principal, contextview.RelationListQuery) (ontologycontract.CursorPage[contextview.RelationItem], error)
	ListEvidence(context.Context, identity.Principal, contextview.ChildQuery) (ontologycontract.CursorPage[contextview.EvidenceItem], error)
	Neighborhood(context.Context, identity.Principal, contextview.NeighborhoodQuery) (contextview.Neighborhood, error)
}

func (r *Router) handleOntologyContext(w http.ResponseWriter, req *http.Request, parts []string) {
	setOntologyPrivateNoStore(w)
	cid := correlationID(req)
	principal, ok := ontologyPrincipal(req)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, ErrorResponse{Code: ErrorUnauthorized, Message: "authorization is required", CorrelationID: cid})
		return
	}
	if r.ontologyContext == nil {
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
	case len(parts) == 4 && parts[3] == "company-overview":
		if tenantID != "" {
			writeOntologyViewError(w, ontologycontract.ErrInvalidRequest, cid)
			return
		}
		result, err := r.ontologyContext.CompanyOverview(req.Context(), principal)
		if err != nil && r.logger != nil {
			r.logger.Error(req.Context(), "ontology company overview failed", map[string]any{
				"operation":      "ontology.context.company_overview",
				"correlation_id": cid,
				"error":          err.Error(),
			})
		}
		writeOntologyContextResult(w, result, err, cid)
	case len(parts) == 4 && parts[3] == "overview":
		if tenantID == "" {
			writeOntologyViewError(w, ontologycontract.ErrInvalidRequest, cid)
			return
		}
		result, err := r.ontologyContext.Overview(req.Context(), principal, tenantID)
		writeOntologyContextResult(w, result, err, cid)
	case len(parts) == 4 && parts[3] == "summary":
		result, err := r.ontologyContext.Summary(req.Context(), principal, tenantID)
		writeOntologyContextResult(w, result, err, cid)
	case len(parts) == 4 && parts[3] == "objects":
		limit, err := optionalOntologyLimit(req.URL.Query().Get("limit"))
		if err != nil {
			writeOntologyViewError(w, err, cid)
			return
		}
		query := contextview.ObjectListQuery{
			TenantID: tenantID, ObjectType: req.URL.Query().Get("object_type"), Status: req.URL.Query().Get("status"),
			Source: req.URL.Query().Get("source"), Search: req.URL.Query().Get("search"), Limit: limit, Cursor: req.URL.Query().Get("cursor"),
		}
		if !validOntologyContextObjectQuery(query) {
			writeOntologyViewError(w, ontologycontract.ErrInvalidRequest, cid)
			return
		}
		result, err := r.ontologyContext.ListObjects(req.Context(), principal, query)
		writeOntologyContextResult(w, result, err, cid)
	case len(parts) == 5 && parts[3] == "objects":
		if tenantID == "" || !validOntologyContextID(parts[4]) {
			writeOntologyViewError(w, ontologycontract.ErrInvalidRequest, cid)
			return
		}
		result, err := r.ontologyContext.GetObject(req.Context(), principal, tenantID, parts[4])
		writeOntologyContextResult(w, result, err, cid)
	case len(parts) == 6 && parts[3] == "objects" && parts[5] == "facts":
		if tenantID == "" || !validOntologyContextID(parts[4]) {
			writeOntologyViewError(w, ontologycontract.ErrInvalidRequest, cid)
			return
		}
		query, ok := ontologyContextChildQuery(req, tenantID, parts[4])
		if !ok {
			writeOntologyViewError(w, ontologycontract.ErrInvalidRequest, cid)
			return
		}
		result, err := r.ontologyContext.ListFacts(req.Context(), principal, query)
		writeOntologyContextResult(w, result, err, cid)
	case len(parts) == 6 && parts[3] == "objects" && parts[5] == "relations":
		if tenantID == "" || !validOntologyContextID(parts[4]) {
			writeOntologyViewError(w, ontologycontract.ErrInvalidRequest, cid)
			return
		}
		limit, err := optionalOntologyLimit(req.URL.Query().Get("limit"))
		if err != nil {
			writeOntologyViewError(w, err, cid)
			return
		}
		query := contextview.RelationListQuery{
			TenantID: tenantID, ObjectID: parts[4], Direction: req.URL.Query().Get("direction"), LinkType: req.URL.Query().Get("link_type"), Limit: limit, Cursor: req.URL.Query().Get("cursor"),
		}
		if !validOntologyContextRelationQuery(query) {
			writeOntologyViewError(w, ontologycontract.ErrInvalidRequest, cid)
			return
		}
		result, err := r.ontologyContext.ListRelations(req.Context(), principal, query)
		writeOntologyContextResult(w, result, err, cid)
	case len(parts) == 6 && parts[3] == "objects" && parts[5] == "evidence":
		if tenantID == "" || !validOntologyContextID(parts[4]) {
			writeOntologyViewError(w, ontologycontract.ErrInvalidRequest, cid)
			return
		}
		query, ok := ontologyContextChildQuery(req, tenantID, parts[4])
		if !ok {
			writeOntologyViewError(w, ontologycontract.ErrInvalidRequest, cid)
			return
		}
		result, err := r.ontologyContext.ListEvidence(req.Context(), principal, query)
		writeOntologyContextResult(w, result, err, cid)
	case len(parts) == 4 && parts[3] == "neighborhood":
		if tenantID == "" {
			writeOntologyViewError(w, ontologycontract.ErrInvalidRequest, cid)
			return
		}
		linkTypes, err := parseOntologyContextLinkTypes(req.URL.Query().Get("link_types"))
		if err != nil {
			writeOntologyViewError(w, err, cid)
			return
		}
		depth, err := optionalPositiveInt(req.URL.Query().Get("depth"))
		if err != nil {
			writeOntologyViewError(w, err, cid)
			return
		}
		maxNodes, err := optionalPositiveInt(req.URL.Query().Get("max_nodes"))
		if err != nil {
			writeOntologyViewError(w, err, cid)
			return
		}
		maxEdges, err := optionalPositiveInt(req.URL.Query().Get("max_edges"))
		if err != nil {
			writeOntologyViewError(w, err, cid)
			return
		}
		query := contextview.NeighborhoodQuery{
			TenantID: tenantID, RootID: req.URL.Query().Get("root_id"), LinkType: req.URL.Query().Get("link_type"),
			LinkTypes: linkTypes, Depth: depth, MaxNodes: maxNodes, MaxEdges: maxEdges,
		}
		if !validOntologyContextNeighborhoodQuery(query) {
			writeOntologyViewError(w, ontologycontract.ErrInvalidRequest, cid)
			return
		}
		result, err := r.ontologyContext.Neighborhood(req.Context(), principal, query)
		writeOntologyContextResult(w, result, err, cid)
	default:
		writeJSON(w, http.StatusNotFound, ErrorResponse{Code: ErrorNotFound, Message: "resource not found", CorrelationID: cid})
	}
}

func ontologyContextChildQuery(req *http.Request, tenantID, objectID string) (contextview.ChildQuery, bool) {
	limit, err := optionalOntologyLimit(req.URL.Query().Get("limit"))
	if err != nil {
		return contextview.ChildQuery{}, false
	}
	return contextview.ChildQuery{TenantID: tenantID, ObjectID: objectID, Limit: limit, Cursor: req.URL.Query().Get("cursor")}, true
}

func validOntologyContextObjectQuery(query contextview.ObjectListQuery) bool {
	return validOptionalOntologyContextID(query.ObjectType) && validOptionalOntologyContextID(query.Status) && validOptionalOntologyContextID(query.Source) && validSchemaSearch(query.Search)
}

func validOntologyContextRelationQuery(query contextview.RelationListQuery) bool {
	direction := strings.ToLower(strings.TrimSpace(query.Direction))
	return (direction == "" || direction == "in" || direction == "out" || direction == "both") && validOptionalOntologyContextID(query.LinkType)
}

func validOntologyContextNeighborhoodQuery(query contextview.NeighborhoodQuery) bool {
	return validOntologyContextID(strings.TrimSpace(query.RootID)) && validOptionalOntologyContextID(query.LinkType) && !(strings.TrimSpace(query.LinkType) != "" && len(query.LinkTypes) > 0) &&
		len(query.LinkTypes) <= contextgraph.MaxNeighborhoodLinkTypes && validOntologyContextLinkTypes(query.LinkTypes) &&
		(query.Depth == 0 || query.Depth <= 2) && (query.MaxNodes == 0 || query.MaxNodes <= 200) && (query.MaxEdges == 0 || query.MaxEdges <= 400)
}

func parseOntologyContextLinkTypes(raw string) ([]string, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	parts := strings.Split(raw, ",")
	if len(parts) > contextgraph.MaxNeighborhoodLinkTypes {
		return nil, ontologycontract.ErrInvalidRequest
	}
	for index := range parts {
		parts[index] = strings.TrimSpace(parts[index])
		if !validOntologyContextID(parts[index]) {
			return nil, ontologycontract.ErrInvalidRequest
		}
	}
	return parts, nil
}

func validOntologyContextLinkTypes(values []string) bool {
	for _, value := range values {
		if !validOntologyContextID(value) {
			return false
		}
	}
	return true
}

func validOntologyContextID(value string) bool {
	value = strings.TrimSpace(value)
	return value != "" && len(value) <= 256
}

func validOptionalOntologyContextID(value string) bool {
	value = strings.TrimSpace(value)
	return value == "" || len(value) <= 256
}

func writeOntologyContextResult(w http.ResponseWriter, result any, err error, cid string) {
	if err != nil {
		writeOntologyViewError(w, err, cid)
		return
	}
	writeJSON(w, http.StatusOK, result)
}
