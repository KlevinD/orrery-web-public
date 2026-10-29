import { render, screen } from '@testing-library/svelte'
import { createRawSnippet } from 'svelte'
import { describe, expect, it } from 'vitest'
import Dialog from './Dialog.svelte'

describe('Dialog', () => {
  it('keeps long titles and content within the mobile viewport', () => {
    const children = createRawSnippet(() => ({
      render: () => '<p data-dialog-test-content>long dialog content</p>'
    }))

    const { container } = render(Dialog, {
      props: {
        open: true,
        title: 'Responsibility evaluation: all active People profiles',
        children,
        onClose: () => undefined
      }
    })

    const dialog = screen.getByRole('dialog')
    const title = screen.getByRole('heading', { level: 2 })
    const content = container.querySelector('[data-dialog-test-content]')

    expect(dialog.classList.contains('min-w-0')).toBe(true)
    expect(title.parentElement?.classList.contains('min-w-0')).toBe(true)
    expect(title.classList.contains('break-words')).toBe(true)
    expect(content?.parentElement?.classList.contains('min-w-0')).toBe(true)
  })
})
