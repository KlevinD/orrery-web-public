import { fireEvent, render, screen, within } from '@testing-library/svelte'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { setLocale } from '#lib/i18n'
import { clearSession } from '#lib/stores/session.svelte'
import DiscussionPanel from './DiscussionPanel.svelte'
import { discussion } from './DiscussionPanel.fixtures'

describe('DiscussionPanel active-run controls', () => {
  beforeEach(() => {
    setLocale('en-US')
    clearSession()
  })

  it('enables an interjection while Agents are working', () => {
    render(DiscussionPanel, {
      props: {
        draft: 'continue',
        discussion: discussion({
          runtimeStatus: 'running',
          activeResponders: ['秦若兰', '陆知行']
        })
      }
    })

    expect(screen.getAllByText('秦若兰, 陆知行 are responding').length).toBeGreaterThan(0)
    expect(screen.getByRole('button', { name: 'Steer active Agent' }).hasAttribute('disabled')).toBe(false)
    expect(screen.getByPlaceholderText('Add instructions to the active run...')).toBeTruthy()
  })

  it('shows an instruction while it is being delivered to the active run', () => {
    render(DiscussionPanel, {
      props: {
        draft: '',
        discussion: discussion({ runtimeStatus: 'running', activeResponders: ['Lead Agent'] }),
        interjectBusy: true,
        pendingInterjection: { turnId: 'turn-1', runId: 'run-1', content: 'Use the new acceptance criteria' }
      }
    })

    expect(screen.getByText('Delivering to the active run')).toBeTruthy()
    expect(screen.getByText('Use the new acceptance criteria')).toBeTruthy()
  })

  it('puts a stop control beside an empty composer', async () => {
    const onAbort = vi.fn()
    render(DiscussionPanel, {
      props: {
        draft: '',
        discussion: discussion({ runtimeStatus: 'running', activeResponders: ['顾明澈'] }),
        onAbort
      }
    })

    const stopButton = screen.getByRole('button', { name: 'Abort chat' })
    await fireEvent.click(stopButton)
    expect(onAbort).toHaveBeenCalledTimes(1)
  })

  it('changes HIL approval mode from the composer selector', async () => {
    const onHILModeChange = vi.fn()
    render(DiscussionPanel, {
      props: {
        draft: '',
        discussion: discussion({ hilMode: 'manual' }),
        onHILModeChange
      }
    })

    const selector = screen.getByRole('combobox', { name: 'HIL approval mode' }) as HTMLSelectElement
    expect(selector.closest('[data-discussion-composer]')).toBeTruthy()
    expect(selector.closest('[data-discussion-header-actions]')).toBeNull()
    expect(selector.value).toBe('manual')
    expect(within(selector).getByRole('option', { name: 'Automatic' })).toBeTruthy()
    expect(within(selector).getByRole('option', { name: 'Manual' })).toBeTruthy()
    expect(within(selector).getByRole('option', { name: 'None' })).toBeTruthy()

    await fireEvent.change(selector, { target: { value: 'none' } })

    expect(onHILModeChange).toHaveBeenCalledOnce()
    expect(onHILModeChange).toHaveBeenCalledWith('none')
  })

  it('keeps the composer selector in sync with the HIL mode', async () => {
    const view = render(DiscussionPanel, {
      props: {
        draft: '',
        discussion: discussion({ hilMode: 'manual' })
      }
    })

    const selector = screen.getByRole('combobox', { name: 'HIL approval mode' }) as HTMLSelectElement
    expect(selector.value).toBe('manual')

    await view.rerender({
      draft: '',
      discussion: discussion({ hilMode: 'auto' })
    })
    expect(selector.value).toBe('auto')
  })

  it('disables the HIL selector while a change is being saved', () => {
    render(DiscussionPanel, {
      props: {
        draft: '',
        discussion: discussion({ hilMode: 'manual' }),
        hilModeBusy: true
      }
    })

    const selector = screen.getByRole('combobox', { name: 'HIL approval mode' }) as HTMLSelectElement
    expect(selector.closest('[data-discussion-hil-mode]')?.getAttribute('aria-busy')).toBe('true')
    expect(selector.disabled).toBe(true)
  })

  it('keeps HIL mode read-only for a non-owner', () => {
    const onHILModeChange = vi.fn()
    render(DiscussionPanel, {
      props: {
        draft: '',
        discussion: discussion({ hilMode: 'manual' }),
        hilModeEditable: false,
        onHILModeChange
      }
    })

    const selector = screen.getByRole('combobox', { name: 'HIL approval mode' }) as HTMLSelectElement
    expect(selector.closest('[data-discussion-hil-mode]')?.getAttribute('title')).toBe('Only the chat owner can change HIL mode')
    expect(selector.disabled).toBe(true)
    expect(onHILModeChange).not.toHaveBeenCalled()
  })
})
