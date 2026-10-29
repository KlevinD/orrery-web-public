import { fireEvent, render, screen, waitFor } from '@testing-library/svelte'
import { describe, expect, it, vi } from 'vitest'
import { setLocale } from '#lib/i18n'
import ContextPanelStub from '#lib/test/ContextPanelStub.svelte'

const gate = vi.hoisted(() => {
  let resolve!: (module: unknown) => void
  const promise = new Promise((done) => { resolve = done })
  return { promise, resolve }
})

vi.mock('./WorkspaceContextPanel.svelte', () => gate.promise)

import LazyWorkspaceContextPanel from './LazyWorkspaceContextPanel.svelte'

describe('lazy workspace context panel', () => {
  it('keeps its size and close control while loading, then forwards props to the loaded panel', async () => {
    setLocale('en-US')
    const onClose = vi.fn()
    const { container } = render(LazyWorkspaceContextPanel, { showClose: true, onClose })

    expect(screen.queryByTestId('loaded-context-panel')).toBeNull()
    expect(container.querySelector('.right-sidebar-shell.right-sidebar-rail')).toBeTruthy()
    expect(screen.getByRole('status').textContent).toContain('Loading sidebar')
    await fireEvent.click(screen.getByRole('button'))
    expect(onClose).toHaveBeenCalledTimes(1)

    gate.resolve({ default: ContextPanelStub })
    await waitFor(() => expect(screen.getByTestId('loaded-context-panel')).toBeTruthy())
    expect(screen.queryByRole('status')).toBeNull()
    await fireEvent.click(screen.getByRole('button', { name: 'Close loaded panel' }))
    expect(onClose).toHaveBeenCalledTimes(2)
  })
})
