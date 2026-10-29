import { fireEvent, render, screen } from '@testing-library/svelte'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import WorkflowInstanceDetail from './WorkflowInstanceDetail.svelte'
import { workflowView } from './workflowTestFixture'
import { setLocale } from '#lib/i18n'

describe('WorkflowInstanceDetail', () => {
  beforeEach(() => setLocale('zh-CN'))

  it('shows readable material with the approval flow in the right column', () => {
    render(WorkflowInstanceDetail, { props: { item: workflowView() } })

    const layout = document.querySelector('[data-workflow-review-layout]')
    const content = document.querySelector('[data-approval-content]')
    const readingColumn = document.querySelector('[data-approval-reading-column]')
    const context = document.querySelector('[data-approval-context]')
    const flow = document.querySelector('[data-workflow-flow]')
    expect(layout?.className).toContain('xl:grid-cols-[minmax(0,1fr)_18rem]')
    expect(content?.nextElementSibling).toBe(context)
    expect(context?.contains(flow)).toBe(true)
    expect(readingColumn?.className).toContain('max-w-[62rem]')
    expect(context?.className).toContain('xl:border-l')
    expect(flow?.querySelectorAll('[data-workforce-avatar]')).toHaveLength(2)
    expect(flow?.querySelectorAll('[data-workflow-stage-connector]')).toHaveLength(1)
    expect(flow?.textContent).toContain('ding')
    expect(flow?.textContent).toContain('owner')
    expect(screen.getByRole('heading', { name: 'Reviewed article' })).toBeTruthy()
    expect(screen.getByText('Reviewed digest')).toBeTruthy()
    expect(screen.getByText('市场审批')).toBeTruthy()
    expect(screen.getByText('第 2 版')).toBeTruthy()
    expect(screen.getAllByText('进行中').length).toBeGreaterThanOrEqual(2)
    expect(screen.queryByText('account_id')).toBeNull()
    expect(screen.queryByText('sha256:material')).toBeNull()
    expect(screen.queryByText('workflow-0')).toBeNull()
    expect(screen.queryByText('om_message_1')).toBeNull()
    expect(screen.queryByText('workflow_viewed')).toBeNull()
    expect(screen.queryByText('技术信息')).toBeNull()
    expect(document.querySelector('[data-approval-action-bar]')).toBeTruthy()
  })

  it('requires a reason for rejection and emits the current instance version', async () => {
    const ondecision = vi.fn()
    render(WorkflowInstanceDetail, { props: { item: workflowView(), ondecision } })

    await fireEvent.click(screen.getByRole('button', { name: /拒绝|Reject/ }))
    const submit = screen.getByRole('button', { name: /确认拒绝|Confirm rejection/ }) as HTMLButtonElement
    expect(submit.disabled).toBe(true)
    await fireEvent.input(screen.getByRole('textbox', { name: /理由|Reason/ }), { target: { value: 'Unsupported claim' } })
    expect(submit.disabled).toBe(false)
    await fireEvent.click(submit)
    expect(ondecision).toHaveBeenCalledWith({ taskId: 'task-1', decision: 'reject', reason: 'Unsupported claim', expectedVersion: 3 })
  })

  it('does not expose task actions for scheduled or completed work', () => {
    const item = workflowView()
    item.activeTask = undefined
    item.tasks = item.tasks.map((task) => ({ ...task, actionable: false }))
    render(WorkflowInstanceDetail, { props: { item } })

    expect(screen.queryByRole('button', { name: /拒绝|Reject/ })).toBeNull()
    expect(screen.queryByRole('button', { name: /批准|Approve/ })).toBeNull()
    expect(document.querySelector('[data-approval-action-bar]')).toBeNull()
  })

  it('blocks decisions for legacy business workflows with incomplete material', () => {
    const item = workflowView()
    item.isBusinessApproval = true
    item.materialComplete = false
    render(WorkflowInstanceDetail, { props: { item } })

    expect(screen.getByText('材料不完整，请发起人重新提交')).toBeTruthy()
    expect(screen.queryByRole('button', { name: /批准|Approve/ })).toBeNull()
    expect(screen.queryByRole('button', { name: /拒绝|Reject/ })).toBeNull()
  })

  it('keeps natural business material and attachments while hiding internal references', () => {
    const item = workflowView()
    item.isBusinessApproval = true
    item.businessMaterial = {
      language: 'zh-CN', title: '公众号文章发布审批', decisionRequest: '是否批准发布本期公众号文章？',
      businessContext: '本周产品更新需要对外同步。', proposedAction: '使用主账号发布已完成校对的文章。',
      expectedResult: '用户能在公众号看到本期更新。', impactScope: '公众号主账号及其订阅用户。',
      risks: [{ description: '内容表述可能产生歧义。', mitigation: '发布前由市场负责人完成终审。' }]
    }
    item.evidenceRefs = ['knowledge:campaign-brief']
    item.artifacts = [{ label: '公众号终稿', url: 'https://example.test/final', kind: 'document' }]
    render(WorkflowInstanceDetail, { props: { item } })

    expect(screen.getByText('审批内容')).toBeTruthy()
    expect(screen.getByText('审批流')).toBeTruthy()
    expect(screen.getByText('审批操作')).toBeTruthy()
    expect(screen.getByRole('link', { name: '公众号终稿' })).toBeTruthy()
    expect(screen.queryByText('knowledge:campaign-brief')).toBeNull()
    expect(screen.queryByText('技术信息')).toBeNull()
  })
})
