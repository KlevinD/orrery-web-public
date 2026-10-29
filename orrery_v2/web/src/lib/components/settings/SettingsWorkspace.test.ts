import { fireEvent, render, screen, waitFor, within } from '@testing-library/svelte'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { gatewaySettingsApi } from '#lib/api/gateway-settings'
import { setLocale } from '#lib/i18n'
import { applySession, clearSession } from '#lib/stores/session.svelte'
import { setThemeBrand } from '#lib/stores/theme.svelte'
import type { GatewaySettingsViewModel, SettingsViewModel } from '#lib/types/view-models'
import SettingsWorkspace from './SettingsWorkspace.svelte'
import { useGatewayAccountsQuery, useGatewaySettingsQuery } from '#lib/queries/gateway-settings'

const updateSettings = vi.fn()
const updateGatewaySettings = vi.fn()
const testPageIndexConnection = vi.fn()
const gatewaySettingsRefetch = vi.fn()
const gatewayAccountsRefetch = vi.fn()
let gatewaySettingsData: GatewaySettingsViewModel | undefined

vi.mock('qrcode', () => ({
  default: { toDataURL: vi.fn().mockResolvedValue('data:image/png;base64,feishu') }
}))

vi.mock('#lib/api/gateway-settings', () => ({
  gatewaySettingsApi: {
    startFeishuRegistration: vi.fn(),
    getFeishuRegistration: vi.fn(),
    cancelFeishuRegistration: vi.fn()
  }
}))

vi.mock('#lib/queries/settings', () => ({
  useSettingsQuery: () => ({ isLoading: false, isError: false, data: settingsFixture() }),
  useSettingsUpdateMutation: () => ({ isPending: false, mutate: updateSettings }),
  usePageIndexConnectionTestMutation: () => ({ isPending: false, mutate: testPageIndexConnection }),
  useRuntimeDiscoveryQuery: () => ({
    data: [],
    isFetching: false,
    isError: false,
    error: null,
    refetch: vi.fn()
  })
}))

vi.mock('#lib/queries/gateway-settings', () => ({
  useGatewaySettingsQuery: vi.fn((_enabled?: () => boolean) => ({ isLoading: false, isError: false, data: gatewaySettingsData, error: null, refetch: gatewaySettingsRefetch })),
  useGatewayAccountsQuery: vi.fn((_enabled?: () => boolean) => ({ data: [], refetch: gatewayAccountsRefetch })),
  useGatewaySettingsUpdateMutation: () => ({ isPending: false, mutate: updateGatewaySettings })
}))

describe('SettingsWorkspace permissions', () => {
  beforeEach(() => {
    vi.mocked(useGatewaySettingsQuery).mockClear()
    vi.mocked(useGatewayAccountsQuery).mockClear()
    updateSettings.mockReset()
    updateGatewaySettings.mockReset()
    testPageIndexConnection.mockReset()
    gatewaySettingsRefetch.mockReset()
    gatewayAccountsRefetch.mockReset()
    vi.mocked(gatewaySettingsApi.startFeishuRegistration).mockReset()
    vi.mocked(gatewaySettingsApi.getFeishuRegistration).mockReset()
    vi.mocked(gatewaySettingsApi.cancelFeishuRegistration).mockReset()
    gatewaySettingsData = undefined
    gatewaySettingsRefetch.mockResolvedValue({ data: undefined })
    gatewayAccountsRefetch.mockResolvedValue({ data: [] })
    setLocale('zh-CN')
    setThemeBrand('default')
    clearSession()
  })

  it('enables gateway queries only while the gateway tab is selected', async () => {
    render(SettingsWorkspace)
    const settingsEnabled = vi.mocked(useGatewaySettingsQuery).mock.calls[0][0]!
    const accountsEnabled = vi.mocked(useGatewayAccountsQuery).mock.calls[0][0]!
    expect(settingsEnabled()).toBe(false)
    expect(accountsEnabled()).toBe(false)
    await fireEvent.click(screen.getByRole('tab', { name: 'Gateway' }))
    expect(settingsEnabled()).toBe(true)
    expect(accountsEnabled()).toBe(true)
    await fireEvent.click(screen.getByRole('tab', { name: '外观' }))
    expect(settingsEnabled()).toBe(false)
    expect(accountsEnabled()).toBe(false)
  })

  it('renders settings as disabled controls for settings.read-only users', async () => {
    applySession({
      permissionScope: ['settings.read'],
      expiresAt: '2099-01-01T00:00:00Z',
      user: {
        userId: 'reader-1',
        username: 'reader',
        displayName: '只读用户',
        nickname: '只读用户',
        email: 'reader@example.com',
        agentProfileId: '',
        role: 'member'
      }
    })

    const view = render(SettingsWorkspace)
    const fieldset = view.container.querySelector('fieldset')
    expect(fieldset?.hasAttribute('disabled')).toBe(true)

    const controls = Array.from(fieldset?.querySelectorAll('button, input, select, textarea') ?? [])
    expect(controls.length).toBeGreaterThan(0)
    expect(controls.every((control) => control.matches(':disabled'))).toBe(true)

    await fireEvent.click(screen.getByRole('button', { name: '保存设置' }))
    expect(updateSettings).not.toHaveBeenCalled()
    expect(testPageIndexConnection).not.toHaveBeenCalled()
    expect(updateGatewaySettings).not.toHaveBeenCalled()
  })

  it('keeps personal appearance controls editable for settings.read-only users', async () => {
    applySession({
      permissionScope: ['settings.read'],
      expiresAt: '2099-01-01T00:00:00Z',
      user: {
        userId: 'reader-1',
        username: 'reader',
        displayName: '只读用户',
        nickname: '只读用户',
        email: 'reader@example.com',
        agentProfileId: '',
        role: 'member'
      }
    })

    const view = render(SettingsWorkspace)
    await fireEvent.click(screen.getByRole('tab', { name: '外观' }))

    const fieldset = view.container.querySelector('fieldset')
    expect(fieldset?.hasAttribute('disabled')).toBe(false)
    await fireEvent.click(screen.getByRole('button', { name: '海洋青' }))
    expect(document.documentElement.classList.contains('brand-ocean')).toBe(true)
    expect(updateSettings).not.toHaveBeenCalled()
  })

  it('opens the runtime approval policy editor from settings navigation', async () => {
    applyManagerSession()

    render(SettingsWorkspace)
    await fireEvent.click(screen.getByRole('tab', { name: '执行审批' }))

    expect(screen.getByRole('heading', { name: '执行与审批' })).toBeTruthy()
    expect(screen.getByText(/系统内置的发布、付款、权限/)).toBeTruthy()
  })

  it('closes the stale gateway editor as soon as Feishu registration connects', async () => {
    applyManagerSession()
    gatewaySettingsData = gatewaySettingsFixture('old-app', 'old-secret')
    gatewaySettingsRefetch.mockResolvedValue({ data: gatewaySettingsFixture('new-app', 'new-secret') })
    vi.mocked(gatewaySettingsApi.startFeishuRegistration).mockResolvedValue({
      registration_id: 'freg-connected',
      account_id: 'main',
      status: 'connected',
      expires_at: '2099-01-01T00:00:00Z'
    })

    render(SettingsWorkspace)
    await fireEvent.click(screen.getByRole('tab', { name: 'Gateway' }))
    await fireEvent.click(screen.getByRole('button', { name: /Feishu/ }))
    await fireEvent.click(within(screen.getByRole('dialog', { name: 'Gateway 通道' })).getByRole('button', { name: /开始扫码/ }))

    await waitFor(() => expect(screen.queryByRole('dialog', { name: 'Gateway 通道' })).toBeNull())
    expect(gatewaySettingsRefetch).toHaveBeenCalledTimes(1)
    expect(gatewayAccountsRefetch).toHaveBeenCalledTimes(1)
    expect(updateGatewaySettings).not.toHaveBeenCalled()
  })

  it('keeps a gateway channel visible when its removal fails', async () => {
    applyManagerSession()
    gatewaySettingsData = gatewaySettingsFixture('existing-app', 'existing-secret')
    updateGatewaySettings.mockImplementation(
      (_body: unknown, options?: { onError?: (error: Error) => void }) => options?.onError?.(new Error('save failed'))
    )

    render(SettingsWorkspace)
    await fireEvent.click(screen.getByRole('tab', { name: 'Gateway' }))
    await fireEvent.click(screen.getByRole('button', { name: /Feishu/ }))
    const dialog = screen.getByRole('dialog', { name: 'Gateway 通道' })
    await fireEvent.click(within(dialog).getByRole('button', { name: '移除' }))

    expect(updateGatewaySettings).toHaveBeenCalledTimes(1)
    expect(screen.getByRole('dialog', { name: 'Gateway 通道' })).toBeTruthy()
    await fireEvent.click(within(dialog).getByRole('button', { name: '取消' }))
    expect(screen.getByRole('button', { name: /Feishu/ })).toBeTruthy()
  })
})

function applyManagerSession() {
  applySession({
    permissionScope: ['settings.manage'],
    expiresAt: '2099-01-01T00:00:00Z',
    user: {
      userId: 'manager-1',
      username: 'manager',
      displayName: '管理员',
      nickname: '管理员',
      email: 'manager@example.com',
      agentProfileId: '',
      role: 'owner'
    }
  })
}

function gatewaySettingsFixture(appId: string, appSecret: string): GatewaySettingsViewModel {
  return {
    configPath: '/tmp/gateway.yaml',
    rawYaml: '',
    server: {
      port: '8081',
      bindAddress: '127.0.0.1',
      idleSessionTimeoutMinutes: '30',
      staticAssetsPath: '',
      maxPerSessionMcpInstances: '4'
    },
    channels: {
      feishu: {
        key: 'feishu',
        label: 'Feishu',
        enabled: true,
        token: appSecret,
        apiKey: appId,
        webhookUrl: '',
        extraText: '{}',
        extraFields: []
      }
    },
    feishuAccounts: {
      main: {
        enabled: true,
        app_id: appId,
        app_secret: appSecret,
        default_session_label: 'general',
        connection_mode: 'stream',
        domain_name: 'feishu'
      }
    }
  }
}

function settingsFixture(): SettingsViewModel {
  return {
    platformName: 'Orrery',
    organizationName: 'Minvia',
    language: 'zh-CN',
    autoCreateAgentProfile: true,
    autoLinkEvidence: true,
    hilDefaultEnabled: true,
    discussionAgentPairLoopLimit: '3',
    workspaceRoot: '',
    workspaceAllowlist: '',
    memory: { enabled: true },
    knowledge: {
      enabled: true,
      defaultProvider: 'pageindex',
      pageindex: {
        apiUrl: 'https://api.pageindex.ai',
        apiKey: '',
        apiKeyRef: 'secret://knowledge/pageindex',
        defaultFolderId: '',
        timeoutSeconds: '30'
      },
      local: { enabled: true }
    },
    runtimeProfiles: [],
    llmProfiles: [],
    promptBlocks: [],
    runtimeApprovals: {
      defaultApprovalPolicy: 'on-request', defaultApprovalsReviewer: 'human',
      defaultHILMode: 'manual', reviewer: { llmProfileId: '', timeoutMs: '30000', maxTokens: '360' },
      exec: { approvalPolicy: 'never', security: 'allowlist', allowlist: [] }, tools: { security: 'allowlist', allowlist: [] },
      forcedHuman: { toolIds: [], execExecutables: [] }, conversationDefaults: { personal_assistant: { hilMode: 'auto' } }, agentOverrides: []
    },
    notificationRules: []
  }
}
