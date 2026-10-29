import { fireEvent, render, screen } from '@testing-library/svelte'
import { describe, expect, it, vi } from 'vitest'
import { setLocale } from '#lib/i18n'
import SkillBundleEditorDialog from './SkillBundleEditorDialog.svelte'
import type { SkillViewModel } from '#lib/types/view-models'

describe('SkillBundleEditorDialog', () => {
  it('creates a bundle from selected existing skills and imported zip packages', async () => {
    const onsubmit = vi.fn()
    setLocale('en-US')

    render(SkillBundleEditorDialog, {
      props: {
        open: true,
        mode: 'create',
        skills: [skillFixture('skill.review', 'Review'), skillFixture('skill.test', 'Test')],
        onsubmit
      }
    })

    await fireEvent.input(screen.getByLabelText('Bundle name'), { target: { value: 'Quality pack' } })
    await fireEvent.input(screen.getByLabelText('Description'), { target: { value: 'Review workflow' } })
    await fireEvent.click(screen.getByRole('checkbox', { name: 'Select Review' }))

    const pkg = new File(['zip'], 'lint.zip', { type: 'application/zip' })
    await fireEvent.change(screen.getByLabelText('Import skill packages'), { target: { files: [pkg] } })
    await fireEvent.click(screen.getByRole('button', { name: 'Create bundle' }))

    expect(onsubmit).toHaveBeenCalledWith({
      name: 'Quality pack',
      description: 'Review workflow',
      skillIds: ['skill.review'],
      files: [pkg]
    })
  })
})

function skillFixture(id: string, name: string): SkillViewModel {
  return {
    id,
    name,
    description: `${name} work`,
    category: 'quality',
    version: '1.0.0',
    enabled: true,
    riskLevel: 'low',
    riskTone: 'success',
    statusLabel: 'Enabled',
    statusTone: 'success',
    taskTypes: [],
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
