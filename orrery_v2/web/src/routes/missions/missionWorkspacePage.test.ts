import { readFileSync } from 'node:fs'
import { describe, expect, it } from 'vitest'

describe('mission workspace Todo origin controls', () => {
  it('links back to the originating Todo and exposes its execution attempt', () => {
    const source = readFileSync('src/routes/missions/[missionId]/+page.svelte', 'utf8')

    expect(source).toContain('mission.originTodoId')
    expect(source).toContain('/todos/${encodeURIComponent(mission.originTodoId)}')
    expect(source).toContain("t('workspace.executionAttempt', { attempt: mission.executionAttempt })")
  })

  it('shows an explicit restricted state instead of a canonical chat link', () => {
    const source = readFileSync('src/routes/missions/[missionId]/+page.svelte', 'utf8')

    expect(source).toContain('{#if view.discussionRestricted}')
    expect(source).toContain("t('workspace.discussionRestricted')")
    expect(source).toContain('{:else if discussionHref}')
  })

  it('uses the complete control projection without refetching each subview', () => {
    const source = readFileSync('src/routes/missions/[missionId]/+page.svelte', 'utf8')

    expect(source).toContain('const view = $derived(controlQuery.data)')
    expect(source).not.toContain('mapMissionControlViewFromSubviews')
    expect(source).not.toContain('useMissionTeamQuery')
    expect(source).not.toContain('useMissionTasksQuery')
    expect(source).not.toContain('useMissionProgressQuery')
    expect(source).not.toContain('useMissionEvidenceQuery')
    expect(source).not.toContain('useMissionQAQuery')
  })
})
