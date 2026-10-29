package projection

import (
	"time"

	ontologycontract "orrery_v2/internal/app/ontologyview/contract"
)

type LatestActivityMetric struct {
	Status     ontologycontract.MetricStatus `json:"status"`
	ObservedAt *time.Time                    `json:"observed_at,omitempty"`
}

type SummaryData struct {
	UnresolvedAudits ontologycontract.CountMetric `json:"unresolved_audits"`
	LatestActivity   LatestActivityMetric         `json:"latest_activity"`
}

type AuditListQuery struct {
	TenantID   string
	Status     string
	ReasonCode string
	Limit      int
	Cursor     string
}

type AuditItem struct {
	TenantID        string     `json:"tenant_id,omitempty"`
	ID              string     `json:"id"`
	SourceEventType string     `json:"source_event_type,omitempty"`
	Status          string     `json:"status,omitempty"`
	ReasonCode      string     `json:"reason_code,omitempty"`
	ObservedAt      *time.Time `json:"observed_at,omitempty"`
}

type AuditDetail struct {
	AuditItem
	SourceEventID string         `json:"source_event_id,omitempty"`
	Reason        string         `json:"reason,omitempty"`
	Details       map[string]any `json:"details,omitempty"`
}

type ReplayHistoryListQuery struct {
	TenantID string
	AuditID  string
	Limit    int
	Cursor   string
}

type ReplayHistoryItem struct {
	ID          string     `json:"id"`
	Status      string     `json:"status,omitempty"`
	Error       string     `json:"error,omitempty"`
	AttemptedAt *time.Time `json:"attempted_at,omitempty"`
}

type ReplayRequest struct {
	TenantID       string
	AuditID        string
	IdempotencyKey string
}

type AcknowledgeRequest struct {
	TenantID       string
	AuditID        string
	Reason         string
	IdempotencyKey string
}

type MutationResult struct {
	AuditID    string     `json:"audit_id"`
	Status     string     `json:"status"`
	ReasonCode string     `json:"reason_code,omitempty"`
	ObservedAt *time.Time `json:"observed_at,omitempty"`
}
