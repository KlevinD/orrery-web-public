import { ApiClientError } from '#lib/api/client'

const terminalCommandCodes = new Set([
  'dependency_unavailable',
  'forbidden',
  'invalid_request',
  'state_conflict',
  'unavailable'
])

export function shouldRotateProjectionCommandKey(error: unknown) {
  return error instanceof ApiClientError && terminalCommandCodes.has(error.code)
}
