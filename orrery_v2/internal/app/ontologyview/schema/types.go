package schema

import (
	"time"

	ontologycontract "orrery_v2/internal/app/ontologyview/contract"
)

type DefinitionKind string
type DefinitionOrigin string
type DefinitionStatus string
type GraphView string

const (
	KindObject   DefinitionKind = "object"
	KindLink     DefinitionKind = "link"
	KindProperty DefinitionKind = "property"
	KindMapping  DefinitionKind = "mapping"
	KindAction   DefinitionKind = "action"
	KindEvent    DefinitionKind = "event"
)

const (
	OriginSystem DefinitionOrigin = "system"
	OriginTenant DefinitionOrigin = "tenant"
)

const (
	StatusActive   DefinitionStatus = "active"
	StatusDisabled DefinitionStatus = "disabled"
)

const (
	GraphSemantic   GraphView = "semantic"
	GraphProjection GraphView = "projection"
)

type DefinitionListQuery struct {
	TenantID string
	Kind     DefinitionKind
	Origin   DefinitionOrigin
	Status   DefinitionStatus
	Search   string
	Limit    int
	Cursor   string
}

type DefinitionItem struct {
	TenantID    string           `json:"tenant_id,omitempty"`
	Kind        DefinitionKind   `json:"kind"`
	ID          string           `json:"id"`
	Label       string           `json:"label"`
	Origin      DefinitionOrigin `json:"origin"`
	OwnerDomain string           `json:"owner_domain,omitempty"`
	Version     string           `json:"version"`
	Status      DefinitionStatus `json:"status"`
	UpdatedAt   *time.Time       `json:"updated_at,omitempty"`
}

type DefinitionDetail struct {
	DefinitionItem
	Attributes DefinitionAttributes `json:"attributes"`
}

type DefinitionAttributes struct {
	TenantScope        string               `json:"tenant_scope,omitempty"`
	CanonicalKeyRule   string               `json:"canonical_key_rule,omitempty"`
	Sensitivity        string               `json:"sensitivity,omitempty"`
	Lifecycle          string               `json:"lifecycle,omitempty"`
	TemporalMode       string               `json:"temporal_mode,omitempty"`
	Properties         []DefinitionProperty `json:"properties,omitempty"`
	FromObjectType     string               `json:"from_object_type,omitempty"`
	ToObjectType       string               `json:"to_object_type,omitempty"`
	Cardinality        string               `json:"cardinality,omitempty"`
	EvidenceRequired   *bool                `json:"evidence_required,omitempty"`
	SuggestedAllowed   *bool                `json:"suggested_allowed,omitempty"`
	ObjectTypeID       string               `json:"object_type_id,omitempty"`
	PropertyID         string               `json:"property_id,omitempty"`
	ValueType          string               `json:"value_type,omitempty"`
	AuthoritySource    string               `json:"authority_source,omitempty"`
	ConflictPolicy     string               `json:"conflict_policy,omitempty"`
	EventType          string               `json:"event_type,omitempty"`
	SourceDomain       string               `json:"source_domain,omitempty"`
	SourceRef          string               `json:"source_ref,omitempty"`
	EventSchemaVersion string               `json:"event_schema_version,omitempty"`
	RuleHash           string               `json:"rule_hash,omitempty"`
	Enabled            *bool                `json:"enabled,omitempty"`
	Category           string               `json:"category,omitempty"`
	Description        string               `json:"description,omitempty"`
	ProviderKind       string               `json:"provider_kind,omitempty"`
	ProviderRef        string               `json:"provider_ref,omitempty"`
	PermissionScope    string               `json:"permission_scope,omitempty"`
	RiskLevel          string               `json:"risk_level,omitempty"`
	DryRunSupported    *bool                `json:"dry_run_supported,omitempty"`
}

type DefinitionProperty struct {
	PropertyID   string `json:"property_id"`
	DisplayName  string `json:"display_name,omitempty"`
	ValueType    string `json:"value_type"`
	TemporalMode string `json:"temporal_mode,omitempty"`
	Required     bool   `json:"required"`
	Sensitivity  string `json:"sensitivity,omitempty"`
}

type DefinitionReference struct {
	Kind      DefinitionKind `json:"kind"`
	ID        string         `json:"id"`
	Label     string         `json:"label"`
	Relation  string         `json:"relation"`
	Direction string         `json:"direction"`
}

type ReferenceQuery struct {
	TenantID     string
	Kind         DefinitionKind
	DefinitionID string
	Direction    string
	Limit        int
	Cursor       string
}

type SummaryData struct {
	ObjectTypes     ontologycontract.CountMetric `json:"object_types"`
	LinkTypes       ontologycontract.CountMetric `json:"link_types"`
	PropertyTypes   ontologycontract.CountMetric `json:"property_types"`
	MappingRules    ontologycontract.CountMetric `json:"mapping_rules"`
	ActionTypes     ontologycontract.CountMetric `json:"action_types"`
	LatestUpdatedAt *time.Time                   `json:"latest_updated_at,omitempty"`
}

type GraphQuery struct {
	TenantID string
	View     GraphView
	Search   string
	MaxNodes int
}

type GraphNode struct {
	ID           string           `json:"id"`
	TenantID     string           `json:"tenant_id,omitempty"`
	DefinitionID string           `json:"definition_id,omitempty"`
	Kind         DefinitionKind   `json:"kind"`
	Label        string           `json:"label"`
	Status       DefinitionStatus `json:"status"`
	Origin       DefinitionOrigin `json:"origin"`
	Matched      bool             `json:"matched"`
}

type GraphEdge struct {
	ID       string `json:"id"`
	SourceID string `json:"source_id"`
	TargetID string `json:"target_id"`
	Relation string `json:"relation"`
}

type Graph struct {
	View            GraphView   `json:"view"`
	Nodes           []GraphNode `json:"nodes"`
	Edges           []GraphEdge `json:"edges"`
	VisibleNodes    int         `json:"visible_nodes"`
	VisibleEdges    int         `json:"visible_edges"`
	AppliedMaxNodes int         `json:"applied_max_nodes"`
	Truncated       bool        `json:"truncated"`
}
