export type KnowledgeProviderMode = 'read_only' | 'read_write'
export type KnowledgeProviderStatus = 'active' | 'disabled' | 'error'

export interface KnowledgeProviderDTO {
  id: string
  name: string
  mcp_server_id: string
  mode: KnowledgeProviderMode
  status: KnowledgeProviderStatus
  tool_mapping: Record<string, string>
  folder_include?: string[]
  folder_exclude?: string[]
  last_error?: string
  last_synced_at?: string
  created_at?: string
  updated_at?: string
}

export interface KnowledgeProviderPageDTO {
  items: KnowledgeProviderDTO[]
  total: number
}

export interface KnowledgeProviderRequest {
  id: string
  name: string
  mcp_server_id: string
  mode: KnowledgeProviderMode
  status?: KnowledgeProviderStatus
  tool_mapping?: Record<string, string>
  folder_include?: string[]
  folder_exclude?: string[]
}

export interface KnowledgeProviderCapabilityTestDTO {
  binding_id: string
  capabilities: Record<string, string>
}

export interface KnowledgeProviderSyncResultDTO {
  folders_synced: number
  documents_synced: number
  completed_at: string
}
