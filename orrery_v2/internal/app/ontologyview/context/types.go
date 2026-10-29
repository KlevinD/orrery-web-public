package contextview

import (
	"orrery_v2/internal/app/contextgraph"
	"time"

	ontologycontract "orrery_v2/internal/app/ontologyview/contract"
)

type SummaryData struct {
	Objects         ontologycontract.CountMetric `json:"objects"`
	Relations       ontologycontract.CountMetric `json:"relations"`
	Facts           ontologycontract.CountMetric `json:"facts"`
	Evidence        ontologycontract.CountMetric `json:"evidence"`
	OldestUpdatedAt *time.Time                   `json:"oldest_updated_at,omitempty"`
	NewestUpdatedAt *time.Time                   `json:"newest_updated_at,omitempty"`
}

type OverviewDomain struct {
	ID      string                       `json:"id"`
	Objects ontologycontract.CountMetric `json:"objects"`
}

type CompanyOverviewData struct {
	Objects         ontologycontract.CountMetric `json:"objects"`
	Facts           ontologycontract.CountMetric `json:"facts"`
	Relations       ontologycontract.CountMetric `json:"relations"`
	Evidence        ontologycontract.CountMetric `json:"evidence"`
	DomainCount     ontologycontract.CountMetric `json:"domain_count"`
	Domains         []OverviewDomain             `json:"domains"`
	RecentObjects   []ObjectItem                 `json:"recent_objects"`
	NewestUpdatedAt *time.Time                   `json:"newest_updated_at,omitempty"`
}

type ObjectListQuery struct {
	TenantID   string
	ObjectType string
	Status     string
	Source     string
	Search     string
	Limit      int
	Cursor     string
}

type ObjectItem struct {
	Redacted    bool                           `json:"-"`
	Temporal    *contextgraph.TemporalMetadata `json:"temporal,omitempty"`
	TenantID    string                         `json:"tenant_id,omitempty"`
	ID          string                         `json:"id"`
	ObjectType  string                         `json:"object_type"`
	Label       string                         `json:"label"`
	Status      string                         `json:"status,omitempty"`
	Sensitivity string                         `json:"sensitivity,omitempty"`
	Confidence  float64                        `json:"confidence,omitempty"`
	UpdatedAt   *time.Time                     `json:"updated_at,omitempty"`
}

type ObjectDetail struct {
	ObjectItem
	CanonicalKey string         `json:"canonical_key,omitempty"`
	Properties   map[string]any `json:"properties,omitempty"`
}

type ChildQuery struct {
	TenantID string
	ObjectID string
	Limit    int
	Cursor   string
}

type RelationListQuery struct {
	TenantID  string
	ObjectID  string
	Direction string
	LinkType  string
	Limit     int
	Cursor    string
}

type FactItem struct {
	EvidenceRefs []contextgraph.Ref             `json:"-"`
	Temporal     *contextgraph.TemporalMetadata `json:"temporal,omitempty"`
	Redacted     bool                           `json:"-"`
	ID           string                         `json:"id"`
	Predicate    string                         `json:"predicate"`
	Value        any                            `json:"value,omitempty"`
	Status       string                         `json:"status,omitempty"`
	Confidence   float64                        `json:"confidence,omitempty"`
	UpdatedAt    *time.Time                     `json:"updated_at,omitempty"`
}

type RelationItem struct {
	RelationCode  string     `json:"relation_code,omitempty"`
	ID            string     `json:"id"`
	LinkType      string     `json:"link_type"`
	LinkTypeLabel string     `json:"link_type_label"`
	Direction     string     `json:"direction"`
	OtherObject   ObjectItem `json:"other_object"`
	Status        string     `json:"status,omitempty"`
	Confidence    float64    `json:"confidence,omitempty"`
	UpdatedAt     *time.Time `json:"updated_at,omitempty"`
}

type EvidenceItem struct {
	ID         string     `json:"id"`
	SourceKind string     `json:"source_kind,omitempty"`
	SourceID   string     `json:"source_id,omitempty"`
	Summary    string     `json:"summary,omitempty"`
	Confidence float64    `json:"confidence,omitempty"`
	ObservedAt *time.Time `json:"observed_at,omitempty"`
}

type NeighborhoodQuery struct {
	TenantID  string
	RootID    string
	LinkType  string
	LinkTypes []string
	Depth     int
	MaxNodes  int
	MaxEdges  int
}

type NeighborhoodNode struct {
	ObjectItem
	Degree int `json:"degree"`
}

type NeighborhoodEdge struct {
	EvidenceRefs  []contextgraph.Ref             `json:"-"`
	Redacted      bool                           `json:"-"`
	Temporal      *contextgraph.TemporalMetadata `json:"temporal,omitempty"`
	RelationCode  string                         `json:"relation_code,omitempty"`
	ID            string                         `json:"id"`
	LinkType      string                         `json:"link_type"`
	LinkTypeLabel string                         `json:"link_type_label"`
	SourceID      string                         `json:"source_id"`
	TargetID      string                         `json:"target_id"`
	Status        string                         `json:"status,omitempty"`
	Confidence    float64                        `json:"confidence,omitempty"`
	UpdatedAt     *time.Time                     `json:"updated_at,omitempty"`
}

type Neighborhood struct {
	RootID          string             `json:"root_id"`
	Nodes           []NeighborhoodNode `json:"nodes"`
	Edges           []NeighborhoodEdge `json:"edges"`
	VisibleNodes    int                `json:"visible_nodes"`
	VisibleEdges    int                `json:"visible_edges"`
	AppliedDepth    int                `json:"applied_depth"`
	AppliedMaxNodes int                `json:"applied_max_nodes"`
	AppliedMaxEdges int                `json:"applied_max_edges"`
	Truncated       bool               `json:"truncated"`
}
