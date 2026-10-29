import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/svelte'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { applySession, clearSession } from '#lib/stores/session.svelte'
import { setLocale } from '#lib/i18n'
import { setThemeBrand, setThemeMode, theme } from '#lib/stores/theme.svelte'
import ProfilePage from './+page.svelte'

const mocks = vi.hoisted(() => ({
  getWeixin: vi.fn(),
  setWeixinDefaultAgent: vi.fn(),
  setWeixinTarget: vi.fn(),
  unbindWeixin: vi.fn(),
  startWeixinLogin: vi.fn(),
  getWeixinLoginStatus: vi.fn(),
  submitWeixinVerifyCode: vi.fn(),
  getGatewayBindings: vi.fn(),
  createGatewayBindingCode: vi.fn(),
  deleteGatewayBinding: vi.fn(),
  getFeishuIdentity: vi.fn(),
  unbindFeishuIdentity: vi.fn(),
  getPersonalAssistant: vi.fn(),
  createPersonalAssistant: vi.fn(),
  updatePersonalAssistant: vi.fn(),
  getPersonalAssistantNotifications: vi.fn(),
  updatePersonalAssistantNotifications: vi.fn(),
  uploadPersonalAssistantAvatar: vi.fn(),
  deletePersonalAssistantAvatar: vi.fn(),
  uploadAvatar: vi.fn(),
  setDefaultAvatar: vi.fn(),
  toDataURL: vi.fn()
}))

vi.mock('#lib/api/profile', () => ({
  profileApi: {
    getWeixin: mocks.getWeixin,
    setWeixinDefaultAgent: mocks.setWeixinDefaultAgent,
    setWeixinTarget: mocks.setWeixinTarget,
    unbindWeixin: mocks.unbindWeixin,
    startWeixinLogin: mocks.startWeixinLogin,
    getWeixinLoginStatus: mocks.getWeixinLoginStatus,
    submitWeixinVerifyCode: mocks.submitWeixinVerifyCode,
    getGatewayBindings: mocks.getGatewayBindings,
    createGatewayBindingCode: mocks.createGatewayBindingCode,
    deleteGatewayBinding: mocks.deleteGatewayBinding,
    getFeishuIdentity: mocks.getFeishuIdentity,
    unbindFeishuIdentity: mocks.unbindFeishuIdentity,
    getPersonalAssistant: mocks.getPersonalAssistant,
    createPersonalAssistant: mocks.createPersonalAssistant,
    updatePersonalAssistant: mocks.updatePersonalAssistant,
    getPersonalAssistantNotifications: mocks.getPersonalAssistantNotifications,
    updatePersonalAssistantNotifications: mocks.updatePersonalAssistantNotifications,
    uploadPersonalAssistantAvatar: mocks.uploadPersonalAssistantAvatar,
    deletePersonalAssistantAvatar: mocks.deletePersonalAssistantAvatar
  }
}))

vi.mock('qrcode', () => ({
  default: { toDataURL: mocks.toDataURL }
}))

vi.mock('#lib/queries/workforce', () => ({
  useWorkforceAvatarUploadMutation: () => ({
    mutate: mocks.uploadAvatar,
    isPending: false,
    variables: undefined
  }),
  useWorkforceAvatarDefaultMutation: () => ({
    mutate: mocks.setDefaultAvatar,
    isPending: false,
    variables: undefined
  })
}))

const profile = {
  tenant_id: 'tenant-a',
  human_id: 'human-alice',
  weixin_bound: false,
  agents: [
    {
      agent_id: 'agent-one',
      display_name: 'Agent One',
      enabled: true,
      default: false,
      current: false
    }
  ]
}

function renderPersonalAssistantPage() {
  return render(ProfilePage, { props: { surface: 'personal-assistant' } })
}

describe('profile page', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    localStorage.clear()
    document.documentElement.classList.remove('light', 'dark', 'brand-ocean', 'brand-forest', 'brand-purple')
    clearSession()
    applySession({
      user: {
        userId: 'identity-alice',
        username: 'alice',
        displayName: 'Alice Example',
        nickname: 'Alice',
        email: 'alice@example.test',
        agentProfileId: 'human-alice',
        role: 'member'
      },
      permissionScope: [],
      expiresAt: ''
    })
    setLocale('en-US')
    setThemeMode('light')
    setThemeBrand('default')
    mocks.getWeixin.mockResolvedValue(profile)
    mocks.getGatewayBindings.mockResolvedValue({ bindings: [], accounts: [] })
    mocks.getFeishuIdentity.mockResolvedValue({ provider: 'feishu', configured: true, bound: false })
    mocks.getPersonalAssistant.mockResolvedValue({
      assistant_enabled: true,
      agent_profile_id: 'personal-assistant-one',
      display_name: 'My Assistant',
      system_prompt: 'Only act for the current user.',
      assigned_agent_id: 'agent-one',
      hosting_mode: 'orrery_managed',
      fallback_to_managed: false,
      execution_identity: 'user',
      cloud_fallback: false,
      revision: 0,
      owned_agents: [
        {
          owned_agent_id: 'owned-openclaw',
          device_id: 'device-one',
          device_name: 'Alice Desktop',
          display_name: 'My OpenClaw',
          runtime_kind: 'openclaw',
          capabilities: ['workspace.read'],
          workspace_grants: [
            {
              grant_id: 'grant-openclaw',
              display_name: 'Product workspace',
              capabilities: ['workspace.read'],
              available: true
            }
          ],
          status: 'online',
          last_seen_at: '2026-08-06T04:00:00Z'
        }
      ]
    })
    mocks.getPersonalAssistantNotifications.mockResolvedValue({
      approval_notifications_enabled: true,
      channel: 'weixin',
      revision: 0
    })
    mocks.toDataURL.mockResolvedValue('data:image/png;base64,qr')
  })

  afterEach(() => {
    cleanup()
    clearSession()
  })

  it('shows current-account details and applies personal appearance controls', async () => {
    render(ProfilePage)

    await screen.findByRole('heading', { name: 'Account' })
    expect(screen.getByText('Alice Example')).toBeTruthy()
    expect(screen.getByText('alice@example.test')).toBeTruthy()
    expect(screen.getByText('member')).toBeTruthy()
    expect(screen.getByText('identity-alice')).toBeTruthy()
    expect(screen.getByText('human-alice')).toBeTruthy()
    expect(screen.getByRole('link', { name: 'Bind Feishu account' }).getAttribute('href')).toBe('/api/profile/identity/feishu/begin?return_to=%2Fprofile')
    expect(mocks.getPersonalAssistant).toHaveBeenCalledOnce()
    expect(await screen.findByRole('heading', { name: 'My Assistant' })).toBeTruthy()
    expect(screen.getByText('Only visible to you')).toBeTruthy()
    expect(screen.getByText('Assistant instructions')).toBeTruthy()
    expect(screen.getByText('Only act for the current user.')).toBeTruthy()
    expect(screen.getByRole('button', { name: 'Save personal assistant' })).toBeTruthy()
    expect(screen.getByRole('button', { name: 'Scan to bind' })).toBeTruthy()

    await fireEvent.click(screen.getByRole('button', { name: 'Dark' }))
    expect(theme.mode).toBe('dark')
    expect(document.documentElement.classList.contains('dark')).toBe(true)
    expect(localStorage.getItem('orrery.theme.mode')).toBe('dark')

    await fireEvent.click(screen.getByRole('button', { name: 'Ocean Cyan' }))
    expect(theme.brand).toBe('ocean')
    expect(document.documentElement.classList.contains('brand-ocean')).toBe(true)

    await fireEvent.change(screen.getByRole('combobox', { name: 'Language' }), { target: { value: 'zh-CN' } })
    await waitFor(() => expect(screen.getByRole('heading', { name: '界面偏好' })).toBeTruthy())
  })

  it('lets the current user update their avatar from personal management', async () => {
    render(ProfilePage)

    await screen.findByRole('heading', { name: 'Account' })
    expect(screen.getByRole('button', { name: 'Change avatar' })).toBeTruthy()

    const input = document.querySelector('input[type="file"]') as HTMLInputElement
    const file = new File(['avatar'], 'avatar.png', { type: 'image/png' })
    await fireEvent.change(input, { target: { files: [file] } })

    expect(mocks.uploadAvatar).toHaveBeenCalledWith(
      { agentProfileId: 'human-alice', file },
      expect.any(Object)
    )
    const callbacks = mocks.uploadAvatar.mock.calls[0][1] as { onSuccess: () => void }
    callbacks.onSuccess()
    expect(await screen.findByText('Avatar updated.')).toBeTruthy()
  })

  it('shows and removes the current Feishu SSO binding', async () => {
    mocks.getFeishuIdentity.mockResolvedValue({
      provider: 'feishu',
      configured: true,
      bound: true,
      display_name: 'Alice Feishu',
      email: 'alice@example.test'
    })
    mocks.unbindFeishuIdentity.mockResolvedValue({ provider: 'feishu', configured: true, bound: false })
    vi.spyOn(window, 'confirm').mockReturnValue(true)

    render(ProfilePage)

    expect(await screen.findByText('Alice Feishu')).toBeTruthy()
    await fireEvent.click(screen.getByRole('button', { name: 'Unbind sign-in' }))
    await waitFor(() => expect(mocks.unbindFeishuIdentity).toHaveBeenCalledOnce())
    expect(await screen.findByText('Feishu single sign-on binding removed.')).toBeTruthy()
  })

  it('lets the current user configure personal assistant hosting from personal settings', async () => {
    mocks.updatePersonalAssistant.mockResolvedValue({
      assistant_enabled: true,
      agent_profile_id: 'personal-assistant-one',
      display_name: 'My Assistant',
      system_prompt: 'Only act for the current user.',
      assigned_agent_id: 'agent-one',
      hosting_mode: 'owned_agent',
      owned_agent_id: 'owned-openclaw',
      workspace_grant_id: 'grant-openclaw',
      fallback_to_managed: false,
      execution_identity: 'user',
      cloud_fallback: false,
      revision: 1,
      owned_agents: []
    })
    render(ProfilePage)

    const openAssistantChat = await screen.findByRole('link', { name: 'Open assistant chat' })
    expect(openAssistantChat.getAttribute('href')).toBe('/discussions?assistant=1')
    expect(screen.getAllByText('My Assistant').length).toBeGreaterThan(0)
    expect(screen.getByText('personal-assistant-one')).toBeTruthy()
    expect(screen.queryByRole('link', { name: 'Configure in People' })).toBeNull()
    expect(screen.getByText('Only visible to you')).toBeTruthy()
    expect(screen.getByText('Assistant instructions')).toBeTruthy()
    expect(screen.getByText('Only act for the current user.')).toBeTruthy()
    expect(screen.getByRole('heading', { name: 'Account' })).toBeTruthy()
    await waitFor(() => expect(mocks.getGatewayBindings).toHaveBeenCalledOnce())
    expect(mocks.getFeishuIdentity).toHaveBeenCalledOnce()
    expect(screen.getByText('Current source')).toBeTruthy()
    expect(screen.getAllByText('Cloud runtime')).toHaveLength(2)

    await fireEvent.click(screen.getByRole('button', { name: 'Local runtime' }))
    await fireEvent.change(screen.getByLabelText('Local runtime environment'), { target: { value: 'owned-openclaw' } })
    await fireEvent.change(screen.getByLabelText('Local workspace grant'), { target: { value: 'grant-openclaw' } })
    await fireEvent.click(screen.getByRole('button', { name: 'Save personal assistant' }))

    await waitFor(() => expect(mocks.updatePersonalAssistant).toHaveBeenCalledWith({
      assistant_enabled: true,
      assigned_agent_id: 'agent-one',
      hosting_mode: 'owned_agent',
      owned_agent_id: 'owned-openclaw',
      workspace_grant_id: 'grant-openclaw',
      fallback_to_managed: false,
      revision: 0
    }))
    expect(await screen.findByText('Personal assistant settings saved.')).toBeTruthy()
  })

  it('creates the current user personal assistant only after an explicit click', async () => {
    mocks.getPersonalAssistant.mockResolvedValue({
      assistant_enabled: true,
      display_name: 'My Assistant',
      hosting_mode: 'orrery_managed',
      fallback_to_managed: false,
      execution_identity: 'user',
      cloud_fallback: false,
      revision: 0,
      owned_agents: []
    })
    mocks.createPersonalAssistant.mockResolvedValue({
      assistant_enabled: true,
      agent_profile_id: 'personal-assistant-one',
      display_name: 'My Assistant',
      system_prompt: 'Only act for the current user.',
      hosting_mode: 'orrery_managed',
      fallback_to_managed: false,
      execution_identity: 'user',
      cloud_fallback: false,
      revision: 1,
      owned_agents: []
    })

    renderPersonalAssistantPage()

    const createButton = await screen.findByRole('button', { name: 'Create personal assistant' })
    expect(screen.queryByRole('link', { name: 'Open assistant chat' })).toBeNull()
    expect(screen.queryByRole('button', { name: 'Scan to bind' })).toBeNull()
    await fireEvent.click(createButton)

    await waitFor(() => expect(mocks.createPersonalAssistant).toHaveBeenCalledOnce())
    expect(await screen.findByText('Personal assistant created.')).toBeTruthy()
    expect(await screen.findByRole('link', { name: 'Open assistant chat' })).toBeTruthy()
    expect(await screen.findByRole('button', { name: 'Scan to bind' })).toBeTruthy()
  })

  it('lets the current user control private Weixin approval reminders', async () => {
    mocks.getWeixin.mockResolvedValue({
      ...profile,
      weixin_bound: true,
      target_kind: 'personal_assistant'
    })
    mocks.updatePersonalAssistantNotifications.mockResolvedValue({
      approval_notifications_enabled: false,
      channel: 'weixin',
      revision: 1,
      updated_at: '2026-08-29T12:00:00Z'
    })

    renderPersonalAssistantPage()

    expect(await screen.findByText('Weixin is connected to your personal assistant')).toBeTruthy()
    const toggle = screen.getByRole('switch', { name: 'Weixin approval reminders' }) as HTMLInputElement
    expect(toggle.checked).toBe(true)
    await fireEvent.click(toggle)

    await waitFor(() => expect(mocks.updatePersonalAssistantNotifications).toHaveBeenCalledWith({
      approval_notifications_enabled: false,
      revision: 0
    }))
    expect(await screen.findByText('Weixin approval reminder settings saved.')).toBeTruthy()
    expect((screen.getByRole('switch', { name: 'Weixin approval reminders' }) as HTMLInputElement).checked).toBe(false)
  })

  it('updates the system assistant avatar on the dedicated page', async () => {
    mocks.uploadPersonalAssistantAvatar.mockResolvedValue({
      avatar_url: '/api/profile/personal-assistant/avatar?v=etag-two',
      content_type: 'image/png',
      size: 6,
      updated_at: '2026-08-08T06:00:00Z',
      etag: 'etag-two'
    })
    renderPersonalAssistantPage()

    await screen.findByRole('heading', { name: 'My Assistant' })
    const input = document.querySelector('[data-personal-assistant-avatar-input]') as HTMLInputElement
    const file = new File(['avatar'], 'assistant.png', { type: 'image/png' })
    await fireEvent.change(input, { target: { files: [file] } })

    await waitFor(() => expect(mocks.uploadPersonalAssistantAvatar).toHaveBeenCalledWith(file))
    const avatar = await screen.findByAltText('My Assistant')
    expect(avatar.getAttribute('src')).toContain('etag-two')
    expect(await screen.findByText('Personal assistant avatar updated.')).toBeTruthy()
  })

  it('starts personal assistant Weixin binding from personal management', async () => {
    mocks.getWeixin.mockResolvedValue({ ...profile, target_kind: 'personal_assistant' })
    mocks.startWeixinLogin.mockResolvedValue({
      session_key: 'session-one',
      qrcode_url: 'https://weixin.example.test/qr',
      status: 'need_verifycode',
      message: 'Enter the code shown on your phone',
      expires_at: '2026-07-10T15:00:00Z'
    })
    render(ProfilePage)

    await fireEvent.click(await screen.findByRole('button', { name: 'Scan to bind' }))
    await waitFor(() => expect(mocks.startWeixinLogin).toHaveBeenCalledWith({
      target_kind: 'personal_assistant',
      agent_id: undefined
    }))

    const status = await screen.findByText('Enter the code shown on your phone')
    expect(status.getAttribute('role')).toBe('status')
    expect(status.getAttribute('aria-live')).toBe('polite')
    expect(await screen.findByRole('img', { name: 'Weixin binding QR code' })).toBeTruthy()

    const input = screen.getByLabelText('Phone verification code')
    expect(input.getAttribute('name')).toBe('weixin-verification-code')
    expect(input.getAttribute('autocomplete')).toBe('one-time-code')
    expect(input.getAttribute('inputmode')).toBe('numeric')
  })

  it('locks the dedicated personal assistant page to its canonical Weixin target', async () => {
    mocks.getWeixin.mockResolvedValue({
      ...profile,
      target_kind: 'agent',
      current_agent_id: 'agent-one',
      default_agent_id: 'agent-one'
    })
    mocks.startWeixinLogin.mockResolvedValue({
      session_key: 'session-personal-assistant',
      qrcode_url: 'https://weixin.example.test/personal-assistant-qr',
      status: 'waiting',
      message: 'Scan with Weixin',
      expires_at: '2026-07-10T15:00:00Z'
    })

    renderPersonalAssistantPage()

    expect(await screen.findByText('My Assistant (personal-assistant-one)')).toBeTruthy()
    expect(screen.queryByRole('button', { name: 'Organization Bot' })).toBeNull()
    await fireEvent.click(screen.getByRole('button', { name: 'Scan to bind' }))
    await waitFor(() => expect(mocks.startWeixinLogin).toHaveBeenCalledWith({
      target_kind: 'personal_assistant',
      agent_id: undefined
    }))
  })

  it('does not treat a legacy assigned Agent as the personal assistant binding identity', async () => {
    mocks.getPersonalAssistant.mockResolvedValue({
      assistant_enabled: true,
      display_name: 'My Assistant',
      assigned_agent_id: 'A000001',
      hosting_mode: 'orrery_managed',
      fallback_to_managed: false,
      execution_identity: 'user',
      cloud_fallback: false,
      revision: 0,
      owned_agents: []
    })

    renderPersonalAssistantPage()

    expect(await screen.findByRole('button', { name: 'Create personal assistant' })).toBeTruthy()
    expect(screen.queryByRole('button', { name: 'Scan to bind' })).toBeNull()
    expect(screen.queryByText('A000001')).toBeNull()
    expect(mocks.startWeixinLogin).not.toHaveBeenCalled()
  })

  it('loads personal Weixin without requesting enterprise gateway bindings', async () => {
    mocks.getGatewayBindings.mockRejectedValue(new Error('Gateway bindings unavailable'))

    renderPersonalAssistantPage()

    expect(await screen.findByRole('button', { name: 'Scan to bind' })).toBeTruthy()
    expect(mocks.getGatewayBindings).not.toHaveBeenCalled()
    expect(screen.queryByRole('alert')).toBeNull()
  })

  it('shows a newly generated command when rebinding an existing gateway identity', async () => {
    mocks.getGatewayBindings.mockResolvedValue({
      bindings: [
        {
          id: 'binding-feishu',
          channel_key: 'feishu',
          account_id: 'feishu-account',
          external_user_id: 'ou-alice',
          external_tenant_id: 'tenant-feishu',
          display_name: 'Alice in Feishu'
        }
      ],
      accounts: [
        {
          channel_key: 'feishu',
          account_id: 'feishu-account',
          agent_id: 'agent-one',
          agent_display_name: 'Agent One',
          enabled: true,
          status: 'connected',
          inbound_enabled: true
        }
      ]
    })
    mocks.createGatewayBindingCode.mockResolvedValue({ command: '/bind FSH-1234' })

    render(ProfilePage)

    await fireEvent.click(await screen.findByRole('button', { name: 'Bind again feishu-account' }))

    expect(await screen.findByText('/bind FSH-1234')).toBeTruthy()
    expect(mocks.createGatewayBindingCode).toHaveBeenCalledWith('feishu', 'feishu-account', 'agent-one')
  })

  it('creates a Feishu code for the selected assigned account and its owner', async () => {
    mocks.getGatewayBindings.mockResolvedValue({
      bindings: [],
      accounts: [
        {
          channel_key: 'feishu',
          account_id: 'main',
          agent_id: 'agent-one',
          agent_display_name: 'Agent One',
          enabled: true,
          status: 'connected',
          inbound_enabled: false
        }
      ]
    })
    mocks.createGatewayBindingCode.mockResolvedValue({ command: '/bind FSH-5678' })

    render(ProfilePage)

    await fireEvent.click(await screen.findByRole('button', { name: 'Generate code main' }))

    expect(mocks.createGatewayBindingCode).toHaveBeenCalledWith('feishu', 'main', 'agent-one')
    expect(await screen.findByText('/bind FSH-5678')).toBeTruthy()
  })
})
