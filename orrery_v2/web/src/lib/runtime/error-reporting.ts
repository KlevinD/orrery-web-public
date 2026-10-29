import { session } from '#lib/stores/session.svelte'
import { waitForApiBaseUrl } from './api-base'
import { createRequestId } from './ids'

export type ClientErrorKind = 'javascript' | 'unhandled_rejection' | 'api_network' | 'api_response'
export interface ClientDiagnostic {
  id: string
  kind: ClientErrorKind
  name: string
  message: string
  stack: string
  path: string
  actor_id: string
  request_id?: string
}
interface ReporterOptions {
  owner: () => string
  actor: () => string
  baseURL: () => string
  storage: Pick<Storage, 'getItem' | 'setItem'>
  send: typeof fetch
}

export function redactClientDiagnostic(value: string, limit = 1024): string {
  return Array.from(value
    .replace(/\b(?:bearer|basic)\s+[a-z0-9._~+/=-]+/gi, 'Bearer [REDACTED]')
    .replace(/(["']?(?:password|passwd|token|access_token|refresh_token|secret|api[_-]?key|authorization|cookie|credential)["']?\s*[:=]\s*)(?:"[^"\r\n]*"|'[^'\r\n]*'|[^\s,;]+)/gi, '$1[REDACTED]')
    .replace(/\bsk-[a-zA-Z0-9_-]{12,}/g, '[REDACTED]')
    .replace(/https?:\/\/[^\s)]+/gi, (value) => {
      try {
        const url = new URL(value)
        return `${url.origin}${url.pathname}`
      } catch { return '[URL]' }
    })
    .replace(/[\u0000-\u0008\u000b\u000c\u000e-\u001f]/g, '')
    .trim()).slice(0, limit).join('')
}

function safePath(value: string) {
  try { return new URL(value, 'http://diagnostic.local').pathname.slice(0, 512) } catch { return '/' }
}

// Stable IDs are retained until acknowledgement; a lost response can be retried
// without creating a second observation. Only the current account's queue is sent.
export class ClientErrorReporter {
  private active = false
  private memory = new Map<string, ClientDiagnostic[]>()
  private seen = new WeakSet<object>()
  constructor(private options: ReporterOptions) {}

  capture(kind: ClientErrorKind, error: unknown, path: string, requestID?: string) {
    const owner = this.options.owner()
    if (!owner || error instanceof Error && error.name === 'AbortError') return
    if (typeof error === 'object' && error !== null) {
      if (this.seen.has(error)) return
      this.seen.add(error)
    }
    const value = error instanceof Error ? error : new Error(typeof error === 'string' ? error : 'Unhandled client error')
    const queue = this.read(owner)
    queue.push({ id: createRequestId(), kind, name: value.name.slice(0, 128),
      message: redactClientDiagnostic(value.message || value.name), stack: redactClientDiagnostic(value.stack ?? '', 1536),
      path: safePath(path), actor_id: this.options.actor().slice(0, 256), request_id: requestID?.slice(0, 128) })
    this.write(owner, queue.slice(-20))
    void this.flush()
  }

  async flush() {
    if (this.active) return
    this.active = true
    const owner = this.options.owner()
    try {
      while (owner && this.options.owner() === owner) {
        const report = this.read(owner)[0]
        if (!report) return
        const controller = new AbortController()
        const timeout = setTimeout(() => controller.abort(), 8000)
        let response: Response
        try {
          response = await this.options.send(`${this.options.baseURL()}/api/diagnostics/client-errors`, {
            method: 'POST', credentials: 'include', signal: controller.signal,
            headers: { 'Content-Type': 'application/json', 'X-Request-ID': report.id }, body: JSON.stringify(report)
          })
        } finally { clearTimeout(timeout) }
        if (this.options.owner() !== owner) return
        // Invalid/oversized reports cannot recover by retrying. Authentication,
        // rate limiting, and server/network failures retain the pending record.
        if (!response.ok && response.status !== 400 && response.status !== 413) return
        this.write(owner, this.read(owner).filter((item) => item.id !== report.id))
      }
    } catch {
      // The uploader uses raw fetch and never reports its own errors.
    } finally { this.active = false }
  }

  private key(owner: string) { return `orrery.diagnostics.v1:${encodeURIComponent(owner)}` }
  private read(owner: string): ClientDiagnostic[] {
    const memory = this.memory.get(owner)
    if (memory) return memory
    try {
      const stored: unknown = JSON.parse(this.options.storage.getItem(this.key(owner)) ?? 'null')
      if (Array.isArray(stored)) {
        const queue = stored.filter((item): item is ClientDiagnostic => Boolean(item && typeof item.id === 'string' && typeof item.message === 'string' && item.actor_id === this.options.actor())).slice(-20)
        this.memory.set(owner, queue)
        return queue
      }
    } catch { /* Private browsing may deny local storage. */ }
    return this.memory.get(owner) ?? []
  }
  private write(owner: string, queue: ClientDiagnostic[]) {
    if (this.memory.size > 8) this.memory.clear()
    this.memory.set(owner, queue)
    try { this.options.storage.setItem(this.key(owner), JSON.stringify(queue)) } catch { /* Keep an in-memory fallback. */ }
  }
}

let reporter: ClientErrorReporter | undefined
function currentReporter() {
  if (typeof window === 'undefined') return undefined
  reporter ??= new ClientErrorReporter({
    owner: () => session.authenticated && session.actorId ? `${waitForApiBaseUrl()}|${session.subjectProvider}|${session.user.userId}|${session.actorId}` : '',
    actor: () => session.actorId,
    baseURL: waitForApiBaseUrl,
    storage: { getItem: (key) => window.localStorage.getItem(key), setItem: (key, value) => window.localStorage.setItem(key, value) },
    send: (...args) => fetch(...args)
  })
  return reporter
}

export function reportClientError(kind: ClientErrorKind, error: unknown, path?: string, requestID?: string) {
  try { currentReporter()?.capture(kind, error, path ?? window.location.pathname, requestID) } catch { /* Preserve the original failure. */ }
}

export function installClientErrorReporting() {
  const errors = (event: ErrorEvent) => reportClientError('javascript', event.error ?? event.message)
  const rejections = (event: PromiseRejectionEvent) => reportClientError('unhandled_rejection', event.reason)
  const flush = () => { void currentReporter()?.flush() }
  window.addEventListener('error', errors)
  window.addEventListener('unhandledrejection', rejections)
  window.addEventListener('online', flush)
  const timer = setInterval(flush, 60_000)
  flush()
  return () => {
    clearInterval(timer)
    window.removeEventListener('error', errors)
    window.removeEventListener('unhandledrejection', rejections)
    window.removeEventListener('online', flush)
  }
}
