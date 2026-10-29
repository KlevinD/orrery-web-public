package knowledgeontology

import (
	"time"

	"orrery_v2/internal/app/contextgraph"
)

type ObjectRef struct {
	Kind string `yaml:"kind" json:"kind"`
	ID   string `yaml:"id" json:"id"`
}

type Assertion struct {
	LinkType string    `yaml:"link_type" json:"link_type"`
	From     ObjectRef `yaml:"from" json:"from"`
	To       ObjectRef `yaml:"to" json:"to"`
	Anchor   string    `yaml:"-" json:"anchor"`
}

type Extraction struct {
	Subjects   []ObjectRef `yaml:"subjects" json:"subjects"`
	Assertions []Assertion `yaml:"assertions" json:"assertions"`
}

type Resolution string

const (
	ResolutionResolved   Resolution = "resolved"
	ResolutionUnresolved Resolution = "unresolved"
	ResolutionAmbiguous  Resolution = "ambiguous"
)

type AssertionStatus string

const (
	AssertionConfirmed  AssertionStatus = "confirmed"
	AssertionSuggested  AssertionStatus = "suggested"
	AssertionUnresolved AssertionStatus = "unresolved"
)

type Evidence struct {
	KnowledgeBaseID  string    `json:"knowledge_base_id"`
	DocumentID       string    `json:"document_id"`
	Revision         int       `json:"revision"`
	ContentHash      string    `json:"content_hash"`
	Anchor           string    `json:"anchor"`
	ExtractionMethod string    `json:"extraction_method"`
	ObservedAt       time.Time `json:"observed_at"`
}

type SubjectProjection struct {
	Reference  ObjectRef       `json:"reference"`
	ObjectID   string          `json:"object_id,omitempty"`
	Resolution Resolution      `json:"resolution"`
	Status     AssertionStatus `json:"status"`
	Surface    string          `json:"surface,omitempty"`
	Confidence float64         `json:"confidence,omitempty"`
	Evidence   Evidence        `json:"evidence"`
}

type AssertionProjection struct {
	LinkType     string          `json:"link_type"`
	From         ObjectRef       `json:"from"`
	To           ObjectRef       `json:"to"`
	FromObjectID string          `json:"from_object_id,omitempty"`
	ToObjectID   string          `json:"to_object_id,omitempty"`
	Status       AssertionStatus `json:"status"`
	Evidence     Evidence        `json:"evidence"`
}

// DocumentReferenceLinkType is the canonical directed edge used for links
// between knowledge documents. Backlinks are derived by querying this edge in
// the incoming direction; Orrery does not persist a second reverse edge.
const DocumentReferenceLinkType = "knowledge_document_references_document"

type DocumentRelationDirection string

const (
	DocumentRelationIncoming DocumentRelationDirection = "in"
	DocumentRelationOutgoing DocumentRelationDirection = "out"
	DocumentRelationBoth     DocumentRelationDirection = "both"
)

type DocumentRelation struct {
	LinkType         string                    `json:"link_type"`
	Direction        DocumentRelationDirection `json:"direction"`
	OtherDocumentID  string                    `json:"other_document_id"`
	SourceDocumentID string                    `json:"source_document_id"`
	Revision         int                       `json:"revision"`
	Anchor           string                    `json:"anchor,omitempty"`
}

type Projection struct {
	TenantID                  string                  `json:"tenant_id"`
	KnowledgeBaseID           string                  `json:"knowledge_base_id"`
	DocumentID                string                  `json:"document_id"`
	KnowledgeDocumentObjectID string                  `json:"knowledge_document_object_id,omitempty"`
	Revision                  int                     `json:"revision"`
	ContentHash               string                  `json:"content_hash"`
	Subjects                  []SubjectProjection     `json:"subjects"`
	Assertions                []AssertionProjection   `json:"assertions"`
	ExistingLinks             []contextgraph.Link     `json:"-"`
	ExistingEvidence          []contextgraph.Evidence `json:"-"`
}

type ProjectRequest struct {
	TenantID        string
	KnowledgeBaseID string
	DocumentID      string
	Revision        int
	ContentHash     string
	PublishedAt     time.Time
	Source          string
	Extraction      Extraction
	Objects         []contextgraph.Object
	Links           []contextgraph.Link
	Facts           []contextgraph.Fact
	Evidence        []contextgraph.Evidence
}

type AssociationKind string

const (
	AssociationMention      AssociationKind = "mention"
	AssociationClaimSubject AssociationKind = "claim_subject"
	AssociationClaimObject  AssociationKind = "claim_object"
)

// KnowledgeAssociation is the indexed, bidirectional bridge between an
// ontology object and an exact knowledge document revision.
type KnowledgeAssociation struct {
	Kind             AssociationKind `json:"kind"`
	LinkType         string          `json:"link_type,omitempty"`
	ObjectRef        ObjectRef       `json:"object_ref"`
	KnowledgeBaseID  string          `json:"knowledge_base_id"`
	DocumentID       string          `json:"document_id"`
	Revision         int             `json:"revision"`
	Status           AssertionStatus `json:"status"`
	Surface          string          `json:"surface,omitempty"`
	Anchor           string          `json:"anchor,omitempty"`
	ExtractionMethod string          `json:"extraction_method,omitempty"`
	Confidence       float64         `json:"confidence,omitempty"`
}
