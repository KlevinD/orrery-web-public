import { fireEvent, render, screen } from '@testing-library/svelte'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import WorkflowTemplateEditor from './WorkflowTemplateEditor.svelte'
import { setLocale } from '#lib/i18n'
import type { HILWorkflowTemplateDTO } from '#lib/types/api'

function template(): HILWorkflowTemplateDTO {
  return {
    id: 'template-1',
    tenant_id: 'tenant-1',
    name: 'Marketing approval',
    description: 'Review public content',
    status: 'active',
    draft_definition: {
      risk_levels: ['medium'],
      stages: [{
        key: 'review',
        name: 'Marketing review',
        order: 1,
        type: 'approval',
        approval: {
          strategy: 'single',
          assignees: [{ kind: 'explicit_human', id: 'ding' }],
          deadline_minutes: 60
        }
      }]
    },
    latest_published_version: 2,
    created_by: { kind: 'human', id: 'orrery' },
    created_at: '2026-08-17T00:00:00Z',
    updated_at: '2026-08-17T01:00:00Z'
  }
}

describe('WorkflowTemplateEditor', () => {
  beforeEach(() => setLocale('zh-CN'))

  it('blocks an invalid draft before calling save', async () => {
    const onsave = vi.fn()
    render(WorkflowTemplateEditor, { props: { onsave } })

    await fireEvent.click(screen.getByRole('button', { name: '保存草稿' }))

    expect(screen.getByText('请输入模板名称')).toBeTruthy()
    expect(onsave).not.toHaveBeenCalled()
  })

  it('adds a simple council stage and emits an ordered draft definition', async () => {
    const onsave = vi.fn()
    render(WorkflowTemplateEditor, { props: { template: template(), onsave } })

    await fireEvent.click(screen.getByRole('button', { name: '添加和议阶段' }))
    await fireEvent.input(screen.getByRole('textbox', { name: '和议阶段 2 参与人' }), { target: { value: 'advisor' } })
    await fireEvent.click(screen.getByRole('button', { name: '保存草稿' }))

    expect(onsave).toHaveBeenCalledOnce()
    expect(onsave.mock.calls[0][0].definition.stages).toMatchObject([
      { key: 'review', order: 1, type: 'approval' },
      { key: 'council_2', order: 2, type: 'council', council: { participants: [{ kind: 'explicit_human', id: 'advisor' }], quorum: 1, approval_threshold: 1 } }
    ])
  })

  it('confirms publication bindings and requires a disable reason', async () => {
    const onpublish = vi.fn()
    const ondisable = vi.fn()
    render(WorkflowTemplateEditor, { props: { template: template(), onpublish, ondisable } })

    await fireEvent.click(screen.getByRole('button', { name: '发布版本' }))
    await fireEvent.click(screen.getByRole('button', { name: '确认发布' }))
    expect(onpublish).toHaveBeenCalledWith({
      bindings: [{ action_type_id: 'tool_use', tool_name: 'mcp.marketing.wechat_publish', risk_level: 'medium', active: true }]
    })

    await fireEvent.click(screen.getByRole('button', { name: '停用模板' }))
    const confirmDisable = screen.getByRole('button', { name: '确认停用' }) as HTMLButtonElement
    expect(confirmDisable.disabled).toBe(true)
    await fireEvent.input(screen.getByRole('textbox', { name: '停用原因' }), { target: { value: '流程已替换' } })
    await fireEvent.click(confirmDisable)
    expect(ondisable).toHaveBeenCalledWith('流程已替换')
  })

  it('does not publish unsaved draft edits', async () => {
    render(WorkflowTemplateEditor, { props: { template: template() } })

    const publish = screen.getByRole('button', { name: '发布版本' }) as HTMLButtonElement
    expect(publish.disabled).toBe(false)
    await fireEvent.input(screen.getByRole('textbox', { name: '模板名称' }), { target: { value: 'Changed locally' } })
    expect(publish.disabled).toBe(true)
  })
})
