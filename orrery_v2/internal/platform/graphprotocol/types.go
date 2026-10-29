package graphprotocol

import (
	"context"
	"time"
)

type ActorRef struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
}

type ObjectRef struct {
	TenantID string `json:"tenant_id"`
	Type     string `json:"type"`
	ID       string `json:"id"`
}

type AccessRef struct {
	Kind     string `json:"kind"`
	ID       string `json:"id"`
	TenantID string `json:"tenant_id"`
}

type ProvenanceRef struct {
	SourceEventID   string          `json:"source_event_id"`
	SourceEventType string          `json:"source_event_type"`
	SourceStreamID  string          `json:"source_stream_id,omitempty"`
	SourceVersion   int64           `json:"source_version,omitempty"`
	SourceAccessRef AccessRef       `json:"source_access_ref,omitempty"`
	SourceSystem    string          `json:"source_system"`
	Actor           ActorRef        `json:"actor_ref"`
	ObservedAt      time.Time       `json:"observed_at"`
	Temporal        TemporalContext `json:"temporal,omitempty"`
}

type TemporalContext struct {
	Mode               string     `json:"mode,omitempty"`
	BusinessValidFrom  time.Time  `json:"business_valid_from,omitempty"`
	BusinessValidTo    *time.Time `json:"business_valid_to,omitempty"`
	SourceRecordedAt   time.Time  `json:"source_recorded_at,omitempty"`
	SourceSupersededAt *time.Time `json:"source_superseded_at,omitempty"`
	OrreryIngestedAt   time.Time  `json:"orrery_ingested_at,omitempty"`
}

type DomainEvent struct {
	ID              string          `json:"id"`
	SourceEventID   string          `json:"source_event_id,omitempty"`
	TenantID        string          `json:"tenant_id"`
	Type            string          `json:"type"`
	SchemaVersion   string          `json:"schema_version,omitempty"`
	StreamID        string          `json:"stream_id,omitempty"`
	Version         int64           `json:"version"`
	Operation       string          `json:"operation,omitempty"`
	CorrelationID   string          `json:"correlation_id,omitempty"`
	CausationID     string          `json:"causation_id,omitempty"`
	OccurredAt      time.Time       `json:"occurred_at,omitempty"`
	Payload         map[string]any  `json:"payload,omitempty"`
	PublishedBy     ActorRef        `json:"published_by,omitempty"`
	SourceAccessRef AccessRef       `json:"source_access_ref,omitempty"`
	Temporal        TemporalContext `json:"temporal,omitempty"`
}

type Assertion interface {
	assertionKind() string
	provenanceRef() ProvenanceRef
	tenantID() string
}

type ObjectAssertion struct {
	Object       ObjectRef      `json:"object"`
	CanonicalKey string         `json:"canonical_key"`
	Label        string         `json:"label,omitempty"`
	Properties   map[string]any `json:"properties,omitempty"`
	Provenance   ProvenanceRef  `json:"provenance_ref"`
}

func (a ObjectAssertion) assertionKind() string        { return "object" }
func (a ObjectAssertion) provenanceRef() ProvenanceRef { return a.Provenance }
func (a ObjectAssertion) tenantID() string             { return a.Object.TenantID }

type LinkAssertion struct {
	Identity   string         `json:"identity,omitempty"`
	LinkType   string         `json:"link_type"`
	From       ObjectRef      `json:"from"`
	To         ObjectRef      `json:"to"`
	Properties map[string]any `json:"properties,omitempty"`
	Provenance ProvenanceRef  `json:"provenance_ref"`
}

func (a LinkAssertion) assertionKind() string        { return "link" }
func (a LinkAssertion) provenanceRef() ProvenanceRef { return a.Provenance }
func (a LinkAssertion) tenantID() string             { return a.From.TenantID }

type FactAssertion struct {
	Subject    ObjectRef      `json:"subject"`
	Predicate  string         `json:"predicate"`
	Value      any            `json:"value"`
	Properties map[string]any `json:"properties,omitempty"`
	Provenance ProvenanceRef  `json:"provenance_ref"`
}

func (a FactAssertion) assertionKind() string        { return "fact" }
func (a FactAssertion) provenanceRef() ProvenanceRef { return a.Provenance }
func (a FactAssertion) tenantID() string             { return a.Subject.TenantID }

type ProjectionSpec struct {
	Name                string   `json:"name"`
	Version             string   `json:"version"`
	TenantID            string   `json:"tenant_id,omitempty"`
	SourceType          string   `json:"source_type"`
	SourceSchemaVersion string   `json:"source_schema_version,omitempty"`
	ObjectTypes         []string `json:"object_types,omitempty"`
	LinkTypes           []string `json:"link_types,omitempty"`
	FactPredicates      []string `json:"fact_predicates,omitempty"`
}

type ProjectionCursor struct {
	SourceID      string `json:"source_id"`
	SourceVersion int64  `json:"source_version"`
}

type ProjectionResult struct {
	Cursor     ProjectionCursor `json:"cursor"`
	Assertions []Assertion      `json:"assertions"`
}

type GraphProjection interface {
	Spec() ProjectionSpec
	Project(context.Context, DomainEvent) (ProjectionResult, error)
}

type StaticProjection struct {
	SpecValue   ProjectionSpec
	ProjectFunc func(context.Context, DomainEvent) (ProjectionResult, error)
}

func (p StaticProjection) Spec() ProjectionSpec {
	return p.SpecValue
}

func (p StaticProjection) Project(ctx context.Context, event DomainEvent) (ProjectionResult, error) {
	if p.ProjectFunc == nil {
		return ProjectionResult{}, nil
	}
	return p.ProjectFunc(ctx, event)
}
