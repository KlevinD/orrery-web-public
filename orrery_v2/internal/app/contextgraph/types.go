package contextgraph

import (
	"context"
	"strings"
	"time"
)

type Ref struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
}

type TemporalMetadata struct {
	BusinessTimeUnspecified bool       `json:"business_time_unspecified,omitempty"`
	Mode                    string     `json:"mode,omitempty"`
	BusinessValidFrom       time.Time  `json:"business_valid_from,omitempty"`
	BusinessValidTo         *time.Time `json:"business_valid_to,omitempty"`
	SourceRecordedAt        time.Time  `json:"source_recorded_at,omitempty"`
	SourceSupersededAt      *time.Time `json:"source_superseded_at,omitempty"`
	OrreryIngestedAt        time.Time  `json:"orrery_ingested_at,omitempty"`
}

type Object struct {
	ID             string           `json:"id"`
	TenantID       string           `json:"tenant_id"`
	ObjectType     string           `json:"object_type"`
	CanonicalKey   string           `json:"canonical_key"`
	Label          string           `json:"label"`
	Properties     map[string]any   `json:"properties,omitempty"`
	Sensitivity    string           `json:"sensitivity,omitempty"`
	Confidence     float64          `json:"confidence,omitempty"`
	Status         string           `json:"status,omitempty"`
	Provenance     Ref              `json:"provenance_ref,omitempty"`
	EvidenceRefs   []Ref            `json:"evidence_refs,omitempty"`
	OwnerAccessRef AccessRef        `json:"-"`
	AccessState    AccessState      `json:"-"`
	SourceStreamID string           `json:"-"`
	SourceVersion  int64            `json:"-"`
	Temporal       TemporalMetadata `json:"temporal,omitempty"`
	UpdatedAt      time.Time        `json:"updated_at,omitempty"`
}

type Link struct {
	ID              string           `json:"id"`
	TenantID        string           `json:"tenant_id"`
	LinkType        string           `json:"link_type"`
	FromObjectID    string           `json:"from_object_id"`
	ToObjectID      string           `json:"to_object_id"`
	Properties      map[string]any   `json:"properties,omitempty"`
	Confidence      float64          `json:"confidence,omitempty"`
	Status          string           `json:"status,omitempty"`
	Provenance      Ref              `json:"provenance_ref,omitempty"`
	EvidenceRefs    []Ref            `json:"evidence_refs,omitempty"`
	SourceAccessRef AccessRef        `json:"-"`
	AccessState     AccessState      `json:"-"`
	SourceStreamID  string           `json:"-"`
	SourceVersion   int64            `json:"-"`
	Temporal        TemporalMetadata `json:"temporal,omitempty"`
	UpdatedAt       time.Time        `json:"updated_at,omitempty"`
}

type Fact struct {
	ID               string           `json:"id"`
	TenantID         string           `json:"tenant_id"`
	Subject          Ref              `json:"subject"`
	Predicate        string           `json:"predicate"`
	Value            any              `json:"value"`
	Properties       map[string]any   `json:"properties,omitempty"`
	Confidence       float64          `json:"confidence,omitempty"`
	Status           string           `json:"status,omitempty"`
	Provenance       Ref              `json:"provenance_ref,omitempty"`
	EvidenceRefs     []Ref            `json:"evidence_refs,omitempty"`
	SourceAccessRef  AccessRef        `json:"-"`
	SubjectAccessRef AccessRef        `json:"-"`
	AccessState      AccessState      `json:"-"`
	SourceStreamID   string           `json:"-"`
	SourceVersion    int64            `json:"-"`
	Temporal         TemporalMetadata `json:"temporal,omitempty"`
	UpdatedAt        time.Time        `json:"updated_at,omitempty"`
}

type Evidence struct {
	ID              string      `json:"id"`
	TenantID        string      `json:"tenant_id"`
	SourceRef       Ref         `json:"source_ref"`
	SourceAccessRef AccessRef   `json:"-"`
	AccessState     AccessState `json:"-"`
	Summary         string      `json:"summary"`
	Confidence      float64     `json:"confidence,omitempty"`
	ObservedAt      time.Time   `json:"observed_at,omitempty"`
}

func EvidenceRecordIDsForRefs(refs []Ref) []string {
	seen := make(map[string]struct{}, len(refs))
	ids := make([]string, 0, len(refs))
	for _, ref := range refs {
		if !evidenceRefUsesRecordID(ref) {
			continue
		}
		id := strings.TrimSpace(ref.ID)
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	return ids
}

func EvidenceMatchesRef(evidence Evidence, ref Ref) bool {
	ref.Kind = strings.ToLower(strings.TrimSpace(ref.Kind))
	ref.ID = strings.TrimSpace(ref.ID)
	if evidenceRefUsesRecordID(ref) {
		return strings.TrimSpace(evidence.ID) == ref.ID
	}
	source := Ref{Kind: strings.ToLower(strings.TrimSpace(evidence.SourceRef.Kind)), ID: strings.TrimSpace(evidence.SourceRef.ID)}
	return source == ref
}

func evidenceRefUsesRecordID(ref Ref) bool {
	switch strings.ToLower(strings.TrimSpace(ref.Kind)) {
	case "event", "evidence":
		return strings.TrimSpace(ref.ID) != ""
	default:
		return false
	}
}

type ContextGraphSnapshot struct {
	Objects  []Object   `json:"objects"`
	Links    []Link     `json:"links"`
	Facts    []Fact     `json:"facts"`
	Evidence []Evidence `json:"evidence"`
}

type ProjectionAudit struct {
	ID              string         `json:"id"`
	TenantID        string         `json:"tenant_id"`
	SourceEventID   string         `json:"source_event_id"`
	SourceEventType string         `json:"source_event_type"`
	Status          string         `json:"status"`
	ReasonCode      string         `json:"reason_code"`
	Reason          string         `json:"reason,omitempty"`
	Details         map[string]any `json:"details,omitempty"`
	SourceAccessRef AccessRef      `json:"-"`
	AccessState     AccessState    `json:"-"`
	ObservedAt      time.Time      `json:"observed_at,omitempty"`
	Revision        int64          `json:"-"`
}

type ProjectionAuditCommandState string

const (
	ProjectionAuditCommandClaimed     ProjectionAuditCommandState = "claimed"
	ProjectionAuditCommandSucceeded   ProjectionAuditCommandState = "succeeded"
	ProjectionAuditCommandFailed      ProjectionAuditCommandState = "failed"
	ProjectionAuditCommandReplay                                  = "replay"
	ProjectionAuditCommandAcknowledge                             = "acknowledge"
)

type ProjectionAuditCommandClaimRequest struct {
	Audit          ProjectionAudit
	Operation      string
	IdempotencyKey string
	RequestHash    string
}

type ProjectionAuditCommandClaim struct {
	Acquired       bool
	State          ProjectionAuditCommandState
	LeaseToken     string
	LeaseExpiresAt *time.Time
	ResultStatus   string
	ReasonCode     string
	ObservedAt     *time.Time
	FailureCode    string
}

type ProjectionAuditCommandCompletion struct {
	TenantID       string
	AuditID        string
	Operation      string
	IdempotencyKey string
	RequestHash    string
	LeaseToken     string
	Result         ProjectionAudit
	History        *ProjectionAuditReplayHistory
	Effect         func(context.Context, WriterStore) error
	Fence          ProjectionAuditCommandFence
}

type ProjectionAuditCommandFence struct {
	TenantID                string
	BindingVersion          int64
	PersistedBindingVersion int64
	PersistenceRequired     bool
	AuthorizationRevision   string
	PolicyRevision          string
}

type ProjectionAuditCommandFailure struct {
	TenantID       string
	AuditID        string
	Operation      string
	IdempotencyKey string
	RequestHash    string
	LeaseToken     string
	FailureCode    string
	Result         *ProjectionAudit
	History        *ProjectionAuditReplayHistory
	Fence          *ProjectionAuditCommandFence
}

type ProjectionAuditReplayHistory struct {
	ID              string         `json:"id"`
	AuditID         string         `json:"audit_id"`
	TenantID        string         `json:"tenant_id"`
	SourceEventID   string         `json:"source_event_id"`
	SourceEventType string         `json:"source_event_type"`
	Status          string         `json:"status"`
	Error           string         `json:"error,omitempty"`
	Details         map[string]any `json:"details,omitempty"`
	SourceAccessRef AccessRef      `json:"-"`
	AccessState     AccessState    `json:"-"`
	AttemptedAt     time.Time      `json:"attempted_at,omitempty"`
}

type ProjectionAuditAcknowledgeRequest struct {
	ActorKind string `json:"actor_kind,omitempty"`
	ActorID   string `json:"actor_id,omitempty"`
	Reason    string `json:"reason,omitempty"`
}

type ProjectionClearReport struct {
	Objects                 int `json:"objects"`
	Links                   int `json:"links"`
	Facts                   int `json:"facts"`
	Evidence                int `json:"evidence"`
	ProjectionAudits        int `json:"projection_audits"`
	ProjectionReplayHistory int `json:"projection_replay_history"`
}

type PermissionConstraint struct {
	Scope        string   `json:"scope,omitempty"`
	ObjectRef    Ref      `json:"object_ref,omitempty"`
	MaskedFields []string `json:"masked_fields,omitempty"`
	Reason       string   `json:"reason,omitempty"`
}

type PermissionDecision struct {
	HiddenObjectIDs []string               `json:"hidden_object_ids,omitempty"`
	MaskedFields    map[string][]string    `json:"masked_fields,omitempty"`
	Constraints     []PermissionConstraint `json:"constraints,omitempty"`
}

type Freshness struct {
	AsOf         time.Time `json:"as_of"`
	BusinessAsOf time.Time `json:"business_as_of,omitempty"`
	KnownAt      time.Time `json:"known_at,omitempty"`
}

type BuildTrace struct {
	SeedObjectIDs   []string `json:"seed_object_ids,omitempty"`
	IncludedKinds   []string `json:"included_kinds,omitempty"`
	HiddenObjectIDs []string `json:"hidden_object_ids,omitempty"`
	MaskedFields    []string `json:"masked_fields,omitempty"`
	KnowledgeQuery  string   `json:"knowledge_query,omitempty"`
}

type KnowledgeEvidence struct {
	KnowledgeBaseID      string                `json:"knowledge_base_id"`
	DocumentID           string                `json:"document_id"`
	ChunkID              string                `json:"chunk_id,omitempty"`
	Revision             int                   `json:"revision"`
	ContentHash          string                `json:"content_hash"`
	Snippet              string                `json:"snippet"`
	HeadingPath          []string              `json:"heading_path,omitempty"`
	DocumentName         string                `json:"document_name,omitempty"`
	Score                float32               `json:"score,omitempty"`
	OntologyAssociations []OntologyAssociation `json:"ontology_associations,omitempty"`
}

type OntologyAssociation struct {
	Kind             string  `json:"kind"`
	LinkType         string  `json:"link_type,omitempty"`
	ObjectRef        Ref     `json:"object_ref"`
	Status           string  `json:"status"`
	Surface          string  `json:"surface,omitempty"`
	Anchor           string  `json:"anchor,omitempty"`
	ExtractionMethod string  `json:"extraction_method,omitempty"`
	Confidence       float64 `json:"confidence,omitempty"`
}

type ContextBundle struct {
	BundleID              string                 `json:"bundle_id"`
	TenantID              string                 `json:"tenant_id"`
	Consumer              string                 `json:"consumer"`
	Purpose               string                 `json:"purpose"`
	ActorRef              Ref                    `json:"actor_ref"`
	Schema                SchemaDictionary       `json:"schema,omitempty"`
	Objects               []Object               `json:"objects"`
	Links                 []Link                 `json:"links"`
	Facts                 []Fact                 `json:"facts"`
	Evidence              []Evidence             `json:"evidence"`
	KnowledgeEvidence     []KnowledgeEvidence    `json:"knowledge_evidence,omitempty"`
	Summaries             []string               `json:"summaries,omitempty"`
	PermissionConstraints []PermissionConstraint `json:"permission_constraints,omitempty"`
	Freshness             Freshness              `json:"freshness"`
	Confidence            float64                `json:"confidence"`
	BuildTrace            BuildTrace             `json:"build_trace"`
	Partial               bool                   `json:"partial,omitempty"`
	PartialReason         string                 `json:"partial_reason,omitempty"`
	Truncated             ContextBundleTruncated `json:"truncated,omitempty"`
}

type ContextBundleTruncated struct {
	Objects  int `json:"objects,omitempty"`
	Links    int `json:"links,omitempty"`
	Facts    int `json:"facts,omitempty"`
	Evidence int `json:"evidence,omitempty"`
}

type SchemaDictionary struct {
	ObjectTypes   []SchemaObjectType   `json:"object_types,omitempty"`
	LinkTypes     []SchemaLinkType     `json:"link_types,omitempty"`
	PropertyTypes []SchemaPropertyType `json:"property_types,omitempty"`
	ActionTypes   []SchemaActionType   `json:"action_types,omitempty"`
	VersionHash   string               `json:"version_hash,omitempty"`
}

type SchemaObjectType struct {
	ID               string   `json:"id"`
	TenantID         string   `json:"tenant_id,omitempty"`
	DisplayName      string   `json:"display_name,omitempty"`
	CanonicalKeyRule string   `json:"canonical_key_rule,omitempty"`
	OwnerDomain      string   `json:"owner_domain,omitempty"`
	TemporalMode     string   `json:"temporal_mode,omitempty"`
	Sensitivity      string   `json:"sensitivity,omitempty"`
	AllowedLinkTypes []string `json:"allowed_link_types,omitempty"`
	AllowedActions   []string `json:"allowed_actions,omitempty"`
}

type SchemaLinkType struct {
	ID               string   `json:"id"`
	TenantID         string   `json:"tenant_id,omitempty"`
	DisplayName      string   `json:"display_name,omitempty"`
	FromObjectType   string   `json:"from_object_type,omitempty"`
	ToObjectType     string   `json:"to_object_type,omitempty"`
	Cardinality      string   `json:"cardinality,omitempty"`
	TemporalMode     string   `json:"temporal_mode,omitempty"`
	EvidenceRequired bool     `json:"evidence_required,omitempty"`
	AllowedActions   []string `json:"allowed_actions,omitempty"`
}

type SchemaPropertyType struct {
	ID              string   `json:"id"`
	TenantID        string   `json:"tenant_id,omitempty"`
	ObjectTypeID    string   `json:"object_type_id"`
	PropertyID      string   `json:"property_id"`
	DisplayName     string   `json:"display_name,omitempty"`
	ValueType       string   `json:"value_type,omitempty"`
	TemporalMode    string   `json:"temporal_mode,omitempty"`
	AuthoritySource string   `json:"authority_source,omitempty"`
	ConflictPolicy  string   `json:"conflict_policy,omitempty"`
	Sensitivity     string   `json:"sensitivity,omitempty"`
	AllowedActions  []string `json:"allowed_actions,omitempty"`
}

type SchemaActionType struct {
	ID               string             `json:"id"`
	TenantID         string             `json:"tenant_id,omitempty"`
	Name             string             `json:"name,omitempty"`
	Category         string             `json:"category,omitempty"`
	Description      string             `json:"description,omitempty"`
	ObjectTypeRefs   []string           `json:"object_type_refs,omitempty"`
	ProviderKind     string             `json:"provider_kind,omitempty"`
	ProviderRef      string             `json:"provider_ref,omitempty"`
	ResourceResolver string             `json:"resource_resolver,omitempty"`
	PermissionScope  string             `json:"permission_scope,omitempty"`
	FieldProfile     string             `json:"field_profile,omitempty"`
	DataPolicy       string             `json:"data_policy,omitempty"`
	PurposePolicy    string             `json:"purpose_policy,omitempty"`
	RiskLevel        string             `json:"risk_level,omitempty"`
	DryRunSupported  bool               `json:"dry_run_supported,omitempty"`
	Verbs            []string           `json:"verbs,omitempty"`
	Roles            []SchemaActionRole `json:"roles,omitempty"`
	Preconditions    []string           `json:"preconditions,omitempty"`
	Effects          []string           `json:"effects,omitempty"`
	DecomposesInto   []string           `json:"decomposes_into,omitempty"`
	KnowledgeTerms   []string           `json:"knowledge_terms,omitempty"`
	MemoryTerms      []string           `json:"memory_terms,omitempty"`
	Enabled          bool               `json:"enabled"`
	Version          string             `json:"version,omitempty"`
}

type SchemaActionRole struct {
	Name       string `json:"name"`
	ObjectType string `json:"object_type,omitempty"`
	Required   bool   `json:"required,omitempty"`
}

type BuildContextBundleRequest struct {
	TenantID         string    `json:"tenant_id"`
	Consumer         string    `json:"consumer"`
	Purpose          string    `json:"purpose"`
	ActorRef         Ref       `json:"actor_ref"`
	SubjectRefs      []Ref     `json:"subject_refs"`
	Query            string    `json:"query,omitempty"`
	IncludeKinds     []string  `json:"include_kinds,omitempty"`
	MaxDepth         int       `json:"max_depth,omitempty"`
	TokenBudget      int       `json:"token_budget,omitempty"`
	BusinessAsOf     time.Time `json:"business_as_of,omitempty"`
	KnownAt          time.Time `json:"known_at,omitempty"`
	PermissionScopes []string  `json:"-"`
}
