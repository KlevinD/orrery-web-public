import { afterEach, describe, expect, it, vi } from 'vitest'
import { ClientErrorReporter, redactClientDiagnostic } from './error-reporting'

afterEach(() => vi.restoreAllMocks())

function fixture() {
  const values = new Map<string, string>()
  const storage = { getItem: (key: string) => values.get(key) ?? null, setItem: (key: string, value: string) => { values.set(key, value) } }
  let actor = 'alice'
  const send = vi.fn<typeof fetch>().mockRejectedValue(new TypeError('offline'))
  const options = { owner: () => actor, actor: () => actor, storage, baseURL: () => '', send }
  return { values, send, reporter: new ClientErrorReporter(options), options, switchActor: (value: string) => { actor = value } }
}

describe('client error delivery', () => {
  it('persists an offline report, redacts secrets, and reuses its ID after restart', async () => {
    const f = fixture()
    const error = new Error('request failed password="private value" https://user:pass@api.example/path?token=hidden')
    error.stack = 'at https://app.example/app.js?session=secret:22:3'
    f.reporter.capture('javascript', error, '/holdings?token=hidden', 'request-1')
    await vi.waitFor(() => expect(f.send).toHaveBeenCalledTimes(1))
    await f.reporter.flush()
    const stored = [...f.values.values()][0]
    expect(stored).not.toMatch(/private value|user:pass|token=hidden|session=secret/)
    expect(JSON.parse(stored)[0].path).toBe('/holdings')
    const originalID = JSON.parse(stored)[0].id
    f.send.mockResolvedValue(new Response('{}', { status: 202 }))
    const restarted = new ClientErrorReporter(f.options)
    await restarted.flush()
    expect(JSON.parse(f.send.mock.calls.at(-1)?.[1]?.body as string).id).toBe(originalID)
    expect(JSON.parse([...f.values.values()][0])).toEqual([])
  })

  it('isolates accounts and retains reports on rate limiting', async () => {
    const f = fixture()
    f.reporter.capture('javascript', new Error('alice error'), '/')
    await vi.waitFor(() => expect(f.send).toHaveBeenCalledTimes(1))
    await f.reporter.flush()
    f.switchActor('bob')
    const restarted = new ClientErrorReporter(f.options)
    f.send.mockResolvedValue(new Response('{}', { status: 429 }))
    const previous = f.send.mock.calls.length
    await restarted.flush()
    expect(f.send).toHaveBeenCalledTimes(previous)
    restarted.capture('javascript', new Error('bob error'), '/')
    await vi.waitFor(() => expect(f.send).toHaveBeenCalledTimes(previous + 1))
    expect(JSON.parse(f.send.mock.calls.at(-1)?.[1]?.body as string).actor_id).toBe('bob')
    expect([...f.values.values()].some((value) => value.includes('alice error'))).toBe(true)
    expect([...f.values.values()].some((value) => value.includes('bob error'))).toBe(true)
  })

  it('ignores aborts and duplicate error objects and bounds the queue when storage is unavailable', async () => {
    const f = fixture()
    const abort = new Error('cancelled'); abort.name = 'AbortError'
    f.reporter.capture('api_network', abort, '/')
    expect(f.values.size).toBe(0)
    const error = new Error('same failure')
    f.reporter.capture('javascript', error, '/')
    f.reporter.capture('unhandled_rejection', error, '/')
    expect(JSON.parse([...f.values.values()][0])).toHaveLength(1)
    for (let i = 0; i < 30; i++) f.reporter.capture('javascript', `failure ${i}`, '/')
    expect(JSON.parse([...f.values.values()][0])).toHaveLength(20)
    const options = { ...f.options, storage: { getItem: () => { throw new Error('denied') }, setItem: () => { throw new Error('denied') } } }
    const memory = new ClientErrorReporter(options)
    memory.capture('javascript', 'storage unavailable', '/')
    await vi.waitFor(() => expect(f.send).toHaveBeenCalled())
    f.send.mockResolvedValue(new Response('{}', { status: 202 }))
    await memory.flush()
  })

  it('redacts bearer tokens and caps Unicode diagnostics', () => {
    expect(redactClientDiagnostic('Authorization: Bearer abc.def Basic YWJjZA== api_key=private sk-abcdefghijklmnop')).not.toMatch(/abc.def|YWJjZA|private|abcdefghijklmnop/)
    expect(Array.from(redactClientDiagnostic('界'.repeat(30), 7))).toHaveLength(7)
  })
})
