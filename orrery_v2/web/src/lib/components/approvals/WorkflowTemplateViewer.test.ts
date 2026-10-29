import { render, screen } from '@testing-library/svelte'
import { beforeEach, describe, expect, it } from 'vitest'
import { setLocale } from '#lib/i18n'
import type { HILWorkflowTemplateDTO } from '#lib/types/api'
import WorkflowTemplateViewer from './WorkflowTemplateViewer.svelte'

function template(): HILWorkflowTemplateDTO {
  return {
    id: 'builtin-command-execution-approval',
    tenant_id: 'system',
    name: '命令执行审批',
    description: '命令执行前由发起人的直属负责人确认。',
    status: 'active',
    draft_definition: {
      risk_levels: ['high', 'critical'],
      stages: [{
        key: 'manager-review',
        name: '直属负责人审批',
        order: 1,
        type: 'approval',
        approval: {
          strategy: 'single',
          assignees: [{ kind: 'requester_manager' }],
          deadline_minutes: 60,
          escalation: [{ actors: [{ kind: 'explicit_human', id: 'H000001' }], deadline_minutes: 30 }]
        }
      }]
    },
    latest_published_version: 1,
    created_by: { kind: 'service', id: 'orrery-bootstrap' },
    created_at: '2026-08-25T00:00:00Z',
    updated_at: '2026-08-25T00:00:00Z'
  }
}

describe('WorkflowTemplateViewer', () => {
  beforeEach(() => setLocale('zh-CN'))

  it('explains a workflow in natural language without edit controls', () => {
    render(WorkflowTemplateViewer, { props: { template: template() } })

    expect(screen.getByRole('heading', { name: '命令执行审批' })).toBeTruthy()
    expect(screen.getByText('发起人的直属上级')).toBeTruthy()
    expect(screen.getByText('由一名审批人确认')).toBeTruthy()
    expect(screen.getByText('逾期后转交：指定审批人 · H000001')).toBeTruthy()
    expect(screen.queryByRole('button')).toBeNull()
    expect(screen.queryByRole('textbox')).toBeNull()
  })
})
