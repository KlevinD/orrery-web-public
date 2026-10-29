import { describe, expect, it } from 'vitest'
import type { KratosLoginFlowDTO } from '#lib/api/kratos'
import { hiddenFields, loginFlowMessageText, messageTone, renderableInputs, submitNodes, visibleFields } from './loginFlow'

export const kratosFlowFixture: KratosLoginFlowDTO = {
  id: '550dd231-4312-4a22-9dcf-4c0d43c6f608',
  expires_at: '2026-07-17T13:36:09Z',
  action: 'http://127.0.0.1:4433/self-service/login?flow=550dd231-4312-4a22-9dcf-4c0d43c6f608',
  method: 'POST',
  messages: [{ id: 1, text: 'Invalid credentials', type: 'error' }],
  nodes: [
    node('default', 'csrf_token', 'hidden', 'csrf-value', true, '', ''),
    node('default', 'identifier', 'text', '', true, 'username', 'Username'),
    node('password', 'password', 'password', undefined, true, 'current-password', 'Password'),
    node('totp', 'totp_code', 'text', '', true, 'one-time-code', 'Authentication code'),
    node('password', 'method', 'submit', 'password', false, '', 'Sign in'),
    node('default', 'ignored', 'script', 'javascript:alert(1)', false, '', 'Ignored')
  ]
}

describe('Kratos login flow model', () => {
  it('separates supported hidden, visible, and submit inputs', () => {
    expect(renderableInputs(kratosFlowFixture).map((node) => node.attributes.name)).toEqual([
      'csrf_token',
      'identifier',
      'password',
      'totp_code',
      'method'
    ])
    expect(hiddenFields(kratosFlowFixture).map((node) => node.attributes.name)).toEqual(['csrf_token'])
    expect(visibleFields(kratosFlowFixture).map((node) => node.attributes.type)).toEqual(['text', 'password', 'text'])
    expect(submitNodes(kratosFlowFixture).map((node) => node.attributes.value)).toEqual(['password'])
  })

  it('maps only known message tones', () => {
    expect(messageTone('error')).toBe('error')
    expect(messageTone('warning')).toBe('warning')
    expect(messageTone('success')).toBe('info')
    expect(messageTone('')).toBe('info')
  })

  it('translates identity-provider errors without exposing account existence', () => {
    expect(loginFlowMessageText({ id: 1, text: 'The provided credentials are invalid', type: 'error' })).toBe('用户名或密码不正确。')
    expect(loginFlowMessageText({ id: 2, text: 'CSRF token is invalid', type: 'error' })).toBe('登录失败，请重试。')
    expect(loginFlowMessageText({ id: 3, text: 'Maintenance starts soon', type: 'info' })).toBe('Maintenance starts soon')
  })
})

function node(
  group: string,
  name: string,
  type: string,
  value: string | boolean | number | undefined,
  required: boolean,
  autocomplete: string,
  label: string
) {
  return {
    type: 'input',
    group,
    attributes: { name, type, value, required, disabled: false, autocomplete },
    messages: [],
    label: { text: label }
  }
}
