import type { TranslationKey } from '#lib/i18n'
import type { RuntimeDiscoveryCandidateViewModel } from '#lib/types/view-models'

export type RuntimeProviderKind = 'managed' | 'codex' | 'claude' | 'cursor' | 'gemini' | 'openclaw' | 'hermes' | 'pi' | 'acp'
export type RuntimeConfigValue = string | string[]

export const claudeAgentSDKDefaultApprovalTimeoutMs = '600000'
export const piDefaultApprovalMode = 'on-request'
export const piDefaultApprovalTimeoutMs = '600000'
export const piDefaultPromptAckTimeoutMs = '60000'

export interface RuntimeProviderField {
  name: string
  label: TranslationKey
  kind: 'text' | 'args' | 'select'
  required?: boolean
  placeholder?: TranslationKey
  help?: TranslationKey
  options?: RuntimeProviderFieldOption[]
  visibleWhen?: Record<string, string[]>
}

export interface RuntimeProviderFieldOption {
  value: string
  label: TranslationKey
}

export interface RuntimeProviderSchema {
  kind: RuntimeProviderKind
  label: string
  fields: RuntimeProviderField[]
}

export const runtimeProviderSchemas: RuntimeProviderSchema[] = [
  {
    kind: 'managed',
    label: 'Orrery AgentCore',
    fields: [
      { name: 'config_ref', label: 'runtime.field.configRef', kind: 'text', placeholder: 'runtime.placeholder.configRef', help: 'runtime.help.agentCore' }
    ]
  },
  {
    kind: 'codex',
    label: 'Codex',
    fields: [
      {
        name: 'mode',
        label: 'runtime.field.mode',
        kind: 'select',
        help: 'runtime.help.codexMode',
        options: [
          { value: 'local', label: 'runtime.option.codex.local' },
          { value: 'remote_ws', label: 'runtime.option.codex.remoteWs' }
        ]
      },
      { name: 'executable', label: 'runtime.field.executable', kind: 'text', placeholder: 'runtime.placeholder.codexExecutable', help: 'runtime.help.codexExecutable', visibleWhen: { mode: ['local'] } },
      { name: 'remote_addr', label: 'runtime.field.remoteAddr', kind: 'text', placeholder: 'runtime.placeholder.codexRemoteAddr', help: 'runtime.help.codexRemoteAddr', visibleWhen: { mode: ['remote_ws'] } },
      { name: 'token', label: 'runtime.field.token', kind: 'text', placeholder: 'runtime.placeholder.codexToken', help: 'runtime.help.codexToken', visibleWhen: { mode: ['remote_ws'] } },
      { name: 'token_env', label: 'runtime.field.tokenEnv', kind: 'text', placeholder: 'runtime.placeholder.codexTokenEnv', help: 'runtime.help.codexTokenEnv', visibleWhen: { mode: ['remote_ws'] } },
      { name: 'tls_fingerprint', label: 'runtime.field.tlsFingerprint', kind: 'text', placeholder: 'runtime.placeholder.codexTlsFingerprint', help: 'runtime.help.codexTlsFingerprint', visibleWhen: { mode: ['remote_ws'] } },
      { name: 'remote_cwd', label: 'runtime.field.remoteCwd', kind: 'text', placeholder: 'runtime.placeholder.remoteCwd', help: 'runtime.help.codexRemoteCwd', visibleWhen: { mode: ['remote_ws'] } },
      { name: 'config_ref', label: 'runtime.field.configRef', kind: 'text', placeholder: 'runtime.placeholder.configRef', help: 'runtime.help.configRef' },
      { name: 'extra_args', label: 'runtime.field.extraArgs', kind: 'args', placeholder: 'runtime.placeholder.extraArgs', help: 'runtime.help.codexExtraArgs', visibleWhen: { mode: ['local'] } }
    ]
  },
  {
    kind: 'claude',
    label: 'Claude Code',
    fields: [
      {
        name: 'mode',
        label: 'runtime.field.mode',
        kind: 'select',
        help: 'runtime.help.claudeMode',
        options: [
          { value: 'local', label: 'runtime.option.claude.local' },
          { value: 'agent_sdk', label: 'runtime.option.claude.agentSdk' },
          { value: 'remote_ssh', label: 'runtime.option.claude.remoteSsh' }
        ]
      },
      { name: 'executable', label: 'runtime.field.executable', kind: 'text', placeholder: 'runtime.placeholder.claudeExecutable', help: 'runtime.help.claudeExecutable', visibleWhen: { mode: ['local'] } },
      {
        name: 'permission_mode',
        label: 'runtime.field.permissionMode',
        kind: 'select',
        help: 'runtime.help.claudePermissionMode',
        visibleWhen: { mode: ['agent_sdk'] },
        options: [
          { value: 'default', label: 'runtime.option.claude.permissionDefault' },
          { value: 'acceptEdits', label: 'runtime.option.claude.permissionAcceptEdits' },
          { value: 'bypassPermissions', label: 'runtime.option.claude.permissionBypass' }
        ]
      },
      { name: 'approval_timeout_ms', label: 'runtime.field.approvalTimeoutMs', kind: 'text', placeholder: 'runtime.placeholder.approvalTimeoutMs', help: 'runtime.help.claudeApprovalTimeoutMs', visibleWhen: { mode: ['agent_sdk'] } },
      { name: 'ssh_executable', label: 'runtime.field.sshExecutable', kind: 'text', placeholder: 'runtime.placeholder.sshExecutable', help: 'runtime.help.sshExecutable', visibleWhen: { mode: ['remote_ssh'] } },
      { name: 'ssh_host', label: 'runtime.field.sshHost', kind: 'text', required: true, placeholder: 'runtime.placeholder.sshHost', help: 'runtime.help.sshHost', visibleWhen: { mode: ['remote_ssh'] } },
      { name: 'ssh_port', label: 'runtime.field.sshPort', kind: 'text', placeholder: 'runtime.placeholder.sshPort', help: 'runtime.help.sshPort', visibleWhen: { mode: ['remote_ssh'] } },
      { name: 'ssh_user', label: 'runtime.field.sshUser', kind: 'text', placeholder: 'runtime.placeholder.sshUser', help: 'runtime.help.sshUser', visibleWhen: { mode: ['remote_ssh'] } },
      { name: 'ssh_key_path', label: 'runtime.field.sshKeyPath', kind: 'text', placeholder: 'runtime.placeholder.sshKeyPath', help: 'runtime.help.sshKeyPath', visibleWhen: { mode: ['remote_ssh'] } },
      { name: 'remote_cwd', label: 'runtime.field.remoteCwd', kind: 'text', placeholder: 'runtime.placeholder.remoteCwd', help: 'runtime.help.remoteCwd', visibleWhen: { mode: ['remote_ssh'] } },
      { name: 'remote_executable', label: 'runtime.field.remoteExecutable', kind: 'text', placeholder: 'runtime.placeholder.claudeRemoteExecutable', help: 'runtime.help.claudeRemoteExecutable', visibleWhen: { mode: ['remote_ssh'] } },
      { name: 'config_ref', label: 'runtime.field.configRef', kind: 'text', placeholder: 'runtime.placeholder.configRef', help: 'runtime.help.configRef' },
      { name: 'extra_args', label: 'runtime.field.extraArgs', kind: 'args', placeholder: 'runtime.placeholder.extraArgs', help: 'runtime.help.extraArgs', visibleWhen: { mode: ['local', 'remote_ssh'] } }
    ]
  },
  {
    kind: 'pi',
    label: 'PI',
    fields: [
      {
        name: 'mode',
        label: 'runtime.field.mode',
        kind: 'select',
        help: 'runtime.help.piMode',
        options: [{ value: 'local', label: 'runtime.option.pi.local' }]
      },
      { name: 'executable', label: 'runtime.field.executable', kind: 'text', placeholder: 'runtime.placeholder.piExecutable', help: 'runtime.help.piExecutable', visibleWhen: { mode: ['local'] } },
      {
        name: 'approval_mode',
        label: 'runtime.field.approvalMode',
        kind: 'select',
        help: 'runtime.help.piApprovalMode',
        options: [
          { value: 'on-request', label: 'runtime.option.pi.approvalOnRequest' },
          { value: 'auto', label: 'runtime.option.pi.approvalAuto' },
          { value: 'never', label: 'runtime.option.pi.approvalNever' }
        ]
      },
      { name: 'approval_timeout_ms', label: 'runtime.field.approvalTimeoutMs', kind: 'text', placeholder: 'runtime.placeholder.approvalTimeoutMs', help: 'runtime.help.piApprovalTimeoutMs' },
      { name: 'prompt_ack_timeout_ms', label: 'runtime.field.promptAckTimeoutMs', kind: 'text', placeholder: 'runtime.placeholder.promptAckTimeoutMs', help: 'runtime.help.piPromptAckTimeoutMs' },
      { name: 'config_ref', label: 'runtime.field.configRef', kind: 'text', placeholder: 'runtime.placeholder.configRef', help: 'runtime.help.configRef' },
      { name: 'extra_args', label: 'runtime.field.extraArgs', kind: 'args', placeholder: 'runtime.placeholder.extraArgs', help: 'runtime.help.piExtraArgs', visibleWhen: { mode: ['local'] } }
    ]
  },
  {
    kind: 'acp',
    label: 'ACP',
    fields: [
      { name: 'executable', label: 'runtime.field.executable', kind: 'text', required: true, placeholder: 'runtime.placeholder.executable', help: 'runtime.help.executable' },
      { name: 'config_ref', label: 'runtime.field.configRef', kind: 'text', placeholder: 'runtime.placeholder.configRef', help: 'runtime.help.configRef' },
      { name: 'extra_args', label: 'runtime.field.extraArgs', kind: 'args', placeholder: 'runtime.placeholder.extraArgs', help: 'runtime.help.extraArgs' }
    ]
  },
  {
    kind: 'openclaw',
    label: 'OpenClaw',
    fields: [
      {
        name: 'mode',
        label: 'runtime.field.mode',
        kind: 'select',
        help: 'runtime.help.openclawMode',
        options: [
          { value: 'local', label: 'runtime.option.openclaw.local' },
          { value: 'remote_ws', label: 'runtime.option.openclaw.remoteWs' },
          { value: 'remote_http', label: 'runtime.option.openclaw.remoteHttp' }
        ]
      },
      { name: 'executable', label: 'runtime.field.executable', kind: 'text', placeholder: 'runtime.placeholder.openclawExecutable', help: 'runtime.help.openclawExecutable', visibleWhen: { mode: ['local'] } },
      { name: 'gateway_url', label: 'runtime.field.gatewayUrl', kind: 'text', placeholder: 'runtime.placeholder.openclawGatewayUrl', help: 'runtime.help.openclawGatewayUrl', visibleWhen: { mode: ['remote_ws', 'remote_http'] } },
      { name: 'token', label: 'runtime.field.token', kind: 'text', placeholder: 'runtime.placeholder.openclawToken', help: 'runtime.help.openclawToken', visibleWhen: { mode: ['remote_ws', 'remote_http'] } },
      { name: 'token_env', label: 'runtime.field.tokenEnv', kind: 'text', placeholder: 'runtime.placeholder.openclawTokenEnv', help: 'runtime.help.openclawTokenEnv', visibleWhen: { mode: ['remote_ws', 'remote_http'] } },
      { name: 'tls_fingerprint', label: 'runtime.field.tlsFingerprint', kind: 'text', placeholder: 'runtime.placeholder.openclawTlsFingerprint', help: 'runtime.help.openclawTlsFingerprint', visibleWhen: { mode: ['remote_ws'] } },
      {
        name: 'http_api',
        label: 'runtime.field.httpApi',
        kind: 'select',
        help: 'runtime.help.openclawHttpApi',
        visibleWhen: { mode: ['remote_http'] },
        options: [
          { value: 'responses', label: 'runtime.option.openclaw.httpResponses' },
          { value: 'chat_completions', label: 'runtime.option.openclaw.httpChatCompletions' }
        ]
      },
      { name: 'agent_id', label: 'runtime.field.agentId', kind: 'text', required: true, placeholder: 'runtime.placeholder.openclawAgentId', help: 'runtime.help.openclawAgentId', visibleWhen: { mode: ['remote_ws', 'remote_http'] } },
      { name: 'model_id', label: 'runtime.field.modelId', kind: 'text', placeholder: 'runtime.placeholder.openclawModelId', help: 'runtime.help.openclawModelId', visibleWhen: { mode: ['local', 'remote_http'] } },
      { name: 'config_ref', label: 'runtime.field.configRef', kind: 'text', placeholder: 'runtime.placeholder.configRef', help: 'runtime.help.configRef' },
      { name: 'extra_args', label: 'runtime.field.extraArgs', kind: 'args', placeholder: 'runtime.placeholder.extraArgs', help: 'runtime.help.openclawExtraArgs', visibleWhen: { mode: ['local'] } }
    ]
  },
  {
    kind: 'hermes',
    label: 'Hermes',
    fields: [
      { name: 'executable', label: 'runtime.field.executable', kind: 'text', placeholder: 'runtime.placeholder.executable', help: 'runtime.help.executable' },
      { name: 'model_id', label: 'runtime.field.modelId', kind: 'text', placeholder: 'runtime.placeholder.modelId', help: 'runtime.help.modelId' },
      { name: 'config_ref', label: 'runtime.field.configRef', kind: 'text', placeholder: 'runtime.placeholder.configRef', help: 'runtime.help.configRef' },
      { name: 'extra_args', label: 'runtime.field.extraArgs', kind: 'args', placeholder: 'runtime.placeholder.extraArgs', help: 'runtime.help.extraArgs' }
    ]
  },
  {
    kind: 'cursor',
    label: 'Cursor',
    fields: [
      { name: 'executable', label: 'runtime.field.executable', kind: 'text', placeholder: 'runtime.placeholder.executable', help: 'runtime.help.executable' },
      { name: 'config_ref', label: 'runtime.field.configRef', kind: 'text', placeholder: 'runtime.placeholder.configRef', help: 'runtime.help.configRef' },
      { name: 'extra_args', label: 'runtime.field.extraArgs', kind: 'args', placeholder: 'runtime.placeholder.extraArgs', help: 'runtime.help.extraArgs' }
    ]
  },
  {
    kind: 'gemini',
    label: 'Gemini',
    fields: [
      { name: 'executable', label: 'runtime.field.executable', kind: 'text', placeholder: 'runtime.placeholder.executable', help: 'runtime.help.executable' },
      { name: 'config_ref', label: 'runtime.field.configRef', kind: 'text', placeholder: 'runtime.placeholder.configRef', help: 'runtime.help.configRef' },
      { name: 'extra_args', label: 'runtime.field.extraArgs', kind: 'args', placeholder: 'runtime.placeholder.extraArgs', help: 'runtime.help.extraArgs' }
    ]
  }
]

export function buildRuntimeRegistrationConfig(
  kind: RuntimeProviderKind,
  values: Record<string, string>
) {
  const runtimeConfig: Record<string, RuntimeConfigValue> = {}
  const executable = values.executable?.trim()
  const mode = values.mode?.trim()
  const remoteAddr = values.remote_addr?.trim()
  const gatewayUrl = values.gateway_url?.trim()
  const token = values.token?.trim()
  const tokenEnv = values.token_env?.trim()
  const tlsFingerprint = values.tls_fingerprint?.trim()
  const httpApi = values.http_api?.trim()
  const agentId = values.agent_id?.trim()
  const modelId = values.model_id?.trim()
  const sshExecutable = values.ssh_executable?.trim()
  const sshHost = values.ssh_host?.trim()
  const sshPort = values.ssh_port?.trim()
  const sshUser = values.ssh_user?.trim()
  const sshKeyPath = values.ssh_key_path?.trim()
  const remoteCwd = values.remote_cwd?.trim()
  const remoteExecutable = values.remote_executable?.trim()
  const permissionMode = values.permission_mode?.trim()
  const approvalMode = values.approval_mode?.trim()
  const approvalTimeoutMs = values.approval_timeout_ms?.trim()
  const promptAckTimeoutMs = values.prompt_ack_timeout_ms?.trim()
  const extraArgs = splitArgs(values.extra_args ?? '')

  if (kind === 'managed') {
    // AgentCore executes in the server. It has no shell/argv/credential fields.
  } else if (kind === 'codex') {
    const codexMode = mode || 'local'
    runtimeConfig.mode = codexMode
    if (codexMode === 'local' && executable) runtimeConfig.executable = executable
    if (codexMode === 'remote_ws' && remoteAddr) runtimeConfig.remote_addr = remoteAddr
    if (codexMode === 'remote_ws' && token) runtimeConfig.token = token
    if (codexMode === 'remote_ws' && tokenEnv) runtimeConfig.token_env = tokenEnv
    if (codexMode === 'remote_ws' && tlsFingerprint) runtimeConfig.tls_fingerprint = tlsFingerprint
    if (codexMode === 'remote_ws' && remoteCwd) runtimeConfig.remote_cwd = remoteCwd
    if (codexMode === 'local' && extraArgs.length > 0) runtimeConfig.extra_args = extraArgs
  } else if (kind === 'claude') {
    const claudeMode = mode || 'local'
    runtimeConfig.mode = claudeMode
    if (claudeMode === 'local' && executable) runtimeConfig.executable = executable
    if (claudeMode === 'agent_sdk') {
      runtimeConfig.permission_mode = permissionMode || 'default'
      runtimeConfig.approval_timeout_ms = approvalTimeoutMs || claudeAgentSDKDefaultApprovalTimeoutMs
    }
    if (claudeMode === 'remote_ssh' && sshExecutable) runtimeConfig.ssh_executable = sshExecutable
    if (claudeMode === 'remote_ssh' && sshHost) runtimeConfig.ssh_host = sshHost
    if (claudeMode === 'remote_ssh' && sshPort) runtimeConfig.ssh_port = sshPort
    if (claudeMode === 'remote_ssh' && sshUser) runtimeConfig.ssh_user = sshUser
    if (claudeMode === 'remote_ssh' && sshKeyPath) runtimeConfig.ssh_key_path = sshKeyPath
    if (claudeMode === 'remote_ssh' && remoteCwd) runtimeConfig.remote_cwd = remoteCwd
    if (claudeMode === 'remote_ssh' && remoteExecutable) runtimeConfig.remote_executable = remoteExecutable
    if (claudeMode !== 'agent_sdk' && extraArgs.length > 0) runtimeConfig.extra_args = extraArgs
  } else if (kind === 'pi') {
    const piMode = mode || 'local'
    runtimeConfig.mode = piMode
    if (piMode === 'local' && executable) runtimeConfig.executable = executable
    runtimeConfig.approval_mode = approvalMode || piDefaultApprovalMode
    runtimeConfig.approval_timeout_ms = approvalTimeoutMs || piDefaultApprovalTimeoutMs
    runtimeConfig.prompt_ack_timeout_ms = promptAckTimeoutMs || piDefaultPromptAckTimeoutMs
    if (piMode === 'local' && extraArgs.length > 0) runtimeConfig.extra_args = extraArgs
  } else if (kind === 'openclaw') {
    const openClawMode = mode || 'local'
    runtimeConfig.mode = openClawMode
    if (openClawMode === 'local' && executable) runtimeConfig.executable = executable
    if ((openClawMode === 'remote_ws' || openClawMode === 'remote_http') && gatewayUrl) runtimeConfig.gateway_url = gatewayUrl
    if ((openClawMode === 'remote_ws' || openClawMode === 'remote_http') && token) runtimeConfig.token = token
    if ((openClawMode === 'remote_ws' || openClawMode === 'remote_http') && tokenEnv) runtimeConfig.token_env = tokenEnv
    if (openClawMode === 'remote_ws' && tlsFingerprint) runtimeConfig.tls_fingerprint = tlsFingerprint
    if (openClawMode === 'remote_http' && httpApi) runtimeConfig.http_api = httpApi
    if ((openClawMode === 'remote_ws' || openClawMode === 'remote_http') && agentId) runtimeConfig.agent_id = agentId
    if ((openClawMode === 'local' || openClawMode === 'remote_http') && modelId) runtimeConfig.model_id = modelId
    if (openClawMode === 'local' && extraArgs.length > 0) runtimeConfig.extra_args = extraArgs
  } else {
    if (mode) runtimeConfig.mode = mode
    if (executable) runtimeConfig.executable = executable
    if (gatewayUrl) runtimeConfig.gateway_url = gatewayUrl
    if (token) runtimeConfig.token = token
    if (tokenEnv) runtimeConfig.token_env = tokenEnv
    if (tlsFingerprint) runtimeConfig.tls_fingerprint = tlsFingerprint
    if (httpApi) runtimeConfig.http_api = httpApi
    if (agentId) runtimeConfig.agent_id = agentId
    if (modelId) runtimeConfig.model_id = modelId
    if (extraArgs.length > 0) runtimeConfig.extra_args = extraArgs
  }

  return {
    runtimeKind: kind,
    runtimeConfigRef: values.config_ref?.trim() ?? '',
    runtimeConfig
  }
}

export function applyRuntimeDiscoveryCandidate(candidate: RuntimeDiscoveryCandidateViewModel) {
  return {
    runtimeKind: candidate.kind as RuntimeProviderKind,
    runtimeConfigRef: candidate.configRef?.trim() ?? '',
    runtimeConfig: { ...candidate.runtimeConfig }
  }
}

function splitArgs(raw: string): string[] {
  const args: string[] = []
  let current = ''
  let quote = ''
  let escaped = false
  const emit = () => {
    if (current.length > 0) {
      args.push(current)
      current = ''
    }
  }
  for (const char of raw.trim()) {
    if (escaped) {
      current += char
      escaped = false
      continue
    }
    if (char === '\\') {
      escaped = true
      continue
    }
    if (quote) {
      if (char === quote) {
        quote = ''
      } else {
        current += char
      }
      continue
    }
    if (char === '"' || char === "'") {
      quote = char
      continue
    }
    if (/\s/.test(char)) {
      emit()
      continue
    }
    current += char
  }
  if (escaped) current += '\\'
  emit()
  return args
}
