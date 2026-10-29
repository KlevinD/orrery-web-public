import { readFileSync } from 'node:fs'
import { describe, expect, it } from 'vitest'

describe('Mission conversation readiness', () => {
  it('links Mission Control to the authorized canonical conversation without a mission fallback', () => {
    const page = readFileSync('src/routes/missions/[missionId]/+page.svelte', 'utf-8')
    const canonicalRoute = readFileSync('src/routes/discussions/[discussionId]/+page.svelte', 'utf-8')

    expect(page).toContain('view?.discussionConversationId')
    expect(page).toContain('/discussions/${encodeURIComponent(view.discussionConversationId)}')
    expect(page).not.toContain('/discussion/${resolvedMissionId}')
    expect(page).toContain('{#if discussionHref}')
    expect(canonicalRoute).toContain('<DiscussionWorkspace initialDiscussionId={discussionId} />')
  })
})
