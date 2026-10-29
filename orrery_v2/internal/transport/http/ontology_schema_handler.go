package httptransport

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"orrery_v2/internal/app/identity"
	"orrery_v2/internal/app/ontologyscope"
	ontologycontract "orrery_v2/internal/app/ontologyview/contract"
	ontologyschema "orrery_v2/internal/app/ontologyview/schema"
)

type OntologySchemaService interface {
	Summary(context.Context, identity.Principal, string) (ontologycontract.Summary[ontologyschema.SummaryData], error)
	ListDefinitions(context.Context, identity.Principal, ontologyschema.DefinitionListQuery) (ontologycontract.CursorPage[ontologyschema.DefinitionItem], error)
	GetDefinition(context.Context, identity.Principal, string, ontologyschema.DefinitionKind, string) (ontologyschema.DefinitionDetail, error)
	ListReferences(context.Context, identity.Principal, ontologyschema.ReferenceQuery) (ontologycontract.CursorPage[ontologyschema.DefinitionReference], error)
	Graph(context.Context, identity.Principal, ontologyschema.GraphQuery) (ontologyschema.Graph, error)
}

func (r *Router) handleOntologySchema(w http.ResponseWriter, req *http.Request, parts []string) {
	setOntologyPrivateNoStore(w)
	cid := correlationID(req)
	principal, ok := ontologyPrincipal(req)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, ErrorResponse{Code: ErrorUnauthorized, Message: "authorization is required", CorrelationID: cid})
		return
	}
	if r.ontologySchema == nil {
		writeOntologyViewError(w, ontologyscope.ErrDependencyUnavailable, cid)
		return
	}
	tenantID := strings.TrimSpace(req.URL.Query().Get("tenant_id"))
	if !validOptionalOntologyTenantID(tenantID) {
		writeOntologyViewError(w, ontologycontract.ErrInvalidRequest, cid)
		return
	}
	switch {
	case req.Method == http.MethodGet && len(parts) == 4 && parts[3] == "summary":
		result, err := r.ontologySchema.Summary(req.Context(), principal, tenantID)
		writeOntologySchemaResult(w, result, err, cid)
	case req.Method == http.MethodGet && len(parts) == 4 && parts[3] == "definitions":
		limit, err := optionalOntologyLimit(req.URL.Query().Get("limit"))
		if err != nil {
			writeOntologyViewError(w, err, cid)
			return
		}
		query := ontologyschema.DefinitionListQuery{
			TenantID: tenantID, Kind: ontologyschema.DefinitionKind(req.URL.Query().Get("kind")), Origin: ontologyschema.DefinitionOrigin(req.URL.Query().Get("origin")), Status: ontologyschema.DefinitionStatus(req.URL.Query().Get("status")), Search: req.URL.Query().Get("search"), Limit: limit, Cursor: req.URL.Query().Get("cursor"),
		}
		if !validSchemaListWireQuery(query) {
			writeOntologyViewError(w, ontologycontract.ErrInvalidRequest, cid)
			return
		}
		result, err := r.ontologySchema.ListDefinitions(req.Context(), principal, query)
		writeOntologySchemaResult(w, result, err, cid)
	case req.Method == http.MethodGet && len(parts) == 6 && parts[3] == "definitions":
		kind := ontologyschema.DefinitionKind(parts[4])
		if tenantID == "" || !validSchemaDefinitionKind(kind) || !validSchemaPathID(parts[5]) {
			writeOntologyViewError(w, ontologycontract.ErrInvalidRequest, cid)
			return
		}
		result, err := r.ontologySchema.GetDefinition(req.Context(), principal, tenantID, kind, parts[5])
		writeOntologySchemaResult(w, result, err, cid)
	case req.Method == http.MethodGet && len(parts) == 7 && parts[3] == "definitions" && parts[6] == "references":
		if tenantID == "" {
			writeOntologyViewError(w, ontologycontract.ErrInvalidRequest, cid)
			return
		}
		limit, err := optionalOntologyLimit(req.URL.Query().Get("limit"))
		if err != nil {
			writeOntologyViewError(w, err, cid)
			return
		}
		query := ontologyschema.ReferenceQuery{TenantID: tenantID, Kind: ontologyschema.DefinitionKind(parts[4]), DefinitionID: parts[5], Direction: req.URL.Query().Get("direction"), Limit: limit, Cursor: req.URL.Query().Get("cursor")}
		if !validSchemaReferenceWireQuery(query) {
			writeOntologyViewError(w, ontologycontract.ErrInvalidRequest, cid)
			return
		}
		result, err := r.ontologySchema.ListReferences(req.Context(), principal, query)
		writeOntologySchemaResult(w, result, err, cid)
	case req.Method == http.MethodGet && len(parts) == 4 && parts[3] == "graph":
		maxNodes, err := optionalPositiveInt(req.URL.Query().Get("max_nodes"))
		if err != nil {
			writeOntologyViewError(w, err, cid)
			return
		}
		query := ontologyschema.GraphQuery{TenantID: tenantID, View: ontologyschema.GraphView(req.URL.Query().Get("view")), Search: req.URL.Query().Get("search"), MaxNodes: maxNodes}
		if !validSchemaGraphWireQuery(query) {
			writeOntologyViewError(w, ontologycontract.ErrInvalidRequest, cid)
			return
		}
		result, err := r.ontologySchema.Graph(req.Context(), principal, query)
		writeOntologySchemaResult(w, result, err, cid)
	default:
		writeJSON(w, http.StatusNotFound, ErrorResponse{Code: ErrorNotFound, Message: "resource not found", CorrelationID: cid})
	}
}

func validOptionalOntologyTenantID(value string) bool {
	value = strings.TrimSpace(value)
	return value == "" || len(value) <= 256
}

func optionalPositiveInt(raw string) (int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value <= 0 {
		return 0, ontologycontract.ErrInvalidRequest
	}
	return value, nil
}

func validSchemaListWireQuery(query ontologyschema.DefinitionListQuery) bool {
	return (query.Kind == "" || validSchemaDefinitionKind(query.Kind)) &&
		(query.Origin == "" || query.Origin == ontologyschema.OriginSystem || query.Origin == ontologyschema.OriginTenant) &&
		(query.Status == "" || query.Status == ontologyschema.StatusActive || query.Status == ontologyschema.StatusDisabled) &&
		validSchemaSearch(query.Search)
}

func validSchemaReferenceWireQuery(query ontologyschema.ReferenceQuery) bool {
	direction := strings.ToLower(strings.TrimSpace(query.Direction))
	return validSchemaDefinitionKind(query.Kind) && validSchemaPathID(query.DefinitionID) &&
		(direction == "" || direction == "in" || direction == "out" || direction == "both")
}

func validSchemaGraphWireQuery(query ontologyschema.GraphQuery) bool {
	view := strings.ToLower(strings.TrimSpace(string(query.View)))
	return (view == "" || view == string(ontologyschema.GraphSemantic) || view == string(ontologyschema.GraphProjection)) &&
		(query.MaxNodes == 0 || query.MaxNodes <= 500) && validSchemaSearch(query.Search)
}

func validSchemaDefinitionKind(kind ontologyschema.DefinitionKind) bool {
	switch ontologyschema.DefinitionKind(strings.ToLower(strings.TrimSpace(string(kind)))) {
	case ontologyschema.KindObject, ontologyschema.KindLink, ontologyschema.KindProperty, ontologyschema.KindMapping, ontologyschema.KindAction:
		return true
	default:
		return false
	}
}

func validSchemaPathID(id string) bool {
	id = strings.TrimSpace(id)
	return id != "" && len(id) <= 256
}

func validSchemaSearch(search string) bool {
	return utf8.ValidString(search) && utf8.RuneCountInString(strings.TrimSpace(search)) <= 128
}

func writeOntologySchemaResult(w http.ResponseWriter, result any, err error, cid string) {
	if err != nil {
		writeOntologyViewError(w, err, cid)
		return
	}
	writeJSON(w, http.StatusOK, result)
}
