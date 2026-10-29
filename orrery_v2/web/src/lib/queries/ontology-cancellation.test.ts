import { afterEach, describe, expect, it, vi } from 'vitest'
import { QueryClient, QueryObserver } from '@tanstack/query-core'
import { ontologyContextApi } from '#lib/api/ontology-context'
import { ontologyLineageApi } from '#lib/api/ontology-lineage'
import * as context from './ontology-context'
import * as lineage from './ontology-lineage'

vi.mock('@tanstack/svelte-query', async (original) => ({
  ...await original<typeof import('@tanstack/svelte-query')>(),
  createQuery: (options: () => unknown) => options()
}))

type Options = { queryKey: readonly unknown[]; queryFn: (context: { signal: AbortSignal }) => Promise<unknown>; enabled: boolean }
const options = (query: unknown) => query as Options
afterEach(() => vi.restoreAllMocks())

describe('ontology read cancellation', () => {
  it('passes the query signal through every context and lineage detail endpoint', async () => {
    const cases = [
      [ontologyContextApi, 'objects', () => context.useOntologyContextObjectsQuery(() => ({ tenantId: 'tenant', limit: 10 }))],
      [ontologyContextApi, 'object', () => context.useOntologyContextObjectQuery(() => 'tenant', () => 'object')],
      [ontologyContextApi, 'facts', () => context.useOntologyContextFactsQuery(() => 'tenant', () => 'object')],
      [ontologyContextApi, 'relations', () => context.useOntologyContextRelationsQuery(() => 'tenant', () => 'object')],
      [ontologyContextApi, 'evidence', () => context.useOntologyContextEvidenceQuery(() => 'tenant', () => 'object')],
      [ontologyContextApi, 'neighborhood', () => context.useOntologyContextNeighborhoodQuery(() => ({ tenantId: 'tenant', rootId: 'object' }))],
      [ontologyLineageApi, 'provenance', () => lineage.useOntologyLineageProvenanceQuery(() => 'tenant', () => 'fact')],
      [ontologyLineageApi, 'assertions', () => lineage.useOntologyLineageAssertionsQuery(() => 'tenant', () => 'fact')],
      [ontologyLineageApi, 'evidence', () => lineage.useOntologyLineageEvidenceQuery(() => 'tenant', () => 'fact')],
      [ontologyLineageApi, 'projectionTrace', () => lineage.useOntologyLineageProjectionTraceQuery(() => 'tenant', () => 'fact')]
    ] as const
    const signal = new AbortController().signal
    for (const [api, method, create] of cases) {
      const spy = vi.spyOn(api as Record<string, (...args: unknown[]) => Promise<unknown>>, method).mockResolvedValue({})
      const query = options(create())
      expect(query.enabled).toBe(true)
      await query.queryFn({ signal })
      expect(spy.mock.calls[0].at(-1)).toEqual({ signal })
      spy.mockRestore()
    }
  })

  it('aborts an outstanding object request when its last observer leaves', async () => {
    let requestSignal: AbortSignal | undefined
    vi.spyOn(ontologyContextApi, 'object').mockImplementation((_tenant, _id, request) => {
      requestSignal = request?.signal ?? undefined
      return new Promise((_resolve, reject) => requestSignal?.addEventListener('abort', () => reject(new DOMException('Cancelled', 'AbortError'))))
    })
    const client = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 } } })
    const query = options(context.useOntologyContextObjectQuery(() => 'tenant', () => 'object'))
    const observer = new QueryObserver(client, query)
    const unsubscribe = observer.subscribe(() => {})
    expect(requestSignal?.aborted).toBe(false)
    unsubscribe()
    expect(requestSignal?.aborted).toBe(true)
    client.clear()
  })

  it('preserves scope keys and inactive-tab guards', () => {
    const first = options(context.useOntologyContextFactsQuery(() => 'tenant-a', () => 'object', () => ({}), () => false))
    const second = options(context.useOntologyContextFactsQuery(() => 'tenant-b', () => 'object'))
    expect(first.enabled).toBe(false)
    expect(first.queryKey).not.toEqual(second.queryKey)
    expect(options(context.useOntologyContextObjectQuery(() => '', () => 'object')).enabled).toBe(false)
  })
})
