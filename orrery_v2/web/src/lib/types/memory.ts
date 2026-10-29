export interface MemoryScopeDTO {
  kind: string
  id: string
}

export interface MemoryVisibilityDTO {
  mode: string
  scope_kind?: string
  scope_id?: string
}

export interface CitationEvidenceDTO {
  kind: string
  id: string
  title?: string
  knowledge_base_id?: string
  document_id?: string
  page_id?: string
  page_number?: number
  provider?: string
  provider_document_id?: string
  provider_node_id?: string
  heading_path?: string[]
  structure_path?: string[]
  bbox?: Record<string, unknown>
  image_ref?: string
  source_uri?: string
  content_hash?: string
  snippet_hash?: string
}

export interface MemoryRecordDTO {
  memory_id: string
  profile_id: string
  candidate_id: string
  layer: string
  scope: MemoryScopeDTO
  topic?: string
  summary: string
  content?: string
  narrative?: string
  facts?: string[]
  lessons?: string[]
  preferences?: string[]
  warnings?: string[]
  decisions?: string[]
  tags?: string[]
  source_memory_refs?: string[]
  source_evidence_refs?: string[]
  citation_evidence?: CitationEvidenceDTO[]
  lineage_refs?: string[]
  confidence: number
  visibility: MemoryVisibilityDTO
  sensitivity: string
  de_id_level: string
  lifecycle_status: string
  reference_count?: number
  created_by: string
  created_at: string
  updated_at: string
}

export interface MemoryRecordPageDTO {
  records: MemoryRecordDTO[]
  total: number
  limit: number
}

export interface MemoryCandidateDTO {
  candidate_id: string
  mission_id?: string
  discussion_group_id?: string
  source_type: string
  source_ref: string
  proposed_layer: string
  proposed_kind: string
  proposed_scope: MemoryScopeDTO
  topic?: string
  summary: string
  narrative?: string
  tags?: string[]
  evidence_refs?: string[]
  lineage_refs?: string[]
  sensitivity: string
  de_id_level: string
  review_required: boolean
  review_reason?: string
  status: string
  created_by: string
  created_at?: string
  updated_at?: string
}

export interface MemoryCandidatePageDTO {
  candidates: MemoryCandidateDTO[]
  total: number
  limit: number
}

export interface AcceptMemoryCandidateRequest {
  profile_id?: string
  memory_id?: string
  reason?: string
  confidence?: number
}

export interface RejectMemoryCandidateRequest {
  reason: string
}

export interface MemoryCommandReceiptDTO {
  status: string
  candidate_id?: string
  memory_id?: string
  target_id?: string
  updated_at?: string
}
