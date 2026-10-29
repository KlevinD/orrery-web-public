package governance

import (
	"context"
	"time"

	"orrery_v2/internal/app/datavisibility"
	"orrery_v2/internal/app/ontology"
)

type ProposalReader interface {
	GetMetadataProposalForTenant(context.Context, string, string) (ontology.OntologyMetadataProposal, error)
	ListMetadataProposalCursorPage(context.Context, ontology.MetadataProposalCursorQuery) (ontology.MetadataProposalCursorPage, error)
}

type ReceiptReader interface {
	ListPromotionReceiptCursorPage(context.Context, ontology.PromotionReceiptCursorQuery) (ontology.PromotionReceiptCursorPage, error)
	ListCurrentSuccessfulPromotionReceipts(context.Context, ontology.CurrentPromotionReceiptBatchQuery) (ontology.PromotionReceiptCursorPage, error)
}

type VisibilityPolicy interface {
	Evaluate(context.Context, datavisibility.EvaluateRequest) (datavisibility.Decision, error)
}

type ProposalAuditSeek struct {
	OccurredAt time.Time
	AuditID    string
}

type ProposalAuditQuery struct {
	TenantID   string
	ProposalID string
	Limit      int
	After      ProposalAuditSeek
}

type ProposalAuditRecord struct {
	AuditID    string
	TenantID   string
	ProposalID string
	Action     string
	Result     string
	Severity   string
	ActorID    string
	ActorKind  string
	Summary    string
	OccurredAt time.Time
	AccessRefs []datavisibility.AccessRef
}

type ProposalAuditPage struct {
	Events  []ProposalAuditRecord
	HasMore bool
}

type AuditReader interface {
	ListProposalAuditEvents(context.Context, ProposalAuditQuery) (ProposalAuditPage, error)
}
