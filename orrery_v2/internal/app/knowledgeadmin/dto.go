package knowledgeadmin

import (
	"io"

	"orrery_v2/internal/knowledge/provider"
	doccore "orrery_v2/internal/platform/document/core"
	"orrery_v2/internal/retrievalevidence"
)

type KnowledgeBaseDTO struct {
	ID                  string              `json:"id"`
	Name                string              `json:"name"`
	Description         string              `json:"description"`
	OwnerType           string              `json:"owner_type"`
	OwnerID             string              `json:"owner_id"`
	IsDefault           bool                `json:"is_default"`
	Visibility          string              `json:"visibility"`
	Provider            string              `json:"provider"`
	ProviderResourceID  string              `json:"provider_resource_id,omitempty"`
	ProviderBindingID   string              `json:"provider_binding_id,omitempty"`
	ProviderDisplayName string              `json:"provider_display_name,omitempty"`
	VectorBackend       string              `json:"vector_backend"`
	VectorStorePath     string              `json:"-"`
	EmbeddingProfileRef string              `json:"embedding_profile_ref"`
	EmbeddingModel      string              `json:"embedding_model"`
	EmbeddingDimensions int                 `json:"embedding_dimensions"`
	ChunkPolicy         doccore.ChunkPolicy `json:"chunk_policy"`
	Status              string              `json:"status"`
}

type KnowledgeBasePageDTO struct {
	Items []KnowledgeBaseDTO `json:"items"`
	Total int                `json:"total"`
}

type KnowledgeBindingDTO struct {
	ID              string `json:"id"`
	KnowledgeBaseID string `json:"knowledge_base_id"`
	BoundToType     string `json:"bound_to_type"`
	BoundToID       string `json:"bound_to_id"`
	Access          string `json:"access"`
}

type KnowledgeBindingPageDTO struct {
	Items []KnowledgeBindingDTO `json:"items"`
	Total int                   `json:"total"`
}

type KnowledgeDocumentDTO struct {
	ID                  string `json:"id"`
	KnowledgeBaseID     string `json:"knowledge_base_id"`
	ArtifactID          string `json:"artifact_id"`
	ProviderDocumentID  string `json:"provider_document_id,omitempty"`
	Filename            string `json:"filename"`
	FolderPath          string `json:"folder_path,omitempty"`
	ContentType         string `json:"content_type"`
	SizeBytes           int64  `json:"size_bytes"`
	Checksum            string `json:"checksum"`
	Status              string `json:"status"`
	ErrorMessage        string `json:"error_message"`
	JobID               string `json:"job_id,omitempty"`
	ProcessingProgress  int    `json:"processing_progress,omitempty"`
	ProcessingStage     string `json:"processing_stage,omitempty"`
	ProcessingJobStatus string `json:"processing_job_status,omitempty"`
}

type KnowledgeDocumentPageDTO struct {
	Items []KnowledgeDocumentDTO `json:"items"`
	Total int                    `json:"total"`
}

type KnowledgeDocumentRelationDTO struct {
	Direction        string               `json:"direction"`
	LinkType         string               `json:"link_type"`
	SourceDocumentID string               `json:"source_document_id,omitempty"`
	Revision         int                  `json:"revision"`
	Anchor           string               `json:"anchor,omitempty"`
	Document         KnowledgeDocumentDTO `json:"document"`
}

type KnowledgeDocumentRelationPageDTO struct {
	Items []KnowledgeDocumentRelationDTO `json:"items"`
}

type KnowledgeDocumentProcessingResultDTO struct {
	Document       KnowledgeDocumentDTO       `json:"document"`
	Status         string                     `json:"status"`
	Progress       int                        `json:"progress"`
	Stage          string                     `json:"stage"`
	ErrorMessage   string                     `json:"error_message,omitempty"`
	Job            *DocumentProcessingJobDTO  `json:"job,omitempty"`
	Structure      []map[string]any           `json:"structure,omitempty"`
	Pages          []map[string]any           `json:"pages,omitempty"`
	StructureNodes []DocumentStructureNodeDTO `json:"structure_nodes,omitempty"`
	PagePreviews   []DocumentPagePreviewDTO   `json:"page_previews,omitempty"`
}

type DocumentProcessingJobDTO struct {
	ID           string `json:"id"`
	DocumentID   string `json:"document_id,omitempty"`
	Topic        string `json:"topic,omitempty"`
	Status       string `json:"status"`
	Progress     int    `json:"progress"`
	Stage        string `json:"stage,omitempty"`
	ErrorMessage string `json:"error_message,omitempty"`
}

type DocumentStructureNodeDTO struct {
	ProviderNodeID string                     `json:"provider_node_id,omitempty"`
	Text           string                     `json:"text,omitempty"`
	PageNumber     int                        `json:"page_number,omitempty"`
	HeadingPath    []string                   `json:"heading_path,omitempty"`
	StructurePath  []string                   `json:"structure_path,omitempty"`
	BBox           map[string]any             `json:"bbox,omitempty"`
	Children       []DocumentStructureNodeDTO `json:"children,omitempty"`
}

type DocumentPagePreviewDTO struct {
	PageNumber     int            `json:"page_number"`
	ProviderNodeID string         `json:"provider_node_id,omitempty"`
	HeadingPath    []string       `json:"heading_path,omitempty"`
	StructurePath  []string       `json:"structure_path,omitempty"`
	BBox           map[string]any `json:"bbox,omitempty"`
	ImageRef       string         `json:"image_ref,omitempty"`
	Text           string         `json:"text,omitempty"`
	ContentHash    string         `json:"content_hash,omitempty"`
	SnippetHash    string         `json:"snippet_hash,omitempty"`
	Raw            map[string]any `json:"raw,omitempty"`
}

type KnowledgeJobDTO struct {
	ID              string `json:"id"`
	KnowledgeBaseID string `json:"knowledge_base_id"`
	DocumentID      string `json:"document_id,omitempty"`
	Topic           string `json:"topic"`
	Status          string `json:"status"`
	ErrorMessage    string `json:"error_message,omitempty"`
}

type KnowledgeJobPageDTO struct {
	Items []KnowledgeJobDTO `json:"items"`
	Total int               `json:"total"`
}

type SearchKnowledgeCommand struct {
	Query            string                 `json:"query"`
	KnowledgeBaseIDs []string               `json:"knowledge_base_ids"`
	OwnerFilter      OwnerFilter            `json:"owner_filter"`
	Limit            int                    `json:"limit"`
	RetrievalMode    provider.RetrievalMode `json:"retrieval_mode"`
}

type OwnerFilter struct {
	OwnerType string `json:"owner_type"`
	OwnerID   string `json:"owner_id"`
}

type KnowledgeSearchResultDTO struct {
	Evidence        *retrievalevidence.Item `json:"evidence,omitempty"`
	KnowledgeBaseID string                  `json:"knowledge_base_id"`
	DocumentID      string                  `json:"document_id"`
	ChunkID         string                  `json:"chunk_id"`
	Score           float32                 `json:"score"`
	Snippet         string                  `json:"snippet"`
	HeadingPath     []string                `json:"heading_path"`
	DocumentName    string                  `json:"document_name"`
}

type KnowledgeSearchPageDTO struct {
	Results    []KnowledgeSearchResultDTO    `json:"results"`
	Executions []KnowledgeSearchExecutionDTO `json:"executions,omitempty"`
}

type KnowledgeSearchExecutionDTO struct {
	Provider        string `json:"provider"`
	KnowledgeBaseID string `json:"knowledge_base_id"`
	RequestedMode   string `json:"requested_mode"`
	ActualMode      string `json:"actual_mode"`
	FallbackReason  string `json:"fallback_reason,omitempty"`
}

type CreateKnowledgeBaseCommand struct {
	Name                string              `json:"name"`
	Description         string              `json:"description"`
	OwnerType           string              `json:"owner_type"`
	OwnerID             string              `json:"owner_id"`
	Visibility          string              `json:"visibility"`
	Provider            string              `json:"provider"`
	VectorRoot          string              `json:"vector_root"`
	EmbeddingProfileRef string              `json:"embedding_profile_ref"`
	EmbeddingModel      string              `json:"embedding_model"`
	EmbeddingDimensions int                 `json:"embedding_dimensions"`
	ChunkPolicy         doccore.ChunkPolicy `json:"chunk_policy"`
}

type UpdateKnowledgeBaseCommand struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Visibility  string   `json:"visibility"`
	Status      string   `json:"status"`
	UpdateMask  []string `json:"update_mask,omitempty"`
}

type BindKnowledgeBaseCommand struct {
	KnowledgeBaseID string `json:"knowledge_base_id"`
	BoundToType     string `json:"bound_to_type"`
	BoundToID       string `json:"bound_to_id"`
	Access          string `json:"access"`
}

type EnsureDefaultCommand struct {
	OwnerType  string
	OwnerID    string
	VectorRoot string
}

type CreateDocumentCommand struct {
	KnowledgeBaseID string `json:"knowledge_base_id"`
	ArtifactID      string `json:"artifact_id"`
	Filename        string `json:"filename"`
	ContentType     string `json:"content_type"`
	SizeBytes       int64  `json:"size_bytes"`
	Checksum        string `json:"checksum"`
}

type DeleteDocumentCommand struct {
	KnowledgeBaseID string `json:"knowledge_base_id"`
	DocumentID      string `json:"document_id"`
}

type DocumentActionCommand struct {
	KnowledgeBaseID string `json:"knowledge_base_id"`
	DocumentID      string `json:"document_id"`
}

type DocumentProcessingResultCommand struct {
	KnowledgeBaseID string `json:"knowledge_base_id"`
	DocumentID      string `json:"document_id"`
	Pages           string `json:"pages"`
}

type ReindexKnowledgeBaseCommand struct {
	KnowledgeBaseID string `json:"knowledge_base_id"`
}

type UploadDocumentCommand struct {
	KnowledgeBaseID string
	Filename        string
	ContentType     string
	Reader          io.Reader
}

type IngestPolicy struct {
	MaxDocumentBytes int64
}

type ListKnowledgeBasesQuery struct {
	OwnerType string
	OwnerID   string
}
