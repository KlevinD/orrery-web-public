package provider

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"orrery_v2/internal/knowledge/core"
)

var ErrChangeCursorExpired = errors.New("knowledge change cursor expired")

type Registry interface {
	Default() Provider
	ForBase(base core.KnowledgeBase) Provider
	Provider(name core.KnowledgeProvider) Provider
}

type Provider interface {
	Name() core.KnowledgeProvider
	EnsureKnowledgeBase(ctx context.Context, req EnsureKnowledgeBaseRequest) (EnsureKnowledgeBaseResult, error)
	DeleteKnowledgeBase(ctx context.Context, req DeleteKnowledgeBaseRequest) error
	UploadDocument(ctx context.Context, req UploadDocumentRequest) (UploadDocumentResult, error)
	CreateDocumentMetadata(ctx context.Context, req CreateDocumentMetadataRequest) (UploadDocumentResult, error)
	DeleteDocument(ctx context.Context, req DeleteDocumentRequest) error
	RefreshDocumentStatus(ctx context.Context, req DocumentStatusRequest) (DocumentStatusResult, error)
	ReprocessDocument(ctx context.Context, req ReprocessDocumentRequest) (ReprocessDocumentResult, error)
	GetDocumentProcessingResult(ctx context.Context, req DocumentProcessingResultRequest) (DocumentProcessingResult, error)
	ReindexKnowledgeBase(ctx context.Context, req ReindexKnowledgeBaseRequest) (ReindexKnowledgeBaseResult, error)
	Search(ctx context.Context, req SearchRequest) (SearchResultPage, error)
}

type RevisionProvider interface {
	UpdateDocumentContent(ctx context.Context, req UpdateDocumentContentRequest) (DocumentRevision, error)
	ListDocumentRevisions(ctx context.Context, req DocumentRevisionsRequest) ([]DocumentRevision, error)
	GetDocumentRevision(ctx context.Context, req DocumentRevisionRequest) (DocumentRevision, error)
	GetSemanticSource(ctx context.Context, req SemanticSourceRequest) (SemanticSource, error)
	PublishDocumentRevision(ctx context.Context, req PublishDocumentRevisionRequest) (DocumentRevision, error)
	ListKnowledgeChanges(ctx context.Context, req KnowledgeChangesRequest) (KnowledgeChangePage, error)
}

type FolderDocumentLister interface {
	ListFolderDocuments(context.Context, FolderDocumentsRequest) ([]FolderDocument, error)
}

type FolderDocumentsRequest struct {
	FolderID string
}

type FolderDocument struct {
	DocumentID   string
	FolderID     string
	Filename     string
	ContentType  string
	SizeBytes    int64
	Checksum     string
	Status       string
	KnowledgeKey string
	Source       string
	Title        string
}

type IndexedDocumentSourceProvider interface {
	GetIndexedDocumentSource(context.Context, IndexedDocumentSourceRequest) (IndexedDocumentSource, error)
}

type IndexedDocumentSourceRequest struct {
	DocumentID string
}

type IndexedDocumentSource struct {
	Revision DocumentRevision
	Source   SemanticSource
}

type ScopedSemanticSourceProvider interface {
	GetSemanticSourceForDocument(context.Context, core.KnowledgeBase, core.KnowledgeDocument, int) (SemanticSource, error)
}

type UpdateDocumentContentRequest struct {
	DocumentID       string
	ExpectedRevision int
	Content          string
	ContentType      string
	CreatedBy        string
}

type DocumentRevisionsRequest struct {
	DocumentID string
}

type DocumentRevisionRequest struct {
	DocumentID string
	Revision   int
}

type SemanticSourceRequest struct {
	DocumentID string
	Revision   int
}

type PublishDocumentRevisionRequest struct {
	DocumentID  string
	Revision    int
	PublishedBy string
}

type KnowledgeChangesRequest struct {
	FolderID string
	Cursor   string
	Limit    int
}

type DocumentRevision struct {
	ID                string     `json:"id"`
	DocumentID        string     `json:"document_id"`
	FolderID          string     `json:"folder_id"`
	Revision          int        `json:"revision"`
	ContentHash       string     `json:"content_hash"`
	ContentType       string     `json:"content_type"`
	SizeBytes         int64      `json:"size_bytes"`
	CreatedBy         string     `json:"created_by"`
	CreatedAt         time.Time  `json:"created_at"`
	PublicationStatus string     `json:"publication_status"`
	PublishedBy       string     `json:"published_by"`
	PublishedAt       *time.Time `json:"published_at"`
}

type SemanticSource struct {
	DocumentID  string `json:"document_id"`
	Revision    int    `json:"revision"`
	ContentHash string `json:"content_hash"`
	ContentType string `json:"content_type"`
	Content     string `json:"content"`
	Editable    bool   `json:"editable"`
}

type KnowledgeChange struct {
	Sequence    int64     `json:"sequence"`
	FolderID    string    `json:"folder_id"`
	DocumentID  string    `json:"document_id"`
	Revision    int       `json:"revision"`
	EventType   string    `json:"event_type"`
	ContentHash string    `json:"content_hash"`
	OccurredAt  time.Time `json:"occurred_at"`
}

// FolderDocumentInventoryProvider returns the complete folder inventory,
// including documents still indexing. A filtered search/indexed-only list is
// insufficient proof that a previously projected document was deleted.
type FolderDocumentInventoryProvider interface {
	ListFolderDocumentInventory(context.Context, FolderDocumentsRequest) ([]FolderDocument, error)
}

type KnowledgeChangePage struct {
	Items      []KnowledgeChange `json:"items"`
	NextCursor string            `json:"next_cursor"`
}

type EnsureKnowledgeBaseRequest struct {
	Base       core.KnowledgeBase
	VectorRoot string
}

type EnsureKnowledgeBaseResult struct {
	ProviderResourceID string
	VectorStorePath    string
	Metadata           map[string]string
}

type DeleteKnowledgeBaseRequest struct {
	Base core.KnowledgeBase
}

type UploadDocumentRequest struct {
	Base        core.KnowledgeBase
	DocumentID  core.KnowledgeDocumentID
	Filename    string
	ContentType string
	Reader      io.Reader
	CreatedBy   string
}

type CreateDocumentMetadataRequest struct {
	Base     core.KnowledgeBase
	Document core.KnowledgeDocument
}

type UploadDocumentResult struct {
	ArtifactID         string
	ProviderDocumentID string
	Filename           string
	ContentType        string
	SizeBytes          int64
	Checksum           string
	Status             core.DocumentStatus
	ErrorMessage       string
	ProviderMetadata   map[string]string
	IngestJobID        core.KnowledgeJobID
	IngestJobQueued    bool
}

type DeleteDocumentRequest struct {
	Base     core.KnowledgeBase
	Document core.KnowledgeDocument
}

type DocumentStatusRequest struct {
	Base     core.KnowledgeBase
	Document core.KnowledgeDocument
}

type DocumentStatusResult struct {
	ProviderDocumentID string
	Filename           string
	ContentType        string
	SizeBytes          int64
	Checksum           string
	Status             core.DocumentStatus
	ErrorMessage       string
	ProviderMetadata   map[string]string
	Job                *DocumentProcessingJob
}

type ReprocessDocumentRequest struct {
	Base     core.KnowledgeBase
	Document core.KnowledgeDocument
}

type ReprocessDocumentResult struct {
	Status       core.DocumentStatus
	ErrorMessage string
	Job          DocumentProcessingJob
}

type DocumentProcessingResultRequest struct {
	Base     core.KnowledgeBase
	Document core.KnowledgeDocument
	Pages    string
}

type DocumentProcessingResult struct {
	Filename       string
	Status         core.DocumentStatus
	Progress       int
	Stage          string
	ErrorMessage   string
	Job            *DocumentProcessingJob
	Structure      []map[string]any
	Pages          []map[string]any
	StructureNodes []DocumentStructureNode
	PagePreviews   []DocumentPagePreview
}

type DocumentProcessingJob struct {
	JobID        string
	DocumentID   string
	Topic        string
	Status       string
	Progress     int
	Stage        string
	ErrorMessage string
}

type DocumentStructureNode struct {
	ProviderNodeID string
	Text           string
	PageNumber     int
	HeadingPath    []string
	StructurePath  []string
	BBox           map[string]any
	Children       []DocumentStructureNode
}

type DocumentPagePreview struct {
	PageNumber     int
	ProviderNodeID string
	HeadingPath    []string
	StructurePath  []string
	BBox           map[string]any
	ImageRef       string
	Text           string
	ContentHash    string
	SnippetHash    string
	Raw            map[string]any
}

type ReindexKnowledgeBaseRequest struct {
	Base core.KnowledgeBase
}

type ReindexKnowledgeBaseResult struct {
	JobID          core.KnowledgeJobID
	Queued         bool
	LocalIngestJob bool
	Supported      bool
}

type SearchRequest struct {
	Query         string
	Bases         []core.KnowledgeBase
	Limit         int
	RetrievalMode RetrievalMode
}

type RetrievalMode string

const (
	RetrievalModeAuto    RetrievalMode = "auto"
	RetrievalModeFast    RetrievalMode = "fast"
	RetrievalModeAgentic RetrievalMode = "agentic"
)

func NormalizeRetrievalMode(value string) (RetrievalMode, error) {
	switch RetrievalMode(strings.ToLower(strings.TrimSpace(value))) {
	case "", RetrievalModeAuto:
		return RetrievalModeAuto, nil
	case RetrievalModeFast:
		return RetrievalModeFast, nil
	case RetrievalModeAgentic:
		return RetrievalModeAgentic, nil
	default:
		return "", fmt.Errorf("%w: unsupported retrieval mode", core.ErrInvalid)
	}
}

type SearchResult struct {
	KnowledgeBaseID string
	DocumentID      string
	ChunkID         string
	Score           float32
	Snippet         string
	HeadingPath     []string
	DocumentName    string
}

type SearchResultPage struct {
	Results    []SearchResult
	Executions []SearchExecution
}

type SearchExecution struct {
	Provider        core.KnowledgeProvider
	KnowledgeBaseID string
	RequestedMode   RetrievalMode
	ActualMode      string
	FallbackReason  string
}

type StaticRegistry struct {
	defaultProvider core.KnowledgeProvider
	providers       map[core.KnowledgeProvider]Provider
}

func NewStaticRegistry(defaultProvider core.KnowledgeProvider, providers ...Provider) *StaticRegistry {
	out := &StaticRegistry{defaultProvider: defaultProvider, providers: map[core.KnowledgeProvider]Provider{}}
	for _, provider := range providers {
		if provider != nil {
			out.providers[provider.Name()] = provider
		}
	}
	return out
}

func (r *StaticRegistry) Default() Provider {
	if r == nil {
		return nil
	}
	return r.Provider(r.defaultProvider)
}

func (r *StaticRegistry) ForBase(base core.KnowledgeBase) Provider {
	if r == nil {
		return nil
	}
	return r.Provider(base.EffectiveProvider())
}

func (r *StaticRegistry) Provider(name core.KnowledgeProvider) Provider {
	if r == nil {
		return nil
	}
	return r.providers[name]
}
