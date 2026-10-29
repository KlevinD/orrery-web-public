package core

import (
	"time"

	doccore "orrery_v2/internal/platform/document/core"
)

type KnowledgeBaseID string
type KnowledgeDocumentID string
type KnowledgeChunkID string
type KnowledgeBindingID string
type KnowledgeJobID string

type OwnerType string

const (
	OwnerAgent        OwnerType = "agent"
	OwnerHuman        OwnerType = "human"
	OwnerOrganization OwnerType = "organization"
	OwnerPosition     OwnerType = "position"
	OwnerGlobal       OwnerType = "global"
)

type BindingSubjectType string

const (
	BindingSubjectAgent        BindingSubjectType = "agent"
	BindingSubjectHuman        BindingSubjectType = "human"
	BindingSubjectOrganization BindingSubjectType = "organization"
	BindingSubjectPosition     BindingSubjectType = "position"
	BindingSubjectRole         BindingSubjectType = "role"
)

type Visibility string

const (
	VisibilityPrivate Visibility = "private"
	VisibilityScoped  Visibility = "scoped"
	VisibilityPublic  Visibility = "public"
)

type AccessLevel string

const (
	AccessRead   AccessLevel = "read"
	AccessWrite  AccessLevel = "write"
	AccessManage AccessLevel = "manage"
)

type KnowledgeStatus string

const (
	KnowledgeActive   KnowledgeStatus = "active"
	KnowledgeDisabled KnowledgeStatus = "disabled"
	KnowledgeDeleting KnowledgeStatus = "deleting"
)

type KnowledgeProvider string

const (
	ProviderPageIndex KnowledgeProvider = "pageindex"
	ProviderLocal     KnowledgeProvider = "local"
	ProviderMCP       KnowledgeProvider = "mcp"
)

type VectorBackend string

const (
	VectorBackendLanceDB VectorBackend = "lancedb"
)

type KnowledgeBase struct {
	ID                  KnowledgeBaseID
	Name                string
	Description         string
	OwnerType           OwnerType
	OwnerID             string
	IsDefault           bool
	Visibility          Visibility
	Provider            KnowledgeProvider
	ProviderResourceID  string
	ProviderMetadata    map[string]string
	VectorBackend       VectorBackend
	VectorStorePath     string
	EmbeddingProfileRef string
	EmbeddingModel      string
	EmbeddingDimensions int
	ChunkPolicy         doccore.ChunkPolicy
	Status              KnowledgeStatus
	CreatedBy           string
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

type KnowledgeBinding struct {
	ID              KnowledgeBindingID
	KnowledgeBaseID KnowledgeBaseID
	BoundToType     BindingSubjectType
	BoundToID       string
	Access          AccessLevel
	CreatedBy       string
	CreatedAt       time.Time
}

type DocumentStatus string

const (
	DocumentUploaded  DocumentStatus = "uploaded"
	DocumentParsing   DocumentStatus = "parsing"
	DocumentChunking  DocumentStatus = "chunking"
	DocumentEmbedding DocumentStatus = "embedding"
	DocumentIndexed   DocumentStatus = "indexed"
	DocumentFailed    DocumentStatus = "failed"
	DocumentDeleting  DocumentStatus = "deleting"
)

type KnowledgeDocument struct {
	ID                 KnowledgeDocumentID
	KnowledgeBaseID    KnowledgeBaseID
	ArtifactID         string
	ProviderDocumentID string
	ProviderMetadata   map[string]string
	Filename           string
	ContentType        string
	SizeBytes          int64
	Checksum           string
	Status             DocumentStatus
	ErrorMessage       string
	CreatedBy          string
	CreatedAt          time.Time
	UpdatedAt          time.Time
	IndexedAt          time.Time
}

type KnowledgeChunk struct {
	ID              KnowledgeChunkID
	KnowledgeBaseID KnowledgeBaseID
	DocumentID      KnowledgeDocumentID
	Ordinal         int
	HeadingPath     []string
	StartOffset     int
	EndOffset       int
	ContentHash     string
	VectorID        string
	TokenEstimate   int
	CreatedAt       time.Time
}

type JobStatus string

const (
	JobQueued    JobStatus = "queued"
	JobRunning   JobStatus = "running"
	JobCompleted JobStatus = "completed"
	JobFailed    JobStatus = "failed"
)

type KnowledgeIngestJob struct {
	ID              KnowledgeJobID
	KnowledgeBaseID KnowledgeBaseID
	DocumentID      KnowledgeDocumentID
	Topic           string
	Status          JobStatus
	ErrorMessage    string
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

type KnowledgeBaseQuery struct {
	OwnerType  OwnerType
	OwnerID    string
	Status     KnowledgeStatus
	Visibility Visibility
	Limit      int
}

type KnowledgeBasePage struct {
	Items []KnowledgeBase
	Total int
}

type BindingQuery struct {
	KnowledgeBaseID KnowledgeBaseID
	BoundToType     BindingSubjectType
	BoundToID       string
}

type DocumentQuery struct {
	KnowledgeBaseID KnowledgeBaseID
	Status          DocumentStatus
	Limit           int
}

type DocumentPage struct {
	Items []KnowledgeDocument
	Total int
}

type ChunkQuery struct {
	KnowledgeBaseID KnowledgeBaseID
	DocumentID      KnowledgeDocumentID
	Limit           int
}

type JobQuery struct {
	KnowledgeBaseID KnowledgeBaseID
	DocumentID      KnowledgeDocumentID
	Status          JobStatus
	Limit           int
}
