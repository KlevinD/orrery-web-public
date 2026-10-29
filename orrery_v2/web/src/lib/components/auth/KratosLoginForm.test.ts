import { fireEvent, render, screen } from '@testing-library/svelte'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import KratosLoginForm from './KratosLoginForm.svelte'
import { authApi } from '#lib/api/auth'
import type { KratosLoginFlowDTO } from '#lib/api/kratos'

const mocks = vi.hoisted(() => ({
  toDataURL: vi.fn()
}))

vi.mock('qrcode', () => ({
  default: { toDataURL: mocks.toDataURL }
}))

const kratosFlowFixture: KratosLoginFlowDTO = {
  id: '550dd231-4312-4a22-9dcf-4c0d43c6f608',
  action: 'http://127.0.0.1:4433/self-service/login?flow=550dd231-4312-4a22-9dcf-4c0d43c6f608',
  method: 'POST',
  messages: [{ id: 1, text: 'Invalid credentials', type: 'error' }],
  nodes: [
    flowNode('default', 'csrf_token', 'hidden', 'csrf-value', true, '', ''),
    flowNode('default', 'identifier', 'text', '', true, 'username', 'Username'),
    flowNode('password', 'password', 'password', undefined, true, 'current-password', 'Password', [
      { id: 2, text: 'Password is incorrect', type: 'error' }
    ]),
    flowNode('totp', 'totp_code', 'text', '', true, 'one-time-code', 'Authentication code'),
    flowNode('password', 'method', 'submit', 'password', false, '', 'Sign in'),
    flowNode('oidc', 'provider', 'submit', 'feishu', false, '', '飞书登录'),
    flowNode('default', 'ignored', 'script', 'javascript:alert(1)', false, '', 'Ignored')
  ]
}

describe('KratosLoginForm', () => {
  beforeEach(() => {
    mocks.toDataURL.mockResolvedValue('data:image/png;base64,preview-qr')
  })

  afterEach(() => {
    vi.useRealTimers()
    vi.restoreAllMocks()
  })

  it('renders the sanitized Kratos flow as a direct browser form', () => {
    const { container } = render(KratosLoginForm, { props: { flow: kratosFlowFixture } })

    const form = container.querySelector('form')
    expect(form?.getAttribute('method')).toBe('post')
    expect(form?.getAttribute('action')).toBe(kratosFlowFixture.action)
    expect(container.querySelector<HTMLInputElement>('input[name="csrf_token"]')?.value).toBe('csrf-value')
    expect(screen.queryByText('Orrery SSO')).toBeNull()
    expect(screen.queryByText('由 Kratos 统一认证')).toBeNull()
    expect(screen.getByRole('form', { name: 'Orrery SSO 登录' })).toBeTruthy()
    const username = screen.getByLabelText('用户名') as HTMLInputElement
    expect(username.parentElement?.className).toContain('h-9')
    expect(screen.getByLabelText('密码')).toBeTruthy()
    expect(screen.getByLabelText('动态验证码')).toBeTruthy()
    const login = screen.getByRole('button', { name: '登录' }) as HTMLButtonElement
    expect(login.name).toBe('method')
    expect(login.className).toContain('h-9')
    expect(screen.getByText('用户名或密码不正确。')).toBeTruthy()
    expect(screen.getByText('登录失败，请重试。')).toBeTruthy()
    expect(screen.queryByText('Invalid credentials')).toBeNull()
    expect(screen.queryByText('Password is incorrect')).toBeNull()
    expect(container.querySelector('[name="ignored"]')).toBeNull()
  })

  it('renders four compact login methods and opens the Feishu choices', async () => {
    render(KratosLoginForm, { props: { flow: kratosFlowFixture } })

    const feishu = screen.getByRole('button', { name: '飞书登录' }) as HTMLButtonElement
    expect(feishu.type).toBe('button')
    expect(feishu.className).toContain('h-9')
    expect(feishu.className).toContain('w-9')
    expect(feishu.textContent?.trim()).toBe('飞')
    expect(feishu.querySelector('img')).toBeNull()
    expect(screen.getByRole('button', { name: '微信登录' }).className).toContain('w-9')
    const dingtalk = screen.getByRole('button', { name: '钉钉登录' })
    expect(dingtalk.className).toContain('w-9')
    expect(dingtalk.textContent?.trim()).toBe('钉')
    expect(dingtalk.querySelector('img')).toBeNull()
    expect(dingtalk.querySelector('.lucide-send')).toBeNull()
    expect(screen.getByRole('button', { name: 'Orrery 扫码登录' }).className).toContain('w-9')

    await fireEvent.click(feishu)

    expect(screen.getByRole('dialog', { name: '飞书登录' })).toBeTruthy()
    expect(await screen.findByRole('img', { name: '飞书登录二维码预览' })).toBeTruthy()
    expect(screen.getByRole('button', { name: '打开飞书扫码页' })).toBeTruthy()
    const direct = screen.getByRole('button', { name: '在本机授权' }) as HTMLButtonElement
    expect(direct.name).toBe('provider')
    expect(direct.value).toBe('feishu')
    expect(direct.formNoValidate).toBe(true)
  })

  it('uses a native targeted submission before polling the Feishu popup session', async () => {
    vi.useFakeTimers()
    const close = vi.fn()
    const append = vi.fn()
    const createElement = vi.fn((tagName: string) => document.createElement(tagName))
    const popupDocument = { body: { append }, createElement } as unknown as Document
    vi.spyOn(window, 'open').mockReturnValue({ closed: false, close, document: popupDocument } as unknown as Window)
    const requestSubmit = vi.spyOn(HTMLFormElement.prototype, 'requestSubmit').mockImplementation(() => {})
    let submittedForm: HTMLFormElement | undefined
    let submittedAction = ''
    let submittedMethod = ''
    let submittedProvider = ''
    let submittedCSRF = ''
    const nativeSubmit = vi.spyOn(HTMLFormElement.prototype, 'submit').mockImplementation(function (this: HTMLFormElement) {
      submittedForm = this
      submittedAction = this.action
      submittedMethod = this.method
      submittedProvider = this.querySelector<HTMLInputElement>('input[type="hidden"][name="provider"]')?.value ?? ''
      submittedCSRF = this.querySelector<HTMLInputElement>('input[type="hidden"][name="csrf_token"]')?.value ?? ''
    })
    const currentSession = vi.spyOn(authApi, 'currentSession').mockResolvedValue({
      subject_provider: 'kratos',
      user: {
        user_id: 'user-1',
        username: 'demo',
        display_name: 'Demo User',
        agent_profile_id: ''
      },
      permission_scope: []
    })
    const onAuthenticated = vi.fn()
    render(KratosLoginForm, { props: { flow: kratosFlowFixture, onAuthenticated } })

    await fireEvent.click(screen.getByRole('button', { name: '飞书登录' }))
    await fireEvent.click(screen.getByRole('button', { name: '打开飞书扫码页' }))
    await vi.runAllTimersAsync()

    expect(nativeSubmit).toHaveBeenCalled()
    expect(requestSubmit).not.toHaveBeenCalled()
    expect(createElement).toHaveBeenCalledWith('form')
    expect(append).toHaveBeenCalledWith(submittedForm)
    expect(submittedAction).toBe(kratosFlowFixture.action)
    expect(submittedMethod).toBe('post')
    expect(submittedProvider).toBe('feishu')
    expect(submittedCSRF).toBe('csrf-value')
    expect(currentSession).toHaveBeenCalled()
    expect(close).toHaveBeenCalled()
    expect(onAuthenticated).toHaveBeenCalledWith(expect.objectContaining({
      subject_provider: 'kratos'
    }))
  })

  it('stops waiting when the Feishu popup authorization expires', async () => {
    vi.useFakeTimers()
    const close = vi.fn()
    vi.spyOn(window, 'open').mockReturnValue({ closed: false, close, document } as unknown as Window)
    vi.spyOn(HTMLFormElement.prototype, 'submit').mockImplementation(() => {})
    vi.spyOn(authApi, 'currentSession').mockResolvedValue(undefined)
    render(KratosLoginForm, { props: { flow: kratosFlowFixture } })

    await fireEvent.click(screen.getByRole('button', { name: '飞书登录' }))
    await fireEvent.click(screen.getByRole('button', { name: '打开飞书扫码页' }))
    await vi.advanceTimersByTimeAsync(5 * 60 * 1_000)

    expect(screen.getByText('扫码登录已超时，请重试。')).toBeTruthy()
    expect(close).toHaveBeenCalled()
  })

  it('toggles password visibility without submitting the form', async () => {
    render(KratosLoginForm, { props: { flow: kratosFlowFixture } })
    const password = screen.getByLabelText('密码') as HTMLInputElement
    expect(password.type).toBe('password')
    await fireEvent.click(screen.getByRole('button', { name: '显示密码' }))
    expect(password.type).toBe('text')
    await fireEvent.click(screen.getByRole('button', { name: '隐藏密码' }))
    expect(password.type).toBe('password')
  })

  it.each([
    ['微信登录', '微信登录', '微信登录二维码预览'],
    ['钉钉登录', '钉钉登录', '钉钉登录二维码预览'],
    ['Orrery 扫码登录', 'Orrery 扫码登录', 'Orrery 扫码登录二维码预览']
  ])('opens a disconnected preview for %s without submitting', async (buttonName, dialogName, qrName) => {
    const open = vi.spyOn(window, 'open')
    const submit = vi.spyOn(HTMLFormElement.prototype, 'submit')
    render(KratosLoginForm, { props: { flow: kratosFlowFixture } })

    await fireEvent.click(screen.getByRole('button', { name: buttonName }))

    expect(screen.getByRole('dialog', { name: dialogName })).toBeTruthy()
    expect(await screen.findByRole('img', { name: qrName })).toBeTruthy()
    expect(screen.getByText('暂未连接')).toBeTruthy()
    expect(screen.queryByRole('button', { name: '在本机授权' })).toBeNull()
    expect(open).not.toHaveBeenCalled()
    expect(submit).not.toHaveBeenCalled()
  })
})

function flowNode(
  group: string,
  name: string,
  type: string,
  value: string | boolean | number | undefined,
  required: boolean,
  autocomplete: string,
  label: string,
  messages: Array<{ id: number; text: string; type: string }> = []
) {
  return {
    type: 'input',
    group,
    attributes: { name, type, value, required, disabled: false, autocomplete },
    messages,
    label: { text: label }
  }
}
