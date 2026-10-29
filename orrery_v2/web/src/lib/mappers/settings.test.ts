import { describe, expect, it } from "vitest";
import {
  mapPageIndexConnectionTestCommand,
  mapPageIndexConnectionTestView,
  mapRuntimeDiscoveryView,
  mapSettingsCommand,
  mapSettingsView,
} from "./settings";
import type { RuntimeDiscoveryResponseDTO, SettingsDTO } from "#lib/types/api";
import type { SettingsViewModel } from "#lib/types/view-models";

const defaultRuntimeApprovalsDTO = {
  default_hil_mode: "manual",
  default_approval_policy: "on-request",
  default_approvals_reviewer: "human",
  reviewer: { timeout_ms: 30000, max_tokens: 360 },
  exec: { approval_policy: "never", security: "allowlist", allowlist: [] },
  tools: { security: "allowlist", allowlist: [] },
  forced_human: { tool_ids: [], exec_executables: [] },
  conversation_defaults: { personal_assistant: { hil_mode: "auto", approval_policy: "on-request", approvals_reviewer: "ai" } },
  agent_overrides: [],
} as const;

const defaultRuntimeApprovalsView = {
  defaultHILMode: "manual",
  defaultApprovalPolicy: "on-request",
  defaultApprovalsReviewer: "human",
  reviewer: { llmProfileId: "", timeoutMs: "30000", maxTokens: "360" },
  exec: { approvalPolicy: "never", security: "allowlist", allowlist: [] },
  tools: { security: "allowlist", allowlist: [] },
  forcedHuman: { toolIds: [], execExecutables: [] },
  conversationDefaults: { personal_assistant: { hilMode: "auto", approvalPolicy: "on-request", approvalsReviewer: "ai" } },
  agentOverrides: [],
} as const;

describe("settings mapper", () => {
  it("maps LLM settings DTOs to a page view model and back to an API command", () => {
    const dto = {
      platform_name: "Orrery",
      organization_name: "Acme Ops",
      language: "en-US",
      auto_create_agent_profile: true,
      auto_link_evidence: false,
      hil_default_enabled: true,
      discussion_agent_pair_loop_limit: 5,
      workspace_root: "D:\\OrreryWorkspace",
      workspace_allowlist: ["/srv/orrery/workspaces", "/data/projects"],
      memory: {
        enabled: true,
      },
      knowledge: {
        enabled: true,
        default_provider: "pageindex",
        pageindex: {
          api_url: "https://api.pageindex.ai",
          api_key_ref: "secret://knowledge/pageindex",
          default_folder_id: "folder-123",
          timeout_seconds: 45,
        },
        local: {
          enabled: false,
        },
      },
      runtime_profiles: [],
      llm_profiles: [
        {
          profile_id: "llm-openai",
          display_name: "OpenAI primary",
          provider: "openai",
          base_url: "https://api.openai.com/v1",
          api_key_ref: "secret://llm/openai",
          model: "gpt-5.4",
          temperature: 0.2,
          context_length: 128000,
          enabled: true,
          global_default: true,
        },
        {
          profile_id: "llm-deepseek",
          display_name: "DeepSeek fallback",
          provider: "deepseek",
          api_key_ref: "secret://llm/deepseek",
          model: "deepseek-chat",
          temperature: 0.3,
          context_length: 64000,
          enabled: false,
          global_default: false,
        },
      ],
      prompt_blocks: [
        {
          block_id: "system_principles",
          code: "system_principles",
          title: "系统原则",
          description: "平台级规则",
          role: "system",
          content: "不要模拟说话人前缀。",
          dynamic: false,
          editable: true,
          enabled: true,
        },
        {
          block_id: "custom-tone",
          code: "tone",
          title: "输出语气",
          description: "自由新增",
          role: "assistant",
          content: "保持短句。",
          dynamic: false,
          editable: true,
          removable: true,
          variable_only: true,
          enabled: true,
        },
        {
          block_id: "current_user_input",
          code: "current_user_input",
          title: "当前用户输入",
          description: "运行时注入",
          role: "user",
          placeholder: "<<current_user_input>>",
          dynamic: true,
          editable: false,
          enabled: true,
        },
      ],
      notification_rules: [
        {
          rule_id: "approval",
          label_key: "settings.notification.approval.label",
          description_key: "settings.notification.approval.desc",
          enabled: false,
        },
      ],
    } satisfies SettingsDTO;

    const view = mapSettingsView(dto);

    expect(view).toEqual({
      platformName: "Orrery",
      organizationName: "Acme Ops",
      language: "en-US",
      autoCreateAgentProfile: true,
      autoLinkEvidence: false,
      hilDefaultEnabled: true,
      discussionAgentPairLoopLimit: "5",
      workspaceRoot: "D:\\OrreryWorkspace",
      workspaceAllowlist: "/srv/orrery/workspaces\n/data/projects",
      memory: {
        enabled: true,
      },
      knowledge: {
        enabled: true,
        defaultProvider: "pageindex",
        pageindex: {
          apiUrl: "https://api.pageindex.ai",
          apiKey: "",
          apiKeyRef: "secret://knowledge/pageindex",
          defaultFolderId: "folder-123",
          timeoutSeconds: "45",
        },
        local: {
          enabled: false,
        },
      },
      runtimeProfiles: [],
      llmProfiles: [
        {
          profileId: "llm-openai",
          displayName: "OpenAI primary",
          provider: "openai",
          baseUrl: "https://api.openai.com/v1",
          apiKey: "",
          apiKeyRef: "secret://llm/openai",
          model: "gpt-5.4",
          temperature: "0.2",
          contextLength: "128000",
          enabled: true,
          globalDefault: true,
        },
        {
          profileId: "llm-deepseek",
          displayName: "DeepSeek fallback",
          provider: "deepseek",
          baseUrl: "",
          apiKey: "",
          apiKeyRef: "secret://llm/deepseek",
          model: "deepseek-chat",
          temperature: "0.3",
          contextLength: "64000",
          enabled: false,
          globalDefault: false,
        },
      ],
      promptBlocks: [
        {
          blockId: "system_principles",
          code: "system_principles",
          title: "系统原则",
          description: "平台级规则",
          role: "system",
          content: "不要模拟说话人前缀。",
          placeholder: "",
          dynamic: false,
          editable: true,
          removable: false,
          variableOnly: false,
          enabled: true,
        },
        {
          blockId: "custom-tone",
          code: "tone",
          title: "输出语气",
          description: "自由新增",
          role: "assistant",
          content: "保持短句。",
          placeholder: "",
          dynamic: false,
          editable: true,
          removable: true,
          variableOnly: true,
          enabled: true,
        },
        {
          blockId: "current_user_input",
          code: "current_user_input",
          title: "当前用户输入",
          description: "运行时注入",
          role: "user",
          content: "",
          placeholder: "<<current_user_input>>",
          dynamic: true,
          editable: false,
          removable: false,
          variableOnly: false,
          enabled: true,
        },
      ],
      runtimeApprovals: defaultRuntimeApprovalsView,
      notificationRules: [
        {
          id: "approval",
          labelKey: "settings.notification.approval.label",
          descriptionKey: "settings.notification.approval.desc",
          enabled: false,
        },
      ],
    });
    expect(mapSettingsCommand(view)).toEqual({ ...dto, runtime_approvals: defaultRuntimeApprovalsDTO });
  });

  it("maps multiple runtime registration profiles to and from settings commands", () => {
    const dto = {
      platform_name: "Orrery",
      organization_name: "Acme Ops",
      language: "en-US",
      auto_create_agent_profile: true,
      auto_link_evidence: false,
      hil_default_enabled: true,
      discussion_agent_pair_loop_limit: 8,
      workspace_root: undefined,
      workspace_allowlist: undefined,
      memory: {
        enabled: false,
      },
      knowledge: {
        enabled: true,
        default_provider: "pageindex",
        pageindex: {
          api_url: "https://api.pageindex.ai",
          timeout_seconds: 30,
        },
        local: {
          enabled: true,
        },
      },
      runtime_profiles: [
        {
          profile_id: "runtime-openclaw-local",
          display_name: "OpenClaw",
          runtime_kind: "openclaw",
          runtime_config_ref: "runtime-openclaw-local",
          runtime_config: {
            executable: "openclaw",
            agent_id: "planner",
            extra_args: ["--profile", "prod"],
          },
          enabled: true,
        },
        {
          profile_id: "runtime-codex-local",
          display_name: "Codex",
          runtime_kind: "codex",
          runtime_config_ref: "runtime-codex-local",
          runtime_config: {
            executable: "codex",
          },
          enabled: false,
        },
      ],
      llm_profiles: [],
      prompt_blocks: [],
      notification_rules: [],
    } satisfies SettingsDTO;

    const view = mapSettingsView(dto);

    expect(view.runtimeProfiles).toEqual([
      {
        profileId: "runtime-openclaw-local",
        displayName: "OpenClaw",
        runtimeKind: "openclaw",
        runtimeConfigRef: "runtime-openclaw-local",
        runtimeConfig: {
          executable: "openclaw",
          agent_id: "planner",
          extra_args: ["--profile", "prod"],
        },
        enabled: true,
      },
      {
        profileId: "runtime-codex-local",
        displayName: "Codex",
        runtimeKind: "codex",
        runtimeConfigRef: "runtime-codex-local",
        runtimeConfig: {
          executable: "codex",
        },
        enabled: false,
      },
    ]);
    expect(mapSettingsCommand(view)).toEqual({ ...dto, runtime_approvals: defaultRuntimeApprovalsDTO });
  });

  it("maps runtime discovery candidates independently from system settings", () => {
    const dto = {
      candidates: [
        {
          kind: "openclaw",
          label: "OpenClaw",
          executable: "openclaw",
          config_ref: "runtime-openclaw-local",
          version: "openclaw 2026.5.5",
          status: "ready",
          source: "PATH",
          runtime_config: {
            executable: "openclaw",
            agent_id: "planner",
            extra_args: ["--profile", "prod"],
          },
          notes: ["agents.list detected"],
        },
      ],
    } satisfies RuntimeDiscoveryResponseDTO;

    expect(mapRuntimeDiscoveryView(dto)).toEqual([
      {
        kind: "openclaw",
        label: "OpenClaw",
        executable: "openclaw",
        configRef: "runtime-openclaw-local",
        version: "openclaw 2026.5.5",
        status: "ready",
        source: "PATH",
        runtimeConfig: {
          executable: "openclaw",
          agent_id: "planner",
          extra_args: ["--profile", "prod"],
        },
        notes: ["agents.list detected"],
      },
    ]);
  });

  it("maps PageIndex connection tests from the current form draft", () => {
    const view = {
      apiUrl: " http://127.0.0.1:8091/ ",
      apiKey: "local-dev",
      apiKeyRef: "",
      defaultFolderId: "folder-ignored",
      timeoutSeconds: "30",
    };

    expect(mapPageIndexConnectionTestCommand(view)).toEqual({
      pageindex: {
        api_url: "http://127.0.0.1:8091/",
        api_key: "local-dev",
        timeout_seconds: 30,
      },
    });

    expect(
      mapPageIndexConnectionTestView({
        status: "ok",
        api_url: "http://127.0.0.1:8091",
        endpoint: "http://127.0.0.1:8091/healthz",
        http_status: 200,
        latency_ms: 18,
        message: "PageIndex server is reachable.",
      }),
    ).toEqual({
      status: "ok",
      apiUrl: "http://127.0.0.1:8091",
      endpoint: "http://127.0.0.1:8091/healthz",
      httpStatus: 200,
      latencyMs: 18,
      message: "PageIndex server is reachable.",
    });
  });

  it("accepts numeric LLM form values produced by number inputs", () => {
    const view = {
      platformName: "Orrery",
      organizationName: "Acme Ops",
      language: "en-US",
      autoCreateAgentProfile: true,
      autoLinkEvidence: true,
      hilDefaultEnabled: true,
      discussionAgentPairLoopLimit: 3,
      workspaceRoot: "",
      workspaceAllowlist: "",
      memory: {
        enabled: false,
      },
      runtimeProfiles: [],
      llmProfiles: [
        {
          profileId: "llm-openai",
          displayName: "OpenAI primary",
          provider: "openai",
          baseUrl: "https://api.openai.com/v1",
          apiKey: "",
          apiKeyRef: "",
          model: "gpt-5",
          temperature: 0.2,
          contextLength: 128000,
          enabled: true,
          globalDefault: true,
        },
      ],
      promptBlocks: [],
      notificationRules: [],
    } as unknown as SettingsViewModel;

    expect(mapSettingsCommand(view).llm_profiles).toEqual([
      {
        profile_id: "llm-openai",
        display_name: "OpenAI primary",
        provider: "openai",
        base_url: "https://api.openai.com/v1",
        model: "gpt-5",
        temperature: 0.2,
        context_length: 128000,
        enabled: true,
        global_default: true,
      },
    ]);
  });

  it("maps runtime approval policy settings to the form and back", () => {
	const dto = {
	  platform_name: "Orrery",
	  organization_name: "Acme",
	  language: "zh-CN",
	  auto_create_agent_profile: true,
	  auto_link_evidence: true,
	  hil_default_enabled: true,
	  discussion_agent_pair_loop_limit: 8,
	  memory: { enabled: false },
	  knowledge: {
		enabled: true,
		default_provider: "local",
		pageindex: { api_url: "https://api.pageindex.ai", timeout_seconds: 30 },
		local: { enabled: true },
	  },
	  runtime_profiles: [],
	  llm_profiles: [],
	  prompt_blocks: [],
	  notification_rules: [],
	  runtime_approvals: {
		default_hil_mode: "manual",
		default_approval_policy: "on-request",
		default_approvals_reviewer: "human",
		reviewer: { llm_profile_id: "reviewer", timeout_ms: 25000, max_tokens: 300 },
		exec: { approval_policy: "on-request", security: "allowlist", allowlist: [{ rule_id: "git-read", executable: "git", subcommands: ["status", "diff"], safe_flags: ["--stat"], workspace_only: true }] },
		tools: { security: "allowlist", allowlist: [{ rule_id: "talent-read", tool_id: "discussion_talent_list", effects: ["read"], risk: "low" }] },
		forced_human: { tool_ids: ["publish_contract"], exec_executables: ["rm"] },
		conversation_defaults: {
		  personal_assistant: { hil_mode: "auto", exec: { approval_policy: "never", security: "deny", allowlist: [] } },
		},
		agent_overrides: [{ logical_agent_id: "assistant:human-1", hil_mode: "auto", approval_bypass: true, tools: { security: "deny", allowlist: [] } }],
	  },
	} satisfies SettingsDTO;

	const view = mapSettingsView(dto);
	expect(view.runtimeApprovals).toEqual({
	  defaultHILMode: "manual",
	  defaultApprovalPolicy: "on-request",
	  defaultApprovalsReviewer: "human",
	  reviewer: { llmProfileId: "reviewer", timeoutMs: "25000", maxTokens: "300" },
	  exec: { approvalPolicy: "on-request", security: "allowlist", allowlist: [{ ruleId: "git-read", executable: "git", subcommands: ["status", "diff"], safeFlags: ["--stat"], workspaceOnly: true }] },
	  tools: { security: "allowlist", allowlist: [{ ruleId: "talent-read", toolId: "discussion_talent_list", effects: ["read"], risk: "low" }] },
	  forcedHuman: { toolIds: ["publish_contract"], execExecutables: ["rm"] },
	  conversationDefaults: {
		personal_assistant: { hilMode: "auto", approvalPolicy: "on-request", approvalsReviewer: "ai", exec: { approvalPolicy: "never", security: "deny", allowlist: [] } },
	  },
	  agentOverrides: [{ logicalAgentId: "assistant:human-1", hilMode: "auto", approvalPolicy: "never", approvalsReviewer: "ai", tools: { security: "deny", allowlist: [] } }],
	});
	const command = mapSettingsCommand(view).runtime_approvals!;
	expect(command.default_approval_policy).toBe("on-request");
	expect(command.exec.approval_policy).toBe("on-request");
	expect(command.default_approvals_reviewer).toBe("human");
	expect(command.conversation_defaults.personal_assistant).toMatchObject({ approval_policy: "on-request", approvals_reviewer: "ai" });
	expect(command.agent_overrides[0]).toMatchObject({ approval_policy: "never", approvals_reviewer: "ai" });
	expect(command.agent_overrides[0]).not.toHaveProperty("approval_bypass");
  });

  it("uses fail-closed runtime approval defaults for a legacy settings response", () => {
	const legacy = {
	  platform_name: "Orrery",
	  organization_name: "Acme",
	  language: "zh-CN",
	  auto_create_agent_profile: true,
	  auto_link_evidence: true,
	  hil_default_enabled: true,
	  discussion_agent_pair_loop_limit: 8,
	  memory: { enabled: false },
	  knowledge: { enabled: true, default_provider: "local", pageindex: { api_url: "https://api.pageindex.ai" }, local: { enabled: true } },
	  runtime_profiles: [],
	  llm_profiles: [],
	  prompt_blocks: [],
	  notification_rules: [],
	} as SettingsDTO;
	const view = mapSettingsView(legacy);
	expect(view.runtimeApprovals.defaultHILMode).toBe("manual");
	expect(view.runtimeApprovals.defaultApprovalPolicy).toBe("on-request");
	expect(view.runtimeApprovals.defaultApprovalsReviewer).toBe("human");
	expect(view.runtimeApprovals.reviewer).toEqual({ llmProfileId: "", timeoutMs: "30000", maxTokens: "360" });
	expect(view.runtimeApprovals.exec.approvalPolicy).toBe("never");
	expect(view.runtimeApprovals.conversationDefaults.personal_assistant?.hilMode).toBe("auto");
  });
});
