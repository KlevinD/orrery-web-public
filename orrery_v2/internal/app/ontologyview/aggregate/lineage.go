package aggregate

import (
	"context"
	"strings"

	"orrery_v2/internal/app/identity"
	"orrery_v2/internal/app/ontologyscope"
	ontologycontract "orrery_v2/internal/app/ontologyview/contract"
	lineageview "orrery_v2/internal/app/ontologyview/lineage"
)

type LineageReader interface {
	Summary(context.Context, identity.Principal, string) (ontologycontract.Summary[lineageview.SummaryData], error)
	GetProvenance(context.Context, identity.Principal, string, string) (lineageview.ProvenanceAnchor, error)
	ListAssertions(context.Context, identity.Principal, lineageview.FactPageQuery) (ontologycontract.CursorPage[lineageview.AssertionItem], error)
	ListEvidence(context.Context, identity.Principal, lineageview.FactPageQuery) (ontologycontract.CursorPage[lineageview.EvidenceItem], error)
	ListProjectionTrace(context.Context, identity.Principal, lineageview.FactPageQuery) (ontologycontract.CursorPage[lineageview.ProjectionTraceItem], error)
}

type LineageService struct {
	scopes   *ScopeEnumerator
	delegate LineageReader
}

func NewLineageService(scopes *ScopeEnumerator, delegate LineageReader) (*LineageService, error) {
	if scopes == nil || delegate == nil {
		return nil, ontologyscope.ErrDependencyUnavailable
	}
	return &LineageService{scopes: scopes, delegate: delegate}, nil
}

func (s *LineageService) Summary(ctx context.Context, principal identity.Principal, tenantID string) (ontologycontract.Summary[lineageview.SummaryData], error) {
	if strings.TrimSpace(tenantID) != "" {
		return s.delegate.Summary(ctx, principal, tenantID)
	}
	scopes, err := s.scopes.Discover(ctx, principal)
	if err != nil {
		return ontologycontract.Summary[lineageview.SummaryData]{}, err
	}
	return aggregateLineageSummary(ctx, scopes, func(ctx context.Context, tenantID string) (ontologycontract.Summary[lineageview.SummaryData], error) {
		return s.delegate.Summary(ctx, principal, tenantID)
	})
}

func (s *LineageService) GetProvenance(ctx context.Context, principal identity.Principal, tenantID, factID string) (lineageview.ProvenanceAnchor, error) {
	tenantID, err := requireConcreteTenant(tenantID)
	if err != nil {
		return lineageview.ProvenanceAnchor{}, err
	}
	return s.delegate.GetProvenance(ctx, principal, tenantID, factID)
}

func (s *LineageService) ListAssertions(ctx context.Context, principal identity.Principal, query lineageview.FactPageQuery) (ontologycontract.CursorPage[lineageview.AssertionItem], error) {
	tenantID, err := requireConcreteTenant(query.TenantID)
	if err != nil {
		return ontologycontract.CursorPage[lineageview.AssertionItem]{}, err
	}
	query.TenantID = tenantID
	return s.delegate.ListAssertions(ctx, principal, query)
}

func (s *LineageService) ListEvidence(ctx context.Context, principal identity.Principal, query lineageview.FactPageQuery) (ontologycontract.CursorPage[lineageview.EvidenceItem], error) {
	tenantID, err := requireConcreteTenant(query.TenantID)
	if err != nil {
		return ontologycontract.CursorPage[lineageview.EvidenceItem]{}, err
	}
	query.TenantID = tenantID
	return s.delegate.ListEvidence(ctx, principal, query)
}

func (s *LineageService) ListProjectionTrace(ctx context.Context, principal identity.Principal, query lineageview.FactPageQuery) (ontologycontract.CursorPage[lineageview.ProjectionTraceItem], error) {
	tenantID, err := requireConcreteTenant(query.TenantID)
	if err != nil {
		return ontologycontract.CursorPage[lineageview.ProjectionTraceItem]{}, err
	}
	query.TenantID = tenantID
	return s.delegate.ListProjectionTrace(ctx, principal, query)
}
