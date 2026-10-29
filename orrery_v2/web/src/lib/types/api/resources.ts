import type { PageDTO } from './common';

export interface ToolDTO {
  tool_id: string;
  kind?: string;
  name?: string;
  description?: string;
  version?: string;
  input_schema?: Record<string, unknown>;
  output_schema?: Record<string, unknown>;
  enabled?: boolean;
  risk_level?: string;
  tags?: string[];
  secret_refs?: Array<string | SecretRefDTO>;
  permission_declarations?: Array<string | PermissionDeclarationDTO>;
  source?: string;
  server_tool_id?: string;
  downstream_tool_name?: string;
  manageable?: boolean;
  command?: string;
  url?: string;
  created_at?: string;
  updated_at?: string;
}

export interface PermissionDeclarationDTO {
  scope?: string;
  description?: string;
  risk?: string;
  required?: boolean;
}

export interface ToolCallRequest {
  tool_id: string;
  arguments?: Record<string, unknown>;
}

export interface ToolCallResultDTO {
  output?: Record<string, unknown>;
}

export interface SkillDTO {
  skill_id: string;
  name?: string;
  version?: string;
  description?: string;
  domain_tags?: string[];
  task_type_tags?: string[];
  risk_level?: string;
  prompt_assets?: Array<string | PromptAssetDTO>;
  required_tools?: Array<string | ToolRefDTO>;
  evidence_refs?: Array<string | EvidenceRefDTO>;
  enabled?: boolean;
  source_ref?: string;
  skill_md_present?: boolean;
  resource_counts?: SkillResourceCountsDTO;
  created_at?: string;
  updated_at?: string;
}

export interface SkillBundleListDTO {
  bundles: SkillBundleDTO[];
}

export interface SkillBundleDTO {
  bundle_id: string;
  name: string;
  description?: string;
  skill_ids: string[];
  skills: SkillDTO[];
  created_by?: string;
  created_at?: string;
  updated_at?: string;
}

export interface CreateSkillBundleRequest {
  name: string;
  description?: string;
  skill_ids?: string[];
}

export interface AddSkillsToBundleRequest {
  skill_ids: string[];
}

export interface SkillResourceCountsDTO {
  assets: number;
  resources: number;
  references: number;
  scripts: number;
  other: number;
  total: number;
}

export interface SkillDetailDTO {
  skill: SkillDTO;
  skill_md: SkillMarkdownDTO;
  roots: SkillFileRootDTO[];
}

export interface SkillMarkdownDTO {
  path: string;
  raw: string;
  updated_at?: string;
}

export interface SkillFileRootDTO {
  name: string;
  label: string;
  exists: boolean;
  children: SkillFileNodeDTO[];
}

export interface SkillFileNodeDTO {
  path: string;
  name: string;
  type: 'file' | 'directory';
  size: number;
  media_type: string;
  preview_kind: SkillFilePreviewKind;
  download_url?: string;
  children?: SkillFileNodeDTO[];
}

export type SkillFilePreviewKind = 'markdown' | 'text' | 'image' | 'json' | 'none';

export interface SkillFileDTO {
  node: SkillFileNodeDTO;
  path: string;
  name: string;
  size: number;
  media_type: string;
  preview_kind: SkillFilePreviewKind;
  content?: string;
  download_url?: string;
}

export interface SaveSkillMarkdownRequest {
  raw: string;
}

export interface CreateSkillRequest {
  name: string;
  description?: string;
  version?: string;
  task_type_tags?: string[];
  prompt: string;
}

export interface SaveSkillFileRequest {
  content: string;
}

export interface PromptAssetDTO {
  path?: string;
  content_ref?: string;
  content?: string;
  kind?: string;
}

export interface EvidenceRefDTO {
  id?: string;
}

export interface MCPServerDTO extends ToolDTO {
  server_name?: string;
  transport?: string;
  command?: string;
  url?: string;
  headers?: Record<string, string>;
  auth_required?: boolean;
  auth_mode?: string;
  auth_header_name?: string;
  default_ak_configured?: boolean;
  availability_status?: string;
  last_probe?: MCPServerProbeDTO;
  args?: string[];
  exposed_tools?: string[];
  exposed_tool_details?: MCPExposedToolDTO[];
  call_timeout_seconds?: number;
  health_check_policy?: string | HealthCheckDTO;
}

export interface MCPServerProbeDTO {
  status?: string;
  message?: string;
  failure_reason?: string;
  http_status?: number;
  latency_ms?: number;
  checked_at?: string;
}

export interface MCPExposedToolDTO {
  name: string;
  description?: string;
  input_schema?: Record<string, unknown>;
  output_schema?: Record<string, unknown>;
}

export interface HealthCheckDTO {
  mode?: string;
  timeout?: string;
}

export interface ResourceUsageDTO {
  resource_id: string;
  resource_type?: string;
  used_by_agent_ids?: string[];
  required_by_skill_ids?: string[];
  usage_count?: number;
}

export interface ResourceBindingDTO {
  resource_id: string;
  resource_type?: string;
  binding_type?: string;
  bound_to_type?: string;
  bound_to_id?: string;
  bound_to_display_name?: string;
  alias?: string;
  version_constraint?: string;
  permission_scope?: string;
  binding_policy?: string;
  mcp_auth_required?: boolean;
  auth_header_name?: string;
  default_ak_configured?: boolean;
  agent_ak_configured?: boolean;
  agent_ak_masked?: string;
}

export interface ResourceAdminViewDTO {
  mcp_servers?: MCPServerDTO[];
  tools?: ToolDTO[];
  skills?: SkillDTO[];
  usage?: ResourceUsageDTO[];
  bindings?: ResourceBindingDTO[];
  page?: PageDTO;
}

export interface DiscoverMCPResponseDTO {
  servers?: DiscoveredMCPServerDTO[];
}

export interface DiscoveredMCPServerDTO {
  discovered_id: string;
  name: string;
  description?: string;
  version?: string;
  package_refs?: string[];
  tools?: string[];
  env_secret_refs?: SecretRefDTO[];
  homepage?: string;
}

export interface ResourceBindingCommand {
  agent_id: string;
  resource_id: string;
  resource_type: string;
  alias?: string;
  version_constraint?: string;
  permission_scope?: string;
  binding_policy?: string;
  agent_ak?: string;
  auth_header_name?: string;
}

export interface SecretRefDTO {
  name: string;
  env?: string;
}

export interface PermissionDTO {
  scope: string;
  description?: string;
  risk?: string;
  required?: boolean;
}

export interface RegisterMCPServerRequest {
  tool_id?: string;
  name: string;
  description?: string;
  version?: string;
  enabled?: boolean;
  risk_level?: string;
  server_name: string;
  transport?: string;
  command?: string;
  url?: string;
  headers?: Record<string, string>;
  auth_header_name?: string;
  args?: string[];
  env_secret_refs?: SecretRefDTO[];
  exposed_tools?: string[];
  exposed_tool_details?: MCPExposedToolDTO[];
  call_timeout_seconds?: number;
  tags?: string[];
}

export type PatchMCPServerRequest = Partial<Omit<RegisterMCPServerRequest, 'tool_id'>>;

export interface ToolDefinitionRequest {
  tool_id?: string;
  kind?: string;
  name: string;
  description?: string;
  version?: string;
  risk_level?: string;
  enabled?: boolean;
  tags?: string[];
  secret_refs?: SecretRefDTO[];
  permission_declarations?: PermissionDTO[];
  command?: string;
  url?: string;
}

export type PatchToolDefinitionRequest = Partial<Omit<ToolDefinitionRequest, 'tool_id'>>;

export interface MCPServerHealthDTO {
  tool_id: string;
  status?: string;
  mode?: string;
  message?: string;
  failure_reason?: string;
  latency_ms?: number;
  checked_at?: string;
}

export interface MCPServerSyncDTO {
  tool_id: string;
  status?: string;
  exposed_tool_count?: number;
  exposed_tools?: string[];
  exposed_tool_details?: MCPExposedToolDTO[];
  failure_reason?: string;
  latency_ms?: number;
  synced_at?: string;
}

export interface ToolRefDTO {
  tool_id: string;
  version_constraint?: string;
  alias?: string;
}
