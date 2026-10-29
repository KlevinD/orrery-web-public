import { readFileSync } from 'node:fs'
import { describe, expect, it } from 'vitest'

describe('MCP server page', () => {
  it('matches the compact Figma table while preserving server commands', () => {
    const source = readFileSync('src/routes/resources/mcp/+page.svelte', 'utf-8')

    expect(source).toContain('mcpStatusLabel(server.availabilityStatus, server.enabled)')
    expect(source).toContain('statusDotClass(server)')
    expect(source).toContain('statusTextClass(server)')
    expect(source).toContain("value === 'streamable_http'")
    expect(source).toContain('lg:grid-cols-[minmax(0,1fr)_300px]')
    expect(source).toContain('sm:grid-cols-[minmax(0,1fr)_90px_60px_60px]')
    expect(source).toContain("t('resources.mcp.exposedTools')")
    expect(source).toContain("t('resources.mcp.syncAndHealth')")
    expect(source).not.toContain('grid grid-cols-2 gap-3 lg:grid-cols-5')
    expect(source).not.toContain('>Transport</dt>')
  })
})
