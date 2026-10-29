package ontologyreasoning

import (
	"errors"
	"time"

	"orrery_v2/internal/app/actioncontract"
	"orrery_v2/internal/app/contextgraph"
	"orrery_v2/internal/app/ontology"
)

var (
	ErrInvalidRequest   = errors.New("invalid ontology reasoning request")
	ErrNotFound         = errors.New("ontology reasoning run not found")
	ErrValidationFailed = errors.New("ontology reasoning validation failed")
)

type CandidateKind string

const (
	CandidateEntity   CandidateKind = "entity"
	CandidateLink     CandidateKind = "link"
	CandidateFact     CandidateKind = "fact"
	CandidateMetadata CandidateKind = "metadata"
	CandidateAction   CandidateKind = "action"
)

type CandidateOperation string

const (
	OperationCreate  CandidateOperation = "create"
	OperationUpsert  CandidateOperation = "upsert"
	OperationUpdate  CandidateOperation = "update"
	OperationSuggest CandidateOperation = "suggest"
)

type RunStatus string

const (
	RunStatusDraft     RunStatus = "draft"
	RunStatusValidated RunStatus = "validated"
	RunStatusProposed  RunStatus = "proposed"
	RunStatusFailed    RunStatus = "failed"
)

type ValidationStatus string

const (
	ValidationAccepted       ValidationStatus = "accepted"
	ValidationRejected       ValidationStatus = "rejected"
	ValidationReviewRequired ValidationStatus = "review_required"
)

type ProposalStatus string

const (
	ProposalStatusProposed ProposalStatus = "proposed"
)

type ObjectRef struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}

type MetadataTarget struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
}

type Candidate struct {
	ID             string                     `json:"id,omitempty"`
	Kind           CandidateKind              `json:"kind"`
	Operation      CandidateOperation         `json:"operation"`
	ObjectType     string                     `json:"object_type,omitempty"`
	ObjectID       string                     `json:"object_id,omitempty"`
	CanonicalKey   string                     `json:"canonical_key,omitempty"`
	Label          string                     `json:"label,omitempty"`
	LinkType       string                     `json:"link_type,omitempty"`
	From           *ObjectRef                 `json:"from,omitempty"`
	To             *ObjectRef                 `json:"to,omitempty"`
	Subject        *ObjectRef                 `json:"subject,omitempty"`
	Predicate      string                     `json:"predicate,omitempty"`
	Value          any                        `json:"value,omitempty"`
	MetadataTarget *MetadataTarget            `json:"metadata_target,omitempty"`
	MetadataKey    string                     `json:"metadata_key,omitempty"`
	MetadataValue  any                        `json:"metadata_value,omitempty"`
	ActionTypeID   string                     `json:"action_type_id,omitempty"`
	ObjectRefs     []actioncontract.ObjectRef `json:"object_refs,omitempty"`
	Purpose        string                     `json:"purpose,omitempty"`
	Confidence     float64                    `json:"confidence,omitempty"`
	Reason         string                     `json:"reason,omitempty"`
	EvidenceRefs   []contextgraph.Ref         `json:"evidence_refs,omitempty"`
	Properties     map[string]any             `json:"properties,omitempty"`
}

type BuildContextPackRequest struct {
	TenantID     string             `json:"tenant_id"`
	Intent       string             `json:"intent,omitempty"`
	SubjectRefs  []contextgraph.Ref `json:"subject_refs,omitempty"`
	EvidenceRefs []contextgraph.Ref `json:"evidence_refs,omitempty"`
	Limit        int                `json:"limit,omitempty"`
	ActorRef     contextgraph.Ref   `json:"-"`
}

type ContextPack struct {
	// Current policy-visible identities for retracting an already owned link.
	// These never enter premises or validate new assertions.
	RetractionEndpoints  []ObjectRef                 `json:"retraction_endpoints,omitempty"`
	TenantID             string                      `json:"tenant_id"`
	IntentHash           string                      `json:"intent_hash,omitempty"`
	CandidateObjects     []contextgraph.Object       `json:"candidate_objects,omitempty"`
	NeighborLinks        []contextgraph.Link         `json:"neighbor_links,omitempty"`
	Facts                []contextgraph.Fact         `json:"facts,omitempty"`
	EvidenceRefs         []contextgraph.Ref          `json:"evidence_refs,omitempty"`
	Evidence             []contextgraph.Evidence     `json:"evidence,omitempty"`
	AllowedObjectTypes   []ontology.ObjectType       `json:"allowed_object_types,omitempty"`
	AllowedLinkTypes     []ontology.LinkType         `json:"allowed_link_types,omitempty"`
	AllowedPropertyTypes []ontology.PropertyType     `json:"allowed_property_types,omitempty"`
	AllowedActions       []actioncontract.ActionType `json:"allowed_actions,omitempty"`
	KnownConflicts       []ValidationIssue           `json:"known_conflicts,omitempty"`
	Hash                 string                      `json:"hash"`
	CreatedAt            time.Time                   `json:"created_at"`
}

type ValidationIssue struct {
	CandidateID string `json:"candidate_id,omitempty"`
	Code        string `json:"code"`
	Severity    string `json:"severity"`
	Message     string `json:"message"`
}

type ValidationResult struct {
	CandidateID string            `json:"candidate_id,omitempty"`
	Status      ValidationStatus  `json:"status"`
	Issues      []ValidationIssue `json:"issues,omitempty"`
}

type ReasoningProposal struct {
	ID               string             `json:"id"`
	TenantID         string             `json:"tenant_id"`
	RunID            string             `json:"run_id"`
	CandidateID      string             `json:"candidate_id"`
	Kind             CandidateKind      `json:"kind"`
	Status           ProposalStatus     `json:"status"`
	ValidationStatus ValidationStatus   `json:"validation_status"`
	Confidence       float64            `json:"confidence,omitempty"`
	Reason           string             `json:"reason,omitempty"`
	EvidenceRefs     []contextgraph.Ref `json:"evidence_refs,omitempty"`
	GovernanceRef    *contextgraph.Ref  `json:"governance_ref,omitempty"`
	CreatedAt        time.Time          `json:"created_at"`
}

type CreateRunRequest struct {
	Evaluation      *EvaluationRecord `json:"evaluation,omitempty"`
	TenantID        string            `json:"tenant_id"`
	Engine          string            `json:"engine,omitempty"`
	Model           string            `json:"model,omitempty"`
	ModelVersion    string            `json:"model_version,omitempty"`
	IntentHash      string            `json:"intent_hash,omitempty"`
	InputHash       string            `json:"input_hash,omitempty"`
	PromptHash      string            `json:"prompt_hash,omitempty"`
	ContextPackHash string            `json:"context_pack_hash,omitempty"`
	ContextPack     ContextPack       `json:"context_pack,omitempty"`
	Candidates      []Candidate       `json:"candidates,omitempty"`
}

type ReasoningRun struct {
	CurrentCandidates      []CandidateState    `json:"current_candidates,omitempty"`
	ID                     string              `json:"id"`
	TenantID               string              `json:"tenant_id"`
	Engine                 string              `json:"engine,omitempty"`
	Model                  string              `json:"model,omitempty"`
	ModelVersion           string              `json:"model_version,omitempty"`
	IntentHash             string              `json:"intent_hash,omitempty"`
	InputHash              string              `json:"input_hash,omitempty"`
	PromptHash             string              `json:"prompt_hash,omitempty"`
	ContextPackHash        string              `json:"context_pack_hash,omitempty"`
	Status                 RunStatus           `json:"status"`
	CandidateCount         int                 `json:"candidate_count"`
	ValidCandidateCount    int                 `json:"valid_candidate_count"`
	RejectedCandidateCount int                 `json:"rejected_candidate_count"`
	ReviewRequiredCount    int                 `json:"review_required_count"`
	Request                CreateRunRequest    `json:"request,omitempty"`
	ContextPack            ContextPack         `json:"context_pack,omitempty"`
	Candidates             []Candidate         `json:"candidates,omitempty"`
	Validation             []ValidationResult  `json:"validation,omitempty"`
	Proposals              []ReasoningProposal `json:"proposals,omitempty"`
	ErrorMessage           string              `json:"error_message,omitempty"`
	CreatedAt              time.Time           `json:"created_at"`
	UpdatedAt              time.Time           `json:"updated_at"`
	CompletedAt            *time.Time          `json:"completed_at,omitempty"`

	RawPrompt string `json:"-"`
	RawInput  string `json:"-"`
	RawOutput string `json:"-"`
}
