import { afterEach, describe, expect, it, vi } from 'vitest'
import { clearApiBaseUrlForTests } from '#lib/runtime/api-base'
import { buildResponsibilityQuery, responsibilityApi } from './responsibilities'

describe('responsibility API', () => {
  afterEach(() => {
    vi.restoreAllMocks()
    clearApiBaseUrlForTests()
  })

  it('serializes inbox filters without leaking tenant selection into the client', () => {
    expect(buildResponsibilityQuery({
      agentId: ' agent/one ',
      states: ['active', 'waiting'],
      limit: 80
    })).toBe('?agent_id=agent%2Fone&state=active%2Cwaiting&limit=80')
  })

  it('binds controls and blocker resolution to idempotency and optimistic concurrency headers', async () => {
    const fetchMock = vi.spyOn(globalThis, 'fetch').mockImplementation(async () =>
      new Response(JSON.stringify({ responsibility_id: 'responsibility/one', version: 8 }), {
        status: 200,
        headers: { 'Content-Type': 'application/json' }
      })
    )

    await responsibilityApi.control(
      'responsibility/one',
      'pause',
      7,
      { reason: 'operator pause' },
      { idempotencyKey: 'control-key' }
    )
    const [controlURL, controlInit] = fetchMock.mock.calls[0]
    expect(controlURL).toBe('/api/responsibilities/responsibility%2Fone/pause')
    expect(controlInit?.method).toBe('POST')
    expect(new Headers(controlInit?.headers).get('If-Match')).toBe('"7"')
    expect(new Headers(controlInit?.headers).get('Idempotency-Key')).toBe('control-key')
    expect(controlInit?.body).toBe(JSON.stringify({ reason: 'operator pause' }))

    await responsibilityApi.resolveBlocker(
      'blocker/one',
      3,
      { resolution: 'permission granted' },
      { idempotencyKey: 'blocker-key' }
    )
    const [blockerURL, blockerInit] = fetchMock.mock.calls[1]
    expect(blockerURL).toBe('/api/responsibility-blockers/blocker%2Fone/resolve')
    expect(new Headers(blockerInit?.headers).get('If-Match')).toBe('"3"')
    expect(new Headers(blockerInit?.headers).get('Idempotency-Key')).toBe('blocker-key')
  })

  it('updates policies through CAS and a distinct idempotency key', async () => {
    const fetchMock = vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response(JSON.stringify({ policy_id: 'policy-1', version: 5 }), {
        status: 200,
        headers: { 'Content-Type': 'application/json' }
      })
    )
    await responsibilityApi.updatePolicy('agent/one', 'mission_task', 4, {
      autonomy_level: 'execute_scoped',
      max_parallel: 2,
      accept_timeout_seconds: 120,
      heartbeat_timeout_seconds: 90,
      max_attempts: 3,
      retry_backoff_seconds: [30, 120],
      max_cost_per_task: 0,
      max_token_budget: 20000,
      approval_policy: 'only_high_risk',
      allowed_risk_levels: ['low'],
      tool_scopes: ['todo.write'],
      data_scopes: ['mission'],
      escalation_chain: [],
      recheck_interval_seconds: 900,
      deadline_warning_seconds: 1800,
      enabled: true
    }, { idempotencyKey: 'policy-key' })

    const [url, init] = fetchMock.mock.calls[0]
    expect(url).toBe('/api/responsibility-policies/agent%2Fone?work_kind=mission_task')
    expect(init?.method).toBe('PUT')
    expect(new Headers(init?.headers).get('If-Match')).toBe('"4"')
    expect(new Headers(init?.headers).get('Idempotency-Key')).toBe('policy-key')
  })
})
