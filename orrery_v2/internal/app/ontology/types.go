package ontology

import (
	"errors"
	"time"
)

var (
	ErrInvalidDefinition   = errors.New("invalid ontology definition")
	ErrNotFound            = errors.New("ontology definition not found")
	ErrVersionConflict     = errors.New("ontology registry version conflict")
	ErrIdempotencyConflict = errors.New("ontology idempotency conflict")
	ErrUnknownObjectType   = errors.New("unknown ontology object type")
	ErrUnknownLinkType     = errors.New("unknown ontology link type")
	ErrUnknownPropertyType = errors.New("unknown ontology property type")
)

type TenantScope string

const (
	TenantScopeGlobal TenantScope = "global"
	TenantScopeTenant TenantScope = "tenant"
)

type ValueType string

const (
	ValueTypeString   ValueType = "string"
	ValueTypeNumber   ValueType = "number"
	ValueTypeBoolean  ValueType = "boolean"
	ValueTypeObject   ValueType = "object"
	ValueTypeArray    ValueType = "array"
	ValueTypeRef      ValueType = "ref"
	ValueTypeDate     ValueType = "date"
	ValueTypeDateTime ValueType = "datetime"
	ValueTypeDuration ValueType = "duration"
	ValueTypeEnum     ValueType = "enum"
)

type TemporalMode string

const (
	TemporalModeStatic        TemporalMode = "static"
	TemporalModeStateInterval TemporalMode = "state_interval"
	TemporalModeEvent         TemporalMode = "event"
	TemporalModeMeasurement   TemporalMode = "measurement"
)

type Cardinality string

const (
	CardinalityOneToOne   Cardinality = "one_to_one"
	CardinalityOneToMany  Cardinality = "one_to_many"
	CardinalityManyToOne  Cardinality = "many_to_one"
	CardinalityManyToMany Cardinality = "many_to_many"
)

type ConflictPolicy string

const (
	ConflictPolicyAuthorityWins ConflictPolicy = "authority_wins"
	ConflictPolicyLatestWins    ConflictPolicy = "latest_wins"
	ConflictPolicyManualReview  ConflictPolicy = "manual_review"
)

type PropertyDefinition struct {
	PropertyID   string       `json:"property_id"`
	DisplayName  string       `json:"display_name,omitempty"`
	ValueType    ValueType    `json:"value_type"`
	TemporalMode TemporalMode `json:"temporal_mode,omitempty"`
	Required     bool         `json:"required,omitempty"`
	Sensitivity  string       `json:"sensitivity,omitempty"`
}

type ObjectType struct {
	ID               string               `json:"id"`
	TenantID         string               `json:"tenant_id,omitempty"`
	TenantScope      TenantScope          `json:"tenant_scope"`
	DisplayName      string               `json:"display_name"`
	CanonicalKeyRule string               `json:"canonical_key_rule"`
	OwnerDomain      string               `json:"owner_domain,omitempty"`
	Sensitivity      string               `json:"sensitivity,omitempty"`
	Lifecycle        string               `json:"lifecycle,omitempty"`
	TemporalMode     TemporalMode         `json:"temporal_mode,omitempty"`
	Properties       []PropertyDefinition `json:"properties,omitempty"`
	AllowedLinkTypes []string             `json:"allowed_link_types,omitempty"`
	AllowedActions   []string             `json:"allowed_actions,omitempty"`
	Version          int                  `json:"version"`
	CreatedAt        time.Time            `json:"created_at,omitempty"`
	UpdatedAt        time.Time            `json:"updated_at,omitempty"`
}

type LinkType struct {
	ID               string       `json:"id"`
	TenantID         string       `json:"tenant_id,omitempty"`
	TenantScope      TenantScope  `json:"tenant_scope"`
	DisplayName      string       `json:"display_name,omitempty"`
	FromObjectType   string       `json:"from_object_type"`
	ToObjectType     string       `json:"to_object_type"`
	Cardinality      Cardinality  `json:"cardinality"`
	TemporalMode     TemporalMode `json:"temporal_mode,omitempty"`
	EvidenceRequired bool         `json:"evidence_required,omitempty"`
	SuggestedAllowed bool         `json:"suggested_allowed,omitempty"`
	AllowedActions   []string     `json:"allowed_actions,omitempty"`
	Version          int          `json:"version"`
	CreatedAt        time.Time    `json:"created_at,omitempty"`
	UpdatedAt        time.Time    `json:"updated_at,omitempty"`
}

type PropertyType struct {
	ID              string         `json:"id"`
	TenantID        string         `json:"tenant_id,omitempty"`
	ObjectTypeID    string         `json:"object_type_id"`
	PropertyID      string         `json:"property_id"`
	DisplayName     string         `json:"display_name,omitempty"`
	ValueType       ValueType      `json:"value_type"`
	TemporalMode    TemporalMode   `json:"temporal_mode,omitempty"`
	AuthoritySource string         `json:"authority_source,omitempty"`
	ConflictPolicy  ConflictPolicy `json:"conflict_policy,omitempty"`
	Sensitivity     string         `json:"sensitivity,omitempty"`
	AllowedActions  []string       `json:"allowed_actions,omitempty"`
	Version         int            `json:"version"`
	CreatedAt       time.Time      `json:"created_at,omitempty"`
	UpdatedAt       time.Time      `json:"updated_at,omitempty"`
}

type MappingRule struct {
	ID                 string         `json:"id"`
	TenantID           string         `json:"tenant_id,omitempty"`
	EventType          string         `json:"event_type"`
	SourceDomain       string         `json:"source_domain,omitempty"`
	SourceRef          string         `json:"source_ref,omitempty"`
	EventSchemaVersion string         `json:"event_schema_version,omitempty"`
	RuleHash           string         `json:"rule_hash,omitempty"`
	ObjectTypes        []string       `json:"object_types,omitempty"`
	LinkTypes          []string       `json:"link_types,omitempty"`
	Rule               map[string]any `json:"rule,omitempty"`
	Enabled            bool           `json:"enabled"`
	Version            int            `json:"version"`
	CreatedAt          time.Time      `json:"created_at,omitempty"`
	UpdatedAt          time.Time      `json:"updated_at,omitempty"`
}
