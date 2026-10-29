package aggregate

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"orrery_v2/internal/app/contextgraph"
	"orrery_v2/internal/app/datavisibility"
	"orrery_v2/internal/app/identity"
	"orrery_v2/internal/app/ontologyscope"
	contextview "orrery_v2/internal/app/ontologyview/context"
	ontologycontract "orrery_v2/internal/app/ontologyview/contract"
)

type ContextReader interface {
	CompanyScopeTenantIDs(context.Context, int) (contextgraph.ContextTenantPage, error)
	Summary(context.Context, identity.Principal, string) (ontologycontract.Summary[contextview.SummaryData], error)
	Overview(context.Context, identity.Principal, string) (ontologycontract.Summary[contextview.CompanyOverviewData], error)
	ListObjects(context.Context, identity.Principal, contextview.ObjectListQuery) (ontologycontract.CursorPage[contextview.ObjectItem], error)
	GetObject(context.Context, identity.Principal, string, string) (contextview.ObjectDetail, error)
	ListFacts(context.Context, identity.Principal, contextview.ChildQuery) (ontologycontract.CursorPage[contextview.FactItem], error)
	ListRelations(context.Context, identity.Principal, contextview.RelationListQuery) (ontologycontract.CursorPage[contextview.RelationItem], error)
	ListEvidence(context.Context, identity.Principal, contextview.ChildQuery) (ontologycontract.CursorPage[contextview.EvidenceItem], error)
	Neighborhood(context.Context, identity.Principal, contextview.NeighborhoodQuery) (contextview.Neighborhood, error)
}

type ContextService struct {
	scopes   *ScopeEnumerator
	delegate ContextReader
}

func NewContextService(scopes *ScopeEnumerator, delegate ContextReader) (*ContextService, error) {
	if scopes == nil || delegate == nil {
		return nil, ontologyscope.ErrDependencyUnavailable
	}
	return &ContextService{scopes: scopes, delegate: delegate}, nil
}

func (s *ContextService) CompanyOverview(ctx context.Context, principal identity.Principal) (ontologycontract.Summary[contextview.CompanyOverviewData], error) {
	result, err := s.companyOverview(ctx, principal)
	if err == nil || ctx.Err() != nil || !errors.Is(err, ontologyscope.ErrDependencyUnavailable) {
		return result, err
	}
	// Authorization and policy revisions can legitimately advance while the
	// bounded company-wide read is running. Retry the complete read once with a
	// fresh request memo so the response is still based on one coherent snapshot.
	return s.companyOverview(ctx, principal)
}

func (s *ContextService) companyOverview(ctx context.Context, principal identity.Principal) (ontologycontract.Summary[contextview.CompanyOverviewData], error) {
	ctx = datavisibility.WithFreshRequestMemo(ctx)
	candidates, err := s.delegate.CompanyScopeTenantIDs(ctx, MaxVisibleScopes)
	if err != nil {
		return ontologycontract.Summary[contextview.CompanyOverviewData]{}, err
	}
	if candidates.HasMore {
		return ontologycontract.Summary[contextview.CompanyOverviewData]{}, ontologycontract.ErrQueryBudgetExceeded
	}
	scopes, err := s.scopes.DiscoverCompanyOverview(ctx, principal, candidates.TenantIDs)
	if err != nil {
		return ontologycontract.Summary[contextview.CompanyOverviewData]{}, err
	}
	result, err := aggregateCompanyOverview(ctx, scopes, func(ctx context.Context, tenantID string) (ontologycontract.Summary[contextview.CompanyOverviewData], error) {
		return s.delegate.Overview(ctx, principal, tenantID)
	})
	if err != nil {
		return ontologycontract.Summary[contextview.CompanyOverviewData]{}, err
	}
	if err := datavisibility.RequestMemoFromContext(ctx).Validate(ctx); err != nil {
		return ontologycontract.Summary[contextview.CompanyOverviewData]{}, fmt.Errorf("%w: %v", ontologyscope.ErrDependencyUnavailable, err)
	}
	return result, nil
}

func (s *ContextService) Overview(ctx context.Context, principal identity.Principal, tenantID string) (ontologycontract.Summary[contextview.CompanyOverviewData], error) {
	tenantID, err := requireConcreteTenant(tenantID)
	if err != nil {
		return ontologycontract.Summary[contextview.CompanyOverviewData]{}, err
	}
	return s.delegate.Overview(ctx, principal, tenantID)
}

func (s *ContextService) Summary(ctx context.Context, principal identity.Principal, tenantID string) (ontologycontract.Summary[contextview.SummaryData], error) {
	if strings.TrimSpace(tenantID) != "" {
		return s.delegate.Summary(ctx, principal, tenantID)
	}
	scopes, err := s.scopes.Discover(ctx, principal)
	if err != nil {
		return ontologycontract.Summary[contextview.SummaryData]{}, err
	}
	return aggregateContextSummary(ctx, scopes, func(ctx context.Context, tenantID string) (ontologycontract.Summary[contextview.SummaryData], error) {
		return s.delegate.Summary(ctx, principal, tenantID)
	})
}

func (s *ContextService) ListObjects(ctx context.Context, principal identity.Principal, query contextview.ObjectListQuery) (ontologycontract.CursorPage[contextview.ObjectItem], error) {
	if strings.TrimSpace(query.TenantID) != "" {
		return s.delegate.ListObjects(ctx, principal, query)
	}
	query.ObjectType = strings.ToLower(strings.TrimSpace(query.ObjectType))
	query.Status = strings.ToLower(strings.TrimSpace(query.Status))
	query.Source = strings.ToLower(strings.TrimSpace(query.Source))
	query.Search = strings.TrimSpace(query.Search)
	query.Cursor = strings.TrimSpace(query.Cursor)
	limit, err := ontologycontract.NormalizeLimit(query.Limit, ontologycontract.DefaultListLimit, ontologycontract.MaxPageLimit)
	if err != nil || !validOptionalAggregateID(query.ObjectType) || !validOptionalAggregateID(query.Status) || !validOptionalAggregateID(query.Source) || !utf8.ValidString(query.Search) || utf8.RuneCountInString(query.Search) > 128 || len(query.Cursor) > 4096 {
		return ontologycontract.CursorPage[contextview.ObjectItem]{}, ontologycontract.ErrInvalidRequest
	}
	key, err := aggregateQueryKey("context-objects", struct {
		ObjectType string `json:"object_type"`
		Status     string `json:"status"`
		Source     string `json:"source"`
		Search     string `json:"search"`
		Limit      int    `json:"limit"`
	}{query.ObjectType, query.Status, query.Source, strings.ToLower(query.Search), limit})
	if err != nil {
		return ontologycontract.CursorPage[contextview.ObjectItem]{}, err
	}
	scopes, err := s.scopes.Discover(ctx, principal)
	if err != nil {
		return ontologycontract.CursorPage[contextview.ObjectItem]{}, err
	}
	return aggregateCursorPage(ctx, s.scopes, principal, scopes, key, query.Cursor, limit,
		func(ctx context.Context, tenantID string, limit int, cursor string) (ontologycontract.CursorPage[contextview.ObjectItem], error) {
			inner := query
			inner.TenantID, inner.Limit, inner.Cursor = tenantID, limit, cursor
			return s.delegate.ListObjects(ctx, principal, inner)
		},
		func(tenantID string, item contextview.ObjectItem) contextview.ObjectItem {
			item.TenantID = tenantID
			return item
		},
	)
}

func (s *ContextService) GetObject(ctx context.Context, principal identity.Principal, tenantID, objectID string) (contextview.ObjectDetail, error) {
	tenantID, err := requireConcreteTenant(tenantID)
	if err != nil {
		return contextview.ObjectDetail{}, err
	}
	return s.delegate.GetObject(ctx, principal, tenantID, objectID)
}

func (s *ContextService) ListFacts(ctx context.Context, principal identity.Principal, query contextview.ChildQuery) (ontologycontract.CursorPage[contextview.FactItem], error) {
	tenantID, err := requireConcreteTenant(query.TenantID)
	if err != nil {
		return ontologycontract.CursorPage[contextview.FactItem]{}, err
	}
	query.TenantID = tenantID
	return s.delegate.ListFacts(ctx, principal, query)
}

func (s *ContextService) ListRelations(ctx context.Context, principal identity.Principal, query contextview.RelationListQuery) (ontologycontract.CursorPage[contextview.RelationItem], error) {
	tenantID, err := requireConcreteTenant(query.TenantID)
	if err != nil {
		return ontologycontract.CursorPage[contextview.RelationItem]{}, err
	}
	query.TenantID = tenantID
	return s.delegate.ListRelations(ctx, principal, query)
}

func (s *ContextService) ListEvidence(ctx context.Context, principal identity.Principal, query contextview.ChildQuery) (ontologycontract.CursorPage[contextview.EvidenceItem], error) {
	tenantID, err := requireConcreteTenant(query.TenantID)
	if err != nil {
		return ontologycontract.CursorPage[contextview.EvidenceItem]{}, err
	}
	query.TenantID = tenantID
	return s.delegate.ListEvidence(ctx, principal, query)
}

func (s *ContextService) Neighborhood(ctx context.Context, principal identity.Principal, query contextview.NeighborhoodQuery) (contextview.Neighborhood, error) {
	tenantID, err := requireConcreteTenant(query.TenantID)
	if err != nil {
		return contextview.Neighborhood{}, err
	}
	query.TenantID = tenantID
	return s.delegate.Neighborhood(ctx, principal, query)
}

func validOptionalAggregateID(value string) bool {
	return value == "" || len(value) <= 256
}
