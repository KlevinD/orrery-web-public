import { fireEvent, render, screen } from '@testing-library/svelte'
import { describe, expect, it, vi } from 'vitest'
import { setLocale } from '#lib/i18n'
import SkillBundleCard from './SkillBundleCard.svelte'
import type { SkillBundleViewModel, SkillViewModel } from '#lib/types/view-models'

describe('SkillBundleCard', () => {
  it('shows members and exposes add, remove, and delete commands', async () => {
    const onadd = vi.fn()
    const onremove = vi.fn()
    const ondelete = vi.fn()
    const bundle = bundleFixture()
    setLocale('en-US')

    render(SkillBundleCard, { props: { bundle, onadd, onremove, ondelete } })

    expect(screen.getByText('Quality pack')).toBeTruthy()
    expect(screen.getByText('1 skill')).toBeTruthy()
    expect(screen.getByText('Review')).toBeTruthy()

    await fireEvent.click(screen.getByRole('button', { name: 'Add skills to Quality pack' }))
    await fireEvent.click(screen.getByRole('button', { name: 'Remove Review from Quality pack' }))
    await fireEvent.click(screen.getByRole('button', { name: 'Delete Quality pack' }))

    expect(onadd).toHaveBeenCalledWith(bundle)
    expect(onremove).toHaveBeenCalledWith(bundle, bundle.skills[0])
    expect(ondelete).toHaveBeenCalledWith(bundle)
  })

  it('uses a concise Chinese summary for an English backend description', () => {
    setLocale('zh-CN')
    render(SkillBundleCard, { props: { bundle: bundleFixture() } })

    expect(screen.getByText('该技能包用于统一管理成员技能。')).toBeTruthy()
    expect(screen.queryByText('Review workflow')).toBeNull()
  })
})

function bundleFixture(): SkillBundleViewModel {
  return {
    id: 'bundle.quality',
    name: 'Quality pack',
    description: 'Review workflow',
    skillIds: ['skill.review'],
    skills: [skillFixture()],
    memberCount: 1,
    createdBy: 'operator-1',
    createdAt: 'Jul 19, 2026',
    updatedAt: 'Jul 19, 2026'
  }
}

function skillFixture(): SkillViewModel {
  return {
    id: 'skill.review',
    name: 'Review',
    description: 'Review changes',
    category: 'quality',
    version: '1.0.0',
    enabled: true,
    riskLevel: 'low',
    riskTone: 'success',
    statusLabel: 'Enabled',
    statusTone: 'success',
    taskTypes: ['review'],
    requiredTools: [],
    promptAssets: [],
    evidenceRefs: [],
    sourceRef: 'local',
    updatedAt: 'Jul 19, 2026',
    skillMdPresent: true,
    resourceCounts: { assets: 0, resources: 0, references: 0, scripts: 0, other: 0, total: 0 },
    usageCount: 0,
    boundAgentCount: 0,
    boundAgents: []
  }
}
