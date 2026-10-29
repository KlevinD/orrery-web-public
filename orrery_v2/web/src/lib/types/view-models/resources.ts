import type { BadgeTone } from './common';

export interface ResourceSummaryViewModel {
  total: number;
  enabled: number;
  disabled: number;
  highRisk: number;
  bound: number;
  usageCount: number;
}

export interface ResourceBindingViewModel {
  agentId: string;
  label: string;
  alias: string;
  scope: string;
  mcpAuthRequired: boolean;
  defaultAKConfigured: boolean;
  agentAKConfigured: boolean;
  agentAKMasked: string;
  authHeaderName: string;
}

export interface ResourceBindingFormViewModel {
  resourceId: string;
  resourceType: string;
  agentId: string;
  alias: string;
  scope: string;
  agentAK?: string;
  authHeaderName?: string;
}

export interface ResourceMcpServerCommandFormViewModel {
  toolId: string;
  configJson?: string;
  name: string;
  description: string;
  version: string;
  serverName: string;
  transport: string;
  command: string;
  url: string;
  headers: string;
  authHeaderName?: string;
  defaultAK?: string;
  args: string;
  exposedTools: string;
  tags: string;
}

export interface ResourceToolCommandFormViewModel {
  toolId: string;
  name: string;
  description: string;
  version: string;
  kind: string;
  riskLevel: string;
  command: string;
  url: string;
  tags: string;
  enabled: string;
}

export interface ResourceSkillCommandFormViewModel {
  name: string;
  description: string;
  version: string;
  taskTypeTags: string;
  prompt: string;
}

export interface ResourceSkillBundleCommandFormViewModel {
  name: string;
  description: string;
  skillIds: string[];
}

export interface SkillBundleEditorSubmission extends ResourceSkillBundleCommandFormViewModel {
  files: File[];
}

export interface ToolViewModel {
  id: string;
  name: string;
  description: string;
  kind: string;
  category: string;
  version: string;
  inputSchema?: Record<string, unknown>;
  outputSchema?: Record<string, unknown>;
  enabled: boolean;
  riskLevel: string;
  riskTone: BadgeTone;
  statusLabel: string;
  statusTone: BadgeTone;
  source: string;
  serverToolId: string;
  downstreamToolName: string;
  manageable: boolean;
  command: string;
  url: string;
  tags: string[];
  permissions: string[];
  secretCount: number;
  usageCount: number;
  boundAgentCount: number;
  boundAgents: string[];
  bindings: ResourceBindingViewModel[];
  updatedAt: string;
}

export interface SkillViewModel {
  id: string;
  name: string;
  description: string;
  category: string;
  version: string;
  enabled: boolean;
  riskLevel: string;
  riskTone: BadgeTone;
  statusLabel: string;
  statusTone: BadgeTone;
  taskTypes: string[];
  requiredTools: string[];
  promptAssets: string[];
  evidenceRefs: string[];
  sourceRef: string;
  updatedAt: string;
  skillMdPresent: boolean;
  resourceCounts: SkillResourceCountsViewModel;
  usageCount: number;
  boundAgentCount: number;
  boundAgents: string[];
}

export interface SkillBundleViewModel {
  id: string;
  name: string;
  description: string;
  skillIds: string[];
  skills: SkillViewModel[];
  memberCount: number;
  createdBy: string;
  createdAt: string;
  updatedAt: string;
}

export interface SkillResourceCountsViewModel {
  assets: number;
  resources: number;
  references: number;
  scripts: number;
  other: number;
  total: number;
}

export interface McpServerViewModel extends ToolViewModel {
  serverName: string;
  transport: string;
  command: string;
  endpoint: string;
  headers: Record<string, string>;
  authRequired: boolean;
  authMode: string;
  authHeaderName: string;
  defaultAKConfigured: boolean;
  availabilityStatus: string;
  lastProbe?: McpServerProbeViewModel;
  args: string[];
  exposedTools: string[];
  exposedToolDetails: McpExposedToolViewModel[];
  healthPolicy: string;
}

export interface McpServerProbeViewModel {
  status: string;
  message: string;
  failureReason: string;
  httpStatus: number;
  latencyMs: number;
  checkedAt: string;
}

export interface McpExposedToolViewModel {
  name: string;
  description: string;
  inputSchema?: Record<string, unknown>;
  outputSchema?: Record<string, unknown>;
}

export interface DiscoveredMcpServerViewModel {
  id: string;
  name: string;
  description: string;
  version: string;
  packageRefs: string[];
  tools: string[];
  secretEnvNames: string[];
  homepage: string;
}

export interface McpDiscoveryViewModel {
  servers: DiscoveredMcpServerViewModel[];
}

export interface ToolGatewayViewModel {
  summary: ResourceSummaryViewModel;
  tools: ToolViewModel[];
}

export interface SkillListViewModel {
  summary: ResourceSummaryViewModel;
  skills: SkillViewModel[];
}

export interface SkillBundleListViewModel {
  bundles: SkillBundleViewModel[];
}

export interface McpServerListViewModel {
  summary: ResourceSummaryViewModel;
  servers: McpServerViewModel[];
}
