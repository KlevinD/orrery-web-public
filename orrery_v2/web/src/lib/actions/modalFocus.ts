const focusableSelector = [
  'a[href]',
  'button:not([disabled])',
  'input:not([disabled])',
  'select:not([disabled])',
  'textarea:not([disabled])',
  '[tabindex]:not([tabindex="-1"])'
].join(',')

export type ModalEscapeHandler = (() => void | Promise<void>) | undefined

export function modalFocus(node: HTMLElement, onEscape?: ModalEscapeHandler) {
  const previousFocus = document.activeElement instanceof HTMLElement ? document.activeElement : null
  let active = true

  function focusableElements() {
    return Array.from(node.querySelectorAll<HTMLElement>(focusableSelector)).filter(
      (element) => element.getAttribute('aria-hidden') !== 'true'
    )
  }

  function handleKeydown(event: KeyboardEvent) {
    if (event.key === 'Escape') {
      event.preventDefault()
      event.stopPropagation()
      void onEscape?.()
      return
    }
    if (event.key !== 'Tab') return

    const elements = focusableElements()
    if (elements.length === 0) {
      event.preventDefault()
      node.focus()
      return
    }

    const first = elements[0]
    const last = elements[elements.length - 1]
    const current = document.activeElement
    if (event.shiftKey && (current === first || current === node || !node.contains(current))) {
      event.preventDefault()
      last.focus()
    } else if (!event.shiftKey && (current === last || !node.contains(current))) {
      event.preventDefault()
      first.focus()
    }
  }

  node.addEventListener('keydown', handleKeydown)
  queueMicrotask(() => {
    if (!active) return
    const preferred = node.querySelector<HTMLElement>('[autofocus]')
    ;(preferred ?? focusableElements()[0] ?? node).focus()
  })

  return {
    update(nextOnEscape?: ModalEscapeHandler) {
      onEscape = nextOnEscape
    },
    destroy() {
      active = false
      node.removeEventListener('keydown', handleKeydown)
      if (previousFocus?.isConnected) previousFocus.focus()
    }
  }
}
