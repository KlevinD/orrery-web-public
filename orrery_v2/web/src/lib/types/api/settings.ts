import type { ImageGenerationSettingsDTO } from './image-generation';

export interface SettingsDTO {
  platform_name: string;
  organization_name: string;
  language: "zh-CN" | "en-US";
  auto_create_agent_profile: boolean;
  auto_link_evidence: boolean;
  hil_default_enabled: boolean;
  discussion_agent_pair_loop_limit: number;
  workspace_root?: string;
  workspace_allowlist?: string[];
  memory?: SettingsMemoryDTO;
  knowledge?: SettingsKnowledgeDTO;
  image_generation?: ImageGenerationSettingsDTO;
  runtime_profiles?: SettingsRuntimeProfileDTO[];
  llm_profiles?: SettingsLLMProfileDTO[];
  prompt_blocks?: SettingsPromptBlockDTO[];
  runtime_approvals?: SettingsRuntimeApprovalPolicyDTO;
  notification_rules: SettingsNotificationRuleDTO[];
}

export type RuntimeApprovalHILMode = "manual" | "auto";
export type RuntimeApprovalPolicy = "on-request" | "never";
export type RuntimeApprovalsReviewer = "human" | "ai";
export type RuntimeApprovalSecurityMode = "deny" | "allowlist" | "full";
export type RuntimeApprovalEffect = "read" | "write" | "network" | "delete" | "publish" | "permission" | "identity" | "credential" | "financial" | "secret" | "unknown";
export type RuntimeApprovalRisk = "" | "low" | "medium" | "high" | "critical" | "unknown";

export interface SettingsRuntimeApprovalReviewerDTO {
  llm_profile_id?: string;
  timeout_ms: number;
  max_tokens: number;
}

export interface SettingsRuntimeApprovalExecRuleDTO {
  rule_id?: string;
  executable: string;
  subcommands?: string[];
  safe_flags?: string[];
  workspace_only?: boolean;
}

export interface SettingsRuntimeApprovalExecPolicyDTO {
  approval_policy: RuntimeApprovalPolicy;
  security: RuntimeApprovalSecurityMode;
  allowlist: SettingsRuntimeApprovalExecRuleDTO[];
}

export interface SettingsRuntimeApprovalToolRuleDTO {
  rule_id?: string;
  tool_id: string;
  effects?: RuntimeApprovalEffect[];
  risk?: RuntimeApprovalRisk;
}

export interface SettingsRuntimeApprovalToolPolicyDTO {
  security: RuntimeApprovalSecurityMode;
  allowlist: SettingsRuntimeApprovalToolRuleDTO[];
}

export interface SettingsRuntimeApprovalForcedHumanDTO {
  tool_ids: string[];
  exec_executables: string[];
}

export interface SettingsRuntimeApprovalOverrideDTO {
  hil_mode?: RuntimeApprovalHILMode;
  approval_policy?: RuntimeApprovalPolicy;
  approvals_reviewer?: RuntimeApprovalsReviewer;
  approval_bypass?: boolean;
  exec?: SettingsRuntimeApprovalExecPolicyDTO;
  tools?: SettingsRuntimeApprovalToolPolicyDTO;
}

export interface SettingsRuntimeApprovalAgentOverrideDTO extends SettingsRuntimeApprovalOverrideDTO {
  logical_agent_id: string;
}

export interface SettingsRuntimeApprovalPolicyDTO {
  default_hil_mode: RuntimeApprovalHILMode;
  default_approval_policy: RuntimeApprovalPolicy;
  default_approvals_reviewer: RuntimeApprovalsReviewer;
  reviewer: SettingsRuntimeApprovalReviewerDTO;
  exec: SettingsRuntimeApprovalExecPolicyDTO;
  tools: SettingsRuntimeApprovalToolPolicyDTO;
  forced_human: SettingsRuntimeApprovalForcedHumanDTO;
  conversation_defaults: Record<string, SettingsRuntimeApprovalOverrideDTO>;
  agent_overrides: SettingsRuntimeApprovalAgentOverrideDTO[];
}

export interface SettingsMemoryDTO {
  enabled: boolean;
}

export interface SettingsKnowledgeDTO {
  enabled: boolean;
  default_provider: "pageindex" | "local";
  pageindex: SettingsPageIndexKnowledgeDTO;
  local: SettingsLocalKnowledgeDTO;
}

export interface SettingsPageIndexKnowledgeDTO {
  api_url: string;
  api_key?: string;
  api_key_ref?: string;
  default_folder_id?: string;
  timeout_seconds?: number;
}

export interface SettingsLocalKnowledgeDTO {
  enabled: boolean;
}

export interface SettingsPageIndexConnectionTestRequestDTO {
  pageindex: SettingsPageIndexKnowledgeDTO;
}

export interface SettingsPageIndexConnectionTestResultDTO {
  status: "ok" | "warning" | "failed";
  api_url: string;
  endpoint: string;
  http_status?: number;
  latency_ms: number;
  message?: string;
}

export interface SettingsRuntimeProfileDTO {
  profile_id: string;
  display_name: string;
  runtime_kind: string;
  runtime_config_ref?: string;
  runtime_config?: Record<string, unknown>;
  enabled: boolean;
}

export interface SettingsLLMProfileDTO {
  profile_id: string;
  display_name: string;
  provider: string;
  base_url?: string;
  api_key?: string;
  api_key_ref?: string;
  model?: string;
  temperature?: number;
  context_length?: number;
  enabled: boolean;
  global_default?: boolean;
}

export interface SettingsNotificationRuleDTO {
  rule_id: string;
  label_key: string;
  description_key: string;
  enabled: boolean;
}

export interface SettingsPromptBlockDTO {
  block_id: string;
  code?: string;
  title: string;
  description?: string;
  role: "system" | "user" | "assistant";
  content?: string;
  placeholder?: string;
  dynamic: boolean;
  editable: boolean;
  removable?: boolean;
  variable_only?: boolean;
  enabled: boolean;
}

export interface RuntimeDiscoveryResponseDTO {
  candidates: RuntimeDiscoveryCandidateDTO[];
}

export interface RuntimeDiscoveryCandidateDTO {
  kind: string;
  label: string;
  executable: string;
  config_ref: string;
  version?: string;
  status: string;
  source: string;
  runtime_config: Record<string, unknown>;
  notes?: string[];
}

export interface GatewaySettingsDTO {
  config_path: string;
  raw_yaml: string;
  config: GatewayConfigDTO;
}

export interface GatewaySettingsUpdateRequest {
  config: GatewayConfigDTO;
}

export interface GatewaySettingsRawUpdateRequest {
  raw_yaml: string;
}

export interface FeishuRegistrationDTO {
  registration_id: string;
  status: 'starting' | 'pending' | 'connected' | 'failed' | 'cancelled' | 'expired';
  verification_url?: string;
  expires_at: string;
  account_id: string;
  error_code?: string;
}

export interface GatewayAccountsDTO {
  accounts: GatewayAccountStatusDTO[];
}

export interface GatewayAccountStatusDTO {
  channel_key: string;
  account_id: string;
  enabled: boolean;
  status: 'connected' | 'degraded' | 'awaiting_binding' | 'disabled' | 'revoked';
  last_error?: string;
}

export interface GatewayWeixinLoginStartResponseDTO {
  session_key: string;
  qrcode_url: string;
  status: string;
  message: string;
  expires_at: string;
}

export interface GatewayWeixinLoginStatusResponseDTO {
  session_key: string;
  status: string;
  message: string;
  qrcode_url?: string;
  connected: boolean;
  already_connected?: boolean;
  account_id?: string;
  base_url?: string;
  user_id?: string;
  bound_user_id?: string;
  agent_id?: string;
  human_id?: string;
  bound_human_id?: string;
  expires_at?: string;
}

export interface GatewayWeixinVerifyCodeRequest {
  verify_code: string;
}

export interface ProfileWeixinDTO {
  tenant_id: string;
  human_id: string;
  weixin_bound: boolean;
  weixin_user_id?: string;
  default_agent_id?: string;
  active_agent_id?: string;
  current_agent_id?: string;
  target_kind: 'agent' | 'personal_assistant';
  agents: ProfileWeixinBotDTO[];
}

export interface ProfileGatewayBindingDTO {
  id: string;
  tenant_id: string;
  channel_key: 'feishu' | 'dingtalk' | 'weixin' | string;
  account_id: string;
  external_tenant_id?: string;
  external_user_id: string;
  human_profile_id: string;
  display_name?: string;
  default_agent_id?: string;
  active_agent_id?: string;
  status: string;
  verified_at: string;
  created_at: string;
  updated_at: string;
}

export interface ProfileGatewayBindingAccountDTO {
  channel_key: 'feishu';
  account_id: string;
  agent_id: string;
  agent_display_name?: string;
  enabled: boolean;
  status: string;
  inbound_enabled: boolean;
}

export interface ProfileGatewayBindingsDTO {
  bindings: ProfileGatewayBindingDTO[];
  accounts: ProfileGatewayBindingAccountDTO[];
}

export interface ProfileIdentityBindingDTO {
  provider: 'feishu' | string;
  configured: boolean;
  bound: boolean;
  display_name?: string;
  email?: string;
  bound_at?: string;
}

export type PersonalAssistantHostingMode = 'orrery_managed' | 'owned_agent';

export type OwnedAgentRuntimeKind = 'openclaw' | 'hermes' | 'codex' | 'pi' | 'aionrs' | 'acp' | 'other';

export type OwnedAgentStatus = 'online' | 'offline' | 'unavailable' | 'revoked';

export interface PersonalAssistantWorkspaceGrantDTO {
  grant_id: string;
  display_name: string;
  capabilities: string[];
  available: boolean;
}

export interface PersonalAssistantOwnedAgentDTO {
  owned_agent_id: string;
  device_id: string;
  device_name: string;
  display_name: string;
  runtime_kind: OwnedAgentRuntimeKind;
  capabilities: string[];
  workspace_grants: PersonalAssistantWorkspaceGrantDTO[];
  status: OwnedAgentStatus;
  last_seen_at: string;
}

export interface PersonalAssistantProfileDTO {
  assistant_enabled: boolean;
  agent_profile_id?: string;
  display_name: string;
  system_prompt?: string;
  assigned_agent_id?: string;
  hosting_mode: PersonalAssistantHostingMode;
  owned_agent_id?: string;
  workspace_grant_id?: string;
  fallback_to_managed: boolean;
  revision: number;
  updated_at?: string;
  avatar_url?: string;
  avatar_etag?: string;
  execution_identity: 'user' | string;
  cloud_fallback: boolean;
  owned_agents: PersonalAssistantOwnedAgentDTO[];
}

export interface PersonalAssistantNotificationPreferencesDTO {
  approval_notifications_enabled: boolean;
  channel: 'weixin';
  revision: number;
  updated_at?: string;
}

export interface PersonalAssistantAvatarDTO {
  avatar_url: string;
  content_type: string;
  size: number;
  updated_at: string;
  etag: string;
}

export interface UpdatePersonalAssistantProfileRequest {
  assistant_enabled: boolean;
  assigned_agent_id?: string;
  hosting_mode: PersonalAssistantHostingMode;
  owned_agent_id?: string;
  workspace_grant_id?: string;
  fallback_to_managed: boolean;
  revision: number;
}

export interface UpdatePersonalAssistantNotificationsRequest {
  approval_notifications_enabled: boolean;
  revision: number;
}

export interface ProfileGatewayBindingCodeDTO {
  channel_key: string;
  account_id: string;
  code: string;
  command: string;
  expires_at: string;
}

export interface ProfileWeixinBotDTO {
  agent_id: string;
  display_name: string;
  real_name?: string;
  role_name?: string;
  enabled: boolean;
  default: boolean;
  current: boolean;
}

export interface ProfileWeixinDefaultAgentRequest {
  agent_id?: string;
  target_kind?: 'agent' | 'personal_assistant';
}

export interface ProfileWeixinLoginStartRequest {
  agent_id?: string;
  target_kind?: 'agent' | 'personal_assistant';
}

export interface GatewayConfigDTO {
  gateway: GatewayServerConfigDTO;
  adapters: GatewayAdaptersConfigDTO;
}

export interface GatewayServerConfigDTO {
  port: number;
  bind_address: string;
  idle_session_timeout_minutes: number;
  static_assets_path?: string;
  max_per_session_mcp_instances: number;
}

export interface GatewayFeishuAdapterDTO {
  enabled: boolean;
  app_id?: string;
  app_secret?: string;
  encrypt_key?: string;
  verify_token?: string;
  default_session_label: string;
  connection_mode?: string;
  domain_name?: string;
  accounts?: Record<string, GatewayFeishuAccountDTO>;
}

export interface GatewayFeishuAccountDTO {
  enabled: boolean;
  app_id?: string;
  app_secret?: string;
  encrypt_key?: string;
  verify_token?: string;
  default_session_label: string;
  connection_mode?: string;
  domain_name?: string;
}

export interface GatewayPlatformAdapterDTO {
  enabled: boolean;
  token?: string;
  api_key?: string;
  webhook_url?: string;
  extra?: Record<string, unknown>;
}

export interface GatewayWeixinAdapterDTO {
  enabled: boolean;
  extra?: Record<string, unknown>;
}

export interface GatewayAdaptersConfigDTO {
  feishu?: GatewayFeishuAdapterDTO;
  weixin?: GatewayWeixinAdapterDTO;
  dingtalk?: GatewayPlatformAdapterDTO;
  email?: GatewayPlatformAdapterDTO;
}
