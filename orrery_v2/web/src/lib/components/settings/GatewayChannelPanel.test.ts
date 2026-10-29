import { fireEvent, render, screen, waitFor } from '@testing-library/svelte'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { setLocale } from '#lib/i18n'
import { gatewaySettingsApi } from '#lib/api/gateway-settings'
import { ApiClientError } from '#lib/api/client'
import type { FeishuRegistrationDTO } from '#lib/types/api'
import GatewayChannelPanel from './GatewayChannelPanel.svelte'
import { gatewayChannelFieldGroups } from './settingsConfig'

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

const testFeishuAccounts = {
  main: {
    enabled: true,
    app_id: 'main-app',
    app_secret: '********',
    default_session_label: 'general',
    connection_mode: 'stream',
    domain_name: 'feishu'
  }
}

describe('GatewayChannelPanel', () => {
  beforeEach(() => {
    vi.useRealTimers()
    vi.clearAllMocks()
  })

  it('keeps Weixin account login out of administrator settings', () => {
    setLocale('zh-CN')
    render(GatewayChannelPanel, {
      open: true,
      mode: 'edit',
      selectedChannelKey: '',
      editingChannelKey: 'weixin',
      availableChannels: [],
      group: gatewayChannelFieldGroups.weixin!,
      channelKey: 'weixin',
      enabled: true,
      fieldValues: { token: '', apiKey: '', webhookUrl: '' },
      extraFields: []
    })

    expect(screen.getByText(/管理员只负责启用微信 Gateway/)).toBeTruthy()
    expect(screen.queryByRole('button', { name: /扫码|QR/i })).toBeNull()
    expect(screen.queryByText('Weixin/iLink Bot Token')).toBeNull()
  })

  it('starts and cancels the administrator Feishu QR registration', async () => {
    setLocale('zh-CN')
    vi.mocked(gatewaySettingsApi.startFeishuRegistration).mockResolvedValue({
      registration_id: 'freg-one',
      account_id: 'main',
      status: 'pending',
      verification_url: 'https://accounts.feishu.cn/activate?ticket=one',
      expires_at: '2099-07-15T12:00:00Z'
    })
    vi.mocked(gatewaySettingsApi.cancelFeishuRegistration).mockResolvedValue({
      registration_id: 'freg-one',
      account_id: 'main',
      status: 'cancelled',
      expires_at: '2099-07-15T12:00:00Z'
    })

    render(GatewayChannelPanel, {
      open: true,
      mode: 'edit',
      selectedChannelKey: '',
      editingChannelKey: 'feishu',
      availableChannels: [],
      group: gatewayChannelFieldGroups.feishu!,
      channelKey: 'feishu',
      feishuAccounts: testFeishuAccounts,
      enabled: true,
      fieldValues: { token: '', apiKey: '', webhookUrl: '' },
      extraFields: []
    })

    await fireEvent.click(screen.getByRole('button', { name: /开始扫码/ }))
    await waitFor(() => expect(screen.getByRole('img', { name: '飞书应用注册二维码' })).toBeTruthy())
    expect(screen.getByRole('link', { name: '在浏览器中打开' }).getAttribute('href')).toContain('accounts.feishu.cn')

    await fireEvent.click(screen.getByRole('button', { name: '取消' }))
    await waitFor(() => expect(gatewaySettingsApi.cancelFeishuRegistration).toHaveBeenCalledWith('freg-one'))
  })

  it('shows immediate progress while Feishu is preparing the QR session', async () => {
    setLocale('zh-CN')
    vi.mocked(gatewaySettingsApi.startFeishuRegistration).mockImplementation(() => new Promise(() => {}))

    render(GatewayChannelPanel, {
      open: true,
      mode: 'edit',
      selectedChannelKey: '',
      editingChannelKey: 'feishu',
      availableChannels: [],
      group: gatewayChannelFieldGroups.feishu!,
      channelKey: 'feishu',
      feishuAccounts: testFeishuAccounts,
      enabled: true,
      fieldValues: { token: '', apiKey: '', webhookUrl: '' },
      extraFields: []
    })

    await fireEvent.click(screen.getByRole('button', { name: /开始扫码/ }))

    expect(screen.getByRole('status').textContent).toContain('正在创建飞书注册会话')
    expect(screen.getByRole('button', { name: '开始扫码 main' }).hasAttribute('disabled')).toBe(true)
  })

  it('explains the production Feishu and DingTalk setup paths', async () => {
    setLocale('zh-CN')
    const view = render(GatewayChannelPanel, {
      open: true,
      mode: 'edit',
      selectedChannelKey: '',
      editingChannelKey: 'feishu',
      availableChannels: [],
      group: gatewayChannelFieldGroups.feishu!,
      channelKey: 'feishu',
      feishuAccounts: testFeishuAccounts,
      enabled: true,
      fieldValues: { token: '', apiKey: '', webhookUrl: '' },
      extraFields: []
    })

    expect(screen.getByText(/不需要公网回调地址/)).toBeTruthy()
    expect(screen.getByText(/im\.message\.receive_v1/)).toBeTruthy()
    expect(screen.getByRole('link', { name: /打开飞书开放平台/ }).getAttribute('href')).toBe('https://open.feishu.cn/app')
    expect(screen.queryByText('Connection Mode')).toBeNull()
    expect(screen.queryByText('Encrypt Key')).toBeNull()

    await view.rerender({
      editingChannelKey: 'dingtalk',
      group: gatewayChannelFieldGroups.dingtalk!,
      channelKey: 'dingtalk'
    })

    expect(screen.getByText(/钉钉 Stream 模式/)).toBeTruthy()
    expect(screen.getByRole('link', { name: /打开钉钉开放平台/ }).getAttribute('href')).toBe('https://open-dev.dingtalk.com/fe/app')
    expect(screen.queryByText('Agent ID')).toBeNull()
    expect(screen.getByText(/留空时 Orrery 使用 Client ID/)).toBeTruthy()
  })

  it('shows a terminal error returned directly by Feishu registration startup', async () => {
    setLocale('zh-CN')
    vi.mocked(gatewaySettingsApi.startFeishuRegistration).mockResolvedValue({
      registration_id: 'freg-failed',
      account_id: 'main',
      status: 'failed',
      expires_at: '2099-07-15T12:00:00Z',
      error_code: 'registration_failed'
    })

    render(GatewayChannelPanel, {
      open: true,
      mode: 'edit',
      selectedChannelKey: '',
      editingChannelKey: 'feishu',
      availableChannels: [],
      group: gatewayChannelFieldGroups.feishu!,
      channelKey: 'feishu',
      feishuAccounts: testFeishuAccounts,
      enabled: true,
      fieldValues: { token: '', apiKey: '', webhookUrl: '' },
      extraFields: []
    })

    await fireEvent.click(screen.getByRole('button', { name: /开始扫码/ }))

    expect(await screen.findByText('飞书应用注册失败。')).toBeTruthy()
    expect(screen.queryByRole('img', { name: '飞书应用注册二维码' })).toBeNull()
  })

  it('cancels an active registration when the parent closes the panel', async () => {
    setLocale('zh-CN')
    vi.mocked(gatewaySettingsApi.startFeishuRegistration).mockResolvedValue({
      registration_id: 'freg-parent-close',
      account_id: 'main',
      status: 'pending',
      verification_url: 'https://accounts.feishu.cn/activate?ticket=parent-close',
      expires_at: '2099-07-15T12:00:00Z'
    })
    vi.mocked(gatewaySettingsApi.cancelFeishuRegistration).mockResolvedValue({
      registration_id: 'freg-parent-close',
      account_id: 'main',
      status: 'cancelled',
      expires_at: '2099-07-15T12:00:00Z'
    })

    const view = render(GatewayChannelPanel, {
      open: true,
      mode: 'edit',
      selectedChannelKey: '',
      editingChannelKey: 'feishu',
      availableChannels: [],
      group: gatewayChannelFieldGroups.feishu!,
      channelKey: 'feishu',
      feishuAccounts: testFeishuAccounts,
      enabled: true,
      fieldValues: { token: '', apiKey: '', webhookUrl: '' },
      extraFields: []
    })

    await fireEvent.click(screen.getByRole('button', { name: /开始扫码/ }))
    await screen.findByRole('img', { name: '飞书应用注册二维码' })
    await view.rerender({ open: false })

    await waitFor(() => expect(gatewaySettingsApi.cancelFeishuRegistration).toHaveBeenCalledWith('freg-parent-close'))
  })

  it('cancels a registration that starts after the panel has already closed', async () => {
    setLocale('zh-CN')
    let resolveStart: ((value: FeishuRegistrationDTO) => void) | undefined
    vi.mocked(gatewaySettingsApi.startFeishuRegistration).mockImplementation(
      () => new Promise((resolve) => { resolveStart = resolve })
    )
    vi.mocked(gatewaySettingsApi.cancelFeishuRegistration).mockResolvedValue({
      registration_id: 'freg-late',
      account_id: 'main',
      status: 'cancelled',
      expires_at: '2099-07-15T12:00:00Z'
    })

    const view = render(GatewayChannelPanel, {
      open: true,
      mode: 'edit',
      selectedChannelKey: '',
      editingChannelKey: 'feishu',
      availableChannels: [],
      group: gatewayChannelFieldGroups.feishu!,
      channelKey: 'feishu',
      feishuAccounts: testFeishuAccounts,
      enabled: true,
      fieldValues: { token: '', apiKey: '', webhookUrl: '' },
      extraFields: []
    })

    await fireEvent.click(screen.getByRole('button', { name: /开始扫码/ }))
    await view.rerender({ open: false })
    resolveStart?.({
      registration_id: 'freg-late',
      account_id: 'main',
      status: 'pending',
      verification_url: 'https://accounts.feishu.cn/activate?ticket=late',
      expires_at: '2099-07-15T12:00:00Z'
    })

    await waitFor(() => expect(gatewaySettingsApi.cancelFeishuRegistration).toHaveBeenCalledWith('freg-late'))
    expect(gatewaySettingsApi.getFeishuRegistration).not.toHaveBeenCalled()
    expect(screen.queryByRole('img', { name: '飞书应用注册二维码' })).toBeNull()
  })

  it('waits for each polling request before scheduling the next one', async () => {
    vi.useFakeTimers()
    setLocale('zh-CN')
    let resolvePoll: ((value: FeishuRegistrationDTO) => void) | undefined
    vi.mocked(gatewaySettingsApi.startFeishuRegistration).mockResolvedValue({
      registration_id: 'freg-slow',
      account_id: 'main',
      status: 'pending',
      verification_url: 'https://accounts.feishu.cn/activate?ticket=slow',
      expires_at: '2099-07-15T12:00:00Z'
    })
    vi.mocked(gatewaySettingsApi.getFeishuRegistration).mockImplementation(
      () => new Promise((resolve) => { resolvePoll = resolve })
    )

    render(GatewayChannelPanel, {
      open: true,
      mode: 'edit',
      selectedChannelKey: '',
      editingChannelKey: 'feishu',
      availableChannels: [],
      group: gatewayChannelFieldGroups.feishu!,
      channelKey: 'feishu',
      feishuAccounts: testFeishuAccounts,
      enabled: true,
      fieldValues: { token: '', apiKey: '', webhookUrl: '' },
      extraFields: []
    })

    await fireEvent.click(screen.getByRole('button', { name: /开始扫码/ }))
    await vi.advanceTimersByTimeAsync(1500)
    expect(gatewaySettingsApi.getFeishuRegistration).toHaveBeenCalledTimes(1)

    await vi.advanceTimersByTimeAsync(6000)
    expect(gatewaySettingsApi.getFeishuRegistration).toHaveBeenCalledTimes(1)

    resolvePoll?.({
      registration_id: 'freg-slow',
      account_id: 'main',
      status: 'connected',
      expires_at: '2099-07-15T12:00:00Z'
    })
    await vi.advanceTimersByTimeAsync(6000)
    expect(gatewaySettingsApi.getFeishuRegistration).toHaveBeenCalledTimes(1)
  })

  it('expires an active registration locally without polling past its deadline', async () => {
    vi.useFakeTimers({ now: new Date('2026-07-16T08:00:00Z') })
    setLocale('zh-CN')
    vi.mocked(gatewaySettingsApi.startFeishuRegistration).mockResolvedValue({
      registration_id: 'freg-expiring',
      account_id: 'main',
      status: 'pending',
      verification_url: 'https://accounts.feishu.cn/activate?ticket=expiring',
      expires_at: '2026-07-16T08:00:01Z'
    })

    render(GatewayChannelPanel, {
      open: true,
      mode: 'edit',
      selectedChannelKey: '',
      editingChannelKey: 'feishu',
      availableChannels: [],
      group: gatewayChannelFieldGroups.feishu!,
      channelKey: 'feishu',
      feishuAccounts: testFeishuAccounts,
      enabled: true,
      fieldValues: { token: '', apiKey: '', webhookUrl: '' },
      extraFields: []
    })

    await fireEvent.click(screen.getByRole('button', { name: /开始扫码/ }))
    await vi.advanceTimersByTimeAsync(1000)

    expect(screen.getByText('飞书注册会话已过期，请重新开始扫码。')).toBeTruthy()
    expect(gatewaySettingsApi.getFeishuRegistration).not.toHaveBeenCalled()
    expect(screen.getByRole('button', { name: /开始扫码/ }).hasAttribute('disabled')).toBe(false)
  })

  it('stops polling when the registration no longer exists on the server', async () => {
    vi.useFakeTimers({ now: new Date('2026-07-16T08:00:00Z') })
    setLocale('zh-CN')
    vi.mocked(gatewaySettingsApi.startFeishuRegistration).mockResolvedValue({
      registration_id: 'freg-missing',
      account_id: 'main',
      status: 'pending',
      verification_url: 'https://accounts.feishu.cn/activate?ticket=missing',
      expires_at: '2026-07-16T08:10:00Z'
    })
    vi.mocked(gatewaySettingsApi.getFeishuRegistration).mockRejectedValue(
      new ApiClientError({ status: 404, code: 'feishu_registration_not_found', message: 'registration not found' })
    )

    render(GatewayChannelPanel, {
      open: true,
      mode: 'edit',
      selectedChannelKey: '',
      editingChannelKey: 'feishu',
      availableChannels: [],
      group: gatewayChannelFieldGroups.feishu!,
      channelKey: 'feishu',
      feishuAccounts: testFeishuAccounts,
      enabled: true,
      fieldValues: { token: '', apiKey: '', webhookUrl: '' },
      extraFields: []
    })

    await fireEvent.click(screen.getByRole('button', { name: /开始扫码/ }))
    await vi.advanceTimersByTimeAsync(60000)

    expect(gatewaySettingsApi.getFeishuRegistration).toHaveBeenCalledTimes(1)
    expect(screen.getByText('registration not found')).toBeTruthy()
    expect(screen.getByRole('button', { name: /开始扫码/ }).hasAttribute('disabled')).toBe(false)
  })

  it('uses bounded backoff for transient registration polling failures', async () => {
    vi.useFakeTimers({ now: new Date('2026-07-16T08:00:00Z') })
    setLocale('zh-CN')
    vi.mocked(gatewaySettingsApi.startFeishuRegistration).mockResolvedValue({
      registration_id: 'freg-offline',
      account_id: 'main',
      status: 'pending',
      verification_url: 'https://accounts.feishu.cn/activate?ticket=offline',
      expires_at: '2026-07-16T08:10:00Z'
    })
    vi.mocked(gatewaySettingsApi.getFeishuRegistration).mockRejectedValue(new TypeError('Failed to fetch'))

    render(GatewayChannelPanel, {
      open: true,
      mode: 'edit',
      selectedChannelKey: '',
      editingChannelKey: 'feishu',
      availableChannels: [],
      group: gatewayChannelFieldGroups.feishu!,
      channelKey: 'feishu',
      feishuAccounts: testFeishuAccounts,
      enabled: true,
      fieldValues: { token: '', apiKey: '', webhookUrl: '' },
      extraFields: []
    })

    await fireEvent.click(screen.getByRole('button', { name: /开始扫码/ }))
    await vi.advanceTimersByTimeAsync(60000)

    expect(gatewaySettingsApi.getFeishuRegistration).toHaveBeenCalledTimes(4)
    expect(gatewaySettingsApi.cancelFeishuRegistration).toHaveBeenCalledWith('freg-offline')
    expect(screen.getByText('飞书应用注册失败。')).toBeTruthy()
    expect(screen.getByRole('button', { name: /开始扫码/ }).hasAttribute('disabled')).toBe(false)
  })

  it('keeps polling the existing registration when cleanup also fails', async () => {
    vi.useFakeTimers({ now: new Date('2026-07-16T08:00:00Z') })
    setLocale('zh-CN')
    vi.mocked(gatewaySettingsApi.startFeishuRegistration).mockResolvedValue({
      registration_id: 'freg-still-active',
      account_id: 'main',
      status: 'pending',
      verification_url: 'https://accounts.feishu.cn/activate?ticket=still-active',
      expires_at: '2026-07-16T08:10:00Z'
    })
    vi.mocked(gatewaySettingsApi.getFeishuRegistration).mockRejectedValue(new TypeError('Failed to fetch'))
    vi.mocked(gatewaySettingsApi.cancelFeishuRegistration).mockRejectedValue(new TypeError('Failed to fetch'))

    render(GatewayChannelPanel, {
      open: true,
      mode: 'edit',
      selectedChannelKey: '',
      editingChannelKey: 'feishu',
      availableChannels: [],
      group: gatewayChannelFieldGroups.feishu!,
      channelKey: 'feishu',
      feishuAccounts: testFeishuAccounts,
      enabled: true,
      fieldValues: { token: '', apiKey: '', webhookUrl: '' },
      extraFields: []
    })

    await fireEvent.click(screen.getByRole('button', { name: /开始扫码/ }))
    await vi.advanceTimersByTimeAsync(23000)

    expect(gatewaySettingsApi.getFeishuRegistration).toHaveBeenCalledTimes(4)
    expect(gatewaySettingsApi.cancelFeishuRegistration).toHaveBeenCalledWith('freg-still-active')
    expect(screen.getByRole('button', { name: /开始扫码/ }).hasAttribute('disabled')).toBe(true)

    await vi.advanceTimersByTimeAsync(12000)
    expect(gatewaySettingsApi.getFeishuRegistration).toHaveBeenCalledTimes(5)
  })
})
