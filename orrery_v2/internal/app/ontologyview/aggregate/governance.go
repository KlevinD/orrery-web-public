package aggregate

import (
	"context"
	"strings"
	"unicode/utf8"

	"orrery_v2/internal/app/identity"
	"orrery_v2/internal/app/ontology"
	"orrery_v2/internal/app/ontologyscope"
	ontologycontract "orrery_v2/internal/app/ontologyview/contract"
	governanceview "orrery_v2/internal/app/ontologyview/governance"
)

type GovernanceReader interface {
	Summary(context.Context, identity.Principal, string) (ontologycontract.Summary[governanceview.SummaryData], error)
	ListProposals(context.Context, identity.Principal, governanceview.ProposalListQuery) (ontologycontract.CursorPage[governanceview.ProposalItem], error)
	GetProposal(context.Context, identity.Principal, string, string) (governanceview.ProposalDetail, error)
	AnalyzeProposal(context.Context, identity.Principal, string, string) (governanceview.AnalysisReport, error)
	ListReceipts(context.Context, identity.Principal, governanceview.ReceiptListQuery) (ontologycontract.CursorPage[governanceview.ReceiptItem], error)
	ListAuditEvents(context.Context, identity.Principal, governanceview.AuditEventListQuery) (ontologycontract.CursorPage[governanceview.AuditEventItem], error)
}

type GovernanceService struct {
	scopes   *ScopeEnumerator
	delegate GovernanceReader
}

func NewGovernanceService(scopes *ScopeEnumerator, delegate GovernanceReader) (*GovernanceService, error) {
	if scopes == nil || delegate == nil {
		return nil, ontologyscope.ErrDependencyUnavailable
	}
	return &GovernanceService{scopes: scopes, delegate: delegate}, nil
}

func (s *GovernanceService) Summary(ctx context.Context, principal identity.Principal, tenantID string) (ontologycontract.Summary[governanceview.SummaryData], error) {
	if strings.TrimSpace(tenantID) != "" {
		return s.delegate.Summary(ctx, principal, tenantID)
	}
	scopes, err := s.scopes.Discover(ctx, principal)
	if err != nil {
		return ontologycontract.Summary[governanceview.SummaryData]{}, err
	}
	return aggregateGovernanceSummary(ctx, scopes, func(ctx context.Context, tenantID string) (ontologycontract.Summary[governanceview.SummaryData], error) {
		return s.delegate.Summary(ctx, principal, tenantID)
	})
}

func (s *GovernanceService) ListProposals(ctx context.Context, principal identity.Principal, query governanceview.ProposalListQuery) (ontologycontract.CursorPage[governanceview.ProposalItem], error) {
	if strings.TrimSpace(query.TenantID) != "" {
		return s.delegate.ListProposals(ctx, principal, query)
	}
	query.Queue = governanceview.ProposalQueue(strings.ToLower(strings.TrimSpace(string(query.Queue))))
	if query.Queue == "" {
		query.Queue = governanceview.QueueActionable
	}
	query.TargetKind = strings.ToLower(strings.TrimSpace(query.TargetKind))
	query.Search = strings.TrimSpace(query.Search)
	query.Cursor = strings.TrimSpace(query.Cursor)
	limit, err := ontologycontract.NormalizeLimit(query.Limit, ontologycontract.DefaultChildListLimit, ontologycontract.MaxPageLimit)
	if err != nil || query.Queue != governanceview.QueueActionable && query.Queue != governanceview.QueueAll || len(query.TargetKind) > 128 || !utf8.ValidString(query.Search) || utf8.RuneCountInString(query.Search) > 128 || len(query.Cursor) > 4096 || !validProposalStatus(query.Status) {
		return ontologycontract.CursorPage[governanceview.ProposalItem]{}, ontologycontract.ErrInvalidRequest
	}
	key, err := aggregateQueryKey("governance-proposals", struct {
		Queue      governanceview.ProposalQueue    `json:"queue"`
		Status     ontology.MetadataProposalStatus `json:"status"`
		TargetKind string                          `json:"target_kind"`
		Search     string                          `json:"search"`
		Limit      int                             `json:"limit"`
	}{query.Queue, query.Status, query.TargetKind, strings.ToLower(query.Search), limit})
	if err != nil {
		return ontologycontract.CursorPage[governanceview.ProposalItem]{}, err
	}
	scopes, err := s.scopes.Discover(ctx, principal)
	if err != nil {
		return ontologycontract.CursorPage[governanceview.ProposalItem]{}, err
	}
	return aggregateCursorPage(ctx, s.scopes, principal, scopes, key, query.Cursor, limit,
		func(ctx context.Context, tenantID string, limit int, cursor string) (ontologycontract.CursorPage[governanceview.ProposalItem], error) {
			inner := query
			inner.TenantID, inner.Limit, inner.Cursor = tenantID, limit, cursor
			return s.delegate.ListProposals(ctx, principal, inner)
		},
		func(tenantID string, item governanceview.ProposalItem) governanceview.ProposalItem {
			item.TenantID = tenantID
			return item
		},
	)
}

func validProposalStatus(status ontology.MetadataProposalStatus) bool {
	switch status {
	case "", ontology.MetadataProposalStatusProposed, ontology.MetadataProposalStatusAccepted, ontology.MetadataProposalStatusRejected:
		return true
	default:
		return false
	}
}

func (s *GovernanceService) GetProposal(ctx context.Context, principal identity.Principal, tenantID, proposalID string) (governanceview.ProposalDetail, error) {
	tenantID, err := requireConcreteTenant(tenantID)
	if err != nil {
		return governanceview.ProposalDetail{}, err
	}
	return s.delegate.GetProposal(ctx, principal, tenantID, proposalID)
}

func (s *GovernanceService) AnalyzeProposal(ctx context.Context, principal identity.Principal, tenantID, proposalID string) (governanceview.AnalysisReport, error) {
	tenantID, err := requireConcreteTenant(tenantID)
	if err != nil {
		return governanceview.AnalysisReport{}, err
	}
	return s.delegate.AnalyzeProposal(ctx, principal, tenantID, proposalID)
}

func (s *GovernanceService) ListReceipts(ctx context.Context, principal identity.Principal, query governanceview.ReceiptListQuery) (ontologycontract.CursorPage[governanceview.ReceiptItem], error) {
	tenantID, err := requireConcreteTenant(query.TenantID)
	if err != nil {
		return ontologycontract.CursorPage[governanceview.ReceiptItem]{}, err
	}
	query.TenantID = tenantID
	return s.delegate.ListReceipts(ctx, principal, query)
}

func (s *GovernanceService) ListAuditEvents(ctx context.Context, principal identity.Principal, query governanceview.AuditEventListQuery) (ontologycontract.CursorPage[governanceview.AuditEventItem], error) {
	tenantID, err := requireConcreteTenant(query.TenantID)
	if err != nil {
		return ontologycontract.CursorPage[governanceview.AuditEventItem]{}, err
	}
	query.TenantID = tenantID
	return s.delegate.ListAuditEvents(ctx, principal, query)
}
