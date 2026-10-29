package aggregate

import (
	"context"
	"strings"
	"unicode/utf8"

	"orrery_v2/internal/app/identity"
	"orrery_v2/internal/app/ontologyscope"
	ontologycontract "orrery_v2/internal/app/ontologyview/contract"
	projectionview "orrery_v2/internal/app/ontologyview/projection"
)

type ProjectionReader interface {
	Summary(context.Context, identity.Principal, string) (ontologycontract.Summary[projectionview.SummaryData], error)
	ListAudits(context.Context, identity.Principal, projectionview.AuditListQuery) (ontologycontract.CursorPage[projectionview.AuditItem], error)
	GetAudit(context.Context, identity.Principal, string, string) (projectionview.AuditDetail, error)
	ListReplayHistory(context.Context, identity.Principal, projectionview.ReplayHistoryListQuery) (ontologycontract.CursorPage[projectionview.ReplayHistoryItem], error)
}

type ProjectionService struct {
	scopes   *ScopeEnumerator
	delegate ProjectionReader
}

func NewProjectionService(scopes *ScopeEnumerator, delegate ProjectionReader) (*ProjectionService, error) {
	if scopes == nil || delegate == nil {
		return nil, ontologyscope.ErrDependencyUnavailable
	}
	return &ProjectionService{scopes: scopes, delegate: delegate}, nil
}

func (s *ProjectionService) Summary(ctx context.Context, principal identity.Principal, tenantID string) (ontologycontract.Summary[projectionview.SummaryData], error) {
	if strings.TrimSpace(tenantID) != "" {
		return s.delegate.Summary(ctx, principal, tenantID)
	}
	scopes, err := s.scopes.Discover(ctx, principal)
	if err != nil {
		return ontologycontract.Summary[projectionview.SummaryData]{}, err
	}
	return aggregateProjectionSummary(ctx, scopes, func(ctx context.Context, tenantID string) (ontologycontract.Summary[projectionview.SummaryData], error) {
		return s.delegate.Summary(ctx, principal, tenantID)
	})
}

func (s *ProjectionService) ListAudits(ctx context.Context, principal identity.Principal, query projectionview.AuditListQuery) (ontologycontract.CursorPage[projectionview.AuditItem], error) {
	if strings.TrimSpace(query.TenantID) != "" {
		return s.delegate.ListAudits(ctx, principal, query)
	}
	query.Status = strings.ToLower(strings.TrimSpace(query.Status))
	query.ReasonCode = strings.ToLower(strings.TrimSpace(query.ReasonCode))
	query.Cursor = strings.TrimSpace(query.Cursor)
	limit, err := ontologycontract.NormalizeLimit(query.Limit, ontologycontract.DefaultListLimit, ontologycontract.MaxPageLimit)
	if err != nil || !validAuditStatus(query.Status) || !utf8.ValidString(query.ReasonCode) || len(query.ReasonCode) > 256 || len(query.Cursor) > 4096 {
		return ontologycontract.CursorPage[projectionview.AuditItem]{}, ontologycontract.ErrInvalidRequest
	}
	key, err := aggregateQueryKey("projection-audits", struct {
		Status     string `json:"status"`
		ReasonCode string `json:"reason_code"`
		Limit      int    `json:"limit"`
	}{query.Status, query.ReasonCode, limit})
	if err != nil {
		return ontologycontract.CursorPage[projectionview.AuditItem]{}, err
	}
	scopes, err := s.scopes.Discover(ctx, principal)
	if err != nil {
		return ontologycontract.CursorPage[projectionview.AuditItem]{}, err
	}
	return aggregateCursorPage(ctx, s.scopes, principal, scopes, key, query.Cursor, limit,
		func(ctx context.Context, tenantID string, limit int, cursor string) (ontologycontract.CursorPage[projectionview.AuditItem], error) {
			inner := query
			inner.TenantID, inner.Limit, inner.Cursor = tenantID, limit, cursor
			return s.delegate.ListAudits(ctx, principal, inner)
		},
		func(tenantID string, item projectionview.AuditItem) projectionview.AuditItem {
			item.TenantID = tenantID
			return item
		},
	)
}

func validAuditStatus(status string) bool {
	switch status {
	case "", "applied", "skipped", "replayed", "acknowledged", "failed":
		return true
	default:
		return false
	}
}

func (s *ProjectionService) GetAudit(ctx context.Context, principal identity.Principal, tenantID, auditID string) (projectionview.AuditDetail, error) {
	tenantID, err := requireConcreteTenant(tenantID)
	if err != nil {
		return projectionview.AuditDetail{}, err
	}
	return s.delegate.GetAudit(ctx, principal, tenantID, auditID)
}

func (s *ProjectionService) ListReplayHistory(ctx context.Context, principal identity.Principal, query projectionview.ReplayHistoryListQuery) (ontologycontract.CursorPage[projectionview.ReplayHistoryItem], error) {
	tenantID, err := requireConcreteTenant(query.TenantID)
	if err != nil {
		return ontologycontract.CursorPage[projectionview.ReplayHistoryItem]{}, err
	}
	query.TenantID = tenantID
	return s.delegate.ListReplayHistory(ctx, principal, query)
}
