import type {
  SettingsRuntimeApprovalOverrideViewModel,
  SettingsViewModel
} from '#lib/types/view-models'
import { cloneRuntimeConfig } from './runtimeProfileHelpers'

export function copySettingsView(view: SettingsViewModel): SettingsViewModel {
  return {
    ...view,
    memory: { ...view.memory },
    knowledge: {
      ...view.knowledge,
      pageindex: { ...view.knowledge.pageindex },
      local: { ...view.knowledge.local }
    },
    runtimeProfiles: view.runtimeProfiles.map((profile) => ({
      ...profile,
      runtimeConfig: cloneRuntimeConfig(profile.runtimeConfig)
    })),
    llmProfiles: view.llmProfiles.map((profile) => ({ ...profile })),
    promptBlocks: view.promptBlocks.map((block) => ({ ...block })),
    runtimeApprovals: {
      ...view.runtimeApprovals,
      reviewer: { ...view.runtimeApprovals.reviewer },
      exec: {
        ...view.runtimeApprovals.exec,
        allowlist: view.runtimeApprovals.exec.allowlist.map((rule) => ({
          ...rule,
          subcommands: [...rule.subcommands],
          safeFlags: [...rule.safeFlags]
        }))
      },
      tools: {
        ...view.runtimeApprovals.tools,
        allowlist: view.runtimeApprovals.tools.allowlist.map((rule) => ({ ...rule, effects: [...rule.effects] }))
      },
      forcedHuman: {
        toolIds: [...view.runtimeApprovals.forcedHuman.toolIds],
        execExecutables: [...view.runtimeApprovals.forcedHuman.execExecutables]
      },
      conversationDefaults: Object.fromEntries(
        Object.entries(view.runtimeApprovals.conversationDefaults).map(([kind, override]) => [kind, copyRuntimeApprovalOverride(override)])
      ),
      agentOverrides: view.runtimeApprovals.agentOverrides.map((override) => ({
        logicalAgentId: override.logicalAgentId,
        ...copyRuntimeApprovalOverride(override)
      }))
    },
    notificationRules: view.notificationRules.map((rule) => ({ ...rule }))
  }
}

function copyRuntimeApprovalOverride(override: SettingsRuntimeApprovalOverrideViewModel): SettingsRuntimeApprovalOverrideViewModel {
  return {
    ...(override.hilMode ? { hilMode: override.hilMode } : {}),
    ...(override.exec
      ? {
          exec: {
            ...override.exec,
            allowlist: override.exec.allowlist.map((rule) => ({
              ...rule,
              subcommands: [...rule.subcommands],
              safeFlags: [...rule.safeFlags]
            }))
          }
        }
      : {}),
    ...(override.tools
      ? {
          tools: {
            ...override.tools,
            allowlist: override.tools.allowlist.map((rule) => ({ ...rule, effects: [...rule.effects] }))
          }
        }
      : {})
  }
}
