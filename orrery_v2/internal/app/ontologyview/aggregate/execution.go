package aggregate

import (
	"context"
	"strings"
	"unicode/utf8"

	"orrery_v2/internal/app/identity"
	"orrery_v2/internal/app/ontologyscope"
	contextview "orrery_v2/internal/app/ontologyview/context"
	ontologycontract "orrery_v2/internal/app/ontologyview/contract"
)

type ExecutionReader interface {
	ListTasks(context.Context, identity.Principal, contextview.ObjectListQuery) (ontologycontract.CursorPage[contextview.ObjectItem], error)
	GetTask(context.Context, identity.Principal, string, string) (contextview.ObjectDetail, error)
	GetRun(context.Context, identity.Principal, string, string) (contextview.ObjectDetail, error)
	RunLineage(context.Context, identity.Principal, contextview.NeighborhoodQuery) (contextview.Neighborhood, error)
}

type ExecutionService struct {
	scopes   *ScopeEnumerator
	delegate ExecutionReader
}

func NewExecutionService(scopes *ScopeEnumerator, delegate ExecutionReader) (*ExecutionService, error) {
	if scopes == nil || delegate == nil {
		return nil, ontologyscope.ErrDependencyUnavailable
	}
	return &ExecutionService{scopes: scopes, delegate: delegate}, nil
}

func (s *ExecutionService) ListTasks(ctx context.Context, principal identity.Principal, query contextview.ObjectListQuery) (ontologycontract.CursorPage[contextview.ObjectItem], error) {
	if strings.TrimSpace(query.TenantID) != "" {
		return s.delegate.ListTasks(ctx, principal, query)
	}
	query.Status = strings.ToLower(strings.TrimSpace(query.Status))
	query.Search = strings.TrimSpace(query.Search)
	query.Cursor = strings.TrimSpace(query.Cursor)
	limit, err := ontologycontract.NormalizeLimit(query.Limit, ontologycontract.DefaultListLimit, ontologycontract.MaxPageLimit)
	if err != nil || !validOptionalAggregateID(query.Status) || !utf8.ValidString(query.Search) || utf8.RuneCountInString(query.Search) > 128 || len(query.Cursor) > 4096 {
		return ontologycontract.CursorPage[contextview.ObjectItem]{}, ontologycontract.ErrInvalidRequest
	}
	key, err := aggregateQueryKey("execution-tasks", struct {
		Status string `json:"status"`
		Search string `json:"search"`
		Limit  int    `json:"limit"`
	}{query.Status, strings.ToLower(query.Search), limit})
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
			return s.delegate.ListTasks(ctx, principal, inner)
		},
		func(tenantID string, item contextview.ObjectItem) contextview.ObjectItem {
			item.TenantID = tenantID
			return item
		},
	)
}

func (s *ExecutionService) GetTask(ctx context.Context, principal identity.Principal, tenantID, taskID string) (contextview.ObjectDetail, error) {
	tenantID, err := requireConcreteTenant(tenantID)
	if err != nil {
		return contextview.ObjectDetail{}, err
	}
	return s.delegate.GetTask(ctx, principal, tenantID, taskID)
}

func (s *ExecutionService) GetRun(ctx context.Context, principal identity.Principal, tenantID, runID string) (contextview.ObjectDetail, error) {
	tenantID, err := requireConcreteTenant(tenantID)
	if err != nil {
		return contextview.ObjectDetail{}, err
	}
	return s.delegate.GetRun(ctx, principal, tenantID, runID)
}

func (s *ExecutionService) RunLineage(ctx context.Context, principal identity.Principal, query contextview.NeighborhoodQuery) (contextview.Neighborhood, error) {
	tenantID, err := requireConcreteTenant(query.TenantID)
	if err != nil {
		return contextview.Neighborhood{}, err
	}
	query.TenantID = tenantID
	return s.delegate.RunLineage(ctx, principal, query)
}
