import { fireEvent, render, screen } from '@testing-library/svelte'
import { describe, expect, it, vi } from 'vitest'
import SkillRecorderDialog from './SkillRecorderDialog.svelte'

describe('SkillRecorderDialog', () => {
  it('moves from demo recording to review and draft output', async () => {
    render(SkillRecorderDialog, {
      props: {
        open: true,
        onclose: vi.fn()
      }
    })

    expect(screen.getByRole('dialog').textContent).toContain('选择录制来源')
    await fireEvent.click(screen.getByRole('button', { name: '加载演示' }))
    expect(screen.getByText('时间线与工具映射')).toBeTruthy()
    await fireEvent.click(screen.getByRole('button', { name: '生成技能草稿' }))

    expect(screen.getByText('SKILL.md')).toBeTruthy()
    expect(screen.getByRole('button', { name: '创建技能' })).toBeTruthy()
  })
})
