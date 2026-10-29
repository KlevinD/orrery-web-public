import { readFileSync } from 'node:fs'
import { describe, expect, it } from 'vitest'

describe('responsibility page deep links', () => {
  it('loads a requested responsibility directly instead of requiring it in the inbox page', () => {
    const source = readFileSync('src/routes/responsibilities/+page.svelte', 'utf-8')

    expect(source).toContain("searchParams.get('responsibility')")
    expect(source).toContain('selectedId = requested')
    expect(source).not.toContain('allItems.some((item) => item.responsibility_id === requested)')
    expect(source).toContain('?? detailQuery.data')
  })
})
