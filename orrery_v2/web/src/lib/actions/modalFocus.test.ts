import { afterEach, describe, expect, it, vi } from 'vitest'
import { modalFocus } from './modalFocus'

afterEach(() => {
  document.body.innerHTML = ''
})

describe('modalFocus', () => {
  it('moves focus into the modal, contains tab navigation, and restores focus', async () => {
    const trigger = document.createElement('button')
    trigger.textContent = 'Open'
    const modal = document.createElement('div')
    modal.tabIndex = -1
    modal.innerHTML = '<input aria-label="Name"><button>Save</button>'
    document.body.append(trigger, modal)
    trigger.focus()

    const action = modalFocus(modal)
    await Promise.resolve()
    const input = modal.querySelector('input') as HTMLInputElement
    const save = modal.querySelector('button') as HTMLButtonElement
    expect(document.activeElement).toBe(input)

    save.focus()
    save.dispatchEvent(new KeyboardEvent('keydown', { key: 'Tab', bubbles: true, cancelable: true }))
    expect(document.activeElement).toBe(input)

    input.dispatchEvent(new KeyboardEvent('keydown', { key: 'Tab', shiftKey: true, bubbles: true, cancelable: true }))
    expect(document.activeElement).toBe(save)

    action.destroy()
    expect(document.activeElement).toBe(trigger)
  })

  it('delegates Escape to the supplied close handler', () => {
    const onEscape = vi.fn()
    const modal = document.createElement('div')
    document.body.append(modal)
    const action = modalFocus(modal, onEscape)

    modal.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true, cancelable: true }))

    expect(onEscape).toHaveBeenCalledOnce()
    action.destroy()
  })
})
