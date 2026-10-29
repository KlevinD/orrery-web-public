import { readFileSync } from 'node:fs'
import { describe, expect, it } from 'vitest'

describe('AppShell context panel loading', () => {
  it('loads context code only inside an open panel branch', () => {
    const source = readFileSync('src/lib/components/layout/AppShell.svelte', 'utf-8')
    const lazy = readFileSync('src/lib/components/layout/LazyWorkspaceContextPanel.svelte', 'utf-8')

    expect(source).not.toContain("import WorkspaceContextPanel from")
    expect(source).toContain("import LazyWorkspaceContextPanel from './LazyWorkspaceContextPanel.svelte'")
    expect(source).toMatch(/\{#if wideContextViewport && desktopContextOpen && !focusedWorkspaceRoute\}[\s\S]*?<LazyWorkspaceContextPanel/)
    expect(source).toMatch(/\{#if \(!wideContextViewport \|\| focusedWorkspaceRoute\) && mobileContextOpen\}[\s\S]*?<LazyWorkspaceContextPanel showClose/)
    expect(lazy).toContain("import('./WorkspaceContextPanel.svelte')")
    expect(lazy).toContain('right-sidebar-shell right-sidebar-rail')
  })

  it('auto-opens the context panel once on discussion workspaces', () => {
    const source = readFileSync('src/lib/components/layout/AppShell.svelte', 'utf-8')

    expect(source).toContain('let desktopContextOpen = $state(false)')
    expect(source).toContain('let discussionWorkspaceRoute = $derived')
    expect(source).toContain('discussionWorkspaceRoute && wideContextViewport && !discussionContextAutoOpened')
    expect(source).toContain('wideContextViewport && desktopContextOpen && !focusedWorkspaceRoute')
  })

  it('keeps direct discussion routes edge-to-edge and delegates scrolling to the workspace', () => {
    const source = readFileSync('src/lib/components/layout/AppShell.svelte', 'utf-8')

    expect(source).toContain('discussionWorkspaceRoute ||')
    expect(source).toContain("discussionWorkspaceRoute ? 'overflow-hidden' : 'overflow-y-auto'")
    expect(source).not.toContain("$page.url.pathname === '/discussions' ||")
  })
})
