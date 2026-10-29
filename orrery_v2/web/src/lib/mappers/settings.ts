import type {
  RuntimeDiscoveryCandidateDTO,
  RuntimeDiscoveryResponseDTO,
  SettingsDTO,
  SettingsKnowledgeDTO,
  SettingsPageIndexConnectionTestRequestDTO,
  SettingsPageIndexConnectionTestResultDTO,
  SettingsPromptBlockDTO,
  SettingsRuntimeApprovalExecPolicyDTO,
  SettingsRuntimeApprovalOverrideDTO,
  SettingsRuntimeApprovalPolicyDTO,
  SettingsRuntimeApprovalToolPolicyDTO,
  SettingsRuntimeProfileDTO,
} from "#lib/types/api";
import type {
  RuntimeDiscoveryCandidateViewModel,
  SettingsPageIndexConnectionTestViewModel,
  SettingsPageIndexKnowledgeViewModel,
  SettingsKnowledgeViewModel,
  SettingsPromptBlockViewModel,
  SettingsRuntimeApprovalExecPolicyViewModel,
  SettingsRuntimeApprovalOverrideViewModel,
  SettingsRuntimeApprovalPolicyViewModel,
  SettingsRuntimeApprovalToolPolicyViewModel,
  SettingsRuntimeProfileViewModel,
  SettingsViewModel,
} from "#lib/types/view-models";

export function mapSettingsView(dto: SettingsDTO): SettingsViewModel {
  return {
    platformName: dto.platform_name,
    organizationName: dto.organization_name,
    language: dto.language,
    autoCreateAgentProfile: dto.auto_create_agent_profile,
    autoLinkEvidence: dto.auto_link_evidence,
    hilDefaultEnabled: dto.hil_default_enabled,
    discussionAgentPairLoopLimit: String(
      dto.discussion_agent_pair_loop_limit ?? 8,
    ),
    workspaceRoot: dto.workspace_root ?? "",
    workspaceAllowlist: (dto.workspace_allowlist ?? []).join("\n"),
    memory: {
      enabled: dto.memory?.enabled ?? false,
    },
    knowledge: mapKnowledgeView(dto.knowledge),
    runtimeProfiles: mapRuntimeProfilesView(dto),
    llmProfiles: (dto.llm_profiles ?? []).map((profile) => ({
      profileId: profile.profile_id,
      displayName: profile.display_name,
      provider: profile.provider,
      baseUrl: profile.base_url ?? "",
      apiKey: "",
      apiKeyRef: profile.api_key_ref ?? "",
      model: profile.model ?? "",
      temperature:
        profile.temperature === undefined ? "" : String(profile.temperature),
      contextLength:
        profile.context_length === undefined
          ? ""
          : String(profile.context_length),
      enabled: profile.enabled,
      globalDefault: profile.global_default ?? false,
    })),
    promptBlocks: (dto.prompt_blocks ?? []).map(mapPromptBlockView),
    runtimeApprovals: mapRuntimeApprovalPolicyView(dto.runtime_approvals),
    notificationRules: dto.notification_rules.map((rule) => ({
      id: rule.rule_id,
      labelKey: rule.label_key,
      descriptionKey: rule.description_key,
      enabled: rule.enabled,
    })),
  };
}

export function mapSettingsCommand(view: SettingsViewModel): SettingsDTO {
  const command: SettingsDTO = {
    platform_name: view.platformName,
    organization_name: view.organizationName,
    language: view.language,
    auto_create_agent_profile: view.autoCreateAgentProfile,
    auto_link_evidence: view.autoLinkEvidence,
    hil_default_enabled: view.hilDefaultEnabled,
    discussion_agent_pair_loop_limit: cleanBoundedInteger(
      view.discussionAgentPairLoopLimit,
      8,
    ),
    workspace_root: cleanOptional(view.workspaceRoot),
    workspace_allowlist: cleanStringList(view.workspaceAllowlist),
    memory: {
      enabled: view.memory?.enabled ?? false,
    },
    knowledge: mapKnowledgeCommand(view.knowledge),
    runtime_profiles: view.runtimeProfiles.map(mapRuntimeProfileCommand),
    llm_profiles: view.llmProfiles.map((profile) =>
      cleanUndefined({
        profile_id: profile.profileId,
        display_name: profile.displayName,
        provider: profile.provider,
        base_url: cleanOptional(profile.baseUrl),
        api_key_ref: cleanOptional(profile.apiKeyRef),
        model: cleanOptional(profile.model),
        temperature: cleanOptionalNumber(profile.temperature),
        context_length: cleanOptionalInteger(profile.contextLength),
        enabled: profile.enabled,
        global_default: profile.globalDefault,
      }),
    ),
    prompt_blocks: (view.promptBlocks ?? []).map(mapPromptBlockCommand),
    runtime_approvals: mapRuntimeApprovalPolicyCommand(
      view.runtimeApprovals ?? mapRuntimeApprovalPolicyView(undefined),
    ),
    notification_rules: view.notificationRules.map((rule) => ({
      rule_id: rule.id,
      label_key: rule.labelKey,
      description_key: rule.descriptionKey,
      enabled: rule.enabled,
    })),
  };
  return command;
}

const defaultRuntimeApprovalPolicy: SettingsRuntimeApprovalPolicyDTO = {
  default_hil_mode: "manual",
  default_approval_policy: "on-request",
  default_approvals_reviewer: "human",
  reviewer: { timeout_ms: 30000, max_tokens: 360 },
  exec: { approval_policy: "never", security: "allowlist", allowlist: [] },
  tools: { security: "allowlist", allowlist: [] },
  forced_human: { tool_ids: [], exec_executables: [] },
  conversation_defaults: { personal_assistant: { hil_mode: "auto" } },
  agent_overrides: [],
};

function mapRuntimeApprovalPolicyView(
  value: SettingsRuntimeApprovalPolicyDTO | undefined,
): SettingsRuntimeApprovalPolicyViewModel {
  const policy = value ?? defaultRuntimeApprovalPolicy;
  return {
    defaultHILMode: policy.default_hil_mode ?? "manual",
    defaultApprovalPolicy: policy.default_approval_policy ?? "on-request",
    defaultApprovalsReviewer: policy.default_approvals_reviewer ?? reviewerFromHIL(policy.default_hil_mode),
    reviewer: {
      llmProfileId: policy.reviewer?.llm_profile_id ?? "",
      timeoutMs: String(policy.reviewer?.timeout_ms ?? 30000),
      maxTokens: String(policy.reviewer?.max_tokens ?? 360),
    },
    exec: mapRuntimeApprovalExecPolicyView(policy.exec),
    tools: mapRuntimeApprovalToolPolicyView(policy.tools),
    forcedHuman: {
      toolIds: [...(policy.forced_human?.tool_ids ?? [])],
      execExecutables: [...(policy.forced_human?.exec_executables ?? [])],
    },
    conversationDefaults: Object.fromEntries(
      Object.entries(policy.conversation_defaults ?? {}).map(([kind, override]) => [kind, mapRuntimeApprovalOverrideView(override)]),
    ),
    agentOverrides: (policy.agent_overrides ?? []).map((override) => ({
      logicalAgentId: override.logical_agent_id,
      ...mapRuntimeApprovalOverrideView(override),
    })),
  };
}

function mapRuntimeApprovalPolicyCommand(
  policy: SettingsRuntimeApprovalPolicyViewModel,
): SettingsRuntimeApprovalPolicyDTO {
  return {
    default_hil_mode: policy.defaultHILMode,
    default_approval_policy: policy.defaultApprovalPolicy,
    default_approvals_reviewer: policy.defaultApprovalsReviewer,
    reviewer: cleanUndefined({
      llm_profile_id: cleanOptional(policy.reviewer.llmProfileId),
      timeout_ms: cleanBoundedInteger(policy.reviewer.timeoutMs, 30000),
      max_tokens: cleanBoundedInteger(policy.reviewer.maxTokens, 360),
    }),
    exec: mapRuntimeApprovalExecPolicyCommand(policy.exec),
    tools: mapRuntimeApprovalToolPolicyCommand(policy.tools),
    forced_human: {
      tool_ids: cleanStringArray(policy.forcedHuman.toolIds),
      exec_executables: cleanStringArray(policy.forcedHuman.execExecutables),
    },
    conversation_defaults: Object.fromEntries(
      Object.entries(policy.conversationDefaults).map(([kind, override]) => [kind.trim(), mapRuntimeApprovalOverrideCommand(override)]).filter(([kind]) => kind !== ""),
    ),
    agent_overrides: policy.agentOverrides.map((override) => ({
      logical_agent_id: override.logicalAgentId.trim(),
      ...mapRuntimeApprovalOverrideCommand(override),
    })),
  };
}

function mapRuntimeApprovalExecPolicyView(
  policy: SettingsRuntimeApprovalExecPolicyDTO | undefined,
): SettingsRuntimeApprovalExecPolicyViewModel {
  return {
    approvalPolicy: policy?.approval_policy ?? "never",
    security: policy?.security ?? "allowlist",
    allowlist: (policy?.allowlist ?? []).map((rule) => ({
      ruleId: rule.rule_id ?? "",
      executable: rule.executable,
      subcommands: [...(rule.subcommands ?? [])],
      safeFlags: [...(rule.safe_flags ?? [])],
      workspaceOnly: rule.workspace_only ?? false,
    })),
  };
}

function mapRuntimeApprovalExecPolicyCommand(
  policy: SettingsRuntimeApprovalExecPolicyViewModel,
): SettingsRuntimeApprovalExecPolicyDTO {
  return {
    approval_policy: policy.approvalPolicy,
    security: policy.security,
    allowlist: policy.allowlist.map((rule) => cleanUndefined({
      rule_id: cleanOptional(rule.ruleId),
      executable: rule.executable.trim(),
      subcommands: cleanStringArray(rule.subcommands),
      safe_flags: cleanStringArray(rule.safeFlags),
      workspace_only: rule.workspaceOnly || undefined,
    })),
  };
}

function mapRuntimeApprovalToolPolicyView(
  policy: SettingsRuntimeApprovalToolPolicyDTO | undefined,
): SettingsRuntimeApprovalToolPolicyViewModel {
  return {
    security: policy?.security ?? "allowlist",
    allowlist: (policy?.allowlist ?? []).map((rule) => ({
      ruleId: rule.rule_id ?? "",
      toolId: rule.tool_id,
      effects: [...(rule.effects ?? [])],
      risk: rule.risk ?? "",
    })),
  };
}

function mapRuntimeApprovalToolPolicyCommand(
  policy: SettingsRuntimeApprovalToolPolicyViewModel,
): SettingsRuntimeApprovalToolPolicyDTO {
  return {
    security: policy.security,
    allowlist: policy.allowlist.map((rule) => cleanUndefined({
      rule_id: cleanOptional(rule.ruleId),
      tool_id: rule.toolId.trim(),
      effects: [...new Set(rule.effects)],
      risk: rule.risk || undefined,
    })),
  };
}

function mapRuntimeApprovalOverrideView(
  override: SettingsRuntimeApprovalOverrideDTO,
): SettingsRuntimeApprovalOverrideViewModel {
  return cleanUndefined({
    hilMode: override.hil_mode,
    approvalPolicy: override.approval_policy ?? (override.approval_bypass ? "never" : override.hil_mode ? "on-request" : undefined),
    approvalsReviewer: override.approvals_reviewer ?? (override.hil_mode ? reviewerFromHIL(override.hil_mode) : undefined),
    exec: override.exec ? mapRuntimeApprovalExecPolicyView(override.exec) : undefined,
    tools: override.tools ? mapRuntimeApprovalToolPolicyView(override.tools) : undefined,
  });
}

function mapRuntimeApprovalOverrideCommand(
  override: SettingsRuntimeApprovalOverrideViewModel,
): SettingsRuntimeApprovalOverrideDTO {
  return cleanUndefined({
    hil_mode: override.hilMode,
    approval_policy: override.approvalPolicy,
    approvals_reviewer: override.approvalsReviewer,
    exec: override.exec ? mapRuntimeApprovalExecPolicyCommand(override.exec) : undefined,
    tools: override.tools ? mapRuntimeApprovalToolPolicyCommand(override.tools) : undefined,
  });
}

function reviewerFromHIL(mode: "manual" | "auto" | undefined): "human" | "ai" {
  return mode === "auto" ? "ai" : "human";
}

function cleanStringArray(values: string[]): string[] {
  return [...new Set(values.map((value) => value.trim()).filter(Boolean))];
}

const defaultKnowledgeSettings: SettingsKnowledgeDTO = {
  enabled: true,
  default_provider: "pageindex",
  pageindex: {
    api_url: "https://api.pageindex.ai",
    timeout_seconds: 30,
  },
  local: {
    enabled: true,
  },
};

function mapKnowledgeView(
  value: SettingsKnowledgeDTO | undefined,
): SettingsKnowledgeViewModel {
  const dto = value ?? defaultKnowledgeSettings;
  return {
    enabled: dto.enabled ?? true,
    defaultProvider: dto.default_provider ?? "pageindex",
    pageindex: {
      apiUrl: dto.pageindex?.api_url ?? "https://api.pageindex.ai",
      apiKey: "",
      apiKeyRef: dto.pageindex?.api_key_ref ?? "",
      defaultFolderId: dto.pageindex?.default_folder_id ?? "",
      timeoutSeconds:
        dto.pageindex?.timeout_seconds === undefined
          ? "30"
          : String(dto.pageindex.timeout_seconds),
    },
    local: {
      enabled: dto.local?.enabled ?? true,
    },
  };
}

function mapKnowledgeCommand(
  view: SettingsKnowledgeViewModel | undefined,
): SettingsKnowledgeDTO {
  const knowledge = view ?? mapKnowledgeView(undefined);
  return {
    enabled: knowledge.enabled,
    default_provider: knowledge.defaultProvider,
    pageindex: cleanUndefined({
      api_url:
        cleanOptional(knowledge.pageindex.apiUrl) ?? "https://api.pageindex.ai",
      api_key_ref: cleanOptional(knowledge.pageindex.apiKeyRef),
      default_folder_id: cleanOptional(knowledge.pageindex.defaultFolderId),
      timeout_seconds: cleanOptionalInteger(knowledge.pageindex.timeoutSeconds),
    }),
    local: {
      enabled: knowledge.local.enabled,
    },
  };
}

export function mapPageIndexConnectionTestCommand(
  pageIndex: SettingsPageIndexKnowledgeViewModel,
): SettingsPageIndexConnectionTestRequestDTO {
  return {
    pageindex: cleanUndefined({
      api_url: cleanOptional(pageIndex.apiUrl) ?? "https://api.pageindex.ai",
      api_key: cleanOptional(pageIndex.apiKey),
      api_key_ref: cleanOptional(pageIndex.apiKeyRef),
      timeout_seconds: cleanOptionalInteger(pageIndex.timeoutSeconds),
    }),
  };
}

export function mapPageIndexConnectionTestView(
  dto: SettingsPageIndexConnectionTestResultDTO,
): SettingsPageIndexConnectionTestViewModel {
  return {
    status: dto.status,
    apiUrl: dto.api_url,
    endpoint: dto.endpoint,
    httpStatus: dto.http_status,
    latencyMs: dto.latency_ms,
    message: dto.message ?? "",
  };
}

function mapPromptBlockView(
  block: SettingsPromptBlockDTO,
): SettingsPromptBlockViewModel {
  return {
    blockId: block.block_id,
    code: block.code ?? block.block_id,
    title: block.title,
    description: block.description ?? "",
    role: block.role,
    content: block.content ?? "",
    placeholder: block.placeholder ?? "",
    dynamic: block.dynamic,
    editable: block.editable,
    removable: block.removable ?? false,
    variableOnly: block.variable_only ?? false,
    enabled: block.enabled,
  };
}

function mapPromptBlockCommand(
  block: SettingsPromptBlockViewModel,
): SettingsPromptBlockDTO {
  return cleanUndefined({
    block_id: block.blockId,
    code: cleanOptional(block.code),
    title: block.title,
    description: cleanOptional(block.description),
    role: block.role,
    content: block.editable ? block.content : undefined,
    placeholder: cleanOptional(block.placeholder),
    dynamic: block.dynamic,
    editable: block.editable,
    removable: block.removable || undefined,
    variable_only: block.variableOnly || undefined,
    enabled: block.enabled,
  }) as SettingsPromptBlockDTO;
}

export function mapRuntimeDiscoveryView(
  dto: RuntimeDiscoveryResponseDTO,
): RuntimeDiscoveryCandidateViewModel[] {
  return (dto.candidates ?? []).map(mapRuntimeDiscoveryCandidate);
}

function mapRuntimeDiscoveryCandidate(
  candidate: RuntimeDiscoveryCandidateDTO,
): RuntimeDiscoveryCandidateViewModel {
  return {
    kind: candidate.kind,
    label: candidate.label,
    executable: candidate.executable,
    configRef: candidate.config_ref,
    version: candidate.version ?? "",
    status: candidate.status,
    source: candidate.source,
    runtimeConfig: mapRuntimeConfig(candidate.runtime_config ?? {}),
    notes: candidate.notes ?? [],
  };
}

function mapRuntimeProfilesView(
  dto: SettingsDTO,
): SettingsRuntimeProfileViewModel[] {
  const profiles = dto.runtime_profiles ?? [];
  if (profiles.length > 0) {
    return profiles.map(mapRuntimeProfileView);
  }
  return [];
}

function mapRuntimeProfileView(
  profile: SettingsRuntimeProfileDTO,
): SettingsRuntimeProfileViewModel {
  return {
    profileId: profile.profile_id,
    displayName: profile.display_name,
    runtimeKind: profile.runtime_kind,
    runtimeConfigRef: profile.runtime_config_ref ?? "",
    runtimeConfig: mapRuntimeConfig(profile.runtime_config ?? {}),
    enabled: profile.enabled,
  };
}

function mapRuntimeProfileCommand(
  profile: SettingsRuntimeProfileViewModel,
): SettingsRuntimeProfileDTO {
  return {
    profile_id: profile.profileId,
    display_name: profile.displayName,
    runtime_kind: profile.runtimeKind,
    runtime_config_ref: cleanOptional(profile.runtimeConfigRef),
    runtime_config: mapRuntimeConfigCommand(profile.runtimeConfig),
    enabled: profile.enabled,
  };
}

function mapRuntimeConfig(
  config: Record<string, unknown>,
): Record<string, string | string[]> {
  const mapped: Record<string, string | string[]> = {};
  for (const [key, value] of Object.entries(config)) {
    if (Array.isArray(value)) {
      mapped[key] = value.map((item) => String(item));
    } else if (value !== undefined && value !== null) {
      mapped[key] = String(value);
    }
  }
  return mapped;
}

function mapRuntimeConfigCommand(
  config: Record<string, string | string[]> | undefined,
): Record<string, unknown> {
  const mapped: Record<string, unknown> = {};
  for (const [key, value] of Object.entries(config ?? {})) {
    if (Array.isArray(value)) {
      const items = value.map((item) => item.trim()).filter(Boolean);
      if (items.length > 0) mapped[key] = items;
    } else {
      const trimmed = value.trim();
      if (trimmed) mapped[key] = trimmed;
    }
  }
  return mapped;
}

function cleanOptional(
  value: string | number | undefined | null,
): string | undefined {
  const trimmed = String(value ?? "").trim();
  return trimmed.length > 0 ? trimmed : undefined;
}

function cleanStringList(value: string | undefined | null): string[] | undefined {
  const entries = String(value ?? "")
    .split(/\r?\n/)
    .map((entry) => entry.trim())
    .filter(Boolean);
  return entries.length > 0 ? entries : undefined;
}

function cleanUndefined<T extends Record<string, unknown>>(value: T): T {
  return Object.fromEntries(
    Object.entries(value).filter(([, item]) => item !== undefined),
  ) as T;
}

function cleanOptionalNumber(
  value: string | number | undefined | null,
): number | undefined {
  const trimmed = String(value ?? "").trim();
  if (trimmed.length === 0) return undefined;
  const parsed = Number(trimmed);
  return Number.isFinite(parsed) ? parsed : undefined;
}

function cleanOptionalInteger(
  value: string | number | undefined | null,
): number | undefined {
  const parsed = cleanOptionalNumber(value);
  return parsed === undefined ? undefined : Math.trunc(parsed);
}

function cleanBoundedInteger(
  value: string | number | undefined | null,
  fallback: number,
): number {
  const parsed = cleanOptionalInteger(value);
  return parsed === undefined ? fallback : parsed;
}
