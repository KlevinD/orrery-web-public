package lineage

import (
	"time"

	ontologycontract "orrery_v2/internal/app/ontologyview/contract"
)

type ReferenceStatus string

const (
	ReferenceResolved    ReferenceStatus = "resolved"
	ReferenceUnresolved  ReferenceStatus = "unresolved"
	ReferenceUnavailable ReferenceStatus = "unavailable"
)

type MappingResolutionStatus string

const (
	MappingExact      MappingResolutionStatus = "exact"
	MappingCandidate  MappingResolutionStatus = "candidate"
	MappingUnresolved MappingResolutionStatus = "unresolved"
)

type FactIdentity struct {
	ID         string          `json:"id"`
	Subject    SubjectIdentity `json:"subject"`
	Predicate  string          `json:"predicate"`
	Status     string          `json:"status,omitempty"`
	Confidence *float64        `json:"confidence,omitempty"`
	UpdatedAt  *time.Time      `json:"updated_at,omitempty"`
}

type SubjectIdentity struct {
	Kind  string `json:"kind"`
	ID    string `json:"id"`
	Label string `json:"label,omitempty"`
}

type SafeEventMetadata struct {
	ID            string     `json:"id"`
	Type          string     `json:"type"`
	StreamID      string     `json:"stream_id,omitempty"`
	Version       int64      `json:"version,omitempty"`
	SchemaVersion string     `json:"schema_version,omitempty"`
	OccurredAt    *time.Time `json:"occurred_at,omitempty"`
}

type MappingRuleRef struct {
	ID        string     `json:"id"`
	Origin    string     `json:"origin"`
	Version   int        `json:"version,omitempty"`
	UpdatedAt *time.Time `json:"updated_at,omitempty"`
}

type MappingResolution struct {
	Status     MappingResolutionStatus `json:"status"`
	Candidates []MappingRuleRef        `json:"candidates,omitempty"`
	Truncated  bool                    `json:"truncated,omitempty"`
}

type ProvenanceAnchor struct {
	Fact             FactIdentity       `json:"fact"`
	ProvenanceStatus ReferenceStatus    `json:"provenance_status"`
	Event            *SafeEventMetadata `json:"event,omitempty"`
	Mapping          MappingResolution  `json:"mapping"`
}

type FactPageQuery struct {
	TenantID string
	FactID   string
	Limit    int
	Cursor   string
}

type AssertionItem struct {
	ID         string     `json:"id"`
	Value      any        `json:"value,omitempty"`
	Status     string     `json:"status,omitempty"`
	Confidence *float64   `json:"confidence,omitempty"`
	ObservedAt *time.Time `json:"observed_at,omitempty"`
}

type EvidenceItem struct {
	Status     ReferenceStatus `json:"status"`
	RefKind    string          `json:"ref_kind,omitempty"`
	RefID      string          `json:"ref_id,omitempty"`
	EvidenceID string          `json:"evidence_id,omitempty"`
	Summary    string          `json:"summary,omitempty"`
	Confidence *float64        `json:"confidence,omitempty"`
	ObservedAt *time.Time      `json:"observed_at,omitempty"`
}

type ProjectionTraceItem struct {
	ID         string     `json:"id"`
	Status     string     `json:"status"`
	ReasonCode string     `json:"reason_code,omitempty"`
	Reason     string     `json:"reason,omitempty"`
	ObservedAt *time.Time `json:"observed_at,omitempty"`
}

type DistributionMetric struct {
	Status   ontologycontract.MetricStatus `json:"status"`
	Low      *int64                        `json:"low,omitempty"`
	Medium   *int64                        `json:"medium,omitempty"`
	High     *int64                        `json:"high,omitempty"`
	Unscored *int64                        `json:"unscored,omitempty"`
}

type FreshnessMetric struct {
	Status          ontologycontract.MetricStatus `json:"status"`
	Last24Hours     *int64                        `json:"last_24h,omitempty"`
	DaysOneToSeven  *int64                        `json:"days_1_7,omitempty"`
	Older           *int64                        `json:"older,omitempty"`
	NewestUpdatedAt *time.Time                    `json:"newest_updated_at,omitempty"`
	OldestUpdatedAt *time.Time                    `json:"oldest_updated_at,omitempty"`
}

type SummaryData struct {
	FactProvenanceCoverage         ontologycontract.RatioMetric `json:"fact_provenance_coverage"`
	FactEvidenceReferenceCoverage  ontologycontract.RatioMetric `json:"fact_evidence_reference_coverage"`
	FactResolvableEvidenceCoverage ontologycontract.RatioMetric `json:"fact_resolvable_evidence_coverage"`
	ConfidenceDistribution         DistributionMetric           `json:"confidence_distribution"`
	Freshness                      FreshnessMetric              `json:"freshness"`
}
