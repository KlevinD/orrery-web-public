import { fireEvent, render, screen, within } from '@testing-library/svelte'
import { describe, expect, it, vi } from 'vitest'
import { setLocale } from '#lib/i18n'
import SkillCard from './SkillCard.svelte'
import type { SkillViewModel } from '#lib/types/view-models'

describe('SkillCard', () => {
  it('renders package metadata and opens the detail route from the card body', async () => {
    const onopen = vi.fn()
    setLocale('en-US')
    render(SkillCard, {
      props: {
        skill: skillFixture(),
        onopen,
        onedit: vi.fn(),
        onexport: vi.fn(),
        onbind: vi.fn(),
        ondisable: vi.fn(),
        ondelete: vi.fn()
      }
    })

    const card = screen.getByRole('button', { name: 'Open Minimax DOCX skill details' })
    expect(within(card).getByText('Minimax DOCX')).toBeTruthy()
    expect(within(card).getByText('v1.2.0')).toBeTruthy()
    expect(within(card).getByLabelText('SKILL.md: 1')).toBeTruthy()
    expect(within(card).getByLabelText('Assets: 3')).toBeTruthy()
    expect(within(card).getByLabelText('Resources: 4')).toBeTruthy()
    expect(within(card).getByLabelText('References: 2')).toBeTruthy()
    expect(within(card).getByLabelText('Scripts: 1')).toBeTruthy()
    expect(within(card).getByText('Enabled')).toBeTruthy()
    expect(within(card).getByText('42')).toBeTruthy()
    expect(within(card).getByText('Agent A')).toBeTruthy()
    expect(within(card).getByText('tool.minimax')).toBeTruthy()

    await fireEvent.click(card)

    expect(onopen).toHaveBeenCalledWith('skill.minimax-docx')
  })

  it('routes edit actions separately from package export and lifecycle buttons', async () => {
    const onedit = vi.fn()
    const onexport = vi.fn()
    const onbind = vi.fn()
    const ondelete = vi.fn()
    setLocale('en-US')
    render(SkillCard, {
      props: {
        skill: skillFixture(),
        onopen: vi.fn(),
        onedit,
        onexport,
        onbind,
        ondisable: vi.fn(),
        ondelete
      }
    })

    await fireEvent.click(screen.getByRole('button', { name: 'Edit Minimax DOCX' }))
    await fireEvent.click(screen.getByRole('button', { name: 'Export Minimax DOCX package' }))
    await fireEvent.click(screen.getByRole('button', { name: 'Bind Minimax DOCX' }))
    await fireEvent.click(screen.getByRole('button', { name: 'Delete Minimax DOCX' }))

    expect(onedit).toHaveBeenCalledWith('skill.minimax-docx')
    expect(onexport).toHaveBeenCalledWith(expect.objectContaining({ id: 'skill.minimax-docx' }))
    expect(onbind).toHaveBeenCalledWith(expect.objectContaining({ id: 'skill.minimax-docx' }))
    expect(ondelete).toHaveBeenCalledWith(expect.objectContaining({ id: 'skill.minimax-docx' }))
  })

  it('does not show a placeholder badge when tags are empty', () => {
    setLocale('en-US')
    render(SkillCard, {
      props: {
        skill: { ...skillFixture(), taskTypes: [], category: 'Not provided' },
        onopen: vi.fn()
      }
    })

    const card = screen.getByRole('button', { name: 'Open Minimax DOCX skill details' })
    expect(within(card).queryByText('Not provided')).toBeNull()
  })

  it('localizes backend display metadata in Chinese', () => {
    setLocale('zh-CN')
    render(SkillCard, {
      props: {
        skill: skillFixture(),
        onopen: vi.fn()
      }
    })

    const card = screen.getByRole('button', { name: '打开 Minimax DOCX 技能详情' })
    expect(within(card).getByText('该技能已注册，完整能力说明可在详情中查看。')).toBeTruthy()
    expect(within(card).getByText('已启用')).toBeTruthy()
    expect(within(card).getByText('Agent A')).toBeTruthy()
    expect(within(card).getByText('42')).toBeTruthy()
    expect(within(card).queryByText('Enabled')).toBeNull()
  })
})

function skillFixture(): SkillViewModel {
  return {
    id: 'skill.minimax-docx',
    name: 'Minimax DOCX',
    description: 'Creates and edits docx files with Minimax.',
    category: 'document',
    version: '1.2.0',
    enabled: true,
    riskLevel: 'medium',
    riskTone: 'warning',
    statusLabel: 'Enabled',
    statusTone: 'success',
    taskTypes: ['writing', 'docx', 'automation', 'review'],
    requiredTools: ['tool.minimax', 'tool.file'],
    promptAssets: [],
    evidenceRefs: [],
    sourceRef: 'Not provided',
    updatedAt: 'Jun 09, 2026',
    skillMdPresent: true,
    resourceCounts: {
      assets: 3,
      resources: 4,
      references: 2,
      scripts: 1,
      other: 1,
      total: 11
    },
    usageCount: 42,
    boundAgentCount: 2,
    boundAgents: ['Agent A', 'Agent B']
  }
}
