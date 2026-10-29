package main

import (
	"context"
	"errors"
	"strings"

	"orrery_v2/internal/app/identity"
	"orrery_v2/internal/app/ontologyscope"
	ontologycontract "orrery_v2/internal/app/ontologyview/contract"
	"orrery_v2/internal/app/problemhub"
	httptransport "orrery_v2/internal/transport/http"
)

const defaultProblemListLimit = 50

type productionOntologyProblemAPI struct {
	control *problemhub.Service
	gate    *ontologyscope.ScopeAccessGate
}

func newProductionOntologyProblemAPI(control *problemhub.Service, gate *ontologyscope.ScopeAccessGate) *productionOntologyProblemAPI {
	return &productionOntologyProblemAPI{control: control, gate: gate}
}

func (a *productionOntologyProblemAPI) ListProblems(ctx context.Context, principal identity.Principal, query httptransport.OntologyProblemListQuery) (httptransport.OntologyProblemListDTO, error) {
	if err := a.authorize(ctx, principal, query.TenantID, false); err != nil {
		return httptransport.OntologyProblemListDTO{}, err
	}
	limit := query.Limit
	if limit == 0 {
		limit = defaultProblemListLimit
	}
	if limit < 1 || limit > ontologycontract.MaxPageLimit {
		return httptransport.OntologyProblemListDTO{}, ontologycontract.ErrInvalidRequest
	}
	problems, err := a.control.List(ctx, problemhub.ListQuery{
		TenantID: strings.TrimSpace(query.TenantID), Status: problemhub.Status(strings.ToLower(strings.TrimSpace(query.Status))),
		OwnerKind: query.OwnerKind, OwnerID: query.OwnerID, SourceDomain: query.SourceDomain, Search: query.Search,
		Limit: limit + 1,
	})
	if err != nil {
		return httptransport.OntologyProblemListDTO{}, ontologyProblemError(err)
	}
	hasMore := len(problems) > limit
	if hasMore {
		problems = problems[:limit]
	}
	items := make([]httptransport.OntologyProblemCaseDTO, len(problems))
	for index := range problems {
		items[index] = ontologyProblemCaseDTO(problems[index])
	}
	return httptransport.OntologyProblemListDTO{Items: items, Limit: limit, Returned: len(items), HasMore: hasMore}, nil
}

func (a *productionOntologyProblemAPI) GetProblem(ctx context.Context, principal identity.Principal, tenantID, problemID string) (httptransport.OntologyProblemCaseDTO, error) {
	if err := a.authorize(ctx, principal, tenantID, false); err != nil {
		return httptransport.OntologyProblemCaseDTO{}, err
	}
	problem, err := a.control.Get(ctx, strings.TrimSpace(tenantID), strings.TrimSpace(problemID))
	if err != nil {
		return httptransport.OntologyProblemCaseDTO{}, ontologyProblemError(err)
	}
	return ontologyProblemCaseDTO(problem), nil
}

func (a *productionOntologyProblemAPI) RouteProblem(ctx context.Context, principal identity.Principal, tenantID, problemID string, request httptransport.OntologyProblemRouteRequest) (httptransport.OntologyProblemCaseDTO, error) {
	if err := a.authorize(ctx, principal, tenantID, true); err != nil {
		return httptransport.OntologyProblemCaseDTO{}, err
	}
	actor, err := problemHubActor(principal)
	if err != nil {
		return httptransport.OntologyProblemCaseDTO{}, ontologycontract.ErrInvalidRequest
	}
	problem, err := a.control.Route(ctx, problemhub.RouteCommand{
		ID: strings.TrimSpace(request.IdempotencyKey), TenantID: strings.TrimSpace(tenantID), ProblemCaseID: strings.TrimSpace(problemID),
		Owner: problemhub.ActorRef{Kind: request.Owner.Kind, ID: request.Owner.ID}, Actor: actor,
	})
	if err != nil {
		return httptransport.OntologyProblemCaseDTO{}, ontologyProblemError(err)
	}
	return ontologyProblemCaseDTO(problem), nil
}

func (a *productionOntologyProblemAPI) authorize(ctx context.Context, principal identity.Principal, tenantID string, manage bool) error {
	if a == nil || a.control == nil || a.gate == nil || strings.TrimSpace(tenantID) == "" {
		return ontologyscope.ErrDependencyUnavailable
	}
	var err error
	if manage {
		_, err = a.gate.AuthorizeManage(ctx, identity.NormalizePrincipal(principal), strings.TrimSpace(tenantID))
	} else {
		_, err = a.gate.AuthorizeRead(ctx, identity.NormalizePrincipal(principal), strings.TrimSpace(tenantID))
	}
	if !manage && ontologyscope.IsNonDisclosingReadError(err) {
		return ontologycontract.ErrUnavailable
	}
	return err
}

func problemHubActor(principal identity.Principal) (problemhub.ActorRef, error) {
	principal = identity.NormalizePrincipal(principal)
	actor := problemhub.ActorRef{ID: firstNonBlank(principal.UserID, principal.AgentProfileID, principal.Username, principal.Subject)}
	switch principal.ViewerKind {
	case identity.ViewerKindUser:
		actor.Kind = "human"
	case identity.ViewerKindAgent:
		actor.Kind = "agent"
	case identity.ViewerKindSystem:
		actor.Kind = "service"
	default:
		return problemhub.ActorRef{}, ontologycontract.ErrInvalidRequest
	}
	if actor.ID == "" {
		return problemhub.ActorRef{}, ontologycontract.ErrInvalidRequest
	}
	return actor, nil
}

func ontologyProblemCaseDTO(problem problemhub.ProblemCase) httptransport.OntologyProblemCaseDTO {
	sources := make([]httptransport.OntologyProblemRefDTO, len(problem.SourceRefs))
	for index, source := range problem.SourceRefs {
		sources[index] = httptransport.OntologyProblemRefDTO{Kind: source.Kind, ID: source.ID}
	}
	return httptransport.OntologyProblemCaseDTO{
		ID: problem.ID, TenantID: problem.TenantID, Fingerprint: problem.Fingerprint,
		SourceDomain: problem.SourceDomain, Category: problem.Category, Severity: problem.Severity,
		Status: string(problem.Status), Title: problem.Title, RedactedSummary: problem.RedactedSummary,
		Owner:         httptransport.OntologyProblemActorDTO{Kind: problem.Owner.Kind, ID: problem.Owner.ID},
		PrimarySource: httptransport.OntologyProblemRefDTO{Kind: problem.PrimarySource.Kind, ID: problem.PrimarySource.ID},
		SourceRefs:    sources, EvidenceIDs: append([]string(nil), problem.EvidenceIDs...), OccurrenceCount: problem.OccurrenceCount,
		LatestOutcomeID: problem.LatestOutcomeID, LatestOutcomeSuccess: problem.LatestOutcomeSuccess,
		LatestOutcomeSummary: problem.LatestOutcomeSummary,
		LatestResolution:     httptransport.OntologyProblemRefDTO{Kind: problem.LatestResolution.Kind, ID: problem.LatestResolution.ID},
		LessonCandidateID:    problem.LessonCandidateID, Version: problem.Version,
		FirstSeenAt: problem.FirstSeenAt, LastSeenAt: problem.LastSeenAt, ResolvedAt: problem.ResolvedAt,
		CreatedAt: problem.CreatedAt, UpdatedAt: problem.UpdatedAt,
	}
}

func ontologyProblemError(err error) error {
	switch {
	case errors.Is(err, problemhub.ErrInvalidRequest):
		return ontologycontract.ErrInvalidRequest
	case errors.Is(err, problemhub.ErrNotFound):
		return ontologycontract.ErrUnavailable
	case errors.Is(err, problemhub.ErrConflict):
		return ontologycontract.ErrStateConflict
	default:
		return err
	}
}

var _ httptransport.OntologyProblemService = (*productionOntologyProblemAPI)(nil)
