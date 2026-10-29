import { readFileSync } from 'node:fs'
import { describe, expect, it } from 'vitest'

describe('resource catalog readiness', () => {
  it('keeps tools compact and MCP and skills aligned with their Figma tables', () => {
    const toolsSource = readFileSync('src/routes/resources/tools/+page.svelte', 'utf-8')
    expect(toolsSource).toContain('class="relative min-w-[180px] flex-1 sm:max-w-xs"')
    expect(toolsSource).toContain('text-muted-foreground/60')
    expect(toolsSource).toContain('<div class="w-36">')
    expect(toolsSource.match(/<div class="w-36">/g)?.length ?? 0).toBeGreaterThanOrEqual(4)

    const mcpSource = readFileSync('src/routes/resources/mcp/+page.svelte', 'utf-8')
    expect(mcpSource).toContain('lg:grid-cols-[minmax(0,1fr)_300px]')
    expect(mcpSource).toContain('sm:grid-cols-[minmax(0,1fr)_90px_60px_60px]')
    expect(mcpSource).not.toContain('grid grid-cols-2 gap-3 lg:grid-cols-5')

    const skillsSource = readFileSync('src/routes/resources/skills/+page.svelte', 'utf-8')
    expect(skillsSource).toContain('grid-cols-[minmax(0,1fr)_90px_120px_110px_80px]')
    expect(skillsSource).not.toContain('mobileFiltersOpen')
    expect(skillsSource).not.toContain('grid grid-cols-2 gap-3 lg:grid-cols-5')
  })
})
