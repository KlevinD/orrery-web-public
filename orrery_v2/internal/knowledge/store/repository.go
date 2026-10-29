package store

import (
	"context"
	"time"

	"orrery_v2/internal/knowledge/core"
)

type Repository interface {
	SaveKnowledgeBase(ctx context.Context, base core.KnowledgeBase) error
	GetKnowledgeBase(ctx context.Context, id core.KnowledgeBaseID) (core.KnowledgeBase, error)
	ListKnowledgeBases(ctx context.Context, query core.KnowledgeBaseQuery) (core.KnowledgeBasePage, error)
	CountKnowledgeBasesByStatus(ctx context.Context) (map[core.KnowledgeStatus]int64, error)
	MarkKnowledgeBaseDeleting(ctx context.Context, id core.KnowledgeBaseID, at time.Time) error
	DeleteKnowledgeBaseMetadata(ctx context.Context, id core.KnowledgeBaseID) error

	SaveBinding(ctx context.Context, binding core.KnowledgeBinding) error
	GetBinding(ctx context.Context, id core.KnowledgeBindingID) (core.KnowledgeBinding, error)
	DeleteBinding(ctx context.Context, id core.KnowledgeBindingID) error
	ListBindings(ctx context.Context, query core.BindingQuery) ([]core.KnowledgeBinding, error)

	SaveDocument(ctx context.Context, doc core.KnowledgeDocument) error
	UpdateDocumentStatus(ctx context.Context, id core.KnowledgeDocumentID, status core.DocumentStatus, message string, at time.Time) error
	GetDocument(ctx context.Context, id core.KnowledgeDocumentID) (core.KnowledgeDocument, error)
	ListDocuments(ctx context.Context, query core.DocumentQuery) (core.DocumentPage, error)
	CountDocumentsByStatus(ctx context.Context) (map[core.DocumentStatus]int64, error)
	DeleteDocumentMetadata(ctx context.Context, id core.KnowledgeDocumentID) error

	ReplaceChunks(ctx context.Context, documentID core.KnowledgeDocumentID, chunks []core.KnowledgeChunk) error
	GetChunk(ctx context.Context, id core.KnowledgeChunkID) (core.KnowledgeChunk, error)
	ListChunks(ctx context.Context, query core.ChunkQuery) ([]core.KnowledgeChunk, error)

	SaveIngestJob(ctx context.Context, job core.KnowledgeIngestJob) error
	GetIngestJob(ctx context.Context, id core.KnowledgeJobID) (core.KnowledgeIngestJob, error)
	ListIngestJobs(ctx context.Context, query core.JobQuery) ([]core.KnowledgeIngestJob, error)
	CountIngestJobsByStatus(ctx context.Context) (map[core.JobStatus]int64, error)
	UpdateIngestJobStatus(ctx context.Context, id core.KnowledgeJobID, status core.JobStatus, message string, at time.Time) error

	NextEventRevision(ctx context.Context, streamID string) (int64, error)
}
