import { afterEach, describe, expect, it, vi } from 'vitest'
import { agentRunRecoveryApi } from '#lib/api/agent-run-recovery'
import { session } from '#lib/stores/session.svelte'
import { useAgentRunRecoveryQuery } from './agent-run-recovery'

vi.mock('@tanstack/svelte-query', async (original) => ({
  ...await original<typeof import('@tanstack/svelte-query')>(),
  createQuery: (options: () => unknown) => options()
}))

type Options = { queryKey: readonly unknown[]; queryFn: (context: { signal: AbortSignal }) => Promise<unknown>; enabled: boolean; gcTime: number; refetchInterval: number | false }
const options = (expanded: boolean, active: boolean, mission = 'mission-1', run = 'run-1') =>
  useAgentRunRecoveryQuery(() => mission, () => run, () => expanded, () => active) as unknown as Options

afterEach(() => vi.restoreAllMocks())

describe('AgentCore recovery query scope', () => {
  it('loads only expanded panels and polls only active runs', () => {
    expect(options(false, true).enabled).toBe(false)
    expect(options(false, true).refetchInterval).toBe(false)
    expect(options(true, true).refetchInterval).toBe(5000)
    expect(options(true, false).refetchInterval).toBe(false)
    expect(options(true, true, '').enabled).toBe(false)
    expect(options(true, true).gcTime).toBe(0)
  })

  it('separates mission, run and authentication context and passes cancellation', async () => {
    const first = options(true, true)
    expect(first.queryKey).not.toEqual(options(true, true, 'mission-2').queryKey)
    expect(first.queryKey).not.toEqual(options(true, true, 'mission-1', 'run-2').queryKey)
    session.securityContextVersion++
    expect(first.queryKey).not.toEqual(options(true, true).queryKey)
    const get = vi.spyOn(agentRunRecoveryApi, 'get').mockResolvedValue({} as never)
    const signal = new AbortController().signal
    await first.queryFn({ signal })
    expect(get).toHaveBeenCalledWith('mission-1', 'run-1', signal)
  })
})
