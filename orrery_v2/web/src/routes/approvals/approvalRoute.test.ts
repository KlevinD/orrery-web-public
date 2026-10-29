import { describe, expect, it } from 'vitest'
import { mergeApprovalItems, parseApprovalRoute, resolveWorkflowSelection } from './approvalRoute'

describe('approval route', () => {
  it('defaults to the unified approval queue', () => {
    expect(parseApprovalRoute(new URL('https://orrery.test/approvals'))).toEqual({ focus: 'queue', workflowId: '', councilId: '' })
  })

  it('accepts legacy mode links as focus hints without splitting the queue', () => {
    expect(parseApprovalRoute(new URL('https://orrery.test/approvals?mode=hil'))).toEqual({ focus: 'queue', workflowId: '', councilId: '' })
    expect(parseApprovalRoute(new URL('https://orrery.test/approvals?mode=approval'))).toEqual({ focus: 'queue', workflowId: '', councilId: '' })
    expect(parseApprovalRoute(new URL('https://orrery.test/approvals?mode=council'))).toEqual({ focus: 'council', workflowId: '', councilId: '' })
  })

  it('keeps workflow deep links in the unified queue', () => {
    expect(parseApprovalRoute(new URL('https://orrery.test/approvals?workflow_id=workflow-1'))).toEqual({ focus: 'queue', workflowId: 'workflow-1', councilId: '' })
    expect(parseApprovalRoute(new URL('https://orrery.test/approvals?view=my_tasks&workflow_id=workflow-2'))).toEqual({ focus: 'queue', workflowId: 'workflow-2', councilId: '' })
  })

  it('focuses the exact council selected by a routed decision link', () => {
    expect(parseApprovalRoute(new URL('https://orrery.test/approvals?mode=council&council=council-1'))).toEqual({ focus: 'council', workflowId: '', councilId: 'council-1' })
  })

  it('keeps an exact linked workflow selected even when it is outside the current list page', () => {
    expect(resolveWorkflowSelection([{ id: 'workflow-visible' }], 'workflow-linked', 'workflow-linked')).toBe('workflow-linked')
    expect(resolveWorkflowSelection([{ id: 'workflow-visible' }], 'workflow-missing', '')).toBe('workflow-visible')
  })

  it('merges route sources in priority order and removes duplicate decisions', () => {
    expect(mergeApprovalItems(
      [{ id: 'manual', source: 'hil' }],
      [{ id: 'fixed', source: 'approval' }, { id: 'manual', source: 'duplicate' }],
      [{ id: 'business', source: 'legacy' }]
    )).toEqual([
      { id: 'manual', source: 'hil' },
      { id: 'fixed', source: 'approval' },
      { id: 'business', source: 'legacy' }
    ])
  })
})
