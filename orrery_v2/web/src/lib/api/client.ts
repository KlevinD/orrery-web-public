import type { ErrorResponseBody } from './errors'
import { get } from 'svelte/store'
import { locale } from '#lib/i18n'
import { session } from '#lib/stores/session.svelte'
import { waitForApiBaseUrl } from '#lib/runtime/api-base'
import { createRequestId } from '#lib/runtime/ids'
import { reportClientError } from '#lib/runtime/error-reporting'

export interface ApiFetchOptions extends RequestInit {
  idempotencyKey?: string
  responseType?: 'json' | 'blob'
  suppressUnauthorizedNotification?: boolean
}

export type UnauthorizedHandler = (error: ApiClientError) => void | Promise<void>
export type ForbiddenHandler = (error: ApiClientError, path: string) => void | Promise<void>

let unauthorizedHandler: UnauthorizedHandler | undefined
let forbiddenHandler: ForbiddenHandler | undefined

export class ApiClientError extends Error {
  readonly status: number
  readonly code: string
  readonly correlationId?: string
  readonly details?: unknown

  constructor(params: { status: number; code: string; message: string; correlationId?: string; details?: unknown }) {
    super(params.message)
    this.name = 'ApiClientError'
    this.status = params.status
    this.code = params.code
    this.correlationId = params.correlationId
    this.details = params.details
  }
}

export function setUnauthorizedHandler(handler: UnauthorizedHandler | undefined) {
  unauthorizedHandler = handler
}

export function setUnauthorizedHandlerForTests(handler: UnauthorizedHandler | undefined) {
  setUnauthorizedHandler(handler)
}

export function setForbiddenHandler(handler: ForbiddenHandler | undefined) {
  forbiddenHandler = handler
}

export function setForbiddenHandlerForTests(handler: ForbiddenHandler | undefined) {
  setForbiddenHandler(handler)
}

export async function apiFetch<T>(path: string, options: ApiFetchOptions = {}): Promise<T> {
  const { idempotencyKey, responseType = 'json', suppressUnauthorizedNotification = false, ...requestOptions } = options
  const baseUrl = waitForApiBaseUrl()
  const headers = new Headers(requestOptions.headers)
  headers.set('Accept', responseType === 'blob' ? 'application/octet-stream' : 'application/json')
  setDefaultHeader(headers, 'Accept-Language', get(locale))
  headers.set('X-Request-ID', createRequestId())
  if (session.authenticated) {
    setDefaultHeader(headers, 'X-User-ID', session.actorId)
    setDefaultHeader(headers, 'X-User-Display-Name', encodeHeaderValue(session.displayName))
    setDefaultHeader(headers, 'X-Viewer-Kind', session.viewerKind)
    setDefaultHeader(headers, 'X-Agent-Profile-ID', session.user.agentProfileId)
    setDefaultHeader(headers, 'X-Permission-Scope', session.permissionScope.join(','))
  }

  if (requestOptions.body && !headers.has('Content-Type') && !isMultipartFormData(requestOptions.body)) {
    headers.set('Content-Type', 'application/json')
  }

  if (idempotencyKey) {
    headers.set('Idempotency-Key', idempotencyKey)
  }

  const response = await fetch(`${baseUrl}${path}`, {
    ...requestOptions,
    credentials: requestOptions.credentials ?? 'include',
    headers
  }).catch((error) => {
    reportClientError('api_network', error, path, headers.get('X-Request-ID') ?? undefined)
    throw error
  })

  if (!response.ok) {
    const error = await toApiClientError(response)
    if (!suppressUnauthorizedNotification) notifyUnauthorized(error)
    notifyOntologyAccessFailure(error, path)
    throw error
  }

  if (response.status === 204) {
    return undefined as T
  }

  if (responseType === 'blob') {
    const bytes = await response.arrayBuffer()
    const blob = new Blob([bytes], { type: response.headers.get('Content-Type') ?? 'application/octet-stream' })
    if (typeof blob.text !== 'function') {
      Object.defineProperty(blob, 'text', {
        value: async () => new TextDecoder().decode(bytes)
      })
    }
    return blob as T
  }

  const text = await response.text()
  if (!text) {
    return undefined as T
  }

  return JSON.parse(text) as T
}

function notifyOntologyAccessFailure(error: ApiClientError, path: string) {
  const ontologyUnavailable = error.status === 404 && ontologyAPIPath(path)
  if (!forbiddenHandler || error.status !== 403 && !ontologyUnavailable) return
  try {
    void Promise.resolve(forbiddenHandler(error, path)).catch(() => undefined)
  } catch {
    // Preserve the original API error if scope revalidation fails.
  }
}

function ontologyAPIPath(path: string) {
  try {
    return new URL(path, 'http://api.local').pathname.startsWith('/api/ontology/')
  } catch {
    return false
  }
}

function setDefaultHeader(headers: Headers, name: string, value: string) {
  if (!headers.has(name) && value.trim()) {
    headers.set(name, value)
  }
}

function encodeHeaderValue(value: string) {
  return encodeURIComponent(value.trim())
}

function isMultipartFormData(body: BodyInit) {
  return typeof FormData !== 'undefined' && body instanceof FormData
}

async function toApiClientError(response: Response): Promise<ApiClientError> {
  const body = await readErrorBody(response)
  const code = body?.code ?? body?.error ?? `http_${response.status}`
  const message = body?.message ?? response.statusText ?? `API request failed with ${response.status}`
  const correlationId = body?.correlation_id ?? body?.correlationId ?? body?.request_id ?? body?.requestId

  return new ApiClientError({
    status: response.status,
    code,
    message,
    correlationId,
    details: body
  })
}

function notifyUnauthorized(error: ApiClientError) {
  if (!unauthorizedHandler || !isAuthorizationRequired(error)) return
  try {
    void Promise.resolve(unauthorizedHandler(error)).catch(() => undefined)
  } catch {
    // Preserve the original API error even if the app-level redirect handler fails.
  }
}

function isAuthorizationRequired(error: ApiClientError) {
  return error.status === 401 && error.code === 'unauthorized' && error.message.trim().toLowerCase() === 'authorization is required'
}

async function readErrorBody(response: Response): Promise<ErrorResponseBody | undefined> {
  const text = await response.text()
  if (!text) {
    return undefined
  }

  try {
    return JSON.parse(text) as ErrorResponseBody
  } catch {
    if (looksLikeHTML(text)) {
      return undefined
    }
    return { message: text }
  }
}

function looksLikeHTML(text: string) {
  return /^\s*<!doctype html/i.test(text) || /^\s*<html[\s>]/i.test(text)
}
