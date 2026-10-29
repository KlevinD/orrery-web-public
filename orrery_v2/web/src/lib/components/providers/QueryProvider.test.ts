import { render, screen } from '@testing-library/svelte'
import { readFileSync } from 'fs'
import { describe, expect, it } from 'vitest'
import QueryProviderHarness from './QueryProviderHarness.svelte'

describe('QueryProvider', () => {
  it('makes a TanStack query client available through Svelte context', () => {
    render(QueryProviderHarness)

    expect(screen.getByText('query-client-ready')).toBeTruthy()
  })

  it('delegates query context setup to TanStack QueryClientProvider', () => {
    const source = readFileSync('src/lib/components/providers/QueryProvider.svelte', 'utf-8')

    expect(source).toContain('QueryClientProvider')
    expect(source).not.toContain('setQueryClientContext')
  })
})
