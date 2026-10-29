import { readFileSync } from 'node:fs'
import { describe, expect, it } from 'vitest'

const read = (path: string) => readFileSync(path, 'utf-8')

describe('right sidebar consistency', () => {
  it('defines one shell contract for rails and drawers', () => {
    const css = read('src/app.css')

    expect(css).toContain('.right-sidebar-shell {')
    expect(css).toContain('.right-sidebar-rail {')
    expect(css).toContain('width: 18.75rem;')
    expect(css).toContain('.right-sidebar-drawer {')
    expect(css).toContain('.right-sidebar-header {')
    expect(css).toContain('min-height: 2.75rem;')
    expect(css).toContain('.right-sidebar-body {')
    expect(css).toContain('.right-sidebar-footer {')
  })

  it('uses the 300px rail shell for persistent right sidebars', () => {
    const sources = [
      'src/lib/components/layout/WorkspaceContextPanel.svelte',
      'src/lib/components/layout/ConversationSwitchPanel.svelte',
      'src/lib/components/discussions/DiscussionPanel.svelte',
      'src/lib/components/mission/MissionDiscussionPanel.svelte',
      'src/lib/components/mission/MissionRoom.svelte'
    ].map(read)

    for (const source of sources) {
      expect(source).toContain('right-sidebar-shell')
      expect(source).toContain('right-sidebar-rail')
      expect(source).toContain('right-sidebar-header')
      expect(source).toContain('right-sidebar-body')
    }

    expect(read('src/lib/components/mission/MissionDiscussionPanel.svelte')).not.toContain('w-64')
    expect(read('src/lib/components/discussions/DiscussionPanel.svelte')).not.toContain('md:w-72 md:max-w-none')
    expect(read('src/lib/components/mission/MissionRoom.svelte')).toContain('xl:grid-cols-[minmax(0,1fr)_18.75rem]')
  })

  it('uses the same structure for inspectors and slide-in editors', () => {
    const inspector = read('src/lib/components/knowledge/KnowledgeWorkspace.svelte')
    const discussion = read('src/lib/components/discussions/DiscussionPanel.svelte')
    const drawers = [
      'src/lib/components/settings/GatewayChannelPanel.svelte',
      'src/lib/components/settings/LLMProfilePanel.svelte',
      'src/lib/components/settings/RuntimeProfilePanel.svelte',
      'src/routes/evaluation/AgentMemorySheet.svelte'
    ].map(read)

    expect(inspector).toContain('right-sidebar-shell workspace-pane')
    expect(inspector).toContain('xl:grid-cols-[15rem_minmax(0,1fr)_18.75rem]')
    expect(discussion).toContain('right-sidebar-shell right-sidebar-drawer')

    for (const source of drawers) {
      expect(source).toContain('right-sidebar-shell right-sidebar-drawer')
      expect(source).toContain('right-sidebar-header right-sidebar-header-comfortable')
      expect(source).toContain('right-sidebar-body')
    }
  })
})
