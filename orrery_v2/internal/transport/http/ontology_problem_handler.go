package httptransport

import (
	"context"
	"net/http"
	"strings"
	"time"

	"orrery_v2/internal/app/identity"
	"orrery_v2/internal/app/ontologyscope"
	ontologycontract "orrery_v2/internal/app/ontologyview/contract"
)

type OntologyProblemActorDTO struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
}

type OntologyProblemRefDTO struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
}

type OntologyProblemCaseDTO struct {
	ID                   string                  `json:"id"`
	TenantID             string                  `json:"tenant_id"`
	Fingerprint          string                  `json:"fingerprint"`
	SourceDomain         string                  `json:"source_domain"`
	Category             string                  `json:"category"`
	Severity             string                  `json:"severity"`
	Status               string                  `json:"status"`
	Title                string                  `json:"title"`
	RedactedSummary      string                  `json:"redacted_summary"`
	Owner                OntologyProblemActorDTO `json:"owner"`
	PrimarySource        OntologyProblemRefDTO   `json:"primary_source"`
	SourceRefs           []OntologyProblemRefDTO `json:"source_refs"`
	EvidenceIDs          []string                `json:"evidence_ids"`
	OccurrenceCount      int64                   `json:"occurrence_count"`
	LatestOutcomeID      string                  `json:"latest_outcome_id,omitempty"`
	LatestOutcomeSuccess bool                    `json:"latest_outcome_success"`
	LatestOutcomeSummary string                  `json:"latest_outcome_summary,omitempty"`
	LatestResolution     OntologyProblemRefDTO   `json:"latest_resolution"`
	LessonCandidateID    string                  `json:"lesson_candidate_id,omitempty"`
	Version              int64                   `json:"version"`
	FirstSeenAt          time.Time               `json:"first_seen_at"`
	LastSeenAt           time.Time               `json:"last_seen_at"`
	ResolvedAt           *time.Time              `json:"resolved_at,omitempty"`
	CreatedAt            time.Time               `json:"created_at"`
	UpdatedAt            time.Time               `json:"updated_at"`
}

type OntologyProblemListQuery struct {
	TenantID     string
	Status       string
	OwnerKind    string
	OwnerID      string
	SourceDomain string
	Search       string
	Limit        int
}

type OntologyProblemListDTO struct {
	Items    []OntologyProblemCaseDTO `json:"items"`
	Limit    int                      `json:"limit"`
	Returned int                      `json:"returned"`
	HasMore  bool                     `json:"has_more"`
}

type OntologyProblemRouteRequest struct {
	Owner          OntologyProblemActorDTO `json:"owner"`
	IdempotencyKey string                  `json:"-"`
}

type OntologyProblemService interface {
	ListProblems(context.Context, identity.Principal, OntologyProblemListQuery) (OntologyProblemListDTO, error)
	GetProblem(context.Context, identity.Principal, string, string) (OntologyProblemCaseDTO, error)
	RouteProblem(context.Context, identity.Principal, string, string, OntologyProblemRouteRequest) (OntologyProblemCaseDTO, error)
}

func (r *Router) handleOntologyProblems(w http.ResponseWriter, req *http.Request, parts []string) {
	setOntologyPrivateNoStore(w)
	cid := correlationID(req)
	principal, ok := ontologyPrincipal(req)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, ErrorResponse{Code: ErrorUnauthorized, Message: "authorization is required", CorrelationID: cid})
		return
	}
	if r.ontologyProblems == nil {
		writeOntologyViewError(w, ontologyscope.ErrDependencyUnavailable, cid)
		return
	}
	tenantID := strings.TrimSpace(req.URL.Query().Get("tenant_id"))
	if !validOntologyContextID(tenantID) {
		writeOntologyViewError(w, ontologycontract.ErrInvalidRequest, cid)
		return
	}

	switch {
	case req.Method == http.MethodGet && len(parts) == 3:
		limit, err := optionalOntologyLimit(req.URL.Query().Get("limit"))
		if err != nil {
			writeOntologyViewError(w, err, cid)
			return
		}
		query := OntologyProblemListQuery{
			TenantID: tenantID, Status: req.URL.Query().Get("status"),
			OwnerKind: req.URL.Query().Get("owner_kind"), OwnerID: req.URL.Query().Get("owner_id"),
			SourceDomain: req.URL.Query().Get("source_domain"), Search: req.URL.Query().Get("search"), Limit: limit,
		}
		if !validOntologyProblemListQuery(query) {
			writeOntologyViewError(w, ontologycontract.ErrInvalidRequest, cid)
			return
		}
		result, err := r.ontologyProblems.ListProblems(req.Context(), principal, query)
		writeOntologyContextResult(w, result, err, cid)
	case req.Method == http.MethodGet && len(parts) == 4 && validOntologyContextID(parts[3]):
		result, err := r.ontologyProblems.GetProblem(req.Context(), principal, tenantID, parts[3])
		writeOntologyContextResult(w, result, err, cid)
	case req.Method == http.MethodPost && len(parts) == 5 && parts[4] == "route" && validOntologyContextID(parts[3]):
		if !ontologyMutationOriginAllowed(req) {
			writeJSON(w, http.StatusForbidden, ErrorResponse{Code: ErrorForbidden, Message: "permission denied", CorrelationID: cid})
			return
		}
		var body OntologyProblemRouteRequest
		if err := decodeOntologyMutationBody(req, &body); err != nil || idempotencyKey(req) == "" ||
			!validOntologyContextID(body.Owner.Kind) || !validOntologyContextID(body.Owner.ID) {
			writeOntologyViewError(w, ontologycontract.ErrInvalidRequest, cid)
			return
		}
		body.IdempotencyKey = idempotencyKey(req)
		result, err := r.ontologyProblems.RouteProblem(req.Context(), principal, tenantID, parts[3], body)
		writeOntologyContextResult(w, result, err, cid)
	default:
		writeJSON(w, http.StatusNotFound, ErrorResponse{Code: ErrorNotFound, Message: "resource not found", CorrelationID: cid})
	}
}

func validOntologyProblemListQuery(query OntologyProblemListQuery) bool {
	return validOptionalOntologyContextID(query.Status) && validOptionalOntologyContextID(query.OwnerKind) &&
		validOptionalOntologyContextID(query.OwnerID) && validOptionalOntologyContextID(query.SourceDomain) && validSchemaSearch(query.Search)
}
