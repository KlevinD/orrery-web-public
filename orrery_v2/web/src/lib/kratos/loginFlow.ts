import type { KratosLoginFlowDTO, KratosLoginFlowMessageDTO, KratosLoginFlowNodeDTO } from '#lib/api/kratos'

const supportedInputTypes = new Set(['hidden', 'text', 'email', 'password', 'number', 'tel', 'submit', 'button'])

export type LoginFlowMessageTone = 'error' | 'warning' | 'info'

export function renderableInputs(flow: KratosLoginFlowDTO) {
  return (flow.nodes ?? []).filter(
    (node) =>
      node.type === 'input' &&
      Boolean(node.attributes?.name?.trim()) &&
      supportedInputTypes.has(node.attributes?.type?.toLowerCase())
  )
}

export function hiddenFields(flow: KratosLoginFlowDTO) {
  return renderableInputs(flow).filter((node) => node.attributes.type === 'hidden')
}

export function visibleFields(flow: KratosLoginFlowDTO) {
  return renderableInputs(flow).filter((node) => !['hidden', 'submit', 'button'].includes(node.attributes.type))
}

export function submitNodes(flow: KratosLoginFlowDTO) {
  return renderableInputs(flow).filter((node) => ['submit', 'button'].includes(node.attributes.type))
}

export function messageTone(type: string): LoginFlowMessageTone {
  switch (type.trim().toLowerCase()) {
    case 'error':
      return 'error'
    case 'warning':
      return 'warning'
    default:
      return 'info'
  }
}

export function loginFlowMessageText(message: KratosLoginFlowMessageDTO) {
  if (messageTone(message.type) !== 'error') return message.text
  if (/credentials? (are|is|were) invalid|invalid credentials?/i.test(message.text)) {
    return '用户名或密码不正确。'
  }
  return '登录失败，请重试。'
}

export function flowNodeValue(node: KratosLoginFlowNodeDTO) {
  const value = node.attributes.value
  return value === undefined ? '' : String(value)
}

export function flowNodeLabel(node: KratosLoginFlowNodeDTO) {
  switch (node.attributes.name) {
    case 'identifier':
      return '用户名'
    case 'password':
      return '密码'
    case 'totp_code':
      return '动态验证码'
    case 'method':
      return '登录'
    default:
      return node.label?.text?.trim() || node.attributes.name
  }
}
