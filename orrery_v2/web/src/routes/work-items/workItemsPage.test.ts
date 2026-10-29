import { describe, expect, it } from 'vitest'
import { readFileSync } from 'node:fs'

describe('work items route wiring', () => {
  it('loads real Todo, Idea, and Mission data in the dedicated route', () => {
    const source = readFileSync('src/routes/work-items/+page.svelte', 'utf8')

    expect(source).toContain('useTodoInfiniteListQuery')
    expect(source).toContain('useIdeaInfiniteListQuery')
    expect(source).toContain('useMissionInfiniteListQuery')
    expect(source).toContain('buildWorkCenterItems')
    expect(source).toContain('<WorkItemsCenter')
  })

  it('keeps the accepted vertical stage layout and excludes chat UI', () => {
    const source = readFileSync('src/lib/components/workhub/WorkItemsCenter.svelte', 'utf8')

    expect(source).toContain("lg:grid-cols-[220px_minmax(0,1fr)]")
    expect(source).toContain("before:absolute before:bottom-6 before:left-5")
    expect(source).not.toContain('MarkdownEditor')
    expect(source).not.toContain('DiscussionPanel')
  })
})
