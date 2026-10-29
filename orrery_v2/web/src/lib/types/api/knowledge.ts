export type KnowledgeOwnerType = 'agent' | 'human' | 'organization' | 'position' | 'global' | string;
export type KnowledgeVisibility = 'private' | 'scoped' | 'public' | string;
export type KnowledgeStatus = 'active' | 'disabled' | 'deleting' | string;
export type KnowledgeAccess = 'read' | 'write' | 'manage' | string;
export type KnowledgeProvider = 'local' | 'pageindex' | 'mcp' | string;

export interface ChunkPolicyDTO {
  max_runes?: number;
  overlap_runes?: number;
  min_runes?: number;
  preserve_heads?: boolean;
}

export interface KnowledgeBaseDTO {
  id: string;
  name: string;
  description?: string;
  owner_type: KnowledgeOwnerType;
  owner_id: string;
  is_default?: boolean;
  visibility?: KnowledgeVisibility;
  provider?: KnowledgeProvider;
  provider_resource_id?: string;
  provider_binding_id?: string;
  provider_display_name?: string;
  vector_backend?: string;
  embedding_profile_ref?: string;
  embedding_model?: string;
  embedding_dimensions?: number;
  chunk_policy?: ChunkPolicyDTO;
  status?: KnowledgeStatus;
}

export interface KnowledgeBasePageDTO {
  items: KnowledgeBaseDTO[];
  total: number;
}

export interface KnowledgeBindingDTO {
  id: string;
  knowledge_base_id: string;
  bound_to_type: string;
  bound_to_id: string;
  access: KnowledgeAccess;
}

export interface KnowledgeBindingPageDTO {
  items: KnowledgeBindingDTO[];
  total: number;
}

export interface KnowledgeDocumentDTO {
  id: string;
  knowledge_base_id: string;
  artifact_id?: string;
  filename?: string;
  folder_path?: string;
  content_type?: string;
  size_bytes?: number;
  checksum?: string;
  status?: string;
  provider_document_id?: string;
  error_message?: string;
  job_id?: string;
  processing_progress?: number;
  processing_stage?: string;
  processing_job_status?: string;
}

export interface KnowledgeDocumentPageDTO {
  items: KnowledgeDocumentDTO[];
  total: number;
}

export interface CatalogReconcileResultDTO {
  created: number;
  updated: number;
  unchanged: number;
  removed: number;
  rejected: number;
}

export interface KnowledgeDocumentRelationDTO {
  direction: 'in' | 'out' | string;
  link_type: string;
  source_document_id?: string;
  revision: number;
  anchor?: string;
  document: KnowledgeDocumentDTO;
}

export interface KnowledgeDocumentRelationPageDTO {
  items: KnowledgeDocumentRelationDTO[];
}

export interface DocumentProcessingJobDTO {
  id: string;
  document_id?: string;
  topic?: string;
  status?: string;
  progress?: number;
  stage?: string;
  error_message?: string;
}

export interface KnowledgeDocumentProcessingResultDTO {
  document: KnowledgeDocumentDTO;
  status?: string;
  progress?: number;
  stage?: string;
  error_message?: string;
  job?: DocumentProcessingJobDTO;
  structure?: Record<string, unknown>[];
  pages?: Record<string, unknown>[];
  structure_nodes?: DocumentStructureNodeDTO[];
  page_previews?: DocumentPagePreviewDTO[];
}

export interface DocumentStructureNodeDTO {
  provider_node_id?: string;
  text?: string;
  page_number?: number;
  heading_path?: string[];
  structure_path?: string[];
  bbox?: Record<string, unknown>;
  children?: DocumentStructureNodeDTO[];
}

export interface DocumentPagePreviewDTO {
  page_number: number;
  provider_node_id?: string;
  heading_path?: string[];
  structure_path?: string[];
  bbox?: Record<string, unknown>;
  image_ref?: string;
  text?: string;
  content_hash?: string;
  snippet_hash?: string;
  raw?: Record<string, unknown>;
}

export interface KnowledgeJobDTO {
  id: string;
  knowledge_base_id: string;
  document_id?: string;
  topic?: string;
  status?: string;
  error_message?: string;
}

export interface KnowledgeJobPageDTO {
  items: KnowledgeJobDTO[];
  total: number;
}

export interface KnowledgeSearchResultDTO {
  knowledge_base_id: string;
  document_id: string;
  chunk_id: string;
  score: number;
  snippet?: string;
  heading_path?: string[];
  document_name?: string;
}

export interface KnowledgeSearchPageDTO {
  results: KnowledgeSearchResultDTO[];
}

export interface KnowledgeSemanticSourceDTO {
  document_id: string;
  revision: number;
  content_hash: string;
  content_type: string;
  content: string;
  editable: boolean;
}

export interface KnowledgeRevisionDTO {
  id?: string;
  document_id: string;
  folder_id?: string;
  revision: number;
  content_hash: string;
  content_type?: string;
  size_bytes?: number;
  created_by?: string;
  created_at?: string;
  publication_status: 'draft' | 'published' | string;
  published_by?: string;
  published_at?: string;
}

export interface KnowledgeRevisionPageDTO { items: KnowledgeRevisionDTO[]; }

export interface KnowledgeOntologyProjectionDTO {
  tenant_id: string;
  knowledge_base_id: string;
  document_id: string;
  revision: number;
  content_hash: string;
  subjects?: Array<{ reference: { kind: string; id: string }; object_id?: string; resolution: string }>;
  assertions?: Array<{
    link_type: string;
    from: { kind: string; id: string };
    to: { kind: string; id: string };
    from_object_id?: string;
    to_object_id?: string;
    status: string;
    evidence?: { anchor?: string; extraction_method?: string };
  }>;
}

export interface CreateKnowledgeBaseCommand {
  name: string;
  description?: string;
  owner_type: KnowledgeOwnerType;
  owner_id: string;
  visibility?: KnowledgeVisibility;
  provider?: KnowledgeProvider;
  embedding_profile_ref?: string;
  embedding_model?: string;
  embedding_dimensions?: number;
  chunk_policy?: ChunkPolicyDTO;
}

export interface UpdateKnowledgeBaseCommand {
  name?: string;
  description?: string;
  visibility?: KnowledgeVisibility;
  status?: KnowledgeStatus;
}

export interface BindKnowledgeBaseCommand {
  bound_to_type: string;
  bound_to_id: string;
  access: KnowledgeAccess;
}

export interface CreateKnowledgeDocumentCommand {
  artifact_id: string;
  filename: string;
  content_type?: string;
  size_bytes?: number;
  checksum?: string;
}

export interface SearchKnowledgeCommand {
  query: string;
  knowledge_base_ids?: string[];
  limit?: number;
}
