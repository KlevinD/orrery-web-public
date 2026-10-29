import { fireEvent, render, screen } from '@testing-library/svelte'
import { describe, expect, it, vi } from 'vitest'
import { setLocale } from '#lib/i18n'
import SkillBundleDeleteDialog from './SkillBundleDeleteDialog.svelte'
import type { SkillBundleViewModel } from '#lib/types/view-models'

describe('SkillBundleDeleteDialog', () => {
  it('lets the operator opt into deleting member skills', async () => {
    const onconfirm = vi.fn()
    const bundle = bundleFixture()
    setLocale('en-US')

    render(SkillBundleDeleteDialog, { props: { open: true, bundle, onconfirm } })

    expect(screen.getByRole('dialog', { name: 'Delete skill bundle?' })).toBeTruthy()
    await fireEvent.click(screen.getByRole('checkbox', { name: 'Also delete 2 member skills' }))
    await fireEvent.click(screen.getByRole('button', { name: 'Delete bundle' }))

    expect(onconfirm).toHaveBeenCalledWith(bundle, true)
  })
})

function bundleFixture(): SkillBundleViewModel {
  return {
    id: 'bundle.quality',
    name: 'Quality pack',
    description: 'Review workflow',
    skillIds: ['skill.review', 'skill.test'],
    skills: [],
    memberCount: 2,
    createdBy: 'operator-1',
    createdAt: 'Jul 19, 2026',
    updatedAt: 'Jul 19, 2026'
  }
}
