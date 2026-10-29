// Package knowledgeinstances owns document-derived identities and assertions.
// It never edits the source records of a business domain.
package knowledgeinstances

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"time"

	"orrery_v2/internal/app/contextgraph"
)

const (
	EntityType    = "knowledge_entity"
	Domain        = "knowledge_instances"
	ActionID      = "ontology.knowledge_instances.apply"
	DocumentLink  = "knowledge_document_describes_entity"
	RelatedLink   = "knowledge_entity_related_to"
	DocumentTopic = "ontology.document.extract"
)

var (
	ErrNotFound = errors.New("knowledge instance record not found")
	ErrConflict = errors.New("knowledge instance input changed")
	ErrInvalid  = errors.New("invalid knowledge instance command")
)

type Source struct {
	Generation      int64                  `json:"generation,omitempty"`
	TenantID        string                 `json:"tenant_id"`
	RootID          string                 `json:"root_id"`
	KnowledgeBaseID string                 `json:"knowledge_base_id"`
	FolderID        string                 `json:"folder_id"`
	DocumentID      string                 `json:"document_id"`
	Revision        int                    `json:"revision"`
	ContentHash     string                 `json:"content_hash"`
	Content         string                 `json:"content"`
	Access          contextgraph.AccessRef `json:"access"`
}

// SourceStamp excludes text because ContentHash has already been verified.
func (s Source) Stamp() string { s.Content = ""; return Digest(s) }

type Property struct {
	Validity  *Validity `json:"validity,omitempty"`
	Predicate string    `json:"predicate"`
	Value     any       `json:"value"`
	Quote     string    `json:"quote"`
}

type EntityInput struct {
	Identifiers []IdentifierInput `json:"identifiers,omitempty"`
	Ref         string            `json:"ref"`
	ExistingID  string            `json:"existing_id,omitempty"`
	Kind        string            `json:"kind"`
	Name        string            `json:"name"`
	Quote       string            `json:"quote"`
	Properties  []Property        `json:"properties"`
}

type RelationInput struct {
	Validity     *Validity `json:"validity,omitempty"`
	RelationCode string    `json:"relation_code,omitempty"`
	From         string    `json:"from"`
	To           string    `json:"to"`
	Relation     string    `json:"relation"`
	Quote        string    `json:"quote"`
}

type Extraction struct {
	Defer     bool            `json:"defer"`
	Reason    string          `json:"reason"`
	Entities  []EntityInput   `json:"entities"`
	Relations []RelationInput `json:"relations"`
}

type Entity struct {
	IdentityKeys   []Identifier           `json:"identity_keys,omitempty"`
	ID             string                 `json:"id"`
	TenantID       string                 `json:"tenant_id"`
	Key            string                 `json:"key"`
	Kind           string                 `json:"kind"`
	Name           string                 `json:"name"`
	Access         contextgraph.AccessRef `json:"access"`
	Version        int64                  `json:"version"`
	Properties     map[string]any         `json:"properties"`
	Conflicts      []string               `json:"conflicts,omitempty"`
	Status         string                 `json:"status"`
	ProjectionHash string                 `json:"projection_hash,omitempty"`
	Resolutions    []ResolutionRecord     `json:"resolutions,omitempty"`
}

type Claim struct {
	Validity       *Validity `json:"validity,omitempty"`
	Source         *Source   `json:"source,omitempty"`
	RelationCode   string    `json:"relation_code,omitempty"`
	ID             string    `json:"id"`
	EntityID       string    `json:"entity_id"`
	Predicate      string    `json:"predicate"`
	Value          any       `json:"value"`
	TargetID       string    `json:"target_id,omitempty"`
	Quote          string    `json:"quote"`
	ProjectionHash string    `json:"projection_hash,omitempty"`
}

// Plan is immutable and records exactly what the independent model reviews.
// Before contains every identity touched by this document replacement.
type Plan struct {
	TemporalProtocol    string               `json:"temporal_protocol,omitempty"`
	IdentityAssignments []IdentityAssignment `json:"identity_assignments,omitempty"`
	CandidateRecall     *RecallSummary       `json:"candidate_recall,omitempty"`
	ClaimSetHash        string               `json:"claim_set_hash,omitempty"`
	Conflicts           []ConflictCase       `json:"conflicts,omitempty"`
	ExtractionReason    string               `json:"extraction_reason"`
	ExtractorModelID    string               `json:"extractor_model_id"`
	ID                  string               `json:"id"`
	BatchID             string               `json:"batch_id"`
	Watch               Watch                `json:"watch"`
	Source              Source               `json:"source"`
	SchemaHash          string               `json:"schema_hash"`
	AuthorizationHash   string               `json:"authorization_hash"`
	Before              map[string]int64     `json:"before"`
	Entities            []Entity             `json:"entities"`
	Claims              []Claim              `json:"claims"`
	Previous            []Claim              `json:"previous_claims"`
	CreatedAt           time.Time            `json:"created_at"`
}

type Decision struct {
	Resolutions []ResolutionChoice `json:"resolutions,omitempty"`
	Decision    string             `json:"decision"`
	Reason      string             `json:"reason"`
	ModelID     string             `json:"model_id"`
	PlanHash    string             `json:"plan_hash"`
}

type Receipt struct {
	ResolutionCount int       `json:"resolution_count,omitempty"`
	ID              string    `json:"id"`
	BatchID         string    `json:"batch_id"`
	PlanHash        string    `json:"plan_hash"`
	ActionRunID     string    `json:"action_run_id"`
	EntityIDs       []string  `json:"entity_ids"`
	ClaimCount      int       `json:"claim_count"`
	AppliedAt       time.Time `json:"applied_at"`
}

type Batch struct {
	ID       string    `json:"id"`
	TenantID string    `json:"tenant_id"`
	WatchID  string    `json:"watch_id"`
	Status   string    `json:"status"`
	Plan     Plan      `json:"plan"`
	Decision *Decision `json:"decision,omitempty"`
	Receipt  *Receipt  `json:"receipt,omitempty"`
}

func Digest(value any) string {
	body, _ := json.Marshal(value)
	return fmtHash(sha256.Sum256(body))
}

func fmtHash(sum [32]byte) string {
	const hex = "0123456789abcdef"
	b := make([]byte, 64)
	for i, v := range sum {
		b[2*i] = hex[v>>4]
		b[2*i+1] = hex[v&15]
	}
	return string(b)
}

func stableID(prefix string, value any) string { return prefix + Digest(value) }
func body(value any) string                    { b, _ := json.Marshal(value); return string(b) }
