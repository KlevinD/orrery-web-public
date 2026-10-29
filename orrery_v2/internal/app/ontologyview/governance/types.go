package governance

import (
	"time"

	"orrery_v2/internal/app/ontology"
	ontologycontract "orrery_v2/internal/app/ontologyview/contract"
)

type ProposalQueue string

const (
	QueueActionable ProposalQueue = "actionable"
	QueueAll        ProposalQueue = "all"
)

type ProposalListQuery struct {
	TenantID   string
	Queue      ProposalQueue
	Status     ontology.MetadataProposalStatus
	TargetKind string
	Search     string
	Limit      int
	Cursor     string
}

type ProposalItem struct {
	TenantID      string                              `json:"tenant_id,omitempty"`
	ID            string                              `json:"id"`
	Revision      *int64                              `json:"revision,omitempty"`
	TargetKind    string                              `json:"target_kind,omitempty"`
	TargetID      string                              `json:"target_id,omitempty"`
	MetadataKey   string                              `json:"metadata_key,omitempty"`
	ProposedValue string                              `json:"proposed_value,omitempty"`
	SourceKind    ontology.MetadataProposalSourceKind `json:"source_kind,omitempty"`
	Confidence    *float64                            `json:"confidence,omitempty"`
	Status        ontology.MetadataProposalStatus     `json:"status,omitempty"`
	Actionable    *bool                               `json:"actionable,omitempty"`
	UpdatedAt     *time.Time                          `json:"updated_at,omitempty"`
}

type EvidenceReference struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
}

type ProposalDetail struct {
	ProposalItem
	Source             string              `json:"source,omitempty"`
	SourceRef          string              `json:"source_ref,omitempty"`
	SourceVersion      string              `json:"source_version,omitempty"`
	Reason             string              `json:"reason,omitempty"`
	EvidenceRefs       []EvidenceReference `json:"evidence_refs,omitempty"`
	ReviewedBy         string              `json:"reviewed_by,omitempty"`
	ReviewedByKind     string              `json:"reviewed_by_kind,omitempty"`
	ReviewedAt         *time.Time          `json:"reviewed_at,omitempty"`
	ReviewReason       string              `json:"review_reason,omitempty"`
	ReviewEvidenceRefs []EvidenceReference `json:"review_evidence_refs,omitempty"`
	CreatedAt          *time.Time          `json:"created_at,omitempty"`
}

type AnalysisIssue struct {
	Code     ontology.MetadataProposalIssueCode     `json:"code"`
	Severity ontology.MetadataProposalIssueSeverity `json:"severity"`
	Message  string                                 `json:"message"`
}

type AnalysisReport struct {
	BlockingCount int             `json:"blocking_count"`
	WarningCount  int             `json:"warning_count"`
	Issues        []AnalysisIssue `json:"issues"`
}

type SuccessfulReceiptRef struct {
	ReceiptID string                          `json:"receipt_id"`
	Status    ontology.PromotionReceiptStatus `json:"status"`
	AppliedAt *time.Time                      `json:"applied_at,omitempty"`
}

type LatestReceiptMetric struct {
	Status  ontologycontract.MetricStatus `json:"status"`
	Receipt *SuccessfulReceiptRef         `json:"receipt,omitempty"`
}

type SummaryData struct {
	ActionableProposals ontologycontract.CountMetric `json:"actionable_proposals"`
	LatestReceipt       LatestReceiptMetric          `json:"latest_receipt"`
}

type ReceiptListQuery struct {
	TenantID   string
	ProposalID string
	Limit      int
	Cursor     string
}

type RetryProjectionRefreshRequest struct {
	TenantID       string
	ReceiptID      string
	IdempotencyKey string
}

type ProjectionRefreshResult struct {
	ReceiptID        string `json:"receipt_id"`
	ProposalID       string `json:"proposal_id"`
	Status           string `json:"projection_refresh_status"`
	IdempotentReplay bool   `json:"-"`
}

type ReceiptItem struct {
	ReceiptID               string                                         `json:"receipt_id"`
	Status                  ontology.PromotionReceiptStatus                `json:"status,omitempty"`
	ProjectionRefreshStatus ontology.MappingProjectionRefreshState         `json:"projection_refresh_status,omitempty"`
	ProposalBindingStatus   ontology.PromotionReceiptProposalBindingStatus `json:"proposal_binding_status,omitempty"`
	ProposalRevision        *int64                                         `json:"proposal_revision,omitempty"`
	ActorID                 string                                         `json:"actor_id,omitempty"`
	ActorKind               string                                         `json:"actor_kind,omitempty"`
	ChangeCount             *int                                           `json:"change_count,omitempty"`
	IssueCount              *int                                           `json:"issue_count,omitempty"`
	AppliedAt               *time.Time                                     `json:"applied_at,omitempty"`
	UpdatedAt               *time.Time                                     `json:"updated_at,omitempty"`
}

type AuditEventListQuery struct {
	TenantID   string
	ProposalID string
	Limit      int
	Cursor     string
}

type AuditEventItem struct {
	AuditID    string     `json:"audit_id"`
	Action     string     `json:"action,omitempty"`
	Result     string     `json:"result,omitempty"`
	Severity   string     `json:"severity,omitempty"`
	ActorID    string     `json:"actor_id,omitempty"`
	ActorKind  string     `json:"actor_kind,omitempty"`
	Summary    string     `json:"summary,omitempty"`
	OccurredAt *time.Time `json:"occurred_at,omitempty"`
}
