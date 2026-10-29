import { fireEvent, render, screen } from '@testing-library/svelte'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import WorkflowTaskList from './WorkflowTaskList.svelte'
import { workflowView } from './workflowTestFixture'
import { setLocale } from '#lib/i18n'

describe('WorkflowTaskList', () => {
  beforeEach(() => setLocale('zh-CN'))

  it('renders a dense owned-task queue and selects an instance', async () => {
    const onselect = vi.fn()
    const scheduled = workflowView()
    scheduled.id = 'workflow-2'
    scheduled.title = 'Historical approval'
    scheduled.activeTask = undefined
    scheduled.currentStageKey = 'human-review'
    render(WorkflowTaskList, { props: { items: [workflowView(), scheduled], selectedId: '', onselect } })

    expect(screen.getByText('Reviewed title')).toBeTruthy()
    expect(screen.getByText('市场审批')).toBeTruthy()
    expect(screen.getByText('人工审批')).toBeTruthy()
    expect(screen.getByText('ding')).toBeTruthy()
    expect(screen.getAllByText('第 2 版')).toHaveLength(2)
    expect(screen.queryByText('human:ding')).toBeNull()
    expect(screen.queryByText('R2')).toBeNull()
    expect(screen.getAllByText('进行中')).toHaveLength(2)
    expect(screen.queryByText('running')).toBeNull()
    await fireEvent.click(screen.getByRole('button', { name: /Reviewed title/ }))
    expect(onselect).toHaveBeenCalledWith('workflow-1')
  })
})
