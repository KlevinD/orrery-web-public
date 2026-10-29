import type { PageDTO, ResourceBindingDTO } from './api'
import type { BadgeTone, ResourceSummaryViewModel } from './view-models'

export interface MemoryResourceAdminViewDTO {
  memory_profiles?: MemoryProfileDTO[]
  bindings?: ResourceBindingDTO[]
  usage?: Array<{ resource_id: string; usage_count?: number }>
  page?: PageDTO
}

export interface MemoryProfileDTO {
  memory_profile_id: string
  owner_agent_id?: string
  organization_id?: string
  department_id?: string
  project_id?: string
  memory_kind: string
  default_scope: MemoryScopeDTO
  retrieval_policy: MemoryRetrievalPolicyDTO
  compression_policy: MemoryCompressionPolicyDTO
  visibility_policy: MemoryVisibilityPolicyDTO
  enabled: boolean
  record_count: number
  candidate_count: number
}

export interface UpdateMemoryProfileRequest {
  enabled: boolean
  max_records: number
  max_summary_bytes: number
  retrieval_tags: string[]
}

export interface MemoryScopeDTO {
  kind: string
  id: string
}

export interface MemoryRetrievalPolicyDTO {
  max_records?: number
  tags?: string[]
}

export interface MemoryCompressionPolicyDTO {
  max_summary_bytes?: number
}

export interface MemoryVisibilityPolicyDTO {
  mode: string
  scope_kind?: string
  scope_id?: string
}

export interface MemoryProfileRefRequestDTO {
  memory_profile_id: string
  owner_agent_id?: string
  memory_kind?: string
  access_scope?: string
  retrieval_policy?: string
  compression_policy?: string
}

export interface BuildMemoryDigestRequest {
  agent_id: string
  mission_id?: string
  task_id?: string
  organization_ids?: string[]
  department_ids?: string[]
  project_ids?: string[]
  discussion_ids?: string[]
  profile_refs: MemoryProfileRefRequestDTO[]
}

export interface MemoryDigestDTO {
  digest_id: string
  agent_id: string
  mission_id?: string
  task_id?: string
  scope: MemoryScopeDTO
  source_memory_refs: string[]
  source_evidence_refs: string[]
  summary: string
  facts?: string[]
  preferences?: string[]
  warnings?: string[]
  decisions?: string[]
  confidence: number
  visibility: MemoryVisibilityPolicyDTO
  created_at: string
  expires_at?: string
}

export interface MemoryProfileViewModel {
  id: string
  ownerAgentId: string
  organizationId: string
  departmentId: string
  projectId: string
  memoryKind: string
  enabled: boolean
  statusLabel: string
  statusTone: BadgeTone
  defaultScopeKind: string
  defaultScopeId: string
  defaultScopeLabel: string
  visibilityLabel: string
  retrievalTags: string[]
  maxRecords: number
  maxSummaryBytes: number
  recordCount: number
  candidateCount: number
  boundAgents: string[]
}

export interface MemoryProfileListViewModel {
  summary: ResourceSummaryViewModel
  profiles: MemoryProfileViewModel[]
}

export interface MemoryDigestFormViewModel {
  agentId: string
  missionId: string
  taskId: string
  profileRefs: string
}

export interface MemoryDigestViewModel {
  id: string
  agentId: string
  missionId: string
  taskId: string
  scopeLabel: string
  sourceMemoryRefs: string[]
  sourceEvidenceRefs: string[]
  summary: string
  facts: string[]
  preferences: string[]
  warnings: string[]
  decisions: string[]
  confidenceLabel: string
  visibilityLabel: string
  createdAt: string
  expiresAt: string
}
