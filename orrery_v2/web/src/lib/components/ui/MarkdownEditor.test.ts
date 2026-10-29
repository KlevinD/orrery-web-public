import { fireEvent, render, screen } from '@testing-library/svelte'
import { describe, expect, it } from 'vitest'
import MarkdownEditor from './MarkdownEditor.svelte'

describe('MarkdownEditor', () => {
  it('edits markdown and previews rendered content', async () => {
    render(MarkdownEditor, {
      props: {
        value: '# 验收标准\n\n- 支持回复',
        ariaLabel: '待办内容',
        writeLabel: '编辑',
        previewLabel: '预览'
      }
    })

    expect((screen.getByLabelText('待办内容') as HTMLTextAreaElement).value).toContain('# 验收标准')

    await fireEvent.click(screen.getByRole('button', { name: '预览' }))

    expect(screen.getByRole('heading', { name: '验收标准' })).toBeTruthy()
    expect(screen.getByText('支持回复')).toBeTruthy()
    expect(screen.queryByLabelText('待办内容')).toBeNull()
  })

  it('removes unsafe urls from markdown preview', async () => {
    const view = render(MarkdownEditor, {
      props: {
        value: '[危险链接](javascript:alert(1))',
        ariaLabel: '回复内容',
        writeLabel: '编辑',
        previewLabel: '预览'
      }
    })

    await fireEvent.click(screen.getByRole('button', { name: '预览' }))

    const link = view.container.querySelector('a')
    expect(link?.getAttribute('href')).toBeNull()
  })
})
