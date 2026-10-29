export interface SettingsViewModel {
  platformName: string;
  organizationName: string;
  language: "zh-CN" | "en-US";
  autoCreateAgentProfile: boolean;
  autoLinkEvidence: boolean;
  hilDefaultEnabled: boolean;
  discussionAgentPairLoopLimit: string;
  workspaceRoot: string;
  workspaceAllowlist: string;
  memory: SettingsMemoryViewModel;
  knowledge: SettingsKnowledgeViewModel;
  runtimeProfiles: SettingsRuntimeProfileViewModel[];
  llmProfiles: SettingsLLMProfileViewModel[];
  promptBlocks: SettingsPromptBlockViewModel[];
  runtimeApprovals: SettingsRuntimeApprovalPolicyViewModel;
  notificationRules: SettingsNotificationRuleViewModel[];
}

export type RuntimeApprovalHILModeView = "manual" | "auto";
export type RuntimeApprovalPolicyView = "on-request" | "never";
export type RuntimeApprovalsReviewerView = "human" | "ai";
export type RuntimeApprovalSecurityModeView = "deny" | "allowlist" | "full";
export type RuntimeApprovalEffectView = "read" | "write" | "network" | "delete" | "publish" | "permission" | "identity" | "credential" | "financial" | "secret" | "unknown";
export type RuntimeApprovalRiskView = "" | "low" | "medium" | "high" | "critical" | "unknown";

export interface SettingsRuntimeApprovalReviewerViewModel {
  llmProfileId: string;
  timeoutMs: string;
  maxTokens: string;
}

export interface SettingsRuntimeApprovalExecRuleViewModel {
  ruleId: string;
  executable: string;
  subcommands: string[];
  safeFlags: string[];
  workspaceOnly: boolean;
}

export interface SettingsRuntimeApprovalExecPolicyViewModel {
  approvalPolicy: RuntimeApprovalPolicyView;
  security: RuntimeApprovalSecurityModeView;
  allowlist: SettingsRuntimeApprovalExecRuleViewModel[];
}

export interface SettingsRuntimeApprovalToolRuleViewModel {
  ruleId: string;
  toolId: string;
  effects: RuntimeApprovalEffectView[];
  risk: RuntimeApprovalRiskView;
}

export interface SettingsRuntimeApprovalToolPolicyViewModel {
  security: RuntimeApprovalSecurityModeView;
  allowlist: SettingsRuntimeApprovalToolRuleViewModel[];
}

export interface SettingsRuntimeApprovalOverrideViewModel {
  hilMode?: RuntimeApprovalHILModeView;
  approvalPolicy?: RuntimeApprovalPolicyView;
  approvalsReviewer?: RuntimeApprovalsReviewerView;
  approvalBypass?: boolean;
  exec?: SettingsRuntimeApprovalExecPolicyViewModel;
  tools?: SettingsRuntimeApprovalToolPolicyViewModel;
}

export interface SettingsRuntimeApprovalAgentOverrideViewModel extends SettingsRuntimeApprovalOverrideViewModel {
  logicalAgentId: string;
}

export interface SettingsRuntimeApprovalPolicyViewModel {
  defaultHILMode: RuntimeApprovalHILModeView;
  defaultApprovalPolicy: RuntimeApprovalPolicyView;
  defaultApprovalsReviewer: RuntimeApprovalsReviewerView;
  reviewer: SettingsRuntimeApprovalReviewerViewModel;
  exec: SettingsRuntimeApprovalExecPolicyViewModel;
  tools: SettingsRuntimeApprovalToolPolicyViewModel;
  forcedHuman: {
    toolIds: string[];
    execExecutables: string[];
  };
  conversationDefaults: Record<string, SettingsRuntimeApprovalOverrideViewModel>;
  agentOverrides: SettingsRuntimeApprovalAgentOverrideViewModel[];
}

export interface SettingsMemoryViewModel {
  enabled: boolean;
}

export interface SettingsKnowledgeViewModel {
  enabled: boolean;
  defaultProvider: "pageindex" | "local";
  pageindex: SettingsPageIndexKnowledgeViewModel;
  local: SettingsLocalKnowledgeViewModel;
}

export interface SettingsPageIndexKnowledgeViewModel {
  apiUrl: string;
  apiKey: string;
  apiKeyRef: string;
  defaultFolderId: string;
  timeoutSeconds: string;
}

export interface SettingsPageIndexConnectionTestViewModel {
  status: "ok" | "warning" | "failed";
  apiUrl: string;
  endpoint: string;
  httpStatus?: number;
  latencyMs: number;
  message: string;
}

export interface SettingsLocalKnowledgeViewModel {
  enabled: boolean;
}

export interface SettingsRuntimeProfileViewModel {
  profileId: string;
  displayName: string;
  runtimeKind: string;
  runtimeConfigRef: string;
  runtimeConfig: Record<string, string | string[]>;
  enabled: boolean;
}

export interface SettingsLLMProfileViewModel {
  profileId: string;
  displayName: string;
  provider: string;
  baseUrl: string;
  apiKey: string;
  apiKeyRef: string;
  model: string;
  temperature: string;
  contextLength: string;
  enabled: boolean;
  globalDefault: boolean;
}

export interface SettingsNotificationRuleViewModel {
  id: string;
  labelKey: string;
  descriptionKey: string;
  enabled: boolean;
}

export interface SettingsPromptBlockViewModel {
  blockId: string;
  code: string;
  title: string;
  description: string;
  role: "system" | "user" | "assistant";
  content: string;
  placeholder: string;
  dynamic: boolean;
  editable: boolean;
  removable: boolean;
  variableOnly: boolean;
  enabled: boolean;
}

export interface RuntimeDiscoveryCandidateViewModel {
  kind: string;
  label: string;
  executable: string;
  configRef: string;
  version: string;
  status: string;
  source: string;
  runtimeConfig: Record<string, string | string[]>;
  notes: string[];
}

export interface GatewaySettingsViewModel {
  configPath: string;
  rawYaml: string;
  server: GatewayServerSettingsViewModel;
  channels: Record<string, GatewayChannelSettingsViewModel>;
  feishuAccounts?: Record<string, GatewayFeishuAccountViewModel>;
}

export interface GatewayFeishuAccountViewModel {
  enabled: boolean;
  app_id?: string;
  app_secret?: string;
  encrypt_key?: string;
  verify_token?: string;
  default_session_label: string;
  connection_mode?: string;
  domain_name?: string;
}

export interface GatewayServerSettingsViewModel {
  port: string;
  bindAddress: string;
  idleSessionTimeoutMinutes: string;
  staticAssetsPath: string;
  maxPerSessionMcpInstances: string;
}

export interface GatewayChannelSettingsViewModel {
  key: string;
  label: string;
  enabled: boolean;
  token: string;
  apiKey: string;
  webhookUrl: string;
  extraText: string;
  extraFields: GatewayExtraFieldViewModel[];
}

export interface GatewayExtraFieldViewModel {
  id: string;
  key: string;
  value: string;
}
