package memoryadmin

import "time"

type MemoryScopeDTO struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
}

type MemoryVisibilityDTO struct {
	Mode      string `json:"mode"`
	ScopeKind string `json:"scope_kind,omitempty"`
	ScopeID   string `json:"scope_id,omitempty"`
}

type MemoryCandidateDTO struct {
	CandidateID       string            `json:"candidate_id"`
	MissionID         string            `json:"mission_id,omitempty"`
	DiscussionGroupID string            `json:"discussion_group_id,omitempty"`
	SourceType        string            `json:"source_type"`
	SourceRef         string            `json:"source_ref"`
	ProposedLayer     string            `json:"proposed_layer"`
	ProposedKind      string            `json:"proposed_kind"`
	ProposedScope     MemoryScopeDTO    `json:"proposed_scope"`
	Topic             string            `json:"topic,omitempty"`
	Summary           string            `json:"summary"`
	Narrative         string            `json:"narrative,omitempty"`
	Lessons           []string          `json:"lessons,omitempty"`
	Warnings          []string          `json:"warnings,omitempty"`
	Entities          map[string]string `json:"entities,omitempty"`
	Tags              []string          `json:"tags,omitempty"`
	EvidenceRefs      []string          `json:"evidence_refs,omitempty"`
	LineageRefs       []string          `json:"lineage_refs,omitempty"`
	SupersedesRefs    []string          `json:"supersedes_refs,omitempty"`
	ContradictsRefs   []string          `json:"contradicts_refs,omitempty"`
	Sensitivity       string            `json:"sensitivity"`
	DeIDLevel         string            `json:"de_id_level"`
	Consent           string            `json:"consent"`
	Confidence        float64           `json:"confidence,omitempty"`
	ReviewRequired    bool              `json:"review_required"`
	ReviewReason      string            `json:"review_reason,omitempty"`
	Status            string            `json:"status"`
	CreatedBy         string            `json:"created_by"`
	CreatedAt         time.Time         `json:"created_at,omitempty"`
	UpdatedAt         time.Time         `json:"updated_at,omitempty"`
}

type MemoryCandidatePageDTO struct {
	Candidates []MemoryCandidateDTO `json:"candidates"`
	Total      int                  `json:"total"`
	Limit      int                  `json:"limit"`
}

type MemoryRecordDTO struct {
	MemoryID           string                `json:"memory_id"`
	ProfileID          string                `json:"profile_id"`
	CandidateID        string                `json:"candidate_id"`
	Layer              string                `json:"layer"`
	Scope              MemoryScopeDTO        `json:"scope"`
	Topic              string                `json:"topic,omitempty"`
	Summary            string                `json:"summary"`
	Content            string                `json:"content,omitempty"`
	Narrative          string                `json:"narrative,omitempty"`
	Lessons            []string              `json:"lessons,omitempty"`
	Facts              []string              `json:"facts,omitempty"`
	Preferences        []string              `json:"preferences,omitempty"`
	Warnings           []string              `json:"warnings,omitempty"`
	Decisions          []string              `json:"decisions,omitempty"`
	Tags               []string              `json:"tags,omitempty"`
	SourceMemoryRefs   []string              `json:"source_memory_refs,omitempty"`
	SourceEvidenceRefs []string              `json:"source_evidence_refs,omitempty"`
	CitationEvidence   []CitationEvidenceDTO `json:"citation_evidence,omitempty"`
	LineageRefs        []string              `json:"lineage_refs,omitempty"`
	SupersedesRefs     []string              `json:"supersedes_refs,omitempty"`
	ContradictsRefs    []string              `json:"contradicts_refs,omitempty"`
	Confidence         float64               `json:"confidence"`
	Visibility         MemoryVisibilityDTO   `json:"visibility"`
	Sensitivity        string                `json:"sensitivity"`
	DeIDLevel          string                `json:"de_id_level"`
	LifecycleStatus    string                `json:"lifecycle_status"`
	ReferenceCount     int                   `json:"reference_count,omitempty"`
	LastReferencedAt   time.Time             `json:"last_referenced_at,omitempty"`
	CreatedBy          string                `json:"created_by"`
	CreatedAt          time.Time             `json:"created_at"`
	UpdatedAt          time.Time             `json:"updated_at"`
}

type CitationEvidenceDTO struct {
	Kind               string         `json:"kind"`
	ID                 string         `json:"id"`
	Title              string         `json:"title,omitempty"`
	KnowledgeBaseID    string         `json:"knowledge_base_id,omitempty"`
	DocumentID         string         `json:"document_id,omitempty"`
	PageID             string         `json:"page_id,omitempty"`
	PageNumber         int            `json:"page_number,omitempty"`
	Provider           string         `json:"provider,omitempty"`
	ProviderDocumentID string         `json:"provider_document_id,omitempty"`
	ProviderNodeID     string         `json:"provider_node_id,omitempty"`
	HeadingPath        []string       `json:"heading_path,omitempty"`
	StructurePath      []string       `json:"structure_path,omitempty"`
	BBox               map[string]any `json:"bbox,omitempty"`
	ImageRef           string         `json:"image_ref,omitempty"`
	SourceURI          string         `json:"source_uri,omitempty"`
	ContentHash        string         `json:"content_hash,omitempty"`
	SnippetHash        string         `json:"snippet_hash,omitempty"`
}

type MemoryRecordPageDTO struct {
	Records []MemoryRecordDTO `json:"records"`
	Total   int               `json:"total"`
	Limit   int               `json:"limit"`
}

type ListCandidatesQuery struct {
	MissionID      string
	ScopeKind      string
	ScopeID        string
	Layer          string
	Topic          string
	Status         string
	ReviewRequired *bool
	Limit          int
}

type ListRecordsQuery struct {
	ProfileID       string
	ScopeKind       string
	ScopeID         string
	Layer           string
	Topic           string
	LifecycleStatus string
	IncludeArchived bool
	Limit           int
}

type CreateCandidateRequest struct {
	CandidateID       string            `json:"candidate_id"`
	MissionID         string            `json:"mission_id,omitempty"`
	DiscussionGroupID string            `json:"discussion_group_id,omitempty"`
	SourceType        string            `json:"source_type"`
	SourceRef         string            `json:"source_ref"`
	ProposedKind      string            `json:"proposed_kind"`
	ProposedLayer     string            `json:"proposed_layer"`
	ProposedScope     MemoryScopeDTO    `json:"proposed_scope"`
	Topic             string            `json:"topic,omitempty"`
	Summary           string            `json:"summary"`
	Narrative         string            `json:"narrative,omitempty"`
	Lessons           []string          `json:"lessons,omitempty"`
	Warnings          []string          `json:"warnings,omitempty"`
	Entities          map[string]string `json:"entities,omitempty"`
	Tags              []string          `json:"tags,omitempty"`
	EvidenceRefs      []string          `json:"evidence_refs,omitempty"`
	LineageRefs       []string          `json:"lineage_refs,omitempty"`
	SupersedesRefs    []string          `json:"supersedes_refs,omitempty"`
	ContradictsRefs   []string          `json:"contradicts_refs,omitempty"`
	Sensitivity       string            `json:"sensitivity,omitempty"`
	DeIDLevel         string            `json:"de_id_level,omitempty"`
	Consent           string            `json:"consent,omitempty"`
	Confidence        float64           `json:"confidence,omitempty"`
	ReviewRequired    bool              `json:"review_required,omitempty"`
	ReviewReason      string            `json:"review_reason,omitempty"`
}

type AcceptCandidateRequest struct {
	ProfileID  string  `json:"profile_id"`
	MemoryID   string  `json:"memory_id,omitempty"`
	Reason     string  `json:"reason,omitempty"`
	Confidence float64 `json:"confidence,omitempty"`
}

type RejectCandidateRequest struct {
	Reason string `json:"reason"`
}

type MergeCandidateRequest struct {
	TargetMemoryID string `json:"target_memory_id"`
	Reason         string `json:"reason"`
}

type MemoryLifecycleRequest struct {
	Reason string `json:"reason"`
}

type TouchMemoryRecordsRequest struct {
	MemoryIDs []string `json:"memory_ids"`
	Reason    string   `json:"reason,omitempty"`
}

type MemoryRetentionSweepRequest struct {
	Limit  int    `json:"limit,omitempty"`
	Reason string `json:"reason,omitempty"`
}

type MemoryRetentionSweepResultDTO struct {
	Status            string    `json:"status"`
	ArchivedMemoryIDs []string  `json:"archived_memory_ids,omitempty"`
	CheckedCount      int       `json:"checked_count"`
	ArchivedCount     int       `json:"archived_count"`
	UpdatedAt         time.Time `json:"updated_at,omitempty"`
}

type CommandReceiptDTO struct {
	Status      string    `json:"status"`
	CandidateID string    `json:"candidate_id,omitempty"`
	MemoryID    string    `json:"memory_id,omitempty"`
	TargetID    string    `json:"target_id,omitempty"`
	UpdatedAt   time.Time `json:"updated_at,omitempty"`
}
