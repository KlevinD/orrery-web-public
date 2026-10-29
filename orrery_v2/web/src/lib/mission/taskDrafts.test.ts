import { describe, expect, it } from 'vitest'
import { getTaskDraft, updateTaskDraft } from './taskDrafts'

describe('task drafts', () => {
  it('keeps command input isolated per task', () => {
    let drafts = {}

    drafts = updateTaskDraft(drafts, 'task-1', { assigneeId: 'agent-1' })
    drafts = updateTaskDraft(drafts, 'task-2', { resultSummary: 'second result' })

    expect(getTaskDraft(drafts, 'task-1')).toMatchObject({ assigneeId: 'agent-1', resultSummary: '' })
    expect(getTaskDraft(drafts, 'task-2')).toMatchObject({ assigneeId: '', resultSummary: 'second result' })
  })
})
