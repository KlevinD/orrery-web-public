import { fireEvent, render, screen } from '@testing-library/svelte'
import { describe, expect, it, vi } from 'vitest'
import { setLocale } from '#lib/i18n'
import SkillMarkdownPanel from './SkillMarkdownPanel.svelte'

describe('SkillMarkdownPanel', () => {
  it('switches between markdown preview and source without comparison mode', async () => {
    setLocale('en-US')
    render(SkillMarkdownPanel, {
      props: {
        raw: '# Skill Prompt\n\nUse the DOCX API.',
        editable: false,
        busy: false,
        onsave: vi.fn()
      }
    })

    expect(screen.getByRole('heading', { name: 'Skill Prompt' })).toBeTruthy()

    await fireEvent.click(screen.getByRole('button', { name: 'Source' }))

    expect((screen.getByLabelText('SKILL.md source') as HTMLTextAreaElement).value).toBe('# Skill Prompt\n\nUse the DOCX API.')
    expect(screen.queryByRole('heading', { name: 'Skill Prompt' })).toBeNull()
  })

  it('edits source and saves markdown only in editable mode', async () => {
    const onsave = vi.fn()
    setLocale('en-US')
    render(SkillMarkdownPanel, {
      props: {
        raw: '# Original',
        editable: true,
        busy: false,
        onsave
      }
    })

    await fireEvent.click(screen.getByRole('button', { name: 'Source' }))
    await fireEvent.input(screen.getByLabelText('SKILL.md source'), { target: { value: '# Updated' } })
    await fireEvent.click(screen.getByRole('button', { name: 'Save SKILL.md' }))

    expect(onsave).toHaveBeenCalledWith('# Updated')
  })

  it('allows saving when metadata changed outside the markdown editor', async () => {
    const onsave = vi.fn()
    setLocale('en-US')
    render(SkillMarkdownPanel, {
      props: {
        raw: '# Original',
        editable: true,
        busy: false,
        saveEnabled: true,
        onsave
      }
    })

    await fireEvent.click(screen.getByRole('button', { name: 'Save SKILL.md' }))

    expect(onsave).toHaveBeenCalledWith('# Original')
  })

  it('keeps save disabled when editable content and metadata are unchanged', () => {
    setLocale('en-US')
    render(SkillMarkdownPanel, {
      props: {
        raw: '# Original',
        editable: true,
        busy: false,
        onsave: vi.fn()
      }
    })

    expect(screen.getByRole('button', { name: 'Save SKILL.md' }).hasAttribute('disabled')).toBe(true)
  })

  it('shows source and preview together in comparison mode', async () => {
    setLocale('en-US')
    render(SkillMarkdownPanel, {
      props: {
        raw: '# Compare Me',
        editable: true,
        busy: false,
        onsave: vi.fn()
      }
    })

    await fireEvent.click(screen.getByRole('button', { name: 'Compare' }))

    expect(screen.getByLabelText('SKILL.md source')).toBeTruthy()
    expect(screen.getByRole('heading', { name: 'Compare Me' })).toBeTruthy()
  })

  it('removes unsafe markdown links from preview html', () => {
    setLocale('en-US')
    const view = render(SkillMarkdownPanel, {
      props: {
        raw: '[Run script](javascript:alert(1))',
        editable: false,
        busy: false,
        onsave: vi.fn()
      }
    })

    const link = view.container.querySelector('a')

    expect(link?.getAttribute('href')).toBeNull()
  })
})
