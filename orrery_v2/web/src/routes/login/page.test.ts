import { fireEvent, render, screen, waitFor } from '@testing-library/svelte'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import LoginPage from './+page.svelte'
import { applySession, clearSession, session } from '#lib/stores/session.svelte'
import { clearApiBaseUrlForTests } from '#lib/runtime/api-base'
import { navigateToIdentityLogin, navigateToLoginReturnTo } from '#lib/routes/identityLoginNavigation'
import { identityLoginReturnToFromURL } from '#lib/routes/returnTo'

vi.mock('$app/stores', () => ({
  page: {
    subscribe(run: (value: { url: URL }) => void) {
      run({ url: new URL(window.location.href) })
      return () => undefined
    }
  }
}))

vi.mock('#lib/routes/identityLoginNavigation', () => ({
  navigateToIdentityLogin: vi.fn(),
  navigateToLoginReturnTo: vi.fn()
}))

describe('login page', () => {
  beforeEach(() => {
    vi.restoreAllMocks()
    vi.mocked(navigateToIdentityLogin).mockClear()
    vi.mocked(navigateToLoginReturnTo).mockClear()
    window.history.replaceState({}, '', '/login')
    window.sessionStorage.clear()
    clearSession()
    clearApiBaseUrlForTests()
    mockProviderList()
    vi.useRealTimers()
  })

  it('releases the form when authentication does not return', async () => {
    vi.useFakeTimers()
    vi.spyOn(globalThis, 'fetch').mockImplementation((input, init) => {
      if (String(input).endsWith('/api/auth/providers')) {
        return Promise.resolve(providerListResponse())
      }
      return new Promise<Response>((_, reject) => {
        init?.signal?.addEventListener('abort', () => reject(init.signal?.reason), { once: true })
      })
    })

    render(LoginPage)

    await fireEvent.input(await screen.findByLabelText('用户名'), { target: { value: 'superadmin' } })
    await fireEvent.input(screen.getByLabelText('密码'), { target: { value: 'wrong-password' } })
    await fireEvent.click(screen.getByRole('button', { name: '登录' }))

    expect((screen.getByRole('button', { name: '处理中' }) as HTMLButtonElement).disabled).toBe(true)

    await vi.advanceTimersByTimeAsync(10001)
    await Promise.resolve()

    expect(screen.queryByText('登录请求超时，请重试')).not.toBeNull()
    expect((screen.getByRole('button', { name: '登录' }) as HTMLButtonElement).disabled).toBe(false)
  })

  it('renders the Minvia company name', () => {
    render(LoginPage)

    expect(screen.getByText('Minvia 明微知径')).not.toBeNull()
  })

  it('does not prefill the superadmin username', async () => {
    render(LoginPage)

    expect(((await screen.findByLabelText('用户名')) as HTMLInputElement).value).toBe('')
  })

  it('lets the user reveal and hide the local password', async () => {
    render(LoginPage)

    const passwordInput = (await screen.findByLabelText('密码')) as HTMLInputElement
    expect(passwordInput.type).toBe('password')

    await fireEvent.click(screen.getByRole('button', { name: '显示密码' }))
    expect(passwordInput.type).toBe('text')

    await fireEvent.click(screen.getByRole('button', { name: '隐藏密码' }))
    expect(passwordInput.type).toBe('password')
  })

  it('hides TOTP until the server requests a second factor', async () => {
    let completeAttempts = 0
    let submittedCode = ''
    vi.restoreAllMocks()
    vi.spyOn(globalThis, 'fetch').mockImplementation((input, init) => {
      const url = String(input)
      if (url.endsWith('/api/auth/providers')) return Promise.resolve(providerListResponse())
      if (url.endsWith('/api/auth/login/begin')) {
        return Promise.resolve(jsonResponse({
          challenge_id: `challenge-${completeAttempts + 1}`,
          username: 'superadmin',
          salt: 'salt-1'
        }))
      }
      if (url.endsWith('/api/auth/login/complete')) {
        completeAttempts += 1
        if (completeAttempts === 1) {
          return Promise.resolve(new Response(JSON.stringify({ code: 'precondition_required', message: 'MFA required' }), {
            status: 428,
            headers: { 'Content-Type': 'application/json' }
          }))
        }
        const body = JSON.parse(String(init?.body)) as { mfa_code?: string }
        submittedCode = body.mfa_code ?? ''
        return Promise.resolve(jsonResponse(loginSessionResponse(false)))
      }
      return Promise.reject(new Error(`unexpected request: ${url}`))
    })

    render(LoginPage)

    expect(screen.queryByLabelText('动态验证码')).toBeNull()
    await fireEvent.input(await screen.findByLabelText('用户名'), { target: { value: 'superadmin' } })
    await fireEvent.input(screen.getByLabelText('密码'), { target: { value: 'correct-password' } })
    await fireEvent.click(screen.getByRole('button', { name: '登录' }))

    await fireEvent.input(await screen.findByLabelText('动态验证码'), { target: { value: '123456' } })
    await fireEvent.click(screen.getByRole('button', { name: '登录' }))

    await waitFor(() => expect(navigateToLoginReturnTo).toHaveBeenCalledWith('/'))
    expect(submittedCode).toBe('123456')
  })

  it('does not invent a local login provider when provider discovery fails', async () => {
    vi.restoreAllMocks()
    vi.spyOn(globalThis, 'fetch').mockRejectedValue(new Error('provider service unavailable'))

    render(LoginPage)

    expect(await screen.findByText('当前没有可用登录方式')).toBeTruthy()
    expect(screen.queryByLabelText('用户名')).toBeNull()
    expect(screen.getByText('provider service unavailable')).toBeTruthy()
  })

  it('presents a branded operations sign-in surface', () => {
    render(LoginPage)

    expect(screen.getByText('企业智能运营工作台')).toBeTruthy()
    expect(screen.getByText('受控访问')).toBeTruthy()
    expect(screen.getByText('安全登录')).toBeTruthy()
    expect(screen.getByText('Minvia Workspace')).toBeTruthy()
    expect(screen.queryByText('任务与讨论同屏推进')).toBeNull()
  })

  it('retains the restricted session while requiring a weak-password replacement', async () => {
    vi.restoreAllMocks()
    vi.spyOn(globalThis, 'fetch').mockImplementation((input) => {
      const url = String(input)
      if (url.endsWith('/api/auth/providers')) return Promise.resolve(providerListResponse())
      if (url.endsWith('/api/auth/login/begin')) {
        return Promise.resolve(jsonResponse({
          challenge_id: 'challenge-1',
          username: 'operator',
          salt: 'salt-1'
        }))
      }
      if (url.endsWith('/api/auth/login/complete')) {
        return Promise.resolve(jsonResponse(loginSessionResponse(true)))
      }
      return Promise.reject(new Error(`unexpected request: ${url}`))
    })

    render(LoginPage)

    await fireEvent.input(await screen.findByLabelText('用户名'), { target: { value: 'operator' } })
    await fireEvent.input(screen.getByLabelText('密码'), { target: { value: 'weak-password' } })
    await fireEvent.click(screen.getByRole('button', { name: '登录' }))

    expect(await screen.findByLabelText('新密码')).toBeTruthy()
    expect(session.authenticated).toBe(true)
    expect(session.mustChangePassword).toBe(true)
    expect(navigateToLoginReturnTo).not.toHaveBeenCalled()
  })

  it('keeps the forced-change form usable when provider discovery fails after reload', async () => {
    vi.restoreAllMocks()
    applySession({
      expiresAt: '',
      mustChangePassword: true,
      permissionScope: [],
      user: {
        userId: 'operator',
        username: 'operator',
        displayName: 'Operator',
        nickname: 'Operator',
        email: '',
        agentProfileId: 'human-operator',
        role: 'member'
      }
    })
    vi.spyOn(globalThis, 'fetch').mockImplementation((input) => {
      const url = String(input)
      if (url.endsWith('/api/auth/providers')) {
        return Promise.reject(new Error('provider service unavailable'))
      }
      if (url.endsWith('/api/auth/password/change')) {
        return Promise.resolve(jsonResponse(loginSessionResponse(false)))
      }
      return Promise.reject(new Error(`unexpected request: ${url}`))
    })

    render(LoginPage)

    await fireEvent.input(await screen.findByLabelText('新密码'), { target: { value: 'replacement-password-1' } })
    await fireEvent.input(screen.getByLabelText('确认新密码'), { target: { value: 'replacement-password-1' } })
    await fireEvent.click(screen.getByRole('button', { name: '更新密码并登录' }))

    await waitFor(() => expect(session.mustChangePassword).toBe(false))
    expect(navigateToLoginReturnTo).toHaveBeenCalledWith('/')
  })

  it('returns a local login to the device authorization endpoint with a full navigation', async () => {
    const returnTo = '/oauth/device/authorize?response_type=code&client_id=orrery-desktop&state=pairing-state'
    window.history.replaceState({}, '', `/login?return_to=${encodeURIComponent(returnTo)}`)
    vi.restoreAllMocks()
    vi.spyOn(globalThis, 'fetch').mockImplementation((input) => {
      const url = String(input)
      if (url.endsWith('/api/auth/providers')) return Promise.resolve(providerListResponse())
      if (url.endsWith('/api/auth/login/begin')) {
        return Promise.resolve(jsonResponse({ challenge_id: 'challenge-1', username: 'superadmin', salt: 'salt-1' }))
      }
      if (url.endsWith('/api/auth/login/complete')) return Promise.resolve(jsonResponse(loginSessionResponse(false)))
      return Promise.reject(new Error(`unexpected request: ${url}`))
    })

    render(LoginPage)

    await fireEvent.input(await screen.findByLabelText('用户名'), { target: { value: 'superadmin' } })
    await fireEvent.input(screen.getByLabelText('密码'), { target: { value: 'orrery-dev-password' } })
    await fireEvent.click(screen.getByRole('button', { name: '登录' }))

    await waitFor(() => expect(navigateToLoginReturnTo).toHaveBeenCalledWith(returnTo))
  })

  it('begins the sole primary Kratos provider when no flow exists', async () => {
    mockProviders([kratosProvider()])

    render(LoginPage)

    await waitFor(() => {
      expect(navigateToIdentityLogin).toHaveBeenCalledWith(
        '/api/auth/identity/kratos/begin?return_to=%2F'
      )
    })
    expect(screen.queryByLabelText('用户名')).toBeNull()
    expect(screen.queryByLabelText('密码')).toBeNull()
  })

  it('keeps a forced password-change URL out of the automatic Kratos login flow', async () => {
    window.history.replaceState({}, '', '/login?change_password=1&return_to=%2F')
    mockProviders([kratosProvider()])

    render(LoginPage)

    expect(await screen.findByLabelText('新密码')).toBeTruthy()
    expect(screen.getByLabelText('确认新密码')).toBeTruthy()
    await waitFor(() => expect(screen.queryByRole('link', { name: 'Kratos' })).toBeNull())
    expect(navigateToIdentityLogin).not.toHaveBeenCalled()
  })

  it('loads and renders a Kratos flow from the flow query', async () => {
    window.history.replaceState({}, '', `/login?flow=${kratosFlowFixture.id}&return_to=%2Fmissions`)
    mockKratosFlow(kratosFlowFixture)

    const { container } = render(LoginPage)

    expect(await screen.findByRole('form', { name: 'Kratos 登录' })).toBeTruthy()
    expect(screen.queryByText('由 Kratos 统一认证')).toBeNull()
    expect(screen.getByLabelText('用户名')).toBeTruthy()
    expect(screen.getByLabelText('密码')).toBeTruthy()
    expect(container.querySelector('form')?.getAttribute('action')).toBe(kratosFlowFixture.action)
    expect(navigateToIdentityLogin).not.toHaveBeenCalled()
  })

  it.each([
    ['flow_cookie_invalid', 403],
    ['flow_not_found', 404],
    ['flow_expired', 410]
  ])('restarts a Kratos flow after %s', async (code, status) => {
    identityLoginReturnToFromURL(
      new URL('http://localhost/login?return_to=%2Fmissions'),
      window.sessionStorage
    )
    window.history.replaceState({}, '', `/login?flow=${kratosFlowFixture.id}`)
    vi.restoreAllMocks()
    vi.spyOn(globalThis, 'fetch').mockImplementation((input) => {
      const url = String(input)
      if (url.endsWith('/api/auth/providers')) return Promise.resolve(providersResponse([kratosProvider()]))
      if (url.includes('/api/auth/identity/kratos/login-flow?')) {
        return Promise.resolve(new Response(JSON.stringify({ code, message: 'login flow is no longer usable' }), {
          status,
          headers: { 'Content-Type': 'application/json' }
        }))
      }
      return Promise.reject(new Error(`unexpected request: ${url}`))
    })

    render(LoginPage)

    await waitFor(() => {
      expect(navigateToIdentityLogin).toHaveBeenCalledWith(
        '/api/auth/identity/kratos/begin?return_to=%2Fmissions'
      )
    })
  })

  it('does not render or call local password login when local is absent', async () => {
    const requests: string[] = []
    vi.restoreAllMocks()
    vi.spyOn(globalThis, 'fetch').mockImplementation((input) => {
      const url = String(input)
      requests.push(url)
      if (url.endsWith('/api/auth/providers')) {
        return Promise.resolve(providersResponse([
          kratosProvider({ primary: false }),
          { name: 'backup-sso', display_name: 'Backup SSO', kind: 'browser', enabled: true, primary: false, begin_url: '/api/auth/identity/backup-sso/begin' }
        ]))
      }
      return Promise.reject(new Error(`unexpected request: ${url}`))
    })

    render(LoginPage)

    expect(await screen.findByRole('link', { name: 'Kratos' })).toBeTruthy()
    expect(screen.queryByLabelText('用户名')).toBeNull()
    expect(screen.queryByLabelText('密码')).toBeNull()
    expect(requests.some((url) => url.includes('/api/auth/login/'))).toBe(false)
  })

  it('turns a provider network failure into an actionable login error', async () => {
    vi.restoreAllMocks()
    vi.spyOn(globalThis, 'fetch').mockRejectedValue(new TypeError('Failed to fetch'))

    render(LoginPage)

    expect(await screen.findByText('无法连接登录服务，请检查网络连接后重试')).toBeTruthy()
    expect(screen.queryByText('Failed to fetch')).toBeNull()
  })

  it('keeps the local form when local login is explicitly enabled', async () => {
    mockProviders([kratosProvider({ primary: false }), localProvider()])

    render(LoginPage)

    expect(await screen.findByLabelText('用户名')).toBeTruthy()
    expect(screen.getByLabelText('密码')).toBeTruthy()
    expect(screen.getByRole('button', { name: '登录' })).toBeTruthy()
    expect(navigateToIdentityLogin).not.toHaveBeenCalled()
  })
})

function mockProviderList() {
  vi.spyOn(globalThis, 'fetch').mockImplementation(() => Promise.resolve(providerListResponse()))
}

function mockProviders(providers: Record<string, unknown>[]) {
  vi.restoreAllMocks()
  vi.spyOn(globalThis, 'fetch').mockImplementation((input) => {
    const url = String(input)
    if (url.endsWith('/api/auth/providers')) return Promise.resolve(providersResponse(providers))
    return Promise.reject(new Error(`unexpected request: ${url}`))
  })
}

function mockKratosFlow(flow: typeof kratosFlowFixture) {
  vi.restoreAllMocks()
  vi.spyOn(globalThis, 'fetch').mockImplementation((input) => {
    const url = String(input)
    if (url.endsWith('/api/auth/providers')) return Promise.resolve(providersResponse([kratosProvider()]))
    if (url.includes('/api/auth/identity/kratos/login-flow?')) return Promise.resolve(jsonResponse(flow))
    return Promise.reject(new Error(`unexpected request: ${url}`))
  })
}

function providerListResponse() {
  return providersResponse([localProvider()])
}

function providersResponse(providers: Record<string, unknown>[]) {
  return jsonResponse({ providers })
}

function localProvider() {
  return {
    name: 'local',
    display_name: '本地账号',
    kind: 'local',
    enabled: true,
    primary: true
  }
}

function kratosProvider(overrides: Record<string, unknown> = {}) {
  return {
    name: 'kratos',
    display_name: 'Kratos',
    kind: 'browser',
    enabled: true,
    primary: true,
    begin_url: '/api/auth/identity/kratos/begin',
    ...overrides
  }
}

const kratosFlowFixture = {
  id: '550dd231-4312-4a22-9dcf-4c0d43c6f608',
  action: 'http://127.0.0.1:4433/self-service/login?flow=550dd231-4312-4a22-9dcf-4c0d43c6f608',
  method: 'POST',
  messages: [],
  nodes: [
    {
      type: 'input',
      group: 'default',
      attributes: { name: 'csrf_token', type: 'hidden', value: 'csrf-value', required: true, disabled: false },
      messages: [],
      label: { text: '' }
    },
    {
      type: 'input',
      group: 'default',
      attributes: { name: 'identifier', type: 'text', value: '', required: true, disabled: false, autocomplete: 'username' },
      messages: [],
      label: { text: 'Username' }
    },
    {
      type: 'input',
      group: 'password',
      attributes: { name: 'password', type: 'password', required: true, disabled: false, autocomplete: 'current-password' },
      messages: [],
      label: { text: 'Password' }
    },
    {
      type: 'input',
      group: 'password',
      attributes: { name: 'method', type: 'submit', value: 'password', required: false, disabled: false },
      messages: [],
      label: { text: 'Sign in' }
    }
  ]
}

function loginSessionResponse(mustChangePassword: boolean) {
  return {
    user: {
      user_id: 'operator',
      username: 'operator',
      display_name: 'Operator',
      agent_profile_id: 'human-operator',
      role: 'member'
    },
    permission_scope: mustChangePassword ? [] : ['operations.read'],
    must_change_password: mustChangePassword
  }
}

function jsonResponse(value: unknown) {
  return new Response(JSON.stringify(value), {
    status: 200,
    headers: { 'Content-Type': 'application/json' }
  })
}
